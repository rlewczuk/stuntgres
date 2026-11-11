package org.stuntgres.core;

import java.sql.SQLException;
import java.sql.Types;
import java.util.HashMap;
import java.util.Locale;
import java.util.Map;

/**
 * The types the driver knows without asking the server, and the rules that turn a type
 * and its modifier into precision, scale and display size (design section 5). The rows
 * and the rules were recorded from the PostgreSQL JDBC driver; the test of this class
 * reads the recorded tables.
 */
public final class TypeTable {
    /** The length reported for a type without a limit. */
    public static final int UNKNOWN_LENGTH = Integer.MAX_VALUE;

    private static final Map<Integer, PgType> BY_OID = new HashMap<>();
    private static final Map<String, PgType> BY_NAME = new HashMap<>();
    private static final Map<String, String> ALIASES = new HashMap<>();

    static {
        add("bool", Oid.BOOL, Oid.BOOL_ARRAY, -7, "java.lang.Boolean", ',');
        add("int2", Oid.INT2, Oid.INT2_ARRAY, 5, "java.lang.Integer", ',');
        add("int4", Oid.INT4, Oid.INT4_ARRAY, 4, "java.lang.Integer", ',');
        add("int8", Oid.INT8, Oid.INT8_ARRAY, -5, "java.lang.Long", ',');
        add("oid", Oid.OID, Oid.OID_ARRAY, -5, "java.lang.Long", ',');
        add("float4", Oid.FLOAT4, Oid.FLOAT4_ARRAY, 7, "java.lang.Float", ',');
        add("float8", Oid.FLOAT8, Oid.FLOAT8_ARRAY, 8, "java.lang.Double", ',');
        add("numeric", Oid.NUMERIC, Oid.NUMERIC_ARRAY, 2, "java.math.BigDecimal", ',');
        add("money", Oid.MONEY, Oid.MONEY_ARRAY, 8, "org.stuntgres.util.PGmoney", ',');
        add("text", Oid.TEXT, Oid.TEXT_ARRAY, 12, "java.lang.String", ',');
        add("varchar", Oid.VARCHAR, Oid.VARCHAR_ARRAY, 12, "java.lang.String", ',');
        add("bpchar", Oid.BPCHAR, Oid.BPCHAR_ARRAY, 1, "java.lang.String", ',');
        add("name", Oid.NAME, Oid.NAME_ARRAY, 12, "java.lang.String", ',');
        add("char", Oid.CHAR, Oid.CHAR_ARRAY, 1, "java.lang.String", ',');
        add("bytea", Oid.BYTEA, Oid.BYTEA_ARRAY, -2, "[B", ',');
        add("date", Oid.DATE, Oid.DATE_ARRAY, 91, "java.sql.Date", ',');
        add("time", Oid.TIME, Oid.TIME_ARRAY, 92, "java.sql.Time", ',');
        add("timetz", Oid.TIMETZ, Oid.TIMETZ_ARRAY, 92, "java.sql.Time", ',');
        add("timestamp", Oid.TIMESTAMP, Oid.TIMESTAMP_ARRAY, 93, "java.sql.Timestamp", ',');
        add("timestamptz", Oid.TIMESTAMPTZ, Oid.TIMESTAMPTZ_ARRAY, 93, "java.sql.Timestamp", ',');
        add("interval", Oid.INTERVAL, Oid.INTERVAL_ARRAY, 1111, "org.stuntgres.util.PGInterval", ',');
        add("uuid", Oid.UUID, Oid.UUID_ARRAY, 1111, "java.util.UUID", ',');
        add("json", Oid.JSON, Oid.JSON_ARRAY, 1111, "org.stuntgres.util.PGobject", ',');
        add("jsonb", Oid.JSONB, Oid.JSONB_ARRAY, 1111, "org.stuntgres.util.PGobject", ',');
        add("xml", Oid.XML, Oid.XML_ARRAY, 2009, "java.sql.SQLXML", ',');
        add("bit", Oid.BIT, Oid.BIT_ARRAY, -7, "java.lang.Boolean", ',');
        add("varbit", Oid.VARBIT, Oid.VARBIT_ARRAY, 1111, "java.lang.String", ',');
        add("inet", Oid.INET, Oid.INET_ARRAY, 1111, "java.lang.String", ',');
        add("cidr", Oid.CIDR, Oid.CIDR_ARRAY, 1111, "java.lang.String", ',');
        add("macaddr", Oid.MACADDR, Oid.MACADDR_ARRAY, 1111, "java.lang.String", ',');
        add("macaddr8", Oid.MACADDR8, Oid.MACADDR8_ARRAY, 1111, "java.lang.String", ',');
        add("point", Oid.POINT, Oid.POINT_ARRAY, 1111, "org.stuntgres.geometric.PGpoint", ',');
        add("box", Oid.BOX, Oid.BOX_ARRAY, 1111, "org.stuntgres.geometric.PGbox", ';');
        add("circle", Oid.CIRCLE, Oid.CIRCLE_ARRAY, 1111, "org.stuntgres.geometric.PGcircle", ',');
        add("line", Oid.LINE, Oid.LINE_ARRAY, 1111, "org.stuntgres.geometric.PGline", ',');
        add("lseg", Oid.LSEG, Oid.LSEG_ARRAY, 1111, "org.stuntgres.geometric.PGlseg", ',');
        add("path", Oid.PATH, Oid.PATH_ARRAY, 1111, "org.stuntgres.geometric.PGpath", ',');
        add("polygon", Oid.POLYGON, Oid.POLYGON_ARRAY, 1111, "org.stuntgres.geometric.PGpolygon", ',');
        add("tsvector", Oid.TSVECTOR, Oid.TSVECTOR_ARRAY, 1111, "java.lang.String", ',');
        add("tsquery", Oid.TSQUERY, Oid.TSQUERY_ARRAY, 1111, "java.lang.String", ',');
        add("refcursor", Oid.REFCURSOR, Oid.REFCURSOR_ARRAY, 2012, "java.sql.ResultSet", ',');
        add("void", Oid.VOID, 0, 1111, "java.lang.String", ',');
        add("record", Oid.RECORD, Oid.RECORD_ARRAY, 1111, "java.lang.String", ',');
        add("unknown", Oid.UNKNOWN, 0, 1111, "java.lang.String", ',');
        add("regclass", Oid.REGCLASS, Oid.REGCLASS_ARRAY, 1111, "java.lang.String", ',');
        add("regtype", Oid.REGTYPE, Oid.REGTYPE_ARRAY, 1111, "java.lang.String", ',');
        add("regproc", Oid.REGPROC, Oid.REGPROC_ARRAY, 1111, "java.lang.String", ',');
        add("xid", Oid.XID, Oid.XID_ARRAY, 1111, "java.lang.String", ',');
        add("cid", Oid.CID, Oid.CID_ARRAY, 1111, "java.lang.String", ',');
        add("tid", Oid.TID, Oid.TID_ARRAY, 1111, "java.lang.String", ',');
        add("pg_lsn", Oid.PG_LSN, Oid.PG_LSN_ARRAY, 1111, "java.lang.String", ',');
        add("jsonpath", Oid.JSONPATH, Oid.JSONPATH_ARRAY, 1111, "java.lang.String", ',');
        add("int4range", Oid.INT4RANGE, Oid.INT4RANGE_ARRAY, 1111, "java.lang.String", ',');
        add("numrange", Oid.NUMRANGE, Oid.NUMRANGE_ARRAY, 1111, "java.lang.String", ',');
        add("tstzrange", Oid.TSTZRANGE, Oid.TSTZRANGE_ARRAY, 1111, "java.lang.String", ',');
        add("int2vector", Oid.INT2VECTOR, Oid.INT2VECTOR_ARRAY, 1111, "java.lang.String", ',');
        add("oidvector", Oid.OIDVECTOR, Oid.OIDVECTOR_ARRAY, 1111, "java.lang.String", ',');
        add("aclitem", Oid.ACLITEM, Oid.ACLITEM_ARRAY, 1111, "java.lang.String", ',');
        add("int4multirange", Oid.INT4MULTIRANGE, Oid.INT4MULTIRANGE_ARRAY, 1111, "java.lang.String", ',');
        String[] aliases = {"smallint", "int2", "int", "int4", "integer", "int4", "long", "int8",
            "bigint", "int8", "boolean", "bool", "float", "float8", "real", "float4",
            "double precision", "float8", "decimal", "numeric", "character varying", "varchar",
            "time without time zone", "time", "time with time zone", "timetz",
            "timestamp without time zone", "timestamp", "timestamp with time zone",
            "timestamptz"};
        for (int i = 0; i < aliases.length; i += 2) {
            ALIASES.put(aliases[i], aliases[i + 1]);
        }
    }

    private TypeTable() {
    }

    private static void add(String name, int oid, int arrayOid, int sqlType, String className,
            char delimiter) {
        PgType type = new PgType(oid, name, sqlType, className, arrayOid, 0, delimiter);
        BY_OID.put(oid, type);
        BY_NAME.put(name, type);
        if (arrayOid != 0) {
            PgType array = new PgType(arrayOid, "_" + name, Types.ARRAY, "java.sql.Array", 0, oid,
                    delimiter);
            BY_OID.put(arrayOid, array);
            BY_NAME.put("_" + name, array);
            BY_NAME.put(name + "[]", array);
        }
    }

    /**
     * Returns a type of the table.
     *
     * @param oid a type OID
     * @return the type, or null when the table does not hold it
     */
    public static PgType byOid(int oid) {
        return BY_OID.get(oid);
    }

    /**
     * Returns a type of the table by its exact name. An array type has the names
     * {@code _element} and {@code element[]}.
     *
     * @param name a type name in the table's spelling
     * @return the type, or null when the table has no type of this name
     */
    public static PgType byName(String name) {
        return BY_NAME.get(name);
    }

    /**
     * Returns the type with an OID from the table or, failing that, from the server.
     *
     * @param oid a type OID
     * @param lookup the access to the server's types
     * @return the type, or null when neither knows it
     * @throws SQLException when the lookup fails
     */
    public static PgType find(int oid, TypeLookup lookup) throws SQLException {
        PgType type = BY_OID.get(oid);
        return type != null ? type : lookup.byOid(oid);
    }

    /**
     * Returns the OID of the type an application named.
     *
     * @param name a type name
     * @param lookup the access to the server's types
     * @return the OID, or 0 when no type has this name
     * @throws SQLException when the lookup fails
     */
    public static int oidByName(String name, TypeLookup lookup) throws SQLException {
        PgType type = BY_NAME.get(name);
        return type != null ? type.oid() : lookup.oidByName(name);
    }

    /**
     * Returns the OID of the array type for an element type name. Besides the names of the
     * table, the SQL spellings such as {@code integer} and {@code double precision} are
     * accepted in any case.
     *
     * @param elementName the element type name
     * @param lookup the access to the server's types
     * @return the OID of the array type, or 0 when there is none
     * @throws SQLException when the lookup fails
     */
    public static int arrayOidByElementName(String elementName, TypeLookup lookup)
            throws SQLException {
        PgType type = BY_NAME.get(elementName);
        if (type == null) {
            String lower = elementName.toLowerCase(Locale.ROOT);
            String alias = ALIASES.get(lower);
            type = BY_NAME.get(alias != null ? alias : lower);
        }
        if (type != null && !type.isArray()) {
            return type.arrayOid();
        }
        return lookup.arrayOidByElementName(elementName);
    }

    /** Returns the element OID for an array type of the table, else the OID itself. */
    private static int base(int oid) {
        PgType type = BY_OID.get(oid);
        return type != null && type.isArray() ? type.elementOid() : oid;
    }

    /**
     * Returns the precision ResultSetMetaData.getPrecision reports.
     *
     * @param oid the type OID of the column
     * @param typmod the type modifier of the column
     * @return the precision; {@link #UNKNOWN_LENGTH} for a type without a limit
     */
    public static int precision(int oid, int typmod) {
        switch (base(oid)) {
            case Oid.INT2: return 5;
            case Oid.INT4: return 10;
            case Oid.OID: return 10;
            case Oid.INT8: return 19;
            case Oid.FLOAT4: return 8;
            case Oid.FLOAT8: return 17;
            case Oid.BOOL: return 1;
            case Oid.CHAR: return 1;
            case Oid.NUMERIC: return typmod == -1 ? 0 : ((typmod - 4) & 0xFFFF0000) >> 16;
            case Oid.BPCHAR:
            case Oid.VARCHAR: return typmod == -1 ? UNKNOWN_LENGTH : typmod - 4;
            case Oid.DATE:
            case Oid.TIME:
            case Oid.TIMETZ:
            case Oid.TIMESTAMP:
            case Oid.TIMESTAMPTZ:
            case Oid.INTERVAL: return displaySize(oid, typmod);
            case Oid.BIT: return typmod;
            case Oid.VARBIT: return typmod == -1 ? UNKNOWN_LENGTH : typmod;
            default: return UNKNOWN_LENGTH;
        }
    }

    /**
     * Returns the scale ResultSetMetaData.getScale reports.
     *
     * @param oid the type OID of the column
     * @param typmod the type modifier of the column
     * @return the scale; 0 for a type without one
     */
    public static int scale(int oid, int typmod) {
        switch (base(oid)) {
            case Oid.FLOAT4: return 8;
            case Oid.FLOAT8: return 17;
            case Oid.NUMERIC: return typmod == -1 ? 0 : (typmod - 4) & 0xFFFF;
            case Oid.TIME:
            case Oid.TIMETZ:
            case Oid.TIMESTAMP:
            case Oid.TIMESTAMPTZ: return typmod == -1 ? 6 : typmod;
            case Oid.INTERVAL: return typmod == -1 ? 6 : typmod & 0xFFFF;
            default: return 0;
        }
    }

    /**
     * Returns the size ResultSetMetaData.getColumnDisplaySize reports.
     *
     * @param oid the type OID of the column
     * @param typmod the type modifier of the column
     * @return the display size; {@link #UNKNOWN_LENGTH} for a type without a limit
     */
    public static int displaySize(int oid, int typmod) {
        int seconds;
        if (typmod == -1) {
            seconds = 7;
        } else if (typmod == 0) {
            seconds = 0;
        } else if (typmod == 1) {
            seconds = 3;
        } else {
            seconds = typmod + 1;
        }
        switch (base(oid)) {
            case Oid.INT2: return 6;
            case Oid.INT4: return 11;
            case Oid.OID: return 10;
            case Oid.INT8: return 20;
            case Oid.FLOAT4: return 15;
            case Oid.FLOAT8: return 25;
            case Oid.BOOL: return 1;
            case Oid.CHAR: return 1;
            case Oid.DATE: return 13;
            case Oid.TIME: return 8 + seconds;
            case Oid.TIMETZ: return 8 + seconds + 6;
            case Oid.TIMESTAMP: return 22 + seconds;
            case Oid.TIMESTAMPTZ: return 22 + seconds + 6;
            case Oid.INTERVAL: return 49;
            case Oid.BPCHAR:
            case Oid.VARCHAR: return typmod == -1 ? UNKNOWN_LENGTH : typmod - 4;
            case Oid.NUMERIC:
                if (typmod == -1) {
                    return 131089;
                }
                int digits = ((typmod - 4) >> 16) & 0xFFFF;
                return 1 + digits + (((typmod - 4) & 0xFFFF) != 0 ? 1 : 0);
            case Oid.BIT: return typmod;
            case Oid.VARBIT: return typmod == -1 ? UNKNOWN_LENGTH : typmod;
            default: return UNKNOWN_LENGTH;
        }
    }

    /**
     * Tells what ResultSetMetaData.isSigned reports.
     *
     * @param oid the type OID of the column
     * @return true for int2, int4, int8, float4, float8, numeric and their arrays
     */
    public static boolean isSigned(int oid) {
        switch (base(oid)) {
            case Oid.INT2:
            case Oid.INT4:
            case Oid.INT8:
            case Oid.FLOAT4:
            case Oid.FLOAT8:
            case Oid.NUMERIC: return true;
            default: return false;
        }
    }

    /**
     * Tells what ResultSetMetaData.isCaseSensitive reports.
     *
     * @param oid the type OID of the column
     * @return false for the numeric, boolean, bit string and date and time types, else true
     */
    public static boolean isCaseSensitive(int oid) {
        switch (base(oid)) {
            case Oid.OID:
            case Oid.INT2:
            case Oid.INT4:
            case Oid.INT8:
            case Oid.FLOAT4:
            case Oid.FLOAT8:
            case Oid.NUMERIC:
            case Oid.BOOL:
            case Oid.BIT:
            case Oid.VARBIT:
            case Oid.DATE:
            case Oid.TIME:
            case Oid.TIMETZ:
            case Oid.TIMESTAMP:
            case Oid.TIMESTAMPTZ:
            case Oid.INTERVAL: return false;
            default: return true;
        }
    }

    /**
     * Returns the largest precision a column of the type can be declared with.
     *
     * @param oid the type OID
     * @return 0 for a type without a declared precision
     */
    public static int maximumPrecision(int oid) {
        switch (base(oid)) {
            case Oid.NUMERIC: return 1000;
            case Oid.TIME:
            case Oid.TIMETZ:
            case Oid.TIMESTAMP:
            case Oid.TIMESTAMPTZ:
            case Oid.INTERVAL: return 6;
            case Oid.BPCHAR:
            case Oid.VARCHAR: return 10485760;
            case Oid.BIT:
            case Oid.VARBIT: return 83886080;
            default: return 0;
        }
    }
}
