# ast-mcp

Servidor [MCP](https://modelcontextprotocol.io) em Go para análise de AST de múltiplas linguagens usando [tree-sitter](https://github.com/tree-sitter/go-tree-sitter) e o [Go SDK oficial](https://github.com/modelcontextprotocol/go-sdk).

Analisa arquivos e diretórios e expõe **13 tools** por stdio
(**breaking change** — nomes legados e tools de ciclo de vida de índice removidos, sem aliases):

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
| `index_status` | repositório | Estado dos índices configurados com `-repo` (sem argumentos) |
| `repo_impact` | repositório | Quem depende (reverse) ou referencia (forward) um símbolo/arquivo |
| `repo_cycles` | repositório | SCCs/ciclos do grafo de calls ou imports |
| `repo_topology` | repositório | DAG condensado (SCC) do grafo, em camadas topológicas |

Todas as tools recebem apenas `path`. Quando o path está coberto por um repositório
configurado com a flag `-repo`, a consulta usa o índice em RAM automaticamente
(`source: indexed`); caso contrário, usa o disco. Nenhum identificador de repositório
é necessário.

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
| `find_usages` | `mode`, `matches`/`files`/`callers`/`symbols`, `errors?`, `next_cursor?`, `truncated?` |
| `rank_complexity` | `language`, `entries: [{file, name, complexity, start, end}]`, `errors?` |
| `outline_file` | `language`, `path`, `outline: [{name, kind, children}]`, `source?` (indexed/ast_fallback) |
| `analyze_file` | `language`, `metrics`, `complexity[]`, `call_graph[]`, `source?` |
| `index_status` | `repos: [{root, state, index_version, files_indexed, files_failed, last_errors?, memory_used_bytes, watch?, last_sync?, restored?}]` |
| `repo_impact` | `nodes: [{name, distance}]`, `truncated?`, `next_cursor?`, `resolution_counts?` |
| `repo_cycles` | `cycles: [[...]]` |
| `repo_topology` | `layers: [[SCC{...}]]` |

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
│   │   ├── metrics.go         # métricas + complexidade ciclomática
│   │   ├── rank_outline.go    # ranking + outline hierárquico
│   │   └── index.go           # facts por arquivo (parse único)
│   ├── repoindex/             # boundary de armazenamento do índice
│   │   ├── store.go           # Store interface + MemoryStore (RAM, interning, Apply delta)
│   │   ├── graph.go           # call/import graphs + Tarjan/SCC + DAG + impacto
│   │   └── cursor.go          # cursores de paginação versionados
│   ├── service/               # orquestração de domínio
│   │   ├── service.go         # Services aggregator
│   │   ├── scan.go            # ScanService (file ou dir)
│   │   ├── file.go            # FileAnalysisService + Outline
│   │   ├── find.go            # FindService (modes)
│   │   ├── rank.go            # RankService
│   │   └── repo.go            # RepoService (index/refresh/watch/consultas)
│   └── tools/                 # camada MCP: registro das 13 tools + timing
│       ├── tools.go
│       └── timing.go
```

Fluxo de dependências: `tools → service → repoindex/engine → lang`. Nada retorna.

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
Qualquer cliente MCP (agente, editor, CLI) que o execute como processo local ganha as tools automaticamente.

### Indexação automática (`-repo`)

A indexação é configuração do servidor, não uma tool. Inicie com uma ou mais flags
`-repo` e o servidor indexa cada repositório no boot (em background), restaura o
snapshot do boot anterior quando existir e mantém o índice fresco via watch:

```bash
ast-mcp -repo /workspace/project -repo /workspace/lib
```

Depois disso, qualquer consulta cujo `path` esteja dentro de um repositório
configurado usa o índice em RAM automaticamente — sem novo walk/parse global e sem
nenhum identificador:

```json
{"name": "find_usages", "arguments": {"path": "/workspace/project/internal", "mode": "occurrences", "name": "walkFiles"}}
{"name": "index_status", "arguments": {}}
```

Sem flag `-repo`, o servidor opera 100% em disco: nada é indexado e nenhuma tool
muda de comportamento. A escolha entre índice e disco é do servidor (maior prefixo
de root coberto, índice em estado `ready`), nunca do cliente. `index_status` lista
os repositórios configurados: estado, contagem de arquivos, memória, watch,
`last_sync` e `restored`.

Resultados de `analyze_file`/`outline_file` declaram `source`: `indexed` (dados no
índice) ou `ast_fallback` (reparse do arquivo quando o dado não está indexado ou o
arquivo mudou; o arquivo é reindexado automaticamente).

**Grafos.** O índice mantém o call graph (função → função, lexical) e o import graph
(arquivo → specifier, com resolução de imports relativos). Cada aresta de call carrega
`resolution`: `exact` (uma declaração), `candidate` (várias) ou `unresolved` (nenhuma).
Os grafos são direcionados gerais (podem ter ciclos): `repo_cycles` roda Tarjan e
`repo_topology` condensa os SCCs em um DAG por camadas. `repo_impact` responde quem
depende de um símbolo (reverse) ou o que ele referencia (forward), com `depth`/`limit`.

```json
{"name": "repo_impact", "arguments": {"path": "/workspace/project/internal", "graph": "calls", "target": "walkFiles", "direction": "reverse", "depth": 1}}
{"name": "repo_cycles", "arguments": {"path": "/workspace/project", "graph": "imports"}}
{"name": "repo_topology", "arguments": {"path": "/workspace/project", "graph": "imports"}}
```

As tools de grafo exigem um path dentro de um repositório configurado com `-repo`
(retornam erro caso contrário).

**Identidade canônica.** Símbolos com uma única declaração recebem a chave
`linguagem|arquivo|kind|nome` em `canonical`; buscas por essa chave ignoram homônimos.
Nomes ambíguos ficam sem `canonical` (resolução `candidate`).

**Unused.** `find_usages` com `mode=unused` em um path indexado usa ocorrências AST
do índice (comentários e strings não contam) e declara `source: "indexed_heuristic"` —
o método não resolve escopo nem overloads.

**Paginação.** `find_usages` (path indexado) e `repo_impact` retornam
`next_cursor` e `truncated`. O cursor é versionado: um cursor de versão anterior do
índice é rejeitado com erro explícito.

**Refresh automático.** O watch (ligado por padrão) aplica o refresh incremental:
compara `size`+`mtime` (de-bounce por SHA-256), reindexa só o delta e aplica
atomicamente (versão do índice sobe). Deltas > 20% dos arquivos (ou > 500) disparam
rebuild completo em background. Refresh concorrente é coalescido (retorna
`state: "refreshing"`); arquivo que muda durante a análise é marcado `unstable` e
não publica fatos parciais.

**Watch.** Ligado por padrão (`-watch-interval=2s`); desligue com `-watch=false`.
`index_status` expõe `watch` e `last_sync` por repositório.

**Memória.** O orçamento é automático (25% da RAM disponível, entre 256 MB e 4 GB) ou
explícito: `ast-mcp -max-memory=2048mb`. Acima do orçamento o índice entra em estado
`partial`. Postings internos usam IDs interning (`FileID`/`NameID`), sem strings
repetidas por ocorrência.

**Persistência entre reinícios.** Ao final de cada build/refresh, o índice é salvo
como snapshot compacto (formato próprio, sem ASTs) em `~/.cache/ast-mcp` (ou
`-cache-dir`). No próximo boot com `-repo`, o servidor restaura o snapshot em vez de
reindexar (`index_status` mostra `restored: true`) e roda um refresh incremental para
capturar mudanças no disco. O snapshot é invalidado quando o root, o schema, as
linguagens ou a versão do binário mudam.

### O que consulta o disco vs. o índice

A escolha é automática: path coberto por um `-repo` em estado `ready` → índice;
caso contrário → disco.

| Tool | Path coberto por `-repo` | Path fora |
|---|---|---|
| `scan_symbols` | índice | disco |
| `find_usages` (occurrences/unused) | índice | disco |
| `rank_complexity` | índice | disco |
| `outline_file` (sem texto) | índice (`source: indexed`) | disco |
| `outline_file` (com texto) / `analyze_file` | reparse de 1 arquivo (`ast_fallback`) | disco |
| `parse_ast` / `query_ast` / `get_text` | disco (aceitam AST/query/range arbitrários) | disco |
| `repo_impact` / `repo_cycles` / `repo_topology` | índice | erro (exigem `-repo`) |

### 1. Obtenha o binário

Instalação automática (detecta OS/arquitetura, baixa a `latest` e adiciona ao `PATH`):

```bash
curl -fsSL https://raw.githubusercontent.com/nicolasmmb/mcp-ast/main/install.sh | bash
```

Versão específica (release):

```bash
curl -fsSL https://raw.githubusercontent.com/nicolasmmb/mcp-ast/main/install.sh | AST_MCP_VERSION=v0.7.1 bash
```

Binário de um PR (artifact do workflow `pr-build`, sem precisar do GitHub CLI):

```bash
curl -fsSL https://raw.githubusercontent.com/nicolasmmb/mcp-ast/main/install.sh | AST_MCP_PR=13 bash
```

O modo PR precisa de `curl`, `jq` e `unzip`. Se `GH_TOKEN` estiver definido o
download usa a API oficial; senão usa `nightly.link` como proxy público dos artifacts.

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
      "command": "ast-mcp",
      "args": ["-repo", "/workspace/project"]
    }
  }
}
```

Sem `args`, o servidor opera em modo disco puro.

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

**Consulta indexada (servidor iniciado com `-repo /workspace/project`)**
```json
{"name": "find_usages", "arguments": {"path": "/workspace/project/internal", "mode": "occurrences", "name": "walkFiles", "group_by_file": false, "limit": 100}}
{"name": "index_status", "arguments": {}}
```

## Desenvolvimento

```bash
go test ./...
```

Matriz de testes cobre as 9 linguagens (Go, Java, Python, JS, TS, Rust, C#, Bash, YAML) com fixtures em `internal/service/testdata/matrix/` e valida **toda** query de `SymbolQueries` e `AuxQueries` por linguagem.

## Releases

Releases são geradas automaticamente: a cada push em `main` um workflow calcula a próxima versão (`feat:` → minor, senão patch), cria a tag `vX.Y.Z` e publica os binários (com checksum `.sha256`).
