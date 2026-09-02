package engine

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"mcp-ast/internal/lang"
)

// skipDirNames are directory basenames skipped during recursive walks.
// Hidden directories (prefix ".") are also skipped, except the walk root.
var skipDirNames = map[string]struct{}{
	"node_modules": {},
	"vendor":       {},
	"target":       {},
	"dist":         {},
	"build":        {},
	"out":          {},
	"bin":          {},
	"__pycache__":  {},
	".venv":        {},
	"venv":         {},
	"env":          {},
	".tox":         {},
	".mypy_cache":  {},
	".pytest_cache": {},
	".next":        {},
	".nuxt":        {},
	".svelte-kit":  {},
	"coverage":     {},
	".turbo":       {},
	".cache":       {},
	"Pods":         {},
	"Carthage":     {},
	".gradle":      {},
	".idea":        {},
	".vscode":      {},
}

// shouldSkipDir reports whether a directory should be skipped during walks.
// The walk root is never skipped. Hidden directories and known dependency/
// build output trees are skipped.
func shouldSkipDir(root, path string, name string) bool {
	if path == root {
		return false
	}
	if strings.HasPrefix(name, ".") {
		return true
	}
	_, ok := skipDirNames[name]
	return ok
}

// Symbols runs the language's built-in symbol queries and returns the
// results grouped by kind (classes, methods, fields, imports, ...).
func (e *Engine) Symbols(l lang.Language, path string) (map[string][]Symbol, error) {
	return e.SymbolsText(l, path, false)
} // SymbolsText is Symbols with a fullText switch: when true, symbol text is the
// full node source instead of the first-line summary.
func (e *Engine) SymbolsText(l lang.Language, path string, fullText bool) (map[string][]Symbol, error) {
	src, tree, err := e.parseFile(l, path)
	if err != nil {
		return nil, err
	}
	defer tree.Close()
	out := make(map[string][]Symbol)
	for kind, qs := range l.SymbolQueries() {
		matches, err := e.runQuery(l, src, tree.RootNode(), qs, 0, fullText)
		if err != nil {
			return nil, fmt.Errorf("symbol query %q: %w", kind, err)
		}
		syms := make([]Symbol, 0, len(matches))
		for _, m := range matches {
			var sym Symbol
			for _, c := range m.Captures {
				switch c.Name {
				case "name":
					sym.Name = c.Text
				case "symbol":
					sym.Start, sym.End = c.Start, c.End
					sym.Text = c.Text
				}
			}
			if sym.Name == "" {
				sym.Name = sym.Text
			}
			syms = append(syms, sym)
		}
		out[kind] = syms
	}
	return out, nil
}

// ScanSymbols walks dir recursively and collects symbols for every file that
// matches the language's extensions (or auto-detected when filter is nil).
// fullText switches symbol text from a first-line summary to the full source.
// Unreadable/unparseable files are reported in errors instead of failing.
// ctx cancels the walk (e.g. tool-call timeout); on cancel it returns ctx.Err.
func (e *Engine) ScanSymbols(ctx context.Context, dir string, filter lang.Language, fullText bool) (map[string]map[string][]Symbol, map[string]string, error) {
	files := make(map[string]map[string][]Symbol)
	errs, err := e.walkFiles(ctx, dir, filter, func(path string, l lang.Language) error {
		syms, err := e.SymbolsText(l, path, fullText)
		if err != nil {
			return err
		}
		files[path] = syms
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return files, errs, nil
}

// walkFiles visits every source file under dir that the language filter (or
// auto-detection) accepts, skipping hidden directories, common dependency and
// build-output trees, and honoring ctx.
// Errors from fn are collected per-file in errs and never abort the walk;
// walk-level errors (e.g. ctx cancellation) are returned.
func (e *Engine) walkFiles(ctx context.Context, dir string, filter lang.Language, fn func(path string, l lang.Language) error) (map[string]string, error) {
	errs := make(map[string]string)
	walk := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if shouldSkipDir(dir, path, d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		var l lang.Language
		if filter != nil {
			if !hasExt(d.Name(), filter.Extensions()) {
				return nil
			}
			l = filter
		} else {
			ll, err := e.reg.Resolve("", path)
			if err != nil {
				return nil
			}
			l = ll
		}
		if err := fn(path, l); err != nil {
			errs[path] = err.Error()
		}
		return nil
	})
	if err := walk; err != nil {
		return nil, err
	}
	return errs, nil
}

func hasExt(name string, exts []string) bool {
	for _, x := range exts {
		if strings.HasSuffix(name, x) {
			return true
		}
	}
	return false
}
