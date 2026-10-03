# PG18-ARCH-SESSION — Per-connection identity and lifecycle

## 1. Decision status and evidence

This is a completed **design decision artifact**, not implemented session support. Every type, signature, lifecycle hook and SQL scenario below is a future contract. The sole deliverable is this document. No production code, parser, API, tests or verification assets change. No SQL, build, test, lint, formatter, smoke command or production gate was executed for this design; the frozen plan declares `smoke_commands=[]`.

Decision: a physical driver connection owns one session over the existing exact-DSN-shared Engine. Pool borrowers do not own session lifetime. Explicit end, failed initialization, invalidation, termination and best-effort abandonment converge on one per-session cleanup protocol. Extend the existing execution and native-control seams rather than introduce a parallel binder, resolver, catalog or storage path.

Transaction visibility/publication, transaction state machines, authorization eligibility/ACLs, lock scheduling and resource SQL implementations remain with their named owners below. The selected record has no prerequisites; absent TX/LOCKING/AUTHORIZATION artifacts are future opaque boundaries, not implemented capabilities or new dependency edges.

### Inspected repository evidence

Paths and line anchors refer to the assigned worktree baseline:

- `driver.go:17-58`: Driver's mutex protects its exact opaque DSN-to-Engine map; Open delegates to OpenConnector/Connect. Connect checks context and creates a conn. There is no session allocator, admission registry or database selector.
- `conn.go:12-33,51-110`: conn owns Engine plus a closed flag; Close only sets the flag. Explicit transactions return unsupported `0A000`. Preparation calls Describe and execution calls Execute. No SessionResetter/Validator exists.
- `stmt.go:11-54`: driver statements retain conn, AST and parameter count; direct and legacy prepared paths reach conn.execute. They are not SQL PREPARE names.
- `control.go:11-65`: Raw captures the native Engine, rejects foreign/wrapped pools and releases the borrower before control I/O. Preserve public signatures and error ordering.
- `internal/engine/engine.go:13-53,245-343`: tables, binding, coercion and statement-atomic execution have one authority. SELECT captures committed rows and copied result metadata.
- `internal/engine/control.go:14-44,128-171`: Reset is atomic; Dump captures before external I/O; Restore privately stages supported CREATE/INSERT before replacement.
- `rows.go:12-43`, `internal/engine/snapshot.go:9-29`: anonymous result snapshots retain committed rows independently of the current table map. They are not named SQL portals.
- `docs/design/PG18-ARCH-CATALOG-IDENTITY.md:32-118`: reuse ObjectRef/epoch ownership, ResolutionContext and NamespaceAccess. These are also future contracts, not existing concrete engine types.
- `reference_test.go:17-64`: independent rows/types/counts expectations exist; error observation is presently SQLSTATE-only. `reference_helpers_test.go:20,57-110`: one bounded serialized reference container, wrapped memory/pq and privileged admin/native-control seams.
- Caller inventory search for `\.(Describe|Execute|Reset|Dump|Restore)\(` and `withEngine` identifies conn, root controls, staged Restore, internal engine tests and the public-control callers listed in section 6. `strings.Builder.Reset` is unrelated.

Supplied frozen research additionally anchors Go connector/pool contracts at `database/sql/driver/driver.go:123-146,230-255,295-315` and `database/sql/sql.go:600-630,2136-2159`, and AddCleanup/KeepAlive constraints at `runtime/mcleanup.go:16-83` in Go 1.27.1. Connect context is dialing-only; sql.Conn.Close returns a physical connection to the pool; reset runs before reuse and validation before pooling. Cleanup cannot retain its target and is nondeterministic, including at process exit.

Backlog grounding is the frozen research's bounded `.cswd/spec_parts.json` anchors: `266-298` (physical-session SET persistence/settings), `62-119` (architecture ownership), `578-600,968-1004` (roles), `1115-1135,1264-1281` (transactions/settings LOCAL), `1368-1383` (advisory), `1931-1947` (temp), `2067-2081` (signals), `2287-2321` (prepared), `4098-4163` (cursors/DISCARD), and `16877-16968` (databases). No catalog definitions are changed here.

### Version and reference policy

Release semantics use the official PostgreSQL 18 manuals, identified as **18.6** by supplied research: [SET](https://www.postgresql.org/docs/18/sql-set.html), [RESET](https://www.postgresql.org/docs/18/sql-reset.html), [DISCARD](https://www.postgresql.org/docs/18/sql-discard.html), [PREPARE](https://www.postgresql.org/docs/18/sql-prepare.html), [EXECUTE](https://www.postgresql.org/docs/18/sql-execute.html), [DEALLOCATE](https://www.postgresql.org/docs/18/sql-deallocate.html), [DECLARE](https://www.postgresql.org/docs/18/sql-declare.html#SQL-DECLARE-NOTES), [temporary tables](https://www.postgresql.org/docs/18/sql-createtable.html#SQL-CREATETABLE-TEMPORARY), [advisory locks §13.3.5](https://www.postgresql.org/docs/18/explicit-locking.html#ADVISORY-LOCKS), [SET ROLE](https://www.postgresql.org/docs/18/sql-set-role.html), [SET SESSION AUTHORIZATION](https://www.postgresql.org/docs/18/sql-set-session-authorization.html), [database ownership §22.1](https://www.postgresql.org/docs/18/manage-ag-overview.html), [ALTER DATABASE](https://www.postgresql.org/docs/18/sql-alterdatabase.html), [CREATE DATABASE connection limits](https://www.postgresql.org/docs/18/sql-createdatabase.html#CREATE-DATABASE-CONNLIMIT), and [SQLSTATE mappings](https://www.postgresql.org/docs/18/errcodes-appendix.html#ERRCODES-TABLE).

Preserve the real-reference runtime **18.0**, UTF8/C, `server_version_num=180000`, image `postgres:18.0-bookworm@sha256:3f55f8895c4ed50603e2fbdfc72fffeeaba3173321fee5cb825bbbeb30d9d854`. Minor-specific 18.6 cases consume `PG18-REF-ORACLE-18-6`; do not invent a digest or upgrade unrelated fixtures. Local PostgreSQL **20devel** sources are research only, never the release oracle or evidence of exact English diagnostics. New release diagnostics are not observed here and must be independently obtained/asserted by each future consumer.

## 2. Identity and ownership invariants

Driver allocates monotonically increasing, nonzero `SessionID uint64` under its ownership synchronization for each physical Connect attempt that reaches initialization; each successful Connect publishes exactly one unique session. Allocation is never per connector, borrower, Ping or statement. IDs are never reused during that Driver's lifetime; failed initialization may leave gaps. Exhaustion fails before admission/publication with an ordinary internal Go error, never wraparound. Different Drivers may allocate equal numbers: a number alone is not authority.

Two simultaneously live physical connections using the same exact DSN share Engine and selected database but have different sessions. Reuse of the same physical connection preserves identity; replacement allocates a fresh identity. Empty DSN and single-space DSN differ. Closing one or all pools never erases committed data; reopening on the same Driver/DSN retains it. Another Driver isolates it.

| Domain/owner | Owned state | Boundary |
|---|---|---|
| Driver | Exact opaque DSN map; physical-session ID allocator | No DSN URI/libpq parsing; no session allocation on pool borrow |
| DSN Engine | Embedded cluster domain: roles, database-name registry, stable default target; session registry | No global terminal sweep; Engine lifetime exceeds conn lifetime |
| Selected logical database | Persistent descriptors, storage, catalog epoch and metadata | Database-local ObjectRef authority; equal numeric refs in a/b are not interchangeable |
| Session | Mutable setting overrides/default-provider attachment, temp namespace/resources, SQL prepared-name and named cursor registries, role view, one opaque transaction attachment | Session token and immutable target qualify ownership; registries track lifetime, not competing semantic stores |
| Feature/catalog owners | Shared setting definitions/default providers; committed cluster roles; persistent dependency edges; actual resource objects | Session values do not become Engine.tables entries; persistent dependencies remain catalog-owned |
| Locking manager | Actual advisory holds keyed by database domain and session token | Session owns release delegation, not lock algorithms |
| Driver stmt | Conn-bound AST/count handle | stmt.Close releases driver handle, not SQL DEALLOCATE |
| Anonymous Rows/Snapshot | Retained immutable committed result rows/metadata | Not a named cursor; existing Reset/Restore retention remains unchanged |

Logical database introduction composes, rather than replaces, the catalog design: the DSN Engine remains the owner, while every read/ref/plan/stage also retains its selected DatabaseTarget. ObjectRef epoch/address is valid only inside that selected database incarnation. This refines the catalog artifact's single-database Engine domain here without editing that artifact or creating a second descriptor authority.

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
func (op *Operation) Detach(h ResourceHandle, reason EndReason) error
func (op *Operation) Done()
func (s *Session) RequestEnd(reason EndReason)
func (s *Session) WaitEnd(ctx context.Context) error
func (s *Session) Reuse(ctx context.Context) error
func (s *Session) IsLive() bool
func (e *Engine) Describe(op *Operation, stmt parser.Statement) (ParameterInfo, error)
func (e *Engine) Execute(op *Operation, stmt parser.Statement, args []driver.NamedValue) (Result, error)
```

### Operation lease and coherent resolution

Acquire linearizes under lifecycle synchronization and requires Live. A successful Acquire returns a pointer-owned, single-owner lease: the Operation value and its private lease/synchronization state must never be copied. Callers pass the returned `*Operation` directly to Describe/Execute and invoke methods, including Done, on that same lease; copying a pointer does not create another lease or transfer ownership. Done is idempotent on that lease. Its context combines caller statement cancellation with a driver-created terminal context, never the retained dialing context. Lease lifetime covers Describe/Execute, preparation and Ping; all paths release it. SQL errors and caller cancellation end the operation, not a healthy session. Preserve current statement-atomic committed execution; transaction abort/restoration later comes from the transaction owner.

Accepted review correction `PG18-ARCH-SESSION-001`: this document's `Acquire(ctx context.Context) (*Operation, error)` is the authoritative consumer contract and supersedes only the value-return signature in the frozen proposal at `.cswd/parts/PG18-ARCH-SESSION.md`. The proposal remains unchanged; all other scope, ownership and lifecycle decisions remain intact.

Resolution returns an immutable coherent operation view using the catalog design's `ResolutionContext`: DatabaseName, CurrentUser, ExplicitPath, TempSchema, EnvironmentRevision and Access. Path slices/access views must not expose mutable session state. AUTHORIZATION supplies NamespaceAccess. Engine validates both operation owner and selected database target; raw SessionID does not authorize execution. Changes to settings/role/temp resolution affect later coherent views rather than mutating a retained operation view.

### Registration, transfer and removal

Attach registers **one feature owner per ResourceKind**, not one callback per SQL object. The owner keeps its actual objects through existing feature/catalog paths. Nil, duplicate, unknown-kind, foreign-owner or ending-session registration fails with an ordinary internal Go error without ownership transfer. Successful Attach transfers terminal cleanup responsibility. A private initialization lease allows attachments in Initializing; public Acquire does not.

Resource serials are nonzero and never reused in a session; exhaustion fails before mutation. Detach atomically claims the exact token/kind/serial entry and invokes EndSession once outside locks. Repeating removal of that already detached handle is harmless. Foreign/mismatched handles fail, and an old handle cannot remove a replacement owner. Terminal sweep claims the same entries. Attach racing Ending either transfers before the transition and joins the sweep, or rejects and stays caller-owned. Explicit SQL RESET/DISCARD acts within its attached provider; it is not terminal Detach and does not destroy the provider needed for subsequent SQL.

| ResourceKind | Attachment responsibility | EndSession scope |
|---|---|---|
| Settings | Overrides and effective-default provider | Release this session's settings attachment only |
| Temporary | Owner temp namespace/resources; expose TempSchema through ResolutionContext | Drop/release owner temp only; TX owner supplies ON COMMIT events |
| PreparedSQL | Named definitions and catalog dependency subscriptions | Remove this session's SQL names/subscriptions only |
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

AUTHORIZATION supplies validated initialization/updates; session never accepts arbitrary client RoleState. Authenticated is immutable. SET ROLE changes CurrentUser only and does not reapply role defaults. SET SESSION AUTHORIZATION changes mutable SessionUser/CurrentUser. ResetRole retains the connection-time configured role; zero means current SessionUser. ROLE NONE uses SessionUser while RESET ROLE honors the configured reset baseline. Eligibility, membership, ACLs and exact errors remain authorization-owned.

There is at most one Transaction attachment. It delegates EndSession to future `PG18-ARCH-TX-VISIBILITY`; no Begin/commit/savepoint/publication interface or transaction state machine is invented here. Its future events integrate LOCAL settings restoration, cursor commit/abort and temporary ON COMMIT actions. BeginTx remains unsupported until TX-CORE.

## 4. Transitions and exactly-once teardown

| Event | Transition | Owner action and failure effects |
|---|---|---|
| Physical Connect | Absent → Initializing → Live | Precheck dialing ctx; allocate ID; resolve/reserve admission; initialize supported principal/default providers and attachments; final context check; publish Live and return conn. Failure unwinds transferred owners only, releases admission once and returns no live conn/registry entry. |
| Use / prepare / Ping | Live → Live | Acquire the pointer-owned lease/coherent context; call Done on that same lease on all paths without copying the Operation value. Non-Live rejects. Failed/canceled statements preserve preexisting session resources, peers and atomic committed state. |
| Borrower release | Live → Live | sql.Conn.Close returns physical conn to pool, not session end or admission decrement. |
| Pool reset/reuse | Live → Live, or unhealthy → Ending | ResetSession(ctx) calls Reuse: context/health check only, no implicit RESET, DISCARD or rollback. Non-Live/unhealthy returns driver.ErrBadConn at reset boundary and requests InvalidConnection end. Context errors stay ordinary context errors. IsValid is false for non-Live/unhealthy. Normal SQL errors are not ErrBadConn. |
| RequestEnd | Initializing/Live → Ending | First request retains reason, stops operation/attachment admission, signals cancellation of admitted operations and elects one runner. Later requests neither replace reason nor repeat effects. Nonblocking even from active operation; never wait on one's own lease. |
| Runner completion | Ending → Ended | Drain operation/private initialization leases; claim callbacks in order below; continue on errors; release admission and remove only matching session token; store joined result and complete waiters. |
| Physical conn.Close | Initializing/Live/Ending → Ended | Request CleanClose, stop runtime cleanup registration, wait independently of query context, return stored cleanup result. Repeated Close is idempotent; competing terminal request may have won the reason. |
| Termination / invalidation | Initializing/Live → Ending → Ended | Same runner; never clear DSN-wide persistent state or peers. Signal consumer distinguishes query cancellation from terminal termination. |
| Actual abandonment | Initializing/Live → Ending → Ended | Best-effort runtime cleanup capsule requests Abandoned asynchronously through same election/runner. Idle pooling and borrower return are not abandonment. |

Cleanup order after lease drain is **Transaction → Cursors → PreparedSQL → Temporary → AdvisorySession → Settings → Authorization**. Callbacks run outside Driver, Engine and lifecycle locks; they may not reenter operations or synchronously WaitEnd. They must complete in-process resource release without external I/O. Scheduling/blocking algorithms remain their owning features' scope. Continue after errors, collect with `errors.Join`, and still release admission/remove the exact registry token/mark Ended. WaitEnd cancellation ends that waiter only, never cleanup. Registry admission remains counted through draining and callbacks.

Detach and terminal sweep share an exact-entry claim, so only one invokes each transferred owner. Old tokens cannot affect a newer session or database incarnation. Close/termination/abandonment races elect one runner and one admission release. Initialization failure after partial transfer uses the same ordering; owners whose Attach failed remain caller-owned. RequestEnd during initialization cancels/drains its private lease before cleanup, preventing late publication or callbacks against actively initialized state.

### Clean close versus abandoned state

Future adapter uses `runtime.AddCleanup` on conn with a separate capsule retaining Session but **no reference back to conn, stmt or rows**. Engine registry retains Session/capsule only, not conn. Capsule asynchronously calls RequestEnd(Abandoned). Explicit Close stops its cleanup registration; the same terminal election handles a racing callback. Adapter methods use `runtime.KeepAlive(conn)` to prevent premature cleanup while operating.

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
func (e *Engine) AdmitDatabase(ctx context.Context, selector DatabaseSelector) (*Admission, error)
func (a *Admission) Target() DatabaseTarget
func (a *Admission) Release()
func (e *Engine) Reset(ctx context.Context, target DatabaseTarget) error
func (e *Engine) Dump(ctx context.Context, target DatabaseTarget, dst io.Writer) error
func (e *Engine) Restore(ctx context.Context, target DatabaseTarget, src io.Reader) error
```

DSN remains the exact opaque Engine map key. Database is an exact already decoded name, not libpq text or SQL to parse. Default=true ignores Name and selects stable default identity; explicit empty name is missing name, not default. Logical introduction registers initial default `postgres` and moves existing tables unchanged into it. OpenConnector uses Default=true. No new role selector is invented: AUTHORIZATION/ROLE-CORE supplies validated principal-provider integration at initialization.

AdmitDatabase atomically resolves/checks admission flags and reserves a physical-session slot against a retained target identity. Admission is opaque, noncopyable and Engine-owned; Release is idempotent. StartSession consumes a nonnil same-Engine reservation exactly once and assumes responsibility on valid submission whether success or failure. Principal/CONNECT authorization and default-provider checks precede publication; absent providers do not fabricate role support. Nil/foreign/already-consumed reservation fails ordinary internal errors without releasing someone else's slot. Caller releases any reservation never submitted. Context failure before or after reservation must not leak a slot.

Counts include reserved, initializing, live and ending physical sessions, never borrower handles. ALLOW_CONNECTIONS/limit checks precede session allocation inside StartSession; authorization supplies role-based exemptions and CONNECT checks, with rejection/unwind before publication. Missing/disabled/limited/denied SQL diagnostics belong to database/authorization consumers, not fabricated driver SQLSTATE. Sequential non-superuser limits have deterministic rejection; PostgreSQL's approximate simultaneous limits do not provide a portable exact winner to assert.

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
| `driver.go`: NewDriver, Open, OpenConnector, connector.Connect | Preserve Driver map/DSN identity; add synchronized allocator, selector, admission and StartSession at sole physical creation seam |
| `conn.go`: conn, check, Close, Ping, PrepareContext, execute, Begin/BeginTx, ExecContext/QueryContext | Own Session instead of redundant execution Engine; Session.Engine supplies owner. Lease preparation/execution/Ping, end on physical Close, add ResetSession/IsValid. BeginTx stays unsupported until TX-CORE; legacy calls preserve conversion/routes |
| `stmt.go`: check, Close, ExecContext/QueryContext, Exec/Query | Keep conn-bound validity and conn.execute route; Close only driver handle. No duplicate binder or SQL PREPARE registry here |
| `internal/engine/engine.go`: Describe/Execute | Cleanly replace signatures with `*Operation` contracts; pass the pointer returned by Acquire directly, never copy the Operation value; validate target; preserve binding/coercion/atomicity; migrate conn callers |
| `internal/engine/control.go`: Reset/Dump/Restore, staged Execute | Pass selected stable target; stage through a private default session/operation ended on every path, never published as user session |
| `control.go`: withEngine and three callbacks | Replace declaration/all callers with withDatabaseTarget, no alias/compatibility overload; retain public API/error ordering/borrower release |
| `internal/engine/engine_test.go`: run, Describe/Execute and internal controls | Explicitly start/clean default test sessions and pass targets/operations; do not bypass session context or weaken expected outcomes |
| `control_test.go`, `driver_test.go`, `reference_test.go`, `cmd/smoke/main.go` | Public control calls intentionally keep signatures and baseline behavior; future consumers add independent lifetime-aware witnesses |
| `rows.go`: newRows; `internal/engine/snapshot.go` | Intentionally retain anonymous result semantics; named portal parser/dispatch belongs to cursor consumers |

Future implementation must repeat the complete Go caller inventory `\.(Describe|Execute|Reset|Dump|Restore)\(`, classify public root versus internal Engine receivers, and migrate every Engine receiver including stage/test helpers. Exclude unrelated Builder.Reset. Search `withEngine` and remove declaration/comment/callers, not keep shims. Parser extensions belong to each SQL consumer; this design widens no unsupported SQL.

## 7. Bounded future implementation sessions

Each row is a future consumer, not another deliverable or a new prerequisite edge. Reuse the contracts above rather than introduce parallel ownership.

| Backlog symbol | Bounded ownership/interface work | Independent acceptance witness |
|---|---|---|
| PG18-DRIVER-SESSION | Identity, operation leases, health reuse and end; one actual application_name provider | Two held connections SET/SHOW independently; same-physical reuse retains value; physical replacement resets defaults |
| PG18-ADMIN-SETTINGS-CORE | Expand settings registry/effective-default attachment | RESET restores independently configured effective default; no cross-session changes |
| PG18-DRIVER-SETTINGS-LOCAL | Consume TX restoration events | LOCAL ends/restores at transaction/savepoint boundaries; failed statement does not wipe prior state |
| PG18-ROLE-CORE | Validated principal initialization/session authorization attachment | Authenticated/session/current relationships and rejected initialization without leak |
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
| PG18-DRIVER-DATABASE-SELECTION | Selector/admission/targeted native controls | a/b same DSN isolate tables; limits/no-leak failures; controls affect selected persistent target |
| PG18-CONTROL-DATABASE-ALTER | Metadata/admission flags | Rename preserves live identity; changed name/flags affect new admission |
| PG18-CONTROL-DATABASE-SETTINGS | New-session database defaults | New sessions observe changed defaults; live values remain unchanged |
| PG18-CONTROL-DATABASE-DROP | Eligible drop/termination | Old target cannot redirect to recreated name; peer/cluster state preserved |
| PG18-CONTROL-DATABASE-TEMPLATE | Committed persistent clone | Clone persistent state only, not settings/temp/roles/cursors/advisory ownership |

DISCARD ALL's future consumer must implement its documented CLOSE ALL, session authorization reset, RESET ALL, DEALLOCATE ALL, UNLISTEN, advisory unlock-all, plan/temp/sequence resets for applicable supported categories, with exact documented transaction prohibition. Do not substitute terminal Detach or a successful no-op for missing substantive resource behavior. The initial session consumer must prove one real SET/SHOW feature, not fake cleanup for unimplemented categories.

## 8. Independently expected future checks

These are **specifications, not executed evidence**. Future consumers use held sql.Conn handles and direct/database/sql-prepared paths, not randomly dispatched DB statements. Reuse the single bounded serialized PostgreSQL 18.0 UTF8/C fixture, wrapped memory/pq observation, privileged unwrapped admin and native memory controls. Close handles/rows before fixture reset. Specify rows, type names, affected counts and errors independently before engine comparison; unordered results are bags with multiplicities. Extend current SQLSTATE-only observation to English messages and exposed fields/positions. The codes below have release-manual grounding; exact new diagnostic text/fields/positions must be captured and independently asserted against the pinned release, never copied from 20devel or inferred from parity.

1. **Identity/settings/reuse:** hold A/B concurrently on one pool. A SET application_name TO 'session-a', B to 'session-b'. Both direct and prepared SHOW return column `application_name`, type TEXT, exactly one respective string row. IDs are distinct/nonzero; Engine/target shared. In a separate max-open/max-idle=1 fixture without expiry, release A and reacquire the same physical ID: value remains 'session-a'. Another physical session never inherits it. RESET restores effective configured default (empty in no-override fixture). Replacement has new ID/default. Borrower isolation needs explicit reset/discard or replacement, not automatic wipe.
2. **Persistent survival:** sentinel(v INT)=7 remains readable by B and new C after A physical end and after all pools close/reopen on the same Driver/DSN. B resources remain unchanged. New Driver/other DSN/empty versus space DSN isolate data. Assert identity relationships, not PostgreSQL allocator numbers.
3. **Temporary ownership:** A/B temp t have 11/22 while public.t has 7. Owner unqualified reads yield 11/22, fresh C yields 7, qualified persistent lookup yields 7. A end leaves B=22 and persistent=7. Dump excludes temp data. TEMP/TX consumers independently cover ON COMMIT preserve/delete/drop and rollback.
4. **SQL prepared names:** persistent t(v INT)=7,8; A/B each PREPARE p(int) AS SELECT v FROM t WHERE v=$1. EXECUTE p(7)/p(8) returns column v, INT4 and one int64 row 7/8 respectively. DEALLOCATE on A leaves B usable. Same-session duplicate is 42P05; missing/deallocated name is 26000 with future release-asserted English/fields. Driver stmt.Close never DEALLOCATEs p.
5. **Named cursors:** over ORDER BY rows 1,2,3, FETCH 1 yields INT4 row 1, FETCH ALL yields 2,3, exhaustion yields zero rows with same metadata. Ordinary transaction end invalidates. WITH HOLD preserves remaining rows after creating commit; creating abort removes it; later rollback preserves an earlier committed held cursor. B's same name survives A CLOSE/end. Missing cursor is 34000 with future exact release diagnostics.
6. **Advisory ownership:** A holds the same session key twice; rollback retains both. B try-lock returns BOOL false until two unlocks or A physical end, then true. C's independent hold survives A end. No wait scheduling/algorithm or fake release proof is implied.
7. **Failure/interleaving:** deterministic barriers race Close/termination/abandonment with active use/Attach/Detach. Exactly one runner, callbacks only after lease drain, exactly one callback per transferred owner and one admission decrement; no peer cleanup/stale-token effect. Initialization failure after two successful attachments cleans both once; rejected registration stays caller-owned and no live entry is published. Pre-canceled Connect returns context.Canceled/no admission. Canceled statement preserves earlier settings/sentinel/B and multirow atomicity. Trigger capsule deterministically for race units; never assert GC collection deadlines. Real GC may only provide best-effort smoke.
8. **Roles/defaults:** authorized A switch changes current_user only, not authenticated identity/session_user/B/defaults. Failed switch preserves all state. ROLE NONE and RESET ROLE honor distinct baselines. Changed role/database defaults affect newly initialized sessions only. TX supplies LOCAL/rollback events.
9. **Logical admission:** selected a/b share Engine but same-named t=11/22 cannot cross-read and survives pool close. Sequential non-superuser limit rejects an extra physical connection with no leaked slot; borrower return does not free one, physical end does. Missing/disabled/denied/limited admission each requires owning consumer's independent SQLSTATE/English/fields. Rename preserves live identity; new explicit connects follow names; drop/recreate cannot redirect old target. Do not assert a portable simultaneous limit winner.
10. **Native scope/atomicity:** controls on a preserve b, cluster roles, A/B session resources and database metadata. Failed Restore/read/parse/execute/cancel keeps sentinel 77. Successful replacement makes new queries see 99 while retained ordered anonymous result still yields 1,2. Slow I/O permits peer operations without held target lock/borrower. Dump excludes session/role/cluster state. Native-only/nil/error boundaries remain exact. Active transaction publication conflicts remain future TX checks, not visibility decisions here.

## 9. Acceptance coverage and authorship limitations

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

This design settles ownership and lifecycle only. Missing exact release diagnostics remain mandatory future consumer evidence, not current passing coverage. No scaffolds, tests, extra documents, catalog mutations or prerequisite implementations accompany it. Coordinator-owned production verification and independent review are not claimed here.

Prewalk: the starting session read skill, selected policy, research, frozen plan and relevant assigned-worktree code and initialized a concrete implementation plan before the first deliverable write. Configured @smol continuation is externally owned. No runtime routing metadata establishing handoff, same-configuration resolution or disabled override was available at authorship; no model/configuration was changed and no handoff is claimed. That unavailable provenance does not imply a design behavior blocker.
