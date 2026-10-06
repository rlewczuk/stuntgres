# SQL dialect

Supported SQL surface, native limits and intentional PostgreSQL differences. See
[README](../README.md) for the consumer contract and [AGENTS.md](../AGENTS.md) for
development instructions.

## Supported surface and limits

CREATE TABLE supports unqualified or schema-qualified names with SMALLINT/INT2, INTEGER/INT/INT4, BIGINT/INT8, TEXT, BOOLEAN/BOOL, NUMERIC/DECIMAL/DEC[(p[,s])], REAL/FLOAT4, DOUBLE PRECISION/FLOAT8, FLOAT[(p)] (1–24 real, 25–53 double; omitted p double), DATE, TIME[(0–6)] [WITHOUT TIME ZONE], TIMESTAMP[(0–6)] [WITHOUT TIME ZONE], UUID, BYTEA and optional NULL/NOT NULL; INSERT VALUES supports optional target columns/multiple rows; SELECT supports star or scalar literal/unqualified-column/parameter targets with FROM, binary scalar comparison leaves and Boolean AND/OR/NOT with grouping in targets and WHERE, bare scalar WHERE in contextual BOOL, and one column ORDER BY key ASC/DESC. FROM remains required outside identity/settings special forms and the selected null-count-only form. Values include exact decimal fractions/exponents, integer/radix literals inferred INT4/INT8/NUMERIC by signed range, quoted strings, TRUE/FALSE/NULL, 1-based `$n` and explicitly typed date/time/timestamp literals. Float4 stores binary32, Float8 binary64; both export float64, and all NaNs compare equal above non-NaNs. Timestamp input uses fixed MDY/ISO civil semantics, validated ignored zones, BC/infinity, two-stage precision rounding and transaction-owned UTC now-like inputs; prepared constants freeze while parameters reread execution start. Finite public timestamps are time.Time, infinities owned bytes; time.Time parameters import original civil fields, not UTC-converted instants. Timestamp-with-zone/DateStyle/TimeZone settings remain excluded. Quoted identifiers, nested comments, doubled quotes/apostrophes and newline string concatenation follow README's C-locale UTF8 subset. Unresolved output parameter occurrences fall back to TEXT after filter analysis, retaining their initially known type: a target-first unknown occurrence conflicts with a later non-TEXT constraint (`42P08`), while TEXT target/filter reuse works. Same-context repeats work, unused gaps fail, mixed assignment destinations remain unsupported. Text/parameter inputs coerce through type input functions; INSERT assignment casts and comparison operator checks differ. WHERE keeps only TRUE. ASC NULLS LAST/DESC NULLS FIRST remain.

Scalar comparisons admit `=`, `<>`, `!=`, `<`, `<=`, `>` and `>=`; `!=` is the
`<>` alias. Both operands may be existing scalar literals, parameters or columns.
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
No comparison implies row ordering. Comparison chaining, aliases, qualified
columns, nonbaseline casts, arithmetic and comparison in INSERT/settings remain
excluded. Feature smoke: `make smoke RUN='^TestCompareOpsSmoke$' VERBOSE=1`;
independent pinned reference: `make reference RUN='^TestReferenceCompareOps' VERBOSE=1`.
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
Existing scalar comparisons form Boolean leaves in targets and WHERE. Logical
INSERT, settings arguments, expression ORDER BY and all adjacent general
expression syntax remain excluded. Feature smoke:
`make smoke RUN='^TestLogicalSmoke$' VERBOSE=1`; pinned independent reference:
`make reference RUN='^TestReferenceLogical.*$' VERBOSE=1`.
PostgreSQL parses `SELECT TRUE AND FROM t` / `SELECT TRUE OR FROM t` as TRUE
targets with bare aliases and/or. Native aliases remain excluded and these
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
from INSERT, WHERE and setting arguments. General calls, qualification, aliases,
nonbaseline casts, arithmetic, DISTINCT, in-call ORDER BY, FILTER, OVER, star/named arguments
remain exclusions rather than PostgreSQL diagnostic-parity claims.
Feature smoke: `make smoke RUN='^TestNullCountSmoke$' VERBOSE=1`;
independent reference: `make reference RUN='^TestReferenceNullCount' VERBOSE=1`.

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
Existing scalar-only comparison operands/chaining restrictions remain.
Arithmetic/general functions/nonbaseline casts, aliases, rows/composites and no-FROM scalar
queries remain excluded except for the selected call roots described above.
Null-count calls remain standalone projection roots, not predicate operands;
predicate expressions remain excluded from their arguments.
INSERT/settings arguments remain scalar-only.
Failure-only INSERT SELECT recognition includes these existing projection roots
without executing them: `INSERT INTO t SELECT num_nulls(v) FROM t` fails0A000
at SELECT (15), while trailing `VALUES (1)` instead fails42601 at VALUES (42).
Genuinely unsupported calls retain their existing call guards.
Native controls retain finalized computed BOOL
values/metadata; dependency/ACL use includes both DISTINCT operands.
Feature smoke: `make smoke RUN='^TestCompareNullSmoke$' VERBOSE=1`;
native: `make test PACKAGES=. RUN='^TestCompareNull.*$' VERBOSE=1`;
real pinned reference: `make reference RUN='^TestReferenceCompareNull.*$' VERBOSE=1`.

Baseline explicit casts extend the scalar positions above: `CAST(expr AS type)`,
`expr::type`, and function-style int4/int8/text/bool calls, with nested/grouped
operands. Standard/postfix targets retain baseline declaration aliases; function
aliases integer/bigint/boolean/int are not admitted. Only INT4/INT8/TEXT/BOOL
source and target types are supported; other explicit casts, including identities,
fail native0A000. The direct matrix permits integer width conversion, integer
text output, TEXT input to integers/BOOL, BOOL text output as true/false and
INT4↔BOOL (nonzero true; false0/true1); INT8↔BOOL remains unsupported.
NULL keeps target metadata; unsupported pairs cannot bypass analysis with NULL.
Assignment/implicit policies do not gain these explicit edges.
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
Stable attribute IDs, ACL/comments and original storage/backing survive ALTER;
independently captured storage length fences a stale scan. DROP COLUMN removes
only the dropped identity. Retained prepared SELECT and parameterized INSERT
rebind current nullability across SET/ADD/DROP: nil23502 while enforced, nil
success after DROP, no cached-result-type error. Native known Nullable and
INT4 int64 ScanType differ deliberately from pinned pq's unavailable Nullable
and int32 ScanType. Retained snapshots keep the original metadata/values.

Dump captures and emits quoted named NOT NULL declarations, generated names
included. Restore uses common isolated CREATE/INSERT binding and atomic target
replacement. UPDATE (no executor), inheritance (no hierarchy/OR merge), and
NOT VALID/VALIDATE/NO INHERIT (no deferred-validation/hierarchy mechanism) remain
explicit native0A000 boundaries; independent real PostgreSQL-only witnesses
exercise their substantive effects. CHECK/PK/domains/general ALTER lists and
explicit-transaction DDL remain excluded, never successful no-ops.

Native INHERITS failure-only grammar allows one complete parent list on either
CREATE TABLE form; qualified/quoted/multiple parents and IF NOT EXISTS remain
0A000 at INHERITS, never a conditional bypass. Malformed/repeated/extra-tail
input is42601; script prefixes do not execute on these full-input rejections.

Public smoke: `make smoke RUN='^TestNotNull(Smoke|ControlsSmoke)$' VERBOSE=1`.


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

DROP TABLE supports grouped schema-qualified/path targets, IF EXISTS and RESTRICT/CASCADE with whole-group atomic preflight. Table owner, schema owner or superuser authority is required. The current inbound table-definition dependency set is empty, so both drop modes remove the same explicitly selected tables/contained metadata/storage/ACLs, not schemas/types/roles/independent LIKE clones. Retained Rows survive removal. Public DROP TABLE remains excluded from private Restore.

ALTER TABLE also supports one DROP [COLUMN] [IF EXISTS] name [RESTRICT|CASCADE] action, ordinary t/t* and ONLY t/ONLY(t) for standalone tables. Table OwnerOnly (not schema-owner fallback) execution uses AX; metadata-only Prepare does not lock. Survivor attribute/origin IDs remain stable while dense rows/metadata compact; descriptor+storage publish through one guarded CatalogStage, checking original storage root/observed length against concurrent inserts. Native dependencies are named NOT NULL/ACL/comments only, with empty external inbound set; indexes/views/FKs/hierarchy and transactional DDL are explicit external-only reference witnesses. Explicit native transactions exclude ALTER TABLE. Retained snapshots stay immutable; prepared survivors rebind, changed result shapes retain the cached-type boundary.
The optional ALTER DROP COLUMN keyword consumes the shared dedicated COLUMN token used by COMMENT, not legacy KW_R text matching. Quoted keyword targets remain decoded IDENT names; preserve keyword categories and positioned parser errors when integrating grammar changes.
Canonical populated zero-column Dump uses one DEFAULT VALUES insertion per row. Only private Restore parsing/authority permits that marker, validated for zero live columns/no targets and executed as a genuine empty row through common Insert. Public DEFAULT VALUES and VALUES() stay unsupported; Restore does not accept ALTER TABLE history. Feature smoke: `make smoke RUN='^TestAlterDropColumn(Smoke|ZeroColumnControlsSmoke)$'`.

Do not widen syntax beyond the documented selected surface: no savepoints, nested transactions, 2PC, SQL transaction modes, projection/table aliases, general casts/joins/functions/expressions, relation defaults, serial, primary keys, UPDATE/DELETE/RETURNING or extra clauses. SQL BEGIN [WORK|TRANSACTION], START TRANSACTION and COMMIT/END/ROLLBACK/ABORT [WORK|TRANSACTION] [AND [NO] CHAIN] support default READ COMMITTED/read-write baseline INSERT/SELECT, settings and the AX utility below. Complete excluded mode/savepoint/2PC grammar fails 0A000; malformed whole clauses/suffixes retain 42601. Driver BeginTx/Commit/Rollback preserves default/explicit READ COMMITTED options and qualified handles. Explicit CREATE/ALTER/DROP SCHEMA and CREATE/ALTER/DROP TABLE, COMMENT and role/session-authorization utilities remain native unsupported boundaries. Settings reuse the shared registry/binder and catalog-resolved sqltype descriptors/values. SESSION/LOCAL/RESET share one effective overlay and a lazy bounded per-touched-setting action map. Transaction ownership resolves commit, rollback, implicit completion and immediate abort; restoration uses captured typed/text overrides without callbacks or reparsing. Stage normalizers outside locks; final publication order is Engine→settings→Session, never settings→transaction. Prepare is metadata-only for settings. SCHEMA is the string-only search_path alias. Driver.RegisterSetting publishes copied dotted typed definitions before first Engine construction, then freezes. Supported builtins are application_name, client_min_messages with notice filtering, search_path, standard_conforming_strings, statement_timeout, lock_timeout, read-only UTF8 server_encoding and deny-only log_statement_stats. No timezone/client_encoding/savepoints or privileged/file/process effects; preserve existing role/ACL capabilities. See README for units, bounds, diagnostics and physical-session behavior. Recognized unsupported features fail `0A000`; malformed supported syntax fails `42601`. Commands accept at most one statement except Restore. Caller context cancellation stays ordinary; own statement timers return 57014 and lock-wait timers return 55P03. Failed multirow INSERT must not partially publish data.

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


Relation locking is transaction-owned AS for SELECT, RX for INSERT, internal SUE for COMMENT TABLE/COLUMN Execute, owner-authorized AX for the selected standalone ALTER TABLE utilities, and explicit single-target `LOCK TABLE name IN ACCESS EXCLUSIVE MODE [NOWAIT]` in an explicit block under the current superuser role. AS/RX coexist with SUE; SUE conflicts with other-owner SUE; AX conflicts across owners; same-owner operations reenter. Semantic SELECT/INSERT Prepare acquires AS/RX (implicit Finish releases, explicit retains despite stmt.Close); LOCK, COMMENT and ALTER Prepare are metadata-only. Earlier conflicting waiters prevent barging. NOWAIT fails without enqueue with 55P03/relation-name message. Ordinary typed lock_timeout defaults to 0/off, uses milliseconds and the existing SET/RESET/local lifecycle, and times each queued acquisition separately (including automatic preparation), never immediate grants or whole operations. Expiry is 55P03/lock-timeout message; earlier absolute SQL deadline wins (exact tie favors lock timeout), while caller context errors retain Go identity. Abort/end immediately releases exact owner state; commit publishes before waking successors. Interruption removes requests and wakes eligible successors without disturbing holders. Successful replacement fences old held/waiting epochs outside metadata locks; only genuinely queued implicit SQL drops replacement admission and revalidates exact identity afterward. ALTER releases replacement admission before catalog provider validation, checking original descriptor/storage at publication. Do not add other exposed modes, conversion/deadlock detection, row/advisory resources or introspection.
Lock-timeout55P03 during direct SELECT/INSERT analysis or semantic Prepare carries the original ObjectName's first token character Position (schema token for qualified names). Cached Execute and utility timeouts have no parse-analysis Position;57014 is excluded from this positional decoration. Do not put SQL source-position policy into manager resources or fabricate Schema/Table fields.
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
return before cancellation is observed; cancellation after final successful
publication cannot retract the replacement. Captured cursors retain old typed
rows/metadata, not authority to publish (Go sql.Tx may itself close owned Rows).

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

Restore's initialized private catalog accepts CREATE/ALTER/DROP SCHEMA, CREATE TABLE, literal INSERT, database-local COMMENT and named SET SESSION AUTHORIZATION only. Dump reconstructs bootstrap identity/name/owner changes before user objects; retained canonical builtin ACLs transform owner grantor/grantee, recreated public has absent ACL, and unchanged bootstrap retains legacy bytes. Local schema comments follow schema creation, table/column comments follow CREATE before INSERT under owner groups. Arbitrary explicit ACL serialization stays unsupported. Held INSERT rebinding INT4→TEXT rejects native 0A000 before importer/write; pinned PostgreSQL retains INT4 parameters and assignment-coerces to TEXT, an explicitly asserted difference.

