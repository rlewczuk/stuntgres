# PG18-ARCH-SESSION — Per-connection identity and lifecycle

## 1. Scope and evidence

This is a **design decision artifact**, not implemented session support. All interfaces and SQL checks below are future contracts. Physical driver connections own sessions over an exact-DSN-shared Engine; pool borrowers do not own their lifetime. End and failed initialization converge on per-session cleanup, never a DSN-wide sweep.

Transaction visibility/publication and state machines belong to `PG18-ARCH-TX-VISIBILITY`; authorization eligibility/ACLs to `PG18-ARCH-AUTHORIZATION`; lock algorithms to `PG18-ARCH-LOCKING`; SQL resource semantics to the consumers in section 7. These are future boundaries, not prerequisites or support claims. Reuse the existing catalog, binder, execution and native-control authority.

PostgreSQL factual evidence takes precedence over current Go behavior. Official **18.6** manuals: [SET](https://www.postgresql.org/docs/18/sql-set.html), [RESET](https://www.postgresql.org/docs/18/sql-reset.html), [DISCARD](https://www.postgresql.org/docs/18/sql-discard.html), [PREPARE](https://www.postgresql.org/docs/18/sql-prepare.html), [EXECUTE](https://www.postgresql.org/docs/18/sql-execute.html), [DEALLOCATE](https://www.postgresql.org/docs/18/sql-deallocate.html), [DECLARE](https://www.postgresql.org/docs/18/sql-declare.html), [temporary tables](https://www.postgresql.org/docs/18/sql-createtable.html#SQL-CREATETABLE-TEMPORARY), [advisory locks §13.3.5](https://www.postgresql.org/docs/18/explicit-locking.html#ADVISORY-LOCKS), [SET ROLE](https://www.postgresql.org/docs/18/sql-set-role.html), [SET SESSION AUTHORIZATION](https://www.postgresql.org/docs/18/sql-set-session-authorization.html), [database ownership §22.1](https://www.postgresql.org/docs/18/manage-ag-overview.html), [CREATE DATABASE](https://www.postgresql.org/docs/18/sql-createdatabase.html), [ALTER DATABASE](https://www.postgresql.org/docs/18/sql-alterdatabase.html), [DROP DATABASE](https://www.postgresql.org/docs/18/sql-dropdatabase.html).

Release C corroboration: [REL_18_0 variable.c](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/commands/variable.c) (`check_role`, `assign_role`, `show_role`), [miscinit.c](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/utils/init/miscinit.c) (`SetSessionAuthorization`, `SetCurrentRoleId`), [portalmem.c](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/utils/mmgr/portalmem.c) (`AtAbort_Portals`, `AtCleanup_Portals`), [discard.c](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/commands/discard.c) (`DiscardAll`), [postinit.c](https://github.com/postgres/postgres/blob/REL_18_0/src/backend/utils/init/postinit.c) (`CheckMyDatabase`, `process_settings`). These ground role reset, held-cursor survival, explicit DISCARD and approximate simultaneous admission limits; they are not runtime observations.

Go adapter evidence: [driver contracts](https://pkg.go.dev/database/sql/driver#Connector), [SessionResetter](https://pkg.go.dev/database/sql/driver#SessionResetter), [Validator](https://pkg.go.dev/database/sql/driver#Validator), [sql.Conn.Close](https://pkg.go.dev/database/sql#Conn.Close), [runtime.AddCleanup](https://pkg.go.dev/runtime#AddCleanup). Connect context is dialing-only; borrower Close returns the physical connection to its pool. Cleanup must not retain its target and is nondeterministic. Adapter reset errors other than ErrBadConn need not propagate through database/sql; operation entry must check cancellation independently.

Installed Go1.27.1 source `database/sql/sql.go:600-628,1353-1358,1426-1431` confirms reset delegation and ErrBadConn-only discard; `runtime/mcleanup.go:17-74,212-218` establishes transitive non-retention, Stop races and KeepAlive. These are Go implementation constraints, not PostgreSQL semantics.

Preserve the pinned reference runtime **18.0**, UTF8/C, `server_version_num=180000`, image `postgres:18.0-bookworm@sha256:3f55f8895c4ed50603e2fbdfc72fffeeaba3173321fee5cb825bbbeb30d9d854`. Minor-specific 18.6 cases consume `PG18-REF-ORACLE-18-6`; local **20devel** C is research-only. Future consumers independently obtain/assert exact release diagnostics; none are observed by this design revision. Current worktree seams and migrations are identified in section 6, not treated as PostgreSQL semantic evidence. Reuse the companion `PG18-ARCH-CATALOG-IDENTITY.md` owner/epoch and resolution contracts; no other design artifact is needed here.

## 2. Identity and ownership invariants

Driver allocates monotonically increasing, nonzero `SessionID uint64` under its ownership synchronization for each physical Connect attempt that reaches initialization; each successful Connect publishes exactly one unique session. Allocation is never per connector, borrower, Ping or statement. IDs are never reused during that Driver's lifetime; failed initialization may leave gaps. Exhaustion fails before admission/publication with an ordinary internal Go error, never wraparound. Different Drivers may allocate equal numbers: a number alone is not authority.

The same Driver and exact DSN select one Engine; two live physical connections have different sessions and share a selected database only when their selectors resolve to the same DatabaseTarget. Reuse preserves physical-session identity; replacement allocates a fresh identity. Empty and single-space DSNs differ. Closing one/all pools never erases committed data; reopening on the same Driver/DSN retains it. Another Driver isolates it.

| Domain/owner | Owned state | Boundary |
|---|---|---|
| Driver | Exact opaque DSN map; physical-session ID allocator | No DSN URI/libpq parsing; no session allocation on pool borrow |
| DSN Engine | Embedded cluster domain: roles, database-name registry, stable default target; session registry | No global terminal sweep; Engine lifetime exceeds conn lifetime |
| Selected logical database | Persistent descriptors, storage, catalog epoch and metadata | Database-local ObjectRef authority; equal numeric refs in a/b are not interchangeable |
| Session | Mutable setting overrides/default-provider attachment, temp namespace/resources, SQL prepared-name and named cursor registries, role view, one opaque transaction attachment | Session token and immutable target qualify ownership; registries track lifetime, not competing semantic stores |
| Feature/catalog owners | Shared setting definitions/default providers; committed cluster roles; persistent dependency edges; actual resource objects | Mutable session values are not persistent descriptors/storage; persistent dependencies remain catalog-owned |
| Locking manager | Actual advisory holds keyed by database domain and session token | Session owns release delegation, not lock algorithms |
| Driver stmt | Conn-bound AST/count handle | stmt.Close releases driver handle, not SQL DEALLOCATE |
| Anonymous Rows/Snapshot | Retained immutable committed result rows/metadata | Not a named cursor; existing Reset/Restore retention remains unchanged |

Logical introduction composes the catalog design: DSN Engine retains its owner; a database-scoped object handle is the complete pair `{DatabaseTarget, ObjectRef}`. Keep that pair together through standalone ref consumption, reads, stages, plans and NamespaceAccess validation; operation ResolutionContext/TempSchema and access views retain the same enclosing target. Never accept a naked database-local ref against another target because owner/epoch/address coincide. Ref epoch/address is meaningful only within that database incarnation; shared identities use the companion's separately assigned cluster owner/read domain. No second authority or per-database catalog-owner redesign is introduced.

## 3. Future lifecycle and resource contracts

These proposed contracts belong in `internal/engine`. Session, Operation and Admission have private implementation state; no equivalent Session implementation exists in the inspected baseline.

```go
type SessionID uint64
type DatabaseID uint64
type SessionState uint8 // Initializing, Live, Ending, Ended
type EndReason uint8 // CleanClose, InitFailure, InvalidConnection, Terminated, Abandoned
type ResourceKind uint8 // Settings, Temporary, PreparedSQL, Cursors, Authorization, Transaction, AdvisorySession

type SessionToken struct { owner *Engine; id SessionID }
type DatabaseTarget struct { owner *Engine; id DatabaseID } // retained incarnation-bound identity
type ResourceHandle struct { session SessionToken; kind ResourceKind; serial uint64 }
type SessionResource interface { EndSession(SessionToken, EndReason) error }
type SessionStart struct { ID SessionID; Admission *Admission }

func (e *Engine) StartSession(ctx context.Context, start SessionStart) (*Session, error)
func (s *Session) Engine() *Engine
func (s *Session) Token() SessionToken
func (s *Session) Target() DatabaseTarget
func (s *Session) Acquire(ctx context.Context) (*Operation, error)
func (op *Operation) Context() context.Context
func (op *Operation) Resolution() ResolutionContext
func (op *Operation) Attach(kind ResourceKind, owner SessionResource) (ResourceHandle, error)
func (s *Session) Detach(h ResourceHandle, reason EndReason) error
func (op *Operation) Done()
func (s *Session) RequestEnd(reason EndReason)
func (s *Session) WaitEnd(ctx context.Context) error
func (s *Session) Reuse(ctx context.Context) error
func (s *Session) IsLive() bool
func (e *Engine) Describe(op *Operation, stmt parser.Statement) (ParameterInfo, error)
func (e *Engine) Execute(op *Operation, stmt parser.Statement, args []driver.NamedValue) (Result, error)
```

### Operation lease and coherent resolution

Acquire checks caller context and linearizes under lifecycle synchronization, requiring Live and no detach cleanup gate. During synchronous Detach it rejects with an ordinary internal busy error, never admits use of the owner being removed. A successful Acquire returns one pointer-owned lease; never copy Operation's private state. Pass that `*Operation` to Describe/Execute and call idempotent Done on every path. Context combines caller cancellation with a driver-created terminal context, never retained dialing context. The lease covers preparation, execution and Ping. SQL errors/caller cancellation end the operation, not a healthy session; preserve statement atomicity, delegating transaction abort/restoration to its future owner.

Resolution returns an immutable coherent operation view using the catalog design's `ResolutionContext`: DatabaseName, CurrentUser, ExplicitPath, TempSchema, EnvironmentRevision and Access. Path slices/access views must not expose mutable session state. AUTHORIZATION supplies NamespaceAccess. Engine validates both operation owner and selected database target; raw SessionID does not authorize execution. Changes to settings/role/temp resolution affect later coherent views rather than mutating a retained operation view.

### Registration, transfer and removal

Attach registers **one feature owner per ResourceKind**, not one callback per SQL object. The owner keeps its actual objects through existing feature/catalog paths. Nil, duplicate, unknown-kind, foreign-owner or ending-session registration fails with an ordinary internal Go error without ownership transfer. Successful Attach transfers terminal cleanup responsibility. A private initialization lease allows attachments in Initializing; public Acquire does not.

Resource serials are nonzero/nonreused; exhaustion fails before mutation. **Quiescent Session.Detach** validates the exact token/kind/serial under lifecycle synchronization; foreign/mismatched handles fail. A known completed removal is an idempotent no-op, including after end; stale handles cannot remove replacements. Otherwise non-Live, active leases or in-flight detach cause ordinary internal busy/lifecycle errors before mutation; never call it from an active operation. Success gates new use, claims the entry, invokes EndSession synchronously outside locks, records/returns its error, then reopens the gate only if still Live. No draining wait or callback queue. Terminal sweep uses the same claim and waits for in-flight detach before remaining cleanup/admission release; no callbacks overlap. Detach callback errors join the stored terminal cleanup result, including when end races detach. Attach racing Ending either transfers before transition or rejects caller-owned. SQL RESET/DISCARD acts within providers, not terminal Detach.

User precedence on **2026-10-04: “Quiescent session Detach”** supersedes only the former `op.Detach` synchronous removal rule: move it to Session, reject while leases are active and block new use during cleanup. Synchronous cleanup/error reporting remains; no queue. Other ownership, lifecycle and SQL acceptance is unchanged.

| ResourceKind | Attachment responsibility | EndSession scope |
|---|---|---|
| Settings | Overrides and effective-default provider | Release this session's settings attachment only |
| Temporary | Owner temp namespace/resources; expose TempSchema through ResolutionContext | Drop/release owner temp only; TX owner supplies ON COMMIT events |
| PreparedSQL | Named definitions and ephemeral catalog invalidation subscriptions | Remove this session's SQL names/subscriptions only; these are not persistent DROP-blocking dependencies |
| Cursors | Named portals, position and held resources | Close this session's named portals only |
| Authorization | Validated role/access view | Release view; never delete committed principals or authorize SQL |
| Transaction | One opaque terminal delegate | Invoke future TX owner's terminal cleanup, not a new transaction engine |
| AdvisorySession | Locking-owned release-by-session-token delegate | Release only matching session holds, never peers or transaction locks globally |

EndSession cannot clear peers or sweep the Engine. Persistent catalog dependencies stay in the descriptor authority; session registries track resource lifetime only.

### Principal and transaction attachment

```go
type PrincipalRef struct { owner *Engine; id uint64 } // opaque authorization-owned identity
type RoleState struct { Authenticated, SessionUser, CurrentUser PrincipalRef; ResetRole PrincipalRef }
type AuthorizationAttachment interface {
    SessionResource
    Roles() RoleState
    ResolutionAccess() NamespaceAccess
}
type TransactionAttachment interface { SessionResource }
```

AUTHORIZATION supplies validated initialization/updates; session never accepts arbitrary client RoleState. Authenticated is immutable. SET ROLE changes CurrentUser only and does not reapply role defaults. SET SESSION AUTHORIZATION changes SessionUser and CurrentUser together, clearing the active SET ROLE selection to NONE. ResetRole retains the connection-time configured role; zero means current SessionUser. ROLE NONE uses SessionUser while RESET ROLE honors that configured reset baseline. Eligibility, membership, ACLs and exact errors remain authorization-owned.

There is at most one Transaction attachment. It delegates EndSession to future `PG18-ARCH-TX-VISIBILITY`; no Begin/commit/savepoint/publication interface or transaction state machine is invented here. Its future events integrate LOCAL settings restoration, cursor commit/abort and temporary ON COMMIT actions. BeginTx remains unsupported until TX-CORE.

## 4. Transitions and exactly-once teardown

| Event | Transition | Owner action and failure effects |
|---|---|---|
| Physical Connect | Absent → Initializing → Live | Precheck dialing ctx; allocate ID; obtain trusted authorization-owned principal; admit/reserve target+principal using ALLOW_CONNECTIONS → CONNECT → applicable limit; construct Session/default providers and attachments from that reservation; final context check; publish Live. Failure returns no live conn/registry entry, unwinds transferred owners and releases any reservation once. |
| Use / prepare / Ping | Live → Live | Acquire the pointer-owned lease/coherent context; call Done on that same lease on all paths without copying the Operation value. Non-Live rejects. Failed/canceled statements preserve preexisting session resources, peers and atomic committed state. |
| Borrower release | Live → Live | sql.Conn.Close returns physical conn to pool, not session end or admission decrement. |
| Pool reset/reuse | Live → Live, or unhealthy → Ending | ResetSession(ctx) calls Reuse: context/health check only, no implicit RESET, DISCARD or rollback. Non-Live/unhealthy returns driver.ErrBadConn at reset boundary and requests InvalidConnection end. Context errors remain ordinary adapter errors, but database/sql may ignore them; Acquire and adapter operation entry independently check caller context. IsValid is false for non-Live/unhealthy. Normal SQL errors are not ErrBadConn. |
| Explicit Session.Detach | Live → Live, unless end races | Reject active leases/non-Live/busy before mutation; gate new use, claim one owner, run synchronous callback outside locks, return error. A terminal request may enter Ending but cannot finish until callback completes. |
| RequestEnd | Initializing/Live → Ending | First request retains reason, stops operation/attachment admission, signals cancellation of admitted operations and elects one runner. Later requests neither replace reason nor repeat effects. Nonblocking even from active operation; never wait on one's own lease. |
| Runner completion | Ending → Ended | Drain operation/private initialization leases and in-flight Detach callback; claim remaining owners in order below; continue on errors; release admission/remove only matching token; store joined cleanup result and complete waiters. |
| Physical conn.Close | Initializing/Live/Ending → Ended | Request CleanClose, stop runtime cleanup registration, wait independently of query context, return stored cleanup result. Repeated Close is idempotent; competing terminal request may have won the reason. |
| Termination / invalidation | Initializing/Live → Ending → Ended | Same runner; never clear DSN-wide persistent state or peers. Signal consumer distinguishes query cancellation from terminal termination. |
| Actual abandonment | Initializing/Live → Ending → Ended | Best-effort runtime cleanup capsule requests Abandoned asynchronously through same election/runner. Idle pooling and borrower return are not abandonment. |

Terminal cleanup after lease/Detach drain is **Transaction → Cursors → PreparedSQL → Temporary → AdvisorySession → Settings → Authorization** for remaining owners. All callbacks run outside Driver, Engine and lifecycle locks and may not reenter Acquire/Attach/Detach/Done or synchronously WaitEnd. They must complete in-process resource release without external I/O. Scheduling/blocking algorithms remain feature-owned. Continue after errors, collect with `errors.Join` (including prior Detach callback errors), then release admission/remove the exact token/mark Ended. WaitEnd cancellation stops that waiter only. Admission remains counted through drain and callbacks.

Detach and terminal sweep share an exact-entry claim, so only one invokes each transferred owner. Old tokens cannot affect a newer session or database incarnation. Close/termination/abandonment races elect one runner and one admission release. Initialization failure after partial transfer uses the same ordering; owners whose Attach failed remain caller-owned. RequestEnd during initialization cancels/drains its private lease before cleanup, preventing late publication or callbacks against actively initialized state.

### Clean close versus abandoned state

Future adapter uses `runtime.AddCleanup` on conn with a separate capsule retaining Session. Neither capsule nor anything reachable through Session, registry or attached owners may refer back to conn (including indirectly via stmt/rows); retain engine-side resources, not adapter handles. Capsule asynchronously requests Abandoned. Explicit Close stops registration, but Stop cannot cancel an already queued callback; terminal election handles that race. Each adapter method keeps conn alive after its last protected use, including Close's Stop/wait, with `runtime.KeepAlive(conn)`.

GC cleanup is nondeterministic best effort, not prompt release or a deadline guarantee. database/sql can retain forgotten borrowers' physical connections. Deterministic cleanup requires explicit physical Close or termination. A pooled idle conn, elapsed idle time, borrower Close or canceled query must not trigger abandonment. Do not add idle sweeps/finalizers that kill healthy sessions. Process exit has no external durable session resources to recover.

Physical end releases owner temp, SQL prepared names, named cursors and session advisory holds, never persistent contents or another session's resources. WITH HOLD survives successful creating-transaction commit within its session; creating abort removes it, but later rollback does not destroy an already committed held cursor. Anonymous committed Snapshot/Rows retains the baseline Reset/Restore lifetime; named cursor cleanup is not retrofitted onto it.

### Reuse policy reconciliation

The specific DRIVER-SESSION requirement that SET survives same-physical reuse governs generic no-leak prose. Different physical sessions never inherit each other's state; a new borrower of **the same** physical session intentionally inherits its identity/settings/temp/prepared/held-cursor/advisory state. Automatic borrower isolation is not promised. Applications needing it must explicitly reset/discard implemented categories or replace the physical connection. Reuse is a health check, not implicit DISCARD or rollback. Transaction completion belongs to the future transaction owner, not pool reuse.

## 5. Initial logical database attachment and native control scope

Proposed selector/API belongs to future DRIVER-DATABASE-SELECTION, not an API change now:

```go
func (d *Driver) OpenDatabaseConnector(dsn, database string) (driver.Connector, error)
type DatabaseSelector struct { Name string; Default bool }
func (e *Engine) DefaultDatabase() DatabaseTarget
func (e *Engine) AdmitDatabase(ctx context.Context, selector DatabaseSelector, principal PrincipalRef) (*Admission, error)
func (a *Admission) Target() DatabaseTarget
func (a *Admission) Release()
func (e *Engine) Reset(ctx context.Context, target DatabaseTarget) error
func (e *Engine) Dump(ctx context.Context, target DatabaseTarget, dst io.Writer) error
func (e *Engine) Restore(ctx context.Context, target DatabaseTarget, src io.Reader) error
```

DSN remains the exact opaque Engine map key; database is an exact decoded name, not libpq/SQL text. Default=true ignores Name and selects stable default identity; explicit empty name is missing name, not default. Logical introduction registers initial default `postgres` and moves existing persistent state unchanged into it. OpenConnector uses Default=true. No role selector is invented: AUTHORIZATION/ROLE-CORE resolves the trusted connection principal through its configured provider **before admission**, independently of mutable session CurrentUser. Zero/foreign/unvalidated principal authority fails ordinary internal errors, never grants exemptions; callers cannot supply exemption booleans.

AdmitDatabase resolves the target and makes ordered checks plus slot reservation **one atomic admission decision** against coherent database/authorization state: **ALLOW_CONNECTIONS → CONNECT → applicable connection limit → retain/reserve target+principal**. AUTHORIZATION computes privileges/superuser status from validated PrincipalRef; superusers bypass CONNECT/limit but not ALLOW_CONNECTIONS=false. Rejection transfers no slot. Admission is opaque, noncopyable, Engine-owned and carries exact principal+target; Release is idempotent. StartSession consumes a nonnil same-Engine reservation once, assumes responsibility on valid submission success/failure and initializes principal/default attachments from its retained identity before publication—no re-selection/caller override. Missing providers do not fabricate role support. Nil/foreign/already-consumed reservation fails ordinary internal errors without releasing another slot. Caller releases reservations never submitted; context/initialization failure releases owned reservations once.

Counts include reserved, initializing, live and ending physical sessions (including exempt principals), never borrower handles. Admission precedes Session/default construction; earlier numeric ID reservation may leave a gap on rejection. Database/authorization consumers own SQL admission diagnostics; ordinary adapter/context errors stay ordinary. Sequential ordinary-principal limit rejection is deterministic; PostgreSQL's approximate concurrent limits promise no portable simultaneous winner.

User precedence on **2026-10-04: “Use PostgreSQL 18 order”** supersedes the former principal-free AdmitDatabase and pre-principal limit rejection/CONNECT-at-StartSession placement. Trusted principal resolution and ALLOW_CONNECTIONS/CONNECT/applicable-limit checks now precede reservation and Session construction, following `REL_18_0 postinit.c` `CheckMyDatabase`. Defaults/attachments still initialize afterward; once-only unwind and all other acceptance remain unchanged.

Explicit connector retains a name selector and resolves it on each Connect. Rename/drop affects new explicit connections, not live attachments. Default selector follows the initial stable identity across rename. Dropping default makes new default admission fail; no fallback or recreation. DatabaseID is Engine-local monotonic/nonzero/nonreused; exhaustion fails before mutation. Recreating a name gives a fresh identity. Captured DatabaseTarget validates owner/incarnation/existence; stale/dropped targets fail, never redirect or resurrect.

### Native controls

Public `Reset(ctx, db)`, `Dump(ctx, db, dst)` and `Restore(ctx, db, src)` signatures remain unchanged. Replace future withEngine with withDatabaseTarget: Raw captures/retains Engine plus selected stable target before returning borrowed conn. Never retain borrower/session state for external I/O.

| Control scope | Decision |
|---|---|
| Selected persistent descriptors/data/catalog epoch | Reset clears; Dump serializes supported persistent state; Restore atomically replaces after private supported CREATE/INSERT staging through the same authority |
| Engine/database identity and registration/name/owner/admission/default metadata | Retained, not recreated or wiped |
| Other databases, cluster roles/memberships | Unchanged and excluded from Dump/Restore |
| Live settings, role views, temp, SQL prepared names, named cursors, advisory state | Unchanged and excluded; persistent binding invalidation may occur but does not delete named prepared entries |
| Anonymous committed result snapshots | Existing retained rows/metadata survive replacement |

Native controls are fixture replacement, not SQL RESET/DISCARD, CREATE/DROP DATABASE or cluster wipe. Preserve default-pool persistent behavior, native-only/wrapped/foreign rejection, nil/context/I/O error boundaries and atomic failure preservation. Neither target locks nor a borrowed conn span external reader/writer I/O; arbitrarily blocking I/O cannot be forcibly canceled. Target captured before release cannot redirect through later use. Failed read/parse/execute/cancel does not publish partial replacement. Active transaction publication and named held-cursor integration consume TX-VISIBILITY; this artifact does not decide those visibility conflicts.

## 6. Clean-cutover caller map (future implementation only)

| File / symbols | Required migration or intentional retention |
|---|---|
| `driver.go`: NewDriver, Open, OpenConnector, connector.Connect | Preserve Driver map/DSN; add synchronized ID allocator, trusted principal-provider handoff, selector and ordered AdmitDatabase at sole physical creation seam; StartSession consumes retained principal+target and unwinds once |
| `conn.go`: conn, check, Close, Ping, PrepareContext, execute, Begin/BeginTx, ExecContext/QueryContext | Own Session instead of redundant execution Engine; Session.Engine supplies owner. Lease preparation/execution/Ping, end on physical Close, add ResetSession/IsValid. BeginTx stays unsupported until TX-CORE; legacy calls preserve conversion/routes |
| `stmt.go`: check, Close, ExecContext/QueryContext, Exec/Query | Keep existing conn-bound AST/count handles and conn.execute route; Close only driver handle, not SQL PREPARE ownership |
| `internal/engine/engine.go`: Describe/Execute, CatalogRead, state/storage | Current Describe takes AST, Execute takes context; neither has session resolution. Replace execution signatures with `*Operation`, validate selected target and preserve the existing descriptor authority/binder/coercion/atomicity |
| `internal/engine/control.go`: Reset/Dump/Restore, staged Execute; `catalog.go`: ReplaceCatalog | Current controls use whole-Engine catalog replacement/capture; pass selected stable target and reuse ReplaceCatalog authority. Private restore session/operation ends on every path, never published as a user session |
| `control.go`: withEngine and three callbacks | Replace declaration/all callers with withDatabaseTarget, no alias/compatibility overload; retain public API/error ordering/borrower release |
| `internal/engine/{engine_test,catalog_test}.go`: helpers, Describe/Execute and internal controls | Explicitly start/clean default test sessions and pass targets/operations; preserve existing expectations |
| `control_test.go`, `driver_test.go`, `reference/controls_test.go`, `reference/fixtures_test.go`, `smoke/{smoke,error_fields,insert_implicit}_test.go` | Public controls intentionally keep signatures/behavior; add independent lifetime-aware witnesses in future consumers |
| `rows.go`: newRows; `internal/engine/snapshot.go` | Intentionally retain anonymous result semantics; named portal parser/dispatch belongs to cursor consumers |

Future implementation must repeat the complete Go caller inventory `\.(Describe|Execute|Reset|Dump|Restore)\(`, classify public root versus internal Engine receivers, and migrate every Engine receiver including stage/test helpers. Exclude unrelated Builder.Reset. Search `withEngine` and remove declaration/comment/callers, not keep shims. Parser extensions belong to each SQL consumer; this design widens no unsupported SQL.

## 7. Bounded future implementation sessions

Each row is a future consumer, not another deliverable or a new prerequisite edge. Reuse the contracts above rather than introduce parallel ownership.

| Backlog symbol | Bounded ownership/interface work | Independent acceptance witness |
|---|---|---|
| PG18-DRIVER-SESSION | Identity, operation leases, health reuse and end; one actual application_name provider | Two held connections SET/SHOW independently; same-physical reuse retains value; physical replacement resets defaults |
| PG18-ADMIN-SETTINGS-CORE | Expand settings registry/effective-default attachment | RESET restores independently configured effective default; no cross-session changes |
| PG18-DRIVER-SETTINGS-LOCAL | Consume TX restoration events | LOCAL ends/restores at transaction/savepoint boundaries; failed statement does not wipe prior state |
| PG18-ROLE-CORE | Trusted principal resolution before admission; session authorization attachment afterward | Same admitted principal becomes authenticated/session identity; rejected resolution/initialization leaks no slot |
| PG18-ROLE-SETTINGS | New-session role default provider | Changed defaults apply to new sessions, not existing ones |
| PG18-ROLE-SWITCH | Current role updates/coherent access view | SET ROLE changes current only, no default reapplication; failed switch preserves view |
| PG18-ARCH-AUTHORIZATION | Membership/ACL/eligibility/diagnostics | Independently asserted permissions and release errors, not lifecycle-generated authorization |
| PG18-DDL-TEMPORARY | TempSchema and owner cleanup; TX ON COMMIT events | A/B shadow persistent t independently; A end preserves B/persistent; preserve/delete/drop/rollback variants |
| PG18-DRIVER-PREPARED | Session named definitions/subscriptions | Same name across A/B; deallocate A leaves B; exact rows/metadata and duplicate/missing errors |
| PG18-DRIVER-PREPARED-REBIND | Catalog/path/environment invalidation | Retain name while invalidating/rebinding changed dependency/path |
| PG18-DRIVER-CURSOR | Named ordinary portals/position/lifetime | Ordered FETCH progression, EOF metadata, transaction end invalidation |
| PG18-DRIVER-CURSOR-HOLD | TX commit retention/creating abort | Remaining rows survive creating commit, not creating abort; later rollback preserves committed held cursor |
| PG18-DRIVER-CURSOR-MOVE | Position operations on same portal owner | Independently specified movement/count and following FETCH |
| PG18-DRIVER-DISCARD | Explicit category operations retaining attachments | RESET ALL settings-only; DISCARD PLANS retains prepared names; DISCARD TEMP owner-only; DISCARD ALL exact effects/prohibition |
| PG18-TX-CORE; PG18-ARCH-TX-VISIBILITY | Opaque transaction attachment, visibility/publication/restoration | Real transaction behavior plus LOCAL/temp/cursor/native publication integration owned there |
| PG18-TX-ADVISORY-SESSION; PG18-ARCH-LOCKING | Session token/release delegation and locking manager | Reentrant counts/rollback retention; A end releases A only, with no scheduling promise here |
| PG18-ADMIN-SESSION-SIGNAL | Operation cancellation versus terminal request | Cancel preserves healthy session; terminate elects same cleanup runner |
| PG18-CONTROL-DATABASE-CREATE | Registry/default migration | Existing committed baseline survives in initial postgres; new database identity is distinct |
| PG18-DRIVER-DATABASE-SELECTION | Trusted-principal selector/admission and targeted controls | a/b same DSN isolate tables; ordinary/superuser/disabled/CONNECT-before-limit checks and no leaks; controls affect selected persistent target |
| PG18-CONTROL-DATABASE-ALTER | Metadata/admission flags | Rename preserves live identity; changed name/flags affect new admission |
| PG18-CONTROL-DATABASE-SETTINGS | New-session database defaults | New sessions observe changed defaults; live values remain unchanged |
| PG18-CONTROL-DATABASE-DROP | Eligible drop/termination | Old target cannot redirect to recreated name; peer/cluster state preserved |
| PG18-CONTROL-DATABASE-TEMPLATE | Committed persistent clone | Clone persistent state only, not settings/temp/roles/cursors/advisory ownership |

DISCARD ALL's future consumer must implement its documented CLOSE ALL, session authorization reset, RESET ALL, DEALLOCATE ALL, UNLISTEN, advisory unlock-all, plan/temp/sequence resets for applicable supported categories, with exact documented transaction prohibition. Do not substitute terminal Detach or a successful no-op for missing substantive resource behavior. The initial session consumer must prove one real SET/SHOW feature, not fake cleanup for unimplemented categories.

## 8. Independently expected future checks

These are **specifications, not executed evidence**. Use held sql.Conn handles and direct/database/sql-prepared paths, not random DB dispatch. Reuse the bounded serialized PostgreSQL 18.0 UTF8/C fixture, wrapped memory/pq observation, unwrapped admin and native controls; close handles/rows before fixture reset. Assert independent rows/types/counts before comparison; unordered results are bags. Reuse current `referenceDiagnostic`, `referenceExtractDiagnostic` and `referenceAssertDiagnostic` in `reference/fixture_test.go` for full English/field/position expectations; no second error transport. Exact new release diagnostics must be independently obtained/asserted, never copied from 20devel or inferred from parity.

1. **Identity/settings/reuse:** hold A/B concurrently on one selected pooled database (same Driver/DSN and resolved target). A SET application_name TO 'session-a', B to 'session-b'. Both direct and prepared SHOW return column `application_name`, type TEXT, exactly one respective string row. IDs are distinct/nonzero; Engine/target shared. In a separate max-open/max-idle=1 fixture without expiry, release A and reacquire the same physical ID: value remains 'session-a'. Another physical session never inherits it. RESET restores effective configured default (empty in no-override fixture). Replacement has new ID/default. Borrower isolation needs explicit reset/discard or replacement, not automatic wipe.
2. **Persistent survival:** sentinel(v INT)=7 remains readable by B and new C after A physical end and after all pools close/reopen on the same Driver/DSN. B resources remain unchanged. New Driver/other DSN/empty versus space DSN isolate data. Assert identity relationships, not PostgreSQL allocator numbers.
3. **Temporary ownership:** A/B temp t have 11/22 while public.t has 7. Owner unqualified reads yield 11/22, fresh C yields 7, qualified persistent lookup yields 7. A end leaves B=22 and persistent=7. Dump excludes temp data. TEMP/TX consumers independently cover ON COMMIT preserve/delete/drop and rollback.
4. **SQL prepared names:** persistent t(v INT)=7,8; A/B each PREPARE p(int) AS SELECT v FROM t WHERE v=$1. EXECUTE p(7)/p(8) returns column v, INT4 and one int64 row 7/8 respectively. DEALLOCATE on A leaves B usable. Same-session duplicate is 42P05; missing/deallocated name is 26000 with future release-asserted English/fields. Driver stmt.Close never DEALLOCATEs p.
5. **Named cursors:** over ORDER BY rows 1,2,3, FETCH 1 yields INT4 row 1, FETCH ALL yields 2,3, exhaustion yields zero rows with same metadata. Ordinary transaction end invalidates. WITH HOLD preserves remaining rows after creating commit; creating abort removes it; later rollback preserves an earlier committed held cursor. B's same name survives A CLOSE/end. Missing cursor is 34000 with future exact release diagnostics.
6. **Advisory ownership:** A holds the same session key twice; rollback retains both. B try-lock returns BOOL false until two unlocks or A physical end, then true. C's independent hold survives A end. No wait scheduling/algorithm or fake release proof is implied.
7. **Failure/interleaving:** deterministic barriers race Close/termination/abandonment with use/Attach/Detach. Detach while a lease is active returns busy with unchanged ownership; quiescent Detach gates new Acquire, invokes one synchronous callback and returns its exact error. End racing that callback waits for completion, includes its error, never repeats it, then cleans remaining owners after drain. Repeated completed removal is harmless; stale handles cannot remove replacement owners. Assert one terminal runner/admission decrement, no peer cleanup. Partial initialization after two transfers cleans both once; rejected Attach remains caller-owned, no live entry. Pre-canceled Connect returns context.Canceled/no admission; canceled Acquire/statement preserves prior settings/sentinel/B/atomicity without invalidating healthy session. Trigger capsule deterministically; real GC is best-effort only, no deadlines.
8. **Roles/defaults:** authorized A switch changes current_user only, not authenticated identity/session_user/B/defaults. Failed switch preserves all state. ROLE NONE and RESET ROLE honor distinct baselines. Changed role/database defaults affect newly initialized sessions only. TX supplies LOCAL/rollback events.
9. **Logical admission:** selected a/b on the same Driver/DSN share Engine but same-named t=11/22 cannot cross-read and survives pool close. Ordinary principal rejects at zero/full limit; trusted superuser admits despite that limit, but ALLOW_CONNECTIONS=false rejects either. An ordinary principal denied CONNECT and also over limit receives CONNECT denial, never the limit error. Source-grounded future expectations from `CheckMyDatabase` (not runtime observations): disabled admission FATAL/55000, `database "a" is not currently accepting connections`; CONNECT denial FATAL/42501, `permission denied for database "a"`, DETAIL `User does not have CONNECT privilege.`; applicable limit FATAL/53300, `too many connections for database "a"`. Consumers independently assert pinned-release English/fields and absence/presence, plus no slot/session/attachment leak on rejection/cancellation/init failure and same retained principal/target at publication. Borrower return retains slot; physical end frees it. Missing-name diagnostics remain independently specified by their owner. Rename preserves live identity; new explicit connects follow names; recreate cannot redirect old target. No portable simultaneous limit winner.
10. **Native scope/atomicity:** controls on a preserve b, cluster roles, A/B session resources and database metadata. Failed Restore/read/parse/execute/cancel keeps sentinel 77. Successful replacement makes new queries see 99 while retained ordered anonymous result still yields 1,2. Slow I/O permits peer operations without held target lock/borrower. Dump excludes session/role/cluster state. Native-only/nil/error boundaries remain exact. Active transaction publication conflicts remain future TX checks, not visibility decisions here.

## 9. Acceptance coverage and verification boundary

| Selected requirement | Decision coverage |
|---|---|
| Work (1): physical identity/Driver/DSN/pool | Sections 2, 4 and check 1 |
| Work (2): persistent versus all session ownership | Sections 2–3 and checks 2–6, 8 |
| Work (3): connect/use/reuse/end/failure/cancel/abandonment | Section 4 and checks 1, 7 |
| Work (4): attach/detach extension interfaces | Section 3; seven category rows; principal/TX boundaries |
| Work (5): clean cutover/native controls | Sections 5–6 and checks 9–10 |
| Work (6): bounded consumers/independent outcomes | Sections 7–8 |
| Added logical admission/cluster-role control scope | Sections 2, 5 and checks 9–10 |
| Acceptance: no claim of executable SQL/session/TX/lock support | Section 1 and all scenarios explicitly future-only |

This design settles ownership/lifecycle, not executable SQL/session/TX/lock support. All selected acceptance is mapped above; missing implementations remain their named backlog capabilities. No feature smoke applies: changing this document cannot exercise future Session interfaces or unsupported SQL, and invented scaffolds would not prove them. The root still runs full production verification, existing public smoke and independent design review; this document makes no passing-check claim.
