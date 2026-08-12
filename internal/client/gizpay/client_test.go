package gizpay

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/idy/gizway/internal/service/quotaexchange"
)

func TestExchangeSendsRawKeyOnceWithoutIdempotencyOrIdentityCopies(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v1/quota/exchanges" || r.Method != http.MethodPost {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if value := r.Header.Get("Idempotency-Key"); value != "" {
			t.Fatalf("unexpected Idempotency-Key %q", value)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["api_key"] != "giz_customer_secret" {
			t.Fatalf("api_key = %#v", body["api_key"])
		}
		for _, forbidden := range []string{"account_id", "api_key_id", "quota_subject", "lease_id"} {
			if _, exists := body[forbidden]; exists {
				t.Fatalf("request contains forbidden field %s", forbidden)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"allowed","quota":{"asset":"GIZ_CREDIT","microcredits":77},"checked_at":"2026-08-12T00:00:00Z","recheck_after_seconds":300}`))
	}))
	defer server.Close()

	client := NewForTest(server.URL, server.Client())
	response, err := client.Exchange(context.Background(), "giz_customer_secret", []quotaexchange.UsageRecord{})
	if err != nil {
		t.Fatal(err)
	}
	if response.Quota.Microcredits != 77 || response.RecheckAfterSeconds != 300 {
		t.Fatalf("response = %+v", response)
	}
}

func TestExchangeErrorNeverIncludesRawKeyOrUntrustedBody(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "giz_customer_secret should not escape", http.StatusInternalServerError)
	}))
	defer server.Close()

	_, err := NewForTest(server.URL, server.Client()).Exchange(context.Background(), "giz_customer_secret", nil)
	if err == nil {
		t.Fatal("Exchange succeeded")
	}
	if strings.Contains(err.Error(), "giz_customer_secret") {
		t.Fatalf("error leaked raw key: %v", err)
	}
}

func TestExchangeHonorsThreeSecondOrEarlierCallerDeadline(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := NewForTest(server.URL, server.Client()).Exchange(ctx, "giz_customer_secret", nil)
	if err == nil || time.Since(started) > time.Second {
		t.Fatalf("deadline result err=%v duration=%s", err, time.Since(started))
	}
}

func TestExchangeClassifiesNodeKeyAndAvailabilityFailuresWithoutReadingErrorBodies(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, code string
		status     int
		want       error
	}{
		{name: "node identity", status: http.StatusUnauthorized, code: "invalid_node_identity", want: ErrInvalidNodeIdentity},
		{name: "customer key", status: http.StatusUnauthorized, code: "invalid_api_key", want: ErrInvalidAPIKey},
		{name: "bad payload", status: http.StatusBadRequest, code: "invalid_request", want: ErrInvalidExchangePayload},
		{name: "UCGID conflict", status: http.StatusConflict, code: "ucgid_conflict", want: ErrUsageConflict},
		{name: "unpriceable Usage", status: http.StatusUnprocessableEntity, code: "usage_unpriceable", want: ErrUsageUnpriceable},
		{name: "unavailable", status: http.StatusServiceUnavailable, code: "temporarily_unavailable", want: ErrTemporarilyUnavailable},
		{name: "internal error", status: http.StatusInternalServerError, code: "internal_error", want: ErrTemporarilyUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("X-Gizway-Error-Code", test.code)
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte("giz_customer_secret must never reach the caller"))
			}))
			defer server.Close()
			_, err := NewForTest(server.URL, server.Client()).Exchange(t.Context(), "giz_customer_secret", nil)
			if !errors.Is(err, test.want) || strings.Contains(err.Error(), "giz_customer_secret") {
				t.Fatalf("Exchange error=%v, want %v without raw credential", err, test.want)
			}
		})
	}
}

func TestExchangeRetryClassificationIsClosedOverEveryKnownFailure(t *testing.T) {
	t.Parallel()
	for _, err := range []error{ErrInvalidAPIKey, ErrInvalidNodeIdentity, ErrInvalidExchangePayload, ErrUsageConflict, ErrUsageUnpriceable} {
		if !IsPermanentExchangeError(err) {
			t.Fatalf("known permanent error was not classified as permanent: %v", err)
		}
	}
	if IsPermanentExchangeError(ErrTemporarilyUnavailable) || IsPermanentExchangeError(errors.New("local database unavailable")) {
		t.Fatal("temporary or unknown internal error was classified as permanent")
	}
}

func TestExchangeRejectsTrailingOrOversizedSuccessPayload(t *testing.T) {
	t.Parallel()
	for _, suffix := range []string{"{}", strings.Repeat(" ", maximumExchangeBody)} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"status":"allowed","quota":{"asset":"GIZ_CREDIT","microcredits":1},"checked_at":"2026-08-12T00:00:00Z","recheck_after_seconds":300}` + suffix))
		}))
		_, err := NewForTest(server.URL, server.Client()).Exchange(t.Context(), "giz_customer_secret", nil)
		server.Close()
		if err == nil {
			t.Fatalf("Exchange accepted trailing payload of %d bytes", len(suffix))
		}
	}
}

func TestRatePublicationCarriesImmutableSourceMetadataAndRecoversBySourceID(t *testing.T) {
	t.Parallel()
	const sourceID = "regional-source-publication"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/internal/v1/rate-publications":
			var body struct {
				SourcePublicationID string           `json:"source_publication_id"`
				Revision            int64            `json:"revision"`
				EffectiveAt         string           `json:"effective_at"`
				Prices              []PublishedPrice `json:"prices"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.SourcePublicationID != sourceID ||
				body.Revision != 7 || body.EffectiveAt == "" || len(body.Prices) != 1 ||
				body.Prices[0].BasePriceMicrocredits != 10 || body.Prices[0].DiscountBPS != 1000 {
				t.Fatalf("publication request = %+v, err=%v", body, err)
			}
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodGet && r.URL.Path == "/internal/v1/rate-publications/"+sourceID:
		default:
			t.Fatalf("unexpected publication request %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"id":"ratepub_center","source_publication_id":"regional-source-publication","revision":7,"status":"active","effective_at":"2026-08-12T00:00:00Z","content_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","created_at":"2026-08-12T00:00:00Z"}`))
	}))
	defer server.Close()

	client := NewForTest(server.URL, server.Client())
	prices := []PublishedPrice{{ModelVariantID: "variant", PublicModel: "model", Metric: "request", UnitSize: 1,
		BasePriceMicrocredits: 10, CustomerPriceMicrocredits: 9, DiscountBPS: 1000}}
	published, err := client.PublishRatePublication(t.Context(), sourceID, 7, "2026-08-12T00:00:00Z", prices)
	if err != nil || published.ID != "ratepub_center" || published.SourcePublicationID != sourceID {
		t.Fatalf("PublishRatePublication = %+v, %v", published, err)
	}
	recovered, err := client.GetRatePublication(t.Context(), sourceID)
	if err != nil || recovered.ID != published.ID {
		t.Fatalf("GetRatePublication = %+v, %v", recovered, err)
	}
}

func TestReadinessRequiresAnExplicitReadyResponse(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		code int
		body string
		want error
	}{
		{name: "ready", code: http.StatusOK, body: `{"status":"ready","node_id":"gw-cn-01","region":"cn"}`},
		{name: "wrong certificate identity", code: http.StatusOK, body: `{"status":"ready","node_id":"gw-global-01","region":"global"}`, want: ErrInvalidNodeIdentity},
		{name: "not ready", code: http.StatusServiceUnavailable, body: `{"status":"not_ready","node_id":"gw-cn-01","region":"cn"}`, want: ErrTemporarilyUnavailable},
		{name: "certificate rejected", code: http.StatusUnauthorized, body: `{}`, want: ErrInvalidNodeIdentity},
		{name: "malformed success", code: http.StatusOK, body: `{}`, want: ErrTemporarilyUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/internal/v1/readyz" {
					t.Fatalf("readiness request=%s %s", r.Method, r.URL.Path)
				}
				w.WriteHeader(test.code)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			err := NewForTest(server.URL, server.Client()).CheckReadiness(t.Context(), "gw-cn-01", "cn")
			if !errors.Is(err, test.want) {
				t.Fatalf("CheckReadiness error=%v, want %v", err, test.want)
			}
		})
	}
}

func TestRatePublicationFailuresAreTypedAndSuccessBodiesAreStrict(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		status int
		want   error
	}{
		{name: "certificate", status: http.StatusUnauthorized, want: ErrInvalidNodeIdentity},
		{name: "unavailable", status: http.StatusServiceUnavailable, want: ErrTemporarilyUnavailable},
		{name: "unexpected", status: http.StatusTeapot},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte("untrusted response"))
			}))
			defer server.Close()
			client := NewForTest(server.URL, server.Client())
			if _, err := client.PublishRatePublication(t.Context(), "source", 1, "2026-08-12T00:00:00Z", nil); err == nil || (test.want != nil && !errors.Is(err, test.want)) {
				t.Fatalf("PublishRatePublication error=%v, want %v", err, test.want)
			}
			if _, err := client.GetRatePublication(t.Context(), "source"); err == nil || (test.want != nil && !errors.Is(err, test.want)) {
				t.Fatalf("GetRatePublication error=%v, want %v", err, test.want)
			}
		})
	}

	for _, body := range []string{
		`{}`,
		`{"id":"id","source_publication_id":"source","revision":1,"status":"active","effective_at":"2026-08-12T00:00:00Z","content_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}{}`,
		strings.Repeat(" ", maximumExchangeBody+1),
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				w.WriteHeader(http.StatusCreated)
			}
			_, _ = w.Write([]byte(body))
		}))
		client := NewForTest(server.URL, server.Client())
		if _, err := client.PublishRatePublication(t.Context(), "source", 1, "2026-08-12T00:00:00Z", nil); err == nil {
			t.Fatalf("PublishRatePublication accepted malformed body of %d bytes", len(body))
		}
		if _, err := client.GetRatePublication(t.Context(), "source"); err == nil {
			t.Fatalf("GetRatePublication accepted malformed body of %d bytes", len(body))
		}
		server.Close()
	}
}

func TestClientConfigurationAndExchangeValidationFailures(t *testing.T) {
	t.Parallel()
	if _, err := New("http://gizpay.invalid", http.DefaultClient); err == nil {
		t.Fatal("production client accepted HTTP")
	}
	if _, err := New("https://gizpay.invalid", nil); err == nil {
		t.Fatal("client accepted nil HTTP transport")
	}
	for _, body := range []string{
		`{"status":"allowed","quota":{"asset":"OTHER","microcredits":1},"checked_at":"2026-08-12T00:00:00Z","recheck_after_seconds":300}`,
		`{"status":"denied","quota":{"asset":"GIZ_CREDIT","microcredits":1},"checked_at":"2026-08-12T00:00:00Z","recheck_after_seconds":300}`,
		`{"status":"unknown","quota":{"asset":"GIZ_CREDIT","microcredits":0},"checked_at":"2026-08-12T00:00:00Z","recheck_after_seconds":0}`,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
		_, err := NewForTest(server.URL, server.Client()).Exchange(t.Context(), "giz_customer_secret", nil)
		server.Close()
		if err == nil {
			t.Fatalf("Exchange accepted invalid response %s", body)
		}
	}
	large := []quotaexchange.UsageRecord{{UCGID: strings.Repeat("x", maximumExchangeBody)}}
	if _, err := NewForTest("http://127.0.0.1:1", http.DefaultClient).Exchange(t.Context(), "giz_customer_secret", large); err == nil || !strings.Contains(err.Error(), "256 KiB") {
		t.Fatalf("oversized Exchange error=%v", err)
	}
}
