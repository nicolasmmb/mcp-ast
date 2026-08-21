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
	Col  int    `json:"col"`
	Text string `json:"text"`
}

// SearchName walks dir and returns every symbol whose name equals name,
// using the language's built-in tree-sitter symbol queries (declarations
// only: classes, functions, variables, ...). limit caps matches (0 = all).
func (e *Engine) SearchName(ctx context.Context, dir, name string, filter lang.Language, limit int) (*SearchResult, error) {
	files, errs, err := e.ScanSymbols(ctx, dir, filter)
	if err != nil {
		return nil, err
	}
	result := &SearchResult{Matches: []SearchMatch{}}
	if len(errs) > 0 {
		result.Errors = errs
	}
	for path, kinds := range files {
		for kind, syms := range kinds {
			for _, sym := range syms {
				if sym.Name != name {
					continue
				}
				result.Matches = append(result.Matches, SearchMatch{
					File: path,
					Kind: kind,
					Name: sym.Name,
					Line: sym.Start.Row + 1,
					Col:  sym.Start.Col,
					Text: sym.Text,
				})
				result.Total++
				if limit > 0 && result.Total >= limit {
					return result, nil
				}
			}
		}
	}
	return result, nil
}

// UnusedSymbols finds symbols declared but never referenced. Heuristic: a
// symbol whose name appears exactly once across the whole tree of recognized
// files is unused (the single occurrence is its own declaration). Comments and
// strings count as references, so this never flags a truly-referenced symbol.
// limit caps results (0 = all).
func (e *Engine) UnusedSymbols(ctx context.Context, dir string, filter lang.Language, limit int) (*SearchResult, error) {
	files, errs, err := e.ScanSymbols(ctx, dir, filter)
	if err != nil {
		return nil, err
	}
	result := &SearchResult{Matches: []SearchMatch{}}
	if len(errs) > 0 {
		result.Errors = errs
	}
	// first pass: map symbol name -> one declaration site (file/kind/pos)
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
	// second pass: count total textual occurrences per name across all files
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
	for name, d := range decls {
		if counts[name] != 1 {
			continue
		}
		result.Matches = append(result.Matches, d)
		result.Total++
		if limit > 0 && result.Total >= limit {
			return result, nil
		}
	}
	return result, nil
}
