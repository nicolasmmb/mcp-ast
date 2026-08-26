package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"mcp-ast/internal/engine"
	"mcp-ast/internal/lang"
	golanglang "mcp-ast/internal/languages/go"
)

func newTestServices(t *testing.T) *Services {
	t.Helper()
	reg := lang.NewRegistry()
	if err := reg.Register(golanglang.Go{}); err != nil {
		t.Fatal(err)
	}
	return New(engine.New(reg))
}

func writeDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const srcA = `package a

func Foo() int { x := 1; return x }
`

const srcB = `package b

type T struct{}

func (t T) Bar() {}
`

func TestScanKindsFilter(t *testing.T) {
	svcs := newTestServices(t)
	dir := writeDir(t, map[string]string{"a.go": srcA, "b.go": srcB})

	res, err := svcs.Scan.Dir(context.Background(), ScanQuery{Dir: dir, Kinds: []string{"variables"}})
	if err != nil {
		t.Fatal(err)
	}
	files := res.Files
	if len(files) != 1 {
		t.Fatalf("want only a.go (has variables), got %v", files)
	}
	a := files[filepath.Join(dir, "a.go")]
	if len(a) != 1 || len(a["variables"]) != 1 || a["variables"][0].Name != "x" {
		t.Fatalf("a.go variables: %+v", a)
	}

	// unknown kind prunes everything
	empty, err := svcs.Scan.Dir(context.Background(), ScanQuery{Dir: dir, Kinds: []string{"nope"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(empty.Files) != 0 {
		t.Fatalf("unknown kind should prune all files, got %v", empty.Files)
	}
}

func TestScanNameFilter(t *testing.T) {
	svcs := newTestServices(t)
	dir := writeDir(t, map[string]string{"a.go": srcA, "b.go": srcB})

	res, err := svcs.Scan.Dir(context.Background(), ScanQuery{Dir: dir, Name: "Foo"})
	if err != nil {
		t.Fatal(err)
	}
	a := res.Files[filepath.Join(dir, "a.go")]
	if len(res.Files) != 1 || len(a["functions"]) != 1 || a["functions"][0].Name != "Foo" {
		t.Fatalf("name=Foo result: %+v", res.Files)
	}
	if res.Language != "auto" {
		t.Fatalf("language = %q, want auto", res.Language)
	}

	// no match -> empty non-nil map
	none, err := svcs.Scan.Dir(context.Background(), ScanQuery{Dir: dir, Name: "Nope"})
	if err != nil {
		t.Fatal(err)
	}
	if none.Files == nil || len(none.Files) != 0 {
		t.Fatalf("want empty non-nil files, got %+v", none.Files)
	}
}

func TestScanLimitAndLanguageDisplay(t *testing.T) {
	svcs := newTestServices(t)
	dir := writeDir(t, map[string]string{"a.go": srcA, "b.go": srcB})

	res, err := svcs.Scan.Dir(context.Background(), ScanQuery{Dir: dir, Languages: []string{"go"}, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if res.Language != "go" {
		t.Fatalf("language = %q, want go", res.Language)
	}
	if len(res.Files) != 1 {
		t.Fatalf("limit=1 must cap files at 1, got %d", len(res.Files))
	}
}

func TestUsagesClassification(t *testing.T) {
	svcs := newTestServices(t)
	dir := writeDir(t, map[string]string{
		"a.go": "package a\n\nfunc Foo() int { return 1 }\n\nfunc call() int { return Foo() + 1 }\n",
	})
	res, err := svcs.Usages.Dir(context.Background(), "Foo", dir, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	for _, m := range res.Matches {
		kinds[m.Kind]++
		if m.Kind == "call-site" && m.Caller != "call" {
			t.Fatalf("call-site caller = %q, want call", m.Caller)
		}
	}
	if kinds["definition"] != 1 || kinds["call-site"] != 1 {
		t.Fatalf("want 1 definition + 1 call-site, got %+v", res.Matches)
	}
}

func TestDossier(t *testing.T) {
	svcs := newTestServices(t)
	dir := writeDir(t, map[string]string{"a.go": srcB})
	rep, err := svcs.File.Dossier("", filepath.Join(dir, "a.go"))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Language != "go" || rep.Metrics == nil {
		t.Fatalf("report head: %+v", rep)
	}
	if len(rep.Complexity) != 1 || rep.Complexity[0].Name != "Bar" {
		t.Fatalf("complexity: %+v", rep.Complexity)
	}
	if rep.CallGraph == nil {
		t.Fatal("call_graph must be non-null")
	}
}

func TestPruneGroups(t *testing.T) {
	files := map[string]map[string][]engine.Symbol{
		"a.go": {"functions": {{Name: "keep"}, {Name: "drop"}}, "imports": {}},
		"b.go": {"types": {{Name: "T"}}},
	}
	pruneGroups(files, kindSet([]string{"Functions"}), "", func(s engine.Symbol) string { return s.Name })
	if _, ok := files["b.go"]; ok {
		t.Fatalf("b.go should be pruned (kind filtered out): %+v", files)
	}
	fns := files["a.go"]["functions"]
	if len(fns) != 2 || fns[0].Name != "keep" || fns[1].Name != "drop" {
		t.Fatalf("functions after kind prune: %+v", fns)
	}
	if _, ok := files["a.go"]["imports"]; ok {
		t.Fatalf("empty imports group should be pruned: %+v", files["a.go"])
	}

	// name filter keeps only exact matches
	pruneGroups(files, nil, "keep", func(s engine.Symbol) string { return s.Name })
	fns = files["a.go"]["functions"]
	if len(fns) != 1 || fns[0].Name != "keep" {
		t.Fatalf("functions after name prune: %+v", fns)
	}

	// name filter with no hits removes the group and the file
	pruneGroups(files, nil, "Nope", func(s engine.Symbol) string { return s.Name })
	if len(files) != 0 {
		t.Fatalf("everything should be pruned, got %+v", files)
	}
}
