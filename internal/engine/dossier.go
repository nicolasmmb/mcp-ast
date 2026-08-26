package engine

import (
	"mcp-ast/internal/lang"
)

// FileReport bundles everything analyze_file returns for a single file.
type FileReport struct {
	Language   string            `json:"language"`
	Metrics    *Metrics          `json:"metrics"`
	Complexity []ComplexityEntry `json:"complexity"`
	CallGraph  []CallEntry       `json:"call_graph"`
}

// Dossier parses path once and computes size/complexity metrics, per-function
// cyclomatic complexity and the call graph from the same tree.
func (e *Engine) Dossier(l lang.Language, path string) (*FileReport, error) {
	src, tree, err := e.parseFile(l, path)
	if err != nil {
		return nil, err
	}
	defer tree.Close()
	root := tree.RootNode()
	m, err := e.analyzeTree(l, src, root)
	if err != nil {
		return nil, err
	}
	cx, err := e.complexityTree(l, src, root)
	if err != nil {
		return nil, err
	}
	cg, err := e.callGraphTree(l, src, root)
	if err != nil {
		return nil, err
	}
	return &FileReport{Language: l.Name(), Metrics: m, Complexity: cx, CallGraph: cg}, nil
}
