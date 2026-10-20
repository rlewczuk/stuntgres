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
   creates its own slot the same way, and 13b skips it.

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

- [ ] 13a — Statement scope slots
- [ ] 13b — Operation, journal, stage and owner slots
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
