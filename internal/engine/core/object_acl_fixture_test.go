package core_test

import (
	"fmt"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
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

func TestWithObjectACLCopiesDescriptor(t *testing.T) {
	principals := &authorization.Domain{}
	owner := principals.Principal(authorization.BootstrapPrincipalID)
	acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.UsagePrivilege}}}
	namespace := &catalog.Namespace{ObjectName: "s", Owner: owner}
	next, err := withObjectACL(namespace, 0, acl)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := next.(*catalog.Namespace)
	if !ok || got == namespace || !got.ACL.Explicit || len(got.ACL.Entries) != 1 || namespace.ACL.Explicit {
		t.Fatal("namespace ACL not set on a copy", got, namespace)
	}
	typ := &catalog.Type{SQLType: sqltype.LookupOnly(910, "mood", 911), ACLSyntheticDefault: true}
	next, err = withObjectACL(typ, 0, acl)
	if err != nil {
		t.Fatal(err)
	}
	if n := next.(*catalog.Type); n.ACLSyntheticDefault || !n.ACL.Explicit || !typ.ACLSyntheticDefault {
		t.Fatal("type ACL kept the synthetic default or changed the original", n)
	}
}

func TestWithObjectACLColumns(t *testing.T) {
	principals := &authorization.Domain{}
	owner := principals.Principal(authorization.BootstrapPrincipalID)
	kept := authorization.ACLValue{Explicit: true}
	relation := &catalog.Relation{Attributes: []int32{1, 2}}
	relation.Security.ColumnACL = map[int32]authorization.ACLValue{1: kept}
	column := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.SelectPrivilege}}}
	next, err := withObjectACL(relation, 2, column)
	if err != nil {
		t.Fatal(err)
	}
	added := next.(*catalog.Relation)
	if len(added.Security.ColumnACL) != 2 || len(added.Security.ColumnACL[2].Entries) != 1 || len(relation.Security.ColumnACL) != 1 || added.Security.ACL.Explicit {
		t.Fatal("column ACL not added on a copied map", added.Security, relation.Security)
	}
	next, err = withObjectACL(added, 1, authorization.ACLValue{})
	if err != nil {
		t.Fatal(err)
	}
	if removed := next.(*catalog.Relation); len(removed.Security.ColumnACL) != 1 || len(added.Security.ColumnACL) != 2 {
		t.Fatal("absent column ACL not removed on a copied map", removed.Security)
	}
	next, err = withObjectACL(relation, 0, column)
	if err != nil {
		t.Fatal(err)
	}
	if table := next.(*catalog.Relation); !table.Security.ACL.Explicit || len(table.Security.ColumnACL) != 1 {
		t.Fatal("table ACL not set", table.Security)
	}
}

func TestWithObjectACLRejectsTargets(t *testing.T) {
	element := sqltype.LookupOnly(910, "mood", 911)
	for _, c := range []struct {
		d     catalog.Descriptor
		subID int32
		want  string
	}{
		{&catalog.Database{}, 1, "stuntgres: database subobject"},
		{&catalog.Namespace{}, 1, "stuntgres: namespace subobject"},
		{&catalog.Type{SQLType: element}, 1, "stuntgres: invalid type ACL target"},
		{&catalog.Type{SQLType: sqltype.LookupArray(911, "_mood", element)}, 0, "stuntgres: invalid type ACL target"},
		{&catalog.Procedure{}, 1, "stuntgres: procedure subobject"},
		{&catalog.Parameter{}, 1, "stuntgres: parameter subobject"},
		{&catalog.Tablespace{}, 1, "stuntgres: tablespace subobject"},
		{&catalog.LargeObject{}, 1, "stuntgres: large object subobject"},
		{&catalog.Relation{Attributes: []int32{1}}, 2, "stuntgres: invalid ACL column"},
		{&catalog.Relation{Attributes: []int32{1}}, -1, "stuntgres: invalid ACL column"},
		{&catalog.Collation{}, 0, "stuntgres: unsupported ACL object"},
	} {
		if _, err := withObjectACL(c.d, c.subID, authorization.ACLValue{}); err == nil || err.Error() != c.want {
			t.Errorf("%T sub %d: %v, want %q", c.d, c.subID, err, c.want)
		}
	}
}
