package api

import (
	"testing"
	"time"
)

func TestPowerSyncTokenIntegrityAndClaims(t *testing.T) {
	config := powerSyncConfig{Endpoint: "https://sync.example", Audience: "powersync", KeyID: "key-1", Key: []byte("01234567890123456789012345678901")}
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	token, err := signPowerSyncToken(config, powerSyncClaims{Subject: "user-1", Audience: "powersync", IssuedAt: now.Unix(), Expires: now.Add(5 * time.Minute).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := verifyPowerSyncToken(config, token, now)
	if err != nil || claims.Subject != "user-1" {
		t.Fatalf("claims=%+v err=%v", claims, err)
	}
	for name, candidate := range map[string]string{
		"tampered": token + "x",
		"shape":    "not-a-jwt",
		"expired": func() string {
			value, _ := signPowerSyncToken(config, powerSyncClaims{Subject: "user-1", Audience: "powersync", IssuedAt: now.Add(-10 * time.Minute).Unix(), Expires: now.Add(-time.Minute).Unix()})
			return value
		}(),
		"audience": func() string {
			value, _ := signPowerSyncToken(config, powerSyncClaims{Subject: "user-1", Audience: "other", IssuedAt: now.Unix(), Expires: now.Add(5 * time.Minute).Unix()})
			return value
		}(),
	} {
		if _, err := verifyPowerSyncToken(config, candidate, now); err == nil {
			t.Fatalf("%s token accepted", name)
		}
	}
}
