package java

import (
	ts "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_java "github.com/tree-sitter/tree-sitter-java/bindings/go"

	"mcp-java-ast/internal/lang"
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
	}
}

var _ lang.Language = Java{}
