package service

import "mcp-ast/internal/engine"

// FileReport is the analyze_file dossier: metrics, cyclomatic complexity and
// call graph for one file (single parse inside the engine).
type FileReport = engine.FileReport

// SymbolsResult lists a file's symbols grouped by kind.
type SymbolsResult struct {
	Language string                     `json:"language"`
	Symbols  map[string][]engine.Symbol `json:"symbols"`
}

type FileAnalysisService struct{ eng *engine.Engine }

// Dossier returns metrics + complexity + call graph for path, resolved to
// langName (empty = auto-detect by extension).
func (s *FileAnalysisService) Dossier(langName, path string) (*FileReport, error) {
	l, err := s.eng.Resolve(langName, path)
	if err != nil {
		return nil, err
	}
	return s.eng.Dossier(l, path)
}

// Symbols extracts one file's symbols grouped by kind.
func (s *FileAnalysisService) Symbols(langName, path string, includeText bool) (*SymbolsResult, error) {
	l, err := s.eng.Resolve(langName, path)
	if err != nil {
		return nil, err
	}
	syms, err := s.eng.SymbolsText(l, path, includeText)
	if err != nil {
		return nil, err
	}
	return &SymbolsResult{Language: l.Name(), Symbols: syms}, nil
}

// OutlineResult is the hierarchical symbol outline for one file.
type OutlineResult struct {
	Language string                `json:"language"`
	Path     string                `json:"path"`
	Outline  []*engine.OutlineNode `json:"outline"`
}

// Outline returns a hierarchical symbol tree for path.
func (s *FileAnalysisService) Outline(langName, path string, includeText bool) (*OutlineResult, error) {
	l, err := s.eng.Resolve(langName, path)
	if err != nil {
		return nil, err
	}
	nodes, err := s.eng.Outline(l, path, includeText)
	if err != nil {
		return nil, err
	}
	return &OutlineResult{Language: l.Name(), Path: path, Outline: nodes}, nil
}
