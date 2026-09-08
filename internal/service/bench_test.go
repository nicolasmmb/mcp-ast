package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"mcp-ast/internal/engine"
	"mcp-ast/internal/lang"
	golanglang "mcp-ast/internal/languages/go"
	"mcp-ast/internal/repoindex"
)

// generateRepo writes n synthetic Go files. Every file references the
// identifier "token" once, so lookups can page over the whole repo.
func generateRepo(tb testing.TB, n int) string {
	tb.Helper()
	dir := tb.TempDir()
	writeSynthetic(tb, dir, n, 0)
	return dir
}

func writeSynthetic(tb testing.TB, dir string, n, start int) {
	tb.Helper()
	for i := 0; i < n; i++ {
		path := filepath.Join(dir, fmt.Sprintf("f%06d.go", start+i))
		src := fmt.Sprintf("package bench\n\nfunc F%06d() int {\n\treturn token + %d\n}\n\nfunc G%06d() int {\n\treturn F%06d()\n}\n", start+i, i, start+i, start+i)
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			tb.Fatal(err)
		}
	}
}

func benchServices(tb testing.TB) *Services {
	tb.Helper()
	reg := lang.NewRegistry()
	if err := reg.Register(golanglang.Go{}); err != nil {
		tb.Fatal(err)
	}
	return NewWithStore(engine.New(reg), repoindex.NewMemory(0))
}

// indexAll indexes dir synchronously (the same work index_repo does in the
// background) and returns the repo info.
func indexAll(tb testing.TB, svcs *Services, dir string) repoindex.Info {
	tb.Helper()
	info := svcs.Repo.store.Create(dir)
	filters, err := filters(svcs.Engine, nil, dir)
	if err != nil {
		tb.Fatal(err)
	}
	files := map[string]repoindex.IndexedFile{}
	errs := map[string]string{}
	for _, f := range filters {
		facts, fileErrs, err := svcs.Engine.IndexDir(context.Background(), dir, f)
		if err != nil {
			tb.Fatal(err)
		}
		for p, fact := range facts {
			st, err := os.Stat(p)
			if err != nil {
				continue
			}
			digest, err := fileDigest(p)
			if err != nil {
				continue
			}
			files[p] = repoindex.IndexedFile{Facts: fact, Size: st.Size(), ModTime: st.ModTime().UnixNano(), Digest: digest}
		}
		for p, e := range fileErrs {
			errs[p] = e
		}
	}
	info, err = svcs.Repo.store.Replace(info.ID, files, errs)
	if err != nil {
		tb.Fatal(err)
	}
	return info
}

func BenchmarkIndex50k(b *testing.B) {
	svcs := benchServices(b)
	dir := generateRepo(b, 50000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		indexAll(b, svcs, dir)
	}
}

func BenchmarkRefreshNoop50k(b *testing.B) {
	svcs := benchServices(b)
	dir := generateRepo(b, 50000)
	info := indexAll(b, svcs, dir)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := svcs.Repo.refresh(context.Background(), info.ID, nil); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRefreshOnePercent50k(b *testing.B) {
	svcs := benchServices(b)
	dir := generateRepo(b, 50000)
	info := indexAll(b, svcs, dir)
	for i := 0; i < 500; i++ {
		path := filepath.Join(dir, fmt.Sprintf("f%06d.go", i*100))
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := f.WriteString("\n// touched\n"); err != nil {
			b.Fatal(err)
		}
		f.Close()
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := svcs.Repo.refresh(context.Background(), info.ID, nil); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLookup50k(b *testing.B) {
	svcs := benchServices(b)
	dir := generateRepo(b, 50000)
	info := indexAll(b, svcs, dir)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := svcs.Repo.usagePage(info.ID, "token", "", 500); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkImpact50k(b *testing.B) {
	svcs := benchServices(b)
	dir := generateRepo(b, 50000)
	info := indexAll(b, svcs, dir)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := svcs.Repo.impact(info.ID, "calls", "F000001", true, 1, 100, ""); err != nil {
			b.Fatal(err)
		}
	}
}

func TestScaleIndexLookup5k(t *testing.T) {
	svcs := benchServices(t)
	dir := generateRepo(t, 5000)
	info := indexAll(t, svcs, dir)
	if info.State != "ready" || info.Files != 5000 {
		t.Fatalf("unexpected state: %#v", info)
	}
	page, err := svcs.Repo.usagePage(info.ID, "token", "", 5000)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Matches) != 5000 {
		t.Fatalf("want 5000 token usages, got %d", len(page.Matches))
	}
}

func TestScaleMemoryPartial(t *testing.T) {
	reg := lang.NewRegistry()
	if err := reg.Register(golanglang.Go{}); err != nil {
		t.Fatal(err)
	}
	svcs := NewWithStore(engine.New(reg), repoindex.NewMemory(1)) // tight budget
	dir := generateRepo(t, 2000)
	info := indexAll(t, svcs, dir)
	if info.State != "partial" {
		t.Fatalf("want partial with 1-byte budget, got %#v", info)
	}
	// usages must still answer (index data kept, state partial)
	if _, err := svcs.Repo.findUsages(info.ID, "token", FindQuery{Mode: FindOccurrences}); err != nil {
		t.Fatalf("query after partial must work: %v", err)
	}
}
