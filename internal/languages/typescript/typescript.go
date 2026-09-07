package typescript

import (
	ts "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"

	"mcp-ast/internal/lang"
)

type TypeScript struct{}

func (TypeScript) Name() string { return "typescript" }

func (TypeScript) Extensions() []string { return []string{".ts", ".tsx"} }

func (TypeScript) Language() *ts.Language {
	return ts.NewLanguage(tree_sitter_typescript.LanguageTypescript())
}

func (TypeScript) SymbolQueries() map[string]string {
	return map[string]string{
		"functions":  `(function_declaration name: (identifier) @name) @symbol`,
		"classes":    `(class_declaration name: (type_identifier) @name) @symbol`,
		"methods":    `(method_definition name: [(property_identifier) (private_property_identifier)] @name) @symbol`,
		"interfaces": `(interface_declaration name: (type_identifier) @name) @symbol`,
		"types":      `(type_alias_declaration name: (type_identifier) @name) @symbol`,
		"enums":      `(enum_declaration name: (identifier) @name) @symbol`,
		"imports":    `[(import_statement) (import_clause)] @symbol`,
		"variables":  `[(lexical_declaration (variable_declarator name: (identifier) @name)) (variable_declaration (variable_declarator name: (identifier) @name))] @symbol`,
	}
}

func (TypeScript) DecisionKinds() []string {
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

func (TypeScript) AuxQueries() map[string]string {
	return map[string]string{
		"identifiers": `[(identifier) (property_identifier) (private_property_identifier) (type_identifier)] @id`,
		"calls": `[
			(call_expression function: (identifier) @callee)
			(call_expression function: (member_expression property: (property_identifier) @callee))
		]`,
	}
}

var _ lang.Language = TypeScript{}
