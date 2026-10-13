# Native query execution contracts

[README](../README.md) · [Compatibility limits](DIALECT.md)

PostgreSQL 18 documentation is the authority for SQL syntax and standard SQL
semantics. This page records native execution ownership, lifecycle guarantees
and compatibility boundaries; it does not claim complete PostgreSQL compatibility.

## Logical query results

Finite native results are finalized eagerly before statement completion. A late
projection, sort, source-close or cancellation failure exposes no successful
prefix or cursor. Public `Rows.Next` and `Close` never resume SQL evaluation,
acquire grants or publish writes. PostgreSQL adapters can instead expose an
output prefix before a terminal `Rows.Err`; drain or close it before another
command on the same connection.

Direct-column output retains immutable captured source/index backing rather than
copying whole tables; computed output retains finalized projected values. Public
results expose stable copied values or immutable owned backing, never borrowed
execution scratch. Rows own their schema independently of the statement. Empty
results retain metadata, and zero-column results retain occurrence counts.
Physical projections preserve origin and known nullability; scalar nullability
can remain unknown. Filter and sort-only columns do not leak into visible metadata.

EOF, cancellation and iteration errors release result resources without erasing
metadata or previously exported values. Raw-driver errors remain sticky until
idempotent Close; closed Next returns EOF. Context errors remain ordinary Go
errors. Exported mutable bytes detach from internal values and other exports.
Rows remain valid across later writes, transaction completion and native
Reset/Restore, but retained snapshots grant no publication authority.

## Analysis and execution-private planning

Preparation describes inputs and outputs without running producers, defaults,
sequences or writes. Held execution imports fresh private parameter frames,
checks current authority and rebinds invalidated definitions while enforcing its
fixed result signature. Parameter types come from analysis, not Go argument kinds;
missing Go arguments are adapter/count errors, not PostgreSQL protocol errors.
Diagnostics use one-based character positions rather than byte offsets.

Conversion preflight and immutable planning run outside engine/publication locks.
Analyzed trees and conservative dependencies remain immutable; only changed
execution branches are copied. Namespace analysis precedes preflight, and exact
relation identity and current authority are revalidated afterward. Empty input
does not waive analysis, source admission, privileges or frame-only conversion
checks. Native eager preflight and Boolean evaluation do not promise PostgreSQL
optimizer demand or general short-circuit parity.

Conditional planning retains all analyzed dependencies and authorization, even
when execution removes a branch. Native parameter-dependent conversions can stay
lazy in conditional branches where pinned PostgreSQL plans them eagerly. Consumed
stored defaults have their own decisive-Boolean pruning policy; ordinary query
preflight does not inherit that policy. Query-containing right operands can be
omitted after a physical decisive left Boolean constant, without erasing the
original admission graph or skipping ordinary coercion preflight. Held parameters
are reconsidered on every execution, never stored as planning facts.

## Comparison and membership ownership

Comparison authority belongs to resolved input descriptors, separately from a
Boolean result. Scalar comparison initializes a provider only when demanded;
key binding rejects conflicting implicit collations during analysis, even for
empty input. Cross-input join equality initializes its provider at execution,
including empty inputs. Index eligibility and predicate implication use full
resolved authority and cannot optimize away incompatible comparison demand.

Scalar `IN`/`NOT IN` lists and array-expression `ANY`/`SOME`/`ALL` resolve
comparison authority against scalar elements, not the array descriptor. IN
common-type analysis includes the left operand and batches eligible non-column
items; column-containing items retain pairwise comparison analysis. All children
are analyzed and retain their authorization/dependencies even after a decisive
match. Array reductions use the existing immutable flat elements; dimensions and
lower bounds do not change truth. A whole NULL array yields NULL; empty arrays
yield ANY/SOME false and ALL true, including a NULL left operand. Other reductions
remember unknown comparisons unless a decisive true/false is reached. These
scalar forms create no query source owner or membership hash table and do not
promise operand evaluation order or new optimizer short-circuit behavior.
Binding diagnostic precedence is distinct from runtime evaluation order:
selected IN-batch right inputs are converted in source order before the left
input is finalized. Quantified comparisons establish the scalar operator's type
compatibility before converting contextual unknown literals or parameters, so a
missing operator is not hidden by an unrelated malformed array input. The final
predicate still retains the actual coerced descriptors and input collation.

Hashable uncorrelated **subquery** membership builds provider-bound RHS buckets before LHS
projection. Empty membership avoids LHS effects and probe scratch. Successful
buckets belong to the execution owner for the actual RHS plan and captured source
occurrence, not frozen expressions, views, prepared handles or a global cache.
Unchanged children retain buckets across correlated parent rescans; distinct view
aliases remain independent. Every Execute starts fresh. Correlated bodies retain
their scan route. Probes reuse owner-local scratch and partial-NULL indexes.
Borrowed stage/CTE-reader closure cannot erase ancestor-owned tables; completion,
errors and cancellation release them. Failed builds are never published.

Stored definitions preserve hidden comparison authority through dependencies,
rendering and remapping. Group-key reuse preserves the original target's collation
provenance and diagnostic location. Owner-dependent stored generation detects
relation-identity dependencies in every row-comparison operand, so native owner
remapping recomputes affected Boolean cells.

## Stored-source demand and rendering

An ordinary stored-view read maps referenced output slots before coercion
preflight. Unused nonvolatile payloads are omitted rather than evaluated because
a sibling is selected. Projection, sort and bounded-payload caches keep their full
typed layout; demand follows selected and internally read slots to scheduled
producers, including shared/transitive reads. Clause/key demand, volatile payloads,
frozen dependencies, authorization and snapshot ownership remain intact. Full
consumers and materialized refresh retain full demand.

Legality preflight and stored-source opening use the same demand-shaped root.
Nested runtime reads come from remaining executable producers, not the full frozen
read manifest. Lexical frames and physical occurrence spans distinguish repeated
aliases; captured attributes bridge those reads to current slots. Frozen manifests
retain authorization/dependency roles and analysis-only CTE edges. Pass-through
stored roots keep their original source/snapshot stage instead of receiving a
synthetic projection, while referenced slots still validate against their schema.

Stored views retain immutable identity-bound trees. Outer predicates are not
pushed across view boundaries. Execution-private borrowed scratch handles frozen
slot mappings without copying already owned projected backing. Preflight uses the
current statement's captured TimeZone and DateStyle, not creation/preparation
settings; returned results retain their original capture. Settings do not mutate
stored definitions or change owner/invoker authorization.

Joined views freeze source occurrences, expanded outputs and merged-key identities.
Rendering uses canonical per-occurrence aliases and explicit bound predicates,
not stale source text. Rename and unrelated attribute compaction preserve self-join
sides and output metadata. Synthetic rendered bridges can distinguish duplicate
positional columns without changing public names or frozen slots. Deparsing never
evaluates dormant errors or volatile providers. Reconstruction proofs consume
rendered definitions and compare complete values/metadata, not cosmetic SQL text.

Every executable logical-node schema validates concrete usage, current descriptor
identity and stored collation authority. Retained contextual string/NULL analysis
occurrences may remain descriptor-free, but cannot become executable untyped
outputs or reserved scalar slots. Capture, retagging and dependency walks preserve
those immutable analysis roots, including roots erased by execution simplification.

## Set-provider execution

Set expansion uses immutable local-group templates and fixed typed marker/scalar
slots. Preflight simplifies original roots once in the execution environment and
reconstructs private level chains from surviving depths without mutating held
plans or shifting existing key/window references. Fixed slot layout is storage
identity, not execution order; reconstruction retains original target-event order.
Capture validates event indices against their template. Ordinary scalar storage
uses an implicit identity layout without a mapping allocation; sparse layouts keep
reserved addresses.

Arguments evaluate once when a call is first reached for an input row; later
attempts reuse its cursor, and exhausted peers do not reopen arguments. Scalar
ownership is classified after lower-level sets become typed references. Bare
expansion owns scalar attempts, including exhaustion; a surviving maximum-depth
wrapper places siblings in a real-row-only projection. Earlier failures prevent
later events, and initialized cursors close without replacing the primary error.
Key-demanded sets share their local expansion owner and window slots.

Correlated function sources use the actual predecessor frame and demanded inherited
prefixes, with physical/version provenance separate from function outputs.
Rendering publishes finalized predecessor slots, never future/right slots early.
Genuinely dependent RHS stages reopen per left row using one borrowed same-query
context; lexical outer frames remain distinct. Ordinary queries do not allocate a
separate regexp lift/output list. Planning can evaluate invariant scalar calls,
but never executes a set provider without a selected input row.

Computed function rows have no physical row-lock identity. Rechecks refresh the
physical contributor and rescan correlated sources; independent sources reuse
recheck-local materialization. Each original candidate contributes at most its
first qualifying replacement rather than a new top-level expansion. Pending marks
and closed cursor caches remain owned by the existing execution owner.
General table functions and lateral derived-query/VALUES sources remain excluded.

## Blocking stages, bounds and shared producers

Sort, grouping, distinct and window work use execution-local caches/state and
finish before native cursor publication. Metadata and finalized values survive
source changes and native controls. Aggregate state is never persisted by Dump;
views remap both input and result descriptor authority. Late argument, comparator
or provider errors veto the entire native result or write batch. No spill,
parallel execution or general optimizer machinery is implied.

The occurrence producer stops nonblocking stages at demanded survivors without
pushing outer predicates through a view or replaying effects across its boundary.
Blocking stages drain qualified input when demanded. Zero-count execution opens
no producing child but retains admission/preflight. Bound stages evaluate their
execution-owned counts once and preserve full result metadata on empty output.
Tie lookahead can execute effects or acquire a mark on an excluded occurrence;
its error still vetoes the complete result. Key caches avoid expression replay.
Presence-only subqueries can bypass proven unnecessary bounds/key production,
while retaining the analyzed dependency graph. Scalar child stages close on
success, failure or cancellation; their statement capture belongs to the parent.

Shared CTE producers open on first demand and store typed rows once. Readers have
independent replay positions over one append-only frontier. Closing an early reader
does not discard a producer needed later. Producers belong to the defining query
invocation; correlated local invocations are independent and every Execute is fresh.
Failure/cancellation sticks at the producer and closes its child once. Public Rows
contain finalized data, not a live producer. Static analysis includes unused
definitions; execution privileges follow referenced subtrees. Virtual scans have
no independent ObjectRef, storage, snapshot or ACL authority, and outer rowmarks
do not propagate through them. Native opaque-consumer gating does not erase an
independently reachable stored physical settlement/lock frontier; PostgreSQL can
omit those extra grants. Recursive and modifying CTEs remain excluded.

Window offsets, partitions, permutations and overlays are execution-private.
Named definitions retain independent volatile identity while equivalent key sorts
can share work; no mutable window state enters prepared plans or catalogs.
ROWS/RANGE/GROUPS determine membership independently; EXCLUDE then removes the
current occurrence, its peers, or its other peers. TIES never inserts an absent
current row. Peers use all window ORDER keys; without ORDER the entire partition
is one peer group even for ROWS. count(*) returns0 for an empty masked frame;
sum(expr) returns NULL. Native direct count(*) and row_number() results are known
nonnullable, while SUM results are known nullable. Stored view columns retain the
existing known-nullable relation metadata contract, including count outputs.
Without a real exclusion, SUM retains transition state while the frame head is
unchanged, appending entering members and reusing full/growing-frame results.
Moving heads restart the existing no-inverse providers. Real exclusions restart
for every current occurrence, evaluating admitted members member-major across
separate SUM calls, without subtracting excluded totals. Semantic target equality
allows matching ORDER expressions and duplicate labels, including typed scalar
child queries in registered SUM arguments. Binding-local comparison pairs child
query frames and source occurrences while retaining exact outer correlation
coordinates; ordinary grouping remains conservative, without global normalization
or CSE. Equal targets do not share selected SUM execution slots or volatile
argument occurrences. A matched ORDER call does not execute again, but its
analyzed child graph retains authorization and dependency ownership.
Integer inputs widen to bigint (smallint/integer) or numeric (bigint);
numeric/real/double retain their existing SUM result types.
Arguments participate in authorization, grouping, capture and descriptor remap
even when runtime membership is empty. General window providers, INTERVAL SUM,
count(expr), FILTER, DISTINCT and call-local aggregate ORDER remain excluded.
Native array/tree aggregate ordering and distinct capability limits are separate
from their represented structural equality. Range aggregation does not add general
range comparison, grouping or temporal-range providers.

## Physical scans and row-lock frontiers

Inherited scans map physical descendants to the named parent's complete layout.
Each occurrence owns its own multiplicity and captures all roots at one statement
snapshot. Parent ACL/RLS owns logical admission; physical descendant ACL/RLS is
checked only when that descendant is itself named. Stored-view admission reuses
validated base grants; effective rowmarks retain their stronger grants. Prepared
execution can see later admitted descendants while already-open eager Rows stay
fixed. Recursive modifying targets, hierarchy LOCK/ALTER, public tableoid exposure
and arbitrary native RLS policy evaluation remain boundaries.

Rowmarks lock actual physical identities. After a wait, only frozen contributors
are followed to current heads; rechecks rebuild their bound frames without finding
new matches or resorting. Ordinary unmarked queries carry no locking provenance.
Marks settle before an irreversible consuming count frontier or outer blocking
stage; outer rejection cannot replace an inner winner or erase held grants.
Replacement peer keys refresh once through their existing cache identity.

Native virtual catalogs have no durable physical tuple identity and reject locking.
Stored set outputs are opaque copy-valued lock-lineage barriers: inherited marks
stop during acquisition, kind checks, authorization and capture, while locally
stored descendant marks remain active. Synthetic VALUES occurrences are likewise
not physical identities. Native rechecks retain already selected synthetic values;
PostgreSQL can reevaluate volatile synthetic cells during replacement recheck.
Native outer-join scan demand can acquire different extra locks from PostgreSQL's
physical plan; this is not physical plan-order parity.

Immediate wait policies use the lock manager's atomic grant path, never an external
eligibility probe. Skipping an unavailable contributor does not release previously
acquired grants. Relation waits remain separate. See
[transaction contracts](TRANSACTIONS.md#relation-and-row-resource-manager).

## Mutation and output publication

Query-backed writes capture one coherent target/source frontier, including prior
successful own writes but excluding the current command's new batch. Target
relation admission precedes source admission. Candidate target/source identities
are frozen before row waits; rechecks retain the captured source and follow only
the original physical target. Ordinary UPDATE/DELETE never widen membership.
Source scanning, sorting, conversions and private row construction run outside the
engine mutex. One cancellation-sensitive Finish publishes the complete accepted
batch, index effects, counts and owned output. Required source Close failure vetoes
success without replacing an earlier primary error.

Partial writes compose in private containers while RHS expressions and bounds
retain the original frame. Final generation, constraints and ACL checks use the
completed row; no partial container is published. Target plans retain captured
identity/type authority rather than live binder/catalog pointers. Whole-row presence
is occurrence metadata, not a NULL-cell or result-width heuristic, and does not
force locking/version provenance merely to distinguish an absent tuple.

Mutation output is projected eagerly after successful private-stage admission.
Exec evaluates output even when discarded; Next/Close do not. Late output or
constraint failure publishes no native rows, affected count or writes. Sequence
effects retain their independent nonrollback contract. RowsAffected counts actual
initiating mutations, including unchanged updates; LastInsertId is unsupported.
Native authority is refreshed after waits, unlike PostgreSQL's startup ACL decision.
Held output signatures remain fixed and finalized rows survive rollback or controls.
Triggers, server rewrite rules and physical/C/wire tuple metadata are not fabricated.

MERGE source Close succeeds before action row locking. Current-command writes do
not widen frozen membership. Its native Repeatable Read update path can retain an
old snapshot version and an own descendant with the same logical RowID, without
advancing the snapshot; deletion retires the selected version and replacement base,
not an unrelated old occurrence. Mixed actions reuse canonical replace/insert/delete
staging. Native RowsAffected sums actual actions, whereas pinned lib/pq reports zero.
Within the initial eligible MERGE class, the first TRUE WHEN selects one action;
FALSE/NULL pass to later clauses. An unconditional arm makes later same-class
arms unreachable, checked before target/source admission and expression binding.
An initial selected DO NOTHING never follows a newer target or acquires its
mutation row grant. A genuine attempted mutation may wait and restart into
DO NOTHING; that omission does not undo the already acquired grant. After lost
ON, a BY SOURCE no-op suppresses only the target action, not the retained
BY TARGET source action. Omitted candidates allocate no replacement payload,
claim no mutation key, consume no identity/default/generated value, mark no
accepted target RowID and evaluate no RETURNING. Empty output keeps its analyzed
schema. All specified no-op arms require any-column target SELECT even for
FALSE conditions or empty input; actual reads and mutation arms keep their
ordinary privilege demands. Late failure/cancellation still discards every
private mutation/output prefix through the same Finish boundary.
The exact correlated scalar array-bound case that caused pinned PostgreSQL server
recovery is not a claimed parity result or SQLSTATE golden.

Conflict arbitration uses actual installed providers and captured typed authority.
Native held statements specialize parameters afresh rather than implementing
PostgreSQL's generic-plan switching. Waits do not rerun sources or defaults;
selected-row replacement can restart proposal admission only. Stable-isolation
visibility recovery validates physical positions against RowID/VersionID. Current
rights/providers are rechecked after waits; late errors discard the mixed prefix.
Writable target views, partition-hierarchy conflict updates and deferrable UNIQUE
remain native boundaries.

Positioned mutations resolve current cursor incarnations at Execute, not Prepare.
Eligibility depends on the native pipeline rather than PostgreSQL's chosen index/
sort plan. Physical identity is never replaced by an equal-valued tuple. Mutation
failure does not invalidate a healthy cursor; rollback restores data but not prior
movement. Exported held cursors have released their producer and physical occurrence
and cannot become updatable from stored projected values. Catalog replacement
fences original authority; detached FETCH/output rows remain immutable.

## Introspection, query-created tables and native controls

Catalog execution materializes immutable typed facts from captured authority outside
the catalog mutex. Repeated timezone leaves use one transaction instant; catalogs
remain read-only and no write expression runs after rejection. Visibility snapshots
are captured only for demanded inquiries and are not independent permissions to
read their subject. Numeric OIDs are backend-local and can be reminted by Restore.

Native PG_NODE_TREE is an immutable nonphysical structural carrier, not SQL TEXT
or PostgreSQL nodeToString interchange. Introspection never evaluates definitions;
saved trees retain structure after owner removal. Attribute ownership uses stable
numbers, not compacted result positions. Held eager catalog rows keep captured
metadata while new executions see current definitions. Query-created storage
rejects structural tree carriers rather than persisting opaque keys as SQL data.

Query-created tables store finalized typed values, never adapter text or a second
producer evaluation. Native utility admission precedes snapshot acquisition and
retains explicit-transaction exclusions rather than PostgreSQL's analysis-time
safe-snapshot waiting behavior. No-data creation still analyzes/imports inputs but
skips execution-owned folding, source capture, runtime source privileges and rowmarks.
Populated creation finalizes once before atomic destination publication. Exec
reports stored occurrences; Query has no result relation.

Dump stores committed ordinary definitions, live rows and generator state, not
query provenance, transition states, cursor caches or output history. Restore's
bounded whitelist rejects mutation-output, conflict-bearing and query-source write
scripts; failure/cancellation preserves the destination. These are process-local
logical host controls, not disk/WAL crash durability, physical pg_dump interchange,
wire-protocol compatibility or native extension ABI support.

## Development and independent coverage

Run shared native/reference scenarios from the repository root using `make check`
and the same selector with `REFERENCE=1`. Relevant existing families include
`^TestReferenceQueryCore`, `^TestReferenceQueryCTE`, `^TestReferenceQuerySetOps`,
`^TestReferenceQuerySubquery`, `^TestReferenceQueryLockFeatureSmoke$`,
`^TestReferenceRegexpSet`, `^TestReferenceAggregateModifiersFeatureSmoke$`,
`^TestReferenceQueryWindowFeatureSmoke$`, `^TestReferenceInsertReturning`,
`^TestReferenceUpdateFrom`, `^TestReferenceDeleteUsingSmoke`,
`^TestReferenceInsertConflictUpdate` and `^TestReferenceAttrdef`.
These are instructions, not claims that checks have run.

Expectations for diagnostics, raw metadata, eager versus streaming errors and native
controls are backend-specific and independently asserted before comparison.
Unordered fixtures preserve duplicate, NULL and zero-width counts. Byte ownership
is checked on both adapters; detached column-name slices are a native-only contract.
Real manager-pending observations and PostgreSQL blockers prove wait boundaries;
public deadline callbacks alone do not. See [testing](TESTING.md),
[dual-mode development](DEVELOPMENT.md#dual-mode-query-reads-and-legacy-transition)
and [CTE design](design/PG18-QUERY-CTE.md).
