# internal/engine/typedef

User-defined type support that does not need the engine: domains, enums,
composites and typed tables, collations and conversions. It owns the records,
validation, Dump/Restore and remap helpers, the DDL stage handlers (which reach
the engine through `StageHost`), the type dump statements (rendered through
`dump.Presenter`), the ALTER ENUM/DOMAIN resolvers, the native domain replay
walk and the scalar-type import of catalog replacement.

The package sits at layer L9 of
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture). It
imports only `access`, `authorization`, `catalog`, `ddl` (the shared host
contract), `dump` (dump records and the `Presenter`), `expr`, `ids`,
`pgcatalog` (collation versions), `session` (the journal slot interfaces and
notice levels), `storage`, `parser`, `sqltype`, `sqlstate` and the standard
library, and never the engine, `analyze`, `exec` or `routine`. See
[ENGINE-SPLIT-typedef.md](../../../docs/ENGINE-SPLIT-typedef.md).

## What it owns

- **Stage handlers** (`collation_ddl.go`, `conversion_ddl.go`, `domain.go`,
  `domain_identity.go`, `enum.go`, `composite.go`, `typed_type_drop.go`):
  CREATE/ALTER/DROP COLLATION and CONVERSION, CREATE/DROP DOMAIN, domain
  RENAME/SET SCHEMA, CREATE TYPE AS ENUM and CREATE/DROP TYPE (composite), as
  `Stage...Locked(h, auth, statement)`.
  - The ALTER staging cores `StageAlterEnumLabelLocked`
    (`enum_label_stage.go`, returning an `EnumLabelEdit`) and
    `StageAlterDomainLocked` (`domain_edit_stage.go`, returning a
    `DomainEdit` with explicit none/CHECK/NULLness validation) stage one ALTER
    TYPE ... VALUE or ALTER DOMAIN action; the executors stay in the engine.
  - `ResolveAlterEnumLocked` and `ResolveAlterDomainLocked`
    (`alter_resolve.go`) resolve the target type in PostgreSQL's order
    (existence, kind, ownership; ALTER DOMAIN reads the reference path before
    the kind check) over `ddl.Host`, using only `SearchNamespaces` and
    `AccessView`.
  - Pure domain identity staging uses `StageHost.ReplaceDomainIdentity` to keep
    the executable analysis stamp while advancing descriptor revisions.
    Ordinary replacement invalidates by default; OWNER stays with the role
    owner.
- **Collations** (`collation_name.go`, `collation_provider.go`,
  `collation_remap.go`, `collation_seed_dump.go`): `CollationName`,
  `CollationSQLName`, `ValidateCollationProvider`, the import remapper
  `CollationRemapper`/`RemappedCollationCapabilities` and the bootstrap
  `CollationSeedDumpStatements`.
- **Conversions** (`conversion_routines.go`, `conversion_probe.go`,
  `conversion_seed_dump.go`): the builtin conversion routine table
  (`BuiltinConversionCount`, `ConversionRoutineName`), the seed constructor
  `BuiltinConversionProcedures`, `ConversionProbeResult`,
  `ConversionDefinition`, `ConversionSQLName` and
  `ConversionSeedDumpStatements`.
- **Domains**:
  - builtin `information_schema` CHECK and DEFAULT seeds
    (`domain_builtin_seeds.go`);
  - ALTER DOMAIN discovery (`domain_alter_discovery.go`: `DomainUsesAt`,
    `DomainValidationFence`, `DomainGraphEqual`, `DomainHistoricalViolation`);
  - the Dump/Restore replay codec (`domain_replay_codec.go`: `DomainReplay*`,
    `EncodeDomainReplay`, `DecodeDomainReplay`, `PairDomainReplay`), its
    reconstruction (`domain_replay_restore.go`: `RestoreDomainReplayNode`
    walks the domain, NULL, array, composite and scalar `SELECT` shapes over a
    `DomainReplayEnv` whose `Scalar` callback binds and evaluates the leaf,
    returning `DomainReplayProvenance`; `DomainReplayCells` checks each
    captured cell against its insert slot) and replay names
    (`domain_replay_names.go`);
  - generation reownership (`domain_reown.go`: `ReownDomainTypes`,
    `ReownDomainDatum`), `TypeDefault`, `DefaultDomainACL`, captured input
    providers (`domain_input.go`) and the restored-owner record
    (`domain_owner_restore.go`);
  - own NOT NULL records shared by CREATE and ALTER (`domain_not_null.go`);
  - default snapshot records (`domain_default_snapshot.go`), paired with
    ordinary CREATE SQL. They carry the qualified result usage, typmod,
    collation and own/absence masks, never source OIDs. The engine's
    authenticated import host performs the ordinary binding without recooking
    copied defaults into extra ancestor conversions. This is distinct from
    the legal-NULL data codec paired to INSERT.
- **Enums**: the PostgreSQL sort-order insertion rule (`enum_order.go`) and
  `EnumSafety`.
- **Composites and typed tables**: `CompositeOpenError`, `ContainsRowType`,
  `RowRewriteError`, `CompositeMembers`, `ChooseAssociatedArrayName`,
  `SynchronizeRowTypeMetadata` (`row_type.go`), the typed-table shape checks
  (`typed_shape.go`), the typed inheritance plan helpers
  (`TypedShapeActions`, `FinishTypedInheritance`, `TypedSortedAddresses`) and
  `TypedColumnAction`.
- **Catalog replacement** (`scalar_import.go`, `typed_control.go`):
  `ImportScalarTypesLocked` gives an imported candidate's enum, `query_int`,
  `cube` and `citext` types their destination codecs, with enum members from
  the engine's allocator callback in member order (so Dump stays
  byte-identical); `RefreshScalarImportDestinations` repoints the imports
  after reconstruction; `CheckTemporaryTypedReplacementLocked` rejects a
  replacement while a TEMP typed table of the published state has a
  persistent OF type.
- **Type helpers** (`types.go`, `usage_dependency.go`):
  `CanonicalDeclarationTypeName`, `RelationTypeNameOccupied`,
  `StoredArrayElement`, `TypeDiagnostic`, `UnsupportedColumnType`,
  `UsageDependsOn`.
- **Dump statements** (`collation_dump.go`, `conversion_dump.go`,
  `composite_dump.go`, `domain_dump.go`): `CollationDumpStatements`,
  `ConversionDumpStatements` (with the helper schemas that replay extra
  default conversions), `MixedDumpStatements` (domains, enums and composite
  types in captured graph order, reserving implicit array names as it goes),
  `DomainInvalidDumpStatement` (the post-data NOT VALID domain CHECKs) and
  `DomainCheckDumpStatement` (one retained CHECK in a historical cast replay
  window, keeping its valid/NOT VALID state; valid additions use the ordinary
  ALTER DOMAIN binder and row-validation path during private Restore). They
  produce `dump.Statement` records and are pure functions of the captured
  view: type and column declarations and the domain DEFAULT and CHECK text
  come from the caller's `dump.Presenter`, so the package never names the
  stored-SQL renderer or `regobj`.
- **Slot structs**: `DomainAlterStage` and `TypedStage` (the type state of a
  `CatalogStage`), `DomainJournal` and `EnumJournal` (the type state of the
  catalog journal, implementing `session.DomainSlot` and `session.EnumSlot`;
  `journal_slots.go`), with their helpers: `DomainWritable` and
  `EnumWritable` give a `session.Journal` a private slot, and
  `DomainJournalOf`, `EnumJournalOf` and `EnumSlotOf` recover and store one.

## The stage host

The stage handlers run under `Engine.mu` without a statement scope and reach
the engine only through `StageHost` (`stage_host.go`), which embeds the shared
[`ddl.Host`](../ddl/README.md) and `ddl.TypeStaging` and adds the type
methods. The engine implements it on its one staging adapter, `ddlHost`
(engine, operation, catalog stage), which also backs `routine.DDLHost`; the
type methods are in `typedef_host.go`.

- **`ddl.Authority`** is the statement authority as an opaque value
  (`access.Environment` plus the `DDLAuthority` marker). Handlers keep it by
  value. `CreationNamespace` may stage an implicit TEMP namespace and returns
  a new authority that reads the stage; the handler assigns it to its own
  `auth`, so later checks read the recaptured view, and the argument is never
  changed. Checks go through `ddl.Check`, object references through
  `ddl.ObjectRef` (the view's owner domain and epoch), NOTICEs through
  `ddl.Notice`.
- **`ddl.Stage`** is the private catalog change (`Create`, `Replace`, `Get`,
  `LookupName`); the engine stage keeps its lifecycle checks.
  `ReplaceUnsynchronized`, the dependency fields (`AddDomainBase`,
  `DropTarget`, `RecordDependencyPath`) and `RemoveDomainCheck` are host
  methods because they write engine-private stage state.
- Name and type resolution (`SearchNamespaces`, `LookupFunction`,
  `NamedType`, `FindCollation`, `Resolve*`) and domain default/constraint
  binding stay in the engine behind the host, because CHECKs need `analyze`.
  `StageHost.BindDomainConstraints` installs CHECK/NOT NULL definitions in
  declaration order; generated names see only earlier installed names, never
  later explicit reservations. CHECK raw-list ordinals and the final
  alphabetical evaluation order are separate from installation order.
- OIDs, revisions and enum members come from `AllocateOID`, `Revision` and
  `AllocateEnumMember` in the order the handlers ask.
- **Lock contract.** Every host method runs with `Engine.mu` held; none takes
  or releases it, and none waits on the lock manager.

## Invariants

- **No locks, no engine state.** The package has no mutex and reads the
  catalog only through `catalog.Read`/`catalog.State`. The engine
  synchronizes the slots it holds.
- **Lock contract of the decisions.** `ResolveAlter*Locked`,
  `ImportScalarTypesLocked` and `CheckTemporaryTypedReplacementLocked` run
  with `Engine.mu` held and take no lock; the allocator callback runs under
  the caller's lock. `RestoreDomainReplayNode` and `DomainReplayCells` run
  without `Engine.mu`, because the engine's `Scalar` callback takes it to bind
  the leaf.
- **Slots are owned by the engine.** `CatalogStage` and the journal
  (`session.Journal`) hold the slots; the engine allocates them
  (`domainAlterState`, `typedState`, and the journal slots through
  `DomainWritable`/`EnumWritable`), recovers the journal slots with
  `DomainJournalOf`/`EnumJournalOf` and writes them. A nil slot is the empty
  state, and the getters here are nil-safe. Journal slots are copy-on-write:
  `Clone` makes a savepoint or statement copy, and a writer replaces a shared
  slot before changing it.
- **Pure seeds.** `BuiltinConversionProcedures`, `BuiltinDomainCheckSeeds` and
  `BuiltinDomainDefaultSeeds` return fresh values. The engine installs them in
  `newEngine`'s order, which fixes their OIDs.
- **Domain identities.** ALTER keeps logical domain and array IDs, while own
  NOT NULL definitions reserve real additional identities. Dump preserves
  names and exact effective default snapshots through versioned host records;
  physical OIDs and the historical spelling of defaults are not preserved.
- **ALTER DOMAIN validation.** A validating NOT NULL uses the discovery and
  physical Share lock fence and checks only stored NULLness, not CHECK or
  default truth. Defaults, DROP and a repeated own SET skip historical
  scanning. The relevant graph, storage, provider, owner and epoch
  observations gate one immutable publication; cancellation before install
  leaves the roots unchanged.
- **Captured-cell replay.** The native captured-cell record requires
  authentic domain-NULL provenance; its wire shape is exact dimensions plus
  recursive typed elements. Negative constructor CIRCLE leaves may occur in an
  authenticated mixed carrier but do not authorize a captured-cell record
  themselves. Circle-only payloads replay through portable constructor SQL;
  scalar negative sidecars and unrelated captured cells are rejected.

## What stays in the engine

- The `ddlHost` type methods (`typedef_host.go`) and the schema statement
  dispatch (`schema.go`).
- The self-orchestrating executors (`executeEnumDDL`, `executeAlterDomain`,
  `executeAlterComposite`, `alterTableOfLocked`), which hold the lock
  windows, lock-manager waits, row validation and publication; they pass a
  stage-less `ddlHost` to the resolvers before the stage exists.
- The enum journal updates and journal capture (`domain_alter_message.go`).
- The native replay INSERT (`domain_default_restore.go`: replay authority,
  target and owner checks, the insert slots from `analyze.BindInsertTargets`,
  the scalar leaf built by `Engine.domainReplayEnv`, which binds under
  `Engine.mu` and evaluates in a local context, and the operation transport
  the destination binder reads).
- The TEMP preservation orchestrator, which calls the scalar import with
  `catalogStore.allocateEnumMemberLocked`.
- Publication-time reownership (`type_generation.go`, `enum_generation.go`)
  and domain index generations (`domain_alter_indexes.go`, calling
  `index.ReownDomainIndexOutputs`).
- TEMP collation cleanup and `collationCreationNamespaceLocked`
  (`collation_temporary.go`); `conversionCheckCreate`
  (`conversion_create_check.go`, shared with `routine.DDLHost`);
  `conversionDefault`.
- Domain check programs and evaluation; the typed inheritance and mutation
  steps that use ALTER TABLE records; the column default records
  (`domain_default.go`).

Elsewhere: the dump graph edges of type declarations are
`control.ColumnPredecessors` and `regobj.DomainInvalidDumpEvents` (it needs
`regobj.ClassReplayReferences`, and `regobj` imports `typedef`); the domain
owner transfer in `emitDumpStatement` and the `dump.Presenter` adapter belong
to `control`; the domain dependency is `table.DomainDependency`. The type
class validator is `stored.TypeProvider`, with the domain default and CHECK
validators and `stored.ReownScalarTypes`; it sits in `stored` because it
renders stored domain recipes through `stored.ScalarRenderer`.

## Testing

Package units pin the seeds, replay pairing and scalar leaf callback, enum
sort insertion, journal slot copy-on-write, default snapshots and storage
admission. `TestRestoreDomainReplayScalarLeaf` pins the scalar callback
contract (parsed target and leaf usage, shape rejection before the callback,
NULL or mistyped results, cancellation).

```sh
make check RUN='^Test(BuiltinConversionProcedures|BuiltinDomainSeeds|DomainDefaultReplayPairingInternal|DomainAlterDefaultReplayInternal|PGEnumSortInsertion|CatalogTypeJournalSlotsCopyOnWrite|RestoreDomainReplayScalarLeaf|MACADDR8?StorageAdmission)$$'
```

Engine witnesses (in `internal/engine`) cover the `ddlHost` side
(`TestDDLHost*`), the ALTER DOMAIN stage and publication invariants, the
replay walk with the real leaf (`Engine.domainReplayEnv`) and the dump
statements (`TestCollationDumpLosslessBootstrapSelection` calls
`CollationDumpStatements`; `TestDumpPresenterDomainExpressions` pins the
presenter methods the domain statements use):

```sh
make check RUN='^TestDDLHost'
make check RACE=1 RUN='^TestDomainAlter(DefaultSnapshotsInternal|NotNullMetadataInternal|DefaultNullabilityPublication|ConstraintPublication)$$'
make check RUN='^Test(DomainDefaultReplay(Admission|DeferredChecks|RoutineManifest)Internal|GeometryConversion(ReplayShapes|MixedReplayProvenance|MixedReplayBoundedRow))$$'
make check RUN='^Test(CollationDumpLosslessBootstrapSelection|DumpPresenterDomainExpressions)$$'
make check RUN='^Test(ConversionDescriptor|ConversionHandlerEmptyProbePrecedence|ConversionDefaultLookup|DomainAlterConstraint|EnumCatalogStagingAndAuthority|PGEnumAllocatorBounds|TypedTemporaryReplacementAdmission|ArrayCatalogAssociationPublicationAndOrigin)'
```

Public behavior is covered by the domain, enum, composite, range, collation,
conversion, typed-table and Dump/Restore families in `reference/` (see
[reference/types](../../../reference/types/README.md#testing)).
