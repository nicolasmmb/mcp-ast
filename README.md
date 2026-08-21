# ast-mcp

Servidor [MCP](https://modelcontextprotocol.io) em Go para análise de AST de múltiplas linguagens usando [tree-sitter](https://github.com/tree-sitter/go-tree-sitter) e o [Go SDK oficial](https://github.com/modelcontextprotocol/go-sdk).

Analisa arquivos e diretórios e expõe 14 tools por stdio:

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
| `search_name_dir` | Busca um nome em arquivos de um diretório |
| `complexity_file` | Complexidade ciclomática por função |
| `unused_symbols_dir` | Símbolos declarados mas nunca referenciados |
| `rename_preview_dir` | Ocorrências de um nome (renomeação) |
| `call_graph_file` | Quem chama quem em um arquivo |
| `callers_dir` | Quem chama um alvo no diretório inteiro |

Toda tool devolve `elapsed_ms` (tempo de processamento da consulta em milissegundos) junto com o resultado.

**Referência rápida — o que cada tool retorna:**

| Tool | Campos do output |
|---|---|
| `list_languages` | `languages: string[]` |
| `parse_ast_file` | `language`, `path`, `has_error: bool`, `ast: Node` |
| `query_ast_file` | `language`, `matches: [{captures: [{name, text, start, end}]}]` |
| `symbols_file` | `language`, `symbols: {kind: [{name, text, start, end}]}` |
| `scan_symbols_dir` | `language`, `files: {path: {kind: [...]}}`, `errors?` |
| `scan_variables_dir` | `language`, `variables: {path: [{name, text, start, end}]}`, `errors?` |
| `analyze_file` | `language`, `metrics: {lines, bytes, nodes, max_nesting, kinds}` |
| `get_text_file` | `language`, `path`, `text: string` |
| `search_name_dir` | `total`, `matches: [{file, kind, name, line, col, text}]`, `errors?` |
| `complexity_file` | `language`, `entries: [{name, kind, complexity, start, end}]` |
| `unused_symbols_dir` | `language`, `symbols: [{file, kind, name, line, col, text}]`, `errors?` |
| `rename_preview_dir` | `language`, `matches: [{file, line, col, text, definition: bool}]`, `errors?` |
| `call_graph_file` | `language`, `functions: [{name, kind, callees: [{name, count}], start, end}]` |
| `callers_dir` | `language`, `callers: [{file, name, kind, line, col, count}]`, `errors?` |

`Node` = `{type, field?, named, start: {row, col}, end: {row, col}, children?}`. Posições são 0-based.

## Estrutura

```
mcp-ast/
├── cmd/ast-mcp/main.go        # entrypoint: registra linguagens + cria o servidor MCP
├── internal/
│   ├── lang/lang.go           # interface Language + registry com pool de parsers
│   ├── engine/engine.go       # parse, AST→JSON, queries, símbolos, métricas (genérico)
│   ├── tools/tools.go         # handlers das 14 tools MCP
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
    DecisionKinds() []string            // kinds que somam 1 na complexidade ciclomática
    AuxQueries() map[string]string      // queries auxiliares (identifiers, calls)
}
```

`AuxQueries` alimenta as tools avançadas:
- `"identifiers"` — captura todo identificador como `@id` (usa o `rename_preview_dir`)
- `"calls"` — captura o callee de cada call como `@callee` (usa `call_graph_file`/`callers_dir`)

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

Ou baixe o binário pronto de uma [release](https://github.com/nicolasmmb/mcp-ast/releases):
`ast-mcp-linux-amd64`, `ast-mcp-linux-arm64`, `ast-mcp-darwin-amd64`, `ast-mcp-darwin-arm64`, `ast-mcp-windows-amd64.exe`.

Releases são geradas automaticamente: a cada push em `main` um workflow calcula a próxima versão (`feat:` → minor, senão patch), cria a tag `vX.Y.Z` e publica os binários (com checksum `.sha256`).

## Como usar o MCP

O servidor fala **MCP por stdio**: lê mensagens JSON-RPC da entrada padrão e responde na saída padrão. Qualquer cliente MCP (agente, editor, CLI) que o execute como processo local ganha as 14 tools automaticamente.

### 1. Obtenha o binário

Instalação automática (detecta OS/arquitetura, baixa a `latest` e adiciona ao `PATH`):

```bash
curl -fsSL https://raw.githubusercontent.com/nicolasmmb/mcp-ast/main/install.sh | bash
```

Ou manualmente: baixe de uma [release](https://github.com/nicolasmmb/mcp-ast/releases) o binário da sua plataforma, torne-o executável e coloque-o no `PATH`:

```bash
# exemplo (macOS arm64)
curl -sSL -o /usr/local/bin/ast-mcp https://github.com/nicolasmmb/mcp-ast/releases/latest/download/ast-mcp-darwin-arm64
chmod +x /usr/local/bin/ast-mcp
```

> Cada binário tem um `.sha256` na release para verificar a integridade:
> `shasum -a 256 -c ast-mcp-darwin-arm64.sha256` (macOS/Linux) ou
> `Get-FileHash ast-mcp-windows-amd64.exe` (Windows).

Ou compile localmente (Go 1.26+):

```bash
go build -o ast-mcp ./cmd/ast-mcp
```

### 2. Configure o cliente

**opencode** (`opencode.json` na raiz do projeto):

```json
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "ast-mcp": {
      "type": "local",
      "command": ["/usr/local/bin/ast-mcp"]
    }
  }
}
```

**Claude Code** (config do projeto ou global):

```json
{ "mcpServers": { "ast-mcp": { "command": "/usr/local/bin/ast-mcp" } } }
```

**Claude Desktop** (`claude_desktop_config.json`):

```json
{ "mcpServers": { "ast-mcp": { "command": "/usr/local/bin/ast-mcp" } } }
```

**Outros clientes** (VS Code, Cursor, JetBrains, etc.) seguem o mesmo padrão `mcpServers` — basta apontar `command` para o caminho do binário.

Reinicie o cliente após configurar.

### 3. Verifique a conexão

Sem cliente, teste o servidor por stdio (as três mensagens são o handshake `initialize` + `notifications/initialized` + `tools/list`; o `sleep` dá tempo da resposta antes do EOF):

```bash
( printf '%s\n' \
    '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}' \
    '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
    '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'; sleep 1 ) \
  | /usr/local/bin/ast-mcp
```

A resposta ao `initialize` traz `serverInfo` (name `ast-mcp`, version = a tag da release, ex.: `v0.1.3`; localmente `dev`), e o `tools/list` retorna as 14 tools.

### 4. Chame as tools

Conectado, as 14 tools aparecem como ferramentas nativas do agente — peça em linguagem natural ("qual a complexidade de `src/foo.go`?") ou invoque direto. Por baixo é o método MCP `tools/call`:

```json
{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"complexity_file","arguments":{"path":"src/foo.go"}}}
```

Cada tool — argumentos, exemplos de request/response e convenções de posição — está documentada na seção [# Tools](#tools) abaixo.

### Diagnóstico (logs e debug)

Flags de inicialização para ver o que o servidor está fazendo:

| Flag | Efeito |
|---|---|
| `-verbose` | Loga em **stderr** no nível **debug** (cada chamada de tool com nome + `elapsed_ms`) |
| `-log <caminho>` | Grava o log em **arquivo** (append), nível Info+ — captura erros mesmo sem `-verbose` |

```bash
ast-mcp -verbose                 # debug no stderr
ast-mcp -log /tmp/ast-mcp.log    # Info+ (erros/avisos) no arquivo
ast-mcp -verbose -log /tmp/ast-mcp.log   # ambos
```

O log registra o startup (versão, timeout, linguagens) e cada tool call — sucesso como `INFO`, falha como `ERROR` com a mensagem. Logs vão para stderr/arquivo, **nunca** para stdout (reservado ao transporte MCP).

## Commits, tags e versões

### Como commitar e publicar

Tudo é dirigido por push em `main` — você não cria tag nem release na mão:

```bash
git add <arquivos>
git commit -m "refat: descreva a mudança"
git push origin main
```

O push dispara o workflow `release` (`.github/workflows/release.yml`), que executa na ordem:

1. **`tag`** — calcula a próxima versão e cria/push a tag `vX.Y.Z`.
2. **`build`** — gate `go vet` + `go test`, depois builda 5 binários em runners nativos: `linux/amd64`, `linux/arm64`, `darwin/amd64` (cross no runner arm64), `darwin/arm64`, `windows/amd64` (+ `.sha256` de cada).
3. **`release`** — publica a [GitHub Release](https://github.com/nicolasmmb/mcp-ast/releases) com todos os binários e checksums.

### Como a versão é calculada

- Parte da tag mais recente (`git describe --tags --abbrev=0`); se não houver nenhuma, começa em `v0.1.0`.
- **Patch** (`v0.1.0 → v0.1.1`): mudanças que não começam com `feat` (ex.: `fix:`, `refat:`, `docs:`, `chore:`, `ci:`).
- **Minor** (`v0.1.x → v0.2.0`): quando algum commit desde a última tag começa com `feat`, `feat(scope):`, `feat!` ou contém `BREAKING`.
- Não há **major** automático.

### Convenção de mensagens de commit (opcional)

Só o prefixo do primeiro commit da faixa importa para o bump, mas seguir a convenção mantém o histórico limpo:

```
feat: adiciona nova tool X          # → minor
feat(scope): muda Y                 # → minor
fix: corrige bug                    # → patch
refat: simplifica Z                 # → patch
docs: atualiza README               # → patch
ci: ajusta workflow                 # → patch
```

Binários reportam a versão do release via `-X main.version=vX.Y.Z` (campo `version` do servidor; localmente sem tag é `dev`).

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

## `search_name_dir`

Busca um símbolo **por nome** em todos os arquivos de um diretório, usando o AST (as symbol queries do tree-sitter por linguagem). Retorna **apenas declarações** — classes, funções, variáveis, etc. — com o `kind`, posição e texto, ignorando usos, comentários e strings.

**Argumentos:**

| Campo | Tipo | Obrigatório | Descrição |
|---|---|---|---|
| `name` | string | sim | Nome do símbolo a buscar (igualdade exata) |
| `path` | string | sim | Diretório para buscar recursivamente |
| `language` | string | não | Filtrar por linguagem (ex. `go`, `java`). Omitir para buscar em todos os arquivos reconhecidos |
| `limit` | int | não | Máximo de matches (0 = sem limite) |

**Request:**

```json
{"name": "search_name_dir", "arguments": {"name": "Engine", "path": "."}}
```

**Response:**

```json
{
  "elapsed_ms": 37.2,
  "total": 1,
  "matches": [
    {
      "file": "internal/engine/engine.go",
      "kind": "types",
      "name": "Engine",
      "line": 48,
      "col": 5,
      "text": "Engine struct {"
    }
  ]
}
```

Cada match tem `file` (caminho), `kind` (tipo de símbolo: `classes`, `functions`, `types`, `variables`...), `name`, `line`/`col` (posição 0-based) e `text` (resumo de 1 linha do símbolo). Por ser AST-aware, só acha **definições** — usar a busca para achar onde o nome aparece como referência não funciona; para isso use `query_ast_file` ou a busca textual.

---

## `complexity_file`

Complexidade ciclomática (`1 + pontos de decisão`) de cada função e método de um arquivo. Pontos de decisão: `if`, loops (`for`/`while`/`do`), `switch`/`case`, ternário, `catch`, e operadores lógicos `&&`/`||` — cada um soma 1.

**Argumentos:**

| Campo | Tipo | Obrigatório | Descrição |
|---|---|---|---|
| `path` | string | sim | Arquivo a analisar |
| `language` | string | não | Omitir para autodetect |

**Request:**

```json
{"name": "complexity_file", "arguments": {"path": "internal/engine/engine.go"}}
```

**Response:**

```json
{
  "elapsed_ms": 3.1,
  "language": "go",
  "entries": [
    {"name": "hasExt", "kind": "functions", "complexity": 3, "start": {"row": 182, "col": 0}, "end": {"row": 189, "col": 1}},
    {"name": "complexityOf", "kind": "functions", "complexity": 5, "start": {"row": 268, "col": 0}, "end": {"row": 291, "col": 1}}
  ]
}
```

Regra da complexidade ciclomática: cada `if`/`for`/`while`/`switch`/`case`/ternário/`catch` soma 1, e cada `&&`/`||` soma 1 (na mesma linha, `a && b` = 2 pontos). O `binary_expression` só conta quando o operador é lógico — aritmética não soma. `complexity > 10` é um bom candidato a refactor.

---

## `unused_symbols_dir`

Símbolos **declarados mas nunca referenciados** (dead code) em um diretório. Heurística: um símbolo cujo nome aparece **exatamente uma vez** em todos os arquivos reconhecidos é considerado não usado — a única ocorrência é a própria declaração.

**Argumentos:**

| Campo | Tipo | Obrigatório | Descrição |
|---|---|---|---|
| `path` | string | sim | Diretório para buscar recursivamente |
| `language` | string | não | Filtrar por linguagem. Omitir para auto |
| `limit` | int | não | Máximo de resultados (0 = sem limite) |

**Request:**

```json
{"name": "unused_symbols_dir", "arguments": {"path": "./src"}}
```

**Response:**

```json
{
  "elapsed_ms": 13.5,
  "language": "auto",
  "symbols": [
    {"file": "internal/engine/engine.go", "kind": "methods", "name": "ListLanguages", "line": 59, "col": 0, "text": "func (e *Engine) ListLanguages() []string { ... }"}
  ]
}
```

**Limitações da heurística** (`ponytail:` trade-off): contagem textual de ocorrências, não análise de referências semântica. Comentários e strings contam como uso (nunca marca falso-unused, mas pode **deixar passar** um símbolo de fato não usado que aparece num comentário). O resultado é por diretório: um símbolo usado **fora** do diretório varrido aparece como unused.

---

## `rename_preview_dir`

Acha **todas as ocorrências** de um nome de símbolo em um diretório (via query de identificadores da linguagem), marcando quais são **definições**. Use antes de renomear para ver todos os pontos a editar.

**Argumentos:**

| Campo | Tipo | Obrigatório | Descrição |
|---|---|---|---|
| `name` | string | sim | Nome do símbolo a renomear |
| `path` | string | sim | Diretório para buscar recursivamente |
| `language` | string | não | Filtrar por linguagem. Omitir para auto |
| `limit` | int | não | Máximo de matches (0 = sem limite) |

**Request:**

```json
{"name": "rename_preview_dir", "arguments": {"name": "Analyze", "path": "internal/engine"}}
```

**Response:**

```json
{
  "elapsed_ms": 2.1,
  "language": "auto",
  "matches": [
    {"file": "internal/engine/engine.go", "line": 335, "col": 13, "text": "func (e *Engine) Analyze(l lang.Language, ...)", "definition": true},
    {"file": "internal/engine/engine_test.go", "line": 50, "col": 3, "text": "eng.Analyze(...)", "definition": false}
  ]
}
```

`definition: true` marca o ponto de declaração (cruzado com as symbol queries da linguagem); os demais são usos. `text` é a linha do pai do identificador (contexto do match). Renomear envolve editar todos os matches com `definition` e todos os usos que você quiser afetar.

---

## `call_graph_file`

Mapeia cada função e método de um arquivo para os **callees** que ela invoca, com contagem de chamadas. Um call pertence à função cujo range o contém.

**Argumentos:**

| Campo | Tipo | Obrigatório | Descrição |
|---|---|---|---|
| `path` | string | sim | Arquivo a analisar |
| `language` | string | não | Omitir para autodetect |

**Request:**

```json
{"name": "call_graph_file", "arguments": {"path": "internal/engine/engine.go"}}
```

**Response:**

```json
{
  "elapsed_ms": 1.8,
  "language": "go",
  "functions": [
    {"name": "complexityOf", "kind": "functions", "callees": [
      {"name": "hasLogicalOp", "count": 1}, {"name": "walk", "count": 2}
    ], "start": {"row": 268, "col": 0}, "end": {"row": 291, "col": 1}}
  ]
}
```

Cada entrada tem `name`, `kind` (functions/methods/constructors), `callees` (nome + `count` de invocações) e o range do símbolo. Útil para entender dependências internas de um arquivo e identificar funções-mãe com muitas chamadas.

---

## `callers_dir`

O reverso do `call_graph_file`: dado um **nome alvo**, acha todas as funções/métodos que o chamam em um diretório, agregando os call sites por função chamadora.

**Argumentos:**

| Campo | Tipo | Obrigatório | Descrição |
|---|---|---|---|
| `name` | string | sim | Nome da função/método alvo |
| `path` | string | sim | Diretório para buscar recursivamente |
| `language` | string | não | Filtrar por linguagem. Omitir para auto |
| `limit` | int | não | Máximo de resultados (0 = sem limite) |

**Request:**

```json
{"name": "callers_dir", "arguments": {"name": "countNodes", "path": "internal/engine"}}
```

**Response:**

```json
{
  "elapsed_ms": 12.3,
  "language": "auto",
  "callers": [
    {"file": "internal/engine/engine.go", "name": "Analyze", "kind": "methods", "line": 335, "col": 13, "count": 1}
  ]
}
```

Cada entrada é uma função chamadora com `file`, `name`, `kind`, posição do símbolo e `count` (quantas vezes chama o alvo). Mesma cobertura do `call_graph_file`: chamadas de método via `selector` (`obj.foo()`) são capturadas pelo nome do campo; o `count` agrega múltiplos call sites da mesma função.

---

# Notas de design

- **`text` truncado por padrão**: para manter os outputs pequenos, `text` vem como a primeira linha do nó (até 200 chars). O corpo completo é obtido com `include_text: true` (`symbols_file`/`query_ast_file`) ou via `get_text_file`. Isso evita que listar símbolos de um arquivo grande exploda o contexto.
- **`matches`/`captures` sempre `[]`**, nunca `null` — garante compatibilidade com a validação de schema de output do SDK.
- **Erros** são retornados como erro da tool MCP (`isError: true`) com mensagem clara — ex. query inválida, arquivo inexistente, linguagem desconhecida, `end` antes de `start`.

# Testes

```bash
go test ./...   # parse + símbolos por linguagem; engine (scan, analyze, query, get_text, include_text, search, complexity, unused, rename, callgraph, callers)
go vet ./...    # gofmt limpo
```
