package engine

import (
	"fmt"
	"os"
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
	src, tree, err := e.parseFile(l, path)
	if err != nil {
		return nil, err
	}
	defer tree.Close()
	return e.runQuery(l, src, tree.RootNode(), querySrc)
}

// Symbols runs the language's built-in symbol queries and returns the
// results grouped by kind (classes, methods, fields, imports, ...).
func (e *Engine) Symbols(l lang.Language, path string) (map[string][]Symbol, error) {
	src, tree, err := e.parseFile(l, path)
	if err != nil {
		return nil, err
	}
	defer tree.Close()
	out := make(map[string][]Symbol)
	for kind, qs := range l.SymbolQueries() {
		matches, err := e.runQuery(l, src, tree.RootNode(), qs)
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

func (e *Engine) parseFile(l lang.Language, path string) ([]byte, *ts.Tree, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("reading %s: %w", path, err)
	}
	p, release := e.reg.Acquire(l)
	defer release()
	return src, p.Parse(src, nil), nil
}

func (e *Engine) runQuery(l lang.Language, src []byte, root *ts.Node, querySrc string) ([]Match, error) {
	q, qerr := ts.NewQuery(l.Language(), querySrc)
	if qerr != nil {
		return nil, fmt.Errorf("invalid query: %s", qerr.Message)
	}
	defer q.Close()
	c := ts.NewQueryCursor()
	defer c.Close()
	names := q.CaptureNames()

	var matches []Match
	it := c.Matches(q, root, src)
	for m := it.Next(); m != nil; m = it.Next() {
		match := Match{}
		for _, cap := range m.Captures {
			match.Captures = append(match.Captures, Capture{
				Name:  names[cap.Index],
				Text:  firstLine(cap.Node.Utf8Text(src)),
				Start: point(cap.Node.StartPosition()),
				End:   point(cap.Node.EndPosition()),
			})
		}
		matches = append(matches, match)
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
