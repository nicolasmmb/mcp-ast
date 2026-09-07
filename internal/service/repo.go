package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"mcp-ast/internal/engine"
	"mcp-ast/internal/lang"
	"mcp-ast/internal/repoindex"
)

// errUnstable marks files that keep changing while being indexed.
var errUnstable = errors.New("file changed while being indexed")

type RepoService struct {
	eng          *engine.Engine
	store        repoindex.Store
	refreshLocks sync.Map // repo id -> *sync.Mutex (refresh coalescing)
}

func (s *RepoService) refreshLock(id string) *sync.Mutex {
	mu, _ := s.refreshLocks.LoadOrStore(id, &sync.Mutex{})
	return mu.(*sync.Mutex)
}

func (s *RepoService) Index(ctx context.Context, dir string, languages []string) (repoindex.Info, error) {
	root, err := absOrErr(dir)
	if err != nil {
		return repoindex.Info{}, err
	}
	filters, err := filters(s.eng, languages, root)
	if err != nil {
		return repoindex.Info{}, err
	}
	info := s.store.Create(root)
	go s.build(context.WithoutCancel(ctx), info.ID, root, filters)
	return info, nil
}

func (s *RepoService) Drop(id string) error {
	if !s.store.Drop(id) {
		return fmt.Errorf("unknown repository %q", id)
	}
	return nil
}

func (s *RepoService) Status(id string) (repoindex.Info, error) {
	info, ok := s.store.Info(id)
	if !ok {
		return repoindex.Info{}, fmt.Errorf("unknown repository %q", id)
	}
	return info, nil
}

// build indexes every recognized file under root and replaces the snapshot.
func (s *RepoService) build(ctx context.Context, id, root string, filters []lang.Language) {
	files := make(map[string]repoindex.IndexedFile)
	errs := make(map[string]string)
	for _, f := range filters {
		facts, fileErrs, err := s.eng.IndexDir(ctx, root, f)
		if err != nil {
			errs[root] = err.Error()
			break
		}
		for p, fact := range facts {
			indexed := s.loadIndexed(p, fact, errs)
			if indexed.Facts != nil {
				files[p] = indexed
			}
		}
		for p, e := range fileErrs {
			errs[p] = e
		}
	}
	_, _ = s.store.Replace(id, files, errs)
}

// loadIndexed attaches size, mtime and sha256 to a file's facts. Unreadable
// files yield a zero IndexedFile with an error recorded.
func (s *RepoService) loadIndexed(path string, facts *engine.FileIndex, errs map[string]string) repoindex.IndexedFile {
	st, err := os.Stat(path)
	if err != nil {
		errs[path] = err.Error()
		return repoindex.IndexedFile{}
	}
	digest, err := fileDigest(path)
	if err != nil {
		errs[path] = err.Error()
		return repoindex.IndexedFile{}
	}
	return repoindex.IndexedFile{Facts: facts, Size: st.Size(), ModTime: st.ModTime().UnixNano(), Digest: digest}
}

// Refresh recomputes the delta between the indexed snapshot and the filesystem.
// Small deltas apply incrementally; large ones trigger a full background
// rebuild. A second concurrent refresh for the same repo is coalesced: it
// returns the current state without starting another pass.
func (s *RepoService) Refresh(ctx context.Context, id string, languages []string) (repoindex.Info, error) {
	info, ok := s.store.Info(id)
	if !ok {
		return repoindex.Info{}, fmt.Errorf("unknown repository %q", id)
	}
	if len(info.Root) == 0 {
		return repoindex.Info{}, fmt.Errorf("repository %q has no root", id)
	}
	lock := s.refreshLock(id)
	if !lock.TryLock() {
		info.State = "refreshing"
		return info, nil
	}
	defer lock.Unlock()
	filters, err := filters(s.eng, languages, info.Root)
	if err != nil {
		return repoindex.Info{}, err
	}
	meta, ok := s.store.Meta(id)
	if !ok {
		return repoindex.Info{}, fmt.Errorf("repository %q state lost", id)
	}
	changed, added, deleted, err := s.diff(ctx, info.Root, filters, meta)
	if err != nil {
		return repoindex.Info{}, err
	}
	threshold := maxInt(500, len(meta)/5)
	if len(added)+len(changed)+len(deleted) > threshold {
		info, err = s.store.SetState(id, "refreshing")
		if err != nil {
			return repoindex.Info{}, err
		}
		go s.build(context.WithoutCancel(ctx), id, info.Root, filters)
		return info, nil
	}
	if len(added) == 0 && len(changed) == 0 && len(deleted) == 0 {
		return info, nil
	}
	cs := repoindex.ChangeSet{
		Added:   map[string]repoindex.IndexedFile{},
		Updated: map[string]repoindex.IndexedFile{},
		Deleted: deleted,
		Errors:  map[string]string{},
	}
	for _, p := range added {
		indexed, err := s.indexPath(p, meta)
		switch {
		case errors.Is(err, errUnstable):
			cs.Errors[p] = err.Error()
		case err != nil:
			cs.Deleted = append(cs.Deleted, p)
			cs.Errors[p] = err.Error()
		case indexed.Facts != nil:
			cs.Added[p] = indexed
		}
	}
	for _, p := range changed {
		indexed, err := s.indexPath(p, meta)
		switch {
		case errors.Is(err, errUnstable):
			cs.Errors[p] = err.Error()
		case err != nil:
			cs.Deleted = append(cs.Deleted, p)
			cs.Errors[p] = err.Error()
		case indexed.Facts != nil:
			cs.Updated[p] = indexed
		}
	}
	return s.store.Apply(id, cs)
}

// indexPath parses a single file into indexed facts with fresh metadata. A
// changed file whose digest equals the stored one is reported as unchanged.
// The file is re-statted after parsing: if it changed during the read it is
// retried once, then marked unstable.
func (s *RepoService) indexPath(p string, meta map[string]repoindex.IndexedFile) (repoindex.IndexedFile, error) {
	for attempt := 0; attempt < 2; attempt++ {
		st, err := os.Stat(p)
		if err != nil {
			return repoindex.IndexedFile{}, err
		}
		digest, err := fileDigest(p)
		if err != nil {
			return repoindex.IndexedFile{}, err
		}
		if prev, ok := meta[p]; ok && prev.Size == st.Size() && prev.Digest == digest {
			return repoindex.IndexedFile{}, nil
		}
		l, err := s.eng.Resolve("", p)
		if err != nil {
			return repoindex.IndexedFile{}, err
		}
		facts, err := s.eng.IndexFile(l, p)
		if err != nil {
			return repoindex.IndexedFile{}, err
		}
		after, err := os.Stat(p)
		if err != nil {
			return repoindex.IndexedFile{}, err
		}
		if after.Size() == st.Size() && after.ModTime().UnixNano() == st.ModTime().UnixNano() {
			return repoindex.IndexedFile{Facts: facts, Size: st.Size(), ModTime: st.ModTime().UnixNano(), Digest: digest}, nil
		}
	}
	return repoindex.IndexedFile{}, errUnstable
}

// diff walks the filesystem once and classifies every path against meta.
func (s *RepoService) diff(ctx context.Context, root string, filters []lang.Language, meta map[string]repoindex.IndexedFile) (changed, added, deleted []string, err error) {
	seen := map[string]bool{}
	var all []string
	for _, f := range filters {
		paths, err := s.eng.ListFiles(ctx, root, f)
		if err != nil {
			return nil, nil, nil, err
		}
		for _, p := range paths {
			if seen[p] {
				continue
			}
			seen[p] = true
			all = append(all, p)
		}
	}
	sort.Strings(all)
	for p := range meta {
		if !seen[p] {
			deleted = append(deleted, p)
		}
	}
	for _, p := range all {
		prev, ok := meta[p]
		if !ok {
			added = append(added, p)
			continue
		}
		st, err := os.Stat(p)
		if err != nil {
			added = append(added, p)
			continue
		}
		if st.Size() == prev.Size && st.ModTime().UnixNano() == prev.ModTime {
			continue
		}
		changed = append(changed, p)
	}
	return changed, added, deleted, nil
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func absOrErr(dir string) (string, error) {
	if dir == "" {
		return "", fmt.Errorf("path is required")
	}
	st, err := os.Stat(dir)
	if err != nil {
		return "", err
	}
	if !st.IsDir() {
		return "", fmt.Errorf("path %q is not a directory", dir)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	return abs, nil
}

func fileDigest(p string) ([32]byte, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(data), nil
}

// --- graph queries ---------------------------------------------------------

// UsagePage is one page of indexed usages with a cursor for the next page.
type UsagePage struct {
	Matches    []engine.UsageMatch `json:"matches"`
	NextCursor string              `json:"next_cursor,omitempty"`
	Truncated  bool                `json:"truncated,omitempty"`
}

// UsagePage returns one page of usages for name (or a canonical key
// containing '|'), ordered by file then line. limit is the page size.
func (s *RepoService) UsagePage(id, name, cursor string, limit int) (*UsagePage, error) {
	info, ok := s.store.Info(id)
	if !ok {
		return nil, fmt.Errorf("unknown repository %q", id)
	}
	c, err := repoindex.CheckCursor(cursor, info.Version)
	if err != nil {
		return nil, err
	}
	matches, ok := s.store.Usages(id, name)
	if !ok {
		return nil, fmt.Errorf("unknown repository %q", id)
	}
	if limit <= 0 {
		limit = 500
	}
	page := &UsagePage{}
	if c.Offset > len(matches) {
		c.Offset = len(matches)
	}
	end := c.Offset + limit
	if end > len(matches) {
		end = len(matches)
	} else {
		page.Truncated = true
		page.NextCursor = repoindex.EncodeCursor(info.Version, end)
	}
	page.Matches = append([]engine.UsageMatch(nil), matches[c.Offset:end]...)
	return page, nil
}

func (s *RepoService) Impact(id, graph, target string, reverse bool, depth, limit int, cursor string) (repoindex.ImpactResult, error) {
	info, ok := s.store.Info(id)
	if !ok {
		return repoindex.ImpactResult{}, fmt.Errorf("unknown repository %q", id)
	}
	c, err := repoindex.CheckCursor(cursor, info.Version)
	if err != nil {
		return repoindex.ImpactResult{}, err
	}
	adj, nodes, err := s.graphData(id, graph, reverse)
	if err != nil {
		return repoindex.ImpactResult{}, err
	}
	if !nodes[target] {
		return repoindex.ImpactResult{}, fmt.Errorf("target %q not found in %s graph", target, graph)
	}
	if limit <= 0 {
		limit = 512
	}
	res := repoindex.Impact(adj, target, depth, limit, c.Offset)
	if res.Truncated {
		res.NextCursor = repoindex.EncodeCursor(info.Version, c.Offset+limit)
	}
	if graph == "calls" {
		if g, ok := s.store.Calls(id); ok {
			res.ResolutionCounts = g.ResolutionCounts()
		}
	}
	return res, nil
}

func (s *RepoService) Cycles(id, graph string) ([][]string, error) {
	adj, nodes, err := s.graphData(id, graph, false)
	if err != nil {
		return nil, err
	}
	return repoindex.Cycles(nodes, adj), nil
}

func (s *RepoService) Topology(id, graph string) ([][]string, error) {
	adj, nodes, err := s.graphData(id, graph, false)
	if err != nil {
		return nil, err
	}
	return repoindex.Topology(nodes, adj), nil
}

func (s *RepoService) graphData(id, graph string, reverse bool) (map[string][]string, map[string]bool, error) {
	switch graph {
	case "calls":
		g, ok := s.store.Calls(id)
		if !ok {
			return nil, nil, fmt.Errorf("unknown repository %q", id)
		}
		return g.Adjacency(reverse), g.Nodes(), nil
	case "imports":
		g, ok := s.store.Imports(id)
		if !ok {
			return nil, nil, fmt.Errorf("unknown repository %q", id)
		}
		return g.Adjacency(reverse), g.Nodes(), nil
	default:
		return nil, nil, fmt.Errorf("invalid graph %q (want calls|imports)", graph)
	}
}

// freshFacts returns the indexed facts for path, reindexing the file when its
// digest no longer matches the stored one. A nil result with nil error means
// the path is not part of the index (fallback to direct parse is safe).
func (s *RepoService) freshFacts(id, path string) (*engine.FileIndex, error) {
	meta, ok := s.store.Meta(id)
	if !ok {
		return nil, fmt.Errorf("unknown repository %q", id)
	}
	f, ok := meta[path]
	if !ok {
		return nil, nil
	}
	digest, err := fileDigest(path)
	if err != nil {
		return nil, nil
	}
	if digest == f.Digest {
		return f.Facts, nil
	}
	indexed, err := s.indexPath(path, meta)
	if err != nil {
		return nil, nil
	}
	if indexed.Facts == nil {
		return nil, nil
	}
	if _, err := s.store.Apply(id, repoindex.ChangeSet{Updated: map[string]repoindex.IndexedFile{path: indexed}}); err != nil {
		return nil, err
	}
	return indexed.Facts, nil
}

// Outline serves a file outline from indexed symbols when full text is not
// needed; otherwise it re-parses that single file (AST fallback).
func (s *RepoService) Outline(id, path string, includeText bool) (*OutlineResult, error) {
	facts, err := s.freshFacts(id, path)
	if err != nil {
		return nil, err
	}
	if facts != nil && !includeText && facts.Capabilities.Has(engine.IndexedOutline) {
		return &OutlineResult{Language: facts.Language, Path: path, Outline: engine.OutlineFromSymbols(facts.Symbols), Source: "indexed"}, nil
	}
	l, err := s.eng.Resolve("", path)
	if err != nil {
		return nil, err
	}
	nodes, err := s.eng.Outline(l, path, includeText)
	if err != nil {
		return nil, err
	}
	return &OutlineResult{Language: l.Name(), Path: path, Outline: nodes, Source: "ast_fallback"}, nil
}

// Analyze serves the file dossier. Metrics and call graph are not indexed
// (ponytail: keep the index lean; add indexed metrics if analyze becomes a
// hot path), so the dossier always re-parses that single file, after
// reindexing it when stale.
func (s *RepoService) Analyze(id, path string) (*engine.FileReport, error) {
	if _, err := s.freshFacts(id, path); err != nil {
		return nil, err
	}
	l, err := s.eng.Resolve("", path)
	if err != nil {
		return nil, err
	}
	report, err := s.eng.Dossier(l, path)
	if err != nil {
		return nil, err
	}
	report.Source = "ast_fallback"
	return report, nil
}

// Usages queries indexed usages for a symbol.
func (s *RepoService) Usages(id, name string) ([]engine.UsageMatch, error) {
	matches, ok := s.store.Usages(id, name)
	if !ok {
		return nil, fmt.Errorf("unknown repository %q", id)
	}
	return matches, nil
}

// Unused returns symbols declared but never referenced, from AST occurrences
// in the index. The result is heuristic (no scope resolution).
func (s *RepoService) Unused(id string, limit int) (*engine.SearchResult, error) {
	matches, ok := s.store.Unused(id)
	if !ok {
		return nil, fmt.Errorf("unknown repository %q", id)
	}
	if limit > 0 && len(matches) > limit {
		matches = matches[:limit]
	}
	return &engine.SearchResult{Total: len(matches), Matches: matches}, nil
}

func (s *RepoService) Complexity(id string, limit int) ([]engine.RankedComplexity, error) {
	entries, ok := s.store.Complexity(id, limit)
	if !ok {
		return nil, fmt.Errorf("unknown repository %q", id)
	}
	return entries, nil
}

// Scan filters indexed symbols by language, kind, exact name and limit.
func (s *RepoService) Scan(id string, languages, kinds []string, name string, limit int) (*ScanResult, error) {
	files, ok := s.store.Files(id)
	if !ok {
		return nil, fmt.Errorf("unknown repository %q", id)
	}
	langs := map[string]bool{}
	for _, l := range languages {
		langs[l] = true
	}
	out := map[string]map[string][]engine.Symbol{}
	for path, facts := range files {
		if len(langs) > 0 && !langs[facts.Language] {
			continue
		}
		out[path] = facts.Symbols
	}
	pruneGroups(out, kindSet(kinds), name, func(se engine.Symbol) string { return se.Name })
	res := &ScanResult{Language: displayLang(languages), Files: limitFiles(out, limit)}
	return res, nil
}

// FindUsages queries indexed usages for a symbol with the same filters as the
// direct FindService flow (kinds, group_by_file, limit).
func (s *RepoService) FindUsages(id, name string, q FindQuery) (*FindResult, error) {
	matches, ok := s.store.Usages(id, name)
	if !ok {
		return nil, fmt.Errorf("unknown repository %q", id)
	}
	kinds := usageKindSet(q.Kinds)
	matches = filterUsageKinds(matches, kinds)
	sortUsageMatches(matches)
	if q.Limit > 0 && len(matches) > q.Limit {
		matches = matches[:q.Limit]
	}
	res := &FindResult{Language: "indexed", Mode: string(q.Mode), Matches: matches, Kinds: usageKindsList(kinds)}
	if q.GroupByFile {
		res.Files = GroupUsageByFile(matches)
	}
	return res, nil
}
