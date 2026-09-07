# Plano — Index-First sem `repo_id`

Status geral: EM ANDAMENTO
Branch: `feat/repo-mode`
Regra: uma história por commit. Gate por história: "Pronto quando" + validação + `go test ./...`, `go vet ./...`, `git diff --check` verdes.

Premissas adotadas:
1. Nenhuma flag `-repo` = nenhuma indexação. O servidor usa o disco.
2. Existe `index_status`. Não existem mais `index_repo`, `refresh_repo`, `drop_repo`, `search_repo`.
3. Flag repetível: `-repo /a -repo /b`.

---

## S1 — Métodos por caminho no serviço — [DOING]

- [x] T1.1 Registro interno `root → id` no `RepoService` (path normalizado com `filepath.Abs`).
- [x] T1.2 `ResolveIndex(path) (Info, bool)`: maior prefixo; só `state=ready`.
- [x] T1.3 Métodos por path: `ScanAt`, `FindOccurrencesAt`, `UnusedAt`, `CallersAt`, `DefinitionsAt`, `ComplexityAt`, `OutlineAt`, `AnalyzeAt`, `ImpactAt`, `CyclesAt`, `TopologyAt`.
- [x] T1.4 Testes: path igual ao root; subdiretório; fora do root; roots aninhados; `building` ignorado; drop remove root.

**Pronto quando:** métodos `*At` funcionam; handlers não mudaram; métodos por id intactos.
**Validação:** `go test ./internal/service -run TestRepo -v`
**Commit:** `feat(repo): path-based index resolution in service`

---

## S2 — Boot com `-repo`, cache e watch — [PENDING]

- [ ] T2.1 Flag repetível `-repo <dir>` (valida diretório existente).
- [ ] T2.2 Boot chama `Repo.Index` para cada `-repo`; sem flag, nada é indexado.
- [ ] T2.3 `-watch` default `true` (desliga com `-watch=false`).
- [ ] T2.4 `-watch-interval` default `2s`.
- [ ] T2.5 Save/restore de snapshot sempre ligados; `-cache-dir` só override.
- [ ] T2.6 Testes de flag (lista repetível; diretório inexistente).

**Pronto quando:** `-repo /a -repo /b` indexa ambos no boot; segundo boot restaura (`restored: true`); sem `-repo`, nada roda.
**Validação:** `go test ./cmd/... ./internal/service` + smoke de boot.
**Commit:** `feat(repo): -repo flag with auto-index, default-on watch and cache`

---

## S3 — Tools por path; `repo_id` removido (breaking) — [PENDING]

- [ ] T3.1 Remover campo `RepoID` dos inputs das tools de busca e grafo; rotear via `*At`.
- [ ] T3.2 Remover tools `index_repo`, `refresh_repo`, `drop_repo`, `search_repo`.
- [ ] T3.3 Criar tool `index_status` (sem argumentos; lista os repos).
- [ ] T3.4 `repo_impact`/`repo_cycles`/`repo_topology` usam `path`.
- [ ] T3.5 Descrições: índice automático quando o path está coberto.
- [ ] T3.6 Atualizar `tools_mcp_test.go` (13 tools) e `tools_contract_test.go`.

**Pronto quando:** `grep -r repo_id internal/tools internal/service` vazio; buscas dentro de `-repo` retornam `source: indexed`; `index_status` sem args.
**Validação:** `go test ./...` + smoke MCP com `-repo`.
**Commit:** `feat(repo)!: index-first tools, repo_id removed, index_status`

---

## S4 — Limpeza da API interna — [PENDING]

- [ ] T4.1 Tornar privados os métodos por id usados só pelas wrappers `*At`.
- [ ] T4.2 Remover `Refresh`/`Drop` públicos sem chamador (watch usa caminho interno).
- [ ] T4.3 Grep final: só `repoindex.Info.ID` interno e testes de store restam.
- [ ] T4.4 Sem funcionalidade nova.

**Pronto quando:** nenhuma API pública referencia id de repositório; suíte `-race` verde.
**Validação:** `go test ./internal/service ./internal/repoindex -race`
**Commit:** `refactor(repo): unexport id-based service API`

---

## S5 — Documentação final — [PENDING]

- [ ] T5.1 README: seção "Indexação automática"; tabela de 13 tools; remover exemplos `repo_id`/`search_repo`.
- [ ] T5.2 ARCHITECTURE: fluxo do boot, flags novas, regra disco-vs-índice.
- [ ] T5.3 Conferir docs contra o código (grep flags/tools).

**Pronto quando:** `grep "repo_id|index_repo|search_repo" README.md ARCHITECTURE.md` vazio; leitor novo usa `-repo` e nada mais.
**Validação:** grep de conferência.
**Commit:** `docs: index-first usage`

---

## Log

| História | Status | Commit | Validação |
|---|---|---|---|
| S1 | PENDING | — | — |
| S2 | PENDING | — | — |
| S3 | PENDING | — | — |
| S4 | PENDING | — | — |
| S5 | PENDING | — | — |
