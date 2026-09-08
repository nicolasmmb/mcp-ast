package main

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepoFlagAccumulates(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	var dirs stringList
	fs.Var(&dirs, "repo", "")
	if err := fs.Parse([]string{"-repo", "/a", "-repo", "/b", "-repo", "/c"}); err != nil {
		t.Fatal(err)
	}
	if len(dirs) != 3 || dirs[0] != "/a" || dirs[1] != "/b" || dirs[2] != "/c" {
		t.Fatalf("want [/a /b /c], got %v", dirs)
	}
}

func TestNewLoggerFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ast.log")
	logger, closeLog := newLogger(false, path)
	logger.Info("hello log file")
	if closeLog != nil {
		closeLog()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "hello log file") {
		t.Fatalf("log file missing test line: %q", data)
	}
}

func TestValidateRepoDirs(t *testing.T) {
	dir := t.TempDir()
	if err := validateRepoDirs([]string{dir}); err != nil {
		t.Fatalf("existing dir must pass: %v", err)
	}
	if err := validateRepoDirs([]string{filepath.Join(dir, "missing")}); err == nil {
		t.Fatal("missing dir must fail")
	}
	f := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateRepoDirs([]string{f}); err == nil {
		t.Fatal("file must fail")
	}
	if err := validateRepoDirs(nil); err != nil {
		t.Fatalf("empty list must pass: %v", err)
	}
}
