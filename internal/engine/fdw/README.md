# internal/engine/fdw

Foreign data wrappers: the definitions the driver registers, the registry of
installed wrappers, validators and value codecs, foreign option transforms,
the foreign write protocol, the foreign transaction participants and the
decisions and helpers of WRAPPER, SERVER, USER MAPPING, IMPORT FOREIGN SCHEMA,
GRANT and foreign-table DDL.

The package is an upper feature at layer L12 of
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture): it
imports `access`, `authorization`, `catalog`, `ddl`, `dump`, `exec`, `expr`,
`ids`, `plan`, `regobj`, `stored`, `typedef`, `parser`, `session`,
`sqlstate`, `sqltype`, `table` (CREATE FOREIGN TABLE stages through
`table.StageCreateTable`) and the standard library, and never the engine,
`dml` or another upper feature. `table` never imports it. The engine and the
driver import it.

## What it owns

- **Public definitions** (`api.go`): `WrapperDefinition`,
  `ValidatorDefinition`, `ImportRequest`, `ColumnDefinition`,
  `TableDefinition` and `SchemaImporter`. The driver exposes them under their
  public names (`stuntgres.ForeignDataWrapperDefinition` and so on).
- **Registration** (`registry.go`): `ValidateWrapper` and `ValidateValidator`
  check a definition before the driver records it (`FunctionName` defaults
  the schema to `public`); their errors carry the engine's
  `RegistrationError` prefix. `Registry` holds the installed definitions, the
  auto-installed namespaces and the value codecs.
  Immutable ordered `ProcedureTokens` supply destination installation authority
  for bootstrap and replacement. SQL DROP does not unregister a driver's
  capability; a foreign source cannot supply its callback. Tokens are selected
  while constructing a private candidate, never by runtime name fallback.
  Core installation records each genuine handler/validator descriptor in both
  the exact name/signature map and the copy-on-write procedure-name index, so
  ordinary utility admission resolves the same capability identity. Handlers
  retain their empty input signature; validators retain `text[], oid`.
  Replacement rebinds imported tokens (`RebindReplacementProviders`) before
  the single strict source-principal graph memo is constructed. Historical original carriers use that same scoped
  source-to-destination token map; retained destination TEMP authority stays separate.
- **Options** (`options.go`): `TransformOptions` applies ordered ADD/SET/DROP
  operations to a private copy, then validates surviving names.
- **Row identity** (`identity.go`): `IdentitySet`, the keyed membership of
  foreign row identities, and `IdentityComparable`.
- **Write protocol** (`modify.go`): `Modification` drives a provider's
  Begin/Exec/End callbacks through a `WriteRuntime` (participant enlistment,
  modification tracking and value environments; the engine's `statementHost`
  implements it); `PlanModification` (the operation check and the wrapper's
  row identity and planning callbacks; the caller has released the
  replacement read), `ModifyAdmission`, `OperationSupported` and the TRUNCATE
  group record `TruncateGroup` (`GroupTruncate` groups the selected foreign
  tables by server in first-seen order; `SetTransaction` gives a group and
  its tables the server's participant transaction).
- **Metadata capability** (`updatability.go`): `UpdatableEvents` resolves the
  captured handler and inspects its actual UPDATE/DELETE interfaces. It does
  not require write row-identity support or call planning, scan, transaction
  or modification callbacks; it is safe in an immutable metadata capture.
- **Transaction participants** (`transaction.go`): `Transaction` is the
  owner's FDW state (`Participant`, `Enlist`, `Retire`, `TakeCleanup`,
  `Commit`, `Pending`, `Savepoint`); `Participant`, `ParticipantKey`,
  `Rollback`, `MarkName` and `CapabilityError`. `BeginParticipant` is a new
  participant's provider handshake: capability checks, Begin and the replay
  of the owner's open savepoint marks, rolling the transaction back on any
  later failure. `EndStatements` ends the statement in each open participant;
  a failed release closes and rolls back that participant once.
- **DDL helpers** (`ddl.go`, `import.go`, `table.go`, `scan.go`): object
  lookup (`Find`, `Class`, `ObjectRef`; the class test is
  `ids.IsForeignCatalogClass`, and the command tag and grant-kind test are
  `session.ForeignDDLTag` and `session.ForeignGrantKind`),
  mapping and ownership authority over an `access.View` (`MappedRole`,
  `MappingAuthority`, `MappingDiagnostic`, `MappingFor`; the owner change rule
  is `access.ForeignOwnershipEligibility`, shared with REASSIGN OWNED),
  validator jobs (`Validation`, `ValidatorJob`, `RunValidations`) retain the
  captured procedure and owned ordered options. Registered native validators
  run directly; ordinary SQL/PL validators use the owning statement's mandatory
  `ValidationRuntime`, with a non-NULL text[] and the option catalog's OID.
  Non-NULL results are ignored and NULL results rejected.
  Catalog-selected invocation omits only explicit-call EXECUTE admission, not
  current descriptor validation, caller body privileges or the shared routine
  SECURITY DEFINER/configuration and unwind. No owner is impersonated.
  The common runtime entry retains or opens the private statement journal for
  actual SQL invocation on every caller, including table ALTER and IMPORT;
  root Finish owns metadata and transactional routine writes together.
  Empty jobs and native callbacks do not require that SQL journal admission.
  No label lookup supplies executable authority.
  Table creation retains raw immutable column syntax and stable attribute
  identities. `PrepareColumnValidation` transforms and resolves one column only
  after table validation and preceding column phases succeed. Its installation
  payload is separate from the callback's mutable copy. Table validation precedes
  foreign metadata; column validation precedes installation of its own options.
  IMPORT helpers (`HandlerFor`, `ImportExpression`, `ImportedOptions`,
  `ImportError`), foreign-table syntax checks (`CheckTableDeclaration`,
  `TableSyntax`, `AlterAdmission`) and scan requests (`ScanRequest`,
  `RequiredAttributes`).
- **DDL decisions** (`host.go`, `stage.go`, `stage_drop.go`, `handler.go`,
  `grant.go`, `import.go`, `table_stage.go`, `table_alter.go`). `Host` is
  `table.Host` (and through it `ddl.Host` and `AddDomainBase`) plus
  `RemoveObject` and the utility `ResolveRelation`; `tablecmds.TableHost`
  implements it.
  - `StageDDL` stages CREATE, ALTER and DROP of wrappers, servers and user
    mappings in this order: actor, owner operand, wrapper superuser gate, the
    foreign-table route, then the drop or definition path with its IF [NOT]
    EXISTS notices, source-ordered HANDLER/VALIDATOR resolution (including
    execution-time redundant-clause errors), options, rename, ownership
    change and the two ALTER WRAPPER warnings. It returns the validator jobs
    and the dropped roots.
  - `StageTable` stages CREATE FOREIGN TABLE (namespace and CREATE, name
    conflict, server USAGE, declaration checks, `table.StageCreateTable`, the
    foreign definition with table options and an empty column map, one table
    validator job and ordered raw configured-column schedules) and DROP FOREIGN
    TABLE (IF EXISTS notices, kind and owner checks, removal); IMPORT stages
    each table through it too.
  - ALTER FOREIGN TABLE's decisions are `AlterTarget` (resolution, IF EXISTS
    notice, kind, admission reference and owner), `AlterBases` (server and
    wrapper as observed dependencies), `AlterCandidate` (the working copy),
    `AlterColumn`, `AlterOptions`, `CheckAddedColumn`, `AddedColumnOptions`
    and `UnsupportedAlter`.
  - `PlanImport` decides IMPORT FOREIGN SCHEMA up to the importer callback and
    records the server, wrapper, mapping, handler and local schema as observed
    dependencies; `ImportSelected` applies LIMIT TO/EXCEPT and
    `ImportedTable` builds each table's CREATE FOREIGN TABLE.
    The caller uses the same private construction schedule for direct CREATE
    and each imported definition, completing it before processing the next.
    Later validators see completed earlier definitions and column options.
    Discovery dependencies remain independently fenced even for empty imports.
    All metadata and transactional effects stay private until enclosing Finish;
    any later failure rolls them back without intermediate committed publication.
    A caught PL exception restores its private catalog/effect suffix and exact
    construction-token membership/progress through the existing exception mark.
    Outer construction remains live; failed nested construction cannot leak a
    pending guard into a successfully handled enclosing command.
    The imported-definition boundary owns one `ImportError` annotation for all
    staging, construction and validator failures; direct CREATE is unannotated.
  - `PlanGrant` is GRANT and REVOKE on wrappers and servers as a pure plan
    over an `access.View`: it returns the `GrantChange`s in statement order
    and the warnings, which come back even with a later error so the engine
    emits them first.
- **Logical persistence** (`dump.go`): `DumpObject`, `DumpTable`,
  `DumpProcedure` and `DumpOptions` render immutable captured declarations,
  actual qualified capability identities and ordered options; `control` owns
  graph ordering and output.
  - Foreign tables never use storage/row collection. Declaration references
    distinguish the actual procedure descriptor: `DumpProcedure` validates only
    native prerequisites, which require compatible destination registrations,
    never serialized code. Ordinary SQL/PL validators use the normal routine
    graph to replay their signature, body, result and dependencies without a
    native registration, preceding their wrappers and executing genuinely during
    atomic replay. Replacement only rebinds genuine native tokens to the
    destination registry; it does not convert ordinary routines into callbacks.
  - Mapping credentials are emitted as stored (sensitive host output).
    Wrapper/server and foreign relation/column raw ACLs replay exactly through
    `control`'s bounded generated catalog repairs.
  - Replay uses bootstrap CREATE, ordinary owner transfers and named raw ACL
    repair, and runs authentic option validators but never IMPORT, scan or
    modify callbacks.
  - Typed import parsing diagnostics keep definition-fragment positions
    rather than inventing offsets into the original IMPORT. Repeated IMPORT
    options stay ordered provider inputs, separate from stored-object option
    uniqueness.

Optional `catalog.ForeignParallelScanProvider` does not change registry or
persistence ownership. Executable capability is discovered on the registered
wrapper; `exec.ForeignExecution` owns runtime shared/partition states. Owner
enrollment and statement startup stay leader-only before concurrent partition
Iterate. SQL/MED Dump/Restore never invokes or serializes runtime coordination.
Child-path reparameterization remains the explicit foreign-hierarchy boundary,
not a no-op private-state callback or correlated ReScan alias.

Optional `catalog.ForeignAsyncScanProvider` likewise adds no registry or
persistence state. Its resource-free selected-path capability admits requests
only under eligible append frontiers. `ForeignExecution` owns each live request;
the existing source owner signals every request before joining any and then Ends
scans. Finite cursor motions settle requests into owned source rows before
detaching authority. Provider options remain opaque catalog data, including a
provider's own async capability policy; Dump/Restore never starts requests.

## Invariants

- **No locks, no callbacks under locks.** The package adds no mutex.
  `Engine.Mu` guards the `Registry`; the transaction owner's mutex and
  admission guard a `Transaction`. Functions that call application code
  (`RunValidations`, including its SQL invocation, the `Modification` and
  `Transaction` methods, `Rollback`) run only after the engine released
  `Engine.Mu` and `replacementMu`. Rollback uses `context.WithoutCancel`.
- **Commit order.** `Transaction.Commit` commits each open participant once.
  A failed Commit has an unknown outcome: it is never retried, and the other
  participants roll back.
- **Publication.** The `Registry` is written only while the engine installs
  definitions, before it is reachable. Afterwards its namespace map is
  replaced whole (`AdoptNamespaces`) in the same Restore publication as the
  catalog state, never mutated.
- **Errors.** Messages, SQLSTATEs, details and hints are compatibility
  contracts.

## What stays in the engine

The DDL orchestration is [`schemacmds`](../schemacmds/README.md)'s, because it
holds `Engine.Mu`, begins and discards stages, releases `replacementMu` before
callbacks and publishes:

- `ExecuteForeignDDL` (DROP dependents, validations, publication) and
  `stageForeignLocked` (a wrapper over `StageDDL` on `TableHost`);
- `executeForeignImport` (the importer callback, the per-table relock,
  re-authorization and epoch check, provider acquisition);
- `ExecuteForeignGrant` (the journal clone, emitting the warnings and
  `CatalogChangeLocked` per change);
- `executeForeignTableAlter` (the OWNER, RENAME and SET SCHEMA routes to the
  shared relation executors, the stage, the phased action loop with its
  context checks and lock windows, the table candidates through the
  `tablecmds` ALTER candidates, DROP COLUMN, whose candidate waits on locks,
  the CHECK capture and rebind, OIDs, validations and publication).

Core keeps:

- `InstallForeignDefinitions` and `RegistrationError`;
- scan capture (`captureForeignSourceLocked`) and participant enlistment
  (`enlistForeign`, `TouchForeignScan`), which need the statement scope:
  `enlistForeign` keeps the replacement-read release, the two `owner.Mu`
  validate and enlist windows around `BeginParticipant` and the statement's
  participant record; `finishForeignStatements` keeps the release and clears
  the slot around `EndStatements`;
- the commit and savepoint call sites, and the scope slot
  `foreignTableScope`, which holds engine cursors.

Foreign INSERT, UPDATE and DELETE execution is `dml.ExecuteForeign`; its
locked prologue (`executeForeignCommandLocked`), foreign TRUNCATE (the source
captures under `Engine.Mu` and the participant admission around
`GroupTruncate`) and COPY FROM stay in the engine.

## Testing

`^TestFDW` runs this package's units (modify identity, option transforms,
definition validation, IMPORT selection, ALTER candidates, participant
handshake and statement end, TRUNCATE grouping) and the engine's FDW units,
which need an `Engine`: `TestFDWStageTable` and `TestFDWLateDependency*` live
in `internal/engine/schemacmds`, and the parallel foreign scan units
(`TestFDWParallel*`) and `transaction_extension_test.go` in
`internal/engine/core`. Add
`RACE=1` for the participant tests.

```sh
make check RUN='^(TestFDW|TestOwnerExtension)'
```

Public behavior: `^TestReferenceFDW` (wrappers, servers, mappings, IMPORT,
foreign tables, foreign DML), `^TestReferenceObjectGrant` (the GRANT warnings
`PlanGrant` shares through `authorization.GrantWarning`),
`^TestReferenceTruncate` (foreign TRUNCATE's server grouping and participant
admission) and `^TestReferenceDumpRoundTrip$`.

Destination-owned registration lifecycle proof:
`make check RUN='^TestRestoreForeignProvider'`.
Public Reset/callback observations use
`make check RUN='^TestReferenceFDWNativeControls$'` in native and `REFERENCE=1`
modes; replacement does not invoke application callbacks to reconstruct tokens.
