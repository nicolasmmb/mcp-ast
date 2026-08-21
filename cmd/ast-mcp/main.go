package main

import (
	"context"
	"flag"
	"log"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"mcp-ast/internal/engine"
	"mcp-ast/internal/lang"
	golanglang "mcp-ast/internal/languages/go"
	"mcp-ast/internal/languages/java"
	"mcp-ast/internal/languages/python"
	"mcp-ast/internal/tools"
)

func main() {
	timeout := flag.Duration("tool-timeout", 30*time.Second, "per-tool-call timeout (0 disables)")
	flag.Parse()
	tools.SetToolTimeout(*timeout)

	reg := lang.NewRegistry()
	for _, l := range []lang.Language{java.Java{}, python.Python{}, golanglang.Go{}} {
		if err := reg.Register(l); err != nil {
			log.Fatalf("registering language: %v", err)
		}
	}

	server := mcp.NewServer(&mcp.Implementation{Name: "ast-mcp", Version: "0.1.0"}, nil)
	tools.Register(server, engine.New(reg))

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatal(err)
	}
}
