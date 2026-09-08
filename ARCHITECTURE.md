# mcp-ast — Arquitetura e Métricas

Servidor MCP em Go para análise AST de múltiplas linguagens via tree-sitter. Comunica por stdio JSON-RPC, expõe 13 tools.

## Visão geral

```
cmd/ast-mcp/main.go          → CLI + bootstrap MCP server
  ├── internal/lang/         → Interface Language + Registry (pool de parsers)
  ├── internal/engine/       → Operações tree-sitter puras (8 arquivos)
  │     engine.go            → Parse, Query, AST→JSON
  │     symbols.go           → Extração de símbolos + scan de diretório
  │     calls.go             → Call graph + caller reverso (+ functionRanges/findFunc)
  │     search.go            → Detecção de unused symbols
  │     metrics.go           → Métricas de arquivo + complexidade (variantes *Tree)
  │     usages.go            → Ocorrências classificadas: definition/call-site/reference
  │     dossier.go           → Composição métricas+complexidade+call graph (parse único)
  │     gettext.go           → Extração de texto por posição
  ├── internal/service/      → Orquestração por domínio, transporte-agnóstico (6 arquivos)
  │     service.go           → Container Services + filtros de linguagem + limitFiles
  │     scan.go              → ScanService: varredura multi-lang + poda genérica kinds/name
  │     file.go              → FileAnalysisService: dossiê do arquivo + símbolos
  │     usages.go / unused.go / calls.go → wrappers com merge multi-linguagem
  │     repo.go              → RepoService: indexação/refresh/consulta de repositório
  ├── internal/repoindex/    → Boundary de armazenamento do índice
  │     store.go             → Store interface + MemoryStore (RAM, interning, Apply delta)
  │     graph.go             → call/import graphs + Tarjan/SCC + DAG condensado + impacto
  │     cursor.go            → cursores de paginação versionados
  └── internal/tools/        → Camada MCP (2 arquivos)
        tools.go             → Tabela declarativa das 13 tools via add[In, Out] genérico
        timing.go            → Wrapper de timeout + elapsed_ms + logging
```

Fluxo de dependências: `tools → service → engine → lang`. Nada retorna.

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

| Arquivo | Linhas | Nós | Max Nesting | Funções | Métodos |
|---------|--------|-----|-------------|---------|---------|
| `main.go` | 89 | 884 | 19 | 2 | 0 |
| `engine.go` | 169 | 1.724 | 24 | 4 | 8 |
| `symbols.go` | 125 | 1.166 | 21 | 1 | 4 |
| `calls.go` | 227 | 2.263 | 29 | 4 | 3 |
| `search.go` | 80 | 629 | 26 | 0 | 1 |
| `metrics.go` | 196 | 1.827 | 23 | 3 | 4 |
| `usages.go` | 151 | 1.482 | 31 | 2 | 1 |
| `dossier.go` | 38 | 313 | 14 | 0 | 1 |
| `gettext.go` | 44 | 375 | 15 | 1 | 1 |
| `service/*.go` (6) | 356 | 2.924 | 24 | 6 | 6 |
| `lang.go` | 112 | 1.061 | 20 | 1 | 5 |
| `tools.go` | 322 | 3.106 | 29 | 12 | 0 |
| `timing.go` | 66 | 613 | 19 | 3 | 1 |
| **Total** | **1.975** | **18.367** | — | **39** | **35** |

### Complexidade ciclomática (funções > 8)

| Função | Arquivo | Complexidade | Notas |
|--------|---------|-------------|-------|
| `UnusedSymbols` | `search.go` | **15** | Maior do repo — contagem + heurística textual |
| `Usages` | `usages.go` | **14** | Classificação definition/call-site/reference |
| `Callers` | `calls.go` | **14** | Agregação de callers por arquivo |
| `pruneGroups` | `service/scan.go` | **12** | Poda genérica kinds/name (helper genérico `[V any]`) |
| `walkFiles` | `symbols.go` | **11** | Walk recursivo + filtros |
| `complexityTree` | `metrics.go` | **11** | Walk + decisão de kinds |
| `SymbolsText` | `symbols.go` | **10** | Query + formatação |
| `functionRanges` | `calls.go` | **10** | Mapeia ranges de funções |

### Tools MCP

| # | Tool | Escopo | Handler (tools.go) |
|---|------|--------|--------------------|
| 1 | `list_languages` | meta | `handleListLanguages` |
| 2 | `parse_ast` | arquivo | `handleParseAST` |
| 3 | `query_ast` | arquivo | `handleQueryAST` |
| 4 | `scan_symbols` | arquivo ou diretório | `handleScanSymbols` |
| 5 | `analyze_file` | arquivo | `handleAnalyzeFile` (dossiê: métricas+complexidade+call graph) |
| 6 | `get_text` | arquivo | `handleGetText` |
| 7 | `find_usages` | diretório | `handleFindUsages` (occurrences/callers/unused/definitions/imports) |
| 8 | `rank_complexity` | diretório | `handleRankComplexity` |
| 9 | `outline_file` | arquivo | `handleOutlineFile` |
| 10 | `index_status` | repositório | `handleIndexStatus` (sem argumentos) |
| 11 | `repo_impact` | repositório | `handleRepoImpact` |
| 12 | `repo_cycles` | repositório | `handleRepoCycles` |
| 13 | `repo_topology` | repositório | `handleRepoTopology` |

Registro declarativo: `Register()` lista 13 chamadas a `add[In any, Out TimedOutput]()`, que aplica o wrapper `timed()` (timeout + elapsed_ms + log). Handlers são adapters finos input→service; toda orquestração vive em `internal/service`. Nenhuma tool recebe identificador de repositório: só `path`.

### Serviços (internal/service)

| Serviço | Método | Responsabilidade |
|---------|--------|------------------|
| `ScanService` | `Dir(ScanQuery)` | Resolve linguagens, varre cada uma, poda com `pruneGroups[V]` (genérico), aplica `limitFiles` |
| `FileAnalysisService` | `Dossier(lang, path)` / `Symbols(...)` | Dossiê com parse único (via variantes `*Tree` do engine); símbolos do arquivo |
| `UsagesService` | `Dir(name, dir, ...)` | Ocorrências classificadas, merge multi-linguagem |
| `UnusedService` | `Dir(dir, ...)` | Dead code heuristic |
| `CallsService` | `Callers(name, dir, ...)` | Quem chama o alvo, agregado |

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

## Indexação automática (índice em RAM)

Fluxo de boot: `main` parseia as flags `-repo` (repetível, valida diretórios) → para
cada root chama `RepoService.Index` (restaura snapshot se válido; senão build em
background) → registra `root → índice` → liga o watch (default on). Sem `-repo`, nada
é indexado e o servidor opera 100% em disco.

O build roda em background e publica um snapshot atômico sob `RWMutex`. Postings
são chaveados por `NameID → FileID → UsageRef` (interning de paths/nomes/canonical;
`UsageRef` é ~40 bytes vs ~112 de `UsageMatch`). O refresh (disparado pelo watch)
aplica `ChangeSet` incremental com coalescing (`TryLock`), erros por arquivo
(`last_errors`, cap 50) e retry TOCTOU (`errUnstable`). Deltas > 20% ou 500 arquivos
viram rebuild completo.

**Resolução por path.** `RepoService.ResolveIndex(path)` normaliza o path e escolhe o
root registrado com o maior prefixo; só índices `ready` atendem. Os handlers das tools
chamam os métodos `*At` (`ScanAt`, `FindOccurrencesAt`, `UnusedAt`, `CallersAt`,
`DefinitionsAt`, `ComplexityAt`, `OutlineAt`, `AnalyzeAt`, `ImpactAt`, `CyclesAt`,
`TopologyAt`): path coberto → índice; não coberto → fluxo de disco. A escolha entre
disco e índice não é da tool nem do cliente. O identificador interno de repositório
não atravessa a camada de tools (métodos por id são não-exportados).

`outline_file` (sem texto) é servido por `OutlineFromSymbols` (`source: indexed`);
dados ausentes ou arquivo alterado caem no AST fallback de um único arquivo (com
reindexação automática). Paginação por cursor versionado
(`next_cursor`/`truncated`; cursor de versão antiga é rejeitado).

Grafos são direcionados gerais, não DAGs: recursão e imports circulares criam ciclos.
`repo_cycles` roda Tarjan; `repo_topology` condensa SCCs em DAG por camadas (Kahn).
`repo_impact` faz BFS reverso/direto com depth/limit. As três exigem path dentro de
um repositório configurado (erro caso contrário). Limites semânticos (lexical):
call graph classifica arestas como `exact|candidate|unresolved` pela contagem de
declarações; imports externos ficam como specifier bruto; `unused` é heurístico
(sem resolução de escopo) e não conta comentários/strings.

Watch por polling (default on, `-watch=false` desliga, `-watch-interval` default 2s):
cada tick chama o refresh incremental (delta vazio = no-op). `index_status` expõe o
estado de todos os repositórios: `state`, `watch`, `last_sync`, `restored`, memória.

## Benchmarks de escala (repo sintético, 50 mil arquivos)

Fixture: 50k arquivos Go sintéticos, 2 funções e 1 referência compartilhada `token` por arquivo.
Hardware: Apple M2 Pro (darwin/arm64). Comando: `go test ./internal/service -bench . -benchtime=1x`.

| Cenário | Tempo | Alocações |
|---|---:|---:|
| build inicial do índice (`-repo`) | ~6,5 s | ~1,07 GB / 14 M allocs |
| refresh do watch sem mudanças (diff 50k) | ~222 ms | 57 MB |
| refresh do watch com 1% alterado (500 arquivos) | ~994 ms | 188 MB |
| lookup indexado (página de 500 em 50k ocorrências) | ~20 ms | 3,9 MB |
| `repo_impact` reverse depth 1 | ~17 ms | 14 MB |

Lookup indexado não repete walk/parse; o custo por página é a janela de postings
(`UsagesWindow`), não a cardinalidade total.

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
| `-repo <dir>` | Indexa o diretório no boot (repetível); buscas dentro dele usam o índice automaticamente |
| `-max-memory` | Orçamento do índice em MB (default `auto`: 25% da RAM, 256 MB–4 GB) |
| `-watch` | Mantém índices frescos por polling (default `true`; `-watch=false` desliga) |
| `-watch-interval` | Intervalo do polling (default 2s) |
| `-cache-dir` | Override do diretório de snapshots (default: cache do usuário) |

## Testes

```bash
go test ./...   # engine (scan, analyze, dossier, query, get_text, usages, complexity, unused, callgraph, callers)
                # service (filtros kinds/name, poda genérica, limite, dossiê, classificação usages)
                # linguagens (parse + símbolos go/java/python)
go vet ./...    # gofmt limpo
```

| Arquivo de teste | Package | Coverage |
|------------------|---------|----------|
| `engine_test.go` | engine | Scan, analyze, dossier, query, get_text, include_text, complexity, unused, usages (classificação), callgraph, callers, cancelamento |
| `service_test.go` | service | Filtro kinds, filtro name, limite, displayLang, classificação call-site/caller, dossiê, pruneGroups |
| `go_test.go` | golang | Parse, símbolos Go |
| `python_test.go` | python | Parse, símbolos Python |
| `java_test.go` | java | Parse, símbolos Java |
