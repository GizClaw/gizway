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
	ID, Method, Path string
	matcher          *regexp.Regexp
}

var hurlRequestPattern = regexp.MustCompile(`^(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)\s+\{\{base_url\}\}([^[:space:]]+)`)

// CheckHurlCoverage binds every `# covers:` declaration to an actual request
// in the same Hurl file. A comment can no longer make CI green unless the file
// really exercises the documented method/path. It also rejects every Gizway
// base_url request that has no OpenAPI operation.
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
				if operation.Method == request.Method && operation.matcher.MatchString(request.Path) {
					matched = true
					break
				}
			}
			if !matched && request.Path != "/healthz" && !strings.HasPrefix(request.Path, "/test/") && !hurlOnlyProtocolPath(request.Path) {
				return fmt.Errorf("%s: Hurl request %s %s has no OpenAPI operation", path, request.Method, request.Path)
			}
		}
		for _, id := range declared {
			operation, ok := operations[id]
			if !ok {
				return fmt.Errorf("%s: stale Hurl coverage declaration %s", path, id)
			}
			matched := false
			for _, request := range requests {
				if operation.Method == request.Method && operation.matcher.MatchString(request.Path) {
					matched = true
					break
				}
			}
			if !matched {
				return fmt.Errorf("%s: coverage declaration %s has no matching %s %s request", path, id, operation.Method, operation.Path)
			}
			covered[id] = true
		}
	}
	missing := make([]string, 0)
	for id := range operations {
		if !covered[id] {
			missing = append(missing, id)
		}
	}
	if len(missing) != 0 {
		sort.Strings(missing)
		return fmt.Errorf("OpenAPI operations missing executable Hurl coverage: %s", strings.Join(missing, ", "))
	}
	return nil
}

func hurlOnlyProtocolPath(path string) bool {
	return strings.HasPrefix(path, "/v1/") || path == "/v1/models" ||
		strings.HasPrefix(path, "/v1beta/") || strings.HasPrefix(path, "/upload/v1beta/") ||
		strings.HasPrefix(path, "/callbacks/")
}

type hurlRequest struct{ Method, Path string }

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
			path, _, _ := strings.Cut(match[2], "?")
			requests = append(requests, hurlRequest{Method: match[1], Path: path})
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
		if name == "common.yaml" {
			continue
		}
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
				operations[id] = operationRoute{ID: id, Method: upper, Path: fullPath, matcher: regexp.MustCompile("^" + pattern + "$")}
			}
		}
	}
	return operations, nil
}
