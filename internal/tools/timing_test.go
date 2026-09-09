package tools

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type fakeTimedIn struct {
	Path string
	Name string
}

type fakeTimedOut struct {
	Timed
}

func TestTimedLogMessage(t *testing.T) {
	var buf bytes.Buffer
	SetLogger(slog.New(slog.NewTextHandler(&buf, nil)))
	defer SetLogger(slog.New(slog.DiscardHandler))

	ok := timed(func(context.Context, *mcp.CallToolRequest, fakeTimedIn) (*mcp.CallToolResult, *fakeTimedOut, error) {
		return &mcp.CallToolResult{}, &fakeTimedOut{}, nil
	})
	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "find_usages"}}
	if _, _, err := ok(context.Background(), req, fakeTimedIn{Path: "/repo/internal", Name: "walkFiles"}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"tool find_usages finished in", "Path:/repo/internal", "Name:walkFiles"} {
		if !strings.Contains(out, want) {
			t.Fatalf("log missing %q: %s", want, out)
		}
	}

	buf.Reset()
	fail := timed(func(context.Context, *mcp.CallToolRequest, fakeTimedIn) (*mcp.CallToolResult, *fakeTimedOut, error) {
		return nil, nil, errors.New("boom")
	})
	if _, _, err := fail(context.Background(), req, fakeTimedIn{Path: "/x"}); err == nil {
		t.Fatal("want error")
	}
	out = buf.String()
	for _, want := range []string{"tool find_usages failed in", "boom", "Path:/x"} {
		if !strings.Contains(out, want) {
			t.Fatalf("log missing %q: %s", want, out)
		}
	}
}
