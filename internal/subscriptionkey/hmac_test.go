package subscriptionkey

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

func TestHMACUsesExactBytesAndBase64URLWithoutPadding(t *testing.T) {
	secret := []byte("milestone-03-shared-secret")
	raw := "  Giz_Key-MixedCase  "
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(raw))
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	if got := HMAC(secret, raw); got != want {
		t.Fatalf("HMAC() = %q, want %q", got, want)
	}
	if len(want) != 43 {
		t.Fatalf("SHA-256 Base64URL digest length = %d, want 43", len(want))
	}
	if HMAC(secret, raw) == HMAC(secret, raw+"\n") {
		t.Fatal("HMAC normalized exact Subscription Key bytes")
	}
}
