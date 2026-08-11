package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

var ErrPaymentMismatch = errors.New("payment callback does not match top-up")

type TopupRate struct {
	FiatMinor          int64 `json:"fiat_minor"`
	CreditMicrocredits int64 `json:"credit_microcredits"`
}

type TopupRateSnapshot struct {
	Base        TopupRate `json:"base"`
	Effective   TopupRate `json:"effective"`
	DiscountBPS int       `json:"discount_bps"`
}

type Topup struct {
	ID                string            `json:"id"`
	AccountID         string            `json:"account_id"`
	PaymentProvider   string            `json:"payment_provider"`
	ProviderReference string            `json:"provider_reference,omitempty"`
	FiatCurrency      string            `json:"fiat_currency"`
	FiatAmountMinor   int64             `json:"fiat_amount_minor"`
	Rate              TopupRateSnapshot `json:"rate"`
	CreditAmount      CreditAmount      `json:"credit_amount"`
	RefundableAmount  CreditAmount      `json:"refundable_amount"`
	Status            string            `json:"status"`
	CheckoutURL       *string           `json:"checkout_url"`
	CreatedAt         string            `json:"created_at"`
	CompletedAt       *string           `json:"completed_at,omitempty"`
}

type topupRow struct {
	ID                 string  `db:"id"`
	AccountID          string  `db:"account_id"`
	PaymentProvider    string  `db:"payment_provider"`
	ProviderReference  string  `db:"provider_reference"`
	FiatCurrency       string  `db:"fiat_currency"`
	FiatAmountMinor    int64   `db:"fiat_amount_minor"`
	BaseFiatMinor      int64   `db:"base_fiat_minor"`
	BaseCredit         int64   `db:"base_credit_microcredits"`
	EffectiveFiatMinor int64   `db:"effective_fiat_minor"`
	EffectiveCredit    int64   `db:"effective_credit_microcredits"`
	DiscountBPS        int     `db:"discount_bps"`
	Credit             int64   `db:"credit_microcredits"`
	Refundable         int64   `db:"refundable_microcredits"`
	Status             string  `db:"status"`
	CheckoutURL        *string `db:"checkout_url"`
	CreatedAt          string  `db:"created_at"`
	CompletedAt        *string `db:"completed_at"`
}

func (r topupRow) public() Topup {
	return Topup{ID: r.ID, AccountID: r.AccountID, PaymentProvider: r.PaymentProvider,
		ProviderReference: r.ProviderReference, FiatCurrency: r.FiatCurrency, FiatAmountMinor: r.FiatAmountMinor,
		Rate: TopupRateSnapshot{Base: TopupRate{FiatMinor: r.BaseFiatMinor, CreditMicrocredits: r.BaseCredit},
			Effective: TopupRate{FiatMinor: r.EffectiveFiatMinor, CreditMicrocredits: r.EffectiveCredit}, DiscountBPS: r.DiscountBPS},
		CreditAmount:     CreditAmount{Asset: "GIZ_CREDIT", Microcredits: r.Credit},
		RefundableAmount: CreditAmount{Asset: "GIZ_CREDIT", Microcredits: r.Refundable}, Status: r.Status,
		CheckoutURL: r.CheckoutURL, CreatedAt: r.CreatedAt, CompletedAt: r.CompletedAt}
}

const topupColumns = `id, account_id, payment_provider, provider_reference, fiat_currency,
	fiat_amount_minor, base_fiat_minor, base_credit_microcredits, effective_fiat_minor,
	effective_credit_microcredits, discount_bps, credit_microcredits,
	refundable_microcredits, status, checkout_url, created_at, completed_at`

// AuthorizeTopupCreation rejects an invalid tenant before the service invokes
// the external checkout provider. CreateTopup repeats this ownership check in
// its serializable transaction to protect the final database mutation.
func (s *Store) AuthorizeTopupCreation(ctx context.Context, userID, accountID string) error {
	var owned int
	if err := s.db.GetContext(ctx, &owned, `SELECT COUNT(*) FROM accounts WHERE id=? AND owner_user_id=? AND status='active'`, accountID, userID); err != nil {
		return err
	}
	if owned == 0 {
		return ErrNotFound
	}
	return nil
}

// LookupTopupCommand performs the durable idempotency check before any
// checkout-provider HTTP call. A concurrent first attempt can still race this
// read, so the deterministic top-up ID is also the provider idempotency key and
// CreateTopup repeats the check inside its serializable transaction.
func (s *Store) LookupTopupCommand(ctx context.Context, userID, accountID, idempotencyKey string, payloadHash []byte) (Topup, bool, error) {
	var existing struct {
		topupRow
		Hash []byte `db:"payload_hash"`
	}
	err := s.db.GetContext(ctx, &existing, `SELECT `+topupColumns+`,payload_hash
		FROM topups WHERE account_id=? AND idempotency_key=?`, accountID, idempotencyKey)
	if errors.Is(err, sql.ErrNoRows) {
		if err := s.AuthorizeTopupCreation(ctx, userID, accountID); err != nil {
			return Topup{}, false, err
		}
		return Topup{}, false, nil
	}
	if err != nil {
		return Topup{}, false, fmt.Errorf("lookup top-up command: %w", err)
	}
	if err := s.AuthorizeTopupCreation(ctx, userID, accountID); err != nil {
		return Topup{}, false, err
	}
	if !bytes.Equal(existing.Hash, payloadHash) {
		return Topup{}, false, ErrIdempotencyConflict
	}
	return existing.topupRow.public(), true, nil
}

// CreateTopup records a provider checkout and the exact rate snapshot.
func (s *Store) CreateTopup(ctx context.Context, userID, idempotencyKey string, payloadHash []byte, topup Topup) (Topup, bool, error) {
	type result struct {
		topup    Topup
		replayed bool
	}
	value, err := retrySerializable(ctx, func() (result, error) {
		created, replayed, commandErr := s.createTopup(ctx, userID, idempotencyKey, payloadHash, topup)
		return result{topup: created, replayed: replayed}, commandErr
	})
	return value.topup, value.replayed, err
}

func (s *Store) createTopup(ctx context.Context, userID, idempotencyKey string, payloadHash []byte, topup Topup) (Topup, bool, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return Topup{}, false, err
	}
	defer tx.Rollback()
	var existing struct {
		ID   string `db:"id"`
		Hash []byte `db:"payload_hash"`
	}
	err = tx.GetContext(ctx, &existing, `SELECT id, payload_hash FROM topups WHERE account_id=? AND idempotency_key=?`, topup.AccountID, idempotencyKey)
	if err == nil {
		if !bytes.Equal(existing.Hash, payloadHash) {
			return Topup{}, false, ErrIdempotencyConflict
		}
		stored, err := getTopup(ctx, tx, existing.ID)
		return stored.public(), true, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Topup{}, false, err
	}
	var owned int
	if err := tx.GetContext(ctx, &owned, `SELECT COUNT(*) FROM accounts WHERE id=? AND owner_user_id=? AND status='active'`, topup.AccountID, userID); err != nil {
		return Topup{}, false, err
	}
	if owned == 0 {
		return Topup{}, false, ErrNotFound
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO topups
		(id,account_id,payment_provider,provider_reference,fiat_currency,fiat_amount_minor,
		 base_fiat_minor,base_credit_microcredits,effective_fiat_minor,effective_credit_microcredits,
		 discount_bps,credit_microcredits,refundable_microcredits,status,checkout_url,
		 idempotency_key,payload_hash,created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,0,'pending',?,?,?,?)`, topup.ID, topup.AccountID,
		topup.PaymentProvider, topup.ProviderReference, topup.FiatCurrency, topup.FiatAmountMinor,
		topup.Rate.Base.FiatMinor, topup.Rate.Base.CreditMicrocredits, topup.Rate.Effective.FiatMinor,
		topup.Rate.Effective.CreditMicrocredits, topup.Rate.DiscountBPS,
		topup.CreditAmount.Microcredits, topup.CheckoutURL, idempotencyKey, payloadHash, topup.CreatedAt)
	if err != nil {
		return Topup{}, false, fmt.Errorf("insert top-up: %w", err)
	}
	if err := recordAudit(ctx, tx, "user", userID, "topup.created", "topup", topup.ID, "customer initiated provider checkout", "{}", topup.CreatedAt); err != nil {
		return Topup{}, false, fmt.Errorf("audit top-up create: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Topup{}, false, err
	}
	return topup, false, nil
}

func (s *Store) ListTopupsPage(ctx context.Context, userID, accountID string, query AccountListQuery) (AccountPage[Topup], error) {
	limit, offset, err := normalizeAccountListQuery(query)
	if err != nil {
		return AccountPage[Topup]{}, err
	}
	var owned int
	if err := s.db.GetContext(ctx, &owned, `SELECT COUNT(*) FROM accounts WHERE id=? AND owner_user_id=?`, accountID, userID); err != nil {
		return AccountPage[Topup]{}, err
	}
	if owned == 0 {
		return AccountPage[Topup]{}, ErrNotFound
	}
	var rows []topupRow
	if err := s.db.SelectContext(ctx, &rows, `SELECT `+topupColumns+` FROM topups WHERE account_id=? ORDER BY created_at DESC,id LIMIT ? OFFSET ?`, accountID, limit+1, offset); err != nil {
		return AccountPage[Topup]{}, err
	}
	result := make([]Topup, 0, len(rows))
	for _, row := range rows {
		result = append(result, row.public())
	}
	return accountPage(result, limit, offset), nil
}

// CompleteTopupEvent deduplicates one signed provider event and posts invoice,
// refundable lot, audit and balanced Credit issuance atomically.
func (s *Store) CompleteTopupEvent(ctx context.Context, eventID, providerReference, currency string, amountMinor int64, payloadHash []byte, receivedAt string) (Topup, bool, error) {
	type result struct {
		topup    Topup
		replayed bool
	}
	value, err := retrySerializable(ctx, func() (result, error) {
		completed, replayed, commandErr := s.completeTopupEvent(ctx, eventID, providerReference, currency, amountMinor, payloadHash, receivedAt)
		return result{topup: completed, replayed: replayed}, commandErr
	})
	return value.topup, value.replayed, err
}

func (s *Store) completeTopupEvent(ctx context.Context, eventID, providerReference, currency string, amountMinor int64, payloadHash []byte, receivedAt string) (Topup, bool, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return Topup{}, false, err
	}
	defer tx.Rollback()
	var existing struct {
		Hash   []byte `db:"payload_hash"`
		Ref    string `db:"provider_reference"`
		Status string `db:"status"`
	}
	err = tx.GetContext(ctx, &existing, `SELECT payload_hash,provider_reference,status FROM payment_provider_events WHERE event_id=?`, eventID)
	if err == nil {
		if !bytes.Equal(existing.Hash, payloadHash) {
			return Topup{}, false, ErrIdempotencyConflict
		}
		row, readErr := getTopupByProvider(ctx, tx, existing.Ref)
		if readErr != nil {
			return Topup{}, false, readErr
		}
		if existing.Status == "quarantined" {
			return row.public(), true, ErrPaymentMismatch
		}
		return row.public(), true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Topup{}, false, err
	}
	row, err := getTopupByProvider(ctx, tx, providerReference)
	if err != nil {
		return Topup{}, false, err
	}
	if row.FiatCurrency != currency || row.FiatAmountMinor != amountMinor {
		_, insertErr := tx.ExecContext(ctx, `INSERT INTO payment_provider_events(event_id,event_type,provider_reference,payload_hash,status,error_code,received_at) VALUES (?,'topup.succeeded',?,?,'quarantined','amount_or_currency_mismatch',?)`, eventID, providerReference, payloadHash, receivedAt)
		if insertErr != nil {
			return Topup{}, false, insertErr
		}
		metadata, marshalErr := json.Marshal(map[string]any{
			"provider_event_id":     eventID,
			"expected_currency":     row.FiatCurrency,
			"received_currency":     currency,
			"expected_amount_minor": row.FiatAmountMinor,
			"received_amount_minor": amountMinor,
		})
		if marshalErr != nil {
			return Topup{}, false, marshalErr
		}
		if err := recordAudit(ctx, tx, "system", "payment-provider", "topup.event_quarantined", "topup", row.ID, "provider amount or currency did not match checkout", string(metadata), receivedAt); err != nil {
			return Topup{}, false, fmt.Errorf("audit quarantined top-up event: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return Topup{}, false, err
		}
		return row.public(), false, ErrPaymentMismatch
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO payment_provider_events(event_id,event_type,provider_reference,payload_hash,status,received_at) VALUES (?,'topup.succeeded',?,?,'processed',?)`, eventID, providerReference, payloadHash, receivedAt)
	if err != nil {
		return Topup{}, false, err
	}
	if row.Status == "pending" {
		if err := postTopup(ctx, tx, row, eventID, receivedAt); err != nil {
			return Topup{}, false, err
		}
		row.Status, row.Refundable, row.CompletedAt = "succeeded", row.Credit, &receivedAt
		metadata, marshalErr := json.Marshal(map[string]string{"provider_event_id": eventID})
		if marshalErr != nil {
			return Topup{}, false, marshalErr
		}
		if err := recordAudit(ctx, tx, "system", "payment-provider", "topup.completed", "topup", row.ID, "signed provider event completed top-up", string(metadata), receivedAt); err != nil {
			return Topup{}, false, fmt.Errorf("audit top-up completion: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return Topup{}, false, err
	}
	return row.public(), false, nil
}

func postTopup(ctx context.Context, tx *boundTx, row topupRow, eventID, at string) error {
	var userLedger, systemLedger string
	if err := tx.GetContext(ctx, &userLedger, `SELECT id FROM ledger_accounts WHERE owner_account_id=? AND asset_code='GIZ_CREDIT'`, row.AccountID); err != nil {
		return err
	}
	if err := tx.GetContext(ctx, &systemLedger, `SELECT id FROM ledger_accounts WHERE code='SYSTEM:CREDIT_LIABILITY'`); err != nil {
		return err
	}
	ledgerID := uuid.NewString()
	if _, err := tx.ExecContext(ctx, `INSERT INTO ledger_transactions(id,transaction_type,status,idempotency_key,initiated_by_account_id,reference_type,reference_id,description,created_at,posted_at) VALUES (?,'topup','posted',?,?,'topup',?,'Fiat top-up',?,?)`, ledgerID, "provider:"+eventID, row.AccountID, row.ID, at, at); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO ledger_entries(id,transaction_id,ledger_account_id,sequence,direction,amount_microcredits,created_at) VALUES (?,?,?,1,'credit',?,?),(?,?,?,2,'debit',?,?)`, uuid.NewString(), ledgerID, userLedger, row.Credit, at, uuid.NewString(), ledgerID, systemLedger, row.Credit, at); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO credit_lots(id,account_id,topup_id,original_microcredits,remaining_microcredits,created_at) VALUES (?,?,?,?,?,?)`, uuid.NewString(), row.AccountID, row.ID, row.Credit, row.Credit, at); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO invoices(id,account_id,topup_id,invoice_number,fiat_currency,fiat_amount_minor,issued_at) VALUES (?,?,?,?,?,?,?)`, uuid.NewString(), row.AccountID, row.ID, "INV-"+eventID, row.FiatCurrency, row.FiatAmountMinor, at); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE topups SET status='succeeded',refundable_microcredits=?,completed_at=? WHERE id=? AND status='pending'`, row.Credit, at, row.ID)
	return err
}

type RefundRecord struct {
	ID              string       `json:"id"`
	TopupID         string       `json:"topup_id"`
	CreditAmount    CreditAmount `json:"credit_amount"`
	FiatAmountMinor int64        `json:"fiat_amount_minor"`
	Status          string       `json:"status"`
	CreatedAt       string       `json:"created_at"`
	CompletedAt     *string      `json:"completed_at,omitempty"`
}

// Invoice is the immutable customer-visible fiat purchase receipt. Provider
// references and credentials deliberately do not belong to this projection.
type Invoice struct {
	ID              string `db:"id" json:"id"`
	AccountID       string `db:"account_id" json:"account_id"`
	TopupID         string `db:"topup_id" json:"topup_id"`
	InvoiceNumber   string `db:"invoice_number" json:"invoice_number"`
	FiatCurrency    string `db:"fiat_currency" json:"fiat_currency"`
	FiatAmountMinor int64  `db:"fiat_amount_minor" json:"fiat_amount_minor"`
	IssuedAt        string `db:"issued_at" json:"issued_at"`
}

func (s *Store) ListInvoices(ctx context.Context, userID, accountID string) ([]Invoice, error) {
	var invoices []Invoice
	err := s.db.SelectContext(ctx, &invoices, `SELECT i.id,i.account_id,i.topup_id,i.invoice_number,i.fiat_currency,i.fiat_amount_minor,i.issued_at FROM invoices i JOIN accounts a ON a.id=i.account_id WHERE i.account_id=? AND a.owner_user_id=? ORDER BY i.issued_at DESC,i.id`, accountID, userID)
	return invoices, err
}

func (s *Store) GetInvoice(ctx context.Context, userID, accountID, invoiceID string) (Invoice, error) {
	var invoice Invoice
	err := s.db.GetContext(ctx, &invoice, `SELECT i.id,i.account_id,i.topup_id,i.invoice_number,i.fiat_currency,i.fiat_amount_minor,i.issued_at FROM invoices i JOIN accounts a ON a.id=i.account_id WHERE i.id=? AND i.account_id=? AND a.owner_user_id=?`, invoiceID, accountID, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return Invoice{}, ErrNotFound
	}
	return invoice, err
}

type refundRow struct {
	ID          string  `db:"id"`
	TopupID     string  `db:"topup_id"`
	Credit      int64   `db:"credit_microcredits"`
	Fiat        int64   `db:"fiat_amount_minor"`
	Status      string  `db:"status"`
	CreatedAt   string  `db:"created_at"`
	CompletedAt *string `db:"completed_at"`
}

// RecoverableRefund is the durable provider command reconstructed from Store
// state. Provider routing and the exact fiat amount come from the originating
// top-up/refund snapshots, never from a retrying client's request body.
type RecoverableRefund struct {
	Refund            RefundRecord
	ProviderReference string
	Currency          string
}

func (r refundRow) public() RefundRecord {
	return RefundRecord{ID: r.ID, TopupID: r.TopupID, CreditAmount: CreditAmount{Asset: "GIZ_CREDIT", Microcredits: r.Credit}, FiatAmountMinor: r.Fiat, Status: r.Status, CreatedAt: r.CreatedAt, CompletedAt: r.CompletedAt}
}

func (s *Store) CreateRefund(ctx context.Context, userID, accountID, topupID, idempotencyKey string, payloadHash []byte, credit int64, createdAt string) (RefundRecord, string, string, bool, error) {
	type result struct {
		refund            RefundRecord
		providerReference string
		currency          string
		replayed          bool
	}
	value, err := retrySerializable(ctx, func() (result, error) {
		refund, providerReference, currency, replayed, commandErr := s.createRefund(ctx, userID, accountID, topupID, idempotencyKey, payloadHash, credit, createdAt)
		return result{refund: refund, providerReference: providerReference, currency: currency, replayed: replayed}, commandErr
	})
	return value.refund, value.providerReference, value.currency, value.replayed, err
}

func (s *Store) createRefund(ctx context.Context, userID, accountID, topupID, idempotencyKey string, payloadHash []byte, credit int64, createdAt string) (RefundRecord, string, string, bool, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return RefundRecord{}, "", "", false, err
	}
	defer tx.Rollback()
	var existing struct {
		ID   string `db:"id"`
		Hash []byte `db:"payload_hash"`
	}
	err = tx.GetContext(ctx, &existing, `SELECT id,payload_hash FROM refunds WHERE account_id=? AND idempotency_key=?`, accountID, idempotencyKey)
	if err == nil {
		if !bytes.Equal(existing.Hash, payloadHash) {
			return RefundRecord{}, "", "", false, ErrIdempotencyConflict
		}
		rr, e := getRefund(ctx, tx, existing.ID)
		if e != nil {
			return RefundRecord{}, "", "", false, e
		}
		tr, e := getTopup(ctx, tx, rr.TopupID)
		return rr.public(), tr.ProviderReference, tr.FiatCurrency, true, e
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return RefundRecord{}, "", "", false, err
	}
	var owner int
	if err := tx.GetContext(ctx, &owner, `SELECT COUNT(*) FROM accounts a WHERE a.id=? AND a.owner_user_id=? AND a.status='active' AND (a.kind<>'merchant' OR EXISTS (SELECT 1 FROM merchant_accounts m WHERE m.account_id=a.id AND m.merchant_status IN ('pending','approved')))`, accountID, userID); err != nil {
		return RefundRecord{}, "", "", false, err
	}
	if owner == 0 {
		return RefundRecord{}, "", "", false, ErrNotFound
	}
	topup, err := getTopup(ctx, tx, topupID)
	if err != nil || topup.AccountID != accountID {
		return RefundRecord{}, "", "", false, ErrNotFound
	}
	if topup.Status != "succeeded" && topup.Status != "partially_refunded" {
		return RefundRecord{}, "", "", false, ErrInsufficientBalance
	}
	var lotRemaining, pending int64
	if err := tx.GetContext(ctx, &lotRemaining, `SELECT remaining_microcredits FROM credit_lots WHERE topup_id=?`, topupID); err != nil {
		return RefundRecord{}, "", "", false, err
	}
	if err := tx.GetContext(ctx, &pending, `SELECT COALESCE(SUM(credit_microcredits),0) FROM refunds WHERE topup_id=? AND status='pending'`, topupID); err != nil {
		return RefundRecord{}, "", "", false, err
	}
	available, err := availableCredit(ctx, tx, accountID)
	if err != nil {
		return RefundRecord{}, "", "", false, err
	}
	if credit <= 0 || credit > lotRemaining-pending || credit > available {
		return RefundRecord{}, "", "", false, ErrInsufficientBalance
	}
	if credit > math.MaxInt64/topup.EffectiveFiatMinor {
		return RefundRecord{}, "", "", false, errors.New("refund conversion overflow")
	}
	fiat := credit * topup.EffectiveFiatMinor / topup.EffectiveCredit
	if fiat <= 0 {
		return RefundRecord{}, "", "", false, errors.New("refund is below provider minimum")
	}
	rr := refundRow{ID: uuid.NewString(), TopupID: topupID, Credit: credit, Fiat: fiat, Status: "pending", CreatedAt: createdAt}
	_, err = tx.ExecContext(ctx, `INSERT INTO refunds(id,topup_id,account_id,credit_microcredits,fiat_amount_minor,status,idempotency_key,payload_hash,created_at) VALUES (?,?,?,?,?,'pending',?,?,?)`, rr.ID, topupID, accountID, credit, fiat, idempotencyKey, payloadHash, createdAt)
	if err != nil {
		return RefundRecord{}, "", "", false, err
	}
	if err := recordAudit(ctx, tx, "user", userID, "topup_refund.created", "refund", rr.ID, "customer requested original-route refund", "{}", createdAt); err != nil {
		return RefundRecord{}, "", "", false, err
	}
	if err := tx.Commit(); err != nil {
		return RefundRecord{}, "", "", false, err
	}
	return rr.public(), topup.ProviderReference, topup.FiatCurrency, false, nil
}

func (s *Store) CompleteRefund(ctx context.Context, refundID, providerRefundID, completedAt string) (RefundRecord, error) {
	return retrySerializable(ctx, func() (RefundRecord, error) {
		return s.completeRefund(ctx, refundID, providerRefundID, completedAt)
	})
}

// FailRefund makes a definitive provider rejection terminal and immediately
// releases the pending-refund hold from available Credit. Ambiguous transport
// failures must not call this method: they remain pending for durable recovery.
func (s *Store) FailRefund(ctx context.Context, refundID, failedAt, reason string) (RefundRecord, error) {
	return retrySerializable(ctx, func() (RefundRecord, error) {
		tx, err := s.db.BeginTxx(ctx, nil)
		if err != nil {
			return RefundRecord{}, err
		}
		defer tx.Rollback()
		rr, err := getRefund(ctx, tx, refundID)
		if err != nil {
			return RefundRecord{}, err
		}
		if rr.Status != "pending" {
			return rr.public(), nil
		}
		result, err := tx.ExecContext(ctx, `UPDATE refunds SET status='failed',completed_at=? WHERE id=? AND status='pending'`, failedAt, refundID)
		if err != nil {
			return RefundRecord{}, err
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return RefundRecord{}, err
		}
		if updated != 1 {
			// A concurrent recovery completed the same provider command. Read its
			// terminal state instead of manufacturing a second transition/audit.
			rr, err = getRefund(ctx, tx, refundID)
			if err != nil {
				return RefundRecord{}, err
			}
			return rr.public(), nil
		}
		if err := recordAudit(ctx, tx, "system", "payment-provider", "topup_refund.failed", "refund", refundID, reason, "{}", failedAt); err != nil {
			return RefundRecord{}, err
		}
		if err := tx.Commit(); err != nil {
			return RefundRecord{}, err
		}
		rr.Status = "failed"
		rr.CompletedAt = &failedAt
		return rr.public(), nil
	})
}

// RecoverableRefunds returns pending provider commands for the background
// reconciler. Stable refund IDs are also provider idempotency keys, so an
// ambiguous provider commit can be queried/replayed without a second refund.
func (s *Store) RecoverableRefunds(ctx context.Context, limit int) ([]RecoverableRefund, error) {
	if limit <= 0 {
		return []RecoverableRefund{}, nil
	}
	type row struct {
		refundRow
		ProviderReference string `db:"provider_reference"`
		Currency          string `db:"fiat_currency"`
	}
	var rows []row
	if err := s.db.SelectContext(ctx, &rows, `
		SELECT r.id,r.topup_id,r.credit_microcredits,r.fiat_amount_minor,r.status,r.created_at,r.completed_at,
		       t.provider_reference,t.fiat_currency
		FROM refunds r
		JOIN topups t ON t.id=r.topup_id
		WHERE r.status='pending'
		ORDER BY r.created_at,r.id
		LIMIT ?`, limit); err != nil {
		return nil, err
	}
	result := make([]RecoverableRefund, 0, len(rows))
	for _, item := range rows {
		result = append(result, RecoverableRefund{
			Refund: item.refundRow.public(), ProviderReference: item.ProviderReference, Currency: item.Currency,
		})
	}
	return result, nil
}

func (s *Store) completeRefund(ctx context.Context, refundID, providerRefundID, completedAt string) (RefundRecord, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return RefundRecord{}, err
	}
	defer tx.Rollback()
	rr, err := getRefund(ctx, tx, refundID)
	if err != nil {
		return RefundRecord{}, err
	}
	// Provider results may arrive concurrently or out of order. Both succeeded
	// and failed are terminal: a late success must never debit Credit or consume
	// the purchased lot after a definitive failure has already released the hold.
	if rr.Status != "pending" {
		return rr.public(), nil
	}
	var accountID, userLedger, systemLedger string
	if err := tx.GetContext(ctx, &accountID, `SELECT account_id FROM refunds WHERE id=?`, refundID); err != nil {
		return RefundRecord{}, err
	}
	if err := tx.GetContext(ctx, &userLedger, `SELECT id FROM ledger_accounts WHERE owner_account_id=? AND asset_code='GIZ_CREDIT'`, accountID); err != nil {
		return RefundRecord{}, err
	}
	if err := tx.GetContext(ctx, &systemLedger, `SELECT id FROM ledger_accounts WHERE code='SYSTEM:CREDIT_LIABILITY'`); err != nil {
		return RefundRecord{}, err
	}
	ledgerID := uuid.NewString()
	if _, err := tx.ExecContext(ctx, `INSERT INTO ledger_transactions(id,transaction_type,status,idempotency_key,initiated_by_account_id,reference_type,reference_id,description,created_at,posted_at) VALUES (?,'refund','posted',?,?,'refund',?,'Original-route refund',?,?)`, ledgerID, "refund:"+refundID, accountID, refundID, completedAt, completedAt); err != nil {
		return RefundRecord{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO ledger_entries(id,transaction_id,ledger_account_id,sequence,direction,amount_microcredits,created_at) VALUES (?,?,?,1,'debit',?,?),(?,?,?,2,'credit',?,?)`, uuid.NewString(), ledgerID, userLedger, rr.Credit, completedAt, uuid.NewString(), ledgerID, systemLedger, rr.Credit, completedAt); err != nil {
		return RefundRecord{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE credit_lots SET remaining_microcredits=remaining_microcredits-? WHERE topup_id=? AND remaining_microcredits>=?`, rr.Credit, rr.TopupID, rr.Credit)
	if err != nil {
		return RefundRecord{}, err
	}
	updated, err := result.RowsAffected()
	if err != nil || updated != 1 {
		return RefundRecord{}, ErrInsufficientBalance
	}
	var remaining int64
	if err := tx.GetContext(ctx, &remaining, `SELECT remaining_microcredits FROM credit_lots WHERE topup_id=?`, rr.TopupID); err != nil {
		return RefundRecord{}, err
	}
	status := "partially_refunded"
	if remaining == 0 {
		status = "refunded"
	}
	if _, err := tx.ExecContext(ctx, `UPDATE topups SET refundable_microcredits=?,status=? WHERE id=?`, remaining, status, rr.TopupID); err != nil {
		return RefundRecord{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE refunds SET status='succeeded',provider_refund_id=?,completed_at=? WHERE id=? AND status='pending'`, providerRefundID, completedAt, refundID); err != nil {
		return RefundRecord{}, err
	}
	if err := recordAudit(ctx, tx, "system", "payment-provider", "topup_refund.completed", "refund", refundID, "provider completed original-route refund", "{}", completedAt); err != nil {
		return RefundRecord{}, err
	}
	if err := tx.Commit(); err != nil {
		return RefundRecord{}, err
	}
	rr.Status = "succeeded"
	rr.CompletedAt = &completedAt
	return rr.public(), nil
}

func getTopup(ctx context.Context, q sqlx.QueryerContext, id string) (topupRow, error) {
	var row topupRow
	err := sqlx.GetContext(ctx, q, &row, `SELECT `+topupColumns+` FROM topups WHERE id=?`, id)
	err = recordCommandRetryFailure(ctx, err)
	if errors.Is(err, sql.ErrNoRows) {
		return row, ErrNotFound
	}
	return row, err
}
func getTopupByProvider(ctx context.Context, q sqlx.QueryerContext, ref string) (topupRow, error) {
	var row topupRow
	err := sqlx.GetContext(ctx, q, &row, `SELECT `+topupColumns+` FROM topups WHERE provider_reference=?`, ref)
	err = recordCommandRetryFailure(ctx, err)
	if errors.Is(err, sql.ErrNoRows) {
		return row, ErrNotFound
	}
	return row, err
}
func getRefund(ctx context.Context, q sqlx.QueryerContext, id string) (refundRow, error) {
	var row refundRow
	err := sqlx.GetContext(ctx, q, &row, `SELECT id,topup_id,credit_microcredits,fiat_amount_minor,status,created_at,completed_at FROM refunds WHERE id=?`, id)
	err = recordCommandRetryFailure(ctx, err)
	if errors.Is(err, sql.ErrNoRows) {
		return row, ErrNotFound
	}
	return row, err
}

// availableCredit is the single spendability definition used by every
// outgoing-credit command. A posted balance is not spendable while it backs
// either an active AI reservation or a provider refund that has been created
// but not yet committed to the ledger.
func availableCredit(ctx context.Context, tx *boundTx, accountID string) (int64, error) {
	var spendable int
	if err := tx.GetContext(ctx, &spendable, `SELECT COUNT(*) FROM accounts a JOIN ledger_accounts l ON l.owner_account_id=a.id AND l.asset_code='GIZ_CREDIT' WHERE a.id=? AND a.status='active' AND l.status='active'`, accountID); err != nil {
		return 0, err
	}
	if spendable == 0 {
		return 0, ErrAccountFrozen
	}
	var available int64
	err := tx.GetContext(ctx, &available, `
		SELECT b.balance_microcredits
		     - COALESCE((SELECT SUM(r.amount_microcredits)
		                 FROM credit_reservations r
		                 WHERE r.account_id=? AND r.status='active'),0)
		     - COALESCE((SELECT SUM(f.credit_microcredits)
		                 FROM refunds f
		                 WHERE f.account_id=? AND f.status='pending'),0)
		FROM account_balances b
		WHERE b.account_id=? AND b.asset_code='GIZ_CREDIT'
	`, accountID, accountID, accountID)
	return available, err
}

// consumePurchasedLots applies the conservative refundable-credit policy: any
// outgoing Credit consumes refundable purchased lots before earned/untracked
// Credit. This prevents a cash withdrawal after spend or transfer.
func consumePurchasedLots(ctx context.Context, tx *boundTx, accountID string, amount int64) error {
	if amount <= 0 {
		return nil
	}
	var lots []struct {
		ID        string `db:"id"`
		TopupID   string `db:"topup_id"`
		Remaining int64  `db:"remaining_microcredits"`
		Pending   int64  `db:"pending_refund_microcredits"`
	}
	if err := tx.SelectContext(ctx, &lots, `
		SELECT l.id,l.topup_id,l.remaining_microcredits,
		       COALESCE((SELECT SUM(r.credit_microcredits) FROM refunds r
		                 WHERE r.topup_id=l.topup_id AND r.status='pending'),0)
		         AS pending_refund_microcredits
		FROM credit_lots l
		WHERE l.account_id=? AND l.remaining_microcredits>0
		ORDER BY l.created_at,l.id
	`, accountID); err != nil {
		return err
	}
	left := amount
	for _, lot := range lots {
		if left == 0 {
			break
		}
		// Pending refunds already reserve their original lot. Other outgoing
		// operations may consume only the unreserved remainder.
		used := lot.Remaining - lot.Pending
		if used <= 0 {
			continue
		}
		if used > left {
			used = left
		}
		remaining := lot.Remaining - used
		if _, err := tx.ExecContext(ctx, `UPDATE credit_lots SET remaining_microcredits=? WHERE id=?`, remaining, lot.ID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE topups SET refundable_microcredits=? WHERE id=?`, remaining, lot.TopupID); err != nil {
			return err
		}
		left -= used
	}
	return nil
}
