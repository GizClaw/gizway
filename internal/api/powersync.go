package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/idy/gizway/internal/store"
	"github.com/idy/gizway/internal/timetext"
)

type powerSyncConfig struct {
	Endpoint string
	Audience string
	KeyID    string
	Key      []byte
}

// ConfigurePowerSync enables short-lived custom-auth credentials. Only a
// signed user subject is placed in the JWT; account membership is derived by
// the sync-rule parameter query from the source accounts table, so a client
// cannot widen its bucket by supplying an account_id parameter.
func (s *Server) ConfigurePowerSync(endpoint, audience, keyID string, key []byte) {
	s.powerSync = powerSyncConfig{
		Endpoint: strings.TrimSpace(endpoint), Audience: strings.TrimSpace(audience),
		KeyID: strings.TrimSpace(keyID), Key: append([]byte(nil), key...),
	}
}

type powerSyncClaims struct {
	Subject  string `json:"sub"`
	Audience string `json:"aud"`
	IssuedAt int64  `json:"iat"`
	Expires  int64  `json:"exp"`
}

func (s *Server) createPowerSyncCredentials(w http.ResponseWriter, r *http.Request) {
	config := s.powerSync
	if config.Endpoint == "" || config.Audience == "" || config.KeyID == "" || len(config.Key) < 32 {
		writeError(w, http.StatusServiceUnavailable, "service_unavailable", "PowerSync authentication is not configured")
		return
	}
	now := s.now().UTC()
	claims := powerSyncClaims{
		Subject: contextString(r.Context(), userIDKey), Audience: config.Audience,
		IssuedAt: now.Unix(), Expires: now.Add(5 * time.Minute).Unix(),
	}
	token, err := signPowerSyncToken(config, claims)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "PowerSync credential could not be issued")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"endpoint": config.Endpoint, "token": token,
		"expires_at": timetext.Format(time.Unix(claims.Expires, 0).UTC()),
	})
}

func signPowerSyncToken(config powerSyncConfig, claims powerSyncClaims) (string, error) {
	header, err := json.Marshal(map[string]string{"alg": "HS256", "kid": config.KeyID, "typ": "JWT"})
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, config.Key)
	_, _ = mac.Write([]byte(unsigned))
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func verifyPowerSyncToken(config powerSyncConfig, token string, now time.Time) (powerSyncClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return powerSyncClaims{}, errors.New("invalid JWT")
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return powerSyncClaims{}, errors.New("invalid JWT header")
	}
	var header struct {
		Algorithm string `json:"alg"`
		KeyID     string `json:"kid"`
	}
	if json.Unmarshal(headerBytes, &header) != nil || header.Algorithm != "HS256" || header.KeyID != config.KeyID {
		return powerSyncClaims{}, errors.New("invalid JWT header")
	}
	provided, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return powerSyncClaims{}, errors.New("invalid JWT signature")
	}
	mac := hmac.New(sha256.New, config.Key)
	_, _ = mac.Write([]byte(parts[0] + "." + parts[1]))
	if !hmac.Equal(provided, mac.Sum(nil)) {
		return powerSyncClaims{}, errors.New("invalid JWT signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return powerSyncClaims{}, errors.New("invalid JWT payload")
	}
	var claims powerSyncClaims
	if json.Unmarshal(payload, &claims) != nil || claims.Subject == "" || claims.Audience != config.Audience || claims.IssuedAt > now.Unix()+30 || claims.Expires <= now.Unix() || claims.Expires-claims.IssuedAt > 3600 {
		return powerSyncClaims{}, errors.New("invalid JWT claims")
	}
	return claims, nil
}

// authorizePowerSyncFixture is a story-only driver for the otherwise external
// PowerSync service boundary. It verifies the actual issued JWT and then asks
// the same sqlx Store whether the signed subject owns the requested projection.
// Production builds never register this route.
func (s *Server) authorizePowerSyncFixture(w http.ResponseWriter, r *http.Request) {
	token, ok := bearerToken(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing PowerSync token")
		return
	}
	claims, err := verifyPowerSyncToken(s.powerSync, token, s.now().UTC())
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "invalid PowerSync token")
		return
	}
	var request struct {
		AccountID string `json:"account_id"`
	}
	if decodeJSON(r, &request) != nil || strings.TrimSpace(request.AccountID) == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "account_id is required")
		return
	}
	counts, err := s.store.PowerSyncProjectionCounts(r.Context(), claims.Subject, request.AccountID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusForbidden, "forbidden", "signed subject does not own this account projection")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "PowerSync projection could not be authorized")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"account_id": request.AccountID, "projection_counts": counts})
}
