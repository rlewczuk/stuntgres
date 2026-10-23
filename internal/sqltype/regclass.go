package sqltype

import "fmt"

// ClassOID reads the physical relation identity without formatting or lookup.
func (v Value) ClassOID() (uint32, error) {
	if v.null || v.typ == nil || v.typ.BaseType().id != Regclass || v.integer != 1 {
		return 0, fmt.Errorf("not a relation identity")
	}
	return v.oid, nil
}
