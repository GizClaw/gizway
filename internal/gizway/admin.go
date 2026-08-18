package gizway

import (
	"crypto/hmac"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	bifrostadapter "github.com/idy/gizway/internal/adapter/bifrost"
	"gorm.io/gorm"
)

type adminProvider struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	BaseURL   string    `json:"base_url"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

type adminModel struct {
	ID            string `db:"id" json:"id"`
	ProviderID    string `db:"provider_id" json:"provider_id"`
	Name          string `db:"name" json:"name"`
	ProviderModel string `db:"provider_model" json:"provider_model"`
	Status        string `db:"status" json:"status"`
}

type adminCustomerPrice struct {
	Metric            string `db:"metric" json:"metric"`
	UnitSize          int64  `db:"unit_size" json:"unit_size"`
	PriceMicrocredits int64  `db:"price_microcredits" json:"price_microcredits"`
}

type adminModelListing struct {
	ID           string    `db:"id" json:"id"`
	ModelID      string    `db:"model_id" json:"model_id"`
	Title        string    `db:"title" json:"title"`
	Description  string    `db:"description" json:"description"`
	Family       string    `db:"family" json:"family"`
	Context      string    `db:"context" json:"context"`
	Latency      string    `db:"latency" json:"latency"`
	Accent       string    `db:"accent" json:"accent"`
	Featured     bool      `db:"featured" json:"featured"`
	DisplayOrder int       `db:"display_order" json:"display_order"`
	Availability string    `db:"availability" json:"availability"`
	CreatedAt    time.Time `db:"created_at" json:"created_at"`
	UpdatedAt    time.Time `db:"updated_at" json:"updated_at"`
}

type createAdminModelListing struct {
	ID           string `json:"id"`
	ModelID      string `json:"model_id"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	Family       string `json:"family"`
	Context      string `json:"context"`
	Latency      string `json:"latency"`
	Accent       string `json:"accent"`
	Featured     bool   `json:"featured"`
	DisplayOrder int    `json:"display_order"`
	Availability string `json:"availability"`
}

type adminProviderKey struct {
	ID                   string    `db:"id" json:"id"`
	ProviderID           string    `db:"provider_id" json:"provider_id"`
	OwnerIdentityIssuer  string    `db:"owner_identity_issuer" json:"owner_identity_issuer"`
	OwnerIdentitySubject string    `db:"owner_identity_subject" json:"owner_identity_subject"`
	MerchantID           string    `db:"merchant_id" json:"merchant_id"`
	Name                 string    `db:"name" json:"name"`
	Status               string    `db:"status" json:"status"`
	SecretConfigured     bool      `db:"-" json:"secret_configured"`
	CreatedAt            time.Time `db:"created_at" json:"created_at"`
	UpdatedAt            time.Time `db:"updated_at" json:"updated_at"`
}

func (h *Handler) serveAdmin(w http.ResponseWriter, r *http.Request) {
	if len(h.config.AdminKey) == 0 || !hmac.Equal([]byte(r.Header.Get("X-GizWay-Admin-Key")), h.config.AdminKey) {
		errJSON(w, http.StatusUnauthorized, "invalid_admin_key", "invalid Admin Key")
		return
	}
	path := r.URL.Path
	switch {
	case path == "/admin/v1/providers":
		h.adminProviders(w, r)
	case strings.HasPrefix(path, "/admin/v1/providers/"):
		h.adminProviderResource(w, r, r.PathValue("provider_id"))
	case path == "/admin/v1/models":
		h.adminModels(w, r)
	case strings.HasSuffix(path, "/customer-prices"):
		h.adminModelCustomerPrices(w, r, r.PathValue("model_id"))
	case strings.HasPrefix(path, "/admin/v1/models/"):
		h.adminModelResource(w, r, r.PathValue("model_id"))
	case path == "/admin/v1/model-listings":
		h.adminModelListings(w, r)
	case strings.HasPrefix(path, "/admin/v1/model-listings/"):
		h.adminModelListingResource(w, r, r.PathValue("model_listing_id"))
	case path == "/admin/v1/provider-keys":
		h.adminProviderKeys(w, r)
	case strings.HasSuffix(path, "/rotate-secret"):
		h.adminProviderKeyRotateSecret(w, r, r.PathValue("provider_key_id"))
	case strings.HasSuffix(path, "/prices"):
		h.adminProviderKeyPrices(w, r, r.PathValue("provider_key_id"))
	case strings.HasPrefix(path, "/admin/v1/provider-keys/"):
		h.adminProviderKeyResource(w, r, r.PathValue("provider_key_id"))
	default:
		notFound(w)
	}
}

func (h *Handler) adminProviders(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		records, err := h.stores.Providers(r.Context())
		if err != nil {
			internal(w)
			return
		}
		providers := make([]adminProvider, len(records))
		for index, record := range records {
			providers[index] = providerResponse(record)
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": providers})
		return
	}
	var body struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Kind    string `json:"kind"`
		BaseURL string `json:"base_url"`
		Status  string `json:"status"`
	}
	if decode(r, &body) != nil || !allNonBlank(body.ID, body.Name, body.Kind, body.BaseURL) || !validURL(body.BaseURL) || !adminOneOf(body.Status, "active", "inactive") {
		invalid(w)
		return
	}
	existing, err := h.stores.Provider(r.Context(), body.ID)
	if err == nil {
		if existing.Name != body.Name || existing.Kind != body.Kind || existing.BaseURL != body.BaseURL || existing.Status != body.Status {
			adminConflict(w)
			return
		}
		writeJSON(w, http.StatusOK, providerResponse(existing))
		return
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		internal(w)
		return
	}
	now := h.config.Now().UTC()
	record := bifrostadapter.ProviderRecord{ID: body.ID, Name: body.Name, Kind: body.Kind, BaseURL: body.BaseURL, Status: body.Status, CreatedAt: now}
	err = h.stores.ExecuteConfigTransaction(r.Context(), func(tx *gorm.DB) error {
		if err := h.stores.CreateProviderInTransaction(r.Context(), record, tx); err != nil {
			return err
		}
		return tx.Exec(`INSERT INTO client_sync.providers(id,name,kind,status) VALUES(?,?,?,?)`, body.ID, body.Name, body.Kind, body.Status).Error
	})
	if err != nil {
		if existing, getErr := h.stores.Provider(r.Context(), body.ID); getErr == nil && existing.Name == body.Name && existing.Kind == body.Kind && existing.BaseURL == body.BaseURL && existing.Status == body.Status {
			writeJSON(w, http.StatusOK, providerResponse(existing))
			return
		}
		adminConflict(w)
		return
	}
	writeJSON(w, http.StatusCreated, providerResponse(record))
}

func (h *Handler) adminProviderResource(w http.ResponseWriter, r *http.Request, id string) {
	record, err := h.stores.Provider(r.Context(), id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		notFound(w)
		return
	}
	if err != nil {
		internal(w)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, providerResponse(record))
	case http.MethodPatch:
		var body struct {
			Name    *string `json:"name"`
			BaseURL *string `json:"base_url"`
			Status  *string `json:"status"`
		}
		if decode(r, &body) != nil || body.Name == nil && body.BaseURL == nil && body.Status == nil || body.Name != nil && strings.TrimSpace(*body.Name) == "" || body.BaseURL != nil && !validURL(*body.BaseURL) || body.Status != nil && !adminOneOf(*body.Status, "active", "inactive") {
			invalid(w)
			return
		}
		if body.Name != nil {
			record.Name = *body.Name
		}
		if body.BaseURL != nil {
			record.BaseURL = *body.BaseURL
		}
		if body.Status != nil {
			record.Status = *body.Status
		}
		if err = h.updateAdminProvider(r, record); err != nil {
			internal(w)
			return
		}
		writeJSON(w, http.StatusOK, providerResponse(record))
	case http.MethodDelete:
		if record.Status != "inactive" {
			record.Status = "inactive"
			if err = h.updateAdminProvider(r, record); err != nil {
				internal(w)
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *Handler) updateAdminProvider(r *http.Request, record bifrostadapter.ProviderRecord) error {
	return h.stores.ExecuteConfigTransaction(r.Context(), func(tx *gorm.DB) error {
		if err := h.stores.UpdateProviderInTransaction(r.Context(), record, tx); err != nil {
			return err
		}
		return tx.Exec(`UPDATE client_sync.providers SET name=?,kind=?,status=? WHERE id=?`, record.Name, record.Kind, record.Status, record.ID).Error
	})
}

func providerResponse(record bifrostadapter.ProviderRecord) adminProvider {
	return adminProvider{ID: record.ID, Name: record.Name, Kind: record.Kind, BaseURL: record.BaseURL, Status: record.Status, CreatedAt: record.CreatedAt}
}

func (h *Handler) adminModels(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		var models []adminModel
		if err := h.config.DB.SelectContext(r.Context(), &models, `SELECT * FROM client_sync.models ORDER BY id`); err != nil {
			internal(w)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": models})
		return
	}
	var body adminModel
	if decode(r, &body) != nil || !allNonBlank(body.ID, body.ProviderID, body.Name, body.ProviderModel) || !adminOneOf(body.Status, "active", "inactive") {
		invalid(w)
		return
	}
	existing, err := h.adminModel(r, body.ID)
	if err == nil {
		if existing != body {
			adminConflict(w)
			return
		}
		writeJSON(w, http.StatusOK, existing)
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		internal(w)
		return
	}
	if _, err = h.stores.Provider(r.Context(), body.ProviderID); errors.Is(err, gorm.ErrRecordNotFound) {
		adminDependencyNotFound(w)
		return
	} else if err != nil {
		internal(w)
		return
	}
	if _, err = h.config.DB.ExecContext(r.Context(), `INSERT INTO client_sync.models(id,provider_id,name,provider_model,status) VALUES($1,$2,$3,$4,$5)`, body.ID, body.ProviderID, body.Name, body.ProviderModel, body.Status); err != nil {
		if existing, getErr := h.adminModel(r, body.ID); getErr == nil && existing == body {
			writeJSON(w, http.StatusOK, existing)
			return
		}
		adminConflict(w)
		return
	}
	writeJSON(w, http.StatusCreated, body)
}

func (h *Handler) adminModelResource(w http.ResponseWriter, r *http.Request, id string) {
	model, err := h.adminModel(r, id)
	if errors.Is(err, sql.ErrNoRows) {
		notFound(w)
		return
	}
	if err != nil {
		internal(w)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, model)
	case http.MethodPatch:
		var body struct {
			Name          *string `json:"name"`
			ProviderModel *string `json:"provider_model"`
			Status        *string `json:"status"`
		}
		if decode(r, &body) != nil || body.Name == nil && body.ProviderModel == nil && body.Status == nil || body.Name != nil && strings.TrimSpace(*body.Name) == "" || body.ProviderModel != nil && strings.TrimSpace(*body.ProviderModel) == "" || body.Status != nil && !adminOneOf(*body.Status, "active", "inactive") {
			invalid(w)
			return
		}
		if body.Name != nil {
			model.Name = *body.Name
		}
		if body.ProviderModel != nil {
			model.ProviderModel = *body.ProviderModel
		}
		if body.Status != nil {
			model.Status = *body.Status
		}
		if _, err = h.config.DB.ExecContext(r.Context(), `UPDATE client_sync.models SET name=$1,provider_model=$2,status=$3 WHERE id=$4`, model.Name, model.ProviderModel, model.Status, id); err != nil {
			internal(w)
			return
		}
		writeJSON(w, http.StatusOK, model)
	case http.MethodDelete:
		if model.Status != "inactive" {
			if _, err = h.config.DB.ExecContext(r.Context(), `UPDATE client_sync.models SET status='inactive' WHERE id=$1`, id); err != nil {
				internal(w)
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *Handler) adminModel(r *http.Request, id string) (adminModel, error) {
	var model adminModel
	err := h.config.DB.GetContext(r.Context(), &model, `SELECT * FROM client_sync.models WHERE id=$1`, id)
	return model, err
}

func (h *Handler) adminModelCustomerPrices(w http.ResponseWriter, r *http.Request, modelID string) {
	if _, err := h.adminModel(r, modelID); errors.Is(err, sql.ErrNoRows) {
		notFound(w)
		return
	} else if err != nil {
		internal(w)
		return
	}
	if r.Method == http.MethodGet {
		prices, err := h.adminCustomerPrices(r, modelID)
		if err != nil {
			internal(w)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"prices": prices})
		return
	}
	var body struct {
		Prices []adminCustomerPrice `json:"prices"`
	}
	if decode(r, &body) != nil || !validCustomerPrices(body.Prices) {
		invalid(w)
		return
	}
	tx, err := h.config.DB.BeginTxx(r.Context(), nil)
	if err != nil {
		internal(w)
		return
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(r.Context(), `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "admin-model-prices:"+modelID); err != nil {
		internal(w)
		return
	}
	var lockedModelID string
	if err = tx.GetContext(r.Context(), &lockedModelID, `SELECT id FROM client_sync.models WHERE id=$1`, modelID); errors.Is(err, sql.ErrNoRows) {
		notFound(w)
		return
	} else if err != nil {
		internal(w)
		return
	}
	if _, err = tx.ExecContext(r.Context(), `DELETE FROM model_customer_prices WHERE model_id=$1`, modelID); err != nil {
		internal(w)
		return
	}
	for _, price := range body.Prices {
		if _, err = tx.ExecContext(r.Context(), `INSERT INTO model_customer_prices(model_id,metric,unit_size,price_microcredits) VALUES($1,$2,$3,$4)`, modelID, price.Metric, price.UnitSize, price.PriceMicrocredits); err != nil {
			internal(w)
			return
		}
	}
	if err = tx.Commit(); err != nil {
		internal(w)
		return
	}
	prices := slices.Clone(body.Prices)
	sort.Slice(prices, func(left, right int) bool { return prices[left].Metric < prices[right].Metric })
	writeJSON(w, http.StatusOK, map[string]any{"prices": prices})
}

func (h *Handler) adminCustomerPrices(r *http.Request, modelID string) ([]adminCustomerPrice, error) {
	var prices []adminCustomerPrice
	err := h.config.DB.SelectContext(r.Context(), &prices, `SELECT metric,unit_size,price_microcredits FROM model_customer_prices WHERE model_id=$1 ORDER BY metric`, modelID)
	return prices, err
}

func validCustomerPrices(prices []adminCustomerPrice) bool {
	if len(prices) == 0 {
		return false
	}
	seen := map[string]bool{}
	for _, price := range prices {
		if !validMetric(price.Metric) || price.UnitSize <= 0 || price.PriceMicrocredits < 0 || seen[price.Metric] {
			return false
		}
		seen[price.Metric] = true
	}
	return true
}

func (h *Handler) adminModelListings(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		var listings []adminModelListing
		if err := h.config.DB.SelectContext(r.Context(), &listings, `SELECT * FROM model_listings ORDER BY created_at,id`); err != nil {
			internal(w)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": listings})
		return
	}
	var body createAdminModelListing
	if decode(r, &body) != nil || !allNonBlank(body.ID, body.ModelID, body.Title, body.Family) || !adminOneOf(body.Availability, "available", "unavailable") {
		invalid(w)
		return
	}
	existing, err := h.adminModelListing(r, body.ID)
	if err == nil {
		if !sameAdminModelListing(existing, body) {
			adminConflict(w)
			return
		}
		writeJSON(w, http.StatusOK, existing)
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		internal(w)
		return
	}
	if _, err = h.adminModel(r, body.ModelID); errors.Is(err, sql.ErrNoRows) {
		adminDependencyNotFound(w)
		return
	} else if err != nil {
		internal(w)
		return
	}
	now := h.config.Now().UTC()
	if _, err = h.config.DB.ExecContext(r.Context(), `INSERT INTO model_listings(id,model_id,title,description,family,context,latency,accent,featured,display_order,availability,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$12)`, body.ID, body.ModelID, body.Title, body.Description, body.Family, body.Context, body.Latency, body.Accent, body.Featured, body.DisplayOrder, body.Availability, now); err != nil {
		if existing, getErr := h.adminModelListing(r, body.ID); getErr == nil && sameAdminModelListing(existing, body) {
			writeJSON(w, http.StatusOK, existing)
			return
		}
		adminConflict(w)
		return
	}
	created, err := h.adminModelListing(r, body.ID)
	if err != nil {
		internal(w)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *Handler) adminModelListingResource(w http.ResponseWriter, r *http.Request, id string) {
	listing, err := h.adminModelListing(r, id)
	if errors.Is(err, sql.ErrNoRows) {
		notFound(w)
		return
	}
	if err != nil {
		internal(w)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, listing)
	case http.MethodPatch:
		var body struct {
			Title        *string `json:"title"`
			Description  *string `json:"description"`
			Family       *string `json:"family"`
			Context      *string `json:"context"`
			Latency      *string `json:"latency"`
			Accent       *string `json:"accent"`
			Availability *string `json:"availability"`
			Featured     *bool   `json:"featured"`
			DisplayOrder *int    `json:"display_order"`
		}
		if decode(r, &body) != nil || body.Title == nil && body.Description == nil && body.Family == nil && body.Context == nil && body.Latency == nil && body.Accent == nil && body.Featured == nil && body.DisplayOrder == nil && body.Availability == nil || body.Title != nil && strings.TrimSpace(*body.Title) == "" || body.Family != nil && strings.TrimSpace(*body.Family) == "" || body.Availability != nil && !adminOneOf(*body.Availability, "available", "unavailable") {
			invalid(w)
			return
		}
		applyModelListingPatch(&listing, body.Title, body.Description, body.Family, body.Context, body.Latency, body.Accent, body.Featured, body.DisplayOrder, body.Availability)
		listing.UpdatedAt = h.config.Now().UTC()
		if _, err = h.config.DB.ExecContext(r.Context(), `UPDATE model_listings SET title=$1,description=$2,family=$3,context=$4,latency=$5,accent=$6,featured=$7,display_order=$8,availability=$9,updated_at=$10 WHERE id=$11`, listing.Title, listing.Description, listing.Family, listing.Context, listing.Latency, listing.Accent, listing.Featured, listing.DisplayOrder, listing.Availability, listing.UpdatedAt, id); err != nil {
			internal(w)
			return
		}
		writeJSON(w, http.StatusOK, listing)
	case http.MethodDelete:
		if listing.Availability != "unavailable" {
			if _, err = h.config.DB.ExecContext(r.Context(), `UPDATE model_listings SET availability='unavailable',updated_at=$1 WHERE id=$2`, h.config.Now().UTC(), id); err != nil {
				internal(w)
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *Handler) adminModelListing(r *http.Request, id string) (adminModelListing, error) {
	var listing adminModelListing
	err := h.config.DB.GetContext(r.Context(), &listing, `SELECT * FROM model_listings WHERE id=$1`, id)
	return listing, err
}

func sameAdminModelListing(left adminModelListing, right createAdminModelListing) bool {
	return left.ID == right.ID && left.ModelID == right.ModelID && left.Title == right.Title && left.Description == right.Description && left.Family == right.Family && left.Context == right.Context && left.Latency == right.Latency && left.Accent == right.Accent && left.Featured == right.Featured && left.DisplayOrder == right.DisplayOrder && left.Availability == right.Availability
}

func applyModelListingPatch(listing *adminModelListing, title, description, family, context, latency, accent *string, featured *bool, displayOrder *int, availability *string) {
	if title != nil {
		listing.Title = *title
	}
	if description != nil {
		listing.Description = *description
	}
	if family != nil {
		listing.Family = *family
	}
	if context != nil {
		listing.Context = *context
	}
	if latency != nil {
		listing.Latency = *latency
	}
	if accent != nil {
		listing.Accent = *accent
	}
	if featured != nil {
		listing.Featured = *featured
	}
	if displayOrder != nil {
		listing.DisplayOrder = *displayOrder
	}
	if availability != nil {
		listing.Availability = *availability
	}
}

func (h *Handler) adminProviderKeys(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		var keys []adminProviderKey
		if err := h.config.DB.SelectContext(r.Context(), &keys, `SELECT k.id,k.provider_id,k.owner_identity_issuer,k.owner_identity_subject,k.merchant_id,k.name,k.status,k.created_at,k.updated_at FROM client_sync.provider_keys k ORDER BY k.created_at,k.id`); err != nil {
			internal(w)
			return
		}
		for index := range keys {
			keys[index].SecretConfigured = true
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": keys})
		return
	}
	var body struct {
		ID                   string     `json:"id"`
		ProviderID           string     `json:"provider_id"`
		OwnerIdentityIssuer  string     `json:"owner_identity_issuer"`
		OwnerIdentitySubject string     `json:"owner_identity_subject"`
		MerchantID           string     `json:"merchant_id"`
		Name                 string     `json:"name"`
		Key                  string     `json:"key"`
		Status               string     `json:"status"`
		Prices               []keyPrice `json:"prices"`
	}
	if decode(r, &body) != nil || !allNonBlank(body.ID, body.ProviderID, body.OwnerIdentityIssuer, body.OwnerIdentitySubject, body.MerchantID, body.Name, body.Key) || !adminOneOf(body.Status, "active", "disabled") || !validatePrices(body.Prices) {
		invalid(w)
		return
	}
	existingKey, err := h.adminProviderKeyWithSecret(r, body.ID)
	if err == nil {
		prices, priceErr := h.adminProviderKeyPriceList(r, body.ID)
		if priceErr != nil || !sameAdminProviderKey(existingKey, body.ProviderID, body.OwnerIdentityIssuer, body.OwnerIdentitySubject, body.MerchantID, body.Name, body.Key, body.Status, prices, body.Prices) {
			adminConflict(w)
			return
		}
		writeJSON(w, http.StatusOK, existingKey.response())
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		internal(w)
		return
	}
	provider, err := h.stores.Provider(r.Context(), body.ProviderID)
	if errors.Is(err, gorm.ErrRecordNotFound) || err == nil && provider.Status != "active" {
		adminDependencyNotFound(w)
		return
	}
	if err != nil {
		internal(w)
		return
	}
	now := h.config.Now().UTC()
	err = h.stores.ExecuteConfigTransaction(r.Context(), func(tx *gorm.DB) error {
		if err := h.validateAdminProviderKeyModels(tx, body.ProviderID, body.Prices); err != nil {
			return err
		}
		enabled := body.Status == "active"
		if err := h.stores.CreateKeyInTransaction(r.Context(), bifrostadapter.KeyRecord{ID: body.ID, ProviderID: body.ProviderID, Name: body.Name, APIKey: body.Key, Weight: 1, Enabled: enabled, Status: body.Status}, tx); err != nil {
			return err
		}
		q := `"` + h.config.DatabaseSchema + `".`
		if err := tx.Exec(`INSERT INTO `+q+`provider_key_billing(provider_key_id,owner_identity_issuer,owner_identity_subject,merchant_id,name,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, body.ID, body.OwnerIdentityIssuer, body.OwnerIdentitySubject, body.MerchantID, body.Name, body.Status, now, now).Error; err != nil {
			return err
		}
		for _, price := range body.Prices {
			if err := tx.Exec(`INSERT INTO `+q+`provider_key_prices(provider_key_id,model_id,metric,unit_size,microcredits_per_unit) VALUES(?,?,?,?,?)`, body.ID, price.ModelID, price.Metric, price.UnitSize, price.Microcredits).Error; err != nil {
				return err
			}
		}
		pricesJSON, _ := json.Marshal(body.Prices)
		return tx.Exec(`INSERT INTO client_sync.provider_keys(id,provider_id,key,merchant_id,owner_identity_issuer,owner_identity_subject,name,status,prices_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, body.ID, body.ProviderID, body.Key, body.MerchantID, body.OwnerIdentityIssuer, body.OwnerIdentitySubject, body.Name, body.Status, string(pricesJSON), now, now).Error
	})
	if errors.Is(err, errUnavailableProviderModel) {
		adminDependencyNotFound(w)
		return
	}
	if err != nil {
		if existing, getErr := h.adminProviderKeyWithSecret(r, body.ID); getErr == nil {
			prices, priceErr := h.adminProviderKeyPriceList(r, body.ID)
			if priceErr == nil && sameAdminProviderKey(existing, body.ProviderID, body.OwnerIdentityIssuer, body.OwnerIdentitySubject, body.MerchantID, body.Name, body.Key, body.Status, prices, body.Prices) {
				writeJSON(w, http.StatusOK, existing.response())
				return
			}
		}
		adminConflict(w)
		return
	}
	writeJSON(w, http.StatusCreated, adminProviderKey{ID: body.ID, ProviderID: body.ProviderID, OwnerIdentityIssuer: body.OwnerIdentityIssuer, OwnerIdentitySubject: body.OwnerIdentitySubject, MerchantID: body.MerchantID, Name: body.Name, Status: body.Status, SecretConfigured: true, CreatedAt: now, UpdatedAt: now})
}

func sameAdminProviderKey(key adminProviderKeySecret, providerID, ownerIssuer, ownerSubject, merchantID, name, secret, status string, existingPrices, requestedPrices []keyPrice) bool {
	return key.ProviderID == providerID && key.OwnerIdentityIssuer == ownerIssuer && key.OwnerIdentitySubject == ownerSubject && key.MerchantID == merchantID && key.Name == name && key.Key == secret && key.Status == status && samePrices(existingPrices, requestedPrices)
}

type adminProviderKeySecret struct {
	adminProviderKey
	Key string `db:"key"`
}

func (key adminProviderKeySecret) response() adminProviderKey {
	result := key.adminProviderKey
	result.SecretConfigured = true
	return result
}

func (h *Handler) adminProviderKeyWithSecret(r *http.Request, id string) (adminProviderKeySecret, error) {
	var key adminProviderKeySecret
	err := h.config.DB.GetContext(r.Context(), &key, `SELECT k.id,k.provider_id,k.owner_identity_issuer,k.owner_identity_subject,k.merchant_id,k.name,k.status,k.created_at,k.updated_at,k.key FROM client_sync.provider_keys k WHERE k.id=$1`, id)
	return key, err
}

func (h *Handler) adminProviderKeyResource(w http.ResponseWriter, r *http.Request, id string) {
	key, err := h.adminProviderKeyWithSecret(r, id)
	if errors.Is(err, sql.ErrNoRows) {
		notFound(w)
		return
	}
	if err != nil {
		internal(w)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, key.response())
	case http.MethodPatch:
		var body struct {
			Name   *string `json:"name"`
			Status *string `json:"status"`
		}
		if decode(r, &body) != nil || body.Name == nil && body.Status == nil || body.Name != nil && strings.TrimSpace(*body.Name) == "" || body.Status != nil && !adminOneOf(*body.Status, "active", "disabled") {
			invalid(w)
			return
		}
		if body.Name != nil {
			key.Name = *body.Name
		}
		if body.Status != nil {
			key.Status = *body.Status
		}
		key.UpdatedAt = h.config.Now().UTC()
		if err = h.updateAdminProviderKey(r, key); err != nil {
			internal(w)
			return
		}
		writeJSON(w, http.StatusOK, key.response())
	case http.MethodDelete:
		if key.Status != "disabled" {
			key.Status = "disabled"
			key.UpdatedAt = h.config.Now().UTC()
			if err = h.updateAdminProviderKey(r, key); err != nil {
				internal(w)
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *Handler) updateAdminProviderKey(r *http.Request, key adminProviderKeySecret) error {
	record, err := h.stores.Key(r.Context(), key.ProviderID, key.ID)
	if err != nil {
		return err
	}
	record.Name, record.Status, record.Enabled = key.Name, key.Status, key.Status == "active"
	return h.stores.ExecuteConfigTransaction(r.Context(), func(tx *gorm.DB) error {
		if err := h.stores.UpdateKeyInTransaction(r.Context(), record, tx); err != nil {
			return err
		}
		q := `"` + h.config.DatabaseSchema + `".`
		if err = tx.Exec(`UPDATE `+q+`provider_key_billing SET name=?,status=?,updated_at=? WHERE provider_key_id=?`, key.Name, key.Status, key.UpdatedAt, key.ID).Error; err != nil {
			return err
		}
		return tx.Exec(`UPDATE client_sync.provider_keys SET name=?,status=?,updated_at=? WHERE id=?`, key.Name, key.Status, key.UpdatedAt, key.ID).Error
	})
}

func (h *Handler) adminProviderKeyRotateSecret(w http.ResponseWriter, r *http.Request, id string) {
	key, err := h.adminProviderKeyWithSecret(r, id)
	if errors.Is(err, sql.ErrNoRows) {
		notFound(w)
		return
	}
	if err != nil {
		internal(w)
		return
	}
	var body struct {
		Key string `json:"key"`
	}
	if decode(r, &body) != nil || strings.TrimSpace(body.Key) == "" {
		invalid(w)
		return
	}
	record, err := h.stores.Key(r.Context(), key.ProviderID, id)
	if err != nil {
		internal(w)
		return
	}
	record.APIKey = body.Key
	now := h.config.Now().UTC()
	err = h.stores.ExecuteConfigTransaction(r.Context(), func(tx *gorm.DB) error {
		if err := h.stores.UpdateKeyInTransaction(r.Context(), record, tx); err != nil {
			return err
		}
		return tx.Exec(`UPDATE client_sync.provider_keys SET key=?,updated_at=? WHERE id=?`, body.Key, now, id).Error
	})
	if err != nil {
		internal(w)
		return
	}
	key.UpdatedAt = now
	writeJSON(w, http.StatusOK, key.response())
}

func (h *Handler) adminProviderKeyPrices(w http.ResponseWriter, r *http.Request, id string) {
	key, err := h.adminProviderKeyWithSecret(r, id)
	if errors.Is(err, sql.ErrNoRows) {
		notFound(w)
		return
	}
	if err != nil {
		internal(w)
		return
	}
	if r.Method == http.MethodGet {
		prices, err := h.adminProviderKeyPriceList(r, id)
		if err != nil {
			internal(w)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"prices": prices})
		return
	}
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
	if _, err = tx.ExecContext(r.Context(), `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "admin-provider-key-prices:"+id); err != nil {
		internal(w)
		return
	}
	var lockedKeyID string
	if err = tx.GetContext(r.Context(), &lockedKeyID, `SELECT id FROM client_sync.provider_keys WHERE id=$1`, id); errors.Is(err, sql.ErrNoRows) {
		notFound(w)
		return
	} else if err != nil {
		internal(w)
		return
	}
	for _, price := range body.Prices {
		var count int
		if err = tx.GetContext(r.Context(), &count, `SELECT count(*) FROM client_sync.models WHERE id=$1 AND provider_id=$2 AND status='active'`, price.ModelID, key.ProviderID); err != nil {
			internal(w)
			return
		}
		if count != 1 {
			adminDependencyNotFound(w)
			return
		}
	}
	if _, err = tx.ExecContext(r.Context(), `DELETE FROM provider_key_prices WHERE provider_key_id=$1`, id); err != nil {
		internal(w)
		return
	}
	for _, price := range body.Prices {
		if _, err = tx.ExecContext(r.Context(), `INSERT INTO provider_key_prices(provider_key_id,model_id,metric,unit_size,microcredits_per_unit) VALUES($1,$2,$3,$4,$5)`, id, price.ModelID, price.Metric, price.UnitSize, price.Microcredits); err != nil {
			internal(w)
			return
		}
	}
	pricesJSON, _ := json.Marshal(body.Prices)
	if _, err = tx.ExecContext(r.Context(), `UPDATE client_sync.provider_keys SET prices_json=$1,updated_at=$2 WHERE id=$3`, string(pricesJSON), h.config.Now().UTC(), id); err != nil {
		internal(w)
		return
	}
	if err = tx.Commit(); err != nil {
		internal(w)
		return
	}
	prices := slices.Clone(body.Prices)
	sort.Slice(prices, func(left, right int) bool {
		if prices[left].ModelID == prices[right].ModelID {
			return prices[left].Metric < prices[right].Metric
		}
		return prices[left].ModelID < prices[right].ModelID
	})
	writeJSON(w, http.StatusOK, map[string]any{"prices": prices})
}

func (h *Handler) adminProviderKeyPriceList(r *http.Request, id string) ([]keyPrice, error) {
	var prices []keyPrice
	err := h.config.DB.SelectContext(r.Context(), &prices, `SELECT model_id,metric,unit_size,microcredits_per_unit FROM provider_key_prices WHERE provider_key_id=$1 ORDER BY model_id,metric`, id)
	return prices, err
}

func (h *Handler) validateAdminProviderKeyModels(tx *gorm.DB, providerID string, prices []keyPrice) error {
	seen := map[string]bool{}
	for _, price := range prices {
		if seen[price.ModelID] {
			continue
		}
		var count int64
		if err := tx.Raw(`SELECT count(*) FROM client_sync.models WHERE id=? AND provider_id=? AND status='active'`, price.ModelID, providerID).Scan(&count).Error; err != nil {
			return err
		}
		if count != 1 {
			return errUnavailableProviderModel
		}
		seen[price.ModelID] = true
	}
	return nil
}

func allNonBlank(values ...string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return false
		}
	}
	return true
}

func adminOneOf(value string, values ...string) bool {
	return slices.Contains(values, value)
}

func validURL(value string) bool {
	request, err := http.NewRequest(http.MethodGet, value, nil)
	return err == nil && request.URL.Scheme != "" && request.URL.Host != ""
}

func adminConflict(w http.ResponseWriter) {
	errJSON(w, http.StatusConflict, "resource_id_conflict", "resource ID already exists with different content")
}
func adminDependencyNotFound(w http.ResponseWriter) {
	errJSON(w, http.StatusNotFound, "dependency_not_found", "required parent resource was not found")
}
