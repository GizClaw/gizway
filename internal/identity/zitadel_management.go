package identity

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ServiceAccountCredential is returned once when a ZITADEL machine identity
// is created. KeyJSON contains the private-key document produced by ZITADEL;
// callers must deliver it to the user and must not persist or log it.
type ServiceAccountCredential struct {
	Subject string
	KeyID   string
	KeyJSON json.RawMessage
}

// ServiceAccountManager creates and revokes ZITADEL machine credentials.
type ServiceAccountManager interface {
	Create(context.Context, string, []string) (ServiceAccountCredential, error)
	RevokeCredential(context.Context, string, string) error
}

type zitadelServiceAccountManager struct {
	baseURL   string
	projectID string
	token     func(context.Context) (string, error)
	client    *http.Client
}

func NewZITADELServiceAccountManager(baseURL, projectID string, token func(context.Context) (string, error), client *http.Client) (ServiceAccountManager, error) {
	if baseURL == "" || projectID == "" || token == nil {
		return nil, errors.New("ZITADEL base URL, service project ID, and management token source are required")
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &zitadelServiceAccountManager{baseURL: strings.TrimRight(baseURL, "/"), projectID: projectID, token: token, client: client}, nil
}

func (m *zitadelServiceAccountManager) Create(ctx context.Context, name string, roles []string) (ServiceAccountCredential, error) {
	subject := "service-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	var created struct {
		UserID string `json:"userId"`
	}
	if err := m.call(ctx, http.MethodPost, "/management/v1/users/machine", map[string]any{
		"userId": subject, "userName": subject, "name": name, "accessTokenType": "ACCESS_TOKEN_TYPE_JWT",
	}, &created); err != nil {
		return ServiceAccountCredential{}, fmt.Errorf("create ZITADEL Service Account: %w", err)
	}
	if created.UserID != "" {
		subject = created.UserID
	}
	if err := m.call(ctx, http.MethodPost, "/management/v1/users/"+subject+"/grants", map[string]any{
		"projectId": m.projectID, "roleKeys": roles,
	}, nil); err != nil {
		return ServiceAccountCredential{}, fmt.Errorf("grant ZITADEL Service Account roles: %w", err)
	}
	var key struct {
		KeyID      string `json:"keyId"`
		KeyDetails string `json:"keyDetails"`
	}
	if err := m.call(ctx, http.MethodPost, "/management/v1/users/"+subject+"/keys", map[string]any{"type": "KEY_TYPE_JSON"}, &key); err != nil {
		return ServiceAccountCredential{}, fmt.Errorf("create ZITADEL Service Account key: %w", err)
	}
	keyJSON, err := base64.StdEncoding.DecodeString(key.KeyDetails)
	if err != nil {
		keyJSON = []byte(key.KeyDetails)
	}
	if key.KeyID == "" || !json.Valid(keyJSON) {
		return ServiceAccountCredential{}, errors.New("ZITADEL returned an invalid Service Account key")
	}
	return ServiceAccountCredential{Subject: subject, KeyID: key.KeyID, KeyJSON: keyJSON}, nil
}

func (m *zitadelServiceAccountManager) RevokeCredential(ctx context.Context, subject, keyID string) error {
	if subject == "" || keyID == "" {
		return errors.New("service account subject and key ID are required")
	}
	err := m.call(ctx, http.MethodDelete, "/management/v1/users/"+subject+"/keys/"+keyID, nil, nil)
	var statusError *managementStatusError
	if errors.As(err, &statusError) && statusError.status == http.StatusNotFound {
		return nil
	}
	return err
}

type managementStatusError struct {
	status int
	body   string
}

func (e *managementStatusError) Error() string { return fmt.Sprintf("status %d: %s", e.status, e.body) }

func (m *zitadelServiceAccountManager) call(ctx context.Context, method, path string, body, result any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	token, err := m.token(ctx)
	if err != nil {
		return fmt.Errorf("management token: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, method, m.baseURL+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := m.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	responseRaw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return &managementStatusError{status: response.StatusCode, body: strings.TrimSpace(string(responseRaw))}
	}
	if result != nil && len(responseRaw) != 0 {
		return json.Unmarshal(responseRaw, result)
	}
	return nil
}
