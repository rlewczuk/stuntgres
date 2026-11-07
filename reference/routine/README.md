# reference/routine

Builtin routine calls whose SQL surface is a catalog routine rather than an
operator or statement form. This package covers `pg_catalog.pg_notify(text,
text) -> void`: the scalar row shape, the NOTIFY-equivalent message (channel,
payload and sender identity), statement-end staging with commit-only delivery,
same-transaction ordering and duplicate folding, channel/payload validation,
missing-signature diagnostics, the released `pg_proc` declaration, and CTE
evaluation shapes. Related coverage lives elsewhere: [sessions](../sessions/README.md)
keeps the `LISTEN`/`NOTIFY`/`UNLISTEN` statements, session messages and the
receiver API; [catalogs](../catalogs/README.md) keeps routine metadata
inquiries; the exact 28-column `pg_proc` inventory is
[query_outer](../query_outer/pg_proc_builtin_inventory_test.go).

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
- **External consumers.** Coder's `coderd/database/pubsub` publishes with
  `select pg_notify(...)` plus a `[]byte` message and the
  `coderd/database/dbtestutil` latency measurer runs the same call shape; the
  optional post-merge external run is described in the part plan and never
  substitutes for the dual-mode selectors below.

Add `REFERENCE=1` for the oracle.

- Row shape (direct and prepared): `RUN='^TestReferencePGNotifyResult$'`
- Delivery, transactions, ordering and folding: `RUN='^TestReferencePGNotifyDelivery$'`
- Validation, signature diagnostics and byte boundaries: `RUN='^TestReferencePGNotifyDiagnostics$'`
- Released `pg_proc` declaration: `RUN='^TestReferencePGNotifyCatalog$'`
- CTE shapes: `RUN='^TestReferencePGNotifyCTE$'`
- All `pg_notify` families: `RUN='^TestReferencePGNotify'`