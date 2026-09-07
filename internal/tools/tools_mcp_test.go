package tools_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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
	bin := filepath.Join(t.TempDir(), "ast-mcp")
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
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin)
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

func TestMCP_ToolsList_NineTools(t *testing.T) {
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
	}
	for _, n := range want {
		if !names[n] {
			t.Errorf("missing tool %q; got %v", n, names)
		}
	}
	if len(names) != 9 {
		t.Errorf("want 9 tools, got %d: %v", len(names), names)
	}
	for _, legacy := range []string{"parse_ast_file", "usages_dir", "callers_dir", "symbols_file", "get_text_file"} {
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
