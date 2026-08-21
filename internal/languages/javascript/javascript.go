package javascript

import (
	ts "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_javascript "github.com/tree-sitter/tree-sitter-javascript/bindings/go"

	"mcp-ast/internal/lang"
)

type JavaScript struct{}

func (JavaScript) Name() string { return "javascript" }

func (JavaScript) Extensions() []string { return []string{".js", ".jsx"} }

func (JavaScript) Language() *ts.Language {
	return ts.NewLanguage(tree_sitter_javascript.Language())
}

func (JavaScript) SymbolQueries() map[string]string {
	return map[string]string{
		"functions": `(function_declaration name: (identifier) @name) @symbol`,
		"classes":   `(class_declaration name: (identifier) @name) @symbol`,
		"methods":   `(method_definition name: [(property_identifier) (private_property_identifier)] @name) @symbol`,
		"imports":   `[(import_statement) (import_clause)] @symbol`,
		"variables": `[(lexical_declaration (variable_declarator name: (identifier) @name)) (variable_declaration (variable_declarator name: (identifier) @name))] @symbol`,
	}
}

func (JavaScript) DecisionKinds() []string {
	return []string{
		"if_statement",
		"for_statement",
		"for_in_statement",
		"while_statement",
		"do_statement",
		"switch_statement",
		"switch_case",
		"ternary_expression",
		"catch_clause",
		"binary_expression", // only counts when the operator is && or ||
	}
}

func (JavaScript) AuxQueries() map[string]string {
	return map[string]string{
		"identifiers": `[(identifier) (property_identifier) (private_property_identifier)] @id`,
		"calls":       `(call_expression function: [(identifier) (member_expression (property_identifier))] @callee)`,
	}
}

var _ lang.Language = JavaScript{}
