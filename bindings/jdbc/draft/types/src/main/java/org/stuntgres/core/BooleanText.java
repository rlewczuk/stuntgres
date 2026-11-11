package org.stuntgres.core;

import java.sql.SQLException;
import java.util.Locale;

/** Conversion of text, characters and numbers to boolean (design section 6.1). */
public final class BooleanText {
    private BooleanText() {
    }

    /**
     * Reads a boolean from text: 1, true, t, yes, y, on and 0, false, f, no, n, off, in
     * any case and with white space around.
     *
     * @param text the text
     * @return the boolean
     * @throws SQLException with SQLState 42846 for any other text
     */
    public static boolean parse(String text) throws SQLException {
        switch (text.trim().toLowerCase(Locale.ROOT)) {
            case "1": case "true": case "t": case "yes": case "y": case "on":
                return true;
            case "0": case "false": case "f": case "no": case "n": case "off":
                return false;
            default:
                throw ConversionErrors.notBoolean(text);
        }
    }

    /**
     * Converts a Java value to boolean: a Boolean as it is; a String as {@link #parse}; a
     * Character 1, t, T, y, Y or 0, f, F, n, N; a Number equal to 1 or 0.
     *
     * @param value the value
     * @return the boolean
     * @throws SQLException with SQLState 42846 for any other value
     */
    public static boolean of(Object value) throws SQLException {
        if (value instanceof Boolean) {
            return (Boolean) value;
        }
        if (value instanceof String) {
            return parse((String) value);
        }
        if (value instanceof Character) {
            char c = (Character) value;
            if (c == '1' || c == 't' || c == 'T' || c == 'y' || c == 'Y') {
                return true;
            }
            if (c == '0' || c == 'f' || c == 'F' || c == 'n' || c == 'N') {
                return false;
            }
        } else if (value instanceof Number) {
            double number = ((Number) value).doubleValue();
            if (number == 1.0d) {
                return true;
            }
            if (number == 0.0d) {
                return false;
            }
        }
        throw ConversionErrors.notBoolean(value);
    }
}
