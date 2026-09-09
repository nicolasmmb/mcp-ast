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
// commit is the short git hash, injected with "-X main.commit=$(git rev-parse --short HEAD)".
var (
	version = "dev"
	commit  = ""
)

// displayVersion returns the version string with commit hash when available.
func displayVersion() string {
	if commit != "" {
		return version + "-" + commit
	}
	return version
}

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
	verbose := flag.Bool("verbose", false, "also log debug output to stderr (info always goes to stderr)")
	logPath := flag.String("log", "", "append logs to file (in addition to stderr)")
	showVersion := flag.Bool("version", false, "print version and exit")
	maxMemory := flag.String("max-memory", "auto", "repository index memory limit in MB, or auto")
	var repoDirs stringList
	flag.Var(&repoDirs, "repo", "index this directory at boot (repeatable); search inside it uses the index automatically")
	watch := flag.Bool("watch", true, "keep repository indexes fresh automatically (polling); -watch=false disables")
	watchInterval := flag.Duration("watch-interval", 2*time.Second, "watch poll interval")
	cacheDir := flag.String("cache-dir", "", "snapshot cache directory (default: user cache)")
	flag.Parse()

	if *showVersion {
		fmt.Printf("ast-mcp %s\n", displayVersion())
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
	logger.Debug(fmt.Sprintf("os.Args: %v", os.Args))

	reg := lang.NewRegistry()
	for _, l := range []lang.Language{java.Java{}, python.Python{}, golanglang.Go{}, bash.Bash{}, csharp.CSharp{}, javascript.JavaScript{}, rust.Rust{}, typescript.TypeScript{}, yaml.YAML{}} {
		if err := reg.Register(l); err != nil {
			log.Fatalf("registering language: %v", err)
		}
	}
	logDest := *logPath
	if logDest == "" {
		logDest = "stderr (no file)"
	}
	logger.Info(fmt.Sprintf("ast-mcp %s started: tool timeout %s, %d languages (%s), log at %s",
		displayVersion(), timeout.String(), len(reg.List()), strings.Join(reg.List(), ", "), logDest))

	server := mcp.NewServer(&mcp.Implementation{Name: "ast-mcp", Version: version}, nil)
	svcs := service.NewWithStore(engine.New(reg), repoindex.NewMemory(memoryLimit))
	svcs.Repo.SetToolVersion(version)
	svcs.Repo.SetLogger(logger)
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
		if info.State == "ready" && info.Restored {
			logger.Info(fmt.Sprintf("index ready for %s (restored: %d %s, %s RAM)", dir, info.Files, plural(info.Files, "file", "files"), formatBytes(info.MemoryBytes)), "dir", dir)
		} else if info.State == "building" {
			logger.Warn(fmt.Sprintf("index building for %s — queries use disk until ready (check index_status)", dir), "dir", dir)
		} else {
			logger.Info(fmt.Sprintf("index %s for %s (%d %s)", info.State, dir, info.Files, plural(info.Files, "file", "files")), "dir", dir)
		}
	}
	if len(repoDirs) == 0 {
		logger.Warn("no -repo configured: server running fully on disk, no index will be built or loaded")
	}
	tools.Register(server, svcs)

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatal(err)
	}
}

// newLogger builds a slog logger: Info (Debug with -verbose) always goes to
// stderr so the MCP debug console shows it; -log additionally appends to a
// file (and routes std log there too).
func newLogger(verbose bool, logPath string) (*slog.Logger, func()) {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	var w io.Writer = os.Stderr
	var closeLog func()
	if logPath != "" {
		f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: cannot open log file %s: %v\n", logPath, err)
		} else {
			w = io.MultiWriter(os.Stderr, f)
			closeLog = func() { f.Close() }
			log.SetOutput(io.MultiWriter(os.Stderr, f))
		}
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: level})), closeLog
}

// formatBytes formats a byte count for log messages ("794 B", "14.2 MB").
func formatBytes(n int64) string {
	if n < 0 {
		return "missing"
	}
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	f := float64(n)
	for _, u := range []string{"KB", "MB", "GB"} {
		f /= 1024
		if f < 1024 {
			return fmt.Sprintf("%.1f %s", f, u)
		}
	}
	return fmt.Sprintf("%.1f TB", f/1024)
}

// plural picks the singular or plural noun for log messages.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
