package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
)

// GatewayKeyPrincipal is a GizPay-only account projection. It is never passed
// to a regional GizWay execution; GizWay knows only the raw customer key.
type GatewayKeyPrincipal struct {
	UserID    string
	AccountID string
	APIKeyID  string
	Scopes    JSON
}

type GatewayPrice struct {
	ID             string `db:"id"`
	Metric         string `db:"metric"`
	UnitSize       int64  `db:"unit_size"`
	BasePrice      int64  `db:"base_customer_price_microcredits"`
	EffectivePrice int64  `db:"customer_price_microcredits"`
	DiscountBPS    int    `db:"discount_bps"`
}

// GatewayCandidate is an identity-free regional provider execution candidate.
type GatewayCandidate struct {
	ModelID                string
	PublicModel            string
	VariantID              string
	ProviderModel          string
	ProviderEndpoint       string
	ProviderCredential     string
	Capabilities           JSON
	ContextWindow          int64
	MaxOutputTokens        int64
	LocalRatePublicationID string
	RatePublicationID      string
	Prices                 map[string]GatewayPrice
}

type ProviderExecutionTarget struct {
	Endpoint   string
	Credential string
	Model      string
	RouteKey   string
}

func (candidate GatewayCandidate) ExecutionTarget() ProviderExecutionTarget {
	return ProviderExecutionTarget{
		Endpoint: candidate.ProviderEndpoint, Credential: candidate.ProviderCredential,
		Model: candidate.ProviderModel, RouteKey: candidate.VariantID,
	}
}

// GatewayMetric is measured Usage priced with the immutable publication
// captured by the regional execution.
type GatewayMetric struct {
	Metric   string
	Quantity int64
	Price    GatewayPrice
	Charge   int64
}

// AuthenticateGatewayKey is a GizPay-only query used by center-owned account
// projections. GizWay never calls it while admitting provider execution.
func (s *Store) AuthenticateGatewayKey(ctx context.Context, secretHash []byte, at string) (GatewayKeyPrincipal, error) {
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
		return GatewayKeyPrincipal{}, ErrNotFound
	}
	if err != nil {
		return GatewayKeyPrincipal{}, fmt.Errorf("authenticate Gateway key: %w", err)
	}
	return GatewayKeyPrincipal{UserID: result.UserID, AccountID: result.AccountID, APIKeyID: result.APIKeyID, Scopes: result.Scopes}, nil
}

// ResolveRegionalGatewayCandidates reads only region-owned Catalog and the
// active immutable rate publication. It has no Account or API-key dimension.
func (s *Store) ResolveRegionalGatewayCandidates(ctx context.Context, publicModel, operation, at string) ([]GatewayCandidate, error) {
	var publication struct {
		LocalID  string `db:"id"`
		GizPayID string `db:"gizpay_publication_id"`
	}
	if err := s.db.GetContext(ctx, &publication, `
		SELECT id,gizpay_publication_id FROM rate_publications
		WHERE status IN ('active','retired') AND gizpay_publication_id IS NOT NULL AND effective_at<=?
		ORDER BY revision DESC LIMIT 1
	`, at); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("read active regional publication: %w", err)
	}
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
	if err := s.db.SelectContext(ctx, &rows, `
		SELECT m.id AS model_id,m.slug AS public_model,mv.id AS variant_id,
		       mv.provider_model_name,pe.base_url,pe.credential_ref,
		       mv.capabilities,mv.context_window,mv.max_output_tokens
		FROM models m
		JOIN model_variants mv ON mv.model_id=m.id
		JOIN provider_endpoints pe ON pe.id=mv.provider_endpoint_id
		JOIN providers p ON p.id=pe.provider_id
		WHERE m.slug=? AND m.status='active' AND mv.status IN ('active','degraded')
		  AND pe.status='active' AND p.status='active'
		  AND EXISTS (SELECT 1 FROM rate_publication_versions price
		              WHERE price.publication_id=? AND price.model_variant_id=mv.id)
		ORDER BY pe.priority,mv.variant_slug
	`, publicModel, publication.LocalID); err != nil {
		return nil, fmt.Errorf("resolve regional Gateway candidates: %w", err)
	}
	candidates := make([]GatewayCandidate, 0, len(rows))
	for _, row := range rows {
		var capabilities map[string]bool
		if json.Unmarshal(row.Capabilities, &capabilities) != nil || !capabilities[gatewayCapability(operation)] ||
			row.ContextWindow == nil || *row.ContextWindow <= 0 || row.MaxOutputTokens == nil || *row.MaxOutputTokens <= 0 {
			continue
		}
		credential := ""
		if s.secrets != nil {
			var err error
			credential, err = s.secrets.decrypt(row.CredentialRef)
			if err != nil {
				return nil, fmt.Errorf("resolve provider credential: %w", err)
			}
		}
		prices, err := s.gatewayPricesForPublication(ctx, publication.LocalID, row.VariantID)
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, GatewayCandidate{
			ModelID: row.ModelID, PublicModel: row.PublicModel, VariantID: row.VariantID,
			ProviderModel: row.ProviderModel, ProviderEndpoint: row.ProviderEndpoint,
			ProviderCredential: credential, Capabilities: row.Capabilities,
			ContextWindow: *row.ContextWindow, MaxOutputTokens: *row.MaxOutputTokens,
			LocalRatePublicationID: publication.LocalID, RatePublicationID: publication.GizPayID, Prices: prices,
		})
	}
	if len(candidates) == 0 {
		return nil, ErrNotFound
	}
	return candidates, nil
}

func (s *Store) gatewayPricesForPublication(ctx context.Context, publicationID, variantID string) (map[string]GatewayPrice, error) {
	var prices []GatewayPrice
	if err := s.db.SelectContext(ctx, &prices, `SELECT rate_version_id AS id,metric,unit_size,
		base_price_microcredits AS base_customer_price_microcredits,
		customer_price_microcredits,discount_bps
		FROM rate_publication_versions WHERE publication_id=? AND model_variant_id=?
		ORDER BY metric`, publicationID, variantID); err != nil {
		return nil, fmt.Errorf("read published regional prices: %w", err)
	}
	result := make(map[string]GatewayPrice, len(prices))
	for _, price := range prices {
		result[price.Metric] = price
	}
	return result, nil
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

// ResolveVariantExecutionTarget reloads the region-owned endpoint and secret
// when a Realtime provider connection is established.
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
	return ProviderExecutionTarget{Endpoint: row.Endpoint, Credential: credential, Model: row.Model, RouteKey: variantID}, nil
}

// GatewayPricesForVariant reads draft regional prices for catalog previews.
// Provider execution itself always uses gatewayPricesForPublication above.
func (s *Store) GatewayPricesForVariant(ctx context.Context, variantID, at string) (map[string]GatewayPrice, error) {
	var prices []GatewayPrice
	if err := s.db.SelectContext(ctx, &prices, `
		SELECT id,metric,unit_size,base_customer_price_microcredits,
		       customer_price_microcredits,discount_bps
		FROM model_variant_prices
		WHERE model_variant_id=? AND valid_from<=?
		  AND (valid_until IS NULL OR valid_until>?)
		ORDER BY metric,valid_from DESC
	`, variantID, at, at); err != nil {
		return nil, fmt.Errorf("read Gateway prices: %w", err)
	}
	result := make(map[string]GatewayPrice, len(prices))
	for _, price := range prices {
		if _, exists := result[price.Metric]; !exists {
			result[price.Metric] = price
		}
	}
	return result, nil
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
