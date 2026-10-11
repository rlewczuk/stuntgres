# SQL types and Go values

[README](../README.md) · [SQL dialect and limits](DIALECT.md) · [SQL reference](SQL.md)

Types are int2/int4/int8/oid/name/text/bpchar/varchar/bool/float4/float8/numeric/money/date, time/timestamp with or without time zone, interval, uuid, inet, cidr, bytea, json, bit, varbit, int4range, int8range, numrange, int4multirange, int8multirange and nummultirange. Values returned to Go are `int64`, `string` (including NAME, character types, DATE, TIME, TIMETZ, INTERVAL, exact NUMERIC, monetary MONEY, canonical UUID/INET/CIDR, exact JSON source, canonical BIT/VARBIT digits and canonical ranges/multiranges), `bool`, `float64`, finite TIMESTAMP/TIMESTAMPTZ `time.Time`, owned binary/infinity bytes, or nil. INSERT coerces unknown strings and supported bound values through PostgreSQL-style textual input, except raw BYTEA binds described below; typed integer/bool literals can assign to strings but not each other, and integers can assign to floats. WHERE typed comparisons do not use assignment-only casts. Parameters support nil, integer, floating point, string, bytes, bool and `time.Time` after standard `database/sql` conversion. NULL equality is never true. ASC uses NULLS LAST; DESC uses NULLS FIRST. String ordering uses C-locale bytewise comparison with BPCHAR's ASCII-tail semantics. INSERT is all-or-nothing, including multirow errors and cancellation before publication.

The internal typed foundation uses catalog-owned immutable descriptors (BOOL OID 16,
BYTEA 17, NAME 19, INT8 20, INT2 21, INT4 23, TEXT 25, OID 26, JSON114/array199, CIDR650, FLOAT4 700, FLOAT8 701, INET869, BPCHAR 1042, VARCHAR 1043, DATE 1082, TIME 1083, TIMESTAMP 1114, TIMESTAMPTZ 1184, INTERVAL 1186, TIMETZ 1266, BIT 1560, VARBIT 1562, NUMERIC 1700, UUID 2950, INT4RANGE3904, NUMRANGE3906, INT8RANGE3926, INT4MULTIRANGE4451, NUMMULTIRANGE4532, INT8MULTIRANGE4536; analysis-only UNKNOWN 705), concrete nullable values,
and bound assignment/comparison providers. The parser retains raw type syntax;
the engine resolves it and publishes parameter/result descriptions with column
origins. Prepared types come from destination columns, never the Go argument
kind. Same-type repeated parameters share one inferred slot; gaps fail `42P18`
and mixed-destination reuse remains the bounded native `0A000` limitation,
including mixed INT2/INT4/INT8 reuse. Each execution imports an owned private typed frame
against the current coherent binding. Driver adapters detach bytes and export
OID and INT2/INT4/INT8 as `int64`, NAME/TEXT/UUID/INET/CIDR/BIT/VARBIT and exact NUMERIC as `string`, BOOL as `bool`, FLOAT4/FLOAT8 as `float64`,
DATE/TIME/INTERVAL as canonical `string`, finite TIMESTAMP as `time.Time`, timestamp infinity as fresh
`[]byte("infinity")` / `[]byte("-infinity")`, BYTEA as fresh owned `[]byte`, and typed NULL as nil.
NULL still retains its concrete metadata identity.
MONEY/OID790 is also a distinct stored scalar, exported as monetary `string`;
its associated `_money`/OID791 identity remains lookup-only, not an executable array.


`pg_prepared_xacts.transaction` has a distinct narrow XID descriptor (OID28,
four-byte unsigned storage, database/SQL name XID, category U). Decimal output
exports fresh owned `[]byte`, matching the pinned pq raw representation, not
OID's `int64`. Scalar ownership/equality and grouping retain XID identity.
It has no collation or typmod; its associated `_xid` identity (OID1011) is
lookup-only, not an executable XID array family. This catalog projection does
not enable XID column declarations, casts to OID/regclass/numerics, ordering/
index classes or XID functions. See [prepared transactions](TRANSACTIONS.md#prepared-transactions).

## INET and CIDR — change note

`INET` (OID869) and `CIDR` (OID650) are noncollatable, modifier-free scalar
values with an owned inline address, family and prefix. CREATE/INSERT/SELECT,
ordinary direct/prepared string and textual `[]byte` binds, NULL, stored
defaults/views and the existing native controls use the same codecs.
Typed-nil `[]byte` is SQL NULL; nonnil empty bytes are invalid network input.
Public Go conversion remains `database/sql`'s ordinary converter/Valuer boundary,
not a binary network API or special support for `net.IP`/Go arrays.

INET defaults to /32 for IPv4 and /128 for IPv6, retains host bits and omits
a full-host mask on raw output. CIDR always shows its mask and rejects input
with bits to its right. IPv4 CIDR shorthand uses classful defaults widened to
the number of supplied octets: `10` → `10.0.0.0/8`, `10.1` → `10.1.0.0/16`,
`192.168` → `192.168.0.0/24`; four supplied octets default to /32, including
`10.0.0.0`. IPv6 CIDR without a mask defaults to /128. Explicit-prefix INET
shorthand permits `192.168/23` but rejects `192.168/24`; without a mask all
four IPv4 octets are required. Standalone INET permits a terminal dot after
the final supplied octet (`1.2.3.4.` → `1.2.3.4`, `1.2.3./24` → `1.2.3.0/24`);
it does not supply another octet or relax those mask constraints. Standalone CIDR
rejects that terminal dot. For both INET and CIDR, IPv6 distinguishes EOF from
an explicit mask: `::ffff:1.2.3./128` supplies a zero final octet, yielding
`::ffff:1.2.3.0` for INET and `::ffff:1.2.3.0/128` for CIDR;
`1:2:3:4:5:6:7:8:/128` yields `1:2:3:4:5:6:7:8` and
`1:2:3:4:5:6:7:8/128`, respectively. The same terminal dot/colon without a
mask fails, and excess octets/words, malformed masks and CIDR host bits
remain invalid. Decimal IPv4 leading zeros and CIDR hexadecimal nybble
notation follow PostgreSQL input. Input is not trimmed;
bracketed or zone-qualified addresses, DNS names and malformed address/prefix text fail.
IPv6 output lowercases hex and compresses the first longest zero run, retaining
PostgreSQL-compatible dotted IPv4 tails and the IPv6 family of mapped addresses.

CIDR→INET is implicit; INET→CIDR is assignment/explicit and clears host bits
without changing the source. Thus `'192.168.0.1/24'::cidr` fails, but
`('192.168.0.1/24'::inet)::cidr` yields `192.168.0.0/24`. CAST, `::`, and
plain/`pg_catalog` INET/CIDR typed literals are admitted. Explicit known TEXT
input is supported; explicit network→TEXT always includes the prefix, e.g.
`'192.168.1.5'::inet::text` yields `192.168.1.5/32`, unlike raw INET output.
Assignment to existing character destinations retains their ordinary I/O route.

All six scalar comparisons and ASC/DESC ordering use family first (IPv4 before
IPv6), common network bits, prefix length, then full address. In particular
`10.255.255.255/8` precedes `10.0.0.0/16`. Equality includes prefix and host
bits; either NULL yields UNKNOWN. The existing order-class seam uses released
B-tree `inet_ops` (OID10015, family `network_ops`, input INET), including CIDR
through implicit relabel. This is not a new specialized index method.

Native raw values and ScanType are immutable string/string; pinned pq returns
canonical owned []byte/interface{}. INET/CIDR database type names survive
nonempty, empty and all-NULL results. Length and DecimalSize are unavailable.
Native declared-column nullability is known; computed projections retain the
existing unknown-nullability contract. Pq nullability is unknown.
Invalid text uses22P02 and `invalid input syntax for type inet/cidr: "..."`.
CIDR host-bit failure uses22P02, `invalid cidr value: "..."`, and Detail
`Value has bits set to right of mask.` Eager literals point at the opening
quote in characters, including during Prepare; bound input failures have no
Position and use actual parameter-number Context with nonempty values redacted
as `'...'` (empty values use `''`). Known TEXT conversion fails at Execute
without Position. Native diagnostics never invent PostgreSQL C source fields.

Actual native Dump/Reset/Restore preserves declarations, data, typed defaults
and views, with atomic staging and retained cursor metadata/data. Restore
failures preserve the original catalog. INET[]1041 and CIDR[]651 retain truthful
lookup-only identities but are not executable array types; no network grouping/
hash provider is added. Network helper functions, containment/overlap,
arithmetic/bitwise operators, aggregates and MAC types are separate features.
Physical storage/WAL/durability, wire protocol serving, OS networking, native
ABI and external pg_dump/custom archives are non-engine boundaries, not no-ops.
Coverage: `make check RUN='^TestReferenceINET.*$'` and
`make check REFERENCE=1 RUN='^TestReferenceINET.*$'`.

## Bit strings — change note

`BIT(n)` stores exactly n bits; `BIT VARYING(n)`/`VARBIT(n)` store at most n.
Modifiers are single integer constants from1 through83886080. Bare unquoted,
unqualified `BIT` means `BIT(1)`. Bare `VARBIT` and `BIT VARYING` are unlimited;
quoted `"bit"` and qualified `pg_catalog.bit` without modifiers are unlimited
BIT too. Width is preserved rather than padded or truncated on assignment.
Parentheses around a simple modifier constant retain its value, so `BIT((1))`
has width1. Actual arithmetic modifier expressions are not evaluated: addition,
subtraction, multiplication and division fail42601
`type modifiers must be simple constants or identifiers` at the type start
during CREATE execution, including prepared CREATE.

Adjacent `B'…'` and `X'…'` tokens produce known BIT literals, not unknown text.
Binary and hexadecimal bodies are raw: no string escape processing or doubled
apostrophe escaping. Newline-separated quoted fragments continue a literal,
including line comments but excluding intervening block comments. Hex input
expands each digit to four bits, preserving leading zeroes. Textual assignment
and binds accept binary digits with optional B/b or hexadecimal X/x markers.
Input validates the complete unbounded value before assignment width checks:
bad digits fail22P02, fixed-width mismatch22026, varying overflow22001.
Implicit/assignment BIT↔VARBIT conversion preserves bits and checks the target.

Go exports are canonical binary `string` values, including `""` for an empty
unlimited/varying value; SQL NULL remains nil. BIT and VARBIT metadata retain
distinct database type names, `ScanType` string, and known bit lengths (n or-1
for unlimited), with no decimal precision/scale or collation. Shared reference
raw checks require native strings and non-nil lib/pq bytes before normalization.
Bound byte slices are textual input, never a packed-bit representation, and
executions own their imported data. Typed-nil byte slices bind as SQL NULL for
BIT/VARBIT; nonnil empty byte slices and empty strings bind as nonnull empty
bits, just like `B''`. Held statements and retained rows keep
their typed metadata and value snapshots across catalog replacement.
Repeated parameters assigned to differing VARBIT maxima still check each
destination independently. Held parameterized SELECTs use fresh argument frames;
an earlier native cursor retains its original values and captured metadata.

Native Dump emits canonical `B'…'` values, explicit bounded declarations, and
`"bit"` for unlimited BIT so replay cannot introduce the keyword default.
Stored existing `format` consumers of B/X literals preserve their concrete BIT
identity as canonical `B'…'` operands in defaults and views. Replay does not
manufacture an explicit BIT cast or substitute a precomputed default value.
Dump/Restore preserves leading zeroes, empty values and NULL; failed or canceled
Restore leaves the old store intact. These scalar seams do not enable explicit
bit casts, comparisons/operators/functions, index classes, or BIT/VARBIT array
storage. Mixed-destination parameter reuse retains the native0A000 boundary.
Spaced `b '…'` and `x '…'` are generic typed-literal attempts, not bit
constants. The existing native typed-literal capability guard reports0A000
`typed literals are unsupported`, at the prefix; PostgreSQL instead reports
42704 for the missing type. Both complete diagnostics remain explicitly tested;
bit-constant admission does not expand generic typed-literal providers.

## JSON textual storage — change note

`JSON` is a distinct scalar (OID114, associated `JSON[]` OID199), exported as
an exact source `string`. Input validates syntax without decoding a document:
whitespace, key order, duplicate keys, numeric spelling and escape spelling all
survive. Huge syntactically valid numbers have no native numeric-range limit.
SQL NULL exports nil; JSON `null` is the nonnull text `null`, and `"null"` retains
its quotation marks. Empty nonnil strings/bytes are invalid, while typed-nil
`[]byte` bound to JSON is SQL NULL. Existing domains delegate to this base codec;
generic JSON arrays distinguish whole NULL, NULL elements and JSON-null elements.

Malformed syntax or Unicode escape syntax reports22P02 with PostgreSQL English
Detail and `JSON data, line N: ...` Context. SQL literal Position denotes the
original SQL character location, not a JSON offset; bound errors have no Position
and append the redacted unnamed-portal parameter Context. Raw invalid UTF8/NUL
reports encoding22021 with portal-only bound Context. Syntactically valid
`\u0000`, lone/mispaired surrogates and mixed-case hex escapes are retained;
these are textual JSON storage, not jsonb decoded-Unicode rules.
Physical EOF inside a `\u` escape reports an invalid token; a present nonhex
character instead reports that four hexadecimal digits are required.

Standard CAST, postfix `::` and `json '...'`/`pg_catalog.json '...'` typed
literals use the shared expression binder in table and ordinary no-FROM SELECT.
No per-target JSON or enum witness is required for the local no-FROM unit.
JSON identity, explicit TEXT↔JSON and assignment/explicit JSON↔JSONB IO casts are supported. Numeric/bool SQL
casts do not construct JSON; numeric/bool *bound protocol values* use textual
input instead. JSON is not a character/collatable/comparable/hashable type:
ordinary equality/order/grouping and B-tree keys remain unavailable.
Missing default B-tree classes report42704 with the operator-class Hint, including
direct-column and expression-key reconstruction after ALTER TYPE; failed
reconstruction leaves the column type, rows and index intact. Explicit incompatible
classes retain42804. JSON may be an INCLUDE payload under an otherwise supported
ordered key; payload maintenance and Dump/Restore need no JSON comparator.

Before iteration, JSON metadata reports DatabaseTypeName `JSON`, no Length or
DecimalSize; native ScanType is `string` and declaration nullability is known.
pq exposes raw `[]byte`, ScanType `interface{}` and unknown nullability.
Native Dump/Restore retains exact JSON bytes, typed stored expressions and
captured cursor ownership; invalid late Restore leaves the live catalog intact.
Ordinary SELECT DISTINCT rejects JSON keys, including JSON domain/array keys,
with 42883 because JSON has no equality provider.
No JSON operators/functions, SQL/JSON constructor `json(...)`
or additional pg_type catalog projections are introduced by JSON storage.

Feature smoke (each invocation has a 900-second deadline):
`make check RUN='^TestReferenceJSONFeatureSmoke$'`
and `make check REFERENCE=1 RUN='^TestReferenceJSONFeatureSmoke$'`.
All definitions: use the same commands with `RUN='^TestReferenceJSON'`.

## JSONB normalized storage — change note

`JSONB` (OID3802, associated array OID3807) is a noncollatable immutable
structural value, exported as normalized `string`. Objects sort decoded keys
by UTF8 byte length and then bytes, retain the last duplicate key, and output
comma-space/colon-space separators. Arrays retain order and duplicates; all
scalar roots, including JSON null, are accepted. For example,
` { "z":1,"a":1,"a":2 } ` becomes `{"a": 2, "z": 1}`.
JSON storage still retains its exact original source.

Numbers use exact unconstrained NUMERIC limits (131072 digits before the
decimal, 16383 after), not float64. Exponents render as decimals;
`1.230e-5` becomes `0.00001230`, and `1.00` keeps its display scale.
JSON numeric grammar excludes NaN/Infinity, radix and underscore syntax.
Unicode escapes decode to UTF8, proper surrogate pairs combine, controls are
reescaped, and slash/HTML/nonASCII/U+2028 are not gratuitously escaped.
Every input token is validated even when a later duplicate overwrites it.
Shared JSON/JSONB string scanning polls cancellation by consumed bytes, including
escape and surrogate paths. JSONB object finalization and string rendering also
poll bounded work; canceled input does not publish a partial value.

Syntax diagnostics retain type-json22P02 messages and JSON Context; escaped
U+0000 reports22P05, numeric format overflow22003, and raw invalid UTF8/NUL22021.
Literal positions are original SQL character positions. Parameter import
failures append redacted portal Context; JSON→JSONB expression conversion
failures do not invent that continuation or a bound-source position.

Six ordinary comparisons and B-tree `jsonb_ops` (OID10088) compare structures.
Order is JSON null, string, number, Boolean, array, object, except the historical
top-level empty array sorts before JSON null. Container counts precede recursive
contents; nested empty arrays follow ordinary array order. Numeric equality
ignores display scale, including inside containers; grouping/DISTINCT/UNIQUE
hashes agree with that equality. JSONB[]/domains reuse the existing associations
and generic grouping facilities, not new SQL-array ordering operators.

Typed literals, direct/prepared columns, bound text/bytes, explicit TEXT↔JSONB,
and assignment/explicit JSON↔JSONB IO casts use existing binders/codecs.
SQL NULL exports nil, while JSON null exports `null`. JSONB metadata has no
Length/DecimalSize; native ScanType is string with known declared nullability;
pq raw values are bytes, ScanType any and nullability unknown.
Native Dump/Restore preserves normalized values, defaults and typed NULLs,
replays ordinary SQL, retains old cursors and replaces atomically.
JSON-specific operators/functions, GIN, containment/path/extraction, JSON scalar
constructors and physical PostgreSQL binary/disk persistence remain out of scope.

Feature smoke (900-second deadline per invocation):
`make check RUN='^TestReferenceJSONBFeatureSmoke$'`
and `make check REFERENCE=1 RUN='^TestReferenceJSONBFeatureSmoke$'`.
The same two modes with `RUN='^TestReference(JSON|TypeCore|Numeric|PGDescription)'` cover the
complete affected reference families and migrated source execution routes.

## Integer range columns — change note

`int4range` (OID3904) and `int8range` (OID3926), including qualified
`pg_catalog` declarations, store integer range columns using inline canonical
endpoints and bound flags. Their subtype descriptors belong to the same immutable
catalog generation. Inclusive/exclusive bounds, unbounded endpoints and empty
values use the ordinary destination-context text codec, not SQL constructors.

For either width, `[1,4]` outputs `[1,5)`, `(1,4]` outputs `[2,5)`,
`[4,4]` outputs `[4,5)`, and `(4,5)` outputs `empty`. Infinite bounds lose
inclusivity: `[,]` outputs `(,)`, `[,4]` outputs `(,5)` and `[1,]` outputs
`[1,)`. The nonnull strings `empty` and `(,)` are distinct from SQL NULL,
which exports nil. Finite integer extrema are not infinity; advancing a maximum
endpoint during discrete canonicalization fails instead of wrapping.

Text input accepts surrounding C whitespace, case-insensitive `empty`, quoted
bound fragments and backslash escapes. Decoded finite endpoints reuse integer
input, including signs, radix prefixes and separators. The complete range grammar
is validated before lower then upper subtype input; reversal/equal-bound
emptiness precedes successor adjustment. Malformed grammar gives22P02
`malformed range literal: "..."` with PostgreSQL Detail; malformed/overflowing
endpoints retain integer/bigint input diagnostics. Canonical overflow gives22003
`integer out of range` or `bigint out of range`; reversed finite endpoints
give22000 `range lower bound must be less than or equal to range upper bound`.
Literal errors point to the original SQL quote in characters; parameter errors
have no Position and retain the redacted unnamed-portal Context.
Type modifiers are disallowed with42601, including prepared CREATE execution.

Direct/prepared INSERT binds accept strings and detached nonnil byte slices.
Nil and typed-nil byte slices bind SQL NULL; nonnil empty bytes are malformed
range input, not NULL or an unbounded range. Repeated same-width parameters
share an inferred slot but each execution imports fresh values.
Native output/ScanType is string; pinned lib/pq returns nonnil bytes and
interface{} ScanType. Both report `INT4RANGE`/`INT8RANGE`, with no Length or
DecimalSize; native declared nullability is known, pq nullability is unavailable.
Empty and NULL-only cursors retain these metadata identities.

Native Dump emits canonical quoted text in destination-context INSERT statements.
Already-admitted domains over either integer range base use the same contextual
row replay: the declared destination applies domain input/wrapping to canonical
quoted base text or NULL. This table-row serialization does not grant explicit
range/domain casts or change non-contextual stored-expression serialization.
Reset/Restore and independent Driver restoration preserve values and metadata;
retained readers preserve their original data and column types through replacement.
Failed or canceled Restore does not publish staged data/schema.
No new explicit casts, typed-range-literal syntax, arrays, constructors,
operators/functions or comparison/order/group/hash/index providers
are admitted. Array identities3905/3927 remain lookup-only.
Semantics follow PostgreSQL18; the real oracle remains pinned18.0 UTF8/C.

Selected definitions (900 seconds per invocation):
`make check RUN='^TestReferenceIntegerRange'`
and `make check RUN='^TestReferenceIntegerRange' REFERENCE=1`.
Feature smoke uses `RUN='^TestReferenceIntegerRangeFeatureSmoke$'` in both modes.
Representation/adapter units use `make check RUN='^TestIntegerRange'`.

## Continuous numeric range columns — change note

`numrange` (OID3906, subtype NUMERIC1700), including `pg_catalog.numrange`,
uses the same immutable inline range representation and grammar as integer
ranges, but has no discrete successor canonicalizer. All four finite delimiter
combinations are retained: `(4,5)` stays nonempty and `[4,4]` is a singleton.
Equal numeric magnitudes are empty unless both ends are inclusive:
`[1.0,1.00]` preserves both display scales, while `[1.0,1.00)` is `empty`.
Endpoints use unconstrained exact NUMERIC input/output, not float64 or int64;
`[001.2300,2e1]` outputs `[1.2300,20]`. Range modifiers are not allowed42601.

Omitted endpoints are exclusive unbounded sentinels, not numeric infinities.
`[,]` outputs `(,)`, while `[-inf,+INF]` outputs `[-Infinity,Infinity]`.
Explicit Infinity/-Infinity keep their supplied inclusivity. Numeric endpoint
ordering is `-Infinity < finite < Infinity < NaN`; `[Infinity,NaN]` and
`[NaN,NaN]` are valid, and `[NaN,)` remains bounded below by NaN.
Empty, unbounded and SQL NULL remain distinct. Plain/nested domains over
NUMRANGE use their already-admitted contextual input and native table-row replay.

The common parser validates the entire grammar before lower then upper NUMERIC
input. Grammar errors retain22P02 primary/Detail; invalid numeric endpoints
retain22P02 numeric input messages; numeric format overflow retains22003
`value overflows numeric format`. Reversal is22000
`range lower bound must be less than or equal to range upper bound`.
Literal errors identify the original SQL quote in characters, including eager
prepared-literal rejection; bound errors have no Position and retain redacted
unnamed-portal Context. Execution-time late parameter/NOT NULL failures leave
the complete statement's committed rows unchanged.

Direct/prepared strings and detached nonnil bytes bind as textual range input.
Nil and typed-nil bytes are SQL NULL; nonnil empty bytes are malformed input.
Native NUMRANGE raw output/ScanType are string/string; pinned pq uses
nonnull []byte/interface{}. Length and DecimalSize are unavailable despite
the numeric subtype. Native declared nullability is known; pq reports unknown.
Empty, filtered-empty, NULL-only and retained cursors preserve type metadata.
Actual Dump/Restore, populated same-engine replacement and independent Drivers
preserve exact values/scales and domain declarations; failed/canceled staging
does not publish, and held cursors retain their original shape and rows.

This feature adds no range constructors/operators/functions, explicit casts,
typed literals, comparisons/order/group/hash/index providers, arrays,
or temporal ranges. NUMRANGE[]3907 remains lookup-only.
The release oracle remains PostgreSQL18.0 UTF8/C.
Use `make check RUN='^TestReferenceNumericRange'` and the same command with
`REFERENCE=1`; feature smoke uses `RUN='^TestReferenceNumericRangeFeatureSmoke$'`.
Common representation/adapter units use `RUN='^Test(NumericRange|IntegerRange)'`.

## Multirange columns — change note

`int4multirange`4451/array6150, `int8multirange`4536/array6157 and
`nummultirange`4532/array6151 are generated companions of every currently
implemented range family. Each descriptor points to its own generation's range
and subtype authority. Future owning range tasks supply and exercise their
companion in that same generation path; temporal companions and all multirange
arrays remain lookup-only. Immutable typed storage owns canonical member datums,
not original text. Generation-only Retag retains the immutable payload.

Input uses braces around zero or more comma-separated range members, with C
whitespace, member bound quoting/backslashes and case-insensitive `empty`.
Whole-member array-style quoting and NULL members are invalid. Each completed
member is decoded immediately; malformed unfinished members fail outer parsing
without subtype decoding. Nonempty members sort lower-then-upper, empty members
vanish, and overlap/adjacency merge:
`{[1,3),[2,4),empty,[8,9)}` becomes `{[1,4),[8,9)}`.
Output has no separator spaces. `{}` is a nonnull empty union; `{(,)}` is a
nonnull universal member; SQL NULL exports nil.

Integer members canonicalize to `[)` before merging, with checked successor
overflow. NUMERIC remains continuous: `{(4,5)}` stays nonempty, while integer
companions remove it. Equal touching numeric boundaries merge if either includes
the shared point; `{[1,2),(2,3]}` retains two members. Exact numeric comparison
preserves large fractions, special values and selected endpoint scales.
Released union chooses the current sorted member's boundary on equality, even
under containment; it does not strip scales or promise stable ordering for fully
comparator-equal different-scale duplicates.

Outer syntax errors are22P02 `malformed multirange literal: "INPUT"` with
`Missing left brace.`, `Unexpected end of input.`, `Expected range start.`,
`Expected comma or end of multirange.` or `Junk after closing right brace.`
Completed malformed members retain range primary/member Detail; invalid endpoints
retain integer/bigint/numeric input diagnostics. Completed-member failures precede
later suffix errors. Reversal is22000 `range lower bound must be less than or
equal to range upper bound`; canonical overflow22003 `integer out of range`/
`bigint out of range`, numeric format overflow22003 `value overflows numeric format`.
Direct/Query literal errors point to the SQL opening quote in characters;
prepared literal INSERT fails during Prepare. Bound errors occur at execution,
have no Position and retain redacted `unnamed portal parameter $N = '...'`
Context (empty input uses `''`). Modifiers reject42601 at CREATE execution.

Direct/prepared strings and owned bytes use the destination codec with fresh
parameter frames. Nil/typed-nil bytes are SQL NULL; nonnil empty bytes are malformed
input. Native raw output/ScanType is string/string and DatabaseTypeName is
INT4MULTIRANGE/INT8MULTIRANGE/NUMMULTIRANGE. Pinned lib/pq exposes blank type names,
raw nonnil []byte and any ScanType for these OIDs; public PostgreSQL catalog
identity witnesses independently verify the release OIDs/companion links.
Length/DecimalSize are unavailable; native declared nullability is known and
pq nullability is unavailable, including empty/all-NULL/zero-row cursors.

Actual native Dump/Reset/Restore and fresh Driver replay preserve canonical
values/metadata, empty tables and plain/nested domain declarations. Domain rows
use canonical destination-context text/NULL without explicit casts. Existing
column NOT NULL enforcement23502 remains supported; domain CHECK/NOT NULL/default
definitions remain native0A000 `domain constraints and defaults are unsupported`.
Held populated readers retain original shape/rows through reset and replacement;
failed/canceled staging preserves live declarations/data and deterministic redump.
No constructors/operators/functions, SQL equality/order/group/hash/index
providers, general casts, typed-literal introducers, arrays or user-defined
range creation are added. PostgreSQL18.0 UTF8/C remains the release oracle.
Native scalar comparisons (`=`, `<>`, `<`, `<=`, `>`, `>=`) reject at admission
with42883 `operator does not exist: TYPE OP TYPE`, the ordinary explicit-casts
Hint and source Position, including plain/nested domains with logical type names.
Direct Query returns no cursor; prepared SELECT fails at Prepare, not evaluation.
Representation identity and private normalization comparators do not grant SQL
comparison. The permanent public boundary independently asserts PostgreSQL's
supported unequal/equal/NULL comparison rows and native's excluded-provider error.
Selected dual-mode proof and standalone smoke: [testing](TESTING.md#multirange-selected-proof).



## JSONPath typed input — change note

`JSONPATH` is a concrete noncollatable scalar (OID4072, associated array4073).
The existing codec validates complete PostgreSQL18 input and retains an immutable
syntax tree: mode, exact numbers, variables, accessor chains, index/range
expressions, filters, predicates, methods and regex pattern/flags. Output is cached
canonical text; copying a value or reminting its descriptor shares the parsed
payload rather than reparsing it. SQL NULL exports nil; nonnull path `null`
exports the string `null`.

Input supports default lax/explicit strict mode, scalar/root/variable expressions,
arithmetic and top-level predicates; quoted/unquoted/escaped keys, wildcards,
recursive descent bounds, expression subscripts/ranges and nested filters.
Current-item `@` is allowed inside filters; `last` is limited to array-subscript
descendants. Exact decimals preserve display scale and NUMERIC limits, including
radix integers, separators and exponent input. Comments and decoded ECMAScript-like
escapes canonicalize; surrogate pairs combine, zero escapes and invalid Unicode
fail, and raw quoted controls reescape on output. All released18 methods and their
syntactic argument forms are admitted, not PostgreSQL20 string methods.

LIKE_REGEX validates the PostgreSQL ARE/ERE/BRE syntax at input time, including
directors, embedded options, lookaround, capture/backreference closure, brackets,
classes/collating names and bounded repetition. It does not use Go RE2 or match
anything. Flags canonicalize to `ismxq`; duplicates collapse. All flags validate
before regex compilation; x without q reports0A000, while q admits literal regex
text and retains other flags. Regex numeric escapes follow regex character-code
rules, separately from JSONPath string Unicode decoding.
BRE escaped bounds allow an omitted lower count as zero (`\{\}`, `\{,2\}`,
`\{,\}`); this does not change ARE/ERE's digit-gated bound admission.
Malformed counts and unfinished bounds retain distinct repetition-count and
unbalanced-brace diagnostics through the released numeric token boundary:
accumulation stops at255 with one token of lookahead. Thus unfinished `a{2550`
reports an invalid repetition count without requesting EOF beyond the pending
digit, while `a{255` and `a{256` encounter EOF first and report unbalanced braces.
This applies to lower and upper counts, including BRE and expanded-mode
whitespace/comments; it is not a global count-before-EOF diagnostic priority.
Bracket validation follows the released lexical/
semantic boundary: one lexical token is read after the current named construct
before its name is resolved, without consuming or resolving a following named
construct. Competing lexical and semantic errors therefore retain construction
order rather than a global diagnostic priority.

Standard/postfix CAST, `jsonpath '...'` and `pg_catalog.jsonpath '...'`, columns,
defaults and views use central type IO. Explicit TEXT/VARCHAR/BPCHAR IO casts are
supported; output modifiers truncate/pad by characters using ordinary explicit
character sizing. Typed character assignment to a path column remains rejected,
as do integer/Boolean/JSON/JSONB SQL casts. Protocol scalar parameters instead use
the inferred destination's text input. Held INSERT/UPDATE and CAST parameters infer
4072 before import, including repeated slots. Byte arguments detach once during
normalization; typed-nil bytes are NULL, nonnil empty bytes are invalid input.
Late bad binds publish no writes and held statements remain usable.

Scanner/grammar diagnostics normally report42601; empty input22P02, escaped
zero22P05, numeric overflow22003 and invalid regex2201B retain PostgreSQL English
messages/Detail. Literal Position is the original SQL character position (including
Unicode prefixes), never an inner-path offset. Bind errors have no Position and
use the existing redacted unnamed-portal parameter Context. Raw invalid UTF8/NUL
is22021 with portal-only Context. Input, traversal and rendering check cancellation;
no partial parsed datum is published.

Native DatabaseTypeName is `JSONPATH` (`_JSONPATH` for arrays), ScanType string,
and raw nonnull output string. Pinned lib/pq omits these OID metadata names,
reports interface{} ScanType and returns bytes; shared reference scenarios assert
each backend's metadata independently rather than hide identity with text casts.
PostgreSQL independently supplies scalar/array OID, attribute typmod/collation
and domain-base inquiry witnesses. Native `pg_catalog.pg_type` and
`pg_catalog.pg_attribute` relations remain absent (42P01), despite other
implemented catalog views; exposing JSONPath's concrete4072/4073 descriptors and
typed results does not implement these adjacent inquiry relations. Reference
setup asserts this exact native boundary rather than skipping type behavior,
while provider units retain exact descriptor and reciprocal array identities.
Generic arrays/domains, logical Dump/Restore and retained cursors reuse existing
ownership. There is no typmod, collation, SQL equality/order/group/hash or B-tree
provider. Path evaluation, JSON query functions/operators, SQL/JSON query forms,
GIN and regex matching remain separate and unsupported: valid `1/0` or `.time(99)`
input is not a successful evaluation claim.

Full definitions (900 seconds per invocation):
`make check RUN='^TestReferenceJSONPath'`
and `make check REFERENCE=1 RUN='^TestReferenceJSONPath'`.
Feature smoke uses `RUN='^TestReferenceJSONPathFeatureSmoke$'` in both modes.

## Regclass relation identifiers — change note

`regclass` (OID2205) stores a canonical unsigned 32-bit relation identity.
Direct `regclass`/`regclass[]` columns, ADD COLUMN, scalar/array domains,
INSERT/UPDATE/RETURNING and stored DEFAULT/CHECK/view consumers use the normal
typed execution path. Native `database/sql` values are canonical strings (NULL
is nil), with `REGCLASS`/`_REGCLASS` metadata; domains report their physical base
type. Retained rows preserve their captured catalog, names and metadata.

Input selects the first accessible matching table, view, index or sequence on
the effective relation path. Schema USAGE is required; relation SELECT is not.
Quoted names preserve case/dots/doubled quotes; unquoted names fold and clip at
63 UTF8-safe bytes. A current-database.schema.relation qualifier is accepted;
foreign databases and excessive qualification retain PostgreSQL diagnostics.
Exact digit strings use the existing OID codec (`010` is octal8; `08` is invalid).
`0` and `-` are zero. Signed/padded strings are symbolic, not numeric shorthand.
INT4 casts preserve unsigned bits; INT8 casts check the OID range. Explicit
OID/TEXT/VARCHAR/integer-to-regclass and regclass-to-OID/INT4/INT8/TEXT edges reuse
the bounded coercion matrix; BOOL and NUMERIC are not implicit extensions.
Output uses visible names, otherwise qualified/quoted names; missing identities
display decimal and zero displays `-`.

`ARRAY[...]` retains scalar regclass elements, including typed NULLs and
parameters inferred from explicit array casts or regclass peers, such as
`ARRAY[$1]::regclass[]` and `ARRAY[$1,'pg_class'::regclass]`.
Repeated parameter occurrences share one input contract and fresh execution
values; constructor children are evaluated once in order.
Contextual `ARRAY[]::regclass[]` is nonnull empty;
untyped empty ARRAY retains42P18 and its explicit-cast hint. NULL elements are
not NULL arrays. Nested and other typed constructor families share the array
expression authority described below; existing literal bounds remain supported.

Stored scalar regclass input constants, including existing digit-string input
and scalar leaves under constructors/domain casts, depend on their live relation.
Rename/schema moves retain identity; RESTRICT protects those consumers and
CASCADE removes defaults/CHECKs/views without deleting their owner-table data.
This includes defaults/CHECKs referencing views removed through recursive view
dependencies. RESTRICT details identify each actual provider edge; CASCADE
preserves the surviving owner tables and their existing rows.
The same rule applies when DROP COLUMN removes a view referenced by surviving
definitions on that very table: the remaining data and unrelated definitions
survive, but definitions bound to removed views do not reappear. Diagnostics and
notices follow immediate-provider traversal, with genuine consumer identities
ordering mixed siblings and same-owner CHECKs retaining declaration order.
Replacing an older view with a newer source does not reverse the dependency chain.
Numeric INT/OID coercion expressions and opaque array/domain-array input carry
no relation DROP dependency. Ordinary prepared scalar SQL reanalyzes its original
spelling after relation/path changes; it is not a stored-definition binding.

Logical native Dump/Reset/Restore reowns actual regclass datums recursively and
remaps live scalar input constants. Captured scalar references and actual scalar
datums use the same canonical literal serializer, while captured authority
validation and dependencies remain intact across replay. Closed regclass-result
defaults replay after
relation definitions, including self/mutual references. Existing pure closed
BOOL/TEXT/CASE defaults carrying genuine captured scalar regclass authority can
also defer to break replay cycles; sequence deferral still requires its captured
provider. One per-definition decision governs ordering, CREATE omission and ALTER
emission. Nondeferred defaults, CHECKs and generated expressions retain mandatory
prerequisites, including providers shared with deferred defaults. Opaque array
payloads under an inline BOOL default such as `IS NOT NULL` receive replay-only
ordering without DROP dependencies; without captured scalar authority that BOOL
definition remains inline. Numeric coercion trees keep
their original numbers across different allocators: their result may become
decimal or denote a different destination object. Dangling numeric datums make
no cross-catalog identity promise. Failed/canceled restore preserves the prior
persistent projection; retained source rows are never rewritten. These are
process-local logical controls, not disk durability, network protocol,
PostgreSQL physical ABI or pg_dump-format compatibility.

INSERT and UPDATE CHECK/NOT NULL diagnostics, including UPDATE RETURNING, format
scalar/domain/array regclass values through the statement's captured catalog:
zero is `-`, live identities use visible or qualified/quoted names. Existing
column/table privilege filtering still determines which fields are disclosed.

This change supersedes the historical direct-regclass-storage exclusion in
`docs/design/PG18-INDEX-CLAUSES.md` and the earlier array/string capability notes.
Feature smoke: `make check RUN='^TestReferenceRegclassFeatureSmoke$'`
and the identical command with `REFERENCE=1` (900 seconds per invocation).



## Arrays — change note

Arrays are immutable catalog-owned values over the supported stored scalars:
BOOL, BYTEA, NAME, INT2/INT4/INT8, TEXT/BPCHAR/VARCHAR, OID, FLOAT4/FLOAT8,
NUMERIC, DATE/TIME/TIMESTAMP/TIMESTAMPTZ/TIMETZ/INTERVAL, UUID, JSON, JSONB, REGCLASS and REGTYPE.
Domains over supported scalars or arrays may also be elements; scalar REGCLASS,
REGCLASS[], scalar REGTYPE, REGTYPE[] and their domain wrappers are admitted table-column types.
There is no added executable CHAR, BOX, UNKNOWN, VOID or PG_NODE_TREE array family.
Each represented element has one authoritative associated array ID. User domains
and their automatic arrays publish together, share namespace/owner/ACL authority,
and cannot leave an orphan on failed creation or dependency removal. Generated
names start with `_`, clip on UTF8 boundaries and use collision suffixes; resolving
`schema.domain[]` follows that association, never a guessed underscore name.
Internal IDs, reverse links and column origins do not add driver/catalog APIs.

`INT[]`, `INT[][]`, `INT[2][3]`, `INT ARRAY` and `INT ARRAY[4]` describe the same
array type. Element identity and existing element typmod/collation resolve before
array lookup; declared dimensions/extents do not restrict actual stored shape.
The literal codec supports empty arrays/elements, rectangular dimensions (up to
six), signed lower bounds, quoted and escaped tokens, and whole NULL versus NULL
elements versus quoted `"NULL"`. Only unquoted, unescaped NULL is a null element.
NAME clipping, BYTEA escapes, exact NUMERIC sizing and the other scalar input
rules apply at each element. Canonical examples are `{NULL,"NULL","",alpha}`,
`{{a,b},{c,NULL}}` and `[0:1]={a,b}`. Malformed/ragged arrays retain full22P02
Detail, descending bounds fail2202E, and rank/size limits fail54000. Literal and
parameter diagnostics retain their existing source-position/portal contexts.
Explicit literal dimension prefixes freeze rank and extents before element input;
inferred rank freezes at the first scalar or NULL element. Opening rank overflow
and closed-subarray extent mismatches fail before later element input. A scalar's
own input error still precedes its depth mismatch. Prefix shape errors use
`Specified array dimensions do not match array contents.`; inferred shape errors
use `Multidimensional arrays must have sub-arrays with matching dimensions.`.
Both array input and output procedures are STABLE, even for setting-independent
elements. Explicit TEXT-to-array and array-to-TEXT IO casts, including physical
domain wrappers, therefore cannot survive in immutable stored generation
expressions. Identity coercions and immutable retained payload sharing are unchanged.
Array grouping/key equality, including SELECT DISTINCT, compares rank, dimensions,
lower bounds and recursively typed NULL-aware elements; numeric scale, BPCHAR
padding and floating NaN/signed-zero normalization follow their scalar contracts.
Domains retain logical identity while delegating key equivalence to their bases.
`array_agg` uses these immutable flat payloads rather than nested array values.
Scalar domains retain their actual associated array; domains over arrays select
the base-array overload, while arrays of domains retain element identity.
Concatenation adds a lower1 outer dimension and preserves every inner bound;
rank, extents and bounds must match across input arrays. Whole NULL/empty arrays
are rejected, but element NULLs are retained. Scalar NULLs are collected, and
no admitted inputs produce a typed NULL array.
See [array aggregation](QUERIES.md#array-aggregation--change-note).
Cross-array coercion delegates elementwise to the existing scalar directional
edges in the same Implicit/Assignment/Explicit context, preserving shape and
bounds. This adds no transitive cast graph or new scalar conversion provider.
INT4[] can therefore promote to INT8[] implicitly or on assignment; known TEXT[]
does not become INT4[] by assignment. Explicit TEXT/array IO remains STABLE.

### Array expressions and partial targets

`ARRAY[...]` resolves a common typed element just like CASE, preserving identical
domains and retained occurrence modifiers. All unknown members resolve to TEXT;
empty constructors require an explicit array cast, even in an INT[] destination.
Explicit casts coerce individual members explicitly (including numeric rounding).
Constructor array association uses the coherent catalog's reciprocal element/
array edge, including dynamically published domains and enums, and checks the
exact current descriptor generation. Builtin codec IDs alone are not authority.
Nested shorthand `ARRAY[[1,2],[3,4]]` and array-valued children build rectangular
arrays with matching ranks, extents and inner lower bounds. The new outer lower
bound is one. Domain-over-array children remain scalar domain elements.
All empty/NULL subarrays yield canonical empty; within the rank limit, mixing
them with a nonempty child fails2202E, distinct from a valid scalar NULL cell.
All child expressions are evaluated before construction shape checks. Adopting
the first nonempty child checks the added outer rank against the maximum six
before sibling shape or mixed-empty errors.

Parenthesized general expressions, columns and parameters support element access
and slices. Adjacent brackets are one multidimensional operation; parenthesized
reaccess is another. Bounds use INT4 assignment conversion. NULL or out-of-range
element access, including wrong rank, returns typed NULL. Any colon switches the
whole group to slicing: a plain `[n]` then means `[1:n]`, whereas omitted bounds
use existing dimension limits. Slices fill trailing dimensions, clip overlap,
preserve rank and reset output lower bounds to one. Outside/inverted/excess-rank
fetches return canonical empty; explicit NULL slice bounds return SQL NULL.

Present bounds are analyzed lower then upper within each lexical dimension
before the group rank limit; more than six dimensions never reach execution.

INSERT/UPDATE element and slice targets preserve ordinary atomic constraints.
Partial INSERT starts with typed NULL and does not consume the column DEFAULT.
Repeated partial targets compose in order; UPDATE RHS and bounds see the old
row. NULL assignment bounds fail22004. Empty originals can acquire explicit
coordinates; existing one-dimensional arrays enlarge with NULL gaps, while
existing multidimensional arrays cannot enlarge. Slice RHS shape/bounds do not
restrict the destination: sufficient flattened entries fill row-major, excess
entries are ignored, and insufficient input fails2202E. NULL slice RHS is a no-op
(a NULL original becomes empty). Empty slice assignment requires explicit bounds.
Nonempty originals validate each dimension's inversion/range in order and final
destination shape before checking RHS sufficiency. Empty/NULL originals instead
construct explicit spans: zero spans produce canonical empty arrays; negative
or overflowing spans fail with SQLSTATE `54000`. A zero span cannot hide a later invalid span,
missing boundary or invalid final bound. Empty originals check RHS sufficiency
before final bounds; NULL RHS remains a no-op before these mutation checks.
Repeated whole/mixed whole-partial targets remain errors.
Element enlargement and slice construction reject oversized results before
allocation with `54000: array size exceeds the maximum allowed (134217727)`,
matching the pinned x64 PostgreSQL limit and retaining the original array.

Existing prepared, query, DEFAULT/CHECK/view/generated/index and native logical
control consumers reuse these nodes and dependencies. Array IO remains STABLE;
construction/access over immutable children are immutable. ARRAY(SELECT),
partial MERGE, new array function/operator/comparator providers and new scalar
cast edges remain excluded.

Smoke: `make check RUN='^TestReferenceArrayExpressionFeatureSmoke$'`
and the identical command with `REFERENCE=1` (900 seconds per invocation).


Ordinary INTERVAL DAY/HOUR array input first parses bare `1` as seconds and then
applies the field mask, yielding zero; array elements which are DAY/HOUR domains
apply their domain input mask first, yielding one day/hour. A domain over an
INTERVAL array retains the corresponding literal-versus-parameter distinction.
DATE/TIME/TIMESTAMP/interval/BYTEA element output uses the captured output
environment. Relative temporal literals bind to statement/transaction time;
REGCLASS elements resolve under one captured catalog/search-path/authorization
view and retain canonical numeric identities. Retained results format names from
their old captured catalog, not a later live provider.

Native array DatabaseTypeName is the actual uppercase catalog name (`_INT4`,
`_NUMERIC`, or a collision-resolved user-domain array name); ScanType/raw values
are string. Pinned pq independently uses interface{} ScanType and owned []byte
raw values, with an empty DatabaseTypeName for dynamic user-domain array OIDs.
Array Length is unknown even for character elements. Sized NUMERIC[] reports the
element precision/scale; unsized native NUMERIC[] reports unknown while pinned pq
reports its independent65535/65531 result. Empty/all-NULL results keep the same
metadata. String/byte binds represent array literal text; nil is whole typed NULL.

Native Dump writes qualified typed array literals using stable temporal/BYTEA
settings and stable REGCLASS identity presentation, independent of live output
settings. Restore reowns nested domains/arrays and REGCLASS leaves explicitly;
source payloads and retained eager rows are not rewritten.
Stored-expression deparsing and Dump/Restore preserve the logical name of a domain
over an array, including domain chains, rather than rewriting its cast as the
underlying element array type.
Persistent replacement preserves an existing internal TEMP builtin-array descriptor,
storage generation, row/version identities and its exact builtin dependency
generations; retained rows are not reminted or mutated as imported persistent rows.
It atomically rejects incompatible live internal TEMP user-domain array dependencies
with the ordinary error
`stuntgres: catalog replacement with incompatible live temporary user-domain array dependencies is unsupported`;
it never retains old persistent domains or retargets by name/OID coincidence.
Automatically associated arrays remain internal lifetime dependencies: DROP traverses
them to real dependent objects without exposing generated-array or blank labels in
domain/schema dependency diagnostics. Stored defaults and constraints retain their
actual definition-allocation chronology.
Public TEMP-table creation is still unsupported; TEMP sequences are unchanged.

## Composites — change note

`CREATE TYPE [schema.]name AS (member type [COLLATE collation], ...)` creates
a distinct named composite, including zero members. Prepare is quiet; execution
atomically publishes its type, reciprocal nonstorage backing relation, stable
attributes and lookup-only associated array. Declarations follow existing
identifier clipping, search_path, collision, type/typmod and collation rules.
Duplicate members, unsupported child types and table constraint syntax fail
without partial publication. Builtin/catalog row identities remain lookup-only.

Supported user tables and views expose the same concrete row codec. Members
may be existing supported scalars, plain domains, composites and executable
scalar arrays. Plain domains over composites retain their own logical identity.
Composite arrays and arrays of composite domains remain nonexecutable, even
for NULL values. Existing domain DEFAULT/CHECK/NOT NULL exclusions are unchanged.

Unknown literals and inferred parameters accept PostgreSQL record text: outer
parentheses, comma-separated members, quoting/doubled quotes/backslashes, and
empty unquoted fields as typed NULL. `()` is a nonNULL empty zero-member record;
an empty record field is not an empty TEXT string. Nested values use their child
codec recursively; errors preserve immediate child-conversion versus arity
precedence. Output quotes/doubles record delimiters and whitespace and passes
the statement's captured catalog, date/time/interval and bytea presentation to
children. Structural errors retain original decoded input and exact Detail.
Child failures are not replaced with generic record errors.
Nested enum failures use the input statement's captured catalog and current
search_path: a visible enum is unqualified, while an invisible or shadowed enum
retains its schema qualification, including through plain domains and deeper
records. This does not mutate type descriptors or change child admission.
Already-admitted retained defaults and generation plans use the consuming
statement's naming capture for recursive TEXT input, rather than declaration-time
visibility. A record computed by a default has neither literal Position nor
parameter Context, even when another column's integer parameter is bound.
For a zero-member record, an input consisting only of `(` fails with `22P02`
and `Too many columns.` Detail; EOF while scanning a real member instead
retains `Unexpected end of input.`.

Explicit TEXT-to-composite and composite-to-TEXT IO casts are supported.
Typed TEXT does not implicitly assign to a composite. Existing composite-to-
character assignment uses ordinary destination length/typmod behavior.
Distinct equal-shaped named types are never structurally interchangeable.
Physical source CHECK/NOT NULL constraints do not constrain row values stored
outside that table or inside a named wrapper.
Existing CREATE TABLE CHECK constraints supply this standalone-row witness.
ALTER TABLE ADD CHECK remains unsupported natively; composite execution does
not add that adjacent DDL capability.

Stable attributes project current values across supported source ADD COLUMN
(new members become NULL), DROP COLUMN and RENAME COLUMN; source/view renames
and namespace moves preserve identity. Type changes, including same-type
changes, and nonNULL ADD defaults that rewrite a stored row type fail with
the actual stored consumer's PostgreSQL0A000 diagnostic. Existing dependency
RESTRICT/CASCADE traversals include named members: CASCADE drops the dependent
member, not its containing named type or holder table. A surviving zero-member
value renders `()`. Held eager results keep their old immutable shape, while
current statements/prepared executions use the new generation.

Native exports canonical strings (nil for NULL), string ScanType, empty
DatabaseTypeName, no Length/DecimalSize and known declaration nullability.
Pinned pq exposes raw []byte, interface{} ScanType and unknown Nullable.
Literal diagnostics carry source Position; bound decoded inputs use the existing
opaque `unnamed portal parameter $n = '...'` Context (`''` for empty input).
Native Dump orders scalar declarations, named composites, domains, tables and
views through one captured provider topology. Restore recursively reowns types,
members, nested values and catalog-valued children without mutating old cursors.
Failed/canceled restore remains atomic; old prepared identities cannot retarget
by name after lifetime replacement.

Standalone composites additionally support [typed-table attachment and attribute
lifecycle](TABLES.md#typed-tables--change-note), including grouped DROP TYPE.
ROW constructors, field extraction/expansion/assignment, comparisons/hashing/key
operations, enum/base-type DROP and other unsupported type lifecycle remain excluded.
Recognized `DROP TYPE` targeting an enum or base type retains native0A000
`statement is unsupported` at the statement's character position; accepting the
bounded composite DROP syntax does not enable enum/base-type DROP.
Verification selectors (900 seconds per invocation):
`make check RUN='^TestReferenceComposite'`
and `make check REFERENCE=1 RUN='^TestReferenceComposite'`.

## Enums — change note

`CREATE TYPE [schema.]name AS ENUM ('sad','ok','happy')` publishes an immutable
catalog-owned scalar with a distinct logical ID, namespace, owner and default
PUBLIC USAGE. Prepare is quiet; only execution publishes the declaration.
TYPE and ENUM remain unreserved identifiers; enum declaration parsing preserves
the existing `CREATE DOMAIN` syntax, including domains over enum types, and
the contextual `ALTER COLUMN TYPE` / `SET DATA TYPE` spellings. `COMMENT ON
TYPE` retains its existing unsupported-object diagnostic rather than a syntax
error. Domain storage in enum CHECK/view predicates can use an explicit cast
to the base enum, as in `v::schema.mood >= 'ok'::schema.mood`.
Enum comparison operators require actual enum operands of the same enum type.
A domain over an enum is not a polymorphic enum operand, even when both
operands have that same domain type; cast explicitly to the base enum.
Domain-over-enum ORDER BY, GREATEST and LEAST still use the base enum's
declaration order and retain the logical domain identity.
The same GREATEST/LEAST ordering authority applies to stored table DEFAULTs,
CHECK expressions and views, including after native Dump/Restore. NULL operands
are ignored; an all-NULL extrema expression remains a typed domain NULL.
For `mood AS ENUM ('z','a')` and `wrapped AS mood`, stored
`GREATEST('z'::wrapped,'a'::wrapped)` yields `a`, while LEAST yields `z`;
scalar comparison operators still require the explicit base-enum cast above.
Labels preserve case, whitespace, quotes, backslashes and the empty string.
The list may be empty (then only NULL is representable). Labels must fit 63 UTF8
bytes; longer declarations fail42602. Duplicate labels fail23505 with the
PostgreSQL pg_enum unique-constraint diagnostic; failed declarations publish nothing.

Equality uses stable member identity; all six ordinary comparisons and ORDER BY
use the current declaration order, not label spelling, numeric identity or
insertion time. Different enum types fail42883 even with
shared labels, NULL or empty sources. Cast both operands to TEXT for lexical
comparison. Unknown literals and inferred string/byte binds use exact enum input;
invalid labels fail22P02 with positioned literals or unnamed portal parameter
Context for bound values. Typed TEXT cannot implicitly assign or compare as enum;
explicit enum↔TEXT casts are supported. Modifiers and COLLATE are rejected.
Domains over enum retain their immediate/physical enum authority.
DISTINCT keys use checked member identities for enums and domains
over enums, with NULL-equal grouping semantics and strict descriptor generations.
Recursive keys also support admitted projected arrays of domain-over-enum
elements and domains over those arrays. Native enum-array column storage remains
excluded; projected literal/annotation codecs are not storage admission, and
ordinary array comparison/ordering and polymorphic enum operators are unchanged.

`enum_first(anyenum)` and `enum_last(anyenum)` return the same actual enum type,
ignoring the datum (including typed NULL). `enum_range(anyenum)` returns every
label; `enum_range(anyenum, anyenum)` returns the inclusive declaration-ordered
interval. NULL endpoints select the first/last boundary; reversed intervals and
empty enums produce a non-NULL empty array. Empty-enum extrema fail55000.
These non-strict functions are STABLE and still evaluate demanded arguments.
Surviving calls are not immutable generation/index expressions or index
predicates (42P17); an eliminated conditional branch does not survive admission.
Unknown peers infer from an actual enum; domains and different enums fail42883.
The range result is an actual computed array with the enum's associated catalog
identity and canonical array output, not TEXT. Native array metadata reports its
uppercase associated name and string ScanType. This does not admit direct enum[]
annotations, casts, columns or domain bases; existing domain-over-enum projections
remain supported. Ordinary computed-array consumers retain their own rules.
Computed ranges and ordinary views remain supported, but physical destinations
derived by CREATE TABLE AS or CREATE MATERIALIZED VIEW reject direct enum-array
elements with0A000 `unsupported array element type`, for both WITH DATA and WITH
NO DATA. Outer domain/array wrappers do not bypass that boundary. Scalar enum
query-derived storage, domain-over-enum element projections and supported
non-enum arrays are unchanged. PostgreSQL supports enum-array annotations,
domain bases and physical storage; these are intentional native exclusions,
not asserted PostgreSQL errors.

Standalone SELECT uses the ordinary local input for enum casts, their admitted
comparisons, Boolean predicates and explicit TEXT output casts. Other scalar
targets do not require an enum witness; type/provider rules remain independent
of source admission. Missing cast types/schemas and enum modifier errors keep
normal binder diagnostics rather than a generic unsupported-query error.

Native Go values and ScanType are string (nil for NULL); DatabaseTypeName is
empty, Length/DecimalSize unknown. Stored nullable-column metadata is true/known.
Native declared view columns retain their nullable/known metadata, even when
their source is a computed enum support call; standalone computed results
do not acquire that stored-column nullability contract.
Pinned pq uses raw []byte/interface{} and unknown Nullable for user enum OIDs.
These transport differences do not erase the logical engine enum ID.
Held prepared parameters/results cannot retarget same-name types after native
replacement; fresh prepares are required. Eager rows retain old values/metadata.
Within the same lifetime, prepared parameter codecs refresh from the admitted
catalog (including private enum/domain/composite generations), while their
logical input contracts remain immutable.
Schema rename updates stored dependencies and rendering; an old explicitly
qualified prepared source is freshly resolved and can fail3F000.

Dump/Restore emits qualified enum declarations before dependent domains/tables,
preserving label order, canonical escaping, owner remapping and stored defaults,
CHECKs and views. Changed type ACLs are refused rather than silently discarded.
Restore remains atomic on parse/input/owner/read/cancellation failures.
`ALTER TYPE [schema.]name ADD VALUE [IF NOT EXISTS] 'label' [BEFORE|AFTER 'neighbor']`
and `ALTER TYPE [schema.]name RENAME VALUE 'old' TO 'new'` are supported.
Prepare is effect-free; Exec/Query completion performs the owner-checked mutation.
ADD appends by default, or inserts at the exact neighbor; RENAME preserves rank
and member identity. Existing rows, nested domain/composite values, stored
DEFAULT/CHECK/view literals and successfully analyzed prepared enum literals
retain their member after rename. Fresh textual input and bound parameters use
current labels. A successful IF NOT EXISTS duplicate emits notice42710 and
skips neighbor resolution; it does not create an unsafe member.

CREATE TYPE AS ENUM and these two ALTER forms are transaction/savepoint-aware.
An ADD to an existing type is unsafe until top-level commit: demanded literal,
cast, bind, nested input and enum support-function producers fail55P04, with
`New enum values must be committed before they can be used.` as Hint.
Support functions check only members they actually produce; old members,
bounded safe intervals and empty reversed intervals remain usable.
Every operation captures current unsafe-member provenance from its admitted
catalog, independently of freshly bound enum inputs. Retained DEFAULT/CHECK
producers, nested stored views and qualifications, lowered view writes, routed
physical leaf CHECKs and MERGE share that environment, including integer-only
writes. Capture does not evaluate a producer: supplied values, untaken branches,
zero-row sources and unused arms remain undemanded. Held prepared consumers
recapture safety on every execution; commit and rollback retire unsafe roots.
A WHERE input proven empty by a FALSE/NULL literal or this execution's BOOL
parameter does not pull a nested view projection unless its child has a
reachable mandatory physical input frontier. Native consuming owners retain
that frontier for actual semantic rowmarks; unrelated semantic context,
identity-only cursor manifests and unopened opaque CTE bodies do not require
it. Proven-empty user predicates also avoid lowered view qualifications.
Arbitrary user work still follows the required qualifications.
The CREATE-and-ADD exemption applies only when both occur at the transaction's
outermost level. Child-created types and child-added labels are not upgraded by
RELEASE; ROLLBACK TO restores exact catalog/member provenance. Other utility
transaction exclusions remain unchanged.
Mutation errors/notices and input/unsafe diagnostics surround original label
bytes with double quotes without Go-style escaping; backslashes, embedded
quotes and supported control characters retain their exact diagnostic text.
The 63-byte new-label limit is checked after object/kind/owner resolution but
before requesting the type lock, so invalid ADD/RENAME does not turn into a
lock timeout. Mutable duplicate/old/neighbor decisions and authorization still
refresh after grant.

Peers retain committed labels until publication. ADD/RENAME serialize on the
actual catalog type resource; waits retain lock_timeout, deadlock, cancellation
and transaction/savepoint release behavior. Generation replacement retains row,
column, type and associated-array identities while rebuilding dependent codecs.
A two-type lock cycle rejects one victim with40P01 `deadlock detected` and the
server-log Hint. The failed transaction remains25P02 until rollback; survivor
commit publishes only its own changes, and both sessions can reuse both type
resources after terminal cleanup.
Dump emits only committed current labels. Restore imports allocate new member
identities and explicitly remap nested values and stored literals, including when
source/destination type OIDs coincide. Ordinary same-lifetime retagging cannot
use that import authority. Owner/read/input/cancellation failures remain atomic.
DROP TYPE, other ALTER TYPE forms, pg_enum queries and general direct enum[]
syntax/storage remain excluded.
Named AS (...) composites are documented separately above. Public verification:
`make check RUN='^TestReferenceEnum.*$'`
and `make check REFERENCE=1 RUN='^TestReferenceEnum.*$'`.
Focused stored-extrema verification uses
`make check RUN='^TestReferenceEnumStoredExtrema$'`
and `make check REFERENCE=1 RUN='^TestReferenceEnumStoredExtrema$'`.

## Domains — change note

Domains are immutable catalog-owned logical types, not aliases or per-value boxed
objects. `CREATE DOMAIN [schema.]name [AS] base_type [COLLATE collation] [NULL]` accepts supported
stored scalar bases, another domain, supported arrays and executable composites. Immediate-base identity,
inherited typmod/collation, namespace, owner and distinct type ID remain represented.
VOID, UNKNOWN and RECORD are not valid bases. Domain DEFAULT/CHECK/NOT NULL and
ALTER DOMAIN are excluded; omitted values and bound nil remain typed NULL.
Table-only UNIQUE clauses, including named and NULLS variants, fail at execution
with42601, `unique constraints not possible for domains`, at the constraint
opening token. Preparing the declaration still succeeds; failed creation publishes
no domain and never silently discards the clause. Ordinary table UNIQUE and
explicit domain NULL declarations remain supported.

A new coercion to a domain with nondefault declared collation supplies that
declaration with implicit strength, replacing both child implicit conflict and
explicit authority nested inside concat/CASE. Top-level COLLATE annotations
are kept outside the domain coercion, preserving their genuine identity and
diagnostic location. A default-collation domain bubbles the child's authority,
including explicit selection or indeterminacy. Same-domain identity relabels
do not create a new coercion boundary. Child evaluation properties, effects,
dependencies and native remapping remain intact.

Physical column assignment is a separate outer destination relabel. Defaults,
both analyzed and planned generation expressions, DML and reconstructed defaults
publish the exact immutable column Usage after preserving the semantic domain
boundary and any top annotation inside it. Retagging maps this captured Usage
and existing structure rather than deriving it again from the child. The same
rule preserves formal-argument destination ownership, including encoding-name
conversions; it does not borrow a child descriptor or historical catalog.

Assignment/input validates the ultimate base typmod: numeric(5,2) `12.345` becomes
`12.35`; varchar(3) rejects assignment `abcd` with22001, while an explicit domain
cast truncates to `abc`. Modifiers on a domain name itself fail42601.
Unknown string literals are parsed at the unsized physical base during binding;
destination sizing is enforced by execution coercion rather than eager domain
input. INTERVAL is field-sensitive: literal casts and inferred bound parameters
retain the effective base field mask and precision during input, including domain
chains. Bare `1` means one day for INTERVAL DAY, one hour for INTERVAL HOUR and
one month for INTERVAL MONTH, not one second followed by field truncation.
Qualified domain names retain their catalog identity even when the last
identifier is a builtin spelling.
Supported operators/functions receive base values and return their ordinary
builtin result types. Explicit cast/assignment-back restores domain identity.
All known same-domain conditional/common-type inputs retain their logical type;
mixed builtin/distinct-domain inputs flatten. Type dependencies survive that
downcast, including stored defaults/checks/views.

Driver values and DatabaseTypeName/ScanType/Length/DecimalSize describe the ultimate
base, including empty/all-NULL results. Integer-domain rows use native int64;
numeric(5,2) uses string and precision5/scale2; varchar(3) uses string and length3;
array bases use their actual associated array name/string. Pinned lib/pq independently
uses int32 ScanType for INT4 and interface{} ScanType/raw []byte for NUMERIC/arrays.
Native nullable declarations
are known true, unlike pq's unavailable Nullable metadata.
REGCLASS-domain result spelling is captured from current catalog authority before
returning rows, for source-backed projections and materialized outputs alike;
row export does not fall back to a later live catalog.

Custom schema USAGE-only ACL SQL probes are explicit PostgreSQL controls: there
is no native public schema-ACL mutation fixture. Native private catalog units
exercise actual ACL filtering and first-usable-schema CREATE denial without
inventing a public GRANT path; ordinary paths and supported array storage remain dual-mode.

Native Dump/Restore preserves qualified declarations, chains, base sizing, owners,
arrays, NULL and dependent persistent expressions. Changed domain ACLs are rejected
by Dump rather than silently discarded; native type ACL-management SQL is excluded.
Fresh prepared reads rebind restored authority; old prepared logical parameter or
result signatures are immutable and may reject reminted identities with0A000.
Retained eager rows keep their owned values and analyzed metadata. This does not
remap stale parameter IDs by name or promise PostgreSQL's old-OID cache diagnostics.

The predefined `information_schema.time_stamp` domain is executable over
`pg_catalog.timestamptz` with effective fractional precision **2**. Its logical
catalog identity remains distinct from the base; finite supplied values round
to hundredths of a second and retain their instant, not their original zone.
Direct/prepared string and `time.Time` input, explicit SQL NULL and bound nil use
the existing domain/base paths. Result metadata is `TIMESTAMPTZ`, ScanType
`time.Time`, with no Length or numeric DecimalSize; temporal precision is not a
numeric scale. Native nullable metadata is known true; pinned pq reports it unknown.
Qualified native Dump/Restore preserves this builtin without emitting CREATE DOMAIN.
Its associated `_time_stamp` array and the other information-schema domains
remain lookup-only; this does not add general information-schema catalog views.

PostgreSQL defines this domain with `DEFAULT current_timestamp(2)`. Native domain
defaults are deliberately not implemented: an omitted/DEFAULT column uses its
explicit column default, or typed NULL if none exists. Explicit NULL/bound nil
stay NULL in both backends. Supplied-value support is not inherited-default parity.
Shared REGTYPE witnesses cover domain/base names; PostgreSQL-only catalog controls
cover the official domain definition, precision and default deparse.

For BYTEA domains, shared binary-buffer binds use `$1::bytea` before assignment:
pq's inferred domain parameter can reject embedded NUL with22021, while native
inferred BYTEA-domain []byte input follows its owned binary adapter.

The catalog-visibility source boundary keeps exact CREATE DOMAIN d AS integer
as a supported Query/empty-utility witness, then checks DROP, effect-free Prepare,
missing-type42704/Position12 before SAME held Execute/count0 and typed7/true
INT4/BOOL readback. Existing other DDL/default-function exclusions remain.
This changes the historical boundary expectation under incoming domain support,
not the bounded domain contract above or the supported ordinary simple-key
UNIQUE-index contract. Concurrent computed standalone UNIQUE uses the shared
tuple provider and enforcement without widening ordinary provider admission.



## VOID selected function result

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
cast binder in ordinary table and no-FROM SELECT. The local input does not add
unrelated cast families.
Typed INT8 -1 fails, unlike text `'-1'` or an OID-inferred Go int64(-1) bind.
String/textual bytes/Valuer/nil use the existing owned parameter path.
Native Go uint64 above MaxInt64 remains an ordinary adapter conversion error;
pq accepts the argument and PostgreSQL rejects out-of-range OID input with22003.

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

Feature smoke: `make check RUN='^TestOIDSmoke$'`.
Native: `make check RUN='^TestReferenceOID.*$'`.
Providers: `make check RUN='^TestOID.*$'`.
Pinned reference: `make check REFERENCE=1 RUN='^TestReferenceOID.*$'`.

### Cast catalog metadata — change note

`pg_catalog.pg_cast` projects metadata declared by the existing type/coercion
implementation owner, not a second graph of all PostgreSQL casts. Exactly65
complete-context existing edges expose source/target OIDs, fixed function OIDs,
context i/a/e and method f/b/i, plus stable local generated row identities.
Function identities are metadata references; this does not expose pg_proc or
execute an arbitrary function by OID. Binary and I/O methods use function0.
Catalog methods need not equal native execution opcodes: INT4/OID signed
conversion is still necessary despite binary metadata, and identity sizing
implements genuine same-type typmod function rows.

Successful automatic scalar/string I/O or domain/base conversion does not imply
a catalog row; ordinary INT4/TEXT/BOOL self identities are omitted. Partial
NAME/REGCLASS/BIT/VARBIT/internal-CHAR/PG_NODE_TREE and absent JSONB-scalar or
TIME-INTERVAL providers cannot advertise complete PostgreSQL context closure.
This metadata addition changes no coercion admission or conversion execution.
All four numeric fields are OID, not INTEGER; both character fields are internal
CHAR, not SQL CHAR(n). Native OID/CHAR ScanType is int64/string; pinned pq reports
interface{} for both despite its raw decimal-byte/string codecs.
See [exact65 tuples and owner/control contract](design/PG18-CAT-PG-CAST.md).

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

### Minimum and maximum overloads — change note

min/max have exact scalar overloads for INT2, INT4, INT8, OID, FLOAT4, FLOAT8,
NUMERIC, TEXT, BPCHAR, BYTEA, DATE, TIME, TIMETZ, TIMESTAMP, TIMESTAMPTZ and
INTERVAL. Result type is the selected overload, not necessarily the original
logical input: VARCHAR/NAME/internal CHAR select TEXT; REGCLASS selects OID;
domains flatten through their base signatures. Unknown strings, NULL and untyped
parameters select TEXT. Internal CHAR uses the existing type-owned printable
TEXT conversion (zero→empty, high bytes→octal), without a public cast edge.

The aggregate retains a datum directly and reuses ordinary Comparison semantics:
exact NUMERIC arithmetic, unsigned OID, raw BYTEA lexicographic bytes, BPCHAR
ASCII-space trimming, floating NaN above infinity, datetime sentinels, TIMETZ
UTC-clock/zone tiebreak and INTERVAL physical-duration comparison. NULL never
enters the comparator. Empty/all-NULL input yields typed NULL. Comparison begins
with the second non-NULL candidate, including demand-time42P22 on indeterminate
string collation; default TEXT collation applies to converted internal CHAR.
Result typmod is unrestricted, but the selected datum retains input-applied
precision/scale/padding and its resolved collation. Native nullable metadata is
known true with no physical origin; native NUMERIC DecimalSize is unavailable,
and unrestricted character Length follows its existing contract. lib/pq's own
ScanType/Length/DecimalSize quirks are independently asserted, not imposed on native.

BOOL and UUID have ordinary comparison but no min/max overload (42883).
PostgreSQL accepts NAME[] and PG_NODE_TREE through its array/TEXT signatures;
native explicitly rejects both with positioned0A000 because array ordering is
not implemented and node trees use private serialization. No general cast or
array/tree comparison is introduced. Unsupported explicit casts to TEXT remain
excluded; raw successful min/max output is independently covered. The separately
supported [INTERVAL→TEXT output edge](DIALECT.md#date-and-time-arithmetic--change-note)
also consumes INTERVAL min/max results, preserving their retained components
and captured IntervalStyle.



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
and the offending hexadecimal bytes. Bound encoding errors have no Position and
carry `unnamed portal parameter $1` without a value suffix in both adapters.
String, textual `[]byte` and Valuer binds share the existing owned-input boundary;
changing the source buffer cannot change rows. Nil interface is SQL NULL.
Native typed nil `[]byte` is an empty NAME value; pq instead sends SQL NULL.
A nonnil empty byte slice remains nonnull empty NAME in both adapters.
SQL TRUE/FALSE assignment uses boolout `t`/`f`, while inferred NAME Go bool binds
encode `true`/`false`.
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

PostgreSQL's physical64-byte padded representation, binary
`namerecv`/`namesend`, unregistered locale providers and wire serving are external-only.
Bounded NAME[] textual input/storage is described in the domain section above;
general array operations are not implied.
Binary NAME receive rejects64 bytes with42622 `identifier too long`, rather
than text input's clipping; native ordinary byte binds are textual, not binary.
Native persistence here is public in-memory logical Dump/Restore, not disk/WAL
durability or PostgreSQL backup tools.

Feature smoke: `make check RUN='^TestNameSmoke$'`.
Native coverage: `make check RUN='^TestReferenceName'`;
codec coverage: `make check RUN='^TestName'`;
pinned reference: `make check REFERENCE=1 RUN='^TestReferenceName'`.


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
empty/NULL-only results and after EOF/Close. Native physical-column and stored-view
column Nullable reflects the declaration; computed BYTEA nullability is unknown.
Native Length/DecimalSize are unavailable; pinned pq reports Length MaxInt64 and
unknown nullability. Native empty BYTEA exports nonnil []byte; pq's escape
decoder exports typed nil []byte for empty, while its hex decoder exports
nonnil empty. Both differ from SQL NULL's nil interface. Raw BYTEA binds admit
NUL and invalid UTF8 octets; SQL string decoding still rejects lexical
NUL/invalid UTF8 before BYTEA input processing.
Bytewise unsigned lexicographic comparison places shorter prefixes first.

BYTEA access uses raw octets: `bit_count(bytea)` returns INT8; `get_bit(bytea,bigint)`
and `get_byte(bytea,integer)` return INT4; `set_bit(bytea,bigint,integer)` and
`set_byte(bytea,integer,integer)` return BYTEA. Zero-based bit n addresses byte
n/8 with mask 1<<(n%8), least-significant bit first. Byte getters return 0..255.
Setters preserve the source; changed results own one new buffer, unchanged
results may share immutable storage. `set_bit` requires exactly 0/1 after bounds
validation; `set_byte` retains low eight bits, including negative INT4 values.
All are strict and immutable; `reverse(bytea)` reverses octets, not characters.
INT2/INT4 bit offsets widen to INT8; INT2 byte offsets/new values widen to INT4,
but INT8 does not implicitly narrow. Unknown-source BIT overload competition
does not add BIT execution. See [access boundaries](DIALECT.md#bytea-bit-and-byte-access--change-note).
Fresh access projections have unknown Nullable; reading the same results from
stored view columns exposes native nullable declarations `(true,true)`, including
before iteration and after Reset/Restore on retained cursors. Pinned pq cannot
expose column nullability and retains `(false,false)` in both cases.


`bytea_output` defaults to `hex`; `escape` doubles backslashes and octal-escapes
bytes below 32 or above 126. SHOW/SET/RESET/DEFAULT and transaction-local unwind
use the existing settings lifecycle. NOT NULL failing-row Detail genuinely uses
the statement-captured mode; raw SELECT bytes remain invariant under the setting.
Dump always writes quoted lowercase hex, including `'\x'` for empty and NULL
for null, independent of output/string settings. Restore privately parses
default-on strings, preserves live settings and retained snapshots, and
publishes no replacement on input/read/cancellation failure.
Explicit BYTEA→TEXT (`v::text`, `CAST(v AS text)`, `text(v)`) uses this same
statement-captured output environment, returning TEXT/string: empty hex is
`\x`, empty escape is empty TEXT, and NULL remains typed TEXT NULL. Held column
and bound-parameter casts use the current execution mode. Existing immutable
output classification and constant folding remain unchanged; a held constant
cast is not a mode-refresh promise. No implicit or reverse TEXT→BYTEA edge is
added; existing assignment conversion is unchanged. Generic BYTEA arrays retain
their established element codec/environment authority. BYTEA typed literals,
modifiers and wire serving are not added.

Migrated feature witnesses: `make check RUN='^TestReferenceByteaSmokeMigration$'`.
Dual-mode native witnesses: `make check RUN='^TestReferenceBytea[A-Za-z0-9_]*$'`;
local adapter ownership: `make check RUN='^TestByteaAdapterOwnership$'`;
codec witnesses: `make check RUN='^TestBytea.*$'`.
Pinned reference: `make check REFERENCE=1 RUN='^TestReferenceBytea[A-Za-z0-9_]*$'`.
Binary scalar/index and text-provider assertion unions additionally run through
`make check RUN='^TestReference(BinaryBasic|BinarySlice|BtreeIndex|StringCore)[A-Za-z0-9_]*$'`
and the same RUN with `make check REFERENCE=1`. Full labels, raw nonnil/empty BYTEA,
descriptor flags and real consumer/control phases remain independently asserted.
Source-consumer companions retain actual original DB/Tx/Stmt and raw native
producer methods, complete NULL/empty/owned binary-buffer behavior and descriptor
flags beside recorded Context streams. Check specialized typed Scan, Length,
Nullable and DecimalSize on the same actual cursor; a normalized row comparison
alone does not establish those metadata or buffer-ownership assertions.

The result-only PostgreSQL array-concatenation boundary is directional:
`'x'::text||current_schemas(false)` returns `_TEXT` / `{x,public}`, while
`current_schemas(false)||'x'::text` returns `_NAME` / `{public,x}`. Both use nonnil
pq bytes and interface ScanType. Native rejects BOTH directions with its full
42883 direct/Prepare diagnostic; only standalone current_schemas returns native
`_NAME`. PostgreSQL-only array success adds no native concatenation capability.

BYTEA checksums and hashes use these unary raw-byte signatures:

| Signature | SQL result | Representation |
| --- | --- | --- |
| `crc32(bytea)`, `crc32c(bytea)` | INT8 | Zero-extended unsigned 32-bit IEEE / Castagnoli checksum |
| `md5(bytea)` | TEXT | 32 lowercase hexadecimal characters |
| `sha224(bytea)`, `sha256(bytea)` | BYTEA | 28 / 32 raw digest bytes |
| `sha384(bytea)`, `sha512(bytea)` | BYTEA | 48 / 64 raw digest bytes |

All are strict: typed NULL yields the typed result NULL, whereas empty input
has a nonnull digest (CRC zero). NUL, invalid UTF8 octets and backslashes are
hashed as bytes, not decoded or normalized. SQL unknown CRC/SHA literals and
parameters infer BYTEA; raw Go binds never invoke byteain. For example,
`sha256('\x41')` hashes A, while a raw bound `\x41` hashes four literal bytes.
MD5 chooses BYTEA for an already-known BYTEA expression/parameter, and prefers
TEXT for unknown input. TEXT MD5 hashes exact UTF8 after implicit TEXT conversion:
BPCHAR padding is removed; TEXT/VARCHAR spaces remain; NUL/invalid UTF8 input
is rejected. Go argument kinds never choose an overload. Inferred TEXT imports
native typed nil `[]byte` as empty TEXT, while pq sends NULL; ordinary nil is
NULL on both. SHA-first establishes BYTEA; MD5-first establishes TEXT.
Textual string-function roles also admit internal `"char"` annotations through
the existing type-owned input codec, so `md5('a'::"char")` hashes `a`. Literal,
NULL and parameter inputs retain the resolved CHAR descriptor without requiring
a general explicit CHAR identity cast; typed NULL still yields TEXT NULL.
This bounded input admission does not enable internal-CHAR columns, nontextual
base-conversion arguments or general CHAR casts.

`to_bin(integer|bigint)`, `to_oct(integer|bigint)` and
`to_hex(integer|bigint)` return TEXT with selected 32/64-bit two's-complement
width for negatives, no sign/prefix, lowercase hex and no leading zeroes for
positives. SMALLINT/unknown are ambiguous (42725); use an explicit selected-width
cast. Typed NULL returns typed TEXT NULL. Like MD5, results have TEXT typmod-1,
Length MaxInt64 and no DecimalSize, not a fixed digest/base-output length.
Stored base calls preserve their selected integer descriptor during SQL
reconstruction: even a small BIGINT value or typed NULL replays with its
original overload, rather than being reinferred as INTEGER or unknown.

`pg_size_bytes(text)` produces unconstrained INT8 with raw `int64` values,
including exact values beyond float's integral precision. Its result has no
Length or DecimalSize. `pg_size_pretty(bigint|numeric)` produces unconstrained
TEXT with raw strings, Length MaxInt64 and no DecimalSize. Typed NULL retains
the result descriptor; metadata is available before NULL-only/empty iteration.
Computed nullability is unknown and results have no physical column origin.
Numeric argument typmods do not become TEXT typmods: bytes output retains its
numeric input scale, while unit promotion truncates before final half rounding.
Neither overload narrows numeric through BIGINT or float. Existing domains
resolve by base type; SMALLINT/INTEGER/unknown pretty arguments require a cast
because BIGINT and NUMERIC are nonpreferred candidates. Stored reconstruction
preserves that selected symbolic argument type, even for small BIGINT constants
and typed NULLs. Native replacement retags descriptors through the existing model.

Size bytes reuses existing native textual coercions, not a new coercion family.
Internal `"char"` to TEXT is still absent: its bytes call has native 42883
versus PostgreSQL's converted result. The selected TEXT signature is unchanged.

Results have computed unknown nullability and no physical origin. MD5 TEXT
reports Length MaxInt64 on both adapters; native CRC/SHA Length is unavailable.
DecimalSize is unavailable for every result; fixed SHA lengths do not add typmods.
Changing bytea_output affects neither checksum integers nor MD5 text nor raw
SHA bytes. Every exported SHA buffer is independently owned. Providers check
cancellation at bounded chunks and before result publication; hashing does not
allocate scratch proportional to input size. Query/write/CHECK/view and native
replay use the existing scalar provider; BYTEA hash DEFAULT calls remain excluded,
while TEXT MD5 and base-conversion DEFAULT calls are supported.
Hash feature smoke: `make check RUN='^TestReferenceBinaryHashSmoke$'`;
pinned reference: `make check REFERENCE=1 RUN='^TestReferenceBinaryHash'`.

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
TEXT/regex slicing and encoding-aware length remain unsupported0A000; selected
TEXT/BPCHAR count/search siblings are documented in
[String length and search](DIALECT.md#string-length-and-search--change-note).
BYTEA functions retain the DEFAULT exclusion; query/write/CHECK/view consumers
share the provider and native replay. No byteapos alias or new cast edges are added.
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
Binary slicing smoke: `make check RUN='^TestReferenceBinarySlice'`;
independent reference: `make check REFERENCE=1 RUN='^TestReferenceBinarySlice'`.


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
and metadata unchanged. Feature smoke: `make check RUN='^TestUUIDSmoke$'`;
real reference coverage: `make check REFERENCE=1 RUN='^TestReferenceUUID.*$'`.

UUID typed literals, casts, arrays, version/timestamp extraction functions and
binary wire UUID APIs remain outside this UUID storage/generation feature.
PostgreSQL disk storage, network protocol serving, and the external pg_dump process/custom archives are
non-engine boundaries, not successful no-op implementations.

### UUID generation

`gen_random_uuid()` and `uuidv4()` are zero-argument, cryptographic version4
providers (122random bits). `uuidv7()` and `uuidv7(interval shift)` are separate
overloads, not an interval default argument. Both return version7 UUIDs with a
48-bit big-endian UNIX millisecond timestamp, a12-bit submillisecond field and
62random tail bits. Every result sets the RFC variant10 bits. On macOS/Windows
coarse-clock platforms the low2fraction bits additionally mix cryptographic
randomness, following PostgreSQL's977ns minimum clock step rather than245ns.
The selected functions are volatile; the interval overload is strict and returns
UUID NULL without demanding clock/entropy when its child is NULL. Contextual
quoted strings/parameters infer INTERVAL; known TEXT/integer has no implicit
interval edge. Only the selected one-argument `shift=>`/`shift:=` name is admitted.
Result metadata retains UUID identity and adapter ScanType even for empty/all-NULL
cursors. Native declared columns retain their known declared nullability;
arbitrary generated expressions do not acquire a UUID-specific nullability
inference. If expression Nullable is unavailable, do not interpret its returned
boolean as a NOT NULL promise. Any available proof must agree with the selected
overload/argument semantics. Pinned pq does not expose Nullable proofs.


The v7 ascending clock belongs to a physical session, shared by both overloads,
not a table, prepared statement or transaction. Tie/backward wall-clock readings
advance by the minimum step. Consecutive unshifted and zero-shift calls on that
session are strictly UUID-ordered; differing shifts or different sessions have
no such guarantee. Pin `sql.Conn` when observing a series. Rollback and native
catalog Reset/Restore do not reset this clock; replacement sessions start fresh.
UUID entropy is independent of `setseed` and is never serialized into Dump.

Shift arithmetic captures current time at execution, preserves submicrosecond
remainder, and uses the existing TIMESTAMPTZ+INTERVAL provider: months with
month-end clamping, then local calendar days, then elapsed microseconds.
Execute-time TimeZone governs offset resolution even on a held prepared call.
Literal interval input meaning retains Prepare-time IntervalStyle; parameter
input uses Execute-time IntervalStyle. Far-future UUID packing uses microseconds
directly, not overflowing int64 UNIX nanoseconds.

Native's PostgreSQL18.6 contract accepts UNIX microseconds
`0 <= timestamp <= 281474976710655000` inclusive. This upper guard is exactly the
start of the final48-bit millisecond, not another999microseconds beyond it.
Infinite intervals fail22008 `interval out of range for UUID version 7`, Detail
`UUID version 7 does not support infinite intervals.` Finite timestamp results
outside the guard fail22008 `timestamp out of range for UUID version 7`, Detail
`UUID version 7 supports timestamps from 1970-01-01 to approximately year 10889.`
Arithmetic overflow still reports22008 `timestamp out of range` without UUID
Detail. Runtime guards do not invent Position/Context/object fields; malformed
interval input retains ordinary22007 character Position or parameter Context.
Valid infinite/out-of-range literals prepare successfully and fail on demand.

The unchanged PostgreSQL18.0 pinned oracle predates these guards: its infinite
shifts succeed with canonical UUID/version7/variant10, but unchecked signed
sentinel overflow prevents a portable timestamp golden. Finite outside-48-bit
results wrap modulo2^48. Dual-mode scenarios independently assert native guards
and these observed/source-grounded18.0 differences.

Closed selected UUID defaults (including closed stable/immutable compositions)
generate fresh values per inserted row, never at CREATE/Prepare/Dump. Unrelated
volatile defaults remain unsupported. Normal views/CHECK/RETURNING/aggregates
and joins visit the interval child and retain typed UUID dependencies. Generated
columns and expression indexes reject surviving volatile generation. A strict
constant-NULL interval call, including a child simplified from CASE/COALESCE,
instead has an immutable typed UUID NULL physical tree and is permitted there.
Planning does not invoke clock/entropy or erase the analyzed symbolic definition.
Non-NULL `uuidv7()`/`uuidv7(interval '0')` remain volatile and rejected.
Native Dump saves committed UUID literals and qualified symbolic provider calls;
Restore preserves saved values and generates fresh values on subsequent demand.
Within a selected UUID shift's stored expression subtree, captured concrete
interval NULLs retain their type casts, including all-NULL COALESCE children,
so replay preserves the formal interval overload rather than inferring TEXT.
Unrelated scalar and row-data NULL serialization remain unchanged.
Feature smoke: `make check RUN='^TestReferenceUUIDGenerationFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceUUIDGenerationFeatureSmoke$'`.

## MONEY — change note

MONEY stores a signed eight-byte count of minor units, not text, NUMERIC or a
binary float. Captured `lc_monetary` determines interpretation and presentation,
not storage rescaling. Supported C, POSIX and empty-as-C policies all have two
fraction digits, decimal period, comma grouping, `$` currency and `-$` negatives.
The exact range is -92233720368547758.08 through 92233720368547758.07.
Zero renders `$0.00`; there is no negative zero.

```sql
CREATE TABLE amounts(id INT, v MONEY DEFAULT MONEY '1.235');
INSERT INTO amounts VALUES(1, '$878.08'), (2, NULL);
SELECT v/11::bigint, v/11::float8 FROM amounts WHERE id=1;
-- MONEY $79.82, MONEY $79.83
SELECT '92233720368547758.07'::money::numeric;
-- exact NUMERIC 92233720368547758.07
```

Text input follows PostgreSQL's permissive cash grammar: optional currency,
signs, whitespace and parentheses; grouping placement is not validated.
Empty/space/currency-only input is zero. The first discarded fraction digit
rounds half away from zero; later digits are ignored. Numeric exponent syntax,
NaN and infinity are not money text. Malformed text fails22P02; input overflow
fails22003 `value "<original>" is out of range for type money`, including
rounding carry past either endpoint.

NUMERIC↔MONEY and INTEGER/BIGINT→MONEY are assignment casts, not implicit
numeric interchange. Numeric conversion is exact and rounds half away from
zero; MONEY→NUMERIC retains exactly two display places. Cast overflow is22003
`bigint out of range`; numeric specials retain the bigint conversion0A000 errors.
SMALLINT→MONEY, direct float→MONEY and MONEY→integer/float are unavailable;
float→NUMERIC→MONEY remains explicit. Use `::`, `CAST` or MONEY typed literals;
general function-style conversion is not expanded.

DatabaseTypeName is MONEY; native ScanType is string and raw values are string
or nil even for empty/all-NULL results. Length and DecimalSize are unknown:
fixed currency precision is not a NUMERIC typmod. Textual string/owned-byte,
int64 and float64 binds use the inferred MONEY input codec; nil and typed-nil
bytes are NULL. Go argument kinds never select an operator overload.

Monetary input/output and conversion casts are STABLE; arithmetic and raw-unit
comparison are IMMUTABLE. Column money→numeric/text casts cannot be generated
or indexed immutable expressions. Canonical native Dump emits typed MONEY
leaves (including typed NULL), preserving default/check/view identity on replay.
View serialization reuses these owning typed leaves instead of adding redundant
same-type casts, so typed NULL and currency leaves remain deterministic across
Dump/Restore/redump.
Dump excludes settings and Restore preserves live settings. This is an
equivalent-C-policy restore contract, not arbitrary-locale dump portability or
pg_dump emulation. Existing atomic failed/canceled Restore and retained
cursor/metadata ownership apply without a monetary side channel.

Native safely rejects minimum raw int64 divided by -1 for all three integer
widths with22003 `money out of range`. This is an explicit safety deviation from
the unguarded REL_18_0 cash_div_int64 source, not observed backend-crash parity.
See [operator boundaries](DIALECT.md#monetary-operators-and-casts--change-note).

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
input errors have no query Position and carry the actual
`unnamed portal parameter $n` Context, with ` = '...'` for nonempty decoded input
and ` = ''` for empty decoded input. Statement and Restore failures publish no
partial rows. Dump quotes canonical exact numeric output and declarations;
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
those functions do not themselves add operators or general casts. Exact operators
are covered by [numeric arithmetic](DIALECT.md#numeric-arithmetic--change-note).

Numeric SQL parsing does not broaden SET's plain-decimal-integer grammar:
settings' quoted numeric codecs retain their separate units/prefix behavior.
Numeric type input does not itself introduce serial, general functions/operator
syntax or SELECT-without-FROM expansion. Standard/postfix annotations reuse
same-type scalar usages and existing directional numeric coercion edges;
known-TEXT input conversion and function-cast boundaries remain unchanged.
Integer arithmetic includes SMALLINT: typed `32767::smallint+1::smallint`
fails with22003 `smallint out of range`, whereas `32767::smallint+1`
selects INT4 and returns32768. Unary signs retain width; mixed integer
`+ - * /` return the wider width and mixed `%` promotes to its homogeneous
signature. Cast operands before evaluation to widen; a BIGINT destination or
outer result cast cannot rescue a narrower overflowing operation.

Feature smoke: `make check RUN='^TestNumericSmoke$'`.
The same smoke includes signed integer carry failures beside successful extrema,
plus direct/prepared REAL/FLOAT8 assignments of 1.5, -1.5 and zero with a tiny
exponent. Populated float comparisons against 1.0 and 1e100 assert exact result
types even when no rows match, in both direct and prepared modes.
Real pinned PostgreSQL witnesses:
`make check REFERENCE=1 RUN='^TestReferenceNumeric.*$'`.

Native logical negative-scale metadata is asserted independently of lib/pq's
unsigned decoding: NUMERIC(2,-3) reports `(2,-3,true)` natively and `(2,2045,true)`
through pinned pq. The adapter difference does not change SQL rounding.
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
Feature smoke: `make check RUN='^TestBitwiseSmoke$'`.
The arithmetic/bitwise/math reference definitions also run Docker-free through
`make check RUN='^TestReference(Arithmetic|Bitwise|Math)[A-Za-z0-9_]*$'`.
The identical definitions add pinned PostgreSQL18.0 under `make check REFERENCE=1` with
that selector; retained tagged oracle-only complements remain server-only.

## Binary floating point

REAL/FLOAT4 store IEEE binary32; DOUBLE PRECISION/FLOAT8 and bare FLOAT store
binary64. FLOAT(p) selects REAL for binary precision 1–24 and DOUBLE PRECISION
for 25–53, without a per-value typmod. Out-of-range precision fails `22023`
at the precision token, even for CREATE TABLE IF NOT EXISTS. Quoted `"float4"`
and `"float8"` are catalog names; quoted keyword aliases such as `"real"` are
not aliases. Result DatabaseTypeName is FLOAT4/FLOAT8, ScanType is float64,
including NULL/empty results; no fictitious DecimalSize or Length is reported.

Decimal inputs may be quoted, inferred numeric tokens or positional binds:
`INSERT INTO f VALUES(0.1)`. Explicit floating-to-character casts use captured
session presentation; other conversions remain in the bounded typed cast graph.
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
lowercase `e` with at least two exponent digits. `extra_float_digits` values1..3
retain this shortest mode; values0..-15 select max(1,6+n) significant digits for
REAL and max(1,15+n) for DOUBLE PRECISION. Output captures Execute-time precision,
including explicit TEXT/VARCHAR/BPCHAR IO, domain/array leaves, string formatting
and failing-row diagnostics. Dump and SQLLiteral always use canonical shortest
output and canonical REAL/DOUBLE PRECISION declarations, so Restore roundtrips
payloads and specials independently of live session settings.
Native REAL export is the exact widened stored binary32; pq instead parses
the session-selected decimal output as binary64. Thus REAL `'0.1'` at the default
precision returns0.10000000149011612 natively and0.1 through pq; reference assertions preserve
this adapter distinction before comparing binary32 semantics. That comparison
must decode independently specified output text directly at 32 bits: narrowing
pq's binary64 export can double-round. For REAL `'7.038531e-26'`, native storage
has bits 0x15ae43fd, but narrowing pq's returned binary64 to binary32 produces
0x15ae43fe. Both adapter outputs are asserted separately, not normalized away.

Executable float acceptance (commands must run to establish verification):

```sh
make check RUN='^TestFloat.*$'
make check RUN='^TestReferenceFloat.*$'
make quickcheck # full Docker-free verification after edits
make check REFERENCE=1 RUN='^TestReferenceFloat.*$'
make check RUN='^TestReferenceFloatMigratedSmoke$'
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
NOT, AND and OR in SELECT targets and WHERE. Bounded integer and floating arithmetic and
baseline and character-family explicit casts are scalar operands. Comparison
chaining, SELECT aliases/qualified column references, general functions, casts
outside the bounded baseline and character-family sets and comparison
in INSERT/settings remain excluded. Comparisons do not sort rows.
Selected null-count calls remain excluded from Boolean/comparison operands,
and Boolean/comparison/arithmetic expressions remain excluded from their arguments.
Former signed literal inputs remain admitted, including signs around baseline
casts of numeric literals. Casts keep their precedence, conversions and errors;
they are not removed or reassociated through the sign.

Feature smoke: `make check RUN='^TestCompareOpsSmoke$'`; independent
pinned PostgreSQL coverage: `make check REFERENCE=1 RUN='^TestReferenceCompareOps'`;
native coverage: `make check RUN='^TestReferenceCompareOps.*$'`.

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
providers, with deterministic UTF8 byte/codepoint ordering under seeded and
registered user collations: no locale-general or ICU comparison is invented.

Comparison, ordering, grouping and B-tree/hash keys retain the selected registered
capability, not merely a type or raw OID. Equivalent copied providers remain
distinct objects. Missing/forged capabilities reject at admission, including
empty/all-NULL keys, and cannot silently select C. Unicode case mappings are not
case-insensitive equality: `A = a`, `ß = SS`, and composed/decomposed Unicode
remain false. BPCHAR equality/hash both trim only their existing ASCII-space
tails, including domain base normalization. Cancellation precedes equal-byte
and NULL key shortcuts. See [expression collation](DIALECT.md#expression-collation--change-note).

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

Focused checks: `make check RUN='^TestReferenceCharacter'`,
`make check REFERENCE=1 RUN='^TestReferenceCharacter'` (real pinned PostgreSQL18.0),
and `make check RUN='^TestCharacterSmoke$'`.

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
and retained TEXT results never consult a later catalog. Nested stored CHECK,
DEFAULT and explicit write/USING expressions propagate snapshot demand through
existing statement capture. REGCLASS input and explicit text/numeric conversions
are admitted as described above; ordinary string defaults compose with
trim/normalization/concatenation and the four case providers.

`lower`, `upper`, `casefold` and `initcap` return unconstrained TEXT with inherited
collation, including NULL outputs. They reuse the selected implicit character
inputs, without adding numeric/BOOL/BYTEA casts. The default/C/POSIX mode is ASCII;
pg_c_utf8811 uses Unicode16 simple mappings, Alphabetic plus ASCII digits for
initcap, and simple UPPER at word starts. pg_unicode_fast6411 uses full
lower/upper/title/fold mappings, Alphabetic plus Unicode Nd, and original-input
Final_Sigma for lower (including initcap's lower phase). PostgreSQL18.0 treats a
nonempty all-Case_Ignorable prefix as sufficient preceding context; an empty
prefix or nearest nonignorable uncased character is not sufficient. The next
nonignorable character must not be Cased. Original input, never mapped output,
owns that context. Full expansions are not clipped to source typmod and folding
does not normalize. These deterministic collations retain byte/codepoint
comparison, BPCHAR tail semantics, immutable expression authority and stored
Dump/Restore identity. NonNULL indeterminate case collation produces42P22; strict
NULL bypasses classification.
Registered user collations inherit their admitted immutable ASCII/Simple/Full
capability, while preserving their own identity through domains/arrays, coercions,
defaults, CHECKs, views and native Dump/Restore. Provider-equivalent objects are
not identity-equivalent and still produce ordinary collation conflicts.


Normalization data is pinned to Unicode **16.0.0** in
`internal/sqltype/unicode16/data/UnicodeData.txt` and
`DerivedNormalizationProps.txt`. The unmodified official conformance corpus is
`internal/sqltype/unicode16/testdata/NormalizationTest.txt`; Unicode attribution
and license are retained beside the runtime. Sources:
https://www.unicode.org/Public/16.0.0/ucd/UnicodeData.txt,
https://www.unicode.org/Public/16.0.0/ucd/DerivedNormalizationProps.txt and
https://www.unicode.org/Public/16.0.0/ucd/NormalizationTest.txt.

Case data additionally uses the original Unicode16.0.0 files
`SpecialCasing.txt`, `CaseFolding.txt` and `DerivedCoreProperties.txt` in the same
data directory, under the retained Unicode license. Original sources:
https://www.unicode.org/Public/16.0.0/ucd/SpecialCasing.txt,
https://www.unicode.org/Public/16.0.0/ucd/CaseFolding.txt and
https://www.unicode.org/Public/16.0.0/ucd/DerivedCoreProperties.txt.
UnicodeData supplies simple mappings and Nd (including First/Last ranges);
SpecialCasing supplies unconditional full mappings plus Final_Sigma, excluding
language-specific rules; CaseFolding supplies C+S simple and C+F full mappings,
excluding T. DerivedCoreProperties supplies Alphabetic, Cased and Case_Ignorable.

From the repository root, deterministic regeneration is:

```sh
go run ./internal/sqltype/unicode16/generate \
  -source internal/sqltype/unicode16/data \
  -output internal/sqltype/unicode16/tables_generated.go \
  -case-output internal/sqltype/unicode16/case_tables_generated.go
```

The standard-library-only generator runs offline over those local originals.
It emits sorted static sparse arrays, recursive decomposition sequences,
composition exclusions, all four quickcheck tables, fixed inline case expansions
up to three codepoints, and sorted merged case-property ranges. The optional
`-case-output` preserves the original normalization-only invocation. Case scans
use binary search and lazy output construction, not per-execution maps or rune
arrays; unchanged results borrow the input. Runtime has no network, ICU/cgo,
mutable startup maps or dependence on Go's host Unicode tables. Tests compare
both outputs byte-for-byte, retain every official normalization corpus assertion,
and independently check case mappings/properties, Unicode16 additions, title
boundaries, original-input sigma context and bounded cancellation.

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
Selected [temporal arithmetic](DIALECT.md#date-and-time-arithmetic--change-note)
and datetime formatting are supported separately; unrelated functions remain excluded.

Feature smoke: `make check RUN='^TestTimeSmoke$'`.
Dual-mode migration: the public TIME SQL scenarios now live in `reference/`.
Native coverage: `make check RUN='^TestReferenceTime[A-Z][A-Za-z0-9_]*$'`.
Real pinned reference: `make check REFERENCE=1 RUN='^TestReferenceTime[A-Z][A-Za-z0-9_]*$'`;
the tagged optional-date complement remains real-oracle coverage only.

## Age and finite predicate results — change note

Selected age calls return the existing unsized INTERVAL1186 descriptor
(typmod-1), preserving symbolic months, days and microseconds independently.
Input TIMESTAMP precision does not constrain the result. Selected isfinite
calls return BOOL16, including typed NULL and zero-row direct/prepared cursors.
Computed metadata has unknown Length, DecimalSize and Nullable; aliases or
the default `age`/`isfinite` names are retained after cursor closure.

Native INTERVAL ScanType/export is string/nil, while pinned lib/pq describes
interface{} and exports []byte/nil. BOOL describes and exports bool/nil in both
modes. Existing output snapshots own IntervalStyle, including held prepared
execution and retained native rows. Infinity uses the existing reserved temporal
sentinels and entire interval tuples; a finite interval component at its integer
limit is not itself infinity. BC arithmetic uses astronomical civil years, not
display years. See [calls](SQL.md#selected-age-and-finite-predicate-calls) and
[semantics](DIALECT.md#age-and-finite-predicates--change-note).

## Constructed datetime values — change note

The five selected numeric constructors return the existing DATE1082, TIME1083,
TIMESTAMP1114, TIMESTAMPTZ1184 and INTERVAL1186 descriptors with typmod-1. They
construct typed inline civil days, clock/instant microseconds or independent
interval month/day/time fields, not text approximations fed back through codecs.
Native DATE/TIME/INTERVAL exports remain string/nil; finite timestamps export
time.Time/nil. pq's DATE/TIME time.Time and INTERVAL byte-slice boundary remains
independently specified in shared reference tests.

Negative input years mean BC (-1 is astronomical zero), with no positive year0.
DATE's finite civil domain is4714-11-24BC through5874897-12-31. Timestamp's domain
is4714-11-24BC through294276-12-31 23:59:59.999999, with exclusive upper END:
constructor rounding/carry may not produce END even when a later typmod input
path accepts it. Julian admissibility is checked before clock completion and
the combined finite timestamp check; prior-day24:00 can carry onto finite MIN.

Seconds use binary64 multiplication and nearest/even microsecond rounding before
clock bounds. TIME retains24:00, whereas timestamps advance the civil date.
Intervals check years*12 and weeks*7 as signed32-bit intermediates and all final
month/day/microsecond sums, retaining24hours separately from1day and30days
separately from1month. NaN/infinite or out-of-range seconds reject; finite
floating multiplication overflow is22003, ordinary interval overflow22008.
Constructors never generate temporal or interval infinity sentinels.

NULL results retain exact types. Session-dependent construction/output uses the
existing captured environments; IntervalStyle still belongs to its output codec.
See [calls](SQL.md#selected-datetime-constructor-calls) and
[semantics](DIALECT.md#datetime-constructors--change-note).

## Datetime formatting — change note

`to_char` on TIMESTAMP, TIMESTAMPTZ and INTERVAL returns unsized TEXT.
`to_date(text,text)` returns DATE directly, retaining DATE's wider range;
`to_timestamp(text,text)` returns unsized TIMESTAMPTZ with microsecond precision.
All five overloads are strict STABLE and use PostgreSQL formatting templates,
not the input/output syntax of an ordinary cast. Their captured session
environment survives retained cursors and is refreshed by prepared execution.
Interval output keeps months, days and microseconds separate, allowing large
and signed hours while rejecting fields requiring a real calendar date.
Formatting readers preserve empty/truncated-input defaults without skipping
already-demanded fields exhausted by whitespace. `TM` collation demand follows
reached name conversion, not the entire mask; exact input names and NULL/infinite
output do not demand a conversion solely because a mask contains `TM`.
`lc_time` supplies reached `TM` month/day fields in both output and input from the
captured bounded C/POSIX category provider. Empty lc_time deliberately uses C;
ordinary non-TM names stay independent of this locale category.

DatabaseTypeName is TEXT, DATE or TIMESTAMPTZ, independent of mask fields,
source precision, empty results and NULL. Native DATE ScanType is string while
pinned lib/pq DATE ScanType is time.Time; native BC strings and pq astronomical
years are asserted independently. TIMESTAMPTZ scans as time.Time and TEXT as
string. Mask fractions do not impose a result typmod or decimal precision.
See [templates, modifiers and limits](DIALECT.md#datetime-formatting--change-note).
Shared acceptance and native replacement controls:
`make check RUN='^TestReferenceDatetimeFormatting'`;
pinned reference uses the same selector with `REFERENCE=1`.

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
execution start. DateStyle affects input/output as described below. CURRENT_DATE
reads transaction start dynamically even when prepared. Selected
[temporal arithmetic](DIALECT.md#date-and-time-arithmetic--change-note) and
arrays compose through the shared scalar/query paths; ordinary no-FROM SELECT
uses that same query path.

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

Feature smoke: `make check RUN='^TestDateSmoke$'`.
Dual-mode migration: the public DATE SQL scenarios now live in `reference/`.
Native coverage: `make check RUN='^TestReferenceDate[A-Z][A-Za-z0-9_]*$'`.
Independent pinned oracle: `make check REFERENCE=1 RUN='^TestReferenceDate[A-Z][A-Za-z0-9_]*$'`.
The tagged timestamp-comparison and transaction-relative complements remain
real-oracle coverage. Pinned pq temporal descriptors report unknown nullability
and unavailable Length/DecimalSize; native physical columns retain known
nullability. pq's Go year-zero February 29 DATE binding formats March 1 BC,
while native binding retains February 29 BC; textual BC input is unchanged.
The civil-bound corpus retains both direct and reused prepared INSERTs and
exact ordered multiplicities. Literal failure coverage checks the same target's
empty rows and retained metadata before separately testing populated sentinels.

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
Run `make check RUN='^TestTimestampSQLTransactionChainSmoke$'`
for literal/parameter clock stability, relative civil dates and fresh chain/end
boundaries. `make check RUN='^TestReferenceTimestampSQLTransactionChainClock$'`
and `make check REFERENCE=1 RUN='^TestReferenceTimestampSQLTransactionChainClock$'`
assert these relationships separately on each selected backend, not absolute clock parity.
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
are described below. Selected [temporal arithmetic](DIALECT.md#date-and-time-arithmetic--change-note) is supported; other temporal functions remain excluded;
ordinary no-FROM SELECT uses the shared query pipeline. The default ordinary
input order is MDY; selected datetime formatting uses its explicit template instead.
Dual-mode migration: public timestamp-without-time-zone SQL scenarios now live
in `reference/`; obsolete root row/diagnostic helpers are removed.
Feature smoke: `make check RUN='^TestTimestampSmoke$'`.
Native acceptance: `make check RUN='^TestReferenceTimestamp[A-Z][A-Za-z0-9_]*$'`.
Independent real PostgreSQL 18.0: `make check REFERENCE=1 RUN='^TestReferenceTimestamp[A-Z][A-Za-z0-9_]*$'`.
Finite raw values must remain `time.Time` with offset zero before comparison;
zero-offset fixed locations and UTC locations are not pointer-identical.
Clock expectations retain microsecond instant and civil equality without
requiring Location identity. Infinity byte mutation/fresh-read checks cover
both stored columns and noisy typed-literal constants before normalization.
Fractional text/`time.Time` frames and both displacement limits reuse their
respective INSERT handles while retaining direct routes and exact row counts.
Codec corner cases and allocation assertions can be selected with
`make check RUN='^TestTimestamp.*$'`;
`make check RUN='^TestTimestamp.*Allocations$'`
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
precede the execution-selected embedded Default/Australia/India abbreviation
table; dynamic abbreviations retain historical meanings, not just today's
active offset. Customized installation files/runtime file loading are unavailable.
Rule bytes are embedded from the pinned
PostgreSQL 18.0 Debian image's IANA2025b/backzone data; runtime never consults host
zone files. Historical second offsets and future rules use the same provider.

PostgreSQL's DST gap/fold preference is intentional, not an error:
New York `2018-03-11 02:30` uses the pre-transition offset (07:30 UTC);
`2018-11-04 01:30` uses the post-transition offset (06:30 UTC).
Explicit EDT selects 05:30 UTC in that fold. Malformed/conflicting fields,
unknown zones and invalid offsets still fail with positioned English diagnostics.
Literal ISO offsets are east-positive and bounded to ±15:59:59.
POSIX specifications have the opposite sign (`UTC-2` means +02:00).

`TIMESTAMP AT TIME ZONE text/interval` produces TIMESTAMPTZ; TIMESTAMPTZ converts
back to civil TIMESTAMP. All nine `timezone(...)` overloads share this provider;
their unary forms and AT LOCAL use session TimeZone. TIMETZ converts to TIMETZ,
shifting/wrapping its clock and replacing its offset. Named destination offsets
are chosen at transaction-start instant, not the source literal's date or a
per-row wall clock. A full DST-bearing zone in timetz input requires a supplied
date; stored timetz output remains fixed when session TimeZone changes.

POSIX/session-derived TIMETZ offsets range from169 hours east to168 hours west,
distinct from literal input's ±15:59:59 bound. Interval-zone conversion truncates
fractional seconds and wraps to an int32 displacement, accepting the complete
released conversion domain without widening typed literals or SET TIME ZONE.
Converted values retain their clock/offset through arithmetic, storage and native
replay; replay reconstructs wide values through interval conversion, not invalid
numeric-offset input. At INT32_MIN, TEXT output preserves the pinned release's
unsigned offset-field rendering. lib/pq cannot decode these huge TIMETZ offsets
as ordinary raw values; shared extrema coverage uses explicit TEXT output.
Timestamptz equality/order uses the instant. Timetz compares non-modular UTC-clock
keys and then seconds-west offset: `09:00+01` sorts before, and is unequal to,
`08:00+00`. Grouping, ordering and conditional/distinct predicates share these semantics.
SELECT DISTINCT and min/max reuse these same temporal comparison semantics.

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
The [timezone lookup views](DIALECT.md#timezone-lookup-views--change-note) now
reuse these codecs: unrestricted TEXT labels, INTERVAL offsets (zero months/days,
exact seconds east of UTC) and BOOL daylight flags. Native interval ScanType is
string; pinned pq advertises interface{} and returns interval bytes. Declared
view columns have known nullable=true natively; pq exposes unknown nullability.
Selected [INTERVAL arithmetic](DIALECT.md#date-and-time-arithmetic--change-note) uses these existing codecs.
The five [SQL current date/time producers](DIALECT.md#sql-current-date-and-time--change-note)
use these same codecs/settings and remain dynamic under held prepared execution.

`date_add(timestamptz,interval[,text])` and `date_subtract` reuse the existing
interval arithmetic provider. Months and days follow the selected zone's civil
calendar (including gap/fold transitions); microseconds remain elapsed time.
Omitting the zone reads execution TimeZone; explicit zones do not change the
session. Full dynamic-abbreviation rules are used for calendar operations.
Every formal is strict and every result has unrestricted typmod. Explicit-zone
calendar forms are Immutable; omitted-zone forms are Stable, with child/cast
dependence retained. Input errors keep the existing adapter parameter Context;
runtime zone/arithmetic errors do not invent one.

Feature smoke: `make check RUN='^TestReferenceDatetimeZoneFeatureSmoke$'`.
Public/provider witnesses: `make check RUN='^TestReference(DatetimeZone|Timezone)'`.
Independent pinned oracle: `make check REFERENCE=1 RUN='^TestReference(DatetimeZone|Timezone)'`.


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
Existing GROUP BY, SELECT DISTINCT and aggregate DISTINCT consume the same
typed equality classes. Aggregate DISTINCT also validates sort eligibility;
native arrays/PG_NODE_TREE have explicit aggregate DISTINCT/order0A000 boundaries
despite PostgreSQL support, without widening ordinary carrier ordering.

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
NULL/infinities retain their existing encoding. Selected
[interval and timestamp arithmetic](DIALECT.md#date-and-time-arithmetic--change-note)
preserves these components and returns unrestricted computed metadata.
Selected `make_interval` construction is documented above.
Selected [interval justification](DIALECT.md#interval-justification--change-note)
normalizes components with unrestricted base INTERVAL output, including NULL,
restricted and domain inputs. Selected
[field extraction](DIALECT.md#datetime-extraction--change-note) reads the independent
interval components. New cross-family cast edges remain excluded.
See [IntervalStyle](SETTINGS.md#intervalstyle--change-note).

Field extraction exposes unrestricted NUMERIC (EXTRACT) or FLOAT8 (date_part),
not a source typmod. Integral fields and microseconds have NUMERIC display scale0,
milliseconds scale3 and seconds scale6. Epoch uses scale6 except integral DATE
EXTRACT; timestamp Julian fractions use default numeric division scales rather
than a fixed precision. Results use genuine numeric or float signed infinities,
and oscillating infinity fields are typed NULL.
Both wire families have unrestricted metadata: lib/pq reports NUMERIC scan type
interface{}, length unavailable and DecimalSize(65535,65531,true), with raw bytes;
native NUMERIC scans as string and DecimalSize is unavailable. FLOAT8 scans as
float64 in both, without decimal metadata. Computed fields report unknown
Nullable(false,false), rather than inferred true/known nullability. These
transport differences are asserted independently, not reconciled by converting
numbers through FLOAT8.
Native raw hour/zone/epoch extraction frames also assert this metadata before
and after source-session physical teardown, with retained NUMERIC strings and
FLOAT8 values from the original execution zone rather than a later session zone.

Feature smoke: `make check RUN='^TestIntervalSmoke$'`.
The interaction regressions are merged into untagged dual-mode `reference/`
scenarios, with independent native string and pq byte-slice representations:
`make check RUN='^TestReferenceInterval[A-Za-z0-9_]*$'`
and `make check REFERENCE=1 RUN='^TestReferenceInterval[A-Za-z0-9_]*$'`.

### Interval average

`avg(INTERVAL)` returns unrestricted INTERVAL (typmod -1), including when its
input is `INTERVAL SECOND(0)`: the mean of one second and zero is `00:00:00.5`.
A subsequent SECOND(0) destination assignment rounds independently to one second.
NULL inputs are ignored; no non-NULL input yields typed NULL. Native metadata is
known nullable even for NOT NULL source columns, with no physical source origin.
The existing string/nil codec and Execute-time IntervalStyle capture apply.

The mean divides checked month/day/microsecond sums independently, cascading
fractional months at 30 days/month and fractional days at 24 hours/day, with
PostgreSQL float8 conversion, intermediate six-decimal rounding and nearest-even
microsecond rounding. It does not use the comparison span or justify components:
the mean of one month and 30 days is `30 days`; of one day and24hours, `24:00:00`.
Finite component overflow, reserved infinity tuples, mixed infinity signs and
division range errors report22008. Finite sums are checked even after infinity.
Scalar interval division shares only this finite release float8 division kernel;
AVG retains independent aggregation state/infinity semantics. Public interval AVG contracts:
`make check RUN='^TestReferenceIntervalAvg'`
and `make check REFERENCE=1 RUN='^TestReferenceIntervalAvg'`.

### Numeric and floating average — change note

AVG over SMALLINT/INTEGER/BIGINT/NUMERIC returns unrestricted NUMERIC (OID1700,
typmod -1); AVG over REAL/DOUBLE PRECISION returns FLOAT8 (OID701). Native
NUMERIC export remains string and FLOAT8 float64. Results are known nullable
without physical origin, even for NOT NULL inputs. NULLs do not contribute;
empty/all-NULL input returns typed NULL. Input numeric precision/scale never
becomes output metadata.

Exact decimal sums retain finite input display scale, including zero and
cancellation. PostgreSQL's normalized base-10000 division rule chooses output
scale, capped at1000 fractional digits, with ties away from zero. Singleton1
returns `1.00000000000000000000`; inputs1,NULL,3 return `2.0000000000000000`.
Packed finite SUM overflow precedes division (22003, `value overflows numeric
format`); oversized transient sums can cancel. NaN/both infinity signs yield
NaN and one infinity sign dominates finite sums, before finite packing.

REAL widens from its stored binary32 value. Float AVG uses the complete
Youngs–Cramer transition, including Sxx overflow checks (22003, `value out of
range: overflow`), even though final output uses only Sx/N. Final division
permits IEEE underflow/signed zero. No unordered floating evaluation order is
promised. Scalar exact numeric arithmetic uses the same division-scale convention
but validates each operator result, unlike an aggregate's transient subtotal.
See [numeric arithmetic](DIALECT.md#numeric-arithmetic--change-note).
Public contracts:
`make check RUN='^(TestReferenceNumericAvg.*|TestReferenceIntervalAvgDiagnostics)$'`
and `make check REFERENCE=1 RUN='^(TestReferenceNumericAvg.*|TestReferenceIntervalAvgDiagnostics)$'`.

### Numeric and floating sum — change note

SUM supports SMALLINT/INTEGER→INT8 (OID20), BIGINT/NUMERIC→NUMERIC (OID1700),
REAL→FLOAT4 (OID700) and DOUBLE PRECISION→FLOAT8 (OID701). Results are nullable,
typmod-1, without physical origin. Native exports INT8 as int64, NUMERIC as
string, and both float types as float64. NULLs are ignored; empty/all-NULL input
returns typed NULL. Numeric input precision/scale is not result metadata.

BIGINT and NUMERIC share AVG's exact accumulation and finite packing, but SUM
has no denominator/division. Maximum finite input display scale survives zeros
and cancellation. Packed finite range is checked only at finalization; NaN and
infinity precedence runs before packing. Integer-width promotion never uses a
float intermediary. REAL SUM rounds every addition to binary32, rather than
AVG's widened binary64 state; FLOAT8 sums at binary64. First-value seeding
preserves negative zero. Finite addition overflow fails22003 immediately;
explicit specials use IEEE arithmetic and addition has no underflow error.
Unordered floating accumulation order is unspecified. Native FLOAT4 exports
widened stored binary32 while pq parses released text at64 and advertises
float32 ScanType, an explicit adapter difference rather than a new SQL type.
Public contracts:
`make check RUN='^TestReferenceNumericSum'`
and `make check REFERENCE=1 RUN='^TestReferenceNumericSum'`.


## Pattern matching and physical character values

LIKE/ILIKE preserves physical BPCHAR padding on the left. For CHAR(3) storing
`a`, LIKE `a` is false, LIKE `a__` true; an explicit TEXT conversion trims
padding. BPCHAR patterns and escape strings convert to TEXT and trim padding.
VARCHAR/TEXT trailing spaces remain significant. Text `_` advances one UTF8
code point; BYTEA `_` advances one octet, so c3a9 matches `__`, not `_`.
ILIKE folds ASCII A–Z only under default/C/POSIX; no Unicode folding is implied.
Supported implicit column collation identities remain distinct and conflicting
C/POSIX matching produces demanded42P22. Explicit scalar COLLATE also supports
pg_c_utf8/pg_unicode_fast and registered user identities; annotations override implicit identity or indeterminacy,
and distinct explicit identities fail analysis with42P21. Plain LIKE remains
case-sensitive under the Unicode identities; effective Unicode-collation ILIKE
is rejected with positioned native0A000. Explicit C on either operand can
override an implicit Unicode identity and permits ASCII ILIKE.
Result metadata is BOOL, including NULL and empty relations.
No new character/binary conversions are introduced.
Collation identity and explicit annotation authority survive collatable casts,
conditional and supported string result wrappers, group/order caches and native
replay. Stored view columns retain their physical identity as implicit operands.
Indeterminacy and explicit annotation origins are expression metadata,
not an invalid stored Usage: CREATE VIEW rejects an indeterminate collatable
output at execution, while determinate POSIX outputs retain identity951.
Terminal-pattern escape errors remain lazy, including the release's ordering
after `%` consumes adjacent `_` wildcards; exhausted literal input does not
globally validate unreachable pattern suffixes.


## REGTYPE — change note

REGTYPE is an unsigned four-byte catalog identifier (OID2206, array2211),
not TEXT, INTEGER or a composite value. Native scalar and array results export
owned strings with REGTYPE/_REGTYPE metadata; NULL exports nil. Domains retain
their logical authority while scalar metadata follows REGTYPE; array-of-domain
metadata uses the actual uppercase associated-array name.

Input recognizes exact `-` as zero and digit-only OIDs through the existing
unsigned OID codec (including octal). Other input is a complete standalone type
name: SQL aliases, quotes, comments, qualification, current-database prefixes,
modifiers and array syntax use the shared grammar. Modifiers are validated then
discarded; provider-less modifiers fail42601. `%TYPE` is not accepted.
SQL aliases force pg_catalog; ordinary internal names follow the effective
type search path. Explicit schema lookup checks USAGE; numeric input needs no
existing referent. Lookup alone does not require type USAGE.

All 621 released/initialized identities are represented, including catalog row
types, information_schema domains and associated arrays. Unsupported builtin
values remain unsupported: lookup identity alone does not authorize storage,
NULL construction, extra catalog projections or adjacent DDL. Supported user
table/view row codecs and their lookup-only arrays publish, rename, move and drop
with the actual owning relation. Ordinary sequences and indexes have no row type.

INT2/INT4/INT8→REGTYPE and OID↔REGTYPE are implicit; REGTYPE→INT4/INT8
are assignment casts. INT4 preserves unsigned bits; INT8 checks range.
TEXT/VARCHAR IO is explicit, not an implicit TEXT assignment.
Equality, grouping and ordering use unsigned identity, never display spelling.
Visible user names are unqualified; off-path/shadowed names are qualified and
quoted. Builtin SQL display aliases are canonical, arrays spell element[].
Unknown identities remain decimal and zero spells `-`.

Selected REGTYPE-to-character IO conversions, including domain-wrapped sources
and INSERT/UPDATE destination assignments, demand the same statement-owned
catalog/path snapshot as explicit TEXT/VARCHAR casts. Defaults and admitted
ALTER-column conversions retain that output demand too. Numeric/OID conversions
do not demand symbolic output. A failed runtime conversion does not leak its
argument frame or authority into a later execution of the same held statement;
NULL remains strict, and a subsequent valid argument uses fresh statement output
authority without consulting a live catalog during formatting.

Held literal analysis retains its complete OID group while effective type
lookup authority is unchanged, even after referent drop/recreation. Changed
path/role authority/database reanalyzes the group atomically; public prepared
metadata stays immutable. Literal modifier warnings occur during analysis;
frame warnings carry their actual portal slot, runtime TEXT casts do not.
Output is captured before returning rows, so held cursors never consult a later
live catalog. See [driver](DRIVER.md#regtype-input-and-held-descriptions) and
[fixture persistence](FIXTURES.md#regtype-references-and-row-identity).

