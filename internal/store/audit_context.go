package store

import "context"

type auditRequestIDContextKey struct{}
type auditActorContextKey struct{}

type auditActor struct {
	Type string
	ID   string
}

// WithAuditRequestID carries the API correlation identity into Store
// transactions without coupling persistence code to HTTP.
func WithAuditRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, auditRequestIDContextKey{}, requestID)
}

func auditRequestID(ctx context.Context) string {
	value, _ := ctx.Value(auditRequestIDContextKey{}).(string)
	return value
}

// WithAuditActor carries the stable, non-secret authenticated principal into
// Store transactions. HTTP credentials use their database key ID; the bearer
// secret itself is never persisted or logged.
func WithAuditActor(ctx context.Context, actorType, actorID string) context.Context {
	return context.WithValue(ctx, auditActorContextKey{}, auditActor{Type: actorType, ID: actorID})
}

func authenticatedAuditActor(ctx context.Context) (auditActor, bool) {
	actor, ok := ctx.Value(auditActorContextKey{}).(auditActor)
	return actor, ok && actor.Type != "" && actor.ID != ""
}
