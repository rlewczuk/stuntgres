package org.stuntgres.core;

import java.lang.reflect.Array;
import java.sql.SQLException;
import org.stuntgres.util.PSQLException;
import org.stuntgres.util.PSQLState;

/** Writing a Java array as PostgreSQL array text (design section 9.3). */
public final class ArrayFormat {
    private ArrayFormat() {
    }

    /**
     * Formats a Java array, of objects or of primitives, of any number of dimensions.
     * An element is written in double quotes with backslash and quote escaped by a
     * backslash; a null element as NULL; a boolean as 1 or 0; a byte array as a quoted
     * bytea in hex form; any other element with its toString text.
     *
     * @param array the array
     * @param delimiter the element delimiter of the element type
     * @return the text, for example <code>{"1",NULL,"a b"}</code>
     * @throws SQLException with SQLState 07006 when the value is not an array
     */
    public static String format(Object array, char delimiter) throws SQLException {
        if (array == null || !array.getClass().isArray() || array instanceof byte[]) {
            throw new PSQLException("The elements of an array must be given as a Java array.",
                    PSQLState.INVALID_PARAMETER_TYPE);
        }
        StringBuilder out = new StringBuilder();
        append(out, array, delimiter);
        return out.toString();
    }

    private static void append(StringBuilder out, Object array, char delimiter) {
        out.append('{');
        int length = Array.getLength(array);
        for (int i = 0; i < length; i++) {
            if (i > 0) {
                out.append(delimiter);
            }
            Object element = Array.get(array, i);
            if (element == null) {
                out.append("NULL");
            } else if (element instanceof byte[]) {
                quote(out, ByteaText.encode((byte[]) element));
            } else if (element.getClass().isArray()) {
                append(out, element, delimiter);
            } else if (element instanceof Boolean) {
                out.append((Boolean) element ? '1' : '0');
            } else {
                quote(out, element.toString());
            }
        }
        out.append('}');
    }

    private static void quote(StringBuilder out, String text) {
        out.append('"');
        for (int i = 0; i < text.length(); i++) {
            char c = text.charAt(i);
            if (c == '"' || c == '\\') {
                out.append('\\');
            }
            out.append(c);
        }
        out.append('"');
    }

    /**
     * Returns the array type for the Java arrays setObject accepts without a type name.
     *
     * @param array a Java array
     * @return the OID of int2[], int4[], int8[], float4[], float8[], bool[], varchar[],
     *         bytea[], numeric[] or uuid[] by the innermost component class (primitive or
     *         wrapper, String, byte[], BigDecimal, UUID); 0 for any other component class
     */
    public static int arrayOid(Object array) {
        Class<?> component = array.getClass().getComponentType();
        while (component.isArray() && component != byte[].class) {
            component = component.getComponentType();
        }
        if (component == int.class || component == Integer.class) {
            return Oid.INT4_ARRAY;
        } else if (component == long.class || component == Long.class) {
            return Oid.INT8_ARRAY;
        } else if (component == short.class || component == Short.class) {
            return Oid.INT2_ARRAY;
        } else if (component == float.class || component == Float.class) {
            return Oid.FLOAT4_ARRAY;
        } else if (component == double.class || component == Double.class) {
            return Oid.FLOAT8_ARRAY;
        } else if (component == boolean.class || component == Boolean.class) {
            return Oid.BOOL_ARRAY;
        } else if (component == String.class) {
            return Oid.VARCHAR_ARRAY;
        } else if (component == byte[].class) {
            return Oid.BYTEA_ARRAY;
        } else if (component == java.math.BigDecimal.class) {
            return Oid.NUMERIC_ARRAY;
        } else if (component == java.util.UUID.class) {
            return Oid.UUID_ARRAY;
        }
        return 0;
    }
}
