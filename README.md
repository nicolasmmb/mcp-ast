# ast-mcp

Servidor [MCP](https://modelcontextprotocol.io) em Go para análise de AST de múltiplas linguagens usando [tree-sitter](https://github.com/tree-sitter/go-tree-sitter) e o [Go SDK oficial](https://github.com/modelcontextprotocol/go-sdk).

Analisa arquivos e diretórios e expõe **9 tools** por stdio
(**breaking change** — nomes legados removidos, sem aliases):

| Tool | Escopo | Função |
|---|---|---|
| `list_languages` | meta | Linguagens registradas |
| `parse_ast` | arquivo | Árvore sintática completa em JSON |
| `query_ast` | arquivo | Queries tree-sitter customizadas |
| `scan_symbols` | arquivo ou diretório | Símbolos com filtros |
| `analyze_file` | arquivo | Métricas + complexidade + call graph |
| `get_text` | arquivo | Código por range de posições |
| `find_usages` | diretório | occurrences / callers / unused / definitions / imports |
| `rank_complexity` | diretório | Top-N complexidade ciclomática |
| `outline_file` | arquivo | Árvore hierárquica de símbolos |

### Migração (breaking)

| Antigo | Novo |
|---|---|
| `parse_ast_file` | `parse_ast` |
| `query_ast_file` | `query_ast` |
| `symbols_file` | `scan_symbols` (path = arquivo) |
| `scan_symbols_dir` | `scan_symbols` (path = diretório) |
| `get_text_file` | `get_text` |
| `usages_dir` | `find_usages` com `mode=occurrences` |
| `callers_dir` | `find_usages` com `mode=callers` |
| `unused_symbols_dir` | `find_usages` com `mode=unused` |
| — | `rank_complexity` (nova) |
| — | `outline_file` (nova) |

**Referência rápida — o que cada tool retorna:**

| Tool | Campos do output |
|---|---|
| `list_languages` | `languages: string[]` |
| `parse_ast` | `language`, `path`, `has_error`, `ast: Node` |
| `query_ast` | `language`, `matches: [{captures}]` |
| `scan_symbols` | `language`, `files: {path: {kind: [Symbol]}}`, `errors?` |
| `analyze_file` | `language`, `path`, `metrics`, `complexity[]`, `call_graph[]` |
| `get_text` | `language`, `path`, `text` |
| `find_usages` | `mode`, `matches`/`files`/`callers`/`symbols`, `errors?` |
| `rank_complexity` | `language`, `entries: [{file, name, complexity, start, end}]`, `errors?` |
| `outline_file` | `language`, `path`, `outline: [{name, kind, children}]` |

Toda tool devolve `elapsed_ms` (tempo de processamento da consulta em milissegundos) junto com o resultado.

`Node` = `{type, field?, named, start: {row, col}, end: {row, col}, children?}`. Posições são 0-based.

## Estrutura

```
mcp-ast/
├── cmd/ast-mcp/main.go        # entrypoint: registra linguagens + cria o servidor MCP
├── internal/
│   ├── lang/lang.go           # interface Language + registry com pool de parsers
│   ├── languages/             # gramáticas + queries por linguagem
│   ├── engine/                # operações tree-sitter puras (sem noção de MCP)
│   │   ├── engine.go          # parse, AST→JSON, queries, walkFiles paralelo
│   │   ├── symbols.go         # extração de símbolos + scan de diretório
│   │   ├── calls.go           # call graph + caller reverso
│   │   ├── complexity.go      # complexidade ciclomática
│   │   └── outline.go         # outline hierárquico
│   ├── service/               # orquestração de domínio
│   │   ├── service.go         # Services aggregator
│   │   ├── scan.go            # ScanService (file ou dir)
│   │   ├── file.go            # FileAnalysisService + Outline
│   │   ├── find.go            # FindService (modes)
│   │   └── rank.go            # RankService
│   └── tools/                 # camada MCP: registro das 9 tools + timing
│       ├── tools.go
│       └── timing.go
```

Fluxo de dependências: `tools → service → engine → lang`. Nada retorna.

## Como funciona a modularidade

Toda linguagem implementa a interface `lang.Language` (`internal/lang/lang.go`):

```go
type Language interface {
    Name() string                       // "java"
    Extensions() []string               // [".java"]
    Language() *ts.Language             // gramática tree-sitter
    SymbolQueries() map[string]string   // queries nomeadas por tipo de símbolo
    DecisionKinds() []string            // kinds que somam 1 na complexidade ciclomática
    AuxQueries() map[string]string      // queries auxiliares (calls, identifiers, ...)
}
```

Queries são pré-compiladas no `Register` (fail-fast). O registry mantém `Compiled` por linguagem.

## Como usar o MCP

O servidor fala **MCP por stdio**: lê mensagens JSON-RPC da entrada padrão e responde na saída padrão.
Qualquer cliente MCP (agente, editor, CLI) que o execute como processo local ganha as 9 tools automaticamente.

### 1. Obtenha o binário

Instalação automática (detecta OS/arquitetura, baixa a `latest` e adiciona ao `PATH`):

```bash
curl -fsSL https://raw.githubusercontent.com/nicolasmmb/mcp-ast/main/install.sh | bash
```

Ou compile localmente:

```bash
go build -o ast-mcp ./cmd/ast-mcp
```

### 2. Configure o cliente MCP

Exemplo (Claude Desktop / Cursor / similares):

```json
{
  "mcpServers": {
    "ast-mcp": {
      "command": "ast-mcp"
    }
  }
}
```

### 3. Exemplos de uso

**Listar linguagens**
```json
{"name": "list_languages", "arguments": {}}
```

**Outline de um arquivo**
```json
{"name": "outline_file", "arguments": {"path": "internal/engine/engine.go"}}
```

**Scan de símbolos em diretório**
```json
{"name": "scan_symbols", "arguments": {"path": "internal", "kinds": ["methods"], "limit": 50}}
```

**Find usages antes de renomear**
```json
{"name": "find_usages", "arguments": {"name": "walkFiles", "path": "internal", "mode": "occurrences"}}
```

**Hotspots de complexidade**
```json
{"name": "rank_complexity", "arguments": {"path": "internal", "limit": 10}}
```

## Desenvolvimento

```bash
go test ./...
```

Matriz de testes cobre as 9 linguagens (Go, Java, Python, JS, TS, Rust, C#, Bash, YAML) com fixtures em `internal/service/testdata/matrix/` e valida **toda** query de `SymbolQueries` e `AuxQueries` por linguagem.

## Releases

Releases são geradas automaticamente: a cada push em `main` um workflow calcula a próxima versão (`feat:` → minor, senão patch), cria a tag `vX.Y.Z` e publica os binários (com checksum `.sha256`).
