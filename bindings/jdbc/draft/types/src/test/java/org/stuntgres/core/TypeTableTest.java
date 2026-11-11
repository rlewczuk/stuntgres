package org.stuntgres.core;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertSame;

import java.sql.SQLException;
import java.sql.Types;
import java.util.Arrays;
import org.junit.jupiter.api.Test;
import org.stuntgres.testing.UnitTest;

@UnitTest
class TypeTableTest {
    @Test
    void recordedTypes() {
        for (String[] row : TestData.rows("types.tsv")) {
            int oid = Integer.parseInt(row[1]);
            PgType type = TypeTable.byOid(oid);
            assertEquals(String.join("\t", Arrays.copyOf(row, 9)), String.join("\t", type.name(),
                    "" + type.oid(), "" + type.arrayOid(), "" + type.sqlType(), type.className(),
                    "" + type.delimiter(), "" + TypeTable.isSigned(oid),
                    "" + TypeTable.isCaseSensitive(oid), "" + TypeTable.maximumPrecision(oid)));
            assertSame(type, TypeTable.byName(row[0]));
            assertEquals(0, type.elementOid());
            int arrayOid = Integer.parseInt(row[2]);
            if (arrayOid != 0) {
                PgType array = TypeTable.byOid(arrayOid);
                assertEquals("_" + row[0] + " 2003 java.sql.Array " + oid + " " + row[5],
                        array.name() + " " + array.sqlType() + " " + array.className() + " "
                            + array.elementOid() + " " + array.delimiter());
                assertSame(array, TypeTable.byName(row[0] + "[]"));
                assertSame(array, TypeTable.byName("_" + row[0]));
                assertEquals(row[6], "" + TypeTable.isSigned(arrayOid));
                assertEquals(row[7], "" + TypeTable.isCaseSensitive(arrayOid));
            }
        }
    }

    @Test
    void recordedModifiers() {
        for (String[] row : TestData.rows("types-typmod.tsv")) {
            int oid = Integer.parseInt(row[1]);
            int typmod = Integer.parseInt(row[2]);
            assertEquals(row[3] + "/" + row[4] + "/" + row[5], TypeTable.precision(oid, typmod) + "/"
                    + TypeTable.scale(oid, typmod) + "/" + TypeTable.displaySize(oid, typmod),
                    row[0] + " with modifier " + typmod);
        }
    }

    @Test
    void lookups() throws SQLException {
        assertEquals(Oid.INT4, TypeTable.oidByName("int4", Matrix.LOOKUP));
        assertEquals(Oid.INT4_ARRAY, TypeTable.oidByName("int4[]", Matrix.LOOKUP));
        assertEquals(Oid.INT4_ARRAY, TypeTable.arrayOidByElementName("INTEGER", Matrix.LOOKUP));
        assertEquals(Oid.FLOAT8_ARRAY,
                TypeTable.arrayOidByElementName("double precision", Matrix.LOOKUP));
        assertEquals(Oid.TIMESTAMPTZ_ARRAY,
                TypeTable.arrayOidByElementName("timestamp with time zone", Matrix.LOOKUP));
        assertEquals(0, TypeTable.arrayOidByElementName("nosuchtype", Matrix.LOOKUP));
        assertNull(TypeTable.byOid(16384));
        assertNull(TypeTable.byName("integer"));
        PgType mood = Matrix.LOOKUP.byOid(Matrix.LOOKUP.oidByName("mood"));
        assertSame(mood, TypeTable.find(mood.oid(), Matrix.LOOKUP));
        assertEquals(Types.VARCHAR, mood.sqlType());
        assertEquals(Types.INTEGER, TypeTable.find(Oid.INT4, Matrix.LOOKUP).sqlType());
        assertEquals(12, TypeTable.displaySize(Oid.NUMERIC, 655366));
        assertEquals(TypeTable.UNKNOWN_LENGTH, TypeTable.precision(Oid.TEXT, -1));
        assertEquals(0, Oid.UNSPECIFIED);
        assertEquals(1184, Oid.TIMESTAMPTZ);
    }
}
