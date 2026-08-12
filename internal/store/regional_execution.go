package store

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/idy/gizway/internal/service/quotaexchange"
	"github.com/idy/gizway/internal/timetext"
)

// BeginRegionalExecution records only routing and local commitment facts. No
// API key, digest, Account, or central financial identity is accepted by this
// function, which makes accidental identity persistence impossible at compile
// time as well as through SQL constraints.
func (s *Store) BeginRegionalExecution(ctx context.Context, id, publicModel, variantID, publicationID, protocol, streamMode string, estimated int64, startedAt string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO gateway_executions
		(id,public_model,model_variant_id,protocol,stream_mode,rate_publication_id,status,
		 estimated_microcredits,started_at)
		VALUES (?,?,?,?,?,?,'running',?,?)`, id, publicModel, variantID, protocol, streamMode,
		publicationID, estimated, startedAt)
	if err != nil {
		return fmt.Errorf("begin regional execution: %w", err)
	}
	return nil
}

// CompleteRegionalExecution atomically closes the local execution and creates
// its identity-free Usage/metrics outbox record. GizPay independently prices
// the same metrics; gatewayCalculated is retained only for local diagnostics.
func (s *Store) CompleteRegionalExecution(ctx context.Context, processEpoch, runtimeKeyToken, providerRequestID string, usage quotaexchange.UsageRecord, metrics []GatewayMetric, gatewayCalculated int64) error {
	payload, err := json.Marshal(usage)
	if err != nil {
		return fmt.Errorf("marshal regional Usage: %w", err)
	}
	payloadHash, err := canonicalUsageHash(usage)
	if err != nil {
		return fmt.Errorf("hash regional Usage: %w", err)
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin regional completion: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE gateway_executions
		SET provider_request_id=?,model_variant_id=?,status='completed',
		    actual_microcredits=?,completed_at=?
		WHERE id=? AND status='running'`, providerRequestID, usage.ModelVariantID,
		gatewayCalculated, usage.CompletedAt, usage.OperationID)
	if err != nil {
		return fmt.Errorf("complete regional execution: %w", err)
	}
	if changed, err := result.RowsAffected(); err != nil || changed != 1 {
		return fmt.Errorf("complete regional execution changed %d rows: %w", changed, err)
	}
	now := timetext.Format(s.now())
	if _, err := tx.ExecContext(ctx, `INSERT INTO gateway_usage_outbox
		(ucgid,process_epoch,runtime_key_token,operation_id,public_model,model_variant_id,
		 rate_publication_id,period_started_at,period_ended_at,gateway_calculated_microcredits,
		 canonical_payload_hash,payload,status,next_attempt_at,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?, 'pending',?,?,?)`, usage.UCGID, processEpoch, runtimeKeyToken,
		usage.OperationID, usage.PublicModel, usage.ModelVariantID, usage.RatePublicationID,
		usage.StartedAt, usage.CompletedAt, gatewayCalculated, payloadHash, payload, now, now, now); err != nil {
		return fmt.Errorf("insert regional Usage outbox: %w", err)
	}
	metricNames := make([]string, 0, len(metrics))
	metricByName := make(map[string]GatewayMetric, len(metrics))
	for _, metric := range metrics {
		metricNames = append(metricNames, metric.Metric)
		metricByName[metric.Metric] = metric
	}
	sort.Strings(metricNames)
	for _, metricName := range metricNames {
		metric := metricByName[metricName]
		if _, err := tx.ExecContext(ctx, `INSERT INTO gateway_usage_metrics
			(ucgid,metric,quantity,unit_size,rate_version_id) VALUES (?,?,?,?,?)`, usage.UCGID,
			metricName, metric.Quantity, metric.Price.UnitSize, metric.Price.ID); err != nil {
			return fmt.Errorf("insert regional Usage metric: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit regional completion: %w", err)
	}
	return nil
}

func (s *Store) FailRegionalExecution(ctx context.Context, id string, completedAt string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE gateway_executions
		SET status='failed',actual_microcredits=0,completed_at=?
		WHERE id=? AND status='running'`, completedAt, id)
	if err != nil {
		return fmt.Errorf("fail regional execution: %w", err)
	}
	if changed, err := result.RowsAffected(); err != nil || changed != 1 {
		return fmt.Errorf("fail regional execution changed %d rows: %w", changed, err)
	}
	return nil
}
