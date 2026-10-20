# internal/engine/routine

Builtin routine and operator inventories, their lookups and validation, and
the operator and CREATE FUNCTION DDL handlers. The package sits at layer L10
of [ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture).
It imports only `access`, `authorization`, `catalog`, `expr`, `functions`,
`ids`, `parser`, `proc`, `session`, `sqltype`, `sqlstate` and the standard
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
  per process and attached to fresh descriptors by `BuiltinProcedures`; `WalkRoutineBody`, `BodyCastMatches`,
  `PolymorphicBodyArgument` and `EmbeddedCastOID` for body admission and
  capture.
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
- **Operator dependencies** (`operator_dependencies.go`):
  `OperatorDependencyAddresses` and `OperatorDependsOn`.
- **Routine descriptors** (`descriptor.go`): carrier extraction
  (`ExpressionRoutineDescriptor`, `ExpressionRecipeDescriptor`) and
  revalidation against the current catalog (`ValidateRoutineDescriptor`,
  `ValidateStoredRoutineDescriptor`, `SameRoutineDeclaration`).
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
engine only through `DDLHost` (`host.go`), which the engine implements on one
scope-free adapter, `schemaHost`, built once per schema statement:

- **`Authority`** is the statement authority as an opaque value
  (`access.Environment` plus a marker). Handlers keep it by value. Host
  methods that change it (`CreationNamespace`, `Recapture`) return a new one,
  so an implicit namespace or a peer recapture stays local to the helper that
  made it. Checks go through `AccessView(auth).Check(auth, ...)`, pairing a
  view with its own authority (`ddlCheck`).
- **`Stage`** is the private catalog change (`Create`, `Replace`,
  `UpdateDescriptor`, `Get`, `LookupName`); the engine stage keeps its
  lifecycle checks.
- Namespace selection, routine selection, type-name resolution and SQL body
  binding stay in the engine behind the host, because they need `analyze`.
  OIDs and revisions come from `AllocateOID` and `Revision` in the order the
  handlers ask. Every host method runs with `Engine.mu` held and takes no
  lock.

## What stays in the engine

The `schemaHost` adapter and the schema statement dispatch (`schema_host.go`,
`schema.go`), SQL routine binding (`sql_routine_binding.go`:
`executeCreateFunction`, `bindSQLRoutineLocked`,
`validateSQLRoutineResult`), SQL routine dumps and Restore validation
(`sql_routine_dump.go`) and SQL routine invocation (`sql_routine.go`,
`sql_routine_scope.go`), the catalog writes that install the seed
(`builtin_catalog.go`), call resolution (`function*.go`,
`operator_binding.go`, `operator_routine_binding.go`; step 12), routine call
admission (`routine_admission.go`, `routine_body_admission.go`,
`routine_program.go`, `routine_inquiry.go`; step 11),
`selectExpressionRecipe`, `dropProvider.clearOperatorLinks`,
`procedureReturnsSet` (it needs the intagg extension provider, step 16), and
operator dump rendering (`operator_dump.go`), which needs the engine's dump
graph and type-declaration rendering until a dump seam exists; the SQL
routine dumps wait for the same seam.

## Testing

Package seams and option validation:

```sh
make check RUN='^Test(BuiltinRoutineLookup|OperatorRoutineLookup|SeededOperatorNamesIsOneIndex|UserOperatorCatalogEstimatorVariants|UserOperatorCatalogEstimatorMetadata|UserOperatorOptionExtraction)$'
```

Engine witnesses that exercise the package through the catalog seed and
the DDL handlers behind `schemaHost`:

```sh
make check RUN='^Test(BuiltinCatalogValidatesForEveryEngineConfiguration|BuiltinProcedureNamesMatchSeed|PGOperatorProviderAgreement|OperatorDependsOnMatchesDependencyAddresses|UserOperator|SQLRoutine)'
```

Public behavior is covered by `reference/operators` and the regproc,
regoperator and `pg_proc` selections listed in the step 14 prompt file.
