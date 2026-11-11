package org.stuntgres.core;

import java.io.InputStream;
import java.io.Reader;
import java.lang.reflect.Proxy;
import java.math.BigDecimal;
import java.sql.Array;
import java.sql.Date;
import java.sql.SQLException;
import java.sql.SQLType;
import java.sql.SQLXML;
import java.sql.Time;
import java.sql.Timestamp;
import java.sql.Types;
import java.util.Calendar;

/**
 * The setters of java.sql.PreparedStatement, each mapped to the conversion function that
 * design section 8 names for it. The parameter index is ignored; the result of the last
 * call is in {@link #value}.
 */
final class SetterCalls {
    final ConversionContext context;
    ParamValue value;

    SetterCalls(ConversionContext context) {
        this.context = context;
    }

    void setNull(int i, int sqlType) throws SQLException {
        value = BasicSetters.setNull(sqlType, context);
    }

    void setNull(int i, int sqlType, String typeName) throws SQLException {
        value = BasicSetters.setNull(sqlType, typeName, context);
    }

    void setBoolean(int i, boolean x) {
        value = BasicSetters.setBoolean(x);
    }

    void setByte(int i, byte x) {
        value = BasicSetters.setShort(x);
    }

    void setShort(int i, short x) {
        value = BasicSetters.setShort(x);
    }

    void setInt(int i, int x) {
        value = BasicSetters.setInt(x);
    }

    void setLong(int i, long x) {
        value = BasicSetters.setLong(x);
    }

    void setFloat(int i, float x) {
        value = BasicSetters.setFloat(x);
    }

    void setDouble(int i, double x) {
        value = BasicSetters.setDouble(x);
    }

    void setBigDecimal(int i, BigDecimal x) {
        value = BasicSetters.setBigDecimal(x);
    }

    void setString(int i, String x) {
        value = BasicSetters.setString(x, context);
    }

    void setNString(int i, String x) throws SQLException {
        throw ConversionErrors.notImplemented("setNString(int, String)");
    }

    void setBytes(int i, byte[] x) {
        value = BasicSetters.setBytes(x);
    }

    void setDate(int i, Date x) {
        value = DateTimeSetters.setDate(x, null, context);
    }

    void setDate(int i, Date x, Calendar cal) {
        value = DateTimeSetters.setDate(x, cal, context);
    }

    void setTime(int i, Time x) {
        value = DateTimeSetters.setTime(x, null, context);
    }

    void setTime(int i, Time x, Calendar cal) {
        value = DateTimeSetters.setTime(x, cal, context);
    }

    void setTimestamp(int i, Timestamp x) {
        value = DateTimeSetters.setTimestamp(x, null, context);
    }

    void setTimestamp(int i, Timestamp x, Calendar cal) {
        value = DateTimeSetters.setTimestamp(x, cal, context);
    }

    void setObject(int i, Object x) throws SQLException {
        value = ObjectSetters.setObject(x, context);
    }

    void setObject(int i, Object x, int targetSqlType) throws SQLException {
        value = ObjectSetters.setObject(x, targetSqlType, -1, context);
    }

    void setObject(int i, Object x, int targetSqlType, int scale) throws SQLException {
        value = ObjectSetters.setObject(x, targetSqlType, scale, context);
    }

    void setObject(int i, Object x, SQLType targetSqlType) throws SQLException {
        throw ConversionErrors.notImplemented("setObject(int, Object, SQLType)");
    }

    void setObject(int i, Object x, SQLType targetSqlType, int scale) throws SQLException {
        throw ConversionErrors.notImplemented("setObject(int, Object, SQLType, int)");
    }

    void setArray(int i, Array x) throws SQLException {
        value = x == null ? BasicSetters.setNull(Types.ARRAY, context)
                : ObjectSetters.setObject(x, context);
    }

    void setSQLXML(int i, SQLXML x) throws SQLException {
        value = x == null ? BasicSetters.setNull(Types.SQLXML, context)
                : ObjectSetters.setObject(x, context);
    }

    void setURL(int i, java.net.URL x) throws SQLException {
        throw ConversionErrors.notImplemented("setURL(int, URL)");
    }

    void setRef(int i, java.sql.Ref x) throws SQLException {
        throw ConversionErrors.notImplemented("setRef(int, Ref)");
    }

    void setRowId(int i, java.sql.RowId x) throws SQLException {
        throw ConversionErrors.notImplemented("setRowId(int, RowId)");
    }

    void setClob(int i, Reader x) throws SQLException {
        throw ConversionErrors.notImplemented("setClob(int, Reader)");
    }

    void setNClob(int i, Reader x) throws SQLException {
        throw ConversionErrors.notImplemented("setNClob(int, Reader)");
    }

    void setAsciiStream(int i, InputStream x) throws SQLException {
        throw ConversionErrors.notImplemented("setAsciiStream(int, InputStream)");
    }

    void setAsciiStream(int i, InputStream x, int length) throws SQLException {
        value = StreamSetters.setAsciiStream(x, length, context);
    }

    void setAsciiStream(int i, InputStream x, long length) throws SQLException {
        throw ConversionErrors.notImplemented("setAsciiStream(int, InputStream, long)");
    }

    void setUnicodeStream(int i, InputStream x, int length) throws SQLException {
        value = StreamSetters.setUnicodeStream(x, length, context);
    }

    void setBinaryStream(int i, InputStream x) throws SQLException {
        value = StreamSetters.setBinaryStream(x, -1);
    }

    void setBinaryStream(int i, InputStream x, int length) throws SQLException {
        value = StreamSetters.setBinaryStream(x, length < 0 ? -2 : length);
    }

    void setBinaryStream(int i, InputStream x, long length) throws SQLException {
        value = StreamSetters.setBinaryStream(x, length < 0 ? -2 : length);
    }

    void setCharacterStream(int i, Reader x) throws SQLException {
        value = StreamSetters.setCharacterStream(x, context);
    }

    void setCharacterStream(int i, Reader x, int length) throws SQLException {
        value = StreamSetters.setCharacterStream(x, length, context);
    }

    void setCharacterStream(int i, Reader x, long length) throws SQLException {
        throw ConversionErrors.notImplemented("setCharacterStream(int, Reader, long)");
    }

    void setNCharacterStream(int i, Reader x) throws SQLException {
        throw ConversionErrors.notImplemented("setNCharacterStream(int, Reader)");
    }

    void setNCharacterStream(int i, Reader x, long length) throws SQLException {
        throw ConversionErrors.notImplemented("setNCharacterStream(int, Reader, long)");
    }

    /** A java.sql.Array as Connection.createArrayOf gives it: type name and text. */
    Array createArrayOf(String typeName, Object elements) throws SQLException {
        // fails for a type name without an array type
        ObjectSetters.setArray(String.valueOf(typeName), null, context);
        String text = elements == null ? null : ArrayFormat.format(elements, ',');
        return (Array) Proxy.newProxyInstance(Array.class.getClassLoader(),
            new Class<?>[] {Array.class}, (proxy, method, args) -> {
                if (method.getName().equals("getBaseTypeName")) {
                    return typeName;
                }
                if (method.getName().equals("toString")) {
                    return text;
                }
                throw new UnsupportedOperationException(method.getName());
            });
    }

    /** A java.sql.SQLXML that holds a string. */
    SQLXML createSQLXML() {
        String[] holder = new String[1];
        return (SQLXML) Proxy.newProxyInstance(SQLXML.class.getClassLoader(),
            new Class<?>[] {SQLXML.class}, (proxy, method, args) -> {
                if (method.getName().equals("setString")) {
                    holder[0] = (String) args[0];
                    return null;
                }
                if (method.getName().equals("getString")) {
                    return holder[0];
                }
                throw new UnsupportedOperationException(method.getName());
            });
    }
}
