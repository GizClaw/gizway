package identity

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var (
	ErrInvalidIssuer   = errors.New("invalid token issuer")
	ErrInvalidAudience = errors.New("invalid token audience")
)

type Principal struct {
	Issuer         string
	Subject        string
	Roles          map[string]bool
	RolesByProject map[string]map[string]bool
	Audiences      map[string]bool
}

func (p Principal) HasRole(projectID, role string) bool {
	return p.RolesByProject[projectID][role]
}

func (p Principal) HasRoleInAnyProject(role string) bool {
	for _, roles := range p.RolesByProject {
		if roles[role] {
			return true
		}
	}
	return false
}

type Verifier struct {
	issuer          string
	jwksURL         string
	client          *http.Client
	mu              sync.RWMutex
	keys            map[string]any
	expires         time.Time
	refreshInterval time.Duration
	observeRefresh  func(error)
}

// SetRefreshObserver records the result of an actual JWKS refresh. Cached JWT
// verification does not manufacture a new remote-dependency observation.
func (v *Verifier) SetRefreshObserver(observer func(error)) *Verifier {
	v.observeRefresh = observer
	return v
}

func NewVerifier(issuer, jwksURL string) *Verifier {
	return NewVerifierWithRefresh(issuer, jwksURL, 5*time.Minute)
}

// NewVerifierWithRefresh configures how long a successfully fetched JWKS may
// be reused before it is refreshed. Unknown key IDs still force one refresh.
func NewVerifierWithRefresh(issuer, jwksURL string, refresh time.Duration) *Verifier {
	return NewVerifierWithRefreshAndClient(issuer, jwksURL, refresh, nil)
}

func NewVerifierWithRefreshAndClient(issuer, jwksURL string, refresh time.Duration, client *http.Client) *Verifier {
	if refresh <= 0 {
		refresh = 5 * time.Minute
	}
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	return &Verifier{issuer: strings.TrimSuffix(issuer, "/"), jwksURL: jwksURL, client: client, refreshInterval: refresh}
}

func (v *Verifier) Issuer() string { return v.issuer }

func (v *Verifier) Authenticate(r *http.Request, audience string) (Principal, error) {
	return v.authenticate(r, audience)
}

// AuthenticateAny validates the signature, issuer and lifetime while leaving
// audience classification to handlers that distinguish regional identities.
func (v *Verifier) AuthenticateAny(r *http.Request) (Principal, error) {
	return v.authenticate(r, "")
}

func (v *Verifier) authenticate(r *http.Request, audience string) (Principal, error) {
	header := r.Header.Get("Authorization")
	raw, ok := strings.CutPrefix(header, "Bearer ")
	if !ok || raw == "" {
		return Principal{}, errors.New("missing bearer token")
	}
	// Reject a foreign issuer before key selection. This is only an early
	// rejection path: matching this unverified value never makes a token valid;
	// signature, lifetime, and all remaining claims are still checked below.
	unverified := jwt.MapClaims{}
	if _, _, parseErr := jwt.NewParser().ParseUnverified(raw, unverified); parseErr == nil {
		if tokenIssuer, issuerErr := unverified.GetIssuer(); issuerErr == nil && strings.TrimSuffix(tokenIssuer, "/") != v.issuer {
			return Principal{}, ErrInvalidIssuer
		}
	}
	claims := jwt.MapClaims{}
	options := []jwt.ParserOption{jwt.WithIssuer(v.issuer), jwt.WithExpirationRequired()}
	if audience != "" {
		options = append(options, jwt.WithAudience(audience))
	}
	token, err := jwt.ParseWithClaims(raw, claims, func(token *jwt.Token) (any, error) {
		if token.Method.Alg() != "RS256" {
			return nil, fmt.Errorf("unsupported signing algorithm %s", token.Method.Alg())
		}
		kid, _ := token.Header["kid"].(string)
		return v.key(r.Context(), kid)
	}, options...)
	if err != nil || !token.Valid {
		if errors.Is(err, jwt.ErrTokenInvalidIssuer) {
			return Principal{}, ErrInvalidIssuer
		}
		if errors.Is(err, jwt.ErrTokenInvalidAudience) {
			return Principal{}, ErrInvalidAudience
		}
		return Principal{}, errors.New("invalid bearer token")
	}
	issuer, _ := claims.GetIssuer()
	subject, _ := claims.GetSubject()
	if subject == "" {
		return Principal{}, errors.New("token subject is required")
	}
	audiences := map[string]bool{}
	if values, audienceErr := claims.GetAudience(); audienceErr == nil {
		for _, value := range values {
			audiences[value] = true
		}
	}
	rolesByProject := claimRolesByProject(claims)
	return Principal{Issuer: issuer, Subject: subject, Roles: rolesByProject[audience], RolesByProject: rolesByProject, Audiences: audiences}, nil
}

func (v *Verifier) key(ctx context.Context, kid string) (any, error) {
	v.mu.RLock()
	key, fresh := v.keys[kid], time.Now().Before(v.expires)
	v.mu.RUnlock()
	if key != nil && fresh {
		return key, nil
	}
	if err := v.refresh(ctx); err != nil {
		return nil, err
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	key = v.keys[kid]
	if key == nil {
		return nil, errors.New("unknown signing key")
	}
	return key, nil
}

func (v *Verifier) refresh(ctx context.Context) (resultErr error) {
	if v.observeRefresh != nil {
		defer func() { v.observeRefresh(resultErr) }()
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, v.jwksURL, nil)
	if err != nil {
		return err
	}
	response, err := v.client.Do(request)
	if err != nil {
		return fmt.Errorf("fetch JWKS: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch JWKS: status %d", response.StatusCode)
	}
	var document struct {
		Keys []struct{ KID, KTY, N, E string } `json:"keys"`
	}
	if err := json.NewDecoder(response.Body).Decode(&document); err != nil {
		return fmt.Errorf("decode JWKS: %w", err)
	}
	keys := make(map[string]any, len(document.Keys))
	for _, jwk := range document.Keys {
		if jwk.KTY != "RSA" {
			continue
		}
		nBytes, err := base64.RawURLEncoding.DecodeString(jwk.N)
		if err != nil {
			continue
		}
		eBytes, err := base64.RawURLEncoding.DecodeString(jwk.E)
		if err != nil {
			continue
		}
		exponent := 0
		for _, value := range eBytes {
			exponent = exponent<<8 + int(value)
		}
		keys[jwk.KID] = &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: exponent}
	}
	if len(keys) == 0 {
		return errors.New("JWKS contains no RSA signing keys")
	}
	v.keys, v.expires = keys, time.Now().Add(v.refreshInterval)
	return nil
}

func claimRoles(claims jwt.MapClaims, projectID string) map[string]bool {
	return claimRolesByProject(claims)[projectID]
}

func claimRolesByProject(claims jwt.MapClaims) map[string]map[string]bool {
	projects := map[string]map[string]bool{}
	const prefix = "urn:zitadel:iam:org:project:"
	const suffix = ":roles"
	for key, value := range claims {
		if !strings.HasPrefix(key, prefix) || !strings.HasSuffix(key, suffix) {
			continue
		}
		projectID := strings.TrimSuffix(strings.TrimPrefix(key, prefix), suffix)
		if projectID == "" || strings.Contains(projectID, ":") {
			continue
		}
		roles := projects[projectID]
		if roles == nil {
			roles = map[string]bool{}
			projects[projectID] = roles
		}
		collectRoles(roles, value)
	}
	return projects
}

func collectRoles(roles map[string]bool, value any) {
	switch typed := value.(type) {
	case string:
		for role := range strings.FieldsSeq(typed) {
			roles[role] = true
		}
	case []any:
		for _, item := range typed {
			collectRoles(roles, item)
		}
	case map[string]any:
		for role := range typed {
			roles[role] = true
		}
	}
}
