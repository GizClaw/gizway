package store

import (
	"context"
	"crypto/sha256"
	"strings"
	"testing"

	"github.com/idy/gizway/internal/testdb"
)

func TestSecretCipherAndDatabaseBoundaries(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	cipher, err := newSecretCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := cipher.encrypt("provider-secret")
	if err != nil || !strings.HasPrefix(sealed, encryptedSecretPrefix) || strings.Contains(sealed, "provider-secret") {
		t.Fatalf("encrypted secret = %q, %v", sealed, err)
	}
	plaintext, err := cipher.decrypt(sealed)
	if err != nil || plaintext != "provider-secret" {
		t.Fatalf("decrypted secret = %q, %v", plaintext, err)
	}
	wrong, _ := newSecretCipher([]byte("abcdef0123456789abcdef0123456789"))
	if _, err := wrong.decrypt(sealed); err == nil {
		t.Fatal("wrong key authenticated ciphertext")
	}
	if value, err := cipher.decrypt("legacy/reference"); err != nil || value != "" {
		t.Fatalf("legacy provider reference became executable: %q, %v", value, err)
	}
	if _, err := newSecretCipher([]byte("short")); err == nil {
		t.Fatal("short encryption key succeeded")
	}

	database := testdb.OpenStory(t)
	defer database.Close()
	repository, err := NewWithSecretKey(database.SQL, key)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	endpointID := "71000000-0000-4000-8000-000000000001"
	if err := repository.RotateProviderCredential(ctx, "41000000-0000-4000-8000-000000000001", endpointID, "rotated-provider-secret", "2026-08-10T00:00:01.000000000Z"); err != nil {
		t.Fatal(err)
	}
	target, err := repository.ResolveVariantExecutionTarget(ctx, "91000000-0000-4000-8000-000000000001")
	if err != nil || target.Credential != "rotated-provider-secret" {
		t.Fatalf("execution target = %+v, %v", target, err)
	}
	var storedProviderSecret string
	if err := database.SQL.Get(&storedProviderSecret, `SELECT credential_ref FROM provider_endpoints WHERE id=$1`, endpointID); err != nil || !strings.HasPrefix(storedProviderSecret, encryptedSecretPrefix) || strings.Contains(storedProviderSecret, "rotated-provider-secret") {
		t.Fatalf("stored provider credential = %q, %v", storedProviderSecret, err)
	}

	webhook := WebhookEndpoint{ID: "encrypted-webhook", URL: "https://merchant.example/hook", Events: JSON(`["payment_intent.succeeded"]`), SigningSecret: "whsec_plaintext", CreatedAt: "2026-08-10T00:00:00.000000000Z"}
	payloadHash := sha256.Sum256([]byte("webhook"))
	if _, _, err := repository.CreateWebhookEndpoint(ctx, "22000000-0000-4000-8000-000000000002", "secret-test-create", payloadHash[:], webhook); err != nil {
		t.Fatal(err)
	}
	var storedWebhookSecret string
	if err := database.SQL.Get(&storedWebhookSecret, `SELECT signing_secret FROM webhook_endpoints WHERE id=$1`, webhook.ID); err != nil || !strings.HasPrefix(storedWebhookSecret, encryptedSecretPrefix) || strings.Contains(storedWebhookSecret, webhook.SigningSecret) {
		t.Fatalf("stored webhook secret = %q, %v", storedWebhookSecret, err)
	}
	if _, err := database.SQL.Exec(`INSERT INTO webhook_events(id,merchant_account_id,event_type,resource_id,payload,created_at) VALUES ('encrypted-event','22000000-0000-4000-8000-000000000002','payment_intent.succeeded','intent','{}','2026-08-10T00:00:00.000000000Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.SQL.Exec(`INSERT INTO webhook_deliveries(id,event_id,endpoint_id,signing_secret_snapshot,attempt,status,created_at) VALUES ('encrypted-delivery','encrypted-event','encrypted-webhook',$1,1,'pending','2026-08-10T00:00:00.000000000Z')`, storedWebhookSecret); err != nil {
		t.Fatal(err)
	}
	if err := repository.ClaimWebhookDelivery(ctx, "encrypted-delivery", "2026-08-10T00:00:00.000000000Z", "2026-08-10T00:00:30.000000000Z"); err != nil {
		t.Fatal(err)
	}
	rotationHash := sha256.Sum256([]byte(webhook.ID))
	if _, _, err := repository.RotateWebhookEndpointSecret(ctx, "22000000-0000-4000-8000-000000000002", webhook.ID, "secret-test-rotate", rotationHash[:], "whsec_rotated", "2026-08-10T00:00:01.000000000Z"); err != nil {
		t.Fatal(err)
	}
	delivery, err := repository.GetDeliveryTarget(ctx, "encrypted-delivery")
	if err != nil || delivery.Secret != webhook.SigningSecret {
		t.Fatalf("delivery target secret = %q, %v", delivery.Secret, err)
	}
}
