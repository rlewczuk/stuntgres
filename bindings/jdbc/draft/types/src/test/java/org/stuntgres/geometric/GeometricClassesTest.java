package org.stuntgres.geometric;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.sql.SQLException;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import org.stuntgres.util.PGobject;
import org.junit.jupiter.api.Test;
import org.stuntgres.core.TestData;
import org.stuntgres.testing.UnitTest;

/**
 * The behavior of the geometric value classes. The cases and their results were recorded from the classes of the same names of
 * the PostgreSQL JDBC driver (value-classes-geometric.tsv); this file is a generated table.
 */
@UnitTest
class GeometricClassesTest {
    interface C {
        Object run() throws Exception;
    }

    static final Map<String, C> CASES = new LinkedHashMap<>();

    static void c(String id, C c) {
        if (CASES.put(id, c) != null) {
            throw new IllegalStateException(id);
        }
    }

    static String d(double v) {
        return Double.toString(v);
    }

    static PGobject obj(String type, String value) throws SQLException {
        PGobject o = new PGobject();
        o.setType(type);
        o.setValue(value);
        return o;
    }

    static Object nothing() {
        return null;
    }

    static String pt(PGpoint p) {
        return p == null ? "null"
                : d(p.x) + "/" + d(p.y) + " null=" + p.isNull + " value=" + p.getValue();
    }

    static String pts(PGpoint[] p) {
        if (p == null) {
            return "null";
        }
        StringBuilder b = new StringBuilder();
        for (PGpoint x : p) {
            b.append(x == null ? "null" : d(x.x) + "/" + d(x.y)).append(';');
        }
        return b.toString();
    }

    static final String[] POINTS = {"(1,2)", "1,2", "(1.5,-2e3)", " ( 1 , 2 ) ", "(NaN,Infinity)", "", "(1)", "(1,2,3)", "(a,b)", "((1,2))", "(1,2", "1,2)", "(1.0,2.0)", "(-0,0)", "(1e400,0)",
        "(,)", "( 1,2)", "(1 ,2)", "[1,2]", "<1,2>", "(1;2)"};
    static final String[] BOXES = {"(1,2),(3,4)", "(3,4),(1,2)", "((1,2),(3,4))", "1,2,3,4", "(1,2)", "", "(1,2),(3,4),(5,6)", "(a,b),(c,d)", " (1,2) , (3,4) ", "[(1,2),(3,4)]", "(1,2);(3,4)", "(1,2)(3,4)"};
    static final String[] CIRCLES = {"<(1,2),3>", "((1,2),3)", "(1,2),3", "1,2,3", "<(1,2),3", "", "<(1,2)>", "<(a,b),c>", " < ( 1 , 2 ) , 3 > ", "<(1,2),-3>", "<(1,2),3,4>", "<(1.5,2.5),1e10>", "<1,2,3>"};
    static final String[] LINES = {"{1,2,3}", "[(0,0),(1,1)]", "{0,-1,0}", "", "{1,2}", "{1,2,3,4}", "{a,b,c}", "[(1,2),(3,4)]", "((1,2),(3,4))", "(1,2),(3,4)", " { 1 , 2 , 3 } ", "{1.5,-2.5,1e10}",
        "[(1,2),(1,5)]", "[(1,2),(1,2)]", "1,2,3", "[(1,2)]"};
    static final String[] LSEGS = {"[(1,2),(3,4)]", "((1,2),(3,4))", "(1,2),(3,4)", "1,2,3,4", "", "[(1,2)]", "[(1,2),(3,4),(5,6)]", "[(a,b),(c,d)]", " [ ( 1 , 2 ) , ( 3 , 4 ) ] ", "[(1,2),(3,4)"};
    static final String[] PATHS = {"((1,2),(3,4))", "[(1,2),(3,4)]", "((0,0))", "[(1,2),(3,4),(5,6)]", "(1,2),(3,4)", "1,2,3,4", "", "()", "[]", "((1,2),(3,4)", "[(a,b)]", " [ ( 1 , 2 ) , ( 3 , 4 ) ] ",
        "((1,2))", "[(1,2)", "<(1,2)>"};
    static final String[] POLYGONS = {"((1,2),(3,4),(5,6))", "((0,0))", "(1,2),(3,4)", "1,2,3,4", "", "()", "[(1,2),(3,4)]", "((a,b))", " ( ( 1 , 2 ) , ( 3 , 4 ) ) ", "((1,2),(3,4)"};

    static {
        c("obj.equals.subclass", () -> obj("point", "(1,2)").equals(new PGpoint(1, 2)) + "|" + new PGpoint(1, 2).equals(obj("point", "(1,2)")));
        // PGpoint
        c("point.new", () -> { PGpoint p = new PGpoint(); return pt(p) + "|" + p.getType() + "|" + p.isNull(); });
        c("point.xy", () -> pt(new PGpoint(1, 2.5)) + "|" + pt(new PGpoint(-0.0, 1e300)) + "|" + pt(new PGpoint(Double.NaN, Double.NEGATIVE_INFINITY)) + "|" + pt(new PGpoint(1e-7, 123456789012.0)) + "|" + pt(new PGpoint(0.1, 100)));
        for (String s : POINTS) c("point.parse[" + s + "]", () -> pt(new PGpoint(s)));
        c("point.parse.null", () -> pt(new PGpoint((String) null)));
        c("point.setValue.null", () -> { PGpoint p = new PGpoint(1, 2); p.setValue(null); return pt(p) + "|" + p.isNull(); });
        c("point.setValue.afterNull", () -> { PGpoint p = new PGpoint((String) null); p.setValue("(3,4)"); return pt(p); });
        c("point.translate", () -> { PGpoint p = new PGpoint(1, 2); p.translate(1, -1); String a = pt(p); p.translate(0.5, 0.25); return a + "|" + pt(p); });
        c("point.move", () -> { PGpoint p = new PGpoint(1, 2); p.move(5, 6); String a = pt(p); p.move(0.5, 0.25); return a + "|" + pt(p); });
        c("point.setLocation", () -> { PGpoint p = new PGpoint(1, 2); p.setLocation(7, 8); return pt(p); });
        c("point.null.ops", () -> { PGpoint p = new PGpoint(); p.translate(1, 1); String a = pt(p); p.move(2, 3); return a + "|" + pt(p); });
        c("point.equals", () -> new PGpoint(1, 2).equals(new PGpoint("(1,2)")) + "|" + new PGpoint(1, 2).equals(new PGpoint(1, 3)) + "|" + new PGpoint().equals(new PGpoint()) + "|" + new PGpoint().equals(new PGpoint(0, 0))
            + "|" + new PGpoint(0, 0).equals(new PGpoint()) + "|" + new PGpoint(1, 2).equals(null) + "|" + new PGpoint(0.0, 0).equals(new PGpoint(-0.0, 0)) + "|" + new PGpoint(Double.NaN, 0).equals(new PGpoint(Double.NaN, 0)));
        c("point.hashCode", () -> new PGpoint(1, 2).hashCode() + "|" + new PGpoint().hashCode() + "|" + new PGpoint(0, 0).hashCode() + "|" + new PGpoint(-1.5, 2.5).hashCode());
        c("point.clone", () -> { PGpoint p = new PGpoint(1, 2); PGpoint k = (PGpoint) p.clone(); k.x = 9; return pt(p) + "|" + pt(k) + "|" + k.getType(); });
        c("point.field", () -> { PGpoint p = new PGpoint(1, 2); p.x = 5; p.isNull = true; String a = p.getValue(); p.isNull = false; return a + "|" + p.getValue(); });
        c("point.toString", () -> new PGpoint(1, 2).toString() + "|" + new PGpoint().toString());
        // PGbox
        c("box.new", () -> { PGbox b = new PGbox(); return pts(b.point) + "|" + b.getValue() + "|" + b.getType() + "|" + b.isNull(); });
        c("box.xyxy", () -> { PGbox b = new PGbox(1, 2, 3, 4); return pts(b.point) + "|" + b.getValue(); });
        c("box.points", () -> { PGpoint p1 = new PGpoint(1, 2), p2 = new PGpoint(3, 4); PGbox b = new PGbox(p1, p2); return pts(b.point) + "|" + b.getValue() + "|" + (b.point[0] == p1); });
        for (String s : BOXES) c("box.parse[" + s + "]", () -> { PGbox b = new PGbox(s); return pts(b.point) + "|" + b.getValue(); });
        c("box.parse.null", () -> { PGbox b = new PGbox((String) null); return pts(b.point) + "|" + b.getValue() + "|" + b.isNull(); });
        c("box.setValue.null", () -> { PGbox b = new PGbox(1, 2, 3, 4); b.setValue(null); return pts(b.point) + "|" + b.getValue(); });
        c("box.equals", () -> new PGbox(1, 2, 3, 4).equals(new PGbox("(1,2),(3,4)")) + "|" + new PGbox(1, 2, 3, 4).equals(new PGbox(3, 4, 1, 2)) + "|" + new PGbox(1, 2, 3, 4).equals(new PGbox(1, 4, 3, 2)) + "|" + new PGbox().equals(new PGbox())
            + "|" + new PGbox(1, 2, 3, 4).equals(new PGbox()) + "|" + new PGbox().equals(new PGbox(1, 2, 3, 4)) + "|" + new PGbox(1, 2, 3, 4).equals(null) + "|" + new PGbox(1, 2, 3, 4).equals(new PGbox(1, 2, 3, 5)));
        c("box.hashCode", () -> new PGbox(1, 2, 3, 4).hashCode() + "|" + new PGbox().hashCode() + "|" + new PGbox(3, 4, 1, 2).hashCode() + "|" + (new PGbox(1, 2, 3, 4).hashCode() == (new PGpoint(1, 2).hashCode() ^ new PGpoint(3, 4).hashCode())));
        c("box.clone", () -> { PGbox b = new PGbox(1, 2, 3, 4); PGbox k = (PGbox) b.clone(); k.point[0].x = 9; return b.getValue() + "|" + k.getValue() + "|" + (k.point != b.point); });
        c("box.clone.null", () -> { PGbox b = new PGbox(); PGbox k = (PGbox) b.clone(); return pts(k.point) + "|" + k.getValue(); });
        // PGcircle
        c("circle.new", () -> { PGcircle x = new PGcircle(); return pt(x.center) + "|" + d(x.radius) + "|" + x.getValue() + "|" + x.getType() + "|" + x.isNull(); });
        c("circle.xyr", () -> { PGcircle x = new PGcircle(1, 2, 3.5); return pt(x.center) + "|" + d(x.radius) + "|" + x.getValue(); });
        c("circle.pointr", () -> { PGpoint p = new PGpoint(1, 2); PGcircle x = new PGcircle(p, 3); return (x.center == p) + "|" + x.getValue(); });
        for (String s : CIRCLES) c("circle.parse[" + s + "]", () -> { PGcircle x = new PGcircle(s); return pt(x.center) + "|" + d(x.radius) + "|" + x.getValue(); });
        c("circle.parse.null", () -> { PGcircle x = new PGcircle((String) null); return pt(x.center) + "|" + d(x.radius) + "|" + x.getValue() + "|" + x.isNull(); });
        c("circle.setValue.null", () -> { PGcircle x = new PGcircle(1, 2, 3); x.setValue(null); return pt(x.center) + "|" + d(x.radius) + "|" + x.getValue(); });
        c("circle.equals", () -> new PGcircle(1, 2, 3).equals(new PGcircle("<(1,2),3>")) + "|" + new PGcircle(1, 2, 3).equals(new PGcircle(1, 2, 4)) + "|" + new PGcircle().equals(new PGcircle()) + "|" + new PGcircle(1, 2, 3).equals(new PGcircle())
            + "|" + new PGcircle().equals(new PGcircle(1, 2, 3)) + "|" + new PGcircle(1, 2, 3).equals(null) + "|" + new PGcircle(1, 2, 3).equals(new PGcircle(1, 3, 3)));
        c("circle.hashCode", () -> new PGcircle(1, 2, 3).hashCode() + "|" + new PGcircle().hashCode() + "|" + new PGcircle(-1.5, 2.5, 1e10).hashCode());
        c("circle.clone", () -> { PGcircle x = new PGcircle(1, 2, 3); PGcircle k = (PGcircle) x.clone(); k.center.x = 9; k.radius = 8; return x.getValue() + "|" + k.getValue(); });
        c("circle.clone.null", () -> { PGcircle k = (PGcircle) new PGcircle().clone(); return pt(k.center) + "|" + k.getValue(); });
        // PGline
        c("line.new", () -> { PGline l = new PGline(); return d(l.a) + "/" + d(l.b) + "/" + d(l.c) + "|" + l.getValue() + "|" + l.getType() + "|" + l.isNull(); });
        c("line.abc", () -> { PGline l = new PGline(1, 2, 3.5); return d(l.a) + "/" + d(l.b) + "/" + d(l.c) + "|" + l.getValue(); });
        for (double[] q : new double[][] {{0, 0, 1, 1}, {1, 2, 3, 4}, {1, 2, 1, 5}, {1, 2, 4, 2}, {1, 2, 1, 2}, {0, 0, 2, 1}, {-1, 5, 3, -7}})
            c("line.xyxy" + Arrays.toString(q), () -> { PGline l = new PGline(q[0], q[1], q[2], q[3]); return d(l.a) + "/" + d(l.b) + "/" + d(l.c) + "|" + l.getValue(); });
        c("line.points", () -> { PGline l = new PGline(new PGpoint(0, 0), new PGpoint(1, 1)); return d(l.a) + "/" + d(l.b) + "/" + d(l.c) + "|" + l.getValue(); });
        c("line.lseg", () -> { PGline l = new PGline(new PGlseg(1, 2, 3, 4)); return d(l.a) + "/" + d(l.b) + "/" + d(l.c) + "|" + l.getValue(); });
        for (String s : LINES) c("line.parse[" + s + "]", () -> { PGline l = new PGline(s); return d(l.a) + "/" + d(l.b) + "/" + d(l.c) + "|" + l.getValue(); });
        c("line.parse.null", () -> { PGline l = new PGline((String) null); return d(l.a) + "/" + d(l.b) + "/" + d(l.c) + "|" + l.getValue() + "|" + l.isNull(); });
        c("line.setValue.null", () -> { PGline l = new PGline(1, 2, 3); l.setValue(null); return d(l.a) + "/" + d(l.b) + "/" + d(l.c) + "|" + l.getValue(); });
        c("line.equals", () -> new PGline(1, 2, 3).equals(new PGline("{1,2,3}")) + "|" + new PGline(1, 2, 3).equals(new PGline(1, 2, 4)) + "|" + new PGline().equals(new PGline()) + "|" + new PGline(0, 0, 0).equals(new PGline())
            + "|" + new PGline().equals(new PGline(0, 0, 0)) + "|" + new PGline(1, 2, 3).equals(null) + "|" + new PGline(1, 2, 3).equals(new PGline(2, 4, 6)));
        c("line.hashCode", () -> new PGline(1, 2, 3).hashCode() + "|" + new PGline().hashCode() + "|" + new PGline(-1.5, 2.5, 1e10).hashCode() + "|" + new PGline(0, 0, 0).hashCode());
        c("line.clone", () -> { PGline l = new PGline(1, 2, 3); PGline k = (PGline) l.clone(); k.a = 9; return l.getValue() + "|" + k.getValue(); });
        // PGlseg
        c("lseg.new", () -> { PGlseg l = new PGlseg(); return pts(l.point) + "|" + l.getValue() + "|" + l.getType() + "|" + l.isNull(); });
        c("lseg.xyxy", () -> { PGlseg l = new PGlseg(1, 2, 3, 4.5); return pts(l.point) + "|" + l.getValue(); });
        c("lseg.points", () -> { PGpoint p1 = new PGpoint(1, 2); PGlseg l = new PGlseg(p1, new PGpoint(3, 4)); return pts(l.point) + "|" + l.getValue() + "|" + (l.point[0] == p1); });
        for (String s : LSEGS) c("lseg.parse[" + s + "]", () -> { PGlseg l = new PGlseg(s); return pts(l.point) + "|" + l.getValue(); });
        c("lseg.parse.null", () -> { PGlseg l = new PGlseg((String) null); return pts(l.point) + "|" + l.getValue() + "|" + l.isNull(); });
        c("lseg.setValue.null", () -> { PGlseg l = new PGlseg(1, 2, 3, 4); l.setValue(null); return pts(l.point) + "|" + l.getValue(); });
        c("lseg.equals", () -> new PGlseg(1, 2, 3, 4).equals(new PGlseg("[(1,2),(3,4)]")) + "|" + new PGlseg(1, 2, 3, 4).equals(new PGlseg(3, 4, 1, 2)) + "|" + new PGlseg().equals(new PGlseg()) + "|" + new PGlseg(1, 2, 3, 4).equals(new PGlseg())
            + "|" + new PGlseg().equals(new PGlseg(1, 2, 3, 4)) + "|" + new PGlseg(1, 2, 3, 4).equals(null));
        c("lseg.hashCode", () -> new PGlseg(1, 2, 3, 4).hashCode() + "|" + new PGlseg().hashCode() + "|" + (new PGlseg(1, 2, 3, 4).hashCode() == (new PGpoint(1, 2).hashCode() ^ new PGpoint(3, 4).hashCode())));
        c("lseg.clone", () -> { PGlseg l = new PGlseg(1, 2, 3, 4); PGlseg k = (PGlseg) l.clone(); k.point[0].x = 9; return l.getValue() + "|" + k.getValue(); });
        c("lseg.clone.null", () -> { PGlseg k = (PGlseg) new PGlseg().clone(); return pts(k.point) + "|" + k.getValue(); });
        // PGpath
        c("path.new", () -> { PGpath p = new PGpath(); return p.open + "|" + pts(p.points) + "|" + p.getValue() + "|" + p.getType() + "|" + p.isNull(); });
        c("path.points.open", () -> { PGpath p = new PGpath(new PGpoint[] {new PGpoint(1, 2), new PGpoint(3, 4)}, true); return p.open + "|" + p.isOpen() + "|" + p.isClosed() + "|" + p.getValue(); });
        c("path.points.closed", () -> { PGpath p = new PGpath(new PGpoint[] {new PGpoint(1, 2), new PGpoint(3, 4)}, false); return p.open + "|" + p.isOpen() + "|" + p.isClosed() + "|" + p.getValue(); });
        c("path.points.empty", () -> new PGpath(new PGpoint[0], true).getValue() + "|" + new PGpath(new PGpoint[0], false).getValue());
        c("path.points.null", () -> { PGpath p = new PGpath(null, true); return pts(p.points) + "|" + p.getValue(); });
        for (String s : PATHS) c("path.parse[" + s + "]", () -> { PGpath p = new PGpath(s); return p.open + "|" + pts(p.points) + "|" + p.getValue(); });
        c("path.parse.null", () -> { PGpath p = new PGpath((String) null); return p.open + "|" + pts(p.points) + "|" + p.getValue() + "|" + p.isNull(); });
        c("path.setValue.null", () -> { PGpath p = new PGpath("[(1,2)]"); p.setValue(null); return p.open + "|" + pts(p.points) + "|" + p.getValue(); });
        c("path.openclose", () -> { PGpath p = new PGpath("((1,2),(3,4))"); p.openPath(); String a = p.getValue(); p.closePath(); return a + "|" + p.getValue(); });
        c("path.equals", () -> new PGpath("[(1,2),(3,4)]").equals(new PGpath("[(1,2),(3,4)]")) + "|" + new PGpath("[(1,2),(3,4)]").equals(new PGpath("((1,2),(3,4))")) + "|" + new PGpath("[(1,2),(3,4)]").equals(new PGpath("[(1,2)]"))
            + "|" + new PGpath().equals(new PGpath()) + "|" + new PGpath("[(1,2)]").equals(new PGpath()) + "|" + new PGpath().equals(new PGpath("[(1,2)]")) + "|" + new PGpath("[(1,2)]").equals(null) + "|" + new PGpath("[(1,2),(3,4)]").equals(new PGpath("[(3,4),(1,2)]")));
        c("path.hashCode", () -> new PGpath("[(1,2),(3,4)]").hashCode() + "|" + new PGpath("((1,2),(3,4))").hashCode() + "|" + new PGpath().hashCode() + "|" + new PGpath(new PGpoint[0], true).hashCode() + "|" + new PGpath("[(1,2),(3,4),(5,6),(7,8),(9,10),(11,12)]").hashCode());
        c("path.clone", () -> { PGpath p = new PGpath("[(1,2),(3,4)]"); PGpath k = (PGpath) p.clone(); k.points[0].x = 9; k.open = false; return p.getValue() + "|" + k.getValue(); });
        c("path.clone.null", () -> { PGpath k = (PGpath) new PGpath().clone(); return pts(k.points) + "|" + k.getValue(); });
        // PGpolygon
        c("polygon.new", () -> { PGpolygon p = new PGpolygon(); return pts(p.points) + "|" + p.getValue() + "|" + p.getType() + "|" + p.isNull(); });
        c("polygon.points", () -> { PGpoint[] a = {new PGpoint(1, 2), new PGpoint(3, 4)}; PGpolygon p = new PGpolygon(a); return (p.points == a) + "|" + p.getValue(); });
        c("polygon.points.empty", () -> new PGpolygon(new PGpoint[0]).getValue());
        c("polygon.points.null", () -> { PGpolygon p = new PGpolygon((PGpoint[]) null); return pts(p.points) + "|" + p.getValue(); });
        for (String s : POLYGONS) c("polygon.parse[" + s + "]", () -> { PGpolygon p = new PGpolygon(s); return pts(p.points) + "|" + p.getValue(); });
        c("polygon.parse.null", () -> { PGpolygon p = new PGpolygon((String) null); return pts(p.points) + "|" + p.getValue() + "|" + p.isNull(); });
        c("polygon.setValue.null", () -> { PGpolygon p = new PGpolygon("((1,2))"); p.setValue(null); return pts(p.points) + "|" + p.getValue(); });
        c("polygon.equals", () -> new PGpolygon("((1,2),(3,4))").equals(new PGpolygon("((1,2),(3,4))")) + "|" + new PGpolygon("((1,2),(3,4))").equals(new PGpolygon("((1,2))")) + "|" + new PGpolygon().equals(new PGpolygon())
            + "|" + new PGpolygon("((1,2))").equals(new PGpolygon()) + "|" + new PGpolygon().equals(new PGpolygon("((1,2))")) + "|" + new PGpolygon("((1,2))").equals(null) + "|" + new PGpolygon("((1,2),(3,4))").equals(new PGpolygon("((3,4),(1,2))")));
        c("polygon.hashCode", () -> new PGpolygon("((1,2),(3,4))").hashCode() + "|" + new PGpolygon().hashCode() + "|" + new PGpolygon(new PGpoint[0]).hashCode() + "|" + new PGpolygon("((1,2),(3,4),(5,6),(7,8),(9,10),(11,12))").hashCode());
        c("polygon.clone", () -> { PGpolygon p = new PGpolygon("((1,2),(3,4))"); PGpolygon k = (PGpolygon) p.clone(); k.points[0].x = 9; return p.getValue() + "|" + k.getValue(); });
        c("polygon.clone.null", () -> { PGpolygon k = (PGpolygon) new PGpolygon().clone(); return pts(k.points) + "|" + k.getValue(); });
    }

    @Test
    void recordedCases() {
        List<String> failures = new ArrayList<>();
        int count = 0;
        for (String[] row : TestData.rows("value-classes-geometric.tsv")) {
            C c = CASES.get(TestData.unescape(row[0]));
            String got;
            try {
                got = TestData.escape(String.valueOf(c.run()));
            } catch (SQLException e) {
                got = "!" + e.getClass().getSimpleName() + "|" + e.getSQLState();
            } catch (Exception e) {
                got = "!" + e.getClass().getSimpleName() + "|-";
            }
            count++;
            if (!got.equals(row[1]) && failures.size() < 20) {
                failures.add(row[0] + ": expected " + row[1] + " but was " + got);
            }
        }
        assertEquals(CASES.size(), count);
        assertTrue(count > 100, "cases: " + count);
        assertEquals("", String.join("\n", failures));
    }
}
