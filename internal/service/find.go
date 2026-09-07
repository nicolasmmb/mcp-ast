package service

import (
	"context"
	"sort"
	"strings"

	"mcp-ast/internal/engine"
	"mcp-ast/internal/lang"
)

type FindMode string

const (
	FindOccurrences FindMode = "occurrences"
	FindCallers     FindMode = "callers"
	FindUnused      FindMode = "unused"
	FindDefinitions FindMode = "definitions"
	FindImports     FindMode = "imports"
)

type FindQuery struct {
	Mode        FindMode
	Name        string
	Dir         string
	Languages   []string
	Kinds       []string
	GroupByFile bool
	Limit       int
}

type FindResult struct {
	Language string                         `json:"language"`
	Mode     string                         `json:"mode"`
	Kinds    []string                       `json:"kinds,omitempty"`
	Matches  []engine.UsageMatch            `json:"matches,omitempty"`
	Files    map[string][]engine.UsageMatch `json:"files,omitempty"`
	Callers  []engine.Caller                `json:"callers,omitempty"`
	Symbols  []engine.SearchMatch           `json:"symbols,omitempty"`
	Errors   map[string]string              `json:"errors,omitempty"`
}

type FindService struct{ eng *engine.Engine }

func (s *FindService) Dir(ctx context.Context, q FindQuery) (*FindResult, error) {
	fs, err := filters(s.eng, q.Languages, q.Dir)
	if err != nil {
		return nil, err
	}
	if q.Mode == "" {
		q.Mode = FindOccurrences
	}
	res := &FindResult{Language: displayLang(q.Languages), Mode: string(q.Mode)}
	errs := map[string]string{}

	switch q.Mode {
	case FindOccurrences:
		matches := make([]engine.UsageMatch, 0)
		kinds := usageKindSet(q.Kinds)
		for _, f := range fs {
			ms, es, err := s.eng.Usages(ctx, q.Dir, q.Name, f, 0)
			if err != nil {
				return nil, err
			}
			matches = append(matches, filterUsageKinds(ms, kinds)...)
			for p, e := range es {
				errs[p] = e
			}
			if len(kinds) == 0 || kinds["import"] {
				im, ies, err := s.collectDefinitionMatches(ctx, q.Dir, q.Name, f, true)
				if err != nil {
					return nil, err
				}
				matches = append(matches, im...)
				for p, e := range ies {
					errs[p] = e
				}
			}
		}
		sortUsageMatches(matches)
		if q.Limit > 0 && len(matches) > q.Limit {
			matches = matches[:q.Limit]
		}
		res.Matches = matches
		res.Kinds = usageKindsList(kinds)
		if q.GroupByFile {
			res.Files = groupUsageByFile(matches)
		}
	case FindCallers:
		all := make([]engine.Caller, 0)
		for _, f := range fs {
			cs, es, err := s.eng.Callers(ctx, q.Dir, q.Name, f, 0)
			if err != nil {
				return nil, err
			}
			all = append(all, cs...)
			for p, e := range es {
				errs[p] = e
			}
		}
		sort.Slice(all, func(i, j int) bool {
			if all[i].Count != all[j].Count {
				return all[i].Count > all[j].Count
			}
			if all[i].File != all[j].File {
				return all[i].File < all[j].File
			}
			return all[i].Name < all[j].Name
		})
		if q.Limit > 0 && len(all) > q.Limit {
			all = all[:q.Limit]
		}
		res.Callers = all
	case FindUnused:
		all := make([]engine.SearchMatch, 0)
		for _, f := range fs {
			r, err := s.eng.UnusedSymbols(ctx, q.Dir, f, 0)
			if err != nil {
				return nil, err
			}
			all = append(all, r.Matches...)
			for p, e := range r.Errors {
				errs[p] = e
			}
		}
		sort.Slice(all, func(i, j int) bool {
			if all[i].File != all[j].File {
				return all[i].File < all[j].File
			}
			if all[i].Line != all[j].Line {
				return all[i].Line < all[j].Line
			}
			return all[i].Name < all[j].Name
		})
		if q.Limit > 0 && len(all) > q.Limit {
			all = all[:q.Limit]
		}
		res.Symbols = all
	case FindDefinitions:
		matches, localErrs, err := s.findDefinitionsByFilters(ctx, q, fs, false)
		if err != nil {
			return nil, err
		}
		mergeErrors(errs, localErrs)
		res.Matches = matches
		if q.GroupByFile {
			res.Files = groupUsageByFile(matches)
		}
	case FindImports:
		matches, localErrs, err := s.findDefinitionsByFilters(ctx, q, fs, true)
		if err != nil {
			return nil, err
		}
		mergeErrors(errs, localErrs)
		res.Matches = matches
		if q.GroupByFile {
			res.Files = groupUsageByFile(matches)
		}
	default:
		return nil, ErrInvalidMode{Mode: string(q.Mode)}
	}
	if len(errs) > 0 {
		res.Errors = errs
	}
	return res, nil
}

type ErrInvalidMode struct{ Mode string }

func (e ErrInvalidMode) Error() string {
	return "invalid find_usages mode: " + e.Mode
}

func (s *FindService) findDefinitionsByFilters(ctx context.Context, q FindQuery, fs []lang.Language, importsOnly bool) ([]engine.UsageMatch, map[string]string, error) {
	matches := make([]engine.UsageMatch, 0)
	err := map[string]string{}
	for _, f := range fs {
		ms, es, e := s.collectDefinitionMatches(ctx, q.Dir, q.Name, f, importsOnly)
		if e != nil {
			return nil, nil, e
		}
		matches = append(matches, ms...)
		mergeErrors(err, es)
	}
	sortUsageMatches(matches)
	if q.Limit > 0 && len(matches) > q.Limit {
		matches = matches[:q.Limit]
	}
	return matches, err, nil
}

func usageKindSet(kinds []string) map[string]bool {
	if len(kinds) == 0 {
		return nil
	}
	set := make(map[string]bool, len(kinds))
	for _, k := range kinds {
		v := strings.ToLower(strings.TrimSpace(k))
		if v == "" {
			continue
		}
		set[v] = true
	}
	return set
}

func usageKindsList(set map[string]bool) []string {
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func filterUsageKinds(in []engine.UsageMatch, kinds map[string]bool) []engine.UsageMatch {
	if len(kinds) == 0 {
		return in
	}
	out := make([]engine.UsageMatch, 0, len(in))
	for _, m := range in {
		if kinds[m.Kind] {
			out = append(out, m)
		}
	}
	return out
}

func sortUsageMatches(matches []engine.UsageMatch) {
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].File != matches[j].File {
			return matches[i].File < matches[j].File
		}
		if matches[i].Line != matches[j].Line {
			return matches[i].Line < matches[j].Line
		}
		if matches[i].Col != matches[j].Col {
			return matches[i].Col < matches[j].Col
		}
		return matches[i].Kind < matches[j].Kind
	})
}

func groupUsageByFile(matches []engine.UsageMatch) map[string][]engine.UsageMatch {
	if len(matches) == 0 {
		return map[string][]engine.UsageMatch{}
	}
	out := make(map[string][]engine.UsageMatch)
	for _, m := range matches {
		out[m.File] = append(out[m.File], m)
	}
	return out
}

func mergeErrors(dst, src map[string]string) {
	for p, e := range src {
		dst[p] = e
	}
}

func (s *FindService) collectDefinitionMatches(ctx context.Context, dir, name string, filter lang.Language, importsOnly bool) ([]engine.UsageMatch, map[string]string, error) {
	files, errs, err := s.eng.ScanSymbols(ctx, dir, filter, false)
	if err != nil {
		return nil, nil, err
	}
	matches := make([]engine.UsageMatch, 0)
	for path, groups := range files {
		for kind, syms := range groups {
			isImport := kind == "imports"
			if importsOnly && !isImport {
				continue
			}
			if !importsOnly && isImport {
				continue
			}
			for _, sym := range syms {
				if name != "" && sym.Name != name && !strings.Contains(sym.Text, name) {
					continue
				}
				mk := "definition"
				if isImport {
					mk = "import"
				}
				matches = append(matches, engine.UsageMatch{File: path, Line: sym.Start.Row + 1, Col: sym.Start.Col, Text: sym.Text, Kind: mk})
			}
		}
	}
	return matches, errs, nil
}
