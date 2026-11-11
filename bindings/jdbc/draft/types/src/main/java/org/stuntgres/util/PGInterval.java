package org.stuntgres.util;

import java.sql.SQLException;
import java.util.Calendar;
import java.util.Date;
import java.util.Locale;
import java.util.StringTokenizer;

/** A value of the PostgreSQL type interval as six fields. */
public class PGInterval extends PGobject {
    private static final long serialVersionUID = 1L;
    private static final int MICROS = 1000000;

    private int years;
    private int months;
    private int days;
    private int hours;
    private int minutes;
    private int wholeSeconds;
    private int microSeconds;
    private boolean isNull;

    /** Creates the interval of length zero. */
    public PGInterval() {
        type = "interval";
    }

    /**
     * Creates an interval from text.
     *
     * @param value text as {@link #setValue(String)} takes it
     * @throws SQLException with SQLState 42820 when a number of the text is malformed
     */
    public PGInterval(String value) throws SQLException {
        this();
        read(value);
    }

    /**
     * Creates an interval from its fields.
     *
     * @param years the years
     * @param months the months
     * @param days the days
     * @param hours the hours
     * @param minutes the minutes
     * @param seconds the seconds, rounded to microseconds
     */
    public PGInterval(int years, int months, int days, int hours, int minutes, double seconds) {
        this();
        assign(years, months, days, hours, minutes, seconds);
    }

    /**
     * Sets the fields from text in one of the server's output styles: {@code postgres}
     * ("1 year 2 mons 3 days 04:05:06.789"), {@code postgres_verbose} ("@ 1 year 2 mons
     * ... ago") or {@code iso_8601} ("P1Y2M3DT4H5M6.789S"). Words the method does not know
     * are skipped.
     *
     * @param value the text, or null for SQL NULL
     * @throws SQLException with SQLState 42820 when a number of the text is malformed
     */
    @Override
    public void setValue(String value) throws SQLException {
        read(value);
    }

    private void read(String value) throws SQLException {
        if (value == null) {
            assign(0, 0, 0, 0, 0, 0);
            isNull = true;
            return;
        }
        isNull = false;
        try {
            if (value.startsWith("P")) {
                iso(value);
                return;
            }
            boolean verbose = value.startsWith("@");
            if (verbose && value.length() == 3 && value.charAt(2) == '0') {
                assign(0, 0, 0, 0, 0, 0);
                return;
            }
            words(value.replace('+', ' ').replace('@', ' ').toLowerCase(Locale.ROOT), verbose);
        } catch (NumberFormatException | IndexOutOfBoundsException e) {
            throw new PSQLException("Cannot read \"" + value + "\" as an interval.",
                    PSQLState.NUMERIC_CONSTANT_OUT_OF_RANGE, e);
        }
    }

    private void words(String text, boolean verbose) {
        int[] parts = new int[5];
        double seconds = 0;
        String number = null;
        StringTokenizer tokens = new StringTokenizer(text);
        for (boolean odd = true; tokens.hasMoreTokens(); odd = !odd) {
            String token = tokens.nextToken();
            int colon = token.indexOf(':');
            if (odd && colon < 0) {
                number = token;
            } else if (odd) {
                int sign = token.charAt(0) == '-' ? -1 : 1;
                parts[3] = sign * Integer.parseInt(token.substring(sign < 0 ? 1 : 0, colon));
                parts[4] = sign * Integer.parseInt(token.substring(colon + 1, colon + 3));
                int second = token.indexOf(':', colon + 1);
                seconds = second < 0 ? seconds : sign * Double.parseDouble(token.substring(second + 1));
                number = null;
            } else if (token.startsWith("sec")) {
                seconds = number == null ? 0 : Double.parseDouble(number);
            } else {
                String[] units = {"year", "mon", "day", "hour", "min"};
                for (int i = 0; i < units.length; i++) {
                    if (token.startsWith(units[i])) {
                        parts[i] = number == null ? 0 : Integer.parseInt(number);
                        break;
                    }
                }
            }
        }
        int sign = verbose && text.endsWith("ago") ? -1 : 1;
        assign(sign * parts[0], sign * parts[1], sign * parts[2], sign * parts[3],
                sign * parts[4], sign * seconds);
    }

    private void iso(String text) {
        int t = text.indexOf('T');
        isoPart(t > 0 ? text.substring(1, t) : text.substring(1), "YMD");
        if (t > 0) {
            isoPart(text.substring(t + 1), "HMS");
        }
    }

    /** Reads numbers that are followed by one of three unit letters. */
    private void isoPart(String part, String units) {
        for (int i = 0; i < part.length(); i++) {
            int unit = -1;
            for (int k = 0; k < 3 && unit <= 0; k++) {
                unit = part.indexOf(units.charAt(k), i);
            }
            if (unit <= 0) {
                continue;
            }
            String number = part.substring(i, unit);
            switch (part.charAt(unit)) {
                case 'Y': years = Integer.parseInt(number); break;
                case 'D': days = Integer.parseInt(number); break;
                case 'H': hours = Integer.parseInt(number); break;
                case 'S': seconds(Double.parseDouble(number)); break;
                default:
                    if (units.charAt(0) == 'Y') {
                        months = Integer.parseInt(number);
                    } else {
                        minutes = Integer.parseInt(number);
                    }
                    break;
            }
            i = unit;
        }
    }

    /**
     * Sets all fields.
     *
     * @param years the years
     * @param months the months
     * @param days the days
     * @param hours the hours
     * @param minutes the minutes
     * @param seconds the seconds, rounded to microseconds
     */
    public void setValue(int years, int months, int days, int hours, int minutes,
            double seconds) {
        assign(years, months, days, hours, minutes, seconds);
    }

    private void assign(int newYears, int newMonths, int newDays, int newHours, int newMinutes,
            double newSeconds) {
        isNull = false;
        years = newYears;
        months = newMonths;
        days = newDays;
        hours = newHours;
        minutes = newMinutes;
        seconds(newSeconds);
    }

    /**
     * Returns the fields in the form "1 years 2 mons 3 days 4 hours 5 mins 6.789 secs";
     * fields that are zero are left out, except that an interval of length zero is
     * "0 secs".
     */
    @Override
    public String getValue() {
        if (isNull) {
            return null;
        }
        StringBuilder text = new StringBuilder();
        int[] fields = {years, months, days, hours, minutes};
        String[] units = {" years", " mons", " days", " hours", " mins"};
        for (int i = 0; i < fields.length; i++) {
            if (fields[i] != 0) {
                text.append(text.length() > 0 ? " " : "").append(fields[i]).append(units[i]);
            }
        }
        if (text.length() == 0 || wholeSeconds != 0 || microSeconds != 0) {
            text.append(text.length() > 0 ? " " : "");
            text.append(wholeSeconds < 0 || microSeconds < 0 ? "-" : "");
            text.append(Math.abs(wholeSeconds));
            if (microSeconds != 0) {
                String micros = String.format(Locale.ROOT, "%06d", Math.abs(microSeconds));
                text.append('.').append(micros.replaceAll("0+$", ""));
            }
            text.append(" secs");
        }
        return text.toString();
    }

    /**
     * Returns the years.
     *
     * @return the years field
     */
    public int getYears() {
        return years;
    }

    /**
     * Sets the years.
     *
     * @param years the years field
     */
    public void setYears(int years) {
        isNull = false;
        this.years = years;
    }

    /**
     * Returns the months.
     *
     * @return the months field
     */
    public int getMonths() {
        return months;
    }

    /**
     * Sets the months.
     *
     * @param months the months field
     */
    public void setMonths(int months) {
        isNull = false;
        this.months = months;
    }

    /**
     * Returns the days.
     *
     * @return the days field
     */
    public int getDays() {
        return days;
    }

    /**
     * Sets the days.
     *
     * @param days the days field
     */
    public void setDays(int days) {
        isNull = false;
        this.days = days;
    }

    /**
     * Returns the hours.
     *
     * @return the hours field
     */
    public int getHours() {
        return hours;
    }

    /**
     * Sets the hours.
     *
     * @param hours the hours field
     */
    public void setHours(int hours) {
        isNull = false;
        this.hours = hours;
    }

    /**
     * Returns the minutes.
     *
     * @return the minutes field
     */
    public int getMinutes() {
        return minutes;
    }

    /**
     * Sets the minutes.
     *
     * @param minutes the minutes field
     */
    public void setMinutes(int minutes) {
        isNull = false;
        this.minutes = minutes;
    }

    /**
     * Returns the seconds with their fraction.
     *
     * @return whole seconds plus microseconds
     */
    public double getSeconds() {
        return wholeSeconds + (double) microSeconds / MICROS;
    }

    /**
     * Returns the seconds without their fraction.
     *
     * @return the whole seconds
     */
    public int getWholeSeconds() {
        return wholeSeconds;
    }

    /**
     * Returns the fraction of the seconds.
     *
     * @return microseconds, with the sign of the seconds
     */
    public int getMicroSeconds() {
        return microSeconds;
    }

    /**
     * Sets the seconds.
     *
     * @param seconds the seconds; rounded to microseconds
     * @throws IllegalArgumentException for NaN and for more whole seconds than an int holds
     */
    public void setSeconds(double seconds) {
        seconds(seconds);
    }

    private void seconds(double seconds) {
        if (Double.isNaN(seconds)) {
            throw new IllegalArgumentException("The number of seconds must not be NaN.");
        }
        long micros = Math.round(seconds * MICROS);
        long whole = micros / MICROS;
        if (whole < Integer.MIN_VALUE || whole > Integer.MAX_VALUE) {
            throw new IllegalArgumentException("The number of seconds is out of range.");
        }
        isNull = false;
        wholeSeconds = (int) whole;
        microSeconds = (int) (micros % MICROS);
    }

    /**
     * Moves a calendar forward by this interval; the fraction of the seconds is rounded to
     * milliseconds.
     *
     * @param cal the calendar to change
     */
    public void add(Calendar cal) {
        if (isNull) {
            return;
        }
        int millis = (microSeconds + (microSeconds < 0 ? -500 : 500)) / 1000 + wholeSeconds * 1000;
        cal.add(Calendar.MILLISECOND, millis);
        cal.add(Calendar.MINUTE, minutes);
        cal.add(Calendar.HOUR, hours);
        cal.add(Calendar.DAY_OF_MONTH, days);
        cal.add(Calendar.MONTH, months);
        cal.add(Calendar.YEAR, years);
    }

    /**
     * Moves a date forward by this interval, using the default calendar.
     *
     * @param date the date to change
     */
    public void add(Date date) {
        if (isNull) {
            return;
        }
        Calendar cal = Calendar.getInstance();
        cal.setTime(date);
        add(cal);
        date.setTime(cal.getTimeInMillis());
    }

    /**
     * Adds this interval to another one, field by field.
     *
     * @param interval the interval to change
     */
    public void add(PGInterval interval) {
        if (isNull || interval.isNull) {
            return;
        }
        interval.assign(interval.years + years, interval.months + months,
                interval.days + days, interval.hours + hours, interval.minutes + minutes,
                interval.getSeconds() + getSeconds());
    }

    /**
     * Multiplies every field.
     *
     * @param factor the factor
     */
    public void scale(int factor) {
        if (isNull) {
            return;
        }
        assign(factor * years, factor * months, factor * days, factor * hours,
                factor * minutes, factor * getSeconds());
    }

    @Override
    public boolean equals(Object obj) {
        if (!(obj instanceof PGInterval)) {
            return false;
        }
        PGInterval other = (PGInterval) obj;
        if (isNull || other.isNull) {
            return isNull && other.isNull;
        }
        return other.years == years && other.months == months && other.days == days
                && other.hours == hours && other.minutes == minutes
                && other.wholeSeconds == wholeSeconds && other.microSeconds == microSeconds;
    }

    @Override
    public int hashCode() {
        if (isNull) {
            return 0;
        }
        int hash = 8 * 31 + microSeconds;
        for (int field : new int[] {wholeSeconds, minutes, hours, days, months, years}) {
            hash = hash * 31 + field;
        }
        return hash * 31;
    }

    @Override
    public Object clone() throws CloneNotSupportedException {
        return super.clone();
    }
}
