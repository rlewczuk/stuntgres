# Engine split: `internal/engine/core`, eighth round

Session prompts for linear items 206–219 and 221–229 of the
[status page](ENGINE-SPLIT-STATUS.md). The rules are in
[ENGINE-SPLIT.md](ENGINE-SPLIT.md): §2 (cuts and test rules T1–T9), §3 (layers
L14.5, L15 and L15.5, rules 1–10) and §5. Sizes are from `b7e30241f` (§1); every
item re-measures.

## How items in this round work

- **Census first.** A move set comes from item 206's census (`tests.tsv`,
  `layout.tsv`). Re-run it on the item's candidates, and on core test files
  added since the census commit. Never move a test by its file prefix alone.
- **Generated moves.** Each item keeps a replay script in `.cswd/tmp/<NAME>/`
  built on item 206's retargeted `cmdmv`. On a conflict, reset to master and
  replay.
- **Tests and accessors.**
  - Tests follow T3–T9.
  - A moved accessor entry lands in the destination's `export_test.go` only
    under T7. Otherwise its users stay in core until a later item moves the
    code they reach.
  - Shared helpers go to `enginetest` (T4).
- **Test-only packages.** A new `core/enginetests/<area>` holds a one-sentence
  `doc.go` and `package <area>_test` files. It stays under 2 MiB (T9).
- **Base verification** (every item adds its own selections):
  - after every commit: `make build` and `go test -count=1 -run '^$' ./internal/engine/... .`;
  - each package that gained tests: `make check` over the package, and each
    moved file alone if it touches session IDs or package globals;
  - `revcheck.py` and `layers.py`; `make deps`;
  - quickcheck, with the CPU figures (§5.3).
- **Core README.** Paragraphs move with their code, as `readme-map.tsv` (item
  207) assigns them. Each new package's README states its contract, layering,
  `Host` or hook, lock rules and `## Testing` selectors.

## Item 206 — tooling and owner census

Needs nothing. Mode P (tools and a docs commit; no code change).

1. **Retarget the tools.** Copy them into `.cswd/tmp/<NAME>/` and leave the
   originals unchanged.
   - `cmdmv`:
     - `-src internal/engine/core`;
     - nested `-dest core/<x>` for production moves with their tests;
     - a test-only mode for `core/enginetests/<area>` that writes `doc.go`;
     - moves accessor entries (T6) and rewrites `core.X` to `x.X` for moved
       names.

     It stays type-checked over the module: the root, the driver and `cmd`.
   - `closure` and `reach`: add `-pkg internal/engine/core`, with core's god
     types (`StatementScope`, `Engine`, `statementHost`, `EngineCatalogRuntime`,
     `DropProvider`, `DDLHost`, `BindingHost`).
   - `layers.py`: map packages by full import path, with the §3 rows and rules 9
     and 10.
   - `lits.py`, `revcheck.py`, `frames.py`: support nested packages.
2. **Census.**
   - **Build:**
     `go test -c -cover -covermode=set -coverpkg=stuntgres/internal/... -o core.cover.test ./internal/engine/core`.
     The probe at `b7e30241f` took 130 s wall under load and produced a
     130 MB binary.
   - **Run:**
     - each of the 2,810 top-level tests alone, with
       `-test.run '^Name$' -test.gocoverdir=DIR`, about 24 in parallel (on
       apu1 via the `remote-run` skill, or a quiet host);
     - fold each run into a covered-block bitset (`go tool covdata textfmt`)
       and delete the raw data, which is about 2 MB per test;
     - the probe measured 0.1–0.7 s per test, and about 6.8k of 214k blocks
       covered.
   - **Weights:** a block weighs its statements × log(N/n_b). Drop blocks
     covered by more than 20% of tests (engine open, session start, simple
     queries).
   - **Groups:**
     - each `internal/...` package below core;
     - each `*cmds` family;
     - the root;
     - core's file groups: one per B and C group in §1, plus "rest".
   - **Owner rule:**
     1. A test follows a B or C cut when that group's share is at least 0.4
        and at least twice the runner-up's.
     2. Otherwise, a lower package or family whose share is at least 0.5,
        while all core groups together have at most 0.3, makes the test join
        `core/enginetests/<that area>` (phase A).
     3. Otherwise, the test stays in core and its top core group names its
        later area (item 229).
     4. A file moves when at least 70% of its test bytes agree. Otherwise
        `split.py` (item 195's) splits it by test function if the split is
        clean, or it stays.
   - **Calibration:** run the census over `functions/enginetests` and
     `pgcatalog/enginetests`. At least 90% of their tests must classify to
     their own package; if not, adjust the weights and record the change.
3. **Outputs** in `.cswd/tmp/<NAME>/`:
   - `tests.tsv`: test, file, owner, share, runner-up, accessor files, helper
     dependencies, and flags (frame, allocation, synctest, session ID);
   - `layout.tsv`: each file with its destination; areas are pre-split so that
     every package stays under 2 MiB (T9);
   - `summary.txt`: projected package sizes.

   The docs commit records the summary in Status. It also rewrites the §1
   projection if the census differs from the prefix estimate by more than 20%.

**Verify:**
- a dry run of `cmdmv` for item 212's move set reports zero violations;
- `layers.py` on master reports zero violations;
- calibration passes;
- `check_links.py` and `check_linear.py` pass on the docs;
- quickcheck is not needed (no code change).

## Item 207 — test seams in place

Needs 206. Mode P. Test files only.

- **Split the mixed accessor files by subsystem.** Names stay the same and their
  users are untouched.
  - `export_statement_test.go` becomes cursor and SQL routine (52 names), portal
    (37) and prepared (9). The rest stays: statement host, replacement
    admission, execute, discard.
  - `export_txsession_test.go` becomes scope (50), transaction (39, including
    serializable and blocking), TEMP (9), notification (9) and session (5).
- **Session IDs.**
  - Add `enginetest.NextSessionID()`.
  - Replace `testSessionID` (`concurrent_index_type_test.go`, its `init` with
    the `1<<20` offset, and 20 user files in six areas) and the same offset in
    `tablecmds`.
  - Moved tests then never collide with `enginetest`'s counter ("duplicate
    advisory session").
- **`enginetest` stage 3.** Promote the helpers that two or more `layout.tsv`
  destinations need, and that use only the exported API:
  - `queryCoreFixture` (`query_test.go`);
  - the session helper copies (`session_test.go`) and the transaction helpers
    (`transaction_test.go`);
  - `sequencePreparedDescription` and `sequencePreparedExecute`;
  - `protocolUnitInput`.

  Helpers that need accessors stay in core.
- **`readme-map.tsv`.** Map each core README paragraph to a destination by the
  identifiers it names.
- **TESTING.md.** Add one placement sentence: tests extracted from core go to
  `internal/engine/core/<x>` with their code, or to
  `internal/engine/core/enginetests/<area>`.
- **Carried over from round 7.**
  - Some names are exported but used only in their own package:
    - core: `DomainCheckPhase`, `Engine.AdmitRuleCommandsLocked`,
      `Engine.PrepareCTECommandsLocked`,
      `Engine.ExecutePreparedCTECommandsLocked`,
      `Engine.RegtypeObservationLocked`, `Engine.SequenceObservationLocked`,
      `PreparedCTEProgram`, `ProgramPhases`, `RuleActionScope`,
      `TemporaryDataTarget`;
    - `tablecmds`: `BindGeneration`, `DropScope`, `GenerationNamespace`.

    Leave them unless a move needs them.
  - Fix `enginetest`'s README, which still names the root originals of its
    helpers.

**Verify:**
- the core and `tablecmds` packages, and `enginetest`'s users;
- the session-ID users each alone;
- `revcheck.py` (only the accessor split and the helper renames);
- quickcheck.

## Item 208 — builtin function, type and extension tests

Needs 207. Mode P. Test files only.

- **What moves:** accessor-free core test files whose census owner is
  `functions`, `sqltype`, `typedef`, `regobj` or `extension`. The prefix
  estimate is 0.6–0.8 MiB:
  - array, string, `query_window`, `query_regexp`, aclitem;
  - hstore, ltree, seg, ean13, reg*;
  - snowball and synonym (the stemmers live in `functions`);
  - `extension_*`.
- **Destinations:** `core/enginetests/functions`, `core/enginetests/types` and
  `core/enginetests/extension`, as named in `layout.tsv`, each under 2 MiB.
- **What stays:** extension tests that stage providers through core accessors.

**Verify:** the base checks. `REFERENCE=1` is not needed (no production change).

## Item 209 — DDL family tests

Needs 207. Mode P. Test files only.

- **What moves:** accessor-free core test files whose census owner is a `*cmds`
  family or `table`, `index`, `partition`, `view`, `typedef` or `role`. The
  prefix estimate is 0.3–0.55 MiB: alter, drop, view, index, partition,
  database, collation, domain, operator, type, access, conversion, materialized
  and inheritance.
- **Destinations:** `core/enginetests/<area>` (tables, indexes and partitions,
  views and rules, types and domains, schema objects, roles), merged or split
  to stay under 2 MiB.

**Verify:** the base checks, plus `RACE=1` for concurrent-index and
lock-timeout files.

## Item 210 — catalog producer tests

Needs 207. Mode P. Test files only.

- **What moves:** accessor-free `pg_*`, `info_*` and catalog-view test files
  whose census owner is `pgcatalog` (the producers). The prefix estimate is
  0.35–0.6 MiB of the 0.74 MiB of accessor-free catalog tests.
- **Destinations:** `core/enginetests/pgcatalog` and
  `core/enginetests/infoschema`, each under 2 MiB.
- **What stays:** tests owned by the catalog runtime (capture, sources, cache)
  wait for item 218.

**Verify:** the base checks.

## Item 211 — query and DML feature tests

Needs 207. Mode P. Test files only.

- **What moves:** accessor-free core test files whose census owner is `exec`,
  `analyze`, `plan`, `dml`, `table`, `storage`, `visibility`, `lock` or
  `session`. The prefix estimate is 0.3–0.8 MiB.
- **Destinations:** `core/enginetests/<area>` (for example `executor`,
  `binder`, `writes`, `locking`, `sessions`), each under 2 MiB.
- **What stays:** tests owned by core's bound query path, DML assembly,
  transactions or sessions, which follow phase C or item 229.
- **Risks:** synctest files (`advisory_wait`, `relation_deadlock_wait`,
  `session_timeout_pending`, …) move unchanged. Lock and session files run with
  `RACE=1`.

**Verify:** the base checks, plus `RACE=1` for every moved lock, session,
advisory and synctest file.

## Item 212 — explain pilot

Needs 206. Mode P, with bench.

- **What moves** (about 106 KiB) to `core/explain` (L14.5): `explain_command`,
  `explain_expression`, `explain_foreign`, `explain_foreign_query`,
  `explain_json`, `explain_options`, `explain_pruning`, `explain_render`,
  `explain_sources`, `explain_statistics` and `planner_costs`.
- **What stays:**
  - `explain.go` and `explain_analyze.go`: the `Engine` seam;
  - `parallel_presentation.go`: the `exec.Host` `CaptureParallelEvaluation`;
  - `scope.explain` (`*explainExecution`), which holds `[]*explain.Renderer`.
- **`explain.Host`** (about 16 methods):
  - context and operation, the pending catalog, planner methods;
  - partition topology and inheritance read, the detach snapshot, query locks;
  - `access.View` plus restricted relations. The renderer's
    `AuthorizationView` uses only `Catalog`, `State`, `View`, `Roles` and
    `RestrictedRelations`, so it becomes `access.View`.
  - private storage, `CheckRestoreEffect`, `ReferencePath`, foreign access;
  - five provider-callback wrappers that release and re-acquire `Engine.Mu`
    (in `explain_foreign`, `explain_foreign_query`, `explain_statistics` and
    `explain_pruning`). These are core host methods (rule 9).
- **Exported API** (provisional): `NewRenderer`, `Renderer`, `Options`,
  `ValidateOptions`, `Output`, `Summaries`, `DescribeFormat`, `Tree`, `Error`,
  `PlannerSourceRows`.
- **Call sites in core:** `execute.go` ×4, `engine.go` ×2,
  `execute_prepared_bound.go`, `relation_scope.go` and `query_admission.go`.
- **Frame names:** `explain_cancel_test.go` matches `(*explainRenderer).node`.
  It becomes `(*Renderer).node` in the move commit (T5).
- **Tests:** `explain_*` and `planner_*` (19 files, 127 KiB, 50 KiB of them
  accessor-free) and `explain_json_presentation_test.go` (internal). The
  entries of `export_explain_test.go` follow T6.
- **The T7 measurement.**
  - Measure the cascade an `export_test.go` in `core/explain` causes: the test
    build's CPU and wall time with and without it (`go test -json`, interleaved
    runs).
  - Record the result; it settles T7 for every B item. Ask the user if one
    package costs more than +2% of quickcheck CPU.
- **Templates:** write the leaf README template and move the EXPLAIN paragraphs
  of the core README.
- **Risks:** `explain_foreign_fastpath_test.go` has an allocation budget.

**Verify:**
- the base checks;
- `make check RUN='^Test(Explain|Planner)'`;
- `REFERENCE=1 RUN='^TestReference(Explain|PlannerCosts)'`;
- bench and `escdiff.py`, because option validation runs per statement.

## Item 213 — host capability seam

Needs nothing. Mode P, with bench. In place; no code moves between packages.

- **`exec.Host` capabilities.** `exec.Host` (107 methods, consumed only by the
  `exec.Context` forwarders in `exec/context_runtimes.go`, 87 `LiveHost()`
  sites) gains capability accessors for the groups that items 214, 215 and 219
  move:
  - contrib (about 16 methods);
  - text search (about 5);
  - inquiry (by group).

  The grouped methods leave `exec.Host` in the same commit.
- **Nested SQL.** A nested-SQL capability (an SPI analogue) is implemented by
  core (rule 4). It replaces direct use of `consumeTextSearchSQL`,
  `prepareNestedStatement`, `nestedTupleStatement` and
  `nestedReadOnlyViolation` (`fts_query_runtime.go`), which ts_rewrite,
  ts_stat, crosstab, connectby, `xml_map_rows` and the PL/pgSQL query loop use.
  Its row callback is a consumer interface, not a closure, so nothing escapes
  through the interface call.
- **No allocation.** In core, each accessor returns a defined type over the
  scope, such as `(*contribHost)(h)`: a pointer conversion with no allocation.

**Verify:**
- the base checks;
- bench (one extra dynamic call per function call): `make bench`, the core
  benchmarks, the allocation-budget tests and `escdiff.py`;
- `REFERENCE=1 RUN='^TestReference(Amcheck|Tablefunc|Connectby|FTS|Thesaurus|Size)'`.

## Item 214 — contrib runtimes

Needs 207, 212 and 213. Mode P, with bench.

- **What moves** (about 53 KiB) to `core/contrib`:
  - `amcheck_capture`, `amcheck_environment`;
  - `verify_heapam_capture`, `verify_heapam_identity`;
  - `pageinspect_capture`;
  - `pgrowlocks_capture`, `pgrowlocks_identity`;
  - `pgstatindex_capture`;
  - `tablefunc_crosstab_runtime`, `tablefunc_normal_identity`;
  - `connectby_runtime`;
  - `generic_file_lists_runtime`, `generic_file_read_runtime`.
- **What stays:**
  - `amcheck_presentation.go`: `refreshRestrictedPresentation` serves execute,
    routine environment, `setting_apply` and `sql_routine_scope`;
  - `xml_map_rows_*`: it reaches 12 portal and 7 cursor internals. Item 222
    revisits it.
- **Host:** the nested-SQL capability, plus about 20 methods:
  - relation acquire and readmission (`ReplacementMu`, `Mu`, lock tickets);
  - data views and the catalog snapshot;
  - the sequence generator;
  - authorization and notices;
  - TEMP marking.

  `contrib.State` is embedded in `StatementScope`.
- **Frame names:**
  - `connectby_lifecycle_test.go` matches `(*StatementScope).connectbyStatement`.
    It changes in the move commit; `(*Engine).materializePolicySourcesLocked`
    stays.
  - Check `amcheck_cancellation_test.go` and
    `amcheck_scoped_contribution_test.go` with `frames.py`.
- **Tests:** 42 files, 309 KiB (161 KiB accessor-free), placed by T7.

**Verify:**
- the base checks;
- `REFERENCE=1 RUN='^TestReference(Amcheck|Pageinspect|Pgrowlocks|Pgstatindex|Tablefunc|Connectby|GenericFile)'`;
- bench.

## Item 215 — text-search runtime

Needs 207, 212 and 213. Mode P, with bench.

- **What moves** (about 36 KiB) to `core/tsearch` (L14.5; `engine/fts` at L-1
  is too low for catalog, session and nested-SQL needs):
  - `fts_acquisition`, `fts_conversion`, `fts_debug`, `fts_json`,
    `fts_parser_runtime`, `fts_stat`;
  - `thesaurus_runtime`, `dictionary_files`, `dictionary_runtime`,
    `unaccent_runtime`;
  - `fts_query_runtime`, minus the nested-SQL helpers (core, item 213).
- **Exported entry:** the text-search capture used by `trigger_builtin` (the
  tsvector trigger, item 217) and by `session_payload` (thesaurus memos).
- **Host** (about 26 names):
  - routine enter and leave;
  - nested SQL;
  - `AcquireRelation`;
  - the `Engine.Mu` captures;
  - `PayloadOf`.
- **Tests:** `fts_*`, `ts_*`, thesaurus, dictionary, unaccent and ispell (about
  0.39 MiB). Snowball and synonym moved in item 208.

**Verify:**
- the base checks;
- `REFERENCE=1 RUN='^TestReference(FTS|Dictionary|Thesaurus|TextSearch)'`;
- bench.

## Item 216 — statistics runtime

Needs 207 and 212. Mode P, with bench.

- **What moves** (about 45 KiB) to `core/stats`:
  - `attribute_statistics_{arguments,catalog,measured,runtime,update}`;
  - `relation_statistics_{arguments,runtime}`;
  - `statistics_snapshot`, `statistics_snapshot_binding`;
  - `statistic_expression_runtime`, `statistics_expression`;
  - `extended_statistics_runtime`.
- **What stays:**
  - `analyze_metadata.go`, `vacuum_metadata.go`;
  - the two `…Locked` methods that `catalog_overlay_stage` and
    `truncate_storage` call.
- **Host:** about 25 methods.
- **Root callers:** the root's `dump_capture.go` and `restore_ndistinct.go` name
  the snapshot codec directly; they change in the move commit.
- **Frame names:** check `statistics_expression_collection_test.go` and
  `ndistinct_publication_test.go` with `frames.py`.
- **Tests:** 25 files, 206 KiB, all accessor-free.
- **Risks:** planning reads the snapshot on every statement.

**Verify:**
- the base checks;
- `REFERENCE=1 RUN='^TestReference(Statistics|AttributeStatistics|AlterStatistics|Analyze)'`;
- bench.

## Item 217 — native triggers

Needs 215. Mode P, with bench.

- **What moves** (33 KiB) to `core/nativetrig`:
  - `trigger_{autoinc,builtin,builtin_datum,insert_username,lo,moddatetime,refint,tcn}`;
  - `refint_sql`.
- **Entry point:** `nativetrig.Invoke` replaces `invokeNativeTrigger` (two
  callers). The package imports `core/tsearch` for the tsvector trigger (a
  rule-6 arrow).
- **Host** (about 29 names):
  - the routine and trigger environment;
  - nested SQL;
  - sequence and large-object host calls.
- **Tests:** about 53 KiB. `tcn_payload_test.go` and `trigger_lo_test.go`
  move as internal tests.
- **Risks:** this is the per-row trigger path. It must stay allocation-free.

**Verify:**
- the base checks;
- `REFERENCE=1 RUN='^TestReference(Autoinc|InsertUsername|Moddatetime|Refint|Tcn|LoManage)'`;
- bench, including `trigger_review_test.go`.

## Item 218 — catalog runtime

Needs 210 and 212. Mode P, with bench.

- **What moves** (about 74 KiB) to `core/catalogrt`:
  - `catalog_source_{cache,capture,keep,runtime,views}`, `catalog_sources`;
  - `catalog_{inquiry,partition,publication}_runtime`;
  - `info_{columns,parameters}_runtime`;
  - `routine_trees`, `shared_dependencies`.
- **`catalogrt.Runtime`** implements `pgcatalog.Runtime` over a Host.
  - Today `EngineCatalogRuntime` (63 methods) holds `*Engine`,
    `AuthorizationView`, the cursor and prepared registries and the settings
    store.
  - The Host covers the Engine-level captures, the pg_locks and pg_sequences
    captures, settings, and the cursor and prepared visits.
  - The runtime takes `access.View`, which `AuthorizationView` embeds.
- **Engine methods:** about 16 KiB become functions over the Host (`meth2fn`).
- **Capture cache:** the type moves; the field stays on `Engine`, under
  `Engine.mu`. Its self-check recaptures and compares; it never panics under
  `Engine.Mu`.
- **Callers:** 19 files in core; tests call `core.NewCatalogRuntime` 124 times
  and switch to the new constructor.
- **Tests:** the catalog tests left after item 210, about 0.4–0.7 MiB,
  including `shared_dependencies_test.go` (internal).

**Verify:**
- the base checks;
- `REFERENCE=1 RUN='^TestReference(Pg|PG|Info|Catalog)'`;
- bench (catalog capture runs per query of a system view).

## Item 219 — inquiry runtime

Needs 213 and 218. Mode P, with bench.

- **Scope.** The inquiry function host is about 36 files and 76–91 KiB, and it
  is not cohesive:
  - `*_runtime` and `*_inquiry` for session, transaction, catalog, visibility,
    blocking, advisory, ACL, size, partition info and publication info;
  - `largeobject_*`, uuid, random, regex, `object_address`, `input_validity`;
  - `pg_locks_capture`, `pg_sequences`, `index_property`;
  - setting calls and inquiry, `namespace_inquiry`, `conversion_execution`,
    collation.
- **Split by group.**
  - Catalog-object inquiry: `FormatType`, `Size`, `Partition*`, publication,
    comment, deparse, describe and identify, `IndexProperty`, `deparse_*`,
    `routine_inquiry`.
  - Session and transaction inquiry.
- **What moves:** only groups whose Host stays at 30 methods or fewer go to
  `core/inquiry`; the package imports `core/catalogrt` (rule 6). Record the
  rest for item 220.

**Verify:**
- the base checks;
- `REFERENCE=1 RUN='^TestReference(Size|LargeObject)'`, plus the selections of
  the moved groups;
- bench.

## Item 221 — PL/pgSQL above core

Needs 220. Mode S, with bench. Gated: item 220 confirms this sketch.

- **Package:** `core/plpgsql` (L15.5). The `plHost` in `plpgsql_*.go` (19
  files, about 119 KiB) implements the `proc` PL host and uses 65 core methods
  and 27 core top-level names. A host below core would be huge.
- **Hook:** `Engine.PL`, a fixed field set by the root, covering the entry
  points:
  - `Engine.PreparePLRoutineLocked` (`plpgsql_deferred.go`);
  - `StatementScope.invokePLRoutineSpecialized` (`plpgsql_specialize.go`);
  - `StatementScope.invokePLTrigger` (`plpgsql_trigger.go`);
  - `Engine.executeDO` (`plpgsql_inline.go`);
  - the event-trigger run (`newPLRun().EventTrigger`).
- **Exports:** core first exports the names `plHost` uses, as production needs
  (S).
- **Tests:** `plpgsql_*` tests and their accessors move with the package. The
  cascade reaches only the root.
- **Risks:** per-statement and per-row PL paths. Bench with `plpgsql_walk` and
  `plpgsql_notices`.

**Verify:**
- the base checks;
- `REFERENCE=1 RUN='^TestReferencePLpgSQL'`;
- bench.

## Item 222 — cursor, prepare and COPY commands

Needs 220. Mode S, with bench. Gated.

- **Packages:** `core/portalcmds` (DECLARE, FETCH, MOVE, CLOSE),
  `core/preparecmds` (PREPARE, EXECUTE, DEALLOCATE) and `core/copycmds`, reached
  through `Statements`.
- **What stays in core:** the registries, portals and the protocol, which are
  the driver API and session state.
- **`xml_map_rows`:** revisit its move here.
- **Tests:** `sql_cursor`, `prepared_*`, `copy_*` and `driver_batch`, with the
  portal, cursor and prepared accessor files from item 207.
- **Risks:** COPY is per-row.

**Verify:**
- the base checks;
- `REFERENCE=1 RUN='^TestReference(Prepared|Copy|NativeCursor|PGCursors)'`;
- bench.

## Item 223 — DML statement assembly

Needs 220. Mode S, with bench. Gated.

- **What moves:** core's INSERT, UPDATE, DELETE and MERGE assembly (about
  110 KiB) moves into `engine/dml`, behind additions to `dml.Host` that
  `statementHost` implements. CTE mutation stays in the bound query path.
- **Tests:** its tests go to `core/enginetests/dml`, split under 2 MiB. Tests
  that need DML accessors stay in core until item 228.
- **Risks:** per-row paths. Bench with `insert_conflict*` and
  `update_allocation`.

**Verify:**
- the base checks;
- `REFERENCE=1 RUN='^TestReference(Insert|Update|Delete|Merge)Core'`;
- bench.

## Item 224 — event triggers

Needs 220. Mode P, with bench. Gated.

- **What moves:** event triggers go to `core/eventcmds`, reached through an
  `Engine.EventTriggers` hook.
- **What stays:** the after-trigger queue stays in core.

**Verify:**
- the base checks;
- `REFERENCE=1 RUN='^TestReferenceEventTrigger'`;
- bench.

## Item 225 — advisory locks and notifications

Needs 220. Mode P, with bench. Gated.

- **What moves:** `core/advisory` and `core/notify` (L14.5) own their state
  structs and `…Locked` functions.
- **What stays:** the fields stay on `Engine`, under `Engine.mu` (rule 3). The
  commit lock domain is unchanged.

**Verify:**
- the base checks, with `RACE=1` for the advisory and notification tests;
- `REFERENCE=1 RUN='^TestReference(Advisory|NotificationInfo)'`;
- bench.

## Item 226 — settings commands

Needs 220. Mode P, with bench. Gated.

- **What moves:** SET, RESET, SHOW, DISCARD and ALTER SYSTEM go to
  `core/variablecmds`, reached through `Statements`.
- **What stays:** the session lifecycle and TEMP.

**Verify:**
- the base checks, with `RACE=1`;
- `REFERENCE=1 RUN='^TestReference(Settings|AlterSystem|Discard)'`;
- bench.

## Item 227 — foreign-data and replication commands

Needs 220. Mode P. Gated.

- **What moves:** `core/foreigncmds`, `core/publicationcmds` and
  `core/subscriptioncmds`, following the round-7 family pattern, with
  replication slot and origin runtime as `closure` assigns it.

**Verify:**
- the base checks;
- `REFERENCE=1 RUN='^TestReference(FDW|Publication|Subscription|ReplicationMessages)'`.

## Item 228 — hub accessor retirement

Needs 221–227. Mode P. Gated.

- **What changes:** tests that still use one or two hub accessors, about
  1 MiB at `b7e30241f`. The top one is `ScopeActiveSQLRoutineDepth`: 39 files,
  0.42 MiB.
  - An accessor whose target became a production export, or moved with its
    subsystem, is replaced by that name.
  - The freed files then move by census.
- **Rules:**
  - No test is rewritten to a weaker assertion.
  - No private-state assertion that `AGENTS.md` requires is dropped.
  - Deletions happen only under T2, with records.
  - What cannot be freed stays in core.

**Verify:** the base checks.

## Item 229 — core engine test packages

Needs 228. Mode S. Gated.

- **What moves:** the accessor-free tests of code that stays in core (about
  2 MiB). They go to `core/enginetests/<area>` packages by census area, each
  under 2 MiB: for example query, transactions and locking, sessions and
  protocol, and catalog stage.
- **Split further:** any area that is still too big.
- **Merge:** in a freeze window.

**Verify:**
- the base checks;
- the `go test -list` multiset over core and the new packages is unchanged
  against the base;
- quickcheck CPU and wall time before and after, including core's test binary.

## Status

- 206: done (docs commit), census at `3320f21b6`.
  - **Tools** in `.cswd/tmp/PG18-ENGINE-SPLIT-206-TOOLING-CENSUS/` (copies; the
    originals are unchanged). Go tools build with `go build -o ../bin/<tool> .`
    and run from a worktree root.
    - `cmdmv`: `-src internal/engine/core -dest core/<x>` (or
      `core/enginetests/<area> -testonly -doc "Package <area> …"`). It moves
      `core_test` files as `ext` lines, rewrites `core.X` to `x.X`, copies the
      staying helpers a moved file reaches, and moves accessor entries to
      `<dest>/export_test.go` when every core name they reach moves (merging
      into an existing `export_test.go` is not supported yet). It reports
      three classes, and `-w` needs all three empty:
      - `VIOLATION`, for example T3 "needs core accessor X";
      - `SEAM`: `out` is moved production reaching staying core, the Host
        input; `in` is staying core reaching unexported moved code, the
        exports needed;
      - `T6`: accessors that reach both moved and staying names.
    - `closure`/`reach`: `-pkg internal/engine/core`, with the seven core god
      types as the default `-gods`.
    - `inv`: per-test accessor files, helper files and T1 flags.
    - `layers.py`: full import paths and rules 1, 6, 9 and 10.
    - `lits.py`/`revcheck.py`: root plus `core/**`.
    - `frames.py`: `list`, `check` and `edit`.
    - `split.py`: `--src`.
    - copies of `check_links.py`, `check_linear.py` and `escdiff.py`.
  - **Census.** `census/census.sh OUT` builds three coverage binaries (core,
    `functions/enginetests`, `pgcatalog/enginetests`), runs every test alone
    (24 in parallel) and packs the covered blocks. Then `census.py T` writes
    `tests.tsv`, `layout.tsv`, `summary.txt` and `calibration.txt`.
    - Groups are in `groups.json` (`mkgroups.py`). The B groups come from the
      "What moves" lists of items 212 and 214–219, plus
      `pageinspect_heap_capture` and `pgstattuple_capture` (feature inflow,
      for item 214). The C groups come from the §1 prefixes. Lower packages
      sum into areas (`areas.json`).
    - On apu1 it ran 2,849 core tests, 361 functions tests and 163 pgcatalog
      tests, all passing alone. The core run took 54 s; the data is 117,820
      covered blocks. Block keys shift per commit, so later items re-run the
      whole census.
  - **Calibration and weights (user decision).** With the spec's weights,
    only 9% of functions tests and 28% of pgcatalog tests are rule-2 owned by
    their own package. These are the best principled weights, now
    `census.py`'s defaults:
    - blocks covered by more than 5% of tests dropped;
    - IDF over all three suites;
    - `parser`, `sqlstate` and `util` out of the denominator;
    - shares summed per area.

    With them, 100% of functions and 97% of pgcatalog tests leave core (core
    share ≤ 0.3), but own-area agreement is only 73% and 65%. The rest is
    label noise: the json, datetime and string tests mostly exercise
    `sqltype`, and the catalog tests exercise `stored` and ACL code. The user
    chose a plurality rule 2: core share ≤ 0.3 plus the top lower area.
    Calibration gates on leaving core.
  - **Result** (core 11.53 MiB):
    - rule 1: 121 tests; rule 2: 2,144; rule 3: 562; no coverage: 22;
    - 403 rule-2 tests are accessor-blocked (T3).
    - Files: 411 move whole, 121 split cleanly by test function and 567 stay.
      92 of the staying files have no 70% agreement.
    - Phase A moves 3.73 MiB (prefix estimate 2.36). Phase B moves 0.46 MiB
      (estimate 1.2), mostly production, because leaf-area tests classify to
      lower areas. §1 and §4 were rewritten.
    - Every area is under 2 MiB, so no T9 pre-split was needed. The largest
      are binder (628 KiB), executor, types and tables (about 310–320 each),
      pgcatalog (276) and infoschema (249).
  - **Docs.**
    - §3 moves `pgcatalog` to L6.5: since `29bb7a019` it imports `plan` for
      `plan.OwnExpressionListTree`.
    - §3 adds `xml` (L0) and `engine/statistics` (L1).
    - The pgcatalog README's Imports paragraph is corrected (it also imports
      `storage`, `proc` and `plan`).
  - **Verified:**
    - `layers.py`: 0 violations and 0 unknown packages;
    - item 212 dry run: 0 VIOLATION, 95 SEAM (34 out, 61 in) and 2 T6
      (`ExplainObservationForTest`, `ExplainBoundForTest`);
    - adding `explain_foreign_context_test.go` gives the expected two T3
      violations;
    - a real `-testonly -w` run of three files into
      `core/enginetests/{functions,types}` built and passed, then was reset;
    - `check_links.py` and `check_linear.py` pass;
    - quickcheck is not needed (no code change); racecheck and finalcheck are
      skipped (user decision).
  - **Notes for later items:**
    - 207: helpers are needed by two or more destinations in 114 files,
      topped by `concurrent_index_type`, `session`, `sequence_prepared` (11
      each), `transaction` and `prepared_folds` (10); see `summary.txt`. 119
      movable tests reach `testSessionID`.
    - 208–211: areas with no item yet. Proposed: `textsearch` → 208;
      `stored`, `dump`, `routines`, `fdw` and `replication` → 209; `catalog`
      → 210. §4 assumes this.
    - 212: the census sends only `explain_json_presentation_test.go` (internal)
      and `explain_foreign_context_test.go` (accessor-blocked) with the cut.
      Most explain tests go to `core/enginetests/executor`.
    - 212 Host input: the 34 `SEAM out` lines name about 11 core names,
      including `executionContext` ×14, `foreignUnlocked`, `inheritanceRead`,
      `ruleAuthorizationOf`, `authorizeViewSourceLocked`,
      `CheckRestoreEffect`, `ReferencePath`, `AuthorizationView` and
      `explainBound`. The 61 `in` lines come from `explain.go`,
      `explain_analyze.go`, `execute_prepared_bound.go`, `execute.go`,
      `relation_scope.go` and `query_admission.go`.
    - 228/229: 1.35 MiB of staying files are accessor-blocked, and 1.93 MiB
      are mixed-owner files.
- 207: - 207: done, `69025b2bb`, `c17408e51`, `378f1b83f`, `f6c26512b`. -
  **Accessor split** (names and users unchanged, by the production file each
  entry reaches; `c1-split.py`): - `export_statement_test.go` keeps 20 entries
  (statement host, replacement admission, execute, discard, presentation,
  `preparedCommandExecution`, `foreignWrites`, `registerExtension`,
  `authorizationOf`, `BindingHost.view`); `export_cursor_test.go` 52,
  `export_portal_test.go` 38, `export_prepared_test.go` 9. -
  `export_txsession_test.go` is gone: `export_scope_test.go` 53,
  `export_transaction_test.go` 40 (serializable, blocking pids, owner extension,
  publication, isolation snapshots, data views), `export_temporary_test.go` 9,
  `export_notification_test.go` 9, `export_session_test.go` 8 (session host,
  payload, operation validation, settings apply and execute, database admission
  count). - **Session IDs.** `enginetest.NextSessionID()` mints from the
  helpers' counter. Core (21 files) and tablecmds (7 files) lost `testSessionID`
  and its 1<<20 offset; the counter is not rebased (the offset only kept clear
  of the helpers' counter). rolecmds and schemacmds (same offset, named in their
  READMEs), partitioncmds, indexcmds and the root keep their own counters. -
  **enginetest stage 3:** `QueryFixture(t, e, sql)` (enginetest cannot import
  the root, so callers pass `engine.New()`), `PreparedDescription`,
  `PreparedExecute`, `ProtocolReader{Data, Position}`. Core's `sessionStart`,
  `sessionSQL` and `transaction*` copies were replaced by the enginetest
  originals (2,589 calls). Staying in core (core-only users): `sessionValue`,
  `defaultProvider`, `sessionOwner`, `transactionBarrierContext`. -
  **`readme-map.tsv`** (`readme-map.py GROUPS LAYOUT OUT` re-runs it; line
  numbers shift with README edits): 236 blocks of the core README. By bytes:
  core 190 KB; `core/contrib` 6.5, `core/plpgsql` 6.2, `core/catalogrt` 3.4,
  `core/stats` 2.1, `core/inquiry` 0.9, `core/tsearch` 0.7, `core/nativetrig`
  0.6; `core/enginetests/<area>` rows are `## Testing` selectors whose tests
  move. - **Measured:** core 11.61 → 11.64 MiB; enginetest 23 → 28 KiB. Round-7
  single-package exports left as they are (no move needs them). - **Notes for
  later items:** - 208–211: `queryProbe`/`queryProbeExpression`/`evalContext`
  (10 destinations) and `queryCountingContext` (2) are shared and accessor-free
  but were not in this item's list; promote them when a move needs them. -
  208–211: the census predates these renames; re-run it (block keys shift per
  commit).
- 208: done, `7b29bdf50`, `5012f8abc`, `d3d2f660d`, `bc5f842eb`, `ef4ec863f`,
  `aa90090a6`. **Census** re-run locally at `9af072317` (2,864 core tests, all
  passing; files in
  `.cswd/tmp/PG18-ENGINE-SPLIT-208-BUILTIN-FUNCTION-TYPE-EXTENSION-TESTS/census/`).
  Calibration unchanged. Areas: `functions`, `types` (`sqltype`, `regobj`,
  `typedef`, `typecmds`, `xml`), `extension` and `textsearch` (`fts`, as item
  206 proposed). **Split** (`split208.py`, `bin/splitdecl`: movedecl that keeps
  each file's formatting): 38 mixed files give 41 `<base>_<area>_test.go` parts,
  which move under their original basename. A file whose groups are all 208
  areas keeps its largest group and moves whole. Skipped, because they have a
  `pgcatalog`, `infoschema` or `catalog` part (item 210 ran in parallel):
  `error_test.go`, `fts_parser_test.go`, `ts_configuration_test.go`. **Moved**
  (`move.sh AREA`, cmdmv `-testonly`): 119 files, 216 tests; functions 32 files,
  types 55, extension 30, textsearch 2. Sizes: functions 199 KiB, types 345,
  extension 255, textsearch 10. No accessor entry moved (T6: none needed); no
  test deleted (T2). **Helpers (T4).** 26 helpers that two or more destinations
  reach moved to `enginetest` (`promote.py`, `promote.spec`): the prepared-fold
  helpers (`folds.go`), catalog-stage publication and dictionary statements
  (`catalog_stage.go`), probe and expression helpers (`expressions.go`;
  `queryProbeExpression.eval` is now `Probe`). Core's users call them there.
  Exact duplicates were mapped: `newTestExpressionBinder` → `ExpressionBinder`,
  `extensionUnitError` → `State` (failure text only), `nullCountRaw` →
  `FunctionCall`, and in moved files `roleTableRef`, `catalogAuthorization` →
  the enginetest originals. Single-destination helpers are copied (cmdmv);
  `dictionaryUnitPrepare` stays copied in functions and extension because it
  needs `schemacmds`, which `enginetest` does not import. `dropdead.sh` deleted
  the core helpers only moved tests used, and five emptied
  `extension_ownership_*` helper files. **Measured:** core 11.08 → 10.34 MiB (at
  `da9c9b9ad`, after item 210); enginetest 28 → 46 KiB. **Verified:** compile
  check per commit; the four packages (216/216); the files with package globals
  alone; `RACE=1` for the cluster, datetime-sleep and execution-pause tests;
  `revcheck.py` (test names 2,971 unchanged; literals: −1 `extensionUnitError`
  text, +199 from copied helpers); `layers.py` 0; quickcheck. Racecheck and
  finalcheck skipped (user decision). **Notes for later items.** 209 and 211:
  the split files keep their other areas' tests in core under the original name;
  re-run the census on them. Mixed files with a 208-area part left whole: the
  three skipped above. 218 and 229: `namespace_temporary_test.go`'s `capture*`
  producer vars (33) are copied into `types/pg_type_test.go`; promote them if
  catalog-runtime moves need them too. Feature inflow:
  `snowball_turkish_test.go` (after the census) is not classified; it mirrors
  `snowball_romanian_test.go`, which has a catalog part, so it stays.
- 209: not started.
- 210: done, `2586cf192`, `ac9603d71`, `f1bc37edb`. **Census** re-run locally on
  `9af072317` (`.cswd/tmp/PG18-ENGINE-SPLIT-210-CATALOG-PRODUCER-TESTS/`,
  `census/out`, `tests.tsv`, `layout.tsv`, `summary.txt`): 2,864 core, 361
  functions and 163 pgcatalog tests, all passing alone; 118,307 blocks;
  calibration unchanged (100%/73.4%, 96.9%/65.0%). `groups.json` regenerated by
  `mkgroups.py`, with the renamed `maintenance_environment.go` kept in B
  contrib. The whole-file movers are the same 42 files as item 206's census.
  **Moved** (`replay.sh`: movedecl, then `cmdmv -testonly` per destination; 0
  VIOLATION, 0 SEAM, 0 T6): `core/enginetests/pgcatalog` 27 files (72 tests: the
  `pg_*` producers, `data_type_privileges`, `fts_parser_projection`,
  `statistics_capture`, and `pg_subscription` after
  `TestPGSubscriptionColumnAuthority` (owner roles) was split into core's
  `pg_subscription_column_authority_test.go`); `core/enginetests/infoschema` 16
  `info_*` files (45 tests). No accessor, export, frame string or deleted test.
  `pg_operator_test.go`'s path to `docs/design` is re-anchored two directories
  deeper (the one changed literal). **Helpers (T4).** cmdmv copies the staying
  helpers a moved file reaches (56 declarations, about 30 KiB, checked identical
  to their core originals by `copycheck.py`), and core keeps reduced copies of
  moved helpers that staying files use (`data_type_privileges`,
  `pg_attribute_capture`, `pg_namespace`, `pg_publication`, `pg_roles`,
  `pg_user`, `info_view_table_usage`). The role and default-privilege fixtures
  (`fixtureRole`, `cloneRoleFixture`, `putFixtureRole`, `setFixtureEdges`,
  `fixtureEdges`, `newDefaultUnitFixture`, `defaultUnitStatement`; 23 core
  users) and `registeredCatalogProducer` with its `capture*` table are now in
  core and both new packages: candidates for an enginetest promotion.
  **Measured.** Core 11.67 → 11.18 MiB; `pgcatalog` 289 KiB, `infoschema` 256
  KiB. Each new test binary costs about 3 s of link CPU; its tests take about 2
  s CPU alone (load average 200+). Quickcheck at load average about 200: core
  634.7 s, `infoschema` 13.0 s, `pgcatalog` 14.4 s elapsed;
  `TestReferenceDatetimeSleepStored/memory`,
  `TestReferenceUpdateCurrentOfVersionsAndArbitration/peer_recheck/…/memory`
  (known pre-existing race) and
  `TestReferenceRowLockRelationWaitPosition/FOR_SHARE/…/memory/direct` failed
  and passed alone (the last 1/1, the second 3/3). Racecheck and finalcheck
  skipped (user decision); no `REFERENCE=1` (test files only). **Not moved
  (notes for later items).** The `catalog` area (owner
  `internal/engine/catalog`) has only two whole-file movers,
  `statistics_expression_test.go` and `snowball_test.go` (the latter is named by
  item 208); a one-file package is not worth a test binary, so both wait (229).
  Minority pgcatalog/catalog pieces of split files owned elsewhere
  (`catalog_publication_runtime`, `catalog_source_views` (218), `fts_parser`,
  `table_view_summary`, `access_method`, `error`, `pg_init_privs_storage`,
  `subscription`, `ts_configuration`) stay. Mixed-owner `pg_*`/`info_*` files
  with shared helpers stay (`pg_authid`, `pg_description`, `pg_group`,
  `pg_opclass`, `pg_largeobject`, `info_key_column_usage`, `info_sequences`,
  `info_triggers`, …), as do the accessor-blocked ones. 218 can re-run the
  census from this item's scripts.
- 211: not started.
- 212: done (`11b535b5d`, `e87134e1a`, `9fa0e76ca`, `f60d36322`, `27aff0554`),
  at `7731d0abf`. - **Moved** to `internal/engine/core/explain`:
  `explain_command`, `explain_expression`, `explain_foreign`,
  `explain_foreign_query`, `explain_json`, `explain_options`, `explain_pruning`,
  `explain_render`, `explain_sources`, `explain_statistics`, `planner_costs`,
  plus the seam file `explain_host.go`, and the internal test
  `explain_json_presentation_test.go` (census rule 1). `explain.go`,
  `explain_analyze.go`, `parallel_presentation.go` and `scope.explain` stay. The
  move commit is cmdmv output (replay `move.sh`), token-equal modulo qualifiers.
  - **Host.** `explain.Host` has 16 methods. `statementHost` implements it and
  reuses five exec.Host methods (`Context`, `Operation`, `Environments`,
  `AdmittedPlannerMethods`, `ReleaseReplacementRead`); new ones are in
  `core/explain_statement_host.go`: `PendingCatalog`, `CatalogDomain`,
  `PartitionCatalog` (inheritance read, partition topology and detach snapshot
  in one call), `EngineStorageLocked`, `PrivateStorageLocked`,
  `ImplicitStatement`, `CheckRestoreEffect`, `CurrentAccessLocked`,
  `AuthorizeViewSourceLocked`, and `ReleaseEngineLocked`/`ReacquireEngine`. The
  last two bracket the five provider-callback and detached-comparison windows
  (pairs, not closures, so nothing escapes). Names must not collide with
  `StatementScope` fields (`PartitionRelations`, `Input`, `Output`):
  `statementHost` shares them. - **Deviation: Authority, not access.View.** The
  renderer also passes its authorization to `analyze.CheckBinding`, uses it as
  the `view.CheckPolicyAuthority` environment (as the policy user), reads
  `ReferencePath` and recovers rewritten rule authorities. A bare `access.View`
  would change behaviour, so `explain.Authority` = `analyze.Privileges` +
  `access.Environment` + `view.RuleAuthority`, implemented by
  `AuthorizationView`. The renderer caches the `access.View` next to it; rule
  authorities are recovered by one checked assertion (new panic text "rule
  authority %T is not an EXPLAIN authority"). - **Exported:** `Host`,
  `Authority`, `Renderer` (`Observation`, `Command`, `ForeignFence`, `Render`,
  `SetForeignParameters`, `CommandTree`, `MarkRuleRewrite`, `PresentedNodes`),
  `NewRenderer`, `Options` (six fields), `ValidateOptions`, `DescribeFormat`,
  `Error`, `Tree` (`Actual`), `Summaries`, `Output` (takes a context), `Bound`
  (from `explain.go`), `PlannerSourceRows`. Core keeps `newExplainRenderer` as
  the wrapper over `NewRenderer`. - **Tests (T5, T6).** `explain_cancel_test.go`
  matches `.(*Renderer).node` (renamed in the export commit). No test moved
  besides the internal one and none was deleted; the other explain and planner
  tests stay for phase A and C owners (`explain_foreign_context_test.go` is
  accessor-blocked). `ExplainBoundForTest` stays in core over the exported API.
  `ExplainObservationForTest` stays in core and calls `Renderer.PresentedNodes`,
  the one test-only export (user decision, T6 exception), because
  `TestExplainAnalyzeForeignExecutionIdentity` also needs the core-only
  `ExplainActiveForTest`. `ExplainActiveForTest` stays. - **T7 measurement**
  (`t7.sh`, 4 interleaved rounds, load 100–130 on 32 cores): an external engine
  test in `core/explain` next to its internal test makes go test rebuild 15
  packages (core, the seven `*cmds` families, the root, enginetest, …) as
  `explain.test` variants. Per edit of explain or below, the build of
  `core/explain` with `exec/enginetests` took 37–41 s CPU and 16–26 s wall,
  against 5.5–6.7 s CPU and 3–12 s wall with the same test in
  `core/explain/enginetests`. That is about +34 s CPU, under 1% of quickcheck
  CPU, so no user decision. T7 stands: leaves without an `export_test.go` keep
  pure units in `core/<x>` and engine tests in `core/<x>/enginetests`. -
  **Measured.** Core 11.67 → 11.57 MiB; `core/explain` 114 KiB with the README.
  Interleaved binaries (`bench.txt`): `BenchmarkOpenDatabase` 3,322–3,330 vs
  3,321–3,329 allocs/op. A temporary EXPLAIN probe (`probe.txt`) shows identical
  allocs/op for SELECT 197, EXPLAIN 289, VERBOSE JSON over a view 722–723,
  forced RLS 211 and ANALYZE 291. `escdiff`: staying core files are unchanged;
  the renderer's authority boxing replaces the per-`CheckBinding` boxing. -
  **Docs.** `core/explain/README.md` is the leaf README template (Layering,
  Contract, Host and authority, Locks, Behaviour, Testing). The core README's
  ordinary-EXPLAIN, TEXT/JSON and ANALYZE presentation paragraphs moved there;
  core keeps the ANALYZE execution paragraph and a pointer. - **Notes for later
  items.** 214–219: implement leaf Hosts on `statementHost`, check new method
  names against `StatementScope` fields and existing methods, and prefer
  release/reacquire pairs over callbacks. cmdmv `-force` is needed when only T6
  reports remain; T6 counts exported moved names too. revcheck's base must be
  the rebased master, not the start tip. - **Verified:** quickcheck (core 520 s
  wall, `core/explain` 0.08 s, load 100–270 on 32 cores;
  `TestReferenceTypedTableAttachmentSourceAdmission` and
  `TestReferenceTransactionalPrivilegesRoutineKinds` hit their step deadlines
  under that load and passed 3/3 alone); `REFERENCE=1
  RUN='^TestReference(Explain|PlannerCosts)'`; `layers.py` 0; `frames.py check`;
  `revcheck.py` (tests unchanged; literals: the frame rename and the new
  rule-authority panic text); deps unchanged. Racecheck and finalcheck skipped
  (user decision).
- 213: - 213: done (`66e8f81a5`, `b861d4501`), in place at `8850a34d2`. -
  **Capability accessors.** `exec.Host` (109 methods) gains `Contrib()`,
  `TextSearch()`, `CatalogInquiry()`, `SessionInquiry()` and
  `TransactionInquiry()` (`exec/host_capabilities.go`); 57 methods leave Host
  (now 57). Contrib has 18 methods (pgstattuple included, feature inflow), text
  search 8: the five Host methods plus `DebugTextSearch`, `StatTextSearch` and
  `ResolveFTSParser`, which the exec forwarders used to reach by type assertion
  on the host. Inquiry is grouped like the `functions` runtime interfaces:
  catalog 20 (with `VisibilityInquiry`), session 6, transaction 8. - **Core.**
  Each group is a defined type over `StatementScope` (`contribHost`,
  `textSearchHost`, `catalogInquiryHost`, `sessionInquiryHost`,
  `transactionInquiryHost`; `host_capabilities.go`); the accessors are pointer
  conversions (no allocation). 69 receivers moved, including helpers only one
  group calls. Helpers shared with other code stay on `statementHost` and are
  reached through `host()`: `catalogInquiryCatalog` (catalog inquiry and
  `GenericFileRead`), `Context`, `Environments`, `acquireDictionary(For)` and
  `tsAcquisition.accept`/`dictionary` (shared with the tsvector trigger). -
  **Nested SQL.** `exec.NestedSQL` (`exec/nested_sql.go`) is implemented by
  `statementHost` (`core/nested_sql.go`): `EnterNestedSQL`/`LeaveNestedSQL`,
  `ParseNestedSQL`, `PrepareNestedSQL`, `ExecuteNestedSQL` with a
  `NestedRequest` value (diagnostic text, caller label, read-only SPI, borrowed
  source roots, close-on-failure) and a `NestedConsumer` interface (before and
  after analysis, rows), plus classification (`NestedCursorStatement`,
  `NestedReadOnlyViolation`, `NestedCommandTag`). ts_rewrite and ts_stat
  (`textSearchSQL`), crosstab (`crosstabCommand`) and connectby
  (`connectbyCommand`) use only it. Each consumer keeps its own SPI policy, so
  diagnostics, error order and closing stay byte-identical. `xml_map_rows` and
  the PL/pgSQL query loop stay in core (items 222, 221) and still call
  `nestedTupleStatement`/`nestedReadOnlyViolation`, now in `nested_sql.go`. -
  **Tests (T5, T6).** 81 core test call sites go through the accessors.
  `connectby_lifecycle_test.go` frames: `(*contribHost).Connectby`,
  `(*statementHost).ExecuteNestedSQL`, and `ParseWithOptions` called from
  `(*statementHost).ParseNestedSQL` inside `(*contribHost).connectbyQuery`. The
  accessor `ActivateConnectbySource` became `ActivateNestedSource` and moved to
  `export_routine_test.go`. No test was deleted. - **Measured.** Core 11.64 MiB
  (+5.5 KiB), exec +6.1 KiB; no new package. Interleaved before/after binaries
  (`bench.txt`): `BenchmarkOpenDatabase` 3,324–3,454 allocs/op on both; inquiry
  and `ts_lexize` queries unchanged (622, 559 allocs/op); ts_rewrite, ts_stat,
  crosstab and connectby +1 alloc/op each (655 → 656, 698 → 699, 897 → 898,
  3,388 → 3,389). `escdiff.py`: the only new heap objects are those four
  consumers. A pointer passed to a dynamic interface call always escapes in Go,
  so a consumer interface costs one object per call; connectby's is hoisted to
  one per call, not per traversal node. Quickcheck: core 480.9 s wall under
  heavy load. - **Verified:** quickcheck
  (`TestReferenceOrderedSetAggregateActiveCancellation/direct/memory` and
  `TestReferenceDatetimeSleepStored/memory` failed under load and passed 3/3
  alone); `REFERENCE=1
  RUN='^TestReference(Amcheck|Tablefunc|Connectby|FTS|Thesaurus|Size)'`;
  `layers.py` 0; `frames.py check` 0 broken; `lits.py` (only the deduplicated
  parse settings and the templated parameter-frame label); deps unchanged.
  Racecheck and finalcheck skipped (user decision). - **Notes for later items:**
  - 214: `contrib.Host` embeds `exec.NestedSQL`; `crosstabCommand` and
  `connectbyCommand` move as they are. The frame strings in the 214 section are
  stale: `(*StatementScope).connectbyStatement` is gone (see above). crosstab's
  type-name display needs `RoutineAuthorizationLocked` and
  `CaptureTypeDiagnosticName` through the Host. - 215: `textSearchSQL` and the
  consumers read `Op.Context()` at consume time; keep that (it is not
  `Host.Context()`). - 219: the inquiry groups follow
  `functions.{Catalog,Session,Transaction}InquiryRuntime`. - Finding:
  ts_rewrite, ts_stat and crosstab do not close a failed execution's rows; only
  connectby does (`CloseFailed`). Kept for byte-identical behaviour.
- 214: not started.
- 215: not started.
- 216: not started.
- 217: not started.
- 218: not started.
- 219: not started.
- 221: not started.
- 222: not started.
- 223: not started.
- 224: not started.
- 225: not started.
- 226: not started.
- 227: not started.
- 228: not started.
- 229: not started.
