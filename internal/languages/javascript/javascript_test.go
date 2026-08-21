package javascript

import (
	"testing"

	"mcp-ast/internal/lang"
)

func TestJavaScriptInterface(t *testing.T) {
	var l lang.Language = JavaScript{}

	if l.Name() != "javascript" {
		t.Errorf("Name() = %q, want %q", l.Name(), "javascript")
	}
	exts := l.Extensions()
	if len(exts) != 2 || exts[0] != ".js" || exts[1] != ".jsx" {
		t.Errorf("Extensions() = %v, want [.js .jsx]", exts)
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
