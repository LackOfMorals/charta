# AGENTS.md — charta-query

## Overview

charta-query is an HTTP server that implements the Neo4j Query API (v2) on top
of the embedded [charta](https://github.com/LackOfMorals/charta) graph database
(Cypher over SQLite). Any client that talks Query API to Neo4j should talk to
this unchanged. It is single-user by design: one basic-auth credential, full
access.

- Module: `github.com/LackOfMorals/charta-query`, Go 1.26, CGO-free
- Single binary: `cmd/charta-query`; everything else is under `internal/`
- Only dependency: `github.com/LackOfMorals/charta`

## Commands

```sh
go build ./...
go vet ./...        # CI runs this
go test ./...       # CI runs this; also run by goreleaser before a release
go run ./cmd/charta-query --username admin --db :memory:   # needs CHARTA_QUERY_PASSWORD
```

`internal/compat` and `internal/perf` are skipped unless a real Neo4j is
configured through `CHARTA_QUERY_COMPAT_NEO4J_*` / `CHARTA_QUERY_PERF_NEO4J_*`
(see README). A plain `go test ./...` therefore does not exercise them. If you
change a response shape, status code or error code, run the compat test when a
Neo4j is available, and say so if it wasn't run.

## Layout

```
cmd/charta-query/   flags, env, logging setup, server lifecycle, `skill` subcommand
internal/api/       HTTP handlers, content negotiation, encoding, errors, CORS, logging
internal/auth/      basic-auth middleware
internal/config/    Config, env helpers, Version
internal/store/     wraps charta: transactions, read-only, import dir, reaper
internal/skills/    embedded agent skills (<name>/SKILL.md) served by CLI and HTTP
internal/compat/    side-by-side tests against a real Neo4j
internal/perf/      latency harness against a real Neo4j (measurement, not pass/fail)
```

## Conventions and constraints

- **Neo4j parity is the contract.** Status codes, error codes
  (`Neo.ClientError.…`), body shapes and `Accept`/`Content-Type` handling copy
  what Neo4j returns. Known differences are listed in the README and marked in
  `internal/compat`; add to both rather than diverging silently.
- **Success is `202`.** A failure during execution (arithmetic, type errors) is
  also `202` with `errors` beside an empty `data`; parse/semantic failures are
  `400`. Use `writeRunFailure` / the helpers in `errors.go`, not ad hoc writes.
- **`Accept: text/toon` is the one deliberate departure from Neo4j** (`toon.go`):
  a flat table for agents, with every failure an error status. It never
  applies to the other formats. The encoder is hand-written and was checked
  against `@toon-format/toon`; if you change quoting or number rules, compare
  against that library again.
- **Responses are marshalled before anything is written** (`writeJSON`), so an
  encoding failure is a clean 500, not a truncated body. Keep it that way.
- **One transaction holds the database.** charta serialises on a single
  connection, so the store waits `--tx-wait` then fails with
  `DatabaseUnavailable`. Don't add code paths that take the connection without
  going through the store.
- **Never log parameters or passwords.** Statement text is logged only when
  `--log-statements` is on.
- **Don't trust request headers for URLs.** Use `Handler.baseURL`, which
  prefers `--public-url` over `Host`/`X-Forwarded-Proto`.
- **Auth:** `/db/...` is behind basic auth. Public by design: `/`, `/health`,
  `/skills`, `/skills/{name}`. Anything new that is public must not return
  secrets or database content.
- **Config:** each flag has a `CHARTA_QUERY_*` env var (via `config.EnvOr` and
  friends). Add both, validate in `Config.Validate`, and document in the README
  table. Passwords are preferred via env, not flags.
- `cmd/charta-query/main_test.go` blanks all `CHARTA_QUERY_*` variables so a
  developer's shell can't leak in; do the same in any new test that calls `run`.
- Tests are table-driven, use real servers via `httptest` (`newTestServer`,
  `do` in `internal/api`), and avoid mocks of charta.

## Agent skills

The skill lives in `internal/skills/charta-query/SKILL.md` (Agent Skills format:
YAML frontmatter with `name` and `description`, then Markdown). It is embedded
with `go:embed` and served by `charta-query skill` and `GET /skills[/{name}]`.

- `{{BASE_URL}}` in the text is replaced with the server's URL when rendered.
- The skill is documentation for *other* agents. When behaviour changes
  (endpoints, formats, error handling, supported Cypher, limits), update it in
  the same change, and check every claim against a running server rather than
  from memory. Do not put credentials or real hostnames in it.
- A new skill is a new `internal/skills/<name>/SKILL.md`; the name must equal
  its directory, with no dots or slashes.

## Releases

Tag-driven via GoReleaser (`.github/workflows/release.yml`, `.goreleaser.yaml`):
linux/darwin/windows on amd64/arm64, `CGO_ENABLED=0`. `config.Version` is the
version reported by discovery and `--version`; bump it with the release.

## Git

Don't commit or push unless asked. Keep commits focused, and run `go vet ./...`
and `go test ./...` first.
