package org.stuntgres.core;

import java.sql.SQLException;

/**
 * The conversions behind ResultSet.getBoolean, getByte, getShort, getInt and getLong
 * (design section 7.2). The value is the column text and is never null: for SQL NULL the
 * caller returns false or 0 without calling these methods.
 */
public final class IntegerGetters {
    private IntegerGetters() {
    }

    /**
     * Reads a column as boolean.
     *
     * @param value the column text
     * @param oid the type OID of the column
     * @return for a bool column whether the text is "t"; else as {@link BooleanText#parse}
     * @throws SQLException with SQLState 42846 when the text is not a boolean
     */
    public static boolean getBoolean(String value, int oid) throws SQLException {
        return oid == Oid.BOOL ? value.equals("t") : BooleanText.parse(value);
    }

    /**
     * Reads a column as byte.
     *
     * @param value the column text
     * @param oid the type OID of the column
     * @param context the conversion context
     * @return the number; 0 for text that is empty or white space
     * @throws SQLException with SQLState 22003 when the text is not a number of the range
     */
    public static byte getByte(String value, int oid, ConversionContext context)
            throws SQLException {
        Long plain = NumberText.plainInteger(value, Byte.MIN_VALUE, Byte.MAX_VALUE);
        if (plain != null) {
            return plain.byteValue();
        }
        String trimmed = value.trim();
        if (trimmed.isEmpty()) {
            return 0;
        }
        try {
            return Byte.parseByte(trimmed);
        } catch (NumberFormatException e) {
            int bool = booleanAsNumber(trimmed, oid, context);
            if (bool >= 0) {
                return (byte) bool;
            }
            return (byte) NumberText.integer(trimmed, Byte.MIN_VALUE, Byte.MAX_VALUE, "byte");
        }
    }

    /**
     * Reads a column as short.
     *
     * @param value the column text
     * @param oid the type OID of the column
     * @param context the conversion context
     * @return the number
     * @throws SQLException with SQLState 22003 when the text is not a number of the range
     */
    public static short getShort(String value, int oid, ConversionContext context)
            throws SQLException {
        return (short) integer(value, oid, context, Short.MIN_VALUE, Short.MAX_VALUE, "short");
    }

    /**
     * Reads a column as int.
     *
     * @param value the column text
     * @param oid the type OID of the column
     * @param context the conversion context
     * @return the number
     * @throws SQLException with SQLState 22003 when the text is not a number of the range
     */
    public static int getInt(String value, int oid, ConversionContext context)
            throws SQLException {
        return (int) integer(value, oid, context, Integer.MIN_VALUE, Integer.MAX_VALUE, "int");
    }

    /**
     * Reads a column as long.
     *
     * @param value the column text
     * @param oid the type OID of the column
     * @param context the conversion context
     * @return the number
     * @throws SQLException with SQLState 22003 when the text is not a number of the range
     */
    public static long getLong(String value, int oid, ConversionContext context)
            throws SQLException {
        return integer(value, oid, context, Long.MIN_VALUE, Long.MAX_VALUE, "long");
    }

    private static long integer(String value, int oid, ConversionContext context, long min,
            long max, String javaType) throws SQLException {
        Long plain = NumberText.plainInteger(value, min, max);
        if (plain != null) {
            return plain;
        }
        String number = NumberText.withoutCurrency(value);
        int bool = booleanAsNumber(number, oid, context);
        if (bool >= 0) {
            return bool;
        }
        return NumberText.integer(number, min, max, javaType);
    }

    /**
     * Returns 1 or 0 for the text "t" or "f" of a bool column when the connection property
     * convertBooleanToNumeric is on, else -1.
     */
    static int booleanAsNumber(String value, int oid, ConversionContext context) {
        if (!context.convertBooleanToNumeric() || oid != Oid.BOOL) {
            return -1;
        }
        String trimmed = value.trim();
        return trimmed.equals("t") ? 1 : trimmed.equals("f") ? 0 : -1;
    }
}
