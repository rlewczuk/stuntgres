# JDBC-FEATURES: callable statements, updatable result sets, large objects, SQLXML, COPY, notifications, pooled and XA connections, cluster control, the jar

This document completes the design of the stuntgres JDBC driver (`bindings/jdbc`). It
stands on `docs/design/JDBC-NATIVE.md` (build, the `org.stuntgres.pq` package, the test
harness; cited as NATIVE), `JDBC-TYPES.md` (SQL text and conversions; TYPES),
`JDBC-CORE.md` (driver, connection, statements, result sets; CORE) and `JDBC-METADATA.md`
(`DatabaseMetaData`; METADATA). The consumer contract is `docs/JDBC.md`.

It is a specification, not source code: per feature it gives the files, the place in the
class chains, the signatures other parts call, the behavior rules, the shape of the SQL
the driver sends, error classes and SQLStates, and where pgjdbc 42.7.13 implements the
behavior (`M/` = `../src-ref/pgjdbc/pgjdbc/src/main/java/org/postgresql/`; "map n" =
`.cswd/tmp/JDBC/research/pgjdbc-map.md` section n). **Nothing in this document was
executed** (18.1): where it says "as pgjdbc shows" or "recorded", the implementing part
establishes the value in reference mode or with a throwaway pgjdbc program (rule R11 of
the parts). A statement marked **(pgjdbc source)** was read from pgjdbc's source and is a
starting point, not an observation.

## 1. Purpose, decisions, layout

### 1.1 Layering

```
org.stuntgres             PGConnection (additions), PGNotification, PGRefCursorResultSet,
                          Stuntgres, IsolatedCluster                       (this design)
org.stuntgres.copy        CopyManager, CopyOperation, CopyIn, CopyOut, PGCopyInputStream,
                          PGCopyOutputStream
org.stuntgres.largeobject LargeObjectManager, LargeObject, BlobInputStream, BlobOutputStream
org.stuntgres.xml         PGXmlFactoryFactory and its two implementations
org.stuntgres.ds, .xa     PGConnectionPoolDataSource, PGPooledConnection; PGXADataSource,
                          PGXAConnection, PGXAException
org.stuntgres.core        BaseConnection (the internal face of a connection)
org.stuntgres.jdbc        PgCallableStatement and its chain; the feature classes of the
                          connection, prepared-statement and result-set chains; PgBlob,
                          PgClob, PgSQLXML; the copy operations
   └ JDBC-CORE classes    (PgConnection, PgPreparedStatement, PgResultSet, Errors, ...)
      └ org.stuntgres.pq  (COPY calls, notifies, PqCluster, PqExt)
```

A class outside `org.stuntgres.jdbc` reaches a connection only through the public JDBC
API and through `core.BaseConnection`; it creates its exceptions itself with `new
PSQLException(text, PSQLState.X)` (`Errors` is package-private in `jdbc`).

### 1.2 Decisions

1. **Nothing was run**, and rule D7, the twelve experiments, numbered algorithms and
   literal expectations of the part record are waived by the project owner (18.1).
2. **Feature classes extend the chains of CORE 1.3 in files of their own.** Two skeleton
   parts (`FEAT-00A`, `FEAT-00B`) replace the extension stubs `ConnFeatures`, `PsFeatures`
   and `RsFeatures` by their final form and create one stub per feature class; each
   feature part replaces its own stub (1.3). `StmtFeatures` stays a stub for the
   fetch-size follow-up (16).
3. **`core.BaseConnection`** is the only way from the public packages (`copy`,
   `largeobject`, `ds`, `xa`) into the connection: it extends `PGConnection` and
   `Connection` and adds `startCopy` and `transactionStatus`. pgjdbc has a public
   interface of the same name and place, and applications write `new
   CopyManager((BaseConnection) connection)`; that line compiles after the package rename.
4. **`PGConnection`** gains `getNotifications` (two forms), `getCopyAPI`,
   `getLargeObjectAPI`, the three cluster-control methods and `alterUserPassword` (a
   default method that rejects with `0A000`). `getFastpathAPI` and `getReplicationAPI` are
   not declared: their return types are not mirrored (2.3).
5. **Large objects go through ordinary prepared statements** on the public API, one SQL
   function call each (5.1); so they take part in the connection's transaction handling
   without special code. The engine implements the functions used
   (`internal/engine/functions/largeobject.go`; read, not run).
6. **Updatability is decided from the result's metadata**, not from the statement text
   (4.1): the table is the one table OID that the result's columns report, the key is
   looked up in the catalog. pgjdbc takes the word after the first `FROM` of the text.
   The driver therefore refuses a result that mixes columns of two tables, which pgjdbc
   may accept; for a result over one table with its key selected both agree. This needs
   no statement text in the result set and no edit of a finished class.
7. **Rows changed through a result set are re-read from the server** (`RETURNING`),
   where pgjdbc builds the new row from the client's values (4.3). Tests that also run
   in reference mode use values whose stored text equals what the client sent.
8. **Corrections to CORE** that this design needs, made in `JDBC-CORE.md`:
   `RsCore.rowCount` is not `final` (10.1; rows are inserted and deleted); the verdict of
   `hideUnprivilegedObjects` is "honoured" (3.3; METADATA decision 9). Three funnels of
   CORE are too narrow and are overridden instead of changed: the stream update methods
   (4.2), `getObject(int, Class)` for `Blob`, `Clob`, `NClob`, `SQLXML` (5.5, 6.3) and
   `setObject` with LOB values (5.5).
9. **A copy in progress** is state of the connection. Statements of applications are
   refused while it lasts (`55000`); pgjdbc blocks instead (7.1). Statements the driver
   itself sends from finished CORE classes (`commit`, `rollback`, session setters) cannot
   be intercepted without editing those classes: the library ends the copy when it gets
   a new command, and the copy object reports that at its next call. A hook in
   `ConnOwnSql.execOwn` would close the gap; it is reported to the coordinator (18.2).
10. **`CopyOut.readFromCopy(false)` blocks** like `readFromCopy()`: the pq layer has only
    the blocking read (NATIVE 7.5), and in-process the next row is produced without
    network delay.
11. **Pooled and XA classes use the public API** of the physical connection and
    `java.lang.reflect.Proxy` for the handles, as pgjdbc does (9.1). No JNDI:
    `getReference`, `PGObjectFactory`, `PGXADataSourceFactory` and `PGPoolingDataSource`
    are not mirrored (2.3; CORE 1.3).
12. **Cluster control**: static methods on `org.stuntgres.Stuntgres`, three methods on
    `PGConnection`, the helper `org.stuntgres.IsolatedCluster` (11). Native only: with a
    real libpq behind the shim they raise `SQLFeatureNotSupportedException` `0A000`.
13. **Opt-in for `jdbc:postgresql:`**: system property
    `stuntgres.jdbc.acceptPostgresqlUrls=true` (12).
14. **Jar**: the binding's own `make jar LIBSPQ=...` (NATIVE 3.2) already builds it; the
    root target copies it to `build/jdbc/`. Compression stays the jar default (deflate):
    NATIVE 8.3 measured 94.7 MB → 39.8 MB in the jar and 0.43 s for a first start with
    extraction; no new measurement was made. The version check compares the driver's
    version with `SPQXversion()` at the first connection (13.2). No sources or javadoc
    jar; nothing is published.
15. **`make jdbc-check` does not run the suite against the jar**: that would need a pom
    change (surefire's class directory), and the handover forbids a second editor of
    `pom.xml`. It runs the unit and native suites from the class directory and the jar
    smoke test from the jar alone (13.3, 13.4); the classes are the same bytes.
16. **One native part per test family** (15), 13 families plus the final part; each
    lists the LIBSPQ-CABI parts its family's pq calls need.
17. **Memory cap** on every test run that sends SQL to a stuntgres server or runs the
    engine in the test JVM (17.2), as METADATA decision 13.
18. **No engine gap is asserted.** Whether the stuntgres server discards `void`
    parameters in function calls (3.2), supports two-phase commit (10) and every
    large-object function as PostgreSQL does was not probed; the parts meet these in
    server mode and report under R8.

### 1.3 Class chains and how the chains of JDBC-CORE are extended

Stubs, roots and final classes follow CORE 1.3 and 1.5. New classes in **bold** are
created as stubs by the skeleton part named first and replaced by the owner in the right
column; `ConnFeatureRoot`, `ConnFeatures`, `PsFeatures`, `RsFeatures` and
`PgCallableStatement` are written once in final form.

```
ConnMetaData                                   (CORE, METADATA)
└ ConnFeatureRoot   rejects the PGConnection additions; transactionStatus   FEAT-00A
  └ ConnVersionCheck  version check in the constructor                      MAKE-01
    └ ConnCalls       prepareCall                                           CALL-01
      └ ConnLobs      getLargeObjectAPI                                     LOB-01B
        └ ConnXml     createSQLXML, the XML factory                         XML-01
          └ ConnCopyState  startCopy, copy state, getCopyAPI                COPY-01A
            └ ConnNotifications  getNotifications                           NOTIFY-01
              └ ConnControl  resetDatabase, dump, restore                   CONTROL-01
                └ ConnFeatures  (final form; constructor only)              FEAT-00A
                  └ PgConnection

PsMetaData                                     (CORE)
└ PsLobs      setBlob, setClob(int, Clob), setObject with LOB values        LOB-03C
  └ PsXml     setSQLXML, setObject with a SQLXML                            XML-03
    └ PsFeatures  (final form)                                              FEAT-00B
      └ PgPreparedStatement
        └ CallableRoot      generated: every CallableStatement method rejects   CALL-00
          └ CallCore        registration, execution, result row, wasNull   CALL-02A
            └ CallBasicGetters                                              CALL-03
              └ CallObjectGetters                                           CALL-04
                └ PgCallableStatement (public; implements CallableStatement) CALL-00

RsReadOnlyUpdates                              (CORE)
└ RsLobs        getBlob, getClob, getObject(int, Class) for LOB classes     LOB-03C
  └ RsXml       getSQLXML, featureObject for SQLXML                         XML-03
    └ RsRefCursor   featureObject for REF_CURSOR, PGRefCursorResultSet      CALL-05
      └ RsUpdatable   checkUpdateable                                       UPD-01
        └ RsUpdValues   updateValue, insert row, cancelRowUpdates           UPD-02
          └ RsUpdRows     updateRow, deleteRow, refreshRow                  UPD-03A
            └ RsUpdInsert   insertRow                                       UPD-03B
              └ RsFeatures  (final form)                                    FEAT-00B
                └ PgResultSet

ds:  BaseDataSource └ PGConnectionPoolDataSource (POOL-03);  PGPooledConnection (POOL-01)
xa:  BaseDataSource └ PGXADataSource (XA-03);  PGPooledConnection └ XaBranch (XA-02A)
     └ PGXAConnection (XA-02B)
```

Rules of the extension:

- A class that overrides a method several feature classes share (`featureObject`,
  `getObject(int, Class)`, `setObject`, `beginExecute`) handles its own case and calls
  `super` for every other; the chain order above fixes who is asked first.
- A stub of a P2 family (`ConnXml`, `PsXml`, `RsXml`, the `RsUpd*` classes) stays a stub
  until that family is implemented; the P1 parts below it in the chain do not depend on
  it.
- The two files of other owners that this design edits: `org/stuntgres/PGConnection.java`
  (owner CONN-08; edited by FEAT-00A only) and `core/DriverUrl.java` (owner DRIVER-02;
  edited by DRIVER-05 only). `bindings/jdbc/README.md` (owner BUILD-01) is rewritten by
  DOCS-01. `jdbc/CallCore.java` may be corrected by CALL-02B after its owner CALL-02A.

### 1.4 Files and owner parts

The table of 17.1 lists every file with its owner; the notes in its Files column are
those of CORE 1.4 ("stub; owner X", "replaces the stub of X", "generated", "edit").
Paths without a directory prefix are below `src/main/java/org/stuntgres/`, those starting
with `test:` below `src/test/java/org/stuntgres/`, `res:` below
`src/test/resources/org/stuntgres/`, `tools/` below `bindings/jdbc/tools/`.

### 1.5 Rules for every class of this layer

1. **CORE 1.5 applies** to the classes in `org.stuntgres.jdbc`: the lock (every public
   method that reaches the connection runs under `connection.lock`), generated roots,
   constructors that call no overridable method, visibility, hooks, error mapping, JDK
   modules `java.base`, `java.sql` (with `java.xml`, `java.logging`, `java.transaction.xa`,
   which `java.sql` requires transitively). No `java.naming`.
2. **Classes in the public packages** hold a `BaseConnection` and synchronize by calling
   its public methods (each takes the connection's lock itself); a sequence of calls that
   must not interleave with other threads is not made atomic, as in pgjdbc: a connection
   is used by one thread at a time for large objects, copies and XA.
3. **Mirrored names** (2.3) have pgjdbc's signatures with `org.postgresql` replaced by
   `org.stuntgres`; PGAPI-01 checks them against pgjdbc's jar. Where this document gives
   a signature of a mirrored member from memory of pgjdbc and the jar shows another, the
   jar wins (the part reports the difference in its commit body).
4. **Own SQL** is written by the implementer (R3): schema-qualified `pg_catalog`
   functions and tables, identifiers quoted with `PGConnection.escapeIdentifier`, literals
   with `escapeLiteral` or bound as parameters. The shapes are given per section.
5. **Messages** are the driver's own wording unless a section says "same"; class and
   SQLState are pgjdbc's (TYPES decision 4).

## 2. Method inventories and verdicts

Generated by `.cswd/tmp/JDBC-DESIGN-FEATURES/parts/inv.py` from the `javap` listings of
JDK 17 (`javap-java.sql.txt`, `javap-javax.txt` in the same directory); the audit checks
that every method has exactly one verdict. Verdicts: a part ID (prefix `JDBC-`);
**rejected**: pgjdbc raises `SQLFeatureNotSupportedException` `0A000` (`Driver.notImplemented`)
and so does the generated root or the class named, for every argument; **JDK default**: a
default method that is not overridden; **no class**: the interface is never implemented.

### 2.1 Interfaces of `java.sql` and `javax.sql`

**`CallableStatement`** (121 methods: 3 CALL-02A-OUT, 11 CALL-03-BASIC-GETTERS, 11 CALL-04-OBJECT-GETTERS, 96 rejected): the methods the interface itself declares; those of `PreparedStatement` are JDBC-CORE's. The default methods (the `SQLType` forms) are overridden by pgjdbc with a rejection, so the root declares them too.

| Verdict | Methods |
|---|---|
| CALL-02A-OUT | `registerOutParameter(int, int)`, `registerOutParameter(int, int, int)`, `wasNull()` |
| CALL-03-BASIC-GETTERS | `getString(int)`, `getBoolean(int)`, `getByte(int)`, `getShort(int)`, `getInt(int)`, `getLong(int)`, `getFloat(int)`, `getDouble(int)`, `getBigDecimal(int, int)`, `getBytes(int)`, `getBigDecimal(int)` |
| CALL-04-OBJECT-GETTERS | `getDate(int)`, `getTime(int)`, `getTimestamp(int)`, `getObject(int)`, `getObject(int, Map)`, `getArray(int)`, `getDate(int, Calendar)`, `getTime(int, Calendar)`, `getTimestamp(int, Calendar)`, `getSQLXML(int)`, `getObject(int, Class)` |
| rejected | `getRef(int)`, `getBlob(int)`, `getClob(int)`, `registerOutParameter(int, int, String)`, `registerOutParameter(String, int)`, `registerOutParameter(String, int, int)`, `registerOutParameter(String, int, String)`, `getURL(int)`, `setURL(String, URL)`, `setNull(String, int)`, `setBoolean(String, boolean)`, `setByte(String, byte)`, `setShort(String, short)`, `setInt(String, int)`, `setLong(String, long)`, `setFloat(String, float)`, `setDouble(String, double)`, `setBigDecimal(String, BigDecimal)`, `setString(String, String)`, `setBytes(String, byte[])`, `setDate(String, Date)`, `setTime(String, Time)`, `setTimestamp(String, Timestamp)`, `setAsciiStream(String, InputStream, int)`, `setBinaryStream(String, InputStream, int)`, `setObject(String, Object, int, int)`, `setObject(String, Object, int)`, `setObject(String, Object)`, `setCharacterStream(String, Reader, int)`, `setDate(String, Date, Calendar)`, `setTime(String, Time, Calendar)`, `setTimestamp(String, Timestamp, Calendar)`, `setNull(String, int, String)`, `getString(String)`, `getBoolean(String)`, `getByte(String)`, `getShort(String)`, `getInt(String)`, `getLong(String)`, `getFloat(String)`, `getDouble(String)`, `getBytes(String)`, `getDate(String)`, `getTime(String)`, `getTimestamp(String)`, `getObject(String)`, `getBigDecimal(String)`, `getObject(String, Map)`, `getRef(String)`, `getBlob(String)`, `getClob(String)`, `getArray(String)`, `getDate(String, Calendar)`, `getTime(String, Calendar)`, `getTimestamp(String, Calendar)`, `getURL(String)`, `getRowId(int)`, `getRowId(String)`, `setRowId(String, RowId)`, `setNString(String, String)`, `setNCharacterStream(String, Reader, long)`, `setNClob(String, NClob)`, `setClob(String, Reader, long)`, `setBlob(String, InputStream, long)`, `setNClob(String, Reader, long)`, `getNClob(int)`, `getNClob(String)`, `setSQLXML(String, SQLXML)`, `getSQLXML(String)`, `getNString(int)`, `getNString(String)`, `getNCharacterStream(int)`, `getNCharacterStream(String)`, `getCharacterStream(int)`, `getCharacterStream(String)`, `setBlob(String, Blob)`, `setClob(String, Clob)`, `setAsciiStream(String, InputStream, long)`, `setBinaryStream(String, InputStream, long)`, `setCharacterStream(String, Reader, long)`, `setAsciiStream(String, InputStream)`, `setBinaryStream(String, InputStream)`, `setCharacterStream(String, Reader)`, `setNCharacterStream(String, Reader)`, `setClob(String, Reader)`, `setBlob(String, InputStream)`, `setNClob(String, Reader)`, `getObject(String, Class)`, `setObject(String, Object, SQLType, int)`, `setObject(String, Object, SQLType)`, `registerOutParameter(int, SQLType)`, `registerOutParameter(int, SQLType, int)`, `registerOutParameter(int, SQLType, String)`, `registerOutParameter(String, SQLType)`, `registerOutParameter(String, SQLType, int)`, `registerOutParameter(String, SQLType, String)` |

**`Blob`** (11 methods: 11 LOB-03A-BLOB)

| Verdict | Methods |
|---|---|
| LOB-03A-BLOB | `length()`, `getBytes(long, int)`, `getBinaryStream()`, `position(byte[], long)`, `position(Blob, long)`, `setBytes(long, byte[])`, `setBytes(long, byte[], int, int)`, `setBinaryStream(long)`, `truncate(long)`, `free()`, `getBinaryStream(long, long)` |

**`Clob`** (13 methods: 3 LOB-03A-BLOB, 3 LOB-03B-CLOB, 7 rejected): `length`, `truncate` and `free` are inherited from `LobBase`.

| Verdict | Methods |
|---|---|
| LOB-03A-BLOB | `length()`, `truncate(long)`, `free()` |
| LOB-03B-CLOB | `getSubString(long, int)`, `getCharacterStream()`, `getAsciiStream()` |
| rejected | `position(String, long)`, `position(Clob, long)`, `setString(long, String)`, `setString(long, String, int, int)`, `setAsciiStream(long)`, `setCharacterStream(long)`, `getCharacterStream(long, long)` |

**`NClob`** (0 methods): declares no method; no object of this type is ever created (`createNClob`, `getNClob`, `setNClob` are rejected).

**`SQLXML`** (9 methods: 7 XML-02A-SQLXML, 1 XML-02B-SOURCES, 1 XML-02C-RESULTS)

| Verdict | Methods |
|---|---|
| XML-02A-SQLXML | `free()`, `getBinaryStream()`, `setBinaryStream()`, `getCharacterStream()`, `setCharacterStream()`, `getString()`, `setString(String)` |
| XML-02B-SOURCES | `getSource(Class)` |
| XML-02C-RESULTS | `setResult(Class)` |

**`Struct`** (3 methods: 3 no class): no implementing class: `Connection.createStruct` is rejected and no getter returns one.

| Verdict | Methods |
|---|---|
| no class | `getSQLTypeName()`, `getAttributes()`, `getAttributes(Map)` |

**`Ref`** (4 methods: 4 no class): no implementing class: `getRef` and `setRef` are rejected.

| Verdict | Methods |
|---|---|
| no class | `getBaseTypeName()`, `getObject(Map)`, `getObject()`, `setObject(Object)` |

**`RowId`** (4 methods: 4 no class): no implementing class: `getRowId` and `setRowId` are rejected.

| Verdict | Methods |
|---|---|
| no class | `equals(Object)`, `getBytes()`, `toString()`, `hashCode()` |

**`ConnectionPoolDataSource`** (7 methods: 2 POOL-03-DATASOURCE, 4 inherited, 1 JDK default)

| Verdict | Methods |
|---|---|
| POOL-03-DATASOURCE | `getPooledConnection()`, `getPooledConnection(String, String)` |
| DRIVER-04A-DATASOURCE (inherited from `BaseDataSource`) | `getLogWriter()`, `setLogWriter(PrintWriter)`, `setLoginTimeout(int)`, `getLoginTimeout()` |
| JDK default | `createPooledConnectionBuilder()` |

**`PooledConnection`** (6 methods: 6 POOL-01-POOLED)

| Verdict | Methods |
|---|---|
| POOL-01-POOLED | `getConnection()`, `close()`, `addConnectionEventListener(ConnectionEventListener)`, `removeConnectionEventListener(ConnectionEventListener)`, `addStatementEventListener(StatementEventListener)`, `removeStatementEventListener(StatementEventListener)` |

**`XADataSource`** (7 methods: 2 XA-03-DATASOURCE, 4 inherited, 1 JDK default)

| Verdict | Methods |
|---|---|
| XA-03-DATASOURCE | `getXAConnection()`, `getXAConnection(String, String)` |
| DRIVER-04A-DATASOURCE (inherited from `BaseDataSource`) | `getLogWriter()`, `setLogWriter(PrintWriter)`, `setLoginTimeout(int)`, `getLoginTimeout()` |
| JDK default | `createXAConnectionBuilder()` |

**`XAConnection`** (1 methods: 1 XA-02A-BRANCH)

| Verdict | Methods |
|---|---|
| XA-02A-BRANCH | `getXAResource()` |

**`XAResource`** (10 methods: 4 XA-02B-COMPLETE, 6 XA-02A-BRANCH)

| Verdict | Methods |
|---|---|
| XA-02B-COMPLETE | `commit(Xid, boolean)`, `prepare(Xid)`, `recover(int)`, `rollback(Xid)` |
| XA-02A-BRANCH | `end(Xid, int)`, `forget(Xid)`, `getTransactionTimeout()`, `isSameRM(XAResource)`, `setTransactionTimeout(int)`, `start(Xid, int)` |

**`Xid`** (3 methods: 3 XA-01-XID)

| Verdict | Methods |
|---|---|
| XA-01-XID | `getFormatId()`, `getGlobalTransactionId()`, `getBranchQualifier()` |

### 2.2 Methods deferred by JDBC-CORE

CORE section 2 left 15 methods to this design (the hand-over counts 14; `setBlob` has
three forms):

| Interface | Method | Part |
|---|---|---|
| `Connection` | `prepareCall(String)` | CALL-01-PREPARE |
| `Connection` | `prepareCall(String, int, int)` | CALL-01-PREPARE |
| `Connection` | `prepareCall(String, int, int, int)` | CALL-01-PREPARE |
| `Connection` | `createSQLXML()` | XML-01-FACTORY |
| `PreparedStatement` | `setBlob(int, Blob)` | LOB-03C-JDBC |
| `PreparedStatement` | `setBlob(int, InputStream, long)` | LOB-03C-JDBC |
| `PreparedStatement` | `setBlob(int, InputStream)` | LOB-03C-JDBC |
| `PreparedStatement` | `setClob(int, Clob)` | LOB-03C-JDBC |
| `PreparedStatement` | `setSQLXML(int, SQLXML)` | XML-03-JDBC |
| `ResultSet` | `getBlob(int)` | LOB-03C-JDBC |
| `ResultSet` | `getBlob(String)` | LOB-03C-JDBC |
| `ResultSet` | `getClob(int)` | LOB-03C-JDBC |
| `ResultSet` | `getClob(String)` | LOB-03C-JDBC |
| `ResultSet` | `getSQLXML(int)` | XML-03-JDBC |
| `ResultSet` | `getSQLXML(String)` | XML-03-JDBC |

Hooks and overrides at the extension points of JDBC-CORE 1.3:

| Member | Part | Section |
|---|---|---|
| `RsReadOnlyUpdates.checkUpdateable()` | UPD-01-ANALYSIS | 4.1 |
| `RsReadOnlyUpdates.updateValue(int, Object)` | UPD-02-VALUES | 4.2 |
| `updateAsciiStream`, `updateBinaryStream`, `updateCharacterStream` `(int, stream, int)` (overridden: the funnel of JDBC-CORE 10.5 loses the length and the kind of stream) | UPD-02-VALUES | 4.2 |
| `moveToInsertRow()`, `moveToCurrentRow()`, `cancelRowUpdates()` | UPD-02-VALUES | 4.2 |
| `updateRow()`, `deleteRow()`, `refreshRow()` | UPD-03A-UPDATE-DELETE | 4.3 |
| `insertRow()` | UPD-03B-INSERT | 4.3 |
| `rowUpdated()`, `rowInserted()`, `rowDeleted()` | stay with RS-90-READONLY-UPDATES (false, as pgjdbc) | 4.3 |
| `RsObjectGetters.featureObject` for JDBC type `SQLXML` | XML-03-JDBC | 6.3 |
| `RsObjectGetters.featureObject` for JDBC type `REF_CURSOR` | CALL-05-REFCURSOR | 3.4 |
| `getObject(int, Class)` for `Blob`, `Clob`, `NClob` (overridden: the hook does not carry the requested class) | LOB-03C-JDBC | 5.5 |
| `getObject(int, Class)` for `SQLXML` | XML-03-JDBC | 6.3 |
| `setObject` (three forms) with a `Blob`, `Clob` or `InputStream` for `Types.BLOB` (JDBC-TYPES 8.7, 8.8: "the caller handles it first") | LOB-03C-JDBC | 5.5 |
| `ConnOwnSql.beginExecute` (overridden to refuse statements during a copy) | COPY-01A-IN | 7.1 |

The 52 update methods of `ResultSet` that pgjdbc implements stay in `RsReadOnlyUpdates`
(RS-90); they work once the hooks above are overridden. The update methods CORE marks
rejected (`updateBlob`, `updateClob`, the `long`-length stream forms, ...) stay rejected.

### 2.3 The mirrored pgjdbc extension API

Verdict for every public type of pgjdbc's extension API (map 1). "CORE", "TYPES": mirrored
by a part of that design. A member not named is mirrored with its type.

| pgjdbc type | Verdict | Notes |
|---|---|---|
| `Driver`, `PGProperty`, `PGStatement`, `PGResultSetMetaData`, `jdbc.AutoSave`, `jdbc.PreferQueryMode`, `jdbc.PgConnection`, `ds.PGSimpleDataSource`, `ds.common.BaseDataSource` | CORE | `Driver.getVersion`, `notImplemented`, `getSharedTimer`: not mirrored (deprecated or internal). `BaseDataSource.getReference`, `setFromReference`, `initializeFrom`: not mirrored (no JNDI). |
| `util.PSQLException`, `PSQLState`, `PSQLWarning`, `ServerErrorMessage`, `PGobject`, `PGInterval`, `PGmoney`, `PGbytea`, `PGTime`, `PGTimestamp`, `PGBinaryObject`, `geometric.*` | TYPES, CORE | as those designs say |
| `PGConnection` | CORE (CONN-08) and FEAT-00A | Added here: `getNotifications()`, `getNotifications(int)`, `getCopyAPI()`, `getLargeObjectAPI()`, `alterUserPassword(String, char[], String)` (declared; rejects with `0A000`: nothing is authenticated in-process). Not mirrored: `getFastpathAPI()` (deprecated; `fastpath.*` is not mirrored), `getReplicationAPI()` (replication is excluded). Own additions: `resetDatabase()`, `dump()`, `restore(byte[])` (11). |
| `PGNotification` | FEAT-00A | `getName()`, `getPID()`, `getParameter()` |
| `PGRefCursorResultSet` | CALL-05 | `getRefCursor()` (deprecated) |
| `jdbc.EscapeSyntaxCallMode` | CALL-00 | `SELECT`, `CALL_IF_NO_RETURN`, `CALL`; `of`, `value` |
| `core.BaseConnection` | FEAT-00A | name and place only: the interface extends `PGConnection` and `Connection`; pgjdbc's internal methods are not mirrored; two own methods (decision 3) |
| `copy.CopyOperation`, `CopyIn`, `CopyOut` | FEAT-00A, COPY-01A | all members |
| `copy.CopyManager` | COPY-02 | all members except `copyDual` |
| `copy.CopyDual` | not mirrored | replication only |
| `copy.PGCopyInputStream`, `PGCopyOutputStream` | COPY-03 | all members |
| `util.ByteStreamWriter`, `ByteStreamTarget` | FEAT-00A | used by `CopyIn` and `LargeObject` |
| `util.ByteBufferByteStreamWriter` | COPY-02 | as a `setObject` value it is not supported (`07006`, TYPES 8.7) |
| `largeobject.LargeObjectManager`, `LargeObject` | LOB-01B, LOB-01A | all members; constructors take `BaseConnection` (pgjdbc's are package-private or take its own `BaseConnection`) |
| `largeobject.BlobInputStream`, `BlobOutputStream` | LOB-02 | all members |
| `xml.PGXmlFactoryFactory`, `DefaultPGXmlFactoryFactory`, `LegacyInsecurePGXmlFactoryFactory` | XML-01 | `EmptyStringEntityResolver`, `NullErrorHandler`: not mirrored (helpers) |
| `ds.PGConnectionPoolDataSource`, `PGPooledConnection` | POOL-03, POOL-01 | all members |
| `ds.PGPoolingDataSource` | not mirrored | deprecated in pgjdbc; a pool is the application's choice |
| `ds.common.PGObjectFactory`, `xa.PGXADataSourceFactory` | not mirrored | JNDI |
| `xa.PGXADataSource`, `PGXAConnection`, `PGXAException` | XA-03, XA-02B, XA-01 | `PGXADataSource.createReference`: not mirrored. `RecoveredXid` is package-private as in pgjdbc. |
| `fastpath.*`, `replication.*`, `ssl.*`, `gss.*`, `sspi.*`, `hostchooser.*`, `plugin.*`, `PGEnvironment`, `jdbcurlresolver.*`, `osgi.*` | not mirrored | excluded by the part record; the driver reads no environment variable (CORE decision 9) |

## 3. CallableStatement

pgjdbc: `M/jdbc/PgCallableStatement.java`; map 4 (CallableStatement).

### 3.1 Classes and `prepareCall`

Files: `jdbc/CallableRoot.java` (generated), `CallCore.java`, `CallBasicGetters.java`,
`CallObjectGetters.java`, `PgCallableStatement.java`, `EscapeSyntaxCallMode.java`,
`ConnCalls.java`. Constructor of the chain:

```java
CallableRoot(PgConnection connection, CallEscape call, int type, int concurrency, int holdability) throws SQLException
```

`CallableRoot` passes `call.sql()` and no returning columns to `PgPreparedStatement`;
`CallCore` keeps `call.isFunction()`.

`ConnCalls.prepareCall(sql, type, concurrency, holdability)`: `checkOpen()`; arguments
validated as `ConnFactories` does (CORE 5.5; the short forms use the defaults);
`CallEscape.parse(sql, standardConformingStrings(), mode)` (TYPES 4.4) with `mode` from
the connection property `escapeSyntaxCallMode` (`select`, `callIfNoReturn`, `call`; CORE
3.3 stores it); then `new PgCallableStatement(...)`. The call syntaxes accepted and the
text sent for each are exactly TYPES 4.4 C1 to C4: an escape `{call f(?)}` or `{? = call
f(?)}` becomes `select * from f(...) as result` or `call f(...)` by the mode; a text that
is no escape (`call p(?)`, `select f(?)`) is sent as written.

A callable statement is a prepared statement: setters, batches, metadata and execution
without registered parameters are inherited.

### 3.2 OUT parameters and execution

`CallCore` (CALL-02A). State: `int[] registered` (the type given, per parameter, 0 = not
registered), `int[] checked` (the type the getters compare with), `Object[] callResult`,
`boolean lastWasNull`, `boolean fetched`.

`registerOutParameter(parameterIndex, sqlType)`; the form with a scale ignores the scale
**(pgjdbc source, 174–231; map 4)**:

- `checkOpen()`; the type is normalized: `TINYINT` → `SMALLINT`, `LONGVARCHAR` → `VARCHAR`,
  `DECIMAL` → `NUMERIC`, `FLOAT` → `DOUBLE`, `VARBINARY` and `LONGVARBINARY` → `BINARY`,
  `BOOLEAN` → `BIT`.
- Not `isFunction`: `PSQLException` `2F003`.
- An index outside the parameters: `22023` (`Errors.parameterIndex`).
- The slot is marked as an output. When it has no value yet it is bound with the
  placeholder `new ParamValue(2278, null)` (a NULL of type `void`); a setter called later
  on the same index (an INOUT parameter) replaces the placeholder, a value set earlier is
  kept. The server drops parameters of type `void` from a function call's argument list
  (PostgreSQL `parse_func.c`); for `CALL` of a procedure the argument stays and stands for
  the OUT parameter.
- For the getter check, `CHAR` and `LONGVARCHAR` count as `VARCHAR` and `FLOAT` as `REAL`.

Execution (`execute`, `executeUpdate`, `executeLargeUpdate`, `executeQuery`): without a
registered parameter the inherited behavior applies unchanged. With registered parameters
**(pgjdbc source, 83–163)**:

1. Run the statement through the inherited path.
2. The first result must be a result set with at least one row, else `02000`.
3. The number of its columns must equal the number of registered parameters, else `42601`.
4. Column j is read with `getObject(j)` into the slot of the j-th registered parameter (in
   parameter order). The column's JDBC type must equal the registered type, except: a
   `DOUBLE` column for a registered `REAL` is converted to `Float`; a `REF_CURSOR` column
   is accepted for a registered `OTHER`. Any other difference: `42821`.
5. The result set is closed; the statement then has no current result. `execute()`
   returns false; `executeUpdate()` returns 0 for a function; what `executeQuery()` does
   is recorded from pgjdbc.

### 3.3 Getters

Every getter is `outValue(parameterIndex, type, getterName)` and a cast:

- nothing registered: `55000`; not yet executed: `02000` (or what pgjdbc shows);
- index outside 1..n: `22023`;
- the slot's checked type differs from the getter's type: `2200G` (`Parameter of type {0}
  was registered, but call to get{1} was made.`, same);
- the value is recorded for `wasNull()`; `wasNull()` before any getter: `55000`.

| Getter | Type compared | NULL gives |
|---|---|---|
| `getString` | `VARCHAR` | null |
| `getBoolean` | `BIT` | false |
| `getByte` | `SMALLINT` (pgjdbc registers `TINYINT` as `SMALLINT`) | 0 |
| `getShort` | `SMALLINT` | 0 |
| `getInt` | `INTEGER` | 0 |
| `getLong` | `BIGINT` | 0 |
| `getFloat` | `REAL` | 0 |
| `getDouble` | `DOUBLE` | 0 |
| `getBigDecimal` (both forms) | `NUMERIC` | null |
| `getBytes` | `VARBINARY` or `BINARY` | null |
| `getDate`, `getTime`, `getTimestamp` (without and with `Calendar`) | `DATE`, `TIME`, `TIMESTAMP` | null |
| `getArray` | `ARRAY` | null |
| `getSQLXML` | `SQLXML` | null |
| `getObject(int)` | no check | null |

The implementer confirms each row in reference mode. The `Calendar` forms re-interpret
the stored value in the calendar's zone as pgjdbc does (recorded for two zones).
`getObject(int, Map)`: a null or empty map is `getObject(int)`, else `0A000`.
`getObject(int, Class)`: `ResultSet.class` returns the value when it is a result set;
every other class `22023`.

### 3.4 Ref cursors

`RsRefCursor.featureObject(columnIndex, type)` for JDBC type `REF_CURSOR` (CALL-05;
pgjdbc `M/jdbc/PgResultSet.java` `internalGetObject`): the column's text is the cursor
name. The driver runs `FETCH ALL IN <name>` through a new statement of the connection
(type scroll-insensitive, read-only), then `CLOSE <name>` through another, with the name
quoted by `escapeIdentifier`; the result set of the first is returned with
`refCursorName` set. `getString` on the column still gives the name. A callable statement
gets the result set through step 4 of 3.2. Cursors live in a transaction: with autocommit
on the fetch fails with the server's `34000`.

### 3.5 Batches and errors

Batches are inherited (`PsBatch`): `addBatch()` stores the parameters, `executeBatch()`
runs each entry; rows a call returns are ignored and count as CORE 8 says for a query in a
batch. CALL-02B records the outcomes, including a batch on a statement with registered
parameters.

| Situation | SQLState |
|---|---|
| malformed call escape | `2F003` (TYPES 4.4 C4) |
| `registerOutParameter` on a statement that is no function call | `2F003` |
| executed with registered parameters, no row | `02000` |
| column count differs from the registered parameters | `42601` |
| registered type differs from the result type | `42821` |
| getter of another type | `2200G` |
| getter or `wasNull` with nothing registered or fetched | `55000` |
| named parameters, `registerOutParameter` with a type name or `SQLType`, the getters of 2.1 marked rejected | `0A000` |

## 4. Updatable result sets

Priority P2. pgjdbc: `M/jdbc/PgResultSet.java` (`isUpdateable` 1840, row statements 1095
to 1569); map 7.

### 4.1 When a result set is updatable

`RsUpdatable.checkUpdateable()` (UPD-01), called by every update method (CORE 10.5):

1. `concurrency` is `CONCUR_READ_ONLY`: the read-only error of CORE 10.5 (`24000`).
2. On the first call the result is analysed, once:
   - the table: the distinct non-zero `data.tableOid(column)` values of the result. None,
     or more than one: `24000` (`ResultSet is not updateable. The query that generated
     this result set must select only one table, and must select all primary keys from
     that table.`);
   - one catalog lookup for the table's schema, name and the names of the result's base
     columns (by `data.tableColumn(column)`), and one for its key candidates: the primary
     key first, then unique indexes without expressions and without a predicate, each
     with its column numbers; a candidate counts when all its columns are `NOT NULL` and
     all occur among the result's columns. The first that counts is the key;
   - no key: `24000` (`No eligible primary or unique key found for table {0}.`).
3. The outcome is kept for the life of the result set.

A column with table OID 0 (an expression) cannot be updated: `updateValue` on it raises
`24000`. Both lookups go through `connection.queryOwn` (inside a failed transaction they
fail with `25P02`, as pgjdbc's lookup does).

### 4.2 Pending values and the insert row

`RsUpdValues` (UPD-02). State: `pending` (column index → value), `onInsertRow`,
`doingUpdates`.

- `updateValue(columnIndex, value)`: `checkUpdateable()`; outside the insert row the
  cursor must be on a row (SQLState as pgjdbc shows); the index is checked; the value is
  stored. A null value means SQL NULL of the column's type.
- The three stream forms with an `int` length are overridden here: the stream or reader
  is read up to `length` and stored as a `String` (`updateAsciiStream`,
  `updateCharacterStream`) or `byte[]` (`updateBinaryStream`); a short stream as pgjdbc
  shows.
- Getters do not see pending values: they read the row as fetched until `updateRow`.
- `moveToInsertRow()`: clears pending values, `onInsertRow = true`. `moveToCurrentRow()`:
  clears them, `onInsertRow = false`; the cursor is where it was. On the insert row the
  getters behave as pgjdbc shows (recorded).
- `cancelRowUpdates()`: on the insert row `24000` (`Cannot call cancelRowUpdates() when
  on the insert row.`); else clears pending values.
- Moving the cursor discards pending values and leaves the insert row, as pgjdbc shows.

### 4.3 Row operations

`RsUpdRows` (UPD-03A), `RsUpdInsert` (UPD-03B), `EditableResultData` (UPD-03A). Statements
are prepared through the public API on the result set's connection, values bound with
`setObject(i, value)` (a null value with `setNull(i, Types.NULL)` or the column's type as
pgjdbc shows), and closed after use. Shapes (own text):

| Operation | Statement shape |
|---|---|
| `updateRow()` | `UPDATE <table> SET <column> = ?, ... WHERE <key column> = ? AND ... RETURNING <base columns of the result, in result order>` |
| `deleteRow()` | `DELETE FROM <table> WHERE <key column> = ? AND ...` |
| `refreshRow()` | `SELECT <base columns> FROM <table> WHERE <key column> = ? AND ...` |
| `insertRow()` | `INSERT INTO <table> (<pending columns>) VALUES (?, ...) RETURNING <base columns>` |

- Key values are those of the current row as fetched (`getObject` of the key columns).
- An expression column of the result keeps its old text in a changed row.
- The first change replaces `RsCore.data` by an `EditableResultData` copy and keeps
  `RsCore.rowCount` equal to its row count.
- `updateRow()`: on the insert row `24000`; not on a row: as pgjdbc shows; no pending
  value: nothing is sent. The returned row replaces the current row; pending values are
  cleared. When no row comes back (the row is gone) the current row stays as it was.
- `deleteRow()`: on the insert row `24000` (`Cannot call deleteRow() when on the insert
  row.`); not on a row: `24000` (or what pgjdbc shows); the row is removed from the result
  set and the cursor moves to the row before it **(pgjdbc source)**.
- `refreshRow()`: on the insert row `24000` (`Can't refresh the insert row.`); the fetched
  row replaces the current row; no row: unchanged.
- `insertRow()`: not on the insert row `24000` (`Not on the insert row.`); no pending
  value `24000` (`You must specify at least one column value to insert a row.`); the
  returned row is appended at the end of the result set; pending values are cleared and
  the cursor stays on the insert row.
- `rowUpdated()`, `rowInserted()`, `rowDeleted()` stay false (CORE 10.5).

Difference from pgjdbc (decision 7): the row shown after `updateRow` and `insertRow` is
what the server stored. `DatabaseMetaData.ownUpdatesAreVisible` and its relatives answer
as pgjdbc (METADATA), and these rules make the answers true.

### 4.4 Errors

All `PSQLException` `24000` unless a row above says otherwise; a server error of a row
statement is passed on unchanged and leaves the result set as it was. The quoted texts
are pgjdbc's ("same", map 7); UPD-01 to UPD-03B confirm the SQLStates in reference mode.

## 5. Large objects, Blob and Clob

pgjdbc: `M/largeobject/`, `M/jdbc/AbstractBlobClob.java`, `PgBlob.java`, `PgClob.java`;
map 10. libspq has no `lo_` client functions (`docs/LIBSPQ.md`, "Absent on purpose"):
everything goes through SQL.

### 5.1 `LargeObject`

`largeobject/LargeObject.java` (LOB-01A). Each method prepares one statement of the form
`SELECT pg_catalog.<function>(?, ...)` on the connection, executes it, reads the single
value and closes the statement:

| Method | Function | Notes |
|---|---|---|
| constructor | `lo_open(oid, mode)` | keeps the descriptor; mode `READ`, `WRITE` or both |
| `close()` | `lo_close(fd)` | idempotent; closes streams made from it first; commits when `commitOnClose` |
| `read(int len)`, `read(byte[], int, int)` | `loread(fd, len)` | bytea result through `getBytes`; the array form loops until `len` bytes or the end and returns the count |
| `write(byte[])`, `write(byte[], int, int)`, `write(ByteStreamWriter)` | `lowrite(fd, bytea)` | bytea parameter through `setBytes`; chunks of at most 1 MiB |
| `seek(int, int)`, `seek64(long, int)`, `seek(int)` | `lo_lseek64(fd, offset, whence)` | `seek(pos)` is `SEEK_SET` |
| `tell()`, `tell64()` | `lo_tell64(fd)` | the `int` forms narrow |
| `size()`, `size64()` | seek to the end, tell, seek back | |
| `truncate(int)`, `truncate64(long)` | `lo_truncate64(fd, len)` | |
| `copy()` | `lo_open` again, then seek to the same position | |
| `getInputStream()` and its two forms, `getOutputStream()` | 5.3 | |

A method after `close()` raises as pgjdbc shows. A descriptor lives as long as the
transaction: the object is unusable after commit or rollback (the server's error is
passed on).

### 5.2 `LargeObjectManager`

`largeobject/LargeObjectManager.java`, `jdbc/ConnLobs.java` (LOB-01B).
`PGConnection.getLargeObjectAPI()`: `checkOpen()`, one manager per connection.

- `open(...)` (eight forms): default mode `READWRITE`, default `commitOnClose` false; the
  `int` OID forms widen as unsigned.
- `createLO()`, `createLO(mode)`, `create()`, `create(mode)`: `SELECT
  pg_catalog.lo_creat(mode)`; default mode `READWRITE`.
- `delete(oid)`, `unlink(oid)`: `SELECT pg_catalog.lo_unlink(oid)`.
- With autocommit on, pgjdbc refuses large-object use with `25P01` (`Large Objects may not
  be used in auto-commit mode.`, same) **(pgjdbc source)**; the part records in which
  methods and follows.

### 5.3 Streams

`largeobject/BlobInputStream.java`, `BlobOutputStream.java` (LOB-02). The input stream
reads through a buffer (default size as pgjdbc, recorded; the constructor's `bufferSize`),
ends at `limit` bytes when one is given, supports `mark`/`reset` by seeking, and `skip`
by seeking. The output stream collects writes in a buffer and sends it when full, on
`flush()` and on `close()`. `close()` of either closes the large object. A database error
becomes `IOException` with the `SQLException` as cause; use after `close()` raises
`IOException`.

### 5.4 Blob and Clob

`jdbc/LobBase.java`, `PgBlob.java` (LOB-03A), `PgClob.java` (LOB-03B). An object holds
the connection and the OID; nothing is opened until first use **(pgjdbc source,
`AbstractBlobClob`)**:

- the first reading method opens the object read-only; the first writing method opens it
  (or reopens it) for writing; streams and sub-streams get descriptors of their own
  (`copy()`), which `free()` closes;
- positions are 1-based; a position below 1 or beyond what pgjdbc allows raises `22023`
  (or what pgjdbc shows);
- `length()` is `size64()`; `truncate(len)` is `truncate64`; `getBytes(pos, length)` seeks
  and reads; `position(pattern, start)` scans from `start` and returns the 1-based
  position or -1;
- `free()` closes every descriptor; afterwards every method raises `55000` (or what
  pgjdbc shows); a second `free()` does nothing;
- `PgClob`: characters are the object's bytes decoded as UTF-8; `getSubString`,
  `getCharacterStream()`, `getAsciiStream()` are implemented, the seven other `Clob`
  methods are rejected (2.1).

`Connection.createBlob`, `createClob`, `createNClob` stay rejected (CORE section 2): a
`Blob` comes from `getBlob`, a new object from `setBlob` with a stream.

### 5.5 JDBC entry points

`jdbc/RsLobs.java`, `PsLobs.java` (LOB-03C); pgjdbc `PgResultSet.getBlob`,
`PgPreparedStatement.setBlob`, `createBlob`.

- `getBlob(i)`, `getClob(i)`: the column is read with `getLong(i)` (TYPES 7.10: `22003`
  for text that is no number); NULL gives null; else `new PgBlob(connection, oid)` or
  `PgClob`. A bytea column is not a `Blob` (pgjdbc: use `getBytes`).
- `getObject(i, Class)`: `Blob.class` and `Clob.class` on a column whose JDBC type TYPES
  7.8 lists for them (BLOB, BINARY, BIGINT; CLOB, BIGINT) call `getBlob`/`getClob`;
  another column type and `NClob.class` raise `22023`.
- `setBlob(i, stream)`, `setBlob(i, stream, length)`: a new large object
  (`getLargeObjectAPI().createLO()`), the stream copied into it (at most `length` bytes; a
  short stream as pgjdbc shows), the object closed, and the OID bound with
  `BasicSetters.setLong`. `setBlob(i, Blob)`: null binds `setNull(Types.BLOB)`; else the
  same with `blob.getBinaryStream()` and `blob.length()` (always a new object).
  `setClob(i, Clob)`: null binds `setNull(Types.CLOB)`; else a new object filled with the
  UTF-8 bytes of `clob.getCharacterStream()`.
- `setObject`: a `Blob` value → `setBlob`; a `Clob` → `setClob`; an `InputStream` with
  target `Types.BLOB` → `setBlob(i, stream)`; everything else `super` (TYPES 8.7, 8.8).
- All of these need a transaction (5.2).

## 6. SQLXML

Priority P2. pgjdbc: `M/jdbc/PgSQLXML.java`, `M/xml/`. JDK XML APIs only.

### 6.1 Factories

`xml/PGXmlFactoryFactory.java` and two implementations (XML-01). The connection property
`xmlFactoryFactory` (CORE 3.3: stored) chooses: empty → `DefaultPGXmlFactoryFactory`;
`LEGACY_INSECURE` → `LegacyInsecurePGXmlFactoryFactory`; else the name of a class with a
public no-argument constructor that implements the interface (`22023` when it cannot be
loaded or is of another type). `ConnXml.xmlFactoryFactory()` resolves it once per
connection.

Secure defaults of `DefaultPGXmlFactoryFactory`, each set where the JDK's factory accepts
it (an unsupported setting is skipped, never an error):

| Factory | Settings |
|---|---|
| `DocumentBuilderFactory` | namespace aware; `disallow-doctype-decl` true; external general and parameter entities off; `load-external-dtd` off; XInclude off; entity expansion off; secure processing on; an entity resolver that returns empty content; an error handler that prints nothing |
| `TransformerFactory`, `SAXTransformerFactory` | secure processing on; `ACCESS_EXTERNAL_DTD` and `ACCESS_EXTERNAL_STYLESHEET` empty |
| `XMLInputFactory` | `SUPPORT_DTD` false; `IS_SUPPORTING_EXTERNAL_ENTITIES` false |
| `XMLOutputFactory` | defaults |
| `XMLReader` | `disallow-doctype-decl` true; external entities off; `load-external-dtd` off; the same resolver |

The legacy factory returns the JDK's factories unconfigured (namespace aware only).

### 6.2 `PgSQLXML`

`jdbc/PgSQLXML.java` (XML-02A), `XmlSources.java` (XML-02B), `XmlResults.java` (XML-02C).
States **(pgjdbc source)**: an object made by `createSQLXML()` is *empty* and writable
once; one read from a column is *readable* once; after `free()` it is *freed*. A read of
an empty object, a second read, a second write, and any use after `free()` raise
`PSQLException` (`55000`, or what pgjdbc shows for each).

- `getString()`, `getCharacterStream()`, `getBinaryStream()` (UTF-8 bytes): mark the
  value read.
- `setString(value)`, `setCharacterStream()`, `setBinaryStream()` (the writer or stream
  collects the text; it becomes the value when it is closed or when the value is needed).
- `getSource(Class)` → `XmlSources.create(factories, data, type)`:

| Class | Result |
|---|---|
| null, `DOMSource` | a `DOMSource` over the document parsed with `newDocumentBuilder()` |
| `SAXSource` | a `SAXSource` with `createXMLReader()` and an `InputSource` over the text |
| `StreamSource` | a `StreamSource` over a `StringReader` |
| `StAXSource` | a `StAXSource` over `newXMLInputFactory().createXMLStreamReader(reader)` |
| any other | `22023` (or what pgjdbc shows) |

  A parse failure raises `PSQLException` `22000` (or what pgjdbc shows).
- `setResult(Class)` → `XmlResults.create(factories, type)`:

| Class | Result; the text |
|---|---|
| null, `DOMResult` | an empty `DOMResult`; serialized with an identity transformer of `newTransformerFactory()`, without XML declaration |
| `SAXResult` | a `SAXResult` whose handler is a `TransformerHandler` of `newSAXTransformerFactory()` writing to a `StringWriter` |
| `StreamResult` | a `StreamResult` over a `StringWriter` |
| `StAXResult` | a `StAXResult` over `newXMLOutputFactory().createXMLStreamWriter(writer)` |
| any other | `22023` (or what pgjdbc shows) |

- `PgSQLXML.text()` (package-private) returns the value for a parameter, completing a
  pending result first.

### 6.3 JDBC entry points

`jdbc/ConnXml.java` (XML-01), `RsXml.java`, `PsXml.java` (XML-03).

- `createSQLXML()`: `checkOpen()`, a new empty `PgSQLXML`.
- `getSQLXML(i)`: NULL gives null; else `new PgSQLXML(connection, text)`. A column that
  is not `xml` as pgjdbc shows.
- `featureObject(i, type)` for JDBC type `SQLXML` and `getObject(i, SQLXML.class)`:
  `getSQLXML(i)`.
- `setSQLXML(i, x)`: null, or a null text, binds `setNull(Types.SQLXML)`; else `new
  ParamValue(142, text)`. `setObject` with a `PgSQLXML` value does the same (a foreign
  `SQLXML` implementation is left to TYPES 8.7, which calls its `getString()`).
- `CallableStatement.getSQLXML` returns the object stored by 3.2 step 4;
  `ResultSet.updateSQLXML` works through `updateValue` and `setObject`.

## 7. COPY

pgjdbc: `M/copy/`, `M/core/v3/QueryExecutorImpl.java` (`startCopy` 1092, `cancelCopy`,
`endCopy`); map 10. pq level: NATIVE 7.5 and 9.5 (`sendQuery`, `getResult`,
`putCopyData`, `putCopyEnd`, `getCopyData`); `docs/LIBSPQ.md` 5.4.

### 7.1 State of the connection

`jdbc/ConnCopyState.java` (COPY-01A). Field `CopyOp activeCopy` (null when none).

`startCopy(sql)` (the `BaseConnection` method), lock held:

1. `checkOpen()`; `checkNoCopy()` (`55000` when a copy is active).
2. The text is parsed with `NativeSql.parse` (no parameters) only to get the first
   statement for `token = beginExecute(first, false)`: with autocommit off the copy joins
   the transaction, exactly like a statement (CORE 6.1).
3. `pq().sendQuery(sql)`, then `pq().getResult()` once.
4. Status `COPY_IN` → a `CopyInOp`; `COPY_OUT` → a `CopyOutOp`; it becomes `activeCopy`
   and is returned. Field count and formats come from the result (`nfields`, `fformat`).
5. An error result: read `getResult()` until null, `endExecute(token, true)`, raise
   `Errors.server`. Any other status (the text was no COPY statement, or `COPY_BOTH`):
   read the results until null, `endExecute(token, false)`, raise what pgjdbc's
   `CopyManager.copyIn` raises for such a text (recorded; this driver's default is
   `42809`).

While `activeCopy` is not null:

- `beginExecute` (overridden here) raises `PSQLException` `55000` (`A COPY operation is in
  progress on this connection.`) before anything is sent: every `Statement` execution and
  batch of the application is refused. So are `startCopy`, `getNotifications`, the
  cluster-control methods and `XAResource` calls that reach `checkNoCopy()`. Large-object
  calls are statements and are refused by the hook.
- Statements the driver sends itself from CORE classes are not refused (decision 9): the
  library ends the copy. The copy object's next call then fails in the pq layer; it marks
  itself inactive, clears `activeCopy` and raises the mapped error.
- `Connection.close()` ends everything: the pq connection is closed and the operation is
  inactive afterwards. `isClosed()`, `cancelQuery()` and `Statement.cancel` stay free.

`copyFinished(op, failed)`: clears `activeCopy`, calls `endExecute(token, failed)`.

### 7.2 Operations

`jdbc/CopyOp.java`, `CopyInOp.java` (COPY-01A), `CopyOutOp.java` (COPY-01B); interfaces
`copy.CopyOperation` (FEAT-00A), `CopyIn`, `CopyOut` (COPY-01A).

| Member | Behavior |
|---|---|
| `int getFieldCount()`, `int getFormat()`, `int getFieldFormat(int field)` | from the start result; overall format 0 text, 1 binary |
| `boolean isActive()` | true until the operation ended, failed or was cancelled |
| `long getHandledRowCount()` | -1 while unknown; after the end the count of the command tag (`COPY n`) |
| `void cancelCopy()` | below; inactive operation: `55000` |
| `CopyIn.writeToCopy(buf, off, siz)`, `writeToCopy(ByteStreamWriter)` | `pq().putCopyData`; inactive: `55000` |
| `CopyIn.flushCopy()` | nothing to do (the pq call hands the data over); inactive: `55000` |
| `long CopyIn.endCopy()` | `putCopyEnd(null)`, then `getResult()` until null: a `COMMAND_OK` gives the row count; an error result ends the operation as failed and is raised (`Errors.server`) |
| `CopyIn.cancelCopy()` | `putCopyEnd(<text>)`, then the results until null; the server's `57014` answer is expected and swallowed; `copyFinished(op, true)` |
| `byte[] CopyOut.readFromCopy()`, `readFromCopy(boolean block)` | `pq().getCopyData()`: a row, or at the end null after reading the results until null (row count from the tag; an error result is raised). `block` is ignored (decision 10) |
| `CopyOut.cancelCopy()` | `cancelHandle().cancel()`, then `getCopyData()` until null and the results until null, errors swallowed; `copyFinished(op, true)` |

Every method takes the connection's lock. A `PqException` marks the operation inactive,
clears the state and is mapped with `failure`. A copy cancelled or failed in autocommit
mode stores nothing; inside a transaction the transaction is in the failed state, as
after any failed statement.

### 7.3 `CopyManager`

`copy/CopyManager.java` (COPY-02). `new CopyManager(BaseConnection)` only keeps the
connection; `PGConnection.getCopyAPI()` returns a new manager.

- `copyIn(sql)`: `startCopy(sql)`; when the result is no `CopyIn`: `cancelCopy()` and
  `PSQLException` `42809` (`Requested CopyIn but got {0}`, same). `copyOut(sql)` likewise.
- `copyIn(sql, Reader[, bufferSize])`: characters are encoded as UTF-8 and written in
  chunks of `bufferSize` (default 65536); `copyIn(sql, InputStream[, bufferSize])` writes
  the bytes; both end with `endCopy()` and return its count. `copyIn(sql,
  ByteStreamWriter)`: one `writeToCopy`, then `endCopy()`.
- `copyOut(sql, Writer)`: every row decoded as UTF-8 and written; `copyOut(sql,
  OutputStream)`: the bytes; both return `getHandledRowCount()`.
- A failure of the caller's stream or of the server while the operation is active:
  `cancelCopy()` (its own failure is suppressed), then the first exception is rethrown.

### 7.4 Streams

`copy/PGCopyInputStream.java`, `PGCopyOutputStream.java` (COPY-03). The output stream
buffers (`bufferSize`, default 65536) and writes with `writeToCopy`; `flush()` sends the
buffer; `close()` sends it and calls `endCopy()`; `endCopy()` may also be called directly.
The input stream serves `read` from the current row and fetches the next with
`readFromCopy()`; `available()` is the rest of the current row; `close()` cancels an
active operation. The `CopyOperation` methods delegate. Inside an `InputStream` or
`OutputStream` method an `SQLException` becomes `IOException` with it as cause; use after
`close()` raises `IOException`.

## 8. Notifications and the remaining extension API

### 8.1 Notifications

`jdbc/ConnNotifications.java`, `NotificationImpl.java` (NOTIFY-01). pgjdbc: `PgConnection` 1267,
`QueryExecutorImpl.processNotifies` 919; map 10. pq: `PqConnection.notifies(timeoutMillis)`
(NATIVE 7.4: 0 returns what is there, positive waits at most that long, negative waits
without limit).

| Call | Meaning (map 10) | pq call |
|---|---|---|
| `getNotifications()` | do not wait | `notifies(0)` |
| `getNotifications(t)`, t < 0 | do not wait | `notifies(0)` |
| `getNotifications(t)`, t > 0 | wait up to t milliseconds for the first notification | slices, below |
| `getNotifications(0)` | wait until one arrives | slices, below |

- `checkOpen()`, `checkNoCopy()`. The library collects notifications while it processes
  any command, so one `notifies(0)` returns everything received so far.
- Waiting is done in slices of at most 500 ms: per slice the lock is taken,
  `pq().notifies(slice)` is called and the lock released, until something arrived, the
  time is over, or the connection was closed by another thread (`08003`). A thread in a
  native wait cannot be woken (NATIVE 7.4); the slices bound how long `close()` waits.
- The result is an array of `PGNotification` in arrival order (name = channel, PID,
  parameter = payload, empty string when none); with nothing to return, what pgjdbc
  returns (an empty array; recorded).
- Notices that arrive meanwhile go to the connection's warnings (`drainNotices`).

### 8.2 Remaining `PGConnection` and `PGStatement` methods

CORE implements every `PGStatement` method (STMT-05) and the `PGConnection` methods of
CORE 5.6 (CONN-08). FEAT-00A adds to `org/stuntgres/PGConnection.java`:

| Declaration | Implemented by |
|---|---|
| `PGNotification[] getNotifications() throws SQLException`, `PGNotification[] getNotifications(int timeoutMillis) throws SQLException` | NOTIFY-01 (8.1) |
| `CopyManager getCopyAPI() throws SQLException` | COPY-01A (7.3) |
| `LargeObjectManager getLargeObjectAPI() throws SQLException` | LOB-01B (5.2) |
| `void resetDatabase() throws SQLException`, `byte[] dump() throws SQLException`, `void restore(byte[] dump) throws SQLException` | CONTROL-01 (11.1) |
| `default void alterUserPassword(String user, char[] newPassword, String encryptionType) throws SQLException` | the interface itself: `SQLFeatureNotSupportedException` `0A000` |

Until its owner is merged a method answers `0A000` from `ConnFeatureRoot`.
`BaseConnection.transactionStatus()` returns `pq().transactionStatus()` (a
`pq.TransactionStatus` value) and is implemented by FEAT-00A.

### 8.3 The surface test

PGAPI-01 holds the driver to pgjdbc's public signatures: a resource lists every public
and protected member of the mirrored P1 types of 2.3, produced once from the pgjdbc jar,
and a unit test checks by reflection that each exists under `org.stuntgres`. Members 2.3
marks "not mirrored" form a second block of the resource with their reasons. The types of
`xml` and `xa` (P2) are checked by a `surface` case of XML-01 and XA-03.

## 9. Pooled connections

pgjdbc: `M/ds/PGPooledConnection.java`, `PGConnectionPoolDataSource.java`.

### 9.1 `PGPooledConnection` and its handles

`ds/PGPooledConnection.java`, `ConnectionHandle.java` (POOL-01), `StatementHandle.java`
(POOL-02). The pooled connection owns a physical connection; `getConnection()` hands out
a *handle*: a `java.lang.reflect.Proxy` for `Connection` and `PGConnection` whose
invocation handler forwards to the physical connection. Rules **(pgjdbc source)**:

- `getConnection()`: a closed pooled connection raises `08003` (or what pgjdbc shows). An
  earlier handle that is still open is closed without an event; an open transaction is
  rolled back; warnings are cleared; autocommit is set to the constructor's `autoCommit`.
  A failure here fires `connectionErrorOccurred` and is rethrown.
- The handle: `close()` marks it closed, rolls back when autocommit is off, clears
  warnings and fires `connectionClosed` once; `isClosed()` answers from the mark; every
  other method on a closed handle raises `08003` (`Connection has been closed.`);
  `equals`, `hashCode`, `toString` are answered by the handler; `createStatement`,
  `prepareStatement`, `prepareCall` return statement handles (`StatementHandle.wrap`).
- With `isXA` true, `commit()`, `rollback()`, `setAutoCommit(true)` and `setSavepoint` on
  the handle are refused while the XA connection says a branch is active (10.3).
- An `SQLException` thrown through a handle fires `connectionErrorOccurred` when its
  SQLState is fatal: null, or starting with `08`, `53`, `58`, `60`, `99`, `F0`, `XX`, or
  one of `57P01`, `57P02`, `57P03`. The exception is rethrown unwrapped (the cause of
  the `InvocationTargetException`).
- A statement handle: `getConnection()` returns the connection handle; a closed handle
  makes its statements raise as pgjdbc shows; errors follow the rule above;
  `PGStatement` methods are forwarded.
- `close()` of the pooled connection: closes an open handle (no event), rolls back when
  needed, closes the physical connection.
- Statement event listeners are accepted and never called.

### 9.2 `PGConnectionPoolDataSource`

`ds/PGConnectionPoolDataSource.java` (POOL-03): extends `BaseDataSource` (CORE 3.6), so
it has every bean property; `getPooledConnection()` and `getPooledConnection(user,
password)` return `new PGPooledConnection(getConnection(...), defaultAutoCommit)`;
`defaultAutoCommit` is true by default; `Serializable` with the state of the base class
and that flag. `getDescription()` is the driver's own text.

## 10. XA

Priority P2. pgjdbc: `M/xa/PGXAConnection.java`, `RecoveredXid.java`; map 10. PostgreSQL
needs `max_prepared_transactions` above zero: the oracle container of the harness sets it
to 100 (NATIVE 10.3). What the stuntgres server and libspq do with `PREPARE TRANSACTION`
was not probed (decision 18).

### 10.1 Xid encoding

`xa/RecoveredXid.java`, `PGXAException.java` (XA-01). The transaction identifier of a
branch is `<formatId>_<Base64(gtrid)>_<Base64(bqual)>` with the standard Base64 alphabet
(map 10; `RecoveredXid.xidToString` 80–90). `stringToXid` splits at the first and the
last `_`, parses the number and decodes both parts with the MIME decoder; any failure
gives null (a prepared transaction that is not ours). Equality of a `RecoveredXid` with
another `Xid` compares the three values.

### 10.2 Statements

Sent through a statement of the physical connection; `<gid>` is the encoded Xid quoted
with `escapeLiteral`:

| Use | Statement |
|---|---|
| `prepare` | `PREPARE TRANSACTION <gid>` |
| `commit`, two-phase | `COMMIT PREPARED <gid>` (autocommit switched on around it) |
| `rollback` of a prepared branch | `ROLLBACK PREPARED <gid>` (likewise) |
| `commit`, one-phase; `rollback` of an active branch | `Connection.commit()`; `Connection.rollback()` |
| `recover` | a query on `pg_catalog.pg_prepared_xacts` for the `gid` column, restricted to the current database and to transactions whose owner the current user is or is a member of (own text) |

### 10.3 State table

`xa/XaBranch.java` (XA-02A: `start`, `end`, `forget`, `isSameRM`, timeouts, the handle),
`PGXAConnection.java` (XA-02B: the rest). State: `IDLE`, `ACTIVE`, `ENDED`; `currentXid`
(the branch started here), `preparedXid`, `committedXid`, `localAutoCommit` (autocommit
before `start`). All rows **(pgjdbc source)**; XA-02A and XA-02B confirm each with a
throwaway pgjdbc program (R11). A refused call raises `PGXAException` with the code and
changes nothing.

| Call | Condition | Outcome |
|---|---|---|
| `start(xid, flags)` | flags other than `TMNOFLAGS`, `TMRESUME`, `TMJOIN`; xid null | `XAER_INVAL` |
| | state `ACTIVE` | `XAER_PROTO` |
| | `TMRESUME` | `XAER_RMERR` (suspend and resume are not implemented) |
| | `TMJOIN` and state not `ENDED`, or another xid than `currentXid` | `XAER_RMERR` |
| | `TMNOFLAGS` and state `ENDED` | `XAER_RMERR` (no interleaving) |
| | `TMNOFLAGS`, state `IDLE` | remember autocommit, `setAutoCommit(false)` (failure: `XAER_RMERR`); state `ACTIVE`; `currentXid = xid`; `preparedXid = null` |
| | `TMJOIN`, state `ENDED`, same xid | state `ACTIVE` |
| `end(xid, flags)` | flags other than `TMSUSPEND`, `TMFAIL`, `TMSUCCESS`; xid null | `XAER_INVAL` |
| | state not `ACTIVE`, or another xid | `XAER_PROTO` |
| | `TMSUSPEND` | `XAER_RMERR` |
| | `TMSUCCESS`, `TMFAIL` | state `ENDED` |
| `prepare(xid)` | no `currentXid` and `preparedXid` set | `XAER_PROTO` |
| | no `currentXid` | `XAER_NOTA` |
| | another xid than `currentXid` | `XAER_RMERR` |
| | state not `ENDED` | `XAER_INVAL` |
| | else | state `IDLE`; `preparedXid = currentXid`; `currentXid = null`; `PREPARE TRANSACTION`; restore autocommit; returns `XA_OK`. A server error: the code of the mapping below |
| `commit(xid, true)` | xid null | `XAER_INVAL` |
| | xid equals `preparedXid` | `XAER_PROTO` |
| | no `currentXid` | `XAER_NOTA` |
| | another xid than `currentXid` | `XAER_RMERR` |
| | state not `ENDED` | `XAER_PROTO` |
| | else | state `IDLE`; `currentXid = null`; `committedXid = xid`; `commit()`; restore autocommit. A server error: the mapping below |
| `commit(xid, false)` | state not `IDLE`, or `transactionStatus()` not idle | `XAER_RMERR` |
| | else | `COMMIT PREPARED`; `committedXid = xid`. Server `42704` (no such prepared transaction): `XAER_NOTA`; a connection error: `XAER_RMFAIL`; other: `XAER_RMERR` |
| `rollback(xid)` | xid equals `currentXid` | state `IDLE`; `currentXid = null`; `rollback()`; restore autocommit |
| | else | `ROLLBACK PREPARED`. Server `42704`: `XAER_NOTA`; a connection error: `XAER_RMFAIL`; other: `XAER_RMERR` |
| `recover(flag)` | flag other than `TMSTARTRSCAN`, `TMENDRSCAN`, `TMNOFLAGS` or the first two together | `XAER_INVAL` |
| | without `TMSTARTRSCAN` | an empty array |
| | with `TMSTARTRSCAN` | the query of 10.2; every gid that decodes becomes a `RecoveredXid`; a failure: `XAER_RMERR` |
| `forget(xid)` | always | `XAER_NOTA` |
| `isSameRM(r)` | | `r == this` |
| `getTransactionTimeout()`, `setTransactionTimeout(s)` | | 0; false |

Mapping of a server error at `prepare` and one-phase `commit` (map 10, 689–697): SQLState
class `23` → `XA_RBINTEGRITY`; otherwise `XAER_RMFAIL`. `getConnection()` of the XA
connection returns the pooled handle with `isXA` true; outside a branch the physical
connection is switched to autocommit on. While the state is not `IDLE` the handle refuses
the transaction methods of 9.1 with `PSQLException` `55000` (or what pgjdbc shows).

### 10.4 Data source

`xa/PGXADataSource.java` (XA-03): extends `BaseDataSource`; `getXAConnection()` and
`getXAConnection(user, password)` return `new PGXAConnection((BaseConnection)
getConnection(...))`.

## 11. Cluster control and the isolation helper

Native only (plan B15): gated on LIBSPQ-CABI-14-CONTROL and -15-DUMP through
JDBC-PQ-14A and -14B; `docs/LIBSPQ.md` section 6 has the contracts.

### 11.1 `Stuntgres` and the `PGConnection` methods

`org/stuntgres/Stuntgres.java`, `jdbc/ConnControl.java` (CONTROL-01).

| Method | Behavior |
|---|---|
| `static String Stuntgres.nativeVersion()` | `PqCluster.version()` (the version libspq reports) |
| `static void Stuntgres.dropCluster(String cluster)`, `dropCluster(String cluster, int port)` | `PqCluster.drop(cluster, port)`; port 5432 in the short form. The next connection to the name gets a new, empty cluster. Fails while the cluster has open connections. A cluster that never existed counts as dropped. |
| `void PGConnection.resetDatabase()` | `PqExt.reset(pq())`: all persistent objects and data of the connected database are removed; cluster, roles, other databases and this connection stay |
| `byte[] PGConnection.dump()` | `PqExt.dump(pq())`: the connected database in the engine's native dump format |
| `void PGConnection.restore(byte[] dump)` | `PqExt.restore(pq(), dump)`: the connected database is replaced |

Rules:

- The static methods map a `PqException` themselves: `UNSUPPORTED` →
  `SQLFeatureNotSupportedException` `0A000`; `BAD_ARGUMENT` → `PSQLException` `22023`;
  `CALL_FAILED` of a drop → `55006` (the cluster is in use); loader failures → `08001`;
  anything else → `99999`. Null or empty cluster and a port outside 1..65535: `22023`.
- The connection methods run under the lock: `checkOpen()`, `checkNoCopy()`; a
  transaction status other than idle raises `PSQLException` `25001` (`... cannot run
  inside a transaction`); `restore(null)` raises `22023`; a `PqException` is mapped with
  `failure` (so `UNSUPPORTED` is `0A000`, CORE 4.3).
- After `resetDatabase()` and `restore()` the connection clears `columnInfo` (CORE 5.1).
  The type lookup's cache cannot be cleared (CORE 11.3): OIDs of user-defined types that
  were looked up before the call may be stale on this connection; `docs/JDBC.md` says to
  open a new connection after a restore when such types are used.
- With a real libpq behind the shim (oracle and server mode) every method raises `0A000`.

### 11.2 `IsolatedCluster`

`org/stuntgres/IsolatedCluster.java` (CONTROL-02): an `AutoCloseable` that owns one
cluster name nobody else uses. It depends on no test framework: JUnit 5 uses it in
try-with-resources or a field closed in `@AfterEach`/`@AfterAll`, JUnit 4 in `@After`.

| Member | Behavior |
|---|---|
| `static IsolatedCluster create()`, `create(String prefix)` | a new name `<prefix>-<pid>-<counter>-<8 hex digits>`; default prefix `test`; the prefix must match `[a-z][a-z0-9]{0,31}` (`IllegalArgumentException` otherwise). Nothing is loaded or created yet. |
| `String name()`, `int port()` | the name; 5432 |
| `String url()`, `url(String database)` | `jdbc:stuntgres://<name>:5432/<database>`; database `test` in the short form |
| `Connection connect()`, `connect(String database, Properties info)` | `DriverManager.getConnection`; the connection is remembered |
| `void close()` | closes the remembered connections that are still open, then `Stuntgres.dropCluster(name)`; idempotent. A connection opened elsewhere on the URL and still open makes it raise `55006`. |

## 12. The `jdbc:postgresql:` opt-in

Plan J12. System property **`stuntgres.jdbc.acceptPostgresqlUrls`**: when it is `true`
(`Boolean.getBoolean`, read at every call), `DriverUrl` accepts the prefix
`jdbc:postgresql:` as well as `jdbc:stuntgres:`, with the same grammar (CORE 3.1); so
`Driver.acceptsURL` and `connect` take such URLs. Without it they are refused
(`acceptsURL` false, `connect` null), as CORE 3.5 says. DRIVER-05 changes only the prefix
test in `core/DriverUrl.java`.

Interaction with pgjdbc: when both drivers are registered, `DriverManager` asks them in
registration order and the first non-null `connect` wins. With the property set and
pgjdbc registered first, pgjdbc gets the URL and tries a network connection. The driver
deregisters nothing: an application that wants this driver to answer `jdbc:postgresql:`
URLs removes pgjdbc from its test class path or calls `org.stuntgres.Driver` directly.
`DatabaseMetaData.getURL()` returns the URL as given.

## 13. Jar, root Makefile, smoke test

### 13.1 Jar contents

Built by `make -C bindings/jdbc jar LIBSPQ=<path>` (NATIVE 3.2, BUILD-01):
`bindings/jdbc/target/stuntgres-jdbc-<version>.jar` with

- the classes of `org.stuntgres` and its subpackages;
- `META-INF/services/java.sql.Driver` (DRIVER-03);
- `org/stuntgres/version.properties` (`version=<pom version>`);
- `META-INF/MANIFEST.MF` with `Automatic-Module-Name: org.stuntgres.jdbc` and
  `Implementation-Version`;
- `META-INF/native/linux-<arch>/libstuntgres_jni.so`, `libspq.so`, `natives.sha256`.

`tools/check_jar.sh JAR ARCH [--with-libspq]` (MAKE-01) verifies this list with `unzip
-l` and `unzip -p` (the manifest lines, the checksum file naming both libraries); without
the flag `libspq.so` is not required. The version has one source, the `<version>` of
`pom.xml`. No sources jar, no javadoc jar, no publication.

### 13.2 Version check

`jdbc/ConnVersionCheck.java` (MAKE-01). In its constructor, after the connection is open,
once per process (a static flag; later connections skip it):

1. Skip unless `PqLibrary.prefix()` is `SPQ` and `PqLibrary.hasFeature(Feature.EXT_VERSION)`
   (a real libpq in oracle and server mode is never checked).
2. `compatible(driverVersion, PqCluster.version())`: both texts start with
   `<major>.<minor>` and these two numbers are equal (a suffix such as `-SNAPSHOT` and
   the patch number are ignored).
3. Not compatible: close the pq connection and raise `PSQLException` `08001` with a text
   that names both versions, `PqLibrary.path()` and the property `stuntgres.native.dir`.
   The flag is not set, so the next connection fails the same way.

libspq and the driver are built from one tree and both are at 0.1.0 today (NATIVE 11.3,
`NativeModeTest`); the check catches a `stuntgres.native.dir` that points at another
build.

### 13.3 Root Makefile targets

MAKE-02 is the only JDBC part that edits the root `Makefile`. It appends one block after
the binding blocks that exist then (libspq's block of `docs/design/LIBSPQ-CABI.md` 8.3
and the Python binding's), in their style (a comment header, `JDBC_OUT = build/jdbc`,
`JDBC_SRC = bindings/jdbc`, one comment line per target), adds `jdbc` and `jdbc-check` to
the `.PHONY` line, and changes nothing else: not `quickcheck`, `finalcheck`, `deps` or
`build`. `.gitignore` already ignores `/build/` (LIBSPQ-BUILD-01-SKELETON). Pending parts
that edit the same file, each a prerequisite: LIBSPQ-BUILD-01-SKELETON,
CPYTHON-MAKE-02-TARGETS.

| Target | Depends on | Runs |
|---|---|---|
| `jdbc` | `libspq` | `$(MAKE) -C $(JDBC_SRC) jar LIBSPQ=$(CURDIR)/build/libspq/lib/libspq.so`; `mkdir -p $(JDBC_OUT)`; copy `$(JDBC_SRC)/target/stuntgres-jdbc-*.jar` to `$(JDBC_OUT)/`; `$(JDBC_SRC)/tools/check_jar.sh <jar> $$(uname -m) --with-libspq` |
| `jdbc-check` | `jdbc` | `$(MAKE) -C $(JDBC_SRC) check MODE=unit`; `... MODE=native`; then the smoke test of 13.4. With `REFERENCE=1` also `MODE=reference` and `MODE=oracle` (Docker); with `SERVER=1` also `MODE=server`. The database modes run under the memory cap of 17.2. |

The exact text is MAKE-02's; its acceptance is `make -n jdbc` and `make -n jdbc-check`
printing these commands in this order, and both targets passing.

### 13.4 Jar smoke test

`bindings/jdbc/tools/JarSmoke.java` (MAKE-02): one source file, run by `jdbc-check` as

```
$(JAVA_HOME)/bin/java --enable-native-access=ALL-UNNAMED -cp build/jdbc/stuntgres-jdbc-<version>.jar bindings/jdbc/tools/JarSmoke.java
```

(source-file mode: the class path holds only the jar). It checks, in this order: loading
`org.stuntgres.Driver` maps no native library (`/proc/self/maps`); a connection through
`DriverManager` to a cluster with a unique name; a prepared statement with typed
results; commit and rollback; the two libraries are mapped from the extraction directory
(NATIVE 8.3); two threads with a connection each; a second class loader with its own
copy of the driver connects too (NATIVE 8: the engine stays one copy per process); the
clusters are dropped. It prints `jar smoke: ok` and exits 0, or a stack trace and exit 1.

## 14. Documentation

DOCS-01 comes last, edits no code, and is ordered after the pending parts that edit the
same documents: LIBSPQ-DOCS-01-FINAL and CPYTHON-DOCS-01-FINAL (all four shared
documents), LIBSPQ-PGSESSION-19-DOCS, CLEANUP-DOC-STALE-CONTRACTS and
GRIND-INFRA-DOCKER-IMAGE (`AGENTS.md`). Not ordered, on purpose (18.2): the P3 parts
LIBSPQ-EXT-01-PORTAL and LIBSPQ-EXT-02-NOTICE-QUEUE, which edit section 6 of
`docs/LIBSPQ.md` (DOCS-01 edits one paragraph of section 8); waiting for them would hold
the documentation back the way waiting for FETCH-01-PORTAL would.

| Document | Change |
|---|---|
| `bindings/jdbc/README.md` (owner BUILD-01) | rewritten, short: what the directory is, how to get the jar (`make jdbc`), a five-line usage example with a `jdbc:stuntgres:` URL and `IsolatedCluster`, the layout of the directory, the check commands, a link to `docs/JDBC.md` and the four design documents |
| `docs/JDBC.md` | the status paragraph: implemented, not "being built"; every "later" removed or replaced by what was built; section 10 brought in line with the final commands; the version of the jar |
| `AGENTS.md` | package map: one entry for `bindings/jdbc` (JDBC driver over libspq; Java 17, Maven under make; tests in four modes); Commands: `make jdbc`, `make jdbc-check` (not part of `quickcheck`) and `make -C bindings/jdbc check MODE=...` |
| `docs/DEVELOPMENT.md` | Make targets: the two root targets and the binding's own; Dependencies: a JDK 17 or newer with `JAVA_HOME`, Maven 3.9 with the pinned plugins in the local repository (offline build), a C compiler, Docker for reference and oracle mode |
| `docs/TESTING.md` | placement policy: the JUnit suite of `bindings/jdbc` is a sanctioned binding suite that issues SQL outside `reference/`, by owner decision (plan J10); its four modes and what each proves; it asserts JDBC API behavior, not SQL semantics; the memory cap for server and native runs |
| `docs/LIBSPQ.md` section 8 | the Java paragraph brought in line with what was built: JNI shim that loads libspq with `dlopen`, blocking functions only, a timed `poll` on the descriptor for notifications (no `SPQsocketPoll`), fetch size as a hint until the portal extension, a link to `docs/JDBC.md` |

Where this document and the tree disagree at that time, the tree wins and DOCS-01
describes what exists.

## 15. Native verification

Plan J4: the implementation parts run in unit, reference, oracle and server mode and carry
no LIBSPQ prerequisite. The `JDBC-NATIVE` parts later run each test family through libspq.

### 15.1 The family-to-LIBSPQ mapping

Derived from the native-method table (NATIVE 5.1) and the steps table of
`docs/design/LIBSPQ-CABI.md` section 10:

| pq part (NATIVE) | Library functions | LIBSPQ part |
|---|---|---|
| PQ-02C | `libVersion` | BUILD-01-SKELETON |
| PQ-04A, PQ-04B | `connectStart`, `connectPoll`; `setNoticeReceiver`; `status`, `socket`, `finish`, `errorMessage`, `transactionStatus`, `parameterStatus`, `serverVersion`, `backendPID` | CABI-12-CONNECT-ASYNC; CABI-05-ERRORS; CABI-01-CONNECT |
| PQ-05B, PQ-06A | `exec`; `sendQuery`, `getResult`, `clear` and the basic accessors; the other accessors, `freemem`; `resultErrorField` | CABI-03-BLOCKING; CABI-02A-BLOCK; CABI-02-RESULT; CABI-05-ERRORS |
| PQ-06B, PQ-07 | `execParams`, `prepare`, `execPrepared`, `describePrepared`, `closePrepared` | CABI-04-PREPARED |
| PQ-08 | the notice receiver | CABI-05-ERRORS |
| PQ-09 | `getCancel`, `cancel`, `freeCancel` | CABI-08-CANCEL |
| PQ-10 | `consumeInput`, `notifies`, `socket` | CABI-02-RESULT, CABI-10-NOTIFY |
| PQ-11 | `putCopyData`, `putCopyEnd`, `getCopyData` | CABI-09-COPY |
| PQ-12 | `escapeLiteral`, `escapeIdentifier`; `setClientEncoding` | CABI-06-ESCAPE; CABI-13-ENCODING |
| PQ-14A, PQ-14B | `Xversion`, `XclusterDrop`, `Xreset`; `Xdump`, `Xrestore` | BUILD-01, CABI-14-CONTROL; CABI-15-DUMP |

**Base set** of every JDBC family (a connection through the driver, statements with
parameters, errors and notices, and the cluster drop of the harness): CABI-04-PREPARED,
CABI-05-ERRORS, CABI-12-CONNECT-ASYNC, CABI-14-CONTROL (each requires CABI-03 and through
it CABI-01, -02A, -02). A family adds the parts of the pq calls it makes:

| Native part | Family (JDBC parts) | LIBSPQ parts beyond the base set |
|---|---|---|
| `NATIVE-01-PQ` | PQ-04A-CONN-BASE, PQ-04B-SESSION, PQ-05B-RESULT-COPY, PQ-06A-EXEC, PQ-06B-PARAMS, PQ-07-PREPARED, PQ-08-NOTICES, PQ-09-CANCEL, PQ-10-NOTIFY, PQ-11-COPY, PQ-12-ESCAPE, PQ-13-CLEANER | CABI-06-ESCAPE, CABI-08-CANCEL, CABI-09-COPY, CABI-10-NOTIFY, CABI-13-ENCODING |
| `NATIVE-02-TYPES` | TYPE-04-LOOKUP, ARRAY-03-JDBC, TYPE-90-ROUNDTRIP-SUPPORT, TYPE-91-ROUNDTRIP-TEXT-NUMBER, TYPE-92-ROUNDTRIP-DATETIME, TYPE-93-ROUNDTRIP-OBJECT, TYPE-94-ROUNDTRIP-SET, TYPE-95-ROUNDTRIP-SQL, TYPE-96-ROUNDTRIP-ARRAY | CABI-06-ESCAPE |
| `NATIVE-03-CONN` | ERR-02-MAPPING, DRIVER-03-DRIVER, DRIVER-04A-DATASOURCE, CONN-01A-OPEN-CLOSE, CONN-01B-EXEC, CONN-02-TXN, CONN-03-TXN-OPTIONS, CONN-04A-SAVEPOINT, CONN-04B-AUTOSAVE, CONN-05-SESSION, CONN-06-VALID, CONN-07-FACTORIES, CONN-08-WRAPPER, FEAT-00A-CONN-SKELETON | CABI-06-ESCAPE, CABI-08-CANCEL |
| `NATIVE-04-STMT` | STMT-01A-STATE, STMT-01B-RUN, STMT-01C-QUERY-UPDATE, STMT-02-RESULTS, STMT-03-LIMITS, STMT-04-CANCEL, STMT-05-EXTENSIONS, BATCH-01-STATEMENT, BATCH-02-PREPARED, KEYS-01-GENERATED, PSTMT-01-STORE, PSTMT-02-BASIC-SETTERS, PSTMT-03-DATETIME-SETTERS, PSTMT-04-STREAM-SETTERS, PSTMT-05-OBJECT-SETTERS, PSTMT-90-METADATA | CABI-06-ESCAPE, CABI-08-CANCEL, CABI-09-COPY |
| `NATIVE-05-RS` | RS-00B-DATA, RS-01A-FORWARD, RS-01B-SCROLL, RS-02-COLUMNS, RS-03-TEXT-GETTERS, RS-04-NUMBER-GETTERS, RS-05-DATETIME-GETTERS, RS-06-OBJECT-GETTERS, RS-90-READONLY-UPDATES, RSMD-01-BASIC, RSMD-02-CATALOG | – |
| `NATIVE-06-DBMD` | DBMD-00-SKELETON, DBMD-01-BASE, DBMD-02A-SQL-FEATURES, DBMD-02B-TXN-FEATURES, DBMD-03-LIMITS, DBMD-04-IDENTIFIERS, DBMD-05-KEYWORDS, DBMD-10-FIXTURE, DBMD-11-SCHEMAS, DBMD-12-TABLES, DBMD-13-COLUMNS, DBMD-14-PRIMARY-KEYS, DBMD-15-FOREIGN-KEYS, DBMD-16-INDEXES, DBMD-17-TYPE-INFO, DBMD-18A-PROCEDURES, DBMD-18B-PROCEDURE-COLUMNS, DBMD-19A-FUNCTIONS, DBMD-19B-FUNCTION-COLUMNS, DBMD-20A-TABLE-PRIVILEGES, DBMD-20B-COLUMN-PRIVILEGES, DBMD-21-UDT | – |
| `NATIVE-07-CALL` | CALL-01-PREPARE, CALL-02A-OUT, CALL-02B-MODES-BATCH, CALL-03-BASIC-GETTERS, CALL-04-OBJECT-GETTERS, CALL-05-REFCURSOR | CABI-06-ESCAPE |
| `NATIVE-08-UPD` (P2) | UPD-01-ANALYSIS, UPD-02-VALUES, UPD-03A-UPDATE-DELETE, UPD-03B-INSERT | CABI-06-ESCAPE |
| `NATIVE-09-LOB` | LOB-01A-OBJECT, LOB-01B-MANAGER, LOB-02-STREAMS, LOB-03A-BLOB, LOB-03B-CLOB, LOB-03C-JDBC | – |
| `NATIVE-10-XML` (P2) | XML-02A-SQLXML, XML-02B-SOURCES, XML-02C-RESULTS, XML-03-JDBC | – |
| `NATIVE-11-COPY-NOTIFY` | COPY-01A-IN, COPY-01B-OUT, COPY-02-MANAGER, COPY-03-STREAMS, NOTIFY-01-API | CABI-08-CANCEL, CABI-09-COPY, CABI-10-NOTIFY |
| `NATIVE-12-POOL` | POOL-01-POOLED, POOL-02-STATEMENTS, POOL-03-DATASOURCE | CABI-08-CANCEL |
| `NATIVE-13-XA` (P2) | XA-02A-BRANCH, XA-02B-COMPLETE, XA-03-DATASOURCE | CABI-06-ESCAPE |

Cluster control (CONTROL-01, CONTROL-02) is native-only from the start and carries its
LIBSPQ prerequisites itself (base set and CABI-15-DUMP); it has no separate native part.
Pure unit families (the conversion functions of TYPES, DRIVER-05, PGAPI-01, MAKE-01, XA-01)
need none.

### 15.2 The procedure of a native part

These parts are marked "a stronger model is advisable": they must tell a fault of the
binding from a fault of libspq or the engine, and nothing they meet was observed when
they were written.

1. `make libspq`; without `build/libspq/lib/libspq.so` afterwards, stop (R8).
2. Run the family's test classes in native mode under the memory cap (17.2), `TEST=` with
   the class names the part lists. Classes restricted to other modes are skipped by the
   gates; that is intended.
3. For every failure decide, with evidence:
   - **a fault of the binding** that native mode exposes (an assumption that holds for
     libpq over TCP and is not part of the contract of `docs/LIBSPQ.md`): repair it in
     the driver files of the family and prove the repair in unit, oracle and server mode;
   - **anything else** (libspq or the engine answers differently from PostgreSQL through
     libpq, or a test asserts TCP behavior that `docs/LIBSPQ.md` section 5 documents
     differently): stop and report under R8 with the smallest call sequence and both
     answers. The coordinator files a LIBSPQ or engine part, or corrects design and test.
4. No test file is changed. Acceptance: the family passes in native mode; the other modes
   still pass; the commit body lists every failure, its case and its evidence.

Known in advance: a notice raised while a session starts cannot reach the driver with
libspq (hand-over point 1 of NATIVE; no test asserts it); the engine reports its own
`server_version`; a failed connect has only two causes (CORE 4.3).

### 15.3 JDK matrix and the final part

NATIVE-99-FINAL, after every native part, MAKE-02 and LIBSPQ-DOCS-01-FINAL: `make
jdbc-check` (unit and native suites, jar smoke test) with `JAVA_HOME` set to a JDK 17 and
again to a JDK 21, each under the memory cap; then the central cases of the gate
experiment of NATIVE section 2 against the real `libspq.so`, as a throwaway program kept
under `.cswd/tmp/JDBC-NATIVE-99-FINAL/`: implicit null checks and `StackOverflowError`
after the first connection, a GC stress loop with open connections (never `System.gc()`
in a tight loop), a thread dump while a statement runs, 32 threads with a connection
each, cancel from another thread, and a run under `-Xcheck:jni` whose only output is the
handler warnings of NATIVE 2.2. A difference from NATIVE section 2 is a stop condition.

## 16. Fetch in slices

FETCH-01-PORTAL, priority P3, needs a stronger model, gated on LIBSPQ-EXT-01-PORTAL
(`SPQXsendBindPortal`, `SPQXsendExecutePortal`, their blocking forms,
`SPQXresultSuspended`; `docs/LIBSPQ.md` section 6, `docs/design/LIBSPQ-CABI.md` 3.2) and
on NATIVE-99-FINAL. Until then `setFetchSize` is a stored hint (plan J11).

Specified as far as the present contract allows:

- **Conditions** (map 4, "Cursor/portal use"): fetch size above 0, the result set
  forward-only and not holdable, and autocommit off (or a transaction open). Otherwise
  the result is read completely, as today.
- **Shape**: the statement is bound to a portal and executed with a row limit of the
  fetch size; `ResultSet.next()` past the last fetched row executes the portal again;
  the portal is closed with the result set or when the transaction ends. With a real
  libpq (oracle and server mode) the extension does not exist and the driver keeps
  reading completely (a feature flag of the shim, NATIVE 4.3).
- **Files**: the stub `jdbc/StmtFeatures.java` is this part's; it needs native methods in
  the shim and a result-set class that fetches lazily. Because every other JDBC part is
  its prerequisite through NATIVE-99-FINAL, it may edit finished files; it names each in
  its commit body and edits none that JDBC-DOCS-01-FINAL names.
- **Open** (the judgement calls of the part): how `RsCore`'s row counting (`rowCount`,
  `isLast`, `isAfterLast`) works with an unknown total; what happens to an open portal
  when another statement runs on the connection; `setFetchSize` on the result set
  between fetches; generated keys and multi-statement calls (pgjdbc uses a portal only
  for a single statement).

## 17. Steps

### 17.1 Parts

Every part lists `JDBC-DESIGN-FEATURES` as a prerequisite; the table shows the others
(IDs without the prefix `JDBC-` unless they belong to another family). "Size" is code +
tests in lines; "gen." counts generated files, which are not part of the estimate.
Priority is P1 unless the Part column says otherwise.

| Part | Outcome | Files | Prerequisites | Size | gen. |
|---|---|---|---|---|---|
| `FEAT-00A-CONN-SKELETON` | Skeleton of the feature layer on the connection: PGConnection additions, BaseConnection, ConnFeatureRoot, chain stubs | `PGConnection.java` (edit), `PGNotification.java`, `core/BaseConnection.java`, `copy/CopyOperation.java`, `util/ByteStreamWriter.java`, `util/ByteStreamTarget.java`, `copy/CopyManager.java` (stub; owner COPY-02-MANAGER), `largeobject/LargeObjectManager.java` (stub; owner LOB-01B-MANAGER), `jdbc/ConnFeatureRoot.java`, `jdbc/ConnVersionCheck.java` (stub; owner MAKE-01-JAR), `jdbc/ConnCalls.java` (stub; owner CALL-01-PREPARE), `jdbc/ConnLobs.java` (stub; owner LOB-01B-MANAGER), `jdbc/ConnXml.java` (stub; owner XML-01-FACTORY), `jdbc/ConnCopyState.java` (stub; owner COPY-01A-IN), `jdbc/ConnNotifications.java` (stub; owner NOTIFY-01-API), `jdbc/ConnControl.java` (stub; owner CONTROL-01-API), `jdbc/ConnFeatures.java` (replaces the stub of CONN-00-SKELETON), `test: jdbc/ConnFeatureSkeletonTest.java`, `test: jdbc/ConnFeatureRootTest.java` | CONN-08-WRAPPER, DRIVER-03-DRIVER, TEST-03-JDBC-SUPPORT | 330 + 110 |  |
| `FEAT-00B-STMT-RS-SKELETON` | Skeleton of the feature layer on prepared statements and result sets: chain stubs, final PsFeatures and RsFeatures | `jdbc/PsLobs.java` (stub; owner LOB-03C-JDBC), `jdbc/PsXml.java` (stub; owner XML-03-JDBC), `jdbc/PsFeatures.java` (replaces the stub of PSTMT-00-SKELETON), `jdbc/RsLobs.java` (stub; owner LOB-03C-JDBC), `jdbc/RsXml.java` (stub; owner XML-03-JDBC), `jdbc/RsRefCursor.java` (stub; owner CALL-05-REFCURSOR), `jdbc/RsUpdatable.java` (stub; owner UPD-01-ANALYSIS), `jdbc/RsUpdValues.java` (stub; owner UPD-02-VALUES), `jdbc/RsUpdRows.java` (stub; owner UPD-03A-UPDATE-DELETE), `jdbc/RsUpdInsert.java` (stub; owner UPD-03B-INSERT), `jdbc/RsFeatures.java` (replaces the stub of RS-00A-SKELETON), `test: jdbc/StmtRsFeatureSkeletonTest.java` | PSTMT-00-SKELETON, RS-00A-SKELETON, BUILD-02-RUNNER | 150 + 50 |  |
| `CALL-00-SKELETON` | Skeleton of CallableStatement: generated CallableRoot, chain stubs, PgCallableStatement, EscapeSyntaxCallMode | `jdbc/CallableRoot.java` (generated), `jdbc/CallCore.java` (stub; owner CALL-02A-OUT), `jdbc/CallBasicGetters.java` (stub; owner CALL-03-BASIC-GETTERS), `jdbc/CallObjectGetters.java` (stub; owner CALL-04-OBJECT-GETTERS), `jdbc/PgCallableStatement.java`, `jdbc/EscapeSyntaxCallMode.java`, `test: jdbc/CallableRootTest.java` | PSTMT-00-SKELETON, SQL-06-CALL, ERR-02-MAPPING, BUILD-02-RUNNER | 130 + 80 | 780 |
| `CALL-01-PREPARE` | Connection.prepareCall: ConnCalls and the rejected methods of CallableStatement | `jdbc/ConnCalls.java` (replaces the stub of FEAT-00A-CONN-SKELETON), `test: jdbc/PrepareCallTest.java`, `test: jdbc/CallRejectedTest.java` | FEAT-00A-CONN-SKELETON, CALL-00-SKELETON, CONN-07-FACTORIES, PSTMT-01-STORE, DRIVER-01-PROPERTIES | 90 + 190 |  |
| `CALL-02A-OUT` | CallableStatement core: OUT parameter registration, execution, the result row, wasNull | `jdbc/CallCore.java` (replaces the stub of CALL-00-SKELETON), `test: jdbc/CallOutTest.java` | CALL-01-PREPARE, PSTMT-02-BASIC-SETTERS, RS-06-OBJECT-GETTERS, RSMD-01-BASIC, STMT-02-RESULTS, CONN-02-TXN | 210 + 200 |  |
| `CALL-02B-MODES-BATCH` | CallableStatement: call syntaxes against functions and procedures under each escapeSyntaxCallMode, batches of calls (tests) | `test: jdbc/CallModesTest.java`, `test: jdbc/CallBatchTest.java`, `jdbc/CallCore.java` (edit only if a case of this part shows it wrong) | CALL-02A-OUT, CALL-03-BASIC-GETTERS, BATCH-02-PREPARED | 20 + 300 |  |
| `CALL-03-BASIC-GETTERS` | CallableStatement getters: strings, numbers, bytes | `jdbc/CallBasicGetters.java` (replaces the stub of CALL-00-SKELETON), `test: jdbc/CallBasicGettersTest.java` | CALL-02A-OUT | 110 + 180 |  |
| `CALL-04-OBJECT-GETTERS` | CallableStatement getters: dates and times, getObject, getArray, getSQLXML | `jdbc/CallObjectGetters.java` (replaces the stub of CALL-00-SKELETON), `test: jdbc/CallObjectGettersTest.java` | CALL-02A-OUT, ARRAY-03-JDBC | 130 + 190 |  |
| `CALL-05-REFCURSOR` | Ref cursors: getObject of a refcursor column, CallableStatement results, PGRefCursorResultSet | `jdbc/RsRefCursor.java` (replaces the stub of FEAT-00B-STMT-RS-SKELETON), `PGRefCursorResultSet.java`, `test: jdbc/RefCursorTest.java`, `test: jdbc/RefCursorNameTest.java` | FEAT-00B-STMT-RS-SKELETON, RS-06-OBJECT-GETTERS, CALL-04-OBJECT-GETTERS, CONN-08-WRAPPER | 110 + 180 |  |
| `UPD-01-ANALYSIS` (P2) | Updatable result sets: when a result set is updatable (table and key from the result's metadata) | `jdbc/RsUpdatable.java` (replaces the stub of FEAT-00B-STMT-RS-SKELETON), `test: jdbc/RsUpdatableTest.java`, `test: jdbc/RsUpdatableShapesTest.java` | FEAT-00B-STMT-RS-SKELETON, RS-90-READONLY-UPDATES, CONN-07-FACTORIES, CONN-01B-EXEC, RS-04-NUMBER-GETTERS | 190 + 190 |  |
| `UPD-02-VALUES` (P2) | Updatable result sets: pending values, the insert row, cancelRowUpdates | `jdbc/RsUpdValues.java` (replaces the stub of FEAT-00B-STMT-RS-SKELETON), `test: jdbc/RsUpdValuesTest.java` | UPD-01-ANALYSIS, RS-01B-SCROLL, RS-03-TEXT-GETTERS | 170 + 190 |  |
| `UPD-03A-UPDATE-DELETE` (P2) | Updatable result sets: updateRow, deleteRow, refreshRow and the editable row store | `jdbc/EditableResultData.java`, `jdbc/RsUpdRows.java` (replaces the stub of FEAT-00B-STMT-RS-SKELETON), `test: jdbc/EditableResultDataTest.java`, `test: jdbc/RsUpdRowsTest.java` | UPD-02-VALUES, PSTMT-05-OBJECT-SETTERS, CONN-08-WRAPPER, RS-00B-DATA, RS-06-OBJECT-GETTERS | 230 + 210 |  |
| `UPD-03B-INSERT` (P2) | Updatable result sets: insertRow | `jdbc/RsUpdInsert.java` (replaces the stub of FEAT-00B-STMT-RS-SKELETON), `test: jdbc/RsUpdInsertTest.java` | UPD-03A-UPDATE-DELETE | 110 + 170 |  |
| `LOB-01A-OBJECT` | LargeObject over the SQL large-object functions | `largeobject/LargeObject.java`, `largeobject/BlobInputStream.java` (stub; owner LOB-02-STREAMS), `largeobject/BlobOutputStream.java` (stub; owner LOB-02-STREAMS), `test: largeobject/LargeObjectTest.java` | FEAT-00A-CONN-SKELETON, PSTMT-02-BASIC-SETTERS, RS-03-TEXT-GETTERS, RS-04-NUMBER-GETTERS, CONN-02-TXN | 230 + 190 |  |
| `LOB-01B-MANAGER` | LargeObjectManager and PGConnection.getLargeObjectAPI | `largeobject/LargeObjectManager.java` (replaces the stub of FEAT-00A-CONN-SKELETON), `jdbc/ConnLobs.java` (replaces the stub of FEAT-00A-CONN-SKELETON), `test: largeobject/LargeObjectManagerTest.java` | LOB-01A-OBJECT | 150 + 160 |  |
| `LOB-02-STREAMS` | Large-object streams: BlobInputStream and BlobOutputStream | `largeobject/BlobInputStream.java` (replaces the stub of LOB-01A-OBJECT), `largeobject/BlobOutputStream.java` (replaces the stub of LOB-01A-OBJECT), `test: largeobject/LargeObjectStreamsTest.java` | LOB-01B-MANAGER | 190 + 190 |  |
| `LOB-03A-BLOB` | java.sql.Blob over a large object: LobBase and PgBlob | `jdbc/LobBase.java`, `jdbc/PgBlob.java`, `test: jdbc/PgBlobTest.java` | LOB-02-STREAMS | 230 + 200 |  |
| `LOB-03B-CLOB` | java.sql.Clob over a large object: PgClob | `jdbc/PgClob.java`, `test: jdbc/PgClobTest.java` | LOB-03A-BLOB | 80 + 140 |  |
| `LOB-03C-JDBC` | Blob and Clob in result sets and prepared statements: getBlob, getClob, setBlob, setClob | `jdbc/RsLobs.java` (replaces the stub of FEAT-00B-STMT-RS-SKELETON), `jdbc/PsLobs.java` (replaces the stub of FEAT-00B-STMT-RS-SKELETON), `test: jdbc/LobJdbcTest.java` | LOB-03B-CLOB, FEAT-00B-STMT-RS-SKELETON, PSTMT-05-OBJECT-SETTERS, RS-06-OBJECT-GETTERS | 160 + 210 |  |
| `XML-01-FACTORY` (P2) | XML factories with secure defaults: PGXmlFactoryFactory, its two implementations, Connection.createSQLXML | `xml/PGXmlFactoryFactory.java`, `xml/DefaultPGXmlFactoryFactory.java`, `xml/LegacyInsecurePGXmlFactoryFactory.java`, `jdbc/ConnXml.java` (replaces the stub of FEAT-00A-CONN-SKELETON), `jdbc/PgSQLXML.java` (stub; owner XML-02A-SQLXML), `test: xml/XmlFactoryTest.java` | FEAT-00A-CONN-SKELETON, DRIVER-01-PROPERTIES, CONN-01A-OPEN-CLOSE | 250 + 160 |  |
| `XML-02A-SQLXML` (P2) | PgSQLXML: text and stream access, states | `jdbc/PgSQLXML.java` (replaces the stub of XML-01-FACTORY), `jdbc/XmlSources.java` (stub; owner XML-02B-SOURCES), `jdbc/XmlResults.java` (stub; owner XML-02C-RESULTS), `test: jdbc/PgSqlXmlTest.java` | XML-01-FACTORY, CONN-07-FACTORIES | 180 + 170 |  |
| `XML-02B-SOURCES` (P2) | SQLXML.getSource: DOM, SAX, StAX and stream sources | `jdbc/XmlSources.java` (replaces the stub of XML-02A-SQLXML), `test: jdbc/XmlSourcesTest.java` | XML-02A-SQLXML | 110 + 140 |  |
| `XML-02C-RESULTS` (P2) | SQLXML.setResult: DOM, SAX, StAX and stream results | `jdbc/XmlResults.java` (replaces the stub of XML-02A-SQLXML), `test: jdbc/XmlResultsTest.java` | XML-02A-SQLXML | 140 + 150 |  |
| `XML-03-JDBC` (P2) | SQLXML in result sets, parameters and callable statements: getSQLXML, setSQLXML, getObject | `jdbc/RsXml.java` (replaces the stub of FEAT-00B-STMT-RS-SKELETON), `jdbc/PsXml.java` (replaces the stub of FEAT-00B-STMT-RS-SKELETON), `test: jdbc/XmlJdbcTest.java` | XML-02A-SQLXML, FEAT-00B-STMT-RS-SKELETON, PSTMT-05-OBJECT-SETTERS, RS-06-OBJECT-GETTERS, CALL-04-OBJECT-GETTERS, LOB-03C-JDBC | 100 + 190 |  |
| `COPY-01A-IN` | COPY: the connection's copy state, CopyIn and its operation class | `copy/CopyIn.java`, `copy/CopyOut.java`, `jdbc/ConnCopyState.java` (replaces the stub of FEAT-00A-CONN-SKELETON), `jdbc/CopyOp.java`, `jdbc/CopyInOp.java`, `jdbc/CopyOutOp.java` (stub; owner COPY-01B-OUT), `test: jdbc/CopyInTest.java` | FEAT-00A-CONN-SKELETON, CONN-02-TXN, PQ-11-COPY, SQL-03-STATEMENTS, STMT-01C-QUERY-UPDATE, RS-03-TEXT-GETTERS | 270 + 190 |  |
| `COPY-01B-OUT` | COPY: CopyOut and its operation class | `jdbc/CopyOutOp.java` (replaces the stub of COPY-01A-IN), `test: jdbc/CopyOutTest.java` | COPY-01A-IN | 100 + 170 |  |
| `COPY-02-MANAGER` | CopyManager and ByteBufferByteStreamWriter | `copy/CopyManager.java` (replaces the stub of FEAT-00A-CONN-SKELETON), `util/ByteBufferByteStreamWriter.java`, `test: copy/CopyManagerTest.java` | COPY-01B-OUT | 150 + 200 |  |
| `COPY-03-STREAMS` | PGCopyInputStream and PGCopyOutputStream | `copy/PGCopyInputStream.java`, `copy/PGCopyOutputStream.java`, `test: copy/CopyStreamsTest.java` | COPY-02-MANAGER | 210 + 190 |  |
| `NOTIFY-01-API` | Notifications: PGConnection.getNotifications and the PGNotification implementation | `jdbc/ConnNotifications.java` (replaces the stub of FEAT-00A-CONN-SKELETON), `jdbc/NotificationImpl.java`, `test: jdbc/NotifyTest.java` | FEAT-00A-CONN-SKELETON, PQ-10-NOTIFY, CONN-02-TXN, STMT-01C-QUERY-UPDATE, COPY-01A-IN | 120 + 190 |  |
| `PGAPI-01-SURFACE` | The mirrored extension API: surface test against pgjdbc's public signatures | `test: MirroredApiTest.java`, `res: mirrored-api.txt` (new; generated list (R6 exemption)) | COPY-03-STREAMS, LOB-03C-JDBC, NOTIFY-01-API, POOL-03-DATASOURCE, CALL-05-REFCURSOR, CONN-08-WRAPPER, STMT-05-EXTENSIONS, RSMD-02-CATALOG, DRIVER-04D-DS-TUNING, DRIVER-04C-DS-TRANSPORT, DRIVER-04B-DS-HONOURED, PGOBJ-02C-PATHS, PGOBJ-01C-TEST, ERR-01-CLASSES, TYPE-99-DRAFT-REMOVE | 0 + 180 | 600 |
| `POOL-01-POOLED` | PGPooledConnection and the connection handle | `ds/PGPooledConnection.java`, `ds/ConnectionHandle.java`, `ds/StatementHandle.java` (stub; owner POOL-02-STATEMENTS), `test: ds/PooledConnectionTest.java` | DRIVER-04A-DATASOURCE, CONN-08-WRAPPER, STMT-02-RESULTS, PSTMT-01-STORE | 240 + 200 |  |
| `POOL-02-STATEMENTS` | Statement handles of a pooled connection | `ds/StatementHandle.java` (replaces the stub of POOL-01-POOLED), `test: ds/PooledStatementTest.java` | POOL-01-POOLED, PSTMT-02-BASIC-SETTERS, RS-01A-FORWARD | 110 + 150 |  |
| `POOL-03-DATASOURCE` | PGConnectionPoolDataSource | `ds/PGConnectionPoolDataSource.java`, `test: ds/ConnectionPoolDataSourceTest.java` | POOL-01-POOLED | 70 + 120 |  |
| `XA-01-XID` (P2) | XA: the Xid encoding (RecoveredXid) and PGXAException | `xa/RecoveredXid.java`, `xa/PGXAException.java`, `test: xa/RecoveredXidTest.java` | ERR-01-CLASSES, BUILD-02-RUNNER | 110 + 130 |  |
| `XA-02A-BRANCH` (P2) | XA: the branch state machine (start, end), the restricted handle, isSameRM, forget, timeouts | `xa/XaBranch.java`, `test: xa/XaBranchTest.java` | XA-01-XID, POOL-02-STATEMENTS, FEAT-00A-CONN-SKELETON | 200 + 200 |  |
| `XA-02B-COMPLETE` (P2) | XA: prepare, commit, rollback, recover (PGXAConnection) | `xa/PGXAConnection.java`, `test: xa/XaCompleteTest.java` | XA-02A-BRANCH | 200 + 220 |  |
| `XA-03-DATASOURCE` (P2) | PGXADataSource | `xa/PGXADataSource.java`, `test: xa/XaDataSourceTest.java` | XA-02B-COMPLETE | 50 + 110 |  |
| `CONTROL-01-API` | Cluster control: Stuntgres.dropCluster and nativeVersion, PGConnection.resetDatabase, dump, restore | `Stuntgres.java`, `jdbc/ConnControl.java` (replaces the stub of FEAT-00A-CONN-SKELETON), `test: ClusterControlTest.java`, `test: ClusterControlUnsupportedTest.java` | FEAT-00A-CONN-SKELETON, PQ-14A-CLUSTER, PQ-14B-EXT, TEST-02-NATIVE, CONN-02-TXN, RS-04-NUMBER-GETTERS, STMT-01C-QUERY-UPDATE, LIBSPQ-CABI-15-DUMP, LIBSPQ-CABI-04-PREPARED, LIBSPQ-CABI-05-ERRORS, LIBSPQ-CABI-12-CONNECT-ASYNC, LIBSPQ-CABI-14-CONTROL | 140 + 180 |  |
| `CONTROL-02-ISOLATION` | Isolation helper for consumers: IsolatedCluster | `IsolatedCluster.java`, `test: IsolatedClusterTest.java`, `test: IsolatedClusterNameTest.java` | CONTROL-01-API, DRIVER-03-DRIVER, LIBSPQ-CABI-15-DUMP, LIBSPQ-CABI-04-PREPARED, LIBSPQ-CABI-05-ERRORS, LIBSPQ-CABI-12-CONNECT-ASYNC, LIBSPQ-CABI-14-CONTROL | 90 + 140 |  |
| `DRIVER-05-PG-URL` | Opt-in acceptance of jdbc:postgresql: URLs | `core/DriverUrl.java` (edit), `test: core/PgUrlOptInTest.java`, `test: core/PgUrlProbe.java` | DRIVER-02-URL, DRIVER-03-DRIVER | 40 + 140 |  |
| `MAKE-01-JAR` | The jar's version check at first connect and the jar layout check | `jdbc/ConnVersionCheck.java` (replaces the stub of FEAT-00A-CONN-SKELETON), `tools/check_jar.sh`, `test: jdbc/VersionCheckTest.java` | FEAT-00A-CONN-SKELETON, PQ-14A-CLUSTER, DRIVER-03-DRIVER, CONN-01A-OPEN-CLOSE | 110 + 130 |  |
| `MAKE-02-TARGETS` | make jdbc and make jdbc-check: the root Makefile block and the jar smoke test | `Makefile` (root Makefile; edit: the .PHONY line and one appended block), `tools/JarSmoke.java` | MAKE-01-JAR, TEST-02-NATIVE, CONTROL-01-API, PQ-03B-EXTRACT, PSTMT-02-BASIC-SETTERS, RS-04-NUMBER-GETTERS, CONN-02-TXN, LIBSPQ-BUILD-01-SKELETON, CPYTHON-MAKE-02-TARGETS, LIBSPQ-CABI-04-PREPARED, LIBSPQ-CABI-05-ERRORS, LIBSPQ-CABI-12-CONNECT-ASYNC, LIBSPQ-CABI-14-CONTROL | 200 + 0 |  |
| `NATIVE-01-PQ` | Native verification of the pq layer (a stronger model is advisable) | none of its own; repairs in the family's driver files | the family of 15.1, TEST-02-NATIVE, LIBSPQ-CABI-04-PREPARED, LIBSPQ-CABI-05-ERRORS, LIBSPQ-CABI-06-ESCAPE, LIBSPQ-CABI-08-CANCEL, LIBSPQ-CABI-09-COPY, LIBSPQ-CABI-10-NOTIFY, LIBSPQ-CABI-12-CONNECT-ASYNC, LIBSPQ-CABI-13-ENCODING, LIBSPQ-CABI-14-CONTROL | 20 + 0 |  |
| `NATIVE-02-TYPES` | Native verification of type lookup, arrays and the conversion round trips (a stronger model is advisable) | none of its own; repairs in the family's driver files | the family of 15.1, TEST-02-NATIVE, LIBSPQ-CABI-04-PREPARED, LIBSPQ-CABI-05-ERRORS, LIBSPQ-CABI-06-ESCAPE, LIBSPQ-CABI-12-CONNECT-ASYNC, LIBSPQ-CABI-14-CONTROL | 20 + 0 |  |
| `NATIVE-03-CONN` | Native verification of driver, data source, errors, connection and transactions (a stronger model is advisable) | none of its own; repairs in the family's driver files | the family of 15.1, TEST-02-NATIVE, LIBSPQ-CABI-04-PREPARED, LIBSPQ-CABI-05-ERRORS, LIBSPQ-CABI-06-ESCAPE, LIBSPQ-CABI-08-CANCEL, LIBSPQ-CABI-12-CONNECT-ASYNC, LIBSPQ-CABI-14-CONTROL | 20 + 0 |  |
| `NATIVE-04-STMT` | Native verification of statements, prepared statements, batches and generated keys (a stronger model is advisable) | none of its own; repairs in the family's driver files | the family of 15.1, TEST-02-NATIVE, LIBSPQ-CABI-04-PREPARED, LIBSPQ-CABI-05-ERRORS, LIBSPQ-CABI-06-ESCAPE, LIBSPQ-CABI-08-CANCEL, LIBSPQ-CABI-09-COPY, LIBSPQ-CABI-12-CONNECT-ASYNC, LIBSPQ-CABI-14-CONTROL | 20 + 0 |  |
| `NATIVE-05-RS` | Native verification of result sets and their metadata (a stronger model is advisable) | none of its own; repairs in the family's driver files | the family of 15.1, TEST-02-NATIVE, LIBSPQ-CABI-04-PREPARED, LIBSPQ-CABI-05-ERRORS, LIBSPQ-CABI-12-CONNECT-ASYNC, LIBSPQ-CABI-14-CONTROL | 20 + 0 |  |
| `NATIVE-06-DBMD` | Native verification of DatabaseMetaData (a stronger model is advisable) | none of its own; repairs in the family's driver files | the family of 15.1, TEST-02-NATIVE, LIBSPQ-CABI-04-PREPARED, LIBSPQ-CABI-05-ERRORS, LIBSPQ-CABI-12-CONNECT-ASYNC, LIBSPQ-CABI-14-CONTROL | 20 + 0 |  |
| `NATIVE-07-CALL` | Native verification of callable statements and ref cursors (a stronger model is advisable) | none of its own; repairs in the family's driver files | the family of 15.1, TEST-02-NATIVE, LIBSPQ-CABI-04-PREPARED, LIBSPQ-CABI-05-ERRORS, LIBSPQ-CABI-06-ESCAPE, LIBSPQ-CABI-12-CONNECT-ASYNC, LIBSPQ-CABI-14-CONTROL | 20 + 0 |  |
| `NATIVE-08-UPD` (P2) | Native verification of updatable result sets (a stronger model is advisable) | none of its own; repairs in the family's driver files | the family of 15.1, TEST-02-NATIVE, LIBSPQ-CABI-04-PREPARED, LIBSPQ-CABI-05-ERRORS, LIBSPQ-CABI-06-ESCAPE, LIBSPQ-CABI-12-CONNECT-ASYNC, LIBSPQ-CABI-14-CONTROL | 20 + 0 |  |
| `NATIVE-09-LOB` | Native verification of large objects, Blob and Clob (a stronger model is advisable) | none of its own; repairs in the family's driver files | the family of 15.1, TEST-02-NATIVE, LIBSPQ-CABI-04-PREPARED, LIBSPQ-CABI-05-ERRORS, LIBSPQ-CABI-12-CONNECT-ASYNC, LIBSPQ-CABI-14-CONTROL | 20 + 0 |  |
| `NATIVE-10-XML` (P2) | Native verification of SQLXML (a stronger model is advisable) | none of its own; repairs in the family's driver files | the family of 15.1, TEST-02-NATIVE, LIBSPQ-CABI-04-PREPARED, LIBSPQ-CABI-05-ERRORS, LIBSPQ-CABI-12-CONNECT-ASYNC, LIBSPQ-CABI-14-CONTROL | 20 + 0 |  |
| `NATIVE-11-COPY-NOTIFY` | Native verification of COPY and notifications (a stronger model is advisable) | none of its own; repairs in the family's driver files | the family of 15.1, TEST-02-NATIVE, LIBSPQ-CABI-04-PREPARED, LIBSPQ-CABI-05-ERRORS, LIBSPQ-CABI-08-CANCEL, LIBSPQ-CABI-09-COPY, LIBSPQ-CABI-10-NOTIFY, LIBSPQ-CABI-12-CONNECT-ASYNC, LIBSPQ-CABI-14-CONTROL | 20 + 0 |  |
| `NATIVE-12-POOL` | Native verification of pooled connections (a stronger model is advisable) | none of its own; repairs in the family's driver files | the family of 15.1, TEST-02-NATIVE, LIBSPQ-CABI-04-PREPARED, LIBSPQ-CABI-05-ERRORS, LIBSPQ-CABI-08-CANCEL, LIBSPQ-CABI-12-CONNECT-ASYNC, LIBSPQ-CABI-14-CONTROL | 20 + 0 |  |
| `NATIVE-13-XA` (P2) | Native verification of XA connections (a stronger model is advisable) | none of its own; repairs in the family's driver files | the family of 15.1, TEST-02-NATIVE, LIBSPQ-CABI-04-PREPARED, LIBSPQ-CABI-05-ERRORS, LIBSPQ-CABI-06-ESCAPE, LIBSPQ-CABI-12-CONNECT-ASYNC, LIBSPQ-CABI-14-CONTROL | 20 + 0 |  |
| `NATIVE-99-FINAL` | Native verification of the whole suite and the jar on JDK 17 and JDK 21, the gate cases against the real libspq (a stronger model is advisable) | none of its own; repairs in the family's driver files | NATIVE-01 to NATIVE-13, MAKE-02-TARGETS, CONTROL-02-ISOLATION, PGAPI-01-SURFACE, DRIVER-05-PG-URL, LIBSPQ-DOCS-01-FINAL | 20 + 0 |  |
| `FETCH-01-PORTAL` (P3) | Fetch in slices: setFetchSize effective through the SPQX portal functions (needs a stronger model) | `jdbc/StmtFeatures.java` (replaces the stub of STMT-00-SKELETON), `test: jdbc/FetchSliceTest.java` | NATIVE-99-FINAL, STMT-03-LIMITS, RS-01A-FORWARD, LIBSPQ-EXT-01-PORTAL | 300 + 250 |  |
| `DOCS-01-FINAL` | Record the JDBC driver in the repository documentation | `README.md` (rewrite; owner JDBC-BUILD-01-SKELETON, which is a prerequisite), `docs/JDBC.md` (edit), `AGENTS.md` (edit), `docs/DEVELOPMENT.md` (edit), `docs/TESTING.md` (edit), `docs/LIBSPQ.md` (edit: the Java paragraph of section 8) | every JDBC implementation part except the `JDBC-ENGINE` parts and FETCH-01-PORTAL; LIBSPQ-DOCS-01-FINAL, CPYTHON-DOCS-01-FINAL, LIBSPQ-PGSESSION-19-DOCS, CLEANUP-DOC-STALE-CONTRACTS, GRIND-INFRA-DOCKER-IMAGE | 160 + 0 |  |
| **58 parts** | | | | **6950 + 7230** | **1380** |

Changes against the list of the part record: two skeleton parts (`FEAT-00A`, `FEAT-00B`)
and `CALL-00` are new (1.3); `LOB-01` is split into the object and the manager, `LOB-03`
into Blob, Clob and the JDBC entry points, `COPY-01` into the two directions, `XML-02`
and `XA-02` in parts, for the size limit; `PGAPI-01` is the surface test, because CORE
already implements the `PGConnection` and `PGStatement` methods the record listed; the
native family has 13 parts and the final one.

### 17.2 Rules common to the parts

- **CORE 13.2 applies**: tags (`@UnitTest`, `@DatabaseTest`, `@ReferenceToo`, `@Modes`,
  `@NativeOnly`), expected values by R11, `JdbcAsserts.assertSqlState`, the order inside
  a chain, the modes to pass before merge.
- **Tests of `java.sql` behavior** (callable statements, updatable result sets, `Blob`
  and `Clob` through result sets and parameters, SQLXML) run in reference mode too. Tests
  of the mirrored API and of package-private classes (large objects, COPY, notifications,
  pooled and XA classes) run in oracle, server and native mode; their expectations are
  recorded once from pgjdbc 42.7.13 with a throwaway program under `.cswd/tmp/<PART-ID>/`
  (R11), using the jar of the test class path.
- **Memory cap.** Every test run of a part whose tests send SQL to the stuntgres server
  (server mode) or run the engine in the test JVM (native mode) is started as

  ```
  systemd-run --user --scope -p MemoryMax=16G -p MemorySwapMax=0 timeout 900 make -C bindings/jdbc check MODE=<mode> [TEST=<Class>[,<Class>]]
  ```

  from the repository root; the parts use the same wrapper for every mode. A run ended
  by the cap or the timeout is a stop condition (R8) and is never retried without the
  cap.
- **"As pgjdbc shows" in a section** means: the rule is a starting point; the part
  records the behavior, follows pgjdbc, and lists a difference from the rule in its
  commit body. A changed name, file, signature or scope is not covered by this: R1 and
  R8 apply.
- **Server-mode differences** are engine gaps (R8): the part stops with the smallest
  statement and both answers; it does not work around the gap.
- **Native-only parts** (CONTROL-01, CONTROL-02, MAKE-02, the NATIVE parts) replace the
  reference, oracle and server runs of their own tests by the native run; the rest of
  the suite must still pass in the four other modes.

## 18. Experiments and audit

### 18.1 What was not run

Nothing. The project owner's directive "No experiments at all" (2026-11-11) replaced the
twelve experiments of the part record: no pgjdbc program, no database, no server, no
build. What the record wanted observed is left to the parts:

| Experiment of the record | Where it is established now |
|---|---|
| 1 rejected methods | CALL-01 (`CallRejectedTest`), LOB-03B, PGAPI-01; the verdicts of section 2 come from pgjdbc's source |
| 2 call syntaxes and modes on both servers | CALL-02A, CALL-02B |
| 3 updatable result sets | UPD-01 to UPD-03B |
| 4 large-object functions | LOB-01A to LOB-03C (the engine has the functions by name; read, not run) |
| 5 COPY state machine | COPY-01A, COPY-01B |
| 6 notifications | NOTIFY-01 |
| 7 pooled connections and XA | POOL-01 to XA-03 |
| 8 SQLXML | XML-01 to XML-03 |
| 9 jar, compression, loading | NATIVE 8.3 measured the jar with a 94.7 MB library; MAKE-02 (`JarSmoke`) |
| 10 Makefile block | MAKE-02 (`make -n`) |
| 11 family mapping | 15.1, derived by reading; the audit checks that every LIBSPQ part named exists |
| 12 final audit | 18.2 |

### 18.2 Final audit

Done by script over the records as the catalog utility's `select` returns them
(`.cswd/tmp/JDBC-DESIGN-FEATURES/parts/audit.py` for this design's 58 parts,
`audit_all.py` for all 214 JDBC parts: 5 design parts, 204 implementation parts and 5
`JDBC-ENGINE` parts).

| Check | Result |
|---|---|
| Prerequisite graph | acyclic; every prerequisite exists |
| An implementation part cannot start before its design | every part lists a `JDBC-DESIGN` part, except JDBC-ENGINE-01-IMPORTED-KEYS-RUNAWAY (added by the coordinating session; reported) |
| Rules block at the start of `work` | 175 parts carry R1 to R11, the 29 foundation parts of JDBC-DESIGN-NATIVE carry R1 to R10 (not rewritten); four of the five ENGINE parts carry R1 to R11, ENGINE-01 has no rules block (reported) |
| Scope markers `Files:`, `Estimated size:`, `Out of scope:` | present in all 209 implementation parts |
| File ownership | 572 distinct files; 169 are named by more than one part (stub and owner, an edit with its owner, a native part and the owners of its family); every such pair has an ordering path. The `Files:` text of the five ENGINE parts names no file ("determined by the implementer") and could not be checked |
| Inventories | every verdict of CORE section 2 and METADATA section 2 names an existing part; the 15 methods CORE deferred have owners here; the 199 methods of section 2.1 have one verdict each |
| Mirrored public names | each public type is a file with one creating part (the ownership check) |
| LIBSPQ prerequisites | 21 parts: TEST-02-NATIVE, PQ-14B-EXT, CONTROL-01, CONTROL-02, MAKE-02, the 14 native parts, FETCH-01, and DOCS-01 (two ordering edges for shared documents, not a run-time need). No other part has one; every part with native-only tests has them |
| Files outside `bindings/jdbc` | the root `Makefile`: MAKE-02 only, ordered after LIBSPQ-BUILD-01-SKELETON and CPYTHON-MAKE-02-TARGETS. `AGENTS.md`, `docs/DEVELOPMENT.md`, `docs/TESTING.md`, `docs/LIBSPQ.md`, `docs/JDBC.md`: DOCS-01 only, ordered after the five parts of section 14. Left without an edge on purpose: LIBSPQ-EXT-01-PORTAL and LIBSPQ-EXT-02-NOTICE-QUEUE (P3; section 14). No JDBC part edits `.gitignore` |

Repairs made in this design's own batch after the first audit pass: the native parts
01, 03, 06 and 09 listed stub files of other owners among the files they may repair
(removed); DOCS-01 got ordering edges to CLEANUP-DOC-STALE-CONTRACTS and
GRIND-INFRA-DOCKER-IMAGE; two classes were renamed before insertion because
`org.stuntgres.pq` already has `ConnCopy` and `ConnNotify`. Left to the coordinating
session: the two points about JDBC-ENGINE-01 above, and a hook in `ConnOwnSql.execOwn`
and `queryOwn` (CORE 5.1, part CONN-01B, still pending) that would let `ConnCopyState`
refuse the driver's own statements during a copy (decision 9).
