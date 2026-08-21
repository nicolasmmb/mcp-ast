package python

import (
	ts "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_python "github.com/tree-sitter/tree-sitter-python/bindings/go"

	"mcp-ast/internal/lang"
)

type Python struct{}

func (Python) Name() string { return "python" }

func (Python) Extensions() []string { return []string{".py"} }

func (Python) Language() *ts.Language {
	return ts.NewLanguage(tree_sitter_python.Language())
}

func (Python) SymbolQueries() map[string]string {
	return map[string]string{
		"classes":   `(class_definition name: (identifier) @name) @symbol`,
		"functions": `(function_definition name: (identifier) @name) @symbol`,
		"imports":   `[(import_statement) (import_from_statement)] @symbol`,
		"variables": `(assignment left: (identifier) @name) @symbol`,
	}
}

func (Python) DecisionKinds() []string {
	return []string{
		"if_statement",
		"for_statement",
		"while_statement",
		"conditional_expression",
		"boolean_operator", // and / or
		"except_clause",
		"case_clause",
	}
}

func (Python) AuxQueries() map[string]string {
	return map[string]string{
		"identifiers": `[(identifier) (attribute)] @id`,
		"calls":       `(call function: [(identifier) (attribute)] @callee)`,
	}
}

var _ lang.Language = Python{}
