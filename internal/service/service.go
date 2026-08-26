// Package service orchestrates the analysis engine into domain operations.
// It is transport-agnostic: MCP tools, CLIs and tests drive it equally.
// Outputs carry json tags and are used verbatim as tool response bodies.
package service

import (
	"sort"
	"strings"

	"mcp-ast/internal/engine"
	"mcp-ast/internal/lang"
)

// Services aggregates one service per domain. Engine hosts the raw stateless
// operations (parse, query, get_text) that need no orchestration.
type Services struct {
	Engine *engine.Engine
	Scan   *ScanService
	File   *FileAnalysisService
	Usages *UsagesService
	Unused *UnusedService
	Calls  *CallsService
}

func New(e *engine.Engine) *Services {
	return &Services{
		Engine: e,
		Scan:   &ScanService{eng: e},
		File:   &FileAnalysisService{eng: e},
		Usages: &UsagesService{eng: e},
		Unused: &UnusedService{eng: e},
		Calls:  &CallsService{eng: e},
	}
}

// filters resolves the requested language names into walk filters. Empty
// names select auto-detection (a single nil filter).
func filters(e *engine.Engine, names []string, path string) ([]lang.Language, error) {
	if len(names) == 0 {
		return []lang.Language{nil}, nil
	}
	ls := make([]lang.Language, 0, len(names))
	for _, n := range names {
		l, err := e.Resolve(n, path)
		if err != nil {
			return nil, err
		}
		ls = append(ls, l)
	}
	return ls, nil
}

func displayLang(names []string) string {
	if len(names) == 0 {
		return "auto"
	}
	return strings.Join(names, ",")
}

// limitFiles caps a path-keyed result at n entries in stable (sorted) order.
func limitFiles[V any](files map[string]V, n int) map[string]V {
	if n <= 0 || len(files) <= n {
		return files
	}
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	capped := make(map[string]V, n)
	for _, p := range paths[:n] {
		capped[p] = files[p]
	}
	return capped
}
