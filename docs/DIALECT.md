# SQL dialect

Supported SQL surface, native limits and intentional PostgreSQL differences. See
[README](../README.md) for getting started, the [SQL reference](SQL.md) for detailed
feature contracts, and the [development guide](DEVELOPMENT.md) for contributor instructions.

## Supported surface and limits

CREATE TABLE supports unqualified or schema-qualified names with SMALLINT/INT2, INTEGER/INT/INT4, BIGINT/INT8, NAME, TEXT, BOOLEAN/BOOL, NUMERIC/DECIMAL/DEC[(p[,s])], REAL/FLOAT4, DOUBLE PRECISION/FLOAT8, FLOAT[(p)] (1–24 real, 25–53 double; omitted p double), DATE, TIME[(0–6)] [WITHOUT TIME ZONE], TIMESTAMP[(0–6)] [WITHOUT TIME ZONE], UUID, BYTEA and optional NULL/NOT NULL; INSERT VALUES supports optional target columns/multiple rows; SELECT supports mixed stars or scalar literal/query-column/parameter targets with FROM, scalar output labels, binary scalar comparison leaves and Boolean AND/OR/NOT with grouping in targets and WHERE, bare scalar WHERE in contextual BOOL, and expression/output-label/ordinal ORDER BY with multiple keys and per-key ASC/DESC/NULLS FIRST/LAST. FROM remains required outside identity/settings special forms and the selected null-count-only form. Values include exact decimal fractions/exponents, integer/radix literals inferred INT4/INT8/NUMERIC by signed range, quoted strings, TRUE/FALSE/NULL, 1-based `$n` and explicitly typed date/time/timestamp literals. Float4 stores binary32, Float8 binary64; both export float64, and all NaNs compare equal above non-NaNs. Timestamp input uses fixed MDY/ISO civil semantics, validated ignored zones, BC/infinity, two-stage precision rounding and transaction-owned UTC now-like inputs; prepared constants freeze while parameters reread execution start. Finite public timestamps are time.Time, infinities owned bytes; time.Time parameters import original civil fields, not UTC-converted instants. Timestamp-with-zone/DateStyle/TimeZone settings remain excluded. Quoted identifiers, nested comments, doubled quotes/apostrophes and newline string concatenation follow README's C-locale UTF8 subset. Unresolved output parameter occurrences fall back to TEXT after filter analysis, retaining their initially known type: a target-first unknown occurrence conflicts with a later non-TEXT constraint (`42P08`), while TEXT target/filter reuse works. Same-context repeats work, unused gaps fail, mixed assignment destinations remain unsupported. Text/parameter inputs coerce through type input functions; INSERT assignment casts and comparison operator checks differ. WHERE keeps only TRUE. Defaults remain ASC NULLS LAST/DESC NULLS FIRST; explicit NULL placement overrides direction. See [expression ordering](QUERIES.md#expression-ordering--change-note-changelog).

FROM supports comma products, CROSS JOIN, JOIN/INNER JOIN ON or USING, and
NATURAL [INNER] JOIN, chains and parentheses. Table/whole-join aliases with or
without AS support positional column aliases; qualified columns and mixed stars
resolve the visible namespace. USING's special AS alias exposes merged keys
without hiding operands. USING key order and NATURAL left-common-column order,
duplicate multiplicities and NULL nonmatch follow PostgreSQL18.0. Merged common
types may retain either physical origin or a synthetic conversion origin.
Every source is authorized and captures one coherent selected statement view.
Prepared execution rebinds all sources; eager results survive native Reset/Restore.
Outer/LATERAL/derived/CTE/function FROM remain excluded.
See [join contracts](QUERIES.md#inner-and-cross-joins--change-note).
Feature smoke: `make smoke RUN='^TestQueryJoinsSmoke' VERBOSE=0`;
independent pinned reference: `make reference RUN='^TestReferenceQueryJoins' VERBOSE=0`.

Persistent CREATE VIEW supports the current SELECT body, optional prefix output
names, security_barrier/security_invoker Boolean options, nested ordinary reads
and INSERT SELECT. Bound relation/attribute identities survive base renames and
unrelated column compaction; dependency-aware RESTRICT/CASCADE integrates with
existing mutation paths. Zero-column views preserve source occurrences.
Empty SELECT target lists use the same source path (`SELECT FROM relation`),
retaining row occurrences independently of output width.
`pg_catalog.pg_class`, `pg_rewrite` and `pg_views` are real bounded normal query
sources with OID/internal CHAR metadata; their exact supported columns and
unsupported tree/array fields are listed in [the view contract](SQL.md#views).
OR REPLACE, recursive/TEMP forms, check options and view writes, general ALTER/
DROP VIEW, full PostgreSQL catalogs/RLS/UDFs/optimizer facilities remain excluded.
Native fixture controls preserve definitions rather than materialized results.
Feature smoke: `make smoke RUN='^TestViewSmoke$' VERBOSE=0`.

SELECT targets support `AS label` and eligible bare labels for view definitions
and normal output metadata. ORDER resolves output labels and ordinals alongside
input expressions and accepts multiple keys. WHERE/HAVING do not expose output
labels; GROUP BY retains its input-name priority and existing ordinal rules.

OID is also a supported column type: distinct unsigned32 type26, native int64
results, unsigned comparison/order and directional integer casts. See
[OID scalar semantics](TYPES.md#oid--unsigned-scalar-change-note) for text syntax,
signed-bit reinterpretation versus checked INT8 conversion and adapter metadata.
It adds no metadata catalog population, object lookup/allocation or OID arithmetic.
Smoke: `make smoke RUN='^TestOIDSmoke$' VERBOSE=0`.

Table SELECT and shared INSERT SELECT also support scalar/multiple GROUP BY
keys, one-based output ordinals, contextual BOOL HAVING, and count(*) as the
initial aggregate (count/INT8/int64). Global empty input returns one count0;
empty keyed grouping returns no rows with full schema. HAVING alone creates a
global group, and hidden count calls can filter groups without changing output
metadata. Typed NULL keys compare equal; numeric scale, NaNs/signed zero and
BPCHAR padding use typed equality, not text keys. Group order is unspecified and
projection duplicates remain separate occurrences. Ungrouped SELECT/HAVING/ORDER
source leaves and aggregate calls in WHERE/GROUP BY give positioned42803.
Bare int32 integer keys are output ordinals (including minus/parentheses);
out-of-list keys42P10 and raw noninteger constants42601. Unary-plus numeric keys
are constant expressions, not ordinals. Count-star requires table SELECT or a
live-column SELECT grant; actual key/HAVING/hidden ORDER reads retain column ACLs.
Eager group finalization preserves cancellation, result ownership and INSERT
atomicity; public Next performs no aggregate work. See [grouping](QUERIES.md#grouping-and-count--change-note).

Grouping does not admit standalone SELECT without FROM, count(expr), count(),
other aggregates, FILTER/DISTINCT/in-call ORDER/OVER, DISTINCT/ALL grouping,
grouping sets/ROLLUP/CUBE/empty grouping sets.
No-FROM aggregate exclusion applies recursively across all targets, including
siblings of selected sequence/REGCLASS singleton expressions. Group-expression
constant identity preserves representation (including NUMERIC scale), separately
from runtime typed grouping equality.
Feature smoke: `make smoke RUN='^TestQueryAggregateSmoke$' VERBOSE=0`;
real independent oracle: `make reference RUN='^TestReferenceQueryAggregate' VERBOSE=0`.

Simple/searched CASE, COALESCE, NULLIF, GREATEST and LEAST are scalar constructs
in every existing general-scalar consumer, including DEFAULT/native roundtrip.
CASE and COALESCE skip unneeded runtime children; NULLIF and extrema demand all
arguments. All children still undergo analysis and authorization. A bounded
execution-only constant simplifier can reject constant division by zero beneath
a row-dependent CASE/COALESCE, while decisive constant branches prune their
unneeded runtime expressions. This is not general Boolean short-circuiting.
Common-type and equality-overload resolution execute across the already-supported
scalar types, including implicit DATE→TIMESTAMP; this does not add general casts,
ordinary routines, arrays/VARIADIC or ordinary relationless SELECT.
See [conditional expressions](QUERIES.md#conditional-expressions).
Feature witnesses: `make smoke RUN='^TestConditionalSmoke$' VERBOSE=0` and
`make reference RUN='^TestReferenceConditional' VERBOSE=0`.

INSERT additionally accepts SELECT sources with the existing projection/filter/
expression-ordering surface, parenthesized sources and INSERT-only no-FROM literal/
parameter/scalar projections. Empty `(SELECT)` is one zero-width occurrence;
`SELECT FROM source` retains table occurrence/filter semantics. These do not
expand standalone SELECT or standalone VALUES. Explicit targets match source
schema width; implicit targets map first N live columns. Omitted columns use
declared defaults or typed NULL and enforce NOT NULL; serial remains excluded
with separate real-PG witnesses. Sequence calls are supported in the bounded
scalar consumers below, not in declared DEFAULT expressions.
Already-supported casts and null/truth/distinct predicates, DATE and BYTEA are
preserved in INSERT sources. Shared SELECT analysis retains current cast result
names. Recursive conversion preflight uses the imported execution frame, outside
the engine lock and before source capture; analyzed query nodes remain immutable.
Selected sequence calls also work in INSERT SELECT/VALUES producers through
that same binder and StatementScope; sequence FROM sources capture the same
three-value row as ordinary SELECT. Cast preflight visits sequence arguments
without evaluating the sequence call. Prepare consumes no sequence values.

Plain or parenthesized undecorated VALUES remains destination-context input.
VALUES with ORDER BY expressions, generated `columnN` names, ordinals and
per-key direction/NULL placement uses column-wise common typing and
implicit coercion before destination assignment. Only unsigned integer tokens
within int32 are output positions (also after unary minus); larger tokens,
fractions/exponents, strings, BOOL and NULL are non-integer raw constants.
Thus `-2147483648` is not an ordinal despite its signed value fitting int32.
Unary-plus numbers such as `+2`, `+1.5` and `+2147483648` are validated constant
expressions, never ordinals; every source occurrence has the same key and no
peer order is promised.
Raw parsing retains these constants and consumes the entire script. Target
relation/column analysis and VALUES width/common typing precede ORDER diagnostics;
a later raw syntax error wins before any analysis or prefix effects.
Unknown-only columns become TEXT; known TEXT does not
gain integer input semantics. Decorated VALUES assignment mismatch has42804/
rewrite-or-cast Hint/no Position (generated output Var); SELECT output assignment,
VALUES common-type/width and ORDER key diagnostics keep their own occurrence
positions. Mixed numeric columns use existing directional implicit casts, not
text-export/reparse or a transitive cast graph.
Missing VALUES ORDER names retain42703 and the key's original Position. A
bounded, case-sensitive character-distance search of this source's generated
names supplies a `*VALUES*.columnN` Hint for one or two nearest candidates;
three-way ties or distant names have no Hint. This does not extend ordinary
relation name resolution or its diagnostics.

Query INSERT has command-only metadata, exact zero/one/many occurrence counts,
fresh prepared source binding and a frozen self-source including earlier own
transaction writes. Both relation identities are retained: target RX first,
source AS second, independent target INSERT/source SELECT column manifests
(including WHERE/hidden ORDER). Empty SELECT/INSERT column manifests require
table rights or the corresponding privilege on any live column, even for a
zero-row source; dropped-column grants do not qualify. Eager source evaluation is unlocked; one complete
converted/constraint-checked batch reaches atomic WriteStage publication, and
ordinary cancellation/epoch errors keep their existing adapter boundaries.
Target-list names and duplicates are checked before source lookup/acquisition;
a missing or blocked source cannot mask a target-list diagnostic. Required
cursor cleanup and a final cancellation check also veto an otherwise complete
batch before publication.
Dump serializes persisted typed rows as literal VALUES. Restore rejects nonnil
query INSERT sources0A000 before trusted execution; canonical Dump roundtrips
and retained result ownership remain supported. Native public SQL/API ACL mutation
is absent: exact positive column grants use the internal consumer seam in units,
while reference-only GRANT witnesses identify this boundary explicitly.

Feature smoke: `make smoke RUN='^TestInsertSelectSmoke$' VERBOSE=1`.
Combined real-oracle feature reference (not a PostgreSQL-only empty regex):
`make reference RUN='^TestReferenceInsertSelect' VERBOSE=1`.
INSERT SELECT preserves raw unknown string/NULL and unresolved parameter output
occurrences until destination assignment; ordinary standalone SELECT still owns
its existing TEXT fallback. An output occurrence unresolved before a later WHERE
inference cannot be globally retagged: an incompatible destination reports42P08
at that occurrence. Already-known INT4 outputs may assignment-cast to BIGINT
without changing the input parameter type; per-target NUMERIC/time/timestamp
typmods size each value independently.

DELETE FROM [schema.]target [[AS] alias] [WHERE predicate] removes real live row
occurrences and retains the table/schema. WHERE reuses typed scalar comparisons,
Boolean AND/OR/NOT and True-only selection, including contextual BOOL parameters.
DELETE predicate columns accept visible target/alias qualification; alias hides
the original target name. Query FROM aliases/qualification are separate supported features. Direct
and held prepared executions report exact affected counts, zero for no matches,
no result relation and unsupported LastInsertId.
Qualified missing-column diagnostics use decoded identifier components without
SQL quoting, including `column D.Zzzzz does not exist` for `"D"."Zzzzz"`.
DELETE admits the same nullness, truth and DISTINCT predicate forms as SELECT,
including qualified target/alias operands. Baseline scalar CAST/`::`/function
casts admit unqualified children only; qualified cast children remain excluded
with whole-input rejection before effects. Frame-only conversions precede
executor ACL checks and candidate capture even on empty input or decisive Boolean
branches; row-dependent conversions still evaluate per row, without guaranteed
logical short-circuiting. With stored `s='oops'`, native `FALSE AND int4(s)=42`
and `TRUE OR int4(s)=42` both fail22P02 and preserve all original rows; empty
sources succeed with count0 because no row conversion is demanded. Pinned
PostgreSQL optimizes these specific decisive predicates (FALSE AND count0 with
originals, TRUE OR countpopulation with no survivors), also when the unused cast
takes an invalid TEXT parameter. Native frame-only preflight still fails22P02
on empty or populated input. This is a bounded optimization difference, not a
general PostgreSQL short-circuit or operand-order guarantee.

Execution requires table DELETE plus SELECT on actually read predicate columns,
never a fabricated SELECT requirement for omitted/constant WHERE. Prepare binds
metadata and acquires RX but no row hold. Data analysis fixes transaction
characteristic timing and the REPEATABLE READ snapshot before relation waits.
READ COMMITTED/UNCOMMITTED capture execution candidates after relation grant:
peer commit skips an ended predecessor, rollback permits deletion, and newly
matching inserts during a wait remain outside the fixed set. REPEATABLE READ
rejects newer committed heads/ends with 40001 before liveness/predicate recheck.
Distinct rows and ordinary SELECT remain independent.
Successful own earlier insert/delete commands overlay subsequent commands. One
atomic frontier publishes all insertions/deletions before holds are released;
failure/cancellation discards everything admitted, including earlier explicit
private effects, while SQL remains 25P02 until recovery.
DELETE lifetime/publication authority uses the exact existing storage-shape
generation plus object/epoch and row/version/ordinal, not descriptor pointer
equality. Compatible TABLE/COLUMN COMMENT and ACL revisions preserve staged work
and fixed candidates through waits; replacement/removal/recreation or incompatible
shape changes still fail. COMMIT does not reauthorize already staged DELETE;
each fresh prepared Execute still checks current table/read-column privileges.
INSERT SELECT sources share live membership, including earlier own deletions;
self-insert captures survivors before its new batch and empty live sources insert zero.

Immutable end roots preserve retained membership/schema, duplicates and zero-width
occurrences. Dump serializes committed survivors only, never private deletes or
DELETE/tombstone history. Restore's whitelist stays unchanged. Self/foreign catalog
replacement and DROP-column shape changes preserve live membership; foreign
imports assign fresh destination identities and omit source history. NOT NULL
scans ignore deleted NULLs but reject live NULLs and enforce later INSERT. Removing
a row cannot violate supported NOT NULL; genuine FK rejection is an explicitly
PostgreSQL-only witness. Native FKs, triggers, DELETE USING/CURRENT OF/
RETURNING, CTE/inheritance targets and additional row modes remain excluded.
Smoke: `make smoke RUN='^TestDeleteCoreSmoke$' VERBOSE=1`;
reference: `make reference RUN='^TestReferenceDeleteCore' VERBOSE=1`.
Integrated expressions: `make smoke RUN='^TestDeleteCoreIntegratedExpressionsSmoke$' VERBOSE=1`.

Column DEFAULT declarations admit outer scalar/typed temporal/existing cast/
INT4/INT8 unary and binary arithmetic/comparison and IS [NOT] DISTINCT FROM syntax.
CAST and postfix casts reuse existing baseline, character-family and bounded
OID conversions; function-style casts additionally admit oid, not general functions.
Logical NOT/AND/OR and null/truth tests require parentheses
(e.g. DEFAULT (NULL IS NULL)), as PostgreSQL's restricted default grammar requires.
Columns (0A000) and parameters (42P02) remain prohibited inside casts and decisive
branches. Unknown input binds at CREATE Execute; destination sizing/conversion
is execution-demanded. CREATE utility Prepare remains analysis-free.
Each column's type resolves before its ordered NULL/NOT NULL/default declaration
validation, before later elements. A second DEFAULT is42601 at that token;
earlier type/nullability errors retain precedence. Complete declaration structure
and NOT NULL merging precede cooking any default expression. Restore shares this
binder, with no replacement publication on failure.
Omitted INSERT columns and per-cell DEFAULT use declared expressions or typed
NULL; explicit NULL bypasses defaults. SELECT and ORDER-decorated VALUES sources
use omitted destination defaults. One execution-only default-demand phase
handles both ordinary and query INSERT after supplied-value/source preflight.
Only defaults consumed by some ordinary row or omitted by the query are planned;
a column explicitly supplied in every row, including NULL, bypasses this phase.
Immutable conversions and closed integer arithmetic are demanded even for an empty
query source. This bounded default-only recursive
planning handles the left first. Concrete non-NULL BOOL TRUE on OR or FALSE on
AND replaces the whole execution node with a private BOOL literal, without
visiting the discarded right branch. Existing typed operations fold closed constant
arithmetic/comparison/predicate/NOT children only after their required children.
NULL/nondecisive left operands still demand right operations; a bad left is not
rescued by a decisive right. CREATE continues analyzing both operands.
The schedule uses lazy private copies; catalog definitions remain unfolded.
Remaining runtime expressions evaluate only in rows/cells consuming the default.
Outside conditional boundaries, ordinary query/DELETE/UPDATE/supplied preflight
and runtime logical evaluator behavior are unchanged; no general Boolean
short-circuit/optimizer promise is made.
ORDER-decorated DEFAULT is42601 `DEFAULT is not allowed in this context` at its
marker; direct Execute/prepared Prepare. Ordinary and parenthesized undecorated
VALUES retain requests. LIMIT and FROM-less WHERE remain outside the source
grammar, not new default features. Public targetless DEFAULT VALUES requests one
all-default row, including on zero-column tables; a target list before DEFAULT
VALUES fails42601 at DEFAULT. Supplied INSERT/settings scalars are not widened.
Recursive analysis reports earlier child Boolean/input/operator errors before
later prohibited occurrences; valid decisive branches still analyze both children.
Defaults are immutable attribute-keyed descriptor definitions, not cached
evaluated values or nullable metadata. Plain LIKE excludes defaults; owner,
comment and NOT NULL changes preserve them, and DROP COLUMN removes its key.
Rounded finite endpoint literals keep the type-owned TIMESTAMP(0) encoding with
exactly one introducer, including within default comparison expressions.
Existing null/truth/distinct default nodes serialize recursively with parentheses,
without Eval, and retain shared typed predicate semantics through Dump/Restore.
Integer arithmetic defaults validate their private operator identity against the
existing concrete-width provider and serialize grouped operands without Eval.
Operand widths remain intact on replay; destination BIGINT does not widen INT4
operations. Ordinary arithmetic stays row-owned outside consumed-default planning.
Existing explicit character CAST typmods preserve Unicode truncation and BPCHAR
trimming through controls; destination assignment retains overlength errors.
Explicit default coercions validate their actual construction mode and retain
canonical CAST structure. Known TEXT cast children remain typed, preserving
deferred conversion timing through Restore. DATE leaves retain typed introducers
recursively, so civil BC ordering cannot silently become TEXT ordering on replay.
Top-level BYTEA defaults use the existing quoted hex codec in destination context;
public results remain raw bytes. No typed BYTEA compound or DATE/BYTEA cast is added.
Dump serializes expression structure/captured temporal input without Eval and
existing rows explicitly. Restore uses the same binder and public DEFAULT
requests, staging atomically. Temporal 'now' input captures CREATE's operation
clock; no new volatile producer, general function, subquery, cast family or arithmetic
operator/type is implied. Serial, identity, ALTER DEFAULT and UPDATE DEFAULT remain excluded.
Sequence/volatile calls remain excluded specifically from declared DEFAULT expressions,
even when constant NULL could otherwise eliminate the call; disk/WAL,
network protocol, native ABI extensions and external pg_dump/custom archives
are specifically non-engine mechanisms, never permission for successful stubs.
Feature smoke: `make smoke RUN='^TestDefaultSmoke$' VERBOSE=1`.

Scalar comparisons admit `=`, `<>`, `!=`, `<`, `<=`, `>` and `>=`; `!=` is the
`<>` alias. Both operands may be existing scalar literals, parameters, columns
or admitted character casts.
Every result is BOOL (`?column?`, native bool ScanType, no physical-column origin),
including empty results; either NULL yields typed BOOL NULL/UNKNOWN, which WHERE
does not keep. INT2/INT4/INT8 compare exactly across widths without narrowing or
floating-point conversion. Existing NAME/TEXT, BOOL, FLOAT4/8, exact NUMERIC,
UUID, BYTEA and same-type DATE/TIME/TIMESTAMP providers keep their existing admission and
ordering (C text order, FALSE before TRUE); no cross-temporal casts are added.
Unknown quoted strings/NULL take a known peer's type; two unknowns choose TEXT.
Known TEXT is not implicitly integer/BOOL. Parameter occurrences copy their
then-known shared type before comparison resolution: `i=$1,j=$1` can reuse INT4
against BIGINT, whereas a later BOOL comparison with that known INT4 fails42883.
An earlier bare `$1` still has TEXT output fallback and conflicts with later
INT4 inference (42P08). Inputs' Go kinds never choose SQL types.
Incompatible operands fail eagerly in Query/Prepare at the operator (42883);
selected literal input errors retain the literal position (22P02/22003).
No comparison implies row ordering. SELECT projection labels and expression
ORDER keys are supported; comparison chaining, casts outside the bounded
baseline and character-family sets, and comparison in INSERT/settings remain
excluded. Feature smoke: `make smoke RUN='^TestCompareOpsSmoke$' VERBOSE=1`;
independent pinned reference: `make reference RUN='^TestReferenceCompareOps' VERBOSE=1`.

BETWEEN predicates support `BETWEEN`, `NOT BETWEEN`, `BETWEEN SYMMETRIC`
and `NOT BETWEEN SYMMETRIC` through the existing comparison/logical binder.
Endpoints are inclusive; reversed ordinary bounds match no non-NULL values,
while symmetric forms accept either order. NOT forms use the complementary
inequalities. NULL follows binary comparison and SQL AND/OR truth tables:
`5 BETWEEN NULL AND 3` is false, its NOT form is true, and its symmetric forms
are UNKNOWN. Each binary comparison resolves its own types; there is no
three-argument common type or bounds sorter. Generated operator diagnostics
point to BETWEEN (or NOT), while input errors retain their original positions.
Group Boolean/nested endpoints, for example
`TRUE BETWEEN (1 BETWEEN 0 AND 2) AND TRUE`. These predicates compose in
existing SELECT/WHERE, DELETE, UPDATE, CASE, INSERT SELECT, grouping/HAVING
and parenthesized DEFAULT paths; they do not expand ordinary INSERT/settings,
no-FROM SELECT, comparison chaining, casts/types or provider admission.
Prepared temporal occurrences at a shared source position retain independently
typed frozen values (including precision); mixed DATE/TIMESTAMP endpoints do
not require a cross-temporal operator. Native Dump/Restore preserves grouped
and nested BETWEEN default definitions without evaluating them, so DEFAULT
inserts continue to use those definitions after replay.
ASYMMETRIC remains excluded. Feature smoke:
`make smoke RUN='^TestBetweenSmoke$' VERBOSE=0`; independent pinned reference:
`make reference RUN='^TestReferenceBetween' VERBOSE=0`.


UPDATE supports schema-qualified/quoted target relations, ordered scalar SET
assignments and optional Boolean WHERE. RHS literals/parameters/old unqualified
columns/comparisons/logical operators/grouping/selected null-count roots and
existing INTEGER/BIGINT arithmetic use the same expression binder; aliases,
FROM, row/subquery assignment, CURRENT OF, DEFAULT, RETURNING and general
casts/functions remain excluded. Arithmetic retains its existing operand/type
boundaries, NULL propagation and division/overflow errors, with no new provider.
Assignments are simultaneous against one old row, preserve unassigned fields,
apply destination type/typmod and then enforce whole-row NOT NULL. Counts include
unchanged matches and duplicate occurrences once each; no match is zero.
Analysis errors are eager even on empty/no-match input; unknown literal input
belongs to analysis, immutable destination casts fold during Execute (including
empty/WHERE FALSE), and row-dependent conversion runs only for qualifying rows.
Duplicate assignments are42601 without Position; assignment42804 retains the
rewrite/cast Hint and RHS expression-start Position in direct execution and
semantic Prepare. Grouping is transparent, postfix casts retain the operand's
start, and positions count UTF8 characters, not bytes. This does not change
operator-resolution errors' operator-token positions.
Runtime NOT NULL includes permission-filtered updated-row Detail
and schema/table/column but no Constraint/Position. Table SELECT, default owner
authority and superuser retain the complete unlabelled row. Otherwise Detail
lists modified targets plus SELECT-readable retained columns in physical order
as `(names) = (values)`, even when column grants cover every column. Unreadable
retained values are not formatted or disclosed; carrying them does not require
SELECT. Captured output settings and UTF8-safe field truncation remain unchanged.
REAL/FLOAT4 and DOUBLE PRECISION/FLOAT8 have direct assignment-only conversions
to each integer width: round to nearest with ties to even, then enforce signed
bounds (the positive 2^63 boundary is excluded, negative -2^63 is valid).
NaN/infinities/out-of-range rounded values report destination-specific 22003
without Position. NULL and provider ownership/context checks remain shared;
NUMERIC conversions retain their existing ties-away rounding.
The existing TIME/TIMESTAMP precision, UUID input/NULL, TEXT assignment and
NUMERIC ordinary/negative/wide-scale destination providers are reused for old
columns and fresh direct/prepared inputs. Late typmod failure discards the
complete statement. Bad parameter input has no native portal Context, unlike
PostgreSQL's unnamed portal; row SQL deadlines have no native tuple Context,
while independently asserted PostgreSQL tuple Context is structurally matched.
UPDATE contextual parameter-type conflicts use occurrence-positioned 42P08 and
old-versus-requested type Detail; other INSERT/settings destination-reuse 0A000
boundaries remain unchanged. Native TIME exports canonical clock strings,
while pq TIME uses civil time.Time; reference expectations preserve this distinction.

Committed storage retains one immutable physical head per stable logical RowID,
including ended heads; DELETE does not compact that vector. New VersionIDs
replace predecessors without changing physical cardinality. One visible/raw-index
mapping substitutes successful private heads before deletion filtering, including
earlier private INSERTs. SELECT and INSERT SELECT consume each live occurrence
once, preserving duplicates and excluding own UPDATE-then-DELETE or
INSERT-UPDATE-DELETE heads. Generic self/foreign catalog replacement selects live
heads through that same mapping once, detaches/remints them and omits ended history.
READ COMMITTED fixes candidates before waits; physical ordinals are hints, stable
RowIDs identify latest predecessors. Peer deletion makes waiting UPDATE count0;
changed peer UPDATE values require predicate recheck for UPDATE/DELETE without
widening the captured set. DELETE ends the actual latest version. Relation RX
and distinct mutually conflicting NO KEY UPDATE/row UPDATE modes use one manager
resource; ordinary readers/different rows do not conflict. Prepare takes RX, no
row locks or data scan. Head vectors and immutable end roots share one all-relation
storage/length/generation/end preflight and frontier install. Cancellation/failure
publishes none and discards both kinds of prior private work, releases holds and
leaves25P02 until end. Compatible COMMENT/ACL revisions preserve genuine rowshape
and storage identity, not descriptor-pointer equality; current authorization still
applies. Dump/ALTER see live committed heads and retained results keep old values.
UPDATE privilege on assigned targets and SELECT on actual RHS/WHERE reads are
checked independently; retained protected columns do not imply SELECT.
Smoke: `make smoke RUN='^TestUpdateCoreSmoke$' VERBOSE=1`;
reference: `make reference RUN='^TestReferenceUpdateCore' VERBOSE=1`.
Coexistence witnesses: `make test PACKAGES='./internal/engine .' RUN='^TestUpdateCoreDelete' VERBOSE=1`;
`make reference RUN='^TestReferenceUpdateCoreDelete' VERBOSE=1`.
The feature smoke includes private UPDATE/self INSERT SELECT/DELETE count2,
no-resurrection count0, peer visibility, rollback, commit and live control roundtrip.

Logical operators use PostgreSQL's three-valued BOOL semantics: FALSE AND NULL
is FALSE, TRUE OR NULL is TRUE, other undecided NULL combinations remain NULL,
and NOT NULL is typed BOOL NULL. Comparisons bind above NOT, then AND, then OR;
NOT associates right and AND/OR left, with parentheses overriding precedence.
There is no guaranteed left-before-right evaluation or short-circuit contract.
Every operand is analyzed, including decisive branches for types, dependencies
and actual-column authorization. Unknown strings/NULL/parameters contextualize
to BOOL, but concrete non-BOOL values fail construct-specific42804; bad Boolean
input is22P02. SQL diagnostics retain character positions. Logical outputs have
?column?/OID16/typmod-1/no collation or physical origin, native bool ScanType and
unknown nullability even on empty output; NULL exports as nil. Prepared frames
are fresh, while output metadata and captured logical rows survive EOF/Close
and native controls. `SELECT $1 AND TRUE,$1 FROM t` infers BOOL for both;
`SELECT $1,$1 AND TRUE FROM t` fails42P08/boolean versus text at the first target.
Existing scalar comparisons form Boolean leaves in targets, WHERE and ORDER
expressions. Logical INSERT, settings arguments and adjacent unsupported
expression forms remain excluded. Feature smoke:
`make smoke RUN='^TestLogicalSmoke$' VERBOSE=1`; pinned independent reference:
`make reference RUN='^TestReferenceLogical.*$' VERBOSE=1`.
PostgreSQL parses `SELECT TRUE AND FROM t` / `SELECT TRUE OR FROM t` as TRUE
targets with bare aliases and/or. Native bare-label parsing of these
keywords continue logical expressions; native42601 points to FROM at17/16.
Independent reference coverage asserts PostgreSQL's positive BOOL outputs and
native failures separately, without normalizing this capability boundary.

Selected `num_nulls`/`num_nonnulls` calls additionally work in SELECT projections
and nested selected arguments, accepting1–100 heterogeneous scalars without
common-type conversion. They are immutable, nonstrict and return nonnull INT4
(native int64) counts with function-name labels and no physical origin.
Explicit NULL positions count; zero, false and empty strings are nonnull.
Their decoded counts sum to the ordinary argument count.

The only added FROM-less form has every target rooted in a selected call, with
literal/parameter/nested-call arguments, no column references and no extra clauses.
It feeds a singleton through the existing binder/project/finalization pipeline,
returning one row and Exec RowsAffected1; table calls retain input multiplicity,
actual-used-column authorization and empty-result metadata.
Direct and prepared calls share this path. ANY does not infer a parameter type:
standalone `num_nulls($1)`/`num_nonnulls($1)` fail42P18 regardless of supplied Go
values. Later target TEXT fallback or WHERE inference does not rewrite an earlier
unknown ANY occurrence; that mismatch fails42P08 at the occurrence. Existing
bare-target42P08 and valid TEXT reuse remain unchanged.

Zero arguments fail42883 with the standard explicit-casts Hint; over100 fail54023.
Children are analyzed in order before the outer arity check; the first missing
column or nested-call error therefore precedes54023. An unresolved ANY parameter
is not a child analysis error, so over100 still fails54023 before whole-statement
parameter finalization.
Explicit VARIADIC scalar operands fail42804 at the operand, including unknown
strings/NULL; multiple ordinary/VARIADIC operands fail42883 lookup first.
Arrays are not admitted: PostgreSQL NULL-array/empty-array semantics are not
native support, and ordinary NULL is a single null argument. Calls remain excluded
from INSERT, WHERE and setting arguments. General calls, function qualification,
casts outside the bounded baseline and character-family sets, arithmetic,
DISTINCT, in-call ORDER BY, FILTER, OVER, star/named arguments
remain exclusions rather than PostgreSQL diagnostic-parity claims.
Computed arithmetic in table-backed selected null-count arguments fails native0A000
`arithmetic in null-count arguments is unsupported` at the operator, including
nested calls. Computed arguments in the singleton no-FROM form instead fail
the existing admission check with0A000 `SELECT without FROM is unsupported`
at SELECT (character1), before function binding. Former signed numeric literal
inputs remain admitted; arithmetic does not broaden this selected call surface.
Character casts are also ordinary call operands; the selected-call singleton
argument rules admit literal/parameter/nested-call casts without columns.
Feature smoke: `make smoke RUN='^TestNullCountSmoke$' VERBOSE=1`;
independent reference: `make reference RUN='^TestReferenceNullCount' VERBOSE=1`.

### Random functions — change note

Selected projection/nested-argument calls now include PostgreSQL18 `random`,
`random_normal` and `setseed`, through the existing direct/prepared expression
pipeline and selected-call-only singleton exception:

| Call | Result | Behavior |
|---|---|---|
| `random()` | FLOAT8 | Uniform `0 <= value < 1` |
| `random(INT4,INT4)` | INT4 | Inclusive bounds, including the full signed domain |
| `random(INT8,INT8)` | INT8 | Inclusive bounds, including the full signed domain |
| `random(NUMERIC,NUMERIC)` | NUMERIC | Exact inclusive decimal lattice at maximum input display scale |
| `random_normal([mean[,stddev]])` | FLOAT8 | Defaults mean0/stddev1; ordinary IEEE results, including negative deviation and specials |
| `setseed(seed)` | VOID | Finite inclusive [-1,1]; nonnull empty string result |

The decimal lattice can exceed2^64 points and retains numeric's full supported
display-scale/weight range; large coefficients do not pass through floating point.
Equal bounds return the endpoint without consuming a draw. Numeric results are
unconstrained, not resized to a source column typmod. Mixed INTEGER/BIGINT
chooses BIGINT; integer/NUMERIC chooses NUMERIC. Bounded FLOAT4/FLOAT8 calls are
not released overloads. Two unknown bounds and SMALLINT/SMALLINT are ambiguous
(42725 with the explicit-casts Hint); inferred parameter types do not depend on
supplied Go values. Missing signatures use42883 and the standard function Hint
at the function token's character Position.
The general PostgreSQL function-call limit is100 arguments: larger admitted calls
fail54023 `cannot pass more than 100 arguments to a function`, with function-token
Position and no Hint. Child-analysis errors precede that limit; calls within the
limit still resolve against each selected function's smaller released arities.

All selected random calls are volatile and strict. Preparing/describing them
does not invoke them. Literal NULL can eliminate a call and its volatile sibling
arguments after type analysis; runtime NULL still evaluates all sibling arguments
before suppressing the provider. Each evaluated row receives its own draw.
Table-backed arguments may use existing integer arithmetic/conditional/group
expressions; this does not widen the singleton arithmetic admission rule or add
general functions/casts, WHERE, DEFAULT, INSERT VALUES or settings-call support.

One lazy physical-session generator is shared by all overloads and normal calls.
Repeated valid seed repeats that session's subsequent mixed series. Pool return,
statement failure, rollback/savepoint rollback and native Reset/Restore do not
undo seed/draw effects; physical close loses the state. Peer sessions remain
independent, and read-only transactions allow these functions. The implementation
uses standard-library PCG; outputs are not cryptographic and exact PostgreSQL
PRNG sequence parity is not promised.

Reversed bounds fail22023 `lower bound must be less than or equal to upper bound`.
Numeric special bounds fail `lower/upper bound cannot be NaN` or
`lower/upper bound cannot be infinity`, lower checked first. Invalid seeds fail
22023 `setseed parameter <value> is out of allowed range [-1,1]`, using six
significant digits or NaN/Infinity/-Infinity. These execution errors carry no
Position/Hint/Detail and leave an existing seeded stream unconsumed.
VOID is result-only, distinct from typed NULL, and does not admit stored columns
or VOID casts/operators.
VOID ORDER keys (including output aliases and ordinals) fail analysis with42883,
`could not identify an ordering operator for type void`, and
`Use an explicit ordering operator or modify the query.` Hint. The error points
at the ORDER reference on empty or populated input, before reseeding or evaluating
any unrelated visible sequence output.

Feature smoke: `make smoke RUN='^TestRandomSmoke$$' VERBOSE=0`.
Independent pinned reference: `make reference RUN='^TestReferenceRandom' VERBOSE=0`.

### Scale and rounding functions — change note

The existing selected-call projection/nesting and call-rooted singleton path
also admits these immutable, strict PostgreSQL18 mathematical overloads:

| Function | Arguments → result |
|---|---|
| `scale`, `min_scale` | NUMERIC → INT4 |
| `trim_scale` | NUMERIC → NUMERIC |
| `round`, `trunc` | NUMERIC → NUMERIC; FLOAT8 → FLOAT8; NUMERIC, INT4 → NUMERIC |
| `sign` | NUMERIC → NUMERIC; FLOAT8 → FLOAT8 |
| `mod` | INT2,INT2 → INT2; INT4,INT4 → INT4; INT8,INT8 → INT8; NUMERIC,NUMERIC → NUMERIC |

Exact NUMERIC selects its numeric overload. Unary integer/REAL
`round`/`trunc`/`sign` select the preferred FLOAT8 overload. Unknown unary
`round`/`sign` also select FLOAT8, but unknown unary `trunc` fails `42725`
because PostgreSQL also has competing nonnumeric overload categories; those
providers are not added here. Numeric-only functions implicitly accept
integers, not known floating inputs. Scaled calls
accept SMALLINT scale widening, but not BIGINT/NUMERIC/FLOAT scale narrowing.
Mixed integer modulo selects the wider integer; integer/NUMERIC selects NUMERIC.
Two unknown modulo arguments are ambiguous (`42725`), not Go-kind inferred.
Missing signatures use `42883` and the standard explicit-casts Hint at the
function token. Invalid unknown literals use the selected input codec at the
literal position. Prepared signatures and result metadata remain fixed across
bindings; NUMERIC results have unconstrained typmod, even from sized columns.

NUMERIC rounding uses ties away from zero; truncation uses toward zero.
The scale request clamps to [-131073,16383] for round and [-131072,16383] for
trunc, independently of NUMERIC typmod's ±1000 scale limit. Output has
`max(scale,0)` fractional places. Carry beyond the full format fails `22003`,
`value overflows numeric format`, without a fabricated execution Position.
`scale(8.4100)=4`, `min_scale(8.4100)=2`, `trim_scale(8.4100)=8.41`.
Finite zero/integer minimum scale is zero. Scale/minimum-scale return typed
NULL for NaN/infinities; trimming/rounding/truncation preserve those specials.
NUMERIC sign returns unscaled -1/0/1 or NaN.

FLOAT8 round uses nearest-even in the pinned reference environment; PostgreSQL's
manual permits host-dependent floating tie behavior. FLOAT8 round/trunc preserve
negative zero, NaN and infinities. FLOAT8 sign returns positive zero for NaN and
either signed zero, unlike NUMERIC sign(NaN).
Remainder has the dividend's sign, with exact NUMERIC output at the greater
finite operand display scale. Finite modulo infinity preserves the dividend's
original scale. NaN wins even modulo zero; infinity modulo a nonzero operand
is NaN. Other zero divisors fail `22012`, `division by zero`, without Position.
Integer minimum modulo -1 is zero without overflow.

Every child is analyzed before strict NULL elimination. Runtime NULL evaluates
demanded siblings first. Execution simplification visits immutable children
before eliminating a strict literal-NULL call: `round(NULL,mod(1,0))` therefore
fails division by zero, while a row-dependent sibling may be eliminated.
Prepare/Describe never invokes math providers. Shared query/DML execution
preflight applies math simplification even without a conditional wrapper,
including selected-call singleton queries. Existing execution conditional
pruning/folding, grouping by projection ordinal, UPDATE/INSERT SELECT atomicity,
ACLs, view capture and
native replacement remain the shared consumers. No general function registry,
numeric arithmetic operators, qualification, new WHERE/DEFAULT/INSERT VALUES
or setting admission, or general SELECT-without-FROM is introduced.
Dump/Restore stores exact results and replays supported view syntax, not private
function state.
Selected calls remain projection/nested-call roots, not new CASE result leaves
or general GROUP BY expressions. Math around an existing CASE with scalar arms
and `round(count(*),1)` are admitted; unsupported `min` aggregates are not added.

Feature smoke: `make smoke RUN='^TestMathSmoke$' VERBOSE=0`.
Independent reference:
`make reference RUN='^TestReferenceMath(Signatures|NumericRoundScale|SignModulo|Diagnostics|Consumers)$' VERBOSE=0`.


Table SELECT projection/WHERE admit `IS [NOT] NULL`, `ISNULL`/`NOTNULL`,
`IS [NOT] DISTINCT FROM`, and Boolean `IS [NOT] TRUE/FALSE/UNKNOWN`.
These return nonnull BOOL with `?column?` names and scalar origin zero;
native ScanType is bool and expression nullability metadata stays unknown.
Nullness accepts every supported scalar type; Boolean tests contextualize
unknown inputs to BOOL, but concrete nonboolean operands fail42804 at the
operand. DISTINCT uses existing typed equality for nonnull inputs, considers
two NULLs equal and one NULL distinct; NOT DISTINCT complements it. Both
sides admit scalar atoms, comparisons or grouped predicates/logical expressions.
Unknown operands adopt the known opposite type, or TEXT if both are unknown.
Incompatible concrete types fail42883 at IS even on empty/all-null input. A bare NULL literal on
either side rewrites to nullness without equality inference; a nil parameter
does not. Thus `$1 IS DISTINCT FROM NULL` fails42P18/no Position just like
`$1 IS NULL`, regardless of supplied Go value. Earlier nested unknown parameter
occurrences do not retroactively acquire later slot types: `$1 IS NULL,$1 IS TRUE`
fails42P08 at the first occurrence, while reversed order fixes BOOL and works.
Root output TEXT fallback and mixed-assignment limits remain unchanged.
Selected occurrence consistency precedes shared-slot gaps/unresolved types:
`$1 IS NULL,$1 IS TRUE,$2 IS NULL` fails42P08/$1 at character8, not42P18/$2.
An inconsistent `$2` occurrence similarly precedes the missing `$1` slot.
Without occurrence inconsistency, `$1 IS TRUE,$2 IS NULL` retains42P18/$2
without Position; validation still precedes count-sized parameter allocation.

ANY arguments and selected null tests retain unknown occurrences in shared
binding order: `num_nulls($2),$1 IS NULL,$1 IS TRUE,$2 IS TRUE` fails42P08
for$2 at18; swapping its first two targets fails for$1 at8. A still-unresolved
ANY slot does not mask a later selected occurrence mismatch.
Parentheses group admitted atoms, comparisons, predicates and logical expressions.
Unary postfix chains nest left: `TRUE IS NULL IS TRUE` and `v IS NULL ISNULL`
both return false. The shared
nonassociative IS/ISNULL/NOTNULL precedence rejects an ungrouped predicate
competing with a binary DISTINCT right operand (`1 IS DISTINCT FROM 1 IS TRUE`
fails42601); explicit grouping admits the intended nesting. Scalar comparisons
bind more tightly than selected predicates, which bind above NOT, AND and OR.
`v=1 IS TRUE` and `(v=NULL) IS UNKNOWN` work in projection and WHERE.
Ordinary `v=NULL` still selects no rows; no transform_null_equals change.
Comparison operands admit bounded scalar arithmetic and baseline and
character-family casts, but not comparison chaining or nested logical/comparison
expressions. Predicates also accept these scalar expressions and use their
resolved type and expression start. General functions/casts outside the bounded
baseline and character-family sets, rows/composites and no-FROM scalar
queries remain excluded except for the selected call roots described above.
Null-count calls remain standalone projection roots, not predicate operands;
predicate expressions remain excluded from their arguments.
Ordinary INSERT VALUES/settings arguments remain scalar-only. INSERT SELECT
executes existing predicate and selected null-count projection roots through the
common query pipeline; a trailing `VALUES (1)` in
`INSERT INTO t SELECT num_nulls(v) FROM t VALUES (1)` still fails42601 at VALUES (42).
Genuinely unsupported calls retain their existing call guards.
Native controls retain finalized computed BOOL
values/metadata; dependency/ACL use includes both DISTINCT operands.
Feature smoke: `make smoke RUN='^TestCompareNullSmoke$' VERBOSE=1`;
native: `make test PACKAGES=. RUN='^TestCompareNull.*$' VERBOSE=1`;
real pinned reference: `make reference RUN='^TestReferenceCompareNull.*$' VERBOSE=1`.

Baseline explicit casts extend the scalar positions above: `CAST(expr AS type)`,
`expr::type`, and function-style int4/int8/text/bool calls, with nested/grouped
operands. Standard/postfix targets retain baseline declaration aliases; function
aliases integer/bigint/boolean/int are not admitted. The baseline matrix supports
INT4/INT8/TEXT/BOOL source and target types; character-family and bounded OID
edges are described separately. Other families, including identities, fail0A000.
The direct baseline matrix permits integer width conversion, integer
text output, TEXT input to integers/BOOL, BOOL text output as true/false and
INT4↔BOOL (nonzero true; false0/true1); INT8↔BOOL remains unsupported.
NULL keeps target metadata; unsupported pairs cannot bypass analysis with NULL.
Assignment/implicit policies do not gain these explicit edges.
OID adds INT2/INT4/INT8→OID implicit, OID→INT4/INT8 assignment, OID identity
and explicit TEXT I/O edges; CAST/::/oid(...) reuse the same providers.
OID→INT2 is42846, including NULL. No unrelated INT2 cast family is admitted.
No new ordinary no-FROM SELECT root is admitted.
Baseline casts remain scalar leaves in existing comparisons, null/Boolean
predicates, Boolean expressions and selected null-count arguments. Selected null-count-only no-FROM projections
may use literal/parameter cast arguments; standalone casts still require FROM.
Scalar grouping also admits existing column operands, e.g. `(id)=1` and `(i)<(j)`.
It does not admit comparison/logical operands such as `(id=1)=TRUE`, comparison
chaining, or logical/comparison INSERT/settings expressions.
First standard/postfix parameter casts establish source type, later casts convert
it; int4/int8/bool unknown parameter calls are42725, text($1) infers TEXT.
Standard/postfix names preserve strong column/function names, otherwise target
names; function casts use callee names. Same-type column casts retain origin,
converted outputs do not. Unknown input errors position the quote; typed
conversion errors are unpositioned and expose no partial query cursor.
Standard/postfix unsupported pairs are42846, function pairs42883.
Multirow INSERT and cast-bearing Restore remain atomic; nested Restore parameters
are rejected at their token. Dump serializes canonical values.
Execution preflights immutable row/stage-independent casts, including imported
parameters, even on empty/fully filtered input without moving typed failures into
Prepare. Fresh private frames prevent parameter reuse across executions.
SELECT preflight precedes executor table ACL outside engine locks; namespace USAGE
analysis remains earlier and valid casts still require SELECT. Row-dependent
conversions remain row-owned; custom/generic plan-cache switching is not implemented.
Preflight traverses null/Boolean tests and both DISTINCT operands without
folding predicates or pruning conservative dependencies.
Equality preserves child cast errors and diagnoses operator
failures from bound operand types. Review smoke:
`make smoke RUN='^TestCastExplicitReviewSmoke$'`.
Smoke: `make smoke RUN='^TestCastExplicitSmoke$'`; details/matrix in README.
Parameter/ACL smoke: `make smoke RUN='^TestCastExplicitPreflightSmoke$'`.

BYTEA is a stored column type (OID17, typmod -1, no collation), with arbitrary
octets, NULL and nonnull empty values. SQL literals accept hex `\x` (either-case
digits; space/tab/LF/CR between pairs only) and double-backslash/three-octal
escape forms after normal SQL string decoding. String/[]byte binds to inferred
BYTEA are raw pq-compatible octets, not byteain text; typed nil []byte is NULL,
while nonnil empty []byte is empty. Native rows export fresh owned []byte,
DatabaseTypeName BYTEA and ScanType []byte; metadata survives EOF/Close and
catalog replacement. Length/DecimalSize are unavailable natively; pq Length is
MaxInt64 and nullability unknown. Comparison is unsigned bytewise prefix order.
Malformed hex is 22023; malformed escape is 22P02 with exact English primary
`invalid input syntax for type bytea`, eagerly character-positioned at literals.
Invalid hex digits report the complete offending UTF8 character, not a lead byte;
both nibble positions preserve the literal's character-based source location.
BYTEA modifiers fail42601; quoted lowercase "bytea" resolves, "BYTEA" does not.
The builtin bytea_output enum defaults hex and supports escape, with generic
settings lifecycle and statement-captured NOT NULL failing-row Detail. Raw
SELECT octets never change with this setting. Native nonnull empty bytea
always exports nonnil []byte; pinned pq's text escape decoder exports typed
nil []byte for empty bytea (hex exports nonnil empty). Both remain distinct
from SQL NULL's nil interface. Canonical Dump literals always
use quoted lowercase hex; Restore preserves settings, atomicity and old
snapshots. No typed BYTEA literals, functions/casts, arrays or wire server.
Smoke: `make smoke RUN='^TestByteaSmoke$' VERBOSE=1`; pinned independent reference:
`make reference RUN='^TestReferenceBytea.*$' VERBOSE=1`.

INT4/INT8 arithmetic uses the common expression binder in table SELECT,
comparison operands, unqualified DELETE predicates and column-free INSERT VALUES. Operators `+ - * / %`,
unary signs and parentheses follow ordinary arithmetic precedence. Same-width
INT4 stays INT4; mixed width widens to INT8 before evaluation, not according to
the destination or result magnitude. Division truncates toward zero; remainder
follows the dividend sign. Overflow is checked (`22003`), zero division/remainder
is `22012`; runtime diagnostics have no invented source position. NULL results
retain result width. Signed numeric literals fold under minus, including grouping.
`i% -1` and `i%(-1)` are remainder with a negative divisor; adjacent `%-`
is a combined symbolic operator and fails typed `42883`, not a successful
remainder. Arithmetic punctuation maximal munch preserves comment boundaries
and PostgreSQL trailing-sign stripping. Contextual errors use the expression
start/resolved type, while missing-operator errors point at the operator.
Computed results expose `?column?` and INT4/INT8 metadata, including empty results.
Parameter width follows the first known integer peer in SQL order. Unknown-only
arithmetic and concrete non-INT4/INT8 arithmetic are explicit native `0A000`
boundaries, not PostgreSQL preferred-type resolution (unary unknown plus is
FLOAT8 there). Existing noninteger providers are unchanged.
Existing baseline scalar casts compose with arithmetic without adding cast
families. Postfix `::` binds before unary signs; operand casts can widen arithmetic,
but result casts cannot prevent narrower overflow. Frame-only cast conversions
are preflighted recursively beneath arithmetic, including on empty/filtered
input; arithmetic roots still evaluate with rows. Computed arithmetic hidden
under casts remains excluded from bounded null-count arguments. Standard/function
cast labels and standalone identity-cast origins retain their existing contracts;
arithmetic results retain computed metadata and no physical origin.
Sequence calls retain their maximum child volatility through arithmetic and
nonidentity casts. Empty or filtered projections do not allocate or read session
sequence memory; imported argument errors still precede demanded effects.
Composition smoke: `make smoke RUN='^TestSequenceArithmeticCompositionSmoke$' VERBOSE=1`.
Arithmetic INSERT and parameter-free Restore use existing staged publication;
Dump emits canonical stored values, and failed/canceled replacement preserves
old state and retained computed cursors. No external pg_dump, physical storage
or wire-protocol parity is implied. Smoke: `make smoke RUN='^TestArithmeticSmoke$'
VERBOSE=1`; reference: `make reference RUN='^TestReferenceArithmetic' VERBOSE=1`.

DELETE arithmetic keeps existing actual-used-column SELECT authorization,
affected-row counts and atomic deletion staging. Qualified arithmetic remains
excluded without changing qualified atom predicates. Mixed arithmetic/DATE
typed-literal projections preserve the existing codecs, not a DATE arithmetic
provider. Smoke: `make smoke RUN='^TestArithmeticDeleteCompositionSmoke$' VERBOSE=1`.

Character declarations add CHAR/CHARACTER/BPCHAR(n) and VARCHAR/CHARACTER
VARYING(n), length1..10485760 counted in UTF8 code points. Unmodified
CHAR/CHARACTER defaults1; BPCHAR/VARCHAR is unlimited. BPCHAR pads constrained
values with ASCII spaces; all string types preserve stored/output tails.
Assignment truncates only ASCII-space excess or fails22001; string-family
CAST/:: silently clips arbitrary excess and keeps typed NULL metadata.
BPCHAR->TEXT/VARCHAR removes ASCII tails before sizing; same-ID BPCHAR casts
preserve physical bytes before resizing. The separate baseline cast matrix above
is retained; it adds no integer/Boolean explicit edges to BPCHAR or VARCHAR.
Existing assignment output conversion is not implicit.
Character casts infer only unresolved parameters from the target; known character
occurrences convert from their fixed source identity. Earlier unknown occurrences
and mixed INSERT inference boundaries are not rewritten. Diagnostic spelling is
`character`/`character varying`, distinct from catalog/metadata/Dump spelling.
Ordinary INSERT assignment failures use the resolved cast's descriptor rather
than its SQL alias spelling, matching INSERT SELECT assignment diagnostics.
Assigning BPCHAR/CHAR or VARCHAR casts to an INT column fails with `42804`,
source type `character` or `character varying`, the rewrite-or-cast Hint and the
source expression's character position. Direct execution and preparation reject
the assignment before changing destination rows; this does not add cast edges.
Ordered INSERT source VALUES classifies BPCHAR/VARCHAR with the existing string
family. Its row-order common-type rules and implicit conversions remain in use;
BPCHAR-to-TEXT trims ASCII tails, VARCHAR-to-TEXT preserves them. Target sizing
and atomic publication are unchanged.
Common source typmods require matching type identities as well as matching
modifiers; differing CHAR/VARCHAR identities use unconstrained common sizing
even when both declare length 2. In an unlimited BPCHAR destination, a
CHAR(2)-first mixed source preserves its original pad without padding the
VARCHAR row. A VARCHAR(2)-first source trims the CHAR row's ASCII pad.

Character-family comparisons reuse the existing target/WHERE comparison and
logical surface, with column/literal/NULL/parameter/cast operands on either side.
BPCHAR/BPCHAR and BPCHAR/VARCHAR ignore ASCII tails, BPCHAR/TEXT trims only
BPCHAR, TEXT/VARCHAR preserve significance; unknown literals beside BPCHAR use
BPCHAR semantics. ORDER BY uses UTF8/C byte comparison, without general
locale/collation claims. No general functions or no-FROM query expansion are added.
Column output labels survive nested casts; casts of unknown strings/NULL/
parameters instead use the outermost resolved type's catalog name.
An identical-Usage cast of a column retains native physical origin/nullability
proof; typmod changes or retyping produce an expression with no origin and
unknown nullability, also through later identity casts of that expression.

BPCHAR/VARCHAR/TEXT native metadata has string ScanType and Length n or
MaxInt64, retained for NULL/empty/closed/old-snapshot results. lib/pq's BPCHAR
interface{} ScanType and unlimited BPCHAR/VARCHAR Length -5 are independent
adapter contracts, not native behavior. String NUL/malformedUTF8 uses22021
with English byte diagnostics; native has no wire portal Context.
Both native and PostgreSQL CREATE utility Prepare accept invalid length/generic/
TEXT modifiers; direct/prepared Execute reports the positioned semantic error.
Keyword modifier syntax errors fail Prepare on both.
Generic BPCHAR/quoted character modifiers admit simple identifiers, decoded
through the existing integer input path; keyword lengths remain unsigned-only.
TEXT's modifier prohibition wins before modifier expression/input errors.
Native controls preserve constrained/unlimited declarations, physical
padding/tails/Unicode/escapes/NULL and retained readers, with atomic failed
Restore. Internal `"char"` remains a distinct unsupported identity; NAME has
the separate bounded value contract below, not character-family typmods/casts.
Smoke: `make smoke RUN='^TestCharacterSmoke$'`; permanent pinned reference:
`make reference RUN='^TestReferenceCharacter'`.

NAME columns reuse builtin OID19 with typmod -1 and C collation950. Textual
input preserves case and whitespace and silently clips to63 bytes at a complete
UTF8-code-point boundary, not63 characters; it does not apply identifier folding.
Full input validation precedes clipping, so malformed UTF8/NUL in a discarded
suffix still fails22021 with the English hexadecimal-byte diagnostic. Ordinary
string/[]byte/Valuer binds are textual, nil is typed NULL, and native typed nil
[]byte retains the existing non-BYTEA empty-text convention. NAME output contains
only visible spelling, not padding. Native metadata is NAME/string with no
Length/DecimalSize; pq instead exposes NAME/interface{} and []byte values.
Native column origin/nullability and NULL/empty metadata remain intact.

NAME/NAME and NAME/TEXT in either direction use C byte ordering with case/tails
significant. Known TEXT is compared without clipping; unknown quoted operands
and NAME-inferred binds clip first. TEXT/VARCHAR-to-NAME is implicit; BPCHAR
conversion clips before removing ASCII tails. NAME-to-TEXT preserves tails
implicitly; NAME-to-VARCHAR/BPCHAR assignment uses existing target sizing.
Other supported scalar assignment uses output/input: SQL TRUE/FALSE stores t/f,
while bound Go bool stores true/false. NAME-to-integer assignment and NAME/BOOL
comparison retain42804/42883 and existing hints/positions. NAME modifiers reach
CREATE utility Execute's positioned42601 prohibition before operand conversion;
Prepare remains metadata-only.

Native Dump/Restore preserves NAME schemas/data/typed NULL/empty tables and old
cursors, with deterministic redumps and no replacement on failed staging.
Explicit NAME casts/typed literals/functions, arrays, internal `"char"`,
non-C COLLATE, binary wire serving and PostgreSQL physical storage are not added.
PostgreSQL binary namerecv rejects64-byte input rather than clipping; native
byte binds do not implement that protocol. See [NAME values](TYPES.md#name).
Feature smoke: `make smoke RUN='^TestNameSmoke$' VERBOSE=0`;
pinned reference: `make reference RUN='^TestReferenceName' VERBOSE=0`.



UUID declarations use contextual quoted values or binds, not UUID typed literals.
UUID accepts either hex case, matching braces, omitted hyphens and single hyphens
at internal four-hex-digit boundaries; no whitespace/URN prefix/version validation.
Stored values are inline sixteen bytes (OID2950), output is canonical lowercase
8-4-4-4-12, ordering unsigned bytewise, with ordinary strict NULL equality and
ASC NULLS LAST/DESC NULLS FIRST. Native values/ScanType are string/string; pq
uses canonical owned []byte/interface{}. UUID metadata persists for empty/NULL
results; Length/DecimalSize are unavailable. Malformed text is 22P02 with exact
original-input primary; eager literal errors point to the quote in characters,
native binds have no Position/Context, PostgreSQL binds add wire portal Context.
The existing native Dump/Restore serializes UUID declarations/canonical quoted
values atomically and preserves retained snapshots. Smoke: `make smoke RUN='^TestUUIDSmoke$'`.
Generators, version/timestamp extractors, arrays/casts/typed UUID literals and
binary wire input remain excluded. PostgreSQL physical disk/network storage,
protocol serving and external pg_dump process/custom archives are non-engine
boundaries, never generic successful no-ops.

SMALLINT/INT2 has signed-16 bounds (-32768 through 32767), native int64 payloads and INT2 result metadata, including NULL/empty results. Quoted lowercase `"int2"` resolves; quoted `"smallint"`/`"INT2"` do not. INTEGER/BIGINT keep their current storage and signed-32/signed-64 bounds. Shared textual input accepts C whitespace, signs, radix prefixes and single separators; malformed/range errors are 22P02/22003 with original value and English smallint/integer/bigint names. Typed narrowing reports `smallint out of range`, unlike input's `value "32768" is out of range for type smallint`. SQL integer inference remains INT4/INT8; cross-width comparisons do not assignment-narrow. Checked imports/coercions precede publication, and staged Dump/Restore preserves all widths and retained cursors. Feature smoke: `make smoke RUN='^TestSmallintSmoke$' VERBOSE=1`; independent pinned reference: `make reference RUN='^TestReferenceSmallint' VERBOSE=1`.

SELECT execution uses one eager finite source/filter/sort/projection pipeline and common typed row result with identity/settings/SHOW producers; no second binder/catalog/storage path or added syntax. Every admitted target is finalized before statement completion, rejected rows do not evaluate targets, and later evaluation/sort failure exposes no cursor/fragment. Result schema remains available after EOF/error/Close, including empty relations; zero-column rows retain input multiplicity. Direct-column results retain immutable relation-local captured prefixes/indices; computed results retain projected values only. EOF/error release resources automatically; raw cursor errors (including ordinary context cancellation) stay sticky until idempotent Close, after which Next is EOF. Saved exported values survive cleanup and append/Reset/Restore; mutable timestamp-infinity bytes detach per export. WHERE keeps only TRUE; unordered results and sort peers have no portable sequence guarantee. Default ASC NULLS LAST/DESC NULLS FIRST remain without admitting NULLS syntax. Missing ORDER keys preserve source character Position in direct/prepared binding on empty or populated input. Feature smoke: `make smoke RUN='^TestQueryCoreSmoke$' VERBOSE=1`; independent pinned reference: `make reference RUN='^TestReferenceQueryCore' VERBOSE=1`.

Analyzed SELECT/INSERT missing-relation errors retain the first relation identifier's one-based character Position (for example, `SELECT id FROM t` gives42P01/`relation "t" does not exist`/16, or22 with an intervening `/*é*/ ` comment). The same normal analysis wrapper handles pre-lock and binder lookup; copied diagnostics preserve every other field and utility COMMENT/ALTER/LOCK/DROP records remain independently owned. Shared malformed-input witnesses use `SELECT id FROM t WHERE` →42601/`syntax error at end of input`/23 rather than treating PostgreSQL's legal empty-target SELECT prefix as shared syntax.

DATE (OID1082) stores civil days with proleptic Gregorian bounds 4714-11-24 BC
through 5874897-12-31, plus infinities. Native output/ScanType is string, including
expanded years, BC and infinities; typed NULL remains DATE/nil. pq exports finite
time.Time and default infinity []byte, with time.Time ScanType. DATE has no
precision: modifiers fail42601 at the type name, during CREATE execution or
typed-literal analysis, before integer conversion or unary-plus validation of
parsed modifiers (`date(3.0)`, `date(2147483648)`, `date(+3)`). Parser syntax
errors still precede type resolution; other types' modifier rules are unchanged.
Incompatible typed DATE assignments fail42804 during analysis/Prepare and name
the source `date`, not `date without time zone`; TIME/TIMESTAMP retain their
`without time zone` diagnostic names. Failed assignments preserve stored rows.
`date` remains an ordinary identifier; `"date"` is legal.
DATE modifier literals retain raw numeric spellings on a parser-private,
per-call continuation distinct from ordinary cast operands; no DATE keyword
or function-style DATE cast is introduced. Baseline int4/int8/text/bool calls
and standard/postfix cast parsing remain unchanged; DATE casts still fail0A000.
ISO/month-name/compact/ordinal/Julian inputs, BC, epoch, fixed MDY and relative
now/today/yesterday/tomorrow reuse the shared civil decoder. Validated clocks/zones
never shift or carry the date; time.Time binds preserve their location's Y/M/D,
including Go year0 as1BC. The date domain is independent of TIMESTAMP's range.
Successful Go imports ignore clock/zone fields without validating or converting
them. On a civil-range failure only, the22008 primary includes the failed
temporal spelling, e.g. `date out of range: "5874898-01-01 00:00:00Z"`;
fractional seconds, numeric offset seconds and trailing ` BC` are preserved.
AD/BC numeric years beyond INT32 fail with the earlier `date/time field value
out of range` primary, before final DATE-domain range validation.
Pinned pq's BC time.Time encoder uses AddDate and normalizes year0 February29
to March1; native DATE preserves that leap day. Textual BC input avoids this
adapter limitation; independent reference expectations distinguish the bind paths.
Prepared typed/contextual constants freeze; bound values use execution-owned
transaction-start UTC. Malformed22007, calendar/range22008 and zone failures
retain English messages, original input and character-positioned literal errors;
native parameters have no Position/Context. Comparisons order civil days with
infinities and default NULL order. Dump/Restore quotes canonical values and
preserves snapshots/atomicity. Captured database/sql ColumnType values remain
usable after EOF/Close; database/sql itself rejects new ColumnTypes calls on
closed Rows. Native DATE/TIME and DATE/TIMESTAMP comparisons reject42883;
PostgreSQL independently supports DATE/TIMESTAMP comparison operators, including
midnight equality, civil-day ordering, infinities and SQL NULL propagation.
No cross-temporal casts/settings/arithmetic, CURRENT_DATE or no-FROM SELECT are
added. Smoke:
`make smoke RUN='^TestDateSmoke$' VERBOSE=1`; independent pinned reference:
`make reference RUN='^TestReferenceDate' VERBOSE=1`.

TIME has OID1083 and canonical string output/ScanType, preserving distinct `24:00:00` (pq instead anchors time.Time to Jan1/Jan2 year0). Precision0–6 uses fractional ties-to-even microseconds then half-up sizing; above6 warns22023/clamps, keyword negative precision is42601, quoted `"time"(-1)` is22023. Colon/compact clocks, AMPM, limited optional validated dates/Julian/T/noise, numeric/abbreviation/full-zone inputs and now/allballs are supported. Zones never shift the written clock; historically varying full names require a date, fixed names do not. All REL_18_0 IANA2025b names/aliases are classified through compiled historical TZif offsets without runtime zone I/O. Constants freeze at analysis, parameters use execution transaction start; current environment is UTC. Same-type equality/order/NULL and native Dump/Restore use existing providers/staging; timetz/functions/casts/session zone settings remain excluded. Feature smoke: `make smoke RUN='^TestTimeSmoke$' VERBOSE=1`; real reference: `go test -tags reference -count=1 -timeout=60s -v -run '^TestReferenceTime.*$' ./reference`.

TIME ignores recognized `on`/`at` noise, but rejects standalone weekday tokens (`04:05 on Monday` is22007). Unknown full-zone names produce22023 with a lowercased token in the primary diagnostic (`America/Bogus` becomes `america/bogus`); the original input remains intact in malformed-input diagnostics.

TIME colon parsing checks lexical/component bounds first, then processes remaining tokens and validates the optional date, AM/PM and final clock domain in that order; TIMESTAMP retains its early clock-domain check. Typed assignment errors use the actual temporal source identity. PostgreSQL permits TIMESTAMP-to-TIME assignment, but native cross-temporal casts remain outside this dialect.

NUMERIC is exact immutable decimal, exported as string with fixed-decimal output and retained display scale (typed NULL is nil). Precision 1–1000 and independently signed scale -1000–1000 include negative/wide scale; omitted scale is zero, unconstrained scale is retained. Sizing rounds ties away from zero before precision overflow; NaN fits constrained types, infinities require unconstrained numeric. Ordering is -Infinity < finite < Infinity < NaN; NaN equals NaN, finite equality ignores display scale, and integer/numeric comparison is exact beyond 2^53. Integer assignment rounds/checks numeric; checked numeric/float conversions preserve binary semantics, numeric-to-TEXT uses canonical output. DatabaseTypeName NUMERIC/ScanType string and DecimalSize reflect declarations including negative scale, never values; unconstrained sizing is unknown. Numeric aliases are unquoted only except catalog `"numeric"`.

Input syntax is validated during INSERT/SELECT Prepare at unconstrained typmod; destination sizing is Execute-owned, with overflow Detail and no Position. Literal input failures are character-positioned; native bind input has no Position/Context. Same-type repeated numeric parameters can size separately per destination. Dump/Restore quotes exact canonical numeric values, preserves scale/specials/NULL/metadata, rejects failed staged replacements atomically and retains old cursor ownership. Format bounds are 131072 integer digits/16383 fractional places; excessive scale rejects rather than truncating to zero. Fraction/exponent SQL tokens do not broaden SET integer syntax or its separate quoted setting codecs. General casts/arithmetic/functions/serial and SELECT without FROM remain excluded; existing SMALLINT participates in exact numeric comparison and checked integer assignment. Feature smoke: `make smoke RUN='^TestNumericSmoke$' VERBOSE=1`; pinned reference: `make reference RUN='^TestReferenceNumeric.*$' VERBOSE=1`. Native negative-scale DecimalSize is independently asserted rather than matching lib/pq's unsigned-scale adapter defect.

For NUMERIC column `v`, `SELECT $1 FROM n WHERE v=$1` fails Prepare with `42P08`, primary `inconsistent types deduced for parameter $1`, Detail `numeric versus text`, Position `8`; direct Query with supplied arguments reports the same record at execution. Zero-argument native Query still reports that `42P08` record, while lib/pq's PostgreSQL simple query instead reports `42P02`, `there is no parameter $1`, Position `8`. The different complete records are asserted independently, without false parity.

CREATE TABLE uses ordered explicit columns and plain LIKE elements, including multiple/interleaved sources and zero-column `()`. LIKE copies declaration shape and NOT NULL, never source rows/owner/ACL/dependencies; qualified/effective-path lookup requires source namespace USAGE and relation SELECT. LIKE options remain unsupported, not successful no-ops. IF NOT EXISTS skips an existing authorized destination before semantic element analysis, including nested CREATE TABLE in CREATE SCHEMA; malformed syntax still fails before effects. Nested elements use the enclosing owner and local staged schema path, never change session identity/settings, and publish atomically. Outer CREATE SCHEMA IF NOT EXISTS still cannot contain elements.

NOT NULL is an identity-bearing relation constraint, separate from CHECK.
Ordered column `[CONSTRAINT name] NULL/NOT NULL` declarations and table
`[CONSTRAINT name] NOT NULL column` are supported; compatible redundancies merge,
named NULL creates no identity. Column-local NULL/NOT NULL conflicts are42601
at the conflicting token; differently named column duplicates are18.0XX000,
validated before table-level merging even if a table constraint comes first.
Explicit column NULL plus table NOT NULL is valid in either order. Only NOT
NULL identities merge; different names at that stage are42601. CREATE table
constraints naming system columns fail0A000 `cannot add not-null constraint on
system column "ctid"` for ctid, as does selected ALTER ADD. Duplicate explicit
names on different attributes
are42710. Generated UTF8-safe63-byte `table_column_not_null` names consult all
destination namespace constraints, including staged nested tables and prior
current-command names, never future explicit names. Allocation follows original
column/table/LIKE element order after same-column merging. A later explicit name
colliding with an earlier generated name fails23505 for
`pg_constraint_conrelid_contypid_conname_index`; Schema pg_catalog, Table
pg_constraint and that Constraint are reported, with exact Detail
`Key (conrelid, contypid, conname)=(OID, 0, name) already exists.` using the real
locally allocated candidate relation OID. CREATE publishes no table; reversing
the declarations succeeds with a generated suffix. Explicit names remain
relation-local. LIKE copies identities in source constraint-name order at its
element position onto fresh attributes, never hierarchy or source dependencies.

Selected ALTER TABLE actions are ALTER [COLUMN] c SET/DROP NOT NULL, ADD
[CONSTRAINT n] NOT NULL c, and DROP CONSTRAINT [IF EXISTS] n [RESTRICT|CASCADE].
They share t/t*/ONLY t/ONLY(t), OwnerOnly AX and metadata-only effect-free Prepare.
Execution captures the admitted command's lock_timeout policy; blocked utilities
fail55P03 without Position or catalog effects, even for semantic no-ops.
Prepare does not resolve or wait for these utility targets. In contrast, a
subsequent absence probe `SELECT * FROM t` is analyzed on direct Exec/Query or
semantic Prepare and returns42P01 at character15 on both native and PostgreSQL;
this SELECT record must not inherit the utility's unpositioned-error policy.
SET/ADD scans visible committed rows before metadata-only publication; old NULL
fails23502 with column/schema/table, no Detail/Constraint. INSERT23502 includes
failing-row Detail even for a named constraint. Both identify the decoded column
literally; all failures preserve schema/data. Compatible SET/ADD retains existing
name; DROP makes nullable; redundant operations are count-zero/no-revision.
Missing DROP42704 or IF EXISTS NOTICE00000 uses the relation/name diagnostic.
DROP NOT NULL on ctid/xmin/cmin/xmax/cmax/tableoid fails0A000 with
`cannot alter system column "name"`; ordinary absent columns remain42703.
DROP's default RESTRICT, explicit RESTRICT and CASCADE remove the selected
NOT NULL identity equivalently: supported NOT NULL constraints have no inbound
constraint dependents and own no SQL index. These modes do not implement a
general constraint dependency closure. PK/UNIQUE/FK constraints and SQL indexes
are unsupported engine capabilities, not external-only mechanisms; constraint-owned
index deletion and referenced-key RESTRICT/CASCADE behavior require those
capabilities first. Unrelated NOT NULL identities and sequences owned by the
still-live table attribute survive even CASCADE.
Stable attribute IDs, ACL/comments and original storage/backing survive ALTER;
independently captured storage length fences a stale scan. DROP COLUMN removes
only the dropped identity. Retained prepared SELECT and parameterized INSERT
rebind current nullability across SET/ADD/DROP: nil23502 while enforced, nil
success after DROP, no cached-result-type error. Native known Nullable and
INT4 int64 ScanType differ deliberately from pinned pq's unavailable Nullable
and int32 ScanType. Retained snapshots keep the original metadata/values.

Dump captures and emits quoted named NOT NULL declarations, generated names
included. Restore uses common isolated CREATE/INSERT binding and atomic target
replacement. Scalar UPDATE uses existing expressions, destination coercions and
NOT NULL enforcement (see below). Inheritance and NOT VALID/VALIDATE/NO INHERIT
remain explicit native0A000 boundaries with real PostgreSQL-only witnesses.
CHECK/PK/domains/general ALTER lists and explicit-transaction DDL remain
excluded, never successful no-ops.
Post-drop Dump/Restore captures nullable declarations, remaining NOT NULL
identities, rows, defaults/comments and owned-sequence state/associations; it
does not replay ALTER history or add ALTER to the Restore whitelist. Physical
server/WAL/crash durability, a wire/server process and external pg_dump/archive
mechanics remain outside the embedded engine.

Native INHERITS failure-only grammar allows one complete parent list on either
CREATE TABLE form; qualified/quoted/multiple parents and IF NOT EXISTS remain
0A000 at INHERITS, never a conditional bypass. Malformed/repeated/extra-tail
input is42601; script prefixes do not execute on these full-input rejections.

Public smoke: `make smoke RUN='^Test(NotNull(Smoke|ControlsSmoke)|DropNotNullSystemColumnSmoke|DropConstraintOwnedSequenceSmoke)$' VERBOSE=0`.


Standalone ALTER TABLE RENAME [COLUMN] old TO new and RENAME TO new support
relation-level IF EXISTS. Both table and column targets accept qualified/path
names and t*/ONLY t/ONLY(t) markers for standalone tables. These syntax markers
do not imply inheritance or partition hierarchy support.
Destinations are single identifiers. Rename is count-zero OwnerOnly AX, with
CREATE on the containing namespace additionally required for table rename.
Prepare has no binding/authorization/lock/notice effects. Missing IF EXISTS
relations, including absent qualified namespaces, emit one NOTICE00000 naming
the unqualified relation; existing-table missing columns still fail42703.
Self/colliding column names fail42701; relation collisions fail42P07, atomically.
System source names fail0A000; system destinations fail42701. Modern oid remains
an ordinary user column. Utility diagnostics have no parse-analysis Position.

Renames preserve full relation/attribute identity, unchanged storage backing,
NOT NULL names and stable-attribute links, ACLs and comments. Published
descriptors and retained Rows are immutable. Prepared ASTs rebind original SQL
names rather than rewriting strings: old columns42703, old relations42P01,
changed star output labels0A000 cached-result-type error. Retained old-table SELECT
has Position15 on both native and pinned PostgreSQL: reanalysis preserves the
original relation-name occurrence. Cached-plan logical Context is empty on both
backends; C Routine is not a logical diagnostic field.
Canonical Dump emits current names/labels/comments/constraints, not ALTER history.
RENAME CONSTRAINT belongs to constraint/index metadata work; stored view/FK/index,
row-type, hierarchy and transactional-DDL effects are PostgreSQL-only witnesses,
not native successful no-ops. Physical pg_dump/server disk/network mechanisms
are outside native Reset/Dump/Restore. Smoke:
`make smoke RUN='^TestAlterRenameSmoke$' VERBOSE=1`.

DROP TABLE supports grouped schema-qualified/path targets, IF EXISTS and RESTRICT/CASCADE with whole-group atomic preflight. Table owner, schema owner or superuser authority is required. Both modes remove selected tables and AUTO-dependent owned sequences, plus contained metadata/storage/ACLs; neither removes schemas/types/roles/independent LIKE clones. Retained Rows survive removal. Public DROP TABLE remains excluded from private Restore.

ALTER TABLE also supports one DROP [COLUMN] [IF EXISTS] name [RESTRICT|CASCADE] action, ordinary t/t* and ONLY t/ONLY(t) for standalone tables. Table OwnerOnly (not schema-owner fallback) execution uses AX; metadata-only Prepare does not lock. Survivor attribute/origin IDs remain stable while dense rows/metadata compact; descriptor+storage publish through one guarded CatalogStage, checking original storage root/observed length/deletion-root identity against concurrent row publication. Native dependencies are named NOT NULL/ACL/comments and AUTO-dependent sequences keyed by stable attribute SubID. Indexes/views/FKs/hierarchy and transactional table DDL remain external-only witnesses. Explicit native transactions exclude ALTER TABLE. Retained snapshots stay immutable; prepared survivors rebind, changed result shapes retain the cached-type boundary.
The optional ALTER DROP COLUMN keyword consumes the shared dedicated COLUMN token used by COMMENT, not legacy KW_R text matching. Quoted keyword targets remain decoded IDENT names; preserve keyword categories and positioned parser errors when integrating grammar changes.
Canonical populated zero-column Dump uses one public DEFAULT VALUES insertion per row through common Insert. DEFAULT VALUES also applies declared defaults/typed NULL for nonzero-column tables; VALUES() remains invalid syntax. Restore does not accept ALTER TABLE history. Feature smoke: `make smoke RUN='^TestAlterDropColumn(Smoke|ZeroColumnControlsSmoke)$'`.

Do not widen syntax beyond the selected surface: no nested transactions,
2PC, SERIALIZABLE/SSI, DEFERRABLE safe snapshots, SELECT aliases/qualified columns,
general casts/joins/functions/expressions, ALTER DEFAULT, serial/identity, primary
keys, UPDATE FROM/row-subqueries/CURRENT OF, DELETE USING/CURRENT OF or RETURNING.
SQL BEGIN [WORK|TRANSACTION] and START TRANSACTION accept ordered isolation
READ COMMITTED/READ UNCOMMITTED/REPEATABLE READ and READ ONLY/READ WRITE modes.
SET TRANSACTION changes current owner modes; SET SESSION CHARACTERISTICS AS
TRANSACTION changes typed defaults. SERIALIZABLE/[NOT] DEFERRABLE syntax is
complete but execution is native 0A000; malformed suffixes remain 42601.
Driver default isolation honors defaults; explicit RU/RC/RR overrides and Go
ReadOnly=false requests RW. COMMIT/END/ROLLBACK/ABORT [WORK|TRANSACTION]
[AND [NO] CHAIN] retain qualified handles and one publication authority.
SAVEPOINT name, RELEASE [SAVEPOINT] name and ROLLBACK [WORK|TRANSACTION] TO
[SAVEPOINT] name support nested marks and newest-name shadow/reveal. Rollback
restores saved writes/deletes/settings/sequence overlays, retains the target
and destroys later marks. Release retains changes and removes target/later
marks, except it restores the target parent's read-only mode. A recoverable
error restores the newest mark and leaves the block Aborted until ROLLBACK TO.
No per-mark lock release, cursor rollback or PL/pgSQL subtransactions are
implemented; native transaction-owned holds persist until outer end.
Explicit modifying utilities remain a native 0A000 boundary in RW blocks.
Exec-only RO rejects supported INSERT/UPDATE/DELETE, CREATE/ALTER/DROP
table/schema/role and COMMENT with 25006/canonical tag before effects and that
native utility boundary; valid data-write Prepare still analyzes/describes.
LOCK, reads and settings remain allowed; session authorization retains its
separate native explicit boundary.

Settings use the existing registry, typed values and SESSION/LOCAL action journal.
Commit preserves SESSION, rollback/abort restores, LOCAL expires. Current
transaction_isolation/read_only resolve the owner; default_transaction_isolation/
read_only affect next starts only. Current RESET/SET DEFAULT/set_config(NULL)
reject 0A000 `parameter "<name>" cannot be reset`; RESET ALL excludes current
modes but transactionally resets defaults. Different isolation after the first
snapshot fails 25001; identical requested mode succeeds (RU != RC). Late RO→RW
fails 25001; RW→RO succeeds. SET TRANSACTION outside a block warns25P01 at Exec.
Route current assignments before Engine/settings/Session locks, never invert
owner-first order. Providers/normalizers run outside publication locks.
Physical settings registration, search_path alias, timer and adapter boundaries
are unchanged. Restore whitelist does not expand. Multirow DML remains atomic.
SHOW ALL built-in descriptions are PostgreSQL 18.0's English short_desc metadata,
including all four current/default transaction settings; custom descriptions
remain the registered text, with no blanket built-in metadata exemption.

REPEATABLE READ retains all canonical relations' immutable slice headers/end
roots once, including empty/never-read relations, before semantic waits; no row
values are copied and BEGIN captures nothing. RC/RU execution remains grant-time
fresh. SELECT singleton/identity/settings functions and data Prepare freeze timing.
Current binding/ACL uses grant-time metadata. Missing saved addresses mean empty
committed roots; saved incompatible storage/row shape is ordinary ErrCatalogReplaced,
not historical-catalog/transactional-DDL support. Results pin roots independently.
Successful COMMIT AND CHAIN preserves final isolation/access despite changed
defaults and clears snapshot/writes/deletes/holds, even with a live child RO mode.
ROLLBACK AND CHAIN and failed-block COMMIT AND CHAIN first unwind all retained
marks and carry the oldest mark's parent readOnly, not a failed/live child mode.
Without marks, abort restores entry defaults before BEGIN/Go overrides and
rolls back in-block defaults; with marks it restores the failed level before
full end unwinds parents. Plain end then BEGIN uses session defaults.
A chained block uses its carried modes as the abort baseline, not session defaults.
For RR conflicts a post-frontier committed replacement reports concurrent update
even if subsequently deleted; only deletion of the snapshot predecessor reports
concurrent delete.
Read-only modifying utilities reject with 25006 before object resolution,
authorization and relation locks, including missing or locked targets and
statements prepared before BEGIN. DML retains semantic analysis before read-only
execution rejection. Utility Prepare inside explicit transactions and read-write
utility execution retain the native unsupported 0A000 boundary; no transactional
DDL support is implied.
Feature commands: `make test RUN='^TestTransactionIsolation'`,
`make reference RUN='^TestReferenceTransactionIsolation'`,
`make smoke RUN='^TestTransactionIsolationSmoke$$'`.

Control classification precedes ordinary implicit birth/epoch/command gating. Prepare is effect/notice-free; start Prepare Aborted gives 25P02, end Prepare remains eligible without recovering until Exec. Same-epoch SQL Aborted COMMIT succeeds by rollback/count0, whereas Go Commit keeps its failed-transaction ordinary error. Completed controls Finish once without late authority checks/merge/command advance into a chained incarnation. Shared private helpers never reacquire admission; one physical-session owner and publishWrites remain sole authority. CHAIN reserves a fresh current-epoch ref and empty ticket before old publication under the existing short replacement read fence; install is infallible, then release old holds before waking waiters. Resolve old settings actions after publication and before clearing/installing the next incarnation; CHAIN inherits committed SESSION values, never LOCAL values or old actions. Old native GoTx refs cannot end a new SQL incarnation (intentionally unlike pq's unqualified old end). Healthy pool reuse/borrower Close preserves SQL blocks; pin and explicitly end before pool return. Physical end drains and discards only its own private work/holds. Restore's whitelist still rejects controls; no script-transaction expansion.

Failed-block SQL COMMIT rollback success is restricted to the same catalog epoch.
Successful native Reset/Restore fences Active and Aborted transactions, writers
and read-only blocks alike: valid ordinary work and every COMMIT/END variant
return ordinary `stuntgres: catalog replaced before publication`, not SQLSTATE.
The first admitted stale failure discards private writes and transactional
settings; repeated ordinary use remains fenced. An accepted failed commit ends
the old incarnation without installing a chain. Full ROLLBACK/ABORT intentionally
recovers across epochs, including fresh current-epoch ROLLBACK AND CHAIN.
End Prepare remains effect-free/eligible; retained handles are not deleted.


Relation locking is transaction-owned AS for SELECT, RX for INSERT/UPDATE/DELETE, row UPDATE for DELETE and NO KEY UPDATE for UPDATE, internal SUE for COMMENT TABLE/COLUMN Execute, owner-authorized AX for selected ALTER utilities, and explicit single-target `LOCK TABLE name IN ACCESS EXCLUSIVE MODE [NOWAIT]` in an explicit block under the current superuser role. The two row modes are distinct, mutually conflicting bits on one logical row resource, not separate domains. AS/RX coexist with SUE; SUE conflicts with other-owner SUE; AX conflicts across owners; same-owner operations reenter. Semantic data Prepare acquires AS/RX, fixes characteristic timing and the explicit RR snapshot before waits (implicit Finish releases, explicit retains despite stmt.Close), never row holds; LOCK, COMMENT and ALTER Prepare are metadata-only. Earlier conflicting waiters prevent barging except for compatible deadlock-resolving queue rearrangement. NOWAIT fails without enqueue with 55P03/relation-name message. Typed lock_timeout defaults to 0/off, uses the existing millisecond SET/RESET/local lifecycle and times each queued acquisition separately, including preparation and UPDATE/DELETE rows, never immediate grants or whole operations. Earlier absolute SQL deadline wins (exact tie favors lock timeout); caller context errors retain Go identity. Abort/end releases exact owner state; commit publishes before waking successors. Interruption removes requests and wakes successors without disturbing holders. Successful replacement fences old held/waiting epochs outside metadata locks; only genuinely queued implicit SQL drops replacement admission and revalidates exact identity afterward. UPDATE/DELETE never widen candidates to peer inserts: RC/RU skip committed deletion and recheck changed predecessor values, while RR rejects newer committed heads/ends with40001 after real immediate/queued grant. ALTER releases replacement admission before catalog provider validation and checks descriptor/storage generation/end-root tokens at publication. Additional exposed modes, advisory resources and introspection remain excluded.
Lock-timeout55P03 and deadlock40P01 during direct SELECT/INSERT/UPDATE/DELETE relation analysis or semantic Prepare carry the original ObjectName's first token character Position (schema token for qualified names). Cached Execute, UPDATE/DELETE row waits and utility errors have no parse-analysis Position;57014 is excluded from this decoration. Do not put source-position policy into manager resources or fabricate Schema/Table fields.
The canonical manager searches finite stable queue-order constraints to resolve soft cycles without aborting; unavoidable relation/row cycles fail an unspecified actual cycle member with ERROR40P01 `deadlock detected` and Hint `See server log for query details.` Native Detail describes exact logical transaction/session/epoch, mode, relation ObjectRef and optional RowID cycle edges, not physical PostgreSQL PID/XID/ctid identities or tuple Context. Without marks, mandatory Finish discards victim private effects and releases held locks, allowing survivors to proceed before explicit victim ROLLBACK. With recoverable marks it restores the newest mark's effects and conservatively retains native outer-ticket holds until full end. Explicit victims retain the existing25P02 recovery gate. SSI remains unsupported and is not a blocking dependency.
Queued work binds the grant-time descriptor of the exact retained ObjectRef; a same-identity definition revision is valid, while epoch changes/removal/recreation remain fenced. Keep subsequent grant-time descriptor pinning and catalog/storage publication guards. Post-drop quoted expression errors use literal decoded identifiers and character positions. Positive qualified ordinary-owner controls use one ordinary schema/table owner; separate distinct-owner Restore/denial/replay witnesses retain the namespace-USAGE and explicit-ACL Dump boundaries without adding native GRANT.
ALTER re-resolution must compare the complete ObjectRef with scope.lockedRef before owner/column semantics, notices or staging, then use the validated grant-time lockedDescriptor. A search_path shadow created during an AX wait fails ErrCatalogReplaced without effects or reacquisition; PostgreSQL's independently asserted utility behavior instead reacquires the shadow AX. Never reinstate a pre-wait descriptor-revision fence.
DROP COLUMN compaction removes only the dropped stable SubID's column comment and preserves table/survivor comments immutably. Validate comment keys with hasAttribute; Dump must capture attributes and index comment payload by attributes[i], not dense i+1. Restore rebinds canonical named comments to the reconstructed declaration's IDs.
Successful CatalogStage publication advances the shared revision allocator past staged installed revisions without lowering an already-leading frontier; MaxUint64 saturates and later allocations fail ordinarily. DROP keeps old revision + 1, with no early allocator reservation. Commit the candidate frontier with descriptor/storage installation only after final fences, so failed/canceled stages and skips do not consume revision tokens. Later TABLE/COLUMN COMMENT must publish strictly fresh tokens at the retained identity.

CREATE/ALTER/DROP SCHEMA and search_path/current_schema reuse existing catalog/session/role seams. Nested CREATE SCHEMA elements are CREATE TABLE only with atomic publication. Schema provisioning/rename uses effective-superuser database-CREATE baseline; fixed native database label stuntgres is diagnostic-only. Native public retains PUBLIC USAGE|CREATE. Prepared AST re-resolution has a coherent frozen signature guard, not full PostgreSQL cached-plan caveat parity. Absent pg_temp lookup never allocates; pending creation fails 0A000.

COMMENT supports TABLE/COLUMN/SCHEMA/ROLE only, with decoded literals and NULL/empty removal (whitespace persists). Store text in immutable descriptors, column maps keyed by stable SubID, or shared RoleDescriptor values; never name-keyed side storage or adjacent SQL description functions. Lookup and live ownership precede mutation; COLUMN nonowner says relation, TABLE says table. Ordinary roles cannot self-comment; CREATEROLE/ADMIN expansion is excluded. Explicit-TX COMMENT remains native 0A000. Rename/owner transfer retains text; drop/recreate clears it; LIKE does not copy it. Database Dump excludes shared role comments, Reset/Restore retain them.

DSNs are exact opaque process-local names, including empty string, scoped to a Driver. Different strings/Drivers isolate data. Closing pools/connections does not erase an engine. Reset atomically clears objects retaining process-local identity. Dump serializes persistent user schemas before owner-grouped qualified tables/rows; unsupported explicit ACLs fail before output. Restore stages and atomically replaces, preserving independently mapped namespace/table owners through private stage-only destination authority, never fake grants. Controls require native pools, reject wrapped/foreign drivers and nil inputs, and preserve data on errors. Snapshots survive controls and schema lifecycle changes. Never hold target locks during external I/O.

Fixture publication preserves physical session identity, live role views and
baseline settings, while transaction abort/end resolves SESSION/LOCAL actions.
Host authorization for native controls does not grant SQL privileges. Failed or
canceled Restore before installation leaves committed state, epoch and otherwise
eligible transactions intact; already-Aborted transactions are not healed.
The control borrower is released before external I/O. A blocking Reader must
return before cancellation is observed. Foreign replacement checks cancellation
while filtering live source rows and once more after authority preparation,
immediately before indivisible identity/frontier/catalog installation. Cancellation
after that final successful observation cannot retract the replacement. Captured
cursors retain old typed rows/metadata, not authority to publish (Go sql.Tx may
itself close owned Rows).

BYTEA control values are canonical ASCII hex regardless of bytea_output.
Late malformed byteain, reader errors and cancellation before publication
leave the old typed catalog/data intact; retained byte buffers remain detached.

Feature smoke: `make smoke RUN='^TestControlTransactionsSmoke$' VERBOSE=1`.
Native fixture replacement has no PostgreSQL wire-command oracle; reference
tests independently assert READ COMMITTED/private visibility, English SQL
diagnostics and same-epoch failed-block recovery on the pinned real server.
Restore's existing private whitelist is unchanged; transaction-control scripts,
live settings and shared role effects are not authorized by these witnesses.

Trusted Restore INSERT and database-local COMMENT use local validated captured-bootstrap AuthorizationViews, never session identity mutation. COMMENT authority covers qualified lookup, lock resolution and metadata mutation, is restricted to local targets and is cleared after each statement. Named CREATE owner groups remain intact; per-statement private markers cannot affect public Prepare/INSERT/COMMENT or persist schema/table ACL grants.

Restore's initialized private catalog accepts CREATE/ALTER/DROP SCHEMA, CREATE TABLE with supported defaults, CREATE SEQUENCE, INSERT with supported parameter-free scalar/default-request forms (including integer arithmetic), database-local COMMENT, generated sequence OWNED BY associations and SELECT setval(name,value,is_called), and named SET SESSION AUTHORIZATION only. Nested parameters remain rejected recursively. Dump reconstructs bootstrap identity/name/owner changes before user objects; retained canonical builtin ACLs transform owner grantor/grantee, recreated public has absent ACL, and unchanged bootstrap retains legacy bytes. Local schema comments follow schema creation, table/column comments follow CREATE before INSERT under owner groups. Arbitrary explicit ACL serialization stays unsupported. Held INSERT rebinding INT4→TEXT rejects native 0A000 before importer/write; pinned PostgreSQL retains INT4 parameters and assignment-coerces to TEXT, an explicitly asserted difference.


## Sequences

Sequence objects support the PostgreSQL18 CREATE/ALTER/DROP option and administrative forms documented in the [SQL reference](SQL.md#sequences), including TEMP/UNLOGGED, optional BY/WITH, CREATE/ALTER RESTART, NO MINVALUE/MAXVALUE/CYCLE, OWNED BY, IF [NOT] EXISTS and grouped DROP. RESTART sets the current uncalled value, not configured START: CREATE START 5 RESTART 9 returns 9 first, and a later bare ALTER RESTART returns 5. Numeric options are integral and bounded by the declared smallint/integer/bigint type; function results always use INT8. Constant-space reservation arithmetic handles large CACHE and int64 extremes without allocating cached arrays. CYCLE wraps on reservation boundaries and can repeat values; it is not a uniqueness constraint.

CREATE accepts bare `RESTART`, `RESTART n` and `RESTART WITH n` through the
same option validation as ALTER, including START/restart bounds and duplicate
options. It initializes an uncalled generator at configured START (bare) or
the explicit restart value without changing START.
`CREATE SEQUENCE s START 5 RESTART 9` first yields9 from `nextval('s')`;
later bare `ALTER SEQUENCE s RESTART` makes the next call yield5.

Generic ALTER TABLE relation rename also accepts a sequence and atomically
publishes its fresh sequence-plan stamp without changing identity/generator/cache.
Generic sequence column rename rejects with42809 `cannot rename columns of relation
"s"` / Detail `This operation is not supported for sequences.` before attribute
lookup, including missing/system-column names and optional COLUMN syntax.

Sequence OWNER TO supports role names and CURRENT_ROLE/CURRENT_USER/SESSION_USER
identity operands; quoted identity spellings remain literal role names.
Same-owner requests need no renewed destination schema CREATE. Genuine ownership
changes by a non-superuser retain destination-role/schema checks; superuser
transfers bypass those transition checks. Actual linked-sequence owner changes
remain forbidden; successful transfers remap owner grantor/grantee identities
while preserving unrelated grants and same-owner requests preserve ACL/state.

CREATE/ALTER OWNED BY takes transaction-owned AccessShare on the referenced
table before fresh exact identity/column/owner/schema validation, waits behind
AccessExclusive and holds through transaction end. Canceled or timed-out
acquisition publishes no new sequence or association. OWNED BY NONE and
skipped IF NOT EXISTS/IF EXISTS commands take no dependency-table lock.
ALTER retains sequence-before-table ordering and participates in the canonical
deadlock graph, including soft queue reordering and unavoidable-cycle40P01.

Sequence SET SCHEMA rejects moves into or out of temporary namespaces with
`0A000` / `cannot move objects into or out of temporary schemas`, including the
`pg_temp` creation alias and the caller's actual temporary namespace identity.
Another session's temporary destination is rejected earlier by creation authority
with `42P16` / `cannot create relations in temporary schemas of other sessions`.
Linked-sequence and destination missing/CREATE checks retain their precedence. A
rejected move leaves identity, cached values, held analysis and physical-session
ownership unchanged; ordinary persistent-to-persistent moves remain valid.
Smoke: `make smoke RUN='^TestSequenceTempMoveSmoke$' VERBOSE=1`.

CREATE SEQUENCE resolves explicit actual namespace identities before CREATE and
requested-persistence checks. TEMP accepts its caller-owned temporary namespace;
ordinary logged creation there becomes temporary. TEMP in a persistent namespace
fails `42P16` / `cannot create temporary relation in non-temporary schema`;
UNLOGGED in any temporary namespace fails `42P16` /
`only temporary relations may be created in temporary schemas`. Foreign TEMP
creation retains the `42P16` ownership record above. Missing/denied namespaces
retain `3F000`/`42501` precedence, including IF NOT EXISTS; failures publish no
private namespace or sequence state.
Smoke: `make smoke RUN='^TestSequenceTempCreationSmoke$' VERBOSE=1`.

Temporary namespaces remain sequence-only. Ordinary CREATE TABLE rejects actual
own/peer temporary namespaces and an initialized `search_path=pg_temp` with
`0A000` / `temporary schema creation is unsupported`, before IF NOT EXISTS or
descriptor/storage publication. This ordinary-table guard does not change the
supported sequence creation rules above. Actual namespace/path errors have no
Position; the existing explicit `pg_temp` creation alias retains Position14.
Smoke: `make smoke RUN='^TestSequenceTemporaryTableBoundarySmoke$' VERBOSE=1`.

The four selected sequence functions and minimal regclass casts reuse the common typed scalar binder/eager evaluator. Literal regclass lookup happens at Prepare with character positions. Nonnull regclass parameters become canonical OIDs during coherent Execute parameter import, before zero-row evaluation, runtime NULL checks or nested function effects; input errors retain portal Context without a literal Position. Numeric/dash inputs require no existing relation. NULL arguments are strict and do not open a sequence. Exact session-before-use, missing/wrong-kind, option, bound, ACL and NOTICE diagnostics have independent pinned-release fixtures. This does not admit arbitrary relationless SELECT, user-defined functions or general casts.

Regclass input follows PostgreSQL qualified-name splitting, separately from the
SQL statement lexer. Unquoted punctuation and comment-looking bytes remain part
of the name; only dots and six ASCII whitespace bytes delimit components.
Quoted components preserve case/embedded dots and decode doubled quotes; names
fold ASCII and clip at 63 UTF8-safe bytes. Only exact nonempty ASCII digit strings
or exact `-` select the opening-free OID path. Padded digit/dash strings instead
resolve symbolic names, and malformed complete input rejects before effects.
Smoke: `make smoke RUN='^TestSequenceRegclassInputSmoke$' VERBOSE=1`.

Both `value::regclass` and `CAST(value AS regclass)` are admitted through the shared cast syntax alongside the existing bounded explicit-cast matrix above; unsupported source/target pairs retain that matrix's diagnostics. Regclass (OID2205) is a sequence expression/result identity, not an admitted stored column type. Valid sequence CREATE prepares without namespace permission checks. At execution, schema CREATE denial has no Position; ordinary CREATE validates options first, while IF NOT EXISTS checks namespace authority before skipping an existing relation or validating options. Literal regclass schema USAGE denial remains an analysis/Prepare error positioned at the input literal.

Standalone selected regclass projections use the `regclass` column label and
PostgreSQL name output: visible names are unqualified, shadowed/off-path names
are schema-qualified, and identifier quoting uses the shared keyword inventory.
Missing nonzero OIDs display decimal text; zero displays `-`. The common result
exporter uses an immutable execution output snapshot without replacing internal
typed OID datums or adding general OID casts/catalog queries.

Sequence metadata, transactional generator replacements and INSERT/UPDATE/DELETE row versions publish together at Finish/COMMIT through the same immutable prepare/validate/install frontier. Ordinary nextval/setval effects and physical-session memories are outside rollback. RX protects demanded nonnull function calls; ALTER uses SRX and administrative rename/schema changes use AX. Sequence row reads use AS and retain captured data/types through subsequent changes. Native log_cnt0 intentionally differs from PostgreSQL WAL prelogging counters; tests assert both independently.

Sequence DDL and COMMENT ON SEQUENCE are the bounded exception to the general explicit-transaction DDL exclusion above. TEMP sequence namespaces are session-private, shadow persistent relations, and survive persistent Reset/Restore until physical end. Controls preserve settings/roles/temp memories, exclude TEMP objects from Dump, serialize effective persistent options and called state, and reject invalid staged replacement atomically.

The internal native catalog-fixture replacement seam imports a persistent source
projection only. Repeated self replacement preserves destination live TEMP once;
foreign or already-retired source TEMP is never imported or resurrected.
Destination TEMP identity, builtin type dependencies, generator, local values
and cache remain exact; imported persistent metadata and detached values use
coherent type identities without mutating retained source catalogs or rows.
Public Restore still rejects TEMP declarations rather than importing them.

Prepared sequence SQL is source, not a permanently retained regclass datum.
Its complete literal group reanalyzes through the existing binder at Execute
start on sequence relation-plan stamp, effective namespace changes or namespace
catalog invalidation. An
already-running function retains its analyzed OID across a queued RX wait.
Rename/move missing original names
fail42P01 without allocation; ordinary SQL DROP/recreation may resolve a new
identity. Numeric OIDs retain their bits and missing opening failsXX000.
Ordinary generator changes and relation shadow creation alone preserve held
analysis. Explicit sequence options rewrite even unchanged values; actual
owner/persistence transitions and sequence ACL publications invalidate.
OWNED BY-only, same-owner and same-persistence forms preserve the stamp;
bare ALTER SEQUENCE without an action or option is a syntax error.
Effective paths compare ordered namespace identities/implicit flags,
not raw setting text; unchanged execution reuses the session path cache.
Namespace publications invalidate both that cache and held sequence analysis,
even with an unchanged effective path, through an independent bounded scalar
without consuming descriptor revisions. Exhaustion rejects publication
atomically, including namespace ACL updates and schema drop closures; native
catalog replacement retains its separate epoch authority.
First committed TEMP namespace insertion uses this same checked authority before
catalog/generator/row/session installation. Rollback or relation-only creation
in an existing TEMP namespace does not publish a namespace event. Physical
EndSession retires native logical TEMP ownership without namespace invalidation
or epoch advance: PostgreSQL RemoveTempRelations preserves the namespace tuple
for backend-slot reuse. Native retirement installs detached coherent state;
previously captured catalog views and result rows remain immutable.
Temporal captures, parameter/result signatures and returned cursors remain
immutable. Native persistent Reset/Restore keeps its separate epoch-fence
contract; exact live caller-owned TEMP provenance alone may refresh.
Native sequence-only reanalysis preserves existing Prepare-time dynamic temporal
captures. PostgreSQL full-query reanalysis can recapture `TIMESTAMP 'now'` at
its reanalysis transaction start; this is an independently asserted bounded
native temporal distinction, not full prepared-query parity or a general
temporal cutover. Static temporal values and parameter/result/sequence contracts
remain exact.

RX is demand-driven after once-only argument evaluation. A queued rename
continues opening/allocating the original OID; the next held Execute reanalyzes
the original missing name and fails42P01. Queued DROP instead fails the running
opening withXX000/no Position, followed by42P01 with literal Position on the
next Execute. No function arguments or prior allocations are replayed.
Filtered/zero-row targets do not open/lock a sequence. These four strict builtins
simplify a known constant NULL argument to typed INT8 NULL, eliminating even
volatile sibling arguments; a runtime column NULL retains once-only nested
argument effects but skips the outer sequence opening/RX. This bounded rule is
not a generic SQL function optimizer.

Sequence DDL/restart and transactional INSERT/DELETE publish their catalog,
generator, appended-row and deletion-end roots atomically at one commit frontier.
DELETE predicates use the same sequence argument/preflight/demand contracts as
other scalar consumers; demanded nextval/setval effects survive row rollback.
DELETE of a sequence itself fails at execution with `42809` / `cannot change
sequence "s"` before predicate allocation. Retained row results and new
INSERT SELECT/Dump consumers respectively retain their captured snapshot and
observe only live survivors. Feature smoke:
`make smoke RUN='^TestSequenceDeleteCompositionSmoke$' VERBOSE=1` (also a
`TestSequenceSmoke` subtest).
