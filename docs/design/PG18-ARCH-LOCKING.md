# PG18-ARCH-LOCKING — Blocking lock manager contract

## 1. Decision status, scope and acceptance

This is a design decision artifact, not an implemented lock manager or evidence of running SQL. Signatures below are proposed future `internal/engine` contracts. Only this blocking contract is revised; production code and other component designs remain unchanged.

Decision: one Engine-owned blocking authority coordinates qualified relation, logical-row and advisory resources across physical sessions. Consumers extend the existing parser, binder, transaction and storage seams; there is no second executor, catalog, DSN interpretation, session registry or name-keyed locking mechanism. Short storage/publication synchronization remains necessary but is not PostgreSQL-visible conflict control.

Reuse [TX-VISIBILITY](PG18-ARCH-TX-VISIBILITY.md) §§2–8 for `TxRef`, `SavepointRef`, `RowRef`, `StatementScope`, `TxParticipant`, statement completion, visibility and replacement fencing; [SESSION](PG18-ARCH-SESSION.md) §§3–4 for exact `SessionToken`, `DatabaseTarget`, pointer-owned `*Operation` and physical teardown; [CATALOG-IDENTITY](PG18-ARCH-CATALOG-IDENTITY.md) §§2–3 for `ObjectRef`, epoch and parser names. These are accepted future contracts, not executable foundations. This artifact does not decide snapshot publication, MVCC, isolation transitions, SSI/predicate tracking or safe-snapshot waits. SIRead tracking is nonblocking, cannot cause ordinary lock deadlocks and can outlive commit; PG18-ARCH-SERIALIZABILITY/PG18-TX-SERIALIZABLE owns it separately.

| Selected requirement | Decisions | Independent future witnesses |
|---|---|---|
| Resource and owner identity; pooled coordination | 2 | W10 |
| Eight table/four row modes and automatic acquisitions | 3, 7 | W4, W8, W9 |
| Acquisition, conversion, queues and deadlocks | 4 | W1, W4, W5 |
| Cancellation, timeouts, synchronization | 5 | W2, W5, W6 |
| Statement/transaction/savepoint/close/native release | 6 | W3, W10 |
| LOCK and SELECT interfaces and cutover | 7, 9 | W7, W8 |
| Actual introspection and exposed diagnostic fields | 8 | W1, W4, W6, W11 |
| Session-sized implementation consumer breakdown | 10 | W1–W11 |

The five literal acceptance scenarios are W1 (exactly one unspecified deadlock victim), W2 (prompt leak-free context cancellation), W3 (failed-operation cleanup and savepoint preservation), W4 (complete compatibility and NOWAIT), and W5 (no false deadlock). They are decision/test specifications, not executed behavior.

### Evidence and version policy

Official PostgreSQL `/docs/18/` pages identify **18.6**. Future reference scenarios retain the configured **18.0**, UTF8/C oracle (`reference/fixture/fixture.go`, `referenceImage`, `runReferenceMain`; startup checks in `reference/fixture/lifecycle.go`); no runtime execution is claimed here. Minor-specific cases belong to PG18-REF-ORACLE-18-6, not an unauthorized image/version substitution.

Release semantics: [explicit locking §13.3, Tables 13.2–3](https://www.postgresql.org/docs/18/explicit-locking.html), [LOCK](https://www.postgresql.org/docs/18/sql-lock.html), [SELECT locking clause](https://www.postgresql.org/docs/18/sql-select.html#SQL-FOR-UPDATE-SHARE), [isolation §13.2](https://www.postgresql.org/docs/18/transaction-iso.html), [pg_locks §53.13](https://www.postgresql.org/docs/18/view-pg-locks.html), [lock_timeout](https://www.postgresql.org/docs/18/runtime-config-client.html#GUC-LOCK-TIMEOUT), [deadlock_timeout](https://www.postgresql.org/docs/18/runtime-config-locks.html#GUC-DEADLOCK-TIMEOUT), [diagnostic fields §54.8](https://www.postgresql.org/docs/18/protocol-error-fields.html).

Local PostgreSQL **20devel** and Ramsql are research aids, not release authorities or implementation prescriptions. Release-tagged C corroborates scheduling and diagnostic rules; exact optional procedural fields remain consumer-owned pinned-runtime capture requirements.

## 2. Resources, owners and concrete extension interface

The Engine owns one authority for its selected DatabaseTargets. Same exact opaque DSN within a Driver shares arbitration; different DSNs, targets and Drivers are independent. A physical session supplies lifecycle identity; neither a pool nor its borrower owns a lock. SESSION is the sole admission/registry authority. Numeric IDs alone never authorize work.

```go
type LockKind uint8 // RelationLock, RowLock, AdvisoryInt64Lock, AdvisoryPairLock
type LockResource struct {
    Kind LockKind
    Target DatabaseTarget
    Relation ObjectRef // active only for RelationLock
    Row RowRef         // active only for RowLock
    Advisory uint64    // active only for either advisory kind
}
type LockScope uint8 // TransactionLockScope, SessionAdvisoryScope
type LockOwner struct { Session SessionToken; Tx TxRef; Scope LockScope }
type TableLockMode uint8 // AccessShare, RowShare, RowExclusive, ShareUpdateExclusive, Share, ShareRowExclusive, Exclusive, AccessExclusive
type RowLockMode uint8 // KeyShare, RowShareMode, NoKeyUpdate, RowUpdate
type AdvisoryLockMode uint8 // AdvisoryShared, AdvisoryExclusive
type LockMode struct { Table TableLockMode; Row RowLockMode; Advisory AdvisoryLockMode }
type LockWaitPolicy uint8 // Wait, NoWait, SkipLocked, TryLock
type LockRequestID uint64
type LockGrantID uint64
type LockRequest struct {
    Owner LockOwner
    Resource LockResource
    Mode LockMode
    Policy LockWaitPolicy
    LockTimeout time.Duration // nonnegative; zero disables
    DeadlockDelay time.Duration // positive; settings default 1s
}
type LockAcquireResult struct { Grant LockGrantID; Acquired bool }
type LockRecord struct {
    Resource LockResource
    Owner LockOwner
    Mode LockMode
    Granted bool
    Request LockRequestID // zero for granted record
    WaitStart time.Time   // zero for granted record
}
type LockBlocker struct { Owner LockOwner; Request LockRequestID; Queued bool }
type BlockingLocks interface {
    Acquire(op *Operation, scope *StatementScope, req LockRequest) (LockAcquireResult, error)
    Convert(op *Operation, scope *StatementScope, grant LockGrantID, mode LockMode, policy LockWaitPolicy, lockTimeout, deadlockDelay time.Duration) (LockAcquireResult, error)
    EndTransaction(tx TxRef)
    EndSession(session SessionToken)
    UnlockAdvisory(session SessionToken, resource LockResource, mode AdvisoryLockMode) (bool, error)
    Snapshot(target DatabaseTarget) ([]LockRecord, error)
    Blockers(session SessionToken) ([]LockBlocker, error)
}
func (e *Engine) BlockingLocks() BlockingLocks
```

Enum declarations reserve zero as invalid and use nonzero named values; inactive discriminant members are zero. These are interface declarations, not executable scaffolding.

1. Relation identity is `(Target, ObjectRef)` with whole-object `SubID=0`, valid relation class and matching Engine/target/epoch authority. Names are diagnostic/resolution inputs only. Rename/schema move preserves the resource; drop/recreate does not.
2. Row identity is stable logical `RowRef`, matching Target and relation authority. UPDATE keeps it while replacing VersionRef. Equal values, scan position, slice index, snapshot pointer and physical ctid are not lock identities. Different duplicate-valued rows are independent resources.
3. Advisory identity is database-qualified, not catalog-epoch-qualified. An int64's uint64 bit pattern and a pair packed as `(uint64(uint32(k1)) << 32) | uint64(uint32(k2))` occupy disjoint LockKinds, including negative input bit patterns. They share no relation/row domain.
4. TransactionLockScope requires the exact current live TxRef and matching session/operation/scope. Ordinary relation/row and transaction advisory contributions belong to that incarnation; all subtransactions of the same TxRef are self-compatible. SessionAdvisoryScope requires an advisory kind and zero Tx, with the exact live SessionToken. Advisory self-compatibility uses SessionToken across both scopes, but lifetime accounting remains separate.
5. Validate nil/copied-invalid/foreign/stale operations, scopes, owners, transactions, marks and grants; malformed resource/mode/policy; unknown enums; negative timeout/nonpositive delay; exhausted IDs before enqueue or ownership mutation. These fail with ordinary internal Go errors. Valid SQL failures use §8 instead. IDs are nonzero and never reused within the manager; a stale grant cannot authorize conversion against a later owner.
6. Only the resource-kind mode member is active. A holder retains a set of contributions/modes with acquisition boundaries, not a scalar maximum table strength. Compatibility checks every active contribution. Same-resource repetition is legal. Grant IDs identify exact contributions owned by this manager/incarnation; caller input cannot select an arbitrary peer grant.

`Acquire` returns a grant only after transfer to the scope/provider journal. Transaction-scope transfer records the TX recovery boundary; session advisory transfer records its counted session lifetime, exempt from transaction restoration. `Wait` is blocking; `NoWait` is relation/row-only and errors on inability to grant immediately. `SkipLocked` is row-only; `TryLock` is advisory-only. The latter two return `{Grant:0, Acquired:false}, nil` on conflict, without queue/grant state. Invalid policy/kind combinations fail before mutation. Success returns nonzero Grant and Acquired=true; error returns no new grant.

`Convert` validates exact grant, owner and operation, retains original contributions while queued, then adds the requested contribution at the current recovery boundary. Failure leaves prior contributions unchanged; rollback of a successful later upgrade removes only the added contribution. Never unlock-then-relock. There is no ordinary table/row unlock. Advisory counted session reentry survives statement/transaction rollback; `UnlockAdvisory` decrements one matching session-scope mode count, never transaction counts, returning false if unmatched. The advisory SQL consumer supplies its warning; unlock effects themselves are not undone by rollback.

## 3. Compatibility representation

`X` is conflict between different ordinary transaction owners; `.` is compatible. These symmetric tables are the sole authoritative mode relation, suitable for fixed bitsets indexed by validated enums. Table modes are not a total strength order; names containing ROW still describe whole-relation locks. A transaction does not conflict with itself, even across subtransactions.

| request/held | AS | RS | RX | SUE | S | SRX | E | AX |
|---|---|---|---|---|---|---|---|---|
| AS | . | . | . | . | . | . | . | X |
| RS | . | . | . | . | . | . | X | X |
| RX | . | . | . | . | X | X | X | X |
| SUE | . | . | . | X | X | X | X | X |
| S | . | . | X | X | . | X | X | X |
| SRX | . | . | X | X | X | X | X | X |
| E | . | X | X | X | X | X | X | X |
| AX | X | X | X | X | X | X | X | X |

AS=ACCESS SHARE; RS=ROW SHARE; RX=ROW EXCLUSIVE; SUE=SHARE UPDATE EXCLUSIVE; S=SHARE; SRX=SHARE ROW EXCLUSIVE; E=EXCLUSIVE; AX=ACCESS EXCLUSIVE.

| request/held | KEY SHARE | SHARE | NO KEY UPDATE | UPDATE |
|---|---|---|---|---|
| KEY SHARE | . | . | . | X |
| SHARE | . | . | X | X |
| NO KEY UPDATE | . | X | X | X |
| UPDATE | X | X | X | X |

Row holds block writers/lockers, not ordinary SELECT. Relation locks and row locks are distinct: a query must first obtain its required relation mode. Advisory shared/shared is compatible; exclusive conflicts with either mode across sessions. Same-session counted reentry can bypass waiters across transaction/session scopes.

## 4. Acquisition, conversion and wait/deadlock transitions

| State/event | Atomic manager effect | Caller result |
|---|---|---|
| Validating, invalid input/interrupted owner | No queue/contribution | Original error |
| Validating, eligible | Grant plus lifetime-journal ownership transfer | Acquired=true |
| Validating, conflict with NoWait | No queue/contribution | NOWAIT error |
| Validating, conflict with SkipLocked/TryLock | No queue/contribution | Acquired=false |
| Validating, conflict with Wait | Allocate exact RequestID; enqueue and record dependencies | Await authoritative terminal state |
| Queued, eligible | Remove wait/dependencies; install contribution and transfer ownership once | Acquired=true |
| Queued, cancel/timeout/victim/end/fence | Remove request/dependencies; no new contribution; recompute successors | Originating failure |
| Granted, scoped restore/end/unlock | Remove only eligible contribution/count; recompute successors | Lifecycle completion |

Every request has exactly one terminal result and at most one ownership transfer. Failed requests cannot acquire later. Wakeups are notifications, not grants; waiters recheck stored state. A conversion is a request in the same queue authority, never a second mechanism.

Each resource has monotone arrival ordering. Eligibility requires compatibility with other holders and earlier incompatible waiters; compatible cohorts may proceed together, but later conflicting requests do not starve earlier ones. Same-owner reentry must not wait behind requests blocked by that owner's existing hold. A conversion may still wait for another owner's incompatible hold. Queue rearrangement required below is the explicit exception to arrival order, not permission for arbitrary priority policies.

Maintain hard edges to conflicting holders and soft edges to earlier conflicting waiters separately. Ordinary holder authority is TxRef. Advisory holders are SessionToken-qualified, while their pending operation/request identifies the current transaction failure destination; a mixed advisory/ordinary graph must follow that session's admitted waiting request rather than omit advisory edges. SESSION/TX serializes one pending request per physical session. Idle advisory holders can block indefinitely but do not form a cycle without an admitted wait. Grant/removal/cancel/restore/end recomputes both dependencies and eligibility, including successors blocked solely by a removed queued waiter.

DeadlockDelay initiates a deferred check, not a timeout or minimum error latency: an already provable cycle may be detected on enqueue. Check the live graph under manager synchronization. First admit a compatible queue permutation removing avoidable soft cycles while preserving unrelated arrival order. Only an unavoidable cycle may elect a victim; concurrent checks must terminalize at most one victim for the same resolved cycle. Capture actual cycle edges/identities and terminalize the selected request atomically. Do not promise victim identity, retry SQL or copy PostgreSQL's internal algorithm.

Release corroboration: [REL_18_0 lmgr README](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/storage/lmgr/README) explains holder/queue ordering and hard/soft dependencies; [proc.c, ProcSleep/LockErrorCleanup](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/storage/lmgr/proc.c) covers early upgrade deadlock and cancel/grant races.

Return 40P01 outside manager synchronization through StatementScope.Finish. TX owns aborted/subtransaction state, private-effect discard and release. Other requests become grantable only after their actual blockers have been released; notification cannot expose victim writes. In the required opposing-order scenario without savepoints, victim ordinary private writes and holds are discarded and the survivor finishes. A recoverable ancestor can retain blocking holds after victim subtransaction failure; the design does not promise an ancestor-blocked survivor finishes before full victim end. Session advisory holds survive victim transaction rollback and require unlock or physical end. No cycle means indefinite waiting is legal absent an interruption.

## 5. Synchronization, interruption and deadlines

Manager metadata synchronization linearizes enqueue/grant/cancel/restore/end and validates exact owner incarnation. It is **never nested** with Engine.mu/publication, session admission or transaction-state synchronization. TX must call manager/provider hooks outside its state/publication critical sections. The lifetime journal needed for transfer is manager-owned provider state, not a callback into TX while holding manager synchronization.

TX supplies an immutable exact-incarnation scope/recovery-boundary ticket before manager entry; per-session statement/control serialization prevents concurrent mark/Finish mutation. Manager-owned provider state validates that ticket and journals transfer without calling TX. Lifecycle hooks mirror transaction/epoch fencing outside TX locks, and Finish restores the journal outside manager locks. Thus enqueue/grant/cancel linearize in one authority without nesting TX/manager synchronization.

The execution sequence is resolve/capture identities under catalog/visibility authority → release its synchronization → acquire/wait → revalidate epoch, descriptor and row predecessor through existing TX/DML authority → proceed with stage/publication. Short engine/storage locks remain for memory safety, not to cover SQL waiting. Catalog replacement and session/transaction admission fencing prevent stale work from proceeding between these steps. Manager state can record fenced owners/epochs without reading another subsystem under its mutex; publication/recovery revalidates authoritative state. No sleeping, external I/O, arbitrary callbacks or abort notification under these locks. Terminal state is published before notification, and notifications occur outside critical sections. A grant grants exclusion, not visibility or permission to publish; immutable retained payloads are never mutated.

Use `op.Context()` from the same noncopied lease. Register its cancellation wakeup before a possible wait, then check interruption at enqueue linearization to close the check/register/enqueue lost-wakeup gap. Callbacks and timers carry exact RequestID and owner incarnation. Cancellation winning before transfer removes pending/provisional new contributions and returns original context.Canceled/context.DeadlineExceeded promptly, without waiting for holder release. If transfer wins, later interruption goes through mandatory Finish/provider recovery; earlier holds remain according to §6. Session advisory ownership already successfully transferred is intentionally not undone by transaction failure. No failed Acquire return retains a provisional grant. Stop registrations on terminal completion; stale callback/timer events are inert.

LockTimeout measures only this request's wait interval, separately for every automatic/explicit acquisition; zero disables it. DeadlockDelay does not limit an acyclic wait. Statement timeout includes the whole statement and comes from the operation interruption owner. PG18-ADMIN-SETTINGS-TIMEOUT owns settings and statement timer scope; manager consumes effective per-attempt inputs. Preserve originating errors; SQL signaling is not Go context cancellation. If lock and statement deadlines have both fired, earlier deadline wins; equal deadlines select lock timeout, following REL_18_0. No invented SQLSTATE for Go context errors. Timer completion uses the same single-terminal transition as cancellation and releases no peer state.

Physical end stops admission, cancels/drains operations and calls Transaction before AdvisorySession outside locks. RequestEnd is nonblocking from an operation; never wait for one's own lease. Borrower sql.Conn.Close, idle pooling, stmt.Close and rows.Close do not end explicit transaction/session locks. GC abandonment remains best effort with no prompt-release guarantee.

## 6. Transaction, savepoint and native replacement lifecycle

The provider implements the accepted TxParticipant, not a replacement transaction model:

| Hook/boundary | Lock effects |
|---|---|
| Mark(tx) | Immutable exact-provider/incarnation journal boundary; partial coordinated Mark failure retires acquired marks through TX |
| Restore(tx, mark) | Remove post-boundary queued/granted transaction contributions; restore prior mode sets/counts; wake eligible successors; never erase an entire owner/resource with older holds |
| ReleaseMark(tx, mark) | Retire mark and retain/merge child contributions into parent; not ordinary lock release |
| PrepareEnd(ctx, tx, commit) | Return local PreparedTxAction recording disposition; no release before outcome |
| PreparedTxAction.Install/Discard | Local infallible/non-reentrant disposition only; neither takes manager synchronization nor releases holds under publication synchronization |
| EndTransaction(tx) | TX invokes after final outcome outside publication/state synchronization; idempotently remove exact-incarnation requests/transaction contributions, never session advisory or peer holds |
| EndSession(token) | After TX cleanup/drain, remove exact-token residual requests and session advisory counts; idempotent; never global unlock-all |

TX failure cleanup invokes provider restoration even without a named savepoint. Every admitted command completes StatementScope.Finish before Operation.Done. An internal restoration failure forces TX full abort and continued cleanup; never claim partial Active recovery.

Implicit success releases transaction holds only after publication; failure discards stage/incarnation and releases. Explicit statement success retains holds. Admitted error/cancellation aborts the current subtransaction and removes its transaction contributions while preserving recoverable ancestors. ROLLBACK TO restores the selected boundary and establishes a fresh recovery mark; repeated recovery is valid. RELEASE retains successful child ownership. Full commit/rollback/failed commit releases once after final outcome. Transaction attachment remains available for the next incarnation; CHAIN never reuses old lock-owner authority.

Release [xact.c, AbortCurrentTransaction/AbortSubTransaction](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/access/transam/xact.c) releases failed transaction/current-child resources at ERROR, not at a later client ROLLBACK or ROLLBACK TO. Failed-block SQL state can persist after locks/private effects have already been discarded.

Reset/Restore use TX/CATALOG replacement fencing: successful epoch replacement stops stale acquisition, wakes old-epoch requests with existing ordinary ErrCatalogReplaced (`stuntgres: catalog replaced before publication`), and discards fenced private effects through TX. Recovery to an old savepoint cannot revive the epoch. Failed replacement leaves all live state unchanged. Advisory resources are not epoch-qualified; session advisory and other targets survive. Retained immutable snapshots remain usable. This is not global unlocking or a new replacement protocol.

Dump captures immutable state and holds no target storage/SQL locks over writer I/O. Restore reads/parses/executes through existing private stage-local operation/executor authority; locks needed for its staged effects are stage-local, never live acquisitions spanning reader I/O. Existing effect admission rejects non-retractable live effects. Complete success replaces atomically; late error discards all private segments without touching live owners.

## 7. Automatic requests and public SQL hooks

Callers supply resolved qualified resources, precise modes, operation and scope. Scheduler performs no name resolution, privilege checks, binding, candidate selection, snapshot capture or isolation transition.

**Preparation lifecycle (user precedence, 2026-10-04: “Use PostgreSQL lifecycle”).** Pure syntax parsing is lock-free, but semantic preparation/Describe and reanalysis acquire required transaction-owned relation locks: ordinary SELECT references use ACCESS SHARE, locking SELECT references use ROW SHARE. No selected-row grants occur until execution. Admitted implicit preparation completes through Finish and releases its relation contributions; explicit transactions retain them until recovery/end. Statement objects own neither persistent locks nor live execution snapshots; snapshot-requiring analysis uses TX's conditional scoped view and releases temporary ownership at completion. Sources: [PREPARE Description](https://www.postgresql.org/docs/18/sql-prepare.html), [REL_18_0 prepare.c, PrepareQuery](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/commands/prepare.c), [parse_relation.c, addRangeTableEntry](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/parser/parse_relation.c). SQL PREPARE, protocol Parse and database/sql PrepareContext remain distinct surfaces; no wire-protocol semantics are added to this adapter.

| Consumer operation | Required requests/boundary |
|---|---|
| Ordinary SELECT | ACCESS SHARE on referenced relations; no row holds |
| SELECT locking clauses | ROW SHARE on locked relations, ACCESS SHARE on other references, then selected logical-row requests |
| INSERT/UPDATE/DELETE/MERGE | ROW EXCLUSIVE on target, ACCESS SHARE on other read relations |
| DELETE / FK-eligible unique-key-changing UPDATE | Row UPDATE on affected logical rows |
| Other UPDATE | Row NO KEY UPDATE |
| Nonconcurrent CREATE INDEX | SHARE |
| Concurrent CREATE INDEX/REINDEX; ANALYZE; non-FULL VACUUM | SHARE UPDATE EXCLUSIVE in operation-owned phases |
| CREATE TRIGGER / specified ALTER forms | SHARE ROW EXCLUSIVE |
| DROP/TRUNCATE/ordinary REINDEX/CLUSTER/VACUUM FULL / specified ALTER forms | ACCESS EXCLUSIVE |

Command owners supply the documented variant and lifecycle; there is no universal DDL mode. FK consumers use parent/key row locking and predecessor revalidation; index/unique consumers route actual relation, key/transaction contention through the same authority rather than separate name mutexes. Their feature-owned key/index identities and multi-phase protocols are extensions, not identity invented from row values in this artifact. Initial relation/row/advisory kinds are closed; any later contention-resource extension must preserve target qualification, lifecycle, graph and interruption rules. SSI/safe-snapshot dependencies are never such ordinary edges.

### Parser-owned future declarations

```go
// In internal/parser; these enums do not import internal/engine.
type LockTable struct {
    Targets []LockTarget
    Mode TableLockMode
    Nowait bool
    Pos Position
}
type LockTarget struct {
    Name ObjectName
    Only bool
    IncludeDescendants bool
    Pos Position
}
// Add Locking []RowLockClause to Select.
type RowLockClause struct {
    Mode RowLockMode
    Targets []Identifier
    Policy LockWaitPolicy
    Pos Position
}
```

Use CATALOG's exact Identifier/ObjectName declarations. Parser enums map explicitly to validated engine enums; ordinary numeric casting is not a contract. LockTarget normalizes descendant inclusion: ONLY means false; default or explicit `*` means true, with original positions retained. Targets is nonempty. An omitted OF list means all eligible query relations; malformed/missing target syntax is not a wildcard. OF identifiers refer to visible FROM alias/name entries resolved by existing query analysis, not a new registry.

LOCK defaults to ACCESS EXCLUSIVE; process explicit targets in SQL list order, not globally sorted identity order. Existing hierarchy/view providers expand descendants unless ONLY and recursive underlying view relations; catalog/authorization checks privileges with the view's security context. Outside an explicit transaction block return 25P01/`LOCK TABLE can only be used in transaction blocks`. A failed later target completes the scope and removes failed-subtransaction new contributions; earlier ancestor holds are retained. There is no UNLOCK TABLE. TX-LOCK-CORE owns the minimal AX path, later table consumers extend this AST and dispatcher.

LOCK privilege checks compose with AUTHORIZATION's coherent access view: table-level MAINTAIN, UPDATE, DELETE or TRUNCATE permits every mode; INSERT permits ACCESS SHARE/ROW SHARE/ROW EXCLUSIVE; SELECT permits ACCESS SHARE. Column-only SELECT/INSERT is insufficient for LOCK. Requested targets are kind/privilege checked before their requested lock; inherited descendants do not each require a separate ACL check. Recursive views use view-owner or security-invoker semantics from the existing authorization/view provider. Source: [LOCK Notes](https://www.postgresql.org/docs/18/sql-lock.html#SQL-LOCK-NOTES) and [REL_18_0 lockcmds.c, LockTableAclCheck/RangeVarCallbackForLockTable/LockTableRecurse](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/commands/lockcmds.c).

TX-ROW-LOCKS includes public grammar/execution for FOR UPDATE, FOR NO KEY UPDATE, FOR SHARE and FOR KEY SHARE. QUERY-LOCKING extends OF, repeated clauses, options and legal placement. Repeated applicable modes choose UPDATE > NO KEY UPDATE > SHARE > KEY SHARE; NOWAIT overrides SKIP LOCKED for the same target. Both options apply only to row attempts: required ROW SHARE still waits. Analysis owns restrictions involving DISTINCT, grouping, aggregates, set operations and relation eligibility. Preserve complete-input grammar and public one-statement rejection; update unsupported LOCK/FOR lexical classifications only in their owning implementations, generate the same parser normally, never dispatch raw SQL strings.

Locking SELECT needs SELECT on actually used columns plus table-level or any-live-column UPDATE on each locked relation, using AUTHORIZATION's operation-specific quantifier rather than redefining ordinary relation ACL masks. An outer locking clause does not propagate into WITH queries; they need their own clause. READ COMMITTED candidates initially matching the predicate may be locked but not returned after a concurrent update makes them nonmatching. Source: [SELECT Description/Locking Clause](https://www.postgresql.org/docs/18/sql-select.html#SQL-FOR-UPDATE-SHARE).

LIMIT counts returned locked rows; OFFSET-stepped rows are locked. Cursor fetch/step requests acquire incrementally through the same provider. After a wait, DML/query rechecks changed candidates using TX's predecessor/visibility rules: READ COMMITTED returns the matching updated row or omits changed/deleted nonmatching candidates. REPEATABLE READ/SERIALIZABLE changed predecessor fails 40001/`could not serialize access due to concurrent update`. The manager neither moves the snapshot frontier nor exposes peer-private rows. READ COMMITTED sort-key changes during waits may change returned ordering as documented; do not invent a portable stronger order guarantee.

## 8. Introspection and diagnostics

Snapshot returns a coherent owned copy under brief manager synchronization: valid empty target yields an empty slice; foreign target is an internal error. Blockers returns actual conflicting holders and earlier conflicting waiters, distinguishing hard from queued dependencies via Queued/Request. Valid nonwaiting session yields empty; foreign token errors. The SQL nonexistent-PID → empty integer-array rule belongs to PG18-FUNC-INFO-BLOCKING-PIDS, not relaxed internal reference validation. Callers cannot mutate manager state through returned slices. No queue order is promised by the public view.

PG18-VIEW-LOCKS projects cluster-wide public lock state, not a blanket ACL-filtered subset; joining relation names still respects database/catalog identity. It owns aggregation and predicate-source merging. Internal Snapshot is a coherent manager-only copy, not a promise that PostgreSQL's mixed fast-path/main/predicate public view is globally atomic. Granted/waiting/blocker relationships must nevertheless be truthful. Generated PID/OID/XID relationships are checked, not sequence-matched. SSI and safe-snapshot waits stay outside the ordinary graph.

Public projection requires a feature-owned extension joining this authority to SESSION backend identity and TX logical virtual/real transaction IDs: expose transaction VXID/XID lifetimes and their completion holds, actual transaction-ID wait dependencies and backend association. Each wait has one current public wait record; multiple row holders remain actual internal blockers, not invented simultaneous public waits. This metadata is not a new conflict authority or physical allocator. An enduring logical row hold is not one `pg_locks` tuple row: PostgreSQL row contention is normally observed as transaction-ID waiting. Never derive page/tuple/ctid from RowRef or invent fast-path/physical tuple state. VIEW-LOCKS owns exact column/NULL projection and transient row-wait metadata with TX; any tuple projection must first provide a separately grounded logical analogue, never fabricated coordinates.

Projection sources: [pg_locks](https://www.postgresql.org/docs/18/view-pg-locks.html) and [REL_18_0 lmgr.c, XactLockTableInsert/XactLockTableWait](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/storage/lmgr/lmgr.c). Public rows aggregate repeated acquisitions; waitstart can briefly be NULL. Advisory bigint projects high/low 32-bit classid/objid with objsubid=1; integer-pair projects the two bit patterns with objsubid=2, both database-qualified.

Reuse the already complete `internal/sqlstate.Error` and root `Error` alias; PG18-DRIVER-ERROR-FIELDS owns diagnostic transport, not a new locking error type. Both `sqlError` and `annotate` in `engine.go` attach `Schema=public` when a table is supplied; generic locking failures must bypass that annotation. Preserve `SeverityUnlocalized=ERROR` alongside Severity.

| Origin | Severity / Code | Exact English primary and additional fields |
|---|---|---|
| Deadlock | ERROR / 40P01 | `deadlock detected`; Hint=`See server log for query details.`; Detail describes actual cycle edges with generated process/resource identities |
| Relation NOWAIT, unqualified t | ERROR / 55P03 | `could not obtain lock on relation "t"` |
| Relation NOWAIT, explicit public.t | ERROR / 55P03 | `could not obtain lock on relation "public.t"` |
| Row NOWAIT | ERROR / 55P03 | `could not obtain lock on row in relation "t"` |
| Lock timeout | ERROR / 55P03 | `canceling statement due to lock timeout` |
| Statement timeout | ERROR / 57014 | `canceling statement due to statement timeout` |
| SQL query cancellation | ERROR / 57014 | `canceling statement due to user request` |
| LOCK outside block | ERROR / 25P01 | `LOCK TABLE can only be used in transaction blocks` |
| Changed repeatable predecessor | ERROR / 40001 | `could not serialize access due to concurrent update` |

REL_18_0 diagnostic construction anchors: [deadlock.c:1100–1140](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/storage/lmgr/deadlock.c#L1100-L1140), [namespace.c:589–614](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/catalog/namespace.c#L589-L614), [heapam.c:5260–5290](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/access/heap/heapam.c#L5260-L5290), [postgres.c:3410–3465](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/tcop/postgres.c#L3410-L3465). These are source-grounded expectations, not executed reference results.

Direct NOWAIT/timeout origins supply no fabricated Detail/Hint, Schema/Table/Column or Position. Naming a relation in Message does not populate object fields. Deadlock Detail uses actual captured graph edges; do not pin generated IDs or fake unrelated SQL text. Preserve originating procedural Context/internal-query fields and catalog/authorization diagnostics; optional automatic/constraint contexts require bounded consumer runtime capture, not universal-empty assertions. Parser `Position.Character` already supplies one-based character positions (`Offset` is a byte offset); retain it when the origin supplies a position, never invent one in the manager. No fake PostgreSQL File/Line/Routine. Ordinary Go context, I/O, adapter and internal errors remain ordinary errors; SQL signaling remains its consumer's responsibility.

## 9. Repository seams and clean cutover

Inspected anchors refer to this worktree's current baseline, not implemented future interfaces:

| Existing seam | Required owning-consumer cutover |
|---|---|
| driver.go:14–58, connector.Connect | Preserve Error alias/exact opaque DSN Engine sharing; consume SESSION physical identity and target |
| conn.go:17–116, Begin/BeginTx/Close/PrepareContext/execute/ExecContext/QueryContext | Replace unsupported transaction route in TX owner; use admitted pointer Operation and StatementScope; execute requests with exact owner; Close delegates SESSION, never ad hoc unlock-all |
| stmt.go:11–48, ExecContext/QueryContext | Keep conn.execute convergence; execution acquires selected-row grants and required relation locks through the same authority. Prepare/Describe uses §7's relation-lock lifecycle; stmt.Close releases its handle, never transaction holds or a stmt-owned snapshot |
| internal/engine/engine.go, Engine/Describe/describe/Execute; catalog.go, PublishCatalog/ReplaceCatalog | Extend current descriptor catalog (`state`, `providers`) and `storage.Store.Relations` (`map[ObjectAddress]*storage.Relation`), not the obsolete table-name map. Reuse prepareArgs, binder/coercion and atomic staging; resolve under short synchronization, acquire outside it, then revalidate; retain memory/publication safety |
| internal/engine/snapshot.go:9–29,61–90; rows.go:12–43 | Preserve immutable captured references, projection/filter/order and nil-selection optimization; RowRef is not an index; future pin Close is not transaction-lock release; ordinary reads do not wait on row holds |
| internal/engine/control.go, Reset/Dump/Restore/stage.Execute; root control.go, withEngine | Extend existing ReplaceCatalog/effect lifecycle; preserve release of borrowed connection and no target lock across external I/O; never sweep peer/session owners |
| internal/parser/ast.go, Statement/Select/Position/Identifier/ObjectName; grammar.y, statement/select/relation_name; lexer.go, unsupportedWords/unsupportedIdentifiers | Extend existing positioned structured names and closed statement/SELECT syntax; retain whole-input parsing; LOCK/FOR remain unsupported until their owner implements them |
| internal/sqlstate/error.go, Error; driver.go, Error alias; reference/fixture/fixture.go, fixture.Diagnostic/fixture.AssertDiagnostic | Complete structured fields and diagnostic assertions already exist; add exact locking fixtures through this sole seam, not another error family |

Before implementation, inventory Go callers using `\.(Describe|Execute)\(`, `e\.mu|e\.state|e\.storage|storage\.rows`, `BeginTx|withEngine`, and `Snapshot\{`; classify receiver ownership rather than mechanically editing unrelated methods. Known direct callers are `conn.PrepareContext`/`execute`, private `Restore` stage execution, `engine_test.go` and `catalog_test.go`; prepared `stmt` methods converge on `conn.execute`. Migrate these with SESSION/TX admission and owning unit/reference/control/smoke fixtures. Remove obsolete context-only engine signatures and unsupported transaction paths in their owning consumers; no aliases, shims, parallel executor or alternate binder/store. All code conflicts remain deferred in the shared `todo-conflict.md`, not fixed by this document.

## 10. Session-sized consumer breakdown

Each row is a bounded consumer slice with public behavior and independent witnesses, not a request to implement it here. Larger feature records extend the same manager in successive slices; absence of those implementations does not reopen the identity/lifetime decisions.

| Existing backlog symbols | Bounded responsibility / witnesses |
|---|---|
| PG18-TX-LOCK-CORE with PG18-DRIVER-SESSION / PG18-TX-CORE | Shared AS/RX/AX foundation; SELECT/INSERT automatic requests and minimal explicit AX; wait/cancel/end; W2/W4/W10 |
| PG18-TX-LOCK-WAITS; PG18-ADMIN-SETTINGS-TIMEOUT | Same queue/conversion/NOWAIT/cancellation authority; per-attempt deadlines, settings/statement timer configuration remains ADMIN owner; W2/W4/W6 |
| PG18-TX-LOCK-DEADLOCKS | Hard/soft graph, compatible reorder, cycle diagnostics and victim recovery; W1/W5 |
| PG18-TX-SAVEPOINTS | Participant boundary/restore/merge integration and failed-subtransaction cleanup; W3 |
| PG18-TX-LOCK-MODES; PG18-TX-LOCK-CONTROL | Sole eight-mode matrix and AST extensions, then catalog/privilege command dispatch; W4/W7 |
| PG18-TX-ROW-LOCKS; PG18-QUERY-LOCKING | Four modes plus public basic grammar/execution; then OF/repeated clauses/NOWAIT/SKIP/query placement and cursor interactions; W4/W8 |
| PG18-DML-UPDATE-CORE; PG18-DML-DELETE-CORE; PG18-DDL-TABLE-CORE; PG18-DDL-FOREIGN-KEY | Automatic requests, predecessor recheck and integrity; W8/W9 |
| PG18-INDEX-BTREE; PG18-INDEX-UNIQUE; PG18-INDEX-CONCURRENT; PG18-INDEX-MAINTENANCE | Operation-specific relation/key/transaction contention and phases, no separate conflict mechanism; W9 |
| PG18-TX-ADVISORY-SESSION; PG18-TX-ADVISORY-XACT | Disjoint key domains/counts/lifetimes using SESSION token and same manager; W10 |
| PG18-VIEW-LOCKS; PG18-FUNC-INFO-BLOCKING-PIDS | Actual state, hard/soft blockers and public identity/privilege mapping; W11 |
| PG18-DRIVER-ERROR-FIELDS; PG18-ADMIN-SESSION-SIGNAL | Sole diagnostic transport/field exposure and explicit SQL interruption; W1/W2/W4/W6 |
| PG18-ARCH-SERIALIZABILITY; PG18-TX-SERIALIZABLE | Independent nonblocking SSI/predicate/safe-snapshot authority; common TX visibility only, no added blocking-lock prerequisite |

Consumer symbols are catalog capability boundaries; selection and status are accessed only through the catalog utility. This design neither modifies catalog definitions/status nor claims these downstream implementations exist.

## 11. Independently executable future scenarios

These are future specifications, not passing-test or runtime-smoke claims. Future units use deterministic barriers around manager transitions and inspect actual state. Public/reference cases use dedicated sql.Conn A/B plus observer C in the serialized pinned 18.0 fixture, with bounded context deadlines solely as test failure bounds. Poll actual pg_locks/pg_blocking_pids or internal state to establish a queued request; arbitrary sleeps do not prove blocking. Assert each independently expected result before parity. Exercise direct and prepared execution where the surface permits it; prepared SQL uses the same dispatcher. Preserve exact rows/types/counts and English diagnostics/conditional field presence. Close rows/statements, end transactions and release dedicated connections before schema reset. Queries without ORDER BY are bags. Enabled oracle/Docker/version failure remains visible, never skipped or mocked.

### W1 — Acceptance 1: opposing-order deadlock

Setup `CREATE TABLE a(v INT); CREATE TABLE b(v INT);`. A and B BEGIN. A `LOCK TABLE a IN ACCESS EXCLUSIVE MODE`; B locks b likewise. Each inserts a private row into its held table (affected=1). Start A's lock of b asynchronously and observe it queued; then B requests a. Exactly one request returns ERROR/40P01 with §8 primary, hint and actual cycle Detail; do not assert victim identity. Await automatic failed-scope recovery, let the successful participant COMMIT and full ROLLBACK the victim. Only survivor's private row is committed; victim row is absent. A healthy third transaction can acquire both AX locks. Repeat opposing logical rows in the row consumer. Units assert one victim terminal completion per resolved cycle and no stale holds/edges after full end.

### W2 — Acceptance 2: prompt cancellation without leaked ownership

A BEGIN and AX-locks t. Start B SELECT or INSERT with cancellable context; observe B queued, then cancel. B promptly returns ordinary context.Canceled (deadline variant context.DeadlineExceeded), with no provisional grant/queue entry; A is unchanged. Finish B's failure recovery. Release A and use healthy C to complete the formerly conflicting request. Units exercise cancellation before enqueue, while queued, racing transfer, and physical end: exactly one terminal outcome, stopped registration and no late grant. SQL user-cancel is an independent reference variant expecting 57014/user-request primary, not a SQLSTATE manufactured on Go errors.

### W3 — Acceptance 3: recovery removes only later contributions

A BEGIN; AX-lock a; SAVEPOINT s; AX-lock b; ROLLBACK TO s. B BEGIN and AX-locks b successfully while A remains active. B's NOWAIT AX attempt on a fails 55P03 with exact relation primary; recover/end B. A full rollback permits a new AX acquisition. Repeat with a failed/canceled post-s request and recover again; no new holds leak, earlier a remains. RELEASE s instead retains b until parent end. Row variant: A FOR UPDATE before s, later update/upgrade after s, ROLLBACK TO s; peer still cannot lock that row until full end. Unit restore compares exact pre-mark mode sets/counts, including upgrades/reentry and shadowed/repeated savepoint boundaries; never delete a whole resource entry to simulate restoration.

Also observe cleanup **before** client recovery: top-level ERROR releases its prior transaction holds while ordinary SQL remains 25P02; child ERROR releases b before ROLLBACK TO but preserves ancestor a. Genuine row-upgrade variant: KEY SHARE before s, UPDATE after s, rollback restores KEY SHARE; a peer KEY SHARE succeeds while peer UPDATE still conflicts. Earlier FOR UPDATE remains separately preserved by the existing witness.

### W4 — Acceptance 4: every compatibility pair and NOWAIT

Independently enumerate all 64 ordered table pairs and 16 row pairs from §3. A holds first mode; B requests second on the same resource. Compatible B completes while A remains holder; incompatible B is observed queued and completes only after A commit/rollback. For row pairs use the four SELECT clauses over one row; relation ROW SHARE holds remain mutually compatible. Test self-transaction reentry, distinct logical rows and ordinary SELECT despite row holds. AS SELECT/RX INSERT coexist; AX blocks both. Repeat incompatible attempts with NOWAIT: relation t → 55P03/`could not obtain lock on relation "t"`; qualified public.t → qualified primary; row t → row primary. Assert direct-origin field absence as §8, immediate terminal result, no B provisional state and retained A. Units validate symmetry independently, not by comparing the same production matrix to itself.

Preparation witnesses use admitted PrepareContext/Describe, separately from SQL PREPARE: peer AX blocks semantic preparation before execution; canceling preparation promptly cleans its new contributions. Implicit preparation finishes/releases so a later peer AX succeeds; an explicit transaction's ordinary prepared SELECT retains AS until end, or until rollback to a pre-prepare savepoint removes it. Preparing locking SELECT obtains RS but no row grant: peer row UPDATE remains possible after preparation alone. Failed preparation preserves ancestor holds but leaks no child contributions. Closing the prepared handle never unlocks retained transaction relation holds.

### W5 — Acceptance 5: no false deadlock

A and B lock separate relations/rows and both COMMIT successfully. Separately hold one resource in A while B waits acyclically beyond its deadlock-check interval, then release A; B succeeds without 40P01. Unit cases include compatible cohorts, reentry/conversion behind a waiter blocked by the owner's original hold, avoidable soft cycles, and cancellation of a queued blocker waking successors. Detection interval expiration alone must never abort a request.

### W6 — Per-attempt lock and whole-statement deadlines

A holds t; B sets lock_timeout shorter than statement_timeout and attempts a conflict. Expect 55P03/lock-timeout primary, no pending contribution, A unaffected. Reverse relative durations: 57014/statement-timeout primary. Zero lock_timeout disables only lock timeout; fresh attempts restart wait interval. Units exercise stale timers, deadline expiration versus grant, both-fired earlier-deadline choice and equal-deadline lock precedence. Recover failed B scopes before subsequent attempts.

### W7 — Explicit LOCK boundaries and list order

Default `LOCK TABLE t` means AX and blocks readers/writers. Outside an explicit block it returns 25P01/exact §8 primary. With a and b held by peers, `LOCK TABLE a, b` is observed first waiting on a, not a sorted/reordered explicit list. Duplicate target reentry succeeds. ONLY, descendants and recursive views use existing identity/security providers. Missing object, privilege and wrong-kind failures preserve catalog/ROLE diagnostics. Failure on a second target removes first-target contributions of that failed subtransaction, not pre-savepoint holds.

Unprivileged-role witnesses independently exercise all-mode MAINTAIN/UPDATE/DELETE/TRUNCATE alternatives, INSERT-only AS/RS/RX success versus SUE denial, SELECT-only AS success versus RS denial, and column-only SELECT insufficient for LOCK AS. Parent privilege permits inheritance recursion without child ACL; view security context remains explicit. Owner status alone is not an ordinary-use ACL fallback after privileges are revoked.

### W8 — Query locking and changed predecessor

Setup `t(id INT, v INT)` with (1,10),(2,20). A locks id=1. B `SELECT id,v FROM t ORDER BY id FOR UPDATE SKIP LOCKED` returns exactly (2,20), INT4 metadata and int64 values; LIMIT 1 counts that returned row. Independently prove OFFSET-stepped row locking. NOWAIT on id=1 gives §8 row error. Against peer AX, even row NOWAIT/SKIP LOCKED waits for required ROW SHARE. Include OF visible alias/name resolution, repeated strongest mode/option precedence and query restrictions. For recheck, B queues locking `WHERE v=10`; A changes v and commits: READ COMMITTED returns updated matching values or no row if predicate fails/deleted. Repeatable changed predecessor returns 40001/exact concurrent-update primary. Sort-key-change witness does not assert stronger ordering than the manual.

With used-column SELECT and only column UPDATE, locking SELECT is authorized; without UPDATE it fails the originating ACL check. An outer clause leaves WITH relations unlocked unless their query has a clause. For a changed nonmatching candidate, independently show peer locking still conflicts after B omits it from returned rows.

### W9 — Automatic DML/index/constraint integrity

Non-key UPDATE permits KEY SHARE; FK-eligible unique-key-changing UPDATE and DELETE block it; different row remains independent. Nonconcurrent CREATE INDEX SHARE blocks writers; concurrent provider phases use SUE as documented. FK parent-delete/child-insert interleaving cannot both commit an invalid relation; unique-key contenders cannot both commit duplicate keys. Each consumer supplies exact winning rows/affected counts and its independently sourced constraint diagnostic, rather than parity alone. Failed statements publish no partial effects. These are downstream witnesses, not algorithms or SQL implementation here.

### W10 — Qualified ownership, pooling and native epochs

Same exact DSN pooled physical sessions arbitrate with distinct qualified owners; independent DSN/Driver/target domains do not conflict. Session advisory counted holds survive borrower return and transaction rollback; xact advisory holds do not survive transaction end. Matching unlock decrements only session counts; unmatched unlock returns false. Cross-session same-key session/xact requests conflict; same-session reentry succeeds; int64/pair domains are disjoint. Physical end releases exact owner only, after drain/TX cleanup. Rename preserves relation identity; drop/recreate/replacement fences stale refs. Failed replacement preserves live state; success preserves session advisory, other targets and retained immutable snapshots while rejecting old-epoch private publication. Units cover stale token/epoch/end/cancel races; no GC deadline claim.

### W11 — Actual introspection

Observer C sees A granted/B waiting with real resource/mode/owner relationships and actual hard/soft blockers. Cancel B: it disappears. Release A: successor becomes granted; end removes holds. Validate generated identity relationships rather than allocator sequence parity. Public row-wait mapping must not invent page/tuple from logical RowRef. Separate SIRead state remains nonblocking and safe-snapshot waiters are absent from the ordinary graph.

Inspect valid logical VXID/XID exclusive completion holders and actual transaction ShareLock waits, current-database relation-name joins and cluster-wide visibility independent of table DML rights. Reentry aggregates one owner/resource/mode row, not one row per count; advisory key formats use exact §8 fields. Transient tuple locks can exist in PostgreSQL, but no enduring tuple row is required for every internal RowRef hold and none is fabricated.

## 12. Delivery boundary

This design specifies future interfaces and independently expected outcomes, not observed lock behavior. Executable SESSION/TX/locking foundations remain downstream work; documenting them does not authorize implementing them. SSI remains separate. Optional runtime diagnostic contexts require consumer capture. Design-only work has no applicable locking feature smoke command; coordinator verification and independent review cover this artifact and its deferred-conflict report.

**Approved supersession:** the 2026-10-04 user decision “Use PostgreSQL lifecycle” replaces only the former §9 blanket “locks at execution, not AST preparation; prepare retains no lock/snapshot authority.” Semantic analysis/Describe may acquire required transaction-owned relation locks and conditional TX analysis views; implicit scopes finish/release, explicit transactions retain until recovery/end. Statement objects still own no persistent lock/live snapshot; row grants remain execution-time. The living plan and conflict report preserve the prior rule, authorization and deferred baseline steps; no production behavior is claimed fixed.
