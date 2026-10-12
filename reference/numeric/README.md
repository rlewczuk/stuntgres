# reference/numeric

## Testing

Follow the [shared testing guide](../../docs/TESTING.md) for common policy,
fixture usage, and oracle-first workflow. Run the commands below from the
repository root.

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
