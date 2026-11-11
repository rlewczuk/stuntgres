package org.stuntgres.geometric;

import java.sql.SQLException;
import java.util.List;
import org.stuntgres.util.PGobject;

/** A value of the PostgreSQL type polygon. */
public class PGpolygon extends PGobject {
    private static final long serialVersionUID = 1L;

    /** The corners; null for SQL NULL. */
    public PGpoint[] points;

    /**
     * Creates a polygon; the array is not copied.
     *
     * @param points the corners
     */
    public PGpolygon(PGpoint[] points) {
        this();
        this.points = points;
    }

    /**
     * Creates a polygon from text.
     *
     * @param s text as {@link #setValue(String)} takes it
     * @throws SQLException with SQLState 42821 when a point of the text is malformed
     */
    public PGpolygon(String s) throws SQLException {
        this();
        read(s);
    }

    /** Creates a polygon that stands for SQL NULL. */
    public PGpolygon() {
        type = "polygon";
    }

    /**
     * Sets the corners from text of the form "((x1,y1),(x2,y2),...)".
     *
     * @param s the text, or null for SQL NULL
     * @throws SQLException with SQLState 42821 when a point of the text is malformed
     */
    @Override
    public void setValue(String s) throws SQLException {
        read(s);
    }

    private void read(String s) throws SQLException {
        if (s == null) {
            points = null;
            return;
        }
        List<String> pieces = GeometryText.split(GeometryText.strip(s, '(', ')'));
        PGpoint[] parsed = points;
        if (parsed == null || parsed.length != pieces.size()) {
            parsed = new PGpoint[pieces.size()];
            points = parsed;
        }
        for (int i = 0; i < parsed.length; i++) {
            parsed[i] = new PGpoint(pieces.get(i));
        }
    }

    @Override
    public boolean equals(Object obj) {
        if (!(obj instanceof PGpolygon)) {
            return false;
        }
        PGpoint[] other = ((PGpolygon) obj).points;
        if (points == null || other == null) {
            return points == null && other == null;
        }
        if (other.length != points.length) {
            return false;
        }
        for (int i = 0; i < points.length; i++) {
            if (!points[i].equals(other[i])) {
                return false;
            }
        }
        return true;
    }

    @Override
    public int hashCode() {
        int hash = 0;
        if (points != null) {
            for (int i = 0; i < points.length && i < 5; i++) {
                hash = hash * 31 + points[i].hashCode();
            }
        }
        return hash;
    }

    @Override
    public Object clone() throws CloneNotSupportedException {
        PGpolygon copy = (PGpolygon) super.clone();
        copy.points = GeometryText.copy(points);
        return copy;
    }

    /** Returns the corners in parentheses. */
    @Override
    public String getValue() {
        if (points == null) {
            return null;
        }
        StringBuilder text = new StringBuilder("(");
        for (int i = 0; i < points.length; i++) {
            text.append(i > 0 ? "," : "").append(points[i].toString());
        }
        return text.append(")").toString();
    }
}
