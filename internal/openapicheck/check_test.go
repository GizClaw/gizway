package openapicheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepositoryOpenAPIContractsResolveBundleAndMatchRoutes(t *testing.T) {
	root := filepath.Join("..", "..")
	output := t.TempDir()
	if err := Check(filepath.Join(root, "api", "openapi"), filepath.Join(root, "internal", "api"), output); err != nil {
		t.Fatal(err)
	}
	if err := CheckHurlCoverage(filepath.Join(root, "api", "openapi"), filepath.Join(root, "tests", "api", "stories")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"account.json", "gizway-user.json", "internal-gizpay.json"} {
		raw, err := os.ReadFile(filepath.Join(output, name))
		if err != nil || len(raw) == 0 {
			t.Fatalf("bundle %s: bytes=%d err=%v", name, len(raw), err)
		}
		if containsExternalRef(raw) {
			t.Fatalf("bundle %s retained an external YAML reference", name)
		}
	}
}

func TestMalformedOpenAPIContractsFailClosed(t *testing.T) {
	write := func(t *testing.T, directory, name, value string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(directory, name), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	validRoute := `openapi: 3.1.0
info: {title: Test, version: 1.0.0}
servers: [{url: https://gateway.gizclaw.com/test/v1}]
paths:
  /things:
    get: {operationId: listThings, responses: {'200': {description: ok}}}
`
	for _, test := range []struct {
		name, document, source string
	}{
		{name: "invalid yaml", document: `openapi: [`},
		{name: "wrong version", document: strings.Replace(validRoute, "3.1.0", "3.0.0", 1), source: `mux.Handle("GET /test/v1/things", handler)`},
		{name: "invalid server", document: strings.Replace(validRoute, "https://gateway.gizclaw.com/test/v1", "http://elsewhere.test", 1), source: `mux.Handle("GET /test/v1/things", handler)`},
		{name: "missing route", document: validRoute},
		{name: "missing operation id", document: strings.Replace(validRoute, "operationId: listThings, ", "", 1), source: `mux.Handle("GET /test/v1/things", handler)`},
		{name: "missing reference document", document: strings.Replace(validRoute, "responses: {'200': {description: ok}}", "responses: {'200': {$ref: './absent.yaml#/response'}}", 1), source: `mux.Handle("GET /test/v1/things", handler)`},
		{name: "unresolved pointer", document: strings.Replace(validRoute, "responses: {'200': {description: ok}}", "responses: {'200': {$ref: '#/components/responses/Missing'}}", 1), source: `mux.Handle("GET /test/v1/things", handler)`},
		{name: "invalid pointer", document: strings.Replace(validRoute, "responses: {'200': {description: ok}}", "responses: {'200': {$ref: '#not-a-pointer'}}", 1), source: `mux.Handle("GET /test/v1/things", handler)`},
	} {
		t.Run(test.name, func(t *testing.T) {
			openapi := t.TempDir()
			source := t.TempDir()
			write(t, openapi, "account.yaml", test.document)
			if test.source != "" {
				write(t, source, "routes.go", "package api\nfunc routes(){ "+test.source+" }\n")
			}
			if err := Check(openapi, source, ""); err == nil {
				t.Fatal("malformed contract succeeded")
			}
		})
	}

	empty := t.TempDir()
	if err := Check(empty, t.TempDir(), ""); err == nil {
		t.Fatal("empty OpenAPI directory succeeded")
	}
}

func TestHurlCoverageDeclarationRequiresMatchingRequest(t *testing.T) {
	openapi := t.TempDir()
	hurl := t.TempDir()
	document := `openapi: 3.1.0
info: {title: Test, version: 1.0.0}
servers: [{url: https://gateway.gizclaw.com/test/v1}]
paths:
  /things/{thing_id}:
    get:
      operationId: getThing
      parameters:
        - {name: thing_id, in: path, required: true, schema: {type: string}}
      responses: {'200': {description: ok}}
`
	if err := os.WriteFile(filepath.Join(openapi, "account.yaml"), []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	storyPath := filepath.Join(hurl, "story.hurl")
	if err := os.WriteFile(storyPath, []byte("# covers: account.yaml#getThing\nGET {{pay_url}}/test/v1/not-things\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckHurlCoverage(openapi, hurl); err == nil {
		t.Fatal("comment-only Hurl coverage succeeded")
	}
	if err := os.WriteFile(storyPath, []byte("# covers: account.yaml#getThing\nGET {{pay_url}}/test/v1/things/{{thing_id}}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckHurlCoverage(openapi, hurl); err != nil {
		t.Fatalf("matching Hurl request failed: %v", err)
	}
}

func TestHurlCoverageRequiresEveryDocumentOperation(t *testing.T) {
	openapi := t.TempDir()
	hurl := t.TempDir()
	document := `openapi: 3.1.0
info: {title: Test, version: 1.0.0}
servers: [{url: https://gateway.gizclaw.com/test/v1}]
paths:
  /things:
    get: {operationId: listThings, responses: {'200': {description: ok}}}
  /other:
    get: {operationId: getOther, responses: {'200': {description: ok}}}
`
	if err := os.WriteFile(filepath.Join(openapi, "account.yaml"), []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	story := "# covers: account.yaml#listThings\nGET {{pay_url}}/test/v1/things\n"
	if err := os.WriteFile(filepath.Join(hurl, "story.hurl"), []byte(story), 0o600); err != nil {
		t.Fatal(err)
	}
	err := CheckHurlCoverage(openapi, hurl)
	if err == nil || !strings.Contains(err.Error(), "account.yaml#getOther") {
		t.Fatalf("missing operation was not reported: %v", err)
	}
}

func TestHurlCoverageSeparatesDocuments(t *testing.T) {
	openapi := t.TempDir()
	hurl := t.TempDir()
	central := `openapi: 3.1.0
info: {title: Central, version: 1.0.0}
servers: [{url: https://pay.gizclaw.com/account/v1}]
paths:
  /me:
    get: {operationId: getCurrentIdentity, responses: {'200': {description: ok}}}
`
	regional := strings.ReplaceAll(central, "title: Central", "title: Regional")
	regional = strings.ReplaceAll(regional, "pay.gizclaw.com", "gateway.gizclaw.com")
	if err := os.WriteFile(filepath.Join(openapi, "account.yaml"), []byte(central), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(openapi, "gizway-user.yaml"), []byte(regional), 0o600); err != nil {
		t.Fatal(err)
	}
	story := "# covers: account.yaml#getCurrentIdentity\nGET {{pay_url}}/account/v1/me\n"
	if err := os.WriteFile(filepath.Join(hurl, "story.hurl"), []byte(story), 0o600); err != nil {
		t.Fatal(err)
	}
	err := CheckHurlCoverage(openapi, hurl)
	if err == nil || !strings.Contains(err.Error(), "gizway-user.yaml#getCurrentIdentity") {
		t.Fatalf("regional operation was incorrectly merged with central coverage: %v", err)
	}
}

func containsExternalRef(raw []byte) bool {
	return bytesContains(raw, []byte("common.yaml"))
}

func bytesContains(value, fragment []byte) bool {
	for index := 0; index+len(fragment) <= len(value); index++ {
		matched := true
		for offset := range fragment {
			if value[index+offset] != fragment[offset] {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}
