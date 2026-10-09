# PRD: Property Indexes and Uniqueness Constraints

> **Status update (2026-10-09):** Largely delivered through Cypher schema commands (CREATE/DROP INDEX|CONSTRAINT, SHOW) executed by cypher/interp, with application-level enforcement that also supports multi-label nodes. The Go-only API, property_index_meta table and store.CreatePropertyIndex proposed below were not needed. Remaining work: public error type, Go wrappers, Import enforcement, EXPLAIN QUERY PLAN tests, benchmark. See tasks-property-indexes-constraints.yml.

## Overview

`store/schema.go:35-39` defines exactly five indexes today: `idx_nodes_labels` (the raw `labels` text column, not used for lookups per `AGENTS.md`'s own guidance — label lookups go through `node_labels`/`idx_node_labels_label` instead), `idx_edges_start`, `idx_edges_end`, `idx_edges_type`, and `idx_node_labels_label`. There is no way to index an individual node/relationship property, no composite index, no full-text search, and no unique or existence constraint. Every property filter (`WHERE n.email = $email`) compiles to `json_extract(props, '$.email') = ?` with a full table scan — there is no supporting index for that expression anywhere in the schema. This is a real performance cliff for any non-trivial dataset, and it is a feature every comparable project (Neo4j, Apache AGE, Kuzu, ArcadeDB) offers in some form. This PRD adds user-declarable property indexes (backed by SQLite expression indexes on `json_extract`) and a uniqueness constraint (backed by a `UNIQUE` index on the same expression), scoped to a single label + property key, which is the common case and keeps the SQL surface simple.

## Goals

- Users can declare an index on `(label, propertyKey)` so that `WHERE n.propertyKey = <value>` (and label-scoped equality lookups generally) use an index instead of a full scan.
- Users can declare a uniqueness constraint on `(label, propertyKey)`, enforced at the SQLite level, returning a clear domain error (not a raw SQLite constraint-violation message) on violation.
- Both features work for nodes; relationship property indexes are out of scope for v1 (see Non-Goals).
- The planner/translator automatically uses a declared property index when a compiled query's `WHERE` clause matches its shape, with no Cypher syntax changes required to *use* an index — declaring it is enough.

## Non-Goals

- No `CREATE INDEX`/`CREATE CONSTRAINT` Cypher syntax in v1 — indexes and constraints are declared via a new Go API (`(*DB) CreatePropertyIndex(ctx, label, property string) error` / `(*DB) CreateUniqueConstraint(...)`), not parsed Cypher. Cypher-syntax `CREATE INDEX`/`CREATE CONSTRAINT` (matching Neo4j's DDL-in-Cypher style) can be layered on top later without changing the storage design.
- No composite (multi-property) indexes in v1.
- No full-text search (FTS5) — a larger, separate feature with its own query-syntax questions (`CONTAINS`-style matching already exists via `LIKE`; FTS5 is a distinct ranking/tokenization feature).
- No relationship property indexes in v1 — nodes only, since node-property lookups are the overwhelmingly common case (`MATCH (n:Label {prop: $v})`).
- No existence constraints (`ASSERT EXISTS`) in v1 — deferred; uniqueness is the higher-value, more commonly requested feature.

## Requirements

### Functional Requirements

- REQ-F-001: `store/schema.go` gains a new table `property_index_meta(label TEXT, property TEXT, unique INTEGER, PRIMARY KEY(label, property))` recording which (label, property) pairs have a declared index and whether it is unique — needed because SQLite expression indexes on `json_extract` cannot be introspected generically at query-plan time without knowing the exact property key in advance.
- REQ-F-002: `store.Store` gains `CreatePropertyIndex(ctx, label, property string) error` and `CreateUniqueConstraint(ctx, label, property string) error`. Both create a SQLite index of the form `CREATE INDEX idx_prop_<hash> ON nodes(json_extract(props, '$.<property>')) WHERE ... label filter ...` — since SQLite partial/expression indexes cannot directly filter on the comma-separated `labels` column efficiently, the index is a plain expression index on `json_extract(props, '$.<property>')` across all nodes (not partial by label), and label-scoping happens at query time via the existing `node_labels` join; the unique variant is `CREATE UNIQUE INDEX ...` on the same expression, scoped correctly (see Technical Considerations for how uniqueness composes with labels).
- REQ-F-003: `graphlite.DB` exposes `(*DB) CreatePropertyIndex(ctx, label, property string) error` and `(*DB) CreateUniqueConstraint(ctx, label, property string) error` as new public API surface, delegating to the store layer.
- REQ-F-004: A uniqueness violation on `CREATE`/`SET` surfaces as a new structured error type (following the existing `ErrImportDepthExceeded`/`ErrImportTooLarge` pattern) — e.g. `ErrConstraintViolation{Label, Property string}` — detected the same way `AGENTS.md` documents FOREIGN KEY errors are caught today: `strings.Contains(err.Error(), "UNIQUE constraint failed")`, translated to the domain error before returning from `InsertNode`/`execWriteBatch`'s `KindInsertNode`/`KindUpdate` cases.
- REQ-F-005: `sql/translator.go`'s WHERE-clause compilation for a simple label + property equality (`MATCH (n:Label) WHERE n.prop = $v` or inline `MATCH (n:Label {prop: $v})`) is unchanged in its generated SQL shape (`json_extract(props, '$.prop') = ?`) — the index is picked up automatically by SQLite's query planner because it matches that exact expression; no translator changes are required for the *lookup* path, only for index creation/management (a key simplifying property of expression indexes).
- REQ-F-006: `(*DB) ListPropertyIndexes(ctx) ([]PropertyIndexInfo, error)` and `(*DB) DropPropertyIndex(ctx, label, property string) error` round out basic index lifecycle management.

### Non-Functional Requirements

- REQ-NF-001: `go build ./...`, `go vet ./...`, `CGO_ENABLED=0 go test -tags=unit -count=1 ./...` pass.
- REQ-NF-002: A benchmark demonstrates a measurable improvement (query plan uses the index — verifiable via `EXPLAIN QUERY PLAN`, and/or wall-clock improvement on a dataset large enough to matter) for an indexed equality lookup versus the unindexed baseline.
- REQ-NF-003: `store/` package boundary is respected: `CreatePropertyIndex`/`CreateUniqueConstraint` accept only raw strings (label, property key), never Cypher AST types, consistent with `AGENTS.md`'s constraint that `store/` never imports Cypher types.
- REQ-NF-004: Index/constraint DDL uses SQLite identifier-quoting for the generated index name (derived from a hash of label+property, not naive string concatenation) to avoid any SQL-identifier-injection risk from property names containing unusual characters.
- REQ-NF-005: Existing TCK pass rate is unaffected (no Cypher syntax changes in this PRD).

## Technical Considerations

**Why expression indexes, not a new column:** Properties are stored as opaque JSON (`props JSON` column) precisely so the schema doesn't need to change per property. `CREATE INDEX ... ON nodes(json_extract(props, '$.email'))` is SQLite's supported mechanism for indexing inside a JSON blob without denormalizing it into its own column — this is a well-established SQLite pattern and requires no schema migration beyond adding the index itself.

**Uniqueness composing with labels is the trickiest part.** A plain `CREATE UNIQUE INDEX ON nodes(json_extract(props, '$.email'))` would enforce global uniqueness across *all* nodes regardless of label, which is wrong for the Neo4j-style semantics of "unique per label." SQLite partial indexes (`CREATE UNIQUE INDEX ... WHERE <condition>`) can filter on a condition, but that condition would need to reference `node_labels` via a subquery, which SQLite's partial-index `WHERE` clause does not support (it must be a deterministic expression over the indexed table's own columns). The practical solution: keep `nodes.labels` (the existing comma-separated denormalized column, already indexed by `idx_nodes_labels`) as the condition source for the partial index — `CREATE UNIQUE INDEX ... ON nodes(json_extract(props, '$.email')) WHERE labels LIKE '%Label%'` is unsafe/wrong for multi-label overlap (`LIKE` false-positives across label name substrings). The safer approach, and the one this PRD adopts: **require single-label nodes for any node participating in a uniqueness constraint** (documented restriction, checked at `CreateUniqueConstraint` time by scanning existing nodes with that label for multi-label conflicts, and enforced going forward via application-level validation in `execWriteBatch`'s `KindInsertNode` case before the INSERT, in addition to the unique index catching the general case). This restriction should be revisited if it proves too limiting in practice (see Open Questions).

**Index creation is a potentially long-running, exclusive operation** — `CreatePropertyIndex`/`CreateUniqueConstraint` should run outside any user transaction (SQLite `CREATE INDEX` on an existing table scans and can take a write lock for the duration), and should be documented as such (similar to how Neo4j's index creation is asynchronous/background — v1 here can be synchronous/blocking, documented clearly, with async as a future enhancement).

**Interaction with `execWriteBatch`'s existing FOREIGN KEY error handling**: `AGENTS.md` already documents `strings.Contains(err.Error(), "FOREIGN KEY constraint failed")` as the established pattern for translating SQLite constraint errors to domain errors in `InsertEdge` callers — this PRD's `UNIQUE constraint failed` detection follows the identical pattern for consistency.

## Acceptance Criteria

- [ ] `db.CreatePropertyIndex(ctx, "Person", "email")` succeeds and `EXPLAIN QUERY PLAN` for `MATCH (n:Person) WHERE n.email = $v RETURN n` shows index usage (not a full table scan).
- [ ] `db.CreateUniqueConstraint(ctx, "Person", "email")` succeeds; a subsequent `CREATE (n:Person {email: 'dup@x.com'})` that duplicates an existing value returns a structured `ErrConstraintViolation`, not a raw SQLite error string.
- [ ] Attempting `CreateUniqueConstraint` against existing data that already violates uniqueness returns a clear pre-flight error rather than a confusing SQLite failure mid-creation.
- [ ] Attempting `CreateUniqueConstraint` where existing nodes with the target label carry additional labels (multi-label) returns a clear, documented error rather than silently under-enforcing.
- [ ] `db.ListPropertyIndexes(ctx)` returns all declared indexes/constraints with their label, property, and uniqueness flag.
- [ ] `db.DropPropertyIndex(ctx, label, property)` removes the index and its `property_index_meta` row.
- [ ] A benchmark shows the indexed lookup path measurably faster than the unindexed baseline on a dataset large enough to matter (e.g. 100k+ nodes).
- [ ] All existing unit tests and TCK scenarios pass unchanged.

## Out of Scope

- Cypher `CREATE INDEX`/`CREATE CONSTRAINT` syntax (Go API only, in v1).
- Composite/multi-property indexes.
- Relationship property indexes.
- Full-text search (FTS5).
- Existence constraints.
- Asynchronous/background index population.

## Open Questions

- Is the "single-label-only" restriction on uniqueness constraints acceptable, or does real usage need multi-label nodes to participate correctly? If the latter, the fallback is an application-level uniqueness check (SELECT-then-INSERT under a transaction) instead of a SQLite-level `UNIQUE` index, trading some concurrency safety for correctness under multi-label nodes.
- Should `CreatePropertyIndex` validate that the label actually exists in the graph before creating the index, or allow declaring indexes for labels that don't exist yet (matching Neo4j's "schema exists ahead of data" model)? Recommend allowing it (matches Neo4j semantics, and is simpler).
