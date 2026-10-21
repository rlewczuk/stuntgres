# Engine split, step 29: `internal/engine/dml`

Session prompt. Start a fresh session with "Implement
`docs/ENGINE-SPLIT-dml.md`." Follow the shared rules in
[ENGINE-SPLIT.md §8](ENGINE-SPLIT.md#8-implementation-sessions): protocol,
constraints, verification, documentation and report.

- **Plan:** [ENGINE-SPLIT.md](ENGINE-SPLIT.md) §6 step 29; §3 (L13, rule
  6: `dml` may import `analyze` payload types and every upper feature);
  §4.1 patterns 2, 3, 4 and 6; §7 decision 17.
- **Needs:**
  - 29a (linear item 107): index 23a (item 82) and table 24a (item 86),
    because their renames touch the same files.
  - 29b (item 108): 29a, plus the Part A moves it imports or shares files
    with: `copyfmt` 19b (59), `index` 23b (83), `table` 24b (87),
    `partition` 25b (91), `fdw` 26b (95), `view` 27b (100).
  - 29c (item 109): 29b, plus every upper feature's Part B: 23d.2 (85b), 24d.1
    and 24d.2 (89a, 89b), 25d (93), 26d (97), 27d (102). Their write-side host interfaces
    are what 29c composes.
  - 29d (item 110): 29c.
- **Effort:** high for 29a and 29b; xhigh for 29c (docs only, host
  design); xhigh for 29d.
- **Session size:** one sub-step per session; 29d may split into one
  session per statement kind if 29c says so (add lettered items then).
- **Linear items:** 29a = 107, 29b = 108, 29c = 109, 29d = 110.
- **Tools:** `reach` (`.cswd/tmp/PG18-ENGINE-SPLIT-EXEC-REFINE/reach/reach
  -dir . -files '<regexp>'`) and `frontier`
  (`.cswd/tmp/PG18-ENGINE-SPLIT-ANALYZE-QUERY-PREP/frontier/frontier -dir .
  -files a.go,b.go [-decls X,Y]`), run from the worktree root; never pipe
  them into `head`. Moves: `movex`, `keylit`, `unalias`, `shadowrn` in
  `.cswd/tmp/PG18-ENGINE-SPLIT-PLAN-ALIASES/`; `meth2fn` (pointer
  receivers) in `.cswd/tmp/PG18-ENGINE-SPLIT-EXEC-PACKAGE/`.

## Goal

INSERT, UPDATE, DELETE, MERGE, TRUNCATE, COPY execution, RETURNING,
writable CTEs and WHERE CURRENT OF are executed by `StatementScope` and
`Engine` methods spread over about 50 root files. Their code calls into
almost every other area: unique and exclusion checks (index), row
constraints (table), FK enforcement, partition routing, inheritance
targets, view writes and row-level security, foreign tables. That is why
`dml` goes last.

`dml` becomes the package of the write executors. It sits at L13: it
consumes the bound statements of `analyze` (`analyze.Binding`, the INSERT
conflict, RETURNING, UPDATE and MERGE payloads) and may import every upper
feature (`index`, `table`, `partition`, `fdw`, `view`). Nothing below it
imports it; `analyze` never does. It never builds a binder.

The root keeps statement dispatch, source capture under `Engine.mu`,
`WriteStage` and row publication, writable-CTE orchestration and the COPY
protocol. The COPY codecs are in `copyfmt` (step 19).

## Inventory (measured at `c154e14fb`)

`reach` over the files below (including `execute_bound.go`, `fdw_*.go`
DML and the FK files): about 2.4k clean and 8.7k dirty lines. The
dml-area files hold 75 `StatementScope` and 20 `Engine` methods.

| Statement | Entry point (receiver) | Files (lines) |
|---|---|---|
| INSERT VALUES | `Engine.executeInsertLocked` (29a; was the `*parser.Insert` case of `executePreparedCommandLocked`), called from `execute_bound.go` | `insert.go` (185), `insert_returning.go` (213), `insert_write_overlay.go` (129) |
| INSERT ... SELECT | `evaluateInsertQuery`, `assembleInsertRows` | `insert_query.go` (222) |
| ON CONFLICT | `newInsertConflictState`, `normalizeConflictPredicate`, `insertConflictState.selectArbiters`; arbiter probing through `StatementScope.probeUniqueRow` | `insert_conflict.go` (142), `insert_conflict_predicate.go` (384, all clean), `insert_conflict_admission.go` (119), `insert_conflict_update.go` (269) |
| UPDATE | `executeUpdate` (function over the scope, 29a) | `update.go` (584), `update_versions.go` (223), `update_assignment_eval.go` (58) |
| DELETE | `executeDelete` (function over the scope, 29a) | `delete.go` (389), `delete_using.go` (165) |
| MERGE | `executeMerge` (function over the scope, 29a) | `merge.go` (221), `merge_rows.go` (546), `merge_returning_output.go` (54) |
| RETURNING | `beginInsertReturning`, `projectInsertReturning`, `finishInsertReturning` (scope), `newMergeReturningOutput`, `preflightReturning` | `insert_returning.go`, `merge_returning_output.go`, `returning_preflight.go` (37) |
| Writable CTEs | `executeCTECommandsLocked`, `prepareCTECommandsLocked`, `executePreparedCTECommandsLocked` (`Engine`), `StatementScope.activateMutation` | `cte_mutation_execute.go` (323), `cte_mutation_scope.go` (177) |
| WHERE CURRENT OF | `StatementScope.positionedMutationRow` | `current_of.go` (181) |
| TRUNCATE | `acquireTruncate`, `stageTruncate`, `stageTruncateSequences` (scope) | `truncate.go` (325), `truncate_storage.go` (225), `truncate_sequence.go` (129), `fdw_truncate.go` (64) |
| COPY execution | `Engine.bindCopyLocked`, `Engine.initCopyInputLocked`, `StatementScope.copyTableRows`, `copyInsertionState` | `copy.go` (471), `copy_insertion.go` (129); codecs and protocol below |
| FK enforcement | `StatementScope.checkForeignKeys` (at Finish) | `foreign_key_writes.go` (435) |
| FK actions | `settleForeignActions`, `planForeignAction`, `applyForeignAction` (scope) | `foreign_actions.go` (270), `foreign_action_admission.go` (272), `foreign_action_apply.go` (467) |
| Inheritance targets | `captureMutationSource`, `mutationStorage` (scope), `validateInheritedMutationRow` | `mutation_targets.go` (279; table 24a merged `inheritance_dml.go` and `inheritance_dml_authority.go`) |
| Foreign DML | `Engine.executeForeignCommandLocked` | `fdw_execute.go` (505) |

**Not dml, but filed with it:**

| Declarations (file) | Owner | When |
|---|---|---|
| `unique_dml.go` (611): `uniqueWriteBinding`, `uniqueBoundKey`, `bindUniqueWrite`, `checkUniqueRow`, `claimUniqueRow`, `probeUniqueRow` | `index` | 23a renames it `index_unique_binding.go` |
| `captureQuerySource`, `captureQueryPrefix` (`delete.go`), `cursorSimpleScan`, `cursorOccurrenceVersion`, `cursorOccurrencePhysicalRow` (`current_of.go`) | `exec` | done in 11e |
| `mergeDeletions`, `mergePrivateRows`: write-set helpers, also used by `transaction_finish.go` | root, beside `WriteStage` (`write_set.go`) | done (29a) |
| `rowByID` (`update_versions.go`) | `storage` (`storage.RowByID`) | index 23a |
| `ruleUpdateAssignmentsSQL` (`update_row_dump.go`) | `stored` (rule rendering) | done (22c; unexported, export it in 29b if `dml` calls it) |
| `validateRuleNewUpdateOutput` (`rule_update_output.go`) | `view` (rules) | done (27b: `view.ValidateRuleNewUpdateOutput`) |
| `checkTemporaryStorageAccess` | root (`temporary_access.go`) | done (29a) |
| `Engine.queryTableRefsLocked` | root query admission (`query_table_refs.go`) | done (29a) |
| `physicalRowEvent`, `flushPhysicalEvents` | root row-lock area (`statement_host_row_lock.go`) | done (29a) |
| FK probe records (`foreign_key_probe.go`: `findForeignKeyParent`, `prepareForeignKeyChild`, `foreignKeyRows`, `probeForeignKeyIndex`); also used by `foreign_key_restore.go` | `table` | done (24b: `table.FindForeignKeyParent`, `table.ForeignKeyRows`, ...); `dml` imports them |
| `inheritance_query*.go` (`captureInheritanceQueryPrefix`, `inheritanceQueryClosure`, `inheritanceQueryLeaves`, `sourceOnly`); also used by SELECT admission (`query_admission.go`) | `table` (clean helpers, 24b); the scope methods and `inheritanceQueryLeaves` (it calls `viewLeafRowMark`) stay with root query admission | done (24b) |
| COPY codecs (options, FORCE binding, `ResolveColumns`, text/CSV `LineReader`, `BinaryReader`) | `copyfmt` (done, 19b) | 19 |
| COPY execution (`copy.go`, `copy_text.go`, `copy_binary.go`, `copy_insertion.go`: cursors' `Next`/`diagnostic`, `copyOutput`, `copyBinaryOutput`) | `dml` | 29b |
| COPY protocol and reservation (`copy_admission.go`, `protocol_copy.go`, `protocol_copy_query.go`, `CopyBoundary`, `StatementScope.Copy`/`CopyProtocol`) | root (public facade) | — |

**`statementMutation`** (`cte_mutation_scope.go`) is the per-command
state embedded in `StatementScope`; `activateMutation` swaps it by value
between the commands of a writable CTE. Since 29a its fields are embedded
value groups, by owner:
- `mutationTarget`: `lockedRef`, `lockedDescriptor`, `lockedStorage`,
  `lockedIdentityOnly`, `admittedTargetName`, `admittedTargetLexical`,
  `routineDependencies` (dml);
- `mutationIndexes`: `uniqueBinding`, `indexBindings` (index);
- `mutationInsert`: `insertConflict`, `insertReturning` (dml);
- `mutationPartition`: `partitionInsert`, `partitionRelations` (partition);
- direct fields: `statement`, `inheritedTargets` (table/dml), `viewWrite`
  (view) and `rls` (`rowSecurityMutation`, 27a's value group).

**Clean set** (2.4k lines, by real owner): dml keeps about 1.0k (the ON
CONFLICT state and the whole of `insert_conflict_predicate.go`,
`bindingForConflict`, `conflictUpdateCardinalityError`,
`insertReturningOutput`, the MERGE records `mergeRows`, `mergeActionRow`,
`mergeCandidate`, `mergeMatches`, `selectMergeArm`,
`mergeCardinalityError`, `mergeOnTruth`, `mergeHasVirtualReads`,
`mergeReturningOutput`, `updateCandidate`, `evaluateUpdateAssignments`,
`deleteUsingCandidate`, `completeCTEMutation`, `statementCTERoot`, the
TRUNCATE records `truncateScope`, `truncateRelation`,
`truncateReferencer(s)`, `compareTruncateAddress`,
`truncateForeignKeyError`, `truncateOtherTemporary`, `truncateOwnsSequence`,
`truncateSequence`, `truncateName`, `truncateGraphFence`, the FK records
`foreignKeyJob`, `foreignActionScope`, `foreignActionTransition`,
`foreignActionPlan`, `foreignActionQuery`, and the inheritance target
helpers `mutationRowIdentity`, `scatterMutationRow`, `mutationPhysicalRow`,
`versionRows`, and COPY's `copyInsertion*` records and `copyRelationKind`;
`copyTargets` is now `copyfmt.ResolveColumns`); about 0.5k goes to `copyfmt`; the rest to the owners
in the table above. Measurement dumps:
`.cswd/tmp/ENGINE-SPLIT-POSTPONED-PLAN/dml/`.

**The host problem.** A flat `dml.Host` would need about 55 root methods
of eight owners:
- write stage: `StatementScope.Writes`, `WriteStage.applyValidated`,
  `replace`, `replaceRows`, `Delete`, `stageDeletionLocked`, the partition
  wrapper `StatementScope.applyInsertValidated`;
- row locks and versions: `acquireRow`, `acquireUpdateRow`,
  `acquireUpdateRowMode`, `mergeFollowChainStrong`, `physicalRowEvent`,
  `flushPhysicalEvents`, `repeatableConflict`, `latestUpdateRow`,
  `latestDeleteRow`, `latestMergeRow`, `latestConflictUpdateRow`;
- index: `bindUniqueWrite`, `checkUniqueRow`, `claimUniqueRow`,
  `probeUniqueRow`, `uniqueKeyChanged`, `discardExclusionCandidate`,
  `projectExclusionDeletionLocked`, `validateIndexCandidates`,
  `revalidateIndexRows`, `insertOwnRow`;
- table: `validateInsertRow`, `validatePhysicalInsertRow`,
  `validateMutationRow`, `rowConstraintError`, `rowConstraintDetail`,
  `rowDiagnosticDetail`;
- FK actions: `validateForeignActionLocked`,
  `recordForeignActionReplacement`, `foreignActionsOrZero`,
  `writeAuthorizationLocked`;
- partition: `capturePartitionInsertLocked`, `checkInsertUniqueRow`,
  `partitionStoredRow`, `partitionReturningGroup`,
  `acquirePartitionAncestors`;
- inheritance and views: `captureMutationSource`, `mutationStorage`,
  `inheritanceRead`, `validateInheritedMutationRow`,
  `validateInheritedReplacements`, `checkViewRow`, `freezeViewWriteSources`;
- engine state: `privateCatalogLocked`, `privateStorageLocked`,
  `serializableWriteLocked`, `visibilityWriteLocked`,
  `markTemporaryAccessLocked`, `authorizationLocked`,
  `statementAuthorizationLocked`, `validateLockedRelation`,
  `refreshRelationGrantsLocked`, `captureRegclassOutput`,
  `captureRegtypeInput`, `captureOperatorInput`.

That would be a service locator (§4.1 pattern 3 forbids it). By the time
29c runs, `index`, `table`, `partition`, `fdw` and `view` each export a
narrow write-side interface (their Part B, items 85–102); the `dml` host is
their union plus about ten write-stage and row-lock methods (§7 decision
17).

## Part A

### 29a — In place (linear item 107)

1. Re-measure with `reach` and `frontier` over the files in the
   inventory. If 23a or 24a have not landed, stop: their renames touch the
   same files.
2. **Extract INSERT** (one commit): the `*parser.Insert` case of
   `executePreparedCommandLocked` becomes
   `Engine.executeInsertValuesLocked` in a new `insert.go`, called from the
   same place with the same arguments. Keep the per-row `ctx.Err()` check
   and the call order (`validateInsertRow`, `admitInsertProducer`,
   `checkViewRow`, `applyInsertValidated`).
3. **Send misplaced code to its owners** (file moves only, one commit):
   - `mergeDeletions` and `mergePrivateRows` to `write_set.go` (root, next
     to `WriteStage`); `rowByID` is `storage.RowByID` after index 23a;
   - `validateRuleNewUpdateOutput` to `rule_update_output.go` (view area);
     `ruleUpdateAssignmentsSQL` goes with the rule rendering to `stored`
     (22c), so leave it unless 22c has not run;
   - `checkTemporaryStorageAccess` into `temporary_access.go`;
     `queryTableRefsLocked` to `query_table_refs.go`;
   - `physical_row_events.go` merges into `statement_host_row_lock.go`;
   - `captureQuerySource`, `captureQueryPrefix` and the `cursor*` helpers:
     11e (item 75) moved them to `exec` (`exec.CaptureSource`,
     `exec.CapturePrefix`, `exec.Cursor*`).
4. **One receiver style.** `executeUpdate` and `executeMerge` are
   `Engine` methods, `executeDelete` a scope method. Make the three
   statement executors the same kind (prefer functions over the scope and
   engine that 29c can retarget onto a host; use `meth2fn`). The duplicate
   names `StatementScope.applyInsertValidated` (`partition_insert.go`) and
   `WriteStage.applyInsertValidated` (`insert_conflict_admission.go`) get
   distinct names.
5. **Group `statementMutation` by owner** (pattern 2, value groups): a
   target group (lock fields, admitted name, routine dependencies), an
   index group (`uniqueBinding`, `indexBindings`), a partition group, and
   the ON CONFLICT/RETURNING pointers. It stays a value struct, because
   `activateMutation` copies it whole; the RLS fields are 27a's slot.
6. **FK runtime.** FK enforcement (`foreign_key_writes.go`) and FK actions
   (`foreign_action*.go`, `foreign_actions.go`) are dml; FK DDL, validation
   scans, dump and the probe records stay with `table`. No rename needed
   once 26a has given the FDW files the `fdw_` prefix.

Open questions for 29a (stop and ask if unsure):
- Settled during planning: `inheritance_dml*.go` goes to `dml` (29b);
  `inheritance_query*.go`'s clean helpers go to `table` (24b), which both
  root query admission and `dml` import; its scope methods stay in the
  root.
- `preflightReturning` names `analyze.Returning`, so it cannot move to
  `exec` with 11f; it stays with `dml`.

### 29b — Create `dml` and move the clean set (linear item 108)

1. Confirm 29a and the listed Part A moves have landed (`go list` shows
   `index`, `table`, `partition`, `fdw`, `view` and `copyfmt`).
2. Create `internal/engine/dml` and move the measured clean set: ON
   CONFLICT state and arbiter inference, MERGE and UPDATE records,
   RETURNING output records, TRUNCATE records and the truncate fence, FK
   job and action records, inheritance target helpers, COPY insertion
   records.
3. `truncateGraphFence` implements a `session` fence kind; the kind
   constant stays declared in `catalog_fence.go` (publication owns fence
   order; 13b note).
4. Give `dml` its own `errors.go` (`sqlError`, `insertError` copies).
5. Check imports: `analyze` (payload types and pure functions only), the
   upper features, `exec`, `storage`, `visibility`, `lock`, `catalog` and
   lower; never the root.

## Part B

### 29c — Refine the host (docs only; linear item 109)

Re-measure after every upper feature's Part B. Then write the host into
this file. Start from this sketch.

**Host sketch (`dml.Host`).** It embeds the write-side interfaces the
upper features exported (`index.WriteHost` for unique and exclusion
checks, the `table` row-constraint and FK validation interface, the
`partition` routing functions (25c decided `partition` exports no write
host: routing builds on `InsertGroup`, `RowKey`, `Contains` and
`EvaluateRecipe`, and the child selection of `validatePartitionInsert` may
move into `partition` as a pure core), the `view` write and RLS interface, the
`fdw` modification interface) and adds:
- **Write stage:** `Writes()`, `ApplyValidated(...)`, `ReplaceRows(...)`,
  `Delete(...)`.
- **Row locks and versions:** `AcquireRow`, `AcquireUpdateRow(mode)`,
  `FollowUpdateChain`, `PhysicalRowEvent`, `FlushPhysicalEvents`,
  `RepeatableConflict`, and one `LatestRow(kind)` replacing the four
  `latest*Row` methods. These may wait: document "caller holds no
  `Engine.mu`".
- **Statement state:** the authority for writes, `ValidateLockedRelation`,
  `RefreshRelationGrants`, presentation captures.

Settle in 29c: which executor keeps a root skeleton (writable CTEs re-enter
dispatch, so their orchestration stays root); whether COPY execution moves
in 29d or stays next to the protocol; one session per statement kind or one
for all. Write the method list with signatures and lock contracts, the move
set, the commit sequence, then wait for approval.

### 29d — Move the executors (linear item 110)

Follow 29c: seam on `statementHost` (seam tests first), port each executor
in place over `dml.Host`, move with `movex`, unexport, docs.

## Invariants

- **Locks.** Executors run after `Engine.mu` is released; the `latest*Row`
  reads take it again briefly. Row-lock waits never happen under
  `Engine.mu`. `copyIO` releases the owner admission and `replacementMu`
  around every read and write and re-checks the lease, `txRef` and command.
  Foreign callbacks run outside `Engine.mu`.
- **Cancellation.** The INSERT loop checks `ctx.Err()` per row. COPY keeps
  `op.CopyInput`, `PendingProtocolUserCancel`, `PendingCopyInputError` and
  the 57014/FATAL mapping. Foreign rollback uses `context.WithoutCancel`.
- **Publication.** Finish keeps its order: exclusion events, FK actions,
  exclusion events again, FK checks, row publication, catalog preflight,
  index publication. Fence kinds stay declared in `catalog_fence.go`.
  `commitForeign` runs after the fences pass, before local install, and a
  failed remote commit is never retried.
- **Diagnostics.** Messages, details, SQLSTATEs and the first-error choice
  are unchanged, including COPY's "line N" context and MERGE cardinality
  errors.
- **Writable CTEs.** `activateMutation` swaps `statementMutation` by value;
  nothing else resets a slot.

## Targeted tests

- Tests follow the code (ENGINE-SPLIT §4): units of moved pure code (ON
  CONFLICT predicate proofs, MERGE arm selection, truncate graphs) move into
  `dml`; engine-fixture tests are rewritten to the new names. Never modify
  `reference/` or `smoke/`.
- Engine units: grep `*_test.go` for `insertConflict`, `conflictPredicate`,
  `selectMergeArm`, `mergeRows`, `truncateScope`, `foreignAction`,
  `checkForeignKeys`, `positionedMutationRow`, `executeCTECommands`,
  `copyInsertion`. `RACE=1` for COPY cancellation, row-lock waits and FK
  actions.
- Reference selections (`make check RUN='...'`, then `REFERENCE=1`):
  `^TestReference(Insert|Update|Delete|Merge|Copy|Truncate)` (about 600
  tests; split into batches), `^TestReferenceCTE`, `CurrentOf`,
  `^TestReferenceForeign(Key|Action)`, `Returning`. Some `REFERENCE=1`
  selections drift on master too (oracle drift, for example MERGE under
  REPEATABLE READ and DELETE RETURNING boundaries): compare with master
  before blaming the change.
- `make bench` is not needed: startup is not touched.

## Documentation

- Create `internal/engine/dml/README.md` (29b): the executors, the lock
  and cancellation contract, what stays in the root, `## Testing`.
- Update the `exec` README ("What stays in the engine" lists the DML
  executors), the `analyze` README (ON CONFLICT execution state) and the
  root README's DML test selectors.
- `AGENTS.md`: add `dml` to the brace list. The file must not grow.

## Done when

Part A: the executors' records and pure algorithms live in `dml`, and every
misplaced declaration lives with its owner. Part B: the INSERT, UPDATE,
DELETE, MERGE, TRUNCATE and COPY executors and the FK actions live there
too, behind a host composed from the upper features' write interfaces;
dispatch, source capture, writable-CTE orchestration, `WriteStage` and
publication stay in the root. The checks in §8.3 pass.

## Status

- [x] 29a — In place (INSERT extracted, misplaced code to owners, one
  receiver style, `statementMutation` grouped) (`919c6319f`, `3bbec01e0`,
  `146d99d5b`, `03b5e1c78`, then the docs commit). `reach` over the
  inventory files: 83 clean / 124 dirty declarations (1.48k / 6.65k lines)
  after, 86 / 125 (1.58k / 6.81k) before; the drop is the helpers that left
  for their owners' files and fdw 26b's move. `mutationTarget`,
  `mutationIndexes` and `mutationInsert` are clean; `mutationPartition` is
  dirty through `partitionInsertState` (`AuthorizationView`). Step 6 (FK
  runtime) needed no code: the FDW files carry `fdw_` since 26a, so
  `foreign_key_writes.go`, `foreign_actions.go` and `foreign_action_*.go`
  are the FK runtime (dml); FK DDL, validation scans, dump and probe
  records stay with `table`. Verified with
  `^Test(Insert|ReferenceInsert)`,
  `^Test(Update|Merge|Delete|Check|InsertConflict|RangePartition|GeneratedVirtualMerge|ReferenceUpdate|ReferenceMerge|ReferenceDelete)`,
  `make check REFERENCE=1
  RUN='^TestReference(InsertConflict|InsertReturning|InsertSelect|UpdateFrom|UpdateCurrent|DeleteUsing|DeleteCurrent|MergeInsert|MergeReturning|CTEDML)'`
  (`reference/dml`, passed) and quickcheck; racecheck/finalcheck
  skipped at user request. Tests: about 20 engine test files call
  `executeUpdate`/`executeMerge` as functions;
  `insert_conflict_authorization_test.go` calls `applyInsertBatch`;
  `partition_attach_definition_test.go` keys the `mutationPartition`
  group. No test moved or deleted.
- [ ] 29b — `dml` created and clean set moved
- [ ] 29c — Host refined (docs only)
- [ ] 29d — Executors moved

Outstanding aliases: none yet.

### Notes for later steps

- 29a deviations and names, for 29b–29d:
  - The INSERT executor is `Engine.executeInsertLocked`, not
    `executeInsertValuesLocked`: it also runs INSERT ... SELECT
    (`b.CommandQuery`). It is entered with `Engine.mu` held and releases it.
  - Of the two `applyInsertValidated` methods, the `WriteStage` one became
    `applyInsertBatch`; the partition router
    `StatementScope.applyInsertValidated` keeps its name because
    `range_partition_publication_test.go` matches it as a stack frame.
  - The three executors take the scope first and reach the engine with
    `s.engine()`; their helpers (`assembleMergeRows`, `appendMergeAction`,
    `latestUpdateRow`, `latestMergeRow`, `deleteSource`) kept their
    receivers for 29c to retarget.
  - The `statementMutation` groups are embedded, so readers still use
    promoted names (`s.lockedRef`); only keyed literals name the group.
    Moving a group to `dml` or `index` needs either named fields or an
    exported group type.
  - `update_row_dump.go` (`ruleUpdateAssignmentsSQL`) stayed: stored 22c
    lists it (moved there in 22c).

- `insert_write_overlay.go` stays with `dml` (the INSERT write overlay that
  unique probing consults); its key overlay uses `index`'s `uniqueBoundKey`.
- The `exec` 11f preflight move: `query_preflight.go` and
  `query_set_preflight.go` name `analyze` symbols, so they cannot go to
  `exec` as they are; `preflightReturning` stays here.
- `fdw_execute.go`'s foreign DML (`executeForeignCommandLocked`,
  `projectForeignReturning`) moves here in 29d; its callback protocol
  (`fdw_modify.go`) belongs to `fdw`.
- Index 23c refine (decision 8): `index.WriteHost` is implemented on
  `*statementHost` with coarse windows that take `Engine.mu` themselves
  (`UniqueParent`, `BindingAuthority`, `CaptureUniqueProbe`,
  `ExistingIndexRoot`, `CaptureExclusionScan`, `ExclusionScanCurrent`,
  `PublishExclusion`, `DiscardExclusion`, `OwnExclusionKeys`,
  `AdmitExclusion`) and the waits (`ClaimUniqueKey`, `WaitTransaction`);
  no callbacks and no `*exec.Context` arguments. 23d.2 leaves the probe,
  claim and exclusion loops (`probeUniqueRow`, `claimUniqueRow`,
  `checkUniqueRow`, `bindUniqueWrite`, `existingUniqueOutput`,
  `probeExclusion`, `probeExclusionRow`, ...) on `StatementScope` with
  `uniqueConflict` and `exclusionScan` (which keep `AuthorizationView` to
  avoid boxing per probe): they interleave ON CONFLICT arbiters, the write
  overlay, own writes and foreign action owners, so they are `dml`'s.
  29c embeds `index.WriteHost` in `dml.Host` and decides whether the
  per-row interface calls cost anything measurable (23d.2 calls the
  concrete methods).
- Table 24c: `evaluateInsertValuesRow` (`identity.go`; callers
  `insert.go`, `merge_rows.go`, `fdw_execute.go`) is DML, not identity
  code; it stays in the root through 24d and moves with the INSERT
  executors (29b/29d), out of `identity.go`.
