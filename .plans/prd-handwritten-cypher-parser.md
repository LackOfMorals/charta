# PRD: Hand-written openCypher Parser (replace ANTLR)

## Overview

`cypher.Parse` (`cypher/parser.go`, 1.7k lines) wraps the ANTLR-generated `cloudprivacylabs/opencypher` parser. Measured cost is ~9.5 ms / 13 MB / 135k allocs per parse of an ordinary query. The dependency is pinned to a 2021 ANTLR runtime that cannot be upgraded (`DeserializeFromUInt16` removed upstream; no newer opencypher release), adds ~1.6 MB / 30k lines of vendored generated code, and forces the wrapper to re-serialise expression text out of the CST (91 `GetText()` calls, `exprText`/`encodePropertyExprText`, `RawExpr` fallbacks, property maps stored as `ExprText` strings — GAP-002).

charta is a lightweight embedded graph database aimed at **Neo4j developers**, so the target language is **openCypher 9 plus Neo4j's Cypher extensions** — Neo4j-flavoured syntax is a feature, not a non-goal. This PRD replaces ANTLR with a hand-written lexer + recursive-descent/precedence-climbing parser in its **own package**, `cypher/syntax`, that implements the **complete openCypher 9 grammar and the Neo4j extensions listed below** and produces a fully typed AST. It is delivered over several iterations, with ANTLR kept in place and used as a differential-testing oracle until cut-over.

## Goals

- New package `github.com/LackOfMorals/charta/cypher/syntax`: lexer, parser, fully typed AST, positioned errors. No dependency on ANTLR, `store/`, or `sql/`.
- Accepts the whole openCypher 9 language plus the Neo4j extensions (see "Neo4j extensions") (all clauses, all expression forms, all pattern forms, `UNION`, `CALL`, `FOREACH`, list/pattern comprehensions, quantifier predicates, `reduce`, `CASE`, named paths, `shortestPath`/`allShortestPaths`, parameters, map/list literals, slicing).
- Parse cost ≥100x lower than ANTLR (target < 100 µs, < 300 allocs for the benchmark query).
- Rich syntax errors (line:col, offending token, expected set) in the style of openCypher TCK `SyntaxError` categories.
- Remove `github.com/antlr/antlr4/...` and `github.com/cloudprivacylabs/opencypher` from `go.mod` and `vendor/`.
- End state: **every openCypher TCK scenario passes**, plus a charta-maintained Neo4j-extension scenario suite with an empty skip list in `compat/tck_test.go` (see "Scope of 'complete'").

## Non-Goals

- Server-only Neo4j features that have no meaning for an embedded single-file store: `USE` (multi-database), `SHOW` for users/roles/databases, `GRANT/DENY` and other access-control statements, `LOAD CSV FROM <url>` with remote URLs, `CALL ... IN CONCURRENT TRANSACTIONS`. These are **parsed where cheap** (so users get a clear "not supported in charta" error rather than a syntax error) but not executed.
- Query-planner hints (`USING INDEX`, `USING JOIN`, `USING SCAN`) are parsed and ignored.
- A general-purpose Cypher tool (formatter, LSP, other dialect targets).
- Query optimisation changes beyond what new syntax requires.

## Neo4j extensions

Delivered in tiers. Exact syntax must be checked against the current Neo4j Cypher Manual during each task (the list below is the scope, not the grammar source).

**Target language: Cypher 25 only.** There is a single dialect — no `Dialect` option and no Cypher 5 mode. `CYPHER 25` prefixes are accepted; `CYPHER 5` (or any other version) is rejected with a clear "charta implements Cypher 25" error. Syntax that Cypher 25 removed or changed (e.g. legacy `filter()`/`extract()`, deprecated pattern/label forms, implicit-import `CALL {}`) is **not** supported, to be confirmed item by item against the current Cypher Manual's list of Cypher 25 changes. Where the openCypher TCK exercises such removed syntax, the scenario goes on a documented exclusion list (with the reason) rather than being supported.

**Tier A — core Neo4j extensions (parser iterations):**
- Label expressions: `:A&B`, `:A|B`, `:!A`, `:%`, parenthesised; relationship type expressions (`[:R1|R2]`, `[:!R]`); label predicates `n:A&(B|C)` in WHERE.
- Subquery expressions: `EXISTS { ... }`, `COUNT { ... }`, `COLLECT { ... }`; `CALL { ... }` subqueries incl. importing `WITH` and `CALL { } IN TRANSACTIONS` (parsed; see Non-Goals).
- Quantified path patterns and quantified relationships: `((a)-[:R]->(b)){1,5}`, `-[:R]->+`, `*`, and path selectors `ANY`, `ALL SHORTEST`, `ANY SHORTEST`, `SHORTEST k`, `SHORTEST k GROUPS`. `WHERE` inside node/relationship/parenthesised patterns.
- Map projections `n{.name, .age, key: expr, .*}`; dynamic labels/types/properties `$(expr)` / `n[$prop]`.
- Type predicates `IS [NOT] :: TYPE`, `valueType()`; `IS [NOT] NORMALIZED`; `elementId()`, `nodes()`, `relationships()`.
- `MERGE`/`CREATE`/`SET` Neo4j forms (`SET n:Label`, `SET n += {…}`, `SET n = $map`), `REMOVE n:Label`, `FOREACH`, `UNWIND`, `OPTIONAL CALL`, `YIELD *`.
- `CALL db.xxx() YIELD` procedure calls; `EXPLAIN` / `PROFILE` prefixes (parsed; EXPLAIN may return the plan).
- Schema commands used by charta's own index/constraint work: `CREATE|DROP INDEX`, `CREATE|DROP CONSTRAINT`, `SHOW INDEXES|CONSTRAINTS` (executed per `prd-property-indexes-constraints.md`), vector index forms per `prd-vector-search.md`.
- Literals/functions: temporal and spatial literals/constructors (`datetime()`, `date()`, `duration()`, `point()`), `toXxxOrNull`, the Neo4j scalar/aggregate/string/list/math function library (name resolution is a planner concern; the parser treats them as `FuncCall`).
- Query prefix `CYPHER 25` and `CYPHER runtime=…` options (parsed, validated, ignored); other versions rejected.

**Tier B — Cypher 25 additions (always parsed; executed as the planner supports them):** `FILTER`, `LET`, `FINISH`, `OFFSET` (alias of `SKIP`), and `WHEN … THEN { } ELSE { }` conditional bodies inside `CALL`/`COLLECT` subqueries — all verified against the Cypher Manual. **Deferred until the manual documents them:** `NEXT`, `INSERT` and a standalone `ORDER BY`/`LIMIT` statement.

Because the TCK only covers openCypher, Neo4j-extension coverage comes from: examples in the Neo4j Cypher Manual (turned into golden tests), a Neo4j-extension scenario suite added under `compat/testdata/neo4j/` (same Gherkin format as the TCK), and, optionally, spot comparison against a live Neo4j instance.

## Scope of "complete"

Parsing is only the first half of "passes all tests". The TCK skip list in `compat/tck_test.go` also excludes things the **planner/translator** cannot yet execute (UNWIND, UNION, `RETURN *`, ~35 functions, list comprehensions, quantifier predicates, named paths, FOREACH, shortestPath, pattern predicates). Therefore:

- Iterations 1–6 deliver the parser, cut-over and ANTLR removal. Existing behaviour is preserved (no regressions).
- Iterations 7–9 add semantic analysis (TCK compile-time errors) and bring planner/`sql/` up to the new AST feature by feature, deleting one skip-list entry at a time until the list is empty.
- Neo4j extensions are parsed in iterations 4–5, and executed in iterations 8–10 in priority order (the ones Neo4j developers hit daily first: label expressions, `EXISTS/COUNT/COLLECT` subqueries, map projections, `CALL {}`, shortest-path selectors, then quantified path patterns).
- Open question for the owner: where SQLite cannot cleanly express a construct (e.g. list comprehensions over arbitrary lists, path values, FOREACH with nested writes), is a Go-side evaluation fallback acceptable? The plan assumes **yes**, as long as results are correct.

## Design

### Package layout

```
cypher/syntax/
  token.go      Token kinds, keywords (case-insensitive), Pos{Offset,Line,Col}
  lexer.go      Hand-written scanner over string; no regexp
  ast.go        Full typed AST (Statement, Clause, Pattern, Expr …) — sealed interfaces, Pos on every node
  parser.go     Entry: Parse(string) (*Statement, error); recursive-descent for clauses
  expr.go       Precedence-climbing expression parser
  pattern.go    Node/relationship/path patterns, var-length ranges, property maps
  errors.go     *SyntaxError{Pos, Found, Expected, Msg}; TCK error-code mapping
  *_test.go     Table tests, fuzz tests (go test -fuzz), golden ASTs
```

Constraints: `cypher/syntax` imports only the standard library. `cypher/` may import `cypher/syntax`; never the reverse.

### AST

Fully typed (no `ExprText`/`RawExpr` strings). Expressions: literals (int/float/string/bool/null/list/map), `Param`, `Ident`, `Property`, `Subscript`, `Slice`, `Unary`, `Binary` (OR XOR AND, comparison chains, + - * / % ^), `IsNull`, `In`, `StringPred` (STARTS/ENDS/CONTAINS), `HasLabels`, `FuncCall` (namespaced, DISTINCT, `count(*)`), `Case` (simple/generic), `ListComp`, `PatternComp`, `Quantifier` (all/any/none/single), `Reduce`, `Exists`, `PatternExpr`, `ShortestPath`.
Neo4j extensions: `LabelExpr` (and/or/not/wildcard), `RelTypeExpr`, `SubqueryExpr` (EXISTS/COUNT/COLLECT), `CallSubquery` (+ importing WITH, IN TRANSACTIONS), `QuantifiedPath`, `PathSelector`, `MapProjection`, `DynamicLabel/Property`, `TypePredicate`, `Explain/Profile`, `QueryOptions` (CYPHER prefix), schema-command nodes.
Clauses: `Match` (optional), `Unwind`, `Create`, `Merge` (+ON CREATE/ON MATCH), `Set` (=, +=, label), `Remove`, `Delete`, `Foreach`, `Call` (standalone/in-query, YIELD, WHERE), `With`, `Return` (DISTINCT, `*`, ORDER BY, SKIP, LIMIT), `Union`.

### Migration strategy

1. `cypher/syntax` is built alongside the ANTLR path; nothing consumes it until the adapter lands.
2. An **adapter** (`cypher/lower.go`) lowers `syntax` AST → the existing `cypher.Query`, so planner/translator stay untouched at cut-over. Constructs the old AST cannot represent return `ErrUnsupportedCypher` exactly as today.
3. A **differential test** parses a large corpus with both parsers and compares lowered ASTs (and accept/reject outcome).
4. Cut over `cypher.Parse`; delete ANTLR + `opencypher`; `go mod tidy && go mod vendor`.
5. Later iterations make the planner consume the `syntax` AST feature by feature.

### Test corpus / oracles

- Existing `cypher/*_test.go`, `sql/translator_test.go`, `testdata/rapid`, `testdata/integration_test.go`.
- openCypher TCK `.feature` files (15 vendored today in `compat/testdata/tck`; import the full TCK).
- The official openCypher grammar (EBNF/`Cypher.g4`) as the grammar checklist.
- Go native fuzzing: parser must never panic or hang; print→parse round-trip once a printer exists.
- ANTLR parser as differential oracle until removed.

## Requirements

- REQ-F-001: `syntax.Parse` accepts every query form in the openCypher 9 grammar and rejects invalid input with a `*SyntaxError` carrying position and expected tokens.
- REQ-F-002: Keywords case-insensitive; backtick-quoted and unicode identifiers; all string escapes (`\n \t \uXXXX \UXXXXXXXX` …); hex/octal/exponent numerics; `1..3` ranges lex correctly vs floats.
- REQ-F-003: Operator precedence/associativity matches the spec (comparison chaining, `^`, unary minus, `NOT`, `IS [NOT] NULL`, `IN`, string/label predicates). The levels follow the openCypher grammar exactly — see the comment atop `cypher/syntax/expr.go`; TCK precedence scenarios are the final arbiter.
- REQ-F-004: `cypher.Parse` yields results identical to the current ANTLR-based `Parse` for every existing test before cut-over.
- REQ-F-005: Parser is goroutine-safe (no global mutable state).
- REQ-NF-001: `go build ./...`, `go vet ./...`, `CGO_ENABLED=0 go test -tags=unit -count=1 ./...` pass at every iteration.
- REQ-NF-002: `cypher/syntax` is stdlib-only; AGENTS.md layering rules hold.
- REQ-NF-003: Benchmark shows ≥100x speed-up and ≥100x fewer allocations vs ANTLR on the reference query.
- REQ-NF-004: Fuzzing finds no panics/hangs before cut-over.
- REQ-F-006: Every Tier A construct parses into a dedicated typed node; unsupported-at-execution constructs fail in the planner with a clear `ErrUnsupportedCypher` message naming the construct (never a generic syntax error).
- REQ-F-007: Cypher 25 is the only supported language version. `CYPHER 25` is accepted; other versions are rejected with a clear error. No dialect switch exists.
- REQ-F-008: Every TCK scenario excluded because it needs syntax removed in Cypher 25 is listed in a single exclusion file with a reason; the "empty skip list" end state means no exclusions except that list.
- REQ-NF-005: At the end the TCK skip list is empty and all scenarios pass; TCK pass rate never decreases between iterations.

## Iterations

| # | Iteration | Outcome |
|---|-----------|---------|
| 1 | Lexer + errors | Full tokenizer, positions, fuzzed |
| 2 | AST + expressions | All expression forms parse |
| 3 | Patterns | Node/rel/path/var-length/named paths/shortestPath |
| 4 | Clauses + statements | Whole openCypher 9 grammar parses incl. UNION, CALL, FOREACH |
| 4b | Neo4j extensions (parser) | Tier A + Tier B constructs parse into typed nodes (Cypher 25 only) |
| 5 | Adapter + differential harness | `syntax` → `cypher.Query` lowering; parity with ANTLR proven on the openCypher subset ANTLR accepts |
| 6 | Cut-over + ANTLR removal | `Parse` uses new parser; deps removed; docs updated |
| 7 | Semantic analysis | Scope/variable/type checks giving TCK compile-time errors |
| 8 | Planner/SQL catch-up (A) | UNWIND, UNION, `RETURN *`, scalar/string/math/list functions, coalesce, size… |
| 9 | Planner/SQL catch-up (B) + full TCK | comprehensions, quantifiers, reduce, named paths, shortestPath, FOREACH, CALL, pattern predicates; full TCK; skip list empty |
| 10 | Neo4j extension execution | Label expressions, EXISTS/COUNT/COLLECT, map projections, CALL {}, shortest-path selectors, QPPs, temporal/spatial, schema commands; Neo4j-extension suite green |

Task breakdown: `tasks-handwritten-cypher-parser.yml`.

## Risks

- **Fidelity vs ANTLR**: mitigated by differential testing and the TCK. ANTLR (openCypher 9 only) cannot act as an oracle for Neo4j extensions; those rely on manual-derived golden tests and the Neo4j-extension suite.
- **Moving target**: Neo4j's Cypher evolves (Cypher 25 is the baseline; later versions will add syntax). Targeting a single version keeps scope small; the Cypher Manual is the source of truth per task.
- **TCK vs Cypher 25**: the TCK is openCypher 9 and includes syntax Cypher 25 dropped. Those scenarios are excluded by an explicit, reviewed list (REQ-F-008), which is a deliberate departure from "every TCK scenario passes". Reverse the decision if strict TCK parity matters more than Cypher 25 fidelity.
- **Hand-parsing QPPs/label expressions**: need more lookahead than openCypher 9 (`(` can start a node, a parenthesised expression, or a quantified path). Dedicated disambiguation tests required.
- **Scope in iterations 8–9**: some constructs are hard in SQLite; the Go-side evaluation fallback bounds this, and each skip-list entry is its own task so progress is incremental.
- **Existing AST limits** (property values as `ExprText`): lowering must preserve planner expectations until the planner migrates; iteration 8 begins retiring `ExprText`.
