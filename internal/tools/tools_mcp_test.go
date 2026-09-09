package tools_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func moduleRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(wd, "go.mod")); err == nil {
			return wd
		}
		wd = filepath.Dir(wd)
	}
	t.Fatal("go.mod not found")
	return ""
}

func buildServer(t *testing.T) string {
	t.Helper()
	name := "ast-mcp"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	bin := filepath.Join(t.TempDir(), name)
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/ast-mcp")
	cmd.Dir = moduleRoot(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

func mcpSession(t *testing.T, bin string, afterInit []string) []map[string]any {
	t.Helper()
	return mcpSessionWithArgs(t, bin, nil, afterInit)
}

func mcpSessionWithArgs(t *testing.T, bin string, args []string, afterInit []string) []map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	write := func(s string) {
		t.Helper()
		if _, err := io.WriteString(stdin, s+"\n"); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	write(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`)
	write(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	for _, line := range afterInit {
		write(line)
	}

	done := make(chan []map[string]any, 1)
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
		var responses []map[string]any
		for sc.Scan() {
			var m map[string]any
			if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
				continue
			}
			responses = append(responses, m)
			if id, ok := m["id"].(float64); ok && id >= 2 {
				done <- responses
				return
			}
		}
		done <- responses
	}()

	var responses []map[string]any
	select {
	case responses = <-done:
	case <-time.After(12 * time.Second):
		t.Fatal("timeout waiting for MCP response")
	}
	_ = stdin.Close()
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	return responses
}

func TestMCP_ToolsList_ThirteenTools(t *testing.T) {
	bin := buildServer(t)
	resps := mcpSession(t, bin, []string{
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
	})
	var list map[string]any
	for _, r := range resps {
		if r["id"] == float64(2) {
			list = r
			break
		}
	}
	if list == nil {
		t.Fatalf("no tools/list response in %#v", resps)
	}
	result, _ := list["result"].(map[string]any)
	toolsRaw, _ := result["tools"].([]any)
	names := map[string]bool{}
	for _, raw := range toolsRaw {
		tm, _ := raw.(map[string]any)
		n, _ := tm["name"].(string)
		names[n] = true
	}
	want := []string{
		"list_languages", "parse_ast", "query_ast", "scan_symbols",
		"analyze_file", "get_text", "find_usages", "rank_complexity", "outline_file",
		"index_status", "repo_impact", "repo_cycles", "repo_topology",
	}
	for _, n := range want {
		if !names[n] {
			t.Errorf("missing tool %q; got %v", n, names)
		}
	}
	if len(names) != len(want) {
		t.Errorf("want %d tools, got %d: %v", len(want), len(names), names)
	}
	for _, legacy := range []string{"parse_ast_file", "usages_dir", "callers_dir", "symbols_file", "get_text_file", "index_repo", "repo_status", "refresh_repo", "drop_repo", "search_repo"} {
		if names[legacy] {
			t.Errorf("legacy tool still registered: %s", legacy)
		}
	}
}

func TestMCP_ListLanguages_Call(t *testing.T) {
	bin := buildServer(t)
	resps := mcpSession(t, bin, []string{
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_languages","arguments":{}}}`,
	})
	var call map[string]any
	for _, r := range resps {
		if r["id"] == float64(2) {
			call = r
			break
		}
	}
	if call == nil {
		t.Fatalf("no tools/call response: %#v", resps)
	}
	raw, _ := json.Marshal(call)
	s := string(raw)
	if !strings.Contains(s, "go") {
		t.Fatalf("expected go in response: %s", s)
	}
	if !strings.Contains(s, "elapsed_ms") {
		t.Fatalf("expected elapsed_ms: %s", s)
	}
}

func TestMCP_IndexStatus_WithRepo(t *testing.T) {
	bin := buildServer(t)
	dir := t.TempDir()
	goFile := filepath.Join(dir, "main.go")
	if err := os.WriteFile(goFile, []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	resps := mcpSessionWithArgs(t, bin, []string{"-repo", dir}, []string{
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"index_status","arguments":{}}}`,
	})
	var call map[string]any
	for _, r := range resps {
		if r["id"] == float64(2) {
			call = r
			break
		}
	}
	if call == nil {
		t.Fatalf("no index_status response: %#v", resps)
	}
	raw, _ := json.Marshal(call)
	s := string(raw)
	if !strings.Contains(s, `"state":"ready"`) {
		t.Fatalf("expected state=ready: %s", s)
	}
	if !strings.Contains(s, `"files_indexed":1`) {
		t.Fatalf("expected files_indexed=1: %s", s)
	}
	if !strings.Contains(s, `"watch":true`) {
		t.Fatalf("expected watch=true: %s", s)
	}
}

func TestMCP_IndexedTools_HaveElapsedMsAndSource(t *testing.T) {
	bin := buildServer(t)
	dir := t.TempDir()
	src := `package app

import "fmt"

func Main() { _ = Helper() }
func Helper() int { return 1 }
func Unused() int { return 42 }
func call() int { fmt.Println("ok"); return Helper() }
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	// Start server with -repo, wait for index, then test via single tool call
	// The index builds fast for a 1-file repo, so we poll index_status
	resps := mcpSessionWithArgs(t, bin, []string{"-repo", dir}, []string{
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"index_status","arguments":{}}}`,
	})
	var indexResp map[string]any
	for _, r := range resps {
		if r["id"] == float64(2) {
			indexResp = r
			break
		}
	}
	if indexResp == nil {
		t.Fatal("no index_status response")
	}
	raw, _ := json.Marshal(indexResp)
	if !strings.Contains(string(raw), `"state":"ready"`) {
		t.Fatalf("index not ready: %s", string(raw))
	}

	// Now test each tool via separate sessions (index is cached, restore is instant)
	tools := []struct {
		name string
		args string
	}{
		{"find_usages", `{"mode":"occurrences","name":"Helper","path":"` + dir + `"}`},
		{"scan_symbols", `{"path":"` + dir + `"}`},
		{"outline_file", `{"path":"` + dir + `/main.go` + `"}`},
		{"analyze_file", `{"path":"` + dir + `/main.go` + `"}`},
	}
	for _, tool := range tools {
		t.Run(tool.name, func(t *testing.T) {
			resps := mcpSessionWithArgs(t, bin, []string{"-repo", dir}, []string{
				fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"%s","arguments":%s}}`, tool.name, tool.args),
			})
			var resp map[string]any
			for _, r := range resps {
				if r["id"] == float64(2) {
					resp = r
					break
				}
			}
			if resp == nil {
				t.Fatalf("no response for %s", tool.name)
			}
			raw, _ := json.Marshal(resp)
			s := string(raw)
			if !strings.Contains(s, "elapsed_ms") {
				t.Errorf("%s: missing elapsed_ms: %s", tool.name, s)
			}
			// find_usages, scan_symbols, outline_file use index; analyze_file always re-parses (ast_fallback)
			if tool.name == "analyze_file" {
				if !strings.Contains(s, `"source":"ast_fallback"`) {
					t.Errorf("%s: expected source=ast_fallback: %s", tool.name, s)
				}
			} else {
				if !strings.Contains(s, `"source":"indexed"`) {
					t.Errorf("%s: expected source=indexed: %s", tool.name, s)
				}
			}
		})
	}
}
