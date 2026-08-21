package engine

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	ts "github.com/tree-sitter/go-tree-sitter"

	"mcp-java-ast/internal/lang"
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
func (e *Engine) ScanSymbols(dir string, filter lang.Language) (map[string]map[string][]Symbol, map[string]string, error) {
	files := make(map[string]map[string][]Symbol)
	errs := make(map[string]string)
	walk := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
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

// KindMetric aggregates per-kind symbol metrics.
type KindMetric struct {
	Count    int     `json:"count"`
	AvgLines float64 `json:"avg_lines"`
	MaxLines int     `json:"max_lines"`
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
