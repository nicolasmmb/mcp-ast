package service_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"mcp-ast/internal/engine"
	"mcp-ast/internal/lang"
	"mcp-ast/internal/languages/bash"
	"mcp-ast/internal/languages/csharp"
	golanglang "mcp-ast/internal/languages/go"
	"mcp-ast/internal/languages/java"
	"mcp-ast/internal/languages/javascript"
	"mcp-ast/internal/languages/python"
	"mcp-ast/internal/languages/rust"
	"mcp-ast/internal/languages/typescript"
	"mcp-ast/internal/languages/yaml"
	"mcp-ast/internal/service"
)

type langFixture struct {
	impl          lang.Language
	file          string
	primarySymbol string
	callName      string
	idQuery       string
	expectedKinds map[string]string
	expectCallee  string
}

func allFixtures() []langFixture {
	return []langFixture{
		{
			impl: golanglang.Go{}, file: "basic.go", primarySymbol: "Helper", callName: "Helper",
			idQuery: `(identifier) @id`,
			expectedKinds: map[string]string{
				"functions": "Helper", "methods": "Method", "types": "Util",
				"imports": "", "variables": "Counter",
			},
			expectCallee: "Helper",
		},
		{
			impl: java.Java{}, file: "basic.java", primarySymbol: "Greeter", callName: "hello",
			idQuery: `(identifier) @id`,
			expectedKinds: map[string]string{
				"classes": "Greeter", "interfaces": "Named", "enums": "Color", "records": "Point",
				"methods": "hello", "constructors": "Greeter", "fields": "count",
				"imports": "", "variables": "local",
			},
			expectCallee: "hello",
		},
		{
			impl: python.Python{}, file: "basic.py", primarySymbol: "helper", callName: "helper",
			idQuery: `(identifier) @id`,
			expectedKinds: map[string]string{
				"classes": "Greeter", "functions": "helper", "imports": "", "variables": "x",
			},
			expectCallee: "helper",
		},
		{
			impl: javascript.JavaScript{}, file: "basic.js", primarySymbol: "helper", callName: "helper",
			idQuery: `(identifier) @id`,
			expectedKinds: map[string]string{
				"functions": "helper", "classes": "Greeter", "methods": "hello",
				"imports": "", "variables": "counter",
			},
			expectCallee: "helper",
		},
		{
			impl: typescript.TypeScript{}, file: "basic.ts", primarySymbol: "helper", callName: "helper",
			idQuery: `(identifier) @id`,
			expectedKinds: map[string]string{
				"functions": "helper", "classes": "Greeter", "methods": "hello",
				"interfaces": "Named", "types": "ID", "enums": "Color",
				"imports": "", "variables": "counter",
			},
			expectCallee: "helper",
		},
		{
			impl: rust.Rust{}, file: "basic.rs", primarySymbol: "helper", callName: "helper",
			idQuery: `(identifier) @id`,
			expectedKinds: map[string]string{
				"functions": "helper", "structs": "Point", "enums": "Color", "traits": "Drawable",
				"impls": "Drawable", "modules": "utils", "types": "ID",
				"imports": "", "variables": "x",
			},
			expectCallee: "helper",
		},
		{
			impl: csharp.CSharp{}, file: "basic.cs", primarySymbol: "Greeter", callName: "Helper",
			idQuery: `(identifier) @id`,
			expectedKinds: map[string]string{
				"classes": "Greeter", "interfaces": "INamed", "structs": "Point", "enums": "Color",
				"methods": "Helper", "properties": "Label", "fields": "count",
				"constructors": "Greeter", "imports": "", "namespaces": "Demo", "variables": "local",
			},
			expectCallee: "Helper",
		},
		{
			impl: bash.Bash{}, file: "basic.sh", primarySymbol: "helper", callName: "helper",
			idQuery: `(word) @id`,
			expectedKinds: map[string]string{
				"functions": "helper", "variables": "NAME",
			},
			expectCallee: "helper",
		},
		{
			impl: yaml.YAML{}, file: "basic.yaml", primarySymbol: "name", callName: "",
			idQuery: `(string_scalar) @id`,
			expectedKinds: map[string]string{
				"mappings": "name",
			},
			expectCallee: "",
		},
	}
}

func testServices(t *testing.T) *service.Services {
	t.Helper()
	reg := lang.NewRegistry()
	for _, f := range allFixtures() {
		if err := reg.Register(f.impl); err != nil {
			t.Fatalf("register %s: %v", f.impl.Name(), err)
		}
	}
	return service.New(engine.New(reg))
}

func fixturePath(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join("testdata", "matrix", name)
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("fixture %s: %v", p, err)
	}
	return p
}

func hasName(syms []engine.Symbol, want string) bool {
	if want == "" {
		return len(syms) > 0
	}
	for _, s := range syms {
		if s.Name == want {
			return true
		}
	}
	return false
}

func TestMatrix_RegisterAllLanguages(t *testing.T) {
	svcs := testServices(t)
	if len(svcs.Engine.ListLanguages()) != 9 {
		t.Fatalf("want 9 langs, got %v", svcs.Engine.ListLanguages())
	}
}

func TestMatrix_AllSymbolQueries(t *testing.T) {
	svcs := testServices(t)
	for _, f := range allFixtures() {
		t.Run(f.impl.Name(), func(t *testing.T) {
			path := fixturePath(t, f.file)
			res, err := svcs.Scan.Path(f.impl.Name(), path, false)
			if err != nil {
				t.Fatal(err)
			}
			var fileSyms map[string][]engine.Symbol
			for _, m := range res.Files {
				fileSyms = m
			}
			if fileSyms == nil {
				t.Fatal("no symbols map")
			}
			for kind := range f.impl.SymbolQueries() {
				want, ok := f.expectedKinds[kind]
				if !ok {
					t.Fatalf("test table missing expectedKinds for %s/%s", f.impl.Name(), kind)
				}
				items := fileSyms[kind]
				if len(items) == 0 {
					t.Fatalf("symbol query %q returned 0 symbols", kind)
				}
				if want != "" && !hasName(items, want) {
					t.Fatalf("kind %q: want name %q in %+v", kind, want, items)
				}
			}
		})
	}
}

func TestMatrix_AllAuxQueries(t *testing.T) {
	svcs := testServices(t)
	for _, f := range allFixtures() {
		t.Run(f.impl.Name(), func(t *testing.T) {
			path := fixturePath(t, f.file)
			l := f.impl
			idQ, ok := l.AuxQueries()["identifiers"]
			if !ok {
				t.Fatal("missing identifiers")
			}
			matches, err := svcs.Engine.QueryText(l, path, idQ, 50, false)
			if err != nil {
				t.Fatal(err)
			}
			if len(matches) == 0 {
				t.Fatal("identifiers: 0 matches")
			}
			callQ, ok := l.AuxQueries()["calls"]
			if !ok {
				t.Fatal("missing calls")
			}
			cmatches, err := svcs.Engine.QueryText(l, path, callQ, 50, false)
			if err != nil {
				t.Fatal(err)
			}
			if f.expectCallee == "" {
				if len(cmatches) == 0 {
					t.Fatal("calls: 0 matches")
				}
				return
			}
			found := false
			for _, m := range cmatches {
				for _, c := range m.Captures {
					if c.Name == "callee" && c.Text == f.expectCallee {
						found = true
					}
				}
			}
			if !found {
				t.Fatalf("calls: want callee %q in %+v", f.expectCallee, cmatches)
			}
		})
	}
}

func TestMatrix_ParseAst(t *testing.T) {
	svcs := testServices(t)
	for _, f := range allFixtures() {
		t.Run(f.impl.Name(), func(t *testing.T) {
			root, _, err := svcs.Engine.Parse(f.impl, fixturePath(t, f.file), 8)
			if err != nil {
				t.Fatal(err)
			}
			if root == nil || root.Type == "" {
				t.Fatal("empty AST")
			}
		})
	}
}

func TestMatrix_QueryAst(t *testing.T) {
	svcs := testServices(t)
	for _, f := range allFixtures() {
		t.Run(f.impl.Name(), func(t *testing.T) {
			matches, err := svcs.Engine.QueryText(f.impl, fixturePath(t, f.file), f.idQuery, 5, false)
			if err != nil {
				t.Fatal(err)
			}
			if len(matches) == 0 {
				t.Fatal("expected matches")
			}
		})
	}
}

func TestMatrix_OutlineAnalyzeGetText(t *testing.T) {
	svcs := testServices(t)
	for _, f := range allFixtures() {
		t.Run(f.impl.Name(), func(t *testing.T) {
			path := fixturePath(t, f.file)
			out, err := svcs.File.Outline(f.impl.Name(), path, false)
			if err != nil {
				t.Fatal(err)
			}
			if len(out.Outline) == 0 {
				t.Fatal("empty outline")
			}
			rep, err := svcs.File.Dossier(f.impl.Name(), path)
			if err != nil {
				t.Fatal(err)
			}
			if rep.Metrics == nil || rep.Metrics.Nodes == 0 {
				t.Fatal("empty metrics")
			}
			syms, err := svcs.Engine.Symbols(f.impl, path)
			if err != nil {
				t.Fatal(err)
			}
			var target engine.Symbol
			for _, list := range syms {
				for _, s := range list {
					if s.Name == f.primarySymbol {
						target = s
					}
				}
			}
			if target.Name == "" {
				t.Fatalf("primary %q missing", f.primarySymbol)
			}
			text, err := svcs.Engine.GetText(f.impl, path, target.Start, target.End)
			if err != nil {
				t.Fatal(err)
			}
			if text == "" {
				t.Fatal("empty get_text")
			}
		})
	}
}

func TestMatrix_RankComplexity(t *testing.T) {
	svcs := testServices(t)
	dir := "testdata/matrix"
	for _, f := range allFixtures() {
		t.Run(f.impl.Name(), func(t *testing.T) {
			res, err := svcs.Rank.ComplexityDir(context.Background(), dir, []string{f.impl.Name()}, 20)
			if err != nil {
				t.Fatal(err)
			}
			if f.impl.Name() != "yaml" && len(res.Entries) == 0 {
				t.Fatal("expected complexity entries")
			}
		})
	}
}

func TestMatrix_FindUsagesModes(t *testing.T) {
	svcs := testServices(t)
	dir := "testdata/matrix"
	for _, f := range allFixtures() {
		if f.callName == "" {
			continue
		}
		t.Run(f.impl.Name(), func(t *testing.T) {
			if _, err := svcs.Find.Dir(context.Background(), service.FindQuery{
				Mode: service.FindOccurrences, Name: f.callName, Dir: dir,
				Languages: []string{f.impl.Name()}, GroupByFile: true,
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := svcs.Find.Dir(context.Background(), service.FindQuery{
				Mode: service.FindCallers, Name: f.callName, Dir: dir,
				Languages: []string{f.impl.Name()},
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := svcs.Find.Dir(context.Background(), service.FindQuery{
				Mode: service.FindDefinitions, Name: f.callName, Dir: dir,
				Languages: []string{f.impl.Name()},
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
