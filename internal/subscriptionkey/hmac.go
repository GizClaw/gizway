// Package subscriptionkey owns the stable cross-region identity derived from
// an exact Subscription API Key value.
package subscriptionkey

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
)

// HMAC returns Base64URL-NoPadding(HMAC-SHA-256(secret, exact key bytes)).
func HMAC(secret []byte, raw string) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(raw))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
