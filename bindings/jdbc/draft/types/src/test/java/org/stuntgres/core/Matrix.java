package org.stuntgres.core;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.util.ArrayList;
import java.util.Arrays;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.TimeZone;

/**
 * Runs conversion functions against a recorded getter table (design section 7.1). A table
 * has the columns id, jvm, session and one column per getter; values.tsv gives for every
 * id the type OID, the type modifier and the column text in the three session time zones.
 * A row with jvm or session "*" holds for every recorded zone. Rows of SQL NULL values are
 * skipped: the conversion functions are not called for NULL.
 */
public final class Matrix {
    /** The recorded default time zones of the JVM. */
    public static final String[] JVM_ZONES = {"UTC", "Europe/Warsaw"};
    /** The recorded session time zones, in the order of the text columns of values.tsv. */
    public static final String[] SESSION_ZONES = {"UTC", "America/New_York", "Asia/Kolkata"};
    /** The result of a getter for a cell the test does not cover. */
    public static final Object SKIP = new Object();
    /**
     * The lookup of the tests: it knows the user-defined types of the recorded values
     * (types-user.tsv) by OID and by name.
     */
    public static final TypeLookup LOOKUP = new TypeLookup() {
        private final Map<Integer, PgType> types = userTypes();

        @Override
        public PgType byOid(int oid) {
            return types.get(oid);
        }

        @Override
        public int oidByName(String name) {
            PgType type = named(name);
            return type == null ? 0 : type.oid();
        }

        @Override
        public int arrayOidByElementName(String elementName) {
            PgType type = named(elementName);
            return type == null ? 0 : type.arrayOid();
        }

        /** As the server resolves a name: user types without quotes, built-in in any case. */
        private PgType named(String name) {
            for (PgType type : types.values()) {
                if (type.name().replace("\"", "").equals(name)) {
                    return type;
                }
            }
            return TypeTable.byName(name.toLowerCase(java.util.Locale.ROOT));
        }
    };

    private Matrix() {
    }

    /** One value of values.tsv. */
    public static final class Value {
        /** The id of the value, for example {@code int4-004}. */
        public final String id;
        /** The declared type, for example {@code numeric(10,2)}. */
        public final String type;
        /** The type OID of the column. */
        public final int oid;
        /** The type modifier of the column. */
        public final int typmod;
        final String[] texts;

        Value(String[] row) {
            id = row[0];
            type = row[2];
            oid = Integer.parseInt(row[3]);
            typmod = Integer.parseInt(row[4]);
            texts = new String[] {TestData.unescape(row[6]), TestData.unescape(row[7]),
                TestData.unescape(row[8])};
        }
    }

    /** A conversion under test. */
    public interface Getter {
        /**
         * Converts a column text.
         *
         * @param value the recorded value: type OID and modifier
         * @param text the column text, never null
         * @param context a context with the default time zone of the row
         * @return the Java value
         */
        Object get(Value value, String text, ConversionContext context) throws Exception;
    }

    private static Map<Integer, PgType> userTypes() {
        Map<Integer, PgType> types = new HashMap<>();
        for (String[] row : TestData.rows("types-user.tsv")) {
            int oid = Integer.parseInt(row[1]);
            types.put(oid, new PgType(oid, row[0], Integer.parseInt(row[2]), row[3],
                    Integer.parseInt(row[4]), Integer.parseInt(row[5]), row[6].charAt(0)));
        }
        return types;
    }

    /** Returns the values of values.tsv by id. */
    public static Map<String, Value> values() {
        Map<String, Value> values = new HashMap<>();
        for (String[] row : TestData.rows("values.tsv")) {
            values.put(row[0], new Value(row));
        }
        return values;
    }

    /** Returns the outcome the driver must produce for a recorded cell. */
    public static String expected(String cell) {
        // pgjdbc fails with an unchecked exception in these cells; the driver raises a
        // PSQLException (design section 7.9)
        if (cell.equals("!ClassCastException|-")) {
            return "!PSQLException|22023";
        }
        if (cell.equals("!ArrayIndexOutOfBoundsException|-")
                || cell.equals("!DateTimeException|-")) {
            return "!PSQLException|22007";
        }
        return cell;
    }

    /**
     * Checks every cell of the given getters.
     *
     * @param resource the table, for example {@code get-integer.tsv}
     * @param getters the conversions by getter name as in the table header
     */
    public static void check(String resource, Map<String, Getter> getters) {
        Map<String, Value> values = values();
        String[] header = TestData.header(resource);
        Getter[] byColumn = new Getter[header.length];
        String[] names = new String[header.length];
        int found = 0;
        for (int c = 3; c < header.length; c++) {
            for (String name : header[c].split("\\|")) {
                if (getters.containsKey(name)) {
                    byColumn[c] = getters.get(name);
                    names[c] = name;
                    found++;
                }
            }
        }
        assertEquals(getters.size(), found, "getters of " + resource + ": " + Arrays.toString(header));
        List<String> failures = new ArrayList<>();
        int cells = 0;
        for (String[] row : TestData.rows(resource)) {
            Value value = values.get(row[0]);
            for (int z = 0; z < SESSION_ZONES.length; z++) {
                if (value.texts[z] == null
                        || !(row[2].equals("*") || row[2].equals(SESSION_ZONES[z]))) {
                    continue;
                }
                for (String jvm : JVM_ZONES) {
                    if (!(row[1].equals("*") || row[1].equals(jvm))) {
                        continue;
                    }
                    ConversionContext context = new ConversionContext(TimeZone.getTimeZone(jvm),
                            LOOKUP, false, false);
                    for (int c = 3; c < header.length; c++) {
                        if (byColumn[c] == null) {
                            continue;
                        }
                        String actual;
                        try {
                            Object result = byColumn[c].get(value, value.texts[z], context);
                            if (result == SKIP) {
                                continue;
                            }
                            actual = Repr.of(result);
                        } catch (Exception e) {
                            actual = Repr.error(e);
                        }
                        cells++;
                        String want = expected(row[c]);
                        if (!want.equals(actual) && failures.size() < 20) {
                            failures.add(names[c] + " of " + value.id + " (" + value.type + " '"
                                    + TestData.escape(value.texts[z]) + "', jvm " + jvm + ", session "
                                    + SESSION_ZONES[z] + "): expected " + want + " but was " + actual);
                        }
                    }
                }
            }
        }
        assertTrue(cells > 0, "no cells in " + resource);
        assertEquals("", String.join("\n", failures), "cells of " + resource + " that differ");
    }
}
