# internal/engine/typedef

User-defined type support that does not need the engine: domains, enums,
composites and typed tables, collations and conversions. Part A of step 15
moved the records, validation, Dump/Restore and remap helpers here; Part B
(15d) moved the DDL stage handlers, which reach the engine through
`StageHost`; 15e moved the type dump statements, which render through
`dump.Presenter`. The package sits at layer L9 of
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture).
It imports only `access`, `authorization`, `catalog`, `ddl` (the shared
host contract), `dump` (dump records and the `Presenter`), `expr`, `ids`,
`pgcatalog` (collation versions), `session` (the journal slot interfaces and
notice levels), `storage`, `parser`, `sqltype`, `sqlstate` and the standard
library, and never the engine, `analyze`, `exec` or `routine`. See
[ENGINE-SPLIT-typedef.md](../../../docs/ENGINE-SPLIT-typedef.md).

## What it owns

- **Stage handlers** (`collation_ddl.go`, `conversion_ddl.go`, `domain.go`,
  `domain_identity.go`, `enum.go`, `composite.go`, `typed_type_drop.go`):
  CREATE/ALTER/DROP COLLATION and CONVERSION, CREATE/DROP DOMAIN, domain
  RENAME/SET SCHEMA, CREATE TYPE AS ENUM and CREATE/DROP TYPE (composite),
  as `Stage...Locked(h, auth, statement)`.
  The ALTER staging cores `StageAlterEnumLabelLocked`
  (`enum_label_stage.go`, returning an `EnumLabelEdit`) and
  `StageAlterDomainConstraintLocked` (`domain_constraint_stage.go`,
  returning a `DomainConstraintEdit`) stage one ALTER TYPE ... VALUE or
  ALTER DOMAIN constraint action; their executors stay in the engine.
  Pure domain identity staging uses `StageHost.ReplaceDomainIdentity` to preserve
  the existing executable analysis stamp while advancing descriptor revisions.
  Ordinary replacement invalidates by default; OWNER stays with the role owner.
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
- **Domains**: builtin `information_schema` CHECK and DEFAULT seeds
  (`domain_builtin_seeds.go`); ALTER DOMAIN discovery (`DomainUsesAt`,
  `DomainValidationFence`, `DomainGraphEqual`, `DomainHistoricalViolation`;
  `domain_alter_discovery.go`); the Dump/Restore replay codec
  (`DomainReplay*`, `EncodeDomainReplay`, `DecodeDomainReplay`,
  `PairDomainReplay`; `domain_replay_codec.go`) and replay names
  (`domain_replay_names.go`); generation reownership (`ReownDomainTypes`,
  `ReownDomainDatum`; `domain_reown.go`); `TypeDefault`, `DefaultDomainACL`,
  captured input providers (`domain_input.go`) and
  the restored-owner record (`domain_owner_restore.go`).
- **Enums**: the PostgreSQL sort-order insertion rule (`enum_order.go`) and
  `EnumSafety`.
- **Composites and typed tables**: `CompositeOpenError`, `ContainsRowType`,
  `RowRewriteError`, `CompositeMembers`, `ChooseAssociatedArrayName`,
  `SynchronizeRowTypeMetadata` (`row_type.go`; table 24b moved it here), the typed-table shape checks (`typed_shape.go`), the
  typed inheritance plan helpers (`TypedShapeActions`,
  `FinishTypedInheritance`, `TypedSortedAddresses`) and `TypedColumnAction`.
- **Type helpers** (`types.go`, `usage_dependency.go`):
  `CanonicalDeclarationTypeName`, `RelationTypeNameOccupied`,
  `StoredArrayElement`, `TypeDiagnostic`, `UnsupportedColumnType`,
  `UsageDependsOn`.
- **Dump statements** (`collation_dump.go`, `conversion_dump.go`,
  `composite_dump.go`, `domain_dump.go`): `CollationDumpStatements`,
  `ConversionDumpStatements` (with the helper schemas that replay extra
  default conversions), `MixedDumpStatements` (domains, enums and composite
  types in captured graph order, reserving implicit array names as it goes)
  and `DomainInvalidDumpStatement` (the post-data NOT VALID domain CHECKs).
  They produce `dump.Statement` records and are pure functions of the
  captured view: type and column declarations and the domain DEFAULT and
  CHECK text come from the caller's `dump.Presenter`, so the package never
  names the stored-SQL renderer or `regobj`.
- **Slot structs**: `DomainAlterStage` and `TypedStage` (the type state of a
  `CatalogStage`), `DomainJournal` and `EnumJournal` (the type state of the
  catalog journal, implementing `session.DomainSlot` and `session.EnumSlot`;
  `journal_slots.go`).

## The stage host

The stage handlers run under `Engine.mu` without a statement scope, and
reach the engine only through `StageHost` (`stage_host.go`), which embeds
the shared [`ddl.Host`](../ddl/README.md) and `ddl.TypeStaging` and adds the
type methods. The engine implements it on its one staging adapter,
`ddlHost` (engine, operation, catalog stage), which also backs
`routine.DDLHost`; the type methods are in `typedef_host.go`.

- **`ddl.Authority`** is the statement authority as an opaque value
  (`access.Environment` plus the `DDLAuthority` marker). Handlers keep it by
  value. `CreationNamespace` may stage an implicit TEMP namespace and
  returns a new authority that reads the stage; the handler assigns it to
  its own `auth`, so later checks read the recaptured view, and the
  argument is never changed. Checks go through `ddl.Check`, object
  references through `ddl.ObjectRef` (the view's owner domain and epoch),
  and NOTICEs through `ddl.Notice`.
- **`ddl.Stage`** is the private catalog change (`Create`, `Replace`, `Get`,
  `LookupName`); the engine stage keeps its lifecycle checks.
  `ReplaceUnsynchronized`, the dependency fields (`AddDomainBase`,
  `DropTarget`, `RecordDependencyPath`) and `RemoveDomainCheck` are host
  methods because they write engine-private stage state.
- Name and type resolution (`SearchNamespaces`, `LookupFunction`,
  `NamedType`, `FindCollation`, `Resolve*`) and domain default and check
  binding stay in the engine behind the host, because they need `analyze`.
  OIDs, revisions and enum members come from `AllocateOID`, `Revision` and
  `AllocateEnumMember` in the order the handlers ask.
- **Lock contract.** Every host method runs with `Engine.mu` held; none
  takes or releases it, and none waits on the lock manager.

## Invariants

- **No locks, no engine state.** The package has no mutex and reads the
  catalog only through `catalog.Read`/`catalog.State`. The engine
  synchronizes the slots it holds, as it did before the move.
- **Slots are owned by the engine.** `CatalogStage` and the journal
  (`session.Journal`) hold the slots; the engine allocates them
  (`domainAlterState`, `typedState`, `domainWritable`, `enumWritable`),
  recovers the journal slots with `domainJournalOf`/`enumJournalOf` and
  writes them. A nil slot is the empty
  state, and the getters here are nil-safe. Journal slots are copy-on-write:
  `Clone` makes a savepoint or statement copy, and a writer replaces a shared
  slot before it changes it.
- **Pure seeds.** `BuiltinConversionProcedures`, `BuiltinDomainCheckSeeds`
  and `BuiltinDomainDefaultSeeds` return fresh values. The engine installs
  them in `newEngine`'s order, so OIDs are unchanged.
- **Byte-identical output.** Type OIDs, array names, enum sort orders,
  domain check order and messages, replay wire format and seed dump text
  match PostgreSQL 18 and the previous engine output exactly.

## What stays in the engine

The `ddlHost` type methods (`typedef_host.go`) and the schema statement
dispatch (`schema.go`); the self-orchestrating executors (`executeEnumDDL`,
`executeAlterDomain`, `executeAlterComposite`, `alterTableOfLocked`) and the
resolvers `resolveAlterEnumLocked`/`resolveAlterDomainLocked`, which hold
the lock windows, lock-manager waits, row validation and publication; the
enum journal updates; `type_generation.go` and `enum_generation.go`
(publication-time reownership); domain index generations
(`domain_alter_indexes.go`; the output reownership they call is
`index.ReownDomainIndexOutputs`); journal capture (`domain_alter_message.go`);
TEMP collation cleanup and `collationCreationNamespaceLocked`
(`collation_temporary.go`); `conversionCheckCreate`
(`conversion_create_check.go`, shared with `routine.DDLHost`); the dump
graph edges of type declarations are `control.ColumnPredecessors` and
`regobj.DomainInvalidDumpEvents` (it needs `regobj.ClassReplayReferences`,
and `regobj` imports `typedef`), and the domain owner transfer in
`emitDumpStatement` and the `dump.Presenter` adapter are `control`'s;
`typeProvider`
(clean since the binder moved; stored 22e, item 119, moves it with
`reownCatalogScalarTypes` to `stored`); domain check programs and
evaluation; the typed
inheritance and mutation steps that use ALTER TABLE records; the column
default records (`domain_default.go`); `conversionDefault`
(`domainDependency` is `table.DomainDependency` since table 24b).

## Testing

Package seams:

```sh
make check RUN='^(TestBuiltinConversionProcedures|TestBuiltinDomainSeeds|TestDomainDefaultReplayPairingInternal|TestPGEnumSortInsertion)$'
```

The engine side of the stage host (`ddlHost`):

```sh
make check RUN='^TestDDLHost'
```

Engine witnesses that call the stage handlers or run their executors:

```sh
make check RUN='^Test(CollationOwnerMembershipRequiresDestinationCreate|ConversionDescriptor|ConversionHandlerEmptyProbePrecedence|ConversionDefaultLookup|DefaultPrivilegesArrayAuthority|DomainAlterConstraint|DomainBaseUsageAndOwnerAuthorizationInternal|EnumCatalogStagingAndAuthority|ArrayCatalogAssociationPublicationAndOrigin)'
```

Engine witnesses that use the package through the catalog and DDL:

```sh
make check RUN='^(TestCatalogTypeJournalSlotsCopyOnWrite|TestDomainDefaultReplayAdmissionInternal|TestPGEnumAllocatorBounds|TestTypedTemporaryReplacementAdmission|TestConversionDescriptorCreateAndMoveUniqueness)$'
```

The dump statements (`TestCollationDumpLosslessBootstrapSelection` calls
`CollationDumpStatements`; `TestDumpPresenterDomainExpressions` pins the
presenter methods the domain statements use):

```sh
make check RUN='^(TestCollationDumpLosslessBootstrapSelection|TestDumpPresenterDomainExpressions)$'
```

Public behavior is covered by the domain, enum, composite, range,
collation, conversion, typed-table and Dump/Restore selections in
`reference/` that the step 15 prompt file lists; dump output must stay
byte-identical (the Dump/Restore selectors in the `dump` README).
