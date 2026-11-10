# reference/temporal_types

Dual-mode scenarios for scalar temporal and BYTEA codecs and consumers.
Date/time functions and zones belong to [datetime](../datetime/README.md#testing),
temporal ranges to [ranges_text_search](../ranges_text_search/README.md#testing),
and series to [numeric](../numeric/README.md#testing).

BYTEA codecs and binary consumers live here. BIT/VARBIT belongs to
[strings](../strings/README.md#testing); JSON codecs remain in
[binary_json_arrays](../binary_json_arrays/README.md#testing).

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` is plain
`fixture.Main(m)`.

- Assert raw cells before normalization. Native DATE and TIME cells are
  strings; lib/pq returns `time.Time` for DATE and TIME (including the next-day
  `24:00:00` endpoint). Finite TIMESTAMP cells are zero-offset `time.Time`.
  lib/pq returns DATE/TIMESTAMP `infinity` and `-infinity` as owned byte payloads.
- lib/pq may decode a zero-offset civil value with an unnamed fixed `Location`
  instead of `time.UTC`. Compare the instant, civil fields and precision, never
  `Location` identity.
- Prepared and transaction clock tests synchronize on a bounded, strict clock
  advance, never on sleeps.
- `date_oracle_test.go` and `time_dates_test.go` carry the `reference` build tag
  (PostgreSQL-only complements); only `REFERENCE=1` runs them.

Add `REFERENCE=1` for the oracle. The DATE and TIMESTAMP patterns exclude
same-prefix families in other packages (`DateRange`/`TimestampRange` in types,
`Datetime*` in datetime, `TimestampSeries` in numeric).

- DATE: `RUN='^TestReferenceDate([A-QS-Z]|Re[pt])'`
- TIME: `RUN='^TestReferenceTime[A-Z]'`
- TIMESTAMP: `RUN='^TestReferenceTimestamp([A-QT-Z]|Re|SQ|Set)'`
- INTERVAL, including `avg(interval)`: `RUN='^TestReferenceInterval'`
- BYTEA type, input and output settings: `RUN='^TestReferenceBytea'`
- BYTEA concatenation, length and trim: `RUN='^TestReferenceBinaryBasic'`
- `md5`, `sha*`, `crc32[c]`: `RUN='^TestReferenceBinaryHash'`
- BYTEA `substring`, `overlay`, `position`: `RUN='^TestReferenceBinarySlice'`
- BYTEA bit/byte access and `reverse`: `RUN='^TestReferenceBinaryAccess'`
- `encode`, `decode`, `convert*`: `RUN='^TestReferenceBinaryConversion'`

Binary helpers stay in `<family>_helpers_test.go` and
`binary_conversion_fixture_test.go`. Shared access diagnostics and scenario setup
use `fixture.BinaryAccessBodyError`, `BinaryAccessAnalysisError`,
`BinaryAccessDiagnosticStages` and `BitAccessScenario`; completed-output
cancellation uses `fixture.OutputGateCancel` from [fixture](../fixture/README.md#helpers).
BYTEA Length is `(MaxInt64,true)` through pq and `(0,false)` natively.
