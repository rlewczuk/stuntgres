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
| REGCLASS, REGTYPE | Captured symbolic `string` |

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

Range and multirange datums own canonical endpoints/members and subtype
descriptors from their generation. Native exports string with no Length or
DecimalSize, despite numeric subtypes. Nil/typed-nil bytes are NULL; nonnil empty
bytes are malformed input. Their internal normalization comparators do not
provide SQL comparison, grouping or indexing. Range/multirange arrays, temporal
ranges and user-defined range creation remain outside native execution.

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
creation exemption. General enum-array storage and `pg_enum` queries remain
excluded.

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

Internal CHAR catalog output uses string transport, not BPCHAR semantics or a
physical ABI. High-byte output is printable escaped text. The narrow XID carrier
in `pg_prepared_xacts` exports fresh decimal bytes, not OID's int64; it does not
supply general XID storage, casts, arrays or ordering. Nonnull VOID result output
is an empty string with VOID metadata, not a TEXT alias; VOID storage/input and
arbitrary binary receive remain excluded.

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

Stream codecs do not add table admission for raw internal CHAR/XID, enum arrays
or the lookup-only MONEY array. Private descriptor tests are independent of
public table admission. These helpers are not protocol serving or disk/WAL
persistence.

## Logical persistence and testing

Dump/Reset/Restore preserves owned values, descriptor associations and captured
result snapshots through atomic private staging. Invalid input, owner resolution,
cancellation or final publication failure leaves the live catalog unchanged.
Canonical dump presentation is independent of session float precision; temporal
and monetary settings are not serialized. Restore keeps live settings.

These are process-local logical controls, not physical durability, PostgreSQL
ABI, external `pg_dump` formats or a network server. Native transport assertions
and pinned pq raw-codec assertions remain separate in the reference suite.
Selected families include `TestReferenceName`, `TestReferenceUUID`,
`TestReferenceJSON`, `TestReferenceJSONB`, `TestReferenceComposite`,
`TestReferenceEnum`, `TestReferenceIntegerRange`, `TestReferenceNumericRange` and
`TestReferenceRegclass`; run the relevant `make check RUN='...'` selector both
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
