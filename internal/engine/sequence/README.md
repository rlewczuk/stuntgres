# internal/engine/sequence

Sequence objects: option validation and limits, a physical session's sequence
memory, the records of prepared sequence literals, sequence dump formatting
and the sequence and identity diagnostics. The package sits at layer L10 of
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture). It
imports only `authorization`, `catalog`, `expr`, `ids`, `storage`, `parser`,
`sqltype`, `sqlstate` and the standard library, and never the engine. Step 17
of the split moves it here; see
[ENGINE-SPLIT-sequence.md](../../../docs/ENGINE-SPLIT-sequence.md).

## What it owns

- **Options and limits** (`options.go`): `BindOptions` validates CREATE and
  ALTER SEQUENCE options against the old options and the generator state;
  `TypeLimits` and `OptionError`. The private `reserve` takes one cache
  reservation from a `storage.SequenceGenerator`. Messages and SQLSTATEs match
  PostgreSQL 18.
- **Session memory** (`memory.go`): `SessionMemory` holds one physical
  session's `currval` values, unused cache tails, the `lastval` target and the
  effective-path cache of prepared literals; `Memory` is one sequence's part.
  Its fields are private. Mutating methods carry the `Locked` suffix; `Len`,
  `All`, `CachedPathKey`, `CachedTail` and `Generation` are read-only
  inspection.
- **Prepared-literal records** (`prepared.go`): the private analysis cell
  `Analysis`, its captured literals `Constant`, the effective path
  `Environment`, the path-cache key `PathKey`, `SameEnvironment` and the
  opaque `JournalIdentity`. The engine builds and validates them
  (`sequence_prepared.go`).
- **Dump formatting** (`dump.go`): `Dump`, `DumpStatements` (byte-identical
  native Dump output), `NumericSQL`, `IdentityDumpDivergent` and the Restore
  whitelist `RestoresState`.
- **Diagnostics** (`diagnostics.go`): `CheckKind`, `LinkedError`,
  `IdentityInsertError`, `IdentityDropError`, `IdentityDefinition` and
  `SerialTypeName`; `ValidateDefaultTemporaryProvider`
  (`default_provider.go`) rejects a DEFAULT whose TEMP sequence another
  session provides.

## Rules

- Sequence DDL and the sequence functions use relation descriptors, central
  ACLs and the existing transaction, lock and query paths.
- Session memories are distinct from transactional sequence metadata (the
  catalog's `SequenceOptions`) and from the shared generator, which stays in
  [`storage`](../storage/README.md) with its rules. Never use per-CACHE
  arrays: a memory holds one cache tail (`next`, `remaining`) and the
  generator generation it came from.
- `Engine.mu` guards every `SessionMemory`; physical-session admission also
  serializes the path cache. This package adds no mutex.
- TEMP sequence lifetime follows the physical session, not pooled borrowing:
  `ForgetLocked` runs when the session's TEMP objects end, and `RetainLocked`
  keeps only the session's own TEMP sequences across persistent controls.
- DISCARD SEQUENCES (`DiscardLocked(false)`) forgets values and tails but
  keeps an unconsumed relation-source stamp; DISCARD ALL clears everything.

## What stays in the engine

Sequence, identity and serial DDL and staging (`sequence.go`, `identity*.go`,
`serial*.go`), `StatementScope.acquireSequence`, the prepared-literal cache
logic on `Engine` (`sequence_prepared.go`), the function authority behind
`functions.SequenceRuntime` (`sequence_runtime.go`), the `CatalogStage`
sequence slot (`sequence_stage.go`), `copySequenceState` and
`clearPersistentSequenceMemoryLocked`, regclass input and output, and the
generic relation diagnostics `tableKind` and `relationDiagnosticKind`. Part B
(17c, 17d) moves the handlers.

## Testing

Package seam:

```sh
make check RUN='^TestSequenceSessionMemoryInspection$'
```

Engine witnesses that exercise the package through sessions, DDL, DISCARD,
TRUNCATE RESTART IDENTITY and native Dump/Restore:

```sh
make check RUN='^Test(Discard|Sequence|Serial|Identity|TruncateRestart|Ownership|AlterTablespacePrefix|GroupingSetsRestoreWhitelist|TemporaryTable)'
```

Public behavior is covered by `reference/sequences`, the identity, serial,
DISCARD, TRUNCATE and Control tests (quick mode, then `REFERENCE=1`):

```sh
make check RUN='^TestReference(Sequence|Serial|Identity|SmokeSequence|SmokeIdentity|Discard|AlterIdentity|AlterTypeIdentity|TruncateRestart|Control|CollationTemporary|TemporaryTable|AlterNullability|CheckSequence|CheckAlterOwnedSequence|DeleteUsingSequence|DeleteReturningSequence|ConditionalSequence)'
```
