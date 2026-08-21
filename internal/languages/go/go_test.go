package golang

import (
	"os"
	"path/filepath"
	"testing"

	"mcp-ast/internal/engine"
	"mcp-ast/internal/lang"
)

func TestParseAndSymbols(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "demo.go")
	src := `package demo

import "fmt"

type Greeter struct{}

func (g Greeter) Hello(who string) string {
	return "hi " + who
}
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := lang.NewRegistry()
	if err := reg.Register(Go{}); err != nil {
		t.Fatal(err)
	}
	eng := engine.New(reg)

	l, err := eng.Resolve("", path)
	if err != nil {
		t.Fatalf("detect language: %v", err)
	}
	if l.Name() != "go" {
		t.Fatalf("want go, got %s", l.Name())
	}

	root, hasErr, err := eng.Parse(l, path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if root.Type != "source_file" || hasErr {
		t.Fatalf("root: %s hasErr=%v", root.Type, hasErr)
	}

	syms, err := eng.Symbols(l, path)
	if err != nil {
		t.Fatal(err)
	}
	if len(syms["types"]) != 1 || syms["types"][0].Name != "Greeter" {
		t.Fatalf("types: %+v", syms["types"])
	}
	if len(syms["methods"]) != 1 || syms["methods"][0].Name != "Hello" {
		t.Fatalf("methods: %+v", syms["methods"])
	}
}
