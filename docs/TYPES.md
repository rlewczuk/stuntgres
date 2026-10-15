# Native typed values and Go transport

[README](../README.md) · [Driver](DRIVER.md) · [Fixture controls](FIXTURES.md)

PostgreSQL 18's [data-type documentation](https://www.postgresql.org/docs/18/datatype.html)
is the SQL authority. This page describes native value ownership, Go transport
and implementation limits, not a supported-syntax inventory or a claim of full
PostgreSQL compatibility.

## Descriptors, parameters and retained results

The typed foundation uses catalog-owned immutable descriptors, concrete nullable
values and type-owned assignment/comparison providers. Prepared parameter types
come from their SQL destinations, never the Go argument kind. Same-type repeated
parameters share an inferred slot; gaps fail `42P18` and mixed-destination reuse
remains a native `0A000` boundary, including mixed integer widths.

Each execution imports an owned private typed frame against the current coherent
binding. Parameter and result signatures remain immutable across executions;
codecs may refresh within the same logical type lifetime. Catalog replacement
can remint user-type identities and make a held signature incompatible rather
than retargeting it by name. Already returned rows retain their values,
descriptor generation, metadata and presentation through replacement.

Typed NULL exports as nil but retains its concrete metadata. Empty and all-NULL
results retain their descriptors. Native declared-column nullability is known;
computed projections can have unknown nullability. Adapter differences must be
checked before fixture normalization: lib/pq frequently exports textual bytes
and reports `interface{}` ScanType where native exports strings.

The one non-concrete result exception is the canonical nullable ANYARRAY column
`pg_attribute.attmissingval`: native exports SQL NULL as nil with database type
ANYARRAY and `interface{}` ScanType. The six selected array shape inspectors
accept this exact already-typed NULL and return concrete TEXT/INT4 NULLs.
Reverse, sort and trim retain its ANYARRAY NULL metadata; array-to-string
returns TEXT NULL. Compatible push/cat reject it, and fill has no association.
This does not admit nonnull ANYARRAY values, ordinary declarations, casts,
domains, parameters or generic operators.

Relation declarations retain array bracket rank separately from the single
associated array type identity and stored dimensions. Rank is catalog metadata,
not shape enforcement. Native column/composite dumps retain explicit rank and
use the catalog array name for copied rank-zero declarations.

## Scalar Go values

| Native value family | Nonnull Go export |
| --- | --- |
| INT2, INT4, INT8, OID | `int64` |
| BOOL | `bool` |
| FLOAT4, FLOAT8 | `float64` |
| NAME, TEXT, BPCHAR, VARCHAR | `string` |
| Exact NUMERIC, MONEY | `string` |
| UUID, INET, CIDR, BIT, VARBIT | Canonical `string` |
| JSON | Exact source `string` |
| JSONB, JSONPath | Canonical `string` |
| DATE, TIME, TIMETZ, INTERVAL | `string` |
| Finite TIMESTAMP, TIMESTAMPTZ | `time.Time` |
| Timestamp infinities | Fresh owned `[]byte` |
| BYTEA | Fresh owned `[]byte` |
| Executable arrays, composites, ranges and multiranges | Canonical `string` |
| REGCLASS, REGTYPE, REGROLE, REGOPER, REGOPERATOR, REGPROC, REGPROCEDURE, REGCOLLATION, REGNAMESPACE, REGCONFIG, REGDICTIONARY | Captured symbolic or absent-identity decimal `string` |
| ACLITEM | Captured canonical `string` |

Numeric I/O never roundtrips through binary float. FLOAT4 input uses its own
binary32 destination codec; a Go float32 has already been widened by
`database/sql`, and a bound float64 follows pq-compatible decimal parameter
encoding before destination input. Scalar transport exports stored binary float
values independently of session textual precision.

MONEY stores signed minor units. Its captured monetary policy controls
presentation, not rescaling; Length and DecimalSize are unavailable rather than
invented from currency precision. Native guards minimum-int64 division by -1
with `22003`; it does not imitate the unguarded PostgreSQL 18.0 source path.

## NAME

NAME uses a distinct descriptor, not a TEXT alias. Native output and ScanType
are string; Length and DecimalSize are unavailable, not 63 or 64. Stored spelling
is not identifier-normalized. Input validates the complete UTF8 value before
retaining its bounded complete-code-point prefix.

Strings, textual bytes and Valuer binds use owned input. A nil interface is NULL;
native typed-nil `[]byte` is an empty NAME value, whereas pq sends NULL. Nonnil
empty bytes are nonnull empty values in both adapters. Native inferred NAME bool
binds encode `true`/`false`, distinct from the SQL Boolean output route.

Binary NAME receive rejects an overlength payload rather than applying textual
clipping. Its unpadded stream value is not PostgreSQL's physical padded storage.

## BYTEA

Inferred BYTEA string and byte-slice binds are raw octets, not encoded SQL text.
They admit NUL and invalid UTF8 bytes. Other destinations retain textual byte
binds. Nil interfaces and typed-nil bytes are NULL; nonnil empty strings/bytes
are nonnull empty BYTEA. Valuer executes once outside engine locks.

Input buffers and every exported byte result are detached. Mutating a returned
buffer cannot alter storage or retained rows. Native BYTEA ScanType is `[]byte`,
with no Length or DecimalSize. Native empty output is nonnil `[]byte`; pq's
escape decoder can instead return typed-nil bytes, distinct from NULL's nil
interface. Metadata remains available after EOF and Close.

## ACL items

`aclitem` (OID 1033) and `aclitem[]` (OID 1034) use ordinary columns,
destination-owned string/textual-byte/Valuer binds, and retained result
descriptors. Native nonnull values and ScanType are string; SQL NULL exports
nil, including typed-nil byte arguments. Length and DecimalSize are unknown.
PostgreSQL's pq adapter instead exports textual bytes with an interface ScanType.

An item stores engine-owned grantee/grantor identities and separate privilege
and grant-option masks, not its original text or an authorization grant.
PUBLIC is the empty grantee (identity zero), distinct from SQL NULL.
Zero-rights items and PUBLIC grant options are valid data. Canonical privilege
order is `arwdDxtXUCTcsAm`; each `*` applies to the preceding privilege letter,
so `wr**` becomes `r*w`, not `rw*`. Duplicate letters/options merge; leading
stars have no effect. Legacy `user`/`group` prefixes are accepted.

ACL names preserve decoded case, use a 63-byte limit, and follow ACL quoting
rules rather than SQL identifier folding. Mixed quoted/unquoted segments and
a final unterminated quoted grantor follow PostgreSQL's input parser.
Whitespace may surround names, but whitespace/digits/punctuation terminate
privilege scanning rather than becoming privilege letters. Omitting `/grantor`
defaults to bootstrap identity 10 and emits WARNING `0L000`; literal warnings
carry the enclosing character Position, while bind warnings carry parameter
Context. Bootstrap output uses the current identity's name, not the actor's name.

Scalar SQL comparison supplies only `=`. ACL-array `=` and `<>`/`!=` preserve
element order, dimensions and lower bounds; matching NULL elements are equal,
and a whole-array NULL remains strict NULL. ACL ordering is unavailable.
ACL equality does not grant native grouping/hash or scalar ACL binary
receive/send providers.

Explicit ACL output casts to sized `varchar`/`char` use character truncation
semantics, including domain coercions; assignments retain overlength rejection.
NULL casts preserve typed NULL without resolving output names.

Prepared literals retain resolved identities; fresh textual binds resolve the
current role. Rename changes new output, DROP retains ordinary decimal-identity
output, and recreating a name does not adopt the old identity. Returned rows
retain their captured names and metadata through role and catalog replacement.
Stored ACL data does not prevent role DROP.
Structural validation of stored defaults, checks and index expressions checks
the owning authority without requiring those retained role identities to remain
live. Actual SQL presentation requires an explicit captured role snapshot;
structural validation never supplies a fallback for Dump or replay SQL.

Native Dump uses captured live names and does not serialize role DDL. Restore
requires preexisting target roles and remaps both identities, including nested
arrays/domains/composites and persisted literals, into the target authority.
Persisted literal reownership propagates through enclosing expressions even
when type descriptors are unchanged; it never mutates the source definition.
View replay preserves one exact owning typed literal cast rather than adding
another on every Dump/Restore cycle; scalar NULL retains its source type cast.
Public view deparse emits one exact same-usage typed ACL annotation, including
NULL, while retaining actual different-usage conversions.
Dropped ACL identities cannot be losslessly reparsed from decimal output:
Dump rejects stale rows or definition literals before any writer call, rather
than adopting a numeric-looking role or creating principals.

## Textual structured and network transport

INET/CIDR exports and ScanType are string. String and textual-byte input use the
ordinary converter/Valuer boundary, not special `net.IP` or Go-array support.
Typed-nil bytes bind NULL; nonnil empty bytes are invalid network input. Network
arrays retain lookup identities but are not executable; network grouping/hash
providers and OS networking are not supplied by scalar storage.

BIT/VARBIT byte binds are textual digits, never packed bits. Typed-nil bytes are
NULL; nonnil empty bytes can represent a nonnull empty value. Metadata reports
bit length where available, not byte length or numeric precision.
Computed BIT substring/overlay and bitwise operator results have unrestricted
typmod -1, not the source column's width or a dynamic result-length typmod.
Concatenating any admitted BIT/VARBIT pair produces unrestricted VARBIT;
different widths are legal. Destination BIT/VARBIT assignment independently
checks width without resizing. AND/OR/XOR require equal actual widths, NOT
complements every digit, and shifts use INTEGER counts, retain width, reverse
for negative counts and zero-fill at/beyond the width (no integral count mask).
Scalar comparisons are lexical over the canonical digit sequence, not numeric
or zero-padded: `B'1' > B'01'` and `B'1' < B'10'`. NULL remains typed NULL.
These scalar comparisons do not admit bit ordering, grouping or index providers.
Bit measurements count logical digits; octet_length rounds up by eight, and
bit_count returns INT8 (other measures INT4).

UUID bindings use the ordinary converter/Valuer path. Bytes are textual UUID
input, not binary sixteen-byte values; Go UUID structs/arrays are not added.
Native UUID ScanType is string, with no Length or DecimalSize.

JSON exports exact source text; JSONB exports normalized structural text. JSON
null is a nonnull string, distinct from SQL NULL. Typed-nil bytes are NULL;
nonnull empty text is invalid. Input and structural rendering poll cancellation
without publishing partial values. JSON has no equality/order/hash provider;
JSONB storage does not itself imply GIN or additional JSON processing providers.
JSONPath retains an immutable typed representation; typed input alone does not
provide a general path evaluator.

JSONB containment `@>`/`<@` compares immutable structural nodes and returns
BOOL with unknown computed nullability. Object keys match at the same depth;
arrays ignore order/duplicates with an immediate primitive-membership exception,
not flattening or object-field scalar coercion. Numeric equality ignores scale
without floating-point conversion. SQL NULL is strict, distinct from JSON null.
Unknown operands infer JSONB from a known JSONB counterpart; known TEXT/JSON
or SQL-array operands need explicit conversion. Direct/prepared/stored calls
share this provider, without requiring JSON, SQL arrays or JSONPath execution.

JSONB top-level `||`, `- text` and `- integer` return the same JSONB descriptor
and canonical string carrier, including NULL/empty-result metadata. Concat is a
shallow right-wins object union or ordered array concatenation with non-array
singleton wrapping. Text deletion removes a key or all matching top-level array
strings; integer deletion removes one element with negative indices from the end.
These providers share immutable descendants and exact numbers without mutating
inputs. Missing keys/indices preserve the original payload. SQL NULL is strict;
JSON null remains a scalar and deletion raises `22023`, with no runtime Position.
Unknown deletion RHS chooses TEXT, INT2 can widen to INT4, and INT8/NUMERIC cannot
implicitly narrow. Existing TEXT/JSONB mixed concat returns TEXT.
With an unknown left operand, a known VARCHAR/BPCHAR key (or domain over either)
also selects JSONB deletion through the existing implicit TEXT cast. BPCHAR
padding is trimmed by that cast; an integer RHS still selects numeric subtraction.

The documented immutable parameter demand boundary also applies to these
operators: the same held `SELECT $1::jsonb - $2::text AS v WHERE FALSE` with
`1`/`x` yields an empty JSONB cursor natively, while PostgreSQL 18.0 folds its
first custom-plan binds and raises `22023 cannot delete from scalar`.
Demanded held calls and literal constant failures retain exact diagnostics;
no PostgreSQL custom/generic optimizer-plan switching is implemented.


JSON extraction (`->`, `#>` and `json_extract_path`) preserves the selected
source fragment rather than reserializing a tree. Its TEXT variants decode
selected strings and map JSON null to SQL NULL; nonstring values retain their
raw spelling. Missing/wrong shapes return typed NULL. Computed JSON metadata
uses native string/pq byte carriers and unknown length; TEXT has string carriers
and the ordinary unbounded TEXT length. Empty results retain those descriptors.
Processing validates Unicode even outside the selected fragment, while NULL
documents/paths and NULL path elements suppress processing. Storage acceptance
of a Unicode escape does not guarantee processing acceptance.

The legacy `json_object(text[])` and `json_object(text[], text[])` functions
produce JSON text directly. One array contains alternating flat key/value pairs
or two-column rows; parallel arrays must both be one-dimensional with equal
lengths (two empty arrays are valid). Lower bounds do not affect pairing. Key
order and duplicates are preserved, nonnull values are always JSON strings,
NULL values become JSON null, and NULL keys are errors. A NULL whole argument
produces SQL NULL after argument evaluation. Qualified explicit final `VARIADIC`
array syntax uses the same two nonvariadic signatures, not argument expansion.
The `jsonb_object(text[])` and `jsonb_object(text[], text[])` counterparts use
the same pairing, shape validation, strictness and string/null value rules.
They construct immutable structural JSONB directly: normalized key order uses
UTF8 byte length then bytes, and the last duplicate wins even when its value is
NULL. Numeric-looking, Boolean-looking and JSON-looking text remains a string.
Empty input produces a structural empty object. JSONB comparison/grouping and
native logical controls use the same payload as typed JSONB input. Ordinary
text-array overloads remain distinct from SQL/JSON object syntax.

SQL/JSON `JSON_ARRAY`, `JSON_OBJECT`, `JSON`, `JSON_SCALAR`, `JSON_SERIALIZE`
and `IS [NOT] JSON` use typed values and the shared validating scanner. Arrays
default to ABSENT ON NULL; objects default to NULL ON NULL. JSON null is not
SQL NULL. Query arrays collect the ordered single target of an existing query:
zero rows yield SQL NULL, while rows whose values are all omitted yield `[]`.
FORMAT JSON validates and embeds source instead of quoting it; UTF8 BYTEA is
supported. RETURNING applies ordinary string lengths and domain constraints.
Constructor RETURNING and an enclosing column assignment are distinct conversions.
Stored defaults retain outer casts: `JSON` and `JSON_SCALAR` never gain RETURNING,
and JSON object construction followed by a JSONB cast is not direct JSONB construction.
Output ENCODING requires the resolved type to be exactly BYTEA (a domain over
BYTEA is not BYTEA for this clause), and only UTF8 is supported. This rule is
shared by argument/query constructors and SQL/JSON aggregates. A non-BYTEA
encoding declaration is rejected before checking whether its encoding is
supported or its JSON_SERIALIZE return category is permitted.
Legacy `JSON_OBJECT(array[, array])` calls do not accept constructor clauses,
including FORMAT JSON, null/uniqueness policies or RETURNING. Nonempty member
objects accept those applicable clauses; empty objects accept RETURNING only.
Exact JSONB operands select JSONB constructor output unless RETURNING or an
explicit output format chooses otherwise; domains do not implicitly select it.
WITH UNIQUE KEYS compares decoded names recursively, independently per object.
Predicates classify objects, arrays and scalar roots, including JSON null. Without
UNIQUE they validate syntax without decoding escapes, so escaped NUL and unpaired
surrogates remain valid JSON source. WITH UNIQUE requires decoding and returns false
on Unicode validation failures, even in values rather than keys. Malformed JSON is
also false; cancellation and unrelated execution failures propagate. Throwing
constructor/input conversion paths retain their Unicode diagnostics.
Object keys use scalar JSON spelling (including `true`/`false` for Boolean
keys); arrays, composites and JSON/JSONB keys raise `22023`, rather than
being cast to TEXT. `IS JSON` rejects domain inputs as distinct types; cast
a domain explicitly to `json` (or another admitted base type) first.
`JSON_SCALAR` conversion of arrays/composites is STABLE, so it is not allowed
in generated expressions or expression indexes requiring immutability.

Native `JSON_SERIALIZE(jsonb)` deliberately returns valid canonical JSON text.
Pinned PostgreSQL 18.0 instead exposes U+0002 for the independently tested
two-member object fixture. Native does not emulate binary storage bytes.
See the [upstream report](https://www.postgresql.org/message-id/03583FE7-A5EE-407C-907D-67865F3F8BFC%40gmail.com)
and [parser/coercion followup](https://www.postgresql.org/message-id/CAGRkXqQXwh_gJmQE3PZj0v13omMPgFGBbb8TJoBFH8ECsAFCUg%40mail.gmail.com).


`json_array_length(json)` and `jsonb_array_length(jsonb)` count top-level array
elements and return INT4 (native ScanType and raw integer cells are int64).
Empty arrays return zero; nested containers and JSON null each count as one
element. SQL NULL returns a typed INT4 NULL. Object roots produce `22023`
`cannot get array length of a non-array`; scalar roots, including JSON null,
produce `22023` `cannot get array length of a scalar`, with no error Position.
Unknown strings/NULL/parameters infer the function's own input family; typed
TEXT, integers, arrays and the opposite JSON family do not implicitly match.
Explicit JSON/JSONB casts and own-family domains use existing coercion.
JSON counting scans validated retained text without decoding or conversion;
JSONB reads its immutable root array size. Both honor cancellation.

Demand boundary: on the first held `SELECT json_array_length($1) AS n WHERE
FALSE` (or JSONB counterpart) with `{}`, pinned PostgreSQL folds its bound
immutable call and raises the non-array `22023`. Native retains runtime-owned
parameters and returns zero rows with INT4 metadata instead. Native does not
implement PostgreSQL custom/generic optimizer-plan switching. This does not
change demanded held-call errors, constant wrong-root preflight errors, or
filtered column no-demand behavior.

Range and multirange datums own canonical endpoints/members and subtype
descriptors from their generation. Native exports string with no Length or
DecimalSize, despite numeric subtypes. Nil/typed-nil bytes are NULL; nonnil empty
bytes are malformed input. Scalar comparison uses the type-owned canonical
endpoints/members; grouping, ordering and index-key providers remain separate.
DATERANGE extends the common range representation with civil DATE days: finite
exclusive lower/inclusive upper
bounds advance one day into canonical [) form; explicit DATE infinities retain
their bounds and inclusivity, distinct from omitted endpoints. Live input/output
honors DATE's DateStyle and transaction-relative environment; SQLLiteral/Dump
uses canonical ISO independently of session presentation. Native DATERANGE
exports string and string ScanType; pq exposes bytes and interface{} ScanType.
Plain/nested domains, named-composite fields and existing binary COPY preserve
the same datum. TSRANGE uses continuous TIMESTAMP WITHOUT TIME ZONE bounds in the
same range representation: exclusive/inclusive endpoints are not advanced, even
for an open one-microsecond interval. Finite output is quoted; explicit timestamp
infinity and its inclusivity stay distinct from omitted endpoints, empty and
NULL. Valid written timezone indications are ignored without rotating civil
fields, but invalid offsets still fail timestamp input. Live bounds use DateStyle
and transaction/session-relative input; prepared literals retain their analyzed
values while binds decode in each execution environment. Native TSRANGE exports
string and string ScanType; pq exposes bytes and interface{} ScanType, with no
Length/DecimalSize. NonISO Dump remains canonical ISO; plain/nested domains,
named-composite fields and binary COPY compose through the existing provider.
TSTZRANGE extends the same continuous representation with UTC microsecond
instants. Explicit zones rotate endpoints; omitted zones use the input session.
Live output applies captured TimeZone and DateStyle, while SQLLiteral/Dump emits
ISO UTC with explicit offsets. Prepared literals freeze the whole range; binds
decode each execution. Equal offset spellings denote one instant; reversed UTC
bounds fail even when civil spellings ascend. Native TSTZRANGE has the same
string transport/metadata contract as TSRANGE, including plain/nested domains,
composite fields, NULL and binary COPY. Native controls preserve held readers
and reject failed replacements atomically across differing session environments.
Range/multirange arrays, temporal multiranges and user-defined range creation
remain outside native execution. General constructors/casts, ordering/grouping/
hash/index providers and new temporal range aggregates are not added by scalar
operator support.

### Range and multirange operators

See [PostgreSQL18 range operators](https://www.postgresql.org/docs/18/functions-range.html)
for the upstream signature tables; the executable native-family boundary is below.

The executable families are int4range, int8range, numrange, daterange, tsrange and
tstzrange, plus int4multirange, int8multirange and nummultirange. Containment,
overlap, position and adjacency accept matching range/multirange cross operands;
containment also accepts the corresponding element subtype in its documented
direction. Set operations and scalar comparisons require the same physical
family. No common-range promotion or implicit range-to-multirange cast is used.
Plain/nested domains unwrap through existing coercion; set results retain the
base range or multirange descriptor, not the domain wrapper.

Range comparison sorts empty before nonempty, then lower before upper endpoint,
including bound inclusivity. Multiranges compare their normalized members
lexicographically; a shorter equal prefix sorts first. Equality is semantic:
different NUMERIC endpoint scales compare equal, and timestamptz bounds compare
UTC instants. Omitted endpoints remain distinct from explicit infinity/NaN.
Scalar comparison does not grant SQL ORDER BY, grouping, hashing or index keys.

Every range/multirange contains an empty range/multirange, including empty
contains empty. Empty contains no element or nonempty set. Empty never overlaps;
`<<`, `>>`, `&<`, `&>` and `-|-` are false if either set is empty. Multirange
position and adjacency inspect outer endpoints, not arbitrary internal member
pairs: filling an internal gap is not adjacency to the whole multirange.
Timestamp families are continuous; a microsecond gap does not become adjacency.

Union uses empty as identity; intersection with empty is empty; subtracting empty
is identity and subtracting from empty is empty. A disconnected range union fails
with22000 `result of range union would not be contiguous`; a range difference
leaving two pieces fails with22000 `result of range difference would not be
contiguous`. These set errors have no operator Position. Multiranges preserve
disconnected pieces successfully. NUMERIC range union selects the second input
bound on comparator equality, while intersection selects the first, preserving
the selected endpoint spelling.

All operators are strict: SQL NULL is not an empty set or false. BOOL and
range/multirange result metadata survives zero rows and all-NULL inputs. Range
outputs retain native string transport/ScanType versus pq bytes/interface{};
pinned pq reports blank multirange DatabaseTypeName because its OID name inventory
predates those types. Known peers contextualize unknown literals/parameters;
literal content is not inspected to guess an element versus range overload.
Typed TEXT does not gain an implicit range conversion.

CHECK/generated/view recipes and native Dump/Restore retain infix spelling,
namespace and remapped descriptor authority. Existing populated readers keep
their rows/metadata across replacement. A held no-parameter BOOL-result predicate
can reanalyze across same-shape domain remint; replacing its operands with TEXT
fails ordinary operator resolution before fixed-result signature validation.
Catalog-domain parameter/result replacement fences remain unchanged.

Datemultirange, tsmultirange and tstzmultirange are still lookup-only. PostgreSQL
has valid operators for those families, but native lacks the prerequisite value
codecs/companions. Declaration/construction boundaries remain explicit; this is
not full temporal-multirange operator execution.

The selected range inspectors observe canonical values, not originally written
brackets. `lower`/`upper` return INT4, INT8, NUMERIC, DATE, TIMESTAMP or TIMESTAMPTZ
according to the range subtype, including on empty/all-NULL/zero-row results.
Multirange inspection uses the first and last normalized nonempty members.
Empty ranges/multiranges have NULL bounds, `isempty=true`, and false for all
four inclusivity/infinity predicates. Omitted endpoints have NULL bounds and
the corresponding infinity predicate true; explicit subtype infinity and
numeric NaN are actual nonnull bounds. Whole SQL NULL makes every inspector
and hull result NULL. String `lower`/`upper` retain TEXT preference for unknown
literals and unconstrained parameters.
The text and range overloads share selected diagnostic ordering: transform
ordinary arguments and FILTER once, validate argument counts/names, resolve
namespace and signature, then reject scalar DISTINCT/ORDER BY/FILTER/OVER.
Named text arguments are not silently treated as positional arguments.

`range_merge(a,b)` returns the smallest enclosing range of one concrete range
family, even across gaps. Empty is an identity, but SQL NULL remains strict.
`range_merge(multirange)` returns its companion range; an empty multirange
returns a nonnull empty range. Equal endpoint coordinates retain enclosing
inclusivity; fully equal endpoint comparisons retain the second operand's
payload, including NUMERIC scale. No output formatting, reparsing or member
scan is needed to inspect sealed input storage.

Direct inspector/hull projections have unknown nullability. Reading nullable
stored view/generated/projection columns retains native declaration metadata
(`true,true`), while pinned pq reports unknown nullability (`false,false`).
Native range hulls retain
string/ScanType string; pq uses bytes/ScanType interface{}. Bound results use
the existing scalar adapter, including native DATE text versus pq time.Time,
finite timestamp time.Time, and timestamp infinity bytes. Unconstrained native
NUMERIC precision/scale is unknown; pinned pq reports65535/65531/true.
TEXT Length is MaxInt64/true under both existing adapters.
Stored CHECK/generated/view recipes use typed columns and survive same/fresh
Driver Restore with reminted authority; previously returned rows and metadata
retain their old snapshots. This adds no explicit range casts, temporal
multiranges or new DEFAULT authoring path.
Unary `multirange(anyrange)` is the selected constructor exception: INT4RANGE,
INT8RANGE and NUMRANGE return INT4MULTIRANGE, INT8MULTIRANGE and NUMMULTIRANGE.
Outer domains flatten to their concrete base family. One nonempty canonical
range becomes one member without reparsing; empty becomes nonnull `{}` and
typed SQL NULL remains NULL. Integer bounds retain discrete canonical form;
numeric inclusivity and endpoint display scale are preserved.
`unnest(anymultirange)` returns the corresponding base range, in ascending member
order, with no rows for NULL or an empty multirange. Independent invocations and
held statements have fresh cursor positions. The result schema exists even on
empty/all-NULL/LIMIT0 inputs. Computed range/multirange outputs have unknown
nullability and absent Length/DecimalSize. Native raw cells and ScanType are
string; pinned pq raw cells are bytes and ScanType interface{}, and pq's older
OID inventory reports a blank multirange DatabaseTypeName.
Unknown constructor input fails42804; unknown unnest input fails42725 because
PostgreSQL has additional overloads. Unary VARIADIC spelling is accepted for
typed selected input; it does not add variadic constructor overloads.


## Temporal transport and presentation

Native DATE and TIME export strings; pq instead exports finite `time.Time`
values, with its TIME values anchored to a synthetic date. Native TIMETZ exports
string, while pq exports `time.Time`. Temporal Length/DecimalSize are unavailable;
fractional temporal precision is not numeric scale.

A `time.Time` input inferred as TIMESTAMPTZ preserves its instant; one inferred
as TIMESTAMP preserves civil fields. TIMETZ input retains its civil clock and
actual fixed offset, including seconds. Native retains a negative sub-minute
offset that pinned lib/pq v1.12.3 can encode with the opposite sign.

Finite TIMESTAMPTZ output keeps the exact instant with the execution-captured
zone offset. Timestamp infinity exports fresh bytes. INTERVAL owns separate
month/day/microsecond components, not a single duration. Exact NUMERIC scale,
floating signed zero and temporal sentinels survive generation-only retagging.

Execution captures one immutable temporal environment. Prepared temporal
constants retain analysis-time interpretation; parameter input and output use
execution capture. Retained rows never consult later session settings. Native
uses embedded timezone and Unicode data, not runtime zone files, host locale
state or process-wide `setlocale`.

## Arrays, composites, enums and domains

Arrays own immutable flat payloads, element identities, dimensions and lower
bounds. Associated array identities come from catalog links, never guessed
underscore names. Cross-array conversion delegates to existing scalar providers;
it does not add a transitive coercion graph. Lookup identity alone never grants
an executable array family.

`array_dims(anyarray)` returns TEXT; `array_ndims` and `cardinality` return INT4,
as do `array_length/lower/upper(anyarray, integer)`. They inspect outer shape
only: arrays of array-valued domains count outer elements, including NULLs.
Non-1 and multidimensional bounds survive unchanged. Zero-rank empty arrays
give NULL dimensions/rank/length/bounds but cardinality zero; whole NULL and
NULL dimensions give typed result NULLs. Nonpositive and missing dimensions
also give NULL without error. Native scalar integer ScanType/cells are int64.
The simple polymorphic input must already be an array (outer domains unwrap,
element domains remain), or the exact catalog typed-NULL carrier above.
Unknown first operands do not infer from dimension, result or Go values.
Explicit VARIADIC decoration does not expand an otherwise matching call.
Stored scalar and native replacement routes preserve mapped descriptor ownership
without introducing new leaf equality providers or additional codecs.

Array append/prepend/cat and the three array `||` overloads select compatible
leaf types and actual catalog-associated arrays. Agreed leaf domains survive;
mixed domain/base inputs use existing common typing. Fill instead uses its
known value's actual associated array. No ordinary array-of-array identity is
invented. Reverse/sort preserve shape and every bound; trim removes first-axis
items and rebases all retained bounds to 1. Sort compares full first-axis
slices, with scalar NULL placement distinct from inner slice NULL ordering.
To-string emits flattened raw scalar output under the captured statement
environment, not array quoting or driver export. It is STABLE even for INT4[];
the seven other transformations are IMMUTABLE.

`array_sample` and `array_shuffle` retain the physical input array family after
outer-domain peeling, including logical element-domain identity. Selection
operates on first-axis slices, preserving each trailing dimension/lower bound
and NULL element. Shuffle retains the first bound too; sample sets it to 1 and
its length to the requested INT4 count. Count 0 yields rank0 `{}`; whole NULL
array/count yields a typed NULL. Count boundaries use first-axis length, not
flattened cardinality. Both functions are volatile and use separate session
entropy from seedable scalar random functions; their output has no fixed order.

Unary array `unnest` retains element type identity and collation while flattening
all dimensions in storage order (last subscript fastest). NULL elements produce
present rows; empty/whole-NULL inputs produce zero rows with full element schema.
Ordinality is INT8 starting at 1, unrelated to array bounds. FROM expands named
composite elements and domains over composites through their physical member
layout; a NULL composite element is one member-NULL row, not exhaustion. Target
unnest instead produces one logical element-valued column.

Array-element typmods are not invented for polymorphic results. A domain over
VARCHAR(3) still carries its underlying length 3; ordinary array unnest produces
unsized VARCHAR. Native truthfully reports its unbounded length as MaxInt64;
pinned lib/pq independently reports `-5` for that unsized typmod. The adapter
artifact is not reproduced in native metadata. Domain INT transports as INT4;
user array OIDs retain native catalog names versus pq's empty type-name inventory.


Ordinary array `=`, `<>`/`!=`, `<`, `<=`, `>`, `>=` require the same physical
array descriptor after whole-domain peeling. Array element-domain and enum
identities remain distinct. Compare row-major elements first, then count, rank,
all dimension lengths and all lower bounds. Equality requires identical shape;
NULL elements equal each other and sort after non-NULL elements. Whole NULL
arrays produce typed BOOL NULLs. Equality and ordering demand separate existing
leaf providers, with shape-first equality and provider-first ordering. String
collation is demanded only by actual non-NULL leaf pairs. These SQL operators
do not add array-key ordering/index opclasses or missing element providers.

Named composites atomically publish their type, nonstorage backing relation,
stable attributes and executable associated array. Native output and ScanType
are string with an empty DatabaseTypeName and no Length/DecimalSize. Stable
attributes project supported shape changes; eager results keep the old shape.
Distinct named types are not interchangeable by shape. Composite arrays preserve
shape, bounds, NULL elements and nested/domain member generations through evolution.
Domains over executable composites publish executable reciprocal arrays too;
catalog replacement reowns their domain, element and member codecs together.
Comparison/hash/key providers and enum/base-type deletion remain excluded.

`ALTER TYPE name ADD ATTRIBUTE name type [CASCADE|RESTRICT]`,
`DROP ATTRIBUTE [IF EXISTS] name [CASCADE|RESTRICT]`,
`RENAME ATTRIBUTE name TO name [CASCADE|RESTRICT]` and
`ALTER ATTRIBUTE name [SET DATA] TYPE type [COLLATE collation] [CASCADE|RESTRICT]`
reuse the typed-table mutation authority. ADD/DROP/TYPE lists are atomic.
ADD fills old values with typed NULL; rename retains stable identity; DROP removes
the live field and never reuses its identity. Storage-free descriptor TYPE changes
do not require assignment casts. Actual typed rows do; stored composite consumers
(including materialized views, arrays/domains/nested values and empty/all-NULL
holders) prohibit explicit TYPE, even identity TYPE. Nonstored views alone do not.
CASCADE propagates to typed tables, not arbitrary stored composite rewrites.
Relation and domain field CHECK dependencies block DROP RESTRICT and are removed
by CASCADE. Typed child/diamond propagation retains privately pruned constraints
and unrelated local/inherited CHECKs. Domain identity, ancestry and surviving
CHECK enforcement remain intact; native pg_constraint still does not project domain
constraints. Held dot and functional field selection retain original stable
attributes and return the original typed NULL after retirement or same-name re-add.

Enums retain stable member identities across label changes. Held analyzed
literals keep their member; fresh textual parameters use current labels. Native
output and ScanType are string, with an empty DatabaseTypeName. Catalog type
locks serialize mutations with the existing cancellation/deadlock authority.
New members added to an existing type retain unsafe-member provenance until
outer commit; every demanded producer recaptures that provenance. Savepoint
release cannot upgrade child-created/child-added members to the outermost
creation exemption. General enum-array storage remains excluded.

`pg_catalog.pg_enum` exposes each actual enum definition's member OID, owning
type OID, binary32 sort position and exact NAME label, even without stored enum
values. Domains and associated arrays are not additional enum definitions.
Initial positions are 1..n; ADD may create negative, zero or fractional positions
and renumbers dense gaps without changing existing member identities. RENAME
changes only the label. Native member OIDs are unique nonzero uint32-valued odd
identities; no cross-server allocator sequence or parity equality is promised.
Catalog discovery is PUBLIC SELECT metadata, not a grant to execute the enum.
Owner-private changes and peer-committed visibility follow the ordinary catalog
authority; reading an unsafe new label is not producing its enum datum.
The source is readonly under the documented native catalog mutation/locking
boundaries. Logical Dump/Reset/Restore recreates positions 1..n and remints member
identities, while retained eager results preserve their captured metadata/data.

Domains retain logical identity while transport and metadata describe their
ultimate base. They are not per-value boxes. Column assignment publishes exact
immutable destination Usage without discarding an inner domain's collation
boundary. Domain CHECK uses the same typed expression and conversion providers:
`VALUE` has the immediate base usage, TRUE/UNKNOWN pass, and FALSE reports 23514
with target domain identity and the failing own/inherited constraint name.
Ancestors run before descendants; each domain's own checks run alphabetically.
Checks apply to fresh input and real conversion, including COPY and admitted
array/composite members, not on reads, downcasts, or exact same-domain assignment.
Checked expression conversions retain runtime demand: a false filter or empty
source does not consume their CHECK truth, and each actual row executes its own
conversion even for a constant physical value and a volatile CHECK provider.
CHECK recipe initialization remains earlier: a constant recipe error can fail
an empty consumer without evaluating its candidate truth. Genuine parameter and
nested-member input still completes checks at the original input boundary.
The same separation applies through retained CASE/COALESCE wrappers and to
planning-only JOIN/MERGE capability facts and conflict inference: those proofs
do not consume candidate CHECK truth. Existing ADD COLUMN initializes recipes
even for an empty table, but materializes a checked default or omitted fresh
NULL separately for each actual old row, never through a once-only missing-value
cache. Existing MERGE with BY SOURCE has a separate target action-classification
ON evaluation in addition to candidate matching; CHECK effects belong to those
actual execution stages, not to planning or an artificial duplicate conversion.
Stored generated, partition and index recipe initialization carries the candidate
catalog and captured input environment, distinct from later row truth. A constant
division-by-zero recipe can reject CREATE even on an empty table. Index row
execution uses the real admitted statement outside catalog/publication locks.
It retains immutable produced membership and owned B-tree keys or a hash scalar,
not an evaluator, catalog closure or CHECK-truth cache. Ordinary INSERT does not
replay that event at Finish/COMMIT; ON CONFLICT separately performs candidate
precheck, insertion and genuine existing-tuple key recheck as applicable.
Insertion metadata is captured/bound once per target/catalog authority, with an
optional checked lane alongside the ordinary fast path. Actual candidate
insertion evaluates indexes in ObjectID order, interleaving unique/exclusion
probes with checked nonunique indexes before RETURNING or the next producer row.
Canonical batch staging only validates and consumes these produced records.
On catalog drift, tracked/mixed batches revalidate explicit produced data against
current providers, roots, visibility and grants, without invoking index SQL.
The cold tracked lane also retains ordinary unique/exclusion probe outputs where
needed, including immutable prior-own row/version data. Missing/stale proof or
a newly ready checked index fails closed; unrelated catalog churn with unchanged
semantics is not rejected wholesale. Only entirely untracked old/current targets
retain the ordinary immutable retry path. Genuine deferred exclusion checks and
RETURNING re-admission keep their real owners and distinct execution phases.
Ordinary own-row comparisons and overlay population retain separate existing-key
scratch storage, leaving the active proposal key unchanged through admission.
COPY retains a different genuine producer phase: eligible input buffers flush
at 1000 tuples or 65535 server raw text bytes, then insert each tuple through its
ordered index list. Binary input has no text-byte counter. Ordinary nextval
defaults remain batch-safe; other physically volatile default trees use single
insertion, without expanding the supported DEFAULT function repertoire.
Partition COPY buffers flush in destination first-use order. Captured input line
origins supply insertion error context without replacing codec column frames or
replaying generation/defaults. Apply/Finish/COMMIT do not recreate this SQL event.
UPDATE evaluates changed values; DELETE/old-version retirement does not replay
their index CHECKs, and rollback retains sequence effects without publishing rows.
Slot-only DROP/projection, stable missing-value ADD and temporary cleanup transport
existing entries with exact row/version/position and storage authority. An actual
physical TYPE/generated/checked-ADD rewrite builds all valid ready/live physical
indexes under the DDL owner, including indexes on otherwise unchanged keys.
Unrelated rewrites preserve ready-invalid phases and roots instead of healing
them. Private pins carry the produced baseline and own-row outputs to publication
without a second build. Deferred comparisons use the actual admitted operation's statement scope.
Earlier private rows receive produced DDL keys even for plain indexes when their
admission predates the new definition; ordinary plain DML retains its no-output
fast path. Pure B-tree layout transport preserves root identity when referenced
dense slots, entries, row/version positions and inverse policy are unchanged.
Changed referenced slots receive a new data-remapped root without SQL evaluation
or phase healing. Metadata-only attachment retains global/private baseline roots
without manufacturing a private copy. Successive temporary projections carry
explicit chained lineage and the original publication fence. TRUNCATE and
ON COMMIT DELETE rebind genuine empty artifacts and private pins to the new
storage incarnation outside catalog locks, without evaluating CHECK inputs.
Overlay candidate capture validates the produced root against its actual staged
storage target; committed predecessor storage and descriptor fences independently
retain publication authority. An absent target never accepts an older root.
Trusted self/foreign catalog replacement captures source roots with its storage
snapshot, filters selected live versions and remints/reowns entries as data.
Represented routine and dictionary identities in B-tree and HASH keys follow
the same class-aware recursive remint as stored cells, including domain and
container payloads, without rebuilding SQL or changing source snapshots.
UNIQUE diagnostics for domain-wrapped oid_ops keys use the physical OID output
descriptor; the preserved logical alias datum still owns index identity.
Recursive scalar reownership also receives the actual captured ACL principal
importer when needed. That authority remains private through remint preparation
and is cleared before publication; neither snapshots nor index roots retain it.
Private remint prepares complete roots and allocator frontiers before the final
installer, which consumes those artifacts and owns one final cancellation
observation before indivisible publication. Cancellation after that successful
observation does not retroactively retract the installed generation.
Missing or malformed source authority fails closed. Only genuinely changed
generated inputs or mapped table-OID-dependent index expressions rebuild in the
existing real restore-repair statement; unrelated entries still transfer without
SQL replay. Retained temporary storage and source roots remain immutable.
Nominal `domain_name 'text'` literals decode the physical base and then use the
same checked conversion; this syntax does not admit additional non-domain
literal providers. Unquoted, separated E/B/X string prefixes retain their
existing boundary; quoted nominal names use domain-only binding, while
contiguous escape/bit literal tokens retain their existing lexer semantics.
Omitted columns and `DEFAULT VALUES` use checked fresh NULL
input when no column default exists. Foreign-action assignments use their
captured type visibility and conversion authority, including fresh NULL from
SET NULL or SET DEFAULT; they do not validate already-domain rows globally.
Consequently fresh NULL is checked while already-domain NULL produced by an
outer join or empty scalar subquery can bypass conversion. Native Dump/Restore
preserves explicit/generated names and expression providers; CHECK-only provider
CASCADE removes the dependent CHECK, not its containing domain or stored data.
Domain DEFAULT/NOT NULL/ALTER remain unavailable; supplied-value support for
`information_schema.time_stamp` does not implement its PostgreSQL inherited
default. Changed type/domain ACLs make Dump fail rather than lose
rights. Ownership transfer follows canonical root/array/backing associations
without rebuilding codecs or value signatures; typed-table owners remain
independent of their attached composite's owner.

## Catalog-valued and supporting types

REGCLASS and REGTYPE own unsigned catalog identities; symbolic output is captured
from statement-owned catalog/path authority before rows are returned. Retained
results never use a later live catalog. Generation replacement reowns actual
catalog-valued datums recursively; numeric coercion trees keep their original
numbers and make no cross-catalog identity promise. Stored scalar REGCLASS
references retain actual relation dependencies, independently of opaque array
payloads or numeric input.

REGCOLLATION (OID4191, array4192) and REGNAMESPACE (OID4089, array4090)
are unsigned four-byte catalog identities with executable scalar/array codecs.
Both fold unquoted identifiers, preserve quoted names and quote symbolic output
when necessary. Exact digit input uses OID input, including octal `010`; exact
`-` and zero display `-`. Unknown nonzero numeric OIDs remain unsigned decimal;
missing symbolic names are errors, never synthetic catalog entries.

REGNAMESPACE accepts one schema identifier, so `"a.b"` is valid but `a.b` is
invalid name syntax. Lookup and output are independent of search path and schema
USAGE; `pg_temp` is not a namespace alias. REGCOLLATION accepts unqualified,
schema-qualified and current-database-qualified names. Its effective collation
path excludes temporary schemas and schemas without USAGE; explicit schema
qualification enforces USAGE and supports `pg_temp`. Lookup uses the database
encoding or an encoding-independent catalog identity. Output is unqualified only
when the exact OID is visible, otherwise schema-qualified. Identifying a represented
ICU/libc collation does not enable its comparison execution.

Explicit IO casts to/from TEXT, CHARACTER, VARCHAR and NAME use captured catalog
authority. Explicit CHARACTER/VARCHAR typmods truncate or pad the resulting
display string; assignment retains ordinary sizing errors. This does not add
implicit string-to-alias casts, internal CHAR casts or unrelated object-alias
casts. NULL remains NULL through admitted casts. Direct and held parameters
treat both untyped nil and typed-nil `[]byte` as SQL NULL; nonnil empty byte
slices remain invalid name syntax rather than NULL. A held frame can recover
from that input error through typed NULL and then valid input.

Stored scalar alias constants retain class-specific schema/collation dependencies;
ordinary row values and opaque alias-array Const payloads alone do not prevent DROP.
DROP SCHEMA RESTRICT also observes scalar schema references in definitions outside
that schema; CASCADE removes those dependent definitions before publishing the
schema's removal. Logical Dump/Restore serializes live named identities and remaps
them by catalog class, including supported array/domain leaves. Zero and unmatched
numeric IDs retain their bits, not a promise that those numbers stay absent:
a reminted catalog can contain the same number, in which case ordinary output
names that live referent. Held literal queries reanalyze their source after
relevant DROP/recreate or rename:
replacement names resolve replacement identities, while a removed spelling
fails. Already-returned Rows retain their original symbolic spelling and metadata.
Native metadata exposes REGCOLLATION/REGNAMESPACE with string ScanType; pinned pq
reports an empty type name for REGCOLLATION and REGNAMESPACE for namespaces.

Mixed routine/object composites and unconstrained composite domains preserve
each alias family's captured symbolic output during logical replay, including
executable composite arrays. Live collation and
namespace identities must resolve to the restored named referents even when
their allocator OIDs are displaced.


REGROLE is unsigned role identity4096 with executable array4097. Input uses the
existing role catalog and single-identifier folding/quoting; dotted input is
invalid name syntax. Exact digits use OID input (including octal), bypass role
existence; unknown symbolic names fail42704. Zero/`-` display `-`, live identities
display identifier-quoted names, and unknown identities display unsigned decimal.
Output honors `quote_all_identifiers` and is captured before returning native
rows. Public REGROLE metadata has string ScanType; the pinned pq adapter uses
byte payloads and interface{} ScanType with unavailable optional flags.

Stored expressions reject only non-NULL exact scalar REGROLE Const leaves with
0A000 `constant of the type regrole cannot be used here`, no Position. NULL,
numeric coercion trees, runtime TEXT→REGROLE, ordinary row data and opaque array
payloads are legal. Scalar children beneath outer casts, CASE, ARRAY constructors
and domain coercions remain subject to the same check. Role values create no
role dependency. Held literals retain analyzed identity through role recreation;
ordinary full analysis invalidation (`DISCARD PLANS`) resolves again. Held input
parameters and runtime conversion use current execution authority.
REGROLE array CHECK equality now uses ordinary array comparison and reaches the
same scalar-Const restriction as other stored consumers. Opaque array payloads
remain distinct from scalar REGROLE constructor children. This does not change
domain CHECK/default declaration admission or add a missing leaf provider.

REGCONFIG (3734) and REGDICTIONARY (3769) are concrete unsigned four-byte
text-search object identities, exported as strings with matching database type
names and string ScanType. Exact `-` and numeric zero output `-`, not SQL NULL;
typed-nil bytes bind as SQL NULL. Digit-only input delegates to the OID base-zero
scanner (`010` means 8; `08` is invalid). Unknown nonzero numeric IDs remain
valid decimal values. Integral/OID input is implicit, INT4/INT8 output assignment;
negative INT4 preserves its unsigned bits, but negative/overflowing INT8 reports
`22003: OID out of range`. Explicit TEXT/VARCHAR input and character output use
captured catalog I/O; arbitrary cross-alias casts are not admitted.

Symbolic REGDICTIONARY input resolves only real installed dictionary objects.
Quoting, schema USAGE, current-database qualification and first-match search-path
resolution are coherent with the statement snapshot; TEMP is excluded from
unqualified lookup. A visible dictionary prints its name, otherwise its qualified
quoted name. Native does not seed `simple` or language dictionaries. REGCONFIG
has no native configuration provider: valid symbolic input returns
`0A000: text search configuration name lookup is not supported`, while numeric
and `-` carriers remain executable. Dictionary OIDs never acquire configuration
meaning merely because their numbers match.

Held text-search literals share the existing occurrence-keyed identifier analysis
cell, with actual dictionary and consulted namespace dependencies. Numeric input
adds no fabricated referent dependency. Parameters resolve at each execution;
returned values and metadata retain finalized output across schema/catalog changes.
Scalar domains and supported composite members preserve these identities; companion
arrays 3735/3770 and alias-domain arrays remain lookup-only. Binary COPY uses four
network-order bytes. Logical Dump qualifies live dictionary references independently
of search_path, installs their extension before consumers, and remaps stored data
and expression literals to destination dictionary allocations. Zero, typed NULL
and unmapped numeric values remain unchanged. These codecs do not enable
configuration/dictionary DDL, lookup functions, full-text execution or the
two-argument unaccent overload.

Mixed composites containing routine aliases also qualify live dictionary members:
routine signature reconstruction does not replace dictionary names with allocated
source OIDs. This applies to stored data, DEFAULT values and view literals.

Finite REGDICTIONARY partition bounds likewise contribute their live dictionary
provider to the logical dump predecessor graph. A child allocated before extension
installation and attached later still replays only after that provider is installed;
its bound and routing follow the destination descriptor identity. This full-reference
ordering walk does not change exact-Const dependency filtering for catalog-object
aliases.

REGOPER (2203) and REGOPERATOR (2204) are concrete unsigned four-byte operator
identities with distinct metadata and string ScanType. REGOPER name input requires
one visible signature; REGOPERATOR uses an exact ordered pair of operand type
identities, with bare NONE for the missing left operand of a prefix operator.
The complete PostgreSQL 18 builtin inventory is independent of native executable
operator providers. Numeric text accepts absent identities too; zero prints `0`,
not `-`, and remains distinct from SQL NULL. Typed-nil bytes bind as SQL NULL.
Quoted/schema-qualified input follows SQL identifier rules; output leaves symbols
raw and quotes only schemas/types when needed. Signature separation trims only
the six ASCII scanner whitespace bytes, never Unicode padding. Qualified-name
syntax precedes the closing delimiter check, and each operand resolves before
later operand syntax is examined. An empty leading operand is an invalid type
name; a trailing comma instead requires another type name.
Held operator literals share the REGTYPE analysis cell, keyed by source offset
and target alias. Named REGOPERATOR operands retain their exact type identities
and definition revisions alongside namespace authority; relevant changes reanalyze
the complete literal group before successful publication. NONE and numeric input
invent no operand dependency. Unrelated changes preserve reusable captures.
Previously returned values, metadata and finalized symbolic rows remain snapshots
across successful refresh or failed reanalysis.

Integral input and OID conversions are implicit; conversions to INT4/INT8 are
assignment casts. REGOPER and REGOPERATOR implicitly retag the same OID. Explicit
TEXT/VARCHAR input and character output use catalog-owned I/O, not implicit text
input or arbitrary cross-alias conversions. Existing binary COPY carries four
network-order bytes. Companion arrays 2208/2209 remain lookup-only.
Scalar domains over either alias (including domain chains) remain executable;
their associated arrays remain lookup-only as well. Negative INT4 input preserves
its unsigned OID bits, while negative or overflowing INT8 input reports
`22003: OID out of range`, including explicit casts.
Native logical Dump uses explicit typed numeric OID literals, preserving builtin,
zero and unknown identities losslessly even when ordinary overloaded REGOPER
output such as `pg_catalog.+` cannot be parsed unambiguously.

REGPROC (24, array1008) and REGPROCEDURE (2202, array2207) use unsigned four-byte
routine identities. Names, textual bytes and NULL frames share the existing
direct/prepared input owner. REGPROC rejects overloaded names; REGPROCEDURE
matches exact input-argument type OIDs, not call coercion/default/variadic rules.
Signature arguments use ordinary type parsing, qualification, aliases, arrays and
modifier validation. Output preserves SQL identifier quoting and canonical type
names. Numeric zero and `-` display `-`; unknown nonzero OIDs display unsigned
decimal without requiring a procedure row. Integral/OID casts and the reciprocal
routine-alias casts preserve identity; outgoing INT4 retains signed low32 bits,
while INT8 input retains the ordinary OID range check. TEXT assignment into an
alias is not implicit; explicit string IO uses current statement authority.
Columns/domains/arrays and stored constants use the same physical carrier.
Prepared literals retain identity across referent-only drops and refresh on
relevant path/role/namespace authority changes; parameter/runtime input is fresh
each execution. Dump uses qualified signatures rather than ambiguous REGPROC
names, including recursive values. Restore remaps actual extension referents,
not unknown numeric values or immutable builtin identities. Data-only values do
not own providers; expression constants have real descriptor dependencies.
Complete released identity lookup does not add execution or public pg_proc rows.
See [routine alias evidence and boundaries](design/regproc-pg18-oracle.md).

Internal CHAR catalog output uses string transport, not BPCHAR semantics or a
physical ABI. High-byte output is printable escaped text. Nonnull VOID result
output is an empty string with VOID metadata, not a TEXT alias; VOID storage/input
and arbitrary binary receive remain excluded.

XID and CID are distinct unsigned32 scalars; XID8 carries unsigned64 bits, including
epoch information supplied by the caller. They are not OID or integer aliases.
Direct/prepared binds accept canonical text or bytes, contextual signed Go integer
input, and NULL. Native results export fresh canonical decimal `[]byte` with
XID/CID/XID8 metadata and `[]byte` ScanType; SQL NULL is untyped nil. Use text or
bytes for XID8 above MaxInt64: the native default driver converter rejects larger
Go uint64 values as ordinary adapter errors. The pinned pq adapter reports an
empty DatabaseTypeName for XID8; native retains its real XID8 name.

XID admits `=` and `<>` with XID, and with INT4 only when XID is on the left.
CID admits only `=` with CID. XID8 admits all six scalar comparisons and unsigned
ORDER BY. XID/CID have no SQL ordering provider, even for empty or all-NULL data.
Existing NULLIF and simple CASE consume the same directional equality signatures,
including contextual unknown inputs and XID/INT4. GREATEST/LEAST use unsigned
XID8 ordering; XID/CID instead reject with PostgreSQL's comparison-function
diagnostic at execution, even for empty/all-NULL sources, after successful Prepare.
Only XID8→XID is an explicit identifier conversion, retaining the low32 bits.
Explicit TEXT/VARCHAR/BPCHAR I/O and assignment to those strings are supported;
numeric/OID and other identifier conversions are not. Companion array identities
remain lookup-only, including arrays of identifier domains. Supplied values do
not allocate transaction IDs, compute snapshots or populate system columns.
Existing public `pg_get_expr`/`pg_get_constraintdef` render canonical identifier
constants with explicit scalar annotations, including typed NULL and retained
XID8→XID conversion trees. Their native post-Restore text remains type-owned.

`pg_cast` reflects complete-context edges declared by the existing coercion
owner, not a second execution graph. Function OIDs are metadata references and
do not permit arbitrary function execution. Represented type identities,
including lookup-only builtins, do not authorize storage or adjacent features.

## Binary stream providers

Pinned stream helpers use descriptor-owned receive/send independently of ordinary
`database/sql` representations. Structured receive owns decoded storage, validates
member OIDs/generation/typmods and grows only after successfully received or NULL
members, never from an announced cardinality. User wire OIDs do not grant catalog
identity or constructor authority. Domains use physical base receive with logical
ownership. Numeric binary I/O remains exact.

Array/record members require exact input consumption; range bounds permit their
receiver padding. Complete earlier child errors precede missing later framing;
container-specific underflow diagnostics remain distinct. Finite temporal wire
values use the 2000 epoch. Binary TIMETZ validates civil clock and displacement
before typmod rounding, with a narrower displacement boundary than configured
POSIX temporal rules. NULL belongs to stream framing, not a receiver spelling.

JSON preserves source; JSONB/JSONPath use canonical versioned text. Special
NUMERIC receive normalizes unused finite state only after full header/digit
validation. `pg_node_tree` sends the deterministic native semantic tree, not
PostgreSQL heap-Datum serialization, and rejects arbitrary receive.

Stream codecs do not add public table admission for raw internal CHAR, identifier
companion arrays, enum arrays or the lookup-only MONEY array. XID/CID binary fields
are unsigned four-byte network-order values; XID8 is eight bytes. Private descriptor
tests are independent of public table admission. These helpers are not protocol
serving or disk/WAL persistence.

## Logical persistence and testing

Dump/Reset/Restore preserves owned values, descriptor associations and captured
result snapshots through atomic private staging. Invalid input, owner resolution,
cancellation or final publication failure leaves the live catalog unchanged.
Canonical dump presentation is independent of session float precision; temporal
and monetary settings are not serialized. Restore keeps live settings.
REGROLE data serializes live symbolic names (including recursive array/domain
leaves), zero and unknown numeric identities. Reset/Restore preserve existing
cluster roles, never export/provision them. Provision destination roles before
cross-Driver replay: live symbolic values resolve target IDs, while unknown
numeric values only promise preserved bits. Illegal stored REGROLE constants,
unknown names and reader/cancellation failures preserve the prior target state.

These are process-local logical controls, not physical durability, PostgreSQL
ABI, external `pg_dump` formats or a network server. Native transport assertions
and pinned pq raw-codec assertions remain separate in the reference suite.
Selected families include `TestReferenceName`, `TestReferenceUUID`,
`TestReferenceJSON`, `TestReferenceJSONB`, `TestReferenceComposite`,
`TestReferenceEnum`, `TestReferenceIntegerRange`, `TestReferenceNumericRange`,
`TestReferenceRegclass` and `TestReferenceRegrole`; run the relevant `make check RUN='...'` selector both
without Docker and with `REFERENCE=1`. See [testing](TESTING.md) for suite policy.

## UUID generation state

The version-7 ascending clock belongs to the physical session, not a table,
statement or transaction. Tie/backward wall time advances its minimum step.
Unshifted and zero-shift draws on that session are strictly ordered; different
shifts or sessions have no such guarantee. Pin `sql.Conn` when observing a series.
Rollback and catalog replacement do not reset the clock; physical replacement
starts fresh. Cryptographic entropy is independent of seeded SQL randomness and
is never dumped.

Planning and preparation never demand clock/entropy. Closed admitted UUID
defaults produce fresh values per inserted row. Dump preserves committed values
and symbolic provider definitions; later restored demand produces fresh values.
Execution captures temporal policy for shift arithmetic and preserves
submicrosecond remainder without overflowing UNIX nanosecond conversion.

Native applies PostgreSQL 18.6 version-7 range guards, while pinned 18.0 predates
them. Native rejects infinite shifts and timestamps outside its 48-bit packing
boundary; the older oracle permits observed wrapping behavior. These are explicit
release differences, not fabricated cross-backend golden values. Computed UUID
metadata does not gain a special nonnull proof.

## Embedded Unicode data and regeneration

Normalization and case data are pinned to Unicode 16.0.0 under
`internal/sqltype/unicode16/data`. The original official conformance corpus is
retained in `internal/sqltype/unicode16/testdata/NormalizationTest.txt`, with
attribution and license beside the runtime. Sources are the official
[Unicode 16 UCD](https://www.unicode.org/Public/16.0.0/ucd/) files:
UnicodeData, DerivedNormalizationProps, SpecialCasing, CaseFolding and
DerivedCoreProperties.

Deterministic offline regeneration from the repository root:

```sh
go run ./internal/sqltype/unicode16/generate \
  -source internal/sqltype/unicode16/data \
  -output internal/sqltype/unicode16/tables_generated.go \
  -case-output internal/sqltype/unicode16/case_tables_generated.go
```

The standard-library-only generator emits sorted static normalization/case
tables. Optional `-case-output` retains the normalization-only invocation.
Runtime classification uses binary search and lazy output construction, borrowing
unchanged input rather than building per-execution maps or rune arrays. Runtime
has no network, ICU/cgo, mutable startup maps or host-Go-Unicode dependency.
Tests compare generated outputs byte-for-byte and retain the official corpus,
original-input case context and bounded cancellation assertions.

Native targets PostgreSQL 18.0's observed normalization release boundary,
including its Hangul TBASE behavior; the official corpus is not edited to hide
that difference. This is not a blanket Unicode-conformance claim. Registered
provider-equivalent collations retain distinct identities through native replay,
not interchangeable authority.
