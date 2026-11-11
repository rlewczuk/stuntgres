package org.stuntgres.geometric;

import java.sql.SQLException;
import java.util.List;
import org.stuntgres.util.PGobject;
import org.stuntgres.util.PSQLException;
import org.stuntgres.util.PSQLState;

/** A value of the PostgreSQL type path: a list of points, open or closed. */
public class PGpath extends PGobject {
    private static final long serialVersionUID = 1L;

    /** Whether the path is open. */
    public boolean open;
    /** The points; null for SQL NULL. */
    public PGpoint[] points;

    /**
     * Creates a path; the array is not copied.
     *
     * @param points the points
     * @param open whether the path is open
     */
    public PGpath(PGpoint[] points, boolean open) {
        this();
        this.points = points;
        this.open = open;
    }

    /** Creates a path that stands for SQL NULL. */
    public PGpath() {
        type = "path";
    }

    /**
     * Creates a path from text.
     *
     * @param s text as {@link #setValue(String)} takes it
     * @throws SQLException with SQLState 42821 when the text is not a path
     */
    public PGpath(String s) throws SQLException {
        this();
        read(s);
    }

    /**
     * Sets the points from text: "[(x1,y1),...]" for an open path, "((x1,y1),...)" for a
     * closed one.
     *
     * @param s the text, or null for SQL NULL
     * @throws SQLException with SQLState 42821 when the text has neither form
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
        boolean brackets = s.startsWith("[") && s.endsWith("]");
        if (!brackets && !(s.startsWith("(") && s.endsWith(")"))) {
            throw new PSQLException("Cannot tell whether the path \"" + s
                    + "\" is open or closed.", PSQLState.DATA_TYPE_MISMATCH);
        }
        String inner = brackets ? GeometryText.strip(s, '[', ']') : GeometryText.strip(s, '(', ')');
        open = brackets;
        List<String> pieces = GeometryText.split(inner);
        PGpoint[] parsed = new PGpoint[pieces.size()];
        points = parsed;
        for (int i = 0; i < parsed.length; i++) {
            parsed[i] = new PGpoint(pieces.get(i));
        }
    }

    @Override
    public boolean equals(Object obj) {
        if (!(obj instanceof PGpath)) {
            return false;
        }
        PGpath other = (PGpath) obj;
        if (points == null || other.points == null) {
            return points == null && other.points == null;
        }
        if (other.open != open || other.points.length != points.length) {
            return false;
        }
        for (int i = 0; i < points.length; i++) {
            if (!points[i].equals(other.points[i])) {
                return false;
            }
        }
        return true;
    }

    @Override
    public int hashCode() {
        if (points == null) {
            return 0;
        }
        int hash = open ? 1231 : 1237;
        for (int i = 0; i < points.length && i < 5; i++) {
            hash = hash * 31 + points[i].hashCode();
        }
        return hash;
    }

    @Override
    public Object clone() throws CloneNotSupportedException {
        PGpath copy = (PGpath) super.clone();
        copy.points = GeometryText.copy(points);
        return copy;
    }

    /** Returns the points in brackets for an open path, in parentheses for a closed one. */
    @Override
    public String getValue() {
        if (points == null) {
            return null;
        }
        StringBuilder text = new StringBuilder(open ? "[" : "(");
        for (int i = 0; i < points.length; i++) {
            text.append(i > 0 ? "," : "").append(points[i].toString());
        }
        return text.append(open ? "]" : ")").toString();
    }

    /**
     * Tells whether the path is open.
     *
     * @return true for an open path that is not SQL NULL
     */
    public boolean isOpen() {
        return open && points != null;
    }

    /**
     * Tells whether the path is closed.
     *
     * @return true for a closed path that is not SQL NULL
     */
    public boolean isClosed() {
        return !open && points != null;
    }

    /** Makes the path closed. */
    public void closePath() {
        open = false;
    }

    /** Makes the path open. */
    public void openPath() {
        open = true;
    }
}
