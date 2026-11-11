# JDBC-TYPES: SQL text processing and type conversion of the JDBC driver

Design for catalog part JDBC-DESIGN-TYPES. It specifies the connection-free core of the
JDBC driver in `bindings/jdbc`: how statement text is scanned, the JDBC escapes, the table
of PostgreSQL types, the text codecs, the conversions behind the `ResultSet` getters and
the `PreparedStatement` setters, array text and the mirrored value classes. It builds on
[JDBC-NATIVE.md](JDBC-NATIVE.md) (build, shim, `org.stuntgres.pq`, test harness); "NATIVE
n.n" means a section of that document.

This document is a specification. The code is not in it: a **draft** of every class and
test of this layer is in `bindings/jdbc/draft/types/` (see its `DRAFT.md`), together with
the recorded tables. Each part of section 13 promotes its draft files to `bindings/jdbc/src`
and makes them pass. Where the draft and this document disagree, this document and the
recorded tables win.

Statements marked **(recorded)** are observations of pgjdbc 42.7.13 against PostgreSQL 18.6
(pinned image) on JDK 17.0.13, made on 2026-11-11 on the tree at commit `e31499235d`;
**(draft)** means the draft reproduced the recorded table in a scratch build (section 14).
The owner's decisions J1 to J12 and the architecture points B1 to B15 of the approved plan
are fixed and not repeated. pgjdbc and OpenJDK were read and run only to learn observable
behavior; the draft's code, names of non-public helpers, messages and tests were written
for this design.

## 1. Purpose, layering, decisions, layout

```
org.stuntgres.jdbc.*  (JDBC-DESIGN-CORE)      Connection, statements, ResultSet, Array, metadata
        │ calls static functions; passes a ConversionContext and a TypeLookup
org.stuntgres.core    (this design)           SqlScanner, NativeSql, JdbcEscapes, CallEscape,
        │                                     TypeTable, text codecs, *Getters, *Setters, ArrayText
org.stuntgres.util, org.stuntgres.geometric   PSQLState, PSQLException (first form), PGobject, PGInterval,
        │                                     PGmoney, PGTime, PGTimestamp, PGpoint ... PGpolygon
org.stuntgres.pq      (JDBC-NATIVE)           PqResult gives column text, type OID and type modifier;
                                              execParams takes OIDs, values and formats
```

Nothing in this layer uses native code, `java.sql.Connection` or the JVM's default time
zone: every function takes what it needs as arguments (5.3, 7.1, 8.1). All of it is tested
in unit mode (NATIVE 10.2).

### 1.1 Fixed points of the part and what the experiments did to them

| # | Fixed point | Result |
|---|---|---|
| F1 | Packages `org.stuntgres.core`, `.util`, `.geometric`; explicit context instead of a connection. | Kept. The context is `ConversionContext` (default time zone, `TypeLookup`, the properties `stringtype` and `convertBooleanToNumeric`). The session time zone is **not** an input: in text format every value that depends on it carries its offset (11). |
| F2 | One lexical scanner with strings, identifiers, dollar quotes, comments, numbers and operators. | **Changed:** `SqlScanner` has no token for numbers and operators. pgjdbc's result does not depend on them (recorded cases `number-operator-*`), and a scanner that treated `1e5` or `?|` as one token could not reproduce it. A question mark is a placeholder wherever it stands outside quotes and comments (2.2). |
| F3 | Placeholder rewriting, splitting, classification and RETURNING detection as separate steps. | **Changed:** they are one pass in one class, `NativeSql` (3). pgjdbc's results depend on the order in which one pass meets a word, a semicolon and a parenthesis (3.3, rules K4 to K7); separate passes cannot reproduce them. The parts SQL-02 and SQL-03 of the record were cut differently (13). |
| F4 | Reproduce pgjdbc where its scanner mishandles a construct, unless that breaks a statement pgjdbc executes correctly. | Reproduced completely, quirks included (1.2 decision 3). No case was found where reproducing pgjdbc breaks a statement that pgjdbc executes correctly. |
| F5 | Escapes with the complete function table, from `Connection.nativeSQL`. | Kept (4). 37 functions are translated, every other name is passed through. |
| F6 | Type table for every type pgjdbc knows by name; lookup interface for the rest. | Kept and widened: the table holds 59 types (5.1). pgjdbc knows 29 of them by name and reads the others from the catalog; the answers recorded for them are constants of PostgreSQL, so the driver saves the round trip. |
| F7 | Text codecs of every type pgjdbc maps. | Kept (6). pgjdbc reads most types with the same few routines whatever the column type is; the codecs follow that. |
| F8 | Getter matrix, recorded per cell, with an algorithm per getter. | Kept: 680 values x 84 getters x 2 JVM zones x 3 session zones (7.1). The draft reproduces every cell of the getters this layer implements **(draft)**. |
| F9 | Setter matrix: OID and text per setter and source class. | Kept: 3299 cases (8.9); the OID is recorded through `pg_typeof`, the text through the literal `PreparedStatement.toString` shows. The draft reproduces every case **(draft)**. The three server contexts of the record (bare parameter, `COALESCE` expression, INSERT) were recorded and stay in the experiment directory (14); they depend on OID and text only. |
| F10 | Arrays: parser, formatter, element classes, `getResultSet` rows. | Kept (9). |
| F11 | Mirrored value classes with pgjdbc's public API. | Kept (10), with two omissions (decision 9). |
| F12 | Time: literal examples in two JVM zones and two session zones. | Kept, three session zones (11). |
| F13 | One class and one test file per part. | Kept; 35 parts (13). |

### 1.2 Decision records

1. **NUL character in a string parameter** (hand-over point 3 of JDBC-DESIGN-NATIVE). pgjdbc
   sends the NUL and the server answers `22021` (`invalid byte sequence for encoding "UTF8":
   0x00`) **(recorded: `setString(a\u0000b)` in `set.tsv`)**. A text-format parameter of
   libpq ends at the first NUL. Decision: the setter never rejects the value;
   `ParamValue.format()` returns 1 (binary) for a text that holds a NUL, so that the bytes
   reach the server and the server rejects them (8.1). For a parameter of OID 25, 1043 or 0
   in a text context JDBC-NATIVE measured `22021` (its experiment 7). **Not measured:** OID 0
   with a NUL where the server infers a non-character type; the server then reads the bytes
   with that type's binary input and may answer another SQLState. JDBC-DESIGN-CORE carries a
   round-trip test for the three contexts (12).
2. **Unchecked exceptions of pgjdbc become `PSQLException`.** Where pgjdbc fails with an
   unchecked exception, the driver raises a `PSQLException` with a SQLState; the recorded
   tables keep pgjdbc's class and the tests map it (`Matrix.expected`, `SettersTest`):

   | Where | pgjdbc | Driver |
   |---|---|---|
   | `getDate` of text that is no date (2238 cells) | `ArrayIndexOutOfBoundsException` | `22007` |
   | `getObject(c, UUID.class)` or a value class for a column of another type | `ClassCastException` | `22023` |
   | `getObject(c, OffsetDateTime.class)` of timetz `24:00:00+00` | `DateTimeException` | `22007` |
   | `setObject(x, Types.DATE)` with a value whose text is no date (38 cases) | `ArrayIndexOutOfBoundsException` | `22007` |
   | value class constructors and `setValue` with malformed text (8 recorded cases) | `IndexOutOfBoundsException`, `NumberFormatException`, `StringIndexOutOfBoundsException` | `42821` (geometric), `42820` (PGmoney, PGInterval) |
   | call escape whose text ends inside a quoted backslash | `StringIndexOutOfBoundsException` | `2F003` |
   | generated-key column name that is empty | `StringIndexOutOfBoundsException` | the name is quoted as `""`; the server rejects it |
   | native `$1` in the text of a statement without `?` | `ArrayIndexOutOfBoundsException` at execute | parameter count 0; the server answers `08P01` (CORE) |
3. **Scanner quirks are reproduced.** The statement parser and the escape processor give
   pgjdbc's result for every input, including inputs pgjdbc mishandles (rules marked
   *quirk* in 2.2, 3 and 4.2). Reason: the behavior target is pgjdbc (J1), and an identical
   result is the only property that can be checked mechanically: the draft agrees with
   pgjdbc's parser on all recorded cases and on 12 million random texts (14).
4. **Message texts are the driver's own** (design rule D9); exception class and SQLState
   are pgjdbc's. Section 7.9 lists the texts. Tests that also run in reference mode compare
   class and SQLState only.
5. **Constructors call no overridable method.** javac 21 with `-Xlint:all` reports
   `this-escape` for a public constructor of a non-final class that calls an overridable
   method; with `-Werror` this fails the build on JDK 21 **(measured)**. The value classes
   therefore parse in a private method that the constructor and `setValue` both call. A
   subclass that overrides `setValue` is not called from the constructor (pgjdbc calls it).
6. **`getObject(c, InetAddress.class)` never looks a name up.** pgjdbc passes the column text
   to `InetAddress.getByName`, which resolves host names. The driver reads numeric addresses
   only and raises `22023` for other text.
7. **A Calendar argument is not changed.** pgjdbc's `getDate(c, cal)` clears and sets the
   caller's Calendar; the driver works on a copy. Without a Calendar the driver uses a
   `GregorianCalendar` in the default zone (pgjdbc: `Calendar.getInstance()`, which is not
   Gregorian in some locales).
8. **Array parameters are written as text with every element quoted** (9.3). pgjdbc sends
   Java arrays of primitives and wrappers in binary; what it would send as text is not
   observable. The server reads both to the same value.
9. **Not mirrored:** `PGpoint.setLocation(java.awt.Point)` (it would make the driver depend
   on the module `java.desktop`); the interface `PGBinaryObject` and its methods on `PGpoint`
   and `PGbox` (binary transfer is not used, plan B6); `PGtokenizer`, `PGbytea` and
   `HStoreConverter` (helpers of pgjdbc's own classes; the driver has `GeometryText`,
   `ByteaText`, `HstoreText`).
10. **Batch rewriting is not designed here.** pgjdbc's parser also finds the VALUES list of
    an INSERT for the property `reWriteBatchedInserts`. `NativeSql` does not report it;
    JDBC-DESIGN-CORE decides whether the property is accepted without effect (plan B8).
11. **The recorded tables live once**, in the test resources of the driver (first under the
    draft directory), not next to this document (owner's instruction of 2026-11-11).
12. **Floating-point values in the tables** are written with `Float.toHexString` and
    `Double.toHexString`: `Float.toString` differs between JDK 17 and 21 **(measured)**.
13. **`getBoolean`, `getInt` and the other typed getters ignore the column type** almost
    everywhere, as pgjdbc does: they parse the column text (7.2 to 7.5). The type matters
    for `getObject` (7.7, 7.8) and in the few places the rules name.

### 1.3 Files and owner parts

Every draft file has one owner part, which moves it to the same path under
`bindings/jdbc/src`.

| Draft file (under `bindings/jdbc/draft/types/`) | Lines | Owner part |
|---|---|---|
| `src/main/java/org/stuntgres/util/PSQLState.java` | 94 | TYPE-00A-ERRORS |
| `src/main/java/org/stuntgres/util/PSQLException.java` | 33 | TYPE-00A-ERRORS |
| `src/main/java/org/stuntgres/core/ConversionErrors.java` | 53 | TYPE-00A-ERRORS |
| `src/main/java/org/stuntgres/util/PGobject.java` | 95 | PGOBJ-01A-OBJECT |
| `src/main/java/org/stuntgres/util/PGmoney.java` | 109 | PGOBJ-01A-OBJECT |
| `src/main/java/org/stuntgres/util/PGTime.java` | 80 | PGOBJ-01A-OBJECT |
| `src/main/java/org/stuntgres/util/PGTimestamp.java` | 80 | PGOBJ-01A-OBJECT |
| `src/main/java/org/stuntgres/util/PGInterval.java` | 456 | PGOBJ-01B-INTERVAL |
| `src/test/java/org/stuntgres/core/TestData.java` | 120 | TYPE-00B-TESTDATA |
| `src/test/java/org/stuntgres/core/Repr.java` | 142 | TYPE-00B-TESTDATA |
| `src/test/java/org/stuntgres/util/ValueClassesTest.java` | 179 | PGOBJ-01C-TEST |
| `src/main/java/org/stuntgres/geometric/GeometryText.java` | 90 | PGOBJ-02A-POINT |
| `src/main/java/org/stuntgres/geometric/PGpoint.java` | 162 | PGOBJ-02A-POINT |
| `src/main/java/org/stuntgres/geometric/PGlseg.java` | 115 | PGOBJ-02A-POINT |
| `src/main/java/org/stuntgres/geometric/PGbox.java` | 119 | PGOBJ-02B-SHAPES |
| `src/main/java/org/stuntgres/geometric/PGcircle.java` | 115 | PGOBJ-02B-SHAPES |
| `src/main/java/org/stuntgres/geometric/PGline.java` | 186 | PGOBJ-02B-SHAPES |
| `src/main/java/org/stuntgres/geometric/PGpath.java` | 157 | PGOBJ-02C-PATHS |
| `src/main/java/org/stuntgres/geometric/PGpolygon.java` | 117 | PGOBJ-02C-PATHS |
| `src/test/java/org/stuntgres/geometric/GeometricClassesTest.java` | 199 | PGOBJ-02C-PATHS |
| `src/main/java/org/stuntgres/core/Oid.java` | 129 | TYPE-01-OIDS |
| `src/main/java/org/stuntgres/core/PgType.java` | 107 | TYPE-01-OIDS |
| `src/main/java/org/stuntgres/core/TypeLookup.java` | 37 | TYPE-01-OIDS |
| `src/main/java/org/stuntgres/core/ConversionContext.java` | 70 | TYPE-01-OIDS |
| `src/main/java/org/stuntgres/core/TypeTable.java` | 355 | TYPE-02-TABLE |
| `src/test/java/org/stuntgres/core/Matrix.java` | 198 | TYPE-03-MATRIX |
| `src/test/java/org/stuntgres/core/TypeTableTest.java` | 72 | TYPE-03-MATRIX |
| `src/main/java/org/stuntgres/core/SqlScanner.java` | 277 | SQL-01-SCANNER |
| `src/test/java/org/stuntgres/core/SqlScannerTest.java` | 87 | SQL-01-SCANNER |
| `src/main/java/org/stuntgres/core/CommandKind.java` | 9 | SQL-02-KINDS |
| `src/main/java/org/stuntgres/core/NativeStatement.java` | 63 | SQL-02-KINDS |
| `src/main/java/org/stuntgres/core/NativeSql.java` | 288 | SQL-03-STATEMENTS |
| `src/test/java/org/stuntgres/core/NativeSqlTest.java` | 108 | SQL-03-STATEMENTS |
| `src/main/java/org/stuntgres/core/EscapeFunctions.java` | 188 | SQL-04-FUNCTIONS |
| `src/main/java/org/stuntgres/core/JdbcEscapes.java` | 206 | SQL-05-ESCAPES |
| `src/test/java/org/stuntgres/core/JdbcEscapesTest.java` | 88 | SQL-05-ESCAPES |
| `src/test/java/org/stuntgres/core/EscapeFunctionsTest.java` | 52 | SQL-05-ESCAPES |
| `src/main/java/org/stuntgres/core/CallEscape.java` | 162 | SQL-06-CALL |
| `src/test/java/org/stuntgres/core/CallEscapeTest.java` | 61 | SQL-06-CALL |
| `src/main/java/org/stuntgres/core/BooleanText.java` | 64 | CODEC-01-NUMBER-BYTEA |
| `src/main/java/org/stuntgres/core/NumberText.java` | 169 | CODEC-01-NUMBER-BYTEA |
| `src/main/java/org/stuntgres/core/ByteaText.java` | 71 | CODEC-01-NUMBER-BYTEA |
| `src/main/java/org/stuntgres/core/DateTimeText.java` | 258 | CODEC-02-DATETIME |
| `src/main/java/org/stuntgres/core/HstoreText.java` | 92 | CODEC-03-HSTORE |
| `src/main/java/org/stuntgres/core/TextGetters.java` | 72 | GET-01-TEXT |
| `src/test/java/org/stuntgres/core/TextGettersTest.java` | 57 | GET-01-TEXT |
| `src/main/java/org/stuntgres/core/IntegerGetters.java` | 123 | GET-02-INTEGER |
| `src/test/java/org/stuntgres/core/IntegerGettersTest.java` | 81 | GET-02-INTEGER |
| `src/main/java/org/stuntgres/core/DecimalGetters.java` | 90 | GET-03-DECIMAL |
| `src/test/java/org/stuntgres/core/DecimalGettersTest.java` | 64 | GET-03-DECIMAL |
| `src/main/java/org/stuntgres/core/DateTimeGetters.java` | 156 | GET-04-DATETIME |
| `src/test/java/org/stuntgres/core/DateTimeGettersTest.java` | 86 | GET-04-DATETIME |
| `src/main/java/org/stuntgres/core/JavaTimeGetters.java` | 169 | GET-05-JAVATIME |
| `src/test/java/org/stuntgres/core/JavaTimeGettersTest.java` | 79 | GET-05-JAVATIME |
| `src/main/java/org/stuntgres/core/ArrayText.java` | 131 | ARRAY-01-PARSE |
| `src/main/java/org/stuntgres/core/ObjectGetters.java` | 284 | GET-06-OBJECT |
| `src/test/java/org/stuntgres/core/ObjectGettersTest.java` | 115 | GET-06-OBJECT |
| `src/main/java/org/stuntgres/core/ClassGetters.java` | 175 | GET-07-CLASS |
| `src/test/java/org/stuntgres/core/ClassGettersTest.java` | 114 | GET-07-CLASS |
| `src/main/java/org/stuntgres/core/ParamValue.java` | 61 | SET-01-BASIC |
| `src/main/java/org/stuntgres/core/BasicSetters.java` | 184 | SET-01-BASIC |
| `src/main/java/org/stuntgres/core/DateTimeSetters.java` | 277 | SET-02-DATETIME |
| `src/main/java/org/stuntgres/core/StreamSetters.java` | 165 | SET-03-STREAM |
| `src/main/java/org/stuntgres/core/ArrayFormat.java` | 105 | ARRAY-02-FORMAT |
| `src/test/java/org/stuntgres/core/ArrayFormatTest.java` | 72 | ARRAY-02-FORMAT |
| `src/main/java/org/stuntgres/core/ObjectSetters.java` | 336 | SET-04-OBJECT |
| `src/test/java/org/stuntgres/core/SetterCalls.java` | 236 | SET-05-TABLE |
| `src/test/java/org/stuntgres/core/SetterCases.java` | 447 | SET-05-TABLE |
| `src/test/java/org/stuntgres/core/SettersTest.java` | 88 | SET-05-TABLE |

### 1.4 The recorded tables

Tab-separated files; a line that starts with `#` is the header. A cell is escaped with
backslash-t, -n, -r, a doubled backslash and backslash-u with four hex digits; the cell
`\N` is SQL NULL. A Java value is written as `Class:text`, for example `Integer:5`,
`Timestamp:1577959872123/123456000` (milliseconds, nanoseconds), `Double:0x1.8p0`,
`byte[]:00ff`, `Integer[Integer:1,null]`; an exception as `!class|SQLState`. `TestData` and
`Repr` (TYPE-00B) read and write this form.

| Table (under `bindings/jdbc/draft/types/src/test/resources/org/stuntgres/core/`) | Rows | Content | Owner part |
|---|---|---|---|
| `values.tsv` | 680 | The 680 recorded values. Columns: id; family; declared type; type OID; type modifier (as a result column reports them); SQL expression; the column text in session zone UTC, America/New_York, Asia/Kolkata (`\N` is SQL NULL). | TYPE-03-MATRIX |
| `get-string.tsv` | 730 | Getter table (7.1): getString, getNString, getAsciiStream, getUnicodeStream, getCharacterStream, getNCharacterStream, getObject(String). | GET-01-TEXT |
| `get-integer.tsv` | 680 | Getter table: getBoolean, getByte, getShort, getInt, getLong, getObject(Boolean, Byte, Short, Integer, Long, BigInteger). | GET-02-INTEGER |
| `get-decimal.tsv` | 680 | Getter table: getFloat, getDouble, getBigDecimal, getBigDecimal with scale 2 and 0, getObject(Float, Double, BigDecimal, Number). | GET-03-DECIMAL |
| `get-bytes.tsv` | 730 | Getter table: getBytes, getBinaryStream, getObject(byte[], InputStream, Reader). | GET-01-TEXT |
| `get-datetime.tsv` | 849 | Getter table: getDate, getTime, getTimestamp, each without and with the Calendar Australia/Adelaide; getObject(java.sql.Date as `Date`, Time, Timestamp, java.util.Date, Calendar, GregorianCalendar). | GET-04-DATETIME |
| `get-javatime.tsv` | 680 | Getter table: getObject(LocalDate, LocalTime, LocalDateTime, OffsetDateTime, OffsetTime, ZonedDateTime, Instant, Duration). | GET-05-JAVATIME |
| `get-object.tsv` | 795 | Getter table: getObject, getObject with a non-empty type map, getObject(Object, Character, int, Map, URL, String[], UUID, InetAddress, PGobject and the nine value classes). | GET-06-OBJECT |
| `get-other.tsv` | 734 | Getter table of the getters this layer does not implement (7.10): getArray, getObject(Array), getURL, getRef, getRowId, getNClob, getBlob, getClob, getSQLXML, getObject(Blob, Clob, NClob, SQLXML). Hand-over to JDBC-DESIGN-CORE and -FEATURES. | TYPE-99-DRAFT-REMOVE |
| `get-errors.tsv` | 290 | Every distinct error of the getter tables: getter; exception class; SQLState; message pattern of pgjdbc; number of cells; an example value id. | TYPE-99-DRAFT-REMOVE |
| `set.tsv` | 4088 | Setter table (8.9): case id (the call); stringtype (`default`, `unspecified`, `*`); JVM default zone or `*`; parameter OID; parameter text. A rejected value has `!class|SQLState` in the OID column and `setter` or `server` in the text column; a text that starts with `?` was not observable. | SET-05-TABLE |
| `sql-statements.tsv` | 3470 | Statement parser (3.5): id; SQL; variant (`ps`, `st`, `gkall`, `gknames`, `gkraw`); standard_conforming_strings; number of statements; index; native SQL; parameter count; kind; RETURNING flag. One row per resulting statement. | SQL-03-STATEMENTS |
| `sql-escapes.tsv` | 388 | Escape processing (4.2): id; standard_conforming_strings; SQL; the text Connection.nativeSQL returns, or `!SQLState`. | SQL-05-ESCAPES |
| `sql-functions.tsv` | 481 | Function escapes (4.3): id (`name/argument count`, or the interval unit); SQL; nativeSQL text or `!SQLState`. | SQL-05-ESCAPES |
| `sql-call.tsv` | 360 | Call escape (4.4): id; standard_conforming_strings; mode (`select`, `callIfNoReturn`, `call`); SQL; result or `!SQLState`; isFunction. | SQL-06-CALL |
| `types.tsv` | 59 | Type table (5.1): name; OID; array OID; java.sql.Types code; class name; array delimiter; signed; case-sensitive; maximum precision; whether pgjdbc knows the name without a catalog query. | TYPE-03-MATRIX |
| `types-typmod.tsv` | 1080 | Precision, scale and display size (5.2): type name; OID; type modifier; precision; scale; display size. | TYPE-03-MATRIX |
| `types-user.tsv` | 17 | The user-defined types of the recorded values, for the lookup of the tests: name as pgjdbc reports it; OID in the recording database; Types code; class name; array OID; element OID; delimiter. | TYPE-03-MATRIX |
| `types-byname.tsv` | 108 | Name resolution of pgjdbc (5.3): type name as an application writes it; OID from the name; Types code; array OID for the name as an element type; whether the name is known without a catalog query. Hand-over. | TYPE-99-DRAFT-REMOVE |
| `types-rsmd.tsv` | 166 | ResultSetMetaData of a table with one column per type and modifier, as pgjdbc reports it (19 columns, header line). Hand-over. | TYPE-99-DRAFT-REMOVE |
| `types-dbmd.tsv` | 473 | The rows of DatabaseMetaData.getTypeInfo of pgjdbc (header line). Hand-over to JDBC-DESIGN-METADATA. | TYPE-99-DRAFT-REMOVE |
| `value-classes-util.tsv` | 188 | Value classes of org.stuntgres.util (10.4): case id; result text or `!class|SQLState`. | PGOBJ-01C-TEST |
| `value-classes-geometric.tsv` | 177 | Geometric value classes (10.4): case id; result text or `!class|SQLState`. | PGOBJ-02C-PATHS |
| `array-format.tsv` | 51 | Connection.createArrayOf of pgjdbc (9.3): case (type name and Java elements); `Array:` element type name, JDBC type, the elements read back, `toString=` the array text. | TYPE-99-DRAFT-REMOVE |
| `array-access.tsv` | 260 | java.sql.Array of pgjdbc (9.4): SQL expression; call (getArray with index and count, getResultSet, getBaseTypeName, free, ...); result. Hand-over. | TYPE-99-DRAFT-REMOVE |

## 2. Scanner

`SqlScanner` (draft `src/main/java/org/stuntgres/core/SqlScanner.java`, part SQL-01) cuts
statement text into tokens. It never fails: a token without an end runs to the end of the
text and `terminated()` is false. pgjdbc source for behavior: `core/Parser.java`
(`parseSingleQuotes`, `parseDoubleQuotes`, `parseDollarQuotes`, `parseLineComment`,
`parseBlockComment`, the character class helpers).

```java
public final class SqlScanner
    public static final int OTHER;
    public static final int WORD;
    public static final int STRING;
    public static final int QUOTED_IDENTIFIER;
    public static final int DOLLAR_STRING;
    public static final int LINE_COMMENT;
    public static final int BLOCK_COMMENT;
    public SqlScanner(char[] sql, boolean standardConformingStrings);
    public static boolean isWordStart(char c);
    public static boolean isWordPart(char c);
    public static boolean isKeyword(char[] sql, int offset, int length, String keyword);
    public int position();
    public void seek(int newPosition);
    public boolean atEnd();
    public int next();
    public int start();
    public int end();
    public boolean terminated();
```

### 2.1 Tokens

| Kind | Starts with | Ends |
|---|---|---|
| `STRING` | `'` | at the next `'` (a doubled quote therefore gives two STRING tokens). With backslash escapes on, a backslash hides the next character. Backslash escapes are on when the session has `standard_conforming_strings` off, or when the character before the quote is `e` or `E` and the character before that is `"`, a space, tab, line feed, carriage return, form feed or one of ``,()[].;:+-*/%^<>=~!@#&|`?``. |
| `QUOTED_IDENTIFIER` | `"` | at the next `"`. |
| `DOLLAR_STRING` | `$` that has a character after it and no Java identifier part character (letters, digits, `_`, `$`, ...) before it, followed by `$` or by a tag (a word start character, then word part characters) and `$` | at the next occurrence of the same opening text `$tag$`. A `$` that starts no dollar quote is an `OTHER` token. |
| `LINE_COMMENT` | `--` | after the first carriage return or line feed (included), or at the end of the text. |
| `BLOCK_COMMENT` | `/*` | after the `*/` that closes it; `/*` inside opens a nested comment. The search for `*/` and `/*` looks at pairs of characters starting with the `*` of the opening `/*` and skips both characters of a pair it used. |
| `WORD` | a word start character: `Character.isJavaIdentifierStart` and not `$` | before the first character that is not a word part character: `Character.isJavaIdentifierPart` and not `$`. |
| `OTHER` | anything else | one character (white space, digits, operators, `?`, `;`, parentheses, braces). |

`isKeyword(sql, offset, length, keyword)` compares with a lower-case ASCII keyword by
`(c | 0x20) == k`.

### 2.2 Rules that differ from the server's grammar (all reproduce pgjdbc)

- *quirk* `/*/` is a complete block comment (the pair rule above).
- *quirk* A dollar quote does not start after an identifier character, and `$` counts as
  one: in `$a$x$a$$b$y$b$` the second quote is not recognized.
- *quirk* `E'...'` is recognized by the two characters before the quote only: `x.E'\''` has
  backslash escapes, `'x'E'\''` has not.
- `U&'...'`, `B'...'`, `X'...'` and `N'...'` are a word followed by a STRING. Numbers are
  `OTHER` characters followed by words (`1e5` is `1` and the word `e5`).
- A question mark is always an `OTHER` token; what it means is decided in 3.1.

### 2.3 Tests

`SqlScannerTest` (draft, part SQL-01): literal token lists for words, strings in both
string modes, dollar quotes, comments and the helpers. The scanner is also covered by
every row of `sql-statements.tsv` and `sql-escapes.tsv` through its callers.

## 3. Placeholders, statement splitting, command kinds, RETURNING

`NativeSql.parse` (draft `core/NativeSql.java`, part SQL-03) turns the text after escape
processing into the list of statements the server receives; `NativeStatement` and
`CommandKind` (part SQL-02) are its result types. pgjdbc source for behavior:
`core/Parser.java` `parseJdbcSql`, `parseWithCommandType`, `addReturning`;
`core/SqlCommandType.java`.

```java
public final class NativeSql
    public static List<NativeStatement> parse(String sql, boolean standardConformingStrings, boolean withParameters, String[] returningColumns, boolean quoteReturningIdentifiers) throws SQLException;
```
```java
public final class NativeStatement
    public String sql();
    public int parameterCount();
    public CommandKind kind();
    public boolean returning();
    public boolean returnsRows();
```
```java
public enum CommandKind
```

`returnsRows()` is true for a RETURNING flag and for the kinds SELECT and WITH. A
`PreparedStatement` calls `parse` with `withParameters` true, a `Statement` with false;
`returningColumns` is empty unless generated keys are requested (`{"*"}` for
`RETURN_GENERATED_KEYS`, the column names for the name form). The parameter indexes of a
`PreparedStatement` run over all statements of the string: statement k takes the next
`parameterCount()` values and numbers them from `$1` **(recorded: `SELECT ?; SELECT 2;
SELECT ?, ?` has 3 parameters and is sent as `SELECT $1`, ` SELECT 2`, ` SELECT $1, $2`)**.

The parser reads the tokens of `SqlScanner` once, keeping: the text copied so far, the
parameter count, the parenthesis depth, whether the current statement is blank, the kind
and the RETURNING flag of the current statement and of the one before, the number of
statements ended by a semicolon, and the flags *INSERT seen*, *BEGIN seen*, *ATOMIC seen*.

### 3.1 Placeholders

- P1. Quoted tokens and comments are copied unchanged.
- P2. `??` becomes one `?` and is no parameter, also for a plain `Statement`.
- P3. Any other `?` becomes `$n` (n counts from 1 in each statement) when `withParameters`
  is true, else it stays `?`. *quirk*: this holds next to operator characters too, so the
  operators `?`, `?|`, `?&`, `@?` must be written `??`, `??|`, `??&`, `@??`.
- P4. Native `$n` in the text is copied and not counted (`SELECT $1, ?` is sent as `SELECT
  $1, $1` with one parameter).

### 3.2 Splitting

- S1. A semicolon ends a statement when the parenthesis depth is 0 and *ATOMIC seen* is
  false. The depth is the number of `(` minus the number of `)` outside quotes and comments
  and may be negative (*quirk*: after an unbalanced `)` no semicolon splits any more).
- S2. A statement's text is the text between the semicolons, white space included (the
  second statement of `SELECT 1; SELECT 2` is ` SELECT 2`). A piece that holds only white
  space and semicolons is dropped. *quirk*: a piece that holds only a comment is a
  statement (the server answers it as an empty query).
- S3. *ATOMIC seen* is set when, in a statement of kind CREATE, the word `begin` is followed
  by the word `atomic` as the next word; it is never reset: nothing after it is split.

### 3.3 Command kinds

- K1. The kind is BLANK until a word is one of `create`, `alter`, `update`, `delete`,
  `move`, `select`, `with`, `insert` (ASCII case-insensitive): that word sets the kind.
  *quirk*: it is the first such word anywhere in the statement, not the first word:
  `EXPLAIN SELECT 1` is SELECT, `GRANT UPDATE ...` is UPDATE, `(SELECT 1)` is SELECT.
- K2. *quirk* `insert` counts only in the first statement of the string, only once, and only
  when at least one character follows the word; `begin` and `atomic` (S3) also need a
  character after the word.
- K3. For the kind WITH, a later word `update`, `delete`, `insert` or `select` at depth 0
  replaces the kind unless the word `as` follows it (after white space, comments, `-` and
  `/`) and is not followed by an identifier part character (then the word names a query).
- K4. *quirk* A word is applied after the token that follows it: a `)` directly after the
  word has been counted (so the last word inside the parentheses of a WITH query is at
  depth 0), and a semicolon directly after the word has ended the statement (the word
  counts for the next statement).
- K5. The RETURNING flag is set by the word `returning` at depth 0 that is not directly
  followed by `)`. *quirk*: an identifier named `returning` sets it too.
- K6. *quirk* When the text ends directly after a placeholder (`?` or `??`) and earlier
  statements were ended by a semicolon, the last statement gets the kind and RETURNING flag
  of the statement before it if exactly one was ended, and BLANK and false if more were.
- K7. Where the kind matters: generated keys (3.4); `returnsRows()`. JDBC-DESIGN-CORE does
  not use the kind to decide between a result set and an update count: the server's answer
  decides.

### 3.4 RETURNING and generated keys

For kinds INSERT, UPDATE, DELETE and WITH without a RETURNING flag, and a non-empty
`returningColumns`, the statement gets a line feed and `RETURNING ` followed by `*` (one
name that starts with `*`) or by the names joined with `, `; with
`quoteReturningIdentifiers` each name is put in double quotes with `"` doubled. The flag is
then set. A name with a NUL character raises `PSQLException` `22023` (`A column name must
not contain a NUL character.`). Because of K2 only the first INSERT of a string gets the
clause **(recorded: `returning-26`)**.

### 3.5 Recorded cases and tests

`sql-statements.tsv`: 307 statement strings in 11 groups (placeholder, escape-question,
string, estring, ustring, identifier, dollar, comment, number-operator, split, classify,
returning), each with `standard_conforming_strings` on and off and in five variants: `ps`
(parameters, no keys), `st` (no parameters), `gkall` (`returningColumns` `{"*"}`),
`gknames` (`{"id", "Na\"me"}`, quoted), `gkraw` (the same, not quoted). `NativeSqlTest`
(draft) checks every row and has literal cases for P1 to P4, S1, S2, K1, K3 and 3.4. The
texts were also sent through a pgjdbc connection; the server log showed the same statement
texts (14).

## 4. JDBC escapes

`JdbcEscapes.replace` (draft `core/JdbcEscapes.java`, part SQL-05) is called before
`NativeSql.parse` when escape processing is on (always for a `PreparedStatement`;
`Statement.setEscapeProcessing(false)` skips it). `Connection.nativeSQL(sql)` is
`replace`, then `parse` with parameters, the statements joined by `;`. pgjdbc source for
behavior: `core/Parser.java` `replaceProcessing`, `parseSql`, `escapeFunction`,
`modifyJdbcCall`; `jdbc/EscapedFunctions2.java`.

```java
public final class JdbcEscapes
    public static String replace(String sql, boolean standardConformingStrings) throws SQLException;
```
```java
final class EscapeFunctions
    static boolean append(StringBuilder out, String name, List<? extends CharSequence> args) throws SQLException;
```
```java
public final class CallEscape
public enum Mode
    public String sql();
    public boolean isFunction();
    public static CallEscape parse(String sql, boolean standardConformingStrings, Mode mode) throws SQLException;
```

### 4.1 Escapes

| Escape | Becomes |
|---|---|
| `{d '...'}`, `{t '...'}`, `{ts '...'}` | `DATE '...'`, `TIME '...'`, `TIMESTAMP '...'` |
| `{escape '...'}` | `ESCAPE '...'` |
| `{oj ...}` | the text between `{oj` and `}` |
| `{fn name(args)}` | the translation of 4.3, or `name(args)` for a name that is not in the table |
| `{call ...}`, `{? = call ...}` | not handled by `replace`; see 4.4 |

### 4.2 Rules

- E1. Escapes are recognized only outside quoted tokens and comments. A STRING,
  QUOTED_IDENTIFIER, DOLLAR_STRING or BLOCK_COMMENT without an end raises `PSQLException`
  `42601` (`The <string literal | quoted identifier | dollar-quoted string | block comment>
  that starts at position <n> of the statement is not terminated.`).
- E2. An escape starts with `{`, directly followed by the keyword in lower or upper case
  (each letter separately), any number of spaces (not tabs or line ends), and a start
  character: `'` for d, t, ts and escape; `"` or a letter for fn; `"`, a letter or `(` for
  oj. The keywords are tried in the order d, t, ts, fn, oj, escape. Anything else that
  starts with `{` is copied.
- E3. *quirk* For d, t, ts, escape and oj the text after the keyword is copied without
  looking at it up to the first `}`, which is dropped (so `{d '2020-01-02}'}` is cut at the
  first brace and nested escapes inside `{oj ...}` are not translated).
- E4. For fn the name is the trimmed text up to the first `(` found anywhere after it; the
  arguments are translated recursively (escapes inside arguments work) and split at commas
  outside parentheses; an empty argument is dropped; an argument keeps the white space
  around it. After the closing `)` the text up to the next `}` is copied, and the `}`
  dropped. *quirk*: without any `(` the first character of the name is lost (`{fn user}`
  gives `ser`).
- E5. A `)` that closes more than was opened ends an argument; at the top level it is
  copied.

`sql-escapes.tsv` holds 194 cases in both string modes (groups datetime, escape,
outerjoin, function, literal-comment, unterminated, braces, placeholder); `JdbcEscapesTest`
(draft) checks every row and the texts of E1.

### 4.3 The function table

`EscapeFunctions.append(out, name, args)` (package-private; part SQL-04) appends the
translation and returns true, or returns false for a name that is not in the table. The
name is compared in lower case. **(recorded)** through `Connection.nativeSQL`, with 0 to 5
arguments per name (`sql-functions.tsv`):

| Function | Arguments | Translation (`a1`, `a2`, ... are the arguments as written) | Other argument counts |
|---|---|---|---|
| `ceiling` | 1 | `ceil(a1)` | !42601 |
| `log` | 1 | `ln(a1)` | !42601 |
| `log10` | 1 | `log(a1)` | !42601 |
| `power` | 2 | `pow(a1, a2)` | !42601 |
| `truncate` | 2 | `trunc(a1, a2)` | !42601 |
| `char` | 1 | `chr(a1)` | !42601 |
| `concat` | any | `(a1\|\| a2)`, `(a1\|\| a2\|\| a3)`, ... (`()` for none) | - |
| `insert` | 4 | `overlay(a1 placing  a4 from  a2 for  a3)` | !42601 |
| `lcase` | 1 | `lower(a1)` | !42601 |
| `left` | 2 | `substring(a1 for  a2)` | !42601 |
| `length` | 1 | `length(trim(trailing from a1))` | !42601 |
| `locate` | 2, 3 | `position(a1 in  a2)`; `( a3*sign(position(a1 in substring( a2 from  a3)))+position(a1 in substring( a2 from  a3)))` | !42601 |
| `ltrim` | 1 | `trim(leading from a1)` | !42601 |
| `right` | 2 | `substring(a1 from (length(a1)+1- a2))` | !42601 |
| `rtrim` | 1 | `trim(trailing from a1)` | !42601 |
| `space` | 1 | `repeat(' ',a1)` | !42601 |
| `substring` | 2, 3 | `substr(a1, a2)`; `substr(a1, a2, a3)` | !42601 |
| `ucase` | 1 | `upper(a1)` | !42601 |
| `curdate` | 0 | `current_date` | !42601 |
| `curtime` | 0 | `current_time` | !42601 |
| `dayname` | 1 | `to_char(a1,'Day')` | !42601 |
| `dayofmonth` | 1 | `extract(day from a1)` | !42601 |
| `dayofweek` | 1 | `extract(dow from a1)+1` | !42601 |
| `dayofyear` | 1 | `extract(doy from a1)` | !42601 |
| `hour` | 1 | `extract(hour from a1)` | !42601 |
| `minute` | 1 | `extract(minute from a1)` | !42601 |
| `month` | 1 | `extract(month from a1)` | !42601 |
| `monthname` | 1 | `to_char(a1,'Month')` | !42601 |
| `quarter` | 1 | `extract(quarter from a1)` | !42601 |
| `second` | 1 | `extract(second from a1)` | !42601 |
| `week` | 1 | `extract(week from a1)` | !42601 |
| `year` | 1 | `extract(year from a1)` | !42601 |
| `timestampadd` | 3 | see below | !42601 |
| `timestampdiff` | 3 | see below | !42601 |
| `database` | 0 | `current_database()` | !42601 |
| `ifnull` | 2 | `coalesce(a1, a2)` | !42601 |
| `user` | 0 | `user` | !42601 |

Passed through unchanged (recorded with these names; any other name too): `abs`, `acos`, `asin`, `atan`, `atan2`, `cos`, `cot`, `degrees`, `exp`, `floor`, `mod`, `pi`, `radians`, `rand`, `round`, `sign`, `sin`, `sqrt`, `tan`, `ascii`, `repeat`, `replace`, `now`, `convert`, `char_length`, `character_length`, `octet_length`, `position`, `extract`, `current_date`, `current_time`, `current_timestamp`, `bit_length`, `difference`, `soundex`, `nosuchfunction`.

`timestampadd(unit, n, ts)` gives `(CAST( n||' <u>' as interval)+ ts)` for the units
`SQL_TSI_DAY`, `_SECOND`, `_HOUR`, `_MINUTE`, `_MONTH`, `_WEEK`, `_YEAR` (`<u>` is day,
second, ...), and `(CAST(( n::int * 3) || ' month' as interval)+ ts)` for
`SQL_TSI_QUARTER`. `timestampdiff(unit, t1, t2)` gives `extract( <u> from ( t2- t1))` for
`SQL_TSI_DAY`, `_SECOND`, `_HOUR`, `_MINUTE`. The unit is the first argument as written
(no trimming) and is compared without case.

| Error | Exception | Message of the driver |
|---|---|---|
| wrong number of arguments | `PSQLException` `42601` | `The function escape <name> takes <no arguments \| exactly one argument \| exactly two arguments \| exactly three arguments \| exactly four arguments \| two or three arguments>.` |
| unit that does not start with `SQL_TSI_` | `42601` | `The interval unit <unit> is not supported by the function escape <timestampadd \| timestampdiff>.` |
| `timestampadd`: `SQL_TSI_FRAC_SECOND` or an unknown `SQL_TSI_` unit | `0A000` | the same text |
| `timestampdiff`: `SQL_TSI_FRAC_SECOND` | `0A000` | the same text |
| `timestampdiff`: another `SQL_TSI_` unit (WEEK, MONTH, QUARTER, YEAR, unknown) | `42601` | the same text |

### 4.4 The call escape

`CallEscape.parse(sql, standardConformingStrings, mode)` (part SQL-06) is for
`Connection.prepareCall`; JDBC-DESIGN-FEATURES owns `CallableStatement`. `Mode` mirrors the
values of pgjdbc's property `escapeSyntaxCallMode`.

- C1. Text whose first character after white space and comments is not `{` is returned
  unchanged; `isFunction()` is true when it starts with the word `call` (any case) and
  white space, and at least six characters follow the start.
- C2. Otherwise the form is `{`, optional white space, optional `?` white space `=`, white
  space, `call` (any case), one white space character, the body, `}`, then only white
  space and comments. In the body a `'` toggles a quoted part, a backslash in a quoted part
  hides the next character when `standard_conforming_strings` is off, the first `}` outside
  a quoted part ends the body (*quirk*: the brace of a nested escape too) and a `;` outside
  a quoted part is an error.
- C3. Result: `select * from <body> as result` for mode SELECT, and for mode
  CALL_IF_NO_RETURN when the escape has `? =`; else `call <body>`. A body without `(` gets
  `()`, or `(?)` when the escape has `? =`. With `? =` and a `(`, `?,` is inserted after the
  first `(` (only `?` when the first thing after it, past white space and comments, is `)`).
  `isFunction()` is true.
- C4. Any other form raises `PSQLException` `2F003` (`The call escape is malformed at
  offset <n>.`; the offset is informative).

`sql-call.tsv`: 60 texts x 2 string modes x 3 modes; `CallEscapeTest` (draft) checks every
row.

## 5. Type table and lookup

pgjdbc source for behavior: `core/Oid.java`, `jdbc/TypeInfoCache.java`.

### 5.1 The type table

`TypeTable` (draft `core/TypeTable.java`, part TYPE-02) holds the types below and, for
each type with an array OID, the array type: name `_<name>` (also found as `<name>[]`),
Types code 2003, class `java.sql.Array`, element OID set. `Oid` (part TYPE-01) has a
constant per row, named after the type in upper case (`"char"` is `CHAR`), plus
`<NAME>_ARRAY` and `UNSPECIFIED` = 0. The delimiter of array text is `,` except for box
(`;`). **(recorded: `types.tsv`)**

| Name | OID | Array OID | Types code | Class name | Known to pgjdbc by name |
|---|---|---|---|---|---|
| `bool` | 16 | 1000 | -7 | `java.lang.Boolean` | yes |
| `int2` | 21 | 1005 | 5 | `java.lang.Integer` | yes |
| `int4` | 23 | 1007 | 4 | `java.lang.Integer` | yes |
| `int8` | 20 | 1016 | -5 | `java.lang.Long` | yes |
| `oid` | 26 | 1028 | -5 | `java.lang.Long` | yes |
| `float4` | 700 | 1021 | 7 | `java.lang.Float` | yes |
| `float8` | 701 | 1022 | 8 | `java.lang.Double` | yes |
| `numeric` | 1700 | 1231 | 2 | `java.math.BigDecimal` | yes |
| `money` | 790 | 791 | 8 | `org.stuntgres.util.PGmoney` | yes |
| `text` | 25 | 1009 | 12 | `java.lang.String` | yes |
| `varchar` | 1043 | 1015 | 12 | `java.lang.String` | yes |
| `bpchar` | 1042 | 1014 | 1 | `java.lang.String` | yes |
| `name` | 19 | 1003 | 12 | `java.lang.String` | yes |
| `char` | 18 | 1002 | 1 | `java.lang.String` | yes |
| `bytea` | 17 | 1001 | -2 | `[B` | yes |
| `date` | 1082 | 1182 | 91 | `java.sql.Date` | yes |
| `time` | 1083 | 1183 | 92 | `java.sql.Time` | yes |
| `timetz` | 1266 | 1270 | 92 | `java.sql.Time` | yes |
| `timestamp` | 1114 | 1115 | 93 | `java.sql.Timestamp` | yes |
| `timestamptz` | 1184 | 1185 | 93 | `java.sql.Timestamp` | yes |
| `interval` | 1186 | 1187 | 1111 | `org.stuntgres.util.PGInterval` | no |
| `uuid` | 2950 | 2951 | 1111 | `java.util.UUID` | yes |
| `json` | 114 | 199 | 1111 | `org.stuntgres.util.PGobject` | yes |
| `jsonb` | 3802 | 3807 | 1111 | `org.stuntgres.util.PGobject` | yes |
| `xml` | 142 | 143 | 2009 | `java.sql.SQLXML` | yes |
| `bit` | 1560 | 1561 | -7 | `java.lang.Boolean` | yes |
| `varbit` | 1562 | 1563 | 1111 | `java.lang.String` | yes |
| `inet` | 869 | 1041 | 1111 | `java.lang.String` | no |
| `cidr` | 650 | 651 | 1111 | `java.lang.String` | no |
| `macaddr` | 829 | 1040 | 1111 | `java.lang.String` | no |
| `macaddr8` | 774 | 775 | 1111 | `java.lang.String` | no |
| `point` | 600 | 1017 | 1111 | `org.stuntgres.geometric.PGpoint` | yes |
| `box` | 603 | 1020 | 1111 | `org.stuntgres.geometric.PGbox` | yes |
| `circle` | 718 | 719 | 1111 | `org.stuntgres.geometric.PGcircle` | no |
| `line` | 628 | 629 | 1111 | `org.stuntgres.geometric.PGline` | no |
| `lseg` | 601 | 1018 | 1111 | `org.stuntgres.geometric.PGlseg` | no |
| `path` | 602 | 1019 | 1111 | `org.stuntgres.geometric.PGpath` | no |
| `polygon` | 604 | 1027 | 1111 | `org.stuntgres.geometric.PGpolygon` | no |
| `tsvector` | 3614 | 3643 | 1111 | `java.lang.String` | no |
| `tsquery` | 3615 | 3645 | 1111 | `java.lang.String` | no |
| `refcursor` | 1790 | 2201 | 2012 | `java.sql.ResultSet` | yes |
| `void` | 2278 | 0 | 1111 | `java.lang.String` | no |
| `record` | 2249 | 2287 | 1111 | `java.lang.String` | no |
| `unknown` | 705 | 0 | 1111 | `java.lang.String` | no |
| `regclass` | 2205 | 2210 | 1111 | `java.lang.String` | no |
| `regtype` | 2206 | 2211 | 1111 | `java.lang.String` | no |
| `regproc` | 24 | 1008 | 1111 | `java.lang.String` | no |
| `xid` | 28 | 1011 | 1111 | `java.lang.String` | no |
| `cid` | 29 | 1012 | 1111 | `java.lang.String` | no |
| `tid` | 27 | 1010 | 1111 | `java.lang.String` | no |
| `pg_lsn` | 3220 | 3221 | 1111 | `java.lang.String` | no |
| `jsonpath` | 4072 | 4073 | 1111 | `java.lang.String` | no |
| `int4range` | 3904 | 3905 | 1111 | `java.lang.String` | no |
| `numrange` | 3906 | 3907 | 1111 | `java.lang.String` | no |
| `tstzrange` | 3910 | 3911 | 1111 | `java.lang.String` | no |
| `int2vector` | 22 | 1006 | 1111 | `java.lang.String` | no |
| `oidvector` | 30 | 1013 | 1111 | `java.lang.String` | no |
| `aclitem` | 1033 | 1034 | 1111 | `java.lang.String` | no |
| `int4multirange` | 4451 | 6150 | 1111 | `java.lang.String` | no |

The class name is what `ResultSetMetaData.getColumnClassName` reports; for some types it
is not the class `getObject` returns (7.7): inet, cidr, macaddr, varbit and the other
`java.lang.String` rows of JDBC type 1111 are returned as `PGobject`, money as `Double`,
bit as `Boolean` only for `bit(1)`.

```java
public final class Oid
    // 117 public constants, see the text
```
```java
public final class PgType
    public PgType(int oid, String name, int sqlType, String className, int arrayOid, int elementOid, char delimiter);
    public int oid();
    public String name();
    public int sqlType();
    public String className();
    public int arrayOid();
    public int elementOid();
    public boolean isArray();
    public char delimiter();
```
```java
public final class TypeTable
    public static final int UNKNOWN_LENGTH;
    public static PgType byOid(int oid);
    public static PgType byName(String name);
    public static PgType find(int oid, TypeLookup lookup) throws SQLException;
    public static int oidByName(String name, TypeLookup lookup) throws SQLException;
    public static int arrayOidByElementName(String elementName, TypeLookup lookup) throws SQLException;
    public static int precision(int oid, int typmod);
    public static int scale(int oid, int typmod);
    public static int displaySize(int oid, int typmod);
    public static boolean isSigned(int oid);
    public static boolean isCaseSensitive(int oid);
    public static int maximumPrecision(int oid);
```

`arrayOidByElementName` accepts, besides the table names, these spellings in any case:
smallint, int, integer, long, bigint, boolean, float (float8), real, double precision,
decimal, character varying, time without time zone, time with time zone, timestamp
without time zone, timestamp with time zone **(recorded: `types-byname.tsv`)**.
`oidByName` accepts the exact table names only (pgjdbc resolves `integer` as an element
type name but not as a type name).

### 5.2 Precision, scale and display size

`precision`, `scale`, `displaySize`, `isSigned`, `isCaseSensitive`, `maximumPrecision`
take the type OID (an array type of the table counts as its element type) and the type
modifier `m`. `U` is `TypeTable.UNKNOWN_LENGTH` (2147483647). Integer arithmetic wraps as
in Java. **(recorded: `types-typmod.tsv`, 52 modifiers per type)**

| Type | Precision | Scale | Display size |
|---|---|---|---|
| int2 / int4 / int8 / oid | 5 / 10 / 19 / 10 | 0 | 6 / 11 / 20 / 10 |
| float4 / float8 | 8 / 17 | 8 / 17 | 15 / 25 |
| bool, "char" | 1 | 0 | 1 |
| numeric | 0 for m = -1, else `((m - 4) & 0xFFFF0000) >> 16` | 0 for m = -1, else `(m - 4) & 0xFFFF` | 131089 for m = -1, else 1 + `((m - 4) >> 16) & 0xFFFF` + (1 if the scale is not 0) |
| bpchar, varchar | U for m = -1, else m - 4 | 0 | as precision |
| date | 13 | 0 | 13 |
| time / timetz / timestamp / timestamptz | the display size | 6 for m = -1, else m | 8 + s / 14 + s / 22 + s / 28 + s, where s is 7 for m = -1, 0 for m = 0, 3 for m = 1, else m + 1 |
| interval | 49 | 6 for m = -1, else `m & 0xFFFF` | 49 |
| bit | m | 0 | m |
| varbit | U for m = -1, else m | 0 | as precision |
| every other type | U | 0 | U |

`isSigned`: int2, int4, int8, float4, float8, numeric. `isCaseSensitive`: false for oid,
int2, int4, int8, float4, float8, numeric, bool, bit, varbit, date, time, timetz,
timestamp, timestamptz, interval; true otherwise. `maximumPrecision`: numeric 1000; time,
timetz, timestamp, timestamptz, interval 6; bpchar, varchar 10485760; bit, varbit
83886080; else 0.

### 5.3 The lookup interface

```java
public interface TypeLookup
    PgType byOid(int oid) throws SQLException;
    int oidByName(String name) throws SQLException;
    int arrayOidByElementName(String elementName) throws SQLException;
```
```java
public final class ConversionContext
    public ConversionContext(TimeZone defaultZone, TypeLookup types, boolean stringTypeUnspecified, boolean convertBooleanToNumeric);
    public TimeZone defaultZone();
    public TypeLookup types();
    public boolean stringTypeUnspecified();
    public boolean convertBooleanToNumeric();
```

`TypeLookup` is implemented by JDBC-DESIGN-CORE with catalog queries and a cache per
connection. The conversion code calls it only for an OID or name the table does not hold.
What it must answer **(recorded: `types-user.tsv`, `types-byname.tsv`, `types-rsmd.tsv`)**:

- `byOid`: JDBC type 2003 (ARRAY) for an array type, 2002 (STRUCT) for a composite type,
  2001 (DISTINCT) for a domain, 12 (VARCHAR) for an enum, 1111 (OTHER) otherwise; class
  name `java.sql.Array` for an array, `java.util.Map` for hstore, else `java.lang.String`;
  the element OID and the element's delimiter for an array. The name is the type name,
  qualified and double-quoted as `"schema"."name"` when the schema is not on the search
  path or the name is not all lower case (recorded: `mood`, `"s2"."color"`,
  `"public"."Mixed Case"`); an array type is `_name`.
- A result column of a domain type arrives with the base type's OID and modifier; no
  lookup happens.
- `oidByName`: the name as the server resolves it (search path, schema qualification,
  quoted names; unquoted names in any case); 0 when there is none.
- `arrayOidByElementName`: the array type over that type; 0 when there is none.

### 5.4 Tests

`TypeTableTest` (draft, part TYPE-03): every row of `types.tsv` and `types-typmod.tsv`,
and literal cases for the name functions. The test support `Matrix` provides
`Matrix.LOOKUP`, a `TypeLookup` over `types-user.tsv`.

## 6. Text codecs

The driver reads and writes text format only (plan B6). The codecs are small classes of
static functions in `org.stuntgres.core`; the getter and setter functions of sections 7
and 8 call them. Session assumptions, to be ensured by JDBC-DESIGN-CORE (12): `DateStyle`
is ISO (any field order); `client_encoding` is UTF8 (plan B5). Nothing depends on
`bytea_output` (both forms are read), `extra_float_digits`, or the session `TimeZone`
(the server writes offsets). `IntervalStyle` may be `postgres`, `postgres_verbose` or
`iso_8601`; `sql_standard` is misread, as by pgjdbc (10.2). The text PostgreSQL 18.6
prints for each recorded value is in `values.tsv` (taken with `COPY ... TO STDOUT`; it
equals what pgjdbc hands out as raw bytes for every value, bytea aside).

### 6.1 Booleans

`BooleanText` (part CODEC-01). `parse(text)`: after trimming, in any case, `1`, `true`,
`t`, `yes`, `y`, `on` are true and `0`, `false`, `f`, `no`, `n`, `off` are false; anything
else raises `PSQLException` `42846`. `of(Object)`: a Boolean as it is; a String through
`parse`; a Character `1`, `t`, `T`, `y`, `Y` true and `0`, `f`, `F`, `n`, `N` false; a
Number equal to 1.0 true and to 0.0 false; anything else `42846`. The server prints bool
as `t` and `f`; the setters write `TRUE` and `FALSE` (8.3).

```java
public final class BooleanText
    public static boolean parse(String text) throws SQLException;
    public static boolean of(Object value) throws SQLException;
```

### 6.2 Numbers

`NumberText` (part CODEC-01), the routines every numeric getter shares:

- `plainInteger(text, min, max)` (package-private): digits with an optional leading `-`
  and an optional `.` followed by digits only, which are cut off; null when the text has
  another form, is only `.` or `-.`, overflows a long or is outside the range. *quirk*: `-`
  alone is 0.
- `integer(text, min, max, javaType)`: trims; `Long.parseLong` and the range; else `new
  BigDecimal(text).toBigInteger()` and the range (so `3.99` is 3 and `-1e3` is -1000); else
  `22003`.
- `decimal(text)`: trims; `new BigDecimal(text)` or `22003`. `NaN` and `Infinity` are
  errors here.
- `real(text)`, `doublePrecision(text)`: trim; `Float.parseFloat` / `Double.parseDouble`
  or `22003` (`NaN`, `Infinity`, `-Infinity` are read as such).

The server prints int2, int4, int8 and oid as decimal digits (oid unsigned: up to
4294967295), float4 and float8 in shortest round-trip form (`1.5`, `1e+10`, `NaN`,
`Infinity`, `-Infinity`, `-0`), numeric with its exact digits and scale (`1.50`, `NaN`,
`Infinity`, `-Infinity`).

```java
public final class NumberText
    static Long plainInteger(String text, long min, long max);
    public static String withoutCurrency(String text);
    public static long integer(String text, long min, long max, String javaType) throws SQLException;
    public static BigDecimal decimal(String text) throws SQLException;
    public static float real(String text) throws SQLException;
    public static double doublePrecision(String text) throws SQLException;
```

### 6.3 Money

The server prints money with the currency format of `lc_monetary`; in the C locale
`$1,234,567.89` and `-$12.34`. `NumberText.withoutCurrency(text)` turns `$1.50` into
`1.50`, `-$1.50` into `-1.50` and `($1.50)` into `-1.50` (text shorter than two characters
or whose first character is above `-` in ASCII is returned unchanged). Group separators
are not removed: *quirk* every numeric getter fails with `22003` for a money value of 1000
or more **(recorded: `money-004`)**. The value class is `PGmoney` (10).

### 6.4 bytea

`ByteaText` (part CODEC-01). `decode(text)`: text that starts with `\x` is hex (two digits
per byte, either case); an odd number of digits or another character raises `22023`
(driver's choice; the server never sends it). Other text is the escape form: `\\` is a
backslash, `\ooo` (three octal digits) a byte, any other character its UTF-8 bytes.
`encode(bytes)` writes `\x` and lower-case hex.

```java
public final class ByteaText
    public static byte[] decode(String text) throws SQLException;
    public static String encode(byte[] bytes);
```

### 6.5 Dates, times and timestamps

`DateTimeText` (part CODEC-02) parses the ISO forms the server prints: `2020-01-02`,
`0044-03-15 BC`, `10:11:12.123456`, `10:11:12+05:30`, `2020-01-02 10:11:12.5`,
`1900-01-01 00:00:00-04:56:02`, `294276-12-31 23:59:59.999999`, and the words `infinity`
and `-infinity` (handled by the callers). `parse(text)` (package-private) reads, in this
order: optional white space; an optional date `digits-digits-digits`; white space; an
optional time `digits:digits:digits` with an optional `.digits` (padded to nanoseconds);
white space; an optional offset `+` or `-` `digits`, optional `:digits`, optional
`:digits`; white space; after a date an optional `AD` or `BC`. The numbers are not
range-checked (*quirk*: `2020-13-45` is accepted and the Calendar rolls it over). Text
without date and time, trailing text, a missing number, or an offset `ZoneOffset` rejects
raises `PSQLException` `22007`. Only ASCII digits and ASCII white space count.

The package-private fields: `hasDate`, `hasTime`, `hasOffset`, `beforeChrist`, `year`
(default 1970), `month`, `day` (default 1), `hour`, `minute`, `second`, `nanos`, `offset`
(`ZoneOffset`, default UTC). `millis(zone, withDate)` gives the epoch milliseconds of the
fields without the fraction, computed with a lenient `GregorianCalendar` (Julian calendar
before 1582-10-15) in the offset of the text or, without one, in `zone`; with `withDate`
false the date is 1970-01-01. `zone(zone)` gives the zone used. `appendDate`, `appendTime`
(microseconds, trailing zeros removed, nothing below one microsecond) and `appendOffset`
(`+hh`, `+hh:mm`, `+hh:mm:ss`) are the formatters of section 8.

The constants `POSITIVE_INFINITY` (9223372036825200000), `NEGATIVE_INFINITY`
(-9223372036832400000) and `POSITIVE_SMALLER_INFINITY` (185543533774800000) are the
millisecond values of `java.sql.Date` and `Timestamp` that stand for infinity; they equal
the constants of pgjdbc's `PGStatement`, which JDBC-DESIGN-CORE mirrors (12).

```java
public final class DateTimeText
    public static final long POSITIVE_INFINITY;
    public static final long NEGATIVE_INFINITY;
    public static final long POSITIVE_SMALLER_INFINITY;
    static DateTimeText parse(String text) throws SQLException;
    long millis(TimeZone zone, boolean withDate);
    TimeZone zone(TimeZone zone);
    static void pad(StringBuilder out, int value, int digits);
    static void appendDate(StringBuilder out, int year, int month, int day);
    static void appendTime(StringBuilder out, int hour, int minute, int second, int nanos);
    static void appendOffset(StringBuilder out, int offsetSeconds);
```

### 6.6 Intervals

Read and written by `PGInterval` (10.2). The server prints, in style `postgres`, `1 year 2
mons 3 days 04:05:06.789`, `-1 years -2 mons +3 days -04:05:06.789012`, `00:00:00`,
`infinity`.

### 6.7 UUID, bit strings, JSON, XML, network and other types

No codec class: `ObjectGetters.uuid(text)` is `UUID.fromString` (`22023` on failure) and a
UUID parameter is `toString()`; bit, varbit, json, jsonb, xml, inet, cidr, macaddr,
macaddr8, tsvector, tsquery, ranges, composite and every other type are passed as text in
both directions, in a `PGobject` or `String` as 7.7 says.

### 6.8 Geometric types

Read and written by the classes of `org.stuntgres.geometric` (10.2).

### 6.9 hstore

`HstoreText` (part CODEC-03). `parse(text)`: pairs `"key"=>"value"` or `"key"=>NULL`
separated by commas; inside quotes a backslash escapes the next character; the result is
a `LinkedHashMap` in text order, null for NULL. `format(map)`: `"k"=>"v"` pairs joined by
`, `, keys and values through `toString`, `"` and `\` escaped by a backslash, a null value
as `NULL` **(recorded: `setObject(Map hstore)` in `set.tsv`, `hstore-*` in
`get-object.tsv`)**.

```java
public final class HstoreText
    public static Map<String, String> parse(String text);
    public static String format(Map<?, ?> map);
```

## 7. Getters

pgjdbc source for behavior: `jdbc/PgResultSet.java` (`getString` to `getTimestamp`,
`internalGetObject`, `getObject(int, Class)`, `toInt`, `toLong`, `trimMoney`),
`jdbc/TimestampUtils.java`, `jdbc/BooleanTypeUtil.java`, `util/NumberParser.java`.

### 7.1 Contract and the getter tables

Every function takes the column text as a `String` that is never null, and what else it
needs: the type OID or the `PgType` of the column, the type modifier, a Calendar, the
`ConversionContext`. For SQL NULL the caller (JDBC-DESIGN-CORE's `ResultSet`) does not
call these functions: it returns `false`, 0 or null and `wasNull()` is true, with one
exception that the tables show: `getObject(c, Class)` applies the type check of 7.8 before
it looks at the value, except for the java.time classes of 7.6. The rows of the 121 NULL
values (ids `<family>-null-<n>`) record this.

The getter tables (`get-*.tsv`, 1.4) have one row per value and zone combination: `id`
(into `values.tsv`), `jvm` (the JVM default zone, `UTC` or `Europe/Warsaw`, or `*` when
both give the same cells), `session` (`UTC`, `America/New_York`, `Asia/Kolkata`, or `*`),
then one column per getter; getters with identical columns share a column whose header
joins the names with `|`. The Calendar of the `(cal)` columns is a `GregorianCalendar` in
`Australia/Adelaide`. Not in the tables because every cell equals another column's:
`getDate(c, null)` = `getDate(c)`, likewise `getTime` and `getTimestamp`;
`getObject(c, emptyMap)` = `getObject(c)`.

Test support (parts TYPE-00B, TYPE-03): `Matrix.check(resource, getters)` runs the given
functions for every non-NULL row, zone and column and compares `Repr.of(result)` or
`Repr.error(exception)` with `Matrix.expected(cell)`, which applies decision 2 of 1.2. A
getter function may return `Matrix.SKIP` for a cell another part covers.

```java
public final class Matrix
    public static final String[] JVM_ZONES;
    public static final String[] SESSION_ZONES;
    public static final Object SKIP;
    public static final TypeLookup LOOKUP;
public static final class Value
public interface Getter
    public static Map<String, Value> values();
    public static String expected(String cell);
    public static void check(String resource, Map<String, Getter> getters);
```

### 7.2 Booleans and integers

`IntegerGetters` (part GET-02; table `get-integer.tsv`).

```java
public final class IntegerGetters
    public static boolean getBoolean(String value, int oid) throws SQLException;
    public static byte getByte(String value, int oid, ConversionContext context) throws SQLException;
    public static short getShort(String value, int oid, ConversionContext context) throws SQLException;
    public static int getInt(String value, int oid, ConversionContext context) throws SQLException;
    public static long getLong(String value, int oid, ConversionContext context) throws SQLException;
    static int booleanAsNumber(String value, int oid, ConversionContext context);
```

- `getBoolean(value, oid)`: for a bool column, whether the text is `t`; else
  `BooleanText.parse(value)`.
- `getShort`, `getInt`, `getLong`: (1) `NumberText.plainInteger(value, MIN, MAX)` when not
  null; (2) `v = NumberText.withoutCurrency(value)`; (3) `booleanAsNumber(v, oid,
  context)` when it is 0 or 1; (4) `NumberText.integer(v, MIN, MAX, name)`.
- `getByte`: (1) as above; (2) the trimmed text, 0 when it is empty (*quirk*: the other
  getters raise `22003` for empty text); (3) `Byte.parseByte`; (4) `booleanAsNumber`; (5)
  `NumberText.integer`. No currency handling.
- `booleanAsNumber` (package-private): 1 or 0 for the text `t` or `f` of a bool column when
  `context.convertBooleanToNumeric()` is true (pgjdbc's connection property of that name,
  default false), else -1. *quirk*: with the default, `getInt` of a bool column raises
  `22003` **(recorded)**. **Not recorded:** the property set to true; the rule is taken
  from pgjdbc's source (`PgResultSet.tryConvertBooleanToNumeric`).

### 7.3 Floating point and decimals

`DecimalGetters` (part GET-03; table `get-decimal.tsv`).

```java
public final class DecimalGetters
    public static float getFloat(String value, int oid, ConversionContext context) throws SQLException;
    public static double getDouble(String value, int oid, ConversionContext context) throws SQLException;
    public static BigDecimal getBigDecimal(String value, int oid, ConversionContext context) throws SQLException;
    public static BigDecimal getBigDecimal(String value, int oid, int scale, ConversionContext context) throws SQLException;
```

`getFloat`, `getDouble`: `withoutCurrency`, `booleanAsNumber`, then `NumberText.real` /
`doublePrecision`. `getBigDecimal(value, oid, context)`: the same with
`NumberText.decimal` (so `NaN` and the infinities of a numeric column raise `22003`); the
result has the scale of the text. `getBigDecimal(value, oid, scale, context)`: the same,
then `setScale(scale, RoundingMode.HALF_EVEN)`.

### 7.4 Strings, streams and bytes

`ResultSet.getString` returns the column text unchanged for every type (bool is `t`,
bpchar keeps its padding, bytea is `\x...`); there is no function for it. `TextGetters`
(part GET-01; tables `get-string.tsv`, `get-bytes.tsv`):

```java
public final class TextGetters
    public static byte[] getBytes(String value, int oid) throws SQLException;
    public static InputStream getBinaryStream(String value, int oid) throws SQLException;
    public static InputStream getAsciiStream(String value);
    public static InputStream getUnicodeStream(String value);
    public static Reader getCharacterStream(String value);
```

`getBytes`: `ByteaText.decode` for a bytea column, else the text in UTF-8.
`getBinaryStream`: a stream over `getBytes`. `getAsciiStream`: the text in US-ASCII (other
characters become `?`). `getUnicodeStream`: the text in UTF-8. `getCharacterStream`: a
`StringReader`. `Statement.setMaxFieldSize` truncation belongs to JDBC-DESIGN-CORE.

### 7.5 Date, time and timestamp

`DateTimeGetters` (part GET-04; table `get-datetime.tsv`). The zone `Z` is the Calendar's
zone or, without one, `context.defaultZone()`. A zone is *fixed* when its ID starts with
`GMT` or `UTC`.

```java
public final class DateTimeGetters
    public static Date getDate(String value, Calendar calendar, ConversionContext context) throws SQLException;
    public static Time getTime(String value, Calendar calendar, ConversionContext context) throws SQLException;
    public static Timestamp getTimestamp(String value, int oid, Calendar calendar, ConversionContext context) throws SQLException;
```

- `getTimestamp(value, oid, calendar, context)`: empty text raises `22007`; `infinity` and
  `-infinity` give the two special values (6.5); else `f = DateTimeText.parse(value)`. For
  a time or timetz column the milliseconds are those of `getTime`; else `f.millis(Z,
  true)`. The result is a `Timestamp` of these milliseconds with `setNanos(f.nanos)`.
- `getTime`: `f = parse(value)`; `ms = f.millis(Z, !f.hasOffset) + f.nanos / 1000000`
  (with an offset the date is 1970-01-01). When the text has an offset or its year is 1970
  AD, the result is `new Time(ms)`. Otherwise the time is moved to 1970-01-01 in `f.zone(Z)`:
  for a fixed zone `floorMod(ms + raw offset, 86400000) - raw offset`; else by setting ERA,
  YEAR, MONTH, DAY_OF_MONTH of a Calendar at `ms` to 1970-01-01.
- `getDate`: `infinity` and `-infinity` give the special values. Text longer than 13 UTF-8
  bytes: `ms = parse(value).millis(Z, true)`, cut to the start of the day in `Z` (fixed
  zone: `floorDiv(ms + raw offset, 86400000) * 86400000 - raw offset`; else HOUR_OF_DAY,
  MINUTE, SECOND, MILLISECOND set to 0); values at or beyond the special values are
  returned as they are. Text of at most 13 bytes is read without checks (*quirk*): a
  trailing `BC` (with the character before it) is removed; the bytes up to the first `-`
  are the year, up to the second `-` the month, the rest the day, each computed as `n * 10
  + (byte - '0')` whatever the byte is; a missing `-` raises `22007` (decision 2). The date
  is midnight of year, month, day (lenient) in a copy of the Calendar, or in a
  `GregorianCalendar` of `Z`, after `clear()`.

### 7.6 java.time

`JavaTimeGetters` (part GET-05; table `get-javatime.tsv`). Each function checks the column
type first and raises `PSQLException` `42821` for another type.

```java
public final class JavaTimeGetters
    public static LocalDate getLocalDate(String value, PgType type) throws SQLException;
    public static LocalTime getLocalTime(String value, PgType type) throws SQLException;
    public static LocalDateTime getLocalDateTime(String value, PgType type) throws SQLException;
    public static OffsetDateTime getOffsetDateTime(String value, PgType type) throws SQLException;
    public static OffsetTime getOffsetTime(String value, PgType type) throws SQLException;
```

| Function | Column types | Result |
|---|---|---|
| `getLocalDate` | date; timestamp (the date of `getLocalDateTime`) | `infinity` / `-infinity`: `LocalDate.MAX` / `MIN`; else year, month, day of the text, era BC applied |
| `getLocalTime` | time | `24:00:00`: `LocalTime.MAX`; else `LocalTime.parse` |
| `getLocalDateTime` | timestamp | `LocalDateTime.MAX` / `MIN` for the infinities; else the fields of `DateTimeText.parse` |
| `getOffsetDateTime` | timestamptz, timestamp, timetz | `OffsetDateTime.MAX` / `MIN` for the infinities; the fields with the offset of the text (UTC when it has none); for timestamptz and timestamp converted to offset zero; for timetz on 1970-01-01 with its own offset |
| `getOffsetTime` | timetz | text that starts with `24:00:00`: `OffsetTime.MAX`; else the fields with the offset |

Fields the java.time class rejects raise `22007`. `ZonedDateTime`, `Instant` and
`Duration` are not supported (7.8).

### 7.7 The default mapping of getObject

`ObjectGetters.getObject(value, type, typmod, context)` (part GET-06; table
`get-object.tsv`, column `getObject`) by the JDBC type of the column's `PgType`:

| JDBC type (examples) | Result |
|---|---|
| BIT, BOOLEAN | bool: `Boolean` of `getBoolean`; bit with text of length 1: `Boolean` (`1`/`0`); any other bit value: `PGobject` |
| SMALLINT, INTEGER, TINYINT (int2, int4) | `Integer` of `getInt` |
| BIGINT (int8, oid) | `Long` of `getLong` |
| NUMERIC | `Double` NaN or infinity for the texts `NaN`, `Infinity`, `-Infinity` (any case); else `BigDecimal`, and for a modifier other than -1 `setScale(s, HALF_EVEN)` with `s = (((typmod - 4) & 0x7ff) ^ 0x400) - 0x400` |
| REAL (float4) | `Float` of `getFloat` |
| FLOAT, DOUBLE (float8, money) | `Double` of `getDouble` (money: 6.3) |
| CHAR, VARCHAR, LONGVARCHAR (bpchar, "char", varchar, text, name, enums) | the text |
| DATE / TIME (time, timetz) / TIMESTAMP (timestamp, timestamptz) | `getDate` / `getTime` / `getTimestamp` without a Calendar |
| BINARY (bytea) | `byte[]` of `getBytes` |
| any other, by type name | `unknown`: the text; `uuid`: `java.util.UUID`; `hstore`: `Map<String, String>`; `interval`, `money`, `point`, `box`, `circle`, `line`, `lseg`, `path`, `polygon`: the value class of 10 through `pgObject`; else a `PGobject` with the type name and the text |
| ARRAY, SQLXML, REF_CURSOR | not handled: `IllegalArgumentException`; JDBC-DESIGN-CORE reads these columns itself (12) |

```java
public final class ObjectGetters
    public static Object getObject(String value, PgType type, int typmod, ConversionContext context) throws SQLException;
    public static UUID uuid(String value) throws SQLException;
    public static PGobject pgObject(String typeName, String value) throws SQLException;
    public static Object getArray(String text, PgType element, ConversionContext context) throws SQLException;
```

`pgObject(typeName, value)` creates the value object; a parse error of the value class
propagates (`42820`, `42821`). *quirk*: a negative money value in a `PGmoney` fails with
`42820` (10.2). `PGConnection.addDataType` (user classes) belongs to JDBC-DESIGN-CORE.

### 7.8 getObject with a class

`ClassGetters.getObject(value, type, target, context)` (part GET-07; the `getObject(X)`
columns of all getter tables). The column's JDBC type must be one of the listed; else
`PSQLException` `22023`.

```java
public final class ClassGetters
    public static <T> T getObject(String value, PgType type, Class<T> target, ConversionContext context) throws SQLException;
```

| Class | JDBC types of the column | Result |
|---|---|---|
| `String` | CHAR, VARCHAR | the text |
| `BigDecimal` | NUMERIC, DECIMAL | `getBigDecimal` (NaN raises `22003`) |
| `Boolean` | BOOLEAN, BIT | `getBoolean` |
| `Short` | SMALLINT | `getShort` |
| `Integer` | INTEGER, SMALLINT | `getInt` |
| `Long`, `BigInteger` | BIGINT | `getLong` |
| `Float` | REAL | `getFloat` |
| `Double` | FLOAT, DOUBLE | `getDouble` |
| `java.sql.Date` / `Time` | DATE / TIME | `getDate` / `getTime` |
| `Timestamp`, `Calendar` | TIMESTAMP, TIMESTAMP_WITH_TIMEZONE | `getTimestamp`; the Calendar is a `GregorianCalendar` in the default zone |
| `java.util.Date` | TIMESTAMP | a `java.util.Date` of the timestamp's milliseconds |
| `byte[]` | BINARY, VARBINARY, LONGVARBINARY | `getBytes` |
| `UUID` | any | the default object of 7.7 when it is a UUID, else `22023` |
| `InetAddress` | any | the text up to a `/`, read as a numeric IPv4 or IPv6 address (decision 6); empty text gives the loopback address; else `22023` |
| the classes of 7.6 | see 7.6 | see 7.6 (`42821`) |
| `PGobject` and its subclasses | any | `ObjectGetters.pgObject(type name, text)` when it is an instance of the class, else `22023` |
| `java.sql.Array`, `Blob`, `Clob`, `SQLXML` | ARRAY; BLOB, BINARY, BIGINT; CLOB, BIGINT; SQLXML | not handled here (12) |
| any other class (`Byte`, `Number`, `Object`, `Character`, `GregorianCalendar`, `ZonedDateTime`, `Instant`, `Duration`, `NClob`, `URL`, `Map`, `InputStream`, `Reader`, arrays, primitives, ...) | none | `22023` |

### 7.9 Errors of the conversion code

`ConversionErrors` (package-private, part TYPE-00A) creates them. `get-errors.tsv` lists
every distinct pgjdbc message of the getter tables with its count.

| Factory | Class, SQLState | Message of the driver | pgjdbc's message |
|---|---|---|---|
| `badValue(javaType, value)` | `PSQLException` 22003 | `Cannot read the value "<value>" as <javaType>.` | `Bad value for type <javaType> : <value>` |
| `notBoolean(value)` | 42846 | `Cannot convert the value "<value>" to boolean.` | `Cannot cast to boolean: "<value>"` |
| `badDateTime(what, value)` | 22007 | `Cannot read the value "<value>" as <what>.` | `Bad value for type timestamp/date/time: <value>` |
| `unsupportedClass(target, typeName)` | 22023 | `A column of type <typeName> cannot be read as <class name>.` | `conversion to <class> from <typeName> not supported` |
| `wrongColumnType(target, typeName)` | 42821 | the same text | `Cannot convert the column of type <TYPE> to requested type <class name>.` |
| `badParameter(value, target)` | 07006 | `Cannot send a value of class <class name> as <target>.` | `Cannot cast an instance of <class name> to type <target>` |
| `notImplemented(method)` | `java.sql.SQLFeatureNotSupportedException` 0A000 | `The method <method> is not implemented.` | `Method <class>.<method> is not yet implemented.` |

Other texts are given where they arise (3.4, 4.2 to 4.4, 6.4, 8, 9, 10). No application
or framework is known to match on any of pgjdbc's texts for these cases.

```java
final class ConversionErrors
    static PSQLException badValue(String javaType, String value);
    static PSQLException notBoolean(Object value);
    static PSQLException badDateTime(String what, String value);
    static PSQLException unsupportedClass(Class<?> target, String typeName);
    static PSQLException wrongColumnType(Class<?> target, String typeName);
    static PSQLException badParameter(Object value, String target);
    static SQLFeatureNotSupportedException notImplemented(String method);
```
```java
public class PSQLException extends SQLException
    public PSQLException(String msg, PSQLState state);
    public PSQLException(String msg, PSQLState state, Throwable cause);
```

`PSQLState` (part TYPE-00A) is the complete enum of the mirrored class: 56 constants with
`getState()` and `static isConnectionError(String)`; the names and codes are listed in
`.cswd/tmp/JDBC/research/pgjdbc-map.md` section 1 and in the draft file.

```java
public enum PSQLState
    public String getState();
    public static boolean isConnectionError(String psqlState);
```

### 7.10 Getters outside this layer

**(recorded: `get-other.tsv`, `get-string.tsv`, `get-object.tsv`)** For JDBC-DESIGN-CORE
and JDBC-DESIGN-FEATURES:

- Rejected for every column and value, NULL included, with
  `SQLFeatureNotSupportedException` `0A000` (`ConversionErrors.notImplemented`):
  `getNString`, `getNCharacterStream`, `getURL`, `getRef`, `getRowId`, `getNClob`, and
  `getObject(c, map)` with a non-empty map.
- `getArray` returns an Array object for every column type; for a column that is no array
  its methods fail (`get-other.tsv` shows the failures). Section 9 gives the parts.
- `getBlob`, `getClob`: the column value read with `getLong` as a large object OID
  (`22003` for other text); `getSQLXML`: xml columns. JDBC-DESIGN-FEATURES.
- `getObject` of a refcursor column fetches the cursor (recorded as SQLState `34000` here
  because no cursor was open). JDBC-DESIGN-FEATURES.

## 8. Setters

pgjdbc source for behavior: `jdbc/PgPreparedStatement.java` (`setNull`, the setters,
`setObject` in three forms, the `castTo...` helpers), `jdbc/TimestampUtils.java`
(`toString` forms), `jdbc/ArrayEncoding.java`.

### 8.1 Parameters and the NUL character

Every setter function returns a `ParamValue`: the type OID and the value text (null for
SQL NULL). JDBC-DESIGN-CORE passes `oid()`, `bytes()` and `format()` of each parameter to
`PqConnection.execParams` (NATIVE 9.5). `format()` is 0, or 1 for a text with a NUL
character (decision 1). OID 0 lets the server infer the type from the statement.

```java
public final class ParamValue
    public ParamValue(int oid, String text);
    public int oid();
    public String text();
    public byte[] bytes();
    public int format();
```

### 8.2 setNull

`BasicSetters.setNull(sqlType, context)` **(recorded)**:

| OID sent | java.sql.Types constants |
|---|---|
| 16 | BIT, BOOLEAN |
| 21 | TINYINT, SMALLINT |
| 23 | INTEGER |
| 20 | BIGINT |
| 701 | FLOAT, DOUBLE |
| 700 | REAL |
| 1700 | NUMERIC, DECIMAL |
| 1042 | CHAR |
| 1043 | VARCHAR (stringtype default), LONGVARCHAR (stringtype default) |
| 0 | VARCHAR (stringtype unspecified), LONGVARCHAR (stringtype unspecified), TIME, TIMESTAMP, NULL, OTHER, DISTINCT, STRUCT, ARRAY, TIME_WITH_TIMEZONE, TIMESTAMP_WITH_TIMEZONE |
| 1082 | DATE |
| 17 | BINARY, VARBINARY, LONGVARBINARY |
| rejected: SQLState 07006 | JAVA_OBJECT, REF, DATALINK, ROWID, NCHAR, NVARCHAR, LONGNVARCHAR, NCLOB |
| 26 | BLOB, CLOB |
| 142 | SQLXML |
| 1790 | REF_CURSOR |

`setNull(sqlType, typeName, context)`: with a null name as above; else the OID of the name
through `TypeTable.oidByName` (0 when no type has the name, without an error)
**(recorded: `setNull(Types.INTEGER,nosuchtype)` gives OID 0, `setNull(Types.INTEGER,text)`
gives 25)**. The error text for a rejected constant: `The java.sql.Types value <n> has no
PostgreSQL type.`

### 8.3 Booleans, numbers, strings and bytes

`BasicSetters` (part SET-01).

```java
public final class BasicSetters
    public static ParamValue setNull(int sqlType, ConversionContext context) throws SQLException;
    public static ParamValue setNull(int sqlType, String typeName, ConversionContext context) throws SQLException;
    static int stringOid(ConversionContext context);
    public static ParamValue setBoolean(boolean x);
    public static ParamValue setShort(short x);
    public static ParamValue setInt(int x);
    public static ParamValue setLong(long x);
    public static ParamValue setFloat(float x);
    public static ParamValue setDouble(double x);
    public static ParamValue setBigDecimal(BigDecimal x);
    public static ParamValue setString(String x, ConversionContext context);
    public static ParamValue setBytes(byte[] x);
```

| Setter | OID | Text |
|---|---|---|
| `setBoolean` | 16 | `TRUE`, `FALSE` |
| `setByte`, `setShort` | 21 | decimal digits |
| `setInt` / `setLong` | 23 / 20 | decimal digits |
| `setFloat` | 700 | `Float.toString` (`NaN`, `Infinity`, `-Infinity`, `1.0E10`) |
| `setDouble` | 701 | `Double.toString` |
| `setBigDecimal` | 1700 | `toString()`, but `toPlainString()` when the scale is negative (`1E+3` is sent as `1000`, `1E-20` as `1E-20`); null: SQL NULL |
| `setString` | 1043, or 0 with `stringtype=unspecified` | the string; null: SQL NULL |
| `setBytes` | 17 | `ByteaText.encode`; null: SQL NULL |

pgjdbc sends setFloat as float4 with its default settings **(recorded)**; the remark of
the source map that the text path sends float8 does not apply to the default.

### 8.4 java.sql.Date, Time and Timestamp

`DateTimeSetters` (part SET-02). The fields are those of a `GregorianCalendar` in the zone
of the Calendar argument or, without one, in `context.defaultZone()`; the offset written
is that zone's offset at the instant (`ZONE_OFFSET + DST_OFFSET`). The OID is 0: the
server decides from the statement whether the text is a date, a time or a timestamp, with
or without time zone, and ignores the parts it does not need.

```java
public final class DateTimeSetters
    public static ParamValue setDate(Date x, Calendar calendar, ConversionContext context);
    public static ParamValue setTime(Time x, Calendar calendar, ConversionContext context);
    public static ParamValue setTimestamp(Timestamp x, Calendar calendar, ConversionContext context);
    public static ParamValue setLocalDate(LocalDate x);
    public static ParamValue setLocalTime(LocalTime x);
    public static ParamValue setOffsetTime(OffsetTime x);
    public static ParamValue setLocalDateTime(LocalDateTime x);
    public static ParamValue setOffsetDateTime(OffsetDateTime x);
```

| Setter | OID | Text |
|---|---|---|
| `setDate(x, cal)` | 0 (null: 1082) | `yyyy-mm-dd`, ` BC` for era BC, a space and the offset: `2020-01-02 +01`; `infinity` / `-infinity` for the special values |
| `setTime(x, cal)` | 0; for a `PGTime` 1083, or 1266 when it has a Calendar (which then replaces `cal`) | `hh:mm:ss`, milliseconds when not 0, the offset: `10:11:12.123+01` |
| `setTimestamp(x, cal)` | 0; for a `PGTimestamp` 1114, or 1184 when it has a Calendar (which then replaces `cal`) | date, space, time with microseconds, offset, ` BC` for era BC: `2020-01-02 10:11:12.123457+01`; the special values as above |

The nanoseconds of a Timestamp are rounded half up to microseconds; from 999999500 on the
fraction is 0 and the time one millisecond later.

### 8.5 java.time

| Function | OID | Text |
|---|---|---|
| `setLocalDate` | 1082 | `infinity` for `LocalDate.MAX`; `-infinity` for a date before 4713-01-01 BC; else year of era (at least four digits), month, day, ` BC` for era BCE |
| `setLocalTime` | 1083 | `24:00:00` for a value after `LocalTime.MAX` minus 500 ns; else the time rounded half up to microseconds |
| `setOffsetTime` | 1266 | `24:00:00` under the same condition, else the time *cut* to microseconds (*quirk*: not rounded); then the offset |
| `setLocalDateTime` | 1114 | `infinity` for a value after `LocalDateTime.MAX` minus 500 ms; `-infinity` before 4713-01-01 BC; else rounded to microseconds, ` BC` for era BCE |
| `setOffsetDateTime` | 1184 | the same limits (the lower one at UTC); the date and time of the value in its own offset, the offset, ` BC` |

### 8.6 Streams and readers

`StreamSetters` (part SET-03). The stream is read completely when the setter is called.

```java
public final class StreamSetters
    public static ParamValue setAsciiStream(InputStream stream, int length, ConversionContext context) throws SQLException;
    public static ParamValue setUnicodeStream(InputStream stream, int length, ConversionContext context) throws SQLException;
    public static ParamValue setCharacterStream(Reader reader, int length, ConversionContext context) throws SQLException;
    public static ParamValue setCharacterStream(Reader reader, ConversionContext context) throws SQLException;
    public static ParamValue setBinaryStream(InputStream stream, long length) throws SQLException;
```

| Setter form | Result |
|---|---|
| `setAsciiStream(i, in, int n)`, `setUnicodeStream(i, in, int n)` | varchar (OID 1043, also with `stringtype=unspecified`): at most n characters decoded as US-ASCII or UTF-8; fewer when the stream ends; null stream: as `setString(null)`; negative n: `22023` |
| `setCharacterStream(i, reader, int n)` | as `setString` of at most n characters; negative n: `22023` |
| `setCharacterStream(i, reader)` | text (OID 25) with everything the reader gives; null reader: as `setString(null)` |
| `setBinaryStream(i, in)`, `(i, in, int n)`, `(i, in, long n)` | bytea; with a length exactly n bytes: `22023` when the stream ends before (`The stream length <n> is not valid.`), or n is negative or above `Integer.MAX_VALUE`; null stream: bytea NULL. The function takes -1 for "no length"; the caller passes another negative number through as an error. |
| `setAsciiStream(i, in)`, `setAsciiStream(i, in, long)`, `setCharacterStream(i, reader, long)`, `setNCharacterStream` (both forms), `setNString`, `setURL`, `setRef`, `setRowId`, `setClob(i, Reader)`, `setNClob`, `setObject` with a `SQLType` | rejected by JDBC-DESIGN-CORE with `SQLFeatureNotSupportedException` `0A000` **(recorded)** |

### 8.7 setObject by the class of the value

`ObjectSetters.setObject(x, context)` (part SET-04), first match:

```java
public final class ObjectSetters
    public static ParamValue setObject(Object x, ConversionContext context) throws SQLException;
    public static ParamValue setArray(String baseTypeName, String text, ConversionContext context) throws SQLException;
    public static ParamValue setObject(Object x, int targetSqlType, int scale, ConversionContext context) throws SQLException;
```

| Class of x | Result |
|---|---|
| null | OID 0, SQL NULL |
| `UUID` | 2950, `toString()` |
| `SQLXML` | 142, `getString()` |
| `String`, `Character` | as `setString` |
| `BigDecimal`, `Short`, `Integer`, `Long`, `Float`, `Double`, `Boolean`, `byte[]` | the setter of 8.3; `Byte` as `setShort` |
| `java.sql.Date`, `Time`, `Timestamp` (and `PGTime`, `PGTimestamp`) | the setter of 8.4 without a Calendar |
| `java.sql.Array` | `setArray(getBaseTypeName(), toString(), context)`: the array type over the element type name (`TypeTable.arrayOidByElementName`); none: `PSQLException` `42602` (`No array type exists for the element type <name>.`) |
| `PGobject` | the OID of `getType()` (`TypeTable.oidByName`), text `getValue()`; a null or unknown type name: `07006` (`The type <name> of a PGobject is not known.`) |
| `LocalDate`, `LocalTime`, `OffsetTime`, `LocalDateTime`, `OffsetDateTime` | 8.5 |
| `Map` | the OID of `hstore` through the lookup, text `HstoreText.format`; no such type: `07006` (`The extension hstore is not installed.`) |
| any other `Number` (`BigInteger`, `AtomicLong`, ...) | 1700, `toString()` |
| a Java array | the array type by the innermost component class (int/Integer int4[], long/Long int8[], short/Short int2[], float/Float float4[], double/Double float8[], boolean/Boolean bool[], String varchar[], byte[] bytea[], BigDecimal numeric[], UUID uuid[]), text `ArrayFormat.format(x, ',')`; another component class: `07006` |
| anything else (`java.util.Date`, `Calendar`, `ZonedDateTime`, `Instant`, `Duration`, `char[]`, `StringBuilder`, an enum, `URL`, `InetAddress`, streams, ...) | `07006` (`Cannot tell the SQL type of a value of class <name>; use setObject with a java.sql.Types value.`) |

`Blob` and `Clob` values are stored as large objects by JDBC-DESIGN-FEATURES before this
function is reached.

### 8.8 setObject with a target type

`ObjectSetters.setObject(x, targetSqlType, scale, context)`; `scale` is negative when the
call gave none. A null `x` is `setNull(targetSqlType)`. A `UUID` with target OTHER is sent
as uuid.

| Target | Result |
|---|---|
| INTEGER / TINYINT, SMALLINT / BIGINT / REAL / FLOAT, DOUBLE | the value as a number, then `setInt` / `setShort` / `setLong` / `setFloat` / `setDouble`. As a number: a String or Character parsed with the parser of the target type; a Number through `intValue()`, `shortValue()`, ... (so a double 1.0E300 becomes 2147483647); a `java.util.Date` its milliseconds; a Boolean 1 or 0; anything else, or text that does not parse: `07006` |
| DECIMAL, NUMERIC | String, BigDecimal, BigInteger, Long/Integer/Short/Byte, Double/Float (`BigDecimal.valueOf`), `java.util.Date` (milliseconds), Boolean, Character converted to BigDecimal; with scale >= 0 `setScale(scale, HALF_UP)`; then `setBigDecimal`; else `07006` |
| CHAR | bpchar (1042), `x.toString()` |
| VARCHAR, LONGVARCHAR | as `setString(x.toString())` |
| DATE | `java.sql.Date`: `setDate`; other `java.util.Date`: its milliseconds as a date; `LocalDate`: 8.5; else `x.toString()` read with `DateTimeGetters.getDate` (`22007` when it is no date) and sent with `setDate` |
| TIME | `Time`; other `java.util.Date`; `LocalTime`; `OffsetTime`; else the text read with `getTime` |
| TIMESTAMP | `PGTimestamp` and `Timestamp`: 8.4; other `java.util.Date`; `LocalDateTime`; else the text read with `getTimestamp` |
| TIMESTAMP_WITH_TIMEZONE | `OffsetDateTime` or `PGTimestamp` only; else `07006` |
| BOOLEAN, BIT | `setBoolean(BooleanText.of(x))` (`42846`) |
| BINARY, VARBINARY, LONGVARBINARY | as `setObject(x)` |
| ARRAY | a `java.sql.Array` or a Java array as in 8.7; else `07006` |
| SQLXML | xml (142): `getString()` of a SQLXML, else `toString()` |
| DISTINCT | OID 0, `toString()` |
| OTHER | `PGobject` and `Map` as in 8.7; else OID 0, `toString()` |
| BLOB, CLOB | a `Blob`, `Clob` or `InputStream` value: JDBC-DESIGN-FEATURES (the caller handles it first); any other value: `07006` |
| any other constant | `07006` (`The java.sql.Types value <n> is not supported as a target type.`) |

*quirk*: `toString()` of a `java.sql.Time`, `Date`, `Timestamp` or `java.util.Date` uses the
JVM's real default zone, not `context.defaultZone()`; `set.tsv` marks these texts with
`?`.

### 8.9 The setter table and its test

`set.tsv` (1.4) has 3299 cases: every setter with edge values; `setObject` for every
source class of 8.7; 61 source values x the 39 constants of `java.sql.Types`; scale
variants; streams with every length form; arrays; value classes. `SettersTest` (draft,
part SET-05) runs each row for both JVM zones and both `stringtype` settings it holds for.
Its support classes: `SetterCases` builds the call of every case id (a generated table);
`SetterCalls` has one method per `PreparedStatement` setter that calls the function named
in this section — it is the mapping JDBC-DESIGN-CORE's `PreparedStatement` implements. A
row whose error is marked `server` must give a parameter; a text that starts with `?` is
not compared; float4 and float8 texts are compared as numbers.

## 9. Arrays

pgjdbc source for behavior: `jdbc/PgArray.java`, `jdbc/ArrayDecoding.java`
(`buildArrayList`), `jdbc/ArrayEncoding.java`.

### 9.1 Array text

`ArrayText.parse(text, delimiter)` (part ARRAY-01) returns the elements of the outer
array: a String, null for an unquoted `NULL` (upper case only), an `Object[]` for a nested
array. Rules: bounds before the first brace (`[0:2]=`) are skipped and ignored; a
backslash takes the next character literally, inside and outside quotes; `"` toggles
quoting and marks the element as quoted; unquoted white space (space, tab, line feed,
carriage return, form feed, vertical tab) is dropped; an element ends at the delimiter or
`}` outside quotes; an empty unquoted element is no element (`{}` has none). Text that is
no brace-delimited array raises `PSQLException` `22000` (`Cannot read "<text>" as an
array.`). `dimensions(elements)` counts the nesting along the first elements.

```java
public final class ArrayText
    public static Object[] parse(String text, char delimiter) throws SQLException;
    public static int dimensions(Object[] elements);
```

### 9.2 Element classes

`ObjectGetters.getArray(text, elementType, context)` (part GET-06) converts to the Java
array `Array.getArray()` returns: one dimension per nesting level, component class by the
element type **(recorded: the `getObject` column of `get-object.tsv` for the 80 array
values)**:

| Element type | Component class | Element conversion |
|---|---|---|
| int2 / int4 / int8, oid | `Short` / `Integer` / `Long` | `NumberText.integer` |
| float4 / float8, money | `Float` / `Double` | `NumberText.real` / `doublePrecision` (*quirk*: money text fails with `22003`) |
| numeric | `BigDecimal` | `NumberText.decimal` (*quirk*: `NaN` fails with `22003`) |
| bool, bit | `Boolean` | `BooleanText.parse` (*quirk*: a bit string longer than one bit fails with `42846`) |
| text, varchar, bpchar, name, "char", json, jsonb, and any type of JDBC type CHAR or VARCHAR (enums) | `String` | the text |
| bytea | `byte[]` | `ByteaText.decode` |
| date / time, timetz / timestamp, timestamptz | `java.sql.Date` / `Time` / `Timestamp` | the getters of 7.5 without a Calendar |
| uuid | `UUID` | `ObjectGetters.uuid` |
| any other | `Object` | `ObjectGetters.pgObject(element type name, text)` |

An empty array is a one-dimensional array of length 0. `Array.getBaseTypeName()` is the
element type's name, `getBaseType()` its JDBC type.

### 9.3 Writing arrays

`ArrayFormat.format(array, delimiter)` (part ARRAY-02) writes a Java array (objects or
primitives, any depth) as array text: `{` elements joined by the delimiter `}`; null as
`NULL`; a nested array recursively; a Boolean as `1` or `0`; a `byte[]` as its hex bytea
text in quotes; every other element as `toString()` in double quotes with `"` and `\`
escaped by a backslash. A value that is no array, or a `byte[]`, raises `07006` (`The
elements of an array must be given as a Java array.`). `arrayOid(array)` gives the array
type of 8.7 or 0. `Connection.createArrayOf(typeName, elements)` (JDBC-DESIGN-CORE) is an
Array with element type name `typeName` and text `format(elements, delimiter of the
element type)`; an unknown type name raises `42602` **(recorded: `array-format.tsv`)**.
Decision 8 applies: pgjdbc's own `toString` leaves the elements of some primitive arrays
unquoted (`text int[]{1,2}` gives `{1,2}`); the driver always quotes.

```java
public final class ArrayFormat
    public static String format(Object array, char delimiter) throws SQLException;
    public static int arrayOid(Object array);
```

### 9.4 java.sql.Array as an object (specification for JDBC-DESIGN-CORE)

**(recorded: `array-access.tsv`)** `getArray(index, count)` is 1-based and returns the
slice of the outer dimension (count 0: all from the index); an index below 1 or a slice
beyond the length raises `PSQLException` `22000`. `getResultSet()` has the columns `INDEX`
(int4) and `VALUE` (the element type; for a multi-dimensional array the array type, each
row holding a sub-array), one row per element of the outer dimension, type
`TYPE_SCROLL_INSENSITIVE` (1004). `toString()` is the array text as received. The forms
with a non-empty type map raise `SQLFeatureNotSupportedException` `0A000`. After `free()`
`getArray()` returns null. The exact rows for 13 arrays and 20 calls are in the table.

### 9.5 Tests

`ObjectGettersTest` (draft, GET-06): the array cells of `get-object.tsv` and literal
cases; `ArrayFormatTest` (draft, ARRAY-02): the recorded texts of `array-format.tsv` as
literals; `ArrayTextTest` is written by ARRAY-01 (13).

## 10. Mirrored value classes

Plan J9: the public names of pgjdbc's classes under `org.stuntgres`. pgjdbc source for
behavior: `util/PGobject.java`, `PGInterval.java`, `PGmoney.java`, `PGTime.java`,
`PGTimestamp.java`, `PGtokenizer.java`, `geometric/*.java`. The public API was listed
from the jar with `javap -public`; the behavior was recorded with one case list compiled
against both packages (365 cases; 14).

### 10.1 Public API

All classes are public and not final; `PGobject` implements `Serializable` and
`Cloneable`; the others extend it, except `PGTime` (extends `java.sql.Time`) and
`PGTimestamp` (extends `java.sql.Timestamp`). Public fields are part of the API. Not
mirrored: decision 9 of 1.2.

```java
public class PGobject implements Serializable, Cloneable
    public PGobject();
    public final void setType(String type);
    public void setValue(String value) throws SQLException;
    public final String getType();
    public String getValue();
    public boolean isNull();
    public boolean equals(Object obj);
    public Object clone() throws CloneNotSupportedException;
    public String toString();
    public int hashCode();
```
```java
public class PGInterval extends PGobject
    public PGInterval();
    public PGInterval(String value) throws SQLException;
    public PGInterval(int years, int months, int days, int hours, int minutes, double seconds);
    public void setValue(String value) throws SQLException;
    public void setValue(int years, int months, int days, int hours, int minutes, double seconds);
    public String getValue();
    public int getYears();
    public void setYears(int years);
    public int getMonths();
    public void setMonths(int months);
    public int getDays();
    public void setDays(int days);
    public int getHours();
    public void setHours(int hours);
    public int getMinutes();
    public void setMinutes(int minutes);
    public double getSeconds();
    public int getWholeSeconds();
    public int getMicroSeconds();
    public void setSeconds(double seconds);
    public void add(Calendar cal);
    public void add(Date date);
    public void add(PGInterval interval);
    public void scale(int factor);
    public boolean equals(Object obj);
    public int hashCode();
    public Object clone() throws CloneNotSupportedException;
```
```java
public class PGmoney extends PGobject
    public double val;
    public boolean isNull;
    public PGmoney(double value);
    public PGmoney(String value) throws SQLException;
    public PGmoney();
    public void setValue(String s) throws SQLException;
    public int hashCode();
    public boolean equals(Object obj);
    public String getValue();
    public Object clone() throws CloneNotSupportedException;
```
```java
public class PGTime extends Time
    public PGTime(long time);
    public PGTime(long time, Calendar calendar);
    public void setCalendar(Calendar calendar);
    public Calendar getCalendar();
    public int hashCode();
    public boolean equals(Object o);
    public Object clone();
```
```java
public class PGTimestamp extends Timestamp
    public PGTimestamp(long time);
    public PGTimestamp(long time, Calendar calendar);
    public void setCalendar(Calendar calendar);
    public Calendar getCalendar();
    public int hashCode();
    public boolean equals(Object o);
    public Object clone();
```
```java
public class PGpoint extends PGobject
    public double x;
    public double y;
    public boolean isNull;
    public PGpoint(double x, double y);
    public PGpoint(String value) throws SQLException;
    public PGpoint();
    public void setValue(String s) throws SQLException;
    public boolean equals(Object obj);
    public int hashCode();
    public String getValue();
    public void translate(int x, int y);
    public void translate(double x, double y);
    public void move(int x, int y);
    public void move(double x, double y);
    public void setLocation(int x, int y);
    public Object clone() throws CloneNotSupportedException;
```
```java
public class PGbox extends PGobject
    public PGpoint[] point;
    public PGbox(double x1, double y1, double x2, double y2);
    public PGbox(PGpoint p1, PGpoint p2);
    public PGbox(String s) throws SQLException;
    public PGbox();
    public void setValue(String value) throws SQLException;
    public boolean equals(Object obj);
    public int hashCode();
    public Object clone() throws CloneNotSupportedException;
    public String getValue();
```
```java
public class PGcircle extends PGobject
    public PGpoint center;
    public double radius;
    public PGcircle(double x, double y, double r);
    public PGcircle(PGpoint c, double r);
    public PGcircle(String s) throws SQLException;
    public PGcircle();
    public void setValue(String s) throws SQLException;
    public boolean equals(Object obj);
    public int hashCode();
    public Object clone() throws CloneNotSupportedException;
    public String getValue();
```
```java
public class PGline extends PGobject
    public double a;
    public double b;
    public double c;
    public PGline(double a, double b, double c);
    public PGline(double x1, double y1, double x2, double y2);
    public PGline(PGpoint p1, PGpoint p2);
    public PGline(PGlseg lseg);
    public PGline(String s) throws SQLException;
    public PGline();
    public void setValue(String s) throws SQLException;
    public boolean equals(Object obj);
    public int hashCode();
    public String getValue();
    public Object clone() throws CloneNotSupportedException;
```
```java
public class PGlseg extends PGobject
    public PGpoint[] point;
    public PGlseg(double x1, double y1, double x2, double y2);
    public PGlseg(PGpoint p1, PGpoint p2);
    public PGlseg(String s) throws SQLException;
    public PGlseg();
    public void setValue(String value) throws SQLException;
    public boolean equals(Object obj);
    public int hashCode();
    public Object clone() throws CloneNotSupportedException;
    public String getValue();
```
```java
public class PGpath extends PGobject
    public boolean open;
    public PGpoint[] points;
    public PGpath(PGpoint[] points, boolean open);
    public PGpath();
    public PGpath(String s) throws SQLException;
    public void setValue(String s) throws SQLException;
    public boolean equals(Object obj);
    public int hashCode();
    public Object clone() throws CloneNotSupportedException;
    public String getValue();
    public boolean isOpen();
    public boolean isClosed();
    public void closePath();
    public void openPath();
```
```java
public class PGpolygon extends PGobject
    public PGpoint[] points;
    public PGpolygon(PGpoint[] points);
    public PGpolygon(String s) throws SQLException;
    public PGpolygon();
    public void setValue(String s) throws SQLException;
    public boolean equals(Object obj);
    public int hashCode();
    public Object clone() throws CloneNotSupportedException;
    public String getValue();
```

### 10.2 Behavior

**(recorded: `value-classes-util.tsv`, `value-classes-geometric.tsv`)**

- **PGobject.** `equals(Object)`: the other object is a PGobject and both `getValue()`
  texts are equal or both null; the type is not compared. `hashCode` is the hash of the
  value or 0; `toString` is the value; `isNull` is `getValue() == null`; `clone` is a
  shallow copy.
- **PGmoney.** `setValue(s)`: a leading `(` makes the amount negative; one `(` at the start
  and one `)` at the end are removed; *quirk* the next character is dropped as the
  currency sign whatever it is (`12.34` gives 2.34; `-$12.34` fails); commas are removed;
  the rest is `Double.parseDouble`; failure raises `PSQLException` `42820`. `getValue()` is
  `$` and `Double.toString(val)`, with `-$` for a negative amount; null when `isNull`.
  `hashCode` is `31 * (hash of getValue()) + hash of the double bits`, 0 when null.
- **PGInterval.** Fields years, months, days, hours, minutes (ints), whole seconds and
  microseconds (ints with the sign of the seconds). `setSeconds(s)` rounds `s * 1000000`
  half up; NaN or more whole seconds than an int raise `IllegalArgumentException`.
  `setValue(String)`: text that starts with `P` is ISO 8601 (`P1Y2M3DT4H5M6.789S`: integers
  before Y, M, D, H, M and a decimal before S); the three-character text `@ 0` is zero;
  else `+` and `@` become spaces, the text is lower-cased and split at white space: an
  odd-numbered token with a colon is `[-]h:mm[:s.s]` (the sign applies to all three), an
  odd-numbered token without one is a number, and the even-numbered token after it is its
  unit by prefix (`year`, `mon`, `day`, `hour`, `min`, `sec`; other words are ignored);
  text that started with `@` and ends with `ago` negates everything. A malformed number
  raises `42820`. *quirk*: text without units, such as `infinity`, the empty string or
  `sql_standard` output, gives zero or wrong fields without an error. `getValue()` lists
  the non-zero fields as `1 years 2 mons 3 days 4 hours 5 mins 6.789 secs` (microseconds
  without trailing zeros; `0 secs` for zero). `add(Calendar)` adds milliseconds (the
  microseconds rounded), minutes, hours, days, months, years in this order; `add(Date)`
  uses `Calendar.getInstance()`; `add(PGInterval)` adds this interval to the argument;
  `scale(n)` multiplies every field. `equals` compares the seven fields; `hashCode` is
  `((((((8 * 31 + micro) * 31 + whole) * 31 + minutes) * 31 + hours) * 31 + days) * 31 +
  months) * 31 + years) * 31`.
- **PGTime, PGTimestamp.** The value of the superclass plus an optional Calendar.
  `equals(Object)`: same class, equal superclass value and equal Calendars (both null
  counts); `hashCode` is `31 * super.hashCode() + calendar hash (0 for none)`; `clone`
  copies the Calendar.
- **Geometric text.** A package-private helper `GeometryText` splits text at commas outside
  `()`, `[]` and `<>` (a closing character counts only when it matches the innermost open
  one; text that ends with a comma has an empty last piece) and strips one opening and one
  closing character. `PGpoint`: optional `(` `)`, at least two numbers (`Double.parseDouble`;
  further pieces are ignored); value `(x,y)` with `Double.toString`. `PGbox`: exactly two
  points; value `p1,p2`. `PGlseg`: `[p1,p2]`. `PGcircle`: `<p,r>`. `PGline`: `{a,b,c}`, or
  `[p1,p2]` giving a = x2 - x1, b = y2 - y1, c = y1 (*quirk*); other text changes nothing;
  the four-number and two-point constructors give a = -1, b = 0 for a vertical line, else
  a = slope, b = -1, and c = y1 - a * x1. `PGpath`: `[..]` open, `(..)` closed, else `42821`
  (`Cannot tell whether the path "<text>" is open or closed.`). `PGpolygon`: `(p1,...)`.
  A parse failure raises `42821` (`Cannot read "<text>" as a value of type <type>.`).
- **Geometric equality.** `PGpoint`: both coordinates with `==`; `PGbox`: the same two
  corners in either order or the other diagonal of the same rectangle; `PGlseg`: the two
  points in either order; `PGcircle`: radius and center; `PGline`: same class, equal value
  text and the three coefficients by `Double.compare`; `PGpath`: openness and all points;
  `PGpolygon`: all points. The `hashCode` formulas are recorded cases (`*.hashCode`).
  `clone` copies points and point arrays. A value that stands for SQL NULL (`isNull` or a
  null point array or center) equals only another such value and has hash 0.

```java
final class GeometryText
    static List<String> split(String text);
    static String strip(String text, char opening, char closing);
    static PSQLException failed(String type, String text, Throwable cause);
    static PGpoint[] points(List<String> pieces) throws SQLException;
    static PGpoint[] copy(PGpoint[] points) throws CloneNotSupportedException;
    static int hash(double value);
```

### 10.3 Use by getters and setters

`getObject` returns these classes for their types (7.7), `getObject(c, Class)` accepts
them (7.8), arrays of these types hold them (9.2); `setObject` sends a `PGobject` with the
OID of its type name and its value text (8.7), `PGTime` and `PGTimestamp` choose the OID
(8.4).

### 10.4 Tests

`ValueClassesTest` (draft, part PGOBJ-01C) and `GeometricClassesTest` (draft, part
PGOBJ-02C) hold the case list (generated from the recording program: constructors, every
parser with valid and malformed texts, `getValue`, `equals`, `hashCode`, `clone`, the
mutators) and compare each case with its row of the table. The draft also agreed with
pgjdbc on 300,000 random texts per parser (14).

## 11. Time zones and Calendar arguments

Rules:

- T1. `java.sql.Date`, `Time` and `Timestamp` are instants. Text **without** an offset
  (date, time, timestamp) is read in the Calendar's zone or, without a Calendar, in the
  default zone (`ConversionContext.defaultZone()`; JDBC-DESIGN-CORE passes
  `TimeZone.getDefault()` at the time of the call). Text **with** an offset (timetz,
  timestamptz) is read in that offset; the Calendar and the default zone do not matter.
- T2. The session `TimeZone` changes only the text the server prints for timestamptz (and
  the offset it assumes for input without one). The same instant printed in two session
  zones gives the same `Timestamp`.
- T3. `getDate` cuts to the start of the day in the Calendar's or default zone, also for a
  timestamptz (the instant is first read in its own offset).
- T4. The java.time getters use neither a zone nor a Calendar: `LocalDateTime` is the
  fields of the text; `OffsetDateTime` of a timestamptz is at offset zero.
- T5. Setters write the fields of the instant in the Calendar's or default zone and add
  that zone's offset (8.4), so the server can compute the instant; the java.time setters
  write the fields of the value (8.5).
- T6. Calendars are lenient `GregorianCalendar`s with the Julian calendar before
  1582-10-15; BC years as era BC. Offsets come from the JDK's time zone data.

**(recorded)** Getters, from `get-datetime.tsv` (`Timestamp:ms/nanos`, `Date:ms`,
`Time:ms`):

| Value (type, text in UTC session) | JVM zone | Session zone | `getDate` | `getTime` | `getTimestamp` | `getTimestamp(cal)` |
|---|---|---|---|---|---|---|
| date-001 (date `2020-01-02`) | UTC | * | `Date:1577923200000` | `Time:0` | `Timestamp:1577923200000/0` | `Timestamp:1577885400000/0` |
| date-001 (date `2020-01-02`) | Europe/Warsaw | * | `Date:1577919600000` | `Time:-3600000` | `Timestamp:1577919600000/0` | `Timestamp:1577885400000/0` |
| time-002 (time `12:34:56`) | UTC | * | `!ArrayIndexOutOfBoundsException\|-` | `Time:45296000` | `Timestamp:45296000/0` | `Timestamp:11096000/0` |
| time-002 (time `12:34:56`) | Europe/Warsaw | * | `!ArrayIndexOutOfBoundsException\|-` | `Time:41696000` | `Timestamp:41696000/0` | `Timestamp:11096000/0` |
| timetz-002 (timetz `12:34:56.789+05:30`) | UTC | * | `Date:0` | `Time:25496789` | `Timestamp:25496789/789000000` | `Timestamp:25496789/789000000` |
| timetz-002 (timetz `12:34:56.789+05:30`) | Europe/Warsaw | * | `Date:-3600000` | `Time:25496789` | `Timestamp:25496789/789000000` | `Timestamp:25496789/789000000` |
| timestamp-001 (timestamp `2020-01-02 10:11:12`) | UTC | * | `Date:1577923200000` | `Time:36672000` | `Timestamp:1577959872000/0` | `Timestamp:1577922072000/0` |
| timestamp-001 (timestamp `2020-01-02 10:11:12`) | Europe/Warsaw | * | `Date:1577919600000` | `Time:33072000` | `Timestamp:1577956272000/0` | `Timestamp:1577922072000/0` |
| timestamptz-001 (timestamptz `2020-01-02 10:11:12+00`) | UTC | * | `Date:1577923200000` | `Time:36672000` | `Timestamp:1577959872000/0` | `Timestamp:1577959872000/0` |
| timestamptz-001 (timestamptz `2020-01-02 10:11:12+00`) | Europe/Warsaw | * | `Date:1577919600000` | `Time:36672000` | `Timestamp:1577959872000/0` | `Timestamp:1577959872000/0` |

**(recorded)** Setters, from `set.tsv`:

| Case | stringtype | JVM zone | OID | Text |
|---|---|---|---|---|
| `setDate(millis 1577960000123)` | default | UTC | 0 | `2020-01-02 +00` |
| `setDate(millis 1577960000123)` | default | Europe/Warsaw | 0 | `2020-01-02 +01` |
| `setDate(millis 1577960000123)` | unspecified | UTC | 0 | `2020-01-02 +00` |
| `setDate(millis 1577960000123,cal)` | * | * | 0 | `2020-01-02 +10:30` |
| `setTime(millis 43200123)` | default | UTC | 0 | `12:00:00.123+00` |
| `setTime(millis 43200123)` | default | Europe/Warsaw | 0 | `13:00:00.123+01` |
| `setTime(millis 43200123)` | unspecified | UTC | 0 | `12:00:00.123+00` |
| `setTime(millis 43200123,cal)` | * | * | 0 | `21:30:00.123+09:30` |
| `setTimestamp(millis 1577960000123)` | default | UTC | 0 | `2020-01-02 10:13:20.123+00` |
| `setTimestamp(millis 1577960000123)` | default | Europe/Warsaw | 0 | `2020-01-02 11:13:20.123+01` |
| `setTimestamp(millis 1577960000123)` | unspecified | UTC | 0 | `2020-01-02 10:13:20.123+00` |
| `setTimestamp(millis 1577960000123,cal)` | * | * | 0 | `2020-01-02 20:43:20.123+10:30` |
| `setObject(PGTimestamp millis 1577960000123)` | default | UTC | 1114 | `2020-01-02 10:13:20.123+00` |
| `setObject(PGTimestamp millis 1577960000123)` | default | Europe/Warsaw | 1114 | `2020-01-02 11:13:20.123+01` |
| `setObject(PGTimestamp millis 1577960000123)` | unspecified | UTC | 1114 | `2020-01-02 10:13:20.123+00` |
| `setObject(PGTimestamp millis 1577960000123,cal)` | * | * | 1184 | `2020-01-02 20:43:20.123+10:30` |
| `setObject(LocalDateTime 2020-01-02T10:11:12)` | * | * | 1114 | `2020-01-02 10:11:12` |
| `setObject(OffsetDateTime 2020-01-02T10:11:12Z)` | * | * | 1184 | `2020-01-02 10:11:12+00` |

## 12. Hand-over to JDBC-DESIGN-CORE

Interfaces this layer offers:

1. Statement text: `JdbcEscapes.replace` (when escape processing is on), then
   `NativeSql.parse`; `CallEscape.parse` for `prepareCall`. Parameter indexes run over all
   statements of the string (3). CORE decides: what to do with `reWriteBatchedInserts`
   (decision 10), the property `quoteReturningIdentifiers`, `preferQueryMode`, and the NUL
   character in statement text (hand-over point 3 of JDBC-NATIVE).
2. Types: `TypeTable.find(oid, lookup)` per result column; CORE implements `TypeLookup`
   (5.3) with catalog queries and a per-connection cache, and `PGConnection.addDataType`.
3. Getters: for a non-NULL value call the function of section 7 with
   `PqResult.string(row, column)`, the column's OID, modifier and `PgType`; NULL handling
   and `wasNull` as in 7.1; `getObject` dispatches ARRAY, SQLXML and REF_CURSOR columns
   itself (7.7); the getters of 7.10 are CORE's or FEATURES'. `Statement.setMaxFieldSize`
   truncation is CORE's.
4. Setters: `SetterCalls` in the draft tests is the mapping from each `PreparedStatement`
   setter to its function (8.9); pass `oid()`, `bytes()` and `format()` of each
   `ParamValue` to `execParams`. `ConversionContext` is built per call with
   `TimeZone.getDefault()`, the connection's `TypeLookup` and the properties `stringtype`
   and `convertBooleanToNumeric`.
5. Arrays: `ArrayText.parse`, `ObjectGetters.getArray` and `ArrayFormat` are the parts of
   `java.sql.Array`, `Connection.createArrayOf` and `PGConnection.createArrayOf` (9).
6. `PSQLException` here is the first form (message, SQLState, cause). CORE's error part
   replaces the file with the complete class (server error fields, `ServerErrorMessage`)
   and takes an ordering edge on JDBC-TYPE-00A-ERRORS. `PSQLState` is complete.
7. `DateTimeText.POSITIVE_INFINITY`, `NEGATIVE_INFINITY`, `POSITIVE_SMALLER_INFINITY` must
   equal `PGStatement.DATE_POSITIVE_INFINITY`, `DATE_NEGATIVE_INFINITY`,
   `DATE_POSITIVE_SMALLER_INFINITY` of CORE's `PGStatement`.
8. Session settings CORE must ensure at connect: `DateStyle` ISO; `client_encoding` UTF8.
9. Tables kept for later designs (moved to the test resources by JDBC-TYPE-99-DRAFT-REMOVE):
   `get-other.tsv`, `get-errors.tsv`, `array-access.tsv`, `array-format.tsv`,
   `types-byname.tsv`, `types-rsmd.tsv` (ResultSetMetaData per type: CORE), `types-dbmd.tsv`
   (getTypeInfo: JDBC-DESIGN-METADATA).

Round-trip tests CORE's parts must carry (database tests in reference, oracle and server
mode; they compare class and SQLState of errors, never message texts):

- R1. Getters: for each getter group of section 7, a test that selects the `sql`
  expressions of `values.tsv` (one statement per value, session zone set as the row says)
  and compares every getter with the cell of the group's table, for the JVM default zone
  of the run. Values of user-defined types need the types of the recording (`CREATE TYPE
  mood AS ENUM ('sad', 'ok', 'happy')`, a domain `posint`, a composite `pair`, schema `s2`
  with enum `color`, extension hstore).
- R2. Setters: for each case family of `set.tsv`, `SELECT pg_typeof(?)::text` (the error
  `42P18` means OID 0) and `SELECT ?` compared with the row; plus an INSERT into a column
  of the natural type and a read back.
- R3. The NUL character: `setString("a\u0000b")` as a bare parameter, in `COALESCE(?,
  NULL::int4)` with `stringtype=unspecified`, and in an INSERT into a text column; the
  first and third must give `22021` in every mode; record the second (decision 1).
- R4. `nativeSQL` for the rows of `sql-escapes.tsv`; multi-statement strings with
  parameters and generated keys from `sql-statements.tsv` (groups split and returning),
  comparing results and update counts with reference mode.
- R5. Parameter round trips of every array type of 8.7 and of `createArrayOf` for the
  rows of `array-format.tsv`.

**Not done in this design (waived by the project owner on 2026-11-11):** the validation
slice of this layer in oracle and server mode (experiment 8). No statement of this design
was run against the stuntgres server, so no engine gap was looked for; R1 to R5 are where
gaps will show (rule R8 of the implementation parts).

## 13. Steps

Thirty-five parts. Each owns the draft files listed for it (1.3) and nothing else.

**The step of every part.** (1) Move the part's draft files to their destinations with
`git mv` (same path below `bindings/jdbc/src` as below `bindings/jdbc/draft/types/src`).
(2) Make them compile under the binding's build (`javac --release 17 -Xlint:all -Werror`,
with the javac of JDK 17 and of JDK 21: JDK 21 has more lint categories, decision 5);
wrap lines longer than 100 columns, except in the three generated case tables. (3) Where
the part says "Test to write", write that test first. (4) Run the part's test classes
with `make -C bindings/jdbc check MODE=unit TEST=<Class>[,<Class>]` and fix what fails.
The draft is a starting point; the sections named for the part and the recorded tables
are the specification. A table test must pass for every row; when a row cannot be met by
the rules of this document, stop and report (rules R8 and R11 of the part), do not edit
the table. (5) Do not touch a draft file of another part; a draft file that this part
needs but does not own is promoted by a prerequisite part.

"Moved lines" are the lines of the draft files; "new lines" estimates the test the part
writes and the fixes.

| Part | Prerequisites besides JDBC-DESIGN-TYPES | Moved lines (main + test) | New lines (estimate) |
|---|---|---|---|
| `TYPE-00A-ERRORS` | BUILD-02-RUNNER | 180 + 0 | 110 |
| `PGOBJ-01A-OBJECT` | BUILD-02-RUNNER, TYPE-00A-ERRORS | 364 + 0 | 20 |
| `PGOBJ-01B-INTERVAL` | BUILD-02-RUNNER, PGOBJ-01A-OBJECT | 456 + 0 | 20 |
| `TYPE-00B-TESTDATA` | BUILD-02-RUNNER, PGOBJ-01A-OBJECT | 0 + 262 | 110 |
| `PGOBJ-01C-TEST` | BUILD-02-RUNNER, PGOBJ-01B-INTERVAL, TYPE-00B-TESTDATA | 0 + 179 | 20 |
| `PGOBJ-02A-POINT` | BUILD-02-RUNNER, PGOBJ-01A-OBJECT | 367 + 0 | 20 |
| `PGOBJ-02B-SHAPES` | BUILD-02-RUNNER, PGOBJ-02A-POINT | 420 + 0 | 20 |
| `PGOBJ-02C-PATHS` | BUILD-02-RUNNER, PGOBJ-02B-SHAPES, TYPE-00B-TESTDATA | 274 + 199 | 20 |
| `TYPE-01-OIDS` | BUILD-02-RUNNER | 343 + 0 | 110 |
| `TYPE-02-TABLE` | BUILD-02-RUNNER, TYPE-01-OIDS | 355 + 0 | 20 |
| `TYPE-03-MATRIX` | BUILD-02-RUNNER, TYPE-02-TABLE, TYPE-00B-TESTDATA | 0 + 270 | 20 |
| `SQL-01-SCANNER` | BUILD-02-RUNNER | 277 + 87 | 20 |
| `SQL-02-KINDS` | BUILD-02-RUNNER | 72 + 0 | 20 |
| `SQL-03-STATEMENTS` | BUILD-02-RUNNER, SQL-01-SCANNER, SQL-02-KINDS, TYPE-00A-ERRORS, TYPE-00B-TESTDATA | 288 + 108 | 20 |
| `SQL-04-FUNCTIONS` | BUILD-02-RUNNER, TYPE-00A-ERRORS | 188 + 0 | 110 |
| `SQL-05-ESCAPES` | BUILD-02-RUNNER, SQL-01-SCANNER, SQL-03-STATEMENTS, SQL-04-FUNCTIONS, TYPE-00B-TESTDATA | 206 + 140 | 20 |
| `SQL-06-CALL` | BUILD-02-RUNNER, SQL-01-SCANNER, TYPE-00A-ERRORS, TYPE-00B-TESTDATA | 162 + 61 | 20 |
| `CODEC-01-NUMBER-BYTEA` | BUILD-02-RUNNER, TYPE-00A-ERRORS | 304 + 0 | 110 |
| `CODEC-02-DATETIME` | BUILD-02-RUNNER, TYPE-00A-ERRORS | 258 + 0 | 110 |
| `CODEC-03-HSTORE` | BUILD-02-RUNNER | 92 + 0 | 110 |
| `GET-01-TEXT` | BUILD-02-RUNNER, CODEC-01-NUMBER-BYTEA, TYPE-03-MATRIX | 72 + 57 | 20 |
| `GET-02-INTEGER` | BUILD-02-RUNNER, CODEC-01-NUMBER-BYTEA, TYPE-03-MATRIX | 123 + 81 | 20 |
| `GET-03-DECIMAL` | BUILD-02-RUNNER, GET-02-INTEGER | 90 + 64 | 20 |
| `GET-04-DATETIME` | BUILD-02-RUNNER, CODEC-02-DATETIME, TYPE-03-MATRIX | 156 + 86 | 20 |
| `GET-05-JAVATIME` | BUILD-02-RUNNER, CODEC-02-DATETIME, TYPE-03-MATRIX | 169 + 79 | 20 |
| `ARRAY-01-PARSE` | BUILD-02-RUNNER, TYPE-00A-ERRORS | 131 + 0 | 110 |
| `GET-06-OBJECT` | BUILD-02-RUNNER, GET-01-TEXT, GET-03-DECIMAL, GET-04-DATETIME, ARRAY-01-PARSE, CODEC-03-HSTORE, PGOBJ-01B-INTERVAL, PGOBJ-02C-PATHS, TYPE-03-MATRIX | 284 + 115 | 20 |
| `GET-07-CLASS` | BUILD-02-RUNNER, GET-05-JAVATIME, GET-06-OBJECT | 175 + 114 | 20 |
| `SET-01-BASIC` | BUILD-02-RUNNER, TYPE-02-TABLE, CODEC-01-NUMBER-BYTEA, TYPE-00A-ERRORS | 245 + 0 | 110 |
| `SET-02-DATETIME` | BUILD-02-RUNNER, CODEC-02-DATETIME, PGOBJ-01A-OBJECT, TYPE-01-OIDS | 277 + 0 | 110 |
| `SET-03-STREAM` | BUILD-02-RUNNER, SET-01-BASIC | 165 + 0 | 110 |
| `ARRAY-02-FORMAT` | BUILD-02-RUNNER, CODEC-01-NUMBER-BYTEA, TYPE-01-OIDS, PGOBJ-02A-POINT | 105 + 72 | 20 |
| `SET-04-OBJECT` | BUILD-02-RUNNER, SET-01-BASIC, SET-02-DATETIME, SET-03-STREAM, ARRAY-02-FORMAT, CODEC-03-HSTORE, GET-04-DATETIME | 336 + 0 | 20 |
| `SET-05-TABLE` | BUILD-02-RUNNER, SET-04-OBJECT, TYPE-03-MATRIX, PGOBJ-01B-INTERVAL, PGOBJ-02C-PATHS | 0 + 771 | 20 |
| `TYPE-99-DRAFT-REMOVE` | every other part of this table | 0 + 0 | 0 |

#### JDBC-TYPE-00A-ERRORS

PSQLState, the first form of PSQLException and the conversion errors.

Files to move with `git mv` (draft → destination):

- `bindings/jdbc/draft/types/src/main/java/org/stuntgres/util/PSQLState.java` → `bindings/jdbc/src/main/java/org/stuntgres/util/PSQLState.java`
- `bindings/jdbc/draft/types/src/main/java/org/stuntgres/util/PSQLException.java` → `bindings/jdbc/src/main/java/org/stuntgres/util/PSQLException.java`
- `bindings/jdbc/draft/types/src/main/java/org/stuntgres/core/ConversionErrors.java` → `bindings/jdbc/src/main/java/org/stuntgres/core/ConversionErrors.java`

Design sections: 7.9 Errors of the conversion code; 12. Hand-over to JDBC-DESIGN-CORE.

Names to provide: the non-private members listed for `PSQLState`, `PSQLException`, `ConversionErrors` in the cited sections.

Test to write (contract only: the draft has no test file for it; the expected values below were recorded or follow the cited rules; where one is wrong, rule R11 applies).

Write `src/test/java/org/stuntgres/core/ConversionErrorsTest.java` (`@UnitTest`, package `org.stuntgres.core`) with these cases:
- `states`: `PSQLState.NUMERIC_VALUE_OUT_OF_RANGE.getState()` is `22003`; `UNKNOWN_STATE.getState()` is the empty string; `PSQLState.values().length` is 56; `PSQLState.isConnectionError("08006")` is true, `isConnectionError("57014")` and `isConnectionError(null)` are false.
- `exception`: `new PSQLException("m", PSQLState.SYNTAX_ERROR)` has message `m`, SQLState `42601`, no cause; with a cause the cause is returned; with state `null` the SQLState is null.
- `messages`: `ConversionErrors.badValue("int", "x")` has SQLState `22003` and message `Cannot read the value "x" as int.`; `notBoolean("2")`: `42846`, `Cannot convert the value "2" to boolean.`; `badDateTime("a date", "x")`: `22007`, `Cannot read the value "x" as a date.`; `unsupportedClass(String.class, "int4")`: `22023`, `A column of type int4 cannot be read as java.lang.String.`; `wrongColumnType(java.time.LocalDate.class, "text")`: `42821`, `A column of type text cannot be read as java.time.LocalDate.`; `badParameter(new Object(), "int")`: `07006`, `Cannot send a value of class java.lang.Object as int.`; `notImplemented("getRef(int)")` is a `java.sql.SQLFeatureNotSupportedException` with SQLState `0A000` and message `The method getRef(int) is not implemented.`

#### JDBC-PGOBJ-01A-OBJECT

Value classes PGobject, PGmoney, PGTime and PGTimestamp.

Files to move with `git mv` (draft → destination):

- `bindings/jdbc/draft/types/src/main/java/org/stuntgres/util/PGobject.java` → `bindings/jdbc/src/main/java/org/stuntgres/util/PGobject.java`
- `bindings/jdbc/draft/types/src/main/java/org/stuntgres/util/PGmoney.java` → `bindings/jdbc/src/main/java/org/stuntgres/util/PGmoney.java`
- `bindings/jdbc/draft/types/src/main/java/org/stuntgres/util/PGTime.java` → `bindings/jdbc/src/main/java/org/stuntgres/util/PGTime.java`
- `bindings/jdbc/draft/types/src/main/java/org/stuntgres/util/PGTimestamp.java` → `bindings/jdbc/src/main/java/org/stuntgres/util/PGTimestamp.java`

Design sections: 10.1 Public API; 10.2 Behavior.

Names to provide: the non-private members listed for `PGobject`, `PGmoney`, `PGTime`, `PGTimestamp` in the cited sections.

The test of these classes is the table test of JDBC-PGOBJ-01C-TEST; this part adds no test file. Check: the classes compile with the javac of JDK 17 and of JDK 21 without a warning (make check MODE=unit under both).

#### JDBC-PGOBJ-01B-INTERVAL

Value class PGInterval.

Files to move with `git mv` (draft → destination):

- `bindings/jdbc/draft/types/src/main/java/org/stuntgres/util/PGInterval.java` → `bindings/jdbc/src/main/java/org/stuntgres/util/PGInterval.java`

Design sections: 10.1 Public API; 10.2 Behavior.

Names to provide: the non-private members listed for `PGInterval` in the cited sections.

The test of this class is the table test of JDBC-PGOBJ-01C-TEST; this part adds no test file.

#### JDBC-TYPE-00B-TESTDATA

Test support for the recorded tables: TestData and Repr.

Files to move with `git mv` (draft → destination):

- `bindings/jdbc/draft/types/src/test/java/org/stuntgres/core/TestData.java` → `bindings/jdbc/src/test/java/org/stuntgres/core/TestData.java`
- `bindings/jdbc/draft/types/src/test/java/org/stuntgres/core/Repr.java` → `bindings/jdbc/src/test/java/org/stuntgres/core/Repr.java`

Design sections: 1.4 The recorded tables; 7.1 Contract and the getter tables.

Test to write (contract only: the draft has no test file for it; the expected values below were recorded or follow the cited rules; where one is wrong, rule R11 applies).

Write `src/test/java/org/stuntgres/core/TestDataTest.java` (`@UnitTest`) with these cases:
- `escapes`: `TestData.unescape("a\\tb\\nc\\\\d\\u00e9")` (the cell text `a\tb\nc\\d\u00e9`) is `a`, tab, `b`, line feed, `c`, backslash, `d`, `é`; `unescape("\\N")` (the cell `\N`) is null; `escape(null)` is the cell `\N`; `escape` of a string with a tab, a line feed, a backslash and U+0001 gives `\t`, `\n`, `\\` and `\u0001`; `escape("\uD83D\uDE00")` returns the string unchanged and `escape("\uD83D")` gives `\ud83d`.
- `repr`: `Repr.of(null)` is `null`; `Repr.of("a\tb")` is `String:a\tb`; `Repr.of(5)` is `Integer:5`; `Repr.of(1.5d)` is `Double:0x1.8p0`; `Repr.of(1.5f)` is `Float:0x1.8p0`; `Repr.of(new byte[] {0, -1})` is `byte[]:00ff`; `Repr.of(new java.sql.Timestamp(5))` is `Timestamp:5/5000000`; `Repr.of(new java.sql.Date(7))` is `Date:7`; `Repr.of(new Integer[] {1, null})` is `Integer[Integer:1,null]`; `Repr.of(java.time.LocalDate.of(2020, 1, 2))` is `LocalDate:2020-01-02`; a `PGobject` with type `json` and value `{}` gives `org.stuntgres.util.PGobject:json:{}`; `Repr.of(new Repr.Raw("x"))` is `x`; `Repr.error(new java.sql.SQLException("m", "22003"))` is `!SQLException|22003`; `Repr.error(new IllegalStateException())` is `!IllegalStateException|-`; `Repr.hex(new byte[] {10})` is `0a`.

#### JDBC-PGOBJ-01C-TEST

Table test of the value classes of org.stuntgres.util.

Files to move with `git mv` (draft → destination):

- `bindings/jdbc/draft/types/src/test/java/org/stuntgres/util/ValueClassesTest.java` → `bindings/jdbc/src/test/java/org/stuntgres/util/ValueClassesTest.java` (generated case table)
- `bindings/jdbc/draft/types/src/test/resources/org/stuntgres/core/value-classes-util.tsv` → `bindings/jdbc/src/test/resources/org/stuntgres/core/value-classes-util.tsv`

Design sections: 10.2 Behavior; 10.4 Tests.

Test classes (draft, already written; they must pass): `ValueClassesTest`. Their table tests run one check per row of the resources above; their other cases hold literal expectations.

#### JDBC-PGOBJ-02A-POINT

Geometric value classes: the text helper, PGpoint and PGlseg.

Files to move with `git mv` (draft → destination):

- `bindings/jdbc/draft/types/src/main/java/org/stuntgres/geometric/GeometryText.java` → `bindings/jdbc/src/main/java/org/stuntgres/geometric/GeometryText.java`
- `bindings/jdbc/draft/types/src/main/java/org/stuntgres/geometric/PGpoint.java` → `bindings/jdbc/src/main/java/org/stuntgres/geometric/PGpoint.java`
- `bindings/jdbc/draft/types/src/main/java/org/stuntgres/geometric/PGlseg.java` → `bindings/jdbc/src/main/java/org/stuntgres/geometric/PGlseg.java`

Design sections: 10.1 Public API; 10.2 Behavior.

Names to provide: the non-private members listed for `GeometryText`, `PGpoint`, `PGlseg` in the cited sections.

The test of these classes is the table test of JDBC-PGOBJ-02C-PATHS; this part adds no test file.

#### JDBC-PGOBJ-02B-SHAPES

Geometric value classes: PGbox, PGcircle and PGline.

Files to move with `git mv` (draft → destination):

- `bindings/jdbc/draft/types/src/main/java/org/stuntgres/geometric/PGbox.java` → `bindings/jdbc/src/main/java/org/stuntgres/geometric/PGbox.java`
- `bindings/jdbc/draft/types/src/main/java/org/stuntgres/geometric/PGcircle.java` → `bindings/jdbc/src/main/java/org/stuntgres/geometric/PGcircle.java`
- `bindings/jdbc/draft/types/src/main/java/org/stuntgres/geometric/PGline.java` → `bindings/jdbc/src/main/java/org/stuntgres/geometric/PGline.java`

Design sections: 10.1 Public API; 10.2 Behavior.

Names to provide: the non-private members listed for `PGbox`, `PGcircle`, `PGline` in the cited sections.

The test of these classes is the table test of JDBC-PGOBJ-02C-PATHS; this part adds no test file.

#### JDBC-PGOBJ-02C-PATHS

Geometric value classes: PGpath, PGpolygon and the table test of the package.

Files to move with `git mv` (draft → destination):

- `bindings/jdbc/draft/types/src/main/java/org/stuntgres/geometric/PGpath.java` → `bindings/jdbc/src/main/java/org/stuntgres/geometric/PGpath.java`
- `bindings/jdbc/draft/types/src/main/java/org/stuntgres/geometric/PGpolygon.java` → `bindings/jdbc/src/main/java/org/stuntgres/geometric/PGpolygon.java`
- `bindings/jdbc/draft/types/src/test/java/org/stuntgres/geometric/GeometricClassesTest.java` → `bindings/jdbc/src/test/java/org/stuntgres/geometric/GeometricClassesTest.java` (generated case table)
- `bindings/jdbc/draft/types/src/test/resources/org/stuntgres/core/value-classes-geometric.tsv` → `bindings/jdbc/src/test/resources/org/stuntgres/core/value-classes-geometric.tsv`

Design sections: 10.1 Public API; 10.2 Behavior; 10.4 Tests.

Names to provide: the non-private members listed for `PGpath`, `PGpolygon` in the cited sections.

Test classes (draft, already written; they must pass): `GeometricClassesTest`. Their table tests run one check per row of the resources above; their other cases hold literal expectations.

#### JDBC-TYPE-01-OIDS

OID constants, PgType, TypeLookup and ConversionContext.

Files to move with `git mv` (draft → destination):

- `bindings/jdbc/draft/types/src/main/java/org/stuntgres/core/Oid.java` → `bindings/jdbc/src/main/java/org/stuntgres/core/Oid.java`
- `bindings/jdbc/draft/types/src/main/java/org/stuntgres/core/PgType.java` → `bindings/jdbc/src/main/java/org/stuntgres/core/PgType.java`
- `bindings/jdbc/draft/types/src/main/java/org/stuntgres/core/TypeLookup.java` → `bindings/jdbc/src/main/java/org/stuntgres/core/TypeLookup.java`
- `bindings/jdbc/draft/types/src/main/java/org/stuntgres/core/ConversionContext.java` → `bindings/jdbc/src/main/java/org/stuntgres/core/ConversionContext.java`

Design sections: 5.1 The type table; 5.3 The lookup interface; 5.4 Tests; 12. Hand-over to JDBC-DESIGN-CORE.

Names to provide: the non-private members listed for `Oid`, `PgType`, `TypeLookup`, `ConversionContext` in the cited sections.

Test to write (contract only: the draft has no test file for it; the expected values below were recorded or follow the cited rules; where one is wrong, rule R11 applies).

Write `src/test/java/org/stuntgres/core/TypeBasicsTest.java` (`@UnitTest`) with these cases:
- `oids`: `Oid.UNSPECIFIED` 0, `BOOL` 16, `BYTEA` 17, `CHAR` 18, `NAME` 19, `INT8` 20, `INT2` 21, `INT4` 23, `TEXT` 25, `OID` 26, `JSON` 114, `XML` 142, `POINT` 600, `FLOAT4` 700, `FLOAT8` 701, `MONEY` 790, `BPCHAR` 1042, `VARCHAR` 1043, `DATE` 1082, `TIME` 1083, `TIMESTAMP` 1114, `TIMESTAMPTZ` 1184, `INTERVAL` 1186, `TIMETZ` 1266, `BIT` 1560, `VARBIT` 1562, `NUMERIC` 1700, `REFCURSOR` 1790, `UUID` 2950, `JSONB` 3802, `INT4_ARRAY` 1007, `TEXT_ARRAY` 1009, `VARCHAR_ARRAY` 1015, `BYTEA_ARRAY` 1001, `NUMERIC_ARRAY` 1231, `UUID_ARRAY` 2951.
- `pgType`: `new PgType(23, "int4", 4, "java.lang.Integer", 1007, 0, ',')` returns its seven arguments from `oid()`, `name()`, `sqlType()`, `className()`, `arrayOid()`, `elementOid()`, `delimiter()` and `isArray()` false; with `elementOid` 23 `isArray()` is true.
- `context`: a `ConversionContext` returns its four constructor arguments from `defaultZone()`, `types()`, `stringTypeUnspecified()`, `convertBooleanToNumeric()`.

#### JDBC-TYPE-02-TABLE

The type table and the rules for precision, scale and display size.

Files to move with `git mv` (draft → destination):

- `bindings/jdbc/draft/types/src/main/java/org/stuntgres/core/TypeTable.java` → `bindings/jdbc/src/main/java/org/stuntgres/core/TypeTable.java`

Design sections: 5.1 The type table; 5.2 Precision, scale and display size; 5.4 Tests.

Names to provide: the non-private members listed for `TypeTable` in the cited sections.

The test of this class is TypeTableTest of JDBC-TYPE-03-MATRIX (it needs the lookup of the test support); this part adds no test file.

#### JDBC-TYPE-03-MATRIX

Test support Matrix, the recorded values and the test of the type table.

Files to move with `git mv` (draft → destination):

- `bindings/jdbc/draft/types/src/test/java/org/stuntgres/core/Matrix.java` → `bindings/jdbc/src/test/java/org/stuntgres/core/Matrix.java`
- `bindings/jdbc/draft/types/src/test/java/org/stuntgres/core/TypeTableTest.java` → `bindings/jdbc/src/test/java/org/stuntgres/core/TypeTableTest.java`
- `bindings/jdbc/draft/types/src/test/resources/org/stuntgres/core/values.tsv` → `bindings/jdbc/src/test/resources/org/stuntgres/core/values.tsv`
- `bindings/jdbc/draft/types/src/test/resources/org/stuntgres/core/types.tsv` → `bindings/jdbc/src/test/resources/org/stuntgres/core/types.tsv`
- `bindings/jdbc/draft/types/src/test/resources/org/stuntgres/core/types-typmod.tsv` → `bindings/jdbc/src/test/resources/org/stuntgres/core/types-typmod.tsv`
- `bindings/jdbc/draft/types/src/test/resources/org/stuntgres/core/types-user.tsv` → `bindings/jdbc/src/test/resources/org/stuntgres/core/types-user.tsv`

Design sections: 1.4 The recorded tables; 5.4 Tests; 7.1 Contract and the getter tables.

Test classes (draft, already written; they must pass): `TypeTableTest`. Their table tests run one check per row of the resources above; their other cases hold literal expectations.

#### JDBC-SQL-01-SCANNER

The lexical scanner of statement text.

Files to move with `git mv` (draft → destination):

- `bindings/jdbc/draft/types/src/main/java/org/stuntgres/core/SqlScanner.java` → `bindings/jdbc/src/main/java/org/stuntgres/core/SqlScanner.java`
- `bindings/jdbc/draft/types/src/test/java/org/stuntgres/core/SqlScannerTest.java` → `bindings/jdbc/src/test/java/org/stuntgres/core/SqlScannerTest.java`

Design sections: 2.1 Tokens; 2.2 Rules that differ from the server's grammar (all reproduce pgjdbc); 2.3 Tests.

Names to provide: the non-private members listed for `SqlScanner` in the cited sections.

Test classes (draft, already written; they must pass): `SqlScannerTest`. Their table tests run one check per row of the resources above; their other cases hold literal expectations.

#### JDBC-SQL-02-KINDS

CommandKind and NativeStatement.

Files to move with `git mv` (draft → destination):

- `bindings/jdbc/draft/types/src/main/java/org/stuntgres/core/CommandKind.java` → `bindings/jdbc/src/main/java/org/stuntgres/core/CommandKind.java`
- `bindings/jdbc/draft/types/src/main/java/org/stuntgres/core/NativeStatement.java` → `bindings/jdbc/src/main/java/org/stuntgres/core/NativeStatement.java`

Design sections: 3.3 Command kinds.

Names to provide: the non-private members listed for `CommandKind`, `NativeStatement` in the cited sections.

These two types are tested through NativeSqlTest of JDBC-SQL-03-STATEMENTS; this part adds no test file.

#### JDBC-SQL-03-STATEMENTS

The statement parser: placeholders, splitting, command kinds, RETURNING.

Files to move with `git mv` (draft → destination):

- `bindings/jdbc/draft/types/src/main/java/org/stuntgres/core/NativeSql.java` → `bindings/jdbc/src/main/java/org/stuntgres/core/NativeSql.java`
- `bindings/jdbc/draft/types/src/test/java/org/stuntgres/core/NativeSqlTest.java` → `bindings/jdbc/src/test/java/org/stuntgres/core/NativeSqlTest.java`
- `bindings/jdbc/draft/types/src/test/resources/org/stuntgres/core/sql-statements.tsv` → `bindings/jdbc/src/test/resources/org/stuntgres/core/sql-statements.tsv`

Design sections: 3.1 Placeholders; 3.2 Splitting; 3.3 Command kinds; 3.4 RETURNING and generated keys; 3.5 Recorded cases and tests.

Names to provide: the non-private members listed for `NativeSql` in the cited sections.

Test classes (draft, already written; they must pass): `NativeSqlTest`. Their table tests run one check per row of the resources above; their other cases hold literal expectations.

#### JDBC-SQL-04-FUNCTIONS

The translation table of the JDBC function escape.

Files to move with `git mv` (draft → destination):

- `bindings/jdbc/draft/types/src/main/java/org/stuntgres/core/EscapeFunctions.java` → `bindings/jdbc/src/main/java/org/stuntgres/core/EscapeFunctions.java`

Design sections: 4.3 The function table.

Names to provide: the non-private members listed for `EscapeFunctions` in the cited sections.

Test to write (contract only: the draft has no test file for it; the expected values below were recorded or follow the cited rules; where one is wrong, rule R11 applies).

Write `src/test/java/org/stuntgres/core/EscapeFunctionsUnitTest.java` (`@UnitTest`, package `org.stuntgres.core`: the class under test is package-private). Each case calls `EscapeFunctions.append(out, name, args)` with a fresh `StringBuilder` and a list of argument strings and checks the returned boolean and the text of `out`. Take one case per row of design table 4.3 (name, argument count, result with `a1`, ` a2`, ` a3`, ` a4` as arguments: every argument after the first starts with a space, as the escape parser passes it), for example `lcase` with `[a1]` gives true and `lower(a1)`; `LEFT` with `[a1,  a2]` gives `substring(a1 for  a2)`; `concat` with no argument gives `()`; `abs` with `[a1]` gives false and leaves `out` empty. Add the error rows of table 4.3: a wrong argument count raises SQLState `42601` with the message of the table; `timestampadd` with unit `SQL_TSI_FRAC_SECOND` raises `0A000`, with unit `DAY` raises `42601`, with unit `SQL_TSI_DAYS` raises `0A000`; `timestampdiff` with unit `SQL_TSI_WEEK` raises `42601`. The table test over `sql-functions.tsv` is EscapeFunctionsTest of JDBC-SQL-05-ESCAPES.

#### JDBC-SQL-05-ESCAPES

JDBC escape processing.

Files to move with `git mv` (draft → destination):

- `bindings/jdbc/draft/types/src/main/java/org/stuntgres/core/JdbcEscapes.java` → `bindings/jdbc/src/main/java/org/stuntgres/core/JdbcEscapes.java`
- `bindings/jdbc/draft/types/src/test/java/org/stuntgres/core/JdbcEscapesTest.java` → `bindings/jdbc/src/test/java/org/stuntgres/core/JdbcEscapesTest.java`
- `bindings/jdbc/draft/types/src/test/java/org/stuntgres/core/EscapeFunctionsTest.java` → `bindings/jdbc/src/test/java/org/stuntgres/core/EscapeFunctionsTest.java`
- `bindings/jdbc/draft/types/src/test/resources/org/stuntgres/core/sql-escapes.tsv` → `bindings/jdbc/src/test/resources/org/stuntgres/core/sql-escapes.tsv`
- `bindings/jdbc/draft/types/src/test/resources/org/stuntgres/core/sql-functions.tsv` → `bindings/jdbc/src/test/resources/org/stuntgres/core/sql-functions.tsv`

Design sections: 4.1 Escapes; 4.2 Rules; 4.3 The function table.

Names to provide: the non-private members listed for `JdbcEscapes` in the cited sections.

Test classes (draft, already written; they must pass): `JdbcEscapesTest`, `EscapeFunctionsTest`. Their table tests run one check per row of the resources above; their other cases hold literal expectations.

#### JDBC-SQL-06-CALL

The call escape of callable statements.

Files to move with `git mv` (draft → destination):

- `bindings/jdbc/draft/types/src/main/java/org/stuntgres/core/CallEscape.java` → `bindings/jdbc/src/main/java/org/stuntgres/core/CallEscape.java`
- `bindings/jdbc/draft/types/src/test/java/org/stuntgres/core/CallEscapeTest.java` → `bindings/jdbc/src/test/java/org/stuntgres/core/CallEscapeTest.java`
- `bindings/jdbc/draft/types/src/test/resources/org/stuntgres/core/sql-call.tsv` → `bindings/jdbc/src/test/resources/org/stuntgres/core/sql-call.tsv`

Design sections: 4.4 The call escape.

Names to provide: the non-private members listed for `CallEscape` in the cited sections.

Test classes (draft, already written; they must pass): `CallEscapeTest`. Their table tests run one check per row of the resources above; their other cases hold literal expectations.

#### JDBC-CODEC-01-NUMBER-BYTEA

Text codecs of booleans, numbers and bytea.

Files to move with `git mv` (draft → destination):

- `bindings/jdbc/draft/types/src/main/java/org/stuntgres/core/BooleanText.java` → `bindings/jdbc/src/main/java/org/stuntgres/core/BooleanText.java`
- `bindings/jdbc/draft/types/src/main/java/org/stuntgres/core/NumberText.java` → `bindings/jdbc/src/main/java/org/stuntgres/core/NumberText.java`
- `bindings/jdbc/draft/types/src/main/java/org/stuntgres/core/ByteaText.java` → `bindings/jdbc/src/main/java/org/stuntgres/core/ByteaText.java`

Design sections: 6.1 Booleans; 6.2 Numbers; 6.3 Money; 6.4 bytea.

Names to provide: the non-private members listed for `BooleanText`, `NumberText`, `ByteaText` in the cited sections.

Test to write (contract only: the draft has no test file for it; the expected values below were recorded or follow the cited rules; where one is wrong, rule R11 applies).

Write `src/test/java/org/stuntgres/core/NumberTextTest.java` (`@UnitTest`, package `org.stuntgres.core`) with these cases (errors are `SQLException` with the given SQLState):
- `booleans`: `BooleanText.parse` gives true for `1`, `true`, `T`, ` yes `, `Y`, `ON` and false for `0`, `FALSE`, `f`, `no`, `n`, `off`; `2`, `1.0`, the empty string and `tr` raise `42846`. `BooleanText.of` gives true for `Boolean.TRUE`, `"t"`, `'1'`, `'T'`, `'y'`, `1`, `1.0d`, `BigDecimal.ONE` and false for `'0'`, `'n'`, `0L`; `2`, `'x'` and `new Object()` raise `42846` with message `Cannot convert the value "2" to boolean.` for `2`.
- `plainInteger`: `NumberText.plainInteger("123", -128, 127)` is 123; `"1.9"` gives 1; `"-0.5"` gives 0; `"-"` gives 0; `""`, `"."`, `"-."`, `"1e3"`, `" 1"`, `"128"` (range -128 to 127) and `"9223372036854775808"` give null; `"-9223372036854775808"` with the long range gives `Long.MIN_VALUE`.
- `withoutCurrency`: `$1.50` gives `1.50`; `-$1.50` gives `-1.50`; `($1.50)` gives `-1.50`; `1.50`, `-1.50`, `$` and the empty string are returned unchanged.
- `integer`: `NumberText.integer(" 42 ", -128, 127, "byte")` is 42; `"3.99"` gives 3; `"-1e2"` gives -100; `"128"`, `"abc"` and `""` raise `22003`; the message for `"abc"` and type `int` is `Cannot read the value "abc" as int.`
- `decimal`: `NumberText.decimal(" 1.50 ")` equals `new BigDecimal("1.50")`; `"1e2"` gives `1E+2`; `"NaN"` raises `22003`. `NumberText.real("1.5")` is `1.5f`, `real("NaN")` is NaN, `doublePrecision("-Infinity")` is negative infinity, `doublePrecision("1,5")` raises `22003`.
The bytea codec is tested by TextGettersTest of JDBC-GET-01-TEXT.

#### JDBC-CODEC-02-DATETIME

Text codec of dates, times and timestamps.

Files to move with `git mv` (draft → destination):

- `bindings/jdbc/draft/types/src/main/java/org/stuntgres/core/DateTimeText.java` → `bindings/jdbc/src/main/java/org/stuntgres/core/DateTimeText.java`

Design sections: 6.5 Dates, times and timestamps; 11. Time zones and Calendar arguments.

Names to provide: the non-private members listed for `DateTimeText` in the cited sections.

Test to write (contract only: the draft has no test file for it; the expected values below were recorded or follow the cited rules; where one is wrong, rule R11 applies).

Write `src/test/java/org/stuntgres/core/DateTimeTextTest.java` (`@UnitTest`, package `org.stuntgres.core`: the fields are package-private) with these cases:
- `parse`: `2020-01-02 10:11:12.5+05:30` gives hasDate, hasTime, hasOffset true, year 2020, month 1, day 2, hour 10, minute 11, second 12, nanos 500000000, offset `+05:30`; `0044-03-15 BC` gives beforeChrist true, hasTime false; `10:11:12.123456-08` gives hasDate false, nanos 123456000, offset `-08:00`; `1900-01-01 00:00:00-04:56:02` gives offset `-04:56:02`; `2020-01-02 10:11:12 AD` is accepted; `2020-13-45` gives month 13 and day 45 (no range check).
- `errors` (SQLState `22007`, message `Cannot read the value "<text>" as a date, time or timestamp.`): the empty string, `abc`, `2020-01-02x`, `10:11`, `2020-01`, `10:11:12+19`, `2020-01-02 10:11:12 XY`.
- `millis`: for `2020-01-02 10:11:12` `millis(TimeZone UTC, true)` is 1577959872000 and with zone `Europe/Warsaw` 1577956272000; for `2020-01-02 10:11:12+05:30` it is 1577940072000 in every zone; for `10:11:12` `millis(UTC, false)` is 36672000.
- `append`: `appendDate` of (44, 3, 15) gives `0044-03-15`, of (10000, 1, 1) `10000-01-01`; `appendTime` of (1, 2, 3, 0) gives `01:02:03`, with nanos 123456000 `01:02:03.123456`, with nanos 500000000 `01:02:03.5`, with nanos 999 `01:02:03`; `appendOffset` of 0 gives `+00`, of 19800 `+05:30`, of -17762 `-04:56:02`, of -3600 `-01`.
- `constants`: `POSITIVE_INFINITY` is 9223372036825200000, `NEGATIVE_INFINITY` -9223372036832400000, `POSITIVE_SMALLER_INFINITY` 185543533774800000.

#### JDBC-CODEC-03-HSTORE

Text codec of hstore.

Files to move with `git mv` (draft → destination):

- `bindings/jdbc/draft/types/src/main/java/org/stuntgres/core/HstoreText.java` → `bindings/jdbc/src/main/java/org/stuntgres/core/HstoreText.java`

Design sections: 6.9 hstore.

Names to provide: the non-private members listed for `HstoreText` in the cited sections.

Test to write (contract only: the draft has no test file for it; the expected values below were recorded or follow the cited rules; where one is wrong, rule R11 applies).

Write `src/test/java/org/stuntgres/core/HstoreTextTest.java` (`@UnitTest`) with these cases: `HstoreText.parse("\"a\"=>\"1\", \"b\"=>NULL")` is the map a→`1`, b→null in this order; the empty string gives an empty map; `"k 1"=>"v\"x", "k2"=>""` (the value of the first pair holds an escaped quote) gives `k 1`→`v"x` and `k2`→empty string; `HstoreText.format` of the map a→`1`, b→null, `k "q"`→`v\x` (insertion order) is `"a"=>"1", "b"=>NULL, "k \"q\""=>"v\\x"` (quote and backslash escaped by a backslash); an empty map gives the empty string; `format(parse(text))` returns `text` for the first example.

#### JDBC-GET-01-TEXT

Getter conversions for strings, streams and bytes.

Files to move with `git mv` (draft → destination):

- `bindings/jdbc/draft/types/src/main/java/org/stuntgres/core/TextGetters.java` → `bindings/jdbc/src/main/java/org/stuntgres/core/TextGetters.java`
- `bindings/jdbc/draft/types/src/test/java/org/stuntgres/core/TextGettersTest.java` → `bindings/jdbc/src/test/java/org/stuntgres/core/TextGettersTest.java`
- `bindings/jdbc/draft/types/src/test/resources/org/stuntgres/core/get-string.tsv` → `bindings/jdbc/src/test/resources/org/stuntgres/core/get-string.tsv`
- `bindings/jdbc/draft/types/src/test/resources/org/stuntgres/core/get-bytes.tsv` → `bindings/jdbc/src/test/resources/org/stuntgres/core/get-bytes.tsv`

Design sections: 7.1 Contract and the getter tables; 7.4 Strings, streams and bytes.

Names to provide: the non-private members listed for `TextGetters` in the cited sections.

Test classes (draft, already written; they must pass): `TextGettersTest`. Their table tests run one check per row of the resources above; their other cases hold literal expectations.

#### JDBC-GET-02-INTEGER

Getter conversions getBoolean, getByte, getShort, getInt and getLong.

Files to move with `git mv` (draft → destination):

- `bindings/jdbc/draft/types/src/main/java/org/stuntgres/core/IntegerGetters.java` → `bindings/jdbc/src/main/java/org/stuntgres/core/IntegerGetters.java`
- `bindings/jdbc/draft/types/src/test/java/org/stuntgres/core/IntegerGettersTest.java` → `bindings/jdbc/src/test/java/org/stuntgres/core/IntegerGettersTest.java`
- `bindings/jdbc/draft/types/src/test/resources/org/stuntgres/core/get-integer.tsv` → `bindings/jdbc/src/test/resources/org/stuntgres/core/get-integer.tsv`

Design sections: 7.1 Contract and the getter tables; 7.2 Booleans and integers.

Names to provide: the non-private members listed for `IntegerGetters` in the cited sections.

Test classes (draft, already written; they must pass): `IntegerGettersTest`. Their table tests run one check per row of the resources above; their other cases hold literal expectations.

#### JDBC-GET-03-DECIMAL

Getter conversions getFloat, getDouble and getBigDecimal.

Files to move with `git mv` (draft → destination):

- `bindings/jdbc/draft/types/src/main/java/org/stuntgres/core/DecimalGetters.java` → `bindings/jdbc/src/main/java/org/stuntgres/core/DecimalGetters.java`
- `bindings/jdbc/draft/types/src/test/java/org/stuntgres/core/DecimalGettersTest.java` → `bindings/jdbc/src/test/java/org/stuntgres/core/DecimalGettersTest.java`
- `bindings/jdbc/draft/types/src/test/resources/org/stuntgres/core/get-decimal.tsv` → `bindings/jdbc/src/test/resources/org/stuntgres/core/get-decimal.tsv`

Design sections: 7.1 Contract and the getter tables; 7.3 Floating point and decimals.

Names to provide: the non-private members listed for `DecimalGetters` in the cited sections.

Test classes (draft, already written; they must pass): `DecimalGettersTest`. Their table tests run one check per row of the resources above; their other cases hold literal expectations.

#### JDBC-GET-04-DATETIME

Getter conversions getDate, getTime and getTimestamp with Calendar.

Files to move with `git mv` (draft → destination):

- `bindings/jdbc/draft/types/src/main/java/org/stuntgres/core/DateTimeGetters.java` → `bindings/jdbc/src/main/java/org/stuntgres/core/DateTimeGetters.java`
- `bindings/jdbc/draft/types/src/test/java/org/stuntgres/core/DateTimeGettersTest.java` → `bindings/jdbc/src/test/java/org/stuntgres/core/DateTimeGettersTest.java`
- `bindings/jdbc/draft/types/src/test/resources/org/stuntgres/core/get-datetime.tsv` → `bindings/jdbc/src/test/resources/org/stuntgres/core/get-datetime.tsv`

Design sections: 7.1 Contract and the getter tables; 7.5 Date, time and timestamp; 11. Time zones and Calendar arguments.

Names to provide: the non-private members listed for `DateTimeGetters` in the cited sections.

Test classes (draft, already written; they must pass): `DateTimeGettersTest`. Their table tests run one check per row of the resources above; their other cases hold literal expectations.

#### JDBC-GET-05-JAVATIME

Getter conversions for the java.time classes.

Files to move with `git mv` (draft → destination):

- `bindings/jdbc/draft/types/src/main/java/org/stuntgres/core/JavaTimeGetters.java` → `bindings/jdbc/src/main/java/org/stuntgres/core/JavaTimeGetters.java`
- `bindings/jdbc/draft/types/src/test/java/org/stuntgres/core/JavaTimeGettersTest.java` → `bindings/jdbc/src/test/java/org/stuntgres/core/JavaTimeGettersTest.java`
- `bindings/jdbc/draft/types/src/test/resources/org/stuntgres/core/get-javatime.tsv` → `bindings/jdbc/src/test/resources/org/stuntgres/core/get-javatime.tsv`

Design sections: 7.1 Contract and the getter tables; 7.6 java.time.

Names to provide: the non-private members listed for `JavaTimeGetters` in the cited sections.

Test classes (draft, already written; they must pass): `JavaTimeGettersTest`. Their table tests run one check per row of the resources above; their other cases hold literal expectations.

#### JDBC-ARRAY-01-PARSE

The parser of array text.

Files to move with `git mv` (draft → destination):

- `bindings/jdbc/draft/types/src/main/java/org/stuntgres/core/ArrayText.java` → `bindings/jdbc/src/main/java/org/stuntgres/core/ArrayText.java`

Design sections: 9.1 Array text; 9.5 Tests.

Names to provide: the non-private members listed for `ArrayText` in the cited sections.

Test to write (contract only: the draft has no test file for it; the expected values below were recorded or follow the cited rules; where one is wrong, rule R11 applies).

Write `src/test/java/org/stuntgres/core/ArrayTextTest.java` (`@UnitTest`) with these cases (`parse(text, ',')` unless a delimiter is given; the result is compared with `assertArrayEquals`):
- `{1,NULL,"a b","NULL",""}` gives `"1"`, null, `"a b"`, `"NULL"`, `""`; `{}` gives an empty array; `{ 1 , 2 }` gives `"1"`, `"2"`; `{"e\"f","g\\h"}` gives `e"f` and `g\h`; `{a\,b}` gives the one element `a,b`; `{null}` gives the string `null` (only upper-case NULL is the null element).
- `[0:2]={1,2,3}` gives `"1"`, `"2"`, `"3"`; `[2:3][0:1]={{1,2},{3,4}}` gives two `Object[]` (`"1"`, `"2"` and `"3"`, `"4"`) and `dimensions` 2; `{{{1}}}` has `dimensions` 3; `{}` has `dimensions` 1.
- with delimiter `;`: `{(1,2),(3,4);(5,6),(7,8)}` gives `(1,2),(3,4)` and `(5,6),(7,8)`.
- errors (SQLState `22000`, message `Cannot read "<text>" as an array.`): `1,2`, the empty string, `{1,2`, `{1}x`, `{"a}`.

#### JDBC-GET-06-OBJECT

The default getObject mapping and the element mapping of arrays.

Files to move with `git mv` (draft → destination):

- `bindings/jdbc/draft/types/src/main/java/org/stuntgres/core/ObjectGetters.java` → `bindings/jdbc/src/main/java/org/stuntgres/core/ObjectGetters.java`
- `bindings/jdbc/draft/types/src/test/java/org/stuntgres/core/ObjectGettersTest.java` → `bindings/jdbc/src/test/java/org/stuntgres/core/ObjectGettersTest.java`
- `bindings/jdbc/draft/types/src/test/resources/org/stuntgres/core/get-object.tsv` → `bindings/jdbc/src/test/resources/org/stuntgres/core/get-object.tsv`

Design sections: 7.1 Contract and the getter tables; 7.7 The default mapping of getObject; 9.2 Element classes.

Names to provide: the non-private members listed for `ObjectGetters` in the cited sections.

Test classes (draft, already written; they must pass): `ObjectGettersTest`. Their table tests run one check per row of the resources above; their other cases hold literal expectations.

#### JDBC-GET-07-CLASS

getObject with a class.

Files to move with `git mv` (draft → destination):

- `bindings/jdbc/draft/types/src/main/java/org/stuntgres/core/ClassGetters.java` → `bindings/jdbc/src/main/java/org/stuntgres/core/ClassGetters.java`
- `bindings/jdbc/draft/types/src/test/java/org/stuntgres/core/ClassGettersTest.java` → `bindings/jdbc/src/test/java/org/stuntgres/core/ClassGettersTest.java`

Design sections: 7.1 Contract and the getter tables; 7.8 getObject with a class.

Names to provide: the non-private members listed for `ClassGetters` in the cited sections.

Test classes (draft, already written; they must pass): `ClassGettersTest`. Their table tests run one check per row of the resources above; their other cases hold literal expectations.

#### JDBC-SET-01-BASIC

ParamValue and the setter conversions for NULL, booleans, numbers, strings and bytes.

Files to move with `git mv` (draft → destination):

- `bindings/jdbc/draft/types/src/main/java/org/stuntgres/core/ParamValue.java` → `bindings/jdbc/src/main/java/org/stuntgres/core/ParamValue.java`
- `bindings/jdbc/draft/types/src/main/java/org/stuntgres/core/BasicSetters.java` → `bindings/jdbc/src/main/java/org/stuntgres/core/BasicSetters.java`

Design sections: 8.1 Parameters and the NUL character; 8.2 setNull; 8.3 Booleans, numbers, strings and bytes.

Names to provide: the non-private members listed for `ParamValue`, `BasicSetters` in the cited sections.

Test to write (contract only: the draft has no test file for it; the expected values below were recorded or follow the cited rules; where one is wrong, rule R11 applies).

Write `src/test/java/org/stuntgres/core/BasicSettersTest.java` (`@UnitTest`) with these cases (a result is checked as OID and text; `ctx(false)` is a context with stringtype not set, `ctx(true)` with stringtype=unspecified; the lookup is an anonymous TypeLookup that knows the name `mood` as OID 16500 and nothing else):
- `param`: `new ParamValue(25, "a")` has `oid()` 25, `text()` `a`, `bytes()` the one byte 0x61 and `format()` 0; text null gives `bytes()` null and `format()` 0; the text `a`, NUL, `b` gives `format()` 1 and three bytes.
- `nulls`: one assertion per row of design table 8.2, for example `setNull(Types.INTEGER)` gives OID 23 and text null, `setNull(Types.VARCHAR)` 1043 with `ctx(false)` and 0 with `ctx(true)`, `setNull(Types.TIMESTAMP)` 0, `setNull(Types.BLOB)` 26; `setNull(Types.REF)`, `setNull(Types.NCHAR)` and `setNull(12345)` raise SQLState `07006` with message `The java.sql.Types value 12345 has no PostgreSQL type.` for 12345. `setNull(Types.OTHER, "json")` gives 114; `setNull(Types.ARRAY, "int4[]")` 1007; `setNull(Types.VARCHAR, "mood")` 16500; `setNull(Types.INTEGER, "nosuchtype")` 0; `setNull(Types.VARCHAR, null)` as `setNull(Types.VARCHAR)`.
- `values`: `setBoolean(true)` gives 16 `TRUE`, false `FALSE`; `setShort((short) -5)` 21 `-5`; `setInt(Integer.MIN_VALUE)` 23 `-2147483648`; `setLong(Long.MAX_VALUE)` 20 `9223372036854775807`; `setFloat(1.5f)` 700 `1.5`; `setFloat(Float.NaN)` 700 `NaN`; `setDouble(Double.NEGATIVE_INFINITY)` 701 `-Infinity`; `setBigDecimal(new BigDecimal("1.50"))` 1700 `1.50`; `new BigDecimal("1E+3")` gives `1000`; `new BigDecimal("1E-20")` gives `1E-20`; `setBigDecimal(null)` gives 1700 and text null; `setString("abc", ctx(false))` 1043 `abc`, with `ctx(true)` 0 `abc`; `setString(null, ...)` text null; `setBytes(new byte[] {0x61, 0, -1})` 17 `\x6100ff`; `setBytes(null)` 17 and text null.
Every row of the recorded table set.tsv is checked by SettersTest of JDBC-SET-05-TABLE.

#### JDBC-SET-02-DATETIME

Setter conversions for java.sql.Date, Time, Timestamp and the java.time classes.

Files to move with `git mv` (draft → destination):

- `bindings/jdbc/draft/types/src/main/java/org/stuntgres/core/DateTimeSetters.java` → `bindings/jdbc/src/main/java/org/stuntgres/core/DateTimeSetters.java`

Design sections: 8.4 java.sql.Date, Time and Timestamp; 8.5 java.time; 11. Time zones and Calendar arguments.

Names to provide: the non-private members listed for `DateTimeSetters` in the cited sections.

Test to write (contract only: the draft has no test file for it; the expected values below were recorded or follow the cited rules; where one is wrong, rule R11 applies).

Write `src/test/java/org/stuntgres/core/DateTimeSettersTest.java` (`@UnitTest`) with these cases (the context has the named default zone; `cal` is `new GregorianCalendar(TimeZone.getTimeZone("Australia/Adelaide"))`; a result is OID and text):
- `timestamps`: `new Timestamp(1577960000123L)` gives OID 0 and `2020-01-02 10:13:20.123+00` in zone UTC, `2020-01-02 11:13:20.123+01` in zone Europe/Warsaw and `2020-01-02 20:43:20.123+10:30` with `cal`; a `PGTimestamp(1577960000123L)` gives OID 1114 and the same texts without `cal`; a `PGTimestamp(1577960000123L, cal)` gives OID 1184 and `2020-01-02 20:43:20.123+10:30` whatever the Calendar argument is; a Timestamp of millis 1577959872000 with nanos 123456789 gives `2020-01-02 10:11:12.123457+00` (UTC), with nanos 999999500 gives `2020-01-02 10:11:13+00`; `new Timestamp(-63517824000000L)` in zone Europe/Warsaw gives `0044-03-17 01:00:00+01 BC`; millis `DateTimeText.POSITIVE_INFINITY` gives `infinity`, `NEGATIVE_INFINITY` `-infinity`; null gives OID 0 and text null.
- `dates`: `new Date(1577960000123L)` gives OID 0 and `2020-01-02 +00` (UTC), `2020-01-02 +01` (Europe/Warsaw), `2020-01-02 +10:30` (`cal`); `new Date(0)` in Europe/Warsaw gives `1970-01-01 +01`; `new Date(Long.MIN_VALUE)` in Europe/Warsaw gives `292269055-12-02 BC +01`; the infinity values give `infinity` and `-infinity`; null gives OID 1082 and text null.
- `times`: `new Time(43200123L)` gives OID 0 and `12:00:00.123+00` (UTC), `13:00:00.123+01` (Europe/Warsaw), `21:30:00.123+09:30` (`cal`); `new PGTime(43200123L)` gives OID 1083, `new PGTime(43200123L, cal)` OID 1266 and `21:30:00.123+09:30`; null gives OID 0 and text null.
- `javaTime`: `LocalDate.of(2020, 1, 2)` gives 1082 `2020-01-02`; `LocalDate.of(-43, 3, 15)` `0044-03-15 BC`; `LocalDate.of(0, 12, 31)` `0001-12-31 BC`; `LocalDate.MAX` `infinity`; `LocalDate.MIN` `-infinity`; `LocalTime.parse("10:11:12.1234565")` gives 1083 `10:11:12.123457`; `LocalTime.parse("23:59:59.9999995")` and `LocalTime.MAX` give `24:00:00`; `LocalDateTime.parse("2020-01-02T10:11:12.123456789")` gives 1114 `2020-01-02 10:11:12.123457`; `LocalDateTime.MAX` `infinity`, `LocalDateTime.MIN` `-infinity`; `OffsetDateTime.parse("2020-01-02T10:11:12.123456789+05:30")` gives 1184 `2020-01-02 10:11:12.123457+05:30`; `OffsetDateTime.parse("2020-01-02T10:11:12+01:02:03")` gives `2020-01-02 10:11:12+01:02:03`; `OffsetDateTime.MAX` `infinity`; `OffsetTime.parse("10:11:12.123456789+05:30")` gives 1266 `10:11:12.123456+05:30` (cut, not rounded).
Every row of the recorded table set.tsv is checked by SettersTest of JDBC-SET-05-TABLE.

#### JDBC-SET-03-STREAM

Setter conversions for streams and readers.

Files to move with `git mv` (draft → destination):

- `bindings/jdbc/draft/types/src/main/java/org/stuntgres/core/StreamSetters.java` → `bindings/jdbc/src/main/java/org/stuntgres/core/StreamSetters.java`

Design sections: 8.6 Streams and readers.

Names to provide: the non-private members listed for `StreamSetters` in the cited sections.

Test to write (contract only: the draft has no test file for it; the expected values below were recorded or follow the cited rules; where one is wrong, rule R11 applies).

Write `src/test/java/org/stuntgres/core/StreamSettersTest.java` (`@UnitTest`) with these cases (streams over the UTF-8 bytes of the given text; `ctx(false)` and `ctx(true)` as in JDBC-SET-01-BASIC):
- `ascii`: `setAsciiStream` of `abc` with length 3 gives OID 1043 and `abc`; length 2 gives `ab`; length 5 gives `abc`; length 0 gives the empty string; length -1 raises SQLState `22023` with message `The stream length -1 is not valid.`; a null stream gives text null with OID 1043 for `ctx(false)` and 0 for `ctx(true)`; a non-null stream gives OID 1043 for both contexts.
- `unicode`: `setUnicodeStream` of the text `a`, U+00E9, U+20AC, U+1F600 with length 3 gives 1043 and the first three characters.
- `characters`: `setCharacterStream(new StringReader("abc"), 2, ctx(false))` gives 1043 `ab`, with `ctx(true)` 0 `ab`; length 5 gives `abc`; length -1 raises `22023`; `setCharacterStream(new StringReader("abc"), ctx(false))` and the same with `ctx(true)` give OID 25 and `abc`; a null reader gives text null as `setString(null)`.
- `binary`: `setBinaryStream` of `abc` with length -1 (no length given) gives 17 `\x616263`; length 3 the same; length 2 `\x6162`; length 0 `\x`; length 5 raises `22023`; length -2 raises `22023`; a null stream gives OID 17 and text null.
Every row of the recorded table set.tsv is checked by SettersTest of JDBC-SET-05-TABLE.

#### JDBC-ARRAY-02-FORMAT

The formatter of array text.

Files to move with `git mv` (draft → destination):

- `bindings/jdbc/draft/types/src/main/java/org/stuntgres/core/ArrayFormat.java` → `bindings/jdbc/src/main/java/org/stuntgres/core/ArrayFormat.java`
- `bindings/jdbc/draft/types/src/test/java/org/stuntgres/core/ArrayFormatTest.java` → `bindings/jdbc/src/test/java/org/stuntgres/core/ArrayFormatTest.java`

Design sections: 9.3 Writing arrays; 9.5 Tests.

Names to provide: the non-private members listed for `ArrayFormat` in the cited sections.

Test classes (draft, already written; they must pass): `ArrayFormatTest`. Their table tests run one check per row of the resources above; their other cases hold literal expectations.

#### JDBC-SET-04-OBJECT

setObject without and with a target type.

Files to move with `git mv` (draft → destination):

- `bindings/jdbc/draft/types/src/main/java/org/stuntgres/core/ObjectSetters.java` → `bindings/jdbc/src/main/java/org/stuntgres/core/ObjectSetters.java`

Design sections: 8.7 setObject by the class of the value; 8.8 setObject with a target type.

Names to provide: the non-private members listed for `ObjectSetters` in the cited sections.

Every row of the recorded table set.tsv for this class is checked by SettersTest of JDBC-SET-05-TABLE, which is the test of this part too; this part adds no test file. Check that the class compiles and that `make check MODE=unit` stays green.

#### JDBC-SET-05-TABLE

The table test of the setters: every recorded case of set.tsv.

Files to move with `git mv` (draft → destination):

- `bindings/jdbc/draft/types/src/test/java/org/stuntgres/core/SetterCalls.java` → `bindings/jdbc/src/test/java/org/stuntgres/core/SetterCalls.java`
- `bindings/jdbc/draft/types/src/test/java/org/stuntgres/core/SetterCases.java` → `bindings/jdbc/src/test/java/org/stuntgres/core/SetterCases.java` (generated case table)
- `bindings/jdbc/draft/types/src/test/java/org/stuntgres/core/SettersTest.java` → `bindings/jdbc/src/test/java/org/stuntgres/core/SettersTest.java`
- `bindings/jdbc/draft/types/src/test/resources/org/stuntgres/core/set.tsv` → `bindings/jdbc/src/test/resources/org/stuntgres/core/set.tsv`

Design sections: 8.9 The setter table and its test.

Test classes (draft, already written; they must pass): `SettersTest`. Their table tests run one check per row of the resources above; their other cases hold literal expectations.

#### JDBC-TYPE-99-DRAFT-REMOVE

Remove the draft directory of the type layer and keep its hand-over tables.

Files to move with `git mv` (draft → destination):

- `bindings/jdbc/draft/types/src/test/resources/org/stuntgres/core/get-errors.tsv` → `bindings/jdbc/src/test/resources/org/stuntgres/core/get-errors.tsv`
- `bindings/jdbc/draft/types/src/test/resources/org/stuntgres/core/get-other.tsv` → `bindings/jdbc/src/test/resources/org/stuntgres/core/get-other.tsv`
- `bindings/jdbc/draft/types/src/test/resources/org/stuntgres/core/array-format.tsv` → `bindings/jdbc/src/test/resources/org/stuntgres/core/array-format.tsv`
- `bindings/jdbc/draft/types/src/test/resources/org/stuntgres/core/array-access.tsv` → `bindings/jdbc/src/test/resources/org/stuntgres/core/array-access.tsv`
- `bindings/jdbc/draft/types/src/test/resources/org/stuntgres/core/types-byname.tsv` → `bindings/jdbc/src/test/resources/org/stuntgres/core/types-byname.tsv`
- `bindings/jdbc/draft/types/src/test/resources/org/stuntgres/core/types-rsmd.tsv` → `bindings/jdbc/src/test/resources/org/stuntgres/core/types-rsmd.tsv`
- `bindings/jdbc/draft/types/src/test/resources/org/stuntgres/core/types-dbmd.tsv` → `bindings/jdbc/src/test/resources/org/stuntgres/core/types-dbmd.tsv`

Design sections: 1.3 Files and owner parts; 1.4 The recorded tables; 12. Hand-over to JDBC-DESIGN-CORE.

No Java file. Move the seven tables named under Files with `git mv` to `bindings/jdbc/src/test/resources/org/stuntgres/core/` (later design parts cite them there), delete `bindings/jdbc/draft/types/DRAFT.md` with `git rm`, and check that `bindings/jdbc/draft` no longer exists and that no file of the repository mentions `draft/types` except docs/design/JDBC-TYPES.md.

## 14. Experiments behind this design

Scratch directory `.cswd/tmp/JDBC-DESIGN-TYPES/` (not in the repository): `ref/` holds the
probe programs (`src/Exp1.java` to `Exp5.java`, `Exp3b.java`, `ExpArr.java`,
`obj/ObjCases.java.in`), their case generators (`cases/*.py`) and raw output (`out/`);
`core/` the scratch build of the draft with its checks; `mk_tsv.py`, `mk_setters.py`,
`mk_sql_tsv.py`, `gen_types.py` build the tables; `log.txt` has commands and output.
Reference mode is pgjdbc 42.7.13 (the jar of `~/.m2`) against the pinned image
`postgres:18.6-bookworm`, started with the settings of `reference/fixture` plus
`log_statement=all`, on JDK 17.0.13.

| # | Experiment | Result |
|---|---|---|
| 1 | Placeholders, splitting, classification, RETURNING: 307 texts x 2 string modes x 5 variants through `Parser.parseJdbcSql` (called as a black box), and through `prepareStatement` / `Statement.execute` with the statements read from the server log and the parameter count from the range error of `setObject(99999, ...)` and from `ParameterMetaData`. | `sql-statements.tsv`. The logged statement texts equal the black-box results in all 2448 executed cases (16 differ only by how the log prints a carriage return). Found: the rules marked *quirk* in section 3; native `$1` without `?` makes pgjdbc throw `ArrayIndexOutOfBoundsException`; a comment-only statement is sent and answered as an empty query. |
| 2 | Escapes: 194 texts x 2 string modes and 73 function names x 0 to 5 arguments and every interval unit through `Connection.nativeSQL`; 60 call texts through `Parser.modifyJdbcCall`. | `sql-escapes.tsv`, `sql-functions.tsv`, `sql-call.tsv`; the table of 4.3. |
| 3 | Type table: pgjdbc's type registry functions over 67 types x 52 modifiers; `ResultSetMetaData` of a table with 152 columns; `DatabaseMetaData.getTypeInfo`; name resolution for 108 names. | `types.tsv`, `types-typmod.tsv`, `types-user.tsv`, `types-rsmd.tsv`, `types-dbmd.tsv`, `types-byname.tsv`. |
| 4 | Getter matrix: 681 values (121 declared types, one NULL each) x 88 getters x JVM zones UTC and Europe/Warsaw x session zones UTC, America/New_York, Asia/Kolkata; repeated on JDK 21.0.6. | The `get-*.tsv` tables (one value could not be selected: NULL of a NOT NULL domain). JDK 21 gives the same cells; only the messages of unchecked exceptions differ. |
| 5 | Setter matrix: 3299 cases x (`pg_typeof(?)`, `SELECT ?` with `PreparedStatement.toString`, `COALESCE(?, NULL::T)` and INSERT for the target types of the case family) x 2 JVM zones x 2 session zones, and once with `stringtype=unspecified`; one combination on JDK 21 (identical). | `set.tsv` (OID and text; the context results stay in `ref/out/setters-*.tsv`). |
| 6 | The text PostgreSQL prints for the same values, `COPY ... TO STDOUT` through psql in the container, three session zones. | The text columns of `values.tsv`; equal to pgjdbc's raw bytes for all 4080 compared values except bytea (which pgjdbc decodes). |
| 7 | The draft against the recorded tables: compiled with `--release 17 -Xlint:all -Werror` (javac 17 and 21) and run as JUnit tests in a copy of the build of JDBC-NATIVE, unit mode, JDK 17: 73 tests, 0 failures (every row of the getter, setter, SQL, type and value-class tables). Differential runs against pgjdbc's classes on random texts: statement parser 6,000,000, escape processing 3,000,000, call escape 3,000,000, each value-class parser 300,000: no difference (pgjdbc's unchecked exceptions counted as the driver's `PSQLException`). | The draft tree. On JDK 21 the unit run had one failing comparison in `SettersTest` (float text); the comparison was changed and **not run again**. |
| 8 | Validation slice in oracle and server mode. | **Waived by the project owner** (2026-11-11). Nothing of this layer ran through libpq, against PostgreSQL through the driver's own code, or against the stuntgres server. |

Also recorded: the public API of pgjdbc's value classes (`javap -public`) and 365
behavior cases; `Connection.createArrayOf` for 51 cases and the methods of
`java.sql.Array` for 13 arrays.

Not measured: the property `convertBooleanToNumeric=true` (7.2); a NUL parameter of OID 0
in a non-character context (decision 1); the SQLState the server gives for the rows of
`set.tsv` marked `server` when the driver's text is sent instead of pgjdbc's binary value;
anything on JDK newer than 21.
