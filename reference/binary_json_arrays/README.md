# reference/binary_json_arrays

## Testing

Follow the [shared testing guide](../../docs/TESTING.md) for common policy,
fixture usage, and oracle-first workflow. Run the commands below from the
repository root.

### Bit-string scalar operators

`TestReferenceBitOperators` covers only `||`, `&`, `|`, `#`, unary `~`,
`<<`, `>>` and scalar comparisons (`=`, `<>`/`!=`, `<`, `<=`, `>`, `>=`).
BIT/VARBIT concatenation returns VARBIT/-1; bitwise operations return BIT/-1;
predicates return BOOL. Input declarations do not constrain expression width.
AND/OR/XOR require equal actual lengths with exact 22026 English diagnostics.
Different widths are legal for concat and lexical comparison. Shifts retain
width, reverse with negative counts and zero-fill for counts at/beyond width,
including INT32_MIN; integral shifts retain their separate modulo behavior.

Independent witnesses include empty/9-/65-bit payloads, all family pairings,
contextual literals/NULLs/parameters, complete diagnostics and API error stages,
analysis despite NULL/empty input, row-sibling evaluation, assignment and late
write atomicity, raw codecs/live metadata, SAME-held bind/error/NULL recovery,
and symbolic defaults/generated/check/view replay. Native stored view columns
retain known nullable metadata; ordinary computed projections and pq metadata
report unknown nullability. Native control phases use actual Dump/Reset/Restore
and replay the emitted SQL on PostgreSQL. `StoredNullFamilies` additionally pins
admitted BIT NULL, VARBIT NULL and nonnull VARBIT expression transports without
introducing bit casts. Grouping/hash, aggregate DISTINCT, ordering/index,
windows, arrays, general casts and get_bit/set_bit remain separate boundaries.

Run the same completed definitions in native and pinned PostgreSQL modes from
the repository root, with an external 900-second deadline:

```sh
make check RUN='^TestReferenceBitOperators(Values|Shifts|Comparisons|Typing|Diagnostics|MetadataAndHeld|StoredReplay|StoredNullFamilies|FeatureSmoke)$'
make check RUN='^TestReferenceBitOperators(Values|Shifts|Comparisons|Typing|Diagnostics|MetadataAndHeld|StoredReplay|StoredNullFamilies|FeatureSmoke)$' REFERENCE=1
make check RUN='^TestBitStringOperators'
make check RUN='^TestReferenceBitOperators(MetadataAndHeld|StoredReplay|StoredNullFamilies)$' RACE=1
```

Focused feature smoke (not a whole-suite alias):

```sh
make check RUN='^TestReferenceBitOperatorsFeatureSmoke$'
make check RUN='^TestReferenceBitOperatorsFeatureSmoke$' REFERENCE=1
```

Smoke independently expects concat1000101101, AND00001, OR11101, XOR11100,
NOT01110, left shift3→01000 and right shift2→00100; all six comparisons,
lexical prefix/leading-zero witnesses, typed NULL, exact mismatch22026 and
SAME-held recovery, VARBIT/BIT/BOOL metadata, and integer1<<32→1. Expected
successful checks are quiet exit zero; recorded root verification, not these
commands or source expectations, establishes exercised evidence.

### JSON object member expansion

`TestReferenceJSONEach` independently specifies `json_each(json)` and
`json_each_text(json)` ([PostgreSQL 18 JSON processing functions](https://www.postgresql.org/docs/18/functions-json.html#FUNCTIONS-JSON-PROCESSING)).
The shared scenarios cover duplicate top-level members, decoded keys, retained
JSON spelling/internal whitespace, decoded text strings, JSON null versus SQL
NULL, empty sets, wrong roots, processing-time Unicode errors and full English
diagnostics. SELECT targets retain one RECORD column; FROM exposes key TEXT and
value JSON/TEXT, including before iteration on empty/NULL inputs. Multi-column
ROWS FROM padding, ordinality, aliases/whole rows, lateral sources, target SRF
zipping, grouping, stored views, CTAS, authorization, atomic INSERT SELECT,
held-statement recovery, cancellation and actual native control replay have
explicit expected rows/types/counts rather than backend-parity-only checks.
The lifecycle witness distinguishes unfiltered late-array INSERT SELECT failure
(no prefix writes) from a left-only WHERE restriction that excludes that source
row before opening its correlated function. Typed multi-member definition lists
are consumed before the exact OUT-parameter redundancy diagnostic at the first
column name; malformed trailing definitions remain syntax errors.

The shared expectations were exercised against pinned PostgreSQL 18.0 before
production implementation. They retain exact Unicode diagnostics, phases,
positions and command counts. JSON and RECORD use pq interface{} ScanType;
raw payloads are owned bytes on pq and strings on native. The shared scenarios
run every available backend, without discovery filters or observation logging.
Oracle observations alone do not claim completed native or dual-mode proof.

```sh
make check RUN='^TestReferenceJSONEach(Values|Unicode|Typing|Diagnostics)$' REFERENCE=1
make check RUN='^TestReferenceJSONEach(Metadata|QueryStructure)$' REFERENCE=1
make check RUN='^TestReferenceJSONEach(ParametersAndLifecycle|Cancellation|AuthorityAndControls|FeatureSmoke)$' REFERENCE=1
```

Run the same three selectors without `REFERENCE=1` for native proof, and repeat
both modes after affected edits. Selected lifecycle/cancellation/control race
proof uses `make check RUN='^TestReferenceJSONEach(ParametersAndLifecycle|Cancellation|AuthorityAndControls)$' RACE=1`.
Provider/engine invariants use `make check RUN='^TestJSONEach'`.
Root owns execution with a 900-second external deadline; no agent refcheck.

Feature smoke:

```sh
make check RUN='^TestReferenceJSONEachFeatureSmoke$'
make check RUN='^TestReferenceJSONEachFeatureSmoke$' REFERENCE=1
```

Expected quiet exit zero: CREATE0/prepared INSERT3, duplicate key/value pairs
with JSON null versus SQL NULL and text decoding, ordered ordinality1–3,
empty/NULL metadata, one-column RECORD target, mixed scalar padding, reusable
statement error/recovery, concrete-member views/CTAS/source rename, and actual
native Dump/Reset/Restore/redump plus emitted SQL replay on the pinned oracle.
Native control phases intentionally do not execute on PostgreSQL. Oracle
observations cannot prove those phases or the Docker-free implementation.

Focused predecessor-demand safety coverage:

```sh
make check RUN='^TestReferenceJSONEach(PredecessorDemand|VolatileDemand)$' REFERENCE=1
make check RUN='^TestReferenceJSONEach(PredecessorDemand|VolatileDemand)$'
```

`PredecessorDemand` uses the same parameterized SELECT directly and through one
held prepared statement: exclude an invalid array predecessor and return the
object's two exact key/value rows; select the array and require the complete
execution-time 22023 diagnostic; reuse successfully after the error and after a
NULL-result qualification; select empty-object/SQL-NULL documents with full
empty-result metadata. Both selected JSON functions share the same scenario.

`VolatileDemand` has a three-member valid object and a whole WHERE predicate
that is left-dependent (`s.id=$1`) and volatile (`nextval(sequence)<=$2`).
With parameters1/2, the pinned PostgreSQL 18.0 post-expansion outcome is members
a/b at ordinals1/2, sequence last_value3/is_called=true. Incorrect whole-predicate
relocation to the predecessor would admit all three members and consume only
one sequence value. Direct and prepared executions use fresh sequences.
Both completed definitions run every available backend with independently
specified expectations; no discovery filters remain.

### JSON null-field stripping

`TestReferenceJSONStripNulls` specifies the JSON-only `json_strip_nulls(target,
strip_in_arrays DEFAULT false)` contract: recursive object stripping, optional
array stripping, bare JSON null versus SQL NULL, empty containers, duplicate
members, raw numeric spelling and processing-time string normalization/errors.
Shared direct/held prepared scenarios assert independent rows, metadata, counts,
English diagnostics, SQLSTATE, character locations and analysis/execution stages.
The function is STRICT STABLE in PostgreSQL 18.0, so generated/index consumers
reject it while DEFAULT/CHECK/view expressions remain symbolic and replayable.
Explicit scalar-final VARIADIC preserves fixed-formal resolution, not expansion;
named reordered final-target VARIADIC has the independently pinned 42883 record.
The `FullJoin` regression checks direct/prepared TEXT equality keys containing
both formal dependencies, including reordered named calls, and independently
expects matched and null-extended rows `(1,10)`, `(2,NULL)`, `(NULL,20)`.
`TestJSONStripNullsJoinDependencies` additionally checks target-only, flag-only,
NULL-target, same-side and cross-side masks in child-frame slots; strict NULL
and written argument order never erase the Boolean formal's dependency.

Root observed the pinned PostgreSQL contract before production implementation
and the native missing-feature failure afterward. These observations do not
replace completed dual-mode proof. No discovery filters remain. Native control
phases use real Dump/Reset/Restore, retained rows and actual emitted SQL replay
on PostgreSQL; public cancellation preserves ordinary native context errors
and independently observes PostgreSQL admission through the common fixture.

Run each selected set both without and with `REFERENCE=1`, under an external
900-second deadline:

```sh
make check RUN='^TestReferenceJSONStripNulls(Values|Nulls|Lexical)$'
make check RUN='^TestReferenceJSONStripNulls(Typing|TypingVariadic|Parameters|Diagnostics|Unicode)$'
make check RUN='^TestReferenceJSONStripNulls(Consumers|FullJoin|Metadata|ACL|Stable)$'
make check RUN='^TestReferenceJSONStripNulls(Controls|Atomicity|Cancellation|FeatureSmoke)$'
make check RUN='^TestJSONStripNulls.*$'
make check RUN='^TestReferenceJSONStripNulls(Parameters|Controls|Cancellation)$' RACE=1
```

Feature smoke: `make check RUN='^TestReferenceJSONStripNullsFeatureSmoke$'`
and the identical command with `REFERENCE=1`. Expected results include exact
nested stripping with retained/default and removed/true array nulls, JSON-null
and SQL-NULL metadata/scans, held named parameters, full invalid-input and
nonimmutable-index diagnostics, constraint atomicity and real native replay.
The expected output is quiet exit zero, not a feature-specific printed message.
Whole-project `make quickcheck` and post-integration finalcheck remain root gates.

### Bit string measurements and slices

The `TestReferenceBitStringFunctions` family specifies the seven functions in
[PostgreSQL 18 Table 9.15](https://www.postgresql.org/docs/18/functions-bitstring.html):
`bit_count`, `bit_length`, `length`, `octet_length`, `overlay`, `position`,
and `substring`. Independent expectations cover logical bit lengths/popcount,
byte rounding, bit-indexed slicing and search, strict NULLs, unrestricted BIT
result metadata, exact diagnostics/stages/character positions, retained statement
frames, assignment and late-write atomicity, and symbolic native control replay.
The feature smoke composes those permanent scenarios rather than duplicating SQL.
Count returns INT8; other measurements and position return INT4; slices/overlays
return BIT/-1. Negative overlay counts are legal, substring and overlay have
different INT4 end-overflow contracts, and empty/empty bit position returns zero.

The independently reasoned portable expectations were confirmed against pinned
PostgreSQL 18.0 before production implementation. The shared scenarios now run
every available backend without discovery filters or observation recording.
This oracle evidence does not claim native support or completed dual-mode proof.

```sh
make check REFERENCE=1 RUN='^TestReferenceBitStringFunctions(Measurements|Slices|Bounds)$'
make check REFERENCE=1 RUN='^TestReferenceBitStringFunctions(TypingDiagnostics|NullMetadata)$'
make check REFERENCE=1 RUN='^TestReferenceBitStringFunctions(HeldStatements|ConsumersControls|Cancellation)$'
make check REFERENCE=1 RUN='^TestReferenceBitStringFunctionsFeatureSmoke$'
make check RUN='^TestBitStringFunctions'
```

Run each same selector without `REFERENCE=1` for native failing proof and final
acceptance. Selected race proof adds `RACE=1` to the lifecycle/consumer group.
Native control phases call real Dump/Reset/Restore and replay the emitted SQL on
PostgreSQL after cleanup. Smoke expects quiet exit zero with the seven-function
projection, VARBIT routing, widths, bounds, held NULL/error/recovery and actual
symbolic controls/default demand independently satisfied. Initial portable
PostgreSQL observations do not prove these native phases; root quickcheck and
post-integration finalcheck remain separate required gates.

### JSONB existence

`TestReferenceJSONBExists` covers only `jsonb ? text`, `jsonb ?| text[]` and
`jsonb ?& text[]`: exact decoded top-level keys/immediate string elements/root
strings, no object values or recursive search. Whole NULL is strict; NULL array
elements are skipped and empty/all-NULL requests reduce to false/true. Request
shape, bounds and duplicates are irrelevant. Unknown `?`/`?&` infer both inputs;
two unknown `?|` inputs report full 42725, while either typed side disambiguates.
Invalid signatures retain full 42883 records and source/namespace authorization.

Direct/held typing, BOOL metadata, parameter recovery, consumers and atomicity
use independent oracle contracts. Direct computed BOOL projections report
unknown nullability (`false,false`) in both adapters; this does not change
native catalog-column nullability. Native-only Dump/Reset/Restore and retained
cursors are real controls; repeated restores must preserve both canonical Dump
SQL and stored results, including array conversions, typmods and domain casts.
Public `pg_get_viewdef` cast spelling is not a canonical native replay contract.
Real mode replays actual emitted native SQL on PG.
Active cancellation admits the reached `sqltype.jsonbExists` structural frame,
joins cancellation, and proves SAME-owner held recovery, not parsing activity.
No geometric execution or SQL-callable `jsonb_exists*` functions are introduced.

Run each selection in native and real pinned modes (external 900-second deadline):

```sh
make check RUN='^TestReferenceJSONBExists(Values|Typing|Diagnostics|ConsumersControls|Cancellation|FeatureSmoke)$'
make check RUN='^TestReferenceJSONBExists(Values|Typing|Diagnostics|ConsumersControls|Cancellation|FeatureSmoke)$' REFERENCE=1
make check RUN='^TestReferenceJSONBExistsCancellation$' RACE=1
make check RUN='^TestReferenceJSONBExistsFeatureSmoke$'
make check RUN='^TestReferenceJSONBExistsFeatureSmoke$' REFERENCE=1
```

Smoke expects ordered object/nested/array/primitive/NULL results
true/false/true/true/NULL, empty false/true, held cast-left `?|` recovery,
full 42883, and stored definitions surviving real replay.
Diagnostics separately verifies the complete unknown `?|` 42725 record.
Whole-project Docker-free verification remains `make quickcheck`.

### JSONB path replacement

The `TestReferenceJSONBSet` family independently specifies `jsonb_set` and
`jsonb_set_lax`: missing final versus intermediate paths, create flags, signed
array indices and int32 parsing boundaries, demand-driven path diagnostics,
strict SQL NULL and the four lax treatments. Typing covers contextual inputs,
domains, catalog formal names, defaults/named calls, qualified/quoted names,
invalid signatures/decorations and ordinary child evaluation. Parameters,
metadata and persistent consumers cover direct/prepared/held recovery, JSONB
raw/Scan ownership, computed metadata, generated/default/CHECK/view/index
expressions and late-write atomicity.

The established ten definitions share the unfiltered `jsonbSetScenario` backend loop.
Rows, types, affected counts, diagnostics and metadata are explicit expectations,
not generated goldens or backend parity alone.

Run the bounded sets in both modes, with an external 900-second deadline per
command:

```sh
make check RUN='^TestReferenceJSONBSet(Values|NullModes|PathDiagnostics|Typing)$' REFERENCE=1
make check RUN='^TestReferenceJSONBSet(Parameters|Metadata|Consumers|FeatureSmoke)$' REFERENCE=1
```

Run the same selectors without `REFERENCE=1` for selected native proof.
Both selected modes are required before review/integration.

Component contracts cover ordinary binding/all-slot inference and child demand,
copy-on-change subtree sharing and descriptor authority, reconstruction/remap/
retag and written named/default/VARIADIC presentation, immutable folding,
iterative deep paths and cancellation during long-key/index/copy/output work:

```sh
make check RUN='^TestJSONBSet'
```

Named view replay preserves formal labels and omitted optional arguments:
`null_value_treatment => 'delete_key'` must not become a positional BOOL input.
Component replay checks reparse/rebind and assert the resulting JSONB value;
controls check the actual emitted native dump on both native and PostgreSQL.
Repeated replay must also preserve byte-identical emitted SQL. Exact captured
JSON/JSONB/TEXT[] leaf annotations are emitted once; explicit conversions, domains
and typmods are retained. Component roundtrips cover positional, named/default
and VARIADIC calls, complementing the native deterministic-redump assertion.
Contextual ARRAY element conversions retain their Explicit executable plan and
mode. Capture-only provenance distinguishes those synthesized wrappers from
written element casts, preserving the original column and VARCHAR(1) truncation
alongside the enclosing TEXT[] annotation. The
`TestJSONBSetContextualArrayCapture` regression exercises positional and reordered
lax calls, default validation, remapping, descriptor reminting and exact results.

`TestReferenceJSONBSetReplayBoundaries` adds actual stored reordered named lax
deletion with SQL NULL replacement and omitted create flag, plus outer cast
chains, checked-domain conversion, VARCHAR/NUMERIC typmods and actual
JSON/JSONB conversions and a NUMERIC(4,1)→FLOAT8→TEXT→JSONB replacement.
Independent assertions distinguish `12.340` from rounded `12.3`, truncated path keys from
their source text, retained domain identity/column typmods and complete 23514
check errors even when lax would return its target. Three native Restore/redump
rounds and real replay of each emitted dump must retain these contracts.
`TestJSONBSetViewReplayCastBoundaries` supplies the component three-round
semantic/error/stability counterpart, using private catalog staging.

Selected replay-boundary proof uses an external 900-second deadline per command:

```sh
make check RUN='^TestReferenceJSONBSetReplayBoundaries$' REFERENCE=1
make check RUN='^TestReferenceJSONBSetReplayBoundaries$'
make check RUN='^TestJSONBSetViewReplayCastBoundaries$'
```

The revised replacement uses existing admitted cast edges rather than adding
the unsupported general NUMERIC→TEXT cast. Its expected `12.3`/`7.9` results
and NUMERIC precision/scale metadata remain independent contracts.
Domain identity is asserted independently through `pg_attribute.atttypid`
equaling the declared domain's qualified `regtype::oid`; the expected BOOL is
true, while cursor metadata independently requires JSONB as the physical base.
This does not admit adjacent `pg_type` or `pg_typeof` execution. All eleven
definitions execute their shared SQL on every available backend. Native-only
controls execute repeated Restore/redump, and real mode additionally checks
each emitted native dump on PostgreSQL.

Star-call syntax reaches ordinary signature binding and retains the pinned
missing-function diagnostic rather than a parser rejection.
DISTINCT, local ORDER BY, FILTER and OVER decorations are rejected with complete
42809 diagnostics after ordinary formal-argument resolution/coercion. The shared
grammar transports these modifiers; scalar execution must never discard them.

`Controls` exercises actual native Dump/Reset/Restore, retained rows and metadata,
deterministic redump, emitted-SQL replay on PostgreSQL, failed replacement and
parameter rejection in all five formal slots. PostgreSQL has no native control
API; those phases remain explicitly native-only. `Cancellation` uses a
pre-imported wide typed array with a retained 32MiB string, same-owner held
read/write recovery and sentinel atomicity using the existing admission/join
helper. Native admission observes the value-receiver provider frame
`sqltype.JSONBSetPlan.Evaluate`, enclosing traversal, changed-ancestor copying
and canonical output; no lexer or sleep stand-in is used.

```sh
make check RUN='^TestReferenceJSONBSet(Controls|Cancellation)$' REFERENCE=1
make check RUN='^TestReferenceJSONBSet(Controls|Cancellation)$'
make check RUN='^TestReferenceJSONBSetCancellation$' RACE=1
make check RUN='^TestReferenceJSONBSetFeatureSmoke$' REFERENCE=1
make check RUN='^TestReferenceJSONBSetFeatureSmoke$'
```

Smoke expects object replacement/missing-parent preservation, negative and
out-of-range arrays, all lax treatments with complete raise/index diagnostics
and same-held recovery, populated/empty/NULL metadata, persistent symbolic
consumers and atomic late failure. After native implementation it additionally
exercises retained native state and actual dump replay. Expected success is
quiet selected-package success after assertions; no printed success banner.

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
The cancellation fixture retains one small row and eight rows containing the
same32MiB JSON token. Bulk parameterized updates import/validate that immutable
token once per phase rather than eight times; row count, genuine lexer polling,
read/write interruption, atomicity and held recovery remain unchanged.

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

Selected post-merge cancellation reliability proof uses the same definition and
unchanged fixture/provider deadlines (external900s per invocation):

```sh
make check RUN='^TestReferenceJSONArrayLengthCancellation$'
make check RUN='^TestReferenceJSONArrayLengthCancellation$' REFERENCE=1
make check RUN='^TestReferenceJSONArrayLengthCancellation$' RACE=1
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
expected observations. Arbitrary user JSON casts, SQL/JSON constructors,
processing operators and aggregates remain outside this family. Executable
composite arrays use the existing recursive conversion path.

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

`TestReferenceJSONBKeysDecodedOrdering` is a focused JSONB-only boundary and
feature smoke. Its bound object combines escaped and literal duplicate `a`,
escaped control/punctuation names, `é`, a supplementary Unicode key with a JSON
null value, and a nested object. `WITH ORDINALITY` plus `ORDER BY n` exposes
canonical decoded UTF8-byte-length/binary ordering, not escape spelling or rune
length. Exact keys are `""`, newline, `"`, `\`, `a`, `é`, `aaa`, `😀`, `outer`,
with ordinals 1–9; duplicate `a` collapses and nested `hidden` is absent.
The shared direct/prepared routes assert TEXT/INT8 rows and actual-reader
metadata before iteration.

Run the completed definition in both modes under external 900-second deadlines:

```sh
make check RUN='^TestReferenceJSONBKeysDecodedOrdering$' REFERENCE=1
make check RUN='^TestReferenceJSONBKeysDecodedOrdering$'
```

Expected observation is quiet package success with all nine exact rows,
ordinality and metadata assertions satisfied. This adds coverage for existing
behavior; it does not establish a compatibility bug or production fix.

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

### JSON object-key enumeration

`TestReferenceJSONKeys*` covers only `json_object_keys(json)`: top-level TEXT
rows, empty/SQL NULL sets, duplicate multiplicities, escaped names, arrays/scalars,
complete PostgreSQL 18.0 diagnostics and metadata before iteration. Shared direct,
prepared and same-held parameter routes exercise target expansion, FROM products,
ROWS FROM padding, mixed regexp sources, ordinality, correlated/LEFT LATERAL,
placement guards, authorization, failed-write atomicity and stored-view dependencies.
Native controls retain old rows/metadata and replay actual emitted dumps on PostgreSQL.
Unordered output is a bag, not an invented portable key-order contract.
The review regressions retain supplied qualification in FILTER/DISTINCT/ORDER BY/
OVER diagnostics and analyze FILTER operands before signature/decorating errors.
`TestReferenceJSONKeysReviewMixedFrom` supplies keys and regexp in the same
ROWS FROM group, checking lockstep padding, duplicates, empty/NULL metadata and
stored mixed-source view ownership rather than only separate FROM products.
Direct function outputs have computed, unknown nullability; stored view reads
retain native catalog-origin nullable attributes, while pq reports unknown
nullability. Both routes check complete metadata before iteration.

JSON storage retains original text and accepts valid zero/surrogate escape syntax;
processing rejects undecodable strings even in ignored nested keys/values. Huge
numbers remain legal because keys processing does not build JSONB or convert numbers.
No adjacent JSON/JSONB function, generic function resolution or lateral derived
SELECT is supplied.

Run each bounded selector in both modes with an external 900-second deadline:

```sh
make check RUN='^TestReferenceJSONKeys(Values|Unicode|Diagnostics)$'
make check REFERENCE=1 RUN='^TestReferenceJSONKeys(Values|Unicode|Diagnostics)$'
make check RUN='^TestReferenceJSONKeys(QueryStructure|ContextDiagnostics|Metadata)$'
make check REFERENCE=1 RUN='^TestReferenceJSONKeys(QueryStructure|ContextDiagnostics|Metadata)$'
make check RUN='^TestReferenceJSONKeys(ParametersAndLifecycle|AuthorityAndControls|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceJSONKeys(ParametersAndLifecycle|AuthorityAndControls|FeatureSmoke)$'
make check RUN='^TestReferenceJSONKeysReview(Diagnostics|MixedFrom)$'
make check REFERENCE=1 RUN='^TestReferenceJSONKeysReview(Diagnostics|MixedFrom)$'
```

Feature smoke: `make check RUN='^TestReferenceJSONKeysFeatureSmoke$'`, then the
same selector with `REFERENCE=1`. Expected observations: CREATE0/prepared INSERT3,
key bag a×2,b×1; ordered (a,1),(a,2),(b,3) TEXT/INT8; full empty/NULL metadata;
LEFT LATERAL preservation; same-held array/Unicode errors followed by recovery;
unchanged JSON storage and native Dump/Reset/Restore with emitted-SQL replay.

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

### Array dimensions and cardinality

`TestReferenceArrayInspectValues`, `Typing`, `Diagnostics`,
`ParametersMetadata`, `ACL`, `ConsumersControls` and `FeatureSmoke` specify
`array_dims`, `array_length`, `array_lower`, `array_upper`, `array_ndims`
and `cardinality` independently of backend parity. The existing
`TestReferenceArraySmoke` retains its array storage, input, atomicity and
native-control assertions and requires shared direct/prepared length and ordinary
array-equality results. Native array-key ordering remains a separate exclusion.

The shape contract includes multidimensional and non-1 bounds, NULL elements,
zero-rank empty arrays, whole NULL, missing/nonpositive/NULL dimensions,
domains and computed NAME/JSON/enum arrays. Empty dimensions and rank are NULL;
empty cardinality is zero. Stored default/generated/CHECK/view/index consumers,
group/conditional/join traversal, held parameter recovery, pre-iteration raw
metadata, complete English diagnostics/character positions, column/schema
authorization and late-write atomicity are independently checked.
Computed inspector projections have unknown nullability; projections of stored
view attributes retain native `Nullable=true, known=true` for every view column.
PostgreSQL/pq independently reports unknown nullability on both routes. All
other raw metadata and cell assertions remain identical, including replay and
retained native cursors. The catalog-NULL witness explicitly compares OIDs after
casting the symbolic regclass identity to OID, without admitting oid/regclass
cross-type equality.
Stored view presentation retains selected argument coercions: an outer domain
over an array is shown with its physical array cast, including after source
column/table renames. Dump/replay preserves the same captured coercion authority;
rendering does not infer replacement types from renamed SQL text.
Native controls retain a scalar cursor across replacement, regenerate symbolic
stored calls and replay emitted SQL on PostgreSQL. No general codec, storage,
comparison, overload or pseudo-type permission is implied.

Review regressions add `TestReferenceArrayInspectReviewGrouping`,
`TestReferenceArrayInspectReviewLabels` and
`TestReferenceArrayInspectReviewNamespaces`. Every inspector groups identically
with mixed unqualified/`pg_catalog` spelling in both directions. Malformed
named notation asserts full 42601 diagnostics at the repeated label or following
positional operand; child/FILTER failures retain precedence. Missing and
inaccessible function schemas cannot hide earlier argument/FILTER failures;
valid children still reach their exact 3F000/42501 schema diagnostics.
Both direct/Prepare routes and multibyte character positions remain exact.

Selected proof commands (repeat both modes after affected changes):

```sh
make check REFERENCE=1 RUN='^TestReferenceArrayInspect(Values|Typing|Diagnostics)$'
make check RUN='^TestReferenceArrayInspect(Values|Typing|Diagnostics)$'
make check REFERENCE=1 RUN='^TestReferenceArrayInspect(ParametersMetadata|ACL|ConsumersControls|FeatureSmoke)$'
make check RUN='^TestReferenceArrayInspect(ParametersMetadata|ACL|ConsumersControls|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceArraySmoke$'
make check RUN='^TestReferenceArraySmoke$'
make check REFERENCE=1 RUN='^TestReferenceArrayInspectReview(Grouping|Labels|Namespaces)$'
make check RUN='^TestReferenceArrayInspectReview(Grouping|Labels|Namespaces)$'
```

Private provider/binder supplements select:
`make check RUN='^(TestArrayInspect(Values|Demand|NoCopy|Resolver|StoredRemap|ClosedDefaults)|TestArrayDimensionAccessors)$'`.
They check allocation-free numeric shape evaluation, immutable input,
left-to-right sibling errors/cancellation, canonical catalog-NULL authority,
generation remapping, child traversal and nested volatile default producers.

Feature smoke selects `RUN='^TestReferenceArrayInspectFeatureSmoke$'` in both
modes: the rank-2 tuple is `[-2:-1][3:5],3,3,5,2,6`, empty cardinality is zero
with other cells NULL, and whole NULL gives six NULLs. The same held dimension
statement recovers after malformed input; invalid scalar/dimension diagnostics,
a stored six-function view and native replacement complete the smoke.

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
Array-valued domain elements reuse ordinary array equality, including nested
shape/bounds/NULL semantics. TypesAndFrames asserts shared positive position,
positions, removal, replacement and existing membership results, preserving
the earlier NULL/empty/search fast paths. No missing leaf provider is invented.

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
Array-key ordering/index opclasses, adjacent array functions, new element codecs/
equality providers and unsupported collation backends remain excluded.

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
Ordinary array comparisons are covered separately below.
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

### Ordinary array comparisons

`TestReferenceArrayOrderValues` checks all seven spellings with independent
Boolean vectors: row-major first differences, equal-prefix element count, rank,
all dimension lengths before all lower bounds (including the rank-three
anti-interleaving witness), NULL elements/arrays, empty arrays and existing leaf
semantics. Typed arrays require the same actual array identity; scalar quantified
cross-width numeric comparisons remain valid. Domain/enum generations are
retained, not compared by coincident names or numeric IDs.

`TypingDiagnostics` checks direct/Prepare failure phases, exact English records
and character positions, visibility-aware domain/enum array names (including
hidden qualified names), missing JSON/JSONPath/ACL element providers, lazy implicit
collation conflicts, malformed contextual input and all-child authorization.
Equality checks shape before demanding the element equality provider; ordering
demands its distinct comparison provider before scanning even an empty array.
Collation is demanded only at a non-NULL collatable leaf pair. A whole NULL array
is strict; NULL elements equal each other and sort after non-NULL elements.

`Metadata` covers BOOL metadata before iteration and after close, empty/all-NULL
results and actual held frames with malformed-input recovery.
`ConsumersControls` exercises stored defaults/generated/CHECK/view and Boolean
expression indexes, CASE/NULLIF, grouped/distinct Boolean results, joins,
correlation, mutations, atomicity, rename/rebind, actual Dump/Reset/Restore,
deterministic redump, retained rows/metadata and unmodified PostgreSQL dump replay.
Stored enum-array provider ownership uses scalar domain-over-enum columns with
transient `ARRAY[a]`/`ARRAY[b]` operands in generated/view comparisons. This keeps
enum/domain-array storage admission unchanged while exercising dump/rebind and
same-name domain/owner recreation over a new enum provider. The original enum
remains live: unsupported enum deletion is not needed or expanded.
`Cancellation` uses admitted direct/prepared sleep requests with an ordinary
array predicate, same physical connection and preheld recovery. Deterministic
provider units separately prove cancellation inside comparison traversal.
Array-key ordering/index opclasses and missing leaf providers remain excluded;
existing grouping/hash admission is unchanged.

`TestReferenceArrayOrderQuantifiedArrayBoundary` exercises ANY/SOME/ALL over
existing array-valued-domain RHS elements. The original concrete RHS array,
element-domain generations, typmod and collation are retained when its physical
element matches the resolved comparison type; no array-of-array provider is
invented. Independent equality/ordering reductions cover constructor/literal,
chained-domain and outer-domain carriers, whole NULL, all-NULL elements, empty
arrays (including NULL left operands), typed parameters, decisive/UNKNOWN mixes
and inner-array prefix ordering. Direct/prepared results have exact BOOL metadata
with no source, a populated source and an empty `WHERE FALSE` result.

Run each selected batch in both modes (external 900-second deadlines):

```sh
make check RUN='^TestReferenceArrayOrder(Values|TypingDiagnostics|Metadata)$'
make check REFERENCE=1 RUN='^TestReferenceArrayOrder(Values|TypingDiagnostics|Metadata)$'
make check RUN='^TestReferenceArrayOrder(ConsumersControls|Cancellation|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceArrayOrder(ConsumersControls|Cancellation|FeatureSmoke)$'
make check RUN='^TestReferenceArrayOrderQuantifiedArrayBoundary$'
make check REFERENCE=1 RUN='^TestReferenceArrayOrderQuantifiedArrayBoundary$'
make check RUN='^TestReferenceArraySmoke$'
make check REFERENCE=1 RUN='^TestReferenceArraySmoke$'
make check RUN='^TestArrayOrder(Provider|Demand|PlanEquality|Cancellation|OwnerRebinding)$'
```

The permanent `TestReferenceArrayOrderFeatureSmoke` entry point checks direct
BOOL metadata/results, held value/NULL/recovery frames, quantified empty/unknown
identities, exact incompatible-signature diagnostics and same-DB recovery.
Its repository commands are listed above; session-only executable invocations
and observations belong in the part's verification evidence, not this checkout.

These are required proof commands/expectations, not unobserved passing claims.
Whole-project Docker-free verification remains `make quickcheck`.

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
Native retains the exact missing-`pg_catalog.pg_type` diagnostic. The original
PostgreSQL attribute queries still assert every expected row; native now asserts
their existing NAME-to-TEXT cast (`0A000`) or mixed OID/regclass comparison
(`42883`) boundary instead of claiming pg_attribute is absent. Additional shared
direct/prepared inquiries use raw NAME fields and each backend's discovered
numeric relation OID, asserting JSONPATH4072/_JSONPATH4073, typmod-1 and collation0.
No NAME cast, mixed-OID operator or pg_type provider is introduced by this migration.
All shared native JSONPATH/_JSONPATH IO, composition and controls remain asserted
without a skip. Equality, ordering and grouping
must reject rather than compare canonical output; grouping diagnostics retain
the declared scalar, array or domain name and original SQL source position.

Run all changed definitions in both modes, with a 900-second deadline per command:

```sh
make check RUN='^TestReferenceJSONPath'
make check REFERENCE=1 RUN='^TestReferenceJSONPath'
make check RUN='^TestReferenceJSONPathFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceJSONPathFeatureSmoke$'
make check RUN='^TestReferenceJSONPath(Representation|Composition)$'
make check REFERENCE=1 RUN='^TestReferenceJSONPath(Representation|Composition)$'
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
expectations are independent; BIT get_bit/set_bit execution is asserted only
there, with the native capability error asserted separately. BIT bit_count is
supported by the selected bit-function family. Portal Context and pq/native BYTEA
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

