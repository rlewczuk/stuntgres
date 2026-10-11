# Driver and connection lifecycle

[README](../README.md) · [SQL dialect and limits](DIALECT.md) · [SQL reference](SQL.md)

## Pinned COPY text streams — change note

`stuntgres.CopyFrom(ctx, conn, sql, reader)` and
`stuntgres.CopyTo(ctx, conn, sql, writer)` use a pinned native `*sql.Conn`.
Both return `CopyResult{Count, CommandTag}`: successful operations report the
complete row count and `COPY n`; every failure returns a zero result. A nil
connection/stream, foreign or wrapped driver, non-COPY/multiple statements,
direction mismatch and precanceled context fail without reading or writing.
Streams remain caller-owned and are never closed.

Use table `COPY ... FROM STDIN`, table `COPY ... TO STDOUT`, or
`COPY (query) TO STDOUT`. Ordinary Exec/Query/Prepare cannot supply a stream and
report native0A000 `COPY FROM STDIN requires stuntgres.CopyFrom` or
`COPY TO STDOUT requires stuntgres.CopyTo`; file and PROGRAM endpoints instead
report their explicit capability boundary. No filesystem or process
access is performed. SQL arguments are not interpolated into a second statement.

Text and modern FORMAT csv support ordered columns, DELIMITER, NULL, DEFAULT
(FROM only) and HEADER Boolean/MATCH. CSV additionally supports QUOTE/ESCAPE,
TO FORCE_QUOTE and FROM FORCE_NULL/FORCE_NOT_NULL with named columns or `*`.
Defaults and identities, generated columns, real type input/output, constraints,
partition routing, ACLs and publication use existing native authorities.
See [the bounded dialect](DIALECT.md#copy-text-streams--change-note).

COPY is one admitted command on the physical session. Explicit transactions must
stay pinned; malformed SQL and SQL COPY errors preserve ordinary transaction
abort semantics. Native caller I/O errors retain their error identity rather
than being invented SQL errors. COPY FROM stages the complete statement privately;
a late decode, reader, constraint, cancellation or publication error cannot publish
a prefix. COPY TO may have written a prefix on failure but returns no success
count/tag. Short writes report `io.ErrShortWrite`.

The physical transaction owner is reserved while caller I/O runs, but no engine,
session, transaction-owner, admission or replacement mutex is held inside Read
or Write. Same-owner commands wait for this reservation with their own cancellation;
peers and native replacement controls can progress. Reset/Restore never cause an
in-flight stream to rebind replacement objects. Session termination cancels the
operation while waiting for the caller to release a blocked I/O call; the driver
does not close caller streams or launch an unjoinable read/write goroutine.
Read and Write callbacks must not execute SQL or otherwise reenter the same
pinned `*sql.Conn`: `database/sql` Raw retains that connection's ownership for
the entire transfer even while engine mutexes are released. Use independent peer
connections for concurrent SQL and controls.

CSV example: `COPY t FROM STDIN WITH(FORMAT csv,HEADER MATCH)` imports a
CSV header followed by rows; `COPY (SELECT * FROM t ORDER BY id) TO STDOUT
WITH(FORMAT csv,HEADER true,FORCE_QUOTE(value))` exports deterministic rows.
An unquoted empty field is NULL by default; `""` is an empty string.
Feature smoke: `make check RUN='^TestReferenceCopyCSVFeatureSmoke$'`;
pinned counterpart:
`make check REFERENCE=1 RUN='^TestReferenceCopyCSVFeatureSmoke$'`.


## Explicit DISCARD and pool reuse

Pin `*sql.Conn` for owner cleanup. Execute standalone `DISCARD ALL` **before**
`Conn.Close` returns it to the pool. Reborrowing that same physical connection
then sees captured setting defaults, no SQL prepared names/TEMP objects/sequence
memories and released session advisory holds. Peers and durable data are unchanged.
Without explicit cleanup, healthy reuse intentionally preserves these resources:
`ResetSession` is still a health check, never automatic DISCARD or rollback.

SQL PREPARE names and ordinary Go statements have distinct lifetimes. PLANS retains
both and lazily reanalyzes private literal captures without changing fixed
parameter/result signatures or catalog fences. ALL removes SQL names (subsequent
EXECUTE gives26000); native Go AST handles remain callable for valid persistent
sources. lib/pq server-backed handles may instead fail26000 after server ALL.
Retained anonymous Rows and executing subset cursors are not rewound or disposed.

## Direct SQL messages — change note

Parameterless direct `ExecContext` and `QueryContext` accept multiple statements
in one SQL string, including semicolons inside quoted text and comments. Empty
segments are ignored. The entire input is parsed before execution: malformed
suffix syntax cannot leave prefix effects. Syntactically valid multiple commands
with direct arguments, Go `Prepare`, or SQL `PREPARE` return ERROR `42601`,
`cannot insert multiple commands into a prepared statement`, without executing a
prefix. Prepared handles retain their existing one-command typed parameter frame.
Native empty/comment-only calls retain their zero-count/empty-row behavior; this
change does not make the lib/pq empty `Exec` route return a native-style count.

One operation owns message admission and statement time. Every command executes
eagerly, advances its command identity, and captures current settings, identity,
parameters and catalog/data authority. The first failure stops execution. No SQL,
notice delivery, or transaction publication is deferred to `Next` or
`NextResultSet`; notices detach after the message and callbacks run outside locks.
See [transaction boundaries](TRANSACTIONS.md#retained-direct-message-transactions).

Transaction controls first in a retained message, or after COMMIT/ROLLBACK,
use a fresh ordinary implicit segment. COMMIT/ROLLBACK PREPARED therefore
reject with25001 before GID lookup; genuine singleton completion is unchanged.
Initial retained PREPARE TRANSACTION warns25P01 and detaches the empty segment,
while its suffix runs independently. Singleton idle PREPARE still creates no entry.

Nonconcurrent materialized refresh retains its full private replacement, including
DATA/NO DATA population, across source writes, private CREATE, repeated refresh and
pending role/MAINTAIN grants. BEGIN promotes already-refreshed roots without
publication; late errors discard them. Supported source indexes remain atomic with
source rows. This does not enable MV indexes, concurrent refresh, explicit-block
refresh or REFRESH as native Restore input.

`Exec` closes all eager row results and reports the final successful command's
count: SELECT reports its row count, and command-only utilities report zero.
An error wins over cleanup errors and makes the public result unavailable.
`Query` retains every successfully completed row-producing command, including
empty sets and zero-column sets.
Commands without a row result do not add a set. Each set exposes its own
column names, database types, lengths, precision/scale and nullability; previously
returned metadata does not mutate when advancing.

Native `driver.RowsNextResultSet` may skip unread rows of the current set.
The pinned lib/pq route instead needs `Next` to discover the following result
description; the native early-skip contract is deliberately not replaced by that
adapter limitation. A failed command contributes no partial set. Completed prefix
sets remain available before the original terminal execution error; normal
result-set boundaries are EOF. Final exhaustion, early close and repeated close
preserve the primary error and release each owned row result once. Closing early
does not suppress an error from a later command that already executed.
Caller cancellation during iteration remains sticky in `Rows.Err`; explicit
driver-row Close releases resources without newly returning that context error.
An already-executed terminal batch SQL error is still returned by Close.

Cancellation/deadlines use the original caller context and current per-command
timeout settings, not the prior command's expiring timer. Native cancellation
remains an ordinary context error: an implicit segment rolls back and the
connection is reusable; an explicit block becomes Aborted until rollback.
The pinned PostgreSQL cancellation witness independently observes `57014` and
lib/pq disposal of that physical connection rather than claiming native fatality.
Already completed detached outputs retain their ownership after SQL rollback or
catalog replacement.

Role-name visibility is not physical command admission. After PREPARE detaches a
newly created/selected role, a subsequent demanded current_user name reports the
dynamic `42704` invalid-role-OID diagnostic. session_user and ordinary permission
checks remain independently usable; private prepared names/ACLs do not become
fallback session authority. Separate prepared COMMIT restores name/permission
visibility, or SET ROLE NONE recovers the original session role after rollback.


## Database names and lifetime

The DSN is an **exact opaque string**, not a libpq connection string or network address. Identical strings (including the empty string) within the same Driver share an engine; different strings or Driver instances are isolated. The registered `stuntgres` driver uses one default Driver. Engines live as long as their Driver, even after every connection or pool closes. Use `NewDriver().OpenConnector(name)` with `sql.OpenDB` for independently owned fixtures. Nothing is shared between processes or persisted across process exit.

Random functions share one generator per physical connection. Pin a `sql.Conn`
when calling `setseed` and reading a repeatable series: interchangeable pool
queries need not use the same session. Returning a borrower to the pool preserves
the stream; closing/replacing the physical session discards it without losing
database tables. Draws and successful reseeding are nontransactional, including
savepoint rollback, and survive native catalog Reset/Restore. Prepared calls
describe types without drawing; each execution uses its current physical session.
No generator state is serialized into Dump. Feature smoke:
`make check RUN='^TestRandomSmoke$$'`.

## Trusted role admission and physical limits

`OpenConnectorAs(dsn, role)` asserts an exact trusted native principal; it does
not parse credentials or perform a password/SCRAM handshake. Missing or miscased
role names retain ordinary host errors without SQLSTATE. An existing NOLOGIN
principal instead rejects new physical connections with FATAL, unlocalized FATAL,
SQLSTATE28000, `role "name" is not permitted to log in`. Superusers are not exempt
from LOGIN. Already admitted connections remain usable after NOLOGIN.

CONNECTION LIMIT counts physical sessions against the immutable original
authenticated role, not current_user or session_user after SET ROLE/SESSION
AUTHORIZATION. Limit -1 is unlimited; 0 denies ordinary admission; positive limits
bound it. Superusers bypass limits. Denial is FATAL/unlocalized FATAL53300,
`too many connections for role "name"`; these admission diagnostics have no other
detail/hint/position fields. Lowering a limit does not terminate incumbents.
An idle pooled connection still counts. Returning `sql.Conn` to its pool does
not release a slot, and borrowing/reusing it does not consume another.
Raw physical `driver.Conn.Close`, or actual pool disposal, releases the slot once.
Failed initialization consumes none; final admission rechecks current attributes.

PASSWORD and VALID UNTIL are meaningful stored SQL metadata but trusted native
admission ignores both, including NULL passwords and expired timestamps.
PostgreSQL password-authenticated fixture connections are an independent route,
not evidence of a native password handshake. Native Reset/Restore retain roles,
attributes, membership and live admission accounting; Dump emits no role/password
DDL. Role utilities remain excluded from native explicit transactions.
Observed public smoke exercised creator ADMIN, omitted ALTER USER retention,
expired trusted admission, physical limit release, NOLOGIN and held authority
refresh. Its repeatable reference selector is
`RUN='^TestReferenceRoleAttributesFeatureSmoke$'` in both check modes.

## SQL cursors — change note

`DECLARE name [NO SCROLL|SCROLL] CURSOR [WITHOUT HOLD|WITH HOLD] FOR query`
belongs to one physical session, so use a pinned `sql.Conn` or `sql.Tx`.
The default and WITHOUT HOLD require a transaction block: an explicit block or a
retained implicit direct message. WITH HOLD also works in autocommit. DECLARE
analyzes the complete query and captures its data snapshot and owned parameters
without producing target values.
FETCH and MOVE accept NEXT/PRIOR/FIRST/LAST, FORWARD/BACKWARD signed count or ALL,
ABSOLUTE signed count and RELATIVE signed count
(cursor counts have an int32 lexical magnitude limit).
Explicit SCROLL retains execution across EOF and permits reverse/seek motion.
MOVE returns no schema and reports corresponding FETCH cardinality; finite
discarded traversal still evaluates targets. Rewind rescans node-specific
volatile WHERE/VALUES demand but preserves ORDER caches. Volatile targets may
redraw, including transparent derived/view routes; no universal redraw or
freezing promise applies. Omitted mode remains conservatively forward-only;
NO SCROLL rejects actual backward demand/rewind, with pristine/zero MOVE exceptions.
Top-level SCROLL locking clauses are rejected during analysis; nested row marks
remain legal. Each successful FETCH publishes a fully eager, detached batch with
the live cursor's complete schema, including
empty/exhausted and zero-column results. Ordinary SELECT remains fully eager.
Blocking sorts, aggregates, joins and views may demand input before a batch;
there is no universal one-expression-draw-per-returned-row promise.
Retained derived/filter/limit adapters keep their child's next uncaptured demand
position when rewind preserves cached occurrences. Re-reading the cached prefix
does not append it again; actual child invalidation still resets generated demand.

The first effective demand (FETCH, MOVE or creating commit's HOLD materialization)
captures selected stable identity and statement-time expression data; before-first
FETCH0 does not start demand. Live FETCH/MOVE and commit demand use fresh
operation/context, current cancellation/notice/lock policy, original transaction
authority and sequence acquisition. DECLARE's source snapshot is never rebound
or reopened. Already returned batches remain immutable after SQL CLOSE,
transaction/session end or native catalog replacement.

The final successful live-cursor utility/FETCH publication preflight is its
completion boundary: later cancellation cannot retract a delivered FETCH batch,
explicit-transaction DECLARE or completed CLOSE/CLOSE ALL in statement Finish.
Autocommit HOLD installation alone is not completion: draining and canonical
transaction publication must finish before DECLARE returns success. Demand or
prepublication errors expose no partial batch and leave a live producer failed,
not rewound. Finish advances the command and releases statement admission/resources.
Ordinary statements and implicit HOLD retain required row/catalog publication.

Runtime REGTYPE casts route parser notices and modifier warnings through the
current demand operation (FETCH, SQL/Go COMMIT, or autocommit DECLARE) and its
captured `client_min_messages` policy, never a finished DECLARE operation.
Previously demanded batches are not replayed or re-warned. A TEXT `timestamp(7)` cast emits WARNING/22023
`TIMESTAMP(7) precision reduced to maximum allowed, 6` when that policy permits it.
Quiet description, non-demanding explicit DECLARE and before-first FETCH0 emit no runtime warning;
bounded FETCH does not project an extra row's warning, and EOF emits none.

`CLOSE name` and `CLOSE ALL` dispose named execution. COMMIT, ROLLBACK, CHAIN,
full abort, PREPARE TRANSACTION detach and physical session end dispose nonholdable
cursors. Unexported HOLD cursors are also removed by creating rollback/abort.
ROLLBACK TO removes declarations after its mark but does not rewind older cursor
motion, undo CLOSE or heal an executing failure. Recovered failed cursors report
55000 `portal "name" cannot be run`; missing names give34000.

Creating SQL/Go COMMIT (including CHAIN) drains each healthy NO SCROLL HOLD
producer's unfetched remainder before ON COMMIT actions, write publication and
transaction cleanup. Explicit SCROLL HOLD instead rewinds the same retained tree,
materializes its entire typed result and restores the saved ordinal and endpoints,
even after live EOF. This commit demand can redraw volatile targets; subsequent
stored FETCH/MOVE only reads captured values through the shared motion owner.
Export preserves typed values, cardinality, schema and original epoch, not a query
producer, source pin, lock, operation, context or transaction authority.
Promotion is per portal: an already exported portal survives a later portal's
drain failure or final serialization/publication failure even though private
transaction writes roll back. Subsequent transactions/rollback/PREPARE preserve
exported names and positions. Stored FETCH performs no target evaluation,
sequence draw or query lock acquisition. Current FETCH catalog, authorization and
search_path render typed REGCLASS/REGTYPE values recursively, including arrays,
domains and composites; rename uses the new name and DROP uses the datum's numeric
OID. Timezone likewise belongs to current FETCH presentation. Typed values/schema
own their codecs, not a retained DECLARE catalog; already delivered batches keep
their captured spellings after later catalog changes. CLOSE, DISCARD ALL and
physical session end release stores; returning a connection to its pool does not.

Cursor descriptors preserve column origin: catalog columns retain native known
nullability; computed targets (including NEXTVAL) retain unknown `(false,false)`
nullability. pq reports unknown nullability for both. Promotion does not synthesize
nullable descriptors or convert typed values to text.

Direct top-level FOR UPDATE/NO KEY UPDATE/SHARE/KEY SHARE is rejected after query
analysis with0A000 and Detail `Holdable cursors must be READ ONLY.` Nested and
stored-view row marks remain supported and release their locks at creating end.
A healthy unexported HOLD cursor created in the current transaction prohibits
PREPARE with0A000 `cannot PREPARE a transaction that has created a cursor WITH HOLD`;
closed, rolled-back-to, failed-after-recovery and previously exported entries do not.

`UPDATE/DELETE WHERE CURRENT OF name` borrows the most recently visited physical
occurrence of the live eligible cursor, including MOVE, not its projected values or a
Prepare-time incarnation. FETCH FORWARD 2 positions on its second returned row, and an exact
bounded last row stays positioned until an actual EOF probe. Positioned mutations
use ordinary DML publication, never the stage-free cursor completion exception;
cancellation/output failure exposes no partial writes or RETURNING batch.
Neither mutation moves/fails a healthy cursor. Repeat DELETE counts zero after
deletion; repeat UPDATE counts one and uses the latest old values, including
same-value assignments. Cursor lookup is live at Execute, not captured by Prepare.
RETURNING metadata comes from target/source analysis independently of FETCH schema.
After a failed FETCH and savepoint recovery, CURRENT OF uses the executor-lifetime
24000 diagnostic (`cursor "name" is held from a previous transaction`), also used
for exported HOLD cursors that no longer have a live physical source, distinct
from FETCH's55000 portal diagnostic. Missing names continue to give34000.
The native current occurrence retains no operation, context or projected-value
copy. Reset/Restore fences old identities rather than refreshing them from equal
restored tuples; detached FETCH/RETURNING results retain their own values/schema.

NO SCROLL effective backward demand and actual rewind give55000
`cursor can only scan forward` with the SCROLL hint. Negative BACKWARD is
effective forward; zero MOVE does not refetch and can succeed on-row.
FETCH0 on-row performs backward-discard then forward-refetch and requires SCROLL.

Preparing DECLARE analyzes parameters/dependencies but installs nothing;
preparing FETCH/MOVE/CLOSE performs no movement/name-error lookup. A native held
FETCH always returns the current entry's actual names/types/values, even after
same-name replacement or missing-at-Prepare declaration. Only FETCH is exempt
from ordinary immutable prepared SELECT-result signatures. lib/pq independently
retains its old wire description: absent descriptions can expose zero-width
occurrences, and same-width replacements can expose old aliases. Type-changing
replacement can fail in pq's stale binary decoder; native never emulates that
corruption. Closing Go Rows or Stmt does not close or rewind the named cursor.
Dump excludes names, positions and retained query execution; successful native
Reset/Restore fences the next admitted use rather than recapturing sources.

Feature smoke: `make check RUN='^TestReferenceSQLCursorMoveFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceSQLCursorMoveFeatureSmoke$'`.
HOLD smoke: `make check RUN='^TestReferenceSQLCursorHoldFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceSQLCursorHoldFeatureSmoke$'`.

## SQL prepared statements — change note

SQL `PREPARE`, `EXECUTE` and `DEALLOCATE` own named definitions in the physical
session. They are neither database/sql statement handles nor `PREPARE TRANSACTION`.
Hold a `sql.Conn` (or a `sql.Tx` on that owner) for a named lifecycle: an arbitrary
pool call may use another physical session. Names use normal identifier folding
and quoted spelling. Returning and reborrowing the same physical connection
retains definitions; physical close/replacement clears them without deleting
committed database data or a peer session's definitions.

`PrepareContext("PREPARE p(int) AS ...")` describes a utility without resolving
declarations or analyzing/installing its body. Its first Exec installs the SQL
name; a second valid Exec reports duplicate42P05. Preparing DEALLOCATE has no
effect until Exec. Preparing EXECUTE describes the current name's result without
evaluating actual expressions; an absent name can have an empty description,
then fail26000 on execution. Native EXECUTE returns its current underlying result
metadata, rather than emulating lib/pq's stale wire-description decoder behavior.
Go `Stmt.Close` does not deallocate a SQL name; detached eager Rows remain usable
after deallocation and physical session end.
The retained-row witness closes the public driver.Conn inside sql.Conn.Raw,
returning nil, then consumes the already eager sql.Rows. It does not force
database/sql's borrowed-connection close via ErrBadConn while those Rows still
own that wrapper's release; wrapper close/wait/cancellation is a separate API
lifetime from the detached engine result.

The wrapper has zero outer adapter parameters. `EXECUTE p($1)` does not bind Go
arguments to the SQL definition: supplied adapter arguments fail as ordinary Go
count errors; with no supplied arguments a nonzero-formal target reports42P02
at `$1`. A zero-formal target ignores SQL actual expressions instead.

Named definitions are session effects, not transactional rows: PREPARE survives
ROLLBACK/ROLLBACK TO, and those commands do not undo DEALLOCATE. EXECUTE runs the
underlying statement in the same admission, command clock, snapshot/lock and
Finish boundary; it inherits normal read-only restrictions, permissions,
cancellation, statement atomicity and explicit-transaction abort behavior.
Final session publication preflight protects installation/removal against late
cancellation, just like cursor session effects. Only successful session utilities
skip private-stage merging; normal EXECUTE writes still publish normally.

Native Dump omits names. Reset/Restore preserves live physical-session owners
and definitions, with existing catalog-generation/signature/reanalysis fences
on their next use. Restore scripts reject SQL lifecycle utilities atomically.
No executable optimizer-plan or query-data cache is introduced.

Feature family: `make check RUN='^TestReferenceSQLPrepared'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceSQLPrepared'`.
See [parameter and diagnostic rules](DIALECT.md#sql-prepared-statements--change-note).

### Prepared catalog and path invalidation

Ordinary Go statements and SQL prepared SELECT/VALUES/INSERT/UPDATE/DELETE/MERGE
bodies share one private analysis cell. It retains target/source occurrence
identities (including nested queries, views and analyzed unused CTE dependencies),
definition stamps and the resolved namespace environment, not descriptors, data
roots, parameter frames or authorization grants. Each use acquires those identities
before lock waits and analyzes against current descriptors, ACLs and data.

Referenced definition changes, effective search_path changes, namespace callbacks
and DISCARD PLANS cause original-source reanalysis. Raw path changes that resolve
to the same namespaces need not invalidate; namespace creation/rename/drop can
invalidate even when the effective path remains equal. Creating an earlier shadow
table in an already existing schema alone does not invalidate a held query or DML
target. Direct queries and fresh preparation still use current name resolution.
Ordinary data changes and unrelated relation creation do not invalidate analysis.

That shadow-retention rule is a statement-boundary rule. Native admission retains
its existing limitation if original-name resolution changes during a lock wait:
direct execution, fresh preparation and retained handles all return
`ErrCatalogReplaced`, without rows/metadata, importing arguments, write effects or
capture publication. A preexisting shadow may differ from the chosen retained
object, but the independently observed name environment must stay unchanged
through the wait. There is no warmup exception, lock retarget or replay.


Parameters retain their original count/types, including declared unused slots.
Result count, ordered labels, type identity, typmod and collation remain fixed;
incompatible shape reports0A000 `cached plan must not change result type`.
Missing relations/columns instead report normal42P01/42703 diagnostics with original
Go or full PREPARE source positions. Compatible nullability/default/origin changes
supply fresh result metadata, even for empty results; already returned rows retain
their prior metadata. No-output DML has no result-shape fence, but RETURNING does,
and a rejected shape publishes no write effects.

Full invalidation refreshes analyzed temporal scalar/temporal-array literals such
as TIMESTAMP 'now', plus the supported sequence/regtype group, atomically with the
analysis identities. Native enum/domain/composite logical captures and arrays
with catalog-owned leaves preserve their existing immutable identity and lifetime
semantics, including temporal fields inside those logical compounds; this change
does not rewrite their embedded leaves or re-input renamed enum spellings.
Analysis, fixed-signature and native-provenance checks must all succeed before
publishing any replacement cell or capture group. A failed attempt leaves the
same handle available for schema repair.
Publication is successful analysis, not successful row execution: a later importer
or execution failure need not undo it. Unchanged use retains the existing cells.

Catalog Reset/Restore can reanalyze builtin signatures against same-shaped tables
at the next statement boundary. Catalog-owned type identities and persistent
sequence provenance retain their existing lifetime fences. Replacement during an
admitted wait remains fenced: no retry, retarget or name-based rescue. Path/namespace
changes while queued never certify older resolution as valid in a newer environment.
FETCH/EXECUTE utility handles keep their separate live-schema behavior.
`pg_prepared_statements`, planner statistics and unavailable routine/operator
invalidation categories are not implemented by this change.

SQL schema GRANT/REVOKE remains outside native capabilities. Pinned reference
tests exercise real PostgreSQL schema-USAGE grant/revoke/regrant; native public
tests explicitly transfer schema ownership to the reader for path visibility,
without transferring bootstrap-owned tables. Both share the supported table-ACL
deny/regrant phases. Ordinary private schemas do not grant reader USAGE by default.
Private native units exercise actual namespace ACL invalidation through the
existing SetObjectACL authority seam.



## Command and transaction clocks

Each native public SQL request captures a microsecond-quantized command sample
before session/settings and transaction admission waits. A fresh implicit
transaction reuses it; an existing explicit transaction retains its own birth.
BeginTx, SQL BEGIN and AND CHAIN obtain fresh transaction starts. Pin a sql.Conn
or sql.Tx to observe stability across statements and independent physical peers.

Held statements retain analysis/type contracts, not clock samples. Direct and
held Execute both start a new command scope. Native prepared autocommit has
now() = transaction_timestamp() = statement_timestamp(); pq may start a
transaction at Bind before Execute, so portable extended-protocol assertions use
now() <= statement_timestamp(). Native does not emulate transport phases.
clock_timestamp()/timeofday() sample only when demanded. Already-open native rows
retain values and presentation settings across later commands and native controls.
Four selected temporal providers export time.Time/TIMESTAMPTZ; timeofday exports
string/TEXT. Its fixed English six-digit layout is independent of DateStyle.


## MONEY transport and prepared signatures

MONEY/OID790 exports native monetary strings (`$1,234.50`, `-$1.24`) and
string ScanType, with unknown Length/DecimalSize even before iteration or for
empty/all-NULL results. Pinned pq independently exports []byte/interface{}
ScanType. Scan into string or []byte preserves the monetary output; NULL is nil.
Declared native column nullability and existing expression metadata conventions
remain unchanged; pq does not provide Nullable metadata.

Inferred MONEY parameters use captured monetary textual input for string,
owned nonnil bytes, int64 and float64. Bytes are currency text, not binary raw
minor units; nil and typed-nil bytes are NULL. Held executions refresh captured
session presentation and parameter frames; Go kinds never resolve operators.
Use `$1::numeric::money` for exact numeric conversion instead of interpreting
Go float64 as exact decimal data. Literal input errors keep source character
Position (including Prepare); bound input errors have no query Position and
carry the actual `unnamed portal parameter $n = '...'` Context. Arithmetic and
cast execution errors are not relabeled as parameter input failures.
Native failed writes/Restore publish no partial state; retained eager values and
metadata remain owned across Reset/Restore. See [MONEY](TYPES.md#money--change-note).

## Timeouts and terminal physical owners

The five timeout settings use the same physical Session and transaction authority
as SQL/Go controls. Transaction expiry and both idle categories terminate that
owner asynchronously, including when no new request arrives. Canonical cleanup
waits for admitted leases to drain, then rolls back private work and releases
resources outside lifecycle locks; the first termination reason wins. Returning
a sql.Conn or closing Rows/Stmt is not physical termination. Retained eager native
Rows continue to own already-materialized values; they do not suspend idle expiry.

Native accepted Query/Prepare/held Execute/Ping/control work reports an exact
structured FATAL25P04/25P03/57P05 with unlocalized severity and independently
specified primary message. Relation-analysis25P04 can carry its relation Position;
the persisted owner fatal remains location-free. Incomplete native parsing,
including CPU Prepare, has no fabricated semantic-analysis Position.
Caller context cancellation is
still an ordinary context error. A fatal accepted command is never ErrBadConn,
which would permit database/sql to replay work. After that fatal is reported,
subsequent pre-admission use is unusable; Validator/ResetSession discard the owner
before a new pool command, which may safely acquire a fresh default0 session.
Unpublished accepted INSERT work is rolled back, not retried or published twice.

The pinned pq transport intentionally maps FATAL to ErrBadConn (Conn-held
Execute can surface ErrConnDone), so those API errors are not portable SQL
diagnostic fields. Reference tests separately correlate PID, FATAL, SQLSTATE and
exact message from bounded public real-container logs. No native wire-protocol
phases, network service or manufactured pq diagnostics are introduced.
See [settings runtime rules](SETTINGS.md#timeout-behavior--change-note).

## Enum values and prepared signatures

Enums retain catalog-owned logical identity while native transport exports string
values and string ScanType with empty DatabaseTypeName. Length and DecimalSize
are unknown; NULL exports nil and stored nullable columns report true/known.
Pinned pq independently exports raw []byte/interface{} ScanType and unknown
Nullable for enum OIDs. Labels are not trimmed, folded or ordered lexically.
Context-inferred parameters accept exact enum labels and nil; typed TEXT requires
an explicit cast. Invalid bound input uses the existing unnamed portal parameter
Context, whereas literal failures retain source character Position.
Reset/Restore cannot retarget old enum parameter/result signatures by name.
Current source lookup and argument arity still precede cached-contract guards;
fresh prepares work and eager precontrol rows/metadata remain owned.
See [enums](TYPES.md#enums--change-note).

## Composite values and retained row shapes

Named composites and supported user table/view row values export canonical record
strings and string ScanType, with empty DatabaseTypeName and unknown Length/
DecimalSize. NULL is nil; a zero-member nonNULL value is `()`. Domains retain their
logical wrapper and expose the same physical composite metadata. Native nullable
columns report true/known; pinned pq returns raw []byte/interface{} ScanType and
unknown Nullable. Metadata is available before iteration and for empty results.

Inferred bound parameters and unknown literals use the shared record codec;
typed TEXT needs an explicit cast. Child conversion failures remain child-owned.
Enum errors inside records/domains use the input statement's captured search_path
visibility, just like scalar enum input, without adapter-specific message rewriting.
Retained defaults/generation plans share that capture when their admitted text IO
requires it; a computed default error does not acquire unrelated parameter Context.
Literal Position points to the source opener; decoded bound values use opaque
portal Context, never an invented rendering of the raw parameter. Record output
recursively uses the captured presentation/catalog environment, including for
held results after source rename or catalog replacement.

Supported source shape changes retain stable attributes. Current prepared
parameter/result generations see the new shape; previously captured literals
and stored values map retained attributes and append typed NULL members.
Held eager cursors retain the old shape and metadata. Reset/Restore cannot remap
old prepared logical IDs by matching a new spelling; use fresh descriptions.
See [composites](TYPES.md#composites--change-note).

## Domain values and prepared signatures

Domain results retain a distinct logical engine identity but export their ultimate
base values and metadata, including inherited numeric precision/scale or character
length on empty/all-NULL scalar results. Native array/domain-array values and
ScanType are strings with the actual uppercase array DatabaseTypeName; pinned
lib/pq instead exposes raw []byte and interface{} ScanType, and reports no name for
dynamic user-domain array OIDs. Array Length is unknown; NUMERIC[] DecimalSize
tracks its element typmod (unsized native is unknown, pinned pq is65535/65531).
Native domain columns declare nullable true; pq does not
provide Nullable metadata. Ordinary base-owned conversion/input errors are not
suppressed or normalized between adapters.

For BYTEA-domain binary-buffer parity, bind through `$1::bytea` before assignment.
Native inferred BYTEA-domain []byte input is owned binary data; pq's inferred
domain parameter may be textual and reject embedded NUL with22021.
Execution freshly resolves current objects/arity before validating retained
logical parameter and result contracts. A dropped old domain parameter identity
fails native0A000 `cached plan must not change parameter type`; same-name recreation
does not remap that ID. Result-type changes retain the existing0A000 result guard.
Missing targets/columns and arity errors preserve their earlier diagnostics.
After native Restore, use fresh prepared descriptions; retained eager rows still
own their old values and metadata. PostgreSQL old-OID cache failures are independent
oracle controls, not a promised native cache implementation.
See [domains](TYPES.md#domains--change-note).


## Notices and warnings

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
Ordinary SQL identifier clipping emits lexical NOTICE/42622 records with the
original and UTF8-boundary-clipped name, without Position or synthetic Context.
They are produced while parsing direct commands or preparing statements and use
the same captured notice filter/delivery route. Executing an already prepared
statement does not repeat lexical clipping notices.

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
No native DO/RAISE, warning-producing procedural SQL or asynchronous
LISTEN/NOTIFY support is implied by notice delivery.

`make check RUN='^TestNoticesSmoke$'` exercises actual notices,
filters, ownership, peer reentry and control round trips; Go test reports the result.


Run `make check RUN='^TestSessionSmoke$'` for asserted isolation, preparation, physical
reuse/replacement, data survival, diagnostics, cancellation and native controls.
Scoped SQL checks use external definitions:
`make check RUN='^TestReferenceSession[A-Za-z0-9_]*$'`
and `make check REFERENCE=1 RUN='^TestReferenceSession[A-Za-z0-9_]*$'`.
The true-mode family also selects retained tagged authority companions.
Focused nonSQL root mechanics use
`make check RUN='^TestSession(PhysicalIdentityMechanics|ResetMechanics|AdapterMechanics)$'`.
Native race coverage uses the same external session-family RUN with
`make check RACE=1`; root mechanics can also use
`make check RACE=1 RUN='^TestSession(PhysicalIdentityMechanics|ResetMechanics|AdapterMechanics)$'`.

Session smoke asserts distinct session settings, prepared TEXT metadata and zero
affected counts, preserved settings on pool reuse, restored defaults on physical
replacement with surviving data, data retention after pool reopening,
42704/22023/42601 diagnostics, and cancellation/control preservation.
Go test reports the result.

## SQL errors and diagnostics

SQL errors support `errors.As` into `interface { SQLState() string }`; exported `stuntgres.Error` preserves PostgreSQL severity, nonlocalized severity, five-character Code, primary Message, Detail, Hint, one-based character Position, InternalQuery/InternalPosition, Context, and independently optional Schema, Table, Column, DataType and Constraint fields. Optional empty strings are absent; InternalPosition is supplied only with InternalQuery. File, Line and Routine, if present, are implementation-specific locations and never PostgreSQL C locations. `Error()` remains a presentation string, not a source for SQLSTATE or severity. The type is not `*pq.Error`; concrete lib/pq assertions are not compatible. Context, I/O, closed-object, argument-count and foreign-driver errors are ordinary Go errors. Cancellation cannot forcibly interrupt arbitrary blocking reader/writer calls.

Native SQL failures supply both severity fields as `ERROR`, including parser, missing-object and duplicate-object failures; naming an object in the primary message does not imply an object diagnostic field. The error-fields feature smoke is `make check RUN='^TestErrorFieldsSmoke$'`: it checks exact syntax and engine diagnostics, direct/prepared NOT NULL failures, statement atomicity, wrapping, ordinary context cancellation and native control round trips. Go test reports the result. Reference-only permission, procedural and SQL cancellation witnesses exercise PostgreSQL diagnostic transport expectations, not native implementations of those unsupported features.

Shared generic SQL diagnostics construct only severity, SQLSTATE and primary message; they never infer object identities. In particular, input/range conversion errors (`22P02`/`22003`) do not infer Schema, Table or Column from a destination or WHERE predicate. Condition-specific NOT NULL producers (`23502`) supply schema/table/column: INSERT includes complete failing-row Detail; UPDATE includes only modified targets and effectively SELECT-readable columns, in physical order. Without relation-level SELECT, UPDATE prints `(names) = (values)`, even when column grants cover every column; table SELECT, default owner authority and superuser retain the complete unlabelled row. Protected retained values are neither formatted nor disclosed, and retaining them does not require SELECT. Captured output settings and UTF8-safe field truncation are unchanged. SET/ADD existing-row validation has no Detail. Even a named constraint leaves the Constraint field empty. This distinction follows PostgreSQL's [conditional error fields](https://www.postgresql.org/docs/18/protocol-error-fields.html); clients must not infer the presence of one object field from another.

Immediate UNIQUE failures (`23505`) supply Schema, Table and Constraint, with no
synthetic Column or Position. Detail contains all key labels and typed values
only when the current effective authority has table SELECT or SELECT on every
key attribute; partial key readability suppresses runtime Detail. Protected
values are not formatted. Index-build/type-rewrite duplicate errors use
`could not create unique index "name"` and, for readable keys,
`Key (...)=(...) is duplicated.` instead of the ordinary write primary and
`already exists.` suffix. Unreadable build keys produce the generic Detail
`Duplicate keys exist.` without disclosing key labels or values.

Invalid boolean strings retain the original decoded input in the PostgreSQL `22P02` primary message (for example, `invalid input syntax for type boolean: "o"`). Boolean WHERE string-literal failures carry the one-based character Position of the source opener, including actual preparation and empty tables. Escapes and continued strings do not change that provenance. Typed parameter input failures have no query Position and carry `unnamed portal parameter $n = '...'` at the actual failing import index (`''` for empty text). Only failed SQL input diagnostics are copied and annotated; ordinary Go errors and later expression/preflight conversion errors remain unchanged. No destination object identities are inferred.

Encoding-input failures (`22021`) carry only the actual parameter index, because decoded parameter text is unavailable. Codec errors on decoded empty text retain ` = ''`; other decoded text retains ` = '...'`.

This bounded diagnostic change does not replace boolean parsing or native `database/sql` values: accepted aliases/unique prefixes, case/ASCII whitespace, TRUE/FALSE/NULL, bool/nil returns, and atomic INSERT and Dump/Reset/Restore behavior remain baseline regressions. Run `make check RUN='^TestBooleanDiagnosticsSmoke$'` for complete native diagnostic, direct/prepared parameter, atomicity and control witnesses; Go test reports the result. The independent pinned-reference fixtures use `make check REFERENCE=1 RUN='^TestReferenceBooleanDiagnostics[A-Za-z0-9_]*$'`; the same RUN with `make check` exercises native without Docker.

Unterminated comments and quoted tokens report PostgreSQL-style English primary messages containing the raw input from the offending opener (`at or near "..."`), with a separate one-based character Position. Newline-separated string segments form one token, so an unterminated later segment points to the first literal opener and includes the continued raw suffix. A zero-length delimited identifier instead includes only its raw `""` token, not trailing SQL. Diagnostic text is not escaped as a Go string; multibyte characters do not turn Position into a byte offset.

INSERT target-resolution and assignment diagnostics interpolate decoded identifiers between literal double quotes, matching PostgreSQL diagnostic prose rather than Go escaping or SQL identifier quoting. The feature smoke includes an embedded-quote column mismatch and checks its exact diagnostic and complete sentinel preservation.

Decoded E-string NUL/invalid UTF8 reports `22021` with no Position; malformed Unicode reports `22025` with a format Hint, while invalid Unicode values/surrogate pairs and unterminated literals report `42601` with original-source character positions. Raw SQL NUL/invalid UTF8 intentionally retains native positioned `42601` preflight diagnostics, distinct from decoded bytes. Bound text is not SQL escape source; string-family input rejects NUL/malformedUTF8 with22021 byte diagnostics and the actual `unnamed portal parameter $n` Context without a decoded-value suffix. B/X/N strings remain unsupported; `standard_conforming_strings` is configurable as documented in [session settings](SETTINGS.md). Dumps use ordinary strings, preserving decoded controls and literal backslashes.

The focused public proof is `make check RUN='^TestStringLiteralsSmoke$'`; it asserts direct/prepared escape and dollar values, parameter separation, exact diagnostics, statement atomicity, shared-pool Dump/Reset/Restore, retained cursors and the raw SQL boundary. Go test reports the result.

Pinned real-reference feature fixtures use `make check REFERENCE=1 RUN='^TestReferenceStringLiterals$'` and `make check REFERENCE=1 RUN='^TestReferenceStringLiteralDumpRoundTrip$'`; the same RUN with `make check` exercises native without Docker.

Driver and diagnostic assertion unions are now untagged external scenarios.
Use the same definition in both modes, for example:

```sh
make check RUN='^TestReference(DirectAndPreparedParameters|RowsMetadataAndCommandSemantics|ErrorFields[A-Za-z0-9_]*|DriverCanceledHandles|DriverRowsBoundary|NativeCursorEOFAndCancellation)$'
make check REFERENCE=1 RUN='^TestReference(DirectAndPreparedParameters|RowsMetadataAndCommandSemantics|ErrorFields[A-Za-z0-9_]*|DriverCanceledHandles|DriverRowsBoundary|NativeCursorEOFAndCancellation)$'
```

Zero-argument pq simple-query errors, prepared parameter inference and native
ordinary argument errors retain independent records and nil-output checks.
Native eager retained rows and detached Columns slices are not pq streaming or
Columns-copy promises. Native precanceled calls and admitted PostgreSQL server
cancellation are separate phases; full57014 and same-owner recovery remain
asserted without timing retries or fabricated transport retirement.
Original source consumers additionally run real DB.Exec/Query/Prepare and
Stmt.Exec/Query, preserving complete diagnostic stages/nil publications, setup
and every sentinel. SAME held replacement consumers and the eight joined
concurrent-insert workers retain their original object/snapshot ownership.
Recorded Context companions remain separate, not substitutes for plain calls.
Notice reentry workers are joined before normal teardown; unjoined failures
remain visibly failed with bounded cleanup. Normal cleanup accepts sql.ErrConnDone
only for a borrower explicitly closed by the test, not other close failures.



## B-tree utility preparation and controls

Direct Exec and database/sql prepared Execute support ordinary CREATE INDEX;
Prepare retains syntax and empty utility description, not target/row/owner
visibility. Each Execute binds current target, role, search_path, keys, included
attributes and provider identities. Utilities return zero affected rows.
Semantic clause errors occur at Execute with full PostgreSQL18.0 diagnostic
records and absent Position; lexical/syntax failures retain character locations
and occur at Prepare. Class-option validation follows name admission, so a
duplicate name wins over unsupported class parameters and IF NOT EXISTS notices.

Native Dump records actual canonical CREATE INDEX definitions with direction,
NULL policies, explicit `"pg_catalog"."collation"`/`"pg_catalog"."opclass"`
identities and current parent/key/INCLUDE names. Restore uses isolated replay and
one atomic replacement, rebuilding key-only roots with destination identities.
A bad late included column/expression/provider leaves destination rows and
definitions unchanged. TYPE reconstruction rebinds old-default classes and
revalidates retained nondefaults rather than freezing obsolete explicit defaults.
These controls require the unwrapped native DB, not a foreign or sqlmw-wrapped pool.
`InspectIndex(ctx, db, schema, name)` returns detached `IndexState` flags
(`Ready`, `Valid`, `Live`) from the current index descriptor. Names are exact
decoded identifiers, not SQL expressions; lookup never uses session search_path.
`WaitingForOlderSnapshots` is a separate live coordinator observation: its older
cohort and xmin boundary have been captured and the builder's validation XID and
snapshot dropped. It is not persisted history or a waiter count, and clears on
phase exit, cancellation and epoch replacement. Ready alone does not prove this
admission boundary.
`WaitingForWriters` uses the same exact owner/kind coordinator record after the
granted writer cohort is frozen. Combine it with Ready to distinguish first
(`false`) from second (`true`) writer admission. Descriptor publication alone
does not prove capture. The two cohort Booleans are mutually exclusive and
false after successful completion or cancellation.
Missing schemas, missing indexes and non-index relations return ordinary control
errors, respectively `stuntgres: index schema "schema" does not exist`,
`stuntgres: index "schema"."name" does not exist`, and
`stuntgres: relation "schema"."name" is not an index` (identifiers are quoted).
Nil databases, canceled contexts and foreign/wrapped pools retain the same
native-control boundaries as Reset. Existing completed ordinary/backing indexes
report all three flags true. This API does not introduce a SQL catalog or promise
an index-specific query plan.
Concurrent builds expose false/false/true initially and true/false/true after
readiness, retaining that last committed phase on failure/cancellation.
Persistent invalid indexes make Dump fail with
`stuntgres: dump cannot serialize invalid index "schema"."name"` before its first
writer call. Ordinary public standalone UNIQUE/NND retains its bounded
simple-key/provider surface. Valid standalone UNIQUE SQL replays through existing
authenticated isolated Restore destination authority, which bypasses only ordinary
provider-admission policy and still performs substantive shared tuple/root validation.
This preserves computed concurrent definitions without widening public ordinary
provider boundaries or enabling concurrent maintenance.
Native replacement fences old completion/snapshot owners without resurrection.
See [concurrent index authority](design/PG18-INDEX-CONCURRENT.md).
The same emitted SQL can be replayed against PostgreSQL.
Writable explicit ordinary CREATE INDEX transactions remain a native 0A000 boundary;
concurrent creation instead reports25001 in explicit blocks,
independently of read-only 25006 utility rejection.


## REGTYPE input and held descriptions

Native REGTYPE/_REGTYPE exports are strings with string ScanType. Scalar domains
report REGTYPE, domain-over-array reports _REGTYPE, and array-of-domain uses its
actual uppercase name. Pinned pq independently supplies nonnull []byte,
interface{} ScanType and unknown Length/DecimalSize/Nullable; dynamic
array-of-domain DatabaseTypeName is empty. Empty/all-NULL cursors keep metadata.

Parameters are imported eagerly through one captured type lookup authority,
before zero-row evaluation and later executor table rights. Input errors retain
decoded parser positions/invalid-type Context and the existing portal Context.
Runtime `$1::text::regtype` is ordinary expression IO, not portal input.
Literal parser errors transpose Position to the outer literal while preserving
the decoded invalid-type Context. Modifier warnings use the same notice API:
literal analysis once per unchanged held authority, frame warnings on each
Execute with actual slot Context, runtime TEXT warnings without portal Context.

A held literal's OID is not refreshed merely because its referent disappears
or is recreated. Effective lookup-path/role authority/database changes trigger
complete successful reanalysis before publishing the private literal group.
Returned rows retain captured output names even after rename, Reset or Restore;
fresh execution formats against its own snapshot. This does not change existing
prepared parameter/result identity guards or sequence-regclass lifecycle.

