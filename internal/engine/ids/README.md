# internal/engine/ids

Identity values shared by every engine subsystem. The package has no behavior
and imports only the standard library. It sits at layer L1 of
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture), so
any engine subpackage may import it.

## What it holds

- **Catalog identity:** `OID`, `CatalogEpoch`, `DefinitionRevision`,
  `ObjectAddress`, `ObjectRef`, `NameKey`, `ObjectKind` and its constants, the
  catalog class IDs (`*ClassID`) and the builtin namespace OIDs.
- **Runtime identity:** `SessionID`, `SessionToken`, `TxID`, `TxRef`,
  `CommandID`, `CommitSeq`, `RowID`, `VersionID` and `TxCompletion` (a
  transaction identity's completion state).
- **Provenance:** `Domain`, plus the owner handles `TxOwner` and
  `SessionOwner`.

## Provenance rules

- **One domain per engine.** `Domain` has nonzero size and is used only as
  `*Domain`, so distinct engines have distinct pointer identities. The engine
  allocates it once in `newEngine` and keeps it across Reset and Restore. A
  nil domain is invalid; zero values of every identity type have no domain.
- **Mint through the domain.** `ObjectRef`, `SessionToken` and `TxRef` keep
  their provenance fields unexported. Values come from `(*Domain).Ref`,
  `Session`, `PrivateSession` and `Tx`, and are read through accessors.
  Copying a complete value preserves provenance; rebuilding one from its
  numbers does not.
- **Equality is identity.** All three types are comparable map keys.
  Equality compares the domain pointer, the numeric fields and the owner
  handles' pointer identity. Values from different domains never compare
  equal.
- **Sealed owner handles.** `TxRef` attaches its engine-side transaction
  owner, and a private maintenance `SessionToken` names its session, as
  `TxOwner` and `SessionOwner` interface values. Only types that embed
  `TxOwnerMarker` or `SessionOwnerMarker` implement them. The engine recovers
  the concrete owner with one checked helper per type (`txOwnerOf`,
  `privateSessionOf`), and compares and dereferences owners only through
  them. Never pass a typed nil pointer as an owner.

## What stays in the engine

Every provenance check (foreign, stale, zero and pre-registration
identities), the transaction options `Isolation` and `TxOptions`, the
lowercase `pg*OID` relation constants, and `authorization.Domain`, which has
its own Restore staging domain.

The engine still reaches these names through temporary aliases in
`internal/engine/ids_aliases.go`. Step 1d of
[ENGINE-SPLIT-ids.md](../../../docs/ENGINE-SPLIT-ids.md) removes them.

## Testing

Unit tests pin identity semantics only: domain distinctness, zero and
nil-domain values, private versus public tokens, and `TxRef` equality and
map keys.

```sh
make check RUN='^Test(Domain|SessionToken|TxRef)'
```
