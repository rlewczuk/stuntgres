package org.stuntgres.core;

import java.sql.SQLException;
import java.time.DateTimeException;
import java.time.ZoneOffset;
import java.util.Calendar;
import java.util.GregorianCalendar;
import java.util.SimpleTimeZone;
import java.util.TimeZone;

/**
 * The fields of a date, time or timestamp in the server's ISO text form, for example
 * "2020-01-02 10:11:12.5+05:30" or "0044-03-15 BC" (design section 6.5).
 */
public final class DateTimeText {
    /** The milliseconds of a java.sql.Date or Timestamp that stand for "infinity". */
    public static final long POSITIVE_INFINITY = 9223372036825200000L;
    /** The milliseconds of a java.sql.Date or Timestamp that stand for "-infinity". */
    public static final long NEGATIVE_INFINITY = -9223372036832400000L;
    /** A second value of milliseconds that is sent as "infinity". */
    public static final long POSITIVE_SMALLER_INFINITY = 185543533774800000L;

    boolean hasDate;
    boolean hasTime;
    boolean hasOffset;
    boolean beforeChrist;
    int year = 1970;
    int month = 1;
    int day = 1;
    int hour;
    int minute;
    int second;
    int nanos;
    ZoneOffset offset = ZoneOffset.UTC;

    private final String text;
    private int position;

    private DateTimeText(String text) {
        this.text = text;
    }

    /**
     * Parses date, time and timestamp text: an optional date "y-m-d", an optional time
     * "h:m:s" with an optional fraction, an optional offset "+h", "+h:m" or "+h:m:s" and,
     * after a date, an optional era "AD" or "BC"; white space may separate the parts.
     * The numbers are not range-checked.
     *
     * @param text the column text
     * @return the fields
     * @throws SQLException with SQLState 22007 when the text has another form
     */
    static DateTimeText parse(String text) throws SQLException {
        DateTimeText fields = new DateTimeText(text);
        try {
            fields.read();
        } catch (NumberFormatException | DateTimeException e) {
            throw ConversionErrors.badDateTime("a date, time or timestamp", text);
        }
        return fields;
    }

    private void read() {
        skipSpace();
        int end = digitsEnd(position);
        if (charAt(end) == '-') {
            hasDate = true;
            year = number(end);
            position = end + 1;
            month = number(digitsEnd(position));
            if (position < text.length()) {
                expect('-');
            }
            position++;
            day = number(digitsEnd(position));
            skipSpace();
        }
        if (isDigit(charAt(position))) {
            hasTime = true;
            hour = number(digitsEnd(position));
            if (position < text.length()) {
                expect(':');
            }
            position++;
            minute = number(digitsEnd(position));
            if (position < text.length()) {
                expect(':');
            }
            position++;
            second = number(digitsEnd(position));
            if (charAt(position) == '.') {
                int start = ++position;
                nanos = number(digitsEnd(position));
                for (int digits = position - start; digits < 9; digits++) {
                    nanos *= 10;
                }
            }
            skipSpace();
        }
        char sign = charAt(position);
        if (sign == '-' || sign == '+') {
            hasOffset = true;
            int[] parts = new int[3];
            for (int i = 0; i < 3 && (i == 0 || charAt(position) == ':'); i++) {
                position++;
                parts[i] = number(digitsEnd(position));
            }
            int direction = sign == '-' ? -1 : 1;
            offset = ZoneOffset.ofHoursMinutesSeconds(direction * parts[0], direction * parts[1],
                    direction * parts[2]);
            skipSpace();
        }
        if (hasDate && text.startsWith("AD", position)) {
            position += 2;
        } else if (hasDate && text.startsWith("BC", position)) {
            beforeChrist = true;
            position += 2;
        }
        if (position < text.length() || (!hasDate && !hasTime)) {
            throw new NumberFormatException(text);
        }
    }

    private char charAt(int index) {
        return index < text.length() ? text.charAt(index) : 0;
    }

    private static boolean isDigit(char c) {
        return c >= '0' && c <= '9';
    }

    private void skipSpace() {
        while (position < text.length() && text.charAt(position) < 128
                && Character.isWhitespace(text.charAt(position))) {
            position++;
        }
    }

    private int digitsEnd(int from) {
        int end = from;
        while (isDigit(charAt(end))) {
            end++;
        }
        return end;
    }

    /** Reads the digits from the current position to {@code end} and moves there. */
    private int number(int end) {
        if (position >= end) {
            throw new NumberFormatException(text);
        }
        int value = 0;
        while (position < end) {
            value = 10 * value + (text.charAt(position++) - '0');
        }
        return value;
    }

    private void expect(char c) {
        if (text.charAt(position) != c) {
            throw new NumberFormatException(text);
        }
    }

    /**
     * Returns the milliseconds of the date and the time of day (without the fraction) in
     * the offset of the text or, when the text has none, in the given zone.
     */
    long millis(TimeZone zone, boolean withDate) {
        Calendar calendar = calendar(zone);
        if (withDate) {
            calendar.set(Calendar.ERA, beforeChrist ? GregorianCalendar.BC : GregorianCalendar.AD);
            calendar.set(Calendar.YEAR, year);
            calendar.set(Calendar.MONTH, month - 1);
            calendar.set(Calendar.DAY_OF_MONTH, day);
        } else {
            calendar.set(Calendar.ERA, GregorianCalendar.AD);
            calendar.set(Calendar.YEAR, 1970);
            calendar.set(Calendar.MONTH, Calendar.JANUARY);
            calendar.set(Calendar.DAY_OF_MONTH, 1);
        }
        calendar.set(Calendar.HOUR_OF_DAY, hour);
        calendar.set(Calendar.MINUTE, minute);
        calendar.set(Calendar.SECOND, second);
        calendar.set(Calendar.MILLISECOND, 0);
        return calendar.getTimeInMillis();
    }

    /** Returns the zone the fields are in: the offset of the text, else the given zone. */
    TimeZone zone(TimeZone zone) {
        if (!hasOffset) {
            return zone;
        }
        int seconds = offset.getTotalSeconds();
        return new SimpleTimeZone(seconds * 1000, seconds == 0 ? "UTC" : "GMT" + offset.getId());
    }

    private Calendar calendar(TimeZone zone) {
        return new GregorianCalendar(zone(zone));
    }

    /**
     * Appends a number with at least the given number of digits.
     */
    static void pad(StringBuilder out, int value, int digits) {
        String number = Integer.toString(value);
        for (int i = number.length(); i < digits; i++) {
            out.append('0');
        }
        out.append(number);
    }

    /** Appends "yyyy-mm-dd"; the year has at least four digits. */
    static void appendDate(StringBuilder out, int year, int month, int day) {
        pad(out, year, 4);
        out.append('-');
        pad(out, month, 2);
        out.append('-');
        pad(out, day, 2);
    }

    /**
     * Appends "hh:mm:ss" and, when the nanoseconds hold at least one microsecond, the
     * microseconds without trailing zeros.
     */
    static void appendTime(StringBuilder out, int hour, int minute, int second, int nanos) {
        pad(out, hour, 2);
        out.append(':');
        pad(out, minute, 2);
        out.append(':');
        pad(out, second, 2);
        if (nanos < 1000) {
            return;
        }
        out.append('.');
        pad(out, nanos / 1000, 6);
        while (out.charAt(out.length() - 1) == '0') {
            out.setLength(out.length() - 1);
        }
    }

    /** Appends "+hh", "+hh:mm" or "+hh:mm:ss" for an offset in seconds. */
    static void appendOffset(StringBuilder out, int offsetSeconds) {
        int absolute = Math.abs(offsetSeconds);
        out.append(offsetSeconds >= 0 ? '+' : '-');
        pad(out, absolute / 3600, 2);
        int minutes = absolute % 3600 / 60;
        int seconds = absolute % 60;
        if (minutes != 0 || seconds != 0) {
            out.append(':');
            pad(out, minutes, 2);
        }
        if (seconds != 0) {
            out.append(':');
            pad(out, seconds, 2);
        }
    }
}
