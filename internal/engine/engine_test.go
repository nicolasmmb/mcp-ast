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

	scanned, errs, err := eng.ScanSymbols(context.Background(), dir, nil)
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

	variables, errs, err := eng.ScanVariables(context.Background(), dir, golanglang.Go{})
	if err != nil {
		t.Fatal(err)
	}
	if len(errs) != 0 {
		t.Fatalf("unexpected variable scan errors: %v", errs)
	}
	if len(variables) != 1 {
		t.Fatalf("want 1 file with variables (a.go), got %d", len(variables))
	}
	if !containsName(variables[filepath.Join(dir, "a.go")], "x") {
		t.Fatalf("a.go variables: %+v", variables[filepath.Join(dir, "a.go")])
	}
}

func containsName(syms []Symbol, name string) bool {
	for _, s := range syms {
		if s.Name == name {
			return true
		}
	}
	return false
}

func TestSearchName(t *testing.T) {
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

	// find declarations of Foo
	result, err := eng.SearchName(context.Background(), dir, "Foo", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 1 {
		t.Fatalf("want 1 match for Foo, got %d", result.Total)
	}
	m := result.Matches[0]
	if m.File != filepath.Join(dir, "a.go") || m.Kind != "functions" || m.Line != 3 {
		t.Fatalf("unexpected match: %+v", m)
	}

	// a name that only appears as a type declaration
	result, err = eng.SearchName(context.Background(), dir, "T", golanglang.Go{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 1 || result.Matches[0].Kind != "types" {
		t.Fatalf("want 1 type match for T, got %+v", result.Matches)
	}

	// limit caps results
	result, err = eng.SearchName(context.Background(), dir, "Foo", nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 1 {
		t.Fatalf("want 1 match with limit=1, got %d", result.Total)
	}

	// undefined name -> 0 matches, not null
	result, err = eng.SearchName(context.Background(), dir, "Nope", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 0 || result.Matches == nil {
		t.Fatalf("want 0 matches non-null, got %+v", result)
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

func TestRenamePreview(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"a.go": `package a

func Foo() int { return 1 }

func call() int { return Foo() + 1 }
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

	matches, _, err := eng.RenamePreview(context.Background(), dir, "Foo", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 2 {
		t.Fatalf("want 2 matches for Foo (def + use), got %d: %+v", len(matches), matches)
	}
	defs, uses := 0, 0
	for _, m := range matches {
		if m.Definition {
			defs++
		} else {
			uses++
		}
	}
	if defs != 1 || uses != 1 {
		t.Fatalf("want 1 definition + 1 use, got %d defs %d uses", defs, uses)
	}
}

func TestRenamePreviewNoMatch(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n\nfunc Foo() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := lang.NewRegistry()
	if err := reg.Register(golanglang.Go{}); err != nil {
		t.Fatal(err)
	}
	eng := New(reg)

	matches, _, err := eng.RenamePreview(context.Background(), dir, "NaoExiste", nil, 0)
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
	_, _, err := eng.ScanSymbols(ctx, dir, nil)
	if err != context.Canceled {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}
