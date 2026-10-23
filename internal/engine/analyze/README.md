# internal/engine/analyze

Statement binding. Since 12d.6 it holds the expression binder (`Binder`, its
field groups and the statement-kind binders), `Describe`, which binds one
statement, and `Host`, the engine services the binder calls. It also holds
the bound form of a statement (`Binding`),
its per-kind payloads and the capture groups that views, rules, policies and
prepared statements read, plus the vocabulary the binder and its host speak:
the binding authority, the held description's inputs, name-lookup kinds and
selections, and the retargeting of captured identifier inputs. It also
holds the clean analysis helpers that the binder and engine code share
(coercion and collation derivation, input error positions, the held
signature check), and since 12d.4 the statement's row-lock admission, the
query namespace types and the query helpers the binder shares with the
engine, and since 12d.3b the DEFAULT closure predicates, the retained-input
walk and the rule namespace. The binder names no engine type; it reaches engine state only
through `Host`. The package sits at layer L10 of
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture).
It imports `plan`, `exec`, `catalog`, `storage`, `expr`, `functions`, `ids`,
`lock`, `access`, `authorization`, `pgcatalog`, the Part A content of
`routine`, `sequence`, `typedef` and `extension` (§3 rule 6),
`parser`, `sqlstate`, `sqltype` and the standard library, and never the
engine or `session`. See
[ENGINE-SPLIT-analyze.md](../../../docs/ENGINE-SPLIT-analyze.md) (step 12).

SQL function calls use one callable selector. SQL formals and provider-owned
pure `NamedCall` shapes map supplied types in written order before labeled path
masking; positional identity masking remains unchanged. Only the winning SQL
call receives checked hidden defaults and a validated input assembly. Actuals
use Implicit conversion; `BindRoutineDefault` selects Assignment conversion through
the same catalog-aware seam, without the table-default volatility whitelist or a
formal/parameter namespace. Declared context refusal precedes generic string IO;
domain/collation normalization and the exact storage destination remain separate.
Defaults resolve against the legitimate staged namespace before their new
declaration is published; other staged objects remain visible. Body validation
then sees the declaration, preserving existing self-recursion behavior.
Closed body formals still use input-only names and slots. Stored capture sees
written occurrences only, while generic traversal also sees hidden defaults.
See [parameter proof](../../../reference/operators/README.md#routine-parameter-binding).

Ordinary unary `box`/`circle`/`lseg`/`path`/`point`/`polygon` typename calls with visible SQL competitors share
the existing explicit relabel/I/O shortcut after exact declaration lookup and
before implicit/unknown best matching. The private selection retains resolved
target Usage and the already analyzed operand, then finishes through ordinary
argument finalization/explicit coercion—not a synthetic parser FunctionCast.
Known TEXT exact SQL declarations still win; unknown string/NULL constants are
not unresolved external parameters. Legacy parser-lowered casts keep their
existing finish route, and selected routines retain normal authority/dependencies.
For stored definitions, both provider and SQL-competitor routes retain the call
position and original operand recipe. Only after successful selected coercion,
the frame-local stored syntax becomes an ordinary qualified explicit cast to the
selected target. Literal capture therefore follows the real bound shape, and
later overloads cannot reinterpret that stored typename call during replay.
Nonstored source syntax and the strict capture mismatch checks remain unchanged.
Successful geometric typename selection also records only its target Usage at
the actual call offset in the existing `Prepared.Types` group. While that
analysis remains valid, held execution checks current lexical namespace
authority then rebuilds the ordinary explicit cast from the original operand;
a newly created overload alone cannot replace the selected typename conversion.
Existing environment/definition invalidation retires and atomically recaptures
these occurrences with domain casts; no expression/coercion cache or new
invalidation trigger is introduced.


Simple CASE binds its saved test through a lexical `plan.CaseTestExpr` for each
equality, retaining the actual winning conversion tree in `CaseBranch.Test`.
The original test expression is not embedded in those trees or rebound at
execution. Their properties and dependencies participate in the same visitors
as ordinary conditional operands. COALESCE, NULLIF, GREATEST/LEAST and CASE
result arms already retain `CoerceConditional` trees; post-selection fixed
fallbacks remain catalog-free.

Boolean consumers (including all six IS TRUE/FALSE/UNKNOWN forms) infer only
unknown inputs as BOOL, then select Assignment conversion of known sources through
`CoerceSelected`. Known parameters keep their source contracts; selected nonstrict
routines receive typed NULL and retain their normal properties and dependencies.
Only genuine type refusal becomes the enclosing construct's 42804 diagnostic;
routine/provider failures remain authoritative. Set-return rejection follows the
selected conversion, without walking every logical-chain prefix again.
The canonical opaque catalog ANYARRAY result (`CatalogNullUsage`) receives that
same construct diagnostic before concrete cast selection; it is not a coercible
BOOL or an unknown input. Producer errors still propagate before this check,
and ordinary concrete conversions retain their selected assignment semantics.

Explicit cast declarations are selected before the existing unsupported-target
capability guard. This admits declared conversions to existing codec-backed
targets (including range/multirange) without enabling undeclared fixed-provider
casts or extending the native target whitelist. Target resolution still precedes
source binding; source errors can now precede that legacy capability diagnostic.

Standard SQL/JSON query syntax dispatches to the functions binder. Unlike
constructor RETURNING, its result conversion stays in the query node's ON ERROR
boundary. Literal capture records RETURNING usage and every document/path,
PASSING and DEFAULT occurrence in written order; normalization, qualification
and VALUES aggregate inspection traverse the same query children.

`JSON_TABLE` binds through general BindFrom into one anonymous composite
FunctionFromNode, with actual expression captures and parent-first member order.
Every declared type/default remains an analysis dependency even for empty paths
or unprojected columns. There is no synthetic JSON_TABLE routine/grant. Normal
lateral rebasing, source authority, prepared generations and stored namespace
ownership remain the existing query authorities. Stored capture pairs only
written document/PASSING/default occurrences; synthetic column documents are not
extra SQL occurrences.
The JSON_TABLE DEFAULT validator uses the ordinary bound coercion recipe before
RETURNING fallback conversion: real cast/typmod functions supply function shape,
while relabel/IO/domain/collation wrappers inspect their input. Array coercions
also inspect the element expression. Ordinary parser CallExpr and decorated
FunctionExpr retain function shape; specialized regclass syntax preserves its
operand's shape, including actual constants and nonliteral operators. An elided
type-name call cannot launder a bare Param. Parameter dependence is not an
enclosing-shape proxy.
For ordinary supported scalar table defaults, binding precedes enclosing shape,
then a full bound-tree ColumnExpr restriction, set detection and fallback
conversion. Accepted operators/functions with columns get the distinct column-
reference diagnostic; bare columns/special shapes get the generic shape error.
Unknown columns/routines keep ordinary binder diagnostics before either check.
DEFAULT locations use transformed operands and operator expression starts, not
operator tokens or specialized cast separators. Existing raw aggregate/grouping/
subquery/window guards remain before analysis; competing errors within those
forbidden families and standalone SQL/JSON admission are unchanged.
Private seam proof: `make check RUN='^TestSQLJSONDefaultShape$'`.

## What it owns

- **`Binder`** (`expression.go` and the other expression, function,
  operator, query, CTE, aggregate, window and DML binder files;
  `binder_groups.go`): the binder state for one statement, the host, the
  statement inputs and the field groups (`TargetContext`, `ParameterState`,
  `ConsumerContext`, `StoredSyntaxState`, `ClauseState`, `GroupingState`,
  `QueryScopeState`, `CTEState`). `NewBinder` builds one over a `Host`;
  `Describe` (`describe_bind.go`) binds one statement into a `Binding`, with
  the statement-kind binders (`*_bind.go`, `insert_binding.go`,
  `update_rows.go`, ...). The members engine DDL and the engine's tests use
  are exported; `functions` reaches the binder through `functions.Binder`.
- **`Host`** (`host.go`): the engine services the binder calls (name and
  type resolution, privileges, definition reads, captures, the retag walker,
  stored views and the view read graph). The engine implements it
  (`bindingHost`).
- **`Binding`** (`binding.go`): the header every statement kind may use
  (target relation, storage and ref, view write, targets, used columns, the
  query, CTEs and query sources, snapshot demands, CURRENT OF, predicate,
  RETURNING, dependencies, parameters and expressions), the kind payloads
  and the capture groups. `ParameterInfo` is the inferred parameter list;
  the driver names it directly (`engine.FrameImporter` takes it).
  `*Binding` implements
  `plan.CTEMutation`, so a data-modifying CTE carries its binding.
- **Kind payloads** (`binding_groups.go`, `payloads.go`, `create_table.go`):
  `InsertBinding`, `DeleteBinding`, `CreateTableBinding` (held by value),
  `UpdateBinding` with its `AssignmentGroup`s, `MergeBinding` with its
  `MergeArm`s, `Returning`, and the CREATE TABLE records
  (`ColumnDeclaration`, `InheritanceInput`, `LikeInput` with `LikeMask`,
  `UniqueConstraint`).
- **Capture groups** (`binding_groups.go`, `captures.go`): `ViewCapture` (the
  rendering captures keyed by `ViewLiteralPath`, with view columns,
  namespaces and composite occurrences), `PreparedCaptures` (temporal,
  type-occurrence, sequence and regtype captures a prepared statement re-resolves).
  `Types`/`PreparedTypeKey` share composite captures with domain and finite
  geometric typename-cast identities;
  `DomainCastAnnotation` reconstructs the live spelling of the retained ID before
  normal lookup/authority/coercion, only while its analysis environment is valid.
  Domain casts include immediate domain-element companion arrays. Reconstruction
  checks live reciprocal carrier/element identity; bracketed syntax names the
  current element without changing its bounds, direct companion syntax names
  the current array. Both stay subject to ordinary annotation authority/coercion.
  Composite-specific validators and stored `ViewCapture.Composites` remain narrow.
  `ResolverInputs` (the statement input resolvers, captured by
  `NewResolverInputs`), `RuleBinding`, and `DefaultOrigin` (where a default
  expression came from).
- **`Authority`** (`authority.go`): the catalog and search-path authority a
  binding runs under. The binder reads only its catalog (`ReadCatalog`) and
  hands it back to the host for names, privileges, definition reads and
  captures. The engine implements it over its authorization view (a stored
  authority) and over the same view read through the operation's current
  definition reader (a binding authority, from `BindingAuthority`).
- **`Expected`** (`expected.go`): what a held statement description gives its
  re-analysis (fixed parameters and results, catalog owner and epoch,
  temporal, composite, sequence and catalog-identifier captures, plan owner).
  The binder holds it by value; `Held` marks a held description.
- **Lookup vocabulary** (`lookup.go`): `LookupKind`, the provider family a
  search-path lookup serves, and `NamespaceSelection`, the namespaces a name
  lookup searches.
- **Search path** (`search_path.go`): `ExplicitPath` (the configured path:
  prefix, `$user`, `pg_temp` and pending creation, deduplication, USAGE
  omission; only VACUUM keeps peer TEMP namespaces), `LookupPath` and
  `EffectivePath` (pg_catalog and the own TEMP namespace implied per
  `LookupKind`), `LookupNamespaces` (qualified and unqualified name lookup,
  with its missing-schema and foreign-TEMP diagnostics) and
  `CreationNamespace` (a CREATE destination, reporting a deferred TEMP
  namespace). They read one by-value `PathInput[E]`: the `access.View`, the
  definition reader, the engine domain that mints namespace references, the
  `search_path` entries, the path prefix, the `$user` principal, the staged
  TEMP namespace and the materialized-population flag. `Env` is the root's
  view as a type parameter constrained by `access.Environment`, so building
  the input boxes nothing; it answers the own TEMP namespace when a decision
  needs it, and its `ReferencePath` (read by a type denial inside
  `View.Check`) resolves `LookupPath` again through the root.
- **Type names** (`type_name_resolution.go`): `ResolveTypeName`, the
  resolution of a type declaration in the namespaces the root's lookup
  selected: first match, admission flags (`TypeNameInput`), typmods and
  array bounds. `TypeNameOperation` carries the operation's context (called
  only for a typmod given as text) and a warning callback, because `analyze`
  never imports `session`; it is a parameter of its own so the callers'
  closures stay on their stack.
- **Prepared domain casts** (`domain_casts.go`): `RetireDomainCasts` and
  `PublishDomainCasts` swap the domain and type-name cast group of a held
  description's prepared types map without mutating a shared map.
- **Captured input retargeting** (`captured_inputs.go`): `IdentifierTarget`
  and the `RegtypeRetarget`, `OperatorRetarget`, `RoutineRetarget` and
  `RegTSRetarget` interfaces, which point the engine's captured resolvers in
  an input environment at one literal's position and sinks.
- **ON CONFLICT clause** (`insert_conflict.go`): `InsertConflict`, the bound
  clause (inference `ConflictKey`s and predicate or named constraint, read
  manifests, the DO UPDATE action and its RETURNING), held by
  `InsertBinding.Conflict`. `Clone` copies the header for one command, as
  rule actions need; the analyzed expressions stay shared.
- **Slots** (`binding_groups.go`): `RuleCTEsSlot` (the rule commands of a rule
  action's CTEs) and `ViewWriteSlot` (the view-write lowering; `Base` is the
  base relation the write lands on). The engine owns these values and
  recovers them with one checked helper each (`binding_slots.go`).
- **Shared statement helpers**, used by the binder and by engine DDL:
  `create_table_columns.go` (CREATE TABLE column declarations, column index,
  NOT NULL and unique constraints, `LikeConstraint`, LIKE options) and
  `statement_checks.go` (view output checks, INSERT targets and value errors,
  generated-column and inheritance conflicts, `TableKindError`,
  `GuardedCatalogRelation`, MERGE reachability). `statement_errors.go` holds
  the package's own `sqlError` and `insertError`.
- **Binding privileges** (`binding_privileges.go`): `CheckBinding` and
  `CheckQuerySources`, the walkers that check a bound statement's target,
  column reads and query sources in PostgreSQL's order, each source under its
  own principal. They decide nothing: `Privileges`, which the engine's
  `AuthorizationView` implements, decides each object through `access` and
  pairs a principal switch with its search path. View-write layers and view
  row marks are checked through its `CheckViewWrite` and `CheckViewRowMarks`.
- **Held signature** (`signature.go`): `Signature` and `CheckSignature`, the
  0A000 cached-plan check of a held description's parameters, results and
  catalog lifetime; `Expected.CheckLifetime` applies it to a replaced
  catalog owner or epoch during re-analysis. `StatementCatalogType`,
  `SameStatementUsage`, `SameStatementResultUsage` and `RefreshableTemporal`
  (which captured temporals a refresh re-inputs) belong to it.
- **Shared analysis helpers** (`coercion.go`, `errors.go`, `record_types.go`,
  `operator_signature.go`): coercion properties and Info,
  collation derivation (`CollatedInfo`), the domain-cast annotation pair
  (`DomainCoercionInput`, `RestoreDomainCastCollations`), storage
  destinations, `ComparisonOperator`, literal and reg* input error positions
  (`InputErrorAt`, `RegtypeInputError`), `RecordTypeMapper` and
  `OperatorSignature`. Engine DDL, retag, remap and preflight code call them
  too; they read only their arguments. The OVERLAPS inline cost model is
  `exec.OverlapsInlineSafe`, shared with preflight.
- **Catalog operator diagnostics** (`operator_binding.go`, `enum_diagnostics.go`):
  binary resolution failures reuse `functions.ComparisonError` and the binder's
  `DiagnosticTypeName`. Array element names recurse through the same captured
  catalog/reference path as scalar user types, omitting namespace qualification
  only when actual visibility permits it. Descriptor persistence names are not
  diagnostic visibility authority. CASE and COALESCE common-type mismatches use
  this same visible-name formatter without changing category-first selection or
  operand error precedence. Unary formatting and ambiguity behavior retain the
  existing operator-error contract.
- **Row-lock admission** (`lock_admission.go`): `LockAdmission`, computed
  by `NewLockAdmission` from a statement's syntax before binding (the row
  marks of each SELECT level, the locked table occurrences as
  `PreparedSource`s, forward CTE references, data-modifying CTE targets as
  `MutationTarget`s), and the row-lock clause helpers (`RowLockClause`,
  `RowLockMode`, `RowLockDiagnostic`, `RowLockTargets`, `RootRowMarks`,
  `FirstSetRowClause`, `FutureCTEError`). The engine holds one per statement
  scope and passes it to `describe`; the binder reads it per query level.
- **Query namespaces** (`namespace.go`): `QueryNamespace`, `QueryQualifier`
  and `QueryColumn`, what a FROM item makes visible. Engine consumers outside
  queries (checks, defaults, generated columns, index, partition and policy
  expressions) build one for their relation, most through
  `RelationNamespace`. `QueryTargetLeaf` marks a DML target column.
- **View namespace kinds** (`view_namespace.go`): the `View*Namespace` node
  kinds stored in `ViewNamespace.Node`, `FrozenNamespaceNode` and
  `ViewFromRoot`, which view rendering also uses.
- **Query helpers** (`query_walk.go`, `query_sets.go`,
  `query_diagnostics.go`): plan and expression walks over query frames,
  recursive worktable slots, CTE volatility, `MutationBinding` (the checked
  recovery of a data-modifying CTE's binding), subquery demand and the
  EXISTS presence rewrite; the set-returning lowering helpers that query
  preflight shares; and column-name hints and query diagnostics.
- **Query legality helpers** (`query_graph.go`, `query_window_prune.go`,
  `query_join_legality.go`, `query_lock_legality.go`): walks of the plan IR
  that freeze a stored query's frames (`CollectQueryFrames`), detect changed
  descriptors and routine carriers (`QueryDescriptorsChanged`,
  `QueryRoutineCarriersChanged`), map an occurrence's runtime reads
  (`QueryExecutionSourceReads`, `QuerySourceReadUses`) and drop unused window
  calls (`PruneProjectWindows`); the FULL JOIN condition facts
  (`FullJoinCondition`, `FullJoinFacts`) and nullable-side row marks
  (`NullableQueryRowMark`, `FirstNullableQueryMark`, `QueryOccurrenceMark`);
  and the row-lock legality of a query shape (`QueryRootRowMode`,
  `QueryHasSetOutput`, `QueryRowLockLegality`, `QueryRowMarkReachesSource`).
  They read only their arguments; the engine's executor walks, view
  rendering, remap and lock initialization call them.
- **Shared expression helpers** (`default_closure.go`, `retained_input.go`,
  `operator_names.go`, `procedure_lookup.go`, `rule_namespace.go`): the closed
  DEFAULT predicates (`ClosedPgcryptoDefault`, `ClosedSequenceDefault`,
  `ClosedRegclassDefault`, `ClosedTransactionTimeDefault`, `ClosedUUIDDefault`, `ClosedSQLRoutineDefault`
  with `SQLRoutineDefaultClosure`) and the installed-call shape checks they use
  (`CryptoExpressionSignature`, `ValidInstalledUUIDOsspShape`,
  `ValidInstalledText`); `NeedsTypeDiagnosticInput` and
  `CaptureRetainedExpressionInput`, the catalog input a retained expression
  needs; `ArrayMembershipName`, `BindExtremaComparison`,
  `ValidateRegroleStoredLiteral` and `ProcedureInNamespaces`; and
  `RuleNamespace` with `RulePseudoScope`, the OLD/NEW frame of rule binding.
  The binder and engine DDL, preflight, retag, rendering and execution
  preparation call them; they reach no engine state.

## Invariants

- **Value semantics.** A `Binding` is copied by value and the copies are
  mutated (preflight, rule remap and graft, MERGE arms, view-write INSERT).
  Payloads and groups are values; `UpdateBinding`, `MergeBinding`,
  `Returning` and the slot values are shared pointers, as before the move.
- **Nil slots mean absent.** The engine never stores a nil pointer in a slot,
  so `b.ViewWrite != nil` keeps its meaning; `b.Insert.Conflict` is a plain
  pointer, nil without ON CONFLICT.
- **One clause per command.** Execution writes the normalized predicate and
  the preflighted DO UPDATE action into the binding's clause, so a stored
  rule action clones it per command; arbiter and write state never live in
  the clause.
- **Capture maps** keep their contents and iteration rules: stored SQL
  (views, rules, policies, defaults, checks) deparses byte-identically.
- **No mutexes and no engine state.** The types are plain records and the
  binder reaches engine state only through `Host`; binding runs under
  `Engine.mu`, held by the engine caller.
- **Authorities.** A nil `Authority` means none is stored; the engine never
  stores a nil pointer. Only the host interprets a binding authority: engine
  code recovers stored authorities with `authorizationOf`, which rejects
  binding authorities. A retarget returns a copy; a nil sink in the target
  keeps the resolver's sink.

## What stays in the engine

The search path wrappers in `schema_path.go` (about 35 files call them) and
`pathInput`, which reads the `search_path` entries under the session
settings mutex only for unqualified resolution, so qualified lookups take
no settings lock; the catalog view validation before lookup and creation,
and the creation reference, minted with the catalog domain;
`temporaryNamespace` (session TEMP ownership); the authorization, name
canonicalization and namespace lookup of `resolveAnnotationTypeInput`; and
`retireAnalysisTypeCasts`, which makes the cast retirement sticky through
the description's reanalysis flag.

The binder's construction point `newExpressionBinder`, the host
implementation (`bindingHost`, `Engine.analysisHost`, `analysis_host.go`) and
the root wrappers `describe`, `describeWithRule` and `describeScoped`; engine
DDL that drives a binder it builds (the rule condition and policy
expression binders behind `view.Host`, `bindCreateDefaults`, checks,
defaults, index and partition expressions); the authority adapters (`analysis_authority.go`:
`bindingAuthorityView`, `authorityOf`, `authorizationOf`), `expectedOf`
(`expected_inputs.go`), `signatureOf`/`checkStatementSignature` (the
`StatementDescription` wrappers of `CheckSignature`), the host forwarders to
the `stored` retag walker (`MapBoundExpression`, `MapBoundQueryExpression`)
and the captured input constructors (`captured_input.go`; the resolvers and
their retarget methods are in `regobj`); the host forwarders through which
the binder reads stored view definitions (`stored`): `StoredViewVolatile`,
`CaptureRetainedViewInput` and `ValidateViewReadGraph` (query and query
set preflight are in `view` since 27e, expression preflight in `exec`); rule
program execution and
admission (rule rewriting, its CTE commands and view-write lowering are in
[`view`](../view/README.md), and so are the RLS setting walkers
`view.CheckRowSecurityBinding` and `CheckRowSecurityRuleProgram` since 27i
and the view-definition and view-write walkers behind
`AuthorizationView.CheckViewWrite` and `CheckViewRowMarks`); and the
execution-side helper `capturedDomainReplayCell` over the binding types
(the default evaluators are here: `ApplyDefaultOrigin` and `EvalDefault`
in `default_eval.go`; `dml` has the ON
CONFLICT execution state `InsertConflictState`, built per command over the
binding's clause, and `MergeHasVirtualReads` and `SelectMergeArm`, see
[`dml`](../dml/README.md); `view` has `PhysicalBinding`,
`BindingDefaultOrigin` and `EvalBindingDefault`; `stored` has
`ValidateCompositeOccurrence` and `RemapLikeExpression`). The executor-side legality walks are
not here either: they import `stored`, `table` and `view`, so view 27e
moved them to `view` (`ValidateQueryJoinExecution`,
`ValidateMutationJoinExecution`, `ValidateExpressionJoinExecution`,
`ValidateMarkedQueryJoin`), and the row-mark readers of stored views to
`stored` (`ViewHasRowMarks`, `ViewRowLockLegality`, `ViewLeafRowMark`;
`view.CheckRowMarks` checks them). Since 12e the engine names these types directly
(`analyze.Binding`, `analyze.Binder`, ...); no aliases remain.

### Catalog-aware coercion

`cast.go` uses `catalog.SelectCoercion` to select direct declared pairs before
fallback and constructs only the winning routine/IO/relabel expression.
Domain/base and unknown-input rules stay separate. `catalog.CanCoerce` is the
effect-free authority shared with callable/operator probes and detached routine
support selection; it neither binds routines nor checks EXECUTE, infers
parameters or captures losing-candidate dependencies.
Common-category/preferred precedence is unchanged.
`sqltype.SelectCommonUsage` remains the single domain/type/typmod fold for
common-expression and anycompatible inference. The analyzer supplies pure
catalog-aware implicit feasibility; catalog-free callers pass nil to retain
fixed coercion selection.
The existing common callable selector also handles builtin-only names when user
pairs exist; concrete formal usages are candidate-local shape inputs, never
replacement ranking operands. Winning arguments are converted once before the
existing selected-argument/reentry provider path.

Stored routine syntax retains the selected function and synthetic arguments;
stored IO/relabel syntax retains its method without a cast-row dependency.
Prepared captures separately record relevant pair identity/absence for private
analysis invalidation. Array mapping evaluates the source once in a scoped
element frame and retains shape/lower bounds.
Exact usage identity bypasses declared-edge lookup and prepared absence capture;
different typmods and genuine absent pairs still retain normal invalidation.
Shared argument coercion accepts already-bound operands without raw syntax,
retaining their source position, usage and properties through fixed conversion
instead of rebinding or skipping the conversion.

Quantified comparisons erase a named ROW constructor's outer cast only at that
structural occurrence. The normalization writer and reader both key a CastExpr
by its own `Pos`, not its `SourcePosition` (which names the child for postfix
casts). The retained constructor remains the source association for already
coerced comparison fields; capture still requires normalized ROW syntax and
keeps member/expansion usages and dependencies. Whole-value and unused-CTE
occurrences retain their own logical type identities. This does not change
declared scalar or array cast recipes or their prepared pair observations.

## Testing

Owned ordinary aggregate binding retains one ordered argument vector and the
actual aggregate/support/type identities. `SelectAggregateSupport` reuses
existing overload ranking with exact declared shape (no default omission or
variadic expansion), then validates binary compatibility. Pure callable-family
result inference is shared with detached declaration validation, not duplicated
as a polymorphic type table or connected through analyzer callbacks.

Owned aggregates fold ordinary-argument collation with existing `CollatedInfo`
even for an uncollatable result; FILTER and ordering-only expressions remain
separate.
The fold starts from an actual ordinary ResultUsage, including default100, not
the state or a descriptor default. With no collatable ordinary input the exact
state/result type retains its declared collation, but effective input is zero.
Full explicit/indeterminate provenance reaches collatable result and builtin
private parameters. Closed quoted SQL support uses only normalized
implicit input-collation annotation, with existing declaration slot/type
ownership. Ordinary scalar and frozen SQL-standard parameter paths stay
unchanged. `TestCreateAggregateCollationBinding` inspects these private contracts,
exact usage remap and source immutability; selected Collation/SQLCollation own
the independent public outcomes.

Builtin aggregate aliases may share immutable-operation state, but every
distinct written routine remains in the shared call's immutable authority list.
Both local and outer-owner dedup merge those references; whole-query semantic
equality still compares exact authority, and stored remap owns a fresh list.
The existing admission frontier checks primary and aliases with their original
manifest, including empty input and excluding the existing PresenceOnly path.
Descriptor-backed owned aggregates retain exact identity rather than adopting
builtin alias sharing. Public selected Authorization covers independent alias
revokes/regrant, held/prepared and stored consumers; private Boolean/statistical
slot counts remain unchanged.

Frozen syntax uses one canonical occurrence key for every writer and lookup:
postfix casts key on their own `::` position rather than the operand position.
This keeps explicit outer casts distinct while retaining intentional quantified
constructor normalization. Private seam proof and the full public/affected
selection are documented in
[reference/aggregates](../../../reference/aggregates/README.md#create-aggregate-contract):

```sh
make check RUN='^TestCreateAggregateStoredSyntaxCastOwnership$'
```
Cast consumer proof: select `TestReferenceCastDefinition` in both modes,
including `BuiltinInvariance` and `RawCompositeArrayBounds`; the owning
[reference README](../../../reference/operators/README.md#cast-definitions)
lists exact selectors and expected smoke observations.

Stored constructor regression proof (unchanged public definitions):

```sh
make check RUN='^TestReferenceComparisonStoredConstructor(Lifecycle|Replay|Dependencies)?$'
make check RUN='^TestReferenceComparisonStoredConstructor(Lifecycle|Replay|Dependencies)?$' REFERENCE=1
```

Expected witnesses: row/single/domain quantified values survive stored-view
capture; held lifecycle reads survive removal of only the erased outer type;
replay preserves correlation, expansion, typmod and enum semantics with identical
native redump bytes and actual native dump replay on PostgreSQL. Dependency
cases retain enum/nested members and independent whole-value/unused-CTE types.

For conditional diagnostic and binary fixture remedies, select:

```sh
make check RUN='^TestReferenceCastDefinition(ContextPrecedence|BinarySafety|CapabilityBoundary)$'
make check RUN='^TestReferenceCastDefinition(ContextPrecedence|BinarySafety|CapabilityBoundary)$' REFERENCE=1
```

Expected witnesses: CASE/COALESCE category mismatches name visible `cast_src`
without `public.` qualification; FLOAT8 `1` through binary MONEY then XID8
retains word `4607182418800017408`; binary LINE `{1,2,3}` produces the ordinary
CIRCLE value `<(1,2),3>` with CIRCLE metadata. PATH/CUBE capability admission
uses raw catalog NAME/INT2/BOOL/CHAR projections to assert both physical
descriptors are `-1`/false/`d`, without depending on unrelated projection casts.


Catalog-first shared operator diagnostics retain the existing public array
type/visibility expectations and FTS diagnostic fields. Root selects both modes:

```sh
make check RUN='^TestReference(ArraySearchTypesAndFrames|FTSOpsDiagnostics)$'
make check RUN='^TestReference(ArraySearchTypesAndFrames|FTSOpsDiagnostics)$' REFERENCE=1
make check RUN='^TestReferencePGOperator(ResolverAgreement|Diagnostics)$'
make check RUN='^TestReferencePGOperator(ResolverAgreement|Diagnostics)$' REFERENCE=1
```

`TestInsertConflictCloneOwnsTheClauseHeader` covers `InsertConflict.Clone`,
`TestExpectedCheckLifetimeFencesCatalogTypes` the held lifetime check, and
`TestCheckQuerySourcesChecksEachSourceAsItsPrincipal` and
`TestCheckBindingDeleteChecksDeleteBeforeReads` the binding privilege walkers
(per-source principals, validation and check order), and
`TestQueryOuterJoinsComparisonNegators` the FULL JOIN comparison negators,
`TestRelationNamespaceShape` the relation namespace, and
`TestDomainIdentityCaptureRetirement` the prepared domain cast swaps.
The binder needs an engine host, so its unit tests stay in the engine and
name `analyze.Binder` and its exported members. The seams have engine unit tests: slots, binding
authorities, held inputs, captured input retargeting and the host's rule
check, expression and query mapping, stored-view reads and the view read
graph:

```sh
make check RUN='^(TestRelationNamespaceShape|TestQueryOuterJoinsComparisonNegators|TestDomainIdentityCaptureRetirement|TestInsertConflictCloneOwnsTheClauseHeader|TestExpectedCheckLifetimeFencesCatalogTypes|TestCheckQuerySourcesChecksEachSourceAsItsPrincipal|TestCheckBindingDeleteChecksDeleteBeforeReads|TestBindingSlotsKeepNilAbsent|TestViewWriteSlotReportsBaseRelation|TestAnalysisHostReportsRelationRules|TestAnalysisHostMapsBoundExpressions|TestAnalysisHostMapsBoundQueryExpressions|TestAnalysisHostValidatesViewReadGraph|TestBindingAuthorityOverlaysOperationDefinition|TestExpectedOfCopiesHeldInputs|TestCapturedInputsRetargetOnlyTheirSinks|TestAnalysisHostReadsStoredViews)$'
```

The search path and type-name decisions need engine-built views, so their
units stay in the engine: path kinds, ACL omission and TEMP placement
(`TestSearchPath*`), qualified TEMP authority
(`TestPGNamespaceTemporarySuperuserAuthority`), the trusted-replay `$user`
principal (`TestForeignKeyTrustedReplayNamespacePrincipal`), installed-function
and visibility paths (`TestExtensionTemporaryLookup`,
`TestVisibilityInquiryAuthorizationAndPathKinds`), the text-search path
(`TestRegTSProviderResolution`), the type denial that re-enters the live
search path (`TestAccessViewTypeDenialFollowsLiveSearchPath`) and the
prepared domain identities (`TestDomainIdentity*`):

```sh
make check RUN='^(TestSearchPath|TestPGNamespaceTemporarySuperuserAuthority|TestForeignKeyTrustedReplayNamespacePrincipal|TestExtensionTemporaryLookup|TestVisibilityInquiryAuthorizationAndPathKinds|TestRegTSProviderResolution|TestAccessViewTypeDenial|TestDomainIdentity)'
make check RUN='^TestReference(SearchPath|SchemaSearchPath|SchemaPathsPrepared|NamespaceIdentityPendingTemp|TemporaryTableNamespace|MaterializedViewRestrictedSearchPath|VacuumF7ForeignTempSearchPath|RegtypeNames|TableCoreQuotedTypeNamesAndBoundaries|DomainPreparedLifecycle|DomainIdentityPrepared)'
```

ON CONFLICT execution state over the clause: `TestRuleConflictInstantiation`,
`TestRuleCTEConflictInstantiation` and the `TestInsertConflict` engine units.

Engine units that build bindings: grep `internal/engine/*_test.go` for
`binding{`, `describe(` and the payload names. Public proof is the reference
selection of step 12, in quick mode and with `REFERENCE=1`:

```sh
make check RUN='^TestReference(InsertConflict|InsertSelect|InsertReturning|Update|Delete|Merge|Inheritance|TableLike|TypedTable|Rule|View|Prepared|CTEDML|Polic|DumpRound|Restore)'
```

The query, FROM and CTE binding that uses the lock admission, the
namespaces and the query helpers:

```sh
make check RUN='^TestReference(Query|RowLock|PartialIndex|GeneratedVirtual|CompositeExpr)'
```

The query legality helpers need engine-built plans, so their unit tests stay
in the engine (`TestQueryOuterJoinsNegatedKeys`, `TestQueryWindow*`,
`TestQuerySet*`, `TestStringSplitRowMarks`, `TestQuerySubquery*`):

`QueryScalarOwner` preserves generated record-field attribute paths and result
usage when a star expansion shares one source position. Reserved columns still
resolve by frame/scope/slot. `TestQuerySetScalarExpansionOwnership` covers
same-typed siblings in scheduled and upper scalar stages; the public
`TestReferencePgrowlocksExpressionRoutes` regression covers genuine populated
and empty installed-SRF records through direct and prepared execution.

```sh
make check RUN='^(TestQueryOuterJoins|TestQueryWindow|TestQuerySet|TestStringSplit|TestQuerySubquery)'
```

COPY WHERE uses the existing full relation namespace and Boolean binder under
`Consumer.CopyWhere`, with COPY-specific forbidden subquery/aggregate/window/set
diagnostics. Target-use capture rejects generated references before preflight
folding and supplies actual SELECT-column privileges. It introduces neither an
expression evaluator nor a new expression provider. Dual-mode Binding and
Privileges witnesses belong to the COPY filter suite in `reference/dml`.
The COPY consumer is independent of `Target.GeneratedOwner`: it suppresses
generation expansion in the real column binders without invoking generation
definition restrictions. The existing SRF placement owner reads CopyWhere
directly, preserving COPY diagnostics rather than rewriting another error.
