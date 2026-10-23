# internal/engine/functions

Builtin functions: signatures and argument rules, immutable call nodes,
evaluation, aggregate and window providers with their state algorithms, and,
family by family, the bind code and its registry entry. The analyzer that
drives binding is in [`analyze`](../analyze/README.md), stored presentation in
[`stored`](../stored/README.md), execution in [`exec`](../exec/README.md), and
the authority behind evaluation capabilities in the root
[engine](../README.md#function-runtimes).

## Imports and layering

- A leaf library: it imports only [`expr`](../expr/README.md), `ids`, `sqltype`,
  `parser` and `sqlstate`, never the root engine or the catalog, storage, lock
  or visibility state packages.
- It reaches the engine only through `Binder` at bind time and through
  `expr.Context` plus the capability interfaces recovered from it at evaluation
  time. `expr.Context` does not grow per family.
- `expr` owns `RoutineReference`, `RoutineRecipeReference`, `RandomOwner`,
  `IntrinsicPropertyRule` and the `RoutineProvider` representation;
  `routine_provider.go` aliases the type (so catalog declarations need not
  import this package) and owns the family constants. There are no other
  aliases.
- Nodes are plain types over the `expr` contract: they embed `expr.Info`, hold
  children as `expr.Expr` and evaluate with `Eval(expr.Context)`.
  `StringVariadic.Origin` is an opaque value the engine stores and reads.
- The package is catalog-free: access-method property inquiries use the
  catalog-snapshot runtime, the engine resolves the current method, and missing
  or TABLE methods return NULL; there is no static OID fallback.

## Registry

`registry.go` holds the static `definitions` table; there is no `init()`
registration. `Lookup(name)` claims a call by its unqualified name,
case-insensitively unless `ExactSpelling`. The analyzer's
`BindFunctionProvider` (`analyze/function.go`) runs SQL-overload competition
first, then consults `Lookup` before its remaining name chain, so a registered
name must not overlap any family the chain still dispatches. `Bind` owns
exact-spelling resolution, visibility, argument analysis and every diagnostic.

`Definition` fields:

- `ExactSpelling`: claim only the exact parser-folded names; other spellings
  (a quoted `"ABS"`) keep the chain's missing-function path.
- `SharedGates`: the analyzer applies the shared gates before `Bind`: argument
  labels get the missing-signature error; `ResolutionOnly` calls and other
  spellings of a case-insensitively claimed name (`"FORMAT"`) get the format
  diagnostics. `Bind` then checks builtin visibility itself.
- `OwnsLabels` (requires `SharedGates`): `Bind` owns every label diagnostic
  (`uuidv7`'s `shift`, `make_*`, advisory locks).
- `OwnsSpelling` (requires `SharedGates`): namespace inquiries keep child
  transformation, builtin visibility, then their own exact-spelling diagnostic.
  Labels and `ResolutionOnly` still pass the shared gates.
- `Claims`: the chain's admission predicate, replacing `Names` matching with its
  own exact, folded or lowercased comparison; `Names` still lists every claimed
  name, and the registry tests check that each satisfies `Claims`.
- `ProbeCall`, `ResultCall`, `NamedCall`: pure applicability hooks for
  SQL-overload competition (below).

Sequence calls are not registered: `nextval`/`currval`/`setval`/`lastval` have
their own `parser.CallExpr` syntax, and expression dispatch calls
`BindSequenceCall` directly, without shared gates, labels or overload
competition. Blocking inquiries have no shared gates: all children and the
argument limit precede visibility and signature checks.

| Family | Bind file(s) | Fields |
| --- | --- | --- |
| Math | `math_bind.go` | `ExactSpelling`, `SharedGates` |
| Datetime; `make_*` | `datetime_bind.go` | `ExactSpelling`, `SharedGates`; `make_*` adds `OwnsLabels` |
| Scalar strings, `string_to_table`, `like_escape`, `to_number` | `string_bind.go`, `like_escape_bind.go`, `numeric_formatting_bind.go` | `ExactSpelling`, `SharedGates` |
| `parse_ident`, `lower`/`upper` | `string_bind.go` | none |
| Variadic strings | `string_variadic_bind.go` | `Claims` |
| `regexp_*` | `regexp_bind.go` | `ExactSpelling` (checks its own labels) |
| Binary data | `binary_bind.go` | `ExactSpelling`, `SharedGates` |
| `format` | `format_bind.go` | `SharedGates` |
| JSON/JSONB calls; `json_object`/`jsonb_object` | `json_call_bind.go` | `Claims`; `ExactSpelling`, `SharedGates` |
| Arrays, range inspection | `array_{inspect,random,transform,search}_bind.go`, `range_inspect_bind.go` | none |
| `uuidv4`/`uuidv7`; UUID extraction; uuid-ossp | `uuid_generation_bind.go`, `uuid_extract_bind.go`, `uuid_ossp_bind.go` | generation: `ExactSpelling`, `SharedGates` (`uuidv7` adds `OwnsLabels`) |
| Random, sleep | `random_bind.go`, `datetime_sleep_bind.go` | `ExactSpelling`, `SharedGates` |
| Clocks | `transaction_time_bind.go` | none |
| FTS, geometry, MACADDR8, network subnet, index support, large objects | `fts_bind.go`, `geometry_*_bind.go`, `{circle,point,polygon}_bind.go`, `line.go`, `macaddr8.go`, `network_subnet_bind.go`, `index_support_bind.go`, `largeobject.go` | `ExactSpelling`, `SharedGates` |
| Deparse, comments, visibility, index properties, `current_schemas` | `deparse_function.go`, `comment_inquiry.go`, `visibility_inquiry.go`, `index_property.go`, `current_schemas.go` | `ExactSpelling`, `SharedGates` |
| Identity; namespace inquiries | `session_identity.go`, `namespace_inquiry.go` | `Claims`, `SharedGates`; namespace adds `OwnsSpelling` |
| Object address, transaction identity, commit timestamps | `object_address.go`, `transaction_identity_bind.go`, `commit_timestamp_bind.go` | parser `Claims` |
| Username/FK catalog, tablespaces, partitions | `catalog_relations.go`, `tablespace_info.go`, `partition_info.go` | none |
| ACL values, `pg_get_acl`, access inquiries | `acl_value_bind.go`, `object_acl_bind.go`, `access_inquiry_bind.go` | none |
| Advisory locks; blocking inquiries | `advisory.go`; `blocking_inquiry.go` | `SharedGates`, `OwnsLabels`; none |
| intagg, pgrowlocks, pgcrypto, fuzzystrmatch, unaccent/`daitch_mokotoff` | `intagg_bind.go`, `pgrowlocks.go`, `pgcrypto_bind.go`, `fuzzystrmatch_bind.go`, `unaccent_bind.go` | none |

Entry points dispatched by syntax or reached from engine glue rather than by
name: `BindDatetimeOverlaps` (`overlaps`), `BindSimilarEscape`,
`BindCurrentDateTime` (SQL value functions; the precision warning is returned
as a value), `BindTimeZone`, `BindPosix`/`BindSimilar`, `StringNode` (`||`,
`^@`), `BindIdentity` (`parser.IdentityExpr`), `BindSettingCall`
(`current_setting`/`set_config`), `AnalyzeIntaggArguments`/`ResolveIntagg`
(intagg as an aggregate), `FinishArrayTransform`, `ArraySearchNode`,
`FinishRangeInspect`, `BindUUIDGeneration` (pgcrypto's `gen_random_uuid` when
`pg_catalog` wins), `BindBinary`/`FinishBinary`, `BindACLContains`, and:

- JSON operators, from the operator binder: `BindJSONExtraction`,
  `BindJSONBExistence`/`BindJSONBExistenceOperands`, `BindJSONBPathDelete`,
  `SelectJSONBConcat`, `SelectJSONBDelete`, `BindJSONBContainment` and, after
  catalog overload selection, `BindJSONBPathOperator`. They build `Binary`
  nodes with the operator's routine identity.
- SQL/JSON syntax: `BindSQLJSON` (constructors; the engine applies the returned
  RETURNING coercion), `SQLJSONInput` (FORMAT JSON via `JSONFormattedArgument`),
  `SQLJSONReturningType`, `BindJSONPredicate` (`IS JSON`) and
  `BindSQLJSONQuery` (query functions, outside the registry).

Shared helpers: `call_syntax.go` (`ValidateCallSyntax`: FILTER analysis, the
100-argument limit and labels; `CallLabelError`; `CallDecorationError`;
`NamedArgumentType`; `ArgumentTypeNames`), `resolution_error.go`
(`ResolutionError`, `UnnotedResolutionError`, `DirectResolutionError`,
`NotedResolutionError`, `ArrayTypeName`, `DiagnosticNameOrUnknown`) and
`operator_error.go` (`OperatorError`, `ComparisonError`, `ArrayOperatorError`).

## Callable competition with SQL overloads

- The analyzer's shared selector keeps builtin and installed providers in the
  same namespace/signature competition as SQL declarations. Builtin metadata
  supplies omitted trailing defaults and variadic expansion; expanded declared
  formals stay the ranking inputs. Polymorphic substitution is never an exact
  concrete match.
- `ProbeCall` (a `CallShape`) receives already analyzed arguments plus separate
  declared-input and resolved-target vectors, and a `CallTypeContext` with type,
  array-association and common-type authority and an effect-free `CanCoerce`.
  It reuses the provider's pure target-selection helpers (`compatibleArrayTargets`,
  `arrayInputTarget`, ...): no speculative Bind, parameter inference, value
  conversion, dependency capture, provider construction or Eval. Unknown
  operands stay deferred until one winner reaches its binder.
- `NamedCall` maps labels to slots (`namedSlots`) with the provider's own roster
  and the same `CanCoerce` authority; ranking inputs and applicability targets
  stay separate and in written order. No hook binds, coerces, evaluates or
  captures a losing call.
- `OriginalArgument` recovers syntax through the private slot carrying an
  already bound operand, never by matching source keys; `Argument.Raw` exposes
  that recipe.
- `SelectedCallArguments` (known-node coercion reuse) lasts through provider
  binding; `selectedCallReentry` is consumed once by the immediate gateway and
  does not disable selection in unanalyzed FILTER or aggregate ORDER BY
  children. Nested winner frames restore both.
- Resolution failure or ambiguity never triggers an unconditional builtin
  fallback, and a common-resolver miss is never retried through a provider.

## Closed source providers

- Source-capable nodes implement `expr.Source` and `SourceOpening` beside their
  family (`*_source.go`); there is no central source registry or cursor tag.
  Admission, set cardinality, OUT labels and record layout are separate
  questions, and admission is per family (for example `String` admits only
  `string_to_table`), not a scalar-FROM fallback.
- Arguments evaluate once per open in native order from owner fields. Scalar
  NULL is one present row; EOF is typed NULL with false presence. Each demanded
  invocation has independent state that close, EOF, error and cancellation
  release without invalidating emitted values. JSONB record/path and regexp
  sets convert their complete invocation privately before publishing a row.
- `JSONTable` is a composite source carrier, not a registered builtin:
  immutable levels hold descriptor/child slots and `OpenSource` owns all row
  state. `Binder.ValidateSQLJSONDefault` checks enclosing shape, then column
  references, before RETURNING fallback coercion.
- Occurrence hooks keep each family's exact identity, not structural equality
  (`RegexpSet` compares concrete family only after the engine's set/position
  guards). Equivalent-child substitution preserves selected providers and
  never calls `Reconstruct`. Query scheduling stays in the engine.

## Binder

`Binder` (`binder.go`) is what bind code may ask of the analyzer;
`analyze.Binder` implements it (`analyze/function_binder.go`). Each method is a
reviewed decision:

- `AnalyzeArgument` / `CoerceArgument`: occurrence-owned analysis and formal
  coercion, selected by intent (intents are values, not methods).
- `CanCoerce`: effect-free catalog-aware coercion feasibility for call shapes.
- `Type`: the binding catalog's builtin descriptor (identity comparisons and
  `Usage()` need the descriptor).
- `Info`: node metadata; records type and collation dependencies.
- `BuiltinVisible` / `BuiltinOperatorVisible`: whether function or operator
  lookup reaches `pg_catalog` under the current `search_path`.
- `BuiltinRoutineInfo` / `BuiltinRoutineReference` /
  `BuiltinOperatorRoutineInfo`: the selected builtin routine identity, keyed by
  `RoutineProvider`, name and formals (catalog state).
- `DiagnosticTypeName`: diagnostic spelling, qualifying user-defined types.
- `SignatureError`: the 42883 for families whose composite-field recovery notes
  the missing signature.
- `RecordUnresolvedCallable`: records only an explicitly missing callable's
  identity; deferral needs that identity to be the terminal error, never its
  SQLSTATE alone.
- `CallSignatureError`: the analyzer's whole-call 42883 (re-analysis for names,
  composite-candidate 0A000, the note); a copy here would fork diagnostic routing.
- `AnalyzeFilter`: analyzes and discards a scalar call's FILTER so its errors
  precede the decoration rejection.
- `DefaultConsumer`: whether a column DEFAULT is being bound (analyzer context;
  families outside the admitted DEFAULT set reject themselves with 0A000).
- `ArrayType`: an element's array type from the binding catalog or definition
  stage.
- `CommonUsage`: the nonthrowing common-type probe shared with CASE, VALUES and
  IN; a plain function once its usage-only core moves to `sqltype`.
- `ComparisonInfo` / `CollatedInfo`: comparison input and result collation with
  the engine's conflict diagnostics and dependency recording.
- `RetainUntypedParameter`: keeps a still-untyped `$n` bound to an ANY formal
  pending (42P08 unless resolved elsewhere) and reports whether it was one.
- `NoteCatalogText` / `NoteCatalogSnapshot` / `NoteVisibilitySnapshot`:
  statement-level snapshot demand (catalog text at execution, analysis-only
  catalog demand, conversion visibility); none captures eagerly.
- `InstalledRoutine`: the routine an `Extension` installed on the function path
  with a signature, as an opaque handle (`installed_routine.go`): `Validate`
  checks the manifest, `Attach` completes identity and returns the engine's
  wrapper. Installation and procedure identity are engine state.
- `CaptureComparisonInput` / `CaptureTypeInput`: diagnostic-name, ACL and
  catalog-identifier inputs that comparing or producing a type needs.
- `Literal` / `ConstantValue` / `LiteralNull`: the engine's literal node, its
  value and the NULL-literal predicate; literal identity drives folding and
  stored SQL.
- `Context`: the statement's cancellation context for bind-time conversion and
  template compilation.
- `AdmitSetReturning` / `TargetSet`: set-returning placement plus target-set
  bookkeeping, and the bookkeeping alone.
- `RegexpSetPlacement`: the regexp family's own set placement (a leak to unify
  with `AdmitSetReturning` once query analysis has one owner).
- `Frame`: the query frame identity that quote-capture folding keys on.
- `CapturePolymorphicBody`: the builtins a SQL-bodied builtin selects from its
  actual argument.
- `JSONReturningType` / `ValidateSQLJSONDefault`: SQL/JSON RETURNING type input
  and DEFAULT shape validation (statement type input and bound shape).
- `SequenceArgument` / `SequenceTemporary`: a sequence call's regclass provider
  input with prepared sequence-literal capture, and its TEMP provenance; DEFAULT
  validation ends call-site capture (statement-analysis state).

`AggregateBinder` adds what is valid only while a `parser.AggregateExpr` binds,
where the engine owns grouping state: `AnalyzeAggregateFilter` (the kept
FILTER), `FinishAggregate` (ORDER BY/DISTINCT, window nesting, merging,
registration, occurrence node), `DomainTypeName` (domain spelling through the
type path, propagating lookup errors), `CollationName`, `CommonArgumentUsage`
(labeled 42804 common type) and `CollationRelabel` (metadata-only relabel).
The INSERT ... VALUES namespace is `AggregateDefinition.ValuesNamespace`, not a
method. Argument intents (`ThresholdArrayArgument`, `ZoneArgument`,
`RecordBaseArgument`, `RecordFormalArgument`, `BareProjectionArgument`,
`AssignmentFormalArgument`, `JSONFormattedArgument`, `TypeNameCastArgument`,
`BareFormalArgument`, ...) are documented in `arguments.go`.

Prefer passing values to adding methods. A new method needs a reason here:
either a genuine analyzer capability or a leak that a later step removes.

## Evaluation capabilities

A family whose evaluation reads subsystem state gets a narrow capability
interface here. The node recovers it from `expr.Context` by type assertion;
`*exec.Context` implements it with a compile-time assertion and forwards to the
root statement host. Nodes pass values (OIDs, names, typed values, descriptors,
immutable provenance) and retain no Session, Engine, StatementScope, manager or
authority cache. A missing runtime or foreign owner is an ordinary Go error.

- `ACLRuntime`: role-store ACL identity, `pg_get_acl`'s raw read,
  `pg_has_role` and `row_security_active`; each call borrows the statement's
  authorization and snapshots under `Engine.mu`.
- `TransactionInquiryRuntime`: identity calls check the evaluation context
  before children, then strict NULL precedes scope demand. Timestamp calls have
  no eager prelude: unary errors/NULL and xid 0/1/2 handling precede runtime
  demand. Results are owned values built after unlocking.
- `CatalogInquiryRuntime` and `SessionInquiryRuntime`: finite username,
  object-address, visibility, index-property, comment, deparse, identity, TEMP
  namespace and schema-array operations.
- `AdvisoryRuntime` (evaluated keys, key space, action, mode, lifetime) and
  `BlockingRuntime` (kind and PID): no managers, scopes, tickets or callbacks
  cross into this package.
- `SequenceRuntime`: `SequenceAuthority` runs the evaluation prelude and
  requires the live scope before any argument is evaluated; `Sequence` takes one
  `SequenceRequest`. The node evaluates every argument once, left to right, and
  returns typed NULL without opening the sequence when any is NULL.
- `LargeObjectRuntime`: evaluated OID/offset/length/octets only; nodes evaluate
  arguments once left to right and apply strict NULL.
- `PgrowlocksRuntime`: the relation text and result descriptor; the host owns
  relation admission, MVCC capture and manager projection.

Per-family ordering is part of the contract (error priority and reached child
effects): object address evaluates all children then applies strict NULL;
visibility and index runtimes validate scope or resolve the relation before
decoding; username decodes its OID before role lookup; comments finish NULL
handling before lazy routine admission.

## Aggregate and window registries

- `aggregate_registry.go`'s `aggregateDefinitions` is matched exactly by
  `LookupAggregate`. Owned ordinary aggregates keep their catalog declaration and
  transition provider; the static table is not authority for a same-spelled
  owned routine.
- `Bind` analyzes arguments, then FILTER, resolves the signature, selects the
  provider and calls `FinishAggregate` with an `AggregateCall` (provider,
  operation identity, routine, ordered `Arguments`, ordered-set `Direct`
  arguments, stage width, `IncludeNulls`, `RetainUntypedArgument`, WITHIN GROUP
  `Order`). Every provider receives that ordered vector in `Advance`; providers
  are immutable and group state is execution-local. `BindReturning` replaces
  `Bind` for JSON aggregates; `Window` resolves the OVER form
  (`ResolveWindowAggregate`).
- Providers that a view may store implement `RemappableProvider`
  (`aggregate_remap.go`): `Remap` takes a `ProviderRemap` (type mapper, usage
  mapper, stored argument usage), rebuilds the provider and rebinds derived
  plans. Without it, view creation fails with "unpersistable view aggregate".
- `window_function_bind.go`'s `windowFunctions` (`LookupWindowFunction`)
  returns a `WindowSignature`; the rank family is in both tables. Its
  `ProbeCall` reuses `ResolveWindowValue` and `windowOffsetTargets`. A selected
  window identity returns to the qualified window binder with its OVER,
  modifier and nesting checks.
- Ordered-set and hypothetical providers sit behind `OrderedAggregate`; the
  executor lends sorted input as `OrderedInput`, and `AggregateOrdering` is
  shared with the engine's finish.

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
5. An installed extension family resolves through `InstalledRoutine` (add its
   `Extension` and the engine's handle). The wrapper node and its validation stay
   in the engine, with explicit wrapper cases ahead of protocol branches.
6. Capture-admitted nodes implement `expr.Capturable` beside the definition.
   Fixed-storage nodes implement value-receiver `expr.IndexedCapturable`, mapping
   `WrittenSlots`/written args directly with checked bounds and no reordered
   copies or retained slices; `Check` and `At` receive the original boxed node.
   SQL-standard calls keep parser argument order. Hooks never evaluate, render,
   rebuild, rebind or mutate.
7. Implement the node protocol below and add registry, node and leaf units here.

## Node protocol

Every function node implements `expr.Composite` beside its definition (helpers
in `composite.go`). `Children` lists lexical children in evaluation order.
`Rebuild` checks the child count and revalidates through `Reconstruct`, keeping
inputs derived at binding (input collation, retained descriptors, text and clock
usages, folded OVERLAPS ends, the math wrapper context). Fixed-arity nodes keep
argument slots beyond `Children`; provider-less nodes take only new metadata and
children. Engine wrappers that embed a node override `Rebuild` to keep their
routine identity.

A new node implements `Children`, `Rebuild`, `Substitute`,
`IntrinsicVolatility`, `Render` and `SameGroup`, plus each optional `expr` hook
whose default (fold as a constant, never strict, not a stored provider, rebuild
on change, preflight by simplification, parenthesized index key, not looked
through by DEFAULT closure) does not describe it. The `expr` README defines the
hooks; notes for this package:

- `Substitute` swaps children without rebinding the provider; `SQLJSON` and
  `JSONCreate` rebuild instead.
- `SameGroup` compares the node's own identity (kind, plan, written-slot
  provenance); presentation (TRIM spelling, `pg_catalog` qualification,
  redundant VARIADIC) never counts.
- `IntrinsicVolatility`: catalog/session inquiry owners return retained analyzed
  volatility; `PlanningFold` is `FoldNever` for stage-owned and
  never-evaluated calls.
- `ResultNullability` answers from the immutable recipe only, never evaluating
  arguments or reading authority; a declined claim keeps both descriptor fields.
- `Render` returns one `expr.Rendering` (tree header and inputs plus the SQL
  shape: leaf, call, infix or keyword template) using `render.go`'s builders.
  SQL/JSON constructors share `SQLJSONPieces` between bound and raw stored
  presentation. Extension carrier nodes have no `Render`; their engine wrappers
  display with the installed schema.
- `SequenceCall`, `Advisory` and `BlockingInquiry` rebuild with mapped usages
  while keeping their catalog facts (provider `Ref`, TEMP provenance, action,
  mode, kind); engine catalog consumers keep explicit `SequenceCall` cases.

## What stays in the engine

The analyzer behind `Binder` and unregistered bind code (`analyze`), name,
catalog, ACL and dependency resolution, descriptor generations, retag/remap,
stored SQL presentation (`stored`), installed-routine wrapper nodes (`plan`)
and manifests (`extension`), query/set expansion, physical regex execution
ownership, grouping, windows and aggregate state lifetime (`analyze`, `exec`),
physical-session RNG, UUID clocks and SQL time (`session`), and the runtime
bodies behind the capabilities (root `*_runtime.go`). There is no alternate
binder, dynamic plugin registration or duplicated type algorithm here.

### Catalog inquiry and session identity

The immutable catalog/session owners live here (CatalogUserName,
CatalogForeignKeys, TablespaceLocation, TablespaceDatabases, the partition
inquiries, ObjectAddress, NamespaceInquiry, CurrentSchemas, VisibilityInquiry,
IndexProperty, CommentInquiry, Deparse, Identity) with their binding,
properties, grouping, substitution, retagging and render/replay recipes.
Published recipes are never mutated. Actual rendering, tree lookup, catalog
locks and serial ownership stay in the engine.

### Transaction inquiry ownership and demand

`transaction_identity` and `commit_timestamp` nodes, binders, source operations
and protocol hooks live here. `Rebuild` validates; `Substitute` keeps the
selected provider and metadata without re-resolution. Members keep nonreflexive
source occurrence. No strict-planning fold, result-nullability, group-leaf or
closure hook is inferred from execution strictness. Display and stored replay
share rendering descriptors, with provider validation deferred to replay.
Allocation, history, publication and lock-manager tickets stay in the engine.

## Family notes

- **ACL** (`acl_value.go`, `object_acl.go`, `access_inquiry.go`,
  `acl_items.go`): aclitem algorithms over raw bits with their own copy of the
  `acldefault` masks (this package cannot import `authorization`), polling
  cancellation every 256 items. Only `ACLValue` is a stored generation provider;
  `ACLValue.ClosedDefault` inspects inline args by kind arity, and `aclexplode`
  is never closed.
- **Regexp**: `RegexpCall` binds a `sqltype.RegexpPlan` (concrete usages,
  collation, formal roles) and evaluates children in written order
  (`WrittenSlots`), so named calls deparse correctly without changing grouping
  identity. Every supplied child evaluates before strict NULL suppresses
  provider work. Evaluation shares the statement-local `PGRegexExecution`
  (`FunctionRegexExecution`); there is no global regex registry or
  prepared-program cache. `RegexpSet.Eval` is an invariant failure: the query
  binder lifts set calls into stages.
- **JSON/JSONB**: `bindJSONCall` replays the chain's branch order; slot calls
  share `bindSlots` (`json_slot_bind.go`). `SQLJSONQuery` keeps result
  conversion inside ON ERROR and evaluates only the selected behavior DEFAULT.
- **pgcrypto**: `PgcryptoMembers` is the installed manifest as data; `sqltype`
  owns signature/result plans and cipher/digest kernels. Plans retain no
  password, salt, entropy, key or IV; volatile calls request entropy once,
  after demanded children, strict NULL and argument validation.
- **fuzzystrmatch, unaccent, Daitch–Mokotoff**: kernels use fixed local
  buffers, borrow input and poll cancellation on long scans. `unaccent.rules`
  (PostgreSQL REL_18_0) is embedded and compiled once into `StockUnaccent`,
  used by `extension.UnaccentProvider`. `InstalledText` is not a protocol node;
  its engine wrapper keeps its own switch cases.
- **dmetaphone licence**: Single Metaphone retains its complete release
  PGDG/Schwern/Philips provenance. The Double Metaphone files retain the complete
  Maurice Aubrey/Text::DoubleMetaphone and North Carolina State Highway Patrol
  notices. The Perl original Artistic alternative and full terms are in
  `dmetaphone_LICENSE.txt`; modified source is Freely Available under clause
  3(a). Binary redistributors should accompany it with machine-readable
  modified source under clause 4(b), or satisfy another applicable license
  condition. This does not relicense the distinct NC permission or imply
  copyright-holder endorsement.
- **UUID**: `UUIDOsspMembers` supplies uuid-ossp signatures and owner recipes;
  V1/V1mc use the physical-session allocator through `FunctionUUIDV1` only on
  demand. `UUIDExtract` is one strict unary carrier, reconstructed with fresh
  usages on retag/remap.
- **FTS** (`FTS`): a fixed three-slot operand array and a finite
  `sqltype.FTSPlan`; constant invalid phrase distances fail at Execute
  preflight through the strict immutable hook.
- **POINT, Circle, Polygon**: fixed operands with selected routine authority.
  `<>`/`!=` and circle area comparisons stay `plan.ComparisonExpr` with
  `PointPlan`/`CirclePlan` payloads; circle sameness and vertical predicates
  select their catalog signature before provider dispatch.
- **Geometry conversion and measurement**: operands are analyzed once,
  coercions probed without evaluating, and only the winning formal signature
  coerced. Typename calls keep their precedence through the explicit plan's
  `FunctionCastShortcut()`; stored calls transport explicitly typed literals so
  replay cannot pick another overload.
- **Network subnet, MACADDR8**: BOOL providers attached to the same
  `OperatorRoutineProvider` declarations as their operators;
  `MACADDR8Comparison` reuses the scalar `ComparisonPredicate`.

## Testing

Leaf units of nodes, kernels, providers and the registry live here; units that
bind through the real analyzer, descriptors, publication, native controls,
grouping/windows or resource cleanup stay with their engine owner (root,
`analyze`, `exec`). Public SQL behavior is tested only in `reference/`
([docs/TESTING.md](../../../docs/TESTING.md)).

- Unit fakes implement the plain contract: fake arguments embed `expr.Info`, and
  fake contexts embed the nil `unitCapabilities` (`expr.Context`), so they
  implement only what the code under test calls; any other call panics.
- Allocation checks construct caller-owned scratch (for example the aggregate
  argument vector) before measuring.
- Frame names matched by tests: `datetimeSleepWaitRemaining`
  (`reference/datetime` sleep cancellation). Renaming or moving it makes that
  observer wait on a runaway query; update the probe with it.

```sh
make check RUN='^(TestLookupClaimsRegisteredNames|TestDefinitionsAreWellFormed)$'  # registry
make check RUN='^Test(AggregateDefinitionsAreWellFormed|Lookup(Aggregate|WindowFunction)ClaimsRegisteredNames)$'  # aggregate/window registries
make check RUN='^Test(AggregateProviderRemap|(Sum|Avg|Count|Extrema|AnyValue|MoneySum|IntervalSum|IntervalAvg|StringAggregate|BooleanAggregate|RangeAggregate)Provider|(Bitwise|Statistical|Array)Aggregate)'  # aggregate providers
make check RUN='^Test(Composite(Arity|Counted|Leaf|Named|Providerless|Retained|Slice|String|Timezone)|IntrinsicVolatilityExcludesChildren|PlanningHooks|RetagReportsDerivedInputChanges|GroupIdentity|GroupLeaf|SubstituteKeepsTheProvider|FunctionSyntax|ClosureTransparency|Render)'  # node protocol
make check RUN='^Test(CaptureOperands|ResultNullability)'  # capture and nullability hooks
make check RUN='^Test(CatalogInquiry|TransactionInquiry|AdvisoryNode|BlockingInquiry|SequenceCall)'  # runtime-capability nodes
make check RUN='^Test(ACLValues|ACLNodes|ACLValueEvaluates|ObjectACLEvaluates|AccessInquiryEvaluates)'  # ACL functions
make check RUN='^Test(FTSOps|Point|Circle|Polygon|GeometryConversion|GeometryMeasure|MACADDR8Comparison|NetworkSubnet)Provider'  # operator providers
```

For any other family, select its prefix alone (for example `^TestPgcrypto`,
`^TestUUIDExtract`, `^TestLevenshtein`, `^TestRangeInspect`). Selectors run
module-wide, so a family prefix also selects that family's engine-integrated
units.
