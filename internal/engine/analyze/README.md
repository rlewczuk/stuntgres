# internal/engine/analyze

Statement binding: the expression and statement binder (`Binder`), `Describe`,
which binds one statement into a `Binding`, the `Host` interface through which
the binder reaches engine state, the capture groups that views, rules, policies
and prepared statements read, and pure analysis helpers shared with engine DDL.
Rule rewriting, view-write lowering and RLS walkers are in
[`view`](../view/README.md); ON CONFLICT and MERGE execution state is in
[`dml`](../dml/README.md).

Reached PL expressions use the ordinary binder with private lexical slots and
current RECORD shapes. PL `$n` resolves the current lexical name first, including
an integer-FOR private counter; outside that scope it identifies all formals.
Unbound positional names remain errors, while SQL stays input-only.
Assignment tries declared/ordinary assignment casts before descriptor I/O fallback.
Simple CASE private search bindings bypass catalog ambiguity lookup and capture
the activation's complete scalar Usage in the ordinary PL-local expression.
Reached WHEN lists use the shared IN binder; no alternate comparison resolver
or published fake local carries search state.

Standalone CALL uses the common callable selector with the full formal-position
projection for every candidate kind, including function OUT declarations.
Candidate ranking and namespace masking precede invocation-kind rejection.
All written actuals bind and coerce; only IN/INOUT/VARIADIC positions enter the
executable child/inverse-slot map. Pure OUT dependencies retain analysis-only
operand purpose, so they cannot demand execution or retire unrelated folds.
Hidden defaults are attached only to the selected, omitted input slots.
Set-returning actuals are rejected by the shared set-placement admission, including
builtin series providers; the CALL context survives ordinary argument binding.
CALL schema uses the checked invocation tuple's exact member usages, retaining
declaration-owned labels and order without rebuilding a shape or exposing
declaration pseudo-types.
Reached PL CALL retains the winner's output-formal source projection separately
from its input-only executable assembly. Only selected coerced lexical-datum
occurrences qualify as scalar/record-field destinations; defaults, subscripts
and wrapping conversions do not inherit writable provenance. Destination recipes
belong to the invocation-private Binding, never declarations or retained plans.

Nominal typed literals for installed ltree and lquery resolve through the ordinary
type lookup before admission. Their resolved scalar descriptors, not their written
names, authorize input alongside domains.

Installed connectby calls complete anonymous caller definitions through the
same captured type/collation/input authority as other record sources, before
FROM schema expansion. Runtime width/depth/branch/serial checks remain after
STRICT NULL handling; binder completion never eagerly traverses source SQL.

Routine family probing is producer-free and leaves parameter inference untouched.
Declared inputs own namespace masking and overload ranking; separate concrete
targets and invocation signatures own actual coercion, defaults and variadic
packing. Simple and compatible variables are independent. Derived output members
use that input-family solution without supplying new inference. Scalar
enum/nonarray result restrictions are checked after selecting the winning
declaration, separately from actual-input applicability. INTERNAL signature
lookup admits only a declared INTERNAL actual, never UNKNOWN. Released builtin
signature-only facts enter the same selector even without a dynamic routine or
executable provider. They can select an explicit-call safety diagnostic, but
carry no provider authority; accepting then returning guards precede argument
conversion.
FROM completion retains the accepted multiple-OUT/TABLE result shape and original
routine declaration. These formal-derived tuples reject redundant column
definitions; scalar RECORD results still require caller-supplied definitions.
Scalar RECORD calls normalize the exact catalog-owned declaration identity to
an occurrence-owned deferred transport before argument assembly. The accepted
signature follows that transport or its completed FROM shape into private
invocation; the published declaration and provider reference remain unchanged.
Unknown string and NULL casts to scalar-family pseudo identities preserve their
UNKNOWN provenance rather than constructing pseudo datums.

## Imports

Layer L10 of [ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture).
It imports `plan`, `exec`, `catalog`, `storage`, `expr`, `functions`, `ids`,
`lock`, `access`, `authorization`, `pgcatalog`, `proc`, `routine`, `sequence`,
`typedef`, `extension`, `parser`, `sqlstate`, `sqltype` and the standard
library, and never the root engine or `session`. The engine, the root driver
(for `ParameterInfo`) and the upper feature packages (`stored`, `regobj`,
`view`, `dml`, `table`, `index`, `partition`, `role`, `control`) import it.

## What it owns

- **`Binder`** (`expression.go`, `expression_bind.go`, `*_bind.go`, `binder_groups.go`): per-statement
  binder state in field groups. `NewBinder` builds one over a `Host`; `Describe`
  (`describe_bind.go`) binds a statement. `functions` reaches it through
  `functions.Binder`.
  Installed heap-verification calls retain their exact provider slot and four
  OUT fields, use shared named/default coercion, and map written source
  occurrences to canonical inputs for frozen-literal capture.
- **`Host`** (`host.go`): the engine services the binder calls (name and type
  resolution, privileges, definition reads, captures, stored views). The engine
  implements it as `BindingHost`.
  Relabelled builtin/installed calls select through current catalog name buckets,
  then bind their checked immutable provider symbol. COW source/destination
  provider-name overrides prevent registry fallback from resurrecting vacated
  signatures, including parser-lowered aggregate, window and function-cast
  syntax routed through the same callable selector. Sparse readers expose the
  same membership without catalog scans.
  Quoted bodies receive the current routine qualifier separately from immutable
  `proc.Definition`; invocation reuse includes that qualifier. Standard formals
  stay checked slots. Stored provider normalization uses existing occurrence
  captures, not a public alias or capability for lookup-only metadata.
  `StoredProviderSymbol` is checked recipe data, not a resolver alias; annotation
  preserves written names, syntax flags and SQL/JSON options. Function and
  aggregate keys include the first written input position, retaining distinct
  generated unary UNNEST occurrences at one token. Selected ordinary names that
  lower to aggregate/window syntax retain that checked occurrence with written
  operands for capture and replay. Owned grouped and window aggregates keep their
  stronger source-call normalization and actual aggregate reference when operands
  replace binder-local slots; builtin provider-symbol capture is a separate lane.
  `FunctionCast` and its selected `FunctionExpr` share the key, so a type-named
  SQL routine remains checked rather than reverting to builtin cast selection;
  standard/postfix cast occurrences remain separate. Guarded normalization
  follows substituted roots, including cast-to-call lowering, and children
  without mutating shared syntax. Proof:
  `make check RUN='^(TestCreateAggregateStoredSyntaxCastOwnership|TestStoredProviderSyntax(GeneratedOccurrences|SubstitutedRoot|OwnedAggregateOccurrence|FunctionCastOccurrence)|TestSQLRoutineFrozenPresentation)$'`.
  Builtin aggregate/window plans retain original checked declarations from the
  binding snapshot alongside refs. Aggregate slot reuse requires the same selected
  declaration, owning descriptor, recipe and complete immutable call shape.
  Value aliases such as `bool_and`/`every` retain separate slots and EXECUTE proofs.
  Literal capture uses each slot's declaration, not a spelling lookup.
  Frozen views/standard bodies prove originals after relabel,
  relocation or owner COW; direct source resolves current public identity.
  The same capture pair belongs to DISTINCT (including ON), GROUP BY, set and
  recursive CYCLE-mark grouping keys, and window partition/peer equality.
  Equality selection keeps the checked declaration after any existing coercion;
  it does not perform a later lookup by the routine's mutable name.
  Owned aggregates derive support collation from their admitted direct and row
  inputs, excluding FILTER. Hypothetical ordering keeps independent per-pair
  comparison collations. Selected catalog kind, not name, admits WITHIN GROUP,
  its declaration split and grouped direct expressions. State and final result
  retain separate type identities and descriptor fallbacks. Each selected
  forward/inverse/final call owns a closed typed formal namespace.
  Owned binding completes signature/type coercion before resolving every ordering
  capability, then checks pair collations and derives support collation. An
  earlier pair's collation cannot mask a later key's missing ordering provider.
  Window implementation selection occurs after the complete frame resolves: a safe
  moving final overrides ordinary unsafe modes; otherwise unbounded starts,
  volatile inputs and subqueries select ordinary execution. Support volatility
  does not change that selection. Grouped calls always use ordinary supports.
  Ordinary projections with a final expose unknown computed nullability;
  without one they retain known nullable state metadata. Owned window results
  and stored view columns retain known nullable metadata.
  Owned window providers are built once after full-frame implementation selection;
  the pending call retains its checked aggregate and written operands, not a
  provisional ordinary provider or superseded support captures.
  Capture purpose is assigned before the first binder deduplication. The
  aggregate and written arguments/FILTER/ORDER BY keep their definition lane;
  only selected support bindings enter the scoped aggregate-support lane.
  Synthetic builtin recaptures inherit that scope, restored on success/error.
  Original revisions remain distinct, including mixed same-address captures.
  SQL routine grouping uses the existing property-rewrite hook: intrinsic
  procedure volatility merges only with actual rewritten children. An aggregate
  sibling cannot stage-taint a literal/parameter-only SQL call; strict stored
  metadata validation remains unchanged.
- **`Binding`** (`binding*.go`, `payloads.go`): the statement header, the kind
  payloads (`InsertBinding`, `UpdateBinding`, `MergeBinding`, ...) and
  `ParameterInfo`.
- **Capture groups** (`binding_groups.go`, `captures.go`): `ViewCapture`
  (keyed by `ViewLiteralPath`), `PreparedCaptures` (temporal, type, sequence and
  regtype captures a prepared statement re-resolves), `RuleBinding`,
  `DefaultOrigin`. Exact anonymous constructor shapes are reused only after fresh
  member binding proves identical usages. `domain_casts.go` swaps refreshable type
  groups without mutating a shared map.
  Contextual INSERT query outputs capture only previously unknown literals after
  assignment establishes their final Usage, at the original raw target ordinal
  and actual producer frame. Already typed outputs are not recaptured. Frozen
  child INSERT definitions share the rule namespace's existing frame reservation;
  lexical allocation remains monotonic. Mutation frame 1 can own genuine
  FROM/USING VALUES, function and JOIN-ON occurrences independently of its target
  row. A modifying CTE scan has no SELECT Query: its actual MutationBinding and
  RETURNING schema supply the definition output, including declared aliases.
  Common mutation finalization activates genuine CTE source demand from its
  actual query, UPDATE FROM, DELETE USING and MERGE source roots through the
  incumbent executable-query walk. These roots need not be registered SELECT
  queries. Main and child mutations share this rule; unused named SELECT CTEs
  retain analysis-only leaves and do not acquire runtime source demand.
- **`Authority`** (`authority.go`): the catalog and search-path authority a
  binding runs under; the binder reads only its catalog and hands it back to
  the host.
- **`Expected`** (`expected.go`, `signature.go`): what a held description gives
  re-analysis; `CheckSignature` is the 0A000 cached-plan check and
  `Expected.CheckLifetime` applies it to a replaced catalog. The actual retained
  `core.Description` supplies these inputs; core, not the binder or root adapter,
  owns the prepared analysis manifest, validity and lifecycle publication.
  Reused REGCONFIG constants preserve exact occurrence, type, value and original
  operand provenance. Their current operand manifest reads the captured OID's
  actual configuration and mapped dictionaries. Missing original configurations
  add no fictitious dependency; their captured typed value reaches runtime.
  Other identifier aliases retain their existing operand transport.
- **Name lookup** (`lookup.go`, `search_path.go`, `type_name_resolution.go`):
  `LookupPath`, `EffectivePath`, `CreationNamespace`, ... over one by-value
  `PathInput[E]`; `ResolveTypeName` takes a `TypeNameOperation` because the
  package never imports `session`.
  SQL-callable namespace failures first publish written-argument errors, then
  FILTER errors, preserving cast/type source positions. Only SQL namespace
  errors are deferred; cancellation and internal failures retain their identity.
  Successful lookup and builtin fallback do not gain a second argument pass.
  Ordinary callable operands are transformed before FILTER, and FILTER before
  namespace/signature errors. The selected routine's actual kind rejects scalar
  decorations before implicit selected-formal conversion; explicitly written
  invalid casts remain ordinary operand errors before FILTER. The
  selected-call frame carries the bound Boolean expression into its immediate
  provider/aggregate gateway; nested expression/provider entries suspend it.
  This avoids rebinding aggregate FILTER children without lending an outer
  call's proof to a nested call or retaining it for a later statement.
- **Captured input retargeting** (`captured_inputs.go`): `IdentifierTarget` and
  the `*Retarget` interfaces.
- **Privilege walkers** (`binding_privileges.go`): `CheckBinding` and
  `CheckQuerySources` check target, column reads and query sources, each source
  under its own principal, deciding each object through `access`.
- **Row-lock admission** (`lock_admission.go`): `NewLockAdmission` computes a
  `LockAdmission` from syntax before binding; the engine holds one per
  statement scope.
- **Query namespaces and helpers** (`namespace.go`, `query_*.go`, ...):
  `QueryNamespace`/`RelationNamespace` for query, mutation and stored row
  consumers (checks, generated columns, index, partition and policy expressions),
  plan walks, coercion properties, collation derivation (`CollatedInfo`) and
  error positions. Rowless consumers expose no relation namespace; assignment
  coercion does not itself admit or forbid column references. INSERT producers
  retain only a diagnostic target candidate, never a visible target row.
  Routine defaults also resolve against an empty namespace; table defaults
  retain their distinct prohibition on all column references.
  `ExpressionName` shares original typed target-label derivation with stored
  recursive presentation; it neither rebinds syntax nor consults current names.
- **Cached query outputs** (`query_order_bind.go`, `query_distinct_on_bind.go`,
  `query_producer_bind.go`): cached-slot constructors preserve value, diagnostic,
  collation and stage metadata, but clear routine and defining-recipe references.
  Sort keys, sort outputs and evaluation producers retain executable callable
  identity and admission/remap ownership; `CachedOrderExpr` only reads a slot.
- **Default evaluation** (`default_eval.go`): `ApplyDefaultOrigin` and
  `EvalDefault` over an `exec.Context`.

## Invariants

- **Value semantics.** A `Binding` is copied by value and the copies are
  mutated (preflight, rule remap, MERGE arms, view-write INSERT). Payloads and
  groups are values; `UpdateBinding`, `MergeBinding`, `Returning` and slot
  values are shared pointers.
- **Nil means absent.** The engine never stores a nil pointer in a slot
  (`RuleCTEsSlot`, `ViewWriteSlot`) or an `Authority`; `b.Insert.Conflict` is
  nil without ON CONFLICT.
- **One clause per command.** Execution writes the normalized predicate and
  preflighted DO UPDATE action into the binding's `InsertConflict`, so a stored
  rule action clones it per command (`Clone`); arbiter and write state never
  live in it.
- **Byte-identical stored SQL.** Capture maps keep their contents and iteration
  rules so views, rules, policies, defaults and checks deparse byte-identically.
- **No mutexes, no engine state.** The binder reaches engine state only through
  `Host`. Binding runs with `Engine.Mu` held by the caller; host methods never
  take or release it.
- **Authorities.** Only the host interprets a binding authority; engine code
  recovers stored authorities with `authorizationOf`, which rejects binding
  authorities. A retarget returns a copy; a nil sink keeps the resolver's sink.
- **Preparation is effect-free.** Describing a statement runs no producers,
  defaults, sequences or writes. Parameter types come from SQL destinations,
  never Go argument kinds. Analyzed trees and their dependencies are immutable;
  conditional planning keeps every analyzed dependency and authorization even
  when execution removes a branch.
  Named composite field selection captures the exact stable backing attribute
  and owning revision through the coherent definition reader. Fresh, retained
  and expanded live fields validate the same type/relation/member ownership;
  retired retained fields keep their existing typed-NULL behavior.
  Catalog-identifier operand/name captures retain operand-purpose dependencies
  for scalar and compound operator/routine inputs, including reused captures.
  A real executable use upgrades the same identity regardless of collection
  order; merging binder manifests preserves that purpose. Ordinary analysis
  still checks the complete manifest, separately from retained-fold invalidation.
- **Frozen syntax keys.** Every capture writer and reader keys an occurrence by
  the node's own `Pos` (a postfix cast's `::`), never `SourcePosition`.
- **Column visibility.** Every row consumer installs its namespace explicitly.
  Nil and empty namespaces expose no local rows; retained write-target metadata
  never supplies an implicit input. Hidden qualifiers contribute diagnostics,
  not executable columns. INSERT producers hide their target, RETURNING exposes
  its own row, and MERGE arms expose only their permitted sides.
  Schema qualification selects an already-visible unaliased relation; aliases
  hide base names. Qualified lookup chooses the nearest matching relation before
  looking for its column, without retrying outer relations on a missing column.
  Allowed correlation preserves frame identity, stable attribute origins,
  descriptors and separate target/source read manifests.
  Eligible real sources expose `tableoid` independently of ordinary slots.
  Comma items contribute unqualified system candidates; explicit JOIN operands
  remain relation-qualified-only. Ordinary names shadow within each visible
  item, not across items. System reads mark a separate table SELECT requirement,
  never a negative index in dense `Uses`; CHECK keeps its own consumer namespace.
  Function-source arguments capture system source occurrences separately from
  ordinary slots. Local-left occurrence membership determines dependency and
  RIGHT/FULL rejection even for zero-column sources; ancestor captures remain
  exported without slot arithmetic.

## Binding rules

- **Callable selection.** SQL calls use one callable selector; only the winning
  call gets checked hidden defaults and a validated input assembly. Actuals use
  Implicit conversion; `BindRoutineDefault` uses Assignment through the same
  catalog-aware seam. Stored capture sees written occurrences only.
- **Catalog-aware coercion** (`cast.go`): `catalog.SelectCoercion` selects
  declared pairs before fallback and builds only the winning expression.
  `catalog.CanCoerce` is the effect-free feasibility predicate shared with
  callable/operator probes: it binds no routine, checks no EXECUTE and captures
  no losing-candidate dependency. Catalog-free callers pass nil. Prepared
  captures record pair identity or absence for invalidation.
  Seed-only comparison resolution retains its allocation-free shortcut for
  ordinary inputs. A missing signature is resolved against visible catalog
  operators and implicit coercions before reporting a missing operator; hidden
  installed operators supply no signature or execution authority.
- **Explicit casts.** `Cast` uses descriptor capabilities and the directional
  coercion planner after declared-pair selection. Supported pairs without a
  path report cast diagnostics, not a type whitelist. Input-free identity can
  retain an existing concrete source even when its SQL input/declaration role
  is unavailable. Named composite-to-record and composite-array-to-record[]
  passthrough includes domains over composites and preserves source descriptors,
  domain identities and result metadata.
  Generic scalar RECORD annotations use a query-only deferred descriptor:
  typed string casts reach its anonymous-composite input diagnostic, while
  RECORD identity and typename shortcuts preserve the source occurrence owner.
  Exact callable signatures precede typename shortcuts; unknown constants,
  binary compatibility and restricted IO are distinct from unresolved Params
  and array element conversion. Declared base-type pairs determine shortcut
  eligibility before generic fallback: function-method casts are not typename
  calls. Winning binary/IO conversions use the ordinary coercion owner, whose
  held captures include consulted domain-base pair presence or absence.
  External VOID Params are omitted only from effective function arguments,
  not the protocol frame. Selected typename identities use the ordinary
  held/stored capture lifetime and recheck current cast authority on reanalysis.
- **Bound operands** keep their position, usage and properties through
  conversion instead of being rebound.
- **PL assignment** keeps the reached lexical frame and full destination Usage.
  The ordinary SELECT binder injects leaf conversion and semantic-datum mutation
  before set-returning lowering and final dependency collection. Bounds remain
  in the per-row projection; empty target sets do not evaluate them. Assignment
  selection distinguishes an absent path from a cast failure, without changing
  SQL assignment policy.
- **Operator diagnostics** reuse `functions.ComparisonError` and the visible
  type-name formatter `DiagnosticTypeName`.
- **Shared operator spellings** select catalog arity and formal types before
  provider dispatch. Geometric alignment spellings, including bar-containing
  operators also recognized by bitwise or JSONB spelling probes, never take
  the spelling-only builtin shortcut.
- **Internal CHAR literals.** Canonical catalog SQL may annotate a direct string
  or NULL literal as `"char"`. Binding uses the existing textual-input codec
  path for those annotations only; parameters, general CHAR casts and column
  storage do not gain admission.

## What stays in the engine

Search-path wrappers (`schema_path.go`) and `PathInput` (reads `search_path`
under the session settings mutex only for unqualified resolution); the binder
construction point (`NewExpressionBinder`), the host (`BindingHost`,
`analysis_host.go`), the `describe*` wrappers and authority adapters
(`analysis_authority.go`); DDL that drives its own binder (rule/policy
conditions, defaults, checks, index and partition expressions); `expectedOf`,
`signatureOf`; rule program execution and admission.

## Testing

Pure selected aggregate-mode invariants use `RUN='^TestAggregateMovingSelection$'`;
the public SQL log witnesses live in `reference/aggregates`.

Binder tests need an engine host, so most live in `internal/engine` or, for
query and DML statements, in `internal/engine/core`, and name `analyze.Binder`
directly; package tests here cover the pure seams, among them the query alias,
CTE, row-mark and set-operation binding units. Public binding behavior belongs in
`reference/`; follow [docs/TESTING.md](../../../docs/TESTING.md). To find engine
units that build bindings, grep `internal/engine/*_test.go` and
`internal/engine/core/` for `Describe(` and the payload names.

- Package seams: `RUN='^Test(SQLCallableParameterMapping|RelationNamespaceShape|InsertConflictClone|ExpectedCheckLifetime|CheckQuerySources|CheckBindingDelete|DomainIdentityCaptureRetirement)'`
- Source identity binding and lexical frame semantics: `RUN='^TestQueryTableOID'`
- Aggregate capture purpose/synthetic builtin scope:
  `RUN='^TestRoutineAggregateDependencyPurpose$'`.
- Exact SQL routine grouped metadata and immutable rewrites:
  `RUN='^TestSQLRoutineGroupProperties$'`.
- Callable projection, default/variadic mapping and candidate ownership:
  `RUN='^TestSQLCallable'`, also with `RACE=1`.
- CALL actual set-placement admission: `RUN='^TestCallArgumentSetAdmission$'`.
- Query binding units: `RUN='^TestQuery(Aliases|Values|CTE|Lock|Set)'`
- Host, slots and authorities (engine): `RUN='^Test(AnalysisHost|ExpectedOfCopiesHeldInputs|CapturedInputsRetargetOnlyTheirSinks|BindingSlotsKeepNilAbsent)'`
- Search path and type names (engine): `RUN='^Test(SearchPath|RegTSProviderResolution|DomainIdentity)'`
- DML binding: `RUN='^TestReference(InsertConflict|InsertSelect|InsertReturning|Merge)'`
- Search path and type names: `RUN='^TestReference(SearchPath|SchemaSearchPath|RegtypeNames|DomainPreparedLifecycle)'`
- Casts, routine parameters and SQL/JSON: `RUN='^TestReference(CastDefinition|RoutineParameters|JSONTable|CopyFilter)'`
