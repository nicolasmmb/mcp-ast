package typescript

import (
	"testing"

	"mcp-ast/internal/lang"
)

func TestTypeScriptInterface(t *testing.T) {
	var l lang.Language = TypeScript{}

	if l.Name() != "typescript" {
		t.Errorf("Name() = %q, want %q", l.Name(), "typescript")
	}
	exts := l.Extensions()
	if len(exts) != 2 || exts[0] != ".ts" || exts[1] != ".tsx" {
		t.Errorf("Extensions() = %v, want [.ts .tsx]", exts)
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
