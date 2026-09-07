package repoindex

import (
	"testing"
	"unsafe"

	"mcp-ast/internal/engine"
)

func fileFacts(path, lang string) IndexedFile {
	return IndexedFile{
		Facts: &engine.FileIndex{
			Language: lang,
			Symbols:  map[string][]engine.Symbol{"functions": {{Name: "Run", Text: "func Run()"}}},
			Usages: []engine.UsageMatch{
				{File: path, Name: "Run", Line: 1, Kind: "definition"},
				{File: path, Name: "Run", Line: 5, Kind: "call-site", Caller: "Main"},
			},
			Complexity: []engine.ComplexityEntry{{Name: "Run", Kind: "functions", Complexity: 2}},
		},
		Size: 1, ModTime: 2,
	}
}

func TestMemoryStoreReplaceAndQuery(t *testing.T) {
	store := NewMemory(0)
	info := store.Create("/repo")
	files := map[string]IndexedFile{"/repo/a.go": fileFacts("/repo/a.go", "go")}
	info, err := store.Replace(info.ID, files, nil)
	if err != nil {
		t.Fatal(err)
	}
	if info.State != "ready" || info.Files != 1 || info.Version != 1 {
		t.Fatalf("unexpected info: %#v", info)
	}
	usages, ok := store.Usages(info.ID, "Run")
	if !ok || len(usages) != 2 {
		t.Fatalf("unexpected usages: %#v", usages)
	}
	ranked, ok := store.Complexity(info.ID, 1)
	if !ok || len(ranked) != 1 || ranked[0].File != "/repo/a.go" {
		t.Fatalf("unexpected complexity: %#v", ranked)
	}
	calls, ok := store.Calls(info.ID)
	if !ok || len(calls.ByCallee["Run"]) != 1 || calls.ByCaller["Main"] == nil {
		t.Fatalf("unexpected call graph: %#v", calls)
	}
}

func TestMemoryStoreApplyDelta(t *testing.T) {
	store := NewMemory(0)
	info := store.Create("/repo")
	info, err := store.Replace(info.ID, map[string]IndexedFile{
		"/repo/a.go": fileFacts("/repo/a.go", "go"),
		"/repo/b.go": fileFacts("/repo/b.go", "go"),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	changed := fileFacts("/repo/b.go", "go")
	changed.Facts.Usages = append(changed.Facts.Usages, engine.UsageMatch{File: "/repo/b.go", Name: "NewThing", Line: 9, Kind: "reference"})
	info, err = store.Apply(info.ID, ChangeSet{
		Updated: map[string]IndexedFile{"/repo/b.go": changed},
		Deleted: []string{"/repo/a.go"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if info.Version != 2 || info.Files != 1 {
		t.Fatalf("unexpected info: %#v", info)
	}
	if _, ok := store.Calls(info.ID); !ok {
		t.Fatal("call graph missing")
	}
	// the a.go call edge must be gone; only b.go's edge remains.
	calls, _ := store.Calls(info.ID)
	edges := calls.ByCaller["Main"]
	if len(edges) != 1 || edges[0].File != "/repo/b.go" {
		t.Fatalf("stale call graph after delete: %#v", edges)
	}
}

func TestMemoryStoreUnused(t *testing.T) {
	store := NewMemory(0)
	info := store.Create("/repo")
	facts := fileFacts("/repo/a.go", "go")
	facts.Facts.Symbols = map[string][]engine.Symbol{
		"functions": {
			{Name: "Dead", Text: "func Dead()"},
			{Name: "Live", Text: "func Live()"},
			{Name: "CommentOnly", Text: "func CommentOnly()"},
		},
	}
	facts.Facts.Usages = []engine.UsageMatch{
		{File: "/repo/a.go", Name: "Dead", Line: 1, Kind: "definition"},
		{File: "/repo/a.go", Name: "Live", Line: 2, Kind: "definition"},
		{File: "/repo/a.go", Name: "Live", Line: 3, Kind: "reference"},
		{File: "/repo/a.go", Name: "CommentOnly", Line: 4, Kind: "definition"},
	}
	if _, err := store.Replace(info.ID, map[string]IndexedFile{"/repo/a.go": facts}, nil); err != nil {
		t.Fatal(err)
	}
	unused, ok := store.Unused(info.ID)
	if !ok || len(unused) != 2 {
		t.Fatalf("want 2 unused symbols, got %#v", unused)
	}
	names := map[string]bool{unused[0].Name: true, unused[1].Name: true}
	if !names["Dead"] || !names["CommentOnly"] || names["Live"] {
		t.Fatalf("unexpected unused set: %#v", unused)
	}
}

func TestCanonicalResolution(t *testing.T) {
	store := NewMemory(0)
	info := store.Create("/repo")
	a := fileFacts("/repo/a.go", "go")
	a.Facts.Symbols = map[string][]engine.Symbol{"functions": {{Name: "Save"}}}
	a.Facts.Usages = []engine.UsageMatch{
		{File: "/repo/a.go", Name: "Save", Line: 1, Kind: "definition"},
		{File: "/repo/a.go", Name: "Save", Line: 2, Kind: "call-site", Caller: "RunA"},
	}
	b := fileFacts("/repo/b.go", "go")
	b.Facts.Symbols = map[string][]engine.Symbol{"functions": {{Name: "Save"}}}
	b.Facts.Usages = []engine.UsageMatch{
		{File: "/repo/b.go", Name: "Save", Line: 1, Kind: "definition"},
		{File: "/repo/b.go", Name: "Save", Line: 2, Kind: "call-site", Caller: "RunB"},
	}
	if _, err := store.Replace(info.ID, map[string]IndexedFile{"/repo/a.go": a, "/repo/b.go": b}, nil); err != nil {
		t.Fatal(err)
	}

	// two declarations: lookup returns both, canonical empty (ambiguous)
	all, ok := store.Usages(info.ID, "Save")
	if !ok || len(all) != 4 {
		t.Fatalf("want 4 Save usages, got %#v", all)
	}
	for _, u := range all {
		if u.Canonical != "" {
			t.Fatalf("ambiguous Save should have empty canonical: %#v", u)
		}
	}

	// delete b.go: Save becomes unambiguous, resolution turns exact
	if _, err := store.Apply(info.ID, ChangeSet{Deleted: []string{"/repo/b.go"}}); err != nil {
		t.Fatal(err)
	}
	canonical := "go|/repo/a.go|functions|Save"
	byCanonical, ok := store.Usages(info.ID, canonical)
	if !ok || len(byCanonical) != 2 {
		t.Fatalf("want 2 usages for canonical %q, got %#v", canonical, byCanonical)
	}
	calls, _ := store.Calls(info.ID)
	for _, e := range calls.ByCallee["Save"] {
		if e.Resolution != "exact" {
			t.Fatalf("Save should resolve exact after delete, got %#v", e)
		}
	}
}

func TestCompactRepresentation(t *testing.T) {
	// The internal posting entry must be far smaller than the public
	// UsageMatch: names, callers and canonicals are interned ids, not
	// per-occurrence strings.
	if sz := unsafe.Sizeof(UsageRef{}); sz >= unsafe.Sizeof(engine.UsageMatch{})*7/10 {
		t.Fatalf("UsageRef too large: %d vs UsageMatch %d", sz, unsafe.Sizeof(engine.UsageMatch{}))
	}
}

func TestCompactEquivalence(t *testing.T) {
	store := NewMemory(0)
	info := store.Create("/repo")
	facts := fileFacts("/repo/a.go", "go")
	facts.Facts.Usages = append(facts.Facts.Usages,
		engine.UsageMatch{File: "/repo/a.go", Name: "Run", Line: 3, Col: 4, Text: "run()", Kind: "call-site", Caller: "Main"},
		engine.UsageMatch{File: "/repo/a.go", Name: "Run", Line: 7, Col: 0, Text: "Run", Kind: "reference"},
	)
	if _, err := store.Replace(info.ID, map[string]IndexedFile{"/repo/a.go": facts}, nil); err != nil {
		t.Fatal(err)
	}
	got, ok := store.Usages(info.ID, "Run")
	if !ok || len(got) != 4 {
		t.Fatalf("want 4 usages, got %#v", got)
	}
	for _, u := range got {
		if u.Name != "Run" || u.File != "/repo/a.go" {
			t.Fatalf("identity lost in compacted postings: %#v", u)
		}
	}
	var callSite *engine.UsageMatch
	for i := range got {
		if got[i].Kind == "call-site" && got[i].Line == 3 {
			callSite = &got[i]
		}
	}
	if callSite == nil || callSite.Caller != "Main" || callSite.Line != 3 || callSite.Col != 4 {
		t.Fatalf("call-site fields lost: %#v", callSite)
	}
}

func TestParseMemoryLimit(t *testing.T) {
	for _, bad := range []string{"2048", "2gb", "2048mbx", "-1mb", ""} {
		if _, err := ParseMemoryLimit(bad); err == nil {
			t.Fatalf("want error for %q", bad)
		}
	}
	for good, want := range map[string]int64{"512mb": 512 << 20, "2048mb": 2048 << 20} {
		got, err := ParseMemoryLimit(good)
		if err != nil || got != want {
			t.Fatalf("%s: got %d, err %v", good, got, err)
		}
	}
}
