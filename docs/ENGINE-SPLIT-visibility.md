# Engine split, step 7: `internal/engine/visibility`

Session prompt. Start a fresh session with "Implement
`docs/ENGINE-SPLIT-visibility.md`." Follow the shared rules in
[ENGINE-SPLIT.md §8](ENGINE-SPLIT.md#8-implementation-sessions): protocol,
constraints, verification, documentation and report.

- **Plan:** [ENGINE-SPLIT.md](ENGINE-SPLIT.md) §5 step 7; the visibility row
  of the layer table and the paragraph after it in §2.3; dependency rule 3 in
  §3.
- **Needs:** step 4 (`lock`) and step 6 (`storage`), both complete. Read the
  lessons recorded in their prompt files and in
  [`ENGINE-SPLIT-catalog.md`](ENGINE-SPLIT-catalog.md).
- **Effort:** xhigh for 7a (concurrency and atomic publication); high for 7b
  and 7c.
- **Session size:** one sub-step per session.

## Goal

Transaction identity, commit order and snapshot resolution become a package
that imports only `storage`, `catalog`, `lock`, `ids`, `sqltype`, `sqlstate`
and the standard library.

**Moves.** At `a2252c0b1` these were:

- the visibility registry and xid assignment, identity history and horizons
  (`transaction_visibility_registry.go`, `transaction_identity_store.go`);
- the commit sequence and commit timestamps (`commit_timestamp*.go`);
- `VisibilityView` and `visibilityData`, which resolve snapshots over storage
  roots (`transaction_visibility.go`);
- `transactionRoot`, the repeatable-read retention (`transaction_isolation.go`);
- the serializable tracker (`serializable*.go`), including safe-snapshot
  blocking.

**Stays in the engine.** `transactionOwner` (the per-session state machine),
savepoints, the `catalogChanges` journal, prepared transactions (2PC),
`StatementScope`, and the statement and transaction-owner entry points.

## Sub-steps

### 7a — State ownership in place

- Group these `Engine` fields into one engine-local `visibilityRegistry`:
  `commitSeq`, `visibilityXID`, `visibility`, `assignedXID`,
  `transactionHistory`, `transactionHorizon`, `assignedLowIDs`,
  `trackCommitTimestamp`, `commitTimestampBase`, `commitTimestamps`,
  `lastCommittedXID`, `lastCommitTimestamp` and `serializable`. In
  refinement, decide where `diagnosticTx` belongs.
- The `Engine` methods that read these fields (about 40 sites at
  `a2252c0b1`) become registry methods. Methods that also need the
  transaction owner, statement scope or catalog publication stay on `Engine`
  as orchestration and call the registry.
- No mutex in the registry. Mutating methods carry the `Locked` suffix or
  document that the caller holds `Engine.mu`. Follow the lock order recorded
  in the `lock` README. Never take the lock manager's mutex under `Engine.mu`.

### 7b — Create `visibility` and move

- Move the types and the registry as `visibility.Registry`.
- `visibility` imports `lock` only for safe-snapshot blocking and ticket
  identity. If anything else pulls in `lock`, stop and record it.
- Leave aliases in the engine.

### 7c — Remove the aliases

Remove them with a tool aware of `go/types`. Do not run this in parallel with
another broad rewrite (§8).

## Invariants

- Commit ordering and publication stay atomic with catalog and storage under
  `Engine.mu`.
- xid assignment stays monotonic, and the low-32-bit alias guard is unchanged.
- Repeatable read retains every immutable relation and end root before
  analysis waits. Read committed and read uncommitted views stay fresh at
  grant time. Snapshot retention never copies row values.
- SSI conflict detection, safe-snapshot behavior, commit-timestamp output and
  xid status functions stay identical.

## Targeted tests

- Visibility, isolation, serializable, commit-timestamp, xid and 2PC unit
  tests, always with `RACE=1`.
- `make bench` after each sub-step.
- 7a and 7b: reference selections for each isolation level, serializable
  anomalies and DEFERRABLE safe snapshots, commit timestamps, xid functions,
  2PC and concurrent DML visibility, run with `REFERENCE=1` and in quick mode.
  Pick names by grepping `reference/`; never edit it.

## Documentation

- Create `internal/engine/visibility/README.md`: what the package owns,
  snapshot and retention rules, the caller-holds-`Engine.mu` contract and lock
  order, what stays in the engine, and `## Testing` selectors moved from
  `internal/engine/README.md`.
- Design notes and docs: `PG18-ARCH-TX-VISIBILITY.md` and
  `PG18-ARCH-SERIALIZABILITY.md` first, then `docs/TRANSACTIONS.md` and
  `docs/QUERIES.md` where they name internals. Grep for more.
- `AGENTS.md`: add `visibility` to the shared subpackage bullet and drop
  "snapshots" from the `internal/engine` bullet. The "Transaction isolation
  uses one owner" rule mixes snapshot retention (now owned by `visibility`)
  with transaction-owner and CHAIN rules (still in the engine). Move the
  retention details into the `visibility` README and keep `AGENTS.md` to the
  cross-cutting part, so the rule gets shorter.

## Done when

`visibility` imports only `storage`, `catalog`, `lock`, `ids`, `sqltype`,
`sqlstate` and the standard library. `Engine` holds one `visibility.Registry`
instead of the fields above. `make racecheck` and the reference selections
pass. 7c completes the step.

## Status

- [x] 7a — `visibilityRegistry` state ownership (`f556f670c`, `e2745162f`,
  `89aed2c58`, then the commit that records this status) *(racecheck
  skipped at user request; targeted RACE=1 runs passed)*
- [x] 7b — `visibility` created and types moved (`41a569c1c`,
  `e896f75d7`, `334ddf32c`, `eb53b840a`, then the commit that records this
  status) *(racecheck skipped at user request; targeted RACE=1 runs passed)*
- [x] 7c — Aliases removed (`81766feb0`, then the commit that records
  this status) *(racecheck skipped at user request; targeted RACE=1 runs
  passed)*

Outstanding aliases: none. 7b kept 12 unexported type aliases in
`internal/engine/visibility_aliases.go`; 7c removed them all. Step 7 is
complete.

7c results:

- **Scope.** Measured on `50d842db3` with go/types: 198 alias uses in 79
  engine files, the largest `privateDeletionRoot` (66), `deleteCandidate`
  (35), `VisibilityView` (30), `serializableTransaction` and
  `serializableWrite` (17 each) and `transactionRoot` (15). No use outside
  package `engine` (the aliases were unexported). One embedded alias field,
  `deleteUsingCandidate.deleteCandidate` (`delete_using.go`), became
  `visibility.DeleteCandidate`; its selectors follow.
- **Shadowing.** None. The only local named `visibility` (a bool in
  `bindCTEMutation`, `cte_mutation_bind.go`) is in a function that never
  names the package, so neither a shadow nor a readability rename was
  needed. The field `Engine.visibility` is a selector and does not collide.
- **Rewrite** (`81766feb0`). 264 edits in 79 files, 64 imports added, and
  `visibility_aliases.go` deleted. One test comment that named
  `visibilityData` was fixed by hand. gofmt reordered imports in two test
  files.
- **Tool.** Scratchpad `visaliasx`, the 6c `storagex` tool parametrized for
  `visibility`. Each write reloads the package with its tests, requires zero
  type errors and an exact per-object use census before and after, mapped
  through the edits.
- **Tests.** Adapted mechanically by the same tool; none moved, deleted or
  rewritten by hand.
- **Docs.** Design notes already name `visibility.View` where they mean
  current code; plan-time records keep the old names. The `visibility`
  README and `AGENTS.md` are unchanged (100 lines).
- **Verification.** `make build` after the rewrite. `make check RACE=1`
  over the package seam selector of the `visibility` README, transaction
  identity, commit time, serializable, prepared transaction, isolation,
  visibility, concurrent index, blocking PIDs, `pg_locks`, DELETE, UPDATE,
  MERGE, materialized refresh, query join, cursor, copied-scope, INSERT
  conflict, inheritance, partition and RLS tests: all passed. `quickcheck`
  passed on the branch rebased onto `50d842db3`. `racecheck` skipped at
  the user's request. No `REFERENCE=1` selection is required for a pure
  rename. `make bench`: 1.49–1.59 ms/op before and 1.51–1.72 ms/op after,
  7,769 allocations and about 2.07 MB/op on both. The <1 ms target is
  already missed on `master`. `make deps` lists only module paths.
  `visibility` still imports only `catalog`, `ids`, `lock`, `storage`,
  `sqlstate` and the standard library.

7b results:

- **Decisions (user).** The surface follows the 6b split. Registry
  counters are private behind accessors. Maps, record fields and methods
  are exported. The transaction-private deletion overlay
  (`privateDeletionRoot`, `deleteCandidate`) and `liveVersion` move into
  `visibility`, because `visibilityData.deleted` holds them and
  `visibility` cannot import the engine. This reverses the 6b note that
  kept them with write staging.
- **Export in place** (`41a569c1c`). A type-checked rename (`fieldrn`)
  capitalized every field of the records to move and of the overlay:
  1,781 edits in 113 files. The 97-field use census was identical before
  and after. `VisibilityView.read` became `Data`; the new `Load` replaces
  raw `data.Load()` reads (including in `generated_virtual.go` and
  `view_locks.go`). The `visibilityData` helpers are exported as
  `GenerationAt`, `NeedsBirthFilterAt` and `FrontierAt`.
- **Methods and counters** (`e896f75d7`):
  - `methx` exported 34 registry methods (177 edits).
    `resolveSerializableCandidatesLocked` stays private, and
    `lastCommitTimestampLocked` became the `LastCommittedXID` and
    `LastCommitTimestamp` reads.
  - A new go/types `counterx` mode rewrote 456 counter uses in 123 files:
    349 `commitSeq` reads, 36 sets and 7 increments, plus the other
    counters. `CommitSeq` and `AdvanceCommitSeqLocked` serve publication,
    and each caller keeps its exhaustion check. `catalog.go`'s
    `commitSeq = frontier` (`frontier` is `commitSeq+1`) became an advance.
  - `SetCommitSeqLocked`, `SetLastAssignedXIDLocked` and
    `SetDiagnosticTxLocked` are seeding seams used only by tests.
- **Safe-snapshot seam.** The wait loop in `acquireSerializableSnapshot`
  now calls `SafeSnapshotWait`, `SafeSnapshotOutcome` and
  `AbandonSafeSnapshotLocked` instead of reading node fields. The loop,
  `replacementMu` and owner locking stay in the engine, so the step-4 note
  on `replacementMu` versus `transactionOwner.mu` is unaffected:
  `visibility` takes no mutex. `transactionStatusLocked` builds its 22023
  error through the package's own `sqlError`, as `lock` did in 4b.
- **Move** (`334ddf32c`). 87 declarations moved with the 6b `movex` tool,
  after a rebase onto B3 (two small mechanical conflicts). The package
  files are `registry.go`, `identity.go`, `commit_timestamp.go`,
  `serializable.go`, `serializable_evidence.go`, `safe_snapshot.go`,
  `predicate_locks.go`, `view.go`, `root.go`, `deletes.go` and `errors.go`.
  The engine qualifies 51 function references. Type names:
  - `visibilityRegistry` → `Registry`, `transactionVisibility` → `Entry`,
    `transactionIdentityRecord` → `IdentityRecord`,
    `commitTimestampEntry` → `CommitTimestamp`;
  - `serializableTracker` → `Tracker`, `serializableTransaction` →
    `SerializableTx`, `serializableWriteKey` → `WriteKey`;
  - `visibilityData` → `Data`, `visibilityGeneration` → `Generation`,
    `VisibilityView` → `View`, `transactionRoot` → `Root`;
  - `privateDeletionRoot` → `PrivateDeletes`, `deleteCandidate` →
    `DeleteCandidate`.

  `visibility` imports only `catalog`, `ids`, `lock`, `storage`,
  `sqlstate` and the standard library. `lock` is used only for
  `*lock.Ticket` identity and its atomics.
- **Tests:**
  - *Moved* into `visibility/serializable_graph_test.go`: six
    `TestSerializableGraph*` tests and
    `TestSerializablePrecommitRetainsAttemptedWriteReadOnlyHistory`. Their
    setup moved onto `fixture_test.go`, which mints TxRefs through
    `ids.Domain` like the lock fixture. Assertions are unchanged.
    `TestSerializableGraphCycleFirstCommitDoomsActivePivot` stays in the
    engine because it also checks the owner's state.
  - *Rewritten:* the forged-view assertions of
    `TestQueryJoinsVisibilityReleaseAuthority` and
    `TestTransactionCopiedScopeStageViewAndSnapshot`, which need the
    view's private fields, became `TestViewForgedCopyHasNoAuthority`.
  - *New seam tests:* `TestViewRetainReleaseIndependence`,
    `TestRegistryCommitSeqAdvanceAndSeed`,
    `TestNextFullXIDSkipsSpecialLowValues`,
    `TestRegistryXIDAssignmentLiveAliasGuard`,
    `TestLiveVersionOverlayAndEnds` and four `TestSafeSnapshot*` tests.
    Mutating `AbandonSafeSnapshotLocked` or `SafeSnapshotOutcome` makes
    them fail.
  - Other engine tests changed only through the mechanical rewrites. No
    test was deleted.
- **Docs.** New `visibility/README.md`. The identity paragraph and the
  identity and commit-time selectors moved there from
  `internal/engine/README.md`. Also updated: the `storage` README, the
  `VisibilityView`/`SSITracker` pointers in the TX-VISIBILITY and
  SERIALIZABILITY design notes, and `docs/QUERIES.md`. `AGENTS.md` stays at
  100 lines (13,134 bytes, 96 fewer): `visibility` joined the
  subpackage bullet, "snapshots" left the engine bullet, and the
  RR/RC retention details of the isolation rule moved into the README.
- **Verification:**
  - `make build`, plus vet of the engine tests, after each commit.
  - `make check RACE=1` over transaction identity, commit time,
    serializable, prepared transaction, isolation, visibility, concurrent
    index, blocking PIDs, `pg_locks`, driver batch, transaction controls,
    savepoint, CREATE TABLE AS serialization, notification publication,
    DELETE, UPDATE, MERGE, rule, query join, authority and cursor tests,
    plus the new package tests. All passed.
  - `quickcheck` passed after the move and on the rebased branch.
  - Reference: the 7a selection plus `DeleteCore|UpdateCore|Merge` (`dml`,
    `locking_transactions`, `sessions`) passed in quick mode. With
    `REFERENCE=1`, every stuntgres subtest passed. 22 `/postgres` subtests
    of `TestReferenceMerge(Update|Delete|SourceUpdate)RepeatableRead*`
    failed with 40001 from PostgreSQL 18.6 itself, with the same 22
    failures on `master` (`1b3a12a08`). That is oracle drift unrelated to
    this step, not yet recorded in `POSTGRES_REGRESSION.md`.
  - `make bench`: 1.72–2.25 ms/op before and 1.51–1.72 ms/op after,
    7,769 allocations and about 2.07 MB/op on both. The <1 ms target is
    already missed on `master`.
  - `make deps` lists only module paths.
  - The full `racecheck` was skipped at the user's request.

7a results:

- **Registry.** `visibilityRegistry` (`transaction_visibility_registry.go`)
  holds the 14 fields; `Engine` holds it by value as `visibility` and never
  replaces it whole. The per-transaction map `Engine.visibility` became
  `visibilityRegistry.entries`; every other field keeps its name.
  `newVisibilityRegistry(track)` replaces the `newEngine` assignments; maps
  stay lazily allocated, so a zero `Engine{}` still works.
- **`diagnosticTx`** went into the registry: it is the engine-global TxRef
  diagnostic (virtual transaction) counter, mutated only under `Engine.mu`.
  The two check-and-bump sites (`transaction.go`, `transaction_controls.go`)
  share `nextDiagnosticTxLocked`, with the same message. Minting the `TxRef`
  stays with the transaction owner.
- **Rewrite** (`f556f670c`). A go/types selector rewriter (scratchpad
  `visx`, adapted from the 6a tool) changed all 717 field uses in 144
  production and in-package test files. The census matched apart from the
  three `newEngine` assignments the constructor replaced.
- **Fence rule.** Registry methods never read `e.catalog`. Engine methods
  that open with the domain/epoch fence keep their names as thin wrappers
  over the registry, so their callers did not change:
  `visibilityWriteLocked`, `visibilitySnapshotLocked`,
  `transactionAgeLocked`, `assignTransactionXIDLocked`, and
  `enrollSerializableLocked` (which also maps `Isolation` to a bool).
  `endSerializableLocked` and `fenceSerializableLocked` stay on `Engine`
  because they also clear `indexBuildWaits`.
- **Registry methods** (36). Moved outright, callers (112 in production and
  tests) rewritten with go/types: `dropSnapshotLocked`, `resetPhaseLocked`,
  `transactionStatusLocked`, `endTransactionIdentityLocked`,
  `recordCommitTimestampLocked`, `retireCommitTimestampsLocked`,
  `lookupCommitTimestampLocked` and the SSI graph (`serializableEdgeLocked`,
  `Precommit`, `Committed`, `Prepared`, `resolveSerializableCandidatesLocked`).
  New: `attachTicketLocked` (the `registerTransactionLocks` body under
  `Engine.mu`; lock order unchanged), `writeLocked`, `snapshotLocked`,
  `withdrawSnapshotLocked`, `olderSnapshotsLocked`/`snapshotOlderLocked`
  (index-build wait cohort), `assignedXIDLocked`, `fullXIDLocked`,
  `tracksCommitTimestamps`, `lastCommitTimestampLocked`,
  `endTransactionLocked`, `fenceLocked`, `captureSerializableLocked` (node
  capture and possible-writer set), `relevantSerializableLocked`,
  `serializableCreatorLocked`, `safeSnapshotBlockersLocked` and
  `predicateLocksLocked` (plain data for `pg_blocking_pids` and `pg_locks`).
- **SSI evidence split.** `Engine.serializableReadLocked`/`WriteLocked` keep
  authority, epoch, event and catalog eligibility checks in their original
  order (read: relevance before eligibility, the cheap test first; write:
  eligibility first) and pass the relevant node to the registry. Rollback
  reconciliation passes the owner's keep predicate (pure, no locks) to
  `reconcileSerializableWritesLocked`.
- **Kept on `Engine`/`StatementScope`.** The `commitSeq` exhaustion checks
  and bumps in catalog and transaction publication (`catalog.go`,
  `transaction_publication.go`) interleave with catalog and storage
  installation and stay inline; 28 files read `e.visibility.commitSeq`
  directly. The safe-snapshot wait loop and roots capture in
  `acquireSerializableSnapshot`, `waitOlderIndexSnapshots` (lock manager
  waits) and `registerTransactionLocks` are orchestration.
- **Tests.** Adapted mechanically by the rewriter; none moved, deleted or
  rewritten by hand. No seam tests yet: they come with the package in 7b,
  as in 6b. `internal/engine/README.md` had no visibility selectors to move;
  its identity paragraph now names the registry.
- **Verification.** `make build` and `make check RACE=1` over transaction
  identity, commit time, serializable, prepared transaction, isolation,
  visibility inquiry, identifier, concurrent index, blocking PIDs,
  `pg_locks`, driver batch, transaction controls and savepoint tests after
  each commit. `quickcheck` passed. The 105 reference tests matching
  `^TestReference(TransactionIsolation|Serializable|CommitTime|TransactionIdentity|PreparedTransaction|TransactionVisibility|TransactionControlsVisibility|ConcurrentInserts|DeleteCoreVisibility|UpdateCoreVisibility|InsertConflictSnapshotVisibility|RowLock(ReadCommitted|RepeatableRead)|BlockingPidsSafeSnapshot)`
  (`dml`, `locking_transactions`, `sessions`) passed with `REFERENCE=1` and
  in quick mode. The full `racecheck` was skipped at the user's request.
  `make bench`: 1.55–1.61 ms/op before, 1.51–1.63 ms/op after, 7,768
  allocations and about 2.07 MB/op on both (the <1 ms target is already
  missed on `master`). `make deps` lists only module paths. Imports are
  unchanged. `AGENTS.md` unchanged (100 lines).

Notes for later steps:

- Later (open): `View.Load` returns the raw pointer without the identity
  check that `Data` and `Retain` make, as the old `data.Load()` reads did.
  Tightening it would be a behavior change.
- 7b (resolved above): decide the exported surface. `commitSeq` needs a
  read accessor and an advance method for the publication sites (keep
  their distinct exhaustion messages). `serializableTransaction` fields are still read by
  `acquireSerializableSnapshot`'s wait loop (`ready`, `resolved`, `unsafe`,
  `aborted`, `ended`, and the error path that closes `ready`); move that
  loop's node handling behind registry methods, and recheck the step-4 note
  on `replacementMu` versus `transactionOwner.mu` when safe-snapshot
  blocking moves.
- 7b: `transactionStatusLocked` uses the engine's `sqlError`, and
  `serializableError` builds a `sqlstate.Error`; `visibility` needs its own
  helper, as `lock` did in 4b.
- 7b: `safeSnapshotBlocker` and `predicateLockHolder` are plain data types
  that move with the registry; `predicateLockHolder.reads` aliases the live
  read set and is valid only under `Engine.mu`.
