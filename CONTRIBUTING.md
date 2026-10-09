# Contributing to charta

Thank you for your interest in contributing to charta. This document covers prerequisites, how to run all test suites, how to add a new Cypher feature, and the benchmark baseline process.

---

## Prerequisites

- Go 1.26 or newer (matches the `go` directive in `go.mod`)
- No CGO required — charta uses `modernc.org/sqlite`, a pure-Go SQLite driver
- `git` for version control

No Docker, no database server, no external services. Everything runs in-process.

---

## Getting started

```bash
git clone https://github.com/LackOfMorals/charta.git
cd charta

# If vendor/ is absent, populate it and restore the neo4j vendor shim
go mod vendor
tail -n +3 scripts/charta_bridge.go > vendor/github.com/neo4j/neo4j-go-driver/v6/neo4j/charta_bridge.go

# Verify the build
CGO_ENABLED=0 go build ./...
```

---

## Running the test suites

### Unit and integration tests (main suite)

```bash
CGO_ENABLED=0 go test -count=1 ./...
```

This runs all unit tests (parser, analysis, interpreter, store) and the integration tests under each package. The `testdata/` package must be run explicitly:

```bash
CGO_ENABLED=0 go test github.com/LackOfMorals/charta/testdata
```

### Property-based tests (rapid)

Property-based tests use `pgregory.net/rapid` and are part of the main package:

```bash
CGO_ENABLED=0 go test -run TestRapid ./...
```

The rapid generators create random graphs and verify full round-trip fidelity through CREATE, MATCH, and JSON import/export cycles.

### TCK harness (openCypher Technology Compatibility Kit)

The TCK harness is opt-in to avoid slowing the main test suite. It uses Godog (Cucumber for Go) and runs inline Gherkin scenarios:

```bash
CGO_ENABLED=0 go test -tags=tck ./compat/... -v
```

Scenarios tagged `@skip` are excluded from execution; a pass-rate banner is printed at the end. All skipped scenarios have an inline `# unsupported: <reason>` comment.

### Benchmarks

```bash
# Run all benchmarks (10 s each)
CGO_ENABLED=0 go test -run=^$ -bench=. -benchtime=10s ./bench/... | tee bench/results/latest.txt

# Run a single benchmark (1 s, fast iteration)
CGO_ENABLED=0 go test -run=^$ -bench=BenchmarkMatchNodeByID -benchtime=1s ./bench/...

# Enable the 1M-node benchmark (disabled by default — ~30 s setup, ~500 MB RAM)
CGO_ENABLED=0 go test -run=^$ -bench=BenchmarkSingleHopTraversal_1M -bench-1m -benchtime=10s ./bench/...
```

### Cross-platform build check

Before opening a PR, verify the CGO-free build passes on all four target platforms:

```bash
GOOS=linux   GOARCH=amd64 CGO_ENABLED=0 go build ./...
GOOS=linux   GOARCH=arm64 CGO_ENABLED=0 go build ./...
GOOS=darwin  GOARCH=arm64 CGO_ENABLED=0 go build ./...
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build ./...
```

---

## How to add a new Cypher feature

Adding a new Cypher clause or expression follows a five-step pipeline:

### Step 1 — Extend the syntax

Syntax lives in `cypher/syntax` (a hand-written lexer and recursive-descent parser that builds a fully typed AST, stdlib only). Add the new AST node to `cypher/syntax/ast.go`, parse it in `clause.go` (clauses), `expr.go` (expressions) or `pattern.go` (patterns), and add golden and error-position tests next to it.

### Step 2 — Teach the analysis

If the construct binds variables, has typing or aggregation rules, or can fail at compile time, teach `cypher/analyze` about it and add a case to `analyze_test.go`. The TCK tests there catch false positives and require the exact error class and code.

### Step 3 — Execute it

Implement the construct in `cypher/interp` (clauses in `exec.go`, `project.go` or `write.go`; expressions in `eval.go` or `funcs.go`; patterns in `match.go`). Add cases to `cypher/interp/interp_test.go`, then run the TCK (`CGO_ENABLED=0 go test -tags=tck ./compat/...`) and add end-to-end tests in `testdata/integration_test.go`.

---

## Benchmark baseline process

When a change may affect query performance, capture a new baseline:

1. Run the full benchmark suite on the target hardware before your change:
   ```bash
   CGO_ENABLED=0 go test -run=^$ -bench=. -benchtime=10s ./bench/... | tee bench/results/before.txt
   ```
2. Apply your change.
3. Run the benchmarks again:
   ```bash
   CGO_ENABLED=0 go test -run=^$ -bench=. -benchtime=10s ./bench/... | tee bench/results/after.txt
   ```
4. Compare the results using `benchstat` (installable via `go install golang.org/x/perf/cmd/benchstat@latest`):
   ```bash
   benchstat bench/results/before.txt bench/results/after.txt
   ```
5. Include the `benchstat` output in your PR description if there is a measurable change.
6. On release tags, CI updates `bench/results/latest.txt` automatically.

---

## API stability commitment

**No breaking changes are made to the public API after v0.3 without a major version bump.**

"Breaking change" means any change that would cause a program that compiled and ran correctly against the previous release to fail to compile or produce different behaviour when run against the new release. This includes:

- Removing or renaming exported types, functions, methods, or constants
- Changing function signatures (parameter types, return types, parameter count)
- Changing the semantics of existing functions in incompatible ways
- Changing error types in ways that break `errors.As` / `errors.Is` callers

Additions (new exported symbols) and bug fixes that correct documented-incorrect behaviour are not considered breaking changes.

This commitment covers the root package (`github.com/LackOfMorals/charta`) and its sub-packages. Internal packages (sub-packages not documented for external use) are exempt.

For the compatibility table of supported Cypher features, see [README.md — Cypher Compatibility](README.md#cypher-compatibility).

---

## Pull request guidelines

- Open an issue before starting significant work so we can agree on the approach.
- Keep PRs focused: one feature or bug fix per PR.
- All new code must have unit tests. Aim for at least 80% coverage on new packages.
- `go vet ./...` must pass with no warnings.
- `CGO_ENABLED=0 go build ./...` must pass for all four target platforms.
- `CGO_ENABLED=0 go test -count=1 ./...` must pass with no failures.
- Doc comments are required on all exported symbols (types, functions, methods, constants).
- Follow the existing code style (no external linter configuration is required).
- Commit messages should be of the form `charta-task-NNN: short description`.
