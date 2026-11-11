package org.stuntgres.core;

import java.lang.reflect.Array;
import java.math.BigDecimal;
import java.math.RoundingMode;
import java.sql.SQLException;
import java.sql.Types;
import java.util.HashMap;
import java.util.Map;
import java.util.UUID;
import org.stuntgres.geometric.PGbox;
import org.stuntgres.geometric.PGcircle;
import org.stuntgres.geometric.PGline;
import org.stuntgres.geometric.PGlseg;
import org.stuntgres.geometric.PGpath;
import org.stuntgres.geometric.PGpoint;
import org.stuntgres.geometric.PGpolygon;
import org.stuntgres.util.PGInterval;
import org.stuntgres.util.PGmoney;
import org.stuntgres.util.PGobject;
import org.stuntgres.util.PSQLException;
import org.stuntgres.util.PSQLState;

/**
 * The default mapping of ResultSet.getObject(column) and the element mapping of
 * Array.getArray() (design sections 7.7 and 9.2). The value is the column text and is
 * never null. Columns of the JDBC types ARRAY, SQLXML and REF_CURSOR are not handled
 * here: the caller reads them with getArray, getSQLXML and a cursor fetch.
 */
public final class ObjectGetters {
    private static final Map<String, Class<? extends PGobject>> CLASSES = new HashMap<>();

    static {
        CLASSES.put("box", PGbox.class);
        CLASSES.put("circle", PGcircle.class);
        CLASSES.put("line", PGline.class);
        CLASSES.put("lseg", PGlseg.class);
        CLASSES.put("path", PGpath.class);
        CLASSES.put("point", PGpoint.class);
        CLASSES.put("polygon", PGpolygon.class);
        CLASSES.put("money", PGmoney.class);
        CLASSES.put("interval", PGInterval.class);
    }

    private ObjectGetters() {
    }

    /**
     * Reads a column as the Java object of its type.
     *
     * @param value the column text
     * @param type the type of the column
     * @param typmod the type modifier of the column
     * @param context the conversion context
     * @return the object of design table 7.7
     * @throws SQLException when the text is not a value of the type
     * @throws IllegalArgumentException for a column of JDBC type ARRAY, SQLXML or
     *         REF_CURSOR
     */
    public static Object getObject(String value, PgType type, int typmod,
            ConversionContext context) throws SQLException {
        int oid = type.oid();
        switch (type.sqlType()) {
            case Types.BOOLEAN:
            case Types.BIT:
                if (oid == Oid.BOOL || (oid == Oid.BIT && value.length() == 1)) {
                    return IntegerGetters.getBoolean(value, oid);
                }
                break;
            case Types.TINYINT:
            case Types.SMALLINT:
            case Types.INTEGER:
                return IntegerGetters.getInt(value, oid, context);
            case Types.BIGINT:
                return IntegerGetters.getLong(value, oid, context);
            case Types.NUMERIC:
            case Types.DECIMAL:
                return numeric(value, oid, typmod, context);
            case Types.REAL:
                return DecimalGetters.getFloat(value, oid, context);
            case Types.FLOAT:
            case Types.DOUBLE:
                return DecimalGetters.getDouble(value, oid, context);
            case Types.CHAR:
            case Types.VARCHAR:
            case Types.LONGVARCHAR:
                return value;
            case Types.DATE:
                return DateTimeGetters.getDate(value, null, context);
            case Types.TIME:
                return DateTimeGetters.getTime(value, null, context);
            case Types.TIMESTAMP:
                return DateTimeGetters.getTimestamp(value, oid, null, context);
            case Types.BINARY:
            case Types.VARBINARY:
            case Types.LONGVARBINARY:
                return TextGetters.getBytes(value, oid);
            case Types.ARRAY:
            case Types.SQLXML:
            case Types.REF_CURSOR:
                throw new IllegalArgumentException("not a scalar column: " + type.name());
            default:
                if (type.name().equals("unknown")) {
                    return value;
                }
                if (type.name().equals("uuid")) {
                    return uuid(value);
                }
                if (type.name().equals("hstore")) {
                    return HstoreText.parse(value);
                }
                break;
        }
        return pgObject(type.name(), value);
    }

    private static Object numeric(String value, int oid, int typmod, ConversionContext context)
            throws SQLException {
        String number = NumberText.withoutCurrency(value);
        if (number.equalsIgnoreCase("NaN")) {
            return Double.NaN;
        }
        if (number.equalsIgnoreCase("Infinity")) {
            return Double.POSITIVE_INFINITY;
        }
        if (number.equalsIgnoreCase("-Infinity")) {
            return Double.NEGATIVE_INFINITY;
        }
        BigDecimal decimal = DecimalGetters.getBigDecimal(value, oid, context);
        if (typmod == -1) {
            return decimal;
        }
        // the scale of the modifier: its low eleven bits as a signed number
        int scale = (((typmod - 4) & 0x7ff) ^ 0x400) - 0x400;
        return decimal.setScale(scale, RoundingMode.HALF_EVEN);
    }

    /**
     * Reads a UUID.
     *
     * @param value the text
     * @return the UUID
     * @throws SQLException with SQLState 22023 when the text is not a UUID
     */
    public static UUID uuid(String value) throws SQLException {
        try {
            return UUID.fromString(value);
        } catch (IllegalArgumentException e) {
            throw new PSQLException("Cannot read the value \"" + value + "\" as a UUID.",
                    PSQLState.INVALID_PARAMETER_VALUE, e);
        }
    }

    /**
     * Creates the value object of a type that has no other Java class: PGInterval, PGmoney
     * or a geometric class for their types, else a plain PGobject.
     *
     * @param typeName the type name
     * @param value the value text
     * @return the object with type and value set
     * @throws SQLException when the class of the type cannot parse the text
     */
    public static PGobject pgObject(String typeName, String value) throws SQLException {
        Class<? extends PGobject> known = CLASSES.get(typeName);
        PGobject object;
        try {
            object = known == null ? new PGobject() : known.getDeclaredConstructor().newInstance();
        } catch (ReflectiveOperationException e) {
            throw new PSQLException("Cannot create an object for the type " + typeName + ".",
                    PSQLState.CONNECTION_FAILURE, e);
        }
        object.setType(typeName);
        object.setValue(value);
        return object;
    }

    /**
     * Converts array text to the Java array Array.getArray() returns.
     *
     * @param text the column text
     * @param element the element type
     * @param context the conversion context
     * @return an array with one dimension per nesting level; its component class follows
     *         design table 9.2
     * @throws SQLException when the text is not an array or an element is not a value of
     *         the element type
     */
    public static Object getArray(String text, PgType element, ConversionContext context)
            throws SQLException {
        Object[] parsed = ArrayText.parse(text, element.delimiter());
        return convert(parsed, ArrayText.dimensions(parsed), element, context);
    }

    private static Class<?> elementClass(PgType element) {
        switch (element.oid()) {
            case Oid.INT2: return Short.class;
            case Oid.INT4: return Integer.class;
            case Oid.INT8:
            case Oid.OID: return Long.class;
            case Oid.FLOAT4: return Float.class;
            case Oid.FLOAT8:
            case Oid.MONEY: return Double.class;
            case Oid.NUMERIC: return BigDecimal.class;
            case Oid.BOOL:
            case Oid.BIT: return Boolean.class;
            case Oid.TEXT:
            case Oid.VARCHAR:
            case Oid.BPCHAR:
            case Oid.NAME:
            case Oid.CHAR:
            case Oid.JSON:
            case Oid.JSONB: return String.class;
            case Oid.BYTEA: return byte[].class;
            case Oid.DATE: return java.sql.Date.class;
            case Oid.TIME:
            case Oid.TIMETZ: return java.sql.Time.class;
            case Oid.TIMESTAMP:
            case Oid.TIMESTAMPTZ: return java.sql.Timestamp.class;
            case Oid.UUID: return UUID.class;
            default:
                int sqlType = element.sqlType();
                return sqlType == Types.CHAR || sqlType == Types.VARCHAR ? String.class
                        : Object.class;
        }
    }

    private static Object convert(Object[] parsed, int dimensions, PgType element,
            ConversionContext context) throws SQLException {
        Class<?> component = elementClass(element);
        for (int i = 1; i < dimensions; i++) {
            component = Array.newInstance(component, 0).getClass();
        }
        Object result = Array.newInstance(component, parsed.length);
        for (int i = 0; i < parsed.length; i++) {
            Object item = parsed[i];
            if (dimensions > 1) {
                item = convert((Object[]) item, dimensions - 1, element, context);
            } else if (item != null) {
                item = element((String) item, element, context);
            }
            Array.set(result, i, item);
        }
        return result;
    }

    private static Object element(String value, PgType element, ConversionContext context)
            throws SQLException {
        int oid = element.oid();
        switch (oid) {
            case Oid.INT2:
                return (short) NumberText.integer(value, Short.MIN_VALUE, Short.MAX_VALUE, "short");
            case Oid.INT4:
                return (int) NumberText.integer(value, Integer.MIN_VALUE, Integer.MAX_VALUE, "int");
            case Oid.INT8:
            case Oid.OID:
                return NumberText.integer(value, Long.MIN_VALUE, Long.MAX_VALUE, "long");
            case Oid.FLOAT4:
                return NumberText.real(value);
            case Oid.FLOAT8:
            case Oid.MONEY:
                return NumberText.doublePrecision(value);
            case Oid.NUMERIC:
                return NumberText.decimal(value);
            case Oid.BOOL:
            case Oid.BIT:
                return BooleanText.parse(value);
            case Oid.BYTEA:
                return ByteaText.decode(value);
            case Oid.DATE:
                return DateTimeGetters.getDate(value, null, context);
            case Oid.TIME:
            case Oid.TIMETZ:
                return DateTimeGetters.getTime(value, null, context);
            case Oid.TIMESTAMP:
            case Oid.TIMESTAMPTZ:
                return DateTimeGetters.getTimestamp(value, oid, null, context);
            case Oid.UUID:
                return uuid(value);
            default:
                return elementClass(element) == String.class ? value
                        : pgObject(element.name(), value);
        }
    }
}
