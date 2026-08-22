package rust

import (
	"testing"

	"mcp-ast/internal/lang"
)

func TestRustInterface(t *testing.T) {
	var l lang.Language = Rust{}

	if l.Name() != "rust" {
		t.Errorf("Name() = %q, want %q", l.Name(), "rust")
	}
	exts := l.Extensions()
	if len(exts) != 1 || exts[0] != ".rs" {
		t.Errorf("Extensions() = %v, want [.rs]", exts)
	}
	if l.Language() == nil {
		t.Error("Language() returned nil")
	}
	sq := l.SymbolQueries()
	if len(sq) == 0 {
		t.Error("SymbolQueries() is empty")
	}
	dk := l.DecisionKinds()
	if len(dk) == 0 {
		t.Error("DecisionKinds() is empty")
	}
	aq := l.AuxQueries()
	if _, ok := aq["identifiers"]; !ok {
		t.Error("AuxQueries() missing 'identifiers'")
	}
	if _, ok := aq["calls"]; !ok {
		t.Error("AuxQueries() missing 'calls'")
	}
}
