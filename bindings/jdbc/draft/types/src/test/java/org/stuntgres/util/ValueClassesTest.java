package org.stuntgres.util;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.sql.SQLException;
import java.util.ArrayList;
import java.util.Calendar;
import java.util.GregorianCalendar;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.TimeZone;
import org.junit.jupiter.api.Test;
import org.stuntgres.core.TestData;
import org.stuntgres.testing.UnitTest;

/**
 * The behavior of PGobject, PGInterval, PGmoney, PGTime and PGTimestamp. The cases and their results were recorded from the classes of the same names of
 * the PostgreSQL JDBC driver (value-classes-util.tsv); this file is a generated table.
 */
@UnitTest
class ValueClassesTest {
    interface C {
        Object run() throws Exception;
    }

    static final Map<String, C> CASES = new LinkedHashMap<>();

    static void c(String id, C c) {
        if (CASES.put(id, c) != null) {
            throw new IllegalStateException(id);
        }
    }

    static String d(double v) {
        return Double.toString(v);
    }

    static PGobject obj(String type, String value) throws SQLException {
        PGobject o = new PGobject();
        o.setType(type);
        o.setValue(value);
        return o;
    }

    static Object nothing() {
        return null;
    }

    static String iv(PGInterval i) {
        return i.getYears() + "y " + i.getMonths() + "m " + i.getDays() + "d " + i.getHours() + "h "
                + i.getMinutes() + "mi " + d(i.getSeconds()) + "s whole=" + i.getWholeSeconds()
                + " micro=" + i.getMicroSeconds() + " value=" + i.getValue() + " null=" + i.isNull()
                + " type=" + i.getType();
    }

    static final String[] INTERVALS = {"0", "00:00:00", "1 year 2 mons 3 days 04:05:06.789", "-1 years -2 mons +3 days -04:05:06.789012", "1 day", "00:00:01", "-00:00:00.000001",
        "178000000 years", "1 mon", "2 years", "1 day 00:00:00.5", "-1 days", "100:00:00", "1 year -1 mons", "infinity", "-infinity", "3 days 04:05:06", "00:00:01.5", "14 mons",
        "1 day -00:00:01", "25:00:00", "-00:59:59.999999", "", " ", "abc", "1", "1 year 2 mons 3 days 04:05:06.789 ago", "@ 1 year 2 mons 3 days 4 hours 5 mins 6.789 secs",
        "@ 1 year 2 mons -3 days 4 hours 5 mins 6.789 secs ago", "@ 0", "P1Y2M3DT4H5M6.789S", "PT0S", "P-1Y-2M3DT-4H-5M-6.789012S", "P1D", "PT1S", "+1-2 +3 +4:05:06.789", "1-2",
        "3 4:05:06", "-1-2 +3 -4:05:06.789012", "1 year 2 months 3 days 4 hours 5 minutes 6 seconds", "1 years 2 mons", "1 hour", "1 minute", "1 second", "1 week", "1 decade",
        "1 century", "1 millennium", "1.5 days", "1 day 2", "04:05", "04:05:06", "-04:05:06", "+04:05:06", "1 day +04:05:06", "1 day -04:05", "10 days 00:00:00.000001",
        "1 mons", "2 mon", "1 yr", "1 hr", "1 min", "1 sec", "1 d", "1 h", "1 m", "1 s", "4:5:6", "1 day 4:5:6.5", "P1W", "P1Y", "PT1H1M1S", "P0001-02-03T04:05:06",
        "1 YEAR 2 MONS", "1 Day", "00:00:00.1234567", "00:00:59.9999999", "23:59:60", "-1 year", "- 1 year", "1year", "1 mon 2 years", "2147483647 years", "2147483648 years",
        "1 day 1 day", "0.5 years", "1e2 days", "+1 day", "1 day ago", "ago", "1 microsecond", "1 millisecond", "1000 milliseconds"};
    static final String[] MONEY = {"$12.34", "-$12.34", "($12.34)", "$1,234,567.89", "12.34", "$0.00", "", "abc", "$", "-12.34", "(12.34)", "$-12.34", "1,234", "$1.5", " $1.00", "$1.00 ",
        "€1.00", "$92,233,720,368,547,758.07", "-$92,233,720,368,547,758.08", "1e3", "NaN", "$1,23,4"};

    static {
        // PGobject
        c("obj.new", () -> { PGobject o = new PGobject(); return o.getType() + "|" + o.getValue() + "|" + o.isNull() + "|" + o + "|" + o.hashCode(); });
        c("obj.set", () -> { PGobject o = obj("json", "{}"); return o.getType() + "|" + o.getValue() + "|" + o.isNull() + "|" + o + "|" + (o.hashCode() == ("{}".hashCode() ^ "json".hashCode())) + "|" + (o.hashCode() == "{}".hashCode()); });
        c("obj.hash.nullValue", () -> obj("json", null).hashCode() == "json".hashCode());
        c("obj.hash.nullType", () -> obj(null, "v").hashCode() == "v".hashCode());
        c("obj.equals.same", () -> obj("json", "{}").equals(obj("json", "{}")));
        c("obj.equals.otherType", () -> obj("json", "{}").equals(obj("jsonb", "{}")));
        c("obj.equals.otherValue", () -> obj("json", "{}").equals(obj("json", "[]")));
        c("obj.equals.null", () -> obj("json", "{}").equals(null));
        c("obj.equals.string", () -> obj("json", "{}").equals("{}"));
        c("obj.equals.bothNullValues", () -> obj("json", null).equals(obj("json", null)));
        c("obj.equals.nullValueVsValue", () -> obj("json", null).equals(obj("json", "{}")) + "|" + obj("json", "{}").equals(obj("json", null)));
        c("obj.equals.nullTypes", () -> obj(null, "a").equals(obj(null, "a")) + "|" + obj(null, "a").equals(obj("t", "a")) + "|" + obj("t", "a").equals(obj(null, "a")));
        c("obj.clone", () -> { PGobject o = obj("json", "{}"); PGobject k = (PGobject) o.clone(); k.setValue("[]"); return o.getValue() + "|" + k.getValue() + "|" + k.getType() + "|" + (k.getClass() == PGobject.class); });
        c("obj.toString.null", () -> String.valueOf(obj("json", null).toString()));
        c("obj.serializable", () -> { java.io.ByteArrayOutputStream b = new java.io.ByteArrayOutputStream(); new java.io.ObjectOutputStream(b).writeObject(obj("json", "{}"));
            PGobject o = (PGobject) new java.io.ObjectInputStream(new java.io.ByteArrayInputStream(b.toByteArray())).readObject(); return o.getType() + "|" + o.getValue(); });
        // PGInterval
        c("interval.new", () -> iv(new PGInterval()));
        for (String s : INTERVALS) c("interval.parse[" + s + "]", () -> iv(new PGInterval(s)));
        c("interval.parse.null", () -> iv(new PGInterval((String) null)));
        c("interval.setValue.null", () -> { PGInterval i = new PGInterval("1 day"); i.setValue(null); return iv(i); });
        c("interval.fields", () -> iv(new PGInterval(1, 2, 3, 4, 5, 6.789)));
        c("interval.fields.negative", () -> iv(new PGInterval(-1, -2, -3, -4, -5, -6.789012)));
        c("interval.fields.zero", () -> iv(new PGInterval(0, 0, 0, 0, 0, 0)));
        c("interval.fields.mixed", () -> iv(new PGInterval(1, -2, 3, -4, 5, -6.5)));
        c("interval.fields.overflow", () -> iv(new PGInterval(0, 14, 40, 25, 61, 61.5)));
        c("interval.fields.rounding", () -> iv(new PGInterval(0, 0, 0, 0, 0, 1.2345678)) + " / " + iv(new PGInterval(0, 0, 0, 0, 0, 59.9999996)) + " / " + iv(new PGInterval(0, 0, 0, 0, 0, 0.0000004)));
        c("interval.fields.negzero", () -> iv(new PGInterval(0, 0, 0, 0, 0, -0.5)) + " / " + iv(new PGInterval(0, 0, 0, -1, 0, 0.5)) + " / " + iv(new PGInterval(0, 0, 0, 0, -1, 0)));
        c("interval.setters", () -> { PGInterval i = new PGInterval(); i.setYears(1); i.setMonths(2); i.setDays(3); i.setHours(4); i.setMinutes(5); i.setSeconds(6.5); return iv(i); });
        c("interval.setSeconds", () -> { PGInterval i = new PGInterval("1 day"); i.setSeconds(1.9999999); String a = iv(i); i.setSeconds(-2.25); return a + " / " + iv(i); });
        c("interval.setValue6", () -> { PGInterval i = new PGInterval(); i.setValue(1, 2, 3, 4, 5, 6.789); return iv(i); });
        c("interval.add.calendar", () -> { Calendar k = new GregorianCalendar(TimeZone.getTimeZone("UTC")); k.setTimeInMillis(1577960000123L); new PGInterval(1, 2, 3, 4, 5, 6.789).add(k); return k.getTimeInMillis(); });
        c("interval.add.calendar.negative", () -> { Calendar k = new GregorianCalendar(TimeZone.getTimeZone("UTC")); k.setTimeInMillis(1577960000123L); new PGInterval(-1, -2, -3, -4, -5, -6.789012).add(k); return k.getTimeInMillis(); });
        c("interval.add.calendar.micros", () -> { Calendar k = new GregorianCalendar(TimeZone.getTimeZone("UTC")); k.setTimeInMillis(0L); new PGInterval(0, 0, 0, 0, 0, 0.0015).add(k); long a = k.getTimeInMillis(); new PGInterval(0, 0, 0, 0, 0, 0.0004).add(k); return a + "|" + k.getTimeInMillis(); });
        c("interval.add.calendar.null", () -> { Calendar k = new GregorianCalendar(TimeZone.getTimeZone("UTC")); k.setTimeInMillis(5L); new PGInterval().add(k); return k.getTimeInMillis(); });
        c("interval.add.date", () -> { java.util.Date dt = new java.util.Date(1577960000123L); new PGInterval(0, 0, 1, 2, 3, 4.5).add(dt); return dt.getTime(); });
        c("interval.add.interval", () -> { PGInterval i = new PGInterval(1, 2, 3, 4, 5, 6.5); i.add(new PGInterval(1, 11, 30, 23, 59, 59.75)); return iv(i); });
        c("interval.add.interval.null", () -> { PGInterval i = new PGInterval(1, 2, 3, 4, 5, 6.5); i.add(new PGInterval()); return iv(i); });
        c("interval.scale", () -> { PGInterval i = new PGInterval(1, 2, 3, 4, 5, 6.5); i.scale(3); return iv(i); });
        c("interval.scale.negative", () -> { PGInterval i = new PGInterval(1, 2, 3, 4, 5, 6.5); i.scale(-1); return iv(i); });
        c("interval.scale.zero", () -> { PGInterval i = new PGInterval(1, 2, 3, 4, 5, 6.5); i.scale(0); return iv(i); });
        c("interval.equals", () -> new PGInterval(1, 2, 3, 4, 5, 6.5).equals(new PGInterval("1 year 2 mons 3 days 04:05:06.5")) + "|" + new PGInterval(1, 0, 0, 0, 0, 0).equals(new PGInterval(0, 12, 0, 0, 0, 0))
            + "|" + new PGInterval().equals(new PGInterval()) + "|" + new PGInterval().equals(new PGInterval(0, 0, 0, 0, 0, 0)) + "|" + new PGInterval(0, 0, 0, 0, 0, 0).equals(new PGInterval())
            + "|" + new PGInterval(1, 0, 0, 0, 0, 0).equals(null) + "|" + new PGInterval(1, 0, 0, 0, 0, 0).equals("1 year") + "|" + new PGInterval(0, 0, 0, 0, 0, 1.5).equals(new PGInterval(0, 0, 0, 0, 0, 1.5000001)));
        c("interval.hashCode", () -> new PGInterval(1, 2, 3, 4, 5, 6.5).hashCode() + "|" + new PGInterval().hashCode() + "|" + new PGInterval(0, 0, 0, 0, 0, 0).hashCode() + "|" + new PGInterval(0, 0, 0, 0, 0, 0.000001).hashCode());
        c("interval.clone", () -> { PGInterval i = new PGInterval(1, 2, 3, 4, 5, 6.5); PGInterval k = (PGInterval) i.clone(); k.setYears(9); return iv(i) + " / " + iv(k); });
        c("interval.toString", () -> new PGInterval(1, 2, 3, 4, 5, 6.5).toString() + "|" + new PGInterval().toString());
        c("interval.setType", () -> { PGInterval i = new PGInterval(); i.setType("x"); return i.getType(); });
        // PGmoney
        c("money.new", () -> { PGmoney m = new PGmoney(); return d(m.val) + "|" + m.isNull + "|" + m.getValue() + "|" + m.getType() + "|" + m.isNull(); });
        for (double v : new double[] {12.34, -12.34, 0, 1234567.89, 0.005, 1e15, -0.001, 0.1 + 0.2, Double.NaN, 1.0 / 3})
            c("money.double[" + v + "]", () -> { PGmoney m = new PGmoney(v); return d(m.val) + "|" + m.isNull + "|" + m.getValue() + "|" + m.getType(); });
        for (String s : MONEY) c("money.parse[" + s + "]", () -> { PGmoney m = new PGmoney(s); return d(m.val) + "|" + m.isNull + "|" + m.getValue(); });
        c("money.parse.null", () -> { PGmoney m = new PGmoney((String) null); return d(m.val) + "|" + m.isNull + "|" + m.getValue() + "|" + m.isNull(); });
        c("money.setValue.null", () -> { PGmoney m = new PGmoney(5); m.setValue(null); return d(m.val) + "|" + m.isNull + "|" + m.getValue(); });
        c("money.equals", () -> new PGmoney(1.5).equals(new PGmoney("$1.50")) + "|" + new PGmoney(1.5).equals(new PGmoney(2)) + "|" + new PGmoney().equals(new PGmoney()) + "|" + new PGmoney(0).equals(new PGmoney())
            + "|" + new PGmoney().equals(new PGmoney(0)) + "|" + new PGmoney(1).equals(null) + "|" + new PGmoney(1).equals(obj("money", "$1.00")));
        c("money.hashCode", () -> new PGmoney(1.5).hashCode() + "|" + new PGmoney().hashCode() + "|" + new PGmoney(0).hashCode() + "|" + new PGmoney(-12.34).hashCode());
        c("money.clone", () -> { PGmoney m = new PGmoney(1.5); PGmoney k = (PGmoney) m.clone(); k.val = 9; return d(m.val) + "|" + d(k.val) + "|" + k.getType(); });
        c("money.field", () -> { PGmoney m = new PGmoney(1.5); m.val = -3.25; String a = m.getValue(); m.isNull = true; return a + "|" + m.getValue() + "|" + m.isNull(); });
        c("money.toString", () -> new PGmoney(1.5).toString() + "|" + new PGmoney().toString());
        // PGTime, PGTimestamp
        c("pgtime.new", () -> { PGTime t = new PGTime(1234L); return t.getTime() + "|" + t.getCalendar(); });
        c("pgtime.cal", () -> { Calendar k = new GregorianCalendar(TimeZone.getTimeZone("Asia/Tokyo")); PGTime t = new PGTime(1234L, k); return t.getTime() + "|" + (t.getCalendar() == k) + "|" + t.getCalendar().getTimeZone().getID(); });
        c("pgtime.setCalendar", () -> { PGTime t = new PGTime(1234L); Calendar k = new GregorianCalendar(TimeZone.getTimeZone("Asia/Tokyo")); t.setCalendar(k); boolean a = t.getCalendar() == k; t.setCalendar(null); return a + "|" + t.getCalendar(); });
        c("pgtime.equals", () -> { Calendar k = new GregorianCalendar(TimeZone.getTimeZone("Asia/Tokyo")); Calendar k2 = new GregorianCalendar(TimeZone.getTimeZone("UTC"));
            return new PGTime(1L).equals(new PGTime(1L)) + "|" + new PGTime(1L).equals(new PGTime(2L)) + "|" + new PGTime(1L, k).equals(new PGTime(1L, k)) + "|" + new PGTime(1L, k).equals(new PGTime(1L, k2))
                + "|" + new PGTime(1L, k).equals(new PGTime(1L)) + "|" + new PGTime(1L).equals(new PGTime(1L, k)) + "|" + new PGTime(1L).equals(new java.sql.Time(1L)) + "|" + new java.sql.Time(1L).equals(new PGTime(1L))
                + "|" + new PGTime(1L).equals(null); });
        c("pgtime.hashCode", () -> { Calendar k = new GregorianCalendar(TimeZone.getTimeZone("Asia/Tokyo")); k.setTimeInMillis(0); return (new PGTime(1L).hashCode() == new java.sql.Time(1L).hashCode()) + "|" + (new PGTime(1L).hashCode() == 31 * new java.sql.Time(1L).hashCode())
            + "|" + (new PGTime(1L, k).hashCode() == 31 * new java.sql.Time(1L).hashCode() + k.hashCode()); });
        c("pgtime.clone", () -> { Calendar k = new GregorianCalendar(TimeZone.getTimeZone("Asia/Tokyo")); PGTime t = new PGTime(1L, k); PGTime n = (PGTime) t.clone(); return n.getTime() + "|" + (n.getCalendar() == k) + "|" + n.getCalendar().equals(k) + "|" + (n.getClass() == PGTime.class); });
        c("pgtimestamp.new", () -> { PGTimestamp t = new PGTimestamp(1234L); return t.getTime() + "|" + t.getNanos() + "|" + t.getCalendar(); });
        c("pgtimestamp.cal", () -> { Calendar k = new GregorianCalendar(TimeZone.getTimeZone("Asia/Tokyo")); PGTimestamp t = new PGTimestamp(1234L, k); return t.getTime() + "|" + (t.getCalendar() == k); });
        c("pgtimestamp.setCalendar", () -> { PGTimestamp t = new PGTimestamp(1234L); Calendar k = new GregorianCalendar(TimeZone.getTimeZone("Asia/Tokyo")); t.setCalendar(k); boolean a = t.getCalendar() == k; t.setCalendar(null); return a + "|" + t.getCalendar(); });
        c("pgtimestamp.equals", () -> { Calendar k = new GregorianCalendar(TimeZone.getTimeZone("Asia/Tokyo")); Calendar k2 = new GregorianCalendar(TimeZone.getTimeZone("UTC"));
            return new PGTimestamp(1L).equals(new PGTimestamp(1L)) + "|" + new PGTimestamp(1L).equals(new PGTimestamp(2L)) + "|" + new PGTimestamp(1L, k).equals(new PGTimestamp(1L, k)) + "|" + new PGTimestamp(1L, k).equals(new PGTimestamp(1L, k2))
                + "|" + new PGTimestamp(1L, k).equals(new PGTimestamp(1L)) + "|" + new PGTimestamp(1L).equals(new PGTimestamp(1L, k)) + "|" + new PGTimestamp(1L).equals((Object) new java.sql.Timestamp(1L)) + "|" + new java.sql.Timestamp(1L).equals((Object) new PGTimestamp(1L))
                + "|" + new PGTimestamp(1L).equals(nothing()); });
        c("pgtimestamp.hashCode", () -> { Calendar k = new GregorianCalendar(TimeZone.getTimeZone("Asia/Tokyo")); k.setTimeInMillis(0); return (new PGTimestamp(1L).hashCode() == new java.sql.Timestamp(1L).hashCode()) + "|" + (new PGTimestamp(1L).hashCode() == 31 * new java.sql.Timestamp(1L).hashCode())
            + "|" + (new PGTimestamp(1L, k).hashCode() == 31 * new java.sql.Timestamp(1L).hashCode() + k.hashCode()); });
        c("pgtimestamp.clone", () -> { Calendar k = new GregorianCalendar(TimeZone.getTimeZone("Asia/Tokyo")); PGTimestamp t = new PGTimestamp(1L, k); t.setNanos(123456789); PGTimestamp n = (PGTimestamp) t.clone(); return n.getTime() + "|" + n.getNanos() + "|" + (n.getCalendar() == k) + "|" + n.getCalendar().equals(k); });
    }

    @Test
    void recordedCases() {
        List<String> failures = new ArrayList<>();
        int count = 0;
        for (String[] row : TestData.rows("value-classes-util.tsv")) {
            C c = CASES.get(TestData.unescape(row[0]));
            String got;
            try {
                got = TestData.escape(String.valueOf(c.run()));
            } catch (SQLException e) {
                got = "!" + e.getClass().getSimpleName() + "|" + e.getSQLState();
            } catch (Exception e) {
                got = "!" + e.getClass().getSimpleName() + "|-";
            }
            count++;
            if (!got.equals(row[1]) && failures.size() < 20) {
                failures.add(row[0] + ": expected " + row[1] + " but was " + got);
            }
        }
        assertEquals(CASES.size(), count);
        assertTrue(count > 100, "cases: " + count);
        assertEquals("", String.join("\n", failures));
    }
}
