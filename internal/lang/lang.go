package lang

import (
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	ts "github.com/tree-sitter/go-tree-sitter"
)

// Language is the extension point of the server: one implementation per
// language, registered at startup.
type Language interface {
	Name() string
	Extensions() []string
	Language() *ts.Language
	SymbolQueries() map[string]string
	// DecisionKinds lists the AST node kinds that add one to cyclomatic
	// complexity (branches, loops, switch cases, ternaries, catch, ...).
	DecisionKinds() []string
	// AuxQueries returns auxiliary queries for advanced tools. Keys:
	//   - "identifiers": captures every identifier-like node as @id
	//   - "calls": captures every call site's callee as @callee
	AuxQueries() map[string]string
}

type entry struct {
	impl    Language
	pool    sync.Pool
	queries map[string]*CompiledQuery
}

type CompiledQuery struct {
	Q     *ts.Query
	Names []string
}

func SymbolKey(kind string) string { return "symbol:" + kind }

func AuxKey(kind string) string { return "aux:" + kind }

func compileQuery(l Language, key, src string) (*CompiledQuery, error) {
	q, qerr := ts.NewQuery(l.Language(), src)
	if qerr != nil {
		return nil, fmt.Errorf("lang %s query %q: %s", l.Name(), key, qerr.Message)
	}
	return &CompiledQuery{Q: q, Names: q.CaptureNames()}, nil
}

type Registry struct {
	mu    sync.RWMutex
	langs map[string]*entry
}

func NewRegistry() *Registry {
	return &Registry{langs: make(map[string]*entry)}
}

func (r *Registry) Register(l Language) error {
	p := ts.NewParser()
	defer p.Close()
	if err := p.SetLanguage(l.Language()); err != nil {
		return fmt.Errorf("lang %s: %w", l.Name(), err)
	}
	e := &entry{impl: l, queries: make(map[string]*CompiledQuery)}
	for kind, qs := range l.SymbolQueries() {
		cq, err := compileQuery(l, SymbolKey(kind), qs)
		if err != nil {
			return err
		}
		e.queries[SymbolKey(kind)] = cq
	}
	for kind, qs := range l.AuxQueries() {
		cq, err := compileQuery(l, AuxKey(kind), qs)
		if err != nil {
			return err
		}
		e.queries[AuxKey(kind)] = cq
	}
	e.pool.New = func() any {
		p := ts.NewParser()
		if err := p.SetLanguage(l.Language()); err != nil {
			panic(fmt.Sprintf("lang %s: %v", l.Name(), err))
		}

		func (r *Registry) Compiled(l Language, key string) (*CompiledQuery, bool) {
			r.mu.RLock()
			defer r.mu.RUnlock()
			e, ok := r.langs[l.Name()]
			if !ok {
				return nil, false
			}
			cq, ok := e.queries[key]
			return cq, ok
		}
		return p
	}
	r.mu.Lock()
	r.langs[l.Name()] = e
	r.mu.Unlock()
	return nil
}

func (r *Registry) Get(name string) (Language, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.langs[name]
	if !ok {
		return nil, false
	}
	return e.impl, true
}

func (r *Registry) Resolve(name, path string) (Language, error) {
	if name != "" {
		if l, ok := r.Get(name); ok {
			return l, nil
		}
		return nil, fmt.Errorf("unknown language %q (registered: %s)", name, strings.Join(r.List(), ", "))
	}
	ext := filepath.Ext(path)
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, e := range r.langs {
		if slices.Contains(e.impl.Extensions(), ext) {
			return e.impl, nil
		}
	}
	return nil, fmt.Errorf("cannot detect language for %q; pass language explicitly (registered: %s)", path, strings.Join(r.List(), ", "))
}

func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.langs))
	for n := range r.langs {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Acquire returns a parser from the per-language pool and a release func.
// Tree-sitter parsers are not thread-safe, so they must never be shared.
func (r *Registry) Acquire(l Language) (*ts.Parser, func()) {
	r.mu.RLock()
	e := r.langs[l.Name()]
	r.mu.RUnlock()
	p := e.pool.Get().(*ts.Parser)
	return p, func() { e.pool.Put(p) }
}
