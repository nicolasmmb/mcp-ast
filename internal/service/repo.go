package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"mcp-ast/internal/engine"
	"mcp-ast/internal/lang"
	"mcp-ast/internal/repoindex"
)

// errUnstable marks files that keep changing while being indexed.
var errUnstable = errors.New("file changed while being indexed")

type RepoService struct {
	eng           *engine.Engine
	store         repoindex.Store
	logger        *slog.Logger
	roots         sync.Map // abs root -> repo id (index-first path resolution)
	refreshLocks  sync.Map // repo id -> *sync.Mutex (refresh coalescing)
	watchLangs    sync.Map // repo id -> languages used at index time
	snapshotLangs sync.Map // repo id -> languages (snapshot header)
	watchInterval time.Duration
	toolVersion   string
	cacheDir      string
	snapshotMu    sync.Mutex
}

// SetLogger routes index operation logs to the server logger (stderr/-log file).
func (s *RepoService) SetLogger(l *slog.Logger) { s.logger = l }

// log returns the service logger, falling back to the slog default for
// direct constructions (tests).
func (s *RepoService) log() *slog.Logger {
	if s.logger != nil {
		return s.logger
	}
	return slog.Default()
}

// SetToolVersion enables snapshot persistence: snapshots are only saved and
// restored when the tool version is known (non-empty).
func (s *RepoService) SetToolVersion(v string) { s.toolVersion = v }

// SetCacheDir overrides the snapshot directory (default: user cache).
func (s *RepoService) SetCacheDir(dir string) { s.cacheDir = dir }

func (s *RepoService) snapshotPath(root string) string {
	dir := s.cacheDir
	if dir == "" {
		if base, err := os.UserCacheDir(); err == nil {
			dir = filepath.Join(base, "ast-mcp")
		} else {
			dir = filepath.Join(os.TempDir(), "ast-mcp-cache")
		}
	}
	sum := sha256.Sum256([]byte(root))
	return filepath.Join(dir, "repo-"+hex.EncodeToString(sum[:8])+".gob")
}

// SetWatchInterval enables the polling watcher when interval > 0. Watching
// uses the same incremental Refresh path: a tick with no delta is a no-op, a
// small delta applies incrementally, a mass change triggers the full rebuild
// threshold. ponytail: polling, not fsnotify; switch to fsnotify if
// event-driven latency becomes a requirement.
func (s *RepoService) SetWatchInterval(interval time.Duration) {
	s.watchInterval = interval
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
	if old, ok := s.roots.Load(root); ok {
		s.log().Info(fmt.Sprintf("replaced previous index for %s", root), "root", root)
		_ = s.drop(old.(string))
	}
	info := s.store.Create(root)
	s.roots.Store(root, info.ID)
	path := s.snapshotPath(root)
	langsKey := strings.Join(languages, ",")
	s.snapshotLangs.Store(info.ID, languages)
	if s.toolVersion != "" {
		start := time.Now()
		if snap, err := repoindex.LoadSnapshot(path); err != nil {
			if os.IsNotExist(err) {
				s.log().Debug(fmt.Sprintf("no snapshot for %s: full index build", root), "root", root)
			} else {
				s.log().Warn(fmt.Sprintf("snapshot corrupt, full index build (reason: %s)", err), "root", root)
			}
		} else if !snap.Header.Valid(repoindex.SnapshotSchemaVersion, s.toolVersion, root, langsKey) {
			s.log().Warn("snapshot expired, full index build (reason: header mismatch — schema, version, root or languages changed)", "root", root)
		} else if _, err := s.store.Replace(info.ID, snap.Files, nil); err != nil {
			s.log().Warn(fmt.Sprintf("snapshot corrupt, full index build (reason: %s)", err), "root", root)
		} else {
			info, _ = s.store.SetCache(info.ID, true, path)
			if s.watchInterval > 0 {
				_, _ = s.store.SetWatch(info.ID, true)
				s.startWatch(info.ID, languages)
			}
			s.log().Info(fmt.Sprintf("index restored from snapshot in %s: %d %s; snapshot %s",
				humanDur(time.Since(start)), len(snap.Files), plural(len(snap.Files), "file", "files"), humanBytes(s.snapshotSize(root))),
				"root", root)
			go func() { _, _ = s.refresh(context.Background(), info.ID, languages) }()
			return info, nil
		}
	}
	go s.build(context.WithoutCancel(ctx), info.ID, root, filters)
	if s.watchInterval > 0 {
		_, _ = s.store.SetWatch(info.ID, true)
		s.startWatch(info.ID, languages)
	}
	return info, nil
}

// startWatch polls the repository every watchInterval and applies the
// incremental refresh. The loop exits when the repo is dropped.
func (s *RepoService) startWatch(id string, languages []string) {
	s.watchLangs.Store(id, languages)
	go func() {
		ticker := time.NewTicker(s.watchInterval)
		defer ticker.Stop()
		for range ticker.C {
			if _, ok := s.store.Info(id); !ok {
				return
			}
			langs, _ := s.watchLangs.Load(id)
			names, _ := langs.([]string)
			_, _ = s.refresh(context.Background(), id, names)
		}
	}()
}

func (s *RepoService) drop(id string) error {
	if !s.store.Drop(id) {
		return fmt.Errorf("unknown repository %q", id)
	}
	s.roots.Range(func(k, v any) bool {
		if v == id {
			s.roots.Delete(k)
			return false
		}
		return true
	})
	s.watchLangs.Delete(id)
	s.snapshotLangs.Delete(id)
	s.refreshLocks.Delete(id)
	return nil
}

// List returns the current info of every registered repository.
func (s *RepoService) List() []repoindex.Info {
	var out []repoindex.Info
	s.roots.Range(func(_, v any) bool {
		if info, ok := s.store.Info(v.(string)); ok {
			out = append(out, info)
		}
		return true
	})
	return out
}

// ResolveIndex maps a file or directory path to the ready index whose root
// is its longest prefix. Returns ok=false when no configured root covers
// the path or the covering index is not ready yet.
func (s *RepoService) ResolveIndex(path string) (repoindex.Info, bool) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return repoindex.Info{}, false
	}
	abs = filepath.Clean(abs)
	bestRoot := ""
	bestID := ""
	s.roots.Range(func(k, v any) bool {
		root := k.(string)
		if abs == root || strings.HasPrefix(abs, root+string(os.PathSeparator)) {
			if len(root) > len(bestRoot) {
				bestRoot, bestID = root, v.(string)
			}
		}
		return true
	})
	if bestID == "" {
		return repoindex.Info{}, false
	}
	info, ok := s.store.Info(bestID)
	if !ok || info.State != "ready" {
		return repoindex.Info{}, false
	}
	return info, true
}

// ---------------------------------------------------------------------------
// Path-based API. Every method resolves the path to a ready index; ok=false
// means "not covered by any index" and the caller should use the disk flow.
// ---------------------------------------------------------------------------

func (s *RepoService) ScanAt(path string, languages, kinds []string, name string, limit int) (*ScanResult, bool, error) {
	info, ok := s.ResolveIndex(path)
	if !ok {
		return nil, false, nil
	}
	res, err := s.scan(info.ID, languages, kinds, name, limit)
	return res, err == nil, err
}

func (s *RepoService) FindOccurrencesAt(path, name, cursor string, limit int) (*UsagePage, bool, error) {
	info, ok := s.ResolveIndex(path)
	if !ok {
		return nil, false, nil
	}
	page, err := s.usagePage(info.ID, name, cursor, limit)
	return page, err == nil, err
}

func (s *RepoService) UnusedAt(path string, limit int) (*engine.SearchResult, bool, error) {
	info, ok := s.ResolveIndex(path)
	if !ok {
		return nil, false, nil
	}
	res, err := s.unused(info.ID, limit)
	return res, err == nil, err
}

func (s *RepoService) CallersAt(path, name string, limit int) ([]engine.Caller, bool, error) {
	info, ok := s.ResolveIndex(path)
	if !ok {
		return nil, false, nil
	}
	res, err := s.callers(info.ID, name, limit)
	return res, err == nil, err
}

func (s *RepoService) DefinitionsAt(path, name string, importsOnly bool, limit int) ([]engine.UsageMatch, bool, error) {
	info, ok := s.ResolveIndex(path)
	if !ok {
		return nil, false, nil
	}
	res, err := s.definitions(info.ID, name, importsOnly, limit)
	return res, err == nil, err
}

func (s *RepoService) ComplexityAt(path string, limit int) ([]engine.RankedComplexity, bool, error) {
	info, ok := s.ResolveIndex(path)
	if !ok {
		return nil, false, nil
	}
	res, err := s.complexity(info.ID, limit)
	return res, err == nil, err
}

func (s *RepoService) OutlineAt(path string, includeText bool) (*OutlineResult, bool, error) {
	info, ok := s.ResolveIndex(path)
	if !ok {
		return nil, false, nil
	}
	res, err := s.outline(info.ID, path, includeText)
	return res, err == nil, err
}

func (s *RepoService) AnalyzeAt(path string) (*engine.FileReport, bool, error) {
	info, ok := s.ResolveIndex(path)
	if !ok {
		return nil, false, nil
	}
	res, err := s.analyze(info.ID, path)
	return res, err == nil, err
}

func (s *RepoService) ImpactAt(path, graph, target string, reverse bool, depth, limit int, cursor string) (repoindex.ImpactResult, bool, error) {
	info, ok := s.ResolveIndex(path)
	if !ok {
		return repoindex.ImpactResult{}, false, nil
	}
	res, err := s.impact(info.ID, graph, target, reverse, depth, limit, cursor)
	return res, err == nil, err
}

func (s *RepoService) CyclesAt(path, graph string) ([][]string, bool, error) {
	info, ok := s.ResolveIndex(path)
	if !ok {
		return nil, false, nil
	}
	res, err := s.cycles(info.ID, graph)
	return res, err == nil, err
}

func (s *RepoService) TopologyAt(path, graph string) ([][]string, bool, error) {
	info, ok := s.ResolveIndex(path)
	if !ok {
		return nil, false, nil
	}
	res, err := s.topology(info.ID, graph)
	return res, err == nil, err
}

func (s *RepoService) status(id string) (repoindex.Info, error) {
	info, ok := s.store.Info(id)
	if !ok {
		return repoindex.Info{}, fmt.Errorf("unknown repository %q", id)
	}
	return info, nil
}

// build indexes every recognized file under root and replaces the snapshot.
func (s *RepoService) build(ctx context.Context, id, root string, filters []lang.Language) {
	start := time.Now()
	files := make(map[string]repoindex.IndexedFile)
	errs := make(map[string]string)
	for _, f := range filters {
		facts, fileErrs, err := s.eng.IndexDir(ctx, root, f)
		if err != nil {
			errs[root] = err.Error()
			s.log().Warn(fmt.Sprintf("failed to scan directory: %s", err), "root", root)
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
	// Clone errs: Replace retains the passed map and later Apply calls mutate
	// it, while firstErrors below still reads the local one.
	info, _ := s.store.Replace(id, files, maps.Clone(errs))
	s.saveSnapshot(id)
	args := []any{"root", root}
	if len(errs) > 0 {
		args = append(args, "first_errors", firstErrors(errs, 3))
	}
	s.log().Info(fmt.Sprintf("index build finished in %s: %d %s, %d %s; %s index in RAM; %s snapshot on disk",
		humanDur(time.Since(start)),
		info.Files, plural(info.Files, "file", "files"),
		info.Errors, plural(info.Errors, "failure", "failures"),
		humanBytes(info.MemoryBytes), humanBytes(s.snapshotSize(root))),
		args...)
}

// snapshotSize returns the on-disk size of the repo snapshot, or -1 when it
// does not exist yet.
func (s *RepoService) snapshotSize(root string) int64 {
	st, err := os.Stat(s.snapshotPath(root))
	if err != nil {
		return -1
	}
	return st.Size()
}

// plural picks the singular or plural noun for log messages.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// humanDur formats a duration for log messages ("45ms", "1.4s", "2m3s").
func humanDur(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return d.Round(time.Second).String()
}

// humanBytes formats a byte count for log messages ("794 B", "14.2 MB");
// negative means absent ("missing").
func humanBytes(n int64) string {
	if n < 0 {
		return "missing"
	}
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	f := float64(n)
	for _, u := range []string{"KB", "MB", "GB"} {
		f /= 1024
		if f < 1024 {
			return fmt.Sprintf("%.1f %s", f, u)
		}
	}
	return fmt.Sprintf("%.1f TB", f/1024)
}

// firstErrors returns up to n path->error entries in stable (sorted) order.
func firstErrors(errs map[string]string, n int) map[string]string {
	if len(errs) == 0 {
		return nil
	}
	paths := make([]string, 0, len(errs))
	for p := range errs {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	out := make(map[string]string, min(n, len(paths)))
	for _, p := range paths[:min(n, len(paths))] {
		out[p] = errs[p]
	}
	return out
}

// saveSnapshot persists the current index state in the background when
// persistence is enabled. The file write is atomic and serialized.
func (s *RepoService) saveSnapshot(id string) {
	if s.toolVersion == "" {
		return
	}
	info, ok := s.store.Info(id)
	if !ok {
		return
	}
	meta, ok := s.store.Meta(id)
	if !ok {
		return
	}
	langs, _ := s.snapshotLangs.Load(id)
	names, _ := langs.([]string)
	snap := &repoindex.Snapshot{
		Header: repoindex.SnapshotHeader{
			SchemaVersion: repoindex.SnapshotSchemaVersion,
			ToolVersion:   s.toolVersion,
			Root:          info.Root,
			Languages:     strings.Join(names, ","),
		},
		Files: meta,
	}
	s.snapshotMu.Lock()
	defer s.snapshotMu.Unlock()
	if err := repoindex.SaveSnapshot(s.snapshotPath(info.Root), snap); err != nil {
		s.log().Warn(fmt.Sprintf("failed to save snapshot: %s", err), "root", info.Root)
	}
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
func (s *RepoService) refresh(ctx context.Context, id string, languages []string) (repoindex.Info, error) {
	info, ok := s.store.Info(id)
	if !ok {
		return repoindex.Info{}, fmt.Errorf("unknown repository %q", id)
	}
	if len(info.Root) == 0 {
		return repoindex.Info{}, fmt.Errorf("repository %q has no root", id)
	}
	if info.State == "building" || info.State == "refreshing" {
		return info, nil
	}
	lock := s.refreshLock(id)
	if !lock.TryLock() {
		info.State = "refreshing"
		return info, nil
	}
	defer lock.Unlock()
	start := time.Now()
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
		s.log().Info(fmt.Sprintf("delta of %d files exceeded the limit of %d: full index build",
			len(added)+len(changed)+len(deleted), threshold),
			"root", info.Root)
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
	info, err = s.store.Apply(id, cs)
	if err != nil {
		return repoindex.Info{}, err
	}
	s.saveSnapshot(id)
	s.log().Info(fmt.Sprintf("incremental refresh in %s: +%d added, %d updated, %d removed; %d %s total, %d %s",
		humanDur(time.Since(start)),
		len(cs.Added), len(cs.Updated), len(cs.Deleted),
		info.Files, plural(info.Files, "file", "files"),
		info.Errors, plural(info.Errors, "failure", "failures")),
		"root", info.Root)
	return info, nil
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
	f, err := os.Open(p)
	if err != nil {
		return [32]byte{}, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return [32]byte{}, err
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out, nil
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
func (s *RepoService) usagePage(id, name, cursor string, limit int) (*UsagePage, error) {
	info, ok := s.store.Info(id)
	if !ok {
		return nil, fmt.Errorf("unknown repository %q", id)
	}
	c, err := repoindex.CheckCursor(cursor, info.Version)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 500
	}
	matches, total, ok := s.store.UsagesWindow(id, name, c.Offset, limit)
	if !ok {
		return nil, fmt.Errorf("unknown repository %q", id)
	}
	page := &UsagePage{Matches: matches}
	end := c.Offset + limit
	if end < total {
		page.Truncated = true
		page.NextCursor = repoindex.EncodeCursor(info.Version, end)
	}
	return page, nil
}

func (s *RepoService) impact(id, graph, target string, reverse bool, depth, limit int, cursor string) (repoindex.ImpactResult, error) {
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

func (s *RepoService) cycles(id, graph string) ([][]string, error) {
	adj, nodes, err := s.graphData(id, graph, false)
	if err != nil {
		return nil, err
	}
	return repoindex.Cycles(nodes, adj), nil
}

func (s *RepoService) topology(id, graph string) ([][]string, error) {
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
func (s *RepoService) outline(id, path string, includeText bool) (*OutlineResult, error) {
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
func (s *RepoService) analyze(id, path string) (*engine.FileReport, error) {
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

// Callers returns every function that calls target, aggregated per caller,
// matching the direct callers flow (kind/line/col from the caller symbol).
func (s *RepoService) callers(id, name string, limit int) ([]engine.Caller, error) {
	g, ok := s.store.Calls(id)
	if !ok {
		return nil, fmt.Errorf("unknown repository %q", id)
	}
	files, _ := s.store.Files(id)
	out := make([]engine.Caller, 0)
	for _, e := range g.ByCallee[name] {
		caller := engine.Caller{File: e.File, Name: e.Caller, Count: e.Count}
		if facts, ok := files[e.File]; ok {
			if kind, sym, found := findSymbol(facts.Symbols, e.Caller); found {
				caller.Kind = kind
				caller.Line = sym.Start.Row + 1
				caller.Col = sym.Start.Col
			}
		}
		out = append(out, caller)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Name < out[j].Name
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// findSymbol locates the first declared symbol with the given name.
func findSymbol(groups map[string][]engine.Symbol, name string) (string, engine.Symbol, bool) {
	for kind, syms := range groups {
		if kind == "imports" {
			continue
		}
		for _, sym := range syms {
			if sym.Name == name {
				return kind, sym, true
			}
		}
	}
	return "", engine.Symbol{}, false
}

// Definitions returns declared symbols matching name (importsOnly selects the
// import kind), mirroring the direct definitions/imports flow.
func (s *RepoService) definitions(id, name string, importsOnly bool, limit int) ([]engine.UsageMatch, error) {
	files, ok := s.store.Files(id)
	if !ok {
		return nil, fmt.Errorf("unknown repository %q", id)
	}
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	matches := make([]engine.UsageMatch, 0)
	for _, p := range paths {
		for kind, syms := range files[p].Symbols {
			isImport := kind == "imports"
			if importsOnly != isImport {
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
				matches = append(matches, engine.UsageMatch{
					File: p, Name: sym.Name, Line: sym.Start.Row + 1, Col: sym.Start.Col, Text: sym.Text, Kind: mk,
				})
			}
		}
	}
	sortUsageMatches(matches)
	if limit > 0 && len(matches) > limit {
		matches = matches[:limit]
	}
	return matches, nil
}

// Unused returns symbols declared but never referenced, from AST occurrences
// in the index. The result is heuristic (no scope resolution).
func (s *RepoService) unused(id string, limit int) (*engine.SearchResult, error) {
	matches, ok := s.store.Unused(id)
	if !ok {
		return nil, fmt.Errorf("unknown repository %q", id)
	}
	if limit > 0 && len(matches) > limit {
		matches = matches[:limit]
	}
	return &engine.SearchResult{Total: len(matches), Matches: matches}, nil
}

func (s *RepoService) complexity(id string, limit int) ([]engine.RankedComplexity, error) {
	entries, ok := s.store.Complexity(id, limit)
	if !ok {
		return nil, fmt.Errorf("unknown repository %q", id)
	}
	return entries, nil
}

// Scan filters indexed symbols by language, kind, exact name and limit.
func (s *RepoService) scan(id string, languages, kinds []string, name string, limit int) (*ScanResult, error) {
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
	res := &ScanResult{Language: displayLang(languages), Source: "indexed", Files: limitFiles(out, limit)}
	return res, nil
}

// FindUsages queries indexed usages for a symbol with the same filters as the
// direct FindService flow (kinds, group_by_file, limit).
func (s *RepoService) findUsages(id, name string, q FindQuery) (*FindResult, error) {
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
