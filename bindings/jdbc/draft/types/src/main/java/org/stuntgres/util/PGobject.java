package org.stuntgres.util;

import java.io.Serializable;
import java.sql.SQLException;

/**
 * A value of a PostgreSQL type the driver has no Java class for: the type name and the
 * value in the server's text form. The classes for intervals, money and the geometric
 * types extend it.
 */
public class PGobject implements Serializable, Cloneable {
    private static final long serialVersionUID = 1L;

    /** The type name; null until set. */
    protected String type;
    /** The value text; null for SQL NULL. */
    protected String value;

    /** Creates an object without type and value. */
    public PGobject() {
    }

    /**
     * Sets the type name.
     *
     * @param type the PostgreSQL type name
     */
    public final void setType(String type) {
        this.type = type;
    }

    /**
     * Sets the value.
     *
     * @param value the value in the server's text form, or null
     * @throws SQLException when a subclass cannot parse the text
     */
    public void setValue(String value) throws SQLException {
        this.value = value;
    }

    /**
     * Returns the type name.
     *
     * @return the PostgreSQL type name, or null when none was set
     */
    public final String getType() {
        return type;
    }

    /**
     * Returns the value.
     *
     * @return the value in the server's text form, or null
     */
    public String getValue() {
        return value;
    }

    /**
     * Tells whether the object stands for SQL NULL.
     *
     * @return true when the value is null
     */
    public boolean isNull() {
        return getValue() == null;
    }

    /** Two objects are equal when their values are equal; the type is not compared. */
    @Override
    public boolean equals(Object obj) {
        if (!(obj instanceof PGobject)) {
            return false;
        }
        String other = ((PGobject) obj).getValue();
        return other == null ? getValue() == null : other.equals(getValue());
    }

    @Override
    public Object clone() throws CloneNotSupportedException {
        return super.clone();
    }

    /** Returns the value text. */
    @Override
    public String toString() {
        return getValue();
    }

    @Override
    public int hashCode() {
        String text = getValue();
        return text == null ? 0 : text.hashCode();
    }
}
