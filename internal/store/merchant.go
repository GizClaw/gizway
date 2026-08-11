package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/idy/gizway/internal/timetext"
)

type PaymentPrincipal struct {
	UserID    string
	AccountID string
	APIKeyID  string
	Scopes    JSON
}

func (s *Store) AuthenticatePaymentKey(ctx context.Context, secret string, at string) (PaymentPrincipal, error) {
	hash := sha256.Sum256([]byte(secret))
	var row struct {
		UserID    string `db:"user_id"`
		AccountID string `db:"account_id"`
		APIKeyID  string `db:"api_key_id"`
		Scopes    JSON   `db:"scopes"`
	}
	err := s.db.GetContext(ctx, &row, `SELECT a.owner_user_id AS user_id,a.id AS account_id,k.id AS api_key_id,k.scopes
		FROM api_keys k JOIN accounts a ON a.id=k.account_id
		JOIN users u ON u.id=a.owner_user_id JOIN merchant_accounts m ON m.account_id=a.id
		WHERE k.secret_hash=? AND k.kind='payment' AND k.status='active'
		AND (k.expires_at IS NULL OR k.expires_at>?) AND a.status='active'
		AND u.status='active' AND m.merchant_status='approved'`, hash[:], at)
	if errors.Is(err, sql.ErrNoRows) {
		return PaymentPrincipal{}, ErrNotFound
	}
	if err != nil {
		return PaymentPrincipal{}, fmt.Errorf("authenticate Payment key: %w", err)
	}
	return PaymentPrincipal{UserID: row.UserID, AccountID: row.AccountID, APIKeyID: row.APIKeyID, Scopes: row.Scopes}, nil
}

type PaymentIntent struct {
	Object            string       `json:"object"`
	ID                string       `db:"id" json:"id"`
	MerchantAccountID string       `db:"merchant_account_id" json:"merchant_account_id"`
	ServiceID         string       `db:"service_id" json:"service_id"`
	ServiceCode       string       `db:"service_code" json:"service_code"`
	ServiceName       string       `db:"service_name" json:"service_name"`
	PayerAccountID    *string      `db:"payer_account_id" json:"payer_account_id"`
	ExternalOrderID   string       `db:"external_order_id" json:"external_order_id"`
	Amount            CreditAmount `json:"amount"`
	PlatformFee       CreditAmount `json:"platform_fee"`
	NetAmount         CreditAmount `json:"net_amount"`
	FeeBPS            int          `db:"fee_bps" json:"fee_bps"`
	Status            string       `db:"status" json:"status"`
	Description       string       `db:"description" json:"description"`
	Metadata          JSON         `db:"metadata" json:"metadata"`
	CheckoutURL       string       `db:"checkout_url" json:"checkout_url"`
	ExpiresAt         string       `db:"expires_at" json:"expires_at"`
	CreatedAt         string       `db:"created_at" json:"created_at"`
	CompletedAt       *string      `db:"completed_at" json:"completed_at"`
}

type paymentIntentRow struct {
	ID                string  `db:"id"`
	MerchantAccountID string  `db:"merchant_account_id"`
	ServiceID         string  `db:"service_id"`
	ServiceCode       string  `db:"service_code"`
	ServiceName       string  `db:"service_name"`
	PayerAccountID    *string `db:"payer_account_id"`
	ExternalOrderID   string  `db:"external_order_id"`
	Amount            int64   `db:"amount_microcredits"`
	Fee               int64   `db:"platform_fee_microcredits"`
	Net               int64   `db:"net_microcredits"`
	FeeBPS            int     `db:"fee_bps"`
	Status            string  `db:"status"`
	Description       string  `db:"description"`
	Metadata          JSON    `db:"metadata"`
	CheckoutURL       string  `db:"checkout_url"`
	ExpiresAt         string  `db:"expires_at"`
	CreatedAt         string  `db:"created_at"`
	CompletedAt       *string `db:"completed_at"`
}

func (r paymentIntentRow) public() PaymentIntent {
	return PaymentIntent{Object: "payment_intent", ID: r.ID, MerchantAccountID: r.MerchantAccountID, ServiceID: r.ServiceID, ServiceCode: r.ServiceCode, ServiceName: r.ServiceName, PayerAccountID: r.PayerAccountID, ExternalOrderID: r.ExternalOrderID,
		Amount: CreditAmount{Asset: "GIZ_CREDIT", Microcredits: r.Amount}, PlatformFee: CreditAmount{Asset: "GIZ_CREDIT", Microcredits: r.Fee}, NetAmount: CreditAmount{Asset: "GIZ_CREDIT", Microcredits: r.Net}, FeeBPS: r.FeeBPS, Status: r.Status, Description: r.Description, Metadata: r.Metadata, CheckoutURL: r.CheckoutURL, ExpiresAt: r.ExpiresAt, CreatedAt: r.CreatedAt, CompletedAt: r.CompletedAt}
}

const paymentIntentColumns = `id,merchant_account_id,service_id,(SELECT service_code FROM merchant_services WHERE id=payment_intents.service_id) AS service_code,(SELECT name FROM merchant_services WHERE id=payment_intents.service_id) AS service_name,payer_account_id,external_order_id,amount_microcredits,platform_fee_microcredits,net_microcredits,fee_bps,status,description,metadata,checkout_url,expires_at,created_at,completed_at`

func (s *Store) CreatePaymentIntent(ctx context.Context, merchantAccountID, idempotencyKey string, payloadHash []byte, intent PaymentIntent) (PaymentIntent, bool, error) {
	return s.CreatePaymentIntentForKey(ctx, merchantAccountID, "", idempotencyKey, payloadHash, intent)
}

// CreatePaymentIntentForKey retains the authenticated credential identity so
// the serializable command can close the authenticate-then-revoke race.
func (s *Store) CreatePaymentIntentForKey(ctx context.Context, merchantAccountID, apiKeyID, idempotencyKey string, payloadHash []byte, intent PaymentIntent) (PaymentIntent, bool, error) {
	type result struct {
		intent   PaymentIntent
		replayed bool
	}
	value, err := retrySerializable(ctx, func() (result, error) {
		intent, replayed, commandErr := s.createPaymentIntent(ctx, merchantAccountID, apiKeyID, idempotencyKey, payloadHash, intent)
		return result{intent: intent, replayed: replayed}, commandErr
	})
	return value.intent, value.replayed, err
}

func (s *Store) createPaymentIntent(ctx context.Context, merchantAccountID, apiKeyID, idempotencyKey string, payloadHash []byte, intent PaymentIntent) (PaymentIntent, bool, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return PaymentIntent{}, false, err
	}
	defer tx.Rollback()
	var existing struct {
		ID   string `db:"id"`
		Hash []byte `db:"create_payload_hash"`
	}
	err = tx.GetContext(ctx, &existing, `SELECT id,create_payload_hash FROM payment_intents WHERE merchant_account_id=? AND create_idempotency_key=?`, merchantAccountID, idempotencyKey)
	if err == nil {
		if !bytes.Equal(existing.Hash, payloadHash) {
			return PaymentIntent{}, false, ErrIdempotencyConflict
		}
		row, e := getPaymentIntent(ctx, tx, existing.ID)
		return row.public(), true, e
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return PaymentIntent{}, false, err
	}
	var merchantActive int
	authQuery := `SELECT COUNT(*) FROM merchant_accounts m JOIN accounts a ON a.id=m.account_id JOIN users u ON u.id=a.owner_user_id WHERE m.account_id=? AND m.merchant_status='approved' AND a.status='active' AND u.status='active'`
	args := []any{merchantAccountID}
	if apiKeyID != "" {
		authQuery += ` AND EXISTS (SELECT 1 FROM api_keys k WHERE k.id=? AND k.account_id=a.id AND k.kind='payment' AND k.status='active' AND (k.expires_at IS NULL OR k.expires_at>?))`
		args = append(args, apiKeyID, intent.CreatedAt)
	}
	if err := tx.GetContext(ctx, &merchantActive, authQuery, args...); err != nil {
		return PaymentIntent{}, false, err
	}
	if merchantActive == 0 {
		return PaymentIntent{}, false, ErrNotFound
	}
	// external_order_id is a merchant-visible uniqueness contract. Detect it
	// before INSERT so a duplicate is a stable business conflict on both
	// PostgreSQL, never a leaked driver/constraint error.
	var orderOwner string
	err = tx.GetContext(ctx, &orderOwner, `SELECT id FROM payment_intents WHERE merchant_account_id=? AND external_order_id=?`, merchantAccountID, intent.ExternalOrderID)
	if err == nil {
		return PaymentIntent{}, false, ErrIdempotencyConflict
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return PaymentIntent{}, false, err
	}
	var service struct {
		Code           string `db:"service_code"`
		Name           string `db:"name"`
		Status         string `db:"status"`
		MaxTransaction int64  `db:"max_transaction_microcredits"`
		DailyLimit     int64  `db:"daily_limit_microcredits"`
	}
	if err := tx.GetContext(ctx, &service, `SELECT service_code,name,status,max_transaction_microcredits,daily_limit_microcredits FROM merchant_services WHERE id=? AND merchant_account_id=?`, intent.ServiceID, merchantAccountID); err != nil {
		return PaymentIntent{}, false, ErrRiskDenied
	}
	if service.Status != "approved" || intent.Amount.Microcredits > service.MaxTransaction {
		return PaymentIntent{}, false, ErrRiskDenied
	}
	var dailyCommitted int64
	if err := tx.GetContext(ctx, &dailyCommitted, `SELECT COALESCE(SUM(amount_microcredits),0) FROM payment_intents WHERE service_id=? AND (status IN ('authorized','succeeded') OR (status='created' AND expires_at>?)) AND substr(created_at,1,10)=substr(?,1,10)`, intent.ServiceID, intent.CreatedAt, intent.CreatedAt); err != nil {
		return PaymentIntent{}, false, err
	}
	if dailyCommitted > service.DailyLimit-intent.Amount.Microcredits {
		return PaymentIntent{}, false, ErrRiskDenied
	}
	intent.ServiceCode = service.Code
	intent.ServiceName = service.Name
	_, err = tx.ExecContext(ctx, `INSERT INTO payment_intents(id,merchant_account_id,service_id,external_order_id,amount_microcredits,platform_fee_microcredits,net_microcredits,fee_bps,status,description,metadata,checkout_url,expires_at,created_at,create_idempotency_key,create_payload_hash) VALUES (?,?,?,?,?,?,?,?,'created',?,?,?,?,?,?,?)`, intent.ID, merchantAccountID, intent.ServiceID, intent.ExternalOrderID, intent.Amount.Microcredits, intent.PlatformFee.Microcredits, intent.NetAmount.Microcredits, intent.FeeBPS, intent.Description, string(intent.Metadata), intent.CheckoutURL, intent.ExpiresAt, intent.CreatedAt, idempotencyKey, payloadHash)
	if err != nil {
		return PaymentIntent{}, false, fmt.Errorf("insert payment intent: %w", err)
	}
	if err := insertAccountOwnerAudit(ctx, tx, merchantAccountID, "payment_intent.created", "payment_intent", intent.ID, "merchant created Credit checkout", "{}", intent.CreatedAt); err != nil {
		return PaymentIntent{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return PaymentIntent{}, false, err
	}
	return intent, false, nil
}

func (s *Store) GetMerchantPaymentIntent(ctx context.Context, merchantAccountID, id string) (PaymentIntent, error) {
	row, err := getPaymentIntent(ctx, s.db, id)
	if err != nil || row.MerchantAccountID != merchantAccountID {
		return PaymentIntent{}, ErrNotFound
	}
	return row.public(), nil
}
func (s *Store) GetCheckoutPaymentIntent(ctx context.Context, id string) (PaymentIntent, error) {
	row, err := getPaymentIntent(ctx, s.db, id)
	if err != nil {
		return PaymentIntent{}, err
	}
	return row.public(), nil
}

// ExpirePaymentIntents durably advances abandoned checkouts to their terminal
// state. The conditional update makes this safe against a simultaneous payer
// confirmation; only the transaction that wins created -> expired writes the
// corresponding audit event.
func (s *Store) ExpirePaymentIntents(ctx context.Context, at string, limit int) (int, error) {
	if limit <= 0 {
		return 0, nil
	}
	var ids []string
	if err := s.db.SelectContext(ctx, &ids, `SELECT id FROM payment_intents WHERE status='created' AND expires_at<=? ORDER BY expires_at,id LIMIT ?`, at, limit); err != nil {
		return 0, err
	}
	expired := 0
	for _, id := range ids {
		err := retrySerializableError(ctx, func() error {
			tx, err := s.db.BeginTxx(ctx, nil)
			if err != nil {
				return err
			}
			defer tx.Rollback()
			result, err := tx.ExecContext(ctx, `UPDATE payment_intents SET status='expired',completed_at=? WHERE id=? AND status='created' AND expires_at<=?`, at, id, at)
			if err != nil {
				return err
			}
			updated, err := result.RowsAffected()
			if err != nil || updated == 0 {
				return err
			}
			if err := recordAudit(ctx, tx, "system", "payment-intent-expiry", "payment_intent.expired", "payment_intent", id, "checkout reached its expiry deadline", "{}", at); err != nil {
				return err
			}
			if err := tx.Commit(); err != nil {
				return err
			}
			expired++
			return nil
		})
		if err != nil {
			return expired, err
		}
	}
	return expired, nil
}

type MerchantTransaction struct {
	ID              string       `db:"id" json:"id"`
	PaymentIntentID string       `db:"payment_intent_id" json:"payment_intent_id"`
	ExternalOrderID string       `db:"external_order_id" json:"external_order_id"`
	GrossAmount     CreditAmount `json:"gross_amount"`
	PlatformFee     CreditAmount `json:"platform_fee"`
	NetAmount       CreditAmount `json:"net_amount"`
	Status          string       `db:"status" json:"status"`
	CreatedAt       string       `db:"created_at" json:"created_at"`
}

type WebhookDelivery struct {
	ID             string  `db:"id" json:"id"`
	EventID        string  `db:"event_id" json:"event_id"`
	EndpointID     string  `db:"endpoint_id" json:"endpoint_id"`
	EventType      string  `db:"event_type" json:"event_type"`
	Attempt        int     `db:"attempt" json:"attempt"`
	Status         string  `db:"status" json:"status"`
	ResponseStatus *int    `db:"response_status" json:"response_status"`
	Error          *string `db:"error" json:"error"`
	CreatedAt      string  `db:"created_at" json:"created_at"`
}

// ConfirmPaymentIntent atomically posts payer debit, merchant net and platform
// fee, then creates one durable webhook event/outbox delivery per endpoint.
func (s *Store) ConfirmPaymentIntent(ctx context.Context, userID, intentID, idempotencyKey, at string) (PaymentIntent, []string, bool, error) {
	type result struct {
		intent     PaymentIntent
		deliveries []string
		replayed   bool
	}
	value, err := retrySerializable(ctx, func() (result, error) {
		intent, deliveries, replayed, commandErr := s.confirmPaymentIntent(ctx, userID, intentID, idempotencyKey, at)
		return result{intent: intent, deliveries: deliveries, replayed: replayed}, commandErr
	})
	return value.intent, value.deliveries, value.replayed, err
}

func (s *Store) confirmPaymentIntent(ctx context.Context, userID, intentID, idempotencyKey, at string) (PaymentIntent, []string, bool, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return PaymentIntent{}, nil, false, err
	}
	defer tx.Rollback()
	row, err := getPaymentIntent(ctx, tx, intentID)
	if err != nil {
		return PaymentIntent{}, nil, false, err
	}
	if row.Status == "succeeded" {
		if idempotencyKey == "" {
			return PaymentIntent{}, nil, false, ErrIdempotencyConflict
		}
		var stored string
		if err := tx.GetContext(ctx, &stored, `SELECT confirm_idempotency_key FROM payment_intents WHERE id=?`, intentID); err != nil {
			return PaymentIntent{}, nil, false, err
		}
		if stored != idempotencyKey {
			return PaymentIntent{}, nil, false, ErrIdempotencyConflict
		}
		return row.public(), nil, true, nil
	}
	if row.Status == "created" && row.ExpiresAt <= at {
		result, err := tx.ExecContext(ctx, `UPDATE payment_intents SET status='expired',completed_at=? WHERE id=? AND status='created' AND expires_at<=?`, at, intentID, at)
		if err != nil {
			return PaymentIntent{}, nil, false, err
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return PaymentIntent{}, nil, false, err
		}
		if updated == 1 {
			if err := recordAudit(ctx, tx, "system", "payment-intent-expiry", "payment_intent.expired", "payment_intent", intentID, "checkout expired before confirmation", "{}", at); err != nil {
				return PaymentIntent{}, nil, false, err
			}
			if err := tx.Commit(); err != nil {
				return PaymentIntent{}, nil, false, err
			}
		}
		return PaymentIntent{}, nil, false, ErrIdempotencyConflict
	}
	if row.Status != "created" {
		return PaymentIntent{}, nil, false, ErrIdempotencyConflict
	}
	var payable int
	if err := tx.GetContext(ctx, &payable, `SELECT COUNT(*) FROM merchant_accounts m JOIN accounts a ON a.id=m.account_id JOIN merchant_services s ON s.id=? AND s.merchant_account_id=m.account_id WHERE m.account_id=? AND m.merchant_status='approved' AND a.status='active' AND s.status='approved'`, row.ServiceID, row.MerchantAccountID); err != nil {
		return PaymentIntent{}, nil, false, err
	}
	if payable == 0 {
		return PaymentIntent{}, nil, false, ErrRiskDenied
	}
	var payerAccount string
	if err := tx.GetContext(ctx, &payerAccount, `SELECT id FROM accounts WHERE owner_user_id=? AND kind='personal' AND status='active'`, userID); err != nil {
		return PaymentIntent{}, nil, false, ErrNotFound
	}
	available, err := availableCredit(ctx, tx, payerAccount)
	if err != nil {
		return PaymentIntent{}, nil, false, err
	}
	if available < row.Amount {
		return PaymentIntent{}, nil, false, ErrInsufficientBalance
	}
	var payerLedger, merchantLedger, feeLedger string
	if err := tx.GetContext(ctx, &payerLedger, `SELECT id FROM ledger_accounts WHERE owner_account_id=? AND status='active'`, payerAccount); err != nil {
		return PaymentIntent{}, nil, false, err
	}
	if err := tx.GetContext(ctx, &merchantLedger, `SELECT id FROM ledger_accounts WHERE owner_account_id=? AND status='active'`, row.MerchantAccountID); err != nil {
		return PaymentIntent{}, nil, false, err
	}
	if err := tx.GetContext(ctx, &feeLedger, `SELECT id FROM ledger_accounts WHERE code='SYSTEM:PLATFORM_FEE_REVENUE' AND status='active'`); err != nil {
		return PaymentIntent{}, nil, false, err
	}
	ledgerID := uuid.NewString()
	if _, err := tx.ExecContext(ctx, `INSERT INTO ledger_transactions(id,transaction_type,status,idempotency_key,initiated_by_account_id,reference_type,reference_id,description,created_at,posted_at) VALUES (?,'merchant_payment','posted',?,?,'payment_intent',?,'Merchant payment',?,?)`, ledgerID, "payment:"+intentID, payerAccount, intentID, at, at); err != nil {
		return PaymentIntent{}, nil, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO ledger_entries(id,transaction_id,ledger_account_id,sequence,direction,amount_microcredits,created_at) VALUES (?,?,?,1,'debit',?,?),(?,?,?,2,'credit',?,?),(?,?,?,3,'credit',?,?)`, uuid.NewString(), ledgerID, payerLedger, row.Amount, at, uuid.NewString(), ledgerID, merchantLedger, row.Net, at, uuid.NewString(), ledgerID, feeLedger, row.Fee, at); err != nil {
		return PaymentIntent{}, nil, false, err
	}
	if err := consumePurchasedLots(ctx, tx, payerAccount, row.Amount); err != nil {
		return PaymentIntent{}, nil, false, err
	}
	transactionID := uuid.NewString()
	if _, err := tx.ExecContext(ctx, `INSERT INTO merchant_transactions(id,payment_intent_id,merchant_account_id,gross_microcredits,platform_fee_microcredits,net_microcredits,status,created_at) VALUES (?,?,?,?,?,?,'posted',?)`, transactionID, intentID, row.MerchantAccountID, row.Amount, row.Fee, row.Net, at); err != nil {
		return PaymentIntent{}, nil, false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE payment_intents SET payer_account_id=?,status='succeeded',completed_at=?,confirm_idempotency_key=? WHERE id=? AND status='created'`, payerAccount, at, idempotencyKey, intentID); err != nil {
		return PaymentIntent{}, nil, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_events(id,actor_type,actor_id,action,resource_type,resource_id,reason,request_id,metadata,created_at) VALUES (?,'user',?,'payment_intent.confirmed','payment_intent',?,'payer confirmed disclosed amount',?,'{}',?)`, uuid.NewString(), userID, intentID, auditRequestID(ctx), at); err != nil {
		return PaymentIntent{}, nil, false, err
	}
	row.PayerAccountID = &payerAccount
	row.Status = "succeeded"
	row.CompletedAt = &at
	eventID := uuid.NewString()
	payload, _ := jsonMarshal(map[string]any{"id": eventID, "type": "payment_intent.succeeded", "data": map[string]any{"object": row.public()}})
	if _, err := tx.ExecContext(ctx, `INSERT INTO webhook_events(id,merchant_account_id,event_type,resource_id,payload,created_at) VALUES (?,?, 'payment_intent.succeeded',?,?,?)`, eventID, row.MerchantAccountID, intentID, string(payload), at); err != nil {
		return PaymentIntent{}, nil, false, err
	}
	var endpoints []struct {
		ID            string `db:"id"`
		Events        JSON   `db:"events"`
		SigningSecret string `db:"signing_secret"`
	}
	if err := tx.SelectContext(ctx, &endpoints, `SELECT id,events,signing_secret FROM webhook_endpoints WHERE merchant_account_id=? AND status='active' AND deleted_at IS NULL`, row.MerchantAccountID); err != nil {
		return PaymentIntent{}, nil, false, err
	}
	deliveryIDs := make([]string, 0, len(endpoints))
	for _, endpoint := range endpoints {
		var eventTypes []string
		if err := json.Unmarshal(endpoint.Events, &eventTypes); err != nil {
			return PaymentIntent{}, nil, false, fmt.Errorf("decode webhook endpoint events: %w", err)
		}
		if !stringSliceContains(eventTypes, "payment_intent.succeeded") {
			continue
		}
		id := uuid.NewString()
		if _, err := tx.ExecContext(ctx, `INSERT INTO webhook_deliveries(id,event_id,endpoint_id,signing_secret_snapshot,attempt,status,created_at) VALUES (?,?,?,?,1,'pending',?)`, id, eventID, endpoint.ID, endpoint.SigningSecret, at); err != nil {
			return PaymentIntent{}, nil, false, err
		}
		deliveryIDs = append(deliveryIDs, id)
	}
	if err := tx.Commit(); err != nil {
		return PaymentIntent{}, nil, false, err
	}
	return row.public(), deliveryIDs, false, nil
}

func (s *Store) CancelPaymentIntent(ctx context.Context, merchantAccountID, intentID, at string) (PaymentIntent, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return PaymentIntent{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE payment_intents SET status='cancelled',completed_at=? WHERE id=? AND merchant_account_id=? AND status='created'`, at, intentID, merchantAccountID)
	if err != nil {
		return PaymentIntent{}, err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return PaymentIntent{}, ErrIdempotencyConflict
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_events(id,actor_type,actor_id,action,resource_type,resource_id,reason,request_id,metadata,created_at) VALUES (?,'user',(SELECT owner_user_id FROM accounts WHERE id=?),'payment_intent.cancelled','payment_intent',?,'merchant cancelled before settlement',?,'{}',?)`, uuid.NewString(), merchantAccountID, intentID, auditRequestID(ctx), at); err != nil {
		return PaymentIntent{}, err
	}
	row, err := getPaymentIntent(ctx, tx, intentID)
	if err != nil {
		return PaymentIntent{}, err
	}
	if err := tx.Commit(); err != nil {
		return PaymentIntent{}, err
	}
	return row.public(), nil
}

// MerchantPaymentReversal is the immutable result of reversing one completed
// merchant payment. It deliberately is not a top-up refund and does not mutate
// the original ledger transaction or payment intent back to an earlier state.
type MerchantPaymentReversal struct {
	ID                string       `db:"id" json:"id"`
	Object            string       `json:"object"`
	PaymentIntentID   string       `db:"payment_intent_id" json:"payment_intent_id"`
	MerchantAccountID string       `db:"merchant_account_id" json:"merchant_account_id"`
	PayerAccountID    string       `db:"payer_account_id" json:"payer_account_id"`
	GrossAmount       CreditAmount `json:"gross_amount"`
	PlatformFee       CreditAmount `json:"platform_fee"`
	NetAmount         CreditAmount `json:"net_amount"`
	Status            string       `db:"status" json:"status"`
	Reason            string       `db:"reason" json:"reason"`
	LedgerTransaction string       `db:"ledger_transaction_id" json:"ledger_transaction_id"`
	CreatedAt         string       `db:"created_at" json:"created_at"`
}

type merchantPaymentReversalRow struct {
	ID                string `db:"id"`
	PaymentIntentID   string `db:"payment_intent_id"`
	MerchantAccountID string `db:"merchant_account_id"`
	PayerAccountID    string `db:"payer_account_id"`
	Gross             int64  `db:"gross_microcredits"`
	Fee               int64  `db:"platform_fee_microcredits"`
	Net               int64  `db:"net_microcredits"`
	Status            string `db:"status"`
	Reason            string `db:"reason"`
	LedgerTransaction string `db:"ledger_transaction_id"`
	CreatedAt         string `db:"created_at"`
}

func (r merchantPaymentReversalRow) public() MerchantPaymentReversal {
	return MerchantPaymentReversal{ID: r.ID, Object: "merchant_payment_reversal", PaymentIntentID: r.PaymentIntentID,
		MerchantAccountID: r.MerchantAccountID, PayerAccountID: r.PayerAccountID,
		GrossAmount: CreditAmount{Asset: "GIZ_CREDIT", Microcredits: r.Gross},
		PlatformFee: CreditAmount{Asset: "GIZ_CREDIT", Microcredits: r.Fee},
		NetAmount:   CreditAmount{Asset: "GIZ_CREDIT", Microcredits: r.Net}, Status: r.Status,
		Reason: r.Reason, LedgerTransaction: r.LedgerTransaction, CreatedAt: r.CreatedAt}
}

const merchantPaymentReversalColumns = `id,payment_intent_id,merchant_account_id,payer_account_id,gross_microcredits,platform_fee_microcredits,net_microcredits,status,reason,ledger_transaction_id,created_at`

// ReversePaymentIntent posts the exact inverse of a settled three-party
// payment in one serializable transaction. Active AI reservations and other
// pending spends are included in availableCredit, so merchant Credit cannot be
// spent and reversed concurrently.
func (s *Store) ReversePaymentIntent(ctx context.Context, merchantAccountID, intentID, idempotencyKey string, payloadHash []byte, reason, at string) (MerchantPaymentReversal, []string, bool, error) {
	type result struct {
		reversal   MerchantPaymentReversal
		deliveries []string
		replayed   bool
	}
	value, err := retrySerializable(ctx, func() (result, error) {
		tx, beginErr := s.db.BeginTxx(ctx, nil)
		if beginErr != nil {
			return result{}, beginErr
		}
		defer tx.Rollback()

		var existing struct {
			merchantPaymentReversalRow
			Hash []byte `db:"payload_hash"`
		}
		lookupErr := tx.GetContext(ctx, &existing, `SELECT `+merchantPaymentReversalColumns+`,payload_hash FROM merchant_payment_reversals WHERE merchant_account_id=? AND idempotency_key=?`, merchantAccountID, idempotencyKey)
		if lookupErr == nil {
			if !bytes.Equal(existing.Hash, payloadHash) || existing.PaymentIntentID != intentID {
				return result{}, ErrIdempotencyConflict
			}
			return result{reversal: existing.merchantPaymentReversalRow.public(), replayed: true}, nil
		}
		if !errors.Is(lookupErr, sql.ErrNoRows) {
			return result{}, lookupErr
		}

		intent, getErr := getPaymentIntent(ctx, tx, intentID)
		if getErr != nil || intent.MerchantAccountID != merchantAccountID {
			return result{}, ErrNotFound
		}
		if intent.Status != "succeeded" || intent.PayerAccountID == nil {
			return result{}, ErrIdempotencyConflict
		}
		var already string
		if getErr := tx.GetContext(ctx, &already, `SELECT id FROM merchant_payment_reversals WHERE payment_intent_id=?`, intentID); getErr == nil {
			return result{}, ErrIdempotencyConflict
		} else if !errors.Is(getErr, sql.ErrNoRows) {
			return result{}, getErr
		}
		available, balanceErr := availableCredit(ctx, tx, merchantAccountID)
		if balanceErr != nil {
			return result{}, balanceErr
		}
		if available < intent.Net {
			return result{}, ErrInsufficientBalance
		}

		var payerLedger, merchantLedger, feeLedger string
		if getErr := tx.GetContext(ctx, &payerLedger, `SELECT id FROM ledger_accounts WHERE owner_account_id=? AND status='active'`, *intent.PayerAccountID); getErr != nil {
			return result{}, getErr
		}
		if getErr := tx.GetContext(ctx, &merchantLedger, `SELECT id FROM ledger_accounts WHERE owner_account_id=? AND status='active'`, merchantAccountID); getErr != nil {
			return result{}, getErr
		}
		if getErr := tx.GetContext(ctx, &feeLedger, `SELECT id FROM ledger_accounts WHERE code='SYSTEM:PLATFORM_FEE_REVENUE' AND status='active'`); getErr != nil {
			return result{}, getErr
		}
		ledgerID := uuid.NewString()
		if _, execErr := tx.ExecContext(ctx, `INSERT INTO ledger_transactions(id,transaction_type,status,idempotency_key,initiated_by_account_id,reference_type,reference_id,description,created_at,posted_at) VALUES (?,'merchant_payment_reversal','posted',?,?,'payment_intent',?,'Merchant payment compensating reversal',?,?)`, ledgerID, "payment-reversal:"+intentID, merchantAccountID, intentID, at, at); execErr != nil {
			return result{}, execErr
		}
		if _, execErr := tx.ExecContext(ctx, `INSERT INTO ledger_entries(id,transaction_id,ledger_account_id,sequence,direction,amount_microcredits,created_at) VALUES (?,?,?,1,'debit',?,?),(?,?,?,2,'debit',?,?),(?,?,?,3,'credit',?,?)`, uuid.NewString(), ledgerID, merchantLedger, intent.Net, at, uuid.NewString(), ledgerID, feeLedger, intent.Fee, at, uuid.NewString(), ledgerID, payerLedger, intent.Amount, at); execErr != nil {
			return result{}, execErr
		}
		reversalID := uuid.NewSHA1(uuid.NameSpaceOID, []byte(merchantAccountID+":"+idempotencyKey)).String()
		if _, execErr := tx.ExecContext(ctx, `INSERT INTO merchant_payment_reversals(id,payment_intent_id,merchant_account_id,payer_account_id,gross_microcredits,platform_fee_microcredits,net_microcredits,status,reason,idempotency_key,payload_hash,ledger_transaction_id,created_at) VALUES (?,?,?,?,?,?,?,'succeeded',?,?,?,?,?)`, reversalID, intentID, merchantAccountID, *intent.PayerAccountID, intent.Amount, intent.Fee, intent.Net, reason, idempotencyKey, payloadHash, ledgerID, at); execErr != nil {
			return result{}, execErr
		}
		if _, execErr := tx.ExecContext(ctx, `UPDATE merchant_transactions SET status='reversed' WHERE payment_intent_id=? AND status='posted'`, intentID); execErr != nil {
			return result{}, execErr
		}
		if _, execErr := tx.ExecContext(ctx, `INSERT INTO audit_events(id,actor_type,actor_id,action,resource_type,resource_id,reason,request_id,metadata,created_at) VALUES (?,'user',(SELECT owner_user_id FROM accounts WHERE id=?),'payment_intent.reversed','merchant_payment_reversal',?,?,?,'{}',?)`, uuid.NewString(), merchantAccountID, reversalID, reason, auditRequestID(ctx), at); execErr != nil {
			return result{}, execErr
		}

		reversal := merchantPaymentReversalRow{ID: reversalID, PaymentIntentID: intentID, MerchantAccountID: merchantAccountID, PayerAccountID: *intent.PayerAccountID, Gross: intent.Amount, Fee: intent.Fee, Net: intent.Net, Status: "succeeded", Reason: reason, LedgerTransaction: ledgerID, CreatedAt: at}.public()
		eventID := uuid.NewString()
		payload, _ := jsonMarshal(map[string]any{"id": eventID, "type": "transaction.reversed", "data": map[string]any{"object": reversal}})
		if _, execErr := tx.ExecContext(ctx, `INSERT INTO webhook_events(id,merchant_account_id,event_type,resource_id,payload,created_at) VALUES (?,?,'transaction.reversed',?,?,?)`, eventID, merchantAccountID, reversalID, string(payload), at); execErr != nil {
			return result{}, execErr
		}
		var endpoints []struct {
			ID            string `db:"id"`
			Events        JSON   `db:"events"`
			SigningSecret string `db:"signing_secret"`
		}
		if selectErr := tx.SelectContext(ctx, &endpoints, `SELECT id,events,signing_secret FROM webhook_endpoints WHERE merchant_account_id=? AND status='active' AND deleted_at IS NULL`, merchantAccountID); selectErr != nil {
			return result{}, selectErr
		}
		deliveryIDs := make([]string, 0, len(endpoints))
		for _, endpoint := range endpoints {
			var events []string
			if decodeErr := json.Unmarshal(endpoint.Events, &events); decodeErr != nil {
				return result{}, decodeErr
			}
			if !stringSliceContains(events, "transaction.reversed") {
				continue
			}
			deliveryID := uuid.NewString()
			if _, execErr := tx.ExecContext(ctx, `INSERT INTO webhook_deliveries(id,event_id,endpoint_id,signing_secret_snapshot,attempt,status,created_at) VALUES (?,?,?,?,1,'pending',?)`, deliveryID, eventID, endpoint.ID, endpoint.SigningSecret, at); execErr != nil {
				return result{}, execErr
			}
			deliveryIDs = append(deliveryIDs, deliveryID)
		}
		if commitErr := tx.Commit(); commitErr != nil {
			return result{}, commitErr
		}
		return result{reversal: reversal, deliveries: deliveryIDs}, nil
	})
	return value.reversal, value.deliveries, value.replayed, err
}

func (s *Store) ListMerchantTransactions(ctx context.Context, merchantAccountID string) ([]MerchantTransaction, error) {
	var rows []struct {
		ID              string `db:"id"`
		PaymentIntentID string `db:"payment_intent_id"`
		External        string `db:"external_order_id"`
		Gross           int64  `db:"gross_microcredits"`
		Fee             int64  `db:"platform_fee_microcredits"`
		Net             int64  `db:"net_microcredits"`
		Status          string `db:"status"`
		Created         string `db:"created_at"`
	}
	if err := s.db.SelectContext(ctx, &rows, `SELECT mt.id,mt.payment_intent_id,pi.external_order_id,mt.gross_microcredits,mt.platform_fee_microcredits,mt.net_microcredits,mt.status,mt.created_at FROM merchant_transactions mt JOIN payment_intents pi ON pi.id=mt.payment_intent_id WHERE mt.merchant_account_id=? ORDER BY mt.created_at DESC`, merchantAccountID); err != nil {
		return nil, err
	}
	result := make([]MerchantTransaction, 0, len(rows))
	for _, r := range rows {
		result = append(result, MerchantTransaction{ID: r.ID, PaymentIntentID: r.PaymentIntentID, ExternalOrderID: r.External, GrossAmount: CreditAmount{Asset: "GIZ_CREDIT", Microcredits: r.Gross}, PlatformFee: CreditAmount{Asset: "GIZ_CREDIT", Microcredits: r.Fee}, NetAmount: CreditAmount{Asset: "GIZ_CREDIT", Microcredits: r.Net}, Status: r.Status, CreatedAt: r.Created})
	}
	return result, nil
}

type WebhookEndpoint struct {
	ID            string  `db:"id" json:"id"`
	URL           string  `db:"url" json:"url"`
	Events        JSON    `db:"events" json:"events"`
	Status        string  `db:"status" json:"status"`
	CreatedAt     string  `db:"created_at" json:"created_at"`
	UpdatedAt     string  `db:"updated_at" json:"updated_at"`
	DeletedAt     *string `db:"deleted_at" json:"deleted_at,omitempty"`
	SigningSecret string  `db:"signing_secret" json:"-"`
}

func (s *Store) CreateWebhookEndpoint(ctx context.Context, merchantAccountID, idempotencyKey string, payloadHash []byte, endpoint WebhookEndpoint) (WebhookEndpoint, bool, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return WebhookEndpoint{}, false, err
	}
	defer tx.Rollback()
	var existing struct {
		ID   string `db:"id"`
		Hash []byte `db:"payload_hash"`
	}
	err = tx.GetContext(ctx, &existing, `SELECT id,payload_hash FROM webhook_endpoints WHERE merchant_account_id=? AND idempotency_key=?`, merchantAccountID, idempotencyKey)
	if err == nil {
		if !bytes.Equal(existing.Hash, payloadHash) {
			return WebhookEndpoint{}, false, ErrIdempotencyConflict
		}
		if err := tx.GetContext(ctx, &endpoint, `SELECT id,url,events,status,created_at,updated_at,deleted_at FROM webhook_endpoints WHERE id=?`, existing.ID); err != nil {
			return WebhookEndpoint{}, false, err
		}
		return endpoint, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return WebhookEndpoint{}, false, err
	}
	protectedSecret := endpoint.SigningSecret
	if s.secrets != nil {
		protectedSecret, err = s.secrets.encrypt(endpoint.SigningSecret)
		if err != nil {
			return WebhookEndpoint{}, false, err
		}
	}
	endpoint.UpdatedAt = endpoint.CreatedAt
	if _, err = tx.ExecContext(ctx, `INSERT INTO webhook_endpoints(id,merchant_account_id,url,events,signing_secret,status,idempotency_key,payload_hash,created_at,updated_at) VALUES (?,?,?,?,?,'active',?,?,?,?)`, endpoint.ID, merchantAccountID, endpoint.URL, string(endpoint.Events), protectedSecret, idempotencyKey, payloadHash, endpoint.CreatedAt, endpoint.UpdatedAt); err != nil {
		return WebhookEndpoint{}, false, err
	}
	if err := insertMerchantAudit(ctx, tx, merchantAccountID, "webhook_endpoint.created", endpoint.ID, "created webhook endpoint", endpoint.CreatedAt); err != nil {
		return WebhookEndpoint{}, false, err
	}
	return endpoint, false, tx.Commit()
}
func (s *Store) ListWebhookEndpoints(ctx context.Context, merchantAccountID string) ([]WebhookEndpoint, error) {
	var result []WebhookEndpoint
	err := s.db.SelectContext(ctx, &result, `SELECT id,url,events,status,created_at,updated_at,deleted_at FROM webhook_endpoints WHERE merchant_account_id=? AND deleted_at IS NULL ORDER BY created_at,id`, merchantAccountID)
	return result, err
}

func (s *Store) SetWebhookEndpointStatus(ctx context.Context, merchantAccountID, endpointID, status, idempotencyKey string, payloadHash []byte, at string) (WebhookEndpoint, bool, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return WebhookEndpoint{}, false, err
	}
	defer tx.Rollback()
	if response, replayed, err := webhookCommandReplay(ctx, tx, merchantAccountID, "status", idempotencyKey, payloadHash); err != nil || replayed {
		if err != nil {
			return WebhookEndpoint{}, false, err
		}
		var endpoint WebhookEndpoint
		if err := json.Unmarshal(response, &endpoint); err != nil {
			return WebhookEndpoint{}, false, err
		}
		return endpoint, true, nil
	}
	result, err := tx.ExecContext(ctx, `UPDATE webhook_endpoints SET status=?,updated_at=? WHERE id=? AND merchant_account_id=? AND deleted_at IS NULL`, status, at, endpointID, merchantAccountID)
	if err != nil {
		return WebhookEndpoint{}, false, err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return WebhookEndpoint{}, false, ErrNotFound
	}
	if err := insertMerchantAudit(ctx, tx, merchantAccountID, "webhook_endpoint.status_changed", endpointID, "changed webhook endpoint status", at); err != nil {
		return WebhookEndpoint{}, false, err
	}
	var endpoint WebhookEndpoint
	if err := tx.GetContext(ctx, &endpoint, `SELECT id,url,events,status,created_at,updated_at,deleted_at FROM webhook_endpoints WHERE id=?`, endpointID); err != nil {
		return WebhookEndpoint{}, false, err
	}
	response, err := json.Marshal(endpoint)
	if err != nil {
		return WebhookEndpoint{}, false, err
	}
	if err := insertWebhookCommand(ctx, tx, merchantAccountID, endpointID, "status", idempotencyKey, payloadHash, response, nil, at); err != nil {
		return WebhookEndpoint{}, false, err
	}
	return endpoint, false, tx.Commit()
}

func (s *Store) RotateWebhookEndpointSecret(ctx context.Context, merchantAccountID, endpointID, idempotencyKey string, payloadHash []byte, secret, at string) (string, bool, error) {
	protected := secret
	var err error
	if s.secrets != nil {
		protected, err = s.secrets.encrypt(secret)
		if err != nil {
			return "", false, err
		}
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback()
	if _, replayed, err := webhookCommandReplay(ctx, tx, merchantAccountID, "rotate_secret", idempotencyKey, payloadHash); err != nil || replayed {
		if err != nil {
			return "", false, err
		}
		var stored string
		if err := tx.GetContext(ctx, &stored, `SELECT secret_result FROM webhook_endpoint_commands WHERE merchant_account_id=? AND operation='rotate_secret' AND idempotency_key=?`, merchantAccountID, idempotencyKey); err != nil {
			return "", false, err
		}
		if s.secrets != nil && strings.HasPrefix(stored, encryptedSecretPrefix) {
			stored, err = s.secrets.decrypt(stored)
		}
		return stored, true, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE webhook_endpoints SET signing_secret=?,updated_at=? WHERE id=? AND merchant_account_id=? AND deleted_at IS NULL`, protected, at, endpointID, merchantAccountID)
	if err != nil {
		return "", false, err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return "", false, ErrNotFound
	}
	if err := insertMerchantAudit(ctx, tx, merchantAccountID, "webhook_endpoint.secret_rotated", endpointID, "rotated webhook signing secret", at); err != nil {
		return "", false, err
	}
	if err := insertWebhookCommand(ctx, tx, merchantAccountID, endpointID, "rotate_secret", idempotencyKey, payloadHash, nil, &protected, at); err != nil {
		return "", false, err
	}
	return secret, false, tx.Commit()
}

func (s *Store) DeleteWebhookEndpoint(ctx context.Context, merchantAccountID, endpointID, idempotencyKey string, payloadHash []byte, at string) (bool, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, replayed, err := webhookCommandReplay(ctx, tx, merchantAccountID, "delete", idempotencyKey, payloadHash); err != nil || replayed {
		return replayed, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE webhook_endpoints SET status='disabled',deleted_at=?,updated_at=? WHERE id=? AND merchant_account_id=? AND deleted_at IS NULL`, at, at, endpointID, merchantAccountID)
	if err != nil {
		return false, err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return false, ErrNotFound
	}
	if err := insertMerchantAudit(ctx, tx, merchantAccountID, "webhook_endpoint.deleted", endpointID, "deleted webhook endpoint", at); err != nil {
		return false, err
	}
	if err := insertWebhookCommand(ctx, tx, merchantAccountID, endpointID, "delete", idempotencyKey, payloadHash, nil, nil, at); err != nil {
		return false, err
	}
	return false, tx.Commit()
}

func webhookCommandReplay(ctx context.Context, tx *boundTx, merchantAccountID, operation, idempotencyKey string, payloadHash []byte) ([]byte, bool, error) {
	var row struct {
		Hash     []byte  `db:"payload_hash"`
		Response *string `db:"response_json"`
	}
	err := tx.GetContext(ctx, &row, `SELECT payload_hash,response_json FROM webhook_endpoint_commands WHERE merchant_account_id=? AND operation=? AND idempotency_key=?`, merchantAccountID, operation, idempotencyKey)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !bytes.Equal(row.Hash, payloadHash) {
		return nil, false, ErrIdempotencyConflict
	}
	if row.Response == nil {
		return nil, true, nil
	}
	return []byte(*row.Response), true, nil
}

func insertWebhookCommand(ctx context.Context, tx *boundTx, merchantAccountID, endpointID, operation, idempotencyKey string, payloadHash, response []byte, secretResult *string, at string) error {
	var encoded *string
	if response != nil {
		value := string(response)
		encoded = &value
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO webhook_endpoint_commands(id,merchant_account_id,endpoint_id,operation,idempotency_key,payload_hash,response_json,secret_result,created_at) VALUES (?,?,?,?,?,?,?,?,?)`, uuid.NewString(), merchantAccountID, endpointID, operation, idempotencyKey, payloadHash, encoded, secretResult, at)
	return err
}

func insertMerchantAudit(ctx context.Context, tx *boundTx, merchantAccountID, action, resourceID, reason, at string) error {
	return insertAccountOwnerAudit(ctx, tx, merchantAccountID, action, "webhook_endpoint", resourceID, reason, "{}", at)
}

type DeliveryTarget struct {
	DeliveryID string `db:"delivery_id"`
	URL        string `db:"url"`
	Secret     string `db:"signing_secret"`
	Payload    []byte `db:"payload"`
}

func (s *Store) GetDeliveryTarget(ctx context.Context, id string) (DeliveryTarget, error) {
	var target DeliveryTarget
	err := s.db.GetContext(ctx, &target, `SELECT d.id AS delivery_id,e.url,COALESCE(d.signing_secret_snapshot,e.signing_secret) AS signing_secret,v.payload FROM webhook_deliveries d JOIN webhook_endpoints e ON e.id=d.endpoint_id JOIN webhook_events v ON v.id=d.event_id WHERE d.id=? AND d.status='delivering'`, id)
	if errors.Is(err, sql.ErrNoRows) {
		return target, ErrNotFound
	}
	if err == nil && s.secrets != nil && strings.HasPrefix(target.Secret, encryptedSecretPrefix) {
		target.Secret, err = s.secrets.decrypt(target.Secret)
	}
	return target, err
}

// RecoverableWebhookDeliveryIDs returns only durable work that is safe to
// claim: never-attempted pending rows and leases abandoned by a dead process.
func (s *Store) RecoverableWebhookDeliveryIDs(ctx context.Context, at string, limit int) ([]string, error) {
	if limit <= 0 {
		return nil, errors.New("webhook delivery batch limit must be positive")
	}
	var ids []string
	err := s.db.SelectContext(ctx, &ids, `SELECT id FROM webhook_deliveries WHERE (status='pending' AND (next_attempt_at IS NULL OR next_attempt_at<=?)) OR (status='delivering' AND lease_until<=?) ORDER BY created_at,id LIMIT ?`, at, at, limit)
	return ids, err
}

// ClaimWebhookDelivery is the concurrency boundary between synchronous request
// delivery and background recovery workers. The conditional update means only
// one process owns the network side effect for the lease duration.
func (s *Store) ClaimWebhookDelivery(ctx context.Context, id, claimedAt, leaseUntil string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE webhook_deliveries SET status='delivering',claimed_at=?,lease_until=? WHERE id=? AND ((status='pending' AND (next_attempt_at IS NULL OR next_attempt_at<=?)) OR (status='delivering' AND lease_until<=?))`, claimedAt, leaseUntil, id, claimedAt, claimedAt)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) FinishWebhookDelivery(ctx context.Context, id string, statusCode int, deliveryError *string, at string) error {
	return retrySerializableError(ctx, func() error {
		tx, err := s.db.BeginTxx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		var eventID, endpointID string
		var secret *string
		var attempt int
		if err := scanCommandRow(ctx, tx.QueryRowxContext(ctx, `SELECT event_id,endpoint_id,signing_secret_snapshot,attempt FROM webhook_deliveries WHERE id=? AND status='delivering'`, id), &eventID, &endpointID, &secret, &attempt); err != nil {
			return err
		}
		if deliveryError == nil && statusCode >= 200 && statusCode < 300 {
			if _, err := tx.ExecContext(ctx, `UPDATE webhook_deliveries SET status='succeeded',response_status=?,error=NULL,completed_at=?,lease_until=NULL WHERE id=?`, statusCode, at, id); err != nil {
				return err
			}
			return tx.Commit()
		}
		status := "failed"
		if attempt >= 5 {
			status = "exhausted"
		}
		if _, err := tx.ExecContext(ctx, `UPDATE webhook_deliveries SET status=?,response_status=?,error=?,completed_at=?,lease_until=NULL WHERE id=?`, status, statusCode, deliveryError, at, id); err != nil {
			return err
		}
		if attempt < 5 {
			completed, parseErr := timetext.Parse(at)
			if parseErr != nil {
				return parseErr
			}
			backoff := time.Duration(1<<uint(attempt-1)) * time.Second
			next := timetext.Format(completed.Add(backoff))
			if _, err := tx.ExecContext(ctx, `INSERT INTO webhook_deliveries(id,event_id,endpoint_id,signing_secret_snapshot,attempt,status,next_attempt_at,created_at) VALUES (?,?,?,?,?,'pending',?,?)`, uuid.NewString(), eventID, endpointID, secret, attempt+1, next, at); err != nil {
				return err
			}
		}
		return tx.Commit()
	})
}

func getPaymentIntent(ctx context.Context, q sqlxGetter, id string) (paymentIntentRow, error) {
	var row paymentIntentRow
	err := q.GetContext(ctx, &row, `SELECT `+paymentIntentColumns+` FROM payment_intents WHERE id=?`, id)
	if errors.Is(err, sql.ErrNoRows) {
		return row, ErrNotFound
	}
	return row, err
}
func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }

func stringSliceContains(values []string, target string) bool {
	return slices.Contains(values, target)
}
