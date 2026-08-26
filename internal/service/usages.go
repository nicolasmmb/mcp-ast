package service

import (
	"context"

	"mcp-ast/internal/engine"
)

// UsagesResult lists classified occurrences of one symbol name.
type UsagesResult struct {
	Language string              `json:"language"`
	Matches  []engine.UsageMatch `json:"matches"`
	Errors   map[string]string   `json:"errors,omitempty"`
}

type UsagesService struct{ eng *engine.Engine }

// Dir finds and classifies every occurrence of name across dir for each
// requested language (empty = auto-detect). limit caps matches (0 = all).
func (s *UsagesService) Dir(ctx context.Context, name, dir string, languages []string, limit int) (*UsagesResult, error) {
	fs, err := filters(s.eng, languages, dir)
	if err != nil {
		return nil, err
	}
	matches := []engine.UsageMatch{}
	errs := map[string]string{}
	for _, f := range fs {
		ms, es, err := s.eng.Usages(ctx, dir, name, f, limit)
		if err != nil {
			return nil, err
		}
		matches = append(matches, ms...)
		for p, e := range es {
			errs[p] = e
		}
	}
	if limit > 0 && len(matches) > limit {
		matches = matches[:limit]
	}
	res := &UsagesResult{Language: displayLang(languages), Matches: matches}
	if len(errs) > 0 {
		res.Errors = errs
	}
	return res, nil
}
