package org.stuntgres.core;

import java.io.ByteArrayOutputStream;
import java.nio.charset.StandardCharsets;
import java.sql.SQLException;
import org.stuntgres.util.PSQLException;
import org.stuntgres.util.PSQLState;

/** The text forms of the type bytea (design section 6.4). */
public final class ByteaText {
    private static final char[] HEX = "0123456789abcdef".toCharArray();

    private ByteaText() {
    }

    /**
     * Decodes bytea text: the hex form (backslash, x, two hex digits per byte) or the
     * escape form (a backslash followed by three octal digits or by a backslash; any other
     * character stands for itself).
     *
     * @param text the column text
     * @return the bytes
     * @throws SQLException with SQLState 22023 when the hex form has an odd number of
     *         digits or a character that is no hex digit
     */
    public static byte[] decode(String text) throws SQLException {
        if (text.startsWith("\\x")) {
            int digits = text.length() - 2;
            byte[] bytes = new byte[digits / 2];
            boolean valid = digits % 2 == 0;
            for (int i = 0; valid && i < bytes.length; i++) {
                int high = Character.digit(text.charAt(2 + 2 * i), 16);
                int low = Character.digit(text.charAt(3 + 2 * i), 16);
                valid = high >= 0 && low >= 0;
                bytes[i] = (byte) (high << 4 | low);
            }
            if (!valid) {
                throw new PSQLException("The bytea value \"" + text + "\" is not valid hex text.",
                        PSQLState.INVALID_PARAMETER_VALUE);
            }
            return bytes;
        }
        byte[] in = text.getBytes(StandardCharsets.UTF_8);
        ByteArrayOutputStream out = new ByteArrayOutputStream(in.length);
        for (int i = 0; i < in.length; i++) {
            byte b = in[i];
            if (b == '\\' && i + 1 < in.length && in[i + 1] == '\\') {
                i++;
            } else if (b == '\\' && i + 3 < in.length) {
                b = (byte) ((in[i + 1] - '0') * 64 + (in[i + 2] - '0') * 8 + (in[i + 3] - '0'));
                i += 3;
            }
            out.write(b);
        }
        return out.toByteArray();
    }

    /**
     * Encodes bytes in the hex form.
     *
     * @param bytes the bytes
     * @return backslash, x and two lower-case hex digits per byte
     */
    public static String encode(byte[] bytes) {
        StringBuilder text = new StringBuilder(2 + bytes.length * 2).append("\\x");
        for (byte b : bytes) {
            text.append(HEX[(b >> 4) & 0xf]).append(HEX[b & 0xf]);
        }
        return text.toString();
    }
}
