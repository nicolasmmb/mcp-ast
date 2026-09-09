package repoindex

import (
	"fmt"
	"math/rand"
	"runtime"
	"testing"

	"mcp-ast/internal/engine"
)

// makeFileIndex generates a synthetic FileIndex that mirrors Java-like patterns:
// 10 imports, 20 functions, 5 usages per function, realistic text lengths.
func makeFileIndex(rng *rand.Rand, path string) *engine.FileIndex {
	syms := map[string][]engine.Symbol{}

	// 10 imports
	imports := make([]engine.Symbol, 10)
	for i := range imports {
		imports[i] = engine.Symbol{
			Name: fmt.Sprintf("com.example.pkg%d.Widget", i),
			Text: fmt.Sprintf("import com.example.pkg%d.Widget;", i),
		}
	}
	syms["imports"] = imports

	// 20 functions
	funcs := make([]engine.Symbol, 20)
	for i := range funcs {
		funcs[i] = engine.Symbol{
			Name: fmt.Sprintf("func%d", i),
			Text: fmt.Sprintf("public void func%d() {", i),
		}
	}
	syms["functions"] = funcs

	// ~110 usages: 1 definition + 9 references per function + 10 import refs + 10 call-sites
	usages := make([]engine.UsageMatch, 0, 120)
	for i := 0; i < 20; i++ {
		name := fmt.Sprintf("func%d", i)
		// definition
		usages = append(usages, engine.UsageMatch{
			File: path, Name: name, Line: 10 + i*5, Col: 5,
			Kind: "definition", Text: textLine(rng),
		})
		// 4 references within same file
		for r := 0; r < 4; r++ {
			usages = append(usages, engine.UsageMatch{
				File: path, Name: name, Line: 10 + i*5 + r + 1, Col: 10,
				Kind: "reference", Text: textLine(rng),
			})
		}
		// 1 call-site from a different caller
		if i%2 == 0 {
			usages = append(usages, engine.UsageMatch{
				File: path, Name: name, Line: 10 + i*5 + 5, Col: 8,
				Kind: "call-site", Caller: fmt.Sprintf("func%d", i+1),
				Text: textLine(rng),
			})
		}
	}
	// import references
	for i := 0; i < 10; i++ {
		usages = append(usages, engine.UsageMatch{
			File: path, Name: fmt.Sprintf("com.example.pkg%d.Widget", i),
			Line: i + 1, Col: 1,
			Kind: "import", Text: textLine(rng),
		})
	}

	complexity := make([]engine.ComplexityEntry, 20)
	for i := range complexity {
		complexity[i] = engine.ComplexityEntry{
			Name: fmt.Sprintf("func%d", i), Kind: "functions",
			Complexity: 1 + rng.Intn(10),
		}
	}

	return &engine.FileIndex{
		Language:   "java",
		Symbols:    syms,
		Usages:     usages,
		Complexity: complexity,
	}
}

func textLine(rng *rand.Rand) string {
	// realistic first-line summary: 50-150 bytes
	n := 50 + rng.Intn(100)
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('a' + rng.Intn(26))
	}
	return string(b)
}

// makeRepo creates a repo with n synthetic IndexedFiles and returns it.
// It does NOT call insert/enrich — caller decides what to measure.
func makeRepo(n int) *repo {
	r := newRepo("/bench", 0)
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < n; i++ {
		path := fmt.Sprintf("/bench/src/com/example/File%06d.java", i)
		fid := r.internFile(path)
		facts := makeFileIndex(rng, path)
		r.files[fid] = &IndexedFile{Facts: facts, Size: 1000, ModTime: int64(i)}
	}
	return r
}

// makeIndexedFiles returns the map[string]IndexedFile that Replace() expects.
func makeIndexedFiles(n int) map[string]IndexedFile {
	rng := rand.New(rand.NewSource(42))
	files := make(map[string]IndexedFile, n)
	for i := 0; i < n; i++ {
		path := fmt.Sprintf("/bench/src/com/example/File%06d.java", i)
		facts := makeFileIndex(rng, path)
		files[path] = IndexedFile{Facts: facts, Size: 1000, ModTime: int64(i)}
	}
	return files
}

func heapBytes() uint64 {
	runtime.GC()
 var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}

// --- Benchmarks ---

func BenchmarkInsert1k(b *testing.B) {
	b.ReportAllocs()
	files := makeIndexedFiles(1000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := newRepo("/bench", 0)
		for p, f := range files {
			fid := r.internFile(p)
			cp := f
			r.files[fid] = &cp
			r.insert(fid, &cp)
		}
	}
}

func BenchmarkInsert10k(b *testing.B) {
	b.ReportAllocs()
	files := makeIndexedFiles(10000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := newRepo("/bench", 0)
		for p, f := range files {
			fid := r.internFile(p)
			cp := f
			r.files[fid] = &cp
			r.insert(fid, &cp)
		}
	}
}

func BenchmarkInsert50k(b *testing.B) {
	b.ReportAllocs()
	files := makeIndexedFiles(50000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := newRepo("/bench", 0)
		for p, f := range files {
			fid := r.internFile(p)
			cp := f
			r.files[fid] = &cp
			r.insert(fid, &cp)
		}
	}
}

func BenchmarkEnrich10k(b *testing.B) {
	r := makeRepo(10000)
	files := makeIndexedFiles(10000)
	for p, f := range files {
		fid := r.internFile(p)
		cp := f
		r.files[fid] = &cp
		r.insert(fid, &cp)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.enrich()
	}
}

func BenchmarkEnrich50k(b *testing.B) {
	r := makeRepo(50000)
	files := makeIndexedFiles(50000)
	for p, f := range files {
		fid := r.internFile(p)
		cp := f
		r.files[fid] = &cp
		r.insert(fid, &cp)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.enrich()
	}
}

func BenchmarkBuildGraphs10k(b *testing.B) {
	r := makeRepo(10000)
	files := makeIndexedFiles(10000)
	for p, f := range files {
		fid := r.internFile(p)
		cp := f
		r.files[fid] = &cp
		r.insert(fid, &cp)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = buildCallGraph(r.filesByPath())
		_ = buildImportGraph(r.filesByPath())
	}
}

func BenchmarkBuildGraphs50k(b *testing.B) {
	r := makeRepo(50000)
	files := makeIndexedFiles(50000)
	for p, f := range files {
		fid := r.internFile(p)
		cp := f
		r.files[fid] = &cp
		r.insert(fid, &cp)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = buildCallGraph(r.filesByPath())
		_ = buildImportGraph(r.filesByPath())
	}
}

func BenchmarkSnapshotSaveLoad10k(b *testing.B) {
	r := makeRepo(10000)
	files := makeIndexedFiles(10000)
	for p, f := range files {
		fid := r.internFile(p)
		cp := f
		r.files[fid] = &cp
		r.insert(fid, &cp)
	}
	snap := &Snapshot{
		Header: SnapshotHeader{SchemaVersion: SnapshotSchemaVersion, ToolVersion: "bench", Root: "/bench"},
		Files:  make(map[string]IndexedFile, len(r.files)),
	}
	for fid, f := range r.files {
		snap.Files[r.filePath(fid)] = *f
	}
 path := b.TempDir() + "/snapshot.gob"
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := SaveSnapshot(path, snap); err != nil {
			b.Fatal(err)
		}
		_, err := LoadSnapshot(path)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSnapshotSaveLoad50k(b *testing.B) {
	r := makeRepo(50000)
	files := makeIndexedFiles(50000)
	for p, f := range files {
		fid := r.internFile(p)
		cp := f
		r.files[fid] = &cp
		r.insert(fid, &cp)
	}
	snap := &Snapshot{
		Header: SnapshotHeader{SchemaVersion: SnapshotSchemaVersion, ToolVersion: "bench", Root: "/bench"},
		Files:  make(map[string]IndexedFile, len(r.files)),
	}
	for fid, f := range r.files {
		snap.Files[r.filePath(fid)] = *f
	}
	path := b.TempDir() + "/snapshot.gob"
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := SaveSnapshot(path, snap); err != nil {
			b.Fatal(err)
		}
		_, err := LoadSnapshot(path)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// TestMemoryProfile logs heap before/after a full Replace for manual inspection.
func TestMemoryProfile(t *testing.T) {
	if testing.Short() {
		t.Skip("skip in short mode")
	}
	for _, n := range []int{1000, 10000, 50000} {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			before := heapBytes()
			store := NewMemory(0)
			info := store.Create("/bench")
			files := makeIndexedFiles(n)
			_, err := store.Replace(info.ID, files, nil)
			if err != nil {
				t.Fatal(err)
			}
			after := heapBytes()
			info2, _ := store.Info(info.ID)
			t.Logf("n=%d  heap_before=%d  heap_after=%d  delta=%d  store_estimate=%d  ratio_actual_vs_estimate=%.2f",
				n, before, after, after-before, info2.MemoryBytes,
				float64(after-before)/float64(max(info2.MemoryBytes, 1)))
		})
	}
}

func max(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
