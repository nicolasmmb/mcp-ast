package engine

import (
	"bytes"
	"fmt"

	ts "github.com/tree-sitter/go-tree-sitter"

	"mcp-ast/internal/lang"
)

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
	return e.complexityTree(l, src, tree.RootNode())
}

// complexityTree is Complexity over an already parsed tree.
func (e *Engine) complexityTree(l lang.Language, src []byte, root *ts.Node) ([]ComplexityEntry, error) {
	decisions := buildDecisionSet(l)
	var out []ComplexityEntry
	for _, kind := range []string{"functions", "methods", "constructors"} {
		entries := processFuncKind(l, kind, root, src, decisions)
		out = append(out, entries...)
	}
	return out, nil
}

// buildDecisionSet converts DecisionKinds() into a map for O(1) lookup.
func buildDecisionSet(l lang.Language) map[string]bool {
	decisions := make(map[string]bool, len(l.DecisionKinds()))
	for _, k := range l.DecisionKinds() {
		decisions[k] = true
	}
	return decisions
}

// processFuncKind queries one symbol kind and returns complexity entries.
func processFuncKind(l lang.Language, kind string, root *ts.Node, src []byte, decisions map[string]bool) []ComplexityEntry {
	qs, ok := l.SymbolQueries()[kind]
	if !ok {
		return nil
	}
	q, qerr := ts.NewQuery(l.Language(), qs)
	if qerr != nil {
		return nil
	}
	defer q.Close()
	c := ts.NewQueryCursor()
	defer c.Close()
	names := q.CaptureNames()
	it := c.Matches(q, root, src)
	var out []ComplexityEntry
	for m := it.Next(); m != nil; m = it.Next() {
		name, symNode := extractSymbolMatch(m, names, src)
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
	return out
}

// extractSymbolMatch extracts the name and symbol node from a query match.
func extractSymbolMatch(m *ts.QueryMatch, names []string, src []byte) (string, *ts.Node) {
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
	return name, symNode
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
	return e.analyzeTree(l, src, tree.RootNode())
}

// analyzeTree is Analyze over an already parsed tree.
func (e *Engine) analyzeTree(l lang.Language, src []byte, root *ts.Node) (*Metrics, error) {
	m := &Metrics{
		Lines: bytes.Count(src, []byte("\n")) + 1,
		Bytes: len(src),
		Kinds: make(map[string]KindMetric),
	}
	m.Nodes, m.MaxNesting = countNodes(root)
	for kind, qs := range l.SymbolQueries() {
		matches, err := e.runQuery(l, src, root, qs, 0, false)
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
