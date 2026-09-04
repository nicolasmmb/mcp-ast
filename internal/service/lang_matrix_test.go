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
	impl     lang.Language
	file     string
	symbol   string
	callName string
}

func allFixtures() []langFixture {
	return []langFixture{
		{golanglang.Go{}, "basic.go", "Helper", "Helper"},
		{java.Java{}, "basic.java", "Greeter", "hello"},
		{python.Python{}, "basic.py", "helper", "helper"},
		{javascript.JavaScript{}, "basic.js", "helper", "helper"},
		{typescript.TypeScript{}, "basic.ts", "helper", "helper"},
		{rust.Rust{}, "basic.rs", "helper", "helper"},
		{csharp.CSharp{}, "basic.cs", "Greeter", "Helper"},
		{bash.Bash{}, "basic.sh", "helper", "helper"},
		{yaml.YAML{}, "basic.yaml", "name", ""},
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

func TestMatrix_RegisterAndScan(t *testing.T) {
	svcs := testServices(t)
	if len(svcs.Engine.ListLanguages()) != 9 {
		t.Fatalf("want 9 langs, got %v", svcs.Engine.ListLanguages())
	}
	for _, f := range allFixtures() {
		t.Run(f.impl.Name(), func(t *testing.T) {
			path := fixturePath(t, f.file)
			res, err := svcs.Scan.Path(f.impl.Name(), path, false)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, kinds := range res.Files {
				for _, syms := range kinds {
					for _, s := range syms {
						if s.Name == f.symbol {
							found = true
						}
					}
				}
			}
			if !found {
				t.Fatalf("symbol %q missing in %+v", f.symbol, res.Files)
			}
			if _, err := svcs.File.Outline(f.impl.Name(), path, false); err != nil {
				t.Fatal(err)
			}
			if _, err := svcs.File.Dossier(f.impl.Name(), path); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMatrix_FindUsages(t *testing.T) {
	svcs := testServices(t)
	for _, f := range allFixtures() {
		if f.callName == "" {
			continue
		}
		t.Run(f.impl.Name(), func(t *testing.T) {
			res, err := svcs.Find.Dir(context.Background(), service.FindQuery{
				Mode: service.FindOccurrences, Name: f.callName, Dir: "testdata/matrix",
				Languages: []string{f.impl.Name()}, GroupByFile: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Files) == 0 && len(res.Matches) == 0 {
				t.Fatalf("no occurrences for %q", f.callName)
			}
		})
	}
}
