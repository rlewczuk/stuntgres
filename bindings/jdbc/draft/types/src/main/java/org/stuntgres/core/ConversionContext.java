package org.stuntgres.core;

import java.util.TimeZone;

/**
 * What a conversion needs to know besides the value: the default time zone, the access to
 * the server's types and the connection properties that change conversions. Objects of
 * this class are immutable.
 */
public final class ConversionContext {
    private final TimeZone defaultZone;
    private final TypeLookup types;
    private final boolean stringTypeUnspecified;
    private final boolean convertBooleanToNumeric;

    /**
     * Creates a context.
     *
     * @param defaultZone the zone used for java.sql.Date, Time and Timestamp when no
     *        Calendar is given; the caller passes TimeZone.getDefault()
     * @param types the access to the server's types
     * @param stringTypeUnspecified true for the connection property stringtype=unspecified:
     *        string parameters are sent without a type
     * @param convertBooleanToNumeric true for the connection property
     *        convertBooleanToNumeric=true: numeric getters read a boolean column as 1 or 0
     */
    public ConversionContext(TimeZone defaultZone, TypeLookup types,
            boolean stringTypeUnspecified, boolean convertBooleanToNumeric) {
        this.defaultZone = defaultZone;
        this.types = types;
        this.stringTypeUnspecified = stringTypeUnspecified;
        this.convertBooleanToNumeric = convertBooleanToNumeric;
    }

    /**
     * Returns the default time zone.
     *
     * @return the zone given to the constructor
     */
    public TimeZone defaultZone() {
        return defaultZone;
    }

    /**
     * Returns the access to the server's types.
     *
     * @return the lookup given to the constructor
     */
    public TypeLookup types() {
        return types;
    }

    /**
     * Tells whether string parameters are sent without a type.
     *
     * @return the constructor argument
     */
    public boolean stringTypeUnspecified() {
        return stringTypeUnspecified;
    }

    /**
     * Tells whether numeric getters read a boolean column as 1 or 0.
     *
     * @return the constructor argument
     */
    public boolean convertBooleanToNumeric() {
        return convertBooleanToNumeric;
    }
}
