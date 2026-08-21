package engine

import (
	"context"
	"fmt"

	ts "github.com/tree-sitter/go-tree-sitter"

	"mcp-ast/internal/lang"
)

// RenameMatch is a single occurrence of a name to consider during a rename.
type RenameMatch struct {
	File       string `json:"file"`
	Line       int    `json:"line"`
	Col        int    `json:"col"`
	Text       string `json:"text"`
	Definition bool   `json:"definition"`
}

// RenamePreview finds every occurrence of name across the directory's
// recognized files (via the language's identifier query) and flags which
// occurrences are declarations. Useful to preview a rename before editing.
func (e *Engine) RenamePreview(ctx context.Context, dir, name string, filter lang.Language, limit int) ([]RenameMatch, map[string]string, error) {
	matches := []RenameMatch{}
	errs, err := e.walkFiles(ctx, dir, filter, func(path string, l lang.Language) error {
		qs, ok := l.AuxQueries()["identifiers"]
		if !ok {
			return nil
		}
		src, tree, err := e.parseFile(l, path)
		if err != nil {
			return err
		}
		defer tree.Close()
		root := tree.RootNode()
		defPos := definitionPositions(l, root, src, name)
		q, qerr := ts.NewQuery(l.Language(), qs)
		if qerr != nil {
			return fmt.Errorf("invalid identifier query: %s", qerr.Message)
		}
		defer q.Close()
		c := ts.NewQueryCursor()
		defer c.Close()
		it := c.Matches(q, root, src)
		for m := it.Next(); m != nil; m = it.Next() {
			for _, cap := range m.Captures {
				if cap.Node.Utf8Text(src) != name {
					continue
				}
				start := point(cap.Node.StartPosition())
				matches = append(matches, RenameMatch{
					File:       path,
					Line:       start.Row + 1,
					Col:        start.Col,
					Text:       firstLine(cap.Node.Parent().Utf8Text(src)),
					Definition: defPos[start],
				})
				if limit > 0 && len(matches) >= limit {
					return nil
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return matches, errs, nil
}

// definitionPositions collects the start positions of every declaration of
// name in the file, from the @name captures of the symbol queries.
func definitionPositions(l lang.Language, root *ts.Node, src []byte, name string) map[Point]bool {
	defPos := make(map[Point]bool)
	for _, sq := range l.SymbolQueries() {
		q, qerr := ts.NewQuery(l.Language(), sq)
		if qerr != nil {
			continue
		}
		names := q.CaptureNames()
		c := ts.NewQueryCursor()
		it := c.Matches(q, root, src)
		for m := it.Next(); m != nil; m = it.Next() {
			for _, cap := range m.Captures {
				if names[cap.Index] == "name" && cap.Node.Utf8Text(src) == name {
					defPos[point(cap.Node.StartPosition())] = true
				}
			}
		}
		c.Close()
		q.Close()
	}
	return defPos
}
