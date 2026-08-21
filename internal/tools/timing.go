package tools

import (
	"context"
	"reflect"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// toolTimeout bounds every tool call. Configurable via SetToolTimeout
// (default 30s); 0 disables the limit.
var toolTimeout = 30 * time.Second

// SetToolTimeout configures the per-tool-call timeout. d <= 0 disables it.
func SetToolTimeout(d time.Duration) { toolTimeout = d }

// Timed is embedded in every tool output so all responses carry the
// processing time of the query.
type Timed struct {
	ElapsedMS float64 `json:"elapsed_ms"`
}

func (t *Timed) SetElapsedMS(ms float64) { t.ElapsedMS = ms }

type TimedOutput interface {
	SetElapsedMS(float64)
}

// timed wraps a tool handler: derives a cancelable, time-bounded context for
// the call and fills ElapsedMS with the handler's duration (milliseconds).
// Applied to every registered tool.
func timed[In any, Out TimedOutput](h func(context.Context, *mcp.CallToolRequest, In) (*mcp.CallToolResult, Out, error)) func(context.Context, *mcp.CallToolRequest, In) (*mcp.CallToolResult, Out, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		if toolTimeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, toolTimeout)
			defer cancel()
		}
		start := time.Now()
		res, out, err := h(ctx, req, in)
		if v := reflect.ValueOf(out); v.Kind() == reflect.Pointer && !v.IsNil() {
			out.SetElapsedMS(float64(time.Since(start).Microseconds()) / 1000)
		}
		return res, out, err
	}
}
