package org.stuntgres.core;

import java.math.BigDecimal;
import java.math.BigInteger;
import java.net.InetAddress;
import java.net.UnknownHostException;
import java.sql.SQLException;
import java.sql.Time;
import java.sql.Timestamp;
import java.sql.Types;
import java.time.LocalDate;
import java.time.LocalDateTime;
import java.time.LocalTime;
import java.time.OffsetDateTime;
import java.time.OffsetTime;
import java.util.Calendar;
import java.util.GregorianCalendar;
import java.util.UUID;
import org.stuntgres.util.PGobject;
import org.stuntgres.util.PSQLException;
import org.stuntgres.util.PSQLState;

/**
 * The conversion behind ResultSet.getObject(column, Class) (design section 7.8). The value
 * is the column text and is never null. The classes java.sql.Array, Blob, Clob and SQLXML
 * are not handled here: the caller reads them with getArray, getBlob, getClob and
 * getSQLXML when the column has a JDBC type that design table 7.8 allows.
 */
public final class ClassGetters {
    private ClassGetters() {
    }

    /**
     * Reads a column as an object of a class.
     *
     * @param <T> the class
     * @param value the column text
     * @param type the type of the column
     * @param target the requested class
     * @param context the conversion context
     * @return the object
     * @throws SQLException with SQLState 22023 when the column type cannot be read as the
     *         class, 42821 for a java.time class and a column of another type, and the
     *         exceptions of the getter the class maps to
     */
    public static <T> T getObject(String value, PgType type, Class<T> target,
            ConversionContext context) throws SQLException {
        return target.cast(convert(value, type, target, context));
    }

    private static Object convert(String value, PgType type, Class<?> target,
            ConversionContext context) throws SQLException {
        int oid = type.oid();
        int sql = type.sqlType();
        boolean supported;
        if (target == BigDecimal.class) {
            supported = sql == Types.NUMERIC || sql == Types.DECIMAL;
            return check(supported, target, type)
                    ? DecimalGetters.getBigDecimal(value, oid, context) : null;
        } else if (target == String.class) {
            return check(sql == Types.CHAR || sql == Types.VARCHAR, target, type) ? value : null;
        } else if (target == Boolean.class) {
            check(sql == Types.BOOLEAN || sql == Types.BIT, target, type);
            return IntegerGetters.getBoolean(value, oid);
        } else if (target == Short.class) {
            check(sql == Types.SMALLINT, target, type);
            return IntegerGetters.getShort(value, oid, context);
        } else if (target == Integer.class) {
            check(sql == Types.INTEGER || sql == Types.SMALLINT, target, type);
            return IntegerGetters.getInt(value, oid, context);
        } else if (target == Long.class) {
            check(sql == Types.BIGINT, target, type);
            return IntegerGetters.getLong(value, oid, context);
        } else if (target == BigInteger.class) {
            check(sql == Types.BIGINT, target, type);
            return BigInteger.valueOf(IntegerGetters.getLong(value, oid, context));
        } else if (target == Float.class) {
            check(sql == Types.REAL, target, type);
            return DecimalGetters.getFloat(value, oid, context);
        } else if (target == Double.class) {
            check(sql == Types.FLOAT || sql == Types.DOUBLE, target, type);
            return DecimalGetters.getDouble(value, oid, context);
        } else if (target == java.sql.Date.class) {
            check(sql == Types.DATE, target, type);
            return DateTimeGetters.getDate(value, null, context);
        } else if (target == Time.class) {
            check(sql == Types.TIME, target, type);
            return DateTimeGetters.getTime(value, null, context);
        } else if (target == Timestamp.class || target == Calendar.class
                || target == java.util.Date.class) {
            supported = sql == Types.TIMESTAMP
                    || (sql == Types.TIMESTAMP_WITH_TIMEZONE && target != java.util.Date.class);
            check(supported, target, type);
            Timestamp timestamp = DateTimeGetters.getTimestamp(value, oid, null, context);
            if (target == Timestamp.class) {
                return timestamp;
            }
            if (target == java.util.Date.class) {
                return new java.util.Date(timestamp.getTime());
            }
            Calendar calendar = new GregorianCalendar(context.defaultZone());
            calendar.setTimeInMillis(timestamp.getTime());
            return calendar;
        } else if (target == byte[].class) {
            supported = sql == Types.BINARY || sql == Types.VARBINARY || sql == Types.LONGVARBINARY;
            check(supported, target, type);
            return TextGetters.getBytes(value, oid);
        } else if (target == UUID.class) {
            Object object = scalar(value, type, context);
            check(object instanceof UUID, target, type);
            return object;
        } else if (target == InetAddress.class) {
            return inetAddress(value);
        } else if (target == LocalDate.class) {
            return JavaTimeGetters.getLocalDate(value, type);
        } else if (target == LocalTime.class) {
            return JavaTimeGetters.getLocalTime(value, type);
        } else if (target == LocalDateTime.class) {
            return JavaTimeGetters.getLocalDateTime(value, type);
        } else if (target == OffsetDateTime.class) {
            return JavaTimeGetters.getOffsetDateTime(value, type);
        } else if (target == OffsetTime.class) {
            return JavaTimeGetters.getOffsetTime(value, type);
        } else if (PGobject.class.isAssignableFrom(target)) {
            Object object = ObjectGetters.pgObject(type.name(), value);
            check(target.isInstance(object), target, type);
            return object;
        }
        throw ConversionErrors.unsupportedClass(target, type.name());
    }

    private static boolean check(boolean supported, Class<?> target, PgType type)
            throws SQLException {
        if (!supported) {
            throw ConversionErrors.unsupportedClass(target, type.name());
        }
        return true;
    }

    /** The default object of a column, or null for a column the caller reads itself. */
    private static Object scalar(String value, PgType type, ConversionContext context)
            throws SQLException {
        int sql = type.sqlType();
        if (sql == Types.ARRAY || sql == Types.SQLXML || sql == Types.REF_CURSOR) {
            return null;
        }
        return ObjectGetters.getObject(value, type, -1, context);
    }

    /**
     * Reads the address of inet or cidr text; the part from the slash on is ignored. Only
     * numeric addresses are read: a name is never looked up. Empty text gives the loopback
     * address, as InetAddress.getByName does.
     */
    private static InetAddress inetAddress(String value) throws SQLException {
        int slash = value.indexOf('/');
        String address = slash < 0 ? value : value.substring(0, slash);
        boolean numeric = true;
        boolean colon = address.indexOf(':') >= 0;
        for (int i = 0; numeric && i < address.length(); i++) {
            char c = address.charAt(i);
            numeric = (c >= '0' && c <= '9') || c == '.'
                    || (colon && (c == ':' || Character.digit(c, 16) >= 0));
        }
        try {
            if (numeric) {
                return InetAddress.getByName(address);
            }
        } catch (UnknownHostException e) {
            // reported below
        }
        throw new PSQLException("Cannot read the value \"" + value + "\" as an IP address.",
                PSQLState.INVALID_PARAMETER_VALUE);
    }
}
