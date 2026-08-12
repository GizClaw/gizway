package store

import "errors"

// ErrCredentialConsumed remains the public protocol classification for a
// one-purpose Realtime secret that has already been used.
var ErrCredentialConsumed = errors.New("realtime client secret is no longer usable")

// RealtimeSession is regional, identity-free runtime state in Refactor 01.
// Account and API-key fields remain omitted on the regional path.
type RealtimeSession struct {
	ID               string  `json:"session_id"`
	GatewayRequestID string  `json:"-"`
	AccountID        string  `json:"account_id,omitempty"`
	APIKeyID         string  `json:"api_key_id,omitempty"`
	ModelID          string  `json:"-"`
	VariantID        string  `json:"-"`
	PublicModel      string  `json:"model"`
	ProviderModel    string  `json:"-"`
	Transport        string  `json:"transport"`
	Status           string  `json:"status"`
	ExpiresAt        string  `json:"expires_at"`
	DeadlineAt       string  `json:"deadline_at"`
	CreatedAt        string  `json:"created_at"`
	ConnectedAt      *string `json:"connected_at,omitempty"`
	CompletedAt      *string `json:"completed_at,omitempty"`
}
