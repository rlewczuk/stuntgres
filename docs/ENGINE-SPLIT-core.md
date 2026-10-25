# Engine split: `internal/engine/core`, seventh round

Session prompts for linear items 182, 188–192, 195 and 204 of the
[status page](ENGINE-SPLIT-STATUS.md). The rules are in
[ENGINE-SPLIT.md](ENGINE-SPLIT.md): §2, §3 (layer L15, rules 1–4 and 8) and §5.

Core is what the runtime needs without the family executors. `closure` computes
it: the closure of the exported root API and the interface methods of reached
types, with the dispatch edges out of `Engine.executeStatement` cut. At
`26f8fea15` it was 28.5k declaration lines (`assign.tsv`, `summary.txt` in
`.cswd/tmp/PG18-ENGINE-SPLIT-CLOSING-REVIEW-4/r2/`). Re-run `closure` at the
start of every item.

## Item 182 — export pass in place

Needs 181. Mode S.

- **What to export.** Export, in the root and under their final names:
  - the unexported core members that family, shared and root code use:
    `exportset.tsv`, 345 names at `26f8fea15` (179 declarations and 166 struct
    fields);
  - the 25 test-inspection names that `waves.py` lists, such as `engineOf`,
    `Engine.publishCatalog`, `engineCatalogRuntime`, `captureCatalogSourceRows`,
    `StatementScope.acquireRelation` and `replacementAdmission.TryLock`.
- **Names.** Capitalize. Build the clash table with `expspec` and record it in
  the Status line. A field whose exported name clashes with a method or a
  promoted name takes a descriptive name: `roles` clashes with
  `Engine.Roles()`, so it becomes `RoleStore`.
- **Tools.** `expspec` plus `movex` member mode, and `fieldrn` for fields. The
  rewrites cover `./internal/engine/...`, the root `stuntgres` package and
  `cmd/stuntgres`. Generate the commit from a replay script.
- **`enginetest` stage 2.** Add the shared inspection helpers that tests in two
  or more wave destinations use, for example role operations and table refs,
  session SQL, and lock and catalog inspection. Base them on the newly
  exported names, or on SQL where that observes the same thing (`pg_locks`).
- **Frame names.** Grep the 14 `runtime.Callers` test files for frames this item
  renames, and update those strings in the same commit.

**Verify:**
- build, and `go test -run '^$' ./internal/engine/...`;
- the string-literal multiset is unchanged except for frame strings;
- `make quickcheck`;
- bench skipped (renames only), and `REFERENCE=1` skipped (no behaviour change);
- merge in a freeze window.

## Item 188 — composition seams

Needs 182. Mode P, with bench.

1. **Dispatch.** Declare a `Statements` interface. It lives in the root for
   now, and moves to core in item 195. It has two methods:
   - unlocked family dispatch: the type switch at `execute.go:32–197`, minus
     the cases `closure` assigns to core (LISTEN/NOTIFY, PREPARE and
     DEALLOCATE, FETCH and CLOSE, DO, DISCARD, SET, SHOW and RESET, setting
     calls, TRUNCATE);
   - locked family dispatch: the `…Locked` handlers at `execute.go:237–262`,
     with ALTER TABLE, ALTER TABLE DEFAULT, DROP TABLE and CREATE TABLE. It is
     a lock-domain continuation: it is entered with `Engine.mu` held and
     releases it on every return.

   A root type implements both in today's case order, and a field on `Engine`
   holds it. Keep the fall-through code (the DeclareCursor checks,
   `scope.completed`) where it is.
2. **Factory hook.** `database_create.go:57` builds the `template1` seed with
   `newEngine`. Give `Engine` a factory field set by the composition.
3. **Reset, Dump and Restore** (`control.go`) become root functions
   (`engine.Reset(ctx, e)`, …). The driver's call sites change in the same
   commit. `principal_remap.go:74` (`restoreStage`) uses the composition
   constructor.
4. **Split `newEngine`.**
   - State seeding moves into one function that becomes `core.NewEngine`: the
     stores, settings, visibility, locks, roles and the builtin providers,
     including `relationProvider`.
   - Registering the statements, validators and factory is composition.

**Verify:**
- `make bench`: `BenchmarkOpenDatabase` and a per-statement benchmark, with no
  new allocation;
- quickcheck;
- `REFERENCE=1` selection: `^TestReferenceControl` (Reset, Dump and Restore
  witnesses) and `^TestReferenceDatabaseCreateControls$`.

## Item 189 — core hooks

Needs 188. Mode P, with bench.

Take each "family code pulled into core" entry from `closure`'s `summary.txt`
and decide it by R4:
- **Hook it** if it reaches at least 150 lines that only one family needs, and it
  is not on a per-row path;
- **keep it** in core otherwise.

At `26f8fea15`:
- **Hook.** `Engine.ValidateCatalogStage` reaches about 1.1k lines of family
  validators: schema definitions 413, types 321, sequences 91, views 76,
  inheritance 64, tables 40, indexes 36, partitions 24. They become an ordered
  validator table (rule 8) in today's order. It is entered with `Engine.mu`
  held.
- **Decide.**
  - `Engine.publishCatalogOperation` (132, schema objects);
  - `catalogDependencyPreflight` (115);
  - `dropProvider.removeObjectLocked` (87);
  - `Engine.catalogPreflightLocked` (80).
- **Keep in core:**
  - relation admission: `acquireRelation`, `acquireRelationTarget`, about 0.9k;
  - the runtime hosts: `statementHost`, about 1.2k;
  - the TEMP lifecycle: `preserveTemporaryLocked`, `prepareTemporaryCommit`,
    `sessionHost.EndMaintenance`;
  - the bound path and query-source capture;
  - COPY, protocol and rule programs.

**Verify:**
- re-run `closure` and record the new core size;
- publication bench, with no new allocation;
- quickcheck;
- a `REFERENCE=1` selection of catalog-validation failures (`tables`, `types`).

## Item 190 — host and slot splits

Needs 189. Mode P.

- **`ddlHost`.** It has 89 methods in 12 files and implements nine interfaces:
  `ddl.Host`, `ddl.SchemaHost`, `index.Host`, `index.KeyHost`,
  `partition.Host`, `fdw.Host`, `table.Host`, `typedef.StageHost` and
  `routine.DDLHost`.
  - Keep a core base with the methods that core paths use; `closure -nodyn
    ddlHost` shows them.
  - Add family host types with the same `{e, op, stage}` layout for the
    family-only interfaces, built by family code.
  - Measured effect: core shrinks by 1.2k lines.
- **Family-only adapters.** `extensionHost` (17 methods) and `alterHost` (10)
  stay root and move with their families. `plHost` is core.
- **Feature slots.** Each slot used by only one family becomes an opaque slot
  that core declares and the family owns (rule 7); runtime slots stay core:
  - `StatementScope`: `sqlRoutine`, `inheritance`, `protocolSlot`,
    `presentationSlot`, `dropsSlot`, `foreignTablesSlot`, `ruleAction`;
  - `CatalogStage`: `extensionNamespace`, `schemaCascadeNames`,
    `dropCascadeNames`, `domainBases`, `domainAlter`, and the database fields.

**Verify:**
- `closure`: no family-only host method is left in core;
- quickcheck;
- bench, if a slot is on a per-statement path.

## Item 191 — family methods to functions I

Needs 183–187 and 190. Mode P.

- **Methods to functions.** Turn the god-struct methods (`Engine`,
  `StatementScope`, `CatalogStage`) that `closure` assigns to these families
  into receiver-first root functions with `meth2fn` (pointer receivers):
  - tables, indexes, inheritance, partitions, sequences, identity and VACUUM,
    plus the code they share with tables;
  - about 126 methods at `26f8fea15`.

  Methods that implement an interface stay methods of their host type.
- **Split mixed files.** Split every root file that mixes core and family
  declarations (`movedecl`), so that each file is wholly core, wholly root or
  wholly one family.
- **Watch.** `meth2fn` misses pointer-receiver call sites nested in other
  rewritten calls; fix those by hand.

**Verify:** build, tests and quickcheck; bench if a converted function is on a
per-row path.

## Item 192 — family methods to functions II

Needs 191. Mode P.

The same as item 191 for the other families, about 100 methods: types, views,
rules, policies, triggers, roles, schema objects, routines, operators, casts,
extensions and FDW.

**Gate.** `closure` reports:
- no family declaration that is a god-struct method;
- every root file is wholly core, root or one family;
- the only core→family edges are dispatch.

## Item 195 — core move

Needs 194. Mode S, with bench.

- **Production.** Move every declaration that `closure` assigns to core into
  `internal/engine/core` (package `core`), about 26k lines, together with
  `Statements`, the hooks and the factory. Move the root `inspect.go` to
  `core/inspect.go`.
- **What the root keeps:**
  - the composition: `New*`, the dispatch implementation, the factory, Reset,
    Dump and Restore, and the Restore hooks;
  - the family functions, now over `*core` types;
  - the family host types;
  - their tests.
- **Tests.**
  - Core-only units, which build no engine, move as internal tests.
  - `inspect_*_test.go` becomes `core/export_test.go`, and its users move to
    `core/` as `core_test`.
  - The wave tests already in `core/` change `engine.X` to `core.X` for moved
    names.
- **Driver.** The root `stuntgres` package and `cmd/stuntgres` import `core`
  for types and runtime methods (rule 2).
- **Docs.** Split the root README: most sections become the core README, and
  the root keeps the composition contract.
- **Tools.** `movespec` and `movex -qual core`, `git mv`, `impfix`, all driven by
  a replay script and merged in a freeze window.

**Verify:**
- build and `go test -run '^$' ./...`;
- quickcheck;
- `make bench`: `BenchmarkOpenDatabase` and the per-statement and per-row
  benchmarks;
- the §3 `go list` import check and `make deps`;
- a `REFERENCE=1` selection across `sessions`, `dml` and `tables`.

## Item 204 — composition cleanup

Needs 203. Mode S.

- Root tests that are left: keep the composition-level ones (`New*`, dispatch
  order, Restore hooks). Move or delete the others under T2 and T3.
- Remove the `core/inspect.go` accessors that no test uses any more, and
  unexport names that only tests use (`closure`).
- Check the §3 import rules with `go list`, then finish the root and core
  READMEs. Measure the root bytes.

**Verify:** quickcheck, a `REFERENCE=1` smoke selection, bench (startup).

## Status

- 182: done (`d8fd275b0`, `1d44144ab`, `c97bb4911`). Generated by `replay.sh` in
  `.cswd/tmp/PG18-ENGINE-SPLIT-182-EXPORT-PASS-IN-PLACE/` (closure → keys →
  `exportpass` → frame string → gofmt; stage-2 patch; README names).
  `exportpass` replaces expspec, movex and fieldrn in one type-checked pass: it
  resolves closure labels in the root test variant, renames embedded fields with
  their types, and checks package scope (test files included), every named type
  that reaches a member in every package that sees the root, ambiguity, local
  shadowing, dynamic-interface method names and fmt conversions of structs with
  renamed fields. Names are capitalized with initialisms (`ddlHost` → `DDLHost`,
  `sqlError` → `SQLError`); `spec.tsv` there maps every name. Clash table
  (descriptive names): `CatalogStage.roleCandidate` → `RoleCandidateRecord`
  (method `RoleCandidate`), `ddlHost.stage` → `CatalogStage` (method `Stage`),
  `dropProvider.path` → `SearchPath` (method `Path`), `Engine.publishCatalog` →
  `PublishPreparedCatalog` (test-only `Engine.PublishCatalog`),
  `Engine.replaceCatalog` → `ReplaceCatalogInOperation` (`ReplaceCatalog`),
  `Engine.roles` → `RoleStore` (`Roles`), `StatementScope.command` → `CommandID`
  (promoted `statementMutation.Command`, `statementHost.Command`),
  `StatementScope.queryLocks` → `QueryLockContext` and `StatementScope.ticket` →
  `LockTicket` (`statementHost` methods). Kept unexported (user decision):
  `StatementScope.acquireRelationTarget` and
  `StatementScope.acquireBoundViewRelationModes`, whose frames read-only
  `reference/` fixtures match (`fixture.NativeRelationLockWait`, the
  `session_state` batch-refresh and `views` concurrent-refresh tests); the item
  that moves their family callers settles them, without editing `reference/`.
  `replacementAdmission.TryLock`/`Unlock` were already exported. Frame string
  changed: `(*StatementScope).acquireSequence` in
  `ownership_manual_utility_fence_test.go`. `enginetest` stage 2: `RoleError`,
  `RoleErrorPosition`, `StartSession`, `SessionSQL`, `TransactionOperation`,
  `TransactionBegin`, `TransactionEnd`, `TransactionRows`, `LockEmpty`,
  `LockPending`, `LockAsync`, `LockAwait`, `CatalogAuthorization`, `CatalogRef`,
  `RoleTableRef`, `PublishCatalog`, `ValidateObjectRef`, `ExpressionBinder`; the
  root originals stay for staying tests (waves replace `e.PublishCatalog(p, t)`
  with `enginetest.PublishCatalog(e, p, t)`). Not added, single-area or larger
  fixtures for the waves to weigh: `Engine.SetObjectACL` (143 files, with
  `withObjectACL`), `queryProbe`, `domainUnitPublish`, `cloneRoleFixture`. Notes
  for later items: the sections below and §1 keep the pre-export spellings
  measured at `26f8fea15`; Go comments still say `Engine.mu`/`e.mu` (READMEs say
  `Engine.Mu`).
- 188: done (`368580d20`, `cbb5001f8`, `6110e58bf`, `212dce0b8`). `Statements`
  (`statements.go`): `Execute(e, scope, stmt, count) (Result, handled bool,
  error)` with nothing held and `ExecuteLocked(e, scope, auth, stmt, count)`,
  entered with `Engine.mu` held and releasing it on every handled return
  (unhandled keeps it held and runs nothing). `familyStatements{}`
  (`statements_dispatch.go`, with `dispatched`) implements both; the locked
  if-chain and switch became one type switch in the same order.
  `executeStatement` keeps the core cases, the DECLARE CURSOR checks and the
  bound path. `Engine.Statements` and `Engine.Factory` (`EngineFactory`,
  implemented by `newSettingsEngine`) are set by `newEngine` and copied by
  `newDatabaseRuntime`. `NewEngine` (future `core.NewEngine`) keeps the stock
  extension registry, because it closes when the seed publishes. `Reset(ctx,
  e)`, `Dump(ctx, e, dst)`, `Restore(ctx, e, src)`; call sites rewritten by the
  type-checked `ctlrw` tool in
  `.cswd/tmp/PG18-ENGINE-SPLIT-188-COMPOSITION-SEAMS/`. Notes for later items:
  run `closure` with `-roots
  'New,NewWithSettings,NewWithCatalogProviders,Reset,Dump,Restore,newEngine,newSettingsEngine,familyStatements,familyStatements.Execute,familyStatements.ExecuteLocked,dispatched'
  -dispatch 'familyStatements.Execute,familyStatements.ExecuteLocked'` (outputs
  in `closure-after/` there). With `executeStatement` no longer a cut dispatch
  decl, its bound-path edges count as core: `executeRuleProgramLocked` (160,
  view), `sequenceAnalysisValidLocked` and `regtypeAnalysisValidLocked`. Item
  189 should keep rule programs in core as planned. `NewEngine` pulls
  `registerExtension`, `initializeDatabase`, `installBuiltinDomainChecks` and
  the catalog-source descriptors into core.
- 189: done (`5103aae7e`); no hook added (user decision); the only code change
  raises the reference scenario deadline in `reference/fixture/fixture.go` from
  30 s to 60 s (user decision, machine load). Measured at `f1d75738c` with a
  `closure` copy in `.cswd/tmp/PG18-ENGINE-SPLIT-189-CORE-HOOKS/`
  (`closure-src/`, `bin/closure`, wrapper `closure.sh OUT EXTRA_DISPATCH
  [flags]` with item 188's `-roots`/`-dispatch`). The copy adds two flags.
  `-famseed=false` stops seeding exported family-file declarations: after item
  182 the stock run credits them to `<root export>` and `Engine (engine.go)`.
  `-fammeth=false` drops type → exported family-file method edges that are not
  interface implementations, modelling items 191–192. The model (core 28,932)
  reproduces this section's attribution, with `ValidateCatalogStage` reaching
  1.0k family lines. The R4 decisions used exclusive reach: the core lines that
  leave when a candidate is added to `-dispatch` (`exclusive.txt`, all 64
  first-referrer entries). `ValidateCatalogStage` frees 3, and only 3–6 even
  with `-nodyn DDLHost,DropProvider` and the TEMP cuts.
  `EvolveStageIndexesLocked` stays core through `prepareTemporaryCommit` and
  `installTemporarySessionStageLocked`. `DropProvider` (`ExpressionDependencies`
  344 lines) stays core through `CatalogStage.RemoveObjectLocked` and
  `sessionHost.EndMaintenance` → `endTemporaryCollations` →
  `StageExpressionDependents`. `ReownStagedTypeGenerationsLocked` stays core
  through `prepareQueryTableLocked` on the bound path. The other candidates are
  kept: `PublishCatalogOperation` 135 (definition dependency lock acquisition,
  ddlcommon 126), `DropProvider.removeObjectLocked` 87 (spread over four
  families), and `catalogDependencyPreflight` and `CatalogPreflightLocked` 0.
  Only `AcquireRelation` (table 201), `capturePreparedQuerySourcesLocked` (view
  309), `preserveTemporaryLocked` (table 164) and `executeStatement` (view 160,
  rule programs) free 150+ lines for one family, and all four are on the keep
  list. Notes for later items: run `closure` with `-famseed=false
  -fammeth=false` for core sizes until item 192; item 192's gate is now
  "dispatch only".
- 190: done (`65b0d3679`, `f5300085c`, `248f347c6`). Measured with a `closure`
  copy in `.cswd/tmp/PG18-ENGINE-SPLIT-190-HOST-SLOT-SPLITS/` (`bin/closure`,
  wrapper `closure.sh OUT EXTRA_DISPATCH [flags]` with item 188's
  `-roots`/`-dispatch` and item 189's `-famseed=false -fammeth=false`). The copy
  adds `fielduse.tsv` (every `StatementScope`/`CatalogStage` field with each
  user and its assignment), and its `areas.json` maps `schema_host.go` to
  `ddlcommon`: the file now holds only `SchemaHost`, and as a `statement` file
  the exported type was seeded as core. Core went from 28,932 to 28,009 lines
  (the `-nodyn DDLHost` preview said 27,877; it also dropped the `ddl.Host`
  methods, which core calls through `BindDomainNotNull`, `index.KeyHost` and
  `ddl.Allocator`). Core base `DDLHost`: `ddl.Host`, `LookupCollation`,
  `NamedType`, `OtherDatabaseDependencies`, and `AddDomainBase`, `RemoveObject`,
  `RelationDiagnostic`, `ResolveRelation` and `SelectFunction` (shared by
  `RoleHost`, `SchemaHost`, `RoutineHost` and the table chain; `closure` assigns
  these five to families). Family hosts: `TableHost` (`index.Host`,
  `table.Host`, `partition.Host`, `fdw.Host`, plus the type staging it shares
  with types and views), `TypeHost{TableHost}` (`typedef.StageHost`),
  `ViewHost{TableHost}` (`view.*Host`), `RoutineHost` (`routine.DDLHost`,
  `index.FamilyDDLHost`), `RoleHost` (`role.*Host`), `SchemaHost`
  (`ddl.SchemaHost`); `alterHost` embeds `TableHost`. Call sites were wrapped by
  the compiler-driven `wrapfix.py`/`wrapfix2.py` there; external tests use keyed
  literals (`engine.TypeHost{TableHost: engine.TableHost{DDLHost: ...}}`). Slots
  by `fielduse.tsv`: only `DropsSlot` (table) and `ExtensionNamespace`
  (extension) have one family's users; `sqlRoutine`, `protocolSlot`,
  `presentationSlot`, `ruleAction` and `foreignTablesSlot` are core-only,
  `Inheritance` is read by `Finish` and query admission, `SchemaCascadeNames` is
  shared by table, view, schema and type DDL, and `DropCascadeNames`,
  `DomainBases`, `domainAlter` and the database fields have core users. Notes
  for later items: items 196–203 move the family hosts; `TypeHost` (used by
  `typecmds` and `schemacmds`) and `TableHost` belong to `tablecmds` by R3.
  `tablecmds` `TestPrimaryKey*` fail when run alone ("duplicate advisory
  session"), also on the base; they pass in the package run.
- 191: - 191: done (`d95343602`, `8972334e1`, `ad0dbb757`, `991fe6a68`).
  Generated by `c1.sh`–`c4.sh` in
  `.cswd/tmp/PG18-ENGINE-SPLIT-191-FAMILY-METHODS-FUNCTIONS-I/` with a patched
  `meth2fn` there (`bin/meth2fn`: call sites keyed by their parenthesis and
  receiver text rendered with nested rewrites, so chained receiver calls convert
  in one pass) and `movedecl` (`bin/movedecl`: `_:Type` keys move `var _ T = v`
  assertions); `ifacecheck` there checks every candidate against every interface
  in `./...` (none implemented one); `impstyle.py` keeps each source file's
  import grouping. Candidates from `closure` (item 190 copy, `-famseed=false
  -fammeth=false`, `areas.json` here maps the new files): `cands.txt`, 136
  god-struct methods labelled table, index, partition, inheritance, sequence or
  vacuum, or a shared set with table but not roles. Renamed for clashes:
  `CatalogStage.table` → `tableStage`, `StatementScope.drops`/`dropsOrZero` →
  `scopeDrops`/`scopeDropsOrZero`. Also converted:
  `DropProvider.projectPreparedColumn`, `AuthorizationView.definitionNamespace`,
  `InheritanceAlterState.retiredBase` and the test-only
  `StatementScope.prepareColumnDrop`. Two method values became a function
  literal (`FinishInheritance`) and a function returning the callback
  (`lockPartitionAttachChild(scope)`, inlined, no escape). External tests call
  `engine.SchemaTableLocked(e, ...)`. Kept a method:
  `Engine.admitInheritanceAlter`, whose frame
  `"(*Engine).admitInheritanceAlter("` the read-only `reference/partitions`
  cancellation test matches; items 196/199 must keep that frame (for example a
  family-local `type Engine core.Engine` with the method). Split files:
  `mix.py`/`mixdecls.py` over `assign.tsv`, grouping destinations as tablecmds
  (table, sequence, vacuum, shared with table), indexcmds, partitioncmds
  (partition, inheritance). Notes for item 192: `ddl_host.go` still mixes (the
  core base methods `closure` labels family, item 190 decision);
  `catalog_name_locks.go`, `typed_table.go` and `alter_type_prepare.go`
  (`Engine.prepareAlterTypeLocked`, fdw+typedef) hold other families'
  declarations.
- 192: not started.
- 195: not started.
- 204: not started.
