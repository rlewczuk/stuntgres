package access

import (
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
)

func TestWithObjectACLCopiesDescriptor(t *testing.T) {
	principals := &authorization.Domain{}
	owner := principals.Principal(authorization.BootstrapPrincipalID)
	acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.UsagePrivilege}}}
	namespace := &catalog.Namespace{ObjectName: "s", Owner: owner}
	next, err := WithObjectACL(namespace, 0, acl)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := next.(*catalog.Namespace)
	if !ok || got == namespace || !got.ACL.Explicit || len(got.ACL.Entries) != 1 || namespace.ACL.Explicit {
		t.Fatal("namespace ACL not set on a copy", got, namespace)
	}
	typ := &catalog.Type{SQLType: sqltype.LookupOnly(910, "mood", 911), ACLSyntheticDefault: true}
	next, err = WithObjectACL(typ, 0, acl)
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
	next, err := WithObjectACL(relation, 2, column)
	if err != nil {
		t.Fatal(err)
	}
	added := next.(*catalog.Relation)
	if len(added.Security.ColumnACL) != 2 || len(added.Security.ColumnACL[2].Entries) != 1 || len(relation.Security.ColumnACL) != 1 || added.Security.ACL.Explicit {
		t.Fatal("column ACL not added on a copied map", added.Security, relation.Security)
	}
	next, err = WithObjectACL(added, 1, authorization.ACLValue{})
	if err != nil {
		t.Fatal(err)
	}
	if removed := next.(*catalog.Relation); len(removed.Security.ColumnACL) != 1 || len(added.Security.ColumnACL) != 2 {
		t.Fatal("absent column ACL not removed on a copied map", removed.Security)
	}
	next, err = WithObjectACL(relation, 0, column)
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
		if _, err := WithObjectACL(c.d, c.subID, authorization.ACLValue{}); err == nil || err.Error() != c.want {
			t.Errorf("%T sub %d: %v, want %q", c.d, c.subID, err, c.want)
		}
	}
}
