package golang

import (
	ts "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_go "github.com/tree-sitter/tree-sitter-go/bindings/go"

	"mcp-ast/internal/lang"
)

type Go struct{}

func (Go) Name() string { return "go" }

func (Go) Extensions() []string { return []string{".go"} }

func (Go) Language() *ts.Language {
	return ts.NewLanguage(tree_sitter_go.Language())
}

func (Go) SymbolQueries() map[string]string {
	return map[string]string{
		"types":     `(type_spec name: (type_identifier) @name) @symbol`,
		"functions": `(function_declaration name: (identifier) @name) @symbol`,
		"methods":   `(method_declaration name: (field_identifier) @name) @symbol`,
		"imports":   `(import_declaration) @symbol`,
		"variables": `[(short_var_declaration left: (expression_list (identifier) @name)) (var_declaration (var_spec (identifier) @name)) (field_declaration name: (field_identifier) @name)] @symbol`,
	}
}

func (Go) DecisionKinds() []string {
	return []string{
		"if_statement",
		"for_statement",
		"expression_switch_statement",
		"type_switch_statement",
		"select_statement",
		"expression_case",
		"type_case",
		"communication_case",
		"default_case",
		"binary_expression",
	}
}

func (Go) AuxQueries() map[string]string {
	return map[string]string{
		"identifiers": `[(identifier) (type_identifier) (field_identifier)] @id`,
		"calls": `(call_expression function: (identifier) @callee)
(call_expression function: (selector_expression field: (field_identifier) @callee))`,
	}
}

var _ lang.Language = Go{}
