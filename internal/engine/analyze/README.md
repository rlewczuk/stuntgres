# internal/engine/analyze

Statement binding: the expression and statement binder (`Binder`), `Describe`,
which binds one statement into a `Binding`, the `Host` interface through which
the binder reaches engine state, the capture groups that views, rules, policies
and prepared statements read, and pure analysis helpers shared with engine DDL.
Rule rewriting, view-write lowering and RLS walkers are in
[`view`](../view/README.md); ON CONFLICT and MERGE execution state is in
[`dml`](../dml/README.md).

Reached PL expressions use the ordinary binder with private lexical slots and
current RECORD shapes; PL `$n` counts all formals, while SQL stays input-only.
Assignment tries declared/ordinary assignment casts before descriptor I/O fallback.

## Imports

Layer L10 of [ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture).
It imports `plan`, `exec`, `catalog`, `storage`, `expr`, `functions`, `ids`,
`lock`, `access`, `authorization`, `pgcatalog`, `proc`, `routine`, `sequence`,
`typedef`, `extension`, `parser`, `sqlstate`, `sqltype` and the standard
library, and never the root engine or `session`. The engine, the root driver
(for `ParameterInfo`) and the upper feature packages (`stored`, `regobj`,
`view`, `dml`, `table`, `index`, `partition`, `role`, `control`) import it.

## What it owns

- **`Binder`** (`expression.go`, `*_bind.go`, `binder_groups.go`): per-statement
  binder state in field groups. `NewBinder` builds one over a `Host`; `Describe`
  (`describe_bind.go`) binds a statement. `functions` reaches it through
  `functions.Binder`.
- **`Host`** (`host.go`): the engine services the binder calls (name and type
  resolution, privileges, definition reads, captures, stored views). The engine
  implements it as `bindingHost`.
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
  operands for capture and replay. Owned aggregates keep their stronger
  source-call normalization when operands replace binder-local slots.
  `FunctionCast` and its selected `FunctionExpr` share the key, so a type-named
  SQL routine remains checked rather than reverting to builtin cast selection;
  standard/postfix cast occurrences remain separate. Guarded normalization
  follows substituted roots, including cast-to-call lowering, and children
  without mutating shared syntax. Proof:
  `make check RUN='^(TestCreateAggregateStoredSyntaxCastOwnership|TestStoredProviderSyntax(GeneratedOccurrences|SubstitutedRoot|OwnedAggregateOccurrence|FunctionCastOccurrence)|TestSQLRoutineFrozenPresentation)$'`.
  Builtin aggregate/window plans retain original checked declarations from the
  binding snapshot alongside refs. Shared `bool_and`/`every` computation retains
  separate declaration/ref pairs and EXECUTE proofs; COUNT star/input identities
  are separate too. Literal capture selects this occurrence inventory, not a new
  spelling lookup. Frozen views/standard bodies prove originals after relabel,
  relocation or owner COW; direct source resolves current public identity.
  The same capture pair belongs to DISTINCT (including ON), GROUP BY, set and
  recursive CYCLE-mark grouping keys, and window partition/peer equality.
  Equality selection keeps the checked declaration after any existing coercion;
  it does not perform a later lookup by the routine's mutable name.
- **`Binding`** (`binding*.go`, `payloads.go`): the statement header, the kind
  payloads (`InsertBinding`, `UpdateBinding`, `MergeBinding`, ...) and
  `ParameterInfo`.
- **Capture groups** (`binding_groups.go`, `captures.go`): `ViewCapture`
  (keyed by `ViewLiteralPath`), `PreparedCaptures` (temporal, type, sequence and
  regtype captures a prepared statement re-resolves), `RuleBinding`,
  `DefaultOrigin`. `domain_casts.go` swaps a held description's domain cast
  group without mutating a shared map.
- **`Authority`** (`authority.go`): the catalog and search-path authority a
  binding runs under; the binder reads only its catalog and hands it back to
  the host.
- **`Expected`** (`expected.go`, `signature.go`): what a held description gives
  re-analysis; `CheckSignature` is the 0A000 cached-plan check and
  `Expected.CheckLifetime` applies it to a replaced catalog.
- **Name lookup** (`lookup.go`, `search_path.go`, `type_name_resolution.go`):
  `LookupPath`, `EffectivePath`, `CreationNamespace`, ... over one by-value
  `PathInput[E]`; `ResolveTypeName` takes a `TypeNameOperation` because the
  package never imports `session`.
- **Captured input retargeting** (`captured_inputs.go`): `IdentifierTarget` and
  the `*Retarget` interfaces.
- **Privilege walkers** (`binding_privileges.go`): `CheckBinding` and
  `CheckQuerySources` check target, column reads and query sources, each source
  under its own principal, deciding each object through `access`.
- **Row-lock admission** (`lock_admission.go`): `NewLockAdmission` computes a
  `LockAdmission` from syntax before binding; the engine holds one per
  statement scope.
- **Query namespaces and helpers** (`namespace.go`, `query_*.go`, ...):
  `QueryNamespace`/`RelationNamespace` for non-query consumers (checks,
  defaults, generated columns, index, partition and policy expressions), plan
  walks, coercion properties, collation derivation (`CollatedInfo`) and error
  positions. They read only their arguments.
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
  `Host`. Binding runs with `Engine.mu` held by the caller; host methods never
  take or release it.
- **Authorities.** Only the host interprets a binding authority; engine code
  recovers stored authorities with `authorizationOf`, which rejects binding
  authorities. A retarget returns a copy; a nil sink keeps the resolver's sink.
- **Preparation is effect-free.** Describing a statement runs no producers,
  defaults, sequences or writes. Parameter types come from SQL destinations,
  never Go argument kinds. Analyzed trees and their dependencies are immutable;
  conditional planning keeps every analyzed dependency and authorization even
  when execution removes a branch.
- **Frozen syntax keys.** Every capture writer and reader keys an occurrence by
  the node's own `Pos` (a postfix cast's `::`), never `SourcePosition`.

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
- **Bound operands** keep their position, usage and properties through
  conversion instead of being rebound.
- **Operator diagnostics** reuse `functions.ComparisonError` and the visible
  type-name formatter `DiagnosticTypeName`.
- **Shared operator spellings** select catalog arity and formal types before
  provider dispatch. Geometric alignment spellings, including bar-containing
  operators also recognized by bitwise or JSONB spelling probes, never take
  the spelling-only builtin shortcut.

## What stays in the engine

Search-path wrappers (`schema_path.go`) and `pathInput` (reads `search_path`
under the session settings mutex only for unqualified resolution); the binder
construction point (`newExpressionBinder`), the host (`bindingHost`,
`analysis_host.go`), the `describe*` wrappers and authority adapters
(`analysis_authority.go`); DDL that drives its own binder (rule/policy
conditions, defaults, checks, index and partition expressions); `expectedOf`,
`signatureOf`; rule program execution and admission.

## Testing

Binder tests need an engine host, so most live in `internal/engine` and name
`analyze.Binder` directly; package tests here cover the pure seams. Public
binding behavior belongs in `reference/`; follow
[docs/TESTING.md](../../../docs/TESTING.md). To find engine units that build
bindings, grep `internal/engine/*_test.go` for `describe(` and the payload names.

- Package seams: `RUN='^Test(SQLCallableParameterMapping|RelationNamespaceShape|InsertConflictClone|ExpectedCheckLifetime|CheckQuerySources|CheckBindingDelete|DomainIdentityCaptureRetirement)'`
- Host, slots and authorities (engine): `RUN='^Test(AnalysisHost|ExpectedOfCopiesHeldInputs|CapturedInputsRetargetOnlyTheirSinks|BindingSlotsKeepNilAbsent)'`
- Search path and type names (engine): `RUN='^Test(SearchPath|RegTSProviderResolution|DomainIdentity)'`
- DML binding: `RUN='^TestReference(InsertConflict|InsertSelect|InsertReturning|Merge)'`
- Search path and type names: `RUN='^TestReference(SearchPath|SchemaSearchPath|RegtypeNames|DomainPreparedLifecycle)'`
- Casts, routine parameters and SQL/JSON: `RUN='^TestReference(CastDefinition|RoutineParameters|JSONTable|CopyFilter)'`
