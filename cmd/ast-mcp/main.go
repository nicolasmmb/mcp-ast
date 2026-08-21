package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"mcp-ast/internal/engine"
	"mcp-ast/internal/lang"
	golanglang "mcp-ast/internal/languages/go"
	"mcp-ast/internal/languages/java"
	"mcp-ast/internal/languages/python"
	"mcp-ast/internal/tools"
)

// version is injected at build time with -ldflags "-X main.version=vX.Y.Z".
var version = "dev"

func main() {
	timeout := flag.Duration("tool-timeout", 30*time.Second, "per-tool-call timeout (0 disables)")
	verbose := flag.Bool("verbose", false, "log debug output to stderr")
	logPath := flag.String("log", "", "write log to file (append)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("ast-mcp %s\n", version)
		return
	}
	tools.SetToolTimeout(*timeout)

	logger, closeLog := newLogger(*verbose, *logPath)
	if closeLog != nil {
		defer closeLog()
	}
	tools.SetLogger(logger)

	reg := lang.NewRegistry()
	for _, l := range []lang.Language{java.Java{}, python.Python{}, golanglang.Go{}} {
		if err := reg.Register(l); err != nil {
			log.Fatalf("registering language: %v", err)
		}
	}
	logger.Info("started", "version", version, "tool_timeout", timeout.String(), "languages", reg.List(), "log", *logPath)

	server := mcp.NewServer(&mcp.Implementation{Name: "ast-mcp", Version: version}, nil)
	tools.Register(server, engine.New(reg))

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatal(err)
	}
}

// newLogger builds a slog logger: -verbose enables debug level on stderr,
// -log appends to a file. Neither flag leaves logging disabled.
func newLogger(verbose bool, logPath string) (*slog.Logger, func()) {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	var w io.Writer = io.Discard
	if verbose {
		w = os.Stderr
	}
	var closeLog func()
	if logPath != "" {
		f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: cannot open log file %s: %v\n", logPath, err)
		} else {
			if w == os.Stderr {
				w = io.MultiWriter(os.Stderr, f)
			} else {
				w = f
			}
			closeLog = func() { f.Close() }
		}
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: level})), closeLog
}
