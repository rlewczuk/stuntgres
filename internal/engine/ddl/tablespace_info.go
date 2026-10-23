package ddl

import (
	"fmt"
	"strconv"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// TablespaceLocation is pg_tablespace_location over the statement's retained
// catalog: the builtin tablespaces have the empty logical location, and any
// other OID would need physical directory inspection.
func TablespaceLocation(view catalog.Read, oid uint32, result *sqltype.Descriptor) (sqltype.Value, error) {
	if oid == 0 {
		oid = uint32(catalog.DefaultTablespaceOID)
	}
	if ids.OID(oid) != catalog.DefaultTablespaceOID && ids.OID(oid) != catalog.GlobalTablespaceOID {
		return sqltype.Value{}, sqlError("0A000", "pg_tablespace_location physical directory inspection is unsupported in embedded mode")
	}
	descriptor := view.Get(ids.ObjectAddress{ClassID: ids.TablespaceClassID, ObjectID: ids.OID(oid)})
	if err := (catalog.TablespaceProvider{}).Validate(view, descriptor); err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.String(result, ""), nil
}

// TablespaceDatabase is one row of pg_tablespace_databases over the
// statement's retained catalog. It returns the current database for
// pg_default; otherwise it returns NULL, not present, and the WARNING the
// caller emits.
func TablespaceDatabase(view catalog.Read, oid uint32, result *sqltype.Descriptor) (sqltype.Value, bool, *sqlstate.Error, error) {
	if ids.OID(oid) == catalog.DefaultTablespaceOID || ids.OID(oid) == catalog.GlobalTablespaceOID {
		descriptor := view.Get(ids.ObjectAddress{ClassID: ids.TablespaceClassID, ObjectID: ids.OID(oid)})
		if err := (catalog.TablespaceProvider{}).Validate(view, descriptor); err != nil {
			return sqltype.Value{}, false, nil, err
		}
	}
	if ids.OID(oid) == catalog.DefaultTablespaceOID {
		descriptor, ok := view.Database(view.Owner().DatabaseAddress())
		if !ok || descriptor == nil {
			return sqltype.Value{}, false, nil, fmt.Errorf("stuntgres: missing tablespace database authority")
		}
		if err := (catalog.DatabaseProvider{}).Validate(view, descriptor); err != nil {
			return sqltype.Value{}, false, nil, err
		}
		// Initialized native databases contain nonshared system catalog relations.
		// They prove logical occupancy even without any user relation or user row.
		value, err := sqltype.OIDValue(result, uint32(descriptor.Address().ObjectID))
		return value, true, nil, err
	}
	message := "global tablespace never has databases"
	if ids.OID(oid) != catalog.GlobalTablespaceOID {
		message = strconv.FormatUint(uint64(oid), 10) + " is not a tablespace OID"
	}
	return sqltype.Null(result), false, &sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000", Message: message}, nil
}
