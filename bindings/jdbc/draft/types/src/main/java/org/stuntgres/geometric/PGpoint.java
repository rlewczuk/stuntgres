package org.stuntgres.geometric;

import java.sql.SQLException;
import java.util.List;
import org.stuntgres.util.PGobject;

/** A value of the PostgreSQL type point. */
public class PGpoint extends PGobject {
    private static final long serialVersionUID = 1L;

    /** The X coordinate. */
    public double x;
    /** The Y coordinate. */
    public double y;
    /** Whether the object stands for SQL NULL. */
    public boolean isNull;

    /**
     * Creates a point.
     *
     * @param x the X coordinate
     * @param y the Y coordinate
     */
    public PGpoint(double x, double y) {
        this();
        this.x = x;
        this.y = y;
    }

    /**
     * Creates a point from text.
     *
     * @param value text as {@link #setValue(String)} takes it
     * @throws SQLException with SQLState 42821 when the text is not a point
     */
    public PGpoint(String value) throws SQLException {
        this();
        read(value);
    }

    /** Creates the point (0,0). */
    public PGpoint() {
        type = "point";
    }

    /**
     * Sets the coordinates from text of the form "(x,y)"; the parentheses are optional and
     * text after the second number is ignored.
     *
     * @param s the text, or null for SQL NULL
     * @throws SQLException with SQLState 42821 when the text has no two numbers
     */
    @Override
    public void setValue(String s) throws SQLException {
        read(s);
    }

    private void read(String s) throws SQLException {
        isNull = s == null;
        if (s == null) {
            return;
        }
        List<String> pieces = GeometryText.split(GeometryText.strip(s, '(', ')'));
        try {
            if (pieces.size() < 2) {
                throw new NumberFormatException(s);
            }
            double first = Double.parseDouble(pieces.get(0));
            y = Double.parseDouble(pieces.get(1));
            x = first;
        } catch (NumberFormatException e) {
            throw GeometryText.failed(type, s, e);
        }
    }

    @Override
    public boolean equals(Object obj) {
        if (!(obj instanceof PGpoint)) {
            return false;
        }
        PGpoint other = (PGpoint) obj;
        if (isNull || other.isNull) {
            return isNull && other.isNull;
        }
        return x == other.x && y == other.y;
    }

    @Override
    public int hashCode() {
        if (isNull) {
            return 0;
        }
        long a = Double.doubleToLongBits(x);
        long b = Double.doubleToLongBits(y);
        return (int) (a ^ b ^ (a >>> 32) ^ (b >>> 32));
    }

    /** Returns "(x,y)" with the numbers as Double.toString prints them. */
    @Override
    public String getValue() {
        return isNull ? null : "(" + x + "," + y + ")";
    }

    /**
     * Moves the point by a distance.
     *
     * @param x the distance on the X axis
     * @param y the distance on the Y axis
     */
    public void translate(int x, int y) {
        translate((double) x, (double) y);
    }

    /**
     * Moves the point by a distance.
     *
     * @param x the distance on the X axis
     * @param y the distance on the Y axis
     */
    public void translate(double x, double y) {
        isNull = false;
        this.x += x;
        this.y += y;
    }

    /**
     * Moves the point to a position.
     *
     * @param x the X coordinate
     * @param y the Y coordinate
     */
    public void move(int x, int y) {
        setLocation(x, y);
    }

    /**
     * Moves the point to a position.
     *
     * @param x the X coordinate
     * @param y the Y coordinate
     */
    public void move(double x, double y) {
        isNull = false;
        this.x = x;
        this.y = y;
    }

    /**
     * Moves the point to a position.
     *
     * @param x the X coordinate
     * @param y the Y coordinate
     */
    public void setLocation(int x, int y) {
        move((double) x, (double) y);
    }

    @Override
    public Object clone() throws CloneNotSupportedException {
        return super.clone();
    }
}
