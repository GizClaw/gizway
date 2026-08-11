package store

import (
	"context"
	"database/sql"

	"github.com/google/uuid"
)

// auditExecutor is deliberately satisfied by both boundTx and boundDB. Domain
// mutations normally pass the transaction that owns the state change, making
// it impossible to commit the business write without its audit record.
type auditExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func recordAudit(ctx context.Context, q auditExecutor, actorType, actorID, action, resourceType, resourceID, reason, metadata, at string) error {
	if metadata == "" {
		metadata = "{}"
	}
	_, err := q.ExecContext(ctx, `INSERT INTO audit_events(id,actor_type,actor_id,action,resource_type,resource_id,reason,request_id,metadata,created_at) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		uuid.NewString(), actorType, actorID, action, resourceType, resourceID, reason, auditRequestID(ctx), metadata, at)
	return err
}

// insertAccountOwnerAudit prefers the stable authenticated actor supplied by
// transport middleware. Direct Store callers without transport context fall
// back to the account owner, preserving truthful attribution in jobs/tests
// that explicitly act for a user. Secret credential values are never stored.
func insertAccountOwnerAudit(ctx context.Context, q auditExecutor, accountID, action, resourceType, resourceID, reason, metadata, at string) error {
	if metadata == "" {
		metadata = "{}"
	}
	if actor, ok := authenticatedAuditActor(ctx); ok {
		return recordAudit(ctx, q, actor.Type, actor.ID, action, resourceType, resourceID, reason, metadata, at)
	}
	_, err := q.ExecContext(ctx, `INSERT INTO audit_events(id,actor_type,actor_id,action,resource_type,resource_id,reason,request_id,metadata,created_at)
		SELECT ?,'user',owner_user_id,?,?,?,?,?,?,? FROM accounts WHERE id=?`,
		uuid.NewString(), action, resourceType, resourceID, reason, auditRequestID(ctx), metadata, at, accountID)
	return err
}
