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
carrying feature fields. Then sessions, operations, the statement scope core
and the transaction owner move into `session`, which imports no feature
package. The root package keeps the public facade and statement dispatch.

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

### 13d — Move (broad; several sessions)

Follow the 13c plan. Each session exports in place, moves, and leaves
aliases. Keep the public facade source-compatible: the root driver keeps
compiling against `engine.Session`, `engine.Operation` and
`engine.StatementScope` until 13e, through aliases or wrappers.

### 13e — Remove the aliases (broad)

Use the alias tool. Decide for each public facade name whether the driver
imports `session` directly or the engine keeps a permanent facade alias (§3
rule 2).

## Stays in the engine

`executeStatement` and the statement-kind dispatch, feature handlers until
Part B of their feature steps, the public `Engine` facade (`NewWithSettings`,
`StartSession`, `Reset`, `Dump`, `Restore`, ...), and native control
(postponed `control`).

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
- `AGENTS.md`: add `session` to the shared subpackage bullet. Move the
  prepared SQL bullets ("Ordinary prepared SQL", "Prepared shadow retention")
  into the `session` README when the prepared registry moves, leaving at most
  one line. The transaction-isolation rule stays (it is cross-cutting).

## Done when

`StatementScope`, `Operation`, the journal, `CatalogStage` and
`transactionOwner` hold no feature fields. `session` imports no feature
package and nothing above L9. The root package keeps only the facade and
dispatch. The checks in §8.3 pass, including `RACE=1` selections.

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
- [ ] 13c — Refine (docs only)
- [ ] 13d — `session` created and moved (sessions per the 13c plan)
- [ ] 13e — Aliases removed

Outstanding aliases: none yet.

Notes for later steps:

- Part B of the feature steps (14c–17d) and the postponed packages need 13d:
  their handlers take the statement scope.
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
