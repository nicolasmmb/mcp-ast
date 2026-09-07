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

func TestContract_ListLanguages(t *testing.T) {
	svcs := testServices(t)
	langs := svcs.Engine.ListLanguages()
	if len(langs) != 1 || langs[0] != "go" {
		t.Fatalf("list_languages: want [go], got %v", langs)
	}
}
