# Engine split, step 25: `internal/engine/partition`

Session prompt. Start a fresh session with "Implement
`docs/ENGINE-SPLIT-partition.md`." Follow the shared rules in
[ENGINE-SPLIT.md §8](ENGINE-SPLIT.md#8-implementation-sessions): protocol,
constraints, verification, documentation and report.

- **Plan:** [ENGINE-SPLIT.md](ENGINE-SPLIT.md) §6 step 25; §3 rules 6–8
  (`partition` is an upper feature above `index` and `table`: it imports
  them, they never import it; it may use `analyze` types and pure
  functions, never build a binder); §4.1 patterns 3 (capability), 4
  (refine first) and 6 (reader-interface cut); §4 "Rules for tests during
  the split".
- **Needs:**
  - 25a (linear item 90): nothing. It touches files that 23a also edits
    (`partition_retained_checks.go`, `partition_*_dump.go`); if 23a is in
    flight on another branch, rebase onto it first.
  - 25b (item 91): 90 and 87 (`table` 24b, which brings `index` 23b,
    `stored` 22b, `regobj` 21b and catalog 5i). The partition dump code
    also needs the `dump` records (item 56, 18b): check that 87 already
    waits for 56, and stop if it does not.
  - 25c (item 92): 91 and 88 (`table` 24c).
  - 25d (item 93): 92 and 89 (`table` 24d).
- **Effort:** high for 25a and 25b; xhigh for 25c; high for 25d.
- **Session size:** one sub-step per session.
- **Linear items:** 25a = 90, 25b = 91, 25c = 92, 25d = 93. Items that wait
  for this step: 29b (108) needs 91, 29c (109) needs 93, 30b (112) needs 91.
- **Tools:** as in [ENGINE-SPLIT-index.md](ENGINE-SPLIT-index.md): `reach`
  (`.cswd/tmp/PG18-ENGINE-SPLIT-EXEC-REFINE/reach/reach -dir . -files '<regexp>' [-cut 'From>To']`),
  `frontier` (`.cswd/tmp/PG18-ENGINE-SPLIT-ANALYZE-QUERY-PREP/frontier/frontier -dir . -files a.go,b.go`),
  the rewrite tools in `.cswd/tmp/PG18-ENGINE-SPLIT-PLAN-ALIASES/` and
  `.cswd/tmp/PG18-ENGINE-SPLIT-ANALYZE-QUERY-PREP/`. Run them from the
  worktree root; never pipe them into `head`. Planning measurements:
  `.cswd/tmp/ENGINE-SPLIT-POSTPONED-PLAN/partition/`.

## Goal

Partitioned tables become a package: partition bounds and routing,
partition proofs and retained checks, hash bounds, and the partition dump
events and Restore replay records (Part A); then the decisions of CREATE
TABLE ... PARTITION BY / PARTITION OF, ATTACH PARTITION and DETACH
PARTITION (Part B).

Partitioned indexes are index code: step 23 moves them to `index`. INSERT
routing keeps running in the root until `dml` (step 29) moves it; its
records move here. The root keeps the orchestration: `Engine.mu`, lock
admission and waits, attach scans, the storage-read fences and publication.

## Inventory (measured at `c154e14fb`)

The 31 `partition*.go` files hold 7,169 lines, 2,003 of them clean. Three
of them (`partition_index_attach.go`, `partition_index_drop.go`,
`partition_unique.go`; 1,090 lines) are partitioned-index code and go to
`index` in 23a. The other 28 files hold 6,079 lines: 1,613 clean today and
about 3,140 once the 22a coverage cut, the 11e preparation seam, the index
23a cuts and the 25a cuts below are applied (simulated with `reach -cut`;
line counts per declaration, approximate).

| Sub-area | Files | Lines | Clean | After cuts |
|---|---|---:|---:|---:|
| Core, bounds, routing | `partition.go`, `partition_bind.go`, `partition_hash_bounds.go`, `partition_insert.go`, `partition_query.go`, `partition_routine.go` | 1,144 | 391 | 517 |
| CREATE, ATTACH, DETACH, DROP, locks | `partition_create.go`, `partition_inherit.go`, `partition_attach.go`, `partition_attach_scan.go`, `partition_attach_schema.go`, `partition_detach.go`, `partition_drop.go`, `partition_scope.go`, `partition_exclusion.go` | 2,006 | 247 | 331 |
| Proofs and retained checks | `partition_proof.go`, `partition_retained_checks.go` | 604 | 339 | 573 |
| Dump, replay, remap | `partition_dump.go`, `partition_dump_events.go`, `partition_invalid_dump.go`, `partition_constraint_dump.go`, `partition_unique_dump.go`, `partition_remap.go`, `partition_replacement.go`, `partition_check_replay.go`, `partition_unique_replay.go`, `partition_using_index_replay.go`, `partition_detach_restore.go` | 2,325 | 636 | 1,722 |
| **Total** | 28 files | 6,079 | 1,613 | about 3,140 |

The 22a coverage cut alone makes 1,171 lines of the dump code clean
(`partitionDumpEvents` 478 lines, `partitionInvalidDumpEvents`,
`partitionChildSQL`, `partitionDumpPredecessors`, the replay readiness
predicates, `partitionConstraintReplay` renames, ...).

### Clean today, or after the cuts (moves in 25b)

- **Bounds and routing:** `comparePartitionTuple`, `partitionContains`,
  `partitionRangeContains`, `partitionEvaluatedKey`,
  `partitionPhysicalDescendants`, `validatePartitionDescriptor`
  (`partition.go`); `partitionAccepts` and `partitionRowKey` after the
  recipe capability (25a step 3); `preparePartitionBounds`,
  `partitionGeneratedError`; `compareHashPartitionBound`,
  `hashPartitionOverlap`, `validHashPartitionBound`;
  `capturePartitionQueryPrefix`; the routing records `partitionInsertState`
  (with `rowFor`) and `partitionInsertGroup`, which `dml` will use.
- **Recipes:** `partitionRoutineIdentity`, `evaluatePartitionRecipe` (after
  25a step 3 and 11e), the slot struct `partitionScope` with
  `dropPrograms` and `releaseValidationView`.
- **CREATE, ATTACH, DETACH, DROP:** `partitionBoundary`,
  `partitionTupleSQL`; `partitionAttachEligibility`,
  `partitionAttachKindError`, `partitionAttachSchema` (after 25a step 2);
  `partitionAttachScan`, `partitionAttachRowConstraint`;
  `partitionDetachKindError`, `partitionDetachMembership`;
  `partitionDependsOn`; `partitionExclusionAddActions`;
  `partitionAppendCompatible`, `partitionKeyUsesAttribute`,
  `partitionWriteExtensionBoundary`.
- **Proofs:** `partitionProof` and its methods, `partitionProofGoal`,
  `partitionProofAnd`/`Or`/`Tuple`, `partitionProofSame`,
  `partitionCheckNonNull`, `partitionConstraintProven` (after 25a step 2).
- **Retained checks:** `partitionRetainedChecks` with `forRow`,
  `partitionCheckRecipeAffected`, `partitionRetainedAttributeChanged`,
  `partitionRetainedCheckAffected`.
- **Dump and replay:** the dump SQL builders (`partitionChildSQL`,
  `partitionKeySQL`, `partitionAttachDumpSQL`, `partitionTupleDumpSQL`,
  `hashPartitionBoundSQL`, `partitionDumpNeedsAttach`,
  `partitionDumpPredecessors`), the dump events (`partitionDumpEvents`,
  `partitionDumpIndexBacking`, `partitionDumpLateIndexTables`,
  `partitionInvalidDumpEvents` and the `partitionPreservedReplay*`,
  `partitionReplayIndexReady`, `partitionReplayNameCompare`,
  `partitionReplayStem` helpers), `partitionInvalidReplay`,
  `partitionConstraintReplay` with its renames,
  `partitionConstraintDumpCatalog`, `partitionIndexDumpAttachmentSQL`, the
  virtual-unique replay records (`partitionConstraintCreator`,
  `partitionReplayOnlyExclusion`, `partitionReplayTopologyReady`,
  `partitionVirtualUniqueDefinitionSQL`,
  `partitionVirtualUniqueMembershipReady`, `partitionExclusionDefaultBefore`);
  the Restore replay records `partitionUniqueReplay`,
  `partitionUsingIndexReplay`, `partitionReplayTeardown`,
  `partitionCheckReplayActions`; the remap repair records
  `hasHashPartition`, `partitionReplacementRow`.

### Not partition code (moves elsewhere)

| Declarations | File | Goes to |
|---|---|---|
| `partitionRowHistory` (with `nextRow`), `partitionNeedsRowHistory` | `partition_retained_checks.go` | `index` (23a; index snapshots read them) |
| `partitionReplayUsingIndex`, `partitionReplayVirtualUniqueRoot`, `partitionReplaySeparateConstraint` | `partition_invalid_dump.go`, `partition_unique_dump.go` | `index` (23a; table NOT NULL dump code and `dumpPredecessor` use them, and `table` sits below `partition`) |
| `partitionHashReplayTarget` | `partition_dump.go` | `table` (24b; only `table_dump.go` uses it, and it needs nothing from the root) |
| `addDumpDependency`, `dumpEventDependsOn` | `partition_dump_events.go` | `dump` (18b; graph operations on dump events, used by 14 table, inheritance and type dump sites) |
| `remapPartitionDescriptor` | `partition_remap.go` | `stored` (22c; the catalog-wide reowner calls it, and `stored` sits below `partition`) |
| `hierarchyRelation`, `samePartitionColumnUsage` | `partition.go`, `partition_attach_schema.go` | `catalog` (5i) |
| the partitioned-index files | see above | `index` (23a) |

The table prompt relies on these two rows: 23a moves
`partitionReplaySeparateConstraint` into `index`, and 24b takes
`partitionHashReplayTarget`, so `table` never imports `partition`.

### Dirty, and why

- **Handlers on `Engine`** (27 declarations): `attachPartitionLocked`
  (210 lines), `detachPartitionLocked`, `stagePartitionDetach`,
  `schemaPartitionTableLocked`, `schemaPartitionChildLocked` (175),
  `bindPartitionKeys`, `executePartitionExclusionAddLocked`,
  `partitionAttachAuthorization`, `partitionAttachRelation`,
  `partitionAttachName`, `partitionAttachCapture`,
  `partitionParentAuthorization`, `partitionReadsValidLocked`; the Restore
  executors `beginPartitionCheckReplay`, `beginPartitionUniqueReplay`,
  `executePartitionUniqueReplayLocked`, `beginPartitionUsingIndexReplay`,
  `capturePartitionReplayTeardown`, `validatePartitionReplayTeardown`,
  `repairRemappedHashPartitions`.
- **On `StatementScope`** (19 declarations): the INSERT routing methods
  (`capturePartitionInsertLocked`, `validatePartitionInsert`,
  `validateTargetPartitionConstraint`, `partitionInsertionGroup`,
  `partitionStoredRow`, `checkInsertUniqueRow`, `applyInsertValidated`);
  lock admission (`acquirePartitionLock`, `acquirePartitionAncestors`,
  `acquirePartitionDescendants`, `acquirePartitionDefinitionDefault`,
  `acquirePartitionDropLocks`, `observePartitionRelationLocked`,
  `partitionCaptureCatalog`); `partitionAttachFrontier`; the slot accessors
  `partitions`, `partitionsOrZero`, `setPartitionValidationView`.
- **Holding root structs:** `pendingPartitionDefinition` holds
  `*CatalogStage` and an `AuthorizationView`; `partitionInsertState`'s
  `missingPartition` reads the search path through the engine;
  `partitionConsumerBoundary`, `partitionDefaultReplayAllowed`,
  `partitionCheckReplayAllowed`, `partitionUniqueReplayAllowed`,
  `partitionUsingIndexReplayAllowed` and `partitionConstraintReplayImport`
  read the engine's Restore and authorization state;
  `dropProvider.removePartitionHierarchyLocked` is drop-pipeline code;
  `partitionReadFence.currentLocked` is a fence check.
- **Through the scope only for a context:** `partitionProof.scope`,
  `partitionConstraintProven(scope, ...)` and
  `partitionAttachSchema(ctxScope, ...)` use the scope only for the
  operation context and the input environment (25a step 2).

### State

- **`StatementScope`:** `partitionsSlot *partitionScope`
  (`partition_routine.go`: `programs`, `validationView`); in the embedded
  `statementMutation`, `partitionInsert *partitionInsertState` and
  `partitionRelations`. `partitionScope.dropPrograms` is also called from
  `sql_cursor_hold.go`, and the SQL routine scratch saves the validation
  view.
- **`CatalogStage`:** `partitionReads []partitionReadFence`
  (`partition_create.go`), appended by `partition_create.go` and
  `partition_attach_scan.go`, checked by `partitionReadsValidLocked`
  (`catalog.go`, three sites; `temporary_session.go`) and by the
  `fenceStorageRead` fence in `catalog_message.go`. `catalog_message.go`
  (line 249) and `catalog_overlay_indexes.go` (line 90) build the same
  fence for journal and overlay-index reads, so it is a generic
  storage-read fence, not partition state.
- **Operation:** `restoreReplay` (`restore_replay.go`) holds
  `partitionAttach`, `partitionCheck`, `partitionUsingIndex` and
  `partitionUnique`: the leased Restore replay targets.

### Outside users

`frontier` IN over the 28 files: 209 production and 205 test uses. By
using area: Dump/Restore driver 29, table 24, DML 21, statement dispatch
18, catalog orchestration 17, views 8, inheritance 8. The root dispatch
(`execute.go`, `not_null.go`, `temporary_table.go`) calls the handlers;
the DML files call the routing methods; `relationDefiningAuthorityCompatible`
(`materialized_refresh.go`, a pure catalog comparison) is used by
`partition_scope.go` and `relation_scope.go`.

## Part A

### 25a — In place (linear item 90)

1. **Re-measure** with `reach` and `frontier` over the 28 files. If the
   totals differ from this file by more than about 10%, update the
   inventory first and say so in the report.
2. **Context instead of the scope.** `partitionProof.scope` becomes two
   fields, the operation context and the input environment (they are all
   it reads: `partition_proof.go:190`, `:312`).
   `partitionConstraintProven` and `partitionAttachSchema` take
   `(ctx context.Context, input sqltype.InputEnvironment, ...)` instead of
   a `*StatementScope`. Gain: about 0.32k lines.
3. **Recipe capability (pattern 3).** `evaluatePartitionRecipe` reaches the
   scope for five things only: the command identity (`DomainPhase`), the
   prepared domain checks, the referential actor (`foreignActionAuth`'s
   current user), the program cache (`partitionsOrZero().programs`) and
   the consumed routine programs (`scope.routinePrograms`). Declare a
   small root interface with one method each (for example
   `partitionRecipeHost`), implement it on `statementHost`, and recover it
   from `local.LiveHost()` with one checked type assertion, as
   `scopeOf` does today. The routine admission part of the function
   (`routineAdmissionAuthority`) becomes clean with 11e (item 75); until
   then it stays dirty.
4. **Renames** (one commit, moving whole declarations only):
   - `partitionReadFence` → `storageReadFence` and
     `CatalogStage.partitionReads` → `storageReads`; `partitionReadsValidLocked`
     → `storageReadsValidLocked`. They are generic storage-read fences and
     stay in the root; the fence kind stays `fenceStorageRead` in
     `catalog_fence.go`.
   - `partition_scope.go` → `partition_locks.go` (it is lock admission).
   - `partition_inherit.go` → `partition_create_child.go`
     (`schemaPartitionChildLocked` creates a partition child).
   - Move the declarations of "Not partition code" that belong to `index`
     only if 23a has not done it yet (coordinate: 23a owns those moves).
5. **Docs commit** with the measured result. Write the clean lines after
   the cuts into the Status record.

### 25b — Create `partition` and move the clean set (linear item 91)

1. **Check prerequisites:** 24b (`table`), 23b (`index`), 22b (`stored`),
   21b (`regobj`), 18b (`dump`), 5i (`catalog` hierarchy helpers), 11e
   (`exec` preparation seam). If 18b has not landed, stop: the dump code
   cannot move.
2. **Re-run `frontier`** over the set. OUT may name only lower packages
   (`index`, `table`, `stored`, `regobj`, `dump`, `analyze` types and pure
   functions, the core packages) and `sqlError` (local `errors.go` copy).
   Anything else: stop and list it.
3. **Move** with the `movex` approach into `internal/engine/partition`
   (package `partition`). Drop the stutter where it reads badly
   (`partitionProof` → `partition.Proof`, `partitionInsertState` →
   `partition.InsertState`, `partitionScope` → `partition.Scope`); list the
   final names in the Status record.
4. **What the root keeps naming:** the slot struct (`partition.Scope`; the
   `StatementScope` accessors stay in the root), the routing records (the
   root routing methods and later `dml` use them), the Restore replay
   records (the `restoreReplay` slot holds them).
5. **Imports.** Expected: `index`, `table`, `stored`, `regobj`, `dump`,
   `catalog`, `storage`, `visibility`, `exec`, `plan`, `expr`, `access`,
   `authorization`, `analyze` (types and pure functions), `ids`, `parser`,
   `sqltype`, `sqlstate`. Never the root, `view` or `dml`.
6. **No aliases;** qualify the root references in the same commit.
7. **Tests follow the code.** Unit tests that use only moved code move
   into `partition`; engine-fixture tests stay and use `partition.X`, or
   are deleted when `reference/` covers them (name the covering test).

## Part B

### 25c — Refine the host (docs only; linear item 92)

Measure the handlers of "Dirty, and why" again, starting from this sketch,
and write the final interfaces, the move set and the commit sequence into
this file. Wait for approval.

**`partition.Host`** — every method runs with `Engine.mu` held, never takes
or releases it, and never waits.

| Group | Methods (draft) | Replaces |
|---|---|---|
| Shared DDL contract | `ddl.Host` (20a) | the `AuthorizationView`, `*CatalogStage` and `op` parameters |
| Table creation | the `table.Host` methods that build a child table (24c) | the table machinery `schemaPartitionChildLocked` calls |
| Binding (the root builds the binders) | `BindPartitionKeys(auth, relation, spec)`, `BindPartitionBound(auth, parent, bound)` | `Engine.bindPartitionKeys`, `pendingPartitionDefinition.bindPartitionDatum` |
| Evaluation | `Evaluation() exec.Context` | the contexts of `evaluateBounds` and the attach checks |
| Fences | `RecordStorageRead(fence)` | appends to `CatalogStage.storageReads` |
| Index children | `index.Host` (23c), through `index` functions | `stagePartitionIndexChildren`, `stagePartitionIndexAttach` |

Questions to settle in 25c:

1. Attach scans (`partitionAttachValidateRows`, `partitionAttachFrontier`,
   `partitionAttachCapture`) read rows and may wait. Recommended: they stay
   in the root, and the package returns the scan jobs to run.
2. Does `partition.Host` embed `table.Host`, or does the root pass a
   child-table builder callback? A callback must not run under a lock it
   does not document.
3. Which of the `*ReplayAllowed` checks and `partitionConstraintReplayImport`
   (used by 17 table, inheritance and type Restore files) wait for the
   Restore hook table of step 30 (item 111), and which move here.

### 25d — Move the handler decisions (linear item 93)

Follow 25c. The decisions that move:

- CREATE TABLE ... PARTITION BY / PARTITION OF: the definition checks of
  `pendingPartitionDefinition` (`validateBounds`, `checkOverlap`,
  `checkHashOverlap`, `evaluateBounds`, the decisions of `finish`) and the
  descriptor decisions of `schemaPartitionTableLocked` and
  `schemaPartitionChildLocked`;
- ATTACH PARTITION: eligibility, the schema check, proof or scan, the
  partitioned-index attach (through `index`);
- DETACH PARTITION: `stagePartitionDetach`'s membership and constraint
  decisions;
- partition constraints added by ALTER TABLE
  (`executePartitionExclusionAddLocked`'s decisions).

**Stays in the root:** the orchestration of `attachPartitionLocked` and
`detachPartitionLocked` (`Engine.mu` windows, lock waits, scans,
publication); lock admission (`partition_locks.go`,
`partitionConsumerBoundary`); the authorization captures
(`partitionAttachAuthorization`, `partitionParentAuthorization`); the
storage-read fences; `dropProvider.removePartitionHierarchyLocked`; INSERT
routing execution until step 29; the Restore executors until step 30.

## Invariants

- **Routing.** The same partition is chosen for every row; "no partition of
  relation found for row" keeps its SQLSTATE, message and DETAIL; default
  partition rules are unchanged.
- **ATTACH.** The scan is skipped only when the constraint is proven, as
  today; the order of the attach checks and their errors is unchanged.
- **Locks.** ATTACH and DETACH keep their lock order (parent before child,
  the `acquirePartition*` order). Waits never run under `Engine.mu`.
- **Fences.** Storage-read fences are checked at publication in the same
  order (`fenceStorageRead` first in `catalog_message.go`).
- **Restore.** Hash partition repair after remap
  (`repairRemappedHashPartitions`) and the leased replay targets of
  `restoreReplay` behave as today.
- **Dump.** The partition dump-event graph and its order
  (`partitionReplayNameCompare`) stay byte-identical.
- **Slots.** `partitionScope.dropPrograms` still runs at Finish and when a
  cursor is held; the SQL routine scratch still saves the validation view.

## Targeted tests

- **Engine units:** `partition_*_test.go` (attach, detach, constraints,
  dump events, exclusion, hash, retained checks, routine authority,
  replay); grep `*_test.go` for every moved identifier. Run
  `partition_attach_transaction_test.go` and
  `partition_detach_transaction_test.go` with `RACE=1`.
- **Reference selections** (quick mode and `REFERENCE=1`):
  `make check RUN='^TestReference(RangePartition|HashPartition|PartitionConstraints|PartitionAttach|PartitionDetach|PartitionExclusion)'`,
  plus `'^TestReference(DumpRoundTrip|InsertConflict)'` for Dump and
  routing through ON CONFLICT.
- `PartitionExclusion*` has known `REFERENCE=1` oracle drift on `master`;
  compare with `master` before reporting a failure there.
- Never modify `reference/` or `smoke/`. Tests follow the code; they never
  keep code in the root.

## Documentation

- Create `internal/engine/partition/README.md` in 25b: what it owns, the
  routing and proof rules, the replay records, what stays in the engine,
  and a `## Testing` section with selectors moved out of
  `internal/engine/README.md`.
- Update the `index` and `table` READMEs where they mention partition code.
- `AGENTS.md`: add `partition` to the subpackage brace list; the file must
  not grow.
- Tick the item in [ENGINE-SPLIT-STATUS.md](ENGINE-SPLIT-STATUS.md) and
  update the step 25 cell of ENGINE-SPLIT.md §8.

## Done when

Part A: the bounds, routing records, proofs, retained checks, dump and
replay records live in `partition`, which imports nothing above it.
Part B: the CREATE ... PARTITION, ATTACH and DETACH decisions live there
too; the root keeps only the orchestration listed above. The checks in
§8.3 pass.

## Status

- [ ] 25a — In place: context cut, recipe capability, renames (linear item 90)
- [ ] 25b — `partition` created and clean set moved (linear item 91)
- [ ] 25c — Host refined (docs only; linear item 92)
- [ ] 25d — Handler decisions moved (linear item 93)

Outstanding aliases: none yet.

### Notes for later steps

- Step 29 (`dml`) moves INSERT routing execution
  (`capturePartitionInsertLocked`, `validatePartitionInsert`,
  `partitionInsertionGroup`, `partitionStoredRow`,
  `validateTargetPartitionConstraint`, `checkInsertUniqueRow`,
  `applyInsertValidated`, `missingPartition`) on top of the routing
  records moved here. Today `StatementScope.applyInsertValidated`
  (`partition_insert.go`) shadows `WriteStage.applyInsertValidated`; rename
  one of them when `dml` touches them.
- Step 30 (`control`) owns the Restore executors left in the root
  (`begin*Replay`, `executePartitionUniqueReplayLocked`,
  `repairRemappedHashPartitions`, the teardown capture and validation).
- Catalog 5i: `relationDefiningAuthorityCompatible`
  (`materialized_refresh.go`) is a pure catalog comparison used by
  `partition_scope.go` and `relation_scope.go`; it belongs in `catalog`.
