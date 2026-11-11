package org.stuntgres.geometric;

import java.sql.SQLException;
import java.util.ArrayList;
import java.util.List;
import org.stuntgres.util.PSQLException;
import org.stuntgres.util.PSQLState;

/** Text helpers of the geometric value classes. */
final class GeometryText {
    private GeometryText() {
    }

    /**
     * Splits text at the commas that are outside parentheses, brackets and angle brackets.
     * A closing character counts only when it matches the innermost open one. Text that
     * ends with a comma has an empty last piece; empty text has no piece.
     */
    static List<String> split(String text) {
        List<String> pieces = new ArrayList<>();
        StringBuilder open = new StringBuilder();
        int start = 0;
        for (int i = 0; i < text.length(); i++) {
            char c = text.charAt(i);
            if (c == '(' || c == '[' || c == '<') {
                open.append(c);
            } else if (c == ')' || c == ']' || c == '>') {
                char opening = c == ')' ? '(' : c == ']' ? '[' : '<';
                int last = open.length() - 1;
                if (last >= 0 && open.charAt(last) == opening) {
                    open.setLength(last);
                }
            } else if (c == ',' && open.length() == 0) {
                pieces.add(text.substring(start, i));
                start = i + 1;
            }
        }
        if (start < text.length()) {
            pieces.add(text.substring(start));
        } else if (text.endsWith(",")) {
            pieces.add("");
        }
        return pieces;
    }

    /** Removes one opening character from the start and one closing character from the end. */
    static String strip(String text, char opening, char closing) {
        String result = text;
        if (!result.isEmpty() && result.charAt(0) == opening) {
            result = result.substring(1);
        }
        if (!result.isEmpty() && result.charAt(result.length() - 1) == closing) {
            result = result.substring(0, result.length() - 1);
        }
        return result;
    }

    static PSQLException failed(String type, String text, Throwable cause) {
        return new PSQLException("Cannot read \"" + text + "\" as a value of type " + type + ".",
                PSQLState.DATA_TYPE_MISMATCH, cause);
    }

    /** Parses the pieces of a list as points. */
    static PGpoint[] points(List<String> pieces) throws SQLException {
        PGpoint[] points = new PGpoint[pieces.size()];
        for (int i = 0; i < points.length; i++) {
            points[i] = new PGpoint(pieces.get(i));
        }
        return points;
    }

    /** Copies an array of points and the points in it; null stays null. */
    static PGpoint[] copy(PGpoint[] points) throws CloneNotSupportedException {
        if (points == null) {
            return null;
        }
        PGpoint[] copy = points.clone();
        for (int i = 0; i < copy.length; i++) {
            if (copy[i] != null) {
                copy[i] = (PGpoint) copy[i].clone();
            }
        }
        return copy;
    }

    static int hash(double value) {
        long bits = Double.doubleToLongBits(value);
        return (int) (bits ^ (bits >>> 32));
    }
}
