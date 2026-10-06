# stuntgres

A process-local, embedded PostgreSQL-compatible Go `database/sql` driver for a deliberately bounded SQL subset. Production code uses only the Go standard library; no server, network, container, filesystem storage, or durability is involved. Requires Go 1.26 or later.

```go
import (
    "context"
    "database/sql"
    _ "stuntgres"
)

func example() error {
    db, err := sql.Open("stuntgres", "example")
    if err != nil { return err }
    defer db.Close()
    _, err = db.ExecContext(context.Background(), "CREATE TABLE people (id INTEGER NOT NULL, name TEXT)")
    return err
}
```

## Database names and lifetime

The DSN is an **exact opaque string**, not a libpq connection string or network address. Identical strings (including the empty string) within the same Driver share an engine; different strings or Driver instances are isolated. The registered `stuntgres` driver uses one default Driver. Engines live as long as their Driver, even after every connection or pool closes. Use `NewDriver().OpenConnector(name)` with `sql.OpenDB` for independently owned fixtures. Nothing is shared between processes or persisted across process exit.

### Physical sessions

Each physical connection has independent typed session-setting state. Hold
`*sql.Conn` to target that session; a `*sql.DB` may dispatch to any physical
connection. Borrower Close returns it to the pool: healthy pool reuse preserves
its settings and identity, without implicit RESET, DISCARD or rollback.
Physical replacement starts with captured defaults and does not erase data.
Canceled statements, canceled pool-reuse checks and normal SQL errors leave
healthy sessions reusable, preserving physical identity and settings.
Explicit physical close drains operations and releases only that session's
owners; runtime abandonment cleanup is nondeterministic best effort.

`SET [SESSION | LOCAL] name {TO | =} value`, `RESET name`, `RESET ALL`, `SHOW name`,
and `SHOW ALL` use the ordinary direct/prepared execution route. Names are
ASCII-case-insensitive, including dotted custom names. SET values use the
existing ordinary/`E'...'`/`U&'...'`/dollar-quoted literal lexer, identifiers, booleans, signed
decimal-digit integer tokens or bare DEFAULT. DEFAULT and RESET restore
the captured physical-session default; quoted 'DEFAULT' is literal text.
SHOW returns one string row with the canonical setting-name TEXT column.
SHOW ALL returns sorted `name,setting,description` TEXT columns for the exact
supported registry, **not** PostgreSQL's entire parameter catalog.
SET/RESET and Exec of SHOW affect zero rows. The selected FROM-less calls
`SELECT current_setting(text)` and `SELECT set_config(text,text,boolean)`
return one nullable TEXT row, with affected count one for Exec.
SHOW, SHOW ALL and setting-function rows own immutable anonymous snapshots:
statement completion releases the implicit transaction, not the returned rows.
Later setting changes or physical-session teardown do not change captured values.
Setting-function arguments accept strings, NULL, booleans in the third position,
and positional parameters through the existing binder. NULL current_setting
returns SQL NULL; NULL set_config name fails 22004; NULL value resets;
NULL is_local means false. Same-type repeated parameters work, gaps fail
42P18 and mixed-type repeated parameters remain unsupported 0A000.
Unsigned decimal tokens fitting PostgreSQL's int32 scanner range
are formatted canonically; larger tokens retain their original digits (including
leading zeros), with unary minus prepended and unary plus omitted.
Unquoted radix-prefixed and underscore-separated numeric setting values,
including signed forms, are unsupported (`0A000`) and cannot mutate settings.
Quoted `'0x2a'`, fractions and exponents reach the full typed setting input
rules; quoted `'1_000'` is valid text but invalid numeric configuration input.
Unquoted floating/exponent SET syntax remains unsupported. These deliberate
setting-grammar boundaries do not change INSERT/SELECT numeric support.
The application_name value clips original UTF8 to 63 bytes before escaping nonprintable/non-ASCII
bytes as lowercase literal `\xNN`. Clipping succeeds and emits a structured
NOTICE when a connector handler is configured and the session filter permits it.
SET reuses the existing literal decoding rules below: for example,
`SET application_name TO E'a\nb'` decodes the newline first, then SHOW returns
literal `a\x0ab`. No second setting-specific string decoder is used.

Supported builtins are deliberately bounded:

| Name | Default / access | Actual behavior |
| --- | --- | --- |
| application_name | empty / session | Normalized descriptive session value |
| client_min_messages | notice / session | Filters structured connector notices; debug aliases debug2 |
| search_path | `"$user", public` / session | Stored explicit path; effective lookup skips absent/inaccessible schemas and expands current user |
| standard_conforming_strings | on / session | Per-call ordinary-string decoding; E strings always escape |
| statement_timeout | 0 / session | Millisecond command-admission timer covering parse, Prepare and Execute |
| lock_timeout | 0 / session | Millisecond limit for each queued lock acquisition; 0 disables |
| server_encoding | UTF8 / read-only | Actual UTF8 invariant; writes and individual RESET fail 55P02 |
| log_statement_stats | off / privileged | Deny-only: all writes and individual RESET fail 42501; no logging activation |

Prepared literals retain Prepare-time string mode; prepared SHOW/functions read
Execute-time values. To retain a literal prepared before LOCAL within a transaction,
prepare it on that `sql.Tx` and execute the same handle: `Tx.Stmt` may reprepare
a handle created on `sql.Conn` using the transaction's current string mode.
Timeout changes apply to the *next* command. Only the
engine's own timer returns 57014 `canceling statement due to statement timeout`;
caller cancellation/deadlines remain ordinary context errors. Zero disables
the timer. RESET ALL resets session-settable definitions and placeholders,
skipping read-only and privileged definitions.

Settings participate in whole transactions. SESSION (also plain SET) persists
on commit and restores its pretransaction value on rollback or immediate abort.
LOCAL expires on either end. SESSION followed by LOCAL commits the masked
SESSION value; a subsequent SESSION or RESET supersedes that candidate.
Go `sql.Tx.Commit` on an aborted transaction restores originals and returns
the existing ordinary `pq: could not complete operation in a failed transaction`
error, not a SQLSTATE diagnostic. SQL COMMIT/END succeeds by rollback while
restoring the same original settings.
RESET ALL operates on current explicit, resettable overrides: LOCAL DEFAULT
is absent and skipped, whereas explicit text equal to the default is reset.
`SET [SESSION | LOCAL] SCHEMA 'schema'` sets search_path to one quoted schema
element; it does not check existence. Prepare is metadata-only.
SQL LOCAL outside an explicit transaction warns with 25P01 before semantic
validation and its effective value expires at implicit completion. Validated
dotted placeholder names remain recognized in that physical session.
Function set_config with true outside a transaction returns its transient,
normalized value without that warning; the next command sees the original.
Savepoints, TIME ZONE, NAMES, timezone and client_encoding remain unsupported.

With `standard_conforming_strings=off`, a `U&'...'` string opener immediately
fails with 0A000 `unsafe use of string constant with Unicode escapes`, before
reading its body or UESCAPE clause. The Detail explains the off-mode restriction;
Position identifies the initial U in characters, not UTF8 bytes. Unicode escaped
identifiers (`U&"..."`) remain allowed. E and dollar strings are independent of
this mode. Preparing a Unicode string while on captures its decoded value:
switching off before Execute does not reparse or reject that prepared literal.

Before the first `OpenConnector`, register custom typed session configuration:

```go
d := stuntgres.NewDriver()
err := d.RegisterSetting(stuntgres.SettingDefinition{
    Name: "demo.memory", Description: "Session memory budget",
    Type: stuntgres.SettingInteger, Unit: stuntgres.SettingKilobytes,
    Default: "1024", IntegerMin: 0, IntegerMax: 2147483647,
})
// Handle err, then use d.OpenConnector and sql.OpenDB.
```

`SettingBoolean`, `SettingString`, `SettingInteger`, `SettingFloating`, and
`SettingEnum` use shared input/default/display rules. Access is
`SettingSession`, `SettingReadOnly`, or deny-only `SettingPrivileged`.
Numeric bounds are inclusive and explicit: zero/zero means exactly zero.
Enums copy `EnumValues` and display registered canonical spellings.
Numeric units are `SettingUnitNone`, `SettingBytes`, `SettingKilobytes`,
`SettingMegabytes`, `SettingMilliseconds`, `SettingSeconds`, `SettingMinutes`.
Memory inputs accept case-sensitive B/kB/MB/GB/TB, time inputs us/ms/s/min/h/d.
Integer fractions use ties-to-even rounding with PostgreSQL unit conversion;
floating display uses six significant digits. Registry publication is immutable
and frozen at first Engine construction, even without Ping, and stays frozen
after pools close. Different Drivers isolate registrations.
Registration rejects irrelevant numeric bounds, units on nonnumeric kinds,
invalid UTF8/NUL metadata, nonfinite floating bounds and duplicate enum values.
Definitions and enum slices are owned copies. The globally sql-registered Driver
cannot be configured retroactively; use NewDriver, RegisterSetting, OpenConnector
and sql.OpenDB in that order for custom definitions.

Numeric text follows PostgreSQL's prefix rules: integer `010` is octal8, while
`010.5` falls back to decimal input and rounds to10; `08.5` and `0x1p0` do not
trigger integer fallback. Exact hexadecimal subnormals such as `0x1p-1074` and
exact decimal subnormals are accepted for floating settings: decimal digits of
`5^1023` followed by `e-1023` represent exactly `2^-1023` and display as
`1.11254e-308`. Inexact subnormal rounding, nonzero values rounded to zero,
NaN and overflow reject; defaults follow the same exactness rule.
Infinity reaches the registered finite bounds and reports a range diagnostic
using PostgreSQL's canonical `Infinity` / `-Infinity` spelling, regardless of
the accepted input alias (`inf`, `Infinity`, or case variants).
Invalid numeric input never publishes an override. The native BOOL argument
importer reports 22P02 with empty Position/Context; real PostgreSQL Bind adds its
portal-parameter Context. Literal BOOL errors instead identify the SQL literal.

Unknown valid dotted SET/set_config names create session-local text placeholders
with empty reset defaults, hidden from SHOW ALL. RESET can create a placeholder;
SHOW/current_setting never create one. Physical replacement drops placeholder
knowledge. Registered custom definitions are typed from the outset and visible
in SHOW ALL; they express session configuration storage, not invented planner
or process effects.

Settings restore on whole-transaction end and immediately when an explicit
transaction aborts. They add no role/grant authority, file defaults, ALTER SYSTEM,
reload, server-start or shell-option effects. Existing role/ACL capabilities
remain separate.
Unsupported builtin names (including work_mem/random_page_cost) remain 42704.
The two selected setting calls do not introduce general functions, casts, aliases
or expressions. NULL/parameters remain invalid **SET scalar syntax**; use
set_config for bound values. Comma lists are supported for search_path; other settings reject multiple values with 22023.
The nonreserved name local remains a parameter name in `SET local TO value`
and `SET SESSION local = value`, rather than silently becoming LOCAL scope.
Native Reset/Restore preserve live settings and use default-on parsing;
Dump excludes settings. Retained SHOW/SELECT snapshots outlive session end.

Run `go test -count=1 -run '^TestSettingsSmoke$' ./smoke` and
`go test -tags=reference -run '^TestReferenceSettings' -count=1 -v ./reference`.
The reference suite separately executes real PostgreSQL numeric/enum settings,
non-superuser privilege denial and pg_sleep timeout; arbitrary custom PG
placeholders are never claimed to validate native typed registrations.
The native smoke also asserts SESSION/LOCAL ordering, commit and rollback
restoration, transactional RESET ALL, and aborted-transaction recovery on the
same physical connection, plus fixed read-only/deny-only access for both
bootstrap postgres and an ordinary authenticated role. These assertions do not
add privilege-grant mechanisms.

Settings smoke also asserts exact decimal subnormal handling and underflow
rejection, typed boolean/string/integer/floating/enum settings, normalized units
and bounds, SET/RESET/SHOW/SHOW ALL/current_setting/set_config behavior,
prepare-time parser mode, healthy sessions after 57014 timeouts, 55P02/42501
access failures, transactional local scope, retained settings on reuse,
and replacement defaults with surviving data. Go test reports the result.

### Notices and warnings

```go
base, err := stuntgres.NewDriver().OpenConnector("example")
if err != nil { return err }
configured := base.(*stuntgres.Connector).WithNoticeHandler(func(n stuntgres.Error) {
    // n is an owned value; retain it if needed. Optional fields remain conditional.
    fmt.Println(n.Severity, n.Code, n.Message)
})
db := sql.OpenDB(configured)
defer db.Close()
```

WithNoticeHandler returns an immutable copy; nil disables delivery. Configuration
applies to newly created physical connections only, never other connectors sharing
the same database. Ordinary sql.Open is silent (no default stderr logging).
CREATE TABLE IF NOT EXISTS emits NOTICE/42P07 when the exact creation destination
already exists, leaving its definition and data unchanged. Fresh creation is real
and silent. Semantic column/type/LIKE validation occurs at execution, after the
existing relation check; malformed syntax still fails during parsing. An existing
target skips unknown types, missing LIKE sources and valid but unsupported LIKE
options, without claiming support for the skipped declaration.
Current schema CREATE authorization is checked before either creation or an
existing-relation skip, including a skip after a competing publication. A skip
does not require ownership or ordinary table privileges and never changes the
existing owner. Denied CREATE emits no skip notice. Session-authorization
switching and reset preserve `client_min_messages`; notice-handler connector
copies preserve the asserted authenticated role.
Cancellation detected before a same-name publication-conflict decision remains
an ordinary context error, not duplicate-table failure or successful skipping;
the winning table remains intact and the canceled loser emits no notice.
Long application_name emits NOTICE/42622 with original and clipped text.

SET/SHOW client_min_messages uses the same physical settings route, default
notice. Case-insensitive values are debug5, debug4, debug3, debug2, debug1, log,
notice, warning, error; debug canonicalizes to debug2 and info is also accepted.
INFO always passes; errors remain returned errors at every threshold, never
callbacks. Invalid values return 22023 with an available-values Hint without
changing the setting. DEFAULT restores the captured physical default.

Callbacks run synchronously in producer order after all engine locks and the
operation lease are released, before command return; payload strings are immutable
owned Go data. User code is not context-preemptible: cancellation during a callback
does not undo committed execution or replace its result. Reenter through another
already held physical connection or a separate pool with free capacity, **not**
the same sql.Conn or an exhausted originating pool. Preparation emits no execution
notices. Native controls do not serialize or deliver staging notices.
No native DO/RAISE, warning-producing procedural SQL, COPY or asynchronous
LISTEN/NOTIFY support is implied.

`go test -count=1 -run '^TestNoticesSmoke$' ./smoke` exercises actual notices,
filters, ownership, peer reentry and control round trips; Go test reports the result.


Run `go test -count=1 -run '^TestSessionSmoke$' ./smoke` for asserted isolation, preparation, physical
reuse/replacement, data survival, diagnostics, cancellation and native controls.
Scoped checks: `go test -run '^TestSession' ./...`,
`go test -race -run '^TestSession' ./...`, and the real pinned PostgreSQL suite
`go test -tags=reference -run '^TestReferenceSession' -count=1 ./reference`.

Session smoke asserts distinct session settings, prepared TEXT metadata and zero
affected counts, preserved settings on pool reuse, restored defaults on physical
replacement with surviving data, data retention after pool reopening,
42704/22023/42601 diagnostics, and cancellation/control preservation.
Go test reports the result.

### SQL and driver transactions (READ COMMITTED)

`DB.BeginTx` / `Conn.BeginTx` and `sql.Tx.Commit` / `Rollback` support the
baseline INSERT, SELECT and DELETE paths, transactional settings and the single-target
explicit AX utility below, including prepared execution. Default isolation
and explicit `sql.LevelReadCommitted` are supported, read-write only. CREATE
tables **before** starting a block. SQL controls support the same INSERT/SELECT/DELETE,
settings and AX paths; savepoints, transactional DDL, stronger isolation and
other DML remain unsupported.
Role creation/removal and SET/RESET SESSION AUTHORIZATION are likewise rejected
with `0A000` inside an explicit transaction; this is a native capability limit,
not PostgreSQL parity. Read-only identity SELECT remains supported there.

Supported whole-input SQL syntax (one statement per public call):

```sql
BEGIN [WORK | TRANSACTION]
START TRANSACTION
(COMMIT | END | ROLLBACK | ABORT) [WORK | TRANSACTION] [AND [NO] CHAIN]
```

Starts use default READ COMMITTED/read-write only. Complete transaction-mode
clauses, including explicit SQL READ COMMITTED or READ WRITE, are recognized but
fail native `0A000`, `transaction modes are unsupported`, at the first mode.
Malformed clauses and suffixes retain `42601`; PostgreSQL supports valid modes,
so this is a native capability boundary, not PostgreSQL rejection parity.
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
installs a fresh healthy Active incarnation with command zero/default modes,
no inherited private writes or locks. **Same-epoch** failed-block SQL COMMIT rolls
back successfully; COMMIT AND CHAIN likewise starts healthy work after discarding it.
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

#### Relation locks

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
each queued acquisition, including automatic SELECT/INSERT/DELETE Prepare locks,
DELETE row waits and COMMENT/ALTER execution. Immediate grants are untimed; every later acquisition
gets a fresh budget. Expiry returns 55P03, `canceling statement due to lock timeout`.
During direct SELECT/INSERT/DELETE relation analysis and semantic Prepare, lock timeout also
reports the relation-name token's character Position (including schema qualification).
Cached Execute and utility lock timeouts have no parser Position; statement
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
cohorts and earlier incompatible waiter ordering. An owner can SELECT/INSERT/DELETE
under its own AX. SELECT captures its READ COMMITTED view after the wait: holder
commits are visible, holder rollbacks are absent. Explicit locks survive
statement/Rows close, until commit/rollback, admitted failure or physical end.
Failures immediately discard private writes and release all holds, even before
the client rolls back the aborted transaction. Implicit completion publishes
first and releases afterward. Canceled pending requests are removed and wake
eligible successors; caller interruption remains an ordinary context error.

Semantic SELECT/INSERT/DELETE Prepare acquires the same AS/RX as execution. Implicit
Prepare releases at completion; explicit Prepare retains until transaction end
or failure, not stmt.Close. Preparation captures no data snapshot or future
authority. LOCK Prepare is metadata-only (zero parameters/results); Execute
performs block/name/current-role checks and acquires AX, affecting zero rows.
Outside a block it returns `25P01`, `LOCK TABLE can only be used in transaction
blocks`, before lookup. Missing targets use `42P01`; ordinary-role AX denial is
`42501`, `permission denied for table t`. There is no upgrade/deadlock detection,
advisory locking, additional row modes or lock introspection. DELETE's row UPDATE
holds arbitrate distinct logical rows independently; ordinary SELECT does not wait
on those holds. DELETE acquires its one candidate view after relation grant and
keeps it fixed across row waits, skipping a peer's committed deletion and retaining
the original candidate after peer rollback. Peer inserts during a wait are not candidates.
Use lock_timeout or caller deadlines to bound conflicting operations; conflicting conversion cycles are
not protected by deadlock detection.

An explicit transaction sees its successful earlier writes; other physical
sessions see only committed data. Each SELECT execution captures a fresh
READ COMMITTED view, so a peer commit becomes visible to the next command,
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
| Unsupported isolation / read-only option | Ordinary `stuntgres: unsupported transaction isolation level` / `stuntgres: read-only transactions are unsupported`, before state change |
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
PostgreSQL C provenance. Analyzed SELECT/INSERT missing-relation diagnostics
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
publication. INSERT/SELECT use the existing binder and identity-keyed storage;
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
These supplement, not replace, the full gates below. The real reference oracle
remains pinned PostgreSQL **18.0 UTF8/C**; current official `/docs/18/` manuals
identify **18.6**, and local **20devel** sources are research only. See
[READ COMMITTED](https://www.postgresql.org/docs/18/transaction-iso.html#XACT-READ-COMMITTED)
and [transactions](https://www.postgresql.org/docs/18/tutorial-transactions.html).

## Roles and authorization

Each engine starts with the trusted native `postgres` principal (LOGIN,
superuser). `OpenConnector` uses it. Role identities are opaque, engine-owned
and never reused after DROP, Reset or Restore. PUBLIC is an ACL grantee meaning
all existing and future principals, not a role or membership endpoint.

```text
CREATE {ROLE | USER | GROUP} name [WITH] [LOGIN | NOLOGIN]
DROP {ROLE | USER | GROUP} name [, ...]
SET [SESSION] AUTHORIZATION {name | 'exact role name' | DEFAULT}
RESET SESSION AUTHORIZATION
SELECT {session_user | current_user} [, ...]
```

ROLE/GROUP default NOLOGIN; USER defaults LOGIN. Duplicate options fail `42601`
at execution, duplicate names `42710`, missing DROP targets `42704`. DROP is
statement-atomic across its list, rejects owner/ACL/persistent dependencies
(`2BP01`), and removes membership endpoints through the internal authority.
SQL role membership, SET ROLE, ALTER ROLE/GROUP, passwords, special role
attributes and GRANT/REVOKE are not implemented; recognized adjacent syntax
fails `0A000`, never successful no-ops. Role administration requires the
effective superuser in this bounded foundation.
Valid `DROP {ROLE | USER | GROUP} IF EXISTS name [, ...]` is likewise excluded:
it fails positioned `0A000` at `IF` before any script statement executes,
including when every target is absent. Ordinary unquoted or quoted role names
`if` and `exists` remain valid. Malformed conditional drops and `CASCADE`
(which is not PostgreSQL role-drop syntax) fail `42601`, not a capability error.
Role names reuse PostgreSQL's ordinary column-name keyword categories;
nonreserved `local`, `session` and `role` remain names. Reserved `WITH` is
only an optional option introducer, not a role name or a repeated option.

SET/RESET change session_user and current_user together. Only the **originally
authenticated** superuser may select another principal (including NOLOGIN);
an ordinary authenticated role may select itself or reset/default only.
Failed switches preserve both identities and application_name. Identity
projections have NAME metadata, exact lowercase labels and one string row;
prepared projections read execution-time identity. NAME is not a supported
table column/storage type. Exec of identity SELECT reports one selected row;
role and authorization commands affect zero rows.

```go
d := stuntgres.NewDriver()
// First create LOGIN roles on the same exact opaque DSN using an admin pool.
connector, err := d.OpenConnectorAs("my fixture", "application_owner")
if err != nil { /* handle host assertion error */ }
ownerPool := sql.OpenDB(connector)
defer ownerPool.Close()
```

`OpenConnectorAs` is a **trusted host identity assertion, not password
authentication**. It does not parse credentials or change DSN naming. The role
argument is an exact decoded stored name (no SQL folding or truncation).
Every physical Connect resolves and LOGIN-validates the requested role;
empty/missing/NOLOGIN assertions return ordinary Go adapter errors, not
network SQLSTATE. Healthy borrower reuse retains selected identity/settings;
physical replacement starts with the connector's requested authenticator and
fresh settings. Use held `*sql.Conn` handles for principal-specific operations.
Two connectors with the same Driver/exact DSN share data, never session state.

New tables belong to current_user and have owner use rights, with no PUBLIC
table privileges. Central checks cover schema CREATE/USAGE, all actually used
SELECT projection/predicate/order columns and assigned INSERT columns.
When that SELECT or INSERT column manifest is empty, table privileges or the
corresponding privilege on **any live column** suffice. Empty/filtered sources
still require authorization; grants on dropped columns never qualify.
Schema USAGE is checked during relation lookup, including preparation: qualified
references to an inaccessible schema fail with `42501` / `permission denied for
schema public` before column/input binding. Unqualified lookup omits inaccessible
public, so an otherwise unresolved name fails with `42P01`; implicit pg_catalog
lookup is preserved. Ordinary table/column ACLs remain execution-time checks:
SELECT/INSERT preparation does not cache permission, and execution reevaluates
the current principal, returning `42501` / `permission denied for table secret`
without synthetic diagnostic object fields. Explicit internal empty ACLs remove
ordinary owner use rights, not owner authority or implicit grant options.
Shared ACL validation rejects grant options for PUBLIC at mutation, staging and
import boundaries. SQL ACL mutation is not exposed.
After successful namespace lookup, parse/binding literal type input and typed
parameter input precede executor table permission checks, as at PostgreSQL's
Parse/Bind boundaries.
Immutable integer assignment casts fold during execution planning, after successful preparation
but before executor permission checks: out-of-range INT4 constants yield
`22003` (`integer out of range`), and invalid typed input can yield `22P02`
before permission denial. Valid nonowner input is denied `42501` without
mutation; executor checks precede protected row reads and runtime constraints.

DROP dependency DETAIL preserves object and column identities, PostgreSQL
relation-name quoting, raw decoded column names and object-identity order
(not alphabetical description order). Failed batches preserve all roles and
membership endpoints, including when a later target has an object dependency.
Retained identity Rows keep their
original NAME metadata and values across session switches and native Reset/Restore,
while new execution observes the current session identity.
NAME identity results use the same catalog-owned typed values and retained
snapshots as ordinary rows (builtin OID 19); NAME table columns remain unsupported.

**Bootstrap divergence from fresh PostgreSQL 18:** native public has an
explicit host-convenience PUBLIC USAGE|CREATE ACL; pg_catalog has PUBLIC USAGE
only. This is not PostgreSQL's fresh public CREATE default and not a generic
default-allow. The reference harness recreates public, so ordinary-owner
fixtures explicitly run `GRANT USAGE, CREATE ON SCHEMA public TO PUBLIC` on
the privileged PostgreSQL setup connection only.

`go test -count=1 -run '^TestRoleSmoke$' ./smoke` checks direct/prepared aliases
and diagnostics, NAME metadata, held prepared statements across switches,
owner/nonowner isolation, genuinely distinct trusted authenticators,
reuse/replacement, dependency failures and owner-preserving native controls.
It asserts role creation defaults, owner access versus 42501 rejection,
owner restoration, legacy control acceptance, atomic failures and successful
role cleanup. Go test reports the result.

Focused checks: `go test -run '^Test(Role|Authorization)' ./...`,
`go test -count=1 -run '^TestRoleSmoke$' ./smoke`, and the genuine pinned suite
`go test -tags reference -run '^TestReferenceRole' -count=1 -v ./reference`.

## Supported SQL

```text
CREATE TABLE [IF NOT EXISTS] [schema.]name ([element [, ...]])
element := column type [[CONSTRAINT name] NULL | [CONSTRAINT name] NOT NULL]...
         | [CONSTRAINT name] NOT NULL column | LIKE [schema.]source
DROP TABLE [IF EXISTS] [schema.]name [, ...] [RESTRICT | CASCADE]
COMMENT ON TABLE [schema.]name IS string_literal | NULL
COMMENT ON COLUMN [schema.]table.column IS string_literal | NULL
COMMENT ON SCHEMA name IS string_literal | NULL
COMMENT ON ROLE name IS string_literal | NULL
ALTER TABLE [ONLY] [schema.]name DROP [COLUMN] [IF EXISTS] column [RESTRICT | CASCADE]
ALTER TABLE target ALTER [COLUMN] column SET NOT NULL
ALTER TABLE target ALTER [COLUMN] column DROP NOT NULL
ALTER TABLE target ADD [CONSTRAINT name] NOT NULL column
ALTER TABLE target DROP CONSTRAINT [IF EXISTS] name [RESTRICT | CASCADE]
target := [schema.]name [*] | ONLY [schema.]name | ONLY ([schema.]name)
INSERT INTO [schema.]name [(column [, ...])] VALUES (value [, ...]) [, ...]
INSERT INTO [schema.]name [(column [, ...])] SELECT [projection_target [, ...]]
       [FROM [schema.]name [WHERE logical_predicate] [ORDER BY column [ASC | DESC]]]
INSERT INTO [schema.]name [(column [, ...])] VALUES (value [, ...]) [, ...]
       ORDER BY (positive_output_ordinal | columnN | +numeric_constant) [ASC | DESC]
DELETE FROM [schema.]name [[AS] alias] [WHERE delete_predicate]
SELECT (* | projection_target [, ...]) FROM [schema.]name
       [WHERE logical_predicate] [ORDER BY column [ASC | DESC]]
SELECT null_count_call [, null_count_call ...]
type  := SMALLINT | INT2 | INTEGER | INT | INT4 | BIGINT | INT8 | TEXT | BOOLEAN | BOOL
       | REAL | FLOAT4 | DOUBLE PRECISION | FLOAT8 | FLOAT[(p)]
       | DATE | (TIME | TIMESTAMP) [(precision)] [WITHOUT TIME ZONE] | UUID | BYTEA
value := signed-64-bit integer | 'string' | E'escape string' | U&'Unicode string' [UESCAPE 'escape'] | $$string$$ | $tag$string$tag$ | TRUE | FALSE | NULL | $n | DATE 'input' | (TIME | TIMESTAMP) [(precision)] [WITHOUT TIME ZONE] 'input'
scalar := unqualified column | value
comparison := scalar (= | <> | != | < | <= | > | >=) scalar
logical_target := scalar | comparison | NOT logical_target
                | logical_target AND logical_target | logical_target OR logical_target
                | (logical_target)
logical_predicate := logical_target
delete_predicate := logical_predicate with optional target/alias qualification on columns
projection_target := logical_target | null_count_call
call_argument := scalar | null_count_call
null_count_call := (num_nulls | num_nonnulls)(call_argument [, ...])
```

### INSERT from query — change note

INSERT now accepts SELECT sources through the common query pipeline, including
parenthesized sources and INSERT-only no-FROM projections such as `SELECT '1'`,
`SELECT NULL` and `SELECT $1`. The target is not part of the source namespace.
Star expansion and output width/type checks happen during analysis, even if the
source is empty. An explicit target list requires the same number of expressions;
without a list, the source maps the first N declared live columns. Omitted
nullable columns become typed NULL; existing NOT NULL constraints still apply.
Declared defaults, DEFAULT expressions, sequences and serial remain unsupported
native subsystems, not successful no-ops. Real PostgreSQL declared-default
behavior is separately witnessed in the reference suite.

```sql
CREATE TABLE source_rows(i INT, label TEXT);
INSERT INTO source_rows VALUES (1,'one'),(1,'one'),(2,NULL);
CREATE TABLE destination(label TEXT, i BIGINT, flag BOOL);
INSERT INTO destination(i,label)
  SELECT i,label FROM source_rows ORDER BY i;
```

This inserts three occurrences (including both duplicates), converts INT4 to
INT8 and fills `flag` with NULL. Zero, one and many selected rows have exact
RowsAffected counts. No RETURNING is implied: direct/prepared Query of INSERT
has no columns or data rows; Exec exposes only the successful command count.
Prepare is effect-free, and reused statements freshly bind and read the current
source rather than caching source data.
Already-supported casts, null/truth/distinct predicates, DATE and BYTEA remain
available in query sources. SELECT result names retain the existing cast naming
rules. Source and assignment conversions use the same recursive, execution-private
preflight as ordinary SELECT, outside the engine lock and before source capture.

Plain `VALUES ('1')` and `(VALUES ('1'))` retain destination-context typing.
Adding the supported one-key ORDER BY makes VALUES a query source: each column
gets a common type first, with all-unknown columns becoming TEXT. Consequently
`INSERT INTO integer_target VALUES ('1') ORDER BY 1` fails 42804 with the rewrite/
cast Hint and no Position; it does not silently reparse known TEXT as integer.
ORDER BY uses an output ordinal, an implicit `columnN` name or a unary-plus
numeric constant, not a destination column name. Unsigned integer tokens within
int32 are ordinals, including after minus; `2147483648` and `-2147483648` instead
give 42601 / `non-integer constant in ORDER BY`. Unary-plus keys such as `+2`
and `+1.5` are validated numeric expressions with all rows tied, never ordinals.
Duplicate occurrences and NULL sort placement for column/ordinal keys are retained.
ORDER analysis follows target validation and VALUES width/common typing. Raw
parsing always consumes the complete script first, so later syntax errors take
precedence over ORDER analysis errors and prevent effects from a valid prefix.
These VALUES/no-FROM forms do not add standalone VALUES or ordinary no-FROM SELECT.
WITH/CTE, aliases, joins, general casts/functions, LIMIT and locking decorations,
RETURNING and ON CONFLICT remain outside this part's surface.

The target RX lock is acquired before source AS; target INSERT and source SELECT
authority (projection, WHERE and hidden ORDER columns) are independently checked.
Self-insert reads a frozen prefix including earlier own transaction writes, never
this command's newly assembled batch. Source scanning, sorting and conversion run
outside the engine mutex; a failed cast, constraint, source evaluation, cancellation
or publication fence cannot publish a partial batch. Existing explicit-transaction
failure/rollback and other-session visibility rules are unchanged.

Dump records persisted destination values as canonical literal VALUES, not query
provenance. Restore roundtrips that output but deliberately rejects SELECT and
decorated VALUES INSERT source scripts with native 0A000 before trusted execution.
Held native result cursors keep their original schema/data across failed Restore,
successful replacement and Reset. These host controls are not PostgreSQL physical
pg_dump, disk durability or wire-server APIs.

Feature checks (run from the repository root):

```sh
make test PACKAGES=./internal/parser RUN='^TestInsertSelect' VERBOSE=1
make test PACKAGES=./internal/engine RUN='^TestInsertSelect' VERBOSE=1
make test PACKAGES=. RUN='^TestInsertSelect' VERBOSE=1
make reference RUN='^TestReferenceInsertSelect' VERBOSE=1
make smoke RUN='^TestInsertSelectSmoke$' VERBOSE=1
```

### DELETE rows

`DELETE FROM target` removes every live occurrence while keeping the table and
its schema. Optional WHERE uses the same typed comparison and three-valued
Boolean evaluator as SELECT: only TRUE selects, never FALSE or NULL. AS and bare
target aliases are supported; an alias hides the original target name. Predicate
columns may be unqualified or qualified by the visible target/alias. This does not
enable SELECT aliases/qualified columns, USING, CURRENT OF, RETURNING, UPDATE,
foreign keys, triggers or additional constraints.
DELETE also reuses SELECT's nullness, Boolean truth and DISTINCT predicates,
including alias-qualified operands. Existing baseline explicit scalar casts work
on unqualified operands; qualified columns inside casts remain excluded, and
the complete statement is rejected before effects. Frame-only cast conversions
run before executor ACL checks or candidate capture, even on empty tables and
inside decisive Boolean branches. Row-dependent conversions remain per-row, but
the shared logical evaluator does not guarantee short-circuiting: on stored
`s='oops'`, both `FALSE AND int4(s)=42` and `TRUE OR int4(s)=42` fail with
22P02 and leave every original row intact. Empty tables demand no row conversion
and succeed with count zero. Pinned PostgreSQL optimizes these specific decisive
branches: FALSE AND deletes zero, TRUE OR deletes all rows, including when the
unused cast takes an invalid TEXT parameter. Native frame-only casts still fail
before scanning even on empty input. This bounded optimization difference is not
a general PostgreSQL evaluation-order or short-circuit promise.

Direct and held prepared execution infer parameter types from SQL, not Go values,
and return the exact number actually deleted through RowsAffected; zero is success.
There is no result relation without RETURNING and no LastInsertId. Preparation
captures descriptions and relation locks only, not rows or a future execution snapshot.
Execution checks table DELETE and SELECT on actually read predicate columns;
omitted or constant predicates do not invent a SELECT requirement. SQL GRANT
remains excluded; the existing internal catalog ACL authority supplies rights.

Immutable row identities/end roots preserve READ COMMITTED views, duplicates,
zero-column occurrence counts and already-returned cursors. Successful earlier
own INSERT/DELETE commands are visible in the next command; peers see only the
combined committed batch. Rollback or an admitted error/cancellation discards
private inserts/deletes and releases transaction holds. All predicates and
preflight complete before atomic publication, and publication precedes lock release.
Go context errors remain ordinary errors. Ordinary SELECT and writes on different
rows are not blocked by a DELETE row hold; same-row DELETE waits and revalidates
the original candidate, never retries SQL or widens its candidate snapshot.
Compatible TABLE/COLUMN COMMENT and ACL metadata revisions do not invalidate
staged or waiting DELETE: authority follows the exact storage-shape generation,
object/epoch and row/version/ordinal, not descriptor revision. ACL changes do not
retroactively reauthorize staged work at COMMIT; each fresh prepared Execute
still checks current privileges. Replacement, removal/recreation and incompatible
row-shape changes remain fenced.
INSERT query sources use that same live view, including earlier own deletions:
self-insert copies only captured survivors, never deleted history or its new batch.

Dump captures only committed survivors (including duplicate/zero-width rows), not
private deletion or tombstone history; canonical CREATE/INSERT restores those
survivors and supported metadata. DELETE scripts remain outside Restore's whitelist.
Deleted NULL history does not prevent SET NOT NULL, while live NULL and subsequent
NULL insertion retain genuine 23502 enforcement. DELETE cannot violate the only
currently supported constraint, NOT NULL. The real PostgreSQL foreign-key rejection
witness is explicitly oracle-only, not native FK support or a synthetic constraint hook.

Feature commands: `make smoke RUN='^TestDeleteCoreSmoke$' VERBOSE=1`,
`make test PACKAGES=./internal/engine RUN='^TestDeleteCore' VERBOSE=1`,
and `make reference RUN='^TestReferenceDeleteCore' VERBOSE=1`.
Integrated-expression smoke:
`make smoke RUN='^TestDeleteCoreIntegratedExpressionsSmoke$' VERBOSE=1`.

#### DELETE change log

- Added positioned DELETE grammar, target-only alias qualification, shared typed
  predicate binding and execution-time DELETE/read-column ACL requirements.
- Added immutable committed/private deletions, combined insertion/deletion
  publication, exact counts and transaction-owned row arbitration.
- Migrated retained SELECT, Dump, catalog replacement/shape and NOT NULL consumers
  to live membership; retained snapshots and failed publications remain unchanged.
- DELETE column predicates evaluate in the shared binder's row frame, including
  qualified aliases and held prepared parameters. Qualified missing-column
  diagnostics render decoded components (`column D.Zzzzz does not exist`).
- Foreign replacement retains one final cancellation boundary after live-row
  filtering and authority preparation; cancellation cannot retract installation
  after that successful observation.
- Corrected DELETE lifetime/publication fences to use existing storage-shape
  identity rather than descriptor identity, preserving compatible COMMENT/ACL
  publication without relaxing replacement or predecessor authority.
- Integrated the existing null/truth/DISTINCT predicate nodes and frame-only
  scalar-cast preflight into DELETE, preserving current qualification boundaries,
  fresh authorization and storage-shape fencing after preflight.
- Corrected decisive-branch reference expectations to assert PostgreSQL's exact
  optimized counts/survivors separately from native conversion diagnostics and
  atomic preservation, without changing the shared evaluator or cast preflight.


### Null argument counts

`num_nulls` and `num_nonnulls` accept 1–100 heterogeneous scalar arguments
without common-type coercion. They are immutable and nonstrict: explicit NULL
positions count as null, while zero, false, empty strings and nested count results
are nonnull. Results are nonnull INT4 with native int64 values and the selected
function name as the column label; physical origin is absent and scalar
nullability metadata stays conservative.

Calls work in table SELECT projections and nested selected arguments. SELECT
without FROM is supported only when every target is rooted in a selected call,
with literal/parameter/nested-call arguments and no columns or extra clauses.
Former signed numeric literals remain valid arguments. Computed arithmetic does
not: table-backed calls fail native0A000 at the operator, while no-FROM calls
fail the existing `SELECT without FROM is unsupported` admission check at
SELECT (character1), before function binding.
Both forms use the same binder, typed execution and finalized query pipeline:

```sql
SELECT num_nulls(1,NULL,'x'),num_nonnulls(1,NULL,'x');
SELECT num_nulls(v,s),num_nonnulls(v,s) FROM t WHERE id=$1;
```

Direct and prepared invocations share analysis and metadata. The second example
can hold a prepared statement and reuse its INT4 predicate parameter. Variadic
ANY arguments themselves do not infer parameter types: `SELECT num_nulls($1)`
fails analysis with42P18 even if the Go value is nil, integer or string. An
initially unknown ANY occurrence is not retroactively typed by a later target
TEXT fallback or WHERE constraint; that mismatch has42P08 at the occurrence.
Existing bare-target42P08 behavior remains unchanged.

Zero arguments fail42883 with PostgreSQL's function-resolution Hint; over100
fail54023 after child analysis: a missing column or failing nested call takes
precedence, while unresolved ANY parameters still reach the arity error before
whole-statement type finalization. Explicit VARIADIC scalar operands fail42804 at the operand, and
multiple ordinary/VARIADIC operands fail42883 signature lookup. Arrays, including
PostgreSQL's NULL-array/empty-array cases, are unsupported; ordinary NULL is a
single null argument, not a NULL array. No calls are added to INSERT, settings
arguments or WHERE. General functions, qualification, aliases, nonbaseline casts, arithmetic,
DISTINCT, ORDER BY within calls, FILTER, OVER, star and named arguments remain
outside the native surface.

Feature smoke: `make smoke RUN='^TestNullCountSmoke$' VERBOSE=1`.
Independent pinned PostgreSQL witnesses:
`make reference RUN='^TestReferenceNullCount' VERBOSE=1`.

### Logical query results

SELECT now uses one bound source/filter/sort/projection pipeline and the same
typed result contract as identity SELECT, SHOW and setting calls. Finite results
are finalized eagerly before statement completion: a later projection or sort
failure exposes no cursor or successful statement fragment. Public iteration
never evaluates targets or publishes writes. Direct-column output retains
immutable captured source/index views rather than copying whole tables;
computed output retains only finalized projected values.

Rows own their schema independently of the statement. Empty results preserve
column names/types; zero-column SELECT preserves one occurrence per input row.
Physical projections preserve origin and known nullability, while scalar
nullability remains unknown. WHERE retains only TRUE (not FALSE or NULL).
Duplicates remain duplicates. Without ORDER BY, output is a bag with no promised
sequence; sort peers have no portable tie order. ASC places NULL last and DESC
places NULL first; filter and sort-only columns do not leak into output metadata.

EOF, cancellation and iteration errors automatically release result resources
without erasing metadata or saved exported values. At the raw driver cursor,
the originating error remains sticky until idempotent Close; closed Next returns
EOF. Ordinary context errors remain ordinary Go errors. Mutable exported bytes
(including timestamp infinity) detach from both internal values and other
exports. Captured rows/types remain stable across append, Reset, Restore and
transaction completion; result retention does not grant publication authority.

Missing ORDER BY identifiers and analyzed relation names now retain their token's
one-based **character** Position during Query and Prepare, including multibyte
SQL and empty input. COMMENT/ALTER/LOCK utility records keep their own semantics.
This migration adds no SQL syntax, binder, catalog, storage or routine-frame path.
Run `make smoke RUN='^TestQueryCoreSmoke$' VERBOSE=1` for direct/prepared
projection/metadata, bags, NULL/empty/zero-width output, ordering, sticky raw
cursor cancellation, byte ownership and retained native-control results.
Independent PostgreSQL witnesses are
`make reference RUN='^TestReferenceQueryCore' VERBOSE=1`; feature-local unordered
fixtures independently check cardinality and every occurrence in both consumed
and recorder-captured rows before canonicalizing only for backend comparison.
Duplicate, NULL and zero-width row counts remain significant; ordered rows and
column metadata still require exact sequence equality.

### Three-valued logical operators

Table SELECT targets and WHERE now support Boolean `AND`, `OR` and `NOT`
through the shared typed expression binder. Results are BOOL, named `?column?`
without a physical origin; computed nullability remains unknown, including
empty results. SQL NULL projects as native `nil`, and only TRUE passes WHERE.

| a | b | a AND b | a OR b |
|---|---|---|---|
| TRUE | TRUE | TRUE | TRUE |
| TRUE | FALSE | FALSE | TRUE |
| TRUE | NULL | NULL | TRUE |
| FALSE | TRUE | FALSE | TRUE |
| FALSE | FALSE | FALSE | FALSE |
| FALSE | NULL | FALSE | NULL |
| NULL | TRUE | NULL | TRUE |
| NULL | FALSE | FALSE | NULL |
| NULL | NULL | NULL | NULL |

NOT maps TRUE to FALSE, FALSE to TRUE, and NULL to BOOL NULL. Scalar comparisons
bind above NOT, then AND, then OR; parentheses override precedence. NOT
associates right, AND/OR left. Thus `TRUE OR FALSE AND FALSE` is TRUE, while
`(TRUE OR FALSE) AND FALSE` is FALSE. There is **no guarantee that the left
operand evaluates before the right**, or that a decisive value suppresses
evaluation of another operand. All operands are analyzed even in
`FALSE AND bad_operand` and `TRUE OR bad_operand`.

Unknown strings, NULL and unconstrained parameters receive contextual BOOL:
`'yes' AND 'no'` is FALSE and `NULL AND NULL` is BOOL NULL. Concrete non-BOOL
columns and already-known parameter types are not implicitly converted; wrong
types report construct-specific `42804`, malformed Boolean input `22P02`, and
invalid syntax `42601`, with source character positions for SQL input.
Logical binding carries each analyzed child's earliest source location into its
parent in constant work per node, preserving positioned diagnostics for long
operator chains and nested NOT without repeatedly walking their raw subtrees.
`SELECT $1 AND TRUE,$1 FROM t` fixes both occurrences as BOOL;
`SELECT $1,$1 AND TRUE FROM t` retains the first unresolved occurrence and
fails `42P08` (`boolean versus text`) on its output fallback.

FROM remains required outside identity/settings special forms and the selected
null-count-only form. Existing binary
scalar comparisons form Boolean leaves in targets and WHERE, such as
`NOT id=1 AND (a OR b)` or `id>=2 AND a=b`. Comparisons keep scalar operands
only: no chaining or logical/comparison expressions as comparison operands.
This adds no logical INSERT VALUES, setting arguments, expression ORDER BY,
nonbaseline casts, aliases, general functions or qualified-column expressions.
PostgreSQL also accepts `SELECT TRUE AND FROM t` and `SELECT TRUE OR FROM t`
as TRUE targets with bare aliases `and` and `or`. Native aliases remain excluded:
these keywords continue logical expressions, so these two incomplete expressions
report `42601` at FROM. Reference tests assert both native failures and PostgreSQL
positive results independently, not false syntax-error parity.

Public feature witness: `make smoke RUN='^TestLogicalSmoke$' VERBOSE=1`.
Independent pinned PostgreSQL witnesses:
`make reference RUN='^TestReferenceLogical.*$' VERBOSE=1`.
Native/parser/engine units:
`make test RUN='^TestLogical.*$' PACKAGES='. ./internal/parser ./internal/engine' VERBOSE=1`.



Relation names accept one or two identifier components. Qualified `schema.name` resolves only in that schema; unqualified references follow the session's effective search_path, with implicit `pg_catalog` first unless explicitly positioned. Unqualified creation chooses the first usable explicit destination and requires CREATE there, without falling through on denial. Namespace and relation descriptors carry engine-local epoch-scoped identities, while rows remain stored by relation identity. Internal object references also retain a stable engine-owner token: coincident numeric identities in separate instances are never interchangeable. Reset/Restore preserve that owner while atomically replacing the catalog lifetime, and retained result snapshots remain usable. Built-in namespace identities are internal infrastructure, not SQL catalog tables.

### Object comments

COMMENT stores one immutable UTF8 text per TABLE, COLUMN, SCHEMA or ROLE.
Another command replaces it; NULL and the empty string remove it, while
whitespace remains text. It affects zero rows and returns no columns or
parameters. Names and literals use the ordinary positioned parser, including
quoted dots, E/dollar/Unicode strings and parse-time string settings. Values
cannot be expressions or parameters. COMMENT remains an unreserved identifier.

TABLE/COLUMN lookup uses current search_path and schema USAGE. Missing columns
are resolved before owner checks. Owners and effective superusers may comment
local objects; ordinary grants do not substitute for ownership. Nonowner TABLE
uses `must be owner of table t`, COLUMN `must be owner of relation t`, and
SCHEMA `must be owner of schema s` (42501). ROLE uses real shared role authority:
ordinary users cannot comment even themselves (CREATEROLE required); a superuser
target requires SUPERUSER. This native role surface has no CREATEROLE/ADMIN SQL.
Prepare caches syntax, never target identity or ownership; Execute resolves anew.
Qualified TABLE/COLUMN targets in a missing schema fail `3F000`,
`schema "s" does not exist`; a missing relation in an existing schema fails
`42P01`. Schema-USAGE denial precedes relation/column lookup and has no Position
field for COMMENT, unlike positioned DML analysis diagnostics.
Serialized COMMENT waiters refresh immutable metadata at the original captured
relation identity after lock grant; a prior comment does not count as replacement.
Reset/Restore and drop/recreate still fence the old epoch/address, never retargeting
a waiting command to an object with the same name.

CatalogRead.Comment exposes local metadata at stable class/object/SubID addresses;
RoleRead exposes shared text through RoleDescriptor.Comment. Native SQL description
functions and pg_description/pg_shdescription queries are not implemented here.
Dropping/recreating an object clears text, while schema rename/owner transfer
preserves it. Plain LIKE does not copy comments. TYPE, DATABASE and other object
kinds remain explicit unsupported capabilities. COMMENT in an explicit driver
transaction returns native 0A000; PostgreSQL itself permits transactional COMMENT.

Dump captures local comments with the same immutable snapshot as rows: schemas
(including bootstrap overrides) in name order, then table/column text after CREATE
in declaration order. Escape literals preserve apostrophes, backslashes, newlines
and Unicode independently of destination string settings. No comments adds no
bytes. Reset clears local text; Restore replaces it atomically. Both retain role
comments, which are deliberately excluded from database Dump and Restore.


### Ordinary table definitions and removal

`CREATE TABLE z ()` is a real zero-column relation: direct/prepared `SELECT *`
returns no columns and no rows for the newly created table. `LIKE z` is legal.
There is no dummy column, `INSERT DEFAULT VALUES` remains unsupported, and
`INSERT ... VALUES ()` is invalid syntax. Ordinary `oid` is a legal column;
`ctid`, `xmin`, `cmin`, `xmax`, `cmax` and `tableoid` conflict with system names.

Plain LIKE copies column names and supported concrete types in declaration
order, and named NOT NULL identities at the LIKE element's position. Copied
constraints are processed in source constraint-name order, as in PostgreSQL.
Multiple/interleaved LIKE clauses are allowed; duplicate expanded names fail `42701`. The destination
has fresh relation/attribute identities, current-user ownership, no copied rows
or ACL, and no persistent dependency on its source. LIKE requires relation-level
SELECT; column-only SELECT is insufficient.

Qualified LIKE sources use that schema's USAGE checks; unqualified sources follow
the effective search path independently of the destination. Nested CREATE TABLE
elements in CREATE SCHEMA support the same ordered elements and zero-column shape.
They bind against preceding staged tables as the enclosing schema owner, with that
schema prepended to a local path. The caller's identity and stored path do not
change; inaccessible or non-SELECT sources fail the entire schema construction.

For example:

```sql
CREATE TABLE source (i INTEGER NOT NULL, s TEXT, f BOOLEAN);
CREATE TABLE clone (lead BIGINT, LIKE source, tail TEXT);
DROP TABLE source RESTRICT; -- clone remains independent and writable
CREATE TABLE empty_shape ();
```

Valid `INCLUDING`/`EXCLUDING` option names (COMMENTS, COMPRESSION, CONSTRAINTS,
DEFAULTS, GENERATED, IDENTITY, INDEXES, STATISTICS, STORAGE, ALL) are parsed, but
new-table execution returns positioned `0A000`, `LIKE options are unsupported`.
Neither INCLUDING nor EXCLUDING is an inert successful option. PostgreSQL's
EXCLUDING CONSTRAINTS still copies NOT NULL; native support is plain LIKE only.
Unknown syntactically valid type names prepare successfully and fail execution
with `42704`, `type "name" does not exist`, at the type's character position.
Known single-name types outside INT2/INT4/INT8/TEXT/BOOL/FLOAT4/FLOAT8/DATE/TIME/TIMESTAMP/NUMERIC/UUID/BYTEA fail
execution with `0A000`. Excluded multiword families (CHARACTER/BIT VARYING,
TIME WITH TIME ZONE, TIMESTAMP WITH TIME ZONE and qualified INTERVAL
fields), qualified type names, arrays and modifiers other than FLOAT(p),
numeric precision/scale, temporal precision and admitted single BYTEA modifiers
remain parser-level `0A000` boundaries. DATE and BYTEA modifier syntax is admitted only
to reject it semantically with `42601`, `type modifier is not allowed for type
"date"` or `"bytea"`, at the type token, before validating modifier operands.
CREATE Prepare is metadata-only and succeeds; direct or prepared execution rejects
the modifier without publishing a table. TIME/TIMESTAMP precision has its own bounded integer grammar.
Even an existing IF NOT EXISTS target cannot bypass excluded syntax forms;
failure-only guards construct no executable family AST. DOUBLE PRECISION and
valid FLOAT(p) are supported.

Type keyword aliases are not quoted catalog names: `"smallint"`, `"integer"`,
`"bigint"` and `"boolean"` fail with `42704`, while `"int2"`, `"int4"`, `"int8"`,
`"text"` and `"bool"` select the supported concrete types. Quoted case is significant (`"INT2"` or `"INT4"` is
missing), and diagnostic names retain decoded quotes and backslashes without
SQL or Go re-escaping. Genuine unsupported catalog names such as
`"name"` fail with `0A000`. Lowercase serial pseudo names, including quoted
`"serial"`, remain a `0A000` CREATE-utility boundary rather than nonexistent
types; uppercase quoted `"SERIAL"` is missing. No serial sequence or default
is implemented.

DROP preflights the entire explicit target group before one atomic catalog
publication. A later missing or unauthorized target leaves every table intact.
Duplicate resolved targets such as `a, public.a, a` remove one identity once.
IF EXISTS emits per-missing-target NOTICE/00000 (`table "name" does not exist,
skipping`, or `schema "name" does not exist, skipping`). DROP requires table
owner, schema owner or superuser authority, not ordinary table privileges, and
preserves current schema lookup/USAGE authorization.

The supported inbound table-definition dependency set is explicitly **empty**.
Default/explicit RESTRICT and CASCADE therefore remove the same explicit tables
and their contained columns, storage and relation/column ACL records. Namespace,
builtin types, owner roles and unrelated tables survive. Real DROP ROLE ownership
and ACL blockers disappear with the removed definitions. LIKE clones, prepared
statements and held cursors are not persistent dependents; held cursors retain old
rows/metadata, while prepared executions rebind (absent table `42P01`,
incompatible recreation `42703`). Foreign keys, views, indexes and sequences are
external-only dependency controls, not native graph compatibility or a fabricated
native `2BP01` table blocker. Explicit-transaction DDL remains unsupported.

Dump emits expanded canonical declarations, including `()`, independently of a
dropped LIKE source. Restore uses the same binder in isolated staging; failed
LIKE/type/duplicate/option declarations preserve the target. DROP remains
excluded from Restore scripts. Existing explicit-ACL Dump rejection is unchanged.
The independent reference recorder preserves genuine zero-column cursor
metadata as an empty result, distinct from no query result; consumed rows and
cursor closure are checked for both native and PostgreSQL executions.

### NOT NULL constraints

Both column `a INT [CONSTRAINT nn] NOT NULL` and table-form
`[CONSTRAINT nn] NOT NULL a` are supported (the table form has no parentheses).
Table constraints can precede their columns. `NULL`, including named NULL, is
nullable syntax, not a stored constraint. Repeated compatible declarations
merge into one identity; an explicit name replaces a pending generated name.
Conflicting NULL/NOT NULL declarations within one column definition fail
`42601` at the conflicting token. Column-local declarations are validated before
table constraints: differently named column NOT NULL duplicates reproduce the
pinned PostgreSQL 18.0 `XX000`, even when a table constraint precedes the column.
Table-level NOT NULL can enforce a column declared NULL in either element order;
only NOT NULL identities participate in that merge. Different names in that
merge use `42601`. Duplicate explicit names on different columns fail `42710`.
Named/unnamed CREATE table constraints targeting a system column, such as ctid,
fail `0A000`, `cannot add not-null constraint on system column "ctid"`, rather
than reporting a missing ordinary column.

An unnamed constraint gets a PostgreSQL-style UTF8-safe 63-byte
`table_column_not_null` name, with numeric suffixes avoiding every constraint
name in its namespace, including preceding nested CREATE SCHEMA tables and
already-selected names in the current command. Allocation follows the original
column/table/LIKE element order after merging later declarations for the same
column; future explicit names are not reserved. For example,
`CREATE TABLE t(a INT NOT NULL,b INT CONSTRAINT t_a_not_null NOT NULL)` fails
`23505` for `pg_constraint_conrelid_contypid_conname_index`, with Schema
`pg_catalog`, Table `pg_constraint`, and Constraint equal to that index name.
Detail is `Key (conrelid, contypid, conname)=(OID, 0, t_a_not_null) already exists.`,
where OID is the actual locally allocated candidate relation ID, not a fixed
cross-server value. No table is published. Reversing these column declarations
succeeds and generates `t_a_not_null1` for `a`.
Explicit names are relation-local and can be reused on another table. Names
stay attached to stable attribute identities through schema renames and dense
DROP COLUMN compaction. DROP COLUMN removes only its own NOT NULL identity.
LIKE copies the names onto fresh destination attributes; source ALTER/DROP
never weakens the clone.

Single-action SET/DROP NOT NULL and ADD/DROP CONSTRAINT use owner-authorized
ACCESS EXCLUSIVE execution; Prepare has no locks, validation scans, notices or
name allocation. SET/ADD validates visible committed rows first. Old NULL gives
`23502`, `column "a" of relation "t" contains null values`, with schema/table/
column fields and no failing-row Detail or Constraint field. INSERT NULL gives
`23502`, `null value in column "a" of relation "t" violates not-null constraint`,
with a failing-row Detail and the same object fields even for named constraints.
Multirow failure publishes no rows. Failed ALTER leaves descriptor, names,
nullability and data intact; successful ALTER changes metadata only, retaining
row storage and immutable snapshots.

Compatible repeated SET/ADD preserves the original name and is a count-zero
no-op. DROP NOT NULL is also a no-op when already nullable. DROP CONSTRAINT
removes that identity and makes its column nullable. Missing names give `42704`;
IF EXISTS emits `00000` NOTICE `constraint "name" of relation "t" does not exist,
skipping`, after authorization, without allocating a revision. Every selected
utility returns zero affected rows. Retained prepared SELECT and parameterized
INSERT handles use current nullability across SET/ADD/DROP without a
cached-result-type failure: bound nil fails while enforced and succeeds after
DROP. Native metadata reports known nullability and INT4's int64 ScanType;
the pinned pq adapter reports int32 ScanType but no Nullable capability.
Table/schema owner distinctions, namespace USAGE and final
context/session/epoch/revision/row-length fences still apply.

Dump emits canonical quoted `CONSTRAINT name NOT NULL` declarations, including
generated names, not ALTER history. Isolated Restore uses the common CREATE/
INSERT binder, preserving names/data/nullability/ownership/comments; failed
declarations or NULL insertion preserve the target. Explicit-ACL Dump rejection
and ordinary context/I/O errors are unchanged.

UPDATE has no native executor; inheritance has no native hierarchy/merge
mechanism; NOT VALID/VALIDATE/NO INHERIT have no native deferred validation or
hierarchy flags. They are explicit `0A000` boundaries, not successful no-ops.
Native failure-only INHERITS recognition accepts one complete parent list after
either CREATE TABLE form, including qualified/quoted/multiple parents, and
rejects it at INHERITS. IF NOT EXISTS cannot bypass that boundary. Empty,
unclosed, malformed or repeated clauses and extra tails are `42601`; complete
input rejection prevents earlier statements in the same script taking effect.
CHECK, primary keys, domains and general ALTER action lists remain excluded.
Explicit-transaction DDL remains `0A000` and aborts the native block. Independent
PostgreSQL-only tests witness actual UPDATE enforcement, inherited OR nullability,
inherited-drop rejection, NO INHERIT, CHECK unknown acceptance, deferred
validation, and transactional DDL rollback. These are not native engine claims.

Public feature smokes:

```sh
make smoke RUN='^TestNotNullSmoke$' VERBOSE=1
make smoke RUN='^TestNotNullControlsSmoke$' VERBOSE=1
```

### Identity-preserving table and column renames

`ALTER TABLE [IF EXISTS] t RENAME [COLUMN] old TO new` and
`ALTER TABLE [IF EXISTS] t RENAME TO new` relabel existing objects without moving
or rewriting rows. Both table and column targets accept `t*`, `ONLY t` and
`ONLY(t)` for standalone tables; these markers do not add hierarchy semantics.
Destinations are one identifier, not schema-qualified. Collisions, including
renaming to the current name, fail atomically (`42701` column, `42P07` relation).
Missing columns fail
`42703`, `column "old" does not exist`; fixed system columns cannot be renamed.
New names resolve immediately and old names fail with undefined-name diagnostics.

Both forms require the table owner or effective superuser, not merely the schema
owner. Table rename additionally requires CREATE on its containing schema;
column rename does not. Execution acquires ACCESS EXCLUSIVE with the existing
lock timeout, cancellation, grant-time identity and publication fences. Prepare
does not resolve targets, authorize, lock, emit notices or mutate data. IF EXISTS
applies only to the relation: missing targets (including absent qualified schemas)
return count zero and one `00000` NOTICE naming the unqualified relation.
It does not suppress missing-column or collision errors.

Relation OID/namespace and sparse attribute/origin identities remain stable;
only the immutable definition revision and selected label/name change. Row
storage, NOT NULL constraint names and attribute links, ownership, ACLs and
table/column comments remain attached. Retained Rows preserve original labels
and values. Retained prepared SQL is reanalyzed, not textually rewritten:
renamed explicit columns fail `42703`, old table names fail `42P01` with the
original relation-name character Position (15 in `SELECT * FROM t`), and a
renamed output label in SELECT star fails `0A000`,
`cached plan must not change result type`. Unchanged projections and omitted-target
INSERT survive column relabeling; fresh prepares expose current result labels.

Dump serializes current qualified CREATE/INSERT/COMMENT names and unchanged
named NOT NULL constraints, never ALTER history. Reset/Restore round trips values,
metadata and comments through existing native controls; explicit ACL serialization
remains rejected rather than silently losing grants. These are process-local
controls, not pg_dump, physical storage durability or network protocol support.
Views, foreign keys, indexes, CHECKs, row types and inheritance have no native
stored-definition descriptors; PostgreSQL reference witnesses exercise view/FK
dependency preservation, hierarchy and transactional DDL separately. Native
explicit-transaction ALTER and RENAME CONSTRAINT remain unsupported.

```sh
make smoke RUN='^TestAlterRenameSmoke$' VERBOSE=1
```

### Single-action column removal

`ALTER TABLE t DROP [COLUMN] [IF EXISTS] name [RESTRICT | CASCADE]`
removes one live column and its stored cells atomically. Ordinary `t`, `t*`,
`ONLY t` and `ONLY(t)` operate on supported standalone tables. No action lists,
inheritance or partition definitions are supported; the other selected ALTER
actions are the NOT NULL utilities and standalone renames above.
The table's effective owner or superuser is required; unlike DROP TABLE, schema
ownership alone does not authorize ALTER. Execution takes ACCESS EXCLUSIVE and
waits for existing readers/writers; utility Prepare is metadata-only. Cancellation
removes a waiting request without changing the table. A committed preceding INSERT
is retained, and later INSERTs use the new dense visible shape.
Queued SELECT, INSERT and subsequent DROP bind the current definition for the
exact originally acquired relation reference after their lock grants. A definition
revision alone is not replacement; actual removal/drop-recreate or Reset/Restore
still fences the old identity, without substituting another table by name.

Surviving columns keep their catalog attribute/origin identities; visible result
slots compact without renumbering those identities. Retained Rows keep old values
and metadata.
Surviving column grants and role-dependency diagnostics resolve those stable
attribute identities back to live column names, not dense slot numbers.
New direct/prepared queries omit the dropped column. Previously
prepared explicit survivor queries rebind successfully; dropped-column queries
fail `42703`, and a changed-width prepared star fails the existing `0A000`,
`cached plan must not change result type` signature boundary. ALTER remains
unsupported in native explicit transactions (`0A000`, aborting the block);
PostgreSQL transactional DDL is an external-only reference witness.
Missing expression-column diagnostics interpolate decoded quoted identifiers
literally, including embedded quotes/backslashes, with character-based positions
when direct or retained/new prepared queries rebind after DROP.

The actual supported dependent set is named NOT NULL constraints, column ACL
payload and column comments. DROP removes exactly the selected attribute's
constraint and comment, retaining
table and survivor comments keyed by stable attribute IDs. Canonical Dump emits
survivor comments by column name; Reset/Restore preserves those comments even
though the restored declaration has fresh dense attribute IDs.
There are no native index, CHECK, FK, view or hierarchy descriptors and no external
inbound definition dependencies. Thus native RESTRICT/default and CASCADE remove
the same selected column only, preserving independent tables and LIKE clones.
Real PostgreSQL-only tests witness automatic local constraint/index removal,
external RESTRICT failure/CASCADE closure, inheritance and partition behavior;
these are not advertised as native capabilities.

Missing columns fail `42703`, `column "missing" of relation "t" does not exist`;
IF EXISTS emits a `00000` NOTICE ending `, skipping` with no descriptor or row
change, after ownership checks. Fixed system names ctid/xmin/cmin/xmax/cmax/tableoid
fail `0A000`, `cannot drop system column "ctid"`, even with IF EXISTS. Modern `oid`
is an ordinary user column. All failures leave the original shape and data intact.

Dump serializes the final live CREATE/INSERT shape, never ALTER history. Dropping
every column retains row multiplicity: CREATE TABLE `()` is followed by one
canonical `INSERT INTO ... DEFAULT VALUES;` per empty row. Restore alone enables
a private positioned parser marker and requires exactly zero live columns and no
target list; it runs the common insertion path with genuine empty rows, not default
evaluation. Public DEFAULT VALUES remains `0A000` (`INSERT defaults are unsupported`);
`VALUES ()` remains a syntax error. ALTER TABLE is not admitted to Restore scripts.
Explicit ACL Dump rejection remains unchanged.

Owner-bearing feature controls exercise an ordinary owner of both a nonpublic
schema and its table, direct/prepared DROP, canonical owner directives, restored
owner authority and replay of the same dump on PostgreSQL. A separate distinct
schema/table-owner Restore fixture checks preserved ownership and schema-owner
denial after compaction. An ordinary distinct table owner still needs namespace
USAGE for qualified public SQL: native GRANT is not added, and Dump does not
silently discard explicit ACLs. PostgreSQL-only fixture grants provide its real
destination authority for replaying the distinct-owner dump.

Executable public feature witnesses (Go test reports actual pass/failure):

```sh
go test -count=1 -v -run '^TestAlterDropColumnSmoke$' ./smoke
go test -count=1 -v -run '^TestAlterDropColumnZeroColumnControlsSmoke$' ./smoke
go test -tags reference -count=1 -v -run '^TestReferenceAlterDropColumn' ./reference
```

Duplicate-relation `42P07` errors preserve decoded names inside literal outer
quotes, including embedded quotes and backslashes, both for an existing target
and for a competing publication. Failed CREATE preserves the winning definition
and rows; utility preparation succeeds before the execution-time failure.

Executable feature proof (Go test reports actual assertions):

```sh
go test -count=1 -v -run '^TestTableCore' ./internal/parser ./internal/engine .
go test -count=1 -v -run '^TestCatalogDrop' ./internal/engine
go test -tags reference -count=1 -v -run '^TestReferenceTableCore' ./reference
go test -count=1 -v -run '^TestTableCoreSmoke$' ./smoke
```

Unquoted names fold ASCII uppercase and truncate to 63 UTF-8-boundary bytes; quoted names preserve case and decode doubled quotes. A quoted identifier containing a dot is one component, e.g. `public."a.b"`. Ordinary strings use doubled apostrophes and literal backslashes. `E'...'` (or lowercase `e`) additionally decodes escaped apostrophes/backslashes, `\b\f\n\r\t\v`, one-to-three octal digits, `\x` with one-to-two hex digits, and `\uXXXX`/`\UXXXXXXXX` Unicode scalars, including adjacent surrogate pairs. Unknown backslash escapes retain the following character. Octal/hex escapes produce bytes, and the complete decoded token must be valid UTF8 without NUL. Adjacent bare quoted segments concatenate only across whitespace containing LF or CR (line comments allowed, block comments excluded), inheriting the first segment's ordinary/escape mode; UTF8 bytes may span segments, surrogate pairs may not. Whitespace, line comments and nested block comments are accepted elsewhere. Parameters are 1-based `$n`, inferred from insert destinations or equality columns. Repeated parameters of one canonical destination type share a binding; gaps fail with `42P18`, mixed destination types with `0A000`. Named arguments and other parameter dialects are not supported.

Dollar-quoted strings preserve their bodies exactly: apostrophes, backslashes, newlines, comments, semicolons and `$1` are text, not escapes, statements or parameters. `$$...$$` uses an empty tag; nonempty tags follow unquoted identifier spelling except that `$` is forbidden in the tag. Delimiters match case-sensitively, without folding or the identifier length limit. Separate a dollar literal from a preceding identifier/keyword with whitespace or punctuation: `VALUES$$v$$` is one identifier, not `VALUES` plus a string. A complete opener without an identical closer reports `42601`, `unterminated dollar-quoted string at or near "..."`, quoting the raw suffix and pointing to the opener's character position. An incomplete nonparameter opener such as `$tag` is instead syntax near `$`; digit-prefixed `$n` remains a parameter.

Identifier classification uses the complete PostgreSQL 18 release keyword inventory, including keywords whose commands are not implemented. Unqualified relation names, first relation components and column names accept unreserved and column-name keywords (`abort`, `by`, `insert`, `between`, `char`); reserved and type/function-name keywords require quoting in those positions (`"select"`, `"join"`). A relation component after the dot accepts every keyword category, so `public.select`, `public.authorization` and `public.from` are legal names and resolve identically to their quoted counterparts. Keyword identity is separate from supported type spelling: `bool`, `int4`, and `int8` are ordinary identifiers and retain their existing built-in type meaning in type position. The independent bare-label property does not imply alias support: valid labels such as `check` or `authorization`, and labels after `AS`, still fail with `0A000` / `projection aliases are unsupported`; malformed bare labels such as `char` and `day` fail with `42601`. Quoting, ASCII folding, UTF8 clipping, qualifications and original-source diagnostic positions are unchanged. See [PostgreSQL's keyword categories](https://www.postgresql.org/docs/18/sql-keywords-appendix.html).

IF and EXISTS retain their release unreserved and column-name categories, respectively, even when their syntax tokens implement `CREATE TABLE IF NOT EXISTS`. Both remain usable as unquoted identifiers and bare labels in the corresponding contexts; dedicated syntax recognition does not change their keyword metadata.

INSERT query inputs execute the selected SELECT/query-VALUES surface above, including parenthesized SELECT inputs. Parsing still consumes the complete script before execution. In `INSERT INTO t (select) VALUES (1)`, `(select)` is a parenthesized zero-target SELECT rather than a target column, and the following raw `VALUES` is the syntax error (`42601`, character position 24). `(SELECT)` alone is an INSERT-only one-occurrence zero-width source: omitted nullable targets initialize to NULL; an explicit nonempty target list fails the ordinary width check.

Generic function-name recognition follows the distinct IDENT/unreserved/type-or-function-name rule, not column-name legality: `between(x)` is malformed, while ordinary/U/T call names and separately recognized built-in constructs such as COALESCE remain unsupported (`0A000`), except the four baseline type-function casts documented below and the selected null-count functions. No general function or special expression is executed by this recognition. DEFAULT is a valid bare projection label but still an unsupported alias. ISNULL/NOTNULL execute as scalar null-test predicates in table SELECT; explicit `ORDER BY x NULLS FIRST/LAST` remains recognized but unsupported, and quoted operator spellings remain ordinary names/labels. Failure boundaries retain complete positioned diagnostics without global keyword-to-error promotion.

Except for the admitted baseline cast calls and the two selected null-count functions (including exact quoted `"num_nulls"` and `"num_nonnulls"` names), the same failure-only call guard applies at every named projection position: `SELECT x, abs(x) FROM t` and later COALESCE or nonbaseline quoted/type-function-name calls remain recognized unsupported features, while a later unquoted `between(x)` is malformed at its opening parenthesis. Recognition retains the original callee token and never reconstructs category identity from normalized projection strings.

NULLS ordering is admitted only when the next actual unquoted keyword token is FIRST or LAST (comments and case folding use the existing scanner). A one-token filter caches that token without rescanning. Invalid or quoted alternatives, or end of input after NULLS, leave the original keyword as the syntax error: `ORDER BY x NULLS bogus` reports `42601` at raw NULLS, not at bogus. The filter preserves original lexemes, character positions and scanner-error precedence; it neither changes keyword metadata nor enables explicit NULL ordering.

Run `go test -count=1 -run '^TestIdentifierSmoke$' ./smoke` for direct/prepared contextual names, complete diagnostics and native control round trips. Go test reports the result. Pinned real-reference fixtures are `go test -tags reference -run '^TestReferenceIdentifier.*$' -count=1 -v ./reference`; alias-valid queries deliberately assert PostgreSQL rows and native unsupported diagnostics separately.

Unicode escaped constants `U&'\0041'` and quoted identifiers `U&"\0041"` decode to `A`, preserving quoted-name case. Four hexadecimal digits or `+` and six digits specify a scalar; adjacent high/low surrogate escapes combine (for example `U&'\D83D\DE00'` is 😀). Doubling the escape character yields it literally. Optional `UESCAPE '!'` selects exactly one byte other than hexadecimal digits, `+`, either quote or scanner whitespace; normally decoded E/dollar strings are also valid arguments. Bare newline-separated string segments concatenate before Unicode decoding. Names clip decoded UTF8 at 63 bytes without splitting a character. B/X/N strings remain unsupported.

U& malformed digits, invalid scalar/surrogate values and invalid UESCAPE arguments report `42601`; malformed digits include Hint `Unicode escapes must be \XXXX or \+XXXXXX.` This differs from malformed E-string Unicode's `22025`. Decoder positions follow PostgreSQL's dequoted/concatenated-body arithmetic, so doubled quotes or removed continuation separators can make the cursor precede the raw escape. Raw lookahead lexical failures and UESCAPE argument validation take precedence over body decoding.

Run `go test -count=1 -run '^TestUnicodeEscapesSmoke$' ./smoke` for direct/prepared Unicode values/names, complete diagnostics, two-pool atomicity, cancellation and native-control round trips. Go test reports the result.
These ordinary-string defaults correspond to standard_conforming_strings=on.
With the session setting off, ordinary strings use that same escape decoder;
E strings always use it. Native Restore always uses the default-on parser.

Types are int2/int4/int8/text/bool/float4/float8/numeric/date, time/timestamp without time zone, uuid and bytea. Values returned to Go are `int64`, `string` (including DATE, TIME, exact NUMERIC and canonical UUID), `bool`, `float64`, finite timestamp `time.Time`, owned binary/infinity bytes, or nil. INSERT coerces unknown strings and supported bound values through PostgreSQL-style textual input, except raw BYTEA binds described below; typed integer/bool literals can assign to text but not each other, and integers can assign to floats. WHERE typed comparisons do not use assignment-only casts. Parameters support nil, integer, floating point, string, bytes, bool and `time.Time` after standard `database/sql` conversion. NULL equality is never true. ASC uses NULLS LAST; DESC uses NULLS FIRST. Text ordering uses C-locale bytewise comparison. INSERT is all-or-nothing, including multirow errors and cancellation before publication.

The internal typed foundation uses catalog-owned immutable descriptors (BOOL OID 16,
BYTEA 17, INT8 20, INT2 21, INT4 23, TEXT 25, FLOAT4 700, FLOAT8 701, DATE 1082, TIME 1083, TIMESTAMP 1114, UUID 2950; analysis-only UNKNOWN 705), concrete nullable values,
and bound assignment/comparison providers. The parser retains raw type syntax;
the engine resolves it and publishes parameter/result descriptions with column
origins. Prepared types come from destination columns, never the Go argument
kind. Same-type repeated parameters share one inferred slot; gaps fail `42P18`
and mixed-destination reuse remains the bounded native `0A000` limitation,
including mixed INT2/INT4/INT8 reuse. Each execution imports an owned private typed frame
against the current coherent binding. Driver adapters detach bytes and export
INT2/INT4/INT8 as `int64`, TEXT/UUID and exact NUMERIC as `string`, BOOL as `bool`, FLOAT4/FLOAT8 as `float64`,
DATE/TIME as canonical `string`, finite TIMESTAMP as `time.Time`, timestamp infinity as fresh
`[]byte("infinity")` / `[]byte("-infinity")`, BYTEA as fresh owned `[]byte`, and typed NULL as nil.
NULL still retains its concrete metadata identity.

BYTEA stores arbitrary octets as immutable raw bytes (OID17, typmod -1, no
collation). SQL quoted input accepts lowercase `\x` hex prefix with either-case
digits and space/tab/LF/CR only between complete byte pairs. Escape input accepts
double backslash and exactly three octal digits from `\000` through `\377`;
ordinary UTF8 contributes its encoded bytes. SQL's normal string-decoding layer
still applies; invalid UTF8/NUL SQL is not permitted. Malformed hex reports
22023 (`invalid hexadecimal digit: "X"` or
`invalid hexadecimal data: odd number of digits`); malformed escapes report
22P02 (`invalid input syntax for type bytea`). Eager literal errors retain
character-based quote positions, including Prepare on empty tables.

Inferred BYTEA string and `[]byte` binds are **raw octets**, matching lib/pq:
bound `\x41` stores four bytes, whereas SQL literal `'\x41'` stores byte 41.
Nil interface and typed nil `[]byte` are NULL; nonnil empty bytes and empty
string are nonnull empty BYTEA. Other SQL destinations retain textual byte
binds. Valuer executes once outside engine locks. Input buffers and each
exported result are detached; mutating returned bytes cannot alter storage or
retained cursors. BYTEA DatabaseTypeName/ScanType are BYTEA/[]byte even for
empty/NULL-only results and after EOF/Close. Native Length/DecimalSize are
unavailable; pinned pq reports Length MaxInt64 and unknown nullability.
Bytewise unsigned lexicographic comparison places shorter prefixes first.

`bytea_output` defaults to `hex`; `escape` doubles backslashes and octal-escapes
bytes below 32 or above 126. SHOW/SET/RESET/DEFAULT and transaction-local unwind
use the existing settings lifecycle. NOT NULL failing-row Detail genuinely uses
the statement-captured mode; raw SELECT bytes remain invariant under the setting.
Dump always writes quoted lowercase hex, including `'\x'` for empty and NULL
for null, independent of output/string settings. Restore privately parses
default-on strings, preserves live settings and retained snapshots, and
publishes no replacement on input/read/cancellation failure. BYTEA typed
literals, modifiers, casts/functions, arrays and wire serving are not added.

Feature smoke: `make smoke RUN='^TestByteaSmoke$' VERBOSE=1`.
Native witnesses: `make test PACKAGES=. RUN='^TestBytea.*$' VERBOSE=1`;
codec witnesses: `make test PACKAGES=./internal/sqltype RUN='^TestBytea.*$' VERBOSE=1`.
Pinned reference: `make reference RUN='^TestReferenceBytea.*$' VERBOSE=1`.

UUID stores all 128 bits inline, independently of UUID generation functions.
Contextual quoted inputs and string/textual `[]byte` binds accept uppercase or
lowercase hex, matching braces, omitted hyphens, and a single optional hyphen
after any internal group of four hex digits. Whitespace, URN prefixes, malformed
braces, nonhex digits and trailing text are rejected. Version and variant bits
are not restricted. Output and native Dump literals are always lowercase
`8-4-4-4-12`, for example `a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11`.
Comparison uses unsigned lexicographic order over all sixteen bytes, not GUID
mixed-endian order; ordinary strict NULL equality and ASC/DESC NULL policies apply.

Native UUID results export immutable canonical `string`, DatabaseTypeName UUID
and ScanType string, including empty/all-NULL results; nullability follows the
column declaration. Length and DecimalSize are unavailable (not Length16).
The pinned lib/pq adapter instead exports canonical owned `[]byte` and ScanType
`interface{}`; reference tests assert these adapter differences independently.
`database/sql` can scan native results into string, []byte, any or sql.NullString.
Bindings reuse the standard converter/Valuer path; byte binds are text, never
binary sixteen-byte UUIDs. General Go UUID structs/arrays are not added.

Malformed UUID input returns `22P02` and
`invalid input syntax for type uuid: "<original>"`. Literal errors occur eagerly,
including Prepare on empty tables, and point to the quote in character offsets.
Native parameter errors have no Position/Context; PostgreSQL's wire adapter adds
`unnamed portal parameter $1 = '...'`. Later tuple/input/constraint failures publish
no rows. UUID declarations and canonical quoted literals round-trip through the
existing atomic native Dump/Reset/Restore controls, with retained UUID snapshots
and metadata unchanged. Feature smoke: `make smoke RUN='^TestUUIDSmoke$'`;
real reference coverage: `make reference RUN='^TestReferenceUUID.*$'`.

UUID typed literals, casts, arrays, generators, version/timestamp extraction
functions and binary wire UUID APIs remain excluded. PostgreSQL disk storage,
network protocol serving, and the external pg_dump process/custom archives are
non-engine boundaries, not successful no-op implementations.

### Exact NUMERIC

NUMERIC, DECIMAL and DEC support unconstrained exact decimals or `(p[,s])`.
Precision is 1–1000; scale independently ranges from -1000 to 1000, including
scale greater than precision. Omitted scale is zero; unconstrained values
preserve input display scale instead of imposing zero. `"numeric"` is a catalog
name, but quoted `"decimal"` and `"dec"` are not aliases.

```sql
CREATE TABLE exact_values (id INTEGER, v NUMERIC, rounded NUMERIC(3,1),
                          thousands NUMERIC(2,-3), tiny NUMERIC(3,5));
INSERT INTO exact_values VALUES
  (1, 12345678901234567890.1234567890123456789000, -1.25, 1500, .009994);
SELECT v, rounded, thousands, tiny FROM exact_values ORDER BY id;
-- exact strings: 12345678901234567890.1234567890123456789000,
--                -1.3, 2000, 0.00999
```

Decimal fraction/exponent tokens always infer NUMERIC, even `1.` or `1e0`.
Signed integer/radix tokens infer INT4, then INT8, then exact NUMERIC beyond
int8; leading decimal zero is not octal. Digit separators and `0x`/`0o`/`0b`
integer forms use the shared literal/input path. Malformed syntax is distinct
from destination overflow. Integer/numeric equality stays exact beyond 2^53;
finite numeric equality ignores display scale. Numeric-to-integer assignment
rounds ties away from zero with checked range. Numeric-to-float conversion is
checked and uses the selected binary type; numeric-to-TEXT emits canonical text.

Numeric output is fixed decimal, never float64 or exponent notation. Examples:
`001.2300` → `1.2300`, `-0.000` → `0.000`, `1.230e-2` → `0.01230`.
Typmods round ties away from zero before checking the precision bound, preserve
`max(s,0)` fractional places and apply separately at each destination. NaN is
allowed with any typmod; Infinity/-Infinity require an unconstrained column.
Specials normalize case and Inf spellings; signed NaN is invalid. Ordering is
-Infinity < finite < Infinity < NaN, with NaN equal to NaN; NULL retains the
existing ASC-last/DESC-first and strict-equality behavior.

Result DatabaseTypeName is NUMERIC and ScanType is string, including empty and
NULL results. Scan into string or []byte preserves exact output. DecimalSize
reports declared precision/scale, including negative scale; unconstrained
numeric has unknown sizing. Bind arguments remain target-driven textual input,
not Go-kind inference. Prepared numeric constants validate input syntax during
Prepare, but typmod sizing happens at Execute: malformed `'oops'` is located at
the literal, while numeric-field overflow has Detail and no Position. Parameter
errors have no native Position/Context. Statement and Restore failures publish
no partial rows. Dump quotes canonical exact numeric output and declarations;
Reset/Restore preserve old cursor values/metadata and deterministic redumps.

`SELECT $1 FROM n WHERE v=$1` with a NUMERIC `v` fails during Prepare with
`42P08`, `inconsistent types deduced for parameter $1`, Detail `numeric versus
text`, Position `8`. Direct Query with a supplied argument reports the same
record at the execution boundary. With no arguments, native Query still reports
`42P08`, but lib/pq's PostgreSQL simple query reports `42P02`, `there is no
parameter $1`, Position `8`. These protocol records are asserted separately,
not normalized into parity.

Unconstrained storage is bounded to 131072 integer digits and 16383 fractional
places; excess format range is rejected, not silently truncated to zero.
Numeric SQL parsing does not broaden SET's plain-decimal-integer grammar:
settings' quoted numeric codecs retain their separate units/prefix behavior.
No serial, NUMERIC/SMALLINT arithmetic, general functions, numeric casts, general operator syntax or
SELECT-without-FROM expansion is introduced; existing SMALLINT participates in
exact numeric comparison and checked integer assignment.

Feature smoke: `make smoke RUN='^TestNumericSmoke$' VERBOSE=1`.
The same smoke includes signed integer carry failures beside successful extrema,
plus direct/prepared REAL/FLOAT8 assignments of 1.5, -1.5 and zero with a tiny
exponent. Populated float comparisons against 1.0 and 1e100 assert exact result
types even when no rows match, in both direct and prepared modes.
Real pinned PostgreSQL witnesses:
`make reference RUN='^TestReferenceNumeric.*$' VERBOSE=1`.
Native logical negative-scale metadata is asserted independently of lib/pq's
unsigned-scale decoding defect, not by accepting its incorrect DecimalSize.

### Binary floating point

REAL/FLOAT4 store IEEE binary32; DOUBLE PRECISION/FLOAT8 and bare FLOAT store
binary64. FLOAT(p) selects REAL for binary precision 1–24 and DOUBLE PRECISION
for 25–53, without a per-value typmod. Out-of-range precision fails `22023`
at the precision token, even for CREATE TABLE IF NOT EXISTS. Quoted `"float4"`
and `"float8"` are catalog names; quoted keyword aliases such as `"real"` are
not aliases. Result DatabaseTypeName is FLOAT4/FLOAT8, ScanType is float64,
including NULL/empty results; no fictitious DecimalSize or Length is reported.

Decimal inputs may be quoted, inferred numeric tokens or positional binds:
`INSERT INTO f VALUES(0.1)`. Floating arithmetic and floating-point casts remain unsupported.
Text REAL input rounds directly to binary32 (not through binary64):
`'7.038531e-26'` stores bits 0x15ae43fd. Bound float32 is already widened by
database/sql; bound float64 is serialized as pq-compatible decimal parameter
text and parsed according to its SQL destination, not its Go kind. Finite
overflow or nonzero input rounded to zero fails `22003`; nonzero subnormals
are accepted. Bad syntax fails `22P02`, preserving the original input; range
errors quote the recognized numeric prefix. Quoted textual input errors point
to the quote, while native parameter input errors have no Position/Context.
Conversion of an inferred NUMERIC literal to a float destination is instead
execution-owned: Prepare succeeds, and range failure has no Position/Context.

Portable input accepts decimal numbers, C-ASCII whitespace at the ends,
case-insensitive NaN, Inf/Infinity and signed infinities. Host strtof/strtod
extensions such as hex floats, signed NaN or NaN payloads are not admitted
natively and are not claimed portable PostgreSQL parity. NaNs compare equal
and sort above all non-NaNs, including Infinity; signed zeros compare equal
but retain their sign. Known integer and NUMERIC operands of float equality
convert to binary64, whereas unknown strings/parameters contextualize to the
column. Thus REAL `'16777216'` does not equal integer `16777217` or known
NUMERIC `16777217.0`, but does equal quoted `'16777217'` after binary32 input
rounding. Known NUMERIC against REAL uses checked FLOAT8 conversion, not REAL
narrowing; overflowing/underflowing FLOAT8 conversion is an execution error.

Output is PostgreSQL's default shortest-precise decimal with **open** rounding
boundaries (not plain Go shortest ties-to-even output), canonical
NaN/Infinity/-Infinity/-0, at most 9/17 significant digits. REAL uses fixed
notation at display exponents [-4,6), double at [-4,15); otherwise signed
lowercase `e` with at least two exponent digits. Selection of legacy
extra_float_digits output modes is not implemented. Dump quotes every float
output and emits canonical REAL/DOUBLE PRECISION declarations, so Restore
roundtrips payloads and specials through the existing supported grammar.
Native REAL export is the exact widened stored binary32; pq instead parses
the decimal output as binary64. Thus REAL `'0.1'` returns
0.10000000149011612 natively and 0.1 through pq; reference assertions preserve
this adapter distinction before comparing binary32 semantics. That comparison
must decode independently specified output text directly at 32 bits: narrowing
pq's binary64 export can double-round. For REAL `'7.038531e-26'`, native storage
has bits 0x15ae43fd, but narrowing pq's returned binary64 to binary32 produces
0x15ae43fe. Both adapter outputs are asserted separately, not normalized away.

Executable float acceptance (commands must run to establish verification):

```sh
go test -count=1 -v -run '^TestFloat.*$' ./internal/sqltype .
go test -count=1 -v ./internal/parser
go test -tags reference -count=1 -v -run '^TestReferenceFloat.*$' ./reference
go test -count=1 -v -run '^TestFloatSmoke$' ./smoke
```

These assert exact release output/bit witnesses, precision/metadata, independently
expected native/pq values, direct/prepared binds and diagnostics, NaN bags and
ordering, later-tuple atomicity, cancellation and retained control snapshots.

Table SELECT targets and scalar comparison filters use one immutable typed
expression binder/evaluator, also shared by INSERT and bounded setting arguments.
Untyped literal and parameter targets are named `?column?`; explicitly typed temporal
targets are named `date`, `time` or `timestamp`; physical columns retain their
names and origin metadata. Integer literals use INT4 in range and INT8 otherwise;
unknown output strings, NULL and otherwise unconstrained parameters resolve to
TEXT **after** filter analysis. Parameter occurrences retain their type at initial
analysis: a target `$1` starts unresolved before WHERE. Thus
`SELECT $1 FROM t WHERE id=$1` fails `42P08` with Detail `integer versus text`
at the target, rather than projecting INT4; a later BOOL constraint similarly
conflicts with TEXT fallback. `SELECT $1 FROM t` infers TEXT independent of the
Go value, and `SELECT $1 FROM t WHERE s=$1` works when `s` is TEXT.
Bare WHERE accepts BOOL columns/literals/parameters and contextual unknown
NULL/strings (`'yes'`/`'no'`); concrete non-BOOL input fails `42804`. Only TRUE
matches: FALSE and typed BOOL NULL/UNKNOWN do not. SELECT targets and WHERE admit
one binary comparison of existing scalar operands: `=`, `<>`, `!=`, `<`, `<=`,
`>` or `>=`. `!=` aliases `<>`. Either operand may be a literal, parameter or
unqualified column, including a baseline cast of those scalar operands; results have `?column?`/BOOL metadata with native bool
ScanType and no physical-column origin, even on empty input. Either NULL yields
BOOL NULL/UNKNOWN, including NULL compared with NULL; WHERE discards it.
INT2/INT4/INT8 compare exactly across widths without assignment narrowing or
float conversion. Existing TEXT/NAME, BOOL, FLOAT4/8, exact NUMERIC, UUID and
same-type DATE/TIME/TIMESTAMP comparison providers remain; no cross-temporal casts
are added. C text ordering and FALSE before TRUE apply.

Both operands are analyzed before unknown coercion. A quoted string or NULL
uses the known peer's type; two unknown operands choose TEXT (`'10'<'2'` is true).
Known TEXT does not become integer/BOOL implicitly. Each parameter occurrence
copies the shared type known when visited: `SELECT i=$1,j=$1 FROM t` fixes INT4
from `i`, then compares the known INT4 with BIGINT `j` without narrowing.
`SELECT i=$1,b=$1 FROM t` instead fails42883 for BOOL versus INTEGER.
An earlier bare `$1` still falls back to TEXT and can fail42P08; later inference
never rewrites it. Selected quoted-input failure is final, at the literal;
incompatible known operands fail eagerly in Query/Prepare at the operator,
even on empty tables, with the canonical operator spelling in diagnostics.
FROM is still required outside identity/settings special forms and the selected
null-count-only form. These scalar comparisons also form Boolean leaves above
NOT, AND and OR in SELECT targets and WHERE. Bounded integer arithmetic and
baseline explicit casts are scalar operands. Comparison chaining, aliases,
qualified column references, general functions, nonbaseline casts and comparison
in INSERT/settings remain excluded. Comparisons do not sort rows.
Selected null-count calls remain excluded from Boolean/comparison operands,
and Boolean/comparison/arithmetic expressions remain excluded from their arguments.
Former signed literal inputs remain admitted.

Feature smoke: `make smoke RUN='^TestCompareOpsSmoke$' VERBOSE=1`; independent
pinned PostgreSQL coverage: `make reference RUN='^TestReferenceCompareOps' VERBOSE=1`;
native coverage: `make test PACKAGES=. RUN='^TestCompareOps' VERBOSE=1`.

### Integer arithmetic

Table SELECT projections, comparison operands/WHERE and INSERT VALUES accept
INT4/INT8 `+`, `-`, `*`, `/`, `%`, unary `+`/`-` and arithmetic parentheses.
Multiplication/division/remainder precede addition/subtraction; binary operators
associate left, unary signs right. Division truncates toward zero and remainder
has the dividend's sign (`7/2=3`, `-7%2=-1`). Unary signs preserve width;
INT4 pairs produce INT4, either INT8 operand produces INT8. Width is fixed before
assignment: a BIGINT destination cannot prevent INT4 expression overflow.
Checked overflow reports `22003` (`integer out of range`/`bigint out of range`);
division/remainder by zero reports `22012` (`division by zero`), without invented
runtime positions. Minimum divided by -1 overflows; minimum remainder -1 is zero.
Strict NULL returns a result-typed NULL. Numeric literal minus is folded,
including grouped literals, so signed minimum constants retain integer width.
Use whitespace or parentheses before a negative remainder divisor (`i% -1`,
`i%(-1)`). Adjacent `%-` is one symbolic operator, not remainder followed by
negation; unsupported combined arithmetic spellings fail with a typed `42883`
at the operator rather than executing or publishing data. Contextual WHERE
and incompatible-assignment errors use the expression start and resolved type;
missing-operator errors retain the operator token location.

Parameters obtain width from a known integer peer, in SQL occurrence order,
not from the Go argument kind or assignment destination. Computed metadata is
`?column?`, INT4/INT8, native int64, with no physical column origin. Existing eager
SELECT and atomic INSERT/Restore publication remain unchanged; Dump stores
canonical values rather than expression source. Parameter-free arithmetic
Restore is supported and nested parameters are rejected.

Existing baseline INT4/INT8/TEXT/BOOL casts compose with these scalar expressions,
using `CAST`, postfix `::`, or the supported function-style cast names. Postfix
casts bind before unary signs: `-i::bigint` widens before negation, whereas
`(-i)::bigint` cannot rescue INT4 negation overflow. Similarly, casting an operand
can widen arithmetic; casting its result cannot undo overflow. Cast preflight
visits arithmetic children even on empty or filtered input, preserving conversion
errors without evaluating the arithmetic root early. Cast wrappers do not admit
computed arithmetic arguments into the bounded null-count functions. Cast
families and BYTEA capabilities remain unchanged.
Composition smoke: `make smoke RUN='^TestArithmeticCastCompositionSmoke$' VERBOSE=1`.

SMALLINT/NUMERIC/floating and other noninteger arithmetic remain explicitly
unsupported, without changing their input/comparison/assignment capabilities.
Previously supported signed NUMERIC literal input, such as `+1.5`, remains a
literal identity operation, not a NUMERIC arithmetic provider.
Unknown-only arithmetic fails `0A000`, `integer arithmetic requires known integer
or bigint operands`; unlike PostgreSQL it does not select FLOAT8 for unary unknown
`+` or emulate ambiguous full-provider overloads. Settings signatures, casts,
functions, aliases and general SELECT-without-FROM remain bounded as before.
Feature smoke: `make smoke RUN='^TestArithmeticSmoke$' VERBOSE=1`;
real pinned reference: `make reference RUN='^TestReferenceArithmetic' VERBOSE=1`.


Missing column and invalid SQL `$0` references fail during analysis with `42703`
and `42P02`, respectively, at the occurrence's one-based character position.
The first parameter gap fails `42P18` without a position in native direct/Prepare
and PostgreSQL Prepare. PostgreSQL's zero-argument direct simple query instead
rejects `SELECT $2 FROM t` with `42P02` at `$2`; this protocol boundary is asserted
independently, not normalized into parity. Missing actual Go arguments remain
ordinary adapter/count errors, not PostgreSQL protocol errors.
Prepared executions retain fixed inferred usage and use fresh private
values. Cursors retain captured multiplicity, imported parameters and metadata
across appends, Reset and Restore; no row order is promised without ORDER BY.

Run `go test -count=1 -v -run '^TestExpressionCoreSmoke$' ./smoke` for the public
direct/prepared scalar, BOOL/NULL, diagnostic, retained-cursor/control and atomicity
witness. Independent native/unit checks are
`go test -count=1 -v -run '^TestExpressionCore.*$' .` and
`go test -count=1 -v -run '^TestExpression.*$' ./internal/engine`;
the real pinned reference is
`go test -tags reference -count=1 -v -run '^TestReferenceExpressionCore.*$' ./reference`.
Go test reports assertion failures or PASS; these commands require execution to
establish verification.

### Null and Boolean comparison predicates

Table SELECT projection and WHERE support scalar `IS [NOT] NULL`, its
`ISNULL`/`NOTNULL` aliases, `IS [NOT] DISTINCT FROM`, and all six Boolean tests:
`IS [NOT] TRUE`, `IS [NOT] FALSE`, `IS [NOT] UNKNOWN`. Each returns a nonnull
BOOL (`?column?`, OID16, typmod−1, native bool ScanType, scalar origin zero).
Expression nullability metadata remains unknown even though these values are
never NULL. Null tests accept every supported scalar type; Boolean tests require
BOOL, contextualizing unknown strings/NULL/parameters but rejecting concrete
nonboolean inputs with `42804` at the operand.

DISTINCT treats two NULLs as not distinct, one NULL as distinct, and nonnull
values through the existing equality provider (including exact numeric,
cross-width integer, NaN, UUID and temporal semantics). NOT DISTINCT is the
complement. Both operands can be admitted scalar atoms, comparisons or grouped
predicates/logical expressions. Unknown operands contextualize to the known
opposite type; two unknown operands use TEXT. Incompatible concrete inputs fail
`42883` at IS
with the ordinary equality signature, even on empty tables or typed NULLs.
An undecorated NULL literal on either side instead becomes a nullness test:
`$1 IS DISTINCT FROM NULL` therefore does **not** infer a parameter type.
Bound nil is not a raw NULL literal: `v IS NOT DISTINCT FROM $1` infers the
column's type and selects its NULL rows when bound nil.

Null tests do not infer parameter types. An entirely unresolved slot fails
`42P18` without Position; an earlier unknown nested occurrence left behind by
a later constraint fails `42P08` at that occurrence. For example,
`SELECT $1 IS NULL,$1 IS TRUE FROM t` fails at character8, while reversing
the targets succeeds with a BOOL slot. Root target TEXT fallback and sparse
parameter/protocol distinctions described above remain unchanged.
Selected occurrence consistency is checked before unresolved shared slots or
gaps. Thus `SELECT $1 IS NULL,$1 IS TRUE,$2 IS NULL FROM t` reports positioned
`42P08` for the earlier `$1`, not `42P18` for `$2`; likewise an inconsistent
`$2` occurrence takes precedence over the missing `$1` slot. With no occurrence
mismatch, `$1 IS TRUE,$2 IS NULL` still reports unpositioned `42P18` for `$2`.

Parentheses group admitted atoms, comparisons, predicates and logical expressions.
Unary postfix tests nest left: `TRUE IS NULL IS TRUE` and `v IS NULL ISNULL`
both return false.
IS/ISNULL/NOTNULL share nonassociative precedence where a binary DISTINCT
right operand competes with another ungrouped predicate:
`1 IS DISTINCT FROM 1 IS TRUE` fails `42601`; group either intended nesting.
`(v IS NULL) IS TRUE` and `TRUE IS DISTINCT FROM (v IS NULL)` work.
Postfix casts complete their target type before a predicate continuation:
`v::int4 IS NULL`, `v::bool IS TRUE` and `v::int8 IS DISTINCT FROM 0`
apply the predicate to the cast result, not to a multiword type name.
Scalar comparisons bind more tightly than these predicates, which bind above
NOT, AND and OR: `v=1 IS TRUE` and `(v=NULL) IS UNKNOWN` work in projection
and WHERE. Ordinary `v=NULL` remains UNKNOWN and selects no rows; no
transform_null_equals behavior is added. Comparison and predicate operands
admit bounded INT4/INT8 arithmetic and existing baseline casts; comparison
chaining remains excluded. General functions, nonbaseline casts, aliases,
row/composite tests and no-FROM scalar SELECT remain excluded except for the
selected null-count call roots described above. Those calls remain standalone
projection roots, not predicate operands; predicates remain excluded from their
arguments. ANY and null-test unknown occurrences share binding-order validation:
`num_nulls($2),$1 IS NULL,$1 IS TRUE,$2 IS TRUE` fails42P08 for$2 at18,
while reversing the first two targets fails for$1 at8. Still-unresolved
occurrences do not mask a later mismatch, and mismatches precede slot gaps.
Ordinary INSERT VALUES and settings retain scalar-only arguments. INSERT SELECT
executes the same admitted predicate and selected null-count projection roots;
`INSERT INTO counts SELECT num_nulls(v) FROM t` inserts one count per source row.
Appending `VALUES (1)` instead reports `42601` at VALUES, character47, so an
implemented projection call cannot mask a malformed trailing suffix.

Executable acceptance (run these commands to establish verification):

```sh
make smoke RUN='^TestCompareNullSmoke$' VERBOSE=1
make test PACKAGES=. RUN='^TestCompareNull.*$' VERBOSE=1
make test PACKAGES=./internal/engine RUN='^TestExpressionCompareNull.*$' VERBOSE=1
make test PACKAGES=./internal/parser VERBOSE=1
make reference RUN='^TestReferenceCompareNull.*$' VERBOSE=1
```

These exercise independent direct/prepared truth/null/distinct matrices,
held typed parameters, complete eager diagnostics/phases, precedence and
admission, provider families, dependency/ACL manifests, cancellation and
captured computed BOOL results across native controls. Reference checks use
the unchanged pinned real PostgreSQL18.0 fixture, asserting expectations
before backend parity. Source/manual semantics:
[PostgreSQL18 comparison predicates](https://www.postgresql.org/docs/18/functions-comparison.html)
and [operator precedence](https://www.postgresql.org/docs/18/sql-syntax-lexical.html).

### Explicit baseline casts

Baseline casts also serve as the existing comparison and null/Boolean predicate
operands, Boolean leaves, and selected null-count arguments. They do not admit logical/comparison input to
INSERT/settings or selected calls as Boolean/comparison leaves. The selected
null-count-only no-FROM form may use literal/parameter cast arguments; a standalone
cast still requires FROM.
Grouping applies to existing scalar operands, including columns: `(id)=1` and
`(i)<(j)` are admitted, with or without a cast. It does not turn a comparison or
logical expression into a scalar comparison operand: `(id=1)=TRUE` and comparison
chaining remain excluded.

`CAST(expression AS type)`, `expression::type` and `int4(expression)`,
`int8(expression)`, `text(expression)`, `bool(expression)` execute through the
shared typed expression path in table SELECT, WHERE and INSERT VALUES.
Nested/grouped casts and postfix chains are supported. Standard/postfix targets
accept INTEGER/INT/INT4, BIGINT/INT8, BOOLEAN/BOOL and TEXT aliases; function-style
names are only int4/int8/text/bool, including their quoted lowercase spellings.
Unquoted `integer(...)`, `bigint(...)`, `boolean(...)` and `int(...)` remain syntax
errors; this feature does not introduce general function resolution.

| Source | INT4 | INT8 | TEXT | BOOL |
| --- | --- | --- | --- | --- |
| INT4 | identity | widen | decimal output | zero false, nonzero true |
| INT8 | checked narrow | identity | decimal output | unsupported |
| TEXT | integer input | bigint input | identity | boolean input |
| BOOL | false 0, true 1 | unsupported | `false` / `true` | identity |

NULL retains the target type; unsupported pairs fail even for typed NULL or empty
relations. No transitive conversion is inferred. Explicit casts involving INT2,
NUMERIC, floats, temporal types or UUID remain native `0A000` boundaries, including
same-type casts. Existing assignment/implicit policies are unchanged: for example,
BOOL cannot assign to INT4 without a cast, and concrete TEXT cannot assign to INT4.
Unknown quoted input may still acquire a destination type directly.

The first standard/postfix cast of an unresolved parameter fixes its source type:
`SELECT $1::int4,$1::text FROM t` imports INT4 and returns INT4/TEXT. Later casts
convert that source rather than re-inferring it. Earlier unresolved targets retain
the occurrence-sensitive rule: `$1,$1::int4` fails `42P08` (integer versus text).
`int4($1)`, `int8($1)` and `bool($1)` are ambiguous (`42725`); `text($1)` infers TEXT,
and `int4($1::text)` imports TEXT then converts. Prepared executions retain fixed
metadata and private parameter frames.

Standard/postfix output names preserve column/function names, otherwise use the
normalized target name; function casts use their callee name. Same-type column
casts reduce to the column and retain physical origin; conversions have no column
origin. Unknown literal input errors carry the quote's character Position.
Typed conversion/overflow errors have no Position and finalize before Query exposes
a cursor. Unsupported standard/postfix pairs use `42846`; function pairs use `42883`
with PostgreSQL's function hint. Multirow INSERT and staged Restore remain atomic.
Dump emits canonical typed literals, and Restore recursively admits literal casts
while rejecting nested parameters at their token.
Immutable row/stage-independent conversions, including imported parameter casts,
are preflighted in a fresh execution-private frame even when the relation is empty
or WHERE selects no rows; Prepare still succeeds for typed conversion failures.
SELECT preflight runs outside engine locks before executor table privileges are
checked; namespace lookup/USAGE remains earlier. Valid casts still require SELECT.
Row-dependent conversions are evaluated only for admitted input rows. This bounded
materialization does not implement PostgreSQL's custom/generic plan-cache switching.
Preflight also traverses null tests, Boolean tests and both DISTINCT operands,
including raw-NULL rewrites, without folding the predicate or pruning its dependencies.
Errors from a cast inside equality keep their original diagnostic and Position;
operator-resolution errors report the actual bound operand types.

Public feature smoke: `make smoke RUN='^TestCastExplicitSmoke$' VERBOSE=1`.
Review-regression smoke: `make smoke RUN='^TestCastExplicitReviewSmoke$' VERBOSE=1`.
Parameter/ACL smoke: `make smoke RUN='^TestCastExplicitPreflightSmoke$' VERBOSE=1`.
Pinned PostgreSQL coverage: `make reference RUN='^TestReferenceCastExplicit.*$'`.

Normalized integer parameters targeting INT2/INT4/INT8 and Boolean parameters targeting
BOOL use checked scalar imports without successful-value formatting or reparsing.
They retain the inferred descriptor and cancellation checks; INT2/INT4 overflow keeps
the textual-input diagnostic rather than the assignment-narrowing diagnostic.

Integer textual input (quoted constants and string/byte parameters) accepts
optional signs, leading/trailing C whitespace, decimal leading zeros, radix
prefixes and single digit separators, including `0x_FF`, `0o17`, `0b10_10` and
`1_000`. Malformed input is `22P02`; width overflow is `22003`. Parsing order
matters: `2147483648x` is malformed integer input, but a sufficiently long
overflowing digit prefix can fail range before trailing junk. Non-ASCII
whitespace is not C whitespace. Boolean input supports case-insensitive unique
prefixes of true/yes/on and false/no/off, plus 1/0. Boolean codec text output is
t/f; Boolean assignment to TEXT is true/false, independently of driver output.
Integer WHERE literals compare across widths without assignment narrowing:
`int4_column = 2147483648` is valid and matches nothing, while a parameter in
that predicate retains INT4 input and rejects the same overflowing bound text.

SMALLINT/INT2 stores the signed range -32768 through 32767 and returns native
`int64` with INT2 metadata; INTEGER and BIGINT retain their signed-32/signed-64
ranges and existing inline storage. Integer input diagnostics use English names:
`invalid input syntax for type smallint: "oops"` (`22P02`) and
`value "32768" is out of range for type smallint` (`22003`), with `integer` or
`bigint` for those widths. Original input spelling is retained. SMALLINT parsing
order likewise distinguishes `32768x` (`22P02`) from `327680x` (`22003`).
Typed INT4/INT8 assignments to SMALLINT prepare successfully and fail execution
with `smallint out of range` on overflow; INT2 widens implicitly to INT4/INT8
and the supported floats. Bare SQL integer literal inference remains INT4/INT8.
All conversions precede statement publication; a bad later tuple publishes no
rows. Dump emits canonical SMALLINT/INTEGER/BIGINT declarations and decimal
values, and staged Restore preserves widths and retained cursor snapshots.

`make smoke RUN='^TestSmallintSmoke$' VERBOSE=1` exercises the public direct and
genuinely prepared smallint path, bounds/English diagnostics, INT2 metadata,
NULL, atomicity/cancellation, mixed-width controls and authorization. Independent
focused witnesses are `make test RUN='^TestSmallint' PACKAGES='./internal/sqltype ./internal/parser ./internal/engine .' VERBOSE=1`
and `make reference RUN='^TestReferenceSmallint' VERBOSE=1` against the pinned
PostgreSQL 18.0 oracle. These commands require execution to establish results.

`go test -count=1 -run '^TestTypeCoreSmoke$' ./smoke` exercises public
direct/prepared inference, codecs, exact diagnostics and phases, metadata, byte
ownership, multirow atomicity, cancellation and four-kind native round trips with
retained raw cursors. Go test reports the result.

Focused independent witnesses are `go test -run '^TestTypeCore.*$' -count=1 -v .`,
`go test ./internal/sqltype ./internal/engine`, and the pinned real-reference
`go test -tags reference -run '^TestReferenceTypeCore.*$' -count=1 -v ./reference`.
Dump uses descriptor-owned canonical SQL literals, and staged Restore uses the
same binder/codecs before replacing the catalog once; existing snapshots retain
their old values, types and column metadata.

### Time without time zone

`TIME`, `TIME(p)` and optional `WITHOUT TIME ZONE` declarations and typed literals
work through the same binder, parameter inference, metadata and native controls
as other supported types. TIME has OID 1083; omitted precision is -1, explicit
0–6 is retained by declarations, LIKE, prepared descriptions and Dump. Higher
precision warns with `22023` (`TIME(7) precision reduced to maximum allowed, 6`)
and clamps to 6. Keyword negative/signed precision is a positioned syntax error;
quoted `"time"(-1)` reaches the provider's `22023` negative-precision diagnostic.
Quoted numeric/Unicode modifiers use the same INTEGER and scanner contracts as
timestamp precision below. Utility Prepare remains effect/notice-free; typed
literal warnings occur once when analyzed, not every held execution.

The stored clock ranges from `00:00:00` through distinct `24:00:00`, inclusive.
Output and native ScanType are **string**, with `HH:MM:SS` and a trimmed optional
microsecond fraction, never a date/zone suffix. The pq reference adapter instead
returns `time.Time` anchored at year 0 January 1, or January 2 for `24:00:00`;
this is an adapter boundary, not a PostgreSQL text-output difference.

Accepted inputs include colon and compact clocks, AM/PM (including 0AM/0PM),
minute/second fractions (`04:05.5` means `00:04:05.5`), seconds 60 carry, and
limited optional Gregorian dates, ISO T/Julian labels and recognized noise
(`on`/`at`). Standalone weekday tokens are rejected: `04:05 on Monday` is
invalid TIME input, even though weekday words are recognized for timestamps.
Input fractions round to microseconds ties-to-even; destination precision then
rounds half-up. `23:59:59.5` at precision 0 becomes `24:00:00` without wrapping;
invalid `24:00:00.1` fails before sizing. Same-type comparison/ORDER BY preserve
the endpoint and ordinary NULL placement; no TIME/TIMESTAMP comparison or casts
are admitted. String, owned byte, nil and `time.Time` parameters use the TIME
destination, retaining written wall-clock fields rather than converting to UTC.
Colon-clock component syntax/ranges are checked while reading the token; the
whole-clock domain is checked only after later tokens, optional-date validation
and AM/PM conversion. Thus duplicate zones in `25:00 UTC UTC` yield `22007`
before the deferred hour overflow, and `2003-13-07 25:00` retains the DateStyle
Hint. TIMESTAMP keeps its distinct early whole-clock check. Incompatible typed
assignments identify the actual source (`time without time zone` for TIME).
PostgreSQL's TIMESTAMP-to-TIME assignment cast is an explicit unsupported native
boundary, not a claimed parity behavior.

Numeric zones, release abbreviations and full names are **validated then ignored**;
`04:05:06-08` stays `04:05:06`. Unknown zones and invalid displacements fail;
unknown full-zone diagnostics use the lowercased token, e.g.
`time zone "america/bogus" not recognized` for `America/Bogus`.
Historically varying full names (including currently no-DST Asia/Kolkata) need
a valid supplied date; historically fixed names/aliases such as Etc/GMT+5 need
none. The immutable inventory covers every REL_18_0 IANA 2025b Zone/Link,
classified from historical fat TZif ttinfos and release-loaded footer offsets,
with no runtime zone I/O or host-zone dependence. Dynamic abbreviations need no
date; DST-modifier and duplicate-field restrictions still apply.
Single-token POSIX `X0` and equal-offset `X0Y0` are fixed, unlike `X0Y`.

`allballs` is midnight. `now` uses the transaction-owned UTC start; held typed
and contextual constants freeze at analysis, while parameters read each execution
start (shared within one explicit transaction). TIME `now` does not reserve a
zone field, unlike TIMESTAMP `now`; `allballs` does. Malformed fields, modifiers,
later tuples and staged Restore failures never publish partial state. Dump quotes
canonical clocks and Restore preserves precisions, NULLs, endpoints and old cursors.
TIME WITH TIME ZONE/timetz, session TimeZone/DateStyle, temporal functions,
casts and no-FROM SELECT remain outside this feature.

Feature smoke: `make smoke RUN='^TestTimeSmoke$' VERBOSE=1`.
Native coverage: `make test PACKAGES=. RUN='^TestTime.*$' VERBOSE=1`.
Real pinned reference: `go test -tags reference -count=1 -timeout=60s -v -run '^TestReferenceTime.*$' ./reference`.

### Civil DATE

`DATE` and quoted `"date"` work in declarations and typed literals; `date`
remains an ordinary table/column identifier. DATE has OID1082, no precision,
and DatabaseTypeName DATE / ScanType string even for empty and NULL results.
All nonnull exports are canonical strings: `YYYY-MM-DD`, expanded years,
` BC`, `infinity` or `-infinity`. lib/pq instead exports finite `time.Time`
and default infinity `[]byte`; these adapter differences are intentional.

DATE modifier literals share the scalar call opener with baseline casts, but
retain raw numeric modifiers until DATE's no-typmod diagnostic is resolved.
This does not admit `date(...)` function casts or change int4/int8/text/bool
calls; standard/postfix casts involving DATE remain unsupported.

Input accepts ISO and unambiguous month names, compact dates, Julian days,
ordinal dates, fixed MDY ambiguous numeric input, BC, epoch and infinities.
Examples `January 8, 1999`, `19990108`, `1999.008` and `J2451187` all produce
`1999-01-08`; `2001--01-01` is also valid and produces `2001-01-01`.
The proleptic Gregorian finite domain is **4714-11-24 BC through 5874897-12-31**,
including dates beyond TIMESTAMP's range. There is no textual year zero;
1 BC is astronomical year zero for leap calculations. Comparisons and ordering
use civil days, with infinities outside all finite values and ordinary NULL rules.

Validated trailing clocks/zones never shift the written date or carry into it:
`1999-01-08 24:00` remains `1999-01-08`. Bound `time.Time` similarly uses its
own location's civil Y/M/D and ignores time/nanoseconds; Go year zero means 1 BC.
Strings, owned textual bytes and nil use the existing type-owned input path.
The pinned pq adapter shifts BC `time.Time` years with Go `AddDate` before
encoding: its year-zero February 29 bind becomes `0001-03-01 BC`. Native DATE
preserves `0001-02-29 BC`; textual BC input avoids that pq encoding limitation.
`now`, `today`, `yesterday` and `tomorrow` use transaction-start UTC; prepared
typed/contextual constants freeze at analysis, while parameters read execution
start. No DateStyle/TimeZone settings, CURRENT_DATE function, cross-temporal casts,
arithmetic, arrays or no-FROM SELECT are added.

Malformed input is `22007`; invalid calendar fields and finite-range failures
are `22008`, preserving original input and English diagnostics.

Failed `time.Time` range imports format the original temporal input only on
failure, including clock, fractional seconds, numeric offset (`Z` for UTC,
offset seconds when present) and a trailing ` BC` for BC years. For example,
year 5874898 at UTC midnight reports
`date out of range: "5874898-01-01 00:00:00Z"`.
Years whose serialized AD/BC number exceeds INT32 fail earlier numeric decoding
with `date/time field value out of range`, rather than `date out of range`.
Successful imports remain civil-day-only, with no clock/zone validation or conversion.

Elementary month/day overflow includes the DateStyle Hint. Literal errors fail Prepare
even on empty tables and point to the quote in character positions; native
bind errors have no Position/Context. Incompatible typed DATE assignments name
the source type `date` (`TIME`/`TIMESTAMP` retain `without time zone`) and fail
`42804` during analysis/Prepare without changing stored rows.
DATE modifiers fail `42601` at the type name (CREATE execution, typed-literal
analysis). This no-modifier rejection precedes integer conversion or unary-plus
validation of parsed modifiers: `date(3.0)`, `date(2147483648)` and `date(+3)`
all report `type modifier is not allowed for type "date"`. Ordinary parser
syntax errors still take precedence; TIME/TIMESTAMP/NUMERIC modifier rules are
unchanged. Later-row failures and canceled staged Restore leave committed data
unchanged. Dump quotes canonical DATE values;
Reset/Restore preserve retained snapshots and metadata. Unordered queries have
no portable row-order guarantee.

Feature smoke: `make smoke RUN='^TestDateSmoke$' VERBOSE=1`.
Independent pinned oracle: `make reference RUN='^TestReferenceDate' VERBOSE=1`.
Native coverage: `make test PACKAGES=. RUN='^TestDate' VERBOSE=1`.

### Timestamp without time zone

`TIMESTAMP`, `TIMESTAMP(p)` and their explicit `WITHOUT TIME ZONE` forms
work in table declarations and typed literals in the existing scalar positions.
Omitted precision is typmod -1; explicit 0–6 is retained in column/result metadata
and native Dump. Plain LIKE copies that precision. The quoted catalog type
`"timestamp"` also admits integer modifiers, including unary-minus constants.
Ordinary signed, empty, multiple and noninteger modifiers are syntax errors;
quoted negative precision reaches the type provider and fails `22023`.
Complete quoted numeric spellings pass through the existing INTEGER input
function at CREATE execution: decimal/exponent input fails `22P02`, and integer
overflow fails `22003`. Quoted unary-plus modifiers are nonsimple syntax: Prepare
accepts them, but CREATE execution fails `42601`,
`type modifiers must be simple constants or identifiers`, at the type opener.
An existing authorized `IF NOT EXISTS` destination skips semantic modifier input,
not malformed numeric syntax. Greater precision succeeds with
WARNING `22023`, for example `TIMESTAMP(7) precision reduced to maximum allowed, 6`,
and effective precision 6. Typed-literal warnings occur at initial analysis/Prepare,
not every held execution; CREATE warnings/errors occur at utility execution.
Column declarations resolve the raw modifier again while building the relation
descriptor, so excess precision emits two execution warnings: the first with
the declaration position, then one without a position.
An authorized existing IF NOT EXISTS table skips semantic precision validation,
not malformed syntax. `client_min_messages=error` filters these warnings.
Unicode-escaped quoted identifiers resolving to `"timestamp"` use the same
modifier rule. Timestamp-related keywords retain their release identifier and
bare-label categories. A cast applied to a typed timestamp literal remains a
positioned `0A000` unsupported cast, not an executable expression.

Input combines dates and times using fixed **MDY** ambiguous ordering, and output
is fixed **ISO** civil date/time with a space, seconds, trimmed fractional zeros
and no offset. Supported families include ISO dates/space or T separators,
full/abbreviated text months and mixed delimiters, MDY slash/dash/dot dates,
compact dates/times, short-year pivot 00–69→2000–2069 / 70–99→1970–1999,
ordinal year/day and Julian day (including fractions), colon times, AM/PM,
recognized weekday/noise tokens, AD/BC, `epoch`, and infinities. Four-digit `0000`
is invalid; 1 BC is exported as astronomical Go year 0. Invalid Gregorian dates
are rejected, not normalized; valid `24:00:00` and `23:59:60` carry to the next day.
Accepted numeric labels are Julian `j` / `jd` / `julian` and the ISO `T`
separator. Interval-like `y`, `m`, `d`, `h`, `mm`, `s`, `dow`, `doy`,
`isodow` and `isoyear` labels are not timestamp input fields and fail rather
than silently constructing a date/time.
Recognized but unsupported unit labels still preserve numeric-error precedence:
`y 2147483648` fails field-range `22008`, whereas `y 1` is malformed `22007`.
An intervening `at`/`on` does not consume a pending label. Julian labels likewise
survive noise, weekday/era/meridian and ordinary zone fields (`J on UTC 2451187`
is January 8, 1999). ISO `T` preserves numeric prefix overflow before duplicate
time rejection; a punctuated zone immediately after pending `T` is not a named
zone input (`1999-01-08 T America/Bogus` is malformed, not unknown-zone `22023`).
Numeric zone suffixes, all release Default abbreviations, case-insensitive IANA
names/aliases and admitted POSIX indications are validated then **ignored**:
`TIMESTAMP WITHOUT TIME ZONE '2004-10-19 10:23:54+02'` remains 10:23:54.
DST gap/fold fields likewise remain civil fields. Arbitrary trailing suffixes are
not discarded. POSIX rule strings are subject to timestamp token boundaries:
comma-separated rules/angle-bracket names are not interchangeable with SET TIMEZONE.
Unbracketed POSIX standard/daylight names may be one or two characters; they
have no artificial three-character minimum. POSIX offset seconds admit 60,
unlike numeric ISO zone seconds. Thus `1999-01-08 04:05:06.1e2` is valid:
the tokenizer separates fraction `.1` from POSIX zone `e2`, retaining civil
04:05:06.1 rather than interpreting an exponent. The fractional-field helper
itself accepts digits only after the dot; whole-input tokenization is distinct.
Recognition uses the immutable PostgreSQL REL_18_0/IANA 2025b name index without
host-zone-file I/O or offset/transition conversion.
The `dst` modifier is admitted with a fixed standard abbreviation or numeric
zone, in either order, without changing civil fields (`PST dst`, `dst +02`).
It conflicts with fixed daylight abbreviations such as `EDT`; dynamic names
such as `MSK`, full IANA/POSIX zones, duplicate modifiers and a modifier without
a zone are invalid. A full punctuated zone is looked up before duplicate-zone
rejection: unknown `America/Bogus` after `UTC` still fails `22023`, while a
known second zone fails `22007`. DST/daylight fields also participate in the
delimited-date completion mask: leading `PST` is exempt as TZ, leading `EDT`
or `PST dst` is not; separately decoded textual-month dates remain distinct.
Combined fields are interpreted sequentially: `January 8 040506-8 1999`
fails `22007` because the date is incomplete when that compact numeric field
is encountered. Compact time with a completed date and textual colon-time
forms remain supported.
Numeric dispatch also depends on previously decoded date and time bits, not
width alone: `January 8 04:05:06 120000 PST` retains year 120000 and civil clock
04:05:06. An overflowing year in that ordering is field-range `22008`.
Small ordinary numeric date fields can supply a fraction before their date
role is assigned: `1999 1 8.5` means January 8 at midnight plus .5 seconds.
This does not admit every dotted field ordering: `1999 1.5 8` and
`1999 1.1 8` are malformed (`22007`), not fractional-month equivalents.
A later clock replaces that fraction; a fraction after an already decoded
clock replaces its fractional seconds. Full numeric-field length controls
short-year interpretation (`January 8 99.1` is year 0099, not 1999).
Compact clocks follow release summation semantics: `250000` carries into the
next day at 01:00, `049900` becomes 05:39, and `040599` becomes 04:06:39.
Colon clocks still reject hour 25, minute 99 and second 99. AM/PM validation
precedes compact carry, and the final summed value must remain in finite range.
Ordinal day 366 is accepted in either year/day spelling: `1999.366` and
`1999 366` roll into 2000-01-01, while `2000.366` is 2000-12-31.
The converted civil date is then checked against the finite timestamp range.
Only ordinal values 1–366 claim the day-of-year field. In `1999 367`, the
second numeric token falls back to a month, whose range is validated before
rejecting the incomplete date: `22008` with a DateStyle hint. In contrast,
`1999.367` fails the delimited-date completion rule first (`22007`, no hint).
Only present year/month/day fields are validated; missing fields do not
invent zero-valued calendar errors.
Weekday vocabulary includes `weds`; a stated weekday does not validate calendar
agreement with the numeric date.
Julian integer and decimal fields are decoded separately: `J2451187.1`
is exactly 1999-01-08 02:24:00. A present fractional suffix supplies the time
even when zero, so `J2451187.0 04:05:06` is a duplicate-time error.
A pending Julian label also admits an empty integer before a fraction:
`J .1` is Julian day zero plus .1 day (4714-11-24 BC at 02:24), while
`J .` is that date at midnight. Both claim the time field and reject an
additional clock; ordinary numeric-date parsing still requires an integer.
A colon time may end with a decimal point (`04:05:06.` means zero fraction);
bare compact or Julian trailing dots follow different lexical branches and
are invalid. Malformed fields fail `22007`, overflowing date/time integers
fail `22008`, and overflowing numeric zone components fail `22009`.

Fractional seconds first convert to binary64 seconds, then scale to microseconds
and round ties-to-even. Go `time.Time` parameters use the same two-stage
conversion without formatting: 125500 nanoseconds becomes 125 microseconds,
while 126500 becomes 127, matching their decimal timestamp input rather than
rounding the nanosecond count directly. Civil fields and validated-but-ignored
offsets are preserved; typed and destination precision still size afterwards.
Julian fractions instead multiply the separately decoded fraction by a day's
microseconds and truncate to integer microseconds, matching the release codec.
Own typed-literal precision and destination precision are separate later sizing
stages, rounding signed microseconds away from the 2000-01-01 epoch on ties:
p0 rounds 2000-01-01 00:00:00.5 up, but 1999-12-31 23:59:59.5 down.
Comparisons never resize the right input to a column's precision. Finite input spans
4714-11-24 BC through 294276-12-31; rounding its last microsecond can produce finite
294277-01-01, although direct external input of that date fails. Dump represents this
rounded endpoint with `TIMESTAMP(0) '294276-12-31 23:59:59.999999'`, so staged Restore
reconstructs it through the ordinary parser/input/sizing path even into a p6 column.

Native `now`, `today`, `tomorrow` and `yesterday` strings use the transaction-owned
UTC civil start, captured once for an explicit/implicit transaction. All rows and
statements in one explicit transaction share it. Prepared timestamp constants,
including contextual quoted inputs, freeze when analyzed; string/byte parameters
are read afresh at execution using that execution transaction's start.
SQL BEGIN and native BeginTx use the same clock owner. COMMIT/ROLLBACK AND CHAIN,
including recovery by COMMIT AND CHAIN from an aborted block, capture a fresh
UTC start for the new incarnation; redundant BEGIN and control Prepare do not
reset it. Plain COMMIT/ROLLBACK clear it before the next implicit transaction.
Independent public, codec/engine and pinned reference assertions retain both
the floating-point and timestamp contracts after integration.
Run `go test -count=1 -v -run '^TestTimestampSQLTransactionChainSmoke$' ./smoke`
for literal/parameter clock stability, relative civil dates and fresh chain/end
boundaries; `go test -tags reference -count=1 -v -run '^TestReferenceTimestampSQLTransactionChainClock$' ./reference`
asserts these relationships separately on each backend, not absolute clock parity.
Freezing belongs to each physical driver preparation, not to SQL text in a
pool-wide cache. Reuse the same Conn-prepared handle to retain a constant;
`Tx.StmtContext` of that handle performs a new physical preparation and captures
the new transaction's start. Native table DDL remains unsupported (`0A000`)
inside explicit transactions; timestamp INSERT errors retain normal abort
semantics.
`now` already supplies date, time and a zone field; `allballs` supplies time
and a zone field. An additional zone therefore conflicts in either token order
(`now UTC`, `UTC now`, or `1999-01-08 allballs UTC` are invalid).
Date-first `1999-01-08 allballs` is valid midnight; `allballs` alone lacks a date.
`epoch` and signed/unsigned infinity are decoded within the same whole-input
field loop: `on epoch`, `epoch at`, `on +InFiNiTy` and `- InFiNiTy at` are valid.
They reserve date, time and zone fields; conflicting fields in either order
are rejected rather than discarded after the first special token.
Core date/time words (including all special values) stay core tokens before
adjacent digits or plus signs; they are not reinterpreted as POSIX zone names.
For example, a completed date/time followed by `now1` or `now+2` is invalid,
while genuine short POSIX names such as `e2` retain their ignored-zone behavior.
This is context-sensitive: standalone `today+2` is a valid date and numeric
zone, with the same UTC civil midnight as `today`, not an adjacency blacklist.
Delimited and dotted-ordinal dates must complete the global date-field mask:
prior clock/`allballs`, weekday, AD/BC or AM/PM fields make that branch invalid;
only ordinal and timezone fields are excluded from its completion test.
For example, `Friday 1999-01-08` is invalid, but separately decoded text-month
fields in `Friday January 8 1999` remain valid. Partial numeric date context
can complete normally (`1999 01-08`). This is token-branch-specific, not a
blanket ban on every date/time field permutation.
The global date mask is shared, but textual-month interpretation is local to
each delimited field: `January 08-1999` and `1999-Jan-08` are valid, whereas
`January 1999-08` and `Jan 1999/08` overflow the day field with a DateStyle hint.
Outer numeric MONTH can become DAY when a textual month follows, provided no
DAY or prior textual month is present and the numeric field is 1–31. Any YEAR
is retained: `1999 8 January` is valid, but `99 January 1999` is a duplicate-month
syntax error without a DateStyle hint. This does not change delimited-field
local textual-month interpretation.
Delimited dates admit a single trailing separator and interior repetitions
(`1999-01-08-`, `1999//01//08`), but reject separator-only trailing runs
(`1999-01-08--`, `1999/01/08//`).
Numeric and alphabetic date components use the release's single-character
advancement (`1999-Jan08` decodes as January 8). Additional components are
decoded before completion is rejected, so an overflowing fourth integer is
a field-range error, not an early component-count syntax error.
The textual pass rejects unknown words before numeric overflow:
`2147483648-xxx-08` yields 22007 without a hint. Throwaway `at`/`on` skip only
that first pass and remain for numeric decoding: `2147483648-at-08` and
`2147483648-on-08` yield field-range 22008 without a hint, while ordinary
`1999-at-08` and `1999-on-08` are malformed. Tokens retain borrowed input
spelling; ASCII case-insensitive comparisons (including sorted zone lookup)
avoid allocating lowercase copies on successful mixed-case input, including
months, weekdays, era/meridian and Julian labels, noise words and special values.
Whitespace following a sign stays in the borrowed input span; numeric-zone and
signed-infinity consumers trim it locally without concatenating a token copy.
Token budgets count the sign, non-whitespace tail and terminator, not the gap.
All ASCII whitespace forms retain numeric-zone bounds (`- 16` fails `22009`).
The 25-field limit is checked before discarding subsequent punctuation; trailing
whitespace is harmless, but punctuation after field 25 is malformed. ASCII DEL
is not a punctuation delimiter and is rejected with the entire original input.
Day-relative inputs still allow an ordinary clock and one zone.
Finite output/ScanType is `time.Time` at zero offset, including BC/wide years;
infinity is owned bytes and NULL is nil without losing TIMESTAMP metadata.
`time.Time` parameters import their original Date/Clock/Nanosecond civil fields,
validating the numeric offset within ±15:59:59 before ignoring its effect;
there is no conversion of the instant to UTC. Offsets of ±16 hours fail with
22009, including when the civil year also exceeds the finite timestamp range.
A serialized date-year integer overflow precedes zone decoding instead (22008,
without a DateStyle hint). On failure, diagnostics include the original parameter
text, including unrounded nanoseconds, offset seconds and BC spelling. Formatting
is failure-only; successful civil imports do not allocate a timestamp string.

Literal diagnostics include the quoted input's rune position. Semantic precision
diagnostics use the type opener; malformed numeric modifiers use the offending
token's rune position. Numeric junk and incomplete exponents report `42601`
`trailing junk after numeric literal` with the complete token; `1..2` instead
reports syntax error at `".."`, positioned at its first dot. FLOAT and TIMESTAMP
modifiers reuse this scanner behavior; scalar decimal expressions are supported
through the shared exact NUMERIC literal path described above.
Parameter input diagnostics omit native Position/Context.
Malformed/date/time/range/zone errors publish no partial INSERT or Restore
replacement. Retained cursors keep original values, precision and origin metadata
across append/Reset/Restore; exported infinity buffers cannot mutate stored values.

No timestamp-with-time-zone type, DateStyle/TimeZone settings, temporal functions,
casts, no-FROM SELECT or broader expression syntax is enabled by this feature.
DMY/YMD setting-dependent interpretations are not native defaults.
Run `go test -count=1 -v -run '^TestTimestampSmoke$' ./smoke` for the feature smoke,
`go test -count=1 -v -run '^TestTimestamp.*$' .` for public acceptance, and
`go test -tags reference -count=1 -v -run '^TestReferenceTimestamp.*$' ./reference`
for independently expected real PostgreSQL 18.0 scenarios.
Codec corner cases and allocation assertions can be selected with
`go test -count=1 -v -run '^TestTimestamp.*$' ./internal/sqltype`;
`go test -count=1 -v -run '^TestTimestamp.*Allocations$' ./internal/sqltype`
includes signed-whitespace and revised numeric/prefix/DST branches.


Integer SQL constants accept decimal and case-insensitive binary `0b`, octal `0o`, or hexadecimal `0x` prefixes. Single underscores may separate digits; radix constants may also have one underscore immediately after the prefix (`0B_1_010`, `0O_1_2`, `0X_A` all equal 10). Decimal leading zeros remain decimal (`010` is 10, `08` is 8). Unary signs may be separated from the number by whitespace/comments. Signed values infer INT4, then INT8, then exact NUMERIC beyond signed int64; `-0x8000_0000_0000_0000` still infers INT8. Integer destinations independently check their assignment range. Malformed digits, separators and trailing identifier junk fail the entire statement with PostgreSQL English `42601` diagnostics pointing to the number's first character (not its sign).

Fractional/exponent SQL constants infer exact NUMERIC, including integral forms such as `1e3` and `1e+3`. Bit-string constants remain unsupported (`0A000`). SQL integer constants and integer textual input have distinct lexical/input diagnostics, though both support radix/separator forms: a valid beyond-int8 SQL constant can be stored as NUMERIC, while assignment to BIGINT fails with `22003` / `bigint out of range`. Dump emits canonical decimal integers and quotes exact numeric output; Restore accepts the supported spellings. Run `go test -count=1 -run '^TestNumericLiteralsSmoke$' ./smoke` for the public feature contract; Go test reports the result.

Lexically valid exponents succeed through NUMERIC inference, but `1e3foo`, `1e3_`, `1_000e3__0` and `1e+3foo` fail with `42601`, quoting the complete malformed numeric token. Lexical errors take precedence over destination conversion or range checks.

Radix and separated constants follow the same prepared assignment contract as decimal integers: `INSERT INTO t VALUES (1),(0x8000_0000)` into an INT4 column prepares successfully, then execution fails with `22003` / `integer out of range` and publishes neither row. The numeric feature smoke checks this boundary through both direct and prepared execution.

For an INSERT without a target list, uniform VALUES rows may supply any positive number of leading declaration-order columns up to the table width; omitted supported columns receive NULL (the supported DDL has no declared-default expressions). Every VALUES row must have the same width. Explicit target lists retain their stated order and must match row width. Conversion, arity and NOT NULL failures publish no rows from the statement. The public feature smoke is `go test -count=1 -run '^TestInsertImplicitSmoke$' ./smoke`.

Preparation checks assignment compatibility; typed integer literals assigned to INT4 are range-checked during execution, before NOT NULL checks. Thus preparing a short INSERT containing `2147483648` succeeds, but executing it fails with `22003` (`integer out of range`) without publishing rows. Unknown quoted numeric strings still run the destination input function during preparation and retain their input-specific diagnostic and cursor position.

That feature smoke exercises both direct execution and an actual prepared short INSERT, checks declaration-order metadata and statement atomicity, and retains a cursor across Reset/Restore. It also checks deterministic full-row Dump/Restore, successful hand-authored short-row Restore, and catalog preservation when a staged short INSERT omits a NOT NULL tail. Go test reports whether those public API assertions pass.

### Schemas and search path

`CREATE SCHEMA name [AUTHORIZATION role]`, authorization-only name inference,
`IF NOT EXISTS`, `ALTER SCHEMA name RENAME TO name`, `ALTER SCHEMA name OWNER TO role`,
and comma-list `DROP SCHEMA [IF EXISTS] ... [RESTRICT|CASCADE]` are supported.
CREATE SCHEMA can contain contiguous CREATE TABLE elements (no intervening
semicolons). All elements publish atomically and belong to the designated
schema owner. Other nested commands are not supported. Rename preserves
identities and storage; RESTRICT leaves dependents intact and CASCADE removes
the selected namespaces and their contained supported tables.
Conditional schema notices retain PostgreSQL diagnostics: CREATE IF NOT EXISTS
uses NOTICE 42P06; DROP IF EXISTS for a missing schema uses NOTICE 00000.
CREATE TABLE destination errors retain the target's one-based AST character
position, including an empty effective path and schema CREATE denial.


Qualified CREATE TABLE, SELECT and INSERT keep quoted name components distinct.
`search_path` is a physical-session setting, default `"$user", public`, shared
by SET/SHOW/RESET/current_setting/set_config. Missing and inaccessible entries
are skipped, decoded `$user` expands current_user, duplicate namespaces are
ignored, and pg_catalog is implicit first unless explicitly positioned.
`SELECT current_schema` and `SELECT current_schema()` return one NAME value,
NULL when no usable explicit namespace exists. Empty paths still permit
qualified references but unqualified creation fails 3F000. SQL SET renders each
string list element as a minimally quoted identifier: `SET search_path TO 'sp1,'`
stores `"sp1,"`, a single schema name, and `SET search_path TO ''` stores `""`,
an unusable empty name. Raw `set_config('search_path','',false)` instead stores
an empty path. Malformed raw identifier lists, such as `set_config` input `sp1,`,
fail 22023 without changing the previous value. Renaming a schema does not
rewrite stored search_path text.

New schemas have no PUBLIC rights. Owners have schema USAGE/CREATE; relation
SELECT/INSERT is independently checked. Qualified creation requires CREATE,
not USAGE. Unqualified creation chooses the first usable explicit namespace,
then checks CREATE without fallback. Database CREATE grants are not implemented:
standalone schema provisioning and rename require effective superuser authority.
The fixed native error label `stuntgres` in `permission denied for database
stuntgres` is diagnostic-only, not an attached database or DSN interpretation.
Native public retains the existing PUBLIC USAGE|CREATE convenience divergence.
Owner transfer updates the schema owner and its explicit ACL, not contained table ownership.
Pinned PostgreSQL 18.0 permits superuser transfer to ordinary NOLOGIN roles
without database CREATE and same-owner no-ops; this observed behavior takes
precedence over the ALTER SCHEMA manual's destination-CREATE wording.

Schema owner transfer also substitutes explicit ACL grantor/grantee ownership
references and merges colliding grants atomically, without changing retained
snapshots, unrelated grants or absent versus explicit-empty ACL semantics.
Held INSERT rebinding from INT4 to TEXT is an explicit bounded difference:
native rejects `0A000 cached plan must not change parameter type` before
parameter import/write; pinned PostgreSQL retains the INT4 parameter and
assignment-coerces the value into TEXT (held Exec9 succeeds with text `9`).

Prepared handles retain ASTs and frozen result/parameter signatures. Compatible
path changes re-resolve; incompatible rebinding fails 0A000 before importing
parameters or publishing writes. This is not PostgreSQL's complete cached-plan
invalidation model: unchanged-path shadow creation and cached namespace-USAGE
caveats are not reproduced. Retained Rows keep original values and metadata
through rename/drop/Reset/Restore. Explicit transactions reject schema utilities
before effects. Absent pg_temp references do not allocate; a pending temporary
creation destination fails 0A000 rather than falling through to public.

Within a native SELECT/INSERT or semantic Prepare, current authorized name
resolution must equal the exact relation whose AS/RX lock was acquired. If an
earlier search-path schema gains a same-shaped shadow while acquisition waits,
native rejects with the ordinary `stuntgres: catalog replaced before publication`
conflict before returning metadata, importing parameters, reading or writing.
The caller may submit a new statement; the engine does not retry SQL. This is an
explicit difference from observed pinned PostgreSQL: direct SELECT/INSERT and
semantic SELECT Prepare reacquire the new shadow's lock; a warmed held SELECT
continues using the originally locked relation. Native held handles also reject
the within-statement mismatch; frozen signatures alone do not authorize an
unlocked relation. No PostgreSQL outcome is asserted here for held INSERT or
semantic INSERT Prepare in this shadow-creation sequence.

Dump writes every persistent user schema, including empty schemas, before
owner-grouped qualified tables and rows. Restore preserves independent schema
and table owners through private stage-only CREATE TABLE destination authority.
Private Restore INSERT uses captured bootstrap authority without stamping owners
or changing named owner directives/session identity; ordinary SQL INSERT retains
normal USAGE/table ACL checks. No ACL grants are synthesized or retained.
Unsupported explicit namespace,
table or column ACLs fail Dump before any output. Invalid Restore leaves the
target catalog untouched. Bootstrap-only public dump bytes remain unchanged.
Feature witnesses: `go test -count=1 -run '^TestSchemaSmoke$' ./smoke` and
`go test -tags reference -count=1 -run '^TestReferenceSchema' ./reference`.


Three-part/database qualification, temporary-object creation, SELECT aliases/qualified columns, joins, generic expressions/functions, nonbaseline explicit casts, constraints beyond nullability, defaults, serial, primary keys, UPDATE, DELETE USING/CURRENT OF/RETURNING, multiple sort keys, SQL transaction mode clauses, savepoints, two-phase commit, transactional DDL/COMMENT/role mutation, stronger isolation, SQL ACL mutation, or SQL catalog projections are not supported. The default-mode SQL transaction controls described above, including chain syntax and transactional settings, are supported. Recognized unsupported features fail with SQLSTATE `0A000`; malformed SQL fails with `42601`. Unsupported suffixes and multiple statements fail before any prefix executes.

SQL errors support `errors.As` into `interface { SQLState() string }`; exported `stuntgres.Error` preserves PostgreSQL severity, nonlocalized severity, five-character Code, primary Message, Detail, Hint, one-based character Position, InternalQuery/InternalPosition, Context, and independently optional Schema, Table, Column, DataType and Constraint fields. Optional empty strings are absent; InternalPosition is supplied only with InternalQuery. File, Line and Routine, if present, are implementation-specific locations and never PostgreSQL C locations. `Error()` remains a presentation string, not a source for SQLSTATE or severity. The type is not `*pq.Error`; concrete lib/pq assertions are not compatible. Context, I/O, closed-object, argument-count and foreign-driver errors are ordinary Go errors. Cancellation cannot forcibly interrupt arbitrary blocking reader/writer calls.

Native SQL failures supply both severity fields as `ERROR`, including parser, missing-object and duplicate-object failures; naming an object in the primary message does not imply an object diagnostic field. The error-fields feature smoke is `go test -count=1 -run '^TestErrorFieldsSmoke$' ./smoke`: it checks exact syntax and engine diagnostics, direct/prepared NOT NULL failures, statement atomicity, wrapping, ordinary context cancellation and native control round trips. Go test reports the result. Reference-only permission, procedural and SQL cancellation witnesses exercise PostgreSQL diagnostic transport expectations, not native implementations of those unsupported features.

Shared generic SQL diagnostics construct only severity, SQLSTATE and primary message; they never infer object identities. In particular, input/range conversion errors (`22P02`/`22003`) do not infer Schema, Table or Column from a destination or WHERE predicate. Condition-specific NOT NULL producers (`23502`) supply schema/table/column: INSERT includes failing-row Detail, while SET/ADD existing-row validation has no Detail. Even a named constraint leaves the Constraint field empty. This distinction follows PostgreSQL's [conditional error fields](https://www.postgresql.org/docs/18/protocol-error-fields.html); clients must not infer the presence of one object field from another.

Invalid boolean strings retain the original decoded input in the PostgreSQL `22P02` primary message (for example, `invalid input syntax for type boolean: "o"`). Boolean WHERE string-literal failures now carry the one-based character Position of the source opener, as INSERT already does, including during actual preparation and on empty tables. Escapes and continued strings do not change that provenance. Bound parameters have no query Position or native portal Context; real PostgreSQL Bind errors independently retain lib/pq's parameter Context. No destination object identities are inferred.

This bounded diagnostic change does not replace boolean parsing or native `database/sql` values: accepted aliases/unique prefixes, case/ASCII whitespace, TRUE/FALSE/NULL, bool/nil returns, and atomic INSERT and Dump/Reset/Restore behavior remain baseline regressions. Run `go test -count=1 -run '^TestBooleanDiagnosticsSmoke$' ./smoke` for complete native diagnostic, direct/prepared parameter, atomicity and control witnesses; Go test reports the result. The independent pinned-reference fixture is `go test -tags reference -run '^TestReferenceBooleanDiagnostics$' -count=1 -v ./reference`.

Unterminated comments and quoted tokens report PostgreSQL-style English primary messages containing the raw input from the offending opener (`at or near "..."`), with a separate one-based character Position. Newline-separated string segments form one token, so an unterminated later segment points to the first literal opener and includes the continued raw suffix. A zero-length delimited identifier instead includes only its raw `""` token, not trailing SQL. Diagnostic text is not escaped as a Go string; multibyte characters do not turn Position into a byte offset.

INSERT target-resolution and assignment diagnostics interpolate decoded identifiers between literal double quotes, matching PostgreSQL diagnostic prose rather than Go escaping or SQL identifier quoting. The feature smoke includes an embedded-quote column mismatch and checks its exact diagnostic and complete sentinel preservation.

Decoded E-string NUL/invalid UTF8 reports `22021` with no Position; malformed Unicode reports `22025` with a format Hint, while invalid Unicode values/surrogate pairs and unterminated literals report `42601` with original-source character positions. Raw SQL NUL/invalid UTF8 intentionally retains native positioned `42601` preflight diagnostics, distinct from decoded bytes. Bound text is not SQL escape source and retains existing type-input behavior. B/X/N strings remain unsupported; `standard_conforming_strings` is configurable as documented above. Dumps use ordinary strings, preserving decoded controls and literal backslashes.

The focused public proof is `go test -count=1 -run '^TestStringLiteralsSmoke$' ./smoke`; it asserts direct/prepared escape and dollar values, parameter separation, exact diagnostics, statement atomicity, shared-pool Dump/Reset/Restore, retained cursors and the raw SQL boundary. Go test reports the result.

Pinned real-reference feature fixtures are `go test -tags reference -run '^TestReferenceStringLiterals$' -count=1 -v ./reference` and `go test -tags reference -run '^TestReferenceStringLiteralDumpRoundTrip$' -count=1 -v ./reference`.

## Native fixture controls

```go
func Reset(ctx context.Context, db *sql.DB) error
func Dump(ctx context.Context, db *sql.DB, dst io.Writer) error
func Restore(ctx context.Context, db *sql.DB, src io.Reader) error
```

Controls require a **native stuntgres** pool; foreign and middleware-wrapped pools are rejected, with no reflection unwrapping. They are **host-authorized**, independently of a borrowed connection's effective principal. Reset atomically replaces the catalog lifetime with initialized built-in `pg_catalog` and `public` namespace descriptors while retaining database identity, cluster roles, nonreuse counters, live identities/settings and existing pools. Dump captures before external I/O, groups deterministically by owner name then schema/relation, and writes quoted owner-switch directives plus PostgreSQL-compatible CREATE/INSERT SQL, declaration-ordered columns and canonical literals. Bootstrap-only dumps retain legacy CREATE/INSERT bytes without a redundant switch. Explicit relation/column ACLs cannot be serialized and cause a failure before output, rather than silent loss. Dump excludes role creation, passwords, membership and settings; it is not complete `pg_dump` output, COPY, or catalog export.

Dump reconstructs renamed, absent and recreated bootstrap namespaces using ordinary
DROP/ALTER SCHEMA SQL before user schemas and tables. Retained builtin ACLs must
equal their canonical bootstrap rights with both grantor and owner-grantee
transformed to the current owner; arbitrary explicit ACLs remain unsupported and
are rejected before output. Recreated public is an ordinary namespace with absent
ACL, never silently granted PUBLIC rights. Unchanged bootstrap streams retain
their exact legacy bytes.

Restore accepts supported CREATE/ALTER/DROP SCHEMA, CREATE TABLE, supported
parameter-free scalar INSERT (including integer arithmetic), database-local
COMMENT and named SET SESSION AUTHORIZATION owner directives. Parameters are
recursively rejected, and other SQL remains excluded. Legacy CREATE/INSERT-only
scripts remain bootstrap-owned. Referenced
roles must already exist in the target engine. Restore captures a private role
snapshot before input I/O, starts an initialized private catalog, executes through
the same SQL engine, remaps owners into target principal authority, then atomically
**replaces** descriptors, storage and epoch, never merges or resets roles/live
sessions. Missing or dropped/recreated owners and read/parse/execution/cancellation
errors leave original data untouched. Empty input clears user relations. No target
locks span external I/O. Private operation-only CREATE destination, INSERT and
database-local COMMENT bootstrap authority preserve independent schema/table owners
without grants or session identity changes. COMMENT's private marker covers lookup,
lock resolution and mutation, is cleared after each statement, and never permits
ROLE replay. Ordinary post-Restore SQL still enforces USAGE/CREATE and ownership.
The shared object allocator skips live namespace and relation identities imported
by Restore, so subsequent schema and table creation continues without collisions.
Replacement does not rewind the destination's nonreuse counter.

`go test -count=1 -run '^TestCatalogOwnersPublicWitness$' ./smoke` additionally checks two opaque DSNs with two simultaneously open pools and held physical connections per instance. It verifies qualified/unqualified INT4 results, same-instance sharing, independent-instance isolation, another instance's Reset/Restore leaving a held cursor unchanged, same-Driver reconnect retention and independent-Driver isolation. Go test reports the result.

## Catalog-part agent workflow

OMP discovers `/implement-part <part-symbol>` from `.omp/commands`, with coordinator/review skills and dedicated scout, planner, implementer and reviewer agents. `part-planner` uses configured `@slow` in the same OMP process; it can exchange questions with the coordinator and scouts, and spawn follow-up scouts as evidence gaps arise. This is workflow infrastructure, not an implementation of the future SQL catalog. It reads existing ignored `.cswd/spec_parts.json` and `.cswd/spec_status.json`; only a selected part with completed prerequisites may start.

Invoke from a clean named integration branch. Initial research and an accepted `.cswd/parts/<symbol>.md` plan precede an owned Git worktree. The coordinator persists supplementary research and ingests the planner's four-field JSON result through the lifecycle helper; planning agents do not implement or manage lifecycle. No external planning process, native auto-approval or guard extension is needed.

The helper runs generation, all unit/race/reference-tagged tests, public smoke, standard-library dependency checks and planned feature smoke. Exact-tree review uses configured `@csw-review-2`; medium/high/critical findings require fixes and fresh verification/review. Model mappings remain unchanged.

Only verified, approved content is committed and fast-forwarded to the latest integration tip. Rebase/conflict resolutions require full reverification. Status becomes `done` only after ancestry proof; failures retain journals/evidence under `.cswd/tmp/<symbol>/runs/<uuid>` and owned worktrees under `.cswd/work/<symbol>/<uuid>`. Use helper `resume` for interrupted runs; never manually reset/stash the integration checkout. The coordinator skill documents helper actions, evidence schemas and recovery rules.

Infrastructure regression proof uses disposable Git repositories and a tiny Go fixture, not real catalog parts or PostgreSQL coverage:

```sh
python3 -m unittest discover -s .omp/bin -p 'test_*.py' -v
python3 .omp/bin/implement_part.py select --repo "$PWD" PG18-ARCH-CATALOG-IDENTITY
```

`select` is read-only. A real `begin` refuses uncommitted user changes, including an edited `.gitignore`. No real part is executed during infrastructure installation.

## Development and verification

```sh
make generate   # pinned goyacc; checked-in parser generation
make build      # compile production packages
make test       # uncached Docker-free tests, including smoke
make reference  # real pinned PostgreSQL differential fixtures
make smoke      # uncached public database/sql and controls smoke tests
make race       # Docker-free tests with the race detector
make test-all   # native, smoke and reference tests with race detection in one run
make bench      # fresh engine + connector + pool + Ping + Close, five runs
make deps       # list non-standard production dependencies
```

Test targets enforce a fixed 60-second wall-clock deadline across all packages,
including compilation, plus a per-package timeout. `make test-all` runs all suites
together within that deadline; slow or hanging tests must be fixed, not exempted.
Output is quiet by default; use `VERBOSE=1` for test diagnostics, `RUN='^TestName$'`
for selection, and `PACKAGES=./internal/parser` to scope ordinary/race tests.
Read `Makefile` for non-standard actions. Only specialized, explicitly enabled
benchmarks may exceed the test budget; the default startup benchmark is bounded too.

Every nonblank production dependency entry must be `stuntgres` or start with `stuntgres/`. goyacc is a generation tool, not a runtime dependency. lib/pq, sqlmw and testcontainers are test-only. Benchmark startup target is below 1,000,000 ns/op; report observed allocations and timings, not a timing-sensitive test assertion.

The public smoke tests live in `smoke/*_test.go` and are also part of `make test`.
`make smoke` runs the entire package, uncached and quiet unless `VERBOSE=1`.
The baseline `TestSmoke` verifies rows, metadata, affected counts,
Dump/Reset/Restore and a second already-open pool. Run the catalog witness with
`make smoke RUN='^TestCatalogOwnersPublicWitness$'`.
Go test reports each test's pass or failure instead of standalone output labels.

The explicitly enabled reference suite requires working Docker and image/module access. It uses one PostgreSQL 18.0 container pinned to `postgres:18.0-bookworm@sha256:3f55f8895c4ed50603e2fbdfc72fffeeaba3173321fee5cb825bbbeb30d9d854`, UTF8/C locale, serialized fixtures and public-schema resets. sqlmw records both direct and prepared paths; native memory handles remain available for controls. Fixtures assert independently specified outcomes in addition to parity. Reference startup failures are errors, not silent skips or mock fallbacks.

Reference tests live in the dedicated `reference/` external test package and use only the public stuntgres API. Shared setup and helpers belong in `reference/fixture_test.go`; functional groups belong in separate test files of at most 750 lines each. `make reference` runs these fixtures uncached, with `VERBOSE=1` available for diagnostics.
The cohesive common fixture is exempt from the 750-line limit; it keeps container lifecycle, diagnostic assertions, notice connections and middleware recording together.

Implicit INSERT diagnostic fixtures independently specify preparation versus execution failure stages, not just backend agreement. Native engine tests also use a test-owned context to deterministically cancel after row staging, during constraint checks, and immediately before publication, checking complete sentinel preservation and subsequent engine usability without sleeps or production hooks.

Authoritative references: [PostgreSQL 18 commands](https://www.postgresql.org/docs/18/sql-commands.html), [SQLSTATE codes](https://www.postgresql.org/docs/18/errcodes-appendix.html), [testcontainers PostgreSQL API](https://golang.testcontainers.org/modules/postgres/), and [pinned sqlmw APIs](https://github.com/ngrok/sqlmw/blob/97c9c04efc79/interceptor.go). Local PostgreSQL source is 20devel and useful for implementation research; the pinned PostgreSQL 18.0 runtime is the compatibility oracle.
