package org.stuntgres.core;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.sql.SQLException;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.Test;
import org.stuntgres.testing.UnitTest;

@UnitTest
class NativeSqlTest {
    private static final String[] NONE = new String[0];

    /**
     * Checks every row of sql-statements.tsv. The variants: ps (a prepared statement), st
     * (a plain statement: question marks stay), gkall (generated keys, all columns),
     * gknames (the columns id and Na"me, quoted) and gkraw (the same, not quoted).
     */
    @Test
    void recordedStatements() throws SQLException {
        Map<String, List<String[]>> cases = new LinkedHashMap<>();
        for (String[] row : TestData.rows("sql-statements.tsv")) {
            cases.computeIfAbsent(row[0] + "\t" + row[2] + "\t" + row[3], k -> new ArrayList<>())
                    .add(row);
        }
        List<String> failures = new ArrayList<>();
        for (List<String[]> rows : cases.values()) {
            String[] first = rows.get(0);
            String variant = first[2];
            String[] returning = variant.equals("gkall") ? new String[] {"*"}
                    : variant.startsWith("gk") ? new String[] {"id", "Na\"me"} : NONE;
            List<NativeStatement> actual = NativeSql.parse(TestData.unescape(first[1]),
                    first[3].equals("on"), !variant.equals("st"), returning,
                    !variant.equals("gkraw"));
            StringBuilder got = new StringBuilder().append(actual.size());
            for (NativeStatement s : actual) {
                got.append('|').append(TestData.escape(s.sql())).append('|')
                        .append(s.parameterCount()).append('|').append(s.kind()).append('|')
                        .append(s.returning());
            }
            StringBuilder want = new StringBuilder(first[4]);
            for (String[] row : rows) {
                if (!row[5].equals("-")) {
                    want.append('|').append(row[6]).append('|').append(row[7]).append('|')
                            .append(row[8]).append('|').append(row[9]);
                }
            }
            if (!want.toString().equals(got.toString()) && failures.size() < 20) {
                failures.add(first[0] + " " + variant + " scs=" + first[3] + ": expected " + want
                        + " but was " + got);
            }
        }
        assertTrue(cases.size() > 3000, "cases: " + cases.size());
        assertEquals("", String.join("\n", failures));
    }

    @Test
    void placeholders() throws SQLException {
        NativeStatement s = NativeSql.parse("SELECT ?, '?', \"?\", $$?$$ -- ?\n, ?? , ?", true, true,
                NONE, true).get(0);
        assertEquals("SELECT $1, '?', \"?\", $$?$$ -- ?\n, ? , $2", s.sql());
        assertEquals(2, s.parameterCount());
        assertEquals(CommandKind.SELECT, s.kind());
        assertTrue(s.returnsRows());
        assertEquals("SELECT ?, ?", NativeSql.parse("SELECT ?, ??", true, false, NONE, true)
                .get(0).sql());
    }

    @Test
    void splittingAndKinds() throws SQLException {
        List<NativeStatement> list = NativeSql.parse("INSERT INTO t(a) VALUES (?); SELECT ?, ?;",
                true, true, NONE, true);
        assertEquals(2, list.size());
        assertEquals("INSERT INTO t(a) VALUES ($1)", list.get(0).sql());
        assertEquals(1, list.get(0).parameterCount());
        assertEquals(CommandKind.INSERT, list.get(0).kind());
        assertEquals(" SELECT $1, $2", list.get(1).sql());
        assertEquals(2, list.get(1).parameterCount());
        assertEquals(0, NativeSql.parse(" ; ; ", true, true, NONE, true).size());
        assertEquals(CommandKind.BLANK,
                NativeSql.parse("SHOW server_version", true, true, NONE, true).get(0).kind());
        assertEquals(CommandKind.UPDATE, NativeSql.parse("WITH x AS (SELECT 1) UPDATE t SET a = 1",
                true, true, NONE, true).get(0).kind());
    }

    @Test
    void returning() throws SQLException {
        NativeStatement s = NativeSql.parse("INSERT INTO t(a) VALUES (?)", true, true,
                new String[] {"*"}, true).get(0);
        assertEquals("INSERT INTO t(a) VALUES ($1)\nRETURNING *", s.sql());
        assertTrue(s.returning());
        assertEquals("UPDATE t SET a = 1\nRETURNING \"id\", \"Na\"\"me\"", NativeSql.parse(
                "UPDATE t SET a = 1", true, true, new String[] {"id", "Na\"me"}, true).get(0).sql());
        assertEquals("DELETE FROM t RETURNING a", NativeSql.parse("DELETE FROM t RETURNING a", true,
                true, new String[] {"*"}, true).get(0).sql());
        assertEquals("SELECT 1",
                NativeSql.parse("SELECT 1", true, true, new String[] {"*"}, true).get(0).sql());
        SQLException e = assertThrows(SQLException.class, () -> NativeSql.parse("DELETE FROM t",
                true, true, new String[] {"a\0b"}, true));
        assertEquals("22023", e.getSQLState());
        assertEquals("A column name must not contain a NUL character.", e.getMessage());
    }
}
