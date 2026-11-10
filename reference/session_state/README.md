# reference/session_state

Dual-mode scenarios for runtime settings, timeouts, physical-session adapters,
message batches and session/statistics monitoring. Wire/session protocols belong
to [sessions](../sessions/README.md#testing); transaction characteristics to
[locking_transactions](../locking_transactions/README.md#testing); cumulative
relation/function view families to [statistics](../statistics/README.md#testing).

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md), especially **Timeouts**.
The package uses the shared pg_stat_statements preload and logical-WAL options
for genuine oracle producers; SQL objects are installed explicitly.

Settings-only native simulations are not physical WAL, logging, replication,
page placement or background-worker facilities. Monitor actual native attachments
and represented state without fabricating senders/receivers or unavailable endpoints.
Native logical estimates and PostgreSQL physical counters have independent
expectations; identities, metadata, authority and lifetime remain exact.

Cumulative none/cache/snapshot cuts are reader-owned. Clearing a cut does not
publish pending writer work. Establish real setup baselines and observe ordinary
publication independently before expecting a refreshed reader. Runtime statistics
and collector history are not logical dump contents.

- Helpers: `driver_batch_helpers_test.go`, `driver_batch_refresh_helpers_test.go`,
  `driver_batch_role_retirement_helpers_test.go`, `settings_adapter_helpers_test.go`,
  `settings_authenticated_helpers_test.go`, `settings_local_helpers_test.go`,
  `timeout_helpers_test.go` (`timeoutReferenceFixture`: `fatalProbe`,
  `awaitFatal`, adaptive preparation) and `timeout_oracle_helpers_test.go`
  (oracle FATAL log evidence). Shared fixture helpers `SettingsPhysical`,
  `BatchSet`/`BatchReadSet`/`BatchSets` and `NoticeWitnesses`/`NoticesInternalDebug`
  are documented in the [fixture README](../fixture/README.md#helpers); after
  changing them, also run their consumers in `sessions` (notices, SQL prepared,
  DISCARD) and `cursors` (`TestReferencePGCursorsSourcesAndTime`).
- **FATAL evidence on PostgreSQL.** lib/pq reports a server FATAL as
  `driver.ErrBadConn` (direct, Prepare, idle and Go-`Tx`-held execution) or
  `sql.ErrConnDone` (`Conn`-held Execute), without SQL fields. The full
  diagnostic comes from the server log: the oracle runs with
  `log_line_prefix=timeout-oracle|%p|%c|%e|`. Before arming, capture the owner's
  PID and backend-start incarnation (`%c`) from `pg_stat_activity`; read logs
  through `fixture.LogClient` with `Since` set to backend start minus one second;
  match the exact PID, incarnation and SQLSTATE and reject pre-arm baseline lines.
  Reads are synchronous with a 32 MiB cap, and truncated or malformed frames fail.
  `TestTimeoutOracleLogFrames` covers the framing without SQL.
- **Observing termination.** Natively, poll `driver.Validator.IsValid` through
  `sql.Conn.Raw`; on PostgreSQL, use the FATAL log observer. Elapsed time or a
  completed sleep never proves that a timer callback ran. `fatalProbe` requires
  terminal state before the first owner SQL. Owner probes restart idle timers, so
  never poll the owner while waiting for idle expiry.
- **Idle transaction setup.** Finish the normal private transaction setup before
  arming the idle-in-transaction timer. For an aborted transaction, arm outside
  the transaction, then send BEGIN, the private lock/write and the failing SELECT
  in one message; the idle budget starts only after that message completes.
- Capture PID, OIDs and the log baseline before arming or `BeginTx`, then run Go
  work through that `Tx`: querying its borrowed `Conn` deadlocks behind
  `database/sql` ownership.
- Preparing a SELECT inside a transaction keeps its relation lock. When a peer
  must take a conflicting lock before a held Execute, prepare
  `LOCK TABLE ... IN ACCESS SHARE MODE` (metadata-only) instead.
- Read and close retained native Rows before the dead-handle probe: the probe's
  `ErrBadConn` makes `database/sql` close the `Conn`, which blocks while Rows
  still owns the release callback.
- Timing-sensitive setup uses the helper's bounded adaptive budget. Only a
  validated setup expiry permits another attempt on a fresh physical owner;
  assertion failures never retry and exhaustion fails. Dispose of every armed
  or expired owner with `conn.Raw(func(any) error { return driver.ErrBadConn })`.
- The order of two simultaneously pending deadlines is not observable through
  public SQL; do not assert it in reference tests. Deterministic ordering is
  covered by `internal/engine` units (`TestSessionTimeoutResidualBefore*` in a
  `synctest` bubble, `TestSessionTimeoutPendingOrderMechanics`).
- Timeout lock scenarios share `settingsTimeoutWorkload` and pass the operation's
  completion channel to `fixture.LockWaitBehind` (self-timing SQL).
- **Batches.** Notice callbacks run only after a message completes, so they
  cannot synchronize its active wait. Native controls use the unwrapped native
  pool that shares the notice connector's engine. Internal `TestDriverBatch*`
  units acquire with `AcquireCommand`, not `Acquire`, to match the public
  statement budget and idle boundaries.
- `TestReferenceSettingsPrivilegeAndTimeoutOracle` and
  `TestReferenceSettingsLocalPrivilegeOracle` carry the legacy `reference` build
  tag and run only with `REFERENCE=1`; a native no-match run is not evidence.

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- Settings: `RUN='^TestReferenceSettings'`
- Settings metadata: `RUN='^TestReferenceStatisticsSettings'`
- Timeouts: `RUN='^TestReference(Timeout|SettingsPublicTimeout)'`
- Physical-session adapter: `RUN='^TestReferenceSession(PhysicalControls|DecimalInteger|Reset|AdapterCancellation|ShowCommand)'`
- Message batches: `RUN='^TestReferenceDriverBatch'`
- Session activity: `RUN='^TestReferenceSessionActivity'`
- Backend identity/state: `RUN='^TestReferenceBackend'`
- Monitoring views: `RUN='^TestReferencePGStat'`
- Database statistics: `RUN='^TestReferenceStatDatabase'`
- Snapshot/reporting lifecycle: `RUN='^TestReferenceStatsSnapshot'`
- Statistics reset: `RUN='^TestReferenceStatsReset'`

Family-local literal inventories define expected setting rows; avoid duplicating
descriptor counts or feature matrices here. Shared helpers belong to
[fixture](../fixture/README.md#helpers).
