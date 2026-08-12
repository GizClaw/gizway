package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/idy/gizway/internal/timetext"
)

type RegionalRatePublication struct {
	ID                  string           `db:"id" json:"id"`
	Region              string           `db:"region" json:"region"`
	Revision            int64            `db:"revision" json:"revision"`
	ContentHash         []byte           `db:"content_hash" json:"-"`
	Status              string           `db:"status" json:"status"`
	GizPayPublicationID *string          `db:"gizpay_publication_id" json:"gizpay_publication_id,omitempty"`
	EffectiveAt         *string          `db:"effective_at" json:"effective_at,omitempty"`
	CreatedAt           string           `db:"created_at" json:"created_at"`
	UpdatedAt           string           `db:"updated_at" json:"updated_at"`
	Prices              []PublishedPrice `json:"prices,omitempty"`
}

// PrepareRegionalRatePublication freezes the currently effective complete
// regional price set before any network call. The local row is not executable
// until GizPay accepts the identical canonical snapshot.
func (s *Store) PrepareRegionalRatePublication(ctx context.Context, region, sourceID, effectiveAt string) (RegionalRatePublication, error) {
	if region != "cn" && region != "global" {
		return RegionalRatePublication{}, errors.New("publication region must be cn or global")
	}
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" || len(sourceID) > 255 {
		return RegionalRatePublication{}, errors.New("source publication ID is required")
	}
	effectiveAt, err := timetext.Normalize(effectiveAt)
	if err != nil {
		return RegionalRatePublication{}, errors.New("publication effective_at is invalid")
	}
	var snapshot []struct {
		PublishedPrice
		RateVersionID string `db:"rate_version_id"`
	}
	if err := s.db.SelectContext(ctx, &snapshot, `SELECT p.id AS rate_version_id,v.id AS model_variant_id,m.slug AS public_model,
		p.metric,p.unit_size,p.base_customer_price_microcredits AS base_price_microcredits,
		p.customer_price_microcredits,p.discount_bps
		FROM model_variant_prices p JOIN model_variants v ON v.id=p.model_variant_id
		JOIN models m ON m.id=v.model_id JOIN provider_endpoints e ON e.id=v.provider_endpoint_id
		JOIN providers provider ON provider.id=e.provider_id
		WHERE p.valid_from<=? AND (p.valid_until IS NULL OR p.valid_until>?)
		  AND v.status='active' AND m.status='active' AND e.status='active' AND provider.status='active'
		ORDER BY v.id,p.metric`, effectiveAt, effectiveAt); err != nil {
		return RegionalRatePublication{}, fmt.Errorf("read regional price snapshot: %w", err)
	}
	if len(snapshot) == 0 {
		return RegionalRatePublication{}, errors.New("regional publication has no executable prices")
	}
	prices := make([]PublishedPrice, len(snapshot))
	for index := range snapshot {
		prices[index] = snapshot[index].PublishedPrice
	}
	canonical, err := json.Marshal(prices)
	if err != nil {
		return RegionalRatePublication{}, fmt.Errorf("encode regional price snapshot: %w", err)
	}
	hash := sha256.Sum256(canonical)
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return RegionalRatePublication{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `LOCK TABLE rate_publications IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return RegionalRatePublication{}, fmt.Errorf("lock regional publication revision: %w", err)
	}
	var existing RegionalRatePublication
	err = tx.GetContext(ctx, &existing, `SELECT id,region,revision,content_hash,status,gizpay_publication_id,effective_at,created_at,updated_at
		FROM rate_publications WHERE id=? FOR UPDATE`, sourceID)
	if err == nil {
		if existing.Region != region || existing.EffectiveAt == nil || *existing.EffectiveAt != effectiveAt ||
			!bytes.Equal(existing.ContentHash, hash[:]) || (existing.Status != "publishing" && existing.Status != "active") {
			return RegionalRatePublication{}, ErrIdempotencyConflict
		}
		if err := tx.SelectContext(ctx, &existing.Prices, `SELECT model_variant_id,public_model,metric,unit_size,
			base_price_microcredits,customer_price_microcredits,discount_bps
			FROM rate_publication_versions WHERE publication_id=? ORDER BY model_variant_id,metric`, sourceID); err != nil {
			return RegionalRatePublication{}, fmt.Errorf("read regional publication replay: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return RegionalRatePublication{}, err
		}
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return RegionalRatePublication{}, fmt.Errorf("lookup regional publication: %w", err)
	}
	var revision int64
	if err := tx.GetContext(ctx, &revision, `SELECT COALESCE(MAX(revision),0)+1 FROM rate_publications WHERE region=?`, region); err != nil {
		return RegionalRatePublication{}, fmt.Errorf("allocate regional publication revision: %w", err)
	}
	now := timetext.Format(s.now())
	publication := RegionalRatePublication{
		ID: sourceID, Region: region, Revision: revision,
		ContentHash: hash[:], Status: "publishing", EffectiveAt: &effectiveAt,
		CreatedAt: now, UpdatedAt: now, Prices: prices,
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO rate_publications
		(id,region,revision,content_hash,status,effective_at,created_at,updated_at)
		VALUES (?,?,?,?,'publishing',?,?,?)`, publication.ID, region, revision, hash[:], effectiveAt, now, now); err != nil {
		return RegionalRatePublication{}, fmt.Errorf("insert regional publication: %w", err)
	}
	for _, price := range snapshot {
		if _, err := tx.ExecContext(ctx, `INSERT INTO rate_publication_versions
			(publication_id,rate_version_id,model_variant_id,public_model,metric,unit_size,
			 base_price_microcredits,customer_price_microcredits,discount_bps)
			VALUES (?,?,?,?,?,?,?,?,?)`, publication.ID, price.RateVersionID, price.ModelVariantID,
			price.PublicModel, price.Metric, price.UnitSize, price.BasePriceMicrocredits,
			price.CustomerPriceMicrocredits, price.DiscountBPS); err != nil {
			return RegionalRatePublication{}, fmt.Errorf("insert regional publication price: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return RegionalRatePublication{}, err
	}
	return publication, nil
}

func (s *Store) ActivateRegionalRatePublication(ctx context.Context, id, gizPayID string) (RegionalRatePublication, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return RegionalRatePublication{}, err
	}
	defer tx.Rollback()
	var publication RegionalRatePublication
	if err := tx.GetContext(ctx, &publication, `SELECT id,region,revision,content_hash,status,gizpay_publication_id,effective_at,created_at,updated_at
		FROM rate_publications WHERE id=? FOR UPDATE`, id); err != nil {
		return RegionalRatePublication{}, err
	}
	if publication.Status != "publishing" && !(publication.Status == "active" && publication.GizPayPublicationID != nil && *publication.GizPayPublicationID == gizPayID) {
		return RegionalRatePublication{}, ErrIdempotencyConflict
	}
	now := timetext.Format(s.now())
	if _, err := tx.ExecContext(ctx, `UPDATE rate_publications SET status='retired',updated_at=? WHERE region=? AND status='active' AND id<>?`, now, publication.Region, id); err != nil {
		return RegionalRatePublication{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE rate_publications SET status='active',gizpay_publication_id=?,updated_at=? WHERE id=?`, gizPayID, now, id); err != nil {
		return RegionalRatePublication{}, err
	}
	if err := tx.Commit(); err != nil {
		return RegionalRatePublication{}, err
	}
	publication.Status, publication.UpdatedAt = "active", now
	publication.GizPayPublicationID = &gizPayID
	return publication, nil
}
