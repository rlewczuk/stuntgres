package access

import (
	"context"
	"errors"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"testing"
)

func TestAccessRelationsRights(t *testing.T) {
	f := newAccessFixture(t)
	v := f.view(t, "pg_monitor")
	owner := v.State.CurrentUser
	r := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 42000}, ObjectNamespace: f.public.Address(), ObjectName: "r", ObjectRevision: 1, Owner: owner, RelationKind: catalog.SequenceRelation, Attributes: []int32{1, 3}, Security: catalog.RelationSecurity{ColumnACL: map[int32]authorization.ACLValue{3: {Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.SelectPrivilege}}}}}}
	state := catalog.CloneState(f.read.State())
	state.SetDescriptor(r.Address(), r)
	f.read = f.read.WithState(state)
	v = f.view(t, "pg_monitor")
	check := func(actor uint64, sub int32, any bool, ordinary, options authorization.Privileges, want bool) {
		t.Helper()
		got, err := v.RelationPrivilege(context.Background(), actor, r, sub, any, ordinary, options)
		if err != nil || got != want {
			t.Fatalf("actor=%d sub=%d any=%v masks=%v/%v: %v %v", actor, sub, any, ordinary, options, got, err)
		}
	}
	check(owner.ID(), 0, false, authorization.SelectPrivilege, 0, true)
	check(owner.ID(), 0, false, authorization.DeletePrivilege, 0, false)
	check(owner.ID(), 0, false, 0, authorization.DeletePrivilege, true)
	check(999999, 0, false, authorization.SelectPrivilege, 0, false)
	check(999999, 1, false, authorization.SelectPrivilege, 0, false)
	check(999999, 3, false, authorization.SelectPrivilege, 0, true)
	check(999999, 0, true, authorization.SelectPrivilege, 0, true)
	check(999999, 0, true, 0, authorization.SelectPrivilege, false)
	check(f.bootstrap.ID(), 0, false, authorization.DeletePrivilege, 0, true)
	// Explicit-empty ordinary rights do not erase the owner's implicit options.
	next := *r
	next.Security.ACL = authorization.ACLValue{Explicit: true}
	state = catalog.CloneState(f.read.State())
	state.SetDescriptor(r.Address(), &next)
	f.read = f.read.WithState(state)
	v = f.view(t, "pg_monitor")
	r = &next
	check(owner.ID(), 0, false, authorization.SelectPrivilege, 0, false)
	check(owner.ID(), 0, false, 0, authorization.SelectPrivilege, true)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := v.RelationPrivilege(ctx, owner.ID(), r, 0, true, authorization.SelectPrivilege, 0); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation", err)
	}
}
