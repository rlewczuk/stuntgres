# Engine split, step 1: `internal/engine/ids`

Session prompt. Start a fresh session with "Implement
`docs/ENGINE-SPLIT-ids.md`." Follow the shared rules in
[ENGINE-SPLIT.md §8](ENGINE-SPLIT.md#8-implementation-sessions): protocol,
constraints, verification, documentation and report.

- **Plan:** [ENGINE-SPLIT.md](ENGINE-SPLIT.md) §5 step 1; cuts 3–6 and 10 in
  §2.3.
- **Needs:** nothing.
- **Effort:** high.
- **Session size:** 1a–1c usually fit in one session. 1d is a separate session.

## Goal

A package `internal/engine/ids` that imports only the standard library and
holds identity values, with no behavior:

- **Catalog identity:** `OID`, `CatalogEpoch`, `DefinitionRevision`,
  `ObjectAddress`, `ObjectRef`, `NameKey`, `ObjectKind` and its constants, the
  catalog class-ID constants, and the builtin namespace OIDs.
- **Runtime identity:** `SessionID`, `SessionToken`, `TxID`, `TxRef`,
  `CommandID`, `CommitSeq`, `RowID`, `VersionID`, and the transaction
  completion state (`ids.TxCompletion`, formerly `transactionIdentityState`).
- **`Domain`:** the per-engine provenance token. Today this is `catalogOwner`,
  allocated once in `newEngine` (`engine.go`) and never replaced.

## Sub-steps

### 1a — Domain replaces the `*Engine` back-pointers (cuts 3, 5, 10)

Replace `TxRef.owner`, `SessionToken.owner` and `relationLockManager.engine`
with the engine's domain. These fields only back provenance checks. The check
sites at `a2252c0b1` were in `relation_lock.go`, `advisory_lock.go`,
`blocking_pids.go`, `pg_locks_snapshot.go`, `sequence_temp.go`,
`sequence_prepared.go`, `prepared_rebind.go`, `collation_temporary.go` and the
namespace validation in `catalog.go`. Re-grep for others.

### 1b — Owner handles replace `TxRef.attachment` and `SessionToken.private` (cuts 4, 6)

Today these fields hold `*transactionOwner` and `*Session`. Replace them with a
type defined in `ids` that the engine-side owner provides. Requirements:

- `TxRef` and `SessionToken` stay comparable map keys with exactly today's
  equality semantics (pointer identity).
- No `unsafe`.
- The engine can still reach the owner wherever it dereferences these fields
  today (`ref.attachment.state`, `.session`, `.ref`, `.catalog`,
  `token.private.token`, ...).

A likely shape is a small exported interface in `ids`, implemented by
`*transactionOwner` and `*Session`, recovered by one checked helper in the
engine. §2.3 says "marker handle embedded in" the owner; if you choose the
interface, update that wording. `relationLockManager.owners` and `.sessions`
are maps keyed by these types on hot paths, so compare `make bench` and the
lock tests before and after.

### 1c — Create `ids` and move the declarations

- Add constructors and accessors for unexported fields
  (`ObjectRef{owner: ...}` becomes `domain.Ref(epoch, address)`).
- Rewrite literals and field reads with tools (`gopls rename`, scripted
  rewrites; check `which gopls` first), not by hand. At `a2252c0b1`:
  `ObjectRef.owner` had about 60 uses, `TxRef` about 180 field uses in 58
  files, and `SessionToken` 62 in 15.
- Move the declarations and leave temporary aliases in the engine
  (`type ObjectAddress = ids.ObjectAddress`, ...).

### 1d — Remove the aliases (separate session)

Replace alias uses with `ids.` names using a tool aware of `go/types`, one
commit per type group, then delete the aliases. This touches most engine
files, so do not run it in parallel with another broad rewrite (§8).

## Out of scope

- `CatalogRead.owner` and `CatalogStage.owner` (step 5).
- `relationLockPolicy.operation` (step 4).
- `Isolation` and `TxOptions`, which stay in the engine.
- `authorization.Domain` stays separate: principal identity has its own
  Restore staging domain (see `principal_remap.go`).

## Invariants

Every check that rejects a foreign, stale, zero or pre-registration identity
today must still reject it. Keep the one-per-engine domain allocation and its
lifetime across Reset and Restore.

## Targeted tests

- Grep for the unit tests of: relation locks, advisory locks, session
  registration, TEMP sequences, `pg_locks`, blocking PIDs, catalog namespace
  validation, temporary collations and prepared rebind. Run them during the
  work, with `RACE=1` for the lock and session ones.
- New unit tests go only in `ids`, and only to pin identity semantics. For
  example: refs from different domains never compare equal, and zero values
  are invalid.
- No `REFERENCE=1` runs are needed.

## Documentation

- Create `internal/engine/ids/README.md`: what the package holds, that it has
  no behavior, the provenance rules, and a `## Testing` section.
- Docs that named these types at `a2252c0b1`:
  - `internal/engine/README.md`;
  - `docs/TRANSACTIONS.md`, `docs/QUERIES.md`, `docs/FIXTURES.md` and
    `docs/AUTHORIZATION.md`;
  - the design notes `PG18-ARCH-CATALOG-IDENTITY.md`, `PG18-ARCH-SESSION.md`
    and `PG18-ARCH-TX-VISIBILITY.md`.

  Grep for more.
- `AGENTS.md`: replace the `internal/engine/*` bullet with the shared
  subpackage bullet from §8.4. A suggestion, kept to one line: "layered
  subsystem packages (layering in `docs/ENGINE-SPLIT.md` §3); each README
  states its contract; put new code in the lowest package that can own it;
  subpackages never import the root engine." Name the existing packages in it.
  Later steps only add a package name to that bullet.

## Done when

`ids` imports only the standard library, the engine uses aliases or direct
`ids.` names, and the checks in §8.3 pass. 1d completes the step.

## Status

- [x] 1a — Domain replaces the `*Engine` back-pointers (`6dead324d` renames,
  `399abdd78` cuts)
- [x] 1b — Owner handles replace `attachment` and `private` (`f39333b2f`)
- [x] 1c — `ids` created, declarations moved, aliases in place (`9a56cf39d`
  accessors, `3993c587c` rewrite, `ebb5ee0dd` move)
- [x] 1d — Aliases removed (`282b6608a` local renames, `369dc435f` catalog
  identity, `3d31f69ba` class IDs and namespaces, `01bb004d6` `Domain`,
  `77a7d4bd0` session, `e799ad1e3` transaction, `ebdcd8c6b` row and version
  identity; `ids_aliases.go` deleted)

Outstanding aliases: none.

Corrections to this prompt, measured at the 1a start:

- `ObjectRef.owner` had 373 production and 428 test references, not about 60.
  The type-checked rewrite in 1c made 1,222 edits in 466 files.
- The 1a fields were not only provenance checks. `relationLockManager.end`
  called `t.ref.owner.removeExclusionOwner(t)`; that cleanup is now
  `Engine.endLockOwner`, which all transaction-ticket callers use.
  `Session.Engine()` and session and notification teardown read the engine
  from the token; `Session` and `sessionNotifications` now hold an `engine`
  field. `publicLockOwner` and `appendBlockingPid` take a `*Domain`.
- Comparisons against possibly-nil engine pointers (`CatalogRead.owner`,
  `AuthorizationView.engine`, unchecked receivers) use the nil-safe
  `domainOf`, so they keep the old short-circuit behavior.

Tests: no engine test was moved or deleted. Tests were adapted mechanically
(gofmt `a.locks.end(b) -> a.endLockOwner(b)`, `.domain` fixes, the 1c
rewrite). Sixteen test field writes and increments now rebuild values through
the helpers in `identity_helpers_test.go`. New: `internal/engine/ids/ids_test.go`.

Notes for later steps:

- Step 4 (lock): the manager no longer calls into the engine. Keep
  exclusion cleanup in `Engine.endLockOwner`; only the session-ticket path
  (`sessionAdvisory.EndSession`) calls the manager's `end` directly.
- Step 5 (catalog): `StatementDescription.catalogOwner *Engine` (`types.go`)
  is another engine back-pointer, compared with `CatalogRead.owner`; cut it
  together with cut 1.
- 1d: a throwaway go/types tool worked well for the 1c rewrite. It
  type-checks `GoFiles` and `TestGoFiles` from `go list -export -deps -test`
  export data and applies innermost edits until a fixpoint. Reuse the
  approach to replace alias names with `ids.` names.
- `gofmt -r` reprints whole files: apply it only to files that were already
  gofmt-clean, or the diff picks up unrelated reformatting.

1d results and lessons for later alias removals (2d, 6c, 7c):

- The type-checked rewrite qualified 14,417 identifiers in 1,021 engine
  files; the root driver (`driver.go`, `tx.go` and two tests) and
  `cmd/stuntgres/database.go` were edited by hand.
- Sixteen local variables named `ids` would have shadowed the package where
  an alias was used inside their scope. A preparatory commit renamed only
  those (`typeIDs`, `oids`, `byObjectID`, `versions`); other `ids` locals stay.
  Before removing the `expr` aliases, audit locals named `expr` the same way:
  resolve `LookupParent(<package name>, pos)` at every rewritten use.
- Rewrite by object identity (`types.Info.Uses` of the alias objects), not by
  name: fields, composite-literal keys and embedded field names resolve to
  field objects and stay untouched.
- `go vet ./internal/engine` reports existing warnings (copylocks, unkeyed
  fields, lostcancel), so use `go test -count=1 -run '^$'` as the
  per-commit compile check for test files.
