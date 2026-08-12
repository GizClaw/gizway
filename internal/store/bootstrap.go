package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// BootstrapAdministrator creates the one credential required to enter the
// normal Admin API and the center-owned ledger accounts required to post
// received Usage. It is intentionally narrower than normal administrator
// creation: only an empty GizPay database or an exact retry is accepted.
func (s *Store) BootstrapAdministrator(ctx context.Context, email, displayName, password, at string) (Administrator, bool, error) {
	return s.bootstrapAdministrator(ctx, email, displayName, password, at, true, "initial GizPay bootstrap")
}

// BootstrapRegionalAdministrator creates the first operator identity in one
// regional GizWay database. CN and Global call this independently: neither
// region copies its Catalog or its operator authorization to the other.
func (s *Store) BootstrapRegionalAdministrator(ctx context.Context, email, displayName, password, at string) (Administrator, bool, error) {
	return s.bootstrapAdministrator(ctx, email, displayName, password, at, false, "initial GizWay bootstrap")
}

func (s *Store) bootstrapAdministrator(ctx context.Context, email, displayName, password, at string, ensureLedgers bool, auditReason string) (Administrator, bool, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	displayName = strings.TrimSpace(displayName)
	if email == "" || displayName == "" || password == "" {
		return Administrator{}, false, errors.New("bootstrap email, display name, and password are required")
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return Administrator{}, false, fmt.Errorf("hash bootstrap administrator password: %w", err)
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return Administrator{}, false, fmt.Errorf("begin administrator bootstrap: %w", err)
	}
	defer tx.Rollback()
	// Concurrent automation invocations serialize before deciding whether this
	// is the initial creation, an exact retry, or a conflicting bootstrap.
	if _, err := tx.ExecContext(ctx, `LOCK TABLE administrators IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return Administrator{}, false, fmt.Errorf("lock administrator bootstrap: %w", err)
	}
	var existing Administrator
	err = tx.GetContext(ctx, &existing, `SELECT `+administratorColumns+` FROM administrators LIMIT 1`)
	if err == nil {
		if !strings.EqualFold(existing.Email, email) || existing.DisplayName != displayName {
			return Administrator{}, false, ErrIdempotencyConflict
		}
		var existingHash string
		if err := tx.GetContext(ctx, &existingHash, `SELECT password_hash FROM administrators WHERE id=?`, existing.ID); err != nil {
			return Administrator{}, false, fmt.Errorf("read bootstrap password: %w", err)
		}
		if bcrypt.CompareHashAndPassword([]byte(existingHash), []byte(password)) != nil {
			return Administrator{}, false, ErrIdempotencyConflict
		}
		if ensureLedgers {
			if err := ensureBootstrapSystemLedgers(ctx, tx, at); err != nil {
				return Administrator{}, false, err
			}
		}
		if err := tx.Commit(); err != nil {
			return Administrator{}, false, fmt.Errorf("commit administrator bootstrap replay: %w", err)
		}
		return existing, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Administrator{}, false, fmt.Errorf("read bootstrap administrator: %w", err)
	}

	administrator := Administrator{
		ID: uuid.NewString(), Email: email, DisplayName: displayName,
		Status: "active", CreatedAt: at, UpdatedAt: at,
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO administrators
		(id,email,display_name,password_hash,status,created_at,updated_at)
		VALUES (?,?,?,?,'active',?,?)`, administrator.ID, administrator.Email,
		administrator.DisplayName, string(passwordHash), at, at); err != nil {
		return Administrator{}, false, fmt.Errorf("insert bootstrap administrator: %w", err)
	}
	if ensureLedgers {
		if err := ensureBootstrapSystemLedgers(ctx, tx, at); err != nil {
			return Administrator{}, false, err
		}
	}
	if err := insertAudit(ctx, tx, administrator.ID, "administrator.bootstrapped", "administrator", administrator.ID, auditReason, at); err != nil {
		return Administrator{}, false, fmt.Errorf("audit administrator bootstrap: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Administrator{}, false, fmt.Errorf("commit administrator bootstrap: %w", err)
	}
	return administrator, false, nil
}

func ensureBootstrapSystemLedgers(ctx context.Context, tx *boundTx, at string) error {
	ledgers := []struct {
		code, kind, normal string
	}{
		{code: "SYSTEM:CREDIT_LIABILITY", kind: "system_credit_liability", normal: "debit"},
		{code: "SYSTEM:PLATFORM_FEE_REVENUE", kind: "platform_fee_revenue", normal: "credit"},
	}
	for _, ledger := range ledgers {
		if _, err := tx.ExecContext(ctx, `INSERT INTO ledger_accounts
			(id,owner_account_id,code,kind,asset_code,normal_balance,status,created_at,updated_at)
			VALUES (?,NULL,?,?, 'GIZ_CREDIT',?,'active',?,?)
			ON CONFLICT (code) DO NOTHING`, uuid.NewString(), ledger.code, ledger.kind, ledger.normal, at, at); err != nil {
			return fmt.Errorf("ensure bootstrap ledger %s: %w", ledger.code, err)
		}
	}
	return nil
}
