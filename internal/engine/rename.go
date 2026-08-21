package engine

import (
	"context"

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
func (e *Engine) RenamePreview(ctx context.Context, dir, name, langName string, limit int) ([]RenameMatch, map[string]string, error) {
	var filter lang.Language
	if langName != "" {
		var err error
		filter, err = e.reg.Resolve(langName, dir)
		if err != nil {
			return nil, nil, err
		}
	}
	files, errs, err := e.ScanSymbols(ctx, dir, filter)
	if err != nil {
		return nil, nil, err
	}
	matches := []RenameMatch{}
	for path := range files {
		var l lang.Language
		if filter != nil {
			l = filter
		} else {
			ll, err := e.reg.Resolve("", path)
			if err != nil {
				continue
			}
			l = ll
		}
		qs, ok := l.AuxQueries()["identifiers"]
		if !ok {
			continue
		}
		src, tree, err := e.parseFile(l, path)
		if err != nil {
			errs[path] = err.Error()
			continue
		}
		root := tree.RootNode()
		// collect definition positions from the symbol queries' @name captures
		defPos := make(map[Point]bool)
		for _, sq := range l.SymbolQueries() {
			q, qerr := ts.NewQuery(l.Language(), sq)
			if qerr != nil {
				continue
			}
			names := q.CaptureNames()
			cc := ts.NewQueryCursor()
			it := cc.Matches(q, root, src)
			for m := it.Next(); m != nil; m = it.Next() {
				for _, cap := range m.Captures {
					if names[cap.Index] == "name" && cap.Node.Utf8Text(src) == name {
						defPos[point(cap.Node.StartPosition())] = true
					}
				}
			}
			cc.Close()
			q.Close()
		}
		q, qerr := ts.NewQuery(l.Language(), qs)
		if qerr != nil {
			tree.Close()
			errs[path] = qerr.Message
			continue
		}
		c := ts.NewQueryCursor()
		it := c.Matches(q, root, src)
		for m := it.Next(); m != nil; m = it.Next() {
			for _, cap := range m.Captures {
				text := cap.Node.Utf8Text(src)
				if text != name {
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
					break
				}
			}
			if limit > 0 && len(matches) >= limit {
				break
			}
		}
		c.Close()
		q.Close()
		tree.Close()
		if limit > 0 && len(matches) >= limit {
			return matches, errs, nil
		}
	}
	return matches, errs, nil
}
