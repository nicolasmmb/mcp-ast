package repoindex

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"mcp-ast/internal/engine"
)

var ErrNotFound = errors.New("repository index not found")

// IndexedFile is a file's facts plus the metadata used to detect changes.
type IndexedFile struct {
	Facts   *engine.FileIndex
	Size    int64
	ModTime int64
	Digest  [32]byte
}

// ChangeSet is the delta produced by refresh_repo.
type ChangeSet struct {
	Added   map[string]IndexedFile
	Updated map[string]IndexedFile
	Deleted []string
}

type Info struct {
	ID          string    `json:"repo_id"`
	Root        string    `json:"root"`
	State       string    `json:"state"`
	Version     uint64    `json:"index_version"`
	Files       int       `json:"files_indexed"`
	Errors      int       `json:"files_failed"`
	UpdatedAt   time.Time `json:"updated_at"`
	MemoryBytes int64     `json:"memory_used_bytes"`
	MemoryLimit int64     `json:"memory_budget_bytes"`
}

// Store is the persistence boundary. A SQLite implementation can replace the
// in-memory store without changing services or MCP tools.
type Store interface {
	Create(root string) Info
	SetState(id, state string) (Info, error)
	Replace(id string, files map[string]IndexedFile, errs map[string]string) (Info, error)
	Apply(id string, changes ChangeSet) (Info, error)
	Info(id string) (Info, bool)
	Meta(id string) (map[string]IndexedFile, bool)
	Drop(id string) bool
	Files(id string) (map[string]*engine.FileIndex, bool)
	Symbols(id string) (map[string]map[string][]engine.Symbol, bool)
	Usages(id, name string) ([]engine.UsageMatch, bool)
	Complexity(id string, limit int) ([]engine.RankedComplexity, bool)
	Unused(id string) ([]engine.SearchMatch, bool)
	Calls(id string) (CallGraph, bool)
	Imports(id string) (ImportGraph, bool)
}

type MemoryStore struct {
	mu     sync.RWMutex
	limit  int64
	nextID uint64
	repos  map[string]*repo
}

type repo struct {
	info        Info
	files       map[string]*IndexedFile // path -> facts+meta
	usages      map[string]map[string][]engine.UsageMatch
	fileUsages  map[string]map[string][]engine.UsageMatch // file -> name -> refs (for O(1) removal)
	byCanonical map[string]map[string][]engine.UsageMatch
	complexity  []engine.RankedComplexity
	fileMemory  map[string]int64
	calls       CallGraph
	imports     ImportGraph
	errors      map[string]string
}

func NewMemory(limit int64) *MemoryStore {
	return &MemoryStore{limit: limit, repos: make(map[string]*repo)}
}

func newRepo(root string, limit int64) *repo {
	return &repo{
		files:      make(map[string]*IndexedFile),
		usages:     make(map[string]map[string][]engine.UsageMatch),
		fileUsages: make(map[string]map[string][]engine.UsageMatch),
		fileMemory: make(map[string]int64),
		errors:     make(map[string]string),
	}
}

func (s *MemoryStore) Create(root string) Info {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	id := "repo_" + itoa(s.nextID)
	r := newRepo(root, s.limit)
	r.info = Info{ID: id, Root: root, State: "building", MemoryLimit: s.limit}
	s.repos[id] = r
	return r.info
}

func (s *MemoryStore) SetState(id, state string) (Info, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.repos[id]
	if !ok {
		return Info{}, ErrNotFound
	}
	r.info.State = state
	return r.info, nil
}

// Replace rebuilds the whole index from scratch (initial index or full rebuild).
func (s *MemoryStore) Replace(id string, files map[string]IndexedFile, errs map[string]string) (Info, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.repos[id]
	if !ok {
		return Info{}, ErrNotFound
	}
	r.files = make(map[string]*IndexedFile, len(files))
	r.usages = make(map[string]map[string][]engine.UsageMatch)
	r.fileUsages = make(map[string]map[string][]engine.UsageMatch)
	r.fileMemory = make(map[string]int64)
	r.complexity = nil
	r.errors = map[string]string{}
	memory := int64(0)
	for p, f := range files {
		cp := f
		r.files[p] = &cp
		memory += r.insert(p, &cp)
	}
	r.errors = errs
	r.enrich()
	r.calls = buildCallGraph(r.files)
	r.imports = buildImportGraph(r.files)
	r.info.MemoryBytes = memory
	r.info.UpdatedAt = time.Now().UTC()
	if s.limit > 0 && memory > s.limit {
		r.info.State = "partial"
		r.info.Files = 0
	} else {
		r.info.State = "ready"
		r.info.Files = len(r.files)
	}
	r.info.Errors = len(errs)
	r.info.Version++
	return r.info, nil
}

// Apply applies a delta: deleted files are removed from every posting,
// updated files replace themselves, added files are inserted.
func (s *MemoryStore) Apply(id string, changes ChangeSet) (Info, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.repos[id]
	if !ok {
		return Info{}, ErrNotFound
	}
	for _, p := range changes.Deleted {
		r.remove(p)
	}
	for p, f := range changes.Updated {
		r.remove(p)
		r.insertNew(p, &f)
	}
	for p, f := range changes.Added {
		r.insertNew(p, &f)
	}
	sort.Slice(r.complexity, func(i, j int) bool { return cmpComplexity(r.complexity[i], r.complexity[j]) })
	r.enrich()
	r.calls = buildCallGraph(r.files)
	r.imports = buildImportGraph(r.files)
	info := r.info
	info.MemoryBytes = s.estimateMemory(r)
	info.UpdatedAt = time.Now().UTC()
	if s.limit > 0 && info.MemoryBytes > s.limit {
		info.State = "partial"
		info.Files = 0
	} else {
		info.State = "ready"
		info.Files = len(r.files)
	}
	info.Errors = len(r.errors)
	info.Version++
	r.info = info
	return info, nil
}

func cmpComplexity(a, b engine.RankedComplexity) bool {
	if a.Complexity != b.Complexity {
		return a.Complexity > b.Complexity
	}
	if a.File != b.File {
		return a.File < b.File
	}
	return a.Name < b.Name
}

func (s *MemoryStore) Info(id string) (Info, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.repos[id]
	if !ok {
		return Info{}, false
	}
	return r.info, true
}

// Meta returns the indexed file set including the stored metadata (size,
// mtime, digest), used to compute refresh deltas.
func (s *MemoryStore) Meta(id string) (map[string]IndexedFile, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.repos[id]
	if !ok {
		return nil, false
	}
	out := make(map[string]IndexedFile, len(r.files))
	for p, f := range r.files {
		out[p] = *f
	}
	return out, true
}

func (s *MemoryStore) Drop(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.repos[id]; !ok {
		return false
	}
	delete(s.repos, id)
	return true
}

func (s *MemoryStore) Files(id string) (map[string]*engine.FileIndex, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.repos[id]
	if !ok {
		return nil, false
	}
	out := make(map[string]*engine.FileIndex, len(r.files))
	for p, f := range r.files {
		out[p] = f.Facts
	}
	return out, true
}

func (s *MemoryStore) Symbols(id string) (map[string]map[string][]engine.Symbol, bool) {
	files, ok := s.Files(id)
	if !ok {
		return nil, false
	}
	out := make(map[string]map[string][]engine.Symbol, len(files))
	for path, facts := range files {
		out[path] = facts.Symbols
	}
	return out, true
}

func (s *MemoryStore) Usages(id, name string) ([]engine.UsageMatch, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.repos[id]
	if !ok {
		return nil, false
	}
	if strings.Contains(name, "|") {
		return flattenUsages(r.byCanonical[name]), true
	}
	return flattenUsages(r.usages[name]), true
}

func flattenUsages(byFile map[string][]engine.UsageMatch) []engine.UsageMatch {
	out := make([]engine.UsageMatch, 0)
	for _, refs := range byFile {
		out = append(out, refs...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Line < out[j].Line
	})
	return out
}

func (s *MemoryStore) Complexity(id string, limit int) ([]engine.RankedComplexity, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.repos[id]
	if !ok {
		return nil, false
	}
	entries := r.complexity
	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}
	return append([]engine.RankedComplexity(nil), entries...), true
}

func (s *MemoryStore) Calls(id string) (CallGraph, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.repos[id]
	if !ok {
		return CallGraph{}, false
	}
	return r.calls, true
}

// Unused returns declared symbols whose name occurs exactly once across the
// AST occurrences of the whole index (that occurrence is the declaration
// itself). Imports are skipped; comments and strings do not count because
// occurrences come from the identifier query, not from raw text. The result
// is heuristic: scope and overloads are not resolved.
func (s *MemoryStore) Unused(id string) ([]engine.SearchMatch, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.repos[id]
	if !ok {
		return nil, false
	}
	seen := map[string]bool{}
	matches := make([]engine.SearchMatch, 0)
	paths := make([]string, 0, len(r.files))
	for p := range r.files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		facts := r.files[p].Facts
		for kind, syms := range facts.Symbols {
			if kind == "imports" {
				continue
			}
			for _, sym := range syms {
				name := strings.TrimSpace(sym.Name)
				if name == "" || seen[name] {
					continue
				}
				seen[name] = true
				total := 0
				for _, refs := range r.usages[name] {
					total += len(refs)
				}
				if total != 1 {
					continue
				}
				matches = append(matches, engine.SearchMatch{
					File: p, Kind: kind, Name: sym.Name,
					Line: sym.Start.Row + 1, Col: sym.Start.Col, Text: sym.Text,
				})
			}
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].File != matches[j].File {
			return matches[i].File < matches[j].File
		}
		return matches[i].Name < matches[j].Name
	})
	return matches, true
}

func (s *MemoryStore) Imports(id string) (ImportGraph, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.repos[id]
	if !ok {
		return ImportGraph{}, false
	}
	return r.imports, true
}

// remove deletes every posting contributed by path.
func (r *repo) remove(path string) {
	f, ok := r.files[path]
	if !ok {
		return
	}
	for name := range r.fileUsages[path] {
		delete(r.usages[name], path)
		if len(r.usages[name]) == 0 {
			delete(r.usages, name)
		}
	}
	_ = f
	delete(r.fileUsages, path)
	delete(r.files, path)
	delete(r.fileMemory, path)
	out := r.complexity[:0]
	for _, e := range r.complexity {
		if e.File != path {
			out = append(out, e)
		}
	}
	r.complexity = out
}

// insert adds postings for a fresh file and returns its memory estimate.
func (r *repo) insert(path string, f *IndexedFile) int64 {
	facts := f.Facts
	memory := estimateFile(path, facts)
	for _, u := range facts.Usages {
		byFile := r.usages[u.Name]
		if byFile == nil {
			byFile = make(map[string][]engine.UsageMatch)
			r.usages[u.Name] = byFile
		}
		byFile[path] = append(byFile[path], u)
		fileMap := r.fileUsages[path]
		if fileMap == nil {
			fileMap = make(map[string][]engine.UsageMatch)
			r.fileUsages[path] = fileMap
		}
		fileMap[u.Name] = append(fileMap[u.Name], u)
	}
	for _, c := range facts.Complexity {
		r.complexity = append(r.complexity, engine.RankedComplexity{File: path, ComplexityEntry: c})
	}
	r.fileMemory[path] = memory
	return memory
}

// insertNew inserts a file that must not exist yet.
func (r *repo) insertNew(path string, f *IndexedFile) {
	cp := *f
	r.files[path] = &cp
	_ = r.insert(path, &cp)
}

// enrich stamps canonical identities on usages. A usage gets the canonical
// key language|file|kind|name when its name has exactly one declaration in
// the repo (not counting imports); otherwise it stays empty (ambiguous).
// It also rebuilds the canonical postings.
func (r *repo) enrich() {
	declCount := map[string]int{}
	declCanonical := map[string]string{}
	for p, f := range r.files {
		for kind, syms := range f.Facts.Symbols {
			if kind == "imports" {
				continue
			}
			for _, s := range syms {
				name := strings.TrimSpace(s.Name)
				if name == "" {
					continue
				}
				declCount[name]++
				if _, ok := declCanonical[name]; !ok {
					declCanonical[name] = f.Facts.Language + "|" + p + "|" + kind + "|" + name
				}
			}
		}
	}
	r.byCanonical = make(map[string]map[string][]engine.UsageMatch)
	for name, byFile := range r.usages {
		canonical := ""
		if declCount[name] == 1 {
			canonical = declCanonical[name]
		}
		for file, refs := range byFile {
			for i := range refs {
				refs[i].Canonical = canonical
			}
			if canonical == "" {
				continue
			}
			c := r.byCanonical[canonical]
			if c == nil {
				c = make(map[string][]engine.UsageMatch)
				r.byCanonical[canonical] = c
			}
			c[file] = append(c[file], refs...)
		}
	}
}

func (s *MemoryStore) estimateMemory(r *repo) int64 {
	var total int64
	for _, m := range r.fileMemory {
		total += m
	}
	return total
}

func estimateFile(path string, facts *engine.FileIndex) int64 {
	mem := int64(len(path) + len(facts.Language) + 64)
	for _, groups := range facts.Symbols {
		for _, sym := range groups {
			mem += int64(len(sym.Name) + len(sym.Text) + 32)
		}
	}
	for _, u := range facts.Usages {
		mem += int64(len(u.Name) + len(u.Text) + len(u.Caller) + 48)
	}
	for _, c := range facts.Complexity {
		mem += int64(len(c.Name) + len(c.Kind) + 32)
	}
	return mem
}

func itoa(n uint64) string {
	if n == 0 {
		return "0"
	}
	var digits [20]byte
	i := len(digits)
	for n > 0 {
		i--
		digits[i] = byte(n%10) + '0'
		n /= 10
	}
	return string(digits[i:])
}

// ParseMemoryLimit accepts only positive memory limits expressed in MB, or
// "auto" for the automatic budget.
func ParseMemoryLimit(value string) (int64, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "auto" {
		return AutoMemoryLimit(), nil
	}
	if !strings.HasSuffix(value, "mb") {
		return 0, errors.New("memory limit must use the mb suffix, for example 2048mb")
	}
	mb, err := strconv.ParseInt(strings.TrimSuffix(value, "mb"), 10, 64)
	if err != nil || mb <= 0 {
		return 0, errors.New("memory limit must be a positive number of MB")
	}
	return mb << 20, nil
}

// AutoMemoryLimit uses one quarter of available Linux memory or physical macOS
// memory, bounded to a safe index budget. Other systems use a 1 GB fallback.
func AutoMemoryLimit() int64 {
	const mb = int64(1 << 20)
	available := int64(0)
	switch runtime.GOOS {
	case "linux":
		if data, err := os.ReadFile("/proc/meminfo"); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				if fields := strings.Fields(line); len(fields) >= 2 && fields[0] == "MemAvailable:" {
					if kb, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
						available = kb << 10
					}
					break
				}
			}
		}
	case "darwin":
		if out, err := exec.Command("sysctl", "-n", "hw.memsize").Output(); err == nil {
			available, _ = strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
		}
	}
	if available == 0 {
		available = 1024 * mb * 4
	}
	limit := available / 4
	if limit < 256*mb {
		return 256 * mb
	}
	if limit > 4096*mb {
		return 4096 * mb
	}
	return limit
}

// NewID returns a random hex identifier, used by tests and future backends.
func NewID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "repo_" + hex.EncodeToString(b[:])
}
