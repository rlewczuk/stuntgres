package org.stuntgres.geometric;

import java.sql.SQLException;
import java.util.List;
import org.stuntgres.util.PGobject;

/** A value of the PostgreSQL type line: the line ax + by + c = 0. */
public class PGline extends PGobject {
    private static final long serialVersionUID = 1L;

    /** The coefficient of x. */
    public double a;
    /** The coefficient of y. */
    public double b;
    /** The constant. */
    public double c;
    private boolean isNull;

    /**
     * Creates a line from its coefficients.
     *
     * @param a the coefficient of x
     * @param b the coefficient of y
     * @param c the constant
     */
    public PGline(double a, double b, double c) {
        this();
        this.a = a;
        this.b = b;
        this.c = c;
    }

    /**
     * Creates the line through two points.
     *
     * @param x1 the X coordinate of the first point
     * @param y1 the Y coordinate of the first point
     * @param x2 the X coordinate of the second point
     * @param y2 the Y coordinate of the second point
     */
    public PGline(double x1, double y1, double x2, double y2) {
        this();
        through(x1, y1, x2, y2);
    }

    /**
     * Creates the line through two points.
     *
     * @param p1 the first point
     * @param p2 the second point; when either is null the line stands for SQL NULL
     */
    public PGline(PGpoint p1, PGpoint p2) {
        this();
        if (p1 == null || p2 == null) {
            isNull = true;
        } else {
            through(p1.x, p1.y, p2.x, p2.y);
        }
    }

    /**
     * Creates the line through the end points of a segment.
     *
     * @param lseg the segment; when it is null or SQL NULL the line stands for SQL NULL
     */
    public PGline(PGlseg lseg) {
        this();
        if (lseg == null || lseg.point == null || lseg.point[0] == null
                || lseg.point[1] == null) {
            isNull = true;
        } else {
            through(lseg.point[0].x, lseg.point[0].y, lseg.point[1].x, lseg.point[1].y);
        }
    }

    /**
     * Creates a line from text.
     *
     * @param s text as {@link #setValue(String)} takes it
     * @throws SQLException with SQLState 42821 when the text is not a line
     */
    public PGline(String s) throws SQLException {
        this();
        read(s);
    }

    /** Creates the line with all coefficients zero. */
    public PGline() {
        type = "line";
    }

    private void through(double x1, double y1, double x2, double y2) {
        if (x1 == x2) {
            a = -1;
            b = 0;
        } else {
            a = (y2 - y1) / (x2 - x1);
            b = -1;
        }
        c = y1 - a * x1;
    }

    /**
     * Sets the coefficients from text of the form "{a,b,c}" or from two points
     * "[(x1,y1),(x2,y2)]"; text that starts with neither a brace nor a bracket changes
     * nothing.
     *
     * @param s the text, or null for SQL NULL
     * @throws SQLException with SQLState 42821 when the text is malformed
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
        String trimmed = s.trim();
        try {
            if (trimmed.startsWith("{")) {
                List<String> pieces = GeometryText.split(GeometryText.strip(s, '{', '}'));
                if (pieces.size() != 3) {
                    throw GeometryText.failed(type, s, null);
                }
                double first = Double.parseDouble(pieces.get(0));
                double second = Double.parseDouble(pieces.get(1));
                c = Double.parseDouble(pieces.get(2));
                a = first;
                b = second;
            } else if (trimmed.startsWith("[")) {
                List<String> pieces = GeometryText.split(GeometryText.strip(s, '[', ']'));
                if (pieces.size() != 2) {
                    throw GeometryText.failed(type, s, null);
                }
                PGpoint p1 = new PGpoint(pieces.get(0));
                PGpoint p2 = new PGpoint(pieces.get(1));
                a = p2.x - p1.x;
                b = p2.y - p1.y;
                c = p1.y;
            }
        } catch (NumberFormatException e) {
            throw GeometryText.failed(type, s, e);
        }
    }

    @Override
    public boolean equals(Object obj) {
        if (this == obj) {
            return true;
        }
        if (obj == null || getClass() != obj.getClass() || !super.equals(obj)) {
            return false;
        }
        PGline other = (PGline) obj;
        if (isNull || other.isNull) {
            return isNull && other.isNull;
        }
        return Double.compare(other.a, a) == 0 && Double.compare(other.b, b) == 0
                && Double.compare(other.c, c) == 0;
    }

    @Override
    public int hashCode() {
        if (isNull) {
            return 0;
        }
        int hash = super.hashCode();
        hash = 31 * hash + GeometryText.hash(a);
        hash = 31 * hash + GeometryText.hash(b);
        return 31 * hash + GeometryText.hash(c);
    }

    /** Returns "{a,b,c}". */
    @Override
    public String getValue() {
        return isNull ? null : "{" + a + "," + b + "," + c + "}";
    }

    @Override
    public Object clone() throws CloneNotSupportedException {
        return super.clone();
    }
}
