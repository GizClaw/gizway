package store

import (
	"context"
	"fmt"

	"github.com/idy/gizway/internal/timetext"
)

// ReadinessChecks are deliberately coarse operational facts. They contain no
// customer identity, quota state, Usage completeness claim, or node liveness
// flag; deployment health and certificate authorization remain separate.
func (s *Store) GizPayReadinessChecks(ctx context.Context, internal bool) (map[string]string, error) {
	checks := map[string]string{"database": "ready", "schema": "ready"}
	var administrators, systemLedgers int
	if err := s.db.GetContext(ctx, &administrators, `SELECT COUNT(*) FROM administrators WHERE status='active'`); err != nil {
		return nil, fmt.Errorf("check GizPay administrators: %w", err)
	}
	if administrators == 0 {
		checks["bootstrap_admin"] = "pending"
	} else {
		checks["bootstrap_admin"] = "ready"
	}
	if err := s.db.GetContext(ctx, &systemLedgers, `SELECT COUNT(*) FROM ledger_accounts
		WHERE code IN ('SYSTEM:CREDIT_LIABILITY','SYSTEM:PLATFORM_FEE_REVENUE') AND status='active'`); err != nil {
		return nil, fmt.Errorf("check GizPay system ledgers: %w", err)
	}
	if systemLedgers == 2 {
		checks["usage_billing"] = "ready"
		checks["quota_calculator"] = "ready"
	} else {
		checks["usage_billing"] = "pending"
		checks["quota_calculator"] = "pending"
	}
	if internal {
		var nodes int
		if err := s.db.GetContext(ctx, &nodes, `SELECT COUNT(DISTINCT n.id)
			FROM gateway_nodes n JOIN gateway_node_certificates c ON c.node_id=n.id
			WHERE c.status='active' AND c.not_before<=? AND c.not_after>?`, timetext.Format(s.now()), timetext.Format(s.now())); err != nil {
			return nil, fmt.Errorf("check GizPay node registry: %w", err)
		}
		if nodes == 0 {
			checks["node_registry"] = "pending"
		} else {
			checks["node_registry"] = "ready"
		}
	}
	return checks, nil
}

func (s *Store) GizWayReadinessChecks(ctx context.Context) (map[string]string, error) {
	checks := map[string]string{"database": "ready", "schema": "ready", "usage_outbox": "ready"}
	var catalog, publications int
	if err := s.db.GetContext(ctx, &catalog, `SELECT COUNT(*)
		FROM model_variants v JOIN models m ON m.id=v.model_id
		JOIN provider_endpoints e ON e.id=v.provider_endpoint_id
		JOIN providers p ON p.id=e.provider_id
		WHERE v.status='active' AND m.status='active' AND e.status='active' AND p.status='active'`); err != nil {
		return nil, fmt.Errorf("check regional Catalog: %w", err)
	}
	if catalog == 0 {
		checks["catalog"] = "pending"
		checks["provider"] = "pending"
	} else {
		checks["catalog"] = "ready"
		checks["provider"] = "ready"
	}
	if err := s.db.GetContext(ctx, &publications, `SELECT COUNT(*) FROM rate_publications
		WHERE status='active' AND gizpay_publication_id IS NOT NULL AND effective_at<=?`, timetext.Format(s.now())); err != nil {
		return nil, fmt.Errorf("check regional rate publication: %w", err)
	}
	if publications == 0 {
		checks["rate_publication"] = "pending"
	} else {
		checks["rate_publication"] = "ready"
	}
	return checks, nil
}
