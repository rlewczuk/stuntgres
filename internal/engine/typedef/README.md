# internal/engine/typedef

User-defined type support that does not need the engine: domains, enums,
composites and typed tables, collations and conversions. Part A of step 15
moved the records, validation, Dump/Restore and remap helpers here; the DDL
handlers follow in Part B (15c/15d). The package sits at layer L10 of
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture).
It imports only `authorization`, `catalog`, `expr`, `ids`, `storage`,
`parser`, `sqltype`, `sqlstate` and the standard library, and never the
engine or `routine`. See
[ENGINE-SPLIT-typedef.md](../../../docs/ENGINE-SPLIT-typedef.md).

## What it owns

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
- **Enums**: `EnumInsertionOrder` (`enum_order.go`) and `EnumSafety`.
- **Composites and typed tables**: `CompositeOpenError`, `ContainsRowType`,
  `RowRewriteError`, `CompositeMembers`, `ChooseAssociatedArrayName`
  (`row_type.go`), the typed-table shape checks (`typed_shape.go`), the
  typed inheritance plan helpers (`TypedShapeActions`,
  `FinishTypedInheritance`, `TypedSortedAddresses`) and `TypedColumnAction`.
- **Type helpers** (`types.go`, `usage_dependency.go`):
  `CanonicalDeclarationTypeName`, `RelationTypeNameOccupied`,
  `StoredArrayElement`, `TypeDiagnostic`, `UnsupportedColumnType`,
  `UsageDependsOn`.
- **Slot structs**: `DomainAlterStage` and `TypedStage` (the type state of a
  `CatalogStage`), `DomainJournal` and `EnumJournal` (the type state of the
  catalog journal; `journal_slots.go`).

## Invariants

- **No locks, no engine state.** The package has no mutex and reads the
  catalog only through `catalog.Read`/`catalog.State`. The engine
  synchronizes the slots it holds, as it did before the move.
- **Slots are owned by the engine.** `CatalogStage` and `catalogChanges`
  allocate the slots (`domainAlterState`, `typedState`, `domainWritable`,
  `enumWritable`) and write them in the engine. A nil slot is the empty
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

The DDL handlers (Part B); the dump statements that produce the engine's
dump-graph record `dumpDomain` (`collationDumpStatements`,
`conversionDumpStatements`, `compositeDumpStatement`); type-declaration
and literal rendering (`catalogTypeDeclaration*`, `catalogLiteral*`), which
use the engine's definition reader and reg* formatters; `typeProvider`
(its `Validate` reaches the expression binder); domain check programs and
evaluation; `reownCatalogScalarTypes` and `remapRegclassDatum`; the typed
inheritance and mutation steps that use ALTER TABLE records; the column
default records (`domain_default.go`); `domainDependency` and
`conversionDefault`.

## Testing

Package seams:

```sh
make check RUN='^(TestBuiltinConversionProcedures|TestBuiltinDomainSeeds|TestDomainDefaultReplayPairingInternal|TestPGEnumSortInsertion)$'
```

Engine witnesses that use the package through the catalog and DDL:

```sh
make check RUN='^(TestCatalogTypeJournalSlotsCopyOnWrite|TestDomainDefaultReplayAdmissionInternal|TestPGEnumAllocatorBounds|TestTypedTemporaryReplacementAdmission|TestConversionDescriptorCreateAndMoveUniqueness)$'
```

Public behavior is covered by the domain, enum, composite, range,
collation, conversion, typed-table and Dump/Restore selections in
`reference/` that the step 15 prompt file lists.
