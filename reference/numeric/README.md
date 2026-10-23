# reference/numeric

Numeric types and functions: integer, SMALLINT, bitwise, floating-point and
NUMERIC arithmetic and literals; math functions (rounding/sign, logarithms,
powers and `width_bucket`, constants, `erf`/`gamma`, trigonometric, hyperbolic,
`gcd`/`lcm`); random functions; MONEY; numeric `to_char`/`to_number`; SUM and AVG
over numeric types; `generate_series` (integer, NUMERIC, TIMESTAMP, TIMESTAMPTZ),
`generate_subscripts` and set-returning functions in FROM. SUM over MONEY and
INTERVAL lives in `reference/aggregates`; INTERVAL AVG in `reference/temporal_types`;
temporal formatting in `reference/datetime_formatting`.

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md) (including its FLOAT8 2-ULP and
random-property rules). `TestMain` uses the default `fixture.Main(m)`.

- **Helpers.** `arithmetic_{error,metadata,migration}_helpers_test.go`,
  `floating_arithmetic_helpers_test.go`, `math_{constants,gcd_lcm,log_power}_helpers_test.go`,
  `math_privileges_test.go`, `numeric_cases_test.go`,
  `numeric_formatting_fixture_test.go`, `random_read_helpers_test.go`, and
  `seriesNativeCancel` in `series_lifecycle_test.go`.
- **Client metadata.** Computed NUMERIC has unknown `DecimalSize` natively, while
  pq reports the unconstrained typmod as `(65535, 65531, true)`; FLOAT4 `ScanType`
  is `float32` in pq and `float64` natively. Assert both before normalization.
- **Parameter-typing errors.** A direct query with `$n` and no arguments runs
  through pq's simple-query protocol (42P02), while native reports 42P08. Assert
  parameter-typing diagnostics with supplied arguments or Prepare (42P08 on both).
- **Series demand.** Huge target-list series with `LIMIT` must stop early natively.
  In FROM, PostgreSQL materializes the demanded function output, so FROM
  witnesses stay bounded.
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
- NUMERIC type and literals: `RUN='^TestReferenceNumeric(Literal|Controls|Conversion|Diagnostics|Exact|FormatBounds|Legacy|Raw|Setting|Specials|Statement)'`
- NUMERIC arithmetic operators: `RUN='^TestReferenceNumericArithmetic'`
- Math functions (all): `RUN='^TestReferenceMath'`
- Logarithms, powers, roots, `width_bucket`: `RUN='^TestReferenceMathLogPower'`
- `cbrt`, `degrees`, `radians`, `pi`, `erf`/`erfc`, `gamma`, `exp`: `RUN='^TestReferenceMathConstants'`
- Trigonometric functions: `RUN='^TestReferenceMathTrig'`
- Hyperbolic functions: `RUN='^TestReferenceMathHyperbolic'`
- `gcd`, `lcm`: `RUN='^TestReferenceMathGcdLcm'`
- Random functions: `RUN='^TestReferenceRandom'`
- Legacy bitwise/math/random smoke: `RUN='^TestReferenceSmoke'`
- MONEY: `RUN='^TestReferenceMoney'`
- Numeric `to_char`, `to_number`: `RUN='^TestReferenceNumericFormatting'`
- SUM and AVG over numeric types: `RUN='^TestReferenceNumeric(Sum|Avg)'`
- Integer `generate_series`: `RUN='^TestReferenceSeries'`
- NUMERIC `generate_series`: `RUN='^TestReferenceNumericSeries'`
- <a id="timezone-aware-series-selected-proof"></a>TIMESTAMP and TIMESTAMPTZ `generate_series`: `RUN='^TestReferenceTimestamp(tz)?Series'`
- `generate_subscripts`: `RUN='^TestReferenceSubscripts'`
- Set-returning functions in FROM: `RUN='^TestReferenceSRFFrom'`
