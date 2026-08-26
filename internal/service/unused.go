package service

import (
	"context"

	"mcp-ast/internal/engine"
)

// UnusedResult lists declared-but-unreferenced symbols (dead code heuristic).
type UnusedResult struct {
	Language string               `json:"language"`
	Symbols  []engine.SearchMatch `json:"symbols"`
	Errors   map[string]string    `json:"errors,omitempty"`
}

type UnusedService struct{ eng *engine.Engine }

// Dir finds unused symbols across dir for each requested language (empty =
// auto-detect). limit caps results (0 = all).
func (s *UnusedService) Dir(ctx context.Context, dir string, languages []string, limit int) (*UnusedResult, error) {
	fs, err := filters(s.eng, languages, dir)
	if err != nil {
		return nil, err
	}
	symbols := []engine.SearchMatch{}
	errs := map[string]string{}
	for _, f := range fs {
		r, err := s.eng.UnusedSymbols(ctx, dir, f, limit)
		if err != nil {
			return nil, err
		}
		symbols = append(symbols, r.Matches...)
	}
	if limit > 0 && len(symbols) > limit {
		symbols = symbols[:limit]
	}
	res := &UnusedResult{Language: displayLang(languages), Symbols: symbols}
	if len(errs) > 0 {
		res.Errors = errs
	}
	return res, nil
}
