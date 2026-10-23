# internal/engine/sequence

Sequence objects: option validation and limits, a physical session's sequence
memory, prepared sequence literals, the decisions of sequence, identity and
serial DDL and of the sequence functions, sequence dump formatting and the
sequence and identity diagnostics. Everything here is a pure function over
catalog records, parser syntax and the session memory; the engine keeps the
orchestration.

The package sits at layer L9 of
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture). It
imports only `authorization`, `catalog`, `expr`, `functions`, `ids`,
`session`, `storage`, `parser`, `sqltype`, `sqlstate` and the standard
library, and never `analyze`, `exec` or the engine.

## What it owns

- **Options and limits** (`options.go`): `BindOptions` validates CREATE and
  ALTER SEQUENCE options against the old options and the generator state;
  `TypeLimits` and `OptionError`. The private `reserve` takes one cache
  reservation from a `storage.SequenceGenerator`. Messages and SQLSTATEs match
  PostgreSQL 18.
- **Relations and generators** (`relation.go`): `Columns`, `NewRelation` (the
  one descriptor shape of CREATE SEQUENCE, identity and serial columns, with
  the plan stamp equal to the creation revision; the caller sets the ACL),
  `InitialGenerator` and `ReplacementGenerator`.
- **DDL decisions** (`owned.go`, `alter.go`): `ParseOwnedBy` and
  `CheckOwnedBy` (after the table's lock grant), `IdentityOwnershipError`,
  `TemporaryLoggedError`, `CheckSchemaMove`, `NotSequenceError` and
  `AlterEffects`, which decides the plan-stamp bump, the generator
  replacement and the session tail discard of ALTER SEQUENCE.
- **Identity and serial options** (`identity.go`): `IdentityTypeOption`,
  `SplitIdentityOptions` with `IdentityOptions.CheckTemporary` (called after
  the options bind, which take precedence), `CheckIdentitySetOptions`,
  `SerialOptions`, `SerialDefault` and `CheckRestoreIdentityConfiguration`.
- **Function calls** (`call.go`): `CallPrivileges`, `ReadOnlyCallError` and
  `CallLocked`, the currval/lastval/nextval/setval semantics over the opened
  relation, its generator and the session memory.
- **Session memory** (`memory.go`): `SessionMemory` holds one physical
  session's `currval` values, unused cache tails, the `lastval` target and the
  effective-path cache of prepared literals; `Memory` is one sequence's part.
  Its fields are private. Mutating methods carry the `Locked` suffix; `Len`,
  `All`, `CachedPathKey`, `CachedTail` and `Generation` are read-only
  inspection.
- **Prepared literals** (`prepared.go`): the private analysis cell
  `Analysis`, its captured literals `Constant`, the effective path
  `Environment`, the path-cache key `PathKey`, `SameEnvironment` and the
  opaque `JournalIdentity`; the `Observation` a literal group is analyzed and
  validated against, `NewAnalysis`, `Analysis.Valid`, `PinReplacement`,
  `TemporaryEpochValid` and the plan-stamp rule `DefinitionStamp`.
- **Stage slot** (`stage.go`): `Stage`, the generator replacements and
  identity captures of one engine `CatalogStage`; nil reads answer empty.
- **Control** (`control.go`): `CopyGenerators` and the retain predicate
  `OwnTemporary`.
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
  ACLs and the shared transaction, lock and query paths.
- Session memories are distinct from transactional sequence metadata (the
  catalog's `SequenceOptions`) and from the shared generator, which stays in
  [`storage`](../storage/README.md) with its rules. Never use per-CACHE
  arrays: a memory holds one cache tail (`next`, `remaining`) and the
  generator generation it came from.
- `Engine.mu` guards every `SessionMemory`, generator and `Stage`; functions
  that change them carry the `Locked` suffix. Physical-session admission also
  serializes the path cache. This package adds no mutex.
- TEMP sequence lifetime follows the physical session, not pooled borrowing:
  `ForgetLocked` runs when the session's TEMP objects end, and `RetainLocked`
  with `OwnTemporary` keeps only the session's own TEMP sequences across
  persistent controls.
- DISCARD SEQUENCES (`DiscardLocked(false)`) forgets values and tails but
  keeps an unconsumed relation-source stamp; DISCARD ALL clears everything.
- The engine calls the decisions in PostgreSQL's order, so error precedence
  is the engine's: for example identity options bind before the TEMP 42P16,
  and CREATE SEQUENCE without IF NOT EXISTS binds options before the
  namespace CREATE check.

## Prepared sequence literals

A prepared statement's sequence literals use a private description analysis
cell, a distinct relation plan stamp and a physical-session effective
namespace cache. Execute start reanalyzes the entire original literal group
after dependency or path changes; the temporal, signature and result
contracts stay immutable. RowExclusive is taken on demand after once-only
argument evaluation; grant-time opening keeps the in-flight OID, with no
expression retarget or replay. Filtered, zero-row and strict NULL calls do
not open their sequence. Generator state and descriptor revisions are not
plan invalidation tokens. Native replacement stays fenced except for the
caller's exact live TEMP provenance.

| Rule | Enforced by |
|---|---|
| Private description analysis cell | `StatementDescription.sequences` (engine `statement_description.go`), non-nil only when the statement has literals; built by `NewAnalysis` in the describe path (`engine.go`) and republished at Execute (`execute.go`); held by `analyze.Expected.Sequences` |
| Distinct relation plan stamp | `catalog.Relation.SequencePlanStamp`: set by `NewRelation`, bumped per `AlterEffects`, identity options, rename, ownership, ALTER SCHEMA, role grants and TRUNCATE RESTART IDENTITY; read by `DefinitionStamp` through the engine's `preparedDefinitionStamp`, adding the session memory's `SourceStamp` |
| Physical-session effective namespace cache | `SessionMemory.CachedPath`/`StorePathLocked`, keyed by `PathKey` and filled by the engine's shared `sequenceEnvironmentLocked` |
| Whole-group reanalysis at Execute start | `execute.go`: `Analysis.Valid` (through `sequenceAnalysisValidLocked`) clears `candidate.sequences`, and the republish rebuilds the analysis from every `callerSequences` literal |
| Immutable temporal, signature and result contracts | `checkStatementSignature` before the republish in `execute.go` (engine, `analyze.CheckSignature`) |
| RX on demand after once-only argument evaluation | `functions.SequenceCall.Eval` evaluates each argument once; the engine's `statementHost.Sequence` takes `RowExclusive` at call time |
| Grant-time opening keeps the in-flight OID; no retarget or replay | `SequenceCall.Eval` reads the OID from the datum and never resolves a name; `statementHost.Sequence` reopens the same address after the grant (XX000 `could not open relation`) before `CallLocked` |
| Filtered, zero-row and strict NULL calls do not open | `SequenceCall.Eval` returns NULL before calling the runtime; nodes evaluate on demand |
| Generator state and descriptor revision are not invalidation tokens | `DefinitionStamp` reads `SequencePlanStamp`, not `ObjectRevision`; `Analysis` holds no generator; `CallLocked` changes no stamp |
| Native replacement fenced except exact caller-owned live TEMP provenance | the epoch branch of `Analysis.Valid`, `PinReplacement` at Execute, `TemporaryEpochValid` in `statementHost.Sequence` and the engine's `storedSequenceEpochValid` for stored defaults |

`Observation` holds no closure: validating an unchanged cell does not
allocate (`TestSequencePreparedAuthority` in the engine pins it). The engine
builds it in `sequenceObservationLocked` only for statements with literals.

## What stays in the engine

The orchestration of sequence DDL and COMMENT (`sequence.go`,
`sequence_comment.go`: `Engine.mu`, lock waits, authorization refresh, OID and
revision allocation, the journal and stage install, scope fields and
notices), `ownedSequenceLocked`, `statementHost.Sequence`
(`sequence_runtime.go`) and `clearPersistentSequenceMemoryLocked`;
`StatementScope.acquireSequence` (the scope's object lock),
`checkSequenceAny` and `index.RelationDropOwner`; `sequenceEnvironmentLocked`,
`journalOf` and `preparedDefinitionStamp`, shared with ordinary prepared SQL;
the ALTER identity staging and the identity DROP flow (table DDL; CREATE
identity and serial staging is `table.StageCreateIdentities` and
`table.StageCreateSerials`); `CatalogStage.sequenceState`; and regclass
input and output. The generic relation diagnostic is
`catalog.RelationDiagnosticKind`.

## Testing

Package units pin the session memory, relation shape, generators,
`CallLocked`, privileges, prepared analysis validity and fences,
`PinReplacement`, `TemporaryEpochValid`, `DefinitionStamp`, `AlterEffects`,
identity option splitting, control copy/retain and the stage slot. Engine
witnesses exercise the package through sessions, DDL, DISCARD, TRUNCATE
RESTART IDENTITY and native Dump/Restore; add `RACE=1` for concurrent
`nextval` and TEMP teardown.

```sh
make check RUN='^TestSequence'
make check RUN='^Test(Discard|Serial|Identity|TruncateRestart)'
```

Public behavior lives in
[reference/sequences](../../../reference/sequences/README.md#testing), plus the
identity, serial, DISCARD, TRUNCATE and Control families.
