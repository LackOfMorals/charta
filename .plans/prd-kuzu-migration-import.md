# PRD: Kuzu Migration Import Path (Exploratory)

> **Status update (2026-10-09):** Still valid, still opportunistic (task 1 is a go/no-go). Updates: temporal types now exist so DATE/TIMESTAMP/INTERVAL columns can be supported, and a stateful import session replaces the stateless ImportKuzuCSV method. See tasks-kuzu-migration-import.yml.

## Overview

KuzuDB — the leading embedded Cypher graph database — was archived in October 2025 after Apple acqui-hired the team. Displaced Kuzu users are actively looking for a new home; ArcadeDB has already published a "From KuzuDB to ArcadeDB" migration guide specifically to capture this audience. charta already has a bulk import mechanism (`importer.go`) supporting a Neo4j-bulk-import-style CSV format (`FormatCSVNodes`/`FormatCSVEdges`, using `:ID`/`:LABEL`/`:START_ID`/`:END_ID`/`:TYPE` header conventions) and a JSON format. This PRD adds a Kuzu-compatible import path so that a Go developer whose data currently lives in Kuzu can move it into charta with a documented, mechanical export/import step — a cheap, timely feature that directly targets a real, currently-homeless user population, and doubles as marketing (a "migrating from Kuzu" guide, mirroring ArcadeDB's). This PRD is marked exploratory because Kuzu's exact export schema shape (node/relationship *table* DDL with typed columns, not `:ID`/`:LABEL`-tagged CSV) is different enough from charta's existing importer conventions that real design decisions are needed before implementation, not just plumbing.

## Goals

- A Go developer with an existing Kuzu database can export it (via Kuzu's own `COPY (query) TO 'file.csv'`/Parquet mechanism, run before Kuzu's archival made new installs harder to obtain, or against an already-exported dataset) and import the result into charta with minimal manual reshaping.
- A published, user-facing "Migrating from Kuzu" guide (README section or standalone doc) walks through the process end-to-end, mirroring the positioning of ArcadeDB's existing Kuzu migration guide.
- The new import format handles Kuzu's typed node-table / relationship-table CSV export shape (column names as declared in the Kuzu schema, no `:ID`/`:LABEL` tagging convention) without requiring the user to hand-edit their exported files into charta's existing Neo4j-style CSV format.

## Non-Goals

- No live/direct connection to a running Kuzu instance (Kuzu is archived — there is no ongoing service to connect to; this is a one-time data migration, not an integration).
- No support for Kuzu-specific features that have no charta equivalent (Kuzu's native vector index, full-text search, or graph-algorithm results) — only the underlying graph data (nodes, relationships, scalar properties) migrates. If `prd-vector-search.md` lands, previously-indexed Kuzu vectors migrate as plain float-array properties, re-indexed separately by the user via charta's own `CreateVectorIndex`.
- No automatic Kuzu schema (DDL) translation into charta constructs — the user supplies a mapping (see Requirements) rather than charta parsing Kuzu's `CREATE NODE TABLE`/`CREATE REL TABLE` DDL directly, at least in v1.
- No Parquet import in v1 (Kuzu's other common export format) — CSV only, consistent with charta's existing `encoding/csv`-based importer; Parquet is a larger, separate dependency decision (per the project's "a little recode > a big dependency" philosophy) and is deferred.

## Requirements

### Functional Requirements

- REQ-F-001: A new `Format` constant `FormatKuzuCSVNodes` and `FormatKuzuCSVEdges` (continuing the existing 1-based `Format` enum in `importer.go:18-45`) accept Kuzu's exported CSV shape: a header row of plain declared column names (no `:ID`/`:LABEL` prefix convention), where one designated column is the node's primary key (Kuzu requires every node table to declare a primary key column) and — for relationship tables — two designated columns are the `FROM`/`TO` node-table references.
- REQ-F-002: Because Kuzu's CSV export has no self-describing `:ID`/`:LABEL`/`:START_ID`/`:END_ID`/`:TYPE` markers, the caller must supply a small mapping alongside the file: which column is the primary key, what label to assign all rows from this file (Kuzu's node *tables* are analogous to a single label, since Kuzu's schema is table-per-label rather than charta's flexible multi-label-per-node model), and — for relationship files — which columns are the from/to key columns and what relationship type to assign. This is expressed as a new parameter on the import call, e.g. `(*DB) ImportKuzuCSV(ctx, r io.Reader, mapping KuzuCSVMapping) error` rather than overloading the existing `Import(ctx, r, Format)` signature, since the mapping has no equivalent in the existing Neo4j-style format.
- REQ-F-003: Kuzu's typed columns (Kuzu CSV headers can carry `INT64`/`STRING`/`DOUBLE`/etc. type annotations depending on export settings) are mapped to charta's existing CSV property-type convention (`name:string`, `age:int`, per `importer.go:38`'s documented header format) — either by the user pre-annotating the mapping, or by best-effort type sniffing (string vs. number vs. bool) when Kuzu's export doesn't carry explicit types. Exact behavior is an open question (see below).
- REQ-F-004: Because a node's Kuzu-table primary key is table-scoped, not globally unique across tables, the importer must generate its own file-local `:ID`-equivalent (row-index or PK-value + table-name composite) internally, reusing the existing `FormatCSVNodes`/`FormatCSVEdges` internal machinery (`importer.go`'s existing id-remapping logic, since charta's actual persisted node IDs are always fresh AUTOINCREMENT values regardless of source format, per `AGENTS.md`'s existing note on CSV `:ID` values being file-local).
- REQ-F-005: Multiple Kuzu node-table files (one per label) and relationship-table files can be imported in sequence into the same charta database, with cross-file relationship references resolved correctly (a relationship file's FROM/TO columns reference primary keys from a specific, named node-table file imported earlier in the same session).
- REQ-F-006: A "Migrating from Kuzu" section is added to README.md (or a new `docs/migrating-from-kuzu.md`), walking through: exporting each Kuzu node/relationship table to CSV, constructing the `KuzuCSVMapping` for each file, and calling `ImportKuzuCSV` in the right order (node tables before the relationship tables that reference them).

### Non-Functional Requirements

- REQ-NF-001: `go build ./...`, `go vet ./...`, `CGO_ENABLED=0 go test -tags=unit -count=1 ./...` pass.
- REQ-NF-002: The existing `importMaxBytes` size limit and depth/size error types (`ErrImportTooLarge`, per `AGENTS.md`'s notes on `importJSON`'s size-detection approach) apply identically to the new Kuzu CSV path — no separate unbounded-read code path is introduced.
- REQ-NF-003: Test fixtures include at least one realistic multi-table Kuzu export sample (two node tables, one relationship table referencing both) under `testdata/`, exercising the cross-file reference resolution in REQ-F-005.
- REQ-NF-004: No change to `store/`'s architectural boundary — this remains entirely an `importer.go`-level (root package) concern translating into the existing `store.InsertNode`/`store.InsertEdge` calls, unchanged.

## Technical Considerations

**Why not parse Kuzu's DDL directly:** Kuzu's schema is declared via `CREATE NODE TABLE Person(id INT64, name STRING, PRIMARY KEY(id))`-style DDL, which charta has no parser for and would need to build one to consume automatically. Requiring the user to supply a small, explicit mapping (REQ-F-002) is far cheaper to build and ship, and is honest about the one piece of information that genuinely can't be inferred from the CSV alone (which column is the primary key, what label/type to assign) — this mirrors how the *existing* `FormatCSVNodes`/`FormatCSVEdges` require Neo4j's own `:ID`/`:LABEL` convention rather than charta inferring structure from arbitrary CSV.

**Kuzu's per-table primary keys vs. charta's global AUTOINCREMENT ids**: this is exactly the same "file-local id, remapped to a real database-assigned id" pattern the existing importer already implements for Neo4j-style CSV (`AGENTS.md`: "CSV node `:ID` values are file-local labels only — the actual SQLite primary keys are AUTOINCREMENT-assigned by `InsertNode`"). The Kuzu path's added wrinkle is that the "file-local id" needs a table-name qualifier (since two different Kuzu tables could reuse the same primary-key value, e.g. both `Person` and `Company` having a row with `id=1`) — this is a straightforward extension of the existing remapping logic, not a new mechanism.

**Type sniffing vs. explicit mapping (REQ-F-003) is a real tradeoff**: Kuzu's default CSV export can include a header row with type annotations (`name:STRING`) similar in spirit to charta's own convention, in which case a fairly mechanical translation table (`INT64`→`int`, `STRING`→`string`, `DOUBLE`→`float`, `BOOL`→`bool`) suffices. But Kuzu also supports exporting without type annotations, or with types charta's simpler type system can't represent 1:1 (Kuzu has `DATE`, `TIMESTAMP`, list/struct/map types). v1 should explicitly scope to the common case (scalar `INT64`/`STRING`/`DOUBLE`/`BOOL` columns) and return a clear error naming the unsupported type for anything else, rather than attempting a lossy best-effort conversion.

## Acceptance Criteria

- [ ] `KuzuCSVMapping` type exists, letting a caller specify: primary-key column, label (for node files) or relationship type + from/to columns + referenced table names (for relationship files).
- [ ] `db.ImportKuzuCSV(ctx, r, mapping)` correctly imports a Kuzu-exported node-table CSV, assigning the specified label and mapping typed columns to properties.
- [ ] `db.ImportKuzuCSV(ctx, r, mapping)` correctly imports a Kuzu-exported relationship-table CSV, resolving FROM/TO primary keys against previously-imported node tables.
- [ ] Importing two node tables with overlapping primary-key values (e.g. both have `id=1`) into the same charta database produces two distinct, correctly-referenced nodes — no id collision.
- [ ] An unsupported Kuzu column type (e.g. `DATE`, `LIST`) returns a clear, actionable error rather than silently truncating or misinterpreting the value.
- [ ] `importMaxBytes`/`ErrImportTooLarge` apply identically to this import path.
- [ ] README.md (or a new doc) contains a complete "Migrating from Kuzu" walkthrough.
- [ ] A realistic multi-table test fixture (two node tables + one relationship table) imports correctly end-to-end.

## Out of Scope

- Live connection to a running Kuzu instance.
- Kuzu-specific features with no charta equivalent (native vector/FTS indexes, graph algorithm outputs) beyond migrating the underlying scalar/vector property data itself.
- Automatic parsing of Kuzu's `CREATE NODE TABLE`/`CREATE REL TABLE` DDL.
- Parquet import (CSV only in v1).
- Kuzu's non-scalar column types (`DATE`, `TIMESTAMP`, `LIST`, `STRUCT`, `MAP`) — clear error, not silent conversion.

## Open Questions

- Is a hand-supplied `KuzuCSVMapping` acceptable UX, or does real migration demand exist for parsing Kuzu's DDL directly to auto-generate the mapping? Recommend shipping the manual-mapping version first (much cheaper) and only investing in DDL parsing if user feedback says the manual step is a real adoption blocker.
- Should `DATE`/`TIMESTAMP` Kuzu columns be supported in v1 by mapping to a JSON string property (lossy but functional), or strictly rejected until charta has a real temporal type story? Recommend strict rejection with a clear error in v1, to avoid silently creating unqueryable or misleading data.
- Is there enough evidence of actual Kuzu-migration demand to justify this ahead of the higher-certainty items in this batch (baseline Cypher coverage, concurrent reads)? This PRD is explicitly opportunistic/marketing-driven rather than derived from an existing internal gap — recommend validating real interest (e.g. a GitHub issue, a forum post) before investing engineering time here.
