package openapicheck

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type operationRoute struct {
	Document, ID, Method, Path string
	matcher                    *regexp.Regexp
}

// InventoryEntry is the reviewable source-to-test ownership record for one
// retained OpenAPI operation. Document remains part of the identity because
// GizPay and GizWay intentionally reuse several administrator operation IDs.
type InventoryEntry struct {
	Document, Method, Path, OperationID, Service string
	HurlFiles                                    []string
}

// Inventory returns every retained operation and the Hurl files that declare
// coverage for it. Callers should run CheckHurlCoverage first so an empty file
// list can never be mistaken for an accepted gap.
func Inventory(openAPIDirectory, hurlDirectory string) ([]InventoryEntry, error) {
	operations, err := openAPIOperationRoutes(openAPIDirectory)
	if err != nil {
		return nil, err
	}
	coveredBy := make(map[string][]string, len(operations))
	err = filepath.WalkDir(hurlDirectory, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".hurl") {
			return nil
		}
		declared, _, readErr := readHurlContract(path)
		if readErr != nil {
			return readErr
		}
		relative, relErr := filepath.Rel(".", path)
		if relErr != nil {
			relative = path
		}
		for _, key := range declared {
			coveredBy[key] = append(coveredBy[key], filepath.ToSlash(relative))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	entries := make([]InventoryEntry, 0, len(operations))
	for key, operation := range operations {
		service := "GizPay"
		if operation.Document == "gizway-user.yaml" || operation.Document == "gizway-public.yaml" {
			service = "GizWay"
		}
		files := append([]string(nil), coveredBy[key]...)
		sort.Strings(files)
		entries = append(entries, InventoryEntry{
			Document: operation.Document, Method: operation.Method, Path: operation.Path,
			OperationID: operation.ID, Service: service, HurlFiles: files,
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		left, right := entries[i], entries[j]
		if left.Document != right.Document {
			return left.Document < right.Document
		}
		if left.Path != right.Path {
			return left.Path < right.Path
		}
		return left.Method < right.Method
	})
	return entries, nil
}

var hurlRequestPattern = regexp.MustCompile(`^(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)\s+\{\{(base_url|pay_url|way_url)\}\}([^[:space:]]+)`)

// CheckHurlCoverage binds every `# covers:` declaration to an actual request
// in the same Hurl file. A comment can no longer make CI green unless the file
// really exercises the documented method/path. It also rejects every request
// aimed at one of the separated service URL variables when no operation owns
// that route.
func CheckHurlCoverage(openAPIDirectory, hurlDirectory string) error {
	operations, err := openAPIOperationRoutes(openAPIDirectory)
	if err != nil {
		return err
	}
	paths, err := filepath.Glob(filepath.Join(hurlDirectory, "**", "*.hurl"))
	if err != nil {
		return err
	}
	// filepath.Glob does not make ** recursive on all platforms.
	paths = paths[:0]
	if err := filepath.WalkDir(hurlDirectory, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && strings.HasSuffix(path, ".hurl") {
			paths = append(paths, path)
		}
		return nil
	}); err != nil {
		return err
	}
	if len(paths) == 0 {
		return errors.New("no Hurl stories found")
	}
	sort.Strings(paths)
	covered := make(map[string]bool, len(operations))
	for _, path := range paths {
		declared, requests, err := readHurlContract(path)
		if err != nil {
			return err
		}
		for _, request := range requests {
			matched := false
			for _, operation := range operations {
				if requestTargetsDocument(request.Variable, operation.Document) && operation.Method == request.Method && operation.matcher.MatchString(request.Path) {
					matched = true
					break
				}
			}
			if !matched && !operationalPath(request.Path) && !removedMilestone01Path(request.Path) && !strings.HasPrefix(request.Path, "/test/") {
				return fmt.Errorf("%s: Hurl request %s %s has no OpenAPI operation", path, request.Method, request.Path)
			}
		}
		for _, key := range declared {
			operation, ok := operations[key]
			if !ok {
				return fmt.Errorf("%s: stale Hurl coverage declaration %s; use document.yaml#operationId", path, key)
			}
			matched := false
			for _, request := range requests {
				if requestTargetsDocument(request.Variable, operation.Document) && operation.Method == request.Method && operation.matcher.MatchString(request.Path) {
					matched = true
					break
				}
			}
			if !matched {
				return fmt.Errorf("%s: coverage declaration %s has no matching %s %s request", path, key, operation.Method, operation.Path)
			}
			covered[key] = true
		}
	}
	var missing []string
	for key := range operations {
		if !covered[key] {
			missing = append(missing, key)
		}
	}
	if len(missing) != 0 {
		sort.Strings(missing)
		return fmt.Errorf("OpenAPI operations missing Hurl coverage:\n%s", strings.Join(missing, "\n"))
	}
	return nil
}

// These paths appear only in explicit 404 assertions proving that the
// breaking refactor did not retain a second operational health contract.
func removedMilestone01Path(path string) bool {
	return path == "/livez" || path == "/readyz" || path == "/internal/v1/readyz" ||
		path == "/account/v1/initialize"
}

// Hurl must name the concrete deployment it contacts. Central contracts use
// pay_url and the regional contract uses way_url; base_url is intentionally
// not accepted for OpenAPI ownership because the two Admin surfaces share
// paths and operation IDs while remaining independent services.
func requestTargetsDocument(variable, document string) bool {
	if variable == "way_url" {
		return document == "gizway-user.yaml" || document == "gizway-public.yaml"
	}
	if variable == "pay_url" {
		return document != "gizway-user.yaml" && document != "gizway-public.yaml"
	}
	return false
}

type hurlRequest struct{ Variable, Method, Path string }

func readHurlContract(path string) ([]string, []hurlRequest, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()
	var declared []string
	var requests []hurlRequest
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if after, ok := strings.CutPrefix(line, "# covers:"); ok {
			declared = append(declared, strings.Fields(strings.TrimSpace(after))...)
		}
		if match := hurlRequestPattern.FindStringSubmatch(line); match != nil {
			path, _, _ := strings.Cut(match[3], "?")
			requests = append(requests, hurlRequest{Method: match[1], Variable: match[2], Path: path})
		}
	}
	return declared, requests, scanner.Err()
}

func openAPIOperationRoutes(directory string) (map[string]operationRoute, error) {
	docs, err := loadDocuments(directory)
	if err != nil {
		return nil, err
	}
	operations := map[string]operationRoute{}
	for name, document := range docs {
		if _, isRoot := document["openapi"]; !isRoot {
			continue
		}
		resolved, err := bundleValue(docs, name, document, map[string]bool{})
		if err != nil {
			return nil, err
		}
		document = resolved.(map[string]any)
		base, err := serverBasePath(document)
		if err != nil {
			return nil, err
		}
		paths, _ := document["paths"].(map[string]any)
		for path, rawItem := range paths {
			item, _ := rawItem.(map[string]any)
			for method, rawOperation := range item {
				upper := strings.ToUpper(method)
				if !isHTTPMethod(upper) {
					continue
				}
				operation, _ := rawOperation.(map[string]any)
				id, _ := operation["operationId"].(string)
				fullPath := strings.TrimSuffix(base, "/") + path
				pattern := regexp.QuoteMeta(fullPath)
				placeholder := regexp.MustCompile(`\\\{[^}]+\\\}`)
				pattern = placeholder.ReplaceAllString(pattern, `[^/?]+`)
				key := name + "#" + id
				operations[key] = operationRoute{Document: name, ID: id, Method: upper, Path: fullPath, matcher: regexp.MustCompile("^" + pattern + "$")}
			}
		}
	}
	return operations, nil
}
