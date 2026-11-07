# reference/sessions

Public driver and session behavior: direct/prepared parameters, rows and metadata,
cancellation and closed objects, physical sessions, notices, identifiers, session
identity, `search_path` and namespace identity, version and Unicode inquiries,
LISTEN/NOTIFY session messages, SQL backend cancellation and termination,
`pg_locks`, `pg_blocking_pids`, transaction status and commit timestamps with
tracking off, primary-mode recovery inquiries and typed physical-registry and
WAL-summary source admission, prepared-statement rebind, DISCARD,
SQL PREPARE/EXECUTE, the `stuntgres/dual` mirroring connector, and the
wire-protocol server (`server_*_test.go`) against a real `cmd/stuntgres`
subprocess, plus the public in-process `pgserver` API
(`embedded_server_*_test.go`). Related families live elsewhere: settings,
timeouts, the physical-session adapter and direct-message batches in
[session_state](../session_state/README.md); SQL cursors and `pg_cursors` in
[cursors](../cursors/README.md); transaction control, commit timestamps with
tracking on and PREPARE TRANSACTION restrictions in
[locking_transactions](../locking_transactions/README.md); server CLI, framing
and process mechanics in [cmd/stuntgres](../../cmd/stuntgres/README.md#testing).

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md).

- `TestMain` is `fixture.Main(m)`: commit-timestamp tracking stays off in this
  package.
- Helpers: `blocking_pids_helpers_test.go`, `discard_helpers_test.go`,
  `namespace_identity_test.go` (`namespaceConn`), `prepared_rebind_helpers_test.go`,
  `session_messages_helpers_test.go`, `sql_prepared_helpers_test.go`,
  `view_locks_helpers_test.go`, `dual_connector_helpers_test.go`
  (`newDualPair`: a dual pool over the scenario's native connector and one
  reference per scenario, a second native engine in quick mode and lib/pq with
  `REFERENCE=1`) and `dual_connector_fault_test.go` (`dualFault`, which wraps
  that reference so fault scenarios run in both modes); shared raw wire client
  in `embedded_server_wire_helpers_test.go`, packet/row helpers in
  `embedded_server_extended_helpers_test.go` and JSONL golden/dump helpers in
  `embedded_server_log_helpers_test.go`.
  Session signaling family: `RUN='^TestReferenceSessionSignal'`.
  Self-signaling Query witnesses use `sessionSignalSelfQuery` to consume all rows
  and the terminal outcome. Native eager diagnostics and pq streaming/retirement
  errors have different adapter phases. Self-termination also uses
  `sessionSignalSelfFatalProtocol` on the existing pinned oracle to retain exact
  FATAL fields through public pgconn; transport errors never substitute for that
  proof. Each termination route owns a disposable physical connection.
  Active termination repeats the real uncommitted-write/relation-wait witness
  with pgconn to retain its full FATAL independently of pq's Exec retirement
  sentinel. Cancellation prepares held statements outside explicit transactions,
  before acquiring the competing lock, so admission observes Execute rather than
  blocked metadata capture. Termination privilege targets use dedicated
  authenticated pools that cannot recycle an unobserved dead idle socket.
- **Server tests.** `fixture.NewServerFixture(t)` builds and launches a real
  `cmd/stuntgres` subprocess ([fixture README](../fixture/README.md#helpers)).
  Client witnesses run real `psql -X -w -At` with a private `PGPASSFILE`; a
  missing `psql` fails the test. With `RACE=1` the fixture also builds the server
  with `-race`; it always uses the test-only `stuntgres_reduced_kdf` tag (add the
  `productionkdf` test build tag to exercise the production PBKDF2 cost). The
  `server_*_test.go` files carry `!noservertests`, which only `make race` sets.
- **Embedded wire tests.** `fixture.NewEmbeddedServerFixture` starts public
  `pgserver` handles with explicit accounts; no CLI transport is substituted.
  Native pgx/pgxpool/pgconn use its pipe DialFunc, with separate real TCP
  pgx.Connect/lib/pq routes; oracle clients always use actual TCP. Keep client
  cleanup before fixture cleanup, cancel and join command workers on failure,
  and observe `pg_stat_activity`/`pg_locks` admission before cancellation or
  shutdown. Native-only lifecycle phases remain explicit within dual-mode
  scenarios; the subprocess families below retain their existing behavior.
- Capture daemon `start` output in ordinary files, not pipe-backed
  `CombinedOutput`: the daemon inherits stdout/stderr and keeps the pipes open.
- Wire codec scenarios use explicit Parse OID hints with `SELECT $1`, so codec
  coverage does not depend on SQL CAST syntax. Obtain catalog-owned type OIDs
  through `regtype`; never copy them into byte goldens.
- Change the effective `search_path` in wire scenarios with named extended SET
  traffic: a simple-Query SET destroys PostgreSQL's unnamed statement.
- Transactional COMMENT uses the raw named-statement Parse/Describe/Bind/Execute
  helpers for every shipped kind, not adapter Prepare as a Describe substitute.
  Assert quiet analysis, exact COMMENT completion and Ready states, private
  owner/committed peer text, savepoint recovery, held reuse and simple-message
  rollback. Shared metadata/lifecycle coverage lives in
  [catalog_objects](../catalog_objects/README.md#testing).
- Transactional view definitions use actual simple-query messages and named
  Parse/Describe/Bind/Execute traffic, plus server-backed Go BeginTx. Ordinary
  utility preparation stays quiet; materialized creation retains source
  analysis. Check command counts, Ready states, private peer invisibility,
  held-reader freshness and rollback/savepoint recovery through the same server.
- Transactional domain/composite/type utilities reuse those raw helpers for
  simple messages and held named Parse/Describe(S), Bind/Describe(P)/Execute.
  Assert effect-free preparation, utility NoData/tags, private owner effects,
  peer invisibility, bound consumers and error/savepoint/readiness recovery.
- Collation/conversion/cast utilities reuse the raw wire helpers and
  `transactionalTypesCommandTag`. Separate Parse/statement Describe and
  Bind/portal Describe from Execute to assert quiet phases, exact utility tags,
  private real consumers, peer invisibility and savepoint/error recovery.
  Named statements survive idle Sync; portals do not, so autocommit execution
  binds in its executing cycle. Server-backed Go BeginTx uses held handles.
- Transactional rewrite rules reuse the named wire and command-tag helpers.
  Parse/Describe must be quiet and effect-free even for utilities whose target
  will be created later. Execute proves real action rows, held DML freshness,
  I/T/E readiness, message atomicity and healthy recovery; Go BeginTx witnesses
  run over the actual server transport.
- Procedure transaction-control wire scenarios distinguish preparation retention
  from preceding completed/suspended Execute, single/multi-Q crossover and Sync.
  Reuse the named builders, complete error fields and independent peer visibility.
  Family selector: `RUN='^TestReferenceServerPLpgSQLTxControl'`.
- Dictionary constant wire scenarios warm a named definition before option ALTER,
  then repeat Bind on that definition while fresh definitions and runtime tokens
  observe current options. Check TEXT[] RowDescription, raw NULL versus empty
  arrays, rejected-ALTER atomicity and idle readiness through the same helpers.
- Keep the comparison wire matrix pipelined: one server fixture, a fresh session
  per left source, and each pair's cells sent as one pipeline of Sync-terminated
  groups whose responses are checked in order.
- Notice-backpressure scenarios physically close setup-only sockets and allocate
  independent authenticated admin/victim/probe sockets per simple/extended
  subtest. Each receives one absolute 20s deadline before login, without renewal.
  Preserve the physical-close barriers and bounded backpressure/rollback proof;
  never extend a reused socket's lifetime to accommodate sibling scenarios.
- Native wire cancellation keys are not `pg_locks` PIDs; identify native waiters
  by relation and mode, or by a captured XID.
- Server fixtures that mutate builtin ACLs restart the native child with dumps
  disabled before SQL setup, because builtin ACL changes are not dump input.
- **Physical identity.** Lock and blocking observers use `fixture.LockPID` on
  PostgreSQL; natively they read the `pg_temp_N` suffix of the session's TEMP
  namespace (`blockingConn`), initialized before BEGIN and never inside a
  transaction that will PREPARE. A physical startup blocked before initialization
  has neither callable backend identity nor TEMP state: discover its positive PID
  through the exact shared database RowExclusive wait in `pg_locks`, independently
  of the blocking function under test. Observe it from another database and
  retain the real transactional rename holder until inquiry assertions finish.
  Connection workers must release that holder before cleanup joins, because
  cancellation alone need not interrupt authentication. Generated OIDs and PIDs
  are discovered from catalogs or observers, never pinned.
- Session-message receivers use `stuntgres.WaitForNotification` natively and pgx
  `WaitForNotification` inside one bounded `sql.Conn.Raw` callback on PostgreSQL;
  the raw connection never escapes the callback
  ([driver contract](../../docs/DRIVER.md#notices-and-notifications)).
  Stock tcn scenarios share these receivers and prove discarded events with a
  later committed marker, rather than a timeout-based absence probe. Restored
  provider/trigger bindings fire against an independent native engine.
- Notification-info inquiries pin middleware-wrapped `backend.DB` connections
  through `notificationInfoConn`, including cross-database and independently
  restored pools, so SQL result assertions retain fixture recording. Only native
  controls use the separate raw pool; these scenarios do not receive notifications.
- Drain and close a PostgreSQL cursor before issuing another command on its
  physical connection. Native controls use `p.Native`/`p.NativeConnector`, never
  the middleware-wrapped pool.
  Control-script atomicity distinguishes enabled CREATE result diagnostics from
  reached SQL-body invocation errors. Deferred definitions are forced through
  actual INSERT/default evaluation before asserting rejected replacement.
  Written scalar calls outside Restore admission remain ordinary errors without
  SQLSTATE; they are not substitutes for reached default-execution failures.
- Capture-lifetime changes must not repin the composite-shape and
  enum-rename/native-control expectations that prepared-rebind scenarios share.
  The rebind contract is in the [engine README](../../internal/engine/README.md).
- Backup-control metadata distinguishes stored view columns from computed
  results: native views report known nullable columns, while computed results
  and pq columns report unknown nullability. Native-control fixtures retain raw
  NULL and raw empty ACL replay; stock explicit-empty ACLs use ordinary REVOKE
  or the one-dimensional `acldefault('c',0)` construction, not a raw `'{}'`
  cast. Cleanup repairs malformed stock ACL dimensions before ordinary REVOKE,
  preserving valid grants until dependency-aware revocation completes.
  Backup diagnostics and feature smoke also demand `22012` before scalar NULL
  for `abs(1/$n::integer)` with a zero denominator, and full PG_LSN NULL metadata
  for denominator2. Both literal and parameter NULL labels use direct, actual
  prepared and held routes, plus one retained statement across `0 → 2 → 0`;
  no generic-plan setting or prepare-phase error replaces execution evidence.
- `TestReferenceFixtures`, `TestReferenceIdentifierKeywordInventory`,
  `TestReferenceNotices(Payload|Severity|Permission)` and
  `TestReferenceSessionIdentity(Trust|SessionAuthorityAndMembership)` carry the
  legacy `reference` build tag and run only with `REFERENCE=1`; a native no-match
  run is not evidence for them.

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- Qualified columns across held and wire transports: `RUN='^TestReferenceThreePart'`

- Driver basics: parameters, rows, metadata, cancellation, Dump round trip: `RUN='^TestReference(DirectAndPrepared|RowsMetadata|TypedValues|CancellationTransactions|NativeCursor|Driver(Canceled|Rows)|DumpRoundTrip|DatabaseIdentity|ConcurrentInserts|ReferencesNulls)'`
- Session lifecycle and physical isolation: `RUN='^TestReferenceSession(DiagnosticsAndPreparedLiveValue|PhysicalIsolationReuseAndReplacement)?$'`
- Notices: `RUN='^TestReferenceNotices'`
- Identifiers and keywords: `RUN='^TestReferenceIdentifier'`
- Session identity (roles, session authorization): `RUN='^TestReferenceSessionIdentity'`
- `search_path` and qualified providers: `RUN='^TestReferenceSearchPath'`
- Schema and TEMP namespace identity: `RUN='^TestReferenceNamespaceIdentity'`
- Version and Unicode inquiries: `RUN='^TestReferenceVersionInformation'`
- LISTEN/NOTIFY session messages: `RUN='^TestReferenceSessionMessages'`
- Stock triggered change notifications: `RUN='^TestReferenceTcn'`
- Notification state inquiries: `RUN='^TestReferenceNotificationInfo'`
- Recovery inquiries and physical registry admission: `RUN='^TestReferenceRecoveryInfo'`
- Logical-message signatures, strictness, live ACLs and physical demand: `RUN='^TestReferenceReplicationMessages'`
- WAL summary signatures, metadata and physical demand: `RUN='^TestReferenceWALSummary'`
- Control-file signatures, metadata, diagnostics and physical demand: `RUN='^TestReferenceControlData'`
- `pg_locks`: `RUN='^TestReferenceViewLocks'`
- `pg_blocking_pids`: `RUN='^TestReferenceBlockingPids'`
- Transaction status and commit timestamps, tracking off: `RUN='^TestReference(TransactionIdentityStatusDefaultTracking|CommitTime(Disabled|SpecialIDs|Resolution))'`
- Native controls inside transactions: `RUN='^TestReferenceControlTransactions'`
- Prepared-statement revalidation and rebind: `RUN='^TestReferencePrepared(Rebind|Fold|QueryRevalidates)'`
- DISCARD: `RUN='^TestReferenceDiscard'`
- SQL PREPARE/EXECUTE/DEALLOCATE: `RUN='^TestReferenceSQLPrepared'`
- Wire server (all): `RUN='^TestReference(Server|TransactionalSchemaProtocol|TransactionalCommentProtocol|TransactionalViewsProtocol|TransactionalMaterializedRefreshProtocol|TransactionalTypesProtocol|TransactionalPoliciesProtocol|TransactionalRulesProtocol|TransactionalCollationConversionCastProtocol)'`
- Embedded clients and COPY: `RUN='^TestReferenceEmbeddedServer(Clients|Copy)$'`
- Embedded notifications and cancellation: `RUN='^TestReferenceEmbeddedServer(Notifications|Cancellation)$'`
- Embedded isolation, persistence and logs: `RUN='^TestReferenceEmbeddedServer(Isolation|Shutdown|Logging)$'`
  Run each selector with and without `REFERENCE=1`; lifecycle race coverage uses
  `RACE=1 RUN='^TestReferenceEmbeddedServer(Cancellation|Isolation|Shutdown)$'`
  in both modes.
- Wire extended protocol and portals: `RUN='^TestReferenceServer(Extended|Portal|Unnamed|FailedPortal|WrappedPortal)'`
- Wire transactional comments: `RUN='^TestReferenceTransactionalCommentProtocol$'`
- Wire transactional view/materialized definitions: `RUN='^TestReferenceTransactionalViewsProtocol$'`
- Wire transactional materialized refresh: `RUN='^TestReferenceTransactionalMaterializedRefreshProtocol$'`
- Wire transactional domain/composite/type lifecycle: `RUN='^TestReferenceTransactionalTypesProtocol$'`
- Wire transactional policy definitions: `RUN='^TestReferenceTransactionalPoliciesProtocol$'`
- Wire transactional rewrite-rule definitions: `RUN='^TestReferenceTransactionalRulesProtocol$'`
- Wire transactional collation/conversion/cast definitions: `RUN='^TestReferenceTransactionalCollationConversionCastProtocol$'`
- Wire COPY: `RUN='^TestReferenceServer(Copy|PreparedCopy)'`
- Legacy binary COPY wire/client matrix: `RUN='^TestReferenceServerCopyLegacyBinary(PGX|Prepared|Transactions|Atomicity)?$'`
- Wire comparison matrix: `RUN='^TestReferenceServerComparison'`
- Wire notifications: `RUN='^TestReferenceServerNotifications'`
- Wire logs (error, server and query logs): `RUN='^TestReferenceServer(Logs|PreparedLogs|QueryLog)$$'`
- Dual-backend query log: `RUN='^TestReferenceServerDualQueryLog$$'`
- Dual connector (`stuntgres/dual`): `RUN='^TestReferenceDualConnector'`

- `make check RUN='^TestReferenceStatDatabaseWire$'` and the same with
  `REFERENCE=1` exercise actual server/frontend lifetime, not synthetic session
  ending requests. The untagged scenario uses pq's public physical Dialer seam
  for normal Terminate, abrupt socket EOF, recognized invalid-message FATAL,
  native idle-session timeout and administrator termination. A separate observer
  attaches to the same managed secondary database: native managed databases have
  independent engines/catalogs, not one shared inventory. Each real client
  independently asserts `current_database() = 'server_second'` and the matching
  `pg_database.oid`/`pg_stat_database.datid` identity. The settled baseline includes
  the real observer, setup and native provisioning sessions; measured client
  session/category/live deltas remain exact. The scenario checks actual English
  FATAL diagnostics and terminal EOF without a second authentication/framing
  fixture or RequestEnd mock. Embedded database-view scenarios separately cover
  cross-database observers within one actual cluster.
  The normal-close witness sends the actual Terminate frame and keeps transport
  open until server EOF before retiring pq's handle. This proves decoded normal
  termination rather than racing a client FIN against frontend decoding.

