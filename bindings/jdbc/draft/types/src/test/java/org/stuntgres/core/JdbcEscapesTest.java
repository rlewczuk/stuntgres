package org.stuntgres.core;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.sql.SQLException;
import java.util.ArrayList;
import java.util.List;
import org.junit.jupiter.api.Test;
import org.stuntgres.testing.UnitTest;

@UnitTest
class JdbcEscapesTest {
    /**
     * What Connection.nativeSQL returns: escape processing, then the statement parser, the
     * statements joined by semicolons; "!" and the SQLState for an exception.
     */
    static String nativeSql(String sql, boolean standardConformingStrings) {
        try {
            StringBuilder out = new StringBuilder();
            for (NativeStatement s : NativeSql.parse(
                    JdbcEscapes.replace(sql, standardConformingStrings), standardConformingStrings,
                    true, new String[0], true)) {
                out.append(out.length() > 0 ? ";" : "").append(s.sql());
            }
            return out.toString();
        } catch (SQLException e) {
            return "!" + e.getSQLState();
        }
    }

    /** Checks the rows of a table whose last two columns are the SQL and the result. */
    static void check(String resource, int scsColumn, int sqlColumn) {
        List<String> failures = new ArrayList<>();
        int count = 0;
        for (String[] row : TestData.rows(resource)) {
            String got = nativeSql(TestData.unescape(row[sqlColumn]),
                    scsColumn < 0 || row[scsColumn].equals("on"));
            count++;
            if (!TestData.escape(got).equals(row[sqlColumn + 1]) && failures.size() < 20) {
                failures.add(row[0] + " " + row[sqlColumn] + ": expected " + row[sqlColumn + 1]
                        + " but was " + TestData.escape(got));
            }
        }
        assertTrue(count > 100, resource + ": " + count);
        assertEquals("", String.join("\n", failures));
    }

    @Test
    void recordedEscapes() {
        check("sql-escapes.tsv", 1, 2);
    }

    @Test
    void literalsAndJoins() throws SQLException {
        assertEquals("SELECT DATE '2020-01-02', TIME '10:11:12', TIMESTAMP '2020-01-02 10:11:12'",
                JdbcEscapes.replace(
                    "SELECT {d '2020-01-02'}, {t '10:11:12'}, {ts '2020-01-02 10:11:12'}", true));
        assertEquals("SELECT 'a' LIKE ? ESCAPE '!'",
                JdbcEscapes.replace("SELECT 'a' LIKE ? {escape '!'}", true));
        assertEquals("SELECT * FROM t1 LEFT OUTER JOIN t2 ON true",
                JdbcEscapes.replace("SELECT * FROM {oj t1 LEFT OUTER JOIN t2 ON true}", true));
        assertEquals("SELECT '{d ''x''}', \"{fn a()}\" /* {t '1'} */",
                JdbcEscapes.replace("SELECT '{d ''x''}', \"{fn a()}\" /* {t '1'} */", true));
        assertEquals("SELECT { d '2020-01-02'}", JdbcEscapes.replace("SELECT { d '2020-01-02'}", true));
        assertEquals("SELECT abs(-1), now()", JdbcEscapes.replace("SELECT {fn abs(-1)}, {fn now()}",
                true));
    }

    @Test
    void unterminated() {
        SQLException e = assertThrows(SQLException.class,
                () -> JdbcEscapes.replace("SELECT 'abc", true));
        assertEquals("42601", e.getSQLState());
        assertEquals("The string literal that starts at position 7 of the statement is not"
                + " terminated.", e.getMessage());
        assertEquals("The quoted identifier that starts at position 7 of the statement is not"
                + " terminated.", assertThrows(SQLException.class,
                    () -> JdbcEscapes.replace("SELECT \"abc", true)).getMessage());
        assertEquals("The block comment that starts at position 7 of the statement is not"
                + " terminated.", assertThrows(SQLException.class,
                    () -> JdbcEscapes.replace("SELECT /* abc", true)).getMessage());
        assertEquals("The dollar-quoted string that starts at position 7 of the statement is not"
                + " terminated.", assertThrows(SQLException.class,
                    () -> JdbcEscapes.replace("SELECT $$abc", true)).getMessage());
    }
}
