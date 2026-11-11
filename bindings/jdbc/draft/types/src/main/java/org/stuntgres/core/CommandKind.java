package org.stuntgres.core;

/**
 * What the statement parser makes of a statement from its keywords (design section 3.3).
 * A statement none of the keywords identifies is {@link #BLANK}.
 */
public enum CommandKind {
    BLANK, INSERT, UPDATE, DELETE, MOVE, SELECT, WITH, CREATE, ALTER
}
