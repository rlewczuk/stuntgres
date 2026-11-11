package org.stuntgres.core;

import java.io.BufferedReader;
import java.io.IOException;
import java.io.InputStream;
import java.io.InputStreamReader;
import java.io.UncheckedIOException;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.List;

/**
 * Reads the recorded tables of the design (tab-separated files in the test resources of
 * this package). A cell is escaped: backslash-t, -n, -r, a doubled backslash, backslash-u
 * with four hex digits, and backslash-N alone for SQL NULL.
 */
public final class TestData {
    private TestData() {
    }

    /**
     * Returns the data rows of a table: every line that does not start with '#', split at
     * the tabs, cells still escaped.
     */
    public static List<String[]> rows(String resource) {
        List<String[]> rows = new ArrayList<>();
        for (String line : lines(resource)) {
            if (!line.isEmpty() && line.charAt(0) != '#') {
                rows.add(line.split("\t", -1));
            }
        }
        return rows;
    }

    /** Returns the column names: the first line without its leading '#'. */
    public static String[] header(String resource) {
        return lines(resource).get(0).substring(1).split("\t", -1);
    }

    private static List<String> lines(String resource) {
        InputStream in = TestData.class.getResourceAsStream(resource);
        if (in == null) {
            throw new IllegalStateException("no test resource org/stuntgres/core/" + resource);
        }
        List<String> lines = new ArrayList<>();
        try (BufferedReader reader =
                new BufferedReader(new InputStreamReader(in, StandardCharsets.UTF_8))) {
            for (String line = reader.readLine(); line != null; line = reader.readLine()) {
                lines.add(line);
            }
        } catch (IOException e) {
            throw new UncheckedIOException(e);
        }
        return lines;
    }

    /** Returns the text of a cell; null for the cell backslash-N. */
    public static String unescape(String cell) {
        if (cell.equals("\\N")) {
            return null;
        }
        StringBuilder text = new StringBuilder(cell.length());
        for (int i = 0; i < cell.length(); i++) {
            char c = cell.charAt(i);
            if (c != '\\') {
                text.append(c);
                continue;
            }
            char next = cell.charAt(++i);
            if (next == 't') {
                text.append('\t');
            } else if (next == 'n') {
                text.append('\n');
            } else if (next == 'r') {
                text.append('\r');
            } else if (next == 'u') {
                text.append((char) Integer.parseInt(cell.substring(i + 1, i + 5), 16));
                i += 4;
            } else {
                text.append(next);
            }
        }
        return text.toString();
    }

    /** Escapes text the way the cells are written. */
    public static String escape(String text) {
        if (text == null) {
            return "\\N";
        }
        StringBuilder cell = new StringBuilder(text.length());
        for (int i = 0; i < text.length(); i++) {
            char c = text.charAt(i);
            if (c == '\\') {
                cell.append("\\\\");
            } else if (c == '\t') {
                cell.append("\\t");
            } else if (c == '\n') {
                cell.append("\\n");
            } else if (c == '\r') {
                cell.append("\\r");
            } else if (c < 0x20 || (c >= 0x7f && c < 0xa0) || c == 0x2028 || c == 0x2029
                    || c == 0xfeff || lone(text, i)) {
                cell.append(String.format("\\u%04x", (int) c));
            } else {
                cell.append(c);
            }
        }
        return cell.toString();
    }

    private static boolean lone(String text, int i) {
        char c = text.charAt(i);
        if (Character.isHighSurrogate(c)) {
            return i + 1 >= text.length() || !Character.isLowSurrogate(text.charAt(i + 1));
        }
        return Character.isLowSurrogate(c)
                && (i == 0 || !Character.isHighSurrogate(text.charAt(i - 1)));
    }
}
