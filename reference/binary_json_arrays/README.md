# reference/binary_json_arrays

## Testing

Follow the [shared testing guide](../../docs/TESTING.md) for common policy,
fixture usage, and oracle-first workflow. Run the commands below from the
repository root.

### JSON array length

`TestReferenceJSONArrayLength` covers exactly `json_array_length(json)` and
`jsonb_array_length(jsonb)`: INTEGER top-level counts, strict SQL NULL, JSON null
as a scalar error, own-family inference/domains and explicit cross-family casts.
Wrong object roots assert complete `22023` non-array diagnostics; other roots
assert scalar diagnostics, including absent Position. Invalid signatures/input,
character positions and source-column/schema authorization remain exact.

Direct/prepared/held routes independently assert INT4 metadata, raw int64 cells,
specialized Scan, parameter recovery and constant/bound-parameter versus column
demand. Stored consumers include default/generated/CHECK/view definitions, both
JSON and JSONB length expression indexes, grouping/aggregates and returning
writes. Late failures preserve the original rows. Cancellation observes actual
stored-JSON provider/server admission, joins the worker and proves SAME-owner
held read/write recovery; it does not substitute pg_sleep or timing sleeps.

Explicit native demand boundary: the first held `SELECT f($1) AS n WHERE FALSE`
with `{}` raises PostgreSQL's complete non-array `22023` (ERROR/ERROR, no optional
fields), while native returns zero rows and checks the complete `n`/INT4 metadata.
Both functions execute the same held SQL route with independent backend
expectations; neither route is skipped and no parity suppression is used.
PostgreSQL folds the bound immutable call in its custom plan; native parameters
remain runtime-owned under the existing [optimizer boundary](../../docs/DIALECT.md).
This does not broaden native custom/generic planning or special-case function
timing. Demanded held errors, constant wrong-root `WHERE FALSE` errors and
filtered column no-demand outcomes retain their shared contracts.

Native-only phases exercise actual Dump/Reset/Restore, retained integer cursors,
failed-Restore atomicity and nested-parameter rejection. In real mode, emitted
native dump SQL is replayed on PostgreSQL and independently checked; PostgreSQL
has no simulated native control API. Calls remain symbolic in stored definitions.
These tests do not imply adjacent JSON operators, extraction, constructors or
scalar-function FROM support.

Run the completed shared family in both modes under external 900-second deadlines:

```sh
make check RUN='^TestReferenceJSONArrayLength(Values|Diagnostics|Typing|Parameters|Metadata|ACL|Consumers|Controls|Cancellation|FeatureSmoke)$'
make check RUN='^TestReferenceJSONArrayLength(Values|Diagnostics|Typing|Parameters|Metadata|ACL|Consumers|Controls|Cancellation|FeatureSmoke)$' REFERENCE=1
```

Feature smoke:

```sh
make check RUN='^TestReferenceJSONArrayLengthFeatureSmoke$'
make check RUN='^TestReferenceJSONArrayLengthFeatureSmoke$' REFERENCE=1
```

Smoke expects literal counts0/5, held []/NULL/object/malformed/recovery frames,
typed projection/predicate/default/generated/view rows, late-write22023 with
unchanged sentinels, and native replacement plus actual emitted-SQL replay.
Whole-project Docker-free verification remains `make quickcheck`.


### JSON text-array object construction

The `TestReferenceJSONObject` families cover exactly the one- and two-text-array
`json_object` overloads. Expectations are explicit JSON text (including order,
duplicates, escaping and separator spaces), complete diagnostics, inferred held
parameters, computed metadata, used-column privileges and symbolic stored
consumers. Qualified explicit final `VARIADIC` text[] syntax, including inferred
held parameters, selects the same one- or two-argument overload; it does not
imply a variadic catalog signature.
Stored deparse assertions preserve quoted `"json_object"` names, shifted array bounds
and exact default/generated/CHECK/view/index formatting. Default/generated
definitions are identified by independently expected `pg_attrdef.adnum` values,
ordered by column number; relation discovery uses `pg_class` OID joins.
Public view deparse uses the quoted unqualified name, while symbolic Dump SQL
retains `pg_catalog.json_object` qualification for replay. Native control phases
use actual Dump/Reset/Restore and replay emitted SQL on PostgreSQL; they do not
invent a PostgreSQL control API.

Select these bounded sets in both modes, each under an external 900-second deadline:

```sh
make check RUN='^TestReferenceJSONObject(Values|Diagnostics|Typing)$'
make check REFERENCE=1 RUN='^TestReferenceJSONObject(Values|Diagnostics|Typing)$'
make check RUN='^TestReferenceJSONObject(Parameters|Metadata|ACL)$'
make check REFERENCE=1 RUN='^TestReferenceJSONObject(Parameters|Metadata|ACL)$'
make check RUN='^TestReferenceJSONObject(Consumers|Deparse|Controls|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceJSONObject(Consumers|Deparse|Controls|FeatureSmoke)$'
```

Feature smoke uses `RUN='^TestReferenceJSONObjectFeatureSmoke$'` in both modes.
It expects flat/2-D/parallel construction, JSON metadata, same-held-statement
NULL/error/recovery frames, generated/default/view consumers, late-write atomicity
and native replacement with emitted-SQL replay. SQL/JSON constructor syntax,
JSONB constructors, JSON comparison and generic builtin catalog enumeration are
not capabilities asserted by this family.

### Scalar list and array comparisons

The shared `TestReferenceArrayCompare*` scenarios cover §9.25 scalar
`IN`/`NOT IN` lists and comparison-operator `ANY`/`SOME`/`ALL` over an array
expression. Independently specified rows/types/full diagnostics cover NULL,
empty arrays, comparable scalar elements, direct and prepared routes, metadata,
held frames and recovery. There is no backend discovery filter. Ordinary
array-to-array comparison/order and new §9.19 functions/operators remain excluded.
Array forms require both a scalar comparator and an existing concrete associated
array/codec. MONEY/INET/CIDR scalar-list positives retain their explicit native
array-element capability failures; catalog association alone is not execution.

StoredConsumers exercises views and repeated held reads, defaults, generated
columns, CHECK constraints, expression indexes, mutation RETURNING, aggregate
arguments/filters/group keys, joins and correlated children. AuthorityAndControls
uses actual column GRANT for late-list/array children, empty-source and decisive
expressions, missing children, dependency rename/DROP, staged-write atomicity and
precanceled queries. Explicit native phases exercise emitted SQL replay on
PostgreSQL when available, Reset/Restore with retained cursor metadata/results,
domain/collation captures, RHS-only tableoid generated-owner remapping and rejected
or canceled Restore without changing stored data.
Frozen IN/NOT IN regressions also preserve independently typed copies of an
unknown left literal: enum label `'1'` and integer `1` remain distinct comparison
inputs through views, defaults, generated columns and actual replay/Restore.
The bounded VALUES scalar-input path uses existing qualified
`OPERATOR(pg_catalog.=) ANY(...)` syntax; ordinary comparison spellings are
exercised by INSERT SELECT and the other expression consumers. Malformed
prepared array input retains the same slot-specific portal Context in both modes.

Diagnostics also exercises competing invalid IN/NOT IN inputs and unsupported
JSON comparators with malformed unknown ANY/SOME/ALL input. Full records prove
RHS-before-LHS batch conversion and operator-before-codec analysis precedence,
with populated/empty sources, direct/Prepare failure phases and same-route
recovery; this does not promise runtime operand evaluation order.

Run the selected completed definitions in both modes, with an external
900-second deadline per command:

```sh
make check RUN='^TestReferenceArrayCompare(ListTruth|QuantifiedTruth)$'
make check REFERENCE=1 RUN='^TestReferenceArrayCompare(ListTruth|QuantifiedTruth)$'
make check RUN='^TestReferenceArrayCompare(TypesAndFrames|Metadata|Diagnostics)$'
make check REFERENCE=1 RUN='^TestReferenceArrayCompare(TypesAndFrames|Metadata|Diagnostics)$'
make check RUN='^TestReferenceArrayCompare(StoredConsumers|AuthorityAndControls)$'
make check REFERENCE=1 RUN='^TestReferenceArrayCompare(StoredConsumers|AuthorityAndControls)$'
make check RUN='^TestReferenceArrayCompareFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceArrayCompareFeatureSmoke$'
```

The feature smoke expects seven stored input rows with fixed IN/NOT IN and
ANY/SOME/ALL Boolean/NULL results, filtered IDs 1 and 7, empty-result BOOL metadata,
held frames with empty/NULL arrays, left-inclusive integer widening, non-1 rank-2
reduction, complete shape/operator diagnostics and valid recovery. These commands
describe required proof, not a claim that the definitions have been verified.
Whole-project verification remains `make quickcheck`; selected race proof may
add `RACE=1` to AuthorityAndControls. Native controls are not PostgreSQL APIs.

### Array aggregation

Array aggregation uses `RUN='^TestReferenceArrayAgg'` with both
`make check` and
`make check REFERENCE=1` (900 seconds each).
Feature smoke uses `RUN='^TestReferenceArrayAggFeatureSmoke$'` in both modes.
These shared scenarios independently assert scalar NULL inclusion, array rank
and bounds, metadata/domain identity, complete diagnostics, direct/prepared
frames, source ACLs and INSERT atomicity; native controls remain explicit phases.
Public source-authority scenarios use actual table/column GRANT and the same
consumer identity in both modes; owner substitutions are not positive ACL proof.

### JSONPath typed-input reference contracts

The JSONPath family uses cohesive, independently expected case tables for
canonical grammar output, input-time regular-expression syntax, complete logical
diagnostics and their direct/prepared literal-versus-bind phases. Consumer
scenarios cover held inferred INSERT/UPDATE/CAST parameters, repeated slots,
owned bytes, SQL NULL versus path `null`, recovery and failed-write atomicity.
Character IO asserts TEXT/VARCHAR canonical output, explicit VARCHAR/BPCHAR
truncation, multibyte character sizing, BPCHAR padding and SQL NULL. Encoding
errors assert the full protocol diagnostic, including portal Context without a
parameter value; malformed paths assert the measured slot-specific Context.

Catalog OIDs are checked separately from driver metadata: lib/pq exposes blank
type names and interface{} ScanType for JSONPath results, whereas native results
must expose JSONPATH (or _JSONPATH) and string ScanType. The shared helper asserts
each backend's metadata and raw representation before normalization. Composition
also retains native-only Dump/Restore phases and emitted-SQL replay on PostgreSQL.
These are input/representation contracts, not path or regex evaluation claims.

JSONPath fixture IDs are unconstrained INT values made unique by the scenarios;
these tests do not exercise primary-key syntax or constraint enforcement.
Scalar/array catalog and attribute identities use separate equality-filtered
queries on PostgreSQL, retaining every independently expected OID, typmod,
collation and domain-base field. Their equality filters are inquiry witnesses,
not a restriction on scalar IN-list expression support.
Native explicitly asserts that the two named relations `pg_catalog.pg_type` and
`pg_catalog.pg_attribute` remain absent (42P01, exact English message and original
relation Position), in both direct and prepared representation setup. Other
implemented catalog views do not imply these inquiry relations exist. Exact native
descriptor4072/4073 and reciprocal array identities remain covered by provider
units; all shared native JSONPATH/_JSONPATH metadata, IO, composition and controls
remain asserted without a skip. Equality, ordering and grouping
must reject rather than compare canonical output; grouping diagnostics retain
the declared scalar, array or domain name and original SQL source position.

Run all changed definitions in both modes, with a 900-second deadline per command:

```sh
make check RUN='^TestReferenceJSONPath'
make check REFERENCE=1 RUN='^TestReferenceJSONPath'
make check RUN='^TestReferenceJSONPathFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceJSONPathFeatureSmoke$'
```

The feature smoke expects inferred repeated-slot insertion, strict indexed-path
and regex-flag canonicalization, exact regex diagnostics, unchanged data after
bad UPDATE/late bad INSERT, subsequent valid recovery, and distinct NULL/path-null
rows. No discovery filter, success logging or observation fallback is retained.
Change note: JSONPath oracle-discovery envelopes are replaced with quiet shared
reference assertions; quick-mode failure precedes production implementation.

### Binary conversion

Binary conversion uses independent pinned18.0 goldens for all128 directed pairs,
all42 identities, format/padding boundaries, complete diagnostics, held statements,
volatility and late-write atomicity. Native lifecycle phases retain readers and
metadata across replacement and failed/canceled imports, and replay actual emitted
SQL on PostgreSQL. Select the whole affected family and smoke in both modes,
each invocation under900 seconds:

```sh
make check RUN='^TestReferenceBinaryConversion'
make check REFERENCE=1 RUN='^TestReferenceBinaryConversion'
make check RUN='^TestReferenceBinaryConversionFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceBinaryConversionFeatureSmoke$'
make check RUN='^TestBinary(Conversion|Encoding|Format)'
make check RUN='^TestBinaryConversion'
make check RUN='^TestBinaryConversion'
```

The shared hex/escape cores also serve BYTEA input; run existing
`^TestReferenceBytea` regressions selectively in both modes before review.

Stored-view metadata uses the established native declared nullable contract
`true,true`; pq reports unknown `false,false`. Computed conversion expressions
continue to require unknown nullability. Actual emitted-SQL replay uses the pq
contract explicitly, not metadata captured from the native backend.
Replay objects have bounded deferred cleanup before the next backend body,
including failure exits; the shared scenario resets schemas per test, not backend.

`TestReferenceBinaryConversionActiveCancellation` separately exercises admitted
aggregate encode reads and ordered staged convert writes on a32MiB LATIN1 source.
Native observes the worker's actual provider stack before canceling its context;
PostgreSQL observes the actual query active on the held backend PID and uses
server cancellation, preserving the same session. Workers have bounded joins on
every exit. Assertions require no successful cursor/result, unchanged sentinel,
ordinary native context cancellation or the complete PostgreSQL57014 diagnostic,
then independently expected read sum4 and write count2/UTF8 bytes through the
same pre-held read/write statements. No pg_sleep substitute or production delay
is used. Existing precanceled/expired contexts remain separate coverage.

```sh
make check RUN='^TestReferenceBinaryConversionActiveCancellation$'
make check REFERENCE=1 RUN='^TestReferenceBinaryConversionActiveCancellation$'
```

The no-bind direct repeated-parameter witness asserts an explicit inherited
transport boundary (PG42P02 Position16 versus native42P08 Position19/Detail
`bytea versus name`); genuine Prepare remains shared42P08. See DIALECT's binary
conversion note. Codec diagnostics and all independent pair/identity goldens
remain unchanged.

### BYTEA access completed definitions

`TestReferenceBinaryAccess*` covers zero-based LSB orientation, raw count and
reversal, setter truncation/immutability, bounds and input diagnostics, overload
resolution, authority, empty/NULL metadata, held frames, consumers and native
symbolic controls with actual emitted SQL replay on PostgreSQL. PostgreSQL
expectations are independent; BIT execution is asserted only there, with the
native capability error asserted separately. Portal Context and pq/native BYTEA
metadata/empty-buffer differences remain explicit, not normalized goldens.

Run each selective quickcheck under a 900-second deadline:

```sh
make check RUN='^TestReference(BinaryAccess|StringPad).*$'
make check REFERENCE=1 RUN='^TestReference(BinaryAccess|StringPad).*$'
make check RUN='^TestReferenceBinaryAccessFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceBinaryAccessFeatureSmoke$'
```

StringPad protects the reused reverse provider and shared parser/binder.
Feature smoke exercises the released six-function projection, held frames,
bounds/new-bit failure and recovery, retained source, empty/NULL metadata and
controls replay. These gates do not replace root quickcheck or post-merge
finalcheck (one 1800-second deadline).

### BYTEA access oracle-first contract

`TestReferenceBinaryAccess` defines the BYTEA `bit_count`, `get_bit`,
`get_byte`, `set_bit`, `set_byte` and existing `reverse` contract. Independent
release/manual and raw-octet goldens precede comparison: LSB-first bit offsets,
INT8 bit indexes/counts versus INT4 byte indexes/getters, low-eight-bit byte
replacement, strict NULL, source ownership, complete errors, overload boundaries,
authorization, metadata, statement/savepoint atomicity and stored consumers.
The existing StringPad reverse definitions remain unchanged.

During initial discovery only, the shared scenario helper temporarily excludes
native backends. This is not native support or passing verification: remove that
filter after pinned PostgreSQL observations and before the native failing proof,
and never integrate the filter. Body-error stages, portal input Context,
ambiguous overloads, diagnostic precedence and complete metadata flags require
root observations before production implementation. Native controls are defined
now but are not exercised by the PostgreSQL-only discovery phase.

Run each command with a separate 900-second deadline, in this order:

```sh
make check REFERENCE=1 RUN='^TestReferenceBinaryAccess.*$'
# After removing the temporary filter: establish native failing proof.
make check RUN='^TestReferenceBinaryAccess.*$'
# After implementation: both completed shared definitions and reverse regression union.
make check RUN='^TestReference(BinaryAccess|StringPad).*$'
make check REFERENCE=1 RUN='^TestReference(BinaryAccess|StringPad).*$'
make check RUN='^TestReferenceBinaryAccessFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceBinaryAccessFeatureSmoke$'
```

The composed smoke asserts the six-function released projection, orientation and
wrapping, raw held frames and unchanged storage, exact bounds/newbit errors with
recovery, empty/NULL metadata before iteration and symbolic native controls with
actual emitted SQL replay on PostgreSQL. Control replacement failures must leave
the destination dump byte-for-byte unchanged; parameters in every new argument
slot remain rejected. Quiet test success alone is not a discovery transcript.
