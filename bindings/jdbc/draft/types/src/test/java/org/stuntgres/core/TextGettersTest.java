package org.stuntgres.core;

import static org.junit.jupiter.api.Assertions.assertArrayEquals;
import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;

import java.sql.SQLException;
import java.util.HashMap;
import java.util.Map;
import org.junit.jupiter.api.Test;
import org.stuntgres.testing.UnitTest;

@UnitTest
class TextGettersTest {
    @Test
    void recordedStreams() {
        Map<String, Matrix.Getter> getters = new HashMap<>();
        getters.put("getString", (v, text, c) -> text);
        getters.put("getAsciiStream", (v, text, c) -> TextGetters.getAsciiStream(text));
        getters.put("getUnicodeStream", (v, text, c) -> TextGetters.getUnicodeStream(text));
        getters.put("getCharacterStream", (v, text, c) -> TextGetters.getCharacterStream(text));
        Matrix.check("get-string.tsv", getters);
    }

    @Test
    void recordedBytes() {
        Map<String, Matrix.Getter> getters = new HashMap<>();
        getters.put("getBytes", (v, text, c) -> TextGetters.getBytes(text, v.oid));
        getters.put("getBinaryStream", (v, text, c) -> TextGetters.getBinaryStream(text, v.oid));
        Matrix.check("get-bytes.tsv", getters);
    }

    @Test
    void bytea() throws SQLException {
        assertArrayEquals(new byte[] {(byte) 0xde, (byte) 0xad, 0, 0x41},
                ByteaText.decode("\\xdead0041"));
        assertArrayEquals(new byte[] {(byte) 0xDE, (byte) 0xAD}, ByteaText.decode("\\xDEAD"));
        assertArrayEquals(new byte[0], ByteaText.decode("\\x"));
        assertArrayEquals(new byte[] {'a', 0, '\\', (byte) 0xff, 'b'},
                ByteaText.decode("a\\000\\\\\\377b"));
        assertEquals("\\x00ff41", ByteaText.encode(new byte[] {0, (byte) 0xff, 0x41}));
        assertEquals("\\x", ByteaText.encode(new byte[0]));
        SQLException e = assertThrows(SQLException.class, () -> ByteaText.decode("\\xabc"));
        assertEquals("22023", e.getSQLState());
        assertEquals("The bytea value \"\\xabc\" is not valid hex text.", e.getMessage());
        assertEquals("22023",
                assertThrows(SQLException.class, () -> ByteaText.decode("\\xzz")).getSQLState());
    }

    @Test
    void otherTypesGiveTheirText() throws SQLException {
        assertArrayEquals(new byte[] {'1', '2'}, TextGetters.getBytes("12", Oid.INT4));
        assertArrayEquals(new byte[] {'\\', 'x', '4', '1'}, TextGetters.getBytes("\\x41", Oid.TEXT));
        assertArrayEquals(new byte[] {(byte) 0xf0, (byte) 0x9f, (byte) 0x98, (byte) 0x80},
                TextGetters.getBytes("\uD83D\uDE00", Oid.TEXT));
    }
}
