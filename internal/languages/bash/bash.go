package bash

import (
	ts "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_bash "github.com/tree-sitter/tree-sitter-bash/bindings/go"

	"mcp-ast/internal/lang"
)

type Bash struct{}

func (Bash) Name() string { return "bash" }

func (Bash) Extensions() []string { return []string{".sh", ".bash"} }

func (Bash) Language() *ts.Language {
	return ts.NewLanguage(tree_sitter_bash.Language())
}

func (Bash) SymbolQueries() map[string]string {
	return map[string]string{
		"functions": `(function_definition name: (word) @name) @symbol`,
		"variables": `(variable_assignment variable: (variable_name) @name) @symbol`,
	}
}

func (Bash) DecisionKinds() []string {
	return []string{
		"if_statement",
		"elif_clause",
		"for_statement",
		"c_style_for_statement",
		"while_statement",
		"case_statement",
		"case_item",
		"pipeline",
		"command_substitution",
		"test_command",
		"binary_expression", // only counts when the operator is && or ||
	}
}

func (Bash) AuxQueries() map[string]string {
	return map[string]string{
		"identifiers": `[(word) (variable_name)] @id`,
		"calls":       `(command_name (word) @callee)`,
	}
}

var _ lang.Language = Bash{}
