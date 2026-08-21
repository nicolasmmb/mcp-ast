package yaml

import (
	"testing"

	"mcp-ast/internal/lang"
)

func TestYAMLInterface(t *testing.T) {
	var l lang.Language = YAML{}

	if l.Name() != "yaml" {
		t.Errorf("Name() = %q, want %q", l.Name(), "yaml")
	}
	exts := l.Extensions()
	if len(exts) != 2 || exts[0] != ".yaml" || exts[1] != ".yml" {
		t.Errorf("Extensions() = %v, want [.yaml .yml]", exts)
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
