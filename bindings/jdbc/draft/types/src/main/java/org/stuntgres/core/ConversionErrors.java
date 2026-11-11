package org.stuntgres.core;

import java.sql.SQLFeatureNotSupportedException;
import org.stuntgres.util.PSQLException;
import org.stuntgres.util.PSQLState;

/** The exceptions of the conversion code, with their SQLState values (design section 7.9). */
final class ConversionErrors {
    private ConversionErrors() {
    }

    /** A column text that is not a value of the requested Java type: 22003. */
    static PSQLException badValue(String javaType, String value) {
        return new PSQLException("Cannot read the value \"" + value + "\" as " + javaType + ".",
                PSQLState.NUMERIC_VALUE_OUT_OF_RANGE);
    }

    /** A value that is not a boolean: 42846. */
    static PSQLException notBoolean(Object value) {
        return new PSQLException("Cannot convert the value \"" + value + "\" to boolean.",
                PSQLState.CANNOT_COERCE);
    }

    /** A column text that is not a date, time or timestamp: 22007. */
    static PSQLException badDateTime(String what, String value) {
        return new PSQLException("Cannot read the value \"" + value + "\" as " + what + ".",
                PSQLState.BAD_DATETIME_FORMAT);
    }

    /** A getObject class the column type cannot be read as: 22023. */
    static PSQLException unsupportedClass(Class<?> target, String typeName) {
        return new PSQLException("A column of type " + typeName + " cannot be read as "
                + target.getName() + ".", PSQLState.INVALID_PARAMETER_VALUE);
    }

    /** A java.time class or date class the column type cannot be read as: 42821. */
    static PSQLException wrongColumnType(Class<?> target, String typeName) {
        return new PSQLException("A column of type " + typeName + " cannot be read as "
                + target.getName() + ".", PSQLState.DATA_TYPE_MISMATCH);
    }

    /** A parameter value the driver cannot send as the requested type: 07006. */
    static PSQLException badParameter(Object value, String target) {
        return new PSQLException("Cannot send a value of class " + value.getClass().getName()
                + " as " + target + ".", PSQLState.INVALID_PARAMETER_TYPE);
    }

    /** A method the driver does not implement: SQLFeatureNotSupportedException, 0A000. */
    static SQLFeatureNotSupportedException notImplemented(String method) {
        return new SQLFeatureNotSupportedException("The method " + method
                + " is not implemented.", PSQLState.NOT_IMPLEMENTED.getState());
    }
}
