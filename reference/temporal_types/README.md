# reference/temporal_types

Dual-mode tests for the DATE, TIME, TIMESTAMP (without time zone) and INTERVAL
types: input forms and civil bounds, modifiers and precision, diagnostics and
statement atomicity, binding and raw metadata, ordering and comparison, clocks,
notices, retained cursors, cancellation, and native Dump/Reset/Restore with
PostgreSQL replay of the emitted SQL. Date/time functions, time zones and
INTERVAL arithmetic are in [reference/datetime](../datetime/README.md#testing);
temporal ranges are in [reference/ranges_text_search](../ranges_text_search/README.md#testing); timestamp
series are in [reference/numeric](../numeric/README.md#testing).

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
- A Go DATE bind at year-zero February 29 is February 29 BC natively but
  March 1 BC through lib/pq (its BC formatting). Encode both expectations.
- Prepared and transaction clock tests synchronize on a bounded, strict clock
  advance, never on sleeps.
- Typed TIMESTAMP setting-call arguments validate literal input before ordinary
  function applicability; no implicit TIMESTAMP-to-TEXT cast is introduced.
  Direct execution and preparation retain complete diagnostics and positions.
- `TestReferenceTimestampDiagnosticsAndAtomicity` creates its fixture inside each
  input subtest, before any backend work, so every input owns a full 30-second
  scenario. The backend child owns its assertions and never calls a parent-bound
  `Fatal`/`FailNow`.
- `date_oracle_test.go` and `time_dates_test.go` carry the `reference` build tag
  (PostgreSQL-only complements); only `REFERENCE=1` runs them.

Add `REFERENCE=1` for the oracle. The DATE and TIMESTAMP patterns exclude
same-prefix families in other packages (`DateRange`/`TimestampRange` in types,
`Datetime*` in datetime, `TimestampSeries` in numeric).

- DATE: `RUN='^TestReferenceDate([A-QS-Z]|Re[pt])'`
- TIME: `RUN='^TestReferenceTime[A-Z]'`
- TIMESTAMP: `RUN='^TestReferenceTimestamp([A-QT-Z]|Re|SQ|Set)'`
- INTERVAL, including `avg(interval)`: `RUN='^TestReferenceInterval'`
