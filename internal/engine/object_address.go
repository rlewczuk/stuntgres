package engine

import (
	"fmt"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/sqltype"
)

func (h *statementHost) LookupObjectAddress(c *exec.Context, values [3]sqltype.Value) (ids.ObjectAddress, error) {
	scope := h.scope()
	if scope.inquiry.commentCatalog.State() == nil {
		return ids.ObjectAddress{}, fmt.Errorf("stuntgres: missing object address snapshot")
	}
	return lookupObjectAddress(c.Context, scope, values)
}

func (h *statementHost) IdentifyObject(c *exec.Context, values [3]sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error) {
	return identifyObjectValues(c, values, result, functions.IdentifyObject)
}

func (h *statementHost) IdentifyObjectAddress(c *exec.Context, values [3]sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error) {
	return identifyObjectValues(c, values, result, functions.IdentifyObjectAddress)
}

func (h *statementHost) DescribeObject(c *exec.Context, values [3]sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error) {
	return identifyObjectValues(c, values, result, functions.DescribeObject)
}
func identifyObjectValues(c *exec.Context, values [3]sqltype.Value, result *sqltype.Descriptor, operation functions.ObjectAddressOperation) (sqltype.Value, error) {
	scope := scopeOf(c.LiveHost())
	if scope == nil || scope.inquiry.commentCatalog.State() == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing object address snapshot")
	}
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
	address := ids.ObjectAddress{ids.OID(class), ids.OID(object), int32(sub)}
	if operation == functions.DescribeObject && class == 0 && object == 0 {
		return sqltype.Null(result), nil
	}
	identity, err := regobj.IdentifyAddress(scope.inquiry.commentCatalog, scope.inquiry.commentRoles, scope.inquiry.deparsePath, address)
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
			fields[1], err = regobj.ObjectAddressTextArray(result.CompositeMember(1).Usage.Type, identity.Names)
			if err != nil {
				return sqltype.Value{}, err
			}
			fields[2], err = regobj.ObjectAddressTextArray(result.CompositeMember(2).Usage.Type, identity.Args)
			if err != nil {
				return sqltype.Value{}, err
			}
		}
	}
	return sqltype.OwnedCompositeValue(result, fields)
}
