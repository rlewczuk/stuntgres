package org.stuntgres.core;

import static org.junit.jupiter.api.Assertions.assertArrayEquals;
import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;

import java.math.BigDecimal;
import java.sql.SQLException;
import java.sql.Types;
import java.util.Collections;
import java.util.HashMap;
import java.util.Map;
import java.util.TimeZone;
import java.util.UUID;
import org.junit.jupiter.api.Test;
import org.stuntgres.geometric.PGpoint;
import org.stuntgres.testing.UnitTest;
import org.stuntgres.util.PGInterval;
import org.stuntgres.util.PGobject;

@UnitTest
class ObjectGettersTest {
    private static final ConversionContext UTC =
            new ConversionContext(TimeZone.getTimeZone("UTC"), Matrix.LOOKUP, false, false);

    @Test
    void recordedDefaultObjects() {
        Map<String, Matrix.Getter> getters = new HashMap<>();
        getters.put("getObject", (v, text, c) -> {
            PgType type = TypeTable.find(v.oid, c.types());
            if (type == null) {
                return Matrix.SKIP;
            }
            int sql = type.sqlType();
            if (sql == Types.ARRAY || sql == Types.SQLXML || sql == Types.REF_CURSOR) {
                return Matrix.SKIP;
            }
            return ObjectGetters.getObject(text, type, v.typmod, c);
        });
        Matrix.check("get-object.tsv", getters);
    }

    @Test
    void scalars() throws SQLException {
        assertEquals(5, ObjectGetters.getObject("5", TypeTable.byOid(Oid.INT2), -1, UTC));
        assertEquals(5L, ObjectGetters.getObject("5", TypeTable.byOid(Oid.OID), -1, UTC));
        assertEquals(new BigDecimal("1.50"),
                ObjectGetters.getObject("1.5", TypeTable.byOid(Oid.NUMERIC), 655366, UTC));
        assertEquals(Double.NaN, ObjectGetters.getObject("NaN", TypeTable.byOid(Oid.NUMERIC), -1, UTC));
        assertEquals(Boolean.TRUE, ObjectGetters.getObject("1", TypeTable.byOid(Oid.BIT), 1, UTC));
        Object bits = ObjectGetters.getObject("101", TypeTable.byOid(Oid.BIT), 3, UTC);
        assertEquals(PGobject.class, bits.getClass());
        assertEquals("bit", ((PGobject) bits).getType());
        assertEquals("101", ((PGobject) bits).getValue());
        assertEquals(UUID.fromString("a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11"), ObjectGetters.getObject(
                "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11", TypeTable.byOid(Oid.UUID), -1, UTC));
        assertEquals(new PGInterval(1, 2, 3, 4, 5, 6.789), ObjectGetters.getObject(
                "1 year 2 mons 3 days 04:05:06.789", TypeTable.byOid(Oid.INTERVAL), -1, UTC));
        assertEquals(new PGpoint(1, 2),
                ObjectGetters.getObject("(1,2)", TypeTable.byOid(Oid.POINT), -1, UTC));
        assertEquals(12.34, ObjectGetters.getObject("$12.34", TypeTable.byOid(Oid.MONEY), -1, UTC));
        assertEquals(Collections.singletonMap("a", "1"), HstoreText.parse("\"a\"=>\"1\""));
        assertEquals("\"a\"=>\"1\", \"b\"=>NULL", HstoreText.format(HstoreText.parse(
                "\"a\"=>\"1\", \"b\"=>NULL")));
        SQLException e = assertThrows(SQLException.class, () -> ObjectGetters.getObject("x",
                TypeTable.byOid(Oid.UUID), -1, UTC));
        assertEquals("22023", e.getSQLState());
        assertEquals("Cannot read the value \"x\" as a UUID.", e.getMessage());
        assertThrows(IllegalArgumentException.class, () -> ObjectGetters.getObject("{1}",
                TypeTable.byOid(Oid.INT4_ARRAY), -1, UTC));
    }

    @Test
    void recordedArrays() {
        Map<String, Matrix.Getter> getters = new HashMap<>();
        getters.put("getObject", (v, text, c) -> {
            PgType type = TypeTable.find(v.oid, c.types());
            if (type == null || !type.isArray()) {
                return Matrix.SKIP;
            }
            PgType element = TypeTable.find(type.elementOid(), c.types());
            String elements;
            try {
                elements = Repr.of(ObjectGetters.getArray(text, element, c));
            } catch (SQLException e) {
                elements = Repr.error(e);
            }
            return new Repr.Raw("Array:" + element.name() + ":" + element.sqlType() + ":" + elements
                    + ":toString=" + TestData.escape(text));
        });
        Matrix.check("get-object.tsv", getters);
    }

    @Test
    void arrays() throws SQLException {
        assertArrayEquals(new Object[] {"1", null, "a b", "NULL", ""},
                ArrayText.parse("{1,NULL,\"a b\",\"NULL\",\"\"}", ','));
        assertArrayEquals(new Object[] {"e\"f", "g\\h"}, ArrayText.parse("{\"e\\\"f\",\"g\\\\h\"}", ','));
        assertArrayEquals(new Object[0], ArrayText.parse("{}", ','));
        Object[] nested = ArrayText.parse("[2:3][0:1]={{1,2},{3,4}}", ',');
        assertEquals(2, ArrayText.dimensions(nested));
        assertArrayEquals(new Object[] {"3", "4"}, (Object[]) nested[1]);
        assertArrayEquals(new Object[] {"(1,2),(3,4)", "(5,6),(7,8)"},
                ArrayText.parse("{(1,2),(3,4);(5,6),(7,8)}", ';'));
        assertArrayEquals(new Integer[][] {{1, 2}, {3, null}}, (Integer[][]) ObjectGetters.getArray(
                "{{1,2},{3,NULL}}", TypeTable.byOid(Oid.INT4), UTC));
        assertArrayEquals(new String[0],
                (String[]) ObjectGetters.getArray("{}", TypeTable.byOid(Oid.TEXT), UTC));
        SQLException e = assertThrows(SQLException.class, () -> ArrayText.parse("1,2", ','));
        assertEquals("22000", e.getSQLState());
        assertEquals("Cannot read \"1,2\" as an array.", e.getMessage());
        assertEquals("22000",
                assertThrows(SQLException.class, () -> ArrayText.parse("{1,2", ',')).getSQLState());
    }
}
