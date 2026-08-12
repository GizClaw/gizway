package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/idy/gizway/internal/store"
	"github.com/idy/gizway/internal/testdb"
)

func TestPostgreSQLRatePublicationSeparatesSourceAndFinancialIdentity(t *testing.T) {
	database := testdb.OpenGizPay(t)
	defer database.Close()
	if _, err := database.SQL.Exec(`INSERT INTO gateway_nodes(id,region,name,created_at,updated_at)
		VALUES ('story-global','global','Global test node','2026-08-11T00:00:00.000000000Z','2026-08-11T00:00:00.000000000Z')`); err != nil {
		t.Fatal(err)
	}
	repository := store.New(database.SQL)
	repository.ConfigureClock(func() time.Time { return time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC) })
	price := store.PublishedPrice{ModelVariantID: "regional-variant", PublicModel: "regional-model", Metric: "request", UnitSize: 1,
		BasePriceMicrocredits: 10, CustomerPriceMicrocredits: 9, DiscountBPS: 1000}
	digest := make([]byte, 32)
	digest[0] = 7
	publication, replayed, err := repository.PublishRatePublication(t.Context(), "story-global", "global",
		"regional-source", 2, "2026-08-12T00:00:00.000000000Z", digest, []store.PublishedPrice{price})
	if err != nil || replayed || publication.ID == "regional-source" || publication.SourcePublicationID != "regional-source" {
		t.Fatalf("first publication=%+v replayed=%t err=%v", publication, replayed, err)
	}
	replayedPublication, replayed, err := repository.PublishRatePublication(t.Context(), "story-global", "global",
		"regional-source", 2, "2026-08-12T00:00:00.000000000Z", digest, []store.PublishedPrice{price})
	if err != nil || !replayed || replayedPublication.ID != publication.ID {
		t.Fatalf("publication replay=%+v replayed=%t err=%v", replayedPublication, replayed, err)
	}
	for _, lookup := range []string{publication.ID, publication.SourcePublicationID} {
		found, err := repository.GetRatePublication(t.Context(), "story-global", lookup)
		if err != nil || found.ID != publication.ID {
			t.Fatalf("publication lookup %q=%+v err=%v", lookup, found, err)
		}
	}
	changed := append([]byte(nil), digest...)
	changed[0]++
	if _, _, err := repository.PublishRatePublication(t.Context(), "story-global", "global",
		"regional-source", 2, publication.EffectiveAt, changed, []store.PublishedPrice{price}); !errors.Is(err, store.ErrIdempotencyConflict) {
		t.Fatalf("changed source replay err=%v", err)
	}
	if _, _, err := repository.PublishRatePublication(t.Context(), "story-global", "global",
		"different-source", 2, publication.EffectiveAt, changed, []store.PublishedPrice{price}); !errors.Is(err, store.ErrIdempotencyConflict) {
		t.Fatalf("duplicate node revision err=%v", err)
	}
	var base int64
	var discount int
	if err := database.SQL.QueryRow(`SELECT base_price_microcredits,discount_bps FROM billing_rate_versions WHERE publication_id=$1`, publication.ID).Scan(&base, &discount); err != nil || base != 10 || discount != 1000 {
		t.Fatalf("immutable rate fields base=%d discount=%d err=%v", base, discount, err)
	}
	page, err := repository.AdminRowsPage(t.Context(), "rate_publications", store.AdminListQuery{NodeID: "story-global", Region: "global", Status: "active"})
	if err != nil || len(page.Items) != 1 || page.Items[0]["id"] != publication.ID {
		t.Fatalf("admin publication page=%+v err=%v", page, err)
	}
	if err := repository.DisableRatePublication(t.Context(), "story-admin", publication.ID, "operator stop", publication.CreatedAt); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := database.SQL.Get(&status, `SELECT status FROM billing_rate_publications WHERE id=$1`, publication.ID); err != nil || status != "disabled" {
		t.Fatalf("disabled publication status=%q err=%v", status, err)
	}
	var audits int
	if err := database.SQL.Get(&audits, `SELECT COUNT(*) FROM audit_events WHERE action='rate_publication.disabled' AND resource_id=$1`, publication.ID); err != nil || audits != 1 {
		t.Fatalf("disable audit count=%d err=%v", audits, err)
	}
}
