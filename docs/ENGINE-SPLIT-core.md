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

- 206: not started.
- 207: not started.
- 208: not started.
- 209: not started.
- 210: not started.
- 211: not started.
- 212: not started.
- 213: not started.
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
