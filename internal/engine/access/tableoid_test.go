package access

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"testing"
)

func TestQueryTableOIDAdmission(t *testing.T) {
	f := newAccessFixture(t)
	v := f.view(t, "pg_monitor")
	reader := v.State.CurrentUser
	grant := authorization.ACLEntry{Grantor: f.bootstrap, Grantee: authorization.ACLGrantee{Role: reader}, Privileges: authorization.SelectPrivilege}
	r := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 21000}, ObjectName: "identity", ObjectNamespace: f.public.Address(), Owner: f.bootstrap, Attributes: []int32{1}, Columns: []catalog.Column{{Name: "a"}}, Security: catalog.RelationSecurity{ACL: authorization.ACLValue{Explicit: true}, ColumnACL: map[int32]authorization.ACLValue{1: {Explicit: true, Entries: []authorization.ACLEntry{grant}}}}}
	f.read.State().SetDescriptor(r.Address(), r)
	ref := v.Ref(r.Address())
	env := countingEnvironment{calls: new(int)}
	for _, uses := range [][]int{nil, {0}} {
		if err := v.CheckColumns(env, ref, r, authorization.SelectPrivilege, uses); err != nil {
			t.Fatal("ordinary column or presence read lost its grant", err)
		}
		if err := v.Check(env, Requirement{Object: ref, Privileges: authorization.SelectPrivilege}); err == nil {
			t.Fatal("ordinary column grant admitted system attribute")
		}
	}
	copy := *r
	copy.Security.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{grant}}
	f.read.State().SetDescriptor(copy.Address(), &copy)
	if err := v.Check(env, Requirement{Object: ref, Privileges: authorization.SelectPrivilege}); err != nil {
		t.Fatal("table SELECT rejected", err)
	}
	if !copy.HasSupportedAttribute(-6) || copy.HasAttribute(-6) {
		t.Fatal("system identity became a dense column")
	}
}
