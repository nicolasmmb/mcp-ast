# ast-mcp

Servidor [MCP](https://modelcontextprotocol.io) em Go para análise de AST de múltiplas linguagens usando [tree-sitter](https://github.com/tree-sitter/go-tree-sitter) e o [Go SDK oficial](https://github.com/modelcontextprotocol/go-sdk).

Analisa arquivos e diretórios e expõe 7 tools por stdio:

| Tool | Função |
|---|---|
| `list_languages` | Linguagens registradas |
| `parse_ast_file` | Árvore sintática completa de um arquivo em JSON |
| `query_ast_file` | Queries tree-sitter customizadas em um arquivo |
| `symbols_file` | Símbolos de um arquivo por tipo (classes, métodos, imports...) |
| `analyze_file` | Métricas de um arquivo |
| `get_text_file` | Código exato de um range de posições de um arquivo |
| `scan_symbols_dir` | Símbolos de um diretório inteiro |
| `scan_variables_dir` | Variáveis de um diretório inteiro |

Toda tool devolve `elapsed_ms` (tempo de processamento da consulta em milissegundos) junto com o resultado.

## Estrutura

```
mcp-java-ast/
├── cmd/ast-mcp/main.go        # entrypoint: registra linguagens + cria o servidor MCP
├── internal/
│   ├── lang/lang.go           # interface Language + registry com pool de parsers
│   ├── engine/engine.go       # parse, AST→JSON, queries, símbolos, métricas (genérico)
│   ├── tools/tools.go         # handlers das 7 tools MCP
│   ├── tools/timing.go        # wrapper que injeta elapsed_ms em toda tool
│   └── languages/
│       ├── java/java.go       # gramática Java + queries de símbolos
│       ├── python/python.go   # gramática Python + queries de símbolos
│       └── go/go.go           # gramática Go + queries de símbolos
```

## Como funciona a modularidade

Toda linguagem implementa a interface `lang.Language` (`internal/lang/lang.go:12`):

```go
type Language interface {
    Name() string                       // "java"
    Extensions() []string               // [".java"]
    Language() *ts.Language             // gramática tree-sitter
    SymbolQueries() map[string]string   // queries nomeadas por tipo de símbolo
}
```

Adicionar uma linguagem = 1 arquivo novo (ex. `internal/languages/python/python.go`) + 1 linha no slice do `main.go`. Engine e tools são 100% genéricos — não conhecem nenhuma gramática.

```go
// cmd/ast-mcp/main.go
for _, l := range []lang.Language{java.Java{}, python.Python{}, golanglang.Go{}} {
    if err := reg.Register(l); err != nil { log.Fatal(err) }
}
```

Exemplo de queries de símbolos do Java (`internal/languages/java/java.go:33`):

```go
"classes": `(class_declaration name: (identifier) @name) @symbol`,
"methods": `(method_declaration name: (identifier) @name) @symbol`,
"imports": `(import_declaration) @symbol`,
```

- `@name` captura o nome do símbolo
- `@symbol` captura o nó inteiro (posições + texto)

### Parser pool

Parsers tree-sitter não são thread-safe e handlers MCP são concorrentes, então o registry mantém um `sync.Pool` por linguagem (`lang.Acquire`). Todo `Parser`/`Tree`/`Query`/`QueryCursor` é liberado com `Close()` (memória CGO).

### Autodetect de linguagem

Nas tools que aceitam `language` opcional, a linguagem é inferida pela extensão do arquivo (`.java`, `.py`, `.go`).

### Timing

O wrapper `timed()` (`internal/tools/timing.go`) envolve todos os handlers, mede a duração e injeta `elapsed_ms` no output. Não há como uma tool retornar sem esse campo.

## Build e uso

```bash
go build -o ast-mcp ./cmd/ast-mcp
```

Servidor stdio — funciona com qualquer cliente MCP. Exemplo de config opencode (`opencode.json`):

```json
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "ast-mcp": {
      "type": "local",
      "command": ["/caminho/absoluto/ast-mcp"]
    }
  }
}
```

Claude Code / Desktop:

```json
{ "mcpServers": { "ast-mcp": { "command": "/caminho/absoluto/ast-mcp" } } }
```

Reinicie o cliente após configurar.

---

# Tools

Convenções comuns:
- **Posições** são 0-based (linha e coluna em bytes), como o tree-sitter reporta.
- **`elapsed_ms`** está presente em toda resposta.
- Todas aceitam `language` opcional exceto quando indicado; se omitido, é detectado pela extensão do arquivo.

---

## `list_languages`

Lista as linguagens registradas no servidor. Não recebe argumentos.

**Request:**

```json
{"name": "list_languages", "arguments": {}}
```

**Response:**

```json
{
  "elapsed_ms": 0.045,
  "languages": ["go", "java", "python"]
}
```

---

## `parse_ast_file`

Parseia um arquivo e devolve a árvore sintática completa (AST) em JSON.

**Argumentos:**

| Campo | Tipo | Obrigatório | Descrição |
|---|---|---|---|
| `path` | string | sim | Caminho do arquivo a parsear |
| `language` | string | não | Nome da linguagem; omitir para autodetect por extensão |
| `max_depth` | int | não | Profundidade máxima da árvore; `0` = padrão 20 |

**Request:**

```json
{
  "name": "parse_ast_file",
  "arguments": {"path": "Greeter.java", "max_depth": 5}
}
```

**Response:**

```json
{
  "elapsed_ms": 0.7,
  "language": "java",
  "path": "Greeter.java",
  "has_error": false,
  "ast": {
    "type": "program",
    "field": "",
    "named": true,
    "start": {"row": 0, "col": 0},
    "end": {"row": 10, "col": 0},
    "children": [
      {
        "type": "package_declaration",
        "field": "",
        "named": true,
        "start": {"row": 0, "col": 0},
        "end": {"row": 0, "col": 13},
        "children": []
      },
      { "type": "class_declaration", "field": "", "named": true,
        "start": {"row": 4, "col": 0}, "end": {"row": 9, "col": 1},
        "children": [ /* ... */ ] }
    ]
  }
}
```

Cada nó tem `type` (tipo da gramática), `field` (nome do campo filho, ex. `name`), `named` (se é nó nomeado), `start`/`end` (posição), e `children`. `has_error` indica se a gramática encontrou nós `ERROR`.

---

## `query_ast_file`

Roda uma [query tree-sitter](https://tree-sitter.github.io/tree-sitter/using-parsers#pattern-matching-with-queries) sobre um arquivo e devolve os matches com captures, texto e posições.

**Argumentos:**

| Campo | Tipo | Obrigatório | Descrição |
|---|---|---|---|
| `path` | string | sim | Arquivo a consultar |
| `query` | string | sim | Query tree-sitter |
| `language` | string | não | Omitir para autodetect por extensão |
| `limit` | int | não | Máximo de matches; `0` = ilimitado |
| `include_text` | bool | não | Incluir texto completo dos nós em vez do resumo de 1 linha |

**Request — nomes de métodos:**

```json
{
  "name": "query_ast_file",
  "arguments": {
    "language": "java",
    "path": "Greeter.java",
    "query": "(method_declaration name: (identifier) @name) @method"
  }
}
```

**Response:**

```json
{
  "elapsed_ms": 1.2,
  "language": "java",
  "matches": [
    {
      "captures": [
        { "name": "method", "text": "public String hello(String who) {",
          "start": {"row": 7, "col": 4}, "end": {"row": 9, "col": 5} },
        { "name": "name", "text": "hello",
          "start": {"row": 7, "col": 18}, "end": {"row": 7, "col": 23} }
      ]
    }
  ]
}
```

Cada match tem uma lista de `captures` (uma por `@nome` na query), com `name` (o rótulo), `text`, e posições. Por padrão `text` é a **primeira linha** do nó (resumo, evita outputs gigantes). Com `include_text: true` o corpo completo vem embutido — uma chamada só, sem precisar de `get_text_file` depois.

**Query inválida** (node type que não existe na gramática):

```json
{
  "elapsed_ms": 0.8,
  "language": "go",
  "error": "invalid query: decorated_definition"
}
```

(erros viram `isError: true` na resposta MCP.)

---

## `symbols_file`

Extrai símbolos de um arquivo agrupados por tipo, usando as queries embutidas da linguagem.

**Argumentos:**

| Campo | Tipo | Obrigatório | Descrição |
|---|---|---|---|
| `path` | string | sim | Arquivo a analisar |
| `language` | string | não | Omitir para autodetect |
| `include_text` | bool | não | Incluir texto completo dos símbolos em vez do resumo |

Tipos por linguagem:

- **Java**: `classes`, `interfaces`, `enums`, `records`, `methods`, `constructors`, `fields`, `variables`, `imports`
- **Python**: `classes`, `functions`, `variables`, `imports`
- **Go**: `types`, `functions`, `methods`, `variables`, `imports`

**Request:**

```json
{"name": "symbols_file", "arguments": {"path": "Greeter.java"}}
```

**Response:**

```json
{
  "elapsed_ms": 8.9,
  "language": "java",
  "symbols": {
    "classes": [
      { "name": "Greeter",
        "text": "public class Greeter {",
        "start": {"row": 4, "col": 0}, "end": {"row": 9, "col": 1} }
    ],
    "methods": [
      { "name": "hello",
        "text": "public String hello(String who) {",
        "start": {"row": 7, "col": 4}, "end": {"row": 9, "col": 5} }
    ],
    "imports": [
      { "name": "import java.util.List;",
        "text": "import java.util.List;",
        "start": {"row": 2, "col": 0}, "end": {"row": 2, "col": 22} }
    ]
  }
}
```

Cada símbolo tem `name`, `text` (resumo de 1 linha; corpo completo com `include_text: true`), e posições usáveis no `get_text_file`.

---

## `scan_symbols_dir`

Varre um diretório recursivamente e devolve os símbolos de todos os arquivos reconhecidos, agrupados por caminho. Ignora pastas ocultas. Erros de arquivos individuais vão em `errors` em vez de abortar a varredura.

**Argumentos:**

| Campo | Tipo | Obrigatório | Descrição |
|---|---|---|---|
| `path` | string | sim | Diretório a varrer |
| `language` | string | não | Restringe a uma linguagem; omitir = autodetect por arquivo |

**Request:**

```json
{"name": "scan_symbols_dir", "arguments": {"path": "./internal", "language": "go"}}
```

**Response:**

```json
{
  "elapsed_ms": 27.7,
  "language": "go",
  "files": {
    "internal/engine/engine.go": {
      "functions": [
        { "name": "New", "text": "func New(reg *lang.Registry) *Engine { return &Engine{reg: reg} }",
          "start": {"row": 51, "col": 0}, "end": {"row": 51, "col": 65} }
      ],
      "methods": [
        { "name": "Parse", "text": "func (e *Engine) Parse(l lang.Language, path string, maxDepth int) (*Node, bool, error) {",
          "start": {"row": 70, "col": 0}, "end": {"row": 80, "col": 1} }
      ]
    },
    "internal/lang/lang.go": {
      "functions": [
        { "name": "NewRegistry", "text": "func NewRegistry() *Registry {",
          "start": {"row": 32, "col": 0}, "end": {"row": 34, "col": 1} }
      ]
    }
  }
}
```

`language` na resposta é `"auto"` quando detectado por arquivo, ou o nome informado. O exemplo acima resolveu o uso típico "pega as funções do repo inteiro" numa única chamada.

---

## `scan_variables_dir`

Varre um diretório recursivamente e devolve **apenas as variáveis** de todos os arquivos reconhecidos, agrupadas por caminho. Ignora pastas ocultas. Erros de arquivos individuais vão em `errors` em vez de abortar a varredura.

O que é considerado variável por linguagem:

- **Java**: variáveis locais (corpo de métodos, loops) — campos de classe ficam no kind `fields` do `symbols_file`
- **Python**: atribuições (`x = ...`)
- **Go**: `:=` (short var), `var` declarations e campos de struct

**Argumentos:**

| Campo | Tipo | Obrigatório | Descrição |
|---|---|---|---|
| `path` | string | sim | Diretório a varrer |
| `language` | string | não | Restringe a uma linguagem; omitir = autodetect por arquivo |

**Request:**

```json
{"name": "scan_variables_dir", "arguments": {"path": "./src"}}
```

**Response:**

```json
{
  "elapsed_ms": 12.5,
  "language": "auto",
  "variables": {
    "src/Sample.java": [
      { "name": "total", "text": "int total = 0;",
        "start": {"row": 3, "col": 8}, "end": {"row": 3, "col": 22} },
      { "name": "i", "text": "int i = 0;",
        "start": {"row": 5, "col": 13}, "end": {"row": 5, "col": 23} }
    ],
    "src/demo.py": [
      { "name": "count", "text": "count = 0",
        "start": {"row": 2, "col": 0}, "end": {"row": 2, "col": 9} },
      { "name": "name", "text": "name = who",
        "start": {"row": 6, "col": 8}, "end": {"row": 6, "col": 18} }
    ]
  }
}
```

Cada variável tem `name`, `text` (linha da declaração) e posições usáveis no `get_text_file`. Arquivos sem variáveis são omitidos do map.

---

## `analyze_file`

Métricas de um arquivo: tamanho, contagem de nós, profundidade de aninhamento, e estatísticas de linhas por tipo de símbolo.

**Argumentos:**

| Campo | Tipo | Obrigatório | Descrição |
|---|---|---|---|
| `path` | string | sim | Arquivo a analisar |
| `language` | string | não | Omitir para autodetect |

**Request:**

```json
{"name": "analyze_file", "arguments": {"path": "internal/engine/engine.go"}}
```

**Response:**

```json
{
  "elapsed_ms": 5.5,
  "language": "go",
  "metrics": {
    "lines": 311,
    "bytes": 7755,
    "nodes": 3063,
    "max_nesting": 24,
    "kinds": {
      "functions": { "count": 6, "avg_lines": 8.67, "max_lines": 18 },
      "methods":   { "count": 9, "avg_lines": 18.33, "max_lines": 39 },
      "imports":   { "count": 1, "avg_lines": 12, "max_lines": 12 },
      "types":     { "count": 8, "avg_lines": 5.25, "max_lines": 8 }
    }
  }
}
```

`lines` conta `\n` + 1 (arquivo `a.go\nb\n` = 3 linhas). `max_nesting` é a profundidade máxima da árvore — proxy útil de complexidade.

---

## `get_text_file`

Devolve o texto exato de um range de posições (0-based), como as que qualquer nó/capture/símbolo retorna. É um slice puro do arquivo — não re-parseia, por isso é rápido. Complementa o `symbols_file`/`query_ast_file` quando se quer o código completo de um trecho sem `include_text`.

**Argumentos:**

| Campo | Tipo | Obrigatório | Descrição |
|---|---|---|---|
| `path` | string | sim | Arquivo |
| `start_row` | int | sim | Linha inicial (inclusiva) |
| `start_col` | int | sim | Coluna inicial (inclusiva) |
| `end_row` | int | sim | Linha final (exclusiva) |
| `end_col` | int | sim | Coluna final (exclusiva) |
| `language` | string | não | Omitir para autodetect |

**Request** (range do método `Analyze` pego de um `symbols_file` anterior):

```json
{
  "name": "get_text_file",
  "arguments": {
    "path": "internal/engine/engine.go",
    "start_row": 196, "start_col": 0,
    "end_row": 232, "end_col": 1
  }
}
```

**Response:**

```json
{
  "elapsed_ms": 0.26,
  "language": "go",
  "path": "internal/engine/engine.go",
  "text": "func (e *Engine) Analyze(l lang.Language, path string) (*Metrics, error) {\n\tsrc, tree, err := e.parseFile(l, path)\n\tif err != nil {\n\t\treturn nil, err\n\t}\n\t...\n\treturn m, nil\n}\n"
}
```

---

# Notas de design

- **`text` truncado por padrão**: para manter os outputs pequenos, `text` vem como a primeira linha do nó (até 200 chars). O corpo completo é obtido com `include_text: true` (`symbols_file`/`query_ast_file`) ou via `get_text_file`. Isso evita que listar símbolos de um arquivo grande exploda o contexto.
- **`matches`/`captures` sempre `[]`**, nunca `null` — garante compatibilidade com a validação de schema de output do SDK.
- **Erros** são retornados como erro da tool MCP (`isError: true`) com mensagem clara — ex. query inválida, arquivo inexistente, linguagem desconhecida, `end` antes de `start`.

# Testes

```bash
go test ./...   # parse + símbolos por linguagem; engine (scan, analyze, query, get_text, include_text)
go vet ./...    # gofmt limpo
```
