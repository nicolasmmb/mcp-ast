# ast-mcp

Servidor [MCP](https://modelcontextprotocol.io) em Go para análise de AST de múltiplas linguagens usando [tree-sitter](https://github.com/tree-sitter/go-tree-sitter) e o [Go SDK oficial](https://github.com/modelcontextprotocol/go-sdk).

Analisa arquivos e diretórios e expõe 10 tools por stdio:

| Tool | Escopo | Função |
|---|---|---|
| `list_languages` | meta | Linguagens registradas |
| `parse_ast_file` | arquivo | Árvore sintática completa de um arquivo em JSON |
| `query_ast_file` | arquivo | Queries tree-sitter customizadas em um arquivo |
| `symbols_file` | arquivo | Símbolos de um arquivo por tipo (classes, métodos, imports...) |
| `analyze_file` | arquivo | Dossiê completo: métricas + complexidade ciclomática + call graph |
| `get_text_file` | arquivo | Código exato de um range de posições de um arquivo |
| `scan_symbols_dir` | diretório | Símbolos de um diretório, com filtros `languages[]`, `kinds[]`, `name` |
| `unused_symbols_dir` | diretório | Símbolos declarados mas nunca referenciados (dead code) |
| `usages_dir` | diretório | Toda ocorrência de um nome, classificada: definition / call-site / reference |
| `callers_dir` | diretório | Quem chama um alvo, agregado por função com contagem |

**Referência rápida — o que cada tool retorna:**

| Tool | Campos do output |
|---|---|
| `list_languages` | `languages: string[]` |
| `parse_ast_file` | `language`, `path`, `has_error: bool`, `ast: Node` |
| `query_ast_file` | `language`, `matches: [{captures: [{name, text, start, end}]}]` |
| `symbols_file` | `language`, `symbols: {kind: [{name, text, start, end}]}` |
| `analyze_file` | `language`, `metrics`, `complexity: [{name, kind, complexity, start, end}]`, `call_graph: [{name, callees}]` |
| `get_text_file` | `language`, `path`, `text: string` |
| `scan_symbols_dir` | `language`, `files: {path: {kind: [...]}}`, `errors?` |
| `unused_symbols_dir` | `language`, `symbols: [{file, kind, name, line, col, text}]`, `errors?` |
| `usages_dir` | `language`, `matches: [{file, line, col, text, kind, caller?}]`, `errors?` |
| `callers_dir` | `language`, `callers: [{file, name, kind, line, col, count}]`, `errors?` |

Toda tool devolve `elapsed_ms` (tempo de processamento da consulta em milissegundos) junto com o resultado.

`Node` = `{type, field?, named, start: {row, col}, end: {row, col}, children?}`. Posições são 0-based.

## Estrutura

```
mcp-ast/
├── cmd/ast-mcp/main.go        # entrypoint: registra linguagens + cria o servidor MCP
├── internal/
│   ├── lang/lang.go           # interface Language + registry com pool de parsers
│   ├── languages/             # gramáticas + queries por linguagem (go, java, python)
│   ├── engine/                # operações tree-sitter puras (sem noção de MCP)
│   │   ├── engine.go          # parse, AST→JSON, queries
│   │   ├── symbols.go         # extração de símbolos + scan de diretório
│   │   ├── calls.go           # call graph + caller reverso
│   │   ├── search.go          # detecção de unused symbols
│   │   ├── metrics.go         # métricas de arquivo + complexidade ciclomática
│   │   ├── usages.go          # ocorrências classificadas (definition/call-site/reference)
│   │   ├── dossier.go         # composição métricas+complexidade+call graph (parse único)
│   │   └── gettext.go         # texto por posição
│   ├── service/               # orquestração por domínio (transporte-agnóstico)
│   │   ├── service.go         # container Services + resolução de filtros de linguagem
│   │   ├── scan.go            # ScanService: varredura com poda genérica kinds/name/limit
│   │   ├── file.go            # FileAnalysisService: dossiê do arquivo + símbolos
│   │   ├── usages.go          # UsagesService
│   │   ├── unused.go          # UnusedService
│   │   └── calls.go           # CallsService: callers agregados
│   └── tools/                 # camada MCP: registro das tools + timing
│       ├── tools.go           # tabela declarativa das 10 tools (registro genérico)
│       └── timing.go          # wrapper que injeta elapsed_ms em toda tool
```

Fluxo de dependências: `tools → service → engine → lang`. Nada retorna.

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
- `"identifiers"` — captura todo identificador como `@id` (usado por `usages_dir`)
- `"calls"` — captura o callee de cada call como `@callee` (usado por `analyze_file`/`callers_dir`)

Adicionar uma linguagem = 1 arquivo novo (ex. `internal/languages/python/python.go`) + 1 linha no slice do `main.go`. Engine, services e tools são 100% genéricos — não conhecem nenhuma gramática.

```go
// cmd/ast-mcp/main.go
for _, l := range []lang.Language{java.Java{}, python.Python{}, golanglang.Go{}} {
    if err := reg.Register(l); err != nil { log.Fatal(err) }
}
```

Exemplo de queries de símbolos do Java (`internal/languages/java/java.go`):

```go
"classes": `(class_declaration name: (identifier) @name) @symbol`,
"methods": `(method_declaration name: (identifier) @name) @symbol`,
"imports": `(import_declaration) @symbol`,
```

- `@name` captura o nome do símbolo
- `@symbol` captura o nó inteiro (posições + texto)

### Camadas: engine → service → tools

- **engine** — operações tree-sitter puras. Não conhece MCP nem filtros de apresentação.
- **service** (`internal/service/`) — orquestração por domínio: varredura multi-linguagem com poda de `kinds[]`/`name`, dossiê do arquivo (parse único), classificação de usages. Outputs têm json tags e são usados verbatim como corpo da resposta. Transporte-agnóstico: um CLI ou teste pode dirigir `service.New(engine.New(reg))` sem MCP.
- **tools** (`internal/tools/tools.go`) — só a camada MCP. O registro é uma tabela declarativa via função genérica `add[In, Out TimedOutput]`: adicionar uma tool = 1 struct de input + 1 handler fino + 1 entrada na tabela.

**Receitas de extensão:**

| Para adicionar... | Passos |
|---|---|
| Linguagem | 1 arquivo em `internal/languages/<lang>/` + 1 item no slice do `main.go`. Zero mudança em engine/service/tools |
| Nova análise de arquivo | função no engine (+ variante que receba árvore pronta, se composta) → método no serviço → campo no DTO |
| Nova tool de diretório | query struct no service → handler + entrada `add()` no `Register` |

### Parser pool

Parsers tree-sitter não são thread-safe e handlers MCP são concorrentes, então o registry mantém um `sync.Pool` por linguagem (`lang.Acquire`). Todo `Parser`/`Tree`/`Query`/`QueryCursor` é liberado com `Close()` (memória CGO).

### Autodetect de linguagem

Nas tools que aceitam `language`/`languages[]` opcionais, a linguagem é inferida pela extensão do arquivo (`.java`, `.py`, `.go`).

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

O servidor fala **MCP por stdio**: lê mensagens JSON-RPC da entrada padrão e responde na saída padrão. Qualquer cliente MCP (agente, editor, CLI) que o execute como processo local ganha as 10 tools automaticamente.

### 1. Obtenha o binário

Instalação automática (detecta OS/arquitetura, baixa a `latest` e adiciona ao `PATH`):

```bash
curl -fsSL https://raw.githubusercontent.com/nicolasmmb/mcp-ast/main/install.sh | bash
```

**Windows** (PowerShell — não funciona `bash`):

```powershell
powershell -ExecutionPolicy Bypass -Command "irm https://raw.githubusercontent.com/nicolasmmb/mcp-ast/main/install.ps1 | iex"
```

O instalador detecta `amd64`/`arm64`, baixa `ast-mcp-windows-<arch>.exe` para `%LOCALAPPDATA%\ast-mcp\` e adiciona ao PATH do usuário (reabra o terminal depois).

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
      "command": ["ast-mcp"]
    }
  }
}
```

**Claude Code** (config do projeto ou global):

```json
{ "mcpServers": { "ast-mcp": { "command": "ast-mcp" } } }
```

**Claude Desktop** (`claude_desktop_config.json`):

```json
{ "mcpServers": { "ast-mcp": { "command": "ast-mcp" } } }
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
  | ast-mcp
```

A resposta ao `initialize` traz `serverInfo` (name `ast-mcp`, version = a tag da release, ex.: `v0.1.3`; localmente `dev`), e o `tools/list` retorna as 10 tools.

### 4. Chame as tools

Conectado, as 10 tools aparecem como ferramentas nativas do agente — peça em linguagem natural ("qual a complexidade de `src/foo.go`?") ou invoque direto. Por baixo é o método MCP `tools/call`:

```json
{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"analyze_file","arguments":{"path":"src/foo.go"}}}
```

Cada tool — argumentos, exemplos de request/response e convenções de posição — está documentada na seção [# Tools](#tools) abaixo.

### Diagnóstico (logs e debug)

Flags de inicialização para ver o que o servidor está fazendo:

| Flag | Efeito |
|---|---|
| `--version` | Imprime a versão (`ast-mcp vX.Y.Z`) e sai; localmente sem tag é `dev` |
| `-verbose` | Loga em **stderr** no nível **debug** (cada chamada de tool com nome + `elapsed_ms`) |
| `-log <caminho>` | Grava o log em **arquivo** (append), nível Info+ — captura erros mesmo sem `-verbose` |

```bash
ast-mcp --version                # ast-mcp v0.2.0
ast-mcp -verbose                 # debug no stderr
ast-mcp -log /tmp/ast-mcp.log    # Info+ (erros/avisos) no arquivo
ast-mcp -verbose -log /tmp/ast-mcp.log   # ambos
```

A versão vem da tag do release, injetada no build (`-ldflags "-X main.version=vX.Y.Z"`); é a mesma reportada no `serverInfo.version` do MCP.

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

Varre um diretório recursivamente e devolve os símbolos de todos os arquivos reconhecidos, agrupados por caminho, depois por tipo. Ignora pastas ocultas. Erros de arquivos individuais vão em `errors` em vez de abortar a varredura. É a tool de diretório mais versátil: com `kinds` substitui uma busca só de variáveis; com `name` localiza onde um símbolo é declarado no codebase inteiro.

**Argumentos:**

| Campo | Tipo | Obrigatório | Descrição |
|---|---|---|---|
| `path` | string | sim | Diretório a varrer |
| `languages[]` | string[] | não | Linguagens a incluir (ex. `["go","java"]`); omitir = autodetect por arquivo |
| `kinds[]` | string[] | não | Tipos de símbolo a retornar; omitir = todos. Válidos por linguagem: **Go**: `types`, `functions`, `methods`, `variables`, `imports`; **Java**: `classes`, `interfaces`, `enums`, `records`, `methods`, `constructors`, `fields`, `variables`, `imports`; **Python**: `classes`, `functions`, `variables`, `imports` |
| `name` | string | não | Retorna apenas declarações cujo nome é exatamente este |
| `include_text` | bool | não | `true` inclui o texto completo da declaração em vez do resumo de 1 linha |
| `limit` | int | não | Máximo de arquivos no resultado (`0` = sem limite) |

**Request — mapa estrutural do repo:**

```json
{"name": "scan_symbols_dir", "arguments": {"path": "./internal", "languages": ["go"]}}
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
    }
  }
}
```

**Exemplos equivalentes às tools antigas:**

```json
{"name": "scan_symbols_dir", "arguments": {"path": "./src", "kinds": ["variables"]}}
{"name": "scan_symbols_dir", "arguments": {"path": ".", "name": "Engine"}}
```

Arquivos que ficam sem símbolo após os filtros são omitidos do map. `language` na resposta é `"auto"` quando detectado por arquivo ou os nomes informados.

---

## `analyze_file`

Dossiê completo de um arquivo em uma chamada (parse único): métricas de tamanho/nesting, estatísticas por tipo de símbolo, **complexidade ciclomática por função/método** e o **call graph** (quem chama quem, com contagem).

**Argumentos:**

| Campo | Tipo | Obrigatório | Descrição |
|---|---|---|---|
| `path` | string | sim | Arquivo a analisar |
| `language` | string | não | Omitir para autodetect |

**Request:**

```json
{"name": "analyze_file", "arguments": {"path": "internal/engine/gettext.go"}}
```

**Response (resumido):**

```json
{
  "elapsed_ms": 5.0,
  "language": "go",
  "metrics": {
    "lines": 44, "bytes": 983, "nodes": 375, "max_nesting": 15,
    "kinds": {
      "methods":   { "count": 1, "avg_lines": 15, "max_lines": 15 },
      "variables": { "count": 7, "avg_lines": 1, "max_lines": 1 }
    }
  },
  "complexity": [
    { "name": "byteOffset", "kind": "functions", "complexity": 4,
      "start": {"row": 29, "col": 0}, "end": {"row": 42, "col": 1} },
    { "name": "GetText", "kind": "methods", "complexity": 4,
      "start": {"row": 12, "col": 0}, "end": {"row": 26, "col": 1} }
  ],
  "call_graph": [
    { "name": "GetText", "kind": "methods", "callees": [
        { "name": "byteOffset", "count": 2 }, { "name": "len", "count": 2 } ],
      "start": {"row": 12, "col": 0}, "end": {"row": 26, "col": 1} }
  ]
}
```

Regra da complexidade ciclomática: cada `if`/`for`/`while`/`switch`/`case`/ternário/`catch` soma 1, e cada `&&`/`||` soma 1. Score > 10 é bom candidato a refactor — use as posições `start`/`end` no `get_text_file` para ler o código da função problemática. `lines` conta `\n` + 1 (arquivo `a.go\nb\n` = 3 linhas). `max_nesting` é a profundidade máxima da árvore — proxy útil de complexidade.

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

## `unused_symbols_dir`

Símbolos **declarados mas nunca referenciados** (dead code) em um diretório. Heurística: um símbolo cujo nome aparece **exatamente uma vez** em todos os arquivos reconhecidos é considerado não usado — a única ocorrência é a própria declaração.

**Argumentos:**

| Campo | Tipo | Obrigatório | Descrição |
|---|---|---|---|
| `path` | string | sim | Diretório para buscar recursivamente |
| `languages[]` | string[] | não | Linguagens a incluir; omitir = autodetect por arquivo |
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

Antes de deletar um suspeito, confirme com `callers_dir` (resposta vazia = ninguém chama) e lembre de símbolos exportados/públicos que podem ser consumidos fora do diretório varrido.

**Limitações da heurística** (`ponytail:` trade-off): contagem textual de ocorrências, não análise de referências semântica. Comentários e strings contam como uso (nunca marca falso-unused, mas pode **deixar passar** um símbolo de fato não usado que aparece num comentário). O resultado é por diretório: um símbolo usado **fora** do diretório varrido aparece como unused.

---

## `usages_dir`

Acha **todas as ocorrências** de um nome de símbolo em um diretório (via query de identificadores da linguagem) e classifica cada uma:

- **`definition`** — o ponto de declaração (cruzado com as symbol queries da linguagem)
- **`call-site`** — o nome é o callee de uma invocação; traz `caller` = função/método contenedor da chamada
- **`reference`** — qualquer outro uso

Use antes de renomear ou deletar para enumerar todos os pontos de edição. Para contagem agregada "quem chama X" use `callers_dir`; para só declarações use `scan_symbols_dir` com `name`.

**Argumentos:**

| Campo | Tipo | Obrigatório | Descrição |
|---|---|---|---|
| `name` | string | sim | Nome do símbolo a rastrear |
| `path` | string | sim | Diretório para buscar recursivamente |
| `languages[]` | string[] | não | Linguagens a incluir; omitir = autodetect por arquivo |
| `limit` | int | não | Máximo de matches (0 = sem limite) |

**Request:**

```json
{"name": "usages_dir", "arguments": {"name": "appendCallee", "path": "internal/engine"}}
```

**Response:**

```json
{
  "elapsed_ms": 47.5,
  "language": "auto",
  "matches": [
    { "file": "internal/engine/calls.go", "line": 75, "col": 23,
      "kind": "call-site", "caller": "callGraphTree",
      "text": "appendCallee(funcs[idx].Callees, callee)" },
    { "file": "internal/engine/calls.go", "line": 218, "col": 5,
      "kind": "definition",
      "text": "func appendCallee(callees []Callee, name string) []Callee {" }
  ]
}
```

`text` é a linha do pai do identificador (contexto do match). Um renome envolve editar todas as ocorrências retornadas: a(s) `definition`, os `call-site` e os `reference`.

---

## `callers_dir`

Dado um **nome alvo**, acha todas as funções/métodos que o chamam em um diretório, agregando os call sites por função chamadora com contagem exata. Responde "quem depende disso?" — a pergunta-chave antes de mudar ou deletar uma função.

**Argumentos:**

| Campo | Tipo | Obrigatório | Descrição |
|---|---|---|---|
| `name` | string | sim | Nome da função/método alvo |
| `path` | string | sim | Diretório para buscar recursivamente |
| `languages[]` | string[] | não | Linguagens a incluir; omitir = autodetect por arquivo |
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

Cada entrada é uma função chamadora com `file`, `name`, `kind`, posição do símbolo e `count` (quantas vezes chama o alvo). Chamadas de método via `selector` (`obj.foo()`) são capturadas pelo nome do campo; o `count` agrega múltiplos call sites da mesma função. O que uma função **chama** (direção inversa) está no `call_graph` do `analyze_file`.

---

# Receitas de composição

As tools foram desenhadas para combinar: as de diretório respondem "onde/quem", as de arquivo respondem "como está", e posições fluem entre elas via `get_text_file`. Padrões que cobrem quase tudo:

**Onboarding num repo novo**
1. `list_languages` → descubra o que há
2. `scan_symbols_dir(path, languages: [...])` → mapa estrutural completo em uma chamada
3. `analyze_file` nos arquivos maiores → complexidade + fluxo interno
4. `get_text_file` nos trechos-chave

**Impacto antes de mudar uma função**
1. `callers_dir(name)` → quem depende dela e com que frequência
2. `usages_dir(name)` → todas as linhas onde aparece
3. `get_text_file` nos contextos relevantes

**Renomear um símbolo com segurança**
1. `usages_dir(name)` → lista completa de edição: definition + reference + call-site
2. Edite cada ponto; re rode `usages_dir` para conferir zero ocorrências do nome velho

**Caçar dead code**
1. `unused_symbols_dir(path)` → suspeitos
2. `callers_dir(name)` por suspeito → resposta vazia confirma
3. `get_text_file` na declaração antes de deletar

**Refactor de hotspot de complexidade**
1. `analyze_file` no arquivo suspeito → pegue a maior `complexity` (já vem com `start`/`end`)
2. `get_text_file` desse range → leia a função inteira sem carregar o arquivo

# Guia para agentes (LLM)

Regras operacionais para usar bem este servidor:

- **Comece largo, depois estreite**: `scan_symbols_dir` com filtros `kinds[]`/`name` substitui grep textual para localizar declarações — é AST-aware (ignora comentários/strings) e devolve posições.
- **Nunca leia um arquivo inteiro para ver uma função**: qualquer output traz `start`/`end`; passe-os ao `get_text_file`.
- **`include_text` só quando precisar do corpo**; o default mantém outputs pequenos.
- **Sempre rode `usages_dir` + `callers_dir` antes de renomear ou deletar.**
- **Uma pergunta por tool**: dossiê de arquivo → `analyze_file`; impacto de símbolo → `callers_dir`/`usages_dir`; extração sob medida → `query_ast_file`; navegação da árvore → `parse_ast_file` (com `max_depth`).

Snippet para o `AGENTS.md`/`CLAUDE.md` dos projetos que consomem este MCP:

```markdown
## AST analysis (ast-mcp)
- Structural overview first: scan_symbols_dir with languages+kinds before exploring manually.
- Before ANY rename/delete: usages_dir(name) for edit points, callers_dir(name) for impact.
- Read exact code ranges with get_text_file using positions from tool outputs — don't cat whole files.
```

---

# Notas de design

- **`text` truncado por padrão**: para manter os outputs pequenos, `text` vem como a primeira linha do nó (até 200 chars). O corpo completo é obtido com `include_text: true` (`symbols_file`/`query_ast_file`) ou via `get_text_file`. Isso evita que listar símbolos de um arquivo grande exploda o contexto.
- **`matches`/`captures` sempre `[]`**, nunca `null` — garante compatibilidade com a validação de schema de output do SDK.
- **Erros** são retornados como erro da tool MCP (`isError: true`) com mensagem clara — ex. query inválida, arquivo inexistente, linguagem desconhecida, `end` antes de `start`.

# Testes

```bash
go test ./...   # parse + símbolos por linguagem; engine (scan, analyze, dossier, query, get_text, usages, complexity, unused, callgraph, callers); service (filtros kinds/name, poda, dossiê)
go vet ./...    # gofmt limpo
```
