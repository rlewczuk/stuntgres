# Driver API

The public Go contract of package `stuntgres`. SQL behavior follows
[PostgreSQL 18](https://www.postgresql.org/docs/18/sql.html); native snapshot
controls are in [FIXTURES.md](FIXTURES.md).

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
The engine owns the [database and template contract](../internal/engine/README.md#cluster-and-logical-database-ownership).

## Physical sessions and pooling

Settings, `SET ROLE`/session authorization, SQL `BEGIN` blocks, SQL `PREPARE`
names, cursors, `LISTEN` subscriptions, TEMP objects, advisory locks, sequence
`currval` memory and the `random()`/`setseed` stream belong to one physical
connection. A `*sql.DB` may use any connection, so pin a `*sql.Conn` (or `*sql.Tx`)
for session-scoped work.

Returning a connection to the pool keeps all of that state: `ResetSession` is a
health check only, never an implicit `DISCARD` or rollback. Run `DISCARD ALL` on
the pinned connection before `Conn.Close` to clean it. Canceled statements and
ordinary SQL errors leave the connection reusable.

## Transactions

- `BeginTx` supports `sql.LevelReadCommitted`, `LevelReadUncommitted`,
  `LevelRepeatableRead` and `LevelSerializable`; `ReadOnly` maps to the access mode.
  Deferrability uses the session default (`sql.TxOptions` has no field for it).
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
- **Results are eager.** A statement finishes executing before `Query` returns, so
  `Rows.Next` never runs SQL and late errors never expose a partial result.
  Rows stay valid across later writes, commits and Reset/Restore. Returned byte
  slices are detached copies.
- `RowsAffected` counts rows actually inserted, updated, deleted or merged;
  `LastInsertId` is not supported.
- **Multi-statement strings.** A parameterless `ExecContext`/`QueryContext` may
  contain several statements. The whole string is parsed first, then commands run
  in order and stop at the first error. `Exec` reports the last command's count;
  `Query` returns one result set per row-producing command (`Rows.NextResultSet`).
  Calls with arguments and prepared statements accept exactly one statement.
- **SQL `PREPARE`/`EXECUTE`** names and cursors are session objects, independent of
  Go `Stmt` handles; closing a `Stmt` or `Rows` neither deallocates a name nor moves
  a cursor.

Routine rename/schema/owner DDL has no parameter/result signature; Prepare stays
effect-free and Execute owns authorization and read-only/autocommit admission.
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
| `json` | `string` (exact input text) |
| `jsonb`, `uuid`, network, geometric, bit strings, text search | canonical `string` |
| arrays, composites, ranges, multiranges, enums | canonical `string` |
| `reg*` types | symbolic `string` |

NULL is `nil`. A `nil` interface or typed-nil `[]byte` argument is NULL; a non-nil
empty `[]byte` is an empty value. `bytea` string and `[]byte` arguments are raw
octets. Time zone and Unicode data are embedded; no host locale is consulted.

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
