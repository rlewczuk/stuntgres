package org.stuntgres.core;

/** What the driver knows about one PostgreSQL type (design section 5). */
public final class PgType {
    private final int oid;
    private final String name;
    private final int sqlType;
    private final String className;
    private final int arrayOid;
    private final int elementOid;
    private final char delimiter;

    /**
     * Creates a type description.
     *
     * @param oid the type OID
     * @param name the name ResultSetMetaData.getColumnTypeName reports
     * @param sqlType the java.sql.Types code
     * @param className the name ResultSetMetaData.getColumnClassName reports
     * @param arrayOid the OID of the array type over this type, or 0
     * @param elementOid the OID of the element type when this is an array type, else 0
     * @param delimiter the character between the elements of an array of this type; for an
     *        array type, the delimiter of its elements
     */
    public PgType(int oid, String name, int sqlType, String className, int arrayOid,
            int elementOid, char delimiter) {
        this.oid = oid;
        this.name = name;
        this.sqlType = sqlType;
        this.className = className;
        this.arrayOid = arrayOid;
        this.elementOid = elementOid;
        this.delimiter = delimiter;
    }

    /**
     * Returns the type OID.
     *
     * @return the OID
     */
    public int oid() {
        return oid;
    }

    /**
     * Returns the type name.
     *
     * @return the name ResultSetMetaData.getColumnTypeName reports
     */
    public String name() {
        return name;
    }

    /**
     * Returns the JDBC type.
     *
     * @return a constant of java.sql.Types
     */
    public int sqlType() {
        return sqlType;
    }

    /**
     * Returns the Java class of the default mapping.
     *
     * @return the name ResultSetMetaData.getColumnClassName reports
     */
    public String className() {
        return className;
    }

    /**
     * Returns the array type over this type.
     *
     * @return its OID, or 0 when there is none
     */
    public int arrayOid() {
        return arrayOid;
    }

    /**
     * Returns the element type of an array type.
     *
     * @return its OID, or 0 when this is not an array type
     */
    public int elementOid() {
        return elementOid;
    }

    /**
     * Tells whether this is an array type.
     *
     * @return true when {@link #elementOid()} is not 0
     */
    public boolean isArray() {
        return elementOid != 0;
    }

    /**
     * Returns the element delimiter of array text.
     *
     * @return a comma, or a semicolon for box
     */
    public char delimiter() {
        return delimiter;
    }
}
