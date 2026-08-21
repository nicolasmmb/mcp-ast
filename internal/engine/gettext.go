package engine

import (
	"bytes"
	"fmt"
	"os"

	"mcp-ast/internal/lang"
)

// GetText returns the exact source slice of a 0-based (row, col) range,
// e.g. the positions reported on every Node/Capture/Symbol.
func (e *Engine) GetText(l lang.Language, path string, start, end Point) (string, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", path, err)
	}
	s := byteOffset(src, start.Row, start.Col)
	en := byteOffset(src, end.Row, end.Col)
	if en < s {
		return "", fmt.Errorf("end position %+v precedes start %+v", end, start)
	}
	if en > len(src) {
		en = len(src)
	}
	return string(src[s:en]), nil
}

// byteOffset maps a 0-based (row, col) to a byte offset. col is a byte column.
func byteOffset(src []byte, row, col int) int {
	off := 0
	for r := 0; r < row; r++ {
		nl := bytes.IndexByte(src[off:], '\n')
		if nl < 0 {
			return len(src)
		}
		off += nl + 1
	}
	if off+col > len(src) {
		return len(src)
	}
	return off + col
}
