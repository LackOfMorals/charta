# PRD: shortestPath() and Basic Path-Length Traversal Algorithms

> **Status update (2026-10-09):** Delivered in the interpreter: shortestPath(), allShortestPaths(), all path selectors, quantified path patterns, charta.Path and the path functions. The CTE design below was never built because the SQL translator was removed. Remaining work is performance (parent-pointer and bidirectional search) and explosion guards. See tasks-shortest-path-traversal.yml.

## Overview

charta already compiles variable-length relationship patterns (`-[*1..n]->`) into a `WITH RECURSIVE` CTE that tracks `end_id` and `depth` per reachable node (`sql/translator.go:908-1044`, `buildFromClauseForVarLengthRel`). This is precisely the machinery `shortestPath()`/`allShortestPaths()` need — both are "find the minimum-depth reachable node(s) matching a pattern," which the existing CTE already computes as `depth`. Today, however, `shortestPath()`/`allShortestPaths()` are explicitly on the unsupported list (`compat/tck_test.go:135-136`), and there is no path-value type at all: `nodes(path)`/`relationships(path)` (also unsupported — see `prd-cypher-baseline-coverage.md`) have nothing to operate on. This PRD adds `shortestPath()`/`allShortestPaths()` as an extension of the existing variable-length-path CTE (ORDER BY depth, LIMIT to the shortest), and introduces a minimal path-value representation sufficient for those two functions plus `nodes()`/`relationships()`, without attempting general graph algorithms (PageRank, centrality, community detection) — those are a much larger, separate investment and are explicitly out of scope here.

## Goals

- `shortestPath((a)-[*..n]-(b))` returns the shortest path between two bound nodes (or an error if none exists / no bound within `n` hops), reusing the existing recursive-CTE hop-tracking machinery.
- `allShortestPaths((a)-[*..n]-(b))` returns all paths tied for shortest length.
- A minimal `Path` value exists (in Go: analogous to `Node`/`Relationship`, exposing ordered nodes and relationships) sufficient to support `nodes(path)`/`relationships(path)` and `length(path)`.
- Remove `shortestPath(`/`allShortestPaths(` from `compat/tck_test.go`'s skip list and pass the corresponding TCK scenarios.

## Non-Goals

- No general-purpose graph algorithms library (PageRank, betweenness centrality, community detection, Louvain, connected components) — this PRD is scoped strictly to shortest-path-by-hop-count, which the existing CTE already computes as a side effect of bounding variable-length patterns.
- No weighted shortest path (Dijkstra) — openCypher's `shortestPath()` is unweighted (hop count only); weighted pathfinding is a distinct, larger feature with its own semantics questions (which property is the weight? how are missing weights handled?) and is deferred.
- No named path variables in general (`p = (a)-->(b)`) beyond what's needed to bind the result of `shortestPath()`/`allShortestPaths()` itself — full named-path-variable support (assigning any pattern to a variable) remains out of scope, tracked separately if ever pursued.
- No changes to `store/` — this remains entirely a `cypher/`/`sql/` translation concern; the recursive CTE runs against the existing `nodes`/`edges` tables unchanged.

## Requirements

### Functional Requirements

- REQ-F-001: `cypher/ast.go`/`cypher/plan.go` gain a `ShortestPathExpr` node (`Pattern PatternPart`, `All bool` distinguishing `shortestPath()` from `allShortestPaths()`) — parsed in `cypher/parser.go`'s function-invocation handling (`buildFunctionInvocation`, `cypher/parser.go:1392`) as a special case recognized before the generic scalar-function dispatch introduced by `prd-cypher-baseline-coverage.md`, since its single argument is a *pattern*, not an expression.
- REQ-F-002: `cypher/planner.go` plans `ShortestPathExpr` into a `ShortestPathPlan` node that reuses `planPatternPart`'s existing variable-length-pattern handling (`cypher/planner.go:279-415`) with an implicit unbounded-or-explicit hop range, forcing `VarLength = true` even if the user wrote a fixed-length pattern inside `shortestPath(...)`.
- REQ-F-003: `sql/translator.go` extends `buildFromClauseForVarLengthRel` (or adds a sibling function reusing its CTE-building core) so that when the pattern is wrapped in `shortestPath()`, the generated CTE additionally tracks the path taken (a JSON array of visited node/edge ids per row, accumulated through the recursive step) and the final `SELECT` orders by `depth ASC` with `LIMIT 1` (for `shortestPath()`) or `WHERE depth = (SELECT MIN(depth) ...)` (for `allShortestPaths()`).
- REQ-F-004: A new `charta.Path` type is added (in `types.go`, alongside `Node`/`Relationship`): `type Path struct { Nodes []Node; Relationships []Relationship }`. Query results binding a `shortestPath()`/`allShortestPaths()` call materialize a `Path` value in the returned `Record`, following the existing pattern used for `Node`/`Relationship` result materialization in `result.go`.
- REQ-F-005: `length(path)` (from `prd-cypher-baseline-coverage.md`'s scalar-function work, or added here if that PRD hasn't landed) returns `len(Path.Relationships)`.
- REQ-F-006: `nodes(path)`/`relationships(path)` (deferred/unsupported in `prd-cypher-baseline-coverage.md` pending path values) are implemented here once `Path` exists, returning `Path.Nodes`/`Path.Relationships` as list values.
- REQ-F-007: If no path exists within the pattern's hop bound, `shortestPath()` returns a null path value (matching openCypher semantics: `shortestPath()` on unmatched input yields `null`, not an error) — consistent with how `OPTIONAL MATCH` already produces null rows (`driver.go:363-373`, `buildOptionalNullRow`).
- REQ-F-008: Remove `"shortestPath("` and `"allShortestPaths("` from `compat/tck_test.go`'s `unsupportedPatterns`.

### Non-Functional Requirements

- REQ-NF-001: `go build ./...`, `go vet ./...`, `CGO_ENABLED=0 go test -tags=unit -count=1 ./...` pass.
- REQ-NF-002: The generated recursive CTE's safety cap (`maxPathHops`, default 15, `options.go:40-58`) applies identically to `shortestPath()`/`allShortestPaths()` patterns — no separate unbounded-recursion risk is introduced.
- REQ-NF-003: `cypher/` still does not import `store/` or `sql/`; `store/` is untouched by this PRD entirely.
- REQ-NF-004: Path-tracking JSON accumulation in the CTE does not regress performance of ordinary (non-shortest-path) variable-length queries — verified by benchmark comparison against the pre-PRD baseline for existing `-[*1..n]->` queries.
- REQ-NF-005: TCK pass rate on executed scenarios remains ≥ its pre-PRD value; the newly-executed `shortestPath`/`allShortestPaths` scenarios pass.

## Technical Considerations

**Reusing the existing CTE is the entire point of sequencing this after the variable-length-path work already shipped.** `buildFromClauseForVarLengthRel`'s CTE (`sql/translator.go:908-1044`) already computes `(end_id, depth)` pairs recursively; `shortestPath()` only needs (a) path accumulation (which nodes/edges were visited to reach each `end_id` at each `depth` — SQLite's recursive CTEs can carry an accumulating JSON array column through the recursive step via `json_insert`/string concatenation of ids) and (b) a final `ORDER BY depth LIMIT 1` / `MIN(depth)` selection instead of returning every reachable row.

**Path accumulation cost**: carrying a growing JSON array through every recursive step is more expensive than the current depth-only tracking. This is why REQ-NF-004 requires confirming ordinary variable-length queries (which don't need path accumulation) are unaffected — the path-tracking columns should only be added to the CTE when the pattern is actually wrapped in `shortestPath()`/`allShortestPaths()`, not unconditionally.

**Multiple shortest paths of equal length**: `allShortestPaths()` requires the CTE to retain *all* rows at the minimum depth, not just the first found — SQLite recursive CTEs by default will enumerate all paths at each depth (not just one), so this is a `WHERE depth = MIN(depth)` filter on the final `SELECT`, not a change to the recursive step itself. Be careful of cycles in the graph producing multiple non-simple paths at the same depth; the existing recursion already has a hop cap (`maxPathHops`) that bounds this.

**`Path` materialization in `result.go`**: `Node`/`Relationship` are already materialized from SQL rows in the existing result pipeline (per `AGENTS.md`'s notes on `newResultFromRows`/`newRecord`'s pre-allocated scan buffers). `Path` values require the SQL SELECT to project the accumulated node/edge id arrays as JSON, then a Go-side step (analogous to existing JSON-to-struct decoding, per `AGENTS.md`'s `convertTo[T]` notes) that fetches the actual `Node`/`Relationship` rows for each id in the path — this may require one or more follow-up queries per result row (batched, not N+1, where possible) rather than a single flat SELECT, since a path is a variable-length nested structure.

## Acceptance Criteria

- [ ] `MATCH (a:Person {name: 'Alice'}), (b:Person {name: 'Bob'}) RETURN shortestPath((a)-[*..5]-(b))` returns a `Path` with the correct minimal hop count.
- [ ] `shortestPath()` returns a null path (not an error) when no path exists within the hop bound.
- [ ] `allShortestPaths()` returns all paths tied for minimum length when multiple exist.
- [ ] `length(shortestPath(...))` returns the correct relationship count.
- [ ] `nodes(path)`/`relationships(path)` return the correct ordered lists for a bound path variable.
- [ ] The existing `maxPathHops` safety cap applies to `shortestPath()`/`allShortestPaths()` patterns.
- [ ] A benchmark confirms no regression to existing (non-shortest-path) variable-length query performance.
- [ ] `"shortestPath("`/`"allShortestPaths("` are removed from `compat/tck_test.go`'s skip list; the corresponding TCK scenarios pass.

## Out of Scope

- Weighted shortest path (Dijkstra/A*).
- General graph algorithms (PageRank, centrality, connected components, community detection).
- Full named-path-variable support beyond `shortestPath()`/`allShortestPaths()` results.

## Open Questions

- Should `Path` materialization eagerly fetch full `Node`/`Relationship` structs (with all properties) for every path element, or lazily/partially (ids only, fetched on demand)? Eager is simpler and matches how `Node`/`Relationship` results already work elsewhere; recommend starting there and revisiting if large-path performance becomes an issue.
- Does this PRD's scope (hop-count shortest path) fully satisfy near-term user need, or should basic BFS/reachability-without-a-target-node (`MATCH (a) RETURN a` reachable within N hops, no `shortestPath()` wrapper) be pulled forward from a future "graph algorithms" PRD? Recommend keeping this PRD scoped to the two named functions and treating open-ended traversal algorithms as a separate future initiative once there's concrete demand.
