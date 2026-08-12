package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// AdminListQuery is the bounded, cursor-based query contract shared by Admin
// list endpoints. Every field is mapped through a fixed SQL whitelist below;
// caller input is never interpolated as a column or expression.
type AdminListQuery struct {
	Cursor          string
	Limit           int
	Query           string
	Status          string
	AccountID       string
	APIKeyID        string
	ModelID         string
	KeyPrefix       string
	Kind            string
	Type            string
	OwnerAccountID  string
	TransactionType string
	ReferenceID     string
	MerchantID      string
	ActorID         string
	Action          string
	ResourceType    string
	ResourceID      string
	NodeID          string
	Region          string
	From            string
	To              string
}

type AdminPage[T any] struct {
	Items      []T
	NextCursor *string
	HasMore    bool
}

func normalizedAdminLimit(limit int) int {
	if limit <= 0 {
		return 20
	}
	if limit > 100 {
		return 100
	}
	return limit
}

func selectAdminStructPage[T any](ctx context.Context, s *Store, selectSQL, orderBy string, conditions []string, args []any, query AdminListQuery) (AdminPage[T], error) {
	if len(conditions) != 0 {
		selectSQL += " WHERE " + strings.Join(conditions, " AND ")
	}
	limit := normalizedAdminLimit(query.Limit)
	offset, err := adminCursorOffset(query.Cursor)
	if err != nil {
		return AdminPage[T]{}, err
	}
	selectSQL += " ORDER BY " + orderBy + " LIMIT ? OFFSET ?"
	args = append(args, limit+1, offset)
	var rows []T
	if err := s.db.SelectContext(ctx, &rows, selectSQL, args...); err != nil {
		return AdminPage[T]{}, err
	}
	page := AdminPage[T]{Items: rows}
	if len(rows) > limit {
		page.HasMore = true
		page.Items = rows[:limit]
		cursor := strconv.Itoa(offset + limit)
		page.NextCursor = &cursor
	}
	return page, nil
}

func (s *Store) ListAdministratorsPage(ctx context.Context, query AdminListQuery) (AdminPage[Administrator], error) {
	conditions, args := []string{}, []any{}
	if query.Status != "" {
		conditions, args = append(conditions, "status=?"), append(args, query.Status)
	}
	return selectAdminStructPage[Administrator](ctx, s, `SELECT `+administratorColumns+` FROM administrators`, "created_at,id", conditions, args, query)
}

func (s *Store) AdminListUsersPage(ctx context.Context, query AdminListQuery) (AdminPage[User], error) {
	conditions, args := []string{}, []any{}
	if query.Query != "" {
		conditions = append(conditions, `(LOWER(email) LIKE ? OR LOWER(display_name) LIKE ?)`)
		value := "%" + strings.ToLower(query.Query) + "%"
		args = append(args, value, value)
	}
	if query.Status != "" {
		conditions, args = append(conditions, "status=?"), append(args, query.Status)
	}
	return selectAdminStructPage[User](ctx, s, `SELECT id,email,display_name,status,created_at FROM users`, "created_at,id", conditions, args, query)
}

func (s *Store) ListModelsPage(ctx context.Context, query AdminListQuery) (AdminPage[Model], error) {
	conditions, args := []string{}, []any{}
	if query.Status != "" {
		conditions, args = append(conditions, "status=?"), append(args, query.Status)
	}
	return selectAdminStructPage[Model](ctx, s, `SELECT id,slug,name,modality,status,metadata,created_at,updated_at FROM models`, "slug,id", conditions, args, query)
}

func (s *Store) AdminListAPIKeysPage(ctx context.Context, query AdminListQuery) (AdminPage[APIKey], error) {
	conditions, args := []string{}, []any{}
	if query.AccountID != "" {
		conditions, args = append(conditions, "account_id=?"), append(args, query.AccountID)
	}
	if query.Status != "" {
		conditions, args = append(conditions, "status=?"), append(args, query.Status)
	}
	if query.KeyPrefix != "" {
		conditions, args = append(conditions, "key_prefix LIKE ?"), append(args, query.KeyPrefix+"%")
	}
	return selectAdminStructPage[APIKey](ctx, s, `SELECT id,account_id,name,kind,key_prefix,scopes,status,expires_at,last_used_at,created_at FROM api_keys`, "created_at,id", conditions, args, query)
}

func (s *Store) ListMerchantsPage(ctx context.Context, query AdminListQuery) (AdminPage[map[string]any], error) {
	conditions, args := []string{}, []any{}
	if query.Status != "" {
		// WHERE must reference the source column rather than the SELECT alias.
		conditions, args = append(conditions, "merchant_status=?"), append(args, query.Status)
	}
	return s.adminMapPage(ctx, `SELECT account_id AS id,account_id,legal_name,public_name,merchant_status AS status,review_level,country_code,website_url,created_at FROM merchant_accounts`, "created_at,account_id", conditions, args, query)
}

func (s *Store) AdminRowsPage(ctx context.Context, kind string, query AdminListQuery) (AdminPage[map[string]any], error) {
	base := ""
	orderBy := "id"
	conditions, args := []string{}, []any{}
	add := func(value, expression string) {
		if value != "" {
			conditions, args = append(conditions, expression), append(args, value)
		}
	}
	switch kind {
	case "received_usage":
		base = `SELECT ucgid,account_id,node_id,region,operation_id,public_model,model_variant_id,
			rate_publication_id,charged_microcredits,ledger_transaction_id,started_at,completed_at,received_at
			FROM gateway_usage_records`
		add(query.AccountID, "account_id=?")
		add(query.NodeID, "node_id=?")
		add(query.Region, "region=?")
		add(query.ModelID, "public_model=?")
		add(query.From, "received_at>=?")
		add(query.To, "received_at<?")
		orderBy = "received_at,ucgid"
	case "rate_publications":
		base = `SELECT id,node_id,region,source_publication_id,revision,status,effective_at,created_at
			FROM billing_rate_publications`
		add(query.NodeID, "node_id=?")
		add(query.Region, "region=?")
		add(query.Status, "status=?")
		orderBy = "created_at,id"
	case "regional_rate_publications":
		base = `SELECT id,region,revision,status,gizpay_publication_id,effective_at,created_at,updated_at
			FROM rate_publications`
		add(query.Region, "region=?")
		add(query.Status, "status=?")
		orderBy = "created_at,id"
	case "usage_outbox":
		// Deliberately omit runtime_key_token and payload: regional diagnostics
		// expose delivery state, never the in-memory key association mechanism.
		base = `SELECT ucgid,operation_id,public_model,model_variant_id,rate_publication_id,
			period_started_at,period_ended_at,gateway_calculated_microcredits,status,attempt_count,
			next_attempt_at,last_error,created_at,updated_at,reported_at,abandoned_at,failed_at
			FROM gateway_usage_outbox`
		add(query.Status, "status=?")
		add(query.ModelID, "public_model=?")
		add(query.From, "created_at>=?")
		add(query.To, "created_at<?")
		orderBy = "created_at,ucgid"
	case "gateway_executions":
		// Regional Admin intentionally exposes the identity-free local
		// execution lifecycle. Account and API-key filters do not exist on a
		// GizWay database because those identities belong exclusively to GizPay.
		base = `SELECT id,public_model,model_variant_id,provider_request_id,protocol,stream_mode,
			rate_publication_id,status,estimated_microcredits,actual_microcredits,started_at,completed_at
			FROM gateway_executions`
		add(query.ModelID, "public_model=?")
		add(query.Status, "status=?")
		add(query.From, "started_at>=?")
		add(query.To, "started_at<?")
		orderBy = "started_at,id"
	case "payments":
		base = `SELECT * FROM (SELECT id,'topup' AS type,account_id,credit_microcredits AS amount_microcredits,status,created_at FROM topups UNION ALL SELECT id,'refund',account_id,credit_microcredits,status,created_at FROM refunds UNION ALL SELECT id,'transfer',sender_account_id AS account_id,amount_microcredits,status,created_at FROM credit_transfers UNION ALL SELECT id,'merchant_payment',merchant_account_id AS account_id,amount_microcredits,status,created_at FROM payment_intents) workflows`
		add(query.Type, "type=?")
		add(query.AccountID, "account_id=?")
		add(query.Status, "status=?")
		add(query.From, "created_at>=?")
		add(query.To, "created_at<?")
		orderBy = "created_at,id"
	case "ledger_accounts":
		base = `SELECT la.id,la.owner_account_id,la.code,la.kind,la.asset_code,la.normal_balance,la.status,COALESCE(b.balance_microcredits,0) AS posted_balance_microcredits FROM ledger_accounts la LEFT JOIN account_balances b ON b.account_id=la.owner_account_id`
		orderBy = "la.code,la.id"
		add(query.OwnerAccountID, "la.owner_account_id=?")
		add(query.Kind, "la.kind=?")
	case "ledger_transactions":
		base = `SELECT id,transaction_type,status,description,reference_type,reference_id,created_at,posted_at FROM ledger_transactions`
		add(query.TransactionType, "transaction_type=?")
		add(query.ReferenceID, "reference_id=?")
		add(query.From, "created_at>=?")
		add(query.To, "created_at<?")
		orderBy = "created_at,id"
	case "webhook_deliveries":
		base = `SELECT d.id,d.event_id,d.endpoint_id,v.event_type,e.merchant_account_id,d.attempt,d.status,d.response_status,d.error,d.created_at FROM webhook_deliveries d JOIN webhook_events v ON v.id=d.event_id JOIN webhook_endpoints e ON e.id=d.endpoint_id`
		orderBy = "d.created_at,d.attempt,d.id"
		add(query.MerchantID, "e.merchant_account_id=?")
		add(query.Status, "d.status=?")
	case "audit_events":
		base = `SELECT id,actor_type,actor_id,action,resource_type,resource_id,reason,request_id,metadata,created_at FROM audit_events`
		add(query.ActorID, "actor_id=?")
		add(query.Action, "action=?")
		add(query.ResourceType, "resource_type=?")
		add(query.ResourceID, "resource_id=?")
		add(query.From, "created_at>=?")
		add(query.To, "created_at<?")
		orderBy = "sequence"
	default:
		return AdminPage[map[string]any]{}, errors.New("unsupported admin query")
	}
	return s.adminMapPage(ctx, base, orderBy, conditions, args, query)
}

func (s *Store) adminMapPage(ctx context.Context, base, orderBy string, conditions []string, args []any, query AdminListQuery) (AdminPage[map[string]any], error) {
	if len(conditions) != 0 {
		base += " WHERE " + strings.Join(conditions, " AND ")
	}
	limit := normalizedAdminLimit(query.Limit)
	offset, err := adminCursorOffset(query.Cursor)
	if err != nil {
		return AdminPage[map[string]any]{}, err
	}
	base += " ORDER BY " + orderBy + " LIMIT ? OFFSET ?"
	args = append(args, limit+1, offset)
	rows, err := s.db.QueryxContext(ctx, base, args...)
	if err != nil {
		return AdminPage[map[string]any]{}, fmt.Errorf("query Admin page: %w", err)
	}
	defer rows.Close()
	items := make([]map[string]any, 0, limit+1)
	for rows.Next() {
		item := map[string]any{}
		if err := rows.MapScan(item); err != nil {
			return AdminPage[map[string]any]{}, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return AdminPage[map[string]any]{}, err
	}
	page := AdminPage[map[string]any]{Items: items}
	if len(items) > limit {
		page.HasMore = true
		page.Items = items[:limit]
		cursor := strconv.Itoa(offset + limit)
		page.NextCursor = &cursor
	}
	return page, nil
}

func adminCursorOffset(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	offset, err := strconv.Atoi(cursor)
	if err != nil || offset < 0 {
		return 0, errors.New("invalid Admin page cursor")
	}
	return offset, nil
}
