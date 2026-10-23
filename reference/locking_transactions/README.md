# reference/locking_transactions

Transaction control (BEGIN/COMMIT/ROLLBACK, CHAIN, Go `Tx` and raw-handle
interop, cancellation), savepoints, isolation levels and serializable conflicts,
transaction settings, transaction identity (`txid`/`xid`/MultiXact functions),
commit timestamps with tracking on, prepared transactions (including the
LISTEN/NOTIFY PREPARE restrictions), relation locks and deadlocks, row locks
(`FOR UPDATE`/`FOR SHARE`, including through views), `pgrowlocks`, session and
transaction advisory locks, and inheritance DML/ALTER lock contention. Related
families: `pg_locks`, `pg_blocking_pids`, default-off commit timestamps and
LISTEN/NOTIFY delivery in [sessions](../sessions/README.md); SQL cursors (WITH
HOLD) in [cursors](../cursors/README.md); timeouts in
[session_state](../session_state/README.md).

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md).

- `TestMain` is `fixture.Main(m, fixture.WithCommitTimestampTracking())`, the only
  package with commit timestamps on; native scenario engines inherit the mode.
- Helpers: `advisory_session_helpers_test.go`, `advisory_xact_helpers_test.go`,
  `pgrowlocks_helpers_test.go`, `query_lock_migration_helpers_test.go`,
  `serializable_helpers_test.go`, `transaction_identity_helpers_test.go`.
- **Waits.** Observe the actual server wait before releasing or canceling:
  `fixture.LockWaitBehind` and `fixture.LockPID`, or narrow `pg_locks` and
  `pg_blocking_pids` queries. Worker start, elapsed sleep or `SHOW` output never
  prove that a request is queued or that a safe-snapshot wait is pending.
  Native public deadlines prove conflict, not exact queue membership; exact
  native queue order is tested in `internal/engine`.
- **Workers and cleanup.** Every worker owns cancellation and a bounded join
  before handles, schemas, roles or GIDs are cleaned up. Advisory holds survive
  deadlock, timeout and rollback: unlock or close them before requiring a
  blocked survivor to finish.
- **Physical identity.** PostgreSQL participants use `pg_backend_pid()` /
  `fixture.LockPID`. Natively, read the `pg_temp_N` suffix of the session's TEMP
  namespace, initialized outside any transaction that may PREPARE.
- **Identities and clocks.** Assert XIDs and MultiXact IDs relationally (for
  example `mxid_age('1'::xid)` deltas), never as constants, `xmax` reads or XID
  ordering. Commit-timestamp witnesses bracket publication with each backend's
  server clock; never compare allocator values or wall-clock time. Prepared XIDs
  come from `pg_prepared_xacts.transaction`.
- **Prepared transactions.** Register bounded GID-specific `ROLLBACK PREPARED`
  cleanup before each prepare-capable operation, ahead of schema, pool and role
  cleanup; never clean unrelated GIDs. `sql.Conn.Close` only returns a borrower:
  close an isolated pool or the raw driver connection to witness physical origin
  end.
- **Pre-canceled lib/pq calls.** Pre-canceled `BeginTx` and rollback race lib/pq's
  context watcher, so their transport outcome is not an SQL expectation. Clean up
  any returned transaction, retire the canceled physical connection, and assert
  unchanged rows through an unaffected observer and a fresh borrower.
- **pgrowlocks.** Each scenario drops the extension before setup and after all
  handles and workers are cleaned up, on a separate administrative connection
  (resetting `public` leaves the extension registered). Owner XIDs/PIDs come
  from the owner's granted `transactionid` ExclusiveLock in `pg_locks`. Compare
  rows and aligned `(xid,mode,pid)` member triples as bags with multiplicity;
  never sort arrays independently, pin allocator numbers or read `ctid`.

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- All transaction families (control, Go `Tx`, raw handles, visibility): `RUN='^TestReferenceTransaction'`
- Transaction control and CHAIN: `RUN='^TestReferenceTransactionControls'`
- Isolation levels, read-only, conflicts: `RUN='^TestReferenceTransactionIsolation'`
- Transaction settings: `RUN='^TestReferenceTransactionSettings'`
- Transaction identity functions: `RUN='^TestReferenceTransactionIdentity'`
- Commit timestamps: `RUN='^TestReferenceCommitTime'`
- Savepoints: `RUN='^TestReferenceSavepoints'`
- Serializable isolation: `RUN='^TestReferenceSerializable'`
- Prepared transactions: `RUN='^TestReference(PreparedTransaction|SessionMessagesPreparedOriginReuse)'`
- Relation locks and deadlocks: `RUN='^TestReference(LockModes|RelationLock|RelationDeadlock|SmokeRelation)'`
- Row locks: `RUN='^TestReference(RowLock|QueryLock|QueryLimitMigratedLock)'`
- `pgrowlocks`: `RUN='^TestReferencePgrowlocks'`
- Session advisory locks: `RUN='^TestReferenceAdvisorySession'`
- Transaction advisory locks: `RUN='^TestReferenceAdvisoryXact'`
- Inheritance lock contention: `RUN='^TestReferenceInheritance(DMLContention|AlterMergeStopLocks|QueryLockRecheck|QueryRelationWait|QueryRowMarks)$'`
