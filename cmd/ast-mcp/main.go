package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"mcp-ast/internal/engine"
	"mcp-ast/internal/lang"
	"mcp-ast/internal/languages/bash"
	"mcp-ast/internal/languages/csharp"
	golanglang "mcp-ast/internal/languages/go"
	"mcp-ast/internal/languages/java"
	"mcp-ast/internal/languages/javascript"
	"mcp-ast/internal/languages/python"
	"mcp-ast/internal/languages/rust"
	"mcp-ast/internal/languages/typescript"
	"mcp-ast/internal/languages/yaml"
	"mcp-ast/internal/repoindex"
	"mcp-ast/internal/service"
	"mcp-ast/internal/tools"
)

// version is injected at build time with -ldflags "-X main.version=vX.Y.Z".
var version = "dev"

// stringList accumulates a repeatable flag value.
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

// validateRepoDirs fails when a -repo value is missing or is not a directory.
func validateRepoDirs(dirs []string) error {
	for _, d := range dirs {
		st, err := os.Stat(d)
		if err != nil {
			return fmt.Errorf("-repo %s: %w", d, err)
		}
		if !st.IsDir() {
			return fmt.Errorf("-repo %s: not a directory", d)
		}
	}
	return nil
}

func main() {
	timeout := flag.Duration("tool-timeout", 30*time.Second, "per-tool-call timeout (0 disables)")
	verbose := flag.Bool("verbose", false, "log debug output to stderr")
	logPath := flag.String("log", "", "write log to file (append)")
	showVersion := flag.Bool("version", false, "print version and exit")
	maxMemory := flag.String("max-memory", "auto", "repository index memory limit in MB, or auto")
	var repoDirs stringList
	flag.Var(&repoDirs, "repo", "index this directory at boot (repeatable); search inside it uses the index automatically")
	watch := flag.Bool("watch", true, "keep repository indexes fresh automatically (polling); -watch=false disables")
	watchInterval := flag.Duration("watch-interval", 2*time.Second, "watch poll interval")
	cacheDir := flag.String("cache-dir", "", "snapshot cache directory (default: user cache)")
	flag.Parse()

	if *showVersion {
		fmt.Printf("ast-mcp %s\n", version)
		return
	}
	if err := validateRepoDirs(repoDirs); err != nil {
		log.Fatalf("%v", err)
	}
	tools.SetToolTimeout(*timeout)
	memoryLimit, err := repoindex.ParseMemoryLimit(*maxMemory)
	if err != nil {
		log.Fatalf("invalid -max-memory: %v", err)
	}

	logger, closeLog := newLogger(*verbose, *logPath)
	if closeLog != nil {
		defer closeLog()
	}
	tools.SetLogger(logger)

	reg := lang.NewRegistry()
	for _, l := range []lang.Language{java.Java{}, python.Python{}, golanglang.Go{}, bash.Bash{}, csharp.CSharp{}, javascript.JavaScript{}, rust.Rust{}, typescript.TypeScript{}, yaml.YAML{}} {
		if err := reg.Register(l); err != nil {
			log.Fatalf("registering language: %v", err)
		}
	}
	logger.Info("started", "version", version, "tool_timeout", timeout.String(), "languages", reg.List(), "log", *logPath)

	server := mcp.NewServer(&mcp.Implementation{Name: "ast-mcp", Version: version}, nil)
	svcs := service.NewWithStore(engine.New(reg), repoindex.NewMemory(memoryLimit))
	svcs.Repo.SetToolVersion(version)
	if *cacheDir != "" {
		svcs.Repo.SetCacheDir(*cacheDir)
	}
	if *watch {
		svcs.Repo.SetWatchInterval(*watchInterval)
	}
	for _, dir := range repoDirs {
		info, err := svcs.Repo.Index(context.Background(), dir, nil)
		if err != nil {
			log.Fatalf("indexing -repo %s: %v", dir, err)
		}
		logger.Info("repo indexed", "dir", dir, "state", info.State, "restored", info.Restored)
	}
	tools.Register(server, svcs)

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
