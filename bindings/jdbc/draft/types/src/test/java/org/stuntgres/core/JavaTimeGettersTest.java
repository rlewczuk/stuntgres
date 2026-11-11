package org.stuntgres.core;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;

import java.sql.SQLException;
import java.time.LocalDate;
import java.time.LocalDateTime;
import java.time.LocalTime;
import java.time.OffsetDateTime;
import java.time.OffsetTime;
import java.util.HashMap;
import java.util.Map;
import org.junit.jupiter.api.Test;
import org.stuntgres.testing.UnitTest;

@UnitTest
class JavaTimeGettersTest {
    private static PgType type(int oid) {
        return TypeTable.byOid(oid);
    }

    @Test
    void recordedCells() {
        Map<String, Matrix.Getter> getters = new HashMap<>();
        getters.put("getObject(LocalDate)", (v, text, c) ->
            JavaTimeGetters.getLocalDate(text, TypeTable.find(v.oid, c.types())));
        getters.put("getObject(LocalTime)", (v, text, c) ->
            JavaTimeGetters.getLocalTime(text, TypeTable.find(v.oid, c.types())));
        getters.put("getObject(LocalDateTime)", (v, text, c) ->
            JavaTimeGetters.getLocalDateTime(text, TypeTable.find(v.oid, c.types())));
        getters.put("getObject(OffsetDateTime)", (v, text, c) ->
            JavaTimeGetters.getOffsetDateTime(text, TypeTable.find(v.oid, c.types())));
        getters.put("getObject(OffsetTime)", (v, text, c) ->
            JavaTimeGetters.getOffsetTime(text, TypeTable.find(v.oid, c.types())));
        Matrix.check("get-javatime.tsv", getters);
    }

    @Test
    void values() throws SQLException {
        assertEquals(LocalDate.of(2020, 1, 2),
                JavaTimeGetters.getLocalDate("2020-01-02", type(Oid.DATE)));
        assertEquals(LocalDate.of(-43, 3, 15),
                JavaTimeGetters.getLocalDate("0044-03-15 BC", type(Oid.DATE)));
        assertEquals(LocalDate.MAX, JavaTimeGetters.getLocalDate("infinity", type(Oid.DATE)));
        assertEquals(LocalDate.of(2020, 1, 2),
                JavaTimeGetters.getLocalDate("2020-01-02 10:11:12", type(Oid.TIMESTAMP)));
        assertEquals(LocalTime.of(10, 11, 12, 123456000),
                JavaTimeGetters.getLocalTime("10:11:12.123456", type(Oid.TIME)));
        assertEquals(LocalTime.MAX, JavaTimeGetters.getLocalTime("24:00:00", type(Oid.TIME)));
        assertEquals(LocalDateTime.of(2020, 1, 2, 10, 11, 12, 500000000),
                JavaTimeGetters.getLocalDateTime("2020-01-02 10:11:12.5", type(Oid.TIMESTAMP)));
        assertEquals(LocalDateTime.MIN,
                JavaTimeGetters.getLocalDateTime("-infinity", type(Oid.TIMESTAMP)));
        assertEquals(OffsetDateTime.parse("2020-01-02T04:41:12Z"), JavaTimeGetters
                .getOffsetDateTime("2020-01-02 10:11:12+05:30", type(Oid.TIMESTAMPTZ)));
        assertEquals(OffsetDateTime.parse("1970-01-01T10:11:12+05:30"),
                JavaTimeGetters.getOffsetDateTime("10:11:12+05:30", type(Oid.TIMETZ)));
        assertEquals(OffsetTime.parse("10:11:12.5+01:02:03"),
                JavaTimeGetters.getOffsetTime("10:11:12.5+01:02:03", type(Oid.TIMETZ)));
        assertEquals(OffsetTime.MAX, JavaTimeGetters.getOffsetTime("24:00:00+00", type(Oid.TIMETZ)));
    }

    @Test
    void errors() {
        SQLException e = assertThrows(SQLException.class,
                () -> JavaTimeGetters.getLocalDateTime("2020-01-02 10:11:12+00",
                    type(Oid.TIMESTAMPTZ)));
        assertEquals("42821", e.getSQLState());
        assertEquals("A column of type timestamptz cannot be read as java.time.LocalDateTime.",
                e.getMessage());
        assertEquals("42821", assertThrows(SQLException.class,
                () -> JavaTimeGetters.getLocalTime("10:11:12+00", type(Oid.TIMETZ))).getSQLState());
        assertEquals("42821", assertThrows(SQLException.class,
                () -> JavaTimeGetters.getLocalDate("2020-01-02", type(Oid.TEXT))).getSQLState());
        assertEquals("22007", assertThrows(SQLException.class, () -> JavaTimeGetters
                .getOffsetDateTime("24:00:00+00", type(Oid.TIMETZ))).getSQLState());
    }
}
