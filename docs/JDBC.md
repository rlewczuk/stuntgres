# stuntgres JDBC driver

The stuntgres JDBC driver lets a Java application run its unit tests against the
stuntgres engine inside the test JVM instead of against a PostgreSQL server. It is a
JDBC 4.3 driver (`org.stuntgres.Driver`) that reaches the engine through libspq
(`docs/LIBSPQ.md`) without any network connection, and it behaves towards the
application as the PostgreSQL JDBC driver (pgjdbc 42.7) behaves against PostgreSQL 18.

**Status.** This document is the consumer contract that the implementation must meet.
The driver is being built by the `JDBC-*` catalog parts; until they are done, treat every
statement here as the specification, not as shipped behavior. Internal design lives in
`docs/design/JDBC-NATIVE.md` (build, native layer, test harness), `JDBC-TYPES.md` (SQL
text and type conversion), `JDBC-CORE.md` (driver, connection, statements, result sets),
`JDBC-METADATA.md` (`DatabaseMetaData`) and `JDBC-FEATURES.md` (callable statements,
updatable result sets, large objects, SQLXML, COPY, notifications, pooled and XA data
sources, cluster control, the jar).

The driver is not a replacement for pgjdbc in production: there is no server, no
authentication and no TLS. It ships no class of package `org.postgresql`.

## 1. Getting the driver

| | |
|---|---|
| Maven coordinates | `org.stuntgres:stuntgres-jdbc` |
| Driver class | `org.stuntgres.Driver` (registered through `META-INF/services/java.sql.Driver`; `Class.forName` is not needed) |
| Data source | `org.stuntgres.ds.PGSimpleDataSource` |
| Java | 17 or newer (compiled with `--release 17`); HotSpot on Linux glibc, x86_64 and aarch64 |
| Runtime dependencies | none |

The jar is self-contained: it carries the engine library `libspq.so` and the driver's JNI
library for the build platform under `META-INF/native/linux-<arch>/`. They are extracted
at the first connection, never when a class is loaded. Two system properties change that:
`stuntgres.native.dir` names a directory that already holds both libraries (nothing is
extracted then), `stuntgres.native.cache` the directory to extract into. `make jdbc`
(section 10) writes the jar to `build/jdbc/stuntgres-jdbc-<version>.jar`; it is not
published to a repository.

At the first connection the driver compares its own version with the version the engine
library reports. When major or minor number differ (possible only with
`stuntgres.native.dir` pointing at another build) the connection fails with SQLState
`08001` and a message that names both versions and the library's path.

## 2. Connection URL

```
jdbc:stuntgres:[//[cluster][:port]/][database][?name=value[&name=value]...]
```

| URL | Meaning |
|---|---|
| `jdbc:stuntgres://shop/orders` | cluster `shop`, port 5432, database `orders` |
| `jdbc:stuntgres://shop:5433/orders?user=app` | another cluster: the port is part of the cluster's identity |
| `jdbc:stuntgres:orders` | cluster `localhost`, database `orders` |
| `jdbc:stuntgres://shop/` | database named like the user |

- **Cluster.** Host and port do not name a machine: together they name an engine
  instance inside the process (`docs/LIBSPQ.md` 5.1). Two connections with the same
  cluster and database see the same data; different clusters share nothing. Names are
  compared byte for byte and nothing is resolved. Of a comma-separated host list only
  the first entry counts.
- **Database** and **user** are created on first use. Nothing is authenticated: a
  password is accepted and ignored. The user defaults to `postgres` (not to the
  operating-system user), the database to the user name.
- Parameter names and values are percent-decoded. A parameter in the URL wins over the
  same key in the `Properties` object passed to `DriverManager.getConnection`.
- Nothing is read from the environment: no `PG*` variables, no service file, no
  password file. The same URL gives the same connection on every machine.
- `DriverManager` returns "no suitable driver" for any other URL prefix. The driver
  answers `jdbc:postgresql:` URLs only when the system property
  `stuntgres.jdbc.acceptPostgresqlUrls` is `true`; the grammar is the same. It never
  deregisters another driver: when pgjdbc is on the class path too, `DriverManager` asks
  the drivers in registration order and the first that accepts the URL connects, so
  remove pgjdbc from the test class path (or make sure this driver is registered first)
  when you use the opt-in.

## 3. Connection properties

Every connection property of pgjdbc 42.7 is accepted, in the URL or in the `Properties`
object, so that an existing configuration can be reused. `org.stuntgres.PGProperty`
lists them. Properties not named here have no effect; an unknown property is ignored.

| Property | Effect |
|---|---|
| `user`, `password` | section 2 |
| `ApplicationName` | `application_name` of the session; default `stuntgres JDBC Driver` |
| `currentSchema` | initial `search_path` |
| `options` | session options as in PostgreSQL (`-c name=value`) |
| `readOnly`, `readOnlyMode` | read-only connections (`ignore`, `transaction`, `always`) |
| `autosave`, `cleanupSavepoints` | `always` keeps a transaction usable after a failed statement; `conservative` behaves as `never` |
| `stringtype` | `unspecified` lets the server infer the type of `setString` parameters |
| `convertBooleanToNumeric`, `disableColumnSanitiser`, `quoteReturningIdentifiers`, `logServerErrorDetail`, `allowEncodingChanges` | as in pgjdbc |
| `queryTimeout`, `defaultRowFetchSize`, `loginTimeout` | defaults of statements and of the connect call |
| `datatype.<type>` | registers a `PGobject` subclass for a type |
| `escapeSyntaxCallMode` | how a `{call ...}` escape is sent: `select` (default), `callIfNoReturn`, `call` |
| `hideUnprivilegedObjects` | `DatabaseMetaData` lists only objects the user may use |
| `xmlFactoryFactory` | XML parser configuration of `SQLXML`: empty (secure defaults), `LEGACY_INSECURE`, or a class name |

Accepted without effect, among others: every TLS, GSS, authentication and socket
property (`ssl*`, `sslmode`, `gss*`, `socketTimeout`, `tcpKeepAlive`, `connectTimeout`
...), `prepareThreshold` and the statement-cache properties (the driver does not create
server-prepared statements implicitly), `preferQueryMode`, `binaryTransfer`,
`reWriteBatchedInserts` (a batch always reports exact update counts), `targetServerType`,
`loadBalanceHosts`. `replication` is refused.

`PGSimpleDataSource` has the bean properties of pgjdbc's class of the same name
(`setServerNames`, `setPortNumbers`, `setDatabaseName`, `setUser`, `setPassword`, `setUrl`,
and one getter and setter per connection property), so a connection pool configured with
`dataSourceClassName` works after the class name is changed.

## 4. Supported API

The reference is the JDBC 4.3 specification; where it leaves a choice, the driver does
what pgjdbc does, and what pgjdbc rejects is rejected the same way
(`SQLFeatureNotSupportedException`, SQLState `0A000`).

| Interface | State |
|---|---|
| `Driver`, `DataSource` | complete |
| `Connection` | complete. `createBlob`, `createClob`, `createNClob`, `createStruct` are rejected, as in pgjdbc. |
| `Statement` | complete: several statements in one string with all their results, batches, generated keys (`RETURN_GENERATED_KEYS` and column names; not column indexes), cancel, query timeout, warnings |
| `PreparedStatement` | complete: every setter of pgjdbc, metadata before execution and `ParameterMetaData` |
| `ResultSet` | forward-only and scrollable; every getter of pgjdbc. Updatable (`CONCUR_UPDATABLE`) when the result comes from one table and contains a primary key or a unique key over `NOT NULL` columns; `insertRow`, `updateRow`, `deleteRow`, `refreshRow` then work, and the changed row shows what the server stored. |
| `ResultSetMetaData`, `ParameterMetaData`, `Array`, `Savepoint` | complete |
| `DatabaseMetaData` | every method of pgjdbc with pgjdbc's columns and rows; `getSuperTypes`, `getSuperTables`, `getAttributes`, `getPseudoColumns`, `getRowIdLifetime` are rejected, as in pgjdbc |
| `CallableStatement` | `{call f(?)}`, `{? = call f(?)}` and native `CALL`/`SELECT` texts; `registerOutParameter` by index and the getters by index; ref cursors as result sets. Named parameters are rejected, as in pgjdbc. |
| `Blob`, `Clob` | over large objects (an `oid` column), inside a transaction: `getBlob`, `getClob`, `setBlob`, `setClob(int, Clob)`. A `bytea` column is read with `getBytes`. The writing methods of `Clob` are rejected, as in pgjdbc. |
| `SQLXML` | `createSQLXML`, `getSQLXML`, `setSQLXML`; string, stream, DOM, SAX and StAX access with the JDK's XML parsers, external entities and DTDs off by default |
| `ConnectionPoolDataSource`, `PooledConnection` | `org.stuntgres.ds.PGConnectionPoolDataSource` |
| `XADataSource`, `XAConnection`, `XAResource` | `org.stuntgres.xa.PGXADataSource`, over two-phase commit; suspend and resume and transaction interleaving are not supported, as in pgjdbc |

Extension API, under the names of pgjdbc with `org.postgresql` replaced by
`org.stuntgres`: `PGConnection`, `PGStatement`, `PGResultSetMetaData`, `PGProperty`,
`util.PSQLException`, `util.PSQLState`, `util.ServerErrorMessage`, `util.PSQLWarning`,
`util.PGobject`, `PGInterval`, `PGmoney`, the classes of `geometric`,
`ds.PGSimpleDataSource`, `ds.PGConnectionPoolDataSource`, `ds.PGPooledConnection`,
`xa.PGXADataSource`, `xa.PGXAConnection`, `copy.CopyManager` with `CopyIn`, `CopyOut`,
`PGCopyInputStream` and `PGCopyOutputStream`, `largeobject.LargeObjectManager` and
`LargeObject`, `PGNotification`, `core.BaseConnection` (as a type to cast to). Code that
imports `org.postgresql` classes has to change the package name; code that uses
`java.sql` only needs a new URL.

- **COPY.** `((PGConnection) connection).getCopyAPI()` or `new CopyManager((BaseConnection)
  connection)`. While a copy operation is open, other statements on the connection fail
  with SQLState `55000` (pgjdbc would block); end or cancel the copy first.
  `CopyOut.readFromCopy(false)` waits for the next row like `readFromCopy()`.
- **Notifications.** `PGConnection.getNotifications()` returns what has arrived;
  `getNotifications(millis)` waits up to that long, `getNotifications(0)` until one
  arrives.
- **Large objects.** `PGConnection.getLargeObjectAPI()`; they need autocommit off.

Not provided: the fastpath and replication APIs (`getFastpathAPI`, `getReplicationAPI`),
`CopyManager.copyDual`, `PGPoolingDataSource`, JNDI object factories and
`getReference()`, TLS, GSS and host-selection classes. `PGConnection.alterUserPassword`
exists and is rejected (`0A000`): nothing is authenticated.

Fetch size is a hint: `setFetchSize` is stored and returned, and every result is read
completely. `setMaxRows` and `setMaxFieldSize` work.

## 5. Errors

Every error is an `SQLException` with the SQLState pgjdbc would report. A server error is
an `org.stuntgres.util.PSQLException` whose message has pgjdbc's layout (`ERROR: ...`
followed by `Detail:`, `Hint:`, `Position:`, `Where:` lines) and whose
`getServerErrorMessage()` gives the fields. Notices arrive as `SQLWarning` chains on the
statement. A cancelled or timed-out statement fails with SQLState `57014`; the driver
never throws `SQLTimeoutException`. Messages of errors that the driver itself raises are
its own wording: match on SQLState, not on text.

## 6. Types

Type mapping is pgjdbc's: the class `getObject` returns for each PostgreSQL type, the
`java.sql.Types` code, the conversions of every getter and setter, `java.time` support,
arrays, and the `PGobject` family for types without a JDBC counterpart.
`docs/design/JDBC-TYPES.md` holds the tables. Values travel as text; the session always
uses `client_encoding` `UTF8` and ISO date style, and its time zone is the JVM's default
zone at connect time. A session that changes `client_encoding` or `DateStyle` is closed.

## 7. Transactions and threads

- Autocommit is on after connect. With autocommit off the driver opens a transaction
  before the first statement; `commit` and `rollback` end it. `BEGIN`, `COMMIT` and
  `ROLLBACK` sent as statements are understood. Isolation levels, read-only, savepoints
  and autosave work as in pgjdbc.
- A call that executes several statements, and a batch, is atomic in autocommit mode:
  when one statement fails, none of the call's changes is kept.
- `close()` of a connection with an open transaction rolls it back.
- A connection and the statements and result sets made from it may be shared by threads;
  the driver serializes their use with one lock per connection, so one thread at a time
  runs a statement. `Statement.cancel`, `Connection.abort` and `isClosed` do not wait
  for that lock. Different connections are independent.

## 8. JVM requirements

The driver runs on HotSpot JDK 17 and 21 with every collector (G1, Parallel, Serial, ZGC,
Shenandoah) and needs no JVM option. Do not pass `-XX:-UseSignalChaining` or
`-XX:+AllowUserSignalHandlers`; they were not tested. A preloaded `libjsig.so` is not
needed and does no harm. Under `-Xcheck:jni` the JVM prints `Warning: SIGSEGV handler
modified!` (and the same for SIGBUS, SIGFPE, SIGILL, SIGUSR2 and, on JDK 17, SIGPIPE and
SIGXFSZ) once after the first connection, because the engine's runtime installs signal
handlers; the warnings are harmless. `-Xrs` works. Shutdown hooks run on normal exit,
`System.exit` and SIGTERM while connections are open. A thread that waits for the
database is inside a native call: it cannot be interrupted with `Thread.interrupt`; use
`Statement.cancel` or a query timeout. A virtual thread that calls the driver pins its
carrier thread for the duration of the call. On JDK 24 and later add
`--enable-native-access=ALL-UNNAMED` (class path) to avoid the JVM's warning about native
access.

Further rules:

- **Class loaders.** The driver may be loaded by several class loaders of one JVM
  (application servers, test runners). The engine library is still mapped once per
  process, so all of them share the same clusters.
- **No fork.** The engine does not survive `fork()` (`docs/LIBSPQ.md` section 7): do not
  use the driver in a forked copy of a process that has opened a connection; start child
  JVMs normally.
- The engine is never unloaded: its memory is released when the process ends. Close
  connections you no longer need; a connection that becomes unreachable is closed by the
  driver, but not at a predictable time.

## 9. Test isolation

Each cluster is a separate engine instance, so giving every test class its own cluster
name in the URL isolates it completely, and two tests that use the same URL share data
on purpose. The helpers below exist only with the engine library; they are not part of
pgjdbc's API.

`org.stuntgres.IsolatedCluster` owns a cluster name nobody else uses and drops the
cluster when it is closed. It depends on no test framework:

```java
try (IsolatedCluster cluster = IsolatedCluster.create();
     Connection connection = cluster.connect()) {
    // an empty engine; cluster.url() is its JDBC URL (database "test"),
    // cluster.url("orders") the URL of another database of the same cluster
}
```

In JUnit 5 keep it in a field and close it in `@AfterEach` or `@AfterAll`; in JUnit 4 in
`@After`. `close()` closes the connections the helper handed out and then drops the
cluster; a connection opened elsewhere on the URL and still open makes the drop fail
(SQLState `55006`).

| Call | Effect |
|---|---|
| `Stuntgres.dropCluster(name)`, `dropCluster(name, port)` | forget the cluster; the next connection to the name gets a new, empty one. Fails while it has open connections. This also releases its memory. |
| `((PGConnection) connection).resetDatabase()` | remove all objects and data of the connected database; the connection, the cluster's roles and its other databases stay |
| `byte[] dump()`, `restore(byte[])` on `PGConnection` | serialize the connected database, or replace it with a dump: load a schema once, dump it, and restore it before each test |
| `Stuntgres.nativeVersion()` | the version of the engine library |

`resetDatabase`, `dump` and `restore` need a connection outside a transaction (SQLState
`25001` otherwise). After `resetDatabase` or `restore`, open a new connection if the
application uses user-defined types: a connection caches what it learned about them.

## 10. Building and testing

From the repository root:

```
make jdbc          # libspq, the JNI library and build/jdbc/stuntgres-jdbc-<version>.jar
make jdbc-check    # the jar, the unit and native test suites, and a smoke test of the jar alone
make jdbc-check REFERENCE=1 SERVER=1    # also the reference, oracle and server modes
```

Neither target is part of `make quickcheck`. They need a JDK 17 or newer (`JAVA_HOME`),
Maven with its plugins in the local repository (the build is offline), a C compiler and,
for libspq, cgo. The single modes run through the Makefile of `bindings/jdbc` (Maven is
called by make):

```
make -C bindings/jdbc check MODE=unit         # no database
make -C bindings/jdbc check MODE=reference    # the test bodies through pgjdbc against PostgreSQL 18.6
make -C bindings/jdbc check MODE=oracle       # this driver through libpq against PostgreSQL 18.6
make -C bindings/jdbc check MODE=server       # this driver through libpq against cmd/stuntgres
make -C bindings/jdbc check MODE=native       # this driver through libspq, the engine in-process
make -C bindings/jdbc check MODE=server TEST=StmtRunTest
```

Reference and oracle mode need Docker; native mode needs a built libspq. A test of
`java.sql` behavior runs unchanged in every mode, which is how the driver is held to
pgjdbc's behavior. `docs/design/JDBC-NATIVE.md` section 10 describes the harness. Runs
that start the stuntgres server or the engine are wrapped in a memory limit, for example
`systemd-run --user --scope -p MemoryMax=16G -p MemorySwapMax=0 timeout 900 make -C
bindings/jdbc check MODE=server`.
