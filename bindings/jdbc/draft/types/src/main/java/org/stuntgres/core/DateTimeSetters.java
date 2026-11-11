package org.stuntgres.core;

import java.sql.Date;
import java.sql.Time;
import java.sql.Timestamp;
import java.time.Duration;
import java.time.LocalDate;
import java.time.LocalDateTime;
import java.time.LocalTime;
import java.time.OffsetDateTime;
import java.time.OffsetTime;
import java.time.ZoneOffset;
import java.time.temporal.ChronoField;
import java.util.Calendar;
import java.util.GregorianCalendar;
import java.util.TimeZone;
import org.stuntgres.util.PGTime;
import org.stuntgres.util.PGTimestamp;

/**
 * The conversions behind PreparedStatement.setDate, setTime and setTimestamp and behind
 * setObject for the java.time classes (design sections 8.4 and 8.5). A java.sql value is
 * written in the zone of the Calendar argument or, without one, in the default zone, and
 * carries that zone's offset; its type is left to the server, so the same text fits
 * columns with and without time zone.
 */
public final class DateTimeSetters {
    private static final LocalDate FIRST_DATE = LocalDate.of(4713, 1, 1).with(ChronoField.ERA, 0);
    private static final LocalDateTime FIRST_TIMESTAMP = FIRST_DATE.atStartOfDay();
    private static final LocalDateTime LAST_TIMESTAMP =
            LocalDateTime.MAX.minus(Duration.ofMillis(500));
    private static final LocalTime LAST_TIME = LocalTime.MAX.minusNanos(500);

    private DateTimeSetters() {
    }

    private static Calendar at(long millis, Calendar calendar, ConversionContext context) {
        TimeZone zone = calendar == null ? context.defaultZone() : calendar.getTimeZone();
        Calendar fields = new GregorianCalendar(zone);
        fields.setTimeInMillis(millis);
        return fields;
    }

    private static void appendDate(StringBuilder out, Calendar fields) {
        DateTimeText.appendDate(out, fields.get(Calendar.YEAR), fields.get(Calendar.MONTH) + 1,
                fields.get(Calendar.DAY_OF_MONTH));
    }

    private static void appendTime(StringBuilder out, Calendar fields, int nanos) {
        DateTimeText.appendTime(out, fields.get(Calendar.HOUR_OF_DAY), fields.get(Calendar.MINUTE),
                fields.get(Calendar.SECOND), nanos);
    }

    private static void appendOffset(StringBuilder out, Calendar fields) {
        DateTimeText.appendOffset(out,
                (fields.get(Calendar.ZONE_OFFSET) + fields.get(Calendar.DST_OFFSET)) / 1000);
    }

    private static boolean beforeChrist(Calendar fields) {
        return fields.get(Calendar.ERA) == GregorianCalendar.BC;
    }

    /**
     * Converts a java.sql.Date parameter.
     *
     * @param x the value, or null
     * @param calendar the calendar of the call, or null for the default zone
     * @param context the conversion context
     * @return text such as "2020-01-02 +01" without a type; "infinity" and "-infinity" for
     *         the two special values; SQL NULL of type date for null
     */
    public static ParamValue setDate(Date x, Calendar calendar, ConversionContext context) {
        if (x == null) {
            return new ParamValue(Oid.DATE, null);
        }
        String infinity = infinity(x.getTime());
        if (infinity != null) {
            return new ParamValue(Oid.UNSPECIFIED, infinity);
        }
        Calendar fields = at(x.getTime(), calendar, context);
        StringBuilder out = new StringBuilder();
        appendDate(out, fields);
        out.append(beforeChrist(fields) ? " BC " : " ");
        appendOffset(out, fields);
        return new ParamValue(Oid.UNSPECIFIED, out.toString());
    }

    private static String infinity(long millis) {
        return millis == DateTimeText.POSITIVE_INFINITY ? "infinity"
                : millis == DateTimeText.NEGATIVE_INFINITY ? "-infinity" : null;
    }

    /**
     * Converts a java.sql.Time parameter.
     *
     * @param x the value, or null; a PGTime gives the type time, or timetz when it has a
     *        calendar, which is then used instead of the argument
     * @param calendar the calendar of the call, or null for the default zone
     * @param context the conversion context
     * @return text such as "10:11:12.123+01", without a type unless x is a PGTime; SQL
     *         NULL without a type for null
     */
    public static ParamValue setTime(Time x, Calendar calendar, ConversionContext context) {
        if (x == null) {
            return new ParamValue(Oid.UNSPECIFIED, null);
        }
        int oid = Oid.UNSPECIFIED;
        Calendar use = calendar;
        if (x instanceof PGTime) {
            Calendar own = ((PGTime) x).getCalendar();
            oid = own == null ? Oid.TIME : Oid.TIMETZ;
            use = own == null ? calendar : own;
        }
        Calendar fields = at(x.getTime(), use, context);
        StringBuilder out = new StringBuilder();
        appendTime(out, fields, fields.get(Calendar.MILLISECOND) * 1000000);
        appendOffset(out, fields);
        return new ParamValue(oid, out.toString());
    }

    /**
     * Converts a java.sql.Timestamp parameter. The nanoseconds are rounded to
     * microseconds.
     *
     * @param x the value, or null; a PGTimestamp gives the type timestamp, or timestamptz
     *        when it has a calendar, which is then used instead of the argument
     * @param calendar the calendar of the call, or null for the default zone
     * @param context the conversion context
     * @return text such as "2020-01-02 10:11:12.123457+01", without a type unless x is a
     *         PGTimestamp; "infinity" and "-infinity" for the two special values; SQL NULL
     *         without a type for null
     */
    public static ParamValue setTimestamp(Timestamp x, Calendar calendar,
            ConversionContext context) {
        if (x == null) {
            return new ParamValue(Oid.UNSPECIFIED, null);
        }
        int oid = Oid.UNSPECIFIED;
        Calendar use = calendar;
        if (x instanceof PGTimestamp) {
            Calendar own = ((PGTimestamp) x).getCalendar();
            oid = own == null ? Oid.TIMESTAMP : Oid.TIMESTAMPTZ;
            use = own == null ? calendar : own;
        }
        String infinity = infinity(x.getTime());
        if (infinity != null) {
            return new ParamValue(oid, infinity);
        }
        long millis = x.getTime();
        int nanos = x.getNanos();
        if (nanos >= 999999500) {
            nanos = 0;
            millis++;
        } else if (nanos % 1000 > 499) {
            nanos += 1000 - nanos % 1000;
        }
        Calendar fields = at(millis, use, context);
        StringBuilder out = new StringBuilder();
        appendDate(out, fields);
        out.append(' ');
        appendTime(out, fields, nanos);
        appendOffset(out, fields);
        out.append(beforeChrist(fields) ? " BC" : "");
        return new ParamValue(oid, out.toString());
    }

    private static void appendDate(StringBuilder out, LocalDate date) {
        DateTimeText.appendDate(out, date.get(ChronoField.YEAR_OF_ERA), date.getMonthValue(),
                date.getDayOfMonth());
    }

    private static void appendTime(StringBuilder out, LocalTime time) {
        DateTimeText.appendTime(out, time.getHour(), time.getMinute(), time.getSecond(),
                time.getNano());
    }

    private static String era(LocalDate date) {
        return date.get(ChronoField.ERA) == 0 ? " BC" : "";
    }

    /**
     * Converts a LocalDate parameter.
     *
     * @param x the value
     * @return a date; "infinity" for LocalDate.MAX, "-infinity" for a date before 4713 BC
     */
    public static ParamValue setLocalDate(LocalDate x) {
        if (LocalDate.MAX.equals(x)) {
            return new ParamValue(Oid.DATE, "infinity");
        }
        if (x.isBefore(FIRST_DATE)) {
            return new ParamValue(Oid.DATE, "-infinity");
        }
        StringBuilder out = new StringBuilder();
        appendDate(out, x);
        return new ParamValue(Oid.DATE, out.append(era(x)).toString());
    }

    /**
     * Converts a LocalTime parameter, rounded to microseconds.
     *
     * @param x the value
     * @return a time; "24:00:00" for a value in the last half microsecond of the day
     */
    public static ParamValue setLocalTime(LocalTime x) {
        if (x.isAfter(LAST_TIME)) {
            return new ParamValue(Oid.TIME, "24:00:00");
        }
        StringBuilder out = new StringBuilder();
        appendTime(out, x.getNano() % 1000 > 499 ? x.plusNanos(1000) : x);
        return new ParamValue(Oid.TIME, out.toString());
    }

    /**
     * Converts an OffsetTime parameter. The fraction is cut, not rounded, to microseconds.
     *
     * @param x the value
     * @return a timetz; "24:00:00" with the offset for a value in the last half
     *         microsecond of the day
     */
    public static ParamValue setOffsetTime(OffsetTime x) {
        StringBuilder out = new StringBuilder();
        if (x.toLocalTime().isAfter(LAST_TIME)) {
            out.append("24:00:00");
        } else {
            appendTime(out, x.toLocalTime());
        }
        DateTimeText.appendOffset(out, x.getOffset().getTotalSeconds());
        return new ParamValue(Oid.TIMETZ, out.toString());
    }

    /**
     * Converts a LocalDateTime parameter, rounded to microseconds.
     *
     * @param x the value
     * @return a timestamp; "infinity" for a value in the last half second before
     *         LocalDateTime.MAX, "-infinity" for a value before 4713 BC
     */
    public static ParamValue setLocalDateTime(LocalDateTime x) {
        if (x.isAfter(LAST_TIMESTAMP)) {
            return new ParamValue(Oid.TIMESTAMP, "infinity");
        }
        if (x.isBefore(FIRST_TIMESTAMP)) {
            return new ParamValue(Oid.TIMESTAMP, "-infinity");
        }
        LocalDateTime rounded = x.getNano() % 1000 > 499 ? x.plusNanos(1000) : x;
        StringBuilder out = new StringBuilder();
        appendDate(out, rounded.toLocalDate());
        out.append(' ');
        appendTime(out, rounded.toLocalTime());
        return new ParamValue(Oid.TIMESTAMP, out.append(era(rounded.toLocalDate())).toString());
    }

    /**
     * Converts an OffsetDateTime parameter, rounded to microseconds.
     *
     * @param x the value
     * @return a timestamptz with the offset of the value; "infinity" for a value in the
     *         last half second before OffsetDateTime.MAX, "-infinity" for a value before
     *         4713 BC
     */
    public static ParamValue setOffsetDateTime(OffsetDateTime x) {
        if (x.isAfter(OffsetDateTime.MAX.minus(Duration.ofMillis(500)))) {
            return new ParamValue(Oid.TIMESTAMPTZ, "infinity");
        }
        if (x.isBefore(FIRST_TIMESTAMP.atOffset(ZoneOffset.UTC))) {
            return new ParamValue(Oid.TIMESTAMPTZ, "-infinity");
        }
        OffsetDateTime rounded = x.getNano() % 1000 > 499 ? x.plusNanos(1000) : x;
        StringBuilder out = new StringBuilder();
        appendDate(out, rounded.toLocalDate());
        out.append(' ');
        appendTime(out, rounded.toLocalTime());
        DateTimeText.appendOffset(out, rounded.getOffset().getTotalSeconds());
        return new ParamValue(Oid.TIMESTAMPTZ, out.append(era(rounded.toLocalDate())).toString());
    }
}
