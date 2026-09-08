package tools

import (
	"context"
	"fmt"
	"os"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"mcp-ast/internal/engine"
	"mcp-ast/internal/repoindex"
	"mcp-ast/internal/service"
)

var parseASTSchema = func() *jsonschema.Schema {
	point := &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"row": {Type: "integer"},
			"col": {Type: "integer"},
		},
	}
	node := &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"type":     {Type: "string"},
			"field":    {Type: "string"},
			"named":    {Type: "boolean"},
			"start":    {Ref: "#/$defs/point"},
			"end":      {Ref: "#/$defs/point"},
			"children": {Type: "array", Items: &jsonschema.Schema{Ref: "#/$defs/node"}},
		},
	}
	return &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"elapsed_ms": {Type: "number"},
			"language":   {Type: "string"},
			"path":       {Type: "string"},
			"has_error":  {Type: "boolean"},
			"ast":        {Ref: "#/$defs/node"},
		},
		Defs: map[string]*jsonschema.Schema{"point": point, "node": node},
	}
}()

var outlineSchema = func() *jsonschema.Schema {
	point := &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"row": {Type: "integer"},
			"col": {Type: "integer"},
		},
	}
	node := &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"kind":     {Type: "string"},
			"name":     {Type: "string"},
			"text":     {Type: "string"},
			"start":    {Ref: "#/$defs/point"},
			"end":      {Ref: "#/$defs/point"},
			"children": {Type: "array", Items: &jsonschema.Schema{Ref: "#/$defs/outline_node"}},
		},
	}
	return &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"elapsed_ms": {Type: "number"},
			"language":   {Type: "string"},
			"path":       {Type: "string"},
			"outline":    {Type: "array", Items: &jsonschema.Schema{Ref: "#/$defs/outline_node"}},
		},
		Defs: map[string]*jsonschema.Schema{"point": point, "outline_node": node},
	}
}()

func add[In any, Out TimedOutput](s *mcp.Server, svcs *service.Services, t *mcp.Tool,
	h func(context.Context, *service.Services, In) (Out, error)) {
	mcp.AddTool(s, t, timed(func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		out, err := h(ctx, svcs, in)
		return nil, out, err
	}))
}

// Register declares every tool. Breaking change: legacy tool names removed (no aliases).
func Register(s *mcp.Server, svcs *service.Services) {
	add(s, svcs, &mcp.Tool{
		Name:        "list_languages",
		Description: "List programming languages supported by this AST server. Call first to discover available languages before other tools. Do not use for file analysis.",
	}, handleListLanguages)

	add(s, svcs, &mcp.Tool{
		Name:        "index_status",
		Description: "List repositories configured with -repo: state, file count, errors, memory usage, watch, last sync, restored. No arguments. Search tools use these indexes automatically when the queried path is covered.",
	}, handleIndexStatus)

	add(s, svcs, &mcp.Tool{
		Name:        "repo_impact",
		Description: "Find dependants (reverse) or references (forward) of a symbol/file in the call or import graph of a repo configured with -repo. depth 0 = transitive. Pass any path inside the repo.",
	}, handleRepoImpact)

	add(s, svcs, &mcp.Tool{
		Name:        "repo_cycles",
		Description: "Find strongly connected components (cycles) in the call or import graph of a repo configured with -repo. Pass any path inside the repo.",
	}, handleRepoCycles)

	add(s, svcs, &mcp.Tool{
		Name:        "repo_topology",
		Description: "Condense the graph of a repo configured with -repo into its SCC DAG and return layers in topological order. Pass any path inside the repo.",
	}, handleRepoTopology)

	add(s, svcs, &mcp.Tool{
		Name:         "parse_ast",
		Description:  "Parse one source file and return its AST as recursive JSON. Use to explore grammar structure. Prefer query_ast or scan_symbols for extraction. Set max_depth (e.g. 5) on large files to keep output small. Next: use get_text with node ranges.",
		OutputSchema: parseASTSchema,
	}, handleParseAST)

	add(s, svcs, &mcp.Tool{
		Name:        "query_ast",
		Description: "Run a tree-sitter query on one file; returns matches with captures and positions. Use for custom extraction. Prefer scan_symbols for built-in kinds (functions, classes). Keep limit small. Next: get_text on capture ranges.",
	}, handleQueryAST)

	add(s, svcs, &mcp.Tool{
		Name:        "scan_symbols",
		Description: "Extract symbols from a file OR directory (path can be either). Filter with languages[], kinds[], name. Prefer outline_file for hierarchy of one file; find_usages before rename/delete. Default text is one-line summary. Uses the index automatically when path is covered by a repo configured with -repo (source=indexed); otherwise scans the disk.",
	}, handleScanSymbols)

	add(s, svcs, &mcp.Tool{
		Name:        "analyze_file",
		Description: "Full dossier for one file in a single parse: metrics, per-kind stats, cyclomatic complexity, call graph. For directory-wide hotspots use rank_complexity instead of looping this tool. Next: get_text on complex functions. Uses the index automatically when path is covered by a repo configured with -repo; stale files are reindexed (source=ast_fallback).",
	}, handleAnalyzeFile)

	add(s, svcs, &mcp.Tool{
		Name:        "get_text",
		Description: "Return exact source text for a 0-based (row,col) range from other tool outputs. Prefer this over loading whole files. Ranges inclusive at start, exclusive at end.",
	}, handleGetText)

	add(s, svcs, &mcp.Tool{
		Name:        "find_usages",
		Description: "Find symbol usages in a directory. ALWAYS run before rename/delete. mode=occurrences (definition|call-site|import|reference), callers (counts), unused (heuristic), definitions, imports. group_by_file defaults true to keep payloads small. Prefer outline_file for one-file structure. Uses the index automatically when path is covered by a repo configured with -repo (source=indexed, cursor pagination via next_cursor); otherwise scans the disk.",
	}, handleFindUsages)

	add(s, svcs, &mcp.Tool{
		Name:        "rank_complexity",
		Description: "Rank functions/methods in a directory by cyclomatic complexity (top-N, default 20). Use BEFORE looping analyze_file on every file. Not a substitute for analyze_file when you need one file's call graph. Next: get_text on returned ranges. Uses pre-ranked hotspots automatically when path is covered by a repo configured with -repo (source=indexed); otherwise scans the disk.",
	}, handleRankComplexity)

	add(s, svcs, &mcp.Tool{
		Name:         "outline_file",
		Description:  "Hierarchical symbol outline for one file (types/classes → methods/fields) via range containment. Use for navigation without full AST cost. Prefer analyze_file for complexity/call graph; get_text for bodies. Uses the index automatically when path is covered by a repo configured with -repo (source=indexed; single-file reparse with include_text).",
		OutputSchema: outlineSchema,
	}, handleOutlineFile)
}

type listLanguagesInput struct{}
type listLanguagesOutput struct {
	Timed
	Languages []string `json:"languages"`
}

func handleListLanguages(_ context.Context, svcs *service.Services, _ listLanguagesInput) (*listLanguagesOutput, error) {
	return &listLanguagesOutput{Languages: svcs.Engine.ListLanguages()}, nil
}

type indexStatusInput struct{}
type indexStatusOutput struct {
	Timed
	Repos []serviceRepoInfo `json:"repos"`
}

// serviceRepoInfo keeps the storage package out of generated tool schemas.
type serviceRepoInfo = repoindex.Info

func handleIndexStatus(_ context.Context, svcs *service.Services, _ indexStatusInput) (*indexStatusOutput, error) {
	repos := svcs.Repo.List()
	if repos == nil {
		repos = []serviceRepoInfo{}
	}
	return &indexStatusOutput{Repos: repos}, nil
}

type impactInput struct {
	Path      string `json:"path" jsonschema:"any file or directory inside a repo configured with -repo"`
	Graph     string `json:"graph" jsonschema:"calls|imports"`
	Target    string `json:"target" jsonschema:"symbol or file name in the graph"`
	Direction string `json:"direction,omitempty" jsonschema:"reverse (default) or forward"`
	Depth     int    `json:"depth,omitempty" jsonschema:"0 = transitive"`
	Limit     int    `json:"limit,omitempty" jsonschema:"optional max nodes per page"`
	Cursor    string `json:"cursor,omitempty" jsonschema:"optional; next_cursor from a previous page"`
}
type impactOutput struct {
	Timed
	repoindex.ImpactResult
}

func handleRepoImpact(_ context.Context, svcs *service.Services, in impactInput) (*impactOutput, error) {
	reverse := in.Direction != "forward"
	res, ok, err := svcs.Repo.ImpactAt(in.Path, in.Graph, in.Target, reverse, in.Depth, in.Limit, in.Cursor)
	if !ok {
		return nil, fmt.Errorf("path %q is not inside a ready repo configured with -repo", in.Path)
	}
	if err != nil {
		return nil, err
	}
	return &impactOutput{ImpactResult: res}, nil
}

type graphQueryInput struct {
	Path  string `json:"path" jsonschema:"any file or directory inside a repo configured with -repo"`
	Graph string `json:"graph" jsonschema:"calls|imports"`
}
type cyclesOutput struct {
	Timed
	Cycles [][]string `json:"cycles"`
}

func handleRepoCycles(_ context.Context, svcs *service.Services, in graphQueryInput) (*cyclesOutput, error) {
	cycles, ok, err := svcs.Repo.CyclesAt(in.Path, in.Graph)
	if !ok {
		return nil, fmt.Errorf("path %q is not inside a ready repo configured with -repo", in.Path)
	}
	if err != nil {
		return nil, err
	}
	return &cyclesOutput{Cycles: cycles}, nil
}

type topologyOutput struct {
	Timed
	Layers [][]string `json:"layers"`
}

func handleRepoTopology(_ context.Context, svcs *service.Services, in graphQueryInput) (*topologyOutput, error) {
	layers, ok, err := svcs.Repo.TopologyAt(in.Path, in.Graph)
	if !ok {
		return nil, fmt.Errorf("path %q is not inside a ready repo configured with -repo", in.Path)
	}
	if err != nil {
		return nil, err
	}
	return &topologyOutput{Layers: layers}, nil
}

type parseASTInput struct {
	Language string `json:"language,omitempty" jsonschema:"optional; language name"`
	Path     string `json:"path" jsonschema:"path to the source file"`
	MaxDepth int    `json:"max_depth,omitempty" jsonschema:"optional; max AST depth, 0 defaults to 20"`
}
type parseASTOutput struct {
	Timed
	Language string       `json:"language"`
	Path     string       `json:"path"`
	HasError bool         `json:"has_error"`
	AST      *engine.Node `json:"ast"`
}

func handleParseAST(_ context.Context, svcs *service.Services, in parseASTInput) (*parseASTOutput, error) {
	l, err := svcs.Engine.Resolve(in.Language, in.Path)
	if err != nil {
		return nil, err
	}
	maxDepth := in.MaxDepth
	if maxDepth == 0 {
		maxDepth = 20
	}
	root, hasErr, err := svcs.Engine.Parse(l, in.Path, maxDepth)
	if err != nil {
		return nil, err
	}
	return &parseASTOutput{Language: l.Name(), Path: in.Path, HasError: hasErr, AST: root}, nil
}

type queryASTInput struct {
	Language    string `json:"language,omitempty" jsonschema:"optional; language name"`
	Path        string `json:"path" jsonschema:"path to the source file"`
	Query       string `json:"query" jsonschema:"tree-sitter query syntax"`
	Limit       int    `json:"limit,omitempty" jsonschema:"optional; max matches"`
	IncludeText bool   `json:"include_text,omitempty" jsonschema:"optional full text"`
}
type queryASTOutput struct {
	Timed
	Language string         `json:"language"`
	Matches  []engine.Match `json:"matches"`
}

func handleQueryAST(_ context.Context, svcs *service.Services, in queryASTInput) (*queryASTOutput, error) {
	l, err := svcs.Engine.Resolve(in.Language, in.Path)
	if err != nil {
		return nil, err
	}
	matches, err := svcs.Engine.QueryText(l, in.Path, in.Query, in.Limit, in.IncludeText)
	if err != nil {
		return nil, err
	}
	return &queryASTOutput{Language: l.Name(), Matches: matches}, nil
}

type scanSymbolsInput struct {
	Path        string   `json:"path" jsonschema:"file or directory"`
	Languages   []string `json:"languages,omitempty" jsonschema:"optional language filter"`
	Kinds       []string `json:"kinds,omitempty" jsonschema:"optional symbol kinds"`
	Name        string   `json:"name,omitempty" jsonschema:"optional exact name"`
	IncludeText bool     `json:"include_text,omitempty" jsonschema:"optional full text"`
	Limit       int      `json:"limit,omitempty" jsonschema:"optional max files"`
}
type scanSymbolsOutput struct {
	Timed
	service.ScanResult
}

func handleScanSymbols(ctx context.Context, svcs *service.Services, in scanSymbolsInput) (*scanSymbolsOutput, error) {
	if in.Path == "" {
		return nil, fmt.Errorf("path is required")
	}
	if res, ok, err := svcs.Repo.ScanAt(in.Path, in.Languages, in.Kinds, in.Name, in.Limit); ok {
		return &scanSymbolsOutput{ScanResult: *res}, err
	}
	st, err := os.Stat(in.Path)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		lang := ""
		if len(in.Languages) == 1 {
			lang = in.Languages[0]
		}
		res, err := svcs.Scan.Path(lang, in.Path, in.IncludeText)
		if err != nil {
			return nil, err
		}
		return &scanSymbolsOutput{ScanResult: *res}, nil
	}
	res, err := svcs.Scan.Dir(ctx, service.ScanQuery{
		Dir: in.Path, Languages: in.Languages, Kinds: in.Kinds,
		Name: in.Name, IncludeText: in.IncludeText, Limit: in.Limit,
	})
	if err != nil {
		return nil, err
	}
	return &scanSymbolsOutput{ScanResult: *res}, nil
}

type analyzeInput struct {
	Language string `json:"language,omitempty" jsonschema:"optional; language name"`
	Path     string `json:"path" jsonschema:"path to the source file"`
}
type analyzeOutput struct {
	Timed
	service.FileReport
}

func handleAnalyzeFile(ctx context.Context, svcs *service.Services, in analyzeInput) (*analyzeOutput, error) {
	if res, ok, err := svcs.Repo.AnalyzeAt(in.Path); ok {
		return &analyzeOutput{FileReport: *res}, err
	}
	res, err := svcs.File.Dossier(in.Language, in.Path)
	if err != nil {
		return nil, err
	}
	return &analyzeOutput{FileReport: *res}, nil
}

type getTextInput struct {
	Language string `json:"language,omitempty" jsonschema:"optional; language name"`
	Path     string `json:"path" jsonschema:"path to the source file"`
	StartRow int    `json:"start_row" jsonschema:"0-based start row inclusive"`
	StartCol int    `json:"start_col" jsonschema:"0-based start col inclusive"`
	EndRow   int    `json:"end_row" jsonschema:"0-based end row exclusive"`
	EndCol   int    `json:"end_col" jsonschema:"0-based end col exclusive"`
}
type getTextOutput struct {
	Timed
	Language string `json:"language"`
	Path     string `json:"path"`
	Text     string `json:"text"`
}

func handleGetText(_ context.Context, svcs *service.Services, in getTextInput) (*getTextOutput, error) {
	l, err := svcs.Engine.Resolve(in.Language, in.Path)
	if err != nil {
		return nil, err
	}
	text, err := svcs.Engine.GetText(l, in.Path, engine.Point{Row: in.StartRow, Col: in.StartCol}, engine.Point{Row: in.EndRow, Col: in.EndCol})
	if err != nil {
		return nil, err
	}
	return &getTextOutput{Language: l.Name(), Path: in.Path, Text: text}, nil
}

type findUsagesInput struct {
	Mode        string   `json:"mode" jsonschema:"occurrences|callers|unused|definitions|imports"`
	Name        string   `json:"name,omitempty" jsonschema:"symbol name; required except unused"`
	Path        string   `json:"path" jsonschema:"directory to scan"`
	Languages   []string `json:"languages,omitempty" jsonschema:"optional language filter"`
	Limit       int      `json:"limit,omitempty" jsonschema:"optional max results per page"`
	GroupByFile *bool    `json:"group_by_file,omitempty" jsonschema:"optional; default true"`
	Kinds       []string `json:"kinds,omitempty" jsonschema:"optional occurrence kinds"`
	Cursor      string   `json:"cursor,omitempty" jsonschema:"optional; next_cursor from a previous page"`
}
type findUsagesOutput struct {
	Timed
	service.FindResult
}

func handleFindUsages(ctx context.Context, svcs *service.Services, in findUsagesInput) (*findUsagesOutput, error) {
	if in.Path == "" {
		return nil, fmt.Errorf("path is required")
	}
	group := true
	if in.GroupByFile != nil {
		group = *in.GroupByFile
	}
	if _, indexed := svcs.Repo.ResolveIndex(in.Path); indexed {
		if in.Name == "" && in.Mode != string(service.FindUnused) {
			return nil, fmt.Errorf("name is required for mode %q", in.Mode)
		}
		switch in.Mode {
		case string(service.FindUnused):
			res, _, err := svcs.Repo.UnusedAt(in.Path, in.Limit)
			if err != nil {
				return nil, err
			}
			return &findUsagesOutput{FindResult: service.FindResult{
				Language: "indexed", Mode: in.Mode, Source: "indexed_heuristic", Symbols: res.Matches,
			}}, nil
		case string(service.FindCallers):
			callers, _, err := svcs.Repo.CallersAt(in.Path, in.Name, in.Limit)
			if err != nil {
				return nil, err
			}
			return &findUsagesOutput{FindResult: service.FindResult{
				Language: "indexed", Mode: in.Mode, Source: "indexed", Callers: callers,
			}}, nil
		case string(service.FindDefinitions), string(service.FindImports):
			matches, _, err := svcs.Repo.DefinitionsAt(in.Path, in.Name, in.Mode == string(service.FindImports), in.Limit)
			if err != nil {
				return nil, err
			}
			res := &service.FindResult{
				Language: "indexed", Mode: in.Mode, Source: "indexed", Matches: matches,
			}
			if group {
				res.Files = service.GroupUsageByFile(matches)
			}
			return &findUsagesOutput{FindResult: *res}, nil
		case string(service.FindOccurrences):
			page, _, err := svcs.Repo.FindOccurrencesAt(in.Path, in.Name, in.Cursor, in.Limit)
			if err != nil {
				return nil, err
			}
			res := &service.FindResult{
				Language:   "indexed",
				Mode:       in.Mode,
				Source:     "indexed",
				Matches:    page.Matches,
				Kinds:      in.Kinds,
				NextCursor: page.NextCursor,
				Truncated:  page.Truncated,
			}
			if group {
				res.Files = service.GroupUsageByFile(page.Matches)
			}
			return &findUsagesOutput{FindResult: *res}, nil
		default:
			return nil, fmt.Errorf("invalid find_usages mode %q", in.Mode)
		}
	}
	res, err := svcs.Find.Dir(ctx, service.FindQuery{
		Mode: service.FindMode(in.Mode), Name: in.Name, Dir: in.Path,
		Languages: in.Languages, Limit: in.Limit, GroupByFile: group, Kinds: in.Kinds,
	})
	if err != nil {
		return nil, err
	}
	return &findUsagesOutput{FindResult: *res}, nil
}

type rankComplexityInput struct {
	Path      string   `json:"path" jsonschema:"directory to scan"`
	Languages []string `json:"languages,omitempty" jsonschema:"optional language filter"`
	Limit     int      `json:"limit,omitempty" jsonschema:"optional; default 20"`
}
type rankComplexityOutput struct {
	Timed
	service.RankResult
}

func handleRankComplexity(ctx context.Context, svcs *service.Services, in rankComplexityInput) (*rankComplexityOutput, error) {
	if in.Path == "" {
		return nil, fmt.Errorf("path is required")
	}
	limit := in.Limit
	if limit == 0 {
		limit = 20
	}
	if entries, ok, err := svcs.Repo.ComplexityAt(in.Path, limit); ok {
		return &rankComplexityOutput{RankResult: service.RankResult{Language: "indexed", Source: "indexed", Entries: entries}}, err
	}
	res, err := svcs.Rank.ComplexityDir(ctx, in.Path, in.Languages, limit)
	if err != nil {
		return nil, err
	}
	return &rankComplexityOutput{RankResult: *res}, nil
}

type outlineInput struct {
	Language    string `json:"language,omitempty" jsonschema:"optional; language name"`
	Path        string `json:"path" jsonschema:"path to the source file"`
	IncludeText bool   `json:"include_text,omitempty" jsonschema:"optional full text"`
}
type outlineOutput struct {
	Timed
	service.OutlineResult
}

func handleOutlineFile(ctx context.Context, svcs *service.Services, in outlineInput) (*outlineOutput, error) {
	if res, ok, err := svcs.Repo.OutlineAt(in.Path, in.IncludeText); ok {
		return &outlineOutput{OutlineResult: *res}, err
	}
	res, err := svcs.File.Outline(in.Language, in.Path, in.IncludeText)
	if err != nil {
		return nil, err
	}
	return &outlineOutput{OutlineResult: *res}, nil
}
