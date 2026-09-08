# Plano — Logs completos de indexação

Status geral: L0 DONE; L1 em execução; L2–L4 pendentes.

**Regras:**
- Fluxo git: `main` → `git pull` → branch `feat/index-logging` → 1 commit por história.
- A história N só começa com a N-1 **commitada e aprovada** (diff mostrado antes de cada commit).
- Cada commit aprovado é pushado imediatamente; a branch é testável em outras máquinas via PR (aberto após L1).
- Todo log usa o `*slog.Logger` injetado (mesma instância que, com `-log`, escreve no arquivo via `io.MultiWriter`).
- Gate por história: *Pronto quando* + *Validação* + `go test ./...` e `go vet ./...` verdes.

---

## L0 — Setup: main atualizado + branch + PR — [DONE]

- [x] L0.1 `git checkout main` (o `java_memory_diag_test.go` não rastreado ficou local).
- [x] L0.2 `git pull origin main` (fast-forward p/ `4e58afc`).
- [x] L0.3 `git checkout -b feat/index-logging`.
- [x] L0.4 Plano salvo em `docs/PLAN-index-logs.md`.

**Validação:** `git log --oneline -1` = `4e58afc`; base com `go test ./...` verde.

## L1 — Infra de logging no serviço — [DONE]

*Enquanto dev, quero o logger do servidor dentro do `RepoService`, para que qualquer operação de índice possa logar.*

- [x] L1.1 Campo `logger *slog.Logger` em `RepoService` + setter `SetLogger` + helper `log()` com fallback p/ `slog.Default()` (`internal/service/repo.go`).
- [x] L1.2 `svcs.Repo.SetLogger(logger)` no boot (`cmd/ast-mcp/main.go`).
- [x] L1.3 Corrigir log enganoso `repo indexed` → `repo index started` (build é async).
- [x] L1.4 `newLogger`: com `-log`, `log.SetOutput(io.MultiWriter(os.Stderr, f))` — fatals de boot (std `log`) também caem no arquivo.
- [x] L1.5 Suíte verde sem mudança de comportamento.

**Pronto quando:** infraestrutura pronta; boot com `-log` grava tudo que grava hoje + fatals.
**Validação:** `go build ./... && go vet ./... && go test ./...`
**Commit:** `feat(repo): wire slog logger into RepoService`
**Pós-commit:** push da branch + `gh pr create` (alvo `main`).

## L2 — Resumo do build completo — [PENDENTE]

*Como operador, quero uma linha ao fim de cada build com duração, arquivos indexados, falhas e tamanho do índice.*

- [ ] L2.1 `build()`: `time.Now()`, capturar `Info` de `store.Replace`, logar `index built` com `root`, `duration_ms`, `files`, `failed`, `mem_bytes`, `snapshot_bytes` e até 3 primeiros erros.
- [ ] L2.2 Warn `index dir scan failed` com `root` + erro.
- [ ] L2.3 Teste: buffer logger, asserta campos de `index built`.

**Pronto quando:** todo build termina com 1 linha `index built` completa em stderr e no arquivo.
**Validação:** `go test ./internal/service -run TestRepoBuildLog -race` + smoke de boot com `-log`.
**Commit:** `feat(repo): log full build summary (duration, files, index size)`

## L3 — Logs de snapshot (persistência) — [PENDENTE]

*Como operador, quero visibilidade quando o snapshot é restaurado, invalidado ou falha ao gravar.*

- [ ] L3.1 `index restored`: `root`, `duration_ms`, `files`, `snapshot_bytes`.
- [ ] L3.2 `snapshot unusable, full rebuild` com motivo (erro de load ou cabeçalho inválido).
- [ ] L3.3 Warn `snapshot save failed` com `root` + erro.
- [ ] L3.4 `old index dropped` quando um root já indexado é reindexado.

**Pronto quando:** restore loga; invalidação loga o motivo; falha de gravação vira warn.
**Validação:** `go test ./internal/service -race` + smoke de 2 boots (build → restored).
**Commit:** `feat(repo): log snapshot restore, invalidation and save failures`

## L4 — Logs de refresh incremental — [PENDENTE]

*Como operador, quero um log a cada atualização incremental — e quando ela vira rebuild total.*

- [ ] L4.1 `index refreshed`: `root`, `duration_ms`, `added`, `updated`, `deleted`, `files`, `failed`, `mem_bytes`.
- [ ] L4.2 `delta exceeds threshold, full rebuild` com `added`, `changed`, `deleted`, `threshold`.

**Pronto quando:** delta loga `index refreshed`; acima do limiar loga o motivo e delega ao `build`.
**Validação:** `go test ./internal/service -race` + smoke: tocar arquivo com `-watch -log`.
**Commit:** `feat(repo): log incremental refresh and threshold rebuilds`

---

## Pulados

- Timing por arquivo e logs de ciclo do watcher por tick — disponíveis via `index_status`.
- `java_memory_diag_test.go` (não rastreado) fica fora do PR.

## Log

| História | Status | Commit | Validação |
|---|---|---|---|
| L0 | DONE | — | base `4e58afc` |
| L1 | DONE | — | — |
| L2 | PENDENTE | — | — |
| L3 | PENDENTE | — | — |
| L4 | PENDENTE | — | — |
