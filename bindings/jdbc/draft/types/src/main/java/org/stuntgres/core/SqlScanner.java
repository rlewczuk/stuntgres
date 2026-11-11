package org.stuntgres.core;

/**
 * Splits PostgreSQL statement text into the pieces the driver has to tell apart: quoted
 * text, comments, words and single characters. It never fails: a quoted piece or block
 * comment without an end runs to the end of the text and reports {@link #terminated()}
 * false. The rules are those of design section 2 (they reproduce what the PostgreSQL JDBC
 * driver recognizes, not the server's grammar).
 */
public final class SqlScanner {
    /** One character that belongs to no other kind (operators, digits, white space). */
    public static final int OTHER = 0;
    /** A run of word characters: a keyword or an unquoted identifier. */
    public static final int WORD = 1;
    /** A single-quoted string, quotes included. */
    public static final int STRING = 2;
    /** A double-quoted identifier, quotes included. */
    public static final int QUOTED_IDENTIFIER = 3;
    /** A dollar-quoted string, both tags included. */
    public static final int DOLLAR_STRING = 4;
    /** A comment from two hyphens up to and including the first line end. */
    public static final int LINE_COMMENT = 5;
    /** A block comment, nested comments included. */
    public static final int BLOCK_COMMENT = 6;

    private static final String IDENTIFIER_END = ",()[].;:+-*/%^<>=~!@#&|`?";

    private final char[] sql;
    private final boolean standardConformingStrings;
    private int position;
    private int start;
    private boolean terminated = true;

    /**
     * Creates a scanner at the start of the text.
     *
     * @param sql the statement text; it is not copied and must not change
     * @param standardConformingStrings the session's standard_conforming_strings setting
     */
    public SqlScanner(char[] sql, boolean standardConformingStrings) {
        this.sql = sql;
        this.standardConformingStrings = standardConformingStrings;
    }

    /**
     * Tells whether a character can start a word.
     *
     * @param c the character
     * @return true for a Java identifier start character other than the dollar sign
     */
    public static boolean isWordStart(char c) {
        return c != '$' && Character.isJavaIdentifierStart(c);
    }

    /**
     * Tells whether a character can continue a word.
     *
     * @param c the character
     * @return true for a Java identifier part character other than the dollar sign
     */
    public static boolean isWordPart(char c) {
        return c != '$' && Character.isJavaIdentifierPart(c);
    }

    /**
     * Compares a piece of text with a lower-case ASCII keyword, ignoring ASCII case.
     *
     * @param sql the text
     * @param offset the index of the first character
     * @param length the number of characters of the piece
     * @param keyword the keyword in lower case
     * @return true when the piece has the keyword's length and letters
     */
    public static boolean isKeyword(char[] sql, int offset, int length, String keyword) {
        if (length != keyword.length() || offset + length > sql.length) {
            return false;
        }
        for (int i = 0; i < length; i++) {
            if ((sql[offset + i] | 0x20) != keyword.charAt(i)) {
                return false;
            }
        }
        return true;
    }

    /**
     * Returns where the next token starts.
     *
     * @return an index into the text; the text length at the end
     */
    public int position() {
        return position;
    }

    /**
     * Continues at another place of the text.
     *
     * @param newPosition the index of the first character of the next token
     */
    public void seek(int newPosition) {
        position = newPosition;
    }

    /**
     * Tells whether the whole text has been read.
     *
     * @return true when no token is left
     */
    public boolean atEnd() {
        return position >= sql.length;
    }

    /**
     * Reads one token. Call only while {@link #atEnd()} is false.
     *
     * @return the kind of the token: one of the constants of this class
     */
    public int next() {
        start = position;
        terminated = true;
        char c = sql[start];
        int kind = OTHER;
        int end = start + 1;
        if (c == '\'') {
            kind = STRING;
            end = stringEnd();
        } else if (c == '"') {
            kind = QUOTED_IDENTIFIER;
            end = closing('"', start + 1);
        } else if (c == '$') {
            int tagEnd = dollarTagEnd();
            if (tagEnd > 0) {
                kind = DOLLAR_STRING;
                end = dollarEnd(tagEnd);
            }
        } else if (c == '-' && start + 1 < sql.length && sql[start + 1] == '-') {
            kind = LINE_COMMENT;
            end = start + 2;
            while (end < sql.length && sql[end - 1] != '\r' && sql[end - 1] != '\n') {
                end++;
            }
        } else if (c == '/' && start + 1 < sql.length && sql[start + 1] == '*') {
            kind = BLOCK_COMMENT;
            end = blockCommentEnd();
        } else if (isWordStart(c)) {
            kind = WORD;
            while (end < sql.length && isWordPart(sql[end])) {
                end++;
            }
        }
        position = end;
        return kind;
    }

    /**
     * Returns where the token read last starts.
     *
     * @return the index of its first character
     */
    public int start() {
        return start;
    }

    /**
     * Returns where the token read last ends.
     *
     * @return the index after its last character
     */
    public int end() {
        return position;
    }

    /**
     * Tells whether the token read last is complete.
     *
     * @return false for a string, quoted identifier, dollar-quoted string or block comment
     *         whose end is missing
     */
    public boolean terminated() {
        return terminated;
    }

    private int closing(char quote, int from) {
        for (int i = from; i < sql.length; i++) {
            if (sql[i] == quote) {
                return i + 1;
            }
        }
        terminated = false;
        return sql.length;
    }

    private int stringEnd() {
        boolean backslash = !standardConformingStrings;
        if (!backslash && start >= 2 && (sql[start - 1] == 'e' || sql[start - 1] == 'E')) {
            char before = sql[start - 2];
            backslash = before == '"' || before == ' ' || before == '\t' || before == '\n'
                    || before == '\r' || before == '\f' || IDENTIFIER_END.indexOf(before) >= 0;
        }
        if (!backslash) {
            return closing('\'', start + 1);
        }
        for (int i = start + 1; i < sql.length; i++) {
            if (sql[i] == '\\') {
                i++;
            } else if (sql[i] == '\'') {
                return i + 1;
            }
        }
        terminated = false;
        return sql.length;
    }

    /** Returns the index of the dollar sign that ends the opening tag, or 0 for none. */
    private int dollarTagEnd() {
        if (start + 1 >= sql.length
                || (start > 0 && Character.isJavaIdentifierPart(sql[start - 1]))) {
            return 0;
        }
        if (sql[start + 1] == '$') {
            return start + 1;
        }
        if (!isWordStart(sql[start + 1])) {
            return 0;
        }
        for (int i = start + 2; i < sql.length; i++) {
            if (sql[i] == '$') {
                return i;
            }
            if (!isWordPart(sql[i])) {
                return 0;
            }
        }
        return 0;
    }

    private int dollarEnd(int tagEnd) {
        int tagLength = tagEnd - start + 1;
        for (int i = tagEnd + 1; i + tagLength <= sql.length; i++) {
            if (sql[i] == '$' && sameTag(i, tagLength)) {
                return i + tagLength;
            }
        }
        terminated = false;
        return sql.length;
    }

    private boolean sameTag(int at, int tagLength) {
        for (int i = 0; i < tagLength; i++) {
            if (sql[start + i] != sql[at + i]) {
                return false;
            }
        }
        return true;
    }

    private int blockCommentEnd() {
        int level = 1;
        int i = start + 2;
        while (i < sql.length) {
            if (sql[i - 1] == '*' && sql[i] == '/') {
                level--;
                if (level == 0) {
                    return i + 1;
                }
                i += 2;
            } else if (sql[i - 1] == '/' && sql[i] == '*') {
                level++;
                i += 2;
            } else {
                i++;
            }
        }
        terminated = false;
        return sql.length;
    }
}
