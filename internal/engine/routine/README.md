# internal/engine/routine

Builtin routine and operator inventories and their lookups, registered index
support binding, the operator, cast, CREATE/ALTER/DROP routine and ordinary aggregate
DDL handlers, and operator/owned-routine dump statements. Immutable SQL routine
definitions and invocation frames are in [`proc`](../proc/README.md); call
resolution and SQL body binding stay in the engine.

SQL and PL declarations share formal/default/staging authority. DO and CREATE
check language USAGE; calls check EXECUTE, not language USAGE again. Dump renders
the actual language and orders real declaration prerequisites before PL source.
PL/pgSQL admits RETURNS trigger only for zero-formal trigger functions, never as
a scalar-storable pseudotype. Trigger installation checks function EXECUTE;
the admitted trigger entry does not recheck it at firing. Ordinary calls and
helper calls retain their normal effective-actor EXECUTE checks.

## Routine ALTER contract

`ALTER FUNCTION`, `ALTER PROCEDURE`, `ALTER ROUTINE` and `ALTER AGGREGATE`
support RENAME TO, SET SCHEMA and OWNER TO. Selection uses exact input identity,
not call-site coercion or default expansion. Aggregate utilities require an
explicit signature, with `(*)` for zero inputs; names and IN do not affect
identity, VARIADIC names the declared type, and output arguments are rejected.
Ordered-set utility syntax flattens direct and ordered arguments into the same
catalog signature. Other routine utilities use IN/INOUT/VARIADIC inputs and
ignore OUT arguments. FUNCTION includes ordinary, aggregate and window kinds;
PROCEDURE selects procedures; ROUTINE selects all kinds. Exact lookup precedes
the requested kind check, including for IF EXISTS.
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

Shipped function, aggregate and operator definitions and routine identity changes
share explicit, autocommit and retained-message publication. Programs, providers
and hard dependencies remain descriptor-owned; private calls use the owner's
current view. Read-only and syntax admission remain separate from publication. See
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
  `display_inventory.go` owns complete REL_18_6 source/language/security facts
  keyed by genuine declaration OID, including matched SQL-standard replacements.
  Materialization rejects missing facts; the display inventory never creates rows.
  SQL/PL creation records original quoted source or empty SQL-standard source
  on the descriptor. Identity changes preserve those immutable facts.
  `cast_inventory.go` joins only real executable cast-procedure references to
  released procedure identity/result facts, deduplicating shared procedures.
  Their `CastRoutineProvider` association executes the retained conversion
  kernel and ordinary typmod/explicitness operands; generic IO/domain/array
  coercions do not manufacture procedure rows.
  Every registered conversion/constructor also owns explicit released display
  facts in `display_inventory.go`; registration cannot bypass the complete
  materialization guard or invent argument names for unnamed inputs.
- **Operator inventory and metadata** (`operator_*.go`): released operator
  identities, their routine associations (`OperatorRoutine`,
  `OperatorRoutines`), `SeededOperatorNames`, `SupportedOperatorMetadata` and
  the seed constructor `BuiltinOperatorDescriptors`. **Adding an operator:** it
  becomes callable only through an explicit routine association and appears in
  `pg_operator` only through `SupportedOperatorMetadata`; every published
  descriptor must also be a row of `docs/design/pg-operator-pg18-supported.tsv`,
  which the engine and `reference/catalog_objects` inventory tests read.
  Geometric relation and transform declarations carry `OperatorRoutineProvider`:
  associations authorize operator execution, not newly callable support names.
  Existing polygon-point/circle-circle providers and callable registrations retain ownership.
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
  catalog lookup; `functions` stays catalog-free. Captures include actual
  member/family/routine revisions from the current private or committed view and
  remain immutable. Cube support requires the exact INTERNAL class address,
  never merely a nonzero family/class owner. Restore binds destination members.
  `RangeTypeRebinder` rebuilds a range
  type's support capture with destination identities for
  `typedef.ReownDomainTypes`.
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
- **Ordinary and moving aggregates** (`aggregate_*.go`): stage selected forward,
  inverse and final declarations with independent ordinary state, moving state
  and result types, atomically. `AggregateSupportTypes` specializes exact declared
  vectors without execution, default insertion or variadic expansion. Retained
  binary recipes govern every support vector after cast-row changes. Detached
  validation is shared by publication, Dump and replacement; it is not live
  EXECUTE authority. Dump preserves qualified support identities, both state
  types and external seeds, signed space estimates, EXTRA and modification flags,
  including flags without a final. Space estimates never size executor buffers.
- **Cast DDL** (`cast.go`): CREATE/DROP CAST with endpoint ownership/USAGE and
  signature checks; declaration neither evaluates the routine nor requires its
  EXECUTE privilege. Actual use checks current EXECUTE authority. Casts depend
  on endpoints, the function and selected adapters. Removal goes through the
  shared expression-dependency retirement path, retiring the directed pair index
  and its name binding. The engine journals these stages in autocommit, retained
  messages, SQL BEGIN and Go BeginTx, including earlier-private endpoint types,
  atomic row publication and full/savepoint rollback. Private pair observations
  never enter peer analysis; retained stored recipes keep their own captures.
  Binary casts still require superuser authority and genuine representation
  compatibility. Cast admission does not widen routine utility admission.
- **Dumps** (`operator_dump.go`, `function_dump.go`, `aggregate_dump.go`): the
  operator Collect decision (`OperatorDumped`), dump events
  (`OperatorDumpEvents`, `RoutineDumpEvents`) and statements
  (`OperatorDumpStatements`, `RoutineDumpStatement`). They render through
  `dump.Presenter` and find predecessors through `dump.Resolver`, so they read
  only their arguments.
  Semantic routine predecessors use `catalog.VisitProcedureDefinitionDependencies`:
  supports precede their aggregate, which precedes checked callers. Runtime-only
  support captures do not add transitive caller predecessors. Replay separately
  retains typed SQL formals (including OUT), the typed SQL result and
  declaration-only source prerequisites; semantic descriptor projections never
  replace that declaration metadata. Rendering still validates the complete
  original manifest, including support-only captures.
  Supported routine raw ACLs are captured separately by `control` and replayed
  after final identity/owner reconciliation, preserving NULL/empty arrays,
  delegated named grantors and grant options. Declarations do not render GRANT
  approximations or copy source principal/object identities.

## IF EXISTS suppression

Routine, operator and cast DROP inspect the SQL diagnostic authority for their
fixed suppression codes, emit the complete notice and return every unsuppressed
error with its outer provenance intact. Preparation stays quiet.
Aggregate DROP uses the same root staging and recorded dependent closure.
RESTRICT preserves dependents; CASCADE retires dependents, never aggregate
support/type prerequisites merely because the aggregate references them.
DROP permits schema-owner authority; ALTER requires actual aggregate ownership.
Missing zero-input errors render `(*)`, while missing notices retain `()`.

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

The handlers run under `Engine.Mu` without a statement scope and reach the
engine only through `DDLHost` (`host.go`): the shared
[`ddl.Host`](../ddl/README.md) plus `DefinitionType`, `DefinitionLookup`,
`SelectFunction`, `SelectAggregateSupport`, `ResolveRegtype`, `BindSQLRoutine`
and `BindSQLDefaults`. The engine implements it on `RoutineHost`, which embeds
the core `DDLHost` and is built once per DDL statement (`routine_host.go`);
`RoutineHost` also implements `index.FamilyDDLHost`.

- **`ddl.Authority`** is an opaque value kept by value. Host methods that change
  it (`CreationNamespace`, `Recapture`) return a new one, so an implicit
  namespace or peer recapture stays local to the helper that made it.
- **`ddl.Stage`** is the private catalog change; the engine stage keeps its
  lifecycle checks.
- Namespace, routine and type-name selection and SQL body binding stay behind
  the host because they need `analyze`. OIDs and revisions come from
  `AllocateOID` and `Revision` in the order the handlers ask. Every host method
  runs with `Engine.Mu` held and takes no lock.

## What stays in the engine

`RoutineHost` and schema statement dispatch (`routine_host.go`,
`operator_family_host.go`, `schema.go`); SQL routine binding, Restore validation and invocation
(`sql_routine*.go`); seed installation (`builtin_catalog.go`); call resolution
(`function*.go`, `operator*_binding.go`) and routine call inquiry admission;
`procedureReturnsSet`; the Collect entries `collectOperatorDump` and
`collectProcedureDump`. The emit entry and dump adapters are in
[`control`](../control/README.md).

## Testing

Package units cover inventory and operator lookups, seeded metadata, option
extraction, index support and exclusion binding, routine drop selection,
aggregate binary feasibility and the dump seams; engine units exercise the seed
and the handlers behind `RoutineHost` (operator, operator class and routine DDL
units live in `internal/engine/schemacmds`). Reduced internal fixtures must install
operator-class authority as well as family and routine authority. Dump changes
must also keep the [`dump`](../dump/README.md#testing) selections passing.
Public SQL lives in [reference/operators](../../../reference/operators/README.md#testing),
[reference/query_core](../../../reference/query_core/README.md#testing) (routine
drop) and [reference/aggregates](../../../reference/aggregates/README.md#testing).

```sh
make check RUN='^Test(BuiltinRoutineLookup|OperatorRoutineLookup|SeededOperatorNames|UserOperator(Option|CatalogEstimator)|OperatorFamilySupportDeclarations)'
make check RACE=1 RUN='^Test(OperatorClassMethodIdentity|ExclusionOperatorFamilyMembership|RoutineDrop)'
make check RUN='^Test(Aggregate(Final|Moving)DetachedDefinition|CreateAggregate(DetachedDeclaredCastSupport|Retained)|SQLRoutineDumpEventsUseResolver|OperatorDumpLinksUsePresenter|UserOperatorDumpFinalGraph)'
make check RUN='^Test(BuiltinCatalogValidatesForEveryEngineConfiguration|BuiltinProcedureNamesMatchSeed|OperatorDependsOnMatchesDependencyAddresses)'
make check RUN='^TestReference(RoutineDrop|UserOperator|CastDefinition)'
```

