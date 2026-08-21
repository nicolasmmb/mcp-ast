package tools

import (
	"context"
	"reflect"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Timed is embedded in every tool output so all responses carry the
// processing time of the query.
type Timed struct {
	ElapsedMS float64 `json:"elapsed_ms"`
}

func (t *Timed) SetElapsedMS(ms float64) { t.ElapsedMS = ms }

type TimedOutput interface {
	SetElapsedMS(float64)
}

// timed wraps a tool handler and fills ElapsedMS with the handler's duration
// (milliseconds). Applied to every registered tool.
func timed[In any, Out TimedOutput](h func(context.Context, *mcp.CallToolRequest, In) (*mcp.CallToolResult, Out, error)) func(context.Context, *mcp.CallToolRequest, In) (*mcp.CallToolResult, Out, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		start := time.Now()
		res, out, err := h(ctx, req, in)
		if v := reflect.ValueOf(out); v.Kind() == reflect.Pointer && !v.IsNil() {
			out.SetElapsedMS(float64(time.Since(start).Microseconds()) / 1000)
		}
		return res, out, err
	}
}
