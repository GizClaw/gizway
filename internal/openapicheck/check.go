// Package openapicheck validates and bundles Gizway-owned OpenAPI documents.
// It is intentionally small and repository-local: CI must not silently skip
// contract validation merely because a globally installed Node tool is absent.
package openapicheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"go.yaml.in/yaml/v3"
)

var routePattern = regexp.MustCompile(`"(GET|POST|PUT|PATCH|DELETE) ([^"]+)"`)

type documents map[string]map[string]any

// Check validates syntax, every local reference, operation ID uniqueness and
// implementation route conformance. When outputDirectory is non-empty it also
// emits self-contained JSON bundles with all cross-file references inlined.
func Check(openAPIDirectory, apiSourceDirectory, outputDirectory string) error {
	docs, err := loadDocuments(openAPIDirectory)
	if err != nil {
		return err
	}
	implemented, err := implementedRoutes(apiSourceDirectory)
	if err != nil {
		return err
	}
	operationIDs := map[string]string{}
	documentedRoutes := map[string]bool{}
	rootCount := 0
	for name, document := range docs {
		if _, isRoot := document["openapi"]; !isRoot {
			continue
		}
		if document["openapi"] != "3.1.0" {
			return fmt.Errorf("%s: openapi must be 3.1.0", name)
		}
		// Use an independent standards-aware parser/validator in addition to the
		// repository-specific route and bundle checks below. This catches schema,
		// parameter, response and security-shape errors that a YAML/ref walker
		// cannot recognize.
		loader := openapi3.NewLoader()
		loader.IsExternalRefsAllowed = true
		standardDocument, err := loader.LoadFromFile(filepath.Join(openAPIDirectory, name))
		if err != nil {
			return fmt.Errorf("%s: standard OpenAPI load: %w", name, err)
		}
		if err := standardDocument.Validate(context.Background()); err != nil {
			return fmt.Errorf("%s: standard OpenAPI validation: %w", name, err)
		}
		resolved, err := bundleValue(docs, name, document, map[string]bool{})
		if err != nil {
			return err
		}
		document = resolved.(map[string]any)
		rootCount++
		basePath, err := serverBasePath(document)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		paths, ok := document["paths"].(map[string]any)
		if !ok || len(paths) == 0 {
			return fmt.Errorf("%s: paths must not be empty", name)
		}
		for path, rawItem := range paths {
			item, ok := rawItem.(map[string]any)
			if !ok {
				return fmt.Errorf("%s: path %s must be an object", name, path)
			}
			for method, rawOperation := range item {
				upper := strings.ToUpper(method)
				if !isHTTPMethod(upper) {
					continue
				}
				operation, ok := rawOperation.(map[string]any)
				if !ok {
					return fmt.Errorf("%s: %s %s must be an object", name, upper, path)
				}
				operationID, _ := operation["operationId"].(string)
				if operationID == "" {
					return fmt.Errorf("%s: %s %s lacks operationId", name, upper, path)
				}
				route := upper + " " + strings.TrimSuffix(basePath, "/") + path
				if previous := operationIDs[operationID]; previous != "" {
					return fmt.Errorf("duplicate operationId %s in %s and %s", operationID, previous, name)
				}
				operationIDs[operationID] = name
				documentedRoutes[route] = true
				if !implemented[route] {
					return fmt.Errorf("%s: documented route %s is not registered by internal/api", name, route)
				}
			}
		}
		if _, err := bundleValue(docs, name, document, map[string]bool{}); err != nil {
			return err
		}
	}
	if rootCount != 5 {
		return fmt.Errorf("expected 5 root OpenAPI documents, found %d", rootCount)
	}
	for route := range implemented {
		path := strings.SplitN(route, " ", 2)[1]
		if operationalPath(path) || strings.HasPrefix(path, "/test/") {
			continue
		}
		if !documentedRoutes[route] {
			return fmt.Errorf("registered API route %s is absent from OpenAPI", route)
		}
	}
	if outputDirectory == "" {
		return nil
	}
	if err := os.MkdirAll(outputDirectory, 0o755); err != nil {
		return err
	}
	for _, name := range []string{"account.yaml", "gizpay-webhooks.yaml", "gizway-user.yaml", "gizway-public.yaml", "internal-gizpay.yaml"} {
		bundled, err := bundleValue(docs, name, docs[name], map[string]bool{})
		if err != nil {
			return err
		}
		encoded, err := json.MarshalIndent(bundled, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal %s bundle: %w", name, err)
		}
		outputName := strings.TrimSuffix(name, filepath.Ext(name)) + ".json"
		if err := os.WriteFile(filepath.Join(outputDirectory, outputName), append(encoded, '\n'), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func operationalPath(path string) bool {
	return path == "/healthz"
}

func loadDocuments(directory string) (documents, error) {
	paths, err := filepath.Glob(filepath.Join(directory, "*.yaml"))
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, errors.New("no OpenAPI YAML documents found")
	}
	docs := make(documents, len(paths))
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var document map[string]any
		if err := yaml.Unmarshal(raw, &document); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		docs[filepath.Base(path)] = document
	}
	return docs, nil
}

func implementedRoutes(directory string) (map[string]bool, error) {
	paths, err := filepath.Glob(filepath.Join(directory, "*.go"))
	if err != nil {
		return nil, err
	}
	routes := map[string]bool{}
	manifest := filepath.Join(directory, "milestone03_routes.go")
	if _, statErr := os.Stat(manifest); statErr == nil {
		paths = []string{manifest}
	}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		for _, match := range routePattern.FindAllStringSubmatch(string(raw), -1) {
			routes[match[1]+" "+match[2]] = true
		}
	}
	return routes, nil
}

func serverBasePath(document map[string]any) (string, error) {
	servers, ok := document["servers"].([]any)
	if !ok || len(servers) != 1 {
		return "", errors.New("exactly one canonical server is required")
	}
	server, ok := servers[0].(map[string]any)
	if !ok {
		return "", errors.New("server must be an object")
	}
	value, _ := server["url"].(string)
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("canonical server must be an absolute HTTPS URL")
	}
	return parsed.Path, nil
}

func bundleValue(docs documents, current string, value any, stack map[string]bool) (any, error) {
	switch typed := value.(type) {
	case []any:
		result := make([]any, len(typed))
		for index, child := range typed {
			bundled, err := bundleValue(docs, current, child, stack)
			if err != nil {
				return nil, err
			}
			result[index] = bundled
		}
		return result, nil
	case map[string]any:
		if reference, ok := typed["$ref"].(string); ok {
			targetFile, target, key, err := resolveReference(docs, current, reference)
			if err != nil {
				return nil, err
			}
			if stack[key] {
				return nil, fmt.Errorf("cyclic OpenAPI reference %s", key)
			}
			nextStack := make(map[string]bool, len(stack)+1)
			for item := range stack {
				nextStack[item] = true
			}
			nextStack[key] = true
			return bundleValue(docs, targetFile, target, nextStack)
		}
		result := make(map[string]any, len(typed))
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			bundled, err := bundleValue(docs, current, typed[key], stack)
			if err != nil {
				return nil, err
			}
			result[key] = bundled
		}
		return result, nil
	default:
		return value, nil
	}
}

func resolveReference(docs documents, current, reference string) (string, any, string, error) {
	parts := strings.SplitN(reference, "#", 2)
	targetFile := current
	if parts[0] != "" {
		targetFile = filepath.Base(filepath.Clean(filepath.Join(filepath.Dir(current), parts[0])))
	}
	document, ok := docs[targetFile]
	if !ok {
		return "", nil, "", fmt.Errorf("%s: reference %s targets missing document", current, reference)
	}
	var value any = document
	pointer := ""
	if len(parts) == 2 {
		pointer = parts[1]
	}
	if pointer != "" {
		if !strings.HasPrefix(pointer, "/") {
			return "", nil, "", fmt.Errorf("%s: invalid reference %s", current, reference)
		}
		for token := range strings.SplitSeq(strings.TrimPrefix(pointer, "/"), "/") {
			token = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
			object, ok := value.(map[string]any)
			if !ok {
				return "", nil, "", fmt.Errorf("%s: reference %s traverses a non-object", current, reference)
			}
			value, ok = object[token]
			if !ok {
				return "", nil, "", fmt.Errorf("%s: unresolved reference %s", current, reference)
			}
		}
	}
	return targetFile, value, targetFile + "#" + pointer, nil
}

func isHTTPMethod(method string) bool {
	switch method {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "TRACE":
		return true
	default:
		return false
	}
}
