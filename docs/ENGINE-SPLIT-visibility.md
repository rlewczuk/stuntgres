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

- [ ] 7a — `visibilityRegistry` state ownership
- [ ] 7b — `visibility` created and types moved
- [ ] 7c — Aliases removed

Outstanding aliases: none yet.

Notes for later steps: none yet.
