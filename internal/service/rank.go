package service

import (
	"context"
	"sort"

	"mcp-ast/internal/engine"
)

type RankResult struct {
	Language string                    `json:"language"`
	Entries  []engine.RankedComplexity `json:"entries"`
	Errors   map[string]string         `json:"errors,omitempty"`
}

type RankService struct{ eng *engine.Engine }

func (s *RankService) ComplexityDir(ctx context.Context, dir string, languages []string, limit int) (*RankResult, error) {
	fs, err := filters(s.eng, languages, dir)
	if err != nil {
		return nil, err
	}
	entries := make([]engine.RankedComplexity, 0)
	errs := map[string]string{}
	for _, f := range fs {
		es, fileErrs, err := s.eng.ComplexityDir(ctx, dir, f, 0)
		if err != nil {
			return nil, err
		}
		entries = append(entries, es...)
		for p, e := range fileErrs {
			errs[p] = e
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Complexity != entries[j].Complexity {
			return entries[i].Complexity > entries[j].Complexity
		}
		if entries[i].File != entries[j].File {
			return entries[i].File < entries[j].File
		}
		return entries[i].Name < entries[j].Name
	})
	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}
	res := &RankResult{Language: displayLang(languages), Entries: entries}
	if len(errs) > 0 {
		res.Errors = errs
	}
	return res, nil
}
