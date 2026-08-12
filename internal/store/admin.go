package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"math"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

type Administrator struct {
	ID          string  `db:"id" json:"id"`
	Email       string  `db:"email" json:"email"`
	DisplayName string  `db:"display_name" json:"display_name"`
	Status      string  `db:"status" json:"status"`
	LastLoginAt *string `db:"last_login_at" json:"last_login_at"`
	CreatedAt   string  `db:"created_at" json:"created_at"`
	UpdatedAt   string  `db:"updated_at" json:"updated_at"`
}

const administratorColumns = `id,email,display_name,status,last_login_at,created_at,updated_at`

func (s *Store) LoginAdministrator(ctx context.Context, email, password, sessionID string, secretHash []byte, createdAt, expiresAt string) (Administrator, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return Administrator{}, err
	}
	defer tx.Rollback()
	var row struct {
		Administrator
		PasswordHash string `db:"password_hash"`
	}
	err = tx.GetContext(ctx, &row, `SELECT `+administratorColumns+`,password_hash FROM administrators WHERE LOWER(email)=LOWER(?) AND status='active'`, email)
	if errors.Is(err, sql.ErrNoRows) || err == nil && bcrypt.CompareHashAndPassword([]byte(row.PasswordHash), []byte(password)) != nil {
		return Administrator{}, ErrNotFound
	}
	if err != nil {
		return Administrator{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO admin_sessions(id,administrator_id,secret_hash,status,expires_at,created_at) VALUES (?,? ,?,'active',?,?)`, sessionID, row.ID, secretHash, expiresAt, createdAt); err != nil {
		return Administrator{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE administrators SET last_login_at=?,updated_at=? WHERE id=?`, createdAt, createdAt, row.ID); err != nil {
		return Administrator{}, err
	}
	if err := insertAudit(ctx, tx, row.ID, "admin_session.issued", "admin_session", sessionID, "password authentication succeeded", createdAt); err != nil {
		return Administrator{}, err
	}
	if err := tx.Commit(); err != nil {
		return Administrator{}, err
	}
	row.LastLoginAt = &createdAt
	row.UpdatedAt = createdAt
	return row.Administrator, nil
}

func (s *Store) RefreshAdminSession(ctx context.Context, oldSecret, newSessionID string, newSecretHash []byte, at, expiresAt string) (string, error) {
	return retrySerializable(ctx, func() (string, error) {
		return s.refreshAdminSession(ctx, oldSecret, newSessionID, newSecretHash, at, expiresAt)
	})
}

func (s *Store) refreshAdminSession(ctx context.Context, oldSecret, newSessionID string, newSecretHash []byte, at, expiresAt string) (string, error) {
	oldHash := sha256.Sum256([]byte(oldSecret))
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var old struct {
		ID              string `db:"id"`
		AdministratorID string `db:"administrator_id"`
	}
	if err := tx.GetContext(ctx, &old, `SELECT s.id,s.administrator_id FROM admin_sessions s JOIN administrators a ON a.id=s.administrator_id WHERE s.secret_hash=? AND s.status='active' AND s.expires_at>? AND a.status='active'`, oldHash[:], at); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", err
	}
	result, err := tx.ExecContext(ctx, `UPDATE admin_sessions SET status='revoked',revoked_at=? WHERE id=? AND status='active'`, at, old.ID)
	if err != nil {
		return "", err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return "", err
	}
	if changed != 1 {
		// The predecessor is a one-purpose refresh credential. This CAS closes
		// the PostgreSQL race where two transactions both read it as active.
		return "", ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO admin_sessions(id,administrator_id,secret_hash,status,expires_at,created_at) VALUES (?,?,?,'active',?,?)`, newSessionID, old.AdministratorID, newSecretHash, expiresAt, at); err != nil {
		return "", err
	}
	if err := insertAudit(ctx, tx, old.AdministratorID, "admin_session.refreshed", "admin_session", newSessionID, "session rotated and predecessor revoked", at); err != nil {
		return "", err
	}
	return old.AdministratorID, tx.Commit()
}

func (s *Store) RevokeAdminSession(ctx context.Context, secret, at string) error {
	return retrySerializableError(ctx, func() error {
		return s.revokeAdminSession(ctx, secret, at)
	})
}

func (s *Store) revokeAdminSession(ctx context.Context, secret, at string) error {
	hash := sha256.Sum256([]byte(secret))
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var row struct {
		ID              string `db:"id"`
		AdministratorID string `db:"administrator_id"`
	}
	if err := tx.GetContext(ctx, &row, `SELECT id,administrator_id FROM admin_sessions WHERE secret_hash=? AND status='active'`, hash[:]); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE admin_sessions SET status='revoked',revoked_at=? WHERE id=? AND status='active'`, at, row.ID)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return ErrNotFound
	}
	if err := insertAudit(ctx, tx, row.AdministratorID, "admin_session.revoked", "admin_session", row.ID, "administrator logout", at); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) AuthenticateAdminSession(ctx context.Context, secret string, at string) (string, error) {
	hash := sha256.Sum256([]byte(secret))
	var id string
	err := s.db.GetContext(ctx, &id, `SELECT a.id FROM admin_sessions s JOIN administrators a ON a.id=s.administrator_id WHERE s.secret_hash=? AND s.status='active' AND s.expires_at>? AND a.status='active'`, hash[:], at)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return id, err
}
func (s *Store) GetAdministrator(ctx context.Context, id string) (Administrator, error) {
	var row Administrator
	err := s.db.GetContext(ctx, &row, `SELECT `+administratorColumns+` FROM administrators WHERE id=?`, id)
	if errors.Is(err, sql.ErrNoRows) {
		return row, ErrNotFound
	}
	return row, err
}
func (s *Store) CreateAdministrator(ctx context.Context, actorID, email, displayName, password, at string) (Administrator, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return Administrator{}, err
	}
	row := Administrator{ID: uuid.NewString(), Email: email, DisplayName: displayName, Status: "active", CreatedAt: at, UpdatedAt: at}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return row, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO administrators(id,email,display_name,password_hash,status,created_at,updated_at) VALUES (?,?,?,?,'active',?,?)`, row.ID, email, displayName, string(hash), at, at); err != nil {
		return row, err
	}
	if err := insertAudit(ctx, tx, actorID, "administrator.created", "administrator", row.ID, "created administrator", at); err != nil {
		return row, err
	}
	return row, tx.Commit()
}
func (s *Store) UpdateAdministrator(ctx context.Context, actorID, id, displayName, status, password, reason, at string) (Administrator, error) {
	return retrySerializable(ctx, func() (Administrator, error) {
		return s.updateAdministrator(ctx, actorID, id, displayName, status, password, reason, at)
	})
}

func (s *Store) updateAdministrator(ctx context.Context, actorID, id, displayName, status, password, reason, at string) (Administrator, error) {
	var passwordHash string
	if password != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return Administrator{}, err
		}
		passwordHash = string(hash)
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return Administrator{}, err
	}
	defer tx.Rollback()
	if status != "" && status != "active" {
		var active int
		if err := tx.GetContext(ctx, &active, `SELECT COUNT(*) FROM administrators WHERE status='active'`); err != nil {
			return Administrator{}, err
		}
		var current string
		if err := tx.GetContext(ctx, &current, `SELECT status FROM administrators WHERE id=?`, id); err != nil {
			return Administrator{}, ErrNotFound
		}
		if current == "active" && active <= 1 {
			return Administrator{}, ErrIdempotencyConflict
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE administrators SET display_name=CASE WHEN ?='' THEN display_name ELSE ? END,status=CASE WHEN ?='' THEN status ELSE ? END,password_hash=CASE WHEN ?='' THEN password_hash ELSE ? END,updated_at=? WHERE id=?`, displayName, displayName, status, status, passwordHash, passwordHash, at, id)
	if err != nil {
		return Administrator{}, err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return Administrator{}, ErrNotFound
	}
	if passwordHash != "" {
		// A password rotation invalidates every bearer session issued under the
		// old authenticator. Admin API keys are separate credentials and retain
		// their independently managed lifecycle.
		if _, err := tx.ExecContext(ctx, `UPDATE admin_sessions SET status='revoked',revoked_at=? WHERE administrator_id=? AND status='active'`, at, id); err != nil {
			return Administrator{}, err
		}
	}
	if err := insertAudit(ctx, tx, actorID, "administrator.updated", "administrator", id, reason, at); err != nil {
		return Administrator{}, err
	}
	if err := tx.Commit(); err != nil {
		return Administrator{}, err
	}
	return s.GetAdministrator(ctx, id)
}

type AdminAPIKey struct {
	ID              string  `db:"id" json:"id"`
	AdministratorID string  `db:"administrator_id" json:"administrator_id"`
	Name            string  `db:"name" json:"name"`
	KeyPrefix       string  `db:"key_prefix" json:"key_prefix"`
	Status          string  `db:"status" json:"status"`
	ExpiresAt       *string `db:"expires_at" json:"expires_at"`
	LastUsedAt      *string `db:"last_used_at" json:"last_used_at"`
	CreatedAt       string  `db:"created_at" json:"created_at"`
}

func (s *Store) ListAdminAPIKeys(ctx context.Context, administratorID string) ([]AdminAPIKey, error) {
	var rows []AdminAPIKey
	err := s.db.SelectContext(ctx, &rows, `SELECT id,administrator_id,name,key_prefix,status,expires_at,last_used_at,created_at FROM admin_api_keys WHERE administrator_id=? ORDER BY created_at,id`, administratorID)
	return rows, err
}
func (s *Store) CreateAdminAPIKey(ctx context.Context, actorID, idempotencyKey string, payloadHash, secretHash []byte, key AdminAPIKey) (AdminAPIKey, bool, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return key, false, err
	}
	defer tx.Rollback()
	var existing struct {
		ID   string `db:"id"`
		Hash []byte `db:"payload_hash"`
	}
	err = tx.GetContext(ctx, &existing, `SELECT id,payload_hash FROM admin_api_keys WHERE administrator_id=? AND idempotency_key=?`, key.AdministratorID, idempotencyKey)
	if err == nil {
		if !bytes.Equal(existing.Hash, payloadHash) {
			return key, false, ErrIdempotencyConflict
		}
		var stored AdminAPIKey
		if err := tx.GetContext(ctx, &stored, `SELECT id,administrator_id,name,key_prefix,status,expires_at,last_used_at,created_at FROM admin_api_keys WHERE id=?`, existing.ID); err != nil {
			return key, false, err
		}
		return stored, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return key, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO admin_api_keys(id,administrator_id,name,key_prefix,secret_hash,status,expires_at,created_at,idempotency_key,payload_hash) VALUES (?,?,?,?,?,'active',?,?,?,?)`, key.ID, key.AdministratorID, key.Name, key.KeyPrefix, secretHash, key.ExpiresAt, key.CreatedAt, idempotencyKey, payloadHash); err != nil {
		return key, false, err
	}
	if err := insertAudit(ctx, tx, actorID, "admin_api_key.created", "admin_api_key", key.ID, "created Admin key", key.CreatedAt); err != nil {
		return key, false, err
	}
	return key, false, tx.Commit()
}
func (s *Store) RevokeAdminAPIKey(ctx context.Context, actorID, administratorID, keyID, reason, at string) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE admin_api_keys SET status='revoked',revoked_at=? WHERE id=? AND administrator_id=? AND status='active'`, at, keyID, administratorID)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrNotFound
	}
	if err := insertAudit(ctx, tx, actorID, "admin_api_key.revoked", "admin_api_key", keyID, reason, at); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) AdminOverview(ctx context.Context, generatedAt string) (map[string]any, error) {
	result := map[string]any{"generated_at": generatedAt}
	queries := map[string]string{"users": `SELECT COUNT(*) FROM users`, "active_api_keys": `SELECT COUNT(*) FROM api_keys WHERE status='active'`, "received_usage": `SELECT COUNT(*) FROM gateway_usage_records`}
	for key, q := range queries {
		var value int64
		if err := s.db.GetContext(ctx, &value, q); err != nil {
			return nil, err
		}
		result[key] = value
	}
	var charged, payments int64
	if err := s.db.GetContext(ctx, &charged, `SELECT COALESCE(SUM(charged_microcredits),0) FROM gateway_usage_records`); err != nil {
		return nil, err
	}
	if err := s.db.GetContext(ctx, &payments, `SELECT COALESCE(SUM(amount_microcredits),0) FROM payment_intents WHERE status='succeeded'`); err != nil {
		return nil, err
	}
	result["charged"] = CreditAmount{Asset: "GIZ_CREDIT", Microcredits: charged}
	result["payments"] = CreditAmount{Asset: "GIZ_CREDIT", Microcredits: payments}
	return result, nil
}

func (s *Store) AdminGetUser(ctx context.Context, id string) (map[string]any, error) {
	user, err := s.GetUser(ctx, id)
	if err != nil {
		return nil, err
	}
	var accounts []map[string]any
	rows, err := s.db.QueryxContext(ctx, `SELECT a.id,a.kind,a.name,a.status,COALESCE(b.balance_microcredits,0) AS balance FROM accounts a LEFT JOIN account_balances b ON b.account_id=a.id WHERE a.owner_user_id=? ORDER BY a.created_at,a.id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var row struct {
			ID      string `db:"id"`
			Kind    string `db:"kind"`
			Name    string `db:"name"`
			Status  string `db:"status"`
			Balance int64  `db:"balance"`
		}
		if err := rows.StructScan(&row); err != nil {
			return nil, err
		}
		accounts = append(accounts, map[string]any{"id": row.ID, "kind": row.Kind, "name": row.Name, "status": row.Status, "balance": CreditAmount{Asset: "GIZ_CREDIT", Microcredits: row.Balance}})
	}
	return map[string]any{"id": user.ID, "email": user.Email, "display_name": user.DisplayName, "status": user.Status, "created_at": user.CreatedAt, "accounts": accounts}, nil
}
func (s *Store) ChangeUserStatus(ctx context.Context, actorID, userID, status, reason, at string) (User, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE users SET status=?,updated_at=? WHERE id=?`, status, at, userID)
	if err != nil {
		return User{}, err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return User{}, ErrNotFound
	}
	if err := insertAudit(ctx, tx, actorID, "user.status_changed", "user", userID, reason, at); err != nil {
		return User{}, err
	}
	if err := tx.Commit(); err != nil {
		return User{}, err
	}
	return s.GetUser(ctx, userID)
}

func (s *Store) GetMerchant(ctx context.Context, id string) (map[string]any, error) {
	var row struct {
		AccountID   string  `db:"account_id"`
		LegalName   string  `db:"legal_name"`
		PublicName  string  `db:"public_name"`
		Status      string  `db:"merchant_status"`
		ReviewLevel string  `db:"review_level"`
		CountryCode *string `db:"country_code"`
		WebsiteURL  *string `db:"website_url"`
		CreatedAt   string  `db:"created_at"`
	}
	err := s.db.GetContext(ctx, &row, `SELECT account_id,legal_name,public_name,merchant_status,review_level,country_code,website_url,created_at FROM merchant_accounts WHERE account_id=?`, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"account_id": row.AccountID, "legal_name": row.LegalName, "public_name": row.PublicName, "status": row.Status, "review_level": row.ReviewLevel, "country_code": row.CountryCode, "website_url": row.WebsiteURL, "created_at": row.CreatedAt}, nil
}
func (s *Store) DecideMerchant(ctx context.Context, actorID, id, decision, reviewLevel, reason, at string) (map[string]any, error) {
	status := map[string]string{"approve": "approved", "reject": "rejected", "suspend": "suspended", "reactivate": "approved"}[decision]
	if status == "" {
		return nil, errors.New("invalid merchant decision")
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE merchant_accounts SET merchant_status=?,review_level=?,updated_at=? WHERE account_id=?`, status, reviewLevel, at, id)
	if err != nil {
		return nil, err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return nil, ErrNotFound
	}
	if err := insertAudit(ctx, tx, actorID, "merchant.decision", "merchant_account", id, reason, at); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetMerchant(ctx, id)
}

func insertAudit(ctx context.Context, tx *boundTx, actorID, action, resourceType, resourceID, reason, at string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO audit_events(id,actor_type,actor_id,action,resource_type,resource_id,reason,request_id,metadata,created_at) VALUES (?,'administrator',?,?,?,?,?,?,'{}',?)`, uuid.NewString(), actorID, action, resourceType, resourceID, reason, auditRequestID(ctx), at)
	return err
}

func (s *Store) AdminRevokeAPIKey(ctx context.Context, actorID, keyID, reason, at string) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE api_keys SET status='revoked',revoked_at=? WHERE id=? AND status='active'`, at, keyID)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrNotFound
	}
	if err := insertAudit(ctx, tx, actorID, "api_key.revoked", "api_key", keyID, reason, at); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) AdminRows(ctx context.Context, kind, id string) ([]map[string]any, error) {
	queries := map[string]string{"gateway_executions": `SELECT id,public_model,model_variant_id,provider_request_id,protocol,stream_mode,rate_publication_id,status,estimated_microcredits,actual_microcredits,started_at,completed_at FROM gateway_executions`, "payments": `SELECT id,'topup' AS type,account_id,credit_microcredits AS amount_microcredits,status,created_at FROM topups UNION ALL SELECT id,'refund',account_id,credit_microcredits,status,created_at FROM refunds UNION ALL SELECT id,'transfer',sender_account_id,amount_microcredits,status,created_at FROM credit_transfers UNION ALL SELECT id,'merchant_payment',merchant_account_id,amount_microcredits,status,created_at FROM payment_intents`, "ledger_accounts": `SELECT la.id,la.owner_account_id,la.code,la.kind,la.asset_code,la.normal_balance,la.status,COALESCE(b.balance_microcredits,0) AS posted_balance_microcredits FROM ledger_accounts la LEFT JOIN account_balances b ON b.account_id=la.owner_account_id`, "ledger_transactions": `SELECT id,transaction_type,status,description,reference_type,reference_id,created_at,posted_at FROM ledger_transactions`, "webhook_deliveries": `SELECT d.id,d.event_id,d.endpoint_id,v.event_type,d.attempt,d.status,d.response_status,d.error,d.created_at FROM webhook_deliveries d JOIN webhook_events v ON v.id=d.event_id`, "audit_events": `SELECT id,actor_type,actor_id,action,resource_type,resource_id,reason,request_id,metadata,created_at FROM audit_events ORDER BY sequence`}
	query, ok := queries[kind]
	if !ok {
		return nil, errors.New("unsupported admin query")
	}
	args := []any{}
	if kind == "gateway_executions" && id != "" {
		query += ` WHERE id=?`
		args = append(args, id)
	}
	rows, err := s.db.QueryxContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []map[string]any{}
	for rows.Next() {
		item := map[string]any{}
		if err := rows.MapScan(item); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

// ChangeAccountBalanceStatus freezes or unfreezes spendability without
// altering a posted ledger entry. Historical balances and transactions remain
// readable while every outgoing command consults ledger_accounts.status.
func (s *Store) ChangeAccountBalanceStatus(ctx context.Context, actorID, accountID, status, reason, at string) (map[string]any, error) {
	if status != "active" && status != "frozen" {
		return nil, ErrRiskDenied
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE ledger_accounts SET status=?,updated_at=? WHERE owner_account_id=? AND asset_code='GIZ_CREDIT' AND status<>'closed'`, status, at, accountID)
	if err != nil {
		return nil, err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return nil, ErrNotFound
	}
	action := "balance.unfrozen"
	if status == "frozen" {
		action = "balance.frozen"
	}
	if err := insertAudit(ctx, tx, actorID, action, "account_balance", accountID, reason, at); err != nil {
		return nil, err
	}
	var item map[string]any
	rowsSQL, err := tx.QueryxContext(ctx, `SELECT owner_account_id AS account_id,asset_code,status,updated_at FROM ledger_accounts WHERE owner_account_id=? AND asset_code='GIZ_CREDIT'`, accountID)
	if err != nil {
		return nil, err
	}
	defer rowsSQL.Close()
	if rowsSQL.Next() {
		item = map[string]any{}
		if err := rowsSQL.MapScan(item); err != nil {
			return nil, err
		}
	} else {
		return nil, ErrNotFound
	}
	if err := rowsSQL.Close(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *Store) CreateLedgerAdjustment(ctx context.Context, actorID, idempotencyKey, description, reason, at string, entries []LedgerEntryInput) (map[string]any, error) {
	if len(entries) < 2 {
		return nil, errors.New("at least two entries required")
	}
	var debit, credit int64
	for _, e := range entries {
		if e.Amount <= 0 {
			return nil, errors.New("positive entries required")
		}
		if e.Direction == "debit" {
			if e.Amount > math.MaxInt64-debit {
				return nil, errors.New("debit total exceeds int64")
			}
			debit += e.Amount
		} else if e.Direction == "credit" {
			if e.Amount > math.MaxInt64-credit {
				return nil, errors.New("credit total exceeds int64")
			}
			credit += e.Amount
		} else {
			return nil, errors.New("invalid direction")
		}
	}
	if debit != credit {
		return nil, errors.New("entries must balance")
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	id := uuid.NewString()
	if _, err := tx.ExecContext(ctx, `INSERT INTO ledger_transactions(id,transaction_type,status,idempotency_key,description,reference_type,reference_id,created_at,posted_at) VALUES (?,'adjustment','posted',?,?,'admin_adjustment',?,?,?)`, id, "admin-adjustment:"+idempotencyKey, description, id, at, at); err != nil {
		return nil, err
	}
	for i, e := range entries {
		if _, err := tx.ExecContext(ctx, `INSERT INTO ledger_entries(id,transaction_id,ledger_account_id,sequence,direction,amount_microcredits,created_at) VALUES (?,?,?,?,?,?,?)`, uuid.NewString(), id, e.LedgerAccountID, i+1, e.Direction, e.Amount, at); err != nil {
			return nil, err
		}
	}
	if err := insertAudit(ctx, tx, actorID, "ledger.adjusted", "ledger_transaction", id, reason, at); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "transaction_type": "adjustment", "status": "posted", "description": description, "entries": entries, "created_at": at, "posted_at": at}, nil
}

type LedgerEntryInput struct {
	LedgerAccountID string `db:"ledger_account_id" json:"ledger_account_id"`
	Direction       string `db:"direction" json:"direction"`
	Amount          int64  `db:"amount_microcredits" json:"amount_microcredits"`
}

type Provider struct {
	ID        string `db:"id" json:"id"`
	Slug      string `db:"slug" json:"slug"`
	Name      string `db:"name" json:"name"`
	Status    string `db:"status" json:"status"`
	CreatedAt string `db:"created_at" json:"created_at"`
	UpdatedAt string `db:"updated_at" json:"updated_at"`
}

func (s *Store) ListProviders(ctx context.Context) ([]Provider, error) {
	var rows []Provider
	err := s.db.SelectContext(ctx, &rows, `SELECT id,slug,name,status,created_at,updated_at FROM providers ORDER BY created_at,id`)
	return rows, err
}
func (s *Store) CreateProvider(ctx context.Context, actorID, slug, name, at string) (Provider, error) {
	row := Provider{ID: uuid.NewString(), Slug: slug, Name: name, Status: "active", CreatedAt: at, UpdatedAt: at}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return row, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO providers(id,slug,name,status,created_at,updated_at) VALUES (?,?,?,'active',?,?)`, row.ID, slug, name, at, at); err != nil {
		return row, err
	}
	if err := insertAudit(ctx, tx, actorID, "provider.created", "provider", row.ID, "created provider", at); err != nil {
		return row, err
	}
	return row, tx.Commit()
}
func (s *Store) UpdateProvider(ctx context.Context, actorID, id, name, status, at string) (Provider, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return Provider{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE providers SET name=CASE WHEN ?='' THEN name ELSE ? END,status=CASE WHEN ?='' THEN status ELSE ? END,updated_at=? WHERE id=?`, name, name, status, status, at, id)
	if err != nil {
		return Provider{}, err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return Provider{}, ErrNotFound
	}
	if err := insertAudit(ctx, tx, actorID, "provider.updated", "provider", id, "updated provider", at); err != nil {
		return Provider{}, err
	}
	if err := tx.Commit(); err != nil {
		return Provider{}, err
	}
	var row Provider
	err = s.db.GetContext(ctx, &row, `SELECT id,slug,name,status,created_at,updated_at FROM providers WHERE id=?`, id)
	return row, err
}

type ProviderEndpoint struct {
	ID                   string  `db:"id" json:"id"`
	ProviderID           string  `db:"provider_id" json:"provider_id"`
	Name                 string  `db:"name" json:"name"`
	BaseURL              string  `db:"base_url" json:"base_url"`
	Region               *string `db:"region" json:"region"`
	Priority             int     `db:"priority" json:"priority"`
	Weight               int     `db:"weight" json:"weight"`
	Status               string  `db:"status" json:"status"`
	CreatedAt            string  `db:"created_at" json:"created_at"`
	UpdatedAt            string  `db:"updated_at" json:"updated_at"`
	CredentialConfigured bool    `json:"credential_configured"`
}

func (s *Store) ListProviderEndpoints(ctx context.Context, providerID string) ([]ProviderEndpoint, error) {
	var rows []ProviderEndpoint
	err := s.db.SelectContext(ctx, &rows, `SELECT id,provider_id,name,base_url,region,priority,weight,status,created_at,updated_at FROM provider_endpoints WHERE provider_id=? ORDER BY priority,id`, providerID)
	for i := range rows {
		rows[i].CredentialConfigured = true
	}
	return rows, err
}
func credentialReference(secret string) string {
	hash := sha256.Sum256([]byte(secret))
	return "sha256:" + hex.EncodeToString(hash[:])
}

func (s *Store) protectProviderCredential(secret string) (string, error) {
	if s.secrets == nil {
		return credentialReference(secret), nil
	}
	return s.secrets.encrypt(secret)
}
func (s *Store) CreateProviderEndpoint(ctx context.Context, actorID, providerID, name, baseURL, credential string, region *string, priority, weight int, at string) (ProviderEndpoint, error) {
	row := ProviderEndpoint{ID: uuid.NewString(), ProviderID: providerID, Name: name, BaseURL: baseURL, Region: region, Priority: priority, Weight: weight, Status: "active", CreatedAt: at, UpdatedAt: at, CredentialConfigured: true}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return row, err
	}
	defer tx.Rollback()
	protectedCredential, err := s.protectProviderCredential(credential)
	if err != nil {
		return row, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO provider_endpoints(id,provider_id,name,base_url,credential_ref,region,priority,weight,status,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?, 'active',?,?)`, row.ID, providerID, name, baseURL, protectedCredential, region, priority, weight, at, at); err != nil {
		return row, err
	}
	if err := insertAudit(ctx, tx, actorID, "provider_endpoint.created", "provider_endpoint", row.ID, "created endpoint", at); err != nil {
		return row, err
	}
	return row, tx.Commit()
}
func (s *Store) UpdateProviderEndpoint(ctx context.Context, actorID, id, name, baseURL, status string, region *string, regionSet bool, priority, weight *int, at string) (ProviderEndpoint, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return ProviderEndpoint{}, err
	}
	defer tx.Rollback()
	var priorityValue, weightValue int
	regionFlag, priorityFlag, weightFlag := 0, 0, 0
	if regionSet {
		regionFlag = 1
	}
	if priority != nil {
		priorityValue = *priority
		priorityFlag = 1
	}
	if weight != nil {
		weightValue = *weight
		weightFlag = 1
	}
	result, err := tx.ExecContext(ctx, `UPDATE provider_endpoints SET
		name=CASE WHEN ?='' THEN name ELSE ? END,
		base_url=CASE WHEN ?='' THEN base_url ELSE ? END,
		status=CASE WHEN ?='' THEN status ELSE ? END,
		region=CASE WHEN ?=0 THEN region ELSE ? END,
		priority=CASE WHEN ?=0 THEN priority ELSE ? END,
		weight=CASE WHEN ?=0 THEN weight ELSE ? END,
		updated_at=? WHERE id=?`,
		name, name, baseURL, baseURL, status, status,
		regionFlag, region, priorityFlag, priorityValue, weightFlag, weightValue, at, id)
	if err != nil {
		return ProviderEndpoint{}, err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ProviderEndpoint{}, ErrNotFound
	}
	if err := insertAudit(ctx, tx, actorID, "provider_endpoint.updated", "provider_endpoint", id, "updated endpoint", at); err != nil {
		return ProviderEndpoint{}, err
	}
	if err := tx.Commit(); err != nil {
		return ProviderEndpoint{}, err
	}
	var row ProviderEndpoint
	err = s.db.GetContext(ctx, &row, `SELECT id,provider_id,name,base_url,region,priority,weight,status,created_at,updated_at FROM provider_endpoints WHERE id=?`, id)
	row.CredentialConfigured = true
	return row, err
}
func (s *Store) RotateProviderCredential(ctx context.Context, actorID, id, credential, at string) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	protectedCredential, err := s.protectProviderCredential(credential)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE provider_endpoints SET credential_ref=?,updated_at=? WHERE id=?`, protectedCredential, at, id)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrNotFound
	}
	if err := insertAudit(ctx, tx, actorID, "provider_endpoint.credential_rotated", "provider_endpoint", id, "rotated credential", at); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ReverseLedgerTransaction(ctx context.Context, actorID, originalID, idempotencyKey, reason, at string) (map[string]any, error) {
	return retrySerializable(ctx, func() (map[string]any, error) {
		return s.reverseLedgerTransaction(ctx, actorID, originalID, idempotencyKey, reason, at)
	})
}

func (s *Store) reverseLedgerTransaction(ctx context.Context, actorID, originalID, idempotencyKey, reason, at string) (map[string]any, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var existingID, existingOriginal string
	err = scanCommandRow(ctx, tx.QueryRowxContext(ctx, `SELECT id,reference_id FROM ledger_transactions WHERE transaction_type='reversal' AND idempotency_key=?`, "admin-reversal:"+idempotencyKey), &existingID, &existingOriginal)
	if err == nil {
		if existingOriginal != originalID {
			return nil, ErrIdempotencyConflict
		}
		return ledgerReversalResult(ctx, tx, existingID)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	// One posted transaction can have only one compensating reversal. A retry
	// with a different transport key still replays the canonical reversal
	// instead of reversing the same economic event twice.
	err = scanCommandRow(ctx, tx.QueryRowxContext(ctx, `SELECT id FROM ledger_transactions WHERE transaction_type='reversal' AND reference_type='ledger_transaction' AND reference_id=?`, originalID), &existingID)
	if err == nil {
		return ledgerReversalResult(ctx, tx, existingID)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	var originalType, status string
	if err := scanCommandRow(ctx, tx.QueryRowxContext(ctx, `SELECT transaction_type,status FROM ledger_transactions WHERE id=?`, originalID), &originalType, &status); err != nil {
		return nil, ErrNotFound
	}
	// The generic administrator endpoint is deliberately limited to manual
	// adjustments. Product transactions have domain state outside the double-
	// entry journal: top-ups own refundable credit lots, refunds own provider
	// state, transfers own sender availability, and Gateway usage owns a
	// reservation. Reversing only their ledger entries would make those domain
	// records disagree with the balance and could spend reserved credit twice.
	// Such transactions must be compensated by their dedicated workflow.
	if originalType != "adjustment" || status != "posted" {
		return nil, ErrIdempotencyConflict
	}
	var original []LedgerEntryInput
	if err := tx.SelectContext(ctx, &original, `SELECT ledger_account_id,direction,amount_microcredits FROM ledger_entries WHERE transaction_id=? ORDER BY sequence`, originalID); err != nil {
		return nil, err
	}
	id := uuid.NewString()
	if _, err := tx.ExecContext(ctx, `INSERT INTO ledger_transactions(id,transaction_type,status,idempotency_key,description,reference_type,reference_id,created_at,posted_at) VALUES (?,'reversal','posted',?,'Compensating reversal','ledger_transaction',?,?,?)`, id, "admin-reversal:"+idempotencyKey, originalID, at, at); err != nil {
		return nil, err
	}
	reversed := make([]LedgerEntryInput, 0, len(original))
	for i, e := range original {
		direction := "credit"
		if e.Direction == "credit" {
			direction = "debit"
		}
		item := LedgerEntryInput{LedgerAccountID: e.LedgerAccountID, Direction: direction, Amount: e.Amount}
		reversed = append(reversed, item)
		if _, err := tx.ExecContext(ctx, `INSERT INTO ledger_entries(id,transaction_id,ledger_account_id,sequence,direction,amount_microcredits,created_at) VALUES (?,?,?,?,?,?,?)`, uuid.NewString(), id, item.LedgerAccountID, i+1, item.Direction, item.Amount, at); err != nil {
			return nil, err
		}
	}
	if err := insertAudit(ctx, tx, actorID, "ledger.reversed", "ledger_transaction", originalID, reason, at); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "transaction_type": "reversal", "status": "posted", "description": "Compensating reversal", "entries": reversed, "created_at": at, "posted_at": at}, nil
}

func ledgerReversalResult(ctx context.Context, tx *boundTx, id string) (map[string]any, error) {
	var row struct {
		CreatedAt string `db:"created_at"`
		PostedAt  string `db:"posted_at"`
	}
	if err := tx.GetContext(ctx, &row, `SELECT created_at,posted_at FROM ledger_transactions WHERE id=?`, id); err != nil {
		return nil, err
	}
	var entries []LedgerEntryInput
	if err := tx.SelectContext(ctx, &entries, `SELECT ledger_account_id,direction,amount_microcredits FROM ledger_entries WHERE transaction_id=? ORDER BY sequence`, id); err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "transaction_type": "reversal", "status": "posted", "description": "Compensating reversal", "entries": entries, "created_at": row.CreatedAt, "posted_at": row.PostedAt}, nil
}

func (s *Store) RetryWebhookDelivery(ctx context.Context, actorID, originalID, idempotencyKey, at string) (string, error) {
	return retrySerializable(ctx, func() (string, error) {
		return s.retryWebhookDelivery(ctx, actorID, originalID, idempotencyKey, at)
	})
}

func (s *Store) retryWebhookDelivery(ctx context.Context, actorID, originalID, idempotencyKey, at string) (string, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	payloadHash := sha256.Sum256([]byte(originalID))
	var recordedOriginal, recordedResult string
	err = scanCommandRow(ctx, tx.QueryRowxContext(ctx, `SELECT original_delivery_id,result_delivery_id FROM admin_webhook_retry_commands WHERE administrator_id=? AND idempotency_key=?`, actorID, idempotencyKey), &recordedOriginal, &recordedResult)
	if err == nil {
		if recordedOriginal != originalID {
			return "", ErrIdempotencyConflict
		}
		return recordedResult, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	var eventID, endpointID string
	var secretSnapshot *string
	var attempt int
	err = scanCommandRow(ctx, tx.QueryRowxContext(ctx, `SELECT event_id,endpoint_id,signing_secret_snapshot,attempt FROM webhook_deliveries WHERE id=? AND status IN ('failed','exhausted')`, originalID), &eventID, &endpointID, &secretSnapshot, &attempt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	var activeSuccessors int
	if err := tx.GetContext(ctx, &activeSuccessors, `SELECT COUNT(*) FROM webhook_deliveries WHERE event_id=? AND endpoint_id=? AND status IN ('pending','delivering')`, eventID, endpointID); err != nil {
		return "", err
	}
	// Automatic delivery failure durably creates the next pending attempt in
	// the same transaction. Do not let an administrator create a second branch
	// of that retry chain: parallel successors can arrive out of order and can
	// duplicate the merchant side effect. A terminal failure with no successor
	// (for example, an imported/seeded failed row) remains manually recoverable.
	if activeSuccessors != 0 {
		return "", ErrIdempotencyConflict
	}
	if err := tx.GetContext(ctx, &attempt, `SELECT MAX(attempt) FROM webhook_deliveries WHERE event_id=? AND endpoint_id=?`, eventID, endpointID); err != nil {
		return "", err
	}
	id := uuid.NewString()
	if _, err = tx.ExecContext(ctx, `INSERT INTO webhook_deliveries(id,event_id,endpoint_id,signing_secret_snapshot,attempt,status,created_at) VALUES (?,?,?,?,?,'pending',?)`, id, eventID, endpointID, secretSnapshot, attempt+1, at); err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO admin_webhook_retry_commands(id,administrator_id,original_delivery_id,result_delivery_id,idempotency_key,payload_hash,created_at) VALUES (?,?,?,?,?,?,?)`, uuid.NewString(), actorID, originalID, id, idempotencyKey, payloadHash[:], at); err != nil {
		return "", err
	}
	if err := insertAudit(ctx, tx, actorID, "webhook_delivery.retried", "webhook_delivery", id, "retried delivery "+originalID, at); err != nil {
		return "", err
	}
	return id, tx.Commit()
}
