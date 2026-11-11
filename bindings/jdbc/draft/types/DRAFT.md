# Draft of the JDBC type layer

This directory holds a draft of the connection-free core of the JDBC driver: the SQL
scanner and statement parser, JDBC escapes, the type table, the text codecs, the getter and
setter conversion functions, array text and the value classes of `org.stuntgres.util` and
`org.stuntgres.geometric`, with their JUnit tests and the recorded tables the tests read.
It was produced by the design part JDBC-DESIGN-TYPES.

- **Not part of any build.** The Maven build and the Makefile of `bindings/jdbc`
  (docs/design/JDBC-NATIVE.md, section 3) compile `bindings/jdbc/src` only. Nothing may
  import from this directory.
- **A starting point, not the specification.** [docs/design/JDBC-TYPES.md](../../../../docs/design/JDBC-TYPES.md)
  and the recorded tables under `src/test/resources/org/stuntgres/core/` are authoritative.
  Where a draft file disagrees with them, the file is wrong.
- **State.** On 2026-11-11 the main classes compiled with `javac --release 17 -Xlint:all
  -Werror` (javac of JDK 17 and of JDK 21) and all test classes passed on JDK 17 in a
  scratch copy of the build of JDBC-NATIVE (73 tests in unit mode, every recorded row).
  Known open points: on JDK 21 one comparison of `SettersTest` failed (float text of
  `Float.toString` differs between JDK versions); the comparison was changed afterwards and
  not run again. Some lines are longer than 100 columns; `SetterCases.java`,
  `ValueClassesTest.java` and `GeometricClassesTest.java` are generated case tables.
- **Promotion.** Every file is owned by one catalog part (JDBC-TYPE-, JDBC-SQL-,
  JDBC-CODEC-, JDBC-GET-, JDBC-SET-, JDBC-ARRAY-, JDBC-PGOBJ-; the table is section 1.2 of
  the design document). That part moves the file with `git mv` to the same path under
  `bindings/jdbc/src`, makes it compile and pass under the binding's build and fixes what
  fails. JDBC-TYPE-99-DRAFT-REMOVE moves the remaining tables and removes this directory.
- **Stub for a class another design owns.** `src/main/java/org/stuntgres/util/PSQLException.java`
  is the minimal form (message, SQLState, cause) this layer needs. JDBC-TYPE-00A-ERRORS
  promotes it; the error part of JDBC-DESIGN-CORE replaces it with the complete class
  (server error fields) and takes an ordering edge on JDBC-TYPE-00A-ERRORS.
  `PSQLState.java` is complete (the constants of the mirrored class).

Layout: `src/main/java`, `src/test/java`, `src/test/resources`, with the package paths of
the final locations.
