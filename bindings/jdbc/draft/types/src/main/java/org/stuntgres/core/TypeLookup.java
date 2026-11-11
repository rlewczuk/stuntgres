package org.stuntgres.core;

import java.sql.SQLException;

/**
 * Finds the types {@link TypeTable} does not hold: enums, domains, composites, ranges and
 * extension types. JDBC-DESIGN-CORE implements it with catalog queries; the conversion
 * code calls it only after the table had no answer.
 */
public interface TypeLookup {
    /**
     * Describes the type with an OID.
     *
     * @param oid a type OID that is not in the table
     * @return the type, or null when the server has no type with this OID
     * @throws SQLException when the server cannot be asked
     */
    PgType byOid(int oid) throws SQLException;

    /**
     * Finds the OID of a type by the name an application wrote.
     *
     * @param name a type name that is not a name of the table
     * @return the OID, or 0 when no type has this name
     * @throws SQLException when the server cannot be asked
     */
    int oidByName(String name) throws SQLException;

    /**
     * Finds the array type whose elements have the named type.
     *
     * @param elementName the element type name an application wrote
     * @return the OID of the array type, or 0 when there is none
     * @throws SQLException when the server cannot be asked
     */
    int arrayOidByElementName(String elementName) throws SQLException;
}
