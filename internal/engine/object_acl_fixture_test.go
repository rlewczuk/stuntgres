package engine

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
)

// withObjectACL backs the SetObjectACL fixture. It returns a copy of d whose
// ACL is acl: the object's own ACL when subID is 0, otherwise the ACL of
// column subID of a relation (an absent acl removes it; the column map is
// copied, never written). acl is stored as given, so the caller owns its
// entries. Types reset their synthetic default.
func withObjectACL(d catalog.Descriptor, subID int32, acl authorization.ACLValue) (catalog.Descriptor, error) {
	switch d := d.(type) {
	case *catalog.Database:
		if subID != 0 {
			return nil, fmt.Errorf("stuntgres: database subobject")
		}
		n := *d
		n.ACL = acl
		return &n, nil
	case *catalog.Namespace:
		if subID != 0 {
			return nil, fmt.Errorf("stuntgres: namespace subobject")
		}
		n := *d
		n.ACL = acl
		return &n, nil
	case *catalog.Type:
		if subID != 0 || d.SQLType.IsArray() {
			return nil, fmt.Errorf("stuntgres: invalid type ACL target")
		}
		n := *d
		n.ACL = acl
		n.ACLSyntheticDefault = false
		return &n, nil
	case *catalog.Procedure:
		if subID != 0 {
			return nil, fmt.Errorf("stuntgres: procedure subobject")
		}
		n := *d
		n.ACL = acl
		return &n, nil
	case *catalog.Language:
		if subID != 0 {
			return nil, fmt.Errorf("stuntgres: language subobject")
		}
		n := *d
		n.ACL = acl
		return &n, nil
	case *catalog.Parameter:
		if subID != 0 {
			return nil, fmt.Errorf("stuntgres: parameter subobject")
		}
		n := *d
		n.ACL = acl
		return &n, nil
	case *catalog.Tablespace:
		if subID != 0 {
			return nil, fmt.Errorf("stuntgres: tablespace subobject")
		}
		n := *d
		n.ACL = acl
		return &n, nil
	case *catalog.LargeObject:
		if subID != 0 {
			return nil, fmt.Errorf("stuntgres: large object subobject")
		}
		n := *d
		n.ACL = acl
		return &n, nil
	case *catalog.Relation:
		if subID < 0 || subID > 0 && !d.HasAttribute(subID) {
			return nil, fmt.Errorf("stuntgres: invalid ACL column")
		}
		n := *d
		if subID == 0 {
			n.Security.ACL = acl
		} else {
			n.Security.ColumnACL = make(map[int32]authorization.ACLValue, len(d.Security.ColumnACL)+1)
			for k, v := range d.Security.ColumnACL {
				n.Security.ColumnACL[k] = v
			}
			if acl.Explicit {
				n.Security.ColumnACL[subID] = acl
			} else {
				delete(n.Security.ColumnACL, subID)
			}
		}
		return &n, nil
	default:
		return nil, fmt.Errorf("stuntgres: unsupported ACL object")
	}
}
