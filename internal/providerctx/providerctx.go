// Package providerctx carries internal provider-execution metadata across the
// Service-to-Bifrost boundary without exposing it on public API types.
package providerctx

import "context"

type idempotencyKeyContextKey struct{}
type recoveryRequestContextKey struct{}
type recoveryExecutionContextKey struct{}

// RecoveryRequest is the minimal, secret-free HTTP envelope required to
// reconstruct an interrupted Gateway command. The Store encrypts its encoded
// form at rest before any provider call. Authorization is represented by the
// immutable API-key principal stored on gateway_requests; bearer credentials
// are deliberately never retained.
type RecoveryRequest struct {
	Method      string `json:"method"`
	RequestURI  string `json:"request_uri"`
	ContentType string `json:"content_type,omitempty"`
	Body        []byte `json:"body"`
}

// WithIdempotencyKey binds the durable Gizway request ID to every upstream
// attempt, including Bifrost retries and cross-provider fallbacks.
func WithIdempotencyKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, idempotencyKeyContextKey{}, key)
}

// IdempotencyKey returns the durable upstream command identity, if present.
func IdempotencyKey(ctx context.Context) string {
	value, _ := ctx.Value(idempotencyKeyContextKey{}).(string)
	return value
}

// WithRecoveryRequest binds a defensive copy of the authenticated public
// request to the service call that creates the durable Gateway reservation.
func WithRecoveryRequest(ctx context.Context, request RecoveryRequest) context.Context {
	request.Body = append([]byte(nil), request.Body...)
	return context.WithValue(ctx, recoveryRequestContextKey{}, request)
}

// RecoveryRequestFrom returns a defensive copy so downstream code cannot
// mutate the request body that the middleware captured for crash recovery.
func RecoveryRequestFrom(ctx context.Context) (RecoveryRequest, bool) {
	request, ok := ctx.Value(recoveryRequestContextKey{}).(RecoveryRequest)
	if !ok {
		return RecoveryRequest{}, false
	}
	request.Body = append([]byte(nil), request.Body...)
	return request, true
}

// WithRecoveryExecution marks an internal worker replay. Provider or response
// failures during an ambiguous replay must retain the reservation for another
// lease attempt; they must not be mistaken for a definitive first-attempt
// rejection and release potentially consumed Credit.
func WithRecoveryExecution(ctx context.Context) context.Context {
	return context.WithValue(ctx, recoveryExecutionContextKey{}, true)
}

// IsRecoveryExecution reports whether the command is an internal replay of an
// already-authorized, potentially provider-committed execution.
func IsRecoveryExecution(ctx context.Context) bool {
	value, _ := ctx.Value(recoveryExecutionContextKey{}).(bool)
	return value
}
