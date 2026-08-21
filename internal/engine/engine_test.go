package engine

import (
	"os"
	"path/filepath"
	"testing"

	"mcp-java-ast/internal/lang"
	golanglang "mcp-java-ast/internal/languages/go"
)

func TestScanSymbolsAndAnalyze(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"a.go": `package a

func Foo() int { return 1 }
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

	scanned, errs, err := eng.ScanSymbols(dir, nil)
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
	if text != "func Foo() int { return 1 }\n" {
		t.Fatalf("get_text: %q", text)
	}

	// symbols with include_text must carry the full function body
	full, err := eng.SymbolsText(golanglang.Go{}, filepath.Join(dir, "a.go"), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(full["functions"]) != 1 || full["functions"][0].Text != "func Foo() int { return 1 }" {
		t.Fatalf("include_text functions: %+v", full["functions"])
	}
}
