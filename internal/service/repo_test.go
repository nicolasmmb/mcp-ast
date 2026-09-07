package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mcp-ast/internal/repoindex"
)

func waitReady(t *testing.T, svcs *Services, id string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		info, err := svcs.Repo.Status(id)
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
	info, err = svcs.Repo.Status(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if info.State != "ready" || info.Files != 2 {
		t.Fatalf("unexpected status: %#v", info)
	}

	scan, err := svcs.Repo.Scan(info.ID, nil, nil, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(scan.Files) != 2 {
		t.Fatalf("scan: want 2 files, got %d", len(scan.Files))
	}

	got, err := svcs.Repo.FindUsages(info.ID, "Helper", FindQuery{Mode: FindOccurrences, GroupByFile: false})
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

	ranked, err := svcs.Repo.Complexity(info.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ranked) == 0 || ranked[0].Complexity != ranked[0].Complexity {
		t.Fatalf("unexpected complexity: %#v", ranked)
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
	info, err = svcs.Repo.Status(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if info.State != "partial" {
		t.Fatalf("want partial state, got %q", info.State)
	}
	if _, err := svcs.Repo.Usages(info.ID, "Helper"); err != nil {
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
	info, err = svcs.Repo.Status(info.ID)
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
	info, err = svcs.Repo.Refresh(context.Background(), info.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if info.Version <= before {
		t.Fatalf("refresh should bump version: before %d, after %d", before, info.Version)
	}
	if _, err := svcs.Repo.Usages(info.ID, "Main2"); err != nil {
		t.Fatalf("usages after refresh: %v", err)
	}
	matches, _ := svcs.Repo.Usages(info.ID, "Main2")
	if len(matches) == 0 {
		t.Fatal("Main2 should have usages after refresh")
	}
	if old, _ := svcs.Repo.Usages(info.ID, "Main"); len(old) != 0 {
		t.Fatalf("Main should be gone after rename: %d matches", len(old))
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
	info, err = svcs.Repo.Refresh(context.Background(), info.ID, nil)
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

	res, err := svcs.Repo.Impact(info.ID, "calls", "Helper", true, 0, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Nodes) == 0 {
		t.Fatal("Helper should have callers in the call graph")
	}
	cycles, err := svcs.Repo.Cycles(info.ID, "calls")
	if err != nil {
		t.Fatal(err)
	}
	layers, err := svcs.Repo.Topology(info.ID, "calls")
	if err != nil {
		t.Fatal(err)
	}
	_ = cycles
	if len(layers) == 0 {
		t.Fatal("topology should return at least one layer")
	}
	imports, err := svcs.Repo.Impact(info.ID, "imports", "fmt", true, 0, 10, "")
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

	outline, err := svcs.Repo.Outline(info.ID, mainPath, false)
	if err != nil {
		t.Fatal(err)
	}
	if outline.Source != "indexed" || len(outline.Outline) == 0 {
		t.Fatalf("want indexed outline, got %#v", outline)
	}
	if !strings.Contains(jsonOut(outline), "Main") {
		t.Fatalf("outline should contain Main: %s", jsonOut(outline))
	}

	full, err := svcs.Repo.Outline(info.ID, mainPath, true)
	if err != nil {
		t.Fatal(err)
	}
	if full.Source != "ast_fallback" || len(full.Outline) == 0 {
		t.Fatalf("want ast_fallback outline with text, got %#v", full)
	}

	report, err := svcs.Repo.Analyze(info.ID, mainPath)
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
	outline, err := svcs.Repo.Outline(info.ID, mainPath, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(jsonOut(outline), "Main2") {
		t.Fatalf("stale file should be reindexed before serving outline: %s", jsonOut(outline))
	}
	info, err = svcs.Repo.Status(info.ID)
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

	res, err := svcs.Repo.Unused(info.ID, 0)
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
	res, err := svcs.Repo.Refresh(context.Background(), info.ID, nil)
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
		page, err := svcs.Repo.UsagePage(info.ID, "token", cursor, 500)
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
	if _, err := svcs.Repo.Refresh(context.Background(), info.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := svcs.Repo.UsagePage(info.ID, "token", cursor, 500); err == nil {
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
	st, err := svcs.Repo.Status(info.ID)
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
		st, _ = svcs.Repo.Status(info.ID)
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
	st, err = svcs.Repo.Status(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if st.Version != v2 {
		t.Fatalf("watch applied extra bumps: %d -> %d", v2, st.Version)
	}
}

func TestRepoServiceUnknownID(t *testing.T) {
	svcs := testRepoServices(t, 1<<30)
	if _, err := svcs.Repo.Status("nope"); err == nil {
		t.Fatal("want error for unknown repo id")
	}
	if _, err := svcs.Repo.Usages("nope", "x"); err == nil {
		t.Fatal("want error for unknown repo id on usages")
	}
}
