package gizway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	bifrostadapter "github.com/idy/gizway/internal/adapter/bifrost"
	"github.com/idy/gizway/internal/identity"
)

type Config struct {
	DB                      *sqlx.DB
	Verifier                *identity.Verifier
	AdminAudience           string
	HMACSecret              []byte
	GizPayURL               string
	ServiceToken            func(context.Context) (string, error)
	HTTPClient              *http.Client
	Now                     func() time.Time
	OutboxBatchSize         int
	OutboxRetryInterval     time.Duration
	OutboxAbandonAfter      time.Duration
	BifrostMaxRetries       int
	BifrostRequestTimeout   time.Duration
	ObserveDependency       func(string, error)
	BifrostStores           *bifrostadapter.Stores
	Logger                  *slog.Logger
	ProviderCallbackBaseURL string
	ProviderCallbackSecret  []byte
}

type creditState struct {
	available int64
	productID string
	billing   string
	expires   time.Time
	loading   bool
	wait      chan struct{}
}

type Handler struct {
	config     Config
	engine     *bifrostadapter.Adapter
	stores     *bifrostadapter.Stores
	creditMu   sync.Mutex
	credits    map[string]*creditState
	realtimeMu sync.Mutex
	realtime   map[string]realtimeSession
	stopOnce   sync.Once
	stop       chan struct{}
	done       chan struct{}
}

func (h *Handler) observeDependency(name string, err error) {
	if h.config.ObserveDependency != nil {
		h.config.ObserveDependency(name, err)
	}
}

func New(config Config) (*Handler, error) {
	if config.DB == nil || config.Verifier == nil || config.AdminAudience == "" || config.BifrostStores == nil {
		return nil, errors.New("incomplete GizWay handler configuration")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	if config.OutboxBatchSize <= 0 {
		config.OutboxBatchSize = 20
	}
	if config.OutboxRetryInterval <= 0 {
		config.OutboxRetryInterval = 250 * time.Millisecond
	}
	if config.OutboxAbandonAfter <= 0 {
		config.OutboxAbandonAfter = 24 * time.Hour
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	engine := bifrostadapter.NewLazyWithExecution(config.BifrostMaxRetries, config.BifrostRequestTimeout)
	engine.ConfigureProviderCallbacks(config.ProviderCallbackBaseURL, config.ProviderCallbackSecret)
	handler := &Handler{
		config: config, engine: engine, stores: config.BifrostStores, credits: make(map[string]*creditState), realtime: make(map[string]realtimeSession),
		stop: make(chan struct{}), done: make(chan struct{}),
	}
	_, _ = config.DB.Exec(`UPDATE charge_outbox SET status='pending',recover_duplicate=true,updated_at=$1 WHERE status='sending'`, config.Now().UTC())
	go handler.runOutbox()
	return handler, nil
}

func (h *Handler) diagnostic(message string, err error, attributes ...any) {
	if err != nil {
		attributes = append(attributes, "error", err.Error())
	}
	h.config.Logger.Error(message, attributes...)
}

func (h *Handler) runOutbox() {
	defer close(h.done)
	retryInterval := h.config.OutboxRetryInterval
	if retryInterval <= 0 {
		retryInterval = 250 * time.Millisecond
	}
	abandonAfter := h.config.OutboxAbandonAfter
	if abandonAfter <= 0 {
		abandonAfter = 24 * time.Hour
	}
	batchSize := h.config.OutboxBatchSize
	if batchSize <= 0 {
		batchSize = 20
	}
	ticker := time.NewTicker(retryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-h.stop:
			return
		case <-ticker.C:
		}
		var ids []string
		_, _ = h.config.DB.Exec(`UPDATE charge_outbox SET status='abandoned',updated_at=$1 WHERE status='pending' AND created_at < $2`, h.config.Now().UTC(), h.config.Now().UTC().Add(-abandonAfter))
		// A failed local state transition after the remote request can leave a row
		// in sending. This worker is single-threaded, so reclaiming both states is
		// safe and avoids requiring a process restart to resume recovery.
		if h.config.DB.Select(&ids, `SELECT external_order_id FROM charge_outbox WHERE status IN('pending','sending') ORDER BY created_at LIMIT $1`, batchSize) != nil {
			continue
		}
		for _, id := range ids {
			h.reportOutbox(id)
		}
	}
}

// Close stops the background Outbox worker before its database is closed.
func (h *Handler) Close() error {
	h.stopOnce.Do(func() { close(h.stop) })
	<-h.done
	return nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, "/admin/") {
		h.protocol(w, r)
		return
	}
	principal, err := h.config.Verifier.AuthenticateAny(r)
	if err != nil {
		if errors.Is(err, identity.ErrInvalidIssuer) {
			errJSON(w, 401, "invalid_token_issuer", "invalid token issuer")
		} else if strings.Count(r.Header.Get("Authorization"), ".") < 2 {
			errJSON(w, 401, "invalid_bearer_token", "invalid bearer token")
		} else {
			errJSON(w, 401, "invalid_token", "invalid token")
		}
		return
	}
	var status string
	lookupErr := h.config.DB.Get(&status, `SELECT status FROM administrators WHERE identity_issuer=$1 AND identity_subject=$2`, principal.Issuer, principal.Subject)
	if !principal.Audiences[h.config.AdminAudience] {
		if lookupErr == nil {
			errJSON(w, 401, "invalid_token_audience", "invalid token audience")
		} else if principal.HasRoleInAnyProject("administrator") {
			errJSON(w, 403, "administrator_region_forbidden", "administrator belongs to another region")
		} else {
			errJSON(w, 403, "administrator_required", "administrator required")
		}
		return
	}
	if lookupErr != nil {
		errJSON(w, 403, "administrator_required", "administrator required")
		return
	}
	if !principal.HasRole(h.config.AdminAudience, "administrator") {
		errJSON(w, 403, "administrator_required", "administrator required")
		return
	}
	if status != "active" {
		errJSON(w, 403, "administrator_inactive", "administrator inactive")
		return
	}
	path := r.URL.Path
	switch {
	case strings.HasPrefix(path, "/admin/v1/models"):
		h.models(w, r)
	case path == "/admin/v1/providers":
		h.providers(w, r)
	case strings.HasPrefix(path, "/admin/v1/providers/"):
		h.providerResource(w, r)
	case strings.HasPrefix(path, "/admin/v1/provider-api-keys/"):
		h.providerKeyResource(w, r)
	case path == "/admin/v1/ai-orders":
		h.listTable(w, `SELECT o.*,m.name AS model FROM ai_orders o JOIN models m ON m.id=o.model_id ORDER BY o.created_at,o.id`)
	case path == "/admin/v1/charge-outbox":
		h.listTable(w, `SELECT * FROM charge_outbox ORDER BY created_at,id`)
	case path == "/admin/v1/bifrost-logs":
		logs, logErr := h.stores.LogsList(r.Context())
		if logErr != nil {
			internal(w)
			return
		}
		writeJSON(w, 200, map[string]any{"data": logs})
	default:
		notFound(w)
	}
}

func (h *Handler) models(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("model_id")
	if id == "" {
		if r.Method == http.MethodGet {
			h.listTable(w, `SELECT * FROM models ORDER BY created_at,id`)
			return
		}
		var body struct {
			Name          string `json:"name"`
			ProviderID    string `json:"provider_id"`
			ProviderModel string `json:"provider_model"`
		}
		if decode(r, &body) != nil {
			invalid(w)
			return
		}
		if !nonBlank(body.Name) || !nonBlank(body.ProviderID) || !nonBlank(body.ProviderModel) {
			invalid(w)
			return
		}
		provider, providerErr := h.stores.Provider(r.Context(), body.ProviderID)
		if providerErr != nil || provider.Status != "active" {
			errJSON(w, 400, "invalid_provider", "Provider is not active")
			return
		}
		id = "mdl_" + uuid.NewString()
		now := h.config.Now().UTC()
		_, err := h.config.DB.Exec(`INSERT INTO models(id,name,provider_id,provider_model,status,created_at,updated_at) VALUES($1,$2,$3,$4,'active',$5,$5)`, id, body.Name, body.ProviderID, body.ProviderModel, now)
		if err != nil {
			internal(w)
			return
		}
		writeJSON(w, 201, map[string]any{"id": id, "name": body.Name, "provider_id": body.ProviderID, "provider_model": body.ProviderModel, "status": "active"})
		return
	}
	if strings.HasSuffix(r.URL.Path, "/prices") {
		h.modelPrices(w, r, id)
		return
	}
	row, err := h.one(`SELECT * FROM models WHERE id=$1`, id)
	if err != nil {
		notFound(w)
		return
	}
	if r.Method == http.MethodPatch {
		var body struct {
			Name   *string `json:"name"`
			Status *string `json:"status"`
		}
		if decode(r, &body) != nil {
			invalid(w)
			return
		}
		if body.Name == nil && body.Status == nil || body.Name != nil && !nonBlank(*body.Name) || body.Status != nil && *body.Status != "active" && *body.Status != "inactive" {
			invalid(w)
			return
		}
		if body.Name != nil {
			row["name"] = *body.Name
		}
		if body.Status != nil {
			row["status"] = *body.Status
		}
		if _, err = h.config.DB.Exec(`UPDATE models SET name=$1,status=$2,updated_at=$3 WHERE id=$4`, row["name"], row["status"], h.config.Now().UTC(), id); err != nil {
			invalid(w)
			return
		}
	}
	writeJSON(w, 200, row)
}

type price struct {
	Metric   string `json:"metric"`
	UnitSize int64  `json:"unit_size"`
	Price    int64  `json:"price_microcredits"`
}

func validMetric(metric string) bool {
	return metric == "input_token" || metric == "output_token"
}

func (h *Handler) modelPrices(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method == http.MethodGet {
		rows, err := h.many(`SELECT metric,unit_size,price_microcredits FROM model_customer_prices WHERE model_id=$1 ORDER BY metric`, id)
		if err != nil {
			internal(w)
			return
		}
		writeJSON(w, 200, map[string]any{"prices": rows})
		return
	}
	var body struct {
		Prices []price `json:"prices"`
	}
	if decode(r, &body) != nil || body.Prices == nil {
		invalid(w)
		return
	}
	tx, err := h.config.DB.Beginx()
	if err != nil {
		internal(w)
		return
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`DELETE FROM model_customer_prices WHERE model_id=$1`, id); err != nil {
		internal(w)
		return
	}
	for _, p := range body.Prices {
		if !validMetric(p.Metric) || p.UnitSize <= 0 || p.Price < 0 {
			invalid(w)
			return
		}
		if _, err = tx.Exec(`INSERT INTO model_customer_prices(model_id,metric,unit_size,price_microcredits) VALUES($1,$2,$3,$4)`, id, p.Metric, p.UnitSize, p.Price); err != nil {
			invalid(w)
			return
		}
	}
	if err = tx.Commit(); err != nil {
		internal(w)
		return
	}
	h.modelPrices(w, &http.Request{Method: http.MethodGet}, id)
}

func (h *Handler) providers(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		providers, err := h.stores.Providers(r.Context())
		if err != nil {
			internal(w)
			return
		}
		data := make([]map[string]any, 0, len(providers))
		for _, provider := range providers {
			data = append(data, map[string]any{"id": provider.ID, "name": provider.Name, "kind": provider.Kind, "base_url": provider.BaseURL, "status": provider.Status, "created_at": provider.CreatedAt})
		}
		writeJSON(w, 200, map[string]any{"data": data})
		return
	}
	var body struct {
		Name    string `json:"name"`
		Kind    string `json:"kind"`
		BaseURL string `json:"base_url"`
	}
	if decode(r, &body) != nil {
		invalid(w)
		return
	}
	id := "prv_" + uuid.NewString()
	if !nonBlank(body.Name) || !validProviderURL(body.BaseURL) || (body.Kind != "openai" && body.Kind != "anthropic" && body.Kind != "gemini") {
		invalid(w)
		return
	}
	err := h.stores.CreateProvider(r.Context(), bifrostadapter.ProviderRecord{ID: id, Name: body.Name, Kind: body.Kind, BaseURL: body.BaseURL, Status: "active", CreatedAt: h.config.Now().UTC()})
	if err != nil {
		internal(w)
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "name": body.Name, "kind": body.Kind, "base_url": body.BaseURL, "status": "active"})
}

func (h *Handler) providerResource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("provider_id")
	if strings.HasSuffix(r.URL.Path, "/api-keys") {
		h.providerKeys(w, r, id)
		return
	}
	provider, err := h.stores.Provider(r.Context(), id)
	if err != nil {
		notFound(w)
		return
	}
	if r.Method == http.MethodPatch {
		var body struct {
			Name    *string `json:"name"`
			BaseURL *string `json:"base_url"`
			Status  *string `json:"status"`
		}
		if decode(r, &body) != nil {
			invalid(w)
			return
		}
		if body.Name == nil && body.BaseURL == nil && body.Status == nil || body.Name != nil && !nonBlank(*body.Name) || body.BaseURL != nil && !validProviderURL(*body.BaseURL) || body.Status != nil && *body.Status != "active" && *body.Status != "inactive" {
			invalid(w)
			return
		}
		if body.Name != nil {
			provider.Name = *body.Name
		}
		if body.BaseURL != nil {
			provider.BaseURL = *body.BaseURL
		}
		if body.Status != nil {
			provider.Status = *body.Status
		}
		err = h.stores.UpdateProvider(r.Context(), provider)
		if err != nil {
			invalid(w)
			return
		}
	}
	writeJSON(w, 200, map[string]any{"id": provider.ID, "name": provider.Name, "kind": provider.Kind, "base_url": provider.BaseURL, "status": provider.Status})
}

type keyPrice struct {
	ModelID    string `json:"model_id"`
	Metric     string `json:"metric"`
	UnitSize   int64  `json:"unit_size"`
	Commission int64  `json:"commission_microcredits"`
}

func (h *Handler) providerKeys(w http.ResponseWriter, r *http.Request, providerID string) {
	if r.Method == http.MethodGet {
		keys, err := h.stores.Keys(r.Context(), providerID)
		if err != nil {
			internal(w)
			return
		}
		data := make([]map[string]any, 0, len(keys))
		for _, key := range keys {
			row := map[string]any{"bifrost_key_id": key.ID, "provider_id": key.ProviderID, "name": key.Name, "api_key": key.APIKey, "weight": key.Weight, "status": key.Status}
			var beneficiary string
			_ = h.config.DB.Get(&beneficiary, `SELECT beneficiary_merchant_id FROM provider_key_billing WHERE bifrost_key_id=$1`, key.ID)
			row["beneficiary_merchant_id"] = beneficiary
			data = append(data, row)
		}
		writeJSON(w, 200, map[string]any{"data": data})
		return
	}
	var body struct {
		Name        string     `json:"name"`
		APIKey      string     `json:"api_key"`
		Weight      int        `json:"weight"`
		Status      string     `json:"status"`
		Beneficiary string     `json:"beneficiary_merchant_id"`
		Prices      []keyPrice `json:"prices"`
	}
	if decode(r, &body) != nil || body.Prices == nil {
		invalid(w)
		return
	}
	if !nonBlank(body.Beneficiary) {
		errJSON(w, 400, "incomplete_provider_key_billing", "beneficiary Merchant is required")
		return
	}
	if !nonBlank(body.Name) || !nonBlank(body.APIKey) || body.Weight <= 0 || body.Status != "active" {
		invalid(w)
		return
	}
	for _, p := range body.Prices {
		if !nonBlank(p.ModelID) || !validMetric(p.Metric) || p.UnitSize <= 0 || p.Commission < 0 {
			invalid(w)
			return
		}
	}
	if _, err := h.stores.Provider(r.Context(), providerID); err != nil {
		notFound(w)
		return
	}
	id := "bfk_" + uuid.NewString()
	if err := h.stores.CreateKey(r.Context(), bifrostadapter.KeyRecord{ID: id, ProviderID: providerID, Name: body.Name, APIKey: body.APIKey, Weight: body.Weight, Enabled: false, Status: "inactive"}); err != nil {
		invalid(w)
		return
	}
	tx, err := h.config.DB.Beginx()
	if err != nil {
		internal(w)
		return
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT INTO provider_key_billing(bifrost_key_id,beneficiary_merchant_id,status) VALUES($1,$2,'active')`, id, body.Beneficiary); err != nil {
		invalid(w)
		return
	}
	for _, p := range body.Prices {
		if _, err = tx.Exec(`INSERT INTO provider_key_prices(bifrost_key_id,model_id,metric,unit_size,commission_microcredits) VALUES($1,$2,$3,$4,$5)`, id, p.ModelID, p.Metric, p.UnitSize, p.Commission); err != nil {
			invalid(w)
			return
		}
	}
	if err = tx.Commit(); err != nil {
		internal(w)
		return
	}
	if err = h.stores.UpdateKey(r.Context(), bifrostadapter.KeyRecord{ID: id, ProviderID: providerID, Name: body.Name, APIKey: body.APIKey, Weight: body.Weight, Enabled: true, Status: "active"}); err != nil {
		internal(w)
		return
	}
	writeJSON(w, 201, map[string]any{
		"bifrost_key_id": id, "provider_id": providerID, "name": body.Name,
		"api_key": body.APIKey, "weight": body.Weight, "status": "active",
		"beneficiary_merchant_id": body.Beneficiary, "prices": body.Prices,
	})
}

func (h *Handler) providerKeyResource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("bifrost_key_id")
	switch {
	case strings.HasSuffix(r.URL.Path, "/disable"):
		key, err := h.stores.FindKey(r.Context(), id)
		if err != nil {
			notFound(w)
			return
		}
		key.Enabled, key.Status = false, "disabled"
		if err := h.stores.UpdateKey(r.Context(), key); err != nil {
			internal(w)
			return
		}
		_, _ = h.config.DB.Exec(`UPDATE provider_key_billing SET status='inactive' WHERE bifrost_key_id=$1`, id)
		writeJSON(w, 200, map[string]any{"bifrost_key_id": id, "status": "disabled"})
	case strings.HasSuffix(r.URL.Path, "/billing"):
		h.keyBilling(w, r, id)
	case strings.HasSuffix(r.URL.Path, "/prices"):
		h.keyPrices(w, r, id)
	default:
		h.key(w, r, id)
	}
}

func (h *Handler) key(w http.ResponseWriter, r *http.Request, id string) {
	key, err := h.stores.FindKey(r.Context(), id)
	if err != nil {
		notFound(w)
		return
	}
	if r.Method == http.MethodPatch {
		var body struct {
			APIKey *string `json:"api_key"`
			Weight *int    `json:"weight"`
		}
		if decode(r, &body) != nil || body.APIKey == nil && body.Weight == nil {
			invalid(w)
			return
		}
		if body.APIKey != nil {
			if !nonBlank(*body.APIKey) {
				invalid(w)
				return
			}
			key.APIKey = *body.APIKey
		}
		if body.Weight != nil {
			if *body.Weight < 1 {
				invalid(w)
				return
			}
			key.Weight = *body.Weight
		}
		err = h.stores.UpdateKey(r.Context(), key)
		if err != nil {
			invalid(w)
			return
		}
	}
	var beneficiary string
	_ = h.config.DB.Get(&beneficiary, `SELECT beneficiary_merchant_id FROM provider_key_billing WHERE bifrost_key_id=$1`, id)
	var prices []keyPrice
	_ = h.config.DB.Select(&prices, `SELECT model_id,metric,unit_size,commission_microcredits FROM provider_key_prices WHERE bifrost_key_id=$1 ORDER BY model_id,metric`, id)
	writeJSON(w, 200, map[string]any{"bifrost_key_id": key.ID, "provider_id": key.ProviderID, "name": key.Name, "api_key": key.APIKey, "weight": key.Weight, "status": key.Status, "beneficiary_merchant_id": beneficiary, "prices": prices})
}

func (h *Handler) keyBilling(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method == http.MethodPut {
		var body struct {
			Beneficiary string `json:"beneficiary_merchant_id"`
			Status      string `json:"status"`
		}
		if decode(r, &body) != nil || !nonBlank(body.Beneficiary) || (body.Status != "active" && body.Status != "inactive") {
			invalid(w)
			return
		}
		key, err := h.stores.FindKey(r.Context(), id)
		if err != nil {
			notFound(w)
			return
		}
		key.Enabled, key.Status = false, "inactive"
		if err := h.stores.UpdateKey(r.Context(), key); err != nil {
			internal(w)
			return
		}
		result, err := h.config.DB.Exec(`UPDATE provider_key_billing SET beneficiary_merchant_id=$1,status=$2 WHERE bifrost_key_id=$3`, body.Beneficiary, body.Status, id)
		if err != nil {
			invalid(w)
			return
		}
		if affected, affectedErr := result.RowsAffected(); affectedErr != nil || affected != 1 {
			notFound(w)
			return
		}
		if body.Status == "active" {
			key.Enabled, key.Status = true, "active"
			if err := h.stores.UpdateKey(r.Context(), key); err != nil {
				internal(w)
				return
			}
		}
	}
	row, err := h.one(`SELECT bifrost_key_id,beneficiary_merchant_id,status FROM provider_key_billing WHERE bifrost_key_id=$1`, id)
	if err != nil {
		notFound(w)
		return
	}
	writeJSON(w, 200, row)
}

func (h *Handler) keyPrices(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method == http.MethodPut {
		var body struct {
			Prices []keyPrice `json:"prices"`
		}
		if decode(r, &body) != nil || body.Prices == nil {
			invalid(w)
			return
		}
		for _, p := range body.Prices {
			if !nonBlank(p.ModelID) || !validMetric(p.Metric) || p.UnitSize <= 0 || p.Commission < 0 {
				invalid(w)
				return
			}
		}
		key, err := h.stores.FindKey(r.Context(), id)
		if err != nil {
			notFound(w)
			return
		}
		wasActive := key.Enabled && key.Status == "active"
		key.Enabled, key.Status = false, "inactive"
		if err := h.stores.UpdateKey(r.Context(), key); err != nil {
			internal(w)
			return
		}
		tx, err := h.config.DB.Beginx()
		if err != nil {
			internal(w)
			return
		}
		defer tx.Rollback()
		_, _ = tx.Exec(`DELETE FROM provider_key_prices WHERE bifrost_key_id=$1`, id)
		for _, p := range body.Prices {
			if _, err = tx.Exec(`INSERT INTO provider_key_prices(bifrost_key_id,model_id,metric,unit_size,commission_microcredits) VALUES($1,$2,$3,$4,$5)`, id, p.ModelID, p.Metric, p.UnitSize, p.Commission); err != nil {
				invalid(w)
				return
			}
		}
		if err = tx.Commit(); err != nil {
			internal(w)
			return
		}
		if wasActive {
			key.Enabled, key.Status = true, "active"
			if err := h.stores.UpdateKey(r.Context(), key); err != nil {
				internal(w)
				return
			}
		}
	}
	rows, err := h.many(`SELECT model_id,metric,unit_size,commission_microcredits FROM provider_key_prices WHERE bifrost_key_id=$1 ORDER BY model_id,metric`, id)
	if err != nil {
		internal(w)
		return
	}
	writeJSON(w, 200, map[string]any{"prices": rows})
}

func (h *Handler) listTable(w http.ResponseWriter, query string) {
	rows, err := h.many(query)
	if err != nil {
		internal(w)
		return
	}
	writeJSON(w, 200, map[string]any{"data": rows})
}

type row map[string]any

func (h *Handler) many(query string, args ...any) ([]map[string]any, error) {
	rows, err := h.config.DB.Queryx(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []map[string]any{}
	for rows.Next() {
		item := row{}
		if err := rows.MapScan(item); err != nil {
			return nil, err
		}
		for key, value := range item {
			if raw, ok := value.([]byte); ok && json.Valid(raw) {
				item[key] = json.RawMessage(append([]byte(nil), raw...))
			}
		}
		result = append(result, map[string]any(item))
	}
	return result, rows.Err()
}
func (h *Handler) one(query string, args ...any) (map[string]any, error) {
	rows, err := h.many(query, args...)
	if err != nil {
		return nil, err
	}
	if len(rows) != 1 {
		return nil, errors.New("not found")
	}
	return rows[0], nil
}
func decode(r *http.Request, value any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return errors.New("multiple values")
	}
	return nil
}

func nonBlank(value string) bool {
	return strings.TrimSpace(value) != ""
}

func validProviderURL(value string) bool {
	if !nonBlank(value) {
		return false
	}
	parsed, err := url.ParseRequestURI(value)
	return err == nil && parsed.Host != "" && (parsed.Scheme == "http" || parsed.Scheme == "https")
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func errJSON(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
func invalid(w http.ResponseWriter)  { errJSON(w, 400, "invalid_request", "invalid request") }
func internal(w http.ResponseWriter) { errJSON(w, 500, "internal_error", "internal server error") }
func notFound(w http.ResponseWriter) { errJSON(w, 404, "not_found", "resource not found") }
