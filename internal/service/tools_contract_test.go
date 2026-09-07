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
//	main.go  - entry + uses helper twice; declares dead UnusedFn
//	util.go  - helper definition + type Util
//	vendor/ignored.go - must be invisible to directory tools (heavy dir skip)
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

func TestContract_ListLanguages(t *testing.T) {
	svcs := testServices(t)
	langs := svcs.Engine.ListLanguages()
	if len(langs) != 1 || langs[0] != "go" {
		t.Fatalf("list_languages: want [go], got %v", langs)
	}
}

func TestContract_ParseAst(t *testing.T) {
	svcs := testServices(t)
	_, mainPath, _ := writeFixture(t)
	root, hasErr, err := svcs.Engine.Parse(mustResolve(t, svcs, "", mainPath), mainPath, 3)
	if err != nil {
		t.Fatal(err)
	}
	if hasErr {
		t.Fatal("parse_ast: unexpected ERROR nodes")
	}
	if root == nil || root.Type == "" {
		t.Fatal("parse_ast: empty AST root")
	}
	if root.Type != "source_file" {
		t.Fatalf("parse_ast: root type = %q", root.Type)
	}
	shallow, _, err := svcs.Engine.Parse(mustResolve(t, svcs, "go", mainPath), mainPath, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, ch := range shallow.Children {
		if len(ch.Children) != 0 {
			t.Fatalf("parse_ast max_depth=1: child %q still has children", ch.Type)
		}
	}
}

func TestContract_QueryAst(t *testing.T) {
	svcs := testServices(t)
	_, mainPath, _ := writeFixture(t)
	l := mustResolve(t, svcs, "go", mainPath)
	matches, err := svcs.Engine.QueryText(l, mainPath,
		`(function_declaration name: (identifier) @name) @fn`, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	names := captureNames(matches, "name")
	if !containsAll(names, "Main", "UnusedFn") {
		t.Fatalf("query_ast: want Main+UnusedFn, got %v", names)
	}
	limited, err := svcs.Engine.QueryText(l, mainPath,
		`(function_declaration name: (identifier) @name)`, 1, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 1 {
		t.Fatalf("query_ast limit=1: got %d", len(limited))
	}
	_, err = svcs.Engine.QueryText(l, mainPath, `(not_a_real_node) @x`, 0, false)
	if err == nil {
		t.Fatal("query_ast: expected error for invalid query")
	}
}

func TestContract_ScanSymbols_File(t *testing.T) {
	svcs := testServices(t)
	_, _, utilPath := writeFixture(t)
	res, err := svcs.Scan.Path("go", utilPath, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Language != "go" {
		t.Fatalf("scan_symbols file language = %q", res.Language)
	}
	fns := res.Files[utilPath]["functions"]
	if len(fns) != 1 || fns[0].Name != "Helper" {
		t.Fatalf("scan_symbols file functions: %+v", res.Files)
	}
	methods := res.Files[utilPath]["methods"]
	if len(methods) != 1 || methods[0].Name != "Method" {
		t.Fatalf("scan_symbols file methods: %+v", methods)
	}
}

func TestContract_AnalyzeFile(t *testing.T) {
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
	if len(rep.Complexity) != 2 {
		t.Fatalf("analyze_file complexity: %+v", rep.Complexity)
	}
	byName := map[string]engine.CallEntry{}
	for _, e := range rep.CallGraph {
		byName[e.Name] = e
	}
	if helper, ok := byName["Helper"]; !ok || len(helper.Callees) != 0 {
		t.Fatalf("call_graph Helper: %+v", byName["Helper"])
	}
	method, ok := byName["Method"]
	if !ok || len(method.Callees) != 1 || method.Callees[0].Name != "Helper" {
		t.Fatalf("call_graph Method: %+v", method)
	}
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
		t.Fatalf("Main missing: %+v", mainRep.CallGraph)
	}
	found := false
	for _, c := range main.Callees {
		if c.Name == "Helper" {
			found = true
			if c.Count != 2 {
				t.Fatalf("Main->Helper count = %d", c.Count)
			}
		}
	}
	if !found {
		t.Fatalf("Main should call Helper x2: %+v", main.Callees)
	}
}

func TestContract_GetText(t *testing.T) {
	svcs := testServices(t)
	_, mainPath, _ := writeFixture(t)
	l := mustResolve(t, svcs, "go", mainPath)
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
		t.Fatal("UnusedFn not found")
	}
	text, err := svcs.Engine.GetText(l, mainPath, unused.Start, unused.End)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "func UnusedFn()") {
		t.Fatalf("get_text: got %q", text)
	}
	_, err = svcs.Engine.GetText(l, mainPath, engine.Point{Row: 10, Col: 0}, engine.Point{Row: 0, Col: 0})
	if err == nil {
		t.Fatal("expected error for inverted range")
	}
}

func TestContract_ScanSymbols_Dir(t *testing.T) {
	svcs := testServices(t)
	dir, mainPath, utilPath := writeFixture(t)
	res, err := svcs.Scan.Dir(context.Background(), ScanQuery{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) != 2 {
		t.Fatalf("scan_symbols dir: want 2 files, got %d", len(res.Files))
	}
	if _, ok := res.Files[filepath.Join(dir, "vendor", "ignored.go")]; ok {
		t.Fatal("vendor must not appear")
	}
	byName, err := svcs.Scan.Dir(context.Background(), ScanQuery{Dir: dir, Name: "Helper"})
	if err != nil {
		t.Fatal(err)
	}
	if len(byName.Files) != 1 {
		t.Fatalf("name=Helper: got %d files", len(byName.Files))
	}
	fns := byName.Files[utilPath]["functions"]
	if len(fns) != 1 || fns[0].Name != "Helper" {
		t.Fatalf("Helper symbols: %+v", byName.Files)
	}
	byKind, err := svcs.Scan.Dir(context.Background(), ScanQuery{Dir: dir, Kinds: []string{"methods"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(byKind.Files) != 1 || len(byKind.Files[utilPath]["methods"]) != 1 {
		t.Fatalf("kinds=methods: %+v", byKind.Files)
	}
	limited, err := svcs.Scan.Dir(context.Background(), ScanQuery{Dir: dir, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(limited.Files) != 1 {
		t.Fatalf("limit=1: got %d", len(limited.Files))
	}
	if _, ok := res.Files[mainPath]; !ok {
		t.Fatal("missing main.go")
	}
}

func TestContract_FindUsages_Unused(t *testing.T) {
	svcs := testServices(t)
	dir, _, _ := writeFixture(t)
	res, err := svcs.Find.Dir(context.Background(), FindQuery{
		Mode: FindUnused, Dir: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, s := range res.Symbols {
		names[s.Name] = true
	}
	if !names["UnusedFn"] {
		t.Fatalf("expected UnusedFn, got %+v", res.Symbols)
	}
	if names["Helper"] {
		t.Fatalf("Helper must not be unused: %+v", res.Symbols)
	}
	if names["ShouldNeverAppear"] {
		t.Fatal("vendor symbol leaked")
	}
}

func TestContract_FindUsages_Occurrences(t *testing.T) {
	svcs := testServices(t)
	dir, _, _ := writeFixture(t)
	res, err := svcs.Find.Dir(context.Background(), FindQuery{
		Mode: FindOccurrences, Name: "Helper", Dir: dir,
	})
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
	if kinds["definition"] != 1 {
		t.Fatalf("want 1 definition, got %+v", kinds)
	}
	if kinds["call-site"] != 3 {
		t.Fatalf("want 3 call-sites, got %+v", kinds)
	}
	if !containsAll(callers, "Main", "Method") {
		t.Fatalf("callers = %v", callers)
	}
	limited, err := svcs.Find.Dir(context.Background(), FindQuery{
		Mode: FindOccurrences, Name: "Helper", Dir: dir, Limit: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(limited.Matches) != 2 {
		t.Fatalf("limit=2: got %d", len(limited.Matches))
	}
}

func TestContract_FindUsages_Callers(t *testing.T) {
	svcs := testServices(t)
	dir, _, _ := writeFixture(t)
	res, err := svcs.Find.Dir(context.Background(), FindQuery{
		Mode: FindCallers, Name: "Helper", Dir: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]int{}
	for _, c := range res.Callers {
		byName[c.Name] = c.Count
	}
	if byName["Main"] != 2 {
		t.Fatalf("Main count = %d", byName["Main"])
	}
	if byName["Method"] != 1 {
		t.Fatalf("Method count = %d", byName["Method"])
	}
	if len(res.Callers) != 2 {
		t.Fatalf("want 2 callers, got %+v", res.Callers)
	}
	none, err := svcs.Find.Dir(context.Background(), FindQuery{
		Mode: FindCallers, Name: "NoSuchFn", Dir: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(none.Callers) != 0 {
		t.Fatalf("want 0 for NoSuchFn, got %+v", none.Callers)
	}
}

func TestContract_RankComplexity(t *testing.T) {
	svcs := testServices(t)
	dir, _, _ := writeFixture(t)
	res, err := svcs.Rank.ComplexityDir(context.Background(), dir, nil, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) == 0 {
		t.Fatal("expected complexity entries")
	}
}

func TestContract_OutlineFile(t *testing.T) {
	svcs := testServices(t)
	_, _, utilPath := writeFixture(t)
	res, err := svcs.File.Outline("go", utilPath, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Outline) == 0 {
		t.Fatal("empty outline")
	}
}

func TestContract_ScanSymbols_Path(t *testing.T) {
	svcs := testServices(t)
	_, mainPath, _ := writeFixture(t)
	res, err := svcs.Scan.Path("", mainPath, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Language != "go" {
		t.Fatalf("language = %q", res.Language)
	}
}

func TestContract_FindUsages_Definitions(t *testing.T) {
	svcs := testServices(t)
	dir, _, _ := writeFixture(t)
	res, err := svcs.Find.Dir(context.Background(), FindQuery{
		Mode: FindDefinitions, Name: "Helper", Dir: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Matches) == 0 && len(res.Files) == 0 {
		t.Fatal("expected definitions")
	}
}

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
