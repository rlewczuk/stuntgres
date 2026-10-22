# Engine split, step 30: `internal/engine/control`

Session prompt. Start a fresh session with "Implement
`docs/ENGINE-SPLIT-control.md`." Follow the shared rules in
[ENGINE-SPLIT.md §8](ENGINE-SPLIT.md#8-implementation-sessions): protocol,
constraints, verification, documentation and report.

- **Plan:** [ENGINE-SPLIT.md](ENGINE-SPLIT.md) §6 step 30; §3 (layer table,
  rule 8); §4.1 patterns 4 (refine first) and 5 (registration); §7
  decision 15. Read [`ENGINE-SPLIT-dump.md`](ENGINE-SPLIT-dump.md) first:
  step 18 owns the Dump half of the registration work.
- **Needs:** 30a: 18c (the Dump tables, so both halves of `control.go`
  follow one shape). 30b: 30a, 22d (`stored` without aliases), 21b
  (`regobj`), and the Part A moves of `index` (23b), `table` (24b),
  `partition` (25b), `fdw` (26b) and `view` (27b), so the feature dump and
  Restore code `control` calls lives in packages; also 14e and 15e (the
  routine and type dump statements), for the same reason. 30c: 30b.
- **Linear items:** 30a = item 111 (needs 57), 30b = item 112 (needs 111,
  70, 83, 87, 91, 95, 100, 65, 80, 81), 30c = item 113 (needs 112).
  Fourth round (item 133): 30g = 148.
- **Effort:** high for 30a; xhigh for 30b (docs only) and 30c (broad
  driver move).
- **Session size:** one sub-step per session.
- **Tools:** `reach`
  (`.cswd/tmp/PG18-ENGINE-SPLIT-EXEC-REFINE/reach/reach -dir . -files '<regexp>'`)
  and `frontier`
  (`.cswd/tmp/PG18-ENGINE-SPLIT-ANALYZE-QUERY-PREP/frontier/frontier -dir . -files a.go,b.go`),
  run from the worktree root; never pipe them into `head`. Rewrite tools:
  `movex`, `unalias`, `keylit`, `shadowrn` in
  `.cswd/tmp/PG18-ENGINE-SPLIT-PLAN-ALIASES/`; `movespec` and `expspec` in
  `.cswd/tmp/PG18-ENGINE-SPLIT-ANALYZE-QUERY-PREP/`.

## Goal

`control.go` implements native Reset, Dump and Restore. It is one file of
2,517 lines whose two big functions, `Engine.Dump` (1,588 lines) and
`Engine.Restore` (610 lines), name the internals of almost every feature.
That is the last central place that must change whenever a feature
changes.

Step 18 turned Dump's two switches into ordered tables. This step does the
same for Restore (30a), designs the host the drivers need (30b), and then
moves the drivers into `control` (30c): the Dump graph assembly and
emission, and the Restore replay loop with its hooks. The root keeps what
only the engine can do: take `Engine.mu`, capture the snapshot, build the
private restore stage, and install the replacement catalog atomically.

## Inventory (measured at `c154e14fb`)

### `control.go` (2,517 lines; 281 clean, 2,213 dirty)

| Declaration | Lines | Status | Content |
|---|---|---|---|
| `Engine.Reset` | 25–37 | dirty | `NewWithSettings`, `InstallForeignDefinitions`, `ReplaceCatalog` |
| `bootstrapNamespaceDumpable` | 39 | clean | builtin namespace ACL check |
| `Engine.Dump` | 73–1661 | dirty | locked capture (80–800), graph assembly (800–1080), emission (1080–1661) |
| `contextReader` | 1662 | clean | cancellable reader for Restore input |
| `Engine.Restore` | 1682–2291 | dirty | parse with decoders, restore session, statement loop, finish, publish |
| `commentLiteral`, `restoreScalar` | 2292, 2299 | clean | moved to `dump_restore_scalar.go` in 18a; `dump.CommentLiteral` and `dump.RestoreScalar` since 18b |

### The three central switches and four central walks

| What | Where | Replaced by |
|---|---|---|
| Switch #1: Dump descriptor kinds | was `control.go:265`–`800` | done in 18c: `dumpCollectors` and `dumpRelationCollectors` (`dump_collect.go`, `dump_collect_relation.go`) |
| Switch #2: Dump phase emission | was `control.go:1095`–`1190` | done in 18c: `dumpPhaseEmitters` (`dump_emit.go`) and `dumpSections` (`dump_sections.go`) |
| Switch #3: Restore statement allow-list and per-statement hooks | was `control.go:276`–`512` (since 18c), plus the hook calls around it | done in 30a: `restoreHooks` (`restore_hooks.go`), admit entries in `restore_admit.go` |
| `remapPreparedCatalog` (descriptor-kind switch over owners and ACLs) | `principal_remap.go:16` | `catalog` 5i (done: `catalog.RemapPrincipals`); mapper construction stays in the root |
| `reownCatalogExpressions` (catalog-wide expression reowner; also used by type generations, owned roles, extension owner import, generated replacement, TEMP preservation) | `catalog_expression_reown.go:15` | done: `stored.ReownExpressions` (22c); `remapCatalogExpressions` stays as the Restore entry |
| `remapViewRuleOIDs` | `catalog_expression_reown.go:312` | stayed in the root in 22c (it allocates OIDs); 30e: `control.RemapViewRuleOIDs` over an allocator function |
| `dumpPredecessor` (descriptor-kind dependency resolver) | `dump_resolver.go` (was `view_dump.go`, then `dump_graph.go`) | root `dumpResolver` implements `dump.Resolver` (18b); 30b: clean, it moves as `control.Resolver` in 30c |

`reownCatalogScalarTypes` (`domain_remap.go:14`) is a fifth walk of the
same kind; it is `stored.ReownScalarTypes` since stored 22e.

### Restore, step by step (`control.go:122`–`729` since 18c)

1. `stage := e.restoreStage()` (`principal_remap.go:276`): a private
   engine with the source roles, database and foreign definitions.
2. `io.ReadAll(contextReader{...})`, then `parser.ParseWithOptions` with a
   `LineComment` decoder chain: `typedef.DecodeDomainReplay`,
   `extension.DecodeOwners`, `view.DecodeMaterializedSnapshot`; then
   `typedef.PairDomainReplay`.
3. A session on the stage (`stage.StartSession`, `sess.Acquire`) as the
   bootstrap superuser.
4. Per statement, in this order:
   - **before** hooks: physical NOT NULL constructors
     (`preparePhysicalNotNullConstructor`, `prepareDeferredPhysicalPrimary`),
     owner-directive tracking, inherited and physical FK captures
     (`captureInheritedForeignKey`, `capturePhysicalSelfForeignKey`,
     `preparePhysicalCompetingUnique`);
   - **admit**: switch #3, one case per allowed statement kind
     (`AlterOwner`, `CreateTable`, `AlterDomain`, the schema-object DDL,
     `CreateOperator`/`AlterOperator`/`DropOperator`, `CreateFunction`,
     `CreateExtension`, `AlterView`, `CreateView`, `CreateQueryTable`,
     `CreateIndex`, `AlterIndex`, `CreatePolicy`, `CreateRule`,
     `AlterTable`, `AlterTableOf`, `AlterTableDetachPartition`,
     `DropTable`, `AlterTableAttachPartition`, `CreateSequence`,
     `AlterSequence`, `AlterTableDefault`, `Select`, `Comment`,
     `SessionAuthorization`, `Update`, `Delete`, `Insert`, `ForeignDDL`;
     `default` rejects). Cases call `dump.RestoreScalar` and restore executors
     such as `restoreAttributeRank`, `restoreInheritedCheck`,
     `restoreRetiredConstraint`, `restorePhysicalNotNull`,
     `restoreNoInheritNotNull`, `restoreInheritedOwner`,
     `restoreDomainOwner`, `capturePartitionReplayTeardown` (DETACH),
     `validatePartitionReplayTeardown` (DROP TABLE) and
     `validateRestoreIdentityConfiguration` (ALTER SEQUENCE);
   - **replay flags** on the operation's replay slot (`operationReplay`,
     `restore_replay.go`): `tableDestination` for 16 creation kinds,
     `insertAuthority`, `commentAuthority`, `defaultReplay`,
     `partitionAttach`, and `beginPartitionCheckReplay`,
     `beginPartitionUsingIndexReplay`, `beginPartitionUniqueReplay`;
   - **execute**: `stage.Execute(op, stmt, 0, nil)`, or
     `executeDomainReplayInsert` for paired domain-replay inserts;
   - **after** hooks: `capturePhysicalNotNullConstructor`,
     `capturePhysicalForeignKeyProvider`, `capturePhysicalImportIndex`,
     owner pairing, `captureRestoredDomainOwner`, historical facts
     (`captureHistoricalRelation`, `captureHistoricalFacts`),
     `captureRestoredNotNulls`, `captureRestoredPlaceholders`,
     `captureInheritedOwner`, `captureRestoredForeignKeys`,
     `resolveRestoredForeignKeyTargets`, `validateRestoredDefault`,
     `captureRestoredGeneration`, `pairRestoredNotNull`,
     `pairRestoredGeneration`, `closeRestoredRecipesForData`.
5. **finish** hooks: the physical NOT NULL boundary check,
   `bindRestoredForeignKeys`, `importExtensionOwners`,
   `importMaterializedSnapshots`, `validateRestoredSQLRoutines`.
6. `e.replaceCatalog(ctx, stage, stage.CatalogRead(), op)`.

The 38 hook methods live in their feature files, grouped by owner:
table and NOT NULL (`not_null_restore.go`, `physical_not_null_*.go`,
`foreign_key_restore.go`, `table_dump.go`, `attribute_rank_restore.go`,
`identity_control.go`; 17 hooks), inheritance (`inheritance_*_restore.go`,
`inheritance_recipe_checks.go`; 10), partition (`partition_*_replay.go`,
`partition_detach_restore.go`; 5), typedef (`domain_alter_dump.go`,
`domain_default_restore.go`; 3), extension (`extension_owner_import.go`;
1), view (`materialized_snapshot.go`; 1), routine (`sql_routine_restore.go`;
1). The per-run state is a dozen local maps (`partitionTeardowns`,
`notNullOrigins`, `physicalIndexes`, `placeholderOrigins`,
`historicalRefs`, `historicalAuthority`, pending foreign keys, extension
owners, materialized snapshots, domain replays, ...).

### What stays in the root (all sub-steps)

- `Engine.mu` and the Dump snapshot capture (`captureDumpLocked` and the
  Collect tables, `dump_capture.go`, since 18c): catalog state, roles, row
  versions, frontier, sequences, `foreignNamespaces`.
- `restoreStage` (`principal_remap.go:276`): it reads `e.roles`,
  `e.catalog`, the foreign definitions and builds a private `*Engine`.
- `replaceCatalog` (`catalog.go:1306`): the call to
  `control.PersistentReplacementCatalog` (30f), storage reminting,
  `remapPreparedCatalog`, `preserveTemporaryLocked` (the orchestrator; its
  decision phases are in `control` since 30f),
  `installForeignCatalogLocked`, `foreignNamespaces` replacement in the same
  publication, `clearPersistentSequenceMemoryLocked`,
  `fenceSerializableLocked`. Its lock order: `source.mu` is taken and
  released (`catalog.go:1466`–`1477`) before `e.replacementMu` and then
  `e.mu` (`:1485`–`1488`); `e.locks.Fence(installed)` runs in a defer
  after both are released.
- `Engine.Reset` and the public API: `Engine.Dump`, `Engine.Restore`,
  `Engine.Reset`, `Engine.ReplaceCatalog`, and the driver functions
  `stuntgres.Dump`, `stuntgres.Restore`, `stuntgres.Reset` (repo root
  `control.go`). After 30c the engine methods are thin wrappers.

## Part A

### 30a — Restore hook table, in place (pattern 5)

1. Re-measure: `reach` on `'^control\.go$'` and on the hook files above;
   confirm the step list. Stop and report if Restore changed shape.
2. **Run state.** Move the Restore locals into one root struct
   `restoreRun` (the maps of step 4 above, the paired-owner fields, the
   pending physical constructor). Its methods are the per-statement steps.
   No behavior change; commit.
3. **Hook table.** Declare, in a new `restore_hooks.go`:

   ```go
   type restoreHook struct {
       name   string // diagnostics and tests only
       decode func(comment string) (recognized bool, err error)
       admit  func(*restoreRun, *session.Operation, parser.Statement) (handled bool, err error)
       before func(*restoreRun, *session.Operation, parser.Statement) error
       after  func(*restoreRun, *session.Operation, parser.Statement, Result) error
       finish func(*restoreRun, context.Context, *session.Operation) error
   }
   ```

   Any field may be nil. The root builds one ordered `[]restoreHook` at
   package initialization. `Restore` runs each phase by walking the table
   in order and stops at the first error, exactly as the inline code does
   today. The `LineComment` chain keeps today's decoder order (domain
   replay, extension owners, materialized snapshot).
4. Fill the table group by group (one commit each): table and NOT NULL,
   inheritance, partition, typedef, extension, view, routine. Each entry
   calls the existing `Engine` method; the bodies do not move.
5. **Admission.** Switch #3 becomes the `admit` entries: one per statement
   kind, in today's case order; a statement no entry handles is rejected
   with today's error. The trusted-destination flag becomes one set of
   statement kinds (the 16 kinds listed above) instead of 16 `if`s.
6. Keep `operationReplay` resets (`defaultReplay`, `partitionAttach`,
   `partitionCheck` cleared after execute) in the same places.
7. Run the Dump/Restore byte-identity smoke of the `dump` prompt file and
   the targeted tests after each commit.

## Part B

### 30b — Refine `control.Host` (docs only; linear item 112)

Re-measure with `frontier` on the moving set, write the host, the move set
and the commit sequence into this file, then wait for approval. The draft
host had `CaptureDump`, `NewRestoreStage` with a `Stage` (`StartSession`,
`Execute` returning `Outcome{Created; Rows}`, `ExecuteDomainReplayInsert`,
`CatalogRead`, `OwnerAuthorization`), root-passed Dump and Restore tables
and `Publish`.

**30b refine (approved; measured at `116432550`).** `frontier` over
`control.go` without `Reset`, `restore_hooks.go`, `restore_run.go`,
`restore_admit.go`, `restore_steps_table.go`,
`restore_steps_inheritance.go`, `restore_steps_features.go`,
`restore_owner_directive.go`, `restore_replay.go`, `dump_script.go`,
`dump_emit.go`, `dump_emit_declaration.go` and `dump_sections.go` (output
in `.cswd/tmp/PG18-ENGINE-SPLIT-CONTROL-REFINE/frontier-control.txt`):

- **Dump is clean after the unlock.** The script build, the Emit table and
  the sections read only `dumpCapture` fields and four helpers that name
  no root declaration themselves: `dumpPredecessor`,
  `dumpColumnPredecessors` and `dumpResolver` (`dump_resolver.go`),
  `dumpPresenter`, `physicalNotNullDumpDependencies` and
  `domainInvalidDumpEvents`. `dumpCapture.recipePlan` is read only by the
  collectors.
- **Restore: the driver, the walkers and the hook type are clean; the
  entries are not.** The 66 entries call about 45 root `Engine` methods
  on `r.stage`, and `restoreRun` holds root records
  (`restoredPhysicalNotNull`, `restoredPhysicalForeignKeyProvider`) whose
  fields the entries change. After entries read only `Result.created`;
  only `restoreRun.execute` reads `Result.Rows`.
- **Not in the moving set.** `bootstrapNamespaceDumpable` has one caller,
  the namespace Collect entry (`dump_collect.go`).
  `validateTemporaryCollationDump` runs only inside `captureDumpLocked`.
  `contextReader` has no user outside `Engine.Restore`.
  `restore_replay.go` is read by about 58 root handler sites
  (`operationReplay`, `operationReplayOrZero`).

Decisions:

1. **Dump gets no host.** The root keeps `Engine.mu`, `captureDumpLocked`,
   the Collect tables (`dumpCollectors`, `dumpRelationCollectors`),
   `dumpSource` and `recipePlan`, which moves from the capture into
   `dumpCollection`. The capture becomes `control.Capture` with exported
   fields (`CatalogView`, `OriginalCatalog`, `OriginalRoles`,
   `DomainRoles`, `ConstraintReplay`, `ACLOutput`, `Preamble`, `Schemas`,
   `SchemaComments`, `Sequences`, `Domains`, `Collations`, `Conversions`,
   `Operators`, `SQLRoutines`, `Tables`, `Materialized`, `Views`,
   `Indexes`, `ViewIndices`, `TableIndices`, `IndexIndices`, `Composites`,
   `Graph`, `InlinePredecessors`, `DeferDefaults`); `dumpSchema`,
   `dumpView` and `dumpComment` become `control.Schema`,
   `control.OwnedStatement` and `control.SchemaComment`. The capture is
   handed over: Dump sorts and completes it in place, as today, and the
   root never reads it after the call.
   - `control.Dump(ctx context.Context, capture *Capture, dst io.Writer)
     error` sets the ACL output context, builds the script, writes the
     sections and returns `ctx.Err()`.
   - The Emit table (`dumpPhaseEmitters`) and the section list
     (`dumpSections`) become `control`'s own tables: they name only
     `control` emitters and feature packages, so nothing needs to pass
     them in. This corrects the sketch; rule 8 holds because a feature
     still adds an entry instead of editing a switch.
   - The root `Engine.Dump` keeps the nil-writer check, the context check
     and the lock window, then calls `control.Dump`.
2. **Resolver and presenter (question 2).** `dumpPredecessor` is clean, so
   it moves: `control.Resolver` (its `Predecessor` method is
   `dumpPredecessor`), `control.ColumnPredecessors` and `control.Presenter`.
   The root collectors and `captureDumpLocked`, and about 14 root tests,
   name them. Two graph helpers go lower instead, each over a
   `dump.Resolver` like `routine.OperatorDumpDependencies`:
   - `physicalNotNullDumpDependencies` → `table.PhysicalNotNullDumpDependencies`
     (table 24c decided this for 24d.3, item 89c; whichever of 30c and
     89c lands first moves it, and the other skips it);
   - `domainInvalidDumpEvents` → `typedef.DomainInvalidDumpEvents` (15e
     left it in the root only because of `dumpPredecessor`).
   `emitDumpDomainChecks` moves with the Emit table.
3. **`validateTemporaryCollationDump` (question 3)** needs neither a host
   method nor a reader cut: it is part of the locked capture, which stays
   in the root.
4. **`control.Host` is the root's side of one Restore.** A root
   `restoreHost{target *Engine; run *restoreRun}` implements it; only
   `Start` and `Publish` touch `target`, and no entry sees it.
   - `Begin(source string, statements []parser.Statement) error` records
     the script on the run and pairs the domain replays
     (`typedef.PairDomainReplay`).
   - `Start(ctx context.Context) (op *session.Operation, end func(), err
     error)` starts the stage session as the bootstrap superuser, acquires
     the operation and runs
     `startCaptures(target.retiredArchiveHighWater())`. `end` runs today's
     deferred steps in today's order: the replay-flag resets, `op.Done`,
     then `RequestEnd(session.CleanClose)` and `WaitEnd`. If `Acquire`
     fails, `Start` ends the session itself before returning the error.
   - `Statement(index int)` resets the per-statement state (`r.cur`).
   - `Execute(op *session.Operation, stmt parser.Statement) (Outcome,
     error)` is today's `restoreRun.execute`: the domain-replay INSERT
     dispatch, the replay-flag resets, closing rows and `DetachNotices`.
   - `Publish(ctx context.Context, op *session.Operation) error` is
     `target.replaceCatalog(ctx, stage, stage.CatalogRead(), op)`.
   `control.Restore[R any](ctx context.Context, host Host, hooks []Hook[R],
   run R, src io.Reader) error` reads through `contextReader`, checks the
   context, parses with the decode walk, then calls `Begin`, `Start`, and
   per statement `Statement`, before, admit, prepare, `Execute` and after;
   then finish and `Publish`, with `end` deferred. The root `Engine.Restore`
   keeps the nil-reader and context checks and builds the stage
   (`restoreStage`) before the input is read, as today.
   Corrections to the sketch: no `NewRestoreStage` or `Stage` interface;
   `CatalogRead` and the owner authorization stay off the host, because
   only entries use them; `Outcome` is `{Created ids.ObjectRef}` without
   rows, because rows are closed before the after entries run.
5. **`restoreRun` stays a root type for good (question 1).** It binds the
   entries to the stage `*Engine`, which `control` must never hold.
   `control` declares the generic entry type `Hook[R any]` (`Name`,
   `Decode`, `Before`, `Admit`, `Prepare`, `After`, `Finish`, with the
   30a signatures over `R`), `Admission` (`NotHandled`, `Admitted`,
   `Consumed`), `Outcome`, the rejection text and the walkers. The root
   composes `restoreHooks` as `[]control.Hook[*restoreRun]` at package
   initialization. Entry signatures change only from `Result` to
   `control.Outcome` and to the `control` admission constants; the
   `restoreAdmitKind`, `restoreAdmitChecked` and `restoreAdmitReplayed`
   adapters stay root, bound to `*restoreRun`. Because `control` never
   names the run's records, table 24d.3, partition 25d and view 27d stay
   independent of 30c.
6. **Every hook body stays a root `Engine` method (question 4)**, reached
   through the table:
   - table, NOT NULL and foreign keys: `preparePhysicalNotNullConstructor`,
     `capturePhysicalNotNullConstructor`, `prepareDeferredPhysicalPrimary`,
     `preparePhysicalCompetingUnique`, `capturePhysicalForeignKeyProvider`,
     `capturePhysicalSelfForeignKey`, `capturePhysicalImportIndex`,
     `captureRestoredNotNulls`, `pairRestoredNotNull`,
     `validateRestoredDefault`, `restoreNoInheritNotNull`,
     `restorePhysicalNotNull`, `restoreAttributeRank`; their cores move in
     24d.3 (item 89c) and the wrappers stay (24c).
     `captureRestoredForeignKeys`, `resolveRestoredForeignKeyTargets` and
     `bindRestoredForeignKeys` stay whole (24c group D);
   - inheritance: `captureInheritedForeignKey`,
     `closeRestoredRecipesForData`, `pairRestoredGeneration`,
     `captureRestoredGeneration`, `captureRestoredPlaceholders`,
     `captureInheritedOwner`, `restoreInheritedOwner`,
     `restoreInheritedCheck`, `captureHistoricalFacts`,
     `restoreRetiredConstraint` and `retiredArchiveHighWater` (the host's
     `Start`); cores in 24d.3 (item 89c);
   - partition: `beginPartitionCheckReplay`,
     `beginPartitionUsingIndexReplay`, `beginPartitionUniqueReplay`,
     `capturePartitionReplayTeardown`, `validatePartitionReplayTeardown`.
     They stay root for good (`Engine.mu` windows on the stage and the
     operation replay slot); this corrects the partition note that step 30
     owns them;
   - typedef: `captureRestoredDomainOwner`, `restoreDomainOwner`,
     `executeDomainReplayInsert` (the host's `Execute`); 15d and 15e are
     done;
   - extension `importExtensionOwners` (16d done), view
     `importMaterializedSnapshots` (stays after 27d, item 102), routine
     `validateRestoredSQLRoutines` (14e done), sequence
     `validateRestoreIdentityConfiguration` (17d moved its whitelist).
   `restore_replay.go`, `restoreStage`, the rest of `principal_remap.go`,
   the stage's `importTargets` and `replaceCatalog` stay in the root.
7. **Imports of `control` (question 5), measured:** `catalog`, `dump`,
   `ids`, `index` (through the resolver), `partition`, `regobj`,
   `routine`, `sequence`, `session`, `stored`, `table`, `typedef`, `view`,
   `expr`, `parser`, `sqltype`, `sqlstate`. Not `extension`, `fdw`, `exec`
   or `authorization`: the entries that use them stay in the root. Only
   the root imports `control`.
8. **Errors and order.** Every message and SQLSTATE stays identical.
   `control` gets its own `sqlError` (`errors.go`, the `view` shape) for
   the rejection. Dump output stays byte-identical; Restore keeps the
   phase order, the first-error rule and the deferred cleanup order of 30a.

**Move set (30c; new names).** Into `control`:

- `dump.go`: `Dump`, `Capture`, `Schema`, `OwnedStatement`,
  `SchemaComment`; `script.go` (from `dump_script.go`), `emit.go`,
  `emit_declaration.go`, `sections.go` (unexported script, writer,
  emitters and tables);
- `resolver.go`: `Resolver`, `ColumnPredecessors`; `presenter.go`:
  `Presenter`;
- `restore.go`: `Restore`, `Host`, `Outcome`, `contextReader`;
  `hooks.go`: `Hook`, `Admission`, the rejection text and the walkers;
- `errors.go`, `README.md`.

Into `table`: `PhysicalNotNullDumpDependencies` (unless 89c moved it).
Into `typedef`: `DomainInvalidDumpEvents`.

**Stays in the root.** `control.go` keeps the three public methods: `Reset`
and thin `Dump` and `Restore` wrappers; it is not deleted.
`bootstrapNamespaceDumpable` moves to `dump_collect.go`, next to its only
caller. `dump_capture.go`, `dump_collect.go`, `dump_collect_relation.go`,
`collation_seed_dump.go`, `restore_run.go` (with the new `restoreHost`),
`restore_hooks.go` (the table only), `restore_admit.go`,
`restore_steps_*.go`, `restore_owner_directive.go`, `restore_replay.go`,
`principal_remap.go` and `replaceCatalog`.

### 30c — Create `control` and move the drivers (linear item 113)

Follow 30b. Not broad: about 30 files. Commit sequence:

1. Graph helpers go down: `table.PhysicalNotNullDumpDependencies` (skip if
   89c moved it) and `typedef.DomainInvalidDumpEvents`, each taking a
   `dump.Resolver`; `dump_script.go` passes `dumpResolver{}`;
   `domain_alter_constraint_test.go` is qualified.
2. Create `internal/engine/control` with `errors.go` and a README stub;
   move `dump_resolver.go` and `dump_presenter.go` as `Resolver`,
   `ColumnPredecessors` and `Presenter`. The collectors, the capture and
   the root tests name `control.Resolver{}`/`control.Presenter{}`.
3. Capture record: `control.Capture` and its three records; the root
   `dumpCollection` embeds `*control.Capture` beside `recipePlan` and
   `source`; the collectors write the exported fields (`fieldrn`).
4. Move `dump_script.go`, `dump_emit.go`, `dump_emit_declaration.go`,
   `dump_sections.go` and the Dump body after the unlock into
   `control.Dump`; `Engine.Dump` becomes the wrapper. Run the
   byte-identity smoke (`.cswd/tmp/PG18-ENGINE-SPLIT-CONTROL-HOOKS/dumpsmoke`,
   baseline taken on the starting master).
5. Restore seam in place: root `restoreHost` with `Begin`, `Start`,
   `Statement`, `Execute` and `Publish`; `Engine.Restore` walks through
   it; `Result` stays for this commit.
6. Unit test first, observed failing: a `control` driver test with a fake
   `Host` and a synthetic table sees `Begin`, `Start`, then per statement
   `Statement`, before, admit, prepare, `Execute` and after, then finish,
   `Publish`, and `end` last; a failing step skips `Publish` but still runs
   `end`. Then move the driver, `Hook`, `Admission`, `Outcome`, the
   walkers and `contextReader` into `control`; the root entries adopt
   `control.Outcome` and the admission constants. The five walker tests
   of `restore_hooks_test.go` move to `control` over a test run type;
   `TestRestoreAdmitsInsertTargetIndirection` stays in the root and calls
   the INSERT entry (`restoreAdmitChecked(admitRestoreInsert)`).
7. `bootstrapNamespaceDumpable` to `dump_collect.go`; check with `go list`
   that only the root imports `control` and that `control` imports no root
   code.
8. Docs: the `control` README (what it owns, the host contract, the lock
   and publication rules that stay in the root, the Capture hand-over,
   `## Testing`); the root README's "Native Restore hook table" section
   becomes a pointer; the `dump` and `regobj` READMEs' "what stays"
   passages; `AGENTS.md` gets `control` in the subpackage brace list on
   the same line; this file's Status, the §8 row and the status page.

Proof: the units and reference selections of "Targeted tests", the
control-transaction and publication units and `^TestReferenceControl`
with `RACE=1`, the byte-identity smoke after commits 1–4 and 6, and
quickcheck.

### 30d — The Dump Collect tables move to `control` (linear item 120)

Planned by the end-state review (linear item 114, ENGINE-SPLIT.md §2.8 and
§6 "Third round"), measured at `17e01ca8b`. Needs 22e (item 119).

30c kept the Collect tables in the root because Dump needs no host; they
are reach-clean and read only captured values:

- `dump_collect.go` (`dumpCollectors`), `dump_collect_relation.go`
  (`dumpRelationCollectors`), the clean part of `dump_capture.go`
  (`dumpSource`, `dumpCollection`, `dumpPortable`,
  `dumpBuiltinSchemaPreamble`) and `preflightACLItemDump`
  (`aclitem_dump.go`).
- `captureDumpLocked` keeps its `Engine.mu` window and fills a
  `control.Source` value; the root passes it to `control.Dump`.
- Their only outside dependency was `typeProvider`; it is
  `stored.TypeProvider` since stored 22e.
- Byte-identical dump smoke (`.cswd/tmp/PG18-ENGINE-SPLIT-DUMP-PACKAGE/dumpsmoke`)
  before and after.

Verify with `make build`, the targeted tests below plus the unit tests of
the touched files, the quick and `REFERENCE=1` selections for the area, and
quickcheck; no behavior, error text or error-order change, no aliases.

### 30e — Definition OID remap (linear item 121)

Planned by the end-state review (linear item 114, ENGINE-SPLIT.md §2.8 and
§6 "Third round"), measured at `17e01ca8b`. Needs —.

`remapDefinitionOIDsLocked` (`definition_identity.go:15`, 348 lines) and
`remapViewRuleOIDs` (`catalog_expression_reown.go:19`) reach the engine only
through `e.catalog.allocateOIDLocked` and one `e.catalog.read`. They take an
allocator function and a `catalog.Read` and move to `control` (ddl 20c
planned this for step 30). The callers keep holding `Engine.mu`. Correct
the `catalog` README, which still says the remap stays in the root.

Verify with `make build`, the targeted tests below plus the unit tests of
the touched files, the quick and `REFERENCE=1` selections for the area, and
quickcheck; no behavior, error text or error-order change, no aliases.

### 30f — TEMP preservation phases (linear item 122)

Planned by the end-state review (linear item 114, ENGINE-SPLIT.md §2.8 and
§6 "Third round"), measured at `17e01ca8b`. Needs 30e (item 121).

`preserveTemporaryLocked` (`temporary_preservation.go:67-630`) is the
largest root function in the Restore path. Its phases over the current and
next `catalog.State` are decisions: the temporary set and retained builtin
types (`:75-255`), the OID collision mapping (`:258-295`, with the 30e
allocator shape) and the descriptor remap. They move to `control` with the
clean helpers of the file; the retain, check, import, repair and install
calls stay root. Measure first; if a phase needs more than three engine
reads, leave it and record why.

Verify with `make build`, the targeted tests below plus the unit tests of
the touched files, the quick and `REFERENCE=1` selections for the area, and
quickcheck; no behavior, error text or error-order change, no aliases.

### 30g — Run-free Restore admission and TEMP replacement checks (linear item 148)

Planned by the closing review (linear item 133, ENGINE-SPLIT.md §2.9 and
§6 "Fourth round"), measured at `4c05da8fe`. Needs —.

- The `restore_admit.go` entries that never read their `*restoreRun`:
  `restoreAdmitKind`, `restoreAdmitSchemaObject`, `restoreAdmitOperator`,
  `restoreRejectForeignDDL`, `restoreStagesTemporary`, the checks for
  ALTER DOMAIN (with `restoreDomainAlter` from `domain_alter_dump.go`),
  ALTER VIEW, CREATE VIEW, CREATE TABLE AS, ALTER INDEX, ALTER TABLE OF,
  ATTACH PARTITION, CREATE SEQUENCE, ALTER TABLE DEFAULT, SELECT, COMMENT
  and INSERT, and the stored-expression scalar walk of
  `admitRestoreCreateTable`. They become `control` statement checks with
  generic adapters (`control.AdmitStatement[R, T]`); the `restoreHooks`
  table stays root in today's order (pattern 5), so rejection order does
  not change.
- `retainTemporaryViewProvidersLocked`,
  `checkTemporaryRelationSourcesReplacementLocked` and
  `checkTemporaryRuleSourcesReplacementLocked`
  (`temporary_preservation.go`; the last two are also called from
  `replaceCatalog`). They read only the published `catalog.State` and its
  `catalog.Read` (the retention also writes the candidate state), so they
  move next to 30f's `TemporarySet` as `…Locked`
  functions over those values. The root composes the relation check with
  the typed replacement check (typedef 15f, item 147) instead of
  `control` calling it. This settles the 30f note.

`control` gains `largeobject` (a leaf). Update the README sentence that
keeps "every entry and hook body" in the root. `restore_hooks_test.go`
needs adapting.

Verify with `make build`, the unit tests of the touched root files and
the target package, quickcheck, and the quick and `REFERENCE=1` selections
for Restore admission, Restore rejection order, Reset/Restore with live TEMP objects and the dump smoke; no behavior, error text, error-order or lock-order change, no
aliases. Re-run `reach` on the moved set first: sizes are approximate.

## Invariants

- Dump output is byte-identical (event order, owner switches, statement
  text, error texts). Dump reads engine state only inside its `Engine.mu`
  window.
- Restore keeps its statement allow-list, the order of every
  before/admit/execute/after/finish step, the first-error-wins rule and the
  `operationReplay` resets; a failed Restore leaves the target catalog
  unchanged (`TestReferenceControlFailedRestorePreservesCatalog`).
- Restore publication stays one atomic `replaceCatalog`: the lock order
  above, the fence after unlock, the sequence-memory and serializable
  fences, and the `foreignNamespaces` replacement in the same publication.
- Hooks run on the private restore stage engine, never on the target
  engine.
- `control` holds no mutex and no engine pointer; it reaches the engine
  only through `control.Host` and the registered tables.

## Targeted tests

Tests follow the code and never block a move (ENGINE-SPLIT §4): driver
tests that need an engine fixture stay in the root and use the new names;
pure tests of the tables or the graph move with them.

- Engine units: `control_transactions_test.go`, `comment_control_test.go`,
  `transaction_controls_publication_test.go`, every `*_controls_test.go`,
  `*_restore_test.go`, `*_replay_test.go` and `*_remap_test.go`; grep
  `*_test.go` for `Restore(`, `Dump(`, `ReplaceCatalog`, `restoreStage`.
  Run the control-transaction and publication units with `RACE=1`
  (Dump and Restore race with live sessions).
- Reference selections, quick mode and `REFERENCE=1` (names only; never
  edit `reference/` or `smoke/`):
  `make check RUN='^TestReference.*(Dump|Restore|Native|RoundTrip|Replay)'`,
  `make check RUN='^TestReferenceControl'` (10 tests: failed Restore,
  snapshot survival, gated Dump and IO, replacement, host authorization,
  smoke), and the Reset tests `TestReferenceBtreeIndexHeldRowsAfterReset`,
  `TestReferenceRoleRestoreOwnerReset`. With `REFERENCE=1` run at least
  the `^TestReferenceControl` group and `TestReferenceDumpRoundTrip`.
  Failures that also occur on a `master` snapshot are oracle drift.
- The Dump/Restore byte-identity smoke from
  [`ENGINE-SPLIT-dump.md`](ENGINE-SPLIT-dump.md) after every commit of
  30a and 30c.

## Documentation

- Create `internal/engine/control/README.md` (30c): what it owns (the
  drivers and the table entry types), the host contract, the lock and
  publication rules that stay in the root, `## Testing`.
- Root `internal/engine/README.md`: replace passages that describe
  `control.go` internals with a pointer to the `control` README.
- `AGENTS.md`: add `control` to the subpackage brace list (30c); the file
  must not grow.
- Tick 30a–30c here and in
  [`ENGINE-SPLIT-STATUS.md`](ENGINE-SPLIT-STATUS.md); update the §8 cell.

## Done when

Restore's switch and hooks are a registered table (30a); `control` holds
the Dump graph assembly, the emission and the Restore loop, and imports no
root code (30c). The root keeps the lock window, the snapshot, the restore
stage and the atomic replacement. The checks in §8.3 pass.

## Status

- [x] 30a — Restore hook table in place (`dc839f171`, `192993651`, `fbcdc7fc9`, `22ef19659`, `6dc277b11`, `2eb9396e7`, `ba1fc1495`, `c22c9b9fb`, `80cc70d38`, `050e10e07`, `26ecb355d`, then the docs commit). Re-measured at `e6f9c647b`: Restore matched the step list
  above. `Engine.Restore` is now the driver (about 100 lines of
  `control.go`, which fell from 731 to 225 lines): read, parse with the
  decode entries, pair domain replays, start the stage session, walk the
  phases, `replaceCatalog`.
  - **Run state** (`restore_run.go`): `restoreRun` holds the stage, the
    script, the decoded records, every capture carried between statements
    and `cur restoreStatement` (index, physical constructor, physical
    index base, CREATE TABLE foreign keys, owner directive snapshot),
    reset before each statement. `startCaptures` allocates the maps after
    the session starts, as before (the history authority reads the target
    catalog's archive high water there).
  - **Hook table** (`restore_hooks.go`): `restoreHook` and the ordered
    `restoreHooks`, 66 entries: 3 decode, 8 before, 29 admit, 8 prepare,
    13 after, 5 finish. Each entry has one phase and the table is grouped
    by phase; entry names start with the owning group (`table:`,
    `inheritance:`, `partition:`, `typedef:`, `extension:`, `view:`,
    `routine:`) or name the statement kind (admit) or the driver step
    (owner directives, replay flags). Walkers take the table as a
    parameter.
  - **Corrections to the sketch above**, each needed to keep today's
    behavior: `decode` takes the run and the comment position (domain
    replay records keep their end offset); `before` returns `consumed`
    (the routine replay directive and the inherited and physical self
    foreign key captures skip the statement); `admit` answers
    `restoreNotHandled`, `restoreAdmitted` or `restoreConsumed` (UPDATE
    and DELETE repairs do not execute); a new `prepare` phase holds the
    steps between admission and execution (recipe data closure, NOT NULL
    and generation pairing, replay flags, the partition CHECK, USING INDEX
    and UNIQUE leases and the captured-unique rejection). Execution, the
    domain-replay INSERT dispatch, the post-execute resets, rows close and
    `DetachNotices` stay in the driver (`restoreRun.execute`).
  - **Entries** (bodies unchanged in their feature files):
    `restore_admit.go` (allow-list; `restoreAdmitKind`,
    `restoreAdmitChecked`, `restoreAdmitReplayed` adapters; the unhandled
    kind text is `restoreRejection`), `restore_steps_table.go`,
    `restore_steps_inheritance.go`, `restore_steps_features.go`
    (decoders, partition, typedef, extension, view, routine),
    `restore_owner_directive.go`, and in `restore_replay.go`
    `prepareRestoreReplayFlags` with `restoreTableDestination` (the 16
    trusted-destination kinds as one switch).
  - **Bug caught during the work:** the commit-2 extraction turned the
    INSERT target loop's inner `continue` (skip a non-subscript step)
    into "consume the statement". No existing test noticed; it was fixed
    in that commit before integration and
    `TestRestoreAdmitsInsertTargetIndirection` pins it.
  - Tests added: `restore_hooks_test.go` (walker semantics on synthetic
    tables, and the INSERT regression). None moved or deleted.
  - Verification: targeted engine units (the prompt's restore, replay,
    remap and controls files plus every `*_test.go` naming `Restore(`,
    `Dump(`, `ReplaceCatalog` or `restoreStage`), the byte-identity smoke
    (the 18c `dumpsmoke`, baseline re-taken on each new master) and
    quick-mode `^TestReference.*(Dump|Restore|Native|RoundTrip|Replay)`
    and `^TestReferenceControl` after every commit and after the rebase
    onto `879848377`; `RACE=1` for `^TestReferenceControl` with the
    control-transaction, publication and hook units; quickcheck;
    `REFERENCE=1` for `^TestReferenceControl`, `TestReferenceDumpRoundTrip`,
    `TestReferenceTypeCoreDumpRoundTrip`,
    `TestReferenceCollationSeedNativeReplay` and
    `TestReferenceCompositeAlterDumpReplay`; racecheck/finalcheck skipped
    at user request. An independent review compared the final table and
    every intermediate commit with the original loop and found no
    behavior difference.
- [x] 30b — Host refined (docs only; the docs commit). Measured at
  `116432550` with `frontier` (see the 30b refine above): Dump gets no
  host, only the `control.Capture` record handed to `control.Dump`, and
  the Emit table and sections become `control`'s own tables; `Resolver`,
  `ColumnPredecessors` and `Presenter` move to `control`,
  `physicalNotNullDumpDependencies` to `table` and
  `domainInvalidDumpEvents` to `typedef`, both over a `dump.Resolver`;
  Restore runs over a five-method `control.Host` (`Begin`, `Start`,
  `Statement`, `Execute`, `Publish`) on a root `restoreHost`, with the
  root-composed `[]control.Hook[*restoreRun]`; `restoreRun`, every hook
  body, `restore_replay.go`, the stage and the publication stay in the
  root. Docs only, no checks needed.
- [x] 30c — `control` created and the drivers moved (`5be23f127`,
  `6bd6f8e1c`, `a1316f97b`, `c1ffde11b`, `c4d6f2924`, `51b38752f`,
  `6493a12a6`, then the docs commit). Re-measured at `949c682a1`: none of
  the moving files had changed since the 30b measurement; rebased onto
  `a8f6930c8` (table 24d.1) and `2f96d483d` (view 27d).
  - **Dump** (`control/dump.go`, `script.go`, `emit.go`,
    `emit_declaration.go`, `sections.go`): `Capture`, `Schema`,
    `OwnedStatement`, `SchemaComment` and `Dump(ctx, capture, dst)`; the
    script, sections, Emit table and writer are unexported. The capture
    fields were renamed type-checked (a multi-field `fieldrn`,
    `.cswd/tmp/PG18-ENGINE-SPLIT-CONTROL-PACKAGE/fieldsrn`).
    `recipePlan` moved off the capture into the root `dumpCollection`.
    `Engine.Dump` keeps the nil-writer and context checks and the lock
    window.
  - **Resolver and presenter** (`resolver.go`, `presenter.go`):
    `Resolver` (`Predecessor`; the body is the unexported `predecessor`),
    `ColumnPredecessors`, `Presenter`.
  - **Restore** (`restore.go`, `hooks.go`): `Restore[R]`, `Host`,
    `Outcome{Created}`, `contextReader`, `Hook[R]` (`Name`, `Decode`,
    `Before`, `Admit`, `Prepare`, `After`, `Finish`), `Admission`
    (`NotHandled`, `Admitted`, `Consumed`), the rejection text and the
    walkers; `errors.go` holds `sqlError`. The root `restoreHost`
    (`restore_run.go`) implements `Host`; `restoreRun.execute` moved beside
    it and returns `control.Outcome`. `restore_hooks.go` holds only
    `restoreHooks` (`[]control.Hook[*restoreRun]`, order unchanged).
    `Engine.Restore` keeps the nil-reader and context checks and builds the
    stage before the input is read.
  - **Graph helpers**: `table.PhysicalNotNullDumpDependencies`
    (`table/physical_not_null_dump_graph.go`; 89c no longer moves it) and
    `regobj.DomainInvalidDumpEvents` (`regobj/domain_invalid_dump.go`).
  - **Corrections to the 30b plan.** The domain helper went to `regobj`,
    not `typedef`: it walks `regobj.ClassReplayReferences`, and `regobj`
    imports `typedef`, so `typedef` cannot call it back. `control` also
    imports `authorization` (the capture's role reads), which decision 7
    left out; it still imports no `extension`, `fdw` or `exec`.
    `bootstrapNamespaceDumpable` moved to `dump_collect.go`; `control.go`
    is 59 lines (`Reset` and the two wrappers).
  - **Tests.** New: `control/restore_test.go` (driver order with a fake
    `Host`: per-statement order, consumed before and admit, a failing
    after or execute skips `Publish` but runs `end`, a failing `Publish`,
    rejection of an unhandled statement, and no host call after a start,
    parse or context failure), written first and observed failing to
    compile. Moved: the five walker tests of `restore_hooks_test.go` to
    `control/hooks_test.go` over a synthetic run (names unchanged).
    Adapted: `TestRestoreAdmitsInsertTargetIndirection` stays root and
    calls `restoreAdmitChecked(admitRestoreInsert)` directly; about 14 root
    tests name `control.Resolver{}`, `control.ColumnPredecessors`,
    `control.Presenter` or `regobj.DomainInvalidDumpEvents`. None deleted.
  - **Verification**: after every commit and after the rebase, the
    byte-identity smoke (the 30a `dumpsmoke`, baseline on the starting
    master, re-taken and unchanged on each new master), the 30a targeted
    engine units plus the `control` tests, the tests of every touched test
    file, and quick `^TestReference.*(Dump|Restore|Native|RoundTrip|Replay)`
    and `^TestReferenceControl`; `RACE=1` for `^TestReferenceControl`, the
    control-transaction, publication, catalog-replacement and hook units;
    quickcheck; `REFERENCE=1` for `^TestReferenceControl`,
    `TestReferenceDumpRoundTrip`, `TestReferenceTypeCoreDumpRoundTrip`,
    `TestReferenceCollationSeedNativeReplay` and
    `TestReferenceCompositeAlterDumpReplay`; `go list` shows only the root
    imports `control`; `make deps` clean. racecheck/finalcheck skipped at
    user request.
- [x] 30d — Dump Collect tables moved (linear item 120) (`10b014190`,
  `d4932f20f`, then the docs commit). Re-measured at `307583de0`: the moving
  files matched the 30d plan; rebased onto `018bd1d8b` (dml 29d.5) and
  `bfad9b467` (partition constraints).
  - **Collect** (`control/collect.go`, `collect_relation.go`, from
    `dump_collect.go` and `dump_collect_relation.go`): `Source`,
    `Collect(ctx, source) (*Capture, error)`, the unexported
    `dumpCollection` (`*Capture` plus `source`), both Collect tables and
    their entries under their old names, and
    `BootstrapNamespaceDumpable`. `Source` carries the capture's reads,
    the constraint replay, the ACL output, the generation recipe plan
    (`RecipePlan`, formerly `dumpCollection.recipePlan`) and the locked
    state (`State`, `Relations`, `Sequences`, `Frontier`, `Bootstrap`,
    `Foreign`).
  - **Capture checks** (`control/capture_checks.go`,
    `aclitem_preflight.go`): `CheckPortable` (was `dumpPortable`), the
    unexported builtin-schema preamble, `PreflightACLItems` (was
    `preflightACLItemDump`, `aclitem_dump.go`).
  - **Root** (`dump_capture.go`, 65 lines): `captureDumpLocked` keeps every
    engine read and today's order: context, `CheckPortable`, catalog and
    role reads, recipe plan, ACL output context, `PreflightACLItems`,
    constraint catalog, domain roles, `validateTemporaryCollationDump`,
    the configured foreign namespace check, then `Collect`.
  - **Correction to the 30d plan.** The plan said the root "passes
    `control.Source` to `control.Dump`". Collection reads relation roots,
    sequence generators and the foreign registry, which are valid only
    under `Engine.mu`, so `Collect` runs inside the root's lock window and
    `control.Dump` still takes the finished `Capture`. The temporary
    collation check stays root (it opens a catalog stage), and so does the
    configured foreign namespace check, so error order is unchanged.
  - **Layering.** `control` now imports `access`, `extension`, `fdw`,
    `plan`, `storage` and `visibility` too (all below L14); `fdw` is the
    only one new to its dependency closure. `go list` shows only the root
    imports `control`.
  - **Tests.** None moved or deleted (no test named the Collect tables).
    Adapted: `TestPGNamespaceShape` and `TestPredefinedRoleControls` call
    `control.BootstrapNamespaceDumpable`; the latter's loop variable
    `control` became `run` (it shadowed the package). No new unit test:
    the move adds no semantics.
  - **Verification**: after each commit and after the rebase, the
    byte-identity dump smoke (the 30c `dumpsmoke`, baseline on the
    starting master and re-taken on each new master: unchanged on
    `018bd1d8b`; `bfad9b467` changed it through its own partition
    constraint work, and the rebased branch matched the new baseline), the 30c
    targeted engine units plus the two adapted tests and the `control`
    tests, and quick `^TestReference.*(Dump|Restore|Native|RoundTrip|Replay)`
    and `^TestReferenceControl`; `RACE=1` for `^TestReferenceControl`,
    `^TestControlTransactions` and `^TestRestore(Driver|Hooks)`;
    quickcheck; `REFERENCE=1` for `^TestReferenceControl`,
    `TestReferenceDumpRoundTrip`, `TestReferenceTypeCoreDumpRoundTrip`,
    `TestReferenceCollationSeedNativeReplay` and
    `TestReferenceCompositeAlterDumpReplay`; `make deps` clean.
    racecheck/finalcheck skipped at user request.
- [x] 30e — Definition OID remap moved (linear item 121) (`46f9e094a`,
  then the docs commit). Re-measured at `559a4ff4a` and rebased onto
  `eaf63bb35` (citext) and `079c38647` (dml 29d.6), which touched none of
  the moved code: both bodies matched the 30e plan (`definition_identity.go`, 348 lines, and
  `catalog_expression_reown.go:19`); the only engine reads were
  `e.catalog.allocateOIDLocked` and one `e.catalog.read(next)`.
  - **Moved** (bodies unchanged): `control.RemapDefinitionOIDs(view,
    allocate)` (`control/definition_oids.go`) and
    `control.RemapViewRuleOIDs(view, allocate)` (`control/view_rule_oids.go`,
    which ends by calling the first). `view` is a `catalog.Read` over the
    replacement candidate, which both change in place (`next :=
    view.State()`, so the candidate has one source); `allocate` is
    `func() (ids.OID, error)`. `definition_identity.go` is gone;
    `catalog_expression_reown.go` keeps only `remapCatalogExpressions`.
  - **Root**: `preserveTemporaryLocked` calls
    `control.RemapViewRuleOIDs(e.catalog.read(next),
    e.catalog.allocateOIDLocked)` under `Engine.mu`, as before. Building
    the view before the remap instead of at the rule step is
    side-effect free (`catalog.NewRead` only pairs owner and state). No
    new imports for `control` (it already imported `catalog`, `ids`,
    `index` and `stored`).
  - **Tests.** None moved or deleted. Adapted (engine fixtures, so they
    stay root): `TestRuleNamedConstraintRemap`,
    `TestDeparseConstraintsAllocatorOccupancy` and
    `TestPartitionConstraintsDefinitionRemapping` call
    `control.RemapDefinitionOIDs`. No new unit test: the move adds no
    semantics.
  - **Verification**: the byte-identity dump smoke (the 30d `dumpsmoke`,
    baseline on the starting master), the 30d targeted engine units plus
    the three adapted tests and the `control` tests, and quick
    `^TestReference.*(Dump|Restore|Native|RoundTrip|Replay)` and
    `^TestReferenceControl`, after the move and after the rebase;
    quickcheck (before and after the rebase); `REFERENCE=1` for
    `^TestReferenceControl`, `TestReferenceDumpRoundTrip`,
    `TestReferenceTypeCoreDumpRoundTrip`,
    `TestReferenceCollationSeedNativeReplay` and
    `TestReferenceCompositeAlterDumpReplay`; `go list` shows only the root
    imports `control`; `make deps` clean. racecheck/finalcheck skipped at
    user request.
- [x] 30f — TEMP preservation phases moved (linear item 122) (`e3948ad1b`,
  `9aec972de`, then the docs commit). Re-measured at `16cb9f7d0` with
  `reach`: `preserveTemporaryLocked` matched the 30f plan (lines 67–635);
  `persistentReplacementCatalog`, `arrayDomainDependency`,
  `retainedArrayDependencies` and `temporaryInheritanceReplacementError`
  were clean, the four `Engine` methods dirty only through `e.catalog`.
  The moving phases read the engine through two things only: the
  published view (`e.catalog.read(e.catalog.state)`) and
  `e.catalog.allocateOIDLocked`, under the three-read limit.
  - **Moved** (bodies unchanged; `e.catalog.state` became
    `current.State()`, the published view `current`, the allocator
    `allocate`): `control.PersistentReplacementCatalog`
    (`persistent_replacement.go`, was `persistentReplacementCatalog`);
    `control.TemporarySet` and `control.CheckTemporaryDependencies` with
    `arrayDomainDependency` and `retainedArrayDependencies`
    (`temporary_set.go`); `control.RetainTemporaryBuiltinTypes`
    (`temporary_types.go`, returns the old `typesChanged`);
    `control.TemporaryCollisionMapping(next, temporary, allocate)`
    (`temporary_identity.go`, the 30e allocator shape);
    `control.RemapReplacementIdentities` (`replacement_remap.go`, the old
    `if len(mapping) != 0` block, now a guard). The per-kind descriptor
    switch became `remapReplacementDescriptor`, its relation case
    `remapReplacementRelation` (`replacement_remap_relation.go`; its
    `break` became `return r, nil`) and the root re-keying
    `remapStorageRoots`.
  - **Root** (`temporary_preservation.go`, 783 → 301 lines):
    `preserveTemporaryLocked` keeps `Engine.mu`, builds `current` once and
    calls the phases in the old order between `retainDatabaseLocked`, the
    source checks, `retainTemporaryViewProvidersLocked`, the scalar and
    domain imports, `repairRemappedGenerationsLocked`, the TEMP install
    loop, the TEMP policy and rule ref refresh, `control.RemapViewRuleOIDs`,
    the table remaps and the final validation. Building `current` before
    the phases instead of at each use is side-effect free
    (`catalog.NewRead` pairs owner and state).
  - **Layering.** `control` now imports `analyze` (L10, below L14) for
    `analyze.WalkQueryArgumentExpression`; `go list` shows only the root
    imports `control`.
  - **Tests.** None moved or deleted. Adapted (engine fixtures, so they
    stay root): `TestExtensionTemporaryReplacement`
    (`extension_temporary_test.go`), `TestDefaultPrivilegesReplacement`
    (`default_privileges_controls_test.go`) and
    `TestUserOperatorIndexTemporaryCollisionReplacement`
    (`operator_index_test.go`) call `control.PersistentReplacementCatalog`.
    No new unit test: the move adds no semantics. The callers of
    `preserveTemporaryLocked` (inheritance, typed, partition, aggregate and
    operator index tests) are unchanged.
  - **Verification**: after each commit and after the rebases onto
    `80dda192c` (scripts only) and `475d044a2` (routine drop, which touched
    none of the moved code), the byte-identity dump smoke (the 30d
    `dumpsmoke`, baseline on the starting master `16cb9f7d0`, re-taken and
    unchanged on `475d044a2`), the 30d
    targeted engine units, the tests of every adapted file and every
    `preserveTemporaryLocked` caller, `Test.*(Temporary|Retained).*`, the
    `control` tests, and quick
    `^TestReference.*(Dump|Restore|Native|RoundTrip|Replay)`,
    `^TestReferenceControl` and `^TestReference.*Temporary`; quickcheck
    after each rebase; `REFERENCE=1` for `^TestReferenceControl.*`,
    `TestReferenceDumpRoundTrip`, `TestReferenceTypeCoreDumpRoundTrip`,
    `TestReferenceCollationSeedNativeReplay` and
    `TestReferenceCompositeAlterDumpReplay`; `go list` shows only the root
    imports `control`; `make deps` clean. racecheck/finalcheck skipped at
    user request.
  - **Status page.** Its tree still showed 30e unticked although item 121
    was ticked; fixed with this record.
- [ ] 30g — Run-free Restore admission and TEMP replacement checks (linear item 148)

Outstanding aliases: none (30a, 30c, 30d, 30e and 30f added none).

### Notes for later steps

- 30c: entries communicate only through `restoreRun`, so table order is
  part of the contract (see the `restoreHooks` comment); the move keeps
  `restoreHooks` in today's order.
- 30c: `restoreHost` holds the target engine only for `Start` (the
  archive high water) and `Publish`; every entry still runs on the stage.
- Table 24d.3 (item 89c) changes the types of `restoreRun` fields
  (`restoredPhysicalNotNull`, `restoredPhysicalForeignKeyProvider` become
  `table` records) without touching `control`, which never names them
  (done: `table.RestoredPhysicalNotNull`,
  `table.RestoredPhysicalForeignKeyProvider`).
  30c landed first and moved `physicalNotNullDumpDependencies`
  (`table.PhysicalNotNullDumpDependencies`); 89c skips it.
- The hook bodies stay root `Engine` methods for good, reached through the
  table (decision 6 of 30b); table 24d.3 moves only their cores into
  `table`, and partition 25d (item 93) only the replay capture and match
  cores (25c). View 27c: 27d (item 102) moves no Restore hook body;
  `importMaterializedSnapshots` stays in the root.
- Partition 25c noted that four root trust checks
  (`partitionConstraintReplayImport`, `partitionDefaultReplayAllowed`,
  `partitionAttachAuthorization`, `partitionParentAuthorization`) repeat
  one bootstrap-superuser test. They are not in `control`'s moving set, so
  folding them into one root helper is a separate root cleanup, not part
  of 30c.
- `restore_replay.go` (`restoreReplay`, the `Operation` replay slot) stays
  in the root: about 58 root handler sites read it.
- 30e: 30f's OID collision mapping can take the same `allocate func()
  (ids.OID, error)` shape; `preserveTemporaryLocked` already passes
  `e.catalog.allocateOIDLocked` to `control.RemapViewRuleOIDs`.
- 30e: `reference/indexes/README.md` still names
  `remapDefinitionOIDsLocked` as a source authority; `reference/` is
  read-only for the split, so a later non-split change should rename it to
  `control.RemapDefinitionOIDs`.
- 30f: step 30 has no planned sub-step left. What remains in
  `temporary_preservation.go` reads only the published and candidate
  catalogs too, except the install loop (storage and the stage lock) and
  the imports and repair (engine state): the TEMP policy and rule ref
  refresh, the final validation loop, `retainTemporaryViewProvidersLocked`
  and the two source checks (`checkTemporary{Relation,Rule}SourcesReplacementLocked`,
  also called from `replaceCatalog`) could take a `catalog.Read` and move
  to `control` in a later cleanup; the 30f plan kept them root. Item 133
  planned the view-provider retention and the two source checks as 30g
  (item 148); the TEMP policy and rule ref refresh and the final
  validation loop stay with the install loop.
- 30d: `dump_capture.go` is now only the lock window. Its two remaining
  engine-only steps are `validateTemporaryCollationDump` (needs
  `BeginCatalogStage` for `domainDependencyClosure`) and the
  `e.foreign.NamespaceEntries` check; moving either needs a host, which
  Dump does not have, so they stay.
