package org.stuntgres.core;

/** One statement of a statement string, in the form the server receives. */
public final class NativeStatement {
    private final String sql;
    private final int parameterCount;
    private final CommandKind kind;
    private final boolean returning;

    NativeStatement(String sql, int parameterCount, CommandKind kind, boolean returning) {
        this.sql = sql;
        this.parameterCount = parameterCount;
        this.kind = kind;
        this.returning = returning;
    }

    /**
     * Returns the statement text with {@code $1}, {@code $2}, ... in place of the
     * placeholders.
     *
     * @return the text to send
     */
    public String sql() {
        return sql;
    }

    /**
     * Returns the number of placeholders of this statement.
     *
     * @return zero or more
     */
    public int parameterCount() {
        return parameterCount;
    }

    /**
     * Returns the command kind.
     *
     * @return never null
     */
    public CommandKind kind() {
        return kind;
    }

    /**
     * Tells whether the statement has a RETURNING clause, written by the caller or added
     * for generated keys.
     *
     * @return true when the parser saw or added one
     */
    public boolean returning() {
        return returning;
    }

    /**
     * Tells whether the parser expects rows from this statement.
     *
     * @return true for a RETURNING clause and for the kinds SELECT and WITH
     */
    public boolean returnsRows() {
        return returning || kind == CommandKind.SELECT || kind == CommandKind.WITH;
    }
}
