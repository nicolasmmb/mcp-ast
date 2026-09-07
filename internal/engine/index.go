package engine

import (
	"context"
	"sync"

	"mcp-ast/internal/lang"
)

// FileIndex contains indexed facts extracted from one source file.
type FileIndex struct {
	Language   string              `json:"language"`
	Symbols    map[string][]Symbol `json:"symbols"`
	Usages     []UsageMatch        `json:"usages"`
	Complexity []ComplexityEntry   `json:"complexity"`
}

// IndexFile parses path once and returns its symbols, identifier usages, and
// cyclomatic complexity.
func (e *Engine) IndexFile(l lang.Language, path string) (*FileIndex, error) {
	src, tree, err := e.parseFile(l, path)
	if err != nil {
		return nil, err
	}
	defer tree.Close()
	root := tree.RootNode()
	symbols, err := e.symbolsTree(l, src, root, false)
	if err != nil {
		return nil, err
	}
	usages, err := e.classifyUsagesTree(l, path, src, root, "", true)
	if err != nil {
		return nil, err
	}
	complexity, err := e.complexityTree(l, src, root)
	if err != nil {
		return nil, err
	}
	return &FileIndex{Language: l.Name(), Symbols: symbols, Usages: usages, Complexity: complexity}, nil
}

// ListFiles returns every recognized file path under dir (same walk rules as
// IndexDir), without parsing. Used to detect add/change/delete deltas.
func (e *Engine) ListFiles(ctx context.Context, dir string, filter lang.Language) ([]string, error) {
	var mu sync.Mutex
	var paths []string
	_, err := e.walkFiles(ctx, dir, filter, func(path string, l lang.Language) error {
		mu.Lock()
		paths = append(paths, path)
		mu.Unlock()
		return nil
	})
	if err != nil {
		return nil, err
	}
	return paths, nil
}

// IndexDir builds compact facts for every recognized file under dir.
func (e *Engine) IndexDir(ctx context.Context, dir string, filter lang.Language) (map[string]*FileIndex, map[string]string, error) {
	files := make(map[string]*FileIndex)
	var mu sync.Mutex
	errs, err := e.walkFiles(ctx, dir, filter, func(path string, l lang.Language) error {
		facts, err := e.IndexFile(l, path)
		if err != nil {
			return err
		}
		mu.Lock()
		files[path] = facts
		mu.Unlock()
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return files, errs, nil
}
