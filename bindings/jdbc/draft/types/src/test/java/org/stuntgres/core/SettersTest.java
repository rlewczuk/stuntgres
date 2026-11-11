package org.stuntgres.core;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.util.ArrayList;
import java.util.List;
import java.util.TimeZone;
import org.junit.jupiter.api.Test;
import org.stuntgres.testing.UnitTest;

@UnitTest
class SettersTest {
    /**
     * Runs every recorded case of set.tsv: the case id (column 1) names a call of
     * SetterCases; the row gives the property stringtype and the default zone it holds for
     * ("*" for all), then the OID and the text of the parameter, or the exception.
     */
    @Test
    void recordedCases() throws Exception {
        List<String> failures = new ArrayList<>();
        int checked = 0;
        for (String jvm : Matrix.JVM_ZONES) {
            TimeZone zone = TimeZone.getTimeZone(jvm);
            SetterCases cases = new SetterCases(zone);
            for (String[] row : TestData.rows("set.tsv")) {
                if (!(row[2].equals("*") || row[2].equals(jvm))) {
                    continue;
                }
                SetterCases.Call call = cases.cases.get(TestData.unescape(row[0]));
                if (call == null) {
                    continue;      // a case of the large-object setters (JDBC-DESIGN-FEATURES)
                }
                for (boolean unspecified : new boolean[] {false, true}) {
                    if (!(row[1].equals("*") || row[1].equals(unspecified ? "unspecified" : "default"))
                            || (unspecified && !row[2].equals("*") && !jvm.equals("UTC"))) {
                        continue;
                    }
                    SetterCalls calls = new SetterCalls(
                            new ConversionContext(zone, Matrix.LOOKUP, unspecified, false));
                    String actual;
                    try {
                        call.set(calls, 1, calls);
                        actual = calls.value.oid() + "\t" + TestData.escape(calls.value.text());
                    } catch (Exception e) {
                        actual = Repr.error(e);
                    }
                    checked++;
                    String problem = compare(row[3], row[4], actual);
                    if (problem != null && failures.size() < 25) {
                        failures.add(row[0] + " (stringtype " + (unspecified ? "unspecified" : "default")
                                + ", jvm " + jvm + "): " + problem);
                    }
                }
            }
        }
        assertTrue(checked > 6000, "cases checked: " + checked);
        assertEquals("", String.join("\n", failures), "cases of set.tsv that differ");
    }

    /** Returns null when the outcome agrees with the recorded row, else a description. */
    private static String compare(String oid, String text, String actual) {
        String want;
        if (oid.startsWith("!") && text.equals("server")) {
            // the server rejects the value: the setter itself must accept it
            return actual.startsWith("!") ? "expected a parameter but was " + actual : null;
        } else if (oid.equals("!ArrayIndexOutOfBoundsException|-")) {
            want = "!PSQLException|22007";       // pgjdbc fails with an unchecked exception
        } else if (oid.startsWith("!")) {
            want = oid;
        } else if (text.startsWith("?")) {
            // the text is not observable through pgjdbc (binary transfer): only the OID
            return actual.startsWith(oid + "\t") ? null : "expected OID " + oid + " but was " + actual;
        } else {
            want = oid + "\t" + text;
            if ((oid.equals("700") || oid.equals("701")) && !actual.startsWith("!")
                    && !text.equals("\\N")) {
                // Float.toString and Double.toString differ between JDK versions
                String[] got = actual.split("\t");
                boolean same = got[0].equals(oid) && (oid.equals("700")
                        ? Float.compare(Float.parseFloat(got[1]), Float.parseFloat(text)) == 0
                        : Double.compare(Double.parseDouble(got[1]), Double.parseDouble(text)) == 0);
                return same ? null : "expected " + want + " but was " + actual;
            }
        }
        return want.equals(actual) ? null : "expected " + want + " but was " + actual;
    }
}
