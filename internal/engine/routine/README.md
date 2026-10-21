# internal/engine/routine

Builtin routine and operator inventories, their lookups and validation, the
operator and CREATE FUNCTION DDL handlers, and the operator and SQL routine
dump statements. The package sits at layer L10
of [ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture).
It imports only `access`, `authorization`, `catalog`, `ddl`, `dump`, `expr`,
`functions`, `ids`, `parser`, `proc`, `session`, `sqltype`, `sqlstate` and the standard
library; never `analyze` (which imports `routine`), another feature package
or the engine. Step 14 of the split moves it here; see
[ENGINE-SPLIT-routine.md](../../../docs/ENGINE-SPLIT-routine.md).

## `routine` and `proc`

- [`proc`](../proc/README.md) owns immutable SQL routine definitions and
  invocation frames (L1). The catalog holds its definitions.
- `routine` owns the released builtin declarations, operator identities and
  metadata, and the validation used by operator and routine DDL. It reads
  the catalog but holds no state of its own.

## What it owns

- **Routine inventory** (`inventory.go`, `procedures.go`):
  `BuiltinRoutineDeclarations` (REL_18 `pg_proc` identities, not OID-sorted),
  `BuiltinRoutine` (by OID), `LookupBuiltinRoutineDeclaration` (by provider,
  name and input types) and the seed constructor `BuiltinProcedures`;
  `IdentityDefaults` (`identity_defaults.go`) counts a builtin identity's
  defaulted arguments, including lookup-only ones.
- **Routine bodies** (`body.go`): the builtin SQL-body edges, resolved once
  per process and attached to fresh descriptors by `BuiltinProcedures`, and
  `EmbeddedCastOID` for body capture. The instance-selected body walk
  (`WalkRoutineBody`, `BodyCastMatches`, `PolymorphicBodyArgument`) lives in
  `exec` with call admission.
- **Operator inventory** (`operator_inventory.go`, `operator_routines.go`,
  `operator_routine_index.go`, `operator_names.go`): released operator
  identities (`BuiltinOperatorCount`), their routine associations
  (`OperatorRoutine`, `OperatorRoutines`, keyed by `OperatorRoutineKey`) and
  the seeded name index `SeededOperatorNames`.
- **Operator metadata and catalog** (`operator_metadata.go`,
  `operator_catalog.go`): `SupportedOperatorMetadata`,
  `ReleasedOperatorEstimatorResult`, the descriptor validator
  `OperatorProvider`, `OperatorSeeded` and the seed constructor
  `BuiltinOperatorDescriptors`.
- **Operator options** (`operator_options.go`): CREATE/ALTER OPERATOR option
  syntax, extraction and validation (`ValidOperatorName` and private
  helpers). Messages and SQLSTATEs match PostgreSQL 18.
- **Operator DDL** (`operator_ddl.go`, `operator_links.go`,
  `operator_lookup.go`): `StageCreateOperator`, `StageAlterOperator` and
  `StageDropOperator`; commutator/negator resolution, forward shells and
  reciprocal staging; argument, utility-operator, implementation and
  estimator lookup.
- **CREATE FUNCTION** (`function_ddl.go`): `StageCreateFunction` for
  LANGUAGE SQL functions. `function_restore.go` holds the pure Restore
  helpers `RestoreDirective`, `ReplayDirective` and `RebindSQLDefinitions`.
- **Dumps** (`operator_dump.go`, `function_dump.go`): what native Dump
  writes for user operators and SQL functions. `OperatorDumped` is the
  operator Collect decision; `OperatorDumpDependencies`, `OperatorDumpEvents`
  and `NormalizeOperatorDumpPredecessors` add the declaration and deferred
  link events to the `dump.Graph`; `OperatorDumpStatements` (one
  `OperatorDumpDeclaration` per operator, orphan shells through a temporary
  generator) renders CREATE OPERATOR, owner changes and the `OperatorLinks`
  phase text; `OperatorShellGenerator` picks the defined operator that
  creates a forward shell. `SQLRoutineDumpEvents` and
  `SQLRoutineDumpStatement` do the same for LANGUAGE SQL functions. They
  render types and routine identities through `dump.Presenter` and find
  predecessors through `dump.Resolver`; both are pure, so these functions
  read only their arguments.
- **Operator dependencies** (`operator_dependencies.go`):
  `OperatorDependencyAddresses` and `OperatorDependsOn`.
- **Small helpers**: `GrantRoutineKindMatches` (`grant_kind.go`) and
  `ObjectRoutineKind` (`address_kind.go`).

## Invariants

- **Immutable tables.** Every inventory and index is built once per process
  at package initialization and never written afterwards. Lookups return
  shared pointers; callers must not modify them. The maps stay private and
  are read through `BuiltinRoutine`, `OperatorRoutine`, `OperatorRoutines`
  and `SeededOperatorNames`.
- **Seeding order.** `BuiltinProcedures` and `BuiltinOperatorDescriptors`
  are pure constructors returning fresh descriptors in installation order.
  The engine installs them (`installBuiltinProcedures`,
  `installBuiltinOperators`); OIDs, names, signatures, providers and
  metadata are fixed release facts.
- **No locks, no engine state.** The package has no mutex and reads the
  catalog only through `catalog.Read` and the DDL host.

## The DDL host

The handlers run under `Engine.mu` without a statement scope. They reach the
engine only through `DDLHost` (`host.go`), which embeds the shared
[`ddl.Host`](../ddl/README.md) and adds the routine methods
(`DefinitionType`, `DefinitionLookup`, `SelectFunction`, `ResolveRegtype`,
`BindSQLRoutine`). The engine implements it on its one scope-free staging
adapter, `ddlHost`, built once per DDL statement (the routine methods are in
`routine_host.go`):

- **`ddl.Authority`** is the statement authority as an opaque value
  (`access.Environment` plus the `DDLAuthority` marker). Handlers keep it by
  value. Host methods that change it (`CreationNamespace`, `Recapture`)
  return a new one, so an implicit namespace or a peer recapture stays local
  to the helper that made it. Checks go through `ddl.Check`, pairing a view
  with its own authority, and references through `ddl.ObjectRef`.
- **`ddl.Stage`** is the private catalog change (`Create`, `Replace`,
  `UpdateDescriptor`, `Get`, `LookupName`); the engine stage keeps its
  lifecycle checks. Operator names are searched with
  `SearchNamespaces(auth, name, ids.OperatorClassID)`.
- Namespace selection, routine selection, type-name resolution and SQL body
  binding stay in the engine behind the host, because they need `analyze`.
  OIDs and revisions come from `AllocateOID` and `Revision` in the order the
  handlers ask. Every host method runs with `Engine.mu` held and takes no
  lock.

## What stays in the engine

The `ddlHost` adapter and the schema statement dispatch (`ddl_host.go`,
`routine_host.go`, `schema.go`), SQL routine binding (`sql_routine_binding.go`:
`executeCreateFunction`, `bindSQLRoutineLocked`,
`validateSQLRoutineResult`), the Restore validation of SQL routine bodies
(`sql_routine_restore.go`) and SQL routine invocation (`sql_routine.go`,
`sql_routine_scope.go`), the catalog writes that install the seed
(`builtin_catalog.go`), call resolution (`function*.go`,
`operator_binding.go`, `operator_routine_binding.go`; step 12), routine call
inquiry admission (`routine_inquiry.go`), `dropProvider.clearOperatorLinks`,
`procedureReturnsSet` (it needs the intagg extension provider, step 16;
`exec` reaches it through `Preparation.ReturnsSet`), and the Dump
orchestration: the Collect and Emit table entries (`collectOperatorDump`,
`collectProcedureDump`, which validates through `stored.ProcedureProvider`, and
`emitDumpOperatorLinks`), the `dumpPresenter` and `dumpResolver` adapters
and the calls in `dump_script.go`.

## Testing

Package seams and option validation:

```sh
make check RUN='^Test(BuiltinRoutineLookup|OperatorRoutineLookup|SeededOperatorNamesIsOneIndex|UserOperatorCatalogEstimatorVariants|UserOperatorCatalogEstimatorMetadata|UserOperatorOptionExtraction)$'
```

Dump seams and graph events:

```sh
make check RUN='^Test(SQLRoutineDumpEventsUseResolver|OperatorDumpLinksUsePresenter|UserOperatorDumpFinalGraph)$'
```

Engine witnesses that exercise the package through the catalog seed and
the DDL handlers behind `ddlHost`:

```sh
make check RUN='^Test(BuiltinCatalogValidatesForEveryEngineConfiguration|BuiltinProcedureNamesMatchSeed|PGOperatorProviderAgreement|OperatorDependsOnMatchesDependencyAddresses|UserOperator|SQLRoutine)'
```

Public behavior is covered by `reference/operators` and the regproc,
regoperator and `pg_proc` selections listed in the step 14 prompt file.
Dump output must stay byte-identical: also run the Dump, Restore and Control
selections in the [`dump` README](../dump/README.md#testing).
