package org.stuntgres.core;

import java.io.ByteArrayInputStream;
import java.io.InputStream;
import java.io.StringReader;
import java.lang.reflect.Field;
import java.math.BigDecimal;
import java.math.BigInteger;
import java.nio.charset.StandardCharsets;
import java.sql.Date;
import java.sql.JDBCType;
import java.sql.SQLException;
import java.sql.SQLXML;
import java.sql.Time;
import java.sql.Timestamp;
import java.sql.Types;
import java.time.Duration;
import java.time.Instant;
import java.time.LocalDate;
import java.time.LocalDateTime;
import java.time.LocalTime;
import java.time.OffsetDateTime;
import java.time.OffsetTime;
import java.time.ZonedDateTime;
import java.util.Calendar;
import java.util.GregorianCalendar;
import java.util.HashMap;
import java.util.LinkedHashMap;
import java.util.Map;
import java.util.TimeZone;
import java.util.UUID;
import org.stuntgres.geometric.PGbox;
import org.stuntgres.geometric.PGcircle;
import org.stuntgres.geometric.PGline;
import org.stuntgres.geometric.PGlseg;
import org.stuntgres.geometric.PGpath;
import org.stuntgres.geometric.PGpoint;
import org.stuntgres.geometric.PGpolygon;
import org.stuntgres.util.PGInterval;
import org.stuntgres.util.PGTime;
import org.stuntgres.util.PGTimestamp;
import org.stuntgres.util.PGmoney;
import org.stuntgres.util.PGobject;

/**
 * The recorded setter cases (set.tsv): every case id with the call that produces it. The
 * calls go to {@link SetterCalls}, which maps each PreparedStatement setter to the
 * conversion function behind it. Values of java.sql.Date, Time and Timestamp that the
 * recording made with valueOf are built for the default time zone of the row.
 */
final class SetterCases {
    /** A setter call. */
    interface Call {
        void set(SetterCalls ps, int i, SetterCalls c) throws Exception;
    }

    final Map<String, Call> cases = new LinkedHashMap<>();
    private final TimeZone zone;
    private String family;

    SetterCases(TimeZone zone) throws Exception {
        this.zone = zone;
        build();
    }

    private void c(String id, Call call) {
        if (cases.put(id, call) != null) {
            throw new IllegalStateException(id);
        }
    }

    static Calendar cal() {
        return new GregorianCalendar(TimeZone.getTimeZone("Australia/Adelaide"));
    }

    private static Calendar calAt(long millis) {
        Calendar calendar = cal();
        calendar.setTimeInMillis(millis);
        return calendar;
    }

    /** The milliseconds of a local date and time in the default zone of this object. */
    private long local(int year, int month, int day, int hour, int minute, int second) {
        Calendar calendar = new GregorianCalendar(zone);
        calendar.clear();
        calendar.set(year, month - 1, day, hour, minute, second);
        return calendar.getTimeInMillis();
    }

    private Date date(String text) {
        LocalDate d = LocalDate.parse(text);
        return new Date(local(d.getYear(), d.getMonthValue(), d.getDayOfMonth(), 0, 0, 0));
    }

    private Time time(String text) {
        LocalTime t = LocalTime.parse(text);
        return new Time(local(1970, 1, 1, t.getHour(), t.getMinute(), t.getSecond()));
    }

    private Timestamp timestamp(String text) {
        LocalDateTime t = LocalDateTime.parse(text.replace(' ', 'T'));
        Timestamp timestamp = new Timestamp(local(t.getYear(), t.getMonthValue(), t.getDayOfMonth(),
                t.getHour(), t.getMinute(), t.getSecond()));
        timestamp.setNanos(t.getNano());
        return timestamp;
    }

    private static InputStream in(String s) {
        return new ByteArrayInputStream(s.getBytes(StandardCharsets.UTF_8));
    }

    private static PGobject pg(String type, String value) throws SQLException {
        PGobject o = new PGobject();
        o.setType(type);
        o.setValue(value);
        return o;
    }

    /** An enum for the case of a class without a PostgreSQL type. */
    enum Color { RED }

    private void build() throws Exception {
        family = "null";
        for (Field f : Types.class.getFields()) { int t = f.getInt(null); c("setNull(Types." + f.getName() + ")", (ps, i, c) -> ps.setNull(i, t)); }
        c("setNull(12345)", (ps, i, c) -> ps.setNull(i, 12345));
        for (String[] tn : new String[][] {{"OTHER", "json"}, {"ARRAY", "_int4"}, {"ARRAY", "int4[]"}, {"STRUCT", "pair"}, {"DISTINCT", "posint"},
                {"INTEGER", "nosuchtype"}, {"OTHER", "uuid"}, {"VARCHAR", "mood"}, {"OTHER", null}, {"INTEGER", "text"}, {"OTHER", "nosuchtype"},
                {"NULL", "int4"}, {"VARCHAR", null}, {"OTHER", "s2.color"}, {"OTHER", "hstore"}, {"REF", "int4"}}) {
            int t = Types.class.getField(tn[0]).getInt(null);
            c("setNull(Types." + tn[0] + "," + tn[1] + ")", (ps, i, c) -> ps.setNull(i, t, tn[1]));
        }
        family = "bool";
        c("setBoolean(true)", (ps, i, c) -> ps.setBoolean(i, true));
        c("setBoolean(false)", (ps, i, c) -> ps.setBoolean(i, false));
        family = "number";
        for (byte v : new byte[] {0, 1, -1, 127, -128}) c("setByte(" + v + ")", (ps, i, c) -> ps.setByte(i, v));
        for (short v : new short[] {0, 1, -1, 32767, -32768}) c("setShort(" + v + ")", (ps, i, c) -> ps.setShort(i, v));
        for (int v : new int[] {0, 1, -1, Integer.MAX_VALUE, Integer.MIN_VALUE}) c("setInt(" + v + ")", (ps, i, c) -> ps.setInt(i, v));
        for (long v : new long[] {0, 1, -1, Long.MAX_VALUE, Long.MIN_VALUE, 2147483648L}) c("setLong(" + v + ")", (ps, i, c) -> ps.setLong(i, v));
        for (float v : new float[] {0f, -0f, 1f, 1.5f, 0.1f, Float.MAX_VALUE, Float.MIN_VALUE, Float.NaN, Float.POSITIVE_INFINITY, Float.NEGATIVE_INFINITY, 16777216f, 1e10f, 123456.79f})
            c("setFloat(" + v + ")", (ps, i, c) -> ps.setFloat(i, v));
        for (double v : new double[] {0d, -0d, 1d, 1.5d, 0.1d, Double.MAX_VALUE, Double.MIN_VALUE, Double.NaN, Double.POSITIVE_INFINITY, Double.NEGATIVE_INFINITY, 1e15, 1e16, 1e21, 123456789.12345679, 1e-5})
            c("setDouble(" + v + ")", (ps, i, c) -> ps.setDouble(i, v));
        for (String v : new String[] {"0", "1", "-1", "1.50", "0.0", "1E+100", "1E-20", "-0.000001", "123456789012345678901234567890.123456789", "1E+3", "0E-10", "1e400"})
            c("setBigDecimal(" + v + ")", (ps, i, c) -> ps.setBigDecimal(i, new BigDecimal(v)));
        c("setBigDecimal(null)", (ps, i, c) -> ps.setBigDecimal(i, null));
        family = "string";
        for (String v : new String[] {"", "abc", "😀", "a\u0000b", "it's", "1", "true", "2020-01-02", "{\"a\":1}", "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11", "12.5", " x ", "a\\b", "abcdef"}) {
            c("setString(" + TestData.escape(v) + ")", (ps, i, c) -> ps.setString(i, v));
            c("setNString(" + TestData.escape(v) + ")", (ps, i, c) -> ps.setNString(i, v));
        }
        c("setString(null)", (ps, i, c) -> ps.setString(i, null));
        c("setNString(null)", (ps, i, c) -> ps.setNString(i, null));
        c("setString(\\ud83d)", (ps, i, c) -> ps.setString(i, "a\ud83db"));
        family = "bytes";
        byte[] all = new byte[256]; for (int k = 0; k < 256; k++) all[k] = (byte) k;
        c("setBytes(empty)", (ps, i, c) -> ps.setBytes(i, new byte[0]));
        c("setBytes(all256)", (ps, i, c) -> ps.setBytes(i, all));
        c("setBytes(616263)", (ps, i, c) -> ps.setBytes(i, new byte[] {0x61, 0x62, 0x63}));
        c("setBytes(00)", (ps, i, c) -> ps.setBytes(i, new byte[] {0}));
        c("setBytes(null)", (ps, i, c) -> ps.setBytes(i, null));
        family = "temporal";
        Map<String, Long> dates = new LinkedHashMap<>();
        for (String v : new String[] {"2020-01-02", "1970-01-01", "0001-01-01", "1582-10-04", "1582-10-15", "9999-12-31", "2021-03-28", "1900-01-01", "1883-11-18"})
            dates.put("valueOf " + v, date(v).getTime());
        dates.put("millis 0", 0L); dates.put("millis 1577960000123", 1577960000123L); dates.put("millis -1", -1L);
        dates.put("millis -62198755200000 (0044-03-15 BC UTC)", -63517824000000L);
        dates.put("millis 253402300800000 (10000-01-01 UTC)", 253402300800000L);
        dates.put("DATE_POSITIVE_INFINITY", DateTimeText.POSITIVE_INFINITY); dates.put("DATE_NEGATIVE_INFINITY", DateTimeText.NEGATIVE_INFINITY);
        dates.put("DATE_POSITIVE_SMALLER_INFINITY", DateTimeText.POSITIVE_SMALLER_INFINITY);
        dates.put("Long.MAX_VALUE", Long.MAX_VALUE); dates.put("Long.MIN_VALUE", Long.MIN_VALUE);
        for (Map.Entry<String, Long> e : dates.entrySet()) {
            long m = e.getValue();
            c("setDate(" + e.getKey() + ")", (ps, i, c) -> ps.setDate(i, new Date(m)));
            c("setDate(" + e.getKey() + ",cal)", (ps, i, c) -> ps.setDate(i, new Date(m), cal()));
            c("setObject(Date " + e.getKey() + ")", (ps, i, c) -> ps.setObject(i, new Date(m)));
        }
        c("setDate(null)", (ps, i, c) -> ps.setDate(i, null));
        c("setDate(valueOf 2020-01-02,null)", (ps, i, c) -> ps.setDate(i, date("2020-01-02"), null));
        Map<String, Long> times = new LinkedHashMap<>();
        for (String v : new String[] {"10:11:12", "00:00:00", "23:59:59", "02:30:00"}) times.put("valueOf " + v, time(v).getTime());
        times.put("millis 0", 0L); times.put("millis 86399999", 86399999L); times.put("millis 1577960000123", 1577960000123L); times.put("millis -1", -1L);
        times.put("millis 43200123", 43200123L);
        for (Map.Entry<String, Long> e : times.entrySet()) {
            long m = e.getValue();
            c("setTime(" + e.getKey() + ")", (ps, i, c) -> ps.setTime(i, new Time(m)));
            c("setTime(" + e.getKey() + ",cal)", (ps, i, c) -> ps.setTime(i, new Time(m), cal()));
            c("setObject(Time " + e.getKey() + ")", (ps, i, c) -> ps.setObject(i, new Time(m)));
            c("setObject(PGTime " + e.getKey() + ")", (ps, i, c) -> ps.setObject(i, new PGTime(m)));
            c("setObject(PGTime " + e.getKey() + ",cal)", (ps, i, c) -> ps.setObject(i, new PGTime(m, cal())));
            c("setTime(PGTime " + e.getKey() + ",cal)", (ps, i, c) -> ps.setTime(i, new PGTime(m, cal())));
        }
        c("setTime(null)", (ps, i, c) -> ps.setTime(i, null));
        c("setTime(valueOf 10:11:12,null)", (ps, i, c) -> ps.setTime(i, time("10:11:12"), null));
        Map<String, Timestamp> tss = new LinkedHashMap<>();
        for (String v : new String[] {"2020-01-02 10:11:12", "2020-01-02 10:11:12.123456789", "2020-01-02 10:11:12.5", "2020-01-02 10:11:12.000001", "2020-01-02 10:11:12.9999995",
                "2020-12-31 23:59:59.9999999", "1970-01-01 00:00:00", "0001-01-01 00:00:00", "1582-10-04 23:59:59", "1582-10-15 00:00:00", "9999-12-31 23:59:59.999999",
                "2021-03-28 02:30:00", "2021-10-31 02:30:00", "2020-06-15 12:00:00", "1900-01-01 00:00:00", "1883-11-18 12:00:00", "2020-01-02 10:11:12.000000001",
                "2020-01-02 10:11:12.1"})
            tss.put("valueOf " + v, timestamp(v));
        tss.put("millis 0", new Timestamp(0)); tss.put("millis -1", new Timestamp(-1)); tss.put("millis 1577960000123", new Timestamp(1577960000123L));
        tss.put("millis -63517824000000 (BC)", new Timestamp(-63517824000000L)); tss.put("millis 253402300800000 (y10000)", new Timestamp(253402300800000L));
        tss.put("DATE_POSITIVE_INFINITY", new Timestamp(DateTimeText.POSITIVE_INFINITY)); tss.put("DATE_NEGATIVE_INFINITY", new Timestamp(DateTimeText.NEGATIVE_INFINITY));
        tss.put("DATE_POSITIVE_SMALLER_INFINITY", new Timestamp(DateTimeText.POSITIVE_SMALLER_INFINITY));
        tss.put("Long.MAX_VALUE", new Timestamp(Long.MAX_VALUE)); tss.put("Long.MIN_VALUE", new Timestamp(Long.MIN_VALUE));
        for (Map.Entry<String, Timestamp> e : tss.entrySet()) {
            Timestamp t = e.getValue();
            c("setTimestamp(" + e.getKey() + ")", (ps, i, c) -> ps.setTimestamp(i, t));
            c("setTimestamp(" + e.getKey() + ",cal)", (ps, i, c) -> ps.setTimestamp(i, t, cal()));
            c("setObject(Timestamp " + e.getKey() + ")", (ps, i, c) -> ps.setObject(i, t));
            c("setObject(PGTimestamp " + e.getKey() + ")", (ps, i, c) -> { PGTimestamp p = new PGTimestamp(t.getTime()); p.setNanos(t.getNanos()); ps.setObject(i, p); });
            c("setObject(PGTimestamp " + e.getKey() + ",cal)", (ps, i, c) -> { PGTimestamp p = new PGTimestamp(t.getTime(), cal()); p.setNanos(t.getNanos()); ps.setObject(i, p); });
            c("setTimestamp(PGTimestamp " + e.getKey() + ",cal)", (ps, i, c) -> { PGTimestamp p = new PGTimestamp(t.getTime(), cal()); p.setNanos(t.getNanos()); ps.setTimestamp(i, p); });
        }
        c("setTimestamp(null)", (ps, i, c) -> ps.setTimestamp(i, null));
        c("setTimestamp(valueOf 2020-01-02 10:11:12,null)", (ps, i, c) -> ps.setTimestamp(i, timestamp("2020-01-02 10:11:12"), null));
        c("setObject(java.util.Date 1577960000123)", (ps, i, c) -> ps.setObject(i, new java.util.Date(1577960000123L)));
        c("setObject(Calendar 1577960000123)", (ps, i, c) -> { Calendar k = cal(); k.setTimeInMillis(1577960000123L); ps.setObject(i, k); });
        for (String v : new String[] {"2020-01-02", "0001-01-01", "0000-12-31", "-0043-03-15", "+10000-01-01", "1582-10-10", "9999-12-31", "+5874897-12-31", "-4712-01-01", "1970-01-01"})
            c("setObject(LocalDate " + v + ")", (ps, i, c) -> ps.setObject(i, LocalDate.parse(v)));
        c("setObject(LocalDate MAX)", (ps, i, c) -> ps.setObject(i, LocalDate.MAX));
        c("setObject(LocalDate MIN)", (ps, i, c) -> ps.setObject(i, LocalDate.MIN));
        for (String v : new String[] {"10:11:12", "00:00", "23:59:59.999999", "23:59:59.999999999", "23:59:59.9999995", "10:11:12.123456789", "10:11:12.1234564", "10:11:12.1234565", "12:00:00.5", "10:11:12.000000499"})
            c("setObject(LocalTime " + v + ")", (ps, i, c) -> ps.setObject(i, LocalTime.parse(v)));
        c("setObject(LocalTime MAX)", (ps, i, c) -> ps.setObject(i, LocalTime.MAX));
        c("setObject(LocalTime MIN)", (ps, i, c) -> ps.setObject(i, LocalTime.MIN));
        for (String v : new String[] {"2020-01-02T10:11:12", "2020-01-02T10:11:12.123456789", "2020-01-02T10:11:12.5", "2020-12-31T23:59:59.9999995", "0001-01-01T00:00:00", "-0043-03-15T12:00:00",
                "+10000-01-01T00:00:00", "2021-03-28T02:30:00", "1582-10-10T00:00:00", "+294276-12-31T23:59:59.999999", "-4712-01-01T00:00:00", "2020-01-02T00:00:00", "2020-01-02T10:11:12.000001"})
            c("setObject(LocalDateTime " + v + ")", (ps, i, c) -> ps.setObject(i, LocalDateTime.parse(v)));
        c("setObject(LocalDateTime MAX)", (ps, i, c) -> ps.setObject(i, LocalDateTime.MAX));
        c("setObject(LocalDateTime MIN)", (ps, i, c) -> ps.setObject(i, LocalDateTime.MIN));
        c("setObject(LocalDateTime MAX-1s)", (ps, i, c) -> ps.setObject(i, LocalDateTime.MAX.minusSeconds(1)));
        for (String v : new String[] {"2020-01-02T10:11:12Z", "2020-01-02T10:11:12.123456789+05:30", "2020-01-02T10:11:12-08:00", "2020-01-02T10:11:12+01:02:03", "2020-01-02T10:11:12+18:00",
                "2020-01-02T10:11:12-18:00", "0001-01-01T00:00:00Z", "-0043-03-15T12:00:00Z", "+10000-01-01T00:00:00Z", "2020-12-31T23:59:59.9999995Z", "2020-06-15T12:00:00+02:00", "1900-01-01T00:00:00Z"})
            c("setObject(OffsetDateTime " + v + ")", (ps, i, c) -> ps.setObject(i, OffsetDateTime.parse(v)));
        c("setObject(OffsetDateTime MAX)", (ps, i, c) -> ps.setObject(i, OffsetDateTime.MAX));
        c("setObject(OffsetDateTime MIN)", (ps, i, c) -> ps.setObject(i, OffsetDateTime.MIN));
        for (String v : new String[] {"10:11:12Z", "10:11:12.123456789+05:30", "23:59:59.9999995-08:00", "10:11:12+01:02:03", "00:00+18:00", "10:11:12-18:00"})
            c("setObject(OffsetTime " + v + ")", (ps, i, c) -> ps.setObject(i, OffsetTime.parse(v)));
        c("setObject(OffsetTime MAX)", (ps, i, c) -> ps.setObject(i, OffsetTime.MAX));
        c("setObject(OffsetTime MIN)", (ps, i, c) -> ps.setObject(i, OffsetTime.MIN));
        c("setObject(ZonedDateTime 2020-01-02T10:11:12+01:00[Europe/Warsaw])", (ps, i, c) -> ps.setObject(i, ZonedDateTime.parse("2020-01-02T10:11:12+01:00[Europe/Warsaw]")));
        c("setObject(Instant 2020-01-02T10:11:12Z)", (ps, i, c) -> ps.setObject(i, Instant.parse("2020-01-02T10:11:12Z")));
        c("setObject(Duration PT1H)", (ps, i, c) -> ps.setObject(i, Duration.ofHours(1)));
        family = "object";
        c("setObject(null)", (ps, i, c) -> ps.setObject(i, null));
        c("setObject(Boolean true)", (ps, i, c) -> ps.setObject(i, Boolean.TRUE));
        c("setObject(Byte 5)", (ps, i, c) -> ps.setObject(i, (byte) 5));
        c("setObject(Short 5)", (ps, i, c) -> ps.setObject(i, (short) 5));
        c("setObject(Integer 5)", (ps, i, c) -> ps.setObject(i, 5));
        c("setObject(Long 5)", (ps, i, c) -> ps.setObject(i, 5L));
        c("setObject(Float 1.5)", (ps, i, c) -> ps.setObject(i, 1.5f));
        c("setObject(Double 1.5)", (ps, i, c) -> ps.setObject(i, 1.5d));
        c("setObject(BigDecimal 1.50)", (ps, i, c) -> ps.setObject(i, new BigDecimal("1.50")));
        c("setObject(BigInteger 12345678901234567890)", (ps, i, c) -> ps.setObject(i, new BigInteger("12345678901234567890")));
        c("setObject(AtomicLong 7)", (ps, i, c) -> ps.setObject(i, new java.util.concurrent.atomic.AtomicLong(7)));
        c("setObject(String abc)", (ps, i, c) -> ps.setObject(i, "abc"));
        c("setObject(Character a)", (ps, i, c) -> ps.setObject(i, 'a'));
        c("setObject(char[] abc)", (ps, i, c) -> ps.setObject(i, new char[] {'a', 'b', 'c'}));
        c("setObject(StringBuilder abc)", (ps, i, c) -> ps.setObject(i, new StringBuilder("abc")));
        c("setObject(byte[] 616263)", (ps, i, c) -> ps.setObject(i, new byte[] {0x61, 0x62, 0x63}));
        c("setObject(UUID)", (ps, i, c) -> ps.setObject(i, UUID.fromString("a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11")));
        c("setObject(Enum RED)", (ps, i, c) -> ps.setObject(i, Color.RED));
        c("setObject(Object)", (ps, i, c) -> ps.setObject(i, new Object()));
        c("setObject(URL)", (ps, i, c) -> ps.setObject(i, new java.net.URL("http://example.com/")));
        c("setObject(InetAddress)", (ps, i, c) -> ps.setObject(i, java.net.InetAddress.getByAddress(new byte[] {10, 0, 0, 1})));
        c("setObject(InputStream)", (ps, i, c) -> ps.setObject(i, in("abc")));
        c("setObject(Reader)", (ps, i, c) -> ps.setObject(i, new StringReader("abc")));
        c("setObject(PGobject json)", (ps, i, c) -> ps.setObject(i, pg("json", "{\"a\":1}")));
        c("setObject(PGobject jsonb)", (ps, i, c) -> ps.setObject(i, pg("jsonb", "{\"a\":1}")));
        c("setObject(PGobject nosuchtype)", (ps, i, c) -> ps.setObject(i, pg("nosuchtype", "x")));
        c("setObject(PGobject type null)", (ps, i, c) -> ps.setObject(i, pg(null, "x")));
        c("setObject(PGobject json null value)", (ps, i, c) -> ps.setObject(i, pg("json", null)));
        c("setObject(PGobject mood)", (ps, i, c) -> ps.setObject(i, pg("mood", "ok")));
        c("setObject(PGobject s2.color)", (ps, i, c) -> ps.setObject(i, pg("s2.color", "red")));
        c("setObject(PGobject int4)", (ps, i, c) -> ps.setObject(i, pg("int4", "5")));
        c("setObject(PGobject text)", (ps, i, c) -> ps.setObject(i, pg("text", "abc")));
        c("setObject(PGobject inet)", (ps, i, c) -> ps.setObject(i, pg("inet", "10.0.0.1")));
        c("setObject(PGobject INT4)", (ps, i, c) -> ps.setObject(i, pg("INT4", "5")));
        c("setObject(PGobject integer)", (ps, i, c) -> ps.setObject(i, pg("integer", "5")));
        c("setObject(PGInterval)", (ps, i, c) -> ps.setObject(i, new PGInterval(1, 2, 3, 4, 5, 6.789)));
        c("setObject(PGmoney 12.34)", (ps, i, c) -> ps.setObject(i, new PGmoney(12.34)));
        c("setObject(PGmoney -12.34)", (ps, i, c) -> ps.setObject(i, new PGmoney(-12.34)));
        c("setObject(PGpoint)", (ps, i, c) -> ps.setObject(i, new PGpoint(1, 2.5)));
        c("setObject(PGbox)", (ps, i, c) -> ps.setObject(i, new PGbox(1, 2, 3, 4)));
        c("setObject(PGcircle)", (ps, i, c) -> ps.setObject(i, new PGcircle(1, 2, 3)));
        c("setObject(PGline)", (ps, i, c) -> ps.setObject(i, new PGline(1, 2, 3)));
        c("setObject(PGlseg)", (ps, i, c) -> ps.setObject(i, new PGlseg(1, 2, 3, 4)));
        c("setObject(PGpath)", (ps, i, c) -> ps.setObject(i, new PGpath(new PGpoint[] {new PGpoint(1, 2), new PGpoint(3, 4)}, true)));
        c("setObject(PGpolygon)", (ps, i, c) -> ps.setObject(i, new PGpolygon(new PGpoint[] {new PGpoint(1, 2), new PGpoint(3, 4)})));
        c("setObject(Map hstore)", (ps, i, c) -> { Map<String, String> m = new LinkedHashMap<>(); m.put("a", "1"); m.put("b", null); m.put("k \"q\"", "v\\x"); ps.setObject(i, m); });
        c("setURL", (ps, i, c) -> ps.setURL(i, new java.net.URL("http://example.com/")));
        c("setRef(null)", (ps, i, c) -> ps.setRef(i, null));
        c("setRowId(null)", (ps, i, c) -> ps.setRowId(i, null));
        c("setSQLXML", (ps, i, c) -> { SQLXML x = c.createSQLXML(); x.setString("<a>1</a>"); ps.setSQLXML(i, x); });
        c("setSQLXML(null)", (ps, i, c) -> ps.setSQLXML(i, null));
        c("setObject(SQLXML)", (ps, i, c) -> { SQLXML x = c.createSQLXML(); x.setString("<a>1</a>"); ps.setObject(i, x); });
        c("setObject(Integer 5, JDBCType.INTEGER)", (ps, i, c) -> ps.setObject(i, 5, JDBCType.INTEGER));
        c("setObject(Integer 5, JDBCType.INTEGER, 0)", (ps, i, c) -> ps.setObject(i, 5, JDBCType.INTEGER, 0));
                c("setClob(Reader)", (ps, i, c) -> ps.setClob(i, new StringReader("abc")));
        c("setNClob(Reader)", (ps, i, c) -> ps.setNClob(i, new StringReader("abc")));
        family = "string";
        for (String m : new String[] {"Ascii", "Unicode", "Binary"}) {
            for (String len : new String[] {"none", "int 3", "int 2", "int 5", "int 0", "int -1", "long 3", "long 2", "long 5"}) {
                if (m.equals("Unicode") && !len.startsWith("int")) continue;
                c("set" + m + "Stream(abc," + len + ")", (ps, i, c) -> stream(ps, i, m, len, in("abc")));
            }
            c("set" + m + "Stream(null,int 0)", (ps, i, c) -> stream(ps, i, m, "int 0", null));
            c("set" + m + "Stream(null,none)", (ps, i, c) -> stream(ps, i, m, "none", null));
            c("set" + m + "Stream(a\\u00e9\\u20ac😀,none)", (ps, i, c) -> stream(ps, i, m, "none", in("aé€😀")));
            c("set" + m + "Stream(bytes ff00,none)", (ps, i, c) -> stream(ps, i, m, "none", new ByteArrayInputStream(new byte[] {(byte) 0xff, 0, 0x41})));
        }
        for (String m : new String[] {"Character", "NCharacter"}) {
            for (String len : new String[] {"none", "int 3", "int 2", "int 5", "int 0", "int -1", "long 3", "long 2", "long 5"}) {
                if (m.equals("NCharacter") && len.startsWith("int")) continue;
                c("set" + m + "Stream(abc," + len + ")", (ps, i, c) -> reader(ps, i, m, len, new StringReader("abc")));
            }
            c("set" + m + "Stream(null,none)", (ps, i, c) -> reader(ps, i, m, "none", null));
            c("set" + m + "Stream(a😀b,none)", (ps, i, c) -> reader(ps, i, m, "none", new StringReader("a😀b")));
        }
        family = "array";
        c("setArray(int4 {1,2,3})", (ps, i, c) -> ps.setArray(i, c.createArrayOf("int4", new Integer[] {1, 2, 3})));
        c("setArray(int4 {1,null})", (ps, i, c) -> ps.setArray(i, c.createArrayOf("int4", new Integer[] {1, null})));
        c("setArray(int4 {})", (ps, i, c) -> ps.setArray(i, c.createArrayOf("int4", new Integer[] {})));
        c("setArray(int4 {{1,2},{3,4}})", (ps, i, c) -> ps.setArray(i, c.createArrayOf("int4", new Integer[][] {{1, 2}, {3, 4}})));
        c("setArray(text special)", (ps, i, c) -> ps.setArray(i, c.createArrayOf("text", new String[] {"a b", "c,d", "e\"f", "g\\h", null, "NULL", "", "{x}", "null"})));
        c("setArray(varchar {a})", (ps, i, c) -> ps.setArray(i, c.createArrayOf("varchar", new String[] {"a"})));
        c("setArray(numeric)", (ps, i, c) -> ps.setArray(i, c.createArrayOf("numeric", new BigDecimal[] {new BigDecimal("1.50"), null})));
        c("setArray(float8)", (ps, i, c) -> ps.setArray(i, c.createArrayOf("float8", new Double[] {1.5, Double.NaN, Double.POSITIVE_INFINITY})));
        c("setArray(bool)", (ps, i, c) -> ps.setArray(i, c.createArrayOf("bool", new Boolean[] {true, false, null})));
        c("setArray(bytea)", (ps, i, c) -> ps.setArray(i, c.createArrayOf("bytea", new byte[][] {{0, (byte) 0xff}, {}})));
        c("setArray(null)", (ps, i, c) -> ps.setArray(i, null));
        c("setObject(int[] {1,2})", (ps, i, c) -> ps.setObject(i, new int[] {1, 2}));
        c("setObject(Integer[] {1,null})", (ps, i, c) -> ps.setObject(i, new Integer[] {1, null}));
        c("setObject(long[] {1,2})", (ps, i, c) -> ps.setObject(i, new long[] {1, 2}));
        c("setObject(short[] {1,2})", (ps, i, c) -> ps.setObject(i, new short[] {1, 2}));
        c("setObject(float[] {1.5})", (ps, i, c) -> ps.setObject(i, new float[] {1.5f}));
        c("setObject(double[] {1.5,NaN})", (ps, i, c) -> ps.setObject(i, new double[] {1.5, Double.NaN}));
        c("setObject(boolean[] {true,false})", (ps, i, c) -> ps.setObject(i, new boolean[] {true, false}));
        c("setObject(String[] {a,null,\"b c\"})", (ps, i, c) -> ps.setObject(i, new String[] {"a", null, "b c"}));
        c("setObject(String[][] {{a},{b}})", (ps, i, c) -> ps.setObject(i, new String[][] {{"a"}, {"b"}}));
        c("setObject(byte[][] {{00,ff}})", (ps, i, c) -> ps.setObject(i, new byte[][] {{0, (byte) 0xff}}));
        c("setObject(Object[] {1,a})", (ps, i, c) -> ps.setObject(i, new Object[] {1, "a"}));
        c("setObject(BigDecimal[] {1.50})", (ps, i, c) -> ps.setObject(i, new BigDecimal[] {new BigDecimal("1.50")}));
        c("setObject(int[][] {{1,2},{3,4}})", (ps, i, c) -> ps.setObject(i, new int[][] {{1, 2}, {3, 4}}));
        c("setObject(UUID[])", (ps, i, c) -> ps.setObject(i, new UUID[] {UUID.fromString("a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11")}));
        c("setObject(Long[] {1})", (ps, i, c) -> ps.setObject(i, new Long[] {1L}));
        c("setObject(Double[] {1.5})", (ps, i, c) -> ps.setObject(i, new Double[] {1.5}));
        c("setObject(Short[] {1})", (ps, i, c) -> ps.setObject(i, new Short[] {1}));
        c("setObject(Float[] {1.5})", (ps, i, c) -> ps.setObject(i, new Float[] {1.5f}));
        c("setObject(Boolean[] {true})", (ps, i, c) -> ps.setObject(i, new Boolean[] {true}));
        c("setObject(java.sql.Array int4)", (ps, i, c) -> ps.setObject(i, c.createArrayOf("int4", new Integer[] {1, 2})));
        // setObject with a target type
        Map<String, Object> src = new LinkedHashMap<>();
        src.put("Boolean true", true); src.put("Boolean false", false); src.put("Byte 5", (byte) 5); src.put("Short 5", (short) 5); src.put("Integer 5", 5);
        src.put("Integer 0", 0); src.put("Integer 2", 2); src.put("Long 5", 5L); src.put("Long 9223372036854775807", Long.MAX_VALUE); src.put("Float 1.5", 1.5f);
        src.put("Double 1.5", 1.5d); src.put("Double 3.99", 3.99d); src.put("Double NaN", Double.NaN); src.put("Double 1.0E300", 1e300);
        src.put("BigDecimal 1.50", new BigDecimal("1.50")); src.put("BigDecimal 1.555", new BigDecimal("1.555")); src.put("BigDecimal 1E+30", new BigDecimal("1E+30"));
        src.put("BigInteger 5", BigInteger.valueOf(5)); src.put("String abc", "abc"); src.put("String 5", "5"); src.put("String 1.5", "1.5"); src.put("String true", "true");
        src.put("String t", "t"); src.put("String 1", "1"); src.put("String 0", "0"); src.put("String ", ""); src.put("String 2020-01-02", "2020-01-02");
        src.put("String 10:11:12", "10:11:12"); src.put("String 2020-01-02 10:11:12.123456", "2020-01-02 10:11:12.123456");
        src.put("String 2020-01-02 10:11:12+05:30", "2020-01-02 10:11:12+05:30"); src.put("String 99999999999", "99999999999"); src.put("String  5 ", " 5 ");
        src.put("Character a", 'a'); src.put("Character 1", '1'); src.put("byte[] 616263", new byte[] {0x61, 0x62, 0x63});
        src.put("Date valueOf 2020-01-02", date("2020-01-02")); src.put("Time valueOf 10:11:12", time("10:11:12"));
        src.put("Timestamp valueOf 2020-01-02 10:11:12.123456789", timestamp("2020-01-02 10:11:12.123456789"));
        src.put("java.util.Date 1577960000123", new java.util.Date(1577960000123L));
        src.put("LocalDate 2020-01-02", LocalDate.parse("2020-01-02")); src.put("LocalTime 10:11:12.123456789", LocalTime.parse("10:11:12.123456789"));
        src.put("LocalDateTime 2020-01-02T10:11:12.123456789", LocalDateTime.parse("2020-01-02T10:11:12.123456789"));
        src.put("OffsetDateTime 2020-01-02T10:11:12.123456789+05:30", OffsetDateTime.parse("2020-01-02T10:11:12.123456789+05:30"));
        src.put("OffsetTime 10:11:12.123456789+05:30", OffsetTime.parse("10:11:12.123456789+05:30"));
        src.put("ZonedDateTime 2020-01-02T10:11:12+01:00[Europe/Warsaw]", ZonedDateTime.parse("2020-01-02T10:11:12+01:00[Europe/Warsaw]"));
        src.put("Instant 2020-01-02T10:11:12Z", Instant.parse("2020-01-02T10:11:12Z"));
        src.put("UUID", UUID.fromString("a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11")); src.put("PGobject json", pg("json", "{\"a\":1}"));
        src.put("PGInterval", new PGInterval(1, 2, 3, 4, 5, 6.789)); src.put("PGpoint", new PGpoint(1, 2.5)); src.put("int[] {1,2}", new int[] {1, 2});
        src.put("String[] {a}", new String[] {"a"}); src.put("Object", new Object() { @Override public String toString() { return "anObject"; } });
        src.put("PGTimestamp 1577960000123 cal", new PGTimestamp(1577960000123L, cal())); src.put("PGTime 43200123 cal", new PGTime(43200123L, cal()));
        src.put("Calendar 1577960000123", calAt(1577960000123L)); src.put("AtomicLong 7", new java.util.concurrent.atomic.AtomicLong(7));
        src.put("StringBuilder 5", new StringBuilder("5")); src.put("Enum RED", Color.RED);
        Map<String, String> fam = new HashMap<>();
        for (String t : "BIT BOOLEAN".split(" ")) fam.put(t, "bool");
        for (String t : "TINYINT SMALLINT INTEGER BIGINT REAL FLOAT DOUBLE DECIMAL NUMERIC".split(" ")) fam.put(t, "number");
        for (String t : "CHAR VARCHAR LONGVARCHAR NCHAR NVARCHAR LONGNVARCHAR".split(" ")) fam.put(t, "string");
        for (String t : "DATE TIME TIMESTAMP TIME_WITH_TIMEZONE TIMESTAMP_WITH_TIMEZONE".split(" ")) fam.put(t, "temporal");
        for (String t : "BINARY VARBINARY LONGVARBINARY".split(" ")) fam.put(t, "bytes");
        fam.put("ARRAY", "array");
        for (Field f : Types.class.getFields()) {
            int t = f.getInt(null);
            family = fam.getOrDefault(f.getName(), "object");
            for (Map.Entry<String, Object> e : src.entrySet()) {
                Object o = e.getValue();
                c("setObject(" + e.getKey() + ", Types." + f.getName() + ")", (ps, i, c) -> ps.setObject(i, o, t));
            }
            c("setObject(null, Types." + f.getName() + ")", (ps, i, c) -> ps.setObject(i, null, t));
        }
        family = "number";
        for (String n : new String[] {"NUMERIC", "DECIMAL", "INTEGER", "DOUBLE", "VARCHAR", "REAL", "BIGINT"}) {
            int t = Types.class.getField(n).getInt(null);
            for (int scale : new int[] {0, 2, -1, 5}) {
                for (String k : new String[] {"BigDecimal 1.555", "Double 1.5", "Double 3.99", "String 1.5", "Integer 5", "Float 1.5", "Long 5", "BigDecimal 1E+30", "Boolean true", "String abc", "BigInteger 5"}) {
                    Object o = src.get(k);
                    c("setObject(" + k + ", Types." + n + ", scale " + scale + ")", (ps, i, c) -> ps.setObject(i, o, t, scale));
                }
            }
        }
        c("setObject(Integer 5, 12345)", (ps, i, c) -> ps.setObject(i, 5, 12345));
    }

    private static void stream(SetterCalls ps, int i, String m, String len, InputStream in)
            throws SQLException {
        String[] l = len.split(" ");
        if (m.equals("Ascii")) {
            if (l[0].equals("none")) {
                ps.setAsciiStream(i, in);
            } else if (l[0].equals("int")) {
                ps.setAsciiStream(i, in, Integer.parseInt(l[1]));
            } else {
                ps.setAsciiStream(i, in, Long.parseLong(l[1]));
            }
        } else if (m.equals("Unicode")) {
            ps.setUnicodeStream(i, in, l[0].equals("none") ? 3 : Integer.parseInt(l[1]));
        } else if (l[0].equals("none")) {
            ps.setBinaryStream(i, in);
        } else if (l[0].equals("int")) {
            ps.setBinaryStream(i, in, Integer.parseInt(l[1]));
        } else {
            ps.setBinaryStream(i, in, Long.parseLong(l[1]));
        }
    }

    private static void reader(SetterCalls ps, int i, String m, String len, java.io.Reader r)
            throws SQLException {
        String[] l = len.split(" ");
        if (m.equals("Character")) {
            if (l[0].equals("none")) {
                ps.setCharacterStream(i, r);
            } else if (l[0].equals("int")) {
                ps.setCharacterStream(i, r, Integer.parseInt(l[1]));
            } else {
                ps.setCharacterStream(i, r, Long.parseLong(l[1]));
            }
        } else if (l[0].equals("none")) {
            ps.setNCharacterStream(i, r);
        } else {
            ps.setNCharacterStream(i, r, Long.parseLong(l[1]));
        }
    }
}
