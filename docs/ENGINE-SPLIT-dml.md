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
  `probeUniqueRow`, `uniqueKeyChanged`, `DiscardExclusion` (a write host method since 23d.2),
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

**29c refine (measured at `be8ed733b`).** `reach` over the 34 root files
that still hold DML code (`insert*.go`, `update*.go`, `delete*.go`,
`merge*.go`, `cte_mutation_*.go`, `current_of.go`, `truncate*.go`,
`fdw_truncate.go`, `fdw_execute.go`, `copy.go`, `copy_insertion.go`,
`copy_text.go`, `copy_binary.go`, `foreign_key_writes.go`,
`foreign_action*.go`, `mutation_targets.go`, `partition_insert.go`,
`unique_dml.go`, `exclusion_admission.go`): 176 declarations, 11 clean and
165 dirty, about 6.2k lines. `frontier` over the same files: 232 root
declarations used (OUT), led by `StatementScope` (124 users), `statementHost.op`
(51), `StatementScope.engine` (47), `sessionHost.mu` (38). Output in
`.cswd/tmp/PG18-ENGINE-SPLIT-DML-REFINE/` (`reach.txt`, `frontier.txt`, and
the per-area notes `agent{A,B,C}-*`). Findings:

- **The cut is already at the unlock.** Dispatch
  (`executePreparedCommandLocked`) captures the target, the query sources
  and the comment snapshots under `Engine.mu`, unlocks, and only then calls
  `executeUpdate`, `executeDelete` and `executeMerge`. INSERT
  (`executeInsertLocked`) and foreign DML (`executeForeignCommandLocked`)
  are entered locked and do the same captures before their own unlock; both
  split there. Writable CTEs re-enter dispatch
  (`executePreparedCTECommandsLocked`), so their orchestration stays root,
  and so does `completeCTEMutation` (it consumes the root `Result`).
- **`WriteStage` is the staging authority** (the `self`/`scope` forgery
  guard, owner.Mu → Engine.mu, version allocation, SSI evidence,
  `multiEvents`, FK replacement recording, `ErrCatalogReplaced` gates). But
  `applyValidated` and `replaceRows` also run DML work before their locked
  window: key-change classification (`uniqueKeyChanged`), row-grant waits
  (`acquireUpdateRowMode`), index candidate validation
  (`validateIndexCandidates`), and on a stale unique binding they unlock and
  call back into the probe code (`revalidateConflictRows`,
  `revalidateIndexRows`) before `goto retry`. `multiEvents` is empty outside
  the staging windows: only `stageDeletionLocked` and the replacement window
  append, and both flush after unlocking.
- **The four latest reads share one skeleton** (one Engine.mu window,
  `validateLockedRelation`, `storage.RowByID`, `visibility.LiveVersion`) but
  differ in gate, row source, isolation policy and error text:
  `latestUpdateRow` checks both epochs and `repeatableConflict`;
  `latestDeleteRow` checks no epoch, only the captured storage generation;
  `latestMergeRow` looks up own rows first (hint -1) without an isolation
  check and fails 40001 "concurrent delete" only when not live;
  `latestConflictUpdateRow` checks the parent row shape, reads
  `AffectedRows`/`stage.rows`/the write overlay, also excludes
  `index.UniqueRowDeleted`, and runs `writeAuthorizationLocked` and
  `analyze.CheckBinding` in the same window. They merge only as a kind
  switch inside the root.
- **Row acquisition.** `acquireRow` (DELETE, MERGE delete) checks the full
  candidate authority, is always FOR UPDATE and retakes the replacement read
  only after a wait; `acquireUpdateRowMode` checks only the epoch and retakes
  it whenever implicit. They stay two methods. Chain following is
  `acquireUpdateRowMode(ForUpdate)` then a second latest read; the sketch's
  `FollowUpdateChain` is not needed, and `mergeFollowChainStrong` is a pure
  decision over the MERGE binding and the unique key slots.
- **The unique and exclusion loops go first.** `bindUniqueWrite`,
  `probeUniqueRow`, `claimUniqueRow`, `checkUniqueRow`,
  `uniqueKeyChanged`, `probeExclusion`, `validateIndexCandidates`,
  `revalidateIndexRows`, the write overlay and ON CONFLICT admission read
  unlocked statement state (`ownWrites`, `ownDeletes`, `stage.rows`,
  `stage.deletes`, the mutation groups) and call the index write host. Every
  executor, `WriteStage.Apply` and `set_constraints.go` (SET CONSTRAINTS
  IMMEDIATE) call them.
- **Authority.** `uniqueConflict`, `exclusionScan` and `partitionInsertState`
  hold an `AuthorizationView` only for its `access.View` plus the operation's
  environment (`authorization.go:220`: engine, definition, settings,
  session, materialized population); `writeAuthorizationLocked` and
  `Recapture` change only the view. The FK actions hold one to act as the
  constraint owner (`foreignActionOwner` changes only `State.CurrentUser`).
- **SQL routines swap command state in place.** `installSQLRoutineScratch`
  swaps `statementMutation`, `input`/`output` and `ownWrites`/`ownDeletes`
  while an outer executor is on the stack, and restores them after. The
  executors also write the active mutation (`bindUniqueWrite` sets
  `uniqueBinding`; MERGE writes `rls.Proposal`). So statement state is read
  at call time, and the mutation is reached through a pointer to the active
  slot, never a snapshot.
- **FK enforcement and actions run only from Finish**
  (`transaction_finish.go:82-94`: exclusion events, `settleForeignActions`,
  exclusion events again, `checkForeignKeys`). Their waits all go through
  `foreignKeyGrant`, never under Engine.mu. `planForeignAction` binds (SET
  DEFAULT) and so needs the root binder.
- **TRUNCATE is catalog staging.** `stageTruncate` is one Engine.mu window
  of journal clone, `BeginCatalogStage`, storage changes, index storage
  fences, SSI, sequence staging (`CatalogStage.sequenceState`, sequence
  payload) and the graph fence; `acquireTruncate` is relation admission. It
  uses no write stage, row lock, unique or FK code. A host for it would wrap
  nearly every line.
- **COPY.** The cursors (`copyCursor`, `copyBinaryCursor`) hold the scope
  only for `op` (`CopyInput`, context), the input environment, the wire flag
  of `copyCommandError` and the reader (`copyReader{scope}` → `copyIO`,
  which releases the owner admission and `replacementMu` around each read).
  `CopyProtocol`, `bindCopyLocked`, `initCopyInputLocked` (it builds a
  binder) and COPY TO output are protocol.
- **Foreign DML** never reaches `sqlCursor`: WHERE CURRENT OF is rejected
  before any capture (`fdw_execute.go:29-32`).
- **Defaults.** `applyDefaultOrigin` and `evalDefaultExpression`
  (`domain_default.go`) need only `analyze`, `exec` and `expr`;
  `evalBindingDefault` also calls `view.BindingDefaultOrigin`.
- **Corrections to the 29b record.** `evaluateUpdateAssignments`,
  `evaluateInsertValuesRow`, `preflightConflictUpdate`, `preflightUpdate`,
  `preflightUpdateWithPlanning` and `preflightMerge` are now reach-clean.
  The preflights still stay root (decision 9). `insert_conflict.go` is an
  empty `package engine` stub left by 29b.
- **No reference test matches a DML frame.** The reference cancellation
  fixtures match `relationRequest.wait`, `acquireMaterializedRefresh`,
  `acquireBoundViewRelationModes` and `policyLockExecute` only. One engine
  unit test does: `range_partition_publication_test.go` matches
  `"(*StatementScope).applyInsertValidated"`.

Decisions:

1. **Host shape.** `dml/host.go` declares two hosts, like `view.Host` and
   `view.StageHost`. `*statementHost` implements both in a new root file
   `statement_host_dml.go` (`var _ dml.FinishHost = (*statementHost)(nil)`).
   Executors take `dml.Host`; FK enforcement and actions take
   `dml.FinishHost`; pure helpers take the narrowest piece. Each piece is
   one owner, so neither host is a service locator (§4.1 pattern 3, §7
   decision 17).
   ```go
   type Host interface {
   	exec.Host        // Context, Environments, ReleaseReplacementRead, EnlistForeign, ...
   	index.WriteHost  // unique and exclusion windows and waits (23c)
   	fdw.WriteRuntime // foreign participants and modification tracking (26c)
   	Statement
   	Stage
   	RowLocks
   	Presentation
   }
   type FinishHost interface {
   	Host
   	ForeignKeys
   }
   ```
   The overlap of `exec.Host` and `fdw.WriteRuntime` (`EnlistForeign`,
   `Environments`) has identical signatures. Executors convert `Host` to
   `exec.Host` once per entry (for `exec.Source.Host`), not per probe.
2. **`Statement`: never locks, never waits; read at call time.**
   - `Command() *Command`: the active command's DML state (decision 3). The
     pointer is the scope's active slot, so it follows `activateMutation`
     and the SQL-routine save/restore exactly as today's promoted fields do.
   - `Target() (ids.ObjectRef, *storage.Relation)`: the admitted target
     (`lockedRef`, `lockedStorage`); the executors only read it (6 and 3
     uses).
   - `PrivateRows(a ids.ObjectAddress) PrivateRows`, with `PrivateRows{Own
     []storage.RowVersion; OwnDeletes *visibility.PrivateDeletes; Staged
     []storage.RowVersion; StagedDeletes *visibility.PrivateDeletes}` from
     `ownWrites`, `ownDeletes`, `stage.rows`, `stage.deletes`. Valid until
     the next host call that stages or evaluates.
   - `Snapshot() (stable bool, frontier ids.CommitSeq)`: the owner's
     isolation (`stableIsolation`) and frontier.
   - `UniqueJournal() lock.UniqueJournal`: a new value type in `lock`
     binding the manager and the ticket, with `Checkpoint`, `Restore`,
     `BeginDelta`, `EndDelta`, `RestoreDelta` (today's
     `CheckpointUnique`, `RestoreUnique`, `*UniqueDelta`; lock-manager mutex
     only).
   - `QueryAuthority() *exec.Authority` (for `view.FreezeWriteSources`).
   - `ForeignActions() *ForeignActionScope` (allocates on first use, as
     `foreignActions()`).
   - `PositionedRow(b analyze.Binding) (MutationRowIdentity, error)`:
     `positionedMutationRow`, which reads the session cursor registry; once
     per statement.
   - `CheckWriteBinding(v access.View, b analyze.Binding) error`: the ON
     CONFLICT `analyze.CheckBinding` over a root view built from `v`.
   - `DisclosureEnv(v access.View) access.Environment`: cold path (unique
     key detail, missing partition).
   - `CommandError(err error) error` (`op.CommandError`) and
     `CopyCommandError(err error) error` (`copyCommandError`; the wire flag
     is fixed per statement).
3. **`dml.Command` replaces the DML groups.** `statementMutation` becomes
   `{statement parser.Statement; mutationTarget; dml.Command}`.
   `mutationTarget` stays root: it is statement lock admission, read by
   about 40 production files (ALTER TABLE, COMMENT, constraint DDL, ...).
   `dml.Command` embeds the exported groups `Indexes` (today
   `mutationIndexes`), `Insert` (`mutationInsert`) and `Partition`
   (`mutationPartition`, holding `*partition.InsertState`, decision 8), and
   has the fields `InheritedTargets []exec.Leaf`, `ViewWrite *view.Write`,
   `RLS view.RowSecurity`. It stays a value, so `activateMutation` still
   copies it whole. `captureMutationSource`, `mutationStorage` and
   `mutationTargetRefs` become `Command` methods (they take the target
   storage as an argument). The promoted field names become capitalized
   (`s.insertConflict` → `s.InsertConflict`): `fieldrn` over 32
   production and 18 test files.
4. **`Stage`: each window takes owner.Mu then Engine.mu (or Engine.mu)
   itself, never waits; the caller holds nothing.** `WriteStage` stays root
   and unsplit; the work around its windows moves.
   - `PrepareReplacement(ref ids.ObjectRef, old []storage.RowVersion)
     error`: `replaceRows`' prologue (forgery guard, `active`,
     `validateInheritedReplacements`, FK internal-action admission and row
     grants), in today's order.
   - `StageInsert(ref ids.ObjectRef, batch index.WriteBatch) (stale bool,
     err error)`: `applyValidated`'s window; `stale` when the unique
     binding's catalog moved, and the caller revalidates and retries.
   - `StageReplacement(ref ids.ObjectRef, old []storage.RowVersion, batch
     index.WriteBatch, modes []lock.MultiMode, lane ReplaceLane) (stale bool,
     err error)`: `replaceRows`' window; `ReplaceMerge` (today `replace`),
     `ReplaceAppend` (`appendUpdateReplacement`; `update_allocation_test.go`
     guards its allocations), `ReplaceConflict` (`replaceDistinct`). It
     flushes the physical events after unlocking on success and drops them
     on failure, so `FlushPhysicalEvents` is not a host method.
   - `AppendAcceptedInsert(ref ids.ObjectRef, c *index.WriteCandidate)
     (accepted, stale bool, err error)`.
   - `Delete(c visibility.DeleteCandidate) (bool, error)`: `WriteStage.Delete`
     unchanged (it flushes itself).
   `dml.ApplyValidated(h, ref, batch)` and `dml.ReplaceRows(h, ref, old,
   batch, lane)` own the loops: classify keys, acquire grants, validate
   candidates, call the window, revalidate on `stale`, retry. The root
   `WriteStage.Apply` (public) and `applyInsertBatch` call them with
   `(*statementHost)(s)`.
5. **`RowLocks`.**
   - `AcquireRow(c visibility.DeleteCandidate) error` and
     `AcquireUpdateRow(ref ids.ObjectRef, row ids.RowID, mode lock.Mode)
     error`: wait with the caller holding nothing; release the replacement
     read across the wait; then lock once to revalidate.
   - `AcquirePartitionLock(ref ids.ObjectRef, mode lock.Mode) error`: waits.
   - `LatestRow(r RowRequest) (LatestRow, error)`: locks once, never waits.
     `RowRequest{Kind RowKind; Ref ids.ObjectRef; Row ids.RowID; Own bool;
     Hint int; Storage *storage.Relation; Parent *catalog.Relation;
     Binding *analyze.Binding}` by value (no per-row allocation);
     `RowKind` is `RowUpdate`, `RowDelete`, `RowMerge`, `RowConflictUpdate`;
     `LatestRow{Version storage.RowVersion; Live bool; Parent
     *catalog.Relation; Schema string}`. The root switches on the kind and
     keeps each kind's gate, isolation policy and error text, so
     `repeatableConflict` stays root.
   - `PhysicalRowEvent(ref ids.ObjectRef, row storage.RowVersion, mode
     lock.MultiMode, successor ids.VersionID) error`: Engine.mu briefly for
     the XID, then the lock manager; never waits.
   - `index.WriteHost` gains `EndExclusionRow(ref ids.ObjectRef, row
     ids.RowID, version ids.VersionID)` (locks, never waits) for the MERGE
     DELETE arm's direct `projectExclusionDeletionLocked`.
6. **`Presentation`: locks once, never waits.**
   - `RowDetail(b *analyze.Binding, row []sqltype.Value, p DetailPurpose)
     (string, error)`: `rowDiagnosticDetail`; `DetailConstraint` is
     `rowConstraintDetail` (partition redirect, empty kept),
     `DetailViewCheck` suppresses an empty detail. Error path only.
   - `SchemaName(ns ids.ObjectAddress) (string, bool)`.
   - `CaptureRegclassOutput(rows *exec.FinalRows) error`.
   Not needed from the sketch: `ValidateLockedRelation` and
   `RefreshRelationGrants` (every use is inside a root window or dispatch),
   `RepeatableConflict`, `FlushPhysicalEvents`, `FollowUpdateChain`,
   `Writes()` (`*WriteStage` is a root type).
7. **Probe authority.** `index.UniqueProbe` and `index.ExclusionScan` gain
   `Authority access.View`, filled by the window already recaptured and with
   the FK action owner applied. The concrete `captureUniqueProbe` and
   `captureExclusionScan` go away; `uniqueConflict` holds `access.View` by
   value (no boxing, the same size of return as today) and its diagnostic
   uses `index.UniqueKeyDetailAllowed(c.auth, h.DisclosureEnv(c.auth), …)`.
   One root constructor builds the environment for both
   `authorizationLocked` and `DisclosureEnv`, so they cannot drift.
8. **Partition routing.** `partitionInsertState` becomes
   `partition.InsertState{Catalog, Authority access.View, ...}` without an
   environment field; the routing loop of `validatePartitionInsert`,
   `rowFor` and `missingPartition` move to `partition` (pure given the view
   and `DisclosureEnv`). `capturePartitionInsertLocked` stays root and fills
   it. The order (route, lock a new group through `AcquirePartitionLock`,
   physical validation, target constraint) is unchanged. `mutationPartition`
   is then clean.
9. **Root skeletons** (what stays):
   - Dispatch and its locked prologues. INSERT splits like UPDATE:
     `executeInsertLocked` keeps the captures, `dml.BeginInsertReturning` at
     today's position (`captureCommentSnapshotLocked` rewrites
     `scope.output` first), source capture, the unlock,
     `SelectQuerySources`, the freeze and the defers, then calls
     `dml.ExecuteInsert(h, InsertInput{...})`. Foreign DML keeps
     `fdw_execute.go:23-68` and calls `dml.ExecuteForeign(h,
     ForeignCommand{...})`.
   - Executors return `dml.Outcome{Count int64; Rows *exec.FinalRows}`; the
     root wraps it in `Result`, never storing a typed nil in `Result.Rows`.
     Thin root wrappers `executeUpdate`, `executeDelete`, `executeMerge`
     stay for the engine tests that call them.
   - Writable-CTE orchestration, `completeCTEMutation`, `WriteStage` and the
     host windows, the Finish order, all preflight (`preflightUpdate*`,
     `preflightMerge`, `preflightConflictUpdate`: the `query_preflight.go`
     and `query_join_legality.go` family is shared with SELECT; moving it to
     `analyze` is an end-state review item, not `dml`'s),
     `positionedMutationRow`, `acquireInheritedMutationTargets` and
     `validateInheritedReplacements`, `foreignTableScope` and the foreign
     participants, `acquireForeignKeyCreation` (CREATE TABLE admission).
   - **TRUNCATE stays root** (findings). Optional pure cores move:
     `dml.TruncateGraph` with `Current(...)` (the fence check over the
     current catalog, committed sequences and own descriptors; the root
     `truncateGraphFence` keeps `Kind` and `currentLocked`, and the kind
     stays in `catalog_fence.go`) and `dml.TruncateStorageChange`
     (`truncate_storage.go:141-161`).
   - **COPY:** the protocol stays (`CopyProtocol`, `bindCopyLocked`,
     `initCopyInputLocked`, `copyTableRows` as skeleton, COPY TO output,
     `copyIO`). The cursors and insertion move (decision 11).
10. **Lower-package moves** (no host needed):
    - `analyze.ApplyDefaultOrigin`, `analyze.EvalDefault`
      (`applyDefaultOrigin`, `evalDefaultExpression`); `view.EvalBindingDefault`
      (`evalBindingDefault`). Root callers `execute_bound.go:203` and
      `copy.go:179` follow.
    - `table.NotNullDetailError` (`notNullDetailError`) and
      `validatePhysicalRowConstraints` into `table`, returning the violation
      instead of taking a detail closure; `partition_replacement.go` keeps
      using it.
    - `view.FreezeWriteSources(src *exec.Source, leaves []exec.Leaf, auth
      *exec.Authority, host exec.Host) *exec.Source` with
      `frozenViewSource`; root SELECT dispatch and `dml` call it.
    - `fdw.PlanModification` (`planForeignModification`); callers already
      release the replacement read first.
    - `dml.MergeFollowStrong(m *analyze.MergeBinding, keySlots []int) bool`
      (`mergeFollowChainStrong`).
11. **COPY cursors.** They move to `dml` holding `op *session.Operation`,
    the host (input environment read per row, as today) and a plain
    `io.Reader`; the root passes `copyReader{s, ...}`, so `copyIO` and its
    lease, `txRef` and command checks stay root. The private `rowDefaults`
    assertion goes; `dml` keeps the static `CopyInsertionCursor` assertions.
    `recordCopyInsertion` and `flushCopyInsertion` move with them.
12. **`ForeignKeys` (Finish only).** `dml.SettleForeignActions(h FinishHost)
    error` and `dml.CheckForeignKeys(h FinishHost) error`; the root Finish
    calls them at today's two positions and keeps the `ExclusionAdded`
    read. The authority crosses as `dml.Authority`, implemented by the root
    `AuthorizationView` like `view.RuleAuthority` (27a):
    ```go
    type Authority interface {
    	Access() access.View
    	access.Environment
    	exec.AdmissionAuthority
    	ActingAs(owner authorization.PrincipalRef) Authority // copy; zero owner: unchanged
    }
    type ForeignKeys interface {
    	ForeignKeyJobs() ([]ForeignKeyJob, map[ids.ObjectRef]lock.Mode, Authority, error) // locks once (prepareForeignActions' first window)
    	WriteAuthority(validate bool) (Authority, error)                                  // locks
    	ForeignKeyRows(ref ids.ObjectRef, idx ids.ObjectAddress) (table.ForeignKeyRows, error) // locks
    	RefreshForeignKeyRows(ref ids.ObjectRef, rows *table.ForeignKeyRows) error           // locks
    	RetainedForeignKeyRows(ref ids.ObjectRef, current table.ForeignKeyRows) (table.ForeignKeyRows, error) // locks
    	GrantForeignKey(ref ids.ObjectRef, row ids.RowID, mode lock.Mode, stored *storage.Relation) error // waits, then locks to revalidate
    	RefreshForeignActionJob(job *ForeignKeyJob) error                                  // locks
    	AdmitForeignAction(ref ids.ObjectRef, job *ForeignKeyJob, writable bool) error       // locks
    	ForeignActionRow(ref ids.ObjectRef, candidate storage.RowVersion) (storage.RowVersion, bool, error) // locks
    	DeleteForeignAction(ref ids.ObjectRef, old storage.RowVersion) error                // owner.Mu then Engine.mu; flushes after
    	PlanForeignActionInput(auth Authority, job *ForeignKeyJob, plan *ForeignActionPlan, action parser.ForeignKeyAction) error // locks; binds SET DEFAULT
    	SwapForeignActionAuthority(next Authority) Authority                                // no lock; nil clears
    }
    ```
    `foreignActionOwner` becomes `ActingAs` (also at
    `statement_host_index.go:203-204`). The pure parts of
    `planForeignAction` become `dml.NewForeignActionPlan`; the reachability
    walk `dml.ForeignKeyReach`. The root `foreignActionAuth` field and its
    about 12 root readers stay. `foreignKeyGrant`, `captureForeignKeyRows`
    and `refreshForeignKeyRows` stay root bodies (ALTER TABLE and FK
    lifecycle use them too). Flag for the end-state review (114):
    `ForeignActionScope.KeyTargets` and `AlterKeys` are DDL data in a `dml`
    record.
13. **Per-row cost.** Plain INSERT with k unique indexes makes about 1 + 3k
    host calls per row (`UniqueParent`, then per key `ClaimUniqueKey`,
    `CaptureUniqueProbe`, `PrivateRows`), 1 + k of them Engine.mu windows
    and k lock-manager calls; UPDATE and DELETE take Engine.mu at least four
    times per row. Interface dispatch is well under the cost of those
    windows, so the executors call the interface: no concrete fast path.
    29d.2 confirms with `insert_conflict_benchmark_test.go`,
    `insert_conflict_update_benchmark_test.go` and the
    `update_allocation_test.go` byte envelopes.
14. **29d splits into six items** (110a–110f), in order; each needs the one
    before it. MERGE goes after INSERT (it uses `evaluateInsertValuesRow`,
    INSERT validation and the RLS proposal), FK last (it uses
    `ValidateMutationRow`, the replacement loop and the unique loops).
    None is broad: each touches only DML, host and the files named below.

### 29d — Move the executors (linear items 110a–110f)

Follow the 29c decisions. Every sub-step: seam methods on `statementHost`
with seam tests first, port in place over `dml.Host` (root-local functions
taking `h dml.Host`), move with `movex -qual dml`, unexport, rewrite the
engine tests to the new names, docs. No behavior, error text or error-order
change. Stack-frame matchers: `range_partition_publication_test.go`'s
`"(*StatementScope).applyInsertValidated"` follows the router (29d.3);
grep `reference/` for frame strings before every rename (none today).

**29d.1 — Seam and lower moves (110a).** `dml/host.go` with `Host`,
`FinishHost`, `Statement`, `Stage`, `RowLocks`, `Presentation`,
`ForeignKeys`, `Authority`, `RowRequest`, `LatestRow`, `ReplaceLane`,
`DetailPurpose`, `PrivateRows`, `Outcome`; root `statement_host_dml.go`
with the assertion and the methods (`LatestRow` merges the four reads by
kind; `StageInsert`/`StageReplacement`/`PrepareReplacement` cut from
`applyValidated`/`replaceRows` with the old methods calling them);
`lock.UniqueJournal`; `index.UniqueProbe`/`ExclusionScan` `Authority`
and `EndExclusionRow`; `dml.Command` and the `fieldrn` of the groups;
decision 10's lower moves; delete the `insert_conflict.go` stub. Seam
tests: `LatestRow` per kind (gate, isolation, error text),
`StageReplacement` stale and flush, `PrepareReplacement` order.

**29d.2 — Unique and exclusion probing, staging loops (110b).** To `dml`:
`unique_dml.go` (`BindUniqueWrite`, `ProbeUniqueRow`, `ClaimUniqueRow`,
`CheckUniqueRow`, `UniqueKeyChanged`, `UniqueConflict` with
`Diagnostic`), exclusion probing (`probeExclusion`, `probeExclusionRow`,
`publishExclusionCandidate`; the `*Locked` helpers stay root inside the
windows), `validateIndexCandidates`, `existingUniqueOutput`,
`matchesExistingUnique`, `revalidateIndexRows`, the write overlay
(`insertOwnRow`, `probeInsertWriteOverlay`, `writeOverlay`,
`addInsertWriteKey`), `dml.ApplyValidated` and `dml.ReplaceRows`. About
0.9k lines. Root callers (`WriteStage.Apply`, `applyInsertBatch`,
`set_constraints.go`, the not-yet-moved executors) call `dml.X(h, ...)`.
`RACE=1`: `^Test(Unique|Exclusion|InsertConflict)`.

**29d.3 — INSERT, ON CONFLICT and COPY FROM (110c).** The INSERT body
(`dml.ExecuteInsert`), `evaluateInsertQuery`, `assembleInsertRows` (nil
host legal), `evaluateInsertValuesRow`, RETURNING (`beginInsertReturning`,
`acceptInsertReturning`, `projectInsertReturning`, `finishInsertReturning`,
`partitionReturningGroup`, `admitInsertProducer`), ON CONFLICT admission
and DO UPDATE, partition routing execution (decision 8:
`applyInsertValidated`, `partitionInsertionGroup`, `partitionStoredRow`,
`validateTargetPartitionConstraint`, `checkInsertUniqueRow`),
`validateInsertRow`, `validatePhysicalInsertRow`, `checkViewRow`, the COPY
cursors and insertion. About 1.4k lines. `RACE=1`: COPY cancellation.

**29d.4 — UPDATE and DELETE (110d).** `executeUpdate` body,
`evaluateUpdateAssignments`, `validateMutationRow`, `rowConstraintError`,
`validateInheritedMutationRow`, `deleteSource`, `deleteUsingSource`, the
`Command` inheritance methods. About 0.8k lines. `RACE=1`: row-lock waits
(`^Test(Update|Delete)(Lock|Contention|Cancellation)`).

**29d.5 — MERGE and foreign DML (110e).** `executeMerge`,
`assembleMergeRows`, `appendMergeAction`, `stageMergeRows`,
`newMergeReturningOutput`; `dml.ExecuteForeign` (`fdw_execute.go:69-381`,
`foreignTargetRows`, `foreignInsertResult`, `executeForeignRowMutation`,
`finishForeignResult`). About 0.9k lines.

**29d.6 — FK enforcement and actions; close step 29 (110f).** First the
FK seam that 29d.1 left out (user decision): `ForeignKeys`, `FinishHost`
and `dml.Authority` (decisions 1 and 12) in `dml/host.go`, implemented on
`statementHost` with `var _ dml.FinishHost`, and `AuthorizationView.ActingAs`
replacing `foreignActionOwner` (also at the exclusion scan capture). Then
`dml.SettleForeignActions`, `dml.CheckForeignKeys`,
`foreignKeyParentExists`, `applyForeignAction`, `foreignActionError`, the
pure plan and reach; optional TRUNCATE cores. About 0.7k lines. Tests in
`foreign_actions*_test.go` move to the new names. `RACE=1`: FK actions.
Docs: README, this file, STATUS, ENGINE-SPLIT.md, the `exec` and
`analyze` READMEs, `AGENTS.md` unchanged in size.

Verify every sub-step with the targeted tests and the reference selections
below (quick and `REFERENCE=1`) and quickcheck.

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
DELETE, MERGE, COPY FROM and foreign DML executors, the unique and
exclusion probing and FK enforcement and actions live there too, behind
`dml.Host`/`dml.FinishHost` (29c); dispatch and its locked prologues,
source capture, writable-CTE orchestration, `WriteStage` and its windows,
the Finish order, preflight, TRUNCATE (catalog staging; 29c decision 9)
and the COPY protocol stay in the root. The checks in §8.3 pass.

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
- [x] 29b — `dml` created and clean set moved (`8b336c6c1`, `280456a1b`, `c69b2ba66`, `92fe07400`, `bb16abdca`, then the docs
  commit) · selected unit proof and quickcheck passed, `REFERENCE=1`
  selection passed (see the 29b record); racecheck/finalcheck skipped at
  user request
- [x] 29c — Host refined (docs only; see the 29c record)
- [ ] 29d — Executors moved, in six items:
  - [x] 29d.1 — Seam and lower moves (110a) (`b0f2220f9`, `fdecd3693`, `e0dad29db`, `08f22a454`, `43720ea7d`, then the docs
    commit; see the 29d.1 record)
  - [ ] 29d.2 — Unique and exclusion probing, staging loops (110b)
  - [ ] 29d.3 — INSERT, ON CONFLICT and COPY FROM (110c)
  - [ ] 29d.4 — UPDATE and DELETE (110d)
  - [ ] 29d.5 — MERGE and foreign DML (110e)
  - [ ] 29d.6 — FK enforcement and actions; close step 29 (110f)

Outstanding aliases: none (29b added none).

### 29b record

Done on `implement-part/PG18-ENGINE-SPLIT-DML-PACKAGE`, started at
`116432550`, rebased onto `edba54105` (docs-only refines of 25c, 27c and
30b) and replayed onto `949c682a1` (index 23d.2: the export commit
conflicted in `exclusion_admission.go` and `insert_conflict_admission.go`;
the scripts `c1.sh`, `c2.sh`, `c3.sh`, `c3-post.sh` and `c4.sh` in
`.cswd/tmp/PG18-ENGINE-SPLIT-DML-PACKAGE/` rebuilt the commits after the
seam commit was cherry-picked, the re-measured fixed point was unchanged
and the moved files came out byte-identical).

| Commit | Change |
|---|---|
| `8b336c6c1` | Seam: the dirty methods of moving records become root functions taking the record first (`stageMergeRows`, `recordCopyInsertion`, `flushCopyInsertion`, `addInsertWriteKey`, `preflightConflictUpdate`) |
| `280456a1b` | Staging: the clean declarations of 23 mixed files to nine `dmlmv_*.go` files (`movedecl`) |
| `c69b2ba66` | Export in place (`expspec`/`movex`, 133 rows); the COPY cursors' `insertionOrigin` becomes `InsertionOrigin` with the interface, plus static assertions |
| `92fe07400` | Move: 12 files (about 1.3k lines) to `dml`, `dml/errors.go`, `movex -qual dml` over 50 root files, `keylit` for 5 `MutationRowIdentity` literals, README |
| `bb16abdca` | `insert_conflict_predicate_test.go` and `insert_conflict_truth_test.go` move to `dml`; `conflictPredicateImpliedBy` unexported again |

- **Re-measure.** `reach` over the inventory files (plus `copy_text.go`,
  `copy_binary.go`, `identity.go`): 82 clean, 143 dirty declarations. The
  `frontier` fixed point after the seam commit: 70 members, OUT `sqlError`
  alone, 162 IN keys.
- **Stayed in the root** (clean or listed for 29b, but not movable now):
  - `truncateGraphFence` and its methods: every catalog fence implements
    the root `catalogFence` (`currentLocked(*Engine, …)`) and its kind is
    declared in `catalog_fence.go`; like 27b's `materializedSourceFence`,
    the type stays with its methods (step 3 above assumed it could move).
  - `mutationTarget`, `mutationIndexes`, `mutationInsert`: clean, but they
    are embedded scope state that `activateMutation` swaps by value (as
    27b kept `rowSecurityMutation`).
  - `completeCTEMutation` (returns the root `Result`),
    `evaluateUpdateAssignments` and `evaluateInsertValuesRow` (they call
    `evalDefaultExpression`/`evalBindingDefault` in `domain_default.go`),
    `preflightUpdate`, `preflightUpdateWithPlanning` and `preflightMerge`
    (root `preflightQuery` and the join legality walks).
  - The COPY cursors (`copyCursor`, `copyBinaryCursor`, `copyOutput`,
    `copyBinaryOutput`, `copyCommandError`): they hold the
    `StatementScope`. The inventory table listed them for 29b.
- **Members the plan did not list:** `copyLineTargets`, `preflightReturning`
  (now clean), `projectForeignReturning` and `foreignMutationRow`
  (`fdw_execute.go`; the plan put them in 29d), `foreignActionAdmission`,
  `foreignActionSameKey`, `refreshForeignActionJob`,
  `sameForeignKeyDefinition`, `insertWriteOverlay`, `insertWriteKeyOverlay`,
  `insertConflictState.markAffected`.
- **Runtime interface match.** `insert_query.go` reaches
  `copyInsertionCursor` through a type assertion on the cursor, so the
  compiler did not flag the cursors when the interface method was
  exported: `movex` renames only objects declared in its `-files`. The
  cursors' methods were renamed by hand and `copy_insertion.go` now
  asserts both cursors statically.
- **Names.** Default capitalization, no stutter drops
  (`dml.InsertConflictState`, `dml.MergeRows`, `dml.TruncateScope`,
  `dml.CopyInsertionState`, ...). `errors.go` holds `sqlError` only: the
  set uses no `insertError`.
- **Imports.** `analyze`, `authorization`, `catalog`, `copyfmt`, `exec`,
  `expr`, `ids`, `index`, `lock`, `partition`, `plan`, `storage`, `table`,
  `visibility`, `parser`, `sqlstate`, `sqltype` (`go list`); no root, no
  `view` or `fdw` yet.
- **Tests.** The two predicate proof files moved (their fixture now scans
  `sqltype.Builtins()` instead of building an engine). The other DML unit
  tests build an engine and stay in the root, naming `dml.X`; five call
  sites use the seam functions. None deleted.
- **Checks.** String-literal multiset unchanged apart from
  `dml/errors.go`; selected units after every commit; quickcheck on the
  rebased branch; `REFERENCE=1` selection
  `^TestReference(InsertConflict|InsertReturning|MergeInsert|MergeReturning|Truncate|Copy|ForeignAction|DeleteUsing|UpdateFrom|CTEDML)`.
  racecheck, finalcheck and bench skipped at user request (no startup
  change).

### 29c record

Done on `implement-part/PG18-ENGINE-SPLIT-DML-REFINE` from `be8ed733b`
(the last prerequisite, partition 25d, merged). Docs only: the refine
(findings and decisions 1–14) is in Part B above, and 29d has its six
items with move sets and commit sequences. No code commit. Deviations from
the sketch and the earlier notes:

- Two hosts, not one: FK enforcement and actions (Finish only) take
  `FinishHost`, so the executors' `Host` has no FK methods.
- Not needed: `Writes()`, `FollowUpdateChain`, `FlushPhysicalEvents`,
  `RepeatableConflict`, `ValidateLockedRelation`, `RefreshRelationGrants`
  (decision 6). `ApplyValidated`/`ReplaceRows` are `dml` loops over the
  root staging windows, not host methods (decision 4).
- TRUNCATE stays root, against the inventory and the old "Done when"; only
  its pure cores may move (decision 9). COPY execution moves (cursors and
  insertion); the protocol stays.
- The table row-constraint "interface" is not a host: `table` gets two pure
  functions and the detail text comes from `Presentation.RowDetail`.
- `mutationTarget` stays root (DDL reads it); only the DML groups become
  `dml.Command`.
- The preflights and `evaluateUpdateAssignments` are reach-clean now
  (29b listed them as root-bound); the preflights stay root anyway, the
  default evaluators go to `analyze` and `view`.
- Baseline at `be8ed733b`: `make build` and quickcheck passed.

### 29d.1 record

Done on `implement-part/PG18-ENGINE-SPLIT-DML-SEAM` from `db8252574`.

| Commit | Change |
|---|---|
| `b0f2220f9` | Lower moves: `analyze.ApplyDefaultOrigin`, `analyze.EvalDefault`, `view.EvalBindingDefault`; `table.PhysicalRowViolation` (returns a `RowViolation`, rendered by `Diagnostic` with the caller's detail) and `table.NotNullDetailError`; `view.FreezeWriteSources(src, leaves, auth, host)`; `fdw.PlanModification`; `dml.MergeFollowStrong`; the `insert_conflict.go` stub and `fdw_modify.go` deleted |
| `fdecd3693` | `lock.UniqueJournal` (`Manager.UniqueJournal(t)`: `Checkpoint`, `Restore`, `BeginDelta`, `EndDelta`, `RestoreDelta`); the ON CONFLICT paths use it through `uniqueJournal()` |
| `e0dad29db` | `UniqueProbe.Authority`/`ExclusionScan.Authority` (`access.View`); the concrete captures and `exclusionScan` go; `uniqueConflict.auth` is a view; one `operationAuthority(op, v)` constructor for `authorizationLocked`, `disclosureEnv` and `checkWriteBinding`; `index.WriteHost.EndExclusionRow` for MERGE's DELETE arm; `partition.InsertState` with `Route`, `RowFor`, `MissingPartition` |
| `08f22a454` | `fieldrn` of the nine promoted fields (`uniqueBinding` → `UniqueBinding`, ..., `rls` → `RLS`); `dml.Command` with `Indexes`, `Insert`, `Partition`, `InheritedTargets`, `ViewWrite`, `RLS` and the methods `TargetRefs`, `Storage`, `CaptureSource`; `statementMutation` = `{statement; mutationTarget; dml.Command}` |
| `43720ea7d` | `dml/host.go` (`Host`, `Statement`, `Stage`, `RowLocks`, `Presentation`, `PrivateRows`, `ReplaceLane`, `RowKind`/`RowRequest`/`LatestRow` with `UpdateRowRequest`, `MergeRowRequest`, `DeleteRowRequest`, `DetailPurpose`, `Outcome`); root `statement_host_dml.go`, `statement_host_stage.go` (the windows cut from `replaceRows`, `applyValidated`, `appendAcceptedInsert`; the old methods keep the loops) and `statement_host_latest_row.go` (the four reads merged by kind); seam tests |

- **Deviations.**
  - `ForeignKeys`, `FinishHost` and `dml.Authority` moved to 29d.6 (user
    decision): their only callers move there, and several methods cut the
    FK windows. 29d.1 asserts `var _ dml.Host` only.
  - `partition.InsertState` keeps `Env access.Environment` next to
    `Authority` (decision 8 said no environment field): Restore's trusted
    insertion authority (`statementAuthorizationLocked`) sets
    `namespaceUser`, the `$user` of `ReferencePath`, which an environment
    rebuilt from the view would lose. The ON CONFLICT probe authority comes
    from `writeAuthorizationLocked`, which sets no such field, so
    `operationAuthority(op, v)` rebuilds its environment exactly.
  - COPY FROM a foreign table now calls `foreignUnlocked` itself before
    `fdw.PlanModification`; the 29c note "callers already release the
    replacement read" held for foreign DML only.
  - `RowRequest` has no `Binding` field: the conflict kind builds its
    binding from the current parent and the active `InsertConflict`.
  - The `RowLocks` methods delegate to the scope methods, which DDL also
    uses (`acquirePartitionLock`); the root keeps `mutationStorage` as a
    helper over `Command.Storage`, because the target is root state.
  - Go 1.26 rejects a keyed literal naming a promoted field, so
    `rule_cte.go` and `partition_attach_definition_test.go` name
    `Command: dml.Command{...}`.
- **Tests.** New: `statement_host_dml_test.go` (`LatestRow` gates and
  isolation per kind, `StageReplacement` stale/drop/flush,
  `PrepareReplacement` order) and `lock/unique_journal_test.go`. Rewritten
  to the new names: `partition_retained_checks_test.go` (a
  `retainedRowError` helper), `view_write_test.go`, `fdw_modify_test.go`,
  `domain_default_test.go`, `partition_attach_definition_test.go`, and the
  17 test files `fieldrn` touched. None moved or deleted.
- **Checks.** Selected units after every commit; string-literal multiset
  unchanged apart from test messages; quickcheck; `make check REFERENCE=1
  RUN='^TestReference(InsertConflict|InsertReturning|InsertSelect|UpdateFrom|UpdateCurrent|DeleteUsing|DeleteCurrent|MergeInsert|MergeReturning|Copy|CTEDML|Partition)'`
  passed apart from oracle-side failures: the known
  `TestReferencePartitionExclusion*` drift and one
  `TestReferenceCopyTextInputStatementTimeout/postgres` timing flake
  (PostgreSQL finished the COPY first; it passes alone on this branch and on
  master); `RACE=1` over ON CONFLICT, range partition publication, row-lock
  waits, view write rechecks and the seam tests; the 128-row ON CONFLICT benchmarks allocate the same or two fewer
  objects per update proposal. racecheck, finalcheck and bench skipped at
  user request (no startup change).

### Notes for later steps

- 29d.1, for 29d.2: `applyValidated` and `appendAcceptedInsert` keep
  `s.active()` in their root prologue, before `validateIndexCandidates`;
  the `dml` loops need the same order, but `Stage` has no `Active` method
  (only `PrepareReplacement` covers replacements). Add one or a
  `PrepareInsert`, without moving the check into the windows (that would
  change the error order). The `modes` slice that `replaceRows` passes to
  `StageReplacement` escapes once the call goes through the interface; only
  test callers leave `ActionMode` zero, so production stays
  allocation-free. Watch the `update_allocation_test.go` envelopes.

29c (item 109) read every note below; where they differ, its decisions
1–14 win (for example `preflightConflictUpdate` stays root, decision 9,
and the probe authority crosses as `access.View`, decision 7).

- 29b, for 29c/29d: the seam functions `stageMergeRows`,
  `recordCopyInsertion`, `flushCopyInsertion`, `addInsertWriteKey` and
  `preflightConflictUpdate` are the record-running code that moves with the
  executors behind `dml.Host`. `truncateGraphFence` can move only if the
  fence check splits into a pure `dml` core and a root wrapper that keeps
  `Kind`/`currentLocked`. `evaluateInsertValuesRow` and
  `evaluateUpdateAssignments` need the default evaluators
  (`domain_default.go`) behind the host or in a lower package.

- View 27c (item 101), for 29c: 27d moves `rowSecurityMutation` to `view`
  as `RowSecurity` (exported fields; `statementMutation.rls` keeps its
  name) and the selection to `view.RowSecurityTarget` and
  `view.SelectRowSecurity`; `configureRowSecurityLocked` in
  `execute_bound.go` stays the root authority wrapper that writes it.
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
  concrete methods). Done in 23d.2: the probe and scan captures also
  return the `AuthorizationView` only through the concrete root methods
  `captureUniqueProbe`/`captureExclusionScan` (the exported methods drop
  it), so 29c must carry that authority across with `uniqueConflict`; the
  decisions are `index.BuildUniqueWriteBinding`, `UniqueRootStart`,
  `ExistingRootOutput`, `OwnExclusionEntry`, `VisitExclusion`,
  `ExclusionDeferred`, `ExclusionShapeEntries` and the projection edits.
- Table 24c: `evaluateInsertValuesRow` (`identity.go`; callers
  `insert.go`, `merge_rows.go`, `fdw_execute.go`) is DML, not identity
  code; it stays in the root through 24d and moves with the INSERT
  executors (29b/29d), out of `identity.go`.
