# Transactions, isolation and relation locks

[README](../README.md) · [SQL dialect and limits](DIALECT.md) · [SQL reference](SQL.md)

`DB.BeginTx` / `Conn.BeginTx` and `sql.Tx.Commit` / `Rollback` support
READ COMMITTED, READ UNCOMMITTED, REPEATABLE READ and SERIALIZABLE, with read-only or read-write
access. READ UNCOMMITTED has READ COMMITTED visibility but retains its requested
name in settings. Default Go isolation honors the session default; explicit
`sql.LevelReadCommitted`, `sql.LevelReadUncommitted`, `sql.LevelRepeatableRead`
and `sql.LevelSerializable` override it. Go `ReadOnly: false` explicitly requests
read-write access. SQL
omitted modes inherit session defaults. INSERT (VALUES or SELECT), scalar UPDATE,
SELECT, DELETE, settings and the single-target AX utility are supported, including
prepared execution. CREATE tables before a block; sequence DDL, COMMENT ON
SEQUENCE and selected ALTER TABLE ADD COLUMN/TYPE/nullability/named-NOT-NULL/
DROP COLUMN/identity action lists are bounded transactional DDL exceptions. Successful private ALTER
definitions govern subsequent metadata and writes; savepoints restore catalog,
projected row shapes and own writes together. Commit installs these and sequence
changes atomically with DML. Successful statement authorization is retained
through Commit despite later role-catalog revisions or internal actor changes.
Native SET ROLE and transactional SESSION AUTHORIZATION remain excluded;
the real PostgreSQL SET ROLE lifetime witness does not enable native SQL.
Other transactional DDL remains unsupported.
ADD COLUMN jointly restores schema, high-water identities, captured nonvolatile
old-row fills and own writes on rollback/savepoint recovery. Supported nextval
backfill draws remain nontransactional. Standalone SET/DROP DEFAULT and unsupported
mixed DEFAULT lists do not gain transaction eligibility from this exception.
In read-write explicit blocks, role creation/removal and SET/RESET SESSION
AUTHORIZATION retain native `0A000` capability boundaries, not PostgreSQL parity.
Identity SELECT remains supported in both access modes.

Query-derived permanent tables (`CREATE TABLE AS` and `SELECT INTO`) are native
standalone utilities, not transactional DDL exceptions. Execute rejects read-only
work with `25006` and writable explicit blocks with `0A000` before safe-snapshot
acquisition, source lookup, ACL checks or relation waits. Standalone Prepare analyzes the source normally;
native explicit Prepare instead retains the `0A000` utility exclusion. A
Conn-prepared statement passed to `Tx.StmtContext` is reprepared inside the block;
its sticky Prepare error surfaces from the held Execute call, before Execute's
read-only gate. These native exclusions also precede the initial safe-snapshot
wait in `SERIALIZABLE READ ONLY DEFERRABLE`; rejected utilities acquire no snapshot
or producer evidence. PostgreSQL supports the commands transactionally but requires
an initial safe snapshot during their analysis: with an unresolved serializable
writer, a statement timer produces `57014` for direct Execute, explicit Prepare,
and the original Conn-prepared → `Tx.StmtContext` held route. The reference tests
assert that intentional phase difference independently. Normal SELECT and admitted
standalone creation retain their existing snapshot acquisition.

Sequence nextval/setval and session currval/lastval are immediate, non-MVCC state:
REPEATABLE READ retains its table snapshot, not a frozen generator. Ordinary
allocation and setval survive rollback and later row-expression errors.
ALTER SEQUENCE RESTART instead stages a private generator replacement, blocks
peer functions (including cache hits), and commits or rolls back atomically
with table INSERT/UPDATE/DELETE through the shared row-publication frontier.

Identity ALTER ADD/DROP/SET generation/options/RESTART shares the existing private
catalog/generator and savepoint journal. Own future inserts see successful private
definitions; peers and Dump see committed state. Rollback restores definitions and
private restarts, not live allocations already consumed by ordinary identity INSERT.
DROP retains existing rows and NOT NULL. Savepoint rollback removes postmark
relation/row lock contributions while retaining earlier holdings.
Ordinary identity VALUES constrains each complete projected row (NOT NULL, then
CHECK) before demanding the next runtime row. An earlier violation retains that
row's already-demanded sequence/session effects but does not consume later
identity or side-sequence producers. Whole-statement analysis, inferred-parameter
import and supplied conversion preflight remain earlier; successful rows still
publish as one atomic batch, never incrementally. USER override prunes only unused
nonvolatile singleton/query projections, preserving independently ordered and
volatile source demand; decorated multirow VALUES remains fully source-demanded.

UPDATE evaluates its RHS once after actual row-lock wait/recheck, using the
initiating statement's sequence scope; it never replays sequence effects.

CREATE/ALTER OWNED BY acquires transaction-owned AccessShare on the owning table,
retained to transaction end; the dependency cannot publish behind a conflicting
table AccessExclusive holder. Read-only blocks reject persistent sequence DDL
and actual persistent nextval/setval calls. Function checks occur after sequence
opening and ACL checks but before cached-tail use, bounds or mutation. Reading
sequence rows, currval/lastval and caller-owned TEMP sequence mutations remain
allowed; TEMP sequences do not imply TEMP table support.

Supported whole-input SQL syntax (one statement per public call):

```sql
BEGIN [WORK | TRANSACTION]
START TRANSACTION
(COMMIT | END | ROLLBACK | ABORT) [WORK | TRANSACTION] [AND [NO] CHAIN]
```

BEGIN/START accept ordered `ISOLATION LEVEL READ COMMITTED | READ UNCOMMITTED |
REPEATABLE READ | SERIALIZABLE`, `READ ONLY`, `READ WRITE`, `DEFERRABLE` and
`NOT DEFERRABLE` assignments, comma-separated or juxtaposed. `SET TRANSACTION`
changes current characteristics; `SET SESSION CHARACTERISTICS AS TRANSACTION`
changes next-transaction defaults. Malformed clauses/suffixes retain `42601`.
Two-phase SQL controls are described below; their driver Prepare phase is
metadata-only and never reserves a GID or changes transaction state.

## Prepared transactions

`PREPARE TRANSACTION 'gid'` ends the complete current transaction, including
surviving savepoints, and transfers its private row/deletion/catalog/generator
roots and original relation/row/unique tickets to the Engine registry. The
origin becomes Idle and may immediately start independent work. Neither it nor
peers see prepared writes until `COMMIT PREPARED 'gid'`; `ROLLBACK PREPARED`
discards them. Closing the physical origin session, Rows or SQL statements does
not release prepared holds. Completion publishes before waking resource waiters.

Preparation and completion share the canonical optimistic publisher and final
authority/catalogue/counter/SSI checks. Preparation validates row predecessors,
deletions, projected shapes and ordered/unique index deltas without constructing
replacement row heads, deletion maps, slot arrays, storage/generator maps or
logical index installation roots. Expression keys and sorted rebuilt entries
remain necessary validation scratch. Completion revalidates the retained private
roots against current predecessors and materializes installation payloads only
for the actual commit. Validation-only index work retains its captured-storage
optimistic fence; it is not a separate, weaker publication path.
Surviving UPDATE writes retain their original row/base/version identities.
Validation-only preparation checks that the exact base is present and live,
without building a replacement head or slot manifest; completion rechecks those
predecessors before installing the retained updates alongside inserts, deletions
and supported catalogue/index/generator changes.

GIDs are decoded, case-sensitive byte strings: empty is valid, at most199 bytes;
200 or more fails22023. Outstanding duplicates fail42710; unknown completion
fails42704. Either outcome frees the name. Completion requires the exact
preparing principal or a current superuser (42501 with
`Must be superuser or the user that prepared the transaction.`); role membership
or recreating a dropped owner's name confers no authority. Concurrent claimed
completion fails55000 busy; denied, canceled or failed completion retains the
entry and holds. COMMIT/ROLLBACK PREPARED inside a healthy block fail25001 before
lookup; an already-aborted block retains25P02.

Successful preparation keeps SET SESSION candidates and expires SET LOCAL.
Any admitted failing preparation rolls back the entire transaction and setting
journal, even with savepoints. Preparing an aborted block rolls back successfully
without registering it. Idle PREPARE succeeds with WARNING25P01
`there is no transaction in progress`. All three utilities return affected0,
no result columns and no arguments. READ ONLY preparation and outside-block
completion with a read-only default are permitted. Completion changes neither
the origin nor finishing session's settings. Old Go transaction references are
stale after detach and cannot complete the entry or the origin's next transaction.

Actual TEMP sequence/object/namespace use is sticky across savepoint rollback
and fails0A000 `cannot PREPARE a transaction that has operated on temporary objects`.
An unused temporary sequence from an earlier transaction is allowed. Persistent
nextval/currval/setval/lastval memory is allowed; consumed draws remain
nontransactional. TEMP TABLE, DECLARE/WITH HOLD cursors, LISTEN/UNLISTEN/NOTIFY
and pg_export_snapshot retain their existing unsupported syntax/function
boundaries, not executable native restriction parity. Ordinary Go Rows/Stmt
handles are not SQL WITH HOLD cursors.

`pg_catalog.pg_prepared_xacts` (also unqualified through pg_catalog lookup) is a
read-only PUBLIC SELECT source with exactly five columns in order:
`transaction XID`, `gid TEXT`, `prepared TIMESTAMPTZ`, `owner NAME`, `database NAME`.
The nonzero four-byte unsigned XID is stable, distinct from OID, and exported as
decimal bytes; this narrow catalog scalar adds no XID casts, ordering classes,
arrays, functions or column-type syntax. Prepare time is stable UTC microsecond
time, not BEGIN time. Dropped owners project NULL without changing retained
completion authority. Database equals the Engine's current_database/current_catalog
NAME display of its exact opaque DSN. Eager rows own a coherent registry snapshot.

Registry scope is the existing Engine: exact DSN within one Driver shares it;
other Engines/DSNs/Drivers do not. Native preparation is always enabled, unlike
PostgreSQL's default max_prepared_transactions0. There is no native capacity
setting, cross-database cluster emulation, replication, WAL or crash persistence.
Native Reset/Restore/ReplaceCatalog reject at final installation with ordinary
error `stuntgres: cannot replace catalog while prepared transactions exist`,
including Restore reader-I/O overlap. Refusal preserves epochs/data/resources.
Dump stays committed-only and never serializes private writes or GIDs. Complete
all entries before replacement; abandoned entries require explicit completion.


## Serializable snapshot isolation

SERIALIZABLE keeps the existing stable snapshot and publication authority and adds
nonblocking read/write dependencies. Reads of supported permanent stored tables
record whole-relation predicates before filtering, including empty results and
source reads for supported writes. These predicates never wait for a writer and
are not relation/row locks. System providers, sequences and metadata-only effects
do not participate. Existing relation/row lock behavior remains independent.

Dependencies are retained across overlapping transaction lifetimes, including
committed participants, and cleared when the cohort drains. Whole-relation
predicates can reject same-relation workloads PostgreSQL's finer predicates would
allow; continuous overlap can retain graph memory for longer. Unrelated relations
or an unrelated commit-frontier advance alone do not create conflicts.

For standalone query-derived tables, an executed producer keeps its source-read
graph through the atomic definition/data installation fence, even if a runtime
predicate filters out every row. A doomed producer cannot install an empty target;
a healthy empty producer commits its graph at the current row frontier. A nonempty
initial batch advances that frontier once. Successful statement cleanup does not
commit the graph a second time. `WITH NO DATA` and existing-target skips omit the
producer entirely and do not attach executed source-read evidence.

An unsafe write skew cannot publish both participants. Failures use SQLSTATE
`40001`, Message `could not serialize access due to read/write dependencies among
transactions`, a reason-code Detail and Hint `The transaction might succeed if
retried.` A doomed participant discovers its failure on its next participating
read, write or commit; no peer session is asynchronously aborted. Failed
statements expose no partial writes or results. The caller must retry the entire
transaction, not merely the failed statement. This guarantee requires the
participating transactions to use Serializable; mixed-isolation workloads gain
no cross-level serializability guarantee.

Ordinary read-only Serializable transactions still track dependencies until their
snapshot is proven safe. `SERIALIZABLE READ ONLY DEFERRABLE` may wait before the
first data-analysis call, including actual Prepare, while overlapping writers
resolve. Unsafe provisional roots are discarded and recaptured before any data,
handle or predicate is exposed; an accepted safe snapshot needs no SSI predicates.
BEGIN, settings inspection and transaction-control Prepare do not capture it.
Data Prepare may establish the stable snapshot but adds no executed predicate and
the prepared handle owns no execution snapshot. DEFERRABLE outside Serializable
read-only mode has no safe-snapshot effect. Statement timeout covers this wait;
lock_timeout does not. Caller cancellation, session end and catalog replacement
retain their ordinary errors and wake the waiter.

Child rollback restores private writes and current characteristics, not predicate
reads, established dependencies, doom or the history of an attempted participating
write. Outer rollback removes its live participation; committed overlap evidence
remains until safe reclamation. Every successful native epoch replacement,
including same-owner catalog replacement and Reset/Restore, fences old SSI
participation and wakes unresolved safe-snapshot acquisitions with the ordinary
replacement error, without waiting for their candidate writers to finish.
Failed replacement preparation leaves those acquisitions and committed state
unchanged; already captured immutable results retain their original payloads.
A participating write attempt also prevents a subsequent READ ONLY assignment
from acquiring read-only graph exemptions or being omitted from another
transaction's safe-snapshot writer candidates, even after its private write
was rolled back.
Accepted creation and ending evidence have distinct identities even for the
same row version. Rolling back an own DELETE after a saved INSERT discards only
the ending; its surviving insertion remains visible to SSI dependency checks.

Successful PREPARE performs SSI final validation and orders an immune prepared
node without declaring it committed or resolving safe-snapshot candidates.
Prepared pivots cannot be doomed later. A current commit with a dangerous
incoming conflict from a prepared pivot fails40001 with Detail
`Reason code: Canceled on commit attempt with conflict in from prepared pivot.`
Read-site conflicts with immune prepared writers report the retained diagnostic
XID. Origin close retains dependency evidence. COMMIT/ROLLBACK PREPARED resolve
actual outcomes and deferrable candidates, then reclaim safe graph cohorts.

Reference feature smoke (both modes):
`make quickcheck PACKAGES=./reference RUN='^TestReferenceSerializable' VERBOSE=0`
and `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceSerializable' VERBOSE=0`.
See [PostgreSQL 18 isolation](https://www.postgresql.org/docs/18/transaction-iso.html#XACT-SERIALIZABLE)
and [retry handling](https://www.postgresql.org/docs/18/mvcc-serialization-failure-handling.html).

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
installs a fresh healthy Active incarnation with command zero, no inherited
snapshot, private writes, deletes or locks. Successful COMMIT AND CHAIN preserves
the current final modes despite changed session defaults, including a live
child's read-only mode. ROLLBACK AND CHAIN and failed-block COMMIT AND CHAIN
first unwind all retained savepoint levels and carry the oldest mark's parent
read-only mode, not a child mode. Isolation cannot change within those levels.
Without marks, admitted failure restores current modes to the defaults captured
at transaction entry (before BEGIN/Go overrides) and rolls back in-block defaults.
With marks, failure restores the failed level; full aborted CHAIN additionally
unwinds remaining parents before choosing the next block's read-only mode.
Same-epoch failed-block SQL COMMIT rolls back successfully.
A fresh chained block's abort baseline is its carried modes, not its session
defaults; later failed mode overrides restore that carried baseline.
AND NO CHAIN and plain ends leave Idle; the next ordinary INSERT autocommits.
Both SQL and Go ends resolve the same settings actions. COMMIT preserves SESSION
changes and expires LOCAL; whole ROLLBACK and abort without marks restore originals.
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

Run `make quickcheck PACKAGES=./smoke RUN='^TestTransactionControlsSmoke$' VERBOSE=1`
for public direct/held-prepared peer visibility, recovery, aliases, chains and
complete warning/error/count/INT4 assertions. The migrated SQL controls run
without Docker through `make quickcheck PACKAGES=./reference RUN='^TestReferenceTransactionControls[A-Za-z0-9_]*$'`;
independently enable real
PostgreSQL with `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceTransactionControls[A-Za-z0-9_]*$'`.

## Savepoints and recoverable errors

```sql
SAVEPOINT name
ROLLBACK [WORK | TRANSACTION] TO [SAVEPOINT] name
RELEASE [SAVEPOINT] name
```

SAVEPOINT is an unreserved identifier as well as a command keyword.
`SAVEPOINT savepoint` creates that name; bare `RELEASE SAVEPOINT` and
`ROLLBACK TO SAVEPOINT` refer to it (they are not missing-name syntax).
`RELEASE SAVEPOINT savepoint` and `ROLLBACK TO SAVEPOINT savepoint` explicitly
include the optional keyword and refer to the same name.

Savepoints require an explicit block and affect zero rows. Names use ordinary
identifier folding/quoting. The newest equal name shadows earlier marks:
releasing it reveals the earlier one. ROLLBACK TO restores writes, deletes,
sequence catalog overlays, typed settings and their SESSION/LOCAL journal,
retains its target for repeated rollback, and removes later marks. RELEASE
keeps those changes but removes the target and all later marks. PostgreSQL's
read-only exception is preserved: RELEASE restores the selected mark's parent
read-only mode; ordinary LOCAL settings survive until outer end.

An admitted error with marks restores the newest mark's effects, preserves
ancestors and leaves the block Aborted. Only execution of a known ROLLBACK TO
recovers it. Unknown targets use `3B001`, `savepoint "<name>" does not exist`;
SAVEPOINT/RELEASE in Aborted use `25P02`. Preparing controls has no effects or
name lookup; Idle preparation succeeds, but execution outside a block raises
`25P01` with the canonical command tag. Aborted rollback preparation succeeds
without recovery; cached handles resolve execution-time marks and block state.
Full SQL/Go ends and physical session end clear all marks.

Sequence nextval/setval and session currval/lastval never rewind. Rollback of
post-mark CREATE/RESTART restores the prior logical generator binding, not a
copy of its counter. REPEATABLE READ's first snapshot and characteristic timing
remain transaction-monotonic even when the first query followed the mark.
Changed isolation is forbidden inside a savepoint level; identical assignment
is allowed. Read-only to read-write is forbidden inside a read-only child.

Locks remain on the same outer transaction ticket, but ROLLBACK TO and
recoverable error unwind restore its held resource/mode snapshot. New resources
and later upgrades disappear; earlier and incomparable modes survive. RELEASE
retains contributions for the parent. Cursor rollback and PL/pgSQL remain excluded.
Successful Reset/Restore fences and discards stale private marks; rollback-to
cannot resurrect an old catalog epoch. Full rollback remains recovery-eligible.
Dump omits private writes; Restore does not accept transaction controls.

Feature smoke: `make quickcheck PACKAGES=./smoke VERBOSE=0 RUN='^TestSavepointsSmoke$'`.


## Relation locks

Ordinary SELECT acquires transaction-owned ACCESS SHARE; locking SELECT acquires
ROW SHARE; INSERT, UPDATE and DELETE acquire ROW EXCLUSIVE. These automatic
relation modes coexist, including concurrent readers and writers. COMMENT
TABLE/COLUMN acquires internal SHARE UPDATE EXCLUSIVE at Execute (not Prepare):
it coexists with AS/RX, but conflicts with another owner's SUE and AX.
`LOCK [TABLE] target [, target ...] [IN lockmode MODE] [NOWAIT]` supports all
eight PostgreSQL table modes, defaulting to ACCESS EXCLUSIVE. Each target accepts
an optionally qualified/quoted name, `name *`, `ONLY name` or `ONLY(name)`.
Native tables are standalone: inheritance/partition hierarchies remain unsupported.
Targets resolve and acquire in requested order; a blocked first target prevents
lookup of later targets. Views recursively lock bound table/view sources in
definition order with the same mode, even with ONLY. Ordinary view sources use
the view owner; security_invoker uses the original statement invoker at every
nested boundary, without repeating schema lookup on already-bound sources.

Execution requires an explicit SQL or driver block (read-only is permitted).
Any table-level MAINTAIN, UPDATE, DELETE or TRUNCATE right permits every mode;
INSERT permits ACCESS SHARE/ROW SHARE/ROW EXCLUSIVE, SELECT only ACCESS SHARE.
Column-only rights do not qualify. Default owner rights and PUBLIC grants apply;
an explicit empty owner ACL removes use rights, while superusers remain allowed.
Native ACL mutation remains internal; SQL GRANT and public ACL APIs are not added.

The symmetric conflict matrix below lists every conflicting held mode. Modes
accumulate as bits, not a strongest-mode ranking; a transaction never conflicts
with itself. AS=ACCESS SHARE, RS=ROW SHARE, RX=ROW EXCLUSIVE, SUE=SHARE UPDATE
EXCLUSIVE, S=SHARE, SRX=SHARE ROW EXCLUSIVE, X=EXCLUSIVE, AX=ACCESS EXCLUSIVE.

|Requested|Conflicting held modes|
|---|---|
|AS|AX|
|RS|X, AX|
|RX|S, SRX, X, AX|
|SUE|SUE, S, SRX, X, AX|
|S|RX, SUE, SRX, X, AX|
|SRX|RX, SUE, S, SRX, X, AX|
|X|RS, RX, SUE, S, SRX, X, AX|
|AX|AS, RS, RX, SUE, S, SRX, X, AX|

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
disturbing holders; mandatory statement cleanup aborts explicit transactions.
Without marks it discards private writes/releases prior locks; with marks it
preserves premark effects/holdings and removes failed-level lock contributions.
Implicit work returns to Idle.

ACCESS EXCLUSIVE conflicts with all automatic modes and another transaction's
AX. Conflicting operations wait using their command context, with compatible
cohorts and earlier incompatible waiter ordering. An owner can SELECT/INSERT/UPDATE/DELETE
under its own AX. SELECT captures its READ COMMITTED view after the wait: holder
commits are visible, holder rollbacks are absent. Explicit locks survive
statement/Rows close, until commit/rollback or physical end. Without marks,
admitted failures discard private writes and release all holds even before
client rollback; recoverable failures retain the outer ticket. Implicit completion publishes
first and releases afterward. Canceled pending requests are removed and wake
eligible successors; caller interruption remains an ordinary context error.

Semantic SELECT/INSERT/UPDATE/DELETE Prepare acquires the same AS/RX as execution. Implicit
Prepare releases at completion; explicit Prepare retains until transaction end
or failure without marks, not stmt.Close. Data preparation fixes characteristic timing and,
in REPEATABLE READ, the transaction snapshot before semantic relation waits.
READ COMMITTED/UNCOMMITTED execution still captures fresh data after grant.
Cached statements retain descriptions, not an owner or future execution snapshot.
LOCK Prepare is metadata-only (zero parameters/results); Execute
performs block/name/kind/current-role checks and acquires the requested modes,
affecting zero rows. Outside a block it returns `25P01`, `LOCK TABLE can only be
used in transaction blocks`, before lookup. Missing targets use `42P01`; explicit
missing schemas use `3F000`. Denials use `42501`, `permission denied for table t`
or `permission denied for view v`. Direct sequences fail `42809`, `cannot lock
relation "s"`, Detail `This operation is not supported for sequences.`, before ACL.
Utility diagnostics have no Position or synthetic Schema/Table fields.
Command feature checks: `make quickcheck VERBOSE=0 PACKAGES=./reference RUN='^TestReferenceLockModes'`
and `make quickcheck REFERENCE=1 PACKAGES=./reference VERBOSE=0 RUN='^TestReferenceLockModes'`. See the
[command change note](#lock-table-command--change-note) for discovery and coverage.
Advisory locking and lock introspection
remain unsupported. DELETE's row UPDATE and UPDATE's
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
to bound acyclic conflicting operations.

The canonical relation/row lock manager detects cycles across exact live
transaction owners. Conflicting holders create hard dependencies; earlier
conflicting queued requests create soft dependencies. It searches finite queue
ordering constraints before aborting: a compatible queue rearrangement resolves
an avoidable soft cycle without an error or revoking any held lock. An unavoidable
cycle terminalizes one actual cycle member with `40P01`, `deadlock detected`.
Victim choice is unspecified. Without savepoints, mandatory Finish discards
all private effects and releases locks, allowing a survivor to complete before
explicit ROLLBACK. With marks, the failed level's effects and lock contributions
roll back while premark holdings survive. An explicit victim is Aborted; known ROLLBACK TO
or full ROLLBACK recovers according to the boundaries above.

Detection normalizes live waits using the same cancellation and absolute-deadline
arbitration as ordinary queue settling, both before searching and immediately
before publishing a queue rearrangement or a prepared victim diagnostic. If that
last normalization removes or grants a request, the stale graph, witness and
proposal are discarded and the live decision is rebuilt; cancellation callbacks
need not obtain the manager mutex first. Acyclic queues store actual dependencies,
not all hypothetical ordering pairs. Ordering and branch scratch is allocated
only for cycle search and reused across proposals; search remains finite and
uncapped.
The selected victim also receives an exact-request interruption check immediately
at terminalization, so cancellation or expiration during later observations in
the final sweep cannot replace its canonical error with `40P01`.

Deadlock diagnostics have ERROR severity and Hint `See server log for query
details.` Native Detail records actual transaction/session/epoch, requested mode,
relation identity and logical row identity for every cycle edge. These are not
PostgreSQL backend PIDs, XIDs or physical tuple addresses; native Context does not
invent those mechanisms. Direct relation analysis and semantic data Prepare add
the relation token's one-based character Position to 40P01 (as for lock timeout).
Cached Execute, utility locks and runtime row waits leave Position absent.
Cancellation/deadline cleanup and exact epoch fencing remain independent of
cycle detection. Nonblocking predicate/SSI tracking is separate from this blocking
graph; it cannot introduce a predicate deadlock.

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
earlier private writes. Data preparation may establish a transaction snapshot but
does not register an executed SSI predicate or retain row capture in the handle.
Committed and private versions retain canonical typed values and their descriptor
generations through the existing type foundation. Prepared handles retain the
AST, source and parameter/result description, never a data view. Argument
normalization remains at the driver boundary before transaction admission;
execution imports a typed frame against the current coherent binding. Query
filtering/ordering and Dump use the type-owned operators and literal codecs.

Admitted SQL errors, preparation/binding failures and statement cancellation
leave an explicit block Aborted. Without marks they discard all private writes
and release holds; with recoverable marks they restore the newest mark's effects
and retain native outer-ticket holds until full end. Valid subsequent
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
| Explicit CREATE/replace/ALTER/DROP VIEW, CREATE/DROP TABLE, table/column RENAME, schema, non-sequence COMMENT or role/session-authorization utilities | Native capability boundary `0A000`: `statement is unsupported in an explicit transaction`; aborts the block (PostgreSQL supports these commands). Selected nullability/named-NOT-NULL/DROP COLUMN/identity lists are supported. Read-only view utility tags are CREATE VIEW, ALTER VIEW and DROP VIEW. |
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

Feature witness: `make quickcheck PACKAGES=./smoke RUN='^TestControlTransactionsSmoke$' VERBOSE=1`.
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
that session's attached private effects, never detached prepared entries.
Committed data and peer settings survive.

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
SSI/provider implementation.

Run `make quickcheck PACKAGES=./smoke RUN='^TestTransactionSmoke$'` (also included in
`make quickcheck PACKAGES=./smoke` and `make quickcheck`). It asserts direct/prepared visibility,
rollback/commit, refreshed prepared reads, exact rows/types/counts, full
23502/25P02 records, failed Commit/recovery, independent autocommit, cancellation
and native replacement/preservation. Go test reports the result.

Scoped migrated SQL witnesses:
`make quickcheck PACKAGES=./reference RUN='^TestReferenceTransaction[A-Za-z0-9_]*$'`,
`make quickcheck RACE=1 PACKAGES=./reference RUN='^TestReferenceTransaction[A-Za-z0-9_]*$'`, and
`make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceTransaction[A-Za-z0-9_]*$'`.
Pure root mechanics remain selected separately by
`make quickcheck PACKAGES=. RUN='^TestTransaction(TerminalReasons|RawReuseState)$'`.
They distinguish CleanClose, Terminated and Abandoned without SQL; public
rollback/publication remains in the migrated scenarios. Native raw Begin-context
lifetime is substantive in both binary modes, not pq raw-context parity.
Its independent PG server/direct+prepared cancel, PID preservation and healthy
rollback witness lives once in `TestReferenceTransactionBeginContextCancellation`.
See [migration inventory and capability phases](DEVELOPMENT.md#dual-mode-transactions-savepoints-and-locking).
These supplement, not replace, the [full verification targets](DEVELOPMENT.md). The real reference oracle
remains pinned PostgreSQL **18.0 UTF8/C**; current official `/docs/18/` manuals
identify **18.6**, and local **20devel** sources are research only. See
[READ COMMITTED](https://www.postgresql.org/docs/18/transaction-iso.html#XACT-READ-COMMITTED)
and [transactions](https://www.postgresql.org/docs/18/tutorial-transactions.html).

## LOCK TABLE command — change note

Public `LOCK TABLE` witnesses are consolidated into untagged, dual-mode
`reference/` tests, replacing the command's root/native-only smoke copies.
Ordinary Docker-free discovery and pinned PostgreSQL runs share the complete
SQL assertion bodies, including the eight-mode conflict matrix, direct/prepared
diagnostics, SQL and driver transaction routes, ordered targets, recursive
views, savepoint contributions and commit/publication versus rollback/discard.

Strengthened witnesses show that healthy active Prepare neither waits for a
conflicting peer nor acquires a table hold. Retained statements check the current
block, catalog and executing ordinary role at Execute; distinct ordinary-role
table/view denials and both sources of an owner-created nested join view are
checked independently. Supported syntax and native boundaries are unchanged:
standalone ONLY/star targets are supported, but native inheritance, SQL GRANT
and lock introspection are not added. PostgreSQL-only ACL, hierarchy and actual
pending-resource observer phases remain explicitly separate capabilities.

```sh
# Docker-free command contract, including held preparation and ordinary roles
make quickcheck VERBOSE=0 PACKAGES=./reference RUN='^TestReferenceLockModes'
# Same completed consumers plus PostgreSQL-only capability controls
make quickcheck REFERENCE=1 PACKAGES=./reference VERBOSE=0 RUN='^TestReferenceLockModes'
```

## Row locks — change note

SELECT supports repeated adjacent `FOR KEY SHARE`, `FOR SHARE`, `FOR NO KEY UPDATE`
and `FOR UPDATE` clauses with optional per-clause `OF` and `NOWAIT`/`SKIP LOCKED`.
The complete list follows ORDER BY and precedes or follows complete bounds.
Existing joins, views and INSERT SELECT share one occurrence-owned arbiter.
OF selects relation/alias occurrences, not output columns; unselected siblings
keep ACCESS SHARE and ordinary SELECT authority, selected targets ROW SHARE plus
UPDATE on any live column. Duplicate targets are idempotent; strength and wait
merge independently. NOWAIT dominates SKIP, and SKIP dominates blocking.
Both conditional policies use the same manager mutex/immediate-grant path.
Unavailable NOWAIT returns55P03 `could not obtain lock on row in relation "t"`;
SKIP rejects the whole joined candidate without a waiter, timer or latest refresh.
Earlier candidate grants remain held. Conflicting earlier queued requests prevent
barging; compatible/self-owned grants remain eligible. These row policies never
bypass table-lock waits, and do not promise planner/join order without ORDER BY.

Different owners conflict only on the same logical row. This matrix lists
requested modes as rows and held modes as columns; **yes** means conflict:

| Requested / held | KEY SHARE | SHARE | NO KEY UPDATE | UPDATE |
|---|---|---|---|---|
| KEY SHARE | no | no | no | yes |
| SHARE | no | no | yes | yes |
| NO KEY UPDATE | no | yes | yes | yes |
| UPDATE | yes | yes | yes | yes |

One owner accumulates modes without blocking itself. UPDATE changing any eligible
simple UNIQUE key takes UPDATE; unchanged keys and payload/INCLUDE-only changes
take NO KEY UPDATE. DELETE takes UPDATE. Standalone unique indexes and genuine
constraint backing indexes share this classification, immediate normalized key
claims, wait/outcome recapture and savepoint lifetime in the existing lock graph.
SameDatum transitions, including NUMERIC scale and signed zero, feed retained RR
key history: changed-then-restored keys are not mistaken for payload-only updates.
LIVE simple-only UNIQUE indexes supply this bitmap even when nonready/invalid.
Expression or mixed-expression indexes are excluded as a whole; their Ready
unique tuple enforcement remains independent. INCLUDE never supplies key bits.
Current uniqueness ignores a stale RR SELECT frontier and sees committed peers.
Ordinary nonunique indexes do not strengthen UPDATE's mode. Other rows and
ordinary MVCC SELECT are not blocked by row locks, including reads of a peer's
privately updated/deleted row (which return the committed visible version).

Execution eagerly freezes qualifying row identities and initial sort order,
then acquires row grants before returning a cursor. Prepare takes ROW SHARE
relation authority but no row grants. Explicit holds survive Rows.Close and
Stmt.Close until transaction end; implicit Finish releases holds before exposing
the eager result. ROLLBACK TO or active-level error unwind restores premark row
holdings and removes later upgrades; RELEASE retains them in the parent.
Reset/Restore fences exact old identities; already finalized results remain valid.

At READ COMMITTED, a committed peer update supplies the latest same-row values
and triggers predicate/join recheck. A deleted or now-nonqualifying candidate is
omitted; acquired grants remain held even when recheck rejects the output.
Rollback preserves the original candidate. No new rows or new join combinations
are discovered after a wait, and results are not re-sorted: ORDER BY before
locking can return changed sort values out of order. Joined lockers lock all
contributing base rows, including unprojected tables, not unmatched source rows.
Duplicate/self-join occurrences retain bag multiplicity.

ORDER output caches retain their admitted occurrence identity separately from
physical row indexes and accepted-result compaction. Unchanged candidates,
rollback and compatible REPEATABLE READ KEY SHARE reuse the original evaluated
outputs, including volatile keys. A qualifying READ COMMITTED replacement
refreshes each visible-output-referenced key once at its original query level;
duplicate keys share that evaluation. Nested views retain independent caches.
Rejected replacements do not evaluate final outputs, and no replacement triggers
sorting again. Thus initially ordered values 11,21 can become 31,21 after a wait.

Volatile timing depends on query-level lock placement. When nextval, ORDER and
FOR occur together (including within a stored view definition), a replacement
refreshes the cached value once. A stored-local row-only FOR settles before a
consuming outer Sort, with or without count decorations: an outer volatile key
evaluates after lower wait/recheck and draws1, not the former native draw2.
This narrow compatibility correction supersedes that documented divergence.
Own-query ORDER followed by FOR still refreshes its volatile cache to draw2
on replacement and retains pre-wait rank. Stored-local immutable upper sorting
now yields 21,31 rather than the former native 31,21 in the pinned replacement
fixture. Inherited-only marks over ordinary nested ordered views retain their
separately qualified native33,23 versus PostgreSQL23,33 result; this is not a
general optimizer or ordering redesign.

Scalar views retain a private execution lineage back to physical base RowIDs,
including hidden/self-join contributors, duplicates and zero-width outputs.
Pending local marks pass through scalar WHERE/joins and a mark owner's own
ORDER, then settle before a consuming outer Sort, aggregate, count or final
publication frontier—not on every inner view row. A stored clause applies to ordinary reads of that subtree;
unrelated outer sources stay unmarked. Overlapping marks normalize SQL strength
and wait policy independently, including stored/inherited options on an occurrence.
Unmarked upper aggregation over a marked view consumes locks at its pre-group
input frontier. Recheck precedes grouping/aggregate Advance; HAVING FALSE and
outer filtering of aggregate outputs cannot release qualifying input locks.
Frozen aggregate outputs never invent row identities or variable contributor lists.
This native eager frontier is not a PostgreSQL optimizer-parity claim. PostgreSQL
can gate constant HAVING FALSE or an upper WHERE FALSE before consuming a marked
child, acquiring no input row grants; native retains qualifying input grants.
Aggregate-dependent HAVING count(*)=0 and upper count-output predicates exercise
genuine executed-input, empty-output lock retention independently.

LIMIT/OFFSET/FETCH is a population/effect frontier. Evaluate OFFSET, then LIMIT,
once before opening a producing child;0 takes no physical row grants. For a
positive/unlimited count, pending marks settle before OFFSET/count consumes
survivors. Acquired OFFSET-discarded and returned rows are held; SKIP-unavailable,
deleted or requalified-away candidates spend neither offset nor count.
Direct/inherited top-level marks follow their own Sort and stop after enough survivors, leaving
unvisited tails writable. A stored mark below an outer blocking Sort can hold
all qualified input before an outer LIMIT1. The mark owner's own ORDER freezes
its rank but does not move its mark across an enclosing scalar predicate.
A view-local LIMIT1 followed by outer rejection is empty but retains its inner
winner's hold; it does not choose a different row using the outer predicate.
Likewise local OFFSET1 LIMIT1 retains both skipped and returned holds even if
outer qualification rejects the result. Outer LIMIT0 initializes neither.
Execution-only inherited marks settle before a bounded view's own counters;
ordinary scalar views remain deferred to enclosing qualification. No stored
definition is mutated with an execution's inherited marks.
Unmarked sibling aggregation never consumes another sibling's pending marks.
Public results are still complete before exposure; public Next performs no lock
wait, recheck, predicate or view production.

Feature checks: `make quickcheck PACKAGES=./smoke RUN='^TestQueryLimitSmoke$' VERBOSE=0`
and `make quickcheck PACKAGES=./reference RUN='^TestReferenceQueryLimitMigratedLockSmoke$' VERBOSE=0`.


REPEATABLE READ KEY SHARE can lock a live chain only if no UNIQUE key datum
changed after its retained snapshot. It retains the original snapshot values and
predicates, rather than promoting to the latest values. A committed key change
reports `40001`, even if the key was later restored or a subsequent UPDATE changed
only payload. Numeric scale and signed-zero representation changes count as key
changes; exact-datum no-ops, payload-only updates and rolled-back changes do not.
Changes committed before the retained snapshot do not cause serialization errors.
SHARE, NO KEY UPDATE and UPDATE instead report `40001`,
`could not serialize access due to concurrent update`, for a newer committed
replacement. Deletion (including replacement followed by deletion) reports that
same error for all four modes. This does not change DELETE/UPDATE's existing
direct-delete wording. Read-only transactions reject locking SELECT at Execute
with `25006`. An explicit outer clause reports `cannot execute SELECT FOR <mode>
in a read-only transaction`; an ordinary outer read of stored-mark views reports
`cannot execute SELECT in a read-only transaction`. Prepare analyzes normally.
All modes require SELECT on used columns
plus UPDATE on any live column of each marked base source. View descendant
authority uses the current owner or original security_invoker, only at Execute.
Sequences reject row marks with42809, `cannot lock rows in sequence "s"`, even
as unselected reference-mark siblings; that kind preflight adds no UPDATE/grant/
refresh on unselected sources.
A clause reaching GROUP BY, HAVING or aggregate output rejects with mode-specific
0A000 without a synthetic Position. Inherited expanded-view grouping legality
is checked at Execute after successful Prepare, before nullable outer-join
descendants; direct grouped-query bind restrictions remain unchanged.
Plain DISTINCT retains its existing materializing provider and prohibits locking.
DISTINCT ON and structured set-operation syntax is rejection-only. Malformed
syntax takes precedence; result-level locking is prohibited before set result
arity/common typing. No adjacent execution provider is introduced.
Native virtual catalogs fail0A000 `row locking is not supported for virtual
catalog relations`, even beneath views or when empty. PostgreSQL physical
catalog row locking is separately exercised, not claimed as native parity.

Immediate UNIQUE enforcement uses transaction-owned typed key claims in the
same relation/row lock manager. Claims serialize equal proposed keys against
concurrent inserts and updates; deletes and key-changing updates claim vacated
keys too. READ COMMITTED recaptures committed index roots after a wait, overlays
private writes/deletes, and checks the accepted statement prefix before producing
the next row. Claims survive successful statements in explicit blocks, and use
the normal savepoint, timeout, cancellation, deadlock and catalog-epoch cleanup.
Changing any UNIQUE key's datum upgrades UPDATE from NO KEY UPDATE to UPDATE,
including numeric scale or signed-zero changes whose B-tree comparison is equal.
Payload and exact-datum no-op updates remain compatible with KEY SHARE.

Waits reuse the existing timeout/cancellation/deadlock authority and diagnostics;
native context cancellation remains an ordinary Go error. PostgreSQL physical
tuple wait Context is independently checked only by reference controls, never
fabricated from native logical RowIDs.

## Concurrent index completion and xmin — change note

Concurrent creation retains parent SUE rather than acquiring parent Share.
Captured granted writer cohorts wait for exact top-level outcomes in the existing
hard relation/row/unique deadlock graph. Savepoint contribution release is not
completion; grant follows final committed install or rollback/abort/close.
Later compatible writers cannot extend a captured cohort or queue behind a
fictional parent Share request.

Visibility XIDs are engine-global and first-write/catalog assigned, distinct from
CommitSeq and per-session TxID. Actual root capture advertises snapshot xmin in
the same engine critical section. RR/Serializable snapshots remain owned after
eager rows close; RC Finish releases snapshot ownership even if eager rows remain
open. Endangered unrelated readers and late-acquired xmin at or below the saved
validation boundary delay Valid. Bare BEGIN/idle RC/genuinely newer xmin do not.
Only simple concurrent builders, not expression builders or ordinary readers,
receive the safe-builder exemption. The builder drops its own snapshots before
waiting. Cancellation/deadlines and replacement use the existing exact request
cleanup and epoch authority. See [concurrent design](design/PG18-INDEX-CONCURRENT.md).


Feature checks: `make quickcheck PACKAGES=./internal/engine RUN='^Test(QueryLock|RowLock)' VERBOSE=0`,
`make quickcheck PACKAGES=./reference RUN='^TestReference(QueryLock|RowLock|QueryLimit|QueryOuterJoins)' VERBOSE=0`,
and `make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReference(QueryLock|RowLock|QueryLimit|QueryOuterJoins)' VERBOSE=0`.
Focused feature smoke uses `RUN='^TestReferenceQueryLockFeatureSmoke$'` in both modes.
Native eager failures return no cursor/partial result. PostgreSQL's streaming pq
route may return an acquired prefix followed by terminal Rows.Err; callers/tests
consume the full error and close rows/statements before recovery SQL.
The family selector includes the migrated ViewInsert union; tagged PostgreSQL
ACL/keyed controls remain exclusive and are included only by the tagged command.
Exact native queue witnesses live in engine tests; public native deadlines are
conflict boundaries, not enqueue proof. PostgreSQL resumed-version witnesses use
actual Lock wait state and pg_blocking_pids, not relation-only pg_locks probes.

Sources: [PostgreSQL 18 row locks](https://www.postgresql.org/docs/18/explicit-locking.html#LOCKING-ROWS),
[READ COMMITTED](https://www.postgresql.org/docs/18/transaction-iso.html#XACT-READ-COMMITTED),
and [SELECT locking](https://www.postgresql.org/docs/18/sql-select.html#SQL-FOR-UPDATE-SHARE).

## Ordinary B-tree build and publication

CREATE INDEX takes the existing Share relation mode: reads/ROW SHARE and other
Share builds are compatible; writers and stronger conflicting utilities wait.
The grant-time committed live rows seed the build, so a prior writer's committed
rows are included and rolled-back writes are absent. Existing timeout,
cancellation, deadlock and epoch-fence authorities apply without a second mutex
lock manager.

Native CREATE INDEX Execute remains an explicit-transaction utility exclusion
(0A000) before target lookup/ACL checks/Share requests, unlike PostgreSQL's
transactional ordinary creation/rollback. Its Prepare remains syntax-only even
in active writable/read-only transactions. Read-only Execute rejects CREATE INDEX
with25006 before that exclusion and target lookup. All existing transactional
DML and supported private TYPE/DROP COLUMN changes maintain index entries at
commit, with rollback/savepoint atomicity. Published roots are immutable;
historical and own-write queries still consume the existing selected MVCC view,
not current-only index membership.


## Index maintenance admission and publication

DROP INDEX preflights whole groups, then takes AX on all distinct sorted parents
before AX on selected sorted indexes. REINDEX INDEX takes parent Share before
selected index AX; TABLE takes parent Share before every implemented index AX and
fences the captured index set. Index rename uses SUE, while non-index rename
aliases use AX. All grants use the existing transaction ticket/queue; authority
is checked before waiting and rechecked against exact grant-time identities.
There is no retry by recreated name or a separate lock manager.

DROP admits index ownership or schema ownership; rename requires ownership plus
schema CREATE; rebuild checks parent MAINTAIN with diagnostics naming the requested
index/table. Selected catalog-stage rebuild intents install actual freshly
extracted/sorted/validated roots atomically. Prior writers' committed rows are
included, rolled-back private writes are absent, and unchanged siblings keep their
newest roots. Operation/authorization/epoch/store/context fences veto all partial
installation and normal Finish releases failed/pending implicit contributions.

Native DROP/ALTER INDEX and REINDEX retain syntax-only Prepare even in an active
transaction. Execute returns canonical25006 for read-only before lookup/ACL/wait,
then writable explicit0A000 before those phases. PostgreSQL utilities support
transactional rollback; PostgreSQL REINDEX may resolve/wait before read-only
rejection. The reference suite asserts that difference explicitly. Native queries
still scan parent snapshots; these utilities add no optimizer index-scan locks or
historical visibility based on current-only roots.
See [index maintenance design](design/PG18-INDEX-MAINTENANCE.md).

