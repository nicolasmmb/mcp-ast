package repoindex

import (
	"testing"

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
