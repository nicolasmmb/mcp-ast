package tools

import (
	"context"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"mcp-java-ast/internal/engine"
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
		Name:         "parse_ast",
		Description:  "Parse a source file and return its abstract syntax tree (AST) as JSON. Language is auto-detected from the file extension when omitted.",
		OutputSchema: parseASTSchema,
	}, timed(t.parseAST))
	mcp.AddTool(s, &mcp.Tool{
		Name:        "query_ast",
		Description: "Run a tree-sitter query over a source file and return the matches with captures, text and positions.",
	}, timed(t.queryAST))
	mcp.AddTool(s, &mcp.Tool{
		Name:        "symbols",
		Description: "Extract symbols (classes, methods, fields, imports, ...) from a source file, grouped by kind, using the language's built-in queries.",
	}, timed(t.symbols))
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
	Language string `json:"language" jsonschema:"language name, e.g. java"`
	Path     string `json:"path" jsonschema:"path to the source file to query"`
	Query    string `json:"query" jsonschema:"tree-sitter query, e.g. (method_declaration name: (identifier) @name) @method"`
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
	matches, err := t.engine.Query(l, in.Path, in.Query)
	if err != nil {
		return nil, nil, err
	}
	return nil, &queryASTOutput{Language: l.Name(), Matches: matches}, nil
}

type symbolsInput struct {
	Language string `json:"language,omitempty" jsonschema:"optional; language name, e.g. java. Omit to auto-detect from the file extension"`
	Path     string `json:"path" jsonschema:"path to the source file to analyze"`
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
	syms, err := t.engine.Symbols(l, in.Path)
	if err != nil {
		return nil, nil, err
	}
	return nil, &symbolsOutput{Language: l.Name(), Symbols: syms}, nil
}
