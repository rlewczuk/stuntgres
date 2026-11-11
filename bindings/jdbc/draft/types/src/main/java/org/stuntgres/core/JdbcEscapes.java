package org.stuntgres.core;

import java.sql.SQLException;
import java.util.ArrayList;
import java.util.List;
import org.stuntgres.util.PSQLException;
import org.stuntgres.util.PSQLState;

/**
 * Replaces the JDBC escape sequences of a statement string by PostgreSQL text: the date,
 * time and timestamp literals, the LIKE escape character, outer joins and scalar functions
 * (design section 4). Quoted text and comments are copied unchanged.
 */
public final class JdbcEscapes {
    private static final String[] KEYWORDS = {"d", "t", "ts", "fn", "oj", "escape"};
    private static final String[] REPLACEMENTS = {"DATE ", "TIME ", "TIMESTAMP ", null, null,
        "ESCAPE "};
    private static final int FUNCTION = 3;
    private static final int OUTER_JOIN = 4;

    private final char[] sql;
    private final boolean standardConformingStrings;

    private JdbcEscapes(char[] sql, boolean standardConformingStrings) {
        this.sql = sql;
        this.standardConformingStrings = standardConformingStrings;
    }

    /**
     * Translates every escape sequence of a statement string.
     *
     * @param sql the statement text as the application wrote it
     * @param standardConformingStrings the session's standard_conforming_strings setting
     * @return the text without escape sequences
     * @throws SQLException with SQLState 42601 for a string, quoted identifier,
     *         dollar-quoted string or block comment without an end and for a wrong number
     *         of function arguments; with 0A000 for an interval unit that is not supported
     */
    public static String replace(String sql, boolean standardConformingStrings)
            throws SQLException {
        JdbcEscapes escapes = new JdbcEscapes(sql.toCharArray(), standardConformingStrings);
        StringBuilder out = new StringBuilder(sql.length());
        int i = 0;
        while (i < escapes.sql.length) {
            i = escapes.copy(i, out, false);
            if (i < escapes.sql.length) {
                // a closing parenthesis without an opening one
                out.append(escapes.sql[i]);
                i++;
            }
        }
        return out.toString();
    }

    /**
     * Copies text from {@code from}, translating escapes, until the end of the text, a
     * closing parenthesis that has no opening one in the copied text or, for an argument,
     * a comma outside parentheses.
     *
     * @return the index of the character that stopped the copy, or the text length
     */
    private int copy(int from, StringBuilder out, boolean argument) throws SQLException {
        SqlScanner scanner = new SqlScanner(sql, standardConformingStrings);
        scanner.seek(from);
        int depth = 0;
        while (!scanner.atEnd()) {
            int token = scanner.next();
            int at = scanner.start();
            if (token != SqlScanner.OTHER) {
                if (!scanner.terminated()) {
                    throw unterminated(token, at);
                }
                out.append(sql, at, scanner.end() - at);
                continue;
            }
            char c = sql[at];
            if (c == '(') {
                depth++;
            } else if (c == ')') {
                depth--;
                if (depth < 0) {
                    return at;
                }
            } else if (c == ',' && argument && depth == 0) {
                return at;
            } else if (c == '{') {
                int after = escape(at, out);
                if (after >= 0) {
                    scanner.seek(Math.min(after, sql.length));
                    continue;
                }
            }
            out.append(c);
        }
        return sql.length;
    }

    private PSQLException unterminated(int token, int at) {
        String what = token == SqlScanner.STRING ? "string literal"
                : token == SqlScanner.QUOTED_IDENTIFIER ? "quoted identifier"
                : token == SqlScanner.DOLLAR_STRING ? "dollar-quoted string" : "block comment";
        return new PSQLException("The " + what + " that starts at position " + at
                + " of the statement is not terminated.", PSQLState.SYNTAX_ERROR);
    }

    /**
     * Translates the escape that starts with the brace at {@code brace}.
     *
     * @return the index to continue at, or -1 when the brace starts no escape
     */
    private int escape(int brace, StringBuilder out) throws SQLException {
        for (int k = 0; k < KEYWORDS.length; k++) {
            int body = bodyStart(brace + 1, k);
            if (body < 0) {
                continue;
            }
            if (k == FUNCTION) {
                return function(body, out) + 1;
            }
            if (REPLACEMENTS[k] != null) {
                out.append(REPLACEMENTS[k]);
            }
            int i = body;
            while (i < sql.length && sql[i] != '}') {
                out.append(sql[i++]);
            }
            return i + 1;
        }
        return -1;
    }

    /**
     * Matches an escape keyword at {@code at}: the keyword in either case, any number of
     * spaces, then a character that may start the body.
     *
     * @return the index of that character, or -1
     */
    private int bodyStart(int at, int k) {
        String keyword = KEYWORDS[k];
        int i = at;
        for (int j = 0; j < keyword.length(); j++, i++) {
            if (i >= sql.length || (sql[i] != keyword.charAt(j)
                    && sql[i] != Character.toUpperCase(keyword.charAt(j)))) {
                return -1;
            }
        }
        while (i < sql.length && sql[i] == ' ') {
            i++;
        }
        if (i >= sql.length) {
            return -1;
        }
        char c = sql[i];
        boolean allowed;
        if (k == FUNCTION) {
            allowed = c == '"' || Character.isLetter(c);
        } else if (k == OUTER_JOIN) {
            allowed = c == '"' || c == '(' || Character.isLetter(c);
        } else {
            allowed = c == '\'';
        }
        return allowed ? i : -1;
    }

    /**
     * Translates a function escape whose name starts at {@code from}.
     *
     * @return the index of the closing brace, or the text length when it is missing
     */
    private int function(int from, StringBuilder out) throws SQLException {
        int open = from;
        while (open < sql.length && sql[open] != '(') {
            open++;
        }
        int i = from;
        if (open < sql.length) {
            String name = new String(sql, from, open - from).trim();
            List<CharSequence> arguments = new ArrayList<>(3);
            i = open + 1;
            while (true) {
                StringBuilder argument = new StringBuilder();
                int before = i;
                i = copy(i, argument, true);
                if (i != before) {
                    arguments.add(argument);
                }
                if (i >= sql.length || sql[i] != ',') {
                    break;
                }
                i++;
            }
            if (!EscapeFunctions.append(out, name, arguments)) {
                out.append(name).append('(');
                for (int k = 0; k < arguments.size(); k++) {
                    out.append(k > 0 ? "," : "").append(arguments.get(k));
                }
                out.append(')');
            }
        }
        i++;
        while (i < sql.length && sql[i] != '}') {
            out.append(sql[i++]);
        }
        return i;
    }
}
