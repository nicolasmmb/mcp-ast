package repoindex

import (
	"encoding/gob"
	"fmt"
	"os"
	"path/filepath"
)

// SnapshotSchemaVersion bumps invalidate stored snapshots. Changing the
// IndexedFile/FileIndex layout or the enrichment semantics must bump it.
const SnapshotSchemaVersion = 1

// SnapshotHeader is the invalidation key: a stored snapshot is reused only
// when schema, tool and root match. Grammar versions are not exposed by the
// language interface; the tool version covers releases that change grammars.
type SnapshotHeader struct {
	SchemaVersion int
	ToolVersion   string
	Root          string
	Languages     string
}

// Snapshot is the persisted repository state: the same facts and metadata the
// in-memory store holds, serialized in a versioned binary format (gob). ASTs
// and source bytes are never persisted.
type Snapshot struct {
	Header SnapshotHeader
	Files  map[string]IndexedFile
}

// Valid reports whether a stored snapshot can be reused for the current
// process configuration.
func (h SnapshotHeader) Valid(schema int, toolVersion, root, languages string) bool {
	return h.SchemaVersion == schema && h.ToolVersion == toolVersion && h.Root == root && h.Languages == languages
}

// SaveSnapshot writes the snapshot atomically (temp file + rename).
func SaveSnapshot(path string, snap *Snapshot) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if err := gob.NewEncoder(f).Encode(snap); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("encoding snapshot: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// LoadSnapshot reads a snapshot back. A missing file is an error so callers
// can fall back to a full build.
func LoadSnapshot(path string) (*Snapshot, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	snap := &Snapshot{}
	if err := gob.NewDecoder(f).Decode(snap); err != nil {
		return nil, fmt.Errorf("decoding snapshot: %w", err)
	}
	return snap, nil
}
