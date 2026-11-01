package core

import (
	"fmt"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/sqltype"
)

func LookupObjectAddress(h CatalogHost, c *exec.Context, values [3]sqltype.Value) (ids.ObjectAddress, error) {
	if read, _ := h.CommentSnapshot(); read.State() == nil {
		return ids.ObjectAddress{}, fmt.Errorf("stuntgres: missing object address snapshot")
	}
	lookup := h.LookupAddress()
	return lookup.Lookup(c.Context, values)
}

func IdentifyObject(h CatalogHost, c *exec.Context, values [3]sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error) {
	return identifyObjectValues(h, values, result, functions.IdentifyObject)
}

func IdentifyObjectAddress(h CatalogHost, c *exec.Context, values [3]sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error) {
	return identifyObjectValues(h, values, result, functions.IdentifyObjectAddress)
}

func DescribeObject(h CatalogHost, c *exec.Context, values [3]sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error) {
	return identifyObjectValues(h, values, result, functions.DescribeObject)
}
func identifyObjectValues(h CatalogHost, values [3]sqltype.Value, result *sqltype.Descriptor, operation functions.ObjectAddressOperation) (sqltype.Value, error) {
	read, roles := h.CommentSnapshot()
	if read.State() == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing object address snapshot")
	}
	return regobj.IdentifyValues(read, roles, h.DeparsePath(), values, result, operation, h.Operation().Session.SessionToken)
}
