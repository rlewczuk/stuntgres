# internal/engine/session

Session-layer state of the engine, at layer L9 of
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture).
Step 13d moves it here in several sessions
([ENGINE-SPLIT-session.md](../../../docs/ENGINE-SPLIT-session.md)); so far
(13d.1) it holds the transaction catalog journal and the interfaces that let
the journal carry feature state, (13d.3) the `Host` interface through
which session code calls back into the engine, and (13d.4) the root slot
interfaces through which sessions, operations, transaction owners and
savepoint marks hold root state. Sessions, operations, the transaction owner,
timeouts and the settings store follow in 13d.5–13d.6.

The package imports only `ids`, `authorization`, `catalog`, `storage`,
`access` and `sqltype` (later also `exec` and `settings`). It never imports
`analyze`, a feature package (`routine`, `typedef`, `extension`, `sequence`,
...) or the root engine: feature packages import `session` to implement its
slot interfaces, so either import would form a cycle.

## What it owns

- **The journal** (`journal.go`): `Journal`, a transaction's private catalog
  overlay (descriptors and name bindings with their originals, default ACLs,
  sequence generators, private relation storage as `StorageChange`, observed
  dependency targets as `DependencyObservation`, the cached private read,
  role overlays, fences and feature slots). `NewJournal`, `Clone` (a nil
  journal clones to an empty one) and `AddFence`.
- **Fences** (`fence.go`): `Fence` with `Kind() FenceKind`. The root engine
  declares the kinds, the fence types and the order in which publication
  checks them.
- **Journal slots** (`journal_slots.go`): `EnumSlot` and `DomainSlot`
  (implemented by `typedef.EnumJournal` and `typedef.DomainJournal`) and
  `IndexSlot` (the root's index journal). Each has an exported marker method
  and `Clone`.
- **Root slots** (`root_slots.go`): `PayloadSlot` (a session's SQL cursors,
  prepared SQL registry, retained SQL routine analyses, sequence session
  memory and notification listener), `OwnerSlot` (a transaction owner's
  foreign participants and cleanup, exclusion modes and detached cursors),
  `ExclusionMarkSlot` (a savepoint's exclusion modes and pending projection,
  nil when both are zero), `ReplaySlot` (an operation's Restore and
  materialized population state) and `AnalysisSlot` (an operation's binding
  host). Each has only an exported marker method; the root implements them
  with `sessionPayload`, `ownerExtension`, `exclusionMark`, `restoreReplay`
  and `bindingHost`.
- **The engine host** (`host.go`): `Host`, what session code needs from the
  engine that owns a session: `EndMaintenance` (TEMP collation teardown,
  called after the Transaction resource callback returns, without
  `Session.mu` and never from inside a `SessionResource` callback) and
  `Unregister` (called under `Session.mu`; takes only `Engine.sessionMu`).
  Both take the session as an `ids.SessionOwner`. The root implements it with
  `sessionHost`, a pointer conversion of `*Engine`, and recovers the engine
  with `engineOf(session)`.

## Invariants

- **No mutex, no orchestration.** The transaction owner holds the journal;
  statement admission serializes writers, and the root engine validates and
  publishes it under `Engine.mu` (§3 rule 4). Nothing here takes a lock.
- **Copy-on-write.** A statement writes a `Clone` and installs it; savepoint
  marks keep older journals. `Clone` copies the maps, clones each non-nil
  slot through its `Clone`, and shares fences (immutable once recorded) in
  recording order.
- **Slot rules.** A nil slot means "none". Owners store and recover a slot
  with one checked helper each (`enumSlotOf`/`enumJournalOf`,
  `domainSlotOf`/`domainJournalOf`, `indexSlotOf`/`indexJournalOf` in the
  root) and never store a typed nil. A slot owner that writes in place (the
  index journal) deep-copies in `Clone`; the typedef slots are replaced
  before a write (`domainWritable`, `enumWritable`).
- **Root slot allocation.** The root allocates a session, its payload and its
  owner extension in one allocation (`sessionAllocation`), and an operation
  with its binding host in another (`newSessionOperation`); both slots are
  set before the value is shared and never change, so neither adds an
  allocation. The replay slot is allocated on first write; the exclusion
  mark only for nonzero state. Root helpers: `payloadOf`,
  `ownerExtensionOf`/`ownerExtensionOrZero`, `exclusionMarkSlotOf`/
  `exclusionMarkOrZero`, `operationReplay`/`operationReplayOrZero` and
  `Engine.analysisHost`.
- **Fence order.** Publication checks fences by kind at fixed points, each
  kind in recording order, so the first error does not depend on storage.

## What stays in the engine

Everything that reads the journal against engine state: the private catalog
read (`privateCatalogLocked`), descriptor recording (`catalogChangeLocked`),
preflight and publication (`catalogPreflightLocked`,
`transaction_publication.go`), private storage projection, the fence types and
their `currentLocked` checks, the index journal type, the root slot types
(SQL cursors, protocol portals, the prepared SQL registry, the notification
listener and its polling on `Engine`, FDW participants, exclusion state,
Restore replay, the binding host), and the temporary type aliases in
`session_aliases.go` (removed in 13e).

## Testing

```sh
make check RUN='^TestJournal'
make check RACE=1 RUN='^TestSessionHost'
make check RACE=1 RUN='^(TestSessionPayload|TestOwnerExtension|TestExclusionMark|TestOperation(AnalysisSlot|ReplaySlot))'
make check RUN='^(TestCatalogFences|TestCatalogTypeJournalSlotsCopyOnWrite|TestEnumProvenanceSavepoints)'
```
