package engine

import (
	"context"
	"fmt"

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
	errs, err := e.walkFiles(ctx, dir, filter, func(path string, l lang.Language) error {
		qs, ok := l.AuxQueries()["identifiers"]
		if !ok {
			return nil
		}
		src, tree, err := e.parseFile(l, path)
		if err != nil {
			return err
		}
		defer tree.Close()
		root := tree.RootNode()
		defPos := definitionPositions(l, root, src, name)
		callPos := callPositions(l, root, src, name)
		q, qerr := ts.NewQuery(l.Language(), qs)
		if qerr != nil {
			return fmt.Errorf("invalid identifier query: %s", qerr.Message)
		}
		defer q.Close()
		c := ts.NewQueryCursor()
		defer c.Close()
		it := c.Matches(q, root, src)
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
				matches = append(matches, u)
			}
		}
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
func definitionPositions(l lang.Language, root *ts.Node, src []byte, name string) map[Point]bool {
	defPos := make(map[Point]bool)
	for _, sq := range l.SymbolQueries() {
		q, qerr := ts.NewQuery(l.Language(), sq)
		if qerr != nil {
			continue
		}
		names := q.CaptureNames()
		c := ts.NewQueryCursor()
		it := c.Matches(q, root, src)
		for m := it.Next(); m != nil; m = it.Next() {
			for _, cap := range m.Captures {
				if names[cap.Index] == "name" && cap.Node.Utf8Text(src) == name {
					defPos[point(cap.Node.StartPosition())] = true
				}
			}
		}
		c.Close()
		q.Close()
	}
	return defPos
}

// callPositions maps the start position of every callee equal to target to the
// name of the function containing that call, using the language's calls query.
func callPositions(l lang.Language, root *ts.Node, src []byte, target string) map[Point]string {
	out := make(map[Point]string)
	qs, ok := l.AuxQueries()["calls"]
	if !ok {
		return out
	}
	funcs := functionRanges(l, root, src)
	if len(funcs) == 0 {
		return out
	}
	q, qerr := ts.NewQuery(l.Language(), qs)
	if qerr != nil {
		return out
	}
	defer q.Close()
	names := q.CaptureNames()
	c := ts.NewQueryCursor()
	defer c.Close()
	it := c.Matches(q, root, src)
	for m := it.Next(); m != nil; m = it.Next() {
		var callee string
		var pos Point
		for _, cap := range m.Captures {
			if names[cap.Index] == "callee" {
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
