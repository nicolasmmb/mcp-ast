package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mcp-ast/internal/engine"
	"mcp-ast/internal/lang"
	golanglang "mcp-ast/internal/languages/go"
)

// Fixture shared by tool-contract tests. Designed so every assertion is
// deterministic: exact symbol names, exact call counts, exact classifications.
//
// layout:
//
//	main.go  — entry + uses helper twice; declares dead UnusedFn
//	util.go  — helper definition + type Util
//	vendor/ignored.go — must be invisible to directory tools (heavy dir skip)
const (
	fixtureMain = `package app

import "fmt"

func Main() {
	_ = Helper()
	_ = Helper()
	fmt.Println("ok")
}

func UnusedFn() int { return 42 }
`

	fixtureUtil = `package app

type Util struct{}

func Helper() int { return 1 }

func (u Util) Method() int { return Helper() }
`

	fixtureVendor = `package ignored

func ShouldNeverAppear() {}
`
)

func writeFixture(t *testing.T) (dir string, mainPath, utilPath string) {
	t.Helper()
	dir = t.TempDir()
	mainPath = filepath.Join(dir, "main.go")
	utilPath = filepath.Join(dir, "util.go")
	if err := os.WriteFile(mainPath, []byte(fixtureMain), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(utilPath, []byte(fixtureUtil), 0o644); err != nil {
		t.Fatal(err)
	}
	vendor := filepath.Join(dir, "vendor")
	if err := os.MkdirAll(vendor, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vendor, "ignored.go"), []byte(fixtureVendor), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, mainPath, utilPath
}

func testServices(t *testing.T) *Services {
	t.Helper()
	reg := lang.NewRegistry()
	if err := reg.Register(golanglang.Go{}); err != nil {
		t.Fatal(err)
	}
	return New(engine.New(reg))
}

// ---------------------------------------------------------------------------
// 1. list_languages  → Engine.ListLanguages
// ---------------------------------------------------------------------------

func TestTool_ListLanguages(t *testing.T) {
	svcs := testServices(t)
	langs := svcs.Engine.ListLanguages()
	if len(langs) != 1 || langs[0] != "go" {
		t.Fatalf("list_languages: want [go], got %v", langs)
	}
}

// ---------------------------------------------------------------------------
// 2. parse_ast_file  → Engine.Parse
// ---------------------------------------------------------------------------

func TestTool_ParseASTFile(t *testing.T) {
	svcs := testServices(t)
	_, mainPath, _ := writeFixture(t)

	// Auto-detect language from extension; depth 3 keeps output small.
	root, hasErr, err := svcs.Engine.Parse(mustResolve(t, svcs, "", mainPath), mainPath, 3)
	if err != nil {
		t.Fatal(err)
	}
	if hasErr {
		t.Fatal("parse_ast_file: unexpected ERROR nodes in valid Go source")
	}
	if root == nil || root.Type == "" {
		t.Fatal("parse_ast_file: empty AST root")
	}
	// Source file root is always "source_file" for Go.
	if root.Type != "source_file" {
		t.Fatalf("parse_ast_file: root type = %q, want source_file", root.Type)
	}
	if len(root.Children) == 0 {
		t.Fatal("parse_ast_file: root has no children")
	}

	// max_depth=1 must truncate children of children.
	shallow, _, err := svcs.Engine.Parse(mustResolve(t, svcs, "go", mainPath), mainPath, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, ch := range shallow.Children {
		if len(ch.Children) != 0 {
			t.Fatalf("parse_ast_file max_depth=1: child %q still has children", ch.Type)
		}
	}
}

// ---------------------------------------------------------------------------
// 3. query_ast_file  → Engine.QueryText
// ---------------------------------------------------------------------------

func TestTool_QueryASTFile(t *testing.T) {
	svcs := testServices(t)
	_, mainPath, _ := writeFixture(t)
	l := mustResolve(t, svcs, "go", mainPath)

	// All function declarations by name.
	matches, err := svcs.Engine.QueryText(l, mainPath,
		`(function_declaration name: (identifier) @name) @fn`, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	names := captureNames(matches, "name")
	if !containsAll(names, "Main", "UnusedFn") {
		t.Fatalf("query_ast_file: want Main+UnusedFn, got %v", names)
	}

	// limit=1 must cap.
	limited, err := svcs.Engine.QueryText(l, mainPath,
		`(function_declaration name: (identifier) @name)`, 1, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 1 {
		t.Fatalf("query_ast_file limit=1: got %d matches", len(limited))
	}

	// Invalid query must error.
	_, err = svcs.Engine.QueryText(l, mainPath, `(not_a_real_node) @x`, 0, false)
	if err == nil {
		t.Fatal("query_ast_file: expected error for invalid query")
	}
}

// ---------------------------------------------------------------------------
// 4. symbols_file  → File.Symbols
// ---------------------------------------------------------------------------

func TestTool_SymbolsFile(t *testing.T) {
	svcs := testServices(t)
	_, _, utilPath := writeFixture(t)

	res, err := svcs.File.Symbols("go", utilPath, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Language != "go" {
		t.Fatalf("symbols_file language = %q", res.Language)
	}
	fns := res.Symbols["functions"]
	if len(fns) != 1 || fns[0].Name != "Helper" {
		t.Fatalf("symbols_file functions: %+v", fns)
	}
	methods := res.Symbols["methods"]
	if len(methods) != 1 || methods[0].Name != "Method" {
		t.Fatalf("symbols_file methods: %+v", methods)
	}
	types := res.Symbols["types"]
	if len(types) != 1 || types[0].Name != "Util" {
		t.Fatalf("symbols_file types: %+v", types)
	}
}

// ---------------------------------------------------------------------------
// 5. analyze_file  → File.Dossier
// ---------------------------------------------------------------------------

func TestTool_AnalyzeFile(t *testing.T) {
	svcs := testServices(t)
	_, mainPath, utilPath := writeFixture(t)

	rep, err := svcs.File.Dossier("", utilPath)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Language != "go" {
		t.Fatalf("analyze_file language = %q", rep.Language)
	}
	if rep.Metrics == nil || rep.Metrics.Lines < 3 || rep.Metrics.Nodes == 0 {
		t.Fatalf("analyze_file metrics: %+v", rep.Metrics)
	}
	// Helper + Method
	if len(rep.Complexity) != 2 {
		t.Fatalf("analyze_file complexity entries: %+v", rep.Complexity)
	}
	for _, c := range rep.Complexity {
		if c.Complexity < 1 {
			t.Fatalf("analyze_file: complexity < 1 for %s", c.Name)
		}
	}
	// Call graph: Method calls Helper once; Helper calls nothing.
	byName := map[string]engine.CallEntry{}
	for _, e := range rep.CallGraph {
		byName[e.Name] = e
	}
	if helper, ok := byName["Helper"]; !ok || len(helper.Callees) != 0 {
		t.Fatalf("analyze_file call_graph Helper: %+v", byName["Helper"])
	}
	method, ok := byName["Method"]
	if !ok || len(method.Callees) != 1 || method.Callees[0].Name != "Helper" || method.Callees[0].Count != 1 {
		t.Fatalf("analyze_file call_graph Method: %+v", method)
	}

	// Main calls Helper twice (via identifier, not selector — matches Go call query).
	mainRep, err := svcs.File.Dossier("go", mainPath)
	if err != nil {
		t.Fatal(err)
	}
	byName = map[string]engine.CallEntry{}
	for _, e := range mainRep.CallGraph {
		byName[e.Name] = e
	}
	main, ok := byName["Main"]
	if !ok {
		t.Fatalf("analyze_file: Main missing from call_graph: %+v", mainRep.CallGraph)
	}
	// fmt.Println is a selector_expression — current Go call query may miss it.
	// Helper() x2 must be present.
	foundHelper := false
	for _, c := range main.Callees {
		if c.Name == "Helper" {
			foundHelper = true
			if c.Count != 2 {
				t.Fatalf("analyze_file: Main→Helper count = %d, want 2", c.Count)
			}
		}
	}
	if !foundHelper {
		t.Fatalf("analyze_file: Main should call Helper x2, got %+v", main.Callees)
	}
}

// ---------------------------------------------------------------------------
// 6. get_text_file  → Engine.GetText
// ---------------------------------------------------------------------------

func TestTool_GetTextFile(t *testing.T) {
	svcs := testServices(t)
	_, mainPath, _ := writeFixture(t)
	l := mustResolve(t, svcs, "go", mainPath)

	// Extract the UnusedFn signature line via known positions from symbols.
	syms, err := svcs.Engine.SymbolsText(l, mainPath, true)
	if err != nil {
		t.Fatal(err)
	}
	var unused engine.Symbol
	for _, s := range syms["functions"] {
		if s.Name == "UnusedFn" {
			unused = s
			break
		}
	}
	if unused.Name == "" {
		t.Fatal("get_text_file setup: UnusedFn not found")
	}
	text, err := svcs.Engine.GetText(l, mainPath, unused.Start, unused.End)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "func UnusedFn()") {
		t.Fatalf("get_text_file: got %q, want body containing func UnusedFn()", text)
	}

	// Inverted range must error.
	_, err = svcs.Engine.GetText(l, mainPath, engine.Point{Row: 10, Col: 0}, engine.Point{Row: 0, Col: 0})
	if err == nil {
		t.Fatal("get_text_file: expected error for inverted range")
	}
}

// ---------------------------------------------------------------------------
// 7. scan_symbols_dir  → Scan.Dir
// ---------------------------------------------------------------------------

func TestTool_ScanSymbolsDir(t *testing.T) {
	svcs := testServices(t)
	dir, mainPath, utilPath := writeFixture(t)

	// Full scan — vendor/ must be skipped.
	res, err := svcs.Scan.Dir(context.Background(), ScanQuery{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != 2 {
		t.Fatalf("scan_symbols_dir: want 2 files (vendor skipped), got %d: %v", len(res.Files), fileKeys(res.Files))
	}
	if _, ok := res.Files[filepath.Join(dir, "vendor", "ignored.go")]; ok {
		t.Fatal("scan_symbols_dir: vendor/ignored.go must not appear")
	}

	// Filter by name.
	byName, err := svcs.Scan.Dir(context.Background(), ScanQuery{Dir: dir, Name: "Helper"})
	if err != nil {
		t.Fatal(err)
	}
	if len(byName.Files) != 1 {
		t.Fatalf("scan_symbols_dir name=Helper: want 1 file, got %v", fileKeys(byName.Files))
	}
	fns := byName.Files[utilPath]["functions"]
	if len(fns) != 1 || fns[0].Name != "Helper" {
		t.Fatalf("scan_symbols_dir name=Helper symbols: %+v", byName.Files)
	}

	// Filter by kind=methods only → only util.go Method.
	byKind, err := svcs.Scan.Dir(context.Background(), ScanQuery{Dir: dir, Kinds: []string{"methods"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(byKind.Files) != 1 || len(byKind.Files[utilPath]["methods"]) != 1 {
		t.Fatalf("scan_symbols_dir kinds=methods: %+v", byKind.Files)
	}

	// limit=1 caps files.
	limited, err := svcs.Scan.Dir(context.Background(), ScanQuery{Dir: dir, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(limited.Files) != 1 {
		t.Fatalf("scan_symbols_dir limit=1: got %d files", len(limited.Files))
	}

	// main.go must still be discoverable when not filtered.
	if _, ok := res.Files[mainPath]; !ok {
		t.Fatalf("scan_symbols_dir: missing main.go in %v", fileKeys(res.Files))
	}
}

// ---------------------------------------------------------------------------
// 8. unused_symbols_dir  → Unused.Dir
// ---------------------------------------------------------------------------

func TestTool_UnusedSymbolsDir(t *testing.T) {
	svcs := testServices(t)
	dir, _, _ := writeFixture(t)

	res, err := svcs.Unused.Dir(context.Background(), dir, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, s := range res.Symbols {
		names[s.Name] = true
	}
	// UnusedFn appears only once in the tree → heuristic flags it.
	if !names["UnusedFn"] {
		t.Fatalf("unused_symbols_dir: expected UnusedFn, got %+v", res.Symbols)
	}
	// Helper is referenced from Main and Method → must NOT be unused.
	if names["Helper"] {
		t.Fatalf("unused_symbols_dir: Helper must not be flagged, got %+v", res.Symbols)
	}
	// ShouldNeverAppear lives under vendor/ and must not surface.
	if names["ShouldNeverAppear"] {
		t.Fatal("unused_symbols_dir: vendor symbol leaked")
	}
}

// ---------------------------------------------------------------------------
// 9. usages_dir  → Usages.Dir
// ---------------------------------------------------------------------------

func TestTool_UsagesDir(t *testing.T) {
	svcs := testServices(t)
	dir, _, _ := writeFixture(t)

	res, err := svcs.Usages.Dir(context.Background(), "Helper", dir, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	var callers []string
	for _, m := range res.Matches {
		kinds[m.Kind]++
		if m.Kind == "call-site" {
			callers = append(callers, m.Caller)
		}
	}
	// 1 definition in util.go
	if kinds["definition"] != 1 {
		t.Fatalf("usages_dir: want 1 definition, got %+v (matches=%+v)", kinds, res.Matches)
	}
	// call-sites: Main x2 + Method x1 = 3
	if kinds["call-site"] != 3 {
		t.Fatalf("usages_dir: want 3 call-sites, got %+v (matches=%+v)", kinds, res.Matches)
	}
	if !containsAll(callers, "Main", "Method") {
		t.Fatalf("usages_dir callers = %v, want Main and Method", callers)
	}

	// limit=2 caps.
	limited, err := svcs.Usages.Dir(context.Background(), "Helper", dir, nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(limited.Matches) != 2 {
		t.Fatalf("usages_dir limit=2: got %d", len(limited.Matches))
	}
}

// ---------------------------------------------------------------------------
// 10. callers_dir  → Calls.Callers
// ---------------------------------------------------------------------------

func TestTool_CallersDir(t *testing.T) {
	svcs := testServices(t)
	dir, _, _ := writeFixture(t)

	res, err := svcs.Calls.Callers(context.Background(), "Helper", dir, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]int{}
	for _, c := range res.Callers {
		byName[c.Name] = c.Count
	}
	// Main calls Helper twice, Method once.
	if byName["Main"] != 2 {
		t.Fatalf("callers_dir: Main count = %d, want 2 (callers=%+v)", byName["Main"], res.Callers)
	}
	if byName["Method"] != 1 {
		t.Fatalf("callers_dir: Method count = %d, want 1 (callers=%+v)", byName["Method"], res.Callers)
	}
	if len(res.Callers) != 2 {
		t.Fatalf("callers_dir: want 2 callers, got %+v", res.Callers)
	}

	// Unknown target → empty list.
	none, err := svcs.Calls.Callers(context.Background(), "NoSuchFn", dir, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(none.Callers) != 0 {
		t.Fatalf("callers_dir: want 0 callers for NoSuchFn, got %+v", none.Callers)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func mustResolve(t *testing.T, svcs *Services, name, path string) lang.Language {
	t.Helper()
	l, err := svcs.Engine.Resolve(name, path)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func captureNames(matches []engine.Match, capture string) []string {
	var out []string
	for _, m := range matches {
		for _, c := range m.Captures {
			if c.Name == capture {
				out = append(out, c.Text)
			}
		}
	}
	return out
}

func containsAll(have []string, want ...string) bool {
	set := map[string]bool{}
	for _, h := range have {
		set[h] = true
	}
	for _, w := range want {
		if !set[w] {
			return false
		}
	}
	return true
}

func fileKeys(m map[string]map[string][]engine.Symbol) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
