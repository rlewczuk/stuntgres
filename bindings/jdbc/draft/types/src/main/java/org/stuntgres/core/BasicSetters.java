package org.stuntgres.core;

import java.math.BigDecimal;
import java.sql.SQLException;
import java.sql.Types;
import org.stuntgres.util.PSQLException;
import org.stuntgres.util.PSQLState;

/**
 * The conversions behind PreparedStatement.setNull and the setters of booleans, numbers,
 * strings and bytes (design sections 8.2 and 8.3).
 */
public final class BasicSetters {
    private BasicSetters() {
    }

    /**
     * Returns the type OID setNull sends for a JDBC type.
     *
     * @param sqlType a constant of java.sql.Types
     * @param context the conversion context
     * @return SQL NULL with the OID of design table 8.2
     * @throws SQLException with SQLState 07006 for a JDBC type without a PostgreSQL type
     */
    public static ParamValue setNull(int sqlType, ConversionContext context)
            throws SQLException {
        int oid;
        switch (sqlType) {
            case Types.SQLXML: oid = Oid.XML; break;
            case Types.INTEGER: oid = Oid.INT4; break;
            case Types.TINYINT:
            case Types.SMALLINT: oid = Oid.INT2; break;
            case Types.BIGINT: oid = Oid.INT8; break;
            case Types.REAL: oid = Oid.FLOAT4; break;
            case Types.DOUBLE:
            case Types.FLOAT: oid = Oid.FLOAT8; break;
            case Types.DECIMAL:
            case Types.NUMERIC: oid = Oid.NUMERIC; break;
            case Types.CHAR: oid = Oid.BPCHAR; break;
            case Types.VARCHAR:
            case Types.LONGVARCHAR: oid = stringOid(context); break;
            case Types.DATE: oid = Oid.DATE; break;
            case Types.BIT:
            case Types.BOOLEAN: oid = Oid.BOOL; break;
            case Types.BINARY:
            case Types.VARBINARY:
            case Types.LONGVARBINARY: oid = Oid.BYTEA; break;
            case Types.BLOB:
            case Types.CLOB: oid = Oid.OID; break;
            case Types.REF_CURSOR: oid = Oid.REFCURSOR; break;
            case Types.TIME:
            case Types.TIMESTAMP:
            case Types.TIME_WITH_TIMEZONE:
            case Types.TIMESTAMP_WITH_TIMEZONE:
            case Types.ARRAY:
            case Types.DISTINCT:
            case Types.STRUCT:
            case Types.NULL:
            case Types.OTHER: oid = Oid.UNSPECIFIED; break;
            default:
                throw new PSQLException("The java.sql.Types value " + sqlType
                        + " has no PostgreSQL type.", PSQLState.INVALID_PARAMETER_TYPE);
        }
        return new ParamValue(oid, null);
    }

    /**
     * Returns SQL NULL of a named type.
     *
     * @param sqlType a constant of java.sql.Types, used when the name is null
     * @param typeName a PostgreSQL type name, or null
     * @param context the conversion context
     * @return SQL NULL with the OID of the named type; OID 0 when no type has the name
     * @throws SQLException as {@link #setNull(int, ConversionContext)} when the name is
     *         null, and when the lookup fails
     */
    public static ParamValue setNull(int sqlType, String typeName, ConversionContext context)
            throws SQLException {
        if (typeName == null) {
            return setNull(sqlType, context);
        }
        return new ParamValue(TypeTable.oidByName(typeName, context.types()), null);
    }

    /** The OID of a string parameter: varchar, or 0 for stringtype=unspecified. */
    static int stringOid(ConversionContext context) {
        return context.stringTypeUnspecified() ? Oid.UNSPECIFIED : Oid.VARCHAR;
    }

    /**
     * Converts a boolean parameter.
     *
     * @param x the value
     * @return bool TRUE or FALSE
     */
    public static ParamValue setBoolean(boolean x) {
        return new ParamValue(Oid.BOOL, x ? "TRUE" : "FALSE");
    }

    /**
     * Converts a short parameter; setByte uses it too.
     *
     * @param x the value
     * @return an int2
     */
    public static ParamValue setShort(short x) {
        return new ParamValue(Oid.INT2, Short.toString(x));
    }

    /**
     * Converts an int parameter.
     *
     * @param x the value
     * @return an int4
     */
    public static ParamValue setInt(int x) {
        return new ParamValue(Oid.INT4, Integer.toString(x));
    }

    /**
     * Converts a long parameter.
     *
     * @param x the value
     * @return an int8
     */
    public static ParamValue setLong(long x) {
        return new ParamValue(Oid.INT8, Long.toString(x));
    }

    /**
     * Converts a float parameter.
     *
     * @param x the value
     * @return a float4 with the text of Float.toString
     */
    public static ParamValue setFloat(float x) {
        return new ParamValue(Oid.FLOAT4, Float.toString(x));
    }

    /**
     * Converts a double parameter.
     *
     * @param x the value
     * @return a float8 with the text of Double.toString
     */
    public static ParamValue setDouble(double x) {
        return new ParamValue(Oid.FLOAT8, Double.toString(x));
    }

    /**
     * Converts a BigDecimal parameter.
     *
     * @param x the value, or null
     * @return a numeric with the text of BigDecimal.toString, but with the digits written
     *         out when the scale is negative ("1E+3" is sent as "1000"); SQL NULL for null
     */
    public static ParamValue setBigDecimal(BigDecimal x) {
        if (x == null) {
            return new ParamValue(Oid.NUMERIC, null);
        }
        return new ParamValue(Oid.NUMERIC, x.scale() < 0 ? x.toPlainString() : x.toString());
    }

    /**
     * Converts a String parameter.
     *
     * @param x the value, or null
     * @param context the conversion context
     * @return a varchar, or a value without type for stringtype=unspecified
     */
    public static ParamValue setString(String x, ConversionContext context) {
        return new ParamValue(stringOid(context), x);
    }

    /**
     * Converts a byte array parameter.
     *
     * @param x the value, or null
     * @return a bytea in hex form
     */
    public static ParamValue setBytes(byte[] x) {
        return new ParamValue(Oid.BYTEA, x == null ? null : ByteaText.encode(x));
    }
}
