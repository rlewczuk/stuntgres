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
- [docs/GRIND.md](docs/GRIND.md): grind-testing external Go applications against
  stuntgres (safety review, `docker/grind-sandbox/`, modes, differences, repros).

## Package map

- Root `stuntgres`: `database/sql` driver/connector, Go APIs (settings, notices,
  COPY, FDW registration, Reset/Dump/Restore). Internal packages never import it.
- `internal/parser`: AST, lexer, keywords and goyacc grammar. Edit `grammar.y`, run
  `make generate`; never hand-edit `grammar_gen.go`.
- `internal/engine`: the composition; `core` is the runtime (transactions, settings,
  publication). Its subpackages (`ids`, `expr`, `functions`, `catalog`, `storage`,
  `lock`, `visibility`, `plan`, `exec`, `analyze`, `session`, `dml`, `ddl`, `table`,
  `index`, `view`, ...) are layered; each README states its contract. Put new code
  in the lowest package that can own it (new builtins: the `functions` registry).
  Subpackages never import the root engine.
- `internal/sqltype`: type descriptors, I/O codecs, coercion and comparison. Reuse
  it instead of adding adapter-local conversions.
- `internal/util`: SQL-independent data structures (e.g. `PersistentMap`); stdlib only.
- `internal/sqlstate`: shared SQL errors; root `Error` aliases it. Tests assert the
  SQLState interface, not `*pq.Error`.
- `pgserver`: importable wire server; read its [API and lifecycle contract](pgserver/README.md)
  for TCP/pipe transport, admission, cancellation, logging and shutdown work.
- `cmd/stuntgres`: thin CLI/process/PID/TTY adapter over `pgserver`.
- `internal/serverauth` and `internal/serverref`: shared account/userfile and
  reference-option owners used by the embedded server and CLI.
- `dual`: `database/sql` connector mirroring every call to a caller-supplied
  reference connector and logging JSONL differences; stdlib only.
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
- **Behavior changes.** Update callers and any affected reusable documentation
  contracts, and remove obsolete paths instead of adding shims.
- **Prepared values.** Retain only owned immutable typed artifacts, never graphs,
  frames, borrowed scratch or authority. Preserve separate analysis and complete
  fold-planning publications; dependency purposes must not suppress analysis.
  Equal immutable outputs alone do not prove retention: pair a distinguishing
  public lifetime scenario with private owner/generation/backing-state assertions.
- **Simulated but query compatible** Retain full query compatibility, but certain
  non-functional aspects of original postgres may be loosened, for example there is
  no need to maintain components that work on physical files or related metrics.
  For query compatibility, settings and statistics provided by missing components 
  should also be simulated, returned values do not have to be accurate, can be 
  approximate or mocked up. Other non-functional aspects like remote database links
  should also be simulated.

## Documentation rules

- Change information belongs in Git commit messages. Do not maintain `CHANGELOG.md`,
  `CHANGES.md` or change-history entries in any other file. Creating or editing
  documentation solely to record changes is forbidden.
- Keep the established commit subject, including the part ID and one-line
  description for catalog work. Follow it with a blank line and a few `-` bullet
  points describing the concrete changes made. Use this format for checkpoints
  and the final squash commit; summarize actual changes, not planned work.
- `README.md` is consumer-facing only: what the project is, usage, server, building.
  Never add notes about individual statements, functions or system views to it.
- Package `README.md` files contain only highly reusable contracts: public seams,
  responsibility, layering, ownership, crucial design decisions, invariants and
  lock order. Omit feature inventories, individual SQL cases, implementation
  trivia and historical notes. `## Testing` covers package-specific helpers and
  one reusable selector per test family, never per-feature smoke or proof lists.
  Shared test policy belongs only in TESTING.md.
- Do not restate PostgreSQL documentation or list differences from PostgreSQL; a
  difference is a bug to fix, not a documented limit.
- Edit the owning document in place only when its reusable contract needs updating;
  a code change alone does not require a documentation edit.

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
