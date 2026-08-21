package yaml

import (
	ts "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_yaml "github.com/tree-sitter-grammars/tree-sitter-yaml/bindings/go"

	"mcp-ast/internal/lang"
)

type YAML struct{}

func (YAML) Name() string { return "yaml" }

func (YAML) Extensions() []string { return []string{".yaml", ".yml"} }

func (YAML) Language() *ts.Language {
	return ts.NewLanguage(tree_sitter_yaml.Language())
}

func (YAML) SymbolQueries() map[string]string {
	return map[string]string{
		"mappings": `(block_mapping_pair key: (flow_node (plain_scalar (string_scalar)) @name)) @symbol`,
		"sequences": `(block_sequence_entry) @symbol`,
	}
}

func (YAML) DecisionKinds() []string {
	return []string{
		"block_mapping_pair",
		"block_sequence_entry",
		"flow_mapping",
		"flow_sequence",
	}
}

func (YAML) AuxQueries() map[string]string {
	return map[string]string{
		"identifiers": `[(plain_scalar (string_scalar)) (double_quote_scalar) (single_quote_scalar)] @id`,
		"calls":       `(block_mapping_pair key: (flow_node (plain_scalar (string_scalar)) @callee))`,
	}
}

var _ lang.Language = YAML{}
