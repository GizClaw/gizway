package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/idy/gizway/internal/timetext"
)

// APICommandResponse is the exact HTTP result persisted with a generic
// mutation command. Rich domain commands keep their dedicated tables; this
// journal closes the remaining catalog/admin mutation gaps.
type APICommandResponse struct {
	StatusCode  int
	ContentType string
	Body        []byte
}

type apiCommandExecution struct {
	response APICommandResponse
	replayed bool
}

// ExecuteAPICommand runs command lookup, every Store mutation performed by
// execute, audit writes, and response persistence inside one SERIALIZABLE
// transaction. The context transparently routes existing Store queries and
// nested transactions through the outer command transaction and savepoints.
// Therefore a process can expose either the complete mutation+response or
// neither of them; there is no business-commit/journal-crash gap.
func (s *Store) ExecuteAPICommand(ctx context.Context, credentialHash []byte, operation, key string, payloadHash []byte, execute func(context.Context) APICommandResponse) (APICommandResponse, bool, error) {
	result, err := retrySerializable(ctx, func() (apiCommandExecution, error) {
		tx, err := s.db.BeginTxx(ctx, nil)
		if err != nil {
			return apiCommandExecution{}, err
		}
		defer tx.Rollback()
		nowTime := s.now().UTC()
		now := timetext.Format(nowTime)
		// Retention is part of command semantics, not an unbounded archive.
		// Opportunistic pruning keeps PostgreSQL test and production databases
		// bounded without requiring a successful prior worker tick.
		if _, err := tx.ExecContext(ctx, `DELETE FROM api_idempotency_commands WHERE expires_at<=?`, now); err != nil {
			return apiCommandExecution{}, err
		}
		var row struct {
			PayloadHash []byte  `db:"payload_hash"`
			Status      string  `db:"status"`
			Code        *int    `db:"response_status"`
			ContentType *string `db:"response_content_type"`
			Body        []byte  `db:"response_body"`
		}
		err = tx.GetContext(ctx, &row, `SELECT payload_hash,status,response_status,response_content_type,response_body FROM api_idempotency_commands WHERE credential_hash=? AND operation=? AND idempotency_key=?`, credentialHash, operation, key)
		if err == nil {
			if !bytes.Equal(row.PayloadHash, payloadHash) {
				return apiCommandExecution{}, ErrIdempotencyConflict
			}
			if row.Status != "completed" || row.Code == nil {
				return apiCommandExecution{}, ErrCommandInProgress
			}
			contentType := "application/json"
			if row.ContentType != nil {
				contentType = *row.ContentType
			}
			body, err := s.unprotectAPIResponse(row.Body)
			if err != nil {
				return apiCommandExecution{}, err
			}
			return apiCommandExecution{response: APICommandResponse{StatusCode: *row.Code, ContentType: contentType, Body: body}, replayed: true}, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return apiCommandExecution{}, err
		}
		retention := 24 * time.Hour
		if strings.Contains(operation, "/auth/login") || strings.Contains(operation, "/auth/refresh") {
			retention = 8 * time.Hour
		}
		expiresAt := timetext.Format(nowTime.Add(retention))
		if _, err := tx.ExecContext(ctx, `INSERT INTO api_idempotency_commands(id,credential_hash,operation,idempotency_key,payload_hash,status,created_at,expires_at) VALUES (?,?,?,?,?,'started',?,?)`, uuid.NewString(), credentialHash, operation, key, payloadHash, now, expiresAt); err != nil {
			return apiCommandExecution{}, err
		}

		collector := &commandRetryCollector{}
		commandContext := withCommandRetryCollector(withCommandTransaction(ctx, tx.Tx), collector)
		response := execute(commandContext)
		if retryFailure := collector.failure(); retryFailure != nil {
			// The HTTP handler may already have translated the database error to a
			// generic 500. Preserve the original SQLSTATE here so retrySerializable
			// can rebuild the entire handler in a fresh transaction snapshot.
			return apiCommandExecution{}, retryFailure
		}
		if response.StatusCode >= 500 {
			// Internal failures are retryable and must not commit partial business
			// state or a terminal replay record.
			return apiCommandExecution{response: response}, nil
		}
		protectedBody, err := s.protectAPIResponse(response.Body)
		if err != nil {
			return apiCommandExecution{}, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE api_idempotency_commands SET status='completed',response_status=?,response_content_type=?,response_body=?,completed_at=? WHERE credential_hash=? AND operation=? AND idempotency_key=? AND status='started'`, response.StatusCode, response.ContentType, protectedBody, now, credentialHash, operation, key); err != nil {
			return apiCommandExecution{}, err
		}
		if err := tx.Commit(); err != nil {
			return apiCommandExecution{}, err
		}
		return apiCommandExecution{response: response}, nil
	})
	return result.response, result.replayed, err
}

// Generic responses can contain one-time credentials (for example an Admin
// login token). Encrypting the whole replay body keeps the atomic command
// contract without persisting those credentials in plaintext.
func (s *Store) protectAPIResponse(body []byte) ([]byte, error) {
	if s.secrets == nil {
		return append([]byte(nil), body...), nil
	}
	protected, err := s.secrets.encrypt(base64.RawStdEncoding.EncodeToString(body))
	return []byte(protected), err
}

func (s *Store) unprotectAPIResponse(body []byte) ([]byte, error) {
	if !bytes.HasPrefix(body, []byte(encryptedSecretPrefix)) {
		return append([]byte(nil), body...), nil
	}
	if s.secrets == nil {
		return nil, errors.New("encrypted API command response requires the process key")
	}
	plain, err := s.secrets.decrypt(string(body))
	if err != nil {
		return nil, err
	}
	decoded, err := base64.RawStdEncoding.DecodeString(plain)
	if err != nil {
		return nil, errors.New("stored API command response is invalid")
	}
	return decoded, nil
}
