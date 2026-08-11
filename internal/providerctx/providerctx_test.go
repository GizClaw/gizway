package providerctx

import (
	"bytes"
	"context"
	"testing"
)

func TestIdempotencyKeyRoundTrip(t *testing.T) {
	if got := IdempotencyKey(context.Background()); got != "" {
		t.Fatalf("empty context key = %q", got)
	}
	ctx := WithIdempotencyKey(context.Background(), "gateway-request-123")
	if got := IdempotencyKey(ctx); got != "gateway-request-123" {
		t.Fatalf("context key = %q", got)
	}
}

func TestRecoveryRequestRoundTripUsesDefensiveCopies(t *testing.T) {
	original := RecoveryRequest{Method: "POST", RequestURI: "/v1/responses", ContentType: "application/json", Body: []byte(`{"model":"story-text"}`)}
	ctx := WithRecoveryRequest(context.Background(), original)
	original.Body[0] = 'x'

	got, ok := RecoveryRequestFrom(ctx)
	if !ok || got.Method != "POST" || got.RequestURI != "/v1/responses" || !bytes.Equal(got.Body, []byte(`{"model":"story-text"}`)) {
		t.Fatalf("RecoveryRequestFrom = %+v, %v", got, ok)
	}
	got.Body[0] = 'y'
	again, _ := RecoveryRequestFrom(ctx)
	if !bytes.Equal(again.Body, []byte(`{"model":"story-text"}`)) {
		t.Fatalf("stored recovery body was mutated: %q", again.Body)
	}
}

func TestRecoveryExecutionMarker(t *testing.T) {
	if IsRecoveryExecution(context.Background()) {
		t.Fatal("background context unexpectedly marked as recovery")
	}
	if !IsRecoveryExecution(WithRecoveryExecution(context.Background())) {
		t.Fatal("recovery marker was not retained")
	}
}
