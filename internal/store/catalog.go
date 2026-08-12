package store

import (
	"context"
	"encoding/json"
	"sort"
)

// AccountCatalogPrice discloses the immutable customer-facing price snapshot
// without upstream cost, provider endpoint, model name, or credential data.
type AccountCatalogPrice struct {
	Metric         string `json:"metric"`
	UnitSize       int64  `json:"unit_size"`
	BasePrice      int64  `json:"base_price_microcredits"`
	EffectivePrice int64  `json:"effective_price_microcredits"`
	DiscountBPS    int    `json:"discount_bps"`
}

type AccountCatalogVariant struct {
	ID              string                `json:"id"`
	Slug            string                `json:"slug"`
	Capabilities    JSON                  `json:"capabilities"`
	ContextWindow   int64                 `json:"context_window"`
	MaxOutputTokens int64                 `json:"max_output_tokens"`
	Prices          []AccountCatalogPrice `json:"prices"`
}

type AccountCatalogModel struct {
	ID           string                  `json:"id"`
	Name         string                  `json:"name"`
	Capabilities []string                `json:"capabilities"`
	Variants     []AccountCatalogVariant `json:"variants"`
	CreatedAt    string                  `json:"created_at"`
}

func (s *Store) listExecutableCatalog(ctx context.Context, _ string, at string) ([]AccountCatalogModel, error) {
	var rows []struct {
		ModelID         string `db:"model_id"`
		Slug            string `db:"slug"`
		Name            string `db:"name"`
		CreatedAt       string `db:"created_at"`
		VariantID       string `db:"variant_id"`
		VariantSlug     string `db:"variant_slug"`
		Capabilities    JSON   `db:"capabilities"`
		ContextWindow   *int64 `db:"context_window"`
		MaxOutputTokens *int64 `db:"max_output_tokens"`
	}
	baseQuery := `SELECT m.id AS model_id,m.slug,m.name,m.created_at,mv.id AS variant_id,mv.variant_slug,mv.capabilities,mv.context_window,mv.max_output_tokens FROM models m JOIN model_variants mv ON mv.model_id=m.id JOIN provider_endpoints pe ON pe.id=mv.provider_endpoint_id JOIN providers p ON p.id=pe.provider_id WHERE m.status='active' AND mv.status IN ('active','degraded') AND pe.status='active' AND p.status='active'`
	args := []any{}
	baseQuery += ` ORDER BY m.slug,pe.priority,mv.variant_slug`
	if err := s.db.SelectContext(ctx, &rows, baseQuery, args...); err != nil {
		return nil, err
	}
	models := make([]AccountCatalogModel, 0)
	modelIndexes := make(map[string]int)
	capabilitySets := make(map[string]map[string]struct{})
	for _, row := range rows {
		if row.ContextWindow == nil || row.MaxOutputTokens == nil || *row.ContextWindow <= 0 || *row.MaxOutputTokens <= 0 {
			continue
		}
		prices, err := s.GatewayPricesForVariant(ctx, row.VariantID, at)
		if err != nil {
			return nil, err
		}
		capabilities, executable := executableCapabilities(row.Capabilities, prices)
		if len(executable) == 0 {
			continue
		}
		index, exists := modelIndexes[row.ModelID]
		if !exists {
			index = len(models)
			modelIndexes[row.ModelID] = index
			models = append(models, AccountCatalogModel{ID: row.Slug, Name: row.Name, CreatedAt: row.CreatedAt, Variants: []AccountCatalogVariant{}})
			capabilitySets[row.ModelID] = make(map[string]struct{})
		}
		for _, capability := range executable {
			capabilitySets[row.ModelID][capability] = struct{}{}
		}
		priceList := make([]AccountCatalogPrice, 0, len(prices))
		for _, price := range prices {
			priceList = append(priceList, AccountCatalogPrice{Metric: price.Metric, UnitSize: price.UnitSize, BasePrice: price.BasePrice, EffectivePrice: price.EffectivePrice, DiscountBPS: price.DiscountBPS})
		}
		sort.Slice(priceList, func(i, j int) bool { return priceList[i].Metric < priceList[j].Metric })
		models[index].Variants = append(models[index].Variants, AccountCatalogVariant{ID: row.VariantID, Slug: row.VariantSlug, Capabilities: capabilities, ContextWindow: *row.ContextWindow, MaxOutputTokens: *row.MaxOutputTokens, Prices: priceList})
	}
	for modelID, index := range modelIndexes {
		for capability := range capabilitySets[modelID] {
			models[index].Capabilities = append(models[index].Capabilities, capability)
		}
		sort.Strings(models[index].Capabilities)
	}
	return models, nil
}

func executableCapabilities(encoded JSON, prices map[string]GatewayPrice) (JSON, []string) {
	var declared map[string]bool
	if json.Unmarshal(encoded, &declared) != nil {
		return encoded, nil
	}
	executable := make([]string, 0, len(declared))
	filtered := make(map[string]bool)
	for capability, enabled := range declared {
		if !enabled || !hasMetrics(prices, capabilityMetrics(capability)) {
			continue
		}
		filtered[capability] = true
		executable = append(executable, capability)
	}
	sort.Strings(executable)
	result, _ := json.Marshal(filtered)
	return JSON(result), executable
}

func capabilityMetrics(capability string) []string {
	switch capability {
	case "audio_speech", "audio_transcriptions":
		return []string{"request", "input_token", "output_token", "audio_second"}
	case "image_generation":
		return []string{"request", "input_token", "output_token", "image"}
	case "embeddings":
		return []string{"input_token", "cached_input_token"}
	case "realtime":
		return []string{"input_token", "cached_input_token", "output_token", "input_audio_token", "output_audio_token"}
	case "chat", "responses", "messages", "generateContent":
		return []string{"input_token", "cached_input_token", "output_token"}
	default:
		return nil
	}
}

func hasMetrics(prices map[string]GatewayPrice, metrics []string) bool {
	if len(metrics) == 0 {
		return false
	}
	for _, metric := range metrics {
		if _, ok := prices[metric]; !ok {
			return false
		}
	}
	return true
}
