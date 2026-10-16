# Driver and connection lifecycle

[README](../README.md) · [Native compatibility limits](DIALECT.md)

## Foreign provider registration

Register concrete application implementations before the Driver's first
`OpenConnector`/`OpenConnectorAs`. Registration copies decoded schema/name
components (empty schema means `public`), rejects nil/typed-nil wrappers,
nil validators, duplicate signatures and invalid names with ordinary Go errors,
and freezes with other startup declarations. Separate Drivers isolate registries;
all exact-DSN engines of one Driver receive the same installed capabilities.
No registration callback runs while constructing a connector.

This complete registration helper accepts an **actual** provider and validator:

```go
package fixturedb

import (
	"context"
	"database/sql"

	"stuntgres"
)

func ForeignDatabase(name string, source stuntgres.ForeignDataWrapper,
	validate func(context.Context, stuntgres.ForeignObjectKind, []stuntgres.ForeignOption) error,
) (*sql.DB, error) {
	d := stuntgres.NewDriver()
	if err := d.RegisterForeignDataWrapper(stuntgres.ForeignDataWrapperDefinition{
		HandlerName: stuntgres.ForeignFunctionName{Schema: "public", Name: "sql_source_handler"},
		Wrapper: source,
	}); err != nil {
		return nil, err
	}
	if err := d.RegisterForeignValidator(stuntgres.ForeignValidatorDefinition{
		Name: stuntgres.ForeignFunctionName{Schema: "public", Name: "sql_source_validator"},
		Validate: validate,
	}); err != nil {
		return nil, err
	}
	connector, err := d.OpenConnector(name)
	if err != nil {
		return nil, err
	}
	return sql.OpenDB(connector), nil
}
```

SQL HANDLER/VALIDATOR clauses resolve these installed procedure identities:
handler `() -> fdw_handler`, validator `(text[],oid) -> void`. They do not load a
Go symbol, PostgreSQL C function, transport, URL or DSN. SQL options remain
provider-owned configuration, not engine-interpreted connection instructions.
Installed handlers and validators are genuine function ACL targets, with strict,
VOLATILE, parallel-unsafe metadata and ordinary PUBLIC EXECUTE defaults.
GRANT/REVOKE changes their routine ACLs, not their registered callback identity.
Internal SQL/MED calls do not require ordinary EXECUTE, and registration does
not create a standalone SQL-callable Go function.
See PostgreSQL's [SQL/MED commands](https://www.postgresql.org/docs/18/sql-createforeigndatawrapper.html)
for syntax, and the [real independent SQL reader](../reference/tables/fdw_reader_test.go)
for a concrete in-process provider, not a canned-row implementation.

The seven callback signatures use public `stuntgres` types:

```go
type ForeignDataWrapper interface {
	GetForeignRelSize(context.Context, ForeignRequest) (ForeignRelSize, error)
	GetForeignPaths(context.Context, ForeignRequest, ForeignRelSize) ([]ForeignPath, error)
	GetForeignPlan(context.Context, ForeignRequest, ForeignPath) (ForeignPlan, error)
	BeginForeignScan(context.Context, ForeignRequest, ForeignPlan) (ForeignScan, error)
}
type ForeignScan interface {
	IterateForeignScan(context.Context) (ForeignRow, bool, error)
	ReScanForeignScan(context.Context, ForeignScanParameters) error
	EndForeignScan(context.Context) error
}
```

`ForeignRequest` contains `Schema`, `Table`, effective source `Role`, `TableOID`,
`RoleOID`, `Columns`, `RequiredAttributes`, four ordered option lists
(`WrapperOptions`, `ServerOptions`, `TableOptions`, `MappingOptions`), and
`Parameters`. Existing view owner/invoker authority determines the source role.
Each `ForeignColumn` carries stable `Attribute`, decoded `Name`, `TypeName`,
`TypeOID`, `TypeModifier`, `CollationOID`, declared `Nullable`, and ordered
`Options`. `ForeignScanParameters.Values` is the evaluated parameter frame;
`Correlation` is the evaluated outer invocation row, both `[]driver.Value`.
These are copied callback inputs, not SQL-expression/pushdown instructions.

`ForeignRelSize{Rows,Width}` and `ForeignPath{StartupCost,TotalCost,Attributes,Private}`
require finite nonnegative estimates and total cost at least startup cost.
At least one valid path is required. The least total cost wins, first on ties;
the selected path reaches GetForeignPlan unchanged. `ForeignPlan{Attributes,Private}`
determines Begin's exact row layout. Attribute lists contain unique declared
stable IDs and every locally required attribute, including filter/join/order
inputs, not merely visible targets. `Private any` is application planning state.
All SQL quals remain local; no callback removes local checks.

`ForeignRow` is `[]driver.Value`: nil cell is SQL NULL, and the separate Boolean
distinguishes a present zero-column row from EOF. Width must match the plan.
Keep returned cells/buffers stable until the next callback on that scan; the
engine converts through its existing argument/type/domain/typmod bridge and
owns the row before that next callback, including before ReScan or End.
Foreign CHECK/NOT NULL declarations are metadata assumptions, not local
fetched-row INSERT checks. Values use the existing type/domain bridge; foreign
support does not add otherwise unavailable constrained-domain DDL.

At first source demand, planning precedes Begin/Iterate; ordinary execution is eager.
Correlated re-execution restarts the same owned scan with real ReScan and fresh
copied parameters; retained earlier occurrences are not overwritten. An ordinary
SELECT/Exec result Ends before successful publication. A named SQL cursor owns
its scan across finite FETCH/MOVE requests: each returned batch is eager, but
public Rows.Next/Close does not advance or terminate the logical SQL cursor.
SCROLL may retain rewindable state; HOLD drains and successfully Ends before
promotion to immutable values. CLOSE, EOF for forward-only cursors, errors,
transaction/savepoint/session disposal finish the corresponding owner.
The statement's one typed source owner also covers existing local INSERT SELECT,
UPDATE FROM, DELETE USING, MERGE, CTE and CTAS consumers. Completed SELECT leaves
may release their own entry early; other entries finish after source-producing
checks and before local catalog/write publication. DECLARE transfers ownership
to its logical cursor, not to individual FETCH results. End-only failure vetoes
otherwise successful local changes, including non-HOLD COMMIT publication.

A nonnil Begin result transfers cleanup ownership even alongside an error.
End runs exactly once per transferred scan, with request cancellation detached;
the source owner retains its cleanup outcome for publication. Generic query-stage
Close remains idempotent and does not replay a prior close error. An existing query/conversion/context error wins
over cleanup failure; End-only failure prevents otherwise successful terminal
publication, CLOSE or HOLD promotion. There are no automatic retries or workers.
Every callback runs outside engine/catalog/transaction/session mutexes and the
replacement fence, but normal SQL object locks and session admission still apply.
Use other connections for unrelated observer SQL; same-session recursive SQL is
not promised. Implementations must support concurrent independent executions.
Never retain a previous request context for a live cursor; each callback receives
its newly admitted request context. Applications own real source cleanup.
Callback contexts have a stable association with that one request, including its
cancellation/deadline and values. They are never mutable cursor-admission proxies:
an asynchronous source-driver observer cannot be retargeted to a later FETCH or
race with clearing the cursor's borrowed statement authority.

Validators receive complete resulting ordered options and the exact
`ForeignObjectKind`: `ForeignAttribute=1249`, `ForeignWrapper=2328`,
`ForeignServer=1417`, `ForeignUserMapping=1418`, `ForeignTable=3118`.
Empty options are nonnil. Rejection prevents the whole catalog statement from
publishing. Ordinary provider/I/O/context errors remain ordinary Go errors;
providers may intentionally return a structured `stuntgres.Error`.

Optional `ForeignSchemaImporter` adds
`ImportForeignSchema(context.Context, ForeignImportRequest) ([]ForeignTableDefinition,error)`.
Its request contains remote/local schema, server, effective role, wrapper/server/
mapping/import options, `Tables` and `Except`. Definitions contain decoded table
names, concrete `ForeignColumnDefinition` entries (`Name`, type spelling `Type`,
ordered `Options`, `NotNull`, scalar `Default`/`Check`) and table `Checks`.
The engine independently filters, forces destination/server, analyzes legal
definitions, validates options and publishes all tables atomically. An actual
empty schema/filter may import zero; absent support is HV00P, never fake success.
Definition and validator SQL diagnostics retain their fields and existing
context, with the importing table appended as logical SQL context. Ordinary
Go/I/O errors are not assigned an invented SQLSTATE or PostgreSQL C context.

Reset/ordinary Restore retain configured callables, not user SQL/MED metadata.
Dump rejects user SQL/MED metadata before writing any bytes or invoking providers.
Unchanged auto-installed custom namespaces can accompany ordinary table dumps
into matching or unconfigured Drivers; owner/comment metadata is preserved.
Renamed/retired/recreated configured namespaces and existing explicit namespace
ACL limitations fail preflight. Dump never serializes executable implementations.
Replacement preserves the existing deferred cursor-owner policy: stale scans
remain owned until the next request, rollback/CLOSE or session close. The next
FETCH/MOVE detects the stale epoch before pulling and cleans up unlocked, with
ErrCatalogReplaced primary. Reset/Restore does not synchronously end other
sessions' scans or claim their resources are already zero. Previously finalized
ordinary results and FETCH batches remain immutable and readable.

## Commit timestamp startup configuration

```go
d := stuntgres.NewDriverWithConfig(stuntgres.DriverConfig{
	TrackCommitTimestamp: true,
})
connector, err := d.OpenConnector("test-database")
// Handle err, then use sql.OpenDB(connector).
```

`NewDriver()` and a zero-value `Driver` leave tracking off. Configuration is
copied at construction, immutable thereafter, and inherited by every engine on
that Driver. DSNs remain exact opaque database keys, not configuration strings.
Closing/reopening physical connections or pools on the same Driver/DSN does not
reset tracking or history. Construct another Driver to choose another startup
mode. Per-session default providers cannot override this builtin.

`SHOW track_commit_timestamp`, `current_setting` and `SHOW ALL` report the
engine's startup mode. Individual SET, SET LOCAL, RESET and `set_config` attempts
return `55P02`, including same-value/default/invalid-value assignments, because
the parameter cannot be changed without restarting the server. RESET ALL leaves
the startup mode unchanged.

Both lookup arguments infer XID for unknown literals/parameters. The origin
lookup alone declares named input `xid`. Returned records use the ordinary
composite metadata/codecs, with named TIMESTAMPTZ/OID fields and an additional
XID field for last completion. Held statements reevaluate live committed state.
See [selected proof](../reference/locking_transactions/README.md#commit-timestamp-information)
and [history retirement](FIXTURES.md#commit-timestamp-history).

## Pinned COPY text streams — change note

`stuntgres.CopyFrom(ctx, conn, sql, reader)` and
`stuntgres.CopyTo(ctx, conn, sql, writer)` use a pinned native `*sql.Conn`.
Both return `CopyResult{Count, CommandTag}`: successful operations report the
complete row count and `COPY n`; every failure returns a zero result. A nil
connection/stream, foreign or wrapped driver, non-COPY/multiple statements,
direction mismatch and precanceled context fail without reading or writing.
Streams remain caller-owned and are never closed.

Ordinary Exec/Query/Prepare cannot supply a stream and report native `0A000`
directing the caller to the corresponding helper. File and program endpoints
remain excluded: no filesystem or process access is performed. SQL arguments
are not interpolated into a second statement.

Text, CSV and binary transfers share these pinned stream helpers, ownership,
atomicity and result contracts; no prepared COPY or protocol server is added.
Defaults, identities, generated columns, type input/output, constraints,
partition routing, ACLs and publication use existing native authorities.
Caller binary bytes are PostgreSQL 18 COPY data, not ordinary SQL parameter
encoding. After the binary trailer, caller EOF is still required.
Typed SELECT/ColumnTypes and held statements see the actual imported state,
and native Dump/Reset/Restore re-exports it through the same binary providers.
Nullable domain-element arrays retain the logical element OID; discover that
identity independently through native type lookup or the PostgreSQL catalog,
not a previous binary export. Domain scalar metadata
continues to describe the physical base, while native array metadata uses its
actual associated array name. Binary NUMERIC special values use the same native
invariant as text input, including inside ranges and multiranges; wire infinity
display scale32 does not become native special-value scale.
An unsupported raw CHAR declaration is not an output exclusion: existing catalog
CHAR fields export real binary values. Public XID/CID/XID8 scalars use fresh
canonical decimal `[]byte` results and `[]byte` ScanType, distinct from OID int64.
COPY preserves four-byte unsigned XID/CID and eight-byte unsigned XID8 network
fields. Full-width XID8 input uses text or bytes; native Go uint64 above MaxInt64
fails at the ordinary driver converter boundary. Typed-nil bytes are SQL NULL,
while nonnil empty bytes are invalid identifier input. Native XID8 metadata stays
XID8 even though pinned pq reports an empty name. Companion arrays remain excluded.


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

Feature smoke: `make check RUN='^TestReferenceCopyCSVFeatureSmoke$'`;
pinned counterpart:
`make check REFERENCE=1 RUN='^TestReferenceCopyCSVFeatureSmoke$'`.


## Explicit DISCARD and pool reuse

Pin `*sql.Conn` for owner cleanup. Execute standalone `DISCARD ALL` **before**
`Conn.Close` returns it to the pool. Reborrowing that same physical connection
then sees captured setting defaults, no SQL prepared names/TEMP objects/sequence
memories, no SQL cursors or their pg_cursors metadata, and released session
advisory holds. Peers and durable data are unchanged.
Without explicit cleanup, healthy reuse intentionally preserves these resources:
`ResetSession` is still a health check, never automatic DISCARD or rollback.

TEMP first use creates a real table/toast namespace pair visible through
`pg_namespace` once committed. `DISCARD TEMP/ALL` and physical termination
remove TEMP objects, not the paired namespace tuples. Physical end clears the
session's table-namespace pointer and sequence memories; retained empty metadata
does not confer active authority on later sessions. Healthy pool reuse keeps
the same physical session and its existing pair. Native Reset/Restore preserves
the destination's exact TEMP pair and live objects but never imports another
engine's physical tokens.

Physical-end cleanup clears sequence memories, the last-sequence address and
active TEMP namespace under the engine catalog mutex, including collation
retirement and the no-owned-namespace exit. This synchronizes ending sessions
with concurrent native catalog replacement while leaving resource callbacks,
maintenance waits and session-registry removal outside that critical section.

SQL PREPARE names and ordinary Go statements have distinct lifetimes. PLANS retains
both and lazily reanalyzes private literal captures without changing fixed
parameter/result signatures or catalog fences. ALL removes SQL names (subsequent
EXECUTE gives26000); native Go AST handles remain callable for valid persistent
sources. lib/pq server-backed handles may instead fail26000 after server ALL.
Retained anonymous Rows and executing subset cursors are not rewound or disposed.

## Direct SQL messages — change note

Direct `ExecContext` and `QueryContext` have a parameterless multi-command route.
The entire input is parsed before execution: malformed suffixes cannot leave
prefix effects. Direct calls with arguments and prepared handles retain a
one-command typed parameter frame and reject batches without prefix execution.
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

Materialized refresh retains its full private replacement, including DATA/NO DATA
population for plain refresh and the effective matched-row diff for concurrent
refresh, across source writes, private CREATE, repeated refresh and pending
role/MAINTAIN grants. Multiple MV and index roots publish together at message end;
runtime uniqueness disclosure uses each concurrent target's MV owner. BEGIN
promotes already-refreshed roots without publication; late errors or cancellation
discard them. Uninterrupted concurrent replacements bridge committed reader
snapshots only at successful outer publication; private CREATE and an intervening
plain rewrite never manufacture that provenance. A producer reading another MV
captures one coherent physical-source snapshot before source waits and retains
its old rows across compatible concurrent source publication. Exact target,
index, provider and defining-authority fences remain strict. Genuine explicit-
block refresh and REFRESH as native Restore input remain excluded.

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

Authenticated physical admission also checks current database CONNECT after the
existing role/login/resource checks. Database ACL changes do not disconnect an
already admitted session, and SET ROLE does not re-admit it. A rejected startup
publishes no physical session or TEMP resources. Own-TEMP USAGE remains available
after allocation; CREATE follows current-role database TEMP. Revocation before
first allocation names the database, while revocation after allocation names the
actual physical TEMP schema. Native Reset/Restore preserves destination database
identity/authority rather than importing a source database ACL.

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

Native Restore owner directives admit named `SET SESSION AUTHORIZATION` and the
existing normalized administrative reset: `RESET SESSION AUTHORIZATION`, `SET
SESSION AUTHORIZATION DEFAULT` and the native-only `SET AUTHORIZATION DEFAULT`
alias. PostgreSQL 18 rejects the short alias with syntax error `42601`, Position5;
dump SQL uses the PostgreSQL-valid long reset. Accepted native resets affect only the
isolated replay session, restoring its original authenticated administrator. They do not
reset live caller sessions, mutate cluster roles or change earlier restored
owners/ACLs. Named short `SET AUTHORIZATION 'role'` and role DDL remain rejected
as Restore input, with late failures leaving the target unchanged.
The final inherited-rank repair block uses this reset rather than extending public
catalog-write privileges. See [selected owner-reset proof](../reference/roles/README.md#testing).

## SQL cursors — change note

Named cursors belong to one physical session; use a pinned `sql.Conn` or
`sql.Tx`, not arbitrary pool calls. Native declaration captures the source
snapshot and owned parameters without producing target values. Ordinary SELECT
is fully eager; each successful cursor fetch likewise returns an eager detached
batch with complete schema, including empty and zero-column results.
Closing Go Rows or Stmt does not close or rewind the named cursor.

`pg_catalog.pg_cursors` has six ordered columns: name TEXT, statement TEXT,
is_holdable BOOL, is_binary BOOL, is_scrollable BOOL and creation_time TIMESTAMPTZ.
Each row belongs to the physical querying session, also through prepared SELECT,
joins and stored views. Metadata observation never demands the producer or moves
the cursor. Exhausted and recovered-failed entries remain visible until removal.

The statement is the complete original submitted input, preserving whitespace,
comments, semicolons and placeholders. Direct batches retain the whole message
in every declared entry. Driver Prepare installs nothing; each successful
execution captures its own declaration statement-start instant, independent of
transaction birth and the first effective FETCH. Commands in one direct message
share that message-start stamp. FETCH/MOVE, HOLD promotion and changes to TimeZone
preserve the instant; SELECT applies its current timezone to the representation.

CLOSE and CLOSE ALL remove rows. Nonhold transaction completion removes rows;
HOLD creating commit retains them, creating abort removes them, and later rollback
preserves already-promoted entries. Savepoint rollback removes newer declarations,
but never rewinds older motion or resurrects CLOSE. DISCARD PLANS and healthy
pool reuse preserve metadata; DISCARD ALL and physical teardown clear it.
Dump excludes it. After Reset/Restore, metadata remains observable with the same
facts and instant under current builtin type ownership; this does not rebind
cursor execution. Actual FETCH/MOVE retains the existing catalog-replacement
fence. Previously detached metadata results remain immutable after any of these
transitions.

Live execution retains its query tree and uncaptured demand position. Rewind
can rescan node-specific volatile input while preserving order caches; there
is no universal target-freezing, redraw or one-expression-draw-per-row promise.
The first effective demand captures stable identity and statement-time data.
Subsequent demand uses fresh operation/context and current cancellation, notice
and lock policy, but original transaction authority and source snapshot.
Returned batches remain immutable after cursor close, transaction/session end
or native catalog replacement.

Successful live-cursor publication preflight is its completion boundary:
later cancellation cannot retract a delivered batch or completed session
utility. Autocommit holdable installation still requires draining and canonical
transaction publication. Demand/prepublication errors expose no partial batch
and leave the producer failed, not rewound. Runtime type-input notices belong
to the current demand operation and its captured filter, never a finished
declaration operation; previously demanded batches are not replayed or re-warned.

Creating commit materializes healthy holdable producers before write publication
and cleanup. Scrollable producers reuse the same retained tree and restore
their saved position; this demand can redraw volatile targets. Export preserves
typed values, schema, cardinality and original epoch, not a source pin, lock,
operation, context or transaction authority. Promotion is per portal: an
already exported store can survive a later drain or publication failure.
Stored reads evaluate no targets, draw no sequences and acquire no query locks.
Current fetch authority renders catalog identifiers and timezone-sensitive
values; already delivered batches retain their captured spellings.
Stored values own their codecs, not a retained declaration catalog.
Physical session end and explicit owner cleanup release stores; pool return does not.

Cursor metadata preserves native column origin and known/unknown nullability.
Promotion does not synthesize nullable descriptors or convert typed values to
text; lib/pq independently reports unknown nullability.

Positioned mutations borrow the live physical occurrence, not projected values
or a Prepare-time incarnation. They use ordinary DML publication; failure
exposes no partial writes or returning batch. The native occurrence retains no
operation, context or projected-value copy. Reset/Restore fences old identities
instead of refreshing them from equal restored tuples. Detached mutation results
retain their own values/schema.

Preparing a cursor utility installs nothing and performs no movement.
Native held fetches return the current entry's actual metadata, including after
same-name replacement, and are exempt from ordinary immutable prepared-result
signatures. lib/pq can instead retain stale wire descriptions or fail in its
decoder after type-changing replacement; native never emulates that corruption.
Dump excludes names, positions and retained execution. Reset/Restore fences the
next admitted use rather than recapturing sources.

Feature smoke: `make check RUN='^TestReferenceSQLCursorMoveFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceSQLCursorMoveFeatureSmoke$'`.
HOLD smoke: `make check RUN='^TestReferenceSQLCursorHoldFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceSQLCursorHoldFeatureSmoke$'`.
Metadata smoke: `make check RUN='^TestReferencePGCursorsFeatureSmoke$'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferencePGCursorsFeatureSmoke$'`.

## SQL prepared statements — change note

SQL `PREPARE`, `EXECUTE` and `DEALLOCATE` own named definitions in the physical
session. They are neither database/sql statement handles nor `PREPARE TRANSACTION`.
Hold a `sql.Conn` (or a `sql.Tx` on that owner) for a named lifecycle: an arbitrary
pool call may use another physical session. Returning and reborrowing the same
physical connection retains definitions; physical close/replacement clears them without deleting
committed database data or a peer session's definitions.

Go preparation of a named-definition utility describes it without resolving
declarations or analyzing/installing the body. Installation/removal occurs only
at Exec. A named-execution utility describes the current name without evaluating
actual expressions; execution returns current native result metadata rather than
emulating lib/pq's stale wire-description decoder behavior.
Go `Stmt.Close` does not deallocate a SQL name; detached eager Rows remain usable
after deallocation and physical session end.
The retained-row witness closes the public driver.Conn inside sql.Conn.Raw,
returning nil, then consumes the already eager sql.Rows. It does not force
database/sql's borrowed-connection close via ErrBadConn while those Rows still
own that wrapper's release; wrapper close/wait/cancellation is a separate API
lifetime from the detached engine result.

SQL named execution has no outer adapter parameter frame. Go arguments are
checked against the utility handle, not rebound into the named definition.

Named definitions are native session effects, independent of private row stages.
Named execution shares the ordinary admission, command clock, snapshot/lock and
Finish boundary, including permission, cancellation and atomicity authorities.
Final session publication preflight protects installation/removal against late
cancellation, just like cursor session effects. Only successful session utilities
skip private-stage merging; normal EXECUTE writes still publish normally.

Native Dump omits names. Reset/Restore preserves live physical-session owners
and definitions, with existing catalog-generation/signature/reanalysis fences
on their next use. Restore scripts reject SQL lifecycle utilities atomically.
No executable optimizer-plan or query-data cache is introduced.

Feature family: `make check RUN='^TestReferenceSQLPrepared'`;
pinned counterpart: `make check REFERENCE=1 RUN='^TestReferenceSQLPrepared'`.

### Prepared catalog and path invalidation

Ordinary Go statements and SQL prepared SELECT/VALUES/INSERT/UPDATE/DELETE/MERGE
bodies share one private analysis cell. It retains target/source occurrence
identities (including nested queries, views and analyzed unused CTE dependencies),
definition stamps and the resolved namespace environment, not descriptors, data
roots, parameter frames or authorization grants. Each use acquires those identities
before lock waits and analyzes against current descriptors, ACLs and data.

Selected genuine routines carry exact formal identity and original lifetime
references through that analysis. Each execution checks current caller EXECUTE
before actual constant-provider computation and surviving scalar/aggregate/window
initialization, including stored view/default/generated/CHECK/index consumers.
Strict literal NULL, eliminated branches and pruned FROM producers retain their
actual demand contracts; there is no per-row privilege lookup or cached grant.
An independently observed PostgreSQL old inlined quote_literal(anyelement) plan
can survive EXECUTE revocation. Native held analysis checks fresh rights instead;
fresh queries/prepared executions deny on both backends. This precise difference
does not imply generic PostgreSQL plan-cache emulation.
The same boundary covers the finite genuine SQL-body closure and VALUE-dependent
domain CHECK programmes. Parameter/literal domain input captures the actual caller
before unlocked providers; RI-local owner authority cannot overwrite or borrow
the invoker's initialization. Actual phase transitions and waits readmit original
consumed graphs, not CHECK truth or old granted rights. Lazy description inquiries
initialize only on first non-NULL body entry under the existing session admission
owner; a new FETCH cannot inherit an earlier command's domain initialization.
Callable index constant keys plan before predicate truth on the first real tuple;
false predicates still prune nonconstant keys, and empty INSERT sources do not
initialize index recipes. Constant RETURNING planning stays earlier than index
demand, while row-dependent RETURNING stays later. Native fresh-X behavior also
differs from the independently observed warm PostgreSQL constant-key index cache;
the warm predicate case continues to require authority on PostgreSQL.

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

Rule definition changes participate in the same transitive prepared-analysis
fences. Reuse expands current rules and rechecks the caller, event owner and
action-source privileges; it does not retain grants or an executed producer.
The original parameter/RETURNING signature remains authoritative when actions
target different relations, and compatible `_RETURN` replacement refreshes a
held view query without changing its described layout.

Direct Query may expose rule SELECT-action result sets. An actual Go-held
non-RETURNING INSERT retains empty result metadata/no action rowsets while those
actions still execute; repeating direct Query is not prepared coverage. All
actions share the original request's clock, timeouts and one publication.
Rule utility preparation is quiet; native DDL admission occurs at execution and
remains autocommit-only. See the [owning public scenarios](../reference/views/README.md#rewrite-rules).


`pg_catalog.pg_class` uses the PostgreSQL 18 34-column order rather than its former
native ten-column schema. Positional full-star consumers must update destinations;
named projections avoid this arity/order upgrade. Catalog readers capture current
descriptors and matching role snapshots on each execution, not table grants at
Prepare. Native eager ACL/option/bound output and metadata remain owned after
descriptor changes and logical replacement. This catalog read does not enable
native SQL GRANT/REVOKE on catalog sources or additional hierarchy alteration.

Full invalidation refreshes analyzed temporal scalar/array literal captures and
the admitted sequence/regtype capture group atomically with analysis identities.
Native enum/domain/composite logical captures and arrays
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

SQL schema GRANT/REVOKE uses real native USAGE/CREATE ACL authority. Direct and
held consumers lose namespace access after revoke and recover after regrant,
without schema-owner transfer or changes to bootstrap-owned tables. Ordinary
private schemas do not grant reader USAGE by default. Namespace generation and
original resolved identities control invalidation; same-name recreation does not
rescue old lifetime authority. Prepare is effect-free for valid object utilities,
including explicit transactions. Database and foreign-wrapper/server grants retain
their explicit-transaction exceptions; other object grants remain autocommit-only.
Execution read-only rejection precedes object lookup.



## Command and transaction clocks

Each native public SQL request captures a microsecond-quantized command sample
before session/settings and transaction admission waits. A fresh implicit
transaction reuses it; an existing explicit transaction retains its own birth.
BeginTx, SQL BEGIN and AND CHAIN obtain fresh transaction starts. Pin a sql.Conn
or sql.Tx to observe stability across statements and independent physical peers.

Held statements retain analysis/type contracts, not clock samples. Direct and
held Execute both start a new command scope. Native prepared autocommit shares
transaction and statement clock samples; pq may
start a transaction at Bind before Execute. Native does not emulate transport
phases. Demand-time providers sample only when evaluated. Already-open native
rows retain values and presentation settings across later commands and controls.
Native temporal clock outputs use time.Time/TIMESTAMPTZ; textual clock output
uses string/TEXT with a fixed English six-digit layout independent of DateStyle.


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
Go float64 input is not exact decimal data. Literal input errors keep source character
Position (including Prepare); bound input errors have no query Position and
carry the actual `unnamed portal parameter $n = '...'` Context. Arithmetic and
cast execution errors are not relabeled as parameter input failures.
Native failed writes/Restore publish no partial state; retained eager values and
metadata remain owned across Reset/Restore. See [MONEY](TYPES.md#scalar-go-values).

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
See [settings runtime rules](SETTINGS.md#timeout-behavior).

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
See [enums](TYPES.md#arrays-composites-enums-and-domains).

## Composite values and retained row shapes

Named composites and supported user table/view row values export canonical record
strings and string ScanType, with empty DatabaseTypeName and unknown Length/
DecimalSize. NULL is nil; a zero-member nonNULL value is `()`. Domains retain their
logical wrapper and expose the same physical composite metadata. Native nullable
columns report true/known; pinned pq returns raw []byte/interface{} ScanType and
unknown Nullable. Metadata is available before iteration and for empty results.
Executable composite arrays retain the existing custom-array metadata convention:
native DatabaseTypeName is the uppercase associated catalog name (for example
`_PAIR`), while pq exposes an empty name for the custom OID. Both export canonical
array text; native ScanType is string and pq ScanType is interface{} with raw bytes.
Domains over arrays expose the physical array metadata; scalar/nested composites
retain the empty DatabaseTypeName described above.

Anonymous ROW outputs instead report DatabaseTypeName `RECORD`, unknown Nullable/
Length/DecimalSize, native raw strings/string ScanType and pinned pq raw []byte/
interface{} ScanType. Constructor/field/expanded-member metadata is available
before iteration and for empty outputs. Prepared execution imports fresh
parameter frames, preserving full member usage; stable captured field attributes
survive permitted preceding-member removal and rename without slot/name retarget.
Held prepared field selections and expansions retain their original stable slots
and output names across ADD/RENAME/DROP; a dropped selected slot returns typed NULL.
Named composite/array outputs instead use the current descriptor. Newly bound
inputs decode against it, while held analyzed literals retain their original shape.
Whole NULL, present all-NULL and present zero-field records remain distinct.

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
See [composites](TYPES.md#arrays-composites-enums-and-domains).

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
See [domains](TYPES.md#arrays-composites-enums-and-domains).


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
Native notice producers use the command's captured filter and delivery route.
Lexical notices occur during direct parsing or preparation and do not repeat
when an unchanged prepared statement executes. Connector copies preserve the
asserted authenticated role. Session-authorization changes preserve the notice
filter. Errors remain returned errors, never callbacks.

Callbacks run synchronously in producer order after all engine locks and the
operation lease are released, before command return; payload strings are immutable
owned Go data. User code is not context-preemptible: cancellation during a callback
does not undo committed execution or replace its result. Reenter through another
already held physical connection or a separate pool with free capacity, **not**
the same sql.Conn or an exhausted originating pool. Preparation emits no execution
notices. Native controls do not serialize or deliver staging notices.
No native DO/RAISE, warning-producing procedural SQL or asynchronous
LISTEN/NOTIFY support is implied by notice delivery.
Native session-message commands remain
[explicitly unsupported](DIALECT.md#session-notifications) because the public
native connection API has no notification receiver; notice handlers are not
a substitute.

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

Native diagnostic tests retain value transport, failed-write atomicity and
Dump/Reset/Restore witnesses; they do not redefine PostgreSQL lexical rules.
Raw SQL NUL/invalid UTF8 retains native positioned `42601` preflight diagnostics,
distinct from decoded input failures. Bound text is data, not SQL escape source:
input rejects NUL/malformed UTF8 with `22021` and actual parameter-index Context,
without inventing a decoded-value suffix. Parameter annotation never changes
ordinary Go errors or later expression/preflight conversion errors.

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

