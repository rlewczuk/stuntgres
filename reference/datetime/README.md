# reference/datetime

Dual-mode scenarios for date/time functions, arithmetic, clocks and time zones.
Scalar temporal codecs belong to [temporal_types](../temporal_types/README.md#testing),
formatting to [datetime_formatting](../datetime_formatting/README.md#testing).

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` uses the default
`fixture.Main(m)`.

- **Helpers.** `timezone_helpers_test.go` (held statements, `timezoneTransactionClock`,
  restore destinations, failing/canceling Restore readers); per-family scenario,
  metadata and diagnostic helpers in `datetime_{age_finite,extract,justify,make,overlaps}_fixture_test.go`;
  `datetimeSleepOwners` (`datetime_sleep_owners_test.go`) and
  `datetimeSleepActiveCancellation`/`datetimeSleepAtomicWriteCancellation`
  (`datetime_sleep_cancellation_test.go`).
- `timezoneTransactionClock` captures each backend's own transaction instant;
  never derive clock expectations from another backend. Keep raw pq temporal
  codec checks under ISO DateStyle.
- Prepare held timeout commands at timeout zero, then arm only the intended
  operation. Active-cancellation scenarios keep timers unarmed.
- Use shared fixture sleep/provider admission helpers rather than local timing
  races; every cancellation owns a bounded join.

Add `REFERENCE=1` for the oracle.

- Date/time functions: `RUN='^TestReferenceDatetime'`
- Time zones: `RUN='^TestReferenceTimezone'`
