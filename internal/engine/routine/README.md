# internal/engine/routine

Builtin routine and operator inventories and their lookups, registered index
support binding, the operator, cast, CREATE/ALTER/DROP routine and ordinary aggregate
DDL handlers, and operator/owned-routine dump statements. Immutable SQL routine
definitions and invocation frames are in [`proc`](../proc/README.md); call
resolution and SQL body binding stay in the engine.

SQL and PL declarations share formal/default/staging authority. DO and CREATE
check language USAGE; calls check EXECUTE, not language USAGE again. Dump renders
the actual language and orders real declaration prerequisites before PL source.

## Routine ALTER contract

`ALTER FUNCTION`, `ALTER PROCEDURE` and `ALTER ROUTINE` support RENAME TO,
SET SCHEMA and OWNER TO. Selection uses exact input identity, not call-site
coercion or default expansion: IN/INOUT/VARIADIC types identify inputs and OUT
arguments are ignored before lookup. FUNCTION includes ordinary, aggregate and
window kinds; PROCEDURE selects procedures; ROUTINE selects all kinds.
Name-only selection must be unambiguous. Changes preserve the routine address,
overload siblings and immutable declaration/provider association. Collision keys
are namespace/name/input signature, regardless of result or routine kind.

ALTER requires actual routine ownership, not DROP's schema-owner allowance.
Rename checks source CREATE; relocation checks destination CREATE even for a
same-schema lookup. OWNER resolves role keywords separately from quoted names,
checks SET ROLE eligibility and recipient schema CREATE, and rewrites existing
owner ACL entries without applying creation defaults. Superuser and same-owner
no-op behavior remains ordinary; recipient/target resolution precedes that no-op.
Failures publish no partial descriptor, name index, ownership or dependency edit.

Hard view, standard-body, declaration-default, CHECK and rule calls retain
checked identity. Quoted bodies keep soft original source; qualified formals
bind against the current routine name while standard formals retain checked
slots. Held original SQL reanalyzes rather than acquiring an old-name alias.
Released builtin/window and installed extension execution stays attached to its
genuine provider. Moving one installed member changes neither membership nor
the extension target namespace or siblings.

Existing read-only/autocommit DDL boundaries remain. This ALTER work does not
add CALL, OR REPLACE, external ABIs, new declaration/result forms or a complete
pg_proc/pg_language projection. See
[native replay](../../../docs/FIXTURES.md#routine-identity-replay) and
[selected public proof](../../../reference/query_core/README.md#routine-alter-proof).

## Imports

Layer L9 of [ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture).
It imports only `access`, `authorization`, `catalog`, `ddl`, `dump`, `expr`,
`functions`, `ids`, `parser`, `proc`, `session`, `sqltype`, `sqlstate` and the
standard library; never `analyze` (which imports `routine`), another feature
package or the engine.

## What it owns

- **Routine inventory** (`inventory.go`, `procedures.go`, `body.go`):
  `BuiltinRoutineDeclarations`, `BuiltinRoutine` (by OID),
  `LookupBuiltinRoutineDeclaration` (by provider, name and input types), the
  seed constructors `BuiltinProcedures` and `NativeAccessMethodProcedures`, and
  the builtin SQL-body edges. Each executable declaration names its routine
  provider with fixed OID, signature and attributes; lookup-only identities
  never become executable or grant a binding.
- **Operator inventory and metadata** (`operator_*.go`): released operator
  identities, their routine associations (`OperatorRoutine`,
  `OperatorRoutines`), `SeededOperatorNames`, `SupportedOperatorMetadata` and
  the seed constructor `BuiltinOperatorDescriptors`. **Adding an operator:** it
  becomes callable only through an explicit routine association and appears in
  `pg_operator` only through `SupportedOperatorMetadata`; every published
  descriptor must also be a row of `docs/design/pg-operator-pg18-supported.tsv`,
  which the engine and `reference/catalogs` inventory tests read.
  Geometric relation declarations carry `OperatorRoutineProvider`: associations
  authorize operator execution, not newly callable support names. Existing
  polygon-point/circle-circle providers and callable registrations retain ownership.
  The metadata array remains OID-sorted for binary lookup and publishes genuine
  result/procedure/link/estimator facts without inferred reverse signatures.
  BOX/POLYGON positions publish their released BOOL signatures and zero
  commutator/negator fields with positionsel/positionjoinsel estimators; the
  mathematical inverse is not an inferred catalog link. POINT/CIRCLE rows keep
  their existing provider ownership.
- **Registered index support** (`index_support*.go`): the one catalog-aware
  class/member selector (`BindClassSupport`, `BindIndexOrdering`,
  `BindPartitionHash`, `BindIndexForeignKey`, `BindExclusionOperator`). It
  verifies method/family coherence over a coherent `catalog.Read`, then finite
  `sqltype.NativeIndexRoutine*` associations select immutable native
  operations. There is no name/default/key-type fallback and no per-datum
  catalog lookup; `functions` stays catalog-free; captures are immutable and
  Restore binds destination members.
- **Operator DDL** (`operator_ddl.go`, `operator_links.go`, `operator_lookup.go`,
  `operator_options.go`): `StageCreateOperator`, `StageAlterOperator`,
  `StageDropOperator`, commutator/negator links and forward shells.
- **Routine DDL** (`function_ddl.go`, `function_restore.go`, `drop.go`,
  `alter.go`, `utility_identity.go`, `language.go`): `StageCreateFunction` binds checked defaults against the
  pre-declaration stage, then stages the declaration for body validation and
  self-recursion; Restore (`RestoreDirective`, `ReplayDirective`,
  `RebindSQLDefinitions`) stabilizes all formal types before checked defaults.
  DROP records every root before the engine's shared dependency closure and
  single publication. Language admission never loads a library or probes a
  symbol.
- **Ordinary aggregates** (`aggregate_*.go`): stage the declaration, support
  and state identities and hard dependencies atomically. `AggregateSupportTypes`
  imports no analyzer, registers no callback and stores no executable payload
  in the catalog; declared binary edges keep immutable recipes that stay valid
  after their cast row changes.
- **Cast DDL** (`cast.go`): CREATE/DROP CAST with endpoint authority and
  signature checks; it never evaluates the routine. Casts depend on endpoints,
  the function and selected adapters. Removal goes through the shared
  expression-dependency retirement path, which retires both the pair index and
  its name binding.
- **Dumps** (`operator_dump.go`, `function_dump.go`, `aggregate_dump.go`): the
  operator Collect decision (`OperatorDumped`), dump events
  (`OperatorDumpEvents`, `RoutineDumpEvents`) and statements
  (`OperatorDumpStatements`, `RoutineDumpStatement`). They render through
  `dump.Presenter` and find predecessors through `dump.Resolver`, so they read
  only their arguments.

## IF EXISTS suppression

Routine, operator and cast DROP inspect the SQL diagnostic authority for their
fixed suppression codes, emit the complete notice and return every unsuppressed
error with its outer provenance intact. Preparation stays quiet.

## Invariants

- **Immutable tables.** Every inventory and index is built once at package
  initialization and never written afterwards. Lookups return shared pointers
  that callers must not modify; the maps are private.
- **Seeding order.** `BuiltinProcedures` and `BuiltinOperatorDescriptors` are
  pure constructors returning fresh descriptors in installation order; OIDs,
  names, signatures, providers and metadata are fixed release facts.
  Installed SQL labels/owners may change; bootstrap facts and immutable provider
  associations do not.
- **Stable dumps.** Dump output is byte-stable.
- **No locks, no engine state.** The package has no mutex and reads the catalog
  only through `catalog.Read` and the DDL host.

## The DDL host

The handlers run under `Engine.mu` without a statement scope and reach the
engine only through `DDLHost` (`host.go`): the shared
[`ddl.Host`](../ddl/README.md) plus `DefinitionType`, `DefinitionLookup`,
`SelectFunction`, `SelectAggregateSupport`, `ResolveRegtype`, `BindSQLRoutine`
and `BindSQLDefaults`. The engine implements it on `ddlHost`, built once per
DDL statement (`routine_host.go`).

- **`ddl.Authority`** is an opaque value kept by value. Host methods that change
  it (`CreationNamespace`, `Recapture`) return a new one, so an implicit
  namespace or peer recapture stays local to the helper that made it.
- **`ddl.Stage`** is the private catalog change; the engine stage keeps its
  lifecycle checks.
- Namespace, routine and type-name selection and SQL body binding stay behind
  the host because they need `analyze`. OIDs and revisions come from
  `AllocateOID` and `Revision` in the order the handlers ask. Every host method
  runs with `Engine.mu` held and takes no lock.

## What stays in the engine

`ddlHost` and schema statement dispatch (`ddl_host.go`, `routine_host.go`,
`schema.go`); SQL routine binding, Restore validation and invocation
(`sql_routine*.go`); seed installation (`builtin_catalog.go`); call resolution
(`function*.go`, `operator*_binding.go`) and routine call inquiry admission;
`procedureReturnsSet`; the Collect entries `collectOperatorDump` and
`collectProcedureDump`. The emit entry and dump adapters are in
[`control`](../control/README.md).

## Testing

Package units cover inventory and operator lookups, seeded metadata, option
extraction, index support and exclusion binding, routine drop selection,
aggregate binary feasibility and the dump seams; engine units exercise the seed
and the handlers behind `ddlHost`. Reduced internal fixtures must install
operator-class authority as well as family and routine authority. Dump changes
must also keep the [`dump`](../dump/README.md#testing) selections passing.
Public SQL lives in [reference/operators](../../../reference/operators/README.md#testing),
[reference/query_core](../../../reference/query_core/README.md#testing) (routine
drop) and [reference/aggregates](../../../reference/aggregates/README.md#testing).

```sh
make check RUN='^Test(BuiltinRoutineLookup|OperatorRoutineLookup|SeededOperatorNames|UserOperator(Option|CatalogEstimator)|OperatorFamilySupportDeclarations)'
make check RACE=1 RUN='^Test(OperatorClassMethodIdentity|ExclusionOperatorFamilyMembership|RoutineDrop)'
make check RUN='^Test(CreateAggregate(DetachedDeclaredCastSupport|Retained)|SQLRoutineDumpEventsUseResolver|OperatorDumpLinksUsePresenter|UserOperatorDumpFinalGraph)'
make check RUN='^Test(BuiltinCatalogValidatesForEveryEngineConfiguration|BuiltinProcedureNamesMatchSeed|OperatorDependsOnMatchesDependencyAddresses)'
make check RUN='^TestReference(RoutineDrop|UserOperator|CastDefinition)'
```

