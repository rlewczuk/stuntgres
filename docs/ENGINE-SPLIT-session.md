# Engine split, step 13: `internal/engine/session`

Session prompt. Start a fresh session with "Implement
`docs/ENGINE-SPLIT-session.md`." Follow the shared rules in
[ENGINE-SPLIT.md §8](ENGINE-SPLIT.md#8-implementation-sessions): protocol,
constraints, verification, documentation and report.

- **Plan:** [ENGINE-SPLIT.md](ENGINE-SPLIT.md) §6 step 13; §3 rule 7 and L9;
  §4.1 patterns 2 and 4; §7 decisions 7, 10 and 11.
- **Needs:** nothing for 13a and 13b. 13c needs 11c, 12d and 8b. Read the
  `lock` and `visibility` READMEs (lock order, snapshot retention) and the
  transaction rules in `AGENTS.md`.
- **Effort:** high for 13a; xhigh for 13b (publication and fences) and 13c;
  high for 13d.
- **Session size:** one sub-step per session; 13d spans several sessions.
  13a, 13b and 13d are broad rewrites.

## Goal

The statement scope, the operation, the catalog journal and the stage stop
carrying feature fields. Then sessions, operations, the transaction owner,
the catalog journal and the settings store move into `session`, which imports
no feature package and not `analyze`. The root package keeps the public
facade, the statement scope and statement dispatch (13c refine).

## Inventory (measured at `75fe0d710`)

**Session-layer files** (39 files, 9.5k lines): `session*.go` (1,510),
`protocol*.go` (1,603), `prepared*.go` (558), `sql_cursor*.go` (882),
`notification*.go` (482), `transaction*.go` (3,969, including 2PC in
`transaction_prepared.go`), `savepoints.go` (261), `commit_timestamp*.go`
(250). Related: `catalog_transaction.go` and
`catalog_transaction_storage.go` (the journal, 603), `discard.go`,
`notices.go`, `execute.go`, `execute_bound.go`, `statement_signature.go`.

**Root driver use of the engine:** `Engine` (`StartStatement`, `Begin`,
`Commit`, `Rollback`, `StartSession`, `ResolveLogin`, `Reset`, `Dump`,
`Restore`, `InspectIndex`, `InstallForeignDefinitions`), `Session`
(`Engine`, `AcquireActivity`, `RequestEnd`, `AcquireCommand`,
`WaitForNotification`, `WaitEnd`, `Reuse`, `IsLive`, `Check`), `Operation`
(`CommandError`, `Done`, `DetachNotices`, `Context`), `StatementScope`
(`Finish`, `Execute`, `RetainMessage`, `NextStatement`, `FinishMessage`,
`Describe`, `Copy`), `RowResult`, `StatementDescription`, `TxOptions` and
the isolation constants, `SessionStart`, `ParameterInfo`, `CopyBoundary`,
`EndReason` values.

**`StatementScope`** (`transaction.go:276`, 119 fields plus the embedded
`statementMutation` with 21; 290 methods). Single-feature fields and their
uses (selectors plus literal keys, production files), grouped into the slots
of 13a:

| Slot | Fields (uses/files) |
|---|---|
| protocol | `protocolBind` 15/6, `protocolResume` 3/2, `protocolSimple` 16/10, `protocolSimpleCreated` 4/3, `protocolSimpleStreaming` 3/2, `protocolSimplePortal` 4/2, `protocolCommand` 9/7, `initialParameters` 7/3 |
| prepared rebind | `preparedReuse` 7/3, `preparedInvalid` 4/2, `preparedPath` 3/1, `preparedNamespace` 2/1, `preparedPrivate` 2/1, `preparedEpoch` 2/1, `preparedChangedDuringAdmission` 4/4 |
| inquiry snapshot (value group) | `commentCatalog` 52/19, `commentRoles` 19/11, `deparseAuth` 22/8, `deparsePath` 24/13, `collationPath` 6/4, `visibilityCatalog` 12/4, `visibilityRelationPath` 5/3, `visibilityOtherPath` 6/4, `visibilityDemand` 13/5, `currentSchemaResult` 7/3, `searchPathResults` 4/2 |
| presentation caches | `regprocOutput` 4/1, `regprocedureOutput` 4/1, `operatorOutput` 4/1, `operatorGroups` 3/1, `regTSOutput` 4/1, `conversionDefaults` 3/1, `pgRegex` 1/1, `quoteCaptures` 4/3 |
| TRUNCATE | `truncateRelations` 7/3, `truncateSequences` 13/1, `truncateRestart` 4/3, `truncateNames` 3/2, `truncateSelected` 5/3, `foreignTruncateCascade` 2/2 |
| foreign-key actions | `foreignActionAdmissions` 15/4, `foreignActionTransitions` 10/2, `foreignActionsSettling` 4/3, `foreignActionExclusionAdded` 2/2, `foreignActionJobs` 2/2, `foreignKeyTargets` 3/3, `alterForeignKeys` 4/2 |
| view creation | `viewCreationNamespace` 6/3, `viewPersistence` 4/2, `viewBinding` 3/2, `viewChanges` 11/3, `activeViews` 6/1 |
| materialized refresh | `materializedRefresh` 8/4, `materializedSources` 4/2, `materializedSourceView` 3/2 |
| inheritance | `inheritanceRelations` 5/3, `inheritanceCatalog` 4/2, `inheritanceAlter` 41/9 |
| index and exclusion | `indexMaintenance` 3/3, `existingIndexOutputs` 6/3, `exclusionModes` 15/3, `exclusionStarted` 4/3 |
| partition | `partitionPrograms` 6/3, `partitionValidationView` 6/3 |
| foreign tables | `foreignOwner` 11/5, `foreignCursor` 4/3, `foreignStatements` 17/4, `foreignModifications` 5/2, `foreignHoldPromotions` 9/4 |
| domain | `domainEvaluation` 5/3, `domainPreparedChecks` 8/7, `domainInputCatalog` 9/7 |
| drop | `consumedColumnDrops` 5/1 |

The header keeps the lifecycle and cross-cutting fields: `self`, `owner`,
`op`, `stage`, `view`, `finished`, `message`, `messageEnded`,
`messageResult`, `result`, `worked`, `completed`, `sessionCompleted`,
`controlOutcome`, `replacementRead` (148/46), `ticket`, `multiEvents`,
`txRef` (132/55), `command`, `ownWrites`, `ownDeletes`, `implicit`,
`input`, `output`, `statementStart`, `transactionStart`, `timeoutRef`,
`ruleActionAuth`, `authority`, `routineAuth`, `identity`, `mutations`,
`activeMutation`, `cteInvocation`, `notificationActions`,
`notificationEvents`, `querySources`, `queryLockAdmission`, `queryLocks`,
`catalog`, `catalogAuthority`, `projectedRoots`, `Expected`,
`admissionWaited`, `routinePrograms`, `foreignActionAuth`, and the embedded
`statementMutation`.

**`Operation`** (`session.go:110`, 35 fields). Restore and population flags:
`restoreTableDestination` 41/16, `restoreInsertAuthority` 5/4,
`restoreCommentAuthority` 4/2, `restoreDefaultReplay` 4/2,
`restorePartitionAttach` 5/2, `restorePartitionCheck` 5/2,
`restorePartitionUsingIndex` 6/2, `restorePartitionUnique` 8/2,
`materializedPopulation` 6/3, `materializedOwner` 6/2; DISCARD:
`discardRoleReset` 7/1.

**`catalogChanges` journal** (`catalog_transaction.go:16`). Feature fences:
`indexFences` 6/2, `namespaceFences` 6/2, `readFences` (partition) 8/3,
`truncateFences` 7/2, `materializedSources` 6/2, `domainValidations` 6/3.
Feature maps: `indexCandidates` 24/9, `indexRebuilds` 11/4, `uniqueRoots`
30/13, `domainIndexRoots` 11/3, `domainChanged` 6/2, `enumCreated` 9/2,
`enumUnsafe` 9/3, `extensionAuthority` 7/2. Core: `originals`,
`descriptors`, `names`, default ACLs, `generators`, `storage`,
`dependencies`, `cachedBase`, `cached`, roles.

**`CatalogStage`** (`catalog.go:340`, 39 fields). Feature fields: index
(`indexRebuilds` 10/4, `indexCandidates` 22/9, `indexLayouts` 9/5,
`indexParents` 8/3, `indexSets` 7/3, `indexOutput` 9/9), domain
(`domainTargets` 30/11, `domainBases` 78/28, `domainValidation` 6/3,
`domainAlter` 8/4, `domainIndexRoots` 7/3), materialized
(`materializedReplacement` 24/6, `materializedSourceView` 4/3), typed
tables (`typedSource` 5/3, `typedObserved` 4/3), identity
(`identityOriginals` 15/4, `identityDependencies` 10/5), `likeSources` 7/3,
cascade names (`schemaCascadeNames` 11/4, `viewCascadeNames` 28/10),
`partitionReads` 6/3, `generators` 20/8. *(17a moved `generators` and both
identity fields into the `sequenceStage` slot.)*

**`transactionOwner`** (`transaction.go:49`, 39 fields). FDW participants:
`foreignCleanup` 8/2, `foreignMarkNext` 3/1, `foreignParticipants` 19/5.

## Sub-steps

### 13a — Statement scope slots (broad)

1. For each slot in the table (except the inquiry snapshot), add a
   feature-owned struct next to its feature code (for example
   `truncateScope` in `truncate.go`) and one pointer field on
   `StatementScope`, allocated on first use by one accessor
   (`s.truncate()`). Where a feature reads a field only to test it for zero,
   a nil slot must mean the same zero state.
2. The inquiry snapshot is read in many files and captured eagerly; make it a
   value group (`inquiry inquirySnapshot`), not a pointer, so allocation does
   not change.
3. Rewrite selectors with a `go/types` tool (field → slot path) and a census.
   One commit per slot or per two small slots.
4. Simplify `transaction_finish.go` (the reset) and `sql_cursor_hold.go` (the
   copy for held cursors) to handle each slot as one unit. Check every reset
   and copy against the old field list: a missed field is a behavior change.
5. Result: about 70 fields leave the header. Record the final header list
   here.

**13a result** (re-measured at `e8598c73e`, rebased onto `1ecaa1876`): 74
fields left the header (124 → 50 header fields plus 15 slot fields).

| Slot | Kind | Type, home file | Fields |
|---|---|---|---|
| `inquiry` | value | `inquirySnapshot`, `query_sources.go` | the 11 inquiry fields, names kept |
| `prepared` | value | `preparedRebind`, `prepared_rebind.go` | reuse, invalid, path, namespace, private, epoch, changedDuringAdmission |
| `sqlRoutine` | value | `sqlRoutineDemand`, `sql_routine_scope.go` | scalar, result, resultPlan (new since the inventory) |
| `inheritance` | value | `inheritanceScope`, `inheritance_query_scope.go` | relations, catalog, alter |
| `domains` | value | `domainScope`, `domain_input.go` | evaluation, preparedChecks, inputCatalog |
| `protocolSlot` | pointer | `protocolScope`, `protocol_portal.go` | bind, resume, simple, simpleCreated, simpleStreaming, simplePortal, command |
| `presentationSlot` | pointer | `presentationCaches`, `regproc_output.go` | regproc, regprocedure, operators, operatorGroups, regTS, conversionDefaults, pgRegex |
| `truncateSlot` | pointer | `truncateScope`, `truncate.go` | relations, sequences, restart, names, selected, foreignCascade |
| `dropsSlot` | pointer | `dropScope`, `drop_provider.go` | consumedColumns |
| `foreignActionsSlot` | pointer | `foreignActionScope`, `foreign_action_admission.go` | admissions, transitions, settling, exclusionAdded, jobs, keyTargets, alterKeys |
| `viewCreationSlot` | pointer | `viewCreationScope`, `view.go` | namespace, persistence, binding, changes, active |
| `materializedSlot` | pointer | `materializedRefreshScope`, `materialized_refresh.go` | refresh, sources, sourceView |
| `indexesSlot` | pointer | `indexScope`, `index_maintenance.go` | maintenance, existingOutputs, exclusionModes, exclusionStarted |
| `partitionsSlot` | pointer | `partitionScope`, `partition_routine.go` | programs, validationView |
| `foreignTablesSlot` | pointer | `foreignTableScope`, `fdw_execute.go` | owner, cursor, statements, modifications, holdPromotions |

Slot rules (also in the comment above the slot fields in `transaction.go`):
a pointer slot `xSlot` stays nil until its first write through `x()`;
reads use `xOrZero()`, which returns a copy and never allocates, so nil
means all zero and the compiler rejects writes through the read accessor.
Clearing on common paths (Finish, COMMIT, wire portals, held cursors) goes
through nil-safe slot methods or setters that never allocate a missing slot.
The SQL routine scratch swaps the `protocolSlot` and `presentationSlot`
pointers, so a nested body starts empty as before; it saves partially saved
slots (view `active`, partition `validationView`, foreign `owner`/`cursor`)
field by field. Nothing else resets a slot to nil.

Deviations from the table above, from re-measurement:

- Value groups, not pointer slots, for `prepared`, `inheritance` and
  `domains`: every SELECT/DML statement or SQL routine body writes them
  (`selectPreparedAnalysis`, `acquireInheritanceSources`, the execution
  input catalog), so a lazy slot would add an allocation per statement and
  per SQL-function row.
- `quoteCaptures` stays in the header: every bound statement sets it and the
  rule scratch saves it with the query state, not with the presentation
  caches.
- `initialParameters` stays in the header: the driver path, prepared SQL and
  SQL routines write it, not only the wire protocol.
- `rowSecurity` and `activeSQLRoutineDepth` (new) stay in the header.

Final header: `statementMutation` (embedded), `mutations`, `activeMutation`,
`cteInvocation`, `self`, `owner`, `op`, `ruleActionAuth`, `rowSecurity`,
`authority`, `routineAuth`, `stage`, `notificationActions`,
`notificationEvents`, `view`, `finished`, `message`, `messageEnded`,
`messageResult`, `result`, `worked`, `completed`, `sessionCompleted`,
`controlOutcome`, `replacementRead`, `ticket`, `multiEvents`,
`querySources`, `queryLockAdmission`, `queryLocks`, `txRef`, `command`,
`ownWrites`, `ownDeletes`, `Expected`, `admissionWaited`, `implicit`,
`input`, `routinePrograms`, `statementStart`, `transactionStart`,
`timeoutRef`, `output`, `initialParameters`, `activeSQLRoutineDepth`,
`quoteCaptures`, `catalog`, `catalogAuthority`, `projectedRoots`,
`foreignActionAuth`, `identity`, then the 15 slot fields.

### 13b — Operation, journal, stage and owner slots (broad)

1. `Operation`'s Restore and population flags become one `restoreReplay`
   slot (allocated only by Restore and materialized population);
   `discardRoleReset` moves into a DISCARD slot or stays if it is the only
   field.
2. The journal's feature fences become one list of a `catalogFence`
   interface with the methods publication calls today (measure them in
   `catalog_transaction*.go` and `transaction_publication.go`: validate at
   commit, savepoint copy, rollback). Keep each fence's validation order: the
   list must preserve the current check order.
3. The journal's feature maps move into feature journal slots (index, domain,
   enum, extension). Savepoint marks (`savepointMark.catalog`) clone slots as
   units; check `savepoints.go`.
4. `CatalogStage`'s feature fields move into feature stage slots (index,
   domain, materialized, typed tables, identity); the cascade names and
   `likeSources` stay if no single feature owns them. Measure first.
5. `transactionOwner`'s FDW fields become one slot.
6. Feature steps 14–17 reuse these slots. If a feature step runs first, it
   creates its own slot the same way, and 13b skips it. Typedef 15a created
   the domain ALTER and typed-table stage slots and the domain and enum
   journal slots; `domainBases`, `domainTargets` and `domainChanged` are
   shared and stay (see the typedef prompt file's Status).

**13b result** (measured at `01c1a9ec4`, rebased onto `c6766e328`): 32
feature fields left the four structs. New pointer slots follow the 13a rules
(`xSlot` field, allocating `x()`, copying `xOrZero()`, nil means all zero).

| Struct | Slot | Kind | Type, home file | Fields |
|---|---|---|---|---|
| `Operation` | `replaySlot` | pointer | `restoreReplay`, `restore_replay.go` | tableDestination, insertAuthority, commentAuthority, materializedPopulation, materializedOwner, defaultReplay, sqlBodies, partitionAttach, partitionCheck, partitionUsingIndex, partitionUnique, domainReplay |
| `catalogChanges` | `fences` | list | `catalogFence`, `catalog_fence.go` | the storage-read, index-set, namespace, extension authority, TRUNCATE graph and materialized source fences |
| `catalogChanges` | `indexesSlot` | pointer | `indexJournal`, `index_journal.go` | uniqueRoots, candidates, rebuilds |
| `CatalogStage` | `indexesSlot` | pointer | `indexStage`, `index_stage.go` | rebuilds, candidates, layouts, parents, sets, output |
| `CatalogStage` | `materializedSlot` | pointer | `materializedStage`, `materialized_stage.go` | replacement, sourceView |
| `transactionOwner` | `foreign` | value | `foreignTransaction`, `fdw_transaction.go` | participants, cleanup, markNext |

Fence list: each fence type declares `fenceKind()` and its own
`currentLocked(e, c)` next to its feature code (`partitionReadFence`,
`catalogIndexFence`, `catalogNamespaceFence`,
`extensionAuthorityFence`, `truncateGraphFence`,
`materializedSourceFence`). `catalogPreflightLocked` checks the kinds at
the same four points as before: storage-read, index-set and namespace after
the typedef domain fences; extension authority before the dependency
preflight; TRUNCATE graph after it; materialized sources after the storage
check. Within a call, kinds run in the given order and fences in recording
order, so the first error is unchanged. The extension fence repeats the
name-conflict check per observation; the check does not depend on the
observation, so the first error is the same. The journal index slot's
`clone` copies its maps (the journal is copy-on-write; savepoint marks keep
old journals), `forget` replaces the paired deletes and `pending` the
publication XID test.

Stays, measured: `discardRoleReset` (the only DISCARD field); journal
`domainChanged` (shared, see the typedef Status), `dependencies` and the
core maps; stage `querySources` (written by policy, rule, view ALTER,
TRUNCATE, CREATE TABLE AS and REFRESH stages), `partitionReads` (generic
storage read fences, also fed by index parents and candidates),
`likeSources` (CREATE TABLE LIKE only, one field; postponed `table`),
cascade names (about 11 features), `initialTables` and `initialTx` (CTAS,
SELECT INTO, TEMP, materialized), `extensionNamespace` (one extension
field; step 16), the database predecessor and authority, `roleCandidate`,
`terminalCleanup`, `dependencyPath`, `rowShapes`.

Deviations from the plan above:

- `transactionOwner`'s FDW fields are a value group, not a pointer slot:
  every SAVEPOINT advances `markNext`, so a lazy slot would allocate in
  transactions with no foreign tables.
- `extensionAuthority` became a fence kind rather than an extension journal
  slot: publication only checks it, like the other fences. Step 16b moved
  the observation to `extension.AuthorityObservation`; the engine wraps it
  in `extensionAuthorityFence` (`extension_authority_fence.go`), because the
  fence interface takes the engine.
- `querySources` stays in the stage header (see above); only the two
  refresh fields form the materialized slot.

### 13c — Refine (docs only)

After 11c and 12d:

1. Re-run the appendix tool with the session-layer files as the move set.
   List every path to the root package and to feature code.
2. Decide what moves: `Session`, `Operation`, the statement scope core, the
   transaction owner, savepoints, the journal, 2PC, notifications, timeouts,
   SQL cursors, protocol portals, the prepared SQL registry, and the session
   settings store (§7 decision 10).
3. Decide how scope methods spread over feature files (about 290 methods)
   are handled: methods that only touch the header move with the scope;
   feature methods become feature functions taking the scope or its slot.
4. Settle §7 decision 7 (slot typing after the move) and decision 11
   (facade types).
5. Write the 13d session order and commit sequence here; wait for approval.

**13c refine (approved; measured at `3fbd7d241`).** The `frontier`,
`reach` and `paths` tools (`.cswd/tmp/PG18-ENGINE-SPLIT-EXEC-REFINE/`;
outputs in `.cswd/tmp/PG18-ENGINE-SPLIT-SESSION-REFINE/`), with the 43
session-layer files (9.75k lines: `session*.go`, `protocol_*.go`,
`prepared_rebind.go`, `prepared_sql.go`, `sql_cursor*.go`,
`notification*.go`, `transaction*.go`, `savepoints.go`,
`commit_timestamp_store.go`, `catalog_transaction*.go`, `catalog_fence.go`,
`notices.go`, `statement_signature.go`) as the move set:

- **Most of the layer is orchestration.** 57 `Engine` methods live in these
  files (begin and end, publication, preflight, 2PC install, notification
  publication, settings application, prepared re-analysis); they install
  changes across catalog, storage and visibility under `Engine.mu` (§3 rule
  4). `StatementScope` has 320 methods in 119 files; against a proposed core
  header, 16 touch only core fields, 58 touch only core fields but call
  other scope methods, 95 reach `Engine` and 151 use execution fields or
  slots.
- **Outside uses** (production, outside the move set): scope header fields
  about 2.5k (`op` 767, `owner` 300, `input` 187, `inquiry` 140,
  `replacementRead` 127), `Operation` about 1k (`session` 376, `Context`
  362), `Session` about 440 (`Engine()` 146), transaction owner about 380
  (`catalog` 187), journal about 310.
- **What stays dirty after the obvious cuts** (session, notification and
  fence back-pointers, the operation's analysis host and replay slot, the
  three `*StatementScope` back-references, the owner's FDW and exclusion
  values, the journal's index slot): `sqlCursor` holds two
  `AuthorizationView` values, which stay in the root (authorization
  prompt), and through it `protocolPortal`, the prepared registry, the
  owner's cursor cleanup and `Session` reach the root.
  `sessionNotifications` holds `*Engine` and its queue is guarded by
  `Engine.mu`; the 2PC records are held only by `Engine.prepared`.
- **Feature types inside moving structs:** the journal's
  `typedef.EnumJournal`/`DomainJournal` and index slot, `Session.sequences`
  (`sequence.SessionMemory`), the owner's `foreign` and `exclusionModes`,
  and the savepoint exclusion projection.
- **Layering.** `analyze` imports `typedef`, `sequence`, `routine` and
  `extension` (§3 rule 6). Part B puts handlers that import `session` into
  those packages, so `session` must never import `analyze` or a feature
  package: either would form a cycle. `StatementDescription` holds
  `analyze.ParameterInfo` and `*sequence.Analysis`, so it cannot move.
- **Driver.** It calls `Session.Engine()` at six call sites (`conn.go`,
  `tx.go`, `copy.go`, `control.go`).
- **What session code needs from the scope is small.** Timeouts compare
  `sessionTimeout.scope` and `Operation.timeoutScope` by identity and write
  only `timeoutRef`; the owner's `copyScope` is identity only. No moving
  code needs to get back from session state to the root scope.

Decisions:

1. **State moves, orchestration stays.** `session` gets `Session`,
   `Operation` and the copy-input context; the transaction owner's state,
   with the owner methods that stay clean (`inBlock`, `retainsTransaction`,
   `truncateSavepoints`, `acquireAdmission`); savepoint marks; the
   `catalogChanges` journal; timeouts; notices; `ProtocolCycle`; the
   `Notification` and `notificationAction` records; the settings store; and
   the value types `Tx*`, `Isolation`, `TxOptions`, `EndReason`,
   `ResourceKind` and `SessionStart`. Engine orchestration stays in the
   root. The other owner methods become root functions (for example
   `clearTransaction(t)`, which retires cursors and portals).
2. **`StatementScope` stays whole in the root** as the dispatch type. A
   core split would buy 16 methods for about 2.5k exports and about 70 of
   the 85 test literals (51 files). Instead `session` declares a small
   `StatementLease` value (statement identity and `TimeoutRef`), held as a
   named scope field; it replaces `Operation.timeoutScope`,
   `sessionTimeout.scope` and the owner's `copyScope`, and the scope fields
   the timeout code only reads (`op`, `message`, `txRef`,
   `transactionStart`) become arguments. Part B feature handlers reach the
   scope through per-consumer host interfaces on `statementHost` (§7
   decision 8), not by naming the scope; they need hosts anyway because
   `CatalogStage` stays in the root.
3. **Stay in the root:** SQL cursors, protocol portals, the prepared SQL
   registry, `sqlRoutines` and `StatementDescription` (execution runtime and
   analysis output), the notification listener (`sessionNotifications`) and
   the 2PC records. `Session` holds them through one root session payload,
   allocated with the session; the owner holds the foreign participants,
   exclusion modes and cursor cleanup through one root owner extension.
   Prepared SQL rules therefore stay in `AGENTS.md`.
4. **Slot typing (§7 decision 7): one interface per slot.** Each slot is a
   `session` interface with an exported marker method (and `Clone` where
   savepoints or the copy-on-write journal copy it); its owner recovers the
   concrete type with one checked helper; a typed nil is never stored, so a
   nil test keeps its meaning. Root-only slots with the same lifetime
   collapse into one payload per struct (decision 3). The fence list
   becomes `[]session.Fence` (`Kind()`); `catalogPreflightLocked` recovers
   the root fence with a checked assertion and keeps the kind order and
   recording order. No generic `session.Slot[T]`: a lower struct cannot
   name the slot types, a typed-key table needs a registry and `[]any`, and
   the slots' clone and reset rules differ.
5. **Settings store (§7 decision 10): `session`.** `sessionSettings` holds
   the `*Session` back-pointer, role setting actions and the owner's
   characteristic projections, and its mutex sits between `Engine.mu` and
   `session.mu` in the lock order; `settings` is L1. `settingAccess` (it
   reads `AuthorizationView`) stays in the root.
6. **Facade types (§7 decision 11), applied in 13e.** The driver and
   `cmd/stuntgres` import `session` directly for `Session`, `SessionStart`,
   `EndReason`, `ResourceKind`, `Tx*`, `TxOptions`, the isolation constants
   and `ProtocolCycle`, with no permanent aliases (as `settings` in 8c,
   `exec` in 11d and `analyze.ParameterInfo` in 12e). `StatementScope`,
   `StatementDescription`, `Result`, the other `Protocol*` and
   `CopyProtocol*` types, `CopyBoundary` and `FrameImporter` stay root
   types. Deviation: `Session.Engine()` goes away (a lower type cannot
   return `*Engine`), so the driver keeps its own engine field; the facade
   is not source-compatible at those six call sites.
7. **Layer line.** `session` imports `exec`, `settings` and L1–L5 only,
   never `analyze` or a feature package (§3).

### 13d — Move (several sessions)

Six sessions, in this order. Each re-measures its move set with `reach` and
`frontier`, checks with `go list` that `session` imports neither `analyze`
nor a feature package, runs `make bench`, and runs `RACE=1` selections for
timeouts, COPY, cancellation and 2PC.

#### 13d.1 — Journal (broad; creates `session`)

1. The journal's `enum`, `domain` and `indexesSlot` become `session` slot
   interfaces with a marker and `Clone`; the typedef journals and the index
   journal implement them; checked helpers recover them. Index maps stay
   deep-copied (savepoint marks keep old journals).
2. `fences` becomes `[]session.Fence`; root fences keep `currentLocked`
   and are recovered by assertion in `catalogPreflightLocked`
   (`catalog_fence_test.go` keeps passing unchanged).
3. Export the journal members the root reads (about 310 references).
4. Create `session`, move `catalogChanges`, `catalogStorageChange`,
   `catalogDependencyObservation` and the clone helper, with aliases.

**13d.1 result** (measured at `96460e8a2`). `session` holds `Journal` (was
`catalogChanges`; 19 fields, all exported, `indexesSlot` became `Indexes`),
`StorageChange`, `DependencyObservation`, `NewJournal`, `Journal.Clone`
(absorbing `cloneDefaultChanges`; a nil journal clones to an empty one) and
`AddFence`; it imports only `ids`, `authorization`, `catalog`, `storage`,
`access` and `sqltype`. Slot interfaces `EnumSlot`, `DomainSlot`,
`IndexSlot` (marker plus `Clone`) and `Fence` (`Kind() FenceKind`).
`typedef.EnumJournal`/`DomainJournal` implement the slots, so `typedef` now
imports `session` (planned for Part B; acyclic). The root keeps the index
journal, the fence types, their kinds and check order, and the helpers
`enumJournalOf`/`enumSlotOf`, `domainJournalOf`/`domainSlotOf`,
`indexJournalOf`/`indexSlotOf`; the four slot accessor methods became root
functions (`journalIndexes`, `journalIndexesOrZero`, `domainWritable`,
`enumWritable`). Call sites of `newCatalogChanges`/`cloneCatalogChanges`
name `session.NewJournal`/`Clone` directly.

Deviations: `catalog_fence_test.go` changed in two lines (its probe fence
implements `Kind() session.FenceKind` instead of `fenceKind()`); a foreign
fence in `catalogFencesCurrentLocked` returns an internal error.

Tests: new `session/journal_test.go` (`TestJournalCloneOfNilIsEmpty`,
`TestJournalCloneKeepsNilSlotsNil`,
`TestJournalCloneCopiesMapsAndSlotsAndSharesFences`). Engine unit tests were
rewritten in place by the tools (slot reads through the helpers, exported
members, `session.NewJournal`/`Clone`), for example
`catalog_type_journal_test.go`, `enum_evolution_test.go`,
`domain_alter_message_test.go` and `driver_batch_role_retirement_test.go`;
`temporary_table_catalog_test.go` renamed a local `session` variable that
shadowed the package. No test moved or was deleted.

#### 13d.2 — Statement lease and small cuts (in place)

May run in parallel with 13d.1 (it touches the timeout, COPY admission and
authorization files, not the journal).

1. Add `StatementLease`; replace `Operation.timeoutScope`,
   `sessionTimeout.scope` and `transactionOwner.copyScope`; pass the read
   fields to `startProvisionalTimeout` and `finishScopeTimeout`.
2. `settingAccess` and `Operation.Authorization` become root functions.

**13d.2 result.** `StatementLease` (`statement_lease.go`, still in the root;
moves in 13d.6) holds `Statement any` (the owning scope, recovered with
`scopeOfLease`) and `TimeoutRef` (was the scope's `timeoutRef`; `Session.mu`
guards it). `StatementScope.lease` is a value field set in
`newStatementScope`, so no allocation is added. `Operation.lease`, the
timeout slot's `lease` and `transactionOwner.copyLease` replace the three
back-pointers and compare by lease address; a copied scope's lease has
another address, as the copy's pointer did. `startProvisionalTimeout(lease,
op, transactionStart)` and `finishScopeTimeout(lease, op, message, txRef,
failed)` take the fields they read. `stageMessageCatalog` and
`insert_conflict_update_lock_test.go` recover the scope with `scopeOfLease`
(the lease's back-reference, per §3 rule 7). `operationAuthorization(op)`
replaces the method (30 production and 103 test call sites, rewritten with
`meth2fn`); `settingAccess` was already a function and moved to
`parameter_permission.go`.

Tests: adapted in place, none moved or deleted (`session_timeout_test.go`,
`session_timeout_pending_test.go`, `copy_admission_test.go`,
`copy_binary_test.go`, `protocol_copy_scope_test.go`,
`protocol_copy_input_start_test.go`, `protocol_simple_test.go`,
`insert_conflict_update_lock_test.go`, and the `operationAuthorization`
call sites).

#### 13d.3 — Engine edges (broad)

1. A `session.Host` interface, implemented by a root adapter over the
   engine, for what moving code needs from it: session end maintenance
   (TEMP collations; outside `s.mu`, never inside a `SessionResource`
   callback), unregistering from `Engine.sessions` (under `s.mu`, takes
   `Engine.sessionMu`), serializable enrolment for characteristic sync, the
   lock manager and the setting definitions. Document each method's lock
   contract.
2. `engineOf(session)` replaces `Session.Engine()` in the root (about 220
   call sites; `s.op.session.Engine()` becomes `s.engine()`); the driver
   keeps an engine field.
3. Owner methods that reach the engine, FDW or cursor code become root
   functions; the owner's `EndSession` becomes a root session resource.

**13d.3 result** (measured at `3438acb6a` with `frontier` over the decision 1
types). `session.Host` (`session/host.go`) has two methods:
`EndMaintenance(ids.SessionOwner) error` (TEMP collations; after the
Transaction callback, without `Session.mu`, never inside a `SessionResource`
callback) and `Unregister(ids.SessionToken, ids.SessionOwner)` (under
`Session.mu`, takes only `Engine.sessionMu`). The root implements it with
`sessionHost` (`session_host.go`), a pointer conversion of `*Engine` (no
state, no allocation, works for test `&Engine{}` literals); `Session.engine`
became `host session.Host`. `engineOf(session)` (nil for a host-less test
session, as `Engine()` returned) and `StatementScope.engine()` replace
`Session.Engine()`, which is gone: 316 engine call sites rewritten with
`meth2fn`, 204 of them to `s.engine()`/`scope.engine()`. The driver's `conn`
and the `cmd/stuntgres` client (`c.db.engine`) and `bootstrapExecute` keep
their own engine. Root functions (`meth2fn`): `clearTransaction`,
`drainCursorCleanup`, `commitForeign`, `foreignPending`, `foreignSavepoint`,
`retireProtocolPortalsAfter`, `captureSavepoint`, `restoreSavepoint`,
`finishNotificationBoundary`, `syncCharacteristics`. The owner's session end
is `transactionSessionEnd` (a pointer conversion of the owner, attached as the
Transaction resource) calling `endTransactionSession`. `validate` and
`acquireAdmission` compare against `t.session.token.Domain()`, which is the
engine's domain by construction; `validateSessionOperation(domain, op)` holds
the body of `Engine.validateOperation`. Bench unchanged (about 820 µs/op,
5014 allocs).

Deviations: the Host has no serializable enrolment, lock manager or setting
definitions: after the cuts no moving code needs them
(`syncCharacteristics` and savepoint capture became root functions,
`sessionAdvisory` holds the lock manager, `sessionSettings.initialize` takes
the definitions). Exporting the session and owner mutexes stays in 13d.5.

Remaining engine edges of the moving types, for 13d.4: `Session.operation`
builds the operation's `bindingHost` with `engineOf` (the analysis slot), and
`Session.pollNotification` (with `WaitForNotification` and
`PollProtocolNotification`, which the driver and `cmd/stuntgres` call) reads
the listener queue under `Engine.mu`: it is listener code and stays with the
root notification payload.

Tests: new `session_host_test.go` (`TestSessionHostRecoversEngine`,
`TestSessionHostUnregisterKeepsOtherOwner`). Adapted in place, none moved or
deleted: the `engineOf`/`meth2fn` rewrites across engine tests,
`session_test.go` (a `Session` literal sets `host`), `savepoints_test.go` and
`settings_local_lifecycle_test.go` (call `endTransactionSession`), and the
root `session_adapter_mechanics_test.go` and `transaction_terminal_test.go`
(compare the connection's engine field).

#### 13d.4 — Root payloads (in place)

1. The root session payload: cursors, prepared registry, `sqlRoutines`,
   `sequences` and the notification listener, allocated with the session.
2. The root owner extension: foreign participants and cleanup, exclusion
   modes, cursor cleanup. `markNext` becomes a generic savepoint serial in
   the owner. The savepoint exclusion mark becomes a slot that is nil when
   zero.
3. The operation's `replaySlot` and `analysis` become slots, without a new
   allocation per operation (the root allocates operations through the
   host).

#### 13d.5 — File splits and exports (broad)

Split the mixed files (`transaction.go`, `session.go`, `session_timeout.go`,
`notifications.go`, `savepoints.go`, `protocol_cycle.go`, the settings
store) into moving and root files; export the moving members the root reads
(about 2.1k references: `Operation` about 1k, `Session` about 440, owner
about 380, settings); key positional test literals (`keylit`).

#### 13d.6 — Move (broad)

One scripted session (it may merge with 13d.5, as 12d.6 did): move the
types of decision 1 with aliases.

### 13e — Remove the aliases (broad)

Use the alias tool. Apply decision 6: the driver and `cmd/stuntgres` import
`session` directly; no permanent facade aliases.

## Stays in the engine

`executeStatement` and the statement-kind dispatch, feature handlers until
Part B of their feature steps, the public `Engine` facade (`NewWithSettings`,
`StartSession`, `Reset`, `Dump`, `Restore`, ...), and native control
(postponed `control`). After the 13c refine also: `StatementScope` (whole),
the cross-subsystem orchestration (begin, end, publication, preflight, 2PC
install, notification publication, settings application, prepared
re-analysis), SQL cursors, protocol portals, the prepared SQL registry,
`StatementDescription`, the notification listener and the 2PC records.

## Invariants

- One transaction-isolation owner; snapshot retention as in the `visibility`
  README; CHAIN and abort rules from `AGENTS.md` unchanged.
- Lock order: `Engine.mu`, session and owner mutexes, the lock manager last;
  never the lock manager's mutex under `Engine.mu`. Document any change in
  the `lock` README.
- Catalog, storage and visibility publication stay atomic under `Engine.mu`;
  fence validation keeps its order and messages.
- Statement and session teardown release every resource on every exit;
  held cursors copy exactly the state they copy today.
- Prepared SQL rules in `AGENTS.md` (analysis-identity cells, invalidation,
  shadow retention) are unchanged.
- A `StatementLease` keeps the identity semantics of the scope pointers it
  replaces: one lease per scope, compared by address, never copied.
- A nil slot interface means "none", as a nil slot pointer does today; a
  slot `Clone` copies exactly what the journal or savepoint copies today.

## Targeted tests

- Engine units: grep `*_test.go` for each moved field name, `StatementScope{`
  and `Operation{`. Run with `RACE=1` for sessions, cancellation,
  transactions, savepoints, 2PC, cursors, notifications and timeouts.
- Reference selections (quick mode and `REFERENCE=1`; grep `reference/`):
  transactions and savepoints, isolation levels, 2PC, LISTEN/NOTIFY, cursors
  (WITH HOLD), prepared statements and invalidation, protocol (extended
  query, COPY), TRUNCATE, foreign keys, materialized views, inheritance and
  partition DDL, domains, timeouts, DISCARD, native Dump/Restore.
- `make bench` after 13a, 13b and each 13d session.

## Documentation

- Create `internal/engine/session/README.md` (13d): what it owns, lock order,
  slot rules, the dispatch boundary, `## Testing` with selectors moved from
  `internal/engine/README.md` (it has a "Server protocol ownership" section).
- `docs/TRANSACTIONS.md`, `docs/QUERIES.md`, `docs/DEVELOPMENT.md` and the
  design notes that name `StatementScope`, `transactionOwner` or
  `catalogChanges` (grep `docs/design`).
- `AGENTS.md`: add `session` to the shared subpackage bullet. The prepared
  SQL bullets ("Ordinary prepared SQL", "Prepared shadow retention") stay:
  the prepared registry stays in the root (13c refine). The
  transaction-isolation rule stays (it is cross-cutting).
- The `lock` README: the `session.Host` callback contracts and the exported
  session and owner mutexes (13d.3).

## Done when

`StatementScope`, `Operation`, the journal, `CatalogStage` and
`transactionOwner` hold no feature fields. `session` imports no feature
package, not `analyze`, and nothing above L9. The root package keeps the
facade, the statement scope, dispatch and the root payloads of 13c decision
3. The checks in §8.3 pass, including `RACE=1` selections.

## Status

- [x] 13a — Statement scope slots (`21d5f3a49`, `a4219195b`, `dd7faf016`,
  `dd018885f`, `df6045183`, `a067c88db`, `40dc0f177`, `3271f3ea1`,
  `dd2394458`, `ef136c2c5`, `c23fe3429`, `a08db5ff1`, then the docs commit)
  *(selected unit/`RACE=1` and dual-mode reference proof and quickcheck
  passed; the only `REFERENCE=1` failures, PostgreSQL-side
  `PartitionExclusion*` and PG-only `SerialDefaultProviderPublicationHold`,
  also fail on `master`; racecheck/finalcheck skipped at user request)*
- [x] 13b — Operation, journal, stage and owner slots (`3ce970c39`,
  `43237d634`, `45992db99`, `f37f352f3`, `8915f5271`, `92ae3b8f3`, then the
  docs commit) *(selected unit/`RACE=1` and dual-mode proof and quickcheck
  passed; racecheck/finalcheck skipped at user request; the only
  `REFERENCE=1` failures, PostgreSQL-side `PartitionExclusion*`, also fail
  on `master`)*
- [x] 13c — Refine (docs only; the commit that records this text)
  *(measured at `3fbd7d241`; quickcheck passed; racecheck/finalcheck
  skipped at user request)*
- [ ] 13d — `session` created and moved (sessions per the 13c plan)
  - [x] 13d.1 — Journal (creates `session`) (`548b81e54`, `353ec50ae`,
    `7cc803313`, `2eb0cefb3`, then the docs commit) *(selected unit/`RACE=1`
    and dual-mode proof (47 `REFERENCE=1` tests: timeouts, COPY admission,
    savepoints, 2PC, enum, index) and quickcheck passed; bench unchanged,
    about 830 µs/op and 5014 allocs; racecheck/finalcheck skipped at user
    request)*
  - [x] 13d.2 — Statement lease and small cuts (`d31baeb58`, `00fec2882`,
    then the docs commit) *(same proof as 13d.1, run on the combined
    branch)*
  - [x] 13d.3 — Engine edges (`41dc92a34`, `12273fdb0`, `ff9eaf1a6`,
    `0ae40ee4e`, then the docs commit) *(selected unit/`RACE=1` and dual-mode
    proof (60 `REFERENCE=1` tests: timeouts, COPY, savepoints, 2PC,
    LISTEN/NOTIFY, FDW transactions, TEMP collation teardown) and quickcheck
    passed; bench unchanged; racecheck/finalcheck skipped at user request)*
  - [ ] 13d.4 — Root payloads
  - [ ] 13d.5 — File splits and exports
  - [ ] 13d.6 — Move
- [ ] 13e — Aliases removed

Outstanding aliases: `catalogChanges`, `catalogDependencyObservation` and
`catalogStorageChange` (`session_aliases.go`, 13d.1).

Notes for later steps:

- 13d.1, for 13d.3–13d.6: the journal's methods now live in `session`, so
  root code cannot add methods to `catalogChanges`; add root functions.
  Fence kinds stay `session.FenceKind` constants in `catalog_fence.go`.
- 13d.2, for 13d.4–13d.6: `StatementLease.Statement` is the only route from
  session-layer state back to the scope (`scopeOfLease`). `copyDone` stays
  next to `copyLease` on the owner.
- 13d.3, for 13d.4–13d.6: moving code reaches the engine only through
  `Session.host`; root code uses `engineOf`. When `Session` moves, tighten the
  `Host` parameters from `ids.SessionOwner` to `*session.Session`. Notification
  polling (`pollNotification`, `WaitForNotification`,
  `PollProtocolNotification`) must leave `Session` with the listener payload
  (13d.4), so the driver and `cmd/stuntgres` call sites change then.
  `validateSessionOperation` moves with `Session`.

- Part B of the feature steps (14c–17d) and the postponed packages need 13d.
  After the 13c refine their handlers do not name `StatementScope` (it
  stays in the root): each declares a host interface implemented on
  `statementHost` (§7 decision 8), and root dispatch passes the feature's
  slot. A feature that owns a journal or owner slot implements the `session`
  slot interface (13c decision 4).
- The FDW write work (`fdw_*.go`, `b54eae5ef`) added the four foreign-table
  scope fields and the three owner fields listed above; it is included in
  these measurements. Feature work keeps adding fields, so re-measure in 13a.
- 13a moved no tests; unit tests that set scope fields directly were
  rewritten in place by the selector tool (for example
  `conversion_lookup_test.go`, `serial_sequence_test.go`,
  `alter_tablespace_prefix_test.go`).
- 13b kept the 15a/17a slots (`sequence`, `typed`, `domainAlter` on the
  stage; `enum`, `domain` in the journal) in their own style (field plus
  `xState()` accessor and nil-safe read methods); its new slots use the 13a
  style. 13c settles one style per struct when the slots move.
- 13b rewrote unit tests in place with the selector tool (for example
  `partition_unique_replay_test.go`, `unique_lifetime_test.go`,
  `materialized_refresh_test.go`, `fdw_transaction_test.go`); three fence
  tests now read `fences` through the `catalogFencesOf` test helper. New
  unit tests: `TestCatalogFencesCheckKindsInCallOrder`,
  `TestCatalogFencesStopAtFirstFailure` (`catalog_fence_test.go`).
- 13b, for 13c/13d: fence kinds are declared centrally in
  `catalog_fence.go` because publication owns their order; a feature
  package adding a fence must add its kind there. The 13b stays list
  (above) names the stage fields with no single owner.
- 13c, §7 decision 7: 13a uses two slot kinds (13b adds a third, the
  journal's ordered fence list). Value groups (`inquiry`,
  `prepared`, `sqlRoutine`, `inheritance`, `domains`) are written by every
  statement; pointer slots are feature-only. Keep that split when slots
  move with their features.
