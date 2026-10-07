# Transactions, isolation and relation locks

[README](../README.md) · [SQL dialect and limits](DIALECT.md) · [SQL reference](SQL.md)

`DB.BeginTx` / `Conn.BeginTx` and `sql.Tx.Commit` / `Rollback` support
READ COMMITTED, READ UNCOMMITTED and REPEATABLE READ, with read-only or read-write
access. READ UNCOMMITTED has READ COMMITTED visibility but retains its requested
name in settings. Default Go isolation honors the session default; explicit
`sql.LevelReadCommitted`, `sql.LevelReadUncommitted` and `sql.LevelRepeatableRead`
override it. Go `ReadOnly: false` explicitly requests read-write access. SQL
omitted modes inherit session defaults. INSERT (VALUES or SELECT), scalar UPDATE,
SELECT, DELETE, settings and the single-target AX utility are supported, including
prepared execution. CREATE tables before a block; transactional DDL, savepoints,
SERIALIZABLE/SSI and DEFERRABLE safe snapshots remain unsupported.
In read-write explicit blocks, role creation/removal and SET/RESET SESSION
AUTHORIZATION retain native `0A000` capability boundaries, not PostgreSQL parity.
Identity SELECT remains supported in both access modes.

Supported whole-input SQL syntax (one statement per public call):

```sql
BEGIN [WORK | TRANSACTION]
START TRANSACTION
(COMMIT | END | ROLLBACK | ABORT) [WORK | TRANSACTION] [AND [NO] CHAIN]
```

BEGIN/START accept ordered `ISOLATION LEVEL READ COMMITTED | READ UNCOMMITTED |
REPEATABLE READ`, `READ ONLY` and `READ WRITE` assignments, comma-separated or
juxtaposed. `SET TRANSACTION` changes current characteristics; `SET SESSION
CHARACTERISTICS AS TRANSACTION` changes next-transaction defaults. Complete
SERIALIZABLE and [NOT] DEFERRABLE modes fail native `0A000`,
`transaction mode is unsupported`; malformed clauses/suffixes retain `42601`.
These excluded modes are not implemented as aliases or successful no-ops.
Complete SAVEPOINT/RELEASE/ROLLBACK TO and two-phase PREPARE TRANSACTION/
COMMIT PREPARED/ROLLBACK PREPARED similarly fail `0A000` without effects.

SQL BEGIN groups private writes on the physical session. Redundant BEGIN/START
succeeds with count zero and WARNING `25001`, `there is already a transaction in
progress`, retaining that incarnation and its writes/holds. COMMIT/END publishes
the entire batch; ROLLBACK/ABORT discards it. Plain ends and AND NO CHAIN outside a
block succeed with count zero and WARNING `25P01`, `there is no transaction in
progress`. Outside AND CHAIN fails ERROR `25P01`, `COMMIT AND CHAIN can only be
used in transaction blocks` or `ROLLBACK AND CHAIN can only be used in transaction
blocks` (canonical text also for END/ABORT). Successful controls have no
parameters/results and affected count zero. State warnings/errors have exact
WARNING/ERROR and nonlocalized severity; optional diagnostic fields are absent.

AND CHAIN ends the old block, releases its holds **after publication**, and
installs a fresh healthy Active incarnation with command zero and the current
isolation/access modes, no inherited snapshot, private writes, deletes or locks.
Healthy CHAIN preserves the block's final modes despite changed session defaults.
An admitted failure first restores current modes to the defaults captured at
transaction entry (before BEGIN/Go overrides), and rolls back in-block default
changes. Aborted CHAIN carries those restored modes, not the failed overrides.
Same-epoch failed-block SQL COMMIT rolls back successfully.
A fresh chained block's abort baseline is its carried modes, not its session
defaults; later failed mode overrides restore that carried baseline.
AND NO CHAIN and plain ends leave Idle; the next ordinary INSERT autocommits.
Both SQL and Go ends resolve the same settings actions. COMMIT preserves SESSION
changes and expires LOCAL; ROLLBACK and abort restore pretransaction values.
CHAIN resolves those old actions before installing the next block, which inherits
committed SESSION values but no LOCAL values or pending settings actions.
Chain birth/ticket capacity is preflighted before old publication under a short
replacement read fence, never a transaction-lifetime fence. Finished controls
cannot be retracted by late cancellation/replacement or advance the next block.

Actual `PrepareContext` of a control has no effects/notices/publication/release;
execution evaluates the current block state, not the state at preparation.
Start Prepare in Aborted fails `25P02`; end Prepare in Aborted succeeds but
ordinary commands remain `25P02` until that handle executes. Idle end-chain
Prepare succeeds; its execution fails `25P01` if still Idle. Malformed SQL is
parsed first, including in Aborted blocks.

Pin a `sql.Conn` for SQL blocks and explicitly end **before returning it to its
pool**. Borrower Close and healthy ResetSession preserve the block. GoTx handles
are incarnation-qualified: SQL COMMIT AND CHAIN, or SQL end then BEGIN, makes an
old Go Commit/Rollback fail `stuntgres: stale transaction reference` without
ending newer work. This intentionally differs from pinned lib/pq: its old
GoTx.Commit after SQL COMMIT AND CHAIN issues an unqualified end and publishes
the newer block. Do not mix these lifetimes expecting pq's behavior.

Run `go test -count=1 -v -timeout=60s -run '^TestTransactionControlsSmoke$' ./smoke`
for public direct/held-prepared peer visibility, recovery, aliases, chains and
complete warning/error/count/INT4 assertions. Independent real PostgreSQL
coverage is selected by `go test -tags=reference -count=1 -v -timeout=60s
-run '^TestReferenceTransactionControls' ./reference`.

## Relation locks

SELECT acquires transaction-owned ACCESS SHARE; INSERT and DELETE acquire ROW EXCLUSIVE.
These automatic modes coexist, including concurrent readers and writers. COMMENT
TABLE/COLUMN acquires internal SHARE UPDATE EXCLUSIVE at Execute (not Prepare):
it coexists with AS/RX, but conflicts with another owner's SUE and AX.
`LOCK TABLE <name> IN ACCESS EXCLUSIVE MODE` is the only explicit lock syntax:
one existing optionally qualified/quoted table, no ONLY/list/star/default mode
or other modes; optional `NOWAIT` is supported. Execution requires an explicit SQL or driver block and the
current role's existing superuser authority. Ordinary SELECT/INSERT privileges
or ownership alone do not authorize this bounded native path. PostgreSQL also
supports additional AX privilege alternatives not modeled here.

`NOWAIT` grants compatible or self-held locks immediately and otherwise returns
55P03, `could not obtain lock on relation "t"` (qualified input preserves the
decoded qualification), without joining the queue. Earlier conflicting waiters
prevent barging, while self-reentry and compatible cohorts remain eligible.
`lock_timeout` uses the ordinary SET/RESET/SET LOCAL lifecycle and applies to
each queued acquisition, including automatic SELECT/INSERT/UPDATE/DELETE Prepare
locks, UPDATE/DELETE row waits and COMMENT/ALTER execution. Immediate grants are
untimed; every later acquisition gets a fresh budget. Expiry returns 55P03,
`canceling statement due to lock timeout`. During direct SELECT/INSERT/UPDATE/DELETE
relation analysis and semantic Prepare, lock timeout also
reports the relation-name token's character Position (including schema qualification).
Cached Execute, UPDATE/DELETE row waits and utility lock timeouts have no parser Position; statement
timeout (57014) likewise does not acquire that parse-analysis decoration.
The earlier absolute SQL deadline wins against statement_timeout (57014);
an exact absolute tie favors lock timeout. Equal configured durations normally
favor statement timeout because command admission occurs first. Caller context
cancellation/deadlines retain their original Go error identity, not SQLSTATE.
Failed waits remove provisional state and wake eligible successors without
disturbing holders; mandatory statement cleanup aborts explicit transactions
and discards private writes/releases prior locks, or returns implicit work to Idle.

ACCESS EXCLUSIVE conflicts with all automatic modes and another transaction's
AX. Conflicting operations wait using their command context, with compatible
cohorts and earlier incompatible waiter ordering. An owner can SELECT/INSERT/UPDATE/DELETE
under its own AX. SELECT captures its READ COMMITTED view after the wait: holder
commits are visible, holder rollbacks are absent. Explicit locks survive
statement/Rows close, until commit/rollback, admitted failure or physical end.
Failures immediately discard private writes and release all holds, even before
the client rolls back the aborted transaction. Implicit completion publishes
first and releases afterward. Canceled pending requests are removed and wake
eligible successors; caller interruption remains an ordinary context error.

Semantic SELECT/INSERT/UPDATE/DELETE Prepare acquires the same AS/RX as execution. Implicit
Prepare releases at completion; explicit Prepare retains until transaction end
or failure, not stmt.Close. Data preparation fixes characteristic timing and,
in REPEATABLE READ, the transaction snapshot before semantic relation waits.
READ COMMITTED/UNCOMMITTED execution still captures fresh data after grant.
Cached statements retain descriptions, not an owner or future execution snapshot.
LOCK Prepare is metadata-only (zero parameters/results); Execute
performs block/name/current-role checks and acquires AX, affecting zero rows.
Outside a block it returns `25P01`, `LOCK TABLE can only be used in transaction
blocks`, before lookup. Missing targets use `42P01`; ordinary-role AX denial is
`42501`, `permission denied for table t`. There is no upgrade/deadlock detection,
advisory locking or lock introspection. DELETE's row UPDATE and UPDATE's
NO KEY UPDATE holds are distinct mutually conflicting modes on the same logical
row resource; distinct rows arbitrate independently and ordinary SELECT does not
wait on those holds. READ COMMITTED/UNCOMMITTED freeze candidates after relation
grant: peer inserts during row waits are not candidates, committed deletions are
skipped, and changed values require predicate recheck. REPEATABLE READ uses its
retained roots; after real row grant a newer committed predecessor fails `40001`,
`could not serialize access due to concurrent update` or `... concurrent delete`,
before dead-row skipping, predicate recheck or assignment. A post-snapshot
replacement followed by deletion reports concurrent update; deletion of the
snapshot predecessor itself reports concurrent delete. Peer rollback retains
the original predecessor. Use lock_timeout or caller deadlines
to bound conflicting operations; conflicting conversion cycles are
not protected by deadlock detection.

An explicit transaction sees successful earlier private writes/deletes; other
sessions see only committed data. READ COMMITTED/UNCOMMITTED execution captures a
fresh view, so a peer commit becomes visible to the next command,
including a query prepared before that commit. Duplicate rows retain their
multiplicity. Commit atomically publishes all staged relations, preserving peer
commits; Rollback discards them. Every standalone statement commits independently.
A failed multirow INSERT publishes none of that statement, without undoing earlier
autocommit statements.

Committed row vectors grow amortized rather than copying the entire relation on
each INSERT. Captured visible prefixes never change; query views retain only the
relation consumed by the current bounded SELECT, including that relation's own
earlier private writes. Preparation performs metadata analysis, not row capture.
Committed and private versions retain canonical typed values and their descriptor
generations through the existing type foundation. Prepared handles retain the
AST, source and parameter/result description, never a data view. Argument
normalization remains at the driver boundary before transaction admission;
execution imports a typed frame against the current coherent binding. Query
filtering/ordering and Dump use the type-owned operators and literal codecs.

Admitted SQL errors, preparation/binding failures and statement cancellation
abort an explicit transaction and discard its private writes. Valid subsequent
commands are rejected before semantic analysis; malformed SQL still reports its
original syntax error. Full SQL/Go Rollback succeeds. Within the same catalog
epoch, SQL COMMIT of aborted work successfully rolls back with count zero; Go
Commit instead returns the ordinary failed-transaction error below. Valid SQL
ends (including their Prepare) remain recovery-eligible in Aborted; SQL starts
still fail `25P02`. Successful native replacement takes precedence over this
failed-block commit recovery: every old-epoch commit, including read-only,
already-Aborted and COMMIT/END AND CHAIN, returns the ordinary replacement error.
An accepted failed commit disposes the old incarnation and installs no chain;
the next ordinary INSERT autocommits. Full ROLLBACK/ABORT remains available,
including ROLLBACK AND CHAIN into the current epoch. End Prepare is effect-free;
replacement is checked at Execute, not by deleting retained prepared handles.
Empty SQL without arguments does no transaction work. Pre-canceled admission, closed
statement/connection checks and adapter argument rejection do not abort prior
successful private work.

| Boundary | SQLSTATE / English diagnostic |
|---|---|
| Valid ordinary command/Prepare or SQL start after explicit failure | `25P02`: `current transaction is aborted, commands ignored until end of transaction block` |
| Explicit CREATE/ALTER/DROP TABLE, schema, COMMENT or role/session-authorization utilities | Native capability boundary `0A000`: `statement is unsupported in an explicit transaction`; aborts the block (PostgreSQL supports these commands) |
| INSERT NULL into t(v INT NOT NULL) | `23502`: `null value in column "v" of relation "t" violates not-null constraint`; Detail `Failing row contains (null).`, Schema `public`, Table `t`, Column `v` |
| Original parse/coercion/bind/constraint failure | Original diagnostic, not a transaction wrapper; `SELECT FROM` yields `42601`, `syntax error at or near "FROM"`, Position `8` |
| Unsupported Go isolation option | Ordinary `stuntgres: unsupported transaction isolation level`, before state change |
| Nested raw driver BeginTx | Ordinary `stuntgres: transaction already active` |
| Repeated raw handle end | Ordinary `stuntgres: transaction is done`; old handles cannot end a newer incarnation |
| Internal foreign/zero / stale TxRef | Ordinary `stuntgres: foreign or missing transaction reference` / `stuntgres: stale transaction reference` |
| Public sql.Tx repeated end/use | `sql.ErrTxDone`: `sql: transaction has already been committed or rolled back` |
| Go Commit after same-epoch aborted transaction | Ordinary `pq: could not complete operation in a failed transaction`, matching pinned lib/pq's adapter diagnostic, not `25P02`; SQL COMMIT succeeds by rollback |
| Successful native replacement fencing old transaction | Ordinary `stuntgres: catalog replaced before publication` (`ErrCatalogReplaced` internally); private writes and transactional settings discarded at first admitted stale failure, repeated ordinary use stays fenced until full end |
| Closed connection / statement | Ordinary `stuntgres: connection is closed` / `stuntgres: statement is closed` |
| Go cancellation / deadline | Ordinary `context.Canceled` / `context.DeadlineExceeded`, preserving `errors.Is`; database/sql automatic rollback can instead make Tx end report `sql.ErrTxDone` |

Reset/Restore are host-authorized native fixture controls, not PostgreSQL wire
commands or physical-session termination. Another pool on the same Driver and
exact opaque DSN shares replacement; other DSNs (including whitespace changes)
and Drivers remain isolated. Replacement installs one complete catalog epoch,
without merging old private writes. Failed/canceled/read-failing Restore before
installation preserves committed state and otherwise eligible transactions.
An arbitrarily blocking Reader must return before cancellation can be observed;
target locks and the borrowed control connection are not held over external I/O.
Late cancellation cannot retract a completed installation.

Already returned immutable cursors retain their original rows and metadata,
including prior own writes captured before replacement; retention is not
publication. `database/sql` may itself close Rows owned by a Go `sql.Tx` when
that transaction ends. Controls preserve physical session identity, live role
views, baseline settings and prepared handles; transaction rollback still
resolves SESSION/LOCAL setting actions. SQL authorization remains unchanged,
even when an unprivileged pool is used for a native host control.

Feature witness: `make smoke RUN='^TestControlTransactionsSmoke$' VERBOSE=1`.
This exercises two-pool Reset/Restore, retained typed results, stale ordinary
execution followed by COMMIT AND CHAIN, exact Driver/DSN isolation and canceled
Restore reader I/O. Real PostgreSQL reference coverage independently checks SQL
visibility, English diagnostics and same-epoch failed-block recovery; native
replacement errors have no PostgreSQL command-equivalence oracle.

Transaction SQL errors supply ERROR/nonlocalized ERROR. Optional diagnostic
fields are absent unless stated above; native source fields do not claim
PostgreSQL C provenance. Analyzed SELECT/INSERT/UPDATE missing-relation diagnostics
retain the relation identifier's one-based character Position, including UTF8
SQL; utility lookup preserves its independently specified diagnostic fields.
Operation, scope, stage and released-view authority errors are ordinary internal
lifecycle errors, never fabricated SQLSTATE. Identity/command/frontier exhaustion
fails before wrap or publication.

database/sql owns BeginTx-context automatic rollback. Driver Commit/Rollback use
fresh end-operation contexts: database/sql cancels its internal BeginTx context
before invoking a healthy driver Commit. Raw driver Rollback is still available
after BeginTx caller cancellation. Borrower `sql.Conn.Close`, stmt.Close and
healthy ResetSession reuse are **not** physical transaction termination. Physical
close, termination and best-effort abandonment drain operations and discard only
that session's private effects. Committed data and peer settings survive.

Dump serializes committed versions only, never transaction overlays.
Reset/Restore waits for admitted implicit row work, including transaction birth,
immediate lock grants, binding and publication. Replacement admission is a
logical reservation, not a mutex held across lock-manager calls. A queued row
lock request relinquishes that reservation; after grant it readmits and revalidates
the original relation and epoch. ALTER TABLE relinquishes replacement admission
before provider validation and guards the exact descriptor/storage at installation.
If a queued ALTER's name resolves to a different relation after AX is granted
(for example, a newly created search_path shadow), execution returns
`ErrCatalogReplaced` before column lookup, skip notices or publication. It never
alters an unlocked shadow or retries acquisition. Same-identity revisions remain
valid through the coherent grant-time descriptor; pinned PostgreSQL instead
re-resolves the utility target and acquires the shadow's AX before altering it.
Successful replacement
advances the epoch and retires old holders/waiters without waiting for an AX owner
to release; stale work fails with `ErrCatalogReplaced`. Old explicit
transactions cannot execute/publish afterward. Standalone CREATE validates
providers without replacement or publication locks and revalidates its catalog
epoch at installation; replacement during validation returns `ErrCatalogReplaced`.
SET/RESET/SHOW and selected setting calls do not retain row-work replacement admission or publish row writes.
Their session preflight precedes setting installation/SHOW capture; once complete,
later cancellation or catalog replacement does not turn that success into failure.
Concurrent self catalog replacement preserves completed inserts without retrying SQL. Failed, canceled
or read-failing Restore leaves eligible transactions and committed data unchanged.
Captured native engine/driver results
remain immutable across commit, rollback and replacement until Close; database/sql
may close Tx-owned Rows at transaction end, so do not retain public Tx Rows past end.
Publication and terminal admission share a final preflight: cancellation/end
observed before installation publishes nothing; completed installation survives
later cancellation. This is process-local atomic visibility, **not physical
durability, WAL, crash recovery, a network server or PostgreSQL's physical MVCC**.

Actual future-consumer seams are the persistent session Transaction owner,
incarnation-qualified TxRef, StatementScope/Finish, current ResolutionCatalog,
conditional AcquireDataView, immutable version WriteStage and indivisible end
publication. INSERT/UPDATE/SELECT use the existing binder and identity-keyed storage;
there is no parallel catalog/store, retrying SQL executor, or placeholder
savepoint/SSI/provider implementation.

Run `go test -count=1 -run '^TestTransactionSmoke$' ./smoke` (also included in
`make smoke` and `make test`). It asserts direct/prepared visibility,
rollback/commit, refreshed prepared reads, exact rows/types/counts, full
23502/25P02 records, failed Commit/recovery, independent autocommit, cancellation
and native replacement/preservation. Go test reports the result.

Scoped witnesses: `go test -run '^TestTransaction' -count=1 ./...`,
`go test -race -run '^TestTransaction' -count=1 ./...`, and
`go test -tags=reference -run '^TestReferenceTransaction' -count=1 -v ./reference`.
These supplement, not replace, the [full verification targets](DEVELOPMENT.md). The real reference oracle
remains pinned PostgreSQL **18.0 UTF8/C**; current official `/docs/18/` manuals
identify **18.6**, and local **20devel** sources are research only. See
[READ COMMITTED](https://www.postgresql.org/docs/18/transaction-iso.html#XACT-READ-COMMITTED)
and [transactions](https://www.postgresql.org/docs/18/tutorial-transactions.html).
