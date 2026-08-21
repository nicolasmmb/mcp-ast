package main

import (
	"context"
	"log"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"mcp-java-ast/internal/engine"
	"mcp-java-ast/internal/lang"
	golanglang "mcp-java-ast/internal/languages/go"
	"mcp-java-ast/internal/languages/java"
	"mcp-java-ast/internal/languages/python"
	"mcp-java-ast/internal/tools"
)

func main() {
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
