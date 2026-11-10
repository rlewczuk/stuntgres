# reference/routine

Dual-mode builtin routine consumers for `pg_notify`.
LISTEN/NOTIFY statements and receiver APIs belong to
[sessions](../sessions/README.md#testing); routine metadata inquiries to
[catalogs](../catalogs/README.md#testing).

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` is `fixture.Main(m)`.

- **Helpers.** `pg_notify_helpers_test.go` provides `pgNotifyScenario`,
  `pgNotifyConn`, `pgNotifySQL`, `pgNotifyVoid`, `pgNotifyReceive`,
  `pgNotifyAbsent`, `pgNotifyHealth` and `pgNotifyPID`. Receivers use
  `stuntgres.WaitForNotification` natively and one bounded `sql.Conn.Raw`
  callback around pgx `WaitForNotification` on PostgreSQL, matching the
  [driver contract](../../docs/DRIVER.md#notices-and-notifications). Senders use
  ordinary fixture-scoped `b.DB.Conn` handles; native-only phases additionally
  exercise `[]byte` and typed-nil parameters. A 100 ms absence probe is the
  terminal observation of its phase; the pinned physical receiver remains
  reusable for a later sentinel, as in `reference/sessions`.

Add `REFERENCE=1` for the oracle.

- Notification routines: `RUN='^TestReferencePGNotify'`