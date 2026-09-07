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

// ChangeSet is the delta produced by refresh_repo. Errors maps per-file
// failures (unreadable, unstable during parse) reported by the refresh.
type ChangeSet struct {
	Added   map[string]IndexedFile
	Updated map[string]IndexedFile
	Deleted []string
	Errors  map[string]string
}

type Info struct {
	ID          string            `json:"repo_id"`
	Root        string            `json:"root"`
	State       string            `json:"state"`
	Version     uint64            `json:"index_version"`
	Files       int               `json:"files_indexed"`
	Errors      int               `json:"files_failed"`
	LastErrors  map[string]string `json:"last_errors,omitempty"`
	Watch       bool              `json:"watch,omitempty"`
	LastSync    time.Time         `json:"last_sync,omitempty"`
	Restored    bool              `json:"restored,omitempty"`
	CachePath   string            `json:"cache_path,omitempty"`
	UpdatedAt   time.Time         `json:"updated_at"`
	MemoryBytes int64             `json:"memory_used_bytes"`
	MemoryLimit int64             `json:"memory_budget_bytes"`
}

// Store is the persistence boundary. A SQLite implementation can replace the
// in-memory store without changing services or MCP tools.
type Store interface {
	Create(root string) Info
	SetState(id, state string) (Info, error)
	SetWatch(id string, on bool) (Info, error)
	SetCache(id string, restored bool, cachePath string) (Info, error)
	Replace(id string, files map[string]IndexedFile, errs map[string]string) (Info, error)
	Apply(id string, changes ChangeSet) (Info, error)
	Info(id string) (Info, bool)
	Meta(id string) (map[string]IndexedFile, bool)
	Drop(id string) bool
	Files(id string) (map[string]*engine.FileIndex, bool)
	Symbols(id string) (map[string]map[string][]engine.Symbol, bool)
	Usages(id, name string) ([]engine.UsageMatch, bool)
	UsagesWindow(id, name string, offset, limit int) ([]engine.UsageMatch, int, bool)
	Complexity(id string, limit int) ([]engine.RankedComplexity, bool)
	Unused(id string) ([]engine.SearchMatch, bool)
	Calls(id string) (CallGraph, bool)
	Imports(id string) (ImportGraph, bool)
}

// FileID and NameID intern paths and names so that usage postings store only
// 32-bit ids instead of repeated strings.
type FileID uint32
type NameID uint32

const (
	kindDefinition uint8 = iota
	kindReference
	kindCallSite
	kindImport
)

func kindOf(s string) uint8 {
	switch s {
	case "definition":
		return kindDefinition
	case "call-site":
		return kindCallSite
	case "import":
		return kindImport
	default:
		return kindReference
	}
}

func kindString(k uint8) string {
	switch k {
	case kindDefinition:
		return "definition"
	case kindCallSite:
		return "call-site"
	case kindImport:
		return "import"
	default:
		return "reference"
	}
}

// UsageRef is the compact internal posting entry. File and Name are the map
// keys; Caller and Canonical are interned NameIDs (0 = absent). Text is kept
// verbatim: it is per-occurrence data, not repeated across postings.
type UsageRef struct {
	Row       uint32
	Col       uint32
	Kind      uint8
	Caller    NameID
	Canonical NameID
	Text      string
}

type MemoryStore struct {
	mu     sync.RWMutex
	limit  int64
	nextID uint64
	repos  map[string]*repo
}

type repo struct {
	info        Info
	files       map[FileID]*IndexedFile // id -> facts+meta
	fileIDs     map[string]FileID       // path -> id
	paths       []string                // id -> path
	usages      map[NameID]map[FileID][]UsageRef
	fileUsages  map[FileID]map[NameID][]UsageRef // for O(1) removal
	byCanonical map[NameID]map[FileID][]UsageRef
	names       []string
	nameIDs     map[string]NameID
	complexity  []engine.RankedComplexity
	fileMemory  map[FileID]int64
	calls       CallGraph
	imports     ImportGraph
	errors      map[string]string
}

func NewMemory(limit int64) *MemoryStore {
	return &MemoryStore{limit: limit, repos: make(map[string]*repo)}
}

func newRepo(root string, limit int64) *repo {
	return &repo{
		files:       make(map[FileID]*IndexedFile),
		fileIDs:     make(map[string]FileID),
		usages:      make(map[NameID]map[FileID][]UsageRef),
		fileUsages:  make(map[FileID]map[NameID][]UsageRef),
		byCanonical: make(map[NameID]map[FileID][]UsageRef),
		nameIDs:     make(map[string]NameID),
		fileMemory:  make(map[FileID]int64),
		errors:      make(map[string]string),
	}
}

func (r *repo) internFile(path string) FileID {
	if id, ok := r.fileIDs[path]; ok {
		return id
	}
	id := FileID(len(r.paths) + 1)
	r.fileIDs[path] = id
	r.paths = append(r.paths, path)
	return id
}

func (r *repo) internName(name string) NameID {
	if name == "" {
		return 0
	}
	if id, ok := r.nameIDs[name]; ok {
		return id
	}
	id := NameID(len(r.names) + 1)
	r.nameIDs[name] = id
	r.names = append(r.names, name)
	return id
}

func (r *repo) filePath(id FileID) string {
	if id == 0 || int(id) > len(r.paths) {
		return ""
	}
	return r.paths[id-1]
}

func (r *repo) nameOf(id NameID) string {
	if id == 0 || int(id) > len(r.names) {
		return ""
	}
	return r.names[id-1]
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

// SetWatch toggles automatic refresh; every sync afterwards stamps LastSync.
func (s *MemoryStore) SetWatch(id string, on bool) (Info, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.repos[id]
	if !ok {
		return Info{}, ErrNotFound
	}
	r.info.Watch = on
	return r.info, nil
}

// SetCache records the persistence state of this index.
func (s *MemoryStore) SetCache(id string, restored bool, cachePath string) (Info, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.repos[id]
	if !ok {
		return Info{}, ErrNotFound
	}
	r.info.Restored = restored
	r.info.CachePath = cachePath
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
	r.files = make(map[FileID]*IndexedFile, len(files))
	r.fileIDs = make(map[string]FileID, len(files))
	r.paths = r.paths[:0]
	r.usages = make(map[NameID]map[FileID][]UsageRef)
	r.fileUsages = make(map[FileID]map[NameID][]UsageRef)
	r.byCanonical = make(map[NameID]map[FileID][]UsageRef)
	r.names = r.names[:0]
	r.nameIDs = make(map[string]NameID)
	r.fileMemory = make(map[FileID]int64)
	r.complexity = nil
	r.errors = map[string]string{}
	memory := int64(0)
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		f := files[p]
		cp := f
		fid := r.internFile(p)
		r.files[fid] = &cp
		memory += r.insert(fid, &cp)
	}
	r.errors = errs
	if r.errors == nil {
		r.errors = map[string]string{}
	}
	r.info.LastErrors = cappedErrors(r.errors)
	if r.info.Watch {
		r.info.LastSync = time.Now().UTC()
	}
	r.enrich()
	r.calls = buildCallGraph(r.filesByPath())
	r.imports = buildImportGraph(r.filesByPath())
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
		r.remove(r.fileIDs[p])
		delete(r.errors, p)
	}
	for p, f := range changes.Updated {
		r.remove(r.fileIDs[p])
		delete(r.errors, p)
		fid := r.internFile(p)
		cp := f
		r.files[fid] = &cp
		r.insert(fid, &cp)
	}
	for p, f := range changes.Added {
		delete(r.errors, p)
		fid := r.internFile(p)
		cp := f
		r.files[fid] = &cp
		r.insert(fid, &cp)
	}
	for p, e := range changes.Errors {
		if r.errors == nil {
			r.errors = map[string]string{}
		}
		r.errors[p] = e
	}
	// ponytail: full graph rebuild per Apply; incremental graph surgery if
	// refresh on 50k-file repos measures this as a hotspot.
	sort.Slice(r.complexity, func(i, j int) bool { return cmpComplexity(r.complexity[i], r.complexity[j]) })
	r.enrich()
	r.calls = buildCallGraph(r.filesByPath())
	r.imports = buildImportGraph(r.filesByPath())
	info := r.info
	info.MemoryBytes = s.estimateMemory(r)
	info.UpdatedAt = time.Now().UTC()
	info.LastErrors = cappedErrors(r.errors)
	if info.Watch {
		info.LastSync = time.Now().UTC()
	}
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

// filesByPath rebuilds the path-keyed view needed by the graph builders.
func (r *repo) filesByPath() map[string]*IndexedFile {
	out := make(map[string]*IndexedFile, len(r.files))
	for id, f := range r.files {
		out[r.filePath(id)] = f
	}
	return out
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
	for fid, f := range r.files {
		out[r.filePath(fid)] = *f
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
	for fid, f := range r.files {
		out[r.filePath(fid)] = f.Facts
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
	matches, _, ok := s.UsagesWindow(id, name, 0, -1)
	return matches, ok
}

// UsagesWindow returns a page of usages for name (or a canonical key
// containing '|') in deterministic file-then-line order, plus the total
// occurrence count. limit < 0 means no limit.
func (s *MemoryStore) UsagesWindow(id, name string, offset, limit int) ([]engine.UsageMatch, int, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.repos[id]
	if !ok {
		return nil, 0, false
	}
	byFile := r.usages[r.nameIDs[name]]
	if strings.Contains(name, "|") {
		byFile = r.byCanonical[r.nameIDs[name]]
	}
	fids := make([]FileID, 0, len(byFile))
	for fid := range byFile {
		fids = append(fids, fid)
	}
	sort.Slice(fids, func(i, j int) bool { return r.filePath(fids[i]) < r.filePath(fids[j]) })
	out := make([]engine.UsageMatch, 0, 8)
	total := 0
	for _, fid := range fids {
		path := r.filePath(fid)
		refs := byFile[fid]
		sorted := append([]UsageRef(nil), refs...)
		sort.Slice(sorted, func(i, j int) bool {
			if sorted[i].Row != sorted[j].Row {
				return sorted[i].Row < sorted[j].Row
			}
			return sorted[i].Col < sorted[j].Col
		})
		for _, ref := range sorted {
			total++
			if total <= offset {
				continue
			}
			if limit < 0 || len(out) < limit {
				out = append(out, engine.UsageMatch{
					File:      path,
					Name:      r.nameOf(r.nameIDs[name]),
					Canonical: r.nameOf(ref.Canonical),
					Line:      int(ref.Row) + 1,
					Col:       int(ref.Col),
					Text:      ref.Text,
					Kind:      kindString(ref.Kind),
					Caller:    r.nameOf(ref.Caller),
				})
			}
		}
	}
	return out, total, true
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
	fids := make([]FileID, 0, len(r.files))
	for fid := range r.files {
		fids = append(fids, fid)
	}
	sort.Slice(fids, func(i, j int) bool { return r.filePath(fids[i]) < r.filePath(fids[j]) })
	for _, fid := range fids {
		p := r.filePath(fid)
		facts := r.files[fid].Facts
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
				for _, refs := range r.usages[r.nameIDs[name]] {
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

// remove deletes every posting contributed by fid.
func (r *repo) remove(fid FileID) {
	if fid == 0 {
		return
	}
	for nameID := range r.fileUsages[fid] {
		delete(r.usages[nameID], fid)
		if len(r.usages[nameID]) == 0 {
			delete(r.usages, nameID)
		}
	}
	delete(r.fileUsages, fid)
	delete(r.files, fid)
	delete(r.fileMemory, fid)
	path := r.filePath(fid)
	out := r.complexity[:0]
	for _, e := range r.complexity {
		if e.File != path {
			out = append(out, e)
		}
	}
	r.complexity = out
}

// insert adds postings for a fresh file and returns its memory estimate.
func (r *repo) insert(fid FileID, f *IndexedFile) int64 {
	facts := f.Facts
	path := r.filePath(fid)
	memory := estimateFile(path, facts)
	for _, u := range facts.Usages {
		nameID := r.internName(u.Name)
		byFile := r.usages[nameID]
		if byFile == nil {
			byFile = make(map[FileID][]UsageRef)
			r.usages[nameID] = byFile
		}
		byFile[fid] = append(byFile[fid], UsageRef{
			Row:    uint32(u.Line - 1),
			Col:    uint32(u.Col),
			Kind:   kindOf(u.Kind),
			Caller: r.internName(u.Caller),
			Text:   u.Text,
		})
		fileMap := r.fileUsages[fid]
		if fileMap == nil {
			fileMap = make(map[NameID][]UsageRef)
			r.fileUsages[fid] = fileMap
		}
		fileMap[nameID] = append(fileMap[nameID], byFile[fid][len(byFile[fid])-1])
	}
	for _, c := range facts.Complexity {
		r.complexity = append(r.complexity, engine.RankedComplexity{File: path, ComplexityEntry: c})
	}
	r.fileMemory[fid] = memory
	return memory
}

// enrich stamps canonical identities on usages. A usage gets the canonical
// key language|file|kind|name when its name has exactly one declaration in
// the repo (not counting imports); otherwise it stays empty (ambiguous).
// It also rebuilds the canonical postings.
func (r *repo) enrich() {
	declCount := map[string]int{}
	declCanonical := map[string]string{}
	for fid, f := range r.files {
		path := r.filePath(fid)
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
					declCanonical[name] = f.Facts.Language + "|" + path + "|" + kind + "|" + name
				}
			}
		}
	}
	r.byCanonical = make(map[NameID]map[FileID][]UsageRef)
	for nameID, byFile := range r.usages {
		name := r.nameOf(nameID)
		canonical := ""
		if declCount[name] == 1 {
			canonical = declCanonical[name]
		}
		canonicalID := r.internName(canonical)
		for fid, refs := range byFile {
			for i := range refs {
				refs[i].Canonical = canonicalID
			}
			if canonical == "" {
				continue
			}
			c := r.byCanonical[canonicalID]
			if c == nil {
				c = make(map[FileID][]UsageRef)
				r.byCanonical[canonicalID] = c
			}
			c[fid] = append(c[fid], refs...)
		}
	}
}

func (s *MemoryStore) estimateMemory(r *repo) int64 {
	var total int64
	for _, m := range r.fileMemory {
		total += m
	}
	for _, name := range r.names {
		total += int64(len(name) + 8)
	}
	return total
}

// estimateFile estimates the compact stored size of one file's facts:
// per-usage cost is the fixed UsageRef plus its text; names and callers are
// interned and counted once globally (added by estimateMemory).
func estimateFile(path string, facts *engine.FileIndex) int64 {
	mem := int64(len(path) + len(facts.Language) + 64)
	for _, groups := range facts.Symbols {
		for _, sym := range groups {
			mem += int64(len(sym.Name) + len(sym.Text) + 32)
		}
	}
	for _, u := range facts.Usages {
		mem += int64(len(u.Text) + 40)
	}
	for _, c := range facts.Complexity {
		mem += int64(len(c.Name) + len(c.Kind) + 32)
	}
	return mem
}

// cappedErrors caps the exposed error map so repo_status stays bounded.
func cappedErrors(errs map[string]string) map[string]string {
	if len(errs) == 0 {
		return nil
	}
	out := make(map[string]string, min(len(errs), 50))
	for p, e := range errs {
		out[p] = e
		if len(out) >= 50 {
			break
		}
	}
	return out
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
