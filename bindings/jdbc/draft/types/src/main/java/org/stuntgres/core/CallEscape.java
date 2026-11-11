package org.stuntgres.core;

import java.sql.SQLException;
import org.stuntgres.util.PSQLException;
import org.stuntgres.util.PSQLState;

/**
 * The statement of a callable statement: the escape forms <code>{call name(...)}</code> and
 * <code>{? = call name(...)}</code> are rewritten to PostgreSQL text, anything else is kept
 * (design section 4.4).
 */
public final class CallEscape {
    /** How an escape is rewritten. */
    public enum Mode {
        /** Always <code>select * from name(...) as result</code>. */
        SELECT,
        /** <code>call name(...)</code>, but the select form when the escape returns a value. */
        CALL_IF_NO_RETURN,
        /** Always <code>call name(...)</code>. */
        CALL
    }

    private final String sql;
    private final boolean function;

    private CallEscape(String sql, boolean function) {
        this.sql = sql;
        this.function = function;
    }

    /**
     * Returns the statement text, placeholders still written as question marks.
     *
     * @return the rewritten escape, or the given text when it holds no escape
     */
    public String sql() {
        return sql;
    }

    /**
     * Tells whether the statement calls a routine.
     *
     * @return true for an escape and for text that starts with the word CALL
     */
    public boolean isFunction() {
        return function;
    }

    /**
     * Parses the text of a callable statement.
     *
     * @param sql the text as the application wrote it
     * @param standardConformingStrings the session's standard_conforming_strings setting
     * @param mode how an escape is rewritten
     * @return the statement text and whether it calls a routine
     * @throws SQLException with SQLState 2F003 for a malformed escape
     */
    public static CallEscape parse(String sql, boolean standardConformingStrings, Mode mode)
            throws SQLException {
        char[] chars = sql.toCharArray();
        int length = chars.length;
        int i = skipBlank(chars, 0);
        if (i >= length || chars[i] != '{') {
            boolean call = i < length - 5 && sql.regionMatches(true, i, "call", 0, 4)
                    && Character.isWhitespace(chars[i + 4]);
            return new CallEscape(sql, call);
        }
        i = skipSpace(chars, i + 1);
        boolean returns = false;
        if (i < length && chars[i] == '?') {
            returns = true;
            i = skipSpace(chars, i + 1);
            if (i >= length || chars[i] != '=') {
                throw malformed(i);
            }
            i = skipSpace(chars, i + 1);
        }
        if (i + 4 > length || !sql.regionMatches(true, i, "call", 0, 4)) {
            throw malformed(Math.min(i, length));
        }
        i += 4;
        if (i >= length || !Character.isWhitespace(chars[i])) {
            throw malformed(i);
        }
        int start = ++i;
        boolean quoted = false;
        int end = -1;
        while (i < length && end < 0) {
            char c = chars[i];
            if (c == '\'') {
                quoted = !quoted;
            } else if (quoted) {
                if (c == '\\' && !standardConformingStrings) {
                    i++;
                }
            } else if (c == '}') {
                // the first brace outside a string ends the call, a nested escape's too
                end = i;
            } else if (c == ';') {
                throw malformed(i);
            }
            i++;
        }
        if (end < 0) {
            throw malformed(length);
        }
        if (i < length) {
            int rest = skipBlank(chars, i);
            if (rest < length) {
                throw malformed(rest);
            }
        }
        boolean select = mode == Mode.SELECT || (returns && mode == Mode.CALL_IF_NO_RETURN);
        String body = sql.substring(start, end);
        StringBuilder out = new StringBuilder(select ? "select * from " : "call ");
        int prefix = out.length();
        out.append(body);
        int open = body.indexOf('(') + 1;
        if (open == 0) {
            out.append(returns ? "(?)" : "()");
        } else if (returns) {
            int first = skipBlank(chars, start + open);
            out.insert(prefix + open, first < end && chars[first] != ')' ? "?," : "?");
        }
        if (select) {
            out.append(" as result");
        }
        return new CallEscape(out.toString(), true);
    }

    private static PSQLException malformed(int offset) {
        return new PSQLException("The call escape is malformed at offset " + offset + ".",
                PSQLState.STATEMENT_NOT_ALLOWED_IN_FUNCTION_CALL);
    }

    private static int skipSpace(char[] sql, int from) {
        int i = from;
        while (i < sql.length && Character.isWhitespace(sql[i])) {
            i++;
        }
        return i;
    }

    /** Skips white space and comments. */
    private static int skipBlank(char[] sql, int from) {
        SqlScanner scanner = new SqlScanner(sql, true);
        int i = from;
        while (i < sql.length) {
            if (Character.isWhitespace(sql[i])) {
                i++;
                continue;
            }
            scanner.seek(i);
            int token = scanner.next();
            if (token != SqlScanner.LINE_COMMENT && token != SqlScanner.BLOCK_COMMENT) {
                break;
            }
            i = scanner.end();
        }
        return i;
    }
}
