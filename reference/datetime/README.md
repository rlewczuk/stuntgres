# reference/datetime

Date/time functions and operators: `date_trunc`, `date_bin`, transaction,
statement and wall clocks (`now`, `transaction_timestamp`, `statement_timestamp`,
`clock_timestamp`, `timeofday`) and the SQL `CURRENT_*`/`LOCAL*` forms,
`timezone`/`AT TIME ZONE` and `date_add`/`date_subtract`, `TimeZone` settings and
abbreviations, datetime arithmetic operators, `justify_*`, `OVERLAPS`,
`extract`/`date_part`, `make_*` constructors, `age` (including `age(xid)`) and the
`pg_sleep` family. DATE/TIME/TIMESTAMP/INTERVAL type input, codecs and precision
(including `TestReferenceInterval*` and `TestReferenceTimestampSQLTransactionChainClock`)
live in `reference/temporal_types`; `to_char`/`to_date` formatting in
`reference/datetime_formatting`; transaction advisory-lock lifetime in
`reference/locking_transactions`.

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` uses the default
`fixture.Main(m)`.

- **Helpers.** `timezone_helpers_test.go` (held statements, `timezoneTransactionClock`,
  restore destinations, failing/canceling Restore readers); per-family scenario,
  metadata and diagnostic helpers in `datetime_{age_finite,extract,justify,make,overlaps}_fixture_test.go`;
  `datetimeSleepOwners` (`datetime_sleep_owners_test.go`) and
  `datetimeSleepActiveCancellation`/`datetimeSleepAtomicWriteCancellation`
  (`datetime_sleep_cancellation_test.go`).
- **Clocks.** `timezoneTransactionClock` captures each backend's own transaction
  instant; never derive clock expectations from another backend. Shared prepared
  tests assert `now() <= statement time`; exact one-command equality belongs in
  package units. Non-ISO `DateStyle` witnesses use TEXT-only `timeofday()` and
  explicit timestamp casts; raw pq temporal codecs are checked only under ISO.
- **Sleep timeouts.** Sleep owner and savepoint fixtures keep `statement_timeout`
  at 0 for setup, lock probes and recovery and arm `100ms` only around the timeout
  witnesses. All held sleep statements are prepared before arming, and the timeout
  is not toggled between direct and held cases (general rule in TESTING.md).
  Active and atomic-write cancellation keep the timeout at 0, so admission and
  explicit cancellation, not a deadline, end each wait.
- **Active cancellation.** `pg_sleep` waits use `fixture.SleepActiveCancel`
  ([fixture README](../fixture/README.md#helpers)). `date_add` cancellation uses
  `fixture.BinaryConversionActiveCancel` on a finite row-dependent INSERT SELECT.
- **Stored sleep controls.** `TestReferenceDatetimeSleepStored` runs under a
  2-second `statement_timeout`: its catalog probes resolve the fixture table first
  and bound every join input before joining. Each native Dump, Reset and Restore
  is observed separately under the 30-second scenario context; admission requires
  the worker and the sleep provider in the same live goroutine stack.
- **Frame names matched by tests.** `sqltype.addTimestampInterval`,
  `functions.datetimeSleepWaitRemaining` and the test worker
  `datetime_test.storedSleepControlWorker`. Update the probes when renaming them.

Add `REFERENCE=1` for the oracle.

- `date_trunc`: `RUN='^TestReferenceDatetimeTrunc'`
- `date_bin`: `RUN='^TestReferenceDatetimeBin'`
- Transaction, statement and wall clocks: `RUN='^TestReferenceDatetimeTxTimes'`
- `CURRENT_*` and `LOCAL*` forms: `RUN='^TestReferenceDatetimeCurrent'`
- `timezone`, `AT TIME ZONE`, `date_add`/`date_subtract`: `RUN='^TestReferenceDatetimeZone'`
- `TimeZone` settings, abbreviations, zoned conversion: `RUN='^TestReferenceTimezone'`
- Datetime arithmetic operators: `RUN='^TestReferenceDatetimeArithmetic'`
- `justify_days`/`justify_hours`/`justify_interval`: `RUN='^TestReferenceDatetimeJustify'`
- `OVERLAPS`: `RUN='^TestReferenceDatetimeOverlaps'`
- `extract`/`date_part`: `RUN='^TestReferenceDatetimeExtract'`
- `make_date`/`make_time`/`make_timestamp[tz]`/`make_interval`: `RUN='^TestReferenceDatetimeMake'`
- `age`, including `age(xid)`: `RUN='^TestReferenceDatetimeAge'`
- `pg_sleep`, `pg_sleep_for`, `pg_sleep_until`: `RUN='^TestReferenceDatetimeSleep'`
