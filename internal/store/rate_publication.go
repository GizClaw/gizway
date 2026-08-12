package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/idy/gizway/internal/timetext"
)

type PublishedPrice struct {
	ModelVariantID            string `db:"model_variant_id" json:"model_variant_id"`
	PublicModel               string `db:"public_model" json:"public_model"`
	Metric                    string `db:"metric" json:"metric"`
	UnitSize                  int64  `db:"unit_size" json:"unit_size"`
	BasePriceMicrocredits     int64  `db:"base_price_microcredits" json:"base_price_microcredits"`
	CustomerPriceMicrocredits int64  `db:"customer_price_microcredits" json:"customer_price_microcredits"`
	DiscountBPS               int    `db:"discount_bps" json:"discount_bps"`
}

type RatePublication struct {
	ID                  string           `db:"id" json:"id"`
	NodeID              string           `db:"node_id" json:"-"`
	Region              string           `db:"region" json:"-"`
	SourcePublicationID string           `db:"source_publication_id" json:"source_publication_id"`
	Revision            int64            `db:"revision" json:"revision"`
	PayloadHash         []byte           `db:"payload_hash" json:"-"`
	Status              string           `db:"status" json:"status"`
	EffectiveAt         string           `db:"effective_at" json:"effective_at"`
	CreatedAt           string           `db:"created_at" json:"created_at"`
	Prices              []PublishedPrice `json:"prices,omitempty"`
}

// PublishRatePublication appends one immutable regional snapshot. Exact replay
// is safe; an ID reused with any changed canonical content is a conflict.
func (s *Store) PublishRatePublication(ctx context.Context, nodeID, region, sourceID string, revision int64, effectiveAt string, payloadHash []byte, prices []PublishedPrice) (RatePublication, bool, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return RatePublication{}, false, err
	}
	defer tx.Rollback()
	// mTLS authentication proves the node exists. Locking that stable identity
	// serializes source-ID/revision allocation for this node, turning concurrent
	// retries into deterministic replay or conflict instead of raw unique-key
	// database errors.
	var lockedNode string
	if err := tx.GetContext(ctx, &lockedNode, `SELECT id FROM gateway_nodes WHERE id=? AND region=? FOR UPDATE`, nodeID, region); err != nil {
		return RatePublication{}, false, fmt.Errorf("lock publishing Gateway node: %w", err)
	}
	var existing RatePublication
	err = tx.GetContext(ctx, &existing, `SELECT id,node_id,region,source_publication_id,revision,payload_hash,status,effective_at,created_at
		FROM billing_rate_publications WHERE node_id=? AND source_publication_id=?`, nodeID, sourceID)
	if err == nil {
		if existing.Region != region || existing.Revision != revision || existing.EffectiveAt != effectiveAt || !bytes.Equal(existing.PayloadHash, payloadHash) {
			return RatePublication{}, false, ErrIdempotencyConflict
		}
		return existing, true, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return RatePublication{}, false, fmt.Errorf("lookup rate publication: %w", err)
	}
	var revisionOwner string
	err = tx.GetContext(ctx, &revisionOwner, `SELECT source_publication_id FROM billing_rate_publications WHERE node_id=? AND revision=?`, nodeID, revision)
	if err == nil {
		return RatePublication{}, false, ErrIdempotencyConflict
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return RatePublication{}, false, fmt.Errorf("lookup rate publication revision: %w", err)
	}
	createdAt := timetext.Format(s.now())
	id := "ratepub_" + uuid.NewString()
	if _, err := tx.ExecContext(ctx, `UPDATE billing_rate_publications SET status='retired'
		WHERE node_id=? AND region=? AND status='active'`, nodeID, region); err != nil {
		return RatePublication{}, false, fmt.Errorf("retire previous rate publication: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO billing_rate_publications
		(id,node_id,region,source_publication_id,revision,payload_hash,status,effective_at,created_at)
		VALUES (?,?,?,?,?,?,'active',?,?)`, id, nodeID, region, sourceID, revision, payloadHash, effectiveAt, createdAt); err != nil {
		return RatePublication{}, false, fmt.Errorf("insert rate publication: %w", err)
	}
	for _, price := range prices {
		if _, err := tx.ExecContext(ctx, `INSERT INTO billing_rate_versions
			(id,publication_id,model_variant_id,public_model,metric,unit_size,base_price_microcredits,customer_price_microcredits,discount_bps)
			VALUES (?,?,?,?,?,?,?,?,?)`, "ratever_"+uuid.NewString(), id, price.ModelVariantID, price.PublicModel,
			price.Metric, price.UnitSize, price.BasePriceMicrocredits, price.CustomerPriceMicrocredits, price.DiscountBPS); err != nil {
			return RatePublication{}, false, fmt.Errorf("insert published price: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return RatePublication{}, false, err
	}
	return RatePublication{ID: id, NodeID: nodeID, Region: region, SourcePublicationID: sourceID,
		Revision: revision, PayloadHash: append([]byte(nil), payloadHash...), Status: "active",
		EffectiveAt: effectiveAt, CreatedAt: createdAt}, false, nil
}

func (s *Store) GetRatePublication(ctx context.Context, nodeID, idOrSourceID string) (RatePublication, error) {
	var publication RatePublication
	err := s.db.GetContext(ctx, &publication, `SELECT id,node_id,region,source_publication_id,revision,payload_hash,status,effective_at,created_at
		FROM billing_rate_publications WHERE node_id=? AND id=?`, nodeID, idOrSourceID)
	if errors.Is(err, sql.ErrNoRows) {
		err = s.db.GetContext(ctx, &publication, `SELECT id,node_id,region,source_publication_id,revision,payload_hash,status,effective_at,created_at
			FROM billing_rate_publications WHERE node_id=? AND source_publication_id=?`, nodeID, idOrSourceID)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return RatePublication{}, ErrNotFound
	}
	if err != nil {
		return RatePublication{}, fmt.Errorf("get rate publication: %w", err)
	}
	return publication, nil
}

// DisableRatePublication is an operator stop for future Usage. Existing rows
// remain immutable and queryable, but quota ingestion no longer accepts new
// UCGIDs against the disabled snapshot.
func (s *Store) DisableRatePublication(ctx context.Context, actorID, id, reason, at string) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE billing_rate_publications SET status='disabled'
		WHERE id=? AND status IN ('active','retired')`, id)
	if err != nil {
		return fmt.Errorf("disable rate publication: %w", err)
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return ErrNotFound
	}
	if err := insertAudit(ctx, tx, actorID, "rate_publication.disabled", "rate_publication", id, reason, at); err != nil {
		return fmt.Errorf("audit rate publication disable: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit rate publication disable: %w", err)
	}
	return nil
}
