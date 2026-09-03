package engine

import (
	"bytes"
	"context"
	"os"

	"mcp-ast/internal/lang"
)

type SearchResult struct {
	Total   int               `json:"total"`
	Matches []SearchMatch     `json:"matches"`
	Errors  map[string]string `json:"errors,omitempty"`
}

type SearchMatch struct {
	File string `json:"file"`
	Kind string `json:"kind"`
	Name string `json:"name"`
	Line int    `json:"line"`
	Col  int    `json:"col,omitempty"`
	Text string `json:"text,omitempty"`
}

// UnusedSymbols finds symbols declared but never referenced. Heuristic: a
// symbol whose name appears exactly once across the whole tree of recognized
// files is unused (the single occurrence is its own declaration). Comments and
// strings count as references, so this never flags a truly-referenced symbol.
// limit caps results (0 = all). Text is omitted from matches (use symbols_file
// / get_text_file when the body is needed).
func (e *Engine) UnusedSymbols(ctx context.Context, dir string, filter lang.Language, limit int) (*SearchResult, error) {
	files, errs, err := e.ScanSymbols(ctx, dir, filter, false)
	if err != nil {
		return nil, err
	}
	result := &SearchResult{Matches: []SearchMatch{}}
	if len(errs) > 0 {
		result.Errors = errs
	}
	decls := collectDeclarations(files)
	counts := countOccurrences(files, decls)
	for name, d := range decls {
		if counts[name] != 1 {
			continue
		}
		// Drop body text by default — name+location is enough to act.
		d.Text = ""
		result.Matches = append(result.Matches, d)
		result.Total++
		if limit > 0 && result.Total >= limit {
			return result, nil
		}
	}
	return result, nil
}

// collectDeclarations builds a map of symbol name → first declaration site,
// skipping imports.
func collectDeclarations(files map[string]map[string][]Symbol) map[string]SearchMatch {
	decls := make(map[string]SearchMatch)
	for path, kinds := range files {
		for kind, syms := range kinds {
			if kind == "imports" {
				continue
			}
			for _, sym := range syms {
				if _, seen := decls[sym.Name]; !seen {
					decls[sym.Name] = SearchMatch{
						File: path, Kind: kind, Name: sym.Name,
						Line: sym.Start.Row + 1, Col: sym.Start.Col, Text: sym.Text,
					}
				}
			}
		}
	}
	return decls
}

// countOccurrences counts total textual occurrences of each declared name
// across all files.
func countOccurrences(files map[string]map[string][]Symbol, decls map[string]SearchMatch) map[string]int {
	counts := make(map[string]int, len(decls))
	for path := range files {
		src, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for name := range decls {
			counts[name] += bytes.Count(src, []byte(name))
		}
	}
	return counts
}
