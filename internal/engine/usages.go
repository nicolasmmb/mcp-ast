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
		_, ok := l.AuxQueries()["identifiers"]
		if !ok {
			return nil
		}
		src, tree, err := e.parseFile(l, path)
		if err != nil {
			return err
		}
		defer tree.Close()
		root := tree.RootNode()
		defPos := e.definitionPositions(l, root, src, name)
		callPos := e.callPositions(l, root, src, name)
		cq, ok := e.reg.Compiled(l, lang.AuxKey("identifiers"))
		if !ok {
			return fmt.Errorf("compiled identifier query not found for %s", l.Name())
		}
		c := ts.NewQueryCursor()
		defer c.Close()
		it := c.Matches(cq.Q, root, src)
		fileMatches := make([]UsageMatch, 0, 8)
		for m := it.Next(); m != nil; m = it.Next() {
			for _, cap := range m.Captures {
				if cap.Node.Utf8Text(src) != name {
					continue
				}
				start := point(cap.Node.StartPosition())
				u := UsageMatch{
					File: path,
					Line: start.Row + 1,
					Col:  start.Col,
					Text: firstLine(cap.Node.Parent().Utf8Text(src)),
				}
				switch {
				case defPos[start]:
					u.Kind = "definition"
				default:
					if caller, ok := callPos[start]; ok {
						u.Kind, u.Caller = "call-site", caller
					} else {
						u.Kind = "reference"
					}
				}
				fileMatches = append(fileMatches, u)
			}
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

// definitionPositions collects the start positions of every declaration of
// name in the file, from the @name captures of the symbol queries.
func (e *Engine) definitionPositions(l lang.Language, root *ts.Node, src []byte, name string) map[Point]bool {
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
				if cq.Names[cap.Index] == "name" && cap.Node.Utf8Text(src) == name {
					defPos[point(cap.Node.StartPosition())] = true
				}
			}
		}
		c.Close()
	}
	return defPos
}

// callPositions maps the start position of every callee equal to target to the
// name of the function containing that call, using the language's calls query.
func (e *Engine) callPositions(l lang.Language, root *ts.Node, src []byte, target string) map[Point]string {
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
		var callee string
		var pos Point
		for _, cap := range m.Captures {
			if cq.Names[cap.Index] == "callee" {
				callee = cap.Node.Utf8Text(src)
				pos = point(cap.Node.StartPosition())
			}
		}
		if callee != target {
			continue
		}
		if idx := findFunc(funcs, pos); idx >= 0 {
			out[pos] = funcs[idx].Name
		}
	}
	return out
}
