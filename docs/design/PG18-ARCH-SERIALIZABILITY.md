# PG18-ARCH-SERIALIZABILITY — Serializable snapshot isolation contract

## 1. Status, scope and requirement map

This is a decision-complete architecture contract for future implementation, not executable SSI or evidence that SQL tests pass. Its only deliverable is this document. It consumes [PG18-ARCH-TX-VISIBILITY](PG18-ARCH-TX-VISIBILITY.md), an accepted design, without changing that prerequisite. All Go contracts below are **proposed future internal/engine contracts**; their names do not assert current APIs exist.

The inspected baseline rejects `conn.BeginTx` with `0A000`, owns mutable tables in one Engine, stages multirow INSERT atomically per statement, and captures retained SELECT results before filtering. It has no executable transaction owner, version history or SSI tracker. Preparation and execution already share the binder, and prepared execution routes through `conn.execute`. These are integration seams, not an SSI implementation.

Serializable uses TX's Repeatable Read visibility: the first actual non-control execution captures a coherent frontier; subsequent statements reuse it plus prior successful own writes. SSI monitors anomalies; it does not select different visible versions. The guarantee applies to consistently Serializable participants, not business logic mixing isolation levels. Snapshot visibility/storage, blocking lock scheduling, physical query planning, two-phase commit, disk spill and network/durability mechanisms are outside this decision.

Predicate tracking is nonblocking: it never waits for a peer transaction to finish and cannot cause predicate-lock deadlocks. Short synchronization critical sections are not transaction lock waits. Ordinary write/write scheduling belongs to the locking/DML contracts. The sole additional SSI wait is safe-snapshot acquisition for `SERIALIZABLE READ ONLY DEFERRABLE`; there is no dependency on PG18-ARCH-LOCKING.

| Selected requirement | Decision sections | Independent future witnesses |
|---|---|---|
| Qualified transaction/read/write identities and dependency ownership | 2–3, 5 | W1–W4 |
| Predicate reads, writes and scan/DML extension points | 4 | W3–W4 |
| Early detection, final commit validation and atomic abort | 5 | W1–W2, W7 |
| PostgreSQL-compatible 40001 diagnostic context | 6 | W7 |
| Ordinary read-only safety and deferrable acquisition | 7 | W5–W6 |
| Post-commit overlap retention, cleanup and replacement | 8 | W4, W8 |
| Concrete interfaces, clean cutover and bounded backlog consumers | 2, 9 | W1–W8 |
| Positive, failure and deterministic interleaving acceptance | 10 | W1–W8 |

## 2. Ownership and proposed interfaces

Exactly one `SSITracker` belongs to each Engine/DatabaseTarget epoch. Engine-owned records can outlive transaction handles and physical sessions. Graph keys and footprints use qualified identities, never SQL names, pointers exposed as SQL identities, or unqualified numeric IDs.

Reuse TX's immutable `TxRef`, `ObjectRef`, `RowRef`, `VersionRef`, `CommitSeq`, `TxOptions`, `VisibilityView`, `VersionWrite`, `StatementScope`, `WriteStage`, `PreparedTransaction` and `TxObserver`. TX remains the transaction/identity allocator, version authority, binder and sole publication owner. SSI introduces neither an XID allocator nor a second store or executor. All subtransaction writer evidence names its top-level TxRef.

```go
// Proposed future contracts, not existing declarations.
type SSICoverageKind uint8 // SSIRelation, SSIRow

type SSICoverage struct {
    Kind SSICoverageKind
    Relation ObjectRef
    Row RowRef
}
type SSIReadEvidence struct {
    Coverage SSICoverage
    Version VersionRef // zero for empty/gap relation coverage
    Creator TxRef      // zero for a nonparticipating creator
    Ender TxRef        // zero if no participating update/delete
}
type SSISnapshot struct {
    Frontier CommitSeq
    ActiveReadWrite []TxRef
}
func (s *SSITracker) Enroll(tx TxRef, options TxOptions) error
func (s *SSITracker) Capture(ctx context.Context, tx TxRef) (*VisibilityView, error)
func (s *SSITracker) Read(view *VisibilityView, relation ObjectRef, predicate any) error
func (s *SSITracker) Write(tx TxRef, change VersionWrite) error
func (s *SSITracker) Validate(ctx context.Context, tx TxRef) error
func (s *SSITracker) End(tx TxRef, committed bool, frontier CommitSeq)
func (s *SSITracker) Finalize(prepared *PreparedTransaction, frontier CommitSeq) error
```

`Read`, `Write`, `Validate` and `End` preserve and implement TX's observer signatures. Register the engine-owned observer once before first use through TX initialization; no session-specific competing tracker. Only Serializable participants enroll in this graph. Other isolation modes retain their existing TX behavior and receive no SSI guarantee.

`predicate` must contain an immutable **value** of `SSIReadEvidence`. Validate the entire payload before graph mutation: view and transaction authority, enrollment, target/epoch, relation, coverage kind, and version/row consistency. `relation` must equal `Coverage.Relation`; `SSIRelation` requires zero Row; `SSIRow` requires a valid RowRef belonging to that relation. Nonzero Version must identify a version in the covered relation and match Row for row coverage. Creator/Ender must be valid participating top-level identities of this target/epoch if nonzero. Wrong types, foreign/stale identities and inconsistent payloads return ordinary internal Go errors, not invented SQLSTATEs, and leave the graph unchanged.

The version reader, not the caller's SQL text or returned values, supplies creator/ender and visibility evidence. Zero creator/ender means genuinely nonparticipating/absent, not missing metadata for an enrolled writer. Duplicate registration is idempotent; reader=writer never creates a self-edge. The tracker owns retained immutable coverage and write evidence, independent of caller buffer lifetime.

`Capture` wraps TX snapshot capture; it is not an alternative visibility algorithm. TX `StartStatement` calls it for Serializable's first actual non-control execution. Under Engine synchronization, capture both the authoritative visibility frontier and immutable `SSISnapshot` evidence of relevant active enrolled read/write participants before any row exposure. The ActiveReadWrite slice is tracker-owned immutable evidence, not a live session list. Describe/preparation captures neither an execution snapshot nor predicates. Subsequent statements keep TX's frontier and successful-own-write overlay.

A record contains options, snapshot frontier, incoming/outgoing deduplicated reader→writer edges, coverage, immutable accepted write evidence, `hasWritten`, doom, finalization/commit frontier, final outcome, candidate possible-unsafe dependencies, and end-delivery/release state. `hasWritten` is reconciled with TX's successful surviving write batch at finalization, not set permanently by an attempted write. A READ WRITE incarnation that finally commits without surviving writes receives the read-only ordering refinement; uncertainty before that proof must not remove protection.

## 3. Enrollment and state transitions

| State/event | Transition and invariant |
|---|---|
| Unenrolled, valid Serializable Begin/implicit incarnation | Enrolled-without-snapshot; options and qualified identity recorded, no data frontier or predicates. |
| First actual execution | Tracked-active, or read-only-unknown with candidate frontier/dependencies; capture and enrollment evidence are coherent. |
| Ordinary read/write registration | Add coverage/evidence and deduplicated edges; no visibility changes and no peer transaction waits. |
| Eligible dangerous participant selected | Doomed-but-active; monotonic until full end. Peer selection delivers no asynchronous SQL error and does not itself abort TX's current statement. The next required Read/Write registration fails as specified in section 6; commit without intervening registration fails at preflight. |
| Successful fallible PrepareTransaction/Validate | Still tracked, with final SSI requirement attached; not immune and not finalized. |
| Successful last preflight inside publication | Finalized; cannot be selected as victim; installation immediately follows under the same protected boundary. |
| Successful installation | Committed-retained with authoritative outcome/frontier recorded before publication unlock. |
| Full rollback/failed commit/terminal discard | Aborted, immediately irrelevant to detection; release coverage, edges and waits exactly once. |
| Read-only candidate proven safe | Safe/exempt; release SSI participation, preserve TX view/pins, no subsequent SSI failures. |
| Deferrable candidate unresolved | Candidate-wait, no row exposure; wait with neither Engine nor graph mutex held. |
| Deferrable candidate unsafe | Rejected/reacquire; release candidate and obtain a fresh TX frontier/view before exposure. |
| Last graph-cohort owner/candidate released | Retained records become reclaimable and drain. |

Finalized is a tracker state denoting the last preflight in indivisible publication. It is neither SQL `PREPARE TRANSACTION` nor mere construction of `PreparedTransaction`. No two-phase SQL support follows from that name. Already finalized/committed nodes cannot be doomed; aborted/doomed nodes cannot be prospective causes of new dangerous structures. A node's final outcome is authoritative even if observer End has not yet been delivered.

## 4. Complete access-point coverage

### Reads and write-before-read conflicts

A sequential scan registers `SSIRelation` **before** reading candidates, filtering, projection, aggregation or deciding that a result is empty. Register zero-Version relation evidence for the footprint itself, then authoritative version evidence for examined relevant versions, including versions invisible to the view. This supplies reader→writer antidependencies for writes that occurred first: the reader's view excludes a concurrent creator or an update/delete ender whose change affects the footprint. Use TX overlap/frontier and version metadata to distinguish these from already visible/nonoverlapping writes. Retention alone is not proof of concurrency.

TX's future `VersionScan` must report this evidence internally to the observer before exposing/skipping a relevant version. Its currently proposed `Next` tuple alone is insufficient to identify invisible writers; providing the internal evidence hook is a consumer requirement, not a new public scan API or a storage-layout decision. Returned-row-only tracking and inference from current relation values are prohibited. The current `Engine.Execute` SELECT capture before `Snapshot.selectRows` identifies the logical seam, but filtering must never become the sole read hook.

### Writes and read-before-write conflicts

Every Serializable INSERT/UPDATE/DELETE invokes observer `Write` on the authoritative `WriteStage.Apply` path after identity/kind/predecessor validation and before accepting the change. Registration and acceptance must be synchronized with read registration so a concurrent read/write cannot pass each other unrecorded.

| Access | Required check |
|---|---|
| INSERT | Relation coverage, including empty scans/gaps; retain accepted creator/write identity evidence for later readers. |
| UPDATE | Predecessor row and relation coverage; destination relation coverage too, including movement into a footprint. Retain new creator and predecessor ender evidence. |
| DELETE | Predecessor row/relation coverage and authoritative ending/tombstone evidence. |
| DML candidate scan | The same pre-filter read contract as SELECT, using TX's unchanged visibility view. |

Foreign/old-epoch or inconsistent change identities fail before mutation. On an admitted statement failure TX discards its entire write stage, including multirow changes; SSI may retain conservative read/edge history until full end but must never publish failed rows. Savepoint recovery restores surviving writes, not read history. Do not treat failed attempts as surviving writes when deciding read-only safety.

`SSIRow` is available only to accesses proven row-complete. Initial sequential scans always keep relation coverage. Future index/range consumers use relation coverage until they supply a complete immutable range/gap extension contract; they may not narrow already registered footprints. This design does not reproduce physical pages, a physical planner or PostgreSQL predicate-lock promotion machinery. Coarse relation coverage allows false positives on the same relation. Different unrelated relations do not conflict merely because the global frontier or graph revision advanced.

## 5. Dangerous structures and atomic publication

### Edge-add detection

Use explicit deduplicated rw edges and REL_18_0's ordering rules. The candidate is `Tin →rw pivot →rw Tout`, allowing `Tin == Tout` for a two-transaction cycle. Tout must finalize first in the relevant order. When Tin is read-only, Tout's finalization frontier must be at or before Tin's snapshot frontier. Mere overlap, a lone edge or arbitrary two edges is not sufficient. Conservative dangerous structures need not prove an actual complete cycle.

For a newly discovered `reader → writer`, ignore aborted/doomed causes and perform these checks against current retained ordering evidence:

1. An already committed writer/pivot whose outgoing edge points to an earlier-finalized Tout invalidates an eligible current reader; use retained ordering and the applicable read-only snapshot refinement.
2. For `reader → writer → Tout`, Tout is finalized, neither reader nor writer committed earlier than Tout finalized, and a read-only reader's snapshot frontier is at least Tout's finalization frontier.
3. For `Tin → reader → finalized-writer`, reader is not read-only; Tin is not doomed, did not commit before writer finalized, and, if read-only, has snapshot frontier at least writer's finalization frontier.

On write-side detection fail the current writer with the write-pivot diagnostic. Otherwise doom the eligible writer/pivot. If that writer is finalized/committed, fail the current reader instead; never revoke a finalized outcome. Duplicate edges do not repeat resource transfer or introduce self-conflicts. All checks retain qualified identities and meaningful commit order, not a graph-revision mismatch shortcut.

### Final commit preflight

First fail the current transaction if already doomed. Treat it as prospective Tout. For each incoming pivot that is not committed/doomed, inspect pivot's incoming Tin. If Tin is current Tout, or is an uncommitted non-read-only non-doomed participant, doom the eligible pivot and let current Tout progress. If that pivot is already finalized, fail current Tout with the prepared-pivot diagnostic. Committed/read-only exclusions and the edge-add read-only/frontier rules remain in force; this is not a blanket cycle-abort algorithm. This progress choice makes a controlled first preflight win W1, without promising a fixed public victim for arbitrary schedules.

### Indivisible final validation and installation

`Validate` performs fallible preparation checks and attaches a mandatory engine-local SSI final-precondition requirement to `PreparedTransaction`. It grants no immunity. Only TX's `PublishTransaction` may consume it, in this order:

1. Hold Engine publication synchronization. Recheck epoch, qualified identity/incarnation/session, terminal state, cancellation, predecessors, catalog and provider preconditions; all other fallible checks precede SSI finalization.
2. Acquire SSI graph synchronization, recheck current graph/doom and run `Finalize(prepared, nextFrontier)`. Validate prepared authority/single-use state and surviving-write status before choosing victims. Mark eligible victims and the successful current node finalized only at this last preflight.
3. Immediately perform TX's infallible row/catalog/provider installation, advance frontier, and record SSI's committed outcome/frontier within the same protected publication boundary. No external I/O, arbitrary user callback or later fallible precondition follows successful finalization.
4. Release locks. TX delivers observer `End` exactly once outside publication locks; it transfers retained ownership/releases resources and wakes safe-snapshot waiters. Finalized outcomes already drive candidate resolution, so delayed End cannot hide a commit from new enrollment.

The universal order when both locks are needed is **Engine publication mutex → SSI graph mutex**, never the reverse. Enrollment/capture, read/write registration and finalization use the same ordering for coherent evidence. Waiting releases both. PreparedTransaction alone cannot mark finalized; an outside-publication validation cannot be reused without the final recheck. Frontier advancement alone never aborts an unrelated commit.

Failure installs zero rows, descriptors, provider actions and ChangeSet; TX fully ends a failed commit. Successful publication survives subsequent notification/cancellation errors, which cannot retract a commit. No second publication path or two-phase-commit implementation is introduced.

## 6. Diagnostics, aborted state and caller retry

Use the sole `internal/sqlstate.Error` and root alias, expanded by PG18-DRIVER-ERROR-FIELDS. Current fields cannot transport Severity/Detail/Hint; this is a future integration requirement. Never use Engine `sqlError` for plain SSI errors: it unconditionally adds `Schema=public`.

Every dependency failure has these English fields:

| Field | Value |
|---|---|
| Severity | `ERROR` |
| Code | `40001` |
| Message | `could not serialize access due to read/write dependencies among transactions` |
| Hint | `The transaction might succeed if retried.` |

Select Detail by the actual REL_18_0 failure site, not one universal commit string:

| Site | Detail |
|---|---|
| Already-doomed participant entering Read conflict-out checking | `Reason code: Canceled on identification as a pivot, during conflict out checking.` |
| Already-doomed participant entering Write conflict-in checking | `Reason code: Canceled on identification as a pivot, during conflict in checking.` |
| Current writer identified as pivot | `Reason code: Canceled on identification as a pivot, during write.` |
| Doomed participant attempting commit | `Reason code: Canceled on identification as a pivot, during commit attempt.` |
| Commit conflict from finalized pivot | `Reason code: Canceled on commit attempt with conflict in from prepared pivot.` |
| Read conflict out to finalized pivot | `Reason code: Canceled on conflict out to pivot %u, during read.` |

For the last branch, substitute the pivot's valid transaction diagnostic identity from TX's identity owner. Supplying that metadata is mandatory before enabling the branch; do not use a pointer, pin PostgreSQL's allocator sequence, or invent an SSI-only XID. `prepared` in PostgreSQL's diagnostic wording does not enable SQL two-phase commit.

For an otherwise valid enrolled operation, check current doom under graph synchronization before adding read coverage/edges or accepting a write. The next required `Read` registration fails with the conflict-out-checking Detail even when it has zero-Version footprint evidence or would discover no new edge; REL_18_0 checks doom in both `CheckForSerializableConflictOutNeeded` and `CheckForSerializableConflictOut` (3990–4040). The next required `Write` registration fails with the conflict-in-checking Detail before accepting the change, even on an unrelated relation; `CheckForSerializableConflictIn` (4325–4351) checks doom before its write/conflict work. This differs from discovering a new pivot during that write, which uses `during write.`. A DML scan may reach Read first and therefore produce the read Detail; pure INSERT VALUES without a candidate scan supplies the independent write-path witness. Operations with no SSI registration, such as preparation or `SELECT 1`, do not acquire an invented SSI failure point. If no registration intervenes, doomed COMMIT uses the commit-attempt Detail.

The three summarization-only REL_18_0 Details at 4066–4082 and 4112–4120 are explicitly unreachable here: `Reason code: Canceled on conflict out to old pivot %u.`, `Reason code: Canceled on identification as a pivot, with conflict out to old committed transaction %u.`, and `Reason code: Canceled on conflict out to old pivot.`. Section 8 retains full committed records and explicit edges for the entire relevant cohort; it never evicts a relevant transaction into SLRU or summary-conflict flags. Retained finalized-pivot conflicts use the full-record read branch above, not old-pivot wording. Future summarization would require a separate contract revision, not a fallback diagnostic.

Plain dependency errors omit Position, Context, Schema/Table/Column/DataType/Constraint unless actual originating context supplies them. Optional fields are independently absent; do not fabricate PostgreSQL C file/line/routine. If an originating query position exists, it is one-based characters, not bytes. Preserve `errors.As` and `SQLState` through direct, prepared and commit adapters.

Keep Repeatable Read's `40001` primary `could not serialize access due to concurrent update` distinct. Preserve unique violation `23505`, deadlock `40P01`, ordinary Go/context errors and `ErrCatalogReplaced`; do not remap them into SSI errors.

Being marked doomed by a peer is distinct from receiving an error: TX remains active until its own operation reaches a required registration or commit preflight. The registration's 40001 is returned by that admitted statement, before exposing its read rows or accepting its write; TX discards the entire statement stage and follows its aborted/subtransaction rules and subsequent `25P02` gating. Earlier successful statement results are not retroactively retracted, and prior private writes still cannot publish. ROLLBACK TO may restore ordinary TX state but cannot clear top-level SSI doom; a resumed Read/Write still fails with its matching checking Detail and that incarnation cannot successfully publish. A final commit failure fully ends the incarnation with no publication. Full rollback remains available. Caller retry covers the entire transaction and application decisions with a fresh TxRef and snapshot, never automatic statement replay or a reusable failed prepared publication.

## 7. Read-only safety and deferrable acquisition

### Ordinary Serializable read-only

READ ONLY does not itself prove safety. At capture frontier F, atomically collect relevant concurrent enrolled Serializable read/write participants whose snapshots can overlap the candidate; include participants that have not yet written, not just `hasWritten` ones. A participant with no acquired snapshot cannot retroactively acquire an older view after a coherent capture, so relevance must be established from TX capture/enrollment evidence rather than merely being a live session.

No relevant possible writer makes the view immediately safe/exempt. Otherwise ordinary read-only starts tracking with a possible-unsafe set. For each such writer's authoritative final outcome:

- Abort, or commit without surviving writes: remove that uncertainty.
- Commit with surviving writes and an outgoing rw edge to a transaction finalized at/before F: mark the candidate unsafe, retaining ordinary SSI monitoring (not immediate automatic error solely for candidate unsafety).
- Otherwise remove that writer. When all uncertainties resolve without unsafe evidence, mark safe/exempt and release predicates/graph references.

Outgoing commit-order evidence must survive peer End/session termination for this calculation. Candidate resolution is driven by recorded outcomes, not delayed notification delivery. Unsafe ordinary readers cannot be recaptured after exposure; until successful commit their results remain provisional unless proven safe. Safe/exempt readers retain TX visibility pins and cease SSI checks/failures, not ordinary cancellation or epoch checks.

### Serializable read-only deferrable

BEGIN enrolls but does not wait or acquire the data snapshot. The first actual execution's `Capture` performs this loop before binding/exposure against the final view:

1. Acquire a coherent TX candidate frontier/view and possible-unsafe set under the ordered synchronization boundary.
2. If immediately safe, return it exempt. Otherwise register the candidate/wait handle and release Engine and graph locks before waiting.
3. Recheck candidate state on wake; resolve on relevant commit/abort, terminal end, epoch fence or caller cancellation. Registration plus state recheck must prevent a lost wake if a peer ends between registration and sleep.
4. Safe candidate returns its captured view. Unsafe candidate releases all its candidate/view/wait references internally and reacquires a fresh TX frontier; no rows from the rejected view escaped.

Candidate rejection is not user-facing `40001` and is not transaction replay: no application statement effects or decisions have occurred yet. Cancellation returns the originating context error, releases wait/candidate resources, publishes nothing and follows TX's admitted-failure rules. Physical session termination cancels/drains the wait; replacement returns TX's ordinary `ErrCatalogReplaced`, not SSI failure. No graph/publication lock is held while waiting. `DEFERRABLE` in READ WRITE or non-Serializable mode has no safe-snapshot effect. After a safe view returns, the transaction runs free of SSI failures while preserving TX visibility and normal lifecycle constraints.

## 8. Retention, recovery and epoch replacement

Choose conservative **graph-cohort retention**. Keep committed predicates, write identities, dependencies and ordering evidence while any tracked nonexempt transaction or candidate can reference that epoch's dependency graph. Drain the retained cohort when that set is empty. Explicit frontier/overlap checks prevent old retained records from manufacturing conflicts with nonoverlapping newcomers. This trades memory for simple sound ownership: continuous overlap can retain much more than PostgreSQL's summarization. Disk spill, configuration limits and technical counters are not introduced here.

- Closing Rows, statements, sql.Conn borrowers or pools does not erase committed SSI records. Physical session termination also cannot erase still-needed committed records; Engine owns them.
- Savepoint rollback restores private writes but retains top-level read history and conservative edges. Doom remains monotonic. Do not restore SSI history from participant write marks.
- Full rollback makes the node immediately irrelevant, detaches edges/coverage/candidate dependencies and releases exactly once. Remove aborted records immediately; surviving cohort peers remain protected.
- CHAIN creates a new TxRef with inherited characteristics, never a recycled graph node.
- Safe/exempt readers release graph/candidate references, retaining their TX view pins. SSI overlap lifetime is distinct from version/result pin lifetime.
- Successful publication records the final outcome before unlocking. End is delivered exactly once by TX and implements idempotent resource transfer/release if cleanup is retried; duplicate cleanup cannot double-release or change outcome.
- Reset/Restore uses TX's existing epoch fence. Old transactions cannot publish; new same-named objects belong to different qualified graph keys. Old immutable results remain usable under TX's pin contract. Wake fenced candidates and reclaim the old graph once its tracked owners/candidates drain.
- Failed replacement changes neither old epoch nor data and leaves otherwise eligible SSI transactions intact. Ordinary `ErrCatalogReplaced`, including TX's `stuntgres: catalog replaced before publication`, is never recoded to 40001.

## 9. Clean cutover and bounded future consumers

These are future implementation instructions, **not additional edits in this design task**. Preserve TX observer signatures and the single parser/binder/executor/publication path.

| Existing caller/seam | Required future cutover |
|---|---|
| `conn.go` BeginTx, PrepareContext, execute, ExecContext, QueryContext | Consume TX operation/options/statement admission; enroll the current incarnation. Preparation records no execution predicates; direct and prepared execution use current TxRef. Remove unsupported transaction paths only in their owning consumer. |
| `stmt.go` ExecContext/QueryContext | Continue through conn.execute; retain AST/count, never a prepared execution snapshot or predicate node. |
| `internal/engine/engine.go` Describe/Execute | Describe remains metadata-only. SELECT/INSERT registration moves to authoritative VersionScan/WriteStage access points; preserve binder, prepareArgs, coercion and multirow atomic staging. |
| `internal/engine/snapshot.go` construction/filtering | Register reads before filtering; preserve immutable result behavior and nil-selection optimization. Returned rows cannot reconstruct coverage. |
| `rows.go` Close | Release TX result pins only, not transaction-owned SSI records. |
| `internal/engine/control.go` Reset/Restore and staging Execute | Consume TX replacement fences and staged executor; no SSI replacement executor or borrower-held wait. Dump remains committed-view capture. |
| `internal/sqlstate/error.go` and root alias | Use DRIVER-ERROR-FIELDS for conditional diagnostics and errors.As preservation; no second error type or public-schema injection. |
| engine/driver/native control/smoke callers and tests | Migrate affected signatures through TX's existing cutover, including unsupported-Begin expectations and snapshot helpers; delete superseded mutable/direct execution paths in the owning implementation task. |
| reference helpers/recording/tests | Retain pinned oracle/reset lifecycle; add future independent transaction/diagnostic recording and held-connection interleavings rather than claiming existing SQLSTATE-only checks cover SSI. |

Before each consumer implementation, inventory exact receivers/callers with repository grep patterns `\.(Describe|Execute|Reset|Dump|Restore)\(`, `BeginTx|Begin\(`, `table\.rows|e\.tables`, `TxObserver|StartStatement|PrepareTransaction|PublishTransaction`, and Snapshot construction. Classify production, helpers, native controls and tests; migrate every affected caller through the existing TX cutover and delete obsolete paths without aliases, shims or parallel executors. This inventory is a consumer exit requirement, not permission to modify those files now.

| Bounded session, existing owner | Concrete exit and independent acceptance |
|---|---|
| PG18-TX-SERIALIZABLE: enrollment/capture/typed relation footprints | Current identity and first-execution frontier wired through TX; malformed payloads leave graph unchanged; preparation adds no footprint; W2 both commit. Coordinate characteristic syntax/timing with PG18-TX-ISOLATION. |
| PG18-TX-SERIALIZABLE: two-direction dependencies | Empty coverage plus invisible creator/ender evidence and retained accepted writes; deduplicated reader→writer edges, no self-edge; W3/W4. |
| PG18-TX-SERIALIZABLE: detection/finalization | REL_18_0 checks and final SSI requirement inside one publication boundary; W1/W2 concurrent preflight and W7 zero loser publication. PG18-DRIVER-ERROR-FIELDS owns transport. |
| PG18-TX-SERIALIZABLE: lifetime/end integration | Post-commit retention, monotonic doom, cohort drain and fresh CHAIN identity; W4/W8. PG18-TX-SAVEPOINTS owns private-write restoration, retaining SSI reads. |
| PG18-TX-SERIALIZABLE: safe read-only/deferrable capture | Candidate resolution, no lost wake, rejection/reacapture before exposure, cancellation/fence cleanup; W5/W6. |
| PG18-QUERY-CORE | Scan/predicate hooks and later complete range/gap extensions; W3 coverage before filtering, no narrowing. |
| PG18-DML-UPDATE-CORE / PG18-DML-DELETE-CORE | Predecessor, movement and tombstone read/write evidence; W3 and unchanged TX visibility/write conflict handling. |
| PG18-CONTROL-TRANSACTIONS | Existing replacement fencing/wait wake and old/new epoch separation; W8. |

Preserve SSI's catalog prerequisites PG18-ARCH-SERIALIZABILITY and PG18-TX-ISOLATION. These sessions introduce no new backlog symbols or blocking-lock prerequisite and do not implement adjacent features here.

## 10. Independently specified future acceptance

**None of these scenarios was executed for this design.** They specify future unit and real-reference expectations independently, not parity-only assertions. The frozen design has no smoke argv. Full production verification belongs to the coordinator; no build/test/formatter/reference query is claimed here.

Future public fixtures hold independent `sql.Conn` A/B/C for the entire schedule, use deterministic statement/commit barriers rather than sleeps or pool routing, and run direct/prepared variants and driver/SQL transaction controls where those features exist. Close rows/transactions/handles before privileged schema reset. Compare unordered bags with multiplicities; require ORDER BY when listing ordered results. Unit barriers may select algorithm-specific preflight order; arbitrary PostgreSQL public schedules must not promise a particular victim or failure statement.

### W1 — Documented SUM write skew

Initialize `mytab(class INT, value INT)` with `(1,10),(1,20),(2,100),(2,200)`. Begin A/B at chosen isolation. Both reads finish before either INSERT:

- A: `SELECT SUM(value) FROM mytab WHERE class = 1` → `int64(30)`.
- B: `SELECT SUM(value) FROM mytab WHERE class = 2` → `int64(300)`.
- SUM(int4) metadata is INT8/int64. A inserts `(2,30)`, B inserts `(1,300)`; each accepted INSERT has affected count 1.

Repeatable Read: both commit; final bag is the four originals plus both additions (ordered pairs if explicitly sorted: `(1,10),(1,20),(1,300),(2,30),(2,100),(2,200)`). Serializable: at most one commits, the loser reports dependency ERROR/40001, and no loser addition is visible. With both writes staged, controlled A-first preflight permits A and dooms B; reverse order in a separate unit schedule. Public arbitrary schedules allow earlier detection/either victim, never both successful commits. Retain this literal SUM architecture criterion even before aggregates exist; bounded early consumers additionally use ordered value reads and application summing, not a narrowed substitute acceptance.

### W2 — Conflict-free pair and ordering boundaries

Committed `a(v INT)=[1]`, `b(v INT)=[2]`. A/B Serializable capture snapshots before writes. A reads/inserts 10 only in a; B reads/inserts 20 only in b. Both commit in either order; ordered final a is `[1,10]`, b is `[2,20]`; INSERT counts 1 and v metadata INT4/int64. Distinct relations avoid legitimate coarse-footprint false positives.

Independent graph units cover reader→writer direction, no self-edge, duplicate idempotence, lone-edge success, Tin=Tout cycles, Tout-first versus non-dangerous order, read-only snapshot refinement, doomed exclusion and finalized victim immunity. Concurrent preflight barriers forbid both invalid commits and retain an eligible winner; unrelated frontier advancement cannot abort the positive pair.

### W3 — Empty/gap predicates and both conflict directions

Initially empty `p(v INT)` and `q(v INT)`. A reads empty p then inserts q(1); B reads empty q then inserts p(1); both reads precede both inserts. Repeatable Read permits both commits, with one addition in each table. Serializable forbids both additions publishing. Independently test read-before-write and write-before-read invisible creator/ender evidence, unmatched predicates, coverage before filtering, update movement into/out of covered relations, deletes, and invalid row/identity payload rejection with no graph mutation. Returned-row-only tracking fails this witness; sequential relation coverage is sufficient.

### W4 — Post-commit overlap and savepoint retention

B captures its Serializable snapshot before A commits. A reads p, inserts q and commits; close A's rows and physical session. B reads old q and writes p. B cannot commit that invalid history: retained A read/writer records survive End/session cleanup. After all tracked owners end, graph records drain and a nonoverlapping transaction inherits no conflict.

Units read inside SAVEPOINT and ROLLBACK TO: retain that read, discard post-mark writes, and never restore doom to healthy. Full rollback removes/ignores the participant and publishes nothing. CHAIN uses a fresh identity. Duplicate cleanup has no double release. Test result-pin release separately from SSI references.

### W5 — Ordinary read-only safety

Units independently prove: no relevant concurrent writers → immediate safe; unresolved writer → unknown; peer abort/no surviving writes → uncertainty removed; peer commit with outgoing edge to finalized transaction at/before candidate F → unsafe; later-than-F edge → not unsafe. Safe readers release SSI state but retain visibility pins. READ ONLY alone does not suppress a dangerous structure.

Public three-transaction anomaly: initially X=0,Y=0; T2 reads both; T1 reads Y, writes Y=20 and commits; R reads X=0,Y=20 and commits; T2 attempts X=-11. The fully Serializable history must reject T2's write/commit with dependency 40001 and never expose an invalid all-committed history. Unit schedules pin the selected early failure branch; public expectations permit a legitimate earlier/final failure point. A separate no-R schedule permits T1/T2 both to commit, demonstrating that overlap alone is not failure.

### W6 — Deferrable safe snapshot

Use the same X/Y start but R is SERIALIZABLE READ ONLY DEFERRABLE and its first query starts after T1 commits while T2 remains active. BEGIN need not wait. R's first query waits before row exposure. T2 writes X=-11 and commits; R eventually returns ordered X=-11,Y=20 and commits without SSI failure. The local 20devel isolation fixture supplies a research schedule, not executed PG18 proof.

Units force unsafe-candidate rejection/fresh capture, immediate-safe acquisition, commit/abort wake, registration/sleep race without lost wake, cancellation/session-end/epoch-fence cleanup, no mutex held during wait and no safe-snapshot effect of DEFERRABLE in other modes. Assert no rejected-candidate rows or writes escape.

### W7 — Diagnostics, atomicity and nonblocking

Doomed COMMIT exposes ERROR/40001, exact dependency Message, commit-attempt Detail and retry Hint from section 6; plain optional fields absent, `errors.As` preserved. Other branches assert their own exact Details, including a valid relational diagnostic pivot identity rather than a pinned allocator value. Assert no loser row/catalog/provider installation or ChangeSet. Early admitted failure applies TX abort/25P02 handling; failed explicit commit fully ends. RR predecessor conflict retains its distinct primary; context/epoch errors remain ordinary.

**W7a — Independently ordered doomed-but-active paths.** In three fresh runs initialize ordinary permanent heap tables `p(v INT)`, `q(v INT)`, `r(v INT)`, each with `[0]`, no indexes/constraints or triggers. Hold A/B Serializable READ WRITE connections. Execute, in order: A `SELECT v FROM p` → `[0]`; B `SELECT v FROM q` → `[0]`; A `INSERT INTO q VALUES (1)` → count 1; B `INSERT INTO p VALUES (1)` → count 1; A COMMIT → success. Both snapshots precede both writes. The reads/writes independently establish A→rw B through p and B→rw A through q; neither write yet has a finalized Tout. A's first preflight is prospective Tout in A→B→A and marks B doomed, without sending B an error. Units assert exactly those edges and B's doomed-but-active/TX-active state, using real registration/finalization paths rather than injecting a doom flag. The pinned public schedule reaches this preflight choice with all prior operations completed and B idle; it is not an arbitrary-schedule victim guarantee.

- Read run: next B `SELECT v FROM q` fails at that statement with ERROR/40001, exact dependency Message/Hint and `Reason code: Canceled on identification as a pivot, during conflict out checking.`; no rows from this statement escape.
- Write run: next B `INSERT INTO r VALUES (9)` (pure VALUES, no DML read scan) fails at that statement with ERROR/40001 and `Reason code: Canceled on identification as a pivot, during conflict in checking.`; no successful affected count or r(9) is reported. Its unrelated relation proves that an already-set doom check, not a newly discovered edge, causes failure.
- Commit control run: with no intervening registration, next B COMMIT fails with ERROR/40001 and `Reason code: Canceled on identification as a pivot, during commit attempt.`, fully ending B.

For read/write runs, a subsequent ordinary B `SELECT v FROM r` returns `25P02` until full ROLLBACK (no savepoints in this witness); do not expect a second dependency 40001 from an already-aborted statement or treat aborted-state COMMIT as the preflight control. Roll back B, then independently read ordered committed p=`[0]`, q=`[0,1]`, r=`[0]`, with v INT4/int64 metadata, on fresh transactions. Assert plain optional diagnostic fields absent and `errors.As`/`SQLState` preserved in direct/prepared variants. Unit counterparts assert no failed statement acceptance/publication/ChangeSet, monotonic doom across savepoint recovery, and full end releasing B while retaining A's records for any remaining cohort owner. These are source-grounded future outcomes, not runtime observations in this design run.

Hold a Serializable reader idle after ordinary SELECT: a peer INSERT completes without waiting for reader end (later SSI failure is allowed). Snapshot SELECT does not wait for an uncommitted peer INSERT to return its visible rows. Separate legitimate write/write scheduling from SSI. No predicate waits/deadlocks occur; only deferrable capture adds SSI waiting.

### W8 — Native replacement and lifetime

Successful Reset/Restore epoch replacement leaves retained old views unchanged, prevents old TxRef publication, and separates new same-named objects from old graph keys. Error is ordinary ErrCatalogReplaced, not 40001; full end drains old registrations/waits. Failed replacement preserves epoch/data and eligible SSI transactions. These are independent native/engine expectations; native replacement has no PostgreSQL SQL-command oracle.

## 11. Evidence and provenance

Normative release semantics:

- [PG18 §§13.2.2–13.2.3](https://www.postgresql.org/docs/18/transaction-iso.html#XACT-SERIALIZABLE): first-execution snapshot, literal SUM skew, nonblocking predicates, coarse coverage, provisional ordinary read-only results and overlap retention.
- [SET TRANSACTION, Description](https://www.postgresql.org/docs/18/sql-set-transaction.html): DEFERRABLE only affects Serializable read-only first-snapshot acquisition.
- [PG18 §13.5](https://www.postgresql.org/docs/18/mvcc-serialization-failure-handling.html): varied serialization diagnostics and complete caller-owned transaction retry.
- [PG18 §54.8](https://www.postgresql.org/docs/18/protocol-error-fields.html): severity/code/primary, conditional context/object fields and character positions.

Pinned implementation research, not executed oracle results:

- [REL_18_0 predicate.c:33–85](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/storage/lmgr/predicate.c#L33-L85): both conflict directions, nonblocking tracking and post-session retention.
- [predicate.c:1540–1614](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/storage/lmgr/predicate.c#L1540-L1614) and [3450–3650](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/storage/lmgr/predicate.c#L3450-L3650): candidate wait/retry and read-only safety resolution.
- [predicate.c:4505–4767](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/storage/lmgr/predicate.c#L4505-L4767): ordering, victim choice, final preflight and branch-specific diagnostics.
- [predicate.c:3990–4120](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/storage/lmgr/predicate.c#L3990-L4120) and [4325–4351](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/storage/lmgr/predicate.c#L4325-L4351): already-doomed read/write checking Details and excluded old-pivot SLRU/summary branches; [4690–4767](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/storage/lmgr/predicate.c#L4690-L4767): peer doom without asynchronous error and later commit failure. These independently ground W7a's state and failure-site expectations.
- [REL_18_0 README-SSI:453–584](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/storage/lmgr/README-SSI#L453-L584): savepoint read retention and read-only optimizations.

Reusable repository evidence (relative paths name this repository):

- `docs/design/PG18-ARCH-TX-VISIBILITY.md`, sections 2–7: qualified ownership, immutable versions/views, observer signatures and one final publication boundary; sections 8–11: replacement, consumers and error boundaries. Its done status supplies a design contract, not executable prerequisites.
- `internal/engine/engine.go:13–53,245–347`: mutable authority, sole binder, staged INSERT and SELECT capture-before-filter. `internal/engine/snapshot.go:9–29,61–90`: retained result/filter/order seam. `conn.go:28–112`, `stmt.go:11–54`, `rows.go:12–43`: unsupported transaction baseline, shared execution and distinct result lifetime. `internal/engine/control.go:14–24,128–171`: Reset and completely staged Restore. `internal/sqlstate/error.go:4–14`: current limited error seam; engine.go:42–43 unconditionally supplies public Schema.
- Supplied research anchors `reference_helpers_test.go:20,57–94,104–150`, `reference_recording_test.go:18–28,63–143`, `reference_test.go:48–64`: pinned oracle and recording seams; current SQLSTATE-only assertions are not SSI diagnostic acceptance.
- Supplied selected record/research and frozen PG18-ARCH-SERIALIZABILITY plan define this sole artifact and bounded backlog mappings. No catalog or prerequisite modification is implied.

Official `/docs/18/` pages identify **18.6**. Preserve the existing real-reference runtime **18.0**, `postgres:18.0-bookworm@sha256:3f55f8895c4ed50603e2fbdfc72fffeeaba3173321fee5cb825bbbeb30d9d854`, UTF8/C. Minor-specific 18.6 behavior belongs to PG18-REF-ORACLE-18-6; no alternative digest is invented. Local `/home/rlew/projects/stuntgres/src-ref/postgres/configure.ac:20` identifies **20devel**; its read-only-anomaly-3 spec/expected files are research-only schedule expectations, never release-runtime observations. Ramsql's mutation-undo/relation-lock model in supplied research is not reusable SSI or this accepted immutable-version design.

No executable SQL behavior, build, test, race check, formatter or runtime smoke was exercised in authoring this artifact. The absent current TX interfaces and extended diagnostic identity/fields are mandatory future consumer inputs, not blockers to this design-only decision and not permission to implement adjacent foundations. Relation coverage and conservative cohort retention are deliberate soundness/simplicity choices with explicit false-positive and memory costs, not PostgreSQL physical implementation claims.
