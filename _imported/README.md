# charta-query

An HTTP server that speaks the Neo4j Query API (v2) on top of an embedded
[charta](https://github.com/LackOfMorals/charta) graph database. Any client
that can send Query API requests to Neo4j can send them here.

It is built for one person or agent at a time. There is a single basic-auth
credential, and whoever holds it can do anything to the graph.

## Run

```sh
export CHARTA_QUERY_PASSWORD=change-me
go run ./cmd/charta-query --username admin --db charta.db

curl -u admin:change-me -H 'Content-Type: application/json' \
  localhost:7474/db/neo4j/query/v2 \
  -d '{"statement":"RETURN 1 AS n"}'
```

## Endpoints

| Method | Path | |
|---|---|---|
| GET | `/` | discovery (no auth) |
| GET | `/health` | liveness (no auth) |
| GET | `/skills` | list agent skills (no auth) |
| GET | `/skills/{name}` | one skill as Markdown (no auth) |
| POST | `/db/{db}/query/v2` | run a statement |
| POST | `/db/{db}/query/v2/tx` | open a transaction |
| POST | `/db/{db}/query/v2/tx/{id}` | run in a transaction |
| POST | `/db/{db}/query/v2/tx/{id}/commit` | commit |
| DELETE | `/db/{db}/query/v2/tx/{id}` | roll back |

Responses follow what a Neo4j server returns, checked side by side against
Neo4j 2026.07:

- Success is `202`. A failed statement is `400` with
  `{"errors":[{"code":"Neo.ClientError.Statement.SyntaxError","message":"…"}]}`.
  A failure *while* producing a result (arithmetic, type errors) is `202` with
  the error beside an empty `data` block.
- A failed statement inside a transaction destroys the transaction (later calls
  get `404`). Rollback answers `200 {}`.
- Unknown databases answer `404`; authentication failures `401`.
- Plain JSON, Typed JSON (`application/vnd.neo4j.query.v1.x`) and JSONL are
  supported via `Accept`, including comma-separated lists with q-values.
- `Accept: text/toon` is an extension for agents, see below.
- CORS is enabled and `OPTIONS` answers preflights (see `--cors-origins`).

**One transaction at a time.** charta serialises on a single connection, so
while an explicit transaction is open it holds the whole database. Other
queries, and attempts to open a second transaction, wait up to `--tx-wait`
(default 5s, enough for tools that open several short transactions at once)
and then fail with `Neo.TransientError.General.DatabaseUnavailable` naming the
open transaction, instead of hanging. Commit or roll it back; idle transactions are rolled back
after `--tx-timeout`.

## TOON output for agents

`Accept: text/toon` answers with a [TOON](https://toonformat.dev) table instead
of the Neo4j response document. It is an extension: Neo4j has no equivalent,
so use JSON when you need parity with a real server.

```sh
curl -u admin:change-me -H 'Content-Type: application/json' -H 'Accept: text/toon' \
  localhost:7474/db/neo4j/query/v2 \
  -d '{"statement":"MATCH (p:Person) RETURN p.name AS name, p.age AS age"}'
# rows[2]{name,age}:
#   Alice,30
#   Bob,null
```

- One flat table, `rows[N]{columns}:`. A node or relationship becomes columns
  (`p._id`, `p._labels`, `p.name`, ... and `r._type`, `r._start`, `r._end`);
  a path column is a Cypher pattern, `(:Person {name:Keanu})-[:ACTED_IN]->(:Movie {title:The Matrix})`;
  other lists and maps are a compact JSON string in one cell.
- No bookmarks or timings. Non-zero mutation counters are always reported
  (`counters:`), without `includeCounters`.
- Every failure is an error status with an `errors[N]{code,message}:` table,
  including one raised while producing rows, which JSON answers with `202`.
- Transactions work as usual: opening returns a `transaction:` block with the
  id, and commit answers `committed: true`.

### The same query, JSON and TOON

A statement that returns two nodes and the relationship between them, on the
Neo4j movies graph:

```cypher
MATCH (p:Person {name: $name})-[r:ACTED_IN]->(m:Movie {title: $title})
RETURN p, r, m
```

with parameters `{"name": "Tom Hanks", "title": "Cast Away"}`.

**JSON** (the default, the same document Neo4j returns; reformatted here, the
server sends it on one line):

```json
{
  "data": {
    "fields": ["p", "r", "m"],
    "values": [
      [
        {"elementId": "57", "labels": ["Person"], "properties": {"born": 1956, "name": "Tom Hanks"}},
        {"elementId": "97", "startNodeElementId": "57", "endNodeElementId": "165", "type": "ACTED_IN", "properties": {"roles": ["Chuck Noland"]}},
        {"elementId": "165", "labels": ["Movie"], "properties": {"released": 2000, "tagline": "At the edge of the world, his journey begins.", "title": "Cast Away"}}
      ]
    ]
  },
  "bookmarks": ["FB:AAAAAAAAAA8vsraGnMfqiQ=="],
  "queryType": "r",
  "resultAvailableAfter": 1,
  "resultConsumedAfter": 1
}
```

**TOON** (`Accept: text/toon`):

```
rows[1]{p._id,p._labels,p.born,p.name,r._id,r._type,r._start,r._end,r.roles,m._id,m._labels,m.released,m.tagline,m.title}:
  "57",Person,1956,Tom Hanks,"97",ACTED_IN,"57","165","[\"Chuck Noland\"]","165",Movie,2000,"At the edge of the world, his journey begins.",Cast Away
```

Each node and relationship is spread over columns: `p._id`, `p._labels`,
`p.born`, `r._type`, `r._start`, `r._end` and so on. A property a row lacks is
`null`, the `roles` list is a JSON string in its cell, and `"57"` is quoted
because it is a string that looks like a number (an element ID), not a number.
This one row is 147 tokens as JSON and 96 as TOON (GPT tokenizer); the header is
written once, so the saving grows with the number of rows. The element IDs
belong to this server and differ from Neo4j's.

### Token cost and accuracy

Measured on a synthetic graph of 80 people and 12 companies. These are small
experiments, not guarantees; your data will differ.

**Tokens** (GPT `o200k` tokenizer, so approximate for other models). Percentages
are against plain JSON as the server returns it:

| Result shape | TOON | Typed JSON |
|---|---|---|
| flat table, 25-50 rows | 81-90% | about 320% |
| wide table, 12 columns | 84% | 350% |
| 25 nodes | 51% | 246% |
| 25 `(p)-[r]->(c)` rows | 47% | 225% |
| `collect()` list cells | 93% | 300% |
| mixed benchmark, all of the above | **58%** | 254% |

Most of the saving comes from flattening nodes and relationships and dropping
bookmarks and timings, not from the TOON syntax itself. For a tiny result such
as a count, the fixed response metadata is most of the JSON.

On the Neo4j movies graph (49 queries, from counts to nodes, relationships
and paths) served TOON was 60% of the tokens of plain JSON; paths were 51%.

**Accuracy.** Haiku and Sonnet, with no tools, were given the same query
result as JSON or TOON and asked about 10 questions each (lookups, filtered
counts, sums, maximums, nulls, list membership) over 3 seeded datasets of 80
rows, with awkward values: numeric-looking strings such as `"007"`, text with
commas, colons, quotes and newlines, empty strings against nulls. Answers were
graded against the true values (246 questions per format):

| | JSON | TOON |
|---|---|---|
| Haiku | 92.7% | 95.1% |
| Sonnet | 96.7% | 97.6% |

The difference is within noise, so read it as parity, not as TOON being more
accurate. Lookups, joins, null handling and the tricky strings were correct in
both formats apart from one wrong zip in TOON. Nearly every error was
arithmetic over many rows (sums and counts), which models get wrong in either
format; an agent with a shell should compute those instead of reading them.
Limits: two models, one run per question, 80 rows, and the prompts included a
short description of each format (the skill carries the same description for
TOON).

## Agent skills

An agent can learn how to use the server (and the Cypher it supports) by
fetching a skill: a `SKILL.md` document in the
[Agent Skills](https://agentskills.io) format. The skill is embedded in the
binary, so it always matches the version you run, and its examples point at
your server.

```sh
# over HTTP (no authentication; also linked as "skills" in discovery)
curl localhost:7474/skills                   # [{name, description, url}]
curl localhost:7474/skills/charta-query      # Markdown

# from the command line, no server needed
charta-query skill --url https://graph.example.com > SKILL.md
charta-query skill --list
```

For Claude Code, save it as `~/.claude/skills/charta-query/SKILL.md`. Behind a
proxy, set `--public-url` so the HTTP version advertises the right address.
The skill holds no credentials; it tells the agent to ask the user for them.

## Configuration

Every flag has an environment variable (`--tx-timeout` is `CHARTA_QUERY_TX_TIMEOUT`).

| Flag | Default | |
|---|---|---|
| `--listen` | `:7474` | listen address |
| `--db` | `charta.db` | database file, or `:memory:` |
| `--username`, `--password` | required | basic-auth credential (prefer the env var for the password) |
| `--database-name` | `neo4j` | name advertised by discovery |
| `--read-only` | false | open the database read-only |
| `--tx-timeout` | `5m` | idle timeout for explicit transactions |
| `--tx-wait` | `5s` | how long a request waits for an open transaction to end before failing with "transaction X is open" (`0` fails at once) |
| `--import-dir` | | enables `LOAD CSV` below this directory |
| `--tls-cert`, `--tls-key` | | enable HTTPS |
| `--cors-origins` | `*` | origins allowed by CORS; `none` disables |
| `--public-url` | from request | base URL advertised by discovery |
| `--log-level` | `info` | `debug`, `info`, `warn`, `error` |
| `--log-format` | `text` | `text` or `json` |
| `--log-file` | stderr | append logs to a file |
| `--log-statements` | true | include Cypher text in request logs |

## Logging

One line per request: `request_id` (also returned as `X-Request-Id`), method,
path, status, duration, response `bytes`, remote address, user agent, and the
`user` name presented. Statement requests add `statement`, `query_type`,
`rows`, and `tx_id`. A failed statement adds `error_code` and `error`, and is
logged at WARN (ERROR for database errors) even though it answers `202`.
Passwords and query parameters are never logged.

## Testing

```sh
go test ./...
```

### Compatibility with a real Neo4j

`internal/compat` sends identical requests to a Neo4j server and to an
in-process charta-query and requires the responses to match (status, content
type and body, ignoring element IDs, bookmarks, timings, transaction IDs and
error wording). It covers every response format, parameters, errors, and
transactions over a small graph. It is skipped unless a server is configured:

```sh
CHARTA_QUERY_COMPAT_NEO4J_URL=http://localhost:7474 \
CHARTA_QUERY_COMPAT_NEO4J_PASSWORD=secret \
  go test ./internal/compat -v
```

`CHARTA_QUERY_COMPAT_NEO4J_USER` (default `neo4j`) and
`CHARTA_QUERY_COMPAT_NEO4J_DB` (default `neo4j`) are optional. The reference
database is never modified: writes happen in explicit transactions that are
always rolled back, on private `Cq*` labels, and no schema is created (Neo4j
does still register the property key names the writes used).

Known differences, each marked in the test with its reason:

- charta-query also supports Typed JSON over JSONL (`application/vnd.neo4j.query+jsonl`);
  Neo4j 2026.07 answers 406.
- A non-string `statement` is `Request.Invalid` here, `Statement.SyntaxError` in Neo4j.
- Neo4j adds `notifications` (such as deprecation warnings); charta-query has none.

Also seen when comparing 54 queries on the Neo4j movies graph (not covered by
the compatibility test; the rest matched, including every temporal and float
format):

- `avg` and `stDev` can differ in the last digits (charta-query computes
  `avg` more exactly; `stDev` is rounded to 15 significant digits).
- `keys(n)` lists the properties sorted; Neo4j lists them in insertion order.
- charta does not support `datetime({epochSeconds: ...})` (Neo4j does).
- Functions that return strings, such as `toString(localtime('12:30'))`, use
  charta's shortest form (`12:30`); only values returned as temporal types are
  written the way Neo4j writes them (`12:30:00`).

### Performance against a real Neo4j

`internal/perf` is a measurement harness, not a pass/fail test: it prints
latency tables (p50, p95, mean, speed-up) and requests per second for the
same requests against Neo4j and charta-query, and fails only if a request
errors. It covers request overhead and result serialisation for each response
format and size, then queries and writes over a small graph. It is skipped
unless a reference server is configured:

```sh
CHARTA_QUERY_PERF_NEO4J_URL=http://localhost:7474 \
CHARTA_QUERY_PERF_NEO4J_PASSWORD=secret \
  go test ./internal/perf -v -timeout 30m
```

`CHARTA_QUERY_PERF_NEO4J_USER` and `CHARTA_QUERY_PERF_NEO4J_DB` default to
`neo4j`. As with the compatibility test the reference database is never
modified: data-dependent cases build their graph on private `Cq*` labels in a
transaction that is always rolled back.

By default charta-query runs inside the test process and shares its CPU and
garbage collector. For a fair comparison start it separately and point the
harness at it (same credentials) with `CHARTA_QUERY_PERF_CHARTA_URL`.
