package gizpay

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"

	"github.com/idy/gizway/internal/identity"
	"github.com/idy/gizway/internal/subscriptionkey"
)

type Config struct {
	DB                      *sqlx.DB
	Verifier                *identity.Verifier
	HumanAudience           string
	ServiceAudience         string
	HMACSecret              []byte
	EncryptionSecrets       map[int][]byte
	ActiveEncryptionVersion int
	PlatformFeeBPS          int64
	RecheckInterval         time.Duration
	MaxOrderMetadataBytes   int
	MaxCommissions          int
	ServiceAccounts         identity.ServiceAccountManager
	Now                     func() time.Time
}

type Handler struct {
	config Config
	aead   map[int]cipher.AEAD
}

func New(config Config) (*Handler, error) {
	if config.DB == nil || config.Verifier == nil || config.ServiceAccounts == nil || len(config.HMACSecret) == 0 || config.ActiveEncryptionVersion <= 0 || len(config.EncryptionSecrets) == 0 {
		return nil, errors.New("incomplete GizPay handler configuration")
	}
	aead := make(map[int]cipher.AEAD, len(config.EncryptionSecrets))
	for version, secret := range config.EncryptionSecrets {
		if version <= 0 || len(secret) == 0 {
			return nil, errors.New("invalid Subscription API Key encryption key")
		}
		key := sha256.Sum256(secret)
		block, err := aes.NewCipher(key[:])
		if err != nil {
			return nil, err
		}
		aead[version], err = cipher.NewGCM(block)
		if err != nil {
			return nil, err
		}
	}
	if aead[config.ActiveEncryptionVersion] == nil {
		return nil, errors.New("active Subscription API Key encryption version is not configured")
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
	return &Handler{config: config, aead: aead}, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
	userID, accountID, err := h.ensureHuman(r, principal)
	if err != nil {
		errorJSON(w, http.StatusInternalServerError, "internal_error", "identity provisioning failed")
		return
	}
	path := r.URL.Path
	switch {
	case r.Method == http.MethodGet && path == "/account/v1/accounts":
		h.listAccounts(w, userID)
	case strings.HasPrefix(path, "/account/v1/accounts/"):
		h.accountRead(w, r, accountID)
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

func (h *Handler) ensureHuman(r *http.Request, principal identity.Principal) (string, string, error) {
	tx, err := h.config.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback()
	userID := "usr_" + uuid.NewString()
	now := h.config.Now().UTC()
	_, err = tx.ExecContext(r.Context(), `INSERT INTO users(id,identity_issuer,identity_subject,status,created_at) VALUES($1,$2,$3,'active',$4) ON CONFLICT(identity_issuer,identity_subject) DO NOTHING`, userID, principal.Issuer, principal.Subject, now)
	if err != nil {
		return "", "", err
	}
	if err = tx.GetContext(r.Context(), &userID, `SELECT id FROM users WHERE identity_issuer=$1 AND identity_subject=$2`, principal.Issuer, principal.Subject); err != nil {
		return "", "", err
	}
	accountID := "acct_" + uuid.NewString()
	_, err = tx.ExecContext(r.Context(), `INSERT INTO accounts(id,owner_user_id,status,created_at) VALUES($1,$2,'active',$3) ON CONFLICT(owner_user_id) DO NOTHING`, accountID, userID, now)
	if err != nil {
		return "", "", err
	}
	if err = tx.GetContext(r.Context(), &accountID, `SELECT id FROM accounts WHERE owner_user_id=$1`, userID); err != nil {
		return "", "", err
	}
	_, err = tx.ExecContext(r.Context(), `INSERT INTO ledger_accounts(id,owner_account_id,asset_code,status) VALUES($1,$2,'credit','active') ON CONFLICT(owner_account_id,asset_code) DO NOTHING`, "ledger_"+uuid.NewString(), accountID)
	if err != nil {
		return "", "", err
	}
	if err = tx.Commit(); err != nil {
		return "", "", err
	}
	return userID, accountID, nil
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
		var balance int64
		if h.config.DB.Get(&balance, `SELECT balance_microcredits FROM account_balances WHERE account_id=$1`, ownedID) != nil {
			notFound(w)
			return
		}
		writeJSON(w, 200, map[string]any{"account_id": ownedID, "balance_microcredits": balance})
	case "charges":
		h.listAccountCharges(w, ownedID)
	case "transactions":
		h.listTransactions(w, ownedID, r.URL.Query().Get("ledger_transaction_id"))
	default:
		notFound(w)
	}
}

func (h *Handler) serviceAccounts(w http.ResponseWriter, r *http.Request, userID string) {
	if r.Method == http.MethodGet {
		rows, err := h.queryMaps(`SELECT id,status,created_at,revoked_at FROM service_principals WHERE owner_user_id=$1 ORDER BY created_at,id`, userID)
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
	_, err = h.config.DB.Exec(`INSERT INTO service_principals(id,owner_user_id,identity_issuer,identity_subject,credential_key_id,status,created_at) VALUES($1,$2,$3,$4,$5,'active',$6)`, id, userID, h.config.Verifier.Issuer(), credential.Subject, credential.KeyID, h.config.Now().UTC())
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
	allowed := map[string]bool{
		"account_reader": true, "account_payer": true, "merchant_operator": true,
		"subscription_credit_reader": true, "subscription_charger": true, "account_admin": true,
	}
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
	_, err := h.config.DB.Exec(`INSERT INTO merchants(id,settlement_account_id,legal_name,public_name,status,review_level,created_at,updated_at) VALUES($1,$2,$3,$4,'active','basic',$5,$5)`, id, body.SettlementAccountID, body.LegalName, body.PublicName, now)
	if err != nil {
		internal(w)
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "settlement_account_id": body.SettlementAccountID, "legal_name": body.LegalName, "public_name": body.PublicName, "status": "active"})
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
	if r.Method == http.MethodPatch {
		var body struct {
			PublicName string `json:"public_name"`
		}
		if decode(r, &body) != nil {
			invalid(w)
			return
		}
		if strings.TrimSpace(body.PublicName) == "" {
			invalid(w)
			return
		}
		_, err = h.config.DB.Exec(`UPDATE merchants SET public_name=$1,updated_at=$2 WHERE id=$3`, body.PublicName, h.config.Now().UTC(), id)
		if err != nil {
			internal(w)
			return
		}
		row["public_name"] = body.PublicName
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
	_, err := h.config.DB.Exec(`INSERT INTO products(id,merchant_id,name,billing_mode,status,terms_version,created_at,updated_at) VALUES($1,$2,$3,$4,'active',$5,$6,$6)`, id, merchantID, body.Name, body.BillingMode, body.TermsVersion, now)
	if err != nil {
		internal(w)
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "merchant_id": merchantID, "name": body.Name, "billing_mode": body.BillingMode, "status": "active", "terms_version": body.TermsVersion})
}

func (h *Handler) productResource(w http.ResponseWriter, r *http.Request, userID, accountID string) {
	id := r.PathValue("product_id")
	if strings.HasSuffix(r.URL.Path, "/subscriptions") {
		h.createSubscription(w, r, id, userID, accountID)
		return
	}
	row, err := h.queryMap(`SELECT p.* FROM products p JOIN merchants m ON m.id=p.merchant_id JOIN accounts a ON a.id=m.settlement_account_id WHERE p.id=$1 AND a.owner_user_id=$2`, id, userID)
	if err != nil {
		notFound(w)
		return
	}
	if r.Method == http.MethodPatch {
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
	}
	writeJSON(w, 200, row)
}

func (h *Handler) createSubscription(w http.ResponseWriter, r *http.Request, productID, userID, accountID string) {
	if r.Method != http.MethodPost {
		notFound(w)
		return
	}
	var body struct {
		AccountID    string `json:"account_id"`
		TermsVersion string `json:"terms_version"`
	}
	if decode(r, &body) != nil {
		invalid(w)
		return
	}
	if body.AccountID != accountID {
		notFound(w)
		return
	}
	var terms string
	if h.config.DB.Get(&terms, `SELECT terms_version FROM products WHERE id=$1 AND status='active'`, productID) != nil {
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
	id := "sub_" + uuid.NewString()
	now := h.config.Now().UTC()
	_, err := h.config.DB.Exec(`INSERT INTO subscriptions(id,account_id,product_id,status,terms_version,accepted_at,created_at) VALUES($1,$2,$3,'active',$4,$5,$5)`, id, accountID, productID, body.TermsVersion, now)
	if err != nil {
		internal(w)
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
	if strings.Contains(r.URL.Path, "/api-keys") {
		h.subscriptionKeys(w, r, id)
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
	keyID := r.PathValue("api_key_id")
	if keyID != "" {
		if strings.HasSuffix(r.URL.Path, "/revoke") {
			result, err := h.config.DB.Exec(`UPDATE subscription_api_keys SET status='revoked',revoked_at=$1 WHERE id=$2 AND subscription_id=$3 AND status='active'`, h.config.Now().UTC(), keyID, subscriptionID)
			if err != nil {
				internal(w)
				return
			}
			n, _ := result.RowsAffected()
			if n == 0 {
				var exists bool
				_ = h.config.DB.Get(&exists, `SELECT EXISTS(SELECT 1 FROM subscription_api_keys WHERE id=$1 AND subscription_id=$2 AND status='revoked')`, keyID, subscriptionID)
				if exists {
					errorJSON(w, 409, "api_key_already_revoked", "API Key already revoked")
				} else {
					notFound(w)
				}
				return
			}
			h.writeKey(w, keyID, subscriptionID)
			return
		}
		h.writeKey(w, keyID, subscriptionID)
		return
	}
	if r.Method == http.MethodGet {
		var ids []string
		if h.config.DB.Select(&ids, `SELECT id FROM subscription_api_keys WHERE subscription_id=$1 ORDER BY created_at,id`, subscriptionID) != nil {
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
	random, err := randomToken(rand.Reader, 24)
	if err != nil {
		internal(w)
		return
	}
	raw := "giz_sk_" + random
	encrypted, err := h.encrypt(raw, h.config.ActiveEncryptionVersion)
	if err != nil {
		internal(w)
		return
	}
	id := "key_" + uuid.NewString()
	_, err = h.config.DB.Exec(`INSERT INTO subscription_api_keys(id,subscription_id,key_hmac,encrypted_key,encryption_version,status,created_at) VALUES($1,$2,$3,$4,$5,'active',$6)`, id, subscriptionID, subscriptionkey.HMAC(h.config.HMACSecret, raw), encrypted, h.config.ActiveEncryptionVersion, h.config.Now().UTC())
	if err != nil {
		internal(w)
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "subscription_id": subscriptionID, "api_key": raw, "status": "active"})
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
		ID                string     `db:"id"`
		SubscriptionID    string     `db:"subscription_id"`
		EncryptedKey      string     `db:"encrypted_key"`
		EncryptionVersion int        `db:"encryption_version"`
		Status            string     `db:"status"`
		CreatedAt         time.Time  `db:"created_at"`
		RevokedAt         *time.Time `db:"revoked_at"`
	}
	err := h.config.DB.Get(&row, `SELECT id,subscription_id,encrypted_key,encryption_version,status,created_at,revoked_at FROM subscription_api_keys WHERE id=$1 AND subscription_id=$2`, id, subscriptionID)
	if err != nil {
		return nil, err
	}
	raw, err := h.decrypt(row.EncryptedKey, row.EncryptionVersion)
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": row.ID, "subscription_id": row.SubscriptionID, "api_key": raw, "status": row.Status, "created_at": row.CreatedAt, "revoked_at": row.RevokedAt}, nil
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
	if strings.HasSuffix(r.URL.Path, "subscription-credit-checks") {
		if !principal.Roles["subscription_credit_reader"] {
			errorJSON(w, 403, "insufficient_role", "missing reader role")
			return
		}
		h.creditCheck(w, r)
		return
	}
	if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "payg-charges") {
		if !principal.Roles["subscription_charger"] {
			errorJSON(w, 403, "insufficient_role", "missing charger role")
			return
		}
		h.createCharge(w, r, principalID)
		return
	}
	if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/payg-charges/") {
		if !principal.Roles["subscription_charger"] {
			errorJSON(w, 403, "insufficient_role", "missing charger role")
			return
		}
		h.getCharge(w, r.PathValue("external_order_id"))
		return
	}
	notFound(w)
}

func (h *Handler) creditCheck(w http.ResponseWriter, r *http.Request) {
	var body struct {
		APIKeyHMAC string `json:"api_key_hmac"`
	}
	if decode(r, &body) != nil || len(body.APIKeyHMAC) != 43 {
		invalid(w)
		return
	}
	var row struct {
		ProductID          string `db:"product_id"`
		BillingMode        string `db:"billing_mode"`
		KeyStatus          string `db:"key_status"`
		SubscriptionStatus string `db:"subscription_status"`
		ProductStatus      string `db:"product_status"`
		Balance            int64  `db:"balance"`
	}
	err := h.config.DB.Get(&row, `SELECT p.id product_id,p.billing_mode,k.status key_status,s.status subscription_status,p.status product_status,b.balance_microcredits balance FROM subscription_api_keys k JOIN subscriptions s ON s.id=k.subscription_id JOIN products p ON p.id=s.product_id JOIN account_balances b ON b.account_id=s.account_id WHERE k.key_hmac=$1`, body.APIKeyHMAC)
	allowed := err == nil && row.KeyStatus == "active" && row.SubscriptionStatus == "active" && row.ProductStatus == "active" && row.Balance > 0
	available := int64(0)
	if allowed {
		available = row.Balance
	}
	result := map[string]any{"status": "denied", "available_microcredits": available, "checked_at": h.config.Now().UTC(), "recheck_after_seconds": int64(h.config.RecheckInterval / time.Second)}
	if allowed {
		result["status"] = "allowed"
		result["product_id"] = row.ProductID
		result["billing_mode"] = row.BillingMode
	}
	writeJSON(w, 200, result)
}

type commissionRequest struct {
	MerchantID string `json:"merchant_id"`
	Amount     int64  `json:"amount_microcredits"`
}
type chargeRequest struct {
	ExternalOrderID string              `json:"external_order_id"`
	APIKeyHMAC      string              `json:"api_key_hmac"`
	Gross           int64               `json:"gross_microcredits"`
	Commissions     []commissionRequest `json:"commissions"`
	Order           json.RawMessage     `json:"order"`
	Started         time.Time           `json:"service_started_at"`
	Completed       time.Time           `json:"service_completed_at"`
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
	if len(body.APIKeyHMAC) != 43 {
		errorJSON(w, 400, "invalid_api_key_hmac", "API Key HMAC is required")
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
	var subscriptionID, payerAccountID, mainMerchantID, mainAccountID, mainMerchantStatus string
	err = tx.QueryRowx(`SELECT s.id,s.account_id,m.id,m.settlement_account_id,m.status FROM subscription_api_keys k JOIN subscriptions s ON s.id=k.subscription_id JOIN products p ON p.id=s.product_id JOIN merchants m ON m.id=p.merchant_id WHERE k.key_hmac=$1 FOR SHARE OF m`, body.APIKeyHMAC).Scan(&subscriptionID, &payerAccountID, &mainMerchantID, &mainAccountID, &mainMerchantStatus)
	if err != nil {
		errorJSON(w, 404, "subscription_api_key_not_found", "subscription API Key not found")
		return
	}
	if mainMerchantStatus != "active" {
		errorJSON(w, 400, "main_merchant_inactive", "Product Merchant is inactive")
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
		internal(w)
		return
	}
	_, err = tx.Exec(`INSERT INTO payg_charges(id,external_order_id,subscription_id,service_principal_id,gross_microcredits,platform_fee_microcredits,main_merchant_net_microcredits,order_snapshot,ledger_transaction_id,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, chargeID, body.ExternalOrderID, subscriptionID, principalID, body.Gross, fee, mainNet, snapshot, transactionID, now)
	if err != nil {
		var pqError *pq.Error
		if errors.As(err, &pqError) && pqError.Code == "23505" && pqError.Constraint == "payg_charges_external_order_id_key" {
			errorJSON(w, 409, "duplicate_external_order_id", "external order ID already exists")
		} else {
			internal(w)
		}
		return
	}
	for _, c := range resolved {
		_, err = tx.Exec(`INSERT INTO charge_commissions(charge_id,merchant_id,settlement_account_id,amount_microcredits) VALUES($1,$2,$3,$4)`, chargeID, c.MerchantID, c.Settlement, c.Amount)
		if err != nil {
			internal(w)
			return
		}
	}
	if r.Header.Get("X-Gizway-Test-Failpoint") == "after_commission_before_ledger_commit" {
		errorJSON(w, 500, "injected_transaction_failure", "injected transaction failure")
		return
	}
	if err = h.postLedger(tx, transactionID, payerAccountID, mainAccountID, body.Gross, mainNet, fee, resolved); err != nil {
		internal(w)
		return
	}
	if _, err = tx.Exec(`UPDATE ledger_transactions SET status='posted' WHERE id=$1`, transactionID); err != nil {
		internal(w)
		return
	}
	if err = tx.Commit(); err != nil {
		internal(w)
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
		"subscription_api_key": true, "provider_api_key": true, "provider_secret": true,
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
		platform, err := ledger("acct_platform", "")
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

func (h *Handler) encrypt(raw string, version int) (string, error) {
	aead := h.aead[version]
	if aead == nil {
		return "", fmt.Errorf("unknown encryption version %d", version)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := aead.Seal(nonce, nonce, []byte(raw), nil)
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}
func (h *Handler) decrypt(value string, version int) (string, error) {
	aead := h.aead[version]
	if aead == nil {
		return "", fmt.Errorf("unknown encryption version %d", version)
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(raw) < aead.NonceSize() {
		return "", errors.New("invalid ciphertext")
	}
	plain, err := aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], nil)
	return string(plain), err
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
func invalid(w http.ResponseWriter)  { errorJSON(w, 400, "invalid_request", "invalid request") }
func notFound(w http.ResponseWriter) { errorJSON(w, 404, "not_found", "resource not found") }
