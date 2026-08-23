package gizway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/GizClaw/gizway/internal/identity"
	"github.com/GizClaw/gizway/internal/testdb"
)

func TestMilestone04OwnerMerchantUsesMerchantListAndCachesMapping(t *testing.T) {
	if os.Getenv("GIZWAY_TEST_POSTGRES_DSN") == "" {
		t.Skip("GIZWAY_TEST_POSTGRES_DSN is required")
	}
	db := testdb.OpenGizWay(t).SQL
	requests := 0
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodGet || r.URL.Path != "/account/v1/merchants" {
			t.Fatalf("Merchant lookup request=%s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer human" {
			t.Fatalf("Authorization=%q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"merchant-m04","is_default":true}]}`))
	}))
	defer remote.Close()
	handler := &Handler{config: Config{DB: db, GizPayURL: remote.URL, HTTPClient: remote.Client()}}
	principal := identity.Principal{Issuer: "issuer-m04", Subject: "subject-m04"}
	for range 2 {
		merchantID, err := handler.ownerMerchant(context.Background(), "Bearer human", principal)
		if err != nil {
			t.Fatal(err)
		}
		if merchantID != "merchant-m04" {
			t.Fatalf("merchantID=%q", merchantID)
		}
	}
	if requests != 1 {
		t.Fatalf("GizPay Merchant List requests=%d, want 1", requests)
	}
}

func TestMilestone04OwnerMerchantClassifiesRemoteFailures(t *testing.T) {
	if os.Getenv("GIZWAY_TEST_POSTGRES_DSN") == "" {
		t.Skip("GIZWAY_TEST_POSTGRES_DSN is required")
	}
	for _, test := range []struct {
		name      string
		status    int
		body      string
		transport bool
		want      error
	}{
		{name: "missing default", status: http.StatusOK, body: `{"data":[]}`, want: errDefaultMerchantInvariantViolation},
		{name: "multiple defaults", status: http.StatusOK, body: `{"data":[{"id":"one","is_default":true},{"id":"two","is_default":true}]}`, want: errDefaultMerchantInvariantViolation},
		{name: "GizPay unavailable", status: http.StatusServiceUnavailable, body: `{}`, want: errGizPayUnavailable},
		{name: "transport unavailable", transport: true, want: errGizPayUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := testdb.OpenGizWay(t).SQL
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if test.transport {
					panic(http.ErrAbortHandler)
				}
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer remote.Close()
			handler := &Handler{config: Config{DB: db, GizPayURL: remote.URL, HTTPClient: remote.Client()}}
			_, err := handler.ownerMerchant(context.Background(), "Bearer human", identity.Principal{Issuer: "issuer-" + test.name, Subject: "subject"})
			if !errors.Is(err, test.want) {
				t.Fatalf("ownerMerchant error=%v, want %v", err, test.want)
			}
		})
	}
}

func TestMilestone04MarkProviderKeyUsedUpdatesBothReadModelsWithoutCharge(t *testing.T) {
	if os.Getenv("GIZWAY_TEST_POSTGRES_DSN") == "" {
		t.Skip("GIZWAY_TEST_POSTGRES_DSN is required")
	}
	db := testdb.OpenGizWay(t).SQL
	now := time.Date(2026, 8, 16, 8, 0, 0, 0, time.UTC)
	_, err := db.Exec(`INSERT INTO provider_key_billing(provider_key_id,owner_identity_issuer,owner_identity_subject,merchant_id,name) VALUES('pkey-used','issuer','subject','merchant','Used')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO client_sync.provider_keys(id,provider_id,key,merchant_id,owner_identity_issuer,owner_identity_subject,name,status,prices_json,created_at,updated_at) VALUES('pkey-used','provider','secret','merchant','issuer','subject','Used','active','[]',$1,$1)`, now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	handler := &Handler{config: Config{DB: db}}
	if err := handler.markProviderKeyUsed(context.Background(), "pkey-used", now); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"provider_key_billing", "client_sync.provider_keys"} {
		var got time.Time
		column := "provider_key_id"
		if table == "client_sync.provider_keys" {
			column = "id"
		}
		if err := db.Get(&got, `SELECT last_used_at FROM `+table+` WHERE `+column+`='pkey-used'`); err != nil {
			t.Fatal(err)
		}
		if !got.Equal(now) {
			t.Fatalf("%s last_used_at=%v, want %v", table, got, now)
		}
	}
}
