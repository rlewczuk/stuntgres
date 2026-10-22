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
  - 25d (item 93): 92, 89a and 89b (`table` 24d.1 and 24d.2).
- **Effort:** high for 25a and 25b; xhigh for 25c; high for 25d.
- **Session size:** one sub-step per session.
- **Linear items:** 25a = 90, 25b = 91, 25c = 92, 25d = 93. Items that wait
  for this step: 29b (108) needs 91, 29c (109) needs 93, 30b (112) needs 91.
  Fourth round (item 133): 25f = 140.
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
`partition_unique.go`; 1,090 lines) are partitioned-index code; 23a renamed
them `index_partition_*.go` and they move to `index` in 23b. The other 28 files hold 6,079 lines: 1,613 clean today and
about 3,140 once the 22a coverage cut, the 11e preparation seam, the index
23a cuts and the 25a cuts below are applied (simulated with `reach -cut`;
line counts per declaration, approximate).

| Sub-area | Files | Lines | Clean | After cuts |
|---|---|---:|---:|---:|
| Core, bounds, routing | `partition.go`, `partition_bind.go`, `partition_hash_bounds.go`, `partition_insert.go`, `partition_query.go`, `partition_routine.go` | 1,144 | 391 | 517 |
| CREATE, ATTACH, DETACH, DROP, locks | `partition_create.go`, `partition_create_child.go`, `partition_attach.go`, `partition_attach_scan.go`, `partition_attach_schema.go`, `partition_detach.go`, `partition_drop.go`, `partition_locks.go`, `partition_exclusion.go` | 2,006 | 247 | 331 |
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
  `partitionReplayTopologyReady`,
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
| `partitionReplayUsingIndex`, `partitionReplayVirtualUniqueRoot`, `partitionReplaySeparateConstraint`, `partitionReplayOnlyExclusion` | `partition_invalid_dump.go`, `partition_unique_dump.go` | `index` (23a moved them to `index_partition_replay.go`; table NOT NULL dump code and `dumpPredecessor` use them, and `table` sits below `partition`) |
| `partitionHashReplayTarget` | `partition_dump.go` | done: `index.PartitionHashReplayTarget` (23b moved it with the replay chain; `table`'s dump record calls it) |
| `addDumpDependency`, `dumpEventDependsOn` | `partition_dump_events.go` | `dump` (18b; graph operations on dump events, used by 14 table, inheritance and type dump sites) |
| `remapPartitionDescriptor` | `partition_remap.go` | done: `stored.RemapPartitionDescriptor` (22c; the catalog-wide reowner calls it, and `stored` sits below `partition`) |
| `hierarchyRelation`, `samePartitionColumnUsage` | `partition.go`, `partition_attach_schema.go` | `catalog` (5i) |
| the partitioned-index files | see above | `index` (23a) |

The table prompt relies on these two rows: 23a moves
`partitionReplaySeparateConstraint` into `index`, and 23b moved
`partitionHashReplayTarget` there too, so `table` never imports
`partition`.

### Dirty, and why

- **Handlers on `Engine`** (26 declarations): `attachPartitionLocked`
  (210 lines), `detachPartitionLocked`, `stagePartitionDetach`,
  `schemaPartitionTableLocked`, `schemaPartitionChildLocked` (175),
  `bindPartitionKeys`, `executePartitionExclusionAddLocked`,
  `partitionAttachAuthorization`, `partitionAttachRelation`,
  `partitionAttachName`, `partitionAttachCapture`,
  `partitionParentAuthorization`; the Restore
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
  `dropProvider.removePartitionHierarchyLocked` is drop-pipeline code.
  *(25a moved the generic `storageReadFence`, formerly
  `partitionReadFence`, to `storage_read_fence.go`.)*
- **Through the scope only for a context** (cut in 25a): `partitionProof`
  holds the operation context and input environment;
  `partitionConstraintProven(ctx, input, ...)` and
  `partitionAttachSchema(ctx, ...)` take them instead of the scope.

### State

- **`StatementScope`:** `partitionsSlot *partitionScope`
  (`partition_routine.go`: `programs`, `validationView`); in the embedded
  `statementMutation`, `partitionInsert *partitionInsertState` and
  `partitionRelations`. `partitionScope.dropPrograms` is also called from
  `sql_cursor_hold.go`, and the SQL routine scratch saves the validation
  view.
- **`CatalogStage`:** `storageReads []storageReadFence`
  (`storage_read_fence.go` since 25a), appended by `partition_create.go` and
  `partition_attach_scan.go`, checked by `storageReadsValidLocked`
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
(`execute.go`, `alter_table.go`, `create_table.go`) calls the handlers;
the DML files call the routing methods; `relationDefiningAuthorityCompatible`
(`materialized_refresh.go`, a pure catalog comparison) is used by
`partition_locks.go` and `relation_scope.go`.

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
   `scopeOf` does today. The routine admission part of the function is
   clean since 11e (item 75): it calls `exec.RoutineAdmissionAuthority`
   and records into an `exec.RoutineProgram`.
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
| Table creation | `table.Host` (24c: embed it; the child needs exactly the CREATE set) and the 24d.1 functions `NewRelation`, `StageCreateIdentities`, `StageInheritance`, ... over it; `BindColumnDefaults` with the zero temporary token, as today | the table machinery `schemaPartitionChildLocked` calls |
| Binding (the root builds the binders) | `BindPartitionKeys(auth, relation, spec)`, `BindPartitionBound(auth, parent, bound)` | `Engine.bindPartitionKeys`, `pendingPartitionDefinition.bindPartitionDatum` |
| Evaluation | `Evaluation() exec.Context` | the contexts of `evaluateBounds` and the attach checks |
| Fences | `RecordStorageRead(fence)` | appends to `CatalogStage.storageReads` |
| Index children | `index.Host` (23c: embedded through `table.Host`), through the 23d.1 `index` functions `StagePartitionIndexChild`/`Children`, `StagePartitionIndexAttach`, `PartitionIndexValidity` (over `index.ValidityTarget`) and `StagePartitionIndexDrop`/`StagePartitionConstraintDrop` (over `index.DropStage`) | `stagePartitionIndexChildren`, `stagePartitionIndexAttach` |

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

**25c refine (approved; measured at `116432550`).** `frontier` over the 17
root `partition_*.go` files left after 25b (output in
`.cswd/tmp/PG18-ENGINE-SPLIT-PARTITION-REFINE/frontier-partition.txt`): 56
declarations, about 120 engine edges. `schemaPartitionTableLocked`,
`schemaPartitionChildLocked`, `bindPartitionKeys`, the
`pendingPartitionDefinition` methods, `attachPartitionLocked` with its scan,
`detachPartitionLocked`, `stagePartitionDetach`,
`executePartitionExclusionAddLocked` and the replay begin/allowed functions
were read directly. Findings:

- **Most decisions run with nothing held.** Only the CREATE side runs under
  `Engine.mu` (`schemaPartitionTableLocked`, the child, key binding, inside
  `createTableLocked`'s window). `pendingPartitionDefinition.finish`, ATTACH
  and DETACH run unlocked: `bindPartitionDatum` takes `Engine.mu` once per
  datum, bound evaluation runs after it releases and may wait (sequence
  calls keep their admission), `validateBounds`, `checkOverlap`,
  `checkHashOverlap` and the default-row scan take no lock, and
  `stagePartitionDetach` replaces on the private stage without one. Only the
  index-children window (`finish`, ATTACH) takes `Engine.mu` again. The
  sketch's "every method under `Engine.mu`" therefore fits the CREATE side
  only; the other decisions need no host.
- **The child needs exactly the 24c CREATE set.** Every edge of
  `schemaPartitionChildLocked` is on `table.Host` or a 24d.1 function:
  `displaceAssociatedArrayLocked` (`DisplaceAssociatedArray`), `CanUse`
  (`AccessView`), `domainBases` (`AddDomainBase`),
  `newTableDescriptorLocked` (`table.NewRelation`), `creationACL`
  (`CreationACL`), `stageRowTypeLocked` (`StageRowType`),
  `stageCreateIdentities` (`table.StageCreateIdentities`), `bindCreateDefaults`
  (`BindColumnDefaults`, zero token), `bindCreateGenerations`
  (`BindGenerations`), `bindCreateChecks` (`BindChecks`),
  `assignDefinitionOIDsLocked` (`table.AssignDefinitionOIDs`),
  `captureLikeSource` (`(*table.Stage).CaptureLikeSource` over
  `TableStage()`), the inherited CHECK rendering (`ACLOutput` is not used:
  the renderer takes the statement authority's owner and roles, both on
  `AccessView(auth)`), `index.StageCreateUniques`/`StageCreateExclusions`.
- **Partition keys bind unlike index keys.** `bindPartitionKeys` binds with
  its own consumer name, no parameters, no `SimplifyConditional` or recipe
  flags, its own 42P17 texts, and renders with the statement authority, so
  `index.Host.BindIndexExpression` cannot serve it. The operator class
  binding is already `index.BindKey` over `index.KeyHost`.
- **Index children.** `ddlHost.ValidityTarget()` builds the same drop
  provider as `stagePartitionIndexValidity` (both `context.Background()`),
  so the index edges reach `index.StagePartitionIndexChild` and
  `index.PartitionIndexValidity` through `index.Host` (embedded through
  `table.Host`).
- **Not needed** (the sketch had them): `Evaluation() exec.Context` (the
  root builds the evaluation context from the scope and passes it as a
  value to functions that run unlocked); `RecordStorageRead` (fences stay in
  the root); `BindPartitionBound` (decision 2).

Decisions:

1. **`partition.Host` = `table.Host` plus one method**, on the shared
   `ddlHost` (new root `partition_host.go`, `var _ partition.Host =
   ddlHost{}`). Every method runs with `Engine.mu` held, never takes or
   releases it, and never waits:
   - `BindKeyExpression(auth ddl.Authority, relation *catalog.Relation, raw
     parser.Expr, input sqltype.InputEnvironment) (*catalog.DerivedIndexKey,
     []int, error)`: builds the binder (rule 6), binds with the
     "partition key expression" consumer and no parameters, preflights, and
     returns the key with `Definition`, `Execution` and `Attributes` set,
     plus the source's used slots. The volatility, constant and
     generated-column checks and the `stored.ScalarRenderer` label move to
     `partition` (owner and roles from `AccessView(auth)`), in today's
     order.
2. **`partition.BoundBinder`**, self-locking like `index.WriteHost`:
   `BindBound(datum parser.PartitionDatum, key catalog.PartitionKeyEntry)
   (expr.Expr, error)`. Callers hold nothing; it takes `Engine.mu` for one
   window (the authorization epoch and revision recheck, the binder, the
   storage coercion), releases it, and never waits. A root adapter over the
   scope and the pending definition implements it
   (`bindPartitionDatum`'s body). `EvaluateBounds` evaluates the result
   after it returns, unlocked, as today.
3. **Embed, no callback** (question 2). `partition.Host` embeds
   `table.Host`; the child calls the 24d.1 functions directly.
4. **Attach scans** (question 1). `partitionAttachFrontier` (lock waits
   between proofs) and `partitionAttachCapture` (`Engine.mu`, fences,
   validation views) stay in the root and produce the jobs. The row checks
   take no lock and never wait, so they move: `ValidateAttachRows` (from
   `partitionAttachValidateRows`) and CREATE's default-row check
   (`CheckDefaultRows`, from `finish`), both over a root-built
   `*exec.Context` and the captured `*visibility.View`.
5. **Restore** (question 3). The trust checks read the engine's Restore and
   role state (`importTargets`, `roles.published`, `catalog.state`,
   `engineOf`, the operation replay slot) and stay in the root for control
   30: `partitionConstraintReplayImport`, `partitionDefaultReplayAllowed`,
   `partitionAttachAuthorization`, `partitionParentAuthorization`. The
   begin and `*Allowed` functions stay root hook wrappers. Their pure
   cores move, as table 24c decision 7 did: `CaptureUniqueReplay(view
   catalog.Read, raw *parser.AlterTable, table *catalog.Relation)
   (*UniqueReplay, error)` (`beginPartitionUniqueReplay` after resolution),
   `(*UniqueReplay).Matches(view, raw, table) bool`,
   `CaptureUsingIndexReplay(view, raw, table) *UsingIndexReplay`,
   `(*UsingIndexReplay).Matches(view, raw, table) bool` and
   `CheckReplayTarget(view, d) bool` (the CHECK replay shape). The
   teardown capture and validation stay whole: they read storage.
6. **What moves in 25d** is listed below. One sub-step (about 1.0k lines);
   no split.
7. **Hand-offs.** DML 29b/29c: `partition` exports no write-side host;
   routing execution moves to `dml` on top of `InsertGroup`, `RowKey`,
   `Contains` and `EvaluateRecipe` (`RecipeHost`), and the child selection
   loop of `validatePartitionInsert` is a pure core that step 29 can move
   into `partition`. Control 30: the four trust checks of decision 5 repeat
   one test and are one root helper candidate.

### 25d — Move the handler decisions (linear item 93)

Follow the 25c refine above. Needs 92, 89a and 89b (`table.Host`,
`StageCreateTable`, `NewRelation`, `StageCreateIdentities` and the CREATE
binders on `ddlHost`). Moved names are capitalized; no aliases.

**Move set.** Into `partition`:

- `host.go`: `Host`, `BoundBinder`.
- `definition.go`: the record `Definition` (`Parent`, `Child`, `Bound`,
  `Raw`, `DefaultLeaves []ids.ObjectRef`, `Unsupported`); the root
  `pendingPartitionDefinition` embeds it and keeps the stage, the
  authorization and the data view. `WithChild(parent, child
  ids.ObjectAddress, defaultBound bool) (*catalog.Relation, error)` and
  `WithoutChild(parent, child)` (the edge edit and revision bump shared by
  `finish`, ATTACH, DETACH and `removePartitionHierarchyLocked`);
  `CheckDefaultConflict(view, parent, bound, name, pos)` (shared by CREATE
  and ATTACH); `CheckSupported(*Definition) error` (the 0A000).
- `create.go` (under `Engine.mu`): `RejectForeignKeys(*parser.CreateTable)`
  (the first 0A000); `StageCreate(h Host, auth ddl.Authority, s
  *parser.CreateTable, namespace ids.ObjectAddress, parent
  *catalog.Relation, owner authorization.PrincipalRef, input
  sqltype.InputEnvironment, foreignTargets []ids.ObjectRef) (*Definition,
  error)`: the "is not partitioned" 42P17, the child (unexported
  `stageChild`, from `schemaPartitionChildLocked`) or
  `table.StageCreateTable`, the stage lookup, `Recapture` over `StageRead`,
  `BindKeys`, the NO INHERIT 42P16, `PrepareBounds`, the parent and default
  leaves through `AddDomainBase`, the default conflict, the unsupported
  flag. `BindKeys(h Host, auth, relation, raw *parser.PartitionKey, input)
  (*catalog.PartitionDefinition, error)` (from `bindPartitionKeys`, over
  `BindKeyExpression` and `index.BindKey`).
- `bounds.go`/`hash_bounds.go` (nothing held): `EvaluateBounds(ctx,
  *Definition, BoundBinder, *exec.Context) error`, `ValidateBounds(ctx,
  input, *Definition) error`, `CheckOverlap(ctx, input, base catalog.Read,
  *Definition) error` (range and hash), `CheckCoverage(stage ddl.Stage,
  child *catalog.Relation) error` (the unique and exclusion coverage of
  `finish`), `CheckDefaultRows(ctx, *exec.Context, rows *visibility.View,
  base catalog.Read, *Definition) error`.
- `attach.go`: `CheckAttachTarget(parent, replay bool) error` (kind error,
  then the hash 0A000), `ValidateAttachRows(ctx, *exec.Context, rows
  *visibility.View, view catalog.Read, parent, bound, jobs []AttachScan)
  error`, `AttachDescriptors(parent, child, bound) (nextParent, nextChild
  *catalog.Relation, error)`; `StageIndexChildren(h index.Host, parent
  *catalog.Relation, child ids.ObjectAddress, attach bool) error` (under
  `Engine.mu`: the declaration-order loop over `StageBase()`, re-reading the
  child through `StageRead()`; CREATE allows incomplete virtual children,
  ATTACH does not and runs `index.PartitionIndexValidity` over
  `ValidityTarget()`).
- `detach.go` (nothing held): `StageDetach(stage ddl.Stage, base
  catalog.Read, parent, child *catalog.Relation) error`.
- `exclusion.go` (under `Engine.mu`): `StageExclusions(h index.Host, auth
  ddl.Authority, input sqltype.InputEnvironment, table *catalog.Relation,
  raw *parser.AlterTable) error` (the clone, `Revision()` and the
  `index.StageExclusion` loop).
- `replay.go`: the cores of decision 5.

**Stays in the root:** `createTableLocked`'s window and the root `finish`,
which sequences the moved checks in today's order (coverage, the
parent-less 0A000, evaluation, bounds, overlap, 0A000, default rows, 0A000,
revision, `Replace`, index children); the parent resolution, Restore
parent authority, locked-identity and owner checks before `StageCreate`,
and the `DefaultLeaves` fences and data views after it; the orchestration
of `attachPartitionLocked` and `detachPartitionLocked` (`Engine.mu`
windows, lock waits, frontier, capture, stage `Replace`, validation and
publication); `executePartitionExclusionAddLocked`'s windows; lock
admission (`partition_locks.go`, `partitionConsumerBoundary`); the
authorization captures and trust checks; the storage-read fences;
`dropProvider.removePartitionHierarchyLocked` (calls `WithoutChild`);
INSERT routing until step 29; the Restore executors, teardown and
`repairRemappedHashPartitions` (they stay root; control 30b). The root wrapper
`stagePartitionIndexChild` is deleted; `stagePartitionIndexChildren` stays
for the unique-replay executor.

**Commit sequence.**

1. Pure helpers: `Definition` (the root struct embeds it), `WithChild`,
   `WithoutChild`, `CheckDefaultConflict`, `CheckSupported`.
2. Seam: `partition.Host`, `BoundBinder`; root `partition_host.go` with the
   assertion, `BindKeyExpression` and the bound-binder adapter. Unit test
   first, observed failing: `BindKeyExpression` through the host returns
   the derived key and used slots that today's expression branch builds,
   and rejects a volatile key with the same 42P17.
3. Port in place: root functions over the host, the records and plain
   values for the decisions above, naming no `Engine`, `CatalogStage`,
   `AuthorizationView` or `StatementScope`; the old entry points become
   the orchestration listed under "Stays".
4. Move into `partition` with `movex` and qualify; no aliases. Tests follow
   the code (`partition_detach_test.go` calls `partition.StageDetach`).
5. Docs: the `partition` README (host, entry points, lock contracts, what
   stays), this Status, the §8 row, the status page; `AGENTS.md` unchanged.

Proof: the engine units of "Targeted tests"
(`partition_attach_transaction_test.go` and
`partition_detach_transaction_test.go` also with `RACE=1`), the reference
selections in quick and `REFERENCE=1` mode, and quickcheck. Check that the
first-error order of CREATE ... PARTITION OF and ATTACH is unchanged on the
reference cases.

### 25e — Consumer boundary and hash repair (linear item 127)

Planned by the end-state review (linear item 114, ENGINE-SPLIT.md §2.8 and
§6 "Third round"), measured at `17e01ca8b`. Needs —.

- `partitionConsumerBoundary` (`partition_locks.go:17-79`) waits on
  nothing: it becomes a `partition` function with its four
  `*ReplayAllowed` trust checks passed in as one callback (cut tested).
- `partitionCaptureCatalog` and the ancestor walk (`:165-173`).
- The routing and validation core of `repairRemappedHashPartitions`
  (`partition_replacement.go:24-131`; it uses `InsertState.MissingPartition`,
  moved in 29d.1); the install loop stays.
- Correct the README, which lists the boundary under lock admission.

Verify with `make build`, the targeted tests below plus the unit tests of
the touched files, the quick and `REFERENCE=1` selections for the area, and
quickcheck; no behavior, error text or error-order change, no aliases.

### 25f — Attach frontier, insert capture and constraint drop (linear item 140)

Planned by the closing review (linear item 133, ENGINE-SPLIT.md §2.9 and
§6 "Fourth round"), measured at `4c05da8fe`. Needs —.

- `partitionAttachFrontier` (`partition_attach_scan.go`), the proof
  walk; the `lockChildren` branch becomes a callback that waits with
  nothing held, as today.
- The layout and ancestor walk of `capturePartitionInsertLocked`
  (`partition_insert.go`), with the snapshot captures as callbacks run
  under the caller's `Engine.mu`.
- The per-action body of `executePartitionConstraintDropLocked`
  (`index_partition_drop.go`) over `index.DropStage` and
  `table.NotNullCandidate`, returning sorted removal roots; the root keeps
  `stageExpressionDependents`.
- The duplicate ancestor walk in `partition_drop.go` becomes
  `partition.Ancestors`.

`partition` then imports `session` directly for `*session.Operation`
(already a transitive dependency, no cycle).

Verify with `make build`, the unit tests of the touched root files and
the target package, quickcheck, and the quick and `REFERENCE=1` selections
for ATTACH/DETACH PARTITION, partitioned INSERT and partitioned constraint drops; no behavior, error text, error-order or lock-order change, no
aliases. Re-run `reach` on the moved set first: sizes are approximate.

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

- [x] 25a — In place: context cut, recipe capability, renames (linear item 90)
  (`22bba0e50`, `47d732220`, `bbe3e3cc1`, then the docs commit) · selected
  unit and dual-mode proof and quickcheck passed; racecheck/finalcheck
  skipped at user request
  - Re-measured at `e68b939e8` (rebased onto `c02c9d97e`) with `reach`
    over `^partition.*\.go$`: 107 clean and 80 dirty declarations (the
    planning snapshot had 86/100). The difference is the 22a coverage cut
    and the 5i/18b moves (`hierarchyRelation`, `samePartitionColumnUsage`, `addDumpDependency`,
    `dumpEventDependsOn` are gone), as the inventory expected; no
    inventory rewrite. After 25a the 28 files hold 6,053 lines, 3,104 of
    them clean (2,668 before; the inventory's "about 3,140" also counts
    the index 23a cuts). Outputs in
    `.cswd/tmp/PG18-ENGINE-SPLIT-PARTITION-IN-PLACE/`.
  - `22bba0e50` (step 2, 319 lines clean): `partitionProof` holds `ctx`
    and `input`; `partitionConstraintProven(ctx, input, ...)`.
    `partitionAttachSchema(ctx, ...)` takes only the context: it never
    reads the input environment (deviation from step 2's wording).
  - `47d732220` (step 3, 117 lines clean: `evaluatePartitionRecipe`,
    `partitionRowKey`, `partitionAccepts`): `partitionRecipeHost`
    (`partition_routine.go`) with `CommandIdentity`,
    `PreparedDomainChecks`, `ReferentialActor`, `PartitionPrograms` and
    `RecordPartitionPrograms` (writes the program cache and the consumed
    routine program together, as before), implemented on `statementHost`
    in the new `statement_host_partition.go`; recovered from
    `local.LiveHost()` with one checked type assertion. The method names
    are exported so 25b can move the interface unchanged (as
    `partition.RecipeHost`).
  - `bbe3e3cc1` (step 4): `storageReadFence`, `CatalogStage.storageReads` and
    `storageReadsValidLocked` live in the new root file
    `storage_read_fence.go`; `partition_scope.go` is `partition_locks.go`,
    `partition_inherit.go` is `partition_create_child.go`. The index
    declarations of "Not partition code" were left to 23a, which owns them
    and had not started.
  - Tests changed only in call shape: `partition_attach_definition_test.go`
    and `partition_attach_eligibility_test.go` pass a context (and an
    empty input environment) instead of a `StatementScope`;
    `partition_attach_publication_test.go` and
    `driver_batch_catalog_provider_test.go` use the fence's new name. None
    moved or deleted.
- [x] 25b — `partition` created and clean set moved (linear item 91)
  (`5ed4b040c`, `f3e01d002`, `b7acfe47e`, `917899609`, then the docs commit) ·
  selected unit and dual-mode proof and quickcheck passed (`REFERENCE=1`
  `PartitionExclusion*` fails identically on `master`: known oracle drift);
  racecheck/finalcheck skipped at user request
  - Re-measured at `bdba4968a` with `reach` over `^partition.*\.go$`: 85 clean
    declarations in 25 files (10 wholly clean, 15 mixed; `partition_attach.go`
    and `partition_create_child.go` all dirty). `frontier` OUT over the clean
    set: `sqlError` and the root `dumpPredecessor` only; no inventory rewrite.
    Outputs in `.cswd/tmp/PG18-ENGINE-SPLIT-PARTITION-PACKAGE/`.
  - `5ed4b040c` (seam): `partitionDumpEvents`, `partitionDumpPredecessors` and
    `partitionConstraintCreator` take a `dump.Resolver` (as
    `routine.SQLRoutineDumpEvents` does); `partitionConstraintDumpCatalog`
    stores it in the replay record for `membershipRenames`. Root callers pass
    `dumpResolver{}`.
  - `f3e01d002` (movedecl staging, 15 `ptmv_*.go` files), `b7acfe47e` (export
    in place, 84 expspec/movex rows), `917899609` (move): 22 files, about 3.1k
    lines, in `partition`, qualified with `movex -qual partition`; two root
    locals named `partition` became `part`; `partition/errors.go` copies
    `sqlError`. No aliases. Imports: `analyze`, `authorization`, `catalog`,
    `dump`, `exec`, `expr`, `ids`, `index`, `plan`, `regobj`, `stored`,
    `table`, `typedef`, `visibility`, `parser`, `sqlstate`, `sqltype`; only
    the root imports `partition`.
  - Final names (the `partition` stutter dropped): `ValidateDescriptor`,
    `CompareTuple`, `EvaluatedKey`, `RowKey`, `Contains`, `RangeContains`,
    `Accepts`, `PhysicalDescendants`, `PrepareBounds`, `GeneratedError`,
    `ValidHashBound`, `HashOverlap`, `CompareHashBound`, `InsertGroup`,
    `CaptureQueryPrefix`; `RoutineIdentity`, `RecipeHost`, `EvaluateRecipe`,
    `Scope` (`Programs`, `ValidationView`, `DropPrograms`,
    `ReleaseValidationView`); `Boundary`, `TupleSQL`, `AttachEligibility`,
    `AttachKindError`, `AttachSchema`, `AttachScan`, `AttachRowConstraint`,
    `DetachMembership`, `DetachKindError`, `DependsOn`, `ExclusionAddActions`,
    `AppendCompatible`, `KeyUsesAttribute`, `WriteExtensionBoundary`;
    `ConstraintProven`, `ProofGoal`, `ProofTuple` (the proof state is the
    unexported `prover`); `RetainedChecks` (`ForRow`), `CheckRecipeAffected`;
    `DumpPredecessors`, `KeySQL`, `DumpNeedsAttach`, `AttachDumpSQL`,
    `ChildSQL`, `DumpEvents`, `ConstraintReplay`, `ConstraintDumpCatalog`,
    `IndexDumpAttachmentSQL`, `InvalidReplay`, `InvalidDumpEvents`,
    `VirtualUniqueDefinitionSQL`, `HasHashPartition`, `ReplacementRow`; the
    Restore replay records `UniqueReplay`, `UsingIndexReplay`,
    `ReplayTeardown` and `CheckReplayActions` share `replay.go`.
  - Deviation: `partitionInsertState` (with `rowFor` and `missingPartition`)
    stays in the root: it holds an `AuthorizationView` and reads the search
    path through the engine. Only `InsertGroup` of the routing records moved.
  - Root after 25b: 17 `partition_*.go` files, 56 dirty declarations, 2,841
    lines; `reach` finds nothing clean left.
  - Tests: none moved or deleted. Every partition unit test builds an engine
    fixture (`partitionConstraintDescriptors`, `hashPartitionUnitEngine`, ...)
    and stays in the root, naming `partition.X`;
    `partition_routine_authority_test.go` renamed one local `partition` to
    `part`. The `TestPartitionRoutineOriginalAuthority` selectors moved from
    `internal/engine/README.md` to the package README.
- [x] 25c — Host refined (docs only; linear item 92) (the docs commit) ·
  docs only, no checks needed
  - Measured at `116432550` (see the 25c refine above): only the CREATE
    side runs under `Engine.mu`; `finish`, ATTACH and DETACH decisions run
    with nothing held.
  - `partition.Host` = `table.Host` + `BindKeyExpression`, on `ddlHost`;
    the self-locking `BoundBinder` for bound datums; the sketch's
    `Evaluation()`, `RecordStorageRead` and `BindPartitionBound` are not
    needed. Attach frontier and capture stay root, row checks move; the
    trust checks stay root for control 30, the replay cores move. 25d stays
    one sub-step.
- [x] 25d — Handler decisions moved (linear item 93) (`f8d726111`,
  `101986429`, `c7de8cd5b`, `b2cd9d59c`, then the docs commit) · selected
  unit, `RACE=1` and dual-mode proof and quickcheck passed;
  racecheck/finalcheck skipped at user request
  - `f8d726111` (pure helpers): `Definition` in `definition.go`; the root
    `pendingPartitionDefinition` embeds it and keeps the stage, the
    authorization and the data view. `WithChild`/`WithoutChild` (revision
    check, edge edit, bump) serve `finish`, ATTACH, DETACH and
    `removePartitionHierarchyLocked`; `CheckDefaultConflict` and
    `CheckSupported` are the shared 42P17 and 0A000.
  - `101986429` (seam): `partition.Host` and `BoundBinder` in `host.go`;
    root `partition_host.go` holds `ddlHost.BindKeyExpression` (binder,
    bind, preflight; returns the derived key and `Sources[0].Uses`) and the
    `partitionBoundBinder` adapter (`bindPartitionDatum`'s body). Deviation:
    the volatility, constant and generated-column 42P17s live in `BindKeys`
    (decision 1), so the new `TestPartitionHost*` units assert the returned
    key, slots and an unrejected volatile execution;
    `reference/tables/range_partition_diagnostics_test.go` covers the 42P17
    texts.
  - `c7de8cd5b` (port in place, `partmv_*.go`) and `b2cd9d59c` (move,
    `.cswd/tmp/PG18-ENGINE-SPLIT-PARTITION-DECISIONS/c4.py`): the 25d move
    set, with the hash overlap as unexported `checkHashOverlap` and the
    replay `Matches` as methods on `UniqueReplay`/`UsingIndexReplay`.
    `frontier` OUT over the staging set named only `sqlError`; the
    string-literal multiset changed only in import paths. New imports:
    `ddl`, `sequence`.
  - Notes: the root `finish` keeps its name
    (`range_partition_publication_test.go` matches the frame
    `(*pendingPartitionDefinition).finish`); `CheckDefaultRows` reads the
    input environment from the evaluation context (the same `scope.input`);
    `AttachDescriptors` returns a copy of the candidate instead of editing
    it in place; `StageDetach` keeps the child's revision check before
    `WithoutChild`; `stagePartitionIndexChild` is gone (the constraint tests
    call `index.StagePartitionIndexChild` over a `ddlHost`),
    `stagePartitionIndexChildren` stays for the unique-replay executor;
    `partition_bind.go`, `partition_create_child.go` and
    `partition_hash_bounds.go` are gone.
- [x] 25e — Consumer boundary and hash repair moved (linear item 127)
  (`70351c00d`, `bcef8bbed`, `b7a0239e7`, then the docs commit) ·
  targeted units (the growth test also `RACE=1`), quickcheck and the quick
  and `REFERENCE=1` partition/dump selections passed (`REFERENCE=1`
  `PartitionExclusion*` fails identically on `master`: known oracle
  drift); racecheck/finalcheck skipped at user request
  - Re-measured at `c40b97c0f`: the plan's line numbers had drifted (the
    ancestor walk is `partition_locks.go:177-185`), the code had not.
  - `70351c00d`: `partition.ConsumerBoundary(statement, d, replayAllowed)` in
    `locks.go`. The four trust checks are one callback, the root
    `partitionReplayAllowed(op, auth)`: ALTER TABLE runs the CHECK, USING
    INDEX and UNIQUE replay checks in their old short-circuit order, ALTER
    ... DEFAULT the default replay check. It is called at the same two
    points of the old body, so the first-error order is unchanged. The
    three replay unit tests call `partition.ConsumerBoundary` with the
    root callback.
  - `bcef8bbed`: `partition.CaptureCatalog(read, admitted)` takes the scope's
    `PartitionRelations` (three `query_admission.go` callers and
    `TestPartitionAttachGrowth`); `partition.Ancestors(view, ref)` is the
    first lookup, the epoch check and the walk, root first. The root keeps
    `Engine.mu` around it and the lock waits outside.
  - `b7a0239e7`: `partition.RepairRemappedHash` returns a `HashRepair`
    (`Removed`, `Incoming`, `Dirty`) and changes nothing; the root
    `repairRemappedHashPartitions` keeps its signature and the install
    loop under `Engine.mu`. New import edge `partition` → `storage`.
  - Tests: none moved or deleted; four root tests changed call shape only.
- [ ] 25f — Attach frontier, insert capture and constraint drop (linear item 140)

Outstanding aliases: none (25a, 25b and 25d added none).

### Notes for later steps

- Step 29 (`dml`) moves INSERT routing execution
  (`capturePartitionInsertLocked`, `validatePartitionInsert`,
  `partitionInsertionGroup`, `partitionStoredRow`,
  `validateTargetPartitionConstraint`, `checkInsertUniqueRow`,
  `applyInsertValidated`, `missingPartition`) on top of
  `partition.InsertGroup`. `partitionInsertState` (with `rowFor` and
  `missingPartition`) stayed in the root at 25b: it holds an
  `AuthorizationView` and reads the search path through the engine. DML 29a
  renamed the shadowed `WriteStage.applyInsertValidated` to
  `applyInsertBatch`; the router `StatementScope.applyInsertValidated`
  (`partition_insert.go`) kept its name.
- The Restore executors left in the root (`begin*Replay`,
  `executePartitionUniqueReplayLocked`, `repairRemappedHashPartitions`,
  the teardown capture and validation) stay root `Engine` methods for
  good: control 30b keeps every Restore hook body in the root, reached
  through the hook table.
- Step 29 (`dml`), from 25c: `partition` exports no write-side host. The
  child selection loop of `validatePartitionInsert` (non-default children
  through `Contains`, then the default child, then `missingPartition`) is a
  pure core 29b/29c can move into `partition` next to `InsertGroup`.
- Step 30 (`control`), from 25c: `partitionConstraintReplayImport`,
  `partitionDefaultReplayAllowed`, `partitionAttachAuthorization` and
  `partitionParentAuthorization` repeat one bootstrap-superuser trust test
  over the engine's Restore state; 30b can fold them into one root helper.
  Control 30b: they stay root and the fold is a separate root cleanup,
  not part of 30c.
- Step 29 (`dml`), from 25e: the routing descent of
  `RepairRemappedHash` repeats `InsertState.Route` except for the row
  mapper (`ReplacementRow` instead of the segment layout). Sharing it
  would put a closure on the INSERT path; it was kept verbatim.
- Catalog 5i: `relationDefiningAuthorityCompatible`
  (`materialized_refresh.go`) is a pure catalog comparison used by
  `partition_locks.go` and `relation_scope.go`; it belongs in `catalog`.
  Done: it is `catalog.RelationDefiningAuthorityCompatible`.
