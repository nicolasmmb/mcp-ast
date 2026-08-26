package service

import (
	"context"

	"mcp-ast/internal/engine"
)

// CallersResult aggregates, per caller function, who invokes a target.
type CallersResult struct {
	Language string            `json:"language"`
	Callers  []engine.Caller   `json:"callers"`
	Errors   map[string]string `json:"errors,omitempty"`
}

type CallsService struct{ eng *engine.Engine }

// Callers finds every function/method across dir that calls target, for each
// requested language (empty = auto-detect). limit caps results (0 = all).
func (s *CallsService) Callers(ctx context.Context, name, dir string, languages []string, limit int) (*CallersResult, error) {
	fs, err := filters(s.eng, languages, dir)
	if err != nil {
		return nil, err
	}
	callers := []engine.Caller{}
	errs := map[string]string{}
	for _, f := range fs {
		cs, es, err := s.eng.Callers(ctx, dir, name, f, limit)
		if err != nil {
			return nil, err
		}
		callers = append(callers, cs...)
		for p, e := range es {
			errs[p] = e
		}
	}
	if limit > 0 && len(callers) > limit {
		callers = callers[:limit]
	}
	res := &CallersResult{Language: displayLang(languages), Callers: callers}
	if len(errs) > 0 {
		res.Errors = errs
	}
	return res, nil
}
