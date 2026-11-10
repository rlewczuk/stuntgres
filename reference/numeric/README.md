# reference/numeric

Dual-mode scenarios for numeric scalar types, arithmetic, formatting, random
functions and series sources. General aggregate declarations belong to
[aggregates](../aggregates/README.md#testing), interval values/AVG to
[temporal_types](../temporal_types/README.md#testing), and temporal formatting to
[datetime_formatting](../datetime_formatting/README.md#testing).

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md) (including its FLOAT8 2-ULP and
random-property rules). `TestMain` uses the default `fixture.Main(m)`.

- **Helpers.** `arithmetic_{error,metadata,migration}_helpers_test.go`,
  `floating_arithmetic_helpers_test.go`, `math_{constants,gcd_lcm,log_power}_helpers_test.go`,
  `math_privileges_test.go`, `numeric_cases_test.go`,
  `numeric_formatting_fixture_test.go`, `random_read_helpers_test.go`,
  `selected_call_operands_helpers_test.go`, and `seriesNativeCancel` in
  `series_lifecycle_test.go`.
- **Client metadata.** Computed NUMERIC has unknown `DecimalSize` natively, while
  pq reports the unconstrained typmod as `(65535, 65531, true)`; FLOAT4 `ScanType`
  is `float32` in pq and `float64` natively. Assert both before normalization.
- Parameter-typing diagnostic tests supply arguments or use Prepare, keeping
  adapter inference separate from PostgreSQL simple-query behavior.
- Keep FROM-series workloads bounded: PostgreSQL materializes demanded function output.
- **Series cancellation.** `seriesNativeCancel` admits only when the provider
  frame and the named worker are in the same goroutine stack. Frame names
  matched by tests: `SourceCursor).Next` (`expr.SourceCursor`),
  `TimestampSeriesCursor).Next` and `NumericSeriesCursor).Next` (`sqltype`).
  Update the probes when renaming them.
- **Never run on the shared oracle:**
  - MONEY minimum divided by -1 (PostgreSQL's `cash_div_int64` has no overflow
    guard). Only the internal `TestMoneyMinimumDivisionOverflow` unit covers it;
    reference tests use safe neighboring values.
  - Reverse `generate_subscripts` over an exact INT32_MIN lower bound, which runs
    until the fixture cancels and invalidates the connection. Use INT32_MIN+1;
    the internal `TestSubscriptsBounds` covers exact MIN natively.

Add `REFERENCE=1` for the oracle.

- Integer and SMALLINT arithmetic: `RUN='^TestReference(Arithmetic|Smallint)'`
- Bitwise operators: `RUN='^TestReferenceBitwise'`
- REAL and DOUBLE PRECISION: `RUN='^TestReferenceFloat'`
- NUMERIC type and literals: `RUN='^TestReferenceNumeric(Literal|Controls|Conversion|Diagnostics|Exact|FormatBounds|Legacy|Raw|Specials|Statement)'`
- NUMERIC arithmetic operators: `RUN='^TestReferenceNumericArithmetic'`
- Math functions (all): `RUN='^TestReferenceMath'`
- Logarithms, powers, roots, `width_bucket`: `RUN='^TestReferenceMathLogPower'`
- `cbrt`, `degrees`, `radians`, `pi`, `erf`/`erfc`, `gamma`, `exp`: `RUN='^TestReferenceMathConstants'`
- Trigonometric functions: `RUN='^TestReferenceMathTrig'`
- Hyperbolic functions: `RUN='^TestReferenceMathHyperbolic'`
- `gcd`, `lcm`: `RUN='^TestReferenceMathGcdLcm'`
- Random functions: `RUN='^TestReferenceRandom'`
- Selected math, random and inquiry operands across query/DML consumers:
  `RUN='^TestReferenceSelectedCallOperands'`
- MONEY: `RUN='^TestReferenceMoney'`
- Numeric `to_char`, `to_number`: `RUN='^TestReferenceNumericFormatting'`
- SUM and AVG over numeric types: `RUN='^TestReferenceNumeric(Sum|Avg)'`
- Integer `generate_series`: `RUN='^TestReferenceSeries'`
- NUMERIC `generate_series`: `RUN='^TestReferenceNumericSeries'`
- TIMESTAMP and TIMESTAMPTZ series: `RUN='^TestReferenceTimestamp(tz)?Series'`
- `generate_subscripts`: `RUN='^TestReferenceSubscripts'`
- Set-returning functions in FROM: `RUN='^TestReferenceSRFFrom'`
