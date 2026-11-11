# JDBC binding: DatabaseMetaData

Design of `java.sql.DatabaseMetaData` for the stuntgres JDBC driver in `bindings/jdbc`.
It builds on `docs/design/JDBC-NATIVE.md` (build, `org.stuntgres.pq`, test harness),
`docs/design/JDBC-TYPES.md` (type table) and `docs/design/JDBC-CORE.md` (connection,
statements, result sets). Part: `JDBC-DESIGN-METADATA`.

This document is a specification, not source code. It gives files, the class chain,
signatures, behavior rules, and for every method the JDBC columns, the argument rules, the
row order, the catalogs an implementation reads and the place in pgjdbc where the
behavior can be studied. It gives **no SQL text**: every implementer writes the
statements of his part himself (clean room, rule R3) and proves them by running the same
test body in reference mode (pgjdbc 42.7.13 against PostgreSQL 18.6) and against this
driver (rule R11).

Marks: **(observed)** is a value seen in the recorded output of the interrupted probe run
(section 9); such values are starting points, not expectations: the fixture of section 4
differs slightly from the probe's. **(map)** is taken from
`.cswd/tmp/JDBC/research/pgjdbc-map.md` section 8 and is to be confirmed in reference
mode. pgjdbc line numbers refer to
`../src-ref/pgjdbc/pgjdbc/src/main/java/org/postgresql/jdbc/PgDatabaseMetaData.java`
(tag REL42.7.13), called `PgDMD` below.

## 1. Purpose, layering, decisions, layout

### 1.1 Layering

`Connection.getMetaData()` returns `org.stuntgres.jdbc.PgDatabaseMetaData`. The class
uses only what JDBC-CORE offers:

- ordinary prepared statements of the connection for its catalog queries
  (`connection.prepareStatement`, JDBC-CORE 5.5), so that a metadata call behaves like any
  statement of the application (implicit `BEGIN` outside autocommit, `25P02` in a failed
  transaction), as in pgjdbc;
- `MemoryResultData` and `PgResultSet` (JDBC-CORE 10.1) for result sets built in memory;
- `TypeTable` and the connection's `TypeLookup` (JDBC-TYPES 5.1 to 5.3, JDBC-CORE 11.3) for
  JDBC type codes, type names, precision and scale;
- `Errors` (JDBC-CORE 4.4) for client-side errors.

It adds no native code, no dependency and no edit of `pom.xml` or `Makefile`.

### 1.2 Decisions

1. **Answers are pgjdbc's** (plan J1): values of constant methods, columns (label and JDBC
   type code, position by position), rows and row order of result sets. The SQL is this
   project's own (plan J7).
2. **No SQL in this document, no recorded row tables.** The owner waived the literal
   statements, the recorded rows and all experiments of the part record. Expected values
   are established by each implementing part in reference mode and written into its test
   or its expectation file (4.3).
3. **Class chain** as JDBC-CORE 1.3: a generated root that rejects every method with
   `0A000`, stub classes created by one skeleton part, one chain class per method group
   and part (1.3). Five methods stay rejected for good because pgjdbc rejects them
   **(observed)**: `getSuperTypes`, `getSuperTables`, `getAttributes`,
   `getPseudoColumns`, `getRowIdLifetime`.
4. **Catalog queries run as ordinary prepared statements** (`DbmdCore.query`, 1.5), never
   through `ConnOwnSql.queryOwn`: pgjdbc's metadata statements take part in the
   connection's transaction, and so do these.
5. **Patterns are bound as parameters**, never pasted into the statement text, also where
   pgjdbc pastes them (`getUDTs`).
6. **One metadata object per connection**, created on the first `getMetaData()` call and
   returned again afterwards (pgjdbc `PgConnection.getMetaData`).
7. **In-memory result sets have no statement**: `getStatement()` of such a result is null
   (as for `Array.getResultSet`, JDBC-CORE 11.4). pgjdbc attaches a statement; no test
   asserts either.
8. **Driver identity is this driver's own**: `getDriverName()` is `stuntgres JDBC Driver`
   (pgjdbc: `PostgreSQL JDBC Driver`), the version is the binding's version, the URL is
   the `jdbc:stuntgres:` URL. Tests of these five answers cannot run in reference mode
   (3.2).
9. **`hideUnprivilegedObjects`** (JDBC-CORE 3.3: stored) is honoured by the six methods in
   which pgjdbc honours it: `getSchemas`, `getTables`, `getProcedures`, `getFunctions`,
   `getTypeInfo`, `getUDTs` (`PgDMD` 1676, 1529, 1236, 3328, 2747, 3165). Its verdict in
   the property table becomes "honoured (JDBC-METADATA 5.1 rule 10)". No other connection
   property changes metadata.
10. **Own SQL keeps to what both servers answered alike in the recorded probes** (7.3):
    `pg_catalog` tables and functions only, explicit `JOIN ... ON`, no
    `information_schema._pg_expandarray`, no `pg_my_temp_schema()`.
11. **`getClientInfoProperties`' DESCRIPTION** is this driver's own sentence (clean room);
    the other three cells are pgjdbc's.
12. **Engine gaps do not block this family.** The gaps found (section 7) concern
    statements pgjdbc itself sends and two DDL forms; no part of this family needs them,
    so the engine parts are priority P2 and nobody's prerequisite.
13. **Memory cap on every test run of this family** (8.2): a catalog statement once made
    the stuntgres server grow without bound.

### 1.3 Class chain

```
DatabaseMetaDataRoot               generated                                        DBMD-00
└ DbmdCore        connection, query and memory helpers, identification, wrapper     DBMD-01
  └ DbmdSqlFeatures     SQL grammar answers                                         DBMD-02A
    └ DbmdTxnFeatures     transactions, cursors, result sets, statements            DBMD-02B
      └ DbmdLimits          limits, default isolation level                         DBMD-03
        └ DbmdIdentifiers     identifier rules, terms, schema and catalog use       DBMD-04
          └ DbmdKeywords        keywords, function lists                            DBMD-05
            └ DbmdSchemas         getSchemas, getCatalogs, getTableTypes            DBMD-11
              └ DbmdTables          getTables                                       DBMD-12
                └ DbmdColumns         getColumns                                    DBMD-13
                  └ DbmdKeys            getPrimaryKeys, getBestRowIdentifier,
                    │                   getVersionColumns                           DBMD-14
                    └ DbmdForeignKeys   getImportedKeys, getExportedKeys,
                      │                 getCrossReference                           DBMD-15
                      └ DbmdIndexes       getIndexInfo                              DBMD-16
                        └ DbmdTypeInfo      getTypeInfo                             DBMD-17
                          └ DbmdProcedures    getProcedures                         DBMD-18A
                            └ DbmdProcedureColumns   getProcedureColumns            DBMD-18B
                              └ DbmdFunctions      getFunctions                     DBMD-19A
                                └ DbmdFunctionColumns    getFunctionColumns         DBMD-19B
                                  └ DbmdTablePrivileges    getTablePrivileges       DBMD-20A
                                    └ DbmdColumnPrivileges   getColumnPrivileges    DBMD-20B
                                      └ DbmdUdt      getUDTs, getClientInfoProperties   DBMD-21
                                        └ PgDatabaseMetaData (public final;
                                          implements DatabaseMetaData)              DBMD-00
```

`jdbc/ConnMetaData.java` (the extension stub of JDBC-CORE 1.3) is replaced by DBMD-00:

```java
abstract class ConnMetaData extends ConnExtensions
    ConnMetaData(Properties info, String url) throws SQLException;
    public DatabaseMetaData getMetaData() throws SQLException;
```

`getMetaData()` runs under the connection's lock: `checkOpen()` (`08003` when closed),
then returns the connection's `PgDatabaseMetaData`, creating it on the first call with
`new PgDatabaseMetaData((PgConnection) this)` (the cast is the one `ConnFactories` uses to
hand the connection to a statement).

The chain order is fixed. A chain class calls only the members of `DbmdCore` listed in 1.5
and, where a later section says so, a member of a class above it; all other parts are
independent of each other, which is why most parts need only DBMD-00 or DBMD-01.

### 1.4 Files and owner parts

The table of 8.1 lists every file with its owner. Notes in its Files column are those of
JDBC-CORE 1.4 ("stub; owner X", "replaces the stub of X", "generated"). Paths without a
directory prefix are below `src/main/java/org/stuntgres/`, those starting with `test:`
below `src/test/java/org/stuntgres/`, those starting with `res:` below
`src/test/resources/org/stuntgres/`.

| Where | Files |
|---|---|
| `org.stuntgres.jdbc` | `DatabaseMetaDataRoot` (generated), the 20 chain classes of 1.3, `PgDatabaseMetaData`, `ConnMetaData` |
| test support (`org.stuntgres.testing`) | `MetaFixture`, `MetaRows` (section 4) |
| test resources | `testing/dbmd-fixture.sql` (4.1); one expectation file per result-set test class under `jdbc/dbmd/` (4.3) |
| tests (`org.stuntgres.jdbc`) | one or two test classes per part, named in 8.1 |

### 1.5 Rules for every class of this layer

1. **JDBC-CORE 1.5 applies** unchanged: lock (every public method of the metadata object
   that reaches the connection runs under `connection.lock`; a method that returns a
   literal needs no lock), generated root, constructors, visibility, errors, JDK modules.
2. **Root.** `DatabaseMetaDataRoot` is generated by DBMD-00 as JDBC-CORE 1.5 rule 2 says,
   from the `javap` output of `java.sql.DatabaseMetaData` on JDK 17
   (`.cswd/tmp/JDBC-DESIGN-METADATA/inv/javap.txt` is that output): the 174 abstract
   methods, `unwrap` and `isWrapperFor`, and the default method `supportsRefCursors`
   (section 2 marks the other two default methods "JDK default"). Constructor
   `DatabaseMetaDataRoot()`.
3. **Constructors** of the chain classes: `(PgConnection connection)`, passed up;
   `PgDatabaseMetaData`'s is package-private. A stub is the class declaration, the
   constructor and a one-line comment naming the owner part.
4. **`DbmdCore`** (DBMD-01) is the only class that other chain classes call into:

```java
abstract class DbmdCore extends DatabaseMetaDataRoot           // jdbc/DbmdCore.java
    DbmdCore(PgConnection connection);
    final PgConnection connection;
    final ResultSet query(String sql, String... parameters) throws SQLException;
    final ResultSet memory(String[] names, int[] typeOids, List<String[]> rows) throws SQLException;
    final boolean otherCatalog(String catalog) throws SQLException;
    final boolean hideUnprivileged();
    final int maxNameLength() throws SQLException;
    final int maxIndexKeys() throws SQLException;
```

   - `query`: `connection.prepareStatement(sql, ResultSet.TYPE_SCROLL_INSENSITIVE,
     ResultSet.CONCUR_READ_ONLY)`, each parameter bound with `setString` (a null parameter
     with `setNull(i, Types.VARCHAR)`), `closeOnCompletion()`, then `executeQuery()`. The
     statement text uses `?` placeholders and casts a parameter where the server could
     not infer its type. On an exception the statement is closed before the exception
     leaves. pgjdbc: `createMetaDataStatement` 3179, `executeMetadataStatement` 3186.
   - `memory`: section 6.
   - `otherCatalog(catalog)`: true when `catalog` is not null and differs from
     `connection.getCatalog()` (so also for the empty string). The caller then returns
     the empty result of 5.1 rule 1.
   - `hideUnprivileged()`: the boolean value of the connection property
     `hideUnprivilegedObjects` (`PGProperty.HIDE_UNPRIVILEGED_OBJECTS`, JDBC-CORE 3.4), read
     once in the constructor.
   - `maxNameLength()`, `maxIndexKeys()`: computed from the server on first use and kept
     for the life of the metadata object (3.4).
5. **SQL of a group lives in that group's file** as `private static final String`
   constants or is assembled there; no statement text is shared between files.
6. **A method that pgjdbc builds in memory** (5.1 rule 11 names them) may be built either
   way here, as long as labels, type codes, rows and order are pgjdbc's.

## 2. Method inventory

Every method of `java.sql.DatabaseMetaData` in Java 17: 174 abstract methods, three
default methods, and the two it inherits from `java.sql.Wrapper` (179 rows). Generated
from `.cswd/tmp/JDBC-DESIGN-METADATA/inv/methods.txt` by `parts/inv.py`; the audit checks
that every method has exactly one row and every owner is a part. Owners are part IDs
without the `JDBC-` prefix. Kinds:

- **constant**: a literal in the chain class, pgjdbc's value (3.1);
- **computed**: read from the server or the connection (section 3 says how);
- **own**: this driver's own answer, not pgjdbc's (3.2);
- **result set**: section 5;
- **rejected**: stays in the generated root and raises `SQLFeatureNotSupportedException`
  `0A000`, as pgjdbc does **(observed)**;
- **JDK default**: the interface's default body is kept; it gives pgjdbc's answer
  **(observed: `getMaxLogicalLobSize()` 0, `supportsSharding()` false)**.

**Identification and wrapper** (16 methods)

| Owner | Kind | Methods |
|---|---|---|
| DBMD-01-BASE | own | `getURL()`, `getDriverName()`, `getDriverVersion()`, `getDriverMajorVersion()`, `getDriverMinorVersion()` |
| DBMD-01-BASE | computed | `getUserName()`, `isReadOnly()`, `getDatabaseProductName()`, `getDatabaseProductVersion()`, `getConnection()`, `getDatabaseMajorVersion()`, `getDatabaseMinorVersion()`, `unwrap(Class<T>)`, `isWrapperFor(Class<?>)` |
| DBMD-01-BASE | constant | `getJDBCMajorVersion()`, `getJDBCMinorVersion()` |

**SQL grammar features** (36 methods)

| Owner | Kind | Methods |
|---|---|---|
| DBMD-02A-SQL-FEATURES | constant | `nullsAreSortedHigh()`, `nullsAreSortedLow()`, `nullsAreSortedAtStart()`, `nullsAreSortedAtEnd()`, `supportsAlterTableWithAddColumn()`, `supportsAlterTableWithDropColumn()`, `supportsColumnAliasing()`, `nullPlusNonNullIsNull()`, `supportsConvert()`, `supportsConvert(int, int)`, `supportsTableCorrelationNames()`, `supportsDifferentTableCorrelationNames()`, `supportsExpressionsInOrderBy()`, `supportsOrderByUnrelated()`, `supportsGroupBy()`, `supportsGroupByUnrelated()`, `supportsGroupByBeyondSelect()`, `supportsLikeEscapeClause()`, `supportsNonNullableColumns()`, `supportsMinimumSQLGrammar()`, `supportsCoreSQLGrammar()`, `supportsExtendedSQLGrammar()`, `supportsANSI92EntryLevelSQL()`, `supportsANSI92IntermediateSQL()`, `supportsANSI92FullSQL()`, `supportsIntegrityEnhancementFacility()`, `supportsOuterJoins()`, `supportsFullOuterJoins()`, `supportsLimitedOuterJoins()`, `supportsSubqueriesInComparisons()`, `supportsSubqueriesInExists()`, `supportsSubqueriesInIns()`, `supportsSubqueriesInQuantifieds()`, `supportsCorrelatedSubqueries()`, `supportsUnion()`, `supportsUnionAll()` |

**Transactions, cursors, result sets, statements** (44 methods)

| Owner | Kind | Methods |
|---|---|---|
| DBMD-02B-TXN-FEATURES | constant | `allProceduresAreCallable()`, `allTablesAreSelectable()`, `usesLocalFiles()`, `usesLocalFilePerTable()`, `supportsMultipleResultSets()`, `supportsMultipleTransactions()`, `supportsPositionedDelete()`, `supportsPositionedUpdate()`, `supportsSelectForUpdate()`, `supportsStoredProcedures()`, `supportsOpenCursorsAcrossCommit()`, `supportsOpenCursorsAcrossRollback()`, `supportsOpenStatementsAcrossCommit()`, `supportsOpenStatementsAcrossRollback()`, `supportsTransactions()`, `supportsTransactionIsolationLevel(int)`, `supportsDataDefinitionAndDataManipulationTransactions()`, `supportsDataManipulationTransactionsOnly()`, `dataDefinitionCausesTransactionCommit()`, `dataDefinitionIgnoredInTransactions()`, `supportsResultSetType(int)`, `supportsResultSetConcurrency(int, int)`, `ownUpdatesAreVisible(int)`, `ownDeletesAreVisible(int)`, `ownInsertsAreVisible(int)`, `othersUpdatesAreVisible(int)`, `othersDeletesAreVisible(int)`, `othersInsertsAreVisible(int)`, `updatesAreDetected(int)`, `deletesAreDetected(int)`, `insertsAreDetected(int)`, `supportsBatchUpdates()`, `supportsSavepoints()`, `supportsNamedParameters()`, `supportsMultipleOpenResults()`, `supportsGetGeneratedKeys()`, `supportsResultSetHoldability(int)`, `getResultSetHoldability()`, `locatorsUpdateCopy()`, `supportsStatementPooling()`, `supportsStoredFunctionsUsingCallSyntax()`, `autoCommitFailureClosesAllResultSets()`, `generatedKeyAlwaysReturned()`, `supportsRefCursors()` |

**Limits** (22 methods)

| Owner | Kind | Methods |
|---|---|---|
| DBMD-03-LIMITS | constant | `getMaxBinaryLiteralLength()`, `getMaxCharLiteralLength()`, `getMaxColumnsInGroupBy()`, `getMaxColumnsInOrderBy()`, `getMaxColumnsInSelect()`, `getMaxColumnsInTable()`, `getMaxConnections()`, `getMaxIndexLength()`, `getMaxRowSize()`, `doesMaxRowSizeIncludeBlobs()`, `getMaxStatementLength()`, `getMaxStatements()`, `getMaxTablesInSelect()` |
| DBMD-03-LIMITS | computed | `getMaxColumnNameLength()`, `getMaxColumnsInIndex()`, `getMaxCursorNameLength()`, `getMaxSchemaNameLength()`, `getMaxProcedureNameLength()`, `getMaxCatalogNameLength()`, `getMaxTableNameLength()`, `getMaxUserNameLength()`, `getDefaultTransactionIsolation()` |

**Identifier rules, terms, schema and catalog use** (27 methods)

| Owner | Kind | Methods |
|---|---|---|
| DBMD-04-IDENTIFIERS | constant | `supportsMixedCaseIdentifiers()`, `storesUpperCaseIdentifiers()`, `storesLowerCaseIdentifiers()`, `storesMixedCaseIdentifiers()`, `supportsMixedCaseQuotedIdentifiers()`, `storesUpperCaseQuotedIdentifiers()`, `storesLowerCaseQuotedIdentifiers()`, `storesMixedCaseQuotedIdentifiers()`, `getIdentifierQuoteString()`, `getSearchStringEscape()`, `getExtraNameCharacters()`, `getSchemaTerm()`, `getProcedureTerm()`, `getCatalogTerm()`, `isCatalogAtStart()`, `getCatalogSeparator()`, `supportsSchemasInDataManipulation()`, `supportsSchemasInProcedureCalls()`, `supportsSchemasInTableDefinitions()`, `supportsSchemasInIndexDefinitions()`, `supportsSchemasInPrivilegeDefinitions()`, `supportsCatalogsInDataManipulation()`, `supportsCatalogsInProcedureCalls()`, `supportsCatalogsInTableDefinitions()`, `supportsCatalogsInIndexDefinitions()`, `supportsCatalogsInPrivilegeDefinitions()`, `getSQLStateType()` |

**Keywords and function lists** (5 methods)

| Owner | Kind | Methods |
|---|---|---|
| DBMD-05-KEYWORDS | computed | `getSQLKeywords()` |
| DBMD-05-KEYWORDS | constant | `getNumericFunctions()`, `getStringFunctions()`, `getSystemFunctions()`, `getTimeDateFunctions()` |

**Result sets** (22 methods)

| Owner | Kind | Methods |
|---|---|---|
| DBMD-18A-PROCEDURES | result set | `getProcedures(String, String, String)` |
| DBMD-18B-PROCEDURE-COLUMNS | result set | `getProcedureColumns(String, String, String, String)` |
| DBMD-12-TABLES | result set | `getTables(String, String, String, String[])` |
| DBMD-11-SCHEMAS | result set | `getSchemas()`, `getCatalogs()`, `getTableTypes()`, `getSchemas(String, String)` |
| DBMD-13-COLUMNS | result set | `getColumns(String, String, String, String)` |
| DBMD-20B-COLUMN-PRIVILEGES | result set | `getColumnPrivileges(String, String, String, String)` |
| DBMD-20A-TABLE-PRIVILEGES | result set | `getTablePrivileges(String, String, String)` |
| DBMD-14-PRIMARY-KEYS | result set | `getBestRowIdentifier(String, String, String, int, boolean)`, `getVersionColumns(String, String, String)`, `getPrimaryKeys(String, String, String)` |
| DBMD-15-FOREIGN-KEYS | result set | `getImportedKeys(String, String, String)`, `getExportedKeys(String, String, String)`, `getCrossReference(String, String, String, String, String, String)` |
| DBMD-17-TYPE-INFO | result set | `getTypeInfo()` |
| DBMD-16-INDEXES | result set | `getIndexInfo(String, String, String, boolean, boolean)` |
| DBMD-21-UDT | result set | `getUDTs(String, String, String, int[])`, `getClientInfoProperties()` |
| DBMD-19A-FUNCTIONS | result set | `getFunctions(String, String, String)` |
| DBMD-19B-FUNCTION-COLUMNS | result set | `getFunctionColumns(String, String, String, String)` |

**Rejected** (5 methods)

| Owner | Kind | Methods |
|---|---|---|
| rejected | rejected | `getSuperTypes(String, String, String)`, `getSuperTables(String, String, String)`, `getAttributes(String, String, String, String)`, `getRowIdLifetime()`, `getPseudoColumns(String, String, String, String)` |

**JDK default** (2 methods)

| Owner | Kind | Methods |
|---|---|---|
| JDK default | JDK default | `getMaxLogicalLobSize()`, `supportsSharding()` |

## 3. Constant and computed answers

### 3.1 The rule

- A constant answer is a literal in the method body. Its value is what pgjdbc 42.7.13
  returns against PostgreSQL 18.6. The owning part writes one test class tagged
  `@DatabaseTest @ReferenceToo` that calls **every** method of its group through
  `java.sql.DatabaseMetaData` and compares with a literal; the literal is established in
  reference mode (R11) and the same test then passes in oracle and server mode. The
  values marked (map) or (observed) below are the starting expectations.
- A method with `int` arguments (`supportsResultSetType`, `supportsResultSetConcurrency`,
  `supportsResultSetHoldability`, `supportsTransactionIsolationLevel`, `supportsConvert(int,
  int)`, the nine visibility and detection methods) is tested with every constant JDBC
  defines for the argument and with one undefined value (`-1`).
- No constant method reaches the server; it works on a closed connection as far as pgjdbc's
  does (the implementer checks one constant and one computed method on a closed
  connection in reference mode and follows pgjdbc).
- A computed answer is tested the same way where its value is the same in all modes. Where
  the value depends on the server instance, the test compares with a value it obtains
  independently through a statement of its own (named below).

### 3.2 Identification (`DbmdCore`, DBMD-01)

| Method | Kind | Answer |
|---|---|---|
| `getDriverName()` | own | `stuntgres JDBC Driver` (pgjdbc: `PostgreSQL JDBC Driver` **(observed)**) |
| `getDriverVersion()` | own | the text `version` of the resource `/org/stuntgres/version.properties` (JDBC-NATIVE, BUILD-01), read once; pgjdbc: `42.7.13` |
| `getDriverMajorVersion()`, `getDriverMinorVersion()` | own | first and second number of that text (the rule of `Driver.getMajorVersion`, JDBC-CORE 3.5) |
| `getURL()` | own | `connection.url`, the URL the connection was opened with (pgjdbc returns its own URL the same way **(observed)**) |
| `getJDBCMajorVersion()`, `getJDBCMinorVersion()` | constant | 4 and 2 **(observed)** |
| `getDatabaseProductName()` | computed | `PostgreSQL` in every mode **(observed)**; a literal |
| `getDatabaseProductVersion()` | computed | the session's `server_version` (`connection.pq().parameterStatus("server_version")`); **(observed: `18.6 (Debian 18.6-1.pgdg12+2)`)**. Test: equals the result of `SHOW server_version` |
| `getDatabaseMajorVersion()`, `getDatabaseMinorVersion()` | computed | from `connection.pq().serverVersion()` (JDBC-NATIVE 9.5; for example 180006): the number divided by 10000 and its remainder: 18 and 6 on PostgreSQL **(observed)**. The stuntgres server reports `server_version` `18.0` and 180000 (JDBC-NATIVE 10.5, measured; docs/LIBSPQ.md 5.2), which is accepted: the test asserts major 18 and compares the minor number with `SHOW server_version_num` modulo 10000 |
| `getUserName()` | computed | the user of the session as pgjdbc reports it for the same connection: the `user` the connection was opened with (JDBC-CORE 3.2). Test: equals `TestDatabase.user()` |
| `isReadOnly()` | computed | `connection.isReadOnly()` **(observed: false)** |
| `getConnection()` | computed | the `PgConnection` |
| `unwrap(Class)`, `isWrapperFor(Class)` | computed | the rule of JDBC-CORE 5.6: the object itself for an interface or class it is an instance of, else `Errors` as that section says |

The five "own" answers and `getURL` are tested by a class tagged `@DatabaseTest
@Modes({Mode.ORACLE, Mode.SERVER, Mode.NATIVE})` with literals of this driver; everything
else of this group is tested in a `@ReferenceToo` class.

### 3.3 Features (`DbmdSqlFeatures` DBMD-02A, `DbmdTxnFeatures` DBMD-02B)

All constant. Starting values **(map)**; pgjdbc: `PgDMD` 136 to 1170.

| Area | Answers |
|---|---|
| Nulls | `nullsAreSortedHigh` true, the other three false; `nullPlusNonNullIsNull` true |
| Grammar | `supportsMinimumSQLGrammar` true; core and extended false; `supportsConvert()` and `supportsConvert(int, int)` false; `supportsDifferentTableCorrelationNames` false; the ANSI-92, outer-join, subquery, union, group-by and order-by answers as pgjdbc shows |
| Cursors, positioning | `supportsOpenCursorsAcrossCommit` / `Rollback` false; `supportsOpenStatementsAcrossCommit` / `Rollback` true; positioned update and delete false |
| Transactions | transactions, savepoints, batch updates, stored procedures, stored functions using call syntax, `supportsRefCursors`: true; `supportsTransactionIsolationLevel` true for the four standard levels, false for `TRANSACTION_NONE` and unknown values; DDL and DML in transactions true, DDL commit or ignore false |
| Statements | `supportsNamedParameters` false; `supportsMultipleOpenResults` false; `supportsGetGeneratedKeys` true; `generatedKeyAlwaysReturned` true; `supportsStatementPooling` false |
| Result sets | `supportsResultSetType`, `supportsResultSetConcurrency`, `supportsResultSetHoldability`: as pgjdbc answers per constant; `getResultSetHoldability()` is 1 (`HOLD_CURSORS_OVER_COMMIT`) **(observed)**, although `Connection.getHoldability()` is 2 (JDBC-CORE 5.4) |
| Visibility | own updates, deletes and inserts visible: as pgjdbc answers per result-set type; others' not; nothing detected |

`supportsResultSetConcurrency(type, CONCUR_UPDATABLE)` keeps pgjdbc's answer even though
updatable result sets arrive only with JDBC-DESIGN-FEATURES: the answer describes the
driver family, and FEATURES implements them before the binding is released.

### 3.4 Limits (`DbmdLimits`, DBMD-03)

| Method | Kind | Answer |
|---|---|---|
| the seven name lengths (`getMaxColumnNameLength`, `...CursorName...`, `...SchemaName...`, `...ProcedureName...`, `...CatalogName...`, `...TableName...`, `...UserName...`) | computed | `maxNameLength()`: the length of type `name` minus one, read from `pg_type` (`typlen` of `pg_catalog.name`); 63 **(observed)**. pgjdbc: `getMaxNameLength` 87 |
| `getMaxColumnsInIndex()` | computed | `maxIndexKeys()`: the setting `max_index_keys`; 32 **(observed)**. pgjdbc: `getMaxIndexKeys` 62 |
| `getDefaultTransactionIsolation()` | computed | the session's `default_transaction_isolation` mapped to the JDBC constant; 2 (`TRANSACTION_READ_COMMITTED`) **(observed)**. pgjdbc: 1072. Test also after `SET default_transaction_isolation` to each of the four levels |
| `getMaxColumnsInTable()`, `getMaxConnections()`, `getMaxRowSize()`, `getMaxIndexLength()`, `getMaxStatementLength()` and the remaining `getMax...` methods | constant | 1600, 8192, 1073741824, 0, 0, the rest 0 or as pgjdbc shows **(map; `getMaxIndexLength` 0 observed)** |
| `doesMaxRowSizeIncludeBlobs()` | constant | as pgjdbc shows |

The probe showed the same `typlen`, `max_index_keys` and
`default_transaction_isolation` on both servers (probe block B21).

### 3.5 Identifier rules and terms (`DbmdIdentifiers`, DBMD-04)

All constant **(map)**: identifiers are stored in lower case
(`storesLowerCaseIdentifiers` true, the other `stores...` answers as pgjdbc shows);
`supportsMixedCaseQuotedIdentifiers` true; quote string `"`; search-string escape `\`
**(observed)**; `getExtraNameCharacters` as pgjdbc shows; terms `schema`, `function`,
`database`; `isCatalogAtStart` true and separator `.` **(observed)**; schemas supported
in the five statement kinds, catalogs in none; `getSQLStateType()` 2 (`sqlStateSQL`)
**(observed)**.

### 3.6 Keywords and function lists (`DbmdKeywords`, DBMD-05)

- `getSQLKeywords()` (computed; pgjdbc 288): the words of `pg_catalog.pg_get_keywords()`
  that are not SQL:2003 keywords, in lower case, ascending, joined by commas; computed on
  first use and kept. The SQL:2003 list to subtract is a set the implementer writes from
  the standard's keyword list (ISO/IEC 9075-2:2003, 5.2; reserved and non-reserved words
  as pgjdbc treats them) and trims until the reference-mode test passes. Test: a literal
  of the whole string established in reference mode **(observed: 1710 characters,
  starting `abort,absent,access,aggregate,also,analyse,analyze,attach,`)**. The probe
  compared only the words starting with `a` between the two servers (equal); a
  difference of the full list in server mode is an engine report (R8).
- `getNumericFunctions()`, `getStringFunctions()`, `getSystemFunctions()`,
  `getTimeDateFunctions()` (constant; pgjdbc 385 to 438): comma-separated lists of the
  JDBC escape function names pgjdbc reports **(map: `NOW` is in the time and date list;
  `insert`, `locate`, `right` are missing from the string list)**. The lists are
  pgjdbc's even where the escape processor of JDBC-TYPES section 4 knows more or fewer
  functions; the part adds a test case that every listed function is accepted in a
  `{fn ...}` escape by `Connection.nativeSQL` and reports the ones that are not (R8), without changing the
  list.

## 4. Fixture schema and test support

Owned by `JDBC-DBMD-10-FIXTURE`. Every result-set test of this family loads the same
fixture into its class's own database and compares rows with pgjdbc's.

### 4.1 Objects

The fixture is the resource `testing/dbmd-fixture.sql`, written by the fixture part.
`.cswd/tmp/JDBC-DESIGN-METADATA/probe/fixture.sql` is a sketch to start from: all of its
statements were accepted by PostgreSQL 18.6, and by the stuntgres server except the two
kinds named under "left out" **(observed)**. The file must contain:

| Kind | Objects |
|---|---|
| Schemas | `md1` (with a comment) and `"Md2"` (mixed case, quoted) |
| Roles | `md_reader`, `md_writer` (cluster-wide: see 4.2) |
| Types | domains `md1.posint` (integer, check), `md1.code` (varchar(8), not null, default), `md1.dom_arr` (integer[]); enum `md1.mood`; composite `md1.pair` (with a comment); range `md1.rng` |
| Sequence | `md1.seq1` |
| Tables | `md1.parent` (primary key, two-column unique constraint, table and column comments); `md1.alltypes` (one column per type of the JDBC-TYPES type table plus identity always and by default, serial, bigserial, smallserial, a stored generated column, defaults, arrays, the domains, the enum, the composite); `md1.child` (two-column primary key, foreign keys of every kind: single-column with `ON UPDATE CASCADE ON DELETE SET NULL`, two-column to a unique constraint with `ON UPDATE RESTRICT ON DELETE SET DEFAULT DEFERRABLE INITIALLY DEFERRED`, self-referencing two-column with `ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE`, a check constraint); `md1."Mixed Case"` (quoted column names, a keyword as a name, names with `_` and `%`); `md1.under_score` and `md1.underXscore` (to tell a pattern from an exact name); `md1.nokey` (unique not-null column, no primary key, all privileges revoked from PUBLIC, a trigger); `md1.dropcol` (three columns, the middle one dropped afterwards); `md1.unlogged_t`; `md1.inh_parent` and `md1.inh_child` (inheritance); `"Md2"."T2"` (foreign key into `md1.parent`, `ON DELETE NO ACTION`) and `"Md2".parent` (same name as in `md1`) |
| Indexes on `md1.child` | descending with `NULLS LAST` over two columns (with a comment), unique partial, expression index, hash, `INCLUDE` |
| Views | `md1.v_parent` (with a comment), materialized `md1.mv_parent` |
| Partitioning | `md1.part` (range, two-column primary key) with partitions `part_2025`, `part_2026` |
| Foreign data | wrapper `md_fdw`, server `md_srv`, foreign table `md1.ft` |
| Routines | functions with IN and DEFAULT, OUT and INOUT, `RETURNS TABLE`, `RETURNS SETOF` a table, VARIADIC, `RETURNS void`, returning the composite, one in plpgsql, one with a comment; procedures `md1.p_simple` (with a comment) and `md1.p_inout` (IN, INOUT, OUT); `"Md2"."F Mixed"("Arg One" integer)`; aggregate `md1.agg_sum` |
| Privileges | `USAGE` on `md1` to both roles; `SELECT` on `parent` to `md_reader`; four privileges on `parent` to `md_writer` `WITH GRANT OPTION`; column privileges on `child` to both roles; `SELECT` on `v_parent` to `PUBLIC`; `EXECUTE` on a function and `USAGE` on the sequence to `md_reader` |

Left out, because the stuntgres server rejects them **(observed; section 7)**:
`DROP ROLE IF EXISTS` and `CREATE INDEX` on the partitioned table. The temporary table of
the sketch is not part of the file (4.2). psql meta-commands are not part of the file.

File format: one statement after the other, each ending with `;` at the end of a line; no
line inside a statement ends with `;`.

### 4.2 Loader `MetaFixture`

```java
public final class MetaFixture                    // test: testing/MetaFixture.java
    public static void load(TestDatabase db) throws SQLException, IOException;
    public static void createTemp(Connection connection) throws SQLException;
```

- `load` opens a connection with `db.connect()`, executes the statements of the resource
  one by one with `Statement.execute` in autocommit mode and closes the connection. A
  test class calls it once in `@BeforeAll`. The class's database is its own (JDBC-NATIVE
  10.6), so nothing needs to be dropped first.
- Roles are shared by all databases of a PostgreSQL instance and test classes run side by
  side: `load` ignores SQLState `42710` (the role exists) and `23505` (a concurrent
  creation) for the two `CREATE ROLE` statements and nothing else. **Risk:** how the
  stuntgres server scopes roles across the databases of one run is not established; the
  fixture part checks that two test classes can load the fixture one after the other in
  server mode and reports a failure (R8).
- `createTemp(connection)` creates the temporary table `md_temp (a integer PRIMARY KEY)`
  in the session of that connection; the tests of temporary objects call it on the
  connection whose metadata they read.

### 4.3 `MetaRows` and expectation files

```java
public final class MetaRows                       // test: testing/MetaRows.java
    public static MetaRows of(TestDatabase db);
    public MetaRows mask(String regex, String replacement);
    public MetaRows maskColumn(String label);
    public MetaRows keep(String label, String... values);
    public String render(ResultSet rs) throws SQLException;
    public void assertSection(String resource, String section, ResultSet rs)
            throws SQLException, IOException;
```

- `render` reads the whole result set and closes it. First line: for each column
  `<label>:<ResultSetMetaData.getColumnType>` joined by `|`. Then one line per row: the
  `getString` value of each cell joined by `|`; SQL NULL is written `null`; in a value,
  `\`, `|` and a line break are written `\\`, `\|` and `\n`.
- `of(db)` masks every cell equal to the database name as `<db>` and every cell equal to
  `db.user()` as `<user>`. `mask` adds a replacement applied to every cell (for example
  `_\d+$` to `_<oid>` for SPECIFIC_NAME, `^pg_temp_\d+$` to `pg_temp_<n>`).
  `maskColumn` writes `*` for every non-null cell of a column. `keep` drops rows whose
  cell in the named column is none of the values.
- `assertSection(resource, section, rs)`: the expectation file is a class-path resource
  made of sections, each starting with a line `== <section>` followed by the rendered
  lines. The method compares `render(rs)` with the section and fails with both texts in
  the message, the actual one in full, so that the implementer can establish the section
  in reference mode by copying it (R11). A missing section fails the same way.
- One expectation file per test class: `jdbc/dbmd/<name>.txt` below the test resources,
  owned by the part that owns the test class.

Because the header line holds labels and JDBC type codes, a result-set test asserts
decision 1 completely: columns, rows and order.

### 4.4 How a test selects its rows

- A test passes the fixture's schema (`md1`, `Md2`) or exact object names wherever the
  method has such an argument.
- A case that must pass null or `%` for the schema uses `keep` on the schema column with
  `md1` and `Md2`, plus the system rows the case is about. No test asserts the row count
  or the complete rows of a call that spans the whole database: the system catalogs of
  the two servers differ (7.2).
- Values that differ per run are masked: database, user, OIDs inside SPECIFIC_NAME,
  temporary schema numbers, and the two statistics columns of `getIndexInfo` (5.9).
- A test of this family creates no further objects, except the temporary table (4.2) and
  session settings; a case that needs another object belongs in the fixture.

## 5. Result-set methods

### 5.1 Common rules

1. **Catalog argument.** null: no restriction. A value for which
   `otherCatalog(catalog)` holds (any other name, also the empty string): the method
   returns an empty result with its columns and sends nothing **(observed for
   `getTables` and `getSchemas`)**. pgjdbc does this in every method with a catalog
   argument (`PgDMD`: the tests `catalog != null && !catalog.equals(...)`); each part
   has a case with `""` and a case with the database's own name. The empty result's
   labels and type codes are what pgjdbc shows for that case; they differ from the
   non-empty result where pgjdbc builds it differently **(observed: `getTables('')` has
   ten varchar columns; `getSchemas('', null)` has the labels `TABLE_CAT`, `TABLE_SCHEM`
   instead of `TABLE_SCHEM`, `TABLE_CATALOG`)**.
2. **Pattern arguments** (`schemaPattern`, `tableNamePattern`, `columnNamePattern`,
   `procedureNamePattern`, `functionNamePattern`, `typeNamePattern`): null means no
   filter; otherwise the stored name must match the value under SQL `LIKE`, with `%` and
   `_` as wildcards and `\` as the escape (`getSearchStringEscape()`). The empty string
   matches no name. Matching is case-sensitive against the stored name; nothing is
   folded **(observed: `under\_score` matches one table, `under_score` matches
   `under_score` and `underxscore`)**.
3. **Name arguments that JDBC declares as plain names** (`schema`, `table` of
   `getPrimaryKeys`, `getImportedKeys`, `getExportedKeys`, `getCrossReference`,
   `getIndexInfo`, `getBestRowIdentifier`, `getVersionColumns`, `getColumnPrivileges`):
   null means no restriction where pgjdbc allows it **(observed:
   `getPrimaryKeys(null, null, null)` returns the keys of all tables)**. Whether a value
   is compared for equality or as a pattern is established per method in reference mode
   with the pair `under_score` / `underxscore`; the entry of each method names the case.
4. **Parameters are bound** (decision 5). A statement is assembled only from constant
   fragments chosen by which arguments are null and, for `getTables`, by the type list.
5. **Transaction.** Outside autocommit a metadata call starts a transaction like any
   query; in a failed transaction it raises the server's `25P02`. Each part has one case
   for the second fact.
6. **Result sets from a query** are `TYPE_SCROLL_INSENSITIVE`, `CONCUR_READ_ONLY`, and
   their statement closes with them (`DbmdCore.query`). Result sets built in memory:
   section 6.
7. **Columns.** Labels, their order and the JDBC type code of every column are pgjdbc's
   (4.3 asserts them). The entries below list them as observed, with pgjdbc's server
   type name after each label: `name`, `text` and `varchar` are type code 12, `int2` 5,
   `int4` 4, `bool` -7, `float4` 7. The meaning of each column is the Java 17 API
   documentation of the method
   (`https://docs.oracle.com/en/java/javase/17/docs/api/java.sql/java/sql/DatabaseMetaData.html`).
   Where pgjdbc returns a label the API does not define, pgjdbc's label is used
   **(observed: three columns of `getProcedures` are labelled `?column?`)**.
8. **Catalog columns** (`TABLE_CAT`, `PROCEDURE_CAT` and the like) hold the current
   database name **(observed)**, except where an entry says otherwise.
9. **Order.** Each entry gives the order. Sorting by a catalog name sorts as type `name`
   does: by bytes (upper case before lower case; **observed: `Mixed Case` before
   `alltypes`**). A statement that turns a name into `text` before sorting must sort it
   with `COLLATE "C"`; a result built in memory sorts with `String.compareTo`. Ties are
   resolved as pgjdbc resolves them (the entry names the further keys).
10. **`hideUnprivilegedObjects`.** When `hideUnprivileged()` is true, the six methods of
    decision 9 return only objects the current user has a privilege on, tested with the
    `has_*_privilege` function and the privilege list pgjdbc uses at the line the entry
    names. Each of the six parts has one case: a connection opened with
    `TestDatabase.connect(Properties)` and `hideUnprivilegedObjects=true`, then `SET ROLE
    md_reader`, expectation established in reference mode.
11. **Built in memory by pgjdbc** (therefore all-varchar or mixed type codes that no
    catalog query yields directly): `getTableTypes`, `getColumns`, `getColumnPrivileges`,
    `getTablePrivileges`, `getBestRowIdentifier`, `getVersionColumns`, `getTypeInfo`,
    `getProcedureColumns`, `getFunctionColumns`, `getClientInfoProperties` and every
    empty result of rule 1. The other methods return the query's result directly.
12. **Own SQL** follows 7.3. In particular the position of a column inside a key comes
    from `unnest(...) WITH ORDINALITY` or `generate_subscripts` over `conkey`, `confkey`
    or `indkey`, both of which the two servers answered alike in the probe (blocks B07,
    B08), never from `information_schema._pg_expandarray`.
13. **Comments** (REMARKS) come from `pg_description` through `obj_description` /
    `col_description` or a join; a missing comment is SQL NULL **(observed)**.

### 5.2 `getSchemas`

`DbmdSchemas` (DBMD-11). pgjdbc: 1650, 1655. Not built in memory.

Columns of `getSchemas` **(observed, 2)**: `TABLE_SCHEM` name, `TABLE_CATALOG` name.

- Forms: `getSchemas()` is `getSchemas(null, null)`. Arguments of the second form:
  `catalog` (rule 1), `schemaPattern` (rule 2).
- Rows: one per schema, without `pg_toast` and without the temporary schemas
  (`pg_temp_<n>`, `pg_toast_temp_<n>`) of other sessions; the session's own temporary
  schemas are listed once the session has one **(observed)**. `TABLE_CATALOG` is the
  current database.
- Order: `TABLE_SCHEM`.
- Reads: `pg_namespace`; `current_schemas(true)` for the own temporary schema (7.3 rule
  2); `current_database()`. With `hideUnprivileged()`: the schema-privilege test of
  pgjdbc 1676.
- Establish: the labels of the empty result (rule 1); the rows after
  `MetaFixture.createTemp` (masked numbers).

### 5.3 `getCatalogs`, `getTableTypes`

`DbmdSchemas` (DBMD-11). pgjdbc: 1685, 1693.

Columns of `getCatalogs` **(observed, 1)**: `TABLE_CAT` name.

Columns of `getTableTypes` **(observed, 1)**: `TABLE_TYPE` varchar.

- `getCatalogs()`: one row per database that allows connections (`pg_database`,
  `datallowconn`), ordered by name. The test asserts that the class's database is listed
  and that the names ascend; it asserts no other name (7.2).
- `getTableTypes()`: built in memory, no query. The 18 names **(observed)**, in this
  order: `FOREIGN TABLE`, `INDEX`, `MATERIALIZED VIEW`, `PARTITIONED INDEX`,
  `PARTITIONED TABLE`, `SEQUENCE`, `SYSTEM INDEX`, `SYSTEM TABLE`, `SYSTEM TOAST INDEX`,
  `SYSTEM TOAST TABLE`, `SYSTEM VIEW`, `TABLE`, `TEMPORARY INDEX`, `TEMPORARY SEQUENCE`,
  `TEMPORARY TABLE`, `TEMPORARY VIEW`, `TYPE`, `VIEW`. `DbmdSchemas` declares them as
  `static final String[] TABLE_TYPES` (package-private, sorted) for `getTables`.

### 5.4 `getTables`

`DbmdTables` (DBMD-12). pgjdbc: 1457; the type clauses 1563 to 1647. Not built in memory.

Columns of `getTables` **(observed, 10)**: `TABLE_CAT` name, `TABLE_SCHEM` name,
`TABLE_NAME` name, `TABLE_TYPE` text, `REMARKS` text, `TYPE_CAT` text, `TYPE_SCHEM` text,
`TYPE_NAME` text, `SELF_REFERENCING_COL_NAME` text, `REF_GENERATION` text.

- Arguments: `catalog` (rule 1), `schemaPattern`, `tableNamePattern` (rule 2), `types`:
  null means every relation; otherwise only the listed names of `TABLE_TYPES` (5.3).
  Establish: an empty array, an unknown name, a name in another case.
- Rows: one per relation of `pg_class`. `TABLE_TYPE` is derived from the relation kind
  (`r`, `p`, `v`, `m`, `i`, `I`, `S`, `f`, `c`) and the kind of schema: `pg_catalog` and
  `information_schema` give the `SYSTEM ...` names, `pg_toast` the `SYSTEM TOAST ...`
  names, a temporary schema the `TEMPORARY ...` names, any other schema the plain names
  **(map)**. `REMARKS` is the relation's comment. The five columns after it are empty
  strings **(observed)**.
- Order: `TABLE_TYPE`, `TABLE_SCHEM`, `TABLE_NAME` (rule 9).
- Reads: `pg_class`, `pg_namespace`, `pg_description`. With `hideUnprivileged()`: the
  table-privilege test of pgjdbc 1529.
- Establish: the `TABLE_TYPE` of the index of a partitioned table **(observed: SQL NULL
  for `part_pkey`, sorted last)**; `md_temp` after `MetaFixture.createTemp` **(observed:
  `TEMPORARY TABLE` in `pg_temp_<n>`)**; one case per type name of the fixture.

### 5.5 `getColumns`

`DbmdColumns` (DBMD-13). pgjdbc: 1710 to 1962. Built in memory by pgjdbc (rule 11).

Columns of `getColumns` **(observed, 24)**: `TABLE_CAT` varchar, `TABLE_SCHEM` varchar,
`TABLE_NAME` varchar, `COLUMN_NAME` varchar, `DATA_TYPE` int2, `TYPE_NAME` varchar,
`COLUMN_SIZE` int4, `BUFFER_LENGTH` varchar, `DECIMAL_DIGITS` int4, `NUM_PREC_RADIX` int4,
`NULLABLE` int4, `REMARKS` varchar, `COLUMN_DEF` varchar, `SQL_DATA_TYPE` int4,
`SQL_DATETIME_SUB` int4, `CHAR_OCTET_LENGTH` int4, `ORDINAL_POSITION` int4, `IS_NULLABLE`
varchar, `SCOPE_CATALOG` varchar, `SCOPE_SCHEMA` varchar, `SCOPE_TABLE` varchar,
`SOURCE_DATA_TYPE` int2, `IS_AUTOINCREMENT` varchar, `IS_GENERATEDCOLUMN` varchar.

- Arguments: `catalog` (rule 1), `schemaPattern`, `tableNamePattern`,
  `columnNamePattern` (rule 2).
- Rows: one per live column (`attnum > 0`, not dropped) of tables, partitioned tables,
  views, materialized views and foreign tables (establish the exact kinds, also whether a
  composite type's attributes appear).
- Cells **(observed on `md1.alltypes`)**: `DATA_TYPE` and `TYPE_NAME` are those of the
  type table (`TypeTable.find(oid, connection.typeLookup())`, JDBC-TYPES 5.1; JDBC-CORE
  11.3 for other types: `"md1"."mood"` 12, `"md1"."pair"` 2002, arrays `_int4` 2003). A
  domain column is 2001 with the domain's quoted name and `SOURCE_DATA_TYPE` the base
  type's code. A column whose default is `nextval(...)` of an integer type is named
  `serial`, `bigserial` or `smallserial`. `COLUMN_SIZE` is the precision of the type and
  modifier (JDBC-TYPES 5.2), the display size where the precision is 0 (establish:
  `numeric` without modifier showed 0). `DECIMAL_DIGITS` is the scale (SQL NULL for
  `numeric` without modifier). `NUM_PREC_RADIX` is 2 for `bit` and `varbit`, else 10.
  `NULLABLE` / `IS_NULLABLE` are 0 / `NO` for a NOT NULL column and for a NOT NULL domain,
  else 1 / `YES`. `COLUMN_DEF` is the default expression text, for a generated column the
  generation expression. `CHAR_OCTET_LENGTH` is set for character and `bytea` columns
  only. `ORDINAL_POSITION` counts live columns from 1 (no gap after a dropped column:
  `md1.dropcol`). `IS_AUTOINCREMENT` is `YES` for serial and identity columns,
  `IS_GENERATEDCOLUMN` `YES` for generated columns. `BUFFER_LENGTH`, `SQL_DATA_TYPE`,
  `SQL_DATETIME_SUB` and the three `SCOPE_` columns are SQL NULL. `REMARKS` is the column
  comment.
- Order: schema, table, attribute number.
- Reads: `pg_attribute` (`attidentity`, `attgenerated`, `atttypmod`, `attnotnull`),
  `pg_class`, `pg_namespace`, `pg_type` (also the base type of a domain: `typbasetype`,
  `typtypmod`, `typnotnull`), `pg_attrdef` with `pg_get_expr`, `pg_description`.

### 5.6 `getPrimaryKeys`

`DbmdKeys` (DBMD-14). pgjdbc: 2428. Not built in memory.

Columns of `getPrimaryKeys` **(observed, 6)**: `TABLE_CAT` name, `TABLE_SCHEM` name,
`TABLE_NAME` name, `COLUMN_NAME` name, `KEY_SEQ` int4, `PK_NAME` name.

- Arguments: `catalog` (rule 1), `schema`, `table` (rule 3; all three null lists every
  primary key **(observed)**). Establish: `under_score` as `table`.
- Rows: one per column of each primary key; `KEY_SEQ` is the position in the key from 1;
  `PK_NAME` the constraint name.
- Order: `TABLE_NAME`, `PK_NAME`, `KEY_SEQ` **(map)**; establish where the schema enters
  the order with `md1.parent` and `"Md2".parent`.
- Reads: `pg_constraint` (`contype` `p`, `conkey`) or `pg_index` (`indisprimary`,
  `indkey`), `pg_class`, `pg_namespace`, `pg_attribute`; key positions by rule 12. The
  partitioned table and its partitions each have a key **(observed)**.

### 5.7 `getBestRowIdentifier`, `getVersionColumns`

`DbmdKeys` (DBMD-14). pgjdbc: 2303, 2380. Both built in memory by pgjdbc.

Columns of `getBestRowIdentifier` **(observed, 8)**: `SCOPE` int2, `COLUMN_NAME` varchar,
`DATA_TYPE` int2, `TYPE_NAME` varchar, `COLUMN_SIZE` int4, `BUFFER_LENGTH` int4,
`DECIMAL_DIGITS` int2, `PSEUDO_COLUMN` int2.

Columns of `getVersionColumns` **(observed, 8)**: `SCOPE` int2, `COLUMN_NAME` varchar,
`DATA_TYPE` int2, `TYPE_NAME` varchar, `COLUMN_SIZE` int4, `BUFFER_LENGTH` int4,
`DECIMAL_DIGITS` int2, `PSEUDO_COLUMN` int2.

- `getBestRowIdentifier(catalog, schema, table, scope, nullable)`: the columns of the
  table's primary key, one row each, ordered by attribute number. A table without a
  primary key gives no row even when it has a unique NOT NULL column **(observed:
  `md1.nokey`)**. `SCOPE` is 2 (`bestRowSession`) and `PSEUDO_COLUMN` 1
  (`bestRowNotPseudo`) **(observed)**; `DATA_TYPE`, `TYPE_NAME`, `COLUMN_SIZE` and
  `DECIMAL_DIGITS` as in 5.5 (computed in this class from `TypeTable`, JDBC-TYPES 5.2);
  `BUFFER_LENGTH` SQL NULL. Establish: whether `scope` and `nullable` change anything,
  and rule 3 for `schema` and `table`. Reads: `pg_index` (`indisprimary`), `pg_class`,
  `pg_namespace`, `pg_attribute`.
- `getVersionColumns(catalog, schema, table)`: one fixed row for the system column `ctid`
  **(observed: `null|ctid|1111|tid|null|null|null|2`)**, no query; rule 1 applies.
  Establish: the answer for a table that does not exist.

### 5.8 `getImportedKeys`, `getExportedKeys`, `getCrossReference`

`DbmdForeignKeys` (DBMD-15). pgjdbc: 2696, 2702, 2708, all through 2572. Not built in

Columns of the three methods **(observed, 14)**: `PKTABLE_CAT` name, `PKTABLE_SCHEM` name,
`PKTABLE_NAME` name, `PKCOLUMN_NAME` name, `FKTABLE_CAT` name, `FKTABLE_SCHEM` name,
`FKTABLE_NAME` name, `FKCOLUMN_NAME` name, `KEY_SEQ` int4, `UPDATE_RULE` int4, `DELETE_RULE`
int4, `FK_NAME` name, `PK_NAME` name, `DEFERRABILITY` int4.

memory. One private method serves the three, with the primary side, the foreign side or
both restricted.

- Arguments: catalogs (rule 1, for either side), schemas and tables (rule 3).
  `getImportedKeys` restricts the foreign-key table, `getExportedKeys` the referenced
  table, `getCrossReference` both.
- Rows: one per column pair of each foreign-key constraint. `KEY_SEQ` is the position in
  the key. `UPDATE_RULE` and `DELETE_RULE` map the constraint's actions to the JDBC
  constants: cascade 0, restrict 1, set null 2, no action 3, set default 4
  **(observed for all five)**. `DEFERRABILITY`: 5 initially deferred, 6 deferrable
  initially immediate, 7 not deferrable **(observed)**. `PK_NAME` is the name of the
  unique or primary-key index the constraint depends on **(observed:
  `parent_code_region_key` for the key over the unique constraint)**, `FK_NAME` the
  constraint name.
- Order **(map)**: `getImportedKeys` by the referenced table's schema and name, then
  constraint name and `KEY_SEQ`; `getExportedKeys` and `getCrossReference` by the
  foreign-key table's schema and name, then constraint name and `KEY_SEQ`. Establish with
  `md1.child` (three constraints, one self-referencing) and `"Md2"."T2"`.
- Reads: `pg_constraint` (`contype` `f`, `conkey`, `confkey`, `confupdtype`,
  `confdeltype`, `condeferrable`, `condeferred`, `conindid`), `pg_class` for both tables
  and the index, `pg_namespace` for both, `pg_attribute` for both; key positions by rule
  12. The referenced index is `conindid` (or the `pg_depend` row from the constraint to
  the index; the probe found that row alike on both servers, block B09).
- **Rule for the own statement:** explicit `JOIN ... ON` for every table (7.3 rule 3);
  the two key arrays are unnested once, side by side, in `FROM`; no cross product of
  attribute tables filtered afterwards.
- **Risk:** pgjdbc's statement for these methods made the stuntgres server grow without
  bound (7.1 gap 1, `JDBC-ENGINE-01-IMPORTED-KEYS-RUNAWAY`). The own statement is a
  different one, but nothing was run: the first server-mode run of this part's test is
  made alone (`TEST=DbmdForeignKeysTest`) under the cap of 8.2. If it is killed, stop and
  report (R8); do not wait for the engine part and do not weaken the test.

### 5.9 `getIndexInfo`

`DbmdIndexes` (DBMD-16). pgjdbc: 2846. Not built in memory.

Columns of `getIndexInfo` **(observed, 14)**: `TABLE_CAT` name, `TABLE_SCHEM` name,
`TABLE_NAME` name, `NON_UNIQUE` bool, `INDEX_QUALIFIER` text, `INDEX_NAME` name, `TYPE`
int4, `ORDINAL_POSITION` int4, `COLUMN_NAME` text, `ASC_OR_DESC` text, `CARDINALITY` float4,
`PAGES` int4, `FILTER_CONDITION` text, `REMARKS` text.

- Arguments: `catalog` (rule 1), `schema`, `table` (rule 3), `unique` (true: unique
  indexes only), `approximate` (no effect; establish).
- Rows: one per index column, no `tableIndexStatistic` row. `NON_UNIQUE` is the negation
  of `indisunique`. `INDEX_QUALIFIER` is SQL NULL. `TYPE` is 2
  (`tableIndexHashed`) for a hash index, else 3 (`tableIndexOther`) **(observed)**. `ORDINAL_POSITION` counts
  from 1. `COLUMN_NAME` is the column name, for an expression the expression text as
  `pg_get_indexdef(index, position, false)` gives it. `ASC_OR_DESC` is `A` or `D` from
  the column's sort option, SQL NULL where the access method has no order **(observed:
  hash)**. `CARDINALITY` and `PAGES` are the index's `reltuples` and `relpages`: storage
  statistics, simulated by the stuntgres server (AGENTS.md, "Simulated but query
  compatible"); the test masks both columns (`maskColumn`). `FILTER_CONDITION` is the
  predicate text of a partial index **(observed: `(amount > (100)::numeric)`)**.
  `REMARKS` is the index comment. An `INCLUDE` column gets a row like a key column
  **(observed: `child_incl_idx`)**.
- Order: `NON_UNIQUE`, `TYPE`, `INDEX_NAME`, `ORDINAL_POSITION`.
- Reads: `pg_index` (`indkey`, `indoption`, `indnatts`, `indnkeyatts`, `indpred`),
  `pg_class` for table and index, `pg_namespace`, `pg_am`, `pg_attribute`,
  `pg_description`; `pg_get_indexdef`, `pg_get_expr`; positions by rule 12 (`indkey` is
  zero-based: block B08).

### 5.10 `getTypeInfo`

`DbmdTypeInfo` (DBMD-17). pgjdbc: 2717 to 2842. Built in memory by pgjdbc.

Columns of `getTypeInfo` **(observed, 18)**: `TYPE_NAME` varchar, `DATA_TYPE` int2,
`PRECISION` int4, `LITERAL_PREFIX` varchar, `LITERAL_SUFFIX` varchar, `CREATE_PARAMS`
varchar, `NULLABLE` int2, `CASE_SENSITIVE` bool, `SEARCHABLE` int2, `UNSIGNED_ATTRIBUTE`
bool, `FIXED_PREC_SCALE` bool, `AUTO_INCREMENT` bool, `LOCAL_TYPE_NAME` varchar,
`MINIMUM_SCALE` int2, `MAXIMUM_SCALE` int2, `SQL_DATA_TYPE` int4, `SQL_DATETIME_SUB` int4,
`NUM_PREC_RADIX` int4.

- No arguments. Rows: one per type of `pg_type` the session may use (444 rows on the
  probe database, the fixture's types and array types included), plus what pgjdbc adds
  itself (establish: the serial names). With `hideUnprivileged()`: the type-privilege
  test of pgjdbc 2747.
- Cells **(observed first rows: `bool|-7|0|'|'|null|1|f|3|t|f|f|null|0|0|null|null|10`)**:
  `DATA_TYPE` and `PRECISION` from the type table (`TypeTable.find`,
  `TypeTable.maximumPrecision`, JDBC-TYPES 5.1 and 5.2); the literal prefix and suffix,
  `NULLABLE` 1, `SEARCHABLE` 3, the boolean columns and `NUM_PREC_RADIX` 10 as pgjdbc
  shows them per type.
- Order: as pgjdbc returns the rows (it sorts in Java; establish the keys: `DATA_TYPE`
  first).
- Reads: `pg_type`, `pg_namespace` (and `pg_class` for row types).
- Test: the two servers hold different sets of built-in types (7.2), so the test keeps
  (`MetaRows.keep` on `TYPE_NAME`) the types of the JDBC-TYPES type table that both
  servers have, their array types and the fixture's types, and asserts those rows and
  their relative order; it never asserts the total.

### 5.11 `getProcedures`

`DbmdProcedures` (DBMD-18A). pgjdbc: 1189. Not built in memory.

Columns of `getProcedures` **(observed, 9)**: `PROCEDURE_CAT` name, `PROCEDURE_SCHEM` name,
`PROCEDURE_NAME` name, `?column?` text, `?column?` text, `?column?` text, `REMARKS` text,
`PROCEDURE_TYPE` int4, `SPECIFIC_NAME` text.

- Arguments: `catalog` (rule 1), `schemaPattern`, `procedureNamePattern` (rule 2).
- Rows: one per procedure (`prokind` `p`); functions and aggregates are not listed
  **(observed: two rows for `md1`)**. Columns 4 to 6 are SQL NULL under the label
  `?column?` **(observed; rule 7)**. `REMARKS` is the comment. `PROCEDURE_TYPE`
  **(observed: 2)**. `SPECIFIC_NAME` is the name, `_`, and the routine's OID (masked in
  tests).
- Order: `PROCEDURE_SCHEM`, `PROCEDURE_NAME`, then the OID.
- Reads: `pg_proc`, `pg_namespace`, `pg_description`. With `hideUnprivileged()`: the
  function-privilege test of pgjdbc 1236.

### 5.12 `getProcedureColumns`

`DbmdProcedureColumns` (DBMD-18B). pgjdbc: 1245 to 1453. Built in memory by pgjdbc.

Columns of `getProcedureColumns` **(observed, 20)**: `PROCEDURE_CAT` varchar,
`PROCEDURE_SCHEM` varchar, `PROCEDURE_NAME` varchar, `COLUMN_NAME` varchar, `COLUMN_TYPE`
int2, `DATA_TYPE` int2, `TYPE_NAME` varchar, `PRECISION` int4, `LENGTH` int4, `SCALE` int2,
`RADIX` int2, `NULLABLE` int2, `REMARKS` varchar, `COLUMN_DEF` varchar, `SQL_DATA_TYPE`
int4, `SQL_DATETIME_SUB` int4, `CHAR_OCTET_LENGTH` int4, `ORDINAL_POSITION` int4,
`IS_NULLABLE` varchar, `SPECIFIC_NAME` varchar.

- Arguments: `catalog` (rule 1), `schemaPattern`, `procedureNamePattern`,
  `columnNamePattern` (rule 2).
- Rows: for **every routine** of `pg_proc` that matches, functions and aggregates
  included **(observed: 38 rows for `md1`, starting with `agg_sum`)**: a row
  `returnValue` with `COLUMN_TYPE` 5 (`procedureColumnReturn`) and `ORDINAL_POSITION` 0
  for a routine with a scalar result, `void` included, then one row per argument: the
  argument's name, or `$<n>` for an unnamed one; `COLUMN_TYPE` 1 for IN, 2 for INOUT, 4
  for OUT; the columns of a `RETURNS TABLE` result are 5 with positions continuing after
  the arguments; the attributes of a returned row type (`f_pair`, `f_setof`) are 3
  (`procedureColumnResult`) with position 0; a routine with OUT arguments or a row-type
  result has no `returnValue` row **(all observed)**. The expectation file covers
  `f_table`, `f_setof`, `f_pair`, `f_out`, `f_variadic`, `f_void`, `p_inout`, `agg_sum`. `DATA_TYPE` and `TYPE_NAME` from the type table. `PRECISION`, `LENGTH`,
  `SCALE`, `RADIX` are SQL NULL, `NULLABLE` is 2 (`procedureNullableUnknown`),
  `IS_NULLABLE` the empty string **(observed)**. `SPECIFIC_NAME` as in 5.11.
- Order: schema, routine name, OID, then the row position within the routine.
- Reads: `pg_proc` (`proargtypes`, `proallargtypes`, `proargmodes`, `proargnames`,
  `prorettype`, `proretset`), `pg_namespace`, `pg_type`, and `pg_attribute` through
  `typrelid` for a returned row type. Argument arrays are unnested side by side with
  `unnest(a, b, c) WITH ORDINALITY` or by subscript (both alike on the two servers:
  block B11).

### 5.13 `getFunctions`

`DbmdFunctions` (DBMD-19A). pgjdbc: 3266. Not built in memory.

Columns of `getFunctions` **(observed, 6)**: `FUNCTION_CAT` name, `FUNCTION_SCHEM` name,
`FUNCTION_NAME` name, `REMARKS` text, `FUNCTION_TYPE` int4, `SPECIFIC_NAME` text.

- Arguments: `catalog` (rule 1), `schemaPattern`, `functionNamePattern` (rule 2).
- Rows: one per function (`prokind` `f`); procedures and aggregates are not listed
  **(observed: 13 rows for `md1`: the eight functions of the fixture and the five
  constructor functions of the range type)**. `FUNCTION_TYPE` is 2
  (`functionReturnsTable`) for a set-returning function (`f_table`, `f_setof`), else 1
  (`functionNoTable`) **(observed)**. `REMARKS`, `SPECIFIC_NAME` as
  in 5.11.
- Order: `FUNCTION_SCHEM`, `FUNCTION_NAME`, then the OID.
- Reads: `pg_proc`, `pg_namespace`, `pg_description`, `pg_type` for the result kind. With
  `hideUnprivileged()`: the function-privilege test of pgjdbc 3328.

### 5.14 `getFunctionColumns`

`DbmdFunctionColumns` (DBMD-19B). pgjdbc: 3337 to 3543. Built in memory by pgjdbc.

Columns of `getFunctionColumns` **(observed, 17)**: `FUNCTION_CAT` varchar, `FUNCTION_SCHEM`
varchar, `FUNCTION_NAME` varchar, `COLUMN_NAME` varchar, `COLUMN_TYPE` int2, `DATA_TYPE`
int2, `TYPE_NAME` varchar, `PRECISION` int2, `LENGTH` int4, `SCALE` int2, `RADIX` int2,
`NULLABLE` int2, `REMARKS` varchar, `CHAR_OCTET_LENGTH` int4, `ORDINAL_POSITION` int4,
`IS_NULLABLE` varchar, `SPECIFIC_NAME` varchar.

- Arguments: `catalog` (rule 1), `schemaPattern`, `functionNamePattern`,
  `columnNamePattern` (rule 2).
- Rows: as 5.12 (every routine, procedures and aggregates included), with the 17 columns
  of this method and its own constants: the `returnValue` row and the columns of a
  `RETURNS TABLE` result have `COLUMN_TYPE` 4 (`functionReturn`); arguments are 1 (IN), 2
  (INOUT), 3 (OUT); the attributes of a returned row type are 5
  (`functionColumnResult`) **(all observed)**. The expectation file covers the routines
  of 5.12 and `"Md2"."F Mixed"` (a quoted argument name).
- Order and reads: as 5.12. The statement is this class's own; nothing is shared with
  `DbmdProcedureColumns`.

### 5.15 `getTablePrivileges`

`DbmdTablePrivileges` (DBMD-20A). pgjdbc: 2061 to 2135, the ACL reading 2141 to 2300.

Columns of `getTablePrivileges` **(observed, 7)**: `TABLE_CAT` varchar, `TABLE_SCHEM`
varchar, `TABLE_NAME` varchar, `GRANTOR` varchar, `GRANTEE` varchar, `PRIVILEGE` varchar,
`IS_GRANTABLE` varchar.

Built in memory by pgjdbc.

- Arguments: `catalog` (rule 1), `schemaPattern`, `tableNamePattern` (rule 2).
- Rows: one per relation, grantee and privilege. A relation without an ACL shows the
  owner's implicit privileges, each grantable **(observed: seven rows per table: DELETE,
  INSERT, REFERENCES, SELECT, TRIGGER, TRUNCATE, UPDATE)**. Explicit grants add rows with
  `GRANTOR`, `GRANTEE` and `IS_GRANTABLE` `YES` / `NO` **(observed: `md_writer` UPDATE
  `YES`)**. Establish: which relation kinds are listed, the grantee text for PUBLIC
  (`md1.v_parent`), a table with everything revoked from PUBLIC (`md1.nokey`), and whether
  the PostgreSQL 17 privilege MAINTAIN appears.
- Order: `TABLE_SCHEM`, `TABLE_NAME`, `PRIVILEGE`, then as pgjdbc orders grantees
  (establish with `md1.parent`).
- Reads: `pg_class` (`relacl`, `relowner`), `pg_namespace`, `pg_roles`. The ACL may be
  expanded on the server with `aclexplode` (alike on both servers: block B13) and the
  default ACL of an owner with `acldefault`, or read as text and expanded in Java;
  `acldefault` was not probed (a difference in server mode is an R8 report).

### 5.16 `getColumnPrivileges`

`DbmdColumnPrivileges` (DBMD-20B). pgjdbc: 1966 to 2057. Built in memory by pgjdbc.

Columns of `getColumnPrivileges` **(observed, 8)**: `TABLE_CAT` varchar, `TABLE_SCHEM`
varchar, `TABLE_NAME` varchar, `COLUMN_NAME` varchar, `GRANTOR` varchar, `GRANTEE` varchar,
`PRIVILEGE` varchar, `IS_GRANTABLE` varchar.

- Arguments: `catalog` (rule 1), `schema`, `table` (rule 3), `columnNamePattern` (rule 2).
- Rows: for each column the privileges that apply to it: those of the table, and the
  column's own grants (`attacl`). **(Observed on `md1.child`: 49 rows, seven per column;
  where a column grant exists for a privilege, the row shows that grantee instead of the
  owner: `amount` has UPDATE for `md_reader` and no UPDATE row for the owner.)** The exact
  merge is pgjdbc's and is established with the expectation file for `md1.child`.
- Order: `COLUMN_NAME`, `PRIVILEGE`, then as pgjdbc orders grantees.
- Reads: `pg_attribute` (`attacl`), `pg_class` (`relacl`, `relowner`), `pg_namespace`,
  `pg_roles`; ACL expansion as in 5.15. The statement is this class's own.

### 5.17 `getUDTs`

`DbmdUdt` (DBMD-21). pgjdbc: 3078. Not built in memory.

Columns of `getUDTs` **(observed, 7)**: `TYPE_CAT` name, `TYPE_SCHEM` name, `TYPE_NAME`
name, `CLASS_NAME` text, `DATA_TYPE` int4, `REMARKS` text, `BASE_TYPE` int4.

- Arguments: `catalog` (rule 1), `schemaPattern`, `typeNamePattern` (rule 2; a pattern
  with a dot is taken as schema and name **(observed: `md1.pair` with a null schema
  pattern finds the type)**; establish which dot splits), `types`: null means both
  kinds; otherwise only `Types.STRUCT` (2002) and `Types.DISTINCT` (2001) select rows,
  other codes select nothing.
- Rows: composite types, the row types of tables included **(observed: `alltypes` as
  2002)**, and domains. `CLASS_NAME` is SQL NULL. `DATA_TYPE` 2002 or 2001. `REMARKS` is
  the type's comment. `BASE_TYPE` is, for a domain, the JDBC type code of its base type
  from the type table **(observed: 12 for `md1.code`, 2003 for `md1.dom_arr`)**, SQL NULL
  for a composite type.
- Order: `DATA_TYPE`, `TYPE_SCHEM`, `TYPE_NAME`.
- Reads: `pg_type` (`typtype` `c` and `d`, `typbasetype`), `pg_namespace`,
  `pg_description`. The base-type code is computed in Java from `TypeTable.find`, or by a
  `CASE` the class generates from the type table. With `hideUnprivileged()`: the test of
  pgjdbc 3165.

### 5.18 `getClientInfoProperties`

`DbmdUdt` (DBMD-21). pgjdbc: 3230. Built in memory, no catalog query.

Columns of `getClientInfoProperties` **(observed, 4)**: `NAME` varchar, `MAX_LEN` int4,
`DEFAULT_VALUE` varchar, `DESCRIPTION` varchar.

- One row: `NAME` `ApplicationName`, `MAX_LEN` `maxNameLength()` **(observed: 63)**,
  `DEFAULT_VALUE` the empty string **(observed)**, `DESCRIPTION` a sentence of this
  driver's own (decision 11): `Name of the application that uses this connection.`
- The test masks `DESCRIPTION` (`maskColumn`) and asserts the rest in reference mode too.

### 5.19 Rejected methods

`getSuperTypes`, `getSuperTables`, `getAttributes`, `getPseudoColumns` and
`getRowIdLifetime` are not overridden: the generated root raises
`SQLFeatureNotSupportedException` `0A000` (`Errors.notImplemented`), as pgjdbc does
**(observed; `PgDMD` 3547 to 3610)**. DBMD-00 tests the five in a `@ReferenceToo` class.

## 6. In-memory result sets

`DbmdCore.memory(names, typeOids, rows)` returns

```java
new PgResultSet(null, connection, new MemoryResultData(names, typeOids, rows),
        ResultSet.TYPE_SCROLL_INSENSITIVE, ResultSet.CONCUR_READ_ONLY,
        connection.getHoldability(), 0, 0)
```

- `names` are the column labels, `typeOids` the type OIDs that give pgjdbc's JDBC type
  code for each column (`Oid.VARCHAR`, `Oid.INT2`, `Oid.INT4`, `Oid.BOOL`, ... of
  JDBC-TYPES 5.1); a row is one `String[]` in the server's text format, null for SQL
  NULL (booleans as `t` / `f`).
- This is the only builder; `MemoryResultData` itself belongs to `JDBC-RS-00B-DATA` and is
  not edited. No statement is attached (decision 7).
- A method that reads rows from the server and rewrites them (privileges, columns, type
  info) reads them with `query`, closes that result set, and returns `memory(...)`.

## 7. Engine gaps

Source: the outputs of the interrupted probe run under
`.cswd/tmp/JDBC-DESIGN-METADATA/probe/` (section 9). Nothing was re-run.

### 7.1 Gaps with a part

| # | Observation (stuntgres wire server against PostgreSQL 18.6) | Part | Needed by this family |
|---|---|---|---|
| 1 | pgjdbc's `getImportedKeys` on the five-foreign-key table of the probe fixture never returned; the server grew to 113 GB in 40 seconds and the host ran out of memory. The statement is the shared imported/exported-keys query of pgjdbc (`PgDMD` 2572); it was not captured. | `JDBC-ENGINE-01-IMPORTED-KEYS-RUNAWAY` (added by the coordinating session) | no; DBMD-15 carries a risk note and the `JOIN ... ON` rule (5.8) |
| 2 | `information_schema._pg_expandarray` does not exist, for `smallint[]`, `integer[]` and `int2vector` arguments (`42883`); PostgreSQL has it as `(anyarray, OUT x anyelement, OUT n integer)`. pgjdbc's own primary-key, best-row-identifier and index statements call it. | `JDBC-ENGINE-02-PG-EXPANDARRAY` | no (5.1 rule 12) |
| 3 | `SELECT pg_my_temp_schema()` fails with `42883` through the wire server (also schema-qualified, before and after a temporary table exists), although the engine has a routine identity for it (`internal/engine/catalog/builtin_routine_identities.go`) and the part `PG18-FUNC-INFO-SESSION-NAMESPACE` is done. | `JDBC-ENGINE-03-MY-TEMP-SCHEMA` | no (7.3) |
| 4 | `DROP ROLE IF EXISTS` is rejected with `0A000` ("DROP ROLE IF EXISTS is unsupported"); a reference test pins that boundary (`reference/object_address/role_boundary_test.go`). | `JDBC-ENGINE-04-DROP-ROLE-IF-EXISTS` | no (the fixture does not use it) |
| 5 | `CREATE INDEX` on a partitioned table is rejected with `0A000` ("partitioned indexes are unsupported"); reference tests pin that boundary. The primary key of a partitioned table works. | `JDBC-ENGINE-05-PARTITIONED-INDEX` | no (the fixture does not use it) |

No existing catalog part covers 2 to 5 (titles of the catalog searched on 2026-11-11;
`LIBSPQ-COMPAT-01-DRIVER-SQL` names none of them). The engine parts are priority P2 and
are no prerequisite of any `JDBC-DBMD` part. They matter to applications that run pgjdbc
or another driver against the stuntgres wire server, and to schema scripts.

### 7.2 Observed differences without a part

| Observation | Consequence here |
|---|---|
| `pg_type` in `pg_catalog` has 455 rows on the stuntgres server and 473 on PostgreSQL (missing: three multirange types, `gtsvector`, `pg_snapshot`, `txid_snapshot`, two `pg_brin_*` summary types, row types of two statistics views, and their array types). | `getTypeInfo` is tested on a named subset of rows (5.10). |
| `pg_database` on the stuntgres server lists no `postgres` or `template1`. | `getCatalogs` is tested for the own database and the order only (5.3). |
| `information_schema.columns` and `information_schema.tables` fail with `foreign-data wrapper "md_fdw" has no handler` once the fixture's foreign table exists. | Own SQL reads `pg_catalog` only (7.3). Not needed by this family; reported to the coordinator as a candidate engine part. |
| `pg_get_expr(relpartbound, oid)` writes `DATE '2025-01-01'` where PostgreSQL writes `'2025-01-01'`. | No metadata column shows partition bounds. Reported as a candidate engine part. |
| Temporary schema numbers differ. | Masked (4.4). |
| `pg_get_keywords()`: only the words starting with `a` were compared (equal). | 3.6: a difference of the full list is an R8 report of DBMD-05. |

Everything else of the 28 building-block queries (`probe/blocks.sql`: `pg_get_expr` of
defaults, `pg_get_indexdef` per column, `pg_get_constraintdef`, `generate_subscripts` and
`unnest WITH ORDINALITY` over `conkey` / `confkey`, `indkey` subscripts, `pg_depend` from
a foreign key to its index, routine argument arrays, `aclexplode`, `has_*_privilege`,
`current_schemas`, `pg_get_serial_sequence`, descriptions, relkinds, `format_type`,
settings, `LIKE` with escape, `row_number`, inheritance, `pg_roles`) gave the same output
on both servers **(observed: `probe/blocks.diff`)**.

### 7.3 Rules that follow for own SQL

1. Read `pg_catalog` tables and functions only, schema-qualified (`pg_catalog.pg_class`);
   never an `information_schema` view or function.
2. Do not call `information_schema._pg_expandarray` or `pg_my_temp_schema()`. Key
   positions: 5.1 rule 12. The session's temporary schema: the first element of
   `current_schemas(true)` when it starts with `pg_temp_` (answered alike by both servers,
   block B16).
3. Write joins as `JOIN ... ON`, never as a comma-separated `FROM` list with the join
   conditions in `WHERE`; prefer `LATERAL` set-returning calls in `FROM` over
   set-returning functions in the select list.
4. Prefer plain joins over rarely used constructs when both give the same rows; never
   change the rows to suit the engine.
5. A statement of this family that fails or answers differently on the stuntgres server,
   or runs longer than a second on the fixture, is an engine gap: report it (R8) with the
   smallest statement that shows it; do not work around it and do not edit the engine.
6. Every test run of this family is wrapped in a memory cap (8.2).

## 8. Steps

### 8.1 Parts

Every part lists `JDBC-DESIGN-METADATA` as a prerequisite; the table shows the others.
"Size" is code + tests in lines, hand-written (expectation files and the fixture SQL are
data and not counted); "gen." counts generated lines. IDs are prefixed `JDBC-`. All
`DBMD` parts are priority P1, kind implementation.

| Part | Outcome | Files | Prerequisites | Size | gen. |
|---|---|---|---|---|---|
| `DBMD-00-SKELETON` | Skeleton of DatabaseMetaData: generated root, chain stubs, PgDatabaseMetaData, Connection.getMetaData | `jdbc/DatabaseMetaDataRoot.java` (generated), `jdbc/PgDatabaseMetaData.java`, `jdbc/ConnMetaData.java` (replaces the stub of CONN-00-SKELETON), `test: jdbc/DbmdSkeletonTest.java`, `test: jdbc/DbmdRejectedTest.java`; stubs `jdbc/<Class>.java` with their owners: `DbmdCore` (DBMD-01-BASE), `DbmdSqlFeatures` (DBMD-02A-SQL-FEATURES), `DbmdTxnFeatures` (DBMD-02B-TXN-FEATURES), `DbmdLimits` (DBMD-03-LIMITS), `DbmdIdentifiers` (DBMD-04-IDENTIFIERS), `DbmdKeywords` (DBMD-05-KEYWORDS), `DbmdSchemas` (DBMD-11-SCHEMAS), `DbmdTables` (DBMD-12-TABLES), `DbmdColumns` (DBMD-13-COLUMNS), `DbmdKeys` (DBMD-14-PRIMARY-KEYS), `DbmdForeignKeys` (DBMD-15-FOREIGN-KEYS), `DbmdIndexes` (DBMD-16-INDEXES), `DbmdTypeInfo` (DBMD-17-TYPE-INFO), `DbmdProcedures` (DBMD-18A-PROCEDURES), `DbmdProcedureColumns` (DBMD-18B-PROCEDURE-COLUMNS), `DbmdFunctions` (DBMD-19A-FUNCTIONS), `DbmdFunctionColumns` (DBMD-19B-FUNCTION-COLUMNS), `DbmdTablePrivileges` (DBMD-20A-TABLE-PRIVILEGES), `DbmdColumnPrivileges` (DBMD-20B-COLUMN-PRIVILEGES), `DbmdUdt` (DBMD-21-UDT) | CONN-00-SKELETON, CONN-01A-OPEN-CLOSE, DRIVER-03-DRIVER, TEST-03-JDBC-SUPPORT | 230 + 130 | 900 |
| `DBMD-01-BASE` | DbmdCore: query and in-memory helpers, identification answers, unwrap | `jdbc/DbmdCore.java` (replaces the stub of DBMD-00-SKELETON), `test: jdbc/DbmdIdentityTest.java`, `test: jdbc/DbmdOwnIdentityTest.java`, `test: jdbc/DbmdHelpersTest.java` | DBMD-00-SKELETON, CONN-07-FACTORIES, PSTMT-02-BASIC-SETTERS, RS-00B-DATA, RS-01B-SCROLL, RS-03-TEXT-GETTERS, RS-04-NUMBER-GETTERS, RSMD-01-BASIC, CONN-05-SESSION, CONN-03-TXN-OPTIONS, STMT-03-LIMITS | 210 + 200 |  |
| `DBMD-02A-SQL-FEATURES` | DbmdSqlFeatures: the SQL grammar answers of DatabaseMetaData | `jdbc/DbmdSqlFeatures.java` (replaces the stub of DBMD-00-SKELETON), `test: jdbc/DbmdSqlFeaturesTest.java` | DBMD-00-SKELETON | 190 + 100 |  |
| `DBMD-02B-TXN-FEATURES` | DbmdTxnFeatures: transaction, cursor, result-set and statement answers | `jdbc/DbmdTxnFeatures.java` (replaces the stub of DBMD-00-SKELETON), `test: jdbc/DbmdTxnFeaturesTest.java` | DBMD-00-SKELETON | 230 + 150 |  |
| `DBMD-03-LIMITS` | DbmdLimits: the getMax answers and the default transaction isolation level | `jdbc/DbmdLimits.java` (replaces the stub of DBMD-00-SKELETON), `test: jdbc/DbmdLimitsTest.java` | DBMD-01-BASE | 140 + 120 |  |
| `DBMD-04-IDENTIFIERS` | DbmdIdentifiers: identifier rules, terms, schema and catalog use | `jdbc/DbmdIdentifiers.java` (replaces the stub of DBMD-00-SKELETON), `test: jdbc/DbmdIdentifiersTest.java` | DBMD-00-SKELETON | 150 + 90 |  |
| `DBMD-05-KEYWORDS` | DbmdKeywords: getSQLKeywords and the four function lists | `jdbc/DbmdKeywords.java` (replaces the stub of DBMD-00-SKELETON), `test: jdbc/DbmdKeywordsTest.java` | DBMD-01-BASE, CONN-05-SESSION | 110 + 130 |  |
| `DBMD-10-FIXTURE` | Metadata fixture: schema resource, MetaFixture loader, MetaRows comparison | `res: testing/dbmd-fixture.sql`, `test: testing/MetaFixture.java` (new, test support), `test: testing/MetaRows.java` (new, test support), `test: jdbc/MetaFixtureTest.java`, `test: testing/MetaRowsTest.java`, `res: jdbc/dbmd/fixture.txt` | TEST-03-JDBC-SUPPORT, DRIVER-03-DRIVER, STMT-01C-QUERY-UPDATE, RS-03-TEXT-GETTERS, RSMD-01-BASIC | 0 + 380 |  |
| `DBMD-11-SCHEMAS` | DbmdSchemas: getSchemas, getCatalogs, getTableTypes | `jdbc/DbmdSchemas.java` (replaces the stub of DBMD-00-SKELETON), `test: jdbc/DbmdSchemasTest.java`, `res: jdbc/dbmd/schemas.txt` | DBMD-01-BASE, DBMD-10-FIXTURE | 160 + 170 |  |
| `DBMD-12-TABLES` | DbmdTables: getTables | `jdbc/DbmdTables.java` (replaces the stub of DBMD-00-SKELETON), `test: jdbc/DbmdTablesTest.java`, `res: jdbc/dbmd/tables.txt` | DBMD-11-SCHEMAS, DBMD-10-FIXTURE | 200 + 180 |  |
| `DBMD-13-COLUMNS` | DbmdColumns: getColumns | `jdbc/DbmdColumns.java` (replaces the stub of DBMD-00-SKELETON), `test: jdbc/DbmdColumnsTest.java`, `res: jdbc/dbmd/columns.txt` | DBMD-01-BASE, DBMD-10-FIXTURE, TYPE-02-TABLE, TYPE-04-LOOKUP | 240 + 170 |  |
| `DBMD-14-PRIMARY-KEYS` | DbmdKeys: getPrimaryKeys, getBestRowIdentifier, getVersionColumns | `jdbc/DbmdKeys.java` (replaces the stub of DBMD-00-SKELETON), `test: jdbc/DbmdKeysTest.java`, `res: jdbc/dbmd/keys.txt` | DBMD-01-BASE, DBMD-10-FIXTURE, TYPE-02-TABLE, TYPE-04-LOOKUP | 190 + 180 |  |
| `DBMD-15-FOREIGN-KEYS` | DbmdForeignKeys: getImportedKeys, getExportedKeys, getCrossReference | `jdbc/DbmdForeignKeys.java` (replaces the stub of DBMD-00-SKELETON), `test: jdbc/DbmdForeignKeysTest.java`, `res: jdbc/dbmd/foreignkeys.txt` | DBMD-01-BASE, DBMD-10-FIXTURE | 200 + 190 |  |
| `DBMD-16-INDEXES` | DbmdIndexes: getIndexInfo | `jdbc/DbmdIndexes.java` (replaces the stub of DBMD-00-SKELETON), `test: jdbc/DbmdIndexesTest.java`, `res: jdbc/dbmd/indexes.txt` | DBMD-01-BASE, DBMD-10-FIXTURE | 180 + 170 |  |
| `DBMD-17-TYPE-INFO` | DbmdTypeInfo: getTypeInfo | `jdbc/DbmdTypeInfo.java` (replaces the stub of DBMD-00-SKELETON), `test: jdbc/DbmdTypeInfoTest.java`, `res: jdbc/dbmd/typeinfo.txt` | DBMD-01-BASE, DBMD-10-FIXTURE, TYPE-02-TABLE, TYPE-04-LOOKUP | 180 + 150 |  |
| `DBMD-18A-PROCEDURES` | DbmdProcedures: getProcedures | `jdbc/DbmdProcedures.java` (replaces the stub of DBMD-00-SKELETON), `test: jdbc/DbmdProceduresTest.java`, `res: jdbc/dbmd/procedures.txt` | DBMD-01-BASE, DBMD-10-FIXTURE | 100 + 120 |  |
| `DBMD-18B-PROCEDURE-COLUMNS` | DbmdProcedureColumns: getProcedureColumns | `jdbc/DbmdProcedureColumns.java` (replaces the stub of DBMD-00-SKELETON), `test: jdbc/DbmdProcedureColumnsTest.java`, `res: jdbc/dbmd/procedurecolumns.txt` | DBMD-01-BASE, DBMD-10-FIXTURE, TYPE-02-TABLE, TYPE-04-LOOKUP | 230 + 160 |  |
| `DBMD-19A-FUNCTIONS` | DbmdFunctions: getFunctions | `jdbc/DbmdFunctions.java` (replaces the stub of DBMD-00-SKELETON), `test: jdbc/DbmdFunctionsTest.java`, `res: jdbc/dbmd/functions.txt` | DBMD-01-BASE, DBMD-10-FIXTURE | 110 + 120 |  |
| `DBMD-19B-FUNCTION-COLUMNS` | DbmdFunctionColumns: getFunctionColumns | `jdbc/DbmdFunctionColumns.java` (replaces the stub of DBMD-00-SKELETON), `test: jdbc/DbmdFunctionColumnsTest.java`, `res: jdbc/dbmd/functioncolumns.txt` | DBMD-01-BASE, DBMD-10-FIXTURE, TYPE-02-TABLE, TYPE-04-LOOKUP | 220 + 160 |  |
| `DBMD-20A-TABLE-PRIVILEGES` | DbmdTablePrivileges: getTablePrivileges | `jdbc/DbmdTablePrivileges.java` (replaces the stub of DBMD-00-SKELETON), `test: jdbc/DbmdTablePrivilegesTest.java`, `res: jdbc/dbmd/tableprivileges.txt` | DBMD-01-BASE, DBMD-10-FIXTURE | 190 + 160 |  |
| `DBMD-20B-COLUMN-PRIVILEGES` | DbmdColumnPrivileges: getColumnPrivileges | `jdbc/DbmdColumnPrivileges.java` (replaces the stub of DBMD-00-SKELETON), `test: jdbc/DbmdColumnPrivilegesTest.java`, `res: jdbc/dbmd/columnprivileges.txt` | DBMD-01-BASE, DBMD-10-FIXTURE | 190 + 150 |  |
| `DBMD-21-UDT` | DbmdUdt: getUDTs and getClientInfoProperties | `jdbc/DbmdUdt.java` (replaces the stub of DBMD-00-SKELETON), `test: jdbc/DbmdUdtTest.java`, `res: jdbc/dbmd/udt.txt` | DBMD-01-BASE, DBMD-10-FIXTURE, TYPE-02-TABLE, TYPE-04-LOOKUP | 150 + 160 |  |
| **22 parts** | | | | **3800 + 3540** | **900** |

Engine parts (kind implementation, priority P2, prerequisite `JDBC-DESIGN-METADATA` only;
the only parts of this design that may edit `internal/` and `reference/`):

| Part | Outcome | Gap (7.1) | Size |
|---|---|---|---|
| `ENGINE-01-IMPORTED-KEYS-RUNAWAY` | added by the coordinating session, not by this design | 1 | – |
| `ENGINE-02-PG-EXPANDARRAY` | Engine: information_schema._pg_expandarray(anyarray) as a callable function | 2 | about 120 + 150 lines |
| `ENGINE-03-MY-TEMP-SCHEMA` | Engine: pg_my_temp_schema() is not found through the wire server | 3 | about 60 + 120 lines |
| `ENGINE-04-DROP-ROLE-IF-EXISTS` | Engine: DROP ROLE IF EXISTS (also DROP USER and DROP GROUP) | 4 | about 80 + 150 lines |
| `ENGINE-05-PARTITIONED-INDEX` | Engine: CREATE INDEX on a partitioned table (needs a stronger model) | 5 | unknown |

Changes against the list of the part record: `DBMD-00-SKELETON` is new (the chain needs
stubs); the constant answers are five parts (`02A`, `02B`, `03`, `04`, `05`) and only
`03` and `05` need `DBMD-01`; `DBMD-10-FIXTURE` depends on core parts only; procedures,
functions and privileges are split in two for the size limit; `getSuperTypes`,
`getSuperTables`, `getAttributes` and `getPseudoColumns` are rejected (DBMD-00), so
`DBMD-21` is `getUDTs` and `getClientInfoProperties`.

### 8.2 Rules common to the parts

- **JDBC-CORE 13.2 applies**: test tags, expected values (R11), SQLState assertions with
  `JdbcAsserts`, order inside a chain, modes to pass before merge.
- **Result-set tests** are `@DatabaseTest @ReferenceToo`, use only `java.sql`, load the
  fixture in `@BeforeAll` (4.2) and assert through `MetaRows.assertSection` (4.3). The
  part establishes every section of its expectation file in reference mode first; the
  same file must then pass in oracle and server mode.
- **A failure in server mode with a pass in oracle mode** is an engine gap (R8): report
  the smallest statement; do not change the statement to hide it unless rule 7.3 already
  forbids the construct.
- **Memory cap.** Every `make -C bindings/jdbc check` run of a part of this family, in
  any mode, is started as `systemd-run --user --scope -p MemoryMax=16G -p MemorySwapMax=0
  timeout 900 make -C bindings/jdbc check MODE=<mode> ...`. A run killed by the cap or
  the timeout is a stop condition (R8): report the last statement sent, do not retry
  without the cap.
- **pgjdbc is read for behavior only** at the lines the entries name; no statement text,
  no comment and no test is copied (R3).
- **The consumer document `docs/JDBC.md`** still lists `DatabaseMetaData` as "later"; no
  part of this family edits it (it is updated by the documentation part of
  JDBC-DESIGN-FEATURES).

## 9. Evidence and what was not run

- An earlier run of this design recorded probe outputs under
  `.cswd/tmp/JDBC-DESIGN-METADATA/probe/` and was then interrupted by the host running out
  of memory (7.1 gap 1). Read for this document, never re-run: `pgjdbc-pg.out` (every
  result-set method and some constants through pgjdbc 42.7.13 on PostgreSQL 18.6 for the
  probe fixture), `fixture.sql` with its two outputs, `blocks.sql` with its two outputs
  and `blocks.diff`, `reduce.sql` with its two outputs. `pgjdbc-srv.out` (pgjdbc against
  the stuntgres server) is incomplete and was not used.
- By the owner's instruction nothing was run for this document: no database, no server,
  no driver, no build. Waived from the part record: experiments 1 to 6, the TSV of
  constant answers, the literal own SQL statement per method, the recorded rows, rule D7
  (validation slice) and the acceptance sentences that depend on them.
- Left to the implementing parts in reference mode (R11): every constant answer, every
  expectation-file section, the per-method questions marked "establish" in section 5,
  and whether each own statement runs on the stuntgres server.
