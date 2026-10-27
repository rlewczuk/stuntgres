# internal/engine/routine

Builtin routine and operator inventories and their lookups, registered index
support binding, the operator, cast, CREATE/ALTER/DROP routine and ordinary aggregate
DDL handlers, and operator/owned-routine dump statements. Immutable SQL routine
definitions and invocation frames are in [`proc`](../proc/README.md); call
resolution and SQL body binding stay in the engine.

`OperatorProvider` validates reciprocal extension membership before ordinary
operand, result and backer checks. Only manifest-authenticated hstore recipe
operators may live outside the installation schema, through
`extension.HstoreRelocatableOperator`; full operator and extension validation
still follows. Comparison families and other providers keep their placement gates.

SQL and PL declarations share formal/default/staging authority. DO and CREATE
check language USAGE; calls check EXECUTE, not language USAGE again. Dump renders
the actual language and orders real declaration prerequisites before PL source.
PL/pgSQL admits RETURNS trigger only for zero-formal trigger functions, never as
a scalar-storable pseudotype. Trigger installation checks function EXECUTE;
the admitted trigger entry does not recheck it at firing. Ordinary calls and
helper calls retain their normal effective-actor EXECUTE checks.
Routine semantic attributes are descriptor-owned and serialized by native Dump.
CREATE/REPLACE starts complete SQL/PL defaults; ALTER patches a private current
copy. Identity and owner checks precede semantic attribute action validation;
malformed SECURITY modes are rejected by parsing, which supplies only normalized
definer/invoker values. Duplicate groups retain the second introducer position.
Ordered SET/RESET clauses validate and capture FROM CURRENT at execution through
the nonmutating DDL host bridge.
External Normalize suspends the same command with all publication locks released;
exact live readmission resumes it once, or rejects stale authority without
replaying callbacks. No session settings are installed, and rejected declarations
publish no partial state.
Reached invocation uses live authority; optimizer-elided strict NULL calls do
not acquire a runtime invocation. Parallel safety/cost/rows are declaration facts,
not an operational worker-scheduling capability.

Generic file declarations belong to the ordinary builtin inventory, including
all read/binary/stat/tmpdir overloads and fixed record outputs.
`SeedGenericFileACLs` installs owner-only read/stat/dirname ACLs and additional
`pg_monitor` grants for the fixed listing family, including tmpdir. These are
mutable procedure ACLs, not runtime name-based exceptions. Dump repairs preserve
raw NULL, empty and explicit states across restricted reseeding.

SQL result declarations retain set cardinality independently of scalar/composite
shape. OUT/INOUT and TABLE fields use the same ordered formal projections and
input-only signature. Functions with multiple outputs and procedures with any
output derive a declaration-owned record; TABLE retains output-only mode `t`.
PL anonymous OUT tuples use this same formal projection and detach their values
before the invocation frame closes. PL set declarations also admit named composite
results; dynamic RETURN QUERY validates their exact member usages at execution
and appends detached tuples through the existing invocation-private set sink.
Replacement checks actual row shape and setness, not just the shared RECORD OID.
Dump renders modes/defaults/VARIADIC and TABLE/SETOF, and orders real formal child
types and named result types before their callers.
Non-superuser routine ownership replays with authenticated administrator creation
and an immediate ordinary FUNCTION/PROCEDURE owner transfer using the input-only
identity. The emitter then restores the actual owner before dependent events;
captured ownership never presumes retained schema CREATE or language USAGE.

Declaration safety checks result/output determinability and INTERNAL input
requirements before replacement compatibility. Language admission follows the
existing declaration's ownership and compatibility checks. SQL admits polymorphic arguments and
polymorphic/RECORD/VOID results. PL keeps its own RECORD/polymorphic whitelist
and compiler validation. Unquoted SQL polymorphic bodies cannot freeze a concrete
analysis. Published signatures remain pseudo identities through Dump/Restore;
the existing presenter spells them independently of column admission.
PL compiler validation rejects `anycompatiblemultirange` formal slots, including
OUT, without excluding determinable result-only or SQL-family specializations.

PL/pgSQL event functions separately admit zero-formal `RETURNS event_trigger`,
inject only TEXT `TG_EVENT`/`TG_TAG`, and cannot be invoked as scalar functions.
`event_trigger_ddl.go` owns database-global CREATE and bounded ENABLE/DISABLE/DROP
decisions; creation checks actual superuser authority before event/filter/function
resolution, while state/drop use normal owner-role authority after lookup.
The engine owns phase scheduling, genuine private utility publication and
unlocked implicit invocation; no replication modes or helper SRFs are fabricated.

`CREATE OR REPLACE FUNCTION/PROCEDURE` uses the same SQL/PL declaration, default and body
binders as creation. Exact name/namespace/input identity retains OID, owner, ACL
and extension membership. Owner/kind/result checks, existing named inputs and
trailing default compatibility precede tentative replacement; body-validation
failure leaves the checked predecessor intact. SQL/PL language changes keep
ordinary validation. Builtin/externally implemented function replacement and
language lifecycle retain their separate admission contracts.

## Routine ALTER contract

`ALTER FUNCTION`, `ALTER PROCEDURE`, `ALTER ROUTINE` and `ALTER AGGREGATE`
support attribute patches as well as RENAME TO, SET SCHEMA and OWNER TO. Selection uses exact input identity,
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

Execution attribute actions (SECURITY DEFINER/INVOKER, SET/RESET/RESET ALL,
strictness and volatility) are supported only by genuine SQL/PL declarations,
including their ordinary, trigger and event-trigger entries. Native intrinsic
and installed providers reject every such action, even semantic no-ops, with
`0A000: execution attributes are unsupported for native routine providers`.
The action gate follows real lookup/kind/ownership checks and precedes setting
normalization, revision allocation and replacement. Parallel/cost/rows remain
metadata-only patches with ordinary cardinality and provider identity checks.
ROWS admission uses the catalog's intrinsic `Procedure.ReturnsSet` fact, shared
with projection; changing an estimate never changes that capability. These
patches neither replace intrinsic strictness/volatility nor enable workers.

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
  The three type inquiries (1081, 1619, 6315) use `CatalogRoutineProvider`,
  with released formal/result, stable/strict and display facts in the same
  inventories; the ANY formal remains distinct from its concrete invocation.
  Fixed OUT type IDs and complete formal names live on those same declaration
  rows as immutable `catalog.RoutineOutputs`, checked against the sole input
  signature. VARIADIC retains the existing declared signature and element/ANY
  identity; projection does not infer OUT layouts from result types or setness.
  Builtin input names and finite literal defaults are attached by immutable
  declaration identity, without installing lookup-only identities. Literal
  recipes preserve source type/spelling for presentation (including implicit
  assignment coercions), independently of call binding and default evaluation.
  Selected aggregate support descriptors also retain genuine signature/ACL facts
  with zero scalar-provider flags: opaque string-state capabilities own their
  execution, and diagnostic-only GiST/final signatures never gain fake bodies.
  The shared-memory view backing declarations likewise retain exact OUT,
  setness, display and EXECUTE ACL metadata with zero provider flags. Their
  descriptors share the canonical OID/pointer validation authority but remain
  outside the scalar provider/formal selector index. They authorize view
  materialization through common ACL checks, without callable functions or a
  shared-memory subsystem.
  `int4larger` has a real strict scalar maximum provider for SFUNC/COMBINEFUNC.
  Builtin string text/bytea supports share their exact `StringAggregateDefinition`
  with catalog rows and binding. Real support descriptors are retained in the
  aggregate call and remapped with it. Serial, worker transport and leader merge
  admission check only active supports under the current aggregate owner's
  authority, including the already-implemented builtin final step; no owner or
  permission cache is attached to the opaque state provider.
  Support admission follows the selected call's immutable aggregate operation
  and declarations, not the concrete state-provider type: wrapping a provider
  does not suppress the aggregate owner's active support checks.
  Catalog `proargtypes` is projected from normalized declared input identities as
  a genuine OIDVECTOR; `proacl` delegates to the existing object ACL producer.
  Aggregate domain INITCOND uses captured type diagnostic naming alongside the
  incumbent locked-domain input boundary, preserving current search-path spelling
  without changing independent schema/type/constraint fields.
  `display_inventory.go` owns complete REL_18_6 source/language facts and explicit
  cost inputs keyed by genuine declaration OID, including SQL-standard replacements.
  `BuiltinRoutineCost` supplies the immutable released/default cost for both
  materialization and replay comparison. Projection applies canonical row
  overrides while retaining genuine builtin/provider defaults.
  The inventory rejects missing facts and never creates additional rows.
  SQL/PL creation records original quoted source or empty SQL-standard source
  on the descriptor. Identity changes preserve those immutable facts.
  `body_inventory.go` owns every represented SQL-standard replacement's genuine
  REL_18_6 `system_functions.sql` parsed body recipe. RETURN expressions use the
  current parser's single-target SELECT representation. Recipes are parsed once
  at declaration setup and attached as readonly `DeclarationBody`, not fake
  checked `SQLBody` programs; missing or extra release body facts fail setup.
  User standard bodies retain their existing checked program owner. Native tree
  text deliberately differs from PostgreSQL `nodeToString`, while defaults
  deparse semantically through the existing expression owner.
  `cast_inventory.go` joins only real executable cast-procedure references to
  released procedure identity/result facts, deduplicating shared procedures.
  Their `CastRoutineProvider` association executes the retained conversion
  kernel and ordinary typmod/explicitness operands; generic IO/domain/array
  coercions do not manufacture procedure rows.
  Every registered conversion/constructor also owns explicit released display
  facts in `display_inventory.go`; registration cannot bypass the complete
  materialization guard or invent argument names for unnamed inputs.
  Statistics I/O declarations carry all twelve released internal-language source
  facts through that same materialization path. Receive routines retain metadata
  without a SQL-callable INTERNAL-argument provider.
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
  TEXT pattern lt/le/ge/gt own explicit scalar associations and cancellable
  bytewise comparisons, independent of locale, without BPCHAR provider admission.
- **Registered index support** (`index_support*.go`): the one catalog-aware
  class/member selector (`BindClassSupport`, `BindIndexOrdering`,
  `BindPartitionHash`, `BindIndexForeignKey`, `BindExclusionOperator`). It
  verifies method/family coherence over a coherent `catalog.Read`, then finite
  `sqltype.NativeIndexRoutine*` associations select immutable native
  operations. There is no name/default/key-type fallback and no per-datum
  catalog lookup; `functions` stays catalog-free. Captures include actual
  member/family/routine revisions from the current private or committed view and remain immutable.
  GiST and SP-GiST select distinct logical classes without granting scalar
  ordering or executing physical support callbacks. SP-GiST family membership
  governs exclusion predicates; method aliases retain their actual SQL identities.
  BRIN captures four genuine internal minmax summary operations through
  `catalog.ValidateBrinSupport`, not `NativeIndexRoutine` scalar comparator
  dispatch. Live class/family members remain authority for both supported minmax
  and explicit excluded-algorithm diagnostics.
  Cube and hstore support require the exact INTERNAL class address,
  never merely a nonzero family/class owner. Hstore binds only its installed
  B-tree comparator or two distinct hash routines, with authenticated manifest
  roles, signatures and revisions; dynamic procedures are not builtin OIDs.
  Restore binds destination members.
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
  The private SQL-body directive leaves quoted source unchanged and defers its
  catalog-dependent analysis/result checks to reached invocation, including
  defaults invoked by replay data. Complete declaration parsing, checked
  standard bodies, hard defaults and persistent capture remapping remain checked.
  DROP records every root before the engine's shared dependency closure and
  single publication. Language admission never loads a library or probes a
  symbol.
- **Ordinary, ordered-set, hypothetical and moving aggregates** (`aggregate_*.go`):
  stage genuine selected support routines with independent state/result types.
  Ordered declarations retain the direct/row split and canonical variadic
  identity. SFUNC receives state and row inputs; FINAL receives state, direct
  inputs and typed NULL row extras when requested. Support specialization never
  supplies defaults or packs arrays. INTERNAL/ANY slots require the selected
  aggregate-context support capability, not general pseudo-type coercion.
  Ordinary and moving final support lookup share state plus canonical direct
  declaration slots, or state plus the complete declaration for EXTRA, including
  collapsed paired ANY exactly once. Execution specializes independent expanded
  direct and row vectors. Strict transition declarations without an initial value
  check the first canonical declared argument (including directs) against state;
  execution-time strict seeding still consumes only the first admitted row.
  Ordered/hypothetical declarations default both final modification fields to
  READ_WRITE even without final/moving functions; ordinary declarations retain
  READ_ONLY, and each explicit override is independent.
  Creating an INTERNAL transition state requires the actual current role to be
  a superuser; inherited membership is not sufficient. This creation-only rule
  does not prohibit a later non-superuser owner or bypass live support privileges.
  Retained binary recipes govern scalar/SQL support conversions after cast changes.
  Detached validation is shared by publication, Dump and replacement; it is not
  live EXECUTE authority. Dump retains named declaration splits, qualified
  supports, seeds, EXTRA, HYPOTHETICAL and modification modes, even without a final.
  A transferred non-superuser-owned INTERNAL aggregate is replayed as the
  authenticated administrator and transferred with ordinary ALTER AGGREGATE
  OWNER before restoring owner authority and emitting dependents.
  Space estimates never size executor buffers.
  COMBINEFUNC retains a separate closed state/state frame alongside FINAL and
  moving support; partial workers never finalize states. The leader combines
  those actual states and invokes the selected FINAL once, retaining its distinct
  result usage. SERIALFUNC/DESERIALFUNC are paired INTERNAL capabilities, not
  public INTERNAL datums. SORTOP retains its exact operator identity and qualified
  dump syntax; PARALLEL retains the declaration's safety marking.
  Unsafe INTERNAL aggregate-result diagnostics precede native implementation
  availability checks; an INTERNAL transition state with a concrete FINAL result
  remains valid when its selected aggregate-context capability is implemented.
  Ordinary and moving INITCOND validation share the host's domain-input callback
  boundary rather than evaluating domain checks while holding the DDL lock.
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
`SelectFunction`, `RoutineSignatureSpelling`, `SelectAggregateSupport`,
`ResolveRegtype`, `BindSQLRoutine` and `BindSQLDefaults`. The engine implements
it on `schemacmds.RoutineHost`, which embeds the core `DDLHost` and is built once per DDL
statement (`routine_host.go`); `RoutineHost` also implements
`index.FamilyDDLHost`. Canonical regprocedure rendering stays behind this same
host boundary: handlers do not depend on `regobj` or its analysis/execution
dependencies.

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

[`schemacmds`](../schemacmds/README.md) holds `RoutineHost` and the schema
statement executors (`routine_host.go`, `operator_family_host.go`,
`schema.go`) and SQL routine body validation (`BindSQLRoutineLocked`,
`sql_routine_binding.go`); the root keeps its Restore hook
(`sql_routine_restore.go`); core keeps SQL routine invocation
(`sql_routine*.go`), seed installation (`builtin_catalog.go`), call resolution
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

Semantic declaration normalization and private patch/error ownership use
`RUN='^TestRoutineAttribute'`, also with `RACE=1`.
Statistics I/O declaration/display and complete seed materialization use
`RUN='^TestStatisticsObjectIORoutineDeclarations$'`; public catalog metadata
and strict calls belong to the statistics family in `reference/catalogs`.

Ordered declaration units distinguish canonical lookup from expanded execution:
`FinalDeclarationVectors` checks independent storage and collapsed ANY slots;
`CanonicalStrictDeclaration` checks normal/moving no-seed admission in both
compatibility directions; `MovingFinalDetachedDefinition` checks independent
ordinary/moving EXTRA flags against real overloads and collapsed ANY supports.

```sh
make check RUN='^Test(BuiltinRoutine(Lookup|Costs)|OperatorRoutineLookup|SeededOperatorNames|UserOperator(Option|CatalogEstimator)|OperatorFamilySupportDeclarations)'
make check RACE=1 RUN='^Test(OperatorClassMethodIdentity|ExclusionOperatorFamilyMembership|RoutineDrop)'
make check RUN='^Test(Aggregate(Final|Moving)DetachedDefinition|CreateAggregate(DetachedDeclaredCastSupport|Retained)|SQLRoutineDumpEventsUseResolver|OperatorDumpLinksUsePresenter|UserOperatorDumpFinalGraph)'
make check RUN='^TestAggregateOrdered(FinalDeclarationVectors|CanonicalStrictDeclaration|MovingFinalDetachedDefinition|DetachedDefinition)$'
make check RUN='^Test(BuiltinCatalogValidatesForEveryEngineConfiguration|BuiltinProcedureNamesMatchSeed|OperatorDependsOnMatchesDependencyAddresses)'
make check RUN='^TestReference(RoutineDrop|UserOperator|CastDefinition)'
```

