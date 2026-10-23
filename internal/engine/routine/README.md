# internal/engine/routine

Builtin routine and operator inventories, their lookups and validation, the
operator and CREATE/DROP routine DDL handlers, ordinary aggregate declarations,
and operator/owned-routine dump statements. The package sits at layer L9
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
  `NativeAccessMethodProcedures` installs exact released internal handler
  identities with finite native heap/B-tree/hash/GiST capability associations.
  These are utility capabilities, not scalar internal-pointer return routines.
  Heap selects existing ordinary storage/visibility and does not enable custom
  TABLE selection syntax. Lookup-only routine identities cannot grant a binding.
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
- **Registered index support** (`index_support.go`,
  `index_support_binding.go`): the one catalog-aware class/member selector,
  `BindClassSupport`, `BindIndexOrdering`, `BindPartitionHash`,
  `BindIndexForeignKey` and `BindExclusionOperator`. A coherent catalog.Read
  supplies actual family/member/routine identities and signatures; finite
  `sqltype.NativeIndexRoutine*` associations select immutable native operations.
  Functions remains catalog-free. Btree comparison1, hash1 and partition
  extended hash2 retain genuine registrations for every existing class and
  cross-type RI comparison. Optional physical callbacks are metadata-only.
  Scalar wrappers351/450/425 share genuine integer primitives with root/routing
  execution; known argument types must implicitly match their signatures before
  conversion, while unknown literals and parameters retain ordinary conversion
  diagnostics and inference. There is no routine-name/key-type fallback or
  per-datum catalog lookup. Declaration volatility remains authoritative:
  cross-timezone comparisons2357/2383/2526/2533 are STABLE, not IMMUTABLE.
- **Operator options** (`operator_options.go`): CREATE/ALTER OPERATOR option
  syntax, extraction and validation (`ValidOperatorName` and private
  helpers). Messages and SQLSTATEs match PostgreSQL 18.
- **Operator DDL** (`operator_ddl.go`, `operator_links.go`,
  `operator_lookup.go`): `StageCreateOperator`, `StageAlterOperator` and
  `StageDropOperator`; commutator/negator resolution, forward shells and
  reciprocal staging; argument, utility-operator, implementation and
  estimator lookup.
- **Routine deletion** (`drop.go`): exact descriptor signatures through the
  catalog name index, kind-before-masking for bare names, owner/schema-owner
  authorization and system/extension fences. All roots are recorded before the
  engine's shared dependency closure and single publication; no second registry
  or call-coercion selector exists. Quoted SQL bodies do not create hard edges.
- **SQL routine declarations** (`function_ddl.go`): `StageCreateFunction`
  shares function/procedure body policies and VOID. Function declarations retain
  ordered IN/OUT/INOUT/VARIADIC formals, input defaults and input-only identity.
  One output uses the scalar executor; multiple-output adaptation, procedure
  parameter conventions and CALL remain separate. `function_restore.go` holds
  `RestoreDirective`, `ReplayDirective` and `RebindSQLDefinitions`: all formal
  types stabilize before checked defaults and standard captures are remapped.
  Declaration defaults are never deferred as quoted body text.
  Checked defaults bind against the legitimate pre-declaration stage, including
  prior staged objects but excluding the new declaration. Only then is the
  declaration staged for body validation and existing self-recursion behavior.
- **Ordinary aggregates** (`aggregate_ddl.go`, `aggregate_definition.go`):
  stage and validate the four-option declaration, real support/state identity
  and hard dependencies atomically. `AggregateSupportTypes` uses the existing
  pure callable-family shape/result authority for concrete specialization; it
  imports no analyzer, registers no callback and stores no executable payload
  in the catalog. Exact support selection remains on the DDL host.
  Concrete support formal/result usages retain the caller's derived collation
  rather than resetting to descriptor defaults, so detached destination
  specialization agrees with exact private-frame usage validation.
  Retained owners also supply historical binary feasibility to polymorphic
  common-type and argument-edge probes before conversion planning; an owner
  without a recipe uses fixed compatibility only, never later live casts.
  Fresh CREATE passes no retained owner and uses current catalog feasibility.
  SFUNC formals and strict/no-initial-value seeds require the catalog's direct
  implicit-binary authority. Declared edges retain immutable binary recipes;
  detached validation accepts the original supported representation after its
  cast row is dropped or replaced, without admitting function/INOUT adapters.
  Execution captures real datum adapters rather than using IO or blind retags.
  Strict/no-init executor startup separately checks current binary feasibility,
  including empty input; Prepare does not execute that check.
- **Dumps** (`operator_dump.go`, `function_dump.go`, `aggregate_dump.go`): what
  native Dump writes for user operators, SQL routines and ordinary aggregates,
  including formal/state types, actual support, checked default and body edges.
  `OperatorDumped` is the operator Collect decision; `OperatorDumpDependencies`, `OperatorDumpEvents`
  and `NormalizeOperatorDumpPredecessors` add the declaration and deferred
  link events to the `dump.Graph`; `OperatorDumpStatements` (one
  `OperatorDumpDeclaration` per operator, orphan shells through a temporary
  generator) renders CREATE OPERATOR, owner changes and the `OperatorLinks`
  phase text; `OperatorShellGenerator` picks the defined operator that
  creates a forward shell. `RoutineDumpEvents` and `RoutineDumpStatement`
  cover owned SQL functions and aggregates. They render types and routine
  identities through `dump.Presenter` and find predecessors through
  `dump.Resolver`; both are pure, so these functions read only their arguments.
  Existing statement owners preserve default-ACL ownership; receiving roles
  must already exist. Explicit SQL-routine or aggregate ACLs reject Dump before
  output rather than being ignored or reset.
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
(`DefinitionType`, `DefinitionLookup`, `SelectFunction`, `SelectAggregateSupport`,
`ResolveRegtype`, `BindSQLRoutine`, `BindSQLDefaults`). The engine implements it on its scope-free staging
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
`executeSQLRoutineDDL`, `bindSQLRoutineLocked`,
`validateSQLRoutineResult`), the Restore validation of SQL routine bodies
(`sql_routine_restore.go`) and SQL routine invocation (`sql_routine.go`,
`sql_routine_scope.go`), the catalog writes that install the seed
(`builtin_catalog.go`), call resolution (`function*.go`,
`operator_binding.go`, `operator_routine_binding.go`; step 12), routine call
inquiry admission (`routine_inquiry.go`), `dropProvider.clearOperatorLinks`,
`procedureReturnsSet` (it needs the intagg extension provider, step 16;
`exec` reaches it through `Preparation.ReturnsSet`), and the Dump
capture: the Collect entries `collectOperatorDump` and
`collectProcedureDump` (it validates through `stored.ProcedureProvider`).
The Emit entry `emitDumpOperatorLinks`, the `control.Presenter` and
`control.Resolver` adapters and the script calls are in `control` (step 30).

### Cast DDL

`cast.go` stages ordinary CREATE/DROP CAST, with endpoint authority, exact versus
omitted function signatures, normal/nonset and binary signature-adapter checks,
domain notices and binary physical/category validation. CREATE does not require
routine ownership/EXECUTE or evaluate the routine. Casts depend on endpoints,
function and selected signature adapters, not an invented cast owner. Publication,
DROP closure and read-only/autocommit policies remain the shared DDL path.
The shared expression-dependency retirement path admits cast roots and dependent
cast edges, validates their authentic predecessor through the cast provider, and
records ordinary catalog removals. Candidate and published removals retire both
the directed pair index and its name binding; RESTRICT discovers blockers before
retirement, while CASCADE removes dependent casts without removing their surviving
function providers. Retained immutable catalog views keep the old definition.

CAST descriptors also use the shared ownerless ACL-validation path: structural
publication validation belongs to the cast provider, while ownership and USAGE
checks belong to the real endpoint types. Owner errors use search-path-visible
type names; DROP IF EXISTS notices retain parsed type spelling and normalized
system aliases (for example `pg_catalog.int4`), like routine deletion notices.
Prepare is quiet even in an explicit read-only transaction; Execute performs
the existing read-only check before the explicit-transaction DDL boundary.

## Testing

Ordinary aggregate declaration, authorization, catalog/dependency and actual
replay proof is owned by
[reference/aggregates](../../../reference/aggregates/README.md#create-aggregate-contract).
Private declaration/publication, actor and detached-specialization fences join
the structural transition/remap units under:

```sh
make check RUN='^(TestCreateAggregate|TestSelectCommonUsage)'
```

`TestCreateAggregateDetachedDeclaredCastSupport` exercises real `array_append`
specialization with catalog-owned enum/array descriptors and one-way INOUT
casts. Implicit A→B selects B[]/B→B[]; implicit B→A selects A[]/A→A[].
Assignment-only and explicit-only edges refuse implicit specialization in
both directions, and the released pseudo declaration remains unchanged.
This pure unit does not admit nonbinary SFUNC execution or issue SQL:

```sh
make check RUN='^TestCreateAggregateDetachedDeclaredCastSupport$'
```
Retained binary-definition and malformed-recipe units include real polymorphic
`array_append`, concrete BYTEACAT, absent/replaced/reverse live edges and
descriptor-generation remapping. A nil historical recipe cannot borrow a
later declared edge:

```sh
make check RUN='^TestCreateAggregateRetained(Polymorphic)?BinaryDefinition$'
```

Public direct/prepared/held/stored, same/fresh-owner native Restore and unchanged
dump-SQL PostgreSQL replay are owned by the cast-definition family:

```sh
make check RUN='^TestReferenceCastDefinitionAggregate(Binary|PolymorphicBinary)$'
make check RUN='^TestReferenceCastDefinitionAggregate(Binary|PolymorphicBinary)$' REFERENCE=1
```


The seventeen REL_18_6 geometric conversion declarations retain fixed OIDs, exact
input/result signatures, strict immutable parallel-safe function attributes and
the dedicated `GeometryConversionRoutineProvider` association. No lookup-only
adjacent constructor or operator becomes executable.

```sh
make check RUN='^TestGeometryConversionDeclarations$'
```

Selected cast DDL/runtime coverage:
`make check RUN='^TestReferenceCastDefinition(Grammar|Notices|Signatures|SignatureAdapters|Authority|Dependencies|FunctionAndContexts|UtilityPolicies)$'`
and the same selector with `REFERENCE=1`.


POINT's ten released operator identities (eight predicates plus deprecated
vertical aliases) are explicitly associated with their eight genuine callable
routine declarations. SQL `!=` normalizes to `<>`; no ordinary equality or
ordering association is invented. These associations retain selected EXECUTE
authority rather than relying on lookup-only catalog metadata.
Their real seeded descriptors also carry released BOOL result, implementation,
commutator/negator and estimator metadata through `SupportedOperatorMetadata`.
This existing metadata seam controls ordinary `pg_operator` publication;
lookup-only zero-function identities remain hidden rather than being fabricated
by the catalog projection.

FTS algebra similarly publishes its seven selected operator descriptors through
`SupportedOperatorMetadata`, independently of its routine associations. The
released vector/query result types, prefix kind, actual implementations,
containment commutators and5040/5041 estimators are asserted by
`TestFTSOpsRoutineDeclarations` and the shared public authority scenario. The
independent `docs/design/pg-operator-pg18-supported.tsv` includes all seven rows,
so complete catalog inventory consumers must also be selected in both modes.
This does not promote FTS matching/comparison/index or estimator execution.


```sh
make check RUN='^Test(OperatorRoutineLookup|PointOperatorMetadata)$'
```

The units check exact POINT routine identity/pointer ownership, actual seeded
descriptor fields and absent ordinary equality/order associations. End-to-end
predicate, catalog-row and authorization proof remains in the shared
[POINT reference definitions](../../../reference/types/README.md#point-oracle-first-contract).

Index support binding reads the current operator-class descriptor and verifies
exact method/family coherence before selecting the real registered member and
native routine signature. Names/defaults are not a fallback authority; captures
are immutable and destination Restore binds destination members. No scalar
support-function exposure or arbitrary SQL callback ABI is added.

```sh
make check RUN='^TestOperatorClass(SupportExecution|MethodIdentity|CatalogAtomicity)$'
make check RACE=1 RUN='^TestOperatorClass(SupportExecution|MethodIdentity|CatalogAtomicity)$'
```
`TestOperatorClassMethodIdentity` passes immutable403/90000 method facts sharing
btree mechanics through the production constructor and private binder path.
Real Procedure descriptors reach `selectNativeIndexSupport`; the test never
installs captures with `WithSupport`. Same-spelling class/default keys select
independent family/member/routine identities and revisions, with351(1,3)=-1
versus181(1,3)=-2. Crossed facts/family/input captures, missing support/procedures
and invalid native signatures/results/identities fail without default fallback.


The custom-class root witness independently distinguishes351(1,3)=-1 from
181(1,3)=-2, checks existing-row build and maintained INSERT and retains the old
root. Public anti-fallback overflow proof:
`make check RUN='^TestReferenceOperatorClassComparisonConsumption$'`, and the
identical selector with `REFERENCE=1`.


Package seams and option validation:

```sh
make check RUN='^Test(BuiltinRoutineLookup|OperatorRoutineLookup|SeededOperatorNamesIsOneIndex|UserOperatorCatalogEstimatorVariants|UserOperatorCatalogEstimatorMetadata|UserOperatorOptionExtraction)$'
```

Independent PostgreSQL 18.6 support declaration metadata (including volatility,
signatures, results and strictness for every installed native support provider):

```sh
make check RUN='^TestOperatorFamilySupportDeclarations$'
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

Routine deletion selectors and private replay boundaries:

```sh
make check RUN='^TestRoutineDrop(Parser|Selection|CatalogIsolation|ReplayClassification)$' RACE=1
make check RUN='^TestReferenceRoutineDrop(Identity|Diagnostics|Dependencies|Atomicity|OperatorDependency)$'
make check RUN='^TestReferenceRoutineDrop(Identity|Diagnostics|Dependencies|Atomicity|OperatorDependency)$' REFERENCE=1
make check RUN='^TestReferenceRoutineDrop(AuthorityAndAdmission|QuotedBody|Persistence|FeatureSmoke)$'
make check RUN='^TestReferenceRoutineDrop(AuthorityAndAdmission|QuotedBody|Persistence|FeatureSmoke)$' REFERENCE=1
```

Use an external 900-second deadline. The selected public feature smoke is
`make check RUN='^TestReferenceRoutineDropFeatureSmoke$'`, also with `REFERENCE=1`.
Expect preserved text overload104/INT8, precise view/default/check cascade,
RESTRICT2BP01, missing NOTICE00000, wrong-kind42809 and a dangling caller that
survives native replay and returns14/INT4 after callee recreation.

Index/exclusion support consumes current catalog classes, family members and
routine descriptors. Binary-compatible index input admission does not establish
exclusion operator membership: an integer column with `oid_ops` still resolves
`=(integer,integer)`, and a text column with `bpchar_ops` resolves `=(text,text)`.
Both remain SQLSTATE `42809` when that actual operator is absent from the selected
family. Reduced internal fixtures must install class authority as well as family
and routine authority; uniqueness diagnostics require a populated catalog view.

```sh
make check RUN='^TestExclusionOperatorFamilyMembership$'
make check RUN='^TestReferenceExclusionOperatorFamilies$'
make check RUN='^TestReferenceExclusionOperatorFamilies$' REFERENCE=1
make check RUN='^(TestIndexClauses|TestMaterializedRefreshConcurrentMatch|TestInsertConflictPredicate|TestPartitionPruning)'
```

Function parameter proof and smoke:

```sh
make check RUN='^(TestRoutineParameterLayout|TestSQLCallableParameterMapping|TestSQLRoutineWrittenCapture|TestSQLRoutineParameterRemap|TestRoutineDefaultOriginRestoration|TestSQLRoutineNamedProviderShapes|TestSQLRoutineDumpEventsUseResolver)$'
make check RUN='^TestReferenceRoutineParameters(NotationDefaults|DefaultEvaluation|ScalarOutputsIdentity|Variadic|WrittenCapturePersistence)$'
make check RUN='^TestReferenceRoutineParameters(NotationDefaults|DefaultEvaluation|ScalarOutputsIdentity|Variadic|WrittenCapturePersistence)$' REFERENCE=1
```

The shared complete contract and bounded selected groups are in
[reference/operators](../../../reference/operators/README.md#routine-parameter-binding).

Input-origin wrappers do not change utility IF EXISTS semantics. Routine,
operator and cast DROP inspect the existing SQL diagnostic authority for their
unchanged suppression codes, emit the existing complete notice, and return every
unsuppressed error with its outer provenance/identity intact. DROP CAST suppresses
only missing endpoint types (`42704`) or schemas (`3F000`) during endpoint
resolution; preparation remains quiet and domain warnings are unchanged. This is
semantic error handling, not fresh-input recoverability classification.

```sh
make check RUN='^TestReferenceRoutineDrop(Diagnostics|AuthorityAndAdmission|Atomicity)$'
make check RUN='^TestReferenceUserOperator(EdgeDiagnostics|DiagnosticsAtomicity)$'
make check RUN='^TestReferenceCastDefinitionNotices$'
```

Run both reference groups again with `REFERENCE=1`; external900-second deadlines.
