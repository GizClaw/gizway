package gizpay

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"

	"github.com/idy/gizway/internal/identity"
	"github.com/idy/gizway/internal/subscriptionkey"
)

type Config struct {
	AdminKey              []byte
	DB                    *sqlx.DB
	Verifier              *identity.Verifier
	HumanAudience         string
	ServiceAudience       string
	HMACSecret            []byte
	PlatformFeeBPS        int64
	RecheckInterval       time.Duration
	MaxOrderMetadataBytes int
	MaxCommissions        int
	ServiceAccounts       identity.ServiceAccountManager
	Now                   func() time.Time
	ZITADELIssuer         string
	ActionSigningKey      []byte
	ActionSigningKeyFile  string
	ActionSignatureMaxAge time.Duration
}

type Handler struct {
	config Config
}

func New(config Config) (*Handler, error) {
	if config.DB == nil || config.Verifier == nil || config.ServiceAccounts == nil || len(config.HMACSecret) == 0 {
		return nil, errors.New("incomplete GizPay handler configuration")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.RecheckInterval <= 0 {
		config.RecheckInterval = 5 * time.Minute
	}
	if config.MaxOrderMetadataBytes <= 0 {
		config.MaxOrderMetadataBytes = 8192
	}
	if config.MaxCommissions <= 0 {
		config.MaxCommissions = 32
	}
	if config.ActionSignatureMaxAge <= 0 {
		config.ActionSignatureMaxAge = 5 * time.Minute
	}
	return &Handler{config: config}, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/admin/v1/") {
		h.serveAdmin(w, r)
		return
	}
	if r.Method == http.MethodPost && r.URL.Path == "/webhooks/v1/zitadel/user-authenticated" {
		h.zitadelUserAuthenticated(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/account/") {
		h.serveAccount(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/service/") {
		h.serveService(w, r)
		return
	}
	errorJSON(w, http.StatusNotFound, "not_found", "resource not found")
}

func (h *Handler) serveAccount(w http.ResponseWriter, r *http.Request) {
	principal, err := h.config.Verifier.Authenticate(r, h.config.HumanAudience)
	if err != nil {
		if _, machineErr := h.config.Verifier.Authenticate(r, h.config.ServiceAudience); machineErr == nil {
			errorJSON(w, http.StatusForbidden, "human_identity_required", "human identity required")
			return
		}
		errorJSON(w, http.StatusUnauthorized, "invalid_token", "invalid token")
		return
	}
	path := r.URL.Path
	userID, accountID, err := h.lookupHuman(r, principal)
	if err != nil {
		writeHumanLookupError(w, err)
		return
	}
	switch {
	case r.Method == http.MethodGet && path == "/account/v1/accounts":
		h.listAccounts(w, userID)
	case strings.HasPrefix(path, "/account/v1/accounts/"):
		h.accountRead(w, r, accountID)
	case r.Method == http.MethodGet && path == "/account/v1/products":
		h.listProducts(w)
	case path == "/account/v1/service-accounts":
		h.serviceAccounts(w, r, userID)
	case strings.HasPrefix(path, "/account/v1/service-accounts/"):
		h.revokeServiceAccount(w, r, userID)
	case path == "/account/v1/merchants":
		h.merchants(w, r, userID)
	case strings.HasPrefix(path, "/account/v1/merchants/"):
		h.merchantResource(w, r, userID)
	case strings.HasPrefix(path, "/account/v1/products/"):
		h.productResource(w, r, userID, accountID)
	case path == "/account/v1/subscriptions":
		h.listSubscriptions(w, userID)
	case strings.HasPrefix(path, "/account/v1/subscriptions/"):
		h.subscriptionResource(w, r, userID)
	default:
		errorJSON(w, http.StatusNotFound, "not_found", "resource not found")
	}
}

func writeHumanLookupError(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		notFound(w)
		return
	}
	internal(w)
}

func (h *Handler) lookupHuman(r *http.Request, principal identity.Principal) (string, string, error) {
	var userID, accountID string
	err := h.config.DB.QueryRowxContext(r.Context(), `
		SELECT u.id,a.id FROM users u JOIN accounts a ON a.owner_user_id=u.id
		WHERE u.identity_issuer=$1 AND u.identity_subject=$2`, principal.Issuer, principal.Subject).Scan(&userID, &accountID)
	return userID, accountID, err
}

func (h *Handler) zitadelUserAuthenticated(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 64*1024+1))
	if err != nil || len(body) == 0 || len(body) > 64*1024 {
		errorJSON(w, http.StatusBadRequest, "invalid_request", "invalid ZITADEL Action payload")
		return
	}
	signingKey := h.config.ActionSigningKey
	if h.config.ActionSigningKeyFile != "" {
		if current, err := os.ReadFile(h.config.ActionSigningKeyFile); err == nil && len(current) != 0 {
			signingKey = current
		}
	}
	maximumAge := h.config.ActionSignatureMaxAge
	if maximumAge <= 0 {
		maximumAge = 5 * time.Minute
	}
	signature := r.Header.Get("ZITADEL-Signature")
	if signature == "" {
		// Keep accepting the historical OpenAPI spelling while Actions V2 uses
		// the canonical header exposed by ZITADEL's official validation helper.
		signature = r.Header.Get("X-ZITADEL-Signature")
	}
	if !validateActionSignature(body, signature, signingKey, h.config.Now().UTC(), maximumAge) {
		errorJSON(w, http.StatusUnauthorized, "invalid_webhook_signature", "invalid ZITADEL Action signature")
		return
	}
	var payload struct {
		User struct {
			ID    string          `json:"id"`
			Human json.RawMessage `json:"human"`
		} `json:"user"`
		UserInfo struct {
			Email             string `json:"email"`
			Name              string `json:"name"`
			PreferredUsername string `json:"preferred_username"`
		} `json:"userinfo"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || strings.TrimSpace(payload.User.ID) == "" {
		errorJSON(w, http.StatusBadRequest, "invalid_request", "invalid ZITADEL Action payload")
		return
	}
	if payload.User.Human == nil || string(payload.User.Human) == "null" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	displayName := strings.TrimSpace(payload.UserInfo.Name)
	if displayName == "" {
		displayName = strings.TrimSpace(payload.UserInfo.PreferredUsername)
	}
	if displayName == "" {
		displayName = payload.User.ID
	}
	if _, _, _, _, err := h.initializeIdentity(r.Context(), h.config.ZITADELIssuer, payload.User.ID, strings.TrimSpace(payload.UserInfo.Email), displayName); err != nil {
		internal(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func validateActionSignature(body []byte, header string, secret []byte, now time.Time, maximumAge time.Duration) bool {
	if len(secret) == 0 || maximumAge <= 0 {
		return false
	}
	var timestampValue, signatureValue string
	for part := range strings.SplitSeq(header, ",") {
		name, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch name {
		case "t":
			timestampValue = value
		case "v1":
			signatureValue = value
		}
	}
	seconds, err := strconv.ParseInt(timestampValue, 10, 64)
	if err != nil {
		return false
	}
	timestamp := time.Unix(seconds, 0)
	age := now.Sub(timestamp)
	if age < -maximumAge || age > maximumAge {
		return false
	}
	provided, err := hex.DecodeString(signatureValue)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(timestampValue + "."))
	_, _ = mac.Write(body)
	return hmac.Equal(provided, mac.Sum(nil))
}

func (h *Handler) initializeIdentity(ctx context.Context, issuer, subject, email, displayName string) (string, string, string, string, error) {
	if strings.TrimSpace(issuer) == "" || strings.TrimSpace(subject) == "" || strings.TrimSpace(displayName) == "" {
		return "", "", "", "", errors.New("invalid Human identity")
	}
	tx, err := h.config.DB.BeginTxx(ctx, nil)
	if err != nil {
		return "", "", "", "", err
	}
	defer tx.Rollback()
	userID := "usr_" + uuid.NewString()
	now := h.config.Now().UTC()
	_, err = tx.ExecContext(ctx, `INSERT INTO users(id,identity_issuer,identity_subject,email,display_name,status,created_at) VALUES($1,$2,$3,$4,$5,'active',$6) ON CONFLICT(identity_issuer,identity_subject) DO UPDATE SET email=EXCLUDED.email,display_name=EXCLUDED.display_name`, userID, issuer, subject, email, displayName, now)
	if err != nil {
		return "", "", "", "", err
	}
	if err = tx.GetContext(ctx, &userID, `SELECT id FROM users WHERE identity_issuer=$1 AND identity_subject=$2`, issuer, subject); err != nil {
		return "", "", "", "", err
	}
	accountID := "acct_" + uuid.NewString()
	_, err = tx.ExecContext(ctx, `INSERT INTO accounts(id,owner_user_id,status,created_at) VALUES($1,$2,'active',$3) ON CONFLICT(owner_user_id) DO NOTHING`, accountID, userID, now)
	if err != nil {
		return "", "", "", "", err
	}
	if err = tx.GetContext(ctx, &accountID, `SELECT id FROM accounts WHERE owner_user_id=$1`, userID); err != nil {
		return "", "", "", "", err
	}
	ledgerID := "ledger_" + uuid.NewString()
	_, err = tx.ExecContext(ctx, `INSERT INTO ledger_accounts(id,owner_account_id,asset_code,status) VALUES($1,$2,'credit','active') ON CONFLICT(owner_account_id,asset_code) DO NOTHING`, ledgerID, accountID)
	if err != nil {
		return "", "", "", "", err
	}
	if err = tx.GetContext(ctx, &ledgerID, `SELECT id FROM ledger_accounts WHERE owner_account_id=$1 AND asset_code='credit'`, accountID); err != nil {
		return "", "", "", "", err
	}
	merchantID := "merchant_" + uuid.NewString()
	_, err = tx.ExecContext(ctx, `INSERT INTO merchants(id,settlement_account_id,legal_name,public_name,is_default,status,created_at,updated_at) VALUES($1,$2,$3,$3,true,'active',$4,$4) ON CONFLICT DO NOTHING`, merchantID, accountID, displayName, now)
	if err != nil {
		return "", "", "", "", err
	}
	if err = tx.GetContext(ctx, &merchantID, `SELECT id FROM merchants WHERE settlement_account_id=$1 AND is_default=true`, accountID); err != nil {
		return "", "", "", "", err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO client_sync.user_profiles(id,owner_identity_issuer,owner_identity_subject,email,display_name,merchant_id,status,created_at) VALUES($1,$2,$3,$4,$5,$6,'active',$7) ON CONFLICT(id) DO UPDATE SET email=EXCLUDED.email,display_name=EXCLUDED.display_name,merchant_id=EXCLUDED.merchant_id,status=EXCLUDED.status`, userID, issuer, subject, email, displayName, merchantID, now)
	if err != nil {
		return "", "", "", "", err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO client_sync.account_balances(id,account_id,owner_identity_issuer,owner_identity_subject,balance_microcredits) VALUES($1,$1,$2,$3,0) ON CONFLICT(account_id) DO NOTHING`, accountID, issuer, subject)
	if err != nil {
		return "", "", "", "", err
	}
	if err = tx.Commit(); err != nil {
		return "", "", "", "", err
	}
	return userID, accountID, ledgerID, merchantID, nil
}

func (h *Handler) listAccounts(w http.ResponseWriter, userID string) {
	rows, err := h.queryMaps(`SELECT id,status,created_at FROM accounts WHERE owner_user_id=$1 ORDER BY created_at,id`, userID)
	if err != nil {
		internal(w)
		return
	}
	data := rowsToMaps(rows)
	for i := range data {
		data[i]["kind"] = "personal"
		data[i]["asset"] = "GIZ_CREDIT"
		data[i]["ledger_accounts"] = []any{map[string]any{"asset_code": "GIZ_CREDIT"}}
	}
	writeJSON(w, 200, map[string]any{"data": data})
}

func (h *Handler) accountRead(w http.ResponseWriter, r *http.Request, ownedID string) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 5 || parts[3] != ownedID {
		notFound(w)
		return
	}
	switch parts[4] {
	case "balance":
		if r.Method != http.MethodGet {
			notFound(w)
			return
		}
		var balance int64
		if h.config.DB.Get(&balance, `SELECT balance_microcredits FROM account_balances WHERE account_id=$1`, ownedID) != nil {
			notFound(w)
			return
		}
		writeJSON(w, 200, map[string]any{"account_id": ownedID, "balance_microcredits": balance})
	case "charges":
		if r.Method != http.MethodGet {
			notFound(w)
			return
		}
		h.listAccountCharges(w, ownedID)
	case "transactions":
		if r.Method != http.MethodGet {
			notFound(w)
			return
		}
		h.listTransactions(w, ownedID, r.URL.Query().Get("ledger_transaction_id"))
	case "topups":
		h.accountTopups(w, r, ownedID)
	default:
		notFound(w)
	}
}

func (h *Handler) accountTopups(w http.ResponseWriter, r *http.Request, accountID string) {
	if r.Method == http.MethodGet {
		rows, err := h.queryMaps(`SELECT * FROM topups WHERE account_id=$1 ORDER BY created_at,id`, accountID)
		if err != nil {
			internal(w)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": rowsToMaps(rows)})
		return
	}
	if r.Method != http.MethodPost {
		notFound(w)
		return
	}
	var body struct {
		ID                string `json:"id"`
		Channel           string `json:"channel"`
		ExternalReference string `json:"external_reference"`
		Amount            int64  `json:"amount_microcredits"`
	}
	if decode(r, &body) != nil || strings.TrimSpace(body.ID) == "" || body.Channel != "fake" || strings.TrimSpace(body.ExternalReference) == "" || body.Amount <= 0 {
		invalid(w)
		return
	}
	var existing struct {
		ID, AccountID, Channel, ExternalReference, Status, LedgerTransactionID string
		Amount                                                                 int64
		CreatedAt, CreditedAt                                                  time.Time
	}
	err := h.config.DB.QueryRowxContext(r.Context(), `SELECT id,account_id,channel,external_reference,amount_microcredits,status,ledger_transaction_id,created_at,credited_at FROM topups WHERE id=$1`, body.ID).Scan(&existing.ID, &existing.AccountID, &existing.Channel, &existing.ExternalReference, &existing.Amount, &existing.Status, &existing.LedgerTransactionID, &existing.CreatedAt, &existing.CreditedAt)
	if err == nil {
		if existing.AccountID != accountID || existing.Channel != body.Channel || existing.ExternalReference != body.ExternalReference || existing.Amount != body.Amount {
			errorJSON(w, http.StatusConflict, "resource_id_conflict", "Top-up ID already exists with different content")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": existing.ID, "account_id": existing.AccountID, "channel": existing.Channel, "external_reference": existing.ExternalReference, "amount_microcredits": existing.Amount, "status": existing.Status, "ledger_transaction_id": existing.LedgerTransactionID, "created_at": existing.CreatedAt, "credited_at": existing.CreditedAt})
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		internal(w)
		return
	}
	tx, err := h.config.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		internalError(w, err, "begin Charge transaction")
		return
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "topup-id:"+body.ID); err != nil {
		internal(w)
		return
	}
	if _, err = tx.Exec(`SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "topup-ref:"+body.Channel+":"+body.ExternalReference); err != nil {
		internal(w)
		return
	}
	err = tx.QueryRowxContext(r.Context(), `SELECT id,account_id,channel,external_reference,amount_microcredits,status,ledger_transaction_id,created_at,credited_at FROM topups WHERE id=$1`, body.ID).Scan(&existing.ID, &existing.AccountID, &existing.Channel, &existing.ExternalReference, &existing.Amount, &existing.Status, &existing.LedgerTransactionID, &existing.CreatedAt, &existing.CreditedAt)
	if err == nil {
		if existing.AccountID != accountID || existing.Channel != body.Channel || existing.ExternalReference != body.ExternalReference || existing.Amount != body.Amount {
			errorJSON(w, http.StatusConflict, "resource_id_conflict", "Top-up ID already exists with different content")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": existing.ID, "account_id": existing.AccountID, "channel": existing.Channel, "external_reference": existing.ExternalReference, "amount_microcredits": existing.Amount, "status": existing.Status, "ledger_transaction_id": existing.LedgerTransactionID, "created_at": existing.CreatedAt, "credited_at": existing.CreditedAt})
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		internal(w)
		return
	}
	var exists bool
	if err = tx.Get(&exists, `SELECT EXISTS(SELECT 1 FROM topups WHERE channel=$1 AND external_reference=$2)`, body.Channel, body.ExternalReference); err != nil {
		internal(w)
		return
	}
	if exists {
		errorJSON(w, http.StatusConflict, "duplicate_external_reference", "Top-up external reference already exists")
		return
	}
	var ledgerID, issuer, subject string
	if err = tx.QueryRowx(`SELECT l.id,u.identity_issuer,u.identity_subject FROM ledger_accounts l JOIN accounts a ON a.id=l.owner_account_id JOIN users u ON u.id=a.owner_user_id WHERE a.id=$1 AND l.asset_code='credit' FOR SHARE`, accountID).Scan(&ledgerID, &issuer, &subject); err != nil {
		notFound(w)
		return
	}
	transactionID := "txn_" + uuid.NewString()
	topupID := body.ID
	now := h.config.Now().UTC()
	if _, err = tx.Exec(`INSERT INTO ledger_transactions(id,transaction_type,status,created_at) VALUES($1,'topup','pending',$2)`, transactionID, now); err != nil {
		internal(w)
		return
	}
	if err = insertEntry(tx, transactionID, "led_clearing", "debit", body.Amount); err != nil {
		internal(w)
		return
	}
	if err = insertEntry(tx, transactionID, ledgerID, "credit", body.Amount); err != nil {
		internal(w)
		return
	}
	if _, err = tx.Exec(`UPDATE ledger_transactions SET status='posted' WHERE id=$1`, transactionID); err != nil {
		internal(w)
		return
	}
	if _, err = tx.Exec(`INSERT INTO topups(id,account_id,channel,external_reference,amount_microcredits,status,ledger_transaction_id,created_at,credited_at) VALUES($1,$2,$3,$4,$5,'succeeded',$6,$7,$7)`, topupID, accountID, body.Channel, body.ExternalReference, body.Amount, transactionID, now); err != nil {
		var pqError *pq.Error
		if errors.As(err, &pqError) && pqError.Code == "23505" {
			errorJSON(w, http.StatusConflict, "duplicate_external_reference", "Top-up external reference already exists")
		} else {
			internal(w)
		}
		return
	}
	if _, err = tx.Exec(`UPDATE client_sync.account_balances SET balance_microcredits=balance_microcredits+$1 WHERE account_id=$2`, body.Amount, accountID); err != nil {
		internal(w)
		return
	}
	if _, err = tx.Exec(`INSERT INTO client_sync.transactions(id,account_id,owner_identity_issuer,owner_identity_subject,transaction_type,amount_microcredits,created_at) VALUES($1,$2,$3,$4,'topup',$5,$6)`, transactionID, accountID, issuer, subject, body.Amount, now); err != nil {
		internal(w)
		return
	}
	if err = tx.Commit(); err != nil {
		internal(w)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": topupID, "account_id": accountID, "channel": body.Channel,
		"external_reference": body.ExternalReference, "amount_microcredits": body.Amount,
		"status": "succeeded", "ledger_transaction_id": transactionID,
		"created_at": now, "credited_at": now,
	})
}

func (h *Handler) serviceAccounts(w http.ResponseWriter, r *http.Request, userID string) {
	if r.Method == http.MethodGet {
		rows, err := h.queryMaps(`SELECT id,name,roles,status,created_at,revoked_at FROM service_principals WHERE owner_user_id=$1 ORDER BY created_at,id`, userID)
		if err != nil {
			internal(w)
			return
		}
		writeJSON(w, 200, map[string]any{"data": rowsToMaps(rows)})
		return
	}
	if r.Method != http.MethodPost {
		notFound(w)
		return
	}
	var body struct {
		Name  string   `json:"name"`
		Roles []string `json:"roles"`
	}
	if decode(r, &body) != nil {
		invalid(w)
		return
	}
	if strings.TrimSpace(body.Name) == "" || !validServiceAccountRoles(body.Roles) {
		errorJSON(w, http.StatusBadRequest, "invalid_service_account", "name and supported roles are required")
		return
	}
	credential, err := h.config.ServiceAccounts.Create(r.Context(), body.Name, body.Roles)
	if err != nil {
		errorJSON(w, http.StatusBadGateway, "identity_provider_unavailable", "could not create Service Account")
		return
	}
	id := "svc_" + uuid.NewString()
	roles, _ := json.Marshal(body.Roles)
	_, err = h.config.DB.Exec(`INSERT INTO service_principals(id,owner_user_id,identity_issuer,identity_subject,credential_key_id,name,roles,status,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,'active',$8)`, id, userID, h.config.Verifier.Issuer(), credential.Subject, credential.KeyID, body.Name, roles, h.config.Now().UTC())
	if err != nil {
		_ = h.config.ServiceAccounts.RevokeCredential(r.Context(), credential.Subject, credential.KeyID)
		internal(w)
		return
	}
	writeJSON(w, 201, map[string]any{
		"id": id, "name": body.Name, "roles": body.Roles, "status": "active",
		"credential": map[string]any{"type": "private_key_jwt", "key": credential.KeyJSON},
	})
}

func validServiceAccountRoles(roles []string) bool {
	if len(roles) == 0 {
		return false
	}
	allowed := map[string]bool{"credit_check": true, "charge": true}
	seen := map[string]bool{}
	for _, role := range roles {
		if !allowed[role] || seen[role] {
			return false
		}
		seen[role] = true
	}
	return true
}

func (h *Handler) revokeServiceAccount(w http.ResponseWriter, r *http.Request, userID string) {
	if r.Method != http.MethodDelete {
		notFound(w)
		return
	}
	id := r.PathValue("service_account_id")
	tx, err := h.config.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		internal(w)
		return
	}
	defer tx.Rollback()
	var subject, keyID string
	if err := tx.QueryRowx(`SELECT identity_subject,COALESCE(credential_key_id,'') FROM service_principals WHERE id=$1 AND owner_user_id=$2 AND status='active' FOR UPDATE`, id, userID).Scan(&subject, &keyID); err != nil {
		notFound(w)
		return
	}
	if keyID == "" || h.config.ServiceAccounts.RevokeCredential(r.Context(), subject, keyID) != nil {
		errorJSON(w, http.StatusBadGateway, "identity_provider_unavailable", "could not revoke Service Account credential")
		return
	}
	if _, err := tx.Exec(`UPDATE service_principals SET status='revoked',revoked_at=$1 WHERE id=$2`, h.config.Now().UTC(), id); err != nil {
		internal(w)
		return
	}
	if err := tx.Commit(); err != nil {
		internal(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) merchants(w http.ResponseWriter, r *http.Request, userID string) {
	if r.Method == http.MethodGet {
		rows, err := h.queryMaps(`SELECT m.* FROM merchants m JOIN accounts a ON a.id=m.settlement_account_id WHERE a.owner_user_id=$1 ORDER BY m.created_at,m.id`, userID)
		if err != nil {
			internal(w)
			return
		}
		writeJSON(w, 200, map[string]any{"data": rowsToMaps(rows)})
		return
	}
	if r.Method != http.MethodPost {
		notFound(w)
		return
	}
	var body struct {
		SettlementAccountID string `json:"settlement_account_id"`
		LegalName           string `json:"legal_name"`
		PublicName          string `json:"public_name"`
	}
	if decode(r, &body) != nil {
		invalid(w)
		return
	}
	if strings.TrimSpace(body.SettlementAccountID) == "" || strings.TrimSpace(body.LegalName) == "" || strings.TrimSpace(body.PublicName) == "" {
		invalid(w)
		return
	}
	var owned bool
	_ = h.config.DB.Get(&owned, `SELECT EXISTS(SELECT 1 FROM accounts WHERE id=$1 AND owner_user_id=$2)`, body.SettlementAccountID, userID)
	if !owned {
		notFound(w)
		return
	}
	id := "mrc_" + uuid.NewString()
	now := h.config.Now().UTC()
	_, err := h.config.DB.Exec(`INSERT INTO merchants(id,settlement_account_id,legal_name,public_name,is_default,status,created_at,updated_at) VALUES($1,$2,$3,$4,false,'active',$5,$5)`, id, body.SettlementAccountID, body.LegalName, body.PublicName, now)
	if err != nil {
		internal(w)
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "settlement_account_id": body.SettlementAccountID, "legal_name": body.LegalName, "public_name": body.PublicName, "is_default": false, "status": "active"})
}

func (h *Handler) merchantResource(w http.ResponseWriter, r *http.Request, userID string) {
	id := r.PathValue("merchant_id")
	row, err := h.queryMap(`SELECT m.* FROM merchants m JOIN accounts a ON a.id=m.settlement_account_id WHERE m.id=$1 AND a.owner_user_id=$2`, id, userID)
	if err != nil {
		notFound(w)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/products") {
		h.merchantProducts(w, r, id)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPatch {
		notFound(w)
		return
	}
	if r.Method == http.MethodPatch {
		var body struct {
			PublicName *string `json:"public_name"`
			Status     *string `json:"status"`
		}
		if decode(r, &body) != nil {
			invalid(w)
			return
		}
		if body.PublicName == nil && body.Status == nil || body.PublicName != nil && strings.TrimSpace(*body.PublicName) == "" {
			invalid(w)
			return
		}
		if body.Status != nil && *body.Status != "active" && *body.Status != "inactive" {
			invalid(w)
			return
		}
		if body.Status != nil && *body.Status == "inactive" {
			isDefault, _ := row["is_default"].(bool)
			if isDefault {
				errorJSON(w, http.StatusConflict, "default_merchant_in_use", "the default Merchant cannot be disabled")
				return
			}
		}
		if body.PublicName != nil {
			row["public_name"] = *body.PublicName
		}
		if body.Status != nil {
			row["status"] = *body.Status
		}
		_, err = h.config.DB.Exec(`UPDATE merchants SET public_name=$1,status=$2,updated_at=$3 WHERE id=$4`, row["public_name"], row["status"], h.config.Now().UTC(), id)
		if err != nil {
			internal(w)
			return
		}
	}
	writeJSON(w, 200, row)
}

func (h *Handler) merchantProducts(w http.ResponseWriter, r *http.Request, merchantID string) {
	if r.Method == http.MethodGet {
		rows, err := h.queryMaps(`SELECT * FROM products WHERE merchant_id=$1 ORDER BY created_at,id`, merchantID)
		if err != nil {
			internal(w)
			return
		}
		writeJSON(w, 200, map[string]any{"data": rowsToMaps(rows)})
		return
	}
	if r.Method != http.MethodPost {
		notFound(w)
		return
	}
	var body struct {
		Name         string `json:"name"`
		BillingMode  string `json:"billing_mode"`
		TermsVersion string `json:"terms_version"`
	}
	if decode(r, &body) != nil {
		invalid(w)
		return
	}
	if body.BillingMode != "pay_as_you_go" {
		errorJSON(w, 400, "unsupported_billing_mode", "only pay_as_you_go is supported")
		return
	}
	if strings.TrimSpace(body.Name) == "" || strings.TrimSpace(body.TermsVersion) == "" {
		invalid(w)
		return
	}
	id := "prd_" + uuid.NewString()
	now := h.config.Now().UTC()
	_, err := h.config.DB.Exec(`INSERT INTO products(id,merchant_id,name,billing_mode,published,status,terms_version,created_at,updated_at) VALUES($1,$2,$3,$4,true,'active',$5,$6,$6)`, id, merchantID, body.Name, body.BillingMode, body.TermsVersion, now)
	if err != nil {
		internal(w)
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "merchant_id": merchantID, "name": body.Name, "billing_mode": body.BillingMode, "status": "active", "terms_version": body.TermsVersion})
}

func (h *Handler) listProducts(w http.ResponseWriter) {
	rows, err := h.queryMaps(`SELECT * FROM products WHERE status='active' AND published=true ORDER BY created_at,id`)
	if err != nil {
		internal(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": rowsToMaps(rows)})
}

func (h *Handler) productResource(w http.ResponseWriter, r *http.Request, userID, accountID string) {
	id := r.PathValue("product_id")
	if strings.HasSuffix(r.URL.Path, "/subscriptions") {
		h.createSubscription(w, r, id, userID, accountID)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPatch {
		notFound(w)
		return
	}
	if r.Method == http.MethodGet {
		row, err := h.queryMap(`SELECT * FROM products WHERE id=$1 AND status='active' AND published=true`, id)
		if err != nil {
			notFound(w)
			return
		}
		writeJSON(w, http.StatusOK, row)
		return
	}
	row, err := h.queryMap(`SELECT p.* FROM products p JOIN merchants m ON m.id=p.merchant_id JOIN accounts a ON a.id=m.settlement_account_id WHERE p.id=$1 AND a.owner_user_id=$2`, id, userID)
	if err != nil {
		notFound(w)
		return
	}
	var body struct {
		Name   *string `json:"name"`
		Status *string `json:"status"`
	}
	if decode(r, &body) != nil {
		invalid(w)
		return
	}
	if body.Name == nil && body.Status == nil || body.Name != nil && strings.TrimSpace(*body.Name) == "" {
		invalid(w)
		return
	}
	if body.Status != nil && *body.Status != "active" && *body.Status != "inactive" {
		invalid(w)
		return
	}
	if body.Name != nil {
		row["name"] = *body.Name
	}
	if body.Status != nil {
		row["status"] = *body.Status
	}
	_, err = h.config.DB.Exec(`UPDATE products SET name=$1,status=$2,updated_at=$3 WHERE id=$4`, row["name"], row["status"], h.config.Now().UTC(), id)
	if err != nil {
		invalid(w)
		return
	}
	writeJSON(w, 200, row)
}

func (h *Handler) createSubscription(w http.ResponseWriter, r *http.Request, productID, userID, accountID string) {
	if r.Method != http.MethodPost {
		notFound(w)
		return
	}
	var body struct {
		ID           string `json:"id"`
		AccountID    string `json:"account_id"`
		TermsVersion string `json:"terms_version"`
	}
	if decode(r, &body) != nil || strings.TrimSpace(body.ID) == "" {
		invalid(w)
		return
	}
	if body.AccountID != accountID {
		notFound(w)
		return
	}
	var existing struct {
		ID           string `db:"id"`
		AccountID    string `db:"account_id"`
		ProductID    string `db:"product_id"`
		Status       string `db:"status"`
		TermsVersion string `db:"terms_version"`
	}
	err := h.config.DB.GetContext(r.Context(), &existing, `SELECT id,account_id,product_id,status,terms_version FROM subscriptions WHERE id=$1`, body.ID)
	if err == nil {
		if existing.AccountID != body.AccountID || existing.ProductID != productID || existing.TermsVersion != body.TermsVersion {
			errorJSON(w, http.StatusConflict, "resource_id_conflict", "Subscription ID already exists with different content")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": existing.ID, "account_id": existing.AccountID, "product_id": existing.ProductID, "status": existing.Status, "terms_version": existing.TermsVersion})
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		internal(w)
		return
	}
	var terms string
	if h.config.DB.Get(&terms, `SELECT terms_version FROM products WHERE id=$1 AND status='active' AND published=true`, productID) != nil {
		notFound(w)
		return
	}
	if strings.TrimSpace(body.TermsVersion) == "" {
		invalid(w)
		return
	}
	if body.TermsVersion != terms {
		errorJSON(w, http.StatusConflict, "terms_version_mismatch", "Product terms must be accepted at their current version")
		return
	}
	var otherID string
	err = h.config.DB.GetContext(r.Context(), &otherID, `SELECT id FROM subscriptions WHERE account_id=$1 AND product_id=$2`, accountID, productID)
	if err == nil {
		if otherID == body.ID {
			if err = h.config.DB.GetContext(r.Context(), &existing, `SELECT id,account_id,product_id,status,terms_version FROM subscriptions WHERE id=$1`, body.ID); err != nil {
				internal(w)
				return
			}
			if existing.AccountID != body.AccountID || existing.ProductID != productID || existing.TermsVersion != body.TermsVersion {
				errorJSON(w, http.StatusConflict, "resource_id_conflict", "Subscription ID already exists with different content")
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"id": existing.ID, "account_id": existing.AccountID, "product_id": existing.ProductID, "status": existing.Status, "terms_version": existing.TermsVersion})
			return
		}
		errorJSON(w, http.StatusConflict, "subscription_already_exists", "The account already has this subscription")
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		internal(w)
		return
	}
	id := body.ID
	now := h.config.Now().UTC()
	result, err := h.config.DB.Exec(`INSERT INTO subscriptions(id,account_id,product_id,status,terms_version,accepted_at,created_at) VALUES($1,$2,$3,'active',$4,$5,$5) ON CONFLICT DO NOTHING`, id, accountID, productID, body.TermsVersion, now)
	if err != nil {
		internal(w)
		return
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		if err = h.config.DB.GetContext(r.Context(), &existing, `SELECT id,account_id,product_id,status,terms_version FROM subscriptions WHERE id=$1`, body.ID); err == nil {
			if existing.AccountID != body.AccountID || existing.ProductID != productID || existing.TermsVersion != body.TermsVersion {
				errorJSON(w, http.StatusConflict, "resource_id_conflict", "Subscription ID already exists with different content")
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"id": existing.ID, "account_id": existing.AccountID, "product_id": existing.ProductID, "status": existing.Status, "terms_version": existing.TermsVersion})
			return
		}
		errorJSON(w, http.StatusConflict, "subscription_already_exists", "The account already has this subscription")
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "account_id": accountID, "product_id": productID, "status": "active", "terms_version": body.TermsVersion})
}

func (h *Handler) listSubscriptions(w http.ResponseWriter, userID string) {
	rows, err := h.queryMaps(`SELECT s.* FROM subscriptions s JOIN accounts a ON a.id=s.account_id WHERE a.owner_user_id=$1 ORDER BY s.created_at,s.id`, userID)
	if err != nil {
		internal(w)
		return
	}
	writeJSON(w, 200, map[string]any{"data": rowsToMaps(rows)})
}

func (h *Handler) subscriptionResource(w http.ResponseWriter, r *http.Request, userID string) {
	id := r.PathValue("subscription_id")
	row, err := h.queryMap(`SELECT s.* FROM subscriptions s JOIN accounts a ON a.id=s.account_id WHERE s.id=$1 AND a.owner_user_id=$2`, id, userID)
	if err != nil {
		notFound(w)
		return
	}
	if strings.Contains(r.URL.Path, "/keys") {
		h.subscriptionKeys(w, r, id)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPatch {
		notFound(w)
		return
	}
	if r.Method == http.MethodPatch {
		var body struct {
			Status string `json:"status"`
		}
		if decode(r, &body) != nil {
			invalid(w)
			return
		}
		if body.Status != "active" && body.Status != "paused" && body.Status != "inactive" {
			invalid(w)
			return
		}
		result, err := h.config.DB.Exec(`UPDATE subscriptions SET status=$1 WHERE id=$2`, body.Status, id)
		if err != nil {
			invalid(w)
			return
		}
		n, _ := result.RowsAffected()
		if n == 0 {
			notFound(w)
			return
		}
		row["status"] = body.Status
	}
	writeJSON(w, 200, row)
}

func (h *Handler) subscriptionKeys(w http.ResponseWriter, r *http.Request, subscriptionID string) {
	keyID := r.PathValue("subscription_key_id")
	if keyID != "" {
		if strings.HasSuffix(r.URL.Path, "/revoke") {
			if r.Method != http.MethodPost {
				notFound(w)
				return
			}
			result, err := h.config.DB.Exec(`UPDATE subscription_keys SET status='revoked',revoked_at=$1 WHERE id=$2 AND subscription_id=$3 AND status='active'`, h.config.Now().UTC(), keyID, subscriptionID)
			if err != nil {
				internal(w)
				return
			}
			n, _ := result.RowsAffected()
			if n == 0 {
				var exists bool
				if err = h.config.DB.Get(&exists, `SELECT EXISTS(SELECT 1 FROM subscription_keys WHERE id=$1 AND subscription_id=$2 AND status='revoked')`, keyID, subscriptionID); err != nil {
					internal(w)
					return
				}
				if exists {
					h.writeKey(w, keyID, subscriptionID)
				} else {
					notFound(w)
				}
				return
			}
			h.writeKey(w, keyID, subscriptionID)
			return
		}
		if r.Method != http.MethodGet {
			notFound(w)
			return
		}
		h.writeKey(w, keyID, subscriptionID)
		return
	}
	if r.Method == http.MethodGet {
		var ids []string
		if h.config.DB.Select(&ids, `SELECT id FROM subscription_keys WHERE subscription_id=$1 ORDER BY created_at,id`, subscriptionID) != nil {
			internal(w)
			return
		}
		data := make([]any, 0, len(ids))
		for _, id := range ids {
			item, err := h.keyMap(id, subscriptionID)
			if err == nil {
				data = append(data, item)
			}
		}
		writeJSON(w, 200, map[string]any{"data": data})
		return
	}
	if r.Method != http.MethodPost {
		notFound(w)
		return
	}
	var body struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if decode(r, &body) != nil || strings.TrimSpace(body.ID) == "" || strings.TrimSpace(body.Name) == "" {
		invalid(w)
		return
	}
	var existing struct {
		ID             string `db:"id"`
		SubscriptionID string `db:"subscription_id"`
		Name           string `db:"name"`
	}
	err := h.config.DB.GetContext(r.Context(), &existing, `SELECT id,subscription_id,name FROM subscription_keys WHERE id=$1`, body.ID)
	if err == nil {
		if existing.SubscriptionID != subscriptionID || existing.Name != body.Name {
			errorJSON(w, http.StatusConflict, "resource_id_conflict", "Subscription Key ID already exists with different content")
			return
		}
		h.writeKey(w, body.ID, subscriptionID)
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		internal(w)
		return
	}
	random, err := randomToken(rand.Reader, 24)
	if err != nil {
		internal(w)
		return
	}
	raw := "giz_sk_" + random
	id := body.ID
	result, err := h.config.DB.Exec(`INSERT INTO subscription_keys(id,subscription_id,name,key,subscription_key_hmac,status,created_at) VALUES($1,$2,$3,$4,$5,'active',$6) ON CONFLICT DO NOTHING`, id, subscriptionID, body.Name, raw, subscriptionkey.HMAC(h.config.HMACSecret, raw), h.config.Now().UTC())
	if err != nil {
		internal(w)
		return
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		if err = h.config.DB.GetContext(r.Context(), &existing, `SELECT id,subscription_id,name FROM subscription_keys WHERE id=$1`, body.ID); err != nil {
			internal(w)
			return
		}
		if existing.SubscriptionID != subscriptionID || existing.Name != body.Name {
			errorJSON(w, http.StatusConflict, "resource_id_conflict", "Subscription Key ID already exists with different content")
			return
		}
		h.writeKey(w, body.ID, subscriptionID)
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "subscription_id": subscriptionID, "name": body.Name, "key": raw, "status": "active"})
}

func (h *Handler) writeKey(w http.ResponseWriter, id, subscriptionID string) {
	item, err := h.keyMap(id, subscriptionID)
	if err != nil {
		notFound(w)
		return
	}
	writeJSON(w, 200, item)
}
func (h *Handler) keyMap(id, subscriptionID string) (map[string]any, error) {
	var row struct {
		ID             string     `db:"id"`
		SubscriptionID string     `db:"subscription_id"`
		Name           string     `db:"name"`
		Key            string     `db:"key"`
		Status         string     `db:"status"`
		CreatedAt      time.Time  `db:"created_at"`
		LastUsedAt     *time.Time `db:"last_used_at"`
		RevokedAt      *time.Time `db:"revoked_at"`
	}
	err := h.config.DB.Get(&row, `SELECT id,subscription_id,name,key,status,created_at,last_used_at,revoked_at FROM subscription_keys WHERE id=$1 AND subscription_id=$2`, id, subscriptionID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": row.ID, "subscription_id": row.SubscriptionID, "name": row.Name, "key": row.Key, "status": row.Status, "created_at": row.CreatedAt, "last_used_at": row.LastUsedAt, "revoked_at": row.RevokedAt}, nil
}

func (h *Handler) serveService(w http.ResponseWriter, r *http.Request) {
	principal, err := h.config.Verifier.Authenticate(r, h.config.ServiceAudience)
	if err != nil {
		if _, humanErr := h.config.Verifier.Authenticate(r, h.config.HumanAudience); humanErr == nil {
			errorJSON(w, 403, "insufficient_role", "machine role required")
			return
		}
		errorJSON(w, 401, "invalid_token", "invalid token")
		return
	}
	var status string
	var principalID string
	err = h.config.DB.QueryRowx(`SELECT id,status FROM service_principals WHERE identity_issuer=$1 AND identity_subject=$2`, principal.Issuer, principal.Subject).Scan(&principalID, &status)
	if err != nil || status != "active" {
		errorJSON(w, 401, "service_principal_revoked", "service principal is not active")
		return
	}
	if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "subscription-credit-checks") {
		if !principal.Roles["credit_check"] {
			errorJSON(w, 403, "insufficient_role", "missing credit_check role")
			return
		}
		h.creditCheck(w, r)
		return
	}
	if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "payg-charges") {
		if !principal.Roles["charge"] {
			errorJSON(w, 403, "insufficient_role", "missing charge role")
			return
		}
		h.createCharge(w, r, principalID)
		return
	}
	if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/payg-charges/") {
		if !principal.Roles["charge"] {
			errorJSON(w, 403, "insufficient_role", "missing charge role")
			return
		}
		h.getCharge(w, r.PathValue("external_order_id"))
		return
	}
	notFound(w)
}

func (h *Handler) creditCheck(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SubscriptionKeyHMAC string `json:"subscription_key_hmac"`
	}
	if decode(r, &body) != nil || strings.TrimSpace(body.SubscriptionKeyHMAC) == "" {
		invalid(w)
		return
	}
	var row struct {
		SubscriptionKeyID  string `db:"subscription_key_id"`
		AccountID          string `db:"account_id"`
		SubscriptionID     string `db:"subscription_id"`
		ProductID          string `db:"product_id"`
		BillingMode        string `db:"billing_mode"`
		OwnerIssuer        string `db:"owner_identity_issuer"`
		OwnerSubject       string `db:"owner_identity_subject"`
		KeyStatus          string `db:"key_status"`
		SubscriptionStatus string `db:"subscription_status"`
		ProductStatus      string `db:"product_status"`
		Balance            int64  `db:"balance"`
	}
	err := h.config.DB.Get(&row, `SELECT k.id subscription_key_id,s.account_id,s.id subscription_id,p.id product_id,p.billing_mode,u.identity_issuer owner_identity_issuer,u.identity_subject owner_identity_subject,k.status key_status,s.status subscription_status,p.status product_status,b.balance_microcredits balance FROM subscription_keys k JOIN subscriptions s ON s.id=k.subscription_id JOIN products p ON p.id=s.product_id JOIN accounts a ON a.id=s.account_id JOIN users u ON u.id=a.owner_user_id JOIN account_balances b ON b.account_id=s.account_id WHERE k.subscription_key_hmac=$1`, body.SubscriptionKeyHMAC)
	if err != nil || row.KeyStatus != "active" {
		errorJSON(w, http.StatusUnauthorized, "invalid_subscription_key", "Subscription Key is unknown or revoked")
		return
	}
	allowed := row.SubscriptionStatus == "active" && row.ProductStatus == "active" && row.Balance > 0
	available := int64(0)
	if allowed {
		available = row.Balance
	}
	result := map[string]any{
		"status": "denied", "available_microcredits": available,
		"account_id": row.AccountID, "subscription_id": row.SubscriptionID,
		"subscription_key_id": row.SubscriptionKeyID,
		"product_id":          row.ProductID, "billing_mode": row.BillingMode,
		"owner_identity_issuer": row.OwnerIssuer, "owner_identity_subject": row.OwnerSubject,
		"checked_at": h.config.Now().UTC(), "recheck_after_seconds": int64(h.config.RecheckInterval / time.Second),
	}
	if allowed {
		result["status"] = "allowed"
	}
	writeJSON(w, 200, result)
}

type commissionRequest struct {
	MerchantID string `json:"merchant_id"`
	Amount     int64  `json:"amount_microcredits"`
}
type chargeRequest struct {
	ExternalOrderID     string              `json:"external_order_id"`
	SubscriptionKeyHMAC string              `json:"subscription_key_hmac"`
	Gross               int64               `json:"gross_microcredits"`
	Commissions         []commissionRequest `json:"commissions"`
	Order               json.RawMessage     `json:"order"`
	Started             time.Time           `json:"service_started_at"`
	Completed           time.Time           `json:"service_completed_at"`
}

func (h *Handler) createCharge(w http.ResponseWriter, r *http.Request, principalID string) {
	var body chargeRequest
	if decode(r, &body) != nil {
		invalid(w)
		return
	}
	if strings.TrimSpace(body.ExternalOrderID) == "" {
		errorJSON(w, 400, "invalid_external_order_id", "external order ID is required")
		return
	}
	if strings.TrimSpace(body.SubscriptionKeyHMAC) == "" {
		errorJSON(w, 400, "invalid_subscription_key_hmac", "Subscription Key HMAC is required")
		return
	}
	if body.Gross <= 0 {
		errorJSON(w, 400, "invalid_gross_microcredits", "gross must be positive")
		return
	}
	if body.Started.IsZero() || body.Completed.IsZero() || body.Completed.Before(body.Started) {
		errorJSON(w, 400, "invalid_service_time_range", "completion precedes start")
		return
	}
	if len(body.Commissions) > h.config.MaxCommissions {
		errorJSON(w, 400, "too_many_commissions", "commission limit exceeded")
		return
	}
	if err := validateOrderMetadata(body.Order, h.config.MaxOrderMetadataBytes); err != nil {
		errorJSON(w, 400, "invalid_order_metadata", err.Error())
		return
	}
	seen := map[string]bool{}
	sum := int64(0)
	for _, c := range body.Commissions {
		if strings.TrimSpace(c.MerchantID) == "" || c.Amount < 0 {
			errorJSON(w, 400, "invalid_commission_amount", "commission must be nonnegative")
			return
		}
		if seen[c.MerchantID] {
			errorJSON(w, 400, "duplicate_commission_merchant", "duplicate beneficiary")
			return
		}
		seen[c.MerchantID] = true
		if c.Amount > body.Gross-sum {
			errorJSON(w, 400, "commission_exceeds_gross", "commission exceeds gross")
			return
		}
		sum += c.Amount
	}
	var duplicate bool
	_ = h.config.DB.Get(&duplicate, `SELECT EXISTS(SELECT 1 FROM payg_charges WHERE external_order_id=$1)`, body.ExternalOrderID)
	if duplicate {
		errorJSON(w, 409, "duplicate_external_order_id", "external order ID already exists")
		return
	}
	tx, err := h.config.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		internal(w)
		return
	}
	defer tx.Rollback()
	var subscriptionKeyID, subscriptionID, payerAccountID, mainMerchantID, mainAccountID, mainMerchantStatus string
	err = tx.QueryRowx(`SELECT k.id,s.id,s.account_id,m.id,m.settlement_account_id,m.status FROM subscription_keys k JOIN subscriptions s ON s.id=k.subscription_id JOIN products p ON p.id=s.product_id JOIN merchants m ON m.id=p.merchant_id WHERE k.subscription_key_hmac=$1 AND (k.status='active' OR (k.status='revoked' AND $2 <= k.revoked_at)) FOR SHARE OF m`, body.SubscriptionKeyHMAC, body.Started).Scan(&subscriptionKeyID, &subscriptionID, &payerAccountID, &mainMerchantID, &mainAccountID, &mainMerchantStatus)
	if err != nil {
		errorJSON(w, 404, "subscription_key_not_found", "Subscription Key not found")
		return
	}
	if mainMerchantStatus != "active" {
		errorJSON(w, 400, "main_merchant_inactive", "Product Merchant is inactive")
		return
	}
	if _, err = tx.Exec(`SELECT pg_advisory_xact_lock(hashtext($1))`, payerAccountID); err != nil {
		internalError(w, err, "lock Charge payer account")
		return
	}
	resolved := make([]resolvedCommission, 0, len(body.Commissions))
	for _, c := range body.Commissions {
		var settlement, status string
		err = tx.QueryRowx(`SELECT settlement_account_id,status FROM merchants WHERE id=$1`, c.MerchantID).Scan(&settlement, &status)
		if err != nil {
			errorJSON(w, 400, "invalid_commission_merchant", "invalid commission Merchant")
			return
		}
		if status != "active" {
			errorJSON(w, 400, "commission_merchant_inactive", "commission Merchant inactive")
			return
		}
		resolved = append(resolved, resolvedCommission{c, settlement})
	}
	fee := platformFee(body.Gross, h.config.PlatformFeeBPS)
	mainNet := body.Gross - sum - fee
	chargeID := "chg_" + uuid.NewString()
	transactionID := "txn_" + uuid.NewString()
	now := h.config.Now().UTC()
	snapshot, _ := json.Marshal(map[string]any{"order": json.RawMessage(body.Order), "service_started_at": body.Started, "service_completed_at": body.Completed, "main_merchant_id": mainMerchantID})
	_, err = tx.Exec(`INSERT INTO ledger_transactions(id,transaction_type,status,created_at) VALUES($1,'payg_charge','pending',$2)`, transactionID, now)
	if err != nil {
		internalError(w, err, "insert Charge ledger transaction")
		return
	}
	_, err = tx.Exec(`INSERT INTO payg_charges(id,external_order_id,subscription_id,service_principal_id,gross_microcredits,platform_fee_microcredits,main_merchant_net_microcredits,order_snapshot,ledger_transaction_id,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, chargeID, body.ExternalOrderID, subscriptionID, principalID, body.Gross, fee, mainNet, snapshot, transactionID, now)
	if err != nil {
		var pqError *pq.Error
		if errors.As(err, &pqError) && pqError.Code == "23505" && pqError.Constraint == "payg_charges_external_order_id_key" {
			errorJSON(w, 409, "duplicate_external_order_id", "external order ID already exists")
		} else {
			internalError(w, err, "insert Charge")
		}
		return
	}
	for _, c := range resolved {
		_, err = tx.Exec(`INSERT INTO charge_commissions(charge_id,merchant_id,settlement_account_id,amount_microcredits) VALUES($1,$2,$3,$4)`, chargeID, c.MerchantID, c.Settlement, c.Amount)
		if err != nil {
			internalError(w, err, "insert Charge commission")
			return
		}
	}
	if r.Header.Get("X-Gizway-Test-Failpoint") == "after_commission_before_ledger_commit" {
		errorJSON(w, 500, "injected_transaction_failure", "injected transaction failure")
		return
	}
	if err = h.postLedger(tx, transactionID, payerAccountID, mainAccountID, body.Gross, mainNet, fee, resolved); err != nil {
		internalError(w, err, "post Charge ledger")
		return
	}
	if _, err = tx.Exec(`UPDATE ledger_transactions SET status='posted' WHERE id=$1`, transactionID); err != nil {
		internalError(w, err, "post Charge transaction")
		return
	}
	if _, err = tx.Exec(`UPDATE subscription_keys SET last_used_at=CASE WHEN last_used_at IS NULL OR last_used_at < $1 THEN $1 ELSE last_used_at END WHERE id=$2`, body.Completed, subscriptionKeyID); err != nil {
		internalError(w, err, "update Subscription Key last use")
		return
	}
	var payerIssuer, payerSubject string
	if err = tx.QueryRowx(`SELECT u.identity_issuer,u.identity_subject FROM accounts a JOIN users u ON u.id=a.owner_user_id WHERE a.id=$1`, payerAccountID).Scan(&payerIssuer, &payerSubject); err != nil {
		internalError(w, err, "resolve Charge payer identity")
		return
	}
	if _, err = tx.Exec(`UPDATE client_sync.account_balances SET balance_microcredits=balance_microcredits-$1 WHERE account_id=$2`, body.Gross, payerAccountID); err != nil {
		internalError(w, err, "update Charge payer balance projection")
		return
	}
	if _, err = tx.Exec(`UPDATE client_sync.account_balances SET balance_microcredits=balance_microcredits+$1 WHERE account_id=$2`, mainNet, mainAccountID); err != nil {
		internalError(w, err, "update Charge merchant balance projection")
		return
	}
	if _, err = tx.Exec(`INSERT INTO client_sync.transactions(id,account_id,owner_identity_issuer,owner_identity_subject,transaction_type,amount_microcredits,created_at) VALUES($1,$2,$3,$4,'payg_charge',$5,$6)`, transactionID, payerAccountID, payerIssuer, payerSubject, -body.Gross, now); err != nil {
		internalError(w, err, "insert Charge transaction projection")
		return
	}
	if _, err = tx.Exec(`INSERT INTO client_sync.charges(id,account_id,subscription_id,owner_identity_issuer,owner_identity_subject,external_order_id,gross_microcredits,order_snapshot,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, chargeID, payerAccountID, subscriptionID, payerIssuer, payerSubject, body.ExternalOrderID, body.Gross, snapshot, now); err != nil {
		internalError(w, err, "insert Charge projection")
		return
	}
	for _, commission := range resolved {
		if _, err = tx.Exec(`UPDATE client_sync.account_balances SET balance_microcredits=balance_microcredits+$1 WHERE account_id=$2`, commission.Amount, commission.Settlement); err != nil {
			internalError(w, err, "update commission balance projection")
			return
		}
		var issuer, subject string
		if err = tx.QueryRowx(`SELECT u.identity_issuer,u.identity_subject FROM accounts a JOIN users u ON u.id=a.owner_user_id WHERE a.id=$1`, commission.Settlement).Scan(&issuer, &subject); err != nil {
			internalError(w, err, "resolve commission owner identity")
			return
		}
		if _, err = tx.Exec(`INSERT INTO client_sync.commissions(id,merchant_id,charge_id,owner_identity_issuer,owner_identity_subject,amount_microcredits,created_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, chargeID+":"+commission.MerchantID, commission.MerchantID, chargeID, issuer, subject, commission.Amount, now); err != nil {
			internalError(w, err, "insert commission projection")
			return
		}
	}
	if err = tx.Commit(); err != nil {
		internalError(w, err, "commit Charge transaction")
		return
	}
	response := map[string]any{"charge_id": chargeID, "external_order_id": body.ExternalOrderID, "gross_microcredits": body.Gross, "platform_fee_microcredits": fee, "main_merchant_net_microcredits": mainNet, "ledger_transaction_id": transactionID, "commissions": body.Commissions}
	writeJSON(w, 201, response)
}

func validateOrderMetadata(raw json.RawMessage, maxBytes int) error {
	if len(raw) == 0 || len(raw) > maxBytes {
		return fmt.Errorf("order metadata must be a nonempty object no larger than %d bytes", maxBytes)
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return errors.New("order metadata must be valid JSON")
	}
	object, ok := value.(map[string]any)
	if !ok {
		return errors.New("order metadata must be an object")
	}
	forbidden := map[string]bool{
		"prompt": true, "authorization": true, "api_key": true,
		"subscription_key": true, "provider_key": true, "provider_secret": true,
	}
	var inspect func(any) error
	inspect = func(current any) error {
		switch typed := current.(type) {
		case map[string]any:
			for key, nested := range typed {
				normalized := strings.ToLower(strings.TrimSpace(key))
				if forbidden[normalized] || strings.Contains(normalized, "secret") {
					return fmt.Errorf("order metadata field %q is forbidden", key)
				}
				if err := inspect(nested); err != nil {
					return err
				}
			}
		case []any:
			for _, nested := range typed {
				if err := inspect(nested); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return inspect(object)
}

func platformFee(gross, basisPoints int64) int64 {
	whole := gross / 10000 * basisPoints
	remainder := gross % 10000 * basisPoints
	return whole + (remainder+9999)/10000
}

func (h *Handler) postLedger(tx *sqlx.Tx, transactionID, payer, main string, gross, mainNet, fee int64, commissions []resolvedCommission) error {
	ledger := func(account, asset string) (string, error) {
		var id string
		query := `SELECT id FROM ledger_accounts WHERE owner_account_id=$1 AND asset_code='credit'`
		args := []any{account}
		if asset != "" {
			query = `SELECT id FROM ledger_accounts WHERE asset_code=$1`
			args = []any{asset}
		}
		err := tx.Get(&id, query, args...)
		return id, err
	}
	payerLedger, err := ledger(payer, "")
	if err != nil {
		return err
	}
	if err = insertEntry(tx, transactionID, payerLedger, "debit", gross); err != nil {
		return err
	}
	mainLedger, err := ledger(main, "")
	if err != nil {
		return err
	}
	if mainNet > 0 {
		if err = insertEntry(tx, transactionID, mainLedger, "credit", mainNet); err != nil {
			return err
		}
	} else if mainNet < 0 {
		if err = insertEntry(tx, transactionID, mainLedger, "debit", -mainNet); err != nil {
			return err
		}
	}
	for _, c := range commissions {
		target, err := ledger(c.Settlement, "")
		if err != nil {
			return err
		}
		if err = insertEntry(tx, transactionID, target, "credit", c.Amount); err != nil {
			return err
		}
	}
	if fee > 0 {
		platform, err := ledger("", "platform_fee")
		if err != nil {
			return err
		}
		if err = insertEntry(tx, transactionID, platform, "credit", fee); err != nil {
			return err
		}
	}
	return nil
}

func (h *Handler) getCharge(w http.ResponseWriter, external string) {
	var row struct {
		ID, ExternalOrderID string
		Gross, Fee, MainNet int64
		LedgerID            string
	}
	err := h.config.DB.QueryRowx(`SELECT id,external_order_id,gross_microcredits,platform_fee_microcredits,main_merchant_net_microcredits,ledger_transaction_id FROM payg_charges WHERE external_order_id=$1`, external).Scan(&row.ID, &row.ExternalOrderID, &row.Gross, &row.Fee, &row.MainNet, &row.LedgerID)
	if err != nil {
		notFound(w)
		return
	}
	writeJSON(w, 200, map[string]any{"charge_id": row.ID, "external_order_id": row.ExternalOrderID, "gross_microcredits": row.Gross, "platform_fee_microcredits": row.Fee, "main_merchant_net_microcredits": row.MainNet, "ledger_transaction_id": row.LedgerID})
}

func (h *Handler) listAccountCharges(w http.ResponseWriter, accountID string) {
	rows, err := h.queryMaps(`SELECT c.id charge_id,c.external_order_id,c.gross_microcredits,c.platform_fee_microcredits,c.main_merchant_net_microcredits,c.ledger_transaction_id,c.created_at FROM payg_charges c JOIN subscriptions s ON s.id=c.subscription_id WHERE s.account_id=$1 ORDER BY c.created_at,c.id`, accountID)
	if err != nil {
		internal(w)
		return
	}
	writeJSON(w, 200, map[string]any{"data": rowsToMaps(rows)})
}

func (h *Handler) listTransactions(w http.ResponseWriter, accountID, transactionID string) {
	var ids []string
	query := `SELECT DISTINCT t.id FROM ledger_transactions t JOIN ledger_entries e ON e.transaction_id=t.id JOIN ledger_accounts l ON l.id=e.ledger_account_id WHERE l.owner_account_id=$1`
	args := []any{accountID}
	if transactionID != "" {
		query += ` AND t.id=$2`
		args = append(args, transactionID)
	}
	query += ` ORDER BY t.id`
	if h.config.DB.Select(&ids, query, args...) != nil {
		internal(w)
		return
	}
	data := make([]any, 0, len(ids))
	for _, id := range ids {
		entries, _ := h.queryMaps(`SELECT l.owner_account_id account_id,e.direction,e.amount_microcredits FROM ledger_entries e JOIN ledger_accounts l ON l.id=e.ledger_account_id WHERE e.transaction_id=$1 ORDER BY e.id`, id)
		data = append(data, map[string]any{"id": id, "entries": rowsToMaps(entries)})
	}
	writeJSON(w, 200, map[string]any{"data": data})
}

type resolvedCommission struct {
	commissionRequest
	Settlement string
}

func insertEntry(tx *sqlx.Tx, transactionID, ledgerID, direction string, amount int64) error {
	if amount == 0 {
		return nil
	}
	_, err := tx.Exec(`INSERT INTO ledger_entries(id,transaction_id,ledger_account_id,direction,amount_microcredits) VALUES($1,$2,$3,$4,$5)`, "entry_"+uuid.NewString(), transactionID, ledgerID, direction, amount)
	return err
}

func randomToken(reader io.Reader, size int) (string, error) {
	raw := make([]byte, size)
	if _, err := io.ReadFull(reader, raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
func decode(r *http.Request, value any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return errors.New("multiple JSON values")
	}
	return nil
}

type mapRow map[string]any

func (h *Handler) queryMaps(query string, args ...any) ([]mapRow, error) {
	rows, err := h.config.DB.Queryx(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []mapRow{}
	for rows.Next() {
		item := mapRow{}
		if err := rows.MapScan(item); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}
func (h *Handler) queryMap(query string, args ...any) (mapRow, error) {
	rows, err := h.queryMaps(query, args...)
	if err != nil {
		return nil, err
	}
	if len(rows) != 1 {
		return nil, errors.New("not found")
	}
	return rows[0], nil
}
func rowsToMaps(rows []mapRow) []map[string]any {
	if rows == nil {
		return []map[string]any{}
	}
	out := make([]map[string]any, len(rows))
	for i := range rows {
		out[i] = map[string]any(rows[i])
	}
	return out
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func errorJSON(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
func internal(w http.ResponseWriter) { errorJSON(w, 500, "internal_error", "internal server error") }

func internalError(w http.ResponseWriter, err error, operation string) {
	slog.Error("GizPay internal operation failed", "operation", operation, "error", err)
	internal(w)
}
func invalid(w http.ResponseWriter)  { errorJSON(w, 400, "invalid_request", "invalid request") }
func notFound(w http.ResponseWriter) { errorJSON(w, 404, "not_found", "resource not found") }
