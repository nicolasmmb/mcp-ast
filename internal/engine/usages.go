package engine

import (
	"context"
	"fmt"
	"sync"

	ts "github.com/tree-sitter/go-tree-sitter"

	"mcp-ast/internal/lang"
)

// UsageMatch is one occurrence of a symbol name, classified by role.
type UsageMatch struct {
	File   string `json:"file"`
	Name   string `json:"name,omitempty"`
	Line   int    `json:"line"`
	Col    int    `json:"col"`
	Text   string `json:"text"`
	Kind   string `json:"kind"`             // "definition", "reference" or "call-site"
	Caller string `json:"caller,omitempty"` // function containing the call (call-sites only)
}

// Usages finds every occurrence of name across the directory's recognized
// files (via the language's identifier query) and classifies each occurrence:
// "definition" is a declaration site, "call-site" is the callee of an
// invocation and carries the enclosing function in Caller, anything else is a
// "reference". limit caps returned matches (0 = all); matching files are fully
// parsed even past the limit.
func (e *Engine) Usages(ctx context.Context, dir, name string, filter lang.Language, limit int) ([]UsageMatch, map[string]string, error) {
	matches := []UsageMatch{}
	var mu sync.Mutex
	errs, err := e.walkFiles(ctx, dir, filter, func(path string, l lang.Language) error {
		fileMatches, err := e.classifyUsages(l, path, name)
		if err != nil {
			return err
		}
		mu.Lock()
		matches = append(matches, fileMatches...)
		mu.Unlock()
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	if limit > 0 && len(matches) > limit {
		matches = matches[:limit]
	}
	return matches, errs, nil
}

func (e *Engine) classifyUsages(l lang.Language, path, name string) ([]UsageMatch, error) {
	src, tree, err := e.parseFile(l, path)
	if err != nil {
		return nil, err
	}
	defer tree.Close()
	return e.classifyUsagesTree(l, path, src, tree.RootNode(), name, false)
}

// classifyUsagesTree classifies identifier captures from an already parsed tree.
// When all is false, it returns only occurrences of name.
func (e *Engine) classifyUsagesTree(l lang.Language, path string, src []byte, root *ts.Node, name string, all bool) ([]UsageMatch, error) {
	if _, ok := l.AuxQueries()["identifiers"]; !ok {
		return []UsageMatch{}, nil
	}
	defPos := e.definitionPositions(l, root, src)
	callPos := e.callPositions(l, root, src)
	cq, ok := e.reg.Compiled(l, lang.AuxKey("identifiers"))
	if !ok {
		return nil, fmt.Errorf("compiled identifier query not found for %s", l.Name())
	}
	c := ts.NewQueryCursor()
	defer c.Close()
	it := c.Matches(cq.Q, root, src)
	matches := make([]UsageMatch, 0, 8)
	for m := it.Next(); m != nil; m = it.Next() {
		for _, cap := range m.Captures {
			if !all && cap.Node.Utf8Text(src) != name {
				continue
			}
			start := point(cap.Node.StartPosition())
			match := UsageMatch{
				File: path,
				Name: cap.Node.Utf8Text(src),
				Line: start.Row + 1,
				Col:  start.Col,
				Text: firstLine(cap.Node.Parent().Utf8Text(src)),
			}
			if defPos[start] {
				match.Kind = "definition"
			} else if caller, ok := callPos[start]; ok {
				match.Kind, match.Caller = "call-site", caller
			} else {
				match.Kind = "reference"
			}
			matches = append(matches, match)
		}
	}
	return matches, nil
}

// definitionPositions collects every declaration's start position from the
// @name captures of the symbol queries.
func (e *Engine) definitionPositions(l lang.Language, root *ts.Node, src []byte) map[Point]bool {
	defPos := make(map[Point]bool)
	for kind := range l.SymbolQueries() {
		cq, ok := e.reg.Compiled(l, lang.SymbolKey(kind))
		if !ok {
			continue
		}
		c := ts.NewQueryCursor()
		it := c.Matches(cq.Q, root, src)
		for m := it.Next(); m != nil; m = it.Next() {
			for _, cap := range m.Captures {
				if cq.Names[cap.Index] == "name" {
					defPos[point(cap.Node.StartPosition())] = true
				}
			}
		}
		c.Close()
	}
	return defPos
}

// callPositions maps every callee's start position to its containing function.
func (e *Engine) callPositions(l lang.Language, root *ts.Node, src []byte) map[Point]string {
	out := make(map[Point]string)
	if _, ok := l.AuxQueries()["calls"]; !ok {
		return out
	}
	funcs := e.functionRanges(l, root, src)
	if len(funcs) == 0 {
		return out
	}
	cq, ok := e.reg.Compiled(l, lang.AuxKey("calls"))
	if !ok {
		return out
	}
	c := ts.NewQueryCursor()
	defer c.Close()
	it := c.Matches(cq.Q, root, src)
	for m := it.Next(); m != nil; m = it.Next() {
		var pos Point
		found := false
		for _, cap := range m.Captures {
			if cq.Names[cap.Index] == "callee" {
				pos = point(cap.Node.StartPosition())
				found = true
			}
		}
		if found {
			idx := findFunc(funcs, pos)
			if idx < 0 {
				continue
			}
			out[pos] = funcs[idx].Name
		}
	}
	return out
}
