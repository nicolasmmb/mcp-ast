# mcp-ast — Arquitetura e Métricas

Servidor MCP em Go para análise AST de múltiplas linguagens via tree-sitter. Comunica por stdio JSON-RPC, expõe 14 tools.

## Visão geral

```
cmd/ast-mcp/main.go          → CLI + bootstrap MCP server
  ├── internal/lang/          → Interface Language + Registry (pool de parsers)
  ├── internal/engine/        → Lógica core (6 arquivos)
  │     engine.go             → Parse, Query, AST→JSON
  │     symbols.go            → Extração de símbolos + scan de diretório
  │     calls.go              → Call graph + caller reverso
  │     search.go             → Busca por nome + detecção de unused
  │     metrics.go            → Métricas de arquivo + complexidade ciclomática
  │     gettext.go            → Extração de texto por posição
  │     rename.go             → Preview de rename com flag de definição
  ├── internal/tools/         → Handlers das 14 tools MCP (2 arquivos)
  │     tools.go              → Definição + handlers
  │     timing.go             → Wrapper de timeout + elapsed_ms + logging
  └── internal/languages/     → Implementações por linguagem
        go/go.go              → Go grammar + queries
        python/python.go      → Python grammar + queries
        java/java.go          → Java grammar + queries
```

## Interface Language

Toda linguagem implementa (`internal/lang/lang.go`):

```go
type Language interface {
    Name() string                       // "go", "java", "python"
    Extensions() []string               // [".go"]
    Language() *ts.Language             // gramática tree-sitter
    SymbolQueries() map[string]string   // queries nomeadas por tipo de símbolo
    DecisionKinds() []string            // kinds que somam complexidade ciclomática
    AuxQueries() map[string]string      // queries auxiliares (identifiers, calls)
}
```

**Registry** mantém um `sync.Pool` por linguagem (parsers tree-sitter não são thread-safe). Métodos: `Register`, `Get`, `Resolve` (autodetect por extensão), `List`, `Acquire` (pool com `Close()`).

## Linguagens suportadas

| Linguagem | Extensões | Symbol Kinds |
|-----------|-----------|--------------|
| Go | `.go` | types, functions, methods, imports, variables |
| Java | `.java` | classes, interfaces, enums, records, methods, constructors, fields, imports, variables |
| Python | `.py` | classes, functions, imports, variables |

## Métricas do código (analisado pelo próprio mcp-ast)

### Arquivos Go — resumo

| Arquivo | Linhas | Bytes | Nós | Max Nesting | Funções | Métodos |
|---------|--------|-------|-----|-------------|---------|---------|
| `main.go` | 88 | 2.332 | 871 | 19 | 2 | 0 |
| `engine.go` | 169 | 4.455 | 1.724 | 24 | 4 | 8 |
| `symbols.go` | 142 | 3.972 | 1.345 | 21 | 1 | 5 |
| `calls.go` | 224 | 5.776 | 2.201 | 29 | 4 | 2 |
| `search.go` | 116 | 3.091 | 948 | 26 | 0 | 2 |
| `metrics.go` | 187 | 4.829 | 1.705 | 23 | 3 | 2 |
| `rename.go` | 96 | 2.676 | 952 | 33 | 1 | 1 |
| `gettext.go` | 44 | 983 | 375 | 15 | 1 | 1 |
| `tools.go` | 463 | 18.448 | 4.954 | 29 | 1 | 15 |
| `lang.go` | 112 | 2.689 | 1.061 | 20 | 1 | 5 |
| **Total** | **1.641** | **49.251** | **16.136** | — | **18** | **41** |

### Complexidade ciclomática (funções > 10)

| Função | Arquivo | Complexidade | Notas |
|--------|---------|-------------|-------|
| `Callers` | `calls.go:132` | **14** | Maior do repo — lógica de aggregate callers |
| `Complexity` | `metrics.go:29` | **12** | Walk + decisão de kinds |
| `walkFiles` | `symbols.go:94` | **11** | Walk recursivo + filtros |
| `functionRanges` | `calls.go:78` | **10** | Mapeia ranges de funções |
| `CallGraph` | `calls.go:29` | **10** | Monta call graph |
| `RenamePreview` | `rename.go:23` | **10** | Walk + cruzamento com definições |
| `SymbolsText` | `symbols.go:20` | **10** | Query + formatação |
| `UnusedSymbols` | `search.go:66` | **15** | Maior do repo — contagem + heurística |
| `Analyze` | `metrics.go:134` | **9** | Coleta métricas |
| `SearchName` | `search.go:28` | **9** | Busca por nome |

**Maior complexidade:** `UnusedSymbols` (15) e `Callers` (14). Ambos envolvem lógica de agregação iterativa.

### Call graph — funções mais chamadoras

**`calls.go`** — função que mais chama outras:
- `Callers` chama: `make`, `functionRanges`, `point`, `findFunc`, `append` (×2), `len` (×2)
- `CallGraph` chama: `functionRanges`, `len`, `point`, `findFunc`, `appendCallee`

### Métodos do engine

| Método | Arquivo | Complexidade | Descrição |
|--------|---------|-------------|-----------|
| `New` | engine.go:48 | 1 | Construtor |
| `Resolve` | engine.go:50 | 1 | Resolve linguagem |
| `ListLanguages` | engine.go:54 | 1 | Lista linguagens |
| `Parse` | engine.go:59 | 2 | Parse de arquivo |
| `Query` | engine.go:71 | 1 | Query tree-sitter |
| `QueryLimit` | engine.go:78 | 1 | Query com limite |
| `QueryText` | engine.go:84 | 2 | Query com texto |
| `parseFile` | engine.go:93 | 2 | Parse interno |
| `runQuery` | engine.go:103 | **7** | Executa query + formata resultados |
| `toNode` | engine.go:137 | 4 | Converte AST node para JSON |

### Tools MCP

| # | Tool | Handler | Complexidade |
|---|------|---------|-------------|
| 1 | `list_languages` | `listLanguages` | 1 |
| 2 | `parse_ast_file` | `parseAST` | 4 |
| 3 | `query_ast_file` | `queryAST` | 3 |
| 4 | `symbols_file` | `symbols` | 3 |
| 5 | `scan_symbols_dir` | `scanSymbols` | 3 |
| 6 | `scan_variables_dir` | `scanVariables` | 3 |
| 7 | `analyze_file` | `analyze` | 3 |
| 8 | `get_text_file` | `getText` | 3 |
| 9 | `search_name_dir` | `searchName` | 3 |
| 10 | `complexity_file` | `complexity` | 3 |
| 11 | `unused_symbols_dir` | `unusedSymbols` | 3 |
| 12 | `rename_preview_dir` | `renamePreview` | 3 |
| 13 | `call_graph_file` | `callGraph` | 3 |
| 14 | `callers_dir` | `callers` | 3 |

Todas usam `langFilter` (complexidade 3) para resolver o filtro de linguagem.

## Dependências

| Módulo | Versão | Uso |
|--------|--------|-----|
| `modelcontextprotocol/go-sdk` | v1.7.0 | SDK MCP oficial |
| `tree-sitter/go-tree-sitter` | v0.25.0 | Bindings Go → tree-sitter (CGO) |
| `tree-sitter/tree-sitter-go` | v0.25.0 | Grammar Go |
| `tree-sitter/tree-sitter-java` | v0.23.5 | Grammar Java |
| `tree-sitter/tree-sitter-python` | v0.25.0 | Grammar Python |
| `google/jsonschema-go` | v0.4.3 | Schema de output das tools |

**CGO obrigatório:** tree-sitter usa C. `CGO_ENABLED=0` falha. Cross-compile precisa de clang cross-arch (macOS) ou runners nativos (Linux).

## CI/CD

Workflow único `.github/workflows/release.yml`:

1. **tag** — calcula versão (feat: → minor, senão patch), cria tag `vX.Y.Z`
2. **build** — `go vet` + `go test`, builda 5 binários em runners nativos
3. **release** — publica GitHub Release com checksums SHA256

Binários: `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`, `windows/amd64`.

## Instaladores

- `install.sh` — bash (Linux/macOS/Git Bash): detecta OS/arch, baixa release, instala em `/usr/local/bin` ou `~/.local/bin`
- `install.ps1` — PowerShell (Windows): detecta AMD64/ARM64, instala em `%LOCALAPPDATA%\ast-mcp\`, adiciona ao PATH

## Flags CLI

| Flag | Efeito |
|------|--------|
| `--version` | Imprime versão e sai |
| `-verbose` | Debug no stderr |
| `-log <path>` | Info+ em arquivo (append) |
| `-tool-timeout` | Timeout por tool (default 30s) |

## Testes

```bash
go test ./...   # 4 arquivos de teste (engine + 3 linguagens)
go vet ./...    # gofmt limpo
```

| Arquivo de teste | Package | Coverage |
|------------------|---------|----------|
| `engine_test.go` | engine | Scan, analyze, query, get_text, search, complexity, unused, rename, callgraph, callers |
| `go_test.go` | golang | Parse, símbolos Go |
| `python_test.go` | python | Parse, símbolos Python |
| `java_test.go` | java | Parse, símbolos Java |
