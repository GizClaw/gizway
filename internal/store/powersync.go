package store

import (
	"context"
	"fmt"
)

// PowerSyncProjectionCounts is the executable ownership companion for the
// external sync-rule configuration. It never trusts a client account claim:
// the signed JWT subject must own one active account before any account-keyed
// projection is observable.
func (s *Store) PowerSyncProjectionCounts(ctx context.Context, userID, accountID string) (map[string]int, error) {
	var owned int
	if err := s.db.GetContext(ctx, &owned, `SELECT COUNT(*) FROM accounts WHERE id=? AND owner_user_id=? AND status='active'`, accountID, userID); err != nil {
		return nil, fmt.Errorf("check PowerSync account ownership: %w", err)
	}
	if owned != 1 {
		return nil, ErrNotFound
	}
	counts := make(map[string]int, 5)
	for _, view := range []string{
		"powersync_account_balances", "powersync_gateway_usage", "powersync_credit_transfers",
		"powersync_payments", "powersync_merchant_orders",
	} {
		var count int
		if err := s.db.GetContext(ctx, &count, `SELECT COUNT(*) FROM `+view+` WHERE account_id=?`, accountID); err != nil {
			return nil, fmt.Errorf("query %s: %w", view, err)
		}
		counts[view] = count
	}
	return counts, nil
}
