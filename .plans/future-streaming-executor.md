# Future work: the executor materialises every row, node and relationship

Status: known limitation, partly mitigated (see "What is done"). Recorded 2026-10-09 from the csvImport
measurements (3.1M requests, 9.4M relationships; `.plans/progress-csv-import-performance.txt`).

## The issue

`cypher/interp` evaluates a query clause by clause and every clause materialises its whole result as `[]row`
(`map[string]any` per row). For `MATCH (r:Request) RETURN r.status, count(*)` over 3.1M nodes that means, per node:

- a `*Node` with a decoded `Props` map and a `Labels` slice, kept for the statement in `graph.nodes`
  (never evicted, because pointer/ID identity is used all over the interpreter),
- a `row` map, copied again by every `row.with(...)`,
- (for scans) the raw text of the row until it is decoded.

Measured: about 2 KB and 1.3 us of Go allocation per node (runtime.madvise ~20% of CPU, GC ~22%), 6.2 GB peak RSS and
6-7 s for one aggregation over 3.1M nodes; traversals that aggregate over millions of relationships took 20-50 s and
the whole benchmark run reached 15 GB RSS. SQLite itself is only about 2 s of that. GC settings (GOGC), SQLite cache_size
and mmap_size made no difference.

## What is done

- Whole-label scans decode rows as they arrive and drive from `node_labels` (`graph.scanNodes`).
- Aggregation pushdown (`cypher/interp/aggpush.go`): `MATCH (a[:L]) | (a[:L])-[e[:T]]->(b[:M]) [WHERE <AND of
  prop op literal|param>]` followed directly by `RETURN|WITH` with `var.key` grouping keys and
  count/count(var)/count(var.key)/min/max/sum/avg(var.key) runs as one SQL GROUP BY. Property kinds SQL cannot reproduce
  (lists, maps/temporals, mixed int/real keys, mixed types under min/max/sum, float sums) fall back to the interpreter.
  Single-node patterns that read properties are NOT pushed (SQLite's json functions are no faster than the interpreter's
  decoder there). 3.1M-row graph: busiest clients 21 s -> 6.7 s, per user agent 47 s -> 6.7 s, errors per region
  20 s -> 6.2 s; `count(*)` per label ~0.3 s.

## What is not done (the suggested solutions, in order of value)

1. **Streaming executor.** Pull-based row iterators (MATCH -> filter -> project/aggregate) instead of `[]row` per clause,
   so an aggregation holds one group table, not every row. Order-by/DISTINCT/collect still buffer; everything else
   streams. This bounds memory for every query shape (the pushdown only helps recognisable ones) and is the fix for
   multi-hop queries such as "clients that hit the same instances as one client" (still ~30 s).
   Prerequisite: decide the node-identity story (below).
2. **Lighter nodes.** Decode `Props` lazily (keep the raw JSON text, decode on first access, or extract only the keys the
   query names, like `vector.StoredVector` does for vectors), and make `row` a slice indexed by variable slot instead of a map.
3. **Bounded node/relationship cache for read-only statements.** Identity is by ID in most places but some code compares
   pointers; audit that first, then evict (or never retain) nodes that no row references any more.
4. **More pushdown shapes** if 1-3 are deferred: two-hop patterns, DISTINCT aggregates, `<>` and `IS NULL` predicates,
   undirected relationships (each edge matches both ways), `WITH ... count(*) AS c MATCH ...` chains.

## How to measure

The csvImport benchmark harness is no longer in the repository (it is git-ignored, with its 598 MB `import.csv`). The last
tracked version is in history: `git show f1576c0:examples/csvImport/main.go > examples/csvImport/main.go`, then
`go run ./examples/csvImport -prepare -load -db /tmp/charta_csv.db` and
`go run ./examples/csvImport -query -db /tmp/charta_csv.db [-only name] [-cpuprofile f]`.
Correctness harness for any new fast path: compare against the interpreter with the plan disabled, as
`TestAggregatePushdownAgrees` (flag `aggPushDisabled`) and `TestReversedPatternsAgree` do.
