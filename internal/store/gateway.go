package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/idy/gizway/internal/timetext"
)

// GatewayPrincipal is the exact authenticated Gateway key attribution.
type GatewayPrincipal struct {
	UserID    string
	AccountID string
	APIKeyID  string
	Scopes    JSON
}

// GatewayPrice is one active immutable metric price.
type GatewayPrice struct {
	ID             string `db:"id"`
	Metric         string `db:"metric"`
	UnitSize       int64  `db:"unit_size"`
	BasePrice      int64  `db:"base_customer_price_microcredits"`
	EffectivePrice int64  `db:"customer_price_microcredits"`
	DiscountBPS    int    `db:"discount_bps"`
}

// GatewayCandidate is a database-authorized provider execution candidate.
type GatewayCandidate struct {
	ModelID            string
	PublicModel        string
	VariantID          string
	ProviderModel      string
	ProviderEndpoint   string
	ProviderCredential string
	Capabilities       JSON
	ContextWindow      int64
	MaxOutputTokens    int64
	Prices             map[string]GatewayPrice
}

// ProviderExecutionTarget is the secret-bearing adapter input. It is never
// serialized by an API response or persisted in a request/usage record.
type ProviderExecutionTarget struct {
	Endpoint   string
	Credential string
	Model      string
	// RouteKey is the database variant identity used only inside the provider
	// adapter. Bifrost exposes it as a private custom-provider routing value so
	// settlement can distinguish two candidates that use the same wire model
	// name on different endpoints. Public response sanitization removes it.
	RouteKey string
}

func (candidate GatewayCandidate) ExecutionTarget() ProviderExecutionTarget {
	return ProviderExecutionTarget{Endpoint: candidate.ProviderEndpoint, Credential: candidate.ProviderCredential, Model: candidate.ProviderModel, RouteKey: candidate.VariantID}
}

// GatewayCommand identifies an idempotent provider invocation.
type GatewayCommand struct {
	ID             string
	AccountID      string
	APIKeyID       string
	ModelID        string
	VariantID      string
	Operation      string
	IdempotencyKey string
	PayloadHash    []byte
	ReserveAmount  int64
	Protocol       string
	StartedAt      string
	// ExecutionLeaseUntil prevents concurrent retries from invoking the
	// provider twice. A dead process's expired lease can be reclaimed with the
	// same durable provider idempotency identity.
	ExecutionLeaseUntil string
	// ExecutionSnapshot is the serialized, immutable candidate and price plan
	// authorized before the first provider call. Store encrypts it at rest.
	ExecutionSnapshot []byte
	// RecoveryRequest is a secret-free serialized HTTP request envelope. It is
	// encrypted separately so a background worker can reconstruct the exact
	// public protocol command without retaining the caller's bearer secret.
	RecoveryRequest []byte
}

// GatewayBeginResult reports whether provider execution is needed.
type GatewayBeginResult struct {
	RequestID         string
	ReplayJSON        []byte
	ExecutionSnapshot []byte
	Existing          bool
	Resumed           bool
}

// RecoverableGatewayCommand is an expired HTTPS execution lease that has no
// provider-success settlement outbox yet. RecoveryRequest has already been
// decrypted by Store and is safe only for the in-process trusted replay path.
type RecoverableGatewayCommand struct {
	RequestID       string
	Principal       GatewayPrincipal
	Operation       string
	IdempotencyKey  string
	RecoveryRequest []byte
}

// ResumeGatewayCommand checks for an existing idempotent command before the
// live catalog is resolved. This ordering is essential: an expired execution
// lease must resume the exact provider endpoint, credential, fallback order
// and price snapshot from the first attempt, even if Admin changed the catalog
// while the process was unavailable.
func (s *Store) ResumeGatewayCommand(ctx context.Context, apiKeyID, operation, idempotencyKey string, payloadHash []byte, startedAt, leaseUntil string) (GatewayBeginResult, error) {
	return retrySerializable(ctx, func() (GatewayBeginResult, error) {
		tx, err := s.db.BeginTxx(ctx, nil)
		if err != nil {
			return GatewayBeginResult{}, fmt.Errorf("begin Gateway resume: %w", err)
		}
		defer tx.Rollback()
		result, err := s.resumeGatewayCommandTx(ctx, tx, apiKeyID, operation, idempotencyKey, payloadHash, startedAt, leaseUntil)
		if err != nil {
			return GatewayBeginResult{}, err
		}
		if !result.Existing {
			return result, nil
		}
		if err := tx.Commit(); err != nil {
			return GatewayBeginResult{}, err
		}
		return result, nil
	})
}

func (s *Store) resumeGatewayCommandTx(ctx context.Context, tx *boundTx, apiKeyID, operation, idempotencyKey string, payloadHash []byte, startedAt, leaseUntil string) (GatewayBeginResult, error) {
	var existing struct {
		ID                  string  `db:"id"`
		PayloadHash         []byte  `db:"payload_hash"`
		Status              string  `db:"status"`
		ResponseJSON        *string `db:"response_json"`
		ExecutionLeaseUntil *string `db:"execution_lease_until"`
		ExecutionSnapshot   []byte  `db:"execution_snapshot"`
	}
	err := tx.GetContext(ctx, &existing, `SELECT id,payload_hash,status,response_json,execution_lease_until,execution_snapshot
		FROM gateway_requests WHERE api_key_id = ? AND operation = ? AND idempotency_key = ?`, apiKeyID, operation, idempotencyKey)
	if errors.Is(err, sql.ErrNoRows) {
		return GatewayBeginResult{}, nil
	}
	if err != nil {
		return GatewayBeginResult{}, fmt.Errorf("lookup Gateway command: %w", err)
	}
	if !bytes.Equal(existing.PayloadHash, payloadHash) {
		return GatewayBeginResult{}, ErrIdempotencyConflict
	}
	if existing.Status == "succeeded" && existing.ResponseJSON != nil {
		return GatewayBeginResult{RequestID: existing.ID, ReplayJSON: []byte(*existing.ResponseJSON), Existing: true}, nil
	}
	if existing.Status != "started" || existing.ExecutionLeaseUntil == nil || *existing.ExecutionLeaseUntil > startedAt {
		return GatewayBeginResult{}, ErrCommandInProgress
	}
	result, err := tx.ExecContext(ctx, `UPDATE gateway_requests
		SET execution_lease_until=?,execution_attempts=execution_attempts+1
		WHERE id=? AND status='started' AND execution_lease_until<=?`, leaseUntil, existing.ID, startedAt)
	if err != nil {
		return GatewayBeginResult{}, err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return GatewayBeginResult{}, ErrCommandInProgress
	}
	if len(existing.ExecutionSnapshot) == 0 {
		return GatewayBeginResult{}, errors.New("gateway command has no recoverable execution snapshot")
	}
	snapshot, err := s.unprotectAPIResponse(existing.ExecutionSnapshot)
	if err != nil {
		return GatewayBeginResult{}, fmt.Errorf("decrypt Gateway execution snapshot: %w", err)
	}
	return GatewayBeginResult{RequestID: existing.ID, ExecutionSnapshot: snapshot, Existing: true, Resumed: true}, nil
}

// GatewayMetric is measured usage to settle at a snapshotted price.
type GatewayMetric struct {
	Metric   string
	Quantity int64
	Price    GatewayPrice
	Charge   int64
}

// GatewayActiveWorkCounts exposes durable in-flight state for health checks and
// failure-path tests without leaking the underlying sqlx handle.
func (s *Store) GatewayActiveWorkCounts(ctx context.Context) (int, int, error) {
	var reservations, requests int
	if err := s.db.GetContext(ctx, &reservations, `SELECT COUNT(*) FROM credit_reservations WHERE status='active'`); err != nil {
		return 0, 0, err
	}
	if err := s.db.GetContext(ctx, &requests, `SELECT COUNT(*) FROM gateway_requests WHERE status='started'`); err != nil {
		return 0, 0, err
	}
	return reservations, requests, nil
}

// GatewayCommandStatus lets the recovery transport verify that an HTTP 2xx
// actually reached the durable business terminal state (important for SSE,
// where headers may be committed before a later execution error).
func (s *Store) GatewayCommandStatus(ctx context.Context, requestID string) (string, error) {
	var status string
	if err := s.db.GetContext(ctx, &status, `SELECT status FROM gateway_requests WHERE id=?`, requestID); err != nil {
		return "", err
	}
	return status, nil
}

// AuthenticateGatewayKey resolves an active Gateway credential and key ID.
func (s *Store) AuthenticateGatewayKey(ctx context.Context, secretHash []byte, at string) (GatewayPrincipal, error) {
	var result struct {
		UserID    string `db:"user_id"`
		AccountID string `db:"account_id"`
		APIKeyID  string `db:"api_key_id"`
		Scopes    JSON   `db:"scopes"`
	}
	err := s.db.GetContext(ctx, &result, `
		SELECT u.id AS user_id, a.id AS account_id, k.id AS api_key_id, k.scopes
		FROM api_keys k
		JOIN accounts a ON a.id = k.account_id
		JOIN users u ON u.id = a.owner_user_id
		WHERE k.secret_hash = ? AND k.kind = 'gateway' AND k.status = 'active'
		  AND (k.expires_at IS NULL OR k.expires_at > ?)
		  AND a.status = 'active' AND u.status = 'active'
	`, secretHash, at)
	if errors.Is(err, sql.ErrNoRows) {
		return GatewayPrincipal{}, ErrNotFound
	}
	if err != nil {
		return GatewayPrincipal{}, fmt.Errorf("authenticate Gateway key: %w", err)
	}
	return GatewayPrincipal{UserID: result.UserID, AccountID: result.AccountID, APIKeyID: result.APIKeyID, Scopes: result.Scopes}, nil
}

// ResolveGatewayCandidates builds the ordered database-authorized candidate
// set. Bifrost receives this order for provider retry/fallback; Gizway retains
// price and variant identity so final settlement can use the actual winner.
// ResolveGatewayCandidatesForAccount applies the same account entitlement
// policy used by all discovery projections. Deny wins. When an account has at
// least one allow row, only explicitly allowed models remain executable.
func (s *Store) ResolveGatewayCandidatesForAccount(ctx context.Context, accountID, publicModel, operation, at string) ([]GatewayCandidate, error) {
	var rows []struct {
		ModelID          string `db:"model_id"`
		PublicModel      string `db:"public_model"`
		VariantID        string `db:"variant_id"`
		ProviderModel    string `db:"provider_model_name"`
		ProviderEndpoint string `db:"base_url"`
		CredentialRef    string `db:"credential_ref"`
		Capabilities     JSON   `db:"capabilities"`
		ContextWindow    *int64 `db:"context_window"`
		MaxOutputTokens  *int64 `db:"max_output_tokens"`
	}
	err := s.db.SelectContext(ctx, &rows, `
		SELECT m.id AS model_id, m.slug AS public_model, mv.id AS variant_id,
		       mv.provider_model_name, pe.base_url, pe.credential_ref,
		       mv.capabilities,mv.context_window,mv.max_output_tokens
		FROM models m
		JOIN model_variants mv ON mv.model_id = m.id
		JOIN provider_endpoints pe ON pe.id = mv.provider_endpoint_id
		JOIN providers p ON p.id = pe.provider_id
		WHERE m.slug = ? AND m.status = 'active' AND mv.status IN ('active','degraded')
		  AND pe.status = 'active' AND p.status = 'active'
		  AND (? = '' OR (
		    NOT EXISTS (SELECT 1 FROM account_model_entitlements deny_rule
		                WHERE deny_rule.account_id=? AND deny_rule.model_id=m.id AND deny_rule.effect='deny')
		    AND (
		      NOT EXISTS (SELECT 1 FROM account_model_entitlements allow_mode
		                  WHERE allow_mode.account_id=? AND allow_mode.effect='allow')
		      OR EXISTS (SELECT 1 FROM account_model_entitlements allow_rule
		                 WHERE allow_rule.account_id=? AND allow_rule.model_id=m.id AND allow_rule.effect='allow')
		    )
		  ))
		  AND EXISTS (SELECT 1 FROM model_variant_prices price
		              WHERE price.model_variant_id = mv.id AND price.valid_from <= ?
		                AND (price.valid_until IS NULL OR price.valid_until > ?))
		ORDER BY pe.priority, mv.variant_slug
	`, publicModel, accountID, accountID, accountID, accountID, at, at)
	if err != nil {
		return nil, fmt.Errorf("resolve Gateway candidates: %w", err)
	}
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	candidates := make([]GatewayCandidate, 0, len(rows))
	for _, row := range rows {
		var capabilities map[string]bool
		if err := json.Unmarshal(row.Capabilities, &capabilities); err != nil || !capabilities[gatewayCapability(operation)] {
			continue
		}
		if row.ContextWindow == nil || *row.ContextWindow <= 0 || row.MaxOutputTokens == nil || *row.MaxOutputTokens <= 0 {
			continue
		}
		credential := ""
		if s.secrets != nil {
			credential, err = s.secrets.decrypt(row.CredentialRef)
			if err != nil {
				return nil, fmt.Errorf("resolve provider credential: %w", err)
			}
		}
		priceMap, err := s.GatewayPricesForVariant(ctx, row.VariantID, at)
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, GatewayCandidate{ModelID: row.ModelID, PublicModel: row.PublicModel,
			VariantID: row.VariantID, ProviderModel: row.ProviderModel,
			ProviderEndpoint: row.ProviderEndpoint, ProviderCredential: credential,
			Capabilities: row.Capabilities, ContextWindow: *row.ContextWindow,
			MaxOutputTokens: *row.MaxOutputTokens, Prices: priceMap})
	}
	if len(candidates) == 0 {
		return nil, ErrNotFound
	}
	return candidates, nil
}

func gatewayCapability(operation string) string {
	switch operation {
	case "chat.completions":
		return "chat"
	case "anthropic.messages":
		return "messages"
	case "gemini.generateContent", "gemini.streamGenerateContent":
		return "generateContent"
	case "embeddings":
		return "embeddings"
	case "audio.speech":
		return "audio_speech"
	case "audio.transcriptions":
		return "audio_transcriptions"
	case "images.generations":
		return "image_generation"
	case "realtime":
		return "realtime"
	default:
		return operation
	}
}

func (s *Store) ResolveGatewayCandidateForAccount(ctx context.Context, accountID, publicModel, operation, at string) (GatewayCandidate, error) {
	candidates, err := s.ResolveGatewayCandidatesForAccount(ctx, accountID, publicModel, operation, at)
	if err != nil {
		return GatewayCandidate{}, err
	}
	if len(candidates) == 0 {
		return GatewayCandidate{}, ErrNotFound
	}
	return candidates[0], nil
}

// ResolveVariantExecutionTarget reloads the endpoint at Realtime connection
// time. A credential rotation therefore takes effect for new provider
// connections without putting plaintext into realtime_sessions.
func (s *Store) ResolveVariantExecutionTarget(ctx context.Context, variantID string) (ProviderExecutionTarget, error) {
	var row struct {
		Model         string `db:"provider_model_name"`
		Endpoint      string `db:"base_url"`
		CredentialRef string `db:"credential_ref"`
	}
	if err := s.db.GetContext(ctx, &row, `SELECT mv.provider_model_name,pe.base_url,pe.credential_ref FROM model_variants mv JOIN provider_endpoints pe ON pe.id=mv.provider_endpoint_id JOIN providers p ON p.id=pe.provider_id WHERE mv.id=? AND mv.status IN ('active','degraded') AND pe.status='active' AND p.status='active'`, variantID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ProviderExecutionTarget{}, ErrNotFound
		}
		return ProviderExecutionTarget{}, err
	}
	credential := ""
	if s.secrets != nil {
		var err error
		credential, err = s.secrets.decrypt(row.CredentialRef)
		if err != nil {
			return ProviderExecutionTarget{}, err
		}
	}
	return ProviderExecutionTarget{Endpoint: row.Endpoint, Credential: credential, Model: row.Model}, nil
}

// GatewayPricesForVariant returns the immutable price versions active at a
// request/session start time, preserving the original settlement contract.
func (s *Store) GatewayPricesForVariant(ctx context.Context, variantID, at string) (map[string]GatewayPrice, error) {
	var prices []GatewayPrice
	if err := s.db.SelectContext(ctx, &prices, `
		SELECT id, metric, unit_size, base_customer_price_microcredits,
		       customer_price_microcredits, discount_bps
		FROM model_variant_prices
		WHERE model_variant_id = ? AND valid_from <= ?
		  AND (valid_until IS NULL OR valid_until > ?)
		ORDER BY metric, valid_from DESC
	`, variantID, at, at); err != nil {
		return nil, fmt.Errorf("read Gateway prices: %w", err)
	}
	priceMap := make(map[string]GatewayPrice, len(prices))
	for _, price := range prices {
		if _, exists := priceMap[price.Metric]; !exists {
			priceMap[price.Metric] = price
		}
	}
	return priceMap, nil
}

// CheckedCharge performs overflow-safe ceiling(quantity*price/unitSize).
func CheckedCharge(quantity, price, unitSize int64) (int64, error) {
	if quantity < 0 || price < 0 || unitSize <= 0 {
		return 0, errors.New("invalid charge inputs")
	}
	if quantity != 0 && price > math.MaxInt64/quantity {
		return 0, errors.New("charge overflow")
	}
	product := quantity * price
	if product == 0 {
		return 0, nil
	}
	return 1 + (product-1)/unitSize, nil
}

// BeginGatewayCommand atomically reserves Credit or returns a completed replay.
func (s *Store) BeginGatewayCommand(ctx context.Context, command GatewayCommand) (GatewayBeginResult, error) {
	return retrySerializable(ctx, func() (GatewayBeginResult, error) {
		return s.beginGatewayCommand(ctx, command)
	})
}

func (s *Store) beginGatewayCommand(ctx context.Context, command GatewayCommand) (GatewayBeginResult, error) {
	if command.ExecutionLeaseUntil == "" {
		started, parseErr := timetext.Parse(command.StartedAt)
		if parseErr != nil {
			return GatewayBeginResult{}, fmt.Errorf("parse Gateway command start: %w", parseErr)
		}
		command.ExecutionLeaseUntil = timetext.Format(started.Add(45 * time.Second))
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return GatewayBeginResult{}, fmt.Errorf("begin Gateway command: %w", err)
	}
	defer tx.Rollback()
	existing, err := s.resumeGatewayCommandTx(ctx, tx, command.APIKeyID, command.Operation, command.IdempotencyKey, command.PayloadHash, command.StartedAt, command.ExecutionLeaseUntil)
	if err != nil {
		return GatewayBeginResult{}, err
	}
	if existing.Existing {
		if err := tx.Commit(); err != nil {
			return GatewayBeginResult{}, err
		}
		return existing, nil
	}
	// Middleware authentication is only an early rejection. Revalidate the
	// credential, account and user inside the serializable reservation command
	// so a concurrent revoke/suspend cannot win after authentication and still
	// cause provider work or a Credit reservation.
	var authorized int
	if err := tx.GetContext(ctx, &authorized, `SELECT COUNT(*) FROM api_keys k JOIN accounts a ON a.id=k.account_id JOIN users u ON u.id=a.owner_user_id WHERE k.id=? AND k.account_id=? AND k.kind='gateway' AND k.status='active' AND (k.expires_at IS NULL OR k.expires_at>?) AND a.status='active' AND u.status='active'`, command.APIKeyID, command.AccountID, command.StartedAt); err != nil {
		return GatewayBeginResult{}, err
	}
	if authorized == 0 {
		return GatewayBeginResult{}, ErrNotFound
	}
	available, err := availableCredit(ctx, tx, command.AccountID)
	if err != nil {
		return GatewayBeginResult{}, fmt.Errorf("read available Credit: %w", err)
	}
	if available < command.ReserveAmount {
		return GatewayBeginResult{}, ErrInsufficientBalance
	}
	if len(command.ExecutionSnapshot) == 0 {
		return GatewayBeginResult{}, errors.New("gateway command execution snapshot is required")
	}
	protectedSnapshot, err := s.protectAPIResponse(command.ExecutionSnapshot)
	if err != nil {
		return GatewayBeginResult{}, fmt.Errorf("encrypt Gateway execution snapshot: %w", err)
	}
	var protectedRecoveryRequest []byte
	if len(command.RecoveryRequest) > 0 {
		protectedRecoveryRequest, err = s.protectAPIResponse(command.RecoveryRequest)
		if err != nil {
			return GatewayBeginResult{}, fmt.Errorf("encrypt Gateway recovery request: %w", err)
		}
	}
	protocol := command.Protocol
	if protocol == "" {
		protocol = "https"
	}
	recoveryStatus := any(nil)
	if len(protectedRecoveryRequest) > 0 {
		recoveryStatus = "pending"
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO gateway_requests
		(id, account_id, api_key_id, model_id, model_variant_id, operation, idempotency_key,
		 payload_hash, protocol, status, started_at,execution_lease_until,execution_attempts,execution_snapshot,recovery_request,recovery_status)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'started', ?,?,1,?,?,?)`, command.ID,
		command.AccountID, command.APIKeyID, command.ModelID, command.VariantID,
		command.Operation, command.IdempotencyKey, command.PayloadHash, protocol, command.StartedAt, command.ExecutionLeaseUntil, protectedSnapshot, protectedRecoveryRequest, recoveryStatus)
	if err != nil {
		return GatewayBeginResult{}, fmt.Errorf("insert Gateway request: %w", err)
	}
	// Fully-discounted/free catalog prices are valid. They still create a
	// durable request and exact zero-price charge snapshots, but no positive
	// Credit hold or empty ledger transaction is necessary.
	if command.ReserveAmount > 0 {
		_, err = tx.ExecContext(ctx, `INSERT INTO credit_reservations
			(id, account_id, api_key_id, amount_microcredits, status, idempotency_key, created_at)
			VALUES (?, ?, ?, ?, 'active', ?, ?)`, uuid.NewString(), command.AccountID,
			command.APIKeyID, command.ReserveAmount, command.ID, command.StartedAt)
		if err != nil {
			return GatewayBeginResult{}, fmt.Errorf("insert Credit reservation: %w", err)
		}
	}
	if err := recordAudit(ctx, tx, "api_key", command.APIKeyID, "gateway.reserved", "gateway_request", command.ID, "Credit reserved for provider execution", `{"operation":"`+command.Operation+`"}`, command.StartedAt); err != nil {
		return GatewayBeginResult{}, fmt.Errorf("audit Gateway reservation: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return GatewayBeginResult{}, fmt.Errorf("commit Gateway reservation: %w", err)
	}
	return GatewayBeginResult{RequestID: command.ID}, nil
}

// RecoverableGatewayCommands returns expired, reconstructable HTTPS commands.
// It does not claim the lease: replaying the saved request runs through the
// ordinary ResumeGatewayCommand transaction, which is the single atomic claim
// shared by client retries and every application replica.
func (s *Store) RecoverableGatewayCommands(ctx context.Context, at string, limit int) ([]RecoverableGatewayCommand, error) {
	if limit <= 0 {
		limit = 32
	}
	var rows []struct {
		RequestID       string `db:"request_id"`
		UserID          string `db:"user_id"`
		AccountID       string `db:"account_id"`
		APIKeyID        string `db:"api_key_id"`
		Operation       string `db:"operation"`
		IdempotencyKey  string `db:"idempotency_key"`
		RecoveryRequest []byte `db:"recovery_request"`
	}
	if err := s.db.SelectContext(ctx, &rows, `SELECT g.id AS request_id,a.owner_user_id AS user_id,g.account_id,g.api_key_id,g.operation,g.idempotency_key,g.recovery_request
		FROM gateway_requests g JOIN accounts a ON a.id=g.account_id
		WHERE g.protocol='https' AND g.status='started' AND g.execution_lease_until<=?
		  AND g.recovery_request IS NOT NULL AND g.recovery_status='pending'
		  AND (g.recovery_next_attempt_at IS NULL OR g.recovery_next_attempt_at<=?)
		  AND NOT EXISTS (SELECT 1 FROM gateway_settlement_outbox o WHERE o.request_id=g.id)
		ORDER BY COALESCE(g.recovery_next_attempt_at,g.execution_lease_until),g.id LIMIT ?`, at, at, limit); err != nil {
		return nil, fmt.Errorf("list recoverable Gateway commands: %w", err)
	}
	commands := make([]RecoverableGatewayCommand, 0, len(rows))
	var firstErr error
	for _, row := range rows {
		recoveryRequest, err := s.unprotectAPIResponse(row.RecoveryRequest)
		if err != nil {
			// One corrupt/foreign-key ciphertext must never poison the ordered
			// recovery queue. Move only this command to an explicit manual state;
			// later valid rows remain eligible in the same pass.
			markErr := s.RecordGatewayRecoveryFailure(ctx, row.RequestID, "decrypt recovery request: "+err.Error(), at, true)
			if markErr != nil && firstErr == nil {
				firstErr = markErr
			}
			continue
		}
		commands = append(commands, RecoverableGatewayCommand{
			RequestID: row.RequestID,
			Principal: GatewayPrincipal{UserID: row.UserID, AccountID: row.AccountID, APIKeyID: row.APIKeyID},
			Operation: row.Operation, IdempotencyKey: row.IdempotencyKey,
			RecoveryRequest: recoveryRequest,
		})
	}
	return commands, firstErr
}

const maximumGatewayRecoveryAttempts = 5

// RecordGatewayRecoveryFailure makes replay failure durable and observable.
// Transient failures use bounded exponential backoff; malformed/corrupt input
// and repeated failures enter reconciliation_required without releasing the
// reservation, because the provider may already have committed the command.
func (s *Store) RecordGatewayRecoveryFailure(ctx context.Context, requestID, message, at string, permanent bool) error {
	return retrySerializableError(ctx, func() error {
		return s.recordGatewayRecoveryFailure(ctx, requestID, message, at, permanent)
	})
}

func (s *Store) recordGatewayRecoveryFailure(ctx context.Context, requestID, message, at string, permanent bool) error {
	if len(message) > 1024 {
		message = message[:1024]
	}
	instant, err := timetext.Parse(at)
	if err != nil {
		return fmt.Errorf("parse Gateway recovery failure time: %w", err)
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var attempts int
	if err := tx.GetContext(ctx, &attempts, `SELECT recovery_attempts FROM gateway_requests WHERE id=? AND status='started' AND recovery_status='pending'`, requestID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	attempts++
	reconciliation := permanent || attempts >= maximumGatewayRecoveryAttempts
	var nextAttempt any
	status := "pending"
	if reconciliation {
		status = "reconciliation_required"
	} else {
		shift := min(attempts-1, 8)
		nextAttempt = timetext.Format(instant.Add(time.Duration(1<<shift) * time.Second))
	}
	if _, err := tx.ExecContext(ctx, `UPDATE gateway_requests SET recovery_status=?,recovery_attempts=?,recovery_next_attempt_at=?,recovery_last_error=? WHERE id=? AND status='started' AND recovery_status='pending'`, status, attempts, nextAttempt, message, requestID); err != nil {
		return err
	}
	if reconciliation {
		if err := recordAudit(ctx, tx, "system", "gateway-recovery", "gateway.reconciliation_required", "gateway_request", requestID, message, fmt.Sprintf(`{"recovery_attempts":%d}`, attempts), at); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// CreateCorruptGatewayRecoveryFixture injects one encrypted-envelope failure
// for the story-only recovery contract. Corruption cannot be produced through
// a public business API; this narrow fault seam lets Hurl prove that a poison
// row enters reconciliation and does not block a later real command. Production
// composition never registers the caller route.
func (s *Store) CreateCorruptGatewayRecoveryFixture(ctx context.Context, at string) (string, error) {
	const requestID = "0c000000-0000-4000-8000-000000000001"
	instant, err := timetext.Parse(at)
	if err != nil {
		return "", err
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO gateway_requests
		(id,account_id,api_key_id,model_id,model_variant_id,operation,idempotency_key,payload_hash,protocol,status,started_at,execution_lease_until,execution_attempts,execution_snapshot,recovery_request,recovery_status)
		VALUES (?,?,?,?,?,?,?,?,'https','started',?,?,1,?,?,'pending')`, requestID,
		"21000000-0000-4000-8000-000000000001", "31000000-0000-4000-8000-000000000001",
		"81000000-0000-4000-8000-000000000001", "91000000-0000-4000-8000-000000000001",
		"chat.completions", "story-poison-recovery", []byte("story-poison"), at,
		timetext.Format(instant.Add(-time.Second)), []byte("enc:v1:corrupt-plan"), []byte("enc:v1:corrupt-envelope"))
	if err != nil {
		return "", err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO credit_reservations(id,account_id,api_key_id,amount_microcredits,status,idempotency_key,created_at)
		VALUES (?,?,?,7,'active',?,?)`, "0c000000-0000-4000-8000-000000000002",
		"21000000-0000-4000-8000-000000000001", "31000000-0000-4000-8000-000000000001", requestID, at)
	if err != nil {
		return "", err
	}
	if err := recordAudit(ctx, tx, "system", "story-fixture", "gateway.recovery_fault_injected", "gateway_request", requestID, "corrupt encrypted recovery envelope", "{}", at); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return requestID, nil
}

// SettleGatewayCommand atomically snapshots charges, posts a balanced ledger
// transaction, persists the provider response, and consumes the reservation.
func (s *Store) SettleGatewayCommand(ctx context.Context, requestID, providerRequestID string, metrics []GatewayMetric, responseJSON []byte, completedAt string) error {
	return s.queueAndSettleGatewayCommand(ctx, requestID, providerRequestID, "", metrics, responseJSON, completedAt)
}

// SettleGatewayCommandForVariant records the Bifrost-resolved fallback winner
// together with its price snapshots. This keeps request attribution truthful
// while preserving the same atomic ledger/response transition.
func (s *Store) SettleGatewayCommandForVariant(ctx context.Context, requestID, providerRequestID, resolvedVariantID string, metrics []GatewayMetric, responseJSON []byte, completedAt string) error {
	return s.queueAndSettleGatewayCommand(ctx, requestID, providerRequestID, resolvedVariantID, metrics, responseJSON, completedAt)
}

func (s *Store) queueAndSettleGatewayCommand(ctx context.Context, requestID, providerRequestID, resolvedVariantID string, metrics []GatewayMetric, responseJSON []byte, completedAt string) error {
	encodedMetrics, err := json.Marshal(metrics)
	if err != nil {
		return fmt.Errorf("encode Gateway settlement metrics: %w", err)
	}
	if !json.Valid(responseJSON) {
		return errors.New("gateway settlement response is not valid JSON")
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO gateway_settlement_outbox
		(request_id,provider_request_id,resolved_variant_id,metrics_json,response_json,completed_at,status,attempts,updated_at)
		VALUES (?,?,?,?,?,?,'pending',0,?) ON CONFLICT(request_id) DO NOTHING`,
		requestID, providerRequestID, resolvedVariantID, string(encodedMetrics), string(responseJSON), completedAt, completedAt)
	if err != nil {
		return fmt.Errorf("queue Gateway settlement: %w", err)
	}
	return s.settleQueuedGatewayCommand(ctx, requestID)
}

func (s *Store) settleQueuedGatewayCommand(ctx context.Context, requestID string) error {
	var row struct {
		ProviderRequestID string `db:"provider_request_id"`
		ResolvedVariantID string `db:"resolved_variant_id"`
		MetricsJSON       string `db:"metrics_json"`
		ResponseJSON      string `db:"response_json"`
		CompletedAt       string `db:"completed_at"`
		Status            string `db:"status"`
	}
	if err := s.db.GetContext(ctx, &row, `SELECT provider_request_id,resolved_variant_id,metrics_json,response_json,completed_at,status FROM gateway_settlement_outbox WHERE request_id=?`, requestID); err != nil {
		return err
	}
	if row.Status == "succeeded" {
		return nil
	}
	var metrics []GatewayMetric
	if err := json.Unmarshal([]byte(row.MetricsJSON), &metrics); err != nil {
		return fmt.Errorf("decode queued Gateway settlement: %w", err)
	}
	return retrySerializableError(ctx, func() error {
		return s.settleGatewayCommandOnce(ctx, requestID, row.ProviderRequestID, row.ResolvedVariantID, metrics, []byte(row.ResponseJSON), row.CompletedAt)
	})
}

// RecoverGatewaySettlements retries durable provider-success records. Failed
// attempts retain the active reservation and are safe to replay because the
// ledger transaction and charge rows are unique per Gateway request.
func (s *Store) RecoverGatewaySettlements(ctx context.Context, limit int) error {
	if limit <= 0 {
		limit = 32
	}
	var ids []string
	if err := s.db.SelectContext(ctx, &ids, `SELECT request_id FROM gateway_settlement_outbox WHERE status='pending' ORDER BY updated_at,request_id LIMIT ?`, limit); err != nil {
		return err
	}
	for _, id := range ids {
		if err := s.settleQueuedGatewayCommand(ctx, id); err != nil {
			_, _ = s.db.ExecContext(ctx, `UPDATE gateway_settlement_outbox SET attempts=attempts+1,last_error=?,updated_at=? WHERE request_id=? AND status='pending'`, err.Error(), timetext.Format(s.now()), id)
		}
	}
	return nil
}

func (s *Store) settleGatewayCommandOnce(ctx context.Context, requestID, providerRequestID, resolvedVariantID string, metrics []GatewayMetric, responseJSON []byte, completedAt string) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin Gateway settlement: %w", err)
	}
	defer tx.Rollback()
	var request struct {
		AccountID string `db:"account_id"`
		APIKeyID  string `db:"api_key_id"`
		Status    string `db:"status"`
	}
	if err := tx.GetContext(ctx, &request, `SELECT account_id,api_key_id,status FROM gateway_requests WHERE id=?`, requestID); err != nil {
		return fmt.Errorf("read unsettled Gateway request: %w", err)
	}
	// Concurrent HTTP retries may both receive the provider's idempotent replay
	// and race to settle. The first transaction performs the economic mutation;
	// later transactions converge on that terminal result instead of reporting a
	// false failure or attempting a second ledger post.
	if request.Status == "succeeded" {
		if _, err := tx.ExecContext(ctx, `UPDATE gateway_settlement_outbox SET status='succeeded',last_error=NULL,updated_at=? WHERE request_id=?`, completedAt, requestID); err != nil {
			return err
		}
		return tx.Commit()
	}
	if request.Status != "started" {
		return fmt.Errorf("gateway request cannot settle from status %s", request.Status)
	}
	var total int64
	for _, metric := range metrics {
		if metric.Charge > math.MaxInt64-total {
			return errors.New("total charge overflow")
		}
		total += metric.Charge
		_, err = tx.ExecContext(ctx, `INSERT INTO gateway_request_charges
			(id, gateway_request_id, model_variant_price_id, metric, quantity, unit_size,
			 base_price_microcredits, effective_price_microcredits, discount_bps,
			 charged_microcredits, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			uuid.NewString(), requestID, metric.Price.ID, metric.Metric, metric.Quantity,
			metric.Price.UnitSize, metric.Price.BasePrice, metric.Price.EffectivePrice,
			metric.Price.DiscountBPS, metric.Charge, completedAt)
		if err != nil {
			return fmt.Errorf("insert Gateway charge: %w", err)
		}
	}
	var reserved int64
	if err := tx.GetContext(ctx, &reserved, `SELECT amount_microcredits FROM credit_reservations WHERE idempotency_key = ? AND status = 'active'`, requestID); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("read Gateway reservation: %w", err)
		}
		reserved = 0
	}
	if total > reserved {
		return errors.New("measured charge exceeds reservation")
	}
	if total > 0 {
		var userLedger, systemLedger string
		if err := tx.GetContext(ctx, &userLedger, `SELECT id FROM ledger_accounts WHERE owner_account_id = ? AND asset_code = 'GIZ_CREDIT'`, request.AccountID); err != nil {
			return err
		}
		if err := tx.GetContext(ctx, &systemLedger, `SELECT id FROM ledger_accounts WHERE owner_account_id IS NULL AND code = 'SYSTEM:CREDIT_LIABILITY'`); err != nil {
			return err
		}
		ledgerID := uuid.NewString()
		_, err = tx.ExecContext(ctx, `INSERT INTO ledger_transactions
			(id, transaction_type, status, idempotency_key, initiated_by_account_id,
			 reference_type, reference_id, description, created_at, posted_at)
			VALUES (?, 'gateway_usage', 'posted', ?, ?, 'gateway_request', ?, 'AI usage settlement', ?, ?)`,
			ledgerID, "gateway:"+requestID, request.AccountID, requestID, completedAt, completedAt)
		if err != nil {
			return fmt.Errorf("insert Gateway ledger transaction: %w", err)
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO ledger_entries
			(id, transaction_id, ledger_account_id, sequence, direction, amount_microcredits, created_at)
			VALUES (?, ?, ?, 1, 'debit', ?, ?), (?, ?, ?, 2, 'credit', ?, ?)`,
			uuid.NewString(), ledgerID, userLedger, total, completedAt,
			uuid.NewString(), ledgerID, systemLedger, total, completedAt)
		if err != nil {
			return fmt.Errorf("insert Gateway ledger entries: %w", err)
		}
		if err := consumePurchasedLots(ctx, tx, request.AccountID, total); err != nil {
			return fmt.Errorf("consume refundable Credit for Gateway request: %w", err)
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE gateway_requests SET status='succeeded', provider_request_id=?,
		model_variant_id=CASE WHEN ?='' THEN model_variant_id ELSE ? END,
		input_tokens=?, output_tokens=?, cached_input_tokens=?, input_audio_tokens=?, output_audio_tokens=?, charged_microcredits=?,
		response_json=?, recovery_status=CASE WHEN recovery_request IS NULL THEN NULL ELSE 'completed' END,
		recovery_next_attempt_at=NULL,recovery_last_error=NULL,completed_at=? WHERE id=?`, providerRequestID, resolvedVariantID, resolvedVariantID,
		metricQuantity(metrics, "input_token")+metricQuantity(metrics, "input_audio_token"),
		metricQuantity(metrics, "output_token")+metricQuantity(metrics, "output_audio_token"),
		metricQuantity(metrics, "cached_input_token"), metricQuantity(metrics, "input_audio_token"),
		metricQuantity(metrics, "output_audio_token"), total, string(responseJSON), completedAt, requestID)
	if err != nil {
		return fmt.Errorf("complete Gateway request: %w", err)
	}
	_, err = tx.ExecContext(ctx, `UPDATE credit_reservations SET status='settled', completed_at=? WHERE idempotency_key=? AND status='active'`, completedAt, requestID)
	if err != nil {
		return fmt.Errorf("settle Gateway reservation: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE realtime_sessions SET status='succeeded',completed_at=? WHERE gateway_request_id=? AND status='connected'`, completedAt, requestID); err != nil {
		return fmt.Errorf("complete Realtime session: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE gateway_settlement_outbox SET status='succeeded',attempts=attempts+1,last_error=NULL,updated_at=? WHERE request_id=?`, completedAt, requestID); err != nil {
		return fmt.Errorf("complete Gateway settlement outbox: %w", err)
	}
	if err := recordAudit(ctx, tx, "api_key", request.APIKeyID, "gateway.settled", "gateway_request", requestID, "trusted provider usage settled", fmt.Sprintf(`{"charged_microcredits":%d}`, total), completedAt); err != nil {
		return fmt.Errorf("audit Gateway settlement: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit Gateway settlement: %w", err)
	}
	return nil
}

func metricQuantity(metrics []GatewayMetric, name string) int64 {
	for _, metric := range metrics {
		if metric.Metric == name {
			return metric.Quantity
		}
	}
	return 0
}

// ReleaseGatewayCommand records a terminal provider failure and frees Credit.
func (s *Store) ReleaseGatewayCommand(ctx context.Context, requestID, code string) error {
	return retrySerializableError(ctx, func() error { return s.releaseGatewayCommand(ctx, requestID, code) })
}

func (s *Store) releaseGatewayCommand(ctx context.Context, requestID, code string) error {
	now := timetext.Format(s.now())
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var request struct {
		AccountID string `db:"account_id"`
		APIKeyID  string `db:"api_key_id"`
	}
	if err := tx.GetContext(ctx, &request, `SELECT account_id,api_key_id FROM gateway_requests WHERE id=?`, requestID); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE gateway_requests SET status='failed', error_code=?, recovery_status=CASE WHEN recovery_request IS NULL THEN NULL ELSE 'completed' END,recovery_next_attempt_at=NULL,completed_at=? WHERE id=? AND status='started'`, code, now, requestID)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		return tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, `UPDATE credit_reservations SET status='released', completed_at=? WHERE idempotency_key=? AND status='active'`, now, requestID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE realtime_sessions SET status=CASE WHEN ?='client_disconnect' THEN 'cancelled' ELSE 'failed' END,completed_at=? WHERE gateway_request_id=? AND status IN ('created','connected')`, code, now, requestID); err != nil {
		return err
	}
	if err := recordAudit(ctx, tx, "api_key", request.APIKeyID, "gateway.released", "gateway_request", requestID, code, "{}", now); err != nil {
		return err
	}
	return tx.Commit()
}
