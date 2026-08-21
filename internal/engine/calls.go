package engine

import (
	"context"
	"fmt"

	ts "github.com/tree-sitter/go-tree-sitter"

	"mcp-ast/internal/lang"
)

// CallEntry is a function/method with the callees it invokes.
type CallEntry struct {
	Name    string   `json:"name"`
	Kind    string   `json:"kind"`
	Callees []Callee `json:"callees"`
	Start   Point    `json:"start"`
	End     Point    `json:"end"`
}

// Callee is one called function/method name and how many times it is invoked.
type Callee struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// CallGraph maps each function/method in the file to the callees it invokes,
// using the language's call query. A call belongs to the function whose range
// contains it. Calls outside any function are reported under "".
func (e *Engine) CallGraph(l lang.Language, path string) ([]CallEntry, error) {
	qs, ok := l.AuxQueries()["calls"]
	if !ok {
		return nil, fmt.Errorf("language %s has no call query", l.Name())
	}
	src, tree, err := e.parseFile(l, path)
	if err != nil {
		return nil, err
	}
	defer tree.Close()
	root := tree.RootNode()

	funcs := functionRanges(l, root, src)
	if len(funcs) == 0 {
		return []CallEntry{}, nil
	}

	// collect calls and bucket them into the containing function
	q, qerr := ts.NewQuery(l.Language(), qs)
	if qerr != nil {
		return nil, fmt.Errorf("invalid call query: %s", qerr.Message)
	}
	defer q.Close()
	names := q.CaptureNames()
	c := ts.NewQueryCursor()
	defer c.Close()
	it := c.Matches(q, root, src)
	for m := it.Next(); m != nil; m = it.Next() {
		var callee string
		for _, cap := range m.Captures {
			if names[cap.Index] == "callee" {
				callee = cap.Node.Utf8Text(src)
			}
		}
		if callee == "" {
			continue
		}
		pos := point(m.Captures[0].Node.StartPosition())
		idx := findFunc(funcs, pos)
		if idx < 0 {
			continue
		}
		funcs[idx].Callees = appendCallee(funcs[idx].Callees, callee)
	}
	return funcs, nil
}

// functionRanges returns one CallEntry (without callees) per function, method
// or constructor in the tree, using the language's symbol queries.
func functionRanges(l lang.Language, root *ts.Node, src []byte) []CallEntry {
	var funcs []CallEntry
	for _, kind := range []string{"functions", "methods", "constructors"} {
		sqs, ok := l.SymbolQueries()[kind]
		if !ok {
			continue
		}
		q, qerr := ts.NewQuery(l.Language(), sqs)
		if qerr != nil {
			continue
		}
		names := q.CaptureNames()
		c := ts.NewQueryCursor()
		it := c.Matches(q, root, src)
		for m := it.Next(); m != nil; m = it.Next() {
			var name string
			var symNode *ts.Node
			for _, cap := range m.Captures {
				switch names[cap.Index] {
				case "name":
					name = cap.Node.Utf8Text(src)
				case "symbol":
					symNode = &cap.Node
				}
			}
			if symNode == nil {
				continue
			}
			funcs = append(funcs, CallEntry{
				Name:    name,
				Kind:    kind,
				Callees: []Callee{},
				Start:   point(symNode.StartPosition()),
				End:     point(symNode.EndPosition()),
			})
		}
		c.Close()
		q.Close()
	}
	return funcs
}

// Caller is one function that calls a target, with the call count.
type Caller struct {
	File  string `json:"file"`
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Line  int    `json:"line"`
	Col   int    `json:"col"`
	Count int    `json:"count"`
}

// Callers walks dir and finds every function/method that calls target,
// aggregating call sites per caller function. Uses the language's call query.
func (e *Engine) Callers(ctx context.Context, dir, target string, filter lang.Language, limit int) ([]Caller, map[string]string, error) {
	callers := make(map[string]*Caller)
	var order []string
	errs, err := e.walkFiles(ctx, dir, filter, func(path string, l lang.Language) error {
		qs, ok := l.AuxQueries()["calls"]
		if !ok {
			return nil
		}
		src, tree, err := e.parseFile(l, path)
		if err != nil {
			return err
		}
		defer tree.Close()
		root := tree.RootNode()
		funcs := functionRanges(l, root, src)
		q, qerr := ts.NewQuery(l.Language(), qs)
		if qerr != nil {
			return fmt.Errorf("invalid call query: %s", qerr.Message)
		}
		defer q.Close()
		names := q.CaptureNames()
		c := ts.NewQueryCursor()
		defer c.Close()
		it := c.Matches(q, root, src)
		for m := it.Next(); m != nil; m = it.Next() {
			var callee string
			for _, cap := range m.Captures {
				if names[cap.Index] == "callee" {
					callee = cap.Node.Utf8Text(src)
				}
			}
			if callee != target {
				continue
			}
			pos := point(m.Captures[0].Node.StartPosition())
			idx := findFunc(funcs, pos)
			if idx < 0 {
				continue
			}
			key := path + ":" + funcs[idx].Name
			cr, ok := callers[key]
			if !ok {
				cr = &Caller{
					File: path, Name: funcs[idx].Name, Kind: funcs[idx].Kind,
					Line: funcs[idx].Start.Row + 1, Col: funcs[idx].Start.Col,
				}
				callers[key] = cr
				order = append(order, key)
			}
			cr.Count++
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	out := make([]Caller, 0, len(order))
	for _, k := range order {
		out = append(out, *callers[k])
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, errs, nil
}

// findFunc returns the index of the innermost function whose range contains pos.
func findFunc(funcs []CallEntry, pos Point) int {
	best, bestIdx := -1, -1
	for i, f := range funcs {
		if inRange(f.Start, f.End, pos) && f.Start.Row > best {
			best, bestIdx = f.Start.Row, i
		}
	}
	return bestIdx
}

func inRange(start, end, pos Point) bool {
	return (pos.Row > start.Row || (pos.Row == start.Row && pos.Col >= start.Col)) &&
		(pos.Row < end.Row || (pos.Row == end.Row && pos.Col < end.Col))
}

func appendCallee(callees []Callee, name string) []Callee {
	for i := range callees {
		if callees[i].Name == name {
			callees[i].Count++
			return callees
		}
	}
	return append(callees, Callee{Name: name, Count: 1})
}
