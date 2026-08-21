package python

import (
	ts "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_python "github.com/tree-sitter/tree-sitter-python/bindings/go"

	"mcp-java-ast/internal/lang"
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
	}
}

var _ lang.Language = Python{}
