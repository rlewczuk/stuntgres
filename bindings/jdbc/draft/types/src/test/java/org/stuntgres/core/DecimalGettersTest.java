package org.stuntgres.core;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.math.BigDecimal;
import java.sql.SQLException;
import java.util.HashMap;
import java.util.Map;
import java.util.TimeZone;
import org.junit.jupiter.api.Test;
import org.stuntgres.testing.UnitTest;

@UnitTest
class DecimalGettersTest {
    private static final ConversionContext PLAIN =
            new ConversionContext(TimeZone.getTimeZone("UTC"), Matrix.LOOKUP, false, false);
    private static final ConversionContext BOOLEAN_NUMBERS =
            new ConversionContext(TimeZone.getTimeZone("UTC"), Matrix.LOOKUP, false, true);

    @Test
    void recordedCells() {
        Map<String, Matrix.Getter> getters = new HashMap<>();
        getters.put("getFloat", (v, text, c) -> DecimalGetters.getFloat(text, v.oid, c));
        getters.put("getDouble", (v, text, c) -> DecimalGetters.getDouble(text, v.oid, c));
        getters.put("getBigDecimal", (v, text, c) -> DecimalGetters.getBigDecimal(text, v.oid, c));
        getters.put("getBigDecimal(scale=2)",
            (v, text, c) -> DecimalGetters.getBigDecimal(text, v.oid, 2, c));
        getters.put("getBigDecimal(scale=0)",
            (v, text, c) -> DecimalGetters.getBigDecimal(text, v.oid, 0, c));
        Matrix.check("get-decimal.tsv", getters);
    }

    @Test
    void floatingPoint() throws SQLException {
        assertEquals(1.5f, DecimalGetters.getFloat("1.5", Oid.FLOAT4, PLAIN));
        assertTrue(Float.isNaN(DecimalGetters.getFloat("NaN", Oid.FLOAT4, PLAIN)));
        assertEquals(Double.NEGATIVE_INFINITY,
                DecimalGetters.getDouble("-Infinity", Oid.FLOAT8, PLAIN));
        assertEquals(1234567.89, DecimalGetters.getDouble("$1234567.89", Oid.TEXT, PLAIN));
        assertEquals(-12.34, DecimalGetters.getDouble("-$12.34", Oid.MONEY, PLAIN));
        SQLException e = assertThrows(SQLException.class,
                () -> DecimalGetters.getDouble("$1,234,567.89", Oid.MONEY, PLAIN));
        assertEquals("22003", e.getSQLState());
        assertEquals("Cannot read the value \"1,234,567.89\" as double.", e.getMessage());
        assertEquals(1.0, DecimalGetters.getDouble("t", Oid.BOOL, BOOLEAN_NUMBERS));
        assertEquals(0f, DecimalGetters.getFloat("f", Oid.BOOL, BOOLEAN_NUMBERS));
    }

    @Test
    void decimals() throws SQLException {
        assertEquals(new BigDecimal("1.50"), DecimalGetters.getBigDecimal("1.50", Oid.NUMERIC, PLAIN));
        assertEquals(new BigDecimal("1E+2"), DecimalGetters.getBigDecimal("1e2", Oid.TEXT, PLAIN));
        assertEquals(new BigDecimal("2"), DecimalGetters.getBigDecimal("2.5", Oid.NUMERIC, 0, PLAIN));
        assertEquals(new BigDecimal("4"), DecimalGetters.getBigDecimal("3.5", Oid.NUMERIC, 0, PLAIN));
        assertEquals(new BigDecimal("1.00"), DecimalGetters.getBigDecimal("1", Oid.INT4, 2, PLAIN));
        assertEquals(new BigDecimal("1"), DecimalGetters.getBigDecimal("t", Oid.BOOL, BOOLEAN_NUMBERS));
        SQLException e = assertThrows(SQLException.class,
                () -> DecimalGetters.getBigDecimal("NaN", Oid.NUMERIC, PLAIN));
        assertEquals("22003", e.getSQLState());
        assertEquals("Cannot read the value \"NaN\" as BigDecimal.", e.getMessage());
    }
}
