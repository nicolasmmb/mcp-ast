package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"mcp-ast/internal/lang"
	golanglang "mcp-ast/internal/languages/go"
)

func TestScanSymbolsAndAnalyze(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"a.go": `package a

func Foo() int { x := 1; return x }
`,
		"b.go": `package b

type T struct{}

func (t T) Bar() {}
`,
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	reg := lang.NewRegistry()
	if err := reg.Register(golanglang.Go{}); err != nil {
		t.Fatal(err)
	}
	eng := New(reg)

	scanned, errs, err := eng.ScanSymbols(context.Background(), dir, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(errs) != 0 {
		t.Fatalf("unexpected scan errors: %v", errs)
	}
	if len(scanned) != 2 {
		t.Fatalf("want 2 files scanned, got %d", len(scanned))
	}

	m, err := eng.Analyze(golanglang.Go{}, filepath.Join(dir, "a.go"))
	if err != nil {
		t.Fatal(err)
	}
	if m.Lines != 4 || m.Bytes == 0 || m.Nodes == 0 || m.MaxNesting == 0 {
		t.Fatalf("metrics: %+v", m)
	}
	fn, ok := m.Kinds["functions"]
	if !ok || fn.Count != 1 || fn.MaxLines != 1 {
		t.Fatalf("functions metric: %+v", m.Kinds)
	}

	empty, err := eng.Query(golanglang.Go{}, filepath.Join(dir, "a.go"), `(decorated_definition) @x`)
	if err == nil || len(empty) != 0 {
		t.Fatalf("expected invalid-query error, got %v %v", empty, err)
	}
	matches, err := eng.QueryLimit(golanglang.Go{}, filepath.Join(dir, "a.go"), `(method_declaration) @m`, 5)
	if err != nil {
		t.Fatal(err)
	}
	if matches == nil {
		t.Fatal("matches must be a non-nil empty slice, not null")
	}
	if len(matches) != 0 {
		t.Fatalf("want 0 matches, got %d", len(matches))
	}

	// limit must actually cap results (a.go has many identifiers)
	limited, err := eng.QueryLimit(golanglang.Go{}, filepath.Join(dir, "a.go"), `(identifier) @i`, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 1 {
		t.Fatalf("want 1 match with limit=1, got %d", len(limited))
	}

	text, err := eng.GetText(golanglang.Go{}, filepath.Join(dir, "a.go"), Point{Row: 2, Col: 0}, Point{Row: 3, Col: 0})
	if err != nil {
		t.Fatal(err)
	}
	if text != "func Foo() int { x := 1; return x }\n" {
		t.Fatalf("get_text: %q", text)
	}

	// symbols with include_text must carry the full function body
	full, err := eng.SymbolsText(golanglang.Go{}, filepath.Join(dir, "a.go"), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(full["functions"]) != 1 || full["functions"][0].Text != "func Foo() int { x := 1; return x }" {
		t.Fatalf("include_text functions: %+v", full["functions"])
	}

	// scan with include_text must carry the full body too
	fullScan, errs, err := eng.ScanSymbols(context.Background(), dir, nil, true)
	if err != nil || len(errs) != 0 {
		t.Fatalf("scan errors: %v %v", errs, err)
	}
	syms := fullScan[filepath.Join(dir, "a.go")]["functions"]
	if len(syms) != 1 || syms[0].Text != "func Foo() int { x := 1; return x }" {
		t.Fatalf("scan include_text: %+v", syms)
	}
}

func TestDossier(t *testing.T) {
	dir := t.TempDir()
	src := `package a

func helper() int { return 1 }

func caller(x int) int {
	if helper() > 0 {
		return helper() + x
	}
	return 0
}
`
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := lang.NewRegistry()
	if err := reg.Register(golanglang.Go{}); err != nil {
		t.Fatal(err)
	}
	eng := New(reg)

	report, err := eng.Dossier(golanglang.Go{}, filepath.Join(dir, "a.go"))
	if err != nil {
		t.Fatal(err)
	}
	if report.Language != "go" || report.Metrics == nil || report.Metrics.Lines == 0 {
		t.Fatalf("dossier metrics: %+v", report)
	}
	if len(report.Complexity) != 2 {
		t.Fatalf("want 2 complexity entries, got %+v", report.Complexity)
	}
	byName := map[string]CallEntry{}
	for _, f := range report.CallGraph {
		byName[f.Name] = f
	}
	caller := byName["caller"]
	if len(caller.Callees) != 1 || caller.Callees[0].Name != "helper" || caller.Callees[0].Count != 2 {
		t.Fatalf("dossier call graph: %+v", report.CallGraph)
	}
}

func TestUsages(t *testing.T) {
	dir := t.TempDir()
	src := `package a

func Foo() int { return 1 }

func call() int { return Foo() + 1 }

func mention() string { return "Foo" }
`
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := lang.NewRegistry()
	if err := reg.Register(golanglang.Go{}); err != nil {
		t.Fatal(err)
	}
	eng := New(reg)

	matches, errs, err := eng.Usages(context.Background(), dir, "Foo", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(errs) != 0 {
		t.Fatalf("unexpected usages errors: %v", errs)
	}
	kinds := map[string][]UsageMatch{}
	for _, m := range matches {
		kinds[m.Kind] = append(kinds[m.Kind], m)
	}
	if len(kinds["definition"]) != 1 {
		t.Fatalf("want 1 definition of Foo, got %+v", matches)
	}
	if len(kinds["call-site"]) != 1 || kinds["call-site"][0].Caller != "call" {
		t.Fatalf(`want 1 call-site with caller "call", got %+v`, matches)
	}
	// the string literal "Foo" must not appear as an identifier match
	total := len(kinds["definition"]) + len(kinds["call-site"]) + len(kinds["reference"])
	if total != len(matches) || len(matches) != 2 {
		t.Fatalf("unexpected matches: %+v", matches)
	}

	// limit caps results
	limited, _, err := eng.Usages(context.Background(), dir, "Foo", nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 1 {
		t.Fatalf("want 1 match with limit=1, got %d", len(limited))
	}
}

func TestUsagesNoMatch(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n\nfunc Foo() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := lang.NewRegistry()
	if err := reg.Register(golanglang.Go{}); err != nil {
		t.Fatal(err)
	}
	eng := New(reg)

	matches, _, err := eng.Usages(context.Background(), dir, "NaoExiste", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if matches == nil {
		t.Fatal("matches must be a non-nil empty slice, not null")
	}
	if len(matches) != 0 {
		t.Fatalf("want 0 matches, got %d", len(matches))
	}
}

func TestComplexity(t *testing.T) {
	dir := t.TempDir()
	src := `package a

func Simple() int { return 1 }

func Branches(x int) int {
	if x > 0 && x < 10 {
		return 1
	}
	if x > 100 || x < -100 {
		return 2
	}
	for i := 0; i < x; i++ {
		select {}
	}
	switch x {
	case 1:
		return 3
	default:
		return 4
	}
}
`
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := lang.NewRegistry()
	if err := reg.Register(golanglang.Go{}); err != nil {
		t.Fatal(err)
	}
	eng := New(reg)

	entries, err := eng.Complexity(golanglang.Go{}, filepath.Join(dir, "a.go"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("want 2 functions, got %d: %+v", len(entries), entries)
	}
	byName := map[string]int{}
	for _, en := range entries {
		byName[en.Name] = en.Complexity
	}
	if byName["Simple"] != 1 {
		t.Fatalf("Simple complexity = %d, want 1", byName["Simple"])
	}
	// Branches: 2 if + 1 logical && + 1 logical || + 1 for + 1 select + 1 switch + 1 case + 1 default + base 1 = 10
	if byName["Branches"] != 10 {
		t.Fatalf("Branches complexity = %d, want 10", byName["Branches"])
	}
}

func TestUnusedSymbols(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"a.go": `package a

func Used() int { return 1 }

func Unused() int { return 2 }
`,
		"b.go": `package b

func caller() int { return a.Used() }
`,
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	reg := lang.NewRegistry()
	if err := reg.Register(golanglang.Go{}); err != nil {
		t.Fatal(err)
	}
	eng := New(reg)

	result, err := eng.UnusedSymbols(context.Background(), dir, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	unused := map[string]bool{}
	for _, m := range result.Matches {
		unused[m.Name] = true
	}
	if !unused["Unused"] {
		t.Fatalf("Unused should be flagged, got %+v", result.Matches)
	}
	if unused["Used"] {
		t.Fatalf("Used should NOT be flagged, got %+v", result.Matches)
	}
}

func TestCallGraph(t *testing.T) {
	dir := t.TempDir()
	src := `package a

func helper() int { return 1 }

func caller(x int) int {
	if helper() > 0 {
		return helper() + x
	}
	return 0
}

func main() {
	_ = caller(1)
}
`
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := lang.NewRegistry()
	if err := reg.Register(golanglang.Go{}); err != nil {
		t.Fatal(err)
	}
	eng := New(reg)

	graph, err := eng.CallGraph(golanglang.Go{}, filepath.Join(dir, "a.go"))
	if err != nil {
		t.Fatal(err)
	}
	if len(graph) != 3 {
		t.Fatalf("want 3 functions, got %d: %+v", len(graph), graph)
	}
	byName := map[string]CallEntry{}
	for _, f := range graph {
		byName[f.Name] = f
	}
	helper := byName["helper"]
	if len(helper.Callees) != 0 {
		t.Fatalf("helper should call nothing, got %+v", helper.Callees)
	}
	caller := byName["caller"]
	if len(caller.Callees) != 1 || caller.Callees[0].Name != "helper" || caller.Callees[0].Count != 2 {
		t.Fatalf("caller should call helper x2, got %+v", caller.Callees)
	}
}

func TestCallers(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"a.go": `package a

func helper() int { return 1 }

func alpha() int { return helper() }

func beta(x int) int {
	return helper() + helper() + x
}
`,
		"b.go": `package b

func gamma() int { return 0 }
`,
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	reg := lang.NewRegistry()
	if err := reg.Register(golanglang.Go{}); err != nil {
		t.Fatal(err)
	}
	eng := New(reg)

	callers, _, err := eng.Callers(context.Background(), dir, "helper", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(callers) != 2 {
		t.Fatalf("want 2 callers of helper (alpha, beta), got %+v", callers)
	}
	byName := map[string]int{}
	for _, c := range callers {
		byName[c.Name] = c.Count
	}
	if byName["alpha"] != 1 {
		t.Fatalf("alpha should call helper 1x, got %d", byName["alpha"])
	}
	if byName["beta"] != 2 {
		t.Fatalf("beta should call helper 2x, got %d", byName["beta"])
	}

	// no callers -> empty non-nil slice
	callers, _, err = eng.Callers(context.Background(), dir, "gamma", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(callers) != 0 {
		t.Fatalf("gamma should have 0 callers, got %+v", callers)
	}
}

func TestScanCancelled(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\nfunc Foo() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := lang.NewRegistry()
	if err := reg.Register(golanglang.Go{}); err != nil {
		t.Fatal(err)
	}
	eng := New(reg)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := eng.ScanSymbols(ctx, dir, nil, false)
	if err != context.Canceled {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

func TestWalkFilesSkipsHeavyDirs(t *testing.T) {
	dir := t.TempDir()

	// Source under the root should be scanned.
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc Main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	heavy := []string{"node_modules", "vendor", "target", "dist", "build", "__pycache__", ".venv", "venv"}
	for _, name := range heavy {
		sub := filepath.Join(dir, name)
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sub, "ignored.go"), []byte("package ignored\n\nfunc ShouldNotAppear() {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Nested heavy dir under a normal package must also be skipped.
	pkg := filepath.Join(dir, "pkg")
	if err := os.MkdirAll(filepath.Join(pkg, "vendor"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "lib.go"), []byte("package pkg\n\nfunc Lib() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "vendor", "dep.go"), []byte("package dep\n\nfunc Dep() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := lang.NewRegistry()
	if err := reg.Register(golanglang.Go{}); err != nil {
		t.Fatal(err)
	}
	eng := New(reg)

	scanned, errs, err := eng.ScanSymbols(context.Background(), dir, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(errs) != 0 {
		t.Fatalf("unexpected scan errors: %v", errs)
	}
	if len(scanned) != 2 {
		t.Fatalf("want 2 source files (main.go, pkg/lib.go), got %d: %v", len(scanned), keysOf(scanned))
	}
	for path := range scanned {
		base := filepath.Base(filepath.Dir(path))
		if _, heavy := skipDirNames[base]; heavy || stringsHasHeavyParent(path) {
			t.Fatalf("scanned file inside skipped dir: %s", path)
		}
	}
}

func keysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func stringsHasHeavyParent(path string) bool {
	parts := stringsSplitPath(path)
	for _, p := range parts {
		if _, ok := skipDirNames[p]; ok {
			return true
		}
	}
	return false
}

func stringsSplitPath(path string) []string {
	var parts []string
	for _, p := range filepath.SplitList(path) {
		_ = p
	}
	// Split on OS separator without importing strings solely for this helper
	// in a way that would require another import change in older tests.
	cur := path
	for {
		dir, file := filepath.Split(cur)
		if file != "" {
			parts = append(parts, file)
		}
		if dir == "" || dir == cur {
			break
		}
		cur = filepath.Clean(dir)
		if cur == "." || cur == string(filepath.Separator) {
			break
		}
	}
	return parts
}
