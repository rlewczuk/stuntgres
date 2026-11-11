package org.stuntgres.geometric;

import java.sql.SQLException;
import java.util.List;
import org.stuntgres.util.PGobject;

/** A value of the PostgreSQL type lseg: a line segment between two points. */
public class PGlseg extends PGobject {
    private static final long serialVersionUID = 1L;

    /** The two points; null for SQL NULL. */
    public PGpoint[] point;

    /**
     * Creates a value from coordinates.
     *
     * @param x1 the X coordinate of the first point
     * @param y1 the Y coordinate of the first point
     * @param x2 the X coordinate of the second point
     * @param y2 the Y coordinate of the second point
     */
    public PGlseg(double x1, double y1, double x2, double y2) {
        this(new PGpoint(x1, y1), new PGpoint(x2, y2));
    }

    /**
     * Creates a value from two points; the points are not copied.
     *
     * @param p1 the first point
     * @param p2 the second point
     */
    public PGlseg(PGpoint p1, PGpoint p2) {
        this();
        point = new PGpoint[] {p1, p2};
    }

    /**
     * Creates a value from text.
     *
     * @param s text as {@link #setValue(String)} takes it
     * @throws SQLException with SQLState 42821 when the text is not a lseg
     */
    public PGlseg(String s) throws SQLException {
        this();
        read(s);
    }

    /** Creates a value that stands for SQL NULL. */
    public PGlseg() {
        type = "lseg";
    }

    /**
     * Sets the points from text of the form "[(x1,y1),(x2,y2)]".
     *
     * @param value the text, or null for SQL NULL
     * @throws SQLException with SQLState 42821 when the text has no two points
     */
    @Override
    public void setValue(String value) throws SQLException {
        read(value);
    }

    private void read(String value) throws SQLException {
        if (value == null) {
            point = null;
            return;
        }
        List<String> pieces = GeometryText.split(GeometryText.strip(value, '[', ']'));
        if (pieces.size() != 2) {
            throw GeometryText.failed(type, value, null);
        }
        PGpoint first = new PGpoint(pieces.get(0));
        PGpoint second = new PGpoint(pieces.get(1));
        if (point == null) {
            point = new PGpoint[2];
        }
        point[0] = first;
        point[1] = second;
    }

    @Override
    public boolean equals(Object obj) {
        if (!(obj instanceof PGlseg)) {
            return false;
        }
        PGpoint[] other = ((PGlseg) obj).point;
        if (point == null || other == null) {
            return point == null && other == null;
        }
        if ((other[0].equals(point[0]) && other[1].equals(point[1]))
                || (other[0].equals(point[1]) && other[1].equals(point[0]))) {
            return true;
        }
        return false;
    }

    @Override
    public int hashCode() {
        return point == null ? 0 : point[0].hashCode() ^ point[1].hashCode();
    }

    @Override
    public Object clone() throws CloneNotSupportedException {
        PGlseg copy = (PGlseg) super.clone();
        copy.point = GeometryText.copy(point);
        return copy;
    }

    /** Returns "[(x1,y1),(x2,y2)]". */
    @Override
    public String getValue() {
        return point == null ? null : "[" + point[0] + "," + point[1] + "]";
    }
}
