package bash

import (
	"testing"

	"mcp-ast/internal/lang"
)

func TestBashInterface(t *testing.T) {
	var l lang.Language = Bash{}

	if l.Name() != "bash" {
		t.Errorf("Name() = %q, want %q", l.Name(), "bash")
	}
	exts := l.Extensions()
	if len(exts) != 2 || exts[0] != ".sh" || exts[1] != ".bash" {
		t.Errorf("Extensions() = %v, want [.sh .bash]", exts)
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
