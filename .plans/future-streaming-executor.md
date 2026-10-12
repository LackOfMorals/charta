# Future work: the executor materialises every row, node and relationship

Status: read-only queries now stream (see "What is done"); lazy node decoding and the relationship-first path remain. Recorded 2026-10-09 from the csvImport
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

- Streaming executor for read-only queries (`cypher/interp/stream.go`): MATCH/UNWIND/WITH/RETURN pipeline with early
  termination (a LIMIT stops the matcher), incremental aggregation (an accumulator set per group, not every row),
  streaming DISTINCT, page-at-a-time label and property scans, and bounded node/relationship/adjacency caches for
  statements that only read. The identity question this plan raised is settled: equality, ordering and grouping go by id
  everywhere (`value.go`), so for a read-only statement the caches are only an optimisation; for a statement that writes
  they are not (snapshots and created/deleted tracking are keyed by `*Node`), so they stay. Measured on a synthetic
  Companies House-shaped graph (1M companies, 250k addresses, 1.5M relationships), every result identical:

  | query | clause by clause | streaming |
  |---|---|---|
  | `MATCH (c:Company) RETURN c.name LIMIT 10` | 1.3 s, 1.5 GB | 1 ms, 27 MB |
  | `... (c)-[:REGISTERED_AT]->(a) RETURN ... LIMIT 10` | 3.4 s, 2.3 GB | 2 ms, 28 MB |
  | `MATCH (c:Company) WHERE c.score > 90 RETURN count(c)` | 1.1 GB | 95 MB |
  | `MATCH (c:Company) RETURN c.status, count(*)` | 1.3 GB | 95 MB |
  | `MATCH (c:Company) RETURN DISTINCT c.category` | 1.6 GB | 95 MB |
  | two-hop `count(DISTINCT c)` per town | 3.6 s, 1.6 GB | 3.8 s, 140 MB |
  | `WITH a, count(c) AS k WHERE k > 12` | 3.6 s, 2.3 GB | 4.0 s, 610 MB |

  Time on a full aggregation is unchanged (decoding every node dominates); the multi-hop ones are about 5% slower
  because trimmed hot nodes are decoded again. A query that returns every row (`MATCH (n) RETURN n`) pays about 2% for
  the pipeline and its paging; the results are the same row for row (`TestStreamAgreesWithClauseAtATime`).

## What is not done (the suggested solutions, in order of value)

1. **Streaming executor: done for read-only queries** (see "What is done"). Still clause by clause: any query with an
   updating clause, `CALL`, `FOREACH` or `LOAD CSV`; relationship-first matching (`relfirst.go`) and `allRels` still read
   every relationship at once; ORDER BY buffers all its rows (a bounded top-N for ORDER BY ... LIMIT would not).
2. **Lighter nodes.** Decode `Props` lazily (keep the raw JSON text, decode on first access, or extract only the keys the
   query names, like `vector.StoredVector` does for vectors), and make `row` a slice indexed by variable slot instead of a map.
3. **Bounded node/relationship cache for read-only statements: done** (`trimCaches`; the audit found no pointer
   comparison that matters for a read-only statement). A smarter policy than "drop everything past `cacheLimit`" (keep
   hot far nodes, evict start nodes as soon as their chunk is done) would recover most of the ~5% it costs on multi-hop
   aggregations.
4. **More pushdown shapes** if 1-3 are deferred: two-hop patterns, DISTINCT aggregates, `<>` and `IS NULL` predicates,
   undirected relationships (each edge matches both ways), `WITH ... count(*) AS c MATCH ...` chains.

## How to measure

The csvImport benchmark harness is no longer in the repository (it is git-ignored, with its 598 MB `import.csv`). The last
tracked version is in history: `git show f1576c0:examples/csvImport/main.go > examples/csvImport/main.go`, then
`go run ./examples/csvImport -prepare -load -db /tmp/charta_csv.db` and
`go run ./examples/csvImport -query -db /tmp/charta_csv.db [-only name] [-cpuprofile f]`.
Correctness harness for any new fast path: compare against the interpreter with the plan disabled, as
`TestAggregatePushdownAgrees` (flag `aggPushDisabled`) and `TestReversedPatternsAgree` do.
