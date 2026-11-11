package org.stuntgres.core;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;

import java.math.BigDecimal;
import java.sql.SQLException;
import java.util.UUID;
import org.junit.jupiter.api.Test;
import org.stuntgres.geometric.PGpoint;
import org.stuntgres.testing.UnitTest;

@UnitTest
class ArrayFormatTest {
    /** The literals are the toString texts of array-format.tsv for the same elements. */
    @Test
    void recordedTexts() throws SQLException {
        assertEquals("{\"1\",\"2\",\"3\"}", ArrayFormat.format(new Integer[] {1, 2, 3}, ','));
        assertEquals("{}", ArrayFormat.format(new Integer[] {}, ','));
        assertEquals("{\"1\",NULL}", ArrayFormat.format(new Integer[] {1, null}, ','));
        assertEquals("{{\"1\",\"2\"},{\"3\",\"4\"}}",
                ArrayFormat.format(new Integer[][] {{1, 2}, {3, 4}}, ','));
        assertEquals("{{},{}}", ArrayFormat.format(new Integer[][] {{}, {}}, ','));
        assertEquals("{{{\"1\"}}}", ArrayFormat.format(new Integer[][][] {{{1}}}, ','));
        assertEquals("{\"1\",\"2\",\"3.0\"}", ArrayFormat.format(new Object[] {"1", 2L, 3.0}, ','));
        assertEquals("{\"1.5\",\"NaN\",\"Infinity\"}", ArrayFormat.format(
                new Double[] {1.5, Double.NaN, Double.POSITIVE_INFINITY}, ','));
        assertEquals("{\"1.50\",NULL,\"1E+3\"}", ArrayFormat.format(
                new BigDecimal[] {new BigDecimal("1.50"), null, new BigDecimal("1E+3")}, ','));
        assertEquals("{1,0,NULL}", ArrayFormat.format(new Boolean[] {true, false, null}, ','));
        assertEquals("{\"a b\",\"c,d\",\"e\\\"f\",\"g\\\\h\",NULL,\"NULL\",\"\",\"{x}\"}",
                ArrayFormat.format(new String[] {"a b", "c,d", "e\"f", "g\\h", null, "NULL", "",
                    "{x}"}, ','));
        assertEquals("{\"\\\\x00ff41\",\"\\\\x\",NULL}", ArrayFormat.format(
                new byte[][] {{0, (byte) 0xff, 0x41}, {}, null}, ','));
        assertEquals("{\"a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11\",NULL}", ArrayFormat.format(
                new UUID[] {UUID.fromString("a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11"), null}, ','));
        assertEquals("{\"(1.0,2.0)\",\"(3.0,4.0)\"}", ArrayFormat.format(
                new Object[] {new PGpoint(1, 2), new PGpoint(3, 4)}, ','));
    }

    @Test
    void primitivesAndDelimiters() throws SQLException {
        assertEquals("{\"1\",\"2\"}", ArrayFormat.format(new int[] {1, 2}, ','));
        assertEquals("{{\"1\",\"2\"},{\"3\",\"4\"}}",
                ArrayFormat.format(new int[][] {{1, 2}, {3, 4}}, ','));
        assertEquals("{1,0}", ArrayFormat.format(new boolean[] {true, false}, ','));
        assertEquals("{\"1.5\",\"NaN\"}", ArrayFormat.format(new double[] {1.5, Double.NaN}, ','));
        assertEquals("{\"a\";\"b\"}", ArrayFormat.format(new String[] {"a", "b"}, ';'));
        SQLException e = assertThrows(SQLException.class, () -> ArrayFormat.format("x", ','));
        assertEquals("07006", e.getSQLState());
        assertEquals("The elements of an array must be given as a Java array.", e.getMessage());
        assertEquals("07006", assertThrows(SQLException.class,
                () -> ArrayFormat.format(new byte[] {1}, ',')).getSQLState());
    }

    @Test
    void arrayTypes() {
        assertEquals(Oid.INT4_ARRAY, ArrayFormat.arrayOid(new int[0]));
        assertEquals(Oid.INT4_ARRAY, ArrayFormat.arrayOid(new Integer[0][0]));
        assertEquals(Oid.INT8_ARRAY, ArrayFormat.arrayOid(new long[0]));
        assertEquals(Oid.INT2_ARRAY, ArrayFormat.arrayOid(new Short[0]));
        assertEquals(Oid.FLOAT4_ARRAY, ArrayFormat.arrayOid(new float[0]));
        assertEquals(Oid.FLOAT8_ARRAY, ArrayFormat.arrayOid(new Double[0]));
        assertEquals(Oid.BOOL_ARRAY, ArrayFormat.arrayOid(new boolean[0]));
        assertEquals(Oid.VARCHAR_ARRAY, ArrayFormat.arrayOid(new String[0][0]));
        assertEquals(Oid.BYTEA_ARRAY, ArrayFormat.arrayOid(new byte[0][0]));
        assertEquals(Oid.NUMERIC_ARRAY, ArrayFormat.arrayOid(new BigDecimal[0]));
        assertEquals(Oid.UUID_ARRAY, ArrayFormat.arrayOid(new UUID[0]));
        assertEquals(0, ArrayFormat.arrayOid(new Object[0]));
    }
}
