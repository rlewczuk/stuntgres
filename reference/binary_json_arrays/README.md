# reference/binary_json_arrays

## Testing

Follow the [shared testing guide](../../docs/TESTING.md) for common policy,
fixture usage, and oracle-first workflow. Run the commands below from the
repository root.

### JSONB containment

The six `TestReferenceJSONBContainment` entrypoints specify structural `@>`
and reversed `<@` independently: same-depth object subsets, unordered and
duplicate-insensitive arrays, reusable nested candidates without merging
objects, immediate primitive membership, exact numeric equality and SQL NULL.
Direct/prepared queries assert BOOL metadata, including empty results; held
statements change inputs, reject malformed JSON even opposite SQL NULL and
recover on the same handles. Diagnostics pin complete English records and
character positions. Stored generated/check/view expressions exercise shared
SQL, affected counts and multirow failure atomicity.

Native-only phases retain old rows/metadata across Reset/Restore, verify the
independently expected stored results and deterministic redump, and reject a
late invalid Restore without changing the catalog or rows. Real mode replays
the actual emitted native dump on PostgreSQL. Both backends use the same SQL
scenario bodies; no discovery filter remains.

```sh
make check RUN='^TestReferenceJSONBContainment(Values|TypingMetadataHeld|Diagnostics)$' REFERENCE=1
make check RUN='^TestReferenceJSONBContainment(ConsumersControls|Cancellation|FeatureSmoke)$' REFERENCE=1
make check RUN='^TestReferenceJSONBContainment(Values|TypingMetadataHeld|Diagnostics)$'
make check RUN='^TestReferenceJSONBContainment(ConsumersControls|Cancellation|FeatureSmoke)$'
make check RUN='^TestReferenceJSONBContainmentFeatureSmoke$'
make check RUN='^TestReferenceJSONBContainmentFeatureSmoke$' REFERENCE=1
make check RUN='^TestJSONBContainment(Provider|DeepTraversal|Cancellation)$'
make check RUN='^TestReferenceJSONBContainment(Cancellation|TypingMetadataHeld)$' RACE=1
```

The smoke expects nested containment/reverse true, reordered duplicate arrays
true, nested-only scalar membership false, immediate string membership true,
the object array/scalar gate false and SQL NULL unknown. It also exercises
same-held true/false/NULL/error/recovery, exact 42883 diagnostics, filtered and
projected three-row data and stored-expression native round trips/PG replay.
Active cancellation
separately uses stored nested candidates, observes provider/server admission,
joins SELECT/UPDATE workers, checks no cursor/result or write publication and
reuses the same physical connection and held consumers. No SQL-array, JSON or
JSONPath operation participates in successful containment or smoke cases.

### JSONB concatenation and top-level deletion

The ten `TestReferenceJSONBConcatDelete` entrypoints specify `jsonb || jsonb`,
`jsonb - text`, and `jsonb - integer` through shared direct/prepared SQL bodies.
They independently expect shallow right-wins object union, ordered arrays and
scalar wrapping, key/all-matching-string removal, negative and extreme indices,
typed SQL NULL, exact JSONB metadata, overload resolution and complete English
diagnostics. Text-array deletion is outside this family; existing path deletion
coverage remains separate.

`ImplicitText` additionally requires unknown-left literals/parameters to resolve
the existing `jsonb - text` overload through implicit VARCHAR/BPCHAR-to-TEXT
coercion, including padded CHAR and domains. Each direct/prepared result is
independently `{}` with JSONB metadata. Numeric subtraction and both-unknown
ambiguity remain separate guard cases.

The shared scenarios run on every available backend without discovery filters.
Each asserts independently specified outcomes rather than parity alone. A held
bound deletion under `WHERE FALSE` preserves the documented native runtime-owned
parameter boundary (empty JSONB cursor), while PostgreSQL folds the first custom
plan and raises the exact scalar diagnostic. Demanded and literal errors remain
exact, as do invalid-signature analysis failures even for empty/filtered inputs.

```sh
make check RUN='^TestReferenceJSONBConcatDelete(Values|Typing|ImplicitText|Metadata|Diagnostics)$'
make check RUN='^TestReferenceJSONBConcatDelete(Values|Typing|ImplicitText|Metadata|Diagnostics)$' REFERENCE=1
make check RUN='^TestReferenceJSONBConcatDelete(Authority|Held|StoredControls|Cancellation|FeatureSmoke)$'
make check RUN='^TestReferenceJSONBConcatDelete(Authority|Held|StoredControls|Cancellation|FeatureSmoke)$' REFERENCE=1
make check RUN='^TestJSONBConcatDelete'
make check RUN='^TestReferenceJSONBConcatDelete(Held|StoredControls|Cancellation)$' RACE=1
```

Feature smoke uses actual concat/deletion expressions, including shallow nested
object replacement, scalar/array order, repeated string removal and final-index
deletion. It checks metadata before iteration, same-held TEXT/INT4/inferred
recovery after scalar errors, generated/default/check/index/view consumers and
late multirow failure preserving the sentinel and source values.

```sh
make check RUN='^TestReferenceJSONBConcatDeleteFeatureSmoke$'
make check RUN='^TestReferenceJSONBConcatDeleteFeatureSmoke$' REFERENCE=1
```

StoredControls and smoke retain old rows/descriptors across native replacement,
restore stored consumers, check deterministic redump, reject late failed/canceled
Restore without losing the saved catalog, and execute fresh restored writes.
Real mode replays the entire actual native dump, unmodified, on PostgreSQL and
checks saved rows and fresh generated/view/default consumers independently.
Cancellation separates native precanceled direct/held contracts from PostgreSQL
admitted, joined server-side cancellation, reusing the same owner and preheld
consumer. Active SELECT/INSERT cases use stored operands and wait for actual
concat/deletion-provider admission (server admission on PostgreSQL), then join
workers, assert no partial write and reuse the same held statements successfully.
One early small row and one heavy row preserve the partial-write boundary.
The 32MiB operand is parsed/stored once and reused by column assignment between
phases, avoiding redundant large binds and duplicate heavy rows.
The provider/server admission checks and cancellation deadlines remain unchanged.
Private units additionally cover descriptor authority, strictness, immutable
sharing/no-op identity, integer endpoints and deterministic merge/filter/render
cancellation, including long equal-prefix keys/strings.

### JSONB pretty output

The `TestReferenceJSONBPretty` family independently specifies exact TEXT for
scalars, empty/nested containers, canonical byte-length key ordering,
duplicate elimination, Unicode/control escaping and exact numeric scale.
Column, extraction and constructor inputs demonstrate actual-value formatting,
not source-whitespace retention. Direct/prepared and held calls check complete
TEXT metadata before iteration (including NULL/empty results), owned values,
inference, domains, qualification, signature/decorated-call diagnostics and
input failure phases. PostgreSQL 18.0 accepts explicit `VARIADIC` on this
single-argument call; named `jsonb =>` does not resolve.

Stored default/generated/check/index/view consumers cover multirow failure
atomicity and recovery. Native phases retain old rows/descriptions across
replacement, check deterministic redump and failed/canceled Restore atomicity,
then replay the whole emitted dump on PostgreSQL when enabled. Active
SELECT/INSERT cancellation observes provider/server admission, joins the
worker, checks unchanged destination rows and reuses the same held handles.
Private `TestJSONBPretty` units supplement these public witnesses with payload
cache independence, immutability and deterministic writer/traversal cancellation.

```sh
make check RUN='^TestReferenceJSONBPretty'
make check RUN='^TestReferenceJSONBPretty' REFERENCE=1
make check RUN='^TestJSONBPretty'
make check RUN='^TestReferenceJSONBPrettyCancellation$' RACE=1
make check RUN='^TestReferenceJSONBPrettyFeatureSmoke$'
make check RUN='^TestReferenceJSONBPrettyFeatureSmoke$' REFERENCE=1
```

The smoke performs real formatting, metadata, held recovery, diagnostic and
stored/replay exercises using the same behavioral helpers, without allocating
the large active-cancellation payload. Shared exact numeric-output regressions:
`make check RUN='^TestNumeric(CodecAndBounds|TypmodsAndImmutableSizing)$'`.

### JSONB path deletion

The ten `TestReferenceJSONBPathDelete` entrypoints exercise public
`jsonb #- text[]` with independent expectations: nested keys and indices,
negative indices, absent paths, reached-only path validation, SQL NULL,
inference/domains/composition, JSONB metadata and raw scans, exact diagnostic
records, actual operand-column privileges, same-held recovery, immutable inputs,
stored expressions, statement atomicity and cancellation.

```sh
make check RUN='^TestReferenceJSONBPathDelete(Values|Paths|Typing|Metadata|Diagnostics|Authority)$'
make check RUN='^TestReferenceJSONBPathDelete(Values|Paths|Typing|Metadata|Diagnostics|Authority)$' REFERENCE=1
make check RUN='^TestReferenceJSONBPathDelete(Held|StoredControls|Cancellation|FeatureSmoke)$'
make check RUN='^TestReferenceJSONBPathDelete(Held|StoredControls|Cancellation|FeatureSmoke)$' REFERENCE=1
make check RUN='^TestReferenceJSONBPathDelete(Held|StoredControls|Cancellation)$' RACE=1
make check RUN='^TestJSONBPathDelete'
make check RUN='^TestReferenceJSONBPathDeleteFeatureSmoke$'
make check RUN='^TestReferenceJSONBPathDeleteFeatureSmoke$' REFERENCE=1
```

Smoke asserts `{"a":[{"x":1},{"x":2}],"keep":3}` with `{a,-1,x}`
becomes `{"a": [{"x": 1}, {}], "keep": 3}`, final negative deletion,
missing-path preservation, typed NULL, metadata and same-held error recovery.
It also checks generated/view expressions and a failing late write that
publishes no rows. Native-only Reset/Dump/Restore retains an old cursor,
restores expressions and redumps deterministically; real mode additionally
replays the actual native-emitted SQL on PostgreSQL and exercises fresh writes.
PostgreSQL has no native-control API. Active cancellation uses stored operands,
not an input conversion workload. Success is the selected package passing
these assertions; no separate output banner or fabricated PostgreSQL control
equivalent is expected. All backends run without oracle-discovery filters.

### Array construction and transformation contracts

`TestReferenceArrayTransform` specifies `array_append`, `array_prepend`,
`array_cat`, `array_fill`, `array_reverse`, `array_sort`, `trim_array`,
`array_to_string` and the three related array `||` overloads. The same completed
scenario bodies run on every available backend. Definitions alone do not
establish native support or passing dual-mode proof.

Independent expectations encode pinned PostgreSQL 18.0 values, complete
diagnostics/stages and explicit backend metadata differences. In particular,
pq reports an empty database type name for user-defined associated arrays;
native metadata retains its named array identity. Shared single-projection
queries assert that public metadata directly; additional PostgreSQL-only
`pg_typeof` probes retain the independent SQL identity observations without
requiring an unrelated native builtin. Named sort calls quote the reserved `"array"`
parameter; unquoted spelling remains an exact syntax-error witness. Side effects
assert formal argument evaluation order (array before descending), not written
named-argument order. STABLE `array_to_string` is rejected by generated-column
and index immutable consumers at execution, including prepared DDL.

The implementation reuses immutable typed payloads, existing compatible
coercion/comparison and scalar output authorities. Reverse/sort preserve all
bounds; trim rebases all retained bounds, including count zero. Empty/singleton
sort avoids ordering-provider demand; scalar JSON sort errors use 0A000 while
multidimensional slice comparison errors use 42883. The exact catalog ANYARRAY
NULL carrier remains limited to the observed simple-polymorphic NULL routes
and explicitly retains interface{} ScanType on both backends, not the native
string ScanType of concrete arrays.
Composite ordering retains the documented native `0A000` boundary, independently
of PostgreSQL's successful composite sort. Composite reverse, fill, push/cat,
trim, serialization and empty/singleton sort remain positive shared witnesses.
A same-held typed composite-array parameter proves no ordering demand for
empty/singleton inputs, execution-stage rejection for two native items,
PostgreSQL's independently sorted rows, NULL propagation and recovery.
Named-order sequence arguments use scalar modulo and explicit INT4-to-BOOL
casts: formal calls 3 and 4 produce false and true, respectively, retaining
the formal-order `{1,2}` result and final sequence value 4. Catalog ANYARRAY
probes explicitly cast regclass to oid rather than demanding a new cross-type
comparison overload. Selected WITHIN GROUP decoration reaches signature lookup,
not generic ordered-set execution.
No new leaf codecs, ordinary array-of-array identities or index opclasses are
part of this family. Official PostgreSQL 18 §9.19 and REL_18_0 array/varlena
implementations ground the independent cases; the runtime oracle remains 18.0.

Package-only behavior units use the selector
`^TestArrayTransform(Kernels|SortAuthority|DomainPayload|ProviderOrder|OutputAndVolatility|ResolverOwners|NamedAndConcatSelection|CollationRemap)$`:

```sh
make check RUN='^TestArrayTransform(Kernels|SortAuthority|DomainPayload|ProviderOrder|OutputAndVolatility|ResolverOwners|NamedAndConcatSelection|CollationRemap)$'
```

They supplement the required public dual-mode definitions; they do not replace
the PostgreSQL proof. Existing text/bytea/bit concatenation and array search,
inspection and comparison feature selectors remain required regression checks.

`TestReferenceArrayTransformACLMutation` independently asserts ACL-item scalar
output in literal and parameterized `INSERT ... VALUES`, direct and prepared:
each write affects one row and stores exact TEXT without array braces. Matching
SELECT controls remain explicit. The fixture's authenticated grantor is
`postgres` natively (`=r/postgres`) and `stuntgres` on pinned PostgreSQL
(`=r/stuntgres`); expectations are fixed separately, never learned or normalized
from returned values.

```sh
make check RUN='^TestReferenceArrayTransformACLMutation$' REFERENCE=1
make check RUN='^TestReferenceArrayTransformACLMutation$'
```

Run each bounded selection in native mode and again with `REFERENCE=1` for
the real pinned PostgreSQL backend (external deadline 900 seconds per command):

```sh
make check RUN='^TestReferenceArrayTransform(Concat|Typing|Fill)$'
make check RUN='^TestReferenceArrayTransform(Concat|Typing|Fill)$' REFERENCE=1
make check RUN='^TestReferenceArrayTransform(FirstAxis|Sort|Serialization)$'
make check RUN='^TestReferenceArrayTransform(FirstAxis|Sort|Serialization)$' REFERENCE=1
make check RUN='^TestReferenceArrayTransform(Diagnostics|HeldMetadata)$'
make check RUN='^TestReferenceArrayTransform(Diagnostics|HeldMetadata)$' REFERENCE=1
make check RUN='^TestReferenceArrayTransform(StoredReplay|AuthorityControls|FeatureSmoke)$'
make check RUN='^TestReferenceArrayTransform(StoredReplay|AuthorityControls|FeatureSmoke)$' REFERENCE=1
```

The standalone feature selector is `^TestReferenceArrayTransformFeatureSmoke$`;
it checks all eight functions, bounds, NULL ordering, serialization, exact
shape errors, held recovery and stored replay, rather than aliasing a baseline.
Stored recipes are proved by behavioral rename checks and execution of actual
emitted SQL, not exact deparse text or native formatting.
The failed staged-restore witness uses an actual `trim_array` INSERT expression;
all eight selected calls are admitted by the recursive restore scalar owner,
while parameters remain prohibited. Actual emitted Dump SQL and retained-cursor
roundtrips are still exercised, never replaced with hand-written replay.

### JSON extraction

The `TestReferenceJSONExtract` family specifies JSON-only `->`, `->>`, `#>`,
`#>>`, `json_extract_path` and `json_extract_path_text`. Independent expectations
cover raw source fragments versus decoded TEXT, JSON null versus SQL NULL,
negative indices, missing/wrong shapes, flat TEXT[] paths (including bounds,
rank and NULL elements), inferred and held parameters, exact diagnostics,
computed metadata, stored expressions and native symbolic replay.

`ReleasePaths` specifies pinned PostgreSQL 18.0 outcomes for intermediate
duplicates, negative-index state on unrelated sibling arrays and empty-path
root whitespace. These expectations passed PostgreSQL discovery before
production implementation; this is not native-support or completed dual-mode
proof. Unicode cases include discarded branches, NULL-path short-circuit and
subsequent recovery. All available backends now run without a discovery filter.

```sh
make check RUN='^TestReferenceJSONExtract(Values|Paths|ReleasePaths|Unicode|Typing|Diagnostics|Metadata|Held|StoredReplay|NativeControls|FeatureSmoke)$' REFERENCE=1
make check RUN='^TestReferenceJSONExtract(Values|Paths|ReleasePaths|Unicode|Typing|Diagnostics|Metadata|Held|StoredReplay|NativeControls|FeatureSmoke)$'
make check RUN='^TestReferenceJSONExtractFeatureSmoke$'
make check RUN='^TestReferenceJSONExtractFeatureSmoke$' REFERENCE=1
```

The feature smoke issues real extraction SQL, checks JSON/TEXT metadata and
diagnostics, alternates held calls, creates stored consumers and exercises
native Dump/Reset/Restore with actual emitted-SQL replay on PostgreSQL.
Stored view dumps retain explicit final `VARIADIC` and named argument labels,
not merely the coerced TEXT[] argument. Ordinary array arguments are not a
substitute for a variadic call during native or PostgreSQL replay.
PostgreSQL has no native control API; those explicit phases run only natively.
Successful proof requires all independent assertions, not parity alone.

### JSON array elements

The `TestReferenceJSONElements` scenarios specify the public contract of
`json_array_elements(json)` and `json_array_elements_text(json)`: ordered
top-level expansion, source-preserving nested serialization, decoded text
strings, JSON-null versus SQL-NULL rows, empty schemas and scalar OUT naming.
They also cover exact diagnostics, direct/fresh-prepared/SAME-held calls,
lateral/target/ROWS FROM integration, write atomicity, authorization refresh,
observed active cancellation and symbolic stored/native-control consumers.

Raw JSON expansion never decodes string escapes; text expansion validates all
strings before publishing a row, including nested strings returned as raw JSON
text. Object/scalar roots have exact runtime 22023 diagnostics. The scalar OUT
label is `value` in FROM even under a table-only alias; SELECT uses the function
name. JSONB siblings and record-population functions remain excluded.
Safe left-only conjuncts of an immediate inner function join's WHERE can
restrict predecessor rows before expansion, including an uncorrelated function
source. Volatile/unsafe residuals remain post-expansion: the fresh-prepared/direct
two-source-row sequence witness consumes exactly three values, not six or one.
This uses the existing function-source eligibility boundaries, not a change to
ordinary Boolean evaluation.

Selected commands (external deadline 900 seconds each):

```sh
make check RUN='^TestReferenceJSONElements(Values|NullMetadata|Typing|Unicode)$' REFERENCE=1
make check RUN='^TestReferenceJSONElements(Values|NullMetadata|Typing|Unicode)$'
make check RUN='^TestReferenceJSONElements(Diagnostics|QueryStructure)$' REFERENCE=1
make check RUN='^TestReferenceJSONElements(Diagnostics|QueryStructure)$'
make check RUN='^TestReferenceJSONElements(ParametersAndLifecycle|Cancellation|AuthorityAndControls)$' REFERENCE=1
make check RUN='^TestReferenceJSONElements(ParametersAndLifecycle|Cancellation|AuthorityAndControls)$'
make check RUN='^TestReferenceJSONElementsFeatureSmoke$' REFERENCE=1
make check RUN='^TestReferenceJSONElementsFeatureSmoke$'
make check RUN='^TestReferenceJSONElements(ParametersAndLifecycle|Cancellation|AuthorityAndControls)$' RACE=1
make check RUN='^TestJSONElements'
make check RUN='^TestJSONEachPredecessorFilterPlacement$'
make check RUN='^TestReferenceJSONEach(PredecessorDemand|VolatileDemand)$'
make check RUN='^TestReferenceJSONEach(PredecessorDemand|VolatileDemand)$' REFERENCE=1
```

Smoke expects exact mixed values/ordinal order, complete empty metadata,
22023 wrong-root errors, held error/empty/NULL recovery, lateral expansion,
renamed symbolic views and native retained rows/metadata plus dump replay.

### JSONB extraction

`TestReferenceJSONBExtract` specifies JSONB-only `->`, `->>`, `#>`, `#>>`,
`jsonb_extract_path` and `jsonb_extract_path_text`. Independent expectations
cover exact TEXT keys versus INT4 array indices, negative indices, canonical
JSONB/decoded TEXT, JSON null versus SQL NULL, missing/wrong structures and
six-form metadata for populated, all-NULL and zero-row results.
Computed extraction nullability is unknown. Native stored table/view columns
retain known declared nullability, and COUNT is known nonnullable; pq exposes
unknown nullability in each case. Stored metadata assertions specify this
distinction explicitly, including retained native metadata after Reset.

Pinned PostgreSQL 18.0 discovery confirmed root scalar integer operators at
0/-1 select the scalar; nonempty paths on scalars return SQL NULL. TEXT[] paths
flatten multidimensional arrays and ignore lower bounds. Empty paths select
the whole value; ordinary one-document function calls are not empty paths.
NULL paths/components return SQL NULL. Array path indices accept leading C
whitespace, signs and leading zeros; malformed/overflowing/trailing-junk
indices return SQL NULL, not mutator errors. Object keys stay exact strings.
JSONB duplicate-intermediate and sibling-array traversal follows the canonical
tree, deliberately unlike JSON's released scanner-state witnesses above.

Full-field diagnostics cover signatures, unknown-left ambiguity, labels,
decorations, arity, character positions, parameter conversion context and
input-conversion precedence even with NULL-containing paths. Held statements
alternate documents/keys/indices/paths and recover after invalid input.
Stored DEFAULT/generated/CHECK/index/view/CTAS/INSERT SELECT SQL shares one
scenario across backends. Native-only phases retain rows/metadata across
Reset, restore exact dumps, reject late failed replacement atomically, and
replay actual emitted SQL on PostgreSQL with fresh default/generated insertion.
Dump replay preserves symbolic operators, named labels and final VARIADIC.
Discovery established PostgreSQL expectations, not completed native proof.

```sh
make check RUN='^(TestReferenceJSONBExtract(Values|Paths|Typing|Metadata|Diagnostics|Held|StoredReplay|NativeControls|FeatureSmoke)|TestReferenceJSONExtract(Values|Paths|ReleasePaths|Typing|Metadata|Diagnostics|Held|Unicode|StoredReplay|NativeControls|FeatureSmoke))$'
make check RUN='^(TestReferenceJSONBExtract(Values|Paths|Typing|Metadata|Diagnostics|Held|StoredReplay|NativeControls|FeatureSmoke)|TestReferenceJSONExtract(Values|Paths|ReleasePaths|Typing|Metadata|Diagnostics|Held|Unicode|StoredReplay|NativeControls|FeatureSmoke))$' REFERENCE=1
make check RUN='^TestReferenceJSONBExtractFeatureSmoke$'
make check RUN='^TestReferenceJSONBExtractFeatureSmoke$' REFERENCE=1
```

The feature smoke observes duplicate-last-key extraction, negative indices,
decoded escapes, raw/text nulls, missing/wrong shapes, all four path forms,
empty/ranked/bounded paths, scalar/operator distinctions, held recovery, exact
diagnostics and stored/native lifecycle behavior. Successful proof requires
independent assertions on each backend, not parity alone. JSONPath execution,
containment/deletion and other JSONB providers are outside this extraction part.


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
windows, arrays and general casts remain separate boundaries; get_bit/set_bit
have the [bit access contract](#bit-string-access).

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

### JSONB array element expansion

`TestReferenceJSONBElements` covers exactly `jsonb_array_elements(jsonb)` and
`jsonb_array_elements_text(jsonb)`. Shared direct/prepared bodies independently
specify ordered top-level values using ordinality and ORDER BY, canonical JSONB,
numeric scale and string decoding, strict empty/NULL sets, JSON null versus SQL
NULL, full logical English diagnostics and execution stages, named OUT `value`
metadata and raw carriers, held-statement recovery, query consumers, lateral
padding, atomic writes, authority/placement, view renames and native replay.
Constant-literal and source-dependent target/FROM views for both functions retain
independent canonical value, null and metadata assertions through restore and
actual PostgreSQL execution of emitted dump SQL.
Cancellation stores large nested elements before observing actual cursor output
work, tests read/write cancellation without partial publication, and reuses held
statements after recovery. Native Dump/Reset/Restore phases have no invented
PostgreSQL control equivalent; emitted dump SQL is replayed on the pinned server.

```sh
make check RUN='^TestReferenceJSONBElements'
make check RUN='^TestReferenceJSONBElements' REFERENCE=1
make check RUN='^TestReferenceJSONBElementsFeatureSmoke$'
make check RUN='^TestReferenceJSONBElementsFeatureSmoke$' REFERENCE=1
```

Expected success is quiet exit zero after independent assertions. Recorded root
verification establishes exercised evidence; these commands are not evidence.

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

### JSONB object member expansion

`TestReferenceJSONBEach` independently specifies both binary object-expansion
signatures: canonical key order observed via ordinality, decoded duplicate
last-wins normalization, canonical nested JSONB/numeric scale, text conversion,
SQL/JSON null distinction, full metadata, and complete PostgreSQL diagnostics.
Direct and SAME-held consumers cover recovery, predecessor demand, volatile
qualification, atomic writes, authorization rechecking, and cancellation during
stored nested-value conversion. Shared stored-view/CTAS/rename assertions run on
both backends; native Dump/Reset/Restore retains old results and replays the
actual emitted SQL on pinned PostgreSQL when available.

Selected proof (repeat each selector without `REFERENCE=1` for native proof):

```sh
make check RUN='^TestReferenceJSONBEach(Values|Typing|Diagnostics)$' REFERENCE=1
make check RUN='^TestReferenceJSONBEach(Metadata|QueryStructure)$' REFERENCE=1
make check RUN='^TestReferenceJSONBEach(ParametersAndLifecycle|PredecessorDemand|VolatileDemand)$' REFERENCE=1
make check RUN='^TestReferenceJSONBEach(Cancellation|AuthorityAndControls|FeatureSmoke)$' REFERENCE=1
make check RUN='^TestReferenceJSONBEach(ParametersAndLifecycle|Cancellation|AuthorityAndControls)$' RACE=1
make check RUN='^(TestJSONBEach.*|TestJSONEach.*|TestJSONFunctionCallAST|TestJSONFunctionParserConsumersAndDecorations|TestJSONBElementsOrdinaryIdentifiersAndFromSyntax)$'
```

Feature smoke:

```sh
make check RUN='^TestReferenceJSONBEachFeatureSmoke$'
make check RUN='^TestReferenceJSONBEachFeatureSmoke$' REFERENCE=1
```

Expected quiet success: CREATE0/held INSERT3; normalized keys a,b,o,zz at
ordinalities1–4 with canonical nested values; complete empty/NULL metadata;
present null records versus absent padded rows; LEFT LATERAL preservation;
held error/recovery, predecessor exclusion, no partial INSERT prefix, views,
CTAS, renames and native controls/actual emitted-SQL replay. PostgreSQL's shared
SQL phase alone does not prove native controls. All commands need an external
900-second deadline. Existing JSONEach selectors above retain source-preserving
regression coverage; common fixture and backend selection remain unchanged.

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

### SQL/JSON constructors and predicates

`TestReferenceSQLJSONArguments`, `Query`, `Conversion`, `Format`, `Returning`,
`Unique`, `Predicates`, `Diagnostics`, `Parameters`, `Metadata`, `Authority`,
`Stored`, `Controls`, `DomainPredicate`, `LegacyResolution` and `FeatureSmoke` specify SQL/JSON argument/query
construction, conversion, recursive uniqueness, root-kind predicates, output
coercions, exact diagnostic fields/stages, held statement recovery, descriptors,
actual-column authorization and argument-sensitive stored-expression admission.
Each suffix above follows `TestReferenceSQLJSON`.
`ClauseBoundaries` checks that legacy argument calls cannot consume constructor
clauses, empty objects accept only RETURNING, and member clauses survive.
`OutputEncoding` checks argument/query/member/empty/serialize routes, exact
BYTEA identity (including domain rejection), UTF8-only support, full diagnostics,
Unicode positions and output-validation precedence. Both use the same direct
and prepared public API routes on native and the unchanged pinned PostgreSQL.
Selected proof is `make check RUN='^TestReferenceSQLJSON(ClauseBoundaries|OutputEncoding)$'`,
also with `REFERENCE=1`.
`EmptyObjectControls` independently checks default and explicit JSON/JSONB/TEXT/
BYTEA empty-object views and stored defaults, then actual native Dump → Reset →
Restore with deterministic redump and actual emitted-SQL PostgreSQL replay.
Post-restore and post-replay inserts exercise the restored default recipes.
`FromClauseBoundaries` requires syntax diagnostics for formatted legacy single/
parallel-array calls in FROM and ROWS FROM, including qualified/quoted ordinary
calls, direct/prepared stages and Unicode character positions. It does not
enable scalar FROM providers. Selected proof and feature smoke:
`make check RUN='^TestReferenceSQLJSON(EmptyObjectControls|FromClauseBoundaries)$'`,
also with `REFERENCE=1`; passing independent assertions and quiet exit0 are the
expected observations after remediation.

`AssignmentControls` covers JSON/JSON_SCALAR defaults assigned to JSONB and the
text-object-to-JSONB boundary where moving assignment into RETURNING changes
null-key behavior. It consumes actual native Dump → Reset → Restore, requires
deterministic redump and restored default insertions, and replays actual emitted
SQL on pinned PostgreSQL. `PredicateUnicode` covers escaped NUL and invalid
surrogate sequences in scalar/array/object keys and values, all root kinds,
both polarities and WITH/WITHOUT UNIQUE through direct and prepared parameters.
Without UNIQUE these syntactically valid sources classify normally; WITH UNIQUE
returns false for decoding failure. Separate throwing input/constructor controls
require full pinned Unicode diagnostics. Selected proof and remedy smoke:
`make check RUN='^TestReferenceSQLJSON(AssignmentControls|PredicateUnicode)$'`
and `make check REFERENCE=1 RUN='^TestReferenceSQLJSON(AssignmentControls|PredicateUnicode)$'`.
Expect independent assertions and quiet exit0, not a rewritten replay script.
Supplemental `TestSQLJSONReturningOwnershipRebind` exercises actual conversion
and descriptor-generation reconstruction without losing constructor versus
ordinary-assignment provenance:
`make check RUN='^TestSQLJSONReturningOwnershipRebind$'`.

Supplemental internal reached-work cancellation units are
`TestSQLJSONRecursiveUniquenessReachedCancellation`,
`TestSQLJSONProviderReachedCancellation` and
`TestSQLJSONArrayQueryReachedCancellationCleanup`. They observe real decoded
recursive duplicates, completed member work, and streamed query progress before
canceling; no timer or production hook triggers cancellation. The query unit
enters the actual subquery owner, observes closure of its real nested producer,
preserves the primary cancellation over a cleanup failure, and recovers the
same bound owner. These supplement, not replace, public held-owner recovery.

Root discovery confirmed the completed initial definitions against pinned18.0
and captured native missing-feature failures before production authorization.
The shared scenarios have no backend discovery filter. Controls and expanded
smoke additionally exercise actual native Dump/Reset/Restore, retained cursor
values/descriptors, deterministic redump, late-error/hidden-parameter rejection,
same-owner cancellation recovery and actual emitted-SQL PostgreSQL replay.
New or changed definitions still require fresh selected proof in both modes;
authoring and production readiness are not verification.
Arguments includes complete runtime rejection contracts for array/composite/
JSON keys and a positive Boolean key control. DomainPredicate retains direct
and prepared analysis-time rejection, including typed NULL; Controls uses an
explicit `::json` in its positive domain CHECK. Metadata preserves the existing
adapter contracts: TEXT Length is `(MaxInt64, true)` on both backends; BYTEA
Length is unavailable `(0, false)` natively and `(MaxInt64, true)` in pq.
Predicates also cover parenthesized predicates as standalone scalar values,
postfix casts, comparison operands, constructor values and VALUE keys. Restore
must parse the same grouped predicate syntax before rejecting its hidden parameter;
domain diagnostics use the type name visible on the effective search path.

`JSON_SERIALIZE(jsonb)` intentionally differs from pinned PostgreSQL18.0:
native must return valid canonical JSON text, not emulate the release's
internal-representation defect. For the bounded input
`' {"b":2,"a":1} '::jsonb`, Conversion independently expects native
`{"a": 1, "b": 2}` and PostgreSQL U+0002 (`"\x02"`), using the same SQL and
direct/prepared routes without cross-backend parity comparison.
The [upstream report](https://www.postgresql.org/message-id/03583FE7-A5EE-407C-907D-67865F3F8BFC%40gmail.com)
and [parser analysis](https://www.postgresql.org/message-id/CAGRkXqQXwh_gJmQE3PZj0v13omMPgFGBbb8TJoBFH8ECsAFCUg%40mail.gmail.com)
identify a JSON coercion applied to a JSONB datum. The same bounded fixture
also exercises explicit `RETURNING text` and input/output `FORMAT JSON`;
an explicit `jsonb::json` cast is a separate canonical-text control on both
backends, not a replacement for the defective route. Root discovery exercised
the bounded default and explicit controls. These literals document only this
fixture, not a portable JSONB layout or a general rule for reproducing bytes.

Use these bounded selectors with an external 900-second deadline:

```sh
make check REFERENCE=1 RUN='^TestReferenceSQLJSON(Arguments|Query|Conversion)$'
make check REFERENCE=1 RUN='^TestReferenceSQLJSON(Format|Returning|Unique|Predicates)$'
make check REFERENCE=1 RUN='^TestReferenceSQLJSON(Diagnostics|Parameters|Metadata|Authority)$'
make check REFERENCE=1 RUN='^TestReferenceSQLJSON(Stored|Controls|DomainPredicate|LegacyResolution|FeatureSmoke)$'
```

Run the identical selectors without `REFERENCE=1` for native proof, and repeat
both modes after implementation or test changes. Actual feature smoke:
`make check RUN='^TestReferenceSQLJSONFeatureSmoke$'`, also with `REFERENCE=1`.
The smoke covers held inserts, ordered correlation, omitted/zero-row distinction,
JSON/JSONB/TEXT/BYTEA output, all root kinds, bounded independent JSONB
serialization differences, same-held malformed/recovery, UNIQUE/null-key
diagnostics, late-tuple atomicity, legacy resolution and the real control cycle.
Expected observation is quiet package success after all assertions, not an
invented SQL transcript. Selected race proof:
`make check RACE=1 RUN='^TestReferenceSQLJSON(Parameters|Controls)$'`.
Whole-project Docker-free proof remains `make quickcheck`.

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

### JSONB path insertion

The `TestReferenceJSONBInsert` family independently specifies `jsonb_insert`.
Catalog formals are `jsonb_in`, `path`, `replacement`, and `insert_after`
(default false). Arrays insert before/after an existing index without consuming
the original element. Negative indices count from the end; out-of-range negative
indices prepend and nonnegative indices append regardless of the flag, without
gaps. Replacement containers remain one element. Missing intermediate parents
are never fabricated; only reached path components are validated. Empty paths
preserve containers, while scalar roots error.

Final object keys must be absent, even when the old value is JSON null or equals
the replacement. Collisions raise `22023 cannot replace existing key`, with
Hint `Try using the function jsonb_set to replace key value.` and no Position.
All four SQL NULL operands are strict; JSON null is an inserted value, not SQL
NULL, and explicit NULL after is not the false default. Named/default and final
scalar VARIADIC calls retain their written presentation. VARIADIC selects the
ordinary scalar formal; it does not expand arrays.

The eight shared definitions cover values, full logical diagnostics/stages/
Unicode positions, ordinary child demand with a row-dependent NULL target,
typing and all-slot held inputs, repeated computed JSONB metadata across
populated/NULL/empty results, raw carriers/rescans/detached ownership, stored
default/generated/CHECK/view/index consumers, grouped/conditional traversal,
late-write atomicity, active read/write cancellation and SAME-owner recovery.
Native control phases retain old cursors/metadata across Reset/Restore,
deterministic symbolic redumps, future recipe execution, failed-Restore
isolation and real PostgreSQL replay of the actual emitted dump SQL.

Run both bounded selectors with an external 900-second deadline per command:

```sh
make check RUN='^TestReferenceJSONBInsert(Values|NullAndDiagnostics|TypingAndParameters|Metadata)$' REFERENCE=1
make check RUN='^TestReferenceJSONBInsert(ConsumersAndAtomicity|Controls|Cancellation|FeatureSmoke)$' REFERENCE=1
```

Run the identical selectors without `REFERENCE=1` for selected native proof.
Component contracts and unchanged replacement-family regressions:

```sh
make check RUN='^TestJSONBInsert'
make check RUN='^TestJSONBSet'
make check RUN='^TestReferenceJSONBSet' REFERENCE=1
make check RUN='^TestReferenceJSONBSet'
```

Feature smoke (exact independent before/after nested order, held negative/OOB/
object/collision recovery, late-write sentinel, native symbolic replay/future
recipes and actual dump replay in real mode):

```sh
make check RUN='^TestReferenceJSONBInsertFeatureSmoke$'
make check RUN='^TestReferenceJSONBInsertFeatureSmoke$' REFERENCE=1
make check RUN='^TestReferenceJSONBInsertCancellation$' RACE=1
```

Other insertion/deletion functions/operators, subscripting and JSONPath execution
remain outside this feature. Discovery filters and catalog probes are not part
of these permanent definitions.

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

### Array sample, shuffle and unnest

`TestReferenceArrayRelational*` independently specifies first-axis random
submultisets/permutations (never random backend parity), multidimensional slice
integrity/bounds, count limits, strictness and separate entropy from `random()`.
Unary storage order, wrapped targets, multi-input FROM padding, mixed ROWS FROM,
lateral joins, ordinality and named composite/domain elements share the same
definitions in native and pinned PostgreSQL18.0 modes. Full English diagnostics
and character locations include qualified/non-FROM calls, decorated syntax,
typed-definition precedence and rewritten unknown unary resolution.
`ZeroWidthComposite` includes zero-field composites and domains, NULL elements,
empty/NULL arrays, either side longest in a zip, explicit normalized ROWS FROM,
SAME-held reuse and stored/native/PostgreSQL replay. `TypedDefinitionClasses`
distinguishes explicit ROWS FROM ownership, scalar results, named composites
and domains, and existing `json_each`/`json_each_text` OUT records, retaining
function-resolution precedence and full direct/prepared Unicode diagnostics.
JSONB OUT-record expansion has separate focused `TestReferenceJSONBEach` coverage.

Typing assertions retain exact public ColumnTypes, raw transport and
literal/parameter-bound outer-domain peeling versus element-domain identity.
`TestArrayRelationalUsage` supplements those public routes with actual binder
providers, checking complete logical Usage (descriptor, typmod and registered
collation) and explicit collation provenance for sample, shuffle and unnest.
Run it with `make check RUN='^TestArrayRelationalUsage$'`. No `pg_typeof` or
`COLLATION FOR` inquiry implementation is a prerequisite. Ordinary unsized
VARCHAR preserves truthful native MaxInt64 length versus pq's observed `-5`;
domain VARCHAR(3) remains length3. The mixed array/multirange unnest witness uses
typed-table INSERT input and retains exact INT4RANGE cells and raw transport,
without requiring explicit multirange or range-to-text casts.
ACL tests traverse both random operands and every normalized source, including
NULL companions, LIMIT0 and held-after-revoke. Consumer/lifecycle coverage includes
source renames, symbolic view Dump/Reset/Restore/redump, actual emitted-SQL
PostgreSQL replay, fresh held cursors, input immutability and atomic late failure.

Run all four groups in both modes after affected changes:

```sh
make check RUN='^TestReferenceArrayRelational(RandomInvariants|SlicesAndBounds|CountDiagnostics|EntropyIsolation)$'
make check RUN='^TestReferenceArrayRelational(RandomInvariants|SlicesAndBounds|CountDiagnostics|EntropyIsolation)$' REFERENCE=1
make check RUN='^TestReferenceArrayRelational(UnnestStorage|UnnestZip|CompositeElements)$'
make check RUN='^TestReferenceArrayRelational(UnnestStorage|UnnestZip|CompositeElements)$' REFERENCE=1
make check RUN='^TestReferenceArrayRelational(ResolutionDiagnostics|TypingMetadata|Authorization)$'
make check RUN='^TestReferenceArrayRelational(ResolutionDiagnostics|TypingMetadata|Authorization)$' REFERENCE=1
make check RUN='^TestReferenceArrayRelational(ConsumersReplay|HeldLifecycle|FeatureSmoke)$'
make check RUN='^TestReferenceArrayRelational(ConsumersReplay|HeldLifecycle|FeatureSmoke)$' REFERENCE=1
make check RUN='^TestReferenceArrayRelational(ZeroWidthComposite|TypedDefinitionClasses)$'
make check RUN='^TestReferenceArrayRelational(ZeroWidthComposite|TypedDefinitionClasses)$' REFERENCE=1
make check RUN='^TestArrayRelationalZeroWidthComposite$'
```

Feature smoke: `make check RUN='^TestReferenceArrayRelationalFeatureSmoke$'`
and the same command with `REFERENCE=1`. Expected assertions: prepared two-row
INSERT; ordered unary values1,NULL,3,4; zip(1,a,1),(2,b,2),(NULL,c,3); wrapped unary
target; exact42883/22023 diagnostics; matrix slice/bounds invariants; empty typed
schema/held recovery; native symbolic view replacement and genuine PG replay.
These commands are not a claim that final verification has run.

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
expectations are independent; the migrated resolution witnesses now assert
BIT get_bit/set_bit success on both backends. BIT bit_count is supported by
the selected bit-function family. Portal Context and pq/native BYTEA
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

### Bit-string access

`TestReferenceBitAccess*` covers BIT and implicit VARBIT `get_bit`/`set_bit`.
Indexes are INT4 and count from the leftmost logical bit, starting at zero,
unlike BYTEA's LSB-first INT8 bit index. Getter results are INT4; setters return
BIT/-1 independently of source width, preserve every other digit and do not
mutate the source. Destination BIT/VARBIT assignment still enforces its width.
Strict NULL suppresses body evaluation, not input, signature or authority checks.
The complete ERROR diagnostics independently require 2202E
`bit index N out of valid range (0..U)` before replacement validation and
22023 `new bit must be 0 or 1` for other in-range replacement values.

Permanent witnesses cover manual examples, first/last/interior bits across
byte boundaries, empty and extreme indexes, overload competition, direct/prepared
results, NULL/empty/live metadata and raw transport, SAME-held frame/error recovery,
owned bind/scan buffers, authorization, statement atomicity and savepoint recovery.
Defaults/generated/CHECK/view calls remain symbolic through native Dump/Reset/Restore;
real mode replays the actual emitted SQL on PostgreSQL. The native retained cursor
and failed/canceled Restore phases assert native APIs, not a fabricated PG equivalent.
General bit casts, arrays, ordering/index/grouping providers and the substr alias
are not added by access functions. Existing BYTEA/default boundaries are unchanged.

Run each small selector in both modes from the repository root, with an external
900-second deadline per command:

```sh
make check RUN='^TestReferenceBitAccess(Values|Typing|NullMetadata)$'
make check REFERENCE=1 RUN='^TestReferenceBitAccess(Values|Typing|NullMetadata)$'
make check RUN='^TestReferenceBitAccess(Diagnostics|HeldStatements)$'
make check REFERENCE=1 RUN='^TestReferenceBitAccess(Diagnostics|HeldStatements)$'
make check RUN='^TestReferenceBitAccess(Ownership|Authorization|Atomicity)$'
make check REFERENCE=1 RUN='^TestReferenceBitAccess(Ownership|Authorization|Atomicity)$'
make check RUN='^TestReferenceBitAccessConsumersControls$'
make check REFERENCE=1 RUN='^TestReferenceBitAccessConsumersControls$'
make check RUN='^TestReferenceBinaryAccessResolution$'
make check REFERENCE=1 RUN='^TestReferenceBinaryAccessResolution$'
make check RUN='^TestBitAccess(Values|Validation|AllocationsAndCancellation)$'
make check RUN='^TestBinaryAccess(Signatures|Resolution|RetagAndRestoreArguments)$'
make check RUN='^TestBitStringFunctionsValidation$'
```

Standalone feature smoke composes the permanent callbacks, independently checking
the manual getter1/setter101010001010101010, BIT/BYTEA orientation(1,0),
mutation/original ownership, typed NULL/metadata, exact error precedence,
SAME-held recovery and real stored/control replay:

```sh
make check RUN='^TestReferenceBitAccessFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceBitAccessFeatureSmoke$'
```

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

