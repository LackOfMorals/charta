# graphlite

**Zero-infrastructure embedded property graph database for Go — openCypher over SQLite.**

graphlite stores a labelled property graph in a local SQLite file (or in memory) and accepts queries written in openCypher. There is no external process to start, no driver dependency to manage, and no network — just open a file and query.

```go
db, err := graphlite.Open(":memory:")
result, err := db.RunQuery(ctx, `MATCH (n:Person) RETURN n.name AS name`, nil)
for result.Next(ctx) {
    fmt.Println(result.Record().Values()[0])
}
```

---

## Why graphlite

- **Tests run without infrastructure.** No Docker container to spin up, no port to reserve, no shared state between CI workers.
- **Development is friction-free.** Clone the repo, run `go test` — it works. No `docker compose up`.
- **No driver dependency.** graphlite has no dependency on the neo4j Go driver package. Add it to any project without import conflicts.
- **Use alongside Neo4j.** The `examples/` directory shows patterns for switching between a local graphlite database and a remote Neo4j instance, copying data in either direction, and running the same Cypher queries against both backends.

graphlite is intentionally embedded-only. It does not implement the Bolt wire protocol and is not designed to run as a standalone server. Staying embedded means staying zero-infrastructure, CGO-free, and deployable anywhere Go runs.

---

## Cypher Compatibility

graphlite passes **every scenario of the openCypher Technology Compatibility Kit** that applies to Cypher 25 (3,895 scenarios; the few that rely on syntax Cypher 25 removed are listed with reasons in `compat/testdata/excluded.txt`). Queries are parsed by a hand-written Cypher 25 parser, checked by a semantic analysis pass that reports the standard error classes and codes, and run by a Go interpreter over SQLite.

| Feature | Supported |
|---|:---:|
| `MATCH` / `OPTIONAL MATCH` — labels, properties, directed and undirected relationships, variable-length paths, named paths | ✅ |
| `shortestPath()` / `allShortestPaths()`, pattern predicates, `EXISTS`/`COUNT`/`COLLECT` subqueries | ✅ |
| `WHERE`, `RETURN`, `WITH`, `ORDER BY`, `SKIP`/`LIMIT`, `DISTINCT`, `UNWIND`, `UNION [ALL]`, `RETURN *` | ✅ |
| Aggregation — `count`, `sum`, `avg`, `min`, `max`, `collect`, `percentile*`, `stDev*` | ✅ |
| `CASE`, list/pattern comprehensions, quantifiers (`all`/`any`/`none`/`single`), `reduce` | ✅ |
| Scalar, string, math, list, conversion and entity functions | ✅ |
| Temporal types — `date`, `time`, `localtime`, `datetime`, `localdatetime`, `duration` (constructors, accessors, truncation, arithmetic, `duration.between`) | ✅ |
| `CREATE`, `MERGE` (`ON CREATE`/`ON MATCH`), `SET`, `REMOVE`, `DELETE`/`DETACH DELETE`, `FOREACH` | ✅ |
| `CALL` procedures — built-in `db.labels()`, `db.relationshipTypes()`, `db.propertyKeys()` and your own via `DB.RegisterProcedure` | ✅ |
| `CALL { … }` subqueries, `FILTER`, `LET`, `FINISH` | ✅ |
| Named query parameters (`$param`) | ✅ |
| Bulk import — JSON, CSV (Neo4j format); bulk export — JSON | ✅ |
| Neo4j extensions — label/type expressions, dynamic labels and properties, map projections, `EXISTS`/`COUNT`/`COLLECT` and `CALL {}` subqueries, `FILTER`/`LET`/`FINISH`, `OFFSET`, `IS :: TYPE` predicates, `EXPLAIN`/`PROFILE` | ✅ |
| Quantified path patterns (`((a)-[:R]->(b)){1,3}`, `-[:R]->+`) and path selectors (`ANY`, `ALL`, `SHORTEST k`, `SHORTEST k GROUPS`) | ✅ |
| `POINT` (cartesian, WGS-84; `point.distance`, `point.withinBBox`) and `VECTOR` (`vector()`, `vector_distance`, `vector_norm`, `vector.similarity.*`) values; vector indexes with exact nearest-neighbour search (`db.index.vector.queryNodes`, `DB.VectorSearch`) | ✅ |
| Schema — `CREATE`/`DROP INDEX` and `CONSTRAINT` (unique, node/relationship key, existence, type), `SHOW INDEXES`/`CONSTRAINTS`/`PROCEDURES` | ✅ |
| `LOAD CSV` (opt-in: `WithImportDirectory`) | ✅ |
| Nested quantified path patterns, `SHOW FUNCTIONS`, `USE` and user/role/database management commands | ❌ (parsed; execution returns `ErrUnsupportedCypher`) |

Unsupported features return `ErrUnsupportedCypher` — they never silently produce wrong results.

---

## Install

```bash
go get github.com/LackOfMorals/graphlite
```

Requires Go 1.26+. No CGO required. Works on Linux (amd64/arm64), macOS (arm64), and Windows (amd64).

---

## Quick Start

### Opening a database

```go
import "github.com/LackOfMorals/graphlite"

// In-memory — transient, great for tests
db, err := graphlite.Open(":memory:")

// File-backed — persists across restarts
db, err := graphlite.Open("/var/data/graph.db")

// With options
db, err := graphlite.Open("graph.db",
    graphlite.WithBusyTimeout(5*time.Second),
    graphlite.WithReadOnly(),
)

defer db.Close(ctx)
```

In tests, use `NewTestDB` to open an in-memory database that is closed automatically when the test ends:

```go
db := graphlite.NewTestDB(t)
```

### Concurrency

A `*DB` is safe for concurrent use. On a file-backed database, statements without updating clauses run on a small pool of read-only SQLite connections (`WithMaxReadConns`, default 2), each in its own snapshot transaction. Readers never wait for the writer and the writer never waits for readers: a read issued while another goroutine holds an open `BeginTx` transaction returns immediately and sees only committed data. Writes, schema commands and explicit transactions use the single write connection. In-memory databases have one connection and no read pool.

More read connections are not always faster, because the pure-Go SQLite shares allocator and file-lock state between connections. On a 20,000-node graph, two readers run indexed lookups 1.8x faster than one but gain nothing from more, and run a full-label scan 1.3x faster, while four run it slower than one. Raise `WithMaxReadConns` only after measuring (`go test -bench ParallelReads`).

### Vector search

Store embeddings as `VECTOR` values (or lists of numbers), declare a vector index, and search it:

```cypher
CREATE VECTOR INDEX doc_embeddings FOR (d:Doc) ON (d.embedding)
OPTIONS {indexConfig: {`vector.dimensions`: 384, `vector.similarity_function`: 'cosine'}}

CALL db.index.vector.queryNodes('doc_embeddings', 10, $query) YIELD node, score
RETURN node.title, score
```

```go
matches, err := db.VectorSearch(ctx, "doc_embeddings", queryVector, 10) // []VectorMatch{Node, Score}
```

The index validates the dimension on every write (a wrong-sized vector fails the statement) and supports `cosine` (score `(1 + cos)/2`) and `euclidean` (score `1/(1 + d²)`). The search is exact, not approximate: it scans an in-memory copy of the index's vectors (float32, contiguous, split across CPUs). Measured on an Apple M5 Max at 384 dimensions: 10,000 vectors in 0.2 ms, 100,000 in 0.8 ms, 1,000,000 in 7 ms (about 1.5 GB of memory). The copy is built on the first search after the database is opened (about 4 µs per vector: 0.4 s for 100,000, 4 s for a million, on all your cores) and then kept current as you write. Writes inside an explicit transaction are visible to searches in that transaction and to others only after it commits.

Limits: node indexes only (no relationship vector indexes), a write to the same file by another process is not seen until the database is reopened, and the in-memory copy is capped at 512 MiB across all indexes (`WithMaxVectorCacheBytes`); beyond that a search still works but rebuilds its copy each time. There is no approximate (HNSW) index, which only starts to matter beyond a few million vectors.

### Auto-commit queries

`RunQuery` executes a Cypher statement in auto-commit mode and returns a lazy `*Result` cursor.

```go
result, err := db.RunQuery(ctx,
    `MATCH (p:Person {name: $name})-[:KNOWS*1..3]->(f:Person) RETURN f.name AS name`,
    map[string]any{"name": "Alice"},
)
if err != nil {
    log.Fatal(err)
}
for result.Next(ctx) {
    fmt.Println(result.Record().Values()[0])
}
if _, err := result.Consume(ctx); err != nil {
    log.Fatal(err)
}
```

### Explicit transactions

`BeginTx` starts an explicit transaction. Call `Commit` or `Rollback` when done; `Close` is idempotent and always safe to defer.

```go
tx, err := db.BeginTx(ctx)
if err != nil {
    log.Fatal(err)
}
defer tx.Close()

_, err = tx.Run(ctx, `CREATE (n:Person {name: $name})`, map[string]any{"name": "Bob"})
if err != nil {
    _ = tx.Rollback()
    log.Fatal(err)
}
if err := tx.Commit(); err != nil {
    log.Fatal(err)
}
```

### Collecting results

`Collect` drains all records from a `*Result` into a slice:

```go
result, err := db.RunQuery(ctx, `MATCH (n:Person) RETURN n`, nil)
records, err := result.Collect(ctx)
for _, rec := range records {
    node, _ := rec.Get("n")
    fmt.Println(node.(*graphlite.Node).Props)
}
```

`Single` returns the one record in a result, or an error if there are zero or more than one:

```go
result, err := db.RunQuery(ctx, `MATCH (n:Person {name: "Alice"}) RETURN n`, nil)
rec, err := result.Single(ctx)
// err is graphlite.ErrNoRecords or graphlite.ErrMultipleRecords when applicable
```

### Generic helpers

Use the generic helpers for typed property and record access:

```go
// Extract a typed property from a Node or Relationship
age, err := graphlite.GetProperty[int64](node, "age")

// Extract a typed value from a Record column
name, isNil, err := graphlite.GetRecordValue[string](rec, "name")

// Collect all records as a typed slice using a mapper
result, _ := db.RunQuery(ctx, `MATCH (n:Person) RETURN n.name AS name`, nil)
names, err := graphlite.CollectT(ctx, result, func(rec *graphlite.Record) (string, error) {
    return graphlite.GetRecordValue[string](rec, "name")
})

// Single with a mapper
result, _ := db.RunQuery(ctx, `MATCH (n:Person {name: "Alice"}) RETURN n`, nil)
node, err := graphlite.SingleT(ctx, result, func(rec *graphlite.Record) (*graphlite.Node, error) {
    n, _, err := graphlite.GetRecordValue[*graphlite.Node](rec, "n")
    return n, err
})
```

### Bulk import and export

```go
// Import from JSON
f, _ := os.Open("testdata/graph.json")
if err := db.Import(ctx, f, graphlite.FormatJSON); err != nil {
    log.Fatal(err)
}

// Export to JSON
var buf bytes.Buffer
if err := db.Export(ctx, &buf, graphlite.FormatJSON); err != nil {
    log.Fatal(err)
}
```

### Snapshots

`Snapshot` writes an atomic, consistent copy of the database to a file using SQLite `VACUUM INTO`. Works on both file-backed and in-memory databases.

```go
db, _ := graphlite.Open(":memory:")
// ... build or import graph data ...

if err := db.Snapshot("/var/data/graph-checkpoint.db"); err != nil {
    log.Fatal(err)
}

// Reopen the snapshot as a normal database.
snap, _ := graphlite.Open("/var/data/graph-checkpoint.db")
```

---

## Examples: graphlite alongside Neo4j

The `examples/` directory contains three self-contained programs. Each has its own `go.mod` that imports both graphlite and the neo4j Go driver, so they do not affect the root module.

| Example | What it shows |
|---|---|
| `examples/backend_switch/` | Choose graphlite or Neo4j at startup via an env var; both run the same Cypher query |
| `examples/copy_from_neo4j/` | Seed a local graphlite database from a running Neo4j instance |
| `examples/copy_to_neo4j/` | Push a graphlite sub-graph to a remote Neo4j cluster |

Run any example with `go run .` from its directory. See the comment block at the top of each `main.go` for environment variable configuration.

---

## API Reference

### Entry point

| Symbol | Signature | Description |
|---|---|---|
| `Open` | `Open(path string, opts ...Option) (*DB, error)` | Open or create a database. Use `":memory:"` for a transient in-memory store. |
| `NewTestDB` | `NewTestDB(t testing.TB) *DB` | Open an in-memory database; registers `t.Cleanup` to close it. |

### DB methods

| Method | Signature | Description |
|---|---|---|
| `RunQuery` | `(*DB) RunQuery(ctx, cypher string, params map[string]any) (*Result, error)` | Execute a Cypher statement in auto-commit mode. |
| `BeginTx` | `(*DB) BeginTx(ctx) (*Tx, error)` | Start an explicit transaction. |
| `Import` | `(*DB) Import(ctx, r io.Reader, format Format) error` | Bulk-import nodes and relationships from JSON or CSV. |
| `Export` | `(*DB) Export(ctx, w io.Writer, format Format) error` | Bulk-export the graph to JSON. |
| `Snapshot` | `(*DB) Snapshot(path string) error` | Write an atomic consistent copy of the database to a file. |
| `Close` | `(*DB) Close(ctx) error` | Release all resources. |

### Tx methods

| Method | Signature | Description |
|---|---|---|
| `Run` | `(*Tx) Run(ctx, cypher string, params map[string]any) (*Result, error)` | Execute a Cypher statement inside the transaction. |
| `Commit` | `(*Tx) Commit() error` | Commit the transaction. |
| `Rollback` | `(*Tx) Rollback() error` | Roll back the transaction. |
| `Close` | `(*Tx) Close() error` | Close the transaction (rolls back if not yet committed). |

### Result methods

| Method | Signature | Description |
|---|---|---|
| `Next` | `(*Result) Next(ctx) bool` | Advance the cursor; returns false when exhausted or on error. |
| `Record` | `(*Result) Record() *Record` | Return the current record (valid after `Next` returns true). |
| `Err` | `(*Result) Err() error` | Return any iteration error. |
| `Keys` | `(*Result) Keys() []string` | Return the projection column names. |
| `Collect` | `(*Result) Collect(ctx) ([]*Record, error)` | Drain all records into a slice. |
| `Single` | `(*Result) Single(ctx) (*Record, error)` | Return the single record, or `ErrNoRecords` / `ErrMultipleRecords`. |
| `Consume` | `(*Result) Consume(ctx) (*ResultSummary, error)` | Drain remaining records and return query counters. |

### Generic helpers

| Function | Description |
|---|---|
| `GetProperty[T PropertyValue](entity, key)` | Extract a typed property from a `*Node` or `*Relationship`. |
| `GetRecordValue[T RecordValue](rec, key)` | Extract a typed value from a `*Record` column; second return is true when null. |
| `CollectT[T any](ctx, result, mapper)` | Collect all records, applying a mapper to each; returns `[]T`. |
| `SingleT[T any](ctx, result, mapper)` | Like `Single`, but applies a mapper to the one record; returns `T`. |

### Sentinel errors

| Error | Meaning |
|---|---|
| `ErrNoRecords` | `Single` found zero records. |
| `ErrMultipleRecords` | `Single` found more than one record. |
| `ErrReadOnly` | A write statement was attempted on a read-only database. |
| `ErrUnsupportedCypher` | The Cypher feature is not yet implemented. |

---

## Architecture

```
graphlite/
├── types.go          ← Node, Relationship, Record, error types
├── driver.go         ← graphlite.Open, DB, RunQuery, BeginTx
├── tx.go             ← Tx type (Run, Commit, Rollback, Close)
├── result.go         ← Result cursor (Next, Record, Err, Keys, Collect, Single, Consume)
├── helpers.go        ← generic helpers (GetProperty, GetRecordValue, CollectT, SingleT)
├── importer.go       ← Import / Export (JSON, CSV)
├── options.go        ← functional options (WithBusyTimeout, WithReadOnly)
├── engine.go         ← parse → analyze → interpret pipeline
├── cypher/
│   ├── syntax/       ← hand-written Cypher 25 lexer + parser + typed AST
│   ├── analyze/      ← semantic analysis (variables, aggregation, types)
│   ├── interp/       ← Go interpreter: executes a statement against SQLite
│   └── proc/         ← procedure signatures and registry (CALL)
├── store/
│   ├── store.go      ← Store interface
│   ├── sqlite.go     ← modernc.org/sqlite implementation
│   └── schema.go     ← DDL: nodes/edges tables + indexes
├── compat/
│   └── tck_test.go   ← openCypher TCK harness (opt-in: -tags=tck)
├── examples/
│   ├── backend_switch/   ← switch between graphlite and Neo4j at runtime
│   ├── copy_from_neo4j/  ← seed graphlite from a Neo4j instance
│   └── copy_to_neo4j/    ← push graphlite data to a Neo4j instance
└── bench/
    └── *.go          ← benchmark suite
```

Storage uses two tables in SQLite WAL mode. Queries are executed by a Go interpreter that reads nodes and relationships from SQLite (narrowing scans with property equalities and automatic expression indexes) and applies writes in one transaction per statement.

```sql
CREATE TABLE nodes (
    id     INTEGER PRIMARY KEY AUTOINCREMENT,
    labels TEXT    NOT NULL DEFAULT '',   -- comma-separated: "Person,Employee"
    props  JSON    NOT NULL DEFAULT '{}'
);

CREATE TABLE edges (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    type     TEXT    NOT NULL,
    start_id INTEGER NOT NULL REFERENCES nodes(id),
    end_id   INTEGER NOT NULL REFERENCES nodes(id),
    props    JSON    NOT NULL DEFAULT '{}'
);
```

---

## Testing

```bash
# Unit and integration tests
CGO_ENABLED=0 go test -count=1 ./...

# TCK harness (openCypher Technology Compatibility Kit)
CGO_ENABLED=0 go test -tags=tck ./compat/... -v

# Property-based tests
CGO_ENABLED=0 go test -run TestRapid ./...

# Benchmarks
CGO_ENABLED=0 go test -run=^$ -bench=. -benchtime=10s ./bench/...
```

---

## API Stability

**No breaking changes are made to the public API after v0.3 without a major version bump.**

This covers the root package and all documented sub-packages. Adding new exported symbols is not a breaking change. See [CONTRIBUTING.md](CONTRIBUTING.md) for the full definition.

| Version | Highlights |
|---|---|
| v0.1 | MATCH, CREATE, SET, DELETE, bulk JSON import |
| v0.2 | OPTIONAL MATCH, WITH, aggregation, COLLECT, DISTINCT, REMOVE, CSV import/export |
| v0.3 | MERGE (with ON CREATE/ON MATCH), property-based tests, TCK harness |
| **v1.0** | **CASE expressions, variable-length paths, 100% openCypher TCK pass rate** |
| v1.1 | CopyFrom / CopyTo migration, Snapshot, functional options (WithBusyTimeout, WithReadOnly, NewTestDB) |
| **v2.0** | **Remove neo4j driver dependency; native `Open`/`RunQuery`/`BeginTx` API; `Single`, `ErrNoRecords`, `ErrMultipleRecords`; generic helpers (`GetProperty`, `GetRecordValue`, `CollectT`, `SingleT`)** |

---

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for setup, test suite commands, the 5-step guide for adding a Cypher feature, benchmark baseline process, and PR guidelines.

---

## License

Apache 2.0 — see [LICENSE](LICENSE).
