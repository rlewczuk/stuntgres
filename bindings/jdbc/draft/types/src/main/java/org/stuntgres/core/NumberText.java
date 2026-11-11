package org.stuntgres.core;

import java.math.BigDecimal;
import java.math.BigInteger;
import java.sql.SQLException;

/** Reading numbers from column text (design section 6.2). */
public final class NumberText {
    private NumberText() {
    }

    /**
     * Reads digits with an optional leading hyphen and an optional fraction, which is cut
     * off.
     *
     * @return the number, or null when the text has another form, would overflow a long or
     *         is outside the given range
     */
    static Long plainInteger(String text, long min, long max) {
        int length = text.length();
        if (length == 0) {
            return null;
        }
        boolean negative = text.charAt(0) == '-';
        long value = 0;
        int i = negative ? 1 : 0;
        while (i < length) {
            char c = text.charAt(i++);
            if (c == '.') {
                if (length == (negative ? 2 : 1)) {
                    return null;
                }
                while (i < length) {
                    c = text.charAt(i++);
                    if (c < '0' || c > '9') {
                        return null;
                    }
                }
                break;
            }
            if (c < '0' || c > '9' || value < Long.MIN_VALUE / 10) {
                return null;
            }
            value *= 10;
            int digit = c - '0';
            if (value < Long.MIN_VALUE + digit) {
                return null;
            }
            value -= digit;
        }
        if (!negative) {
            if (value == Long.MIN_VALUE) {
                return null;
            }
            value = -value;
        }
        return value < min || value > max ? null : value;
    }

    /**
     * Removes the currency form of the type money: "$1.50" becomes "1.50", "-$1.50"
     * becomes "-1.50" and "($1.50)" becomes "-1.50".
     *
     * @param text the column text
     * @return the text without the currency sign
     */
    public static String withoutCurrency(String text) {
        if (text.length() < 2 || text.charAt(0) > '-') {
            return text;
        }
        char first = text.charAt(0);
        if (first == '(') {
            String inner = text.substring(1);
            if (inner.endsWith(")")) {
                inner = inner.substring(0, inner.length() - 1);
            }
            return "-" + inner.substring(Math.min(1, inner.length()));
        }
        if (first == '$') {
            return text.substring(1);
        }
        if (first == '-' && text.charAt(1) == '$') {
            return "-" + text.substring(2);
        }
        return text;
    }

    /**
     * Reads an integer of a range from text: first as a plain integer, then as a decimal
     * number whose fraction is cut off.
     *
     * @param text the text; white space around it is ignored
     * @param min the smallest value of the Java type
     * @param max the largest value of the Java type
     * @param javaType the name of the Java type for the error message
     * @return the number
     * @throws SQLException with SQLState 22003 when the text is not a number of the range
     */
    public static long integer(String text, long min, long max, String javaType)
            throws SQLException {
        String trimmed = text.trim();
        try {
            long value = Long.parseLong(trimmed);
            if (value >= min && value <= max) {
                return value;
            }
        } catch (NumberFormatException e) {
            // not a plain integer: try a decimal number
        }
        try {
            BigInteger value = new BigDecimal(trimmed).toBigInteger();
            if (value.compareTo(BigInteger.valueOf(min)) >= 0
                    && value.compareTo(BigInteger.valueOf(max)) <= 0) {
                return value.longValue();
            }
        } catch (NumberFormatException e) {
            // reported below
        }
        throw ConversionErrors.badValue(javaType, trimmed);
    }

    /**
     * Reads a BigDecimal from text.
     *
     * @param text the text; white space around it is ignored
     * @return the number with the scale the text has
     * @throws SQLException with SQLState 22003 when the text is not a decimal number
     */
    public static BigDecimal decimal(String text) throws SQLException {
        String trimmed = text.trim();
        try {
            return new BigDecimal(trimmed);
        } catch (NumberFormatException e) {
            throw ConversionErrors.badValue("BigDecimal", trimmed);
        }
    }

    /**
     * Reads a float from text.
     *
     * @param text the text; white space around it is ignored
     * @return the number; NaN, Infinity and -Infinity are read as such
     * @throws SQLException with SQLState 22003 when the text is not a number
     */
    public static float real(String text) throws SQLException {
        String trimmed = text.trim();
        try {
            return Float.parseFloat(trimmed);
        } catch (NumberFormatException e) {
            throw ConversionErrors.badValue("float", trimmed);
        }
    }

    /**
     * Reads a double from text.
     *
     * @param text the text; white space around it is ignored
     * @return the number; NaN, Infinity and -Infinity are read as such
     * @throws SQLException with SQLState 22003 when the text is not a number
     */
    public static double doublePrecision(String text) throws SQLException {
        String trimmed = text.trim();
        try {
            return Double.parseDouble(trimmed);
        } catch (NumberFormatException e) {
            throw ConversionErrors.badValue("double", trimmed);
        }
    }
}
