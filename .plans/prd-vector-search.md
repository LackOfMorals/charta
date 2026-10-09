# PRD: Pure-Go Vector Similarity Search (Exploratory)

> **Status update (2026-10-09):** The VECTOR value, similarity/distance functions and CREATE VECTOR INDEX metadata exist (Neo4j naming). Remaining: validate index options, enforce dimension on write, brute-force db.index.vector.queryNodes and DB.VectorSearch, benchmarks. See tasks-vector-search.yml.

## Overview

The single most-cited feature developers looked for in KuzuDB (archived October 2025 after Apple acqui-hired the team) was its built-in HNSW vector index, used for hybrid graph+vector retrieval in GraphRAG-style applications — a workload that moved from research to production through 2026 (Microsoft GraphRAG, LightRAG, HippoRAG; Neo4j and ArangoDB both shipped native vector indexes for the same reason). This is graphlite's clearest differentiation opportunity given the Kuzu-shaped gap in the Go ecosystem, but it comes with a hard architectural constraint that every comparable implementation (`sqlite-vec`, most HNSW libraries) violates: `AGENTS.md` mandates **CGO must remain disabled** — `modernc.org/sqlite`, never `mattn/go-sqlite3`, and by extension no C-extension SQLite loadable modules like `sqlite-vec`. This PRD is deliberately marked exploratory: it proposes a pure-Go approach (brute-force or a pure-Go approximate index) and calls out the open questions that need resolving — scale limits, index persistence, and library-vs-build-it-yourself — before committing to an implementation plan.

## Goals

- Node and relationship properties can hold a fixed-dimension float vector (already possible today — a JSON array of floats in the existing `props` column — no storage schema change required for *storing* vectors).
- A new Cypher function or Go API computes similarity (cosine similarity and/or dot product, minimally) between a query vector and a stored vector, usable in `ORDER BY`/`WHERE`/`RETURN`.
- A `(*DB) CreateVectorIndex(ctx, label, property string, dim int) error` accelerates top-k similarity search beyond brute force, for graphs large enough that a full scan is too slow — the exact mechanism (exact brute-force with SIMD-friendly Go, or an approximate pure-Go index) is an open question this PRD frames but does not fully resolve (see Open Questions).
- No CGO is introduced anywhere in the dependency graph — this is a hard constraint, not a preference.

## Non-Goals

- No `sqlite-vec` or any other C-extension integration — this violates the CGO-free constraint outright and is not reconsidered by this PRD.
- No multi-modal embedding generation (calling out to an embedding model) — graphlite stores and searches vectors a caller already computed; it does not compute embeddings itself.
- No distributed/sharded vector index — this remains a single-process, single-file embedded feature, consistent with graphlite's overall scope.
- No guarantee of matching HNSW's asymptotic query performance at very large scale (millions of vectors) in the first version — see Open Questions on where brute-force stops being acceptable.

## Requirements

### Functional Requirements

- REQ-F-001: A new scalar function (working name: `vectorSimilarity(a, b)` or `cosineSimilarity(a, b)`, exact naming TBD — see Open Questions) computes cosine similarity between two JSON float-array-valued expressions, usable anywhere a normal scalar expression is (this depends on `prd-cypher-baseline-coverage.md`'s `ScalarCallExpr` machinery landing first, or is added standalone if that PRD hasn't shipped).
- REQ-F-002: `(*DB) CreateVectorIndex(ctx, label, property string, dim int) error` declares a vector index on a node label + property, validating that `dim` is consistent with stored values (rejecting mismatched-dimension vectors at index-creation and at subsequent write time, similar in spirit to `prd-property-indexes-constraints.md`'s `property_index_meta` bookkeeping table).
- REQ-F-003: A top-k query form is added — either a new Cypher function (`vectorTopK(label, property, queryVector, k)`) or a Go-level API (`(*DB) VectorSearch(ctx, label, property string, query []float64, k int) ([]VectorMatch, error)`) — returning the `k` nearest nodes by similarity. Given the Cypher-integration complexity of a "search" operation that isn't naturally expressible as a `WHERE`/`ORDER BY` predicate without computing similarity for every row first, **the Go-level API is the recommended starting point** (see Technical Considerations), with a Cypher-level form as a possible follow-up once the semantics are proven out.
- REQ-F-004: The v1 implementation is brute-force: `VectorSearch` computes similarity against every indexed vector and returns the top-k, implemented in pure Go (no SQL-level vector math beyond fetching the raw JSON arrays) — this is correct and simple, and is explicitly framed as the baseline that a future approximate index (HNSW-in-Go or similar) would need to beat on measured workloads before being adopted.
- REQ-F-005: Vector values are validated on write (dimension matches the declared index, all elements are numeric) when a `CreateVectorIndex` exists for that label+property, returning a clear structured error on mismatch.

### Non-Functional Requirements

- REQ-NF-001: `go build ./...`, `go vet ./...`, `CGO_ENABLED=0 go test -tags=unit -count=1 ./...` pass — CGO-free-ness is directly tested by the existing build command already requiring `CGO_ENABLED=0`.
- REQ-NF-002: No new dependency introduces CGO, verified by checking `go list -deps` for any package requiring cgo (`go list -f '{{.CgoFiles}}' ./...` across the full dependency tree returns empty).
- REQ-NF-003: A benchmark establishes the brute-force baseline's performance characteristics (queries/sec at N=1k/10k/100k/1M vectors, at a fixed dimension e.g. 384 or 768 matching common embedding models) — this data is what any future approximate-index proposal must be justified against.
- REQ-NF-004: `store/` package boundary is respected — vector values remain opaque JSON in the existing `props` column; `store/` gains no vector-specific typed API, consistent with `AGENTS.md`'s "raw IDs, labels, JSON blobs only" constraint.

## Technical Considerations

**Why brute-force first:** Cosine similarity over a few hundred to a few thousand vectors of typical embedding dimension (384–1536) is genuinely fast in Go — a naive loop with no SIMD can do this at a scale that covers a large fraction of realistic embedded-use-case graph sizes (this is an embedded database, not a dedicated vector store competing with Pinecone/Weaviate at billion-scale). Shipping brute-force first, benchmarked honestly, avoids committing to an approximate index's accuracy/recall tradeoffs before there's real usage data motivating it.

**Why a Go API, not pure Cypher, for the top-k form:** openCypher has no native "vector search" clause (this is exactly why Kuzu, Neo4j, and ArangoDB all added *procedure-call* or *function*-based vector search rather than new clause syntax). A Cypher function returning a list of matches, called from `WITH vectorTopK('Person', 'embedding', $q, 10) AS matches UNWIND matches AS m ...`, is possible once `UNWIND` exists (`prd-cypher-baseline-coverage.md`), but the ergonomics of a table-returning function inside Cypher add real parser/planner complexity. Shipping the Go-level `VectorSearch` API first de-risks the feature and gets it in users' hands sooner; a Cypher-integrated form is a natural follow-up once `UNWIND` + table-valued function support exists.

**Pure-Go approximate index libraries exist** (e.g. HNSW implementations in pure Go) and could replace the brute-force scan later without changing the public API shape (`VectorSearch`'s signature doesn't need to change; only what backs `CreateVectorIndex` does) — but adopting a new dependency here should follow the project's stated "a little recode > a big dependency" philosophy (`AGENTS.md`/skill guidance) and be justified by the REQ-NF-003 benchmark data showing brute-force is actually insufficient at a scale users hit in practice, not adopted speculatively.

**Storage**: vectors need no schema change — they are just JSON float arrays in the existing `props` column, exactly like any other property. This keeps the feature's storage footprint at zero migration cost; the only new persistent state is the `property_index_meta`-style bookkeeping table (or an extension of it, if `prd-property-indexes-constraints.md` lands first) recording declared vector indexes and their dimension.

## Acceptance Criteria

- [ ] A node property can hold a JSON float array (already true today — validated by a test, not new behavior).
- [ ] `vectorSimilarity(a, b)` (or the chosen name) computes correct cosine similarity for two equal-dimension vectors and returns a clear error for mismatched dimensions.
- [ ] `db.CreateVectorIndex(ctx, "Document", "embedding", 384)` succeeds and records the declared dimension.
- [ ] Writing a vector of the wrong dimension to an indexed property returns a clear structured error.
- [ ] `db.VectorSearch(ctx, "Document", "embedding", queryVec, 10)` returns the 10 nearest nodes by cosine similarity, correctly ranked.
- [ ] A benchmark documents brute-force query throughput at N=1k/10k/100k/1M vectors at a realistic embedding dimension.
- [ ] `go list -f '{{.CgoFiles}}' ./...` (or equivalent) confirms zero CGO files anywhere in the dependency tree after this PRD.

## Out of Scope

- `sqlite-vec` or any C-extension vector index.
- Embedding generation.
- Distributed/sharded vector search.
- A Cypher-native vector-search clause (Go API only, in v1).
- Guaranteed sub-linear query time at arbitrary scale.

## Open Questions

- **Naming**: `vectorSimilarity`/`cosineSimilarity`/matching Neo4j's `vector.similarity.cosine` naming convention for familiarity — needs a decision before REQ-F-001 is implemented.
- **What similarity metrics are required for v1** — cosine only, or also dot product and Euclidean distance (common alternatives depending on how the embeddings were trained/normalized)? Recommend starting with cosine (the most common default) and adding others if requested.
- **At what N does brute-force stop being acceptable**, and does that threshold justify a pure-Go approximate index investment? This is explicitly deferred to the REQ-NF-003 benchmark data — no decision should be made without it.
- **Should `VectorSearch` support metadata filtering** (e.g. "top-10 similar `Document` nodes where `n.year > 2020`") in v1, or is unfiltered top-k sufficient to start? Filtering composes naturally with the brute-force approach (filter first, then rank) but adds API surface — recommend deferring unless there's a concrete use case driving it.
