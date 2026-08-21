package tools

import (
	"context"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"mcp-java-ast/internal/engine"
	"mcp-java-ast/internal/lang"
)

type tools struct {
	engine *engine.Engine
}

// AST nodes are recursive, which the SDK's schema inference rejects (cycle),
// so parse_ast ships a hand-written recursive output schema.
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

func Register(s *mcp.Server, eng *engine.Engine) {
	t := &tools{engine: eng}
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_languages",
		Description: "List the programming languages supported by this AST analysis server.",
	}, timed(t.listLanguages))
	mcp.AddTool(s, &mcp.Tool{
		Name:         "parse_ast_file",
		Description:  "Parse a source file and return its abstract syntax tree (AST) as JSON. Language is auto-detected from the file extension when omitted.",
		OutputSchema: parseASTSchema,
	}, timed(t.parseAST))
	mcp.AddTool(s, &mcp.Tool{
		Name:        "query_ast_file",
		Description: "Run a tree-sitter query over a source file and return the matches with captures, text and positions.",
	}, timed(t.queryAST))
	mcp.AddTool(s, &mcp.Tool{
		Name:        "symbols_file",
		Description: "Extract symbols (classes, methods, fields, imports, ...) from a source file, grouped by kind, using the language's built-in queries.",
	}, timed(t.symbols))
	mcp.AddTool(s, &mcp.Tool{
		Name:        "scan_symbols_dir",
		Description: "Recursively scan a directory and return symbols of every recognized source file, grouped by file path. Errors reading individual files are reported per-file.",
	}, timed(t.scanSymbols))
	mcp.AddTool(s, &mcp.Tool{
		Name:        "analyze_file",
		Description: "Compute metrics for a source file: size, node count, nesting depth, and per-symbol-kind line statistics (count, avg/max lines).",
	}, timed(t.analyze))
	mcp.AddTool(s, &mcp.Tool{
		Name:        "scan_variables_dir",
		Description: "Recursively scan a directory and return the variables of every recognized source file, grouped by file path. Errors reading individual files are reported per-file.",
	}, timed(t.scanVariables))
	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_text_file",
		Description: "Return the exact source text of a 0-based (row, col) range, e.g. the positions reported on every node, capture or symbol. Use to read full code without truncation.",
	}, timed(t.getText))
	mcp.AddTool(s, &mcp.Tool{
		Name:        "search_name_dir",
		Description: "Search for a symbol by name (class, function, variable, etc.) across source files in a directory, using the AST. Returns only declarations with their kind, file and position.",
	}, timed(t.searchName))
}

type listLanguagesInput struct{}

type listLanguagesOutput struct {
	Timed
	Languages []string `json:"languages"`
}

func (t *tools) listLanguages(ctx context.Context, req *mcp.CallToolRequest, in listLanguagesInput) (*mcp.CallToolResult, *listLanguagesOutput, error) {
	return nil, &listLanguagesOutput{Languages: t.engine.ListLanguages()}, nil
}

type parseASTInput struct {
	Language string `json:"language,omitempty" jsonschema:"optional; language name, e.g. java. Omit to auto-detect from the file extension"`
	Path     string `json:"path" jsonschema:"path to the source file to parse"`
	MaxDepth int    `json:"max_depth,omitempty" jsonschema:"optional; maximum AST depth to include in the output, 0 uses the default of 20"`
}

type parseASTOutput struct {
	Timed
	Language string       `json:"language"`
	Path     string       `json:"path"`
	HasError bool         `json:"has_error"`
	AST      *engine.Node `json:"ast"`
}

func (t *tools) parseAST(ctx context.Context, req *mcp.CallToolRequest, in parseASTInput) (*mcp.CallToolResult, *parseASTOutput, error) {
	l, err := t.engine.Resolve(in.Language, in.Path)
	if err != nil {
		return nil, nil, err
	}
	maxDepth := in.MaxDepth
	if maxDepth == 0 {
		maxDepth = 20
	}
	root, hasErr, err := t.engine.Parse(l, in.Path, maxDepth)
	if err != nil {
		return nil, nil, err
	}
	return nil, &parseASTOutput{Language: l.Name(), Path: in.Path, HasError: hasErr, AST: root}, nil
}

type queryASTInput struct {
	Language    string `json:"language,omitempty" jsonschema:"optional; language name, e.g. java. Omit to auto-detect from the file extension"`
	Path        string `json:"path" jsonschema:"path to the source file to query"`
	Query       string `json:"query" jsonschema:"tree-sitter query, e.g. (method_declaration name: (identifier) @name) @method"`
	Limit       int    `json:"limit,omitempty" jsonschema:"optional; maximum number of matches to return, 0 = unlimited"`
	IncludeText bool   `json:"include_text,omitempty" jsonschema:"optional; include full node text in captures instead of the first-line summary"`
}

type queryASTOutput struct {
	Timed
	Language string         `json:"language"`
	Matches  []engine.Match `json:"matches"`
}

func (t *tools) queryAST(ctx context.Context, req *mcp.CallToolRequest, in queryASTInput) (*mcp.CallToolResult, *queryASTOutput, error) {
	l, err := t.engine.Resolve(in.Language, in.Path)
	if err != nil {
		return nil, nil, err
	}
	matches, err := t.engine.QueryText(l, in.Path, in.Query, in.Limit, in.IncludeText)
	if err != nil {
		return nil, nil, err
	}
	return nil, &queryASTOutput{Language: l.Name(), Matches: matches}, nil
}

type symbolsInput struct {
	Language    string `json:"language,omitempty" jsonschema:"optional; language name, e.g. java. Omit to auto-detect from the file extension"`
	Path        string `json:"path" jsonschema:"path to the source file to analyze"`
	IncludeText bool   `json:"include_text,omitempty" jsonschema:"optional; include full symbol text instead of the first-line summary"`
}

type symbolsOutput struct {
	Timed
	Language string                     `json:"language"`
	Symbols  map[string][]engine.Symbol `json:"symbols"`
}

func (t *tools) symbols(ctx context.Context, req *mcp.CallToolRequest, in symbolsInput) (*mcp.CallToolResult, *symbolsOutput, error) {
	l, err := t.engine.Resolve(in.Language, in.Path)
	if err != nil {
		return nil, nil, err
	}
	syms, err := t.engine.SymbolsText(l, in.Path, in.IncludeText)
	if err != nil {
		return nil, nil, err
	}
	return nil, &symbolsOutput{Language: l.Name(), Symbols: syms}, nil
}

type scanSymbolsInput struct {
	Path     string `json:"path" jsonschema:"directory to scan recursively"`
	Language string `json:"language,omitempty" jsonschema:"optional; language name (e.g. go). Omit to auto-detect each file by extension"`
}

type scanSymbolsOutput struct {
	Timed
	Language string                                `json:"language"`
	Files    map[string]map[string][]engine.Symbol `json:"files"`
	Errors   map[string]string                     `json:"errors,omitempty"`
}

func (t *tools) scanSymbols(ctx context.Context, req *mcp.CallToolRequest, in scanSymbolsInput) (*mcp.CallToolResult, *scanSymbolsOutput, error) {
	var filter lang.Language
	if in.Language != "" {
		var err error
		filter, err = t.engine.Resolve(in.Language, in.Path)
		if err != nil {
			return nil, nil, err
		}
	}
	files, errs, err := t.engine.ScanSymbols(ctx, in.Path, filter)
	if err != nil {
		return nil, nil, err
	}
	langName := "auto"
	if filter != nil {
		langName = filter.Name()
	}
	return nil, &scanSymbolsOutput{Language: langName, Files: files, Errors: errs}, nil
}

type scanVariablesInput struct {
	Path     string `json:"path" jsonschema:"directory to scan recursively"`
	Language string `json:"language,omitempty" jsonschema:"optional; language name (e.g. go). Omit to auto-detect each file by extension"`
}

type scanVariablesOutput struct {
	Timed
	Language  string                     `json:"language"`
	Variables map[string][]engine.Symbol `json:"variables"`
	Errors    map[string]string          `json:"errors,omitempty"`
}

func (t *tools) scanVariables(ctx context.Context, req *mcp.CallToolRequest, in scanVariablesInput) (*mcp.CallToolResult, *scanVariablesOutput, error) {
	var filter lang.Language
	if in.Language != "" {
		var err error
		filter, err = t.engine.Resolve(in.Language, in.Path)
		if err != nil {
			return nil, nil, err
		}
	}
	variables, errs, err := t.engine.ScanVariables(ctx, in.Path, filter)
	if err != nil {
		return nil, nil, err
	}
	langName := "auto"
	if filter != nil {
		langName = filter.Name()
	}
	return nil, &scanVariablesOutput{Language: langName, Variables: variables, Errors: errs}, nil
}

type analyzeInput struct {
	Language string `json:"language,omitempty" jsonschema:"optional; language name, e.g. java. Omit to auto-detect from the file extension"`
	Path     string `json:"path" jsonschema:"path to the source file to analyze"`
}

type analyzeOutput struct {
	Timed
	Language string          `json:"language"`
	Metrics  *engine.Metrics `json:"metrics"`
}

func (t *tools) analyze(ctx context.Context, req *mcp.CallToolRequest, in analyzeInput) (*mcp.CallToolResult, *analyzeOutput, error) {
	l, err := t.engine.Resolve(in.Language, in.Path)
	if err != nil {
		return nil, nil, err
	}
	metrics, err := t.engine.Analyze(l, in.Path)
	if err != nil {
		return nil, nil, err
	}
	return nil, &analyzeOutput{Language: l.Name(), Metrics: metrics}, nil
}

type getTextInput struct {
	Language string `json:"language,omitempty" jsonschema:"optional; language name, e.g. java. Omit to auto-detect from the file extension"`
	Path     string `json:"path" jsonschema:"path to the source file"`
	StartRow int    `json:"start_row" jsonschema:"0-based start row (inclusive)"`
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

func (t *tools) getText(ctx context.Context, req *mcp.CallToolRequest, in getTextInput) (*mcp.CallToolResult, *getTextOutput, error) {
	l, err := t.engine.Resolve(in.Language, in.Path)
	if err != nil {
		return nil, nil, err
	}
	text, err := t.engine.GetText(l, in.Path, engine.Point{Row: in.StartRow, Col: in.StartCol}, engine.Point{Row: in.EndRow, Col: in.EndCol})
	if err != nil {
		return nil, nil, err
	}
	return nil, &getTextOutput{Language: l.Name(), Path: in.Path, Text: text}, nil
}

type searchNameInput struct {
	Name     string `json:"name" jsonschema:"name to search for (e.g. class name, function name, variable)"`
	Path     string `json:"path" jsonschema:"directory to search recursively"`
	Language string `json:"language,omitempty" jsonschema:"optional; language name (e.g. go, java). Omit to search all recognized files"`
	Limit    int    `json:"limit,omitempty" jsonschema:"optional; maximum number of matches to return, 0 = unlimited"`
}

type searchNameOutput struct {
	Timed
	Total   int                  `json:"total"`
	Matches []engine.SearchMatch `json:"matches"`
	Errors  map[string]string    `json:"errors,omitempty"`
}

func (t *tools) searchName(ctx context.Context, req *mcp.CallToolRequest, in searchNameInput) (*mcp.CallToolResult, *searchNameOutput, error) {
	result, err := t.engine.SearchName(ctx, in.Path, in.Name, in.Language, in.Limit)
	if err != nil {
		return nil, nil, err
	}
	return nil, &searchNameOutput{Total: result.Total, Matches: result.Matches, Errors: result.Errors}, nil
}
