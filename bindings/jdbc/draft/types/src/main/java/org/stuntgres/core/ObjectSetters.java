package org.stuntgres.core;

import java.math.BigDecimal;
import java.math.BigInteger;
import java.math.RoundingMode;
import java.sql.Date;
import java.sql.SQLException;
import java.sql.SQLXML;
import java.sql.Time;
import java.sql.Timestamp;
import java.sql.Types;
import java.time.LocalDate;
import java.time.LocalDateTime;
import java.time.LocalTime;
import java.time.OffsetDateTime;
import java.time.OffsetTime;
import java.util.Map;
import java.util.UUID;
import org.stuntgres.util.PGTimestamp;
import org.stuntgres.util.PGobject;
import org.stuntgres.util.PSQLException;
import org.stuntgres.util.PSQLState;

/**
 * The conversions behind PreparedStatement.setObject without and with a target type
 * (design sections 8.7 and 8.8). Values of the classes java.sql.Blob and Clob are not
 * handled here: the caller stores them as large objects.
 */
public final class ObjectSetters {
    private ObjectSetters() {
    }

    /**
     * Converts the argument of setObject(index, x).
     *
     * @param x the value, or null
     * @param context the conversion context
     * @return the parameter by the class of the value (design table 8.7)
     * @throws SQLException with SQLState 07006 for a class without a PostgreSQL type
     */
    public static ParamValue setObject(Object x, ConversionContext context)
            throws SQLException {
        if (x == null) {
            return new ParamValue(Oid.UNSPECIFIED, null);
        } else if (x instanceof UUID) {
            return new ParamValue(Oid.UUID, x.toString());
        } else if (x instanceof SQLXML) {
            return new ParamValue(Oid.XML, ((SQLXML) x).getString());
        } else if (x instanceof String) {
            return BasicSetters.setString((String) x, context);
        } else if (x instanceof BigDecimal) {
            return BasicSetters.setBigDecimal((BigDecimal) x);
        } else if (x instanceof Short) {
            return BasicSetters.setShort((Short) x);
        } else if (x instanceof Integer) {
            return BasicSetters.setInt((Integer) x);
        } else if (x instanceof Long) {
            return BasicSetters.setLong((Long) x);
        } else if (x instanceof Float) {
            return BasicSetters.setFloat((Float) x);
        } else if (x instanceof Double) {
            return BasicSetters.setDouble((Double) x);
        } else if (x instanceof byte[]) {
            return BasicSetters.setBytes((byte[]) x);
        } else if (x instanceof Date) {
            return DateTimeSetters.setDate((Date) x, null, context);
        } else if (x instanceof Time) {
            return DateTimeSetters.setTime((Time) x, null, context);
        } else if (x instanceof Timestamp) {
            return DateTimeSetters.setTimestamp((Timestamp) x, null, context);
        } else if (x instanceof Boolean) {
            return BasicSetters.setBoolean((Boolean) x);
        } else if (x instanceof Byte) {
            return BasicSetters.setShort((Byte) x);
        } else if (x instanceof java.sql.Array) {
            java.sql.Array array = (java.sql.Array) x;
            return setArray(array.getBaseTypeName(), array.toString(), context);
        } else if (x instanceof PGobject) {
            return setPGobject((PGobject) x, context);
        } else if (x instanceof Character) {
            return BasicSetters.setString(x.toString(), context);
        } else if (x instanceof LocalDate) {
            return DateTimeSetters.setLocalDate((LocalDate) x);
        } else if (x instanceof LocalTime) {
            return DateTimeSetters.setLocalTime((LocalTime) x);
        } else if (x instanceof OffsetTime) {
            return DateTimeSetters.setOffsetTime((OffsetTime) x);
        } else if (x instanceof LocalDateTime) {
            return DateTimeSetters.setLocalDateTime((LocalDateTime) x);
        } else if (x instanceof OffsetDateTime) {
            return DateTimeSetters.setOffsetDateTime((OffsetDateTime) x);
        } else if (x instanceof Map) {
            return setMap((Map<?, ?>) x, context);
        } else if (x instanceof Number) {
            return new ParamValue(Oid.NUMERIC, x.toString());
        } else if (x.getClass().isArray()) {
            return setJavaArray(x);
        }
        throw new PSQLException("Cannot tell the SQL type of a value of class "
                + x.getClass().getName() + "; use setObject with a java.sql.Types value.",
                PSQLState.INVALID_PARAMETER_TYPE);
    }

    /**
     * Converts a java.sql.Array parameter from its element type name and its text.
     *
     * @param baseTypeName the element type name of the array
     * @param text the array text, or null
     * @param context the conversion context
     * @return an array of the array type over the element type
     * @throws SQLException with SQLState 42602 when the element type has no array type
     */
    public static ParamValue setArray(String baseTypeName, String text,
            ConversionContext context) throws SQLException {
        int oid = TypeTable.arrayOidByElementName(baseTypeName, context.types());
        if (oid == 0) {
            throw new PSQLException("No array type exists for the element type " + baseTypeName
                    + ".", PSQLState.INVALID_NAME);
        }
        return new ParamValue(oid, text);
    }

    private static ParamValue setJavaArray(Object x) throws SQLException {
        int oid = ArrayFormat.arrayOid(x);
        if (oid == 0) {
            throw ConversionErrors.badParameter(x, "Types.ARRAY");
        }
        return new ParamValue(oid, ArrayFormat.format(x, ','));
    }

    private static ParamValue setPGobject(PGobject x, ConversionContext context)
            throws SQLException {
        String typeName = x.getType();
        int oid = typeName == null ? 0 : TypeTable.oidByName(typeName, context.types());
        if (oid == 0) {
            throw new PSQLException("The type " + typeName + " of a PGobject is not known.",
                    PSQLState.INVALID_PARAMETER_TYPE);
        }
        return new ParamValue(oid, x.getValue());
    }

    private static ParamValue setMap(Map<?, ?> x, ConversionContext context)
            throws SQLException {
        int oid = TypeTable.oidByName("hstore", context.types());
        if (oid == 0) {
            throw new PSQLException("The extension hstore is not installed.",
                    PSQLState.INVALID_PARAMETER_TYPE);
        }
        return new ParamValue(oid, HstoreText.format(x));
    }

    /**
     * Converts the argument of setObject(index, x, targetSqlType, scale).
     *
     * @param x the value, or null
     * @param targetSqlType a constant of java.sql.Types
     * @param scale the scale for NUMERIC and DECIMAL; negative for none
     * @param context the conversion context
     * @return the parameter by the target type (design table 8.8)
     * @throws SQLException with SQLState 07006 for a value that cannot be sent as the
     *         target type, 42846 for a value that is no boolean, 22007 for text that is
     *         no date, time or timestamp
     */
    public static ParamValue setObject(Object x, int targetSqlType, int scale,
            ConversionContext context) throws SQLException {
        if (x == null) {
            return BasicSetters.setNull(targetSqlType, context);
        }
        if (targetSqlType == Types.OTHER && x instanceof UUID) {
            return new ParamValue(Oid.UUID, x.toString());
        }
        switch (targetSqlType) {
            case Types.SQLXML:
                return new ParamValue(Oid.XML,
                        x instanceof SQLXML ? ((SQLXML) x).getString() : x.toString());
            case Types.INTEGER:
                return BasicSetters.setInt(number(x, "int").intValue());
            case Types.TINYINT:
            case Types.SMALLINT:
                return BasicSetters.setShort(number(x, "short").shortValue());
            case Types.BIGINT:
                return BasicSetters.setLong(number(x, "long").longValue());
            case Types.REAL:
                return BasicSetters.setFloat(number(x, "float").floatValue());
            case Types.DOUBLE:
            case Types.FLOAT:
                return BasicSetters.setDouble(number(x, "double").doubleValue());
            case Types.DECIMAL:
            case Types.NUMERIC:
                return BasicSetters.setBigDecimal(decimal(x, scale));
            case Types.CHAR:
                return new ParamValue(Oid.BPCHAR, x.toString());
            case Types.VARCHAR:
            case Types.LONGVARCHAR:
                return BasicSetters.setString(x.toString(), context);
            case Types.DATE:
                return date(x, context);
            case Types.TIME:
                return time(x, context);
            case Types.TIMESTAMP:
                return timestamp(x, context);
            case Types.TIMESTAMP_WITH_TIMEZONE:
                if (x instanceof OffsetDateTime) {
                    return DateTimeSetters.setOffsetDateTime((OffsetDateTime) x);
                } else if (x instanceof PGTimestamp) {
                    return setObject(x, context);
                }
                throw ConversionErrors.badParameter(x, "Types.TIMESTAMP_WITH_TIMEZONE");
            case Types.BOOLEAN:
            case Types.BIT:
                return BasicSetters.setBoolean(BooleanText.of(x));
            case Types.BINARY:
            case Types.VARBINARY:
            case Types.LONGVARBINARY:
                return setObject(x, context);
            case Types.ARRAY:
                if (x instanceof java.sql.Array) {
                    return setObject(x, context);
                }
                if (!x.getClass().isArray() || x instanceof byte[]) {
                    throw ConversionErrors.badParameter(x, "Types.ARRAY");
                }
                return setJavaArray(x);
            case Types.DISTINCT:
                return new ParamValue(Oid.UNSPECIFIED, x.toString());
            case Types.OTHER:
                if (x instanceof PGobject) {
                    return setPGobject((PGobject) x, context);
                } else if (x instanceof Map) {
                    return setMap((Map<?, ?>) x, context);
                }
                return new ParamValue(Oid.UNSPECIFIED, x.toString());
            default:
                throw new PSQLException("The java.sql.Types value " + targetSqlType
                        + " is not supported as a target type.", PSQLState.INVALID_PARAMETER_TYPE);
        }
    }

    /** Converts a String, Number, java.util.Date, Boolean or Character to a number. */
    private static Number number(Object x, String target) throws SQLException {
        try {
            if (x instanceof String || x instanceof Character) {
                String text = x.toString();
                switch (target) {
                    case "int": return Integer.parseInt(text);
                    case "short": return Short.parseShort(text);
                    case "long": return Long.parseLong(text);
                    case "float": return Float.parseFloat(text);
                    default: return Double.parseDouble(text);
                }
            } else if (x instanceof Number) {
                return (Number) x;
            } else if (x instanceof java.util.Date) {
                return ((java.util.Date) x).getTime();
            } else if (x instanceof Boolean) {
                return (Boolean) x ? 1 : 0;
            }
        } catch (NumberFormatException e) {
            // reported below
        }
        throw ConversionErrors.badParameter(x, target);
    }

    private static BigDecimal decimal(Object x, int scale) throws SQLException {
        BigDecimal value = null;
        try {
            if (x instanceof String) {
                value = new BigDecimal((String) x);
            } else if (x instanceof BigDecimal) {
                value = (BigDecimal) x;
            } else if (x instanceof BigInteger) {
                value = new BigDecimal((BigInteger) x);
            } else if (x instanceof Long || x instanceof Integer || x instanceof Short
                    || x instanceof Byte) {
                value = BigDecimal.valueOf(((Number) x).longValue());
            } else if (x instanceof Double || x instanceof Float) {
                value = BigDecimal.valueOf(((Number) x).doubleValue());
            } else if (x instanceof java.util.Date) {
                value = BigDecimal.valueOf(((java.util.Date) x).getTime());
            } else if (x instanceof Boolean) {
                value = (Boolean) x ? BigDecimal.ONE : BigDecimal.ZERO;
            } else if (x instanceof Character) {
                value = new BigDecimal(x.toString());
            }
        } catch (NumberFormatException e) {
            value = null;
        }
        if (value == null) {
            throw ConversionErrors.badParameter(x, "BigDecimal");
        }
        return scale >= 0 ? value.setScale(scale, RoundingMode.HALF_UP) : value;
    }

    private static ParamValue date(Object x, ConversionContext context) throws SQLException {
        if (x instanceof Date) {
            return DateTimeSetters.setDate((Date) x, null, context);
        } else if (x instanceof java.util.Date) {
            return DateTimeSetters.setDate(new Date(((java.util.Date) x).getTime()), null, context);
        } else if (x instanceof LocalDate) {
            return DateTimeSetters.setLocalDate((LocalDate) x);
        }
        return DateTimeSetters.setDate(DateTimeGetters.getDate(x.toString(), null, context), null,
                context);
    }

    private static ParamValue time(Object x, ConversionContext context) throws SQLException {
        if (x instanceof Time) {
            return DateTimeSetters.setTime((Time) x, null, context);
        } else if (x instanceof java.util.Date) {
            return DateTimeSetters.setTime(new Time(((java.util.Date) x).getTime()), null, context);
        } else if (x instanceof LocalTime) {
            return DateTimeSetters.setLocalTime((LocalTime) x);
        } else if (x instanceof OffsetTime) {
            return DateTimeSetters.setOffsetTime((OffsetTime) x);
        }
        return DateTimeSetters.setTime(DateTimeGetters.getTime(x.toString(), null, context), null,
                context);
    }

    private static ParamValue timestamp(Object x, ConversionContext context)
            throws SQLException {
        if (x instanceof PGTimestamp) {
            return setObject(x, context);
        } else if (x instanceof Timestamp) {
            return DateTimeSetters.setTimestamp((Timestamp) x, null, context);
        } else if (x instanceof java.util.Date) {
            return DateTimeSetters.setTimestamp(new Timestamp(((java.util.Date) x).getTime()),
                    null, context);
        } else if (x instanceof LocalDateTime) {
            return DateTimeSetters.setLocalDateTime((LocalDateTime) x);
        }
        return DateTimeSetters.setTimestamp(
                DateTimeGetters.getTimestamp(x.toString(), Oid.TIMESTAMP, null, context), null,
                context);
    }
}
