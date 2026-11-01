# Engine split — status

This page tracks round 8 of the engine split, which splits
`internal/engine/core` into packages under `internal/engine/core/`. The plan is
[ENGINE-SPLIT.md](ENGINE-SPLIT.md); each item's scope is in the prompt file
linked from its line.

Items 1–204 are done: the earlier rounds and their closing reviews. Their record
is in `git log` and in this file at `7b262861a`
(`git show 7b262861a:docs/ENGINE-SPLIT-STATUS.md`). Item 205, closing review 5,
is superseded by this plan.

Last verified against `master` at `6a5061b23` (item 219 rebased on it).

## Next ready items

Every item below has all its needs ticked. Items that share no needs may run
in parallel on separate branches.

- 220.

## Linear plan

One item per session. Start one with "Implement Item N from Linear Plan in
`docs/ENGINE-SPLIT-STATUS.md`". Mode is from ENGINE-SPLIT.md §4; "core after"
is the §1 projection in MiB. Phase C (221–229) is gated by item 220.

206. [x] Tooling and owner census —
     [core](ENGINE-SPLIT-core.md#item-206--tooling-and-owner-census) · needs — ·
     P · core after 11.5 (docs commit) · tools retargeted to core in
     `.cswd/tmp/PG18-ENGINE-SPLIT-206-TOOLING-CENSUS/`; census of 2,849 core
     tests at `3320f21b6`. Rule 2 uses the plurality area (user decision).
     Core is 11.53 MiB; phase A moves 3.73 MiB and phase B 0.46. Rewritten:
     the §1 and §4 projection, and §3 (pgcatalog moves to L6.5; adds `xml` and
     `statistics`). Passed: `layers.py` 0, the item-212 `cmdmv` dry run with
     0 violations, `check_links`, `check_linear`. Quickcheck not needed (docs
     only); racecheck and finalcheck skipped at user request.
207. [x] Test seams in place —
     [core](ENGINE-SPLIT-core.md#item-207--test-seams-in-place) · needs 206 · P
     · core after 11.5 (`69025b2bb`, `c17408e51`, `378f1b83f`, `f6c26512b`, docs
     commit) · test files only: the statement and transaction-session accessor
     files are split into cursor, portal, prepared, scope, transaction,
     temporary, notification and session files; core and tablecmds mint test
     session IDs from `enginetest.NextSessionID()`; enginetest stage 3 adds
     `QueryFixture`, `PreparedDescription`, `PreparedExecute` and
     `ProtocolReader`, and core's session and transaction helper copies are
     gone. `readme-map.tsv` is in
     `.cswd/tmp/PG18-ENGINE-SPLIT-207-TEST-SEAMS-IN-PLACE/`. Core grows from
     11.61 to 11.64 MiB (the `enginetest.` qualifiers). Passed: core and
     tablecmds packages, the 28 session-ID files alone, `revcheck` (test names
     unchanged; one duplicate literal left with the deleted `transactionRows`
     copy), quickcheck. Racecheck and finalcheck skipped at user request.
208. [x] Builtin function, type and extension tests —
     [core](ENGINE-SPLIT-core.md#item-208--builtin-function-type-and-extension-tests)
     · needs 207 · P · core after 10.9 (`7b29bdf50`, `5012f8abc`, `d3d2f660d`,
     `bc5f842eb`, `ef4ec863f`, `aa90090a6`, docs commit) · test files only: 38
     mixed files split by test function (41 parts), then 119 accessor-free files
     with 216 tests move to `core/enginetests/functions` (0.19 MiB), `types`
     (0.34), `extension` (0.25) and `textsearch` (0.01); 26 helpers that two or
     more of them share move to `enginetest` (`folds.go`, `catalog_stage.go`,
     `expressions.go`). Core 11.08 → 10.34 MiB. Passed: the four packages, the
     files with package globals alone, `RACE=1` for the concurrency files,
     `revcheck.py` (test names unchanged), `layers.py`, quickcheck. Racecheck
     and finalcheck skipped at user request.
209. [x] DDL family tests —
     [core](ENGINE-SPLIT-core.md#item-209--ddl-family-tests) · needs 207 · P ·
     core after 9.6 (`5d6bd7653`, `f6a1c3baa`, `48133b565`, `16b4e18cb`,
     `fa11d6286`, `6d82aa38d`, `ee055850f`, `a0da5fc37`, `98ce0e13d`,
     `5d8cf4e3e`, `3e20e5d9a`, `7836018dc`, docs commit) · test files only: 49
     mixed files split by test function (60 parts), then 230 accessor-free files
     with 360 tests move by the re-run census to `core/enginetests/tables` (380
     KiB), `stored` (239), `indexes` (238), `dump` (223), `roles` (160), `views`
     (155), `schema` (82) and `routines` (41), plus five type leftovers into
     `types`; 42 helpers that two or more of them share move to `enginetest`.
     Core 9.21 → 7.83 MiB. Passed: per-commit compile, the nine packages and
     core, the files with package globals or session IDs alone, `RACE=1` for the
     concurrent-index, lock and wait files, `revcheck.py` (test names
     unchanged), `layers.py`, deps, quickcheck. Racecheck and finalcheck skipped
     at user request.
210. [x] Catalog producer tests —
     [core](ENGINE-SPLIT-core.md#item-210--catalog-producer-tests) · needs 207 ·
     P · core after 9.0 (`2586cf192`, `ac9603d71`, `f1bc37edb`, docs commit) ·
     test files only: 27 accessor-free `pg_*` and catalog producer files move to
     `core/enginetests/pgcatalog` (289 KiB) and 16 `info_*` files to
     `core/enginetests/infoschema` (256 KiB), 117 tests, by the re-run census;
     `TestPGSubscriptionColumnAuthority` (owner roles) is split off and stays.
     Core shrinks from 11.67 to 11.18 MiB. Passed: new packages and per-commit
     compile, `revcheck` (test names unchanged; only gained literals, all from
     56 helper copies proven identical), `layers.py`, deps, quickcheck (three
     reference tests failed under load average 200 and passed alone). Racecheck
     and finalcheck skipped at user request.
211. [x] Query and DML feature tests —
     [core](ENGINE-SPLIT-core.md#item-211--query-and-dml-feature-tests) · needs
     207 · P · core after 7.8 (`10ed29283`, `2ad5a7ec7`, `89320ee08`,
     `048d4b69d`, `9317d50f6`, `93d756868`, `5201e030e`, `698588293`, docs
     commit) · test files only: 33 mixed files split by test function (35
     parts), then 201 accessor-free files with 441 tests move to
     `core/enginetests/binder` (0.54 MiB), `executor` (0.41), `writes` (0.16),
     `locking` (0.09) and `sessions` (0.03), by the re-run census; four lock and
     DML helpers that two or more of them share move to `enginetest`. Core 10.31
     → 9.21 MiB (at `3175b2b35`). Passed: per-commit compile, the five packages
     (each test alone too), `-race` over them, `revcheck.py` (test names
     unchanged; only gained literals, from 154 helper copies checked identical),
     `layers.py`, library deps, quickcheck. Racecheck and finalcheck skipped at
     user request.
212. [x] Explain pilot — [core](ENGINE-SPLIT-core.md#item-212--explain-pilot) ·
     needs 206 · P, bench · core after 7.7 (`11b535b5d`, `e87134e1a`,
     `9fa0e76ca`, `f60d36322`, `27aff0554`, docs commit) · the EXPLAIN renderer
     (11 files plus the new `explain_host.go`, 114 KiB) and its internal JSON
     presentation test move to the leaf `core/explain` (L14.5) over
     `explain.Host` (16 methods on `statementHost`) and `explain.Authority`
     (implemented by `AuthorizationView`); one recorded T6 exception,
     `Renderer.PresentedNodes` (user decision). Core 11.67 → 11.57 MiB. T7: an
     engine test inside `core/explain` costs about +34 s CPU and +5–16 s wall
     per build, so engine tests of leaves go to `core/<x>/enginetests`. Bench:
     startup and EXPLAIN allocations unchanged. Passed: quickcheck (two deadline
     load flakes passed alone), REFERENCE=1
     `^TestReference(Explain|PlannerCosts)`, `layers.py` 0, `revcheck.py`, deps.
     Racecheck and finalcheck skipped at user request.
213. [x] Host capability seam —
     [core](ENGINE-SPLIT-core.md#item-213--host-capability-seam) · needs — · P,
     bench · core after 7.7 (`66e8f81a5`, `b861d4501`, docs commit) ·
     `exec.Host` capability accessors `Contrib`, `TextSearch`, `CatalogInquiry`,
     `SessionInquiry`, `TransactionInquiry` (57 methods leave Host, 109 → 57;
     core implements each group on its own `StatementScope` conversion), and the
     `exec.NestedSQL` SPI capability on `statementHost` used by ts_rewrite,
     ts_stat, crosstab and connectby. In place; core 11.64 MiB (+5.5 KiB), exec
     +6.1 KiB. Bench: startup and inquiry allocations unchanged; +1 alloc per
     nested-SQL function call (the consumer). Passed: quickcheck (two load
     flakes passed alone), REFERENCE=1
     `^TestReference(Amcheck|Tablefunc|Connectby|FTS|Thesaurus|Size)`,
     `layers.py`, `frames.py check`, `lits.py`, deps. Racecheck and finalcheck
     skipped at user request.
214. [x] Contrib runtimes —
     [core](ENGINE-SPLIT-core.md#item-214--contrib-runtimes) · needs 207, 212,
     213 · P, bench · core after 7.6 (`0b5cb58a9`, `3d8bb9743`, `ec2988fd6`,
     `46f898f64`, docs commit) · the contrib runtimes (14 files plus the new
     `host.go`, 66.5 KiB: amcheck, `verify_heapam`, pageinspect, pgrowlocks,
     pgstattuple, tablefunc, connectby, generic files) move to the leaf
     `core/contrib` (L14.5) over `contrib.Host[A]` (shared method set on
     `statementHost`, 28 new methods) and `contrib.Authority` (implemented by
     `AuthorizationView`), generic over the authorization value; core keeps
     `contribHost`'s `exec.ContribRuntime` adapters, `WithMaintenanceOwner`
     (shared with CLUSTER), the pgrowlocks/`verify_heapam` readmission fences
     and pgstattuple's owner cohort as host methods; `resultUsageName` becomes
     `exec.ResultUsageName`. The connectby descriptor units move as internal
     tests (two accessors deleted); no other test moves. Core 7.85 → 7.80 MiB.
     Bench: startup and 21 contrib probe allocations unchanged; `escdiff` shows
     no new heap object. Passed: quickcheck, REFERENCE=1
     `^TestReference(Amcheck|Pageinspect|Pgrowlocks|Pgstatindex|Tablefunc|Connectby|GenericFile)`,
     `layers.py` 0, `frames.py check`, `revcheck.py`, deps. Racecheck and
     finalcheck skipped at user request.
215. [x] Text-search runtime —
     [core](ENGINE-SPLIT-core.md#item-215--text-search-runtime) · needs 207,
     212, 213 · P, bench · core after 7.6 (`fb8b37d36`, `700f75839`,
     `43cb74a34`, docs commit) · the text-search runtime (10 files plus the new
     `host.go`, 38 KiB) moves to the leaf `core/tsearch` (L14.5) over
     `tsearch.Host[A]` (shared method set on `statementHost`, 6 new methods) and
     `tsearch.Authority[A]` (implemented by `AuthorizationView`), generic over
     the authorization value so it is never boxed; core keeps `textSearchHost`'s
     `exec.TextSearchRuntime` adapters and `dictionary_files.go` (a `DDLHost`
     method). No test moves (census: no rule-1 tests). Core 10.34 → 10.31 MiB.
     Bench: startup and 12 text-search probe allocations unchanged; `escdiff`
     shows no new heap object. Passed: quickcheck, REFERENCE=1
     `^TestReference(FTS|Dictionary|Thesaurus|TextSearch|TriggerBuiltins)`,
     `layers.py` 0, `frames.py check`, `revcheck.py`, deps. Racecheck and
     finalcheck skipped at user request.
216. [x] Statistics runtime —
     [core](ENGINE-SPLIT-core.md#item-216--statistics-runtime) · needs 207, 212
     · P, bench · core after 7.55 (`1c0e1592b`, `593b39acb`, `89d8a66f0`, docs
     commit) · the statistics runtime (11 files plus the new `host.go`, 44 KiB)
     moves to the leaf `core/stats` (L14.5): the relation and attribute
     statistics imports over `stats.Host[A]` (shared method set on
     `statementHost`, 5 new methods) and `stats.Authority`, generic over the
     authorization value; the `pg_stats` and `pg_statistic_ext_data` visitors,
     statistics expression recipes and the snapshot codec over
     `stats.SnapshotHost`. Core keeps one-line `exec.Host`, `pgcatalog.Runtime`
     and `ddl.Host` adapters, the imported-statistics reset and, as deviations,
     the snapshot binding host and the `StatisticsSnapshotCodec` constructor
     (the root is unchanged). No test moves (census: no rule-1 tests). Core 7.83
     → 7.80 MiB. Bench: startup and 9 statistics probe allocations unchanged;
     `escdiff` shows no new heap object. Passed: quickcheck, REFERENCE=1
     `^TestReference(Statistics|AttributeStatistics|AlterStatistics|Analyze)`,
     `layers.py` 0, `frames.py`, `revcheck.py`, deps. Racecheck and finalcheck
     skipped at user request.
217. [x] Native triggers —
     [core](ENGINE-SPLIT-core.md#item-217--native-triggers) · needs 215 · P,
     bench · core after 7.5 (`c1712c579`, `8d1945229`, `dc5c94c2f`, docs commit)
     · the native trigger bodies (9 files plus the new `host.go`, 37 KiB) move
     to the leaf `core/nativetrig` (L14.5) behind `nativetrig.Invoke` over
     `nativetrig.Host[A]` (embeds `tsearch.Host[A]`; shared method set on
     `statementHost`, 8 new methods), generic over `tsearch.Authority[A]`; core
     keeps the routine envelope in `invokeNativeTrigger` and, as a deviation,
     refint's parameterized nested SQL
     (`PrepareTriggerSQL`/`ExecuteTriggerSQL`). `tcn_payload_test.go` and
     `trigger_lo_test.go` move as internal tests (the lo units over a stub
     authority). Core 7.70 → 7.67 MiB. Bench: startup and 12 trigger probe
     allocations unchanged; `escdiff` shows no new heap object. Passed:
     quickcheck, REFERENCE=1
     `^TestReference(Autoinc|InsertUsername|Moddatetime|Refint|Tcn|LoManage)`,
     `layers.py` 0, `frames.py list` unchanged, `revcheck.py`, deps. Racecheck
     and finalcheck skipped at user request.
218. [x] Catalog runtime —
     [core](ENGINE-SPLIT-core.md#item-218--catalog-runtime) · needs 210, 212 ·
     P, bench · core after 7.4 (`add68f97e`, `3e955c780`, `f4a8ebd53`, docs
     commit) · the catalog source runtime (`EngineCatalogRuntime` →
     `catalogrt.Runtime` over an embedded `catalogrt.Host` and the captured
     `access.View`), the capture cache, keep filters, `CaptureRows`,
     `InitializeSources`, the partition, publication, column, parameter and
     routine-tree runtimes and the pg_shdepend classification move to the leaf
     `core/catalogrt` (L14.5, 80 KiB); core keeps the capture policy
     `openCatalogSourceLocked`, the Host (`catalogRuntime`, one allocation per
     capture as before) and `catalog_inquiry_runtime.go` (deviations). Tests
     build runtimes through `core.NewCatalogRuntime` and read them back with the
     new `core.CatalogRuntimeFieldsOf` (user decision; ~150 test files). Core
     7.77 → 7.70 MiB. Bench: startup, catalog captures and the cache unchanged
     or fewer allocations. Passed: quickcheck (core 231.7 s, catalogrt 0.2 s
     wall, load ~160), REFERENCE=1 `^TestReference(Pg|PG|Info|Catalog)`,
     `layers.py` 0 violations, `revcheck.py`, `frames.py`, deps. Racecheck and
     finalcheck skipped at user request.
219. [x] Inquiry runtime —
     [core](ENGINE-SPLIT-core.md#item-219--inquiry-runtime) · needs 213, 218 ·
     P, bench · core after 7.35 (`3391a8258`, `d2c46daa0`, `4666a5b14`, docs
     commit) · the catalog-object, session and transaction inquiry bodies (the
     three `exec.Host` capability groups of item 213: 14 files plus the new
     `host.go`, 45 KiB) move to the leaf `core/inquiry` (L14.5, imports
     `core/catalogrt`) over `inquiry.CatalogHost` (18 methods) and
     `inquiry.SessionHost` (21), both on `statementHost`'s shared method set;
     core keeps the inquiry snapshot and its captures, lazy body admission
     (`routine_inquiry.go`), the cluster size captures and every
     authorization-dependent answer as host methods
     (`inquiry_statement_host.go`), plus the 34 capability adapters. The other
     B-inquiry files (ACL, advisory, blocking, large objects, sequences,
     settings, …) are not capability groups and are recorded for item 220. No
     test moves. Core 7.67 → 7.64 MiB. Bench: startup and 24 inquiry probe
     allocations unchanged; `escdiff` shows no new heap object. Passed:
     quickcheck, REFERENCE=1 selections of the moved groups, `layers.py` 0,
     `frames.py list` unchanged, `revcheck.py`, deps. Racecheck and finalcheck
     skipped at user request.
220. [ ] Closing review 6 (checkpoint, docs only) —
     [ENGINE-SPLIT.md §4](ENGINE-SPLIT.md#item-220--closing-review-6) · needs
     207–219
221. [ ] PL/pgSQL above core —
     [core](ENGINE-SPLIT-core.md#item-221--plpgsql-above-core) · needs 220 · S,
     bench · core after 6.65
222. [ ] Cursor, prepare and COPY commands —
     [core](ENGINE-SPLIT-core.md#item-222--cursor-prepare-and-copy-commands) ·
     needs 220 · S, bench · core after 6.15
223. [ ] DML statement assembly —
     [core](ENGINE-SPLIT-core.md#item-223--dml-statement-assembly) · needs 220 ·
     S, bench · core after 5.95
224. [ ] Event triggers — [core](ENGINE-SPLIT-core.md#item-224--event-triggers)
     · needs 220 · P, bench · core after 5.9
225. [ ] Advisory locks and notifications —
     [core](ENGINE-SPLIT-core.md#item-225--advisory-locks-and-notifications) ·
     needs 220 · P, bench · core after 5.55
226. [ ] Settings commands —
     [core](ENGINE-SPLIT-core.md#item-226--settings-commands) · needs 220 · P,
     bench · core after 5.35
227. [ ] Foreign-data and replication commands —
     [core](ENGINE-SPLIT-core.md#item-227--foreign-data-and-replication-commands)
     · needs 220 · P · core after 5.25
228. [ ] Hub accessor retirement —
     [core](ENGINE-SPLIT-core.md#item-228--hub-accessor-retirement) · needs
     221–227 · P · core after 3.9
229. [ ] Core engine test packages —
     [core](ENGINE-SPLIT-core.md#item-229--core-engine-test-packages) · needs
     228 · S · core after ≈ 2
230. [ ] Closing review 7 —
     [ENGINE-SPLIT.md §4](ENGINE-SPLIT.md#item-230--closing-review-7) · needs
     229

## Updating this file

Every split session updates this page in the same commit that records its
item.
1. **Before starting**, check this page against `git log`. If they disagree,
   trust `git log`, fix the page and say so in the report.
2. **When an item is done** (ENGINE-SPLIT.md §5.3), tick it. Add its commit
   hashes in parentheses, with the docs commit marked `docs`, and one line:
   what moved, core's size and the sizes of the packages it created or grew,
   and which checks passed or were skipped.
3. **Leave an item unticked** if a check failed or was blocked, with a short
   note such as `*(blocked: REFERENCE=1 needs Docker)*`.
4. **Keep the plan strictly linear.** New items go at the end with the next free
   number, a needs list naming only earlier items, and a section in a prompt
   file. Never renumber, and never add lettered sub-items. If an item splits,
   the part left over becomes a new item at the end.
5. **Rewrite "Next ready items"**: every unticked item whose needs are all
   ticked.
6. **Update "Last verified"** with the `master` commit you checked against.

Keep entries short. Details and notes for later items go in the prompt file's
Status section.
