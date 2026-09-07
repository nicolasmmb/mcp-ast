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
	Source   string                         `json:"source,omitempty"` // "indexed_heuristic" for repo unused
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

	handlers := map[FindMode]func() error{
		FindOccurrences: func() error {
			matches, kinds, localErrs, err := s.findOccurrences(ctx, q, fs)
			if err != nil {
				return err
			}
			mergeErrors(errs, localErrs)
			res.Matches = matches
			res.Kinds = kinds
			if q.GroupByFile {
				res.Files = groupUsageByFile(matches)
			}
			return nil
		},
		FindCallers: func() error {
			callers, localErrs, err := s.findCallers(ctx, q, fs)
			if err != nil {
				return err
			}
			mergeErrors(errs, localErrs)
			res.Callers = callers
			return nil
		},
		FindUnused: func() error {
			symbols, localErrs, err := s.findUnused(ctx, q, fs)
			if err != nil {
				return err
			}
			mergeErrors(errs, localErrs)
			res.Symbols = symbols
			return nil
		},
		FindDefinitions: func() error {
			matches, localErrs, err := s.findDefinitionsByFilters(ctx, q, fs, false)
			if err != nil {
				return err
			}
			mergeErrors(errs, localErrs)
			res.Matches = matches
			if q.GroupByFile {
				res.Files = groupUsageByFile(matches)
			}
			return nil
		},
		FindImports: func() error {
			matches, localErrs, err := s.findDefinitionsByFilters(ctx, q, fs, true)
			if err != nil {
				return err
			}
			mergeErrors(errs, localErrs)
			res.Matches = matches
			if q.GroupByFile {
				res.Files = groupUsageByFile(matches)
			}
			return nil
		},
	}
	handle, ok := handlers[q.Mode]
	if !ok {
		return nil, ErrInvalidMode{Mode: string(q.Mode)}
	}
	if err := handle(); err != nil {
		return nil, err
	}
	if len(errs) > 0 {
		res.Errors = errs
	}
	return res, nil
}

func (s *FindService) findOccurrences(ctx context.Context, q FindQuery, fs []lang.Language) ([]engine.UsageMatch, []string, map[string]string, error) {
	matches := make([]engine.UsageMatch, 0)
	errs := map[string]string{}
	kinds := usageKindSet(q.Kinds)
	for _, f := range fs {
		ms, es, err := s.eng.Usages(ctx, q.Dir, q.Name, f, 0)
		if err != nil {
			return nil, nil, nil, err
		}
		matches = append(matches, filterUsageKinds(ms, kinds)...)
		mergeErrors(errs, es)
		if len(kinds) == 0 || kinds["import"] {
			imports, importErrs, err := s.collectDefinitionMatches(ctx, q.Dir, q.Name, f, true)
			if err != nil {
				return nil, nil, nil, err
			}
			matches = append(matches, imports...)
			mergeErrors(errs, importErrs)
		}
	}
	sortUsageMatches(matches)
	if q.Limit > 0 && len(matches) > q.Limit {
		matches = matches[:q.Limit]
	}
	return matches, usageKindsList(kinds), errs, nil
}

func (s *FindService) findCallers(ctx context.Context, q FindQuery, fs []lang.Language) ([]engine.Caller, map[string]string, error) {
	callers := make([]engine.Caller, 0)
	errs := map[string]string{}
	for _, f := range fs {
		matches, localErrs, err := s.eng.Callers(ctx, q.Dir, q.Name, f, 0)
		if err != nil {
			return nil, nil, err
		}
		callers = append(callers, matches...)
		mergeErrors(errs, localErrs)
	}
	sort.Slice(callers, func(i, j int) bool {
		if callers[i].Count != callers[j].Count {
			return callers[i].Count > callers[j].Count
		}
		if callers[i].File != callers[j].File {
			return callers[i].File < callers[j].File
		}
		return callers[i].Name < callers[j].Name
	})
	if q.Limit > 0 && len(callers) > q.Limit {
		callers = callers[:q.Limit]
	}
	return callers, errs, nil
}

func (s *FindService) findUnused(ctx context.Context, q FindQuery, fs []lang.Language) ([]engine.SearchMatch, map[string]string, error) {
	symbols := make([]engine.SearchMatch, 0)
	errs := map[string]string{}
	for _, f := range fs {
		result, err := s.eng.UnusedSymbols(ctx, q.Dir, f, 0)
		if err != nil {
			return nil, nil, err
		}
		symbols = append(symbols, result.Matches...)
		mergeErrors(errs, result.Errors)
	}
	sort.Slice(symbols, func(i, j int) bool {
		if symbols[i].File != symbols[j].File {
			return symbols[i].File < symbols[j].File
		}
		if symbols[i].Line != symbols[j].Line {
			return symbols[i].Line < symbols[j].Line
		}
		return symbols[i].Name < symbols[j].Name
	})
	if q.Limit > 0 && len(symbols) > q.Limit {
		symbols = symbols[:q.Limit]
	}
	return symbols, errs, nil
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
		matches = append(matches, matchSymbols(path, groups, name, importsOnly)...)
	}
	return matches, errs, nil
}

func matchSymbols(path string, groups map[string][]engine.Symbol, name string, importsOnly bool) []engine.UsageMatch {
	matches := make([]engine.UsageMatch, 0)
	for kind, symbols := range groups {
		isImport := kind == "imports"
		if importsOnly != isImport {
			continue
		}
		for _, symbol := range symbols {
			if name != "" && symbol.Name != name && !strings.Contains(symbol.Text, name) {
				continue
			}
			matchKind := "definition"
			if isImport {
				matchKind = "import"
			}
			matches = append(matches, engine.UsageMatch{
				File: path, Line: symbol.Start.Row + 1, Col: symbol.Start.Col,
				Text: symbol.Text, Kind: matchKind,
			})
		}
	}
	return matches
}
