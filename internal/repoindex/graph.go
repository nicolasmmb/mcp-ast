package repoindex

import (
	"path/filepath"
	"sort"
	"strings"
)

type CallEdge struct {
	File       string `json:"file"`
	Caller     string `json:"caller"`
	Callee     string `json:"callee"`
	Count      int    `json:"count"`
	Resolution string `json:"resolution,omitempty"` // exact | candidate | unresolved
}

type ImportEdge struct {
	File      string `json:"file"`
	Specifier string `json:"specifier"`
	Resolved  string `json:"resolved,omitempty"`
}

type CallGraph struct {
	ByCaller map[string][]CallEdge `json:"by_caller"`
	ByCallee map[string][]CallEdge `json:"by_callee"`
}

type ImportGraph struct {
	ByFile map[string][]ImportEdge `json:"by_file"`
	BySpec map[string][]ImportEdge `json:"by_spec"`
}

// buildCallGraph aggregates call-sites from indexed usages: one CallEdge per
// (file, caller, callee) pair with a count, indexed by caller name and callee
// name. This is a lexical, not semantic, graph. Each edge carries a
// resolution: exact when the callee name has exactly one declaration in the
// index, candidate when it has several, unresolved when none.
func buildCallGraph(files map[string]*IndexedFile) CallGraph {
	declCount := map[string]int{}
	for _, f := range files {
		for kind, syms := range f.Facts.Symbols {
			if kind == "imports" {
				continue
			}
			for _, s := range syms {
				if name := strings.TrimSpace(s.Name); name != "" {
					declCount[name]++
				}
			}
		}
	}
	resolution := func(callee string) string {
		switch declCount[callee] {
		case 1:
			return "exact"
		case 0:
			return "unresolved"
		default:
			return "candidate"
		}
	}
	agg := map[string]*CallEdge{}
	for path, f := range files {
		for _, u := range f.Facts.Usages {
			if u.Kind != "call-site" || u.Caller == "" || u.Name == "" {
				continue
			}
			key := path + "|" + u.Caller + "|" + u.Name
			e := agg[key]
			if e == nil {
				e = &CallEdge{File: path, Caller: u.Caller, Callee: u.Name, Resolution: resolution(u.Name)}
				agg[key] = e
			}
			e.Count++
		}
	}
	g := CallGraph{ByCaller: map[string][]CallEdge{}, ByCallee: map[string][]CallEdge{}}
	for _, e := range agg {
		g.ByCaller[e.Caller] = append(g.ByCaller[e.Caller], *e)
		g.ByCallee[e.Callee] = append(g.ByCallee[e.Callee], *e)
	}
	for _, es := range g.ByCaller {
		sortEdges(es)
	}
	for _, es := range g.ByCallee {
		sortEdges(es)
	}
	return g
}

func sortEdges(edges []CallEdge) {
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].Caller != edges[j].Caller {
			return edges[i].Caller < edges[j].Caller
		}
		return edges[i].File < edges[j].File
	})
}

// buildImportGraph extracts import specifiers from symbol kind "imports" and
// links left-to-right: file -> spec value. Relative specifiers ("./x", "../x")
// that resolve to an indexed file also produce file -> file edges (Resolved).
func buildImportGraph(files map[string]*IndexedFile) ImportGraph {
	g := ImportGraph{ByFile: map[string][]ImportEdge{}, BySpec: map[string][]ImportEdge{}}
	for path, f := range files {
		seen := map[string]bool{}
		for kind, syms := range f.Facts.Symbols {
			if kind != "imports" {
				continue
			}
			for _, sym := range syms {
				spec := importSpecifier(sym.Text)
				if spec == "" || seen[path+"|"+spec] {
					continue
				}
				seen[path+"|"+spec] = true
				edge := ImportEdge{File: path, Specifier: spec}
				g.ByFile[path] = append(g.ByFile[path], edge)
				g.BySpec[spec] = append(g.BySpec[spec], edge)
				if target, ok := resolveLocalImport(path, files, spec); ok {
					edgeResolved := ImportEdge{File: path, Specifier: spec, Resolved: target}
					g.ByFile[path] = append(g.ByFile[path], edgeResolved)
					g.BySpec[target] = append(g.BySpec[target], edgeResolved)
				}
			}
		}
	}
	return g
}

func importSpecifier(text string) string {
	for _, opener := range []string{`"`, "`", "'"} {
		start := strings.Index(text, opener)
		if start < 0 {
			continue
		}
		end := strings.LastIndex(text, opener)
		if end > start {
			inner := text[start+1 : end]
			inner = strings.TrimSpace(inner)
			inner = strings.TrimPrefix(inner, "path.")
			if inner != "" {
				return inner
			}
		}
	}
	return ""
}

func resolveLocalImport(fromPath string, files map[string]*IndexedFile, spec string) (string, bool) {
	if !strings.HasPrefix(spec, "./") && !strings.HasPrefix(spec, "../") {
		if strings.HasPrefix(spec, "internal/") {
			// exact absolute-prefix match is non-deterministic without module
			// config; conservatively try candidates under the index root.
			for p := range files {
				if strings.HasSuffix(p, spec) {
					return p, true
				}
			}
		}
		return "", false
	}
	rel := filepath.Join(filepath.Dir(fromPath), filepath.Clean(spec))
	clean := filepath.Clean(rel)
	target, err := filepath.Abs(clean)
	if err != nil {
		return "", false
	}
	if _, ok := files[target]; ok {
		return target, true
	}
	return "", false
}

// ResolutionCounts returns the edge count per resolution kind.
func (g *CallGraph) ResolutionCounts() map[string]int {
	out := map[string]int{}
	for _, edges := range g.ByCaller {
		for _, e := range edges {
			kind := e.Resolution
			if kind == "" {
				kind = "unresolved"
			}
			out[kind]++
		}
	}
	return out
}

// ImpactNode is one dependant found by impact search.
type ImpactNode struct {
	Name     string `json:"name"`
	Distance int    `json:"distance"`
}

type ImpactResult struct {
	Nodes            []ImpactNode   `json:"nodes"`
	Truncated        bool           `json:"truncated,omitempty"`
	NextCursor       string         `json:"next_cursor,omitempty"`
	ResolutionCounts map[string]int `json:"resolution_counts,omitempty"`
}

// Impact walks the graph from target. Reverse direction answers "who depends
// on this?", forward answers "what does this reference?". depth 0 = direct
// only; limit caps the page size (0 = 512); offset pages into the
// deterministic BFS order.
func Impact(adj map[string][]string, target string, depth, limit, offset int) ImpactResult {
	if limit <= 0 {
		limit = 512
	}
	seen := map[string]int{target: 0}
	level := []string{target}
	all := []ImpactNode{}
	for d := 1; len(level) > 0; d++ {
		if depth > 0 && d > depth {
			break
		}
		var next []string
		for _, cur := range level {
			for _, nb := range sortedNeighbors(adj, cur) {
				if _, ok := seen[nb]; ok {
					continue
				}
				seen[nb] = d
				all = append(all, ImpactNode{Name: nb, Distance: d})
				next = append(next, nb)
			}
		}
		level = next
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Distance != all[j].Distance {
			return all[i].Distance < all[j].Distance
		}
		return all[i].Name < all[j].Name
	})
	result := ImpactResult{}
	if offset > len(all) {
		offset = len(all)
	}
	end := offset + limit
	if end >= len(all) {
		end = len(all)
	} else {
		result.Truncated = true
	}
	result.Nodes = append([]ImpactNode(nil), all[offset:end]...)
	return result
}

func sortedNeighbors(adj map[string][]string, n string) []string {
	ns := adj[n]
	if len(ns) <= 1 {
		return ns
	}
	out := append([]string(nil), ns...)
	sort.Strings(out)
	return out
}

// Cycles returns SCCs with more than one node, or single nodes with a self
// loop, from the given graph. Each cycle lists its nodes in deterministic
// order.
func Cycles(nodes map[string]bool, adj map[string][]string) [][]string {
	var out [][]string
	for _, scc := range Tarjan(nodes, adj) {
		if len(scc) > 1 {
			out = append(out, scc)
			continue
		}
		v := scc[0]
		if contains(adj[v], v) {
			out = append(out, scc)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		ti, tj := strings.Join(out[i], "\x00"), strings.Join(out[j], "\x00")
		return ti < tj
	})
	return out
}

// Topology condenses the graph into its SCC DAG and returns layers of
// supernodes in topological order (Kahn), deterministic by name.
func Topology(nodes map[string]bool, adj map[string][]string) [][]string {
	sccs := Tarjan(nodes, adj)
	sccIndex := map[string]int{}
	for i, scc := range sccs {
		for _, v := range scc {
			sccIndex[v] = i
		}
	}
	incount := make([]int, len(sccs))
	outEdges := make(map[int]map[int]bool)
	for i := range sccs {
		outEdges[i] = map[int]bool{}
	}
	for i, scc := range sccs {
		for _, v := range scc {
			for _, to := range adj[v] {
				j := sccIndex[to]
				if i != j && !outEdges[i][j] {
					outEdges[i][j] = true
					incount[j]++
				}
			}
		}
	}
	queue := []int{}
	for i := range sccs {
		if incount[i] == 0 {
			queue = append(queue, i)
		}
	}
	sort.Ints(queue)
	var layers [][]string
	for len(queue) > 0 {
		var next []int
		var layer []string
		for _, i := range queue {
			layer = append(layer, "SCC{"+strings.Join(sccs[i], ",")+"}")
			for j := range outEdges[i] {
				incount[j]--
				if incount[j] == 0 {
					next = append(next, j)
				}
			}
		}
		sort.Slice(layer, func(a, b int) bool { return layer[a] < layer[b] })
		layers = append(layers, layer)
		sort.Ints(next)
		queue = next
	}
	if len(layers) == 0 && len(nodes) > 0 {
		var names []string
		for n := range nodes {
			names = append(names, n)
		}
		sort.Strings(names)
		layers = append(layers, names)
	}
	return layers
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

// Tarjan runs the SCC algorithm over the node set and adjacency.
func Tarjan(nodes map[string]bool, adj map[string][]string) [][]string {
	index := map[string]int{}
	low := map[string]int{}
	onStack := map[string]bool{}
	var stack []string
	var sccs [][]string
	counter := 0
	var strong func(string)
	strong = func(v string) {
		index[v] = counter
		low[v] = counter
		counter++
		stack = append(stack, v)
		onStack[v] = true
		for _, w := range sortedNeighbors(adj, v) {
			if _, ok := index[w]; !ok {
				strong(w)
				if low[w] < low[v] {
					low[v] = low[w]
				}
			} else if onStack[w] && index[w] < low[v] {
				low[v] = index[w]
			}
		}
		if low[v] == index[v] {
			var scc []string
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				scc = append(scc, w)
				if w == v {
					break
				}
			}
			sort.Strings(scc)
			sccs = append(sccs, scc)
		}
	}
	var names []string
	for n := range nodes {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if _, ok := index[n]; !ok {
			strong(n)
		}
	}
	return sccs
}

// CallAdjacency builds a name -> neighbors adjacency for the call graph.
func (g *CallGraph) Adjacency(reverse bool) map[string][]string {
	adj := map[string][]string{}
	dedupe := func(from, to string) {
		if from == "" || to == "" {
			return
		}
		for _, v := range adj[from] {
			if v == to {
				return
			}
		}
		adj[from] = append(adj[from], to)
	}
	if reverse {
		for target, edges := range g.ByCallee {
			for _, e := range edges {
				dedupe(target, e.Caller)
			}
		}
	} else {
		for caller, edges := range g.ByCaller {
			for _, e := range edges {
				dedupe(caller, e.Callee)
			}
		}
	}
	return adj
}

// Nodes returns the set of node names present in the graph.
func (g *CallGraph) Nodes() map[string]bool {
	nodes := map[string]bool{}
	for from := range g.ByCaller {
		nodes[from] = true
	}
	for to := range g.ByCallee {
		nodes[to] = true
	}
	return nodes
}

// Adjacency builds a spec/file -> neighbors adjacency for the import graph.
func (g *ImportGraph) Adjacency(reverse bool) map[string][]string {
	adj := map[string][]string{}
	add := func(from, to string) {
		if from == "" || to == "" || from == to {
			return
		}
		for _, v := range adj[from] {
			if v == to {
				return
			}
		}
		adj[from] = append(adj[from], to)
	}
	if reverse {
		for spec, edges := range g.BySpec {
			for _, e := range edges {
				add(spec, e.File)
				if e.Resolved != "" {
					// dependents of a resolved file are the files importing it
					add(importSpecifierKey(e.Specifier), e.File)
				}
			}
		}
		for file, edges := range g.ByFile {
			for _, e := range edges {
				if e.Resolved != "" {
					add(e.Resolved, e.File)
				}
				_ = file
			}
		}
	} else {
		for file, edges := range g.ByFile {
			for _, e := range edges {
				add(file, importSpecifierKey(e.Specifier))
				if e.Resolved != "" {
					add(file, e.Resolved)
				}
			}
		}
	}
	return adj
}

func importSpecifierKey(spec string) string { return "import:" + spec }

// Nodes returns the set of node names present in the import graph.
func (g *ImportGraph) Nodes() map[string]bool {
	nodes := map[string]bool{}
	for file := range g.ByFile {
		nodes[file] = true
	}
	for spec := range g.BySpec {
		nodes[spec] = true
		nodes[importSpecifierKey(spec)] = true
	}
	return nodes
}
