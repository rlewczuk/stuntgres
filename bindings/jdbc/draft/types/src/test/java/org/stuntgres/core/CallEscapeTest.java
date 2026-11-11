package org.stuntgres.core;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.sql.SQLException;
import java.util.ArrayList;
import java.util.List;
import org.junit.jupiter.api.Test;
import org.stuntgres.testing.UnitTest;

@UnitTest
class CallEscapeTest {
    private static CallEscape.Mode mode(String name) {
        return name.equals("select") ? CallEscape.Mode.SELECT
                : name.equals("call") ? CallEscape.Mode.CALL : CallEscape.Mode.CALL_IF_NO_RETURN;
    }

    @Test
    void recordedCalls() {
        List<String> failures = new ArrayList<>();
        for (String[] row : TestData.rows("sql-call.tsv")) {
            String got;
            try {
                CallEscape call = CallEscape.parse(TestData.unescape(row[3]), row[1].equals("on"),
                        mode(row[2]));
                got = TestData.escape(call.sql()) + "\t" + call.isFunction();
            } catch (SQLException e) {
                got = "!" + e.getSQLState() + "\t-";
            }
            if (!got.equals(row[4] + "\t" + row[5]) && failures.size() < 20) {
                failures.add(row[0] + " " + row[2] + " " + row[3] + ": expected " + row[4] + " "
                        + row[5] + " but was " + got);
            }
        }
        assertEquals("", String.join("\n", failures));
    }

    @Test
    void forms() throws SQLException {
        CallEscape call = CallEscape.parse("{? = call f(?, ?)}", true, CallEscape.Mode.CALL_IF_NO_RETURN);
        assertEquals("select * from f(?,?, ?) as result", call.sql());
        assertTrue(call.isFunction());
        assertEquals("call f(?, ?)",
                CallEscape.parse("{call f(?, ?)}", true, CallEscape.Mode.CALL_IF_NO_RETURN).sql());
        assertEquals("select * from f(?, ?) as result",
                CallEscape.parse("{call f(?, ?)}", true, CallEscape.Mode.SELECT).sql());
        assertEquals("call f(?)", CallEscape.parse("{? = call f}", true, CallEscape.Mode.CALL).sql());
        assertEquals("call f()", CallEscape.parse(" {call f} -- c", true, CallEscape.Mode.CALL).sql());
        CallEscape plain = CallEscape.parse("call f(?)", true, CallEscape.Mode.CALL);
        assertEquals("call f(?)", plain.sql());
        assertTrue(plain.isFunction());
        assertFalse(CallEscape.parse("SELECT f(?)", true, CallEscape.Mode.CALL).isFunction());
        SQLException e = assertThrows(SQLException.class,
                () -> CallEscape.parse("{call f(?)} x", true, CallEscape.Mode.CALL));
        assertEquals("2F003", e.getSQLState());
        assertEquals("The call escape is malformed at offset 12.", e.getMessage());
    }
}
