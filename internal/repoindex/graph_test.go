package repoindex

import "testing"

func TestTarjanAndCycles(t *testing.T) {
	nodes := map[string]bool{"A": true, "B": true, "C": true, "D": true}
	adj := map[string][]string{"A": {"B"}, "B": {"C"}, "C": {"A", "D"}, "D": nil}
	sccs := Tarjan(nodes, adj)
	if len(sccs) != 2 {
		t.Fatalf("want 2 SCCs, got %#v", sccs)
	}
	cycles := Cycles(nodes, adj)
	if len(cycles) != 1 || len(cycles[0]) != 3 {
		t.Fatalf("want one 3-node cycle, got %#v", cycles)
	}
	if !sameSet(cycleSet(cycles[0]), cycleSet([]string{"A", "B", "C"})) {
		t.Fatalf("cycle mismatch: %v", cycles[0])
	}
}

func TestSelfLoopCycle(t *testing.T) {
	nodes := map[string]bool{"R": true, "X": true}
	adj := map[string][]string{"R": {"R"}, "X": nil}
	cycles := Cycles(nodes, adj)
	if len(cycles) != 1 || cycles[0][0] != "R" {
		t.Fatalf("want self-loop cycle R, got %#v", cycles)
	}
}

func TestTopologyLayers(t *testing.T) {
	nodes := map[string]bool{"A": true, "B": true, "C": true, "D": true}
	adj := map[string][]string{"A": {"B"}, "B": {"C"}, "C": {"A", "D"}, "D": nil}
	layers := Topology(nodes, adj)
	// SCC(A,B,C) -> D
	if len(layers) != 2 || len(layers[0]) != 1 || len(layers[1]) != 1 {
		t.Fatalf("unexpected layers: %#v", layers)
	}
}

func TestImpactBFS(t *testing.T) {
	adj := map[string][]string{
		"Run": {"Helper"}, "Helper": {"Main"},
	}
	res := Impact(adj, "Run", 0, 0)
	if len(res.Nodes) != 2 {
		t.Fatalf("want 2 dependants for Run, got %#v", res.Nodes)
	}
	if res.Nodes[0].Name != "Helper" || res.Nodes[0].Distance != 1 {
		t.Fatalf("unexpected first node: %#v", res.Nodes[0])
	}
	if res.Nodes[1].Name != "Main" || res.Nodes[1].Distance != 2 {
		t.Fatalf("unexpected second node: %#v", res.Nodes[1])
	}
}

func TestImpactLimit(t *testing.T) {
	adj := map[string][]string{"A": {"B", "C"}}
	res := Impact(adj, "A", 0, 1)
	if !res.Truncated || len(res.Nodes) != 1 {
		t.Fatalf("want truncated single node, got %#v", res)
	}
}

func cycleSet(ns []string) map[string]bool {
	out := map[string]bool{}
	for _, n := range ns {
		out[n] = true
	}
	return out
}

func sameSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}
