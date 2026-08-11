package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/idy/gizway/internal/timetext"
)

// ConsumeRateLimit increments one shared fixed-window counter. The conditional
// PostgreSQL UPSERT is atomic, so multiple API processes
// cannot each grant the full allowance independently. scopeKey contains only a
// stable principal/key hash or non-secret ID, never a bearer credential.
func (s *Store) ConsumeRateLimit(ctx context.Context, scopeKey, action string, limit int64, window time.Duration, at time.Time) error {
	if scopeKey == "" || action == "" || limit <= 0 || window <= 0 {
		return errors.New("invalid rate limit configuration")
	}
	windowNanos := window.Nanoseconds()
	windowStart := time.Unix(0, at.UTC().UnixNano()/windowNanos*windowNanos).UTC()
	formattedStart := timetext.Format(windowStart)
	formattedAt := timetext.Format(at)
	result, err := s.db.ExecContext(ctx, `INSERT INTO request_rate_limits(scope_key,action,window_started_at,request_count,updated_at)
		VALUES (?,?,?,1,?)
		ON CONFLICT(scope_key,action,window_started_at) DO UPDATE
		SET request_count=request_rate_limits.request_count+1,updated_at=excluded.updated_at
		WHERE request_rate_limits.request_count < ?`, scopeKey, action, formattedStart, formattedAt, limit)
	if err != nil {
		return fmt.Errorf("consume rate limit: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read rate limit result: %w", err)
	}
	if rows != 1 {
		return ErrRateLimited
	}
	// Keep storage bounded without a dedicated worker. This best-effort prune
	// never affects the current decision and is safe under concurrent callers.
	cutoff := timetext.Format(windowStart.Add(-24 * time.Hour))
	_, _ = s.db.ExecContext(ctx, `DELETE FROM request_rate_limits WHERE window_started_at < ?`, cutoff)
	return nil
}
