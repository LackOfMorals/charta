# AGENTS.md — charta

## Project Overview

charta is an embedded property graph database for Go, backed by SQLite and queryable via a subset of openCypher. The primary entry point is `charta.Open`; queries are executed with `db.RunQuery` or via explicit transactions started with `db.BeginTx`.

- Module path: `github.com/LackOfMorals/charta`
- Go minimum version: 1.26
- SQLite driver: `modernc.org/sqlite` (CGO-free, no mattn/go-sqlite3)

## Feedback Instructions

### Build
```bash
CGO_ENABLED=0 go build ./...
```

### Test (unit)
```bash
CGO_ENABLED=0 go test -tags=unit -count=1 ./...
```

### Test (all, excluding tck)
```bash
CGO_ENABLED=0 go test -count=1 ./...
```

### Vet
```bash
go vet ./...
```

## Package Layout

```
charta/
├── types.go        ← Node, Relationship, Record, error types
├── driver.go       ← charta.Open, DB, RunQuery, BeginTx
├── interfaces.go   ← exported interfaces (Driver, Session, Transaction, Result, …)
├── session.go      ← session, managedTx, Tx concrete types
├── result.go       ← QueryResult / Result cursor implementation
├── importer.go     ← Import / Export helpers
├── migrate.go      ← neo4j migration helpers (to be removed in v2)
├── neo4jadapter/   ← neo4j DriverCompat (to be removed in v2)
├── engine.go       ← parse → analyze → interpret pipeline (statement cache, procedures)
├── cypher/
│   ├── syntax/     ← hand-written Cypher 25 lexer/parser + typed AST (stdlib only)
│   ├── analyze/    ← semantic analysis: scopes, aggregation and type errors with TCK codes
│   ├── interp/     ← Go interpreter: the default execution engine (imports syntax + database/sql only)
│   └── proc/       ← procedure signatures, argument coercion and registry (stdlib only)
├── store/          ← Store interface + SQLite implementation + DDL
├── compat/         ← TCK harness (opt-in: -tags=tck)
└── testdata/       ← .cypher fixture files
```

## Key Architectural Constraints

- The `store/` package must NEVER import Cypher types — it works with raw IDs, labels, JSON blobs only.
- `cypher/syntax` imports only the standard library (nothing from this module). `cypher/analyze` imports only `cypher/syntax` and `cypher/proc`. `cypher/interp` imports `cypher/syntax`, `cypher/proc`, `cypher/temporal`, `cypher/spatial`, `cypher/vector`, `golang.org/x/text/unicode/norm` and `database/sql` (never `store/`).
- All SQL must use parameterised queries — never `fmt.Sprintf` user input into SQL strings.
- CGO must remain disabled: always use `modernc.org/sqlite`, never `mattn/go-sqlite3`.

## Storage Schema

```sql
CREATE TABLE nodes (
    id     INTEGER PRIMARY KEY AUTOINCREMENT,
    labels TEXT    NOT NULL DEFAULT '',
    props  JSON    NOT NULL DEFAULT '{}'
);
CREATE TABLE edges (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    type     TEXT    NOT NULL,
    start_id INTEGER NOT NULL REFERENCES nodes(id),
    end_id   INTEGER NOT NULL REFERENCES nodes(id),
    props    JSON    NOT NULL DEFAULT '{}'
);
CREATE TABLE node_labels (
    node_id INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    label   TEXT    NOT NULL,
    UNIQUE (node_id, label)
);
-- idx_node_labels_label ON node_labels(label, node_id) — O(log n) label lookups
```

WAL mode is enabled via `PRAGMA journal_mode=WAL` on every open.
`node_labels` is kept in sync automatically by SQLite triggers on nodes INSERT/UPDATE; label lookups use EXISTS subquery or JOIN against node_labels rather than LIKE on nodes.labels.

## Gotchas and Learnings

- `store.Open` calls `db.SetMaxOpenConns(1)` — CRITICAL. Without it, `:memory:` SQLite gives each pool connection a separate database.
- `Consume` and `Collect` on `QueryResult` must guard against `r.rows == nil` — write results have `rows: nil` and `consumed: true`.
- `NewRecord` panics on key/value length mismatch (programmer error).
- Labels are stored as comma-separated text in the `labels` column.
- `json_extract(props, '$.key')` is used for property access in SQLite queries.
- `go.mod` `go` directive is 1.26.5 (raised from 1.24 so govulncheck is clean: `golang.org/x/text` >= 0.39 and the net/url fix need Go 1.25+); `modernc.org/sqlite v1.35.0` builds fine at it.
- `testdata/` package is excluded from `./...` by Go design. Run explicitly: `CGO_ENABLED=0 go test github.com/LackOfMorals/charta/testdata`.
- Only one file per package should have a `// Package foo ...` doc comment.
- When deleting files that export methods used in `example_test.go`, also remove the corresponding `Example*` functions — otherwise `go build ./...` fails even if core tests pass.
- neo4j driver fully removed in task-010 via `go mod tidy` + `go mod vendor` (both needed — the repo uses a vendor dir; `go build` fails with "inconsistent vendoring" if only tidy is run).
- `Tx` type lives in `tx.go` (moved from session.go in task-003); context params on Commit/Rollback/Close were removed in task-005 — all were blank identifiers so no behavior changed.
- `DB.Close` still takes `context.Context`; only `Tx` methods are context-free.
- `*ErrImportDepthExceeded` must never be wrapped with `fmt.Errorf("%w")` — the existing test uses a direct type assertion (not `errors.As`). Use a `wrapErr` helper that checks `errors.As(err, &depthErr)` and returns the unwrapped sentinel directly.
- `//go:build ignore` example files (examples/getting_started, examples/neo4j_roundtrip) use deleted v1 APIs and are not compiled by `go build ./...` — they will be rewritten in task-012.
- `interfaces.go` is deleted in v2; all session-layer/compat interfaces (Driver, Session, Transaction, ResultTransformer, etc.) are gone.
- When replacing `NewEagerResult(ctx, qr)` calls, use `qr.Collect(ctx)` to get records directly — no intermediate struct needed.
- `QueryResult` is renamed to `Result` (task-004); `NewQueryResultFromRows` → `NewResultFromRows`; `newInMemoryQueryResult` → `newInMemoryResult`. `NewResultFromRows` is still exported until task-007 unexports it.
- When a rename touches test files that use `NewQueryResultFromRows` via dot-import, update those call sites mechanically in the same task to keep the unit test suite green.
- `ErrNoRecords` and `ErrMultipleRecords` are `fmt.Errorf` sentinels (consistent with `ErrReadOnly`); `errors.Is` works via pointer equality.
- `(*Result).Single()` uses `Consume()` to close the cursor in all paths. In the `ErrMultipleRecords` path, drain/close errors from `Consume()` are intentionally discarded (secondary to the primary sentinel); documented with a comment.
- Task-009 adds test coverage for `Single`, `ErrNoRecords`, and `ErrMultipleRecords` — but task-007 already added it in `result_test.go`.
- All formerly-exported internal helpers are now unexported (task-007): `newResultFromRows`, `newRecord`, `setCounters`, `queryCounters`. `MapColumnValue` and `SplitLabels` wrappers are deleted entirely.
- `driver.go` increments `queryCounters` fields directly (e.g. `ctr.nodesCreated++`); no exported `QueryCounters` struct exists.
- `result_test.go` and `types_test.go` use `charta.Open` + `db.RunQuery` to construct test fixtures — no raw `*sql.Rows` or `newResultFromRows` in tests.
- `testdata/integration_test.go` and `compat/tck_test.go` still reference `NewEagerResult` (removed in task-003) — they are out-of-scope for `go build ./...` and will be fixed in task-009.
- `helpers.go` adds `PropertyValue`, `RecordValue`, `GetProperty[T]`, `GetRecordValue[T]`, `CollectT[T]`, `SingleT[T]`. The unexported `propsGetter` interface is implemented by `*Node` and `*Relationship` via `getProps()` methods added to `types.go`-adjacent declarations in `helpers.go`.
- `convertTo[T]` uses `any(zero).(type)` type-switch (not reflection) to coerce JSON-decoded values; SQLite returns JSON numbers as `float64`, so `toInt64` converts `float64→int64` via truncation.
- `charta.EagerResult` and `charta.NewEagerResult` are deleted in v2. Test packages that need eager collection define a local `eagerResult` struct + `collectResult` helper using `qr.Collect(ctx)` followed by `qr.Consume(ctx)` (idempotent) to get counters.
- `types.go` had a second `// Package charta ...` doc block (v1-era text referencing Neo4j Aura); it was removed in task-011. Only `driver.go` carries the package doc comment.
- `testdata/integration_test.go` and `compat/tck_test.go` both define their own `eagerResult`/`collectResult` — they are separate packages and cannot share a common helper without a new exported type.
- `DB.Close` still takes `context.Context` (only `Tx` methods are context-free); any test calling `db.Close()` without args must be fixed to `db.Close(context.Background())`.
- `analyze.Check` reports only what it can prove (unknown types are accepted) and must have ZERO false positives: `cypher/analyze` `TestTCK_ValidScenariosPass` checks every valid TCK query and `TestTCK_CompileTimeErrors` requires the exact class and code for all 586 compile-time-error cases. Rules worth knowing: a path variable is bound AFTER its elements (so `p = (p)-->()` is "already bound" while a later node `r` after path `r` is a type conflict); in an aggregating item every variable/property leaf outside an aggregate must equal a projected non-aggregating item (so `me.age + you.age + count(*)` is ambiguous even if `me.age + you.age` is projected); `NoExpressionAlias` is reported after the other projection checks; `WITH *` with no variables is legal but `RETURN *` is `NoVariablesInScope`.
- The TCK harness (`compat`) requires class AND code to match for "should be raised at compile time"; runtime and any-time expectations still accept any error. Property access on a path is a `SyntaxError`, on a non-map value a `TypeError` (both `InvalidArgumentType`).
- `cypher/syntax` operator precedence is the one the TCK pins down (Precedence1-4), loosest to tightest: OR, XOR, AND, NOT, comparison (`= <> < >` …), predicates (`IN`, `STARTS WITH`, `CONTAINS`, `=~`, `IS NULL`, `:: TYPE`), `+ - ||`, `* / %`, `^` (left-associative), unary `-`/`+`, then postfix. So `[1]+2 IN [3]+4` is `([1]+2) IN ([3]+4)` and `false = true IN l` is `false = (true IN l)`. Operator and clause words (`NOT`, `AND`, `IN`, `WHEN`, …) cannot start an expression; other keywords are valid variable, label and function names.
- SQLite FOREIGN KEY constraint errors are detected via `strings.Contains(err.Error(), "FOREIGN KEY constraint failed")` — modernc.org/sqlite surfaces the constraint name verbatim in the error string. Catch this in `InsertEdge` callers and return a domain-appropriate error rather than exposing the raw SQLite message.
- CSV node `:ID` values are file-local labels only — the actual SQLite primary keys are AUTOINCREMENT-assigned by `InsertNode`. In a fresh empty DB, sequential inserts give IDs 1, 2, 3, … matching the CSV row order, which benchmarks rely on.
- `Result.rawVals`, `ptrs`, and `vals` are pre-allocated in `newResultFromRows` and reused across all `Next` calls. `ptrs[i] = &rawVals[i]` is stable because `rawVals` is never appended to. `newRecord` copies both keys and values internally, so reusing `vals` is safe.
- `importJSON` uses `io.ReadAll(io.LimitReader(r, importMaxBytes+1))` for size detection. Do NOT replace this with a streaming decoder approach: `json.Decoder` scans bytes one at a time in a whitespace loop, causing `TestImport_TooLarge` (which sends 500MB of spaces via `io.Pipe`) to hang for 30+ seconds.
- `decodeImportJSON` must handle `null` values for `"nodes"` and `"edges"` keys. When Go marshals a struct with nil slice fields, JSON produces `"nodes":null`; the decoder must treat this as an empty array (check `tok == nil` after `dec.Token()`).
- `go test -race ./cypher/...` takes about a second now that parsing no longer goes through ANTLR; the whole-module race run is dominated by the SQLite tests in the root package.
- `node_labels(node_id, label)` junction table is maintained by SQLite triggers (AFTER INSERT / AFTER UPDATE OF labels on nodes). All write paths — including raw SQL from the interpreter and importer — stay in sync automatically without Go-level changes.
- SQLite triggers use a recursive CTE to split the comma-separated `labels` column because SQLite has no native STRING_SPLIT function.
- `node_labels` has `UNIQUE(node_id, label)` so that `INSERT OR IGNORE` in `backfillMigrationSQL` truly prevents duplicate rows. Without a unique constraint, `INSERT OR IGNORE` is a no-op and does NOT deduplicate.
- The backfill migration uses `WHERE NOT EXISTS (... WHERE node_id = n.id)` to skip nodes already populated by triggers (i.e., inserted after the schema upgrade). `INSERT OR IGNORE` handles the edge case where a node partially appears in node_labels.
- The interpreter applies writes to SQLite immediately inside one transaction (rolled back on error), defers deletes to the end of the statement, and reports net counters. A null property is never stored (Cypher semantics). `collect()` returns a `[]any`, and a `WHERE` on `OPTIONAL MATCH` is part of the match (it never removes input rows).
- Missing `$parameters` are detected up front from `syntax.Statement.Params` and returned as an unwrapped `*ErrMissingParameter`.
- Procedures: `DB.RegisterProcedure(*proc.Procedure)`; built-ins are `db.labels`, `db.relationshipTypes`, `db.propertyKeys`. `analyze.CheckWith` validates calls (existence, argument count/type/passing mode) against the registry. The TCK harness registers its "there exists a procedure" declarations as lookup tables (`compat/tckproc_test.go`).
- Interpreter performance (`cypher/interp`): node scans are narrowed in SQLite by property equalities from inline property maps and top-level `WHERE var.key = <literal|param|bound var>` conjuncts (`pushdown.go`); they are only a prefilter and Go re-checks every match. Hint values must be string/int64/float64 (SQLite would equate a boolean with 1/0) and keys must be plain `[A-Za-z0-9_]` identifiers because the JSON path is inlined in the SQL (needed for expression indexes to apply). `interp.Engine` (one per `DB`) creates `idx_auto_np_<hex key>` expression indexes on `json_extract(props, '$."key"')` after a key has narrowed 3 scans on a graph with >=500 nodes (max 32, never dropped), and caches parsed+analysed statements (cleared when procedures change). `decodeProps` uses a hand-written JSON decoder (`fastjson.go`) with an encoding/json fallback; `fastjson_test.go` checks they agree.
- Query execution is `syntax.Parse` → `analyze.CheckWith` → `interp.RunWith` (`engine.go`); there is no SQL translator, planner or plan cache any more (removed after the interpreter reached parity and beat it on lookups/traversals). SQLite is the only store; the interpreter talks to it directly through `database/sql` and does not use `store.Store` for queries. Do not add a second storage backend.
- `cypher/syntax` parses Cypher 25 including Neo4j's extensions (label expressions, `EXISTS/COUNT/COLLECT {}`, `CALL (a) {} IN TRANSACTIONS`, quantified path patterns and path selectors, map projections, dynamic labels/properties, `IS :: TYPE`, `FILTER`/`LET`/`FINISH`, schema commands, `LOAD CSV`, hints, server-only commands). The interpreter executes all of these except nested quantified path patterns, `SHOW FUNCTIONS`, `USE` and the other server-only commands (user/role/database management), which return an "unsupported" error naming the construct. `CYPHER 5` is rejected; `NEXT`, `INSERT` and standalone `ORDER BY` are deliberately not parsed (not documented in the Cypher Manual). Syntax was checked against the Neo4j Cypher Manual, not guessed.
- Compile-time errors are `*syntax.SyntaxError` or `*analyze.Error`; `analyze.Describe(err)` returns the openCypher TCK class and detail code (`UndefinedVariable`, `VariableAlreadyBound`, `AmbiguousAggregationExpression`, `InvalidArgumentType`, …) through charta's `%w` wrapping. Lexer/parser errors carry a `Code` too (`IntegerOverflow`, `InvalidNumberLiteral`, …; default `UnexpectedSyntax`).
- TCK: `CGO_ENABLED=0 go test -count=1 -tags=tck ./compat/... -v`. Nothing is skipped and the harness requires 100% of the scenarios not listed in `compat/testdata/excluded.txt` (3,895 pass). `WithMaxPathHops` is an opt-in cap (default: none).
- Benchmarks: `go test -run XXX -bench BenchmarkQueries .` (2,000-node graph).
- Temporal values live in `cypher/temporal` and are returned to callers as `charta.Date`, `LocalTime`, `Time`, `LocalDateTime`, `DateTime` and `Duration` (aliases; `String()` is the canonical Cypher rendering, which the TCK compares). They are stored in the props JSON as `{"$t":<kind>,"v":"<canonical string>"}` (maps cannot be property values, so a nested object is unambiguous) and revived in `decodeProps`; they cannot be used as scan-pushdown hints. "Now" is fixed per statement (`exec.clock`) except `.realtime()`. Named zones need `time/tzdata`, which is embedded (about 450 KB of binary). A duration keeps months, days and seconds separately; fractional units cascade (month fraction -> days at 30.436875, day fraction -> seconds) using exact rational arithmetic in `duration.go`.
- Void procedures (no outputs) pass rows through an in-query CALL; procedures with outputs multiply rows by their results.
- The TCK harness checks every side-effect counter exactly (`no side effects` means all zero; unlisted names in `the side effects should be` must be zero). Label counters follow the TCK: `+labels`/`-labels` count labels that came into or went out of use in the database (compared before and after the statement), not label assignments; a node created and deleted in one statement leaves no counters; deleting an existing entity counts its properties (and labels) as removed.
- Neo4j-extension scenarios live in `compat/testdata/neo4j/*.feature` (run by `TestNeo4jExtensions`, which requires 100% except entries in `compat/testdata/neo4j-deferred.txt`). Add a scenario there with every extension construct you implement.
- Quantified path patterns (`group.go`): a group's variables are lists (one entry per iteration; `analyze` types them as lists but checks the group's own WHERE and property maps against per-iteration singletons), juxtaposed node patterns are one and the same node, relationships are never reused (so cycles end), and an unbounded quantifier is capped by `WithMaxPathHops` when set. Quantified relationships (`-[:R]->+`) are var-length relationships. Selectors other than ANY/ALL SHORTEST over a single relationship pattern enumerate all matches and select per (start, end) partition (`matchSelected`).
- Schema (`schema.go`, `show.go`): definitions are rows in `charta_schema` (created on first use); RANGE/TEXT indexes and the lookup index of a uniqueness/key constraint are SQLite expression indexes `gl_idx_<id>`; other index kinds are recorded only. Constraints (unique, key, existence, type; nodes and relationships) are checked in `graph.finish()` over the entities the statement created or changed, so a violation rolls the statement back. Bulk `Import` writes through the store and does NOT check constraints.
- `LOAD CSV` is disabled unless the DB is opened with `WithImportDirectory(dir)`; it then reads `file:///x.csv` (or `x.csv`) below `dir` only (path and symlink escapes are rejected, remote URLs are not supported). Empty fields are null; `linenumber()` and `file()` work directly after it.
- `EXPLAIN` compiles and analyses the query but does not run it and returns no rows; `PROFILE` just runs it.
- Concurrent reads: `store.Open` keeps a second `*sql.DB` (the read pool, `PRAGMA query_only` set through the DSN so every pooled connection gets it) for file databases when `Config.ReadConns > 0`; `Store.BeginReadTx` hands out a snapshot transaction on it (and is just `BeginExecTx` for `:memory:`). `runInterp` sends a statement for which `hasWrites` is false through `interp.RunReadOnly` on a read tx wrapped in `readOnlyDB` (whose `ExecContext` errors). Anything with `CREATE`/`MERGE`/`SET`/`REMOVE`/`DELETE`/`FOREACH`, a writing `CALL {}`, a schema command, or an explicit `BeginTx` stays on the write connection (`Tx` must read its own writes). Keep `hasWrites` conservative: a statement wrongly classified as read-only fails at run time with a read-only error. On the read path the automatic index advisor only queues keys (`Engine.TakeWantedIndexes`); `DB.buildIndexes` creates them on the write connection in a goroutine that `DB.Close` waits for.
- Default read pool size is 2 (`defaultReadConns`): the pure-Go SQLite serialises on allocator and file-lock mutexes, so more concurrent heavy scans get slower than serial (measured: 2 readers 1.3x faster, 4 readers 0.6x as fast on a 20k-node scan). `go test -bench ParallelReads` reproduces this; do not raise the default without re-measuring.
- Vector indexes (`schema.go`): `CREATE VECTOR INDEX ... OPTIONS {indexConfig: {`vector.dimensions`: n, `vector.similarity_function`: 'cosine'|'euclidean'}}` validates the options (dimension 1..4096 required, unknown keys rejected, HNSW tuning keys accepted and ignored) and stores them in the definition (`VectorDims`, `VectorSim`). `graph.finish()` rejects a created/changed node or relationship whose indexed property is neither null, a VECTOR of that dimension nor a list of that many finite numbers (`checkVectorIndexes`). `SHOW INDEXES YIELD options` reports the configuration.
- Vector search (`cypher/vector`, `cypher/interp/vecindex.go`): `db.index.vector.queryNodes(index, k, query)` and `DB.VectorSearch` scan an in-memory `vector.Matrix` (contiguous float32 rows, normalised for cosine, 8-accumulator kernels, worker-pool scan with per-worker bounded heaps, ties by node id; 100k x 384 in about 1.3 ms) instead of decoding properties: decoding a 384-float JSON property costs ~10 us, 100x the arithmetic. The matrix is built lazily on first use (`buildMatrixFast`: fetches each node's raw props text, a pool of parser goroutines extracts only the vector property with `vector.StoredVector` and a fast number parser straight into `vector.Builder` rows; about 4 us per 384-dim vector, limited by the SQLite fetch; the generic decoder and SQLite's json_extract both cost 11-30 us) and kept current from `VectorDelta`s that `graph.finish()` computes (created/changed/deleted nodes, label or property removal) and that the caller applies with `Engine.ApplyVectorDeltas` ONLY after the transaction commits (auto-commit path in `runInterp`, and `Tx.Commit`; a rollback discards them). Only statements with no updating clause and no enclosing explicit transaction use the shared matrix (`Options.VectorCache`); any other statement builds a private matrix from what it can see (read-your-writes, slower). A build that races a commit or a schema change is used for that query but not cached (`vecCache.epoch`). `WithMaxVectorCacheBytes` (default 512 MiB total) bounds the shared matrices. Limits: nodes only (relationship vector indexes are unsupported), and a write by another process to the same file is not seen until reopen.
- Relationship-first matching (`relfirst.go`): for a single-hop pattern `(a)-[r {k: v}]-(b)` whose relationship has pushable equality properties (inline map or the MATCH's WHERE) and whose ends are unbound with no property/WHERE constraints, `matchRelFirst` looks the relationships up by property (`graph.scanRels`, which uses a relationship property index when there is one) and then checks the ends, instead of scanning all nodes and walking their relationships. `TestRelFirstAgreesWithNodeFirst` compares it with the node-first plan (`relFirstDisabled`) on random graphs for every direction/type/label/path/WHERE shape; keep that test green when changing either strategy. `plan_internal_test.go` asserts with `EXPLAIN QUERY PLAN` that declared, constraint-backing, automatic and relationship indexes are really used. `WithoutAutomaticIndexes` turns the automatic node indexes off (`Engine.NoAutoIndexes`).
- Bulk import and the schema (`importer.go`, `cypher/interp/importcheck.go`): the importer inserts through the store, bypassing the interpreter, so after the inserts and before `Commit` it calls `DB.checkImport` -> `Engine.ValidateImported(ctx, tx.Exec(), nodeIDs, relIDs)`, which loads the imported rows in batches of 500 as if a statement had just created them and runs the constraint and vector-dimension checks; a violation rolls the import back and comes out as `*ErrConstraintViolation`. After a successful import `Engine.InvalidateVectors()` drops the vector matrices. JSON export adds a `"schema"` array (`Engine.SchemaStatements`: CREATE INDEX/CONSTRAINT ... IF NOT EXISTS, skipping the built-in lookup indexes) and JSON import replays it in the import transaction (`applyImportSchema` accepts only CREATE INDEX / CREATE CONSTRAINT). CSV formats carry no schema.

- Shortest paths (`match.go`): `shortestPaths` runs a node BFS with predecessor lists (`shortestPathsBFS`, `pathsFromPreds`; `allShortestPaths` is capped at 100,000 paths), or a bidirectional search for a single path with bound ends (`shortestPathBidirectional`; `shortestBidirectionalDisabled` test hook). Start == end and minimum length > 1 use the old path-state search (`shortestPathsSlow`). `WithMaxPathHops` errors if the requested bound exceeds the cap. Each BFS level is prefetched in batches (`prefetchLevel` -> `graph.prefetchAdj`/`prefetchNodes`, `IN (SELECT value FROM json_each(?))`; 100k grid far-corner 2.6 s -> 0.68 s). `matchSelected` (other selectors) enumerates all matches, failing above `maxSelectedMatches` (1,000,000), stops early for `ANY k` when both ends are bound, and for SHORTEST k / SHORTEST k GROUPS / ALL SHORTEST / ANY SHORTEST with bound ends searches by increasing path length (`exec.relLimit`/`overBudget` prune `walk`, `walkVarLength`; `matchParts` clears the limit for nested patterns; `selectedEarlyStopDisabled` disables both). `shortest_internal_test.go` compares the strategies on random multigraphs; `BenchmarkShortestPath` uses a 100k-node grid (results in `.plans/progress-shortest-path-traversal.txt`).
