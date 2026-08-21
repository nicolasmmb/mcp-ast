package engine

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	ts "github.com/tree-sitter/go-tree-sitter"

	"mcp-ast/internal/lang"
)

type Point struct {
	Row int `json:"row"`
	Col int `json:"col"`
}

type Node struct {
	Type     string  `json:"type"`
	Field    string  `json:"field,omitempty"`
	Named    bool    `json:"named"`
	Start    Point   `json:"start"`
	End      Point   `json:"end"`
	Children []*Node `json:"children,omitempty"`
}

type Capture struct {
	Name  string `json:"name"`
	Text  string `json:"text"`
	Start Point  `json:"start"`
	End   Point  `json:"end"`
}

type Match struct {
	Captures []Capture `json:"captures"`
}

type Symbol struct {
	Name  string `json:"name"`
	Text  string `json:"text"`
	Start Point  `json:"start"`
	End   Point  `json:"end"`
}

type Engine struct {
	reg *lang.Registry
}

func New(reg *lang.Registry) *Engine { return &Engine{reg: reg} }

func (e *Engine) Resolve(name, path string) (lang.Language, error) {
	return e.reg.Resolve(name, path)
}

func (e *Engine) ListLanguages() []string { return e.reg.List() }

// Parse reads the file, parses it and converts the tree to a generic JSON
// node. maxDepth truncates the tree (0 = unlimited). Returns whether the tree
// contains ERROR nodes.
func (e *Engine) Parse(l lang.Language, path string, maxDepth int) (*Node, bool, error) {
	_, tree, err := e.parseFile(l, path)
	if err != nil {
		return nil, false, err
	}
	defer tree.Close()
	root := tree.RootNode()
	return toNode(root, maxDepth, 0), root.HasError(), nil
}

// Query runs a tree-sitter query over the file and returns one Match per
// pattern match, with every capture's name, text and position.
func (e *Engine) Query(l lang.Language, path, querySrc string) ([]Match, error) {
	return e.QueryLimit(l, path, querySrc, 0)
}

// QueryLimit runs a tree-sitter query over the file and returns one Match per
// pattern match, with every capture's name, text and position. limit caps the
// number of matches (0 = unlimited).
func (e *Engine) QueryLimit(l lang.Language, path, querySrc string, limit int) ([]Match, error) {
	return e.QueryText(l, path, querySrc, limit, false)
}

// QueryText is QueryLimit with a fullText switch: when true, capture text is
// the full node source instead of the first-line summary.
func (e *Engine) QueryText(l lang.Language, path, querySrc string, limit int, fullText bool) ([]Match, error) {
	src, tree, err := e.parseFile(l, path)
	if err != nil {
		return nil, err
	}
	defer tree.Close()
	return e.runQuery(l, src, tree.RootNode(), querySrc, limit, fullText)
}

// Symbols runs the language's built-in symbol queries and returns the
// results grouped by kind (classes, methods, fields, imports, ...).
func (e *Engine) Symbols(l lang.Language, path string) (map[string][]Symbol, error) {
	return e.SymbolsText(l, path, false)
}

// SymbolsText is Symbols with a fullText switch: when true, symbol text is the
// full node source instead of the first-line summary.
func (e *Engine) SymbolsText(l lang.Language, path string, fullText bool) (map[string][]Symbol, error) {
	src, tree, err := e.parseFile(l, path)
	if err != nil {
		return nil, err
	}
	defer tree.Close()
	out := make(map[string][]Symbol)
	for kind, qs := range l.SymbolQueries() {
		matches, err := e.runQuery(l, src, tree.RootNode(), qs, 0, fullText)
		if err != nil {
			return nil, fmt.Errorf("symbol query %q: %w", kind, err)
		}
		syms := make([]Symbol, 0, len(matches))
		for _, m := range matches {
			var sym Symbol
			for _, c := range m.Captures {
				switch c.Name {
				case "name":
					sym.Name = c.Text
				case "symbol":
					sym.Start, sym.End = c.Start, c.End
					sym.Text = c.Text
				}
			}
			if sym.Name == "" {
				sym.Name = sym.Text
			}
			syms = append(syms, sym)
		}
		out[kind] = syms
	}
	return out, nil
}

// ScanSymbols walks dir recursively and collects symbols for every file that
// matches the language's extensions (or auto-detected when filter is nil).
// Unreadable/unparseable files are reported in errors instead of failing.
// ctx cancels the walk (e.g. tool-call timeout); on cancel it returns ctx.Err.
func (e *Engine) ScanSymbols(ctx context.Context, dir string, filter lang.Language) (map[string]map[string][]Symbol, map[string]string, error) {
	files := make(map[string]map[string][]Symbol)
	errs := make(map[string]string)
	walk := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if path != dir && strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		var l lang.Language
		if filter != nil {
			if !hasExt(d.Name(), filter.Extensions()) {
				return nil
			}
			l = filter
		} else {
			ll, err := e.reg.Resolve("", path)
			if err != nil {
				return nil
			}
			l = ll
		}
		syms, err := e.Symbols(l, path)
		if err != nil {
			errs[path] = err.Error()
			return nil
		}
		files[path] = syms
		return nil
	})
	if err := walk; err != nil {
		return nil, nil, err
	}
	return files, errs, nil
}

func hasExt(name string, exts []string) bool {
	for _, x := range exts {
		if strings.HasSuffix(name, x) {
			return true
		}
	}
	return false
}

// ScanVariables walks dir recursively and returns only the variables of every
// recognized source file, grouped by file path. Mirrors ScanSymbols.
func (e *Engine) ScanVariables(ctx context.Context, dir string, filter lang.Language) (map[string][]Symbol, map[string]string, error) {
	files, errs, err := e.ScanSymbols(ctx, dir, filter)
	if err != nil {
		return nil, nil, err
	}
	variables := make(map[string][]Symbol)
	for path, syms := range files {
		if v, ok := syms["variables"]; ok && len(v) > 0 {
			variables[path] = v
		}
	}
	return variables, errs, nil
}

// KindMetric aggregates per-kind symbol metrics.
type KindMetric struct {
	Count    int     `json:"count"`
	AvgLines float64 `json:"avg_lines"`
	MaxLines int     `json:"max_lines"`
}

// ComplexityEntry is one function/method's cyclomatic complexity.
type ComplexityEntry struct {
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Complexity int    `json:"complexity"`
	Start      Point  `json:"start"`
	End        Point  `json:"end"`
}

// Complexity computes cyclomatic complexity (1 + decision points) for every
// function and method in the file, using the language's decision node kinds.
func (e *Engine) Complexity(l lang.Language, path string) ([]ComplexityEntry, error) {
	src, tree, err := e.parseFile(l, path)
	if err != nil {
		return nil, err
	}
	defer tree.Close()
	root := tree.RootNode()
	decisions := make(map[string]bool, len(l.DecisionKinds()))
	for _, k := range l.DecisionKinds() {
		decisions[k] = true
	}
	// function-like symbol queries: name comes from the @name capture,
	// the body range from the @symbol capture.
	funcKinds := []string{"functions", "methods", "constructors"}
	out := []ComplexityEntry{}
	for _, kind := range funcKinds {
		qs, ok := l.SymbolQueries()[kind]
		if !ok {
			continue
		}
		q, qerr := ts.NewQuery(l.Language(), qs)
		if qerr != nil {
			return nil, fmt.Errorf("invalid query for %q: %s", kind, qerr.Message)
		}
		c := ts.NewQueryCursor()
		names := q.CaptureNames()
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
			out = append(out, ComplexityEntry{
				Name:       name,
				Kind:       kind,
				Complexity: complexityOf(symNode, src, decisions),
				Start:      point(symNode.StartPosition()),
				End:        point(symNode.EndPosition()),
			})
		}
		c.Close()
		q.Close()
	}
	return out, nil
}

// complexityOf counts decision nodes in n's subtree: every node whose kind is
// in decisions adds 1, except binary_expression which only counts when its
// operator is && or ||. Base complexity is 1.
func complexityOf(n *ts.Node, src []byte, decisions map[string]bool) int {
	complexity := 1
	var walk func(*ts.Node)
	walk = func(nn *ts.Node) {
		kind := nn.Kind()
		if decisions[kind] {
			if kind == "binary_expression" {
				if hasLogicalOp(nn, src) {
					complexity++
				}
			} else {
				complexity++
			}
		}
		for i := uint(0); i < nn.ChildCount(); i++ {
			walk(nn.Child(i))
		}
	}
	walk(n)
	return complexity
}

// hasLogicalOp reports whether a binary_expression's operator is && or ||.
func hasLogicalOp(n *ts.Node, src []byte) bool {
	for i := uint(0); i < n.ChildCount(); i++ {
		ch := n.Child(i)
		if ch.ChildCount() == 0 {
			op := ch.Utf8Text(src)
			if op == "&&" || op == "||" {
				return true
			}
		}
	}
	return false
}

// Metrics is the per-file analysis report.
type Metrics struct {
	Lines      int                   `json:"lines"`
	Bytes      int                   `json:"bytes"`
	Nodes      int                   `json:"nodes"`
	MaxNesting int                   `json:"max_nesting"`
	Kinds      map[string]KindMetric `json:"kinds"`
}

// Analyze computes file-level metrics (size, node count, nesting depth) and
// per-symbol-kind line statistics using the language's built-in queries.
func (e *Engine) Analyze(l lang.Language, path string) (*Metrics, error) {
	src, tree, err := e.parseFile(l, path)
	if err != nil {
		return nil, err
	}
	defer tree.Close()
	m := &Metrics{
		Lines: bytes.Count(src, []byte("\n")) + 1,
		Bytes: len(src),
		Kinds: make(map[string]KindMetric),
	}
	m.Nodes, m.MaxNesting = countNodes(tree.RootNode())
	for kind, qs := range l.SymbolQueries() {
		matches, err := e.runQuery(l, src, tree.RootNode(), qs, 0, false)
		if err != nil {
			return nil, fmt.Errorf("symbol query %q: %w", kind, err)
		}
		km := KindMetric{Count: len(matches)}
		total := 0
		for _, mt := range matches {
			for _, c := range mt.Captures {
				if c.Name == "symbol" {
					lines := c.End.Row - c.Start.Row + 1
					total += lines
					if lines > km.MaxLines {
						km.MaxLines = lines
					}
				}
			}
		}
		if km.Count > 0 {
			km.AvgLines = float64(total) / float64(km.Count)
		}
		m.Kinds[kind] = km
	}
	return m, nil
}

func countNodes(n *ts.Node) (nodes, maxDepth int) {
	var walk func(*ts.Node, int)
	walk = func(nn *ts.Node, depth int) {
		nodes++
		if depth > maxDepth {
			maxDepth = depth
		}
		for i := uint(0); i < nn.ChildCount(); i++ {
			walk(nn.Child(i), depth+1)
		}
	}
	walk(n, 1)
	return
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

// RenameMatch is a single occurrence of a name to consider during a rename.
type RenameMatch struct {
	File       string `json:"file"`
	Line       int    `json:"line"`
	Col        int    `json:"col"`
	Text       string `json:"text"`
	Definition bool   `json:"definition"`
}

// RenamePreview finds every occurrence of name across the directory's
// recognized files (via the language's identifier query) and flags which
// occurrences are declarations. Useful to preview a rename before editing.
func (e *Engine) RenamePreview(ctx context.Context, dir, name, langName string, limit int) ([]RenameMatch, map[string]string, error) {
	var filter lang.Language
	if langName != "" {
		var err error
		filter, err = e.reg.Resolve(langName, dir)
		if err != nil {
			return nil, nil, err
		}
	}
	files, errs, err := e.ScanSymbols(ctx, dir, filter)
	if err != nil {
		return nil, nil, err
	}
	var matches []RenameMatch
	for path := range files {
		l, err := e.reg.Resolve("", path)
		if err != nil {
			continue
		}
		qs, ok := l.AuxQueries()["identifiers"]
		if !ok {
			continue
		}
		src, tree, err := e.parseFile(l, path)
		if err != nil {
			continue
		}
		root := tree.RootNode()
		// collect definition positions from the symbol queries' @name captures
		defPos := make(map[Point]bool)
		for _, sq := range l.SymbolQueries() {
			q, qerr := ts.NewQuery(l.Language(), sq)
			if qerr != nil {
				continue
			}
			names := q.CaptureNames()
			cc := ts.NewQueryCursor()
			it := cc.Matches(q, root, src)
			for m := it.Next(); m != nil; m = it.Next() {
				for _, cap := range m.Captures {
					if names[cap.Index] == "name" && cap.Node.Utf8Text(src) == name {
						defPos[point(cap.Node.StartPosition())] = true
					}
				}
			}
			cc.Close()
			q.Close()
		}
		q, qerr := ts.NewQuery(l.Language(), qs)
		if qerr != nil {
			tree.Close()
			continue
		}
		c := ts.NewQueryCursor()
		it := c.Matches(q, root, src)
		for m := it.Next(); m != nil; m = it.Next() {
			for _, cap := range m.Captures {
				text := cap.Node.Utf8Text(src)
				if text != name {
					continue
				}
				start := point(cap.Node.StartPosition())
				matches = append(matches, RenameMatch{
					File:       path,
					Line:       start.Row + 1,
					Col:        start.Col,
					Text:       firstLine(cap.Node.Parent().Utf8Text(src)),
					Definition: defPos[start],
				})
				if limit > 0 && len(matches) >= limit {
					break
				}
			}
			if limit > 0 && len(matches) >= limit {
				break
			}
		}
		c.Close()
		q.Close()
		tree.Close()
		if limit > 0 && len(matches) >= limit {
			return matches, errs, nil
		}
	}
	return matches, errs, nil
}

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
func (e *Engine) Callers(ctx context.Context, dir, target, langName string, limit int) ([]Caller, map[string]string, error) {
	var filter lang.Language
	if langName != "" {
		var err error
		filter, err = e.reg.Resolve(langName, dir)
		if err != nil {
			return nil, nil, err
		}
	}
	errs := make(map[string]string)
	callers := make(map[string]*Caller)
	var order []string
	walk := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if path != dir && strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		var l lang.Language
		if filter != nil {
			if !hasExt(d.Name(), filter.Extensions()) {
				return nil
			}
			l = filter
		} else {
			ll, err := e.reg.Resolve("", path)
			if err != nil {
				return nil
			}
			l = ll
		}
		qs, ok := l.AuxQueries()["calls"]
		if !ok {
			return nil
		}
		src, tree, err := e.parseFile(l, path)
		if err != nil {
			errs[path] = err.Error()
			return nil
		}
		root := tree.RootNode()
		funcs := functionRanges(l, root, src)
		q, qerr := ts.NewQuery(l.Language(), qs)
		if qerr != nil {
			tree.Close()
			errs[path] = qerr.Message
			return nil
		}
		names := q.CaptureNames()
		c := ts.NewQueryCursor()
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
		c.Close()
		q.Close()
		tree.Close()
		return nil
	})
	if err := walk; err != nil {
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

// GetText returns the exact source slice of a 0-based (row, col) range,
// e.g. the positions reported on every Node/Capture/Symbol.
func (e *Engine) GetText(l lang.Language, path string, start, end Point) (string, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", path, err)
	}
	s := byteOffset(src, start.Row, start.Col)
	en := byteOffset(src, end.Row, end.Col)
	if en < s {
		return "", fmt.Errorf("end position %+v precedes start %+v", end, start)
	}
	if en > len(src) {
		en = len(src)
	}
	return string(src[s:en]), nil
}

// byteOffset maps a 0-based (row, col) to a byte offset. col is a byte column.
func byteOffset(src []byte, row, col int) int {
	off := 0
	for r := 0; r < row; r++ {
		nl := bytes.IndexByte(src[off:], '\n')
		if nl < 0 {
			return len(src)
		}
		off += nl + 1
	}
	if off+col > len(src) {
		return len(src)
	}
	return off + col
}

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
func (e *Engine) SearchName(ctx context.Context, dir, name, langName string, limit int) (*SearchResult, error) {
	var filter lang.Language
	if langName != "" {
		var err error
		filter, err = e.reg.Resolve(langName, dir)
		if err != nil {
			return nil, err
		}
	}
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

func (e *Engine) parseFile(l lang.Language, path string) ([]byte, *ts.Tree, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("reading %s: %w", path, err)
	}
	p, release := e.reg.Acquire(l)
	defer release()
	return src, p.Parse(src, nil), nil
}

func (e *Engine) runQuery(l lang.Language, src []byte, root *ts.Node, querySrc string, limit int, fullText bool) ([]Match, error) {
	q, qerr := ts.NewQuery(l.Language(), querySrc)
	if qerr != nil {
		return nil, fmt.Errorf("invalid query: %s", qerr.Message)
	}
	defer q.Close()
	c := ts.NewQueryCursor()
	defer c.Close()
	names := q.CaptureNames()

	matches := []Match{}
	it := c.Matches(q, root, src)
	for m := it.Next(); m != nil; m = it.Next() {
		match := Match{Captures: []Capture{}}
		for _, cap := range m.Captures {
			text := cap.Node.Utf8Text(src)
			if !fullText {
				text = firstLine(text)
			}
			match.Captures = append(match.Captures, Capture{
				Name:  names[cap.Index],
				Text:  text,
				Start: point(cap.Node.StartPosition()),
				End:   point(cap.Node.EndPosition()),
			})
		}
		matches = append(matches, match)
		if limit > 0 && len(matches) >= limit {
			break
		}
	}
	return matches, nil
}

func toNode(n *ts.Node, maxDepth, depth int) *Node {
	nd := &Node{
		Type:  n.Kind(),
		Named: n.IsNamed(),
		Start: point(n.StartPosition()),
		End:   point(n.EndPosition()),
	}
	if maxDepth > 0 && depth >= maxDepth {
		return nd
	}
	for i := uint(0); i < n.ChildCount(); i++ {
		c := n.Child(i)
		cn := toNode(c, maxDepth, depth+1)
		cn.Field = n.FieldNameForChild(uint32(i))
		nd.Children = append(nd.Children, cn)
	}
	return nd
}

func point(p ts.Point) Point { return Point{Row: int(p.Row), Col: int(p.Column)} }

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 200 {
		s = s[:197] + "..."
	}
	return s
}
