# reference/session_state

Runtime settings (SET/SHOW/RESET, `set_config`/`current_setting`, SET LOCAL,
SHOW ALL, the builtin and custom registry, `pg_settings` metadata), statement,
lock, idle and transaction timeouts, the physical-session adapter (pool reuse,
Reset/Restore, cancellation) and direct-message batches, including retained
concurrent materialized-view refresh. Related families: SQL PREPARE, DISCARD,
notices and wire-server tests in [sessions](../sessions/README.md); transaction
characteristics (SET TRANSACTION, isolation defaults) in
[locking_transactions](../locking_transactions/README.md).

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md), including its **Timeouts** rules.

- `TestMain` is `fixture.Main(m)`.
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
- Capture PID, OIDs and the log baseline before arming or `BeginTx`, then run Go
  work through that `Tx`: querying its borrowed `Conn` deadlocks behind
  `database/sql` ownership.
- Preparing a SELECT inside a transaction keeps its relation lock. When a peer
  must take a conflicting lock before a held Execute, prepare
  `LOCK TABLE ... IN ACCESS SHARE MODE` (metadata-only) instead.
- Read and close retained native Rows before the dead-handle probe: the probe's
  `ErrBadConn` makes `database/sql` close the `Conn`, which blocks while Rows
  still owns the release callback.
- **Adaptive timing.** Timing-sensitive witnesses use adaptive budgets (200 ms
  doubling up to 6400 ms within the 30 s scenario) or fixed budgets with at most
  three attempts in a six-second setup allowance. Only a validated setup expiry
  (exact 25P04, nil outputs, dead owner, intact peer data, observed after the
  earliest configured expiry) permits another attempt, on a fresh physical owner.
  Assertion failures never retry, and exhaustion fails. Dispose of an expired or
  armed owner with `conn.Raw(func(any) error { return driver.ErrBadConn })`.
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
- A builtin settings registry change must pass every complete-inventory test:
  `RUN='^TestReference(Settings(BuiltinRegistry|ShowAllPlaceholdersAndLifecycle)|FormatEncodingSettings|TimezoneBuiltinRegistryDescriptions)$'`
  (session_state, patterns_encoding, datetime) in both modes.
- `TestReferenceSettingsPrivilegeAndTimeoutOracle` and
  `TestReferenceSettingsLocalPrivilegeOracle` carry the legacy `reference` build
  tag and run only with `REFERENCE=1`; a native no-match run is not evidence.

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- Settings SQL, registry, SHOW ALL: `RUN='^TestReferenceSettings'`
- SET LOCAL and transaction-scoped settings: `RUN='^TestReferenceSettingsLocal'`
- `pg_settings` metadata and statistics settings: `RUN='^TestReferenceStatisticsSettings'`
- Timeouts: `RUN='^TestReference(Timeout|SettingsPublicTimeout)'`
- Physical-session adapter: `RUN='^TestReferenceSession(PhysicalControls|DecimalInteger|Reset|AdapterCancellation|ShowCommand)'`
- Direct-message batches: `RUN='^TestReferenceDriverBatch'`
- Concurrent MV refresh in retained batches: `RUN='^TestReference(MaterializedRefreshConcurrentRetained|DriverBatchRefresh)'`
