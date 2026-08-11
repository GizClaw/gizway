package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

var (
	ErrCredentialConsumed           = errors.New("realtime client secret is no longer usable")
	ErrInvalidRealtimeProviderState = errors.New("realtime session cannot accept terminal provider usage")
	errRealtimeExpiryLostRace       = errors.New("realtime session changed before expiry")
)

type RealtimeSession struct {
	ID               string  `db:"id" json:"session_id"`
	GatewayRequestID string  `db:"gateway_request_id" json:"-"`
	AccountID        string  `db:"account_id" json:"account_id"`
	APIKeyID         string  `db:"api_key_id" json:"api_key_id"`
	ModelID          string  `db:"model_id" json:"-"`
	VariantID        string  `db:"model_variant_id" json:"-"`
	PublicModel      string  `db:"public_model" json:"model"`
	ProviderModel    string  `db:"provider_model" json:"-"`
	Transport        string  `db:"transport" json:"transport"`
	Status           string  `db:"status" json:"status"`
	ExpiresAt        string  `db:"expires_at" json:"expires_at"`
	DeadlineAt       string  `db:"deadline_at" json:"deadline_at"`
	CreatedAt        string  `db:"created_at" json:"created_at"`
	ConnectedAt      *string `db:"connected_at" json:"connected_at,omitempty"`
	CompletedAt      *string `db:"completed_at" json:"completed_at,omitempty"`
}

const realtimeColumns = `id,gateway_request_id,account_id,api_key_id,model_id,
	model_variant_id,public_model,provider_model,transport,status,expires_at,deadline_at,
	created_at,connected_at,completed_at`

func (s *Store) CreateRealtimeSession(ctx context.Context, session RealtimeSession, idempotencyKey string, payloadHash, secretHash []byte) error {
	if session.DeadlineAt == "" {
		session.DeadlineAt = session.ExpiresAt
	}
	var existing struct {
		Hash   []byte `db:"payload_hash"`
		Status string `db:"status"`
	}
	err := s.db.GetContext(ctx, &existing, `SELECT payload_hash,status FROM realtime_sessions WHERE api_key_id=? AND idempotency_key=?`, session.APIKeyID, idempotencyKey)
	if err == nil {
		if !bytes.Equal(existing.Hash, payloadHash) {
			return ErrIdempotencyConflict
		}
		return ErrCredentialConsumed
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("lookup Realtime session: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO realtime_sessions
		(id,gateway_request_id,account_id,api_key_id,model_id,model_variant_id,
		 public_model,provider_model,client_secret_hash,transport,status,
		 idempotency_key,payload_hash,expires_at,deadline_at,created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,'created',?,?,?,?,?)`, session.ID,
		session.GatewayRequestID, session.AccountID, session.APIKeyID,
		session.ModelID, session.VariantID, session.PublicModel,
		session.ProviderModel, secretHash, session.Transport, idempotencyKey,
		payloadHash, session.ExpiresAt, session.DeadlineAt, session.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert Realtime session: %w", err)
	}
	return nil
}

// CreateRealtimeCommand atomically creates the Gateway command, Credit
// reservation and one-purpose Realtime credential. There is no committed
// intermediate state for a process crash to strand.
func (s *Store) CreateRealtimeCommand(ctx context.Context, command GatewayCommand, session RealtimeSession, secretHash []byte) error {
	return retrySerializableError(ctx, func() error {
		if session.DeadlineAt == "" {
			session.DeadlineAt = session.ExpiresAt
		}
		tx, err := s.db.BeginTxx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		var existingHash []byte
		err = tx.GetContext(ctx, &existingHash, `SELECT payload_hash FROM realtime_sessions WHERE api_key_id=? AND idempotency_key=?`, session.APIKeyID, command.IdempotencyKey)
		if err == nil {
			if !bytes.Equal(existingHash, command.PayloadHash) {
				return ErrIdempotencyConflict
			}
			return ErrCredentialConsumed
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var authorized int
		if err := tx.GetContext(ctx, &authorized, `SELECT COUNT(*) FROM api_keys k JOIN accounts a ON a.id=k.account_id JOIN users u ON u.id=a.owner_user_id WHERE k.id=? AND k.account_id=? AND k.kind='gateway' AND k.status='active' AND (k.expires_at IS NULL OR k.expires_at>?) AND a.status='active' AND u.status='active'`, command.APIKeyID, command.AccountID, command.StartedAt); err != nil {
			return err
		}
		if authorized == 0 {
			return ErrNotFound
		}
		var accountSessions, keySessions int
		if err := tx.GetContext(ctx, &accountSessions, `SELECT COUNT(*) FROM realtime_sessions WHERE account_id=? AND status IN ('created','connected')`, command.AccountID); err != nil {
			return err
		}
		if err := tx.GetContext(ctx, &keySessions, `SELECT COUNT(*) FROM realtime_sessions WHERE api_key_id=? AND status IN ('created','connected')`, command.APIKeyID); err != nil {
			return err
		}
		// Initial launch limits are intentionally conservative and deterministic:
		// one active session per key, two across an account's separate keys.
		if keySessions >= 1 || accountSessions >= 2 {
			return ErrRealtimeSessionLimit
		}
		available, err := availableCredit(ctx, tx, command.AccountID)
		if err != nil {
			return err
		}
		if available < command.ReserveAmount {
			return ErrInsufficientBalance
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO gateway_requests
			(id,account_id,api_key_id,model_id,model_variant_id,operation,idempotency_key,payload_hash,protocol,status,started_at)
			VALUES (?,?,?,?,?,?,?,?,?,'started',?)`, command.ID, command.AccountID, command.APIKeyID, command.ModelID, command.VariantID, command.Operation, command.IdempotencyKey, command.PayloadHash, command.Protocol, command.StartedAt); err != nil {
			return err
		}
		if command.ReserveAmount > 0 {
			if _, err := tx.ExecContext(ctx, `INSERT INTO credit_reservations
				(id,account_id,api_key_id,amount_microcredits,status,idempotency_key,created_at)
				VALUES (?,?,?,?,'active',?,?)`, uuid.NewString(), command.AccountID, command.APIKeyID, command.ReserveAmount, command.ID, command.StartedAt); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO realtime_sessions
			(id,gateway_request_id,account_id,api_key_id,model_id,model_variant_id,public_model,provider_model,client_secret_hash,transport,status,idempotency_key,payload_hash,expires_at,deadline_at,created_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,'created',?,?,?,?,?)`, session.ID, session.GatewayRequestID, session.AccountID, session.APIKeyID, session.ModelID, session.VariantID, session.PublicModel, session.ProviderModel, secretHash, session.Transport, command.IdempotencyKey, command.PayloadHash, session.ExpiresAt, session.DeadlineAt, session.CreatedAt); err != nil {
			return err
		}
		if err := recordAudit(ctx, tx, "api_key", command.APIKeyID, "realtime.created", "realtime_session", session.ID, "one-purpose Realtime credential issued", `{"transport":"`+session.Transport+`"}`, session.CreatedAt); err != nil {
			return err
		}
		return tx.Commit()
	})
}

// ConnectRealtimeSession consumes a one-purpose secret atomically. A second
// connection, a different transport, or an expired secret cannot reconnect.
func (s *Store) ConnectRealtimeSession(ctx context.Context, secretHash []byte, transport, at, deadlineAt string) (RealtimeSession, error) {
	return retrySerializable(ctx, func() (RealtimeSession, error) {
		return s.connectRealtimeSession(ctx, secretHash, transport, at, deadlineAt)
	})
}

func (s *Store) connectRealtimeSession(ctx context.Context, secretHash []byte, transport, at, deadlineAt string) (RealtimeSession, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return RealtimeSession{}, err
	}
	defer tx.Rollback()
	var session RealtimeSession
	err = tx.GetContext(ctx, &session, `SELECT `+realtimeColumns+` FROM realtime_sessions WHERE client_secret_hash=?`, secretHash)
	if errors.Is(err, sql.ErrNoRows) {
		return RealtimeSession{}, ErrNotFound
	}
	if err != nil {
		return RealtimeSession{}, err
	}
	if session.Status != "created" || session.Transport != transport || session.ExpiresAt <= at {
		if session.Status == "created" && session.ExpiresAt <= at {
			if err := expireRealtimeSession(ctx, tx, session.ID, session.GatewayRequestID, "created", at); err != nil {
				return RealtimeSession{}, err
			}
			if err := tx.Commit(); err != nil {
				return RealtimeSession{}, err
			}
		}
		return RealtimeSession{}, ErrCredentialConsumed
	}
	result, err := tx.ExecContext(ctx, `UPDATE realtime_sessions SET status='connected',connected_at=?,deadline_at=? WHERE id=? AND status='created'`, at, deadlineAt, session.ID)
	if err != nil {
		return RealtimeSession{}, err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return RealtimeSession{}, ErrCredentialConsumed
	}
	if err := recordAudit(ctx, tx, "api_key", session.APIKeyID, "realtime.connected", "realtime_session", session.ID, "one-purpose Realtime credential consumed", `{"transport":"`+transport+`"}`, at); err != nil {
		return RealtimeSession{}, err
	}
	if err := tx.Commit(); err != nil {
		return RealtimeSession{}, err
	}
	session.Status = "connected"
	session.ConnectedAt = &at
	session.DeadlineAt = deadlineAt
	return session, nil
}

// ExpireRealtimeSessions releases reservations for credentials that were never
// connected. The durable worker calls this even when no client retries the
// expired secret.
func (s *Store) ExpireRealtimeSessions(ctx context.Context, at string, limit int) (int, error) {
	if limit <= 0 {
		return 0, errors.New("realtime expiry batch limit must be positive")
	}
	var ids []struct {
		SessionID string `db:"session_id"`
		RequestID string `db:"gateway_request_id"`
		Status    string `db:"status"`
	}
	if err := s.db.SelectContext(ctx, &ids, `SELECT id AS session_id,gateway_request_id,status FROM realtime_sessions WHERE (status='created' AND expires_at<=?) OR (status='connected' AND deadline_at<=?) ORDER BY CASE WHEN status='created' THEN expires_at ELSE deadline_at END,id LIMIT ?`, at, at, limit); err != nil {
		return 0, err
	}
	expired := 0
	for _, row := range ids {
		err := retrySerializableError(ctx, func() error {
			tx, err := s.db.BeginTxx(ctx, nil)
			if err != nil {
				return err
			}
			defer tx.Rollback()
			if err := expireRealtimeSession(ctx, tx, row.SessionID, row.RequestID, row.Status, at); err != nil {
				return err
			}
			return tx.Commit()
		})
		if err != nil {
			if errors.Is(err, errRealtimeExpiryLostRace) {
				continue
			}
			return expired, err
		}
		expired++
	}
	return expired, nil
}

func expireRealtimeSession(ctx context.Context, tx *boundTx, sessionID, requestID, expectedStatus, at string) error {
	result, err := tx.ExecContext(ctx, `UPDATE realtime_sessions SET status='expired',completed_at=? WHERE id=? AND status=?`, at, sessionID, expectedStatus)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		// A connector may have changed created -> connected after the sweep
		// selected this row. That is a successful competing transition, not
		// permission to release its active economic reservation.
		return errRealtimeExpiryLostRace
	}
	errorCode := "realtime_expired"
	reason := "client secret expired before connection"
	if expectedStatus == "connected" {
		errorCode = "session_timeout"
		reason = "connected Realtime session exceeded its durable deadline"
	}
	if _, err := tx.ExecContext(ctx, `UPDATE gateway_requests SET status='failed',error_code=?,completed_at=? WHERE id=? AND status='started'`, errorCode, at, requestID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE credit_reservations SET status='released',completed_at=? WHERE idempotency_key=? AND status='active'`, at, requestID); err != nil {
		return err
	}
	return recordAudit(ctx, tx, "system", "realtime-expiry-worker", "realtime.expired", "realtime_session", sessionID, reason, "{}", at)
}

func (s *Store) GetRealtimeSession(ctx context.Context, id string) (RealtimeSession, error) {
	var session RealtimeSession
	err := s.db.GetContext(ctx, &session, `SELECT `+realtimeColumns+` FROM realtime_sessions WHERE id=?`, id)
	if errors.Is(err, sql.ErrNoRows) {
		return session, ErrNotFound
	}
	return session, err
}

type RealtimeProviderUsageEvent struct {
	EventID           string `db:"event_id"`
	SessionID         string `db:"session_id"`
	InputTokens       int64  `db:"input_tokens"`
	OutputTokens      int64  `db:"output_tokens"`
	CachedInputTokens int64  `db:"cached_input_tokens"`
	InputAudioTokens  int64  `db:"input_audio_tokens"`
	OutputAudioTokens int64  `db:"output_audio_tokens"`
}

// RecordRealtimeProviderEvent journals a signed terminal usage event before
// settlement. Exact duplicates may retry; reusing an event id with a changed
// body or recording a second terminal event for the same session is an
// idempotency conflict. The database UNIQUE(session_id) constraint closes the
// race between concurrent callback attempts.
func (s *Store) RecordRealtimeProviderEvent(ctx context.Context, eventID, sessionID string, payloadHash []byte, inputTokens, outputTokens, cachedInputTokens, inputAudioTokens, outputAudioTokens int64, receivedAt string) (bool, error) {
	return retrySerializable(ctx, func() (bool, error) {
		return s.recordRealtimeProviderEvent(ctx, eventID, sessionID, payloadHash, inputTokens, outputTokens, cachedInputTokens, inputAudioTokens, outputAudioTokens, receivedAt)
	})
}

func (s *Store) recordRealtimeProviderEvent(ctx context.Context, eventID, sessionID string, payloadHash []byte, inputTokens, outputTokens, cachedInputTokens, inputAudioTokens, outputAudioTokens int64, receivedAt string) (bool, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var existing struct {
		SessionID string `db:"session_id"`
		Hash      []byte `db:"payload_hash"`
	}
	err = tx.GetContext(ctx, &existing, `SELECT session_id,payload_hash FROM realtime_provider_events WHERE event_id=?`, eventID)
	if err == nil {
		if existing.SessionID != sessionID || !bytes.Equal(existing.Hash, payloadHash) {
			return false, ErrIdempotencyConflict
		}
		return true, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("lookup Realtime provider event: %w", err)
	}
	var existingEventID string
	err = tx.GetContext(ctx, &existingEventID, `SELECT event_id FROM realtime_provider_events WHERE session_id=?`, sessionID)
	if err == nil {
		return false, ErrIdempotencyConflict
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("lookup Realtime session event: %w", err)
	}
	var transport, status, deadlineAt, requestID string
	if err := scanCommandRow(ctx, tx.QueryRowxContext(ctx, `SELECT transport,status,deadline_at,gateway_request_id FROM realtime_sessions WHERE id=?`, sessionID), &transport, &status, &deadlineAt, &requestID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, ErrInvalidRealtimeProviderState
		}
		return false, err
	}
	if transport != "webrtc" || status != "connected" {
		return false, ErrInvalidRealtimeProviderState
	}
	if deadlineAt <= receivedAt {
		// The callback and periodic expiry worker race inside the same serializable
		// state transition. A provider cannot turn a late event into a customer
		// charge merely because the one-second sweep has not selected the row yet.
		if err := expireRealtimeSession(ctx, tx, sessionID, requestID, "connected", receivedAt); err != nil {
			return false, err
		}
		if err := tx.Commit(); err != nil {
			return false, err
		}
		return false, ErrInvalidRealtimeProviderState
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO realtime_provider_events(event_id,session_id,payload_hash,input_tokens,output_tokens,cached_input_tokens,input_audio_tokens,output_audio_tokens,status,received_at) VALUES (?,?,?,?,?,?,?,?,'received',?)`, eventID, sessionID, payloadHash, inputTokens, outputTokens, cachedInputTokens, inputAudioTokens, outputAudioTokens, receivedAt)
	if err != nil {
		// A concurrent callback can pass both lookups. Convert either unique-key
		// violation into the same stable API conflict instead of exposing a
		// driver-specific PostgreSQL error.
		message := strings.ToLower(err.Error())
		if strings.Contains(message, "unique") || strings.Contains(message, "duplicate key") {
			return false, ErrIdempotencyConflict
		}
		return false, fmt.Errorf("record Realtime provider event: %w", err)
	}
	if err := recordAudit(ctx, tx, "system", "realtime-provider", "realtime.usage_received", "realtime_provider_event", eventID, "authenticated terminal usage received", "{}", receivedAt); err != nil {
		return false, err
	}
	return false, tx.Commit()
}

// RecoverableRealtimeProviderEvents returns durable, authenticated terminal
// usage that has not yet completed settlement. The signed callback owns the
// insert; this reader lets any process finish the economic transition later.
func (s *Store) RecoverableRealtimeProviderEvents(ctx context.Context, limit int) ([]RealtimeProviderUsageEvent, error) {
	if limit <= 0 {
		return nil, errors.New("realtime provider event batch limit must be positive")
	}
	var events []RealtimeProviderUsageEvent
	err := s.db.SelectContext(ctx, &events, `SELECT event_id,session_id,input_tokens,output_tokens,cached_input_tokens,input_audio_tokens,output_audio_tokens FROM realtime_provider_events WHERE status='received' ORDER BY received_at,event_id LIMIT ?`, limit)
	return events, err
}

func (s *Store) MarkRealtimeProviderEventProcessed(ctx context.Context, eventID, processedAt string) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE realtime_provider_events SET status='processed',processed_at=? WHERE event_id=? AND status='received'`, processedAt, eventID)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		// Exact callback replay is a no-op after the first durable settlement;
		// it must not manufacture a second "processed" audit event.
		var status string
		if err := tx.GetContext(ctx, &status, `SELECT status FROM realtime_provider_events WHERE event_id=?`, eventID); err != nil {
			return err
		}
		if status == "processed" {
			return tx.Commit()
		}
		return ErrIdempotencyConflict
	}
	if err := recordAudit(ctx, tx, "system", "realtime-settlement-worker", "realtime.usage_processed", "realtime_provider_event", eventID, "terminal usage settlement completed", "{}", processedAt); err != nil {
		return err
	}
	return tx.Commit()
}
