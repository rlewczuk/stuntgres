# reference/sessions

Dual-mode scenarios for public driver/session APIs, SQL prepared statements,
notifications, session identity and real wire-server behavior.
Settings/timeouts/batches belong to [session_state](../session_state/README.md#testing);
SQL cursors to [cursors](../cursors/README.md#testing); transaction controls to
[locking_transactions](../locking_transactions/README.md#testing).
Wire framing belongs to [pgserver](../../pgserver/README.md#testing), and CLI/process
mechanics to [cmd/stuntgres](../../cmd/stuntgres/README.md#testing).

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
  Signaling helpers retain complete FATAL diagnostics through public pgconn,
  independently of pq transport-retirement sentinels. Termination routes own
  disposable physical connections; prepare held commands before taking blockers.
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
- Keep Parse/statement Describe, Bind/portal Describe and Execute observations
  separate. Utility analysis stays quiet/effect-free; real Execute establishes
  publication, command tags, readiness and atomicity.
  A simple Query destroys PostgreSQL's unnamed statement; unnamed Bind/Execute
  must share the executing Sync group. Named statements survive idle Sync,
  portals do not. Server-backed Go transactions use actual held handles.
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
- Drain and close a PostgreSQL cursor before issuing another command on its
  physical connection. Native controls use `p.Native`/`p.NativeConnector`, never
  the middleware-wrapped pool.
- `TestReferenceFixtures`, `TestReferenceIdentifierKeywordInventory`,
  `TestReferenceNotices(Payload|Severity|Permission)` and
  `TestReferenceSessionIdentity(Trust|SessionAuthorityAndMembership)` carry the
  legacy `reference` build tag and run only with `REFERENCE=1`; a native no-match
  run is not evidence for them.

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- Driver basics: `RUN='^TestReference(DirectAndPrepared|RowsMetadata|TypedValues|CancellationTransactions|NativeCursor|Driver(Canceled|Rows)|DumpRoundTrip)'`
- Session lifecycle: `RUN='^TestReferenceSession'`
- Notices: `RUN='^TestReferenceNotices'`
- Identifiers: `RUN='^TestReferenceIdentifier'`
- Namespace/search path: `RUN='^TestReference(SearchPath|NamespaceIdentity)'`
- Prepared rebind: `RUN='^TestReferencePrepared(Rebind|Fold|QueryRevalidates)'`
- SQL prepared commands: `RUN='^TestReferenceSQLPrepared'`
- Server protocol: `RUN='^TestReferenceServer'`
- Embedded server: `RUN='^TestReferenceEmbeddedServer'`
- Dual connector: `RUN='^TestReferenceDualConnector'`

Protocol statistics witnesses observe actual frontend/session retirement, not
synthetic end requests. For normal Terminate, keep transport open until server
EOF before retiring the client handle; a client FIN race does not prove decoding.
