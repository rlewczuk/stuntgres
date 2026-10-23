# internal/engine/typedef

User-defined type support that does not need the engine: domains, enums,
composites and typed tables, collations and conversions. It owns the records,
validation, Dump/Restore and remap helpers, the DDL stage handlers (which reach
the engine through `StageHost`), the type dump statements (rendered through
`dump.Presenter`), the ALTER ENUM/DOMAIN resolvers, the native domain replay
walk and the scalar-type import of catalog replacement.

## Imports

Layer L9 of [ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture). It
imports only `access`, `authorization`, `catalog`, `ddl` (the shared host
contract), `dump`, `expr`, `ids`, `pgcatalog` (collation versions), `session`
(journal slot interfaces, notice levels), `storage`, `parser`, `sqltype`,
`sqlstate` and the standard library, never the engine, `analyze`, `exec` or
`routine`.

## What it owns

- **Stage handlers** (`collation_ddl.go`, `conversion_ddl.go`, `domain*.go`,
  `enum.go`, `composite.go`, `typed_type_drop.go`): CREATE/ALTER/DROP COLLATION
  and CONVERSION, CREATE/DROP DOMAIN, domain RENAME/SET SCHEMA, CREATE TYPE AS
  ENUM and composite CREATE/DROP TYPE, as `Stage...Locked(h, auth, statement)`.
  `StageAlterEnumLabelLocked` and `StageAlterDomainLocked` stage one ALTER TYPE
  ... VALUE or ALTER DOMAIN action and return an edit record; the executors stay
  in the engine.
- **ALTER resolvers** (`alter_resolve.go`): `ResolveAlterEnumLocked` and
  `ResolveAlterDomainLocked` resolve the target in diagnostic order (existence,
  kind, ownership; ALTER DOMAIN reads the reference path before the kind check)
  over `ddl.Host`, using only `SearchNamespaces` and `AccessView`.
- **Collations and conversions** (`collation_*.go`, `conversion_*.go`): SQL names,
  provider validation, the import remapper, the builtin conversion routine table
  and seed constructor `BuiltinConversionProcedures`, and the bootstrap seed dump
  statements.
- **Domains** (`domain_*.go`): builtin `information_schema` seeds, ALTER DOMAIN
  discovery, own NOT NULL records, generation reownership, captured input
  providers and default snapshot records (qualified usage, never source OIDs).
  The Dump/Restore replay codec pairs each record with its INSERT;
  `RestoreDomainReplayNode` walks the replayed `SELECT` shapes over a
  `DomainReplayEnv` whose `Scalar` callback binds and evaluates the leaf, and
  `DomainReplayCells` checks each captured cell against its insert slot.
- **Enums** (`enum_order.go`: sort-order insertion, `EnumSafety`) and
  **composites and typed tables** (`row_type.go`, `typed_shape.go`: row-type
  membership and metadata, array names, typed-table shape and inheritance plans).
- **Catalog replacement** (`scalar_import.go`, `typed_control.go`):
  `ImportScalarTypesLocked` gives an imported candidate's enum, `query_int`,
  `cube` and `citext` types their destination codecs, taking enum members from
  the engine's allocator callback in member order so Dump stays byte-identical;
  `CheckTemporaryTypedReplacementLocked` rejects a replacement while a TEMP typed
  table of the published state has a persistent OF type.
- **Dump statements** (`*_dump.go`): `CollationDumpStatements`,
  `ConversionDumpStatements`, `MixedDumpStatements` (domains, enums and
  composites in captured graph order) and the domain CHECK statements. They are
  pure functions of the captured view; declaration, DEFAULT and CHECK text comes
  from the caller's `dump.Presenter`, so the package never names the stored-SQL
  renderer or `regobj`.
- **Slot structs**: `DomainAlterStage` and `TypedStage` (the type state of a
  `CatalogStage`), `DomainJournal` and `EnumJournal` (`journal_slots.go`, the type
  state of the catalog journal, implementing `session.DomainSlot` and
  `session.EnumSlot`), with `DomainWritable`/`EnumWritable` and
  `DomainJournalOf`/`EnumJournalOf`/`EnumSlotOf`.

## The stage host

The stage handlers run under `Engine.mu` without a statement scope and reach the
engine only through `StageHost` (`stage_host.go`), which embeds
[`ddl.Host`](../ddl/README.md) and `ddl.TypeStaging` and adds the type methods.
The engine implements it on its one staging adapter, `ddlHost`
(`typedef_host.go`), which also backs `routine.DDLHost`.

- **`ddl.Authority`** is the statement authority as an opaque value; handlers
  keep it by value. `CreationNamespace` may stage an implicit TEMP namespace and
  returns a new authority that reads the stage; the handler assigns it to its own
  `auth`, so later checks read the recaptured view.
- **`ddl.Stage`** is the private catalog change; the engine stage keeps its
  lifecycle checks. Methods that write engine-private stage state
  (`ReplaceUnsynchronized`, dependency recording, `RemoveDomainCheck`,
  `ReplaceDomainIdentity`) are host methods.
- Name and type resolution and domain default/constraint binding stay in the
  engine behind the host, because CHECKs need `analyze`.
  `BindDomainConstraints` installs definitions in declaration order; generated
  names see only earlier installed names.
- OIDs, revisions and enum members come from `AllocateOID`, `Revision` and
  `AllocateEnumMember` in the order the handlers ask.
- **Lock contract.** Every host method runs with `Engine.mu` held; none takes or
  releases it, and none waits on the lock manager.

## Invariants

- **No locks, no engine state.** The package has no mutex and reads the catalog
  only through `catalog.Read`/`catalog.State`.
- **Lock contract of the decisions.** `ResolveAlter*Locked`,
  `ImportScalarTypesLocked` and `CheckTemporaryTypedReplacementLocked` run with
  `Engine.mu` held and take no lock (the allocator callback runs under it).
  `RestoreDomainReplayNode` and `DomainReplayCells` run without `Engine.mu`,
  because the engine's `Scalar` callback takes it to bind the leaf.
- **Slots are owned by the engine.** `CatalogStage` and the journal hold the
  slots; the engine allocates, recovers and writes them. A nil slot is the empty
  state, and the getters are nil-safe. Journal slots are copy-on-write: `Clone`
  makes a savepoint or statement copy, and a writer replaces a shared slot before
  changing it.
- **Pure seeds.** `BuiltinConversionProcedures`, `BuiltinDomainCheckSeeds` and
  `BuiltinDomainDefaultSeeds` return fresh values; the engine installs them in
  `newEngine`'s order, which fixes their OIDs.
- **Domain identities.** ALTER keeps logical domain and array IDs; pure identity
  edits keep the executable analysis stamp while ordinary replacement
  invalidates it. Own NOT NULL definitions reserve real additional identities.
- **ALTER DOMAIN publication.** Validation observations (graph, storage,
  provider, owner, epoch) gate one immutable publication; cancellation before
  install leaves the roots unchanged.

## What stays in the engine

The `ddlHost` type methods (`typedef_host.go`) and schema dispatch; the
self-orchestrating executors (`executeEnumDDL`, `executeAlterDomain`,
`executeAlterComposite`, `alterTableOfLocked`), which hold the lock windows,
lock-manager waits, row validation and publication; enum journal updates; the
native replay INSERT (`domain_default_restore.go`, with the scalar leaf built by
`Engine.domainReplayEnv`); the TEMP preservation orchestrator;
publication-time reownership and domain index generations; TEMP collation
cleanup; and domain check programs and evaluation.

Elsewhere: type dump graph edges (`control.ColumnPredecessors`,
`regobj.DomainInvalidDumpEvents`), the `dump.Presenter` adapter (`control`),
`table.DomainDependency` and the type class validator `stored.TypeProvider`.

## Testing

Package units pin the seeds, replay pairing, the scalar leaf callback contract
(`TestRestoreDomainReplayScalarLeaf`), enum sort insertion, journal slot
copy-on-write, default snapshots and storage admission. Engine witnesses in
`internal/engine` cover the `ddlHost` side, ALTER DOMAIN publication, the replay
walk with the real leaf and the dump statements.

```sh
make check RUN='^Test(BuiltinConversionProcedures|BuiltinDomainSeeds|DomainDefaultReplayPairingInternal|DomainAlterDefaultReplayInternal|PGEnumSortInsertion|CatalogTypeJournalSlotsCopyOnWrite|RestoreDomainReplayScalarLeaf|MACADDR8?StorageAdmission)$$'
make check RUN='^TestDDLHost'
make check RACE=1 RUN='^TestDomainAlter(DefaultSnapshotsInternal|NotNullMetadataInternal|DefaultNullabilityPublication|ConstraintPublication)$$'
make check RUN='^Test(DomainDefaultReplay(Admission|DeferredChecks|RoutineManifest)Internal|CollationDumpLosslessBootstrapSelection|DumpPresenterDomainExpressions)$$'
```

Public behavior is covered by the domain, enum, composite, collation,
conversion, typed-table and Dump/Restore families in `reference/` (see
[reference/types](../../../reference/types/README.md#testing)).
