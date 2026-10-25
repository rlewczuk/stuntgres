# Agent development guide

## Where to look

- [README.md](README.md): what stuntgres is and how consumers use it.
- [PostgreSQL 18 documentation](https://www.postgresql.org/docs/18/sql.html): the
  authority for SQL syntax and behavior. Do not copy it into `docs/`.
- [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md): workflow, make targets, generation, dependencies.
- **MANDATORY for any test work:** read and follow [docs/TESTING.md](docs/TESTING.md)
  before adding, changing or migrating a test.
- [docs/DRIVER.md](docs/DRIVER.md) and [docs/FIXTURES.md](docs/FIXTURES.md): public Go API contracts.
- Each package's `README.md`: its contract and `## Testing` instructions.
- `docs/design/`: design notes; `docs/ENGINE-SPLIT*.md`: engine package layering
  (§3 of `ENGINE-SPLIT.md`); `.cswd/`: catalog-part workflow.

## Package map

- Root `stuntgres`: `database/sql` driver/connector, Go APIs (settings, notices,
  COPY, FDW registration, Reset/Dump/Restore). Internal packages never import it.
- `internal/parser`: AST, lexer, keywords and goyacc grammar. Edit `grammar.y`, run
  `make generate`; never hand-edit `grammar_gen.go`.
- `internal/engine`: the analysis host, transactions, settings and lifecycle
  publication. Its subpackages (`ids`, `expr`, `functions`, `catalog`, `storage`,
  `lock`, `visibility`, `plan`, `exec`, `analyze`, `session`, `dml`, `ddl`, `table`,
  `index`, `view`, ...) are layered; each README states its contract. Put new code
  in the lowest package that can own it (new builtins: the `functions` registry).
  Subpackages never import the root engine.
- `internal/sqltype`: type descriptors, I/O codecs, coercion and comparison. Reuse
  it instead of adding adapter-local conversions.
- `internal/util`: SQL-independent data structures (e.g. `PersistentMap`); stdlib only.
- `internal/sqlstate`: shared SQL errors; root `Error` aliases it. Tests assert the
  SQLState interface, not `*pq.Error`.
- `cmd/stuntgres`: the wire-protocol server.
- `reference/`: public SQL/API tests, one shared body run natively (quick mode) and
  against pinned real PostgreSQL 18. `reference/fixture` is the shared fixture.
- Root and internal `*_test.go`: non-SQL adapter and package unit tests only.
  `smoke/`: legacy; do not add tests there.

Production dependencies: standard library and this module only; `cmd/stuntgres`
may also use cobra and viper (with their dependencies). goyacc is generation-only; pq, pgx, sqlmw and testcontainers are test-only. Reuse existing
seams, binders and storage paths; do not build parallel implementations.

## Development rules

- **Plan first:** affected seams, PostgreSQL evidence, invariants, verification.
  Keep scope explicit.
- **Test first:** every PostgreSQL feature or compatibility bug gets new or changed
  dual-mode reference coverage. Run it against real PostgreSQL first and record the
  oracle's results as expectations. Then watch quick mode fail, implement the
  smallest correct change, and refactor with tests passing (details in TESTING.md).
- **Reference tests are the golden standard.** Never loosen, disable or skip them to
  make a change pass; only test bugfixes and additions are allowed. Resolve
  conflicting requirements by real PostgreSQL 18 behavior.
- **Assert outcomes.** Assert consumer-visible results, errors, atomicity and
  lifecycle against independently specified expectations, never source text, call
  counts or mock echoes. Cover direct and prepared paths. Keep test output quiet.
- **Code shape.** Write clear, focused functions and short files split by
  responsibility. Reference test files have a hard 750-line limit; the shared
  fixture and generated files are exempt.
- **Concurrency.** Document lock ownership and order. Never acquire recursively, and
  never run callbacks or external I/O under a lock. Release resources on every exit.
  Publish immutable snapshots atomically. Synchronize deterministically, never with
  sleeps.
- **Parser.** State and options are per call; keep source positions and parameter
  indices in the AST; consume the entire input. Never execute a valid prefix and
  ignore the suffix.
- **Behavior changes.** Update callers and the owning docs, and remove obsolete
  paths instead of adding shims.

## Documentation rules

- Write change notes, change logs, history, task/step IDs, run results and proof
  notes **only** in [CHANGES.md](CHANGES.md): a brief entry at the top for
  consumer-visible changes. Never put them in README.md, `docs/` or package READMEs.
- `README.md` is consumer-facing only: what the project is, usage, server, building.
- A package `README.md` states the current contract: responsibility, layering,
  ownership, invariants, lock order and entry points. Its `## Testing` covers
  package-specific helpers, rules and one reusable selector per test family, never
  per-feature smoke or proof lists. Shared test policy belongs only in TESTING.md.
- Do not restate PostgreSQL documentation or list differences from PostgreSQL; a
  difference is a bug to fix, not a documented limit.
- When behavior changes, edit the owning document in place and keep it concise.

## Commands

Run from the repository root (Go ≥ 1.26; `REFERENCE=1` also needs Docker):

```sh
make generate                     # regenerate parser and Unicode tables
make build                        # compile production packages
make check RUN='^TestName$'       # selected tests only; RACE=1, REFERENCE=1 optional
make quickcheck                   # full Docker-free suite before review/merge
make racecheck                    # full quick suite with -race
make finalcheck                   # generate, startup benchmark, deps
make bench                        # BenchmarkOpenDatabase, 5 runs with allocations
make deps                         # non-standard library and cmd dependencies
```

- **Deadlines.** Agents set external deadlines: 900 s for `check`, `racecheck` and
  `bench`; 1800 s for `quickcheck` and `finalcheck`. Targets impose none. Fix slow
  or hanging tests; do not raise deadlines or drop coverage.
- **Targets agents must not run.** `make refcheck` is human-only. Run `make race`
  only when explicitly asked.
- **Reference checks.** Every changed or new reference test must pass both
  `make check RUN='...'` and `make check REFERENCE=1 RUN='...'` before review.
  `RUN` must select individual tests or a small set.
- **Before review or merge.** Run `make quickcheck` after implementation and after
  rebases. Commit post-merge remedies separately. Report observed results and any
  blocked checks.
- **Oracle and dependencies.** The pinned PostgreSQL 18.6 image (UTF8, C locale) is
  documented in [reference/fixture/README.md](reference/fixture/README.md#postgresql-oracle-policy-and-provenance).
  `make deps` library output may contain only this module's paths. The startup benchmark
  target is under 1 ms per fresh engine (Ping and Close).
