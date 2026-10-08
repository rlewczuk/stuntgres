# SQL types and Go values

[README](../README.md) · [SQL dialect and limits](DIALECT.md) · [SQL reference](SQL.md)

Types are int2/int4/int8/oid/name/text/bpchar/varchar/bool/float4/float8/numeric/date, time/timestamp with or without time zone, interval, uuid and bytea. Values returned to Go are `int64`, `string` (including NAME, character types, DATE, TIME, TIMETZ, INTERVAL, exact NUMERIC and canonical UUID), `bool`, `float64`, finite TIMESTAMP/TIMESTAMPTZ `time.Time`, owned binary/infinity bytes, or nil. INSERT coerces unknown strings and supported bound values through PostgreSQL-style textual input, except raw BYTEA binds described below; typed integer/bool literals can assign to strings but not each other, and integers can assign to floats. WHERE typed comparisons do not use assignment-only casts. Parameters support nil, integer, floating point, string, bytes, bool and `time.Time` after standard `database/sql` conversion. NULL equality is never true. ASC uses NULLS LAST; DESC uses NULLS FIRST. String ordering uses C-locale bytewise comparison with BPCHAR's ASCII-tail semantics. INSERT is all-or-nothing, including multirow errors and cancellation before publication.

The internal typed foundation uses catalog-owned immutable descriptors (BOOL OID 16,
BYTEA 17, NAME 19, INT8 20, INT2 21, INT4 23, TEXT 25, OID 26, FLOAT4 700, FLOAT8 701, BPCHAR 1042, VARCHAR 1043, DATE 1082, TIME 1083, TIMESTAMP 1114, TIMESTAMPTZ 1184, INTERVAL 1186, TIMETZ 1266, NUMERIC 1700, UUID 2950; analysis-only UNKNOWN 705), concrete nullable values,
and bound assignment/comparison providers. The parser retains raw type syntax;
the engine resolves it and publishes parameter/result descriptions with column
origins. Prepared types come from destination columns, never the Go argument
kind. Same-type repeated parameters share one inferred slot; gaps fail `42P18`
and mixed-destination reuse remains the bounded native `0A000` limitation,
including mixed INT2/INT4/INT8 reuse. Each execution imports an owned private typed frame
against the current coherent binding. Driver adapters detach bytes and export
OID and INT2/INT4/INT8 as `int64`, NAME/TEXT/UUID and exact NUMERIC as `string`, BOOL as `bool`, FLOAT4/FLOAT8 as `float64`,
DATE/TIME/INTERVAL as canonical `string`, finite TIMESTAMP as `time.Time`, timestamp infinity as fresh
`[]byte("infinity")` / `[]byte("-infinity")`, BYTEA as fresh owned `[]byte`, and typed NULL as nil.
NULL still retains its concrete metadata identity.

### VOID selected function result

`setseed` returns the true builtin VOID descriptor (OID2278, typmod-1, no
collation). Nonnull VOID exports an empty native string; strict `setseed(NULL)`
exports nil with the same VOID result metadata. DatabaseTypeName is `VOID`,
ScanType is string, and there is no Length/PrecisionScale or physical column
origin. VOID is not a TEXT/NULL alias and remains excluded from declarations,
input/typed literals, explicit casts, storage and native data dumps. Its sole new
operator participation is mixed string `||`: nonnull VOID contributes empty TEXT
without deleting the volatile `setseed` child. No general pseudo-type subsystem
is added.


## OID — unsigned scalar change note

OID is a distinct unsigned four-byte scalar (builtin type26, typmod -1,
no collation), not INTEGER or symbolic REGCLASS. `CREATE TABLE o(v OID)`
stores0 through4294967295, with canonical unsigned decimal output.
OID declarations are also part of the supported type list above.
NULL retains OID identity; comparisons and sorting use unsigned order.
Mixed OID/integer comparisons implicitly convert the integer to OID.
The same conversion governs `IS DISTINCT FROM` and `IS NOT DISTINCT FROM`:
OID max and INT4 -1 are not distinct in either order. Two typed NULLs are
not distinct; exactly one NULL is distinct. Negative or overflowing typed
INT8 still fails its checked conversion rather than becoming a signed comparison.

Text input follows PostgreSQL's64-bit Linux `strtoul(base0)` semantics:
C whitespace and an optional sign, decimal, leading-zero octal and `0x` hex
are accepted; binary prefixes, separators and Unicode whitespace are not.
`'-1'` becomes4294967295, `'-2147483648'` becomes2147483648,
and `'-18446744073709551615'` becomes1 through unsigned-long negation.
Positive unsigned-long sign extensions are accepted too:
`'18446744073709551615'` becomes4294967295 and
`'18446744071562067968'` becomes2147483648.
This is not universal modulo32 conversion. Malformed input gives22P02
`invalid input syntax for type oid: "<original>"`; range failure gives22003
`value "<original>" is out of range for type oid`. Unsigned-long overflow
precedes trailing-junk validation, which precedes32-bit narrowing.
Literal failures point to the quote; typed bind input errors have no Position.
Native and pinned PostgreSQL18.0 failed text-bind diagnostics add
`unnamed portal parameter $1 = '...'` for nonempty input and
`unnamed portal parameter $1 = ''` for empty input; the original value still
appears verbatim in the primary message.

| Conversion | Context | Behavior |
|---|---|---|
| INT2/INT4→OID | implicit | Sign-extend/reinterpret32 bits; -1 becomes max |
| INT8→OID | implicit | Check0..4294967295; otherwise22003 `OID out of range` |
| OID→INT4 | assignment | Reinterpret32 bits; max becomes -1 |
| OID→INT8 | assignment | Zero-extend; max stays4294967295 |
| OID→INT2 | unsupported | Explicit42846, including NULL |
| TEXT→OID | explicit | Type-owned input |
| OID→TEXT | assignment | Canonical output through the existing I/O convention |

CAST, postfix `::`, OID identity and bounded `oid(...)` use the existing
cast binder. Unrelated cast families and standalone SELECT without FROM
remain excluded. Run scalar witnesses FROM a real one-row table.
Typed INT8 -1 fails, unlike text `'-1'` or an OID-inferred Go int64(-1) bind.
String/textual bytes/Valuer/nil use the existing owned parameter path.
Go uint64 above MaxInt64 remains an ordinary adapter conversion error.

Native OID results/ScanType are int64; pinned lib/pq returns decimal []byte
with interface{} ScanType. Both report OID and unavailable Length/DecimalSize,
including empty/NULL results. Identity casts retain column origins; conversions
lose them. Native raw-driver metadata and held snapshots survive EOF/Close/native
replacement; database/sql's closed Rows wrapper does not expose ColumnTypes.
Existing grouping, conditionals, INSERT SELECT and defaults reuse this scalar.
Dump writes OID declarations and quoted canonical decimals, and atomic
Restore preserves defaults, NULL, empty tables and failure/cancellation safety.

This feature does not populate metadata catalogs, allocate/resolve objects,
add reg* families, oidvector/arrays or OID arithmetic. Physical/binary wire
receive/send, a network server, disk/WAL durability and external pg_dump
process/archive support are non-engine mechanisms, not successful no-ops.

Feature smoke: `make smoke RUN='^TestOIDSmoke$' VERBOSE=0`.
Native: `make test PACKAGES=. RUN='^TestOID.*$' VERBOSE=0`.
Providers: `make test PACKAGES=./internal/sqltype RUN='^TestOID.*$' VERBOSE=0`.
Pinned reference: `make reference RUN='^TestReferenceOID.*$' VERBOSE=0`.

### View catalog supporting types — change note

Bounded catalog sources expose the same OID scalar described above and PostgreSQL
internal `"char"` (type OID18, database metadata `CHAR`, one-byte payload, Go
`string`). Internal CHAR is a concrete nullable type with typed comparison,
ORDER BY, grouping and parameter import/export, not TEXT/BPCHAR or SQL `CHAR(n)`. Its supporting
catalog surface does not add general internal-CHAR cast/type-DDL capabilities.

Internal CHAR accepts an empty zero byte, takes the first raw byte of other
input, and decodes exact four-byte `\ooo` octal spellings. High bytes export
as escaped octal strings (`é` becomes `\303`), never invalid UTF8 or replacement
characters; ordering compares unsigned bytes. It is not Unicode CHAR(n).
Canonical SQL literals use that same printable CHAR output, quoted as SQL text,
so contextual high-byte literals remain valid UTF8 and preserve their original
byte through queried view-definition reconstruction and native Dump/Restore.
String functions use the release implicit internal-CHAR-to-TEXT operation without
adding public casts. String `||` resolution preserves CHAR's categoryZ:
CHAR/TEXT (including typed NULL) is ambiguous42725 in either direction, whereas
CHAR/integer and integer/CHAR have unique mixed overloads. Their successful
high-byte output remains four printable ASCII bytes, and zero contributes empty
TEXT. NAME/TEXT/VARCHAR/BPCHAR are categoryS and prefer the TEXT/TEXT overload.


Stored view literal annotations retain the analyzed scalar and complete type usage,
not just the source spelling. Canonical temporal introducers preserve precision;
contextually resolved NAME, UUID, BYTEA and NUMERIC values use their existing
type-owned literal serializers and supported surrounding expression contexts.
Native generation remapping retags an immutable datum only to the same concrete
builtin type identity, preserving typed NULLs, numeric display scale and sentinels.

## NAME

NAME is the distinct PostgreSQL identifier-value type (OID19, typmod -1,
C collation950), not a TEXT alias or CHAR(64). `NAME` and quoted `"name"`
declare columns; quoted `"NAME"` is a missing type. Ordinary values preserve
case, empty strings, whitespace, trailing spaces, Unicode, apostrophes,
backslashes and newlines. They do not undergo SQL identifier folding or quoting.
Textual input silently keeps the longest complete UTF8-code-point prefix within
63 bytes, without a truncation notice: 61 ASCII bytes plus `éZ` keeps `é`,
whereas 62 ASCII bytes plus `é` keeps only the ASCII prefix. This is not a
63-character or grapheme limit, and does not normalize Unicode.

The full input is validated before clipping. NUL and malformed UTF8, even in a
discarded suffix, fail22021 with `invalid byte sequence for encoding "UTF8":`
and the offending hexadecimal bytes. Native bind errors have no Position or
Context; PostgreSQL text Bind encoding errors have `unnamed portal parameter $1`
without a value suffix. String, textual `[]byte` and Valuer binds share the
existing owned-input boundary; changing the source buffer cannot change rows.
Nil interface is SQL NULL; native typed nil `[]byte` is an empty textual value,
as for other non-BYTEA destinations. SQL TRUE/FALSE assignment uses boolout
`t`/`f`, while inferred NAME Go bool binds encode `true`/`false`.
Modifiers such as `name(3)` are retained by CREATE utility Prepare but fail at
Execute with positioned42601, `type modifier is not allowed for type "name"`,
before modifier operand conversion.

TEXT/VARCHAR-to-NAME assignment and implicit conversion clip without trimming.
BPCHAR-to-NAME clips first, then removes trailing ASCII spaces. NAME-to-TEXT
is implicit and preserves tails; NAME-to-VARCHAR/BPCHAR is assignment-only and
uses the destination's existing sizing rules. Supported scalar-to-NAME
assignment uses output/input conversion, not new explicit cast edges.
NAME-to-integer assignment fails42804; NAME compared with BOOL fails42883.
Explicit NAME casts, typed literals and name functions remain excluded.

NAME/NAME and NAME/TEXT comparisons in both directions use UTF8/C byte order,
with case and spaces significant and shorter prefixes first. A typed TEXT
operand is compared in full, never clipped into NAME. An unknown quoted operand
or NAME-inferred parameter does pass through NAME input: a64-byte input equals
its stored63-byte prefix, but a typed TEXT64-byte value is greater. Strict NULL
predicates and ASC NULLS LAST/DESC NULLS FIRST retain the existing policy.

Native DatabaseTypeName/ScanType are NAME/string; nonnull `any` results are
string. lib/pq reports NAME/interface{} and returns nonnull `[]byte`, independently
of fixture normalization. Both adapters support database/sql scans into string,
[]byte, any and sql.NullString. Length/DecimalSize are unavailable, not64 or63;
native origin-column nullability is known while pq's is unavailable. NULL-only
and empty results still retain NAME metadata. Dump/Reset/Restore preserve NAME
declarations, visible values, NULL and empty tables; retained cursors keep their
original descriptor/data, and failed staged Restore does not replace the target.

PostgreSQL's physical64-byte padded representation, arrays (OID1003), binary
`namerecv`/`namesend`, non-C collations and wire serving are external-only.
Binary NAME receive rejects64 bytes with42622 `identifier too long`, rather
than text input's clipping; native ordinary byte binds are textual, not binary.
Native persistence here is public in-memory logical Dump/Restore, not disk/WAL
durability or PostgreSQL backup tools.

Feature smoke: `make smoke RUN='^TestNameSmoke$' VERBOSE=0`.
Native coverage: `make test PACKAGES=. RUN='^TestName' VERBOSE=0`;
codec coverage: `make test PACKAGES=./internal/sqltype RUN='^TestName' VERBOSE=0`;
pinned reference: `make reference RUN='^TestReferenceName' VERBOSE=0`.


## BYTEA

BYTEA stores arbitrary octets as immutable raw bytes (OID17, typmod -1, no
collation). SQL quoted input accepts lowercase `\x` hex prefix with either-case
digits and space/tab/LF/CR only between complete byte pairs. Escape input accepts
double backslash and exactly three octal digits from `\000` through `\377`;
ordinary UTF8 contributes its encoded bytes. SQL's normal string-decoding layer
still applies; invalid UTF8/NUL SQL is not permitted. Malformed hex reports
22023 (`invalid hexadecimal digit: "X"` or
`invalid hexadecimal data: odd number of digits`); malformed escapes report
22P02 (`invalid input syntax for type bytea`). Eager literal errors retain
character-based quote positions, including Prepare on empty tables.

Inferred BYTEA string and `[]byte` binds are **raw octets**, matching lib/pq:
bound `\x41` stores four bytes, whereas SQL literal `'\x41'` stores byte 41.
Nil interface and typed nil `[]byte` are NULL; nonnil empty bytes and empty
string are nonnull empty BYTEA. Other SQL destinations retain textual byte
binds. Valuer executes once outside engine locks. Input buffers and each
exported result are detached; mutating returned bytes cannot alter storage or
retained cursors. BYTEA DatabaseTypeName/ScanType are BYTEA/[]byte even for
empty/NULL-only results and after EOF/Close. Native Length/DecimalSize are
unavailable; pinned pq reports Length MaxInt64 and unknown nullability.
Bytewise unsigned lexicographic comparison places shorter prefixes first.

`bytea_output` defaults to `hex`; `escape` doubles backslashes and octal-escapes
bytes below 32 or above 126. SHOW/SET/RESET/DEFAULT and transaction-local unwind
use the existing settings lifecycle. NOT NULL failing-row Detail genuinely uses
the statement-captured mode; raw SELECT bytes remain invariant under the setting.
Dump always writes quoted lowercase hex, including `'\x'` for empty and NULL
for null, independent of output/string settings. Restore privately parses
default-on strings, preserves live settings and retained snapshots, and
publishes no replacement on input/read/cancellation failure. BYTEA typed
literals, modifiers, casts, arrays and wire serving are not added.

Feature smoke: `make smoke RUN='^TestByteaSmoke$' VERBOSE=1`.
Native witnesses: `make test PACKAGES=. RUN='^TestBytea.*$' VERBOSE=1`;
codec witnesses: `make test PACKAGES=./internal/sqltype RUN='^TestBytea.*$' VERBOSE=1`.
Pinned reference: `make reference RUN='^TestReferenceBytea.*$' VERBOSE=1`.

Raw binary scalar signatures include `||(bytea,bytea)`, `length(bytea)`,
`octet_length(bytea)`, `bit_length(bytea)`, `btrim/ltrim/rtrim(bytea,bytea)`,
`overlay(bytea,bytea,int4[,int4])`, `substring/substr(bytea,int4[,int4])`,
and `pg_catalog.position(bytea,bytea)` (haystack then needle).
SQL OVERLAY PLACING/FROM/FOR, POSITION IN, SUBSTRING FROM/FOR, FOR/FROM and
FOR-only, plus BOTH/LEADING/TRAILING TRIM standard/comma forms are admitted.
Ordinary POSITION must be quoted/qualified, not unqualified comma syntax.
All indexing counts bytes: substring may split UTF8 sequences into arbitrary
octets; zero/negative starts clip the counted interval at one. Omitted count
returns the tail. Negative length fails22011 `negative substring length not
allowed`; INT4 start+length overflow instead extends to end. Overlay requires
positive start (same22011), defaults count to replacement octet length, inserts
for zero count, allows negative count to duplicate bytes, and appends past end
without padding. Its INT4 start+count overflow fails22003 `integer out of range`.
POSITION returns first one-based INT4 or zero, and one for an empty needle.
Search uses a constant-space linear-progress byte matcher, with cancellation
checkpoints during both needle preprocessing and source comparisons.
TRIM strips maximal endpoint byte-set membership, preserving interior bytes;
there is no BYTEA unary whitespace overload. Empty results are nonnull empty.
All operations are strict after successful input/signature analysis. Runtime
domain errors have no source Position; malformed contextual literals fail during
analysis at their original character location. Known BYTEA peers infer unknown
byte arguments and INT4 index/count slots; SMALLINT slots coerce implicitly,
but BIGINT/NUMERIC/TEXT/BOOL do not. FOR-only SUBSTRING uses the existing explicit
INT4 cast admission. Inferred Go byte binds stay raw, not byteain text.
Results retain BYTEA/INT4 metadata without samples and ignore output settings.
TEXT/regex slicing and count siblings and encoding-aware length remain unsupported0A000.
Functions retain the DEFAULT exclusion; query/write/CHECK/view consumers share
the provider and native replay. No byteapos alias or new cast edges are added.
CHECK compact/pretty SQL deparsing retains SQL-origin special-form spelling and
ordinary callee quoting. Native structural PG_NODE_TREE records `Binary <name>`
with typed recursive children, never provider evaluation or PostgreSQL physical
node serialization; immutable captured carriers survive catalog replacement.
Special BYTEA TRIM nodes also record `syntax TRIM`; ordinary Binary tags are unchanged.
Spelling provenance is presentation-only: equivalent SQL and ordinary calls
share grouping/HAVING and duplicate-label ORDER BY semantic identity.
Selected slicing/search fixed scalar signatures also accept matching `VARIADIC`
call spelling without adding array arguments or variadic execution providers.
Core strings implement TEXT trim/concatenation siblings through shared names.
Operands are analyzed once before selecting their concrete BYTEA or string
signature; all-unknown trim/concatenation calls prefer TEXT, never a destination.
Encoding-aware length exclusion recognizes its real BYTEA/NAME signature with
unknown operands or implicit TEXT/VARCHAR/BPCHAR encoding-name inputs; it returns
positioned 0A000 without adding encoding-aware evaluation. An integer encoding
argument still has no such signature and returns the independent 42883 diagnostic.
Binary slicing smoke: `make smoke RUN='^TestBinarySliceSmoke$' VERBOSE=0`;
independent reference: `make reference RUN='^TestReferenceBinarySlice' VERBOSE=0`.


## UUID

UUID stores all 128 bits inline, independently of UUID generation functions.
Contextual quoted inputs and string/textual `[]byte` binds accept uppercase or
lowercase hex, matching braces, omitted hyphens, and a single optional hyphen
after any internal group of four hex digits. Whitespace, URN prefixes, malformed
braces, nonhex digits and trailing text are rejected. Version and variant bits
are not restricted. Output and native Dump literals are always lowercase
`8-4-4-4-12`, for example `a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11`.
Comparison uses unsigned lexicographic order over all sixteen bytes, not GUID
mixed-endian order; ordinary strict NULL equality and ASC/DESC NULL policies apply.

Native UUID results export immutable canonical `string`, DatabaseTypeName UUID
and ScanType string, including empty/all-NULL results; nullability follows the
column declaration. Length and DecimalSize are unavailable (not Length16).
The pinned lib/pq adapter instead exports canonical owned `[]byte` and ScanType
`interface{}`; reference tests assert these adapter differences independently.
`database/sql` can scan native results into string, []byte, any or sql.NullString.
Bindings reuse the standard converter/Valuer path; byte binds are text, never
binary sixteen-byte UUIDs. General Go UUID structs/arrays are not added.

Malformed UUID input returns `22P02` and
`invalid input syntax for type uuid: "<original>"`. Literal errors occur eagerly,
including Prepare on empty tables, and point to the quote in character offsets.
Typed parameter input errors have no Position and carry
`unnamed portal parameter $1 = '...'`. Later tuple/input/constraint failures publish
no rows. UUID declarations and canonical quoted literals round-trip through the
existing atomic native Dump/Reset/Restore controls, with retained UUID snapshots
and metadata unchanged. Feature smoke: `make smoke RUN='^TestUUIDSmoke$'`;
real reference coverage: `make reference RUN='^TestReferenceUUID.*$'`.

UUID typed literals, casts, arrays, generators, version/timestamp extraction
functions and binary wire UUID APIs remain excluded. PostgreSQL disk storage,
network protocol serving, and the external pg_dump process/custom archives are
non-engine boundaries, not successful no-op implementations.

## Exact NUMERIC

NUMERIC, DECIMAL and DEC support unconstrained exact decimals or `(p[,s])`.
Precision is 1–1000; scale independently ranges from -1000 to 1000, including
scale greater than precision. Omitted scale is zero; unconstrained values
preserve input display scale instead of imposing zero. `"numeric"` is a catalog
name, but quoted `"decimal"` and `"dec"` are not aliases.

```sql
CREATE TABLE exact_values (id INTEGER, v NUMERIC, rounded NUMERIC(3,1),
                          thousands NUMERIC(2,-3), tiny NUMERIC(3,5));
INSERT INTO exact_values VALUES
  (1, 12345678901234567890.1234567890123456789000, -1.25, 1500, .009994);
SELECT v, rounded, thousands, tiny FROM exact_values ORDER BY id;
-- exact strings: 12345678901234567890.1234567890123456789000,
--                -1.3, 2000, 0.00999
```

Decimal fraction/exponent tokens always infer NUMERIC, even `1.` or `1e0`.
Signed integer/radix tokens infer INT4, then INT8, then exact NUMERIC beyond
int8; leading decimal zero is not octal. Digit separators and `0x`/`0o`/`0b`
integer forms use the shared literal/input path. Malformed syntax is distinct
from destination overflow. Integer/numeric equality stays exact beyond 2^53;
finite numeric equality ignores display scale. Numeric-to-integer assignment
rounds ties away from zero with checked range. Numeric-to-float conversion is
checked and uses the selected binary type; numeric-to-TEXT emits canonical text.

Numeric output is fixed decimal, never float64 or exponent notation. Examples:
`001.2300` → `1.2300`, `-0.000` → `0.000`, `1.230e-2` → `0.01230`.
Typmods round ties away from zero before checking the precision bound, preserve
`max(s,0)` fractional places and apply separately at each destination. NaN is
allowed with any typmod; Infinity/-Infinity require an unconstrained column.
Specials normalize case and Inf spellings; signed NaN is invalid. Ordering is
-Infinity < finite < Infinity < NaN, with NaN equal to NaN; NULL retains the
existing ASC-last/DESC-first and strict-equality behavior.

Result DatabaseTypeName is NUMERIC and ScanType is string, including empty and
NULL results. Scan into string or []byte preserves exact output. DecimalSize
reports declared precision/scale, including negative scale; unconstrained
numeric has unknown sizing. Bind arguments remain target-driven textual input,
not Go-kind inference. Prepared numeric constants validate input syntax during
Prepare, but typmod sizing happens at Execute: malformed `'oops'` is located at
the literal, while numeric-field overflow has Detail and no Position. Parameter
errors have no native Position/Context. Statement and Restore failures publish
no partial rows. Dump quotes canonical exact numeric output and declarations;
Reset/Restore preserve old cursor values/metadata and deterministic redumps.

`SELECT $1 FROM n WHERE v=$1` with a NUMERIC `v` fails during Prepare with
`42P08`, `inconsistent types deduced for parameter $1`, Detail `numeric versus
text`, Position `8`. Direct Query with a supplied argument reports the same
record at the execution boundary. With no arguments, native Query still reports
`42P08`, but lib/pq's PostgreSQL simple query reports `42P02`, `there is no
parameter $1`, Position `8`. These protocol records are asserted separately,
not normalized into parity.

Unconstrained storage is bounded to 131072 integer digits and 16383 fractional
places; excess format range is rejected, not silently truncated to zero.

Selected `round`/`trunc` functions use the full format scale bounds rather than
typmod's ±1000 limit: round clamps requests to [-131073,16383], trunc to
[-131072,16383], and both display `max(scale,0)` places. NUMERIC ties round away
from zero; truncation is toward zero. `scale` measures stored display places,
`min_scale` the minimum finite fraction places and `trim_scale` removes only
redundant display zeros. Numeric remainder is exact at the greater finite input
display scale (finite modulo infinity retains its original scale).
These function results are unconstrained NUMERIC/string with unknown native
DecimalSize, not the source column's typmod. See
[selected mathematical signatures](DIALECT.md#scale-and-rounding-functions--change-note);
this does not add NUMERIC operators or general casts.

Numeric SQL parsing does not broaden SET's plain-decimal-integer grammar:
settings' quoted numeric codecs retain their separate units/prefix behavior.
No serial, NUMERIC/SMALLINT arithmetic, general functions/operator syntax or
SELECT-without-FROM expansion is introduced. Standard/postfix annotations reuse
same-type scalar usages and existing directional numeric coercion edges;
known-TEXT input conversion and function-cast boundaries remain unchanged.

Feature smoke: `make smoke RUN='^TestNumericSmoke$' VERBOSE=1`.
The same smoke includes signed integer carry failures beside successful extrema,
plus direct/prepared REAL/FLOAT8 assignments of 1.5, -1.5 and zero with a tiny
exponent. Populated float comparisons against 1.0 and 1e100 assert exact result
types even when no rows match, in both direct and prepared modes.
Real pinned PostgreSQL witnesses:
`make reference RUN='^TestReferenceNumeric.*$' VERBOSE=1`.

Native logical negative-scale metadata is asserted independently of lib/pq's
unsigned-scale decoding defect, not by accepting its incorrect DecimalSize.
For unconstrained NUMERIC results (including bounded random and selected math), pinned lib/pq
reports DecimalSize `(65535,65531,true)` in direct and prepared queries by
unsigned decoding of server typmod-1. Native metadata remains `(0,0,false)`;
this adapter defect does not constrain the result's actual display scale or
precision. Reference fixtures assert the exact adapter difference independently
while retaining exact output/range/scale expectations.

## Integral bitwise operators

SMALLINT/INTEGER/BIGINT support `&` (AND), `|` (OR), `#` (XOR), unary
`~` (NOT), `<<` and signed arithmetic `>>`. Binary AND/OR/XOR widen mixed
integral inputs to the wider type; same-width inputs and unary NOT preserve
their type. Shifts preserve the left type and require an INTEGER right operand
(SMALLINT can widen implicitly; BIGINT cannot narrow implicitly).
Computed metadata is INT2/INT4/INT8 with int64 ScanType, including NULL and
empty results, without inherited physical-column origin or known nullability.

Standard CAST/`::` syntax uses existing checked integral conversions or contextual
unknown input, including SMALLINT annotations and bitwise result wrappers.
No new coercion edges are added. For example, `~1::smallint` is INT2 and
`CAST(32767::smallint << 1 AS bigint)` is INT8 -2. Standalone and conditional
SMALLINT casts use the same binder; known TEXT-to-SMALLINT and `int2()`
function-style casts retain their existing exclusions.

Shifts truncate to the left width rather than raising arithmetic overflow.
For the pinned PostgreSQL18 x86_64 target, counts mask modulo32 for SMALLINT
and INTEGER, modulo64 for BIGINT, including negative counts. SMALLINT shifts
promote to INTEGER before narrowing: `1::smallint << 32` is1, not0.
These out-of-range count rules describe pinned-target compatibility, not a
platform-independent PostgreSQL promise. `(-8)::smallint >> 2` is-2.

Known integral operands resolve unknown quoted/NULL/parameter peers to their
width; shift counts resolve to INTEGER. Unknown left shift inputs with known
INTEGER counts resolve to INTEGER; SMALLINT counts leave them ambiguous.
Unknown-only AND/OR/XOR or unary NOT report42725, not a default INTEGER
signature. Unsupported concrete signatures report42883 at the operator.
Quoted input errors retain existing width-specific English diagnostics.
After complete analysis, a known constant NULL simplifies a selected strict
bitwise expression to its resolved typed NULL, so a row-dependent conversion in
the other operand is not evaluated. For example,
`NULL::integer & CAST(x AS integer)` returns INT4 NULL even when stored TEXT
`x` is `'bad'`. This planning rule also covers either shift operand and retains
the left operand's result width. Eager quoted-input errors and immutable
conversion preflight errors remain errors; `NULL::integer & 'bad'` still fails.
A NULL obtained only from a row or imported parameter is not this SQL constant
planning fact: ordinary runtime operand evaluation remains left-to-right.
Stored default/view definitions, conservative dependencies and computed result
metadata are preserved.

These operators compose in existing table queries, writes, conditionals,
aggregate expressions, defaults and views. Defaults preserve each operand's
type through native Dump/Restore, including narrow truncation when the
destination is BIGINT and INTEGER shift counts. BIT/VARBIT, ordinary SMALLINT
arithmetic, NUMERIC/float/OID bitwise overloads and ordinary SELECT without
FROM remain excluded.
Feature smoke: `make smoke RUN='^TestBitwiseSmoke$' VERBOSE=0`.

## Binary floating point

REAL/FLOAT4 store IEEE binary32; DOUBLE PRECISION/FLOAT8 and bare FLOAT store
binary64. FLOAT(p) selects REAL for binary precision 1–24 and DOUBLE PRECISION
for 25–53, without a per-value typmod. Out-of-range precision fails `22023`
at the precision token, even for CREATE TABLE IF NOT EXISTS. Quoted `"float4"`
and `"float8"` are catalog names; quoted keyword aliases such as `"real"` are
not aliases. Result DatabaseTypeName is FLOAT4/FLOAT8, ScanType is float64,
including NULL/empty results; no fictitious DecimalSize or Length is reported.

Decimal inputs may be quoted, inferred numeric tokens or positional binds:
`INSERT INTO f VALUES(0.1)`. Floating arithmetic and floating-point casts remain unsupported.
Text REAL input rounds directly to binary32 (not through binary64):
`'7.038531e-26'` stores bits 0x15ae43fd. Bound float32 is already widened by
database/sql; bound float64 is serialized as pq-compatible decimal parameter
text and parsed according to its SQL destination, not its Go kind. Finite
overflow or nonzero input rounded to zero fails `22003`; nonzero subnormals
are accepted. Bad syntax fails `22P02`, preserving the original input; range
errors quote the recognized numeric prefix. Quoted textual input errors point
to the quote, while typed parameter input errors carry actual unnamed portal Context without Position.
Conversion of an inferred NUMERIC literal to a float destination is instead
execution-owned: Prepare succeeds, and range failure has no Position/Context.

Portable input accepts decimal numbers, C-ASCII whitespace at the ends,
case-insensitive NaN, Inf/Infinity and signed infinities. Host strtof/strtod
extensions such as hex floats, signed NaN or NaN payloads are not admitted
natively and are not claimed portable PostgreSQL parity. NaNs compare equal
and sort above all non-NaNs, including Infinity; signed zeros compare equal
but retain their sign. Known integer and NUMERIC operands of float equality
convert to binary64, whereas unknown strings/parameters contextualize to the
column. Thus REAL `'16777216'` does not equal integer `16777217` or known
NUMERIC `16777217.0`, but does equal quoted `'16777217'` after binary32 input
rounding. Known NUMERIC against REAL uses checked FLOAT8 conversion, not REAL
narrowing; overflowing/underflowing FLOAT8 conversion is an execution error.

Output is PostgreSQL's default shortest-precise decimal with **open** rounding
boundaries (not plain Go shortest ties-to-even output), canonical
NaN/Infinity/-Infinity/-0, at most 9/17 significant digits. REAL uses fixed
notation at display exponents [-4,6), double at [-4,15); otherwise signed
lowercase `e` with at least two exponent digits. Selection of legacy
extra_float_digits output modes is not implemented. Dump quotes every float
output and emits canonical REAL/DOUBLE PRECISION declarations, so Restore
roundtrips payloads and specials through the existing supported grammar.
Native REAL export is the exact widened stored binary32; pq instead parses
the decimal output as binary64. Thus REAL `'0.1'` returns
0.10000000149011612 natively and 0.1 through pq; reference assertions preserve
this adapter distinction before comparing binary32 semantics. That comparison
must decode independently specified output text directly at 32 bits: narrowing
pq's binary64 export can double-round. For REAL `'7.038531e-26'`, native storage
has bits 0x15ae43fd, but narrowing pq's returned binary64 to binary32 produces
0x15ae43fe. Both adapter outputs are asserted separately, not normalized away.

Executable float acceptance (commands must run to establish verification):

```sh
go test -count=1 -v -run '^TestFloat.*$' ./internal/sqltype .
go test -count=1 -v ./internal/parser
go test -tags reference -count=1 -v -run '^TestReferenceFloat.*$' ./reference
go test -count=1 -v -run '^TestFloatSmoke$' ./smoke
```

These assert exact release output/bit witnesses, precision/metadata, independently
expected native/pq values, direct/prepared binds and diagnostics, NaN bags and
ordering, later-tuple atomicity, cancellation and retained control snapshots.

Table SELECT targets and scalar comparison filters use one immutable typed
expression binder/evaluator, also shared by INSERT and bounded setting arguments.
Untyped literal and parameter targets are named `?column?`; explicitly typed temporal
targets are named `date`, `time` or `timestamp`; physical columns retain their
names and origin metadata. Integer literals use INT4 in range and INT8 otherwise;
unknown output strings, NULL and otherwise unconstrained parameters resolve to
TEXT **after** filter analysis. Parameter occurrences retain their type at initial
analysis: a target `$1` starts unresolved before WHERE. Thus
`SELECT $1 FROM t WHERE id=$1` fails `42P08` with Detail `integer versus text`
at the target, rather than projecting INT4; a later BOOL constraint similarly
conflicts with TEXT fallback. `SELECT $1 FROM t` infers TEXT independent of the
Go value, and `SELECT $1 FROM t WHERE s=$1` works when `s` is TEXT.
Bare WHERE accepts BOOL columns/literals/parameters and contextual unknown
NULL/strings (`'yes'`/`'no'`); concrete non-BOOL input fails `42804`. Only TRUE
matches: FALSE and typed BOOL NULL/UNKNOWN do not. SELECT targets and WHERE admit
one binary comparison of existing scalar operands: `=`, `<>`, `!=`, `<`, `<=`,
`>` or `>=`. `!=` aliases `<>`. Either operand may be a literal, parameter or
unqualified column, including an admitted baseline or character-family cast of
those scalar operands; results have `?column?`/BOOL metadata with native bool
ScanType and no physical-column origin, even on empty input. Either NULL yields
BOOL NULL/UNKNOWN, including NULL compared with NULL; WHERE discards it.
INT2/INT4/INT8 compare exactly across widths without assignment narrowing or
float conversion. Existing TEXT/NAME, BOOL, FLOAT4/8, exact NUMERIC, UUID and
same-type DATE/TIME/TIMESTAMP comparison providers remain; no cross-temporal casts
are added. C text ordering and FALSE before TRUE apply.

Both operands are analyzed before unknown coercion. A quoted string or NULL
uses the known peer's type; two unknown operands choose TEXT (`'10'<'2'` is true).
Known TEXT does not become integer/BOOL implicitly. Each parameter occurrence
copies the shared type known when visited: `SELECT i=$1,j=$1 FROM t` fixes INT4
from `i`, then compares the known INT4 with BIGINT `j` without narrowing.
`SELECT i=$1,b=$1 FROM t` instead fails42883 for BOOL versus INTEGER.
An earlier bare `$1` still falls back to TEXT and can fail42P08; later inference
never rewrites it. Selected quoted-input failure is final, at the literal;
incompatible known operands fail eagerly in Query/Prepare at the operator,
even on empty tables, with the canonical operator spelling in diagnostics.
FROM is still required outside identity/settings special forms and the
selected-call-only form (including random functions). These scalar comparisons also form Boolean leaves above
NOT, AND and OR in SELECT targets and WHERE. Bounded integer arithmetic and
baseline and character-family explicit casts are scalar operands. Comparison
chaining, SELECT aliases/qualified column references, general functions, casts
outside the bounded baseline and character-family sets and comparison
in INSERT/settings remain excluded. Comparisons do not sort rows.
Selected null-count calls remain excluded from Boolean/comparison operands,
and Boolean/comparison/arithmetic expressions remain excluded from their arguments.
Former signed literal inputs remain admitted, including signs around baseline
casts of numeric literals. Casts keep their precedence, conversions and errors;
they are not removed or reassociated through the sign.

Feature smoke: `make smoke RUN='^TestCompareOpsSmoke$' VERBOSE=1`; independent
pinned PostgreSQL coverage: `make reference RUN='^TestReferenceCompareOps' VERBOSE=1`;
native coverage: `make test PACKAGES=. RUN='^TestCompareOps' VERBOSE=1`.

## Character types

`CHAR(n)`/`CHARACTER(n)`/`BPCHAR(n)` use BPCHAR (OID1042), while
`VARCHAR(n)`/`CHARACTER VARYING(n)` use VARCHAR (OID1043); TEXT remains OID25.
Length counts UTF8 code points, not bytes or grapheme clusters, and ranges from
1 through10485760. CHAR/CHARACTER without length default to1; BPCHAR and
VARCHAR without length are unlimited. Quoted `"bpchar"` and `"varchar"` resolve
catalog names; `"char"` is a separate unsupported internal one-byte type, and
`name` is not a character alias.

Fixed-width values pad with ASCII spaces. TEXT, VARCHAR and unlimited BPCHAR
preserve physical trailing spaces. Assignment shortens excess only when every
discarded character is an ASCII space, otherwise reporting22001 (`value too
long for type character(n)` or `character varying(n)`). Explicit `CAST` and
`::` between these string types truncate arbitrary excess at code-point
boundaries; NULL retains destination type/length. Explicitness does not leak
into surrounding assignment. BPCHAR conversion to TEXT/VARCHAR trims trailing
ASCII spaces before destination sizing; BPCHAR-to-BPCHAR preserves and resizes.
Nonstring assignment output conversions do not add explicit edges to BPCHAR or
VARCHAR; the separate [baseline cast matrix](QUERIES.md#explicit-baseline-casts) retains conversions to TEXT.
A cast infers an unresolved parameter from its destination, but converts an
already-known character parameter from its fixed source identity. Earlier
unknown output/ANY occurrences and mixed INSERT inference boundaries remain
unchanged. Diagnostic type names are `character` and `character varying`.
Catalog names and cast labels remain `bpchar`/`varchar`; metadata and unlimited
Dump declarations remain BPCHAR/VARCHAR.
BPCHAR and VARCHAR also participate in the existing string-category resolver for
ordered INSERT source VALUES. Common-type selection follows row order and the
existing implicit edges, rather than forcing TEXT for every string combination.
Subsequent BPCHAR-to-TEXT conversion trims ASCII tails; VARCHAR-to-TEXT preserves
them. Target assignment sizing and all-or-nothing publication remain unchanged.
Source VALUES retains a common length only when every row has the same type
identity and modifier. Equal numeric modifiers on CHAR(2) and VARCHAR(2) do not
impose a common width. With an unlimited BPCHAR destination, CHAR(2) `'b'`
followed by VARCHAR(2) `'a'` stores `"b "` and `"a"` without adding a pad to
`"a"`; reversing the source rows chooses VARCHAR and removes the CHAR pad.

Character comparisons accept string columns, literals, NULL, parameters and
these casts on either side within the existing comparison/logical surface.
BPCHAR/BPCHAR and BPCHAR/VARCHAR comparisons ignore ASCII tails; BPCHAR/TEXT
trims only BPCHAR, while TEXT/VARCHAR tails remain significant. An unknown string
literal next to BPCHAR follows BPCHAR semantics. Ordering uses the same nonnull
providers, with UTF8/C byte ordering only: no locale-general, ICU, collation or
general string-function claims.

Expression ORDER BY validates the bounded provider/collation before reading rows.
REGCLASS's internal canonical OID text compares as unsigned32 numeric identity,
not lexical spelling or all-equal fallback. VOID is a valid selected function
result but has no PostgreSQL ordering provider; ORDER BY rejects it with42883 and
the explicit-ordering-operator Hint before evaluation. The position names the
original ORDER reference, including an alias or ordinal, not its visible target.
UNKNOWN/nil/internal invalid usages remain invariant failures.

Native ColumnTypes reports BPCHAR/VARCHAR/TEXT, string ScanType, constrained
Length n and unlimited Length MaxInt64, including empty/NULL results and after
EOF/Close/Reset/Restore. DecimalSize is unavailable. Independently, lib/pq
reports BPCHAR ScanType interface{} and unlimited BPCHAR/VARCHAR Length -5;
these adapter quirks are not copied into native metadata.
Column-derived labels survive nested casts; casts of unknown strings, NULL and
parameters use the outermost resolved type's catalog name as their output label.
Identical-Usage column casts retain native physical origin and known nullability,
including NOT NULL. Resizing or retyping is a genuine expression with unknown
nullability and no origin; a later identity cast of that expression does not
restore the physical proof.

Invalid string encoding uses22021 with PostgreSQL English byte diagnostics.
Typed input failures carry actual unnamed portal Context. Both native and PostgreSQL CREATE
utility Prepare accept invalid min/max/generic modifiers and TEXT modifiers;
direct/prepared Execute reports their positioned semantic error.
Keyword modifier syntax errors are Prepare errors on both backends.
Generic BPCHAR and quoted catalog character spellings accept simple identifier
modifiers as well as constants: `bpchar("2")` has length2, while `bpchar(foo)`
reports integer-input22P02 at the type name. Keyword CHAR/VARCHAR precision
remains a single unsigned integer. TEXT rejects modifiers before interpreting
their contents, including nonnumeric, overflow and unary-plus forms.
Native Dump/Reset/Restore preserves declarations, padding, Unicode, escapes,
NULL and old reader snapshots; failed staged restores publish nothing.
PostgreSQL wire serving, COPY, pg_dump/pg_restore processes and physical
TOAST/disk storage remain external-only mechanisms.

Focused checks: `make test PACKAGES=. RUN='^TestCharacter'`,
`make reference RUN='^TestReferenceCharacter'` (real pinned PostgreSQL18.0),
and `make smoke RUN='^TestCharacterSmoke$'`.

## Unicode16 normalization data

The core string providers return TEXT/-1 (BOOL/-1 for normalization predicates)
and reuse implicit character conversion before evaluation. BPCHAR padding is
removed from source, trim set and ordinary form operands; VARCHAR/TEXT tails
remain meaningful. Trim works on UTF8 codepoints, not grapheme clusters.
Mixed string `||` uses descriptor-owned explicit-text output, including BOOL
`true`/`false`; existing adapter output is not repurposed as a conversion.
REGCLASS mixed output formats its canonical OID through the execution's captured
catalog and relation search path: visible names are unqualified, shadowed names
are schema-qualified, and necessary identifier quotes are preserved. Missing
relations retain numeric output (zero spells `-`). The datum is not rewritten,
no public cast edge is added, and retained TEXT results never consult a later
catalog. Nested stored CHECK and explicit write/USING expressions propagate
snapshot demand through existing statement capture. Native DEFAULT retains its
existing REGCLASS-input exclusion (0A000); ordinary string defaults still compose
with trim/normalization/concatenation. PostgreSQL-only DEFAULT success is tested
separately, not advertised as native capability.


Normalization data is pinned to Unicode **16.0.0** in
`internal/sqltype/unicode16/data/UnicodeData.txt` and
`DerivedNormalizationProps.txt`. The unmodified official conformance corpus is
`internal/sqltype/unicode16/testdata/NormalizationTest.txt`; Unicode attribution
and license are retained beside the runtime. Sources:
https://www.unicode.org/Public/16.0.0/ucd/UnicodeData.txt,
https://www.unicode.org/Public/16.0.0/ucd/DerivedNormalizationProps.txt and
https://www.unicode.org/Public/16.0.0/ucd/NormalizationTest.txt.

From the repository root, deterministic regeneration is:

```sh
go run ./internal/sqltype/unicode16/generate \
  -source internal/sqltype/unicode16/data \
  -output internal/sqltype/unicode16/tables_generated.go
```

The standard-library-only generator emits sorted static sparse arrays, recursive
decomposition sequences, composition exclusions and all four quickcheck tables.
Runtime has no network, ICU/cgo, mutable startup maps or dependence on Go's host
Unicode tables. Tests compare regeneration byte-for-byte, exercise every official
corpus row and independently read assigned scalars absent from Part1.

The target is PostgreSQL18.0 release behavior. Its inclusive Hangul TBASE
composition consumes U+11A7 after LV, and predicate quickcheck may still return
TRUE for that input. The official corpus is not edited to hide this release
boundary; a separate pinned regression covers it. This is not a blanket claim
of Unicode conformance beyond the exercised corpus. Native text encoding remains
UTF8, with no new encoding setting; the real PostgreSQL LATIN1 oracle is an
independent encoding-restriction witness, not a native configuration option.

## Time without time zone

`TIME`, `TIME(p)` and optional `WITHOUT TIME ZONE` declarations and typed literals
work through the same binder, parameter inference, metadata and native controls
as other supported types. TIME has OID 1083; omitted precision is -1, explicit
0–6 is retained by declarations, LIKE, prepared descriptions and Dump. Higher
precision warns with `22023` (`TIME(7) precision reduced to maximum allowed, 6`)
and clamps to 6. Keyword negative/signed precision is a positioned syntax error;
quoted `"time"(-1)` reaches the provider's `22023` negative-precision diagnostic.
Quoted numeric/Unicode modifiers use the same INTEGER and scanner contracts as
timestamp precision below. Utility Prepare remains effect/notice-free; typed
literal warnings occur once when analyzed, not every held execution.

The stored clock ranges from `00:00:00` through distinct `24:00:00`, inclusive.
Output and native ScanType are **string**, with `HH:MM:SS` and a trimmed optional
microsecond fraction, never a date/zone suffix. The pq reference adapter instead
returns `time.Time` anchored at year 0 January 1, or January 2 for `24:00:00`;
this is an adapter boundary, not a PostgreSQL text-output difference.

Accepted inputs include colon and compact clocks, AM/PM (including 0AM/0PM),
minute/second fractions (`04:05.5` means `00:04:05.5`), seconds 60 carry, and
limited optional Gregorian dates, ISO T/Julian labels and recognized noise
(`on`/`at`). Standalone weekday tokens are rejected: `04:05 on Monday` is
invalid TIME input, even though weekday words are recognized for timestamps.
Input fractions round to microseconds ties-to-even; destination precision then
rounds half-up. `23:59:59.5` at precision 0 becomes `24:00:00` without wrapping;
invalid `24:00:00.1` fails before sizing. Same-type comparison/ORDER BY preserve
the endpoint and ordinary NULL placement; no TIME/TIMESTAMP comparison or casts
are admitted. String, owned byte, nil and `time.Time` parameters use the TIME
destination, retaining written wall-clock fields rather than converting to UTC.
Colon-clock component syntax/ranges are checked while reading the token; the
whole-clock domain is checked only after later tokens, optional-date validation
and AM/PM conversion. Thus duplicate zones in `25:00 UTC UTC` yield `22007`
before the deferred hour overflow, and `2003-13-07 25:00` retains the DateStyle
Hint. TIMESTAMP keeps its distinct early whole-clock check. Incompatible typed
assignments identify the actual source (`time without time zone` for TIME).
Temporal assignment and explicit casts follow the bounded datetime cast pairs
described below; they are not permission for arbitrary cross-category casts.

Numeric zones, release abbreviations and full names are **validated then ignored**;
`04:05:06-08` stays `04:05:06`. Unknown zones and invalid displacements fail;
unknown full-zone diagnostics use the lowercased token, e.g.
`time zone "america/bogus" not recognized` for `America/Bogus`.
Historically varying full names (including currently no-DST Asia/Kolkata) need
a valid supplied date; historically fixed names/aliases such as Etc/GMT+5 need
none. The immutable timezone rules come from the pinned PostgreSQL 18.0 image's
IANA 2025b dataset, including backzone history, without runtime zone I/O or
host-zone dependence. Dynamic abbreviations need no
date; DST-modifier and duplicate-field restrictions still apply.
Single-token POSIX `X0` and equal-offset `X0Y0` are fixed, unlike `X0Y`.

`allballs` is midnight. `now` uses the transaction-owned session-local start; held typed
and contextual constants freeze at analysis, while parameters read each execution
start (shared within one explicit transaction). TIME `now` does not reserve a
zone field, unlike TIMESTAMP `now`; `allballs` does. Malformed fields, modifiers,
later tuples and staged Restore failures never publish partial state. Dump quotes
canonical clocks and Restore preserves precisions, NULLs, endpoints and old cursors.
See the timezone-aware section below for TIMETZ, session settings and conversions.
General temporal arithmetic/functions and ordinary no-FROM SELECT remain excluded.

Feature smoke: `make smoke RUN='^TestTimeSmoke$' VERBOSE=1`.
Native coverage: `make test PACKAGES=. RUN='^TestTime.*$' VERBOSE=1`.
Real pinned reference: `go test -tags reference -count=1 -timeout=300s -v -run '^TestReferenceTime.*$' ./reference`.

## Civil DATE

`DATE` and quoted `"date"` work in declarations and typed literals; `date`
remains an ordinary table/column identifier. DATE has OID1082, no precision,
and DatabaseTypeName DATE / ScanType string even for empty and NULL results.
All nonnull exports are canonical strings: `YYYY-MM-DD`, expanded years,
` BC`, `infinity` or `-infinity`. lib/pq instead exports finite `time.Time`
and default infinity `[]byte`; these adapter differences are intentional.

DATE modifier literals share the scalar call opener with baseline casts, but
retain raw numeric modifiers until DATE's no-typmod diagnostic is resolved.
This does not admit `date(...)` function casts or change int4/int8/text/bool
calls; standard/postfix casts involving DATE remain unsupported.

Input accepts ISO and unambiguous month names, compact dates, Julian days,
ordinal dates, fixed MDY ambiguous numeric input, BC, epoch and infinities.
Examples `January 8, 1999`, `19990108`, `1999.008` and `J2451187` all produce
`1999-01-08`; `2001--01-01` is also valid and produces `2001-01-01`.
The proleptic Gregorian finite domain is **4714-11-24 BC through 5874897-12-31**,
including dates beyond TIMESTAMP's range. There is no textual year zero;
1 BC is astronomical year zero for leap calculations. Comparisons and ordering
use civil days, with infinities outside all finite values and ordinary NULL rules.

Validated trailing clocks/zones never shift the written date or carry into it:
`1999-01-08 24:00` remains `1999-01-08`. Bound `time.Time` similarly uses its
own location's civil Y/M/D and ignores time/nanoseconds; Go year zero means 1 BC.
Strings, owned textual bytes and nil use the existing type-owned input path.
The pinned pq adapter shifts BC `time.Time` years with Go `AddDate` before
encoding: its year-zero February 29 bind becomes `0001-03-01 BC`. Native DATE
preserves `0001-02-29 BC`; textual BC input avoids that pq encoding limitation.
`now`, `today`, `yesterday` and `tomorrow` use session-local transaction start;
prepared typed/contextual constants freeze at analysis, while parameters read
execution start. DateStyle affects input/output as described below. CURRENT_DATE,
general temporal arithmetic, arrays and ordinary no-FROM SELECT remain excluded.

Malformed input is `22007`; invalid calendar fields and finite-range failures
are `22008`, preserving original input and English diagnostics.

Failed `time.Time` range imports format the original temporal input only on
failure, including clock, fractional seconds, numeric offset (`Z` for UTC,
offset seconds when present) and a trailing ` BC` for BC years. For example,
year 5874898 at UTC midnight reports
`date out of range: "5874898-01-01 00:00:00Z"`.
Years whose serialized AD/BC number exceeds INT32 fail earlier numeric decoding
with `date/time field value out of range`, rather than `date out of range`.
Successful imports remain civil-day-only, with no clock/zone validation or conversion.

Elementary month/day overflow includes the DateStyle Hint. Literal errors fail Prepare
even on empty tables and point to the quote in character positions; native
bind input errors carry actual unnamed portal Context without Position. Incompatible typed DATE assignments name
the source type `date` (`TIME`/`TIMESTAMP` retain `without time zone`) and fail
`42804` during analysis/Prepare without changing stored rows.
DATE modifiers fail `42601` at the type name (CREATE execution, typed-literal
analysis). This no-modifier rejection precedes integer conversion or unary-plus
validation of parsed modifiers: `date(3.0)`, `date(2147483648)` and `date(+3)`
all report `type modifier is not allowed for type "date"`. Ordinary parser
syntax errors still take precedence; TIME/TIMESTAMP/NUMERIC modifier rules are
unchanged. Later-row failures and canceled staged Restore leave committed data
unchanged. Dump quotes canonical DATE values;
Reset/Restore preserve retained snapshots and metadata. Unordered queries have
no portable row-order guarantee.

Feature smoke: `make smoke RUN='^TestDateSmoke$' VERBOSE=1`.
Independent pinned oracle: `make reference RUN='^TestReferenceDate' VERBOSE=1`.
Native coverage: `make test PACKAGES=. RUN='^TestDate' VERBOSE=1`.

## Timestamp without time zone

`TIMESTAMP`, `TIMESTAMP(p)` and their explicit `WITHOUT TIME ZONE` forms
work in table declarations and typed literals in the existing scalar positions.
Omitted precision is typmod -1; explicit 0–6 is retained in column/result metadata
and native Dump. Plain LIKE copies that precision. The quoted catalog type
`"timestamp"` also admits integer modifiers, including unary-minus constants.
Ordinary signed, empty, multiple and noninteger modifiers are syntax errors;
quoted negative precision reaches the type provider and fails `22023`.
Complete quoted numeric spellings pass through the existing INTEGER input
function at CREATE execution: decimal/exponent input fails `22P02`, and integer
overflow fails `22003`. Quoted unary-plus modifiers are nonsimple syntax: Prepare
accepts them, but CREATE execution fails `42601`,
`type modifiers must be simple constants or identifiers`, at the type opener.
An existing authorized `IF NOT EXISTS` destination skips semantic modifier input,
not malformed numeric syntax. Greater precision succeeds with
WARNING `22023`, for example `TIMESTAMP(7) precision reduced to maximum allowed, 6`,
and effective precision 6. Typed-literal warnings occur at initial analysis/Prepare,
not every held execution; CREATE warnings/errors occur at utility execution.
Column declarations resolve the raw modifier again while building the relation
descriptor, so excess precision emits two execution warnings: the first with
the declaration position, then one without a position.
An authorized existing IF NOT EXISTS table skips semantic precision validation,
not malformed syntax. `client_min_messages=error` filters these warnings.
Unicode-escaped quoted identifiers resolving to `"timestamp"` use the same
modifier rule. Timestamp-related keywords retain their release identifier and
bare-label categories. Standard/postfix timestamp casts follow the selected
datetime cast matrix; unrelated cast pairs remain unsupported.

Input combines dates and times using captured DateStyle ordering (default MDY).
Output uses captured ISO/SQL/Postgres/German style; default ISO has a space,
seconds, trimmed fractional zeros and no offset for plain TIMESTAMP. Supported
families include ISO dates/space or T separators,
full/abbreviated text months and mixed delimiters, MDY slash/dash/dot dates,
compact dates/times, short-year pivot 00–69→2000–2069 / 70–99→1970–1999,
ordinal year/day and Julian day (including fractions), colon times, AM/PM,
recognized weekday/noise tokens, AD/BC, `epoch`, and infinities. Four-digit `0000`
is invalid; 1 BC is exported as astronomical Go year 0. Invalid Gregorian dates
are rejected, not normalized; valid `24:00:00` and `23:59:60` carry to the next day.
Accepted numeric labels are Julian `j` / `jd` / `julian` and the ISO `T`
separator. Interval-like `y`, `m`, `d`, `h`, `mm`, `s`, `dow`, `doy`,
`isodow` and `isoyear` labels are not timestamp input fields and fail rather
than silently constructing a date/time.
Recognized but unsupported unit labels still preserve numeric-error precedence:
`y 2147483648` fails field-range `22008`, whereas `y 1` is malformed `22007`.
An intervening `at`/`on` does not consume a pending label. Julian labels likewise
survive noise, weekday/era/meridian and ordinary zone fields (`J on UTC 2451187`
is January 8, 1999). ISO `T` preserves numeric prefix overflow before duplicate
time rejection; a punctuated zone immediately after pending `T` is not a named
zone input (`1999-01-08 T America/Bogus` is malformed, not unknown-zone `22023`).
Numeric zone suffixes, all release Default abbreviations, case-insensitive IANA
names/aliases and admitted POSIX indications are validated then **ignored**:
`TIMESTAMP WITHOUT TIME ZONE '2004-10-19 10:23:54+02'` remains 10:23:54.
DST gap/fold fields likewise remain civil fields. Arbitrary trailing suffixes are
not discarded. POSIX rule strings are subject to timestamp token boundaries:
comma-separated rules/angle-bracket names are not interchangeable with SET TIMEZONE.
Unbracketed POSIX standard/daylight names may be one or two characters; they
have no artificial three-character minimum. POSIX offset seconds admit 60,
unlike numeric ISO zone seconds. Thus `1999-01-08 04:05:06.1e2` is valid:
the tokenizer separates fraction `.1` from POSIX zone `e2`, retaining civil
04:05:06.1 rather than interpreting an exponent. The fractional-field helper
itself accepts digits only after the dot; whole-input tokenization is distinct.
Recognition uses the immutable PostgreSQL REL_18_0/IANA 2025b name index without
host-zone-file I/O or offset/transition conversion.
The `dst` modifier is admitted with a fixed standard abbreviation or numeric
zone, in either order, without changing civil fields (`PST dst`, `dst +02`).
It conflicts with fixed daylight abbreviations such as `EDT`; dynamic names
such as `MSK`, full IANA/POSIX zones, duplicate modifiers and a modifier without
a zone are invalid. A full punctuated zone is looked up before duplicate-zone
rejection: unknown `America/Bogus` after `UTC` still fails `22023`, while a
known second zone fails `22007`. DST/daylight fields also participate in the
delimited-date completion mask: leading `PST` is exempt as TZ, leading `EDT`
or `PST dst` is not; separately decoded textual-month dates remain distinct.
Combined fields are interpreted sequentially: `January 8 040506-8 1999`
fails `22007` because the date is incomplete when that compact numeric field
is encountered. Compact time with a completed date and textual colon-time
forms remain supported.
Numeric dispatch also depends on previously decoded date and time bits, not
width alone: `January 8 04:05:06 120000 PST` retains year 120000 and civil clock
04:05:06. An overflowing year in that ordering is field-range `22008`.
Small ordinary numeric date fields can supply a fraction before their date
role is assigned: `1999 1 8.5` means January 8 at midnight plus .5 seconds.
This does not admit every dotted field ordering: `1999 1.5 8` and
`1999 1.1 8` are malformed (`22007`), not fractional-month equivalents.
A later clock replaces that fraction; a fraction after an already decoded
clock replaces its fractional seconds. Full numeric-field length controls
short-year interpretation (`January 8 99.1` is year 0099, not 1999).
Compact clocks follow release summation semantics: `250000` carries into the
next day at 01:00, `049900` becomes 05:39, and `040599` becomes 04:06:39.
Colon clocks still reject hour 25, minute 99 and second 99. AM/PM validation
precedes compact carry, and the final summed value must remain in finite range.
Ordinal day 366 is accepted in either year/day spelling: `1999.366` and
`1999 366` roll into 2000-01-01, while `2000.366` is 2000-12-31.
The converted civil date is then checked against the finite timestamp range.
Only ordinal values 1–366 claim the day-of-year field. In `1999 367`, the
second numeric token falls back to a month, whose range is validated before
rejecting the incomplete date: `22008` with a DateStyle hint. In contrast,
`1999.367` fails the delimited-date completion rule first (`22007`, no hint).
Only present year/month/day fields are validated; missing fields do not
invent zero-valued calendar errors.
Weekday vocabulary includes `weds`; a stated weekday does not validate calendar
agreement with the numeric date.
Julian integer and decimal fields are decoded separately: `J2451187.1`
is exactly 1999-01-08 02:24:00. A present fractional suffix supplies the time
even when zero, so `J2451187.0 04:05:06` is a duplicate-time error.
A pending Julian label also admits an empty integer before a fraction:
`J .1` is Julian day zero plus .1 day (4714-11-24 BC at 02:24), while
`J .` is that date at midnight. Both claim the time field and reject an
additional clock; ordinary numeric-date parsing still requires an integer.
A colon time may end with a decimal point (`04:05:06.` means zero fraction);
bare compact or Julian trailing dots follow different lexical branches and
are invalid. Malformed fields fail `22007`, overflowing date/time integers
fail `22008`, and overflowing numeric zone components fail `22009`.

Fractional seconds first convert to binary64 seconds, then scale to microseconds
and round ties-to-even. Go `time.Time` parameters use the same two-stage
conversion without formatting: 125500 nanoseconds becomes 125 microseconds,
while 126500 becomes 127, matching their decimal timestamp input rather than
rounding the nanosecond count directly. Civil fields and validated-but-ignored
offsets are preserved; typed and destination precision still size afterwards.
Julian fractions instead multiply the separately decoded fraction by a day's
microseconds and truncate to integer microseconds, matching the release codec.
Own typed-literal precision and destination precision are separate later sizing
stages, rounding signed microseconds away from the 2000-01-01 epoch on ties:
p0 rounds 2000-01-01 00:00:00.5 up, but 1999-12-31 23:59:59.5 down.
Comparisons never resize the right input to a column's precision. Finite input spans
4714-11-24 BC through 294276-12-31; rounding its last microsecond can produce finite
294277-01-01, although direct external input of that date fails. Dump represents this
rounded endpoint with `TIMESTAMP(0) '294276-12-31 23:59:59.999999'`, so staged Restore
reconstructs it through the ordinary parser/input/sizing path even into a p6 column.

Native `now`, `today`, `tomorrow` and `yesterday` strings use the transaction-owned
session-local civil start, captured once for an explicit/implicit transaction. All rows and
statements in one explicit transaction share it. Prepared timestamp constants,
including contextual quoted inputs, freeze when analyzed; string/byte parameters
are read afresh at execution using that execution transaction's start.
SQL BEGIN and native BeginTx use the same clock owner. COMMIT/ROLLBACK AND CHAIN,
including recovery by COMMIT AND CHAIN from an aborted block, capture a fresh
transaction start for the new incarnation; redundant BEGIN and control Prepare do not
reset it. Plain COMMIT/ROLLBACK clear it before the next implicit transaction.
Independent public, codec/engine and pinned reference assertions retain both
the floating-point and timestamp contracts after integration.
Run `go test -count=1 -v -run '^TestTimestampSQLTransactionChainSmoke$' ./smoke`
for literal/parameter clock stability, relative civil dates and fresh chain/end
boundaries; `go test -tags reference -count=1 -v -run '^TestReferenceTimestampSQLTransactionChainClock$' ./reference`
asserts these relationships separately on each backend, not absolute clock parity.
Freezing belongs to each physical driver preparation, not to SQL text in a
pool-wide cache. Reuse the same Conn-prepared handle to retain a constant;
`Tx.StmtContext` of that handle performs a new physical preparation and captures
the new transaction's start. Native table DDL remains unsupported (`0A000`)
inside explicit transactions; timestamp INSERT errors retain normal abort
semantics.
`now` already supplies date, time and a zone field; `allballs` supplies time
and a zone field. An additional zone therefore conflicts in either token order
(`now UTC`, `UTC now`, or `1999-01-08 allballs UTC` are invalid).
Date-first `1999-01-08 allballs` is valid midnight; `allballs` alone lacks a date.
`epoch` and signed/unsigned infinity are decoded within the same whole-input
field loop: `on epoch`, `epoch at`, `on +InFiNiTy` and `- InFiNiTy at` are valid.
They reserve date, time and zone fields; conflicting fields in either order
are rejected rather than discarded after the first special token.
Core date/time words (including all special values) stay core tokens before
adjacent digits or plus signs; they are not reinterpreted as POSIX zone names.
For example, a completed date/time followed by `now1` or `now+2` is invalid,
while genuine short POSIX names such as `e2` retain their ignored-zone behavior.
This is context-sensitive: standalone `today+2` is a valid date and numeric
zone, with the same session-local civil midnight as `today`, not an adjacency blacklist.
Delimited and dotted-ordinal dates must complete the global date-field mask:
prior clock/`allballs`, weekday, AD/BC or AM/PM fields make that branch invalid;
only ordinal and timezone fields are excluded from its completion test.
For example, `Friday 1999-01-08` is invalid, but separately decoded text-month
fields in `Friday January 8 1999` remain valid. Partial numeric date context
can complete normally (`1999 01-08`). This is token-branch-specific, not a
blanket ban on every date/time field permutation.
The global date mask is shared, but textual-month interpretation is local to
each delimited field: `January 08-1999` and `1999-Jan-08` are valid, whereas
`January 1999-08` and `Jan 1999/08` overflow the day field with a DateStyle hint.
Outer numeric MONTH can become DAY when a textual month follows, provided no
DAY or prior textual month is present and the numeric field is 1–31. Any YEAR
is retained: `1999 8 January` is valid, but `99 January 1999` is a duplicate-month
syntax error without a DateStyle hint. This does not change delimited-field
local textual-month interpretation.
Delimited dates admit a single trailing separator and interior repetitions
(`1999-01-08-`, `1999//01//08`), but reject separator-only trailing runs
(`1999-01-08--`, `1999/01/08//`).
Numeric and alphabetic date components use the release's single-character
advancement (`1999-Jan08` decodes as January 8). Additional components are
decoded before completion is rejected, so an overflowing fourth integer is
a field-range error, not an early component-count syntax error.
The textual pass rejects unknown words before numeric overflow:
`2147483648-xxx-08` yields 22007 without a hint. Throwaway `at`/`on` skip only
that first pass and remain for numeric decoding: `2147483648-at-08` and
`2147483648-on-08` yield field-range 22008 without a hint, while ordinary
`1999-at-08` and `1999-on-08` are malformed. Tokens retain borrowed input
spelling; ASCII case-insensitive comparisons (including sorted zone lookup)
avoid allocating lowercase copies on successful mixed-case input, including
months, weekdays, era/meridian and Julian labels, noise words and special values.
Whitespace following a sign stays in the borrowed input span; numeric-zone and
signed-infinity consumers trim it locally without concatenating a token copy.
Token budgets count the sign, non-whitespace tail and terminator, not the gap.
All ASCII whitespace forms retain numeric-zone bounds (`- 16` fails `22009`).
The 25-field limit is checked before discarding subsequent punctuation; trailing
whitespace is harmless, but punctuation after field 25 is malformed. ASCII DEL
is not a punctuation delimiter and is rejected with the entire original input.
Day-relative inputs still allow an ordinary clock and one zone.
Finite output/ScanType is `time.Time` at zero offset, including BC/wide years;
infinity is owned bytes and NULL is nil without losing TIMESTAMP metadata.
`time.Time` parameters import their original Date/Clock/Nanosecond civil fields,
validating the numeric offset within ±15:59:59 before ignoring its effect;
there is no conversion of the instant to UTC. Offsets of ±16 hours fail with
22009, including when the civil year also exceeds the finite timestamp range.
A serialized date-year integer overflow precedes zone decoding instead (22008,
without a DateStyle hint). On failure, diagnostics include the original parameter
text, including unrounded nanoseconds, offset seconds and BC spelling. Formatting
is failure-only; successful civil imports do not allocate a timestamp string.

Literal diagnostics include the quoted input's rune position. Semantic precision
diagnostics use the type opener; malformed numeric modifiers use the offending
token's rune position. Numeric junk and incomplete exponents report `42601`
`trailing junk after numeric literal` with the complete token; `1..2` instead
reports syntax error at `".."`, positioned at its first dot. FLOAT and TIMESTAMP
modifiers reuse this scanner behavior; scalar decimal expressions are supported
through the shared exact NUMERIC literal path described above.
Parameter input diagnostics omit native Position/Context.
Malformed/date/time/range/zone errors publish no partial INSERT or Restore
replacement. Retained cursors keep original values, precision and origin metadata
across append/Reset/Restore; exported infinity buffers cannot mutate stored values.

Timezone-aware types, DateStyle/TimeZone and selected temporal casts/conversions
are described below. General temporal functions/arithmetic and ordinary no-FROM
SELECT remain excluded; the default input order is MDY.
Run `go test -count=1 -v -run '^TestTimestampSmoke$' ./smoke` for the feature smoke,
`go test -count=1 -v -run '^TestTimestamp.*$' .` for public acceptance, and
`go test -tags reference -count=1 -v -run '^TestReferenceTimestamp.*$' ./reference`
for independently expected real PostgreSQL 18.0 scenarios.
Codec corner cases and allocation assertions can be selected with
`go test -count=1 -v -run '^TestTimestamp.*$' ./internal/sqltype`;
`go test -count=1 -v -run '^TestTimestamp.*Allocations$' ./internal/sqltype`
includes signed-whitespace and revised numeric/prefix/DST branches.

## Time-zone-aware values

`TIMESTAMPTZ(p)` / `TIMESTAMP(p) WITH TIME ZONE` (OID1184) stores a UTC
microsecond instant, not the input zone name. `TIMETZ(p)` / `TIME(p) WITH TIME ZONE`
(OID1266) stores a civil clock and fixed seconds-west offset. Precision 0–6
uses existing microsecond sizing; above6 warns/clamps. Precision precedes WITH,
for example `TIMESTAMP(3) WITH TIME ZONE`. Plain TIMESTAMP/TIME remain without-zone.
Qualified `pg_catalog.timestamptz` / `pg_catalog.timetz` typed literals retain
their explicit namespace, with or without precision and quoted type names.
Resolution uses the existing namespace-USAGE authority, not the session's
unqualified type lookup route.
NULL retains the concrete type and metadata. Native DatabaseTypeName is
TIMESTAMPTZ/TIMETZ and ScanType is time.Time/string respectively; temporal
Length/DecimalSize are unavailable. Finite timestamptz time.Time exports preserve
the exact instant with the execution-captured zone offset, whereas infinity
exports fresh owned bytes. A time.Time parameter inferred as timestamptz preserves
its instant; one inferred as plain timestamp preserves its civil fields.
A `time.Time` parameter inferred as TIMETZ retains its civil clock and actual
fixed offset, including positive or negative offset seconds; it does not round
the offset to whole minutes or convert the clock to the session zone. Direct
and prepared imports use the same checked temporal input and precision rules,
and native Dump/Restore retains that imported fixed-offset value.
The pinned lib/pq v1.12.3 parameter encoder has a distinct sub-minute boundary:
it encodes a `time.Time` offset of -30 seconds as +00:00:30. Native import retains
-00:00:30; textual negative offsets bypass that pq encoding discrepancy.
The pinned lib/pq adapter exports TIMETZ as time.Time, unlike native string output.
Cross-backend text witnesses use `::TEXT`; native ScanType assertions remain
independent rather than normalizing this adapter difference.

Explicit input offsets/names convert to an instant; absent zones use session
TimeZone. Output renders that instant in the captured session zone. Thus
`2004-10-19 10:23:54+02` renders `2004-10-19 08:23:54+00` in UTC and
`2004-10-19 04:23:54-04` in New York, but compares equal in both sessions.
IANA names and aliases are case-insensitive. Session-zone abbreviation meanings
precede installed Default abbreviations; dynamic abbreviations retain historical
meanings, not just today's active offset. Rule bytes are embedded from the pinned
PostgreSQL 18.0 Debian image's IANA2025b/backzone data; runtime never consults host
zone files. Historical second offsets and future rules use the same provider.

PostgreSQL's DST gap/fold preference is intentional, not an error:
New York `2018-03-11 02:30` uses the pre-transition offset (07:30 UTC);
`2018-11-04 01:30` uses the post-transition offset (06:30 UTC).
Explicit EDT selects 05:30 UTC in that fold. Malformed/conflicting fields,
unknown zones and invalid offsets still fail with positioned English diagnostics.
Literal ISO offsets are east-positive and bounded to ±15:59:59.
POSIX specifications have the opposite sign (`UTC-2` means +02:00).

`TIMESTAMP AT TIME ZONE text` produces TIMESTAMPTZ; TIMESTAMPTZ converts back
to civil TIMESTAMP. AT LOCAL uses session TimeZone. TIMETZ converts to TIMETZ,
shifting/wrapping its clock and replacing its offset. Named destination offsets
are chosen at transaction-start instant, not the source literal's date or a
per-row wall clock. A full DST-bearing zone in timetz input requires a supplied
date; stored timetz output remains fixed when session TimeZone changes.

Stored/provider-derived TIMETZ offsets range from 169 hours east to 168 hours west,
distinct from literal input's ±15:59:59 bound: a valid wide POSIX/session rule may produce
a timetz offset that cannot be entered as an ordinary numeric-offset literal.
Prepared capture, comparison, conversion and native replay retain that identity.
Timestamptz equality/order uses the instant. Timetz compares non-modular UTC-clock
keys and then seconds-west offset: `09:00+01` sorts before, and is unequal to,
`08:00+00`. Grouping, ordering and conditional/distinct predicates share these semantics.
SELECT DISTINCT and min/max aggregates remain outside the supported query surface.

Selected datetime casts interpret civil DATE/TIMESTAMP in session TimeZone when
converting/comparing with an instant; reverse casts use local civil fields.
DATE/TIMESTAMP midnight equals the corresponding local-midnight timestamptz.
DATE and plain TIMESTAMP also compare across types at civil midnight, and
timestamp-to-time assignment drops the date and retains the clock.
Dropping timetz's zone retains its clock. Temporal TEXT casts honor DateStyle:
ISO/SQL/Postgres/German output and MDY/DMY/YMD numeric input ordering.
TIME/TIMETZ output clocks do not depend on DateStyle. Use explicit SQL TEXT casts
to witness non-ISO timestamp formatting; Go time.Time string scans are not a
PostgreSQL wire-format equivalence claim.

Prepared typed/default constants freeze value, precision and timetz offset at
analysis. Parameters, column casts/comparisons and AT LOCAL read execution settings.
Held cursors retain captured presentation and metadata across settings changes
and native replacement. Dump uses canonical ISO/UTC timestamptz and stored timetz
offsets, independent of TimeZone/DateStyle, and Restore preserves live settings.
No timezone catalog views, INTERVAL arithmetic, timezone(...)
aliases or CURRENT_* producer-family expansion are delivered here.

Feature smoke: `make smoke RUN='^TestTimezoneSmoke$' VERBOSE=0`.
Public/provider witnesses: `make test RUN='^TestTimezone' VERBOSE=0`.
Independent pinned oracle: `make reference RUN='^TestReferenceTimezone' VERBOSE=0`.


Initial timestamptz input range checking and typmod rounding are distinct:
rounding the valid upper input `294276-12-31 23:59:59.999999+00` to precision0
can produce `294277-01-01 00:00:00+00`. That PostgreSQL-compatible carry succeeds;
it is not a new permission to parse arbitrary initially out-of-range input.
An explicit offset that moves an initially finite input beyond either endpoint
fails with SQLSTATE `22008` and `timestamp out of range: "<original input>"`,
not the zoned type label used by malformed timestamptz input.


## INTERVAL — change note

INTERVAL (OID1186) stores signed32-bit months, signed32-bit days and signed64-bit
microseconds independently. `24 hours` stays `24:00:00`, not `1 day`; `30 days`
stays `30 days`, not `1 mon`. SQL comparison and grouping use the widened span
`(months*30+days)*86400000000+microseconds`, so those pairs compare equal while
native serialization retains their distinct representations. Infinity sorts
below/above finite values and is unchanged by field restrictions.
Existing GROUP BY consumes the same equality classes; SELECT DISTINCT and
aggregate DISTINCT remain outside the supported query surface.

Declarations admit INTERVAL, INTERVAL(p), YEAR/MONTH/DAY/HOUR/MINUTE/SECOND,
YEAR TO MONTH, DAY TO HOUR/MINUTE/SECOND, HOUR TO MINUTE/SECOND and MINUTE TO
SECOND. Precision may accompany SECOND-ending ranges; p0–6 rounds fractional
time half away from zero. Larger p succeeds with warning22023 and effective6.
Restrictions truncate lower fields toward zero, never remove higher fields:
YEAR removes leftover months/days/time, MONTH removes days/time, DAY removes
time, HOUR/MINUTE endings discard smaller time fields. Quoted generic
`"interval"(range[,precision])` takes PostgreSQL's raw range mask, unlike
keyword INTERVAL(p)'s precision shorthand.
Explicit generic precision is retained even for non-SECOND endings: for example,
`"interval"(4,2)` is YEAR with precision2. Dump preserves that legal generic
declaration rather than emitting invalid keyword `INTERVAL YEAR(2)`. Defaults
serialize legal field literals and retain the exact destination typmod on Restore.
Precision warnings identify the INTERVAL keyword: at analysis/Prepare for typed
literals, and at Execute for CREATE (alongside the existing unpositioned
descriptor-build warning). Invalid generic CREATE modifiers likewise fail at
Execute, while invalid field grammar is a syntax error.

Input accepts verbose quantity/unit forms, optional @/ago, SQL year-month and
day-time forms, ISO8601 designators and basic/extended alternatives. Fractional
months/weeks spill downward using30days/month and24hours/day. Verbose duplicate
fields fail22007; ISO repeated designators accumulate. Months, days and time
have checked independent domains; malformed input uses22007, decoder overflow
22015 and final conversion/precision overflow22008 without partial publication.
Generic verbose/SQL input uses C-locale ASCII whitespace, without Unicode boundary
stripping. ISO retry receives the original bytes: ` PT1S`, `PT1S ` and ISO input
bounded by tabs/newlines fail22007, with the original whitespace retained in the
diagnostic. Non-C whitespace such as NBSP is not accepted around ISO, verbose or
infinity input. Ordinary ASCII whitespace around `1 second` or `infinity` remains
valid. Only generic malformed syntax retries ISO; generic range errors do not.
Generic year-month parsing validates the leading numeric field before checking
the month component's range. A malformed prefix remains a syntax failure eligible
for the original-byte ISO retry, not a spurious month-range failure.
The SQL year-month form `1-2` means14months; a preceding bare quantity in
`1 1-2` duplicates the month field and fails22007, including restricted columns.
ISO exponent notation retains basic-format integral width:
`P00021015e0` means `2 years 10 mons 15 days`, and `PT001020e0` means `00:10:20`.
ISO numeric fields also preserve PostgreSQL's legacy hexadecimal floating syntax
inside interval strings: `PT0x1p0S` means one second, `P1DT0x1p0S` one day and
one second, and `PT0x1.8S` 1.5 seconds without an explicit binary exponent.
Hexadecimal digits/prefixes and `p` exponents admit either case; signed fractions
retain their components. Leading plus remains malformed22007. This is not an
extension of SQL numeric literal syntax. Malformed hexadecimal fields and floating
conversion overflow fail22007; accepted numbers still undergo the same magnitude,
component-domain and exact underflow checks.
The same leading-minus number guard admits case-insensitive `-NaN`, `-Inf` and
`-Infinity` prefixes (including `-NaN(foo)`), but rejects these nonfinite numbers
with22015 before unit validation. Unsigned NaN/Inf fields remain malformed22007.
No NaN interval value is stored, and whole-interval ±infinity behavior is unchanged.
A parsed ISO magnitude outside ±1e15 is decoder overflow22015, not malformed
input. Unrestricted `2562047788:00:54.775807` is the largest finite microsecond
component, but rounding it for SECOND(2) fails22008; a later overflowing parameter
cannot publish an earlier valid row from the same statement.
Clock input checks the positive magnitude before applying its sign:
`-2562047788:00:54.775808` fails22007, while the adjacent signed clock
`-2562047788:00:54.775807` and `-9223372036854775808 microseconds` remain
valid finite values. The rejected spelling does not shrink the stored time domain.
Precision sizing checks the signed half-unit offset before truncating, after field
restriction. Intermediate offset overflow is22008 even if the mathematically
rounded result would fit. At SECOND(5), both `9223372036854775804 microseconds`
and its negative fail. The accepted neighboring positive `9223372036854775802`
and negative `-9223372036854775803` microseconds round to their respective signed
`9223372036854775800` values. Precision6 preserves the exact finite component,
and infinities bypass finite precision sizing.
ISO floating underflow follows the pinned reference number parser, not an exponent
blacklist: `PT1e-400S` and inexact `PT5e-324S` fail22007, while `PT0e-400S`,
`PT-0e-400S`, exact subnormal values and bounded `PT1e-300S` remain valid zero
intervals. Minimum-normal boundary cases retain the observed rounding/tininess
distinction. First and later ISO fields share this validation before publication.
Boundary validation uses the original exact decimal, not an intermediate rounded
decimal conversion that could incorrectly reject an exact subnormal input.

Unknown literals read the destination typmod: `'1'` assigned INTERVAL YEAR means
`1 year`. Parameters parse unrestricted first, then apply assignment restrictions:
the same parameter becomes `00:00:00`. Typed literals carry their own modifiers,
for example `INTERVAL '1:02.345' MINUTE TO SECOND(2)`. Prepared literals freeze
Prepare-time input meaning; bound parameters use Execute-time IntervalStyle.
String/textual owned bytes and nil use the existing parameter importer.

Native results are string/nil with INTERVAL/string metadata; numeric DecimalSize
and Length are unavailable, including SECOND(p). pq reports an interface ScanType
instead of native string. Output captures Execute-time style and retained cursors
keep that style through later SET, close, Reset or Restore. Dump/Restore emits
canonical typed interval input with explicitly signed month, day and exact integer
microsecond quantities, independent of either session's style. This uniform encoding
preserves the finite minimum time component through data, defaults and views;
NULL/infinities retain their existing encoding. Interval arithmetic,
timestamp+interval, justify/extract/make_interval and cross-family casts are not
added. See [IntervalStyle](SETTINGS.md#intervalstyle--change-note).
Feature smoke: `make smoke RUN='^TestIntervalSmoke$' VERBOSE=0`.


