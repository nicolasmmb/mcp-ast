package tools

import (
	"context"
	"os"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"mcp-ast/internal/engine"
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
		Description: "List programming languages supported by this AST server. Call first to discover available languages before other tools.",
	}, handleListLanguages)

	add(s, svcs, &mcp.Tool{
		Name:         "parse_ast",
		Description:  "Parse one source file and return its AST as recursive JSON. Prefer query_ast for targeted extraction; set max_depth (e.g. 5) on large files.",
		OutputSchema: parseASTSchema,
	}, handleParseAST)

	add(s, svcs, &mcp.Tool{
		Name:        "query_ast",
		Description: "Run a tree-sitter query on one file. Prefer scan_symbols for built-in symbol kinds.",
	}, handleQueryAST)

	add(s, svcs, &mcp.Tool{
		Name:        "scan_symbols",
		Description: "Extract symbols from a file OR directory. Prefer outline_file for hierarchy; find_usages for references.",
	}, handleScanSymbols)

	add(s, svcs, &mcp.Tool{
		Name:        "analyze_file",
		Description: "Full dossier for one file: metrics, complexity, call graph. For directory hotspots use rank_complexity.",
	}, handleAnalyzeFile)

	add(s, svcs, &mcp.Tool{
		Name:        "get_text",
		Description: "Return exact source text for a 0-based (row,col) range from other tool outputs.",
	}, handleGetText)

	add(s, svcs, &mcp.Tool{
		Name:        "find_usages",
		Description: "Find usages in a directory. mode=occurrences|callers|unused|definitions|imports. group_by_file defaults true. Run before rename/delete.",
	}, handleFindUsages)

	add(s, svcs, &mcp.Tool{
		Name:        "rank_complexity",
		Description: "Rank functions/methods in a directory by cyclomatic complexity (top-N). Default limit=20.",
	}, handleRankComplexity)

	add(s, svcs, &mcp.Tool{
		Name:         "outline_file",
		Description:  "Hierarchical symbol outline for one file via range containment. Prefer get_text for bodies.",
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
	IncludeText bool   `json:"include_text,omitempty" jsonschema:"optional; full text"`
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

func handleAnalyzeFile(_ context.Context, svcs *service.Services, in analyzeInput) (*analyzeOutput, error) {
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
	Limit       int      `json:"limit,omitempty" jsonschema:"optional max results"`
	GroupByFile *bool    `json:"group_by_file,omitempty" jsonschema:"optional; default true"`
	Kinds       []string `json:"kinds,omitempty" jsonschema:"optional occurrence kinds"`
}
type findUsagesOutput struct {
	Timed
	service.FindResult
}

func handleFindUsages(ctx context.Context, svcs *service.Services, in findUsagesInput) (*findUsagesOutput, error) {
	group := true
	if in.GroupByFile != nil {
		group = *in.GroupByFile
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
	limit := in.Limit
	if limit == 0 {
		limit = 20
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

func handleOutlineFile(_ context.Context, svcs *service.Services, in outlineInput) (*outlineOutput, error) {
	res, err := svcs.File.Outline(in.Language, in.Path, in.IncludeText)
	if err != nil {
		return nil, err
	}
	return &outlineOutput{OutlineResult: *res}, nil
}
