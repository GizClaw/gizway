package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/idy/gizway/internal/service/quotaexchange"
	"github.com/idy/gizway/internal/timetext"
)

// QuotaExchangeResult is the center's answer after all supplied Usage has
// committed. Posted may be negative; Quota is always clamped to a nonnegative
// value suitable for a Gateway's local admission counter.
type QuotaExchangeResult struct {
	PostedMicrocredits int64
	QuotaMicrocredits  int64
	Allowed            bool
}

type concurrentUCGIDError struct{ cause error }

func (e concurrentUCGIDError) Error() string    { return e.cause.Error() }
func (e concurrentUCGIDError) Unwrap() error    { return e.cause }
func (e concurrentUCGIDError) SQLState() string { return "40001" }

func retryConcurrentUCGID(ctx context.Context, err error) error {
	var state sqlStateError
	if errors.As(err, &state) && state.SQLState() == "23505" {
		// The idempotency key is derived exclusively from UCGID. Under
		// SERIALIZABLE isolation, two first-seen copies may race at this unique
		// constraint before the losing snapshot can observe the received row.
		// Rebuild the whole transaction; the next attempt performs the canonical
		// payload equality check and either acknowledges or returns conflict.
		return recordCommandRetryFailure(ctx, concurrentUCGIDError{cause: err})
	}
	return err
}

// ExchangeQuota validates the customer credential, atomically ingests the
// optional Usage batch, and independently calculates the current quota. It
// deliberately accepts the raw key only as an argument: the transaction hashes
// it immediately and no SQL statement, persisted row, or returned error owns
// the secret text.
func (s *Store) ExchangeQuota(ctx context.Context, rawAPIKey, nodeID, region string, usage []quotaexchange.UsageRecord) (QuotaExchangeResult, error) {
	type result struct {
		value QuotaExchangeResult
	}
	value, err := retrySerializable(ctx, func() (result, error) {
		exchanged, exchangeErr := s.exchangeQuota(ctx, rawAPIKey, nodeID, region, usage)
		return result{value: exchanged}, exchangeErr
	})
	return value.value, err
}

func (s *Store) exchangeQuota(ctx context.Context, rawAPIKey, nodeID, region string, usage []quotaexchange.UsageRecord) (QuotaExchangeResult, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return QuotaExchangeResult{}, fmt.Errorf("begin quota exchange: %w", err)
	}
	defer tx.Rollback()

	secretHash := sha256.Sum256([]byte(rawAPIKey))
	var principal struct {
		AccountID string `db:"account_id"`
	}
	err = tx.GetContext(ctx, &principal, `
		SELECT k.account_id
		FROM api_keys k
		JOIN accounts a ON a.id=k.account_id
		JOIN users u ON u.id=a.owner_user_id
		WHERE k.secret_hash=? AND k.kind='gateway' AND k.status='active'
		  AND (k.expires_at IS NULL OR k.expires_at>?)
		  AND a.status='active' AND u.status='active'
	`, secretHash[:], timetext.Format(s.now()))
	if errors.Is(err, sql.ErrNoRows) {
		return QuotaExchangeResult{}, ErrNotFound
	}
	if err != nil {
		return QuotaExchangeResult{}, fmt.Errorf("authenticate quota key: %w", err)
	}

	// This row lock is the account's central settlement boundary. Independent
	// regional nodes may have granted overlapping local quota already, so the
	// transaction never rejects an otherwise valid Usage record for insufficient
	// funds; it serializes the resulting finite negative balance instead.
	var accountLedger struct {
		ID     string `db:"id"`
		Status string `db:"status"`
	}
	if err := tx.GetContext(ctx, &accountLedger, `SELECT id,status FROM ledger_accounts WHERE owner_account_id=? AND asset_code='GIZ_CREDIT' FOR UPDATE`, principal.AccountID); err != nil {
		return QuotaExchangeResult{}, fmt.Errorf("lock account ledger: %w", err)
	}

	receivedAt := timetext.Format(s.now())
	for _, record := range usage {
		payloadHash, err := canonicalUsageHash(record)
		if err != nil {
			return QuotaExchangeResult{}, fmt.Errorf("hash usage payload: %w", err)
		}
		var existingHash []byte
		err = tx.GetContext(ctx, &existingHash, `SELECT canonical_payload_hash FROM gateway_usage_records WHERE ucgid=?`, record.UCGID)
		if err == nil {
			if !bytes.Equal(existingHash, payloadHash) {
				return QuotaExchangeResult{}, ErrUCGIDConflict
			}
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return QuotaExchangeResult{}, fmt.Errorf("lookup received UCGID: %w", err)
		}

		var priceRows []struct {
			RateVersionID string `db:"rate_version_id"`
			Metric        string `db:"metric"`
			UnitSize      int64  `db:"unit_size"`
			Microcredits  int64  `db:"customer_price_microcredits"`
		}
		if err := tx.SelectContext(ctx, &priceRows, `
			SELECT p.id AS rate_version_id,p.metric,p.unit_size,p.customer_price_microcredits
			FROM billing_rate_versions p
			JOIN billing_rate_publications publication ON publication.id=p.publication_id
			WHERE p.publication_id=? AND p.model_variant_id=? AND p.public_model=?
			  AND publication.node_id=? AND publication.region=? AND publication.status IN ('active','retired')
		`, record.RatePublicationID, record.ModelVariantID, record.PublicModel, nodeID, region); err != nil {
			return QuotaExchangeResult{}, fmt.Errorf("read rate publication: %w", err)
		}
		prices := make(map[string]quotaexchange.Price, len(priceRows))
		rateVersions := make(map[string]string, len(priceRows))
		for _, price := range priceRows {
			prices[price.Metric] = quotaexchange.Price{UnitSize: price.UnitSize, Microcredits: price.Microcredits}
			rateVersions[price.Metric] = price.RateVersionID
		}
		charge, err := quotaexchange.PriceMetrics(record.Metrics, prices)
		if err != nil {
			return QuotaExchangeResult{}, ErrUnpriceableUsage
		}

		ledgerID := uuid.NewString()
		if _, err := tx.ExecContext(ctx, `INSERT INTO ledger_transactions
			(id,transaction_type,status,idempotency_key,initiated_by_account_id,reference_type,reference_id,description,created_at,posted_at)
			VALUES (?,'gateway_usage','posted',?,?,'received_usage',?,'Received regional AI usage',?,?)`,
			ledgerID, "ucgid:"+record.UCGID, principal.AccountID, record.UCGID, receivedAt, receivedAt); err != nil {
			return QuotaExchangeResult{}, fmt.Errorf("insert usage ledger transaction: %w", retryConcurrentUCGID(ctx, err))
		}
		if charge > 0 {
			var systemLedgerID string
			if err := tx.GetContext(ctx, &systemLedgerID, `SELECT id FROM ledger_accounts WHERE code='SYSTEM:CREDIT_LIABILITY'`); err != nil {
				return QuotaExchangeResult{}, fmt.Errorf("read system ledger: %w", err)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO ledger_entries
				(id,transaction_id,ledger_account_id,sequence,direction,amount_microcredits,created_at)
				VALUES (?,?,?,1,'debit',?,?),(?,?,?,2,'credit',?,?)`,
				uuid.NewString(), ledgerID, accountLedger.ID, charge, receivedAt,
				uuid.NewString(), ledgerID, systemLedgerID, charge, receivedAt); err != nil {
				return QuotaExchangeResult{}, fmt.Errorf("insert usage ledger entries: %w", err)
			}
			if err := consumePurchasedLots(ctx, tx, principal.AccountID, charge); err != nil {
				return QuotaExchangeResult{}, fmt.Errorf("consume refundable lots for usage: %w", err)
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO gateway_usage_records
			(ucgid,account_id,node_id,region,operation_id,public_model,model_variant_id,
			 rate_publication_id,canonical_payload_hash,charged_microcredits,ledger_transaction_id,
			 started_at,completed_at,received_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, record.UCGID, principal.AccountID,
			nodeID, region, record.OperationID, record.PublicModel, record.ModelVariantID,
			record.RatePublicationID, payloadHash, charge, ledgerID, record.StartedAt, record.CompletedAt, receivedAt); err != nil {
			return QuotaExchangeResult{}, fmt.Errorf("insert received usage: %w", err)
		}
		metrics := make([]string, 0, len(record.Metrics))
		for metric := range record.Metrics {
			metrics = append(metrics, metric)
		}
		sort.Strings(metrics)
		for _, metric := range metrics {
			price := prices[metric]
			metricCharge, err := quotaexchange.PriceMetrics(map[string]int64{metric: record.Metrics[metric]}, prices)
			if err != nil {
				return QuotaExchangeResult{}, ErrUnpriceableUsage
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO gateway_usage_metrics
				(ucgid,rate_version_id,metric,quantity,unit_size,price_microcredits,charged_microcredits)
				VALUES (?,?,?,?,?,?,?)`, record.UCGID, rateVersions[metric], metric, record.Metrics[metric], price.UnitSize, price.Microcredits, metricCharge); err != nil {
				return QuotaExchangeResult{}, fmt.Errorf("insert received usage metric: %w", err)
			}
		}
	}

	var posted int64
	if err := tx.GetContext(ctx, &posted, `SELECT balance_microcredits FROM account_balances WHERE account_id=? AND asset_code='GIZ_CREDIT'`, principal.AccountID); err != nil {
		return QuotaExchangeResult{}, fmt.Errorf("read post-exchange balance: %w", err)
	}
	// Pending original-route refunds are actual financial holds. Regional AI
	// reservations are intentionally excluded: Quota itself never reserves.
	var paymentHolds int64
	if err := tx.GetContext(ctx, &paymentHolds, `
		SELECT
		  COALESCE((SELECT SUM(credit_microcredits) FROM refunds WHERE account_id=? AND status='pending'),0) +
		  COALESCE((SELECT SUM(amount_microcredits) FROM credit_holds WHERE account_id=? AND status='active' AND expires_at>?),0)
	`, principal.AccountID, principal.AccountID, timetext.Format(s.now())); err != nil {
		return QuotaExchangeResult{}, fmt.Errorf("read payment holds: %w", err)
	}
	quota, allowed := quotaexchange.CurrentQuota(posted, paymentHolds)
	if accountLedger.Status != "active" {
		quota, allowed = 0, false
	}
	if err := tx.Commit(); err != nil {
		return QuotaExchangeResult{}, fmt.Errorf("commit quota exchange: %w", err)
	}
	return QuotaExchangeResult{PostedMicrocredits: posted, QuotaMicrocredits: quota, Allowed: allowed}, nil
}

func canonicalUsageHash(record quotaexchange.UsageRecord) ([]byte, error) {
	encoded, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(encoded)
	return digest[:], nil
}
