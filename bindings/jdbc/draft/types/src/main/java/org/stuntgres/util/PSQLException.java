package org.stuntgres.util;

import java.sql.SQLException;

/**
 * The exception the driver raises. This is the first form of the class: it carries a
 * message and a SQLState. The part of JDBC-DESIGN-CORE that maps server errors replaces
 * this file with the complete class (server error fields).
 */
public class PSQLException extends SQLException {
    private static final long serialVersionUID = 1L;

    /**
     * Creates an exception raised by the driver itself.
     *
     * @param msg the message
     * @param state the SQLState, or null for none
     */
    public PSQLException(String msg, PSQLState state) {
        super(msg, state == null ? null : state.getState());
    }

    /**
     * Creates an exception raised by the driver itself with a cause.
     *
     * @param msg the message
     * @param state the SQLState, or null for none
     * @param cause the cause, or null
     */
    public PSQLException(String msg, PSQLState state, Throwable cause) {
        super(msg, state == null ? null : state.getState(), cause);
    }
}
