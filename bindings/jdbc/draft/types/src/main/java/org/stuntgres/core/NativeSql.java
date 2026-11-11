package org.stuntgres.core;

import java.sql.SQLException;
import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import org.stuntgres.util.PSQLException;
import org.stuntgres.util.PSQLState;

/**
 * Turns a JDBC statement string into the statements the server receives: placeholders
 * become {@code $n}, the string is split at semicolons, every statement gets a command
 * kind and, on request, a RETURNING clause (design section 3).
 */
public final class NativeSql {
    private final char[] sql;
    private final SqlScanner scanner;
    private final boolean withParameters;
    private final String[] returningColumns;
    private final boolean quoteReturningIdentifiers;
    private final List<NativeStatement> statements = new ArrayList<>();
    private final StringBuilder text = new StringBuilder();

    private int copied;
    private int parameters;
    private int depth;
    private boolean blank = true;
    private int terminatedCount;
    private CommandKind kind = CommandKind.BLANK;
    private boolean returning;
    private CommandKind previousKind = CommandKind.BLANK;
    private boolean previousReturning;
    private boolean insertSeen;
    private boolean beginSeen;
    private boolean atomic;

    private NativeSql(String sql, boolean standardConformingStrings, boolean withParameters,
            String[] returningColumns, boolean quoteReturningIdentifiers) {
        this.sql = sql.toCharArray();
        this.scanner = new SqlScanner(this.sql, standardConformingStrings);
        this.withParameters = withParameters;
        this.returningColumns = returningColumns;
        this.quoteReturningIdentifiers = quoteReturningIdentifiers;
    }

    /**
     * Parses a statement string.
     *
     * @param sql the text after escape processing
     * @param standardConformingStrings the session's standard_conforming_strings setting
     * @param withParameters true for a prepared statement: a question mark becomes a
     *        parameter; false for a plain statement: it stays a question mark
     * @param returningColumns the generated-key columns to return: an empty array for
     *        none, one name starting with an asterisk for all columns, else column names
     * @param quoteReturningIdentifiers whether the column names are quoted when added
     * @return the statements in order; empty when the text holds none
     * @throws SQLException with SQLState 22023 when a column name to quote holds a NUL
     */
    public static List<NativeStatement> parse(String sql, boolean standardConformingStrings,
            boolean withParameters, String[] returningColumns,
            boolean quoteReturningIdentifiers) throws SQLException {
        return new NativeSql(sql, standardConformingStrings, withParameters, returningColumns,
                quoteReturningIdentifiers).run();
    }

    private List<NativeStatement> run() throws SQLException {
        int wordStart = -1;
        int wordLength = 0;
        while (!scanner.atEnd()) {
            int token = scanner.next();
            int at = scanner.start();
            char c = sql[at];
            if (token == SqlScanner.WORD) {
                blank = false;
                wordStart = at;
                wordLength = scanner.end() - at;
                continue;
            }
            if (token != SqlScanner.OTHER) {
                blank = false;
            } else if (c == '?') {
                blank = false;
                placeholder(at);
            } else if (c == ')') {
                blank = false;
                depth--;
            } else if (c == ';') {
                if (!atomic && depth == 0) {
                    endOfStatement(at);
                }
            } else if (!Character.isWhitespace(c)) {
                blank = false;
            }
            // A word is applied after the token that follows it: a semicolon has ended the
            // statement by then, and a closing parenthesis has been counted.
            if (wordStart >= 0) {
                word(wordStart, wordLength, token == SqlScanner.OTHER && c == ')');
                wordStart = -1;
            }
            if (token == SqlScanner.OTHER && c == '(') {
                depth++;
            }
        }
        if (wordStart >= 0) {
            word(wordStart, wordLength, false);
        }
        return finish();
    }

    private void placeholder(int at) {
        text.append(sql, copied, at - copied);
        if (at + 1 < sql.length && sql[at + 1] == '?') {
            text.append('?');
            scanner.seek(at + 2);
        } else if (withParameters) {
            parameters++;
            text.append('$').append(parameters);
        } else {
            text.append('?');
        }
        copied = scanner.position();
    }

    private void endOfStatement(int at) throws SQLException {
        if (!blank) {
            terminatedCount++;
            text.append(sql, copied, at - copied);
            blank = true;
        }
        copied = at + 1;
        if (text.length() > 0) {
            addReturning();
            statements.add(new NativeStatement(text.toString(), parameters, kind, returning));
        }
        previousKind = kind;
        previousReturning = returning;
        kind = CommandKind.BLANK;
        returning = false;
        text.setLength(0);
        parameters = 0;
    }

    private List<NativeStatement> finish() throws SQLException {
        if (copied < sql.length && !blank) {
            text.append(sql, copied, sql.length - copied);
        } else if (terminatedCount > 1) {
            returning = false;
            kind = CommandKind.BLANK;
        } else if (terminatedCount == 1) {
            returning = previousReturning;
            kind = previousKind;
        }
        if (text.length() == 0) {
            return statements;
        }
        addReturning();
        NativeStatement last = new NativeStatement(text.toString(), parameters, kind, returning);
        if (statements.isEmpty()) {
            return Collections.singletonList(last);
        }
        if (!blank) {
            statements.add(last);
        }
        return statements;
    }

    /**
     * Applies a word to the state.
     *
     * @param start the index of the word
     * @param length its length
     * @param closing whether a closing parenthesis follows the word directly
     */
    private void word(int start, int length, boolean closing) {
        if (kind == CommandKind.BLANK) {
            firstKeyword(start, length);
        } else if (kind == CommandKind.WITH) {
            if (depth == 0) {
                afterWith(start, length);
            }
        } else if (kind == CommandKind.CREATE) {
            if (keyword(start, length, "begin", 6)) {
                beginSeen = true;
            } else if (beginSeen) {
                atomic = keyword(start, length, "atomic", 7) || atomic;
                beginSeen = false;
            }
        }
        if (depth == 0 && !closing && keyword(start, length, "returning", 9)) {
            returning = true;
        }
    }

    private void firstKeyword(int start, int length) {
        if (keyword(start, length, "create", 6)) {
            kind = CommandKind.CREATE;
        } else if (keyword(start, length, "alter", 5)) {
            kind = CommandKind.ALTER;
        } else if (keyword(start, length, "update", 6)) {
            kind = CommandKind.UPDATE;
        } else if (keyword(start, length, "delete", 6)) {
            kind = CommandKind.DELETE;
        } else if (keyword(start, length, "move", 4)) {
            kind = CommandKind.MOVE;
        } else if (keyword(start, length, "select", 6)) {
            kind = CommandKind.SELECT;
        } else if (keyword(start, length, "with", 4)) {
            kind = CommandKind.WITH;
        } else if (keyword(start, length, "insert", 7) && !insertSeen && statements.isEmpty()) {
            insertSeen = true;
            kind = CommandKind.INSERT;
        }
    }

    /** A keyword counts only when the text has {@code room} characters from its start. */
    private boolean keyword(int start, int length, String keyword, int room) {
        return start + room <= sql.length && SqlScanner.isKeyword(sql, start, length, keyword);
    }

    private void afterWith(int start, int length) {
        CommandKind promoted;
        if (keyword(start, length, "update", 6)) {
            promoted = CommandKind.UPDATE;
        } else if (keyword(start, length, "delete", 6)) {
            promoted = CommandKind.DELETE;
        } else if (keyword(start, length, "insert", 7)) {
            promoted = CommandKind.INSERT;
        } else if (keyword(start, length, "select", 6)) {
            promoted = CommandKind.SELECT;
        } else {
            return;
        }
        // The word names a query, not the command, when "AS" follows it. The search starts
        // at the last character read so far and passes white space, comments, hyphens and
        // slashes.
        int resume = scanner.position();
        int i = resume - 1;
        while (i < sql.length) {
            char c = sql[i];
            if (c == '-' || c == '/') {
                scanner.seek(i);
                int comment = scanner.next();
                if (comment == SqlScanner.LINE_COMMENT || comment == SqlScanner.BLOCK_COMMENT) {
                    i = scanner.terminated() ? scanner.end() - 1 : sql.length;
                }
            } else if (!Character.isWhitespace(c)) {
                break;
            }
            i++;
        }
        scanner.seek(resume);
        if (i + 2 >= sql.length || !SqlScanner.isKeyword(sql, i, 2, "as")
                || Character.isJavaIdentifierPart(sql[i + 2])) {
            kind = promoted;
        }
    }

    private void addReturning() throws SQLException {
        if (returning || returningColumns.length == 0) {
            return;
        }
        if (kind != CommandKind.INSERT && kind != CommandKind.UPDATE
                && kind != CommandKind.DELETE && kind != CommandKind.WITH) {
            return;
        }
        text.append("\nRETURNING ");
        returning = true;
        if (returningColumns.length == 1 && returningColumns[0].startsWith("*")) {
            text.append('*');
            return;
        }
        for (int i = 0; i < returningColumns.length; i++) {
            String name = returningColumns[i];
            if (i > 0) {
                text.append(", ");
            }
            if (!quoteReturningIdentifiers) {
                text.append(name);
                continue;
            }
            if (name.indexOf('\0') >= 0) {
                throw new PSQLException("A column name must not contain a NUL character.",
                        PSQLState.INVALID_PARAMETER_VALUE);
            }
            text.append('"').append(name.replace("\"", "\"\"")).append('"');
        }
    }
}
