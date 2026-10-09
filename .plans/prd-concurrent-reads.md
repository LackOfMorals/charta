# PRD: Concurrent Reads for File-Backed Databases

> **Status update (2026-10-09):** The translator-based wording below is historical. Reads now run in the Go interpreter (cypher/interp); the read pool must serve statements with no updating clause inside one read transaction (snapshot), and the interpreter must not write on that path (automatic index advisor, constraint checks). See tasks-concurrent-reads.yml.

## Overview

`store/store.go:98-101`'s `Store` interface doc comment claims: "The Store is safe for concurrent reads when opened with WAL mode (which the SQLiteStore implementation enables automatically)." In practice this promise is not delivered: `store/sqlite.go:59` calls `db.SetMaxOpenConns(1)`, so the underlying `*sql.DB` connection pool never has more than one connection. WAL mode (`store/sqlite.go:62`) is enabled, but its main practical benefit — concurrent readers proceeding without blocking on a writer — is unreachable, because every read and write from a single `*graphlite.DB` handle is serialized through that one connection. This is a real, user-visible gap versus comparable Go+SQLite graph libraries (e.g. `go-sqlite-graph`, which pools connections explicitly for concurrent access) and is the kind of limitation that shows up immediately in any non-trivial embedded use — e.g. a web server issuing concurrent read queries against a shared `graphlite.DB`.

This PRD adds a second, size-bounded pool of read-only connections for file-backed databases, routes read-only Cypher queries to it, and keeps a single dedicated connection for all writes — closing the gap between the `Store` interface's documented promise and what `SQLiteStore` actually does.

## Goals

- File-backed (non-`:memory:`) `graphlite.DB` handles support genuinely concurrent reads: multiple goroutines calling `RunQuery` with read-only Cypher can execute simultaneously without serializing through a single connection.
- Writes remain strictly single-writer, as SQLite requires — no change to write correctness or the existing `KindMatchForWrite` single-connection-cursor constraint documented in `AGENTS.md`.
- The `Store` interface's existing doc comment ("safe for concurrent reads … WAL mode") becomes true rather than aspirational.
- `:memory:` databases are explicitly documented as remaining single-connection (they are inherently per-connection in SQLite, per `AGENTS.md`'s existing gotcha), with no behavior change and no silent data-isolation bugs.

## Non-Goals

- No change to write concurrency — SQLite (and this PRD) still allows exactly one writer at a time.
- No distributed/multi-process concurrency story — this is entirely about intra-process goroutine concurrency on a single open `*graphlite.DB`.
- No change to the `store.Store`/`store.Execer`/`store.TxExecer` interface *signatures* — only to what backs them inside `SQLiteStore`.
- No support for concurrent reads on `:memory:` databases (architecturally impossible without a shared-cache mode change that is out of scope here — see Open Questions).
- No new `graphlite.Open` option is required for this to work by default; a `WithMaxReadConns` option may be added but is not required for the base goal (see Requirements).

## Requirements

### Functional Requirements

- REQ-F-001: `store/sqlite.go`'s `SQLiteStore` gains a second `*sql.DB` (or a dedicated pooled read handle) opened against the same file URI, with `SetMaxOpenConns(n)` for some bounded `n > 1`, used exclusively for read-only queries.
- REQ-F-002: The read pool's connections are opened read-only where the driver supports it (e.g. `?mode=ro` DSN parameter or `PRAGMA query_only = ON` per-connection) to guarantee a read connection can never accidentally take a write lock.
- REQ-F-003: For `:memory:` databases, `store.Open` detects the URI and falls back to the current single-connection behavior entirely (no read pool) — `:memory:` databases are per-connection in SQLite, so a second pooled connection would see an empty, disconnected database. This must be enforced, not just documented.
- REQ-F-004: `driver.go`'s query-execution path (`executeStatements` and callers) classifies a query as read-only when its single statement's `Kind == glsql.KindSelect` (matching the existing fast-path check at `driver.go:280`) and, when so, executes it against the read pool's `Execer` instead of the single write connection's.
- REQ-F-005: All write paths (`execWriteStatements`, `execWriteThenSelect`, `execMergeBatch`, explicit `BeginTx` transactions) continue to use the single write connection unchanged — no behavior change to write correctness or the single-connection-cursor constraint.
- REQ-F-006: `WithReadOnly()`-opened databases use the read pool for all queries (since by definition no writes are possible), giving them full concurrent-read benefit with zero write-connection contention.
- REQ-F-007: A new `WithMaxReadConns(n int)` functional option (default: a small fixed number, e.g. 4) lets callers tune the read pool size; `n <= 0` is a no-op retaining the default, matching the existing `WithMaxPathHops` convention (`options.go:52-58`).
- REQ-F-008: `store.Store`'s doc comment is updated to describe precisely what "safe for concurrent reads" now means (file-backed only, read-pool-backed) rather than the current aspirational wording.

### Non-Functional Requirements

- REQ-NF-001: `go build ./...`, `go vet ./...`, and `CGO_ENABLED=0 go test -tags=unit -count=1 ./...` pass.
- REQ-NF-002: A new concurrency-focused test (under `-race`) issues concurrent `RunQuery` reads against a file-backed `graphlite.DB` and asserts no data races and no serialization stalls beyond what the read-pool size implies.
- REQ-NF-003: No regression to existing single-writer semantics: a benchmark comparable to existing ones in `bench/` shows write throughput unchanged (within noise) versus the pre-PRD baseline.
- REQ-NF-004: The `store/` package continues to work only with raw IDs, labels, and JSON blobs — no Cypher types cross into `store/` as part of this change (`AGENTS.md`'s architectural constraint).
- REQ-NF-005: `db.Close(ctx)` closes both the write connection and the read pool; no connection leak (verified via a test that opens/closes many `graphlite.DB` instances under `-race` with goroutine-leak detection, per the project's existing testing conventions).

## Technical Considerations

**Why not just raise `SetMaxOpenConns` on the single `*sql.DB`?** Because SQLite allows only one writer at a time regardless of pool size, and mixing a bigger pool with the existing single-cursor-then-write pattern (`AGENTS.md`: "SQLite's single connection cannot hold an open `*sql.Rows` cursor and a concurrent write") would reintroduce exactly the bug that constraint exists to prevent — a read cursor from one pooled connection could still collide with an in-flight write's schema/lock expectations in ways the current code doesn't guard against, because all of today's collision-avoidance logic (`collectMatchRows` draining fully before writing) assumes a single physical connection. Two pools with a hard read/write split sidesteps this: the write connection's single-connection invariants are completely unchanged, and the read pool never issues writes.

**WAL mode is a prerequisite, not sufficient by itself** — WAL is already enabled (`store/sqlite.go:62`); it's what makes concurrent readers-during-a-write safe at the SQLite level. This PRD's job is purely to let the Go connection pool actually take advantage of that.

**Read-only DSN enforcement**: `modernc.org/sqlite` DSN read-only support should be verified at implementation time (`?mode=ro` or equivalent); `PRAGMA query_only = ON` executed once per new connection (via `sql.DB`'s `ConnMaxLifetime`/connection-init hook, or simply once at pool-open time relying on `SetMaxIdleConns` keeping connections warm) is the fallback if DSN-level read-only isn't supported cleanly by the driver.

**Routing decision point**: `driver.go:280`'s existing `stmts[0].Kind == glsql.KindSelect` check is already exactly the signal needed to decide "route to read pool" — this PRD extends that same fast path rather than inventing a new classification mechanism.

**Plan cache is unaffected**: `plan_cache.go` caches the translated SQL, not a connection choice — routing decisions happen after a cache hit/miss, at execution time.

## Acceptance Criteria

- [ ] A file-backed `graphlite.DB` served concurrently by N goroutines each running read-only `RunQuery` calls shows real concurrency (measurable via a benchmark or a blocking-read test) rather than full serialization.
- [ ] A concurrent read + a concurrent write against the same file-backed `graphlite.DB` do not deadlock and do not corrupt data (verified under `-race` and with a data-integrity assertion after the run).
- [ ] `:memory:` databases behave exactly as before (single connection, no read pool) — verified by a test that a `:memory:` DB's read pool is never created.
- [ ] `WithMaxReadConns(n)` option exists, defaults sensibly, and `n <= 0` is a no-op.
- [ ] `WithReadOnly()` databases use the read pool for 100% of queries.
- [ ] `db.Close(ctx)` cleanly closes all connections in both pools; no goroutine or connection leak.
- [ ] `store.Store`'s doc comment accurately describes the new behavior.
- [ ] All existing unit tests and the TCK suite continue to pass unchanged.

## Out of Scope

- Multi-process concurrency (separate OS processes sharing one SQLite file) — already possible today via SQLite's own locking, unaffected by this PRD either way.
- `:memory:` concurrent reads.
- Read replicas, read-your-writes consistency guarantees beyond what SQLite/WAL already provides, or any distributed story.

## Open Questions

- Should `:memory:` databases eventually get concurrent-read support via SQLite's shared-cache mode (`cache=shared`) instead of being permanently excluded? Shared-cache mode has its own well-known locking quirks and is deliberately deferred — flag for a future PRD if there's real demand.
- Should the read pool size default scale with `GOMAXPROCS`, or stay a small fixed constant? Recommend starting fixed (e.g. 4) and revisiting with real benchmark data.
