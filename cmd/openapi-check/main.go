package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/idy/gizway/internal/openapicheck"
)

func main() {
	output := flag.String("out", "", "directory for self-contained JSON bundles")
	inventory := flag.Bool("inventory", false, "print the retained API and Hurl coverage inventory as Markdown")
	flag.Parse()
	checkOutput := io.Writer(os.Stdout)
	if *inventory {
		checkOutput = io.Discard
	}
	if err := run("api/openapi", "internal/api", "tests/api/stories", *output, checkOutput); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *inventory {
		if err := writeInventory("api/openapi", "tests/api/stories", os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}

func writeInventory(openAPIDir, hurlDir string, output io.Writer) error {
	entries, err := openapicheck.Inventory(openAPIDir, hurlDir)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(output, "\n# Current implementation API inventory\n\nFinal retained operation count: **%d**.\n\n", len(entries)); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(output, "| OpenAPI file | Method | Path | operationId | Service | Status | Deletion reason | Hurl coverage |\n|---|---|---|---|---|---|---|---|"); err != nil {
		return err
	}
	for _, entry := range entries {
		if _, err := fmt.Fprintf(output, "| %s | %s | `%s` | %s | %s | Keep | - | %s |\n",
			entry.Document, entry.Method, entry.Path, entry.OperationID, entry.Service, strings.Join(entry.HurlFiles, "<br>")); err != nil {
			return err
		}
	}
	deleted := []struct{ document, method, path, operation, service, reason string }{
		{"account.yaml", "GET", "/account/v1/accounts/{account_id}/models", "listAccountModels", "GizPay", "Regional Catalog belongs to GizWay; GizPay has no Model or Provider tables, so this cross-database projection is not part of the split architecture."},
		{"gizpay-admin.yaml", "PUT", "/admin/v1/accounts/{account_id}/model_entitlements/{model_id}", "setAccountModelEntitlement", "GizPay", "Account-to-Catalog entitlement would duplicate regional Model identity in GizPay and reintroduce the removed merged-database boundary."},
	}
	for _, entry := range deleted {
		if _, err := fmt.Fprintf(output, "| %s | %s | `%s` | %s | %s | Delete | %s | Removed with route, handler, Store implementation, and obsolete Hurl coverage. |\n",
			entry.document, entry.method, entry.path, entry.operation, entry.service, entry.reason); err != nil {
			return err
		}
	}
	return nil
}

// run keeps the command's orchestration testable without weakening the real
// checker. The production main function supplies the repository paths, while
// unit tests can exercise both success and failure without spawning a process.
func run(openAPIDir, apiDir, hurlDir, output string, stdout io.Writer) error {
	if err := openapicheck.Check(openAPIDir, apiDir, output); err != nil {
		return err
	}
	if err := openapicheck.CheckHurlCoverage(openAPIDir, hurlDir); err != nil {
		return err
	}
	_, err := fmt.Fprintln(stdout, "OpenAPI lint, bundle, and implementation conformance passed")
	return err
}
