# Plano — Validação completa do fluxo mcp-ast

Status geral: V0 DONE; V1–V7 pendentes. Branch: `feat/index-logging` (PR #14).

**Regras:**
- Fluxo git: `main` → `git pull` → branch `feat/index-logging` → 1 commit por história.
- A história N só começa com a N-1 **commitada e aprovada** (diff mostrado antes de cada commit).
- Cada commit aprovado é pushado na hora (PR #14 atualiza sozinho).
- Gate por história: *Pronto quando* + *Validação* + `go test ./...` e `go vet ./...` verdes.
- Idioma dos commits: **pt-BR**.
- Idioma dos logs do servidor: **inglês** (já implementado em L5–L8).

---

## V0 — Setup: binário local com versão e commit — [DONE]

*Como operador, quero um binário local com versão `pr-14-{commit}` para validar o boot completo.*

- [x] V0.1 Build com ldflags: `go build -ldflags "-X main.version=pr-14 -X main.commit=$(git rev-parse --short HEAD) -s -w" -o ast-mcp ./cmd/ast-mcp`
- [x] V0.2 Smoke: `./ast-mcp -version` → saída `ast-mcp pr-14-5f3f01e`
- [x] V0.3 Smoke: `./ast-mcp -repo /tmp/ast-test-repo -verbose -log ./test-v0.log` → boot sem erro, WARN não aparece
- [x] V0.4 Cleanup: `rm -f ./ast-mcp ./test-v0.log`

**Pronto quando:** binário local criado, `-version` mostra `pr-14-{commit}`, boot com `-repo .` sem WARN.
**Validação:** `./ast-mcp -version` + `go test ./...` + `go vet ./...`
**Commit:** `test: build local binary with version pr-14-{commit}`

**Resultado:**
- 1º boot: `index build finished in 1ms: 1 file, 0 failures; 985 B index in RAM; 1.8 KB snapshot on disk`
- 2º boot: `index restored from snapshot in 0ms: 1 file; snapshot 1.8 KB`

---

## V1 — Handshake MCP e descoberta de tools — [PENDENTE]

*Como operador, quero que o handshake MCP retorne as 13 tools e o estado do índice.*

- [ ] V1.1 Iniciar servidor com `-repo /tmp/ast-test-repo` via stdio JSON-RPC
- [ ] V1.2 Enviar `initialize` + `notifications/initialized`
- [ ] V1.3 Enviar `tools/list` (id=2)
- [ ] V1.4 Validar: 13 tools listadas (nomes idênticos a `Register()`)
- [ ] V1.5 Enviar `tools/call index_status` (id=3)
- [ ] V1.6 Validar: `repos[0].root` = path absoluto, `repos[0].state` = "ready", `repos[0].files_indexed` > 0, `repos[0].watch` = true

**Pronto quando:** handshake completo, 13 tools, index_status retorna repo pronto.
**Validação:** `go test ./internal/tools -run TestMCP -race` + smoke via stdio
**Commit:** `test: validate MCP handshake and tool discovery with -repo`

---

## V2 — Validação de indexação (build vs snapshot) — [PENDENTE]

*Como operador, quero confirmar que a indexação gera arquivos, RAM e snapshot.*

- [ ] V2.1 Primeira execução: STDERR mostra `index build finished in X: N files, M failures; X RAM; Y snapshot`
- [ ] V2.2 Verificar arquivo de log: linha `index build finished` com files > 0, RAM > 0, snapshot > 0
- [ ] V2.3 Verificar snapshot em disco: `ls ~/.cache/ast-mcp/repo-*.gob` ou `-cache-dir` → arquivo existe
- [ ] V2.4 Segunda execução: STDERR mostra `index restored from snapshot in Xms: N files; snapshot Y`
- [ ] V2.5 Verificar background refresh: após restore, log mostra `incremental refresh in Xms` (ou delta zero = no-op)

**Pronto quando:** 1º boot = build completo; 2º boot = restore do snapshot; snapshot persiste em disco.
**Validação:** `go test ./internal/service -run TestRepoServiceRestoreOnBoot -race`
**Commit:** `test: validate index build, snapshot save, and restore on boot`

---

## V3 — Performance: tempo de resposta indexado vs disco — [PENDENTE]

*Como operador, quero medir o tempo de resposta de queries indexadas vs disco.*

- [ ] V3.1 Tool `find_usages` (mode=occurrences, name=Helper, path=/tmp/ast-test-repo): medir `elapsed_ms`, validar < 1s
- [ ] V3.2 Tool `scan_symbols` (path=/tmp/ast-test-repo): medir `elapsed_ms`, validar < 1s
- [ ] V3.3 Tool `outline_file` (path=/tmp/ast-test-repo/main.go): medir `elapsed_ms`, validar < 500ms
- [ ] V3.4 Tool `analyze_file` (path=/tmp/ast-test-repo/main.go): medir `elapsed_ms`, validar < 2s
- [ ] V3.5 Logs em stderr: cada tool mostra `tool X finished in Yms ({args})`
- [ ] V3.6 Todos os resultados têm `source: indexed`

**Pronto quando:** todas as queries indexadas < 1s; logs mostram args e duração.
**Validação:** `go test ./internal/tools -run TestTimed -race` + smoke com timing
**Commit:** `test: validate indexed query response times and tool-call logs`

---

## V4 — Diagnóstico do problema `-repo` não chega ao servidor — [PENDENTE]

*Como operador, quero diagnosticar por que o `-repo` configurado no cliente não é recebido pelo servidor.*

- [ ] V4.1 Analisar `opencode.json` do repo: `command: ["ast-mcp"]` — sem args → fonte do WARN
- [ ] V4.2 Verificar se `os.Args` recebe os flags (logar `os.Args` no boot)
- [ ] V4.3 Identificar a causa: entrada duplicada (`user-ast-mcp` vs `ast-mcp`), cliente lê arquivo errado, ou processo antigo
- [ ] V4.4 Implementar `L9`: logar `os.Args` no boot em Debug level para diagnóstico futuro
- [ ] V4.5 Documentar correção: qual arquivo de config o cliente realmente lê
- [ ] V4.6 Testar com args corretos: WARN não aparece

**Pronto quando:** causa identificada; `L9` implementado; correção documentada.
**Validação:** `go test ./...` + `go vet ./...` + smoke com args corretos
**Commit:** `feat(log): log os.Args at boot for diagnostics (L9)`

---

## V5 — Snapshot: invalidação e persistência — [PENDENTE]

*Como operador, quero que o snapshot seja invalidado corretamente quando a versão muda.*

- [ ] V5.1 Boot com versão `pr-14`: build + snapshot salvo
- [ ] V5.2 Mudar `main.version` para `pr-15` (simular upgrade): snapshot deve ser invalidado
- [ ] V5.3 Boot com `pr-15`: WARN `snapshot expired, full index build` → rebuild completo
- [ ] V5.4 Boot com `pr-14` novamente: restore do snapshot antigo (se still válido)
- [ ] V5.5 Verificar integridade: `find_usages` após restore retorna dados corretos

**Pronto quando:** upgrade de versão invalida snapshot; downgrade restaura; dados íntegros.
**Validação:** `go test ./internal/service -run TestRepoServiceRestore -race`
**Commit:** `test: validate snapshot invalidation on version change`

---

## V6 — Diagrama de fluxo completo — [PENDENTE]

*Como documentador, quero um diagrama visual do fluxo mcp-ast para referência futura.*

- [ ] V6.1 Criar `docs/FLOW-DIAGRAM.md` com diagramas Mermaid
- [ ] V6.2 Diagrama 1: Boot Flow (main → flags → logger → index → tools → server.Run)
- [ ] V6.3 Diagrama 2: Index Build Flow (build → IndexDir → loadIndexed → store.Replace → saveSnapshot)
- [ ] V6.4 Diagrama 3: Tool Call Flow (MCP → timed wrapper → ResolveIndex → handler → response)
- [ ] V6.5 Diagrama 4: Refresh Flow (watcher → diff → incremental/rebuild → saveSnapshot)
- [ ] V6.6 Diagrama 5: Snapshot Flow (LoadSnapshot → valid? → restore/build → saveSnapshot)
- [ ] V6.7 Validar que o diagrama reflete o código atual

**Pronto quando:** diagrama completo, rendering Mermaid correto, reflete código atual.
**Validação:** Mermaid syntax check
**Commit:** `docs: add complete flow diagram (Mermaid)`

---

## Log

| História | Status | Commit | Validação |
|---|---|---|---|
| V0 | DONE | (este commit) | `./ast-mcp -version` + boot sem WARN + `go test ./...` |
| V1 | PENDENTE | — | `TestMCP` + smoke stdio: 13 tools, index_status |
| V2 | PENDENTE | — | `TestRepoServiceRestoreOnBoot` + smoke 2 boots |
| V3 | PENDENTE | — | `TestTimed` + smoke: elapsed_ms < 1s |
| V4 | PENDENTE | — | `L9` + diagnóstico documentado |
| V5 | PENDENTE | — | `TestRepoServiceRestore` + smoke upgrade/downgrade |
| V6 | PENDENTE | — | Mermaid syntax check |
