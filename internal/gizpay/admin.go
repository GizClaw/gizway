package gizpay

import (
	"crypto/hmac"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"
)

type adminProduct struct {
	ID           string    `db:"id" json:"id"`
	MerchantID   string    `db:"merchant_id" json:"merchant_id"`
	Name         string    `db:"name" json:"name"`
	BillingMode  string    `db:"billing_mode" json:"billing_mode"`
	Published    bool      `db:"published" json:"published"`
	Status       string    `db:"status" json:"status"`
	TermsVersion string    `db:"terms_version" json:"terms_version"`
	CreatedAt    time.Time `db:"created_at" json:"created_at"`
	UpdatedAt    time.Time `db:"updated_at" json:"updated_at"`
}

type adminProductListing struct {
	ID           string    `db:"id" json:"id"`
	ProductID    string    `db:"product_id" json:"product_id"`
	Site         string    `db:"site" json:"site"`
	Title        string    `db:"title" json:"title"`
	Description  string    `db:"description" json:"description"`
	BillingMode  string    `db:"billing_mode" json:"billing_mode"`
	PriceText    string    `db:"price_text" json:"price_text"`
	DisplayOrder int       `db:"display_order" json:"display_order"`
	Status       string    `db:"status" json:"status"`
	CreatedAt    time.Time `db:"created_at" json:"created_at"`
	UpdatedAt    time.Time `db:"updated_at" json:"updated_at"`
}

type createAdminProductListing struct {
	ID           string `json:"id"`
	ProductID    string `json:"product_id"`
	Site         string `json:"site"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	BillingMode  string `json:"billing_mode"`
	PriceText    string `json:"price_text"`
	DisplayOrder int    `json:"display_order"`
	Status       string `json:"status"`
}

type adminServicePrincipal struct {
	ID                   string     `db:"id" json:"id"`
	OwnerIdentityIssuer  string     `db:"owner_identity_issuer" json:"owner_identity_issuer"`
	OwnerIdentitySubject string     `db:"owner_identity_subject" json:"owner_identity_subject"`
	IdentityIssuer       string     `db:"identity_issuer" json:"identity_issuer"`
	IdentitySubject      string     `db:"identity_subject" json:"identity_subject"`
	Name                 string     `db:"name" json:"name"`
	RolesJSON            []byte     `db:"roles" json:"-"`
	Status               string     `db:"status" json:"status"`
	CreatedAt            time.Time  `db:"created_at" json:"created_at"`
	RevokedAt            *time.Time `db:"revoked_at" json:"revoked_at,omitempty"`
	Roles                []string   `db:"-" json:"roles"`
}

func (h *Handler) serveAdmin(w http.ResponseWriter, r *http.Request) {
	if len(h.config.AdminKey) == 0 || !hmac.Equal([]byte(r.Header.Get("X-GizWay-Admin-Key")), h.config.AdminKey) {
		errorJSON(w, http.StatusUnauthorized, "invalid_admin_key", "invalid Admin Key")
		return
	}
	switch {
	case r.URL.Path == "/admin/v1/products":
		h.adminProducts(w, r)
	case strings.HasPrefix(r.URL.Path, "/admin/v1/products/"):
		h.adminProductResource(w, r, r.PathValue("product_id"))
	case r.URL.Path == "/admin/v1/product-listings":
		h.adminProductListings(w, r)
	case strings.HasPrefix(r.URL.Path, "/admin/v1/product-listings/"):
		h.adminProductListingResource(w, r, r.PathValue("product_listing_id"))
	case r.URL.Path == "/admin/v1/service-principals":
		h.adminServicePrincipals(w, r)
	case strings.HasPrefix(r.URL.Path, "/admin/v1/service-principals/"):
		h.adminServicePrincipalResource(w, r, r.PathValue("service_principal_id"))
	default:
		notFound(w)
	}
}

func (h *Handler) adminProducts(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		var products []adminProduct
		if err := h.config.DB.SelectContext(r.Context(), &products, `SELECT * FROM products ORDER BY created_at,id`); err != nil {
			internal(w)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": products})
		return
	}
	var body struct {
		ID           string `json:"id"`
		MerchantID   string `json:"merchant_id"`
		Name         string `json:"name"`
		BillingMode  string `json:"billing_mode"`
		Published    bool   `json:"published"`
		Status       string `json:"status"`
		TermsVersion string `json:"terms_version"`
	}
	if decode(r, &body) != nil || !nonBlank(body.ID, body.MerchantID, body.Name, body.TermsVersion) || body.BillingMode != "pay_as_you_go" || !oneOf(body.Status, "active", "inactive") {
		invalid(w)
		return
	}
	existing, err := h.adminProduct(r, body.ID)
	if err == nil {
		if existing.MerchantID != body.MerchantID || existing.Name != body.Name || existing.BillingMode != body.BillingMode || existing.Published != body.Published || existing.Status != body.Status || existing.TermsVersion != body.TermsVersion {
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
	var merchantCount int
	if err = h.config.DB.GetContext(r.Context(), &merchantCount, `SELECT count(*) FROM merchants WHERE id=$1`, body.MerchantID); err != nil {
		internal(w)
		return
	}
	if merchantCount == 0 {
		adminDependencyNotFound(w)
		return
	}
	now := h.config.Now().UTC()
	if _, err = h.config.DB.ExecContext(r.Context(), `INSERT INTO products(id,merchant_id,name,billing_mode,published,status,terms_version,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$8)`, body.ID, body.MerchantID, body.Name, body.BillingMode, body.Published, body.Status, body.TermsVersion, now); err != nil {
		adminConflict(w)
		return
	}
	created, err := h.adminProduct(r, body.ID)
	if err != nil {
		internal(w)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *Handler) adminProductResource(w http.ResponseWriter, r *http.Request, id string) {
	product, err := h.adminProduct(r, id)
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
		writeJSON(w, http.StatusOK, product)
	case http.MethodPatch:
		var body struct {
			Name         *string `json:"name"`
			Published    *bool   `json:"published"`
			Status       *string `json:"status"`
			TermsVersion *string `json:"terms_version"`
		}
		if decode(r, &body) != nil || body.Name == nil && body.Published == nil && body.Status == nil && body.TermsVersion == nil || body.Name != nil && strings.TrimSpace(*body.Name) == "" || body.TermsVersion != nil && strings.TrimSpace(*body.TermsVersion) == "" || body.Status != nil && !oneOf(*body.Status, "active", "inactive") {
			invalid(w)
			return
		}
		if body.Name != nil {
			product.Name = *body.Name
		}
		if body.Published != nil {
			product.Published = *body.Published
		}
		if body.Status != nil {
			product.Status = *body.Status
		}
		if body.TermsVersion != nil {
			product.TermsVersion = *body.TermsVersion
		}
		product.UpdatedAt = h.config.Now().UTC()
		if _, err = h.config.DB.ExecContext(r.Context(), `UPDATE products SET name=$1,published=$2,status=$3,terms_version=$4,updated_at=$5 WHERE id=$6`, product.Name, product.Published, product.Status, product.TermsVersion, product.UpdatedAt, id); err != nil {
			internal(w)
			return
		}
		writeJSON(w, http.StatusOK, product)
	case http.MethodDelete:
		if product.Status != "inactive" {
			if _, err = h.config.DB.ExecContext(r.Context(), `UPDATE products SET status='inactive',updated_at=$1 WHERE id=$2`, h.config.Now().UTC(), id); err != nil {
				internal(w)
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *Handler) adminProduct(r *http.Request, id string) (adminProduct, error) {
	var product adminProduct
	err := h.config.DB.GetContext(r.Context(), &product, `SELECT * FROM products WHERE id=$1`, id)
	return product, err
}

func (h *Handler) adminProductListings(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		var listings []adminProductListing
		if err := h.config.DB.SelectContext(r.Context(), &listings, `SELECT * FROM product_listings ORDER BY created_at,id`); err != nil {
			internal(w)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": listings})
		return
	}
	var body createAdminProductListing
	if decode(r, &body) != nil || !nonBlank(body.ID, body.ProductID, body.Site, body.Title) || body.BillingMode != "pay_as_you_go" || !oneOf(body.Status, "active", "inactive") {
		invalid(w)
		return
	}
	existing, err := h.adminProductListing(r, body.ID)
	if err == nil {
		if !sameProductListing(existing, body) {
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
	if _, err = h.adminProduct(r, body.ProductID); errors.Is(err, sql.ErrNoRows) {
		adminDependencyNotFound(w)
		return
	} else if err != nil {
		internal(w)
		return
	}
	now := h.config.Now().UTC()
	if _, err = h.config.DB.ExecContext(r.Context(), `INSERT INTO product_listings(id,product_id,site,title,description,billing_mode,price_text,display_order,status,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$10)`, body.ID, body.ProductID, body.Site, body.Title, body.Description, body.BillingMode, body.PriceText, body.DisplayOrder, body.Status, now); err != nil {
		adminConflict(w)
		return
	}
	created, err := h.adminProductListing(r, body.ID)
	if err != nil {
		internal(w)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *Handler) adminProductListingResource(w http.ResponseWriter, r *http.Request, id string) {
	listing, err := h.adminProductListing(r, id)
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
			PriceText    *string `json:"price_text"`
			DisplayOrder *int    `json:"display_order"`
			Status       *string `json:"status"`
		}
		if decode(r, &body) != nil || body.Title == nil && body.Description == nil && body.PriceText == nil && body.DisplayOrder == nil && body.Status == nil || body.Title != nil && strings.TrimSpace(*body.Title) == "" || body.Status != nil && !oneOf(*body.Status, "active", "inactive") {
			invalid(w)
			return
		}
		if body.Title != nil {
			listing.Title = *body.Title
		}
		if body.Description != nil {
			listing.Description = *body.Description
		}
		if body.PriceText != nil {
			listing.PriceText = *body.PriceText
		}
		if body.DisplayOrder != nil {
			listing.DisplayOrder = *body.DisplayOrder
		}
		if body.Status != nil {
			listing.Status = *body.Status
		}
		listing.UpdatedAt = h.config.Now().UTC()
		if _, err = h.config.DB.ExecContext(r.Context(), `UPDATE product_listings SET title=$1,description=$2,price_text=$3,display_order=$4,status=$5,updated_at=$6 WHERE id=$7`, listing.Title, listing.Description, listing.PriceText, listing.DisplayOrder, listing.Status, listing.UpdatedAt, id); err != nil {
			internal(w)
			return
		}
		writeJSON(w, http.StatusOK, listing)
	case http.MethodDelete:
		if listing.Status != "inactive" {
			if _, err = h.config.DB.ExecContext(r.Context(), `UPDATE product_listings SET status='inactive',updated_at=$1 WHERE id=$2`, h.config.Now().UTC(), id); err != nil {
				internal(w)
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *Handler) adminProductListing(r *http.Request, id string) (adminProductListing, error) {
	var listing adminProductListing
	err := h.config.DB.GetContext(r.Context(), &listing, `SELECT * FROM product_listings WHERE id=$1`, id)
	return listing, err
}

func sameProductListing(left adminProductListing, right createAdminProductListing) bool {
	return left.ID == right.ID && left.ProductID == right.ProductID && left.Site == right.Site && left.Title == right.Title && left.Description == right.Description && left.BillingMode == right.BillingMode && left.PriceText == right.PriceText && left.DisplayOrder == right.DisplayOrder && left.Status == right.Status
}

func (h *Handler) adminServicePrincipals(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		principals, err := h.adminServicePrincipalList(r)
		if err != nil {
			internal(w)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": principals})
		return
	}
	var body struct {
		ID                   string   `json:"id"`
		OwnerIdentityIssuer  string   `json:"owner_identity_issuer"`
		OwnerIdentitySubject string   `json:"owner_identity_subject"`
		IdentityIssuer       string   `json:"identity_issuer"`
		IdentitySubject      string   `json:"identity_subject"`
		Name                 string   `json:"name"`
		Roles                []string `json:"roles"`
		Status               string   `json:"status"`
	}
	if decode(r, &body) != nil || !nonBlank(body.ID, body.OwnerIdentityIssuer, body.OwnerIdentitySubject, body.IdentityIssuer, body.IdentitySubject, body.Name) || body.Status != "active" || !validServiceAccountRoles(body.Roles) {
		invalid(w)
		return
	}
	existing, err := h.adminServicePrincipal(r, body.ID)
	if err == nil {
		if existing.OwnerIdentityIssuer != body.OwnerIdentityIssuer || existing.OwnerIdentitySubject != body.OwnerIdentitySubject || existing.IdentityIssuer != body.IdentityIssuer || existing.IdentitySubject != body.IdentitySubject || existing.Name != body.Name || existing.Status != body.Status || !sameStrings(existing.Roles, body.Roles) {
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
	var ownerID string
	if err = h.config.DB.GetContext(r.Context(), &ownerID, `SELECT id FROM users WHERE identity_issuer=$1 AND identity_subject=$2`, body.OwnerIdentityIssuer, body.OwnerIdentitySubject); errors.Is(err, sql.ErrNoRows) {
		adminDependencyNotFound(w)
		return
	} else if err != nil {
		internal(w)
		return
	}
	roles, _ := json.Marshal(body.Roles)
	if _, err = h.config.DB.ExecContext(r.Context(), `INSERT INTO service_principals(id,owner_user_id,identity_issuer,identity_subject,name,roles,status,created_at) VALUES($1,$2,$3,$4,$5,$6,'active',$7)`, body.ID, ownerID, body.IdentityIssuer, body.IdentitySubject, body.Name, roles, h.config.Now().UTC()); err != nil {
		adminConflict(w)
		return
	}
	created, err := h.adminServicePrincipal(r, body.ID)
	if err != nil {
		internal(w)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *Handler) adminServicePrincipalResource(w http.ResponseWriter, r *http.Request, id string) {
	principal, err := h.adminServicePrincipal(r, id)
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
		writeJSON(w, http.StatusOK, principal)
	case http.MethodPatch:
		var body struct {
			Name  *string   `json:"name"`
			Roles *[]string `json:"roles"`
		}
		if decode(r, &body) != nil || body.Name == nil && body.Roles == nil || body.Name != nil && strings.TrimSpace(*body.Name) == "" || body.Roles != nil && !validServiceAccountRoles(*body.Roles) {
			invalid(w)
			return
		}
		if body.Name != nil {
			principal.Name = *body.Name
		}
		if body.Roles != nil {
			principal.Roles = *body.Roles
		}
		roles, _ := json.Marshal(principal.Roles)
		if _, err = h.config.DB.ExecContext(r.Context(), `UPDATE service_principals SET name=$1,roles=$2 WHERE id=$3`, principal.Name, roles, id); err != nil {
			internal(w)
			return
		}
		writeJSON(w, http.StatusOK, principal)
	case http.MethodDelete:
		if principal.Status != "revoked" {
			now := h.config.Now().UTC()
			if _, err = h.config.DB.ExecContext(r.Context(), `UPDATE service_principals SET status='revoked',revoked_at=$1 WHERE id=$2`, now, id); err != nil {
				internal(w)
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *Handler) adminServicePrincipal(r *http.Request, id string) (adminServicePrincipal, error) {
	var principal adminServicePrincipal
	err := h.config.DB.GetContext(r.Context(), &principal, `SELECT sp.id,u.identity_issuer owner_identity_issuer,u.identity_subject owner_identity_subject,sp.identity_issuer,sp.identity_subject,sp.name,sp.roles,sp.status,sp.created_at,sp.revoked_at FROM service_principals sp JOIN users u ON u.id=sp.owner_user_id WHERE sp.id=$1`, id)
	if err == nil {
		err = json.Unmarshal(principal.RolesJSON, &principal.Roles)
	}
	return principal, err
}

func (h *Handler) adminServicePrincipalList(r *http.Request) ([]adminServicePrincipal, error) {
	var principals []adminServicePrincipal
	err := h.config.DB.SelectContext(r.Context(), &principals, `SELECT sp.id,u.identity_issuer owner_identity_issuer,u.identity_subject owner_identity_subject,sp.identity_issuer,sp.identity_subject,sp.name,sp.roles,sp.status,sp.created_at,sp.revoked_at FROM service_principals sp JOIN users u ON u.id=sp.owner_user_id ORDER BY sp.created_at,sp.id`)
	if err != nil {
		return nil, err
	}
	for index := range principals {
		if err := json.Unmarshal(principals[index].RolesJSON, &principals[index].Roles); err != nil {
			return nil, err
		}
	}
	return principals, nil
}

func nonBlank(values ...string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return false
		}
	}
	return true
}

func oneOf(value string, allowed ...string) bool {
	return slices.Contains(allowed, value)
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func adminConflict(w http.ResponseWriter) {
	errorJSON(w, http.StatusConflict, "resource_id_conflict", "resource ID already exists with different content")
}

func adminDependencyNotFound(w http.ResponseWriter) {
	errorJSON(w, http.StatusNotFound, "dependency_not_found", "required parent resource was not found")
}
