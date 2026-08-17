package catalogtoken

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const maximumTTL = 24 * time.Hour

type Token struct {
	AccessToken string    `json:"access_token"`
	TokenType   string    `json:"token_type"`
	ExpiresAt   time.Time `json:"expires_at"`
}

type Config struct {
	TokenURL, ClientID, ClientSecret, Scope string
	TTL, RefreshBefore                      time.Duration
	HTTPClient                              *http.Client
	Now                                     func() time.Time
	ValidateJWT                             func(string) error
}

type Manager struct {
	config  Config
	mu      sync.Mutex
	token   Token
	stop    chan struct{}
	done    chan struct{}
	once    sync.Once
	lifeMu  sync.Mutex
	started bool
}

func New(config Config) (*Manager, error) {
	if config.TokenURL == "" || config.ClientID == "" || config.ClientSecret == "" || config.Scope == "" ||
		config.TTL <= 0 || config.TTL > maximumTTL || config.RefreshBefore <= 0 || config.RefreshBefore >= config.TTL ||
		config.ValidateJWT == nil {
		return nil, errors.New("invalid Public Catalog token configuration")
	}
	parsed, err := url.Parse(config.TokenURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, errors.New("invalid Public Catalog token URL")
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Manager{config: config, stop: make(chan struct{}), done: make(chan struct{})}, nil
}

// Start obtains the initial token before the Gateway becomes ready and starts
// the single proactive refresh loop owned by this manager.
func (m *Manager) Start(ctx context.Context) error {
	m.lifeMu.Lock()
	defer m.lifeMu.Unlock()
	if m.started {
		return errors.New("public catalog token manager already started")
	}
	if _, err := m.Current(ctx); err != nil {
		return err
	}
	m.started = true
	go m.refreshLoop()
	return nil
}

func (m *Manager) Close() {
	m.lifeMu.Lock()
	started := m.started
	m.lifeMu.Unlock()
	if !started {
		return
	}
	m.once.Do(func() { close(m.stop) })
	<-m.done
}

func (m *Manager) refreshLoop() {
	defer close(m.done)
	for {
		m.mu.Lock()
		delay := m.token.ExpiresAt.Add(-m.config.RefreshBefore).Sub(m.config.Now().UTC())
		m.mu.Unlock()
		if delay <= 0 {
			delay = time.Second
		}
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
			_, _ = m.Current(context.Background())
		case <-m.stop:
			if !timer.Stop() {
				<-timer.C
			}
			return
		}
	}
}

func (m *Manager) Current(ctx context.Context) (Token, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.config.Now().UTC()
	if m.token.AccessToken != "" && now.Before(m.token.ExpiresAt.Add(-m.config.RefreshBefore)) {
		return m.token, nil
	}
	refreshed, err := m.fetch(ctx, now)
	if err == nil {
		m.token = refreshed
		return refreshed, nil
	}
	if m.token.AccessToken != "" && now.Before(m.token.ExpiresAt) {
		return m.token, nil
	}
	return Token{}, err
}

func (m *Manager) fetch(ctx context.Context, now time.Time) (Token, error) {
	form := url.Values{"grant_type": {"client_credentials"}, "scope": {m.config.Scope}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, m.config.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return Token{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.SetBasicAuth(m.config.ClientID, m.config.ClientSecret)
	response, err := m.config.HTTPClient.Do(request)
	if err != nil {
		return Token{}, fmt.Errorf("request Public Catalog token: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Token{}, fmt.Errorf("request Public Catalog token: status %d", response.StatusCode)
	}
	var body struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	decoder := json.NewDecoder(response.Body)
	if err := decoder.Decode(&body); err != nil {
		return Token{}, fmt.Errorf("decode Public Catalog token: %w", err)
	}
	parts := strings.Split(body.AccessToken, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return Token{}, errors.New("ZITADEL returned an opaque Public Catalog token")
	}
	if body.TokenType != "Bearer" || body.ExpiresIn <= 0 {
		return Token{}, errors.New("ZITADEL returned an invalid Public Catalog token response")
	}
	ttl := time.Duration(body.ExpiresIn) * time.Second
	if ttl > maximumTTL || ttl > m.config.TTL {
		return Token{}, errors.New("catalog token TTL exceeds the deployment contract")
	}
	if err := m.config.ValidateJWT(body.AccessToken); err != nil {
		return Token{}, fmt.Errorf("verify Public Catalog JWT: %w", err)
	}
	return Token{AccessToken: body.AccessToken, TokenType: body.TokenType, ExpiresAt: now.Add(ttl)}, nil
}
