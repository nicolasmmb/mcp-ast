# Plano — Index-First sem `repo_id`

Status geral: S1–S5 CONCLUÍDO; R1 DONE; R2–R3 em andamento.

Nota S5: gate estrito confirmado — nomes antigos zerados de README/ARCHITECTURE (commit 5e8df4e).

---

## Follow-up — R1–R3

### R1 — Raiz duplicada remove o dono anterior — [DONE]

- [x] R1.1 `Index`: se `roots.Load(root)` existir, `s.drop(oldID)` antes do `Create`.
- [x] R1.2 `drop`: limpar `watchLangs`/`snapshotLangs`/`refreshLocks` do id.
- [x] R1.3 Testes: reindex da mesma raiz substitui (store sem id1, `List` 1 item, `ResolveIndex` → id2); mapas sem chave id1.

**Pronto quando:** `-repo /a -repo /a` = 1 repositório; watcher zumbi termina no próximo tick.
**Validação:** `go test ./internal/service -run TestRepoServiceReindex -race`
**Commit:** `fix(repo): drop previous index when a root is indexed again`

### R2 — Deletar `usages` sem chamador — [DONE]

- [x] R2.1 Deletar `usages` (repo.go).
- [x] R2.2 Migrar call sites: repo_test 110, 145, 153, 492, 553, 622; bench_test 179 → `findUsages(...).Matches` (7 sites, não 3 — o grep inicial subestimou).
- [x] R2.3 `startWatch`: remover `_ = info` morto.
- [x] R2.4 Grep final: `Repo.usages(` vazio em todo o repo.

**Commit:** `refactor(repo): remove unused usages helper`

### R3 — PR 13 descreve o modelo novo — [PENDING]

- [ ] R3.1 Corpo+título novos (breaking, 13 tools, flags, hashes, como testar).
- [ ] R3.2 `gh pr edit 13`.
- [ ] R3.3 Conferência: body sem `repo_id`/`index_repo`/`search_repo`/`17 tools`.
- [ ] R3.4 Push do branch.
Branch: `feat/repo-mode`
Regra: uma história por commit. Gate por história: "Pronto quando" + validação + `go test ./...`, `go vet ./...`, `git diff --check` verdes.

Premissas adotadas:
1. Nenhuma flag `-repo` = nenhuma indexação. O servidor usa o disco.
2. Existe `index_status`. Não existem mais `index_repo`, `refresh_repo`, `drop_repo`, `search_repo`.
3. Flag repetível: `-repo /a -repo /b`.

---

## S1 — Métodos por caminho no serviço — [DONE]

- [x] T1.1 Registro interno `root → id` no `RepoService` (path normalizado com `filepath.Abs`).
- [x] T1.2 `ResolveIndex(path) (Info, bool)`: maior prefixo; só `state=ready`.
- [x] T1.3 Métodos por path: `ScanAt`, `FindOccurrencesAt`, `UnusedAt`, `CallersAt`, `DefinitionsAt`, `ComplexityAt`, `OutlineAt`, `AnalyzeAt`, `ImpactAt`, `CyclesAt`, `TopologyAt`.
- [x] T1.4 Testes: path igual ao root; subdiretório; fora do root; roots aninhados; `building` ignorado; drop remove root.

**Pronto quando:** métodos `*At` funcionam; handlers não mudaram; métodos por id intactos.
**Validação:** `go test ./internal/service -run TestRepo -v`
**Commit:** `feat(repo): path-based index resolution in service`

---

## S2 — Boot com `-repo`, cache e watch — [DONE]

- [x] T2.1 Flag repetível `-repo <dir>` (valida diretório existente).
- [x] T2.2 Boot chama `Repo.Index` para cada `-repo`; sem flag, nada é indexado.
- [x] T2.3 `-watch` default `true` (desliga com `-watch=false`).
- [x] T2.4 `-watch-interval` default `2s`.
- [x] T2.5 Save/restore de snapshot sempre ligados; `-cache-dir` só override.
- [x] T2.6 Testes de flag (lista repetível; diretório inexistente).

**Pronto quando:** `-repo /a -repo /b` indexa ambos no boot; segundo boot restaura (`restored: true`); sem `-repo`, nada roda.
**Validação:** `go test ./cmd/... ./internal/service` + smoke de boot.
**Commit:** `feat(repo): -repo flag with auto-index, default-on watch and cache`

---

## S3 — Tools por path; `repo_id` removido (breaking) — [DONE]

- [x] T3.1 Remover campo `RepoID` dos inputs das tools de busca e grafo; rotear via `*At`.
- [x] T3.2 Remover tools `index_repo`, `refresh_repo`, `drop_repo`, `search_repo` (+ `repo_status`).
- [x] T3.3 Criar tool `index_status` (sem argumentos; lista os repos via `RepoService.List`).
- [x] T3.4 `repo_impact`/`repo_cycles`/`repo_topology` usam `path`.
- [x] T3.5 Descrições: índice automático quando o path está coberto.
- [x] T3.6 Atualizar `tools_mcp_test.go` (13 tools).

**Pronto quando:** `grep -r repo_id internal/tools internal/service` vazio; buscas dentro de `-repo` retornam `source: indexed`; `index_status` sem args.
**Validação:** `go test ./...` + smoke MCP com `-repo`.
**Commit:** `feat(repo)!: index-first tools, repo_id removed, index_status`

---

## S4 — Limpeza da API interna — [DONE]

- [x] T4.1 Tornar privados os métodos por id usados só pelas wrappers `*At` e testes.
- [x] T4.2 `refresh`/`drop`/`status` privados; watch usa caminho interno.
- [x] T4.3 Grep final: `repo_id` só existia no tag JSON de `Info.ID` → tag `json:"-"` (id nunca exposto a clientes).
- [x] T4.4 Sem funcionalidade nova.

**Pronto quando:** nenhuma API pública referencia id de repositório; suíte `-race` verde.
**Validação:** `go test ./internal/service ./internal/repoindex -race`
**Commit:** `refactor(repo): unexport id-based service API`

---

## S5 — Documentação final — [DONE]

- [x] T5.1 README: seção "Indexação automática"; tabela de 13 tools; exemplos sem identificador de repo; config MCP com `-repo`.
- [x] T5.2 ARCHITECTURE: fluxo do boot, resolução por path, tabela de 13 tools, flags novas, benchmarks renomeados.
- [x] T5.3 Conferir docs contra o código: 9 flags e 13 tools confirmadas por grep.

**Pronto quando:** `grep "repo_id|index_repo|search_repo|repo_status|refresh_repo|drop_repo" README.md ARCHITECTURE.md` vazio; leitor novo usa `-repo` e nada mais.
**Validação:** grep de conferência (flags/tools vs. código).
**Commit:** `docs: index-first usage`

---

## Log

| História | Status | Commit | Validação |
|---|---|---|---|
| S1 | DONE | 422754f | `go test ./...`, `go vet`, gates verdes |
| S2 | DONE | 84e870f | testes + smoke de boot: `state=ready restored=true` |
| S3 | DONE | db7a30c | grep `repo_id` vazio; `go test ./...` verde; smoke MCP: `source=indexed` com `-repo`, disco puro sem |
| S4 | DONE | 9ddfc71 | `go test -race` verde; grep `repo_id`/`RepoID` vazio em todo `internal/` |
| S5 | DONE | a0e341e (+ref) | grep de nomes antigos vazio nas docs; 9 flags + 13 tools conferidas |
