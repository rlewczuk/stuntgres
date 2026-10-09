# SQL dialect

Supported SQL surface, native limits and intentional PostgreSQL differences. See
[README](../README.md) for getting started, the [SQL reference](SQL.md) for detailed
feature contracts, and the [development guide](DEVELOPMENT.md) for contributor instructions.

## Supported surface and limits

### Domains — change note

`CREATE DOMAIN [schema.]name [AS] base_type [COLLATE [schema.]collation] [NULL]`
supports the existing stored scalar families, domain chains and bounded NAME[].
The target schema requires CREATE; a domain base requires its represented USAGE
authority. Domains have distinct catalog IDs, owners, namespaces and immediate
bases. Their nullable values use the ultimate base codec/typmod/collation.
Known same-domain CASE/COALESCE/VALUES inputs retain logical identity; mixed
base/distinct-domain inputs flatten through existing common-type rules. Supported
operators/functions, ordering, grouping, COUNT and BOOL_AND/BOOL_OR use
base signatures; assigning or explicitly casting back restores the domain tag.

Duplicate names fail42710 before resolving the proposed base. Missing types fail
42704; void/unknown/record are invalid domain bases (42804). Domain use-site
modifiers fail42601. Literal input preserves source character positions and base
SQLSTATE/messages; parameter input retains existing adapter diagnostics.
Contextual literals read the unmodified physical base first: lexical errors stay
eager, while assignment typmod enforcement and explicit truncation use the normal
execution coercion. Qualified modifier syntax resolves the actual catalog type,
including a domain whose name is also a builtin spelling.
The default is typed NULL. Domain DEFAULT/CHECK/NOT NULL, ALTER DOMAIN, general
arrays, GRANT/REVOKE TYPE/DOMAIN and explicit-transaction domain utilities remain
unsupported, not successful no-ops.
The migrated visibility boundary retains its exact CREATE DOMAIN d AS integer
Query, empty utility result and typed7/true INT4/BOOL witness. DROP followed by
quiet Prepare leaves the type absent until SAME held Execute; six other native
DDL exclusions and the default-function negative remain. Original source and
recorded Context consumers are separate phases, not new dialect support.


`DROP DOMAIN [IF EXISTS] name [, ...] [RESTRICT | CASCADE]` requires ownership.
RESTRICT retains all objects/data and reports actual dependent types, columns and
stored expressions. CASCADE removes the transitive closure, preserving unrelated
columns/tables, stable surviving attributes and row identities; schema removal
includes external dependents. Missing IF EXISTS emits a normal NOTICE.
Native logical Dump/Restore orders domains before dependent persistent objects
and reowns logical/base pointers, owners, payloads and dependencies atomically.
It is not PostgreSQL wire storage or external pg_dump support. Native pg_type
projections, pg_typeof and general array operators are not added.
See [type/driver contracts](TYPES.md#domains--change-note) and the domain feature
smokes in [README](../README.md).


### Constraint/expression introspection — change note

The native catalog sources add exactly `pg_catalog.pg_constraint`
(`oid OID, conname NAME, contype CHAR, conrelid OID, conbin PG_NODE_TREE`) and
`pg_catalog.pg_attrdef` (`oid OID, adrelid OID, adnum INT2, adbin PG_NODE_TREE`).
They project existing CHECK (`c`), NOT NULL (`n`, NULL conbin) and explicit
nontrivial defaults from real immutable descriptors. Attribute numbers are stable,
not dense evaluation slots. These are not general constraint/catalog rollout:
foreign keys, indexes, generated columns and bonus metadata remain absent.

`pg_node_tree` is distinct type194 with PG_NODE_TREE metadata and native string
ScanType. Non-NULL values are internal immutable structural carriers. Output uses
native deterministic node tags, concrete type/typmod, attribute/provider identities,
constant payloads and recursive children. It is neither original/replay SQL nor
an opaque lookup token, and is **not** PostgreSQL nodeToString physical interchange.
External non-NULL input always fails0A000, including empty/invalid text; typed NULL
is ordinary. The four `pg_get_constraintdef`/`pg_get_expr` signatures return TEXT,
with structural rendering, strict NULL, current names and supplied-relation lookup.
Both modes preserve grouped predicate operands and current provider visibility.
Standard/postfix same-type scalar annotations and existing directional numeric
edges consume returned definitions; function casts and known-TEXT input conversion
boundaries are unchanged. Literal NULL may carry `::unknown` syntax provenance,
but UNKNOWN is not an input/DDL type.
Synthetic comparison casts are unsized base-type annotations, not destination
column assignments; actual explicit NUMERIC precision/scale remains enforced.
Reconstruction does not add NAME[] equality/order or plain TIME AT inputs.
current_schemas behavior uses strict BOOL NULL and public changing-path output;
AT/LOCAL consumer fixtures use typed TIMESTAMP/TIMESTAMPTZ/TIMETZ columns.
Persisted identity leaves preserve keyword versus ordinary-call syntax and stable
NAME/TEXT typing; neither SQL nor structural output evaluates the identity.
Native SYSTEM_USER remains typed NULL, but its nontrivial DEFAULT stays stored.
These consumers do not change identity providers, authority or namespace resolution.
See [query contracts](QUERIES.md#constraint-and-expression-deparsing).


CREATE TABLE supports unqualified or schema-qualified names with SMALLINT/INT2, INTEGER/INT/INT4, BIGINT/INT8, NAME, TEXT, BOOLEAN/BOOL, NUMERIC/DECIMAL/DEC[(p[,s])], REAL/FLOAT4, DOUBLE PRECISION/FLOAT8, FLOAT[(p)] (1–24 real, 25–53 double; omitted p double), DATE, TIME[(0–6)] [WITH | WITHOUT TIME ZONE], TIMESTAMP[(0–6)] [WITH | WITHOUT TIME ZONE], TIMETZ[(p)], TIMESTAMPTZ[(p)], UUID, BYTEA and optional NULL/NOT NULL; INSERT VALUES supports optional target columns/multiple rows; SELECT supports mixed stars or scalar literal/query-column/parameter targets with FROM, scalar output labels, binary scalar comparison leaves and Boolean AND/OR/NOT with grouping in targets and WHERE, bare scalar WHERE in contextual BOOL, and expression/output-label/ordinal ORDER BY with multiple keys and per-key ASC/DESC/NULLS FIRST/LAST. FROM remains required outside identity/settings special forms and the selected null-count-only form. Values include exact decimal fractions/exponents, integer/radix literals inferred INT4/INT8/NUMERIC by signed range, quoted strings, TRUE/FALSE/NULL, 1-based `$n` and explicitly typed date/time/timestamp literals. Float4 stores binary32, Float8 binary64; both export float64, and all NaNs compare equal above non-NaNs. Temporal input/output uses captured TimeZone/DateStyle, BC/infinity and existing precision sizing; prepared constants freeze while parameters reread execution start. Finite public timestamps are time.Time, infinities owned bytes; time.Time parameters import civil fields for TIMESTAMP and preserve instants for TIMESTAMPTZ. Quoted identifiers, nested comments, doubled quotes/apostrophes and newline string concatenation follow README's C-locale UTF8 subset. Unresolved output parameter occurrences fall back to TEXT after filter analysis, retaining their initially known type: a target-first unknown occurrence conflicts with a later non-TEXT constraint (`42P08`), while TEXT target/filter reuse works. Same-context repeats work, unused gaps fail, mixed assignment destinations remain unsupported. Text/parameter inputs coerce through type input functions; INSERT assignment casts and comparison operator checks differ. WHERE keeps only TRUE. Defaults remain ASC NULLS LAST/DESC NULLS FIRST; explicit NULL placement overrides direction. See [expression ordering](QUERIES.md#expression-ordering--change-note-changelog).

### Interval values — change note

The supported type surface also includes INTERVAL with every permitted field
range and precision declaration, verbose/SQL/ISO8601 input, NULL/prepared binds,
comparison/grouping and native round trips. Month/day/time representations stay
distinct even where SQL30-day-month/24-hour-day comparison considers them equal.
IntervalStyle is real per-session input/output state; prepared constants capture
input at Prepare and cursor output at Execute. See [types](TYPES.md#interval--change-note)
and [settings](SETTINGS.md#intervalstyle--change-note).
Malformed/range/precision errors use originating PostgreSQL logical diagnostics
and atomic publication. No interval arithmetic, date-time cross-family cast,
justify/extract/make_interval, wire protocol or physical storage is added.
Smoke: `make quickcheck PACKAGES=./smoke RUN='^TestIntervalSmoke$' VERBOSE=0`.

FROM supports comma products, CROSS JOIN, INNER/LEFT/RIGHT/FULL JOIN ON or USING,
optional OUTER for non-inner kinds, and NATURAL variants, chains and parentheses. Table/whole-join aliases with or
without AS support positional column aliases; qualified columns and mixed stars
resolve the visible namespace. USING's special AS alias exposes merged keys
without hiding operands. USING key order and NATURAL left-common-column order,
duplicate multiplicities and NULL nonmatch follow PostgreSQL18.0. Unmatched
operands receive typed NULLs before WHERE. LEFT/RIGHT merged keys select their
coerced preserved operand; FULL uses COALESCE with no physical output origin.
Every source is authorized and captures one coherent selected statement view.
Prepared execution rebinds all sources; eager results survive native Reset/Restore.
LATERAL joins, derived SELECT, CTE and function FROM remain excluded;
parenthesized VALUES is a supported non-lateral FROM operand.
See [join contracts](QUERIES.md#inner-and-cross-joins--change-note).
Feature smoke: `make quickcheck PACKAGES=./reference RUN='^TestReferenceQuery(OuterJoins|Joins)' VERBOSE=0`;
independent pinned reference: `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceQuery(OuterJoins|Joins)' VERBOSE=0`.

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
CREATE OR REPLACE VIEW preserves view/rule/attribute identity, owner, ACLs,
comments and dependents while replacing the definition and outgoing dependencies.
Existing names/types/typmods/collations form a required prefix; trailing columns
may be added. ALTER VIEW supports relation/column rename, owner/schema changes
and security SET/RESET; grouped DROP VIEW supports RESTRICT/CASCADE and IF EXISTS.
Scalar COLLATE annotations support default/C/POSIX and the fixed Unicode16
`pg_c_utf8`/`pg_unicode_fast` identities; arbitrary locale/ICU providers remain excluded.
Cyclic replacement is admitted, but recursive reads fail42P17 and native cyclic
Dump explicitly fails. CREATE RECURSIVE/TEMP syntax, check options, view writes,
column defaults/type-changing ALTER, full catalogs/RLS/UDFs/optimizer facilities
and explicit-transaction view DDL remain excluded.
Native fixture controls serialize current definitions, not materialized results
or alteration history.
Feature smokes: `make quickcheck PACKAGES=./smoke RUN='^TestViewSmoke$' VERBOSE=0` and
`make quickcheck PACKAGES=./smoke RUN='^TestViewReplaceSmoke$' VERBOSE=0`.

SELECT targets support `AS label` and eligible bare labels for view definitions
and normal output metadata. ORDER resolves output labels and ordinals alongside
input expressions and accepts multiple keys. WHERE/HAVING do not expose output
labels; GROUP BY retains its input-name priority and existing ordinal rules.

FROM column aliases rename input names; SELECT labels rename output names only
and are not visible to sibling targets or WHERE. Bare ORDER labels prefer
output names; qualified/compound ORDER expressions use input names. Missing
column Hints use exposed base relation/view aliases with PostgreSQL18 bounded
character distance, qualifier penalties and one/two-candidate tie handling;
three tied fuzzy occurrences and distant names suppress suggestions. Exact
inaccessible base columns instead take precedence and produce singular/plural
Detail, with PostgreSQL's table-qualified-name Hint for an unqualified request
matching multiple hidden entries. Duplicate exact column spellings within one
base entry instead raise positioned42702, regardless of qualifier penalty or
earlier exact entries. Diagnostics never change visibility or physical-column
authorization. Complete logical English
diagnostics and character positions have independent pinned18.0 reference
expectations. See [alias diagnostic fidelity](QUERIES.md#query-labels-and-relation-aliases--diagnostic-fidelity-change-note).
Feature smoke: `make quickcheck PACKAGES=./smoke RUN='^TestQueryAliasesSmoke$' VERBOSE=0`.

Four trailing SELECT locking clauses are supported: `FOR KEY SHARE`, `FOR SHARE`,
`FOR NO KEY UPDATE` and `FOR UPDATE`, including current inner/cross joins,
persistent scalar views and INSERT SELECT sources. They lock contributing base
RowIDs eagerly before result exposure and recheck the same frozen candidate
combinations after READ COMMITTED contention. Unmarked reads remain nonblocking.
Marks stored in a view apply to ordinary reads of that subtree, not unrelated
sources; overlapping clauses normalize KEY SHARE < SHARE < NO KEY UPDATE < UPDATE.
Enclosing marks reaching an outer join's nullable side reject on Execute after
successful Prepare, including empty/LIMIT0 queries. Local stored input marks
remain legal on either side of an unmarked outer query and settle at real-source
demand, never at absent extensions. FULL path-capability0A000 is also Execute-only:
immutable Boolean constants or a usable opposite-side equality conjunct admit it;
inequality alone or same-side equality does not. Native left-major demand is not
PostgreSQL's hash-build lock order: empty-left LEFT skips right input marks,
empty-right RIGHT scans left input marks. Selected/preserved physical marks and
LIMIT0 guarantees remain independently tested across backends.
An unmarked upper aggregate may consume a marked view: input locks precede
grouping/HAVING, so an empty HAVING result does not release them. A FOR clause
reaching GROUP BY/HAVING/aggregate output fails mode-specific0A000; sequences
fail42809 at Execute. Native virtual catalogs reject marks with0A000
`row locking is not supported for virtual catalog relations`, including empty
and nested view paths. PostgreSQL's physical catalog lockability is an independent
reference control, not native parity. READ ONLY
fails25006 at Execute. At RR, KEY SHARE preserves snapshot values after a
compatible update leaving UNIQUE keys unchanged; stronger modes and all concurrent
deletions fail40001 with concurrent-update wording. Every table needs used-column
SELECT plus UPDATE on any live column. `OF`, `NOWAIT`, `SKIP LOCKED` and repeated
SELECT locking clauses remain excluded, as does adjacent query syntax.
UPDATE uses NO KEY UPDATE for unchanged keys, UPDATE for changed UNIQUE key
datums; DELETE uses UPDATE. Matrix, lifetime,
savepoint contribution restoration and exact diagnostics:
[row locks](TRANSACTIONS.md#row-locks--change-note).
Smoke: `make quickcheck PACKAGES=./smoke RUN='^TestRowLock(Smoke|JoinSmoke|VersionSmoke|ViewSmoke)$' VERBOSE=0`;
reference: `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceRowLock' VERBOSE=0`.

OID is also a supported column type: distinct unsigned32 type26, native int64
results, unsigned comparison/order and directional integer casts. See
[OID scalar semantics](TYPES.md#oid--unsigned-scalar-change-note) for text syntax,
signed-bit reinterpretation versus checked INT8 conversion and adapter metadata.
It adds no metadata catalog population, object lookup/allocation or OID arithmetic.
Smoke: `make quickcheck PACKAGES=./smoke RUN='^TestOIDSmoke$' VERBOSE=0`.

### LIMIT, OFFSET and FETCH — change note

Table SELECT, standalone/FROM VALUES, shared INSERT SELECT/decorated VALUES,
stored scalar views and already-admitted singleton calls accept LIMIT/ALL, OFFSET [ROW/ROWS] and FETCH
FIRST/NEXT [count] ROW/ROWS ONLY. OFFSET and LIMIT/FETCH accept either order;
FETCH defaults to1. Bounds use BIGINT assignment conversion and evaluate once,
OFFSET first. NULL OFFSET is0, NULL LIMIT/ALL unlimited; negative bounds give
2201X/2201W. LIMIT0 skips producing children but retains full analysis, source/
destination authorization, metadata and bound diagnostics. Nonblocking predicates/
views/joins are demand-driven; Sort/aggregate and local count are effect/lock
frontiers. WITH TIES, comma LIMIT and ordinary no-FROM SELECT remain excluded.
See [demand and persistence](QUERIES.md#limit-offset-and-fetch--change-note-changelog).
Smoke: `make quickcheck PACKAGES=./smoke RUN='^TestQueryLimit(Smoke|LockSmoke)$' VERBOSE=0`.


Table SELECT and shared INSERT SELECT also support scalar/multiple GROUP BY
keys, one-based output ordinals, contextual BOOL HAVING, count(*) and
count(expression) (count/INT8/int64). Expression count ignores NULL, not zero,
false or empty strings. Global empty/all-NULL input returns one count0;
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

`bool_and(BOOL)`, `bool_or(BOOL)` and `every(BOOL)` return BOOL/Go bool with
known nullable metadata, including NOT NULL inputs. NULL inputs are ignored;
empty/all-NULL global input returns one NULL, empty keyed input no rows.
Every aliases AND behavior, not its raw label or diagnostic name. Unknown
string/NULL literals and bare parameters infer BOOL; typed TEXT/INTEGER arguments
give42883 with actual signature, explicit-casts hint and character call position.
All eligible argument occurrences evaluate even after a decisive Boolean state.

`avg(INTERVAL)` returns nullable INTERVAL/Go string, ignoring NULL and returning
NULL for empty/all-NULL input. Checked signed32 month/day and signed64 microsecond
sums can overflow even when the mathematical average would fit (`22008`,
`interval out of range`). Fractional months cascade at 30 days/month, fractional
days at 24 hours/day; nearest-even floating-point rounding preserves physical
components without justifying days/time. The result does not inherit source
field/precision restrictions. Mixed positive/negative infinities fail22008;
same-sign infinity dominates only after finite transitions have been checked.
Unknown strings, NULL and unconstrained parameters give42725 ambiguity; use an
explicit cast to a supported AVG input. Invalid types/arity give42883 with the
real signature, explicit-casts hint and character position.

`avg(SMALLINT/INTEGER/BIGINT/NUMERIC)` returns unrestricted nullable NUMERIC;
`avg(REAL/DOUBLE PRECISION)` returns unrestricted nullable FLOAT8. Empty/all-NULL
global input returns typed NULL; empty keyed input returns no rows, retaining
metadata. NUMERIC sums are exact, with base-10000 division scale capped at1000;
packing the finite sum can fail22003 even if the mean fits. NaN or opposite
numeric infinities return NaN; one infinity sign dominates finite sums.
REAL inputs widen from stored binary32. Float AVG retains the full
Youngs–Cramer state: sum or Sxx overflow fails22003, `value out of range: overflow`.
Final division permits IEEE underflow/signed zero; NaN/infinity follow IEEE
arithmetic. Result metadata drops input typmod and physical origin. Eager
execution preserves late-error/cancellation and INSERT SELECT atomicity, without
promising an unordered floating accumulation order.
Public smoke: `make quickcheck PACKAGES=./reference RUN='^TestReferenceNumericAvgSmoke$' VERBOSE=0`;
independent oracle: `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceNumericAvgSmoke$' VERBOSE=0`.

`any_value(anyelement)` returns an arbitrary non-NULL datum of the concrete input
type, not a stable first/last/minimum/maximum winner. Empty/all-NULL global input
returns typed NULL; empty keyed input returns no rows. Result metadata is known
nullable, retains input collation, drops source typmod and has no physical origin.
Existing codecs and immutable datum representations are unchanged. Unknown
NULL/string literals and unresolved parameters give42804 without a position;
cast the argument explicitly. Zero/star/multiple arguments give42883. All input
arguments evaluate even after a candidate is retained, preserving late-error
and cancellation atomicity. Direct/prepared calls, views/native roundtrip,
HAVING/ORDER and INSERT SELECT share the ordinary aggregate pipeline.
Public smoke: `make quickcheck PACKAGES=./reference RUN='^TestReferenceAnyValueSmoke$' VERBOSE=0`;
independent oracle: `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceAnyValue' VERBOSE=0`.

Grouping does not admit standalone SELECT without FROM,
aggregates other than count/bool_and/bool_or/every/avg(interval or numeric inputs)/any_value, FILTER/DISTINCT/ALL/in-call ORDER/OVER, DISTINCT/ALL grouping,
grouping sets/ROLLUP/CUBE/empty grouping sets.
No-FROM aggregate exclusion applies recursively across all targets, including
siblings of selected sequence/REGCLASS singleton expressions. Group-expression
constant identity preserves representation (including NUMERIC scale), separately
from runtime typed grouping equality.
Feature smoke: `make quickcheck PACKAGES=./reference RUN='^TestReferenceQueryAggregateSmokeMigration$' VERBOSE=0`;
real independent oracle: `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceQueryAggregate' VERBOSE=0`.
Expression-count smoke: `make quickcheck PACKAGES=./reference RUN='^TestReferenceCountExpressionSmokeMigration$' VERBOSE=0`;
independent oracle: `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceCountExpression' VERBOSE=0`.
Boolean smoke: `make quickcheck PACKAGES=./reference RUN='^TestReference(BooleanAggregate[A-Za-z0-9_]*|AnyValueSourceACL)$' VERBOSE=0`;
independent oracle: `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceBooleanAggregate' VERBOSE=0`.
Parameterless count() gives42809 and preserves a qualified call name in its message.
Children are analyzed before the more-than-100-argument guard54023, then
namespace/visibility and signature resolution. An absent explicit schema gives
3F000 only after child analysis and the guard; bad arity (including 100 arguments)
gives42883 with actual argument types.
Only a resolved aggregate call rejects nesting with42803 at the inner call.
ANY does not infer bare parameter types: cast unresolved parameters explicitly.

Simple/searched CASE, COALESCE, NULLIF, GREATEST and LEAST are scalar constructs
in every existing general-scalar consumer, including DEFAULT/native roundtrip.
Native CASE and COALESCE skip unneeded runtime children; native NULLIF and extrema
demand all arguments. Pinned PostgreSQL can instead plan immutable bound
conversions before CASE/COALESCE demand, and `NULLIF(NULL,10/v)` at v=0 returns
SQL NULL rather than native22012. The reference suite asserts these outcomes
independently, not as an optimizer guarantee. All children still undergo analysis
and authorization. A bounded
execution-only constant simplifier can reject constant division by zero beneath
a row-dependent CASE/COALESCE, while decisive constant branches prune their
unneeded runtime expressions. This is not general Boolean short-circuiting.
Common-type and equality-overload resolution execute across the already-supported
scalar types, including implicit DATE→TIMESTAMP; this does not add general casts,
ordinary routines, arrays/VARIADIC or ordinary relationless SELECT.
See [conditional expressions](QUERIES.md#conditional-expressions).
Feature witnesses: `make quickcheck PACKAGES=./smoke RUN='^TestConditionalSmoke$' VERBOSE=0` and
`make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceConditional' VERBOSE=0`.

### Standalone and FROM VALUES — change note

VALUES is a query and a parenthesized, optionally aliased non-lateral FROM source.
It exposes computed `columnN` columns, independently common-types each column
(unknown-only becomes TEXT), preserves duplicate occurrences and typed NULLs,
and supports local ORDER BY and LIMIT/OFFSET/FETCH. Positional column aliases may
rename a prefix. Existing inner/cross joins, expressions, INSERT SELECT and
stored views compose through the shared binder and occurrence runtime.
DEFAULT, aggregates and surrounding FROM references are not admitted in cells.
Missing-column suggestions include VALUES labels/aliases without a catalog
identity. Inaccessible preceding operands retain table/column LATERAL diagnostic
hints, independently of whether those operands are physical; no correlation is added.
Synthetic columns have no physical ACL/origin/locking identity. Standalone
VALUES rejects FOR UPDATE; FROM VALUES accepts existing SELECT locking with no
synthetic locks. Mixed physical sources retain ordinary lock/recheck behavior.
Native rechecks reuse eagerly captured synthetic values/effects instead of
PostgreSQL EPQ volatile-expression replay; sequence outputs/counters can differ.
Existing native join/view predicate boundaries also retain operand demand where
PostgreSQL may avoid an already-rejected operand. Physical row replacement and
predicate requalification remain supported; this adds no expression replay.
Stored views deparse and round-trip through native controls with owned metadata
and results. No derived SELECT, LATERAL or new locking syntax is added.
See [VALUES contracts](QUERIES.md#standalone-and-from-values--change-note-changelog).
Independent pinned reference: `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceQueryFromValues' VERBOSE=0`.


INSERT additionally accepts SELECT sources with the existing projection/filter/
expression-ordering surface, parenthesized sources and INSERT-only no-FROM literal/
parameter/scalar projections. Empty `(SELECT)` is one zero-width occurrence;
`SELECT FROM source` retains table occurrence/filter semantics. These INSERT-only
forms do not expand standalone SELECT. Explicit targets match source
schema width; implicit targets map first N live columns. Omitted columns use
declared defaults or typed NULL and enforce NOT NULL. SERIAL uses ordinary
width-checked defaults, distinct from identity overrides. The minimum fixed
early-bound nextval leaf is also admitted in declared DEFAULT expressions.
Already-supported casts and null/truth/distinct predicates, DATE and BYTEA are
preserved in INSERT sources. Shared SELECT analysis retains current cast result
names. Recursive conversion preflight uses the imported execution frame, outside
the engine lock and before source capture; analyzed query nodes remain immutable.
Selected sequence calls also work in INSERT SELECT/VALUES producers through
that same binder and StatementScope; sequence FROM sources capture the same
three-value row as ordinary SELECT. Cast preflight visits sequence arguments
without evaluating the sequence call. Prepare consumes no sequence values.

Plain or parenthesized undecorated VALUES remains destination-context input.
VALUES with ORDER BY or LIMIT/OFFSET/FETCH uses column-wise common typing,
generated `columnN` names, eligible ordinals/directions/NULL placement, and
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

Feature smoke: `make quickcheck PACKAGES=./smoke RUN='^TestInsertSelectSmoke$' VERBOSE=1`.
Combined real-oracle feature reference (not a PostgreSQL-only empty regex):
`make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceInsertSelect' VERBOSE=1`.
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
and unsupported LastInsertId. Omitted RETURNING has no result relation.
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
PostgreSQL-only witness. Native FKs, triggers, DELETE USING/CURRENT OF,
CTE/inheritance targets and adjacent unsupported row mechanisms remain excluded.
Smoke: `make quickcheck PACKAGES=./smoke RUN='^TestDeleteCoreSmoke$' VERBOSE=1`;
reference: `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceDeleteCore' VERBOSE=1`.
Integrated expressions: `make quickcheck PACKAGES=./smoke RUN='^TestDeleteCoreIntegratedExpressionsSmoke$' VERBOSE=1`.

DELETE RETURNING supports mixed target/OLD/NEW stars and all already-admitted
general scalar projections, labels, parameters and sequences, with
`WITH (OLD AS o, NEW AS n)` output aliases. Actual successful deletion
predecessors provide OLD/default values; ordinary NEW is source-typed NULL
with nullable metadata and the same source-column SELECT requirement.
Explicit aliases/visible target names mask implicit OLD/NEW; output namespaces
do not leak into WHERE. Prepare is effect-free; held execution rebinds, checks
current ACLs and validates its output signature. Finalized rows are owned,
eager and never reevaluated by Next, including when Exec ignores output.
No-match output retains full metadata. Output errors or prepublication
cancellation discard transactional deletes atomically, not sequence draws.
Postpublication consumer cancellation does not undo deletion.
Dump/Restore persists committed survivors/objects and sequence state, not
RETURNING cursors/history; its mutation-script whitelist is not widened.
Whole-row records, scalar subqueries, absent windows/SRFs, triggers/FKs,
view writes/rewrite-to-UPDATE and physical tuple/C/wire metadata remain
PostgreSQL-only boundaries. See [output details](QUERIES.md#delete-returning--change-note).
Feature smoke: `make quickcheck PACKAGES=./smoke RUN='^TestDeleteReturning(Smoke|ControlsSmoke)$' VERBOSE=0`;
reference: `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceDeleteReturning' VERBOSE=0`.

UPDATE RETURNING shares scalar projection/alias binding and eager result ownership
with DELETE RETURNING. OLD is the actual predecessor used by simultaneous SET;
NEW, unqualified and target-qualified outputs read the constrained replacement.
Both real versions preserve declared source metadata and SELECT dependencies.
Output aliases are visible only in RETURNING. Target aliases and ordinary
qualifications in SET/WHERE use the shared UPDATE namespace. Same-value matches
return/count once; duplicates survive and no matches retain metadata. WHERE analysis precedes RETURNING output
fallback, which precedes SET; prepared execution freshly checks ACLs and result
signature. Output/constraint/cancellation failure publishes no partial replacement
or output, following existing abort/savepoint rules; sequence draws are not undone.
Dump/Restore persists committed live data/catalog/sequence state, not output/history;
its script whitelist is unchanged. Triggers, whole-row records/record expansion,
scalar subqueries, view writes/rewrites/rules/partition movement and physical/C/wire
tuple metadata are genuine PostgreSQL-only mechanisms, not native no-ops.
See [UPDATE output details](QUERIES.md#update-returning--change-note).
Feature smoke: `make quickcheck PACKAGES=./reference RUN='^TestReferenceUpdateReturning(MigratedSmoke|ControlsSmoke)' VERBOSE=0`; pinned mode: `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceUpdateReturning(MigratedSmoke|ControlsSmoke)' VERBOSE=0`.



Column DEFAULT declarations admit outer scalar/typed temporal/existing cast/
INT4/INT8 unary and binary arithmetic, integral bitwise operations, comparison
and IS [NOT] DISTINCT FROM syntax.
CAST and postfix casts reuse existing baseline, character-family, bounded OID/
temporal conversions, same-type stored scalar usages and directional numeric
edges. Function-style casts retain their bounded whitelist, including oid.
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
NULL; explicit NULL bypasses defaults.
Reordered explicit targets map supplied parameters and DEFAULT to their named
destinations, not declaration-order input slots. DEFAULT markers consume no
parameter index; a supplied parameter NULL bypasses the declared default.
See [TABLES](TABLES.md) for declared metadata and native-control boundaries.
SELECT and ORDER-decorated VALUES sources use omitted destination defaults.
One execution-only default-demand phase
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
VALUES retain requests. Count-decorated SELECT/VALUES use the shared source
grammar; FROM-less WHERE remains excluded, without additional default features.
Public targetless DEFAULT VALUES requests one
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
operator/type is implied. UPDATE DEFAULT remains excluded. Identity columns use
an internally owned sequence, not a declared DEFAULT. SERIAL expands to integer
NOT NULL columns, width-matched AUTO-owned sequences and ordinary fixed nextval
defaults. Only early-bound nextval leaves and assignment/cast wrappers are admitted;
other sequence/volatile default composition remains excluded. Disk/WAL,
network protocol, native ABI extensions and external pg_dump/custom archives
are specifically non-engine mechanisms, never permission for successful stubs.
Feature smoke: `make quickcheck PACKAGES=./smoke RUN='^TestDefaultSmoke$' VERBOSE=1`.
Mixed-parameter/reordered-target INSERT coverage:
`make quickcheck PACKAGES=./smoke RUN='^TestInsertDefaultSmoke$' VERBOSE=0`.

### Identity columns — change note

SMALLINT/INTEGER/BIGINT columns admit `[CONSTRAINT name] GENERATED ALWAYS` or
`GENERATED BY DEFAULT AS IDENTITY [(sequence options)]`. Identity implies NOT NULL,
but no uniqueness. CREATE publishes the table and every internal sequence atomically.
Options include START, INCREMENT, MINVALUE/MAXVALUE, CACHE, CYCLE and explicit
SEQUENCE NAME; supplied OWNED BY cannot replace the internal link. Declaration
LOGGED/UNLOGGED selects sequence persistence independently of the ordinary logged
table. Duplicate persistence options conflict. ALTER identity SET LOGGED/UNLOGGED
retains PostgreSQL's XX000 unrecognized-option error; direct ALTER SEQUENCE
persistence and its native control reconstruction remain admitted.

INSERT omission and DEFAULT generate once per consumed row. BY DEFAULT accepts
explicit values; ALWAYS rejects them at analysis with428C9 unless SYSTEM or USER
override is present. `OVERRIDING SYSTEM VALUE` preserves supplied values;
`OVERRIDING USER VALUE` discards only identity inputs after normal type analysis.
Single ordinary VALUES binds discarded inputs and imports inferred parameters,
but does not execute their runtime conversion or volatile producer. Its remaining
supplied/default/identity expressions execute in destination attribute order, even
with reordered INSERT targets. Multirow ordinary VALUES demands every reached
source row, including discarded identity expressions, before that row's identity
projection. SELECT and singleton decorated VALUES prune unused nonvolatile
identity outputs after full analysis/inference/import; volatile outputs and
independent ORDER consumers remain demanded. Decorated multirow VALUES retains
all source-cell demand, including with LIMIT or ordering another column.
Complete destination rows are checked for NOT NULL and CHECK before the next
runtime identity row is demanded. Supplied statement-wide conversion preflight
still precedes executor constraints; only one fully constrained batch publishes.
DEFAULT VALUES does not admit an override clause.
Identity generation updates physical-session currval/lastval using sequence cache
and allocation authority; explicit bypass and empty sources consume no values.
An early constraint failure retains already-demanded sequence effects, never
demands later runtime rows, and cannot be masked by their sequence exhaustion.
Failure-demand smoke: `make quickcheck PACKAGES=./smoke RUN='^TestIdentityFailureDemandSmoke$' VERBOSE=0`.

ALTER COLUMN ADD/DROP IDENTITY, SET GENERATED, SET sequence options and RESTART
compose with admitted nullability/drop-column actions and support transactions/
savepoints. They affect future inserts, never existing data. DROP retains NOT NULL.
Direct internal-sequence DROP fails2BP01 even CASCADE; OWNED BY reassociation fails
0A000. Removing identity NOT NULL or assigning a regular DEFAULT fails42601;
ALWAYS explicit UPDATE fails428C9, even for an empty target.
Repeated SET GENERATED is rejected during utility execution, not parsing;
numeric sequence validation takes precedence. ADD sequence validation precedes
table identity/nullability/default guards, while all proposed changes stay
private on any failure. See [identity ALTER diagnostics and shared regression
coverage](TABLES.md#identity-columns--change-note). SQL GRANT is not added by
this identity ALTER surface; owner-only ALTER uses existing role authority.

TYPE on an original identity column accepts only SMALLINT/INTEGER/BIGINT.
After binding every raw USING against the original tuple, it migrates the exact
linked sequence through AS before the table action passes, even for same-type
TYPE. Nonintegral targets fail22023 before row evaluation; DROP IDENTITY in the
same command cannot bypass that guard. Default type-extreme bounds migrate,
custom options remain subject to the existing sequence bounds checks, and plain
OWNED BY sequences do not migrate. USING and CHECK sequence calls demand the
private AS replacement. On failure/rollback its scalar state is discarded with
the candidate; physical currval/lastval and the caller's AS cache-tail discard
are not undone. Unseen peer cache tails remain untouched until committed AS is
demanded. Identity mode, linked address, privileges and comments remain intact.
Feature smokes: `make quickcheck PACKAGES=./smoke RUN='^TestAlterTypeIdentity(Smoke|ControlsSmoke)$' VERBOSE=0`.

Native controls preserve definitions, actual sequence names/configuration/state and
literal rows without allocating values. Generated expressions, UPDATE DEFAULT,
LIKE INCLUDING IDENTITY, arbitrary table DDL in transactions and full identity
catalog views remain excluded. Logical controls do not imply physical/WAL/wire support.


ALTER TABLE supports one `ALTER [COLUMN] name SET DEFAULT expression` or
`ALTER [COLUMN] name DROP DEFAULT` action. It uses the existing general expression
grammar (unparenthesized NOT/AND/OR and null/truth tests), unlike CREATE's restricted
DEFAULT grammar. Owner-only AX execution changes an immutable definition without
evaluating it or rewriting rows; invalid binding leaves the old default intact.
Subsequent omitted/DEFAULT inserts, including held prepared statements, see the new
definition; DROP twice succeeds and restores implicit NULL without altering NOT NULL.
ALTER semantic default diagnostics have empty Position; syntax and CREATE positions
are unchanged. Qualified/quoted targets, ONLY/ONLY(...) and trailing `*` are accepted
for standalone tables. Inheritance remains rejected: descendant recursion is a
PostgreSQL-only reference contract. Action lists, IF EXISTS for these actions,
UPDATE DEFAULT, unimplemented expression families and explicit-transaction SET/DROP DEFAULT
remain excluded. Selected ADD COLUMN/TYPE/nullability/DROP COLUMN lists support
explicit transactions. Dump serializes current CREATE definitions and explicit rows; Restore's
existing whitelist does not admit ALTER TABLE history.
Feature smoke: `make quickcheck PACKAGES=./smoke RUN='^TestAlterDefaultSmoke$' VERBOSE=0`.

Change note: ADD [COLUMN] [IF NOT EXISTS] admits existing scalar types/typmods/
collations and column NULL/NOT NULL, DEFAULT and CHECK declarations. Owner-only
AX execution publishes schema and widened rows together. A no-default addition
exposes typed NULL in old rows. Supported Immutable/Stable defaults are evaluated
once during ADD, including on empty tables; the admitted fixed-nextval volatile
default is evaluated for each effective live row. Definitions remain immutable
and future omission/DEFAULT uses the original expression. Failed default/NN/CHECK
validation changes no schema/data, but demanded sequence allocations persist.
IF NOT EXISTS skips an ordinary collision before type/default/CHECK binding and
emits42701 NOTICE; system-column names still fail42701. Complete syntax is required.
DROP and TYPE passes precede ADD; generated CHECKs can see later-added columns.
Mixed SET/DROP DEFAULT lists remain unsupported. ADD serial/identity/generated/
unique/FK declarations and inheritance/partition propagation are not admitted.
Stable attributes advance a retained per-relation ceiling across DROP/ADD;
pg_class.relnatts reflects that ceiling. This does not introduce pg_attribute,
physical tombstones, PostgreSQL missing-value tuple storage, wire/disk durability
or a pg_dump process. Native logical Restore may remint live attributes.
Feature smoke: `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceAlterAddColumn(Smoke|ControlsSmoke)$' VERBOSE=0`.

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
excluded. Feature smoke: `make quickcheck PACKAGES=./smoke RUN='^TestCompareOpsSmoke$' VERBOSE=1`;
independent pinned reference: `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceCompareOps' VERBOSE=1`.

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
`make quickcheck PACKAGES=./smoke RUN='^TestBetweenSmoke$' VERBOSE=0`; independent pinned reference:
`make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceBetween' VERBOSE=0`.


UPDATE supports schema-qualified/quoted target relations, AS/bare target aliases,
ordered scalar SET assignments, supported inner/outer FROM trees and optional Boolean WHERE.
SET destinations remain bare physical target columns; qualification applies to
RHS/WHERE and RETURNING. FROM reuses table/view aliases, ON/USING/NATURAL and joins.
Row/subquery assignment, CURRENT OF and DEFAULT remain excluded. Existing scalar
casts/functions and RETURNING are admitted through the same expression binder.
Arithmetic retains its operand/type boundaries, NULL propagation and division/overflow errors, with no new provider.
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
complete statement. Typed parameter input failures carry the actual unnamed
portal parameter Context without a query Position; row SQL deadlines have no native tuple Context,
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
and distinct NO KEY UPDATE/row UPDATE modes use the shared four-mode matrix on
one logical row resource; ordinary readers/different rows do not conflict. Prepare takes RX, no
row locks or data scan. Head vectors and immutable end roots share one all-relation
storage/length/generation/end preflight and frontier install. Cancellation/failure
publishes none and discards both kinds of prior private work, releases holds and
leaves25P02 until end. Compatible COMMENT/ACL revisions preserve genuine rowshape
and storage identity, not descriptor-pointer equality; current authorization still
applies. Dump/ALTER see live committed heads and retained results keep old values.
UPDATE privilege on assigned targets and SELECT on actual RHS/WHERE reads are
checked independently; retained protected columns do not imply SELECT.
Smoke: `make quickcheck PACKAGES=./reference RUN='^TestReferenceUpdateCoreMigratedSmoke' VERBOSE=0`;
quick: `make quickcheck PACKAGES=./reference RUN='^TestReferenceUpdateCore[A-Za-z0-9_]*$' VERBOSE=0`;
reference: `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceUpdateCore[A-Za-z0-9_]*$' VERBOSE=0`.
UPDATE FROM includes target and every expanded source in one coherent statement
view. Stable RowIDs, not values or target/source pair ordinals, suppress successful
duplicate replacements; eligible losing pairs still evaluate SET. RETURNING uses
the same winning source row with actual OLD and constrained NEW. Source-only reads
do not require target SELECT; target/source column dependencies are authorized
under their existing principals. Prepared execution refreshes every source and
result signature. Native eager preflight/atomic-output boundaries are preserved.
Smoke: `make quickcheck PACKAGES=./reference RUN='^TestReferenceUpdateFromSmoke$' VERBOSE=0`;
reference: `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceUpdateFrom' VERBOSE=0`.
Coexistence witnesses: `make quickcheck PACKAGES=./reference RUN='^TestReferenceUpdateCoreDelete[A-Za-z0-9_]*$' VERBOSE=0`;
`make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceUpdateCoreDelete[A-Za-z0-9_]*$' VERBOSE=0`.
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
`make quickcheck PACKAGES=./smoke RUN='^TestLogicalSmoke$' VERBOSE=1`; pinned independent reference:
`make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceLogical.*$' VERBOSE=1`.
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
Feature smoke: `make quickcheck PACKAGES=./smoke RUN='^TestNullCountSmoke$' VERBOSE=1`;
independent reference: `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceNullCount' VERBOSE=1`.

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

Feature smoke: `make quickcheck PACKAGES=./smoke RUN='^TestRandomSmoke$$' VERBOSE=0`.
Dual-mode independent regressions:
`make quickcheck PACKAGES=./reference RUN='^TestReferenceRandom[A-Za-z0-9_]*$' VERBOSE=0`
and `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceRandom[A-Za-z0-9_]*$' VERBOSE=0`.
The same definitions preserve per-backend reseed/range/lattice/demand witnesses;
native controls and PostgreSQL adapter boundaries are explicit phases, not
cross-backend stream parity or a widened DEFAULT/VOID storage surface.

### Core string functions — change note

`btrim(text[,set])`, `ltrim(text[,set])` and `rtrim(text[,set])` remove endpoint
codepoints belonging to a set, not a substring. The omitted set is exactly ASCII
space; empty sets change nothing, and tabs/nonbreaking spaces are not implicit
whitespace. SQL `TRIM` supports BOTH/LEADING/TRAILING, the FROM variants and
ordinary argument-list variants, lowering to these providers with the source
first. There is no ordinary `trim()` function alias.
Endpoint and set-membership scans check cancellation during bounded work intervals
and return a slice of the immutable input without allocating a replacement string.
Shared trim names select TEXT or the existing two-BYTEA signature after analyzing
operands once; a known BYTEA peer infers unknown BYTEA arguments, while all-unknown
arguments prefer TEXT.


`||` resolves the release TEXT/TEXT, TEXT/ANYNONARRAY and ANYNONARRAY/TEXT string
signatures over existing scalars. Successful results are TEXT, typmod-1.
BPCHAR conversion removes trailing ASCII spaces; TEXT/VARCHAR preserve theirs.
BOOL mixed output is `true`/`false`, without changing ordinary boolean export.
BYTEA, numeric widths, OID, NAME, temporal values, UUID, REGCLASS and VOID retain
their type-owned text output. Prepared executions capture current ByteaOutput,
DateStyle, IntervalStyle and TimeZone settings; eager results remain owned.
REGCLASS presentation also uses the captured relation search path: names are
quoted and schema-qualified when needed, while nonexistent identities remain
numeric. Held prepared concatenation observes current execution namespace
visibility without changing its internal OID or retaining a live formatter.
Volatile VOID children execute normally even though their text output is empty.
Internal CHAR preserves PostgreSQL category-aware ambiguity with string/unknown
peers (42725); CHAR/integer is a successful mixed overload with printable octal
General array operators (including NAME[] comparison), `concat()` and new
cast/type families are not added. Existing BYTEA/BYTEA concatenation is preserved;
known BYTEA/unknown selects it, while unknown/unknown prefers TEXT.

`normalize(text[,form])` and `is_normalized(text[,form])` default to NFC.
Unquoted SQL `normalize` requires an NFC/NFD/NFKC/NFKD keyword; quoted or
qualified ordinary calls accept ASCII-case-insensitive TEXT form names without
whitespace trimming or Unicode lookalike folding (for example, Kelvin SIGN is
not ASCII K). `IS [NOT] [form] NORMALIZED` yields BOOL and uses the same
release quickcheck. Resolved signatures are strict: NULL skips the body; malformed
forms otherwise fail22023 without a runtime call Position. Binding/arity/type/
namespace failures occur during analysis, including beneath typed NULL.

`lower(text)`, `upper(text)`, `casefold(text)` and `initcap(text)` return TEXT/-1
and inherit input collation, not VARCHAR/BPCHAR width. Unknown strings, NULLs
and bare parameters infer TEXT; typed numeric/BOOL/BYTEA arguments and wrong
arities fail42883. BPCHAR-to-TEXT removes trailing ASCII spaces; TEXT/VARCHAR
preserve them. NULL bypasses case classification, but not analysis of invalid
children or explicit collation mismatches. NonNULL indeterminate collation
fails42P22 with the COLLATE hint; PostgreSQL18.0 casefold uses `lower()` in that
diagnostic. Namespace and authorization checks remain the ordinary shared rules.

The fixed modes are:

| Collation | Case conversion and initcap words |
|---|---|
| default100 / C950 / POSIX951 | ASCII lower/upper; fold equals lower; ASCII letters/digits form words and every nonASCII byte separates them |
| pg_c_utf8 811 | Unicode16 simple lower/upper and C+S fold; initcap uses simple UPPER then lower; Alphabetic plus ASCII digits form words |
| pg_unicode_fast 6411 | Unicode16 full lower/upper/title and C+F fold; initcap uses TITLE then lower; Alphabetic plus Unicode Nd form words |

Full mappings can expand (`upper('ß')` → `SS`, fold `ﬃ` → `ffi`, lower `İ` →
`i`+U+0307), without clipping to source width. Full lower uses original-input
Final_Sigma, skipping Case_Ignorable on either side. PostgreSQL18.0 also accepts
a nonempty all-ignorable prefix: lower(U+0345 followed by Σ) ends in `ς`, and
initcap produces `Ις`. Bare Σ and a nearest nonignorable uncased predecessor
remain nonfinal; a following nonignorable cased letter also prevents final sigma.
Context comes from the original input, not emitted title expansions.
Folding does not use final sigma and performs no normalization.
Cherokee folding can produce uppercase.
Unconditional SpecialCasing and Final_Sigma apply; tr/az/lt tailoring does not.
Initcap uses alnum runs, not linguistic word segmentation: underscores,
apostrophes and nonAlphabetic marks separate words; a leading digit consumes
the word start rather than capitalizing its next letter. Fullwidth digits are
word members only in the full mode. Scans, including sigma lookahead, check
cancellation during bounded work; unchanged strings are borrowed.

All five collation identities use deterministic UTF8 byte/codepoint ordering.
No CREATE COLLATION, arbitrary libc/ICU locale, nondeterministic comparison or
new native encoding setting is added. Native text remains UTF8. The real
PostgreSQL LATIN1 control rejects nonNULL casefold, including empty text, with
42601; strict NULL bypasses that restriction. Unicode-collation ILIKE is a
documented native0A000 boundary, not silently ASCII matching; plain LIKE works.
Case expressions use existing queries/writes/RETURNING/defaults/CHECKs/views,
stored deparsing and native replay. Parenthesized DEFAULT forms retain current
grammar limits; no arbitrary singleton expression or bare DEFAULT-call admission.
Compact/pretty stored expression and CHECK presentation quotes collation
identifiers only when required: `pg_unicode_fast` remains unquoted, while
`"C"` and `"POSIX"` retain their case-preserving quotes. Returned SQL is executable.
Qualified and unqualified case calls use the existing selected-string scalar
grammar, including its bounded no-FROM exception. Plain LIKE/ILIKE projections
without a selected-string expression still require FROM; activating Unicode
collations does not broaden standalone SELECT admission.

Normalization uses pinned Unicode16 canonical/compatibility data, recursive
decomposition, stable canonical ordering, exclusions, blocking and algorithmic
Hangul. PostgreSQL18.0's inclusive TBASE composition consumes U+11A7 after an LV
syllable: NFC/NFKC of U+AC00 U+11A7 is U+AC00, yet its release quickcheck predicate
is TRUE. This is release compatibility, not a claim that the predicate always
equals normalize(input)==input. The independent official corpus remains unchanged,
with a separate release-boundary regression. Normalization performs no case
folding, locale conversion, stream-safe CGJ insertion or truncation.

These expressions compose in existing queries, writes/RETURNING, grouping,
defaults/checks, views and native Dump/Restore. Bounded selected-string no-FROM
SELECT is supported, not arbitrary scalar singletons. Native text stays UTF8;
there is no server-encoding configuration. The independent LATIN1 PostgreSQL
oracle records nonnull normalization's encoding restriction; strict NULL still
bypasses it. Input UTF8/NUL validation remains at existing textual boundaries.
Stored DEFAULT/CHECK string nodes support all existing compact/pretty deparse
signatures and typed native structural carriers. SQL TRIM, special NORMALIZE
(including omitted versus explicit NFC), and normalized predicates retain their
syntax through Dump/Restore, rename and supplied-relation rendering; ordinary
normalize calls render as quoted function names. Ordinary calls display resolved
non-TEXT character argument conversions, including source, set and form operands;
special syntax hides those implicit conversions. Rendering never installs runtime
coercions or evaluates arguments. Structural output records `String <provider>`
with recursive typed children, not PostgreSQL physical nodes.
Parameter-free Restore INSERTs accept the selected string function family,
LIKE escape normalization and the seven BYTEA hashes documented below;
they recursively reject parameters and unsupported nested producers. Failed
normalization publishes no replacement and preserves held old results.
String-dependent INSERT rows analyze source occurrences before target assignment;
their conflicting parameter diagnostics use existing occurrence coercion. This
does not change the native stringless assignment or settings inference boundary.
The shared concatenation grammar does not admit other selected functions as bare
CREATE DEFAULTs: their existing `0A000`, `functions are unsupported` rejection
remains positioned at the function occurrence, including visibility inquiries.

See [data provenance and regeneration](TYPES.md#unicode16-normalization-data).
Smoke: `make quickcheck PACKAGES=./smoke RUN='^TestStringCoreSmoke$' VERBOSE=0`.

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
| `abs` | INT2 → INT2; INT4 → INT4; INT8 → INT8; FLOAT4 → FLOAT4; FLOAT8 → FLOAT8; NUMERIC → NUMERIC |
| `ceil`, `ceiling`, `floor`, `exp` | NUMERIC → NUMERIC; FLOAT8 → FLOAT8 |
| `div` | NUMERIC,NUMERIC → NUMERIC |
| `factorial` | INT8 → NUMERIC |

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

`abs` selects exact integer/REAL widths rather than widening them. Integer/REAL
inputs to `ceil`/`ceiling`/`floor`/`exp` select FLOAT8; unknown unary inputs to
these functions or `abs` also select FLOAT8. `div` contextualizes both unknown
parameters as NUMERIC; `factorial` contextualizes its parameter as INT8 and
implicitly widens SMALLINT/INTEGER, but rejects NUMERIC/REAL/FLOAT8 (including
`factorial(5.0)`). No parameter's Go value changes the prepared signature.


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
numeric arithmetic operators, new WHERE/DEFAULT/INSERT VALUES
or setting admission, or general SELECT-without-FROM is introduced.
Dump/Restore stores exact results and replays supported view syntax, not private
function state.
Selected calls remain projection/nested-call roots, not new CASE result leaves
or general GROUP BY expressions. Math around an existing CASE with scalar arms
and `round(count(*),1)` are admitted; unsupported `min` aggregates are not added.

Feature smoke: `make quickcheck PACKAGES=./smoke RUN='^TestMathSmoke$' VERBOSE=0`.
Independent reference:
`make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceMath(Signatures|NumericRoundScale|SignModulo|Diagnostics|Consumers)$' VERBOSE=0`.

### Numeric scalar functions — change note

`abs` preserves finite numeric display scale, normalizes floating negative zero
to positive zero, and maps negative infinity to positive infinity. Signed
integer minima fail `22003` with `smallint out of range`, `integer out of range`
or `bigint out of range`. Numeric/floating NaN remains NaN.

Numeric `ceil`/`ceiling`/`floor` return scale-zero integers, using exact decimal
fraction/carry rules; `ceil` and `ceiling` differ only in their SQL name.
For example, `ceil(-42.8)=-42` and `floor(-42.8)=-43`. All preserve NaN and
infinities. Floating operations preserve libm signed zero, including
`ceil('-0.5')=-0`. Decimal carry beyond 131072 integer digits fails `22003`,
`value overflows numeric format`.

`div` is exact truncation toward zero with scale-zero output:
`div(-9.00,4.000)=-2` and `div(9007199254740993,2)=4503599627370496`.
NaN wins even over zero; infinity/infinity returns NaN; infinity divided by a
finite nonzero value returns signed infinity; finite/infinity returns zero.
All other zero divisors fail `22012`, `division by zero`; overflowing finite
quotients fail `22003`, `value overflows numeric format`.

`factorial(0)` and `factorial(1)` are unscaled 1; `factorial(20)` is exact
2432902008176640000. Its maximum input is 32177; larger values fail `22003`,
`value overflows numeric format`. Negative inputs fail `22003`,
`factorial of a negative number is undefined`. Cancellation remains an ordinary
context error and does not publish partial numeric results or DML changes.

Numeric `exp` uses guarded decimal range reduction/series/squaring, not a
binary64 result formatted as decimal. `exp(1.0)=2.7182818284590452`; higher
input display scale retains greater precision. Finite result scale is
`min(1000,max(0,input_scale,16-trunc(clamp(float8(input)*log10(e),-2000,2000))))`.
The float estimate controls precision and PostgreSQL's finite ±6000 guard,
not the decimal coefficients. Inputs whose estimate reaches +6000 fail
`22003`, `value overflows numeric format`; estimates at or below -6000
produce zero at the selected scale. The guard uses represented float estimates,
so sufficiently close high-precision decimal neighbors can hit the same bound.
Numeric NaN/+Infinity persist; -Infinity yields unscaled positive zero.

Floating `exp` preserves NaN/+Infinity and maps -Infinity to positive zero.
Finite inputs producing infinity fail `22003`, `value out of range: overflow`;
those producing zero fail `22003`, `value out of range: underflow`. The pinned
PostgreSQL18.0 environment accepts nonzero subnormals, including `exp('-745')`.
Host floating results remain subject to the PostgreSQL manual's libm caveat.
Provider errors above have no invented Position/Detail/Hint. Prepare fixes types
without evaluating these immutable operations; domain errors belong to Execute.

Scalar feature smoke: `make quickcheck PACKAGES=./smoke RUN='^TestMathScalarSmoke$' VERBOSE=0`.
Independent pinned coverage: `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceMathScalar' VERBOSE=0`.


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
Feature smoke: `make quickcheck PACKAGES=./smoke RUN='^TestCompareNullSmoke$' VERBOSE=1`;
native: `make quickcheck PACKAGES=./reference RUN='^TestReferenceCompareNull.*$' VERBOSE=1`;
real pinned reference: `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceCompareNull.*$' VERBOSE=1`.

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
Grouped predicates also serve as ordinary comparison operands, such as
`(id=1)=TRUE`. Ungrouped comparison chaining, predicate arithmetic and ordinary
logical/comparison INSERT/settings expressions remain excluded.
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
`make quickcheck PACKAGES=./smoke RUN='^TestCastExplicitReviewSmoke$'`.
Smoke: `make quickcheck PACKAGES=./smoke RUN='^TestCastExplicitSmoke$'`; details/matrix in README.
Parameter/ACL smoke: `make quickcheck PACKAGES=./smoke RUN='^TestCastExplicitPreflightSmoke$'`.

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
snapshots. Raw BYTEA operations are `||`, `bit_length`, `octet_length`, `length`,
two-argument `btrim`/`ltrim`/`rtrim`, `overlay(bytea,bytea,int4[,int4])`,
`substring`/`substr(bytea,int4[,int4])`, and `pg_catalog.position(bytea,bytea)`.
SQL OVERLAY PLACING/FROM/FOR, POSITION IN, SUBSTRING FROM/FOR (including reversed
keywords and FOR-only), and TRIM BOTH/LEADING/TRAILING standard and comma forms
are supported. Ordinary POSITION takes haystack then needle and must be quoted
or qualified; unqualified `position(a,b)` is a syntax error. Positions/counts
index bytes, not Unicode. POSITION returns first one-based INT4, zero if absent,
and one for an empty needle. SUBSTRING clips the interval at byte one/end;
negative counts fail22011, while INT4 start+count overflow means through end.
OVERLAY requires positive start (22011), checks start+count overflow (22003),
defaults count to replacement octet length, permits negative counts, and appends
beyond end without padding. Both errors use PostgreSQL English messages and
have no fabricated query location. FOR-only SUBSTRING explicitly casts count to
INT4; ordinary and FROM+FOR calls require implicit INT4 compatibility.
Trim uses endpoint byte membership, not whitespace or Unicode; BYTEA unary
TRIM has no overload. All providers are strict after successful input analysis;
empty results remain nonnull empty. Known BYTEA operands contextualize unknown
byte arguments, with INT4 index/count slots and raw Go binds. Results retain
BYTEA/INT4 metadata even on empty rowsets. Scalar query/write/view/CHECK consumers
and native replay share the providers; DEFAULT functions remain excluded.
TEXT/regex slicing and count siblings and encoding-aware length remain truthful0A000 boundaries.
The excluded `length(bytea,name)` candidate also recognizes unknown source/encoding
arguments and existing implicit TEXT/VARCHAR/BPCHAR-to-NAME encoding conversions.
It is not executed; genuinely absent signatures such as `length(bytea,integer)`
retain 42883 with the PostgreSQL missing-function diagnostic.
CHECK compact/pretty `pg_get_constraintdef` and `pg_get_expr` preserve SQL-origin
OVERLAY/POSITION/SUBSTRING/TRIM syntax; ordinary calls retain identifier quoting
(including `"overlay"`). Concat retains `||` grouping. Native `conbin` emits
typed `Binary <name>` nodes and recursive children without evaluation, not
PostgreSQL nodeToString interchange. Captured carriers survive native replay.
Special BYTEA TRIM nodes also record `syntax TRIM`; ordinary Binary tags are unchanged.
No new typed BYTEA literals, casts, direct byteacat, byteapos alias, binary
functions beyond the selected raw-byte families, arrays or wire server are
added; existing cast admission is unchanged. Binary slicing smoke:
`make quickcheck PACKAGES=./smoke RUN='^TestBinarySliceSmoke$' VERBOSE=0`; pinned independent reference:
`make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceBinarySlice' VERBOSE=0`.
Selected slicing/search fixed scalar signatures accept matching `VARIADIC`
call spelling without adding variadic execution or arrays. Reserved TRIM/OVERLAY
continuation words retain their qualified-name and projection-label legality.
SQL-special spelling is display provenance, not a semantic distinction in
GROUP BY/HAVING or duplicate-label ORDER BY. POSITION searches in constant
space with linear progress and bounded preprocessing/comparison checkpoints.
Smoke: `make quickcheck PACKAGES=./smoke RUN='^TestByteaSmoke$' VERBOSE=0`;
Docker-free native witnesses: `make quickcheck PACKAGES=./reference RUN='^TestReferenceBytea[A-Za-z0-9_]*$' VERBOSE=0`;
pinned independent reference of the same definitions:
`make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceBytea[A-Za-z0-9_]*$' VERBOSE=0`.

### BYTEA checksums and hashes — change note

Exactly seven unary BYTEA signatures are admitted in existing scalar consumers:

| Call | Result |
| --- | --- |
| `crc32(bytea)` | INT8: IEEE CRC32, unsigned range 0..4294967295 |
| `crc32c(bytea)` | INT8: Castagnoli CRC32C, same unsigned range |
| `md5(bytea)` | TEXT: 32 lowercase hexadecimal characters |
| `sha224(bytea)`, `sha256(bytea)` | BYTEA: 28 / 32 raw digest bytes |
| `sha384(bytea)`, `sha512(bytea)` | BYTEA: 48 / 64 raw digest bytes |

Hash input is immutable raw octets: embedded NUL, high bytes and invalid UTF8
octets are valid. There is no string normalization, byteain re-decoding or
output-format hashing. Strict NULL yields typed NULL; empty input yields a
nonnull digest (CRC zero). Unknown CRC/SHA SQL literals, NULL and parameters
infer BYTEA; Go string/[]byte binds are raw bytes. Literal `'\x41'` hashes A,
but a bound `\x41` hashes its four literal bytes. Inference is occurrence-based:
an earlier SHA occurrence can fix a shared parameter as BYTEA for later MD5.

Native MD5 TEXT and unresolved/unknown operands intentionally fail 42883 with
the existing missing-function primary, standard explicit-casts Hint and
character-based function Position. PostgreSQL's independent TEXT overload is
tested, not implemented here. In particular, MD5-first parameter occurrence
selects TEXT on PostgreSQL and can make a later SHA call fail `sha256(text)`;
native instead rejects the initial `md5(unknown)`. No backwards/global inference
pass or Go-kind-based overload selection is introduced.

Quoted lowercase hash names resolve normally. Quoted uppercase/mixed-case names
retain their spelling and fail 42883 rather than selecting a lowercase routine.
For these admitted calls, child-analysis errors precede the 100-argument limit;
101 valid arguments fail 54023 at the function token with no Hint, even when
the quoted spelling would otherwise fail routine lookup. Direct Query and actual
Prepare independently cover all seven names and these diagnostic stages.

Ordinary/pg_catalog calls and matching fixed-signature VARIADIC decoration use
the existing namespace/signature authority; no arrays are added. Wrong
types/arity use 42883; absent explicit schema uses 3F000. Inferred malformed
BYTEA literals retain the existing 22023/22P02 English diagnostics and
character-based quote locations, eagerly at direct Query/Prepare analysis.
Valid raw bytes have no hash-data errors. Runtime cancellation stays owned by
the existing context/adapter boundary, without a fabricated source Position.
Public precanceled tests use DB calls and DB-owned prepared statements;
PostgreSQL's watcher on an already borrowed Conn can race a tiny query.

Computed result names/types and raw int64/string/[]byte representations remain
available on NULL-only/empty results and retained metadata. Native computed
Nullable is unknown. MD5 TEXT Length is MaxInt64 on both adapters, native
CRC/SHA Length is unavailable, and DecimalSize is unavailable for all results.
Fixed digest sizes do not add BYTEA typmods. bytea_output does not change
digest values. Returned SHA buffers never alias stored inputs, retained cursors
or other executions. CRC reads raw octets directly with immutable standard-library
tables and no scratch or copies. Cryptographic hashes use per-evaluation state
and fixed 4096-byte scratch. Both paths check cancellation during input and before
publication. No unsafe aliases, proportional temporary input copy, new dependency
or PostgreSQL/OpenSSL provider-policy emulation is introduced.

Queries, predicates, grouping/aggregates, conditionals, writes and UPDATE RETURNING,
named CHECKs and views reuse the binary carrier and staged publication.
Dump preserves stored raw inputs/digests and structural CHECK/view calls;
Reset/Restore rebinds destination descriptors without evaluating stored rows.
Parameter-free Restore INSERTs admit these seven hash names recursively,
including `sha256('abc')`. Nested parameters remain rejected with 42P02 at the
parameter token. Parameter/input/read/cancellation failures publish no replacement,
even after a valid staged prefix, and retained results stay detached. DEFAULT
functions remain excluded. No md5(TEXT), SHA1, encode/decode, pgcrypto, routine
catalog/OID publication, new cast edges or adjacent function family is implied.

Feature smoke:
`make quickcheck PACKAGES=./reference RUN='^TestReferenceBinaryHashSmoke$' VERBOSE=0`;
pinned counterpart: `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceBinaryHash' VERBOSE=0`;
provider units: `make quickcheck PACKAGES=./internal/sqltype RUN='^TestBinaryHash' VERBOSE=0`.


Integral bitwise operators `& | # ~ << >>` now use a separate SMALLINT/
INTEGER/BIGINT provider in existing general scalar consumers: queries,
predicates, grouped/aggregate expressions, writes, conditional expressions,
defaults and view/native replay. Mixed AND/OR/XOR widen; NOT and shifts
preserve left width. Shift counts require INTEGER. Typed NULL and computed
INT2/INT4/INT8 metadata survive empty results and replay. Unsupported signatures
report42883 and ambiguous unknown signatures42725, with English messages and
operator positions. See [width/count behavior](TYPES.md#integral-bitwise-operators).
Catalog-qualified OPERATOR(pg_catalog.symbol) forms retain these same bitwise
providers and ordinary bounded cast rules; wrong namespaces do not bypass lookup, and
canonical defaults/views preserve explicit qualification.
No BIT/VARBIT provider, ordinary SMALLINT arithmetic or no-FROM widening is added.
Standard SMALLINT CAST/`::` now uses the ordinary existing directional numeric
coercion edges, including standalone and conditional occurrences. Known TEXT
input conversions and `int2()` function-style casts remain excluded.
Smoke: `make quickcheck PACKAGES=./smoke RUN='^TestBitwiseSmoke$' VERBOSE=0`.

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
Composition smoke: `make quickcheck PACKAGES=./smoke RUN='^TestSequenceArithmeticCompositionSmoke$' VERBOSE=1`.
Arithmetic INSERT and parameter-free Restore use existing staged publication;
Dump emits canonical stored values, and failed/canceled replacement preserves
old state and retained computed cursors. No external pg_dump, physical storage
or wire-protocol parity is implied. Smoke: `make quickcheck PACKAGES=./smoke RUN='^TestArithmeticSmoke$'
VERBOSE=1`; reference: `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceArithmetic' VERBOSE=1`.

DELETE arithmetic keeps existing actual-used-column SELECT authorization,
affected-row counts and atomic deletion staging. Qualified arithmetic remains
excluded without changing qualified atom predicates. Mixed arithmetic/DATE
typed-literal projections preserve the existing codecs, not a DATE arithmetic
provider. Smoke: `make quickcheck PACKAGES=./smoke RUN='^TestArithmeticDeleteCompositionSmoke$' VERBOSE=1`.

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
with English byte diagnostics and actual parameter Context on import failures.
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
Smoke: `make quickcheck PACKAGES=./smoke RUN='^TestCharacterSmoke$'`; permanent pinned reference:
`make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceCharacter'`.

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
arbitrary locale COLLATE, binary wire serving and PostgreSQL physical storage are not added.
PostgreSQL binary namerecv rejects64-byte input rather than clipping; native
byte binds do not implement that protocol. See [NAME values](TYPES.md#name).
Feature smoke: `make quickcheck PACKAGES=./smoke RUN='^TestNameSmoke$' VERBOSE=0`;
pinned reference: `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceName' VERBOSE=0`.



UUID declarations use contextual quoted values or binds, not UUID typed literals.
UUID accepts either hex case, matching braces, omitted hyphens and single hyphens
at internal four-hex-digit boundaries; no whitespace/URN prefix/version validation.
Stored values are inline sixteen bytes (OID2950), output is canonical lowercase
8-4-4-4-12, ordering unsigned bytewise, with ordinary strict NULL equality and
ASC NULLS LAST/DESC NULLS FIRST. Native values/ScanType are string/string; pq
uses canonical owned []byte/interface{}. UUID metadata persists for empty/NULL
results; Length/DecimalSize are unavailable. Malformed text is 22P02 with exact
original-input primary; eager literal errors point to the quote in characters,
typed bind input errors have no Position and retain unnamed portal parameter Context.
The existing native Dump/Restore serializes UUID declarations/canonical quoted
values atomically and preserves retained snapshots. Smoke: `make quickcheck PACKAGES=./smoke RUN='^TestUUIDSmoke$'`.
Generators, version/timestamp extractors, arrays/casts/typed UUID literals and
binary wire input remain excluded. PostgreSQL physical disk/network storage,
protocol serving and external pg_dump process/custom archives are non-engine
boundaries, never generic successful no-ops.

SMALLINT/INT2 has signed-16 bounds (-32768 through 32767), native int64 payloads and INT2 result metadata, including NULL/empty results. Quoted lowercase `"int2"` resolves; quoted `"smallint"`/`"INT2"` do not. INTEGER/BIGINT keep their current storage and signed-32/signed-64 bounds. Shared textual input accepts C whitespace, signs, radix prefixes and single separators; malformed/range errors are 22P02/22003 with original value and English smallint/integer/bigint names. Typed narrowing reports `smallint out of range`, unlike input's `value "32768" is out of range for type smallint`. SQL integer inference remains INT4/INT8; cross-width comparisons do not assignment-narrow. Checked imports/coercions precede publication, and staged Dump/Restore preserves all widths and retained cursors. Feature smoke: `make quickcheck PACKAGES=./smoke RUN='^TestSmallintSmoke$' VERBOSE=1`; independent pinned reference: `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceSmallint' VERBOSE=1`.

SELECT execution uses one eager finite source/filter/sort/projection pipeline and common typed row result with identity/settings/SHOW producers; no second binder/catalog/storage path or added syntax. Every admitted target is finalized before statement completion, rejected rows do not evaluate targets, and later evaluation/sort failure exposes no cursor/fragment. Result schema remains available after EOF/error/Close, including empty relations; zero-column rows retain input multiplicity. Direct-column results retain immutable relation-local captured prefixes/indices; computed results retain projected values only. EOF/error release resources automatically; raw cursor errors (including ordinary context cancellation) stay sticky until idempotent Close, after which Next is EOF. Saved exported values survive cleanup and append/Reset/Restore; mutable timestamp-infinity bytes detach per export. WHERE keeps only TRUE; unordered results and sort peers have no portable sequence guarantee. Default ASC NULLS LAST/DESC NULLS FIRST remain without admitting NULLS syntax. Missing ORDER keys preserve source character Position in direct/prepared binding on empty or populated input. Feature smoke: `make quickcheck PACKAGES=./smoke RUN='^TestQueryCoreSmoke$' VERBOSE=1`; independent pinned reference: `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceQueryCore' VERBOSE=1`.

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
remain unchanged; selected standard/postfix datetime casts are supported.
ISO/month-name/compact/ordinal/Julian inputs, BC, epoch, DateStyle ordering and relative
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
transaction-start session-local date. Malformed22007, calendar/range22008 and zone failures
retain English messages, original input and character-positioned literal errors;
typed parameter input errors retain unnamed portal Context, without Position. Comparisons order civil days with
infinities and default NULL order. Dump/Restore quotes canonical values and
preserves snapshots/atomicity. Captured database/sql ColumnType values remain
usable after EOF/Close; database/sql itself rejects new ColumnTypes calls on
closed Rows. Native DATE/TIME comparisons reject42883. DATE/TIMESTAMP
comparisons are supported with the same PostgreSQL semantics, including midnight
equality, civil-day ordering, infinities and SQL NULL propagation.
Selected datetime casts and TimeZone/DateStyle are supported as documented in
[temporal types](TYPES.md#time-zone-aware-values); arithmetic, CURRENT_DATE and ordinary no-FROM SELECT remain excluded. Smoke:
`make quickcheck PACKAGES=./smoke RUN='^TestDateSmoke$' VERBOSE=1`; independent pinned reference:
`make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceDate' VERBOSE=1`.

TIME has OID1083 and canonical string output/ScanType, preserving distinct `24:00:00` (pq instead anchors time.Time to Jan1/Jan2 year0). Precision0–6 uses fractional ties-to-even microseconds then half-up sizing; above6 warns22023/clamps, keyword negative precision is42601, quoted `"time"(-1)` is22023. Colon/compact clocks, AMPM, limited optional validated dates/Julian/T/noise, numeric/abbreviation/full-zone inputs and now/allballs are supported. Zones never shift the written TIME clock; historically varying full names require a date, fixed names do not. Rules are embedded from the pinned image's IANA2025b/backzone data without runtime zone I/O. Constants freeze at analysis; parameters use session-local execution transaction start. TIMETZ, selected casts and session zone settings extend these same providers. Feature smoke: `make quickcheck PACKAGES=./smoke RUN='^TestTimeSmoke$' VERBOSE=1`; real reference: `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceTime.*$' VERBOSE=1`.

TIME ignores recognized `on`/`at` noise, but rejects standalone weekday tokens (`04:05 on Monday` is22007). Unknown full-zone names produce22023 with a lowercased token in the primary diagnostic (`America/Bogus` becomes `america/bogus`); the original input remains intact in malformed-input diagnostics.

TIME colon parsing checks lexical/component bounds first, then processes remaining tokens and validates the optional date, AM/PM and final clock domain in that order; TIMESTAMP retains its early clock-domain check. Typed assignment errors use the actual temporal source identity. Selected temporal casts follow PostgreSQL's implicit/assignment/explicit policies.

NUMERIC is exact immutable decimal, exported as string with fixed-decimal output and retained display scale (typed NULL is nil). Precision 1–1000 and independently signed scale -1000–1000 include negative/wide scale; omitted scale is zero, unconstrained scale is retained. Sizing rounds ties away from zero before precision overflow; NaN fits constrained types, infinities require unconstrained numeric. Ordering is -Infinity < finite < Infinity < NaN; NaN equals NaN, finite equality ignores display scale, and integer/numeric comparison is exact beyond 2^53. Integer assignment rounds/checks numeric; checked numeric/float conversions preserve binary semantics, numeric-to-TEXT uses canonical output. DatabaseTypeName NUMERIC/ScanType string and DecimalSize reflect declarations including negative scale, never values; unconstrained sizing is unknown. Numeric aliases are unquoted only except catalog `"numeric"`.

Input syntax is validated during INSERT/SELECT Prepare at unconstrained typmod; destination sizing is Execute-owned, with overflow Detail and no Position. Literal input failures are character-positioned; typed bind input errors have actual unnamed portal parameter Context without Position. Later expression conversion/sizing failures do not acquire parameter Context. Same-type repeated numeric parameters can size separately per destination. Dump/Restore quotes exact canonical numeric values, preserves scale/specials/NULL/metadata, rejects failed staged replacements atomically and retains old cursor ownership. Format bounds are 131072 integer digits/16383 fractional places; excessive scale rejects rather than truncating to zero. Fraction/exponent SQL tokens do not broaden SET integer syntax or its separate quoted setting codecs. General casts/arithmetic/functions/serial and SELECT without FROM remain excluded; existing SMALLINT participates in exact numeric comparison and checked integer assignment. Feature smoke: `make quickcheck PACKAGES=./smoke RUN='^TestNumericSmoke$' VERBOSE=1`; pinned reference: `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceNumeric.*$' VERBOSE=1`. Native negative-scale DecimalSize is independently asserted rather than matching lib/pq's unsigned-scale adapter defect.

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

Selected ALTER TABLE actions are ALTER [COLUMN] c SET/DROP NOT NULL,
ALTER [COLUMN] c [SET DATA] TYPE type [COLLATE name] [USING expression],
ADD [CONSTRAINT n] NOT NULL c, and DROP CONSTRAINT [IF EXISTS] n [RESTRICT|CASCADE].
They share t/t*/ONLY t/ONLY(t), OwnerOnly AX and metadata-only effect-free Prepare.
Execution captures the admitted command's lock_timeout policy; blocked utilities
fail55P03 without Position or catalog effects, even for semantic no-ops.
Prepare does not resolve or wait for these utility targets. In contrast, a
subsequent absence probe `SELECT * FROM t` is analyzed on direct Exec/Query or
semantic Prepare and returns42P01 at character15 on both native and PostgreSQL;
this SELECT record must not inherit the utility's unpositioned-error policy.
SET/ADD builds the final descriptor before one NULL scan in physical attribute
order. DROP actions run first, TYPE actions next, original SET actions next,
transformed ADD NOT NULL actions last (within each pass, original order). A later action error
precedes validation and installs no descriptor, dependency, generator or row
prefix. Executed IF EXISTS notices remain delivered even if a later action fails.
Validation uses fresh committed roots after AX grant, including under RR, plus
own successful INSERT/UPDATE/DELETE effects; ordinary RR queries stay snapshot
isolated. Existing NULL fails23502 with column/schema/table, no Detail/Constraint.
Pure nullability changes retain the exact storage/backing; DROP COLUMN projects
rows and TYPE may rewrite live rows. INSERT23502 includes
failing-row Detail even for a named constraint. Both identify the decoded column
literally; all failures preserve schema/data. Compatible SET/ADD retains existing
name; DROP makes nullable; redundant operations are count-zero/no-revision.
Missing DROP42704 or IF EXISTS NOTICE00000 uses the relation/name diagnostic.
DROP NOT NULL on ctid/xmin/cmin/xmax/cmax/tableoid fails0A000 with
`cannot alter system column "name"`; ordinary absent columns remain42703.
DROP's default RESTRICT, explicit RESTRICT and CASCADE remove the selected
NOT NULL identity equivalently: supported NOT NULL constraints have no inbound
constraint dependents and own no SQL index. These modes do not implement a
general constraint dependency closure. CREATE TABLE UNIQUE constraints own real
B-tree relations; DROP CONSTRAINT or an included key column removes that backing
relation atomically. PK/FK and referenced-key RESTRICT/CASCADE remain unsupported.
Unrelated NOT NULL identities and sequences owned by the
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
NOT NULL and CHECK enforcement (see below). Inheritance and NOT NULL's
NOT VALID/VALIDATE/NO INHERIT remain explicit native0A000 boundaries with real
PostgreSQL-only witnesses.
Selected nullability/named-NOT-NULL actions and existing DROP COLUMN compose
comma-separated atomic lists, including explicit transactions and savepoints.
Same-session metadata and DML see successful private definitions; rollback
restores descriptor, row shape and own-write roots. Commit publishes these with
sequence metadata/generators and DML through one callback-free installation.
Successive DROP actions see earlier staged view/owned-sequence removals; final
publication still fences the original dependency observations. Dependency
sequence admission preserves callback-safe replacement freedom without changing
ordinary sequence-function/DML reservations. UPDATE binding and staging use the
same private layout, including moved surviving slots and retained handles.
If the first ordinary RR snapshot follows private column projection, raw
committed roots retain authority while corresponding private dense snapshots
retain values/types. Savepoint restoration repairs a missing private projection
against those retained roots without refreshing ordinary RR visibility.
Preparation fences the owning transaction epoch before combining private
descriptors with the catalog/type generation. Provider validation remains
outside engine locks against that immutable generation; replacement is checked
again before installation and reports the ordinary catalog-replaced error.
Successful statement authorization is retained through Commit despite later
role-catalog revisions or internal actor changes, without reauthorizing table
ownership. Autocommit SET/RESET ROLE is supported; transactional ROLE and SESSION
AUTHORIZATION remain excluded. PostgreSQL transactional lifetime is an independent control.
PK and unrelated ALTER actions/transactional DDL remain excluded,
never successful no-ops. CHECK CREATE/enforcement and DROP CONSTRAINT are
supported; ALTER ADD CHECK/VALIDATE remains excluded.
Post-drop Dump/Restore captures nullable declarations, remaining NOT NULL
identities, rows, defaults/comments and owned-sequence state/associations; it
does not replay ALTER history or add ALTER to the Restore whitelist. Physical
server/WAL/crash durability, a wire/server process and external pg_dump/archive
mechanics remain outside the embedded engine.

Native INHERITS failure-only grammar allows one complete parent list on either
CREATE TABLE form; qualified/quoted/multiple parents and IF NOT EXISTS remain
0A000 at INHERITS, never a conditional bypass. Malformed/repeated/extra-tail
input is42601; script prefixes do not execute on these full-input rejections.

Public smoke: `make quickcheck PACKAGES=./smoke RUN='^Test(NotNull(Smoke|ControlsSmoke)|DropNotNullSystemColumnSmoke|DropConstraintOwnedSequenceSmoke)$' VERBOSE=0`.

### CHECK constraints — change note

CREATE TABLE admits ordered column/table `[CONSTRAINT name] CHECK (expression)`
with optional `NO INHERIT`. The complete destination row is bound before
publication, so references to later columns and current table/schema qualifiers
work. Existing scalar expressions, including supported volatile functions, use
the ordinary typed binder/providers. Definition binding and Dump do not invoke
providers. Parameters, aggregates and outside-row references are forbidden.
ctid/xmin/cmin/xmax/cmax report CHECK-specific42P10; tableoid is PostgreSQL's
exception but remains undefined in the native ordinary system-column surface.
Subquery/window/SRF grammar, other CHECK modifiers, inheritance and ALTER
ADD CHECK/VALIDATE are not added.

After defaults/coercions and NOT NULL, the shared completed-row validator admits
TRUE/NULL and rejects FALSE with23514, actual Schema/Table/Constraint and
privilege-filtered physical-order failing-row Detail. All VALUES/default/query
INSERT and simultaneous UPDATE routes stage a whole successful batch before
publication. Sequence/random/session effects retain their existing
nontransactional semantics; row rollback does not rewind them.

Automatic names use distinct referenced attributes (`table_column_check` for
one, `table_check` otherwise), UTF8-safe63-byte clipping and current-command
suffixes. Explicit identities remain relation-local; generated identities avoid
existing namespace constraints. CHECKs enforce in alphabetical name order.
Current-command CHECK duplicates are42710; initial CHECK/NOT NULL name collisions
are pinned18.0's23505 pg_constraint unique-key diagnostic, with the actual newly
allocated relation OID, not a fixed allocator value. NO INHERIT persists and
enforces locally; it does not enable native inheritance.

Dump/Restore retains named typed CHECK definitions, data and NO INHERIT.
Renames preserve names and render current attribute spellings. DROP CONSTRAINT
removes enforcement; DROP COLUMN automatically removes owner-local dependent
CHECKs and remaps surviving dense slots. Plain LIKE does not copy CHECK.
Supported sequence dependencies participate in existing RESTRICT/CASCADE
metadata staging: RESTRICT reports dependent CHECK identities; CASCADE removes
those constraints together with the sequence through the statement's catalog
publication authority. A failed drop leaves definitions and enforcement intact.
CHECK alone never changes known Nullable metadata.

Public smoke: `make quickcheck PACKAGES=./smoke RUN='^TestCheckSmoke$' VERBOSE=0`.
CHECK interaction coverage uses the same untagged `reference/` definitions in
native and enabled PostgreSQL modes; the [complete selector](DEVELOPMENT.md#dual-mode-check-constraint-interactions)
excludes unchanged tagged oracle-only boundaries. No SQL surface is added by
this test migration.

### ALTER COLUMN TYPE — change note

`ALTER TABLE target ALTER [COLUMN] c [SET DATA] TYPE type [COLLATE name]
[USING expression]` shares the existing owner-authorized AX transaction,
catalog, binder and SQL type coercion providers. Without USING, conversion is
assignment coercion, not an arbitrary explicit cast. TEXT to INTEGER therefore
requires `USING c::integer`, even on empty/all-NULL tables. Every USING reads
the old row and may reference other old columns, including a source dropped in
the same list. For each live row, every TYPE expression runs in action order
against that original tuple, then final NOT NULL checks run in physical-column
order and affected CHECKs run on the completed transformed values, before the
next row is converted. An early constraint failure therefore precedes a later
row's conversion failure. No descriptor or row becomes visible until all
binding, conversion and validation succeeds. Invalid input, overflow, typmod,
dependency or cancellation failures preserve the complete previous definition
and data; explicit failures abort the transaction until rollback/savepoint recovery.

Character shrinking uses assignment length checks; an explicit character cast
inside USING truncates by characters. Numeric and temporal modifiers reuse their
existing rounding/range/environment providers. Stored defaults retain immutable,
unevaluated expressions and are independently assignment-coerced: USING never
changes a default. Existing standalone SET/DROP DEFAULT remains a separate
utility, not an action in TYPE lists. Existing named NOT
NULL, stable attribute/relation identities, comments, ownership and ACLs survive.
Dependent views/rules reject TYPE with0A000, even same-type commands; views
reading only unrelated attributes survive. Simple B-tree indexes rebind key usages
and rebuild sorted logical entries from the final candidate row shape atomically;
failed TYPE conversions, rollback and savepoint recovery preserve prior membership.

Surviving affected CHECKs are captured from their original canonical definitions,
reparsed after the entire TYPE/default pass against final destination usages and
DROP-adjusted slots, and atomically published with refreshed provider/attribute
manifests. Identity/same-type USING/collation-only TYPE discovers dependencies too.
Original concrete literals and typed NULLs retain their resolved usages and
values during TYPE capture; comparison constants are not reintroduced as unknown
input that could be inferred as the new column type. Private generated markers
transport immutable literal nodes through the existing CHECK binder, without
public parameters, new explicit-cast providers, or input-environment replay.
Final native CHECK serialization preserves the exact promoted value of retained
finite REAL literals, so Dump/Reset/Restore of a widened DOUBLE PRECISION CHECK
keeps the original comparison threshold. This does not change public casts,
ordinary floating output, or DEFAULT/view serialization.
TYPE traversal follows TYPE action then retained CHECK declaration order,
deduplicating a CHECK touched by multiple columns; stored CHECK order and ordinary
DML enforcement stay alphabetical. FALSE produces `23514`, `check constraint
"NAME" of relation "TABLE" is violated by some row`, with Schema/Table/Constraint
and no failing-row Detail. CHECK reparse operator errors have empty Position,
not the source position of an internally generated CHECK wrapper. Runtime CHECK errors
retain their own SQLSTATE/message. Incompatible defaults precede CHECK reparse;
CHECK reparse precedes any row conversion, including on empty/all-NULL targets.
Native controls retain the final rebound CHECK and NO INHERIT, not TYPE history.
Internal scalar reparsing uses an existing CREATE TABLE/CHECK grammar wrapper
with a quoted owner name, then binds only the extracted CHECK expression to the
completed descriptor. CHECK context preserves selected function calls instead of
misreading them as function-style casts; visibility inquiries use the existing
implicit BIGINT-to-OID provider after a column is widened. The wrapper is parsed,
never executed: it creates no metadata and does not broaden ordinary SQL admission.

Column COLLATE (also in final CREATE declarations for Restore) resolves
`pg_catalog."default"`100, `"C"`950, `"POSIX"`951, `pg_c_utf8`811 and
`pg_unicode_fast`6411 with namespace authority, deterministic UTF8 byte/codepoint
comparison and distinct stored identities. Omission resets to the target type's
intrinsic default. Missing names/noncollatable types fail; arbitrary locale/ICU
providers remain excluded. Scalar annotations use the same fixed identities.
Dump emits final type/typmod/nondefault collation/default/NOT NULL/CHECK declarations,
not ALTER history; Restore retains its CREATE/INSERT whitelist and atomic replay.

Fresh direct/new prepared results expose updated metadata. Held prepared results
whose type/typmod/collation changes fail0A000 `cached plan must not change result
type`; stable result signatures rebind. Acquired eager rows retain old data/schema.
Held prepared INSERT/UPDATE retains its original inferred input parameter types,
then assignment-coerces to the new destination: widening input7 succeeds;
narrowing BIGINT input2147483648 to INTEGER fails22003 without inserting a prefix.
Fresh parameterized preparations infer the new destination type.
TYPE converts only grant-time live committed heads plus surviving own DML, not
invalid ended/deleted/replaced predecessors. Its RR owner reads the captured
live replacement; rollback/savepoints retain original immutable roots without
replaying USING. A peer RR future statement after replacement retains the native
catalog-replaced fence. This differs intentionally from PostgreSQL's physical
rewrite caveat (a pre-rewrite snapshot can subsequently see an empty table).
Physical MVCC rewrite, WAL/crash durability, ANALYZE statistics, pg_dump archives
and a wire/network server are not native logical-control mechanisms.

Smoke: `make quickcheck PACKAGES=./smoke RUN='^TestAlterType(Smoke|ControlsSmoke)$' VERBOSE=0`.
Pinned reference: `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceAlterType' VERBOSE=0`.


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
Stored views preserve stable sources and render current renamed identifiers.
RENAME CONSTRAINT belongs to constraint metadata work; FK/backing-index, row-type,
hierarchy and transactional-DDL effects remain PostgreSQL-only witnesses. Simple
B-tree index names remain unchanged while their stable parent/key identities survive renames.
Physical pg_dump/server disk/network mechanisms are outside native Reset/Dump/Restore. Smoke:
`make quickcheck PACKAGES=./smoke RUN='^TestAlterRenameSmoke$' VERBOSE=1`.

Standalone `ALTER TABLE [IF EXISTS] target SET SCHEMA schema` moves ordinary
tables and all existing column-owned sequences in one indivisible CatalogStage.
Source USAGE and table OwnerOnly authority precede destination resolution/CREATE;
source CREATE and destination USAGE are not required. Same-schema requests still
check CREATE, then leave revisions and sequence plan stamps unchanged. Missing
IF EXISTS sources skip with NOTICE00000 before checking the destination.
In autocommit, Prepare returns no columns/parameters without lookup, locks,
authorization or notices. Prepare inside a native explicit transaction fails
0A000 `statement is unsupported in an explicit transaction`, even in READ ONLY,
and aborts the transaction under the existing utility admission contract.
Direct execution or a utility handle prepared before BEGIN instead applies
read-only execution precedence: READ ONLY fails25006 `cannot execute ALTER TABLE
in a read-only transaction`; READ WRITE retains the explicit utility 0A000 boundary.
Comma action lists are 42601 at the comma and never execute a valid prefix.

Owner-authorized table AX and deterministic owned-sequence AX protect the complete
closure. Collision preflight and final publication fences preserve the entire group
on failure/cancellation, including a later sequence collision (42P07 with destination
schema in the primary). Namespace/revision changes preserve storage, stable column
and NOT NULL identities, owners/ACLs, defaults/comments, sequence options/generators
and every physical-session cache/currval/lastval. Referencing views stay in place,
retain dependencies and render current source namespaces. Held qualified table and
sequence SQL reanalyzes original names. Native unqualified table statements bind
their original names against the current search_path at every Execute. PostgreSQL
can instead reuse a revalidated held plan while its effective search_path is unchanged:
after s1.t moves to s2 and a held statement rebinds there, recreating a same-shaped
s1.t does not alone redirect that PostgreSQL held SELECT. Native held SELECT sees
the new s1 shadow; PostgreSQL held SELECT continues reading s2. Fresh direct and
newly prepared SELECTs see the s1 shadow on both backends. This bounded cache
difference is independently tested, not full PostgreSQL cached-plan parity.
Canonical Dump/Restore uses current qualified persistent definitions/data and does
not replay ALTER history.

`ALTER TABLE view SET SCHEMA` is supported on both backends as an alias of the
view-only namespace move; it does not move the view's sources or dependents.
Native generic sequence SET SCHEMA still fails 0A000 `ALTER TABLE SET SCHEMA
supports ordinary tables only`; PostgreSQL independently supports that move.
Standalone generic index moves remain excluded; table-owned index movement is
part of the supported parent-table move.
Native pg_temp/actual temporary destinations retain 0A000 `temporary schema creation
is unsupported`, and pg_catalog retains 0A000 `system schema creation is unsupported`.
PostgreSQL temp moves fail 0A000 `cannot move objects into or out of temporary schemas`;
a privileged pg_catalog move is independently exercised with rollback. Native
read-write explicit transactions retain 0A000 `statement is unsupported in an
explicit transaction`; read-only execution rejects canonical ALTER TABLE first
with25006. Simple B-tree indexes move atomically with their parent, including
destination name collisions. Constraint backing-index and row-type/array moves
remain PostgreSQL-only controls; native named NOT NULL and owned sequences
are supported. Equal constraint names on unrelated tables
do not collide. Smoke: `make quickcheck PACKAGES=./smoke RUN='^TestAlterSchemaSmoke$' VERBOSE=0`.

DROP TABLE supports grouped schema-qualified/path targets, IF EXISTS and RESTRICT/CASCADE with whole-group atomic preflight. Table owner, schema owner or superuser authority is required. Both modes remove selected tables and AUTO-dependent owned sequences, plus contained metadata/storage/ACLs; neither removes schemas/types/roles/independent LIKE clones. Retained Rows survive removal. Public DROP TABLE remains excluded from private Restore.

ALTER TABLE supports DROP [COLUMN] [IF EXISTS] name [RESTRICT|CASCADE] alone or in selected TYPE/nullability lists, ordinary t/t* and ONLY t/ONLY(t) for standalone tables. Table OwnerOnly (not schema-owner fallback) execution uses AX; metadata-only Prepare does not lock. Survivor attribute/origin IDs remain stable while dense rows/metadata compact. Native dependencies include named NOT NULL/ACL/comments, stored views and AUTO-dependent sequences keyed by stable attribute SubID. The existing transaction descriptor overlay carries projected committed/own rows and dependent view/owned-sequence removals through successful Finish, savepoint recovery and combined Commit. Final installation checks original storage pointer/root/independent length/generation/deletion roots. Index/FK/hierarchy mechanisms remain excluded; unrelated table DDL stays unsupported in explicit transactions. Retained snapshots stay immutable; prepared survivors rebind, changed result shapes retain the cached-type boundary.
The optional ALTER DROP COLUMN keyword consumes the shared dedicated COLUMN token used by COMMENT, not legacy KW_R text matching. Quoted keyword targets remain decoded IDENT names; preserve keyword categories and positioned parser errors when integrating grammar changes.
Canonical populated zero-column Dump uses one public DEFAULT VALUES insertion per row through common Insert. DEFAULT VALUES also applies declared defaults/typed NULL for nonzero-column tables; VALUES() remains invalid syntax. Restore does not accept ALTER TABLE history. Feature smoke: `make quickcheck PACKAGES=./smoke RUN='^TestAlterDropColumn(Smoke|ZeroColumnControlsSmoke)$'`.

Do not widen syntax beyond the selected surface: no nested transactions,
2PC, SERIALIZABLE/SSI, DEFERRABLE safe snapshots, SELECT aliases/qualified columns,
general casts/joins/functions/expressions, primary
keys, UPDATE FROM/row-subqueries/CURRENT OF and DELETE USING/CURRENT OF.
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
Per-mark relation/row lock contributions are restored on rollback/error, retaining
the same ticket and premark modes. Cursor rollback and PL/pgSQL remain excluded.
Modifying utilities outside selected ALTER TYPE/nullability/identity/DROP COLUMN
lists and sequence utilities remain a native 0A000 boundary in RW blocks.
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
execution rejection. Selected ALTER TYPE/nullability/named-NOT-NULL/DROP COLUMN lists
and sequence utilities permit effect-free Prepare and transactional execution;
unrelated utility Prepare/execution retains its native unsupported0A000 boundary.
Feature commands: `make quickcheck PACKAGES=./reference RUN='^TestReferenceTransactionIsolation[A-Za-z0-9_]*$'`,
`make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceTransactionIsolation[A-Za-z0-9_]*$'`,
`make quickcheck PACKAGES=./smoke RUN='^TestTransactionIsolationSmoke$'`.
The migrated SQL witnesses use the same untagged definitions in native and actual
PostgreSQL modes; backend-specific capabilities remain independent phases.
See [transaction/locking test migration](DEVELOPMENT.md#dual-mode-transactions-savepoints-and-locking)
for the 111-original/130-definition inventory and retained tagged controls.

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


Relation locking is transaction-owned AS for ordinary SELECT, ROW SHARE for locking SELECT, RX for INSERT/UPDATE/DELETE, row UPDATE for DELETE and NO KEY UPDATE for keyless UPDATE, SUE for COMMENT Execute, and owner-authorized AX for selected ALTER utilities. Explicit `LOCK [TABLE]` accepts ordered targets with ONLY/star, all eight table modes (default AX) and NOWAIT in an explicit SQL/driver block, including read-only blocks. Documented table-level ANY-rights privileges apply; ordinary views recurse with owner authority, security_invoker with the original invoker. Native table inheritance remains unsupported. The complete matrix and savepoint restoration contract are in [relation locks](TRANSACTIONS.md#relation-locks). Semantic data Prepare acquires automatic table modes and fixes characteristic/RR snapshot timing before waits; LOCK/COMMENT/ALTER Prepare remain metadata-only. Existing fair compatible cohorts, deadlock resolution, absolute deadlines, typed lock_timeout, caller-context identity, exact grant-time relation identity, replacement fences and publication-before-release are unchanged. UPDATE/DELETE preserve frozen candidate identities, RC/RU recheck and RR40001 behavior. Advisory locking/introspection remain excluded. Feature smokes: `make quickcheck PACKAGES=./reference VERBOSE=0 RUN='^TestReferenceLockModes(Matrix|Order|Views|Savepoints)$'`.
Lock-timeout55P03 and deadlock40P01 during direct SELECT/INSERT/UPDATE/DELETE relation analysis or semantic Prepare carry the original ObjectName's first token character Position (schema token for qualified names). Cached Execute, SELECT/UPDATE/DELETE row waits and utility errors have no parse-analysis Position;57014 is excluded from this decoration. Do not put source-position policy into manager resources or fabricate Schema/Table fields.
The canonical manager searches finite stable queue-order constraints to resolve soft cycles without aborting; unavoidable relation/row cycles fail an unspecified actual cycle member with ERROR40P01 `deadlock detected` and Hint `See server log for query details.` Native Detail describes exact logical transaction/session/epoch, mode, relation ObjectRef and optional RowID cycle edges, not physical PostgreSQL PID/XID/ctid identities or tuple Context. Without marks, mandatory Finish discards victim private effects and releases held locks, allowing survivors to proceed before explicit victim ROLLBACK. With recoverable marks it restores the newest mark's effects and held-mode checkpoint, releasing failed-level contributions while preserving ancestor holds. Explicit victims retain the existing25P02 recovery gate. SSI remains unsupported and is not a blocking dependency.
Queued work binds the grant-time descriptor of the exact retained ObjectRef; a same-identity definition revision is valid, while epoch changes/removal/recreation remain fenced. Keep subsequent grant-time descriptor pinning and catalog/storage publication guards. Post-drop quoted expression errors use literal decoded identifiers and character positions. Positive qualified ordinary-owner controls use one ordinary schema/table owner; separate distinct-owner Restore/denial/replay witnesses retain the namespace-USAGE and explicit-ACL Dump boundaries without adding native GRANT.
ALTER re-resolution must compare the complete ObjectRef with scope.lockedRef before owner/column semantics, notices or staging, then use the validated grant-time lockedDescriptor. A search_path shadow created during an AX wait fails ErrCatalogReplaced without effects or reacquisition; PostgreSQL's independently asserted utility behavior instead reacquires the shadow AX. Never reinstate a pre-wait descriptor-revision fence.
DROP COLUMN compaction removes only the dropped stable SubID's column comment and preserves table/survivor comments immutably. Validate comment keys with hasAttribute; Dump must capture attributes and index comment payload by attributes[i], not dense i+1. Restore rebinds canonical named comments to the reconstructed declaration's IDs.
Successful CatalogStage publication advances the shared revision allocator past staged installed revisions without lowering an already-leading frontier; MaxUint64 saturates and later allocations fail ordinarily. DROP keeps old revision + 1, with no early allocator reservation. Commit the candidate frontier with descriptor/storage installation only after final fences, so failed/canceled stages and skips do not consume revision tokens. Later TABLE/COLUMN COMMENT must publish strictly fresh tokens at the retained identity.

CREATE/ALTER/DROP SCHEMA and search_path/current_schema reuse existing catalog/session/role seams. Nested CREATE SCHEMA elements are CREATE TABLE only with atomic publication. Schema provisioning/rename uses effective-superuser database-CREATE baseline; fixed native database label stuntgres is diagnostic-only. Native public retains PUBLIC USAGE|CREATE. Prepared AST re-resolution has a coherent frozen signature guard, not full PostgreSQL cached-plan caveat parity. Absent pg_temp lookup never allocates; pending creation fails 0A000.

Search-path namespace selection has one kind-aware authority. Explicit entries
retain decoded order and deduplicate by namespace identity; missing, empty,
no-USAGE and foreign TEMP entries are ignored. `$user` expands CURRENT_USER.
This native foreign-TEMP exclusion differs from PostgreSQL18: an explicitly
named peer temporary namespace is searchable there when schema USAGE permits
it, so its relation can be `pg_table_is_visible` even from a different session.
An absent explicit pg_catalog is inserted before the explicit path; initialized
own TEMP is inserted before catalog for relation/type lookup only. Explicit
catalog/TEMP keep their stated placement. Unqualified function/operator lookup
excludes TEMP even when explicitly listed; qualified lookup restricts candidates
to its validated namespace, never falls back by familiar spelling.
Qualified non-TEMP schema USAGE denials preserve the namespace token's position;
COMMENT diagnostics retain their existing unpositioned utility policy.
Supported builtin type identifiers, null-count/sequence/settings functions,
random/random_normal/setseed, current_schema(), type-input functions, count(*) and arithmetic/comparison
operators accept catalog qualification in their existing expression contexts.
The min_scale/round/scale/trim_scale/trunc/sign/mod and
abs/ceil/ceiling/div/exp/factorial owners also accept catalog qualification
through this resolver, without allowing familiar spellings in a wrong namespace
to bypass lookup.
Qualified type names use catalog identifiers, not SQL aliases:
pg_catalog.int4 is valid, pg_catalog.integer is not an alias. Qualified operators
use OPERATOR(schema.symbol) and generic operator precedence.
Qualified temporal declarations such as pg_catalog.time(3) and
pg_catalog.timestamp(3) preserve precision and canonical TIME(3)/TIMESTAMP(3)
Dump/Restore declarations. Bounded temporal explicit casts and same-type precision
annotations work qualified or unqualified, without adding conversion providers.

The scalar function owner exposes stable, strict current_schemas(boolean).
FALSE returns the explicit searchable path; TRUE includes implicit namespaces.
Prepared BOOL parameters observe fresh settings/current-role state at execution;
returned values are immutable snapshots. NULL returns typed NULL; an empty
explicit path returns `{}`. Stored views containing current_schemas likewise
read the executing session's runtime path, including through held prepared
queries, rather than the view's creation-time path.
NAME[] has OID1003, catalog name _name, public
DatabaseTypeName `_NAME`, typmod-1, no origin and NAME-element collation950.
Native ScanType/export are string; PostgreSQL/libpq reports interface{} and
raw []byte instead. The selected bounded domain-base path adds textual array
input/storage/casts, not general array operations. Output quotes and escapes names.
NAME[] may be projected alongside scalar ordering, but native ORDER on NAME[]
itself returns positioned42883 at analysis, even for empty/all-NULL sources.
PostgreSQL supports array ordering; that external comparator is not native parity
or a reason to sort the encoded array as TEXT.
Native current_schema/current_schemas omit absent TEMP without materializing it;
PostgreSQL current_schemas may materialize a pending leading pg_temp. Once a
supported TEMP sequence creates the namespace, normal ordering/dedup applies.
SQL routine/type/operator DDL and general overload resolution remain excluded.
Native Dump/Restore preserves qualified persistent expression spelling and
objects/data, not session search_path; it is not external pg_dump, wire protocol,
server configuration or filesystem durability.
Feature smoke: `make quickcheck PACKAGES=./smoke RUN='^TestSearchPathSmoke$' VERBOSE=0`.

### Search-path visibility inquiries — change note

The four PostgreSQL18 inquiry names each have one STABLE/STRICT `(oid) → boolean`
signature: `pg_table_is_visible`, `pg_type_is_visible`,
`pg_collation_is_visible`, `pg_conversion_is_visible`. There is no function
overload visibility inquiry or SQL routine DDL in this change. They use the
existing typed expression path, including OID parameter inference, BOOL result
metadata and positioned signature/input diagnostics.
Children are transformed once before the more-than-100-argument guard (54023),
then explicit namespace/signature lookup and OID inference/coercion. A missing
child column gives positioned 42703 even when the explicit schema is absent or
the call has 101 arguments. With 101 valid children, 54023 precedes absent-schema
3F000; with one valid child, the absent schema still gives positioned 3F000.
STRICT NULL folding occurs only after this analysis and resolution.

NULL input or an OID missing from the queried object class returns BOOL NULL.
An existing object is visible exactly when its catalog identity is the first
eligible same-name identity in the effective path; shadowed/off-path objects
are false, not NULL. Relation kinds share a namespace. Types use catalog names,
including result-only/pseudo descriptors, not SQL aliases or storage admission.
No subject table SELECT/type USAGE/conversion-procedure EXECUTE check is made.
Schema USAGE still controls effective-path membership.

Relations/types include own TEMP; collations/conversions exclude it even if
explicitly listed. Missing TEMP is never allocated. An Execute whose bound
query, nested view, RETURNING or CHECK requires visibility captures one
immutable private-aware catalog at its existing statement frontier. The explicit
path is resolved once, with separate relation and no-TEMP paths. Unrelated
statements retain no visibility capture. Nested demand is derived from current
identity-bound view definitions, not cached on dependent views: replacement can
add or remove an inquiry without rebinding dependents or forcing capture after
removal. Held prepared SQL refreshes authority, not Prepare-time decisions.
Stored views remain execution-path sensitive;
CHECKs, including transformed ALTER COLUMN TYPE validation, share statement
authority. Stored inquiry CHECKs support compact/pretty pg_get_constraintdef
and pg_get_expr rendering and replay; native conbin is structural data, not
PostgreSQL nodeToString. DEFAULT admission still rejects these inquiries.

All25 native type descriptors, seven fixed collations (100,950,951,962,963,811,6411)
and all128 fixed conversions (4402–4449,4452–4531) have real inquiry metadata.
Collation lookup is namespace-first, then UTF8 encoding6 before encoding-1
fallback inside each namespace. A wrong-encoding existing collation is false.
An earlier namespace fallback beats a later exact encoding. Conversion lookup
uses name/namespace, not encoding pair/default/procedure privilege:4402 is visible
despite having no UTF8 side. Metadata does not enable conversion execution,
general locale comparison, imported OS collations or broader catalogs/DDL.
Comparison supports default/C/POSIX and pg_c_utf8/pg_unicode_fast. Ordinary B-tree indexes participate in
relation visibility, including same-name cross-kind shadowing, and their
definitions roundtrip through native Dump/Restore. CREATE TYPE/COLLATION/
CONVERSION, automatic row types, materialized/foreign tables and TEMP table
creation retain their native exclusion boundaries; real PostgreSQL-only
reference tests exercise those identities rather than fake native support.

Fixed identities rebuild on New/Reset/Restore without separate serialization.
Supported user objects/views/CHECK definitions roundtrip through existing SQL;
restored generated OIDs must be rediscovered. Session search_path is not dumped.
Feature smoke: `make quickcheck PACKAGES=./smoke RUN='^TestVisibilityInquirySmoke$' VERBOSE=0`.


COMMENT supports TABLE/COLUMN/SCHEMA/ROLE only, with decoded literals and NULL/empty removal (whitespace persists). Store text in immutable descriptors, column maps keyed by stable SubID, or shared RoleDescriptor values; never name-keyed side storage or adjacent SQL description functions. Lookup and live ownership precede mutation; COLUMN nonowner says relation, TABLE says table. Ordinary roles cannot self-comment; CREATEROLE/ADMIN expansion is excluded. Explicit-TX COMMENT remains native 0A000. Rename/owner transfer retains text; drop/recreate clears it; LIKE does not copy it. Database Dump excludes shared role comments, Reset/Restore retain them.

DSNs are exact opaque process-local names, including empty string, scoped to a Driver. Different strings/Drivers isolate data. Closing pools/connections does not erase an engine. Reset atomically clears objects retaining process-local identity. Dump serializes persistent user schemas before qualified tables/views in common creation-address order among objects whose sources are ready, with owner directives as needed and table data/comments beside CREATE; unsupported explicit ACLs fail before output. Incumbent view topology takes precedence over exceptional remapped source addresses. Restore stages and atomically replaces, preserving independently mapped namespace/table owners through private stage-only destination authority, never fake grants. Controls require native pools, reject wrapped/foreign drivers and nil inputs, and preserve data on errors. Snapshots survive controls and schema lifecycle changes. Never hold target locks during external I/O.

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

Feature smoke: `make quickcheck PACKAGES=./smoke RUN='^TestControlTransactionsSmoke$' VERBOSE=1`.
Native fixture replacement has no PostgreSQL wire-command oracle; reference
tests independently assert READ COMMITTED/private visibility, English SQL
diagnostics and same-epoch failed-block recovery on the pinned real server.
Restore's existing private whitelist is unchanged; transaction-control scripts,
live settings and shared role effects are not authorized by these witnesses.

Trusted Restore INSERT and database-local COMMENT use local validated captured-bootstrap AuthorizationViews, never session identity mutation. COMMENT authority covers qualified lookup, lock resolution and metadata mutation, is restricted to local targets and is cleared after each statement. Named CREATE owner groups remain intact; per-statement private markers cannot affect public Prepare/INSERT/COMMENT or persist schema/table ACL grants.

Restore's initialized private catalog accepts CREATE/ALTER/DROP SCHEMA, CREATE TABLE with supported defaults/identity, CREATE SEQUENCE, INSERT with supported parameter-free scalar/default-request forms (including integer arithmetic and SYSTEM identity override), database-local COMMENT, generated sequence OWNED BY associations, numeric/persistence configuration of internally owned identity sequences, SELECT setval(name,value,is_called), and named SET SESSION AUTHORIZATION only. Nested parameters remain rejected recursively. Dump reconstructs bootstrap identity/name/owner changes before user objects; retained canonical builtin ACLs transform owner grantor/grantee, recreated public has absent ACL, and unchanged bootstrap retains legacy bytes. Local schema comments follow schema creation, table/column comments follow CREATE before INSERT under owner groups. Arbitrary explicit ACL serialization stays unsupported. Held INSERT rebinding INT4→TEXT rejects native 0A000 before importer/write; pinned PostgreSQL retains INT4 parameters and assignment-coerces to TEXT, an explicitly asserted difference.

The parameter-free scalar inventory includes the selected string functions,
LIKE escape normalization and `crc32`, `crc32c`, `md5(bytea)`, `sha224`,
`sha256`, `sha384`, `sha512`. Hash operands use normal signature/inference
validation, recursively reject parameters and preserve Restore atomicity;
this does not admit arbitrary functions, query INSERT sources or hash DEFAULTs.


SERIAL native controls use the same shared CREATE/default binder, including
canonical `nextval(CAST('qualified_sequence' AS regclass))` replay. Dump retains
actual integer widths, renamed sequence identities/options/state, AUTO ownership
and independent default dependencies without allocating. Direct sequence
RESTRICT blocks its dependent default; CASCADE removes the default only.
Feature smoke: `make quickcheck PACKAGES=./smoke RUN='^TestSerialSmoke$' VERBOSE=0`.


## Sequences

Sequence objects support the PostgreSQL18 CREATE/ALTER/DROP option and administrative forms documented in the [SQL reference](SQL.md#sequences), including TEMP/UNLOGGED, optional BY/WITH, CREATE/ALTER RESTART, NO MINVALUE/MAXVALUE/CYCLE, OWNED BY, IF [NOT] EXISTS and grouped DROP. RESTART sets the current uncalled value, not configured START: CREATE START 5 RESTART 9 returns 9 first, and a later bare ALTER RESTART returns 5. Numeric options are integral and bounded by the declared smallint/integer/bigint type; function results always use INT8. Constant-space reservation arithmetic handles large CACHE and int64 extremes without allocating cached arrays. CYCLE wraps on reservation boundaries and can repeat values; it is not a uniqueness constraint.

Sequence SQL interactions now use the untagged mode-aware external reference
definitions. Select `^TestReferenceSequence[A-Za-z0-9_]*$` with
`make quickcheck PACKAGES=./reference` for native and `make quickcheck REFERENCE=1 PACKAGES=./reference` for native plus
the pinned PostgreSQL18.0 oracle. Native snapshots/control replacement and server
cache/namespace/queue witnesses have independently specified backend phases;
the migration changes neither sequence semantics nor native exclusions.

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
Smoke: `make quickcheck PACKAGES=./smoke RUN='^TestSequenceTempMoveSmoke$' VERBOSE=1`.

CREATE SEQUENCE resolves explicit actual namespace identities before CREATE and
requested-persistence checks. TEMP accepts its caller-owned temporary namespace;
ordinary logged creation there becomes temporary. TEMP in a persistent namespace
fails `42P16` / `cannot create temporary relation in non-temporary schema`;
UNLOGGED in any temporary namespace fails `42P16` /
`only temporary relations may be created in temporary schemas`. Foreign TEMP
creation retains the `42P16` ownership record above. Missing/denied namespaces
retain `3F000`/`42501` precedence, including IF NOT EXISTS; failures publish no
private namespace or sequence state.
Smoke: `make quickcheck PACKAGES=./smoke RUN='^TestSequenceTempCreationSmoke$' VERBOSE=1`.

Temporary namespaces remain sequence-only. Ordinary CREATE TABLE rejects actual
own/peer temporary namespaces and an initialized `search_path=pg_temp` with
`0A000` / `temporary schema creation is unsupported`, before IF NOT EXISTS or
descriptor/storage publication. This ordinary-table guard does not change the
supported sequence creation rules above. Actual namespace/path errors have no
Position; the existing explicit `pg_temp` creation alias retains Position14.
Smoke: `make quickcheck PACKAGES=./smoke RUN='^TestSequenceTemporaryTableBoundarySmoke$' VERBOSE=1`.

The four selected sequence functions and minimal regclass casts reuse the common typed scalar binder/eager evaluator. Literal regclass lookup happens at Prepare with character positions. Nonnull regclass parameters become canonical OIDs during coherent Execute parameter import, before zero-row evaluation, runtime NULL checks or nested function effects; input errors retain portal Context without a literal Position. Numeric/dash inputs require no existing relation. NULL arguments are strict and do not open a sequence. Exact session-before-use, missing/wrong-kind, option, bound, ACL and NOTICE diagnostics have independent pinned-release fixtures. This does not admit arbitrary relationless SELECT, user-defined functions or general casts.

Regclass input follows PostgreSQL qualified-name splitting, separately from the
SQL statement lexer. Unquoted punctuation and comment-looking bytes remain part
of the name; only dots and six ASCII whitespace bytes delimit components.
Quoted components preserve case/embedded dots and decode doubled quotes; names
fold ASCII and clip at 63 UTF8-safe bytes. Only exact nonempty ASCII digit strings
or exact `-` select the opening-free OID path. Padded digit/dash strings instead
resolve symbolic names, and malformed complete input rejects before effects.
Smoke: `make quickcheck PACKAGES=./smoke RUN='^TestSequenceRegclassInputSmoke$' VERBOSE=1`.

Both `value::regclass` and `CAST(value AS regclass)` are admitted through the shared cast syntax alongside the existing bounded explicit-cast matrix above; unsupported source/target pairs retain that matrix's diagnostics. Regclass (OID2205) is a sequence expression/result identity, not an admitted stored column type. Valid sequence CREATE prepares without namespace permission checks. At execution, schema CREATE denial has no Position; ordinary CREATE validates options first, while IF NOT EXISTS checks namespace authority before skipping an existing relation or validating options. Literal regclass schema USAGE denial remains an analysis/Prepare error positioned at the input literal.

Standalone selected regclass projections use the `regclass` column label and
PostgreSQL name output: visible names are unqualified, shadowed/off-path names
are schema-qualified, and identifier quoting uses the shared keyword inventory.
Missing nonzero OIDs display decimal text; zero displays `-`. The common result
exporter uses an immutable execution output snapshot without replacing internal
typed OID datums or adding general OID casts/catalog queries.

Sequence metadata, transactional generator replacements and INSERT/UPDATE/DELETE row versions publish together at Finish/COMMIT through the same immutable prepare/validate/install frontier. Ordinary nextval/setval effects and physical-session memories are outside rollback. RX protects demanded nonnull function calls; ALTER uses SRX and administrative rename/schema changes use AX. Sequence row reads use AS and retain captured data/types through subsequent changes. Native log_cnt0 intentionally differs from PostgreSQL WAL prelogging counters; tests assert both independently.

Sequence DDL, COMMENT ON SEQUENCE and the selected ALTER TABLE action lists above are bounded exceptions to the general explicit-transaction DDL exclusion. TEMP sequence namespaces are session-private, shadow persistent relations, and survive persistent Reset/Restore until physical end. Controls preserve settings/roles/temp memories, exclude TEMP objects from Dump, serialize effective persistent options and called state, and reject invalid staged replacement atomically.

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
`make quickcheck PACKAGES=./smoke RUN='^TestSequenceDeleteCompositionSmoke$' VERBOSE=1` (also a
`TestSequenceSmoke` subtest).

## Timezone-aware temporal extension

TIMESTAMPTZ stores instants; TIMETZ stores clock plus fixed offset.
AT TIME ZONE/AT LOCAL and selected datetime casts/comparisons reuse the scalar
binder and captured session environment. TimeZone/DateStyle use existing
SET/SHOW/RESET/current_setting/set_config and transaction/savepoint lifecycle.
AT TIME ZONE is strict: a NULL temporal operand or NULL zone produces a typed
NULL result. AT LOCAL likewise preserves the result type for a NULL operand.
An unreachable CASE branch does not evaluate its zone conversion.
IANA rules, historical abbreviations and POSIX/fixed offsets are deterministic
embedded pinned-image data, not host lookups. DST gaps/folds follow PostgreSQL's
accepted transition-side preference. See [types](TYPES.md#time-zone-aware-values)
and [settings](SETTINGS.md#temporal-settings) for signs, precision, adapter
representation and DateStyle ordering. Dump/Restore remains native logical replay.
Standard/postfix temporal identity casts and temporal-to-TEXT output casts are
supported in existing scalar positions, including direct and prepared queries.
For example, `TIMESTAMP 'epoch'::timestamp` preserves the timestamp value and
`CAST(TIMESTAMP 'epoch' AS text)` renders `1970-01-01 00:00:00` under ISO DateStyle.
This does not expand unrelated cast families: integer-to-VARCHAR remains excluded.
Timezone name/abbreviation views, INTERVAL arithmetic, timezone(...) aliases and
CURRENT_* producer-family expansion are excluded. The timezone-only INTERVAL
operand of SET TIME ZONE remains bounded independently of the general
[interval value type](#interval-values--change-note).
Feature smoke: `make quickcheck PACKAGES=./smoke RUN='^TestTimezoneSmoke$' VERBOSE=0`.


## Session and database identity — change note

The scalar pipeline supports bare `current_catalog`, `current_role`,
`current_user`, `session_user`, `user`, `system_user` and ordinary
`current_database()`. The first six identities excluding system_user return NAME;
system_user returns TEXT SQL NULL for trusted embedded sessions. Keyword labels
retain their lowercase spellings. The real catalog functions are current_database,
current_user, session_user and system_user: qualified/quoted calls use namespace
lookup, while unqualified keyword parentheses are syntax errors. There are no
ordinary current_catalog/current_role/user function aliases.
NAME has no arithmetic overloads: identity or NAME-column arithmetic reports
`42883` with the missing operator signature, operand types, operator position
and PostgreSQL cast hint, rather than a native unsupported-provider diagnostic.
Identity expressions follow the existing cast boundary: TEXT `system_user` can
participate in supported conditionals and explicit TEXT casts, but explicit
NAME-to-TEXT casts are not admitted (`0A000`). NAME identity assignment to TEXT
columns and TEXT defaults remains supported; this feature adds no cast families.

Identity-derived singleton comparisons/casts/conditionals are admitted without
FROM; unrelated general singleton expressions remain outside the bounded dialect.
Table projections/predicates, assignments, defaults, RETURNING and views reuse
the typed scalar path. Plans store syntax, not identity values: every execution
captures effective/session/database identity, and retained results remain detached.
Native Dump/Restore preserves expression syntax and stored data, not session
identity, authentication or database naming.
Supported quoted identity calls in views are dumped as canonical
`pg_catalog` calls so replay never turns them into forbidden bare keyword calls.
Ordinary identity-call arguments are analyzed before namespace lookup, preserving
undefined-child diagnostics even when the callee schema is missing or denied.

Database naming is the exact opaque connector DSN converted through NAME input:
empty remains empty, whitespace and URL-looking bytes are literal, invalid UTF8
fails, and valid names silently clip at a complete-codepoint 63-byte prefix.
The exact untruncated DSN still keys engine isolation. No database registry or
credential/URL parsing is added.

Autocommit SET [SESSION] ROLE accepts identifiers/strings, NONE, TO/= assignment,
and RESET ROLE. Only CurrentUser changes. Eligibility uses SessionUser self,
superuser or cycle-safe all-SET membership reachability; ADMIN/INHERIT alone do
not authorize. Quoted/string lowercase `none` is the sentinel; uppercase `NONE`
is a real role name. Bare DEFAULT is a syntax error, TO/= DEFAULT resets, and
quoted/string `default` is a real name. SET LOCAL ROLE and explicit-transaction
selection/reset fail native `0A000` without mutation, unlike PostgreSQL.
SET SESSION AUTHORIZATION still uses the original authenticator and resets both
mutable identities. SQL membership administration/authentication remains excluded.

Feature smoke: `go test -count=1 -run '^TestSessionIdentitySmoke$' ./smoke`.
Pinned reference: `go test -tags reference -count=1 -run '^TestReferenceSessionIdentity.*$' ./reference`.
The normal oracle authenticates with SCRAM and independently reports its method;
a dedicated real trust connection demonstrates system_user NULL.
The trust witness also runs independently under lazy fixture startup:
`go test -tags reference -count=1 -run '^TestReferenceSessionIdentityTrust$' ./reference`.
Identity cancellation regression entrypoints include
`TestSessionIdentityCanceledRolePublication`,
`TestSessionIdentityCanceledDefaultWriteFinish`, and
`TestSessionIdentityFailedAndCanceledControlsPreserveState`; these cover the
role publication fence, staged default writes, and failed/canceled controls
without publishing replacement data or changing retained results.


## Ordinary B-tree indexes — change note

Native CREATE INDEX supports default/USING btree over 1–32 simple keys, including
multikey and repeated columns, ASC/DESC and NULLS FIRST/LAST. It builds actual
sorted live-row identity/key vectors and maintains them through common row and
catalog publication. It is not metadata-only success. Scan/filter/sort remains
the query semantic authority; no optimizer, index-only scan, disk algorithm or
unordered-result ordering promise is added.

Ordinary build uses Share, checks owner before waiting, accepts compatible
same-incarnation grant-time revisions and revalidates namespace/role authority.
Existing replacement/epoch and utility lock/cancellation boundaries apply.
The native exact-incarnation utility contract retains its documented
search_path-shadow distinction; no replacement is reacquired by name.
CREATE INDEX is excluded inside native writable explicit transactions (0A000),
whereas PostgreSQL ordinary transactional CREATE succeeds and rolls back.
Read-only execution is independently 25006 before target lookup.

Duplicate names are 42P07 in the shared relation namespace; missing target/key
are 42P01/42703. PostgreSQL18.0 missing simple-key diagnostics have no Position,
unlike newer development-source behavior. Binding precedes duplicate-name
checking, including IF NOT EXISTS. Semantic prepared errors occur at Execute;
syntax diagnostics retain one-based character positions. Non-btree/expression/
partial/concurrent/unique definitions and custom index decorations are bounded
capability exclusions, not ignored clauses. No DROP/ALTER INDEX or broad index
inquiry catalog family is added.

Dump writes canonical real definitions/current names/options after parent data;
Reset removes persistent indexes and Restore privately rebuilds destination
membership. Supported table lifecycle commands preserve or remove indices
atomically. Smokes:
`make quickcheck PACKAGES=./reference RUN='^TestReferenceBtreeIndex(Regression|Controls|Lifecycle)$'`;
the same RUN with `REFERENCE=1` includes the independent pinned PostgreSQL oracle.
The independent reference family is `TestReferenceBtreeIndex...`.
The consolidated source/reference union keeps actual plain maintenance and
transaction consumers beside recorded Context owners. Standalone CREATE UNIQUE
INDEX still fails with native0A000/Position8/nil publications and unchanged
catalog/data/Dump; independent PostgreSQL supported direct/held witnesses do
not turn that native exclusion into a success.


## LIKE and ILIKE — change note

LIKE, NOT LIKE, ILIKE and NOT ILIKE are whole-string predicates. Their operator
spellings are `~~`, `!~~`, `~~*` and `!~~*`, also available through
`OPERATOR(pg_catalog.symbol)` and the existing namespace/authorization rules.
Keyword predicates accept `ESCAPE`; symbolic predicates do not accept that suffix.
`%` consumes zero or more characters and `_` one UTF8 code point, including
newlines. Regex punctuation has no special meaning. No Unicode normalization
or grapheme counting is performed.

Backslash is the default escape. ESCAPE is empty (disable escaping) or one TEXT
code point; custom escapes quote the following character, including themselves.
Escape normalization precedes ASCII-only ILIKE folding under admitted
default/C/POSIX collations. Thus É/é do not match case-insensitively. C and POSIX
are distinct identities: conflicting implicit nondefault operands, including an
escape operand, raise 42P22 when matching non-NULL values. Implicit default yields
to a nondefault identity. Scalar COLLATE also admits pg_c_utf8/pg_unicode_fast;
an explicit annotation, including default, wins over implicit conflicts.
Equal explicit identities combine; different explicit identities fail analysis
and Prepare with 42P21 at the second conflicting COLLATE, even for NULL or empty
input. Collatable casts, conditional results and supported string outputs retain
this expression authority. Stored view columns expose implicit physical identity.
Other locale/nondeterministic providers remain excluded.
Plain LIKE supports both Unicode identities. ILIKE/NOT ILIKE (`~~*`/`!~~*`)
with effective pg_c_utf8 or pg_unicode_fast collation fails native0A000,
`ILIKE is unsupported for this collation`, positioned at the operator during
analysis/Prepare. The guard uses the combined collation, including the RHS and
escape wrapper, after explicit-conflict resolution. Explicit C on either side
overrides an implicit Unicode column and allows ASCII ILIKE; existing42P21 and
indeterminate42P22 diagnostics remain authoritative.

BPCHAR matching preserves the left operand's physical trailing spaces; BPCHAR
patterns/escapes convert to TEXT and trim those spaces. VARCHAR retains them.
Existing BYTEA LIKE/NOT LIKE uses octets and an empty or one-octet escape; BYTEA
ILIKE has no signature (42883). No binary producers or casts are added.
Negation complements successful booleans only; NULL inputs produce BOOL NULL.

Invalid escape length is 22025 `invalid escape string`, with Hint
`Escape string must be empty or one character.`. A demanded trailing escape
is 22025 `LIKE pattern must not end with escape character`; mismatches or
exhaustion can return false without reaching it. Runtime errors have no query
Position. Incompatible operators/escape argument types fail analysis/Prepare
with 42883 at LIKE/ILIKE or NOT, using canonical symbols/function signatures.
Prepare does not perform matching. Execute preflights immutable row-independent
normalization, even on empty relations or a constant NULL lhs. Row-dependent
normalization is not run on empty/filtered input. A variable NULL lhs still
evaluates the escape child; a constant NULL lhs can eliminate row-dependent
matching after independent normalization preflight. Held executions never retain
normalized parameter values.

Matching participates in shared CHECK/view expression consumers, deparse/rebind
and atomic public Dump/Reset/Restore. Regex/SIMILAR TO, additional locales and
general no-FROM scalar SELECT are not included.
Feature smoke: `make quickcheck PACKAGES=./smoke RUN='^TestLikeSmoke$' VERBOSE=0`.

