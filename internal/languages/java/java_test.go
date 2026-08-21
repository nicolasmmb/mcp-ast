package java

import (
	"os"
	"path/filepath"
	"testing"

	"mcp-java-ast/internal/engine"
	"mcp-java-ast/internal/lang"
)

const sample = `package demo;

import java.util.List;

public class Greeter {
    private String name;

    public String hello(String who) {
        return "hi " + who;
    }
}
`

func TestParseAndSymbols(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Greeter.java")
	if err := os.WriteFile(path, []byte(sample), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := lang.NewRegistry()
	if err := reg.Register(Java{}); err != nil {
		t.Fatal(err)
	}
	eng := engine.New(reg)

	l, err := eng.Resolve("", path)
	if err != nil {
		t.Fatalf("detect language: %v", err)
	}
	if l.Name() != "java" {
		t.Fatalf("want java, got %s", l.Name())
	}

	root, hasErr, err := eng.Parse(l, path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if root.Type != "program" {
		t.Fatalf("root type: want program, got %s", root.Type)
	}
	if hasErr {
		t.Fatal("sample must parse without errors")
	}

	syms, err := eng.Symbols(l, path)
	if err != nil {
		t.Fatal(err)
	}
	if len(syms["classes"]) != 1 || syms["classes"][0].Name != "Greeter" {
		t.Fatalf("classes: %+v", syms["classes"])
	}
	if len(syms["methods"]) != 1 || syms["methods"][0].Name != "hello" {
		t.Fatalf("methods: %+v", syms["methods"])
	}
	if len(syms["imports"]) != 1 {
		t.Fatalf("imports: %+v", syms["imports"])
	}

	matches, err := eng.Query(l, path, `(method_declaration name: (identifier) @name) @method`)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("query matches: %+v", matches)
	}
	found := false
	for _, c := range matches[0].Captures {
		if c.Name == "name" && c.Text == "hello" {
			found = true
		}
	}
	if !found {
		t.Fatalf("query captures: %+v", matches[0].Captures)
	}
}
