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
  It cannot be configured, so use `NewDriver` when registering settings, foreign
  data wrappers, compiled host routine languages or text-search files, or when
  tests need isolation.
- `OpenConnector` connects as the bootstrap superuser `postgres`.
  `d.OpenConnectorAs(name, role)` asserts an existing role by exact name: a trusted
  host assertion, not password authentication. Each physical connect checks
  LOGIN and database admission before initializing defaults; role connection
  limits remain cluster-wide. Stored passwords and `VALID UNTIL` do not affect
  native admission.
- Configuration (`RegisterSetting`, `RegisterForeignDataWrapper`,
  `RegisterForeignValidator`, `RegisterHostLanguage`, `RegisterTextSearchFile`)
  must happen before the Driver's first successful connector construction; the
  registry then freezes.
  `RegisterTextSearchFile(TextSearchFile{Name: "words.dict", Data: data})`
  registers explicit Driver-local startup bytes. Names are nonempty lowercase
  ASCII letters, digits and underscores followed by `.dict`, `.affix` or `.stop`,
  never filesystem paths. Registration rejects duplicate names and copies Data;
  nil and empty Data both supply a genuinely empty file. Content parsing belongs
  to SQL dictionary initialization, not registration.
  Ispell dictionary initialization selects these explicit `.dict`/`.affix` bytes
  by SQL basename; optional `.stop` bytes filter normalized lexemes. Explicit
  stop files override the embedded English list for ispell, even when empty.
  The immutable capability is shared across a Driver's exact-DSN engines,
  logical databases and template seed, and survives native Reset. Dump records
  dictionary definitions, never resource bytes; Restore initializes against
  destination resources and fails atomically when they are missing or malformed.
  Missing names fail; no filesystem paths, loader callbacks or sample language
  data are synthesized.

### Application-supplied scalar routine languages

`d.RegisterHostLanguage(HostLanguageDefinition)` registers compiled application
code before opening a connector. It copies declaration slices, checks shape and
duplicates atomically, and runs no application callback. Independent Drivers have
independent registrations. This is a native Go callback ABI, not Go source
interpretation, PL/Python, PL/Perl, PostgreSQL's C ABI or a shared-library loader.

A definition supplies a stable `Provider`/`ABIVersion`, decoded language `Name`,
exact `HostName{Schema, Name}` handler name, `Trusted` security decision and a
non-nil `HostHandlerFunc`. Its handler receives the already-resolved immutable
`HostProgramFunc` and actually invokes supplied compiled code. Program definitions
carry exact `ID`, `Version`, ordered input/result `HostType`s, conversion selectors
and non-nil executable functions. SQL `AS 'program-id'` selects that registered
program; arbitrary source and symbol/library forms do not manufacture execution.
The bounded declaration surface is ordinary scalar IN arguments and results.
Existing SQL/PLpgSQL and absent external-runtime boundaries remain separate.

`HostType` supports `HostBool`, `HostInt4`, `HostInt8`, `HostText` and the genuine
extension-owned `HostHstore`. Registration does **not** install hstore. Explicit
`CREATE EXTENSION hstore` supplies its actual type generation and materializes
independent registered converter procedures and bootstrap associations, not extra
released extension members. `HostConversionDefinition.Bootstrap` designates at
most one actual pair per logical type in a language. Other conversion definitions
may supply real defaults without creating pair rows.

`InputConversions` has exactly one selector per input. A nonempty selector names
the registered bootstrap pair; an empty selector chooses the actual registered
language default for that direction. A missing selected direction can use only
that direction's genuine registered default, never an implicit identity/text
conversion. These are compiled program selectors, not SQL TRANSFORM clauses.

Callbacks use only context and plain Go carriers:

- `HostSQLValue` contains a logical type, SQL-null flag and one matching
  Bool/Int64/Text/Hstore payload. Integer range, text and returned-carrier checks
  precede publication into SQL values.
- `HostHstoreEntry{Key, Value, Null}` is an owned pair projection.
  `HostHstoreMap map[string]HostHstoreValue` distinguishes a missing key, a null
  entry and a textual value; `HostHstoreValue{Text, Null}` allocates no per-value
  string pointer. A genuine map converter traverses the owned entries into a
  map, and its reverse validates/projects the returned map into entries.
- `HostObject{Null, Value}` distinguishes top-level SQL NULL from language objects.
  A map ABI must reject a nil nonnull map; an allocated empty map is nonnull.
  STRICT calls retain the shared SQL invocation-demand and typed-null rules.

FROM SQL, handler/program and TO SQL callbacks run synchronously outside engine,
replacement-reservation, session and settings mutexes. Context cancellation is
cooperative: arbitrary application Go code cannot safely be killed. Close any
application-owned resources in the supplied callback's own defers. A separate
connection may be used for reentry; recursively using the same busy physical
connection is not promised. Balanced current operation/epoch/capability and
EXECUTE readmission surrounds each individual callback, including the program
invoked inside a handler, before another capability or returned carrier is used.
The program capability lent to the handler is detached on unwind: retaining it
cannot retain a live SQL frame/authority or execute it after invocation ends.
The common routine environment owns settings/security unwind and statement
rollback. Native callback/registration/capability errors are not fabricated
Python diagnostics.

Input carriers never expose immutable SQL backing. Returned storage must not be
mutated concurrently during transfer; mutation after the SQL invocation completes
cannot alter detached SQL results. Applications remain responsible for their own
callback data races. Stable provider/ABI/program versions are application promises
of compatible semantics and object representation, not function-pointer equality.
Native logical replay requires explicit matching destination registration and
never serializes executable closures or claims that a Go-host stream runs as
Python SQL on PostgreSQL.
Native replay rebinds to the destination's immutable registered capabilities and
fresh catalog/type/pair generations. It preserves dropped/renamed providers and
real surviving directions rather than re-installing them from names. A strict
versioned native absence boundary preserves missing pairs before declarations
consume them; it does not add SQL `TRANSFORM` DDL or persist runtime objects.


### Selecting a logical database

SQL `CREATE DATABASE` registers a database in the selected cluster. Roles,
database owner/ACL metadata and configuration-parameter identities/ACLs are
shared; ordinary objects, data and physical-session setting values belong to
their database/session. Existing and future attachments consume the same
committed parameter authority; transaction-private grants remain private until
commit. Default connectors retain the original
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
and atomically checks ALLOW_CONNECTIONS, CONNECT, then the database connection
limit before defaults callbacks. Disabled databases reject everyone (FATAL 55000);
superusers bypass CONNECT and limit rejection but occupy counted physical slots.
Database limits count reservations, initialization, live sessions and terminal
cleanup, not borrowers. Returning a borrower does not free a slot; physical close
does so only after cleanup drains. Role limits are checked separately at session
publication. Names are exact decoded names, not SQL-quoted, URL-decoded or trimmed.
Explicit empty is not the default; missing names fail with FATAL 3D000 and are
never created by connecting. CONNECT denial is FATAL 42501; an applicable full
database limit is FATAL 53300. Revocation after reservation affects the next
physical admission, not the retained admission; SET ROLE never re-admits.

Reset/Dump/Restore and InspectIndex capture the selected database before releasing
the borrower. Controls require an unwrapped native pool. Local data replacement
affects only that database, preserving registrations, owners/ACLs, admission
policy, shared roles and other targets. Parameter ACL authority is the shared exception: Reset clears
it cluster-wide and Restore replaces it cluster-wide with valid destination
identities. Session setting values are not shared or imported. Dump excludes
cluster registration and roles; see [FIXTURES.md](FIXTURES.md).
Installed amcheck members share one portable installation: `verify_heapam` and
the seven scalar B-tree overloads retain distinct result shapes. Native controls
preserve current installed routine ACLs, including PUBLIC entries, grant options
and grantor provenance, atomically with member identities. Held calls still
recheck current EXECUTE authority; a captured persistent regclass literal is not
retargeted across Reset/Restore.

`pg_catalog.pg_init_privs` exposes historical initial ACLs for represented
bootstrap objects and supported extension privilege events. Ordinary grants,
revokes and owner changes do not refresh these snapshots. Extension ADD captures
explicit object and column ACLs; DROP membership removes them. Inventory rows
are public, subject to normal SELECT/column privileges on the catalog itself;
reading history never grants access to the represented object.
Historical ACL references participate in role dependencies. REASSIGN OWNED
rewrites those references explicitly. DROP OWNED revokes the selected grantee's
historical grants made by the object's current owner, with RESTRICT semantics:
old-owner grants can remain after an owner change, and dependent onward grants
can reject cleanup. DROP ROLE remains blocked while historical references remain.
Native Dump/Restore preserves initial and current ACLs independently, including
historical role references and deleted bootstrap records, remapping both object
and role identities into the destination. Reset restores the fresh bootstrap
inventory. Existing exported rows retain their captured values.

Logged and unlogged tables retain their SQL identity in `pg_class` and native
Dump/Restore. Table persistence changes publish transactionally with all surviving
owned sequences; failed action lists, cancellation and rollback publish none.
Both kinds retain committed values for the Driver's process-local lifetime,
including pool close/reopen. These logical controls do not provide WAL, crash
truncation, disk durability, standby replication or recovery across process exit.

Current extension membership does not select a provider implementation. Native
controls recreate genuine released declarations and dependencies, then restore
final membership, symbolic identities, owners and ACLs, including detached
survivors and stock `plpgsql` deltas. Ownership directives use the strict v3
logical format; obsolete or malformed directives cannot publish a replacement.
Lawful membership changes do not alter released `tablefunc`, `pageinspect` or
`hstore` declarations, including record/SRF output and nullable library metadata.
Stored consumers retain genuine selected identities and recheck current access;
removing an extension link never removes the object's ordinary dependencies.
Native replay preserves current routine parallel/cost/rows settings separately
from immutable released implementation identity.
The installed `moddatetime` trigger keeps those bindings through routine rename,
extension relocation and native controls. Its transaction-derived timestamp
values retain full microseconds even in precision-limited columns; replay
restores the saved values without firing a new clock or requiring a surviving
helper. Ordinary SQL assignment still applies the column's declared precision.
Ordinary helper calls remain evaluation-time protocol errors under the genuine
current name. Held SQL is reparsed after catalog changes: a vacated old spelling
is undefined, and an ordinary decoy at that spelling does not retarget the
stored trigger binding.
Ordinary text-search parsers may be extension members without acquiring an owner,
ACL or installed-provider provenance. Their genuine callback declarations and
dependent nonmember configurations survive membership changes and relocation;
native replay creates the parser before its configurations and final membership.
Membership ADD/DROP requires superuser authority after extension ownership;
actual extension relocation also retains the lower ownerless parser gate.
The pinned default parser may acquire or detach membership, but actual DROP
remains prohibited by its database-system pin, including while it is a member.
Snowball's intrinsic helpers, template, English/Arabic/Armenian dictionaries and
configurations retain their bootstrap identities independently of current extension membership.
Their current memberships may differ or be absent without changing callback
capability. Native replay reuses those seeds through ordinary ALTER operations,
preserving current names, placement, owners, ACLs, dictionary options and mappings;
ordinary Snowball dictionaries still replay as genuine CREATE declarations.
Database-system pins remain independent of membership and prohibit fixed simple/parser
seed DROP. Allocated Snowball identities instead retain ordinary dependencies.

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
`ALTER DATABASE ... SET`, `SET FROM CURRENT`, `RESET` and `RESET ALL` change
the selected database's committed defaults, not the issuing physical session.
They share storage with `ALTER ROLE ALL IN DATABASE`; role-specific defaults
take precedence at startup. The exact opaque DSN and `WithDatabase` selector
still choose the engine/database independently of settings. Native Reset and
Restore preserve destination defaults; Dump does not export them.

Returning a connection to the pool keeps all of that state: `ResetSession` is a
health check only, never an implicit `DISCARD` or rollback. Run `DISCARD ALL` on
the pinned connection before `Conn.Close` to clean it. Canceled statements and
ordinary SQL errors leave the connection reusable.

### Cooperative execution pauses

`NewExecutionPause(point)` creates a one-shot host backpressure request.
`WithExecutionPause(ctx, pause)` scopes it to that request and its descendants;
`pause.Wait(ctx)` observes entry into matching detached execution, and
`pause.Resume()` idempotently releases all matching workers, including future
arrivals. Always defer Resume before starting work. Resume before entry makes
Wait return an ordinary error, never a false admission. Canceling Wait's context
does not resume work; cancellation of the effective SQL operation does release
its worker with that operation's original error and cause.

Attachment is not permission to block metadata capture: description, binding
and stored-definition constant evaluation remain unsuspended. Runtime admission
enables pauses only after releasing metadata locks; nested binding removes that
permission while retaining the request for subsequent detached execution.
Statement deadlines, session termination, operation release and COPY input
cancellation ownership are unchanged. No callback runs at a pause, and an
unconfigured request allocates no pause state or polling goroutine.

Supported stable work points are:

- JSON: `json.each.open`, `json.each.next`, `json.record.open`,
  `json.strip_nulls`, `json.array_length`.
- JSONB: `jsonb.containment`, `jsonb.elements.next`, `jsonb.exists`,
  `jsonb.path_delete`, `jsonb.pretty`, `jsonb.populate_record`
  (both record and recordset conversion).
- Transformations: `array.transform`, `datetime.add_interval`,
  `encoding.convert`, `text.like.match`, `text.like.escape`,
  `text_search.vector`.
- Query/crypto work: `query.aggregate`, `query.recursive`, `crypto.bcrypt`,
  `crypto.sha`.

A point names cooperative work, not a SQL admission guarantee: NULL, unused,
folded or otherwise undemanded work need not reach it. Unknown points never
admit. A nested WithExecutionPause replaces the inherited request; nil removes
it. Pauses neither own a connection nor permit concurrent use of its busy
physical owner; use an independent connection for other work.


## Transactions

- `BeginTx` supports `sql.LevelReadCommitted`, `LevelReadUncommitted`,
  `LevelRepeatableRead` and `LevelSerializable`; `ReadOnly` maps to the access mode.
  Deferrability uses the session default (`sql.TxOptions` has no field for it).
- Constraint timing is independent of transaction READ DEFERRABLE. SQL and Go
  commits check deferred UNIQUE/PK/EX/FK obligations before publication; a failed
  commit rolls back the transaction's private rows and definitions. SET
  CONSTRAINTS checks pending work retroactively when made IMMEDIATE; preparing
  that command has no timing effects. Savepoints restore modes and obligations.
  Native Dump/Restore preserves committed definition defaults and data, not
  active SET overrides, pending events or private rows. These remain logical,
  process-local controls, not PostgreSQL physical durability or pg_dump.
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
- `ALTER EXTENSION` UPDATE, SET SCHEMA and member ADD/DROP share SQL and Go
  transactions and retained messages. Schema relocation checks destination CREATE
  before extension ownership, including same-target requests, and retains the
  actual member-class authority. Recursive membership and mechanical relocation
  publish atomically with ordinary dependencies; neither operation changes
  executable provider origin, effective ownership or ACLs.
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
- `pg_catalog.pg_prepared_xacts` projects only successfully prepared transactions:
  typed XID, exact GID, preparation time, owner and database. Its metadata is
  PUBLIC-readable; reading another user's row does not grant permission to finish
  it. Each execution, including a held SELECT, observes current prepared state;
  commit or rollback prepared removes the corresponding row.

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
- **BEFORE statement triggers.** Logical INSERT/UPDATE/DELETE boundaries execute
  before source and row effects, including zero-row commands. Statement frames
  have no OLD/NEW row images, and PL composite returns cannot suppress or modify
  rows or counts. Name ordering, WHEN, UPDATE OF and trigger modes use current
  firing authority. Writable CTEs share query-owned relation/event firing state;
  nested routine commands own independent boundaries. Conflict UPDATE announces
  INSERT then UPDATE; MERGE announces its declared action kinds. Parent-targeted
  DML does not emit leaf statement events. BEFORE TRUNCATE runs for every selected
  relation before any truncation or identity restart. Errors and cancellation
  roll back outer and trigger writes without replay.
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
  `pg_catalog.pg_prepared_statements` observes the current physical session's
  named definitions with eight columns: name, original statement, prepare_time,
  parameter_types, result_types, from_sql, generic_plans and custom_plans.
  SQL definitions retain the complete original PREPARE source; named wire Parse
  definitions retain their Parse source and have from_sql=false. Unnamed Parse
  and in-process Go handles do not introduce named rows. Creation time is the
  admitted command-start instant, unchanged by execution or observation. Formal
  and result identities are regtype arrays; a zero-column result has an empty
  result array, while a command without results has NULL. Both bigint planner
  statistics are typed NULL: this runtime has no generic/custom plan chooser.
  DEALLOCATE, DISCARD ALL and physical-session termination remove definitions;
  DISCARD PLANS, pool ResetSession and native Reset/Restore retain names and
  original provenance. Native catalog replacement gives newly observed values
  current type descriptors without mutating earlier eager rows.
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
| `pg_snapshot`, `txid_snapshot` | `[]byte` (owned canonical snapshot text; byte-slice scan type) |
| `pg_lsn` | canonical uppercase slash-hex `string` (string scan type) |
| `json` | `string` (exact input text) |
| `xml` | transport-normalized `string` (string scan type; OID 142) |
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
Native SQL source, identifiers, Go string/typed parameters and returned text
remain Unicode regardless of `client_encoding`, including characters that the
selected network encoding cannot represent. This setting governs the server
wire boundary and byte-stream COPY, not a reinterpretation of native Go values.

Connection endpoint inquiries on in-process connections return SQL NULL. Address
columns retain `INET` with string scan types and port columns `INT4` with int64
scan types, including empty results; raw values are actual `nil` interfaces.
Direct, prepared and stored-view calls use the same typed result path. Native
Dump/Reset/Restore preserves stored calls symbolically, not as endpoint values.

XML string and non-nil byte arguments use text input; bytes are copied before
execution and are not binary protocol parameters. XML nil/typed-nil bytes are
SQL NULL, distinct from empty CONTENT. Session `xmloption` is captured for each
execution's XML parameters and concrete text casts. Accepted typed XML literals
retain their prepare-time value without reparsing or replaying input warnings.
XMLPARSE has its own explicit mode. Character casts and XMLSERIALIZE CONTENT
NO INDENT preserve original spelling; typed output and binary send apply their
transport declaration and encoding rules.

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
Scalar `@>` and `<@` return BOOL for inclusive ancestor/descendant relationships
over complete case-sensitive labels. Equal paths match in both directions;
the empty path is every path's ancestor. Either SQL NULL operand produces NULL.
The genuine `ltree_isparent` and `ltree_risparent` backing routines use the same
installed identity and caller privileges. Stored scalar predicates and projections
retain destination-owned identities through native controls.
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

## Cooperative work admission

`WithWorkAdmission(ctx, admission)` attaches a request-scoped native embedding
policy. `WorkAdmission` is `func(context.Context, WorkClass) error`; a nil policy
disables control. Pass the returned context to ordinary direct or prepared
`database/sql` operations. Attaching the policy does not alter context values,
deadlines, cancellation or causes. It is inert during binding and locked codecs;
only actual runtime work at supported unlocked provider frontiers invokes it.
Completed requests and retained statements, values and plans retain no policy.
Without a policy the provider admission path allocates nothing.

Each successful invocation consumes one application-defined credit for one
semantic quantum of its class. Applications can share credits across requests,
replenish a bounded channel to resume work, or return an ordinary refusal error.
These are consumptive credits, not permits held across nested work. A callback
must be concurrency-safe, honor the supplied effective context, and return
`ctx.Err()` when its wait is canceled. It must not reenter the busy connection;
an independent connection remains usable. The engine invokes admission without
holding its engine/session/store mutexes. Statement-owned SQL resource locks
remain held while waiting, just as for other statement work.

```go
credits := make(chan struct{}, 32)
ctx = stuntgres.WithWorkAdmission(ctx, func(ctx context.Context, class stuntgres.WorkClass) error {
    if class != stuntgres.WorkEncodingConvert {
        return nil
    }
    select {
    case <-credits:
        return nil
    case <-ctx.Done():
        return ctx.Err()
    }
})
// A workload scheduler supplies credits; the request waits until granted.
rows, err := db.QueryContext(ctx, "SELECT convert_to(v,'LATIN1') FROM messages")
```

The supplied context is the effective operation child, including statement
timeout and session termination, not merely the original caller context.
Cancellation is checked before admission and again after a successful grant,
so a callback cannot publish success after cancellation. Ordinary policy errors
preserve identity and do not acquire a synthetic SQLSTATE. Existing parent
cancellation, deadline, cancellation-cause and SQL timeout/FATAL error mappings
remain authoritative. A nil nested policy disables an inherited policy.

Supported `WorkClass` quanta:

| Class | One semantic quantum |
| --- | --- |
| `WorkPatternNormalize` | At most 4096 escape-normalization iterations; an iteration may consume an escaped pair. |
| `WorkPatternMatch` | At most 4096 LIKE matching iterations, distinct from normalization. |
| `WorkJSONArrayCount` | One token-state step after validation of the array root. |
| `WorkJSONObjectExpand` | One JSON object-expansion token-state step. |
| `WorkJSONRecordExpand` | One JSON record-expansion token-state step. |
| `WorkJSONStripNulls` | One token-state step or at most 4096 copied bytes. |
| `WorkJSONBContainment` | One containment traversal frame step. |
| `WorkJSONBObjectExpand` | One present object member, not NULL or EOF. |
| `WorkJSONBArrayExpand` | One present array element, not NULL or EOF. |
| `WorkJSONBExistence` | One object-search, array or scalar-string candidate step. |
| `WorkJSONBPathDelete` | One delete path or bounded copied-entry chunk; unrelated set/insert work is not charged. |
| `WorkJSONBPretty` | One node/frame step or at most 64 indentation bytes. |
| `WorkJSONBRecordPopulate` | One record-field, domain or array-frame step. |
| `WorkArrayTransform` | One `array_to_string` input element examined, including skipped NULLs, after child operands, strict NULL handling and separator/replacement conversion; no element-output byte bound. |
| `WorkCalendarArithmetic` | One nonzero month/day adjustment stage, after interval-negation and infinity precedence. |
| `WorkEncodingConvert` | At most 4096 source bytes on character boundaries in either sizing or emission pass; validation and identity conversions are not charged. |
| `WorkPasswordBcrypt` | One fixed bcrypt round containing two Blowfish expansions. |
| `WorkPasswordSHA` | At most 4096 password-specific copied/hash bytes or 64 repeated bytes, not an arbitrary password round. |
| `WorkAggregateInput` | One aggregate support/input initialization before the child opens or drains, absent precomputed partials; not input draining or sorting. |
| `WorkRecursiveUnion` | One producer loop step advancing a row or opening/completing a round, possibly encountering EOF or discarding a duplicate. |
| `WorkTSVectorConvert` | One text-search document/token conversion step after unlocked configuration capture. |

These quanta are cooperative scheduling boundaries, not universal CPU, memory,
wall-clock or query-wide accounting. JSON expansion uses an explicit operation
class; raw input scans, classification and extraction do not spend its credits.
Long scalar scans and comparisons retain their ordinary cancellation checks;
a structural step does not promise a scalar byte bound. Cached immutable work
need not execute again and therefore need not consume credit. SQL NULL, lazy
operand evaluation and original error precedence are unchanged.

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
after engine locks and the request lease are released. Input notices arrive
before the command returns; typed XML output notices arrive during `Rows.Next`,
with each result set's captured `client_min_messages` filter. Metadata, NULL,
EOF, Close and incidental diagnostic/dump formatting emit no output notices.
A warning produced before a later field conversion fails is still delivered.
Handlers may use an independent connection; do not re-enter the same `*sql.Conn`.

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

## Logical publication definitions

Publication commands retain database-local definitions, explicit table and schema
memberships, column lists, normal Boolean row filters and publish options in the
ordinary transaction journal. Catalog readers and membership inquiries consume
current definitions; held statements recheck current authority at Execute.
Replica-identity restrictions apply to relevant UPDATE/DELETE capabilities,
including empty targets. Publication filters do not restrict local reads or
writes. Dump/Reset/Restore preserve or atomically replace this logical metadata;
there is no WAL streaming, network replication or data-transport API.

## COPY

`stuntgres.CopyFrom(ctx, conn, "COPY t FROM STDIN ...", reader)` and
`stuntgres.CopyTo(ctx, conn, "COPY ... TO STDOUT ...", writer)` stream text, CSV or
binary COPY data over a pinned native `*sql.Conn` and return
`CopyResult{Count, CommandTag}` on success. Streams stay caller-owned and are never
closed. `COPY FROM` publishes nothing on failure; `COPY TO` may have written a prefix
but reports no result. Read/Write callbacks must not use the same `*sql.Conn`.
Ordinary `Exec`/`Query` of COPY fails with 0A000, and file/program endpoints are
not available.

Callback panics preserve their original value, including an `EndForeignInsert`
panic raised during terminal completion rather than stream execution. The native
COPY boundary completes the original scope's failed cleanup before rethrowing:
no success result or publication, no replay of the ended terminal callback,
and no retained COPY reservation or implicit transaction. The borrowed physical
connection remains reusable unless its session independently ended.

Text/CSV reader and writer bytes use `client_encoding` unless an explicit
`ENCODING` selects a separate file encoding; the SQL statement itself remains
Unicode. Input converts incrementally before line framing and escape handling.
Output converts attributes before escaping, including multibyte trail bytes.
Binary COPY preserves its framing and raw numeric/bytea payloads; type-defined
textual leaves use the session client encoding even with a file override.

`pg_catalog.pg_stat_progress_copy` exposes real admitted COPY commands to an
independent connection, including workers in other databases of the same cluster.
Both reader and writer callbacks must use a separate connection for observation;
neither can reenter the pinned worker. Rows end with the command, even inside an
open transaction, and disappear after success, cancellation, failure or panic.
The active row includes genuinely queued AFTER-trigger execution and terminal
foreign insertion callbacks. Terminal provider cleanup uses a detached context:
canceling COPY does not interrupt an already-entered callback. Progress remains
while it is held; cancellation still requires failed command completion afterward.
`relid` is the table OID, or zero for query output; supported endpoints are `PIPE`
and their unknown `bytes_total` is zero, not NULL or an inferred reader length.

Counters describe completed work, not committed rows. `bytes_processed` sums raw
bytes obtained from input before conversion, or encoded payload bytes accepted
by output writes, including headers and framing but excluding wire envelopes.
Tuple counts follow validated row admission, genuine buffered insertion flushes
and accepted foreign results; HEADER and binary framing are not tuples.
WHERE false/NULL rows are excluded; IGNORE conversion discards are skipped.
No physical byte estimate or cross-backend read-ahead equality is implied.
Superusers, inherited members of the worker's authenticated role and
`pg_read_all_stats` (including `pg_monitor`) see the full row. Other observers see
only `pid`, `datid`, `datname`; the remaining eight fields are SQL NULL.
Observer effective-role changes apply to each fresh read. Row order is unspecified.

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

## VACUUM progress

`pg_catalog.pg_stat_progress_vacuum` is a read-only, unordered view of actual
admitted VACUUM work. Its 15 nullable columns, in order, are `pid integer`,
`datid oid`, `datname name`, `relid oid`, `phase text`, then bigint
`heap_blks_total`, `heap_blks_scanned`, `heap_blks_vacuumed`,
`index_vacuum_count`, `max_dead_tuple_bytes`, `dead_tuple_bytes`,
`num_dead_item_ids`, `indexes_total`, `indexes_processed`, and
`delay_time double precision`.

PID/database/relation identify the real worker and its current target, not the
observer. Superusers, inherited members of the worker's authenticated role, and
inherited `pg_read_all_stats` members see details. Other observers retain only
pid/datid/datname; all other twelve cells are typed NULL. Table privileges and
the worker's current SET ROLE alone do not grant visibility. Each execution,
including a held prepared SELECT, uses current observer privileges. Authorized
numeric fields are zero when not populated or inapplicable, never unknown NULL.

Native blocks are deterministic logical estimates, not PostgreSQL physical
pages: for first-capture version count N0, total is ceil(N0/128), fixed at scan
entry. Completed inspect/materialize prefixes use floor(prefix/128), except the
complete N0 prefix reports the total. Prefixes are capped at N0 and retain their
high-water values across real recaptures, including discarded detached work.
An empty extent reports zero; no materialization means zero vacuumed blocks.
Updates are batched at 128-version boundaries and flush the final prefix.
The current collection's exact retirement-eligible dead-version count is
`num_dead_item_ids`; `dead_tuple_bytes` estimates eight bytes per identifier,
and `max_dead_tuple_bytes` is eight times the current capture's version count.
Retirement-blocked versions do not enter that collection. Native `delay_time`
is always floating-point zero: there is no cost-delay scheduler or wall-time
substitution.

Phases follow actual native work: initializing, scanning heap, optional
vacuuming heap, optional vacuuming indexes, then performing final cleanup.
Materialization precedes index remapping; no synthetic phases are emitted.
Index gauges are zero outside an actual root-maintenance loop; inside it,
total counts only roots needing work and processed counts successful roots.
`index_vacuum_count` counts completed nonempty loops, not individual indexes,
including real freeze-only hash/BRIN work and completed recapture attempts.
Dead-item gauges reset when consumed or when a new collection starts; index
gauges reset on leaving index work. Only heap high-water estimates and completed
cycles are target-global monotone; phase/collection gauges may reset.

Rows start after admission and bound-storage checks, never during a pre-work
lock wait. They disappear on success, cancellation, failure, session end or
catalog replacement; late work cannot resurrect removed rows. Multi-target
commands expose their current target only. Retained results remain immutable;
progress is not persisted by Dump/Restore. Cancellation does not install a
candidate or record maintenance completion. INSERT/UPDATE/DELETE, direct or
prepared and even with a false predicate, reject with SQLSTATE55000 and the
standard non-automatically-updatable-view detail and matching trigger/rule hint.

## Physical base-backup progress

`pg_catalog.pg_stat_progress_basebackup` (also available unqualified on the
default path) is a read-only view with these nullable columns, in order:
`pid integer`, `phase text`, `backup_total bigint`, `backup_streamed bigint`,
`tablespaces_total bigint`, `tablespaces_streamed bigint`. It has PUBLIC SELECT
and genuinely returns zero rows: this engine has no physical `BASE_BACKUP`
WAL-sender workers. There is no all-NULL/zero placeholder and no row-order
promise. Ordinary SQL `BASE_BACKUP` rejects with syntax error 42601; it is a
replication-protocol command, not a logical SQL backup command. INSERT, UPDATE
and DELETE reject with 55000; TRUNCATE rejects with 42809.

Native logical `Dump` never publishes progress here, including while its output
writer is blocked, after completion, or after cancellation/failure. This view
adds no physical streaming, worker registry, estimates or approximated counters.
Its displayed PostgreSQL 18 definition describes the released physical view,
not an executable native backup worker or backing progress function.

For an actual PostgreSQL worker, superusers and roles with the privileges of
`pg_read_all_stats` or the worker's authenticated role can read its details.
Other observers retain its `pid`; the other five fields are NULL, not zero,
and the row is not removed. Native observers of every role see no rows because
there is no worker. Upstream phases are `initializing`, `waiting for checkpoint
to finish`, `estimating backup size`, `streaming database files`, `waiting for wal
archiving to finish`, and `transferring wal files`. Unknown `backup_total`, before
estimation or with `--no-estimate-size`, is NULL (the internal -1 is not exposed).
An estimate becomes available when file streaming starts and follows streamed
bytes if exceeded. `backup_streamed` counts file/WAL-transfer bytes and
`tablespaces_streamed` counts streamed tablespaces; zero is a real counter,
never an unknown/redacted sentinel. No native physical-work formula is used.


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

## Control-file information boundary

`pg_control_checkpoint()`, `pg_control_system()`, `pg_control_init()` and
`pg_control_recovery()` are zero-input scalar RECORD functions. Their PostgreSQL
18 OUT shapes flow through ordinary typed scalar, field, expansion and FROM
paths; `LIMIT 0` exposes metadata without reading physical state.
Computed members have unknown nullability. Unconstrained TEXT members report
`ColumnType.Length()` as `(1<<63-1, true)`; the other control-file output types
report `(0, false)`. None of these output types reports decimal precision/scale.
Native `ColumnType.ScanType()` follows the existing type codecs: XID is `[]byte`,
OID/INT4/INT8 are `int64`, TEXT/PG_LSN/RECORD are `string`, BOOL is `bool`, and
TIMESTAMPTZ is `time.Time`. These Go carriers do not change the SQL output types.

All four start with ordinary PUBLIC EXECUTE. Mutable routine ACLs are checked
live, including held prepared statements: revoked execution fails `42501`,
before an authorized physical demand fails `0A000` with
`<function_name> physical control-file access is unsupported in embedded mode`.
No cluster control file exists in the embedded engine, so no system identifier,
checkpoint LSN, recovery endpoint, initialization value or timestamp is invented.
Stock PostgreSQL instead reads its real cluster control file and returns a row.
Native Dump/Restore preserves stored view definitions and procedure ACL edits
through the existing logical catalog; stateless builtins need no separate
serialization, and no physical control-file state is dumped.



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

SQL-defined validators with inputs `(text[], oid)` can also be selected by
`VALIDATOR`. They execute in the current SQL statement; their non-NULL return
value is ignored, while NULL results or routine errors abort the definition or
option change together with transactional validator writes. This does not supply
a foreign handler: remote reads and writes still require a genuine compatible
registered provider.

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
