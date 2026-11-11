package org.stuntgres.core;

import java.nio.charset.StandardCharsets;

/**
 * A statement parameter as the server receives it: the type OID and the value in text
 * form (design section 8.1).
 */
public final class ParamValue {
    private final int oid;
    private final String text;

    /**
     * Creates a parameter.
     *
     * @param oid the type OID; {@link Oid#UNSPECIFIED} lets the server infer the type
     * @param text the value in the server's text form, or null for SQL NULL
     */
    public ParamValue(int oid, String text) {
        this.oid = oid;
        this.text = text;
    }

    /**
     * Returns the type OID.
     *
     * @return the OID to pass as the parameter type
     */
    public int oid() {
        return oid;
    }

    /**
     * Returns the value text.
     *
     * @return the text, or null for SQL NULL
     */
    public String text() {
        return text;
    }

    /**
     * Returns the value as the bytes to pass to the pq layer.
     *
     * @return the text in UTF-8, or null for SQL NULL
     */
    public byte[] bytes() {
        return text == null ? null : text.getBytes(StandardCharsets.UTF_8);
    }

    /**
     * Returns the parameter format to pass to the pq layer.
     *
     * @return 0 (text); 1 (binary) for text that holds a NUL character, which a text
     *         parameter cannot carry: the server then rejects the value itself, with
     *         SQLState 22021 for a parameter of a character type
     */
    public int format() {
        return text != null && text.indexOf('\0') >= 0 ? 1 : 0;
    }
}
