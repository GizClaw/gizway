package identity

import (
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

func TestRequireTokenRolesUsesConfiguredProjectAndEveryRequiredRole(t *testing.T) {
	claims := jwt.MapClaims{
		"urn:zitadel:iam:org:project:gateway-project:roles": map[string]any{
			"credit_check": map[string]any{},
			"charge":       map[string]any{},
		},
		"urn:zitadel:iam:org:project:other-project:roles": map[string]any{
			"account_admin": map[string]any{},
		},
	}
	raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte("test-only"))
	if err != nil {
		t.Fatal(err)
	}
	if err := RequireTokenRoles(raw, "gateway-project", []string{"credit_check", "charge"}); err != nil {
		t.Fatalf("configured roles rejected: %v", err)
	}
	if err := RequireTokenRoles(raw, "gateway-project", []string{"account_admin"}); err == nil {
		t.Fatal("role from another project satisfied required_roles")
	}
}
