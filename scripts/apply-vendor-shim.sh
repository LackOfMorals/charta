#!/usr/bin/env bash
# apply-vendor-shim.sh — copy the charta neo4j vendor shim into place.
#
# Run this after `go mod vendor` to restore the shim that enables
# neo4j.ExecuteQuery to work with charta's DriverCompat.
#
# The shim source is tracked at internal/neo4jshim/charta_bridge.go.
# The vendor/ directory is gitignored; run `go mod vendor` then this script
# whenever the neo4j driver version changes.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SHIM_SRC="${REPO_ROOT}/internal/neo4jshim/charta_bridge.go"
SHIM_DST="${REPO_ROOT}/vendor/github.com/neo4j/neo4j-go-driver/v6/neo4j/charta_bridge.go"

if [[ ! -d "${REPO_ROOT}/vendor" ]]; then
  echo "vendor/ not found — run 'go mod vendor' first" >&2
  exit 1
fi

cp "${SHIM_SRC}" "${SHIM_DST}"
echo "Shim installed: ${SHIM_DST}"
