package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/idy/gizway/internal/store"
	"github.com/idy/gizway/internal/timetext"
)

func (s *Server) registerAdminRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /admin/v1/auth/login", s.loginAdministrator)
	mux.Handle("POST /admin/v1/auth/refresh", s.requireAdmin(http.HandlerFunc(s.refreshAdministratorSession)))
	mux.Handle("POST /admin/v1/auth/logout", s.requireAdmin(http.HandlerFunc(s.logoutAdministrator)))
	mux.Handle("GET /admin/v1/me", s.requireAdmin(http.HandlerFunc(s.getCurrentAdministrator)))
	mux.Handle("GET /admin/v1/administrators", s.requireAdmin(http.HandlerFunc(s.listAdministrators)))
	mux.Handle("POST /admin/v1/administrators", s.requireAdmin(http.HandlerFunc(s.createAdministrator)))
	mux.Handle("GET /admin/v1/administrators/{administrator_id}", s.requireAdmin(http.HandlerFunc(s.getAdministrator)))
	mux.Handle("PATCH /admin/v1/administrators/{administrator_id}", s.requireAdmin(http.HandlerFunc(s.updateAdministrator)))
	mux.Handle("GET /admin/v1/administrators/{administrator_id}/api_keys", s.requireAdmin(http.HandlerFunc(s.listAdministratorAPIKeys)))
	mux.Handle("POST /admin/v1/administrators/{administrator_id}/api_keys", s.requireAdmin(http.HandlerFunc(s.createAdministratorAPIKey)))
	mux.Handle("POST /admin/v1/administrators/{administrator_id}/api_keys/{admin_api_key_id}/revoke", s.requireAdmin(http.HandlerFunc(s.revokeAdministratorAPIKey)))
	mux.Handle("GET /admin/v1/overview", s.requireAdmin(http.HandlerFunc(s.getAdminOverview)))
	mux.Handle("GET /admin/v1/users", s.requireAdmin(http.HandlerFunc(s.adminListUsers)))
	mux.Handle("GET /admin/v1/users/{user_id}", s.requireAdmin(http.HandlerFunc(s.adminGetUser)))
	mux.Handle("POST /admin/v1/users/{user_id}/status", s.requireAdmin(http.HandlerFunc(s.changeUserStatus)))
	mux.Handle("GET /admin/v1/merchants", s.requireAdmin(http.HandlerFunc(s.adminListMerchants)))
	mux.Handle("GET /admin/v1/merchants/{account_id}", s.requireAdmin(http.HandlerFunc(s.adminGetMerchant)))
	mux.Handle("POST /admin/v1/merchants/{account_id}/decision", s.requireAdmin(http.HandlerFunc(s.decideMerchant)))
	mux.Handle("POST /admin/v1/merchant_services/{service_id}/decision", s.requireAdmin(http.HandlerFunc(s.decideMerchantService)))
	mux.Handle("GET /admin/v1/providers", s.requireAdmin(http.HandlerFunc(s.listProviders)))
	mux.Handle("POST /admin/v1/providers", s.requireAdmin(http.HandlerFunc(s.createProvider)))
	mux.Handle("PATCH /admin/v1/providers/{provider_id}", s.requireAdmin(http.HandlerFunc(s.updateProvider)))
	mux.Handle("GET /admin/v1/providers/{provider_id}/endpoints", s.requireAdmin(http.HandlerFunc(s.listProviderEndpoints)))
	mux.Handle("POST /admin/v1/providers/{provider_id}/endpoints", s.requireAdmin(http.HandlerFunc(s.createProviderEndpoint)))
	mux.Handle("PATCH /admin/v1/provider_endpoints/{endpoint_id}", s.requireAdmin(http.HandlerFunc(s.updateProviderEndpoint)))
	mux.Handle("POST /admin/v1/provider_endpoints/{endpoint_id}/rotate_credential", s.requireAdmin(http.HandlerFunc(s.rotateProviderCredential)))
	mux.Handle("GET /admin/v1/api_keys", s.requireAdmin(http.HandlerFunc(s.adminListAPIKeys)))
	mux.Handle("POST /admin/v1/api_keys/{api_key_id}/revoke", s.requireAdmin(http.HandlerFunc(s.adminRevokeAPIKey)))
	mux.Handle("GET /admin/v1/gateway_requests", s.requireAdmin(http.HandlerFunc(s.adminListGatewayRequests)))
	mux.Handle("GET /admin/v1/gateway_requests/{request_id}", s.requireAdmin(http.HandlerFunc(s.adminGetGatewayRequest)))
	mux.Handle("GET /admin/v1/payments", s.requireAdmin(http.HandlerFunc(s.adminListPayments)))
	mux.Handle("GET /admin/v1/ledger/accounts", s.requireAdmin(http.HandlerFunc(s.adminListLedgerAccounts)))
	mux.Handle("POST /admin/v1/accounts/{account_id}/balance_status", s.requireAdmin(http.HandlerFunc(s.changeAccountBalanceStatus)))
	mux.Handle("PUT /admin/v1/accounts/{account_id}/model_entitlements/{model_id}", s.requireAdmin(http.HandlerFunc(s.setAccountModelEntitlement)))
	mux.Handle("GET /admin/v1/ledger/transactions", s.requireAdmin(http.HandlerFunc(s.adminListLedgerTransactions)))
	mux.Handle("POST /admin/v1/ledger/adjustments", s.requireAdmin(http.HandlerFunc(s.createLedgerAdjustment)))
	mux.Handle("POST /admin/v1/ledger/transactions/{transaction_id}/reverse", s.requireAdmin(http.HandlerFunc(s.reverseLedgerTransaction)))
	mux.Handle("GET /admin/v1/webhook_deliveries", s.requireAdmin(http.HandlerFunc(s.adminListWebhookDeliveries)))
	mux.Handle("POST /admin/v1/webhook_deliveries/{delivery_id}/retry", s.requireAdmin(http.HandlerFunc(s.retryWebhookDelivery)))
	mux.Handle("GET /admin/v1/audit_events", s.requireAdmin(http.HandlerFunc(s.adminListAuditEvents)))
}

func randomSecret(prefix string) (string, []byte, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, err
	}
	secret := prefix + base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256([]byte(secret))
	return secret, hash[:], nil
}
func (s *Server) nowText() string { return timetext.Format(s.now()) }

func parseAdminListQuery(r *http.Request) (store.AdminListQuery, error) {
	values := r.URL.Query()
	query := store.AdminListQuery{
		Cursor: values.Get("cursor"), Query: strings.TrimSpace(values.Get("query")), Status: values.Get("status"),
		AccountID: values.Get("account_id"), APIKeyID: values.Get("api_key_id"), ModelID: values.Get("model_id"),
		KeyPrefix: values.Get("key_prefix"), Kind: values.Get("kind"), Type: values.Get("type"),
		OwnerAccountID: values.Get("owner_account_id"), TransactionType: values.Get("transaction_type"),
		ReferenceID: values.Get("reference_id"), MerchantID: values.Get("merchant_account_id"),
		ActorID: values.Get("actor_user_id"), Action: values.Get("action"), ResourceType: values.Get("resource_type"),
		ResourceID: values.Get("resource_id"), From: values.Get("from"), To: values.Get("to"),
	}
	if len(query.Cursor) > 255 {
		return query, errors.New("cursor is too long")
	}
	if query.Cursor != "" {
		offset, err := strconv.Atoi(query.Cursor)
		if err != nil || offset < 0 {
			return query, errors.New("cursor is invalid")
		}
	}
	if raw := values.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 100 {
			return query, errors.New("limit must be between 1 and 100")
		}
		query.Limit = limit
	}
	return query, nil
}

func writeAdminPage[T any](w http.ResponseWriter, result store.AdminPage[T]) {
	writeJSON(w, http.StatusOK, map[string]any{
		"data": result.Items,
		"page": map[string]any{"next_cursor": result.NextCursor, "has_more": result.HasMore},
	})
}

func (s *Server) changeAccountBalanceStatus(w http.ResponseWriter, r *http.Request) {
	if !requireIdempotencyKey(w, r) {
		return
	}
	var request struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	if err := decodeJSON(r, &request); err != nil || strings.TrimSpace(request.Reason) == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "status and reason are required")
		return
	}
	item, err := s.store.ChangeAccountBalanceStatus(r.Context(), contextString(r.Context(), administratorIDKey), r.PathValue("account_id"), request.Status, request.Reason, s.nowText())
	if err != nil {
		switch {
		case errors.Is(err, store.ErrRiskDenied):
			writeError(w, http.StatusBadRequest, "invalid_request", "status must be active or frozen")
		default:
			writeDataError(w, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) setAccountModelEntitlement(w http.ResponseWriter, r *http.Request) {
	if !requireIdempotencyKey(w, r) {
		return
	}
	var request struct {
		Effect string `json:"effect"`
		Reason string `json:"reason"`
	}
	if decodeJSON(r, &request) != nil || (request.Effect != "allow" && request.Effect != "deny") || strings.TrimSpace(request.Reason) == "" || len(request.Reason) > 500 {
		writeError(w, http.StatusBadRequest, "invalid_request", "effect must be allow or deny and reason is required")
		return
	}
	row, err := s.store.SetAccountModelEntitlement(r.Context(), contextString(r.Context(), administratorIDKey), r.PathValue("account_id"), r.PathValue("model_id"), request.Effect, strings.TrimSpace(request.Reason), s.nowText())
	if err != nil {
		writeDataError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (s *Server) loginAdministrator(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if decodeJSON(r, &request) != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "email and password are required")
		return
	}
	if !s.consumeRateLimit(w, r, authenticationRateLimitScope(request.Email), "admin.login", 20, 5) {
		return
	}
	secret, hash, err := randomSecret("gizadms_")
	if err != nil {
		writeDataError(w, err)
		return
	}
	created := s.now().UTC()
	expires := created.Add(8 * time.Hour)
	admin, err := s.store.LoginAdministrator(r.Context(), request.Email, request.Password, uuid.NewString(), hash, timetext.Format(created), timetext.Format(expires))
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "invalid administrator credentials")
		return
	}
	writeJSON(w, http.StatusOK, sessionResponse("administrator", admin, secret, timetext.Format(expires)))
}
func (s *Server) getCurrentAdministrator(w http.ResponseWriter, r *http.Request) {
	admin, err := s.store.GetAdministrator(r.Context(), contextString(r.Context(), administratorIDKey))
	if err != nil {
		writeDataError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, admin)
}
func (s *Server) listAdministrators(w http.ResponseWriter, r *http.Request) {
	query, err := parseAdminListQuery(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	rows, err := s.store.ListAdministratorsPage(r.Context(), query)
	if err != nil {
		writeDataError(w, err)
		return
	}
	writeAdminPage(w, rows)
}
func (s *Server) getAdministrator(w http.ResponseWriter, r *http.Request) {
	row, err := s.store.GetAdministrator(r.Context(), r.PathValue("administrator_id"))
	if err != nil {
		writeDataError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}
func (s *Server) createAdministrator(w http.ResponseWriter, r *http.Request) {
	if !requireIdempotencyKey(w, r) {
		return
	}
	var request struct {
		Email       string `json:"email"`
		DisplayName string `json:"display_name"`
		Password    string `json:"password"`
	}
	if decodeJSON(r, &request) != nil || request.Email == "" || len(request.Password) < 12 {
		writeError(w, http.StatusBadRequest, "invalid_request", "valid email, name, and 12-character password are required")
		return
	}
	row, err := s.store.CreateAdministrator(r.Context(), contextString(r.Context(), administratorIDKey), request.Email, request.DisplayName, request.Password, s.nowText())
	if err != nil {
		writeConflictOrDataError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, row)
}
func (s *Server) updateAdministrator(w http.ResponseWriter, r *http.Request) {
	var request struct {
		DisplayName string `json:"display_name"`
		Status      string `json:"status"`
		Reason      string `json:"reason"`
		Password    string `json:"password"`
	}
	if decodeJSON(r, &request) != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid body")
		return
	}
	if request.DisplayName == "" && request.Status == "" && request.Password == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "at least one administrator field is required")
		return
	}
	if request.Password != "" && len(request.Password) < 12 {
		writeError(w, http.StatusBadRequest, "invalid_request", "password must contain at least 12 characters")
		return
	}
	if request.Status != "" && request.Status != "active" && request.Status != "suspended" && request.Status != "closed" {
		writeError(w, http.StatusBadRequest, "invalid_request", "status must be active, suspended, or closed")
		return
	}
	row, err := s.store.UpdateAdministrator(r.Context(), contextString(r.Context(), administratorIDKey), r.PathValue("administrator_id"), request.DisplayName, request.Status, request.Password, request.Reason, s.nowText())
	if err != nil {
		if errors.Is(err, store.ErrIdempotencyConflict) {
			writeError(w, http.StatusConflict, "last_active_administrator", "final active administrator cannot be suspended or closed")
		} else {
			writeDataError(w, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, row)
}
func (s *Server) listAdministratorAPIKeys(w http.ResponseWriter, r *http.Request) {
	rows, err := s.store.ListAdminAPIKeys(r.Context(), r.PathValue("administrator_id"))
	if err != nil {
		writeDataError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": rows})
}
func (s *Server) createAdministratorAPIKey(w http.ResponseWriter, r *http.Request) {
	if !requireIdempotencyKey(w, r) {
		return
	}
	var request struct {
		Name      string  `json:"name"`
		ExpiresAt *string `json:"expires_at"`
	}
	if decodeJSON(r, &request) != nil || strings.TrimSpace(request.Name) == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "name is required")
		return
	}
	if request.ExpiresAt != nil {
		expires, err := timetext.Parse(*request.ExpiresAt)
		if err != nil || !expires.After(s.now()) {
			writeError(w, http.StatusBadRequest, "invalid_request", "expires_at must be a future RFC3339 timestamp")
			return
		}
		canonical := timetext.Format(expires)
		request.ExpiresAt = &canonical
	}
	payload, _ := json.Marshal(request)
	payloadHash := sha256.Sum256(payload)
	secret, secretHash, err := randomSecret("gizadm_")
	if err != nil {
		writeDataError(w, err)
		return
	}
	key := store.AdminAPIKey{ID: uuid.NewString(), AdministratorID: r.PathValue("administrator_id"), Name: request.Name, KeyPrefix: secret[:12], Status: "active", ExpiresAt: request.ExpiresAt, CreatedAt: s.nowText()}
	created, replayed, err := s.store.CreateAdminAPIKey(r.Context(), contextString(r.Context(), administratorIDKey), r.Header.Get("Idempotency-Key"), payloadHash[:], secretHash, key)
	if err != nil {
		writeConflictOrDataError(w, err)
		return
	}
	if replayed {
		writeJSON(w, http.StatusOK, created)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": created.ID, "administrator_id": created.AdministratorID, "name": created.Name, "key_prefix": created.KeyPrefix, "status": created.Status, "expires_at": created.ExpiresAt, "last_used_at": created.LastUsedAt, "created_at": created.CreatedAt, "secret": secret})
}
func (s *Server) revokeAdministratorAPIKey(w http.ResponseWriter, r *http.Request) {
	if !requireIdempotencyKey(w, r) {
		return
	}
	var request struct {
		Reason string `json:"reason"`
	}
	_ = decodeJSON(r, &request)
	if err := s.store.RevokeAdminAPIKey(r.Context(), contextString(r.Context(), administratorIDKey), r.PathValue("administrator_id"), r.PathValue("admin_api_key_id"), request.Reason, s.nowText()); err != nil {
		writeDataError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) getAdminOverview(w http.ResponseWriter, r *http.Request) {
	row, err := s.store.AdminOverview(r.Context(), s.nowText())
	if err != nil {
		writeDataError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}
func (s *Server) adminListUsers(w http.ResponseWriter, r *http.Request) {
	query, err := parseAdminListQuery(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	rows, err := s.store.AdminListUsersPage(r.Context(), query)
	if err != nil {
		writeDataError(w, err)
		return
	}
	writeAdminPage(w, rows)
}
func (s *Server) adminGetUser(w http.ResponseWriter, r *http.Request) {
	row, err := s.store.AdminGetUser(r.Context(), r.PathValue("user_id"))
	if err != nil {
		writeDataError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}
func (s *Server) changeUserStatus(w http.ResponseWriter, r *http.Request) {
	if !requireIdempotencyKey(w, r) {
		return
	}
	var request struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	if decodeJSON(r, &request) != nil || !oneOf(request.Status, "active", "suspended", "closed") || request.Reason == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "status and reason are required")
		return
	}
	row, err := s.store.ChangeUserStatus(r.Context(), contextString(r.Context(), administratorIDKey), r.PathValue("user_id"), request.Status, request.Reason, s.nowText())
	if err != nil {
		writeDataError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}
func (s *Server) adminListMerchants(w http.ResponseWriter, r *http.Request) {
	query, err := parseAdminListQuery(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	rows, err := s.store.ListMerchantsPage(r.Context(), query)
	if err != nil {
		writeDataError(w, err)
		return
	}
	writeAdminPage(w, rows)
}
func (s *Server) adminGetMerchant(w http.ResponseWriter, r *http.Request) {
	row, err := s.store.GetMerchant(r.Context(), r.PathValue("account_id"))
	if err != nil {
		writeDataError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}
func (s *Server) decideMerchant(w http.ResponseWriter, r *http.Request) {
	if !requireIdempotencyKey(w, r) {
		return
	}
	var request struct {
		Decision    string `json:"decision"`
		ReviewLevel string `json:"review_level"`
		Reason      string `json:"reason"`
	}
	if decodeJSON(r, &request) != nil || request.Reason == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "decision, review_level and reason are required")
		return
	}
	row, err := s.store.DecideMerchant(r.Context(), contextString(r.Context(), administratorIDKey), r.PathValue("account_id"), request.Decision, request.ReviewLevel, request.Reason, s.nowText())
	if err != nil {
		writeConflictOrDataError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (s *Server) decideMerchantService(w http.ResponseWriter, r *http.Request) {
	if !requireIdempotencyKey(w, r) {
		return
	}
	var request struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if decodeJSON(r, &request) != nil || request.Reason == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "decision and reason are required")
		return
	}
	service, err := s.store.DecideMerchantService(r.Context(), contextString(r.Context(), administratorIDKey), r.PathValue("service_id"), request.Decision, request.Reason, s.nowText())
	if err != nil {
		if errors.Is(err, store.ErrRiskDenied) {
			writeError(w, http.StatusConflict, "risk_denied", "risk decision does not permit approval")
		} else {
			writeConflictOrDataError(w, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, service)
}

func (s *Server) listProviders(w http.ResponseWriter, r *http.Request) {
	rows, err := s.store.ListProviders(r.Context())
	if err != nil {
		writeDataError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": rows})
}
func (s *Server) createProvider(w http.ResponseWriter, r *http.Request) {
	if !requireIdempotencyKey(w, r) {
		return
	}
	var request struct {
		Slug string `json:"slug"`
		Name string `json:"name"`
	}
	if decodeJSON(r, &request) != nil || request.Slug == "" || request.Name == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "slug and name are required")
		return
	}
	row, err := s.store.CreateProvider(r.Context(), contextString(r.Context(), administratorIDKey), request.Slug, request.Name, s.nowText())
	if err != nil {
		writeConflictOrDataError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, row)
}
func (s *Server) updateProvider(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	}
	if decodeJSON(r, &request) != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid body")
		return
	}
	row, err := s.store.UpdateProvider(r.Context(), contextString(r.Context(), administratorIDKey), r.PathValue("provider_id"), request.Name, request.Status, s.nowText())
	if err != nil {
		writeDataError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}
func (s *Server) listProviderEndpoints(w http.ResponseWriter, r *http.Request) {
	rows, err := s.store.ListProviderEndpoints(r.Context(), r.PathValue("provider_id"))
	if err != nil {
		writeDataError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": rows})
}
func (s *Server) createProviderEndpoint(w http.ResponseWriter, r *http.Request) {
	if !requireIdempotencyKey(w, r) {
		return
	}
	var request struct {
		Name       string  `json:"name"`
		BaseURL    string  `json:"base_url"`
		Credential string  `json:"credential"`
		Region     *string `json:"region"`
		Priority   *int    `json:"priority"`
		Weight     *int    `json:"weight"`
	}
	if decodeJSON(r, &request) != nil || strings.TrimSpace(request.Credential) == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "endpoint and credential are required")
		return
	}
	priority := 100
	if request.Priority != nil {
		priority = *request.Priority
	}
	weight := 100
	if request.Weight != nil {
		weight = *request.Weight
	}
	if err := validateProviderEndpoint(request.Name, request.BaseURL, "active", request.Region, priority, weight, true); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	row, err := s.store.CreateProviderEndpoint(r.Context(), contextString(r.Context(), administratorIDKey), r.PathValue("provider_id"), strings.TrimSpace(request.Name), request.BaseURL, request.Credential, request.Region, priority, weight, s.nowText())
	if err != nil {
		writeConflictOrDataError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, row)
}
func (s *Server) updateProviderEndpoint(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Name     string          `json:"name"`
		BaseURL  string          `json:"base_url"`
		Status   string          `json:"status"`
		Priority *int            `json:"priority"`
		Weight   *int            `json:"weight"`
		Region   json.RawMessage `json:"region"`
	}
	if decodeJSON(r, &request) != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid body")
		return
	}
	regionSet := len(request.Region) != 0
	var region *string
	if regionSet && string(request.Region) != "null" {
		var value string
		if json.Unmarshal(request.Region, &value) != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "region must be a string or null")
			return
		}
		region = &value
	}
	if request.Name == "" && request.BaseURL == "" && request.Status == "" && request.Priority == nil && request.Weight == nil && !regionSet {
		writeError(w, http.StatusBadRequest, "invalid_request", "at least one endpoint field is required")
		return
	}
	priority, weight := 0, 1
	if request.Priority != nil {
		priority = *request.Priority
	}
	if request.Weight != nil {
		weight = *request.Weight
	}
	if err := validateProviderEndpoint(request.Name, request.BaseURL, request.Status, region, priority, weight, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	row, err := s.store.UpdateProviderEndpoint(r.Context(), contextString(r.Context(), administratorIDKey), r.PathValue("endpoint_id"), strings.TrimSpace(request.Name), request.BaseURL, request.Status, region, regionSet, request.Priority, request.Weight, s.nowText())
	if err != nil {
		writeDataError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func validateProviderEndpoint(name, baseURL, status string, region *string, priority, weight int, create bool) error {
	if (create || name != "") && (strings.TrimSpace(name) == "" || len(strings.TrimSpace(name)) > 120) {
		return errors.New("endpoint name must contain 1 to 120 characters")
	}
	if create || baseURL != "" {
		parsed, err := url.Parse(baseURL)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.User != nil {
			return errors.New("base_url must be an absolute HTTP(S) URL without userinfo")
		}
	}
	if status != "" && status != "active" && status != "draining" && status != "disabled" {
		return errors.New("status must be active, draining, or disabled")
	}
	if priority < 0 || priority > 1_000_000 {
		return errors.New("priority must be between 0 and 1000000")
	}
	if weight <= 0 || weight > 1_000_000 {
		return errors.New("weight must be between 1 and 1000000")
	}
	if region != nil {
		trimmed := strings.TrimSpace(*region)
		if trimmed == "" || len(trimmed) > 64 {
			return errors.New("region must contain 1 to 64 characters when present")
		}
		*region = trimmed
	}
	return nil
}
func (s *Server) rotateProviderCredential(w http.ResponseWriter, r *http.Request) {
	if !requireIdempotencyKey(w, r) {
		return
	}
	var request struct {
		Credential string `json:"credential"`
	}
	if decodeJSON(r, &request) != nil || request.Credential == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "credential is required")
		return
	}
	if err := s.store.RotateProviderCredential(r.Context(), contextString(r.Context(), administratorIDKey), r.PathValue("endpoint_id"), request.Credential, s.nowText()); err != nil {
		writeDataError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminListAPIKeys(w http.ResponseWriter, r *http.Request) {
	query, err := parseAdminListQuery(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	rows, err := s.store.AdminListAPIKeysPage(r.Context(), query)
	if err != nil {
		writeDataError(w, err)
		return
	}
	writeAdminPage(w, rows)
}
func reasonBody(r *http.Request) string {
	var request struct {
		Reason string `json:"reason"`
	}
	_ = decodeJSON(r, &request)
	return request.Reason
}
func (s *Server) adminRevokeAPIKey(w http.ResponseWriter, r *http.Request) {
	if !requireIdempotencyKey(w, r) {
		return
	}
	if err := s.store.AdminRevokeAPIKey(r.Context(), contextString(r.Context(), administratorIDKey), r.PathValue("api_key_id"), reasonBody(r), s.nowText()); err != nil {
		writeDataError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) adminRows(w http.ResponseWriter, r *http.Request, kind string) {
	query, err := parseAdminListQuery(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	rows, err := s.store.AdminRowsPage(r.Context(), kind, query)
	if err != nil {
		writeDataError(w, err)
		return
	}
	writeAdminPage(w, rows)
}
func (s *Server) adminListGatewayRequests(w http.ResponseWriter, r *http.Request) {
	s.adminRows(w, r, "gateway_requests")
}
func (s *Server) adminGetGatewayRequest(w http.ResponseWriter, r *http.Request) {
	rows, err := s.store.AdminRows(r.Context(), "gateway_requests", r.PathValue("request_id"))
	if err != nil || len(rows) == 0 {
		writeDataError(w, store.ErrNotFound)
		return
	}
	writeJSON(w, http.StatusOK, rows[0])
}
func (s *Server) adminListPayments(w http.ResponseWriter, r *http.Request) {
	s.adminRows(w, r, "payments")
}
func (s *Server) adminListLedgerAccounts(w http.ResponseWriter, r *http.Request) {
	s.adminRows(w, r, "ledger_accounts")
}
func (s *Server) adminListLedgerTransactions(w http.ResponseWriter, r *http.Request) {
	s.adminRows(w, r, "ledger_transactions")
}
func (s *Server) adminListWebhookDeliveries(w http.ResponseWriter, r *http.Request) {
	s.adminRows(w, r, "webhook_deliveries")
}
func (s *Server) adminListAuditEvents(w http.ResponseWriter, r *http.Request) {
	s.adminRows(w, r, "audit_events")
}
func (s *Server) createLedgerAdjustment(w http.ResponseWriter, r *http.Request) {
	if !requireIdempotencyKey(w, r) {
		return
	}
	var request struct {
		Description string                   `json:"description"`
		Reason      string                   `json:"reason"`
		Entries     []store.LedgerEntryInput `json:"entries"`
	}
	if decodeJSON(r, &request) != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid adjustment")
		return
	}
	row, err := s.store.CreateLedgerAdjustment(r.Context(), contextString(r.Context(), administratorIDKey), r.Header.Get("Idempotency-Key"), request.Description, request.Reason, s.nowText(), request.Entries)
	if err != nil {
		writeError(w, http.StatusBadRequest, "unbalanced_adjustment", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, row)
}
func (s *Server) reverseLedgerTransaction(w http.ResponseWriter, r *http.Request) {
	if !requireIdempotencyKey(w, r) {
		return
	}
	row, err := s.store.ReverseLedgerTransaction(r.Context(), contextString(r.Context(), administratorIDKey), r.PathValue("transaction_id"), r.Header.Get("Idempotency-Key"), reasonBody(r), s.nowText())
	if err != nil {
		writeConflictOrDataError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, row)
}
func (s *Server) retryWebhookDelivery(w http.ResponseWriter, r *http.Request) {
	if !requireIdempotencyKey(w, r) {
		return
	}
	id, err := s.store.RetryWebhookDelivery(r.Context(), contextString(r.Context(), administratorIDKey), r.PathValue("delivery_id"), r.Header.Get("Idempotency-Key"), s.nowText())
	if err != nil {
		writeConflictOrDataError(w, err)
		return
	}
	if s.merchant != nil {
		_ = s.merchant.Deliver(r.Context(), id)
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"delivery_id": id})
}

var _ = errors.Is
