package gizway

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"gorm.io/gorm"

	bifrostadapter "github.com/idy/gizway/internal/adapter/bifrost"
	"github.com/idy/gizway/internal/identity"
)

type Config struct {
	DB                         *sqlx.DB
	DatabaseSchema             string
	Verifier                   *identity.Verifier
	HumanAudience              string
	HMACSecret                 []byte
	GizPayURL                  string
	ServiceToken               func(context.Context) (string, error)
	HTTPClient                 *http.Client
	Now                        func() time.Time
	OutboxBatchSize            int
	OutboxRetryInterval        time.Duration
	OutboxAbandonAfter         time.Duration
	CreditCacheCleanupInterval time.Duration
	BifrostMaxRetries          int
	BifrostRequestTimeout      time.Duration
	RealtimeSessionTTL         time.Duration
	BifrostStores              *bifrostadapter.Stores
	Logger                     *slog.Logger
	ProviderCallbackBaseURL    string
	ProviderCallbackSecret     []byte
}

type creditAdmission struct {
	accountID, subscriptionID, productID, billing string
	ownerIssuer, ownerSubject                     string
	allowed                                       bool
}

type creditState struct {
	admission creditAdmission
	available int64
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

var schemaNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

const defaultCreditCacheCleanupInterval = time.Minute

func New(config Config) (*Handler, error) {
	if config.DB == nil || config.Verifier == nil || config.HumanAudience == "" || config.BifrostStores == nil || !schemaNamePattern.MatchString(config.DatabaseSchema) {
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
	if config.CreditCacheCleanupInterval <= 0 {
		config.CreditCacheCleanupInterval = defaultCreditCacheCleanupInterval
	}
	if config.RealtimeSessionTTL <= 0 {
		config.RealtimeSessionTTL = time.Minute
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	engine := bifrostadapter.NewLazyWithExecution(config.BifrostMaxRetries, config.BifrostRequestTimeout)
	engine.ConfigureProviderCallbacks(config.ProviderCallbackBaseURL, config.ProviderCallbackSecret)
	h := &Handler{config: config, engine: engine, stores: config.BifrostStores, credits: map[string]*creditState{}, realtime: map[string]realtimeSession{}, stop: make(chan struct{}), done: make(chan struct{})}
	_, _ = config.DB.Exec(`UPDATE charge_outbox SET status='pending',recover_duplicate=true,updated_at=$1 WHERE status='sending'`, config.Now().UTC())
	go h.runBackgroundWorkers()
	return h, nil
}

func (h *Handler) diagnostic(message string, err error, attributes ...any) {
	if err != nil {
		attributes = append(attributes, "error", err.Error())
	}
	logger := h.config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.Error(message, attributes...)
}

func (h *Handler) runBackgroundWorkers() {
	var workers sync.WaitGroup
	workers.Add(2)
	go func() {
		defer workers.Done()
		h.runOutbox()
	}()
	go func() {
		defer workers.Done()
		h.runCreditCacheCleanup()
	}()
	workers.Wait()
	close(h.done)
}

func (h *Handler) runOutbox() {
	interval := h.config.OutboxRetryInterval
	if interval <= 0 {
		interval = 250 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-h.stop:
			return
		case <-ticker.C:
		}
		h.pruneRealtimeSessions()
		if h.config.DB == nil || h.config.Now == nil {
			continue
		}
		abandonAfter := h.config.OutboxAbandonAfter
		if abandonAfter <= 0 {
			abandonAfter = 24 * time.Hour
		}
		batchSize := h.config.OutboxBatchSize
		if batchSize <= 0 {
			batchSize = 20
		}
		_, _ = h.config.DB.Exec(`UPDATE charge_outbox SET status='abandoned',updated_at=$1 WHERE status='pending' AND created_at < $2`, h.config.Now().UTC(), h.config.Now().UTC().Add(-abandonAfter))
		var ids []string
		if h.config.DB.Select(&ids, `SELECT external_order_id FROM charge_outbox WHERE status IN('pending','sending') ORDER BY created_at LIMIT $1`, batchSize) != nil {
			continue
		}
		for _, id := range ids {
			h.reportOutbox(id)
		}
	}
}

func (h *Handler) runCreditCacheCleanup() {
	interval := h.config.CreditCacheCleanupInterval
	if interval <= 0 {
		interval = defaultCreditCacheCleanupInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-h.stop:
			return
		case <-ticker.C:
			h.pruneCreditStates()
		}
	}
}

func (h *Handler) pruneCreditStates() {
	if h.config.Now == nil {
		return
	}
	now := h.config.Now().UTC()
	h.creditMu.Lock()
	defer h.creditMu.Unlock()
	for keyHMAC, state := range h.credits {
		if state != nil && !state.loading && !now.Before(state.expires) {
			delete(h.credits, keyHMAC)
		}
	}
}

func (h *Handler) Close() error {
	h.stopOnce.Do(func() { close(h.stop) })
	<-h.done
	return nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, "/user/") {
		h.protocol(w, r)
		return
	}
	principal, err := h.config.Verifier.Authenticate(r, h.config.HumanAudience)
	if err != nil {
		if errors.Is(err, identity.ErrInvalidIssuer) {
			errJSON(w, 401, "invalid_token_issuer", "invalid token issuer")
		} else if errors.Is(err, identity.ErrInvalidAudience) {
			errJSON(w, 401, "invalid_token_audience", "invalid token audience")
		} else {
			errJSON(w, 401, "invalid_bearer_token", "invalid bearer token")
		}
		return
	}
	path := r.URL.Path
	switch {
	case r.Method == http.MethodPost && strings.HasPrefix(path, "/user/v1/providers/") && strings.HasSuffix(path, "/keys"):
		h.createProviderKey(w, r, strings.TrimSuffix(strings.TrimPrefix(path, "/user/v1/providers/"), "/keys"), principal)
	case r.Method == http.MethodPut && strings.HasPrefix(path, "/user/v1/provider-keys/") && strings.HasSuffix(path, "/prices"):
		h.putProviderKeyPrices(w, r, strings.TrimSuffix(strings.TrimPrefix(path, "/user/v1/provider-keys/"), "/prices"), principal)
	case r.Method == http.MethodPost && strings.HasPrefix(path, "/user/v1/provider-keys/") && strings.HasSuffix(path, "/disable"):
		h.disableProviderKey(w, r, strings.TrimSuffix(strings.TrimPrefix(path, "/user/v1/provider-keys/"), "/disable"), principal)
	default:
		notFound(w)
	}
}

func (h *Handler) ownerMerchant(ctx context.Context, authorization string, principal identity.Principal) (string, error) {
	var merchantID string
	err := h.config.DB.GetContext(ctx, &merchantID, `SELECT merchant_id FROM gizway_user_merchants WHERE owner_identity_issuer=$1 AND owner_identity_subject=$2`, principal.Issuer, principal.Subject)
	if err == nil {
		return merchantID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(h.config.GizPayURL, "/")+"/account/v1/initialize", nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Authorization", authorization)
	response, err := h.config.HTTPClient.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", errors.New("GizPay initialize failed")
	}
	var body struct {
		DefaultMerchantID string `json:"default_merchant_id"`
	}
	if json.NewDecoder(response.Body).Decode(&body) != nil || body.DefaultMerchantID == "" {
		return "", errors.New("GizPay initialize response is invalid")
	}
	_, err = h.config.DB.ExecContext(ctx, `INSERT INTO gizway_user_merchants(owner_identity_issuer,owner_identity_subject,merchant_id) VALUES($1,$2,$3) ON CONFLICT(owner_identity_issuer,owner_identity_subject) DO UPDATE SET merchant_id=EXCLUDED.merchant_id,updated_at=now()`, principal.Issuer, principal.Subject, body.DefaultMerchantID)
	return body.DefaultMerchantID, err
}

type keyPrice struct {
	ModelID      string `json:"model_id"`
	Metric       string `json:"metric"`
	UnitSize     int64  `json:"unit_size"`
	Microcredits int64  `json:"microcredits_per_unit"`
}

func validMetric(metric string) bool { return metric == "input_tokens" || metric == "output_tokens" }

func validatePrices(prices []keyPrice) bool {
	if len(prices) == 0 {
		return false
	}
	seen := map[string]bool{}
	for _, p := range prices {
		key := p.ModelID + "\x00" + p.Metric
		if !nonBlank(p.ModelID) || !validMetric(p.Metric) || p.UnitSize <= 0 || p.Microcredits < 0 || seen[key] {
			return false
		}
		seen[key] = true
	}
	return true
}

func (h *Handler) createProviderKey(w http.ResponseWriter, r *http.Request, providerID string, principal identity.Principal) {
	var body struct {
		Name   string     `json:"name"`
		Key    string     `json:"key"`
		Status string     `json:"status"`
		Prices []keyPrice `json:"prices"`
	}
	if decode(r, &body) != nil || !nonBlank(providerID) || !nonBlank(body.Key) || body.Status != "active" || !validatePrices(body.Prices) {
		invalid(w)
		return
	}
	provider, err := h.stores.Provider(r.Context(), providerID)
	if err != nil || provider.Status != "active" {
		notFound(w)
		return
	}
	merchantID, err := h.ownerMerchant(r.Context(), r.Header.Get("Authorization"), principal)
	if err != nil {
		errJSON(w, 503, "account_initialize_unavailable", "account initialization unavailable")
		return
	}
	id, now := "pkey_"+uuid.NewString(), h.config.Now().UTC()
	name := body.Name
	if !nonBlank(name) {
		name = "Provider Key"
	}
	err = h.stores.ExecuteConfigTransaction(r.Context(), func(tx *gorm.DB) error {
		validatedModels := map[string]bool{}
		for _, price := range body.Prices {
			if validatedModels[price.ModelID] {
				continue
			}
			var count int64
			if err := tx.Raw(`SELECT count(*) FROM client_sync.models WHERE id=? AND provider_id=? AND status='active'`, price.ModelID, providerID).Scan(&count).Error; err != nil || count != 1 {
				return errors.New("price references an unavailable provider model")
			}
			validatedModels[price.ModelID] = true
		}
		if err := h.stores.CreateKeyInTransaction(r.Context(), bifrostadapter.KeyRecord{ID: id, ProviderID: providerID, Name: name, APIKey: body.Key, Weight: 1, Enabled: true, Status: "active"}, tx); err != nil {
			return err
		}
		q := func(table string) string { return `"` + h.config.DatabaseSchema + `".` + table }
		if err := tx.Exec(`INSERT INTO `+q("provider_key_billing")+`(provider_key_id,owner_identity_issuer,owner_identity_subject,merchant_id,status,created_at,updated_at) VALUES(?,?,?,?,? ,?,?)`, id, principal.Issuer, principal.Subject, merchantID, "active", now, now).Error; err != nil {
			return err
		}
		for _, p := range body.Prices {
			if err := tx.Exec(`INSERT INTO `+q("provider_key_prices")+`(provider_key_id,model_id,metric,unit_size,microcredits_per_unit) VALUES(?,?,?,?,?)`, id, p.ModelID, p.Metric, p.UnitSize, p.Microcredits).Error; err != nil {
				return err
			}
		}
		pricesJSON, _ := json.Marshal(body.Prices)
		return tx.Exec(`INSERT INTO client_sync.provider_keys(id,provider_id,key,merchant_id,owner_identity_issuer,owner_identity_subject,status,prices_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, id, providerID, body.Key, merchantID, principal.Issuer, principal.Subject, "active", string(pricesJSON), now, now).Error
	})
	if err != nil {
		invalid(w)
		return
	}
	writeJSON(w, 201, map[string]any{"provider_key_id": id, "provider_id": providerID, "key": body.Key, "merchant_id": merchantID, "status": "active", "prices": body.Prices})
}

func (h *Handler) putProviderKeyPrices(w http.ResponseWriter, r *http.Request, id string, principal identity.Principal) {
	var body struct {
		Prices []keyPrice `json:"prices"`
	}
	if decode(r, &body) != nil || !validatePrices(body.Prices) {
		invalid(w)
		return
	}
	tx, err := h.config.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		internal(w)
		return
	}
	defer tx.Rollback()
	var providerID string
	if tx.Get(&providerID, `SELECT k.provider_id FROM provider_key_billing b JOIN client_sync.provider_keys k ON k.id=b.provider_key_id WHERE b.provider_key_id=$1 AND b.owner_identity_issuer=$2 AND b.owner_identity_subject=$3`, id, principal.Issuer, principal.Subject) != nil {
		notFound(w)
		return
	}
	validatedModels := map[string]bool{}
	for _, price := range body.Prices {
		if validatedModels[price.ModelID] {
			continue
		}
		var count int
		if tx.Get(&count, `SELECT count(*) FROM client_sync.models WHERE id=$1 AND provider_id=$2 AND status='active'`, price.ModelID, providerID) != nil || count != 1 {
			invalid(w)
			return
		}
		validatedModels[price.ModelID] = true
	}
	if _, err = tx.Exec(`DELETE FROM provider_key_prices WHERE provider_key_id=$1`, id); err != nil {
		internal(w)
		return
	}
	for _, p := range body.Prices {
		if _, err = tx.Exec(`INSERT INTO provider_key_prices(provider_key_id,model_id,metric,unit_size,microcredits_per_unit) VALUES($1,$2,$3,$4,$5)`, id, p.ModelID, p.Metric, p.UnitSize, p.Microcredits); err != nil {
			invalid(w)
			return
		}
	}
	pricesJSON, _ := json.Marshal(body.Prices)
	if _, err = tx.Exec(`UPDATE client_sync.provider_keys SET prices_json=$1,updated_at=$2 WHERE id=$3`, string(pricesJSON), h.config.Now().UTC(), id); err != nil {
		internal(w)
		return
	}
	if err = tx.Commit(); err != nil {
		internal(w)
		return
	}
	writeJSON(w, 200, body)
}

func (h *Handler) disableProviderKey(w http.ResponseWriter, r *http.Request, id string, principal identity.Principal) {
	var providerID, keyValue, merchantID string
	err := h.config.DB.QueryRowxContext(r.Context(), `SELECT b.merchant_id,k.provider_id,k.key FROM provider_key_billing b JOIN client_sync.provider_keys k ON k.id=b.provider_key_id WHERE b.provider_key_id=$1 AND b.owner_identity_issuer=$2 AND b.owner_identity_subject=$3`, id, principal.Issuer, principal.Subject).Scan(&merchantID, &providerID, &keyValue)
	if err != nil {
		notFound(w)
		return
	}
	key, err := h.stores.Key(r.Context(), providerID, id)
	if err != nil {
		notFound(w)
		return
	}
	key.Enabled = false
	key.Status = "disabled"
	if err = h.stores.ExecuteConfigTransaction(r.Context(), func(tx *gorm.DB) error {
		if err := h.stores.UpdateKeyInTransaction(r.Context(), key, tx); err != nil {
			return err
		}
		q := `"` + h.config.DatabaseSchema + `".`
		if err := tx.Exec(`UPDATE `+q+`provider_key_billing SET status='disabled',updated_at=? WHERE provider_key_id=?`, h.config.Now().UTC(), id).Error; err != nil {
			return err
		}
		return tx.Exec(`UPDATE client_sync.provider_keys SET status='disabled',updated_at=? WHERE id=?`, h.config.Now().UTC(), id).Error
	}); err != nil {
		internal(w)
		return
	}
	writeJSON(w, 200, map[string]any{"provider_key_id": id, "provider_id": providerID, "key": keyValue, "merchant_id": merchantID, "status": "disabled"})
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
func nonBlank(value string) bool { return strings.TrimSpace(value) != "" }
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

var _ = bytes.NewReader
