# Plano: Otimização de Memória do ast-mcp

**Problema:** Processo `ast-mcp -repo` consome 42GB de RAM em repos Java grandes (10k+ arquivos).
**Meta:** Reduzir consumo para <8GB em repos de 50k arquivos, mantendo funcionalidade.

---

## Diagnóstico Atual

### Causas Raiz Identificadas (fatos)

| # | Causa | Impacto | Localização |
|---|-------|---------|-------------|
| 1 | `UsageRef` armazenado **3x** em maps separados (usages + fileUsages + byCanonical) | **Crítico** | `store.go:139-141`, `insert()` :622, `enrich()` :658 |
| 2 | `FileIndex.Usages` (raw) mantido em `IndexedFile` junto com postings internados | **Alto** | `index.go:26`, `insert()` lê mas não libera após |
| 3 | `estimateMemory()` não contabiliza os postings maps, nem graphs | **Diagnóstico** | `store.go:704-713` |
| 4 | `buildCallGraph`/`buildImportGraph` reconstruídos do zero a cada `Apply`/`Replace` | **Moderado** | `graph.go:38,103`, chamados em `store.go:293-294,347-348` |
| 5 | `filesByPath()` aloca map completo N vezes por operação | **Moderado** | `graph.go:370-376` |
| 6 | `saveSnapshot` via `Meta()` copia todo o map de IndexedFiles | **Moderado** | `repo.go` (chama `store.Meta()`) |

### Benchmark Real Atual

- `BenchmarkIndex50k` existe mas **não** chama `b.ReportAllocs()`
- `TestJava10kMemoryProfile` existe mas é gating `AST_MEM_DIAG=1`, sem assertions
- **Nenhum** teste mede `estimateMemory` vs `runtime.MemStats`

---

## Estratégia

Uma história = um tipo de melhoria. Cada história:
1. **Antes**: benchmark/profile que demonstra o problema (fatos)
2. **Implementação**: mudança única e focada
3. **Depois**: mesmo benchmark/profile confirma a melhoria (fatos)
4. **Commit**: uma mensagem clara do que mudou

---

## Épico: Fundação de Medição

### História 1: Criar benchmark de memória com profiling

**Objetivo:** Ter métricas reais antes de qualquer otimização.

**Tasks:**
- [ ] Criar `internal/repoindex/bench_memory_test.go`
- [ ] Implementar `BenchmarkInsertAllocs` que mede alocação do `insert()` com 1k, 10k, 50k arquivos sintéticos
- [ ] Implementar `BenchmarkEnrichAllocs` que mede alocação do `enrich()`
- [ ] Implementar `BenchmarkBuildGraphsAllocs` que mede `buildCallGraph` + `buildImportGraph`
- [ ] Implementar `BenchmarkSnapshotSaveLoad` que mede serialize/deserialize
- [ ] Todos os benchmarks devem chamar `b.ReportAllocs()` e `b.ReportMetric(b.BytesAllocatedPerOp(), "bytes/op")`
- [ ] Criar helper `makeRepo(n int) *repo` que gera N `IndexedFile` com usages realistas (nomes, calls, imports)

**Subtasks:**
- Gerar dados sintéticos que espelham padrão Java: 10 imports/arquivo, 20 métodos/arquivo, 5 usages/método
- Cada `UsageMatch` com `Text` de 50-200 bytes (primeira linha do pai, como no real)
- Medir `runtime.MemStats` antes/depois de cada operação com `runtime.GC()` forçado

**Done-when:**
- `go test -bench=BenchmarkInsertAllocs -benchmem -count=3` roda sem erro
- Output mostra `B/op` e `allocs/op` para cada tamanho
- `go test -bench=. -benchmem -run=^$ -cpuprofile=cpu.prof -memprofile=mem.prof` gera profiles

**Próximo:** Usar esses profiles pra identificar os top-3 hotspots de alocação.

---

### História 2: Fix `estimateMemory` para refletir realidade

**Objetivo:** O campo `MemoryBytes` no `Info` deve ser confiável.

**Tasks:**
- [ ] Atualizar `estimateMemory()` em `store.go:704` para incluir:
  - Overhead de map buckets (~50 bytes/entry) para `usages`, `fileUsages`, `byCanonical`
  - Tamanho dos slices `[]UsageRef` em cada posting
  - Tamanho de `CallGraph` e `ImportGraph`
  - Tamanho de `complexity` slice
- [ ] Criar `TestEstimateMemoryVsActual` que compara `estimateMemory` com `runtime.MemStats.HeapAlloc`
- [ ] O teste deve rodar com 1k e 10k arquivos e assert que estimativa está dentro de 2x do real

**Subtasks:**
- Criar função `measureHeap() int64` que faz `runtime.GC()` + `runtime.ReadMemStats`
- Teste deve isolar a diferença: medir heap antes do insert, depois, delta = real

**Done-when:**
- `TestEstimateMemoryVsActual` passa
- `MemoryBytes` reportado está dentro de 2x do heap real (não mais 5-10x)

**Próximo:** Com estimativa confiável, podemos medir impacto real de cada otimização.

---

## Épico: Eliminar Redundância de Dados

### História 3: Eliminar `fileUsages` espelho (3x → 2x)

**Objetivo:** Cada `UsageRef` deve existir em 2 maps (não 3).

**Análise:**
- `fileUsages[FileID][NameID]` existe pra O(1) no `remove()`
- `remove()`它它它 iterate `fileUsages[fid]` pra encontrar quais `NameID` pertencem ao file
- Alternativa: guardar `[]NameID` por file ( leve) e usar `usages[nameID][fid]` direto

**Tasks:**
- [ ] Criar campo `fileUsageNames map[FileID][]NameID` (slice leve de IDs)
- [ ] Modificar `insert()` para popular `fileUsageNames` e parar de popular `fileUsages`
- [ ] Modificar `remove()` para iterar `fileUsageNames[fid]` e fazer `delete(r.usages[nameID], fid)`
- [ ] Remover campo `fileUsages` do struct `repo`
- [ ] Benchmark antes vs depois: `BenchmarkInsertAllocs` + `BenchmarkRemoveAllocs`

**Subtasks:**
- Garantir que `remove()` existente continua correto (testes existentes passam)
- Criar `TestRemoveConsistency` que insere N files, remove 1, verifica postings corretos

**Done-when:**
- `fileUsages` não existe mais no struct
- `BenchmarkInsertAllocs` mostra redução de ~30% em `B/op`
- Todos os testes existentes passam
- `TestRemoveConsistency` passa

**Próximo:** Eliminar redundância seguinte.

---

### História 4: Nil out `FileIndex.Usages` após `insert()`

**Objetivo:** Liberar os `UsageMatch` raw após internação.

**Análise:**
- `facts.Usages []UsageMatch` é lido apenas em `insert()` (`store.go:626`)
- Após insert, dados vivem em `r.usages` (postings internados)
- Cada `UsageMatch` tem: Name, Canonical, Caller, Text, Kind, File, Line, Col (~200 bytes)
- Para 50k arquivos × 1000 usages/arquivo = 50M entries × 200 bytes = **10GB** liberados

**Tasks:**
- [ ] Ao final de `insert()`, fazer `f.Facts.Usages = nil`
- [ ] Verificar que nenhum código lê `Facts.Usages` após `insert()` (grep needed)
- [ ] Ajustar `estimateFile()` para não contar usages após nil (ou contar só antes)
- [ ] Benchmark: `BenchmarkInsertAllocs` antes vs depois

**Subtasks:**
- Grep em `store.go`, `graph.go`, `snapshot.go` por `facts.Usages` ou `f.Facts.Usages`
- Verificar que `buildCallGraph` e `buildImportGraph` usam `f.Facts.Usages` — se sim, precisa rodar ANTES do nil

**Done-when:**
- `f.Facts.Usages == nil` após toda chamada a `insert()`
- `BenchmarkInsertAllocs` mostra redução em `B/op` (esperado: ~10-15%)
- Todos os testes passam

**Problema identificado:** `buildCallGraph` (`graph.go:64`) lê `f.Facts.Usages`. Precisa rodar ANTES de nil out, ou refatorar para ler dos postings internados.

**Próximo:** Resolver conflito entre nil out e graph build.

---

### História 5: Construir graphs a partir dos postings internados

**Objetivo:** `buildCallGraph` e `buildImportGraph` devem ler de `r.usages`/`r.files`, não de `f.Facts.Usages`.

**Análise:**
- `buildCallGraph` itera `f.Facts.Usages` filtrando `kind == "call-site"`
- Pode ser reimplementado iterando `r.usages` e filtrando por `kindOf(ref.Kind) == call-site`
- `buildImportGraph` itera `f.Facts.Symbols["imports"]` — esse campo NÃO é nil out, então OK

**Tasks:**
- [ ] Reescrever `buildCallGraph` para aceitar `*repo` ao invés de `map[string]*IndexedFile`
- [ ] Implementar `buildCallGraphFromRepo(r *repo) CallGraph` que itera `r.usages`
- [ ] Manter `buildCallGraph` antigo como deprecated para testes de equivalência
- [ ] Criar `TestCallGraphEquivalence` que compara output antigo vs novo
- [ ] Trocar chamada em `Replace()` e `Apply()` para a nova versão
- [ ] Agora sim, nil out `f.Facts.Usages` em `insert()`
- [ ] Benchmark antes vs depois

**Subtasks:**
- `buildCallGraphFromRepo`: iterar `r.usages[nameID]`, para cada `byFile[fid]`, filtrar refs com `kindOf(ref.Kind) == "call-site"`, montar `agg` com `r.nameOf(nameID)` e `r.filePath(fid)`
- `buildImportGraph` não precisa mudar (lê `Symbols`, não `Usages`)

**Done-when:**
- `TestCallGraphEquivalence` passa (output antigo == novo para mesmo input)
- `f.Facts.Usages` é nil após insert
- `BenchmarkBuildGraphsAllocs` mostra redução (esperado: graph build não muda, mas heap total cai)

**Próximo:** Otimizar `byCanonical`.

---

### História 6: Otimizar `byCanonical` — não copiar UsageRefs

**Objetivo:** `byCanonical` deve referenciar, não copiar, os postings existentes.

**Análise:**
- `enrich()` linha 699: `c[fid] = append(c[fid], refs...)` — copia todos os `UsageRef`
- Para nomes com 1 declaração (unambiguous), `byCanonical[canonicalID]` contém cópia integral
- Alternativa: armazenar `[]NameID` por canonical, e resolver via `r.usages` no query time

**Tasks:**
- [ ] Criar `byCanonicalIndex map[NameID][]NameID` (canon → lista de nomes unambiguous)
- [ ] Modificar `enrich()` para popular `byCanonicalIndex` ao invés de copiar UsageRefs
- [ ] Adaptar queries que leem `byCanonical` para resolver via index + `r.usages`
- [ ] Remover campo `byCanonical map[NameID]map[FileID][]UsageRef`
- [ ] Benchmark antes vs depois

**Subtasks:**
- Identificar todas as queries que usam `byCanonical` (grep)
- Garantir que resolução via index retorna mesmos resultados

**Done-when:**
- `byCanonical` (map de UsageRef) não existe mais
- `BenchmarkEnrichAllocs` mostra redução de ~20-30%
- Todos os testes passam

**Próximo:** Otimizar graph rebuilds.

---

## Épico: Reduzir Alocação Temporária

### História 7: `filesByPath()` lazy ou cacheada

**Objetivo:** Evitar alloc de map completo N vezes por operação.

**Tasks:**
- [ ] Tornar `filesByPath()` um campo cacheado no `repo` (invalidado em `Replace`/`Apply`)
- [ ] Ou: passar `r.files` + `r.paths` diretamente pras funções de graph
- [ ] Benchmark: `BenchmarkBuildGraphsAllocs` antes vs depois

**Done-when:**
- `filesByPath()` não aloca novo map a cada chamada
- ou graph builders aceitam `*repo` diretamente

---

### História 8: Graph rebuild incremental no `Apply()`

**Objetivo:** `Apply()` com 1 arquivo alterado não deve reconstruir grafos inteiros.

**Tasks:**
- [ ] Medir com benchmark: `BenchmarkApplyOnePercent50k` com profiling
- [ ] Implementar `applyCallGraphDelta(r *repo, changes ChangeSet)` que:
  - Remove arestas dos arquivos deletados/modificados
  - Adiciona arestas dos arquivos adicionados/modificados
- [ ] Implementar `applyImportGraphDelta` similar
- [ ] Manter `buildFull` como fallback
- [ ] Benchmark antes vs depois

**Done-when:**
- `BenchmarkApplyOnePercent50k` mostra redução de ≥50% em alocação
- Testes de equivalência passam

---

### História 9: `fileDigest` streaming

**Objetivo:** Não ler arquivo inteiro em memória pra calcular SHA256.

**Tasks:**
- [ ] Trocar `os.ReadFile(p)` + `sha256.Sum256(data)` por `sha256.New()` + `io.Copy`
- [ ] Criar `BenchmarkFileDigest` antes vs depois

**Done-when:**
- `BenchmarkFileDigest` mostra redução de alocação
- Nenhum `os.ReadFile` no path de digest

---

## Épico: Snapshot Eficiente

### História 10: Snapshot incremental

**Objetivo:** Salvar snapshot sem copiar todo o map de IndexedFiles.

**Tasks:**
- [ ] Medir pico de memória durante `saveSnapshot` com profiling
- [ ] Implementar `SaveSnapshotIncremental`: serializar diretamente de `r.files` sem criar cópia via `Meta()`
- [ ] Ou: streaming gob encoder que itera `r.files` sem materializar map completo
- [ ] Benchmark antes vs depois

**Done-when:**
- Pico de memória durante save é <2x o steady state (antes era ~2x)

---

## Resumo de Impacto Esperado

| História | Impacto Memória | Esforço |
|----------|-----------------|---------|
| 1. Benchmark profiling | Diagnóstico | Baixo |
| 2. Fix estimateMemory | Diagnóstico | Baixo |
| 3. Eliminar fileUsages | **-30%** | Médio |
| 4. Nil out FileIndex.Usages | **-15%** | Baixo |
| 5. Graphs via postings | Habilita #4 | Médio |
| 6. Otimizar byCanonical | **-10-15%** | Médio |
| 7. filesByPath lazy | -5% transiente | Baixo |
| 8. Graph incremental | **-50% no Apply** | Alto |
| 9. fileDigest streaming | -5% pico | Baixo |
| 10. Snapshot incremental | -50% pico no save | Médio |

**Total estimado:** 42GB → ~12-15GB (histórias 3-6) → <8GB (com snapshot + incremental)

---

## Ordem de Execução

```
1 → 2 → 3 → 4 → 5 → 6 → 7 → 8 → 9 → 10
     fundação   redundância   alloc    snapshot
```

Cada história é um PR separado. Cada PR tem:
- Benchmark/profile ANTES (commit anterior)
- Implementação
- Benchmark/profile DEPOIS
- Comparação no PR description

---

## Próximo Passo

Começar pela **História 1**: criar benchmark de memória com profiling.
Depois rodar pra obter baseline, e só então implementar História 2.
