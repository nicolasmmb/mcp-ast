package service

import (
	"context"
	"strings"

	"mcp-ast/internal/engine"
)

// ScanQuery is a directory symbol scan request.
type ScanQuery struct {
	Dir         string
	Languages   []string
	Kinds       []string // empty = all kinds
	Name        string   // exact symbol name filter; empty = all
	IncludeText bool
	Limit       int // max files in result; 0 = unlimited
}

// ScanResult groups symbols by file path, then kind.
type ScanResult struct {
	Language string                                `json:"language"`
	Files    map[string]map[string][]engine.Symbol `json:"files"`
	Errors   map[string]string                     `json:"errors,omitempty"`
}

type ScanService struct{ eng *engine.Engine }

// Dir scans q.Dir recursively for every requested language, then prunes the
// result by kind and exact name. Files left with no matching symbol are
// removed; per-file errors do not abort the scan.
func (s *ScanService) Dir(ctx context.Context, q ScanQuery) (*ScanResult, error) {
	fs, err := filters(s.eng, q.Languages, q.Dir)
	if err != nil {
		return nil, err
	}
	files := map[string]map[string][]engine.Symbol{}
	errs := map[string]string{}
	for _, f := range fs {
		fls, es, err := s.eng.ScanSymbols(ctx, q.Dir, f, q.IncludeText)
		if err != nil {
			return nil, err
		}
		for p, syms := range fls {
			files[p] = syms
		}
		for p, e := range es {
			errs[p] = e
		}
	}
	pruneGroups(files, kindSet(q.Kinds), q.Name, func(s engine.Symbol) string { return s.Name })
	res := &ScanResult{
		Language: displayLang(q.Languages),
		Files:    limitFiles(files, q.Limit),
	}
	if len(errs) > 0 {
		res.Errors = errs
	}
	return res, nil
}

// pruneGroups drops groups not listed in kinds (when set) and items whose
// nameOf() differs from name (when set), removing files left empty. Generic:
// it works for any grouped value, not just symbols.
func pruneGroups[V any](files map[string]map[string][]V, kinds map[string]bool, name string, nameOf func(V) string) {
	for path, groups := range files {
		for kind, items := range groups {
			if len(kinds) > 0 && !kinds[kind] {
				delete(groups, kind)
				continue
			}
			switch {
			case name != "":
				kept := items[:0]
				for _, it := range items {
					if nameOf(it) == name {
						kept = append(kept, it)
					}
				}
				if len(kept) == 0 {
					delete(groups, kind)
					continue
				}
				groups[kind] = kept
			case len(items) == 0:
				delete(groups, kind)
			}
		}
		if len(groups) == 0 {
			delete(files, path)
		}
	}
}

// kindSet normalizes requested kinds (case-insensitive); nil means no filter.
func kindSet(kinds []string) map[string]bool {
	if len(kinds) == 0 {
		return nil
	}
	set := make(map[string]bool, len(kinds))
	for _, k := range kinds {
		set[strings.ToLower(strings.TrimSpace(k))] = true
	}
	return set
}
