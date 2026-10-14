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

### JSON conversion and heterogeneous builders

The `TestReferenceJSONCreate` scenarios specify exactly `to_json`, `to_jsonb`,
`array_to_json`, `row_to_json`, `json_build_array`, `jsonb_build_array`,
`json_build_object`, and `jsonb_build_object`. They independently assert typed
recursive conversion, source-preserving JSON versus canonical JSONB, escaping,
NULL and pretty formatting, heterogeneous/coerced keys and duplicates, flat
explicit VARIADIC expansion, complete diagnostics, held parameters, live temporal
output settings, raw/empty-result metadata and stored consumers. Native-only
control phases retain eager rows through Reset/Restore and replay actual Dump SQL
on PostgreSQL; they do not invent a PostgreSQL control API.
VARBIT/range/multirange conversion witnesses obtain values through admitted typed
column input, not additional explicit-cast capabilities. View deparse is proved
by replaying the actual emitted definition and checking independent result values
and types; redundant cast spelling is not a behavioral assertion. Stored replay
must preserve explicit VARIADIC expansion, and prepared reanalysis must retain
the occurrence-owned RECORD parameter shape rather than catalog lookup-only RECORD.

Select each bounded set in both modes under an external 900-second deadline:

```sh
make check RUN='^TestReferenceJSONCreate(Conversion|Escaping|Builders|Environment)$'
make check REFERENCE=1 RUN='^TestReferenceJSONCreate(Conversion|Escaping|Builders|Environment)$'
make check RUN='^TestReferenceJSONCreate(Diagnostics|Parameters|Metadata|Consumers|Controls)$'
make check REFERENCE=1 RUN='^TestReferenceJSONCreate(Diagnostics|Parameters|Metadata|Consumers|Controls)$'
make check RUN='^TestReferenceJSONCreateFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceJSONCreateFeatureSmoke$'
```

Feature smoke expects all eight functions on direct and prepared routes, exact
JSON/JSONB types and bytes, duplicate-key distinctions, pretty/multidimensional
VARIADIC results, bound escaping, live UTC/Los_Angeles timestamp output, full
odd-arity/NULL-key errors and held-statement recovery, stored calls and native
retention/replay. Passing assertions and the normal package summary are the
expected observations. Arbitrary user JSON casts, composite arrays, SQL/JSON
constructors, processing operators and aggregates remain outside this family.

The eight calls are intrinsically STABLE even with constant or empty inputs.
Index/generated consumers must reject them rather than silently treating scalar
conversion as immutable. Unary anyelement/anyarray calls require a resolved type;
builders accept ordinary unknown literals but do not infer bare parameters from
Go values. `row_to_json`'s fixed RECORD formal can contextualize an untyped
parameter: typed SQL NULL is strict, but nonnull text fails the anonymous-record
input boundary with the ordinary portal parameter diagnostic. This is not an
anonymous-record text codec or a storage capability.

Supplemental typed-provider, descriptor generation, subtree sharing, cancellation,
deep traversal, binding/visitor/retag and lexer provenance units use:

```sh
make check RUN='^TestJSONCreate'
make check RUN='^TestJSONObjectParserConsumersAndBoundaries$'
```

These component units supplement the shared public definitions; they do not
replace either selected reference mode or the actual feature smoke.

### JSON kind, array length and object keys

The twelve `TestReferenceJSONType` definitions specify the six selected
`json`/`jsonb` kind, immediate-array-length and object-key functions. Expectations
distinguish SQL NULL from JSON null, preserve JSON duplicate/source-order keys
and JSONB canonical UTF8-byte-length order, and check TEXT/INT4 results and
INT8 ordinality. Shared direct/prepared and same-held-statement bodies cover
complete diagnostics, signature resolution, SRF zip/padding/lateral stages,
computed metadata, actual column authorization, stored consumers and atomic
late failures. Native-only control phases retain cursors across replacement and
replay actual emitted SQL on PostgreSQL.

The same scenario body runs on every available backend. Metadata expectations
remain independent: native's type-owned unbounded TEXT contract and pinned pq
both expose Length `(9223372036854775807, true)`, including empty/all-NULL results.
INT4/INT8 Length and DecimalSize remain absent; backend ScanTypes stay separate.
Computed projections retain unknown nullability. Native column-derived outputs
retain their source declarations (including nullable view columns); pq Nullable
remains unknown. Retained control cursors assert these distinct contracts before
and after replacement, using explicit source-column expectations. Fresh native
restored readers and actual PostgreSQL emitted-SQL replay readers also attach
full metadata hooks on direct/prepared stored scalar and JSON/JSONB key-view
selections. Representative empty stored/view selections and newly evaluated
default/generated columns retain the same descriptor assertions before iteration.
Prepared DDL placement errors occur at execution, unlike SELECT analysis errors.
ACL setup makes the schema visible using real PostgreSQL USAGE or native schema
ownership without changing the bootstrap-owned table or granting SELECT.
The pinned reader identity is asserted directly as NAME from `current_user`,
without requiring an adjacent NAME-to-TEXT cast.

Select these small groups in both modes, each under an external 900-second
deadline:

```sh
make check RUN='^TestReferenceJSONType(Kinds|Lengths|Keys)$' REFERENCE=1
make check RUN='^TestReferenceJSONType(Diagnostics|Typing)$' REFERENCE=1
make check RUN='^TestReferenceJSONType(Parameters|Metadata|ACL)$' REFERENCE=1
make check RUN='^TestReferenceJSONType(SRF|Consumers|Controls|FeatureSmoke)$' REFERENCE=1
```

Run the identical selectors without `REFERENCE=1` for native coverage.
Every completed changed definition requires both modes before review. Feature smoke:

```sh
make check RUN='^TestReferenceJSONTypeFeatureSmoke$'
make check RUN='^TestReferenceJSONTypeFeatureSmoke$' REFERENCE=1
```

Smoke asserts kinds/nulls, nested lengths, ordered duplicate-vs-canonical keys,
held error/recovery frames, SRF wrappers/zip/lateral/placement, metadata,
authorization, unchanged sentinel after late decoding failure and native
replacement/emitted-SQL replay. Success is quiet package success with every
independent assertion satisfied.
No adjacent JSON operators, constructors or generic catalog enumeration are
asserted by this family.

Supplementary provider/engine/parser units assert concrete ownership, strict
typed NULLs, eager key validation without JSONB numeric conversion, immutable
payload retention, bounded token/nested-scan and prepublication cancellation,
single child evaluation, real zip/padding and complete map/retag/deparse traversal.
They call internal providers/nodes directly; public SQL remains in the twelve
reference definitions.

```sh
make check RUN='^TestJSON(Type|Keys)'
make check RUN='^TestJSONFunction'
make check RUN='^TestJSONLexerEscapedStringCancellation$'
make check RUN='^TestReferenceJSONType(Parameters|SRF)$' RACE=1
```

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

### Array search and membership

The shared `TestReferenceArraySearch*` scenarios cover only `array_position`,
`array_positions`, `array_remove`, `array_replace`, `@>`, `<@` and `&&`.
Expectations were independently reasoned and resolved against pinned PostgreSQL
18.0 before production work; the completed scenarios contain no discovery filter
or observation recorder. Exact rows, raw array families, type names and complete
diagnostics are asserted independently on every backend before parity.

Search/removal/replacement match NULL elements. Position returns actual
subscripts with an inclusive optional INT4 start; positions distinguishes a NULL
array from an empty result. Removal keeps surviving order/lower bound and makes
all-removed results canonical empty arrays. Search/removal reject multiple
dimensions; replacement retains shape. Membership ignores shape/bounds/order/
duplicate multiplicity, never matches NULL elements and is strict for whole NULL
arrays. Compatible function leaves can widen; independently different operator
array types do not. Equality-provider lookup and actual collation comparison
have distinct demand boundaries, including JSON empty/NULL and NULL-only strings.
When a domain element is itself array-valued, reached equality retains native
`0A000: unsupported array element type`, independently of PostgreSQL's supported
result. Earlier NULL/empty/search fast paths remain usable. This does not invent
missing PostgreSQL equality or add an ordinary native array comparator.

TypesAndFrames exercises actual held Prepare handles across varied parameters,
execution error and same-handle recovery. StoredConsumers covers defaults,
generated/CHECK/index/view expressions, held reads, scalar aggregate/group/filter,
join/CTE/correlation, mutation RETURNING and exact renamed-view deparse.
AuthorityAndControls covers all-child column privileges, revocation/regrant,
tuple atomicity, ordinary cancellation, actual emitted-SQL PostgreSQL replay,
native Reset/Restore, retained rows and failed later import preserving prior data.
Native controls are explicit native-only phases, not invented PostgreSQL APIs.
FilterDiagnostics preserves ordinary-child → FILTER Boolean analysis → signature/
nonaggregate diagnostic order, with full character positions and Prepare stages.
NestedViews covers explicit source casts, nested INT→BIGINT→NUMERIC argument
widening, text-collation authority, exact initial/renamed definitions and actual
safe Dump replay/catalog replacement. Safe replay's frozen source aliases are
distinct from the original source spelling; exact post-replay definitions use
the renderer's independently specified representation, not allocator goldens.
The private ViewCoercions regression renders and evaluates both original and
remapped frozen trees across distinct type generations and collation identities,
then re-renders the original to assert retained ownership. Immutability also
requires canonical all-removed results to retain no survivor backing storage.

Select each bounded set in both modes with an external 900-second deadline:

```sh
make check RUN='^TestReferenceArraySearch(Values|Membership)$'
make check REFERENCE=1 RUN='^TestReferenceArraySearch(Values|Membership)$'
make check RUN='^TestReferenceArraySearch(TypesAndFrames|Metadata|Diagnostics)$'
make check REFERENCE=1 RUN='^TestReferenceArraySearch(TypesAndFrames|Metadata|Diagnostics)$'
make check RUN='^TestReferenceArraySearch(StoredConsumers|AuthorityAndControls)$'
make check REFERENCE=1 RUN='^TestReferenceArraySearch(StoredConsumers|AuthorityAndControls)$'
make check RUN='^TestReferenceArraySearch(FilterDiagnostics|NestedViews)$'
make check REFERENCE=1 RUN='^TestReferenceArraySearch(FilterDiagnostics|NestedViews)$'
make check RUN='^TestReferenceArraySearchFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceArraySearchFeatureSmoke$'
```

FeatureSmoke expects negative-bound position/positions/removal/replacement,
duplicate containment and flattened multidimensional overlap, NULL/empty
metadata, exact rank/NULL-start diagnostics, same-held-statement recovery and
stored/native-control round trips. Component proof supplements public scenarios:
`make check RUN='^TestArraySearch(Values|Demand|Immutability|Cancellation|Parser|ViewCoercions)$'`.
Ordinary array comparison/order/hash, adjacent array functions, new element
codecs/equality providers and unsupported collation backends remain excluded.

### JSONB text-array object construction

The `TestReferenceJSONBObject` families cover exactly `jsonb_object(text[])`
and `jsonb_object(text[], text[])`, using one shared body in both modes.
Independent expectations follow PostgreSQL 18 Table 9.49 and REL_18_0
`jsonb.c`/`jsonb_util.c`: flat/two-column/parallel forms ignore lower bounds;
empty arrays produce `{}`; whole-array NULL is strict; shape/count validation
precedes NULL-key rejection. Values are always strings or JSON null, not parsed
JSON primitives. Keys normalize by UTF8 byte length then bytes; the last duplicate
wins, including a final NULL.

Values and diagnostics cover both direct/prepared routes. Held statements cover
inferred TEXT[] slots, repeated slots, malformed portal inputs, recovery and byte
ownership. Metadata checks raw cells and computed descriptors before iteration,
including empty/NULL results and detached scans. Authorization checks both
argument columns even on empty inputs. Stored consumers cover defaults, stored/
virtual generated columns, CHECK/view/index, CTAS/materialized view, structural
JSONB equality/grouping, late-write atomicity and savepoint recovery. Deparse
assertions preserve unquoted `jsonb_object` calls; native Dump SQL retains
`pg_catalog.jsonb_object`. Actual native Dump/Reset/Restore preserves retained
rows/metadata, deterministically redumps, replays unmodified emitted SQL on PG,
and rejects late failing/parameterized Restore input without publishing it.

Select all new definitions in both modes, each with an external 900-second deadline:

```sh
make check RUN='^TestReferenceJSONBObject(Values|Diagnostics|Typing)$'
make check RUN='^TestReferenceJSONBObject(Values|Diagnostics|Typing)$' REFERENCE=1
make check RUN='^TestReferenceJSONBObject(Parameters|Metadata|ACL)$'
make check RUN='^TestReferenceJSONBObject(Parameters|Metadata|ACL)$' REFERENCE=1
make check RUN='^TestReferenceJSONBObject(Consumers|Deparse|Controls|FeatureSmoke)$'
make check RUN='^TestReferenceJSONBObject(Consumers|Deparse|Controls|FeatureSmoke)$' REFERENCE=1
```

The JSONObject sets above must also run in both modes after changes to the
shared binder/shape provider: JSON keeps its original order, duplicates and
separator spaces. Component units independently check structural payload/hash,
descriptor boundaries, cancellation without partial publication, retagging,
rendering, evaluation order and parser admission.

Actual feature smoke:

```sh
make check RUN='^TestReferenceJSONBObjectFeatureSmoke$'
make check RUN='^TestReferenceJSONBObjectFeatureSmoke$' REFERENCE=1
```

Expected observations are normalized flat/2-D/shifted-parallel JSONB output,
duplicate-last-NULL and string primitives, computed JSONB metadata, SAME held
empty/NULL/shape-error/codec-error/recovery frames, structural equality/grouping,
stored recomputation, unchanged sentinels after a late write failure and actual
native replacement with emitted-SQL replay. No printed JSON transcript is
required; success is the independent assertions and quiet package exit0.
SQL/JSON syntax, other object builders, extraction/operators/JSONPath and generic
builtin catalog enumeration remain outside this feature. Whole-project Docker-
free verification remains `make quickcheck`.


### Scalar list and array comparisons

The shared `TestReferenceArrayCompare*` scenarios cover §9.25 scalar
`IN`/`NOT IN` lists and comparison-operator `ANY`/`SOME`/`ALL` over an array
expression. Independently specified rows/types/full diagnostics cover NULL,
empty arrays, comparable scalar elements, direct and prepared routes, metadata,
held frames and recovery. There is no backend discovery filter.
Array-to-array comparison/order remains excluded.
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
