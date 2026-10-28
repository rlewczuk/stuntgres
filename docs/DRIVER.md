# Driver API

The public Go contract of package `stuntgres`. SQL behavior follows
[PostgreSQL 18](https://www.postgresql.org/docs/18/sql.html); native snapshot
controls are in [FIXTURES.md](FIXTURES.md).

Type inquiry expressions publish ordinary typed metadata: `format_type` returns
TEXT and `pg_typeof`/`pg_basetype` return REGTYPE (OID 2206), not display text
masquerading as an OID alias. Native REGTYPE scan values are strings formatted
from the result's captured catalog/path; retained rows keep that spelling across
later catalog replacement. Typed NULL operands still carry logical identity.

## Opening databases

- `stuntgres.NewDriver()` returns a Driver that owns independent in-memory
  clusters. `NewDriverWithConfig(DriverConfig{TrackCommitTimestamp: true})` fixes
  startup settings for every database on that Driver (the zero value leaves commit
  timestamps off; `SET track_commit_timestamp` fails with 55P02).
- `d.OpenConnector(name)` opens a cluster's default database by an **exact opaque
  key** (not a connection string). Identical keys on one Driver share the cluster;
  different keys or Drivers are isolated. Clusters live as long as the Driver,
  even after every pool closes; nothing persists across process exit.
  Use `sql.OpenDB(connector)`.
- Importing the package registers one default Driver as `sql.Open("stuntgres", name)`.
  It cannot be configured, so use `NewDriver` when registering settings or foreign
  data wrappers, or when tests need isolation.
- `OpenConnector` connects as the bootstrap superuser `postgres`.
  `d.OpenConnectorAs(name, role)` asserts an existing role by exact name: a trusted
  host assertion, not password authentication. Each physical connect checks
  LOGIN (28000), CONNECTION LIMIT (53300) and database CONNECT. Stored passwords and
  `VALID UNTIL` do not affect native admission.
- Configuration (`RegisterSetting`, `RegisterForeignDataWrapper`,
  `RegisterForeignValidator`) must happen before the Driver's first connector;
  the registry then freezes.


### Selecting a logical database

SQL `CREATE DATABASE` registers a database in the selected cluster. Roles and
database owner/ACL metadata are shared; objects, data and physical-session
resources belong to the selected database. Default connectors retain the original
attachment and its UTF8-validated, DSN-derived NAME (at most 63 complete UTF8
bytes); clipped display-name collisions never merge distinct opaque keys.

`d.OpenDatabaseConnector(key, database)` selects an existing database as the
bootstrap principal. To combine selection with a trusted role or notice handler,
copy an existing connector:

```go
base, err := d.OpenConnectorAs("opaque cluster key", "app_owner")
if err != nil {
    return err
}
selected := base.(*stuntgres.Connector).WithDatabase("app_database")
db := sql.OpenDB(selected)
defer db.Close()
```

`WithDatabase` preserves role and notice configuration without retargeting the
original connector or live sessions. Every physical Connect resolves the selector
and checks current LOGIN, cluster-wide role connection limits and database
CONNECT. Names are exact decoded names, not SQL-quoted, URL-decoded or trimmed.
Explicit empty is not the default; missing names fail with FATAL 3D000 and are
never created by connecting. CONNECT revocation does not disconnect admitted
sessions; SET ROLE does not re-admit them.

Reset/Dump/Restore and InspectIndex capture the selected database before releasing
the borrower. Controls require an unwrapped native pool and affect only that
database, preserving registrations, owners/ACLs, shared roles and other targets.
Dump excludes cluster registration and roles; see [FIXTURES.md](FIXTURES.md).
Installed amcheck members share one portable installation: `verify_heapam` and
the seven scalar B-tree overloads retain distinct result shapes. Native controls
preserve current installed routine ACLs, including PUBLIC entries, grant options
and grantor provenance, atomically with member identities. Held calls still
recheck current EXECUTE authority; a captured persistent regclass literal is not
retargeted across Reset/Restore.

The engine core owns the [database and template contract](../internal/engine/core/README.md#cluster-and-logical-database-ownership).

## Physical sessions and pooling

Settings, `SET ROLE`/session authorization, SQL `BEGIN` blocks, SQL `PREPARE`
names, cursors, `LISTEN` subscriptions, TEMP objects, advisory locks, sequence
`currval` memory and the `random()`/`setseed` stream belong to one physical
connection. A `*sql.DB` may use any connection, so pin a `*sql.Conn` (or `*sql.Tx`)
for session-scoped work.

Persisted role/database defaults are shared cluster authority, captured once for
the login role and selected database when a physical connection opens. Existing
sessions, pool reuse and identity switches retain their captured reset baseline;
new connections see committed changes. Role rename preserves the principal and
its defaults. Configured notice handlers also receive startup diagnostics before
Connect returns, without running under engine lifecycle locks.

Returning a connection to the pool keeps all of that state: `ResetSession` is a
health check only, never an implicit `DISCARD` or rollback. Run `DISCARD ALL` on
the pinned connection before `Conn.Close` to clean it. Canceled statements and
ordinary SQL errors leave the connection reusable.

## Transactions

- `BeginTx` supports `sql.LevelReadCommitted`, `LevelReadUncommitted`,
  `LevelRepeatableRead` and `LevelSerializable`; `ReadOnly` maps to the access mode.
  Deferrability uses the session default (`sql.TxOptions` has no field for it).
- SQL transaction controls and `BeginTx` share transactional relation/schema/type
  DDL: permanent CREATE/DROP TABLE, CTAS/SELECT INTO, CREATE/ALTER/DROP SCHEMA,
  supported ALTER TABLE forms, ALTER/DROP TRIGGER, ordinary DROP INDEX and
  ALTER INDEX rename/SET/RESET, CREATE/OR REPLACE/ALTER/DROP VIEW,
  CREATE/ALTER/DROP MATERIALIZED VIEW, CREATE DOMAIN/composite TYPE, domain
  rule changes, composite attribute evolution, type/domain rename, schema move
  and DROP, CREATE/OR REPLACE, ALTER RENAME and DROP RULE, shipped function
  creation/replacement and aggregate creation, routine identity ALTER/DROP,
  bounded CREATE/ENABLE/DISABLE/DROP EVENT TRIGGER, operator CREATE/ALTER/DROP,
  CREATE/ALTER/DROP COLLATION and CONVERSION, and CREATE/DROP CAST see earlier
  successful own changes. Direct and held prepared commands use the same transaction; subsequent
  owner consumers use private definitions without exposing them to peers.
  Ordinary and concurrent REFRESH consume earlier own source writes and keep
  stored rows, indexes and population private until commit. Peers see complete
  definitions, constraints, consuming rows and logical keys only after commit.
  Eligible transactional DDL starts event callbacks before target resolution;
  callback rows and DDL stay private through end callbacks and one Finish.
  Callback failure rolls back the statement, including autocommit DDL. Shared
  objects and event-trigger DDL do not recursively fire command callbacks.
  Rollback/savepoints restore definitions, dependencies, row layouts and data
  together; sequence counters are not rewound. Autocommit and retained simple-query
  messages use the same supported forms. Feature-specific collation and conversion
  OWNER is included; procedure creation and language lifecycle keep their separate
  admission policies. Native collation/conversion ALTER retains its original
  target across lock waits rather than adopting a search-path fallback.
  `ErrCatalogReplaced` is an ordinary native error without SQLSTATE and clears
  explicit-transaction savepoints: fully roll back and begin a fresh transaction
  on the same connection before qualified recovery.
- CREATE/DROP ACCESS METHOD and CREATE/ALTER/DROP OPERATOR FAMILY and OPERATOR
  CLASS also share autocommit, SQL transaction controls, Go `BeginTx` and retained
  messages. Later owner commands consume private methods, classes and members;
  peers see the complete group only after commit. Savepoints and rollback restore
  identities, names, dependencies and providers together. Distinct concurrently
  created DEFAULT classes can coexist: multiple exact defaults make implicit
  selection report 42710; nonunique compatible defaults leave no selection and
  report 42704. Explicit classes and existing indexes retain their selected identities.
- ALTER OWNER for tables, types, domains and databases, feature-specific view/
  materialized-view OWNER, REASSIGN OWNED and DROP OWNED share autocommit, SQL
  transaction controls and Go `BeginTx`. They see earlier successful own changes;
  ownership, dependencies, ACLs, policies, default privileges and supported
  membership cleanup remain private until commit and restore at savepoints or
  rollback. Held execution checks current authority rather than retaining the
  preparer's privileges. Ownership transfer preserves object identities and data.
  Database-owner changes publish shared metadata coherently and change implicit
  `pg_database_owner` authority without creating membership rows. Bulk relation/
  type/schema changes do not cross database boundaries; shared metadata uses
  cluster publication, and objects in other databases still prevent role
  retirement. Other utility exclusions remain independent.
- `GRANT`/`REVOKE` on supported objects (including table columns) and
  `ALTER DEFAULT PRIVILEGES` participate in SQL and Go transactions and retained
  messages. Later owner operations see private ACLs and creation-time defaults;
  peers see them only after commit. Savepoints and rollback restore ACLs,
  defaults, created definitions, dependencies and rows together. Defaults apply
  only to future objects of the actual creator, never retroactively or through
  inherited creator roles. Sequence generator advancement is not rolled back.
  These ACL/default directives do not widen existing role lifecycle/membership
  or ownership policies, enable `ALTER SYSTEM`, or admit SQL `PREPARE` of these
  utilities.
- A Go transaction handle outlived by a SQL `COMMIT`/`ROLLBACK`/`AND CHAIN` boundary
  returns `stuntgres: stale transaction reference` instead of ending newer work.
- Under `SERIALIZABLE`, retry the whole transaction on SQLSTATE 40001.
- `PREPARE TRANSACTION` GIDs are case-sensitive, at most 199 bytes, and shared by
  all sessions of one database. Reset/Restore refuse to run while prepared
  transactions exist.

## Statements and results

- **Parameters** use PostgreSQL `$n` placeholders. A parameter's type comes from its
  SQL context, never from the Go argument type; an untypeable parameter fails with
  42P18. Prepared statements keep their parameter and result signatures: an
  incompatible catalog change fails with 0A000 `cached plan must not change result
  type` rather than changing shape.
- A held persistent `regclass` literal retains its catalog lifetime. Native
  Reset/Restore retires that capture with the ordinary error
  `stuntgres: catalog replaced before publication`, without SQLSTATE; reprepare
  it against the current catalog. A held checker taking `$1::regclass` can accept
  a freshly resolved target after replacement. Stored-view queries reanalyze the
  restored definition rather than adopting an old caller literal.
- Relation/schema/type and object/default-privilege utility Prepare/Describe is
  effect-free; execution performs
  current authority and identity checks. CTAS/SELECT INTO and materialized-view
  creation still analyze their query source during preparation. Ordinary view
  creation analyzes its body at execution. Actual held statements use these
  same paths.
- **Results are eager.** A statement finishes executing before `Query` returns, so
  `Rows.Next` never runs SQL and late errors never expose a partial result.
  Rows stay valid across later writes, commits and Reset/Restore. Returned byte
  slices are detached copies.
- `RowsAffected` counts rows actually inserted, updated, deleted or merged.
  CREATE MATERIALIZED VIEW WITH DATA reports the produced row count; WITH NO
  DATA reports zero without running the producer. `LastInsertId` is not supported.
- **AFTER triggers.** INSERT, UPDATE and DELETE row triggers run at query
  completion with completed row images, before statement triggers. Statement
  triggers also fire for zero-row commands. Returned values do not change the
  affected rows or results; errors roll back the command and its trigger writes
  before eager results are exposed. TRUNCATE has statement triggers only, fired
  after all selected tables and identity restarts are staged. Native Dump/Restore
  preserves definitions and data without replay-time firing; information-schema
  trigger metadata reports timing, orientation and per-kind action ordering.
  Direct and held prepared commands share these firing boundaries.
- **Procedures.** Standalone `CALL` returns one row of declared OUT/INOUT columns,
  including a sole composite-valued output as one column. A procedure without
  outputs has no row descriptor. CALL reports zero affected rows, independently
  of body DML. OUT actuals are required and implicitly typechecked (including
  protocol parameter input conversion), but never evaluated or folded.
  Named/default/variadic input assembly shares function overload resolution;
  routine kind is checked after selecting the winner. SQL and PL bodies borrow
  the same statement transaction, with no separate commit or result lifetime.
  SQL-body calls with outputs are rejected rather than inventing copyback.
- **Routine execution context.** Runtime calls check current EXECUTE authority
  before strict NULL suppression; constant-NULL simplification can avoid a call.
  A variadic array is one input value, independently of its NULL elements.
  SECURITY DEFINER uses the current routine owner for the body, without changing
  the authenticated session identity. Routine-local settings nest with the call:
  LOCAL changes remain confined, while ordinary SET can survive successful return
  and remains subject to transaction rollback. SQL and PL bodies share this
  context and restore it on errors and cancellation. Volatility governs body
  command admission and snapshots; parallel, cost and rows attributes are
  declaration metadata, not a promise of worker scheduling.
  Supported PL DML-trigger and event-trigger bodies install the same live
  owner/configuration/volatility environment. Firing retains its own admission
  rules; it does not add an ordinary-call EXECUTE check. ALTER attributes and
  owner changes affect subsequent firings, including held outer commands.
  Non-SQL intrinsic/native providers reject execution-attribute clauses (including
  semantic no-ops) with `0A000` before publication; they do not acquire an
  invented SQL/PL invocation wrapper. Their admitted parallel/cost/rows patches
  are real SQL-visible declaration metadata, separate from execution behavior.
  ROWS changes the estimate, not intrinsic set capability or returned rows;
  defaults remain provider-owned until explicitly changed. Declaration-time
  setting normalization is non-installing; native injected callbacks run outside
  publication locks, and stale or unauthorized candidates cannot publish after
  callback reentry.
- **Multi-statement strings.** A parameterless `ExecContext`/`QueryContext` may
  contain several statements. The whole string is parsed first, then commands run
  in order and stop at the first error. `Exec` reports the last command's count;
  `Query` returns one result set per row-producing command (`Rows.NextResultSet`).
  Calls with arguments and prepared statements accept exactly one statement.
- **SQL `PREPARE`/`EXECUTE`** names and cursors are session objects, independent of
  Go `Stmt` handles; closing a `Stmt` or `Rows` neither deallocates a name nor moves
  a cursor.
- Parameterless prepared definitions retain immutable typed constant results,
  not executable graphs or permission decisions. The first execution (wire
  Bind) computes demanded folds; Prepare/Parse/Describe do not. Current binding,
  casts and authority still run on reuse. Relevant executable dependencies,
  effective namespace/private context, DISCARD PLANS and native replacement
  retire the affected fold lifetime. Incidental operand-name reanalysis alone
  need not discard an otherwise valid typed result. Parameterized definitions
  keep their computed values operation-local. Wire Describe preserves the
  shared definition owner. Bind plans supported CALL, INSERT/UPDATE/DELETE/MERGE
  and modifying-CTE SELECT without applying their effects. Those portals keep an
  independent immutable Bind generation; Execute uses fresh canonical binding,
  current authority and data without publishing back to the definition. Closing
  or replacing the statement, DISCARD PLANS, or a later path change cannot replace
  that accepted portal generation or its selected relation occurrences. Creating
  the same transaction's private catalog journal after Bind is not portal
  replacement: current binding still checks exact identities and current rights,
  and unlocked planning is fenced against its accepted catalog context.
  Native/type/provider incompatibility still rejects use.
  Plain SELECT portals retain their existing private producer graphs. The
  accepted binding owns the actual graph/resource association and immutable
  typed group independently of the named definition; session end closes every
  remaining binding through the normal terminal cleanup path.


Access-method, family and class definition utilities have no parameter or row
signature and report zero affected rows. Go Prepare and wire Parse/Describe/Bind
do not apply their effects; Execute checks current role, schema and provider
authority. Held handles participate in the executing owner's transaction and can
be reused after savepoint recovery. SQL `PREPARE` retains its own syntax policy.

Routine identity and attribute DDL has no parameter/result signature; Prepare stays
effect-free and Execute owns authorization and read-only/transaction admission.
Affected original Go/SQL prepared source reanalyzes: an old spelling may fail
42883, and recreating it legitimately binds the replacement. Hard stored calls
instead retain checked callee identity with current owner/EXECUTE checks.
Returned Rows retain captured values/metadata across these changes. See the
[routine contract](../internal/engine/routine/README.md#routine-alter-contract)
and [selected proof](../reference/query_core/README.md#routine-alter-proof).

### Go values

| SQL type | Non-NULL Go value |
| --- | --- |
| `smallint`, `integer`, `bigint`, `oid` | `int64` |
| `boolean` | `bool` |
| `real`, `double precision` | `float64` |
| `text`, `varchar`, `char`, `name` | `string` |
| `numeric`, `money` | `string` (exact text) |
| `date`, `time`, `timetz`, `interval` | `string` |
| finite `timestamp`, `timestamptz` | `time.Time` |
| `±infinity` timestamps, `bytea` | `[]byte` (owned copy) |
| `xid`, `cid`, `xid8` | `[]byte` (decimal text) |
| `pg_lsn` | canonical uppercase slash-hex `string` (string scan type) |
| `json` | `string` (exact input text) |
| `jsonb`, `uuid`, network, geometric, bit strings, text search | canonical `string` |
| `int2vector`, `oidvector` | canonical space-separated `string` |
| arrays, composites, ranges, multiranges, enums | canonical `string` |
| installed `ean13`, `isbn`, `isbn13`, `ismn`, `ismn13`, `issn`, `issn13`, `upc` | canonical `string`, including retained invalid marker |
| installed `hstore` | canonical key/value-pair `string` |
| installed `lquery` | canonical query-syntax `string` |
| installed `ltree` | exact path-text `string`, including the empty path |
| installed `ltxtquery` | canonical Boolean label-query `string` |
| installed `seg` | canonical interval `string` |
| `reg*` types | symbolic `string` |

NULL is `nil`. A `nil` interface or typed-nil `[]byte` argument is NULL; a non-nil
empty `[]byte` is an empty value. `bytea` string and `[]byte` arguments are raw
octets. Time zone and Unicode data are embedded; no host locale is consulted.

Connection endpoint inquiries on in-process connections return SQL NULL. Address
columns retain `INET` with string scan types and port columns `INT4` with int64
scan types, including empty results; raw values are actual `nil` interfaces.
Direct, prepared and stored-view calls use the same typed result path. Native
Dump/Reset/Restore preserves stored calls symbolically, not as endpoint values.

Vector columns report `INT2VECTOR`/`OIDVECTOR` and string scan types. Text or
non-nil byte arguments may carry empty vectors, distinct from SQL NULL; typed-nil
byte arguments are NULL. Their canonical values and declared types survive native
Dump/Reset/Restore through the ordinary type-owned codecs.

After `CREATE EXTENSION hstore`, hstore columns accept literal and bound text or
non-nil bytes. Empty input is an empty nonnull value; nil and typed-nil bytes are
SQL NULL. A present NULL-valued key and quoted text `NULL` remain distinct.
Results report `HSTORE`, string scan types and declaration-based nullability,
including empty results. Native Dump/Reset/Restore preserves canonical values,
NULLs and installed type identities through destination-authorized remapping.
Held literal assignments retain their values across type rename/schema movement.
Consumer teardown/recreation binds original SQL literals under current types;
it does not revive retired type authority.
`hstore_to_json` and `hstore_to_json_loose` return JSON with string scan types:
strict conversion quotes nonnull values; loose conversion recognizes complete
JSON numbers and lowercase `t`/`f`, preserving numeric spelling. Member NULL
becomes JSON null while SQL NULL remains SQL NULL.
`hstore_to_jsonb` and `hstore_to_jsonb_loose` return JSONB through the same
installed-call hooks. Loose JSONB numbers are normalized exact numerics and
subject to numeric range checks. Explicit `hstore::jsonb` uses strict conversion;
the installed cast does not authorize implicit or assignment coercion.
Stored JSON and JSONB calls keep installed routine identity across presentation
changes and native restoration.
`hstore(record)` uses live field names and each field's text output; a known
named NULL record produces present NULL-valued keys. `populate_record(base,hstore)`
and `base #= hstore` return the base's composite type, replacing exact matching
field names through destination input codecs and declared typmods. Unknown keys
are ignored, absent fields stay intact, and NULL-valued keys set fields NULL.
An oversized varchar replacement fails rather than truncating. SQL NULL hstore
leaves the base unchanged; an empty nonnull hstore with a NULL named base makes
a nonnull all-NULL tuple. These scalar composites expand through ordinary FROM
projection. Stored calls preserve installed identity through native controls.

After `CREATE EXTENSION ltree`, ltree, lquery and ltxtquery columns accept literal and bound
text or non-nil bytes; nil and typed-nil bytes are SQL NULL. Typed literal syntax
such as `ltree 'a.b'` or `public.lquery 'a*'` uses ordinary type resolution in
direct and prepared queries. Ltree preserves exact path spelling and accepts an
empty nonnull path, while lquery emits canonical query
syntax and rejects empty input. Ltxtquery stores canonical Boolean label-query text,
with suffix modifiers and precedence; empty input is invalid. Results report
`LTREE`/`LQUERY`/`LTXTQUERY`, string scan types
and stored column nullability even for empty results. Native Dump/Reset/Restore
preserves values and typed NULLs with destination-owned scalar and codec identities.
Ltree and lquery companion arrays use the shared rectangular array input/output,
bound-value and stored-value contracts. Installed array predicates return BOOL;
first-ancestor/descendant and first-lquery/ltxtquery-match (`?~`/`?@`) extraction
returns LTREE or NULL in physical entry order, independent of array lower bounds.
Whole NULL operands are distinct from empty arrays and NULL elements:
visited arrays reject multidimensional or NULL-containing values at invocation,
not at storage. Native controls preserve arrays, lower bounds and restored
selected expressions with destination-owned generations.

After `CREATE EXTENSION seg`, seg columns accept literal and bound text or
non-nil bytes; nil and typed-nil bytes are SQL NULL. Results expose `SEG`, string
scan types and stored-column nullability, including empty results. Length and
decimal precision metadata remain unknown. Native Dump/Reset/Restore preserves
the exact binary32 endpoint words, significant-digit counts and annotations
under destination-owned identities; ordinary emitted SQL carries canonical text.
Derived infinity and inverted endpoints retain their canonical spelling through
native controls: `3.4e38(+-)3.4e38` yields `0.0 .. Infinity`, and `0(+-)-1`
yields `1 .. -1`. Negative overflow is spelled `-Infinity`. Stock seg text input
rejects these canonical outputs; SQL recreation must reinsert the original valid
spellings. Ordinary SQL replay is a canonical-text round trip only for
reinputtable values; native records retain all these representations exactly.

After `CREATE EXTENSION isn`, EAN13, ISBN, ISBN13, ISMN, ISMN13, ISSN, ISSN13 and UPC columns
accept textual literals and bound strings or bytes, including NULL. Results
expose the corresponding uppercase database type name and string scan type.
ISBN13, ISMN13 and ISSN13 format the long thirteen-digit value, including expanded
short input and any retained `!` marker. ISBN retains its short-output convention;
ISMN accepts M-prefixed or modern 9790 input and returns hyphenated M notation.
ISSN accepts short or 977-prefixed input, keeps its invalid marker in short output,
and uses long output when nonzero issue digits cannot fit in the short form.
An already-typed text expression requires an explicit conversion to its installed
scalar type. UPC emits twelve compact digits plus a retained `!`; typed UPC
assigns to EAN13, and EAN13 converts explicitly to UPC after a subset check.
The driver does not add implicit conversions between other installed
families. Native Reset/Dump/Restore preserves full values, validity flags, issue
digits, NULLs and independent type identities.

## Errors

SQL errors satisfy `errors.As(err, &e)` for `interface{ SQLState() string }` and
are `stuntgres.Error` values carrying PostgreSQL severity, Code, Message, Detail,
Hint, Position (1-based characters), InternalQuery/InternalPosition, Context and
the optional Schema/Table/Column/DataType/Constraint fields. They are not
`*pq.Error`; assert the interface. Context cancellation, I/O and closed-object
errors are ordinary Go errors.

Transaction, idle-in-transaction and idle-session timeouts end the physical session
and report FATAL 25P04, 25P03 or 57P05 on the next use. They are never
`driver.ErrBadConn`, so `database/sql` does not replay work; the pool then discards
that connection.

## Notices and notifications

```go
base, err := stuntgres.NewDriver().OpenConnector("example")
if err != nil {
    return err
}
connector := base.(*stuntgres.Connector).WithNoticeHandler(func(n stuntgres.Error) {
    log.Println(n.Severity, n.Code, n.Message)
})
db := sql.OpenDB(connector)
```

`WithNoticeHandler` returns a configured copy that affects new physical connections
only; plain connectors deliver no notices. Handlers run synchronously, in order,
after engine locks are released and before the command returns. Do not re-enter
the same `*sql.Conn` from a handler.

`stuntgres.WaitForNotification(ctx, conn)` waits on a pinned, unwrapped native
`*sql.Conn` for the next committed `NOTIFY` on a channel it `LISTEN`s to and returns
`Notification{PID, Channel, Payload}` (`PID` is the sender's session identity, not an
OS process). Notifications are not delivered while the receiver is inside a
transaction. Other SQL on the same `*sql.Conn` waits until the call returns, so
cancel its context before closing the connection.

`pg_listening_channels()` returns the pinned physical connection's committed
subscriptions as TEXT rows. `pg_notification_queue_usage()` returns a FLOAT8
fraction shared by the logical databases of that Driver/DSN cluster. Busy
registered listeners retain pending pages even for unmatched or foreign-database
traffic; payload delivery remains database-local. Idle processing at publication
and outer transaction boundaries releases those holds, independently of client
receipt. A within-page hold can report zero.

Both inquiries work directly and through held prepared statements. Native
Reset/Restore retain live subscriptions, pending holds and client-ready events;
Dump serializes none of them, and an independent restored Driver starts empty.

## COPY

`stuntgres.CopyFrom(ctx, conn, "COPY t FROM STDIN ...", reader)` and
`stuntgres.CopyTo(ctx, conn, "COPY ... TO STDOUT ...", writer)` stream text, CSV or
binary COPY data over a pinned native `*sql.Conn` and return
`CopyResult{Count, CommandTag}` on success. Streams stay caller-owned and are never
closed. `COPY FROM` publishes nothing on failure; `COPY TO` may have written a prefix
but reports no result. Read/Write callbacks must not use the same `*sql.Conn`.
Ordinary `Exec`/`Query` of COPY fails with 0A000, and file/program endpoints are
not available.

## Custom settings

```go
d := stuntgres.NewDriver()
err := d.RegisterSetting(stuntgres.SettingDefinition{
    Name: "demo.memory", Description: "Session memory budget",
    Type: stuntgres.SettingInteger, Unit: stuntgres.SettingKilobytes,
    Default: "1024", IntegerMin: 0, IntegerMax: 2147483647,
})
```

Names must be dotted custom names. Types: `SettingBoolean`, `SettingString`,
`SettingInteger`, `SettingFloating`, `SettingEnum` (`EnumValues`). Access:
`SettingSession`, `SettingReadOnly`, deny-only `SettingPrivileged`. Units:
`SettingUnitNone`, `SettingBytes`, `SettingKilobytes`, `SettingMegabytes`,
`SettingMilliseconds`, `SettingSeconds`, `SettingMinutes`. Bounds are inclusive
(zero bounds mean exactly zero); the default is validated like any later value.
Registered settings appear in `SHOW ALL` and `pg_settings`.

The builtin file-location settings `config_file`, `data_directory`,
`external_pid_file`, `hba_file` and `ident_file` expose simulated startup values,
not host paths in use by the engine. The four path labels are respectively
`/stuntgres/simulated/postgresql.conf`, `/stuntgres/simulated/data`,
`/stuntgres/simulated/pg_hba.conf` and `/stuntgres/simulated/pg_ident.conf`;
the external PID value is empty. All five have NULL `pg_settings.boot_val`;
the PID `reset_val` is also NULL, while the other reset values equal their labels.
Source `override` denotes the four simulated initial assignments, not file loading.

SHOW/current_setting require the effective superuser or INHERIT-effective
`pg_read_all_settings` privileges; SHOW ALL and `pg_settings` omit these rows
otherwise. Held prepared readers recheck current privileges. Every individual
SET/RESET/set_config assignment fails with startup-context SQLSTATE `55P02`,
including same values and DEFAULT. RESET ALL leaves their values unchanged.
There is no configuration/HBA/ident file loading, data-directory selection,
PID-file creation, reload or restart effect. These strings neither change
logical database contents nor configure authentication or persistence.

`ALTER SYSTEM SET name TO/= value` (including lists and DEFAULT), `RESET name`
and `RESET ALL` prepare without effects, then fail at execution with SQLSTATE
`0A000`, `ALTER SYSTEM is not allowed in this environment`. Transaction blocks
(including internal exception subtransactions and read-only blocks) take `25001`
precedence over the nested-routine context restriction. These commands are not session
SET/LOCAL/RESET: no live setting changes and no auto file is opened or created.
The absent mechanism is data-directory `postgresql.auto.conf` merge/removal,
`AutoFileLock`, `.tmp` write/sync/rename, and postmaster/backend reload (or restart
for startup-only settings). RESET ALL would remove auto-file entries, not reset
sessions or necessarily delete the file. Simulated paths and `allow_alter_system`
do not enable persistence; logical Dump/Restore never serialize these commands.

The builtin connection/authentication family also exposes stored, typed
simulations through the same SQL registry. Startup-only and sighup parameters
reject individual live changes with 55P02; the eight user-context settings retain
ordinary SET/LOCAL, rollback/savepoint and captured-default semantics. Restricted
reads use the same effective-user authority and aggregate omission described
above. The [settings owner contract](../internal/engine/settings/README.md#connection-and-authentication-settings)
lists the names, simulated build labels and metadata/presentation rules.

These values do not configure listeners, connection admission, kernel sockets,
TLS/SSL, authentication timers, password hashing/storage, MD5 warnings,
OAuth/Kerberos/GSS loaders, files, passphrase commands or Bonjour advertisement.
In particular, native TCP values retain requested numeric settings, with zero
defaults and no OS probes. Existing trusted connectors, real role/database
admission checks and password/warning behavior are unchanged. Returning a
connection retains its settings; replacement captures fresh defaults.
Reset/Dump/Restore remain logical controls and do not reset or serialize live
connection settings.

## Replication settings

The [replication family](../internal/engine/settings/README.md#replication-settings)
uses the same typed SQL registry. `wal_sender_timeout` is user-settable and
`output_plugin_libraries` requires effective superuser or parameter SET authority.
The other 24 settings reject individual live changes with55P02 according to
postmaster/sighup context, including same-value assignments and resets.
Only `primary_conninfo` and `output_plugin_libraries` restrict examination;
SET permission does not grant read permission. Unauthorized aggregate readers
omit those rows. SQL SET reconstructs the plugin identifier list with quoting;
set_config stores its supplied text verbatim without loading libraries, but also
requires examination permission for its returned value. If that read is denied,
the statement fails and preserves the previous setting/source; SQL SET alone
does not require examination permission.

Immutable defaults accept only normalized-equivalent provider answers.
Mutable defaults are captured at physical connection creation; RESET/default
restores that capture, not a new provider answer. LOCAL, transactions and
savepoints use ordinary setting semantics. Same-physical pool reuse retains
overrides; replacement captures fresh defaults. Logical Reset/Dump/Restore
does not reset or serialize live settings or authorization.

These values do not activate physical WAL, replication slots/origins/workers,
receiver connections, feedback, replay delays, synchronous waiting, timeout
termination, restart or plugin-loading effects. Logical data and constraints
remain active. `DriverConfig.TrackCommitTimestamp` and its genuine native
commit-timestamp tracking remain unchanged.

## Resource settings

The [44 resource settings](../internal/engine/settings/README.md#resource-settings)
share typed SHOW/current_setting, pg_settings and ordinary session actions.
All roles can read them. Postmaster and sighup settings reject individual changes
with55P02 before parsing, even with parameter SET permission:
`SET shared_buffers='bogus'` requires restart; `SET file_extend_method='write_zeros'`
cannot be changed now. RESET ALL skips these immutable settings.
max_stack_depth and temp_file_limit require effective superuser or inherited SET
permission; read-all-settings membership does not grant SET.

For mutable settings, `SET work_mem='1024kB'` yields SHOW/current_setting `1MB`,
pg_settings.setting `1024`, unit `kB`, source `session`. RESET/default restores the
physical session's captured default, without provider reentry. Transactions,
LOCAL, savepoints and peer isolation use the existing settings store. Returned
Rows remain snapshots; held statements observe current values and authority.

The native profile is explicitly simulated64-bit Linux/8kB blocks: the automatic
commit/subtransaction/transaction buffer counts are current/reset32 with boot0;
io_max_concurrency is64 with boot-1; max_stack_depth is2MB with boot100kB and a
simulated7680kB ceiling, independent of the host's stack limit. Native
max_prepared_transactions metadata is0; the PostgreSQL18.6 fixture is configured
to100. Actual native logical PREPARE TRANSACTION remains operative. The existing
max_parallel_workers_per_gather logical executor consumer is likewise preserved;
max_parallel_workers/max_worker_processes do not add a pool cap, and
parallel_leader_participation does not introduce executor policy.

Successful owned logical TEMP table data access captures temp_buffers once.
Creating/preparing a TEMP table, using TEMP sequences and failed foreign TEMP
attempts do not initialize it. After SET2048 and TEMP INSERT, explicit SET1024
fails22023; RESET still restores1024, explicit SET2048 still succeeds, and explicit
SET1024 still fails. This is a sticky initialized count, not a comparison against
the current setting. DEFAULT, NULL reset, RESET ALL and transaction/savepoint
restoration bypass that check. DROP/recreate, DISCARD TEMP, pool reuse and logical
Reset/Restore do not uninitialize it; a replacement physical session does.
**Physical page/cache initialization timing is not simulated.**

These values do not allocate memory, enforce spill/file/notify budgets, copy or
extend files, flush writes, prefetch, spawn AIO/autovacuum/background workers,
configure IPC/huge pages, tune byte stacks or enforce2PC capacity. SQL-routine
active-body safety remains separate. Settings are not physical-effect claims.
Logical Reset/Dump/Restore neither clear nor serialize live resource settings.

## WAL settings

The [40 WAL settings](../internal/engine/settings/README.md#wal-settings) expose
typed session configuration and exact catalog metadata to every role. The
seven mutable settings are `commit_delay`, `commit_siblings`,
`synchronous_commit`, `wal_compression`, `wal_init_zero`, `wal_recycle` and
`wal_skip_threshold`; the first, fourth, fifth and sixth require effective
superuser or parameter SET authority. Read-all-settings and ALTER SYSTEM do not
grant SET. The other 33 refuse live changes with PostgreSQL context errors,
including same values, DEFAULT, RESET and NULL `set_config`; grants cannot
unlock them. RESET ALL skips immutable settings and clears mutable overrides
even after their SET grant is revoked.

These are explicit setting-only physical simulations, not durability,
replication acknowledgements, commit delays, WAL generation/compression,
checkpoints, flushes, archive commands/libraries, restore/recovery or summary
workers. Logical data, constraints, transactions and ACLs remain active.
External startup relationships (archive command versus library, WAL level and
mutually exclusive recovery targets) do not establish a native startup API.

The initialized Linux profile presents `archive_command` as `(disabled)` with
empty boot/reset, and `wal_buffers` as 4MB/raw512 with boot -1 and reset512.
`checkpoint_flush_after` is 256kB/raw32 and `wal_summary_keep_time` is 10d/raw14400.
Compression `on` is canonical `pglz`; enum availability is a fixed configuration
profile, not host probing. No native sourcefile/sourceline is invented.
Session SET/LOCAL, rollback, savepoints and prepared authority rechecks use the
ordinary settings store. Providers capture typed defaults once; immutable
providers accept equivalent values only. Returning the same physical connection
retains overrides, replacement captures defaults, and logical Dump/Reset/Restore
never serializes or clears these session settings.

## Preset options

The [20 preset settings](../internal/engine/settings/README.md#preset-options)
share the ordinary registry, SHOW/current_setting and typed `pg_settings`
projection. All roles can read them. Their context is `internal`: SET, SET
DEFAULT, RESET and `set_config` (including NULL reset and LOCAL) fail with
`55P02`, `parameter "name" cannot be changed`, before parsing the supplied
value or consulting SET privileges. RESET ALL skips them while resetting mutable
settings. Transaction/savepoint error and rollback semantics remain ordinary.

Native session-default providers cannot change a preset's effective value.
Equivalent normalized answers (such as `segment_size=1GB` or directory mode
`448`/`0700`) are accepted; differing parsed answers produce the ordinary
`stuntgres: default provider cannot change NAME` error. Invalid answers retain
their typed parse errors. This builtin-only restriction does not change custom
read-only provider defaults or the public provider API. Equivalent provider
answers retain native `source=client`; this is provider provenance, not a
PostgreSQL physical-startup facility. Without a provider, preset source is
`default`, including initialized UTF8 encoding and huge-pages `off`.

`server_version=18.0`, `server_version_num=180000` and startup ParameterStatus
agree with native's truthful 18.0-compatible embedded banner. The reference
fixture independently runs PostgreSQL 18.6 (180006); its vendor banner is not
native identity. Logical Reset/Dump/Restore neither serialize nor reset live
preset values, and restoring tables cannot allocate their simulated resources.

## Server file access boundary

The typed SQL surface includes `pg_ls_dir(text)` and
`pg_ls_dir(text, boolean, boolean)`, plus `pg_ls_logdir()`, `pg_ls_waldir()`,
`pg_ls_logicalmapdir()`, `pg_ls_logicalsnapdir()`, `pg_ls_replslotdir(text)`,
`pg_ls_summariesdir()` and `pg_ls_archive_statusdir()`. Record sources expose
`name TEXT`, `size INT8` and `modification TIMESTAMPTZ`; ordinary aliases and
ordinality preserve this metadata even for zero rows.

EXECUTE is an ordinary mutable procedure privilege: the dirname overloads start
owner-only and the seven record routines also grant `pg_monitor`. An admitted
strict NULL call yields zero rows, not a NULL row; denied NULL calls still fail
with `42501`. Dirname authorization canonicalizes Linux-style logical paths,
allows relative paths staying below the logical data root and component-contained
absolute data/log roots, and respects current inherited `pg_read_server_files`
privileges without treating that role as an EXECUTE grant.

Every authorized non-NULL physical request then fails with SQLSTATE `0A000` and
`<function_name> physical directory listing is unsupported in embedded mode`.
No host directory is inspected, including for superusers, `missing_ok=true` or
non-NULL slot names. Stock PostgreSQL instead operates on real server storage:
missing directories may fail or be tolerated by the selected function/flags,
ordinary files are not directories, and absent replication slots fail `42704`.
Those stock filesystem/slot outcomes are not simulated native directory state.
Native Dump/Restore preserves procedure ACL edits, not physical listings or
session settings.

The same typed boundary includes all four overloads each of `pg_read_file`
and `pg_read_binary_file`, both `pg_stat_file` overloads and both
`pg_ls_tmpdir` overloads. Read results are TEXT and BYTEA respectively; stat
exposes `size INT8`, four TIMESTAMPTZ fields (`access`, `modification`, `change`,
`creation`) and `isdir BOOL`. NULL stat in FROM yields one all-NULL row;
NULL tmpdir yields no rows with the listing shape above. Scalar read calls
with planning-constant NULL can erase before EXECUTE admission, even though
volatile; retained runtime calls, RECORD and SRF calls still require EXECUTE.
Prepared scalar NULL handling uses each imported typed parameter frame, not a
PostgreSQL cost-based custom/generic plan heuristic: repeated native NULL
executions still erase the call. Stock PostgreSQL can instead retain the
parameterized call in a generic plan and require EXECUTE even for NULL.

Read/stat seeds are owner-only; tmpdir additionally grants `pg_monitor`.
Path-role membership does not grant EXECUTE. Non-NULL read validation orders
negative explicit length, lexical path authorization, then excessive length
(above 1073741819). Every INT8 offset is a valid pure input. Tmpdir validates
the logical tablespace OID (default 1663), reporting `42704` for missing OIDs
before the physical boundary.

Authorized reads fail `0A000` with
`<function_name> physical file reading is unsupported in embedded mode`;
stat uses `pg_stat_file physical file metadata is unsupported in embedded mode`;
tmpdir uses `pg_ls_tmpdir physical directory listing is unsupported in embedded mode`.
Length zero and both `missing_ok` values still require physical access and
therefore reject, never returning invented empty content or missing-file NULL.
Stock PostgreSQL instead reads bytes, checks text encoding after reading and
returns actual file metadata. On-disk encoding validation is unreachable
natively; ordinary typed text/argument validation remains enforced. No host
files, timestamps, directory entries or physical state enter Dump/Restore.


## Foreign data wrappers

Register a handler and validator implemented in Go, then create the SQL objects:

```go
d := stuntgres.NewDriver()
err := d.RegisterForeignDataWrapper(stuntgres.ForeignDataWrapperDefinition{
    HandlerName: stuntgres.ForeignFunctionName{Schema: "public", Name: "sql_source_handler"},
    Wrapper:     source, // a stuntgres.ForeignDataWrapper
})
// handle err
err = d.RegisterForeignValidator(stuntgres.ForeignValidatorDefinition{
    Name:     stuntgres.ForeignFunctionName{Schema: "public", Name: "sql_source_validator"},
    Validate: validate, // func(ctx, stuntgres.ForeignObjectKind, []stuntgres.ForeignOption) error
})
```

`CREATE FOREIGN DATA WRAPPER ... HANDLER sql_source_handler VALIDATOR
sql_source_validator` then binds these registrations; SQL options are passed to
the provider uninterpreted. See `reference/tables/fdw_reader_test.go` and
`fdw_writer_test.go` for complete in-process providers.

- **Scans.** `ForeignDataWrapper` mirrors PostgreSQL's FDW callbacks
  (`GetForeignRelSize`, `GetForeignPaths`, `GetForeignPlan`, `BeginForeignScan`), and
  `ForeignScan` provides `IterateForeignScan`, `ReScanForeignScan` and
  `EndForeignScan`. A `ForeignRow` is `[]driver.Value` in the plan's attribute order
  (nil is NULL; the separate Boolean distinguishes a row from EOF). Keep returned
  buffers valid until the next callback. All quals are rechecked locally. A non-nil
  `BeginForeignScan` result is always ended exactly once.
- **Writes.** Optional interfaces enable each command: `ForeignInserter`,
  `ForeignUpdater`, `ForeignDeleter`, `ForeignTruncater`, batching
  (`ForeignBatchSizer` + `ForeignBatchInserter`), and row identity for UPDATE/DELETE
  (`ForeignUpdateTargetProvider` + `ForeignIdentityScan`). Every writer must also
  implement `ForeignTransactionProvider` with statement and transaction rollback;
  savepoints need `ForeignTransactionSavepoints`. Commit is remote-first in
  enlistment order and local last: **not two-phase commit**, so a late failure can
  leave earlier remote commits in place.
- **Parallel scans.** Optional `ForeignParallelScanProvider`
  (`IsForeignScanParallelSafe`, `BeginForeignParallelScan(ctx, req, plan, max)`)
  returns a `ForeignParallelScan` that splits the scan into `PartitionCount()`
  disjoint, exhaustive partitions (count in `[1, max]`), each read through
  `BeginPartitionForeignScan`; `EndForeignParallelScan` ends the shared state.
  Only simple top-level eager SELECTs are eligible; partition `Iterate` calls run
  concurrently, everything else runs on the leader, and results stay eager.
- **Asynchronous scans.** Optional `ForeignAsyncScanProvider` checks the selected
  path with `IsForeignPathAsyncCapable`. When admitted under compatible UNION ALL
  arms, its scan must implement `ForeignAsyncScan.RequestForeignScan`, returning
  a `ForeignAsyncRequest`. `Ready` is a stable channel closed when one row,
  error or EOF is available; pending is not EOF. The leader calls `Complete`
  once after readiness and imports cells before `Close`; cells must remain stable
  until then. At most one request is outstanding per scan. `Cancel` is idempotent
  and nonblocking; `Close` is idempotent and joins all request work. A nonnil
  request returned with an error still transfers cleanup ownership. The engine
  cancels all outstanding requests before joining any, then Ends scans with an
  uncanceled cleanup context. At a successful finite FETCH/MOVE it settles and
  closes requests, retaining at most one imported source row per scan; the next
  motion uses its own admitted context. `enable_async_append` defaults on and is
  captured at statement/DECLARE admission. Provider options, including any
  `async_capable` policy, remain provider-owned. Absent/false capability uses the
  synchronous protocol; async admission does not also start parallel partitions.
  Expression subqueries and repeatedly opened correlated inputs use ordinary
  reusable scans; safe enclosing producers and top-level cursors remain eligible.
- **Import.** `ForeignSchemaImporter` implements `IMPORT FOREIGN SCHEMA`.
- **Contract.** Callbacks run outside engine locks, may run concurrently, and must
  use the context of each call. Never return `driver.ErrBadConn` or `driver.ErrSkip`
  (they are converted to ordinary errors). Native Dump includes user-mapping
  options, including credentials: protect dump output.

## Index state

`stuntgres.InspectIndex(ctx, db, schema, name)` reports an index's publication
state as `IndexState{Ready, Valid, Live, WaitingForOlderSnapshots,
WaitingForWriters}`, for observing the phases of concurrent index builds. Ready
permits write maintenance, Valid permits query use and Live permits use at all.
It needs the unwrapped native pool and exact (unquoted, case-sensitive)
names; missing objects return ordinary `stuntgres:` errors.
