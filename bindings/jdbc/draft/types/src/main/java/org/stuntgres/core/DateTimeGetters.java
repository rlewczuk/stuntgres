package org.stuntgres.core;

import java.sql.Date;
import java.sql.SQLException;
import java.sql.Time;
import java.sql.Timestamp;
import java.util.Calendar;
import java.util.GregorianCalendar;
import java.util.TimeZone;

/**
 * The conversions behind ResultSet.getDate, getTime and getTimestamp, with and without a
 * Calendar (design section 7.5). The value is the column text and is never null. A
 * Calendar argument only gives its time zone; it is not changed.
 */
public final class DateTimeGetters {
    private static final long DAY = 86400000L;

    private DateTimeGetters() {
    }

    private static TimeZone zone(Calendar calendar, ConversionContext context) {
        return calendar == null ? context.defaultZone() : calendar.getTimeZone();
    }

    private static boolean fixedOffset(TimeZone zone) {
        return zone.getID().startsWith("GMT") || zone.getID().startsWith("UTC");
    }

    /**
     * Reads a column as java.sql.Date: the start of the day in the zone of the calendar.
     *
     * @param value the column text
     * @param calendar the calendar of the call, or null for the default zone
     * @param context the conversion context
     * @return the date; "infinity" and "-infinity" give the two special values
     * @throws SQLException with SQLState 22007 when the text is not a date or timestamp
     */
    public static Date getDate(String value, Calendar calendar, ConversionContext context)
            throws SQLException {
        if (value.equals("infinity")) {
            return new Date(DateTimeText.POSITIVE_INFINITY);
        }
        if (value.equals("-infinity")) {
            return new Date(DateTimeText.NEGATIVE_INFINITY);
        }
        TimeZone zone = zone(calendar, context);
        byte[] bytes = value.getBytes(java.nio.charset.StandardCharsets.UTF_8);
        if (bytes.length > 13) {
            DateTimeText fields = DateTimeText.parse(value);
            return startOfDay(fields.millis(zone, true), zone);
        }
        // Short text is read as year-month-day without any check of the characters.
        int length = bytes.length;
        boolean beforeChrist = length >= 2 && bytes[length - 2] == 'B' && bytes[length - 1] == 'C';
        if (beforeChrist) {
            length -= 3;
        }
        int[] parts = new int[3];
        int i = 0;
        for (int part = 0; part < 3; part++) {
            while (part == 2 ? i < length : i < bytes.length && bytes[i] != '-') {
                parts[part] = parts[part] * 10 + (bytes[i++] - '0');
            }
            if (part < 2 && i++ >= bytes.length) {
                throw ConversionErrors.badDateTime("a date", value);
            }
        }
        Calendar day = calendar == null ? new GregorianCalendar(zone) : (Calendar) calendar.clone();
        day.clear();
        day.set(Calendar.YEAR, parts[0]);
        day.set(Calendar.DAY_OF_MONTH, parts[2]);
        day.set(Calendar.MONTH, parts[1] - 1);
        day.set(Calendar.ERA, beforeChrist ? GregorianCalendar.BC : GregorianCalendar.AD);
        return new Date(day.getTimeInMillis());
    }

    private static Date startOfDay(long millis, TimeZone zone) {
        if (millis <= DateTimeText.NEGATIVE_INFINITY || millis >= DateTimeText.POSITIVE_INFINITY) {
            return new Date(millis);
        }
        if (fixedOffset(zone)) {
            int offset = zone.getRawOffset();
            return new Date(Math.floorDiv(millis + offset, DAY) * DAY - offset);
        }
        Calendar calendar = new GregorianCalendar(zone);
        calendar.setTimeInMillis(millis);
        calendar.set(Calendar.HOUR_OF_DAY, 0);
        calendar.set(Calendar.MINUTE, 0);
        calendar.set(Calendar.SECOND, 0);
        calendar.set(Calendar.MILLISECOND, 0);
        return new Date(calendar.getTimeInMillis());
    }

    /**
     * Reads a column as java.sql.Time: the time of day on 1970-01-01.
     *
     * @param value the column text
     * @param calendar the calendar of the call, or null for the default zone
     * @param context the conversion context
     * @return the time with milliseconds
     * @throws SQLException with SQLState 22007 when the text is not a time or timestamp
     */
    public static Time getTime(String value, Calendar calendar, ConversionContext context)
            throws SQLException {
        DateTimeText fields = DateTimeText.parse(value);
        TimeZone zone = fields.zone(zone(calendar, context));
        long millis = fields.millis(zone, !fields.hasOffset) + fields.nanos / 1000000;
        if (fields.hasOffset || (fields.year == 1970 && !fields.beforeChrist)) {
            return new Time(millis);
        }
        if (fixedOffset(zone)) {
            int offset = zone.getRawOffset();
            return new Time(Math.floorMod(millis + offset, DAY) - offset);
        }
        Calendar day = new GregorianCalendar(zone);
        day.setTimeInMillis(millis);
        day.set(Calendar.ERA, GregorianCalendar.AD);
        day.set(Calendar.YEAR, 1970);
        day.set(Calendar.MONTH, Calendar.JANUARY);
        day.set(Calendar.DAY_OF_MONTH, 1);
        return new Time(day.getTimeInMillis());
    }

    /**
     * Reads a column as java.sql.Timestamp.
     *
     * @param value the column text
     * @param oid the type OID of the column
     * @param calendar the calendar of the call, or null for the default zone
     * @param context the conversion context
     * @return the timestamp with nanoseconds; "infinity" and "-infinity" give the two
     *         special values; a time or timetz column gives its time on 1970-01-01
     * @throws SQLException with SQLState 22007 when the text is not a date, time or
     *         timestamp
     */
    public static Timestamp getTimestamp(String value, int oid, Calendar calendar,
            ConversionContext context) throws SQLException {
        if (value.isEmpty()) {
            throw ConversionErrors.badDateTime("a timestamp", value);
        }
        if (value.equals("infinity")) {
            return new Timestamp(DateTimeText.POSITIVE_INFINITY);
        }
        if (value.equals("-infinity")) {
            return new Timestamp(DateTimeText.NEGATIVE_INFINITY);
        }
        DateTimeText fields = DateTimeText.parse(value);
        long millis = oid == Oid.TIME || oid == Oid.TIMETZ
                ? getTime(value, calendar, context).getTime()
                : fields.millis(zone(calendar, context), true);
        Timestamp timestamp = new Timestamp(millis);
        timestamp.setNanos(fields.nanos);
        return timestamp;
    }
}
