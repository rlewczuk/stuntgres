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
| REGCLASS, REGTYPE, REGROLE, REGOPER, REGOPERATOR, REGPROC, REGPROCEDURE | Captured symbolic `string` |
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
and a whole-array NULL remains strict NULL. Ordering is unavailable. This does
not admit unrelated array comparison, native grouping/search/hash providers,
or scalar ACL binary receive/send.

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

UUID bindings use the ordinary converter/Valuer path. Bytes are textual UUID
input, not binary sixteen-byte values; Go UUID structs/arrays are not added.
Native UUID ScanType is string, with no Length or DecimalSize.

JSON exports exact source text; JSONB exports normalized structural text. JSON
null is a nonnull string, distinct from SQL NULL. Typed-nil bytes are NULL;
nonnull empty text is invalid. Input and structural rendering poll cancellation
without publishing partial values. JSON has no equality/order/hash provider;
JSONB storage does not imply GIN, containment, extraction or SQL/JSON providers.
JSONPath retains an immutable typed representation; typed input alone does not
provide a general path evaluator.

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
native logical controls use the same payload as typed JSONB input. SQL/JSON
object syntax, other JSONB constructors and builtin catalog enumeration remain
outside this feature.

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
bytes are malformed input. Their internal normalization comparators do not
provide SQL comparison, grouping or indexing. DATERANGE extends the common range
representation with civil DATE days: finite exclusive lower/inclusive upper
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
remain outside native execution. TSRANGE/TSTZRANGE add no constructors, general
casts, comparison/order/group/index providers or range aggregates.
Scalar tsrange/tstzrange comparisons reject during analysis with42883, including
plain/nested-domain and contextual string/NULL operands. Empty relations and
NULL data do not bypass capability admission. Internal endpoint normalization
does not supply a SQL comparison provider.

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

Named composites atomically publish their type, nonstorage backing relation,
stable attributes and lookup-only associated array. Native output and ScanType
are string with an empty DatabaseTypeName and no Length/DecimalSize. Stable
attributes project supported shape changes; held results keep the old shape.
Distinct named types are not interchangeable by shape. Composite arrays,
comparison/hash/key providers and enum/base-type deletion remain excluded.

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
boundary. Domain constraints/defaults remain unimplemented; supplied-value
support for `information_schema.time_stamp` does not implement its PostgreSQL
inherited default. Changed type/domain ACLs make Dump fail rather than lose
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
This does not add general array-to-array equality or constrained domains:
native CHECK expressions requiring array equality still fail42883, and domain
CHECK/default declarations still hit their existing0A000 capability boundary.
Those analysis errors precede the stored-Const dependency walk. The reference
grid retains exact PostgreSQL outcomes independently from these native limits.

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
