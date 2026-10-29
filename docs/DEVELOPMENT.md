# Development and verification

Package layout and the core rules are in [AGENTS.md](../AGENTS.md); test placement,
fixtures and the oracle-first workflow are in [TESTING.md](TESTING.md).

## Workflow

1. **Plan.** Identify the owning package (the lowest one that can own the change),
   the affected seams and invariants, the PostgreSQL 18 behavior to match, and how
   you will verify it. Read the owning package README first.
2. **Write the failing test.** Add or change a dual-mode reference test, run it
   against the pinned PostgreSQL 18 oracle, and record the oracle's results as
   expectations. Then confirm it fails in quick mode. Package-internal unit tests
   may supplement it.
3. **Implement** the smallest correct change by extending existing seams (binders,
   catalogs, storage, codecs); do not add parallel paths or compatibility shims.
4. **Refactor** with the tests passing. Update the owning package README if its
   contract changed, and add a brief `CHANGES.md` entry for consumer-visible changes.
5. **Verify.** Run the selected tests in both modes, then `make quickcheck`. Rebase
   on the current integration tip and rerun if the tip advanced while you verified.

## Make targets

Targets impose no deadline (`-timeout=0`) and keep output quiet; test and generation
targets regenerate the parser and Unicode tables first. Agents set external
deadlines: 900 s for `check`, `racecheck` and `bench`; 1800 s for `quickcheck` and
`finalcheck`. Fix slow or hanging tests rather than exempting them.

| Target | Purpose |
| --- | --- |
| `make generate` | Regenerate `internal/parser/grammar_gen.go` and `internal/sqltype/unicode16` tables. |
| `make build` | Compile all packages. |
| `make check RUN='...'` | Selected tests only (`RUN` required). `RACE=1` adds `-race`; `REFERENCE=1` also runs the pinned PostgreSQL backend. |
| `make quickcheck` | Full Docker-free suite; run after implementation and rebases, before review or merge. |
| `make racecheck` | Full Docker-free suite with `-race`. |
| `make bench` | `BenchmarkOpenDatabase`: fresh engine, connector, pool, Ping and Close; five runs with allocations. Target: under 1 ms/op. |
| `make finalcheck` | Generation, the startup benchmark and the dependency list. |
| `make deps` | Non-standard dependencies of the library (`. ./internal/...`) and third-party ones of `./cmd/...`. |
| `make race` | Very slow full race suite with `noservertests`. Run only when explicitly asked. |
| `make refcheck` | Complete real-PostgreSQL suite. **Human-only; agents never run it.** |

Timings on the shared development machine inflate two to three times while other
sessions run suites. Check `uptime` and compare allocation counts before
attributing a timing change to code; report measurements, never assert timings
in tests.

## Generated code and data

- **Parser.** `go generate ./internal/parser` runs `internal/parser/generate.sh`,
  which runs pinned `goyacc@v0.50.0` (`-l -p sql -o grammar_gen.go -v /dev/null
  grammar.y`) on a private copy of `grammar.y` and atomically renames the result
  over `grammar_gen.go`. Edit only `grammar.y`; never hand-edit `grammar_gen.go`.
  Grammar actions must never publish or retain addresses of goyacc semantic cells
  (`&$n`): copy the value to an owned local or allocate a fresh node.
- **Unicode.** Unicode 16.0.0 inputs live in `internal/sqltype/unicode16/data`;
  `make generate` rebuilds the checked-in tables.
- **Encodings.** Conversion tables are generated offline from PostgreSQL REL_18_0
  sources by the generator in `internal/sqltype/pgencoding18/`; keep the upstream
  licenses and notices when regenerating.
- **Time zones.** Embedded tzdata provenance is in `internal/sqltype/tzdata/README.md`.

## Dependencies

- Library code (`.` and `internal/...`, the first `make deps` list) may depend
  only on the standard library and `stuntgres/...` packages. `cmd/stuntgres` may
  also use cobra and viper with their dependencies (the second list); library
  packages never import them. goyacc is a generation tool.
- lib/pq, pgx, sqlmw and testcontainers are test-only. `reference/fixture` is test
  support and must never be imported by production code.

## Code statistics

`scripts/code_stats [repository-root]` reports tracked source files, physical lines
and nonblank code lines split into production, tests and totals (Python 3 and Git;
read-only). Tests are `*_test.go`, Python test files and sources under
`reference/`, `smoke/` or `tests/`; documentation and data are excluded.

## Performance tools

Tools for finding what makes the quick suite slow; each script's header
documents its arguments. Run Go-building steps on the test host (for example
through the remote-run skill); `python3 -m unittest scripts/tests/test_perf_tools.py`
tests the Python tools.

- `scripts/perf_profile_suite OUT [PKG...]` runs the suite (default `./...`) with a
  CPU profile, wall/CPU/RSS and start time per test binary and merges the profiles.
- `scripts/perf_suite_report OUT [PKG_SUFFIX...]` prints packages by wall time with
  CPU and start/end, the latest-ending binaries (the tail), total CPU and, for the
  named packages, per-test times. On a saturated host the suite takes about total
  CPU / cores; a long serial package at the end can dominate instead.
- `scripts/perf_ab BASE NEW OUT ROUNDS PKG...` compares the CPU of two trees' test
  binaries, base and new side by side per round. Single quickcheck timings follow
  host load; prove a change with alternating runs of unchanged tests.
- `scripts/perf_steplog install|remove|subset|same` installs a temporary
  `fixture.RunStep`/SQL logger (never commit it) and proves that a test dedupe
  keeps every step (`subset`) or that a refactor changes none (`same`).
