package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	if err := run("../../api/openapi", "../../internal/api", "../../tests/api/stories", "", &output); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(output.String(), "OpenAPI lint, bundle, and implementation conformance passed") {
		t.Fatalf("unexpected output: %q", output.String())
	}
}

func TestRunRejectsInvalidInputs(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		openAPIDir string
		apiDir     string
		hurlDir    string
	}{
		{name: "openapi", openAPIDir: "missing", apiDir: "../../internal/api", hurlDir: "../../tests/api/stories"},
		{name: "hurl", openAPIDir: "../../api/openapi", apiDir: "../../internal/api", hurlDir: "missing"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			if err := run(test.openAPIDir, test.apiDir, test.hurlDir, "", &output); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestWriteInventoryIncludesServiceScopedCoverageAndDeletionReasons(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	if err := writeInventory("../../api/openapi", "../../tests/api/stories", &output); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, expected := range []string{
		"Final retained operation count:",
		"gizpay-admin.yaml | GET | `/admin/v1/me` | getCurrentAdministrator | GizPay | Keep",
		"gizway-admin.yaml | GET | `/admin/v1/me` | getCurrentAdministrator | GizWay | Keep",
		"listAccountModels | GizPay | Delete",
		"setAccountModelEntitlement | GizPay | Delete",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("inventory missing %q", expected)
		}
	}
}
