# Diagrama de Fluxo — mcp-ast

Fluxos completos do servidor MCP com indexação de repositório.

---

## 1. Boot Flow

```mermaid
flowchart TD
    A[main.go] --> B[flag.Parse]
    B --> C{showVersion?}
    C -- sim --> D[print version + exit]
    C -- nao --> E[validateRepoDirs]
    E --> F{dirs validas?}
    F -- nao --> G[log.Fatalf]
    F -- sim --> H[newLogger verbose logPath]
    H --> I[lang.Register 9 linguagens]
    I --> J[logger.Info ast-mcp started]
    J --> K[mcp.NewServer]
    K --> L[service.NewWithStore engine memoryStore]
    L --> M[svcs.Repo.SetLogger]
    M --> N{tem cacheDir?}
    N -- sim --> O[svcs.Repo.SetCacheDir]
    N -- nao --> P{tem watch?}
    O --> P
    P --> Q[FOR EACH repoDir]
    Q --> R[svcs.Repo.Index ctx dir nil]
    R --> S{info.State?}
    S -- ready + restored --> T[logger.Info index ready]
    S -- building --> U[logger.Warn index building]
    S -- outro --> V[logger.Info index status]
    T --> W{tem mais dirs?}
    U --> W
    V --> W
    W -- sim --> Q
    W -- nao --> X{len repoDirs == 0?}
    X -- sim --> Y[logger.Warn no -repo configured]
    X -- nao --> Z[tools.Register]
    Y --> Z
    Z --> AA[server.Run StdioTransport]
```

---

## 2. Index Build Flow (background goroutine)

```mermaid
flowchart TD
    A[RepoService.build] --> B[start := time.Now]
    B --> C[FOR EACH language filter]
    C --> D[engine.IndexDir ctx root lang]
    D --> E{err?}
    E -- sim --> F[errs root = err.Error]
    E -- nao --> G[FOR EACH path fact]
    G --> H[loadIndexed path facts errs]
    H --> I{facts != nil?}
    I -- sim --> J[files path = indexed]
    I -- nao --> K[skip]
    J --> L{mais paths?}
    K --> L
    L -- sim --> G
    L -- nao --> M{mais filters?}
    M -- sim --> C
    M -- nao --> N[maps.Clone errs]
    N --> O[store.Replace id files errs]
    O --> P[saveSnapshot id]
    P --> Q{len errs > 0?}
    Q -- sim --> R[logger.Info build finished + first_errors]
    Q -- nao --> S[logger.Info build finished]
    R --> T[FIM]
    S --> T
```

---

## 3. Tool Call Flow

```mermaid
flowchart TD
    A[MCP stdio] --> B[server.Run dispatch]
    B --> C[timed wrapper]
    C --> D{toolTimeout > 0?}
    D -- sim --> E[context.WithTimeout]
    D -- nao --> F[pass]
    E --> G[start := time.Now]
    F --> G
    G --> H[handler ctx req in]
    H --> I[ResolveIndex path]
    I --> J{index found + ready?}
    J -- sim --> K[usar dados do indice]
    J -- nao --> L[disk scan fallback]
    K --> M[service method]
    L --> M
    M --> N[out.SetElapsedMS ms]
    N --> O{err?}
    O -- sim --> P[logger.Error tool failed]
    O -- nao --> Q[logger.Info tool finished]
    P --> R[return res out err]
    Q --> R
    R --> S[JSON-RPC response stdout]
```

---

## 4. Refresh Flow (watcher polling)

```mermaid
flowchart TD
    A[startWatch] --> B[ticker 2s default]
    B --> C{repo still exists?}
    C -- nao --> D[return]
    C -- sim --> E[refresh ctx id langs]
    E --> F[TryLock]
    F --> G{lock acquired?}
    G -- nao --> H[return refreshing]
    G -- sim --> I[diff root filters meta]
    I --> J[classify changed added deleted]
    J --> K{delta > threshold?}
    K -- sim --> L[logger.Info delta exceeded]
    L --> M[go build full rebuild]
    K -- nao --> N{delta == 0?}
    N -- sim --> O[return no-op]
    N -- nao --> P[FOR EACH added changed]
    P --> Q[indexPath meta]
    Q --> R{err?}
    R -- errUnstable --> S[cs.Errors]
    R -- outro --> T[cs.Deleted]
    R -- ok --> U[cs.Added ou cs.Updated]
    S --> V{mais arquivos?}
    T --> V
    U --> V
    V -- sim --> P
    V -- nao --> W[store.Apply cs]
    W --> X[saveSnapshot id]
    X --> Y[logger.Info incremental refresh]
```

---

## 5. Snapshot Flow

```mermaid
flowchart TD
    A[Index root] --> B[snapshotPath root]
    B --> C[toolVersion != empty?]
    C -- nao --> D[skip snapshot]
    C -- sim --> E[LoadSnapshot path]
    E --> F{err?}
    F -- IsNotExist --> G[logger.Debug no snapshot]
    F -- corrupt --> H[logger.Warn snapshot corrupt]
    F -- ok --> I{header valid?}
    I -- nao --> J[logger.Warn snapshot expired]
    I -- sim --> K{store.Replace ok?}
    K -- nao --> L[logger.Warn snapshot corrupt]
    K -- sim --> M[store.SetCache]
    M --> N[logger.Info index restored]
    N --> O[go refresh background]
    O --> P[return info nil]
    G --> Q[go build background]
    H --> Q
    J --> Q
    L --> Q
    Q --> R[return info nil]
    D --> Q

    S[saveSnapshot id] --> T{toolVersion != empty?}
    T -- nao --> U[return]
    T -- sim --> V[store.Info + store.Meta]
    V --> W[snapshotMu.Lock]
    W --> X[repoindex.SaveSnapshot]
    X --> Y{err?}
    Y -- sim --> Z[logger.Warn save failed]
    Y -- nao --> AA[ok]
    Z --> AB[snapshotMu.Unlock]
    AA --> AB
```

---

## 6. Path Resolution Flow

```mermaid
flowchart TD
    A[ResolveIndex path] --> B[filepath.Abs path]
    B --> C[filepath.Clean abs]
    C --> D[bestRoot = empty]
    D --> E[FOR EACH root in roots]
    E --> F{abs == root OR hasPrefix?}
    F -- sim --> G{len root > len bestRoot?}
    G -- sim --> H[bestRoot = root, bestID = id]
    G -- nao --> I[skip]
    F -- nao --> I
    H --> E
    I --> E
    E --> J{bestID == empty?}
    J -- sim --> K[return empty false]
    J -- nao --> L[store.Info bestID]
    L --> M{ok AND state == ready?}
    M -- sim --> N[return info true]
    M -- nao --> O[return empty false]
```

---

## Legenda

| Elemento | Significado |
|----------|-------------|
| `flag.Parse` | CLI parse das flags `-repo`, `-verbose`, `-log`, etc. |
| `store.Replace` | Publica o índice via RWMutex (state → ready) |
| `saveSnapshot` | Escrita atômica do snapshot em disco (gob) |
| `ResolveIndex` | Maior prefixo entre roots registrados |
| `timed` | Wrapper de timeout + elapsed_ms + logging |
| `TryLock` | Coalescing de refreshes concorrentes |
| `diff` | Compara filesystem vs meta (size + mtime) |
| `threshold` | max(500, 20% do total) — delta maior → rebuild |
