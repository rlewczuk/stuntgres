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
| `remapViewRuleOIDs` | `catalog_expression_reown.go:312` | stays in the root: it allocates OIDs (the `stored` prompt) |
| `dumpPredecessor` (descriptor-kind dependency resolver) | `dump_resolver.go` (was `view_dump.go`, then `dump_graph.go`) | root `dumpResolver` implements `dump.Resolver` (18b); 30b decides whether it moves with the drivers |

`reownCatalogScalarTypes` (`domain_remap.go:14`) is a fifth walk of the
same kind; it goes with `ReownExpressions` in 22c.

### Restore, step by step (`control.go:122`–`729` since 18c)

1. `stage := e.restoreStage()` (`principal_remap.go:276`): a private
   engine with the source roles, database and foreign definitions.
2. `io.ReadAll(contextReader{...})`, then `parser.ParseWithOptions` with a
   `LineComment` decoder chain: `typedef.DecodeDomainReplay`,
   `extension.DecodeOwners`, `decodeMaterializedSnapshot`; then
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
- `replaceCatalog` (`catalog.go:1306`): `persistentReplacementCatalog`,
  storage reminting, `remapPreparedCatalog`, `preserveTemporaryLocked`,
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

### 30b — Refine `control.Host` (docs only)

Write the result into this file and wait for approval. Re-measure with
`frontier` on the moving set: `control.go` without `Reset` and without the
locked capture, plus `restore_hooks.go`, `restore_replay.go`,
`contextReader`, `bootstrapNamespaceDumpable`.

**Host sketch (to confirm or correct):**

- **Dump capture.** `CaptureDump(ctx) (Capture, error)`: the root takes
  `Engine.mu`, runs `captureDumpLocked` and returns an immutable record
  (catalog and role views, collected per-kind lists, table dump records,
  sequence dump state, priorities). After unlock `control` assembles the
  graph and emits; it never reads engine state again.
- **Restore stage.** `NewRestoreStage(ctx) (Stage, error)` wraps
  `restoreStage`; `Stage` offers `StartSession`, `Execute(op, stmt)`
  returning a narrow `Outcome{Created ids.ObjectRef; Rows exec.RowResult}`
  (never the root `Result`), `ExecuteDomainReplayInsert`, `CatalogRead`,
  and `OwnerAuthorization(op)` for owner directives.
- **Hooks.** The `[]restoreHook` table and the Dump Collect/Emit tables
  are passed in by the root (rule 8); `control` declares the entry types.
- **Publication.** `Publish(ctx, stage, op) error` wraps `replaceCatalog`.

**Questions the refine settles:**

1. Whether `restoreRun` moves into `control` (likely: it holds only
   records of feature packages after their Part A) or stays a root type
   behind the hooks.
2. Whether `dumpPredecessor` moves with the drivers as the default
   `dump.Resolver` (only if `reach` reports it clean after 22a) or stays a
   root function passed in.
3. Whether `validateTemporaryCollationDump` (builds a `CatalogStage`) gets
   a host method or a reader cut (pattern 6).
4. Which hook bodies are still root `Engine` methods at that point (they
   stay root and are reached through the table); list each with its
   owner's Part B item.
5. The import list of `control`: expected `dump`, `stored`, `regobj`,
   `typedef`, `routine`, `sequence`, `extension`, `index`, `table`,
   `partition`, `view`, `fdw`, `session`, `catalog`, `authorization`,
   `exec`, `parser`, `sqltype`, `sqlstate`. `control` must not import the
   root, and nothing but the root may import `control`.

### 30c — Create `control` and move the drivers

Follow 30b. Expected shape:

1. Create `internal/engine/control` with `errors.go` and `README.md`.
2. Move the Dump graph assembly and emission (`dump_script.go`,
   `dump_emit.go`, `dump_emit_declaration.go`, `dump_sections.go` since
   18c), the Restore loop (`control.go:122`–`729` minus stage creation and
   publication), `contextReader`, `bootstrapNamespaceDumpable`, the hook
   and table entry types and `restoreRun` (if 30b keeps it there).
3. The root keeps thin `Engine.Dump` and `Engine.Restore` methods: capture
   or stage creation, then `control.Dump(ctx, capture, tables, w)` /
   `control.Restore(ctx, host, hooks, r)`.
4. No aliases: the root names `control.X` directly.
5. Delete `control.go` from the root when it only holds `Reset`; move
   `Reset` next to `replaceCatalog` (`catalog.go`) or into a short
   `reset.go`.

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
- [ ] 30b — Host refined (docs only)
- [ ] 30c — `control` created and the drivers moved

Outstanding aliases: none (30a added none).

### Notes for later steps

- 30b's moving set after 30a: the `Restore` driver and `restoreRun.execute`
  (`control.go`), `restore_hooks.go`, `restore_run.go`, `restore_admit.go`,
  `restore_steps_*.go`, `restore_owner_directive.go` and the replay-flag
  entry in `restore_replay.go`.
- `restoreRun` fields hold root records (`restoredForeignKeys`,
  `restoredNotNull`, `restoredPhysicalNotNull`,
  `physicalNotNullConstructor`, `restoredPhysicalForeignKeyProvider`,
  `restoredGenerationRecipe`, `restoredHistoryAuthority`,
  `partitionReplayTeardown`, `restoredInheritedOwner`,
  `materializedSnapshot`), and every entry calls an `Engine` method on
  `r.stage`. 30b question 1 therefore depends on those records reaching
  their packages or becoming opaque to `control`.
- `after` entries take the root `Result` but read only `created`; the
  sketch's narrow `Outcome` can replace it. Entries communicate only
  through `restoreRun`, so table order is part of the contract (see the
  `restoreHooks` comment).

- Item 112 (30b) needs items 80 (routine 14e) and 81 (typedef 15e): the
  moved drivers call the routine and type dump statements, which must live
  in their packages by then.
- The hook bodies stay root `Engine` methods until their owners' Part B
  (24d table, 25d partition, 15e typedef, 27d view); the table reaches
  them, so 30c does not wait for those. Table 24c: the table hook bodies
  stay root wrappers for good (authority, the trust check, `Engine.mu` and
  the stage lifecycle); 24d.3 (item 89c) moves only their cores into
  `table`.
- `restore_replay.go` (`restoreReplay`, the `Operation` replay slot) is
  read by feature handlers (`tableDestination`, `partitionAttach`, ...);
  if it moves, it moves to the lowest package those handlers import.
