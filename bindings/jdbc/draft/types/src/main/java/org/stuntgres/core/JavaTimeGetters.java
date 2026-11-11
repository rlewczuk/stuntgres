package org.stuntgres.core;

import java.sql.SQLException;
import java.time.DateTimeException;
import java.time.LocalDate;
import java.time.LocalDateTime;
import java.time.LocalTime;
import java.time.OffsetDateTime;
import java.time.OffsetTime;
import java.time.ZoneOffset;
import java.time.format.DateTimeParseException;
import java.time.temporal.ChronoField;

/**
 * The conversions behind ResultSet.getObject(column, Class) for the java.time classes
 * (design section 7.6). Each method accepts only the column types named in its
 * description; the value is the column text and is never null.
 */
public final class JavaTimeGetters {
    private JavaTimeGetters() {
    }

    /**
     * Reads a date or timestamp column as LocalDate.
     *
     * @param value the column text
     * @param type the type of the column
     * @return the date; LocalDate.MAX and LocalDate.MIN for "infinity" and "-infinity"
     * @throws SQLException with SQLState 42821 for another column type, 22007 for text
     *         that is not a date
     */
    public static LocalDate getLocalDate(String value, PgType type) throws SQLException {
        if (type.oid() == Oid.TIMESTAMP) {
            return getLocalDateTime(value, type).toLocalDate();
        }
        if (type.oid() != Oid.DATE) {
            throw ConversionErrors.wrongColumnType(LocalDate.class, type.name());
        }
        if (value.equals("infinity")) {
            return LocalDate.MAX;
        }
        if (value.equals("-infinity")) {
            return LocalDate.MIN;
        }
        try {
            boolean beforeChrist = value.endsWith("BC");
            String[] parts = (beforeChrist ? value.substring(0, value.length() - 3) : value)
                    .split("-", -1);
            LocalDate date = LocalDate.of(Integer.parseInt(parts[0]), Integer.parseInt(parts[1]),
                    Integer.parseInt(parts[2]));
            return beforeChrist ? date.with(ChronoField.ERA, 0) : date;
        } catch (RuntimeException e) {
            throw ConversionErrors.badDateTime("a date", value);
        }
    }

    /**
     * Reads a time column as LocalTime.
     *
     * @param value the column text
     * @param type the type of the column
     * @return the time; LocalTime.MAX for "24:00:00"
     * @throws SQLException with SQLState 42821 for another column type, 22007 for text
     *         that is not a time
     */
    public static LocalTime getLocalTime(String value, PgType type) throws SQLException {
        if (type.oid() != Oid.TIME) {
            throw ConversionErrors.wrongColumnType(LocalTime.class, type.name());
        }
        if (value.equals("24:00:00")) {
            return LocalTime.MAX;
        }
        try {
            return LocalTime.parse(value);
        } catch (DateTimeParseException e) {
            throw ConversionErrors.badDateTime("a time", value);
        }
    }

    /**
     * Reads a timestamp column as LocalDateTime.
     *
     * @param value the column text
     * @param type the type of the column
     * @return the timestamp; LocalDateTime.MAX and MIN for "infinity" and "-infinity"
     * @throws SQLException with SQLState 42821 for another column type, 22007 for text
     *         that is not a timestamp
     */
    public static LocalDateTime getLocalDateTime(String value, PgType type)
            throws SQLException {
        if (type.oid() != Oid.TIMESTAMP) {
            throw ConversionErrors.wrongColumnType(LocalDateTime.class, type.name());
        }
        if (value.equals("infinity")) {
            return LocalDateTime.MAX;
        }
        if (value.equals("-infinity")) {
            return LocalDateTime.MIN;
        }
        DateTimeText f = DateTimeText.parse(value);
        try {
            LocalDateTime result = LocalDateTime.of(f.year, f.month, f.day, f.hour, f.minute,
                    f.second, f.nanos);
            return f.beforeChrist ? result.with(ChronoField.ERA, 0) : result;
        } catch (DateTimeException e) {
            throw ConversionErrors.badDateTime("a timestamp", value);
        }
    }

    /**
     * Reads a timestamptz, timestamp or timetz column as OffsetDateTime. A timestamp is
     * returned at offset zero; a timestamp without time zone is taken as UTC; a timetz is
     * returned on 1970-01-01 with its own offset.
     *
     * @param value the column text
     * @param type the type of the column
     * @return the timestamp; OffsetDateTime.MAX and MIN for "infinity" and "-infinity"
     * @throws SQLException with SQLState 42821 for another column type, 22007 for text
     *         that is not a timestamp
     */
    public static OffsetDateTime getOffsetDateTime(String value, PgType type)
            throws SQLException {
        int oid = type.oid();
        if (oid != Oid.TIMESTAMPTZ && oid != Oid.TIMESTAMP && oid != Oid.TIMETZ) {
            throw ConversionErrors.wrongColumnType(OffsetDateTime.class, type.name());
        }
        if (value.equals("infinity")) {
            return OffsetDateTime.MAX;
        }
        if (value.equals("-infinity")) {
            return OffsetDateTime.MIN;
        }
        DateTimeText f = DateTimeText.parse(value);
        try {
            OffsetDateTime result = OffsetDateTime.of(f.year, f.month, f.day, f.hour, f.minute,
                    f.second, f.nanos, f.offset);
            if (f.beforeChrist) {
                result = result.with(ChronoField.ERA, 0);
            }
            return oid == Oid.TIMETZ ? result : result.withOffsetSameInstant(ZoneOffset.UTC);
        } catch (DateTimeException e) {
            throw ConversionErrors.badDateTime("a timestamp", value);
        }
    }

    /**
     * Reads a timetz column as OffsetTime.
     *
     * @param value the column text
     * @param type the type of the column
     * @return the time; OffsetTime.MAX for text that starts with "24:00:00"
     * @throws SQLException with SQLState 42821 for another column type, 22007 for text
     *         that is not a time
     */
    public static OffsetTime getOffsetTime(String value, PgType type) throws SQLException {
        if (type.oid() != Oid.TIMETZ) {
            throw ConversionErrors.wrongColumnType(OffsetTime.class, type.name());
        }
        if (value.startsWith("24:00:00")) {
            return OffsetTime.MAX;
        }
        DateTimeText f = DateTimeText.parse(value);
        try {
            return OffsetTime.of(f.hour, f.minute, f.second, f.nanos, f.offset);
        } catch (DateTimeException e) {
            throw ConversionErrors.badDateTime("a time", value);
        }
    }
}
