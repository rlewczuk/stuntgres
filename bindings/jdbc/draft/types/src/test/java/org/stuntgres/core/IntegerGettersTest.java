package org.stuntgres.core;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.sql.SQLException;
import java.util.HashMap;
import java.util.Map;
import java.util.TimeZone;
import org.junit.jupiter.api.Test;
import org.stuntgres.testing.UnitTest;

@UnitTest
class IntegerGettersTest {
    private static final ConversionContext PLAIN =
            new ConversionContext(TimeZone.getTimeZone("UTC"), Matrix.LOOKUP, false, false);
    private static final ConversionContext BOOLEAN_NUMBERS =
            new ConversionContext(TimeZone.getTimeZone("UTC"), Matrix.LOOKUP, false, true);

    @Test
    void recordedCells() {
        Map<String, Matrix.Getter> getters = new HashMap<>();
        getters.put("getBoolean", (v, text, c) -> IntegerGetters.getBoolean(text, v.oid));
        getters.put("getByte", (v, text, c) -> IntegerGetters.getByte(text, v.oid, c));
        getters.put("getShort", (v, text, c) -> IntegerGetters.getShort(text, v.oid, c));
        getters.put("getInt", (v, text, c) -> IntegerGetters.getInt(text, v.oid, c));
        getters.put("getLong", (v, text, c) -> IntegerGetters.getLong(text, v.oid, c));
        Matrix.check("get-integer.tsv", getters);
    }

    @Test
    void booleans() throws SQLException {
        assertTrue(IntegerGetters.getBoolean("t", Oid.BOOL));
        assertFalse(IntegerGetters.getBoolean("f", Oid.BOOL));
        assertFalse(IntegerGetters.getBoolean("true", Oid.BOOL));
        assertTrue(IntegerGetters.getBoolean(" YES ", Oid.TEXT));
        assertFalse(IntegerGetters.getBoolean("off", Oid.TEXT));
        assertTrue(IntegerGetters.getBoolean("1", Oid.INT4));
        SQLException e = assertThrows(SQLException.class,
                () -> IntegerGetters.getBoolean("2", Oid.INT4));
        assertEquals("42846", e.getSQLState());
        assertEquals("Cannot convert the value \"2\" to boolean.", e.getMessage());
    }

    @Test
    void integers() throws SQLException {
        assertEquals(2147483647, IntegerGetters.getInt("2147483647", Oid.INT4, PLAIN));
        assertEquals(3, IntegerGetters.getInt("3.99", Oid.NUMERIC, PLAIN));
        assertEquals(-1000, IntegerGetters.getInt("-1e3", Oid.TEXT, PLAIN));
        assertEquals(42, IntegerGetters.getInt(" 42 ", Oid.TEXT, PLAIN));
        assertEquals(1234, IntegerGetters.getLong("$1234.50", Oid.MONEY, PLAIN));
        assertEquals(-12, IntegerGetters.getShort("-$12.34", Oid.MONEY, PLAIN));
        assertEquals(-12, IntegerGetters.getInt("($12.34)", Oid.TEXT, PLAIN));
        assertEquals(0, IntegerGetters.getByte("", Oid.TEXT, PLAIN));
        assertEquals(0, IntegerGetters.getInt("-", Oid.TEXT, PLAIN));
        assertEquals(Long.MIN_VALUE,
                IntegerGetters.getLong("-9223372036854775808", Oid.INT8, PLAIN));
        SQLException e = assertThrows(SQLException.class,
                () -> IntegerGetters.getInt("2147483648", Oid.INT8, PLAIN));
        assertEquals("22003", e.getSQLState());
        assertEquals("Cannot read the value \"2147483648\" as int.", e.getMessage());
        assertEquals("22003", assertThrows(SQLException.class,
                () -> IntegerGetters.getByte("128", Oid.INT2, PLAIN)).getSQLState());
        assertEquals("22003", assertThrows(SQLException.class,
                () -> IntegerGetters.getShort("", Oid.TEXT, PLAIN)).getSQLState());
        assertEquals("22003", assertThrows(SQLException.class,
                () -> IntegerGetters.getLong("t", Oid.BOOL, PLAIN)).getSQLState());
    }

    @Test
    void booleanColumnAsNumber() throws SQLException {
        assertEquals(1, IntegerGetters.getInt("t", Oid.BOOL, BOOLEAN_NUMBERS));
        assertEquals(0, IntegerGetters.getLong("f", Oid.BOOL, BOOLEAN_NUMBERS));
        assertEquals(1, IntegerGetters.getByte("t", Oid.BOOL, BOOLEAN_NUMBERS));
        assertEquals(1, IntegerGetters.getShort("t", Oid.BOOL, BOOLEAN_NUMBERS));
        assertEquals("22003", assertThrows(SQLException.class,
                () -> IntegerGetters.getInt("t", Oid.TEXT, BOOLEAN_NUMBERS)).getSQLState());
    }
}
