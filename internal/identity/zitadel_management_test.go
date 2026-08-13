package identity

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestZITADELServiceAccountManagerCreateAndRevoke(t *testing.T) {
	var requests []string
	var grantedRoles []string
	keyDocument := []byte(`{"keyId":"key-1","userId":"machine-1","key":"secret-private-key"}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer management-token" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		requests = append(requests, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/management/v1/users/machine":
			_ = json.NewEncoder(w).Encode(map[string]string{"userId": "machine-1"})
		case r.Method == http.MethodPost && r.URL.Path == "/management/v1/users/machine-1/grants":
			var body struct {
				ProjectID string   `json:"projectId"`
				RoleKeys  []string `json:"roleKeys"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.ProjectID != "service-project" {
				t.Errorf("projectId = %q", body.ProjectID)
			}
			grantedRoles = body.RoleKeys
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && r.URL.Path == "/management/v1/users/machine-1/keys":
			_ = json.NewEncoder(w).Encode(map[string]string{"keyId": "key-1", "keyDetails": base64.StdEncoding.EncodeToString(keyDocument)})
		case r.Method == http.MethodDelete && r.URL.Path == "/management/v1/users/machine-1/keys/key-1":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	manager, err := NewZITADELServiceAccountManager(server.URL, "service-project", func(context.Context) (string, error) {
		return "management-token", nil
	}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	credential, err := manager.Create(context.Background(), "CN Gateway", []string{"subscription_credit_reader", "subscription_charger"})
	if err != nil {
		t.Fatal(err)
	}
	if credential.Subject != "machine-1" || credential.KeyID != "key-1" || string(credential.KeyJSON) != string(keyDocument) {
		t.Fatalf("credential = %#v", credential)
	}
	if !reflect.DeepEqual(grantedRoles, []string{"subscription_credit_reader", "subscription_charger"}) {
		t.Fatalf("roles = %#v", grantedRoles)
	}
	if err := manager.RevokeCredential(context.Background(), credential.Subject, credential.KeyID); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(requests, "\n")
	for _, expected := range []string{
		"POST /management/v1/users/machine",
		"POST /management/v1/users/machine-1/grants",
		"POST /management/v1/users/machine-1/keys",
		"DELETE /management/v1/users/machine-1/keys/key-1",
	} {
		if !strings.Contains(joined, expected) {
			t.Errorf("missing request %q in %s", expected, joined)
		}
	}
}

func TestZITADELServiceAccountManagerTreatsMissingCredentialAsRevoked(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	manager, err := NewZITADELServiceAccountManager(server.URL, "service-project", func(context.Context) (string, error) { return "token", nil }, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.RevokeCredential(context.Background(), "machine", "missing"); err != nil {
		t.Fatal(err)
	}
}
