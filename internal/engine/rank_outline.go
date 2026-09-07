package engine

import (
	"context"
	"sort"
	"strings"
	"sync"

	"mcp-ast/internal/lang"
)

type RankedComplexity struct {
	File string `json:"file"`
	ComplexityEntry
}

func (e *Engine) ComplexityDir(ctx context.Context, dir string, filter lang.Language, limit int) ([]RankedComplexity, map[string]string, error) {
	entries := make([]RankedComplexity, 0, 16)
	var mu sync.Mutex
	errs, err := e.walkFiles(ctx, dir, filter, func(path string, l lang.Language) error {
		cx, err := e.Complexity(l, path)
		if err != nil {
			return err
		}
		if len(cx) == 0 {
			return nil
		}
		local := make([]RankedComplexity, 0, len(cx))
		for _, it := range cx {
			local = append(local, RankedComplexity{File: path, ComplexityEntry: it})
		}
		mu.Lock()
		entries = append(entries, local...)
		mu.Unlock()
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Complexity != entries[j].Complexity {
			return entries[i].Complexity > entries[j].Complexity
		}
		if entries[i].File != entries[j].File {
			return entries[i].File < entries[j].File
		}
		if entries[i].Start.Row != entries[j].Start.Row {
			return entries[i].Start.Row < entries[j].Start.Row
		}
		if entries[i].Start.Col != entries[j].Start.Col {
			return entries[i].Start.Col < entries[j].Start.Col
		}
		return entries[i].Name < entries[j].Name
	})
	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}
	return entries, errs, nil
}

type OutlineNode struct {
	Name     string         `json:"name"`
	Kind     string         `json:"kind"`
	Text     string         `json:"text"`
	Start    Point          `json:"start"`
	End      Point          `json:"end"`
	Children []*OutlineNode `json:"children,omitempty"`
}

type flatOutlineSymbol struct {
	kind string
	sym  Symbol
}

func (e *Engine) Outline(l lang.Language, path string, includeText bool) ([]*OutlineNode, error) {
	syms, err := e.SymbolsText(l, path, includeText)
	if err != nil {
		return nil, err
	}
	flat := make([]flatOutlineSymbol, 0)
	for kind, items := range syms {
		for _, sym := range items {
			if strings.TrimSpace(sym.Name) == "" && strings.TrimSpace(sym.Text) == "" {
				continue
			}
			flat = append(flat, flatOutlineSymbol{kind: kind, sym: sym})
		}
	}
	sort.Slice(flat, func(i, j int) bool {
		a, b := flat[i].sym, flat[j].sym
		if a.Start.Row != b.Start.Row {
			return a.Start.Row < b.Start.Row
		}
		if a.Start.Col != b.Start.Col {
			return a.Start.Col < b.Start.Col
		}
		if a.End.Row != b.End.Row {
			return a.End.Row > b.End.Row
		}
		if a.End.Col != b.End.Col {
			return a.End.Col > b.End.Col
		}
		if flat[i].kind != flat[j].kind {
			return flat[i].kind < flat[j].kind
		}
		return a.Name < b.Name
	})

	roots := make([]*OutlineNode, 0)
	stack := make([]*OutlineNode, 0)
	for _, item := range flat {
		n := &OutlineNode{
			Name:  item.sym.Name,
			Kind:  item.kind,
			Text:  item.sym.Text,
			Start: item.sym.Start,
			End:   item.sym.End,
		}
		for len(stack) > 0 && !containsRange(stack[len(stack)-1].Start, stack[len(stack)-1].End, n.Start, n.End) {
			stack = stack[:len(stack)-1]
		}
		if len(stack) == 0 {
			roots = append(roots, n)
		} else {
			parent := stack[len(stack)-1]
			parent.Children = append(parent.Children, n)
		}
		stack = append(stack, n)
	}
	return roots, nil
}

func containsRange(outerStart, outerEnd, innerStart, innerEnd Point) bool {
	return pointLE(outerStart, innerStart) && pointLE(innerEnd, outerEnd)
}

func pointLE(a, b Point) bool {
	if a.Row != b.Row {
		return a.Row < b.Row
	}
	return a.Col <= b.Col
}
