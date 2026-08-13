package gizpay

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"math"
	"testing"
)

func TestPlatformFeeRoundsUpWithoutOverflow(t *testing.T) {
	tests := []struct {
		gross, bps, want int64
	}{
		{1, 200, 1},
		{100, 200, 2},
		{101, 200, 3},
		{math.MaxInt64, 10000, math.MaxInt64},
	}
	for _, test := range tests {
		if got := platformFee(test.gross, test.bps); got != test.want {
			t.Errorf("platformFee(%d, %d) = %d, want %d", test.gross, test.bps, got, test.want)
		}
	}
}

func TestSubscriptionKeyEncryptionUsesStoredVersion(t *testing.T) {
	handler := &Handler{aead: map[int]cipher.AEAD{
		1: testAEAD(t, "old-secret"),
		2: testAEAD(t, "active-secret"),
	}}
	ciphertext, err := handler.encrypt("gzk_live_raw", 2)
	if err != nil {
		t.Fatal(err)
	}
	if plain, err := handler.decrypt(ciphertext, 2); err != nil || plain != "gzk_live_raw" {
		t.Fatalf("decrypt with stored encryption_version = %q, %v", plain, err)
	}
	if _, err := handler.decrypt(ciphertext, 1); err == nil {
		t.Fatal("ciphertext decrypted with a different encryption_version")
	}
	if _, err := handler.encrypt("raw", 3); err == nil {
		t.Fatal("unknown active encryption version was silently accepted")
	}
}

func testAEAD(t *testing.T, secret string) cipher.AEAD {
	t.Helper()
	key := sha256.Sum256([]byte(secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	return aead
}
