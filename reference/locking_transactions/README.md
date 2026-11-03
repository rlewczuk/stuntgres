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
- **Timed relation-lock owners.** Prove actual admission with the matching untimed
  cancellation operation before arming SQL timers. Check implicit-error recovery
  and physical identity on that same owner at timeout zero; PostgreSQL uses
  server-side cancellation to retain its transport. Once armed, issue only the
  intended timed operation, without racing a short timer against an observer.
  After holder rollback, require a healthy observer's AX NOWAIT acquisition while
  the timed owner is still alive, then check sentinel visibility and mutation
  through that observer, not as original-owner recovery. Register cleanup so
  workers cancel/join before transactions and Rows/Stmt close, then physically
  discard the armed owner with Raw ErrBadConn before returning its borrower.
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
  come from `pg_prepared_xacts.transaction`; bracket preparation with server
  clocks after the transaction-start instant, not client-clock tolerances.
- **Prepared transactions.** Register bounded GID-specific `ROLLBACK PREPARED`
  cleanup before each prepare-capable operation, ahead of schema, pool and role
  cleanup; never clean unrelated GIDs. `sql.Conn.Close` only returns a borrower:
  close an isolated pool or the raw driver connection to witness physical origin
  end.
  Origin-aware completion scenarios use
  `RUN='^TestReferenceReplicationOriginSession'` in both modes. They capture the
  actual assigned write XID and `roident`, distinguish supplied and persistent
  fallback stamps, and finish detached transactions with an independently
  configured physical actor. Reset selected actors before shared-origin cleanup.
  On the same physical actor, an ordinary assigned commit's local timestamp must
  survive subsequent origin selection without xact configuration: compare the
  selected commit's exact timestamp and independently resolved origin identity.
  Active-origin DROP also captures a real failed transaction's write XID: abort
  may advance origin progress but cannot publish its row, timestamp lookup or
  last-committed cache. Read-only rejection preserves the next commit's supplied
  timestamp and origin without another context setup.
  View witnesses reuse one held SELECT across real prepare/commit/rollback
  transitions and assert complete typed metadata even for empty results.
  Unrelated ordinary logins read through PUBLIC SELECT without acquiring
  completion authority; denied completion must preserve the entire row.
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
- Snapshot values, visibility and capture lifetimes: `RUN='^TestReferenceSnapshot'`
- Snapshot synchronization: `RUN='^TestReferenceSnapshotSync'` covers typed
  zero-argument TEXT exports, literal imports, old committed rows/private-write
  exclusion, multiple RC/RR cuts, lifetime/savepoints/PREPARE reset, SSI, native
  epoch boundaries, indirect expressions and observed grant/Eval barriers.
  Tokens are compared only within a backend. Full portable diagnostics and
  independently expected ordered rows precede backend comparison.
  `TestReferenceSnapshotSyncDescendantWrites` independently exercises direct and
  held inherited UPDATEs (including a modifying CTE), descendant-only triggers,
  generated columns, CHECKs and incoming FK actions. An observed advisory wait
  places a peer commit before recipe evaluation. Direct descendant recipes
  retain the parent cut, while generated RC RI commands acquire a newer cut.
  Foreign-action lanes include direct/held execution, post-prepare default and
  trigger/routine replacement, and self-cascade cycle termination. Separate
  parent and child advisory barriers prove old outer RETURNING versus newer
  action snapshots, and exclude a still-later peer publication from the latter.
  SQL routine wrappers exercise supported default closure without expanding
  volatile builtin-default admission; generated descendants are defined at CREATE.
  `TestReferenceSnapshotSyncUtilitySnapshots` checks CTAS source-grant-before-cut
  ordering and REFRESH nested-view cuts. `TestReferenceSnapshotSyncPartitionUtility` covers
  unbound partition-bound execution; the utility policy-selection lane proves
  conservative metadata demand does not change actual RLS bypass or effects.
  `TestReferenceSnapshotSyncUtilityEvents` checks STABLE start/end event
  callbacks on direct/held REFRESH, with and without data: peer commits during
  an observed callback wait must not change the imported utility cut.
  `TestReferenceSnapshotSyncAlterExpressions` covers direct/held historical
  CHECK validation, ADD defaults, stored-generation backfill, USING and current
  expression/predicate-index routines, child-only retained CHECK execution,
  domain conversion and nonrewriting DEFAULT preflight. A poisoned unrelated
  index routine distinguishes a captured missing value from an index rebuild.
  Peer commits during observed expression waits cannot alter exported committed
  cuts. Importers read untouched witness relations, never an ALTER target still
  locked AccessExclusive by the exporter.
  Real PL NOTICE diagnostics publish callback tokens even when an immutable index
  executor restores its GUC nest; imports check every published token. Stored
  generation uses SET EXPRESSION on a CREATE-defined generated column, inherited
  validation refers to the actually altered attribute, and domain checks use
  represented routine wrappers rather than unsupported volatile stored forms.
  The volatile ADD DEFAULT lane uses a genuine PL routine, avoiding unrelated
  SQL-inlining assumptions. Index NOTICE context retains the public-qualified
  function name under restricted search_path. Advisory barriers also observe
  early operation completion, reporting actual executor errors rather than
  replacing them with an observer timeout; replacement lanes remain intact.
  `TestReferenceSnapshotSyncAlterCancellation` cancels an observed CHECK
  advisory wait and checks aborted-token 55000 before ROLLBACK, expiry 42704
  afterward, private-row rollback and absence of
  partially published columns/constraints. `TestReferenceSnapshotSyncAlterErrorOrder`
  uses nontransactional sequence evidence for parent DEFAULT evaluation before
  child binding/conversion failure, with exact diagnostics and schema rollback.
  `TestReferenceSnapshotSyncAlterAtomicity` covers later CHECK, DEFAULT and index
  failures without partially published schema, converted rows or private inserts.
  Run these four ALTER definitions in both native and `REFERENCE=1` modes;
  structural `TestSnapshotSyncAlterIndexSelection` checks real index identities
  are preserved after preflight-only fill and rebuilt for a physical rewrite.
  The existing `TestReferencePreparedTransactionSessionBoundaries` export lane
  now asserts the real prohibition in both modes, without an unsupported branch.
  Compact feature smoke: `make check RUN='^TestReferenceSnapshotSyncFeatureSmoke$'`
  and the same command with `REFERENCE=1`; the fixture remains pinned to 18.6.
- Legacy bigint snapshot values, identities and consumers: `RUN='^TestReferenceLegacySnapshot'`
- Commit timestamps: `RUN='^TestReferenceCommitTime'`
- Savepoints: `RUN='^TestReferenceSavepoints'`
- Serializable isolation: `RUN='^TestReferenceSerializable'`
- Prepared transactions: `RUN='^TestReference(PreparedTransaction|SessionMessagesPreparedOriginReuse)'`
- Prepared transaction metadata view: `RUN='^TestReferencePreparedXactsView'`
- Relation locks and deadlocks: `RUN='^TestReference(LockModes|RelationLock|RelationDeadlock|SmokeRelation)'`
- Row locks: `RUN='^TestReference(RowLock|QueryLock|QueryLimitMigratedLock)'`
- `pgrowlocks`: `RUN='^TestReferencePgrowlocks'`
- Session advisory locks: `RUN='^TestReferenceAdvisorySession'`
- Transaction advisory locks: `RUN='^TestReferenceAdvisoryXact'`
- Inheritance lock contention: `RUN='^TestReferenceInheritance(DMLContention|AlterMergeStopLocks|QueryLockRecheck|QueryRelationWait|QueryRowMarks)$'`
