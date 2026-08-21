package python

import (
	"os"
	"path/filepath"
	"testing"

	"mcp-ast/internal/engine"
	"mcp-ast/internal/lang"
)

func TestParseAndSymbols(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "demo.py")
	src := `import os

class Greeter:
    def hello(self, who):
        return "hi " + who
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := lang.NewRegistry()
	if err := reg.Register(Python{}); err != nil {
		t.Fatal(err)
	}
	eng := engine.New(reg)

	l, err := eng.Resolve("", path)
	if err != nil {
		t.Fatalf("detect language: %v", err)
	}
	if l.Name() != "python" {
		t.Fatalf("want python, got %s", l.Name())
	}

	root, hasErr, err := eng.Parse(l, path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if root.Type != "module" || hasErr {
		t.Fatalf("root: %s hasErr=%v", root.Type, hasErr)
	}

	syms, err := eng.Symbols(l, path)
	if err != nil {
		t.Fatal(err)
	}
	if len(syms["classes"]) != 1 || syms["classes"][0].Name != "Greeter" {
		t.Fatalf("classes: %+v", syms["classes"])
	}
	if len(syms["functions"]) != 1 || syms["functions"][0].Name != "hello" {
		t.Fatalf("functions: %+v", syms["functions"])
	}
}
