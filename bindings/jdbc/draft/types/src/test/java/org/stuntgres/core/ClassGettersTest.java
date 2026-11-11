package org.stuntgres.core;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;

import java.math.BigDecimal;
import java.math.BigInteger;
import java.net.InetAddress;
import java.sql.SQLException;
import java.sql.Time;
import java.sql.Timestamp;
import java.sql.Types;
import java.time.LocalDate;
import java.util.Calendar;
import java.util.GregorianCalendar;
import java.util.HashMap;
import java.util.Map;
import java.util.TimeZone;
import java.util.UUID;
import org.junit.jupiter.api.Test;
import org.stuntgres.geometric.PGbox;
import org.stuntgres.geometric.PGcircle;
import org.stuntgres.geometric.PGline;
import org.stuntgres.geometric.PGlseg;
import org.stuntgres.geometric.PGpath;
import org.stuntgres.geometric.PGpoint;
import org.stuntgres.geometric.PGpolygon;
import org.stuntgres.testing.UnitTest;
import org.stuntgres.util.PGInterval;
import org.stuntgres.util.PGmoney;
import org.stuntgres.util.PGobject;

@UnitTest
class ClassGettersTest {
    private static final ConversionContext UTC =
            new ConversionContext(TimeZone.getTimeZone("UTC"), Matrix.LOOKUP, false, false);

    private static Map<String, Matrix.Getter> getters(Class<?>... classes) {
        Map<String, Matrix.Getter> getters = new HashMap<>();
        for (Class<?> target : classes) {
            String name = target == java.util.Date.class ? "java.util.Date" : target.getSimpleName();
            getters.put("getObject(" + name + ")", (v, text, c) -> {
                PgType type = TypeTable.find(v.oid, c.types());
                int sql = type.sqlType();
                if (target == UUID.class
                        && (sql == Types.ARRAY || sql == Types.SQLXML || sql == Types.REF_CURSOR)) {
                    return Matrix.SKIP;
                }
                return ClassGetters.getObject(text, type, target, c);
            });
        }
        return getters;
    }

    @Test
    void recordedNumbersAndStrings() {
        Matrix.check("get-string.tsv", getters(String.class));
        Matrix.check("get-integer.tsv", getters(Boolean.class, Byte.class, Short.class,
                Integer.class, Long.class, BigInteger.class));
        Matrix.check("get-decimal.tsv",
                getters(Float.class, Double.class, BigDecimal.class, Number.class));
        Matrix.check("get-bytes.tsv", getters(byte[].class, java.io.InputStream.class));
    }

    @Test
    void recordedDatesAndTimes() {
        Matrix.check("get-datetime.tsv", getters(java.sql.Date.class, Time.class, Timestamp.class,
                java.util.Date.class, Calendar.class, GregorianCalendar.class));
        Matrix.check("get-javatime.tsv", getters(java.time.ZonedDateTime.class));
    }

    @Test
    void recordedOtherClasses() {
        Matrix.check("get-object.tsv", getters(Object.class, UUID.class, InetAddress.class,
                PGobject.class, PGInterval.class, PGmoney.class, PGpoint.class, PGbox.class,
                PGcircle.class, PGline.class, PGlseg.class, PGpath.class, PGpolygon.class));
    }

    @Test
    void values() throws Exception {
        assertEquals(Integer.valueOf(5),
                ClassGetters.getObject("5", TypeTable.byOid(Oid.INT2), Integer.class, UTC));
        assertEquals(BigInteger.valueOf(5),
                ClassGetters.getObject("5", TypeTable.byOid(Oid.INT8), BigInteger.class, UTC));
        assertEquals(LocalDate.of(2020, 1, 2),
                ClassGetters.getObject("2020-01-02", TypeTable.byOid(Oid.DATE), LocalDate.class, UTC));
        assertEquals(InetAddress.getByAddress(new byte[] {(byte) 192, (byte) 168, 1, 1}), ClassGetters
                .getObject("192.168.1.1/24", TypeTable.byOid(Oid.INET), InetAddress.class, UTC));
        assertEquals(new PGpoint(1, 2),
                ClassGetters.getObject("(1,2)", TypeTable.byOid(Oid.POINT), PGpoint.class, UTC));
        assertEquals(new java.util.Date(1577959872000L), ClassGetters.getObject(
                "2020-01-02 10:11:12", TypeTable.byOid(Oid.TIMESTAMP), java.util.Date.class, UTC));
        Calendar calendar = ClassGetters.getObject("2020-01-02 10:11:12+00",
                TypeTable.byOid(Oid.TIMESTAMPTZ), Calendar.class, UTC);
        assertEquals(1577959872000L, calendar.getTimeInMillis());
        assertEquals("UTC", calendar.getTimeZone().getID());
    }

    @Test
    void errors() {
        SQLException e = assertThrows(SQLException.class, () -> ClassGetters.getObject("5",
                TypeTable.byOid(Oid.INT4), String.class, UTC));
        assertEquals("22023", e.getSQLState());
        assertEquals("A column of type int4 cannot be read as java.lang.String.", e.getMessage());
        assertEquals("22023", assertThrows(SQLException.class, () -> ClassGetters.getObject("5",
                TypeTable.byOid(Oid.INT4), Long.class, UTC)).getSQLState());
        assertEquals("22023", assertThrows(SQLException.class, () -> ClassGetters.getObject("5",
                TypeTable.byOid(Oid.INT4), UUID.class, UTC)).getSQLState());
        assertEquals("22023", assertThrows(SQLException.class, () -> ClassGetters.getObject("(1,2)",
                TypeTable.byOid(Oid.POINT), PGbox.class, UTC)).getSQLState());
        assertEquals("22023", assertThrows(SQLException.class, () -> ClassGetters.getObject(
                "example.com", TypeTable.byOid(Oid.TEXT), InetAddress.class, UTC)).getSQLState());
    }
}
