# Plano — Logs descritivos (arquivo + debug do MCP)

Status geral: L5 em execução; L6–L7 pendentes. Branch: `feat/index-logging` (PR #14).

**Regras:**
- Uma história por commit; a história N só começa com a N-1 **commitada e aprovada** (diff mostrado antes de cada commit).
- Cada commit aprovado é pushado na hora (PR #14 atualiza sozinho).
- Gate por história: *Pronto quando* + *Validação* + `go test ./...` e `go vet ./...` verdes.
- Idioma das mensagens: **pt-BR**.
- Fora de escopo: `[error]`/`undefined` no console do cliente — renderização do próprio cliente sobre o stderr.

---

## L5 — Logs visíveis no debug do MCP por padrão — [DONE]

*Como operador, quero ver os logs no console de debug do MCP sem passar `-verbose`.*

- [x] L5.1 `newLogger` (`cmd/ast-mcp/main.go`): writer padrão = `os.Stderr` (nível Info); `-verbose` só eleva para Debug; `-log` espelha para o arquivo. Fim do `io.Discard`.
- [x] L5.2 Help das flags atualizado.
- [x] L5.3 Este plano criado em `docs/PLAN-logs-descritivos.md`.
- [x] L5.4 Teste `TestNewLoggerFile` + smoke de boot sem `-verbose`.

**Pronto quando:** sem flag de log, INFO aparece no stderr (debug do MCP); com `-log`, vai para stderr + arquivo.
**Validação:** `go test ./cmd/ast-mcp` + smoke de boot sem `-verbose`.
**Commit:** `feat(log): log to stderr by default so MCP debug console shows index logs`

## L6 — Mensagens descritivas do servidor e da indexação — [PENDENTE]

*Como operador, quero cada linha de log como frase completa, com durações e tamanhos legíveis.*

- [x] L6.1 Helpers `humanDur` (`"15.5s"`/`"42ms"`) e `humanBytes` (`"14.2 MB"`/`"794 B"`/`"missing"` p/ -1), mais `plural`, em `internal/service/repo.go`.
- [ ] L6.2 Reescrever as mensagens (tabela abaixo).
- [ ] L6.3 `first_errors` só entra na linha quando há falhas.
- [ ] L6.4 Atualizar `TestRepoBuildLog`; smoke com `-log` conferindo as frases.

| Hoje | Depois |
|---|---|
| `started` | `ast-mcp dev started: tool timeout 30s, 9 languages (bash, csharp, go…), log at /tmp/ast.log` |
| `repo index started` | `indexing started for /repo (state: building, restored: false)` |
| `index built` | `index build finished in 1.4s: 1523 files, 0 failures; 14.2 MB index in RAM; 3.1 MB snapshot on disk (root=/repo)` |
| `index restored` | `index restored from snapshot in 2ms: 1523 files; snapshot 3.1 MB (root=/repo)` |
| `index refreshed` | `incremental refresh in 45ms: +1 added, 1 updated, 0 removed; 1523 files total, 0 failures (root=/repo)` |
| `snapshot unusable, full rebuild` | `snapshot unusable, full index build (reason: …)` |
| `snapshot save failed` (warn) | `failed to save snapshot: <erro> (root=/repo)` |
| `old index dropped` | `replaced previous index for /repo` |
| `delta exceeds threshold, full rebuild` | `delta of 612 files exceeded the limit of 500: full index build (root=/repo)` |
| `index dir scan failed` (warn) | `failed to scan directory: <erro> (root=/repo)` |

**Pronto quando:** nenhuma linha exige decodificar `duration_ms`/`mem_bytes`.
**Validação:** `go test ./internal/service -race` + smoke de boot com `-log`.
**Commit:** `feat(log): descriptive server and index messages`

## L7 — Logs descritivos de chamadas de tools — [DONE]

*Como operador, quero que cada `msg=tool` do debug diga qual consulta rodou, com quais argumentos e quanto demorou.*

- [x] L7.1 Wrapper `timed` (`internal/tools/timing.go`): `tool find_usages finished in 15.5s ({Mode:occurrences Name:walkFiles Path:/repo/internal …})`; erro vira `tool … failed in 12ms: <erro> (…)`. Args = `%+v` do input (sem tocar nos handlers); helper `humanDur` local; `tool` mantido como campo estruturado.
- [x] L7.2 Teste `TestTimedLogMessage` (`internal/tools/timing_test.go`, novo): buffer via `SetLogger`, asserta frase com tool + duração + args, nos casos ok e erro.
- [x] L7.3 Smoke via stdio: `tool outline_file finished in 0ms ({Language: Path:/tmp/smokerepo/main.go IncludeText:false})` no stderr.

**Pronto quando:** a linha responde "o quê, com o quê, quanto tempo".
**Validação:** `go test ./internal/tools -race` + smoke via stdio.
**Commit:** `feat(log): descriptive tool-call messages with args and duration`

---

## Pulados

- Contagem de resultados no log de tool (ex.: "42 matches") — exige tocar todos os handlers.
- Timing por arquivo indexado e logs por tick do watcher — disponíveis via `index_status`.

## L8 — Aviso de boot sem `-repo` — [DONE]

*Como operador, quero que o boot avise quando nenhum índice será carregado.*

- [x] L8.1 `main.go`: sem `-repo`, `WARN no -repo configured: server running fully on disk, no index will be built or loaded`.
- [x] L8.2 Smoke: boot sem `-repo` mostra o WARN no stderr.

**Pronto quando:** impossível subir sem índice sem ver o aviso no debug e no arquivo.
**Validação:** `go test ./cmd/...` + smoke.
**Commit:** `feat(log): warn at boot when no -repo is configured`

## Log

| História | Status | Commit | Validação |
|---|---|---|---|
| L5 | DONE | 6645f77 | `TestNewLoggerFile`; smoke sem `-verbose` no stderr e no arquivo |
| L6 | DONE | b855113 | `TestRepoBuildLog` + `-race`; smoke com frases em inglês no arquivo |
| L7 | DONE | — | `TestTimedLogMessage`; smoke via stdio com a frase no stderr |
