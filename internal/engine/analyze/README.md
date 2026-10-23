# internal/engine/analyze

Statement binding: the expression and statement binder (`Binder`), `Describe`,
which binds one statement into a `Binding`, the `Host` interface through which
the binder reaches engine state, the capture groups that views, rules, policies
and prepared statements read, and the pure analysis helpers that the binder and
engine DDL share. Rule rewriting, view-write lowering and RLS walkers are in
[`view`](../view/README.md); ON CONFLICT and MERGE execution state is in
[`dml`](../dml/README.md).

## Imports

Layer L10 of [ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture).
It imports `plan`, `exec`, `catalog`, `storage`, `expr`, `functions`, `ids`,
`lock`, `access`, `authorization`, `pgcatalog`, `proc`, `routine`, `sequence`,
`typedef`, `extension`, `parser`, `sqlstate`, `sqltype` and the standard
library, and never the root engine or `session`. The engine, the root driver
(for `ParameterInfo`) and the upper feature packages (`stored`, `regobj`,
`view`, `dml`, `table`, `index`, `partition`, `role`, `control`) import it.

## What it owns

- **`Binder`** (`expression.go`, the other `*_bind.go` and binder files,
  `binder_groups.go`): per-statement binder state and its field groups
  (`TargetContext`, `ParameterState`, `ConsumerContext`, `StoredSyntaxState`,
  `ClauseState`, `GroupingState`, `QueryScopeState`, `CTEState`). `NewBinder`
  builds one over a `Host`; `Describe` (`describe_bind.go`) binds a statement.
  `functions` reaches the binder through `functions.Binder`.
- **`Host`** (`host.go`): the engine services the binder calls (name and type
  resolution, privileges, definition reads, captures, the retag walker, stored
  views and the view read graph). The engine implements it (`bindingHost`).
- **`Binding`** (`binding.go`, `binding_groups.go`, `payloads.go`,
  `create_table.go`): the statement header (target, storage, view write,
  targets, used columns, query, CTEs, snapshot demands, CURRENT OF, predicate,
  RETURNING, dependencies, parameters), the kind payloads (`InsertBinding`,
  `UpdateBinding`, `DeleteBinding`, `MergeBinding`, `CreateTableBinding`, ...)
  and `ParameterInfo`. `*Binding` implements `plan.CTEMutation`.
- **Capture groups** (`binding_groups.go`, `captures.go`): `ViewCapture`
  (rendering captures keyed by `ViewLiteralPath`), `PreparedCaptures`
  (temporal, type-occurrence, sequence and regtype captures that a prepared
  statement re-resolves; `Types`/`PreparedTypeKey` also hold domain and
  geometric typename-cast identities), `ResolverInputs`, `RuleBinding`,
  `DefaultOrigin`. `DomainCastAnnotation` reconstructs the live spelling of a
  retained domain ID (including immediate companion arrays, after checking the
  live carrier/element pair) before ordinary lookup, authority and coercion,
  only while its analysis environment is valid.
- **`Authority`** (`authority.go`): the catalog and search-path authority a
  binding runs under. The binder reads only its catalog and hands it back to
  the host for names, privileges, definition reads and captures.
- **`Expected`** (`expected.go`): what a held description gives re-analysis
  (fixed parameters and results, catalog owner and epoch, captures, plan
  owner). `Signature`/`CheckSignature` (`signature.go`) are the 0A000
  cached-plan check; `Expected.CheckLifetime` applies it to a replaced catalog.
- **Name lookup** (`lookup.go`, `search_path.go`, `type_name_resolution.go`):
  `LookupKind`, `NamespaceSelection`, `ExplicitPath`, `LookupPath`,
  `EffectivePath`, `LookupNamespaces` and `CreationNamespace` read one by-value
  `PathInput[E]` whose `Env` type parameter (constrained by
  `access.Environment`) avoids boxing. `ResolveTypeName` takes a
  `TypeNameOperation` (context and warning callback) because the package never
  imports `session`.
- **Prepared domain casts** (`domain_casts.go`): `RetireDomainCasts` and
  `PublishDomainCasts` swap a held description's domain/type-name cast group
  without mutating a shared map.
- **Captured input retargeting** (`captured_inputs.go`): `IdentifierTarget`
  and the `RegtypeRetarget`, `OperatorRetarget`, `RoutineRetarget` and
  `RegTSRetarget` interfaces.
- **ON CONFLICT clause** (`insert_conflict.go`): `InsertConflict`; `Clone`
  copies the header per command and shares analyzed expressions.
- **Slots** (`binding_groups.go`): `RuleCTEsSlot` and `ViewWriteSlot`; the
  engine recovers each with one checked helper (`binding_slots.go`).
- **Privilege walkers** (`binding_privileges.go`): `CheckBinding` and
  `CheckQuerySources` check target, column reads and query sources in
  PostgreSQL's order, each source under its own principal. `Privileges` (the
  engine's `AuthorizationView`) decides each object through `access`.
- **Row-lock admission** (`lock_admission.go`): `NewLockAdmission` computes a
  `LockAdmission` from syntax before binding; the engine holds one per
  statement scope and the binder reads it per query level.
- **Query namespaces and helpers** (`namespace.go`, `view_namespace.go`,
  `query_*.go`): `QueryNamespace` and `RelationNamespace` for consumers outside
  queries (checks, defaults, generated columns, index, partition and policy
  expressions); plan walks that freeze stored frames (`CollectQueryFrames`),
  detect changed descriptors/routine carriers, map runtime reads, prune unused
  window calls and decide row-lock legality.
- **Shared helpers** (`coercion.go`, `errors.go`, `record_types.go`,
  `operator_signature.go`, `operator_binding.go`, `create_table_columns.go`,
  `statement_checks.go`, `default_closure.go`, `retained_input.go`,
  `rule_namespace.go`, ...): coercion properties, collation derivation
  (`CollatedInfo`), input error positions, closed-DEFAULT predicates, retained
  expression inputs and the rule OLD/NEW scope. They read only their arguments
  and reach no engine state.
- **Default evaluation** (`default_eval.go`): `ApplyDefaultOrigin` and
  `EvalDefault` over an `exec.Context`.

## Invariants

- **Value semantics.** A `Binding` is copied by value and the copies are
  mutated (preflight, rule remap, MERGE arms, view-write INSERT). Payloads and
  groups are values; `UpdateBinding`, `MergeBinding`, `Returning` and slot
  values are shared pointers.
- **Nil means absent.** The engine never stores a nil pointer in a slot or an
  `Authority`; `b.Insert.Conflict` is nil without ON CONFLICT.
- **One clause per command.** Execution writes the normalized predicate and the
  preflighted DO UPDATE action into the binding's clause, so a stored rule
  action clones it per command; arbiter and write state never live in it.
- **Byte-identical stored SQL.** Capture maps keep their contents and iteration
  rules so views, rules, policies, defaults and checks deparse byte-identically.
- **No mutexes, no engine state.** The binder reaches engine state only through
  `Host`. Binding runs with `Engine.mu` held by the caller; host methods never
  take or release it.
- **Authorities.** Only the host interprets a binding authority; engine code
  recovers stored authorities with `authorizationOf`, which rejects binding
  authorities. A retarget returns a copy; a nil sink keeps the resolver's sink.
- **Preparation is effect-free.** Describing a statement runs no producers,
  defaults, sequences or writes. Parameter types come from SQL destinations,
  never Go argument kinds. Analyzed trees and their conservative dependencies
  are immutable; conditional planning keeps every analyzed dependency and
  authorization even when execution removes a branch.

## Binding rules

- **Callable selection.** SQL calls use one callable selector. SQL formals and
  provider-owned pure `NamedCall` shapes map supplied types in written order
  before labeled path masking. Only the winning SQL call gets checked hidden
  defaults and a validated input assembly. Actuals use Implicit conversion;
  `BindRoutineDefault` uses Assignment through the same catalog-aware seam.
  Defaults resolve against the staged namespace before their declaration is
  published; body validation then sees the declaration. Stored capture sees
  written occurrences only; generic traversal also sees hidden defaults.
- **Geometric typename calls.** Unary `box`/`circle`/`lseg`/`path`/`point`/
  `polygon` calls with visible SQL competitors take the explicit relabel/IO
  shortcut after exact declaration lookup and before implicit/unknown matching,
  finishing through ordinary explicit coercion. Stored definitions become a
  qualified explicit cast only after selection succeeds. Prepared statements
  record the target `Usage` at the call offset in `Prepared.Types`; held
  execution rebuilds the cast from the original operand, so a new overload
  alone cannot replace it.
- **Conditionals.** Simple CASE binds its test through a lexical
  `plan.CaseTestExpr` per equality and keeps the winning conversion tree in
  `CaseBranch.Test`; the test expression is neither embedded nor rebound.
  COALESCE, NULLIF, GREATEST/LEAST and CASE results keep `CoerceConditional`
  trees.
- **Boolean consumers** (including IS TRUE/FALSE/UNKNOWN) infer only unknown
  inputs as BOOL, then apply Assignment conversion to known sources through
  `CoerceSelected`. Only genuine type refusal becomes the construct's 42804;
  routine/provider failures stay authoritative. The opaque catalog ANYARRAY
  (`CatalogNullUsage`) gets the construct diagnostic before cast selection.
- **Explicit casts.** Declared casts are selected before the unsupported-target
  capability guard; target resolution precedes source binding.
- **Comparisons.** Key binding rejects conflicting implicit collations during
  analysis, even for empty input. IN common-type analysis includes the left
  operand and batches eligible non-column items (converted in source order
  before the left input is finalized); column-containing items compare
  pairwise. Quantified comparisons establish the scalar operator before
  converting contextual unknown literals or parameters, and erase a named ROW
  constructor's outer cast only at that structural occurrence.
- **Frozen syntax keys.** Every writer and reader keys an occurrence by the
  node's own `Pos` (a postfix cast's `::`), never `SourcePosition`.
- **SQL/JSON.** Query functions dispatch to the `functions` binder; result
  conversion stays inside the node's ON ERROR boundary. Literal capture records
  RETURNING and every document, path, PASSING and DEFAULT occurrence in written
  order. `JSON_TABLE` binds through `BindFrom` into one anonymous composite
  `FunctionFromNode`; every declared type/default is an analysis dependency even
  when unprojected, and there is no synthetic routine or grant.
- **DEFAULT shape.** The JSON_TABLE DEFAULT validator inspects the bound
  coercion recipe before RETURNING fallback (real cast/typmod functions supply
  shape; relabel/IO/domain/collation wrappers and array elements are
  inspected). Table defaults: raw aggregate/grouping/subquery/window guards,
  then binding, enclosing shape, the bound-tree `ColumnExpr` restriction, set
  detection and fallback conversion, in that order. Locations use transformed
  operands and operator expression starts.
- **COPY WHERE** uses the full relation namespace and Boolean binder under
  `Consumer.CopyWhere`, with COPY-specific forbidden-construct diagnostics.
  Target-use capture rejects generated references before preflight folding and
  supplies SELECT-column privileges; it is independent of
  `Target.GeneratedOwner`.
- **Aggregates.** `SelectAggregateSupport` reuses overload ranking with the
  exact declared shape (no default omission or variadic expansion), then checks
  binary compatibility. Owned aggregates fold ordinary-argument collation with
  `CollatedInfo` starting from the actual result usage; FILTER and ordering
  expressions stay separate. Builtin aliases may share operation state, but each
  distinct written routine stays in the call's authority list.
- **Star expansion.** `QueryScalarOwner` keeps generated record-field attribute
  paths and result usage when an expansion shares one source position.
- **Operator diagnostics** reuse `functions.ComparisonError` and the visible
  type-name formatter (`DiagnosticTypeName`), omitting a namespace only when
  visibility permits; descriptor persistence names are not visibility authority.

### Catalog-aware coercion

- `cast.go` uses `catalog.SelectCoercion` to select declared pairs before
  fallback and builds only the winning routine/IO/relabel expression.
  `catalog.CanCoerce` is the effect-free feasibility predicate shared with
  callable/operator probes; it binds no routine, checks no EXECUTE and captures
  no losing-candidate dependency. Catalog-free callers pass nil and keep fixed
  coercion selection.
- Stored routine syntax keeps the selected function and synthetic arguments;
  stored IO/relabel syntax keeps its method without a cast-row dependency.
  Prepared captures record pair identity or absence for invalidation; exact
  usage identity skips declared-edge lookup and absence capture.
- Array mapping evaluates the source once in a scoped element frame and keeps
  shape and lower bounds. Already-bound operands keep their position, usage and
  properties through fixed conversion instead of being rebound.

## What stays in the engine

- Search-path wrappers (`schema_path.go`), `pathInput` (reads `search_path`
  under the session settings mutex only for unqualified resolution),
  `temporaryNamespace`, annotation type-input resolution and the sticky
  `retireAnalysisTypeCasts`.
- The binder construction point (`newExpressionBinder`), the host
  (`bindingHost`, `analysis_host.go`), the `describe*` wrappers, DDL that drives
  its own binder (rule/policy conditions, defaults, checks, index and partition
  expressions), authority adapters (`analysis_authority.go`), `expectedOf`,
  `signatureOf`, and the host forwarders to `stored`.
- Rule program execution and admission.

## Testing

Binder tests need an engine host, so most live in `internal/engine` and name
`analyze.Binder` directly; package tests here cover the pure seams. Public
binding behavior belongs in `reference/`; follow
[docs/TESTING.md](../../../docs/TESTING.md). Add `REFERENCE=1` for the oracle.

- Package seams: `RUN='^Test(QueryOuterJoinsComparisonNegators|SQLCallableParameterMapping|RelationNamespaceShape|InsertConflictClone|ExpectedCheckLifetime|CheckQuerySources|CheckBindingDelete|DomainIdentityCaptureRetirement|CreateAggregateStoredSyntaxCastOwnership|SQLJSONDefaultShape)'`
- Host, slots and authorities (engine): `RUN='^Test(AnalysisHost|BindingAuthorityOverlaysOperationDefinition|ExpectedOfCopiesHeldInputs|CapturedInputsRetargetOnlyTheirSinks|BindingSlotsKeepNilAbsent|ViewWriteSlotReportsBaseRelation)'`
- Search path and type names (engine): `RUN='^Test(SearchPath|PGNamespaceTemporarySuperuserAuthority|ExtensionTemporaryLookup|VisibilityInquiryAuthorizationAndPathKinds|RegTSProviderResolution|AccessViewTypeDenial|DomainIdentity)'`
- Query legality helpers (engine): `RUN='^Test(QueryOuterJoins|QueryWindow|QuerySet|StringSplit|QuerySubquery)'`
- DML binding: `RUN='^TestReference(InsertConflict|InsertSelect|InsertReturning|Merge)'`
- Query, row-lock and composite binding: `RUN='^TestReference(RowLock|CompositeExpr)'`
- Search path and type names: `RUN='^TestReference(SearchPath|SchemaSearchPath|RegtypeNames|DomainPreparedLifecycle|DomainIdentityPrepared)'`
- Casts and routine parameters: `RUN='^TestReference(CastDefinition|RoutineParameters)'`
- Stored constructors and aggregates: `RUN='^TestReference(ComparisonStoredConstructor|CreateAggregate)'`
- SQL/JSON and COPY WHERE: `RUN='^TestReference(JSONTable|CopyFilter)'`

To find engine units that build bindings, grep `internal/engine/*_test.go` for
`describe(` and the payload names.
