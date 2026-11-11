package org.stuntgres.core;

import java.io.ByteArrayInputStream;
import java.io.InputStream;
import java.io.Reader;
import java.io.StringReader;
import java.nio.charset.StandardCharsets;
import java.sql.SQLException;

/**
 * The conversions behind ResultSet.getBytes and the stream getters (design sections 7.1
 * and 7.4). ResultSet.getString returns the column text unchanged and needs no method
 * here. The value is the column text and is never null.
 */
public final class TextGetters {
    private TextGetters() {
    }

    /**
     * Reads a column as bytes.
     *
     * @param value the column text
     * @param oid the type OID of the column
     * @return for a bytea column the decoded bytes, else the text in UTF-8
     * @throws SQLException with SQLState 22023 for malformed bytea text
     */
    public static byte[] getBytes(String value, int oid) throws SQLException {
        return oid == Oid.BYTEA ? ByteaText.decode(value) : value.getBytes(StandardCharsets.UTF_8);
    }

    /**
     * Returns the stream of ResultSet.getBinaryStream.
     *
     * @param value the column text
     * @param oid the type OID of the column
     * @return a stream over the bytes of {@link #getBytes}
     * @throws SQLException with SQLState 22023 for malformed bytea text
     */
    public static InputStream getBinaryStream(String value, int oid) throws SQLException {
        return new ByteArrayInputStream(getBytes(value, oid));
    }

    /**
     * Returns the stream of ResultSet.getAsciiStream.
     *
     * @param value the column text
     * @return a stream over the text in US-ASCII; other characters become question marks
     */
    public static InputStream getAsciiStream(String value) {
        return new ByteArrayInputStream(value.getBytes(StandardCharsets.US_ASCII));
    }

    /**
     * Returns the stream of ResultSet.getUnicodeStream.
     *
     * @param value the column text
     * @return a stream over the text in UTF-8
     */
    public static InputStream getUnicodeStream(String value) {
        return new ByteArrayInputStream(value.getBytes(StandardCharsets.UTF_8));
    }

    /**
     * Returns the reader of ResultSet.getCharacterStream.
     *
     * @param value the column text
     * @return a reader over the text
     */
    public static Reader getCharacterStream(String value) {
        return new StringReader(value);
    }
}
