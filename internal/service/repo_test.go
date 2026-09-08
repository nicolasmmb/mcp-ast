package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"mcp-ast/internal/repoindex"
)

func waitReady(t *testing.T, svcs *Services, id string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		info, err := svcs.Repo.status(id)
		if err != nil {
			t.Fatal(err)
		}
		if info.State == "ready" || info.State == "partial" {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("repo %s never became ready", id)
}

func testRepoServices(t *testing.T, limit int64) *Services {
	t.Helper()
	svcs := testServices(t)
	return releaseSvcs(svcs, repoindex.NewMemory(limit))
}

// releaseSvcs preserves the engine from Services and swaps the store.
func releaseSvcs(svcs *Services, store repoindex.Store) *Services {
	svcsWithout := &Services{
		Engine: svcs.Engine,
		Scan:   svcs.Scan, File: svcs.File, Usages: svcs.Usages,
		Unused: svcs.Unused, Calls: svcs.Calls, Find: svcs.Find, Rank: svcs.Rank,
	}
	return NewWithStore(svcsWithout.Engine, store)
}

func TestRepoServiceIndexAndQuery(t *testing.T) {
	svcs := testRepoServices(t, 1<<30)
	dir, _, _ := writeFixture(t)

	info, err := svcs.Repo.Index(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitReady(t, svcs, info.ID)
	info, err = svcs.Repo.status(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if info.State != "ready" || info.Files != 2 {
		t.Fatalf("unexpected status: %#v", info)
	}

	scan, err := svcs.Repo.scan(info.ID, nil, nil, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(scan.Files) != 2 {
		t.Fatalf("scan: want 2 files, got %d", len(scan.Files))
	}

	got, err := svcs.Repo.findUsages(info.ID, "Helper", FindQuery{Mode: FindOccurrences, GroupByFile: false})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Matches) < 4 {
		t.Fatalf("usages: want >=4 Helper matches, got %d", len(got.Matches))
	}
	for _, m := range got.Matches {
		if m.Name != "Helper" {
			t.Fatalf("usage name mismatch: %+v", m)
		}
	}

	ranked, err := svcs.Repo.complexity(info.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ranked) == 0 || ranked[0].Complexity != ranked[0].Complexity {
		t.Fatalf("unexpected complexity: %#v", ranked)
	}
}

// logBuf is a concurrency-safe buffer: index logs are written from the
// build goroutine while the test polls the contents.
type logBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func TestRepoBuildLog(t *testing.T) {
	buf := &logBuf{}
	svcs := testRepoServices(t, 1<<30)
	svcs.Repo.SetLogger(slog.New(slog.NewTextHandler(buf, nil)))
	dir, _, _ := writeFixture(t)
	info, err := svcs.Repo.Index(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitReady(t, svcs, info.ID)
	line := ""
	deadline := time.Now().Add(2 * time.Second)
	for line == "" && time.Now().Before(deadline) {
		for _, l := range strings.Split(buf.String(), "\n") {
			if strings.Contains(l, "index build finished in") {
				line = l
			}
		}
		if line == "" {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if line == "" {
		t.Fatalf("no 'index build finished' line in log output:\n%s", buf.String())
	}
	for _, want := range []string{"index build finished in", "2 files, 0 failures", "index in RAM", "snapshot", "on disk"} {
		if !strings.Contains(line, want) {
			t.Fatalf("log line missing %q: %s", want, line)
		}
	}
	if strings.Contains(line, "first_errors") {
		t.Fatalf("log line should omit first_errors when empty: %s", line)
	}
}

func TestRepoServiceMemoryPartial(t *testing.T) {
	svcs := testRepoServices(t, 1024)
	dir, _, _ := writeFixture(t)
	info, err := svcs.Repo.Index(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitReady(t, svcs, info.ID)
	info, err = svcs.Repo.status(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if info.State != "partial" {
		t.Fatalf("want partial state, got %q", info.State)
	}
	if _, err := svcs.Repo.findUsages(info.ID, "Helper", FindQuery{Mode: FindOccurrences}); err != nil {
		t.Fatalf("query on partial index should still succeed: %v", err)
	}
}

func TestRepoServiceRefreshIncremental(t *testing.T) {
	svcs := testRepoServices(t, 1<<30)
	dir, mainPath, _ := writeFixture(t)

	info, err := svcs.Repo.Index(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitReady(t, svcs, info.ID)
	info, err = svcs.Repo.status(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	before := info.Version

	facts, err := os.Stat(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	newMain := strings.Replace(fixtureMain, "func Main()", "func Main2()", 1)
	if err := os.WriteFile(mainPath, []byte(newMain), facts.Mode()); err != nil {
		t.Fatal(err)
	}
	info, err = svcs.Repo.refresh(context.Background(), info.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if info.Version <= before {
		t.Fatalf("refresh should bump version: before %d, after %d", before, info.Version)
	}
	if _, err := svcs.Repo.findUsages(info.ID, "Main2", FindQuery{Mode: FindOccurrences}); err != nil {
		t.Fatalf("usages after refresh: %v", err)
	}
	res, _ := svcs.Repo.findUsages(info.ID, "Main2", FindQuery{Mode: FindOccurrences})
	if len(res.Matches) == 0 {
		t.Fatal("Main2 should have usages after refresh")
	}
	if old, _ := svcs.Repo.findUsages(info.ID, "Main", FindQuery{Mode: FindOccurrences}); len(old.Matches) != 0 {
		t.Fatalf("Main should be gone after rename: %d matches", len(old.Matches))
	}
}

func TestRepoServiceRefreshDelete(t *testing.T) {
	svcs := testRepoServices(t, 1<<30)
	dir, mainPath, _ := writeFixture(t)
	info, err := svcs.Repo.Index(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitReady(t, svcs, info.ID)
	if err := os.Remove(mainPath); err != nil {
		t.Fatal(err)
	}
	info, err = svcs.Repo.refresh(context.Background(), info.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if info.Files != 1 {
		t.Fatalf("want 1 file after delete, got %d", info.Files)
	}
}

func TestRepoServiceGraphs(t *testing.T) {
	svcs := testRepoServices(t, 1<<30)
	dir, _, _ := writeFixture(t)
	info, err := svcs.Repo.Index(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitReady(t, svcs, info.ID)

	res, err := svcs.Repo.impact(info.ID, "calls", "Helper", true, 0, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Nodes) == 0 {
		t.Fatal("Helper should have callers in the call graph")
	}
	cycles, err := svcs.Repo.cycles(info.ID, "calls")
	if err != nil {
		t.Fatal(err)
	}
	layers, err := svcs.Repo.topology(info.ID, "calls")
	if err != nil {
		t.Fatal(err)
	}
	_ = cycles
	if len(layers) == 0 {
		t.Fatal("topology should return at least one layer")
	}
	imports, err := svcs.Repo.impact(info.ID, "imports", "fmt", true, 0, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(imports.Nodes) == 0 {
		t.Fatal("fmt import should be listed")
	}
}

func TestRepoServiceOutlineIndexedAndFallback(t *testing.T) {
	svcs := testRepoServices(t, 1<<30)
	dir, mainPath, _ := writeFixture(t)
	info, err := svcs.Repo.Index(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitReady(t, svcs, info.ID)

	outline, err := svcs.Repo.outline(info.ID, mainPath, false)
	if err != nil {
		t.Fatal(err)
	}
	if outline.Source != "indexed" || len(outline.Outline) == 0 {
		t.Fatalf("want indexed outline, got %#v", outline)
	}
	if !strings.Contains(jsonOut(outline), "Main") {
		t.Fatalf("outline should contain Main: %s", jsonOut(outline))
	}

	full, err := svcs.Repo.outline(info.ID, mainPath, true)
	if err != nil {
		t.Fatal(err)
	}
	if full.Source != "ast_fallback" || len(full.Outline) == 0 {
		t.Fatalf("want ast_fallback outline with text, got %#v", full)
	}

	report, err := svcs.Repo.analyze(info.ID, mainPath)
	if err != nil {
		t.Fatal(err)
	}
	if report.Source != "ast_fallback" || report.Metrics == nil {
		t.Fatalf("want ast_fallback dossier with metrics, got %#v", report)
	}
}

func TestRepoServiceStaleFileReindexed(t *testing.T) {
	svcs := testRepoServices(t, 1<<30)
	dir, mainPath, _ := writeFixture(t)
	info, err := svcs.Repo.Index(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitReady(t, svcs, info.ID)
	before := info.Version

	newMain := strings.Replace(fixtureMain, "func Main()", "func Main2()", 1)
	if err := os.WriteFile(mainPath, []byte(newMain), 0o644); err != nil {
		t.Fatal(err)
	}
	outline, err := svcs.Repo.outline(info.ID, mainPath, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(jsonOut(outline), "Main2") {
		t.Fatalf("stale file should be reindexed before serving outline: %s", jsonOut(outline))
	}
	info, err = svcs.Repo.status(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if info.Version <= before {
		t.Fatalf("stale file reindex should bump version: before %d, after %d", before, info.Version)
	}
}

func TestRepoServiceUnused(t *testing.T) {
	svcs := testRepoServices(t, 1<<30)
	dir, _, _ := writeFixture(t)
	info, err := svcs.Repo.Index(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitReady(t, svcs, info.ID)

	res, err := svcs.Repo.unused(info.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range res.Matches {
		if m.Name == "UnusedFn" {
			found = true
		}
	}
	if !found {
		t.Fatalf("UnusedFn should be unused, got %#v", res.Matches)
	}
	for _, m := range res.Matches {
		if m.Name == "Helper" {
			t.Fatalf("Helper is used and must not be flagged: %#v", m)
		}
	}
}

func jsonOut(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(data)
}

func TestRepoServiceRefreshCoalesced(t *testing.T) {
	svcs := testRepoServices(t, 1<<30)
	dir, _, _ := writeFixture(t)
	info, err := svcs.Repo.Index(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitReady(t, svcs, info.ID)

	lock := svcs.Repo.refreshLock(info.ID)
	lock.Lock() // simulate an in-flight refresh
	defer lock.Unlock()
	res, err := svcs.Repo.refresh(context.Background(), info.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.State != "refreshing" {
		t.Fatalf("concurrent refresh should coalesce into refreshing, got %q", res.State)
	}
}

func TestRepoServiceUsagePagination(t *testing.T) {
	svcs := testRepoServices(t, 1<<30)
	dir := t.TempDir()
	path := filepath.Join(dir, "big.go")
	src := "package app\nfunc F() {\n"
	for i := 0; i < 5000; i++ {
		src += "\t_ = token\n"
	}
	src += "}\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := svcs.Repo.Index(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitReady(t, svcs, info.ID)

	seen := map[string]bool{}
	cursor := ""
	total := 0
	for {
		page, err := svcs.Repo.usagePage(info.ID, "token", cursor, 500)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range page.Matches {
			key := fmt.Sprintf("%s:%d", m.File, m.Line)
			if seen[key] {
				t.Fatalf("duplicate across pages: %s", key)
			}
			seen[key] = true
			total++
		}
		if !page.Truncated {
			break
		}
		cursor = page.NextCursor
		if cursor == "" {
			t.Fatal("truncated page without next_cursor")
		}
	}
	if total != 5000 {
		t.Fatalf("want 5000 usages across pages, got %d", total)
	}

	// a stale cursor (older index version) must be rejected after an update
	if err := os.WriteFile(path, []byte(src+"// touch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svcs.Repo.refresh(context.Background(), info.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := svcs.Repo.usagePage(info.ID, "token", cursor, 500); err == nil {
		t.Fatal("stale cursor must be rejected after index version bump")
	}
}

func TestRepoServiceWatch(t *testing.T) {
	svcs := testRepoServices(t, 1<<30)
	svcs.Repo.SetWatchInterval(30 * time.Millisecond)
	dir, mainPath, _ := writeFixture(t)
	info, err := svcs.Repo.Index(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitReady(t, svcs, info.ID)
	st, err := svcs.Repo.status(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Watch {
		t.Fatal("watch should be enabled after index_repo")
	}
	v1 := st.Version

	newMain := strings.Replace(fixtureMain, "func Main()", "func Main2()", 1)
	if err := os.WriteFile(mainPath, []byte(newMain), 0o644); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	var v2 uint64
	for time.Now().Before(deadline) {
		st, _ = svcs.Repo.status(info.ID)
		if st.Version > v1 {
			v2 = st.Version
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if v2 == 0 {
		t.Fatal("watch did not pick up the change")
	}
	// one edit -> one bump: further ticks must not bump again
	time.Sleep(150 * time.Millisecond)
	st, err = svcs.Repo.status(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if st.Version != v2 {
		t.Fatalf("watch applied extra bumps: %d -> %d", v2, st.Version)
	}
}

func waitSnapshot(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := repoindex.LoadSnapshot(path); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("snapshot %s never written", path)
}

func TestRepoServiceRestoreOnBoot(t *testing.T) {
	cacheDir := t.TempDir()
	dir, mainPath, _ := writeFixture(t)

	svcs1 := testRepoServices(t, 1<<30)
	svcs1.Repo.SetToolVersion("test-v1")
	svcs1.Repo.SetCacheDir(cacheDir)
	info, err := svcs1.Repo.Index(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitReady(t, svcs1, info.ID)
	snapPath := svcs1.Repo.snapshotPath(mustAbs(t, dir))
	waitSnapshot(t, snapPath)
	snapInfo, err := os.Stat(snapPath)
	if err != nil {
		t.Fatalf("snapshot file must exist on disk: %v", err)
	}
	if snapInfo.Size() == 0 {
		t.Fatal("snapshot file must have non-zero size")
	}
	st1, err := svcs1.Repo.status(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if st1.Restored {
		t.Fatal("first boot must build, not restore")
	}

	// second boot: a fresh process (new store) must restore without re-parse
	svcs2 := testRepoServices(t, 1<<30)
	svcs2.Repo.SetToolVersion("test-v1")
	svcs2.Repo.SetCacheDir(cacheDir)
	info2, err := svcs2.Repo.Index(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitReady(t, svcs2, info2.ID)
	st2, err := svcs2.Repo.status(info2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !st2.Restored || st2.Files != 2 {
		t.Fatalf("second boot must restore: %#v", st2)
	}
	usages, err := svcs2.Repo.findUsages(info2.ID, "Helper", FindQuery{Mode: FindOccurrences})
	if err != nil || len(usages.Matches) < 4 {
		t.Fatalf("restored index must answer queries: %#v %v", usages, err)
	}

	// different tool version invalidates the snapshot
	svcs3 := testRepoServices(t, 1<<30)
	svcs3.Repo.SetToolVersion("test-v2")
	svcs3.Repo.SetCacheDir(cacheDir)
	info3, err := svcs3.Repo.Index(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitReady(t, svcs3, info3.ID)
	st3, err := svcs3.Repo.status(info3.ID)
	if err != nil {
		t.Fatal(err)
	}
	if st3.Restored {
		t.Fatal("different tool version must rebuild, not restore")
	}
	_ = mainPath
}

func TestRepoServiceRestoreStaleFile(t *testing.T) {
	cacheDir := t.TempDir()
	dir, mainPath, _ := writeFixture(t)

	svcs1 := testRepoServices(t, 1<<30)
	svcs1.Repo.SetToolVersion("test-v1")
	svcs1.Repo.SetCacheDir(cacheDir)
	info, err := svcs1.Repo.Index(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitReady(t, svcs1, info.ID)
	waitSnapshot(t, svcs1.Repo.snapshotPath(mustAbs(t, dir)))

	// the file changes on disk after the snapshot was written
	newMain := strings.Replace(fixtureMain, "func Main()", "func Main2()", 1)
	if err := os.WriteFile(mainPath, []byte(newMain), 0o644); err != nil {
		t.Fatal(err)
	}

	svcs2 := testRepoServices(t, 1<<30)
	svcs2.Repo.SetToolVersion("test-v1")
	svcs2.Repo.SetCacheDir(cacheDir)
	info2, err := svcs2.Repo.Index(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitReady(t, svcs2, info2.ID)
	st2, err := svcs2.Repo.status(info2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !st2.Restored {
		t.Fatal("stale-file restore should still restore then refresh")
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if usages, _ := svcs2.Repo.findUsages(info2.ID, "Main2", FindQuery{Mode: FindOccurrences}); len(usages.Matches) > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("background refresh after restore did not pick up the changed file")
}

func mustAbs(t *testing.T, dir string) string {
	t.Helper()
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func TestRepoServiceFindAllModes(t *testing.T) {
	svcs := testRepoServices(t, 1<<30)
	dir, _, _ := writeFixture(t)
	info, err := svcs.Repo.Index(context.Background(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitReady(t, svcs, info.ID)

	callers, err := svcs.Repo.callers(info.ID, "Helper", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(callers) != 2 {
		t.Fatalf("want 2 callers of Helper, got %#v", callers)
	}
	counts := map[string]int{}
	for _, c := range callers {
		counts[c.Name] = c.Count
	}
	if counts["Main"] != 2 || counts["Method"] != 1 {
		t.Fatalf("unexpected caller counts: %#v", callers)
	}

	defs, err := svcs.Repo.definitions(info.ID, "Helper", false, 0)
	if err != nil {
		t.Fatal(err)
	}
	foundDef := false
	for _, d := range defs {
		if d.Name == "Helper" && d.Kind == "definition" {
			foundDef = true
		}
	}
	if !foundDef {
		t.Fatalf("Helper definition missing: %#v", defs)
	}

	imports, err := svcs.Repo.definitions(info.ID, "fmt", true, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(imports) != 1 || imports[0].Kind != "import" {
		t.Fatalf("want 1 fmt import, got %#v", imports)
	}
}

func TestRepoServiceUnknownID(t *testing.T) {
	svcs := testRepoServices(t, 1<<30)
	if _, err := svcs.Repo.status("nope"); err == nil {
		t.Fatal("want error for unknown repo id")
	}
	if _, err := svcs.Repo.findUsages("nope", "x", FindQuery{Mode: FindOccurrences}); err == nil {
		t.Fatal("want error for unknown repo id on usages")
	}
}

func TestRepoServiceReindexSameRootReplaces(t *testing.T) {
	svcs := testRepoServices(t, 1<<30)
	ctx := context.Background()
	dir, _, _ := writeFixture(t)

	first, err := svcs.Repo.Index(ctx, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitReady(t, svcs, first.ID)

	second, err := svcs.Repo.Index(ctx, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitReady(t, svcs, second.ID)

	if first.ID == second.ID {
		t.Fatal("reindex of the same root must create a new repository")
	}
	if _, ok := svcs.Repo.store.Info(first.ID); ok {
		t.Fatal("previous repository of the root must be dropped")
	}
	repos := svcs.Repo.List()
	if len(repos) != 1 || repos[0].ID != second.ID {
		t.Fatalf("want only the new repository, got %v", repos)
	}
	if got, ok := svcs.Repo.ResolveIndex(dir); !ok || got.ID != second.ID {
		t.Fatalf("root must resolve to the new repository: ok=%v got=%v", ok, got)
	}
	if _, ok := svcs.Repo.watchLangs.Load(first.ID); ok {
		t.Fatal("watchLangs must not keep a dropped repository")
	}
	if _, ok := svcs.Repo.snapshotLangs.Load(first.ID); ok {
		t.Fatal("snapshotLangs must not keep a dropped repository")
	}
}

func TestRepoServiceResolveIndexPath(t *testing.T) {
	svcs := testRepoServices(t, 1<<30)
	ctx := context.Background()
	dir, _, _ := writeFixture(t)
	info, err := svcs.Repo.Index(ctx, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitReady(t, svcs, info.ID)

	if got, ok := svcs.Repo.ResolveIndex(dir); !ok || got.ID != info.ID {
		t.Fatalf("root path must resolve to indexed repo: ok=%v got=%v", ok, got)
	}
	if got, ok := svcs.Repo.ResolveIndex(filepath.Join(dir, "vendor")); !ok || got.ID != info.ID {
		t.Fatalf("subdir path must resolve to indexed repo: ok=%v got=%v", ok, got)
	}
	if got, ok := svcs.Repo.ResolveIndex(filepath.Join(dir, "vendor", "..")); !ok || got.ID != info.ID {
		t.Fatalf("path with .. must normalize and resolve: ok=%v got=%v", ok, got)
	}
	outside := t.TempDir()
	if _, ok := svcs.Repo.ResolveIndex(outside); ok {
		t.Fatal("path outside every root must not resolve")
	}

	res, ok, err := svcs.Repo.ScanAt(dir, nil, nil, "", 10)
	if err != nil || !ok || res == nil {
		t.Fatalf("ScanAt on root: ok=%v err=%v res=%v", ok, err, res)
	}
	if _, ok, err := svcs.Repo.ScanAt(outside, nil, nil, "", 10); ok || err != nil {
		t.Fatalf("ScanAt outside roots must return ok=false, nil err: ok=%v err=%v", ok, err)
	}

	if _, err := svcs.Repo.store.SetState(info.ID, "building"); err != nil {
		t.Fatal(err)
	}
	if _, ok := svcs.Repo.ResolveIndex(dir); ok {
		t.Fatal("building index must not resolve")
	}
	if _, ok, err := svcs.Repo.ScanAt(dir, nil, nil, "", 10); ok || err != nil {
		t.Fatalf("ScanAt on building index must return ok=false: ok=%v err=%v", ok, err)
	}
	if _, err := svcs.Repo.store.SetState(info.ID, "ready"); err != nil {
		t.Fatal(err)
	}

	if err := svcs.Repo.drop(info.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := svcs.Repo.ResolveIndex(dir); ok {
		t.Fatal("dropped repo must not resolve")
	}
}

func TestRepoServiceResolveNestedRoots(t *testing.T) {
	svcs := testRepoServices(t, 1<<30)
	ctx := context.Background()
	outer := t.TempDir()
	inner := filepath.Join(outer, "inner")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outer, "a.go"), []byte("package outer\nfunc A() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inner, "b.go"), []byte("package inner\nfunc B() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	outerInfo, err := svcs.Repo.Index(ctx, outer, nil)
	if err != nil {
		t.Fatal(err)
	}
	innerInfo, err := svcs.Repo.Index(ctx, inner, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitReady(t, svcs, outerInfo.ID)
	waitReady(t, svcs, innerInfo.ID)

	if got, ok := svcs.Repo.ResolveIndex(inner); !ok || got.ID != innerInfo.ID {
		t.Fatalf("nested root must win by longest prefix: ok=%v got=%v", ok, got)
	}
	if got, ok := svcs.Repo.ResolveIndex(filepath.Join(inner, "b.go")); !ok || got.ID != innerInfo.ID {
		t.Fatalf("file in nested root must resolve to inner: ok=%v got=%v", ok, got)
	}
	if got, ok := svcs.Repo.ResolveIndex(filepath.Join(outer, "a.go")); !ok || got.ID != outerInfo.ID {
		t.Fatalf("file outside nested root must resolve to outer: ok=%v got=%v", ok, got)
	}
}
