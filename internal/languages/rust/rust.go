package rust

import (
	ts "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_rust "github.com/tree-sitter/tree-sitter-rust/bindings/go"

	"mcp-ast/internal/lang"
)

type Rust struct{}

func (Rust) Name() string { return "rust" }

func (Rust) Extensions() []string { return []string{".rs"} }

func (Rust) Language() *ts.Language {
	return ts.NewLanguage(tree_sitter_rust.Language())
}

func (Rust) SymbolQueries() map[string]string {
	return map[string]string{
		"functions": `(function_item name: (identifier) @name) @symbol`,
		"structs":   `(struct_item name: (type_identifier) @name) @symbol`,
		"enums":     `(enum_item name: (type_identifier) @name) @symbol`,
		"traits":    `(trait_item name: (type_identifier) @name) @symbol`,
		"impls":     `(impl_item trait: (type_identifier) @name) @symbol`,
		"modules":   `(mod_item name: (identifier) @name) @symbol`,
		"types":     `(type_item name: (type_identifier) @name) @symbol`,
		"imports":   `(use_declaration) @symbol`,
		"variables": `(let_declaration pattern: (identifier) @name) @symbol`,
	}
}

func (Rust) DecisionKinds() []string {
	return []string{
		"if_expression",
		"if_let_expression",
		"for_expression",
		"while_expression",
		"loop_expression",
		"match_expression",
		"match_arm",
		"binary_expression", // only counts when the operator is && or ||
	}
}

func (Rust) AuxQueries() map[string]string {
	return map[string]string{
		"identifiers": `[(identifier) (type_identifier) (field_identifier) (scoped_identifier)] @id`,
		"calls": `[
			(call_expression function: (identifier) @callee)
			(call_expression function: (field_expression field: (field_identifier) @callee))
			(call_expression function: (scoped_identifier) @callee)
		]`,
	}
}

var _ lang.Language = Rust{}
