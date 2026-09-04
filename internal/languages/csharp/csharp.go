package csharp

import (
	ts "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_c_sharp "github.com/tree-sitter/tree-sitter-c-sharp/bindings/go"

	"mcp-ast/internal/lang"
)

type CSharp struct{}

func (CSharp) Name() string { return "csharp" }

func (CSharp) Extensions() []string { return []string{".cs"} }

func (CSharp) Language() *ts.Language {
	return ts.NewLanguage(tree_sitter_c_sharp.Language())
}

func (CSharp) SymbolQueries() map[string]string {
	return map[string]string{
		"classes":      `(class_declaration name: (identifier) @name) @symbol`,
		"interfaces":   `(interface_declaration name: (identifier) @name) @symbol`,
		"structs":      `(struct_declaration name: (identifier) @name) @symbol`,
		"enums":        `(enum_declaration name: (identifier) @name) @symbol`,
		"methods":      `(method_declaration name: (identifier) @name) @symbol`,
		"properties":   `(property_declaration name: (identifier) @name) @symbol`,
		"fields":       `(field_declaration (variable_declaration (variable_declarator (identifier) @name))) @symbol`,
		"constructors": `(constructor_declaration name: (identifier) @name) @symbol`,
		"imports":      `(using_directive) @symbol`,
		"namespaces":   `(namespace_declaration name: [(identifier) (qualified_name)] @name) @symbol`,
		"variables":    `(variable_declaration (variable_declarator (identifier) @name)) @symbol`,
	}
}

func (CSharp) DecisionKinds() []string {
	return []string{
		"if_statement",
		"for_statement",
		"foreach_statement",
		"while_statement",
		"do_statement",
		"switch_statement",
		"switch_section",
		"ternary_expression",
		"catch_clause",
		"binary_expression",
	}
}

func (CSharp) AuxQueries() map[string]string {
	return map[string]string{
		"identifiers": `[(identifier) (qualified_name)] @id`,
		"calls": `(invocation_expression function: (identifier) @callee)
(invocation_expression function: (member_access_expression name: (identifier) @callee))`,
	}
}

var _ lang.Language = CSharp{}
