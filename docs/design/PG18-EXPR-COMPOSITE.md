# PG18-EXPR-COMPOSITE

## Scope and precedence

This design extends the existing concrete named-composite codec and scalar/query
architecture. It supersedes earlier notes excluding ROW construction and field
operations, but not their comparison/key, generic RECORD storage,
constrained-domain or new composite-DDL exclusions. Composite-array and attribute-DDL
exclusions are superseded by the current TYPES.md evolution contract. MERGE RETURNING, MERGE partial
INSERT and generic multi-column SET retain existing boundaries. Upstream INSERT
RETURNING is preserved, not implemented here: its side-aware column/named-row
provider remains the authority for potentially absent OLD tuples. No routine
provider, adapter-local conversion or new catalog identity is added.

## PostgreSQL18 evidence

The shared untagged `reference/types/composite_expr_*_test.go` definitions specify
independent direct/prepared results, metadata and complete portable diagnostics.
The root recorded pinned PostgreSQL18 oracle and native-red checkpoints before
production, and separately released the whole-row absence fix after its ten
original cases passed pinned mode and failed native mode. Later additions require
fresh selected proof in both modes; this document is not a passing-check claim.
PostgreSQL18 parse_expr/parse_func whole-row and member lookup, parse_target
expansion/assignment, and rowtypes input/output ground the implementation.
The local newer source checkout is research-only, not the release oracle.

Important oracle decisions: `(1).num_nulls` is a genuine existing scalar provider
fallback and returns integer0; a field called num_nulls wins dot lookup while the
builtin wins ordinary function notation. All constructor children are analyzed
and constant-preflighted even if selected-field runtime demand avoids a sibling.
lib/pq precancellation does not establish native no-publication semantics;
native canceled-context guarantees remain separately specified. Actual server
cancellation uses the existing deterministic relation-lock barrier.

## Types and demand

`sqltype.RecordShape` creates an immutable occurrence descriptor with SQL OID2249,
associated lookup-only array2287 and database name RECORD. Distinct anonymous
shapes may share that SQL identity, including distinct nested shapes, without
sharing catalog generation or pointer ownership. Anonymous expressions permit
1664 fields; named catalog/storage composites retain1600. Exact named identity,
member typmods/collations and domain wrappers remain authoritative.
Structural assignment requires actual anonymous RECORD identity, not merely a
`plan.RowExpr` implementation node. Both raw rebind and rawless member rebuilding
are guarded; named/domain operands use normal destination/conditional coercion.
Projected INSERT SELECT RECORD cannot recover direct constructor/whole-source
conversion authority, including typed derived rows. Direct UPDATE/INSERT VALUES
remain contextual; anonymous missing-source UPDATE reconstructs present NULL
members, whereas compatible named/base-domain conversion retains whole NULL.

The existing binder constructs `plan.RowExpr` and `plan.FieldExpr`. A named
row destination types each child through ordinary coercion edges. Every child
is bound/finalized before final arity/coercion validation. A field stores the
stable attribute and current dense projection index. Direct constructor field
execution evaluates only its demanded child; general expansion evaluates the
computed base independently for each projected member. Ordinary preflight,
volatility, dependencies, grouping, retagging and lexical visitors cover both
nodes. Nonrecursive NULL tests inspect immediate fields and preserve mixed and
zero-field behavior. Composite ordinary comparisons fail closed even for NULL
and domain-wrapped values, without changing structural row-subquery consumers.

Stored constructor-field admission first simplifies/preflights the complete
base, then reduces only a genuine raw constructor with a stable matching
attribute and exact child/member/result Usage. `plannedConditionalResult`
retains conservative analyzed metadata around the selected physical provider.
Generated admission and index admission both invoke that existing simplifier;
their original definitions retain dependencies, ACLs and rendering authority.
Unused immutable errors precede pruning, and selected volatility remains
rejected. No source/presence/transition whole row is a constructor candidate.

Existing function-style casts retain their written callee identity separately
from canonical target type names. Dedicated builtin/keyword and failure-only
format argument contexts precede otherwise-unselected call admission. Binding
retains the native unsupported-function boundary for unresolved scalar calls;
typed composite arguments may resolve a field or report an ordinary missing
signature. An eligible call owns one scoped pre-coercion composite operand and
one identity-marked signature error. Providers keep their analysis fences and
common coercion checks; fallback never rebinds an argument or intercepts a
child's missing-function error. Nested calls save and restore that scoped value.

## Namespace and presence authority

Whole-row binding follows visible source/alias and correlated query frames,
never a second name-based source catalog. Direct whole-row member projection
uses exposed namespace labels, including alias column lists and ambiguity;
arbitrary computed named-composite projection uses descriptor member names.
The named row type identity is unchanged by those exposed labels.

Scalar and scalar-star correlated RETURNING routes reuse the saved
queryBindingScope returning/relation/frame/allowed authority after nearer column
and source lookup. Child/grandchild whole values retain transition wrappers and
DELETE NEW whole-NULL provenance; direct whole member/functional projections
neutralize the side wrapper to read the default output row. Explicit aliases
mask defaults across the same lexical frames. INSERT routes delegate to their
existing named-row provider, retaining dynamic OLD absence and logical targets.

The admitted function producer declares scalar-alias authority for a single
nonordinal call, including ROWS FROM(single call); alias binding preserves that
marker. Alias-value binding returns its existing column in the resolved frame
before record shaping or presence demand. List expansion remains a namespace
row solely for exposed columns and explicit ROW construction. Ordinality or
multiple calls is a genuine tuple and uses the ordinary source-presence owner.
Scalar-star capture, closure validation and rendering reuse the existing
viewColumn frame/slot identity. Frozen constructor-list expansion resolves
captured attributes through namespace column slots, so scalar f expands as
ROW(f.v), never ROW((f.*).v). No width/NULL-cell heuristic, synthetic type or
new evaluator is added.

Potentially null-extended demanded source occurrences receive dense frame slots
through their existing bound source/VALUES/derived/join/function-tuple node.
Proven-present or
undemanded sources allocate no presence metadata. `querySource` stores a once-
built slot lookup and packed immutable flags; joins append those flags beside
existing materialized occurrences, without per-row maps/objects. EvalContext
borrows flags and follows Outer to the actual frame. Missing demanded authority
is an invariant error, never guessed from NULL cells, width or row versions.

An emitted named, VALUES, derived, materialized or join-alias tuple is present,
even with all NULL fields or zero width. Only enclosing null-extension makes
that occurrence absent. Filters, ordering, bounds, projection, DISTINCT ON,
window overlays, aggregates, correlation and UPDATE/MERGE source evaluation
preserve borrowed occurrence flags. True derived/materialized output establishes
its own presence. Lock rechecks use current staged occurrence metadata; this
demand does not force locks, versions or physical provenance caching.

## Ordered assignment paths

The parser retains one ordered sequence of member/subscript steps rather than
separate suffix lists. Existing array assignment plans compose member containers
with array elements/slices and recurse to the typed leaf. INSERT starts partial
containers from typed NULL rather than defaults. UPDATE/MERGE evaluate every RHS
and bound against the original target/source frame while writes rebuild private
containers in SQL order. Each boundary reapplies its existing domain coercion.
Implicit old-container access is not SELECT authority; explicit expressions are.
The existing completed-row generation, constraints, uniqueness, ACL and atomic
publication owners remain unchanged.
Both VALUES width binders choose safe expanded-output diagnostic provenance:
an empty later list has no Position, and a nonempty later list retains its first
expression position. No empty-list indexing, panic recovery or syntax exclusion.
All recursive target consumers follow target.next. UPDATE/MERGE virtual demand
uses one nil-safe complete-path helper and retains provenance only when demanded.
Mutation-join validation visits each segment's existing execution-owned children
without evaluating them; empty inputs still expose planned errors. View lowering
uses the existing expression mapper's changed bit, copying only changed target
ancestry and the first changed target slice, including logical/physical source
offsets. Preflight and application retain their existing recursive ownership.
Partial target diagnostics use the binding authority's diagnosticTypeName for
both source and destination, including nonarray rejection. Only error formatting
borrows the binder; executable target plans never retain it or a live catalog.
Assignment-head Position is retained for coercion failures. Nonarray rejection
uses the effective container expression location: plain column targets retain
their head, while a selected member has no Position, matching PostgreSQL.
The recursive binder tracks these two locations independently; executable target
plans retain only their existing assignment-head position. SQLSTATE, Hint and
failure phase remain unchanged.
Anonymous2249 shapes do not allocate or consult named-member uniqueness maps;
text/name validation, stable attributes and descriptor coherence still apply.
The existing generic function source inherits scalar-alias metadata and default
single-call exposed-column naming through the same alias binder as string sources.
Column precedence still applies to ordinal namespace lookup. Correlated right
presence topology is compiled from demanded bound slots before consumers borrow
it, without opening or evaluating the source. Deferred and opened layouts share
one packed-slot authority. Invocation frames borrow actual local/inherited
occurrence flags independently of the function's emitted occurrence flag.

## Frozen definitions and lifetime

The existing occurrence-path capture map has explicit selection, expansion,
dot-builtin and whole-row kinds. Each stores exact base Usage and ordered stable
attributes, with the genuine null-count kind or frozen namespace/qualifier/frame
handle only where required. Empty expansion and whole-row captures retain an
explicit kind even with zero attributes. The map stores no live `expr.Expr` or
catalog root. Raw expansion bases are captured once despite physical lowering.

Rendering resolves stable members through validated current descriptors and
emits explicit selections in captured order, never a wildcard that could widen.
Whole-row references render actual alias.* so a same-named scalar column cannot
capture them; exposed alias labels are resolved from the actual rendered source.
Dot-builtin frozen-query rendering emits the canonical pg_catalog null-count
call; stored-default replay preserves admitted dot notation rather than
widening generic DEFAULT capability.
Clone/remap/reownership covers runtime children, retained expansion analysis,
record closures, member collations and capture usages. Existing catalog/type
lifetime fences reject replacement retargeting. Eager held values remain owned.
Actual native emitted SQL is replayed on PostgreSQL in the controls definition,
with two native replacement cycles and subsequent writes.

## Selected proof owned by root

Each command has an external900-second deadline. Run each reference selector
natively and with `REFERENCE=1`; readiness is not verification:

- `make check RUN='^TestReferenceCompositeExpr(Constructors|ConstructorCoercion|ConstructorDemand|Fields|Metadata|NullSemantics)$'`
- `make check RUN='^TestReferenceCompositeExpr(ScopeAmbiguity|FunctionalNotation|Expansion|ValuesAndInsertExpansion|NestedStars|Returning|Diagnostics)$'`
- `make check RUN='^TestReferenceCompositeExpr(Assignments|MixedArrayFields|MergeUpdate|AssignmentDiagnostics|AtomicityAndACL|Boundaries)$'`
- `make check RUN='^TestReferenceCompositeExpr(StoredConsumers|ControlsAndLifetime|ControlRejection|FeatureSmoke)$'`
- `make check RUN='^TestReferenceCompositeExprWholeRowAbsence$'`
- `make check RUN='^TestReferenceMergeUpdateFields$'`
- `make check RUN='^TestReferenceCompositeExpr(StoredFieldPruning|CorrelatedReturning|FunctionAlias)$'`
- `make check RUN='^TestReferenceCompositeExpr(ZeroWidthValues|NamedAssignmentIdentity)$'`
- `make check RUN='^TestReferenceCompositeExpr(VirtualBoundUpdate|VirtualBoundMerge|ViewTargetPaths|JoinTargetPaths)$'`
- `make check RUN='^TestReferenceCompositeExprNamedTargetDiagnostics$'`
- `make check RUN='^TestReferenceCompositeExprFunctionSourceCutover$'`
- `make check RUN='^TestReferenceCompositeExprFunctionSourceRestore$'`
- `make check RUN='^TestCompositeExpr(RecordShape|FieldAndNull|AssignmentPaths|VisitorsAndRetag|Parser)$'`
- `make check RACE=1 RUN='^TestReferenceCompositeExpr(AtomicityAndACL|ControlsAndLifetime|ControlRejection)$'`

The root owns generation/formatting, full Docker-free `make quickcheck`, review,
integration and post-merge finalcheck. No subagent checks are represented here.
The standalone session artifact uses only public stuntgres/standard-library APIs
and prints `composite expressions public smoke: PASS` only after all assertions.
