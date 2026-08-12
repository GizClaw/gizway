// Package store implements Gizway's SQL-backed application queries and commands.
package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/idy/gizway/internal/service/quotaexchange"
	"github.com/idy/gizway/internal/timetext"
	"github.com/jmoiron/sqlx"
)

// ErrNotFound indicates that a requested or authenticated resource does not exist.
var (
	ErrNotFound             = errors.New("resource not found")
	ErrInsufficientBalance  = errors.New("insufficient balance")
	ErrIdempotencyConflict  = errors.New("idempotency key reused with different payload")
	ErrCommandInProgress    = errors.New("idempotent command is already in progress")
	ErrRiskDenied           = errors.New("merchant service is not approved or exceeds risk limits")
	ErrAccountFrozen        = errors.New("account balance is frozen")
	ErrRealtimeSessionLimit = errors.New("realtime concurrent session limit reached")
	ErrRateLimited          = errors.New("request rate limit exceeded")
	ErrUCGIDConflict        = errors.New("UCGID reused with different usage")
	ErrUnpriceableUsage     = errors.New("usage cannot be priced from the referenced publication")
)

// Store executes application queries and commands against a SQL database.
type Store struct {
	db      *boundDB
	secrets *secretCipher
	now     func() time.Time
}

// New constructs a Store over an already-owned database handle.
func New(db *sqlx.DB) *Store { return &Store{db: &boundDB{DB: db}, now: time.Now} }

// NewWithSecretKey enables authenticated encryption for provider and webhook
// credentials stored in the application database. The key must be exactly 32
// bytes (AES-256) and is owned by process configuration, never by SQL seeds.
func NewWithSecretKey(db *sqlx.DB, key []byte) (*Store, error) {
	cipher, err := newSecretCipher(key)
	if err != nil {
		return nil, err
	}
	return &Store{db: &boundDB{DB: db}, secrets: cipher, now: time.Now}, nil
}

// ConfigureClock composes the business clock used for authentication,
// validity windows, idempotency retention and durable timestamps. Production
// keeps time.Now; story mode supplies one controllable fixture clock.
func (s *Store) ConfigureClock(now func() time.Time) {
	if now != nil {
		s.now = now
	}
}

// JSON preserves a JSON value as PostgreSQL JSONB without exposing driver types.
type JSON []byte

// Scan implements sql.Scanner for string and byte-backed database values.
func (value *JSON) Scan(source any) error {
	switch source := source.(type) {
	case nil:
		*value = nil
		return nil
	case string:
		*value = append((*value)[:0], source...)
	case []byte:
		*value = append((*value)[:0], source...)
	default:
		return fmt.Errorf("scan JSON from %T", source)
	}
	if !json.Valid(*value) {
		return errors.New("scan invalid JSON")
	}
	return nil
}

// MarshalJSON emits the stored JSON value without double encoding it.
func (value JSON) MarshalJSON() ([]byte, error) {
	if len(value) == 0 {
		return []byte("null"), nil
	}
	if !json.Valid(value) {
		return nil, errors.New("marshal invalid JSON")
	}
	return value, nil
}

// UnmarshalJSON validates and owns an incoming JSON value.
func (value *JSON) UnmarshalJSON(encoded []byte) error {
	if !json.Valid(encoded) {
		return errors.New("unmarshal invalid JSON")
	}
	*value = append((*value)[:0], encoded...)
	return nil
}

// User is the account-facing user representation.
type User struct {
	ID          string `db:"id" json:"id"`
	Email       string `db:"email" json:"email"`
	DisplayName string `db:"display_name" json:"display_name"`
	Status      string `db:"status" json:"status"`
	CreatedAt   string `db:"created_at" json:"created_at"`
}

// Account is a personal or merchant account owned by a user.
type Account struct {
	ID        string `db:"id" json:"id"`
	Kind      string `db:"kind" json:"kind"`
	Name      string `db:"name" json:"name"`
	Status    string `db:"status" json:"status"`
	CreatedAt string `db:"created_at" json:"created_at"`
}

// Balance separates the signed posted ledger fact from nonnegative amounts a
// caller may use. Recent regional Usage may still be pending at GizWay, so this
// is an eventual center view, not a completeness claim.
type Balance struct {
	AccountID          string  `db:"account_id" json:"account_id"`
	Asset              string  `db:"asset_code" json:"asset"`
	Amount             int64   `db:"balance_microcredits" json:"microcredits"`
	PostedMicrocredits int64   `json:"posted_microcredits"`
	PaymentHeld        int64   `db:"payment_held_microcredits" json:"payment_held_microcredits"`
	AISpendable        int64   `json:"ai_spendable_microcredits"`
	Transferable       int64   `json:"transferable_microcredits"`
	Refundable         int64   `db:"refundable_microcredits" json:"refundable_microcredits"`
	AsOf               string  `json:"as_of"`
	UpdatedAt          *string `db:"updated_at" json:"updated_at"`
}

// APIKey is the public, secret-free representation of an account credential.
type APIKey struct {
	ID         string  `db:"id" json:"id"`
	AccountID  string  `db:"account_id" json:"account_id"`
	Name       string  `db:"name" json:"name"`
	Kind       string  `db:"kind" json:"kind"`
	KeyPrefix  string  `db:"key_prefix" json:"key_prefix"`
	Scopes     JSON    `db:"scopes" json:"scopes"`
	Status     string  `db:"status" json:"status"`
	ExpiresAt  *string `db:"expires_at" json:"expires_at"`
	LastUsedAt *string `db:"last_used_at" json:"last_used_at"`
	CreatedAt  string  `db:"created_at" json:"created_at"`
}

// MerchantAccount is an owned account plus its review state.
type MerchantAccount struct {
	Account        Account `json:"account"`
	LegalName      string  `json:"legal_name"`
	PublicName     string  `json:"public_name"`
	ReviewLevel    string  `json:"review_level"`
	MerchantStatus string  `json:"merchant_status"`
	CountryCode    *string `json:"country_code"`
	WebsiteURL     *string `json:"website_url"`
}

// AccountTransaction is one account-visible ledger posting.
type AccountTransaction struct {
	ID          string         `db:"id" json:"id"`
	Type        string         `db:"transaction_type" json:"type"`
	Direction   string         `db:"direction" json:"direction"`
	Amount      map[string]any `json:"amount"`
	Status      string         `db:"status" json:"status"`
	Description string         `db:"description" json:"description"`
	CreatedAt   string         `db:"created_at" json:"created_at"`
}

// CreditAmount is the wire representation of an integer Credit amount.
type CreditAmount struct {
	Asset        string `json:"asset"`
	Microcredits int64  `json:"microcredits"`
}

// CreditTransfer is the public transfer projection.
type CreditTransfer struct {
	ID                 string       `db:"id" json:"id"`
	SenderAccountID    string       `db:"sender_account_id" json:"sender_account_id"`
	RecipientAccountID string       `db:"recipient_account_id" json:"recipient_account_id"`
	Amount             CreditAmount `json:"amount"`
	Status             string       `db:"status" json:"status"`
	Note               string       `db:"note" json:"note"`
	CreatedAt          string       `db:"created_at" json:"created_at"`
	CompletedAt        *string      `db:"completed_at" json:"completed_at"`
	Direction          string       `json:"direction,omitempty"`
}

// Model is a canonical public model.
type Model struct {
	ID        string `db:"id" json:"id"`
	Slug      string `db:"slug" json:"slug"`
	Name      string `db:"name" json:"name"`
	Modality  JSON   `db:"modality" json:"modality"`
	Status    string `db:"status" json:"status"`
	Metadata  JSON   `db:"metadata" json:"metadata"`
	CreatedAt string `db:"created_at" json:"created_at"`
	UpdatedAt string `db:"updated_at" json:"updated_at"`
}

// ModelVariant maps a canonical model to one provider endpoint.
type ModelVariant struct {
	ID                 string `db:"id" json:"id"`
	ModelID            string `db:"model_id" json:"model_id"`
	ProviderEndpointID string `db:"provider_endpoint_id" json:"provider_endpoint_id"`
	ProviderModelName  string `db:"provider_model_name" json:"provider_model_name"`
	VariantSlug        string `db:"variant_slug" json:"variant_slug"`
	Capabilities       JSON   `db:"capabilities" json:"capabilities"`
	ContextWindow      *int64 `db:"context_window" json:"context_window"`
	MaxOutputTokens    *int64 `db:"max_output_tokens" json:"max_output_tokens"`
	Status             string `db:"status" json:"status"`
	CreatedAt          string `db:"created_at" json:"created_at"`
	UpdatedAt          string `db:"updated_at" json:"updated_at"`
}

// ModelPrice is one immutable effective price version.
type ModelPrice struct {
	ID                            string  `db:"id" json:"id"`
	ModelVariantID                string  `db:"model_variant_id" json:"model_variant_id"`
	Metric                        string  `db:"metric" json:"metric"`
	UnitSize                      int64   `db:"unit_size" json:"unit_size"`
	UpstreamCostMicrocredits      int64   `db:"upstream_cost_microcredits" json:"upstream_cost_microcredits"`
	BaseCustomerPriceMicrocredits int64   `db:"base_customer_price_microcredits" json:"base_customer_price_microcredits"`
	CustomerPriceMicrocredits     int64   `db:"customer_price_microcredits" json:"customer_price_microcredits"`
	DiscountBPS                   int     `db:"discount_bps" json:"discount_bps"`
	ValidFrom                     string  `db:"valid_from" json:"valid_from"`
	ValidUntil                    *string `db:"valid_until" json:"valid_until"`
	CreatedAt                     string  `db:"created_at" json:"created_at"`
}

// PublicModel is the secret-free OpenAI-compatible model projection.
type PublicModel struct {
	ID      string `db:"slug" json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// AuthenticateUserSession returns an active user and the user's personal
// account. Sessions are intentionally distinct from Gateway and Payment keys.
func (s *Store) AuthenticateUserSession(ctx context.Context, secret string) (string, string, error) {
	hash := sha256.Sum256([]byte(secret))
	var result struct {
		UserID    string `db:"user_id"`
		AccountID string `db:"account_id"`
	}
	err := s.db.GetContext(ctx, &result, `
		SELECT u.id AS user_id, a.id AS account_id
		FROM user_sessions session
		JOIN users u ON u.id = session.user_id
		JOIN accounts a ON a.owner_user_id = u.id AND a.kind = 'personal'
		WHERE session.secret_hash = ? AND session.status = 'active'
		  AND session.expires_at > ? AND u.status = 'active' AND a.status = 'active'
	`, hash[:], timetext.Format(s.now()))
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrNotFound
	}
	if err != nil {
		return "", "", fmt.Errorf("authenticate user session: %w", err)
	}
	return result.UserID, result.AccountID, nil
}

// AuthenticateAdminKey verifies an active administrator API key.
func (s *Store) AuthenticateAdminKey(ctx context.Context, secret string) (string, error) {
	hash := sha256.Sum256([]byte(secret))
	var administratorID string
	err := s.db.GetContext(ctx, &administratorID, `
		SELECT k.administrator_id
		FROM admin_api_keys k JOIN administrators a ON a.id = k.administrator_id
		WHERE k.secret_hash = ? AND k.status = 'active' AND a.status = 'active'
		  AND (k.expires_at IS NULL OR k.expires_at > ?)
	`, hash[:], timetext.Format(s.now()))
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("authenticate admin key: %w", err)
	}
	return administratorID, nil
}

// GetUser returns one user.
func (s *Store) GetUser(ctx context.Context, id string) (User, error) {
	var user User
	if err := s.db.GetContext(ctx, &user, `SELECT id, email, display_name, status, created_at FROM users WHERE id = ?`, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, fmt.Errorf("get user: %w", err)
	}
	return user, nil
}

// UpdateUser changes the mutable profile and returns its persisted projection.
func (s *Store) UpdateUser(ctx context.Context, id, displayName string) (User, error) {
	now := timetext.Format(s.now())
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return User{}, fmt.Errorf("begin user update: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE users SET display_name = ?, updated_at = ? WHERE id = ? AND status = 'active'`, displayName, now, id)
	if err != nil {
		return User{}, fmt.Errorf("update user: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return User{}, fmt.Errorf("read updated user count: %w", err)
	}
	if rows == 0 {
		return User{}, ErrNotFound
	}
	if err := recordAudit(ctx, tx, "user", id, "user.profile_updated", "user", id, "user changed profile", "{}", now); err != nil {
		return User{}, fmt.Errorf("audit user update: %w", err)
	}
	var user User
	if err := tx.GetContext(ctx, &user, `SELECT id, email, display_name, status, created_at FROM users WHERE id = ?`, id); err != nil {
		return User{}, fmt.Errorf("read updated user: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return User{}, fmt.Errorf("commit user update: %w", err)
	}
	return user, nil
}

// ListAccounts returns accounts owned by a user.
func (s *Store) ListAccounts(ctx context.Context, userID string) ([]Account, error) {
	var accounts []Account
	if err := s.db.SelectContext(ctx, &accounts, `SELECT id, kind, name, status, created_at FROM accounts WHERE owner_user_id = ? ORDER BY created_at, id`, userID); err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	return accounts, nil
}

// GetBalance returns a balance only when the user owns the account.
func (s *Store) GetBalance(ctx context.Context, userID, accountID string) (Balance, error) {
	var balance Balance
	err := s.db.GetContext(ctx, &balance, `
		SELECT b.account_id,b.asset_code,b.balance_microcredits,b.updated_at,
		       COALESCE((SELECT SUM(r.credit_microcredits) FROM refunds r
		                 WHERE r.account_id=b.account_id AND r.status='pending'),0) +
		       COALESCE((SELECT SUM(h.amount_microcredits) FROM credit_holds h
		                 WHERE h.account_id=b.account_id AND h.status='active' AND h.expires_at>?),0)
		         AS payment_held_microcredits,
		       COALESCE((SELECT SUM(GREATEST(l.remaining_microcredits-
		                 COALESCE((SELECT SUM(r.credit_microcredits) FROM refunds r
		                           WHERE r.topup_id=l.topup_id AND r.status='pending'),0),0))
		                 FROM credit_lots l WHERE l.account_id=b.account_id),0)
		         AS refundable_microcredits
		FROM account_balances b JOIN accounts a ON a.id = b.account_id
		WHERE b.account_id = ? AND a.owner_user_id = ?
	`, timetext.Format(s.now()), accountID, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return Balance{}, ErrNotFound
	}
	if err != nil {
		return Balance{}, fmt.Errorf("get balance: %w", err)
	}
	balance.PostedMicrocredits = balance.Amount
	balance.AISpendable, _ = quotaexchange.CurrentQuota(balance.Amount, balance.PaymentHeld)
	balance.Transferable = balance.AISpendable
	balance.AsOf = timetext.Format(s.now())
	return balance, nil
}

// ListAPIKeys returns secret-free key metadata for an account owned by userID.
func (s *Store) ListAPIKeys(ctx context.Context, userID, accountID string) ([]APIKey, error) {
	var owned int
	if err := s.db.GetContext(ctx, &owned, `SELECT COUNT(*) FROM accounts WHERE id = ? AND owner_user_id = ?`, accountID, userID); err != nil {
		return nil, fmt.Errorf("check API key account ownership: %w", err)
	}
	if owned == 0 {
		return nil, ErrNotFound
	}
	var keys []APIKey
	if err := s.db.SelectContext(ctx, &keys, `
		SELECT id, account_id, name, kind, key_prefix, scopes, status,
		       expires_at, last_used_at, created_at
		FROM api_keys WHERE account_id = ? ORDER BY created_at, id
	`, accountID); err != nil {
		return nil, fmt.Errorf("list API keys: %w", err)
	}
	return keys, nil
}

// CreateAPIKey inserts a hashed credential and its audit record atomically.
func (s *Store) CreateAPIKey(ctx context.Context, userID, idempotencyKey string, payloadHash, secretHash []byte, key APIKey) (APIKey, bool, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return APIKey{}, false, fmt.Errorf("begin create API key: %w", err)
	}
	defer tx.Rollback()
	var existing struct {
		ID          string `db:"id"`
		PayloadHash []byte `db:"payload_hash"`
	}
	err = tx.GetContext(ctx, &existing, `SELECT id, payload_hash FROM api_keys WHERE account_id = ? AND idempotency_key = ?`, key.AccountID, idempotencyKey)
	if err == nil {
		if !bytes.Equal(existing.PayloadHash, payloadHash) {
			return APIKey{}, false, ErrIdempotencyConflict
		}
		var stored APIKey
		if err := tx.GetContext(ctx, &stored, `SELECT id, account_id, name, kind, key_prefix, scopes, status, expires_at, last_used_at, created_at FROM api_keys WHERE id = ?`, existing.ID); err != nil {
			return APIKey{}, false, fmt.Errorf("read idempotent API key: %w", err)
		}
		return stored, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return APIKey{}, false, fmt.Errorf("lookup idempotent API key: %w", err)
	}

	var owned int
	if err := tx.GetContext(ctx, &owned, `SELECT COUNT(*) FROM accounts WHERE id = ? AND owner_user_id = ? AND status = 'active'`, key.AccountID, userID); err != nil {
		return APIKey{}, false, fmt.Errorf("check create API key ownership: %w", err)
	}
	if owned == 0 {
		return APIKey{}, false, ErrNotFound
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO api_keys (id, account_id, kind, name, key_prefix, secret_hash,
		                      scopes, status, expires_at, created_at, idempotency_key, payload_hash)
		VALUES (?, ?, ?, ?, ?, ?, ?, 'active', ?, ?, ?, ?)
	`, key.ID, key.AccountID, key.Kind, key.Name, key.KeyPrefix, secretHash,
		string(key.Scopes), key.ExpiresAt, key.CreatedAt, idempotencyKey, payloadHash)
	if err != nil {
		return APIKey{}, false, fmt.Errorf("insert API key: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO audit_events (id, actor_type, actor_id, action, resource_type,
		                          resource_id, request_id, metadata, created_at)
		VALUES (?, 'user', ?, 'api_key.created', 'api_key', ?, ?, '{}', ?)
	`, uuid.NewString(), userID, key.ID, auditRequestID(ctx), key.CreatedAt)
	if err != nil {
		return APIKey{}, false, fmt.Errorf("audit API key creation: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return APIKey{}, false, fmt.Errorf("commit create API key: %w", err)
	}
	return key, false, nil
}

// RevokeAPIKey irreversibly revokes an owned key and audits the mutation.
func (s *Store) RevokeAPIKey(ctx context.Context, userID, accountID, keyID string) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin revoke API key: %w", err)
	}
	defer tx.Rollback()
	now := timetext.Format(s.now())
	result, err := tx.ExecContext(ctx, `
		UPDATE api_keys SET status = 'revoked', revoked_at = ?
		WHERE id = ? AND account_id = ? AND status = 'active'
		  AND EXISTS (SELECT 1 FROM accounts WHERE id = ? AND owner_user_id = ?)
	`, now, keyID, accountID, accountID, userID)
	if err != nil {
		return fmt.Errorf("revoke API key: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read revoked API key count: %w", err)
	}
	if rows == 0 {
		return ErrNotFound
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO audit_events (id, actor_type, actor_id, action, resource_type,
		                          resource_id, request_id, metadata, created_at)
		VALUES (?, 'user', ?, 'api_key.revoked', 'api_key', ?, ?, '{}', ?)
	`, uuid.NewString(), userID, keyID, auditRequestID(ctx), now)
	if err != nil {
		return fmt.Errorf("audit API key revocation: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit revoke API key: %w", err)
	}
	return nil
}

// CreateMerchantAccount creates an owned account, review profile, ledger
// account, and audit record in one transaction.
func (s *Store) CreateMerchantAccount(ctx context.Context, userID string, merchant MerchantAccount) (MerchantAccount, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return MerchantAccount{}, fmt.Errorf("begin merchant application: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO accounts (id, owner_user_id, kind, name, status, created_at, updated_at)
		VALUES (?, ?, 'merchant', ?, 'active', ?, ?)
	`, merchant.Account.ID, userID, merchant.Account.Name, merchant.Account.CreatedAt, merchant.Account.CreatedAt)
	if err != nil {
		return MerchantAccount{}, fmt.Errorf("insert merchant account: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO merchant_accounts (
			account_id, owner_user_id, legal_name, public_name, review_level,
			merchant_status, country_code, website_url, created_at, updated_at
		) VALUES (?, ?, ?, ?, 'basic', 'pending', ?, ?, ?, ?)
	`, merchant.Account.ID, userID, merchant.LegalName, merchant.PublicName,
		merchant.CountryCode, merchant.WebsiteURL, merchant.Account.CreatedAt, merchant.Account.CreatedAt)
	if err != nil {
		return MerchantAccount{}, fmt.Errorf("insert merchant review profile: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO ledger_accounts (id, owner_account_id, code, kind, asset_code,
		                             normal_balance, status, created_at, updated_at)
		VALUES (?, ?, ?, 'merchant_credit', 'GIZ_CREDIT', 'credit', 'active', ?, ?)
	`, uuid.NewString(), merchant.Account.ID, "MERCHANT:"+merchant.Account.ID, merchant.Account.CreatedAt, merchant.Account.CreatedAt)
	if err != nil {
		return MerchantAccount{}, fmt.Errorf("insert merchant ledger account: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO audit_events (id, actor_type, actor_id, action, resource_type,
		                          resource_id, request_id, metadata, created_at)
		VALUES (?, 'user', ?, 'merchant.applied', 'merchant_account', ?, ?, '{}', ?)
	`, uuid.NewString(), userID, merchant.Account.ID, auditRequestID(ctx), merchant.Account.CreatedAt)
	if err != nil {
		return MerchantAccount{}, fmt.Errorf("audit merchant application: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return MerchantAccount{}, fmt.Errorf("commit merchant application: %w", err)
	}
	return merchant, nil
}

// ListReceivedGatewayUsagePage is the GizPay account-history query. It has no
// API-key dimension and never refers to a regional execution table: GizPay can
// expose only Usage delivered through Quota Exchange and posted to the ledger.
func (s *Store) ListReceivedGatewayUsagePage(ctx context.Context, userID, accountID, from, to string, query AccountListQuery) (AccountPage[map[string]any], error) {
	limit, _, err := normalizeAccountListQuery(AccountListQuery{Limit: query.Limit})
	if err != nil {
		return AccountPage[map[string]any]{}, err
	}
	initialAsOf := query.AsOf
	if initialAsOf == "" {
		initialAsOf = timetext.Format(s.now())
	}
	offset, asOf, err := decodeReceivedUsageCursor(query.Cursor, initialAsOf)
	if err != nil {
		return AccountPage[map[string]any]{}, err
	}
	asOf, err = timetext.Normalize(asOf)
	if err != nil {
		return AccountPage[map[string]any]{}, errors.New("invalid received Usage cursor")
	}
	var owned int
	if err := s.db.GetContext(ctx, &owned, `SELECT COUNT(*) FROM accounts WHERE id=? AND owner_user_id=?`, accountID, userID); err != nil {
		return AccountPage[map[string]any]{}, fmt.Errorf("check received usage account ownership: %w", err)
	}
	if owned == 0 {
		return AccountPage[map[string]any]{}, ErrNotFound
	}

	rows, err := s.db.QueryxContext(ctx, `
		SELECT u.ucgid AS id,u.ucgid,u.public_model,u.model_variant_id,
		       COALESCE(SUM(m.quantity) FILTER (WHERE m.metric='input_token'),0)::BIGINT AS input_tokens,
		       COALESCE(SUM(m.quantity) FILTER (WHERE m.metric='output_token'),0)::BIGINT AS output_tokens,
		       COALESCE(SUM(m.quantity) FILTER (WHERE m.metric='cached_input_token'),0)::BIGINT AS cached_input_tokens,
		       COALESCE(SUM(m.quantity) FILTER (WHERE m.metric='input_audio_token'),0)::BIGINT AS input_audio_tokens,
		       COALESCE(SUM(m.quantity) FILTER (WHERE m.metric='output_audio_token'),0)::BIGINT AS output_audio_tokens,
		       u.charged_microcredits,u.started_at,u.completed_at,u.received_at
		FROM gateway_usage_records u
		LEFT JOIN gateway_usage_metrics m ON m.ucgid=u.ucgid
		WHERE u.account_id=? AND u.started_at>=? AND u.started_at<? AND u.received_at<=?
		GROUP BY u.ucgid
		ORDER BY u.started_at DESC,u.ucgid
		LIMIT ? OFFSET ?
	`, accountID, from, to, asOf, limit+1, offset)
	if err != nil {
		return AccountPage[map[string]any]{}, fmt.Errorf("list received gateway usage: %w", err)
	}
	items := make([]map[string]any, 0, limit+1)
	for rows.Next() {
		item := map[string]any{}
		if err := rows.MapScan(item); err != nil {
			_ = rows.Close()
			return AccountPage[map[string]any]{}, fmt.Errorf("scan received gateway usage: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return AccountPage[map[string]any]{}, fmt.Errorf("iterate received gateway usage: %w", err)
	}
	if err := rows.Close(); err != nil {
		return AccountPage[map[string]any]{}, fmt.Errorf("close received gateway usage rows: %w", err)
	}
	page := accountPage(items, limit, offset)
	page.AsOf = asOf
	if page.HasMore {
		cursor := encodeReceivedUsageCursor(offset+limit, asOf)
		page.NextCursor = &cursor
	}
	for _, item := range page.Items {
		var charges []struct {
			Metric         string `db:"metric" json:"metric"`
			Quantity       int64  `db:"quantity" json:"quantity"`
			UnitSize       int64  `db:"unit_size" json:"unit_size"`
			BasePrice      int64  `db:"base_price_microcredits" json:"base_price_microcredits"`
			EffectivePrice int64  `db:"effective_price_microcredits" json:"effective_price_microcredits"`
			DiscountBPS    int    `db:"discount_bps" json:"discount_bps"`
			Charged        int64  `db:"charged_microcredits" json:"charged_microcredits"`
		}
		if err := s.db.SelectContext(ctx, &charges, `SELECT m.metric,m.quantity,m.unit_size,
			p.base_price_microcredits,m.price_microcredits AS effective_price_microcredits,
			p.discount_bps,m.charged_microcredits
			FROM gateway_usage_metrics m JOIN billing_rate_versions p ON p.id=m.rate_version_id
			WHERE m.ucgid=? ORDER BY m.metric`, databaseText(item["ucgid"])); err != nil {
			return AccountPage[map[string]any]{}, fmt.Errorf("list received usage charges: %w", err)
		}
		item["charges"] = charges
	}
	return page, nil
}

func databaseText(value any) string {
	switch value := value.(type) {
	case string:
		return value
	case []byte:
		return string(value)
	default:
		return fmt.Sprint(value)
	}
}

// ListAccountTransactions projects immutable ledger entries for an owner.
func (s *Store) ListAccountTransactionsPage(ctx context.Context, userID, accountID string, query AccountListQuery) (AccountPage[AccountTransaction], error) {
	limit, offset, err := normalizeAccountListQuery(query)
	if err != nil {
		return AccountPage[AccountTransaction]{}, err
	}
	var owned int
	if err := s.db.GetContext(ctx, &owned, `SELECT COUNT(*) FROM accounts WHERE id = ? AND owner_user_id = ?`, accountID, userID); err != nil {
		return AccountPage[AccountTransaction]{}, fmt.Errorf("check transaction account ownership: %w", err)
	}
	if owned == 0 {
		return AccountPage[AccountTransaction]{}, ErrNotFound
	}
	var rows []struct {
		ID          string `db:"id"`
		Type        string `db:"transaction_type"`
		Direction   string `db:"direction"`
		Amount      int64  `db:"amount_microcredits"`
		Asset       string `db:"asset_code"`
		Status      string `db:"status"`
		Description string `db:"description"`
		CreatedAt   string `db:"created_at"`
	}
	if err := s.db.SelectContext(ctx, &rows, `
		SELECT lt.id, lt.transaction_type,
		       CASE WHEN le.direction = la.normal_balance THEN 'incoming' ELSE 'outgoing' END AS direction,
		       le.amount_microcredits, la.asset_code, lt.status, lt.description, lt.created_at
		FROM ledger_entries le
		JOIN ledger_accounts la ON la.id = le.ledger_account_id
		JOIN ledger_transactions lt ON lt.id = le.transaction_id
		WHERE la.owner_account_id = ?
		ORDER BY lt.created_at DESC, lt.id
		LIMIT ? OFFSET ?
	`, accountID, limit+1, offset); err != nil {
		return AccountPage[AccountTransaction]{}, fmt.Errorf("list account transactions: %w", err)
	}
	transactions := make([]AccountTransaction, 0, len(rows))
	for _, row := range rows {
		transactions = append(transactions, AccountTransaction{
			ID: row.ID, Type: row.Type, Direction: row.Direction,
			Amount: map[string]any{"asset": row.Asset, "microcredits": row.Amount},
			Status: row.Status, Description: row.Description, CreatedAt: row.CreatedAt,
		})
	}
	return accountPage(transactions, limit, offset), nil
}

// CreateCreditTransfer moves posted Credit with one balanced ledger posting.
// The boolean result is true when an earlier identical command is replayed.
func (s *Store) CreateCreditTransfer(ctx context.Context, userID, idempotencyKey string, payloadHash []byte, transfer CreditTransfer) (CreditTransfer, bool, error) {
	type result struct {
		transfer CreditTransfer
		replayed bool
	}
	value, err := retrySerializable(ctx, func() (result, error) {
		created, replayed, commandErr := s.createCreditTransfer(ctx, userID, idempotencyKey, payloadHash, transfer)
		return result{transfer: created, replayed: replayed}, commandErr
	})
	return value.transfer, value.replayed, err
}

func (s *Store) createCreditTransfer(ctx context.Context, userID, idempotencyKey string, payloadHash []byte, transfer CreditTransfer) (CreditTransfer, bool, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return CreditTransfer{}, false, fmt.Errorf("begin credit transfer: %w", err)
	}
	defer tx.Rollback()

	var existing struct {
		ID          string `db:"id"`
		PayloadHash []byte `db:"payload_hash"`
	}
	err = tx.GetContext(ctx, &existing, `SELECT id, payload_hash FROM credit_transfers WHERE sender_account_id = ? AND idempotency_key = ?`, transfer.SenderAccountID, idempotencyKey)
	if err == nil {
		if !bytes.Equal(existing.PayloadHash, payloadHash) {
			return CreditTransfer{}, false, ErrIdempotencyConflict
		}
		stored, err := getCreditTransfer(ctx, tx, existing.ID)
		return stored, true, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return CreditTransfer{}, false, fmt.Errorf("lookup idempotent transfer: %w", err)
	}

	var senderStatus string
	err = tx.GetContext(ctx, &senderStatus, `SELECT status FROM accounts WHERE id = ? AND owner_user_id = ?`, transfer.SenderAccountID, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return CreditTransfer{}, false, ErrNotFound
	}
	if err != nil {
		return CreditTransfer{}, false, fmt.Errorf("read sender account: %w", err)
	}
	if senderStatus != "active" {
		return CreditTransfer{}, false, ErrInsufficientBalance
	}
	var recipientStatus string
	err = tx.GetContext(ctx, &recipientStatus, `SELECT status FROM accounts WHERE id = ?`, transfer.RecipientAccountID)
	if errors.Is(err, sql.ErrNoRows) || recipientStatus != "active" {
		return CreditTransfer{}, false, ErrNotFound
	}
	if err != nil {
		return CreditTransfer{}, false, fmt.Errorf("read recipient account: %w", err)
	}

	available, err := availableCredit(ctx, tx, transfer.SenderAccountID)
	if err != nil {
		return CreditTransfer{}, false, fmt.Errorf("read sender balance: %w", err)
	}
	if available < transfer.Amount.Microcredits {
		return CreditTransfer{}, false, ErrInsufficientBalance
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO credit_transfers (id, sender_account_id, recipient_account_id,
		 amount_microcredits, status, note, idempotency_key, payload_hash, created_at, completed_at)
		VALUES (?, ?, ?, ?, 'succeeded', ?, ?, ?, ?, ?)
	`, transfer.ID, transfer.SenderAccountID, transfer.RecipientAccountID,
		transfer.Amount.Microcredits, transfer.Note, idempotencyKey, payloadHash,
		transfer.CreatedAt, transfer.CompletedAt)
	if err != nil {
		return CreditTransfer{}, false, fmt.Errorf("insert credit transfer: %w", err)
	}

	var senderLedgerID, recipientLedgerID string
	if err := tx.GetContext(ctx, &senderLedgerID, `SELECT id FROM ledger_accounts WHERE owner_account_id = ? AND asset_code = 'GIZ_CREDIT' AND status = 'active'`, transfer.SenderAccountID); err != nil {
		return CreditTransfer{}, false, fmt.Errorf("read sender ledger account: %w", err)
	}
	if err := tx.GetContext(ctx, &recipientLedgerID, `SELECT id FROM ledger_accounts WHERE owner_account_id = ? AND asset_code = 'GIZ_CREDIT' AND status = 'active'`, transfer.RecipientAccountID); err != nil {
		return CreditTransfer{}, false, fmt.Errorf("read recipient ledger account: %w", err)
	}
	ledgerTransactionID := uuid.NewString()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO ledger_transactions (id, transaction_type, status, idempotency_key,
		 initiated_by_account_id, reference_type, reference_id, description, created_at, posted_at)
		VALUES (?, 'transfer', 'posted', ?, ?, 'credit_transfer', ?, ?, ?, ?)
	`, ledgerTransactionID, "transfer:"+transfer.SenderAccountID+":"+idempotencyKey,
		transfer.SenderAccountID, transfer.ID, transfer.Note, transfer.CreatedAt, transfer.CreatedAt)
	if err != nil {
		return CreditTransfer{}, false, fmt.Errorf("insert transfer ledger transaction: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO ledger_entries (id, transaction_id, ledger_account_id, sequence, direction, amount_microcredits, created_at)
		VALUES (?, ?, ?, 1, 'debit', ?, ?), (?, ?, ?, 2, 'credit', ?, ?)
	`, uuid.NewString(), ledgerTransactionID, senderLedgerID, transfer.Amount.Microcredits, transfer.CreatedAt,
		uuid.NewString(), ledgerTransactionID, recipientLedgerID, transfer.Amount.Microcredits, transfer.CreatedAt)
	if err != nil {
		return CreditTransfer{}, false, fmt.Errorf("insert transfer ledger entries: %w", err)
	}
	if err := consumePurchasedLots(ctx, tx, transfer.SenderAccountID, transfer.Amount.Microcredits); err != nil {
		return CreditTransfer{}, false, fmt.Errorf("consume refundable Credit for transfer: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO audit_events (id, actor_type, actor_id, action, resource_type, resource_id, request_id, metadata, created_at)
		VALUES (?, 'user', ?, 'credit_transfer.created', 'credit_transfer', ?, ?, '{}', ?)
	`, uuid.NewString(), userID, transfer.ID, auditRequestID(ctx), transfer.CreatedAt)
	if err != nil {
		return CreditTransfer{}, false, fmt.Errorf("audit credit transfer: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return CreditTransfer{}, false, fmt.Errorf("commit credit transfer: %w", err)
	}
	return transfer, false, nil
}

type sqlxGetter interface {
	GetContext(context.Context, any, string, ...any) error
}

func getCreditTransfer(ctx context.Context, query sqlxGetter, id string) (CreditTransfer, error) {
	var row struct {
		ID                 string  `db:"id"`
		SenderAccountID    string  `db:"sender_account_id"`
		RecipientAccountID string  `db:"recipient_account_id"`
		Amount             int64   `db:"amount_microcredits"`
		Status             string  `db:"status"`
		Note               string  `db:"note"`
		CreatedAt          string  `db:"created_at"`
		CompletedAt        *string `db:"completed_at"`
	}
	if err := query.GetContext(ctx, &row, `
		SELECT id, sender_account_id, recipient_account_id, amount_microcredits,
		       status, note, created_at, completed_at FROM credit_transfers WHERE id = ?
	`, id); err != nil {
		return CreditTransfer{}, fmt.Errorf("read credit transfer: %w", err)
	}
	return CreditTransfer{ID: row.ID, SenderAccountID: row.SenderAccountID,
		RecipientAccountID: row.RecipientAccountID,
		Amount:             CreditAmount{Asset: "GIZ_CREDIT", Microcredits: row.Amount},
		Status:             row.Status, Note: row.Note, CreatedAt: row.CreatedAt, CompletedAt: row.CompletedAt}, nil
}

// ListCreditTransfers returns both sent and received transfers for an owner.
func (s *Store) ListCreditTransfersPage(ctx context.Context, userID, accountID string, query AccountListQuery) (AccountPage[CreditTransfer], error) {
	limit, offset, err := normalizeAccountListQuery(query)
	if err != nil {
		return AccountPage[CreditTransfer]{}, err
	}
	var owned int
	if err := s.db.GetContext(ctx, &owned, `SELECT COUNT(*) FROM accounts WHERE id = ? AND owner_user_id = ?`, accountID, userID); err != nil {
		return AccountPage[CreditTransfer]{}, fmt.Errorf("check transfer account ownership: %w", err)
	}
	if owned == 0 {
		return AccountPage[CreditTransfer]{}, ErrNotFound
	}
	var rows []struct {
		ID                 string  `db:"id"`
		SenderAccountID    string  `db:"sender_account_id"`
		RecipientAccountID string  `db:"recipient_account_id"`
		Amount             int64   `db:"amount_microcredits"`
		Status             string  `db:"status"`
		Note               string  `db:"note"`
		CreatedAt          string  `db:"created_at"`
		CompletedAt        *string `db:"completed_at"`
	}
	if err := s.db.SelectContext(ctx, &rows, `
		SELECT id, sender_account_id, recipient_account_id, amount_microcredits,
		       status, note, created_at, completed_at
		FROM credit_transfers WHERE sender_account_id = ? OR recipient_account_id = ?
		ORDER BY created_at DESC, id
		LIMIT ? OFFSET ?
	`, accountID, accountID, limit+1, offset); err != nil {
		return AccountPage[CreditTransfer]{}, fmt.Errorf("list credit transfers: %w", err)
	}
	result := make([]CreditTransfer, 0, len(rows))
	for _, row := range rows {
		direction := "incoming"
		if row.SenderAccountID == accountID {
			direction = "outgoing"
		}
		result = append(result, CreditTransfer{ID: row.ID, SenderAccountID: row.SenderAccountID,
			RecipientAccountID: row.RecipientAccountID,
			Amount:             CreditAmount{Asset: "GIZ_CREDIT", Microcredits: row.Amount},
			Status:             row.Status, Note: row.Note, CreatedAt: row.CreatedAt,
			CompletedAt: row.CompletedAt, Direction: direction})
	}
	return accountPage(result, limit, offset), nil
}

// ListPublicModelsForAccount filters both account policy and protocol
// capability before a compatible API advertises a model as usable.
func (s *Store) ListPublicModelsForAccount(ctx context.Context, accountID, protocol string, at time.Time) ([]PublicModel, error) {
	rows, err := s.listExecutableCatalog(ctx, accountID, timetext.Format(at))
	if err != nil {
		return nil, fmt.Errorf("list public models: %w", err)
	}
	models := make([]PublicModel, 0, len(rows))
	for _, row := range rows {
		if !catalogSupportsProtocol(row.Capabilities, protocol) {
			continue
		}
		created, err := time.Parse(time.RFC3339, row.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("parse model creation time: %w", err)
		}
		models = append(models, PublicModel{ID: row.ID, Object: "model", Created: created.Unix(), OwnedBy: "gizway"})
	}
	return models, nil
}

func catalogSupportsProtocol(capabilities []string, protocol string) bool {
	if protocol == "" {
		return true
	}
	allowed := map[string]map[string]bool{
		"anthropic": {"messages": true},
		"gemini":    {"generateContent": true},
		"openai": {
			"chat": true, "responses": true, "embeddings": true,
			"audio_speech": true, "audio_transcriptions": true,
			"image_generation": true, "realtime": true,
		},
	}
	for _, capability := range capabilities {
		if allowed[protocol][capability] {
			return true
		}
	}
	return false
}

// CreateModel inserts a canonical model.
func (s *Store) CreateModel(ctx context.Context, actorID string, model Model) (Model, error) {
	now := timetext.Format(s.now())
	model.ID = uuid.NewString()
	model.Status = "active"
	model.CreatedAt = now
	model.UpdatedAt = now
	if len(model.Metadata) == 0 {
		model.Metadata = JSON(`{}`)
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return Model{}, fmt.Errorf("begin model creation: %w", err)
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO models (id, slug, name, modality, status, metadata, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, model.ID, model.Slug, model.Name, string(model.Modality), model.Status, string(model.Metadata), now, now)
	if err != nil {
		return Model{}, fmt.Errorf("create model: %w", err)
	}
	if err := insertAudit(ctx, tx, actorID, "model.created", "model", model.ID, "created model", now); err != nil {
		return Model{}, fmt.Errorf("audit model creation: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Model{}, fmt.Errorf("commit model creation: %w", err)
	}
	return model, nil
}

// UpdateModel changes mutable canonical model fields.
func (s *Store) UpdateModel(ctx context.Context, actorID, id, name, status string) (Model, error) {
	now := timetext.Format(s.now())
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return Model{}, fmt.Errorf("begin model update: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE models SET name = ?, status = ?, updated_at = ? WHERE id = ?`, name, status, now, id)
	if err != nil {
		return Model{}, fmt.Errorf("update model: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return Model{}, fmt.Errorf("read updated model count: %w", err)
	}
	if rows == 0 {
		return Model{}, ErrNotFound
	}
	var model Model
	if err := tx.GetContext(ctx, &model, `SELECT id, slug, name, modality, status, metadata, created_at, updated_at FROM models WHERE id = ?`, id); err != nil {
		return Model{}, fmt.Errorf("read updated model: %w", err)
	}
	if err := insertAudit(ctx, tx, actorID, "model.updated", "model", id, "updated model", now); err != nil {
		return Model{}, fmt.Errorf("audit model update: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Model{}, fmt.Errorf("commit model update: %w", err)
	}
	return model, nil
}

// ListModelVariants returns variants of a canonical model.
func (s *Store) ListModelVariants(ctx context.Context, modelID string) ([]ModelVariant, error) {
	var variants []ModelVariant
	if err := s.db.SelectContext(ctx, &variants, `
		SELECT id, model_id, provider_endpoint_id, provider_model_name, variant_slug,
		       capabilities, context_window, max_output_tokens, status, created_at, updated_at
		FROM model_variants WHERE model_id = ? ORDER BY variant_slug
	`, modelID); err != nil {
		return nil, fmt.Errorf("list model variants: %w", err)
	}
	return variants, nil
}

// CreateModelVariant inserts one provider-backed model variant.
func (s *Store) CreateModelVariant(ctx context.Context, actorID string, variant ModelVariant) (ModelVariant, error) {
	now := timetext.Format(s.now())
	variant.ID = uuid.NewString()
	variant.Status = "active"
	variant.CreatedAt = now
	variant.UpdatedAt = now
	if len(variant.Capabilities) == 0 {
		variant.Capabilities = JSON(`{}`)
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return ModelVariant{}, fmt.Errorf("begin model variant creation: %w", err)
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO model_variants (
			id, model_id, provider_endpoint_id, provider_model_name, variant_slug,
			capabilities, context_window, max_output_tokens, status, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, variant.ID, variant.ModelID, variant.ProviderEndpointID, variant.ProviderModelName,
		variant.VariantSlug, string(variant.Capabilities), variant.ContextWindow,
		variant.MaxOutputTokens, variant.Status, now, now)
	if err != nil {
		return ModelVariant{}, fmt.Errorf("create model variant: %w", err)
	}
	if err := insertAudit(ctx, tx, actorID, "model_variant.created", "model_variant", variant.ID, "created model variant", now); err != nil {
		return ModelVariant{}, fmt.Errorf("audit model variant creation: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return ModelVariant{}, fmt.Errorf("commit model variant creation: %w", err)
	}
	return variant, nil
}

// UpdateModelVariant changes mutable serving fields while retaining history.
func (s *Store) UpdateModelVariant(ctx context.Context, actorID string, variant ModelVariant) (ModelVariant, error) {
	variant.UpdatedAt = timetext.Format(s.now())
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return ModelVariant{}, fmt.Errorf("begin model variant update: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		UPDATE model_variants SET provider_model_name = ?, capabilities = ?,
		 context_window = ?, max_output_tokens = ?, status = ?, updated_at = ?
		WHERE id = ?
	`, variant.ProviderModelName, string(variant.Capabilities), variant.ContextWindow,
		variant.MaxOutputTokens, variant.Status, variant.UpdatedAt, variant.ID)
	if err != nil {
		return ModelVariant{}, fmt.Errorf("update model variant: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return ModelVariant{}, fmt.Errorf("read updated variant count: %w", err)
	}
	if rows == 0 {
		return ModelVariant{}, ErrNotFound
	}
	if err := tx.GetContext(ctx, &variant, `
		SELECT id, model_id, provider_endpoint_id, provider_model_name, variant_slug,
		 capabilities, context_window, max_output_tokens, status, created_at, updated_at
		FROM model_variants WHERE id = ?
	`, variant.ID); err != nil {
		return ModelVariant{}, fmt.Errorf("read updated model variant: %w", err)
	}
	if err := insertAudit(ctx, tx, actorID, "model_variant.updated", "model_variant", variant.ID, "updated model variant", variant.UpdatedAt); err != nil {
		return ModelVariant{}, fmt.Errorf("audit model variant update: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return ModelVariant{}, fmt.Errorf("commit model variant update: %w", err)
	}
	return variant, nil
}

// ListModelPrices returns all price versions for a model variant.
func (s *Store) ListModelPrices(ctx context.Context, variantID string) ([]ModelPrice, error) {
	var prices []ModelPrice
	if err := s.db.SelectContext(ctx, &prices, `
		SELECT id, model_variant_id, metric, unit_size, upstream_cost_microcredits,
		       base_customer_price_microcredits, customer_price_microcredits,
		       discount_bps, valid_from, valid_until, created_at
		FROM model_variant_prices WHERE model_variant_id = ? ORDER BY valid_from DESC, metric
	`, variantID); err != nil {
		return nil, fmt.Errorf("list model prices: %w", err)
	}
	return prices, nil
}

// CreateModelPrice publishes an immutable price version.
func (s *Store) CreateModelPrice(ctx context.Context, actorID string, price ModelPrice) (ModelPrice, error) {
	return retrySerializable(ctx, func() (ModelPrice, error) {
		return s.createModelPrice(ctx, actorID, price)
	})
}

func (s *Store) createModelPrice(ctx context.Context, actorID string, price ModelPrice) (ModelPrice, error) {
	price.ID = uuid.NewString()
	price.CreatedAt = timetext.Format(s.now())
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return ModelPrice{}, fmt.Errorf("begin model price publication: %w", err)
	}
	defer tx.Rollback()
	var overlaps int
	if err := tx.GetContext(ctx, &overlaps, `
		SELECT COUNT(*) FROM model_variant_prices
		WHERE model_variant_id = ? AND metric = ?
		  AND (CAST(? AS TEXT) IS NULL OR valid_from < ?)
		  AND (valid_until IS NULL OR valid_until > ?)
	`, price.ModelVariantID, price.Metric, price.ValidUntil, price.ValidUntil, price.ValidFrom); err != nil {
		return ModelPrice{}, fmt.Errorf("check price overlap: %w", err)
	}
	if overlaps > 0 {
		return ModelPrice{}, fmt.Errorf("price interval constraint: overlapping price version")
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO model_variant_prices (
			id, model_variant_id, metric, unit_size, upstream_cost_microcredits,
			base_customer_price_microcredits, customer_price_microcredits,
			discount_bps, valid_from, valid_until, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, price.ID, price.ModelVariantID, price.Metric, price.UnitSize,
		price.UpstreamCostMicrocredits, price.BaseCustomerPriceMicrocredits,
		price.CustomerPriceMicrocredits, price.DiscountBPS, price.ValidFrom,
		price.ValidUntil, price.CreatedAt)
	if err != nil {
		return ModelPrice{}, fmt.Errorf("create model price: %w", err)
	}
	if err := insertAudit(ctx, tx, actorID, "model_price.created", "model_price", price.ID, "published model price", price.CreatedAt); err != nil {
		return ModelPrice{}, fmt.Errorf("audit model price publication: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return ModelPrice{}, fmt.Errorf("commit model price: %w", err)
	}
	return price, nil
}
