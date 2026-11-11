# JDBC-CORE: driver, connection, statements and result sets of the JDBC driver

Status: **specification**. Nothing described here exists yet; the parts of section 13
implement it. Produced by catalog part JDBC-DESIGN-CORE on 2026-11-11, on top of
`docs/design/JDBC-NATIVE.md` (cited as NATIVE) and `docs/design/JDBC-TYPES.md` (cited as
TYPES). Behavior target: pgjdbc 42.7.13 against PostgreSQL 18.6 (plan J1).

How to read it. This is a specification, not source code: per class it gives the file, the
place in the class chain, the signatures other parts call, the behavior rules, the SQL
the driver sends, and the exception class and SQLState of every error. An implementation
part plans its algorithm itself and establishes expected values it is not given in
reference mode (rule R11 of the parts). Marks: **(probe)** was observed in one of the
three probes of section 14; **(map)** is taken from the pgjdbc source map
(`.cswd/tmp/JDBC/research/pgjdbc-map.md`) and is confirmed by the implementing part in
reference mode; unmarked rules are decisions of this design. "pgjdbc:" names the source
file of pgjdbc to read for behavior (under
`../src-ref/pgjdbc/pgjdbc/src/main/java/org/postgresql/`); nothing is copied from it.

Paths are relative to `bindings/jdbc`; Java packages are below `src/main/java/org/stuntgres`
and `src/test/java/org/stuntgres`. Part IDs are written without the prefix `JDBC-`.

## 1. Purpose, layering, decisions, layout

### 1.1 Layering

```
org.stuntgres.Driver, PGProperty, ds.*        URL, properties, registration, DataSource      (section 3)
org.stuntgres.jdbc.*                          PgConnection, PgStatement, PgPreparedStatement,
        │                                     PgResultSet, metadata, PgArray, PSQLSavepoint  (5 to 11)
org.stuntgres.util                            PSQLException, ServerErrorMessage, PSQLWarning (4)
org.stuntgres.core   (TYPES)                  NativeSql, JdbcEscapes, TypeTable, getters, setters;
        │                                     this design adds DriverUrl and ConninfoBuilder
org.stuntgres.pq     (NATIVE)                 PqConnection, PqResult, PqCancel, PqNotice
```

The JDBC classes call `org.stuntgres.pq` for every exchange with the database and
`org.stuntgres.core` for every conversion. They hold no native memory: a `PqResult` is a
heap copy (NATIVE 6.1), the only native handles are the connection and its cancel handle.

### 1.2 Decisions

The open points handed to this design, and the points where a probe changed the fixed
design points of the part record.

1. **One execution path, extended protocol.** Every statement of every `execute` call,
   with or without parameters, is sent on its own with `PqConnection.execParams`. The part
   record asked for one simple query for a statement string without parameters; pgjdbc's
   result sequence cannot be reproduced that way **(probe A5: `SELECT 1; /* only a comment
   */; INSERT ...; ; SELECT 2` gives result set, count 0, count 1, result set)**, and one
   path is simpler than two. `sendQuery` and `execAll` are not used by this layer.
2. **A call with several statements is atomic in autocommit mode**, and so is a batch
   **(probe A5, A7: after a failure in the middle nothing of the call is stored)**. pgjdbc
   gets this from one protocol Sync; the driver sends `BEGIN` before the first statement
   and `COMMIT` or `ROLLBACK` after the last (6.1).
3. **Failed connect.** libpq and libspq give message text only. `Errors.connectFailure`
   maps the text to the SQLState pgjdbc reports **(probe A3)** by the table of 4.3; the
   message is the library text unchanged. With libspq only two rows of that table can
   occur (a rejected `options` value, a role without LOGIN): databases and roles are
   created on demand and nothing is authenticated (`docs/LIBSPQ.md` 5.1).
4. **NUL in statement text** raises `PSQLException` `08P01` before anything is sent
   (pgjdbc: the server answers `08P01` **(probe A4)**). Difference that stays: with pgjdbc
   the server error aborts an open transaction; here the transaction is untouched. No test
   asserts the transaction state after this error.
5. **Properties** (3.3): all 92 pgjdbc properties are known; 21 are honoured, 3 are stored for a later design, 67 are accepted and ignored, 1 is rejected when set (`replication`). In particular: `reWriteBatchedInserts` and `reWriteBatchedInsertsSize`
   are ignored (a batch always returns exact counts, never `SUCCESS_NO_INFO`);
   `quoteReturningIdentifiers` is honoured; `preferQueryMode` is validated, reported by
   `PGConnection.getPreferQueryMode()` and otherwise ignored (decision 1 is the only
   execution model); `prepareThreshold` and the statement cache properties are ignored
   (plan B8). An unknown property is ignored, as in pgjdbc.
6. **`Statement.setMaxFieldSize`** is honoured: `getString`, `getBytes` and `getObject`
   cut the value of a character or binary column **(probe A6: text, varchar, name and
   bytea are cut, int4 is not)** (10.4).
7. **`PGConnection.addDataType`** is honoured: a registry per connection of type name to
   `PGobject` subclass, consulted by `getObject` (10.4); properties named
   `datatype.<type>` fill it at connect.
8. **URL** (3.1): the forms pgjdbc accepts for `jdbc:postgresql:` **(probe A1)** with the
   prefix `jdbc:stuntgres:`. The form `jdbc:stuntgres:/`, which the part record lists, is
   rejected, as pgjdbc's parser rejects it. The harness form
   `jdbc:stuntgres://<host>:<port>/<db>` with the properties `user` and `password`
   (hand-over point 4) is accepted.
9. **Default user**: `postgres` when neither the URL nor the properties name one, which is
   libspq's default made explicit, so that a real libpq behaves the same. The driver reads
   no environment variable, no service file, no password file and not `user.name` (pgjdbc
   uses the operating-system user). The database defaults to the user name, the cluster
   to `localhost`, the port to `5432`.
10. **Session setup** is done in the connection string, not by statements after connect
    (3.2): `client_encoding=UTF8`, and `options` with `-c DateStyle=ISO`, `-c
    TimeZone=<JVM zone>`, `-c search_path=...` for `currentSchema`. The session then
    reports what a pgjdbc session reports **(probe A2, B: `ISO, MDY`, the JVM zone, `UTF8`,
    `extra_float_digits` 1 untouched)**. After every execution the driver checks that
    `client_encoding` is still `UTF8` and `DateStyle` still starts with `ISO`, and closes
    the connection with `08006` otherwise, as pgjdbc does (5.2).
11. **Lock**: one `java.util.concurrent.locks.ReentrantLock` per connection, not fair,
    taken with `lock()` (5.1). Reentrant because operations nest (a getter runs a catalog
    lookup; closing a statement closes its result sets). Not `synchronized`: the lock
    object is shared by all objects of the connection and must not be the monitor of an
    object applications can reach. A virtual thread is pinned while it is inside a native
    call whatever the lock type (NATIVE 2.2); documented only.
12. **Query timeout and cancel** end with the server's error `57014` as a
    `PSQLException`; `SQLTimeoutException` is never thrown **(map 9)**.
13. **Message texts** are the driver's own (TYPES decision 4) except the three "closed"
    messages of 4.4 and server messages, which are passed through in pgjdbc's layout.
    Tests that also run in reference mode compare exception class and SQLState only.
14. **Autosave** uses the savepoint name `STUNTGRES_AUTOSAVE`. The mode `conservative`
    behaves as `never`: pgjdbc rolls back to the savepoint in that mode only for errors of
    server-prepared statements, which this driver does not use (plan B8).
15. **`PreparedStatement.toString()`** returns the SQL text as given to
    `prepareStatement` (pgjdbc inlines parameter literals). Not tested in reference mode.
16. **`JDBC-TEST-03`** is not a connection factory: `TestDatabase.connect()` of
    TEST-01A already goes through `DriverManager` (NATIVE 10.6). The part provides
    assertion helpers instead. **`JDBC-STMT-05-WARNINGS`** of the part record is dropped:
    the warning chains are state of the base classes (STMT-01A, CONN-01A, RS-01A).
    **`JDBC-TYPE-03-LOOKUP`** is named `JDBC-TYPE-04-LOOKUP` (the number 03 is taken).
17. **No engine gap was found**: the fixed SQL of this layer gives the same answers on
    PostgreSQL 18.6 and on the stuntgres server **(probe B)**. No `JDBC-ENGINE` part.
18. **Not validated** (waived by the lean-design directive of 2026-11-11): nothing of this
    design ran as code. Rule D7 of the part record does not apply.

### 1.3 Class chains and extension points

Plan B14: a class too large for one part is a chain of abstract classes, one file per
part. Every chain has the same shape:

- A **root** class, generated once from the interface (1.5 rule 2): it implements every
  method by raising `SQLFeatureNotSupportedException` `0A000` (`Errors.notImplemented`).
  A method that pgjdbc rejects stays there for good; every other method is overridden
  further down the chain.
- **Chain classes**: package-private abstract classes, each `extends` the one above. The
  skeleton part of a chain creates all of them as *stubs* (the class with its constructor
  and nothing else); each later part replaces exactly its own stub file. Parts therefore
  never share a file, and the order of the chain is fixed here.
- The **concrete class** at the end: public, constructor only, created by the skeleton
  part and never edited again.
- **Extension stubs** for later designs sit directly above the concrete class. A later
  design owns such a stub: it replaces that one file, and when it needs several files it
  lets the stub extend new classes of its own. It edits nothing else.

```
ConnectionRoot                     generated                                          CONN-00
└ ConnCore        open, close, isClosed, lock, pq access, warnings                    CONN-01A
  └ ConnOwnSql      own statements, execution hooks, session checks, context            CONN-01B
    └ ConnTxn     autocommit, implicit BEGIN, commit, rollback                        CONN-02
      └ ConnTxnOptions   isolation level, read-only                                   CONN-03
        └ ConnSavepoints     savepoints                                               CONN-04A
          └ ConnAutosave     autosave                                                 CONN-04B
            └ ConnSettings    schema, catalog, client info, type map, holdability, nativeSQL   CONN-05
              └ ConnValid    isValid, abort, network timeout                          CONN-06
                └ ConnArrays     createArrayOf, makeArray                             ARRAY-03
                  └ ConnFactories    createStatement, prepareStatement                CONN-07
                    └ ConnExtensions     PGConnection methods of this design, unwrap  CONN-08
                      └ ConnMetaData     stub: getMetaData            → JDBC-DESIGN-METADATA
                        └ ConnFeatures   stub: prepareCall, createSQLXML, the other
                          │              PGConnection methods         → JDBC-DESIGN-FEATURES
                          └ PgConnection (public final; implements Connection, PGConnection)

StatementRoot                      generated                                          STMT-00
└ StmtCore        state, checks, close, warnings                                      STMT-01A
  └ StmtRun       the execution path, execute(String)                                 STMT-01B
    └ StmtExecute     executeQuery, executeUpdate, executeLargeUpdate                 STMT-01C
      └ StmtResults       getResultSet, getUpdateCount, getMoreResults                STMT-02
        └ StmtLimits      limits, hints, poolable, closeOnCompletion                  STMT-03
          └ StmtCancel        cancel, query timeout                                   STMT-04
            └ StmtBatch       addBatch, clearBatch, executeBatch                      BATCH-01
              └ StmtKeys      generated keys                                          KEYS-01
                └ StmtExtensions   PGStatement, unwrap                                STMT-05
                  └ StmtFeatures   stub                               → JDBC-DESIGN-FEATURES
                    └ PgStatement (public; implements Statement, PGStatement)
                      └ PreparedRoot           generated                              PSTMT-00
                        └ PsCore     parameter store, execution, forbidden methods    PSTMT-01
                          └ PsBasicSetters                                            PSTMT-02
                            └ PsDateTimeSetters                                       PSTMT-03
                              └ PsStreamSetters                                       PSTMT-04
                                └ PsObjectSetters                                     PSTMT-05
                                  └ PsBatch      addBatch()                           BATCH-02
                                    └ PsMetaData     getMetaData, getParameterMetaData   PSTMT-90
                                      └ PsFeatures   stub: setBlob, setClob(Clob),
                                        │            setSQLXML        → JDBC-DESIGN-FEATURES
                                        └ PgPreparedStatement (public; implements PreparedStatement)
                                          (JDBC-DESIGN-FEATURES extends it for CallableStatement)

ResultSetRoot                      generated                                          RS-00A
└ RsCore          data, position, next, close, lifetime                               RS-01A
  └ RsScroll      scrolling, fetch hints                                              RS-01B
    └ RsColumns       findColumn, wasNull, value access                               RS-02
      └ RsTextGetters                                                                 RS-03
        └ RsNumberGetters                                                             RS-04
          └ RsDateTimeGetters                                                         RS-05
            └ RsObjectGetters     getObject, getArray                                 RS-06
              └ RsReadOnlyUpdates     the update methods, funnelled into two hooks    RS-90
                └ RsFeatures      stub: getBlob, getClob, getSQLXML, refcursor,
                  │               updatable result sets               → JDBC-DESIGN-FEATURES
                  └ PgResultSet (public final; implements ResultSet)

ResultSetMetaDataRoot              generated                                          RS-00A
└ RsmdBasic       answers from the column descriptors and the type table              RSMD-01
  └ RsmdCatalog   answers that need the catalog; PGResultSetMetaData                  RSMD-02
    └ PgResultSetMetaData (public final; implements ResultSetMetaData, PGResultSetMetaData)

ds.common: DsStore (DRIVER-04A) └ DsHonoured (04B) └ DsIgnoredTransport (04C)
           └ DsIgnoredTuning (04D) └ BaseDataSource (public abstract, 04A);
ds: PGSimpleDataSource extends BaseDataSource (04A)
```

Single-file classes need no chain: `PgArray`, `PSQLSavepoint`, `PgParameterMetaData`,
`CatalogTypeLookup` (a stub from CONN-00, replaced by TYPE-04), `Driver`, `PGProperty`.

What the later designs get:

| Design | Extension point | Notes |
|---|---|---|
| JDBC-DESIGN-METADATA | stub `jdbc/ConnMetaData.java` (`getMetaData()`) | Synthetic result sets are built with `MemoryResultData` (10.1); catalog queries run through ordinary statements or `ConnOwnSql.queryOwn`. |
| JDBC-DESIGN-FEATURES | stubs `jdbc/ConnFeatures.java`, `StmtFeatures.java`, `PsFeatures.java`, `RsFeatures.java` | `RsFeatures` overrides the hooks `checkUpdateable`, `updateValue` and `featureObject` (10.4, 10.5); `PgCallableStatement` extends `PgPreparedStatement`. |
| JDBC-DESIGN-FEATURES | two files it may edit, each by one part with an ordering edge to the owner named: `org/stuntgres/PGConnection.java` (CONN-08; to add `getCopyAPI`, `getLargeObjectAPI`, `getNotifications`, `getFastpathAPI`, `getReplicationAPI`, `alterUserPassword`) and `core/DriverUrl.java` (DRIVER-02; the `jdbc:postgresql:` opt-in of plan J12 changes the constant list of accepted prefixes) | Everything else is added as new files. |
| JDBC-DESIGN-FEATURES | `BaseDataSource` does not implement `javax.naming.Referenceable` (module `java.naming` is not used by this design) | To be decided there together with the pooled and XA data sources. |

### 1.4 Files and owner parts

The table of 13.1 lists every file with its owner part (column Files). In it, "stub;
owner X" means: created here as a stub and replaced as a whole by X; "replaces the stub of
X": the reverse; "generated" marks the files of 1.5 rule 2. Paths without a directory
prefix are below `src/main/java/org/stuntgres/`, those starting with `test:` below
`src/test/java/org/stuntgres/`. Summary by package:

| Package | Classes |
|---|---|
| `org.stuntgres` | `Driver`, `PGProperty`, `PGConnection`, `PGStatement`, `PGResultSetMetaData` |
| `org.stuntgres.util` | `PSQLException`, `ServerErrorMessage`, `PSQLWarning` (`PSQLState` and the value classes are TYPES) |
| `org.stuntgres.core` | `DriverUrl`, `ConninfoBuilder` |
| `org.stuntgres.ds`, `.ds.common` | `PGSimpleDataSource`; `BaseDataSource` and its chain |
| `org.stuntgres.jdbc` | the chains of 1.3, `Errors`, `AutoSave`, `PreferQueryMode`, `PSQLSavepoint`, `PgArray`, `PgParameterMetaData`, `CatalogTypeLookup`, `ColumnInfo`, `ResultData`, `PqResultData`, `MemoryResultData`, `StmtResult`, `BatchRunner`, `CancelTimer` |
| test tree | `testing/JdbcAsserts`, `pq/PqFixtures`, `core/RoundTrip`, `core/JdbcSetterCalls`, one test class per part as 13.1 names |

### 1.5 Rules for every class of this layer

1. **Lock.** Every public method of a connection, statement, result set, metadata object
   or array that reads or writes mutable state, or that reaches the pq layer, runs as
   `connection.lock.lock(); try { ... } finally { connection.lock.unlock(); }`. Outside
   the lock by design: `Connection.isClosed`, `Statement.cancel`, `PGConnection.cancelQuery`,
   the first half of `Connection.abort`, the timer thread (7.6). Package-private methods
   documented as "lock held" take no lock and are called only from inside it.
2. **Generated roots.** `ConnectionRoot`, `StatementRoot`, `PreparedRoot`, `ResultSetRoot`
   and `ResultSetMetaDataRoot` are written once by the skeleton part with a throwaway
   script (kept under `.cswd/tmp/<PART-ID>/`, never committed) from the output of `javap`
   for the interface on JDK 17: one method per abstract method of the interface, the two
   it inherits from `java.sql.Wrapper` included (the concrete class must compile from the
   skeleton part on; the part named in section 2 overrides them), and per default method
   that section 2 does not mark "JDK default", each with the body
   `throw Errors.notImplemented(<concrete class>.class, "<name>(<parameter types>)");`.
   These files are data tables in the sense of rule R6 and are exempt from the 500-line
   limit; they carry a class comment that says how they were generated and are never
   edited by hand afterwards. A method of the interface that is deprecated (`getUnicodeStream`,
   `getBigDecimal(int, int)`, `setUnicodeStream`) is declared with `@Deprecated`, in the
   root and in the class that overrides it: javac reports `[deprecation]` for an override
   without it **(probe C)**.
3. **Constructors** of chain classes take the arguments this document gives, pass them
   up, initialize their own fields and call no overridable method (TYPES decision 5;
   `final` and `static` methods are fine). A stub is the class declaration, the
   constructor and a one-line class comment naming the owner part.
4. **Visibility.** Roots and chain classes are package-private and abstract; the concrete
   classes are public. javac generates bridge methods, so the public methods inherited
   from package-private classes can be called through the public class, also by
   reflection **(probe C)**. Fields that other chains read are package-private and are
   listed in the "internal interface" tables; everything else is private.
5. **Hooks.** A method described as a hook has a default body in the class that declares
   it and is overridden by a later chain class; the caller always calls the hook. This is
   how a part compiles and runs before the parts below it in the chain exist.
6. **Errors.** A `PqException` never leaves this layer: it is mapped with
   `ConnCore.failure` (5.1). The client-side errors are those of 4.4, with exactly that
   class and SQLState.
7. **JDK modules**: `java.base` and `java.sql` (which brings `java.logging` and
   `java.xml`). Nothing else.

## 2. Method inventory

Every method of the Java 17 interfaces this design covers, with one verdict (generated by
`.cswd/tmp/JDBC-DESIGN-CORE/parts/inv.py` from `javap` of JDK 17.0.13; the audit checks
that every method appears once):

- a part ID: implemented by that part, in the section this document gives for it;
- **METADATA**, **FEATURES**: left to JDBC-DESIGN-METADATA or JDBC-DESIGN-FEATURES; until
  then the root answers `0A000`;
- **rejected**: pgjdbc does not implement the method (`Driver.notImplemented`,
  **(map 5)**); the root's `SQLFeatureNotSupportedException` `0A000` (`Method
  <class>.<method> is not yet implemented.`) is the final answer, for every argument;
- **JDK default**: a default method of the interface that pgjdbc does not override; the
  root does not declare it.

Default methods that are overridden, as in pgjdbc: `Statement.getLargeUpdateCount`,
`executeLargeBatch`, `executeLargeUpdate` (four forms), `setLargeMaxRows`, `getLargeMaxRows`
(the last two rejected); `PreparedStatement.executeLargeUpdate()` and `setObject` with an
`SQLType` (two forms, rejected); `ResultSet.updateObject` with an `SQLType` (four forms,
rejected). `Wrapper.unwrap` and `isWrapperFor` are implemented per class:
`PgConnection` in CONN-08-WRAPPER; `PgStatement, PgPreparedStatement` in STMT-05-EXTENSIONS; `PgResultSet` in RS-01A-FORWARD; `PgResultSetMetaData` in RSMD-01-BASIC; `PgParameterMetaData` in PSTMT-90-METADATA; `PGSimpleDataSource` in DRIVER-04A-DATASOURCE.

**`Driver`** (7 methods: 7 implemented)

| Verdict | Methods |
|---|---|
| DRIVER-03-DRIVER | `connect(String, Properties)`, `acceptsURL(String)`, `getPropertyInfo(String, Properties)`, `getMajorVersion()`, `getMinorVersion()`, `jdbcCompliant()`, `getParentLogger()` |

**`Connection`** (58 methods: 43 implemented, 4 FEATURES, 1 METADATA, 4 rejected, 6 JDK default)

| Verdict | Methods |
|---|---|
| CONN-07-FACTORIES | `createStatement()`, `prepareStatement(String)`, `createStatement(int, int)`, `prepareStatement(String, int, int)`, `createStatement(int, int, int)`, `prepareStatement(String, int, int, int)`, `prepareStatement(String, int)`, `prepareStatement(String, int[])`, `prepareStatement(String, String[])` |
| FEATURES | `prepareCall(String)`, `prepareCall(String, int, int)`, `prepareCall(String, int, int, int)`, `createSQLXML()` |
| CONN-05-SESSION | `nativeSQL(String)`, `setCatalog(String)`, `getCatalog()`, `getTypeMap()`, `setTypeMap(Map)`, `setHoldability(int)`, `getHoldability()`, `setClientInfo(String, String)`, `setClientInfo(Properties)`, `getClientInfo(String)`, `getClientInfo()`, `setSchema(String)`, `getSchema()` |
| CONN-02-TXN | `setAutoCommit(boolean)`, `getAutoCommit()`, `commit()`, `rollback()` |
| CONN-01A-OPEN-CLOSE | `close()`, `isClosed()`, `getWarnings()`, `clearWarnings()` |
| METADATA | `getMetaData()` |
| CONN-03-TXN-OPTIONS | `setReadOnly(boolean)`, `isReadOnly()`, `setTransactionIsolation(int)`, `getTransactionIsolation()` |
| CONN-04A-SAVEPOINT | `setSavepoint()`, `setSavepoint(String)`, `rollback(Savepoint)`, `releaseSavepoint(Savepoint)` |
| rejected | `createClob()`, `createBlob()`, `createNClob()`, `createStruct(String, Object[])` |
| CONN-06-VALID | `isValid(int)`, `abort(Executor)`, `setNetworkTimeout(Executor, int)`, `getNetworkTimeout()` |
| ARRAY-03-JDBC | `createArrayOf(String, Object[])` |
| JDK default | `beginRequest()`, `endRequest()`, `setShardingKeyIfValid(ShardingKey, ShardingKey, int)`, `setShardingKeyIfValid(ShardingKey, int)`, `setShardingKey(ShardingKey, ShardingKey)`, `setShardingKey(ShardingKey)` |

**`Statement`** (54 methods: 48 implemented, 2 rejected, 4 JDK default)

| Verdict | Methods |
|---|---|
| STMT-01C-QUERY-UPDATE | `executeQuery(String)`, `executeUpdate(String)`, `executeLargeUpdate(String)` |
| STMT-01A-STATE | `close()`, `getWarnings()`, `clearWarnings()`, `getConnection()`, `isClosed()` |
| STMT-03-LIMITS | `getMaxFieldSize()`, `setMaxFieldSize(int)`, `getMaxRows()`, `setMaxRows(int)`, `setEscapeProcessing(boolean)`, `setCursorName(String)`, `setFetchDirection(int)`, `getFetchDirection()`, `setFetchSize(int)`, `getFetchSize()`, `getResultSetConcurrency()`, `getResultSetType()`, `getResultSetHoldability()`, `setPoolable(boolean)`, `isPoolable()`, `closeOnCompletion()`, `isCloseOnCompletion()` |
| STMT-04-CANCEL | `getQueryTimeout()`, `setQueryTimeout(int)`, `cancel()` |
| STMT-01B-RUN | `execute(String)` |
| STMT-02-RESULTS | `getResultSet()`, `getUpdateCount()`, `getMoreResults()`, `getMoreResults(int)`, `getLargeUpdateCount()` |
| BATCH-01-STATEMENT | `addBatch(String)`, `clearBatch()`, `executeBatch()`, `executeLargeBatch()` |
| KEYS-01-GENERATED | `getGeneratedKeys()`, `executeUpdate(String, int)`, `executeUpdate(String, int[])`, `executeUpdate(String, String[])`, `execute(String, int)`, `execute(String, int[])`, `execute(String, String[])`, `executeLargeUpdate(String, int)`, `executeLargeUpdate(String, int[])`, `executeLargeUpdate(String, String[])` |
| rejected | `setLargeMaxRows(long)`, `getLargeMaxRows()` |
| JDK default | `enquoteLiteral(String)`, `enquoteIdentifier(String, boolean)`, `isSimpleIdentifier(String)`, `enquoteNCharLiteral(String)` |

**`PreparedStatement`** (58 methods: 37 implemented, 16 rejected, 5 FEATURES)

| Verdict | Methods |
|---|---|
| PSTMT-01-STORE | `executeQuery()`, `executeUpdate()`, `clearParameters()`, `execute()`, `executeLargeUpdate()` |
| PSTMT-02-BASIC-SETTERS | `setNull(int, int)`, `setBoolean(int, boolean)`, `setByte(int, byte)`, `setShort(int, short)`, `setInt(int, int)`, `setLong(int, long)`, `setFloat(int, float)`, `setDouble(int, double)`, `setBigDecimal(int, BigDecimal)`, `setString(int, String)`, `setBytes(int, byte[])`, `setNull(int, int, String)` |
| PSTMT-03-DATETIME-SETTERS | `setDate(int, Date)`, `setTime(int, Time)`, `setTimestamp(int, Timestamp)`, `setDate(int, Date, Calendar)`, `setTime(int, Time, Calendar)`, `setTimestamp(int, Timestamp, Calendar)` |
| PSTMT-04-STREAM-SETTERS | `setAsciiStream(int, InputStream, int)`, `setUnicodeStream(int, InputStream, int)`, `setBinaryStream(int, InputStream, int)`, `setCharacterStream(int, Reader, int)`, `setBinaryStream(int, InputStream, long)`, `setBinaryStream(int, InputStream)`, `setCharacterStream(int, Reader)` |
| PSTMT-05-OBJECT-SETTERS | `setObject(int, Object, int)`, `setObject(int, Object)`, `setArray(int, Array)`, `setObject(int, Object, int, int)` |
| BATCH-02-PREPARED | `addBatch()` |
| rejected | `setRef(int, Ref)`, `setURL(int, URL)`, `setRowId(int, RowId)`, `setNString(int, String)`, `setNCharacterStream(int, Reader, long)`, `setNClob(int, NClob)`, `setClob(int, Reader, long)`, `setNClob(int, Reader, long)`, `setAsciiStream(int, InputStream, long)`, `setCharacterStream(int, Reader, long)`, `setAsciiStream(int, InputStream)`, `setNCharacterStream(int, Reader)`, `setClob(int, Reader)`, `setNClob(int, Reader)`, `setObject(int, Object, SQLType, int)`, `setObject(int, Object, SQLType)` |
| FEATURES | `setBlob(int, Blob)`, `setClob(int, Clob)`, `setBlob(int, InputStream, long)`, `setSQLXML(int, SQLXML)`, `setBlob(int, InputStream)` |
| PSTMT-90-METADATA | `getMetaData()`, `getParameterMetaData()` |

**`ResultSet`** (193 methods: 130 implemented, 57 rejected, 6 FEATURES)

| Verdict | Methods |
|---|---|
| RS-01A-FORWARD | `next()`, `close()`, `getWarnings()`, `clearWarnings()`, `getCursorName()`, `getMetaData()`, `isBeforeFirst()`, `isAfterLast()`, `isFirst()`, `isLast()`, `getRow()`, `getType()`, `getConcurrency()`, `getStatement()`, `isClosed()` |
| RS-02-COLUMNS | `wasNull()`, `findColumn(String)` |
| RS-03-TEXT-GETTERS | `getString(int)`, `getBytes(int)`, `getAsciiStream(int)`, `getUnicodeStream(int)`, `getBinaryStream(int)`, `getString(String)`, `getBytes(String)`, `getAsciiStream(String)`, `getUnicodeStream(String)`, `getBinaryStream(String)`, `getCharacterStream(int)`, `getCharacterStream(String)` |
| RS-04-NUMBER-GETTERS | `getBoolean(int)`, `getByte(int)`, `getShort(int)`, `getInt(int)`, `getLong(int)`, `getFloat(int)`, `getDouble(int)`, `getBigDecimal(int, int)`, `getBoolean(String)`, `getByte(String)`, `getShort(String)`, `getInt(String)`, `getLong(String)`, `getFloat(String)`, `getDouble(String)`, `getBigDecimal(String, int)`, `getBigDecimal(int)`, `getBigDecimal(String)` |
| RS-05-DATETIME-GETTERS | `getDate(int)`, `getTime(int)`, `getTimestamp(int)`, `getDate(String)`, `getTime(String)`, `getTimestamp(String)`, `getDate(int, Calendar)`, `getDate(String, Calendar)`, `getTime(int, Calendar)`, `getTime(String, Calendar)`, `getTimestamp(int, Calendar)`, `getTimestamp(String, Calendar)` |
| RS-06-OBJECT-GETTERS | `getObject(int)`, `getObject(String)`, `getObject(int, Map)`, `getArray(int)`, `getObject(String, Map)`, `getArray(String)`, `getObject(int, Class<T>)`, `getObject(String, Class<T>)` |
| RS-01B-SCROLL | `beforeFirst()`, `afterLast()`, `first()`, `last()`, `absolute(int)`, `relative(int)`, `previous()`, `setFetchDirection(int)`, `getFetchDirection()`, `setFetchSize(int)`, `getFetchSize()` |
| RS-90-READONLY-UPDATES | `rowUpdated()`, `rowInserted()`, `rowDeleted()`, `updateNull(int)`, `updateBoolean(int, boolean)`, `updateByte(int, byte)`, `updateShort(int, short)`, `updateInt(int, int)`, `updateLong(int, long)`, `updateFloat(int, float)`, `updateDouble(int, double)`, `updateBigDecimal(int, BigDecimal)`, `updateString(int, String)`, `updateBytes(int, byte[])`, `updateDate(int, Date)`, `updateTime(int, Time)`, `updateTimestamp(int, Timestamp)`, `updateAsciiStream(int, InputStream, int)`, `updateBinaryStream(int, InputStream, int)`, `updateCharacterStream(int, Reader, int)`, `updateObject(int, Object, int)`, `updateObject(int, Object)`, `updateNull(String)`, `updateBoolean(String, boolean)`, `updateByte(String, byte)`, `updateShort(String, short)`, `updateInt(String, int)`, `updateLong(String, long)`, `updateFloat(String, float)`, `updateDouble(String, double)`, `updateBigDecimal(String, BigDecimal)`, `updateString(String, String)`, `updateBytes(String, byte[])`, `updateDate(String, Date)`, `updateTime(String, Time)`, `updateTimestamp(String, Timestamp)`, `updateAsciiStream(String, InputStream, int)`, `updateBinaryStream(String, InputStream, int)`, `updateCharacterStream(String, Reader, int)`, `updateObject(String, Object, int)`, `updateObject(String, Object)`, `insertRow()`, `updateRow()`, `deleteRow()`, `refreshRow()`, `cancelRowUpdates()`, `moveToInsertRow()`, `moveToCurrentRow()`, `updateArray(int, Array)`, `updateArray(String, Array)`, `updateSQLXML(int, SQLXML)`, `updateSQLXML(String, SQLXML)` |
| rejected | `getRef(int)`, `getRef(String)`, `getURL(int)`, `getURL(String)`, `updateRef(int, Ref)`, `updateRef(String, Ref)`, `updateBlob(int, Blob)`, `updateBlob(String, Blob)`, `updateClob(int, Clob)`, `updateClob(String, Clob)`, `getRowId(int)`, `getRowId(String)`, `updateRowId(int, RowId)`, `updateRowId(String, RowId)`, `getHoldability()`, `updateNString(int, String)`, `updateNString(String, String)`, `updateNClob(int, NClob)`, `updateNClob(String, NClob)`, `getNClob(int)`, `getNClob(String)`, `getNString(int)`, `getNString(String)`, `getNCharacterStream(int)`, `getNCharacterStream(String)`, `updateNCharacterStream(int, Reader, long)`, `updateNCharacterStream(String, Reader, long)`, `updateAsciiStream(int, InputStream, long)`, `updateBinaryStream(int, InputStream, long)`, `updateCharacterStream(int, Reader, long)`, `updateAsciiStream(String, InputStream, long)`, `updateBinaryStream(String, InputStream, long)`, `updateCharacterStream(String, Reader, long)`, `updateBlob(int, InputStream, long)`, `updateBlob(String, InputStream, long)`, `updateClob(int, Reader, long)`, `updateClob(String, Reader, long)`, `updateNClob(int, Reader, long)`, `updateNClob(String, Reader, long)`, `updateNCharacterStream(int, Reader)`, `updateNCharacterStream(String, Reader)`, `updateAsciiStream(int, InputStream)`, `updateBinaryStream(int, InputStream)`, `updateCharacterStream(int, Reader)`, `updateAsciiStream(String, InputStream)`, `updateBinaryStream(String, InputStream)`, `updateCharacterStream(String, Reader)`, `updateBlob(int, InputStream)`, `updateBlob(String, InputStream)`, `updateClob(int, Reader)`, `updateClob(String, Reader)`, `updateNClob(int, Reader)`, `updateNClob(String, Reader)`, `updateObject(int, Object, SQLType, int)`, `updateObject(String, Object, SQLType, int)`, `updateObject(int, Object, SQLType)`, `updateObject(String, Object, SQLType)` |
| FEATURES | `getBlob(int)`, `getClob(int)`, `getBlob(String)`, `getClob(String)`, `getSQLXML(int)`, `getSQLXML(String)` |

**`ResultSetMetaData`** (21 methods: 21 implemented)

| Verdict | Methods |
|---|---|
| RSMD-01-BASIC | `getColumnCount()`, `isCaseSensitive(int)`, `isSearchable(int)`, `isCurrency(int)`, `isSigned(int)`, `getColumnDisplaySize(int)`, `getColumnLabel(int)`, `getColumnName(int)`, `getSchemaName(int)`, `getPrecision(int)`, `getScale(int)`, `getCatalogName(int)`, `getColumnType(int)`, `isReadOnly(int)`, `isWritable(int)`, `isDefinitelyWritable(int)`, `getColumnClassName(int)` |
| RSMD-02-CATALOG | `isAutoIncrement(int)`, `isNullable(int)`, `getTableName(int)`, `getColumnTypeName(int)` |

**`ParameterMetaData`** (9 methods: 9 implemented)

| Verdict | Methods |
|---|---|
| PSTMT-90-METADATA | `getParameterCount()`, `isNullable(int)`, `isSigned(int)`, `getPrecision(int)`, `getScale(int)`, `getParameterType(int)`, `getParameterTypeName(int)`, `getParameterClassName(int)`, `getParameterMode(int)` |

**`Array`** (11 methods: 11 implemented)

| Verdict | Methods |
|---|---|
| ARRAY-03-JDBC | `getBaseTypeName()`, `getBaseType()`, `getArray()`, `getArray(Map)`, `getArray(long, int)`, `getArray(long, int, Map)`, `getResultSet()`, `getResultSet(Map)`, `getResultSet(long, int)`, `getResultSet(long, int, Map)`, `free()` |

**`Savepoint`** (2 methods: 2 implemented)

| Verdict | Methods |
|---|---|
| CONN-04A-SAVEPOINT | `getSavepointId()`, `getSavepointName()` |

**`Wrapper`** (2 methods: 2 per class)

| Verdict | Methods |
|---|---|
| per class | `unwrap(Class<T>)`, `isWrapperFor(Class<?>)` |

**`DataSource`** (9 methods: 7 implemented, 2 JDK default)

| Verdict | Methods |
|---|---|
| DRIVER-04A-DATASOURCE | `getConnection()`, `getConnection(String, String)`, `getLogWriter()`, `setLogWriter(PrintWriter)`, `setLoginTimeout(int)`, `getLoginTimeout()`, `getParentLogger()` |
| JDK default | `createConnectionBuilder()`, `createShardingKeyBuilder()` |

## 3. Driver, URL, properties, DataSource

### 3.1 URL grammar

`core/DriverUrl.java` (DRIVER-02). pgjdbc: `Driver.java` `parseURL`.

```java
public final class DriverUrl
    public static final String PREFIX = "jdbc:stuntgres:";
    public static boolean hasPrefix(String url);            // exact, case-sensitive prefix match
    public static Properties parse(String url, Properties defaults);   // null when the URL is not accepted
```

```
url      = "jdbc:stuntgres:" [ location ] [ "?" parameters ]
location = database                          ; no slash at all: jdbc:stuntgres:db
         | "//" [ hosts ] "/" [ database ]   ; jdbc:stuntgres://cluster:5433/db
hosts    = host [ ":" port ] { "," host [ ":" port ] }
parameters = name [ "=" value ] { "&" name [ "=" value ] }
```

Rules, all **(probe A1)** with pgjdbc's prefix:

- The result is a new `Properties` whose defaults are `defaults` (so `getProperty` falls
  back to the caller's properties). Keys set from the URL: `PGHOST`, `PGPORT`, `PGDBNAME`
  and one key per parameter. A URL value therefore wins over the caller's property.
- Everything from the first `?` is the parameter list. A name without `=` gets the value
  `""`; of a repeated name the last wins; names and values are decoded with
  `URLDecoder.decode(..., UTF_8)`; a malformed percent sequence anywhere rejects the URL.
- No location, `//` or `///`: `PGHOST` = `localhost`, `PGPORT` = `5432`, no `PGDBNAME`
  from the URL (`jdbc:stuntgres:` alone sets `PGDBNAME` to `""`, as pgjdbc does).
  `///db` sets `PGHOST` to `""`.
- A location that starts with one slash (`jdbc:stuntgres:/`, `jdbc:stuntgres:/db`) is
  rejected. After `//` a slash must follow the host list (`//h` is rejected); a second
  slash after the database (`//h/db/x`) rejects the URL.
- A host may be empty (`//:9/db` gives `localhost`); a missing port is `5432`; a port must
  be an integer from 1 to 65535, else the URL is rejected. Several hosts give
  comma-separated lists in `PGHOST` and `PGPORT` (`h1,h2` and `1,5432`). An IPv6 literal
  keeps its brackets (`[::1]`).
- The database is percent-decoded.
- Any other prefix, in any letter case, is not this driver's URL: `parse` returns null.

The implementing part records further cases from `org.postgresql.Driver.parseURL` (rule
R11) and keeps the two parsers in agreement on every URL it tests.

### 3.2 Defaults and the connection string

`core/ConninfoBuilder.java` (DRIVER-02): `public static String build(Properties info,
TimeZone jvmZone) throws SQLException`. It turns the parsed properties into a libpq
connection string with `org.stuntgres.pq.Conninfo` (NATIVE 9.3), in this order:

| Keyword | Value |
|---|---|
| `host` | `PGHOST`; empty or absent: `localhost`. The cluster name (`docs/LIBSPQ.md` 5.1). |
| `port` | `PGPORT`; absent: `5432` |
| `user` | property `user`; absent or empty: `postgres` (decision 9) |
| `dbname` | `PGDBNAME`; absent or empty: the user |
| `password` | property `password`, only when present |
| `client_encoding` | `UTF8` |
| `application_name` | property `ApplicationName`; default `stuntgres JDBC Driver` |
| `sslmode`, `gssencmode` | `disable` (a real libpq then negotiates nothing; libspq ignores both) |
| `options` | `-c DateStyle=ISO -c TimeZone=<zone>`, then `-c search_path=<currentSchema>` when that property is set, then the property `options` when set, separated by one space |

`<zone>` is `jvmZone.getID()`, except that an ID of the form `GMT+hh:mm` or `GMT-hh:mm`
is sent with the sign inverted (PostgreSQL reads such names with the POSIX sign
convention; pgjdbc: `core/v3/ConnectionFactoryImpl.java` `createPostgresTimeZone`). In a
value inside `options` a space and a backslash are each preceded by a backslash (libpq's
rule for the `options` keyword). No other keyword is ever written; in particular no
property of the "ignored" class reaches the library. `build` raises `PSQLException`
`22023` for a `stringtype` other than `unspecified` or `varchar` and for a value that the
typed accessor of its `PGProperty` rejects; a `replication` value other than `false`
raises `0A000`.

### 3.3 Properties

Verdicts: **honoured** (changes behavior as the note says), **stored** (validated and
kept for the design named), **ignored** (accepted, kept in the properties, without
effect), **rejected** (raises at connect when set). The constant name, property name,
default and choices mirror pgjdbc's `PGProperty` (plan J9), with two differences: the
default of `ApplicationName` is this driver's name and `sslfactory` has no default.
Order of the table = order of the enum constants = order of `Driver.getPropertyInfo`.

| Constant | Property | Default | Choices | Verdict | Note |
|---|---|---|---|---|---|
| `ADAPTIVE_FETCH` | `adaptiveFetch` | `false` |  | ignored | reported by `getAdaptiveFetch()` |
| `ADAPTIVE_FETCH_MAXIMUM` | `adaptiveFetchMaximum` | `-1` |  | ignored |  |
| `ADAPTIVE_FETCH_MINIMUM` | `adaptiveFetchMinimum` | `0` |  | ignored |  |
| `ALLOW_ENCODING_CHANGES` | `allowEncodingChanges` | `false` |  | honoured | the session check accepts a `client_encoding` other than UTF8 (5.2) |
| `APPLICATION_NAME` | `ApplicationName` | `stuntgres JDBC Driver` |  | honoured | `application_name` of the session (3.2); `getClientInfo` (5.4) |
| `ASSUME_MIN_SERVER_VERSION` | `assumeMinServerVersion` | – |  | ignored |  |
| `AUTHENTICATION_PLUGIN_CLASS_NAME` | `authenticationPluginClassName` | – |  | ignored |  |
| `AUTOSAVE` | `autosave` | `never` | always, never, conservative | honoured | 6.5; `conservative` behaves as `never` |
| `BINARY_TRANSFER` | `binaryTransfer` | `true` |  | ignored | text format only (plan B6) |
| `BINARY_TRANSFER_DISABLE` | `binaryTransferDisable` | `""` |  | ignored |  |
| `BINARY_TRANSFER_ENABLE` | `binaryTransferEnable` | `""` |  | ignored |  |
| `CANCEL_SIGNAL_TIMEOUT` | `cancelSignalTimeout` | `10` |  | ignored |  |
| `CHANNEL_BINDING` | `channelBinding` | `prefer` | disable, prefer, require | ignored |  |
| `CLASS_LOADER_STRATEGY` | `classLoaderStrategy` | `driver-first` | driver, driver-first, context-first | ignored | driver's loader first, then the context loader (5.2) |
| `CLEANUP_SAVEPOINTS` | `cleanupSavepoints` | `false` | true, false | honoured | 6.5 |
| `CONNECT_EXECUTOR` | `connectExecutor` | – |  | ignored |  |
| `CONNECT_EXECUTOR_ARG` | `connectExecutorArg` | – |  | ignored |  |
| `CONNECT_TIMEOUT` | `connectTimeout` | `10` |  | ignored | use `loginTimeout` |
| `CONVERT_BOOLEAN_TO_NUMERIC` | `convertBooleanToNumeric` | `false` |  | honoured | `ConversionContext` (TYPES 7.2) |
| `CURRENT_SCHEMA` | `currentSchema` | – |  | honoured | `search_path` of the session (3.2) |
| `DATABASE_METADATA_CACHE_FIELDS` | `databaseMetadataCacheFields` | `65536` |  | ignored | the cache of 11.2 is unbounded |
| `DATABASE_METADATA_CACHE_FIELDS_MIB` | `databaseMetadataCacheFieldsMiB` | `5` |  | ignored |  |
| `DEFAULT_ROW_FETCH_SIZE` | `defaultRowFetchSize` | `0` |  | honoured | initial `getFetchSize()` of statements; a hint (7.5) |
| `DISABLE_COLUMN_SANITISER` | `disableColumnSanitiser` | `false` |  | honoured | `findColumn` (10.3) |
| `ESCAPE_SYNTAX_CALL_MODE` | `escapeSyntaxCallMode` | `select` | select, callIfNoReturn, call | stored | JDBC-DESIGN-FEATURES (`CallEscape.Mode`, TYPES 4.4) |
| `FLUSH_CACHE_ON_DDL` | `flushCacheOnDdl` | `true` |  | ignored |  |
| `GROUP_STARTUP_PARAMETERS` | `groupStartupParameters` | `false` |  | ignored |  |
| `GSS_ENC_MODE` | `gssEncMode` | `allow` | disable, allow, prefer, require | ignored |  |
| `GSS_LIB` | `gsslib` | `auto` | auto, sspi, gssapi | ignored |  |
| `GSS_RESPONSE_TIMEOUT` | `gssResponseTimeout` | `5000` |  | ignored |  |
| `GSS_USE_DEFAULT_CREDS` | `gssUseDefaultCreds` | `false` |  | ignored |  |
| `HIDE_UNPRIVILEGED_OBJECTS` | `hideUnprivilegedObjects` | `false` |  | stored | JDBC-DESIGN-METADATA |
| `HOST_RECHECK_SECONDS` | `hostRecheckSeconds` | `10` |  | ignored |  |
| `JAAS_APPLICATION_NAME` | `jaasApplicationName` | `pgjdbc` |  | ignored |  |
| `JAAS_LOGIN` | `jaasLogin` | `true` |  | ignored |  |
| `KERBEROS_SERVER_NAME` | `kerberosServerName` | – |  | ignored |  |
| `LOAD_BALANCE_HOSTS` | `loadBalanceHosts` | `false` |  | ignored |  |
| `LOCAL_SOCKET_ADDRESS` | `localSocketAddress` | – |  | ignored |  |
| `LOGGER_FILE` | `loggerFile` | – |  | ignored |  |
| `LOGGER_LEVEL` | `loggerLevel` | – | OFF, DEBUG, TRACE | ignored |  |
| `LOGIN_TIMEOUT` | `loginTimeout` | `0` |  | honoured | limit of the connect call, seconds (3.5) |
| `LOG_SERVER_ERROR_DETAIL` | `logServerErrorDetail` | `true` |  | honoured | message of server errors (4.1, 4.2) |
| `LOG_UNCLOSED_CONNECTIONS` | `logUnclosedConnections` | `false` |  | ignored |  |
| `MAX_RESULT_BUFFER` | `maxResultBuffer` | – |  | ignored |  |
| `MAX_SEND_BUFFER_SIZE` | `maxSendBufferSize` | `8192` |  | ignored |  |
| `OPTIONS` | `options` | – |  | honoured | appended to the session options (3.2) |
| `PASSWORD` | `password` | – |  | honoured | connection string (3.2) |
| `PEM_KEY_ALGORITHM` | `pemKeyAlgorithm` | `RSA` |  | ignored |  |
| `PG_DBNAME` | `PGDBNAME` (required) | – |  | honoured | connection string (3.2); default: the user |
| `PG_HOST` | `PGHOST` | `localhost` |  | honoured | the cluster (3.2) |
| `PG_PORT` | `PGPORT` | `5432` |  | honoured | the cluster's port (3.2) |
| `PREFER_QUERY_MODE` | `preferQueryMode` | `extended` | extended, extendedForPrepared, extendedCacheEverything, simple | ignored | reported by `getPreferQueryMode()` (5.6) |
| `PREPARED_STATEMENT_CACHE_QUERIES` | `preparedStatementCacheQueries` | `256` |  | ignored |  |
| `PREPARED_STATEMENT_CACHE_SIZE_MIB` | `preparedStatementCacheSizeMiB` | `5` |  | ignored |  |
| `PREPARE_THRESHOLD` | `prepareThreshold` | `5` |  | ignored | reported by `getPrepareThreshold()`; plan B8 |
| `PROTOCOL_VERSION` | `protocolVersion` | `3` | 3 | ignored |  |
| `QUERY_TIMEOUT` | `queryTimeout` | `0` |  | honoured | initial query timeout of statements, seconds (7.6) |
| `QUOTE_RETURNING_IDENTIFIERS` | `quoteReturningIdentifiers` | `true` |  | honoured | generated-keys column names (8.3) |
| `READ_ONLY` | `readOnly` | `false` |  | honoured | 6.3 |
| `READ_ONLY_MODE` | `readOnlyMode` | `transaction` | ignore, transaction, always | honoured | 6.3 |
| `RECEIVE_BUFFER_SIZE` | `receiveBufferSize` | `-1` |  | ignored |  |
| `REPLICATION` | `replication` | – |  | rejected | a value other than `false` raises `0A000` at connect (3.2) |
| `REQUIRE_AUTH` | `requireAuth` | – | password, md5, gss, sspi, scram-sha-256, none, !password, !md5, !gss, !sspi, !scram-sha-256, !none | ignored |  |
| `REWRITE_BATCHED_INSERTS` | `reWriteBatchedInserts` | `false` |  | ignored | batches return exact counts (8.1) |
| `REWRITE_BATCHED_INSERTS_SIZE` | `reWriteBatchedInsertsSize` | `0` |  | ignored |  |
| `SCRAM_MAX_ITERATIONS` | `scramMaxIterations` | `100000` |  | ignored |  |
| `SEND_BUFFER_SIZE` | `sendBufferSize` | `-1` |  | ignored |  |
| `SERVICE` | `service` | – |  | ignored | no service file is read (`docs/LIBSPQ.md` 5.1) |
| `SOCKET_FACTORY` | `socketFactory` | – |  | ignored |  |
| `SOCKET_FACTORY_ARG` | `socketFactoryArg` | – |  | ignored |  |
| `SOCKET_TIMEOUT` | `socketTimeout` | `0` |  | ignored |  |
| `SSL` | `ssl` | – |  | ignored |  |
| `SSL_CERT` | `sslcert` | – |  | ignored |  |
| `SSL_FACTORY` | `sslfactory` | – |  | ignored |  |
| `SSL_FACTORY_ARG` | `sslfactoryarg` | – |  | ignored |  |
| `SSL_HOSTNAME_VERIFIER` | `sslhostnameverifier` | – |  | ignored |  |
| `SSL_KEY` | `sslkey` | – |  | ignored |  |
| `SSL_MODE` | `sslmode` | – | disable, allow, prefer, require, verify-ca, verify-full | ignored |  |
| `SSL_NEGOTIATION` | `sslNegotiation` | `postgres` | postgres, direct | ignored |  |
| `SSL_PASSWORD` | `sslpassword` | – |  | ignored |  |
| `SSL_PASSWORD_CALLBACK` | `sslpasswordcallback` | – |  | ignored |  |
| `SSL_RESPONSE_TIMEOUT` | `sslResponseTimeout` | `5000` |  | ignored |  |
| `SSL_ROOT_CERT` | `sslrootcert` | – |  | ignored |  |
| `SSPI_SERVICE_CLASS` | `sspiServiceClass` | `POSTGRES` |  | ignored |  |
| `STRING_TYPE` | `stringtype` | – | unspecified, varchar | honoured | `ConversionContext` (TYPES 8.3); other values raise `22023` (3.2) |
| `TARGET_SERVER_TYPE` | `targetServerType` | `any` | any, primary, master, slave, secondary, preferSlave, preferSecondary, preferPrimary | ignored |  |
| `TCP_KEEP_ALIVE` | `tcpKeepAlive` | `false` |  | ignored |  |
| `TCP_NO_DELAY` | `tcpNoDelay` | `true` |  | ignored |  |
| `UNKNOWN_LENGTH` | `unknownLength` | `2147483647` |  | ignored | the default always applies (TYPES 5.2) |
| `USER` | `user` (required) | – |  | honoured | connection string (3.2); default `postgres` |
| `USE_SPNEGO` | `useSpnego` | `false` |  | ignored |  |
| `XML_FACTORY_FACTORY` | `xmlFactoryFactory` | `""` |  | stored | JDBC-DESIGN-FEATURES |

### 3.4 `PGProperty`

`org/stuntgres/PGProperty.java` (DRIVER-01): a public enum with the 92 constants of 3.3 in
that order. The constant list is a data table in the sense of rule R6 (exempt from the
500-line limit); the description of each constant is one sentence in the driver's own
words. pgjdbc: `PGProperty.java` (names, defaults and accessor semantics only).

```java
public enum PGProperty
    public String getName();
    public String getDefaultValue();                 // null when there is none
    public boolean isRequired();
    public String getDescription();
    public String[] getChoices();                    // null when there are none
    public String getOrDefault(Properties properties);   // the property, else the default
    public String getOrNull(Properties properties);      // the property without the default
    public void set(Properties properties, String value);   // null removes
    public void set(Properties properties, boolean value);
    public void set(Properties properties, int value);
    public boolean getBoolean(Properties properties);     // Boolean.parseBoolean of getOrDefault
    public int getInt(Properties properties) throws PSQLException;      // 22023 when not an int
    public int getIntNoCheck(Properties properties);      // 0 when absent; NumberFormatException otherwise
    public Integer getInteger(Properties properties) throws PSQLException;   // null when absent
    public boolean isPresent(Properties properties);
    public DriverPropertyInfo toDriverPropertyInfo(Properties properties);
    public static PGProperty forName(String name);        // null when unknown
```

Also in DRIVER-01, both public enums with `value()` and `static of(String)` as in pgjdbc:
`jdbc/AutoSave.java` (`NEVER`, `ALWAYS`, `CONSERVATIVE`; values `never`, `always`,
`conservative`) and `jdbc/PreferQueryMode.java` (`SIMPLE`, `EXTENDED_FOR_PREPARED`,
`EXTENDED`, `EXTENDED_CACHE_EVERYTHING`, in this order; values `simple`,
`extendedForPrepared`, `extended`, `extendedCacheEverything`; an unknown text gives
`EXTENDED`).

### 3.5 `Driver`

`org/stuntgres/Driver.java` and `src/main/resources/META-INF/services/java.sql.Driver`
(one line: `org.stuntgres.Driver`) (DRIVER-03). pgjdbc: `Driver.java`.

```java
public class Driver implements java.sql.Driver
    public Connection connect(String url, Properties info) throws SQLException;
    public boolean acceptsURL(String url);
    public DriverPropertyInfo[] getPropertyInfo(String url, Properties info);
    public int getMajorVersion();
    public int getMinorVersion();
    public boolean jdbcCompliant();
    public Logger getParentLogger();
    public static Properties parseURL(String url, Properties defaults);
    public static void register() throws SQLException;
    public static void deregister() throws SQLException;
    public static boolean isRegistered();
```

| Member | Behavior |
|---|---|
| class initialization | `register()`; a failure becomes `ExceptionInInitializerError`. Loads no native code and touches no class of `org.stuntgres.pq` (plan B12). |
| `connect` | `url` null: `SQLException` (`url is null`, no SQLState). Not `DriverUrl.hasPrefix(url)`: return null (`DriverManager` then tries the next driver). A value of `info` that is not a `String`: `PSQLException` `99999`. `parseURL` null: `PSQLException` `99999` (`Unable to parse URL`). Else `new PgConnection(properties, url)`. |
| `acceptsURL` | `parseURL(url, null) != null`; a null URL raises `NullPointerException` as in pgjdbc. |
| `getPropertyInfo` | One `DriverPropertyInfo` per `PGProperty` in enum order, filled from the parsed URL over `info` (from `info` alone when the URL does not parse). |
| versions | Major and minor number of the text `version` in the resource `org/stuntgres/version.properties` (BUILD-01): `0` and `1` for `0.1.0`. Read once, without native code. |
| `jdbcCompliant` | `false` |
| `getParentLogger` | `Logger.getLogger("org.stuntgres")` |
| `parseURL` | `DriverUrl.parse` |
| `register` | Registers one instance with `DriverManager`; a second call raises `IllegalStateException`. `deregister` removes it (`IllegalStateException` when not registered). |

Login timeout: `connect` passes `loginTimeout` seconds (the property; when absent,
`DriverManager.getLoginTimeout()`) to `PqConnection.connect(conninfo, millis)`; the
library's `timeout expired` becomes `08001` (4.3).

### 3.6 DataSource

`ds/common/BaseDataSource.java` (public abstract, the end of the chain `DsStore` →
`DsHonoured` → `DsIgnoredTransport` → `DsIgnoredTuning`) and `ds/PGSimpleDataSource.java`
(`public class PGSimpleDataSource extends BaseDataSource implements DataSource,
Serializable`). pgjdbc: `ds/common/BaseDataSource.java`, `ds/PGSimpleDataSource.java`.
The bean accessors mirror pgjdbc's name for name, so that a pool configured with
`dataSourceClassName` and bean properties keeps working.

`DsStore` (DRIVER-04A) holds the state: server names (default `{"localhost"}`), port
numbers (default `{0}`, meaning 5432), database name, user, password and a `Properties`
object for everything else.

```java
abstract class DsStore implements CommonDataSource, Serializable
    public Connection getConnection() throws SQLException;                    // getConnection(user, password)
    public Connection getConnection(String user, String password) throws SQLException;
    public PrintWriter getLogWriter();                                        // always null
    public void setLogWriter(PrintWriter writer);                             // ignored
    public Logger getParentLogger();                                          // "org.stuntgres"
    public String getServerName();   public String[] getServerNames();
    public void setServerName(String name);   public void setServerNames(String[] names);
    public String getDatabaseName();   public void setDatabaseName(String name);
    public String getUser();   public void setUser(String user);
    public String getPassword();   public void setPassword(String password);
    public int getPortNumber();   public int[] getPortNumbers();
    public void setPortNumber(int port);   public void setPortNumbers(int[] ports);
    public String getUrl();   public String getURL();
    public void setUrl(String url);   public void setURL(String url);
    public String getProperty(String name) throws SQLException;
    public void setProperty(String name, String value) throws SQLException;
    public String getProperty(PGProperty property);
    public void setProperty(PGProperty property, String value);
    public abstract String getDescription();
```

- `getUrl()` builds `jdbc:stuntgres://host[:port][,host[:port]...]/database` (a port of 0
  is left out; the database is percent-encoded) followed by `?name=value&...` for every
  stored property, values percent-encoded.
- `setUrl(url)` parses with `DriverUrl.parse`; an unparsable URL raises
  `IllegalArgumentException`; the parsed entries replace the stored state.
- `getConnection(user, password)` is `DriverManager.getConnection(getUrl(), properties)`
  with `user` and `password` added when not null. A failure is the driver's exception.
- `getProperty(String)` / `setProperty(String, String)`: an unknown name raises
  `PSQLException` `22023`; `user`, `password`, `PGHOST`, `PGPORT`, `PGDBNAME` go to the
  dedicated fields. `setLoginTimeout`/`getLoginTimeout` are the bean pair of `loginTimeout`.
- `PGSimpleDataSource`: `getDescription()` returns `Non-Pooling DataSource from stuntgres
  JDBC Driver`; `unwrap`/`isWrapperFor` answer for the interfaces it implements
  (`cannotUnwrap` of 4.4 otherwise); `createConnectionBuilder` is the JDK default.

The typed bean accessors, one pair per property, each a one-line call of
`getProperty`/`setProperty` with the `PGProperty` (boolean pairs read with
`PGProperty.getBoolean`, int pairs with `getIntNoCheck`). They are split by the verdict of
3.3: `DsHonoured` (DRIVER-04B) has the pairs of honoured and stored properties,
`DsIgnoredTransport` (04C) and `DsIgnoredTuning` (04D) those of ignored properties. The
last two files are mechanical accessor tables (data tables in the sense of rule R6). Generated from pgjdbc's public signatures
(87 properties; `service`, `user`, `password` and the three `PG*` properties have none):

**`DsHonoured`** (18 properties, 42 accessors): `allowEncodingChanges`: `boolean getAllowEncodingChanges()`, `boolean isAllowEncodingChanges()`, `void setAllowEncodingChanges(boolean allow)`; `ApplicationName`: `String getApplicationName()`, `void setApplicationName(String applicationName)`; `autosave`: `AutoSave getAutosave()`, `void setAutosave(AutoSave autoSave)`; `cleanupSavepoints`: `boolean getCleanupSavepoints()`, `boolean isCleanupSavePoints()`, `void setCleanupSavePoints(boolean cleanupSavepoints)`, `void setCleanupSavepoints(boolean cleanupSavepoints)`; `convertBooleanToNumeric`: `boolean getConvertBooleanToNumeric()`, `void setConvertBooleanToNumeric(boolean convertBooleanToNumeric)`; `currentSchema`: `String getCurrentSchema()`, `void setCurrentSchema(String currentSchema)`; `defaultRowFetchSize`: `int getDefaultRowFetchSize()`, `void setDefaultRowFetchSize(int fetchSize)`; `disableColumnSanitiser`: `boolean getDisableColumnSanitiser()`, `boolean isColumnSanitiserDisabled()`, `boolean isDisableColumnSanitiser()`, `void setDisableColumnSanitiser(boolean disableColumnSanitiser)`; `escapeSyntaxCallMode`: `String getEscapeSyntaxCallMode()`, `void setEscapeSyntaxCallMode(String callMode)`; `hideUnprivilegedObjects`: `boolean getHideUnprivilegedObjects()`, `void setHideUnprivilegedObjects(boolean hideUnprivileged)`; `logServerErrorDetail`: `boolean getLogServerErrorDetail()`, `void setLogServerErrorDetail(boolean enabled)`; `options`: `String getOptions()`, `void setOptions(String options)`; `queryTimeout`: `int getQueryTimeout()`, `void setQueryTimeout(int seconds)`; `quoteReturningIdentifiers`: `boolean getQuoteReturningIdentifiers()`, `void setQuoteReturningIdentifiers(boolean quoteIdentifiers)`; `readOnly`: `boolean getReadOnly()`, `boolean isReadOnly()`, `void setReadOnly(boolean readOnly)`; `readOnlyMode`: `String getReadOnlyMode()`, `void setReadOnlyMode(String mode)`; `stringtype`: `String getStringType()`, `void setStringType(String stringType)`; `xmlFactoryFactory`: `String getXmlFactoryFactory()`, `void setXmlFactoryFactory(String xmlFactoryFactory)`.

**`DsIgnoredTransport`** (26 properties, 71 accessors): `authenticationPluginClassName`: `String getAuthenticationPluginClassName()`, `void setAuthenticationPluginClassName(String className)`; `channelBinding`: `String getChannelBinding()`, `void setChannelBinding(String channelBinding)`; `gssEncMode`: `String getGssEncMode()`, `void setGssEncMode(String mode)`; `gsslib`: `String getGssLib()`, `void setGssLib(String lib)`; `gssResponseTimeout`: `int getGssResponseTimeout()`, `void setGssResponseTimeout(int gssResponseTimeout)`; `gssUseDefaultCreds`: `boolean getGssUseDefaultCreds()`, `void setGssUseDefaultCreds(boolean gssUseDefaultCreds)`; `jaasApplicationName`: `String getJaasApplicationName()`, `void setJaasApplicationName(String name)`; `jaasLogin`: `boolean getJaasLogin()`, `void setJaasLogin(boolean doLogin)`; `kerberosServerName`: `String getKerberosServerName()`, `void setKerberosServerName(String serverName)`; `pemKeyAlgorithm`: `String getPemKeyAlgo()`, `String getPemKeyAlgorithm()`, `void setPemKeyAlgo(String algorithm)`, `void setPemKeyAlgorithm(String algorithm)`; `requireAuth`: `String getRequireAuth()`, `void setRequireAuth(String requireAuth)`; `scramMaxIterations`: `int getScramMaxIterations()`, `void setScramMaxIterations(int scramMaxIterations)`; `ssl`: `boolean getSsl()`, `boolean isSsl()`, `void setSsl(boolean enabled)`; `sslcert`: `String getSslCert()`, `String getSslcert()`, `void setSslCert(String file)`, `void setSslcert(String file)`; `sslfactory`: `String getSslfactory()`, `void setSslfactory(String classname)`; `sslfactoryarg`: `String getSslFactoryArg()`, `String getSslfactoryarg()`, `void setSslFactoryArg(String arg)`, `void setSslfactoryarg(String arg)`; `sslhostnameverifier`: `String getSslHostnameVerifier()`, `String getSslhostnameverifier()`, `void setSslHostnameVerifier(String className)`, `void setSslhostnameverifier(String className)`; `sslkey`: `String getSslKey()`, `String getSslkey()`, `void setSslKey(String file)`, `void setSslkey(String file)`; `sslmode`: `String getSslMode()`, `String getSslmode()`, `void setSslMode(String mode)`, `void setSslmode(String mode)`; `sslNegotiation`: `String getSslNegotiation()`, `void setSslNegotiation(String sslNegotiation)`; `sslpassword`: `String getSslPassword()`, `String getSslpassword()`, `void setSslPassword(String password)`, `void setSslpassword(String sslpassword)`; `sslpasswordcallback`: `String getSslPasswordCallback()`, `String getSslpasswordcallback()`, `void setSslPasswordCallback(String className)`, `void setSslpasswordcallback(String className)`; `sslResponseTimeout`: `int getSslResponseTimeout()`, `void setSslResponseTimeout(int sslResponseTimeout)`; `sslrootcert`: `String getSslRootCert()`, `String getSslrootcert()`, `void setSslRootCert(String file)`, `void setSslrootcert(String file)`; `sspiServiceClass`: `String getSspiServiceClass()`, `void setSspiServiceClass(String serviceClass)`; `useSpnego`: `boolean getUseSpNego()`, `void setUseSpNego(boolean use)`.

**`DsIgnoredTuning`** (41 properties, 88 accessors): `adaptiveFetch`: `boolean getAdaptiveFetch()`, `void setAdaptiveFetch(boolean adaptiveFetch)`; `adaptiveFetchMaximum`: `int getAdaptiveFetchMaximum()`, `void setAdaptiveFetchMaximum(int adaptiveFetchMaximum)`; `adaptiveFetchMinimum`: `int getAdaptiveFetchMinimum()`, `void setAdaptiveFetchMinimum(int adaptiveFetchMinimum)`; `assumeMinServerVersion`: `String getAssumeMinServerVersion()`, `void setAssumeMinServerVersion(String minVersion)`; `binaryTransfer`: `boolean getBinaryTransfer()`, `void setBinaryTransfer(boolean enabled)`; `binaryTransferDisable`: `String getBinaryTransferDisable()`, `void setBinaryTransferDisable(String oidList)`; `binaryTransferEnable`: `String getBinaryTransferEnable()`, `void setBinaryTransferEnable(String oidList)`; `cancelSignalTimeout`: `int getCancelSignalTimeout()`, `void setCancelSignalTimeout(int seconds)`; `classLoaderStrategy`: `String getClassLoaderStrategy()`, `void setClassLoaderStrategy(String classLoaderStrategy)`; `connectExecutor`: `String getConnectExecutor()`, `void setConnectExecutor(String connectExecutor)`; `connectExecutorArg`: `String getConnectExecutorArg()`, `void setConnectExecutorArg(String connectExecutorArg)`; `connectTimeout`: `int getConnectTimeout()`, `void setConnectTimeout(int connectTimeout)`; `databaseMetadataCacheFields`: `int getDatabaseMetadataCacheFields()`, `void setDatabaseMetadataCacheFields(int cacheSize)`; `databaseMetadataCacheFieldsMiB`: `int getDatabaseMetadataCacheFieldsMiB()`, `void setDatabaseMetadataCacheFieldsMiB(int cacheSize)`; `flushCacheOnDdl`: `boolean getFlushCacheOnDdl()`, `void setFlushCacheOnDdl(boolean flushCacheOnDdl)`; `groupStartupParameters`: `boolean getGroupStartupParameters()`, `void setGroupStartupParameters(boolean groupStartupParameters)`; `hostRecheckSeconds`: `int getHostRecheckSeconds()`, `void setHostRecheckSeconds(int hostRecheckSeconds)`; `loadBalanceHosts`: `boolean getLoadBalanceHosts()`, `boolean isLoadBalanceHosts()`, `void setLoadBalanceHosts(boolean loadBalanceHosts)`; `localSocketAddress`: `String getLocalSocketAddress()`, `void setLocalSocketAddress(String localSocketAddress)`; `loggerFile`: `String getLoggerFile()`, `void setLoggerFile(String loggerFile)`; `loggerLevel`: `String getLoggerLevel()`, `void setLoggerLevel(String loggerLevel)`; `logUnclosedConnections`: `boolean getLogUnclosedConnections()`, `boolean isLogUnclosedConnections()`, `void setLogUnclosedConnections(boolean enabled)`; `maxResultBuffer`: `String getMaxResultBuffer()`, `void setMaxResultBuffer(String maxResultBuffer)`; `maxSendBufferSize`: `int getMaxSendBufferSize()`, `void setMaxSendBufferSize(int nbytes)`; `preferQueryMode`: `PreferQueryMode getPreferQueryMode()`, `void setPreferQueryMode(PreferQueryMode preferQueryMode)`; `preparedStatementCacheQueries`: `int getPreparedStatementCacheQueries()`, `void setPreparedStatementCacheQueries(int cacheSize)`; `preparedStatementCacheSizeMiB`: `int getPreparedStatementCacheSizeMiB()`, `void setPreparedStatementCacheSizeMiB(int cacheSize)`; `prepareThreshold`: `int getPrepareThreshold()`, `void setPrepareThreshold(int count)`; `protocolVersion`: `int getProtocolVersion()`, `void setProtocolVersion(int protocolVersion)`; `receiveBufferSize`: `int getReceiveBufferSize()`, `int getRecvBufferSize()`, `void setReceiveBufferSize(int nbytes)`, `void setRecvBufferSize(int nbytes)`; `replication`: `String getReplication()`, `void setReplication(String replication)`; `reWriteBatchedInserts`: `boolean getReWriteBatchedInserts()`, `boolean isReWriteBatchedInserts()`, `void setReWriteBatchedInserts(boolean reWrite)`; `reWriteBatchedInsertsSize`: `int getReWriteBatchedInsertsSize()`, `void setReWriteBatchedInsertsSize(int size)`; `sendBufferSize`: `int getSendBufferSize()`, `void setSendBufferSize(int nbytes)`; `socketFactory`: `String getSocketFactory()`, `void setSocketFactory(String socketFactoryClassName)`; `socketFactoryArg`: `String getSocketFactoryArg()`, `void setSocketFactoryArg(String socketFactoryArg)`; `socketTimeout`: `int getSocketTimeout()`, `void setSocketTimeout(int seconds)`; `targetServerType`: `String getTargetServerType()`, `void setTargetServerType(String targetServerType)`; `tcpKeepAlive`: `boolean getTcpKeepAlive()`, `boolean isTcpKeepAlive()`, `void setTcpKeepAlive(boolean enabled)`; `tcpNoDelay`: `boolean getTcpNoDelay()`, `void setTcpNoDelay(boolean enabled)`; `unknownLength`: `int getUnknownLength()`, `void setUnknownLength(int unknownLength)`.

## 4. Errors and warnings

### 4.1 Classes

`util/ServerErrorMessage.java`, `util/PSQLWarning.java`, and the complete
`util/PSQLException.java` (ERR-01; the last replaces the first form that TYPE-00A-ERRORS
created and keeps its two constructors). `PSQLState` is complete (TYPES). pgjdbc:
`util/ServerErrorMessage.java`, `util/PSQLException.java`, `util/PSQLWarning.java`.

```java
public class ServerErrorMessage implements Serializable
    public ServerErrorMessage(IntFunction<String> fieldByCode);   // reads the 18 field codes of NATIVE 9.2 once
    public String getSQLState();    public String getMessage();    public String getSeverity();
    public String getDetail();      public String getHint();       public int getPosition();
    public String getWhere();       public String getSchema();     public String getTable();
    public String getColumn();      public String getDatatype();   public String getConstraint();
    public String getFile();        public int getLine();          public String getRoutine();
    public String getInternalQuery();    public int getInternalPosition();
    public String toString();
    public String getNonSensitiveErrorMessage();
public class PSQLException extends SQLException
    public PSQLException(String msg, PSQLState state);
    public PSQLException(String msg, PSQLState state, Throwable cause);
    public PSQLException(ServerErrorMessage serverError);                  // detail = true
    public PSQLException(ServerErrorMessage serverError, boolean detail);
    public ServerErrorMessage getServerErrorMessage();                     // null for a client-side error
public class PSQLWarning extends SQLWarning
    public PSQLWarning(ServerErrorMessage err);
    public String getMessage();                                            // the primary message only
    public ServerErrorMessage getServerErrorMessage();
```

- `ServerErrorMessage` is built from a function that maps a field code (`'S'`, `'C'`, `'M'`
  ...) to its text or null: `result::errorField` for a `PqResult`, `notice::field` for a
  `PqNotice`. The class itself does not import `org.stuntgres.pq`. A missing text field is
  null; a missing or non-numeric position or line is 0.
- `toString()` is pgjdbc's layout, because this text is the message of every server
  error and applications match on it **(probe A3, A5, A6)**: `<severity>: <message>`,
  then for each present field a line feed, two spaces and `Detail: <detail>`, `Hint:
  <hint>`, `Position: <position>`, `Where: <where>`, in this order (examples: `ERROR:
  duplicate key value violates unique constraint "pa_pkey"\n  Detail: Key (id)=(1) already
  exists.`; `ERROR: COPY from stdin failed: ...\n  Where: COPY pa, line 1`). The implementer
  establishes in reference mode whether internal query, internal position and location
  are ever part of it at pgjdbc's default settings.
- `getNonSensitiveErrorMessage()` is `<severity>: <message>` only.
- `PSQLException(serverError, detail)`: message `toString()` when `detail`, else the
  non-sensitive form; SQLState `getSQLState()`; vendor code 0.
- `PSQLWarning`: SQLState of the notice; `getMessage()` is the primary message.

### 4.2 Server errors and notices

`jdbc/Errors.java` (ERR-02): `final class Errors` with static methods only; the one place
that turns pq-level facts into exceptions. Test support of the same part:
`src/test/java/org/stuntgres/pq/PqFixtures.java`, a public class in the test tree that
builds `PqResult` and `PqNotice` objects through their package-private constructors
(`error(Map<Character, String> fields, String message)`, `notice(...)`, `rows(String[]
names, int[] typeOids, int[] typeModifiers, String[][] values)` with null for SQL NULL,
`command(String tag, String tuples)`, `empty()`), for the unit tests of this layer.

```java
final class Errors
    static PSQLException server(PqResult result, boolean detail);
    static SQLWarning warning(PqNotice notice);
    static SQLException pq(PqException e);
    static PSQLException connectFailure(String libraryText);
    static PSQLException closedConnection();
    static PSQLException closedStatement();
    static PSQLException closedResultSet();
    static SQLFeatureNotSupportedException notImplemented(Class<?> type, String method);
    static PSQLException columnIndex(int index, int count);
    static PSQLException parameterIndex(int index, int count);
    static PSQLException invalidValue(String what, Object value);
    static SQLException cannotUnwrap(Class<?> iface);
```

- `server(result, detail)`: a result with a SQLSTATE field becomes
  `new PSQLException(new ServerErrorMessage(result::errorField), detail)`. A result
  without one is a failure of the connection itself (NATIVE 6.1): `PSQLException` `08006`
  with `result.errorMessage()` as the message. `detail` is the connection's
  `logServerErrorDetail`.
- `warning(notice)`: `new PSQLWarning(new ServerErrorMessage(notice::field))`.
- Notices are drained with `PqConnection.takeNotices()` after every call into the pq
  layer and appended, oldest first, to the warning chain of the statement that is
  executing, or of the connection when the driver runs a statement of its own (5.1). No
  test asserts on notices raised while a session starts in native mode (hand-over point 1).

### 4.3 Failures of the pq layer and of connect

`Errors.pq(e)` by `e.kind()` (NATIVE 9.3); the message is `e.getMessage()` and the cause
is `e`:

| Kind | SQLState | Class |
|---|---|---|
| `NOT_LOADED`, `LOAD_FAILED`, `UNSUPPORTED_PLATFORM` | `08001` | `PSQLException` |
| `OUT_OF_MEMORY`, `TOO_LARGE` | `53200` | `PSQLException` |
| `CLOSED` | `08003` | `PSQLException` (`Errors.closedConnection()`) |
| `BAD_ARGUMENT` | `22023` | `PSQLException` |
| `CALL_FAILED` | `08006` | `PSQLException` |
| `CONNECT_FAILED` | `connectFailure(text)` | `PSQLException` |
| `UNSUPPORTED` | `0A000` | `SQLFeatureNotSupportedException` |

`connectFailure(text)`: the first row whose pattern occurs in the text decides
**(probe A3 gives the pgjdbc states of the first, second and fifth row and of the last)**:

| Text contains | SQLState | With libspq |
|---|---|---|
| `FATAL:` and `database "` and `does not exist` | `3D000` | cannot occur |
| `FATAL:` and `password authentication failed` | `28P01` | cannot occur |
| `FATAL:` and `role "` and `does not exist` | `28000` | cannot occur |
| `FATAL:` and `is not permitted to log in` | `28000` | a role without LOGIN |
| `FATAL:` and `invalid value for parameter` | `22023` | a bad `options` value |
| `FATAL:` (anything else) | `08004` | other rejections by the engine |
| no `FATAL:` (refused, `timeout expired`, a malformed connection string) | `08001` | a malformed connection string |

pgjdbc passes the server's SQLState through, so it reports other states for rejections this
table does not know (`53300` for too many connections, for example); those arrive as `08004`.
Messages are matched in English: the harness and
libspq produce English texts; a server with another `lc_messages` falls to `08004`.

After a failure the caller decides whether the connection is still usable:
`ConnCore.failure(e)` (5.1) maps with `Errors.pq` and, when `pq.status()` is
`ConnStatus.BAD`, closes the connection first.

### 4.4 Client-side errors

Class is `PSQLException` unless stated. "Same" = the text equals pgjdbc's (rule D9: it is
matched by applications); every other text is the driver's own wording, given here
literally (`{0}` marks an inserted value). "Where" names the factory of `Errors` or the
section whose part writes the `new PSQLException(text, PSQLState.X)` itself.

| Situation | SQLState | Text | Where |
|---|---|---|---|
| closed connection | `08003` | `This connection has been closed.` (same) | `Errors.closedConnection` |
| closed statement | `55000` | `This statement has been closed.` (same) | `Errors.closedStatement` |
| closed result set | `55000` | `This ResultSet is closed.` (same) | `Errors.closedResultSet` |
| method not implemented | `0A000`, `SQLFeatureNotSupportedException` | `Method {0}.{1} is not yet implemented.` | `Errors.notImplemented` |
| column index | `22023` | `The column index is out of range: {0}, number of columns: {1}.` | `Errors.columnIndex` |
| parameter index | `22023` | `The parameter index is out of range: {0}, number of parameters: {1}.` | `Errors.parameterIndex` |
| invalid argument of a setter such as `setMaxRows(-1)`, `setFetchSize(-1)`, `setQueryTimeout(-1)`, a bad fetch direction, holdability, generated-keys constant, `isValid(-1)`, `setNetworkTimeout(-1)` | `22023` | `Invalid value for {0}: {1}.` | `Errors.invalidValue` |
| unwrap to a foreign interface | none, `SQLException` | `Cannot unwrap to {0}` | `Errors.cannotUnwrap` |
| NUL in statement text | `08P01` | `The statement text contains a NUL character.` | 7.2 |
| COPY through a statement | `0A000` | `COPY commands are only supported using the CopyManager API.` | 7.2 |
| `executeQuery` without a result set | `02000` | `No results were returned by the query.` | 7.3 |
| `executeQuery` with several results | `0100E` | `Multiple ResultSets were returned by the query.` | 7.3 |
| `executeUpdate` with a result set | `0100E` | `A result was returned when none was expected.` | 7.3 |
| statement method with SQL text on a prepared statement | `42809` | `A PreparedStatement cannot execute a statement text given to the method.` | 9.1 |
| parameter without a value | `22023` | `No value specified for parameter {0}.` | 9.1 |
| more than 65535 parameters | `22023` | `A statement can have at most 65535 parameters; this one has {0}.` | 9.1 |
| `commit`/`rollback` in autocommit mode | `25P01` | `Cannot commit when autoCommit is enabled.` / `Cannot rollback when autoCommit is enabled.` | 6.2 |
| `commit` answered with `ROLLBACK` | `25P02` | `The transaction was rolled back by the server because an earlier statement failed.` | 6.2 |
| isolation or read-only change in a transaction | `25001` | `Cannot change the transaction isolation level in the middle of a transaction.` / `Cannot change the read-only property in the middle of a transaction.` | 6.3 |
| unsupported isolation level | `0A000` | `Transaction isolation level {0} is not supported.` | 6.3 |
| savepoint in autocommit mode | `25P01` | `Cannot use a savepoint in auto-commit mode.` | 6.4 |
| released savepoint used | `3B000` | `The savepoint has been released.` | 6.4 |
| id of a named savepoint, name of an unnamed one | `42809` | `A named savepoint has no id.` / `An unnamed savepoint has no name.` | 6.4 |
| client info name other than `ApplicationName` | `0A000`, an `SQLWarning` on the connection, not an exception | `ClientInfo property not supported.` | 5.4 |
| non-empty type map used | `0A000`, `SQLFeatureNotSupportedException` | `Custom type maps are not supported.` | 10.4 |
| generated keys by column index | `0A000` | `Returning generated keys by column index is not supported.` | 8.3 |
| several statements in `Statement.addBatch` | `0A000` | `A batch entry must be a single statement.` | 8.1 |
| failed batch | SQLState of the cause, `BatchUpdateException` | `Batch entry {0} {1} failed: {2}` | 8.1 |
| scroll operation on a forward-only result set | `24000` | `The operation requires a scrollable ResultSet, but this one is FORWARD_ONLY.` | 10.2 |
| getter without a current row | `24000` | `The ResultSet is not positioned on a row; call next() first.` | 10.3 |
| unknown column label | `42703` | `The column name {0} was not found in this ResultSet.` | 10.3 |
| update method on a read-only result set | `24000` | `A ResultSet with concurrency CONCUR_READ_ONLY cannot be updated.` | 10.5 |
| unknown type name in `createArrayOf` | `42602` | `No array type was found for the type name {0}.` | 11.4 |
| array index or count out of range | `22000` | `The array index is out of range: {0}, number of elements: {1}.` | 11.4 |
| `abort(null)` | none, `SQLException` | `executor is null` | 5.3 |
| session left UTF8 or ISO dates | `08006` | `The session changed client_encoding or DateStyle; the connection was closed.` | 5.2 |

## 5. Connection

pgjdbc: `jdbc/PgConnection.java`, `core/v3/ConnectionFactoryImpl.java`,
`core/v3/QueryExecutorImpl.java` (`sendQueryPreamble`, `receiveParameterStatus`).

### 5.1 State, lock, internal interface

`jdbc/ConnCore.java` (CONN-01A) and `jdbc/ConnOwnSql.java` (CONN-01B). Constructor of every
class of the chain: `(Properties info, String url) throws SQLException`; `PgConnection`'s
is public. `info` is the result of `DriverUrl.parse` and is not modified afterwards.

Internal interface (package-private; "lock held" unless marked *free*):

```java
abstract class ConnCore extends ConnectionRoot
    final ReentrantLock lock;                           // free; the lock of 1.5 rule 1
    final Properties info;    final String url;         // free; immutable
    final Map<String, Class<? extends PGobject>> customTypes;   // the addDataType registry (5.6)
    final Map<Long, ColumnInfo> columnInfo;             // cache of 11.2; key (table OID << 16) | attribute number
    final PqConnection pq() throws SQLException;        // the open pq connection; 08003 when closed
    final void checkOpen() throws SQLException;         // 08003 when closed
    final PqCancel cancelHandle();                      // free; null after close
    final void addWarning(SQLWarning warning);          // appends to the connection's chain
    final void drainNotices(Consumer<SQLWarning> sink) throws SQLException;   // takeNotices -> Errors.warning -> sink
    final SQLException failure(PqException e);          // Errors.pq; closes the connection when the library reports it BAD
    final boolean logServerErrorDetail();               // free; the property
    public boolean isClosed();                          // free; a volatile flag
    public void close() throws SQLException;
    public SQLWarning getWarnings() throws SQLException;
    public void clearWarnings() throws SQLException;

abstract class ConnOwnSql extends ConnCore
    final PqResult execOwn(String sql) throws SQLException;
    final PqResult queryOwn(String sql, String... parameters) throws SQLException;
    final boolean standardConformingStrings();          // the value cached by checkSession
    final ConversionContext conversionContext();
    final TypeLookup typeLookup();                      // the connection's CatalogTypeLookup
    final void checkSession() throws SQLException;
    String beginSql();                                                        // hook
    int beginExecute(NativeStatement first, boolean atomic) throws SQLException;   // hook
    void endExecute(int token, boolean failed) throws SQLException;           // hook
```

- `execOwn(sql)` runs a statement of the driver itself with `pq().exec(sql)`: no implicit
  `BEGIN`, no autosave, no timer. Notices go to the connection's chain. An error result
  raises `Errors.server(result, logServerErrorDetail())`; a `PqException` raises
  `failure(e)`. It returns the result.
- `queryOwn(sql, parameters)` is the same through `pq().execParams(sql, null, values,
  null)` with the parameters as UTF-8 text (a null parameter is SQL NULL); for catalog
  lookups. Inside a failed transaction such a lookup fails with the server's `25P02`, as
  in pgjdbc.
- `conversionContext()` is `new ConversionContext(TimeZone.getDefault(), typeLookup(),
  stringtype is "unspecified", convertBooleanToNumeric)` (TYPES 5.3), built per call.
- The hooks in `ConnOwnSql`: `beginSql()` returns `BEGIN`; `beginExecute` returns 0;
  `endExecute` calls `checkSession()`. Sections 6.1 and 6.5 override them. The statement
  layer calls `beginExecute` before the first statement of a call and `endExecute` after
  the last or after the first failure (7.2), always in pairs.
- `ColumnInfo` (`jdbc/ColumnInfo.java`, created complete by CONN-00): an immutable holder
  with the package-private final fields `String schema`, `String table`, `String column`,
  `int nullable` (a `ResultSetMetaData.columnNullable...` constant), `boolean
  autoIncrement` and a constructor taking them in this order.

### 5.2 Open, session setup, session check

`ConnCore`'s constructor, in this order: `ConninfoBuilder.build(info,
TimeZone.getDefault())`; `PqConnection.connect(conninfo, loginTimeoutMillis)` (3.5; a
`PqException` becomes `Errors.pq`); `PqCancel.of(pq)`; the registry `customTypes` gets one
entry per property named `datatype.<type>` (the class is loaded with the driver's class
loader, then the thread's context class loader; a class that is missing or is no
`PGobject` raises `PSQLException` `22023`). When any step after the connect fails the pq
connection is closed before the exception leaves. No SQL statement is sent at open by
`ConnCore`. `ConnOwnSql`'s constructor calls `checkSession()`.

`checkSession()`: reads `pq().parameterStatus` of `client_encoding`, `DateStyle` and
`standard_conforming_strings`; caches the last as a boolean (`on`); when the encoding is
not `UTF8` and the property `allowEncodingChanges` is false, or `DateStyle` does not start
with `ISO`, it closes the connection and raises `08006` (4.4). pgjdbc:
`QueryExecutorImpl.receiveParameterStatus`.

What a session looks like after open **(probe A2, B)**: `DateStyle` `ISO, MDY`;
`TimeZone` the JVM default zone; `client_encoding` `UTF8`; `extra_float_digits` 1;
`application_name` as 3.2; `standard_conforming_strings` `on`; autocommit on; isolation
`TRANSACTION_READ_COMMITTED`; holdability `CLOSE_CURSORS_AT_COMMIT`; schema `public`.

### 5.3 Close, isValid, abort, network timeout

- `close()` (`ConnCore`): idempotent. Sets the closed flag, takes the lock, closes the
  cancel handle and the pq connection, releases the lock. It sends nothing: an open
  transaction is rolled back by the server when the session ends. A thread that is inside
  a statement of this connection makes `close()` wait; use `cancel` or `abort` to
  interrupt. Result sets already read stay readable (they are heap data); every method
  that needs the connection raises `08003` afterwards **(probe A8)**.
- `isClosed()`: the flag; no lock.
- `isValid(int timeout)` (`jdbc/ConnValid.java`, CONN-06): negative timeout raises
  `Errors.invalidValue("timeout", timeout)`; a closed connection gives false; else it runs
  the empty statement `pq().exec("")` under the lock, with the cancel timer of 7.6 armed
  for `timeout` seconds when it is positive. True for a result of status `EMPTY_QUERY`,
  and also for a server error `25P02` (a failed transaction is still a valid connection);
  false for anything else. It never throws for a broken connection.
- `abort(Executor executor)`: null raises `SQLException` (`executor is null`). Else: set
  the closed flag at once; then `executor.execute` a task that calls
  `cancelHandle().cancel()` (ignoring a failure), takes the lock and closes the pq
  connection. Abort on a closed connection does nothing.
- `setNetworkTimeout(Executor, int milliseconds)`: negative raises `invalidValue`; the
  value is stored and returned by `getNetworkTimeout()`; it has no other effect (there is
  no network).

### 5.4 Session methods

`jdbc/ConnSettings.java` (CONN-05). Each checks `checkOpen()` first. SQL is sent with
`execOwn`.

| Method | Behavior |
|---|---|
| `getCatalog()` | `SELECT pg_catalog.current_database()`, once; cached. |
| `setCatalog(String)` | nothing (after the open check). |
| `getSchema()` | `SELECT pg_catalog.current_schema()`; the text of the single value, or null. |
| `setSchema(String schema)` | null: `SET SESSION search_path TO DEFAULT`; else `SET SESSION search_path TO ` followed by `pq().escapeLiteral(schema)`. |
| `getClientInfo(String name)` | `ApplicationName`: `pq().parameterStatus("application_name")`; any other name: null. |
| `getClientInfo()` | a new `Properties` with `ApplicationName` only. |
| `setClientInfo(String name, String value)` | `ApplicationName`: `SET application_name = ` + `escapeLiteral(value)` (null as the empty string). Any other name: adds the `SQLWarning` of 4.4 to the connection and changes nothing. A failure, the closed check included, is wrapped in an `SQLClientInfoException` whose failed-properties map has the name with `ClientInfoStatus.REASON_UNKNOWN`. |
| `setClientInfo(Properties)` | null or empty: nothing. Else `setClientInfo("ApplicationName", ...)` when the key is present; every other key gets the warning. |
| `getTypeMap()` / `setTypeMap(Map)` | Stored (initially an empty `HashMap`). A non-empty map is refused where it would be used (10.4). |
| `getHoldability()` / `setHoldability(int)` | Initially `CLOSE_CURSORS_AT_COMMIT`; another value than the two constants raises `invalidValue("holdability", value)`. It is the default holdability of new statements and has no other effect: results are read completely, so every result set survives a commit. |
| `nativeSQL(String sql)` | `JdbcEscapes.replace(sql, standardConformingStrings())`, then `NativeSql.parse(text, scs, true, new String[0], true)`, the `sql()` of the statements joined with `;` (TYPES 4). |

### 5.5 Factories

`jdbc/ConnFactories.java` (CONN-07). pgjdbc: `PgConnection.createStatement`,
`prepareStatement`. Constructors they call (the classes exist from the skeleton parts):

```java
PgStatement(PgConnection connection, int type, int concurrency, int holdability)
PgPreparedStatement(PgConnection connection, String sql, int type, int concurrency, int holdability,
                    String[] returningColumns) throws SQLException
```

- Each factory checks `checkOpen()`. The forms without type, concurrency or holdability
  use `TYPE_FORWARD_ONLY`, `CONCUR_READ_ONLY` and `getHoldability()`.
- Type, concurrency and holdability are validated: a value that is none of the three,
  two and two JDBC constants raises `invalidValue` (the implementer checks in reference
  mode which of them pgjdbc validates and follows it).
- `TYPE_SCROLL_SENSITIVE` is accepted and behaves as `TYPE_SCROLL_INSENSITIVE`;
  `CONCUR_UPDATABLE` is accepted and stored. Both are reported back as requested. Until
  JDBC-DESIGN-FEATURES replaces `RsFeatures`, every update method of such a result set
  still raises the read-only error of 10.5.
- `prepareStatement(sql, int autoGeneratedKeys)`: `RETURN_GENERATED_KEYS` gives
  `returningColumns` `{"*"}`; `NO_GENERATED_KEYS` an empty array; another value raises
  `invalidValue("autoGeneratedKeys", value)`.
- `prepareStatement(sql, int[] columnIndexes)`: null or empty behaves as
  `prepareStatement(sql)`; otherwise `0A000` (4.4) **(probe A8)**.
- `prepareStatement(sql, String[] columnNames)`: null or empty behaves as
  `prepareStatement(sql)`; otherwise the names are the `returningColumns`.

### 5.6 `PGConnection` and wrappers

`org/stuntgres/PGConnection.java` (an empty interface from CONN-00, completed by
CONN-08) and `jdbc/ConnExtensions.java` (CONN-08). pgjdbc: `PGConnection.java`. The
methods of this design; JDBC-DESIGN-FEATURES adds the others (1.3).

| Method | Behavior |
|---|---|
| `Array createArrayOf(String typeName, Object elements) throws SQLException` | as `Connection.createArrayOf` for any Java array, primitives included (11.4). |
| `void addDataType(String type, Class<? extends PGobject> klass) throws SQLException` | puts the pair into `customTypes`. |
| `void addDataType(String type, String className)` (deprecated in pgjdbc; declared `@Deprecated`) | loads the class as 5.2 does; a failure is a `RuntimeException` as in pgjdbc. |
| `int getBackendPID()` | `pq().backendPid()`. |
| `void cancelQuery() throws SQLException` | `cancelHandle().cancel()`; no lock; nothing on a closed connection. |
| `String escapeIdentifier(String identifier) throws SQLException`, `String escapeLiteral(String literal) throws SQLException` | the pq methods of the same name (NATIVE 9.5). |
| `PreferQueryMode getPreferQueryMode()` | the property, parsed. |
| `AutoSave getAutosave()`, `void setAutosave(AutoSave autoSave)` | the field of 6.5. |
| `int getPrepareThreshold()`, `void setPrepareThreshold(int threshold)`; `int getDefaultFetchSize()`, `void setDefaultFetchSize(int fetchSize) throws SQLException` (negative: `invalidValue`); `boolean getAdaptiveFetch()`, `void setAdaptiveFetch(boolean adaptiveFetch)` | stored values, initialized from the properties; the fetch size is the initial `getFetchSize()` of new statements; no other effect. |
| `Map<String, String> getParameterStatuses()`, `String getParameterStatus(String parameterName)` | `pq().parameterStatus` for the names `docs/LIBSPQ.md` 5.2 lists, as an unmodifiable map; null for an unknown name. |
| `unwrap(Class<T> iface)`, `isWrapperFor(Class<?> iface)` | true and `this` when `iface.isInstance(this)`; else false and `Errors.cannotUnwrap`. The same rule in every class of this layer. |

## 6. Transactions

pgjdbc: `PgConnection.java` (`setAutoCommit`, `commit`, `rollback`, `setTransactionIsolation`,
`setReadOnly`, `setSavepoint`), `core/v3/QueryExecutorImpl.java` (`sendQueryPreamble`,
`sendAutomaticSavepoint`), `jdbc/PSQLSavepoint.java`. The statements of this section give
the same answers on PostgreSQL 18.6 and on the stuntgres server **(probe B)**.

### 6.1 State and implicit BEGIN

The transaction state is never a Java flag: it is `pq().transactionStatus()` (`IDLE`,
`INTRANS`, `INERROR`), read when needed. The only Java state is `boolean autoCommit`
(initially true). A `BEGIN`, `COMMIT` or `ROLLBACK` that the application sends as SQL
therefore needs no special handling.

`jdbc/ConnTxn.java` (CONN-02) overrides the hooks:

- `beginExecute(first, atomic)`: when (autocommit is off or `atomic`) and the status is
  `IDLE`: `execOwn(beginSql())`. The token is 1 when a `BEGIN` was sent while autocommit
  is on (the driver must end that transaction itself), else 0.
- `endExecute(token, failed)`: when the token has bit 1: status `INTRANS` → `execOwn("COMMIT")`,
  status `INERROR` → `execOwn("ROLLBACK")`, status `IDLE` → nothing (the application's own
  statement ended the transaction). Then `super.endExecute`.

`atomic` is true for a call with more than one statement and for every batch (decision
2). A single statement in autocommit mode is sent without `BEGIN`.

### 6.2 `setAutoCommit`, `commit`, `rollback`

| Method | Behavior |
|---|---|
| `getAutoCommit()` | the flag. |
| `setAutoCommit(boolean on)` | No change of value: nothing. Else, when the old value is false and the status is not `IDLE`: `COMMIT` first. With `readOnlyMode` `always` and a read-only connection the session characteristics of 6.3 are sent again for the new mode. |
| `commit()` | Autocommit on: `25P01`. Status not `IDLE`: `execOwn("COMMIT")`; when the command tag of the answer is `ROLLBACK` (the transaction had failed) raise `25P02` (4.4; **(map)**: the implementer confirms class and state in reference mode). |
| `rollback()` | Autocommit on: `25P01`. Status not `IDLE`: `execOwn("ROLLBACK")`. |

After a failed statement inside a transaction every further statement is answered by the
server with `25P02` until `rollback()`; the driver adds nothing **(probe A4)**.

### 6.3 Isolation level and read-only

`jdbc/ConnTxnOptions.java` (CONN-03).

| Method | Behavior |
|---|---|
| `getTransactionIsolation()` | `SHOW TRANSACTION ISOLATION LEVEL`; the text `read uncommitted`, `read committed`, `repeatable read`, `serializable` (case-insensitive) maps to the JDBC constant; another text raises `99999`. |
| `setTransactionIsolation(int level)` | Status not `IDLE`: `25001`. A level without a name (`TRANSACTION_NONE`, other numbers): `0A000`. Else `SET SESSION CHARACTERISTICS AS TRANSACTION ISOLATION LEVEL <NAME>` with the name in upper case. |
| `isReadOnly()` | the flag (initially the property `readOnly`). |
| `setReadOnly(boolean)` | Status not `IDLE`: `25001`. Stores the flag. With `readOnlyMode` `always` and autocommit on: `SET SESSION CHARACTERISTICS AS TRANSACTION READ ONLY` or `... READ WRITE`. |
| hook `beginSql()` | `BEGIN READ ONLY` when the flag is set and `readOnlyMode` is not `ignore`; else `BEGIN`. |

`readOnlyMode`: `ignore` sends nothing ever; `transaction` (default) only changes the
implicit `BEGIN`; `always` also sets the session characteristics while autocommit is on.
When the property `readOnly` is true the constructor applies `setReadOnly(true)`'s effect
through a private method (1.5 rule 3).

### 6.4 Savepoints

`jdbc/ConnSavepoints.java` and `jdbc/PSQLSavepoint.java` (CONN-04A).

```java
public class PSQLSavepoint implements Savepoint
    public PSQLSavepoint(int id);          public PSQLSavepoint(String name);
    public int getSavepointId() throws SQLException;        // 3B000 when released; 42809 when named
    public String getSavepointName() throws SQLException;   // 3B000 when released; 42809 when unnamed
    public void invalidate();
    public String getPGName() throws SQLException;          // 3B000 when released
```

- `getPGName()`: `JDBC_SAVEPOINT_<id>` for an unnamed savepoint; for a named one the name
  as a quoted identifier (`"` doubled, in double quotes).
- `setSavepoint()` takes the next id of a per-connection counter starting at 1;
  `setSavepoint(String name)` uses the name. Both: autocommit on raises `25P01`; when the
  status is `IDLE` send `beginSql()` first; then `SAVEPOINT <pg name>`.
- `rollback(Savepoint s)`: `ROLLBACK TO SAVEPOINT <pg name>`. `releaseSavepoint(s)`:
  `RELEASE SAVEPOINT <pg name>`, then `invalidate()`. A savepoint that is no
  `PSQLSavepoint` raises `ClassCastException` as in pgjdbc.

### 6.5 Autosave

`jdbc/ConnAutosave.java` (CONN-04B). Field `AutoSave autosave` (property `autosave`),
field `boolean cleanupSavepoints`; package-private `AutoSave autosave()` and `void
autosave(AutoSave value)` for 5.6. Mode `ALWAYS` only (decision 14):

- `beginExecute`: after `super`, when the status is `INTRANS`, autocommit is off and the
  first word of `first.sql()` is none of `BEGIN`, `START`, `COMMIT`, `END`, `ROLLBACK`,
  `ABORT`, `SAVEPOINT`, `RELEASE`, `PREPARE` (ASCII case-insensitive, after white space):
  `execOwn("SAVEPOINT STUNTGRES_AUTOSAVE")` and set bit 2 of the token.
- `endExecute`: with bit 2: failed → `ROLLBACK TO SAVEPOINT STUNTGRES_AUTOSAVE`; not
  failed and `cleanupSavepoints` → `RELEASE SAVEPOINT STUNTGRES_AUTOSAVE`. Then `super`.

Effect: with `autosave=always` a failed statement does not fail the transaction; the
next statement runs. pgjdbc uses its own savepoint name; no test names the savepoint.

## 7. Statement

pgjdbc: `jdbc/PgStatement.java`, `core/v3/QueryExecutorImpl.java`,
`core/CommandCompleteParser.java`, `jdbc/StatementCancelTimerTask.java`.

### 7.1 State and internal interface

`jdbc/StmtCore.java` (STMT-01A). Constructor of the chain: `(PgConnection connection, int
type, int concurrency, int holdability)`.

```java
abstract class StmtCore extends StatementRoot
    final PgConnection connection;
    final int resultSetType, resultSetConcurrency, resultSetHoldability;
    // state, lock held
    boolean closed;
    int maxRows, maxFieldSize, fetchSize, fetchDirection;     // 0, 0, connection default, FETCH_FORWARD
    boolean escapeProcessing;          // true
    long timeoutMillis;                // property queryTimeout * 1000
    boolean poolable, closeOnCompletion;
    final List<StmtResult> results;    int resultIndex;       // the results of the last execution
    final List<PqResult> keyParts;     boolean keysRequested;  // generated keys (8.3)
    final List<PgResultSet> openResultSets;
    volatile boolean executing;        // free; true while a statement of this object is in the pq layer
    long lastOid;                      // oidValue() of the last command result (7.8)
    final void checkOpen() throws SQLException;               // 08003 (connection closed), then 55000
    final void addWarning(SQLWarning warning);
    final void closeResults() throws SQLException;            // closes open result sets, clears results, keys, warnings
    void startTimer();                 // hook, nothing
    void stopTimer();                  // hook, nothing
    void resultSetClosed(PgResultSet resultSet) throws SQLException;   // hook: removes it from openResultSets
    public void close() throws SQLException;        public boolean isClosed() throws SQLException;
    public Connection getConnection() throws SQLException;
    public SQLWarning getWarnings() throws SQLException;      public void clearWarnings() throws SQLException;
```

`StmtResult` (`jdbc/StmtResult.java`, STMT-01B): a final holder with `final ResultData
rows` (null for an update count) and `final long count` (-1 for a result set), and the
field `PgResultSet resultSet` that is filled when the result set is first handed out.

`close()`: idempotent; `closeResults()`, then the closed flag. It needs no round trip.
`isClosed()` reads the flag under the lock. A statement of a closed connection answers
`08003` from `checkOpen()` **(probe A8)**.

### 7.2 Execution path

`jdbc/StmtRun.java` (STMT-01B):

```java
abstract class StmtRun extends StmtCore
    final List<NativeStatement> parse(String sql, boolean withParameters, String[] returningColumns) throws SQLException;
    final void runText(String sql, String[] returningColumns) throws SQLException;
    final void runParsed(List<NativeStatement> statements, ParamValue[] parameters) throws SQLException;
    final StmtResult execOne(NativeStatement statement, ParamValue[] parameters, int offset) throws SQLException;
    final PgResultSet resultSet(int index) throws SQLException;
    public boolean execute(String sql) throws SQLException;
```

`parse`: null text raises `invalidValue("sql", null)`; when `escapeProcessing`,
`JdbcEscapes.replace(sql, scs)`; then `NativeSql.parse(text, scs, withParameters,
returningColumns, quoteReturningIdentifiers)` with `scs` =
`connection.standardConformingStrings()` (TYPES 3, 4). A text with a NUL character raises
`08P01` here (decision 4). `keysRequested` is set when `returningColumns` is not empty.

`runText(sql, returningColumns)`: `checkOpen()`, `parse(sql, false, returningColumns)`,
`runParsed(statements, null)`.

`runParsed(statements, parameters)`, lock held:

1. `checkOpen()`; `closeResults()`.
2. `token = connection.beginExecute(first statement, statements.size() > 1)` (nothing when
   the list is empty: the results are then empty too).
3. `executing = true`; `startTimer()`.
4. For each statement in order: `execOne(statement, parameters, offset)`; the offset
   advances by `statement.parameterCount()`. The first exception ends the loop.
5. `stopTimer()`; `executing = false`; `connection.endExecute(token, failed)`. An
   exception of `endExecute` is added to the first one with `setNextException`, or thrown
   when there was none.
6. On failure: the results stay empty and the exception is thrown. Otherwise the list of
   `StmtResult` is the statement's result list and `resultIndex` is 0.

`execOne`: sends one statement with `pq.execParams(statement.sql(), oids, values,
formats)` built from `ParamValue.oid()`, `bytes()`, `format()` of its parameters (TYPES
8.1; no parameters: empty arrays), drains notices into this statement's warning chain,
and classifies the result by `PqResult.status()`:

| Status | Outcome |
|---|---|
| `TUPLES_OK` | When `keysRequested` and `statement.returning()`: the result is added to `keyParts` and the outcome is the update count parsed from `commandTuples()`. Else a result set over `new PqResultData(result)`. |
| `COMMAND_OK` | Update count: `commandTuples()` as a long; empty text gives 0 **(probe A6: DDL, `SET`, `DO` give 0; `SELECT ... INTO` gives 1)**. |
| `EMPTY_QUERY` | Update count 0 **(probe A6)**. |
| `COPY_OUT` | Read and discard `pq.getCopyData()` until it returns null, read `pq.getResult()` until null, raise `0A000` (4.4) **(probe A6)**. |
| `COPY_IN`, `COPY_BOTH` | `pq.putCopyEnd(<the text of the 0A000 row of 4.4>)`, read `pq.getResult()` until null and raise the server's error result (`57014`) **(probe A6)**. |
| `FATAL_ERROR`, `NONFATAL_ERROR`, `BAD_RESPONSE` | `Errors.server(result, detail)`. |

A `PqException` becomes `connection.failure(e)`. The connection stays usable after every
row of this table **(probe A6)**. Whether a statement produced rows is decided by the
server's answer, never by `NativeStatement.kind()` (TYPES 3.3 K7).

`execute(String sql)`: lock; `runText(sql, new String[0])`; true when the first result
is a result set.

### 7.3 `executeQuery`, `executeUpdate`, `executeLargeUpdate`

`jdbc/StmtExecute.java` (STMT-01C). All run `runText(sql, new String[0])` first.

- `executeQuery(sql)`: more than one result raises `0100E`; a first result that is no
  result set (or no result at all) raises `02000` (4.4); else the result set
  (`resultSet(0)`, 7.4).
- `executeLargeUpdate(sql)`: any result set among the results raises `0100E`; returns the
  count of the first result, or 0 when there is none.
- `executeUpdate(sql)`: the same, as an int; a count above `Integer.MAX_VALUE` gives
  `Statement.SUCCESS_NO_INFO`.

Both checks run after the statements have run: an `INSERT` passed to `executeQuery` is
executed and then reported **(map 4)**.

### 7.4 Results

`jdbc/StmtResults.java` (STMT-02). It uses `StmtRun.resultSet(int index)`, which creates, on
first use, `new PgResultSet(this, connection, result.rows, resultSetType,
resultSetConcurrency, resultSetHoldability, maxRows, maxFieldSize)`, stores it in the
`StmtResult` and in `openResultSets`, and returns it (null for a count or a missing
result).

| Method | Behavior |
|---|---|
| `getResultSet()` | the result set of the current result, or null when it is a count or there is none. |
| `getLargeUpdateCount()` | the count of the current result; -1 when it is a result set or there is none. |
| `getUpdateCount()` | the same as int; above `Integer.MAX_VALUE`: `SUCCESS_NO_INFO`. |
| `getMoreResults()` | `getMoreResults(CLOSE_ALL_RESULTS)` **(map 4)**. |
| `getMoreResults(int current)` | `CLOSE_CURRENT_RESULT` closes the current result set, `CLOSE_ALL_RESULTS` every open one, `KEEP_CURRENT_RESULT` none; another value raises `invalidValue`. Then `resultIndex` advances (not beyond the size); true when the new current result is a result set. |

Recorded sequence for `SELECT 1; /* only a comment */; INSERT ...; ; SELECT 2`
**(probe A5)**: result set; count 0; count 1; result set; then `getMoreResults()` false
and `getUpdateCount()` -1.

### 7.5 Limits and hints

`jdbc/StmtLimits.java` (STMT-03).

| Method | Behavior |
|---|---|
| `setMaxRows(int)` / `getMaxRows()` | negative: `invalidValue`. A result set created afterwards shows at most that many rows (0: all) **(probe A6)**; update counts are not affected. |
| `setMaxFieldSize(int)` / `getMaxFieldSize()` | negative: `invalidValue`. Applied by the getters of 10.4. |
| `setFetchSize(int)` / `getFetchSize()` | negative: `invalidValue`. A stored hint (plan J11): every result is read completely. |
| `setFetchDirection(int)` / `getFetchDirection()` | one of `FETCH_FORWARD`, `FETCH_REVERSE`, `FETCH_UNKNOWN`, else `invalidValue`. Stored. |
| `setEscapeProcessing(boolean)` | stored; read by `parse` (7.2). |
| `setCursorName(String)` | nothing. |
| `setPoolable(boolean)` / `isPoolable()` | stored; initially false for a `Statement` **(probe A8)**, true for a `PreparedStatement` (set by `PsCore`'s constructor). |
| `closeOnCompletion()` / `isCloseOnCompletion()` | sets the flag. Override of the hook `resultSetClosed`: after `super`, when the flag is set and `openResultSets` is empty and no result set remains to be handed out, the statement closes itself. |
| `getResultSetType()`, `getResultSetConcurrency()`, `getResultSetHoldability()` | the constructor's values. |

Every method checks `checkOpen()`.

### 7.6 Cancel and query timeout

`jdbc/StmtCancel.java` and `jdbc/CancelTimer.java` (STMT-04).

```java
final class CancelTimer
    static Object schedule(Runnable task, long delayMillis);   // one shared daemon thread, started on first use
    static void cancel(Object handle);                         // the task does not start after this returns;
                                                               // waits when it is running
```

- `cancel()`: no lock. When `executing` is true, `connection.cancelHandle().cancel()`;
  otherwise nothing. A closed statement or connection: nothing. A `PqException` becomes
  `Errors.pq`. The thread that is inside the statement gets the server's error `57014`
  as a `PSQLException` through the normal path (decision 12), and the connection stays
  usable; in a transaction the transaction is failed as after any error.
- `setQueryTimeout(int seconds)` / `getQueryTimeout()`: negative raises `invalidValue`;
  stored as `timeoutMillis`.
- Hooks: `startTimer()` schedules, when `timeoutMillis` is positive, a task that calls
  `cancel()` of this statement; `stopTimer()` calls `CancelTimer.cancel(handle)`. Because
  `stopTimer` waits for a task that is already running, a cancel request of this
  execution can never reach a later statement of the connection.
- The shared thread is a daemon named `stuntgres-jdbc-timer`; it holds no reference to a
  statement that has no pending task.
- Interrupting the Java thread does not cancel a statement (NATIVE 2.2).

### 7.7 Warnings

Notices of an execution are chained on the statement in arrival order (`getWarnings()`
returns the first; `clearWarnings()` and the next execution drop them); notices of the
driver's own statements go to the connection (5.1). A result set has no warnings:
`ResultSet.getWarnings()` is null. pgjdbc: `PgStatement.addWarning`.

### 7.8 `PGStatement`

`org/stuntgres/PGStatement.java` (an empty interface from STMT-00, completed by STMT-05)
and `jdbc/StmtExtensions.java` (STMT-05). pgjdbc: `PGStatement.java`.

| Member | Behavior |
|---|---|
| `long DATE_POSITIVE_INFINITY`, `DATE_NEGATIVE_INFINITY`, `DATE_POSITIVE_SMALLER_INFINITY` | the values of `DateTimeText.POSITIVE_INFINITY`, `NEGATIVE_INFINITY`, `POSITIVE_SMALLER_INFINITY` (TYPES 12 point 7), written as literals; a unit test compares them. |
| `long getLastOID() throws SQLException` | `oidValue()` of the last command result of the last execution; 0 when none. `execOne` records it in a field of `StmtCore` (`long lastOid`). |
| `void setUseServerPrepare(boolean flag) throws SQLException`, `boolean isUseServerPrepare()` (both `@Deprecated`) | stored flag, no effect. |
| `void setPrepareThreshold(int threshold) throws SQLException`, `int getPrepareThreshold()` | stored, initially the connection's; no effect (plan B8). |
| `void setAdaptiveFetch(boolean adaptiveFetch)`, `boolean getAdaptiveFetch()` | stored; no effect. |
| `unwrap`, `isWrapperFor` | the rule of 5.6. |

## 8. Batches and generated keys

pgjdbc: `PgStatement.java` (`addBatch`, `internalExecuteBatch`), `jdbc/BatchResultHandler.java`,
`PgPreparedStatement.java` (`addBatch`).

### 8.1 Statement batches

`jdbc/StmtBatch.java` and `jdbc/BatchRunner.java` (BATCH-01).

```java
final class BatchRunner
    static long[] run(StmtRun statement, List<List<NativeStatement>> entries, List<ParamValue[]> parameters)
        throws SQLException;          // parameters: null, or one array per entry
```

- `addBatch(String sql)`: `checkOpen()`; `parse(sql, false, none)`; more than one
  statement raises `0A000` (4.4); the entry is appended. `clearBatch()` empties the list.
- `executeLargeBatch()`: an empty batch returns an empty array without touching the
  database. Otherwise `BatchRunner.run`, and the batch is cleared whether it succeeds or
  fails. `executeBatch()` is the same as int values (above `Integer.MAX_VALUE`:
  `SUCCESS_NO_INFO`).
- `BatchRunner.run`, lock held: `closeResults()`; `beginExecute(first statement, true)`;
  timer and `executing` as in 7.2; each entry's statements through `execOne`; the count of
  an entry is the count of its result, 0 for a result set (the rows are dropped)
  **(probe A7: a batch entry `SELECT 1` gives 0)**; the first failure stops the batch;
  `endExecute(token, failed)`.
- On failure the exception is a `BatchUpdateException` with the message of 4.4 (`{0}` the
  zero-based entry index, `{1}` the entry's native SQL, `{2}` the cause's message), the
  SQLState of the cause, vendor code 0, a `long[]` count array of the batch size in which
  **every** element is `Statement.EXECUTE_FAILED`, and the cause both as `getCause()` and
  as `getNextException()` **(probe A7: `[-3, -3, -3]` whether the failure is at the first,
  a middle or the last entry, in autocommit mode and in a transaction)**.
- In autocommit mode nothing of a failed batch is stored (decision 2); in a transaction
  the transaction is failed **(probe A7)**.
- Rewriting of batched inserts is not done (decision 5).

### 8.2 PreparedStatement batches

`jdbc/PsBatch.java` (BATCH-02): `addBatch()` checks that every parameter has a value
(9.1) and appends a copy of the parameter array; `clearBatch()` also drops these copies;
`executeBatch()` and `executeLargeBatch()` are those of 8.1 with one parameter array per
entry (the override passes them to `BatchRunner.run`). The parameter values stay set
after `addBatch()`. `addBatch(String)` is refused (9.1).

### 8.3 Generated keys

`jdbc/StmtKeys.java` (KEYS-01). pgjdbc: `PgStatement.executeInternal`, `getGeneratedKeys`;
the statement text is TYPES 3.4.

- The request reaches `NativeSql.parse` as `returningColumns`: `{"*"}` for
  `RETURN_GENERATED_KEYS`, the names for the name form. The text sent ends with a line
  feed and `RETURNING *`, or `RETURNING "id", "V x"` (names quoted unless
  `quoteReturningIdentifiers` is false) **(probe A8)**; nothing is appended to a statement
  that is no `INSERT`, `UPDATE`, `DELETE` (or `WITH`) or that has a `RETURNING` clause.
- `execute(sql, int)`, `executeUpdate(sql, int)`, `executeLargeUpdate(sql, int)`: the
  constant is validated as in 5.5; then as the form without it, through `runText(sql,
  returningColumns)`. The `int[]` forms: null or empty as without; else `0A000`. The
  `String[]` forms: null or empty as without; else the names.
- With keys requested the result of the statement is an update count (7.2), so
  `executeUpdate` returns the number of rows **(probe A8)**.
- `getGeneratedKeys()`: `checkOpen()`; a result set (`TYPE_SCROLL_INSENSITIVE`,
  `CONCUR_READ_ONLY`, not counted for `closeOnCompletion`) over `new PqResultData(keyParts)`,
  which concatenates the rows of every part: after a batch it holds the keys of all
  entries. When no keys were requested or no statement returned any, an empty result set
  without columns (`new MemoryResultData(new String[0], new int[0], List.of())`)
  **(probe A8: 0 columns)**. The same object is returned until the next execution.

## 9. PreparedStatement

pgjdbc: `jdbc/PgPreparedStatement.java`, `jdbc/PgParameterMetaData.java`.

### 9.1 Parameter store and execution

`jdbc/PsCore.java` (PSTMT-01). Constructor of the chain: `(PgConnection connection, String
sql, int type, int concurrency, int holdability, String[] returningColumns) throws
SQLException`; it parses the text once (`parse(sql, true, returningColumns)`: escape
processing is always on for a prepared statement), sets `poolable`, and allocates the
parameter array (`ParamValue[]`, one slot per `?` of all statements, null = not set).
More than 65535 parameters raise `22023` (4.4).

```java
abstract class PsCore extends PreparedRoot
    final String sql;                                   // the text as given
    final List<NativeStatement> statements;
    final ParamValue[] parameters;
    final void bind(int parameterIndex, ParamValue value) throws SQLException;   // lock taken inside
    final ConversionContext context();                  // connection.conversionContext()
    final void checkParameters() throws SQLException;   // 22023 "No value specified for parameter {n}."
    public boolean execute() throws SQLException;       public ResultSet executeQuery() throws SQLException;
    public int executeUpdate() throws SQLException;     public long executeLargeUpdate() throws SQLException;
    public void clearParameters() throws SQLException;
    public String toString();                           // the text as given (decision 15)
```

- `bind`: `checkOpen()`; an index outside 1..n raises `Errors.parameterIndex`; stores the
  value.
- `execute()`: `checkParameters()`, `runParsed(statements, parameters)`; the result rules
  are those of 7.2 to 7.4, and `executeQuery()`, `executeUpdate()`, `executeLargeUpdate()`
  apply the checks of 7.3. Parameters keep their values for the next execution;
  `clearParameters()` unsets all.
- Parameters are numbered over all statements of the text; statement k receives its
  slice (TYPES 3). A call with several statements is atomic in autocommit mode
  **(probe A5)**.
- Refused with `42809` (4.4): `execute(String)`, `executeQuery(String)`,
  `executeUpdate(String)`, `executeLargeUpdate(String)`, their nine generated-keys forms
  and `addBatch(String)`. `PsCore` overrides these fourteen methods.
- A native `$1` in the text is not a JDBC parameter (TYPES decision 2): the server
  answers.

### 9.2 Setters by group

Each setter is one statement: `bind(parameterIndex, <function>(...))`, where the function
and its arguments are exactly those that the method of the same name and parameter list
of `SetterCalls` uses (test tree, `core/SetterCalls.java`, part SET-05; TYPES 8.9). That
class is the specification of the mapping; the groups follow the function classes:

| File (part) | Setters | Function class (TYPES) |
|---|---|---|
| `jdbc/PsBasicSetters.java` (PSTMT-02) | `setNull` (two forms), `setBoolean`, `setByte`, `setShort`, `setInt`, `setLong`, `setFloat`, `setDouble`, `setBigDecimal`, `setString`, `setBytes` | `BasicSetters` (8.2, 8.3) |
| `jdbc/PsDateTimeSetters.java` (PSTMT-03) | `setDate`, `setTime`, `setTimestamp`, each without and with a `Calendar` | `DateTimeSetters` (8.4) |
| `jdbc/PsStreamSetters.java` (PSTMT-04) | `setAsciiStream(int, InputStream, int)`, `setUnicodeStream`, `setBinaryStream` (three forms), `setCharacterStream(int, Reader, int)`, `setCharacterStream(int, Reader)` | `StreamSetters` (8.6) |
| `jdbc/PsObjectSetters.java` (PSTMT-05) | `setObject` (three forms), `setArray` | `ObjectSetters` (8.7, 8.8) |

`setArray(i, array)`: null binds `BasicSetters.setNull(Types.ARRAY, context())`; else
`ObjectSetters.setArray(array.getBaseTypeName(), array.toString(), context())`. A setter
whose function raises leaves the parameter as it was. The setters section 2 marks
"rejected" stay in the root; `setBlob`, `setClob(int, Clob)` and `setSQLXML` belong to
JDBC-DESIGN-FEATURES.

### 9.3 Metadata before execution, `ParameterMetaData`

`jdbc/PsMetaData.java` and `jdbc/PgParameterMetaData.java` (PSTMT-90). Both answers come
from describing an unnamed prepared statement (plan B8): `pq.prepare("", sql, oids)` then
`pq.describePrepared("")`, for the first statement of the text, with the OIDs of the
parameters that are set and 0 for the others; no `BEGIN` is sent for it. An error result
raises `Errors.server`.

- `getMetaData()`: when the statement has a current open result set, its metadata. Else
  the description: a result without columns gives null, otherwise `new
  PgResultSetMetaData(connection, new PqResultData(description))`.
- `getParameterMetaData()`: `new PgParameterMetaData(connection, int[] oids)` with the
  described parameter types of **all** statements of the text in parameter order (one
  describe per statement that has parameters).

```java
public class PgParameterMetaData implements ParameterMetaData
    public PgParameterMetaData(PgConnection connection, int[] oids);
```

| Method | Answer **(map 6)** |
|---|---|
| `getParameterCount()` | the number of OIDs |
| `getParameterType(i)`, `getParameterTypeName(i)`, `getParameterClassName(i)` | `sqlType()`, `name()`, `className()` of `TypeTable.find(oid, lookup)` |
| `isSigned(i)` | `TypeTable.isSigned(oid)` |
| `getPrecision(i)`, `getScale(i)` | 0 |
| `isNullable(i)` | `parameterNullableUnknown` |
| `getParameterMode(i)` | `parameterModeIn` |
| an index outside 1..n | `Errors.parameterIndex` |
| `unwrap`, `isWrapperFor` | the rule of 5.6 |

## 10. ResultSet

pgjdbc: `jdbc/PgResultSet.java`.

### 10.1 Data and internal interface

A result set reads from a `ResultData`, so that it can stand on a pq result or on rows
built in memory (`Array.getResultSet`, the empty generated-keys result, the synthetic
results of JDBC-DESIGN-METADATA).

```java
interface ResultData                                     // jdbc/ResultData.java, RS-00A
    int rowCount();              int columnCount();
    String name(int column);     int typeOid(int column);    int typeModifier(int column);
    int tableOid(int column);    int tableColumn(int column);     // 0 when not a table column
    boolean isNull(int row, int column);
    String string(int row, int column);                  // null for SQL NULL
    byte[] bytes(int row, int column);                   // the UTF-8 bytes of the text; null for SQL NULL
final class PqResultData implements ResultData           // jdbc/PqResultData.java, RS-00B
    PqResultData(PqResult result);
    PqResultData(List<PqResult> parts);                  // rows concatenated; columns of the first part; empty list: no columns
final class MemoryResultData implements ResultData       // jdbc/MemoryResultData.java, RS-00B
    MemoryResultData(String[] names, int[] typeOids, List<String[]> rows);   // modifier -1, table 0; a null cell is SQL NULL
```

Rows and columns are zero-based here; an index out of range raises
`IndexOutOfBoundsException`.

`jdbc/RsCore.java` (RS-01A). Constructor of the chain: `(StmtCore statement, PgConnection
connection, ResultData data, int type, int concurrency, int holdability, int maxRows, int
maxFieldSize)`; `statement` may be null (a result set that belongs to no statement).

```java
abstract class RsCore extends ResultSetRoot
    final StmtCore statement;     final PgConnection connection;
    final int type, concurrency, holdability, maxFieldSize;
    ResultData data;              // null after close
    final int rowCount;           // data.rowCount(), limited to maxRows when maxRows > 0
    int row;                      // -1 before the first row, rowCount after the last
    int fetchSize, fetchDirection;
    final void checkOpen() throws SQLException;          // 55000 closed result set
    final void checkScrollable() throws SQLException;    // 24000 when type is TYPE_FORWARD_ONLY
```

Every public method takes the connection's lock (1.5 rule 1).

### 10.2 Navigation and lifetime

`RsCore` (RS-01A): `next`, `close`, `isClosed`, `isBeforeFirst`, `isAfterLast`, `isFirst`,
`isLast`, `getRow`, `getStatement`, `getType`, `getConcurrency`, `getCursorName` (null),
`getWarnings` (null), `clearWarnings`, `getMetaData`, `unwrap`, `isWrapperFor`.
`jdbc/RsScroll.java` (RS-01B): `previous`, `first`, `last`, `beforeFirst`, `afterLast`,
`absolute`, `relative`, `setFetchDirection`, `getFetchDirection`, `setFetchSize`,
`getFetchSize`.

| Method | Rule |
|---|---|
| `next()` | `checkOpen()`; when `row < rowCount` increment; true when `row < rowCount`. |
| `isBeforeFirst()` / `isAfterLast()` | `row < 0` / `row >= rowCount`, and false for a result without rows. |
| `isFirst()` / `isLast()` | `row == 0` / `row == rowCount - 1`, and false for a result without rows. |
| `getRow()` | `row + 1` on a row, else 0. |
| `previous`, `first`, `last`, `beforeFirst`, `afterLast`, `absolute`, `relative` | `checkScrollable()` first. `absolute(n)`: n > 0 moves to row n or after the last; n < 0 counts from the end (`-1` is the last row) or before the first; 0 moves before the first; the answer is whether the cursor is on a row. `relative(n)` moves by n from the current position, clamped the same way. `first()`/`last()` are false for a result without rows. `previous()` moves back one, to before the first at most. |
| `setFetchDirection(d)` | an unknown constant: `invalidValue`; `FETCH_REVERSE` or `FETCH_UNKNOWN` on a forward-only result set: `24000`. Stored. |
| `setFetchSize(n)` | negative: `invalidValue`. Stored. |
| `close()` | idempotent; drops `data`; calls `statement.resultSetClosed(this)` when there is a statement. |
| `getStatement()` | the statement, or null. |
| `getMetaData()` | one `PgResultSetMetaData(connection, data)` per result set, created on first use. |

The position answers at the edges (empty result, one row, the calls after the last row)
are established by the implementing parts in reference mode for both scroll types; the
table gives the model, pgjdbc gives the values (rule R11). After `close()` every method
except `close` and `isClosed` raises `55000`. A commit or rollback does not close a
result set, whatever its holdability (5.4). Closing or re-executing the statement closes
its result sets (7.1).

### 10.3 Columns and `wasNull`

`jdbc/RsColumns.java` (RS-02).

```java
abstract class RsColumns extends RsScroll
    final String text(int columnIndex) throws SQLException;     // null for SQL NULL; records wasNull
    final byte[] raw(int columnIndex) throws SQLException;
    final int oid(int columnIndex);      final int typmod(int columnIndex);    // after text(): no checks
    final PgType pgType(int columnIndex) throws SQLException;   // TypeTable.find(oid, lookup), cached per column
    final ConversionContext context();
    public int findColumn(String columnLabel) throws SQLException;
    public boolean wasNull() throws SQLException;
```

- `text(columnIndex)`: `checkOpen()`; no current row raises `24000` (4.4); an index
  outside 1..n raises `Errors.columnIndex`; records whether the value is NULL.
- `findColumn(label)` **(map 7)**: the labels are put into a map on first use, from the
  last column to the first, so that of equal labels the first column wins; the keys are
  lower-cased with `Locale.US` unless the property `disableColumnSanitiser` is true. The
  lookup tries the label as given, then lower-cased, then upper-cased. A miss raises
  `42703` (4.4). The result is 1-based.
- `wasNull()`: `checkOpen()`; the flag of the last value read.
- Every getter with a label is `getX(findColumn(label), ...)`.

### 10.4 Getters by group

A getter with an index reads `text(i)`; for NULL it returns null, `false` or 0 without
calling a conversion function (TYPES 7.1); otherwise it calls the function below with the
text, `oid(i)`, `typmod(i)`, `pgType(i)` and `context()` as that function needs. An
exception of the function is thrown unchanged.

| File (part) | Getters (index and label form) | Functions (TYPES) |
|---|---|---|
| `jdbc/RsTextGetters.java` (RS-03) | `getString` (the text itself), `getBytes`, `getAsciiStream`, `getUnicodeStream`, `getBinaryStream`, `getCharacterStream` | `TextGetters` (7.4) |
| `jdbc/RsNumberGetters.java` (RS-04) | `getBoolean`, `getByte`, `getShort`, `getInt`, `getLong`; `getFloat`, `getDouble`, `getBigDecimal` (without and with scale) | `IntegerGetters` (7.2), `DecimalGetters` (7.3) |
| `jdbc/RsDateTimeGetters.java` (RS-05) | `getDate`, `getTime`, `getTimestamp`, each without and with a `Calendar` | `DateTimeGetters` (7.5) |
| `jdbc/RsObjectGetters.java` (RS-06) | `getObject` (three forms), `getArray` | `ObjectGetters` (7.7), `ClassGetters` (7.8), which uses `JavaTimeGetters` (7.6) |

Rules that belong to this layer:

- **`maxFieldSize`** (decision 6). `RsTextGetters` declares `final String trim(int
  columnIndex, String value)` and `final byte[] trim(int columnIndex, byte[] value)`: when
  `maxFieldSize` > 0 and the column's JDBC type is `CHAR`, `VARCHAR`, `LONGVARCHAR`,
  `BINARY`, `VARBINARY` or `LONGVARBINARY`, a longer value is cut to `maxFieldSize`
  characters or bytes. `getString`, `getBytes` and the default `getObject` apply it
  **(probe A6)**; the implementer checks the stream getters in reference mode.
- **`getObject(i)`**: NULL gives null. By `pgType(i)`: JDBC type `ARRAY` → `getArray(i)`;
  type name with an entry in `connection.customTypes` → a new instance of that class
  (no-argument constructor) with `setType` and `setValue`; JDBC type `SQLXML` or
  `REF_CURSOR` → the hook `featureObject(i, type)`; else `ObjectGetters.getObject(text,
  type, typmod, context)`, trimmed as above when the result is a `String` or `byte[]`.
- **Hook** `Object featureObject(int columnIndex, PgType type) throws SQLException`
  (declared in `RsObjectGetters`): raises `Errors.notImplemented`. JDBC-DESIGN-FEATURES
  overrides it in `RsFeatures`.
- **`getObject(i, Map)`**: a null or empty map is `getObject(i)`; else `0A000` (4.4).
- **`getObject(i, Class<T> type)`**: null class raises `invalidValue("type", null)`.
  `Array` → `getArray(i)`; `Blob`, `Clob`, `NClob`, `SQLXML` → `featureObject`; else, for a
  non-NULL value, `ClassGetters.getObject(text, pgType, type, context)`. For NULL the
  type check of TYPES 7.8 still applies before null is returned, except for the
  `java.time` classes (TYPES 7.1): the implementer follows `get-object.tsv` and the
  NULL rows of the other getter tables.
- **`getArray(i)`**: NULL gives null; else `connection.makeArray(oid(i), text)` (11.4),
  for every column type (TYPES 7.10).

### 10.5 Update methods of a read-only result set

`jdbc/RsReadOnlyUpdates.java` (RS-90). The update methods that pgjdbc implements (52 in
section 2) are written once here and funnel into two hooks, which JDBC-DESIGN-FEATURES
overrides in `RsFeatures` to make result sets updatable:

```java
abstract class RsReadOnlyUpdates extends RsObjectGetters
    void checkUpdateable() throws SQLException;                          // hook: raises 24000 (4.4)
    void updateValue(int columnIndex, Object value) throws SQLException; // hook: checkUpdateable()
```

- Every `updateX(int, value)` is `updateValue(columnIndex, value)` with the value boxed
  (`updateNull` passes null); every label form goes through `findColumn`. The three
  stream forms with an `int` length call `checkUpdateable()` first and then pass the
  stream or reader on unread.
- `insertRow`, `updateRow`, `deleteRow`, `refreshRow`, `cancelRowUpdates`,
  `moveToInsertRow`, `moveToCurrentRow`: `checkOpen()`, then `checkUpdateable()`.
- `rowUpdated()`, `rowInserted()`, `rowDeleted()`: `checkOpen()`, false.
- In this design `checkUpdateable()` always raises, also for a result set requested with
  `CONCUR_UPDATABLE` (5.5); the implementer confirms class and SQLState for a read-only
  result set in reference mode. The update methods marked "rejected" answer `0A000` from
  the root.

## 11. Metadata, type lookup, arrays

### 11.1 `ResultSetMetaData`

`jdbc/RsmdBasic.java` (RSMD-01). Constructor of the chain: `(PgConnection connection,
ResultData data)`. pgjdbc: `jdbc/PgResultSetMetaData.java`, `jdbc/TypeInfoCache.java`.
Column indexes are 1-based; one outside 1..n raises `Errors.columnIndex`. `type` below is
`TypeTable.find(data.typeOid(c), connection.typeLookup())`.

| Method | Answer **(map 6; recorded per type in `types-rsmd.tsv`)** |
|---|---|
| `getColumnCount()` | `data.columnCount()` |
| `getColumnLabel(c)`, `getColumnName(c)` | `data.name(c)` (both the label) |
| `getColumnType(c)` | `type.sqlType()` |
| `getColumnTypeName(c)` | `type.name()` (11.2 refines it for serial columns) |
| `getColumnClassName(c)` | `type.className()` |
| `getPrecision(c)`, `getScale(c)`, `getColumnDisplaySize(c)` | `TypeTable.precision`, `scale`, `displaySize` of OID and modifier (TYPES 5.2) |
| `isSigned(c)`, `isCaseSensitive(c)` | `TypeTable.isSigned`, `isCaseSensitive` |
| `isCurrency(c)` | true for `money` |
| `isSearchable(c)` | true |
| `isReadOnly(c)` / `isWritable(c)` / `isDefinitelyWritable(c)` | false / true / false |
| `getCatalogName(c)`, `getSchemaName(c)` | `""` |
| `unwrap`, `isWrapperFor` | the rule of 5.6 |

### 11.2 Answers that need the catalog

`jdbc/RsmdCatalog.java` and `org/stuntgres/PGResultSetMetaData.java` (an empty interface
from RS-00A, completed by RSMD-02 with `getBaseColumnName(int)`, `getBaseTableName(int)`,
`getBaseSchemaName(int)`, `getFormat(int)`, each `throws SQLException`).

For a column with `data.tableOid(c)` = 0 (an expression) the answers are: base names
`""`, `columnNullableUnknown`, not auto-increment. Otherwise they come from
`connection.columnInfo`; a missing table is loaded with one statement
(`connection.queryOwn(sql, tableOid)`) that fills the cache for all its columns
**(probe B: same rows on PostgreSQL 18.6 and the stuntgres server)**:

```sql
SELECT a.attnum, a.attname, c.relname, n.nspname,
       (a.attnotnull OR (t.typtype = 'd' AND t.typnotnull)) AS notnull,
       (a.attidentity <> '' OR COALESCE(pg_catalog.pg_get_expr(d.adbin, d.adrelid), '') LIKE '%nextval(%') AS autoinc
  FROM pg_catalog.pg_class c
  JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
  JOIN pg_catalog.pg_attribute a ON a.attrelid = c.oid
  JOIN pg_catalog.pg_type t ON t.oid = a.atttypid
  LEFT JOIN pg_catalog.pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
 WHERE c.oid = $1 AND a.attnum > 0 AND NOT a.attisdropped
 ORDER BY a.attnum
```

| Method | Answer |
|---|---|
| `getBaseColumnName(c)`, `getBaseTableName(c)`, `getBaseSchemaName(c)` | the cached names |
| `getTableName(c)` | `getBaseTableName(c)` |
| `isNullable(c)` | `columnNoNulls` when `notnull`, else `columnNullable` |
| `isAutoIncrement(c)` | `autoinc` |
| `getColumnTypeName(c)` | for an auto-increment column of type `int4`, `int8`, `int2`: `serial`, `bigserial`, `smallserial`; else 11.1 |
| `getFormat(c)` | 0 |

Recorded with the probe table (`id serial`, `g int generated always as identity`, `n text
not null`, a not-null domain column, `d int default 5`): notnull `t t t t f`, autoinc `t t
f f f`; every column of a view over it: `f`, `f`. The cache is never invalidated (pgjdbc
bounds it; the properties `databaseMetadataCacheFields...` are ignored).

### 11.3 Type lookup

`jdbc/CatalogTypeLookup.java` (a stub from CONN-00 whose three methods raise
`Errors.notImplemented`; TYPE-04 replaces it): `final class CatalogTypeLookup implements
TypeLookup` with the constructor `CatalogTypeLookup(ConnOwnSql connection)`. One instance
per connection, created by `ConnOwnSql`'s constructor. It answers what TYPES 5.3 asks, with
a cache per connection that is never invalidated (maps OID → `PgType`, name → OID). It is
called with the lock held and uses `queryOwn`. pgjdbc: `jdbc/TypeInfoCache.java`.

By OID **(probe B)**:

```sql
SELECT t.typname, n.nspname, t.typtype, t.typcategory, t.typelem, t.typarray, t.typdelim,
       pg_catalog.pg_type_is_visible(t.oid) AS visible, e.typdelim AS elemdelim
  FROM pg_catalog.pg_type t
  JOIN pg_catalog.pg_namespace n ON n.oid = t.typnamespace
  LEFT JOIN pg_catalog.pg_type e ON e.oid = t.typelem
 WHERE t.oid = $1
```

- No row: a `PgType` named `unknown` with JDBC type `OTHER`, class `java.lang.String`
  (the implementer checks pgjdbc's answer for an OID that does not exist).
- `typcategory` `A`: an array; JDBC type `ARRAY`, class `java.sql.Array`, element OID
  `typelem`, delimiter `elemdelim`. `typtype` `c`: `STRUCT`; `d`: `DISTINCT`; `e`:
  `VARCHAR`; else `OTHER`. Class `java.util.Map` for the type named `hstore`, else
  `java.lang.String`.
- Name: `typname` when `visible` and the name equals its lower-case form; else
  `"<nspname>"."<typname>"`. **(probe B: `mood` visible; `s2.color` not visible; `Mixed
  Case` visible.)** The recorded names are in `types-user.tsv` and `types-byname.tsv`.

By name: the name is split at the first dot outside double quotes; a part in double
quotes is taken literally (`""` is one quote), any other part is lower-cased; a trailing
`[]` asks for the array type. No error can arise from a malformed name (a function such
as `to_regtype` raises a syntax error for one **(probe B)**, which would fail an open
transaction):

```sql
SELECT t.oid, t.typarray FROM pg_catalog.pg_type t
  JOIN pg_catalog.pg_namespace n ON n.oid = t.typnamespace
 WHERE t.typname = $1 AND n.nspname = $2                          -- with a schema
SELECT t.oid, t.typarray FROM pg_catalog.pg_type t
 WHERE t.typname = $1 AND pg_catalog.pg_type_is_visible(t.oid)    -- without
```

`oidByName` returns `oid` (or `typarray` for the `[]` form), `arrayOidByElementName`
returns `typarray`; 0 when there is no row.

### 11.4 `PgArray` and `createArrayOf`

`jdbc/PgArray.java` and `jdbc/ConnArrays.java` (ARRAY-03). pgjdbc: `jdbc/PgArray.java`.
Behavior: TYPES 9.4, with the recorded calls in `array-access.tsv` and `array-format.tsv`.

```java
public class PgArray implements Array
    public PgArray(PgConnection connection, int oid, String text);
    public String toString();                     // the text as received
abstract class ConnArrays extends ConnValid
    final PgArray makeArray(int oid, String text);
    public Array createArrayOf(String typeName, Object[] elements) throws SQLException;
    final Array arrayOf(String typeName, Object elements) throws SQLException;   // any Java array, primitives included
```

- `getBaseTypeName()`, `getBaseType()`: name and JDBC type of the element type
  (`TypeTable.find(type.elementOid(), lookup)`); for a column that is no array the
  methods fail as `get-other.tsv` records.
- `getArray()`: `ObjectGetters.getArray(text, elementType, context)` (TYPES 9.2).
  `getArray(index, count)`: the slice of the outer dimension, 1-based, count 0 meaning
  all from the index; out of range raises `22000` (4.4).
- `getResultSet()` / `getResultSet(index, count)`: a `PgResultSet` without statement,
  `TYPE_SCROLL_INSENSITIVE`, over a `MemoryResultData` with the columns `INDEX` (int4)
  and `VALUE` (the element type; the array type itself when the array has more than one
  dimension, each row then holding a sub-array as text), one row per element of the
  slice.
- The four forms with a `Map`: null or empty map as without; else `0A000` (4.4).
- `free()`: afterwards `getArray()` returns null **(TYPES 9.4)**; the other methods are
  established from `array-access.tsv`.
- `createArrayOf(typeName, elements)` is `arrayOf(typeName, elements)`: `checkOpen()`; the array type is
  `TypeTable.arrayOidByElementName(typeName, lookup)`; 0 raises `42602` (4.4); the text is
  `ArrayFormat.format(elements, delimiter of the element type)`; the result is
  `makeArray(arrayOid, text)`. Null elements give an Array whose text is null
  (established in reference mode).

## 12. Round-trip tests of the conversion matrices

TYPES 12 hands five groups of database tests to this design. They are the first place
where the conversion layer meets a server, in oracle and in server mode (TYPES ran
neither). Each is a part of family `JDBC-TYPE-9x`; all run in reference, oracle and
server mode and compare class and SQLState of errors, never messages.

| Part | TYPES group | What it runs |
|---|---|---|
| TYPE-90 | support | `jdbc/RoundTrip.java` (test tree): creates the user-defined types of the recording (`CREATE TYPE mood AS ENUM ('sad', 'ok', 'happy')`, domain `posint`, composite `pair`, schema `s2` with enum `color`, extension `hstore`; the exact definitions are those the rows of `values.tsv` and `types-user.tsv` need), sets the session zone, and offers `check(resource, getters)`: for every row of a getter table that holds for the JVM zone of the run, `SELECT <sql of the value>` and each getter of the map applied to the real `ResultSet`, compared through `Repr` and `Matrix.expected` (TYPES 7.1). |
| TYPE-91 | R1 | `get-string.tsv`, `get-bytes.tsv`, `get-integer.tsv`, `get-decimal.tsv` |
| TYPE-92 | R1 | `get-datetime.tsv`, `get-javatime.tsv` |
| TYPE-93 | R1 | `get-object.tsv`, and the getters of `get-other.tsv` this design implements (`getArray`, `getObject(Array)`) |
| TYPE-94 | R2, R3 | `set.tsv` through a real `PreparedStatement`: the parameter type (`SELECT pg_typeof(?)::oid`, error `42P18` meaning OID 0), the value read back, an INSERT and read back per setter family; the NUL cases of R3 |
| TYPE-95 | R4 | `Connection.nativeSQL` for `sql-escapes.tsv`; multi-statement strings with parameters and generated keys from the groups `split` and `returning` of `sql-statements.tsv` |
| TYPE-96 | R5 | array parameters of every array type of TYPES 8.7; `createArrayOf` for `array-format.tsv`; `array-access.tsv` |

A difference between oracle and server mode in one of these tests is an engine gap: rule
R8 of the part applies (stop and report the smallest statement); this design adds no
workaround and no `JDBC-ENGINE` part in advance.

## 13. Steps

### 13.1 Parts

Every part lists `JDBC-DESIGN-CORE` as a prerequisite; the table shows the others. "Size"
is code + tests in lines, hand-written; "gen." counts the lines of generated root classes
and of mechanical tables, which are not part of the estimate. IDs are prefixed `JDBC-`. The
notes in the Files column are explained in 1.4.

| Part | Outcome | Files | Prerequisites | Size | gen. |
|---|---|---|---|---|---|
| `ERR-01-CLASSES` | ServerErrorMessage, PSQLWarning and the complete PSQLException | `util/ServerErrorMessage.java`, `util/PSQLWarning.java`, `util/PSQLException.java` (replaces the first form of JDBC-TYPE-00A-ERRORS), `test: util/ErrorClassesTest.java` | TYPE-00A-ERRORS, BUILD-02-RUNNER | 190 + 140 |  |
| `ERR-02-MAPPING` | Errors: pq results, pq failures and connect failures to exceptions, notices to warnings | `jdbc/Errors.java`, `test: jdbc/ErrorsTest.java`, `test: pq/PqFixtures.java` (new, test support) | ERR-01-CLASSES, PQ-01B-VALUES, PQ-05A-RESULT-CLASS | 200 + 190 |  |
| `DRIVER-01-PROPERTIES` | PGProperty with the 92 connection properties, AutoSave and PreferQueryMode | `PGProperty.java` (new; the constant list is a data table (R6 exemption)), `jdbc/AutoSave.java`, `jdbc/PreferQueryMode.java`, `test: PGPropertyTest.java` | TYPE-00A-ERRORS, BUILD-02-RUNNER | 190 + 140 | 290 |
| `DRIVER-02-URL` | URL parser and connection-string builder: DriverUrl and ConninfoBuilder | `core/DriverUrl.java`, `core/ConninfoBuilder.java`, `test: core/DriverUrlTest.java`, `test: core/ConninfoBuilderTest.java` | DRIVER-01-PROPERTIES, PQ-01B-VALUES | 210 + 200 |  |
| `TEST-03-JDBC-SUPPORT` | Assertion helpers for JDBC tests: JdbcAsserts | `test: testing/JdbcAsserts.java` (new, test support), `test: testing/JdbcAssertsTest.java` | TEST-01G-GATES, TEST-01C-SERVER | 110 + 80 |  |
| `CONN-00-SKELETON` | Skeleton of the connection chain: generated ConnectionRoot, stubs, PgConnection | `jdbc/ConnectionRoot.java` (generated), `jdbc/ConnCore.java` (stub; owner CONN-01A-OPEN-CLOSE), `jdbc/ConnOwnSql.java` (stub; owner CONN-01B-EXEC), `jdbc/ConnTxn.java` (stub; owner CONN-02-TXN), `jdbc/ConnTxnOptions.java` (stub; owner CONN-03-TXN-OPTIONS), `jdbc/ConnSavepoints.java` (stub; owner CONN-04A-SAVEPOINT), `jdbc/ConnAutosave.java` (stub; owner CONN-04B-AUTOSAVE), `jdbc/ConnSettings.java` (stub; owner CONN-05-SESSION), `jdbc/ConnValid.java` (stub; owner CONN-06-VALID), `jdbc/ConnArrays.java` (stub; owner ARRAY-03-JDBC), `jdbc/ConnFactories.java` (stub; owner CONN-07-FACTORIES), `jdbc/ConnExtensions.java` (stub; owner CONN-08-WRAPPER), `jdbc/ConnMetaData.java` (stub; owner JDBC-DESIGN-METADATA), `jdbc/ConnFeatures.java` (stub; owner JDBC-DESIGN-FEATURES), `jdbc/PgConnection.java`, `PGConnection.java` (stub; owner CONN-08-WRAPPER), `jdbc/CatalogTypeLookup.java` (stub; owner TYPE-04-LOOKUP), `jdbc/ColumnInfo.java`, `test: jdbc/ConnectionRootTest.java` | ERR-02-MAPPING, TYPE-01-OIDS | 230 + 70 | 320 |
| `CONN-01A-OPEN-CLOSE` | ConnCore: open a connection, close, isClosed, the lock, pq access, failure mapping, warnings | `jdbc/ConnCore.java` (replaces the stub of CONN-00-SKELETON), `test: jdbc/ConnCoreTest.java` | CONN-00-SKELETON, DRIVER-02-URL, PGOBJ-01A-OBJECT, PQ-06B-PARAMS, PQ-08-NOTICES, PQ-09-CANCEL, TEST-03-JDBC-SUPPORT | 200 + 170 |  |
| `CONN-01B-EXEC` | ConnOwnSql: the driver's own statements, execution hooks, session check, conversion context | `jdbc/ConnOwnSql.java` (replaces the stub of CONN-00-SKELETON), `test: jdbc/ConnOwnSqlTest.java` | CONN-01A-OPEN-CLOSE, SQL-02-KINDS | 140 + 150 |  |
| `DRIVER-03-DRIVER` | org.stuntgres.Driver, registration with DriverManager and the services file | `Driver.java`, `src/main/resources/META-INF/services/java.sql.Driver`, `test: DriverTest.java`, `test: DriverConnectTest.java` | CONN-01A-OPEN-CLOSE, DRIVER-02-URL, TEST-03-JDBC-SUPPORT | 180 + 200 |  |
| `DRIVER-04A-DATASOURCE` | DsStore, BaseDataSource and PGSimpleDataSource | `ds/common/DsStore.java`, `ds/common/DsHonoured.java` (stub; owner DRIVER-04B-DS-HONOURED), `ds/common/DsIgnoredTransport.java` (stub; owner DRIVER-04C-DS-TRANSPORT), `ds/common/DsIgnoredTuning.java` (stub; owner DRIVER-04D-DS-TUNING), `ds/common/BaseDataSource.java`, `ds/PGSimpleDataSource.java`, `test: ds/PGSimpleDataSourceTest.java`, `test: ds/DataSourceConnectTest.java` | DRIVER-03-DRIVER | 240 + 180 |  |
| `DRIVER-04B-DS-HONOURED` | Bean accessors of the honoured and stored properties: DsHonoured | `ds/common/DsHonoured.java` (replaces the stub of DRIVER-04A-DATASOURCE), `test: ds/DsHonouredTest.java` | DRIVER-04A-DATASOURCE | 230 + 90 |  |
| `DRIVER-04C-DS-TRANSPORT` | Bean accessors of the ignored transport, TLS and authentication properties: DsIgnoredTransport | `ds/common/DsIgnoredTransport.java` (replaces the stub of DRIVER-04A-DATASOURCE), `test: ds/DsIgnoredTransportTest.java` | DRIVER-04A-DATASOURCE | 60 + 70 | 260 |
| `DRIVER-04D-DS-TUNING` | Bean accessors of the ignored tuning properties: DsIgnoredTuning | `ds/common/DsIgnoredTuning.java` (replaces the stub of DRIVER-04A-DATASOURCE), `test: ds/DsIgnoredTuningTest.java` | DRIVER-04A-DATASOURCE | 60 + 70 | 260 |
| `STMT-00-SKELETON` | Skeleton of the statement chain: generated StatementRoot, stubs, PgStatement | `jdbc/StatementRoot.java` (generated), `jdbc/StmtCore.java` (stub; owner STMT-01A-STATE), `jdbc/StmtRun.java` (stub; owner STMT-01B-RUN), `jdbc/StmtExecute.java` (stub; owner STMT-01C-QUERY-UPDATE), `jdbc/StmtResults.java` (stub; owner STMT-02-RESULTS), `jdbc/StmtLimits.java` (stub; owner STMT-03-LIMITS), `jdbc/StmtCancel.java` (stub; owner STMT-04-CANCEL), `jdbc/StmtBatch.java` (stub; owner BATCH-01-STATEMENT), `jdbc/StmtKeys.java` (stub; owner KEYS-01-GENERATED), `jdbc/StmtExtensions.java` (stub; owner STMT-05-EXTENSIONS), `jdbc/StmtFeatures.java` (stub; owner JDBC-DESIGN-FEATURES), `jdbc/PgStatement.java`, `PGStatement.java` (stub; owner STMT-05-EXTENSIONS), `test: jdbc/StatementRootTest.java` | CONN-00-SKELETON | 170 + 60 | 300 |
| `PSTMT-00-SKELETON` | Skeleton of the prepared-statement chain: generated PreparedRoot, stubs, PgPreparedStatement | `jdbc/PreparedRoot.java` (generated), `jdbc/PsCore.java` (stub; owner PSTMT-01-STORE), `jdbc/PsBasicSetters.java` (stub; owner PSTMT-02-BASIC-SETTERS), `jdbc/PsDateTimeSetters.java` (stub; owner PSTMT-03-DATETIME-SETTERS), `jdbc/PsStreamSetters.java` (stub; owner PSTMT-04-STREAM-SETTERS), `jdbc/PsObjectSetters.java` (stub; owner PSTMT-05-OBJECT-SETTERS), `jdbc/PsBatch.java` (stub; owner BATCH-02-PREPARED), `jdbc/PsMetaData.java` (stub; owner PSTMT-90-METADATA), `jdbc/PsFeatures.java` (stub; owner JDBC-DESIGN-FEATURES), `jdbc/PgPreparedStatement.java`, `test: jdbc/PreparedRootTest.java` | STMT-00-SKELETON | 140 + 60 | 310 |
| `RS-00A-SKELETON` | Skeleton of the result-set and metadata chains: generated roots, ResultData, stubs, PgResultSet, PgResultSetMetaData | `jdbc/ResultSetRoot.java` (generated), `jdbc/ResultSetMetaDataRoot.java` (generated), `jdbc/ResultData.java`, `jdbc/RsCore.java` (stub; owner RS-01A-FORWARD), `jdbc/RsScroll.java` (stub; owner RS-01B-SCROLL), `jdbc/RsColumns.java` (stub; owner RS-02-COLUMNS), `jdbc/RsTextGetters.java` (stub; owner RS-03-TEXT-GETTERS), `jdbc/RsNumberGetters.java` (stub; owner RS-04-NUMBER-GETTERS), `jdbc/RsDateTimeGetters.java` (stub; owner RS-05-DATETIME-GETTERS), `jdbc/RsObjectGetters.java` (stub; owner RS-06-OBJECT-GETTERS), `jdbc/RsReadOnlyUpdates.java` (stub; owner RS-90-READONLY-UPDATES), `jdbc/RsFeatures.java` (stub; owner JDBC-DESIGN-FEATURES), `jdbc/RsmdBasic.java` (stub; owner RSMD-01-BASIC), `jdbc/RsmdCatalog.java` (stub; owner RSMD-02-CATALOG), `jdbc/PgResultSet.java`, `jdbc/PgResultSetMetaData.java`, `PGResultSetMetaData.java` (stub; owner RSMD-02-CATALOG), `test: jdbc/ResultSetRootTest.java` | STMT-00-SKELETON | 230 + 70 | 1150 |
| `RS-00B-DATA` | Result data: PqResultData and MemoryResultData | `jdbc/PqResultData.java`, `jdbc/MemoryResultData.java`, `test: jdbc/ResultDataTest.java` | RS-00A-SKELETON, PQ-05A-RESULT-CLASS | 170 + 150 |  |
| `CONN-07-FACTORIES` | ConnFactories: createStatement and prepareStatement in all forms | `jdbc/ConnFactories.java` (replaces the stub of CONN-00-SKELETON), `test: jdbc/ConnFactoriesTest.java` | CONN-01B-EXEC, STMT-00-SKELETON, PSTMT-00-SKELETON, TEST-03-JDBC-SUPPORT, DRIVER-03-DRIVER | 130 + 140 |  |
| `STMT-01A-STATE` | StmtCore: statement state, open check, close, warnings, hooks | `jdbc/StmtCore.java` (replaces the stub of STMT-00-SKELETON), `test: jdbc/StmtCoreTest.java` | STMT-00-SKELETON, CONN-07-FACTORIES, RS-00A-SKELETON | 160 + 110 |  |
| `STMT-01B-RUN` | StmtRun: the execution path of statements and Statement.execute(String) | `jdbc/StmtRun.java` (replaces the stub of STMT-00-SKELETON), `jdbc/StmtResult.java`, `test: jdbc/StmtRunTest.java` | STMT-01A-STATE, CONN-01B-EXEC, RS-00B-DATA, SQL-03-STATEMENTS, SQL-05-ESCAPES, SET-01-BASIC, PQ-11-COPY | 240 + 190 |  |
| `STMT-01C-QUERY-UPDATE` | StmtExecute: executeQuery, executeUpdate and executeLargeUpdate with a statement text | `jdbc/StmtExecute.java` (replaces the stub of STMT-00-SKELETON), `test: jdbc/StmtExecuteTest.java` | STMT-01B-RUN | 90 + 170 |  |
| `STMT-02-RESULTS` | StmtResults: getResultSet, getUpdateCount, getLargeUpdateCount, getMoreResults | `jdbc/StmtResults.java` (replaces the stub of STMT-00-SKELETON), `test: jdbc/StmtResultsTest.java` | STMT-01B-RUN | 120 + 190 |  |
| `RS-01A-FORWARD` | RsCore: result-set data, next, position answers, close, lifetime | `jdbc/RsCore.java` (replaces the stub of RS-00A-SKELETON), `test: jdbc/RsCoreTest.java` | RS-00B-DATA, STMT-01C-QUERY-UPDATE, STMT-02-RESULTS | 200 + 200 |  |
| `RS-01B-SCROLL` | RsScroll: scrolling and fetch hints of result sets | `jdbc/RsScroll.java` (replaces the stub of RS-00A-SKELETON), `test: jdbc/RsScrollTest.java` | RS-01A-FORWARD | 140 + 200 |  |
| `RS-02-COLUMNS` | RsColumns: findColumn, wasNull and the value access of the getters | `jdbc/RsColumns.java` (replaces the stub of RS-00A-SKELETON), `test: jdbc/RsColumnsTest.java`, `test: jdbc/RsValueAccessTest.java` | RS-01A-FORWARD, TYPE-02-TABLE | 150 + 190 |  |
| `RS-03-TEXT-GETTERS` | RsTextGetters: getString, getBytes and the stream getters; maxFieldSize trimming | `jdbc/RsTextGetters.java` (replaces the stub of RS-00A-SKELETON), `test: jdbc/RsTextGettersTest.java` | RS-02-COLUMNS, GET-01-TEXT | 150 + 180 |  |
| `RS-04-NUMBER-GETTERS` | RsNumberGetters: getBoolean, the integer getters, getFloat, getDouble, getBigDecimal | `jdbc/RsNumberGetters.java` (replaces the stub of RS-00A-SKELETON), `test: jdbc/RsNumberGettersTest.java` | RS-02-COLUMNS, GET-02-INTEGER, GET-03-DECIMAL | 150 + 170 |  |
| `RS-05-DATETIME-GETTERS` | RsDateTimeGetters: getDate, getTime and getTimestamp without and with a Calendar | `jdbc/RsDateTimeGetters.java` (replaces the stub of RS-00A-SKELETON), `test: jdbc/RsDateTimeGettersTest.java` | RS-02-COLUMNS, GET-04-DATETIME | 100 + 150 |  |
| `TYPE-04-LOOKUP` | CatalogTypeLookup: types outside the built-in table, from the catalog, cached per connection | `jdbc/CatalogTypeLookup.java` (replaces the stub of CONN-00-SKELETON), `test: jdbc/CatalogTypeLookupTest.java` | CONN-01B-EXEC, TYPE-02-TABLE, TYPE-99-DRAFT-REMOVE, TYPE-00B-TESTDATA, TEST-03-JDBC-SUPPORT, DRIVER-03-DRIVER | 200 + 200 |  |
| `ARRAY-03-JDBC` | PgArray as java.sql.Array, ConnArrays and Connection.createArrayOf | `jdbc/PgArray.java`, `jdbc/ConnArrays.java` (replaces the stub of CONN-00-SKELETON), `test: jdbc/PgArrayTest.java`, `test: jdbc/CreateArrayOfTest.java` | TYPE-04-LOOKUP, RS-03-TEXT-GETTERS, RS-04-NUMBER-GETTERS, RS-01B-SCROLL, GET-06-OBJECT, ARRAY-02-FORMAT, TYPE-99-DRAFT-REMOVE | 230 + 200 |  |
| `RS-06-OBJECT-GETTERS` | RsObjectGetters: getObject in its three forms, getArray, custom types, the feature hook | `jdbc/RsObjectGetters.java` (replaces the stub of RS-00A-SKELETON), `test: jdbc/RsObjectGettersTest.java`, `test: jdbc/RsCustomTypeTest.java` | RS-03-TEXT-GETTERS, RS-04-NUMBER-GETTERS, RS-05-DATETIME-GETTERS, GET-07-CLASS, ARRAY-03-JDBC | 170 + 220 |  |
| `RS-90-READONLY-UPDATES` | RsReadOnlyUpdates: the update methods of a read-only result set and the two update hooks | `jdbc/RsReadOnlyUpdates.java` (replaces the stub of RS-00A-SKELETON), `test: jdbc/RsReadOnlyUpdatesTest.java` | RS-02-COLUMNS | 290 + 140 |  |
| `RSMD-01-BASIC` | RsmdBasic: ResultSetMetaData from the column descriptors and the type table | `jdbc/RsmdBasic.java` (replaces the stub of RS-00A-SKELETON), `test: jdbc/RsmdBasicTest.java` | RS-01A-FORWARD, TYPE-02-TABLE, TYPE-00B-TESTDATA, TYPE-99-DRAFT-REMOVE | 160 + 200 |  |
| `RSMD-02-CATALOG` | RsmdCatalog: metadata answers from the catalog and PGResultSetMetaData | `jdbc/RsmdCatalog.java` (replaces the stub of RS-00A-SKELETON), `PGResultSetMetaData.java` (replaces the stub of RS-00A-SKELETON), `test: jdbc/RsmdCatalogTest.java`, `test: jdbc/RsmdBaseNamesTest.java` | RSMD-01-BASIC, CONN-01B-EXEC | 170 + 210 |  |
| `STMT-03-LIMITS` | StmtLimits: maxRows, maxFieldSize, fetch hints, escape processing, poolable, closeOnCompletion | `jdbc/StmtLimits.java` (replaces the stub of STMT-00-SKELETON), `test: jdbc/StmtLimitsTest.java` | STMT-02-RESULTS, STMT-01C-QUERY-UPDATE, RS-03-TEXT-GETTERS, RS-01B-SCROLL | 170 + 220 |  |
| `STMT-04-CANCEL` | StmtCancel and CancelTimer: Statement.cancel and the query timeout | `jdbc/StmtCancel.java` (replaces the stub of STMT-00-SKELETON), `jdbc/CancelTimer.java`, `test: jdbc/StmtCancelTest.java`, `test: jdbc/CancelTimerTest.java` | STMT-02-RESULTS, STMT-01C-QUERY-UPDATE, RS-03-TEXT-GETTERS | 160 + 220 |  |
| `STMT-05-EXTENSIONS` | StmtExtensions and the PGStatement interface; unwrap of statements | `jdbc/StmtExtensions.java` (replaces the stub of STMT-00-SKELETON), `PGStatement.java` (replaces the stub of STMT-00-SKELETON), `test: jdbc/StmtExtensionsTest.java` | STMT-01C-QUERY-UPDATE, CODEC-02-DATETIME | 100 + 120 |  |
| `CONN-02-TXN` | ConnTxn: autocommit, implicit BEGIN, commit, rollback, atomic multi-statement calls | `jdbc/ConnTxn.java` (replaces the stub of CONN-00-SKELETON), `test: jdbc/ConnTxnTest.java`, `test: jdbc/ConnTxnStatusTest.java` | CONN-01B-EXEC, STMT-01C-QUERY-UPDATE, STMT-02-RESULTS, RS-03-TEXT-GETTERS, STMT-04-CANCEL | 140 + 260 |  |
| `CONN-03-TXN-OPTIONS` | ConnTxnOptions: transaction isolation level and read-only | `jdbc/ConnTxnOptions.java` (replaces the stub of CONN-00-SKELETON), `test: jdbc/ConnTxnOptionsTest.java` | CONN-02-TXN | 130 + 190 |  |
| `CONN-04A-SAVEPOINT` | ConnSavepoints and PSQLSavepoint: savepoints | `jdbc/ConnSavepoints.java` (replaces the stub of CONN-00-SKELETON), `jdbc/PSQLSavepoint.java`, `test: jdbc/ConnSavepointsTest.java`, `test: jdbc/PSQLSavepointTest.java` | CONN-02-TXN, PQ-12-ESCAPE | 140 + 190 |  |
| `CONN-04B-AUTOSAVE` | ConnAutosave: the autosave and cleanupSavepoints properties | `jdbc/ConnAutosave.java` (replaces the stub of CONN-00-SKELETON), `test: jdbc/ConnAutosaveTest.java` | CONN-04A-SAVEPOINT, DRIVER-01-PROPERTIES | 110 + 160 |  |
| `CONN-05-SESSION` | ConnSettings: schema, catalog, client info, type map, holdability, nativeSQL | `jdbc/ConnSettings.java` (replaces the stub of CONN-00-SKELETON), `test: jdbc/ConnSettingsTest.java` | CONN-02-TXN, PQ-12-ESCAPE, SQL-05-ESCAPES | 190 + 220 |  |
| `CONN-06-VALID` | ConnValid: isValid, abort, network timeout | `jdbc/ConnValid.java` (replaces the stub of CONN-00-SKELETON), `test: jdbc/ConnValidTest.java` | CONN-02-TXN, STMT-04-CANCEL | 120 + 170 |  |
| `CONN-08-WRAPPER` | ConnExtensions: the PGConnection interface with the methods of this design, unwrap of connections | `jdbc/ConnExtensions.java` (replaces the stub of CONN-00-SKELETON), `PGConnection.java` (replaces the stub of CONN-00-SKELETON), `test: jdbc/ConnExtensionsTest.java` | ARRAY-03-JDBC, CONN-05-SESSION, CONN-04B-AUTOSAVE, RS-06-OBJECT-GETTERS, STMT-04-CANCEL, PQ-12-ESCAPE | 200 + 220 |  |
| `PSTMT-01-STORE` | PsCore: the parameter store, execution of prepared statements, the refused Statement methods | `jdbc/PsCore.java` (replaces the stub of PSTMT-00-SKELETON), `test: jdbc/PsCoreTest.java`, `test: jdbc/PsCoreBindTest.java` | PSTMT-00-SKELETON, STMT-02-RESULTS, STMT-01C-QUERY-UPDATE, RS-03-TEXT-GETTERS | 230 + 180 |  |
| `PSTMT-02-BASIC-SETTERS` | PsBasicSetters: setNull, setBoolean, the numeric setters, setString, setBytes | `jdbc/PsBasicSetters.java` (replaces the stub of PSTMT-00-SKELETON), `test: jdbc/PsBasicSettersTest.java` | PSTMT-01-STORE, SET-05-TABLE, RS-04-NUMBER-GETTERS | 110 + 190 |  |
| `PSTMT-03-DATETIME-SETTERS` | PsDateTimeSetters: setDate, setTime, setTimestamp without and with a Calendar | `jdbc/PsDateTimeSetters.java` (replaces the stub of PSTMT-00-SKELETON), `test: jdbc/PsDateTimeSettersTest.java` | PSTMT-02-BASIC-SETTERS, RS-05-DATETIME-GETTERS | 80 + 150 |  |
| `PSTMT-04-STREAM-SETTERS` | PsStreamSetters: the stream and reader setters pgjdbc implements | `jdbc/PsStreamSetters.java` (replaces the stub of PSTMT-00-SKELETON), `test: jdbc/PsStreamSettersTest.java` | PSTMT-02-BASIC-SETTERS | 100 + 160 |  |
| `PSTMT-05-OBJECT-SETTERS` | PsObjectSetters: setObject in its three forms and setArray | `jdbc/PsObjectSetters.java` (replaces the stub of PSTMT-00-SKELETON), `test: jdbc/PsObjectSettersTest.java` | PSTMT-02-BASIC-SETTERS, RS-06-OBJECT-GETTERS | 90 + 190 |  |
| `PSTMT-90-METADATA` | PsMetaData and PgParameterMetaData: metadata before execution | `jdbc/PsMetaData.java` (replaces the stub of PSTMT-00-SKELETON), `jdbc/PgParameterMetaData.java`, `test: jdbc/PsMetaDataTest.java` | PSTMT-02-BASIC-SETTERS, RSMD-01-BASIC, PQ-07-PREPARED, CONN-03-TXN-OPTIONS | 190 + 200 |  |
| `BATCH-01-STATEMENT` | StmtBatch and BatchRunner: Statement batches | `jdbc/StmtBatch.java` (replaces the stub of STMT-00-SKELETON), `jdbc/BatchRunner.java`, `test: jdbc/StatementBatchTest.java` | CONN-02-TXN | 190 + 230 |  |
| `KEYS-01-GENERATED` | StmtKeys: generated keys of statements | `jdbc/StmtKeys.java` (replaces the stub of STMT-00-SKELETON), `test: jdbc/StatementKeysTest.java` | STMT-02-RESULTS, STMT-01C-QUERY-UPDATE, RS-04-NUMBER-GETTERS, RS-03-TEXT-GETTERS | 140 + 200 |  |
| `BATCH-02-PREPARED` | PsBatch: PreparedStatement batches and generated keys of prepared statements | `jdbc/PsBatch.java` (replaces the stub of PSTMT-00-SKELETON), `test: jdbc/PreparedBatchTest.java`, `test: jdbc/PreparedKeysTest.java` | PSTMT-02-BASIC-SETTERS, BATCH-01-STATEMENT, KEYS-01-GENERATED | 120 + 240 |  |
| `TYPE-90-ROUNDTRIP-SUPPORT` | Round-trip support: RoundTrip runs the recorded getter tables against a database | `test: core/RoundTrip.java` (new, test support), `test: core/RoundTripSmokeTest.java` | RS-06-OBJECT-GETTERS, TYPE-03-MATRIX, TYPE-99-DRAFT-REMOVE | 0 + 300 |  |
| `TYPE-91-ROUNDTRIP-TEXT-NUMBER` | Round trip of the text, bytes, integer and decimal getters against the recorded tables | `test: core/GetTextNumberRoundTripTest.java` | TYPE-90-ROUNDTRIP-SUPPORT | 0 + 260 |  |
| `TYPE-92-ROUNDTRIP-DATETIME` | Round trip of the date, time, timestamp and java.time getters against the recorded tables | `test: core/GetDateTimeRoundTripTest.java` | TYPE-90-ROUNDTRIP-SUPPORT | 0 + 260 |  |
| `TYPE-93-ROUNDTRIP-OBJECT` | Round trip of getObject and of the array getters against the recorded tables | `test: core/GetObjectRoundTripTest.java` | TYPE-90-ROUNDTRIP-SUPPORT | 0 + 260 |  |
| `TYPE-94-ROUNDTRIP-SET` | Round trip of the setter table through a real PreparedStatement, and the NUL cases | `test: core/SetterCalls.java` (edit: remove the modifier final from the class and from nothing else; owner JDBC-SET-05-TABLE), `test: core/JdbcSetterCalls.java` (new, test support), `test: core/SetRoundTripTest.java`, `test: core/NulRoundTripTest.java` | PSTMT-05-OBJECT-SETTERS, PSTMT-03-DATETIME-SETTERS, PSTMT-04-STREAM-SETTERS, SET-05-TABLE, TYPE-90-ROUNDTRIP-SUPPORT | 0 + 380 |  |
| `TYPE-95-ROUNDTRIP-SQL` | Round trip of statement text: nativeSQL, multi-statement strings with parameters, generated keys | `test: core/SqlRoundTripTest.java` | CONN-05-SESSION, BATCH-02-PREPARED, TYPE-99-DRAFT-REMOVE, TYPE-00B-TESTDATA | 0 + 280 |  |
| `TYPE-96-ROUNDTRIP-ARRAY` | Round trip of arrays: array parameters, createArrayOf and the recorded Array calls | `test: core/ArrayRoundTripTest.java` | PSTMT-05-OBJECT-SETTERS, CONN-08-WRAPPER, TYPE-90-ROUNDTRIP-SUPPORT | 0 + 280 |  |
| **60 parts** | | | | **8510 + 10880** | **2890** |

Changes against the list of the part record: the skeleton parts (`CONN-00`, `STMT-00`,
`PSTMT-00`, `RS-00A`, `RS-00B`) are new (1.3); `CONN-01`, `STMT-01`, `RS-01`, `CONN-04` and
`DRIVER-04` are split for the size limit; `STMT-05-WARNINGS` is dropped and `STMT-05` is
the `PGStatement` part; `TEST-03` and `TYPE-04` are as decision 16 says; the getter and
setter groups follow the function classes of TYPES (four getter parts, four setter
parts); the round trips are seven parts.

### 13.2 Rules common to the parts

- **Tests.** A test class is either `@UnitTest` or `@DatabaseTest` (NATIVE 10.7). A
  database test of `java.sql` behavior also carries `@ReferenceToo` and takes its
  connection from `TestDatabase.connect()`; it then asserts only what pgjdbc shows in
  reference mode (class and SQLState of errors, values, states), never a message text of
  this driver. A database test that uses package-private members or the mirrored
  `org.stuntgres` API cannot run in reference mode: it carries `@Modes({Mode.ORACLE,
  Mode.SERVER, Mode.NATIVE})`, and gets the driver's object by casting the connection of
  `TestDatabase.connect()` to `PgConnection`. Its expected values are recorded from pgjdbc
  once (rule R11).
- **Expected values.** Where a part lists a case without a literal, the implementer
  writes the expectation, runs the test in reference mode and keeps what pgjdbc shows
  (R11). A literal marked **(probe)** in this document was observed and is given.
- **Assertions on SQLState** use `JdbcAsserts.assertSqlState` (TEST-03).
- **Order inside a chain.** A part calls only members that this document lists for
  classes above its own in the chain, and hooks. When a member it needs is still a stub
  the part lists the owner as a prerequisite.
- **Modes to pass before merge**: unit, reference, oracle, server (rule R5). Native mode
  is not run by these parts (plan J4); the `JDBC-NATIVE` parts of JDBC-DESIGN-FEATURES do.

## 14. Probes behind this design

Three probes, each run once; sources, commands and output are in
`.cswd/tmp/JDBC-DESIGN-CORE/` (`log.txt`, `probe/`). Nothing else was run: no class of
this design exists as code (decision 18).

| Probe | What ran | Findings used |
|---|---|---|
| A | A Java program through pgjdbc 42.7.13 against PostgreSQL 18.6, JVM zone Europe/Warsaw. | A1 `parseURL` for 21 URL forms (3.1). A2 session settings after connect (5.2). A3 SQLState of five failed connects: `3D000`, `28P01` (wrong password and unknown user), `08001` (refused), `22023` (bad `options` value) (4.3). A4 NUL in statement text: `08P01`, connection valid, an open transaction failed (decision 4). A5 a statement string with a failing middle statement stores nothing in autocommit mode, with and without parameters; the result sequence of 7.4; `COMMIT` inside a string. A6 `maxRows`, `maxFieldSize`, COPY through a statement (`0A000`, `57014`), counts of an empty statement, DDL, `SET`, `DO`, `SELECT INTO`. A7 batches: `[-3, -3, -3]`, nothing stored in autocommit mode, SQLState and chaining of the `BatchUpdateException`, a query in a batch counts 0. A8 the `RETURNING` text, `0A000` for column indexes, the empty keys result, statement defaults, `08003` after close. |
| B | The fixed SQL of this layer through psql 18.6 with the connection string of 3.2, once against PostgreSQL 18.6 and once against `cmd/stuntgres` built from the tree. | The two outputs are equal except for the database name: session settings from `options`, isolation and read-only statements, savepoints, schema and application name, the catalog statements of 11.2 and 11.3, describe of an unnamed statement with parameters, `RETURNING`. No engine gap. `to_regtype` raises a syntax error for a malformed name (11.3). |
| C | javac 17.0.13 and 21.0.6 with `--release 17 -Xlint:all -Werror` on a five-class model of a chain. | A public class at the end of a package-private abstract chain compiles without warnings and its inherited methods can be invoked by reflection; an override of a deprecated interface method needs `@Deprecated` (1.5). |

Not probed, left to the implementing parts in reference mode (rule R11): edge answers of
navigation, the exact set of validated factory arguments, `commit()` after a failed
statement, `readOnlyMode=always`, `ResultSetMetaData` per type (recorded in
`types-rsmd.tsv` by TYPES), cancel and timeout timing, `Array` method results (recorded in
`array-access.tsv`).
