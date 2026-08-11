package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"

	"golang.org/x/crypto/bcrypt"
)

// LoginUser verifies a real bcrypt credential and issues a database-backed
// bearer session. Browser clients use the Authorization header rather than a
// cookie, so this contract is not ambient-authority and requires no CSRF token.
func (s *Store) LoginUser(ctx context.Context, email, password, sessionID string, secretHash []byte, createdAt, expiresAt string) (User, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	var row struct {
		User
		PasswordHash string `db:"password_hash"`
	}
	err = tx.GetContext(ctx, &row, `SELECT id,email,display_name,status,created_at,password_hash FROM users WHERE LOWER(email)=LOWER(?) AND status='active'`, email)
	if errors.Is(err, sql.ErrNoRows) || err == nil && bcrypt.CompareHashAndPassword([]byte(row.PasswordHash), []byte(password)) != nil {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO user_sessions(id,user_id,secret_hash,status,expires_at,created_at) VALUES (?,?,?,'active',?,?)`, sessionID, row.ID, secretHash, expiresAt, createdAt); err != nil {
		return User{}, err
	}
	if err := recordAudit(ctx, tx, "user", row.ID, "user_session.issued", "user_session", sessionID, "password authentication succeeded", "{}", createdAt); err != nil {
		return User{}, err
	}
	if err := tx.Commit(); err != nil {
		return User{}, err
	}
	return row.User, nil
}

func (s *Store) RefreshUserSession(ctx context.Context, oldSecret, newSessionID string, newSecretHash []byte, at, expiresAt string) (string, error) {
	return retrySerializable(ctx, func() (string, error) {
		return s.refreshUserSession(ctx, oldSecret, newSessionID, newSecretHash, at, expiresAt)
	})
}

func (s *Store) refreshUserSession(ctx context.Context, oldSecret, newSessionID string, newSecretHash []byte, at, expiresAt string) (string, error) {
	oldHash := sha256.Sum256([]byte(oldSecret))
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var old struct {
		ID     string `db:"id"`
		UserID string `db:"user_id"`
	}
	if err := tx.GetContext(ctx, &old, `SELECT s.id,s.user_id FROM user_sessions s JOIN users u ON u.id=s.user_id WHERE s.secret_hash=? AND s.status='active' AND s.expires_at>? AND u.status='active'`, oldHash[:], at); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", err
	}
	result, err := tx.ExecContext(ctx, `UPDATE user_sessions SET status='revoked',revoked_at=? WHERE id=? AND status='active'`, at, old.ID)
	if err != nil {
		return "", err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return "", err
	}
	if changed != 1 {
		// A refresh token is one-purpose. Concurrent refreshes can both observe
		// the predecessor before either UPDATE acquires the row lock, so the
		// conditional update is the authoritative compare-and-swap.
		return "", ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO user_sessions(id,user_id,secret_hash,status,expires_at,created_at) VALUES (?,?,?,'active',?,?)`, newSessionID, old.UserID, newSecretHash, expiresAt, at); err != nil {
		return "", err
	}
	if err := recordAudit(ctx, tx, "user", old.UserID, "user_session.refreshed", "user_session", newSessionID, "session rotated and predecessor revoked", "{}", at); err != nil {
		return "", err
	}
	return old.UserID, tx.Commit()
}

func (s *Store) RevokeUserSession(ctx context.Context, secret, at string) error {
	return retrySerializableError(ctx, func() error {
		return s.revokeUserSession(ctx, secret, at)
	})
}

func (s *Store) revokeUserSession(ctx context.Context, secret, at string) error {
	hash := sha256.Sum256([]byte(secret))
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var row struct {
		ID     string `db:"id"`
		UserID string `db:"user_id"`
	}
	if err := tx.GetContext(ctx, &row, `SELECT id,user_id FROM user_sessions WHERE secret_hash=? AND status='active'`, hash[:]); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE user_sessions SET status='revoked',revoked_at=? WHERE id=? AND status='active'`, at, row.ID)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return ErrNotFound
	}
	if err := recordAudit(ctx, tx, "user", row.UserID, "user_session.revoked", "user_session", row.ID, "user logout", "{}", at); err != nil {
		return err
	}
	return tx.Commit()
}
