# internal/engine/functions

Builtin functions: signatures and argument rules, immutable call nodes,
evaluation, aggregate and window providers, and each family's bind code with its
registry entry. The analyzer that drives binding is in
[`analyze`](../analyze/README.md), stored presentation in
[`stored`](../stored/README.md), execution in [`exec`](../exec/README.md), and
the authority behind evaluation capabilities in the root
[engine](../README.md#function-runtimes).

`EmbeddedVersionBanner` is the pure native product/version identity shared by
`version()` and implementation-information metadata; it retains no runtime or
catalog authority.

`random` and `random_normal` retain their selected declarations and children
through binding. Conditional planning and statement preflight process required
constant children before strict literal-NULL elimination. Required immutable
parameter children use the imported typed frame and current actor; an incomplete
frame defers elimination. Discarded volatile and row-dependent siblings are not
evaluated. Non-NULL calls remain volatile and unfolded. Runtime row NULL still
evaluates the written siblings before testing strictness or borrowing the
session generator.

`AccessInquiry` uses a closed kind for role membership, row security and routine,
language, wrapper and server privilege inquiries. Role NAME/OID and object
TEXT/OID axes bind independently to exact selected declarations. Arguments
evaluate once in written order before strict NULL handling; the BOOL recipe
retains neither a role snapshot nor a decision. `ACLRuntime.ObjectPrivilege`
borrows the executing statement's current private catalog and expression
principal, including on every reuse of a held prepared statement. Text routine
identities use the existing regprocedure resolver. Inquiry-specific PUBLIC
defaults and unknown role OIDs do not change creation or execution admission.


## Imports and layering

- A leaf library: it imports only [`expr`](../expr/README.md), `fts`, `ids`, `sqltype`,
  `parser` and `sqlstate`, never the root engine or the catalog, storage, lock
  or visibility packages. It is catalog-free: catalog facts arrive through
  `Binder` or an evaluation runtime, never from a static OID table.
- It reaches the engine only through `Binder` at bind time and through
  `expr.Context` plus the capability interfaces asserted from it at evaluation
  time. `expr.Context` does not grow per family.
  JSONPath functions, operators and SQL/JSON consumers pass the current captured
  input through `JSONPathEnvironment`. Precision warnings borrow the current
  statement's `FunctionJSONPathNotice` capability; neither plans nor parsed paths
  retain a session, notice sink or execution scratch. Ordinary path operators
  use the explicit environment-bearing `BinaryPlan.EvaluateJSONPath` entry point.
- `expr` owns `RoutineReference`, `RoutineRecipeReference`, `RandomOwner`,
  `IntrinsicPropertyRule` and the `RoutineProvider` representation;
  `routine_provider.go` aliases the type (so catalog declarations need not
  import this package) and owns the family constants. There are no other
  aliases.
- Nodes embed `expr.Info`, hold children as `expr.Expr` and evaluate with
  `Eval(expr.Context)`.
- Snapshot calls use the ordinary typed binder and stored-expression protocols.
  Current-snapshot evaluation borrows an immutable command capture through
  `TransactionInquiryRuntime`; it never allocates an XID or refreshes visibility.
  The member SRF declares target-set placement and stage dependence, preserves
  occurrence identity across rewrites, and keeps only its immutable value and
  invocation-private index. NULL and empty member lists exhaust that invocation
  without producing a row.

`ts_parse` and `ts_token_type` share one fixed-OUT source provider. Their text
and OID formals select ordinary routine authority; text-name resolution borrows
the statement's captured text-search namespace path. Inputs evaluate once in
written order before strict NULL handling. Document tokenization completes
privately through the FTS leaf before a source cursor can publish a token.
Cursor advancement constructs typed records against its current result
descriptor, so rewritten plans retain no stale composites or invocation state.

`VerifyHeapam` holds six canonical typed inputs, including omitted defaults.
Named-input binding and applicability share slot assignment and ordinary
coercion; supplied inputs evaluate once in canonical formal order. The
non-STRICT volatile source delegates to a distinct statement capability after
current installed identity and EXECUTE admission. Every invocation owns its
materialized four-field record set, including the empty set.

## Registry

`registry.go` holds the static `definitions` table; there is no `init()`
registration. `Lookup(name)` claims a call by unqualified name,
case-insensitively unless `ExactSpelling`. The analyzer's `BindFunctionProvider`
(`analyze/function.go`) runs SQL-overload competition, then `Lookup`, then its
remaining name chain, so a registered name must not overlap a family the chain
still dispatches. `Bind` owns exact-spelling resolution, visibility, argument
analysis and every diagnostic.

- `ExactSpelling`: claim only the exact parser-folded names; other spellings
  keep the chain's missing-function path.
- `SharedGates`: the analyzer applies the shared label and spelling gates before
  `Bind`; `OwnsLabels` and `OwnsSpelling` (both require it) leave the label or
  exact-spelling diagnostic to `Bind`.
- `Claims`: a custom admission predicate replacing `Names` matching; registry
  tests check every name in `Names` satisfies it.
- `ProbeCall`, `ResultCall`, `NamedCall`: pure overload-competition hooks.

Bind code lives in `<family>_bind.go` or beside the family's node. Calls reached
by syntax or by the operator binder (SQL value functions, OVERLAPS, SIMILAR,
`||`, JSON operators, SQL/JSON, sequence calls through `BindSequenceCall`)
enter through exported `Bind*`, `Finish*` and `Select*` functions and bypass the
registry. Shared diagnostics: `call_syntax.go` (`ValidateCallSyntax`),
`resolution_error.go` and `operator_error.go`.

Setting-call statement arguments resolve in their owner's rowless namespace
before an unsupported expression-shape diagnostic. This analysis does not admit
new executable argument shapes; literal, parameter and typed-literal inputs keep
their existing fixed-formal coercion and routine identity.

`CastRoutine` is the selected executable cast-procedure provider. Its checked
routine declaration supplies signature, namespace, strictness and volatility;
`sqltype.CoercionPlan` supplies conversion and session codec semantics. Typmod
and explicitness arguments remain evaluated operands. Rebuild, capture, grouping
and rendering preserve the selected call rather than reinterpreting it as a
typename shortcut. No procedure becomes callable from type-catalog presence alone.
`String` optionally retains a `CitextStringCapture` for a selected installed
scalar. Reconstruction, grouping, rendering and type retagging preserve its
opaque declaration and member reference; explicit formal replay casts retain
overload selection. The shared strict typed-NULL preflight precedes routine
admission, while nonnull calls use current caller permissions. Matching and
translation remain type-owned plans rather than name-based expression dispatch.


## Geometric relation operator provider

`GeometryRelation` consumes one or two already selected/coerced children, preserving
routine authority, immutable volatility, namespace/group identity and cancellation.
Runtime row NULL does not hide a later child error; the existing literal-NULL
shortcut remains. Rebuild rebinds actual operand/result authority. Analyzed
deparsing retains formal display casts; private scalar replay retains separate
formal transports, including NULL. Ordinary view dumps use captured source AST
literals instead, whose owning-type annotations belong to `regobj.ConstantLiteral`.
Both paths preserve overload identity. Capture is operator-only, not function-call
admission. Polygon-point and circle-circle nodes retain their existing ownership;
the callable registry is not expanded by relation routine metadata.
BOX/POLYGON positions use this node as well, including formal typed NULL
operands in persisted expressions. Their predicates do not add expression
variants or change the existing POINT/CIRCLE providers.
LINE/LSEG prefix alignment exposes one active child in evaluation, rebuilding,
group identity and operator capture. Its template rendering retains qualified
operator identity and typed formal transports during replay; binary relations
retain their two-child infix form.
Public execution, ACLs and stored consumers use
[`reference/geometry_network`](../../../reference/geometry_network/README.md#testing).


## Callable competition with SQL overloads

- The analyzer's shared selector ranks builtin and installed providers in the
  same namespace/signature competition as SQL declarations. Builtin metadata
  supplies omitted trailing defaults and variadic expansion; expanded declared
  formals stay the ranking inputs. Polymorphic substitution is never an exact
  concrete match.
- `ProbeCall` (a `CallShape`) and `NamedCall` receive analyzed arguments,
  separate declared-input and resolved-target vectors and a `CallTypeContext`
  with an effect-free `CanCoerce`, and reuse the provider's own pure
  target-selection helpers. No hook binds, infers parameters, converts values,
  captures dependencies, constructs a provider or evaluates, so a losing call
  leaves nothing behind. Unknown operands stay deferred until the winner binds.
- `OriginalArgument` recovers syntax through the private slot carrying an
  already bound operand (`Argument.Raw`), never by matching source keys.
  `selectedCallReentry` is consumed once by the immediate gateway; nested
  winner frames restore it and `SelectedCallArguments`.
- Resolution failure or ambiguity never falls back to a builtin, and a
  common-resolver miss is never retried through a provider.

## Closed source providers

- Source-capable nodes implement `expr.Source` and `SourceOpening` beside their
  family (`*_source.go`); there is no central source registry. Admission is per
  family, never a scalar-FROM fallback.
- Arguments evaluate once per open in native order. Each invocation's state is
  released by close, EOF, error and cancellation without invalidating emitted
  values; set families convert a complete invocation privately before
  publishing a row. `JSONTable` is a composite source carrier whose
  `OpenSource` owns all row state.
- Occurrence hooks keep each family's exact identity, not structural equality.
  Equivalent-child substitution preserves selected providers and never calls
  `Reconstruct`. Query scheduling stays in the engine.

## Binder

`Binder` (`binder.go`) is what bind code may ask of the analyzer;
`analyze.Binder` implements it (`analyze/function_binder.go`). Each method is a
reviewed decision:

- `AnalyzeArgument` / `CoerceArgument` / `CanCoerce`: occurrence-owned
  analysis, formal coercion selected by intent (intents are values in
  `arguments.go`, not methods) and effect-free feasibility for call shapes.
- `Type` / `ArrayType` / `Info`: binding-catalog descriptors (identity
  comparisons and `Usage()` need them) and node metadata that records type and
  collation dependencies.
- `BuiltinVisible` / `BuiltinOperatorVisible` / `BuiltinRoutineInfo` /
  `BuiltinRoutineReference` / `BuiltinOperatorRoutineInfo`: whether lookup
  reaches `pg_catalog` under `search_path`, and the selected builtin routine
  identity (catalog state).
- `DiagnosticTypeName` / `SignatureError` / `CallSignatureError` /
  `RecordUnresolvedCallable`: diagnostic spelling and the analyzer's 42883
  routing (a copy here would fork it); deferral needs the recorded missing
  identity as the terminal error, not its SQLSTATE alone.
- `AnalyzeFilter` / `DefaultConsumer`: a scalar call's FILTER errors precede
  the decoration rejection; whether a column DEFAULT is being bound.
- `CommonUsage` / `ComparisonInfo` / `CollatedInfo` / `RetainUntypedParameter`:
  common type, comparison collation and pending untyped `$n` for ANY formals,
  with the analyzer's diagnostics and dependency recording.
- `NoteCatalogText` / `NoteCatalogSnapshot` / `NoteVisibilitySnapshot`:
  statement-level snapshot demand; none captures eagerly.
- `InstalledRoutine`: an opaque handle to an `Extension`'s installed routine;
  `Attach` returns the engine's wrapper (installation is engine state).
- `CaptureComparisonInput` / `CaptureTypeInput`: the diagnostic-name, ACL and
  catalog inputs a comparison or type production needs.
- `Literal` / `ConstantValue` / `LiteralNull` / `Frame` / `Context`: the
  engine's literal node (its identity drives folding and stored SQL), the query
  frame quote-capture folding keys on, and bind-time cancellation.
- `AdmitSetReturning` / `TargetSet` / `RegexpSetPlacement`: set-returning
  placement and bookkeeping; the regexp form is a leak to unify.
- `CapturePolymorphicBody` / `JSONReturningType` / `ValidateSQLJSONDefault` /
  `SequenceArgument` / `SequenceTemporary`: statement-analysis state for
  SQL-bodied builtins, SQL/JSON RETURNING and DEFAULT shape, and sequence
  regclass input with prepared-literal capture and TEMP provenance.
  Scalar query functions and JSON_TABLE validate analyzed DEFAULT shapes through
  the same hook before RETURNING fallback coercion; relabels and I/O wrappers
  cannot confer function-expression status on a bare parameter.

`AggregateBinder` adds what is valid only while a `parser.AggregateExpr` binds,
where the engine owns grouping state: `AnalyzeAggregateFilter`,
`FinishAggregate` (ORDER BY/DISTINCT, window nesting, registration, occurrence
node), `DomainTypeName`, `CollationName`, `CommonArgumentUsage` and
`CollationRelabel`.

Prefer passing values to adding methods. A new method needs a reason here:
either a genuine analyzer capability or a leak that a later step removes.

## Evaluation capabilities

A family whose evaluation reads subsystem state gets a narrow capability
interface here (`ACLRuntime`, `TransactionInquiryRuntime`,
`CatalogInquiryRuntime`, `SessionInquiryRuntime`, `AdvisoryRuntime`,
`BlockingRuntime`, `SequenceRuntime`, `LargeObjectRuntime`,
`PgrowlocksRuntime`, `FTSQueryRuntime`). The node asserts it from `expr.Context`; `*exec.Context`
implements it with a compile-time assertion and forwards to the root statement
host, which owns all locking and returns owned values.

- Nodes pass values (OIDs, names, typed values, descriptors, immutable
  provenance) and retain no Session, Engine, StatementScope, manager, ticket,
  callback or authority cache. A missing runtime or foreign owner is an
  ordinary Go error.
- Evaluation order is part of each family's contract (error priority and
  reached child effects): which children evaluate, when strict NULL applies and
  when the runtime is first demanded.
- Notification inquiries use `SessionInquiryRuntime` for committed physical
  subscriptions and the cluster queue fraction. The zero-child recipes retain
  neither snapshots nor authority. Listening opens an invocation-owned typed
  vector (including empty TEXT sources); queue usage is volatile and scalar.
  Both are nonfoldable and reuse ordinary callable identities and source gates.

## Aggregate and window registries

- `aggregateDefinitions` (`aggregate_registry.go`) is matched exactly by
  `LookupAggregate`; `windowFunctions` (`window_function_bind.go`) by
  `LookupWindowFunction`. Owned ordinary aggregates keep their catalog
  declaration; the static table is not authority for a same-spelled owned
  routine.
- `Bind` analyzes arguments, then FILTER, resolves the signature, selects the
  provider and calls `FinishAggregate` with an `AggregateCall`. Providers are
  immutable, receive the ordered argument vector in `Advance`, and keep group
  state execution-local. Ordered-set and hypothetical providers sit behind
  `OrderedAggregate` and read the executor's sorted `OrderedInput`.
  `ExecutionAggregateProvider` borrows the current `expr.Context` for both
  initialization and finalization; executors must use the contextual final entry
  whenever that provider implements it.
- Providers a view may store implement `RemappableProvider`
  (`aggregate_remap.go`); without it view creation fails with "unpersistable
  view aggregate".

## Adding or porting a family

1. In `analyze`, turn `func (b *Binder) bindX(...)` into
   `func bindX(b functions.Binder, ...)`; the compiler proves the interface is
   enough. Record any new `Binder` method above.
2. Move it here, add its `Definition`, and delete its engine branch and glue.
   Unit tests move with it unless they exercise engine switches.
3. A family whose engine branch sat after the chain's shared gates sets
   `SharedGates` (plus `ExactSpelling` if it sat in the final switch,
   `OwnsLabels` if the label gate exempted it), because `Lookup` runs first.
   Check that no earlier chain predicate claims its names.
4. Paths that avoid composite-recovery notes (regexp, string variadics) use
   `UnnotedResolutionError` for genuine callable absence, not `SignatureError`.
5. An installed extension family resolves through `InstalledRoutine`. The
   wrapper node and its validation stay in the engine, with explicit wrapper
   cases ahead of protocol branches.
6. Capture-admitted nodes implement `expr.Capturable` (fixed-storage nodes the
   value-receiver `expr.IndexedCapturable`, mapping written slots directly with
   checked bounds and no retained slices). Hooks never evaluate, render,
   rebuild, rebind or mutate.
7. Implement the node protocol below and add registry, node and leaf units.

## Node protocol

Every function node implements `expr.Composite` beside its definition (helpers
in `composite.go`): `Children` (lexical children in evaluation order),
`Rebuild`, `Substitute`, `IntrinsicVolatility`, `Render` and `SameGroup`, plus
each optional `expr` hook whose default does not describe it (the `expr` README
defines them).

- `Rebuild` checks the child count and revalidates through `Reconstruct`,
  keeping inputs derived at binding (input collation, retained descriptors,
  text and clock usages). Engine wrappers that embed a node override it to keep
  their routine identity.
- `Substitute` swaps children without rebinding the provider; `SQLJSON` and
  `JSONCreate` rebuild instead.
- `SameGroup` compares the node's own identity (kind, plan, written-slot
  provenance), never presentation.
- `ResultNullability` answers from the immutable recipe only, never evaluating
  arguments or reading authority.
- `Render` returns one `expr.Rendering` built with `render.go`'s builders.
  Extension carrier nodes have no `Render`; their engine wrappers display with
  the installed schema.

## What stays in the engine

The analyzer behind `Binder` and unregistered bind code (`analyze`); name,
catalog, ACL and dependency resolution; descriptor generations and retag/remap;
stored SQL presentation (`stored`); installed-routine wrappers (`plan`) and
manifests (`extension`); query/set expansion, grouping, windows and aggregate
state lifetime (`analyze`, `exec`); session RNG, UUID clocks and SQL time
(`session`); and the runtime bodies behind the capabilities (root
`*_runtime.go`). Catalog and session inquiry owners here hold binding,
properties, grouping and render/replay recipes, which are never mutated once
published; rendering, catalog locks and serial ownership stay in the engine.

### Transaction inquiry ownership and demand

`transaction_identity` and `commit_timestamp` nodes, binders, source operations
and protocol hooks live here; `Substitute` keeps the selected provider without
re-resolution, and no planning hook is inferred from execution strictness.
Display and stored replay share rendering descriptors. Allocation, history,
publication and lock-manager tickets stay in the engine.

## Family notes

- **Text-search query utilities**: `FTSQuery` binds owner-checked INT4, TEXT
  and TSQUERY results. Inspection and direct rewriting are immutable;
  SQL-based rewriting is intrinsically volatile and only demands
  `FTSQueryRuntime` after strict-NULL and empty-query bypass. Stored nodes keep
  typed operands and routine identity, never SQL rows or a statement host.
- **Text-search vector utilities**: `FTSVector` owns checked immutable scalar
  operations and strict typed operands. The string family's `FinishString`
  routes an already analyzed typed vector `length` operand to this provider;
  unknown and other typed length operands retain their existing owners.
- **Cube spatial** (`cube_spatial.go`, `cube_spatial_bind.go`): `CubeSpatial`
  owns two coerced operands and a checked `sqltype.CubeSpatialPlan`.
  `InstalledCubeRoutine.CubeType` exposes the selected installation's descriptor
  to the shared binder; discovery preserves namespace/signature precedence.
  The `plan` wrapper, not the kernel, owns installed callable admission.

- **ACL**: aclitem algorithms keep their own copy of the `acldefault` masks
  (this package cannot import `authorization`).
- **Regexp**: `RegexpCall` binds a `sqltype.RegexpPlan` and shares the
  statement-local `PGRegexExecution` (no global regex cache). The query binder
  lifts set calls into stages, so `RegexpSet.Eval` is an invariant failure.
  An optional `CitextRegexpCapture` preserves opaque selected-routine identity,
  reference/member provenance and descriptive naming without importing catalog
  ownership. Scalar/set scheduling and kernel hooks stay shared; `analyze`,
  `stored` and `exec` validate actual declarations, generations and current ACLs.
  Captured SQL bodies retain their declaration-default collation independently
  of outer metadata and inline error provenance. After shared constant/parameter
  evaluation declines, eligible nonconstant no-flags scalar bodies retain inline
  diagnostics in projection, WHERE and stored consumers; flags and set wrappers
  retain invocation Context. Diagnostic naming borrows the current validated
  declaration through `CitextRegexpRuntime`, never the kernel symbol or stale
  captured public spelling. Scalar NULL preflight uses the shared typed-frame
  proof and regex reset hook; set initialization still demands routine admission.
  Unchanged replacements borrow the original bytes but construct the bound TEXT
  result descriptor; the input's installed CITEXT identity never becomes a
  TEXT-typed literal's datum identity.
- **pgcrypto**: `sqltype` owns the plans and kernels. Plans retain no password,
  salt, entropy, key or IV.
- **MAC operations**: `trunc` keeps one registry owner in `math_bind.go`;
  exact one-argument typed MACADDR/MACADDR8 calls delegate to `MACUnary` using
  the already analyzed child. Numeric/unknown selection stays with the existing
  math owner, and numeric defaults remain excluded. Resolved numeric trunc
  also rejects decorations admitted by the shared ordinary scalar grammar.
  Only `macaddr8_set7bit` adds a registry name. Its function-owned six-to-eight
  expansion retains source829 while selecting sole formal/result774 and
  routine4125, not a general cast. Helpers use genuine NetworkRoutineProvider
  declarations; symbolic MAC operators use OperatorRoutineProvider.
  Rebuild/retag binds fresh source/result descriptors. Stored validation
  checks the selected operation, provider and routine identity; rendering emits
  the original typed source in `pg_catalog.macaddr8_set7bit`, without a
  cross-width CAST. Catalog lifetime/provenance remains with existing admission
  and stored-renderer owners. See [kernels](../../sqltype/README.md#mac-address-operations)
  and [public proof](../../../reference/geometry_network/README.md#mac-address-operations).
- **cube**: `cubeFunctionNames` is the single name inventory for registry dispatch
  and analyzer family recognition; registry folding does not bypass exact
  installed resolution. `bindCube` consumes selected/coerced operands from the
  shared analyzer callable selector; exact/preferred/unknown ranking and type-name
  coercion remain shared. Explicit operation/formal selection includes int4
  coordinate indices, INT4[] subset selectors, FLOAT8 enlargement radii and
  FLOAT8 extraction results.
  `CubeRoutine` exposes the resolved installation's cube descriptor without a
  type-name lookup in `Binder`. `Cube` invokes sqltype's immutable constructor,
  inquiry, coordinate and transformation kernels; strict evaluation finishes
  all children first.
  `plan.InstalledCubeExpr` preserves procedure authority through
  rebuild, retag, substitution, grouping and stored rendering.
- **earthdistance**: `EarthRoutine` exposes the genuine installed domain to
  `bindEarthCoordinate`; shared callable selection owns coercion and shadows.
  `EarthCoordinate` evaluates a captured typed plan, then attaches SQL-body
  diagnostic context only to failures reached inside that plan. On that error
  path, `EarthCoordinateRuntime` resolves the checked current routine name;
  immutable operation selection never follows mutable presentation. Argument
  and coercion errors keep their own origin. Successful calls perform no
  diagnostic-name lookup. `InstalledEarthExpr` retains callable authority
  through reconstruction and catalog replacement.
  `EarthPoint` separately owns two POINT operands and a checked mile-distance
  plan, without domain/cube generations or SQL-body error wrapping.
  `InstalledEarthPointExpr` retains its selected procedure through the same
  reconstruction and lifecycle seams; no callable exists without installation.
- **unaccent**: `unaccent.rules` (PostgreSQL REL_18_0) is embedded and compiled
  once into `StockUnaccent`, used by `extension.UnaccentProvider`.
- **dmetaphone licence**: Single Metaphone retains its complete release
  PGDG/Schwern/Philips provenance. The Double Metaphone files retain the complete
  Maurice Aubrey/Text::DoubleMetaphone and North Carolina State Highway Patrol
  notices. The Perl original Artistic alternative and full terms are in
  `dmetaphone_LICENSE.txt`; modified source is Freely Available under clause
  3(a). Binary redistributors should accompany it with machine-readable
  modified source under clause 4(b), or satisfy another applicable license
  condition. This does not relicense the distinct NC permission or imply
  copyright-holder endorsement.
- **Geometry measurements**: `GeometryMeasure` retains fixed operands and selected
  routine/formal/result authority for eighteen signatures, with radius remaining
  in `Circle`. Binding probes every formal without evaluation and coerces only
  the winner. Shared-name `length` stays string-owned and sends analyzed PATH/LSEG
  operands through `FinishGeometryMeasure`, preserving text/binary/unknown
  selection. Matching scalar VARIADIC syntax does not declare a variadic overload;
  scalar decorations are rejected after viable selection. Rebuild, substitution,
  grouping, capture and stored validation retain the selected provider.
  Provider rendering uses the established literal transport; captured query AST
  literals instead pass through `regobj.ConstantLiteral`, whose explicit LSEG and
  POLYGON annotations preserve geometric length and point-count overloads on
  logical replay.

## Testing

Leaf units of nodes, kernels, providers and the registry live here; units that
bind through the real analyzer, descriptors, publication, grouping/windows or
resource cleanup stay with their engine owner (root, `analyze`, `exec`). Public
SQL behavior is tested only in `reference/`
([docs/TESTING.md](../../../docs/TESTING.md)).

- Unit fakes embed `expr.Info` (arguments) or the nil `unitCapabilities`
  (contexts), so they implement only what the code under test calls; any other
  call panics.
- Allocation checks construct caller-owned scratch before measuring.
- Frame names matched by tests: `datetimeSleepWaitRemaining`
  (`reference/datetime` sleep cancellation). Renaming or moving it makes that
  observer wait on a runaway query; update the probe with it.

```sh
make check RUN='^Test(LookupClaimsRegisteredNames|DefinitionsAreWellFormed|AggregateDefinitionsAreWellFormed|Lookup(Aggregate|WindowFunction)ClaimsRegisteredNames)$'  # registries
make check RUN='^Test(AggregateProviderRemap|(Sum|Avg|Count|Extrema|AnyValue|MoneySum|IntervalSum|IntervalAvg|StringAggregate|BooleanAggregate|RangeAggregate)Provider|(Bitwise|Statistical|Array)Aggregate)'  # aggregate providers
make check RUN='^Test(Composite(Arity|Counted|Leaf|Named|Providerless|Retained|Slice|String|Timezone)|IntrinsicVolatilityExcludesChildren|PlanningHooks|RetagReportsDerivedInputChanges|GroupIdentity|GroupLeaf|SubstituteKeepsTheProvider|FunctionSyntax|ClosureTransparency|Render|CaptureOperands|ResultNullability)'  # node protocol and hooks
make check RUN='^Test(CatalogInquiry|TransactionInquiry|AdvisoryNode|BlockingInquiry|SequenceCall)'  # runtime-capability nodes
make check RUN='^Test(ACLValues|ACLNodes|ACLValueEvaluates|ObjectACLEvaluates|AccessInquiryEvaluates)'  # ACL functions
make check RUN='^Test(FTSOps|Point|Circle|Polygon|GeometryAlignment|GeometryRelation|GeometryConversion|GeometryMeasureBoxLine|MACADDR8Comparison|NetworkSubnet)Provider'  # operator providers
make check RUN='^TestGeometryPositionProviderLifecycle$'  # position provider authority
make check RUN='^TestFTSQueryFunctions'  # query utility providers, kernels and runtime lifetime
make check RUN='^TestGeometryMeasure(GroupIdentity|ProviderLifecycle)$'  # measurement providers
make check RUN='^TestMACOperationsProviderLifecycle$'  # fixed-width MAC function protocol
```

For any other family, select its prefix alone (for example `^TestPgcrypto`,
`^TestUUIDExtract`, `^TestLevenshtein`). Selectors run module-wide, so a family
prefix also selects that family's engine-integrated units.
