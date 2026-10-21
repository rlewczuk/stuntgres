# reference/numeric

## Testing

### Timestamp series selected proof

Untagged `timestamp_series_*_test.go` defines independent civil rows, iterative
month clamping, microseconds, strict NULLs, widened mixed-sign behavior, infinity
endpoints, complete diagnostics, held parameters, metadata, composition,
atomicity, active cancellation and symbolic/data native replay. Oracle-first
core and migrated boundary/ACL observations use pinned PostgreSQL18.6.
Every changed definition needs both modes after its last change. Run each
command with an external900-second deadline:

```sh
make check RUN='^TestReferenceTimestampSeries(Values|Binding|Metadata|Diagnostics|Composition|Atomicity)$'
make check REFERENCE=1 RUN='^TestReferenceTimestampSeries(Values|Binding|Metadata|Diagnostics|Composition|Atomicity)$'
make check RUN='^TestReferenceTimestampSeries(Lifecycle|Controls|Infinity)$'
make check REFERENCE=1 RUN='^TestReferenceTimestampSeries(Lifecycle|Controls|Infinity)$'
make check RUN='^TestTimestampSeries(Cursor|Sign|Overflow|Lifecycle|Hooks|RoutineAuthority)$'
make check RACE=1 RUN='^TestReferenceTimestampSeries(Lifecycle|Atomicity|Controls|Binding)$'
make check REFERENCE=1 RACE=1 RUN='^TestReferenceTimestampSeries(Lifecycle|Atomicity|Controls|Binding)$'
make check RUN='^TestReferenceTimestampSeriesFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceTimestampSeriesFeatureSmoke$'
```

FeatureSmoke invokes the cohesive permanent witnesses with fresh fixtures:
exact leap/month rows, held failure/recovery, NULL metadata, endpoint overflow,
zip/ordinality/lateral, failed mutation preservation and real Dump/Reset/Restore,
fresh-driver and PostgreSQL emitted-SQL replay. Expected output is quiet package
success, with exact independently asserted outcomes, not a custom success label.
No check is claimed passing here; root records actual verification evidence.


Follow the [shared testing guide](../../docs/TESTING.md) for common policy,
fixture usage, and oracle-first workflow. Run the commands below from the
repository root.

### Logarithms, roots, powers and buckets

Untagged `math_log_power_*_test.go` scenarios specify all seven selected functions
and released overloads through direct, prepared, held and SQL-prepared routes.
Numeric goldens retain exact decimal digits/scales, including near-one and
1000-place inputs. Finite irrational FLOAT8 targets permit at most2ULP; NaN,
infinity and signed-zero categories are exact. Expected records were resolved
against pinned PostgreSQL18.0, never inferred from native output.

Full diagnostics retain absent arithmetic Position, function-start lookup
positions, portal parameter Context and the distinct numeric unary log/log10
SQL-wrapper Context for constants/binds versus column inputs. A literal NULL
argument does not suppress a demanded logarithm error in a sibling expression.
Four-argument buckets cover exact decimal boundaries, reversed bounds, safe
floating extremes, validation precedence and actual count-plus-one overflow.
Threshold proof covers existing text/date/timestamp/enum/domain types, mixed
common types, duplicate/rightmost matches, NaNs and real rank/NULL/comparator
errors, including missing comparison on empty arrays. Ascending sortedness is
a precondition: no sortedness validation or invented error is supplied.
Enum-array annotations are admitted only while analyzing the selected threshold
argument, consuming existing providers without enabling general enum-array
casts/storage/domain bases or unrelated function arguments. Empty/NULL/bound
threshold arrays and symbolic stored enum comparisons exercise that boundary.

Consumer proof exercises stored/default/generated/CHECK/view expressions,
conditional/grouping traversal and late-write atomicity. Native control phases
genuinely Dump/Reset/Restore, preserve old cursors/descriptors, consume a power
default after replacement, reject a later invalid replacement atomically and
restore into a distinct catalog allocation. Real mode replays actual native Dump
SQL on PostgreSQL. Provider/component units assert owner generations, collations,
all four source-order operands, strictness and deterministic cancellation.
Deterministic redump compares the complete emitted SQL before/after replacement;
failure output includes both dumps. Captured enum literals own their exact
replay cast, so matching leaf annotations do not accumulate another cast on
Restore. Outer conversions, domain checks and public deparse presentation remain
unchanged.

Root runs each group in both modes, with an external900-second deadline:

```sh
make check RUN='^TestReferenceMathLogPower(Binding|Metadata|HeldParameters|SQLPrepared)$'
make check REFERENCE=1 RUN='^TestReferenceMathLogPower(Binding|Metadata|HeldParameters|SQLPrepared)$'
make check RUN='^TestReferenceMathLogPower(Values|NumericPrecision|IEEE|Diagnostics)$'
make check REFERENCE=1 RUN='^TestReferenceMathLogPower(Values|NumericPrecision|IEEE|Diagnostics)$'
make check RUN='^TestReferenceMathLogPower(Buckets|BucketDiagnostics|Thresholds|ThresholdDiagnostics)$'
make check REFERENCE=1 RUN='^TestReferenceMathLogPower(Buckets|BucketDiagnostics|Thresholds|ThresholdDiagnostics)$'
make check RUN='^TestReferenceMathLogPower(Consumers|Controls|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceMathLogPower(Consumers|Controls|FeatureSmoke)$'
make check RUN='^TestMath(LogPower(Provider|Signatures|Cancellation|ThresholdOwnership)|FourArgumentTraversal)$'
make check RUN='^TestSize(Provider(Signatures|OwnershipAndCancellation)|NumericScratchAndScanner)$'
make check RACE=1 RUN='^TestReferenceMathLogPower(HeldParameters|Controls)$'
make check REFERENCE=1 RACE=1 RUN='^TestReferenceMathLogPower(HeldParameters|Controls)$'
```

The actual feature smoke independently expects FLOAT8 results0,0,2,3,8,2;
NUMERIC binary log `6.0000000000000000`; ascending/reversed/duplicate-threshold
buckets2,2,3; held sqrt error/recovery; stored four-argument and threshold calls;
and actual native lifecycle/default replay. Expected success is quiet exit0:

```sh
make check RUN='^TestReferenceMathLogPowerFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceMathLogPowerFeatureSmoke$'
```

Whole-project Docker-free verification remains `make quickcheck`; post-merge
`make finalcheck` remains separate. Selected proof is required for every changed
definition, including migrated Math provider callers; never use `make refcheck`.

### FROM SRF selected proof

Untagged `srf_from_*_test.go` scenarios assert independent direct/prepared
INT4/INT8 values and empty metadata, aliases, bigint ordinality, comma products,
ROWS FROM typed padding and mixed existing providers. Lateral invocations reset
ordinality for repeated inputs; held statements retain schema across empty,
error and recovered executions. Complete diagnostics include Unicode locations,
analysis/runtime failure stages and source binding before column-definition
rejection (count, then ordinality, then record-only). Nested FROM diagnostics
track successful call completion: the last sibling, an earlier nested failure,
scalar wrappers and mixed/regexp calls, without child-SELECT leakage.

Consumer proof covers symbolic views, CTAS/INSERT SELECT late-error atomicity,
joined cancellation workers and same-owner recovery. Controls genuinely retain
old rows/schema across Dump/Reset/Restore, restore into a fresh Driver and replay
the actual emitted SQL on PostgreSQL after dependency rename. Restore and
INSERT VALUES boundaries remain unchanged. Use bounded portable FROM witnesses:
PostgreSQL materializes demanded function output, so huge LIMIT series are only
appropriate to the existing target tests, never FROM smoke.

Run each group in both modes, with external900s deadlines:

```sh
make check RUN='^TestReferenceSRFFrom(Values|Metadata|Composition|Lateral|Diagnostics|HeldParameters)$'
make check REFERENCE=1 RUN='^TestReferenceSRFFrom(Values|Metadata|Composition|Lateral|Diagnostics|HeldParameters)$'
make check RUN='^TestReferenceSRFFrom(Consumers|Lifecycle|Controls|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceSRFFrom(Consumers|Lifecycle|Controls|FeatureSmoke)$'
make check RUN='^TestReference(Series|Subscripts)Boundaries$'
make check REFERENCE=1 RUN='^TestReference(Series|Subscripts)Boundaries$'
make check RUN='^Test(SRFFrom(Binding|Invocation|Ownership|StoredEligibility)|SeriesBinding|SubscriptsBinding|SRFFromParser|SeriesParserRestrictions)$'
make check RACE=1 RUN='^TestReferenceSRFFrom(Lifecycle|Controls|HeldParameters)$'
make check REFERENCE=1 RACE=1 RUN='^TestReferenceSRFFrom(Lifecycle|Controls|HeldParameters)$'
```

Focused diagnostic proof includes the 16 review-regression witnesses through
both direct and prepared routes, with plain and Unicode-prefixed positions:

```sh
make check RUN='^TestReferenceSRFFromDiagnostics$'
make check REFERENCE=1 RUN='^TestReferenceSRFFromDiagnostics$'
make check RUN='^TestReference(StringSplitTableSources|StringSplitDiagnostics|StringSplitStored|StringSplitLifecycle|JSONKeysReviewMixedFrom)$'
make check REFERENCE=1 RUN='^TestReference(StringSplitTableSources|StringSplitDiagnostics|StringSplitStored|StringSplitLifecycle|JSONKeysReviewMixedFrom)$'
```

Actual smoke checks descending INT8/INT8 (5,1),(3,2),(1,3), group padding
(1,10,1),(2,NULL,2), lateral reset/LEFT empty preservation, empty metadata,
same-held zero-step22023/recovery and actual control roundtrip/dump replay.
Expected observations are exact rows/types/diagnostics and quiet success:

```sh
make check RUN='^TestReferenceSRFFromFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceSRFFromFeatureSmoke$'
```


### Integer target series selected proof

Permanent untagged `series_*_test.go` definitions specify two-/three-argument
INT4/INT8 values, endpoints, NULL empties, checked overflow, metadata and actual
held reuse. Overload/decorations/context expectations are independent and
resolved against pinned PostgreSQL18.0, including legal ALL and window keys.
DATE endpoints plus INTERVAL step resolve to the preferred TIMESTAMPTZ signature:
Boundaries asserts exact UTC dates on PG and the full native temporal capability
diagnostic, never a fabricated ambiguity or native temporal execution.
Composition reuses zip/padding/nested target stages; huge finite series with
LIMIT3 must return only1,2,3 without whole-series materialization.

Lifecycle uses actual native provider-advance cancellation and observed
PostgreSQL server admission/cancellation with joined workers. Atomicity covers
late errors/cancellation through INSERT SELECT and CTAS. Controls exercise
symbolic series views, genuine persisted INT4/INT8 data, retained rows/schema,
same/fresh Driver Dump/Reset/Restore and actual emitted SQL replay on PG.
Restore rejection of INSERT query sources remains truthful and atomic;
zero-step SQL inside a rejected script is not claimed to execute.
The native gate observes `SourceCursor.Next` in the same goroutine as the named
worker. This follows provider admission after the function-source protocol
cutover; it does not pin the deleted cursor representation or change expected
cancellation, atomic publication, worker joining or same-owner recovery.

Root runs each reference group in both modes after edits, with external900s
per invocation. These selectors supplement whole-project `make quickcheck`:

```sh
make check RUN='^TestReferenceSeries(Values|Overflow|Metadata)$'
make check REFERENCE=1 RUN='^TestReferenceSeries(Values|Overflow|Metadata)$'
make check RUN='^TestReferenceSeries(Binding|Diagnostics|HeldParameters)$'
make check REFERENCE=1 RUN='^TestReferenceSeries(Binding|Diagnostics|HeldParameters)$'
make check RUN='^TestReferenceSeries(Composition|Limit|Restrictions|Boundaries|OracleDecisions)$'
make check REFERENCE=1 RUN='^TestReferenceSeries(Composition|Limit|Restrictions|Boundaries|OracleDecisions)$'
make check RUN='^TestReferenceSeries(Lifecycle|Atomicity|Controls)$'
make check REFERENCE=1 RUN='^TestReferenceSeries(Lifecycle|Atomicity|Controls)$'
make check RUN='^TestSeries(Binding|Cursor|Overflow|ExpressionHooks|Lowering|Cancellation|Finalization)$'
make check RUN='^TestSeriesParser(Admission|Decorations|Restrictions)$'
make check RACE=1 RUN='^TestReferenceSeries(Lifecycle|Controls|HeldParameters)$'
make check REFERENCE=1 RACE=1 RUN='^TestReferenceSeries(Lifecycle|Controls|HeldParameters)$'
```

The bounded public new-behavior smoke checks ordered INT4 1,2,3; INT8 5,3,1;
empty metadata; held zero-step failure/recovery; endpoint overflow; huge LIMIT3;
zip padding; late INSERT SELECT failure preserving sentinel99; and actual
control roundtrips/replay. Expected observations are exact rows/types/errors and
unchanged atomic state, with exit0 and quiet package PASS output:

```sh
make check RUN='^TestReferenceSeriesFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceSeriesFeatureSmoke$'
```

### Numeric series selected proof

Untagged `numeric_series_*_test.go` definitions independently specify exact
decimal values, nonuniform seed/step scales, NULL/nonfinite/zero rules, metadata,
overload resolution, held reuse and function-source composition. Initial pinned
PostgreSQL18.0 discovery confirmed Values/Boundaries/Demand, Binding/HeldParameters/
Metadata and Diagnostics/OracleDecisions/Composition expectations before native
implementation. Later additions require fresh selected proof in both modes.

Boundaries includes signed equal/non-equal extreme successors beyond packed
numeric range, full execution, LIMIT1 and count1 witnesses, plus finest fractional
scale. Lifecycle observes the numeric provider on the native worker's live stack;
PostgreSQL uses observed active-owner server cancellation. Workers are joined.
Atomicity checks late zero/nonfinite failures and admitted cancellation through
SELECT, INSERT SELECT and CTAS. Controls exercises symbolic numeric views,
materialized exact data, retained metadata/rows, same/fresh Driver replay,
deterministic redump, failed/canceled Restore and actual emitted SQL on PG.

Run each small group in both modes (external deadline900s each), then root
whole-project `make quickcheck`:

```sh
make check RUN='^TestReferenceNumericSeries(Values|Boundaries|Demand)$'
make check RUN='^TestReferenceNumericSeries(Values|Boundaries|Demand)$' REFERENCE=1
make check RUN='^TestReferenceNumericSeries(Binding|HeldParameters|Metadata)$'
make check RUN='^TestReferenceNumericSeries(Binding|HeldParameters|Metadata)$' REFERENCE=1
make check RUN='^TestReferenceNumericSeries(Diagnostics|OracleDecisions|Composition)$'
make check RUN='^TestReferenceNumericSeries(Diagnostics|OracleDecisions|Composition)$' REFERENCE=1
make check RUN='^TestReferenceNumericSeries(Lifecycle|Atomicity|Controls)$'
make check RUN='^TestReferenceNumericSeries(Lifecycle|Atomicity|Controls)$' REFERENCE=1
make check RUN='^TestReferenceSeries(Boundaries|Lifecycle|Atomicity)$'
make check RUN='^TestReferenceSeries(Boundaries|Lifecycle|Atomicity)$' REFERENCE=1
make check RUN='^TestNumericSeries(Cursor|Specials|Successor|Cancellation|Binding|ExpressionHooks|Invocation)$'
make check RUN='^TestReferenceNumericSeries(Lifecycle|HeldParameters|Controls)$' RACE=1
make check RUN='^TestReferenceNumericSeries(Lifecycle|HeldParameters|Controls)$' REFERENCE=1 RACE=1
```

Permanent feature smoke covers exact tenths/descending/nonuniform scales,
NULL empty metadata, zero/nonfinite diagnostics, actual plain and Context held
failure/recovery, ROWS FROM zip/ordinality, demand-limited huge numeric series,
late INSERT failure preserving99, and genuine numeric view/data replay with
emitted SQL on PostgreSQL. Expected observations are exact asserted rows,
metadata and diagnostics, unchanged failed mutation state, exit0 and quiet PASS:

```sh
make check RUN='^TestReferenceNumericSeriesFeatureSmoke$'
make check RUN='^TestReferenceNumericSeriesFeatureSmoke$' REFERENCE=1
```

### Array target subscripts selected proof

Permanent untagged `subscripts_*_test.go` definitions exercise both released
`generate_subscripts` signatures in one shared backend body, with independent
INT4 values/types and complete diagnostics/stages resolved against pinned
PostgreSQL18.0. They cover 1D/multidimensional stored bounds, NULL elements,
each strict NULL input, empty/absent dimensions, reverse, simple polymorphism,
domains, naming/decorations and held parameter error/recovery.

Composition and consumers cover existing target-SRF zip/NULL padding/nesting,
group/sort/distinct/window-key lanes, views/CTAS/INSERT SELECT, late malformed
array atomicity, and every argument's dependencies/authorization. Lifecycle
uses real provider advancement and observed PostgreSQL server admission with
joined cancellation workers. Controls retain eager rows/schema across actual
native Dump/Reset/Restore, restore into a fresh Driver and replay the actual
emitted supported SQL on PostgreSQL. INSERT VALUES remains an explicit native
capability failure; FROM support is covered by the dedicated selected proof above.
The PostgreSQL VALUES witness independently expects affected2 on each direct
and prepared insertion, leaving ordered rows1,1,2,2.

Frozen typed array literals own their replay cast. The subscripts dump path
retains differing source casts without adding a redundant owning cast on each
Restore; Controls asserts exact redump bytes and the internal Deparse unit
checks matching/differing casts and original traversal ordinals.

Oracle discovery observed pinned18.0 reverse at an exact INT32_MIN lower bound
run until fixture cancellation and invalidate the connection. Do not repeat that
hazardous query. Shared reverse lower-edge coverage uses INT32_MIN+1; internal
`TestSubscriptsBounds` independently proves safe native exact-MIN reverse
iteration and typed EOF/no wrap. Public ORDER BY proves the stated SQL ordering,
not provider reverse direction; real internal cursor iteration proves direction.
No release-defect emulation, array storage expansion or version repin is intended.

Root runs each selected group in both modes, with external900s deadlines.
Whole-project `make quickcheck` is additional Docker-free verification:

```sh
make check RUN='^TestSubscripts(Binding|Cursor|Bounds|ExpressionHooks|Deparse|Lowering|Cancellation|Finalization)$'
make check RUN='^TestSubscriptsParser(Admission|Decorations|Restrictions)$'
make check RUN='^TestReferenceSubscripts(Values|Binding|Diagnostics|Metadata)$'
make check REFERENCE=1 RUN='^TestReferenceSubscripts(Values|Binding|Diagnostics|Metadata)$'
make check RUN='^TestReferenceSubscripts(Parameters|Composition|Restrictions|Boundaries)$'
make check REFERENCE=1 RUN='^TestReferenceSubscripts(Parameters|Composition|Restrictions|Boundaries)$'
make check RUN='^TestReferenceSubscripts(Consumers|Controls|Lifecycle|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceSubscripts(Consumers|Controls|Lifecycle|FeatureSmoke)$'
make check RACE=1 RUN='^TestReferenceSubscripts(Parameters|Controls|Lifecycle)$'
make check REFERENCE=1 RACE=1 RUN='^TestReferenceSubscripts(Parameters|Controls|Lifecycle)$'
```

The actual feature smoke asserts direct indices−2,−1,0; prepared dimension-two
descending5,4,3; typed empty results; peer padding; held malformed input followed
by valid recovery; late INSERT SELECT failure leaving sentinel99; and native
symbolic/materialized control roundtrips plus actual PostgreSQL dump replay.
Expected success is exit0 and quiet package success output after all assertions:

```sh
make check RUN='^TestReferenceSubscriptsFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceSubscriptsFeatureSmoke$'
```

### Math constants selected proof

Permanent untagged `reference/numeric/math_constants_*_test.go` scenarios cover
cbrt/degrees/radians/pi/erf/erfc/gamma/FLOAT8 exp through the shared public fixture.
Root's initial pinned PostgreSQL18.0 discovery established accepted signed gamma
subnormals, exact radians zero boundaries and complete execution diagnostics.
Discovery filters and unknown assertions are removed before native-red.
Finite libm targets use independent constants and <=2ULP; endpoint signs,
NULL, metadata, exp subnormal witnesses and logical errors remain exact.
The review matrix's `gamma(-128.75)` witness retains the independent mathematical
target `0x92fa951a80fb7288` for native/private tests; pinned PostgreSQL18 tests-8
observed `0x92fa951a80fb728b`. Only that PG witness uses the independently recorded
target, still <=2ULP. This does not widen tolerance or alter sign/error categories.
Native Controls genuinely Dump/Reset/Restore and restore into a fresh Driver;
real mode replays the actual native-emitted SQL, not an invented server API.

Root runs each group in native and pinned mode, external900s per command:

```sh
make check RUN='^TestReferenceMathConstants(Values|IEEE|Binding|Metadata)$'
make check REFERENCE=1 RUN='^TestReferenceMathConstants(Values|IEEE|Binding|Metadata)$'
make check RUN='^TestReferenceMathConstants(GammaFrontier|GammaReviewBoundaries|BoundaryDiagnostics|HeldParameters|SQLPrepared)$'
make check REFERENCE=1 RUN='^TestReferenceMathConstants(GammaFrontier|GammaReviewBoundaries|BoundaryDiagnostics|HeldParameters|SQLPrepared)$'
make check RUN='^TestReferenceMathConstants(Consumers|Controls|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferenceMathConstants(Consumers|Controls|FeatureSmoke)$'
make check RUN='^TestReferenceMathScalar(ExpSubnormals|FloatingOverloads)$'
make check REFERENCE=1 RUN='^TestReferenceMathScalar(ExpSubnormals|FloatingOverloads)$'
make check RUN='^TestMathConstants(Provider|Signatures|Cancellation|PiConsumers)$'
make check RACE=1 RUN='^TestReferenceMathConstants(HeldParameters|Controls)$'
make check REFERENCE=1 RACE=1 RUN='^TestReferenceMathConstants(HeldParameters|Controls)$'
```

Whole-project Docker-free verification is `make quickcheck`, not a broad check
selector. Finalcheck is post-integration under external1800s. Never `make refcheck`.
The separate public program `local://math-constants-feature-smoke.go` stays in the
OMP session. Root materializes `.math-constants-feature-smoke/main.go`, runs
`timeout 60s go run ./.math-constants-feature-smoke` and removes that owned
materialization before review. Quiet exit0 requires all eight values/types,
held-handle errors/recovery, IEEE/range boundaries, stored pi same/fresh-driver
roundtrips and failed later-row DML atomicity. Passing tests are not that smoke.

### MONEY contracts

MONEY definitions use `RUN='^TestReferenceMoney'` in both
`make check` and
`make check REFERENCE=1` (900 seconds per invocation).
Run affected catalog/locale definitions with
`RUN='^TestReference(PGCast|PGDescription|FormatEncoding)'` in both modes.
Feature smoke is `RUN='^TestReferenceMoneyFeatureSmoke$'` in both modes: exact
integer versus float division, FLOAT8 monetary ratio metadata, prepared bytes,
numeric endpoint conversion, recoverable overflow and typed native Dump replay
have independently specified observations. Native controls stay explicitly
native-only inside the shared scenario; discovery filters are not retained.
The exact money_ops catalog inventory (OID10047/family2099/input790/method403/
defaulttrue) is explicitly PostgreSQL-only: native pg_opclass projection is
outside this part. `TestMoneyDefaultOrderingProvider` asserts actual native
class ownership/default identity, input admission and endpoint comparison;
the shared monetary arithmetic scenario independently exercises real index
creation, ordering and grouping in both modes. Native declared MONEY columns
have known nullability; literal/computed expression nullability remains unknown,
including a typed NULL literal. Tests assert these distinct metadata contracts.
`TestReferenceMoneyUniqueIndexes` covers constraint-backed and standalone unique
keys, direct/prepared differently spelled equal amounts, exact large-unit
neighbors, default NULL DISTINCT behavior, complete23505 diagnostics and atomic
failed writes. Locale/stability coverage queries the immutable arithmetic
expression index before and after INSERT/UPDATE/DELETE maintenance.

MONEY Restore cancellation distinguishes precanceled API rejection from a live
request canceled at reader EOF after consuming a nonempty actual Dump. The latter
proves complete reader progress beyond admission, before parsing or staged SQL
execution. Both preserve exact original rows/view and byte-identical catalog
Dump; subsequent default insertion establishes recovery and retained defaults.

Actual provider units use
`make check RUN='^TestMoney'`.
`TestMoneyMinimumDivisionOverflow` binds the real MONEY/INT2/INT4/INT8
Arithmetic providers with minimum raw units and -1, asserting complete native
22003 diagnostics, no result, unchanged operands and subsequent valid division.
This is the approved native safety exception: REL_18_0 cash_div_int64 lacks
an overflow guard. It is not a pinned oracle result, and no minimum/-1 SQL may
run in the shared singleton PostgreSQL fixture, discovery or smoke. Safe
neighboring negative division and checked overflow remain dual-mode witnesses.

### Direct trigonometric functions

Direct trigonometric definitions use `RUN='^TestReferenceMathTrigDirect'` in both
`make check` and `make check REFERENCE=1`, with900-second external deadlines.
Complete direct/inverse regression proof uses `RUN='^TestReferenceMathTrig'`;
private parser/binder/provider units use `RUN='^TestMathTrig'`.
Feature smoke selects `RUN='^TestReferenceMathTrigDirectFeatureSmoke$'` in both
modes: all eight FLOAT8 direct/prepared functions, exact degree poles and zero
signs, NULL, complete22003/22P02 diagnostics, fresh held bindings and recovery.
Independent degree matrices, rotations, subnormal order, ordinary at-most2ULP
targets and finite/sign/bounded-magnitude near-zero witnesses precede comparison.
All discovery backend filters must be absent from completed definitions.
Native precancellation and Reset/Dump/Restore/retained readers are explicit native
phases; actual native-emitted SQL replays on PostgreSQL when the real mode runs.
SQL PREPARE/EXECUTE uses a held physical connection and checks source Position
instead of inventing wire-portal Context. After edits the root runs `make quickcheck`;
post-integration verification remains `make finalcheck`.
The separate root-owned public smoke source is the living plan Appendix A;
materialize it temporarily, run `timeout 60s go run ./.math-trig-direct-feature-smoke`,
record quiet exit0 or full failure evidence, and remove its directory.

### Hyperbolic functions

The six unary FLOAT8 functions `sinh`, `cosh`, `tanh`, `asinh`, `acosh` and
`atanh` use independently specified public expectations validated first against
the pinned PostgreSQL18.0 UTF8/C oracle. Ordinary finite values allow at most
2ULP; IEEE categories and zero signs are exact. The moderate inverse identities
have bounded absolute residuals and supplement, not replace, manual constants.
The mathematical overflow frontier includes finite ±710 and infinite ±711;
`atanh(±1)` succeeds with signed Infinity. Domains independently assert full
`22003 / input is out of range` diagnostics, without Position or portal Context.
Go math and host C libm may differ in ordinary last bits or tiny-input handling;
pinned Linux preserves signed smallest subnormals for the four odd functions.
No portable errno emulation or universal cross-host bit identity is promised.

Run each small group in both modes, with an external900-second deadline:

```sh
timeout 900s make check RUN='^TestReferenceMathHyperbolic(Values|Identities|Frontiers|SpecialValues|Metadata)$'
timeout 900s make check RUN='^TestReferenceMathHyperbolic(Values|Identities|Frontiers|SpecialValues|Metadata)$' REFERENCE=1
timeout 900s make check RUN='^TestReferenceMathHyperbolic(DomainDiagnostics|AnalysisDiagnostics|HeldParameters)$'
timeout 900s make check RUN='^TestReferenceMathHyperbolic(DomainDiagnostics|AnalysisDiagnostics|HeldParameters)$' REFERENCE=1
timeout 900s make check RUN='^TestReferenceMathHyperbolic(Authorization|ConsumerAtomicity|Controls)$'
timeout 900s make check RUN='^TestReferenceMathHyperbolic(Authorization|ConsumerAtomicity|Controls)$' REFERENCE=1
timeout 900s make check RUN='^TestMathHyperbolic'
timeout 900s make check RUN='^TestReferenceMathHyperbolic(HeldParameters|ConsumerAtomicity|Authorization)$' RACE=1
```

Held parameter error/recovery uses the same actual statement. Column ACLs are
checked for empty and invalid-domain sources and rechecked after revocation;
routine ACL mutation is not implied. Late-row failed DML publishes no prefix.
Native Reset/Dump/Restore phases preserve symbolic views, old rows and held
readers; the completed real-mode scenario replays actual native Dump SQL on PG.
Private units assert descriptor ownership, strict NULL and ordinary cancellation
before NULL/domain evaluation, without fabricating SQLSTATE for context errors.
All temporary discovery backend filters are removed before native proof.

The separate root-owned public API smoke source is the living plan's Appendix A.
Materialize it temporarily as `.math-hyperbolic-feature-smoke/main.go`, then run
`timeout 60s go run ./.math-hyperbolic-feature-smoke`. Expected exit0 and output
`hyperbolic public API smoke: ok` require all six values/FLOAT8 types in direct
and prepared modes,710 finite,1000 overflow, signed poles/zero, NULL, exact
domain diagnostics, same-held recovery and atomic late-row DML failure.
Record observed output and remove the throwaway directory before review.
After implementation the root runs whole-project `make quickcheck`; selected
dual-mode proof and smoke precede review, and `make finalcheck` follows integration.

### GCD and LCM

The gcd/lcm family selects `RUN='^TestReferenceMathGcdLcm'` in both modes;
private parser/binder/provider coverage selects `RUN='^TestMathGcdLcm'`.
Use a900-second external deadline for each selected invocation:

```sh
timeout 900s make check RUN='^TestMathGcdLcm'
timeout 900s make check RUN='^TestReferenceMathGcdLcm'
timeout 900s make check REFERENCE=1 RUN='^TestReferenceMathGcdLcm'
timeout 900s make check RUN='^TestReferenceMathGcdLcmFeatureSmoke$'
timeout 900s make check REFERENCE=1 RUN='^TestReferenceMathGcdLcmFeatureSmoke$'
```

Independent expectations cover the six INT4/INT8/NUMERIC signatures, sign/zero/
signed-minimum rules, exact fractions and display scales, nonfinite-before-zero
precedence, strict NULL and ambiguity, full diagnostics and character positions.
Held contextual binds and SQL PREPARE/EXECUTE assert phase, metadata and recovery.
Invalid held input uses the existing exact `unnamed portal parameter $1 = '...'`
Context on both adapters. Math projections have unknown nullability; the separate
COUNT projection retains native known nonnullable versus pq unknown metadata.
Computed NUMERIC has native unknown DecimalSize; pq exposes unconstrained typmod
as DecimalSize(65535,65531,true) and ScanType any, independently asserted before
normalized string comparison. Later-row write overflow asserts unchanged data.
Generated/CHECK/expression-index and aggregate/grouping consumers, actual native
Dump/Reset/Restore/fresh-Driver replay, retained Rows/descriptors and native ordinary
precancellation are part of the same shared definitions. Real mode also replays
the actual native-emitted SQL on PostgreSQL, not an invented PostgreSQL Restore API.
Discovery filters and observation-only errors must be absent before native proof.
The root-owned standalone public smoke is the catalog living plan Appendix A;
materialize it temporarily as `.gcd-lcm-smoke/main.go`, run
`timeout 900s go run ./.gcd-lcm-smoke`, record quiet exit0 or complete failure
evidence, then remove it. This complements, not replaces, dual-mode feature smoke,
whole-project quickcheck and post-integration finalcheck.

### Numeric formatting

The numeric-formatting family independently asserts the five numeric
`to_char` overloads, preferred FLOAT8 SMALLINT route, `to_number`, exact/binary
rounding, C currency space, global FM, signs, Roman/ordinal/V/scientific
semantics, permissive parsing, complete diagnostics and deferred mask demand.
Metadata, consumers, atomicity, stored definitions and actual native control
replay use the shared fixture. The temporal ownership boundary now expects
numeric success; epoch/TIME/ARRAY boundaries remain unchanged. Negative
numeric setup uses signed numeric string casts, not adjacent unary arithmetic.
The repeated-parameter NUMERIC→TEXT conversion gap keeps independently
specified native 0A000 and PostgreSQL success rather than widening cast scope.
Consumer TEXT composition uses `to_char(to_number(...),'FM99')`, not an
unowned general NUMERIC→TEXT cast. Stored-definition inquiries retain their
exact SQL text and reuse deparse's own backend metadata contract; numeric
computed metadata remains unchanged. Default catalog lookup joins matching
OID columns instead of requiring an adjacent OID=REGCLASS operator. Failed
native Restore reaches an invalid Roman DEFAULT through a scalar INSERT
that omits its column, then asserts unchanged complete catalog/data, retained
cursors and held statements; it does not expand the scalar Restore importer.
The UUID-default regression covers rebased closed-tree admission through both
numeric providers: non-NULL UUID generators make `num_nulls` return0, so
`to_char` and `to_number(format(...))` independently return TEXT/NUMERIC0.
Direct/prepared DEFAULTs, reused held inserts, new DEFAULT VALUES after actual
Dump/Restore and emitted SQL replay retain symbolic execution. The numeric
feature smoke reuses the same scenario body. Package admission units preserve
unrelated volatility and row/parameter/stage rejection.

Run all changed definitions in both modes under separate 900-second deadlines:
```sh
make check RUN='^TestReference(NumericFormatting|DatetimeFormattingSignaturesOwnerBoundary)'
make check REFERENCE=1 RUN='^TestReference(NumericFormatting|DatetimeFormattingSignaturesOwnerBoundary)'
make check RUN='^TestReferenceNumericFormattingFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceNumericFormattingFeatureSmoke$'
make check RUN='^TestNumericFormatting'
make check RUN='^Test(NumericFormatting|DatetimeFormattingBindingAuthority)'
make check RUN='^TestNumericFormatting'
```
Smoke independently expects exact BIGINT9007199254740993, PR `<485>`,
Roman `CDLXXXV`, scientific ` 4.86e-04`, and V input
`1234.560000000000000000`, plus C/SMALLINT precision, NULL/error recovery,
late-write atomicity and stored default/view replay. Package units supplement
these public contracts with exhaustive canonical Roman1–3999, V factor-first
underflow, immutable plan/diagnostic ownership, Retag and cancellation.
The smoke also demands numeric/UUID defaults before and after native replacement;
these defaults must return0 rather than fail the closed UUID admission walker.

### Numeric arithmetic oracle-first contract

The `TestReferenceNumericArithmetic` family exercises exact decimal `+`, unary
`+`, `-`, unary `-`, `*`, `/`, `%` and integer/fractional `^`, with independent
finite strings, published PostgreSQL 18 special matrices, pinned high-precision
power strings, full diagnostics, direct/held inputs, computed NUMERIC metadata,
statement/TX atomicity, precancellation and symbolic native control replay.
`TestReferenceArithmeticBoundaries` migrates the former numeric exclusions to
shared direct/prepared successes. These test definitions do not themselves
claim native support.

The permanent expectations include observed PostgreSQL18.0 precision/scale and
complete binding/input diagnostics. The general-power guard success asserts the
full 2609-byte result through SHA256, plus exact scale/weight/prefix/suffix.
Unknown results were resolved in oracle-first discovery; no backend filters,
observation failures or numeric tolerances remain. Deparsed DEFAULT/view
expressions are replayed to assert left64/right512 semantically rather than
pinning incidental SQL whitespace. Both backends expose the actual unnamed
portal parameter Context for invalid numeric binds.
Zero-argument native Query retains its existing42P08 parameter-typing record;
pq's simple-query protocol reports42P02 instead. Supplied-argument direct and
Prepare routes independently require42P08, not protocol emulation.

Root runs each invocation under a separate 900-second deadline:

```sh
make check REFERENCE=1 RUN='^TestReference(NumericArithmetic|ArithmeticBoundaries)'
make check RUN='^TestReference(NumericArithmetic|ArithmeticBoundaries)'
make check RUN='^TestReferenceNumericArithmeticFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferenceNumericArithmeticFeatureSmoke$'
make check RUN='^TestReference(NumericAvg|MathScalar|FloatingArithmetic)'
make check REFERENCE=1 RUN='^TestReference(NumericAvg|MathScalar|FloatingArithmetic)'
make check RUN='^Test(NumericArithmetic|NumericPower|NumericAvg|MathScalar)'
make check RUN='^TestExpressionNumericArithmetic'
```

The composed feature smoke demands exact0.3, large integer9223372036854775809,
product2.76000, division2.5000000000000000, remainder−1.00, typed left64/right512
with NUMERIC metadata, published fractional18.379173679952560, fresh held input
scales/NULLs, domain/zero/format-overflow diagnostics and unchanged destinations
after late failures. Its native phase additionally checks deterministic
Dump/Reset/Restore, preserved cursors and symbolic DEFAULT/view/CHECK grouping.
