package org.stuntgres.core;

import java.sql.SQLException;
import java.util.ArrayList;
import java.util.List;
import org.stuntgres.util.PSQLException;
import org.stuntgres.util.PSQLState;

/** Parsing the server's array text, for example <code>{1,NULL,"a b"}</code> (design section 9.1). */
public final class ArrayText {
    private final String text;
    private final char delimiter;
    private int position;

    private ArrayText(String text, char delimiter) {
        this.text = text;
        this.delimiter = delimiter;
    }

    /**
     * Parses array text into its elements.
     *
     * @param text the column text; bounds such as "[0:2]=" before the first brace are
     *        skipped
     * @param delimiter the element delimiter of the element type
     * @return the elements of the outer array: a String for an element, null for an
     *         unquoted NULL, an Object[] for a nested array
     * @throws SQLException with SQLState 22000 when the text is not array text
     */
    public static Object[] parse(String text, char delimiter) throws SQLException {
        ArrayText parser = new ArrayText(text, delimiter);
        if (text.startsWith("[")) {
            parser.position = text.indexOf('=') + 1;
        }
        if (parser.position >= text.length() || text.charAt(parser.position) != '{') {
            throw parser.malformed();
        }
        Object[] elements = parser.array();
        if (parser.position != text.length()) {
            throw parser.malformed();
        }
        return elements;
    }

    private PSQLException malformed() {
        return new PSQLException("Cannot read \"" + text + "\" as an array.", PSQLState.DATA_ERROR);
    }

    private static boolean isSpace(char c) {
        return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == 0x0B;
    }

    /** Parses from an opening brace through its closing brace. */
    private Object[] array() throws SQLException {
        List<Object> elements = new ArrayList<>();
        position++;
        while (true) {
            while (position < text.length() && isSpace(text.charAt(position))) {
                position++;
            }
            if (position >= text.length()) {
                throw malformed();
            }
            char c = text.charAt(position);
            if (c == '{') {
                elements.add(array());
            } else if (c != delimiter && c != '}') {
                element(elements);
            }
            while (position < text.length() && isSpace(text.charAt(position))) {
                position++;
            }
            if (position >= text.length()) {
                throw malformed();
            }
            c = text.charAt(position++);
            if (c == '}') {
                return elements.toArray();
            }
            if (c != delimiter) {
                throw malformed();
            }
        }
    }

    /** Reads one element up to the delimiter or closing brace that follows it. */
    private void element(List<Object> elements) throws SQLException {
        StringBuilder value = new StringBuilder();
        boolean quoted = false;
        boolean inQuotes = false;
        while (true) {
            if (position >= text.length()) {
                throw malformed();
            }
            char c = text.charAt(position);
            if (c == '\\' && position + 1 < text.length()) {
                value.append(text.charAt(position + 1));
                position += 2;
            } else if (c == '"') {
                inQuotes = !inQuotes;
                quoted = true;
                position++;
            } else if (!inQuotes && (c == delimiter || c == '}')) {
                break;
            } else {
                if (inQuotes || !isSpace(c)) {
                    value.append(c);
                }
                position++;
            }
        }
        String element = value.toString();
        elements.add(!quoted && element.equals("NULL") ? null : element);
    }

    /**
     * Returns the number of dimensions of parsed elements.
     *
     * @param elements the result of {@link #parse}
     * @return 1 for a flat or empty array, more for nested arrays
     */
    public static int dimensions(Object[] elements) {
        int dimensions = 1;
        Object[] level = elements;
        while (level.length > 0 && level[0] instanceof Object[]) {
            level = (Object[]) level[0];
            dimensions++;
        }
        return dimensions;
    }
}
