package org.stuntgres.core;

import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.InputStreamReader;
import java.io.Reader;
import java.nio.charset.Charset;
import java.nio.charset.StandardCharsets;
import java.sql.SQLException;
import org.stuntgres.util.PSQLException;
import org.stuntgres.util.PSQLState;

/**
 * The conversions behind the stream setters of PreparedStatement (design section 8.6).
 * The stream is read completely when the setter is called. The forms pgjdbc does not
 * implement are rejected by the caller: setAsciiStream and setCharacterStream with a long
 * length, setAsciiStream without a length and every setNCharacterStream.
 */
public final class StreamSetters {
    private StreamSetters() {
    }

    private static PSQLException badLength(long length) {
        return new PSQLException("The stream length " + length + " is not valid.",
                PSQLState.INVALID_PARAMETER_VALUE);
    }

    private static String read(Reader reader, int length) throws SQLException {
        if (length < 0) {
            throw badLength(length);
        }
        char[] buffer = new char[length];
        int count = 0;
        try {
            while (count < length) {
                int n = reader.read(buffer, count, length - count);
                if (n < 0) {
                    break;
                }
                count += n;
            }
        } catch (IOException e) {
            throw new PSQLException("The stream of a parameter cannot be read.",
                    PSQLState.UNEXPECTED_ERROR, e);
        }
        return new String(buffer, 0, count);
    }

    /**
     * Converts the argument of setAsciiStream(index, stream, int length).
     *
     * @param stream the stream, or null
     * @param length the number of characters to read at most
     * @return a varchar with the characters read as US-ASCII; SQL NULL for null
     * @throws SQLException with SQLState 22023 for a negative length
     */
    public static ParamValue setAsciiStream(InputStream stream, int length, ConversionContext
            context) throws SQLException {
        if (stream == null) {
            return BasicSetters.setString(null, context);
        }
        return new ParamValue(Oid.VARCHAR, decode(stream, length, StandardCharsets.US_ASCII));
    }

    /**
     * Converts the argument of setUnicodeStream(index, stream, int length).
     *
     * @param stream the stream, or null
     * @param length the number of characters to read at most
     * @return a varchar with the characters read as UTF-8; SQL NULL for null
     * @throws SQLException with SQLState 22023 for a negative length
     */
    public static ParamValue setUnicodeStream(InputStream stream, int length,
            ConversionContext context) throws SQLException {
        if (stream == null) {
            return BasicSetters.setString(null, context);
        }
        return new ParamValue(Oid.VARCHAR, decode(stream, length, StandardCharsets.UTF_8));
    }

    private static String decode(InputStream stream, int length, Charset charset)
            throws SQLException {
        return read(new InputStreamReader(stream, charset), length);
    }

    /**
     * Converts the argument of setCharacterStream(index, reader, int length).
     *
     * @param reader the reader, or null
     * @param length the number of characters to read at most
     * @param context the conversion context
     * @return a string parameter as setString gives it
     * @throws SQLException with SQLState 22023 for a negative length
     */
    public static ParamValue setCharacterStream(Reader reader, int length,
            ConversionContext context) throws SQLException {
        return BasicSetters.setString(reader == null ? null : read(reader, length), context);
    }

    /**
     * Converts the argument of setCharacterStream(index, reader).
     *
     * @param reader the reader, or null
     * @param context the conversion context
     * @return a text with everything the reader gives; for null as setString(null)
     * @throws SQLException when the reader fails
     */
    public static ParamValue setCharacterStream(Reader reader, ConversionContext context)
            throws SQLException {
        if (reader == null) {
            return BasicSetters.setString(null, context);
        }
        StringBuilder text = new StringBuilder();
        char[] buffer = new char[8192];
        try {
            for (int n = reader.read(buffer); n >= 0; n = reader.read(buffer)) {
                text.append(buffer, 0, n);
            }
        } catch (IOException e) {
            throw new PSQLException("The stream of a parameter cannot be read.",
                    PSQLState.UNEXPECTED_ERROR, e);
        }
        return new ParamValue(Oid.TEXT, text.toString());
    }

    /**
     * Converts the argument of setBinaryStream.
     *
     * @param stream the stream, or null
     * @param length the exact number of bytes, or -1 for the form without a length
     * @return a bytea; SQL NULL for null
     * @throws SQLException with SQLState 22023 when the stream ends before the length or
     *         the length is negative and not -1
     */
    public static ParamValue setBinaryStream(InputStream stream, long length)
            throws SQLException {
        if (stream == null) {
            return BasicSetters.setBytes(null);
        }
        if (length < -1 || length > Integer.MAX_VALUE) {
            throw badLength(length);
        }
        ByteArrayOutputStream bytes = new ByteArrayOutputStream();
        byte[] buffer = new byte[8192];
        try {
            long left = length < 0 ? Long.MAX_VALUE : length;
            while (left > 0) {
                int n = stream.read(buffer, 0, (int) Math.min(buffer.length, left));
                if (n < 0) {
                    break;
                }
                bytes.write(buffer, 0, n);
                left -= n;
            }
            if (length >= 0 && left > 0) {
                throw badLength(length);
            }
        } catch (IOException e) {
            throw new PSQLException("The stream of a parameter cannot be read.",
                    PSQLState.UNEXPECTED_ERROR, e);
        }
        return BasicSetters.setBytes(bytes.toByteArray());
    }
}
