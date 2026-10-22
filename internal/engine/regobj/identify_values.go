package regobj

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// IdentifyValues answers pg_identify_object, pg_identify_object_as_address
// and pg_describe_object for (classid, objid, objsubid) over the statement's
// comment catalog, roles and deparse path.
func IdentifyValues(view catalog.Read, roles authorization.Read, path []ids.ObjectAddress, values [3]sqltype.Value, result *sqltype.Descriptor, operation functions.ObjectAddressOperation) (sqltype.Value, error) {
	class, err := values[0].OID()
	if err != nil {
		return sqltype.Value{}, err
	}
	object, err := values[1].OID()
	if err != nil {
		return sqltype.Value{}, err
	}
	sub, err := values[2].Integer()
	if err != nil {
		return sqltype.Value{}, err
	}
	address := ids.ObjectAddress{ClassID: ids.OID(class), ObjectID: ids.OID(object), SubID: int32(sub)}
	if operation == functions.DescribeObject && class == 0 && object == 0 {
		return sqltype.Null(result), nil
	}
	identity, err := IdentifyAddress(view, roles, path, address)
	if err != nil {
		return sqltype.Value{}, err
	}
	if operation == functions.DescribeObject {
		if !identity.Found {
			return sqltype.Null(result), nil
		}
		return sqltype.String(result, identity.Description), nil
	}
	fields := make([]sqltype.Value, result.CompositeLen())
	for i := range fields {
		fields[i] = sqltype.Null(result.CompositeMember(i).Usage.Type)
	}
	fields[0] = sqltype.String(result.CompositeMember(0).Usage.Type, identity.Kind)
	if identity.Found {
		if operation == functions.IdentifyObject {
			for i, text := range []string{identity.Schema, identity.Name, identity.Identity} {
				if text != "" {
					fields[i+1] = sqltype.String(result.CompositeMember(i+1).Usage.Type, text)
				}
			}
		} else {
			fields[1], err = ObjectAddressTextArray(result.CompositeMember(1).Usage.Type, identity.Names)
			if err != nil {
				return sqltype.Value{}, err
			}
			fields[2], err = ObjectAddressTextArray(result.CompositeMember(2).Usage.Type, identity.Args)
			if err != nil {
				return sqltype.Value{}, err
			}
		}
	}
	return sqltype.OwnedCompositeValue(result, fields)
}
