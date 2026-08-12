package api

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/idy/gizway/internal/store"
	"github.com/idy/gizway/internal/testdb"
)

func TestGizPayAdministratorBootstrapsNodeCertificateAndRateReceipt(t *testing.T) {
	database := testdb.OpenGizPay(t)
	repository := store.New(database.SQL)
	now := func() time.Time { return time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC) }
	repository.ConfigureClock(now)
	if _, _, err := repository.BootstrapAdministrator(t.Context(), "admin@gizpay.invalid", "GizPay Operator", "central-password", "2026-08-12T00:00:00.000000000Z"); err != nil {
		t.Fatal(err)
	}
	server := NewWithServicesAndClockSurface(repository, nil, nil, nil, now, nil, SurfaceGizPay)
	handler := server.Handler()
	login := regionalJSONRequest(t, handler, http.MethodPost, "/admin/v1/auth/login", "", "central-login", map[string]any{
		"email": "admin@gizpay.invalid", "password": "central-password",
	}, http.StatusOK)
	token := requiredString(t, login, "access_token")
	regionalJSONRequest(t, handler, http.MethodPost, "/admin/v1/gateway_nodes", token, "central-node", map[string]any{
		"id": "gw-global-central", "region": "global", "name": "Global Gateway",
	}, http.StatusCreated)
	regionalJSONRequest(t, handler, http.MethodPost, "/admin/v1/gateway_nodes", token, "central-node-replay", map[string]any{
		"id": "gw-global-central", "region": "global", "name": "Global Gateway",
	}, http.StatusOK)
	regionalJSONRequest(t, handler, http.MethodPost, "/admin/v1/gateway_nodes", token, "central-node-conflict", map[string]any{
		"id": "gw-global-central", "region": "global", "name": "Changed Gateway",
	}, http.StatusConflict)
	certificateBody := map[string]any{
		"fingerprint_sha256": "1111111111111111111111111111111111111111111111111111111111111111",
		"serial_number":      "serial-one", "subject_dn": "CN=gw-global-central",
		"san_uri":    "spiffe://gizway/gateway/global/gw-global-central",
		"not_before": "2026-08-11T00:00:00.000000000Z", "not_after": "2026-08-13T00:00:00.000000000Z",
	}
	certificate := regionalJSONRequest(t, handler, http.MethodPost, "/admin/v1/gateway_nodes/gw-global-central/certificates", token, "central-certificate", certificateBody, http.StatusCreated)
	certificateID := requiredString(t, certificate, "id")
	regionalJSONRequest(t, handler, http.MethodPost, "/admin/v1/gateway_nodes/gw-global-central/certificates", token, "central-certificate-replay", certificateBody, http.StatusOK)
	conflictingCertificate := make(map[string]any, len(certificateBody))
	maps.Copy(conflictingCertificate, certificateBody)
	conflictingCertificate["serial_number"] = "different-serial"
	regionalJSONRequest(t, handler, http.MethodPost, "/admin/v1/gateway_nodes/gw-global-central/certificates", token, "central-certificate-conflict", conflictingCertificate, http.StatusConflict)
	regionalJSONRequest(t, handler, http.MethodPost, "/admin/v1/gateway_nodes/gw-global-central/certificates/"+certificateID+"/activate", token, "central-activate", nil, http.StatusOK)
	node := regionalJSONRequest(t, handler, http.MethodGet, "/admin/v1/gateway_nodes/gw-global-central", token, "", nil, http.StatusOK)
	if certificates, _ := node["certificates"].([]any); len(certificates) != 1 {
		t.Fatalf("node response: %+v", node)
	}

	publication := ratePublicationRequest{
		SourcePublicationID: "global-source", Revision: 1, EffectiveAt: "2026-08-11T00:00:00Z",
		Prices: []store.PublishedPrice{{
			ModelVariantID: "global-variant", PublicModel: "global-model", Metric: "request", UnitSize: 1,
			BasePriceMicrocredits: 10, CustomerPriceMicrocredits: 9, DiscountBPS: 1000,
		}},
	}
	encoded, err := json.Marshal(publication)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/internal/v1/rate-publications", bytes.NewReader(encoded))
	ctx := context.WithValue(request.Context(), gatewayNodeIDKey, "gw-global-central")
	ctx = context.WithValue(ctx, gatewayRegionKey, "global")
	response := httptest.NewRecorder()
	server.publishRatePublication(response, request.WithContext(ctx))
	if response.Code != http.StatusCreated {
		t.Fatalf("publish status=%d body=%s", response.Code, response.Body.String())
	}
	var receipt map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	requiredString(t, receipt, "id")
	replayResponse := httptest.NewRecorder()
	server.publishRatePublication(replayResponse, httptest.NewRequest(http.MethodPost, "/internal/v1/rate-publications", bytes.NewReader(encoded)).WithContext(ctx))
	if replayResponse.Code != http.StatusOK {
		t.Fatalf("publication replay status=%d body=%s", replayResponse.Code, replayResponse.Body.String())
	}
	publication.Prices[0].CustomerPriceMicrocredits = 8
	changed, err := json.Marshal(publication)
	if err != nil {
		t.Fatal(err)
	}
	conflictResponse := httptest.NewRecorder()
	server.publishRatePublication(conflictResponse, httptest.NewRequest(http.MethodPost, "/internal/v1/rate-publications", bytes.NewReader(changed)).WithContext(ctx))
	if conflictResponse.Code != http.StatusConflict {
		t.Fatalf("publication conflict status=%d body=%s", conflictResponse.Code, conflictResponse.Body.String())
	}
	invalidResponse := httptest.NewRecorder()
	server.publishRatePublication(invalidResponse, httptest.NewRequest(http.MethodPost, "/internal/v1/rate-publications", bytes.NewReader([]byte(`{}`))).WithContext(ctx))
	if invalidResponse.Code != http.StatusBadRequest {
		t.Fatalf("invalid publication status=%d body=%s", invalidResponse.Code, invalidResponse.Body.String())
	}
	getRequest := httptest.NewRequest(http.MethodGet, "/internal/v1/rate-publications/"+publication.SourcePublicationID, nil).WithContext(ctx)
	getRequest.SetPathValue("source_publication_id", publication.SourcePublicationID)
	getResponse := httptest.NewRecorder()
	server.getRatePublication(getResponse, getRequest)
	if getResponse.Code != http.StatusOK {
		t.Fatalf("get publication status=%d body=%s", getResponse.Code, getResponse.Body.String())
	}
	missingRequest := httptest.NewRequest(http.MethodGet, "/internal/v1/rate-publications/missing", nil).WithContext(ctx)
	missingRequest.SetPathValue("source_publication_id", "missing")
	missingResponse := httptest.NewRecorder()
	server.getRatePublication(missingResponse, missingRequest)
	if missingResponse.Code != http.StatusNotFound {
		t.Fatalf("missing publication status=%d body=%s", missingResponse.Code, missingResponse.Body.String())
	}

	regionalJSONRequest(t, handler, http.MethodPost, "/admin/v1/gateway_nodes/gw-global-central/certificates/"+certificateID+"/revoke", token, "central-revoke", nil, http.StatusOK)
}
