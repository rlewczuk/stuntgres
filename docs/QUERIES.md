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

## Foreign scans and logical cursor ownership

Only explicitly installed Go providers can execute foreign leaves; foreign
metadata has no physical heap root. Planning selects a valid concrete path,
Begin establishes execution ownership, and Iterate supplies typed owned
occurrences to the existing filter/project/join/sort pipeline. Required layouts
include local quals and rechecks even when their columns are not projected.
NULL and zero-column presence are separate from EOF. Conversion uses the same
native codecs, typmods and domain authority as ordinary values, outside locks.

Ordinary statement completion closes all foreign resources before public result
publication, including LIMIT, failure, cancellation and Exec-discard paths.
Correlated RHS invocations perform actual ReScan on the same owned scan with
fresh evaluated invocation values. Their new selections retain distinct stable
occurrence indices; earlier join output never points at overwritten scratch.
Noncorrelated cache replay need not restart its producer. Fallible rewind errors
propagate through existing derived/view/join/cursor owners.

A named SQL cursor is an explicit longer-lived owner. Finite FETCH/MOVE demands
only its existing producer frontier (apart from legitimate blocking operators),
publishes an eager independent batch, and retains the live scan for later SQL
requests. Public Rows.Next/Close performs no callbacks or source I/O. Current
request context/ACL admission is refreshed; no reopen-and-skip or blanket upfront
materialization substitutes for cursor motion. Forward EOF Ends once; SCROLL may
retain real rewind state. CLOSE/failure/savepoint/transaction/session teardown is
unlocked and error-capable. HOLD drains and successfully Ends before promotion.
Primary errors win cleanup errors; End-only terminal failure publishes no batch.

Catalog replacement retains existing stale-cursor semantics, not a cross-session
teardown sweep: a later owner interaction detects the stale epoch before pulling,
then releases resources; finalized batches remain readable. See the
[embedding lifecycle contract](DRIVER.md#foreign-provider-registration).

## SQL/JSON query arrays

`JSON_ARRAY(query)` reuses the ordinary query binder, source capture and
execution stages. Its query must expose exactly one target; SELECT, VALUES,
WITH and set-query decorations retain their existing admission and ordering.
Each outer occurrence and each held execution reopens the child query with its
current parameters and correlation frame, never a cached first result. Values
stream into the typed array writer without scalar-subquery cardinality limits.
SQL NULL rows are omitted; zero rows yield SQL NULL and all-omitted rows yield
`[]`. FORMAT JSON is per-row; RETURNING converts the finalized array. Actual
query columns retain existing ACL/dependency capture and stored-query consumer
restrictions. Stage closure and cancellation finish before any result escapes.

## Array relational expressions

Unary array `unnest` shares existing target-SRF lowering and FROM function
cursors with multirange expansion. Each invocation, prepared execution and
lateral source owns a fresh index into the immutable input. Storage order
includes NULL elements; zero-row inputs retain metadata. In a scalar wrapper
such as `abs(unnest(a))`, each present element is evaluated by the existing
scalar projection lane, not a separate expansion implementation.

`aclexplode(aclitem[])` uses the same invocation-owned target/FROM machinery,
with fixed OUT columns grantor/grantee OID, privilege_type TEXT and
is_grantable BOOL. It preserves duplicate ACL entries, emits one row per
ordinary privilege bit, and has no implicit SQL ordering. NULL input emits
zero rows with the same metadata. Rank-one empty column ACL defaults also
emit zero rows, while rank-zero text `{}` is rejected. LATERAL reopening,
ROWS FROM padding, aliases and ordinality retain ordinary set-source rules;
typed column definitions are redundant for this named-OUT record provider.


Installed intagg1.1 exposes fixed-INT4 `int_array_aggregate` and
fixed-INT4[] `int_array_enum` wrappers over these existing query kernels.
The aggregate includes NULLs and duplicates, with element order specified only
by aggregate ORDER BY. Enum reuses target/FROM/lateral cursors and ordinality,
including multidimensional inputs and present NULL elements. Both require
installation and retain their own procedure dependencies in stored views and
native replay; they are not builtin aliases. See the
[bounded intagg contract](DIALECT.md#intagg-installed-wrappers).

Only unqualified multiargument FROM `unnest(a,b,...)` without aggregate/window/
VARIADIC decorations or per-function typed definitions is rewritten to unary
`pg_catalog.unnest` calls. Zipping uses the longest input, typed NULL padding and
existing ordinality, alias and lateral semantics. Qualified and target-list
multiargument calls remain ordinary missing signatures. Explicit `ROWS FROM`
members are normalized independently. This is syntax normalization, not a
multiargument builtin overload or generic SRF subsystem.

Ordinary function sources retain `AS alias`, optional column-name aliases and
top-level typed-definition diagnostics. Per-function `AS (column type,...)`
definitions are parsed only inside `ROWS FROM`; they do not consume the `AS`
that introduces a source alias. FROM `FILTER`/`OVER` diagnostics remain owned by
their original syntax tokens rather than scalar function resolution.
After function resolution, typed-definition diagnostics distinguish scalar
results, named composites (including domains), and OUT-parameter records.
The same rejection applies to string-only and mixed sources; adding an
unrelated sibling never makes an invalid per-function definition valid.
Explicit `ROWS FROM` owns the multiple-function diagnostic even when its
single written `unnest` call expands into several unary calls.

Binding, frozen literal capture, symbolic rendering and replay share the same
normalized call ordering without mutating retained ASTs. Views rendered after
source renames or native Dump/Reset/Restore retain argument ownership and padded
result schema. Present NULL composite elements expand to typed member NULLs;
exhausted inputs pad identically but do not create another present invocation.
Zero-field composites and their domains expose no member columns, but each
element (including NULL) still creates a row and advances ordinality. Their
zero-column spans neither stop zipping nor contribute phantom scalar columns.

`array_sample` and `array_shuffle` stay volatile even on NULL/empty/singleton
arrays. Argument dependencies, including sample count, participate in normal
ACL/capture/preflight traversal before strictness or demand shortcuts. Existing
immutable generated/index restrictions and conservative parallel admission
remain unchanged. Random-state borrowing happens after argument/count validation;
context cancellation releases ownership without publishing partial results.

## Installed Daitch–Mokotoff expressions

`daitch_mokotoff(text)` resolves only through installed fuzzystrmatch procedure
identity. The shared binder infers TEXT for unknown parameters, applies existing
implicit coercion, captures the procedure dependency and owns the `_text`1009
result/element generation. Integer input does not silently stringify. Scalar
DISTINCT/ORDER BY/FILTER/OVER report the ordinary nonaggregate/nonwindow errors;
their primary messages preserve the full supplied qualified name, including a
relocated schema. Procedure lookup still uses the final identifier in the
resolved namespaces. ALL is accepted as PostgreSQL accepts it.

Evaluation uses call-local prefix-tree state and the complete release phoneme
chart, preserving correlated J alternatives, adjacent-digit suppression,
two-digit codes and completion-before-padding order. It never sorts or truncates
alternatives. Strict NULL and nonencodable input produce typed array NULL;
successful output transfers fresh rank1/lower1 array buffers once. Cancellation
checks cover skipped-codepoint scans, phoneme lookahead and branching.

Intrinsic volatility comes from the installed procedure and combines with child
volatility. Genuine immutable expressions can fold and participate in STORED
generated columns without relaxing unaccent's STABLE restriction. Existing
walkers, stored renderers and native control remappers preserve live procedure
identity and exact array/element ownership; no dictionary is invented for this
provider. Retained rows remain readable after replacement; fresh preparation and
same-shaped held TEXT→text[] statements reanalyze against restored installed
identity under the existing driver contract. No blanket DM stale-statement fence
or retired builtin-TEXT alias capture is introduced.
See [selected proof](../reference/catalogs/README.md#daitch-mokotoff-selected-proof).

### Installed Levenshtein expressions

The four selected `levenshtein` / `levenshtein_less_equal` overloads resolve
through the same installed fuzzystrmatch authority. Shared argument analysis
coerces the first two formals to TEXT and remaining cost/bound formals to INT4;
known integer text inputs do not stringify, and known BIGINT/NUMERIC costs do
not silently narrow. The functions-package typed carrier evaluates every
demanded child once in source order before deciding strict typed INT4 NULL.
Each evaluation owns fixed-size distance rows, with no parameter-frame cache.

All child walkers, grouping identity, conditional/preflight reconstruction,
stored renderers and control remappers retain the actual installed procedure
and every argument. Namespace-qualified defaults, STORED generated columns,
CHECKs and views remain executable after native replacement. Already returned
INT4 rows/metadata retain their values; compatible held statements reanalyze
under the existing driver contract, without a blanket replacement fence.
See [selected proof](../reference/catalogs/README.md#levenshtein-selected-proof).


## Parallel SELECT scans

The eager top-level SELECT entry can partition one ordinary persistent physical
table's already-selected visible occurrences into disjoint balanced intervals.
All workers share the original immutable statement frontier, physical prefixes,
parameters and admitted environments; each owns a distinct view-release handle,
source header, query state, evaluator, presence scratch, selections, ordering
caches and output buffer. There is no complete input-table copy or mutable
evaluator shared between workers. Own updates/deletes and retained visibility
are partitioned by logical occurrence, not raw committed offsets.

`max_parallel_workers_per_gather` defaults to 2, accepts 0..1024, and is captured
under the session settings lock at command admission. Held prepared SELECTs use
fresh execution-time settings and parameters. Zero disables workers; one and
empty/singleton scans stay serial. Actual dispatch is bounded by input size and
native CPU resources, with no PostgreSQL process scheduler or optimizer costs.

Gather combines qualifying occurrences without an SQL order guarantee.
Supported ORDER BY sorts worker-local runs and performs a genuine sorted-run
head merge with the same fallible typed comparator as serial sort. Direction,
NULL placement, hidden keys and expression-cache identity are preserved; complete
key peers have no additional ordering promise. Final projection and merge finish
before publication; public Next never evaluates a target or resumes a worker.

Positive eligibility is deliberately conservative: one physical sequential source,
optional filter/sort and audited row-local scalar expressions. Writes, row locks,
live SQL cursors, nested queries, temporary/catalog/sequence/view sources,
index candidates, append/join/group/distinct/window/CTE/bounds, virtual generated
reads and unaudited functions keep the existing serial path. Stored generated
values can be read. Sequence/settings effects and restricted random functions
are not mistaken for safe work merely because of their volatility annotation.

The first execution error cancels sibling evaluators without replacing its
diagnostic with shutdown cancellation. Worker contexts retain live admission
authority but observe owned sibling cancellation. All stages and independent
worker pins close, and every started worker joins, before scope completion,
ordered merge or result publication. A final context fence vetoes success.
Direct output owns its final source pin/selection; computed output releases
unneeded inputs. Caller cancellation remains an ordinary Go context error;
the command's own timeout retains SQLSTATE 57014 and its timeout diagnostic.

## Integer target series

The INT4 and INT8 two-/three-argument `generate_series` overloads use the ordinary
target-SRF lane, not a second query executor. Omitted step is +1. Endpoints are
included only when reached; NULL input or a direction mismatch gives no rows.
Nonnull zero step fails with `22023: step size cannot equal zero`, without a
runtime Position. An overflowing successor terminates normally after the last
valid value rather than wrapping or raising input-overflow errors.

Each admitted input row owns its invocation and evaluates arguments once in
source order. Same-depth sets zip to the longest provider with typed NULL padding;
nested levels preserve the existing depth scheduling. Group/sort/distinct and
legal window PARTITION/ORDER keys use the existing demanded expansion stages.
Window frames, scalar restricted owners, and direct CASE/COALESCE calls remain
illegal. A child SELECT owns its own placement context.

Strict arithmetic with a physical constant NULL erases its target-set subtree
during immutable planning, then re-lowers surviving peers. This is not a
first-item series evaluation: the erased provider contributes no cardinality.
A nullable source column does not supply that constant proof and still yields
one NULL per expanded row. Stored target-set validation and FROM source
eligibility are separate checks; both supported integer/bigint series lanes
are admitted.

Arithmetic state is constant-space and execution-private. LIMIT stops provider
demand; only finalized immutable output is published. Late errors or cancellation
publish no native cursor or successful mutation prefix. Held statements and
stored views preserve symbolic arguments and width, never live cursors.
Dump/Restore replays eligible views and literal stored data under its existing
whitelist; it does not enable INSERT query sources or CTAS during Restore.

Plain timestamp series executes as described below; timestamptz signatures
participate in truthful overload resolution but remain non-executable.
INSERT VALUES remains excluded; FROM providers use the shared source lane below.
Public coverage and commands live in [reference/numeric](../reference/numeric/README.md#integer-target-series-selected-proof).

## Numeric series

The numeric two-/three-argument `generate_series` overloads reuse these same
target and FROM stages. Arithmetic uses exact, invocation-private decimal
coefficients, not floating point or precomputed row arrays. Default step is +1;
reachable endpoints are inclusive and mismatched directions are empty.
The first row keeps start's display scale; subsequent rows use the maximum of
start's and step's scales. Stop's scale never contributes. For example,
`generate_series(0.1,0.3,0.10)` yields `0.1,0.20,0.30`.

Result metadata is unconstrained NUMERIC (OID1700, typmod-1), including empty
results, domains and constrained numeric inputs. NULL arguments suppress
provider validation only after demanded arguments have been evaluated. Nonnull
start, stop and step are checked in that order for NaN/infinity, then zero step;
failures are `22023` with role-specific messages and absent runtime Position.
Validation precedes direction checks. LIMIT0 does not demand a target provider.

Successor state is unpacked and exact: a valid final row whose unreturned
successor exceeds packed numeric limits still terminates normally. For example,
`generate_series(9e131071,9.5e131071,9e131071)` emits one value without overflow;
ordinary numeric addition retains its normal overflow checks.
Held executions open fresh state, and existing final publication and mutation
staging preserve cancellation/failure atomicity. Symbolic view arguments and
materialized literal data survive native Dump/Reset/Restore; live cursors never
do. Timestamptz, INSERT VALUES and Restore query-source exclusions are unchanged.
See [selected numeric proof and smoke](../reference/numeric/README.md#numeric-series-selected-proof).

## Timestamp series

Exactly `generate_series(timestamp,timestamp,interval)` returns TIMESTAMP,
including empty results. Each successor applies months with destination-month
day clamping, then days, then microseconds to the preceding row. January31,
February28, March28 is therefore an iterative monthly sequence. Plain civil
timestamps are unaffected by session timezone or daylight-saving transitions.

Bounds are inclusive for either sign; direction mismatch and any NULL input
produce no rows. Step sign uses widened `(months*30+days)*86400000000+microseconds`.
Nonzero mixed-sign steps are accepted even when calendar progress reverses or
stalls; no progress guard exists. Zero/net-zero steps raise22023
`step size cannot equal zero`; infinite steps raise22023 `step size cannot be infinite`.
Infinite timestamp endpoints remain legal and can repeat indefinitely.

The successor is computed before the current row is emitted. Any required
addition overflow raises22008 `timestamp out of range`, including equal endpoints
and LIMIT1. Invocation state is private and constant-space; existing eager native
publication and mutation staging prevent partial results on failure/cancellation.
Views and native Dump/Restore retain symbolic calls and typed persisted data,
never cursors; Restore's query-source whitelist is unchanged.
See [selected proof and smoke](../reference/numeric/README.md#timestamp-series-selected-proof).

## Array target subscripts

`generate_subscripts(anyarray, integer)` and its three-argument Boolean-reverse
overload return INT4 indices for the selected dimension. They use stored lower
bounds and selected extent, not flattened element count: NULL elements still
have subscripts. Omitted reverse is false; true visits upper to lower.
Empty arrays, dimension <=0 or greater than rank, and any typed NULL input
produce no rows with stable INT4 metadata.

This is simple `anyarray` polymorphism. The first operand must independently
supply an array type; untyped NULL, string or parameter cannot infer that identity
from dimension, reverse or output. An outer domain over an array is flattened for
matching; an array of domain elements keeps its array identity. Smallint dimension
can widen to INT4; bigint, numeric and text cannot implicitly narrow. Explicit
`$1::integer[]` is the held parameter route. Every argument is analyzed and every
demanded invocation evaluates its arguments once in source order before strict
emptiness, so a NULL array does not erase another child's runtime error or privilege.
Existing immutable planning may still simplify constant strict scalar wrappers.

The execution-private bounds cursor is constant-space and does not copy array
payloads or inspect elements. Existing target-SRF zip/padding, nested stages,
group/sort/distinct and legal window keys remain the owners. Held executions open
fresh state. Views retain symbolic calls; CTAS and INSERT SELECT materialize
ordinary INT4 rows, with late errors/cancellation preserving statement atomicity.
Native controls replay supported views and literal persisted rows under the
existing Restore whitelist.
Typed array literals own their subscripts replay cast; matching source casts do
not grow on successive Dump/Restore rounds, while differing casts are retained.

INSERT VALUES expansion remains excluded even where PostgreSQL executes it.
Scalar restricted owners retain existing SRF placement diagnostics.

**Pinned release safety exception:** PostgreSQL18.0 oracle discovery observed
reverse iteration with lower bound INT32_MIN fail to terminate before fixture
cancellation. The shared reverse boundary witness therefore uses INT32_MIN+1.
Native int64 cursor arithmetic safely emits the exact INT32_MIN endpoint once
and terminates; an internal real-cursor unit proves this without emulating the
release defect or repeating the hazardous PostgreSQL query. The existing array
storage upper-bound exclusion remains unchanged.
See [selected proof](../reference/numeric/README.md#array-target-subscripts-selected-proof).

## Function sources

Existing integer/bigint/numeric/plain-timestamp `generate_series` and `generate_subscripts` calls use the
same typed source stage as supported string, JSON, `jsonb_path_query` and regexp
calls. Output aliases preserve positional names and types, including empty metadata.
Comma sources form Cartesian products; `ROWS FROM` zips to the longest member
and pads exhausted members with typed NULLs, emitting no row when all are empty.
`WITH ORDINALITY` appends INT8 starting at1 before filters/OFFSET.

`jsonb_path_query(target jsonb,path jsonpath,vars jsonb DEFAULT '{}',silent boolean
DEFAULT false)` returns ordered JSONB items, with base column name
`jsonb_path_query` (not `value`). Each invocation completes before exposing its
rows, including in target-list expansion and under LIMIT. No match or any SQL
NULL formal produces zero rows; selected JSON null remains a present JSONB row.
An explicitly silent structural stop retains its prefix, while ordinary errors
and cancellation discard the invocation. Existing owned-set cursors hold only
that invocation's collection and release it on close; no live evaluator state
is stored in views or dumps. Scalar exists/match/first calls do not become
additional FROM providers. Restore INSERT query sources remain unsupported.

Functions implicitly see preceding FROM columns; `LATERAL` makes that scope
explicit. Each left occurrence opens fresh arguments and ordinal state.
LEFT joins preserve empty invocations with NULL extension; RIGHT/FULL reject
actual join-left lateral captures, not uncorrelated sources. Nested lexical SRFs
must appear at top level of FROM, while child SELECTs own their target SRFs.
Ordinary source-call binding and nested placement validation precede semantic
column-definition restrictions. The selected JSONB record conversion sources
accept complete typed definitions; unresolved record sources require them.
Definitions resolve all member types, typmods and collations in declared order.
Ordinary member names inside ROWS FROM reach function resolution before
definition restrictions, so a missing function retains its signature diagnostic.
Resolved calls still require an explicitly supported source provider; this
diagnostic reachability does not add general scalar function sources.
After successful call analysis, multiple-function top-level definition
restrictions precede ordinality restrictions, then the resolved
scalar/named-composite/OUT-record restriction. A definition is not a cast of an
already named composite result. See the PostgreSQL table-function syntax below
for per-member definitions with ROWS FROM and ordinality.
Expanding an anonymous JSONB record result without its own static layout using
`(...).*` is a binding error, including strict calls with SQL-NULL documents
and runtime ROW() bases. It cannot silently select zero columns. Genuine named
zero-member row types retain static expansion. Typed FROM output definitions
never erase real inner evaluation: a NULL datum can use the outer descriptor,
whereas a non-NULL record supplies its actual conversion layout before caller
arity/type validation and positional output labels.
Arguments evaluate once per demanded invocation; unopened sources do not run
under LIMIT0. JSONB recordsets convert their complete demanded invocation
privately before exposing the first row, so a late bad member also fails under
LIMIT1 or EXISTS. This does not change other providers' demand-driven execution.
Prepared selected JSONB record sources retain analyzed star-projection member
targets and labels when attributes are added. The source still converts its
complete current destination, including added members not exposed by that
projection. Other sources and the ordinary result-signature fence are unchanged.
Renamed members retain their analyzed output labels; dropping a captured source
target fails with 42703 (`attribute N of type record has been dropped`), not a
fabricated NULL or a freshly expanded result contract.

Symbolic source views, CTAS, INSERT SELECT and native control replay reuse their
existing consumers and atomicity boundaries. The selected JSONB record lane
does not add general scalar FROM capability, lateral derived queries or
Restore whitelist entries.
See [PostgreSQL table functions](https://www.postgresql.org/docs/18/queries-table-expressions.html#QUERIES-TABLEFUNCTIONS)
and [selected proof](../reference/numeric/README.md#from-srf-selected-proof).


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

`json_each(json)`, `json_each_text(json)`, `jsonb_each(jsonb)` and
`jsonb_each_text(jsonb)` each own one record-valued cursor.
SELECT expansion keeps one RECORD marker slot per call. FROM alone exposes the
record's key/value members: immutable result usages determine contiguous spans,
so mixed ROWS FROM advances each call once, pads every exhausted record member
with its own typed NULL, and writes optional INT8 ordinality last. A present
record with a NULL value is not an exhausted call. A composite function alias
qualifies the member columns and denotes the whole row; it does not rename the
first field unless an explicit positional column alias does so. Empty and SQL
NULL inputs retain their schema before iteration. These selected calls reuse
ordinary correlation, retained views and generation remapping, not independent
key/value invocations or a separate function-source store.
JSONB each borrows canonical immutable object fields, preserving last-key-wins
normalization and byte-length/byte key ordering without reparsing output.
Each invocation owns only its iteration state; JSONB values retain nested
payloads, while text values decode strings and map JSON null to SQL NULL.
SQL NULL is strict and empty; non-object JSONB roots fail before publication.

The four JSON record converters share these function-source spans and ordinary
scalar evaluation. `json_populate_record(base,json)` and `json_to_record(json)`
are scalar, even when used in FROM; a whole SQL NULL result expands to one row
of typed NULL fields. Only their `recordset` counterparts enter target-set
lifting. Recordsets take an array of objects, restart every row from the original
base, and yield no rows for SQL NULL or an empty array. Assert sequence order
through explicit WITH ORDINALITY/ORDER BY, not an unordered SELECT.
Processing validates every string in a demanded object before converting its
fields. Recordsets process one object before decoding later rows, so a later
Unicode error cannot mask an earlier typed-field error. Unicode diagnostics
retain the original document's line and Context, including multiline arrays.

`json_to_record`/`json_to_recordset` in FROM require ordered typed definitions:
`f(...) AS r(a int,b text)`, `f(...) r(a int,b text)` or `f(...) AS (a int,b text)`.
Per-member `ROWS FROM(f(...) AS (a int,b text), ...)` keeps each complete
descriptor and permits outer WITH ORDINALITY and name-only aliases. Named
composite/OUT providers reject redundant definitions. Binding resolves ordinary
arguments and nested-SRF placement before definition conflicts, then completes
anonymous source shape before expansion. An anonymous populate ROW keeps its
original input field names even when output definitions rename the columns.
Output reconciliation admits directional implicit binary compatibility, including
TEXT/VARCHAR, without applying output typmod sizing or changing payloads. Original
ROW conversion names and typmods remain in force; declared output usages own the
metadata and returned field identities. Function/I/O/numeric assignment casts do
not qualify. Incompatible anonymous output widths/types fail at execution, not preparation;
`NULL::record` can instead obtain its genuine conversion shape from the typed
FROM list.
Result metadata does not depend on the first produced row, and each held or
correlated invocation owns its conversion/cursor state. Stored views render
symbolic member types and collations so native Dump/Restore and emitted SQL
replay resolve the target owner's descriptors.


Correlated function sources use the actual predecessor frame and demanded inherited
prefixes, with physical/version provenance separate from function outputs.
Rendering publishes finalized predecessor slots, never future/right slots early.
Genuinely dependent RHS stages reopen per left row using one borrowed same-query
context; lexical outer frames remain distinct. Ordinary queries do not allocate a
separate regexp lift/output list. Planning can evaluate invariant scalar calls,
but never executes a set provider without a selected input row.

A whole, nonvolatile left-only WHERE restriction can gate predecessor rows
before a directly correlated function RHS on an inner/cross join. Execution
opens a private copied join with an ordinary left filter; the canonical bound
tree, view literal paths, frames and source provenance remain unchanged, and
the predicate is evaluated once rather than duplicated after expansion.
Stage/domain-sensitive predicates, outer joins, merged columns, unsupported
dependency forms and mandatory physical frontiers retain their original
filter placement. Refreshed-row (EPQ) execution also retains its captured
identity-keyed topology, even after the recheck clears physical locks; private
join/filter copies must not replace its captured nodes. This is bounded
function-input demand, not general predicate splitting or an optimizer that
crosses subquery boundaries.

Computed function rows have no physical row-lock identity. As in PostgreSQL
18.6, a recheck refreshes the physical contributor and requalifies the row with
the function-scan tuple it was produced with; it does not rerun the function
(18.0 did). Pending marks remain owned by the existing execution owner.
General table functions and lateral derived-query/VALUES sources remain excluded.

## Blocking stages, bounds and shared producers

Sort, grouping, distinct and window work use execution-local caches/state and
finish before native cursor publication. Metadata and finalized values survive
source changes and native controls. Aggregate state is never persisted by Dump;
views remap both input and result descriptor authority. Late argument, comparator
or provider errors veto the entire native result or write batch. No spill or
general optimizer machinery is implied. Grouping, distinct and windows remain
serial; the bounded SELECT scan path described above is the parallel exception.

Ordinary MONEY and INTERVAL SUM use that same aggregate pipeline. MONEY retains
exact signed minor units and reports22003 `money out of range` on transition
overflow. INTERVAL independently checks months, days and microseconds, preserving
calendar components and exact time units without normalization or AVG division.
Its transitions still check finite overflow after infinity; opposite infinity
signs and range failures report22008 `interval out of range`. Both ignore NULL,
return a typed NULL for global empty/all-NULL input, and return base-family
nullable results with typmod -1. Views re-own the MONEY arithmetic binding and
both provider descriptors. Neither overload is admitted to native windows.

Ordinary statistical aggregates share the same blocking pipeline:
`var_pop`, `var_samp`, `variance`, `stddev_pop`, `stddev_samp`, `stddev`
have six numeric input overloads. Integer/NUMERIC inputs produce exact-decimal
NUMERIC moments; REAL/DOUBLE PRECISION produce FLOAT8, widening actual binary32
REAL data before accumulation. `variance=var_samp`, `stddev=stddev_samp`.
Unknown inputs resolve to the preferred FLOAT8 overload. Numeric sums/squares
are unpacked until the final result; division uses up to 1000 fractional digits,
and stddev rounds the square root of the rounded variance at that same scale.
Zero finite numeric variance has display scale zero.
The twelve bivariate calls `regr_slope`, `regr_sxx`, `regr_sxy`, `regr_syy`,
`corr`, `covar_pop`, `covar_samp`, `regr_avgx`, `regr_avgy`, `regr_count`,
`regr_intercept` and `regr_r2` take `(Y,X)`, independently coerce both arguments
to DOUBLE PRECISION and omit any pair with either argument NULL, after evaluating
both expressions on FILTER-admitted rows. DISTINCT uses the whole pair.
Seven new floating finalizers reuse released six-field Youngs–Cramer arithmetic,
retaining input-order roundoff and checking unused axes for overflow even in
averages. Pair count instead uses checked exact INT8 arithmetic without floating
moment evaluation. Huge/nonfinite payloads count once after successful coercion.
Empty floating results are NULL; count is exact zero. Finite singleton population
covariance and regression sums are zero; sample covariance and slope are NULL.
Correlation requires nonzero variance on both axes. Intercept and r² require
nonzero X variance; with varying X and constant Y they return Y and1 respectively.
Released arithmetic is not clamped or rearranged; sample cardinality and axis
branches precede dependent IEEE arithmetic. Named approximate mathematical
witnesses use `abs(actual-expected) <= 1e-12*max(1,abs(expected))`; counts, NULLs,
metadata and diagnostics are exact. No unordered accumulation order is promised.
Results have typmod -1 and no physical origin, nullable except known nonnullable
INT8 pair count. Native Dump persists tables, SQL views and materialized results,
never live moment/count state; restored providers own destination input/result
descriptors. No statistical window provider or parallel grouping is added.


### Ordered-set aggregates

`percentile_cont(fraction) WITHIN GROUP (ORDER BY input)` returns FLOAT8 or
INTERVAL. `percentile_disc` retains its admitted sortable input type, and `mode()`
returns the most frequent input. ASC/DESC and NULLS syntax are retained, but NULL
inputs are always ignored. An ORDER BY number is a constant, not an output
ordinal. FILTER FALSE/NULL suppresses aggregated-input evaluation.

Percentile direct arguments are evaluated once against each final group-key
frame, not once per source row. They participate in dependencies, privileges and
call identity but do not choose the aggregate's semantic owner. Ungrouped direct
columns are errors. Empty global and all-filtered groups still evaluate direct
expressions; keyed empty input has no group, and HAVING rejection occurs after
finalization. Distinct volatile calls remain independent.

Scalar fractions require [0,1], with NULL returning NULL and invalid values
raising22003 even on empty input. FLOAT8-array fractions retain dimensions, lower
bounds, repetition, original element order and NULL elements. Empty input returns
SQL NULL before array-element validation; nonempty input with an empty fraction
array returns a typed empty array. Direct-expression errors precede those exits.

Discrete percentiles select ceil(fraction*N), with zero selecting the first
value. Continuous percentiles interpolate rank fraction*(N-1), returning exact
rank hits unchanged. FLOAT8 uses raw IEEE `lo + p*(hi-lo)` without fused or
weighted-endpoint rewriting. INTERVAL reuses calendar arithmetic: descending .25
between2months and0 yields `2 mons -15 days`, not normalized `1 mon 15 days`.
Mode compares sorted equality runs and retains the first longest run; thus ASC
chooses the least tied value and DESC the greatest. Comparator-equal but
representation-distinct values have no portable representative promise.

After admitted input evaluation and the once-per-group direct expression,
percentile preparation resolves NULL/empty exits and validates fractions before
allocating sort indices or comparing inputs. Scalar validation still precedes
empty-input return; array empty-input return still precedes element validation.
Array fractions are validated once in original flattened order, without a
fraction workspace or error-path result allocation. Only a nonterminal decision
sorts. Its finalizer borrows the executor datum buffer and sorted row indices,
without copying or replaying values into another provider buffer. Necessary array
output filling does not revalidate fractions. Preparation, sorting, direct
evaluation, array processing and mode scanning check cancellation before any
result or INSERT batch is published. Views retain WITHIN syntax and
remap direct/input/result/comparison identities through ordinary native controls.
Captured array constants in direct-argument subtrees already own their exact
typed annotation. Rendering does not add another identical leaf annotation on
dump replay; explicit outer casts, input conversions and domain casts remain.
Registered C/POSIX and builtin bytewise collation providers remain authoritative;
no additional array leaf codecs, collation providers or general ordering families
are enabled.

### Hypothetical-set aggregates

`rank(args)`, `dense_rank(args)`, `percent_rank(args)`, and `cume_dist(args)`
accept `WITHIN GROUP (ORDER BY sorted_args)` with matching positive-length
argument lists. Each pair independently selects a common type (sorted argument
first), then a collation (direct argument first). Different keys may have
unrelated types or collations. The aggregate result never inherits a key's
collation. There is a total 99-argument aggregate cap, not a two-key tuple limit.
Analysis completes common-type selection and coercion for every pair before
resolving any ordering operator, then resolves every ordering capability before
assigning pair collations. Consequently a later pair's type/coercion error
precedes an earlier pair's missing ordering operator or collation conflict;
a later missing ordering operator also precedes an earlier collation conflict.
Runtime comparisons use the final selected pair collations.

Ordering is lexicographic under existing typed providers, with independent
ASC/DESC and NULLS FIRST/LAST rules on each key. Unlike percentiles/mode, these
forms retain every FILTER-admitted input tuple, including partial/all-NULL
tuples. NULL direct arguments are real sort keys. Default ASC places NULL last;
default DESC places NULL first. Peer equality follows ordering, including
NUMERIC scale, BPCHAR padding, NaN and signed-zero semantics.

For N original admitted rows, B rows strictly before the hypothetical tuple,
E existing peers and D distinct preceding peer groups, outputs are:
`rank=B+1`, `dense_rank=D+1`, `percent_rank=B/N` (zero for N=0), and
`cume_dist=(B+E+1)/(N+1)`. Rank results are INT8; fractional results are FLOAT8.
Empty global/all-filtered groups return `(1,1,0,1)`, not NULL. Keyed empty input
produces no group. Results have typmod -1 and no physical origin.

Every direct child runs left-to-right once per executed call/group against
final grouped keys, even with empty input or HAVING rejection. Ungrouped direct
columns are rejected with the ordered-set direct-argument Detail. FILTER
FALSE/NULL suppresses input-key evaluation, not direct evaluation. All direct
and ordering children participate in privileges, dependencies, immutable call
identity, parameter inference and stored view replay. Finalization borrows one
executor-owned tuple buffer and target scratch; it never inserts or mutates a
hypothetical row in shared inputs. Sorting and placement use one comparator.
Cancellation/errors precede row or INSERT batch publication.

These aggregate overloads remain distinct from zero-argument ranking windows.
Existing ordering, collation and unsupported-carrier boundaries remain unchanged.
See [selected tests and smoke](../reference/aggregates/README.md#hypothetical-set-aggregates).



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

Shared SELECT CTE producers open on first demand and store typed rows once. Readers have
independent replay positions over one append-only frontier. Closing an early reader
does not discard a producer needed later. Producers belong to the defining query
invocation; correlated local invocations are independent and every Execute is fresh.
Failure/cancellation sticks at the producer and closes its child once. Public Rows
contain finalized data, not a live producer. Static analysis includes unused
SELECT definitions; execution privileges follow referenced SELECT subtrees. Virtual scans have
no independent ObjectRef, storage, snapshot or ACL authority, and outer rowmarks
do not propagate through them. Native opaque-consumer gating does not erase an
independently reachable stored physical settlement/lock frontier; PostgreSQL can
omit those extra grants.

Recursive definitions bind anchor output before the recursive arm, requiring
exact overall type/typmod/collation agreement. Unknown anchor outputs become TEXT
at this boundary. Recursive WITH names are visible throughout the group; lexical
dependency analysis topologically binds siblings and rejects mutual cycles.
The self scan is an opaque typed identity edge, not a structural query cycle or
physical/catalog dependency. Genuine recursion is never folded by any hint.

Each invocation owns an iterative current/next worktable over immutable accepted
tuple indexes. Anchor rows seed the first generation; the recursive arm sees only
that generation, and an exhausted arm swaps the next generation into place.
Positive-width UNION uses existing type-owned NULL-aware hash/equality on every
output column across the whole result, with complete collision comparison.
Unused outer columns cannot be pruned from recursive keys. ALL, and PostgreSQL18's
zero-column recursive UNION, require no deduplication structure.
Independent external readers replay the same frontier; every Execute/correlated
invocation starts fresh. Positive parent LIMIT stops production, while a blocking
parent may force a complete drain. LIMIT0 keeps metadata without opening the child.

Iteration, duplicate rejection, coercion and replay remain cancellable. The first
failure sticks, opened stages close once, and successful-path close failure vetoes
the single existing statement publication. Views and native Dump/Restore retain
symbolic definitions plus real persisted rows/dependencies, never worktable state.
MERGE's own recursive modifier is rejected, independently of a local recursive
SELECT in its derived source. Recursive modifying definitions remain outside
this execution owner. See [recursive design](design/PG18-QUERY-CTE-RECURSIVE.md).

CYCLE appends a typed mark and occurrence-owned record[] path. Each row's keys
form one ordered, non-NULL record, even when all fields are NULL. The candidate
compares that record with its actual parent's ancestry using retained NULL-aware
key equality, appends it and emits the closing row. Only SQL parent-mark
inequality TRUE permits another generation; equal or NULL custom marks can stop
at the anchor. UNION DISTINCT compares the complete augmented tuple.
Generated columns are explicit-only in the definition's own stars and visible
normally to external consumers. Mark values are grammar constants, not parameters
or arbitrary expressions. Persistent outputs must project ordinary columns,
marks or path::text rather than record[]. Stored/native replay keeps symbolic
CYCLE syntax, never transient paths. See [cycle design](design/PG18-QUERY-CTE-RECURSIVE-CYCLE.md).

SEARCH generates a typed sort value, not a traversal schedule. BREADTH FIRST
produces RECORD containing INT8 depth (starting at zero) and the current BY keys;
DEPTH FIRST produces _RECORD ancestry including the current key record. Sort by
the generated column to obtain the declared order. Numeric keys use numeric
comparison; NULL record fields sort after non-NULL fields. Equal keys remain ties.
Actual parent carries preserve ancestry through joins, and UNION DISTINCT uses
the complete augmented tuple even when the outer query omits the sequence.
SEARCH precedes CYCLE and output order is originals, sequence, mark, path.
Contained stars omit generated columns; explicit references remain available.
Record comparison demands reached key providers lazily: JSON can be emitted but
cannot be compared when its field is reached. This adds no general array/index
or named-composite ordering capability. Persist original scalars or seq::text,
not anonymous RECORD/_RECORD outputs. Stored/native replay retains symbolic
SEARCH, while prepared executions own fresh ancestry and retain stable public
RECORD/_RECORD result identity across key-field reanalysis. Internal occurrence
shapes and parameter/catalog fences remain exact.
See [SEARCH design](design/PG18-QUERY-CTE-RECURSIVE-SEARCH.md).



Modifying definitions are separate mandatory execution roots, not lazy SELECT
projects. The existing command binders share one parameter/dependency owner;
command-occurrence target contexts switch inside one StatementScope, operation,
lock ticket, WriteStage and data view (`visibility.View`). The complete physical source/target
union is captured before effects. Every physical scan uses statement-entry
committed/prior-transaction roots, not sibling stage writes. Constraints retain
their shared private-stage visibility, while RETURNING explicitly communicates
new rows to other consumers.

Each modifying producer settles once to completion and exposes the existing
typed finalized RETURNING buffer for independent replay. Hints cannot fold it.
Unused/no-RETURNING definitions still execute; executing LIMIT 0/false/partial
main consumers do not suppress them. SELECT definitions remain demand-driven.
Main only supplies adapter rows/schema/count. Invocation/source cleanup and any
error/cancellation veto precede one atomic Finish for every target; successful
eager rows retain owned data and metadata after later writes/native replacement.
Prepared handles retain signatures/analysis identities, never producer/data state.

Modifying WITH is top-level only. Referenced no-RETURNING definitions and
ordinary/materialized view or cursor persistence are rejected at their command's
proper semantic stage. CTAS/SELECT INTO reuse scoped catalog staging and publish
creation plus auxiliary effects together. Existing CTAS WITH NO DATA intentionally
does not execute the query at all; it creates only empty schema. Native lexical
scheduling is not a PostgreSQL sibling-order promise, and overlapping writes to
one physical row remain unsupported/unpredictable. Sequence effects are not
transactionally undone; canceling a successfully published result does not undo
its writes.

Window offsets, partitions, permutations and overlays are execution-private.
Named definitions retain independent volatile identity while equivalent key sorts
can share work; no mutable window state enters prepared plans or catalogs.
ROWS/RANGE/GROUPS determine membership independently; EXCLUDE then removes the
current occurrence, its peers, or its other peers. TIES never inserts an absent
current row. Peers use all window ORDER keys; without ORDER the entire partition
is one peer group even for ROWS. count(*) returns0 for an empty masked frame;
sum(expr) returns NULL. Native direct count(*), row_number() and ranking results
are known nonnullable, while SUM results are known nullable. Stored view columns
retain the existing known-nullable relation metadata contract, including count
and ranking outputs.
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

rank(), dense_rank(), percent_rank() and cume_dist() require OVER and use the
full partition after WHERE/group/HAVING, before outer ORDER/LIMIT. Only window
ORDER keys define peers; NULLs peer and an orderless partition is one peer group.
For N rows and the current zero-based peer interval [s,e), with zero-based peer
ordinal g, results are `s+1`, `g+1`, `s/(N-1)` (zero for N=1), and `e/N`.
The first two return INT8 and the latter two FLOAT8, all nonNULL. Ties therefore
leave rank gaps; terminal ties can keep percent_rank below one. Empty input
retains the complete schema without emitting a row. Ranking ignores membership
and exclusions, including ROWS frames, and alone does not evaluate runtime frame
offsets. A frame-sensitive count/SUM/value sibling still demands its own bounds.
Positive-argument calls retain the real hypothetical-set aggregate overload and
its WITHIN GROUP requirement; star selects the zero-argument window overload.
Direct/prepared, INSERT SELECT and stored views use the same window stage and
native persistence paths. See [ranking proof and smoke](../reference/query_order/README.md#ranking-windows).

first_value(expr), last_value(expr) and nth_value(expr,n) return the input's
concrete type from the admitted frame, including domain/enum/array identities.
The default ordered RANGE ends at the last current peer, not the partition tail;
an explicit UNBOUNDED FOLLOWING frame selects across the whole partition.
NULL values are respected, not skipped: they consume ordinal positions.
For each actual nth_value invocation, n is evaluated on the current output row;
NULL n or an absent one-based member returns a typed NULL, and n<=0 raises22016
`argument of nth_value must be greater than zero`. Empty/reversed/excluded frames
also return NULL. Computed value outputs are known nullable even for NOT NULL
inputs. Value expressions evaluate only on a selected member, not every source
row; volatile values are reevaluated for every actual executing call/output.
Upper sort-input target matching can project an actual retained whole window
output under multiple equal labels. Two unmatched postponed volatile calls keep
independent executing slots, including equal calls with an unrelated outer
ORDER key. This neither caches a selected value across rows nor merges genuinely
different value/ordinal expressions. Both children retain grouping, authorization,
dependency, capture and descriptor-remapping ownership.

lead(value [,offset [,default]]) and lag use the complete partition, not the
current frame or its exclusions. Omitted offset is1 and omitted default is a
typed NULL. Offsets are signed INT4: zero selects the current member; negative
offsets reverse direction, including MinInt32 without negation overflow.
The offset expression runs on each current row first. NULL returns a typed NULL
without evaluating either value or default. Otherwise value runs only on the
selected row, or default only on the current row when that target is absent.
A selected NULL remains NULL rather than using the default. Volatile values are
not cached even when several outputs select the same member. Retained whole
sort targets follow the matching rule above; differing defaults remain distinct.
Frames do not choose these members, but written frame offsets still undergo
runtime NULL/negative validation, including when no frame-sensitive call exists.

One-/two-argument lead/lag overloads use anyelement: the value must already have
a concrete type. The three-argument overload uses anycompatible for value and
default, excluding the offset from common-type selection. Unknown-only values
then resolve to TEXT; an agreed known domain is preserved, while domain/base or
different domains resolve through their base types. Known offset arguments must
implicitly coerce to INT4; unknown literals/parameters infer INT4. Common result
collation includes the default even when its runtime branch is unreachable.
Computed outputs are known nullable, have typmod-1 and no physical column origin,
including empty results. All three children retain grouping, scalar-query demand,
authorization, dependency, capture, lowering and destination-remapping ownership.
Held statements retain no runtime partition/argument cache; errors publish no
partial output or INSERT prefix, and subsequent valid executions start fresh.


ntile(buckets) returns INT4 labels1..B across the complete ordered partition,
with the first N%B buckets receiving one extra row. For six rows and four
buckets the labels are1,1,2,2,3,4, not ceil(row*B/N). More buckets than rows emit
labels1..N; tied peers may split, so use unique window ORDER keys when a label
must belong to a particular row. Frames/exclusions do not affect ntile results
or demand runtime offsets when ntile is the only provider; frame-sensitive
siblings still demand their own frame. Static frame analysis always applies.

The bucket argument is evaluated on the current row only until initialization
succeeds. Initial NULLs return typed NULL without advancing bucket membership;
the next row retries, with all initial NULL rows still in the denominator.
For five rows with bucket arguments NULL,2,0,NULL,-1, results are NULL,1,1,1,2.
After the first positive argument, later arguments are not evaluated at all.
A reached nonpositive value raises22014
`argument of ntile must be greater than zero`, without a parser position.
Empty input and LIMIT0 suppress runtime bucket evaluation; nonempty EXISTS
still demands the window target. The formal is integer: smallint and existing
integer domains coerce implicitly; bigint/text/boolean/numeric signatures do not.
Unknown strings, NULL and parameters infer INT4 through existing coercion.
Computed ntile outputs are known nullable even with positive constants.
State belongs to each actual call/partition/execution, never a held plan or view.
Bucket expressions retain grouping, scalar-child, authorization, dependency,
capture and remapping ownership, including when runtime evaluation is suppressed.
Upper sort-target matching follows the retained whole-window-output rule above,
not blanket merging of volatile calls.

Arguments participate in authorization, grouping, capture and descriptor remap
even when runtime membership is empty. Window providers beyond the ones above,
MONEY/INTERVAL SUM, count(expr), FILTER, DISTINCT, call-local aggregate ORDER,
explicit IGNORE/RESPECT NULLS syntax and FROM LAST remain excluded.
Native array/tree aggregate ordering and distinct capability limits are separate
from their represented structural equality. Scalar range comparisons/operators
use separate providers; range grouping and temporal range aggregates remain excluded.

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
