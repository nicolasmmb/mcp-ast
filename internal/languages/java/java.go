package java

import (
	ts "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_java "github.com/tree-sitter/tree-sitter-java/bindings/go"

	"mcp-ast/internal/lang"
)

type Java struct{}

func (Java) Name() string { return "java" }

func (Java) Extensions() []string { return []string{".java"} }

func (Java) Language() *ts.Language {
	return ts.NewLanguage(tree_sitter_java.Language())
}

func (Java) SymbolQueries() map[string]string {
	return map[string]string{
		"classes":      `(class_declaration name: (identifier) @name) @symbol`,
		"interfaces":   `(interface_declaration name: (identifier) @name) @symbol`,
		"enums":        `(enum_declaration name: (identifier) @name) @symbol`,
		"records":      `(record_declaration name: (identifier) @name) @symbol`,
		"methods":      `(method_declaration name: (identifier) @name) @symbol`,
		"constructors": `(constructor_declaration name: (identifier) @name) @symbol`,
		"fields":       `(field_declaration declarator: (variable_declarator name: (identifier) @name)) @symbol`,
		"imports":      `(import_declaration) @symbol`,
		"variables":    `(local_variable_declaration declarator: (variable_declarator name: (identifier) @name)) @symbol`,
	}
}

func (Java) DecisionKinds() []string {
	return []string{
		"if_statement",
		"for_statement",
		"enhanced_for_statement",
		"while_statement",
		"do_statement",
		"switch_expression",
		"switch_block_statement_group",
		"switch_rule",
		"ternary_expression",
		"catch_clause",
		"binary_expression", // only counts when the operator is && or ||
	}
}

func (Java) AuxQueries() map[string]string {
	return map[string]string{
		"identifiers": `[(identifier) (type_identifier) (scoped_identifier)] @id`,
		"calls":       `(method_invocation name: (identifier) @callee)`,
	}
}

var _ lang.Language = Java{}
