<!--
TCK results after task-039 (typed Query AST), semantic analysis (task-018) and
the strict compile-time error check. Full openCypher TCK, compat/testdata/tck
(commit 677cbaf).
Regenerate with:  TCK_REPORT=.plans/tck-baseline.md CGO_ENABLED=0 go test -tags=tck ./compat/...
Diff two runs scenario by scenario with TCK_OUTCOMES=path.
-->

> **History.** 816/2,631 (31.0%) with the full TCK imported and any error accepted
> for compile-time expectations -> 911/2,631 (34.6%) with semantic analysis and exact
> error classes and codes -> **950/2,631 (36.1%)** with the typed Query AST (task-039:
> property values, ORDER BY keys and SET values lower to typed expressions, so negative
> literals, escapes and arithmetic in property maps now work; 0 regressions).
>
> **What is left.** The failures are not parsing or analysis problems:
> `expressions/temporal` (975 scenarios, date/time types, task-038) and the
> translator rejecting "complex expressions" (functions, `*`/`/`/`%`/`^`, ~1,000,
> tasks 019-027). Runtime error expectations (negative SKIP/LIMIT parameters,
> deleted-entity access, ...) still accept any error. `expressions/quantifier` (604)
> and `typeConversion` (47) are skipped until list predicates and conversion
> functions exist.

# openCypher TCK results

Scenarios: 3897 total - **950 passed**, 1681 failed, 1264 skipped (feature not supported yet), 2 excluded (Cypher 25 removed syntax).

Pass rate over executed scenarios: **950/2631 (36.1%)**

## By area

| | passed | failed | skipped | excluded | pass rate |
|---|---:|---:|---:|---:|---:|
| clauses/call | 2 | 0 | 50 | 0 | 100% |
| clauses/create | 61 | 5 | 12 | 0 | 92% |
| clauses/delete | 17 | 21 | 3 | 0 | 45% |
| clauses/match | 178 | 83 | 120 | 0 | 68% |
| clauses/match-where | 23 | 7 | 4 | 0 | 77% |
| clauses/merge | 25 | 35 | 13 | 2 | 42% |
| clauses/remove | 17 | 10 | 6 | 0 | 63% |
| clauses/return | 29 | 21 | 13 | 0 | 58% |
| clauses/return-orderby | 17 | 3 | 15 | 0 | 85% |
| clauses/return-skip-limit | 24 | 3 | 4 | 0 | 89% |
| clauses/set | 20 | 29 | 4 | 0 | 41% |
| clauses/union | 0 | 0 | 12 | 0 | 0% |
| clauses/unwind | 0 | 0 | 14 | 0 | 0% |
| clauses/with | 6 | 13 | 10 | 0 | 32% |
| clauses/with-orderBy | 70 | 172 | 50 | 0 | 29% |
| clauses/with-skip-limit | 1 | 6 | 2 | 0 | 14% |
| clauses/with-where | 1 | 14 | 4 | 0 | 7% |
| expressions/aggregation | 13 | 6 | 16 | 0 | 68% |
| expressions/boolean | 127 | 3 | 20 | 0 | 98% |
| expressions/comparison | 22 | 34 | 16 | 0 | 39% |
| expressions/conditional | 11 | 1 | 1 | 0 | 92% |
| expressions/existentialSubqueries | 1 | 8 | 1 | 0 | 11% |
| expressions/graph | 21 | 12 | 28 | 0 | 64% |
| expressions/list | 61 | 31 | 93 | 0 | 66% |
| expressions/literals | 105 | 26 | 0 | 0 | 80% |
| expressions/map | 9 | 23 | 12 | 0 | 28% |
| expressions/mathematical | 1 | 4 | 1 | 0 | 20% |
| expressions/null | 11 | 33 | 0 | 0 | 25% |
| expressions/path | 0 | 0 | 7 | 0 | 0% |
| expressions/pattern | 18 | 30 | 2 | 0 | 38% |
| expressions/precedence | 27 | 35 | 59 | 0 | 44% |
| expressions/quantifier | 0 | 0 | 604 | 0 | 0% |
| expressions/string | 15 | 13 | 4 | 0 | 54% |
| expressions/temporal | 12 | 975 | 17 | 0 | 1% |
| expressions/typeConversion | 0 | 0 | 47 | 0 | 0% |
| useCases/countingSubgraphMatches | 4 | 7 | 0 | 0 | 36% |
| useCases/triadicSelection | 1 | 18 | 0 | 0 | 5% |

## By feature file

| | passed | failed | skipped | excluded | pass rate |
|---|---:|---:|---:|---:|---:|
| clauses/call/Call1.feature | 2 | 0 | 14 | 0 | 100% |
| clauses/call/Call2.feature | 0 | 0 | 6 | 0 | 0% |
| clauses/call/Call3.feature | 0 | 0 | 6 | 0 | 0% |
| clauses/call/Call4.feature | 0 | 0 | 2 | 0 | 0% |
| clauses/call/Call5.feature | 0 | 0 | 19 | 0 | 0% |
| clauses/call/Call6.feature | 0 | 0 | 3 | 0 | 0% |
| clauses/create/Create1.feature | 20 | 0 | 0 | 0 | 100% |
| clauses/create/Create2.feature | 24 | 0 | 0 | 0 | 100% |
| clauses/create/Create3.feature | 7 | 5 | 1 | 0 | 58% |
| clauses/create/Create4.feature | 2 | 0 | 0 | 0 | 100% |
| clauses/create/Create5.feature | 4 | 0 | 1 | 0 | 100% |
| clauses/create/Create6.feature | 4 | 0 | 10 | 0 | 100% |
| clauses/delete/Delete1.feature | 8 | 0 | 0 | 0 | 100% |
| clauses/delete/Delete2.feature | 2 | 1 | 2 | 0 | 67% |
| clauses/delete/Delete3.feature | 0 | 1 | 1 | 0 | 0% |
| clauses/delete/Delete4.feature | 1 | 2 | 0 | 0 | 33% |
| clauses/delete/Delete5.feature | 2 | 7 | 0 | 0 | 22% |
| clauses/delete/Delete6.feature | 4 | 10 | 0 | 0 | 29% |
| clauses/match-where/MatchWhere1.feature | 11 | 0 | 4 | 0 | 100% |
| clauses/match-where/MatchWhere2.feature | 1 | 1 | 0 | 0 | 50% |
| clauses/match-where/MatchWhere3.feature | 3 | 0 | 0 | 0 | 100% |
| clauses/match-where/MatchWhere4.feature | 1 | 1 | 0 | 0 | 50% |
| clauses/match-where/MatchWhere5.feature | 4 | 0 | 0 | 0 | 100% |
| clauses/match-where/MatchWhere6.feature | 3 | 5 | 0 | 0 | 38% |
| clauses/match/Match1.feature | 60 | 0 | 26 | 0 | 100% |
| clauses/match/Match2.feature | 67 | 0 | 19 | 0 | 100% |
| clauses/match/Match3.feature | 20 | 10 | 0 | 0 | 67% |
| clauses/match/Match4.feature | 3 | 5 | 2 | 0 | 38% |
| clauses/match/Match5.feature | 16 | 13 | 0 | 0 | 55% |
| clauses/match/Match6.feature | 7 | 19 | 71 | 0 | 27% |
| clauses/match/Match7.feature | 4 | 26 | 1 | 0 | 13% |
| clauses/match/Match8.feature | 1 | 2 | 0 | 0 | 33% |
| clauses/match/Match9.feature | 0 | 8 | 1 | 0 | 0% |
| clauses/merge/Merge1.feature | 11 | 3 | 3 | 0 | 79% |
| clauses/merge/Merge2.feature | 4 | 1 | 1 | 0 | 80% |
| clauses/merge/Merge3.feature | 2 | 2 | 1 | 0 | 50% |
| clauses/merge/Merge4.feature | 0 | 2 | 0 | 0 | 0% |
| clauses/merge/Merge5.feature | 8 | 20 | 1 | 0 | 29% |
| clauses/merge/Merge6.feature | 0 | 2 | 3 | 1 | 0% |
| clauses/merge/Merge7.feature | 0 | 3 | 1 | 1 | 0% |
| clauses/merge/Merge8.feature | 0 | 1 | 0 | 0 | 0% |
| clauses/merge/Merge9.feature | 0 | 1 | 3 | 0 | 0% |
| clauses/remove/Remove1.feature | 3 | 1 | 3 | 0 | 75% |
| clauses/remove/Remove2.feature | 2 | 0 | 3 | 0 | 100% |
| clauses/remove/Remove3.feature | 12 | 9 | 0 | 0 | 57% |
| clauses/return-orderby/ReturnOrderBy1.feature | 0 | 0 | 12 | 0 | 0% |
| clauses/return-orderby/ReturnOrderBy2.feature | 11 | 1 | 2 | 0 | 92% |
| clauses/return-orderby/ReturnOrderBy3.feature | 1 | 0 | 0 | 0 | 100% |
| clauses/return-orderby/ReturnOrderBy4.feature | 0 | 1 | 1 | 0 | 0% |
| clauses/return-orderby/ReturnOrderBy5.feature | 1 | 0 | 0 | 0 | 100% |
| clauses/return-orderby/ReturnOrderBy6.feature | 4 | 1 | 0 | 0 | 80% |
| clauses/return-skip-limit/ReturnSkipLimit1.feature | 9 | 1 | 1 | 0 | 90% |
| clauses/return-skip-limit/ReturnSkipLimit2.feature | 13 | 2 | 2 | 0 | 87% |
| clauses/return-skip-limit/ReturnSkipLimit3.feature | 2 | 0 | 1 | 0 | 100% |
| clauses/return/Return1.feature | 2 | 0 | 0 | 0 | 100% |
| clauses/return/Return2.feature | 8 | 8 | 2 | 0 | 50% |
| clauses/return/Return3.feature | 3 | 0 | 0 | 0 | 100% |
| clauses/return/Return4.feature | 4 | 4 | 3 | 0 | 50% |
| clauses/return/Return5.feature | 2 | 3 | 0 | 0 | 40% |
| clauses/return/Return6.feature | 10 | 5 | 6 | 0 | 67% |
| clauses/return/Return7.feature | 0 | 0 | 2 | 0 | 0% |
| clauses/return/Return8.feature | 0 | 1 | 0 | 0 | 0% |
| clauses/set/Set1.feature | 7 | 3 | 1 | 0 | 70% |
| clauses/set/Set2.feature | 3 | 0 | 0 | 0 | 100% |
| clauses/set/Set3.feature | 0 | 5 | 3 | 0 | 0% |
| clauses/set/Set4.feature | 0 | 5 | 0 | 0 | 0% |
| clauses/set/Set5.feature | 4 | 1 | 0 | 0 | 80% |
| clauses/set/Set6.feature | 6 | 15 | 0 | 0 | 29% |
| clauses/union/Union1.feature | 0 | 0 | 5 | 0 | 0% |
| clauses/union/Union2.feature | 0 | 0 | 5 | 0 | 0% |
| clauses/union/Union3.feature | 0 | 0 | 2 | 0 | 0% |
| clauses/unwind/Unwind1.feature | 0 | 0 | 14 | 0 | 0% |
| clauses/with-orderBy/WithOrderBy1.feature | 10 | 50 | 36 | 0 | 17% |
| clauses/with-orderBy/WithOrderBy2.feature | 25 | 56 | 2 | 0 | 31% |
| clauses/with-orderBy/WithOrderBy3.feature | 30 | 53 | 10 | 0 | 36% |
| clauses/with-orderBy/WithOrderBy4.feature | 5 | 13 | 2 | 0 | 28% |
| clauses/with-skip-limit/WithSkipLimit1.feature | 0 | 2 | 0 | 0 | 0% |
| clauses/with-skip-limit/WithSkipLimit2.feature | 1 | 2 | 1 | 0 | 33% |
| clauses/with-skip-limit/WithSkipLimit3.feature | 0 | 2 | 1 | 0 | 0% |
| clauses/with-where/WithWhere1.feature | 0 | 3 | 1 | 0 | 0% |
| clauses/with-where/WithWhere2.feature | 0 | 2 | 0 | 0 | 0% |
| clauses/with-where/WithWhere3.feature | 0 | 3 | 0 | 0 | 0% |
| clauses/with-where/WithWhere4.feature | 0 | 2 | 0 | 0 | 0% |
| clauses/with-where/WithWhere5.feature | 0 | 4 | 0 | 0 | 0% |
| clauses/with-where/WithWhere6.feature | 1 | 0 | 0 | 0 | 100% |
| clauses/with-where/WithWhere7.feature | 0 | 0 | 3 | 0 | 0% |
| clauses/with/With1.feature | 1 | 2 | 3 | 0 | 33% |
| clauses/with/With2.feature | 0 | 2 | 0 | 0 | 0% |
| clauses/with/With3.feature | 0 | 1 | 0 | 0 | 0% |
| clauses/with/With4.feature | 4 | 2 | 1 | 0 | 67% |
| clauses/with/With5.feature | 0 | 2 | 0 | 0 | 0% |
| clauses/with/With6.feature | 1 | 2 | 6 | 0 | 33% |
| clauses/with/With7.feature | 0 | 2 | 0 | 0 | 0% |
| expressions/aggregation/Aggregation1.feature | 2 | 0 | 0 | 0 | 100% |
| expressions/aggregation/Aggregation2.feature | 0 | 0 | 12 | 0 | 0% |
| expressions/aggregation/Aggregation3.feature | 1 | 0 | 1 | 0 | 100% |
| expressions/aggregation/Aggregation5.feature | 2 | 0 | 0 | 0 | 100% |
| expressions/aggregation/Aggregation6.feature | 6 | 6 | 1 | 0 | 50% |
| expressions/aggregation/Aggregation8.feature | 2 | 0 | 2 | 0 | 100% |
| expressions/boolean/Boolean1.feature | 26 | 0 | 4 | 0 | 100% |
| expressions/boolean/Boolean2.feature | 26 | 0 | 4 | 0 | 100% |
| expressions/boolean/Boolean3.feature | 23 | 3 | 4 | 0 | 88% |
| expressions/boolean/Boolean4.feature | 52 | 0 | 0 | 0 | 100% |
| expressions/boolean/Boolean5.feature | 0 | 0 | 8 | 0 | 0% |
| expressions/comparison/Comparison1.feature | 16 | 24 | 3 | 0 | 40% |
| expressions/comparison/Comparison2.feature | 5 | 10 | 4 | 0 | 33% |
| expressions/comparison/Comparison3.feature | 1 | 0 | 8 | 0 | 100% |
| expressions/comparison/Comparison4.feature | 0 | 0 | 1 | 0 | 0% |
| expressions/conditional/Conditional1.feature | 0 | 0 | 1 | 0 | 0% |
| expressions/conditional/Conditional2.feature | 11 | 1 | 0 | 0 | 92% |
| expressions/existentialSubqueries/ExistentialSubquery1.feature | 0 | 3 | 1 | 0 | 0% |
| expressions/existentialSubqueries/ExistentialSubquery2.feature | 1 | 2 | 0 | 0 | 33% |
| expressions/existentialSubqueries/ExistentialSubquery3.feature | 0 | 3 | 0 | 0 | 0% |
| expressions/graph/Graph3.feature | 0 | 0 | 9 | 0 | 0% |
| expressions/graph/Graph4.feature | 0 | 0 | 11 | 0 | 0% |
| expressions/graph/Graph5.feature | 9 | 0 | 0 | 0 | 100% |
| expressions/graph/Graph6.feature | 9 | 5 | 0 | 0 | 64% |
| expressions/graph/Graph7.feature | 0 | 3 | 0 | 0 | 0% |
| expressions/graph/Graph8.feature | 0 | 0 | 8 | 0 | 0% |
| expressions/graph/Graph9.feature | 3 | 4 | 0 | 0 | 43% |
| expressions/list/List1.feature | 18 | 4 | 1 | 0 | 82% |
| expressions/list/List11.feature | 0 | 0 | 67 | 0 | 0% |
| expressions/list/List12.feature | 0 | 0 | 7 | 0 | 0% |
| expressions/list/List2.feature | 0 | 15 | 0 | 0 | 0% |
| expressions/list/List3.feature | 4 | 3 | 0 | 0 | 57% |
| expressions/list/List4.feature | 1 | 1 | 0 | 0 | 50% |
| expressions/list/List5.feature | 38 | 8 | 0 | 0 | 83% |
| expressions/list/List6.feature | 0 | 0 | 17 | 0 | 0% |
| expressions/list/List9.feature | 0 | 0 | 1 | 0 | 0% |
| expressions/literals/Literals1.feature | 6 | 0 | 0 | 0 | 100% |
| expressions/literals/Literals2.feature | 12 | 0 | 0 | 0 | 100% |
| expressions/literals/Literals3.feature | 16 | 0 | 0 | 0 | 100% |
| expressions/literals/Literals4.feature | 10 | 0 | 0 | 0 | 100% |
| expressions/literals/Literals5.feature | 21 | 6 | 0 | 0 | 78% |
| expressions/literals/Literals6.feature | 11 | 2 | 0 | 0 | 85% |
| expressions/literals/Literals7.feature | 20 | 0 | 0 | 0 | 100% |
| expressions/literals/Literals8.feature | 9 | 18 | 0 | 0 | 33% |
| expressions/map/Map1.feature | 6 | 13 | 0 | 0 | 32% |
| expressions/map/Map2.feature | 3 | 10 | 1 | 0 | 23% |
| expressions/map/Map3.feature | 0 | 0 | 11 | 0 | 0% |
| expressions/mathematical/Mathematical11.feature | 0 | 0 | 1 | 0 | 0% |
| expressions/mathematical/Mathematical13.feature | 0 | 1 | 0 | 0 | 0% |
| expressions/mathematical/Mathematical2.feature | 0 | 1 | 0 | 0 | 0% |
| expressions/mathematical/Mathematical3.feature | 1 | 0 | 0 | 0 | 100% |
| expressions/mathematical/Mathematical8.feature | 0 | 2 | 0 | 0 | 0% |
| expressions/null/Null1.feature | 4 | 13 | 0 | 0 | 24% |
| expressions/null/Null2.feature | 4 | 13 | 0 | 0 | 24% |
| expressions/null/Null3.feature | 3 | 7 | 0 | 0 | 30% |
| expressions/path/Path1.feature | 0 | 0 | 1 | 0 | 0% |
| expressions/path/Path2.feature | 0 | 0 | 3 | 0 | 0% |
| expressions/path/Path3.feature | 0 | 0 | 3 | 0 | 0% |
| expressions/pattern/Pattern1.feature | 18 | 20 | 1 | 0 | 47% |
| expressions/pattern/Pattern2.feature | 0 | 10 | 1 | 0 | 0% |
| expressions/precedence/Precedence1.feature | 11 | 2 | 59 | 0 | 85% |
| expressions/precedence/Precedence2.feature | 0 | 26 | 0 | 0 | 0% |
| expressions/precedence/Precedence3.feature | 6 | 5 | 0 | 0 | 55% |
| expressions/precedence/Precedence4.feature | 10 | 2 | 0 | 0 | 83% |
| expressions/quantifier/Quantifier1.feature | 0 | 0 | 105 | 0 | 0% |
| expressions/quantifier/Quantifier10.feature | 0 | 0 | 8 | 0 | 0% |
| expressions/quantifier/Quantifier11.feature | 0 | 0 | 22 | 0 | 0% |
| expressions/quantifier/Quantifier12.feature | 0 | 0 | 17 | 0 | 0% |
| expressions/quantifier/Quantifier2.feature | 0 | 0 | 106 | 0 | 0% |
| expressions/quantifier/Quantifier3.feature | 0 | 0 | 105 | 0 | 0% |
| expressions/quantifier/Quantifier4.feature | 0 | 0 | 105 | 0 | 0% |
| expressions/quantifier/Quantifier5.feature | 0 | 0 | 31 | 0 | 0% |
| expressions/quantifier/Quantifier6.feature | 0 | 0 | 21 | 0 | 0% |
| expressions/quantifier/Quantifier7.feature | 0 | 0 | 36 | 0 | 0% |
| expressions/quantifier/Quantifier8.feature | 0 | 0 | 31 | 0 | 0% |
| expressions/quantifier/Quantifier9.feature | 0 | 0 | 17 | 0 | 0% |
| expressions/string/String1.feature | 0 | 1 | 0 | 0 | 0% |
| expressions/string/String10.feature | 5 | 3 | 1 | 0 | 62% |
| expressions/string/String11.feature | 0 | 2 | 0 | 0 | 0% |
| expressions/string/String3.feature | 0 | 1 | 0 | 0 | 0% |
| expressions/string/String4.feature | 0 | 0 | 1 | 0 | 0% |
| expressions/string/String8.feature | 5 | 3 | 1 | 0 | 62% |
| expressions/string/String9.feature | 5 | 3 | 1 | 0 | 62% |
| expressions/temporal/Temporal1.feature | 0 | 207 | 0 | 0 | 0% |
| expressions/temporal/Temporal10.feature | 0 | 131 | 0 | 0 | 0% |
| expressions/temporal/Temporal2.feature | 0 | 53 | 0 | 0 | 0% |
| expressions/temporal/Temporal3.feature | 0 | 183 | 0 | 0 | 0% |
| expressions/temporal/Temporal4.feature | 12 | 27 | 0 | 0 | 31% |
| expressions/temporal/Temporal5.feature | 0 | 7 | 0 | 0 | 0% |
| expressions/temporal/Temporal6.feature | 0 | 0 | 17 | 0 | 0% |
| expressions/temporal/Temporal7.feature | 0 | 18 | 0 | 0 | 0% |
| expressions/temporal/Temporal8.feature | 0 | 27 | 0 | 0 | 0% |
| expressions/temporal/Temporal9.feature | 0 | 322 | 0 | 0 | 0% |
| expressions/typeConversion/TypeConversion1.feature | 0 | 0 | 10 | 0 | 0% |
| expressions/typeConversion/TypeConversion2.feature | 0 | 0 | 12 | 0 | 0% |
| expressions/typeConversion/TypeConversion3.feature | 0 | 0 | 11 | 0 | 0% |
| expressions/typeConversion/TypeConversion4.feature | 0 | 0 | 14 | 0 | 0% |
| useCases/countingSubgraphMatches/CountingSubgraphMatches1.feature | 4 | 7 | 0 | 0 | 36% |
| useCases/triadicSelection/TriadicSelection1.feature | 1 | 18 | 0 | 0 | 5% |

## Top failure reasons

| count | reason |
|---:|---|
| 1060 | query failed: graphlite: translate: sql: SELECT projection: sql: unsupported expression "…": complex expressions are not yet supported in … |
| 141 | expected N row(s), got N |
| 84 | having executed "…": graphlite: translate: sql: CREATE node props: property "…": sql: unsupported expression "…": complex expressions … |
| 46 | query failed: graphlite: query: SQL logic error: near "…": syntax error (N) |
| 42 | query failed: graphlite: parse: cypher: multiple WITH stages are not yet supported |
| 39 | column "…": unexpected value "…" in actual results |
| 29 | query failed: graphlite: query: SQL logic error: ambiguous column name: nN.id (N) |
| 27 | query failed: graphlite: translate: sql: SELECT projection: sql: variable "…" not in scope |
| 26 | query failed: graphlite: plan: cypher: MERGE with relationship patterns is not yet supported |
| 22 | query failed: graphlite: translate: sql: SELECT projection: sql: arith lhs: sql: unsupported expression "…": complex expressions are not y… |
| 21 | query failed: graphlite: query: SQL logic error: ambiguous column name: nN.props (N) |
| 21 | query failed: graphlite: translate: sql: WHERE predicate: sql: unsupported expression "…": complex expressions are not yet supported in th… |
| 17 | query failed: graphlite: parse: cypher: only "…" and "…" SET items are supported |
| 10 | query failed: graphlite: query: SQL logic error: HAVING clause on a non-aggregate query (N) |
| 8 | having executed "…": graphlite: insert node: SQL logic error: no such column: nN.props (N) |
| 8 | query failed: graphlite: parse: cypher: WHERE clause: cypher: an EXISTS/COUNT/COLLECT subquery is not supported |
| 7 | query failed: graphlite: plan: cypher: DELETE of an expression other than a variable is not supported |
| 6 | query failed: graphlite: query: SQL logic error: no such column: rN.id (N) |
| 6 | query failed: graphlite: translate: sql: CREATE node props: property "…": sql: unsupported expression "…": complex expressions are not y… |
| 5 | query failed: graphlite: translate: sql: match-for-write FROM clause: sql: HAVING predicate: sql: unsupported expression "…": complex expr… |
| 4 | expected empty result (table has no data rows), got N row(s) |
| 4 | query failed: graphlite: translate: sql: GROUP BY expression: sql: unsupported expression "…": complex expressions are not yet supported i… |
| 4 | query failed: graphlite: translate: sql: SELECT projection: sql: count() argument: sql: unsupported expression "…": complex expressions ar… |
| 4 | query failed: graphlite: write-then-select query: SQL logic error: no such column: nN.props (N) |
| 3 | expected SyntaxError error (NegativeIntegerArgument) but query succeeded |

## Skip and exclusion reasons

| count | reason |
|---:|---|
| 295 | skipped: UNWIND not supported |
| 138 | skipped: any() predicate not supported |
| 117 | skipped: named path variables not supported |
| 114 | skipped: all() predicate not supported |
| 114 | skipped: size() function not supported |
| 96 | skipped: none() predicate not supported |
| 90 | skipped: single() predicate not supported |
| 66 | skipped: range() function not supported |
| 50 | skipped: test procedures (CALL) not supported |
| 30 | skipped: toString() function not supported |
| 22 | skipped: RETURN * not supported |
| 21 | skipped: labels() function not supported |
| 17 | skipped: type() function not supported |
| 16 | skipped: keys() function not supported |
| 13 | skipped: toInteger() function not supported |
| 11 | skipped: toFloat() function not supported |
| 10 | skipped: UNION not supported |
| 8 | skipped: length() function not supported |
| 8 | skipped: nodes() function not supported |
| 7 | skipped: relationships() function not supported |
| 7 | skipped: toBoolean() function not supported |
| 4 | skipped: math function abs not supported |
| 3 | skipped: coalesce() function not supported |
| 2 | excluded: SET with a relationship/node on the right-hand side is removed in Cypher 25 (use properties()) |
| 2 | skipped: head() function not supported |
| 2 | skipped: list comprehensions not supported |
| 1 | skipped: last() function not supported |
| 1 | skipped: string function toLower not supported |
| 1 | skipped: tail() function not supported |

