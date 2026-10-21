# internal/engine/fdw

Foreign data wrappers: the definitions the driver registers, the registry
of installed wrappers, validators and value codecs, foreign option
transforms, the foreign write protocol, the foreign transaction
participants and the pure helpers of WRAPPER, SERVER, USER MAPPING, IMPORT
FOREIGN SCHEMA, GRANT and foreign-table DDL. Step 26 of the engine split
created it (26b moved the clean set; 26d and 26e move the DDL decisions).
The package is an upper feature at layer L12 of
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture):
it imports `access`, `authorization`, `catalog`, `exec`, `expr`, `ids`,
`plan`, `parser`, `sqlstate`, `sqltype` and the standard library, and never
the engine, `dml`, `table` or another upper feature. The engine and the
driver import it. See
[ENGINE-SPLIT-fdw.md](../../../docs/ENGINE-SPLIT-fdw.md).

## What it owns

- **Public definitions** (`api.go`): `WrapperDefinition`,
  `ValidatorDefinition`, `ImportRequest`, `ColumnDefinition`,
  `TableDefinition` and `SchemaImporter`. The driver exposes them under
  their public names (`stuntgres.ForeignDataWrapperDefinition` and so on).
- **Registration** (`registry.go`): `ValidateWrapper` and
  `ValidateValidator` check a definition before the driver records it
  (`FunctionName` defaults the schema to `public`); their errors carry the
  engine's `RegistrationError` prefix. `Registry` holds the installed
  definitions, the auto-installed namespaces and the value codecs.
- **Options** (`options.go`): `TransformOptions` applies ADD/SET/DROP
  option lists with PostgreSQL 18 diagnostics.
- **Row identity** (`identity.go`): `IdentitySet`, the keyed membership of
  foreign row identities, and `IdentityComparable`.
- **Write protocol** (`modify.go`): `Modification` drives a provider's
  Begin/Exec/End callbacks through a `WriteRuntime` (participant
  enlistment, modification tracking and value environments; the engine's
  `statementHost` implements it); `ModifyAdmission`, `OperationSupported`
  and the TRUNCATE group record `TruncateGroup`.
- **Transaction participants** (`transaction.go`): `Transaction` is the
  owner's FDW state (`Participant`, `Enlist`, `Retire`, `TakeCleanup`,
  `Commit`, `Pending`, `Savepoint`); `Participant`, `ParticipantKey`,
  `Rollback`, `MarkName` and `CapabilityError`.
- **DDL helpers** (`ddl.go`, `import.go`, `table.go`, `scan.go`): object
  lookup (`Find`, `Class`, `Label`, `IsCatalogClass`, `CommandTag`,
  `ObjectRef`, `GrantKind`), mapping and ownership authority over an
  `access.View` (`MappedRole`, `MappingAuthority`, `MappingDiagnostic`,
  `OwnershipEligibility`, `MappingFor`), validator jobs (`Validation`,
  `ValidatorJob`, `RunValidations`), IMPORT helpers (`HandlerFor`,
  `ImportExpression`, `ImportedOptions`, `ImportError`), foreign-table
  syntax checks (`CheckTableDeclaration`, `TableSyntax`, `AlterAdmission`)
  and scan requests (`ScanRequest`, `RequiredAttributes`).

## Invariants

- **No locks, no callbacks.** The package adds no mutex. `Engine.mu` guards
  the `Registry`; the transaction owner's mutex and admission guard a
  `Transaction`. Functions that call application code (`RunValidations`,
  the `Modification` and `Transaction` methods, `Rollback`) run only after
  the engine released `Engine.mu` and `replacementMu`. Rollback uses
  `context.WithoutCancel`.
- **Commit order.** `Transaction.Commit` commits each open participant once.
  A failed Commit has an unknown outcome: it is never retried, and the other
  participants roll back.
- **Publication.** The `Registry` is written only while the engine installs
  definitions, before it is reachable. Afterwards its namespace map is
  replaced whole (`AdoptNamespaces`) in the same Restore publication as the
  catalog state, never mutated.
- **Errors.** Messages, SQLSTATEs, details and hints match the engine's
  previous output.

## What stays in the engine

The DDL orchestration (`executeForeignDDL`, `stageForeignLocked`,
`executeForeignGrant`, `executeForeignImport`, the foreign-table staging and
ALTER), because it holds `Engine.mu`, begins and discards stages, releases
`replacementMu` before callbacks and publishes; `InstallForeignDefinitions`
and `RegistrationError`; scan capture (`captureForeignSourceLocked`) and
participant enlistment (`enlistForeign`, `TouchForeignScan`), which need the
statement scope; the commit and savepoint call sites; and the scope slot
`foreignTableScope`, which holds engine cursors. Foreign DML execution
(`fdw_execute.go`, foreign TRUNCATE and COPY FROM) moves to `dml` (step 29),
which will import this package.

## Testing

```sh
make check RUN='^(TestFDW|TestOwnerExtension)'
make check RUN='^TestReferenceFDW'
make check RUN='^TestReferenceDumpRoundTrip$'
```

The first selection runs this package's unit tests (`TestFDWModifyIdentity`,
`TestFDWOptionsTransform`, `TestFDWValidateDefinitions`) and the engine's
FDW unit tests, which need an `Engine` and stay in `internal/engine`
(`fdw_*_test.go`, `transaction_extension_test.go`). Add `RACE=1` for the
participant tests, and `REFERENCE=1` to the reference selections for the
real PostgreSQL oracle.
