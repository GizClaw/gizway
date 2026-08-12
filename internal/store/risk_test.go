package store_test

import (
	"crypto/sha256"
	"errors"
	"testing"

	"github.com/idy/gizway/internal/store"
)

const merchantAccountID = "22000000-0000-4000-8000-000000000002"

func TestMerchantServiceRiskLifecycle(t *testing.T) {
	repository := newStore(t)
	ctx := t.Context()
	if err := repository.AuthorizeMerchantServiceCreation(ctx, userOneID, merchantAccountID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("cross-tenant authorization = %v", err)
	}
	if err := repository.AuthorizeMerchantServiceCreation(ctx, "11000000-0000-4000-8000-000000000002", merchantAccountID); err != nil {
		t.Fatalf("owner authorization: %v", err)
	}

	payload := sha256.Sum256([]byte("service-one"))
	service := store.MerchantService{
		ID: "service-test-1", MerchantAccountID: merchantAccountID,
		ServiceCode: "vpn-test", Name: "VPN Test", InterfaceSet: store.JSON(`["checkout"]`),
		Status: "pending", MaxTransactionMicrocredits: 1000, DailyLimitMicrocredits: 5000,
		CreatedAt: "2026-08-10T00:00:00.000000000Z", UpdatedAt: "2026-08-10T00:00:00.000000000Z",
	}
	risk := store.RiskDecision{
		ID: "risk-test-1", MerchantAccountID: merchantAccountID, ServiceID: service.ID,
		ProviderReference: "provider-risk-1", Decision: "allow", KYCStatus: "verified",
		KYBStatus: "verified", SanctionsStatus: "clear", AnomalyScore: 4,
		Reason: "checks passed", CreatedAt: service.CreatedAt,
	}
	created, assessment, replayed, err := repository.CreateMerchantService(ctx, "11000000-0000-4000-8000-000000000002", "merchant-service-key", payload[:], service, risk)
	if err != nil || replayed || created.ID != service.ID || assessment.Decision != "allow" {
		t.Fatalf("CreateMerchantService = %+v, %+v, %v, %v", created, assessment, replayed, err)
	}
	created, _, replayed, err = repository.CreateMerchantService(ctx, "11000000-0000-4000-8000-000000000002", "merchant-service-key", payload[:], service, risk)
	if err != nil || !replayed || created.ID != service.ID {
		t.Fatalf("CreateMerchantService replay = %+v, %v, %v", created, replayed, err)
	}
	otherPayload := sha256.Sum256([]byte("different"))
	if _, _, _, err := repository.CreateMerchantService(ctx, "11000000-0000-4000-8000-000000000002", "merchant-service-key", otherPayload[:], service, risk); !errors.Is(err, store.ErrIdempotencyConflict) {
		t.Fatalf("payload conflict = %v", err)
	}

	services, err := repository.ListMerchantServices(ctx, "11000000-0000-4000-8000-000000000002", merchantAccountID)
	found := false
	for _, listed := range services {
		found = found || listed.ID == service.ID
	}
	if err != nil || len(services) != 2 || !found {
		t.Fatalf("ListMerchantServices = %d, %v", len(services), err)
	}
	approved, err := repository.DecideMerchantService(ctx, "90000000-0000-4000-8000-000000000001", service.ID, "approve", "approved after risk", "2026-08-10T00:00:01.000000000Z")
	if err != nil || approved.Status != "approved" {
		t.Fatalf("approve service = %+v, %v", approved, err)
	}
	if _, err := repository.DecideMerchantService(ctx, "admin", service.ID, "suspend", "incident", "2026-08-10T00:00:02.000000000Z"); err != nil {
		t.Fatalf("suspend service: %v", err)
	}
	if _, err := repository.DecideMerchantService(ctx, "admin", service.ID, "reactivate", "resolved", "2026-08-10T00:00:03.000000000Z"); err != nil {
		t.Fatalf("reactivate service: %v", err)
	}
	if _, err := repository.DecideMerchantService(ctx, "admin", service.ID, "invalid", "bad", "2026-08-10T00:00:04.000000000Z"); err == nil {
		t.Fatal("invalid service decision succeeded")
	}
}

func TestMerchantServiceRiskDenialAndPaymentLimits(t *testing.T) {
	repository := newStore(t)
	ctx := t.Context()
	payload := sha256.Sum256([]byte("blocked-service"))
	service := store.MerchantService{ID: "service-blocked", MerchantAccountID: merchantAccountID, ServiceCode: "blocked", Name: "Blocked", InterfaceSet: store.JSON(`["checkout"]`), Status: "pending", MaxTransactionMicrocredits: 100, DailyLimitMicrocredits: 200, CreatedAt: "2026-08-10T00:00:00.000000000Z", UpdatedAt: "2026-08-10T00:00:00.000000000Z"}
	risk := store.RiskDecision{ID: "risk-blocked", MerchantAccountID: merchantAccountID, ServiceID: service.ID, ProviderReference: "provider-risk-blocked", Decision: "deny", KYCStatus: "verified", KYBStatus: "verified", SanctionsStatus: "match", Reason: "sanctions match", CreatedAt: service.CreatedAt}
	if _, _, _, err := repository.CreateMerchantService(ctx, "11000000-0000-4000-8000-000000000002", "blocked-key", payload[:], service, risk); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.DecideMerchantService(ctx, "admin", service.ID, "approve", "override", "2026-08-10T00:00:01.000000000Z"); !errors.Is(err, store.ErrRiskDenied) {
		t.Fatalf("blocked approval = %v", err)
	}
	if _, err := repository.DecideMerchantService(ctx, "admin", "missing", "approve", "missing", "2026-08-10T00:00:01.000000000Z"); !errors.Is(err, store.ErrRiskDenied) {
		t.Fatalf("missing risk approval = %v", err)
	}

	intentHash := sha256.Sum256([]byte("intent"))
	intent := store.PaymentIntent{ID: "intent-over-limit", MerchantAccountID: merchantAccountID, ServiceID: "23000000-0000-4000-8000-000000000001", ExternalOrderID: "over", Amount: store.CreditAmount{Asset: "GIZ_CREDIT", Microcredits: 10_000_001}, PlatformFee: store.CreditAmount{Asset: "GIZ_CREDIT", Microcredits: 1}, NetAmount: store.CreditAmount{Asset: "GIZ_CREDIT", Microcredits: 10_000_000}, FeeBPS: 250, Status: "created", Metadata: store.JSON(`{}`), ExpiresAt: "2027-08-10T00:00:00.000000000Z", CreatedAt: "2026-08-10T00:00:00.000000000Z"}
	if _, _, err := repository.CreatePaymentIntentForKey(ctx, merchantAccountID, "", "over-limit", intentHash[:], intent); !errors.Is(err, store.ErrRiskDenied) {
		t.Fatalf("over-limit payment intent = %v", err)
	}
}
