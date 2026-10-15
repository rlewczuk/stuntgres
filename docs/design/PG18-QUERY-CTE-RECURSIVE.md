# Recursive common table expressions

## Scope and authority

This part extends the existing SELECT CTE owner with `WITH RECURSIVE` anchor/term
`UNION [ALL | DISTINCT]`, physical worktable iteration, PostgreSQL restrictions,
public metadata and existing prepared/stored consumers. SEARCH/CYCLE, recursive
modifying definitions, CREATE RECURSIVE VIEW sugar, new arithmetic/equality/type
providers, general LATERAL and spill/optimizer machinery are not included.
The original [SELECT CTE design](PG18-QUERY-CTE.md) remains historical; this design
uses the existing [query execution architecture](PG18-ARCH-QUERY-EXECUTION.md).
The later [CYCLE extension](PG18-QUERY-CTE-RECURSIVE-CYCLE.md) adds bounded
branch-local cycle detection through these same owners; the exclusions above
describe this prerequisite's original scope.

Release authority is [PostgreSQL18 recursive WITH](https://www.postgresql.org/docs/18/queries-with.html#QUERIES-WITH-RECURSIVE),
[REL_18_0 parse_cte.c](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/parser/parse_cte.c)
and [nodeRecursiveunion.c](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/executor/nodeRecursiveunion.c).
The public scenarios use the repository's pinned18.0 UTF8/C oracle; local newer
sources are research aids, not release diagnostic evidence.

## Lexical binding and opaque identity

The modifier is retained by the existing WITH owners. The sole parser source
walker resolves CTE definition identities, including nested shadows and qualified
physical names. Recursive groups expose all sibling names, derive dependencies,
and topologically bind them; self edges identify true recursion, distinct-sibling
cycles fail. Original textual order remains available for rendering and diagnostics.
Unused definitions retain static analysis. No self/later virtual name acquires a
physical relation, OID, lock or independent privilege authority.

`bindRecursiveSet` binds the anchor as an ordinary finalized SELECT: unknown outputs
become TEXT before exposure. Its immutable schema supplies self-reference names,
including legal explicit alias prefixes. Existing set common-type/coercion/grouping
recipes analyze the recursive arm. Overall descriptor identity, typmod and collation
must match anchor exposure; widening cannot silently change recursive input types.
Collation indeterminacy is expression provenance, not a zero Usage collation ID;
the anchor/overall comparison preserves that distinction and renders an
indeterminate collation as PostgreSQL's `(null)`.

A `worktableScanNode` carries a definition identity and typed virtual scan layout,
not a child pointing at the canonical query. `recursiveUnionNode` owns the two real
arm projects and existing set coercion/grouping recipes. Structural visitors remain
acyclic and traverse both real arms once per occurrence. The self identity remains
symbolic in stored rendering and never becomes a persistent catalog dependency.
Genuine recursion cannot fold, including under NOT MATERIALIZED; nonrecursive
items in a recursive group retain their existing materialization semantics.

## Recursive legality and diagnostic order

For a genuinely recursive item, modifying-statement and top-UNION form checks
precede the body WITH check. The body's WITH definition list is then walked in
source order under forced subquery context, before ORDER/OFFSET/LIMIT/locking or
either UNION arm. The list is not exposed as a new enclosing namespace during
this release-specific precheck; nested WITH groups inside each definition body
still resolve their own lexical identities. Harmless body WITH clauses and local
shadows remain legal, and qualified physical names never become self references.

Expression SubLinks always override the enclosing legality context with
subquery, including inside anchors, nullable join sides and prohibited set
operands. Legality visits the subselect before its test expression and restores
the enclosing context before that expression or an error return. Thus a competing
subselect reference supplies the primary location, not the test expression's
reference. The private recursive-legality traversal flag changes only this
SubLink handling; ordinary physical-source admission and dependency discovery
keep their established Left-first order and captured identities.

## Iteration, equality and demand

Each invocation owns a `recursiveUnionStage`. Accepted tuples are retained once
in the ordinary shared producer's append-only typed buffer. Current and next
worktables contain indexes into those immutable tuples, not copied accumulated
payloads. Anchor acceptance seeds current. Each recursive scan reads current only;
when the arm is exhausted, next becomes current, the old index buffer is reused,
and the arm reopens with the same captured statement/outer/parameter environment.
An empty next generation terminates. There is no recursive Go-call iteration,
per-pass binding, source recapture or arbitrary iteration cap.

Positive-width UNION retains a seen map, using existing typed PrepareKey/Hash and
complete NULL-aware tuple equality for collisions. All output columns participate,
even if a parent/view omits them. ALL needs no grouping/seen/count structure.
PostgreSQL18's zero-column RecursiveUnion also skips hashing and retains occurrences
like ALL; occurrence counts therefore remain independent from payload length.
Projecting a positive-width recursive result to zero columns does not erase its
internal keys or change termination semantics.

External readers independently replay one producer frontier. Closing an early
reader does not discard the producer. Every held execution and correlated local
invocation starts with fresh generations, parameters, seen set and failure state.
The existing pull/limit owner stops nonblocking production at parent demand;
LIMIT0 exposes full schema without opening a producing child. Blocking sort,
aggregate or join consumers can drain input, so LIMIT outside a blocker does not
promise termination of an infinite recursion. Traversal order is not an API promise.

## Failure, consumers and persistent state

Context checks cover each pull/pass, accepted/rejected tuples, coercion, hash/equality
and replay. The existing indexed-stage/producer failure owner retains the first
error and closes opened children once, including early parent stop. Required close
failure vetoes otherwise successful publication. Native results remain detached,
finalized Rows, not live worktable/evaluator/producer pointers. Reader-stage failure
notifications propagate even cancellation detected before the next producer pull,
so siblings cannot replay a previously accepted prefix after that invocation fails.

Recursive SELECTs use the installed query consumer path for INSERT/UPDATE/DELETE,
views, materialized-view refresh, query-created tables and cursors. All physical
arms use the existing statement snapshot and authorization; LIMIT0/dead qualification
do not remove referenced privileges. Late pass failure or cancellation vetoes the
single existing statement publication, including provisional writes/table creation.
Sequence draws retain their existing nontransactional behavior.

MERGE rejects its own WITH RECURSIVE before WITH/target analysis, but a recursive
SELECT local to its derived source remains a SELECT owner. The reference witness
uses the established MERGE adapter convention: native real count, lib/pq count0
because pq does not decode MERGE command tags, plus independent exact target rows.
No new MERGE provider or command-tag layer is introduced.

Rewrite preserves immutable canonical plans and the opaque self edge. Foreign
view remapping registers project/definition identities before descending into arms
and re-owns schema/coercion/grouping descriptors coherently. Frozen frame/source
rendering retains WITH RECURSIVE and symbolic self names. Renderer slot maps use
the actual opaque worktable node identity, not the address of its embedded scan
layout; otherwise stored self-column lookup loses its frame. Output-demand pruning
cannot drop complete recursive keys. Existing prepared dependency/path/type/ACL
revalidation remains the sole owner; definitions/prepared handles contain no runtime
worktables. Dump/Reset/Restore persist symbolic definitions and real table/MV rows
with physical dependency ordering, never transient recursion state.

## Proof contract

Public acceptance is owned by the nine `TestReferenceQueryRecursiveCTE*` definitions
in `reference/query_core/`; selected commands and standalone-smoke argv are in its
[Testing section](../../reference/query_core/README.md#recursive-cte-verification).
Private parser/engine function units supplement, never replace, those shared SQL
scenarios. Root owns generation, dual-mode proof, quickcheck, smoke, review and
integration. Design/readiness claims are not verification evidence.
