package org.stuntgres.util;

/**
 * SQLState values the driver raises itself. The constant names and codes are those of the
 * PostgreSQL JDBC driver's class of the same name, so that code written against it compiles
 * after a change of the package name.
 */
public enum PSQLState {
    UNKNOWN_STATE(""),
    TOO_MANY_RESULTS("0100E"),
    NO_DATA("02000"),
    INVALID_PARAMETER_TYPE("07006"),
    CONNECTION_UNABLE_TO_CONNECT("08001"),
    CONNECTION_DOES_NOT_EXIST("08003"),
    CONNECTION_REJECTED("08004"),
    CONNECTION_FAILURE("08006"),
    CONNECTION_FAILURE_DURING_TRANSACTION("08007"),
    PROTOCOL_VIOLATION("08P01"),
    COMMUNICATION_ERROR("08S01"),
    NOT_IMPLEMENTED("0A000"),
    DATA_ERROR("22000"),
    STRING_DATA_RIGHT_TRUNCATION("22001"),
    NUMERIC_VALUE_OUT_OF_RANGE("22003"),
    BAD_DATETIME_FORMAT("22007"),
    DATETIME_OVERFLOW("22008"),
    DIVISION_BY_ZERO("22012"),
    MOST_SPECIFIC_TYPE_DOES_NOT_MATCH("2200G"),
    INVALID_PARAMETER_VALUE("22023"),
    NOT_NULL_VIOLATION("23502"),
    FOREIGN_KEY_VIOLATION("23503"),
    UNIQUE_VIOLATION("23505"),
    CHECK_VIOLATION("23514"),
    EXCLUSION_VIOLATION("23P01"),
    INVALID_CURSOR_STATE("24000"),
    TRANSACTION_STATE_INVALID("25000"),
    ACTIVE_SQL_TRANSACTION("25001"),
    NO_ACTIVE_SQL_TRANSACTION("25P01"),
    IN_FAILED_SQL_TRANSACTION("25P02"),
    INVALID_SQL_STATEMENT_NAME("26000"),
    INVALID_AUTHORIZATION_SPECIFICATION("28000"),
    INVALID_PASSWORD("28P01"),
    INVALID_TRANSACTION_TERMINATION("2D000"),
    STATEMENT_NOT_ALLOWED_IN_FUNCTION_CALL("2F003"),
    INVALID_SAVEPOINT_SPECIFICATION("3B000"),
    SERIALIZATION_FAILURE("40001"),
    DEADLOCK_DETECTED("40P01"),
    SYNTAX_ERROR("42601"),
    UNDEFINED_COLUMN("42703"),
    UNDEFINED_OBJECT("42704"),
    WRONG_OBJECT_TYPE("42809"),
    NUMERIC_CONSTANT_OUT_OF_RANGE("42820"),
    DATA_TYPE_MISMATCH("42821"),
    UNDEFINED_FUNCTION("42883"),
    INVALID_NAME("42602"),
    DATATYPE_MISMATCH("42804"),
    CANNOT_COERCE("42846"),
    UNDEFINED_TABLE("42P01"),
    OUT_OF_MEMORY("53200"),
    OBJECT_NOT_IN_STATE("55000"),
    OBJECT_IN_USE("55006"),
    QUERY_CANCELED("57014"),
    SYSTEM_ERROR("60000"),
    IO_ERROR("58030"),
    UNEXPECTED_ERROR("99999");

    private final String state;

    PSQLState(String state) {
        this.state = state;
    }

    /**
     * Returns the five-character SQLState code.
     *
     * @return the code; empty for {@link #UNKNOWN_STATE}
     */
    public String getState() {
        return state;
    }

    /**
     * Tells whether a SQLState reports a lost or refused connection.
     *
     * @param psqlState a SQLState code
     * @return true for 08001, 08003, 08004, 08006 and 08007
     */
    public static boolean isConnectionError(String psqlState) {
        return CONNECTION_UNABLE_TO_CONNECT.state.equals(psqlState)
                || CONNECTION_DOES_NOT_EXIST.state.equals(psqlState)
                || CONNECTION_REJECTED.state.equals(psqlState)
                || CONNECTION_FAILURE.state.equals(psqlState)
                || CONNECTION_FAILURE_DURING_TRANSACTION.state.equals(psqlState);
    }
}
