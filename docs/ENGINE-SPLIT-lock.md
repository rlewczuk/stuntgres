# Engine split, step 4: `internal/engine/lock`

Session prompt. Start a fresh session with "Implement
`docs/ENGINE-SPLIT-lock.md`." Follow the shared rules in
[ENGINE-SPLIT.md §8](ENGINE-SPLIT.md#8-implementation-sessions): protocol,
constraints, verification, documentation and report.

- **Plan:** [ENGINE-SPLIT.md](ENGINE-SPLIT.md) §5 step 4; cuts 10 and 11 in
  §2.3; dependency rule 3 in §3.
- **Needs:** step 1 (`ids`), at least through 1c. Independent of steps 2 and
  3.
- **Effort:** high. Use xhigh for 4a if the lock-order audit finds a
  violation.
- **Session size:** one sub-step per session.

## Goal

The heavyweight lock manager becomes a package that imports only `ids`,
`sqlstate` and the standard library. It owns its own mutex. This step is the
pilot for the state-ownership method (§2.4), so record what worked for steps 5
to 7.

**Moves.** `relationLockManager` with its tickets, requests, policies, lock
resources and modes; multixact members and rows; unique-key buckets; the
advisory-lock manager part; deadlock detection; and the wait and timeout
mechanics. At `a2252c0b1` this was `relation_lock.go`,
`relation_lock_unique_delta.go`, `relation_deadlock.go`, `relation_timeout.go`,
`unique_lock.go`, `transaction_multixact*.go`, and parts of `advisory_lock.go`
and `blocking_pids.go` (about 2.5k lines).

**Stays in the engine.** Query lock admission, `LOCK TABLE` handling, relation
scopes (they combine locks with the catalog), the `pg_locks` projection,
safe-snapshot blocking (step 7), and `Engine.exclusionPending`, which only
references tickets.

## Sub-steps

### 4a — Port in place: cuts, encapsulation and lock-order audit

- **Cut 11.** `relationLockPolicy.operation *Operation` is used to publish the
  pending timeout request (`policy.operation.pendingTimeoutRequest.Store(r)`),
  not to ask whether a timeout is pending as §2.3 says. Replace it with the
  narrowest thing that does that job: a pointer to the atomic slot itself, or
  a one-method interface. Correct §2.3.
- **`EndReason`** (in `session.go`) becomes a lock-local enum where the lock
  manager needs it. The completion state comes from `ids`.
- **Encapsulate the mutex.** At `a2252c0b1`, nine engine files locked the
  manager's mutex directly: `discard.go`, `drop_provider.go`, `delete.go`,
  `inheritance_dml_authority.go`, `foreign_action_admission.go`,
  `index_wait.go`, `pg_locks_snapshot.go`, `update_versions.go` and
  `transaction_visibility_registry.go`. Turn each locked section into a named
  manager method that does that specific job. Never use a generic "run this
  callback under the lock" method (`AGENTS.md`: no callbacks under locks).
- **Session lookups** in lock entry points become values passed in by the
  engine.
- **Lock-order audit.** List every acquisition of `Engine.mu`, `sessionMu`,
  `replacementMu`, the manager mutex and the transaction synchronization.
  Confirm the rule stated in `transaction_visibility_registry.go` ("never take
  manager.mu under Engine.mu"; "neither manager nor TX synchronization is held
  while taking Engine.mu"). If the code violates it, stop and report the
  violation as a finding before changing anything. Write the confirmed order
  into a comment on the manager.
- Methods that touch only lock-manager state become manager methods.

### 4b — Create `lock` and move

- Move the declarations. Export the API the engine entry points need. Keep it
  small and named for what callers do, not a mirror of internal fields.
- Leave aliases in the engine only where they shrink the diff.
- Unit tests that use only lock APIs (deadlock, cancellation, timeout,
  teardown, multixact) move into `lock`. Tests that need an `Engine` stay.

### 4c — Remove the aliases and finish

Remove the remaining aliases. Check that `lock` imports only `ids`,
`sqlstate` and the standard library.

## Invariants

- Lock modes, conflict tables, queue order, deadlock detection and victim
  choice, timeout SQLSTATEs and messages, advisory-lock semantics and
  `pg_locks`/blocking-PID output stay identical.
- Every exit path releases its resources and wakes its waiters, including on
  cancellation and engine Close.
- No sleep-based synchronization in moved or new tests.

## Targeted tests

- Unit tests for relation locks, deadlock, timeout, advisory and unique locks,
  multixact, blocking PIDs and `pg_locks`, always with `RACE=1`.
- Reference selections for explicit locking, lock timeouts, deadlocks,
  advisory locks and `pg_locks`, run with `REFERENCE=1` and in quick mode.
  Pick names by grepping `reference/`; never edit it.

## Documentation

- Create `internal/engine/lock/README.md`: what the manager owns, mutex
  ownership and the confirmed lock order, the cancellation and teardown
  contract, what stays in the engine, and `## Testing` selectors moved from
  `internal/engine/README.md`.
- Design notes and docs: `PG18-ARCH-LOCKING.md` first, then
  `docs/TRANSACTIONS.md`, `PG18-ARCH-TX-VISIBILITY.md`,
  `PG18-ARCH-SESSION.md`, `PG18-ARCH-ROUTINE-EXECUTION.md` and
  `PG18-DML-INSERT-CONFLICT.md` where they name moved types or files. Grep for
  more.
- `AGENTS.md`: add `lock` to the shared subpackage bullet and drop "relation
  locks" from the `internal/engine` bullet. The general mutex rule in
  "Development rules" stays. The lock order goes into the `lock` README, not
  into `AGENTS.md`.

## Done when

`lock` imports only `ids`, `sqlstate` and the standard library. No engine code
touches the manager's mutex. The README documents mutex ownership and lock
order. `make racecheck` passes, including the moved deadlock, cancellation and
teardown tests.

## Status

- [x] 4a — Cut 11, `EndReason`, mutex encapsulation, lock-order audit
  (`78265dd49`, `d4a96abb9`, `e9c2356cd`, then the commit that records this
  status)
- [ ] 4b — `lock` created and declarations moved
- [ ] 4c — Aliases removed

Outstanding aliases: none yet.

4a results, for 4b:

- **Lock-owned files:** `relation_lock.go`, `relation_lock_advisory.go`,
  `relation_lock_blocking.go`, `relation_lock_queries.go`,
  `relation_lock_snapshot.go` (formerly `pg_locks_snapshot.go`),
  `relation_lock_unique.go`, `relation_lock_unique_delta.go`,
  `relation_deadlock.go`, `relation_timeout.go`, `transaction_multixact.go`
  and `transaction_multixact_cache.go`. Compiled as their own package they
  import only `ids`, `sqlstate` and the standard library; the only undefined
  names are `ErrCatalogReplaced` and `sqlError`.
- **Engine side of former lock files:** `advisory_lock.go` (session delegate,
  `acquireAdvisory`), `blocking_pids.go` (SQL function, PID values),
  `unique_lock.go` (key hash, comparer, `acquireUnique`) and
  `physical_row_events.go`.
- **Cut 11:** `relationTimeoutSlot` in `relation_timeout.go`; `Operation`
  embeds it as `lockWait` and the policy holds `timeoutSlot`.
- **`EndReason`:** no change. The manager never used it.
- **Mutex encapsulation:** `holds`, `missingRowGrant`, `replacementGrants`,
  `validSession` and `transactionTicket` in `relation_lock_queries.go`, with
  `indexWriters`. No engine file outside the lock files touches the manager
  mutex or maps.
- **Session lookups:** the engine checks `txOwnerAttached` and
  `sessionTokenAttached` before `register` and `registerSession`; lock code
  uses `token.Private()`.
- **Lock order:** confirmed and documented on `relationLockManager`. The
  manager mutex is innermost: taken under `replacementMu` or
  `transactionOwner.mu`, never under `Engine.mu`, `Session.mu`,
  `sessionSettings.mu` or `sessionMu`, and holding nothing else. The rule in
  `transaction_visibility_registry.go` holds for the manager. Its "TX
  synchronization" half describes only `registerTransactionLocks`:
  `transactionOwner.mu` is taken before `Engine.mu` elsewhere (about 50
  paths, never the reverse). The comment now says so.
- **Tests adapted, none moved or deleted:** `session_timeout_race_test.go`,
  `session_timeout_pending_test.go`, `inheritance_query_stored_view_test.go`,
  `insert_conflict_update_lock_test.go` (finds the worker's operation by its
  timeout slot), `blocking_pids_test.go`, `unique_test.go`,
  `primary_key_claim_test.go` and `exclusion_peer_test.go`.

Lessons for steps 5–7 (state-ownership method):

- Re-measure with a compile probe: copy the candidate files into a throwaway
  package and build it with `-gcflags=-e`. The undefined names are exactly the
  remaining upward references, which is quicker than the declaration graph.
- A lock-order audit needs callee summaries that model `*Locked` functions
  releasing and retaking `Engine.mu`. A plain held-set walk reported about
  ten times more false edges. Resolving interface calls to every
  implementation adds chains through constant folding; read each chain's
  tail before calling it a finding.
- Encapsulating a foreign mutex took few methods: most locked sections asked
  one question ("does this ticket hold this mode here?"), so five named
  queries covered nine sites.
- Move declarations with a declaration-level mover (go/ast byte spans with
  doc comments), fix imports per file, and keep each existing file's import
  style.

Notes for later steps:

- 4b: `ErrCatalogReplaced` (`catalog.go`, shared catalog provenance error) and
  `sqlError` (`engine.go`) need homes. `ErrCatalogReplaced` must keep one
  identity for `errors.Is`.
- 4b: engine code reads immutable ticket fields (`ref`, `session`,
  `persistent`, `manager`) directly; tests also read requests and entries.
  Export small accessors.
- Step 7: `acquireSerializableSnapshot` (`serializable_snapshot.go`) waits for
  a safe snapshot and retakes `replacementMu` as a reader while its callers
  (`snapshotDataPhase`, `not_null.go`, `typed_mutation.go`) hold
  `transactionOwner.mu`; other paths take `transactionOwner.mu` with
  `replacementMu` held. This does not involve the lock manager. Check it when
  safe-snapshot blocking moves.
- File lists in this prompt were measured at `a2252c0b1`; use the list above.
