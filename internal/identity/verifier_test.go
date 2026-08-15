package identity

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestVerifierUsesConfiguredJWKSRefreshInterval(t *testing.T) {
	verifier := NewVerifierWithRefresh("https://issuer.example", "https://issuer.example/keys", 15*time.Minute)
	if verifier.refreshInterval != 15*time.Minute {
		t.Fatalf("refresh = %s", verifier.refreshInterval)
	}
}

func TestVerifierObservesSemanticJWKSRefreshFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"keys":[]}`))
	}))
	defer server.Close()
	var observed error
	verifier := NewVerifierWithRefresh("https://issuer.example", server.URL, time.Minute).
		SetRefreshObserver(func(err error) { observed = err })
	if err := verifier.refresh(t.Context()); err == nil {
		t.Fatal("empty JWKS was accepted")
	}
	if observed == nil {
		t.Fatal("semantic JWKS failure was not recorded for health")
	}
}

func TestClaimRolesAcceptsOnlyConfiguredZITADELProject(t *testing.T) {
	claims := jwt.MapClaims{
		"urn:zitadel:iam:org:project:386000000000000003:roles":          map[string]any{"administrator": map[string]any{}},
		"urn:zitadel:iam:org:project:386000000000000004:roles":          map[string]any{"administrator": map[string]any{}},
		"urn:zitadel:iam:org:project:386000000000000002:roles:attacker": []any{"account_payer"},
		"attacker_roles": []any{"charge"},
		"profile":        map[string]any{"role": "account_admin"},
	}
	roles := claimRoles(claims, "386000000000000003")
	if !roles["administrator"] {
		t.Fatal("configured ZITADEL project role was not parsed")
	}
	if roles["charge"] || roles["account_admin"] || roles["account_payer"] {
		t.Fatalf("untrusted claims injected roles: %#v", roles)
	}
	if roles := claimRoles(claims, "386000000000000099"); roles["administrator"] {
		t.Fatal("same-named role from a different Project was accepted")
	}
}

func TestPrincipalDisplayNamePrefersNameThenPreferredUsername(t *testing.T) {
	tests := []struct {
		principal Principal
		want      string
	}{
		{principal: Principal{Name: " Human Name ", PreferredName: "handle"}, want: "Human Name"},
		{principal: Principal{PreferredName: " handle "}, want: "handle"},
		{principal: Principal{}, want: ""},
	}
	for _, test := range tests {
		if got := test.principal.DisplayName(); got != test.want {
			t.Errorf("DisplayName() = %q, want %q", got, test.want)
		}
	}
}

func TestClaimStringRejectsNonStringClaims(t *testing.T) {
	claims := jwt.MapClaims{"name": []any{"attacker"}, "preferred_username": " user "}
	if got := claimString(claims, "name"); got != "" {
		t.Fatalf("non-string name = %q", got)
	}
	if got := claimString(claims, "preferred_username"); got != "user" {
		t.Fatalf("preferred_username = %q", got)
	}
}
