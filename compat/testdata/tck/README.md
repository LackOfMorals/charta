# openCypher TCK

The Technology Compatibility Kit feature files in this directory (and the named
graphs in `../graphs`) are copied unmodified from the openCypher project:

- Repository: https://github.com/opencypher/openCypher (directory `tck/`)
- Commit: `677cbafabb8c3c5eed458fd3b1ec0daec8d67d23` (2026-03-20)
- Licence: Apache License 2.0 (each file carries the header)

220 feature files, 1,615 scenarios (about 3,900 once outlines are expanded).

To update: copy `tck/features/*` here and `tck/graphs/binary-tree-*` to `../graphs`,
update the commit above, regenerate the baseline report with
`TCK_REPORT=../../.plans/tck-baseline.md CGO_ENABLED=0 go test -tags=tck ./compat/...`
and review `../excluded.txt`.
