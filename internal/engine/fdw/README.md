# internal/engine/fdw

Foreign data wrappers: the definitions the driver registers, the registry
of installed wrappers, validators and value codecs, foreign option
transforms, the foreign write protocol, the foreign transaction
participants and the pure helpers of WRAPPER, SERVER, USER MAPPING, IMPORT
FOREIGN SCHEMA, GRANT and foreign-table DDL. Step 26 of the engine split
created it (26b moved the clean set, 26d the WRAPPER, SERVER, USER MAPPING,
IMPORT and GRANT decisions, 26e the foreign-table decisions).
The package is an upper feature at layer L12 of
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture):
it imports `access`, `authorization`, `catalog`, `ddl`, `exec`, `expr`,
`ids`, `plan`, `parser`, `session`, `sqlstate`, `sqltype`, `table` (CREATE
FOREIGN TABLE stages through `table.StageCreateTable`) and the standard
library, and never the engine, `dml` or another upper feature. `table`
never imports it. The engine and the driver import it. See
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
  `statementHost` implements it); `PlanModification` (the operation check
  and the wrapper's row identity and planning callbacks; the caller has
  released the replacement read), `ModifyAdmission`, `OperationSupported`
  and the TRUNCATE group record `TruncateGroup`.
- **Transaction participants** (`transaction.go`): `Transaction` is the
  owner's FDW state (`Participant`, `Enlist`, `Retire`, `TakeCleanup`,
  `Commit`, `Pending`, `Savepoint`); `Participant`, `ParticipantKey`,
  `Rollback`, `MarkName` and `CapabilityError`.
- **DDL helpers** (`ddl.go`, `import.go`, `table.go`, `scan.go`): object
  lookup (`Find`, `Class`, `IsCatalogClass`, `CommandTag`,
  `ObjectRef`, `GrantKind`), mapping and ownership authority over an
  `access.View` (`MappedRole`, `MappingAuthority`, `MappingDiagnostic`,
  `MappingFor`; the owner change rule is `access.ForeignOwnershipEligibility`,
  which REASSIGN OWNED shares), validator jobs (`Validation`,
  `ValidatorJob`, `RunValidations`), IMPORT helpers (`HandlerFor`,
  `ImportExpression`, `ImportedOptions`, `ImportError`), foreign-table
  syntax checks (`CheckTableDeclaration`, `TableSyntax`, `AlterAdmission`)
  and scan requests (`ScanRequest`, `RequiredAttributes`).
- **DDL decisions** (`host.go`, `stage.go`, `stage_drop.go`, `handler.go`,
  `grant.go`, `import.go`, `table_stage.go`, `table_alter.go`). `Host` is
  `table.Host` (and through it `ddl.Host` and `AddDomainBase`) plus
  `RemoveObject` and the utility `ResolveRelation`; the engine's one
  staging adapter `ddlHost` implements it. `StageDDL` stages CREATE, ALTER and DROP of
  wrappers, servers and user mappings in today's order (actor, owner
  operand, wrapper superuser gate, the foreign-table route, then the drop or
  definition path with its IF [NOT] EXISTS notices, HANDLER/VALIDATOR
  resolution, options, rename, ownership change and the two ALTER WRAPPER
  warnings) and returns the validator jobs and the dropped roots.
  `StageTable` stages CREATE FOREIGN TABLE (namespace and CREATE, name
  conflict, server USAGE, declaration checks, `table.StageCreateTable`,
  the foreign definition, options and one validator job for the table and
  each column) and DROP FOREIGN TABLE (IF EXISTS notices, kind and owner
  checks, removal); IMPORT stages each table through it too. ALTER FOREIGN
  TABLE's decisions are `AlterTarget` (resolution, IF EXISTS notice, kind,
  admission reference and owner), `AlterBases` (server and wrapper as
  observed dependencies), `AlterCandidate` (the working copy),
  `AlterColumn`, `AlterOptions`, `CheckAddedColumn`, `AddedColumnOptions`
  and `UnsupportedAlter`.
  `PlanImport` decides IMPORT FOREIGN SCHEMA up to the importer callback and
  records the server, wrapper, mapping, handler and local schema as observed
  dependencies; `ImportSelected` applies LIMIT TO/EXCEPT and `ImportedTable`
  builds each table's CREATE FOREIGN TABLE. `PlanGrant` is GRANT and REVOKE
  on wrappers and servers as a pure plan over an `access.View`: it returns
  the `GrantChange`s in statement order and the warnings, which come back
  with a later error so the engine emits them first.

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

The DDL orchestration, because it holds `Engine.mu`, begins and discards
stages, releases `replacementMu` before callbacks and publishes:
`executeForeignDDL` (DROP dependents, validations, publication),
`stageForeignLocked` (a wrapper over `StageDDL` on `ddlHost`),
`executeForeignImport` (the importer callback, the per-table relock,
re-authorization and epoch check, provider acquisition) and
`executeForeignGrant` (the journal clone, emitting the warnings and
`catalogChangeLocked` per change); `executeForeignTableAlter` (the
OWNER, RENAME and SET SCHEMA routes to the shared relation executors, the
stage, the phased action loop with its context checks and lock windows,
the table candidates through the engine's ALTER wrappers, DROP COLUMN,
whose candidate waits on locks, the CHECK capture and rebind, OIDs,
validations and publication); `InstallForeignDefinitions`
and `RegistrationError`; scan capture (`captureForeignSourceLocked`) and
participant enlistment (`enlistForeign`, `TouchForeignScan`), which need the
statement scope; the commit and savepoint call sites; and the scope slot
`foreignTableScope`, which holds engine cursors. Foreign INSERT, UPDATE
and DELETE execution is `dml.ExecuteForeign` (step 29d.5), which imports
this package; its locked prologue (`executeForeignCommandLocked`), foreign
TRUNCATE and COPY FROM stay in the engine.

## Testing

```sh
make check RUN='^(TestFDW|TestOwnerExtension)'
make check RUN='^TestReferenceFDW'
make check RUN='^TestReferenceDumpRoundTrip$'
make check RUN='^TestReferenceObjectGrant'
```

The first selection runs this package's unit tests (`TestFDWModifyIdentity`,
`TestFDWOptionsTransform`, `TestFDWValidateDefinitions`,
`TestFDWImportSelected`, `TestFDWAlterCandidate`) and the engine's FDW unit tests, which need an
`Engine` and stay in `internal/engine` (`fdw_*_test.go`, including the
`StageTable` test `TestFDWStageTable`,
`transaction_extension_test.go`). `^TestReferenceObjectGrant` covers the
GRANT warnings `PlanGrant` shares through `authorization.GrantWarning`. Add `RACE=1` for the
participant tests, and `REFERENCE=1` to the reference selections for the
real PostgreSQL oracle.
