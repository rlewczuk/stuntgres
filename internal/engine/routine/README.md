# internal/engine/routine

Builtin routine and operator inventories, their lookups and validation,
registered index support binding, the operator, cast and CREATE/DROP routine DDL
handlers, ordinary aggregate declarations, and operator/owned-routine dump
statements.

## Imports

Layer L9 of [ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture)
(see also [ENGINE-SPLIT-routine.md](../../../docs/ENGINE-SPLIT-routine.md)). It
imports only `access`, `authorization`, `catalog`, `ddl`, `dump`, `expr`,
`functions`, `ids`, `parser`, `proc`, `session`, `sqltype`, `sqlstate` and the
standard library; never `analyze` (which imports `routine`), another feature
package or the engine.

## `routine` and `proc`

- [`proc`](../proc/README.md) owns immutable SQL routine definitions and
  invocation frames (L1). The catalog holds its definitions.
- `routine` owns the released builtin declarations, operator identities and
  metadata, and the validation used by operator and routine DDL. It reads the
  catalog but holds no state of its own.

## What it owns

- **Routine inventory** (`inventory.go`, `procedures.go`):
  `BuiltinRoutineDeclarations` (released `pg_proc` identities, not OID-sorted),
  `BuiltinRoutine` (by OID), `LookupBuiltinRoutineDeclaration` (by provider,
  name and input types) and the seed constructor `BuiltinProcedures`;
  `IdentityDefaults` (`identity_defaults.go`) counts a builtin identity's
  defaulted arguments, including lookup-only ones. Each executable declaration
  names its routine provider (for example `GeometryConversionRoutineProvider`,
  `GeometryMeasureRoutineProvider`) with fixed OID, signature and function
  attributes; lookup-only identities never become executable or grant a binding.
- **Native access-method handlers.** `NativeAccessMethodProcedures` installs the
  exact released internal handler identities with finite native
  heap/B-tree/hash/GiST capability associations. These are utility
  capabilities, not scalar internal-pointer routines; heap selects ordinary
  storage/visibility.
- **Routine bodies** (`body.go`): the builtin SQL-body edges, resolved once per
  process and attached to fresh descriptors by `BuiltinProcedures`, and
  `EmbeddedCastOID` for body capture. The instance-selected body walk
  (`WalkRoutineBody`, `BodyCastMatches`, `PolymorphicBodyArgument`) lives in
  `exec` with call admission.
- **Operator inventory** (`operator_inventory.go`, `operator_routines.go`,
  `operator_routine_index.go`, `operator_names.go`): released operator
  identities (`BuiltinOperatorCount`), their routine associations
  (`OperatorRoutine`, `OperatorRoutines`, keyed by `OperatorRoutineKey`) and the
  seeded name index `SeededOperatorNames`.
- **Operator metadata and catalog** (`operator_metadata.go`,
  `operator_catalog.go`): `SupportedOperatorMetadata`,
  `ReleasedOperatorEstimatorResult`, the descriptor validator `OperatorProvider`,
  `OperatorSeeded` and the seed constructor `BuiltinOperatorDescriptors`. An
  operator becomes callable only through an explicit routine association, and is
  published in `pg_operator` only through `SupportedOperatorMetadata`;
  lookup-only zero-function identities stay hidden. Every published descriptor
  must also be a row of `docs/design/pg-operator-pg18-supported.tsv`, which the
  engine and `reference/catalogs` inventory tests read.
- **Registered index support** (`index_support.go`, `index_support_binding.go`):
  the one catalog-aware class/member selector, `BindClassSupport`,
  `BindIndexOrdering`, `BindPartitionHash`, `BindIndexForeignKey` and
  `BindExclusionOperator`. A coherent `catalog.Read` supplies the current
  operator-class descriptor and actual family/member/routine identities and
  signatures; binding verifies exact method/family coherence, then finite
  `sqltype.NativeIndexRoutine*` associations select immutable native operations.
  `functions` stays catalog-free. Btree comparison 1, hash 1 and partition
  extended hash 2 keep genuine registrations for every class and cross-type RI
  comparison; optional physical callbacks are metadata-only. Known argument
  types must implicitly match the support signature before conversion, while
  unknown literals and parameters keep ordinary conversion diagnostics. There is
  no name/default/key-type fallback and no per-datum catalog lookup; captures are
  immutable and Restore binds destination members. Declaration volatility is
  authoritative.
- **Exclusion operator membership.** Binary-compatible index input admission
  does not establish exclusion operator membership: an integer column with
  `oid_ops` still resolves `=(integer,integer)`, and a text column with
  `bpchar_ops` resolves `=(text,text)`; an operator absent from the selected
  family fails with 42809. Reduced internal fixtures must install class
  authority as well as family and routine authority; uniqueness diagnostics need
  a populated catalog view.
- **Operator options** (`operator_options.go`): CREATE/ALTER OPERATOR option
  syntax, extraction and validation (`ValidOperatorName` and private helpers).
- **Operator DDL** (`operator_ddl.go`, `operator_links.go`, `operator_lookup.go`):
  `StageCreateOperator`, `StageAlterOperator` and `StageDropOperator`;
  commutator/negator resolution, forward shells and reciprocal staging;
  argument, utility-operator, implementation and estimator lookup.
- **Routine deletion** (`drop.go`): exact descriptor signatures through the
  catalog name index, kind-before-masking for bare names, owner/schema-owner
  authorization and system/extension fences. All roots are recorded before the
  engine's shared dependency closure and single publication; there is no second
  registry or call-coercion selector. Quoted SQL bodies create no hard edges.
- **SQL routine declarations** (`function_ddl.go`, `function_restore.go`):
  `StageCreateFunction` shares function/procedure body policies and VOID.
  Function declarations retain ordered IN/OUT/INOUT/VARIADIC formals, input
  defaults and input-only identity. One output uses the scalar executor;
  multiple-output adaptation, procedure parameter conventions and CALL are
  separate. Checked defaults bind against the pre-declaration stage (prior
  staged objects, excluding the new declaration); only then is the declaration
  staged for body validation and self-recursion. Declaration defaults are never
  deferred as quoted body text. On Restore (`RestoreDirective`,
  `ReplayDirective`, `RebindSQLDefinitions`), all formal types stabilize before
  checked defaults and standard captures are remapped.
- **Language admission** (`language.go`): namespace resolution and CREATE
  authority come first. Absent optional or custom languages give `42704`,
  C/internal authority `42501`, unavailable mechanisms `0A000`. It checks only
  locally decidable AS cardinality and standard-body rules and never loads a
  library or probes a symbol; `RejectCreateLanguage` checks the effective actor
  before rejecting explicit custom handlers. There is no callback registry or
  synthetic language row.
- **Ordinary aggregates** (`aggregate_ddl.go`, `aggregate_definition.go`): stage
  and validate the four-option declaration, real support/state identity and hard
  dependencies atomically. `AggregateSupportTypes` uses the pure callable-family
  shape/result authority for concrete specialization; it imports no analyzer,
  registers no callback and stores no executable payload in the catalog. Exact
  support selection stays on the DDL host. Concrete support formal/result
  usages keep the caller's derived collation, so detached specialization agrees
  with private-frame usage validation. Retained owners supply historical binary
  feasibility to polymorphic probes before conversion planning; an owner
  without a recipe uses fixed compatibility only, and fresh CREATE uses current
  catalog feasibility. SFUNC formals and strict/no-initial-value seeds require
  the catalog's direct implicit-binary authority; declared edges retain
  immutable binary recipes that stay valid after their cast row is dropped or
  replaced, never admitting function/INOUT adapters. Strict/no-init executor
  startup separately checks current binary feasibility; Prepare does not.
- **Cast DDL** (`cast.go`): ordinary CREATE/DROP CAST with endpoint authority,
  exact versus omitted function signatures, normal/nonset and binary
  signature-adapter checks, domain notices and binary physical/category
  validation. CREATE neither requires routine ownership/EXECUTE nor evaluates the
  routine. Casts depend on endpoints, the function and selected signature
  adapters, never an invented owner. The shared expression-dependency retirement
  path admits cast roots and dependent cast edges, validates their predecessor
  through the cast provider and retires both the pair index and its name
  binding; RESTRICT discovers blockers before retirement, CASCADE removes
  dependent casts but not their function providers. Ownerless ACL validation:
  structure belongs to the cast provider, ownership and USAGE checks to the real
  endpoint types; owner errors use search-path-visible type names. Prepare is
  quiet even in a read-only transaction; Execute performs the read-only check
  before the explicit-transaction DDL boundary.
- **Dumps** (`operator_dump.go`, `function_dump.go`, `aggregate_dump.go`): what
  native Dump writes for user operators, SQL routines and ordinary aggregates,
  including formal/state types, actual support, checked default and body edges.
  `OperatorDumped` is the operator Collect decision; `OperatorDumpDependencies`,
  `OperatorDumpEvents` and `NormalizeOperatorDumpPredecessors` add the
  declaration and deferred link events to the `dump.Graph`;
  `OperatorDumpStatements` (one `OperatorDumpDeclaration` per operator, orphan
  shells through a temporary generator) renders CREATE OPERATOR, owner changes
  and the `OperatorLinks` phase text; `OperatorShellGenerator` picks the defined
  operator that creates a forward shell. `RoutineDumpEvents` and
  `RoutineDumpStatement` cover owned SQL functions and aggregates. They render
  through `dump.Presenter` and find predecessors through `dump.Resolver`, so they
  read only their arguments. Statement owners preserve default-ACL ownership;
  receiving roles must already exist. Explicit SQL-routine or aggregate ACLs
  reject Dump before output rather than being ignored or reset.
- **Operator dependencies** (`operator_dependencies.go`):
  `OperatorDependencyAddresses` and `OperatorDependsOn`.
- **Small helpers**: `GrantRoutineKindMatches` (`grant_kind.go`) and
  `ObjectRoutineKind` (`address_kind.go`).

## IF EXISTS suppression

Routine, operator and cast DROP inspect the SQL diagnostic authority for their
fixed suppression codes, emit the complete notice and return every unsuppressed
error with its outer provenance/identity intact; input-origin wrappers do not
change this. DROP CAST suppresses only missing endpoint types (42704) or schemas
(3F000) during endpoint resolution; its notices keep the parsed type spelling and
normalized system aliases (for example `pg_catalog.int4`). Preparation stays
quiet. This is semantic error handling, not fresh-input recoverability.

## Invariants

- **Immutable tables.** Every inventory and index is built once per process at
  package initialization and never written afterwards. Lookups return shared
  pointers; callers must not modify them. The maps stay private and are read
  through `BuiltinRoutine`, `OperatorRoutine`, `OperatorRoutines` and
  `SeededOperatorNames`.
- **Seeding order.** `BuiltinProcedures` and `BuiltinOperatorDescriptors` are
  pure constructors returning fresh descriptors in installation order. The engine
  installs them (`installBuiltinProcedures`, `installBuiltinOperators`); OIDs,
  names, signatures, providers and metadata are fixed release facts.
- **Stable dumps.** Dump output is byte-stable.
- **No locks, no engine state.** The package has no mutex and reads the catalog
  only through `catalog.Read` and the DDL host.

## The DDL host

The handlers run under `Engine.mu` without a statement scope. They reach the
engine only through `DDLHost` (`host.go`), which embeds the shared
[`ddl.Host`](../ddl/README.md) and adds the routine methods (`DefinitionType`,
`DefinitionLookup`, `SelectFunction`, `SelectAggregateSupport`,
`ResolveRegtype`, `BindSQLRoutine`, `BindSQLDefaults`). The engine implements it
on its scope-free staging adapter, `ddlHost`, built once per DDL statement (the
routine methods are in `routine_host.go`):

- **`ddl.Authority`** is the statement authority as an opaque value
  (`access.Environment` plus the `DDLAuthority` marker). Handlers keep it by
  value. Host methods that change it (`CreationNamespace`, `Recapture`) return a
  new one, so an implicit namespace or a peer recapture stays local to the helper
  that made it. Checks go through `ddl.Check`, pairing a view with its own
  authority, and references through `ddl.ObjectRef`.
- **`ddl.Stage`** is the private catalog change (`Create`, `Replace`,
  `UpdateDescriptor`, `Get`, `LookupName`); the engine stage keeps its lifecycle
  checks. Operator names are searched with
  `SearchNamespaces(auth, name, ids.OperatorClassID)`.
- Namespace selection, routine selection, type-name resolution and SQL body
  binding stay in the engine behind the host, because they need `analyze`. OIDs
  and revisions come from `AllocateOID` and `Revision` in the order the handlers
  ask. Every host method runs with `Engine.mu` held and takes no lock.

## What stays in the engine

The `ddlHost` adapter and the schema statement dispatch (`ddl_host.go`,
`routine_host.go`, `schema.go`), SQL routine binding (`sql_routine_binding.go`:
`executeSQLRoutineDDL`, `bindSQLRoutineLocked`, `validateSQLRoutineResult`), the
Restore validation of SQL routine bodies (`sql_routine_restore.go`), SQL routine
invocation (`sql_routine.go`, `sql_routine_scope.go`), the catalog writes that
install the seed (`builtin_catalog.go`), call resolution (`function*.go`,
`operator_binding.go`, `operator_routine_binding.go`), routine call inquiry
admission (`routine_inquiry.go`), `dropProvider.clearOperatorLinks`,
`procedureReturnsSet` (it needs the intagg extension provider; `exec` reaches it
through `Preparation.ReturnsSet`), and the Dump capture: the Collect entries
`collectOperatorDump` and `collectProcedureDump` (it validates through
`stored.ProcedureProvider`). The Emit entry `emitDumpOperatorLinks`, the
`control.Presenter` and `control.Resolver` adapters and the script calls are in
`control`.

## Testing

Package units cover inventory and operator lookups, seeded operator metadata,
option extraction, declaration facts of selected routine providers, support
declaration metadata, index support and exclusion membership binding, routine
drop selection, aggregate binary feasibility and the dump seams. Engine units
exercise the seed and the DDL handlers behind `ddlHost`. Dump changes must also
keep the [`dump`](../dump/README.md#testing) selections passing. Public SQL lives
in [reference/operators](../../../reference/operators/README.md#testing),
[reference/query_core](../../../reference/query_core/README.md#testing) (routine
drop) and [reference/aggregates](../../../reference/aggregates/README.md#testing);
add `REFERENCE=1` for the oracle.

```sh
make check RUN='^Test(BuiltinRoutineLookup|OperatorRoutineLookup|SeededOperatorNames|UserOperator(Option|CatalogEstimator)|PointOperatorMetadata|MACADDR8OperatorMetadata|Geometry(Conversion|MeasureBoxLine)Declarations|FTSOpsRoutineDeclarations|OperatorFamilySupportDeclarations)'
make check RACE=1 RUN='^Test(OperatorClassMethodIdentity|ExclusionOperatorFamilyMembership|RoutineDrop)'
make check RUN='^Test(CreateAggregate(DetachedDeclaredCastSupport|Retained)|SQLRoutineDumpEventsUseResolver|OperatorDumpLinksUsePresenter|UserOperatorDumpFinalGraph)'
make check RUN='^Test(BuiltinCatalogValidatesForEveryEngineConfiguration|BuiltinProcedureNamesMatchSeed|OperatorDependsOnMatchesDependencyAddresses)'
make check RUN='^TestReference(RoutineDrop|UserOperator|CastDefinition)'
```
