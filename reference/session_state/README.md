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
- Builtin settings changes retain selected-value, metadata, lifecycle and
  security witnesses without asserting the size of the evolving registry.
  Run `RUN='^TestReference(Settings(BuiltinRegistry|ShowAllPlaceholdersAndLifecycle|UtilitySnapshotOwnership)|FormatEncodingSettings|TimezoneBuiltinRegistryValues|StatisticsSettings)'`
  in both modes.
  `TestReferenceStatisticsSettingsMetadata` keeps the complete selected native
  tuples, including boot/startup source distinctions, and the same released
  isolation enum order in both modes. Internal `TestPhysicalSettingsReadAccess`
  independently names all twenty-four restricted builtins and asserts every aggregate
  membership and individual read, not merely a filtered row count.
- `TestReferenceSettingsPrivilegeAndTimeoutOracle` and
  `TestReferenceSettingsLocalPrivilegeOracle` carry the legacy `reference` build
  tag and run only with `REFERENCE=1`; a native no-match run is not evidence.

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- Settings SQL, registry, SHOW ALL: `RUN='^TestReferenceSettings'`
- SET LOCAL and transaction-scoped settings: `RUN='^TestReferenceSettingsLocal'`
- `pg_settings` metadata and statistics settings: `RUN='^TestReferenceStatisticsSettings'`
- Vacuum settings: `RUN='^TestReferenceVacuumSettings'` uses independent
  released 28-row literals and the shared physical-session helpers for full
  metadata, direct/prepared reads and assignments, immutable context/SET-grant
  precedence, ordinary authority, typed validation and transactional lifetimes.
  Native control phases use recorded public pools and retained catalog rows;
  logical constraint/data witnesses assert the setting-only simulation boundary.
  The pinned official image's initdb configuration writes
  `autovacuum_worker_slots=16`, so only that PostgreSQL row has configuration-file
  source; owner/inherited-reader file provenance is exact and ordinary-login
  provenance stays NULL. Native retains default source without invented files.
  Shared logical sentinel setup, reads and DML explicitly target `public`.
  Changes to the shared numeric consumer helper also select its existing
  `TestReferenceSettingsPublicTypedSQL` caller in both modes.
- Preset settings: `RUN='^TestReferencePresetSettings(Inventory|Context|Authority|Lifecycle|Smoke)$$'`
  covers all 20 independently expected release descriptors, full catalog metadata,
  direct/prepared text presentation, context-first 55P02 refusals, unrestricted
  authenticated reads (plain/superuser/read-all-settings/SET grantee), LOCAL
  notices, savepoints, constraints, held statements and logical native controls.
  Run each bounded top-level name in both native and `REFERENCE=1` modes.
  Smoke exercises 1GB/131072/8kB segment presentation, octal mode versus decimal
  reset, 16MB WAL labels, SQL_ASCII boot versus UTF8 current and independent
  native 18.0 versus oracle 18.6 identity, not physical allocation.
- Client connection defaults: `RUN='^TestReferenceClientSettings(Inventory|Validation|Authority|Lifecycle|SimulationSmoke)$$'`
  covers all 46 selected settings, independently expected full metadata and
  diagnostics, direct/prepared routes, parameter SET grants versus restricted
  reads, catalog validation, LOCAL/savepoint/pool/native-control behavior and
  the explicit setting-only safety boundary. Run both native and REFERENCE=1.
  SimulationSmoke is the new-feature smoke (8MB SHOW versus raw8192, XML alias,
  native FK/routine safety and unchanged logical rows), not a physical-effect
  claim. Native initialization is UTF8/C/UTC/simple FTS; the pinned 18.6
  initialized oracle has Etc/UTC and English FTS. pq initializes both encoding
  and DateStyle with source client and NULL file provenance; other configured
  defaults retain authorized config-file provenance. Session overrides expose
  NULL file fields; RESET restores the actual captured client/config source;
  unauthorized readers never see file fields. Ordinary-login scenarios use
  the fixture's role-core-fixture password convention.
- Resource settings: run each bounded `TestReferenceResourceSettingsInventory`,
  `TestReferenceResourceSettingsContext`, `TestReferenceResourceSettingsValidation`,
  `TestReferenceResourceSettingsAuthority` and `TestReferenceResourceSettingsLifecycle`
  with `make check RUN='^NAME$'`, then the same argv with `REFERENCE=1`.
  The independent44-row literals cover all17 metadata columns, boot versus
  initialized defaults, unrestricted ordinary/inherited-reader inventory,
  typed direct/prepared SQL, complete diagnostics, grants/revocation, LOCAL,
  reset/savepoints and retained native controls. The oracle is pinned18.6;
  native's simulated Linux enum profile intentionally omits io_uring, while
  reference expects the Debian build's additional IO method. Config-file
  provenance is permitted only for the independently named initdb defaults.
  Provider injection is an internal-only unit seam, not a public SQL API.
  TEMP first-use witnesses capture a sticky initialized count at owned logical
  table data access, with reset bypass, rollback retention, COPY streaming,
  failed foreign attempts, sequences, pool replacement and logical controls.
  The same Lifecycle selector independently proves first initialization through
  SELECT, UPDATE, DELETE, MERGE and streaming COPY TO on fresh **empty native**
  owners, without prior INSERT/COPY FROM. Empty-source timing is native's
  explicit logical simulation boundary; the populated PostgreSQL preservation
  cases remain shared. Initialized direct/prepared SET and bound set_config
  separately assert malformed/range errors before sticky-count rejection,
  normalized1MB/8MB candidates and unchanged current/reset-source values.
  `TestResourceTemporaryInitializedValidation` checks full diagnostics and
  unchanged native Actions/values/provenance immediately after Execute, before
  Finish can restore a statement checkpoint.
  Ordinary first-use SQL runs on its recorded fixture owner. Streaming COPY TO
  uses an unwrapped pinned transport with explicit value/source, output/count
  and complete diagnostic assertions; it never reuses another SQL owner's
  middleware recorder.
  No physical-page/cache timing or resource allocation is claimed.
  Feature smoke argv:
  `make check RUN='^TestReferenceResourceSettingsSimulationSmoke$'` and
  `make check RUN='^TestReferenceResourceSettingsSimulationSmoke$' REFERENCE=1`.
  Expect1MB/raw1024 work_mem, typed flush/hash/combine values, exact vacuum
  rejection, immutable context errors, TEMP captured-count/reset nuance and
  unchanged sentinel/peer data. Also run migrated definitions
  `TestReferenceSettingsPublicTypedSQL` and
  `TestReferenceObjectGrantParametersTablespaces` in both modes, plus retained
  `TestReferenceQueryParallelSettings`/`TestReferenceQueryParallelErrors`.
- WAL settings: `RUN='^TestReferenceWALSettings'` covers the independent 40-row
  inventory, all metadata and current/boot/reset distinctions, unrestricted
  direct/prepared reads, context-first refusals, exact typed validation,
  effective parameter authority, transactional lifetimes and native controls.
  Simulation witnesses assert logical rows/constraints and peer isolation,
  never physical WAL, compression, durability, recovery or timing effects.
  Run the bounded top-level definitions in native and `REFERENCE=1` modes.
  The pinned fixture initializes PostgreSQL fsync off with command-line source,
  boot on and reset off; native retains on/default/reset on. Both profiles
  initialize wal_buffers to 512 with default source and boot -1. Only
  max_wal_size/min_wal_size use configuration-file provenance in this family.
  Ordinary validation actors retain setting authority without schema grants;
  their logical sentinel is created and inspected by the authorized owner.
- Replication settings: `RUN='^TestReferenceReplicationSettings'` covers the exact
  26-name family, independently expected metadata, immutable contexts, mutable
  values/list quoting, read versus SET authority, physical-session capture and
  logical native controls. Run bounded top-level tests in both modes.
- Compatibility settings: `RUN='^TestReferenceCompatibleSettings'` covers all
  nine released descriptors and unrestricted reads, Boolean/hidden-enum input,
  sighup precedence, parameter SET authority, transaction/savepoint and physical
  attachment lifecycle, native controls and the seven setting-only simulations.
  Integer sentinel rows constrain logical-state preservation without claiming
  absent array, lexer, large-object security, rewrite or physical scan effects.
- Lock settings: `RUN='^TestReferenceLockSettings'` covers independent selected
  metadata, unrestricted visibility, typed validation, immutable contexts,
  effective parameter grants, transactional/physical lifecycle and the
  setting-only boundary without timing or lock-capacity claims.
- Logging settings: `RUN='^TestReferenceLoggingSettings'` covers all 42 literal
  release descriptors, typed reads/full metadata, contexts, parameter SET versus
  restricted-read authority, pure-domain unit counterparts, LOCAL/savepoints,
  physical reuse/replacement and logical native controls. Settings-only
  simulations never claim log emission, files, processes or background effects.
  With `log_parameter_max_length_on_error` enabled, parameterized failing
  `set_config` calls assert PostgreSQL's exact unnamed-portal parameter Context
  separately from native's empty Context. This oracle diagnostic difference is
  not an implemented native log-emission or diagnostic-rewriting effect.
- Timeouts: `RUN='^TestReference(Timeout|SettingsPublicTimeout)'`
- Physical-session adapter: `RUN='^TestReferenceSession(PhysicalControls|DecimalInteger|Reset|AdapterCancellation|ShowCommand)'`
- Direct-message batches: `RUN='^TestReferenceDriverBatch'`
- Concurrent MV refresh in retained batches: `RUN='^TestReference(MaterializedRefreshConcurrentRetained|DriverBatchRefresh)'`
