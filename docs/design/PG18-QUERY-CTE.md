# PG18-QUERY-CTE: SELECT WITH definitions and materialization

## Scope and PostgreSQL authority

This change admits nonrecursive SELECT WITH definitions and their existing SELECT
consumers. It does not admit recursive/SEARCH/CYCLE, modifying bodies, top-level
WITH DML, standalone VALUES/TABLE bodies or LATERAL. Existing VALUES set operands
remain query operands, not a new CTE-body surface.

The contracts are grounded in PostgreSQL 18 [WITH SELECT](https://www.postgresql.org/docs/18/queries-with.html#QUERIES-WITH-SELECT),
[materialization](https://www.postgresql.org/docs/18/queries-with.html#QUERIES-WITH-CTE-MATERIALIZATION),
and [SELECT](https://www.postgresql.org/docs/18/sql-select.html). The pinned test
oracle remains the repository's PostgreSQL 18.0 UTF8/C image; newer local source
checkouts are research aids, not release evidence.

## Syntax, lexical visibility and the sole binder

`Select.With` retains positioned definition names, prefix aliases, SELECT bodies
and the three materialization choices. Grammar uses the existing query-select
entrypoint, so scalar/derived/set/stored/query-consuming forms share the same AST.
The lexer/parser still consume the complete input and preserve absolute character
locations. Excluded families retain honest unsupported/syntax boundaries rather
than pretending PostgreSQL rejects their valid syntax.

Parentheses can decorate the same SELECT rather than create a new FROM/query
namespace. Attaching a second WITH to that SELECT raises42601 `multiple WITH
clauses not allowed` at the outer WITH token, as observed in the pinned
parenthesized regression. The grammar retains that token's position and rejects
the overwrite; it does not synthesize a derived wrapper that would hide qualified
ORDER BY or physical OF-table row marks. A single WITH through parentheses keeps
the original query namespace and decorations.

`VisitQuerySources` classifies a reference against one lexical WITH environment.
Bodies see enclosing and earlier definitions; pending self/later local names are
only diagnostic evidence. Qualified names are physical. Duplicate local names
are prechecked before admitting any body. Physical acquisition, binding and
row-mark admission therefore agree on actual table occurrences; virtual names
never receive a physical occurrence, ObjectRef, OID, ACL or relation lock.

The existing expression binder owns parameters, types, source identities and
static dependencies. Its separate WITH environment is restored at query exit.
Each immutable `cteDefinition` records the typed canonical query, owning frame,
reference count, hint, volatility and diagnostic position. An earlier definition
is published only after its body has bound successfully. Aliases alter only the
occurrence namespace, not the canonical definition or another alias's outputs.

## Static retention is not execution reachability

Definition subplans are retained as explicitly analysis-only `subqueryExpression`
edges in the existing project's `analysisExpressions`. Existing static graph
walkers continue to see frames, expressions, dependencies and stored syntax.
Executable walkers exclude those edges, but still visit every target and nested
projected query of a referenced definition. Folding eligibility is a separate
static traversal: PostgreSQL inspects even nested WITH definitions that remain
runtime-unused. Their VOLATILE calls can fence the enclosing CTE without being
executed or adding executable permissions. The pinned enclosing division witness
raises22012 under default and NOT MATERIALIZED. The independently expected next
sequence draw is1, proving the nested producer remains unused. Demand properties
and source/ACL reachability remain execution-specific.

Physical leaves first bound inside definitions are marked analysis-only. The
reachable executable graph activates referenced leaves after complete statement
analysis. Authorization and source capture omit unactivated leaves, without
omitting their name/type resolution or stored dependencies. This is static
reference reachability, not produced-row count: a referenced LIMIT0 or WHERE FALSE
still requires source access. The observed PostgreSQL id-only-grant control allows
an unused v-reading definition with SELECT7, but rejects a referenced v reader.

## Derived graph reuse and genuine folding

The closed logical-node convention is retained. CTE occurrences extend the
existing `derivedNode` with immutable definition identity, original table syntax,
virtual namespace ordinal, scanning frame and positional output demand.
Ordinary derived queries keep their existing behavior. Materialized occurrences
open a shared CTE reader; eligible folded occurrences use independent query graph
paths and the existing derived output-frame adapter.

Eligibility is frozen only after all identity-based references are known:
MATERIALIZED forces sharing; VOLATILE definitions never fold; default eligible
single-reference definitions fold; NOT MATERIALIZED permits eligible repeated
references. STABLE is not VOLATILE. Explicit row locks in a definition retain their
own semantics and prevent unsafe folding.

Folding is not merely reopening a projected body. `query_cte_fold.go` substitutes
occurrence-local parent columns with the body's typed projection expressions and
places safe restrictions below that projection. Inner-join alias-local WHERE/ON
conjuncts remain independent. Cross-occurrence and nullable-side movement is not
performed. LIMIT/OFFSET and aggregate/window-dependent restrictions retain their
evaluation boundaries. Raw DISTINCT/grouping/set keys can move through their
existing nodes; set restrictions are substituted into each typed arm. A raw
window input-column restriction filters completed window rows, retaining original
row_number/peer/partition membership. Sort alone is not a projection fence.
Each modified path is copied; the canonical definition, schema and other aliases
remain immutable. Unrequested folded output targets are not needlessly projected.

## Invocation-owned shared producer

`cteInvocation` is an execution-private owner chain keyed by the defining lexical
frame. Every owning query invocation gets a fresh local owner; child scalar,
derived and set states inherit enclosing owners. A correlated local WITH therefore
has a new producer per invocation, whereas separate scalar readers of an enclosing
WITH find the same producer. No mutable producer is stored in a plan, prepared
handle, catalog descriptor or global map.

Owner setup and close wrapping belong to the universal project-node dispatcher,
not only the SELECT/INSERT row-mark-settlement entrypoint. Stored view source
bridges therefore establish the same fresh invocation owner when opening their
project directly. Existing inherited row-mark settlement remains unchanged;
there is no second stored-query authority or snapshot owner.

A filter proven false or BOOL NULL by the existing literal/imported-parameter
proof returns a typed empty stage before opening its child only when no mandatory
physical consuming frontier is reachable. This proof uses the current execution
frame, not Prepare-time values; arbitrary predicates retain row-by-row evaluation.
The allocation-free inspection beside the pending-mark walker carries an owner
fact down the same branch, expands stored sources with their own nested inputs,
and reads effective captured physical marks, ignoring identity-only manifests.
It traverses consumed marks beneath aggregate/sort/distinct/set/limit/window owners,
not just marks still exposed on output. A CTE-derived scan is opaque for every
hint; unused analysis definitions are not executable demand.

When a mandatory physical frontier exists, the filter retains its original
child/open/owner-settlement path rather than acquiring or draining anything itself.
Thus native stored marked aggregates retain their deliberate eager-lock contract,
while settlement-free dead CTE consumers still leave their producers unopened.
Schema, cancellation, parameter errors and static referenced-source authority
remain independent of row demand.

Each definition has at most one producer in its owner. It borrows the owning
statement's already captured sources, authority and outer frame; it does not
resolve sources, acquire a second snapshot or recapture authority. First demand
opens one existing project stage. The producer appends typed rows to a flat buffer
and records cardinality separately, including zero-column rows. Readers keep
independent replay positions and only extend the shared frontier when necessary.
Closing a reader does not close the producer or erase rows required by a later
reader. EOF closes the child once while retaining replay rows until owner release.
Owners close remaining producers in registration order.

Cancellation is checked both before replay and frontier extension and before a
row is published into the spool. A producer retains its original failure, closes
its child once, and never lets a cleanup error replace that primary failure. A
required Close error on an otherwise-successful path still vetoes success.
Final public Rows retain only eagerly finalized values and immutable schema, not
owners, unfinished stages or borrowed statement authority.

## Stored graphs and physical lineage

Stored views, materialized definitions, CTAS/SELECT INTO, cursors and INSERT SELECT
reuse the same graph and command contracts. Stored source manifests retain
analysis-only flags; invocation state is never stored. Rendering consults the
original virtual table occurrences and canonical definition syntax, preserving
WITH names, prefix aliases and hints while remapping real-object names/dependencies.
Native Dump/Restore persists real objects and stored definitions, never transient
CTE objects or producer results.

Nested stored remapping memoizes by `*projectNode` graph identity, not lexical frame
number. Distinct folded roots may share a lexical evaluation coordinate while
carrying different occurrence predicates; they must not collapse. Identical
canonical roots still share. A separate immutable-definition memo preserves CTE
identity and remaps its canonical body independently of the first encountered
folded occurrence. Physical row-mark/absence/recheck visitors distinguish virtual
boundaries: enclosing row marks do not propagate through a CTE scan, and a marked
physical sibling's recheck retains the CTE's frozen tuple.
Explicit OF targets retain their virtual-CTE kind independently of physical
occurrence lists. Naming a CTE occurrence or alias in any admitted locking
strength raises positioned0A000, while untargeted locking ignores it and qualified
physical same-name entries remain ordinary lock targets.

## Evidence definitions and verification ownership

Public independently expected direct/prepared SQL witnesses live exclusively in
`reference/query_core/query_cte_*_test.go`. They cover bags/NULLs/duplicate and
zero-width rows, lexical scopes, real folding versus fences, volatility, lazy
replay, correlated ownership, complete diagnostics, authority/snapshots, stored
consumers, native controls and feature smoke. `pg_class` and transient-name checks
are shared; `pg_type` is a PostgreSQL supplement because native has no pg_type
provider. No adjacent catalog provider is introduced.

Parser units exercise positioned ASTs, all SELECT consumers, coherent lexical
source classification, duplicate-before-admission precedence and whole input.
Engine function units exercise frontier interleaving, early-reader lifetime,
NULL/zero-width spools, cancellation replay, sticky primary and Close failures,
invocation isolation/inheritance, static whole-definition volatility versus
executable demand, direct project-dispatch ownership, graph memo identity,
frame-safe restriction placement and original immutability. The obsolete parser
INSERT-WITH rejection is removed; it is covered positively by SELECT-consumer
units and public stored INSERT SELECT witnesses.
Review regressions `TestReferenceQueryCTERowLocks`,
`TestReferenceQueryCTEDeadDemand` and `TestReferenceQueryCTEPhysicalFrontier`
independently specify explicit target diagnostics, no-draw/no-division gates,
per-Execute BOOL parameters, opaque lock bodies and the native physical sibling
frontier. Existing aggregate composition regressions retain their original native
grants and intentional PostgreSQL no-lock control. Function units exercise target
kind, pre-child producer absence, branch-local mandatory-owner detection, stored
occurrence mapping, identity-only/CTE opacity, errors/cancellation and refusal to
pre-evaluate arbitrary predicates.

The root generates the checked-in parser and runs selected native and pinned modes,
units/race, the session's standalone public Go smoke, Docker-free whole-project
quickcheck, review and post-integration finalcheck. This document describes design
and test definitions, not unobserved verification success. Exact selected commands
and smoke lifecycle are documented in [TESTING](../TESTING.md#select-cte-verification).
