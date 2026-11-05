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

Temporary tablespace policy uses `RUN='^TestReferenceTempTablespaces'` in both
fixture modes, covering live creation effects, metadata, diagnostics, authority,
transaction and routine lifetimes, list selection and the physical boundary.

- Default tablespace effects: `make check RUN='^TestReferenceDefaultTablespace'`
  and `make check REFERENCE=1 RUN='^TestReferenceDefaultTablespace'` cover empty
  versus `pg_global` creation, full metadata/diagnostics, normal CREATE authority,
  partition-index synthesis versus reuse, temporary/partition defaults,
  prepared execution, transaction/savepoint and nested settings. The durable
  feature smoke is `RUN='^TestReferenceDefaultTablespaceFeatureSmoke$$'` in both
  modes. Physical CREATE TABLESPACE remains an explicit native capability
  boundary rather than simulated placement.

Current-source, physical identity, event clocks and native-control scenarios use
`RUN='^TestReferenceSessionActivity'` in both fixture modes.

Current-backend callable scenarios use `RUN='^TestReferenceBackendCurrent'` in
both fixture modes. Dedicated physical connections distinguish retained status
membership, current inherited projection, owning-provider cumulative cuts and
actual retirement; no ordering is inferred from un-ordered record sources.
Cancellation starts a live operation, observes its genuine relation-lock wait,
then cancels and joins the row consumer before releasing the holder. Recovery
checks the preserved reader, or proves documented pq transport retirement before
acquiring a healthy replacement; monitored target birth/reset remain unchanged.

Legacy backend-ID identity scenarios use `RUN='^TestReferenceBackendIdentity'`
in both fixture modes. Independently known physical peers establish slot/PID,
database, immutable login and birth relationships; slots are unordered and may
be recycled after retirement. Shared activity cuts retain old membership until
clear/end, while current inherited authority is checked on every held Execute.
Native endpoints are typed NULL; the TCP oracle uses its actual activity
endpoints. Native Dump/Reset/Restore phases retain real attachments and detached
rows, and cancellation requires observed admission and joined workers.


Logical-slot monitoring uses `RUN='^TestReferencePGStatReplicationSlots'` in both
fixture modes for typed empty/populated inventory, query composition, current
SELECT admission, stock test_decoding publication/drop, physical rejected-start
no-publication and complete read-only diagnostics.
The package oracle retains pg_stat_statements and enables logical WAL through
the existing fixture options so populated scenarios use genuine logical slots.

Replication monitoring uses
`RUN='^TestReferencePGStatReplication(Shape|ReadOnly|Privileges|Lifecycle|FeatureSmoke)$'`
with `make check` and then `make check REFERENCE=1`. It covers the exact twenty
nullable columns on direct, fresh and retained prepared empty results, catalog
type/array links and symbolic view display, full read-only diagnostics,
ordinary activity/settings invariance, current reader roles, native cancellation
and Dump/Reset/Restore. The native provider has no real WAL sender: the view is
empty, never a fictitious zero-progress or all-NULL worker. Neither inactive
slots nor receivers/configuration imply a sender; physical replication is not
implemented. There is no portable unordered row sequence.

PostgreSQL's two independent gates must not be conflated: `pid`, `usesysid`,
`usename`, `application_name` and `backend_xmin` are unredacted general fields,
subject to genuine absence. `client_addr`, `client_hostname`, `client_port`
and `backend_start` require superuser, effective `pg_read_all_stats` privileges
**or privileges of the sender's authenticated role**. All eleven fields
`state` through `reply_time` require **only superuser or effective
`pg_read_all_stats` privileges**, remaining NULL even for sender-role readers.
Unavailable IP/hostname, xmin, LSN, lag and reply time remain NULL rather than
fabricated values; Unix sockets have NULL address and port -1. Invalid LSNs
are not `0/0`, unavailable lag is not zero, and idle measured lag may briefly
persist before NULL. Native empty role results prove access/absence, **not
positive-row masking**; no genuine native sender exists for that evidence.

WAL receiver monitoring uses `RUN='^TestReferencePGStatWALReceiver'` with
`make check` and `make check REFERENCE=1`. The family covers the exact fifteen
nullable descriptors, symbolic view display, direct/fresh/retained prepared
emptiness, PUBLIC readability under current ordinary/inherited-stats roles,
read-only diagnostics, ordinary workload invariance and native cancellation/
Dump/Reset/Restore. There is no actual native physical receiver and no own-role
exception to the released all-stats-only detail visibility rule. Empty role
results are absence/readability evidence, not positive masking proof.

The existing `RUN='^TestReferenceServerProtocol$'` also runs in both modes:
native `replication=database` startup remains FATAL/0A000 with
`replication connections are not supported`, while a separate ordinary
observer sees both sender and receiver count0 before/after. Genuine PostgreSQL
superuser replication startup remains successful and its connection is closed
as before.

The full 22-column monitoring view uses
`RUN='^TestReferencePGStatActivity(Metadata|Lifecycle|Privileges|RoleIdentity|WaitsAndSignals|Snapshots|IdentityAndTracking|ReadOnly|FeatureSmoke)$'`
with `make check`, then the same selector with `REFERENCE=1`. Expectations cover
all nullable metadata, real state/transaction/application/source lifecycles,
the entire inherited current-role redaction mask (XID/xmin stay public), canonical
identity/horizons, exact read-only diagnostics and actual lock/advisory/sleep
cancellation/termination. Cached activity cuts retain membership/state but not
live waits. Native endpoints/absent leaders are NULL and no background rows or
portable unordered row sequence is expected. After joined cancellation the native
idle wait is NULL/NULL; the real TCP oracle reports Client/ClientRead instead of
the removed Lock/Timeout wait. Completed termination clears live waits in both.
`RoleIdentity` retains the same prepared cuts across connected-target rename,
physical retirement and role drop: OID stays public, current name changes then
becomes NULL, super/stats readers keep captured protected facts, unrelated roles
stay masked, and an unaffected PID remains readable without clearing any cut.
Disabled tracking likewise preserves actual waits: a positive disabled command
must report its genuine Lock/relation wait while query/timestamps/ID remain
hidden. Native driver transaction controls preserve prior SQL; lib/pq's actual
default BeginTx source is exactly `BEGIN READ WRITE`. Driver Tx rollback is
registered immediately so assertion failures cannot retain the borrowed Conn.
Both Commit and Rollback also run actual table-dependent SQL through `*sql.Tx`;
native terminal controls retain that last exact query/start/signed query ID.
Disabled and ordinary SQL cancellation independently prove queue count1→0,
empty blocking owners and the holder's still-live grant before release. The
disabled successful grant/completion case remains separate.
Grant/revoke witnesses independently check the committed inherited edge in
pg_auth_members and the observer's current_user/session_user, read-committed
isolation and pg_has_role USAGE. A direct, zero-parameter pg_roles query receives
PostgreSQL shared catalog invalidations before the held SELECT checks its mask.
The observer transaction, cached activity cut and prepared handle remain the same:
no stats clear, transaction end, reprepare or retry hides stale masked rows.
This uses the released relation_openrv_extended invalidation boundary, not a
production authorization-cache workaround or extra synchronization tables.

`TestMain` uses the existing `fixture.WithPgStatStatements()` option: the same
pinned PostgreSQL18.6 oracle genuinely preloads pg_stat_statements with
compute_query_id=on. SQL objects are installed explicitly by the identity
scenario. Its uncomputed witness turns compute_query_id off on that PG target,
or the existing startup-active native module's track policy to none, then
executes a subsequent known query. Originals, startup defaults and the fixture
image are preserved.
Its positive ID lookup selects a unique real relation/query shape by prescribed
normalized SQL and independently resolved database/user identities, never by the
activity ID under test. A distinct same-owner historical shape is a nonzero,
different-ID distractor; no global reset or empty-history assumption is used.
Recheck affected existing definitions with
`RUN='^TestReference(StatisticsSettingsUnsupportedCollectors|SessionActivitySessionsAndTimes|SessionActivityNativeControls|StatsSnapshotLifecycle|StatsSnapshotModes|StatsSnapshotInvalidation)$'`
in both modes.

Durable quiet feature smoke:
`make check RUN='^TestReferencePGStatActivityFeatureSmoke$'` and
`make check REFERENCE=1 RUN='^TestReferencePGStatActivityFeatureSmoke$'`.
Expected observations: three real sessions, exact metadata, idle→active
Lock/relation with actual blocker→idle in transaction→aborted, unrelated-role
literal redaction, cancel removing the live wait, termination removing fresh
membership, and a complete 55000 mutation diagnostic. All async operations join.
Apply an external 900-second deadline to each selected command; success is quiet
ordinary package summaries, never a skipped oracle. Internal ownership selectors
are `RUN='^TestPGStatActivity(DescriptorAndMask|Publication|XIDAuthority|ConcurrentCapture)$'`;
selected native race proof uses `RUN='^TestPGStatActivity(XIDAuthority|ConcurrentCapture)$'`.
XIDAuthority consumes actual typed high-epoch XID/xmin rows, actual accepted and
rejected safe candidates (including cancellation/epoch cleanup), and distinct
nested child/outer horizon restoration on success/error unwind. Publication adds
strict present/missing/foreign principal authority. ConcurrentCapture races real
view reads with reset/cancel, actual successor wait callbacks and drained physical
retirement/new identity, while preserving a detached old cut. Public waits,
snapshots and identity/tracking scenarios also use their selected `RACE=1` modes.
The migrated internal activity-registration/routing units additionally use
`RUN='^(TestStatsProvider(Collection|ResetShared|TargetedReset)|TestStatsResetSharedBackendLifetime)$'`.

Transport monitoring uses `RUN='^TestReferencePGStatSSL'` in both fixture modes.
Shape, Lifecycle, Privileges, ReadOnly, Snapshots and FeatureSmoke assert eight
declared nullable types, real PID membership, truthful false/sixNULL transport,
unauthorized peer omission and current inherited authority over immutable login.
Held prepared queries retain their explicit transaction cut across SET/RESET ROLE:
self visibility is present/absent/present when switching away from login authority;
SET-only, noninherited pg_read_all_stats access is absent/present/absent. Empty
results retain the exact eight-column metadata.
Both activity/SSL first-consumer orders share a canonical transaction cut across
all consistency modes, clear/end and physical retirement/new attachment; SSL
alone leaves cumulative timestamps untouched. Unread public Rows is held across
peer lifetime changes, then consumed before more SQL on its streaming observer.
Internal `RUN='^TestPGStatSSL'` covers descriptor/provenance, SSL-first high-epoch
cross-database XID/xmin, unchanged target visibility entries/allocation and snapshot
frontiers after monitoring, and detached backing; add `RACE=1` for shared/concurrent
capture. SQL without ORDER BY never promises physical traversal order.


Pending relation block signatures, complete diagnostics, actual source/index
reads, tracking/cache independence, publication lifetime and mutable EXECUTE
admission use `RUN='^TestReferenceStatsXactBlocks'` in both fixture modes.
Physical counts use independent monotonic/subset properties and exact native
logical-request deltas; rows, metadata, privileges and lifetime remain exact.
Literal/custom-bound NULL erasure is distinguished from surviving column-NULL
admission. Native Reset/Restore are explicit control phases.
Chosen-index witnesses retain the existing implicit-only access eligibility and
project pending counters in the reached SELECT before ordinary publication;
they do not depend on transaction-throttle timing or expand the optimizer.
The block smoke retains its physical client after COMMIT and observes ordinary
publication before expecting pending zero in the successor transaction. Session
and database events share the same writer/throttle; transaction completion alone
does not promise an immediate flush or erase an unflushed relation prefix.

Replication statistics reset scalar metadata, diagnostics, mutable EXECUTE,
cross-database subscription identities and native ACL controls use
`RUN='^TestReferenceStatsResetReplication'` in both fixture modes.

Shared/SLRU/backend reset signatures, strict versus nonstrict NULL, exact
diagnostics, live EXECUTE and native ACL controls use
`RUN='^TestReferenceStatsResetShared'` in both fixture modes. Positive provider
effects and retained reader/pending ownership are composed internal witnesses;
these SQL scenarios do not manufacture physical work or use neighboring getters.

Database/relation/function reset signatures, effects, exact diagnostics, mutable
EXECUTE and cache/transaction/native-control lifetimes use
`RUN='^TestReferenceStatsResetDatabase'` in both fixture modes. Public scenarios
publish real table/index/database work through ordinary owners. Genuine PL
function effects and qualified shared-provider partitions without neighboring
SQL getters use composed core witnesses, alongside private pending/ledger/frame,
accepted-fold/backing and generation/retirement assertions.
Reset timestamp assertions preserve NULL validity and compare exact instants,
not Go location-pointer representation. Reporting owners retire exactly once;
the shared reporting helper owns failure cleanup.
`ParameterPrivileges` retains each unary driver's statement across explicit
custom/generic modes and grant/revoke, asserting full execute-stage diagnostics
and unchanged positive counters/timestamps for noninvoking demands.
`Maintenance` proves database-wide clearing from genuine positive tuple,
scan and vacuum/analyze history, preserving physical rows and optimizer data
before a subsequent real ANALYZE. Reset leaves physical maintenance metadata
untouched, so the later scan can repopulate estimates without resurrecting old
tuple counters. Its reset observation holds the observer's own new deltas pending
in a transaction, not an assumed throttle interval.

Recovery-prefetch monitoring uses `RUN='^TestReferencePGStatRecoveryPrefetch'`
with `make check` in native and `REFERENCE=1` modes.

SLRU monitoring uses `RUN='^TestReferencePGStatSLRU'` in native and `REFERENCE=1`
modes: Shape, Lifecycle, Privileges, ReadOnly, Controls and FeatureSmoke cover the
eight-category/nine-column contract, typed empty/direct/prepared results, genuine
reset bounds, none/cache/snapshot retention and clear, live view/column/getter ACLs,
exact mutation diagnostics and retained native replacement rows/session authority.
Native tests assert stable zero estimates across ordinary SQL; live PostgreSQL
uses inactive other for stability/reset observations, never assumes active
physical categories remain zero. Internal one-hot provider/projection tests prove
all seven positive positions and composed capture proves cross-family snapshots.
The native Controls/FeatureSmoke phase also round-trips the backing getter ACL:
implicit NULL needs no dump repair, while restricted user-role grants (including
grant options), explicit-empty and explicit-effective-default ACLs retain their
independently expected raw values. Direct and previously held ordinary-role view
reads must honor the restored deny/grant boundary after each actual Restore.
These controls use the public Dump/Restore APIs, not PostgreSQL SQL substitutes.


Setting-flag metadata and option-array expansion use
`RUN='^TestReferenceSettingsOptions'` in both modes. Shared physical-session and
held-call helpers witness noncreating live placeholder recognition, independent
cursor reexecution, complete diagnostics, typed empty-result metadata and routine
EXECUTE admission; unordered option rows retain duplicate multiplicities.
Stored scalar and fixed-OUT source views use the same family: independent
direct/prepared rows and view-column metadata are checked before and after
same-engine and fresh-engine native Dump/Reset/Restore. PostgreSQL also executes
the actual emitted native SQL definitions; empty views retain their OUT schema.

Connection endpoint values, typed metadata, diagnostics, EXECUTE authority and
stored-view native controls/replay use `RUN='^TestReferenceSessionEndpoints'`
in both fixture modes. Native calls yield typed NULLs; the TCP oracle checks
actual valid endpoints and stability on a held physical connection.

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
- **Adaptive timing.** Timing-sensitive witnesses use adaptive budgets (200 ms
  doubling up to 6400 ms; idle resets 250 ms up to 8 s; all within the scenario
  budget) or fixed budgets with at most
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
- Unquoted fractional SET values: `RUN='^TestReferenceSettingsFractional'`
- Planner constants: `RUN='^TestReferencePlannerCosts'` covers all thirteen
  independent descriptor rows, numeric diagnostics, authority and transactional
  lifetimes, supported access-method choices, local-worker cost profiles and
  native-control retention. Private owner tests distinguish actual candidate
  traversal and worker dispatch from their public metadata/result witnesses.
- Other planner options: `RUN='^TestReferencePlannerOther'` covers all eight
  USERSET surfaces, exact validation and transactional lifetimes, real ANALYZE
  retention, constraint-proof eligibility, cursor results and logical recursive
  estimates. Native access/proof witnesses are distinct from PostgreSQL plans;
  collapse/reorder, JIT and generic/custom machinery are named absent boundaries.
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
- Short-option counterparts: `RUN='^TestReferenceShortSettings'` covers the
  independently specified 25-name metadata/read inventory, typed validation,
  context and privilege filtering, statistics cross-setting checks and cached
  reset semantics, transactions, physical-session reuse and logical native controls.
  Run bounded top-level definitions in both native and `REFERENCE=1` modes.
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
  the explicit setting-only safety boundary for external mechanisms, not a claim
  that session_replication_role is inert. Run both native and REFERENCE=1.
  Released FTS inventory validation includes Swedish configuration spellings,
  direct/prepared SET and set_config, and rejected names preserving the last
  accepted configuration.
  SimulationSmoke is the new-feature smoke (8MB SHOW versus raw8192, XML alias,
  native FK/routine safety and unchanged logical rows), not a physical-effect
  claim. Native initialization is UTF8/C/UTC/simple FTS; the pinned 18.6
  initialized oracle has Etc/UTC and English FTS. pq initializes both encoding
  and DateStyle with source client and NULL file provenance; other configured
  defaults retain authorized config-file provenance. Session overrides expose
  NULL file fields; RESET restores the actual captured client/config source;
  unauthorized readers never see file fields. Ordinary-login scenarios use
  the fixture's role-core-fixture password convention.
- Session replication role: run `make check RUN='^TestReferenceSessionReplicationRoleAcceptance$'`
  and `make check RUN='^TestReferenceSessionReplicationRoleFeatureSmoke$'`, then
  repeat both with `REFERENCE=1` (the pinned PostgreSQL 18.6 oracle).
  The focused direct/retained-prepared fixture independently expects ordinary
  BEFORE INSERT trigger results 105/5/105 for origin/replica/local, exact
  SHOW/current_setting and all 17 pg_settings fields, physical-session isolation,
  SET rollback/commit, SET LOCAL commit restoration and savepoint rollback with
  exact retained/removed rows. local has origin's logical behavior but keeps its
  distinct displayed value. FeatureSmoke also proves ordinary FK checks are
  suppressed under replica: an accepted orphan remains after returning to origin,
  while the next invalid insert fails with the exact 23503 diagnostic.
  Selected `TestReferenceClientSettings(Inventory|Validation|Authority|Lifecycle)`
  subtest `session_replication_role` supplies canonical enum validation, exact
  22023/42501 English diagnostics (authority before validation), ordinary metadata
  visibility, superuser/parameter SET grants and revocation. Run those four
  bounded top-level tests in both modes, along with
  `TestReferenceTriggerMode(Effects|Prepared|ForeignKeys)`. Avoid slash selectors
  here: they select every reference package and unnecessarily start unrelated
  oracle containers.
  These are real logical trigger/FK effects through the existing provider, not
  replication transport, WAL application or replication workers; those physical
  mechanisms remain outside this setting's embedded SQL contract.
- Default table access method: `RUN='^TestReferenceDefaultTableAccessMethod'`
  owns the focused Values/Lifecycle/Validation/Controls family. Its local
  `defaultAMCases` installs a genuine heap-handler method with a distinct catalog
  identity; shared witnesses assert exact relation identity, real rows and full
  setting metadata. Direct/prepared and held execution cover creation defaults,
  USERSET authority, transaction/savepoint restoration, physical-session reuse
  and recorded native Reset/Dump/Restore destinations. The broad client inventory
  remains the registry-wide baseline.
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

- Database monitoring: untagged `stat_database_*_test.go` definitions use actual
  other-database observers, physical session retirement and bounded ordinary
  publication waits. Setup DDL can contribute PostgreSQL system-catalog tuples;
  exact user-DML deltas start only after full setup publication, never assume a
  database's setup total equals the user marker. TRUNCATE/DROP retain historical
  work without assuming catalog U/D zero. Cumulative cache/snapshot/none cases
  include B and C and a first read through existing table statistics.
  `TestReferenceStatDatabaseSnapshotFirstAccess` adds fresh independent index,
  archiver, bgwriter, SLRU and IO vectors, each projecting a cumulative field from an
  actual local row before any held B/C read. Exact published user-insert deltas +2/+3
  establish distinct positive peers; +4/+5 remain hidden until clear, and +6/+7
  remain hidden until transaction end. A separate none-mode observer proves
  publication first; clear/end only discard read cuts. Timestamp inspection stays
  lazy, captures retain their timestamp across peer publication, and clear/end
  release it. Native backend admission/removal remains live despite retained
  cumulative facts.
  PostgreSQL's transaction-local backend-status snapshot instead retains the
  old count in all three cumulative fetch modes; a separate outside-transaction
  observer proves actual live admission and later publication without clearing
  or weakening the held cumulative-cache assertions.
  Idle SQL COMMIT/ROLLBACK and parsed empty simple queries have real backend
  outcomes; idle physical cleanup does not. PREPARE transfers unresolved work
  without reporting at preparing exit. Successful completion reports the original
  prepared outcome once as well as the finisher's own commit: COMMIT PREPARED
  C2/R0, ROLLBACK PREPARED C1/R1, as the released
  [`FinishPreparedTransaction`](https://github.com/postgres/postgres/blob/REL_18_6/src/backend/access/transam/twophase.c#L1654-L1655)
  callback specifies. Independent mixed commit/abort, truncate commit/abort and
  prepared-finisher vectors each own a fresh complete scenario (both modes),
  rather than exhausting a shared context across legitimate idle retries.
  No assertion or shared fixture deadline is reduced/extended.
  Parse/Describe plus Sync can itself
  commit without Execute. PostgreSQL's
  [`InitPostgres`](https://github.com/postgres/postgres/blob/REL_18_6/src/backend/utils/init/postinit.c)
  also commits a genuine startup catalog transaction; native public admission
  has no such transaction owner and must not manufacture one.
  Before retiring the setup writer, PostgreSQL scenarios explicitly ANALYZE
  `pg_catalog.pg_database`, assert its manual analyze count advances and its
  modification-since-analyze counter is zero, and await no target autovacuum
  worker. Full-column
  [`pgstat_report_analyze`](https://github.com/postgres/postgres/blob/REL_18_6/src/backend/utils/activity/pgstat_relation.c#L279-L356)
  resets shared analyze eligibility; worker exit drains predecessor statistics.
  This places real catalog maintenance before the captured setup cut, without
  accepting or subtracting extra updates later. Native virtual catalog sources
  have no corresponding background maintenance; no pretend ANALYZE is issued.
  Measured admission/outcome windows perform no database-inventory DDL.
  Every exact SQL-outcome scenario establishes its baseline on the measured
  live writer using one committed marker INSERT in the separate marker table,
  awaiting normal publication (including the existing idle retry). Its known
  marker, startup and earlier named preparation commits independently specify a
  minimum published commit total. The observed baseline may include additional
  PostgreSQL catalog/startup commits; marker tuples, session/live counts and every
  other baseline field remain exact. Subsequent SQL outcomes are exact deltas
  from that observed baseline, never relaxed commit minima. No forced flush,
  reset or observer-clear substitutes for publication.
  The smoke prepares one reusable parameterized INSERT on that same writer
  before the marker baseline; its pre-admission lifecycle baseline stays separate.
  Thus startup/preparation/marker work cannot contaminate commit+1/rollback+1/
  insert+2 or hide the one new physical session. Empty SQL asserts successful
  execution without demanding an affected-row count absent from pq's response.
  DML read witnesses reject every UPDATE/DELETE/USING/FROM/MERGE candidate yet
  require genuine target input. Native exact scan counts follow its actual
  scanner (including MERGE rescans); the independent PostgreSQL plan must visit
  the live target, without asserting planner identity or charging affected rows.
  View-definition witnesses independently require the released expression in
  default/pretty forms and identifier quoting/search-path masking, without
  installing SQL getter routines. The definition parameter is explicitly boolean
  to select the pretty-print overload rather than the integer wrap-column overload.
  The shared-row witness uses existing `OidReferenceRows` family normalization:
  native int64 and pq decimal-byte OIDs independently normalize to literal zero,
  while preserving the actual OID declaration and raw adapter contracts.
  Native parallel witnesses use actual projection and supported partial
  `string_agg` execution, not an unsupported count(*) parallel recipe. The
  PostgreSQL plan independently supplies its own actual planned/launched totals.
  Deadlock admission uses the existing managed `LockOperation` worker, including
  its strict scheduler barrier and cancellation/join cleanup.

  ```sh
  make check RUN='^TestReferenceStatDatabase(Schema|SharedBackendWitness|Inventory|Privileges|ReadOnly|Technical|Definition)$'
  make check RUN='^TestReferenceStatDatabase(Transactions|Tuples|CommandOutcomes|PreparedOutcomes|DMLReads|CacheLive|SnapshotFirstAccess)$'
  make check RUN='^TestReferenceStatDatabase(Sessions|Deadlocks|Parallel)$'
  ```

  Run each selected definition again with `REFERENCE=1` against the unchanged
  pinned PostgreSQL18.6 fixture. These are independent expected outcomes, not
  backend-only shadow tests. Select `SnapshotFirstAccess` with `RACE=1` in each
  mode as well. The distinct feature smoke has its own argv and
  expected observations:

  ```sh
  make check RUN='^TestReferenceStatDatabaseFeatureSmoke$'
  make check REFERENCE=1 RUN='^TestReferenceStatDatabaseFeatureSmoke$'
  ```

  Each serial direct/prepared-DML smoke owns a fresh fixture/context and isolated
  B/C. It proves B live+1, peer commit/rollback and both executed insert attempts
  after physical retire, no abnormal disconnect count, initially unchanged C,
  final B rows `[11]`, and the exact thirty-column schema. A's snapshot begins
  through existing table statistics, retains both databases across a published C
  insert and B admission/retirement, and independently distinguishes native live
  backends from PostgreSQL's cached backend status. A separate none-mode reader
  proves publication before clear exposes the new B/C facts. Both routes also
  assert the literal shared row, setting-aware checksum NULLs and full 55000
  read-only UPDATE diagnostic. Clear-snapshot is never used as a writer flush.

