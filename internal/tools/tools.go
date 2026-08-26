package tools

import (
	"context"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"mcp-ast/internal/engine"
	"mcp-ast/internal/service"
)

// AST nodes are recursive, which the SDK's schema inference rejects (cycle),
// so parse_ast_file ships a hand-written recursive output schema.
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

// add registers one tool: the handler receives the service container and
// returns the response body; timed() applies timeout + elapsed_ms + logging.
func add[In any, Out TimedOutput](s *mcp.Server, svcs *service.Services, t *mcp.Tool,
	h func(context.Context, *service.Services, In) (Out, error)) {
	mcp.AddTool(s, t, timed(func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		out, err := h(ctx, svcs, in)
		return nil, out, err
	}))
}

// Register declares every tool served by this MCP server. Adding a capability
// is one entry here plus its service method.
func Register(s *mcp.Server, svcs *service.Services) {
	add(s, svcs, &mcp.Tool{
		Name:        "list_languages",
		Description: "List the programming languages supported by this AST analysis server. Call this first to discover which languages are available before using other tools.",
	}, handleListLanguages)
	add(s, svcs, &mcp.Tool{
		Name:         "parse_ast_file",
		Description:  "Parse a source file and return its full abstract syntax tree (AST) as a recursive JSON structure. Each node has type, field, named, start/end positions, and children. Use to explore the structure of a file or discover grammar node types. For targeted extraction prefer query_ast_file; for large files set max_depth (e.g. 5). Language is auto-detected from the file extension when omitted.",
		OutputSchema: parseASTSchema,
	}, handleParseAST)
	add(s, svcs, &mcp.Tool{
		Name:        "query_ast_file",
		Description: "Run a tree-sitter query over a source file and return matching nodes with their captures, text, and positions. Use for surgical extraction — e.g. find all function declarations, all string literals, all error handlers — without reading the whole file. Each match contains a captures array with name, text, start and end. Language is auto-detected from the file extension when omitted.",
	}, handleQueryAST)
	add(s, svcs, &mcp.Tool{
		Name:        "symbols_file",
		Description: "Extract symbols (classes, methods, fields, imports, etc.) from a single source file, grouped by kind. Each symbol has name, text, and position. Use this for one file; use scan_symbols_dir for a whole directory. Language is auto-detected from the file extension when omitted.",
	}, handleSymbolsFile)
	add(s, svcs, &mcp.Tool{
		Name:        "analyze_file",
		Description: "Compute a full dossier for one source file in a single parse: size metrics (lines, bytes, AST nodes, max nesting), per-symbol-kind statistics, cyclomatic complexity per function/method (1 + decision points; scores above 10 are refactor candidates), and the call graph mapping each function to its callees with counts. Use to assess file complexity and internal flow. For directory-wide overviews use scan_symbols_dir.",
	}, handleAnalyzeFile)
	add(s, svcs, &mcp.Tool{
		Name:        "get_text_file",
		Description: "Return the exact source text for a 0-based (row, col) range. Coordinates come from AST nodes, symbols, captures, or matches returned by other tools. Ranges are inclusive at start, exclusive at end. Use to read the actual code behind any position without loading whole files. Language is auto-detected from the file extension when omitted.",
	}, handleGetText)
	add(s, svcs, &mcp.Tool{
		Name:        "scan_symbols_dir",
		Description: "Recursively scan a directory and extract symbols (classes, functions, methods, fields, attributes, variables, imports...) from every recognized source file, grouped by file path, then kind. Narrow with languages[] and/or kinds[]; pass name for an exact declaration lookup across the codebase. Errors per file are reported separately. For a single file, use symbols_file instead.",
	}, handleScanDir)
	add(s, svcs, &mcp.Tool{
		Name:        "unused_symbols_dir",
		Description: "Find symbols declared but never referenced across a directory. Heuristic: a symbol whose name appears exactly once in all recognized source files is likely unused. Use to identify dead code — confirm suspects with callers_dir before deleting. Language is optional — omit to scan all recognized files.",
	}, handleUnused)
	add(s, svcs, &mcp.Tool{
		Name:        "usages_dir",
		Description: "Find every occurrence of a symbol name across a directory, classified as definition (declaration site), call-site (callee of an invocation, carrying the enclosing function in caller), or reference. Use BEFORE renaming or deleting to enumerate all edit points. For aggregated who-calls-X counts per caller use callers_dir; for declarations only, scan_symbols_dir with name is lighter.",
	}, handleUsages)
	add(s, svcs, &mcp.Tool{
		Name:        "callers_dir",
		Description: "Find every function and method across a directory that calls a given target function, aggregated per caller with exact call counts. Given a function name, this answers who depends on it — use to assess the impact of changing or deleting it. For the raw occurrence list use usages_dir; for what a function calls, use analyze_file's call_graph.",
	}, handleCallers)
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
	Language string `json:"language,omitempty" jsonschema:"optional; language name, e.g. java, python, go. Omit to auto-detect from the file extension"`
	Path     string `json:"path" jsonschema:"path to the source file to parse"`
	MaxDepth int    `json:"max_depth,omitempty" jsonschema:"optional; maximum AST depth to include, 0 defaults to 20. Use lower values (3-5) for large files to keep output manageable"`
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
	Language    string `json:"language,omitempty" jsonschema:"optional; language name, e.g. java, python, go. Omit to auto-detect from the file extension"`
	Path        string `json:"path" jsonschema:"path to the source file to query"`
	Query       string `json:"query" jsonschema:"tree-sitter query syntax, e.g. (method_declaration name: (identifier) @name) @method"`
	Limit       int    `json:"limit,omitempty" jsonschema:"optional; maximum number of matches to return, 0 = unlimited"`
	IncludeText bool   `json:"include_text,omitempty" jsonschema:"optional; true returns full source text of each matched node, false (default) returns a first-line summary"`
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

type symbolsInput struct {
	Language    string `json:"language,omitempty" jsonschema:"optional; language name, e.g. java, python, go. Omit to auto-detect from the file extension"`
	Path        string `json:"path" jsonschema:"path to the source file to analyze"`
	IncludeText bool   `json:"include_text,omitempty" jsonschema:"optional; true returns full declaration text, false (default) returns a first-line summary of each symbol"`
}

type symbolsOutput struct {
	Timed
	service.SymbolsResult
}

func handleSymbolsFile(_ context.Context, svcs *service.Services, in symbolsInput) (*symbolsOutput, error) {
	res, err := svcs.File.Symbols(in.Language, in.Path, in.IncludeText)
	if err != nil {
		return nil, err
	}
	return &symbolsOutput{SymbolsResult: *res}, nil
}

type analyzeInput struct {
	Language string `json:"language,omitempty" jsonschema:"optional; language name, e.g. java, python, go. Omit to auto-detect from the file extension"`
	Path     string `json:"path" jsonschema:"path to the source file to analyze"`
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
	Language string `json:"language,omitempty" jsonschema:"optional; language name, e.g. java, python, go. Omit to auto-detect from the file extension"`
	Path     string `json:"path" jsonschema:"path to the source file"`
	StartRow int    `json:"start_row" jsonschema:"0-based start row (inclusive), as reported by AST nodes, symbols, or captures"`
	StartCol int    `json:"start_col" jsonschema:"0-based start byte column (inclusive)"`
	EndRow   int    `json:"end_row" jsonschema:"0-based end row (exclusive)"`
	EndCol   int    `json:"end_col" jsonschema:"0-based end byte column (exclusive)"`
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

type scanInput struct {
	Path        string   `json:"path" jsonschema:"directory to scan recursively"`
	Languages   []string `json:"languages,omitempty" jsonschema:"optional; language names to include, e.g. [\"go\",\"java\"]. Omit to auto-detect each file by extension"`
	Kinds       []string `json:"kinds,omitempty" jsonschema:"optional; symbol kinds to return, e.g. [\"functions\",\"methods\"]. Omit for all kinds. Valid kinds per language - Go: types, functions, methods, variables, imports; Java: classes, interfaces, enums, records, methods, constructors, fields, variables, imports; Python: classes, functions, variables, imports"`
	Name        string   `json:"name,omitempty" jsonschema:"optional; return only declarations whose name equals this value (exact match)"`
	IncludeText bool     `json:"include_text,omitempty" jsonschema:"optional; true returns full declaration text, false (default) returns a first-line summary"`
	Limit       int      `json:"limit,omitempty" jsonschema:"optional; maximum number of files returned, 0 = unlimited"`
}

type scanOutput struct {
	Timed
	service.ScanResult
}

func handleScanDir(ctx context.Context, svcs *service.Services, in scanInput) (*scanOutput, error) {
	res, err := svcs.Scan.Dir(ctx, service.ScanQuery{
		Dir:         in.Path,
		Languages:   in.Languages,
		Kinds:       in.Kinds,
		Name:        in.Name,
		IncludeText: in.IncludeText,
		Limit:       in.Limit,
	})
	if err != nil {
		return nil, err
	}
	return &scanOutput{ScanResult: *res}, nil
}

type unusedInput struct {
	Path      string   `json:"path" jsonschema:"directory to scan recursively"`
	Languages []string `json:"languages,omitempty" jsonschema:"optional; language names to include, e.g. [\"go\",\"java\"]. Omit to auto-detect each file by extension"`
	Limit     int      `json:"limit,omitempty" jsonschema:"optional; maximum number of results, 0 = unlimited"`
}

type unusedOutput struct {
	Timed
	service.UnusedResult
}

func handleUnused(ctx context.Context, svcs *service.Services, in unusedInput) (*unusedOutput, error) {
	res, err := svcs.Unused.Dir(ctx, in.Path, in.Languages, in.Limit)
	if err != nil {
		return nil, err
	}
	return &unusedOutput{UnusedResult: *res}, nil
}

type usagesInput struct {
	Name      string   `json:"name" jsonschema:"symbol name to find occurrences of"`
	Path      string   `json:"path" jsonschema:"directory to scan recursively"`
	Languages []string `json:"languages,omitempty" jsonschema:"optional; language names to include, e.g. [\"go\",\"java\"]. Omit to auto-detect each file by extension"`
	Limit     int      `json:"limit,omitempty" jsonschema:"optional; maximum number of matches, 0 = unlimited"`
}

type usagesOutput struct {
	Timed
	service.UsagesResult
}

func handleUsages(ctx context.Context, svcs *service.Services, in usagesInput) (*usagesOutput, error) {
	res, err := svcs.Usages.Dir(ctx, in.Name, in.Path, in.Languages, in.Limit)
	if err != nil {
		return nil, err
	}
	return &usagesOutput{UsagesResult: *res}, nil
}

type callersInput struct {
	Name      string   `json:"name" jsonschema:"target function/method name to find callers of, e.g. validate, processOrder"`
	Path      string   `json:"path" jsonschema:"directory to scan recursively"`
	Languages []string `json:"languages,omitempty" jsonschema:"optional; language names to include, e.g. [\"go\",\"java\"]. Omit to auto-detect each file by extension"`
	Limit     int      `json:"limit,omitempty" jsonschema:"optional; maximum number of results, 0 = unlimited"`
}

type callersOutput struct {
	Timed
	service.CallersResult
}

func handleCallers(ctx context.Context, svcs *service.Services, in callersInput) (*callersOutput, error) {
	res, err := svcs.Calls.Callers(ctx, in.Name, in.Path, in.Languages, in.Limit)
	if err != nil {
		return nil, err
	}
	return &callersOutput{CallersResult: *res}, nil
}
