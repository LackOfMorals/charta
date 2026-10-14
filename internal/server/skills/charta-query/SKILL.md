---
name: charta-query
description: Run Cypher against a charta-query server, an embedded graph database that speaks the Neo4j Query API v2 over HTTP. Use when you need to read, write or explore a graph with Cypher, or when you are given a charta-query URL.
---

# charta-query

charta-query is an HTTP server with the Neo4j Query API (v2) contract on top of
an embedded graph database (charta, Cypher over SQLite). Anything you can send
to a Neo4j Query API endpoint works here.

Server: `{{BASE_URL}}`

## Before you start

- **Credentials.** The query endpoints need HTTP basic auth. Ask the user for
  the username and password and keep them in environment variables
  (`CQ_USER`, `CQ_PASSWORD` below). Never write them into files or commands you
  show back. There is one credential and it can do anything to the graph,
  including delete all of it, so confirm before destructive statements.
- **Database name.** Use `neo4j` unless discovery says otherwise:
  `curl {{BASE_URL}}/` (no auth) returns the `database` name and endpoint URLs.
- **Liveness.** `curl {{BASE_URL}}/health` needs no auth.
- The server may be read-only. Writes then fail with
  `Neo.ClientError.General.ForbiddenOnReadOnlyDatabase`.

## Run a statement

Ask for TOON (`Accept: text/toon`). It is a compact table, uses far fewer
tokens than JSON, and failures are always an error status.

```sh
curl -s -u "$CQ_USER:$CQ_PASSWORD" -H 'Content-Type: application/json' \
  -H 'Accept: text/toon' {{BASE_URL}}/db/neo4j/query/v2 \
  -d '{"statement":"MATCH (p:Person {name: $name}) RETURN p.name AS name, p.age AS age",
       "parameters":{"name":"Alice"}}'
```

```
rows[1]{name,age}:
  Alice,30
```

`rows[N]{columns}:` is followed by N lines, one per row, comma-separated in
column order. Strings are quoted only when needed (commas, colons, leading
`-`, or when they look like a number, `true`, `false` or `null`), so `"1"` is
the string 1 while `1` is a number; `null` is null.

Always pass values through `parameters` (`$name`) rather than building them into
the statement text. It avoids quoting bugs and injection. Request fields:
`statement` (required) and `parameters`. `Content-Type` stays JSON.

What TOON does with graph values:

- **Nodes and relationships become columns.** `RETURN p` gives
  `p._id,p._labels,p.name,...` (`_labels` is `Person` or `Person:Admin`);
  a relationship `r` gives `r._id,r._type,r._start,r._end,r.<property>`. Rows
  that lack a property get `null`. Use `_id` with `elementId(n) = "<id>"` to
  find the entity again.
- **Lists, maps and paths are a JSON string in one cell**, e.g.
  `"[1,2]"`. Return scalars (`RETURN p.name AS name`) when you can.
- **Writes report what they changed**: a `counters:` block with the non-zero
  counts (`nodesCreated: 1`) after `rows[0]:`. You do not need
  `includeCounters`.
- Bookmarks and timings are not included.

## Errors

With TOON every failure has an error status and this body:

```
errors[1]{code,message}:
  Neo.ClientError.Statement.SyntaxError,"..."
```

- `400`: the statement failed to parse or analyse, or failed while running
  (arithmetic, type errors), or a constraint was violated. Read the message,
  fix the statement and retry.
- `401` bad credentials; `404` unknown database or transaction.
- Unsupported Cypher (see Limits) is a `400`, never silently wrong results.

If you use JSON instead (the Neo4j-compatible default), a failure while
producing rows is HTTP `202` with the error in `errors` beside an empty
`data`. **There, check `errors` on every response, not just the status
code.**

## Transactions

Use a transaction only when several statements must succeed or fail together.
Otherwise run single statements, which auto-commit.

```sh
# open: the response has a transaction block with the id
curl -s -u "$CQ_USER:$CQ_PASSWORD" -H 'Accept: text/toon' -X POST {{BASE_URL}}/db/neo4j/query/v2/tx

# run statements (same body as above)
curl -s -u "$CQ_USER:$CQ_PASSWORD" -H 'Content-Type: application/json' -H 'Accept: text/toon' \
  {{BASE_URL}}/db/neo4j/query/v2/tx/<id> -d '{"statement":"CREATE (:Person {name:\"Bob\"})"}'

# finish: commit answers `committed: true`
curl -s -u "$CQ_USER:$CQ_PASSWORD" -H 'Accept: text/toon' -X POST {{BASE_URL}}/db/neo4j/query/v2/tx/<id>/commit
curl -s -u "$CQ_USER:$CQ_PASSWORD" -X DELETE {{BASE_URL}}/db/neo4j/query/v2/tx/<id>   # roll back
```

Rules that differ from a full Neo4j server:

- **One open transaction holds the whole database.** Other requests wait a few
  seconds and then fail with `Neo.TransientError.General.DatabaseUnavailable`,
  which names the open transaction. Keep transactions short and always commit
  or roll back. Idle ones are rolled back automatically.
- **A failed statement destroys the transaction.** Later calls on that id get
  `404`. Open a new one.

## Other output formats

Set `Accept` to choose another shape when you need it:

- `text/toon`: described above. Best for reading results.
- default / `application/json`: the Neo4j Query API shape
  (`{"data":{"fields":[...],"values":[[...]]},"bookmarks":...}`). Nodes and
  relationships are objects; dates and other special types become strings.
- `application/vnd.neo4j.query.v1.2` (Typed JSON; v1.0 and v1.1 also work):
  every value is `{"$type": "...", "_value": ...}`. About 2.5 times the size of
  plain JSON. Use it only when you must tell an integer from a float, or need
  exact temporal, point or vector values. To *send* typed parameters, set the
  same media type as `Content-Type`.
- `application/jsonl`: one JSON object per line, for large results. Typed
  JSON has a streaming form too: `application/vnd.neo4j.query.v1.2+jsonl`.

Prefer `LIMIT` on exploratory reads. Results are built in memory before they
are sent.

## Cypher support

charta implements Cypher 25 (it passes the openCypher TCK). You can use:

- `MATCH`, `OPTIONAL MATCH`, variable-length paths, `shortestPath()`,
  quantified path patterns, `WHERE`, `WITH`, `ORDER BY`, `SKIP`/`LIMIT`,
  `UNWIND`, `UNION`, `CASE`, comprehensions, `EXISTS`/`COUNT`/`COLLECT`
  subqueries, `CALL { ... }`
- `CREATE`, `MERGE ... ON CREATE / ON MATCH`, `SET`, `REMOVE`, `DELETE`,
  `DETACH DELETE`, `FOREACH`
- aggregation, string, math, list and temporal functions; `point` and
  `vector` values
- schema: `CREATE INDEX`, `CREATE CONSTRAINT` (unique, key, existence, type),
  `SHOW INDEXES`, `SHOW CONSTRAINTS`, vector indexes with
  `CALL db.index.vector.queryNodes(...)`
- `EXPLAIN` / `PROFILE`

## Explore an unknown graph

Do this before writing queries against data you haven't seen:

```cypher
CALL db.labels()
CALL db.relationshipTypes()
CALL db.propertyKeys()
SHOW INDEXES
SHOW CONSTRAINTS
MATCH (n) RETURN labels(n) AS labels, count(*) AS n ORDER BY n DESC
MATCH ()-[r]->() RETURN type(r) AS type, count(*) AS n ORDER BY n DESC
MATCH (n:Label) RETURN n LIMIT 5          // sample properties
```

## Limits

- Not executed: `USE`, user / role / database management (`CREATE USER`, ...),
  nested quantified path patterns, APOC and other plugin procedures. There is
  one database and one user.
- `LOAD CSV` works only if the server was started with an import directory.
- Sessions, routing and bookmark-based causal consistency do nothing. There is
  a single embedded database.

## Writing good statements

- Use `MERGE` on a unique key for idempotent loads, and create a uniqueness
  constraint first so duplicates are rejected.
- Constraint violations roll back the whole statement and report
  `Neo.ClientError.Schema.ConstraintValidationFailed`.
- Check the counters after a write (`nodesCreated`, `propertiesSet`, ...) to
  confirm the effect. TOON reports them automatically; with JSON set
  `includeCounters`.
- Return named columns (`RETURN p.name AS name`) so the column names are readable.
