package service

import (
	"context"
	"os"
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

	res, err := svcs.Repo.Impact(info.ID, "calls", "Helper", true, 0, 10)
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
	imports, err := svcs.Repo.Impact(info.ID, "imports", "fmt", true, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(imports.Nodes) == 0 {
		t.Fatal("fmt import should be listed")
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
