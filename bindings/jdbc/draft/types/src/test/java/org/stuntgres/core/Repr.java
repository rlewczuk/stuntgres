package org.stuntgres.core;

import java.io.ByteArrayOutputStream;
import java.io.InputStream;
import java.io.Reader;
import java.lang.reflect.Array;
import java.math.BigDecimal;
import java.math.BigInteger;
import java.net.InetAddress;
import java.sql.SQLException;
import java.sql.Time;
import java.sql.Timestamp;
import java.util.Calendar;
import java.util.GregorianCalendar;
import java.util.Map;
import java.util.TreeMap;
import java.util.UUID;
import org.stuntgres.util.PGobject;

/**
 * The text form of a Java value in the recorded tables, for example {@code Integer:5},
 * {@code Timestamp:1577959872123/123456000} (milliseconds and nanoseconds),
 * {@code Double:0x1.8p0} (Double.toHexString, exact on every JDK) or
 * {@code !PSQLException|22003} for an exception (class and SQLState).
 */
public final class Repr {
    private Repr() {
    }

    /** A value that is its own text form. */
    public static final class Raw {
        private final String text;

        /** Creates a value with the given text form. */
        public Raw(String text) {
            this.text = text;
        }
    }

    /** Returns the text form of an exception: its simple class name and its SQLState. */
    public static String error(Throwable e) {
        String state = e instanceof SQLException ? ((SQLException) e).getSQLState() : "-";
        return "!" + e.getClass().getSimpleName() + "|" + state;
    }

    /** Returns the text form of a value. */
    public static String of(Object o) throws Exception {
        if (o == null) {
            return "null";
        }
        if (o instanceof Raw) {
            return ((Raw) o).text;
        }
        if (o instanceof String) {
            return "String:" + TestData.escape((String) o);
        }
        if (o instanceof Float) {
            return "Float:" + Float.toHexString((Float) o);
        }
        if (o instanceof Double) {
            return "Double:" + Double.toHexString((Double) o);
        }
        if (o instanceof Boolean || o instanceof Byte || o instanceof Short || o instanceof Integer
                || o instanceof Long || o instanceof BigDecimal || o instanceof BigInteger
                || o instanceof UUID || o instanceof Character) {
            return o.getClass().getSimpleName() + ":" + o;
        }
        if (o instanceof byte[]) {
            return "byte[]:" + hex((byte[]) o);
        }
        if (o instanceof Timestamp) {
            Timestamp t = (Timestamp) o;
            return "Timestamp:" + t.getTime() + "/" + t.getNanos() + subclass(o, Timestamp.class);
        }
        if (o instanceof java.sql.Date) {
            return "Date:" + ((java.sql.Date) o).getTime() + subclass(o, java.sql.Date.class);
        }
        if (o instanceof Time) {
            return "Time:" + ((Time) o).getTime() + subclass(o, Time.class);
        }
        if (o instanceof java.util.Date) {
            return "java.util.Date:" + ((java.util.Date) o).getTime();
        }
        if (o instanceof Calendar) {
            Calendar c = (Calendar) o;
            return "Calendar:" + c.getTimeInMillis() + "/" + c.getTimeZone().getID()
                    + subclass(o, GregorianCalendar.class);
        }
        if (o.getClass().getName().startsWith("java.time.")) {
            return o.getClass().getSimpleName() + ":" + o;
        }
        if (o instanceof InetAddress) {
            return "InetAddress:" + ((InetAddress) o).getHostAddress();
        }
        if (o instanceof PGobject) {
            PGobject p = (PGobject) o;
            return o.getClass().getName() + ":" + p.getType() + ":"
                    + TestData.escape(p.getValue());
        }
        if (o.getClass().isArray()) {
            StringBuilder b = new StringBuilder(o.getClass().getComponentType().getSimpleName());
            b.append('[');
            for (int i = 0; i < Array.getLength(o); i++) {
                b.append(i > 0 ? "," : "").append(of(Array.get(o, i)));
            }
            return b.append(']').toString();
        }
        if (o instanceof InputStream) {
            ByteArrayOutputStream out = new ByteArrayOutputStream();
            byte[] buffer = new byte[4096];
            for (int n = ((InputStream) o).read(buffer); n > 0; n = ((InputStream) o).read(buffer)) {
                out.write(buffer, 0, n);
            }
            return "InputStream:" + hex(out.toByteArray());
        }
        if (o instanceof Reader) {
            StringBuilder b = new StringBuilder();
            char[] buffer = new char[4096];
            for (int n = ((Reader) o).read(buffer); n > 0; n = ((Reader) o).read(buffer)) {
                b.append(buffer, 0, n);
            }
            return "Reader:" + TestData.escape(b.toString());
        }
        if (o instanceof Map) {
            return "Map:" + TestData.escape(new TreeMap<Object, Object>((Map<?, ?>) o).toString());
        }
        return o.getClass().getName() + ":" + TestData.escape(o.toString());
    }

    private static String subclass(Object o, Class<?> expected) {
        return o.getClass() == expected ? "" : "(" + o.getClass().getName() + ")";
    }

    /** Returns bytes as lower-case hex digits. */
    public static String hex(byte[] bytes) {
        StringBuilder b = new StringBuilder(bytes.length * 2);
        for (byte x : bytes) {
            b.append(Character.forDigit((x >> 4) & 0xf, 16)).append(Character.forDigit(x & 0xf, 16));
        }
        return b.toString();
    }
}
