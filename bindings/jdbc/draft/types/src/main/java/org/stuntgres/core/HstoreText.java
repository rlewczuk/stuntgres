package org.stuntgres.core;

import java.util.LinkedHashMap;
import java.util.Map;

/** The text form of the extension type hstore (design section 6.9). */
public final class HstoreText {
    private HstoreText() {
    }

    /**
     * Parses hstore text: pairs "key"=>"value" or "key"=>NULL, separated by commas; inside
     * the quotes a backslash escapes the next character.
     *
     * @param text the column text
     * @return the pairs in the order of the text; a NULL value is a null
     */
    public static Map<String, String> parse(String text) {
        Map<String, String> map = new LinkedHashMap<>();
        int[] position = {0};
        while (true) {
            int quote = text.indexOf('"', position[0]);
            if (quote < 0) {
                return map;
            }
            position[0] = quote;
            String key = quoted(text, position);
            position[0] = text.indexOf("=>", position[0]) + 2;
            while (position[0] < text.length() && text.charAt(position[0]) == ' ') {
                position[0]++;
            }
            if (text.startsWith("NULL", position[0])) {
                map.put(key, null);
                position[0] += 4;
            } else {
                map.put(key, quoted(text, position));
            }
        }
    }

    /** Reads the quoted string that starts at the position and moves behind it. */
    private static String quoted(String text, int[] position) {
        StringBuilder value = new StringBuilder();
        int i = position[0] + 1;
        while (i < text.length() && text.charAt(i) != '"') {
            if (text.charAt(i) == '\\' && i + 1 < text.length()) {
                i++;
            }
            value.append(text.charAt(i++));
        }
        position[0] = i + 1;
        return value.toString();
    }

    /**
     * Formats a map as hstore text.
     *
     * @param map the pairs; keys and values are converted with toString, a null value
     *        becomes NULL, a null key the text NULL
     * @return "key"=>"value" pairs separated by ", ", quotes and backslashes escaped by a
     *         backslash; empty text for an empty map
     */
    public static String format(Map<?, ?> map) {
        StringBuilder text = new StringBuilder();
        for (Map.Entry<?, ?> entry : map.entrySet()) {
            if (text.length() > 0) {
                text.append(", ");
            }
            append(text, entry.getKey());
            text.append("=>");
            append(text, entry.getValue());
        }
        return text.toString();
    }

    private static void append(StringBuilder text, Object value) {
        if (value == null) {
            text.append("NULL");
            return;
        }
        text.append('"');
        String s = value.toString();
        for (int i = 0; i < s.length(); i++) {
            char c = s.charAt(i);
            if (c == '"' || c == '\\') {
                text.append('\\');
            }
            text.append(c);
        }
        text.append('"');
    }
}
