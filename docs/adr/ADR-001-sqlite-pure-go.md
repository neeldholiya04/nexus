# ADR-001: Use modernc.org/sqlite (Pure Go) over mattn/go-sqlite3 (CGO)

## Status
Accepted

## Context

Nexus requires SQLite with FTS5. Two mature Go drivers exist:

- `mattn/go-sqlite3` — CGO binding, battle-tested, widely used
- `modernc.org/sqlite` — machine-translated pure Go, no CGO

The development environment is Windows (GoLand). The deployment target is Linux (amd64). This cross-platform build requirement is the primary forcing function.

## Options Considered

| Factor | mattn/go-sqlite3 | modernc.org/sqlite |
|---|---|---|
| CGO required | Yes | No |
| Cross-compile Windows→Linux | Requires C toolchain (MinGW/WSL) | `GOOS=linux go build` just works |
| FTS5 support | Yes (compile flag needed) | Yes (included) |
| Performance | Marginally faster | ~5-10% slower in benchmarks |
| Ecosystem maturity | Very mature (2012) | Mature (2019, actively maintained) |
| Community | Large | Smaller but sufficient |

## Decision

Use `modernc.org/sqlite`.

## Rationale

1. **Cross-compilation is zero-friction.** `GOOS=linux GOARCH=amd64 go build` produces a valid Linux binary from Windows without MinGW, WSL, or Docker. This directly supports the dev-on-Windows, deploy-on-Linux workflow.

2. **CGO adds operational complexity.** CGO requires a matching C toolchain on every build host (CI, dev machines). Pure Go removes this class of environment-specific failures entirely.

3. **Performance difference is negligible at V0 scale.** SQLite query performance at <100K memories is dominated by schema design and indexing, not driver overhead.

4. **FTS5 is included without special build tags.** `mattn/go-sqlite3` requires `-tags sqlite_fts5` at compile time; `modernc.org/sqlite` includes it by default.

## Consequences

- All builds are CGO-free: `CGO_ENABLED=0` can be set explicitly in CI
- Migration to `mattn/go-sqlite3` later is a one-line import change (same `database/sql` interface)
- Slightly higher memory usage due to the pure-Go SQLite implementation

## Trade-offs

- If Nexus ever needs SQLite extensions that can only be loaded as shared libraries (e.g., vector search extensions like sqlite-vss), CGO would be required at that point. This is an acceptable future trade-off — in V3 we migrate to Graphiti anyway, removing the SQLite dependency for vector operations.
