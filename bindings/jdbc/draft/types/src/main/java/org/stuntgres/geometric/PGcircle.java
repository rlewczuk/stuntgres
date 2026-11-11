package org.stuntgres.geometric;

import java.sql.SQLException;
import java.util.List;
import org.stuntgres.util.PGobject;

/** A value of the PostgreSQL type circle. */
public class PGcircle extends PGobject {
    private static final long serialVersionUID = 1L;

    /** The center; null for SQL NULL. */
    public PGpoint center;
    /** The radius. */
    public double radius;

    /**
     * Creates a circle from coordinates.
     *
     * @param x the X coordinate of the center
     * @param y the Y coordinate of the center
     * @param r the radius
     */
    public PGcircle(double x, double y, double r) {
        this(new PGpoint(x, y), r);
    }

    /**
     * Creates a circle; the center is not copied.
     *
     * @param c the center
     * @param r the radius
     */
    public PGcircle(PGpoint c, double r) {
        this();
        center = c;
        radius = r;
    }

    /**
     * Creates a circle from text.
     *
     * @param s text as {@link #setValue(String)} takes it
     * @throws SQLException with SQLState 42821 when the text is not a circle
     */
    public PGcircle(String s) throws SQLException {
        this();
        read(s);
    }

    /** Creates a circle that stands for SQL NULL. */
    public PGcircle() {
        type = "circle";
    }

    /**
     * Sets center and radius from text of the form "&lt;(x,y),r&gt;".
     *
     * @param s the text, or null for SQL NULL
     * @throws SQLException with SQLState 42821 when the text has no point and number
     */
    @Override
    public void setValue(String s) throws SQLException {
        read(s);
    }

    private void read(String s) throws SQLException {
        if (s == null) {
            center = null;
            return;
        }
        List<String> pieces = GeometryText.split(GeometryText.strip(s, '<', '>'));
        if (pieces.size() != 2) {
            throw GeometryText.failed(type, s, null);
        }
        try {
            PGpoint point = new PGpoint(pieces.get(0));
            radius = Double.parseDouble(pieces.get(1));
            center = point;
        } catch (NumberFormatException e) {
            throw GeometryText.failed(type, s, e);
        }
    }

    @Override
    public boolean equals(Object obj) {
        if (!(obj instanceof PGcircle)) {
            return false;
        }
        PGcircle other = (PGcircle) obj;
        if (center == null || other.center == null) {
            return center == null && other.center == null;
        }
        return other.radius == radius && other.center.equals(center);
    }

    @Override
    public int hashCode() {
        return center == null ? 0 : GeometryText.hash(radius) * 31 + center.hashCode();
    }

    @Override
    public Object clone() throws CloneNotSupportedException {
        PGcircle copy = (PGcircle) super.clone();
        if (center != null) {
            copy.center = (PGpoint) center.clone();
        }
        return copy;
    }

    /** Returns "&lt;(x,y),r&gt;". */
    @Override
    public String getValue() {
        return center == null ? null : "<" + center + "," + radius + ">";
    }
}
