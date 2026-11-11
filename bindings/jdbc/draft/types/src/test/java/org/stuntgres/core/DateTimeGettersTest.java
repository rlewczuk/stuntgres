package org.stuntgres.core;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;

import java.sql.SQLException;
import java.sql.Timestamp;
import java.util.Calendar;
import java.util.GregorianCalendar;
import java.util.HashMap;
import java.util.Map;
import java.util.TimeZone;
import org.junit.jupiter.api.Test;
import org.stuntgres.testing.UnitTest;

@UnitTest
class DateTimeGettersTest {
    /** The Calendar argument of the recorded calls. */
    static Calendar adelaide() {
        return new GregorianCalendar(TimeZone.getTimeZone("Australia/Adelaide"));
    }

    private static ConversionContext context(String zone) {
        return new ConversionContext(TimeZone.getTimeZone(zone), Matrix.LOOKUP, false, false);
    }

    @Test
    void recordedCells() {
        Map<String, Matrix.Getter> getters = new HashMap<>();
        getters.put("getDate", (v, text, c) -> DateTimeGetters.getDate(text, null, c));
        getters.put("getDate(cal)", (v, text, c) -> DateTimeGetters.getDate(text, adelaide(), c));
        getters.put("getTime", (v, text, c) -> DateTimeGetters.getTime(text, null, c));
        getters.put("getTime(cal)", (v, text, c) -> DateTimeGetters.getTime(text, adelaide(), c));
        getters.put("getTimestamp",
            (v, text, c) -> DateTimeGetters.getTimestamp(text, v.oid, null, c));
        getters.put("getTimestamp(cal)",
            (v, text, c) -> DateTimeGetters.getTimestamp(text, v.oid, adelaide(), c));
        Matrix.check("get-datetime.tsv", getters);
    }

    @Test
    void timestamps() throws SQLException {
        Timestamp utc = DateTimeGetters.getTimestamp("2020-01-02 10:11:12.123456", Oid.TIMESTAMP,
                null, context("UTC"));
        assertEquals(1577959872123L, utc.getTime());
        assertEquals(123456000, utc.getNanos());
        assertEquals(1577956272123L, DateTimeGetters.getTimestamp("2020-01-02 10:11:12.123456",
                Oid.TIMESTAMP, null, context("Europe/Warsaw")).getTime());
        assertEquals(1577922072123L, DateTimeGetters.getTimestamp("2020-01-02 10:11:12.123456",
                Oid.TIMESTAMP, adelaide(), context("UTC")).getTime());
        assertEquals(1577959872000L, DateTimeGetters.getTimestamp("2020-01-02 15:41:12+05:30",
                Oid.TIMESTAMPTZ, adelaide(), context("Europe/Warsaw")).getTime());
        assertEquals(DateTimeText.POSITIVE_INFINITY, DateTimeGetters.getTimestamp("infinity",
                Oid.TIMESTAMP, null, context("UTC")).getTime());
        assertEquals(DateTimeText.NEGATIVE_INFINITY, DateTimeGetters.getTimestamp("-infinity",
                Oid.TIMESTAMPTZ, null, context("UTC")).getTime());
        SQLException e = assertThrows(SQLException.class, () -> DateTimeGetters.getTimestamp(
                "abc", Oid.TEXT, null, context("UTC")));
        assertEquals("22007", e.getSQLState());
        assertEquals("Cannot read the value \"abc\" as a date, time or timestamp.", e.getMessage());
    }

    @Test
    void datesAndTimes() throws SQLException {
        assertEquals(1577923200000L,
                DateTimeGetters.getDate("2020-01-02", null, context("UTC")).getTime());
        assertEquals(1577919600000L,
                DateTimeGetters.getDate("2020-01-02", null, context("Europe/Warsaw")).getTime());
        assertEquals(1577885400000L,
                DateTimeGetters.getDate("2020-01-02", adelaide(), context("UTC")).getTime());
        assertEquals(36672000L,
                DateTimeGetters.getTime("10:11:12", null, context("UTC")).getTime());
        assertEquals(33072000L,
                DateTimeGetters.getTime("10:11:12", null, context("Europe/Warsaw")).getTime());
        assertEquals(36672500L,
                DateTimeGetters.getTime("12:11:12.5+02", null, context("Europe/Warsaw")).getTime());
        Calendar calendar = adelaide();
        calendar.setTimeInMillis(5L);
        DateTimeGetters.getDate("2020-01-02", calendar, context("UTC"));
        assertEquals(5L, calendar.getTimeInMillis());
        assertEquals("22007", assertThrows(SQLException.class,
                () -> DateTimeGetters.getDate("t", null, context("UTC"))).getSQLState());
        assertEquals("22007", assertThrows(SQLException.class,
                () -> DateTimeGetters.getTime("10:11", null, context("UTC"))).getSQLState());
    }
}
