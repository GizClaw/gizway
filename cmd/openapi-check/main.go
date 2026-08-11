package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/idy/gizway/internal/openapicheck"
)

func main() {
	output := flag.String("out", "", "directory for self-contained JSON bundles")
	flag.Parse()
	if err := run("api/openapi", "internal/api", "tests/api/stories", *output, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
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
