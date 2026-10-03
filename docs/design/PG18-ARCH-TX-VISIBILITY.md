# PG18-ARCH-TX-VISIBILITY: Transaction ownership, visibility, and publication

## 1. Status, scope and acceptance

This is a consumer contract, not a transaction implementation or evidence of running SQL. All interfaces below are proposed future `internal/engine` contracts. No executable Session, TransactionOwner, version store or transaction AST exists in the inspected baseline. The accepted SESSION artifact, including its pointer-owned Operation correction, is authoritative. CATALOG-IDENTITY supplies the existing design authority for descriptors, epochs and publication; neither prerequisite is reimplemented here.

Selected scope: transaction/session ownership; autocommit, explicit and aborted transaction states; savepoints; row/version identity; statement/transaction snapshots and private writes; atomic commit/rollback and transactional DDL; cancellation/termination/abandonment; native replacement and retained results/cursors. Blocking lock scheduling belongs to PG18-ARCH-LOCKING. Nonblocking SSI detection belongs to PG18-ARCH-SERIALIZABILITY and its implementation consumer. No physical durability, PostgreSQL storage emulation, wire server, retry policy or adjacent implementation is introduced.

### Literal catalog acceptance

> The future artifact at `docs/design/PG18-ARCH-TX-VISIBILITY.md` is consumer-ready: it states ownership and visibility invariants; defines concrete interfaces and data ownership for transaction context, snapshots, staged writes, publication, rollback, savepoints, cancellation/termination, and Reset/Restore/cursor boundaries; specifies state transitions including failed-statement aborted state and rollback-to-savepoint recovery; names clean-cutover callsites and integration points; and provides a session-sized implementation breakdown mapped to backlog symbols without implementing them. Include independently specified positive, failure, and interleaving scenarios, at minimum the representative visibility witness: session A runs `BEGIN; INSERT INTO t VALUES (1);` and can read its uncommitted row, while peer session B cannot see it; A then rolls back and neither session sees it. Also specify a successful commit becoming visible to B, statement failure/autocommit rollback, explicit transaction abort-state recovery via savepoint, connection abandonment cleanup, and behavior of retained snapshots/cursors across commit/rollback and Reset/Restore. Scenarios are design specifications, not executed behavior. State the acceptance SQLSTATE and PostgreSQL error-message/error-field expectations for defined failure cases (including transaction-aborted and serialization-facing boundaries), without specifying SSI detection. The design review can derive implementation consumers without reopening architecture questions; it does not claim running SQL behavior or passing tests.

| Selected work item | Decision sections | Independent witnesses |
|---|---|---|
| 1: ownership/context reaches execution | 2, 3, 9 | W1, W5, W9 |
| 2: transaction/control/cleanup transitions | 3, 4, 7 | W3–W5, W9 |
| 3: identity, views and private writes | 5, 6 | W1, W2, W6, W7 |
| 4: DDL and native publication | 6, 8 | W8, W11 |
| 5: retained results, cursors, abandonment | 5, 7, 8 | W9–W11 |
| 6: interfaces, errors and consumer cutover | 2–9, 11 | W1–W11 |

## 2. Ownership and invariants

| Owner | Authority and lifetime |
|---|---|
| Driver | Exact opaque DSN to Engine map, unchanged; independent Drivers isolate Engines. Closing pools does not delete committed data. |
| DSN Engine / selected DatabaseTarget | One authoritative committed catalog/storage, database-local commit frontier, epoch and identity allocators. No transaction-owned alternate database. |
| Physical Session | One persistent Transaction resource delegate. It remains attached while idle and across transaction incarnations. SessionToken and DatabaseTarget, not a numeric ID alone, qualify authority. |
| TransactionOwner | Current transaction incarnation/options/state; private successful statement overlays; savepoint stack; transaction snapshot pin; registered participant/observer lifecycle. |
| StatementScope | One command's immutable view and mutable write stage; mandatory completion before releasing its Operation. |
| VisibilityView / Snapshot / portal | Immutable retained read lifetime, not permission to commit. Private-write result retention never publishes rolled-back writes. |
| Feature participant | Actual settings/temp/portal/provider state; TX coordinates marks/end, not a competing feature store. |

A physical session has at most one current transaction incarnation. Operation admission does not imply concurrent mutation of that owner: transaction entrypoints serialize per-session statement/control state changes, without defining cross-session lock scheduling. A TxRef cannot authorize another Engine, target or session. Publication either installs the entire validated batch or installs nothing. Peers cannot observe private rows, descriptors, edges or intermediate commit state. Immutable published payloads are never changed in place. Unrelated committed changes must not be lost by installing an old whole-database copy.

Ending a transaction releases transaction resources, not the Transaction attachment. Physical session end first drains Operations, then ends the Transaction delegate before Cursors, PreparedSQL, Temporary, AdvisorySession, Settings and Authorization, as SESSION requires. Persistent data and peer resources survive.

```go
type TxID uint64
type CommandID uint64
type CommitSeq uint64
type TxState uint8 // TxIdle, TxImplicit, TxActive, TxAborted, TxEnding
type Isolation uint8 // ReadCommitted, RepeatableRead, Serializable
type TxOptions struct { Isolation Isolation; ReadOnly, Deferrable bool }
type TxRef struct { owner *Engine; target DatabaseTarget; session SessionToken; id TxID }
type SavepointRef struct { tx TxRef; serial uint64 }
type EndResult struct { Committed bool; Next TxRef }
type TransactionOwner interface {
    TransactionAttachment
    State() TxState
    Begin(op *Operation, options TxOptions) (TxRef, error)
    StartStatement(op *Operation) (*StatementScope, error)
    Commit(op *Operation, tx TxRef, chain bool) (EndResult, error)
    Rollback(op *Operation, tx TxRef, chain bool) (EndResult, error)
    Savepoint(op *Operation, name string) (SavepointRef, error)
    RollbackTo(op *Operation, name string) error
    Release(op *Operation, name string) error
}
func (op *Operation) Transactions() TransactionOwner
func (scope *StatementScope) View() *VisibilityView
func (scope *StatementScope) Writes() *WriteStage
func (scope *StatementScope) Finish(err error) error
```

Refs are immutable. TxIDs and savepoint serials are nonzero and never reused within their owning domain; zero EndResult.Next means Idle. Command/commit/identity counters detect exhaustion before mutation, never wrap. Nil, foreign or stale internal references fail ordinary internal Go errors without mutating any transaction. Internal state errors are not fabricated SQLSTATEs. The root adapter's transaction handle owns `conn *conn`, `ref engine.TxRef`, and `done atomic.Bool`; it implements driver.Tx separately from conn. Stale public database/sql handles yield `sql.ErrTxDone`; nested driver BeginTx is rejected at the adapter boundary. SQL CHAIN fences the old handle by incarnation, even if its local done bit was not set by that SQL call.

Options are validated before Begin changes state. Unsupported options fail rather than silently weakening isolation/read-only/deferrable behavior. Defaults resolve from the session settings provider. READ UNCOMMITTED maps to READ COMMITTED when the isolation consumer enables it. TX-CORE enables only its bounded implemented option set; later consumers expand that set.

## 3. Operation admission and statement protocol

Use SESSION `Acquire(ctx) (*Operation, error)` and pass that same noncopied lease to Engine.Describe/Execute. Its context combines caller cancellation with terminal cancellation, never connector dialing context. The immutable ResolutionContext and selected target are supplied by the operation. The view's CatalogRead is the authority used for binding as well as data reads.

Direct execution and preparation acquire before parsing/analysis so admitted syntax, binding and execution SQL errors participate in transaction error handling. Parsing still consumes the whole input through the existing parser. A provisional statement scope owns failure cleanup before AST classification: a successfully parsed control takes the control route without capturing a data frontier; a non-control route captures its view before catalog analysis. Parse failure completes the provisional scope with the original syntax error. Do not let abort gating replace a syntax diagnostic. For a valid AST, aborted-state gating precedes catalog analysis.
Prepare uses the same admission/Describe protocol; it retains AST/count and no snapshot, private stage, or transaction snapshot pin. Describe is metadata analysis, not execution of a non-control statement for snapshot timing. A later prepared execution acquires a new operation and uses the then-current transaction/view; it never reuses a preparation view. Direct and prepared requests share conn.execute and the same binder/coercion path. Public requests remain one statement per call; Restore alone owns scripts. Empty SQL with no arguments performs no transaction work. Empty SQL with arguments and adapter argument errors rejected before admission stay ordinary Go errors and do not abort. Argument/coercion SQL errors reached after admission follow the statement failure rule.

Finish is mandatory, single-effect and idempotent; repeat calls return the stored completion result rather than merging twice. Success freezes/merges the stage, advances command order, and publishes only if implicit. Failure discards the statement stage and applies section 4. Every path releases scope-owned views/stages and then calls Operation.Done. Results that escape first obtain a separate retained pin. Pre-canceled admission changes neither state nor command order. Describe/execution failures preserve their originating diagnostics.

## 4. State machine and savepoints

| Current state / event | Next state and effects |
|---|---|
| Idle, non-control statement | Implicit with fresh incarnation; success validates/publishes once then Idle; failure or admitted cancellation discards then Idle. |
| Idle, SQL BEGIN/START TRANSACTION or driver BeginTx | Active with fresh TxRef/options. BEGIN captures no repeatable snapshot. |
| Active, redundant SQL BEGIN | Active unchanged; WARNING 25001. This is not nested BeginTx. |
| Active, successful statement | Active; merge private stage and advance command order. No peer publication. |
| Active, failed statement | Aborted; discard failed stage and abort its subtransaction. Preserve recoverable savepoint ancestors. Without a recoverable mark, discard all ordinary private effects. |
| Aborted, SQL BEGIN/START TRANSACTION | Aborted unchanged; ERROR 25P02 without effects. Driver BeginTx remains rejected at the adapter boundary as specified in section 2. |
| Aborted, valid ordinary command/SAVEPOINT/RELEASE | Aborted unchanged; ERROR 25P02 without effects. |
| Aborted, ROLLBACK TO existing mark | Active; discard target subtransaction and descendants, restore all provider/row/catalog marks; retain target as a fresh subtransaction. |
| Active/Aborted, ROLLBACK TO missing mark | ERROR 3B001; failed control follows abort rules; already Aborted stays Aborted. |
| Active, COMMIT | Ending; validate/preflight, indivisible publication, then Idle. Preparation/validation failure installs nothing, fully rolls back, ends and returns original error. |
| Aborted, COMMIT | Ending then Idle by rollback; no publication and no successful-commit event. |
| Active/Aborted, full ROLLBACK | Ending then Idle; discard private state, release marks/pins/resources. |
| Successful end with AND CHAIN | Fresh healthy Active incarnation retaining characteristics. COMMIT of aborted work chains after rollback. AND NO CHAIN/ordinary end leaves Idle. |
| Idle, ordinary COMMIT/ROLLBACK | Idle; WARNING 25P01, no transaction created. |
| Idle, COMMIT/ROLLBACK AND CHAIN | Idle; ERROR 25P01, no transaction created. |
| Physical terminal end | SESSION stops admission/cancels/drains; delegate discards current work once and releases TX resources. Session becomes Ended, not a reusable Idle session. |

Ending excludes further mutation of that incarnation. Full end clears savepoints, statement stages, snapshot state and transaction-scoped resources. Validation failures during explicit commit do not leave a usable old transaction. No automatic retries. Cancellation of an admitted explicit statement aborts its current subtransaction, not the healthy physical session. A native epoch fence is stronger than a savepoint failure: discard all private state and require full end; rollback-to cannot revive the replaced epoch.

SAVEPOINT requires explicit Active. Each mark owns immutable row/catalog overlay roots, command boundary and provider-specific marks. Newest equal name shadows older marks. ROLLBACK TO selects newest matching mark, removes younger marks, restores the selected boundary and retains that name with fresh provider/subtransaction marks. Repeating rollback-to is valid. RELEASE selects newest matching mark, removes it and all younger marks, retains their successful work in the parent, releases obsolete marks, and reveals an older equal name. Released child writes then belong to the parent and are discarded if that parent later aborts. Savepoints are not nested BEGIN. Prepare all coordinated marks before pushing; a partial Mark failure releases acquired marks and aborts the statement, never exposes a half-savepoint. Restore failure forces full abort; never claim partial recovery.

## 5. Logical versions, views and retained reads

Use immutable logical row versions and a database-local commit frontier, not in-place undo and not a second store. These identities do not prescribe PostgreSQL physical XID/ctid or SQL system-column values.

```go
type RowID uint64
type VersionID uint64
type RowRef struct { target DatabaseTarget; relation ObjectRef; id RowID }
type VersionRef struct { row RowRef; id VersionID }
type WriteKind uint8 // InsertVersion, UpdateVersion, DeleteVersion
type VersionWrite struct {
    Kind WriteKind
    Row RowRef
    Expected VersionRef // zero only for insert
    New VersionRef // zero only for delete
    Values []driver.Value // owned immutable payload; nil for delete
}
func (v *VisibilityView) Tx() TxRef
func (v *VisibilityView) Command() CommandID
func (v *VisibilityView) Frontier() CommitSeq
func (v *VisibilityView) Catalog() CatalogRead
func (v *VisibilityView) Scan(relation ObjectRef) (*VersionScan, error)
func (s *VersionScan) Next() (VersionRef, []driver.Value, bool, error)
func (s *VersionScan) Close()
func (w *WriteStage) Apply(change VersionWrite) error
func (w *WriteStage) Catalog() *CatalogStage
func (v *VisibilityView) Retain() *VisibilityView
func (v *VisibilityView) Release()
```

RowRef includes Engine/target/epoch/relation authority through DatabaseTarget and ObjectRef. Distinct duplicate-valued rows have distinct RowIDs. Update preserves RowRef and creates a new VersionRef; delete stages an end/tombstone without mutating the predecessor. New IDs are never reused in their domain; rollback gaps are acceptable. Apply validates identity, kind/payload/predecessor consistency before transfer. Success owns the supplied payload, which callers must not mutate; failure leaves it caller-owned. Borrowed scan payloads are immutable for the scan/view lifetime. Mutable feature types must provide immutable owned representations rather than sharing caller buffers.

A committed version is visible if born at or before the captured frontier and not ended at or before it. Overlay successful earlier commands from the owner; own replacement/deletion hides the predecessor. Ordinary statement reads do not see writes staged by that same command until successful Finish. Feature-specific RETURNING/conflict processing may inspect its stage explicitly, not silently alter this read view.

READ COMMITTED captures a fresh coherent row/catalog frontier for each non-control statement; peer commits after capture cannot enter the running statement. REPEATABLE READ and SERIALIZABLE pin the first non-control statement frontier until full end, adding successful prior own writes. BEGIN alone does not capture it. Savepoint recovery restores private roots, not the transaction's isolation frontier. DML candidate reads use this view. READ COMMITTED changed-predecessor waiting/recheck belongs to DML/LOCKING; repeatable-read conflicting changed predecessors yield the serialization boundary in section 11. This is not an SSI algorithm.

Statement stages progress Open → Merged or Discarded; merged stages cannot be reapplied. Savepoints share immutable roots without rewriting published payloads. Retain creates a distinct pointer-owned release handle to the same frozen view; Release/scan Close/Snapshot.Close are idempotent per handle. Releasing one handle cannot invalidate another pin. Reading a released handle is an internal lifecycle error. Reclamation waits for every transaction view, scan, Snapshot, portal and provider pin referencing a version/epoch.

Keep Snapshot.Len/Values/projection/filter/order and its nil-selection allocation optimization. Capture immutable visible rows under the existing synchronization boundary, then filter/sort outside it. Snapshot construction/filter errors release their pin. rows.Close calls Snapshot.Close before dropping it. Engine snapshots, including own-write results, remain immutable until closed across commit, rollback, Reset and Restore; retention is not publication. database/sql may automatically close Tx-owned Rows when the Tx ends, so public Tx Rows usability after end is not promised. Anonymous Rows are not named cursors. Forgotten pins have best-effort cleanup, no deadline.

## 6. Atomic row/catalog/provider publication

Reuse CATALOG section 7 CatalogStage, PreparedCatalogPublication, PublicationToken, ChangeSet, ValidateCatalogStage, PublishCatalog, DiscardCatalogStage and ReplaceCatalog. CatalogStage is the private overlay of the same descriptor authority. TX selects its base view, savepoint attachment and end timing. There is one combined Engine publication boundary, not sequential row then descriptor installation.

```go
func PrepareTransaction(ctx context.Context, tx TxRef) (*PreparedTransaction, error)
func PublishTransaction(op *Operation, prepared *PreparedTransaction) (ChangeSet, error)
func DiscardTransaction(tx TxRef)
```

PreparedTransaction owns frozen row changes, prepared catalog batch, prepared provider actions, epoch token, touched row predecessors, catalog revisions/name keys/dependency neighborhoods and validator preconditions. Preparation freezes/validates/preallocates outside final installation. Any preparation failure discards all prepared actions and installs nothing. Publication is single-use; a successful or failed consumed object cannot be retried. DiscardTransaction is idempotent, releases owned stages/actions/pins and emits no committed ChangeSet.

Under existing Engine publication synchronization, recheck Engine/target/session/incarnation, terminal-admission state, epoch, cancellation and all touched preconditions. A frontier advance alone does not conflict with unrelated writes. After final preflight, install rows, descriptors, names, indexes, dependency edges, provider prepared state, new commit frontier and authoritative invalidations indivisibly. No external I/O, fallible partial mutation or arbitrary callback occurs after that preflight. Prepared provider Install is restricted local non-reentrant state installation; it is not a user callback. Deferred notifications happen after installation and cannot undo a commit. Lazy validity checks still detect drops even if notification delivery is delayed.

Cancellation/terminal end and final installation share a linearization boundary: observed cancellation/end before preflight prevents publication; completed publication survives later cancellation/end. Never report rollback after installation. CatalogConflict remains internal until its operation owner maps the actual conflict to its diagnostic; no universal fabricated SQLSTATE or blanket conflict on every commit.

Transactional DDL reads/binds through the coherent private catalog/data overlay. CREATE+INSERT and descriptor/index/dependency/storage changes commit together. Savepoint rollback and abort discard them together. Peers see no half-object. Conflicting peer DDL may wait according to the future locking contract; do not assert an immediate missing-object diagnostic where PostgreSQL would wait.

## 7. Participants, validation, cancellation and portals

```go
type TxParticipant interface {
    Mark(tx TxRef) (any, error)
    Restore(tx TxRef, mark any) error
    ReleaseMark(tx TxRef, mark any)
    PrepareEnd(ctx context.Context, tx TxRef, commit bool) (PreparedTxAction, error)
}
type PreparedTxAction interface { Install(); Discard() }
type TxObserver interface {
    Read(view *VisibilityView, relation ObjectRef, predicate any) error
    Write(tx TxRef, change VersionWrite) error
    Validate(ctx context.Context, tx TxRef) error
    End(tx TxRef, committed bool, frontier CommitSeq)
}
```

Register participants/observers once before their first transaction use through the TransactionOwner's engine-owned initialization wiring; reject nil/duplicate/foreign providers before transfer. Marks are opaque, immutable, exact-provider/exact-incarnation owned. Invoke Mark, Restore and PrepareEnd in registration order outside publication locks. ReleaseMark is infallible local release. Install is infallible/local/non-reentrant; Discard is idempotent. Failed rollback preparation cannot prevent ordinary private-write discard and resource release: continue cleanup, join errors, finish full end. Restore failure preserves the initiating failure and joins cleanup errors; it cannot restore Active partially.

Observers receive immutable identity and predicate coverage; consumer-owned immutable predicate payloads remain valid through their recorded retention period. Read/Write errors fail the admitted statement. Validate runs before preflight; SSI supplies final preconditions and post-end overlap retention through its owner, not an algorithm here. End occurs exactly once per incarnation after outcome is final, outside publication locks; `committed=false` never implies a ChangeSet. Locks consume transaction/row/subtransaction lifecycle events without this artifact choosing wait queues or resource scheduling.

Participants integrate transactional settings/LOCAL, temp ON COMMIT, catalog overlays and named portals. Their actual state remains feature-owned. Ordinary named cursors end with their creating transaction's commit/rollback. WITH HOLD prepares immutable remaining output before commit; failure prevents commit. Successful creating commit transfers portal lifetime/pins to SESSION Cursors. Creating abort removes it; later unrelated rollback does not remove a committed held cursor. CLOSE and session end release it. Rollback-to closes portals created after the mark but does not rewind earlier FETCH/MOVE, undo CLOSE or revive failed portals. Provider restoration must implement these exceptions rather than naively cloning cursor positions.

SESSION termination stops admission/cancels/drains then calls Transaction.EndSession exactly once, outside locks. It discards only owned private effects and releases TX resources; committed data and peers are unchanged. Borrower sql.Conn.Close, pool idling, stmt.Close and statement cancellation are not physical end. GC abandonment is best effort, no deterministic deadline. Active-query SQL signaling maps to ERROR 57014; terminal SQL signaling maps to FATAL 57P01. Ordinary Go context errors remain ordinary Go errors.

## 8. Native Reset/Dump/Restore and effect admission

Keep public native signatures and SESSION's selected persistent DatabaseTarget scope. Root withDatabaseTarget captures Engine/target and releases the borrower before external I/O; it never retains that borrower's transaction. Reset/Restore use CATALOG ReplaceCatalog only after complete staging/validation, advance one epoch, and never merge replacement with old target state. Other databases/DSNs/Drivers, cluster roles and live session settings/temp/prepared names/cursors/advisory resources are unchanged. Binding invalidation does not delete SQL prepared names.

Successful replacement fences every old-epoch transaction. Captured immutable reads stay usable; subsequent execution or commit returns ordinary sentinel ErrCatalogReplaced with `stuntgres: catalog replaced before publication`, discards private work and requires full end. Full ROLLBACK remains available. COMMIT cannot publish across the fence; rollback-to cannot recover it. Failure/cancellation/read failure before replacement preserves epoch, committed sentinel and otherwise eligible transactions. Retained named cursor output uses its old immutable view; new bindings see replacement.

Restore uses the ordinary parser/executor with a private SESSION Operation and replacement-mode stage. No borrower or target lock spans reader I/O. A script COMMIT may seal a private stage-local segment, but never publishes live target state; full script success publishes one replacement. Any late error discards every segment. CONTROL-SCRIPTS owns syntax and effect classification, not a second interpreter.

| Effect class | Restore boundary |
|---|---|
| Supported persistent descriptors/data/dependencies | Stage-owned, validated then atomically replaced. |
| Interpretation/settings needed only inside staging | Allowed only with isolated stage-local provider; never mutate live session state. |
| Live session/role changes, signaling, live session-resource acquisition | Reject before invocation unless consumer supplies completely isolated semantics. |
| Remote writes, nontransactional live state, process/network/WAL/native ABI effects | Reject before invocation; cannot claim rollback of external effects. |
| Unsupported serializer/stager feature | Explicit failure, never silently omit or successful no-op. |

Non-retractable effect rejection: ERROR 0A000, primary `restore cannot stage non-retractable effects`. Preserve original ordinary reader/context errors. Dump captures one committed view, excludes private writes and emits outside target locks. Writer/short-write errors remain ordinary; already emitted bytes cannot be retracted. Arbitrarily blocking external readers/writers cannot be forcibly canceled. These native boundaries have no PostgreSQL SQL-command oracle.

## 9. Clean cutover and session-sized consumers

No implementation callsite changes are made by this design. Future consumers migrate all affected callers/tests and remove obsolete paths rather than add aliases or parallel executors.

| Existing file / symbols | Required future migration |
|---|---|
| driver.go connector.Connect | SESSION physical session creation; preserve exact-DSN Engine map and dialing-context boundary. |
| conn.go Begin/BeginTx, Close, PrepareContext, execute, ExecContext, QueryContext | Session operation lease and transaction owner; replace unsupported BeginTx with incarnation-qualified driver.Tx, include admitted parse/Describe failures in scope completion; physical Close uses SESSION teardown. |
| stmt.go ExecContext/QueryContext/Close | Retain conn-bound AST/count/current conn.execute path; no preparation snapshot; Close releases handle only. |
| internal/engine/engine.go Describe/describe/Execute | Operation view/catalog authority, version reads/private stages instead of direct table.rows/e.tables publication; retain prepareArgs, binder, literal/parameter coercion and multirow atomicity. |
| internal/engine/snapshot.go; rows.go | Add idempotent pin release on Snapshot.Close/rows.Close and construction errors; retain metadata/filter/order/nil-selection behavior. |
| internal/parser/ast.go; grammar.y | Extend closed AST/grammar for controls/options/savepoints; preserve source positions, whole-input parsing and one-statement public adapter. No SQL-text dispatcher. |
| internal/engine/control.go Reset/Dump/Restore/staged Execute | Selected target, coherent committed dump view, replacement mode/session stage and common publication fence. |
| root control.go withEngine and callbacks | Replace withDatabaseTarget declaration and every caller, no alias; public signatures/native rejection/release-before-I/O unchanged. |
| internal/sqlstate/error.go and root Error alias | Remain sole error seam; PG18-DRIVER-ERROR-FIELDS adds conditional fields/warning reporting; transaction errors must not inherit unconditional public Schema annotation. |

Proposed parser forms: `TransactionControl{Kind TransactionControlKind; Chain bool; Options TransactionOptions; Pos Position}` with BeginTransaction/CommitTransaction/RollbackTransaction; `SavepointControl{Kind SavepointControlKind; Name string; Pos Position}` with CreateSavepoint/RollbackSavepoint/ReleaseSavepoint. TransactionOptions carries parser-owned isolation/read-only/deferrable values and presence flags, so omitted/default options differ from explicit settings. Owners handle characteristic timing rather than dropping these flags.

Before each implementation cutover, inventory Go receivers for `\.(Describe|Execute|Reset|Dump|Restore)\(`, `withEngine`, `table\.rows|e\.tables`, `BeginTx|Begin\(` and Snapshot construction using repository search. Classify public/internal receiver; migrate engine_test helpers, root/public tests, reference recording and smoke callers as applicable. Remove superseded mutable store/direct executor/unsupported branches; preserve public API signatures where specified.

| Backlog symbol | Bounded implementation session / integration | Exit witnesses |
|---|---|---|
| PG18-TX-CORE | Driver BeginTx, READ COMMITTED INSERT/SELECT, implicit stage/end and incarnation handle; no SQL controls/savepoints/isolation extension/DDL | W1–W3, W9 |
| PG18-TX-CONTROLS | Closed SQL control AST/dispatch, abort gate, warnings, CHAIN and stale-handle fence | W1 SQL variant, W4–W5 |
| PG18-TX-ISOLATION | Characteristics/options/timing, first-statement repeatable view; supported option rejection/read-only enforcement | W6, W7 repeatable conflict |
| PG18-TX-SAVEPOINTS | Named marks, shadow/reveal, coordinated row/catalog/provider restoration | W4 |
| PG18-CAT-CORE; PG18-DDL-TABLE-CORE | Authoritative descriptors/data overlays and transactional table effects | W8 |
| PG18-DML-UPDATE-CORE; PG18-DML-DELETE-CORE | Version predecessor writes/tombstones and changed-row recheck via locking contract | W6 identity, W7 |
| PG18-QUERY-CORE | Coherent immutable view scans and retained output | W2, W6, W10 |
| PG18-CONTROL-TRANSACTIONS | Epoch replacement fence, old-transaction recovery boundary | W11 |
| PG18-CONTROL-SCRIPTS | Stage-local script controls/effect classification and complete replacement | W11 |
| PG18-DRIVER-CURSOR; PG18-DRIVER-CURSOR-HOLD; PG18-DRIVER-CURSOR-MOVE | Named portal lifecycle, held remaining output, savepoint movement exceptions | W10 |
| PG18-DRIVER-SETTINGS-LOCAL; PG18-DDL-TEMPORARY | Transaction participant marks/end and LOCAL/temp effects | W4 provider restoration, W9 end |
| PG18-ADMIN-SESSION-SIGNAL | Active cancel versus physical termination through SESSION | W9 |
| PG18-DRIVER-ERROR-FIELDS | Severity, detail/hint/position/context/object fields and warning exposure | W3–W5, W7, W9 |
| PG18-ARCH-LOCKING | Separate blocking algorithms consuming transaction/row/subtransaction identity/events | W7/W8 integration, no algorithm here |
| PG18-ARCH-SERIALIZABILITY; PG18-TX-SERIALIZABLE | Separate nonblocking SSI contract/implementation consuming observer/preflight/end retention | W7; owner adds conflict-free/write-skew witnesses |

These are consumers, not additional deliverables or claims that their prerequisites are implemented. Capability edges govern implementation order; the table does not change catalog scope.

## 10. Independent future acceptance scenarios

These are specifications, not executed tests or smoke evidence. Use two explicitly held sql.Conn handles A/B, not arbitrary pool routing. Public SQL sequences below are separate calls. Run direct/prepared variants where applicable; driver BeginTx and SQL BEGIN variants belong to their respective consumers. Future fixtures assert expected results independently on memory and pinned real PostgreSQL, not parity alone. Close rows/handles before fixture reset. Unordered results are bags with multiplicities; listed sequences use ORDER BY v. `t(v INT)` reports column v, database type INT4 and scan type int64 on empty and nonempty results. Native/unit scenarios have no invented SQL oracle.

**W1 — Required rollback visibility (TX-CORE/TX-CONTROLS).** Empty committed t. A runs BEGIN then INSERT INTO t VALUES (1), affected count 1. A SELECT v FROM t ORDER BY v yields int64 [1]; B yields zero rows with matching metadata. A ROLLBACK succeeds; both then yield zero. Repeat with A.BeginTx and prepared INSERT/SELECT. No B dirty-read outcome is allowed.

**W2 — Successful atomic commit (TX-CORE/QUERY-CORE).** A stages 1 and 2 in separate statements, each count 1; A sees [1,2], B sees empty. Retain B's empty snapshot before commit. A commit succeeds; next READ COMMITTED B query yields [1,2], while old snapshot stays empty. Engine barriers assert no half-batch and preservation of an unrelated peer commit, not scheduling counts.

**W3 — Statement failure/autocommit (PG18-TX-CORE/PG18-DRIVER-ERROR-FIELDS).** t(v INT NOT NULL), committed 7. INSERT VALUES (8),(NULL) fails 23502 with section 11 fields, publishes neither row, leaves Idle and a healthy next SELECT yielding [7]. Prepared failure is identical. An admitted syntax/bind error preserves its own diagnostics and publishes nothing. A pre-admission argument error is ordinary and leaves prior transaction state unchanged.

**W4 — Abort and savepoint recovery (TX-SAVEPOINTS).** BEGIN; INSERT 1; SAVEPOINT s; failing INSERT (2),(NULL). Ordinary SELECT and RELEASE s fail 25P02. ROLLBACK TO s succeeds, restores Active and keeps s. INSERT 3; COMMIT gives [1,3]. A repeated rollback-to still works. Unknown mark gives 3B001. Outside-block SAVEPOINT/RELEASE/ROLLBACK TO give command-specific 25P01. Units assert duplicate-name shadow/reveal, RELEASE retaining child work, younger-mark removal and coordinated row/catalog/provider restoration. Inject failed provider Restore: full abort, no partial Active recovery/publication, original error retained with joined cleanup failures.

**W5 — Control and handle boundaries (TX-CONTROLS).** Redundant BEGIN leaves same incarnation with warning, not execution error. Outside ordinary COMMIT/ROLLBACK warns; outside AND CHAIN errors. COMMIT after failed block rolls back/no publication. COMMIT/ROLLBACK AND CHAIN returns healthy fresh incarnation with retained options. Old driver handle cannot commit/rollback it; sql.ErrTxDone at the public boundary. Unsupported/nested driver BeginTx rejects without mutation.

**W6 — Snapshot timing and identity (PG18-TX-ISOLATION/PG18-DML-UPDATE-CORE/PG18-DML-DELETE-CORE).** B begins REPEATABLE READ; A commits 1 before B's first query: B sees [1], proving BEGIN captured none. A commits 2 after that query: B still sees [1]; a READ COMMITTED peer sees [1,2]. B's own INSERT 3 becomes visible to B, not peers until commit. READ UNCOMMITTED has READ COMMITTED outcomes. The isolation consumer checks supported read-only and option timing/rejection without silent downgrade. Units assert duplicate values have distinct IDs, update preserves RowRef/new VersionRef, old view retains predecessor after update/delete, rollback preserves multiplicity, and equal numeric foreign/old-epoch refs cannot mutate another domain.

**W7 — Validation boundary (PG18-TX-ISOLATION/PG18-TX-SERIALIZABLE/PG18-DML-UPDATE-CORE).** Future conflicting repeatable update reports 40001 concurrent-update primary; SSI owner reports dependency primary. An injected validator failure is a unit interface witness only: no rows/descriptors/ChangeSet, complete cleanup and unchanged originating error. Caller retries whole transaction; no automatic retry. SSI owner independently adds real-reference write-skew and conflict-free expectations; this document does not claim SSI detection coverage.

**W8 — Transactional DDL (CAT-CORE/DDL-TABLE-CORE).** A private CREATE+INSERT resolves its own descriptor/data. Rollback leaves no descriptor, row, index or dependency; commit installs all together. Savepoint rollback removes post-mark DDL/data. Failed multi-object validation installs nothing. Peer same-object DDL may block; synchronize commit/rollback before asserting peer results rather than expecting immediate 42P01 while locking applies.

**W9 — Cancellation/end/abandonment (TX-CORE/ADMIN-SESSION-SIGNAL).** Pre-canceled operation returns context.Canceled and does not mutate state. Admitted cancellation before publication discards implicit work to Idle or explicit work to Aborted. A later healthy session operation remains possible. Publication/end barriers allow either complete commit first or no publication, never partial install/double cleanup. A physical termination discards A uncommitted writes and preserves B's committed sentinel. Borrower close/idle/stmt close do not end A. Explicit SESSION end deterministically releases once; actual GC abandonment has no deadline. SQL target cancel/terminate diagnostics follow section 11, not fabricated mappings of Go errors.

**W10 — Retained results and portals (PG18-QUERY-CORE/PG18-DRIVER-CURSOR/PG18-DRIVER-CURSOR-HOLD/PG18-DRIVER-CURSOR-MOVE).** Retain engine snapshot [1,2]. Peer inserts 3, transaction ends, Restore installs 99 or Reset clears: old snapshot remains [1,2]; new queries see [99] or missing table. Private-own-write snapshot remains immutable after rollback but never enters committed state; database/sql Tx Rows may auto-close. Ordinary cursor ends on creating commit/rollback. Held remaining output survives successful creating commit and a later unrelated rollback, not creating abort/CLOSE/session end. Rollback-to closes newly created portals; earlier FETCH/MOVE position stays advanced, CLOSE stays closed and failed portal stays unusable. Verify pin release/error paths as well as row values.

**W11 — Replacement and external effects (CONTROL consumers).** A stages old-epoch write, B retains [1,2]; Restore installs [99]. B old view stays [1,2], new view sees [99], A execution/commit gets ordinary ErrCatalogReplaced and cannot resurrect data; full ROLLBACK works, rollback-to cannot recover epoch. Reset same fence. Failed/canceled/read-failing/invalid Restore preserves sentinel 77, epoch and A's otherwise eligible stage. Blocking external I/O holds no borrower/target lock; peers proceed. Reject live/non-retractable effect before invocation, not after damage. Stage-local COMMIT followed by late failure publishes nothing. Successful script performs one replacement. Dump excludes A's private writes, preserves writer errors and cannot retract emitted bytes.

## 11. Diagnostics and adapter boundaries

SQL semantic errors use the sole internal/sqlstate.Error/root alias. PG18-DRIVER-ERROR-FIELDS owns transport and warning exposure. Below specify PostgreSQL severity, SQLSTATE and unprefixed primary; the ordinary Go Error string may retain the documented stuntgres prefix. Warnings are not execution errors. For ordinary transaction-control failures, optional Detail/Hint/Position/Context/Schema/Table/Column/DataType/Constraint are absent unless an originating failure actually supplies them. Never inherit engine.sqlError's unconditional Schema=public for a transaction error. Positions, when present, are one-based character offsets, not byte offsets. Do not fabricate PostgreSQL source-file/line/routine fields.

| Failure/boundary | Severity / SQLSTATE | Primary and fields |
|---|---|---|
| Ordinary aborted block | ERROR / 25P02 | `current transaction is aborted, commands ignored until end of transaction block`; ordinary case has no object/detail/hint/position fields. Preserve conditional originating recovery fields if applicable. |
| Missing savepoint | ERROR / 3B001 | `savepoint "missing" does not exist` for requested name missing; no invented optional fields. |
| Savepoint outside block | ERROR / 25P01 | `SAVEPOINT can only be used in transaction blocks` |
| Release outside block | ERROR / 25P01 | `RELEASE SAVEPOINT can only be used in transaction blocks` |
| Rollback-to outside block | ERROR / 25P01 | `ROLLBACK TO SAVEPOINT can only be used in transaction blocks` |
| Redundant BEGIN | WARNING / 25001 | `there is already a transaction in progress` |
| Outside ordinary COMMIT/ROLLBACK | WARNING / 25P01 | `there is no transaction in progress` |
| Outside COMMIT AND CHAIN | ERROR / 25P01 | `COMMIT AND CHAIN can only be used in transaction blocks` |
| Outside ROLLBACK AND CHAIN | ERROR / 25P01 | `ROLLBACK AND CHAIN can only be used in transaction blocks` |
| W3 NOT NULL | ERROR / 23502 | `null value in column "v" of relation "t" violates not-null constraint`; Detail `Failing row contains (null).`; Schema public, Table t, Column v. No invented Constraint/Hint/Position. |
| Repeatable changed predecessor | ERROR / 40001 | `could not serialize access due to concurrent update` |
| SSI validation boundary | ERROR / 40001 | `could not serialize access due to read/write dependencies among transactions`; preserve validator-supplied conditional Detail/Hint, never invent or suppress them. |
| SQL active-query cancel | ERROR / 57014 | `canceling statement due to user request` |
| SQL terminal signal | FATAL / 57P01 | `terminating connection due to administrator command` |
| Restore non-retractable effect | ERROR / 0A000 | `restore cannot stage non-retractable effects` — embedded native staging boundary, not a PostgreSQL native-control parity claim. |

Syntax/coercion/binding errors preserve their owning SQLSTATE/primary/fields, not a generic transaction wrapper. Native ErrCatalogReplaced (`stuntgres: catalog replaced before publication`), context cancellation, reader/writer errors, driver argument rejection and internal reference/lifecycle errors are ordinary Go errors with no SQLSTATE. Participant cleanup errors are joined without replacing the initiating error. Reference consumers expand existing SQLSTATE-only fixtures to record required English messages/fields/warnings; never weaken expectations to accommodate current shorter baseline diagnostics.

## 12. Evidence and verification boundary

Grounded repository anchors (paths relative to this worktree):

- `docs/design/PG18-ARCH-SESSION.md:38-133,143-175,196-213`: target/session ownership, authoritative `*Operation`, transaction attachment, ordered cleanup and native scope.
- `docs/design/PG18-ARCH-CATALOG-IDENTITY.md:32-52,235-263`: epoch/ref domain, staged validation/indivisible publication, ordinary replacement sentinel.
- `driver.go:17-58`, `conn.go:12-110`, `stmt.go:11-54`: existing exact-DSN owner, unsupported BeginTx, direct/prepared dispatch and admission cutover.
- `internal/engine/engine.go:245-347`, `snapshot.go:9-29,61-90`, `rows.go:12-43`: current statement staging, captured immutable values/filtering and result lifetime.
- `internal/engine/control.go:14-44,128-171`, `control.go:11-65`: capture-before-I/O, staged replacement, borrower release and unchanged public APIs.
- `internal/parser/ast.go:3-51`, `grammar.y:41-48`, `internal/sqlstate/error.go:4-15`: closed AST and single error seam.
- Research records `reference_helpers_test.go:20,57-94,104-150` and `control_test.go:96-182` as future fixture reuse/sentinel witnesses, not runtime evidence for this design.

Normative PostgreSQL 18 references: [BEGIN](https://www.postgresql.org/docs/18/sql-begin.html), [START TRANSACTION](https://www.postgresql.org/docs/18/sql-start-transaction.html), [COMMIT](https://www.postgresql.org/docs/18/sql-commit.html), [ROLLBACK](https://www.postgresql.org/docs/18/sql-rollback.html), [SAVEPOINT](https://www.postgresql.org/docs/18/sql-savepoint.html), [RELEASE](https://www.postgresql.org/docs/18/sql-release-savepoint.html), [ROLLBACK TO](https://www.postgresql.org/docs/18/sql-rollback-to.html), [SET TRANSACTION](https://www.postgresql.org/docs/18/sql-set-transaction.html), [isolation 13.2](https://www.postgresql.org/docs/18/transaction-iso.html), [DECLARE](https://www.postgresql.org/docs/18/sql-declare.html), [system columns](https://www.postgresql.org/docs/18/ddl-system-columns.html), [error fields](https://www.postgresql.org/docs/18/protocol-error-fields.html), [SQLSTATE](https://www.postgresql.org/docs/18/errcodes-appendix.html). Research diagnostic anchors use pinned release [REL_18_0 xact.c](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/access/transam/xact.c#L4021-L4072) and [REL_18_0 postgres.c](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/tcop/postgres.c#L1120-L1135).

Official `/docs/18/` manual provenance is 18.6; real-reference consumers remain pinned to PostgreSQL 18.0, C locale/UTF8, image `postgres:18.0-bookworm@sha256:3f55f8895c4ed50603e2fbdfc72fffeeaba3173321fee5cb825bbbeb30d9d854`. No 18.6 image is invented; minor-specific cases consume PG18-REF-ORACLE-18-6. Local PostgreSQL 20devel and ramsql are research/comparison only, not release oracles or dependencies.

This part has no executable feature smoke command and no SQL behavior smoke requirement. W1–W11 are independent future specifications. Readiness means this declared decision artifact exists and covers the frozen contract, not that its proposed APIs or behaviors run. Coordinator-owned gates and a fresh finding-free design review are required separately; this document claims no builds/tests/formatters or review approval.
