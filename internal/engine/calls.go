package engine

import (
	"context"
	"fmt"
	"sort"
	"sync"

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
	src, tree, err := e.parseFile(l, path)
	if err != nil {
		return nil, err
	}
	defer tree.Close()
	return e.callGraphTree(l, src, tree.RootNode())
}

// callGraphTree is CallGraph over an already parsed tree.
func (e *Engine) callGraphTree(l lang.Language, src []byte, root *ts.Node) ([]CallEntry, error) {
	if _, ok := l.AuxQueries()["calls"]; !ok {
		return nil, fmt.Errorf("language %s has no call query", l.Name())
	}
	funcs := e.functionRanges(l, root, src)
	if len(funcs) == 0 {
		return []CallEntry{}, nil
	}

	// collect calls and bucket them into the containing function
	cq, ok := e.reg.Compiled(l, lang.AuxKey("calls"))
	if !ok {
		return nil, fmt.Errorf("language %s has no compiled call query", l.Name())
	}
	c := ts.NewQueryCursor()
	defer c.Close()
	it := c.Matches(cq.Q, root, src)
	for m := it.Next(); m != nil; m = it.Next() {
		var callee string
		for _, cap := range m.Captures {
			if cq.Names[cap.Index] == "callee" {
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
func (e *Engine) functionRanges(l lang.Language, root *ts.Node, src []byte) []CallEntry {
	var funcs []CallEntry
	for _, kind := range []string{"functions", "methods", "constructors"} {
		_, ok := l.SymbolQueries()[kind]
		if !ok {
			continue
		}
		cq, ok := e.reg.Compiled(l, lang.SymbolKey(kind))
		if !ok {
			continue
		}
		c := ts.NewQueryCursor()
		it := c.Matches(cq.Q, root, src)
		for m := it.Next(); m != nil; m = it.Next() {
			var name string
			var symNode *ts.Node
			for _, cap := range m.Captures {
				switch cq.Names[cap.Index] {
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
	var mu sync.Mutex
	errs, err := e.walkFiles(ctx, dir, filter, func(path string, l lang.Language) error {
		cs, err := e.processFileCallers(l, path, target)
		if err != nil {
			return err
		}
		mu.Lock()
		for _, c := range cs {
			aggregateCaller(callers, &order, c)
		}
		mu.Unlock()
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(order)
	out := make([]Caller, 0, len(order))
	for _, k := range order {
		out = append(out, *callers[k])
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, errs, nil
}

// processFileCallers finds all calls to target in a single file and aggregates
// them into the callers map.
func (e *Engine) processFileCallers(l lang.Language, path, target string) ([]Caller, error) {
	if _, ok := l.AuxQueries()["calls"]; !ok {
		return nil, nil
	}
	src, tree, err := e.parseFile(l, path)
	if err != nil {
		return nil, err
	}
	defer tree.Close()
	root := tree.RootNode()
	funcs := e.functionRanges(l, root, src)
	cq, ok := e.reg.Compiled(l, lang.AuxKey("calls"))
	if !ok {
		return nil, fmt.Errorf("language %s has no compiled call query", l.Name())
	}
	c := ts.NewQueryCursor()
	defer c.Close()
	it := c.Matches(cq.Q, root, src)
	perCaller := map[string]Caller{}
	for m := it.Next(); m != nil; m = it.Next() {
		callee := extractCallee(m, cq.Names, src)
		if callee != target {
			continue
		}
		pos := point(m.Captures[0].Node.StartPosition())
		idx := findFunc(funcs, pos)
		if idx < 0 {
			continue
		}
		fn := funcs[idx]
		key := path + ":" + fn.Name
		item, ok := perCaller[key]
		if !ok {
			item = Caller{
				File: path, Name: fn.Name, Kind: fn.Kind,
				Line: fn.Start.Row + 1, Col: fn.Start.Col,
			}
		}
		item.Count++
		perCaller[key] = item
	}
	out := make([]Caller, 0, len(perCaller))
	for _, c := range perCaller {
		out = append(out, c)
	}
	return out, nil
}

// extractCallee returns the callee name from a call query match.
func extractCallee(m *ts.QueryMatch, names []string, src []byte) string {
	for _, cap := range m.Captures {
		if names[cap.Index] == "callee" {
			return cap.Node.Utf8Text(src)
		}
	}
	return ""
}

// aggregateCaller creates or increments a Caller entry in the map.
func aggregateCaller(callers map[string]*Caller, order *[]string, c Caller) {
	key := c.File + ":" + c.Name
	cr, ok := callers[key]
	if !ok {
		cr = &Caller{File: c.File, Name: c.Name, Kind: c.Kind, Line: c.Line, Col: c.Col}
		callers[key] = cr
		*order = append(*order, key)
	}
	cr.Count += c.Count
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
