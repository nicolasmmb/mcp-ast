package repoindex

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Cursor is an opaque pagination cursor: the index version plus an offset
// into a deterministic ordering. A cursor from a different index version is
// rejected so pages never mix snapshots.
type Cursor struct {
	Version uint64
	Offset  int
}

var ErrStaleCursor = errors.New("stale cursor")

func EncodeCursor(v uint64, offset int) string {
	raw := strconv.FormatUint(v, 10) + ":" + strconv.Itoa(offset)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func DecodeCursor(s string) (Cursor, error) {
	if s == "" {
		return Cursor{}, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return Cursor{}, fmt.Errorf("invalid cursor: %w", err)
	}
	parts := strings.SplitN(string(data), ":", 2)
	if len(parts) != 2 {
		return Cursor{}, fmt.Errorf("invalid cursor %q", s)
	}
	v, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		return Cursor{}, fmt.Errorf("invalid cursor version: %w", err)
	}
	off, err := strconv.Atoi(parts[1])
	if err != nil || off < 0 {
		return Cursor{}, fmt.Errorf("invalid cursor offset: %w", err)
	}
	return Cursor{Version: v, Offset: off}, nil
}

// CheckCursor validates a cursor against the current index version.
func CheckCursor(s string, current uint64) (Cursor, error) {
	c, err := DecodeCursor(s)
	if err != nil {
		return Cursor{}, err
	}
	if s != "" && c.Version != current {
		return Cursor{}, fmt.Errorf("%w: cursor is from index version %d, current is %d; re-run the query", ErrStaleCursor, c.Version, current)
	}
	return c, nil
}
