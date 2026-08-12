package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/idy/gizway/internal/service/quotaexchange"
	"github.com/idy/gizway/internal/timetext"
)

// ClaimedUsage is an Exchange-ready row. Payload contains only the canonical
// Usage record; process_epoch and runtime_key_token are deliberately omitted.
type ClaimedUsage struct {
	UCGID   string `db:"ucgid"`
	Payload JSON   `db:"payload"`
}

// EnqueueUsage records only the identity-free Usage envelope. runtimeKeyToken
// is a random current-process lookup token, not a key hash; it has no meaning
// after restart and must never be sent to GizPay.
func (s *Store) EnqueueUsage(ctx context.Context, processEpoch, runtimeKeyToken string, usage quotaexchange.UsageRecord) error {
	return s.EnqueuePricedUsage(ctx, processEpoch, runtimeKeyToken, usage, 0)
}

// EnqueuePricedUsage persists a provider result before it can be reported.
// The locally calculated amount is diagnostic only; GizPay always recomputes
// the charge from its immutable publication snapshot.
func (s *Store) EnqueuePricedUsage(ctx context.Context, processEpoch, runtimeKeyToken string, usage quotaexchange.UsageRecord, calculatedMicrocredits int64) error {
	if processEpoch == "" || runtimeKeyToken == "" {
		return errors.New("process epoch and runtime key token are required")
	}
	if calculatedMicrocredits < 0 {
		return errors.New("calculated usage must be nonnegative")
	}
	payload, err := json.Marshal(usage)
	if err != nil {
		return fmt.Errorf("marshal usage outbox payload: %w", err)
	}
	payloadHash, err := canonicalUsageHash(usage)
	if err != nil {
		return fmt.Errorf("hash usage outbox payload: %w", err)
	}
	now := timetext.Format(s.now())
	_, err = s.db.ExecContext(ctx, `INSERT INTO gateway_usage_outbox
		(ucgid,process_epoch,runtime_key_token,operation_id,public_model,model_variant_id,
		 rate_publication_id,period_started_at,period_ended_at,gateway_calculated_microcredits,
		 canonical_payload_hash,payload,status,next_attempt_at,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?, 'pending',?,?,?)`, usage.UCGID, processEpoch, runtimeKeyToken,
		usage.OperationID, usage.PublicModel, usage.ModelVariantID, usage.RatePublicationID,
		usage.StartedAt, usage.CompletedAt, calculatedMicrocredits, payloadHash, payload, now, now, now)
	if err != nil {
		return fmt.Errorf("enqueue usage: %w", err)
	}
	return nil
}

// ClaimUsage selects only rows tied to the caller's current in-memory key
// context. The whole selection and pending->sending transition is atomic, and
// the encoded body cap is enforced before anything is sent to GizPay.
func (s *Store) ClaimUsage(ctx context.Context, processEpoch, runtimeKeyToken string, maxRecords, maxBytes int) ([]ClaimedUsage, error) {
	if processEpoch == "" || runtimeKeyToken == "" || maxRecords <= 0 || maxBytes <= 0 {
		return nil, errors.New("valid process, token, record limit, and byte limit are required")
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin Usage claim: %w", err)
	}
	defer tx.Rollback()
	var candidates []ClaimedUsage
	if err := tx.SelectContext(ctx, &candidates, `
		SELECT ucgid,payload
		FROM gateway_usage_outbox
		WHERE process_epoch=? AND runtime_key_token=? AND status IN ('pending','sending') AND next_attempt_at<=?
		ORDER BY created_at,ucgid
		LIMIT ?
		FOR UPDATE SKIP LOCKED
	`, processEpoch, runtimeKeyToken, timetext.Format(s.now()), maxRecords); err != nil {
		return nil, fmt.Errorf("select Usage claim: %w", err)
	}
	claimed := make([]ClaimedUsage, 0, len(candidates))
	encodedBytes := 2 // JSON array brackets; commas are counted below.
	for _, candidate := range candidates {
		additional := len(candidate.Payload)
		if len(claimed) > 0 {
			additional++
		}
		if encodedBytes+additional > maxBytes {
			break
		}
		encodedBytes += additional
		claimed = append(claimed, candidate)
	}
	if len(claimed) == 0 {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit empty Usage claim: %w", err)
		}
		return nil, nil
	}
	query, args := usageIDsQuery(`UPDATE gateway_usage_outbox
		SET status='sending',attempt_count=attempt_count+1,updated_at=?
		WHERE status IN ('pending','sending') AND ucgid IN (%s)`, []any{timetext.Format(s.now())}, claimed)
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return nil, fmt.Errorf("mark Usage sending: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit Usage claim: %w", err)
	}
	return claimed, nil
}

// MarkUsageReported completes the local queue transition only after one
// successful Exchange acknowledged the entire batch.
func (s *Store) MarkUsageReported(ctx context.Context, ucgids []string) error {
	if len(ucgids) == 0 {
		return nil
	}
	claimed := make([]ClaimedUsage, len(ucgids))
	for i, id := range ucgids {
		claimed[i].UCGID = id
	}
	now := timetext.Format(s.now())
	query, args := usageIDsQuery(`UPDATE gateway_usage_outbox
		SET status='reported',reported_at=?,updated_at=?,last_error=NULL
		WHERE status='sending' AND ucgid IN (%s)`, []any{now, now}, claimed)
	if _, err := s.db.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("mark Usage reported: %w", err)
	}
	return nil
}

// MarkUsageFailed terminates rows rejected for a permanent protocol or
// business reason. Failed rows remain visible to regional diagnostics but can
// never be claimed by the current-process retry worker again.
func (s *Store) MarkUsageFailed(ctx context.Context, ucgids []string, message string) error {
	if len(ucgids) == 0 {
		return nil
	}
	claimed := make([]ClaimedUsage, len(ucgids))
	for i, id := range ucgids {
		claimed[i].UCGID = id
	}
	now := timetext.Format(s.now())
	query, args := usageIDsQuery(`UPDATE gateway_usage_outbox
		SET status='failed',last_error=?,failed_at=?,updated_at=?
		WHERE status='sending' AND ucgid IN (%s)`, []any{message, now, now}, claimed)
	if _, err := s.db.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("mark Usage failed: %w", err)
	}
	return nil
}

// HasUnreportedUsage lets the current-process worker forget the raw key as
// soon as every associated row is reported. It intentionally ignores rows
// from prior process epochs, which startup handles by abandonment.
func (s *Store) HasUnreportedUsage(ctx context.Context, processEpoch, runtimeKeyToken string) (bool, error) {
	var exists bool
	if err := s.db.GetContext(ctx, &exists, `SELECT EXISTS (
		SELECT 1 FROM gateway_usage_outbox
		WHERE process_epoch=? AND runtime_key_token=? AND status IN ('pending','sending')
	)`, processEpoch, runtimeKeyToken); err != nil {
		return false, fmt.Errorf("check unreported Usage: %w", err)
	}
	return exists, nil
}

// ReturnUsagePending makes a current-process network failure retryable. It is
// never called during startup recovery; startup abandons the rows instead.
func (s *Store) ReturnUsagePending(ctx context.Context, ucgids []string, message string) error {
	if len(ucgids) == 0 {
		return nil
	}
	claimed := make([]ClaimedUsage, len(ucgids))
	for i, id := range ucgids {
		claimed[i].UCGID = id
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin Usage retry: %w", err)
	}
	defer tx.Rollback()
	type retryRow struct {
		UCGID        string `db:"ucgid"`
		AttemptCount int64  `db:"attempt_count"`
	}
	query, args := usageIDsQuery(`SELECT ucgid,attempt_count FROM gateway_usage_outbox
		WHERE status='sending' AND ucgid IN (%s) FOR UPDATE`, nil, claimed)
	var rows []retryRow
	if err := tx.SelectContext(ctx, &rows, query, args...); err != nil {
		return fmt.Errorf("select Usage retry: %w", err)
	}
	now := s.now()
	for _, row := range rows {
		// First retry waits one second, then doubles up to five minutes. The
		// schedule is deliberately local and short-lived: restart abandons these
		// rows instead of turning retry metadata into a recovery contract.
		exponent := min(max(row.AttemptCount-1, 0), 9)
		delaySeconds := min(int64(1)<<uint(exponent), int64(300))
		if _, err := tx.ExecContext(ctx, `UPDATE gateway_usage_outbox
			SET status='pending',last_error=?,next_attempt_at=?,updated_at=?
			WHERE status='sending' AND ucgid=?`, message,
			timetext.Format(now.Add(time.Duration(delaySeconds)*time.Second)), timetext.Format(now), row.UCGID); err != nil {
			return fmt.Errorf("return Usage pending: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit Usage retry: %w", err)
	}
	return nil
}

func usageIDsQuery(format string, prefix []any, claimed []ClaimedUsage) (string, []any) {
	placeholders := make([]string, len(claimed))
	args := make([]any, 0, len(claimed)+len(prefix))
	args = append(args, prefix...)
	for i, row := range claimed {
		placeholders[i] = "?"
		args = append(args, row.UCGID)
	}
	return fmt.Sprintf(format, strings.Join(placeholders, ",")), args
}

// AbandonUsageOutboxOnStartup makes the product's loss boundary explicit. A
// restarted process no longer possesses the raw API key needed to attribute an
// Exchange, so it neither restores nor backcharges unfinished Usage.
func (s *Store) AbandonUsageOutboxOnStartup(ctx context.Context) (int64, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE gateway_usage_outbox
		SET status='abandoned',last_error='process restarted before acknowledgement',updated_at=?,abandoned_at=?
		WHERE status IN ('pending','sending')`, timetext.Format(s.now()), timetext.Format(s.now()))
	if err != nil {
		return 0, fmt.Errorf("abandon unfinished usage outbox: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read abandoned usage count: %w", err)
	}
	return changed, nil
}
