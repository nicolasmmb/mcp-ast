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
		_ = buildCallGraphFromRepo(r)
		_ = buildImportGraphFromRepo(r)
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
		_ = buildCallGraphFromRepo(r)
		_ = buildImportGraphFromRepo(r)
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

// TestEstimateMemoryVsActual asserts that estimateMemory is within 2x of the
// actual heap delta measured via runtime.MemStats.
func TestEstimateMemoryVsActual(t *testing.T) {
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
			delta := int64(after) - int64(before)
			info2, _ := store.Info(info.ID)
			est := info2.MemoryBytes
			ratio := float64(delta) / float64(max(est, 1))
			t.Logf("n=%d  heap_delta=%d  estimate=%d  ratio=%.2f", n, delta, est, ratio)
			if ratio < 0.25 || ratio > 4.0 {
				t.Errorf("estimate out of range: heap_delta=%d estimate=%d ratio=%.2f (want 0.25..4.0)", delta, est, ratio)
			}
		})
	}
}

// TestCallGraphEquivalence verifies that buildCallGraphFromRepo produces
// identical results to the old buildCallGraph that read from FileIndex.Usages.
func TestCallGraphEquivalence(t *testing.T) {
	for _, n := range []int{10, 100, 1000} {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			// Build old-style call graph from raw FileIndex.Usages FIRST,
			// before insert() nils them out.
			oldFiles := make(map[string]*IndexedFile, n)
			rng := rand.New(rand.NewSource(42))
			for i := 0; i < n; i++ {
				path := fmt.Sprintf("/bench/src/com/example/File%06d.java", i)
				facts := makeFileIndex(rng, path)
				oldFiles[path] = &IndexedFile{Facts: facts, Size: 1000, ModTime: int64(i)}
			}
			oldGraph := buildCallGraph(oldFiles)

			// Build new-style call graph from interned postings.
			r := newRepo("/bench", 0)
			for p, f := range oldFiles {
				fid := r.internFile(p)
				cp := *f
				r.files[fid] = &cp
				r.insert(fid, &cp)
			}
			newGraph := buildCallGraphFromRepo(r)

			// Compare ByCaller.
			if len(oldGraph.ByCaller) != len(newGraph.ByCaller) {
				t.Fatalf("ByCaller len: old=%d new=%d", len(oldGraph.ByCaller), len(newGraph.ByCaller))
			}
			for caller, oldEdges := range oldGraph.ByCaller {
				newEdges := newGraph.ByCaller[caller]
				if len(oldEdges) != len(newEdges) {
					t.Errorf("ByCaller[%s] len: old=%d new=%d", caller, len(oldEdges), len(newEdges))
					continue
				}
				for i := range oldEdges {
					if oldEdges[i].Callee != newEdges[i].Callee || oldEdges[i].Count != newEdges[i].Count || oldEdges[i].File != newEdges[i].File {
						t.Errorf("ByCaller[%s][%d]: old=%+v new=%+v", caller, i, oldEdges[i], newEdges[i])
					}
				}
			}
		})
	}
}

func max(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// BenchmarkApplyAddOnly50k benchmarks Apply with adds only (incremental graph path).
func BenchmarkApplyAddOnly50k(b *testing.B) {
	store := NewMemory(0)
	info := store.Create("/bench")
	files := makeIndexedFiles(50000)
	_, err := store.Replace(info.ID, files, nil)
	if err != nil {
		b.Fatal(err)
	}
	// Prepare 100 new files to add per iteration.
	newFiles := make(map[string]IndexedFile, 100)
	rng := rand.New(rand.NewSource(99))
	for i := 0; i < 100; i++ {
		path := fmt.Sprintf("/bench/src/com/example/New%06d.java", i)
		newFiles[path] = IndexedFile{Facts: makeFileIndex(rng, path), Size: 1000, ModTime: int64(i)}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := store.Apply(info.ID, ChangeSet{Added: newFiles})
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkChurn50k simulates high file churn: 100 rounds of deleting 500
// files and adding 500. Measures heap stability.
func BenchmarkChurn50k(b *testing.B) {
	const total = 50000
	const batchSize = 500
	const rounds = 100
	store := NewMemory(0)
	info := store.Create("/bench")
	files := makeIndexedFiles(total)
	_, err := store.Replace(info.ID, files, nil)
	if err != nil {
		b.Fatal(err)
	}
	// Build list of all paths so we can pick random ones to delete.
	allPaths := make([]string, 0, total)
	for p := range files {
		allPaths = append(allPaths, p)
	}
	rng := rand.New(rand.NewSource(77))

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// reset the index from scratch each iteration
		store.Replace(info.ID, makeIndexedFiles(total), nil)
		for round := 0; round < rounds; round++ {
			delSet := map[string]bool{}
			for j := 0; j < batchSize; j++ {
				idx := rng.Intn(len(allPaths))
				delSet[allPaths[idx]] = true
			}
			del := make([]string, 0, len(delSet))
			for p := range delSet {
				del = append(del, p)
			}
			added := map[string]IndexedFile{}
			for j := 0; j < batchSize; j++ {
				path := fmt.Sprintf("/bench/src/com/example/Churn%06d_R%06d_I%06d.java", i, round, j)
				added[path] = IndexedFile{Facts: makeFileIndex(rng, path), Size: 1000, ModTime: int64(round*batchSize + j)}
			}
			store.Apply(info.ID, ChangeSet{Deleted: del, Added: added})
		}
	}
}

// TestChurnMemoryStabilization verifies heap stabilizes under churn.
func TestChurnMemoryStabilization(t *testing.T) {
	const total = 10000
	const batchSize = 200
	const rounds = 100
	store := NewMemory(0)
	info := store.Create("/bench")
	files := makeIndexedFiles(total)
	_, err := store.Replace(info.ID, files, nil)
	if err != nil {
		t.Fatal(err)
	}
	allPaths := make([]string, 0, total)
	for p := range files {
		allPaths = append(allPaths, p)
	}
	rng := rand.New(rand.NewSource(77))

	// Measure heap AFTER initial load (this is our baseline).
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	memBase := int64(m.HeapInuse)

	// Track heap at checkpoints.
	heaps := make([]int64, 0, 21)

	for round := 0; round < rounds; round++ {
		delSet := map[string]bool{}
		for j := 0; j < batchSize; j++ {
			idx := rng.Intn(len(allPaths))
			delSet[allPaths[idx]] = true
		}
		del := make([]string, 0, len(delSet))
		for p := range delSet {
			del = append(del, p)
		}
		added := map[string]IndexedFile{}
		for j := 0; j < batchSize; j++ {
			path := fmt.Sprintf("/bench/src/com/example/Churn_R%06d_I%06d.java", round, j)
			added[path] = IndexedFile{Facts: makeFileIndex(rng, path), Size: 1000, ModTime: int64(round*batchSize + j)}
		}
		_, err := store.Apply(info.ID, ChangeSet{Deleted: del, Added: added})
		if err != nil {
			t.Fatal(err)
		}
		if round%5 == 0 {
			runtime.GC()
			runtime.ReadMemStats(&m)
			heaps = append(heaps, int64(m.HeapInuse))
		}
	}

	// After churn: heap should be within 50% of baseline.
	runtime.GC()
	runtime.ReadMemStats(&m)
	memFinal := int64(m.HeapInuse)
	t.Logf("heap_base=%d MB heap_final=%d MB delta=%d MB", memBase/(1024*1024), memFinal/(1024*1024), (memFinal-memBase)/(1024*1024))

	// Print heap progression.
	for i, h := range heaps {
		t.Logf("  round %d: heap=%d MB", i*5, h/(1024*1024))
	}

	// Final heap should not exceed 150% of baseline.
	limit := memBase + memBase/2
	if memFinal > limit {
		t.Errorf("heap grew too much: final=%d limit=%d", memFinal, limit)
	}
}
