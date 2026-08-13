package identity

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type MachineTokenConfig struct {
	TokenURL, Subject, KeyID, PrivateKeyFile, Audience string
	AssertionAudience                                  string
	Scopes                                             []string
	RefreshBefore                                      time.Duration
}

type MachineTokenSource struct {
	config  MachineTokenConfig
	key     *rsa.PrivateKey
	client  *http.Client
	mu      sync.Mutex
	token   string
	expires time.Time
}

func NewMachineTokenSource(config MachineTokenConfig, client *http.Client) (*MachineTokenSource, error) {
	raw, err := os.ReadFile(config.PrivateKeyFile)
	if err != nil {
		return nil, err
	}
	key, embeddedID, embeddedSubject, err := parseMachineKey(raw)
	if err != nil {
		return nil, err
	}
	if config.KeyID == "" {
		config.KeyID = embeddedID
	}
	if config.Subject == "" {
		config.Subject = embeddedSubject
	}
	if config.TokenURL == "" || config.Subject == "" || config.KeyID == "" {
		return nil, errors.New("machine token URL, subject, and key ID are required")
	}
	if config.RefreshBefore <= 0 {
		config.RefreshBefore = 30 * time.Second
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &MachineTokenSource{config: config, key: key, client: client}, nil
}

func parseMachineKey(raw []byte) (*rsa.PrivateKey, string, string, error) {
	var document struct {
		KeyID      string `json:"keyId"`
		UserID     string `json:"userId"`
		Key        string `json:"key"`
		PrivateKey string `json:"private_key"`
	}
	pemBytes := raw
	_ = json.Unmarshal(raw, &document)
	if document.Key != "" {
		pemBytes = []byte(document.Key)
	} else if document.PrivateKey != "" {
		pemBytes = []byte(document.PrivateKey)
	}
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, "", "", errors.New("machine private key is not PEM")
	}
	if parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		key, ok := parsed.(*rsa.PrivateKey)
		if !ok {
			return nil, "", "", errors.New("machine private key is not RSA")
		}
		return key, document.KeyID, document.UserID, nil
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	return key, document.KeyID, document.UserID, err
}

func (s *MachineTokenSource) Token(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token != "" && time.Now().Add(s.config.RefreshBefore).Before(s.expires) {
		return s.token, nil
	}
	now := time.Now()
	assertionAudience := s.config.AssertionAudience
	if assertionAudience == "" {
		assertionAudience = s.config.TokenURL
	}
	claims := jwt.MapClaims{"iss": s.config.Subject, "sub": s.config.Subject, "aud": assertionAudience, "iat": now.Unix(), "exp": now.Add(5 * time.Minute).Unix(), "jti": uuid.NewString()}
	assertion := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	assertion.Header["kid"] = s.config.KeyID
	signed, err := assertion.SignedString(s.key)
	if err != nil {
		return "", err
	}
	values := url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {signed},
	}
	scopes := append([]string(nil), s.config.Scopes...)
	if s.config.Audience != "" {
		scopes = append(scopes,
			"urn:zitadel:iam:org:projects:roles",
			"urn:zitadel:iam:org:project:id:"+s.config.Audience+":aud",
		)
	}
	if len(scopes) > 0 {
		values.Set("scope", strings.Join(scopes, " "))
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.config.TokenURL, strings.NewReader(values.Encode()))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := s.client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	var result struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if json.NewDecoder(response.Body).Decode(&result) != nil || response.StatusCode != http.StatusOK || result.AccessToken == "" {
		return "", fmt.Errorf("machine token endpoint returned %d", response.StatusCode)
	}
	s.token, s.expires = result.AccessToken, now.Add(time.Duration(result.ExpiresIn)*time.Second)
	return s.token, nil
}

// RequireTokenRoles verifies the roles granted by the trusted token endpoint
// before a Gateway uses the token for GizPay business requests. Signature and
// issuer validation remain GizPay's responsibility at the receiving boundary.
func RequireTokenRoles(raw, projectID string, required []string) error {
	claims := jwt.MapClaims{}
	if _, _, err := jwt.NewParser().ParseUnverified(raw, claims); err != nil {
		return fmt.Errorf("parse machine access token: %w", err)
	}
	roles := claimRoles(claims, projectID)
	for _, role := range required {
		if !roles[role] {
			return fmt.Errorf("machine access token missing required role %q", role)
		}
	}
	return nil
}
