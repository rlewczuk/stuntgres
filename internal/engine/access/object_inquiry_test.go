package access

import (
	"context"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func TestAccessNamespacesObjectPrivileges(t *testing.T) {
	f := newAccessFixture(t)
	ctx := context.Background()
	env := countingEnvironment{temporary: f.temporary.Address(), calls: new(int)}
	actor := f.view(t, "pg_monitor").State.CurrentUser
	check := func(actor uint64, address ids.ObjectAddress, ordinary, options authorization.Privileges, want, exists bool) {
		t.Helper()
		v := f.view(t, "postgres")
		got, present, err := v.ObjectPrivilege(ctx, env, actor, address, ordinary, options)
		if err != nil || got != want || present != exists {
			t.Fatalf("actor %d object %v masks %v/%v: got %v/%v/%v want %v/%v", actor, address, ordinary, options, got, present, err, want, exists)
		}
	}
	missing := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 4294967294}
	check(actor.ID(), missing, authorization.UsagePrivilege, 0, false, false)
	check(f.bootstrap.ID(), missing, authorization.UsagePrivilege, 0, true, true)
	check(4294967294, f.owner.DatabaseAddress(), authorization.ConnectPrivilege, 0, true, true)
	check(4294967294, f.owner.DatabaseAddress(), authorization.TemporaryPrivilege, 0, true, true)
	check(4294967294, f.owner.DatabaseAddress(), authorization.CreatePrivilege, 0, false, true)
	check(4294967294, f.public.Address(), authorization.UsagePrivilege, 0, false, true)

	ns := *f.public
	ns.Owner = actor
	ns.ACL = authorization.ACLValue{Explicit: true}
	f.read.State().SetDescriptor(ns.Address(), &ns)
	check(actor.ID(), ns.Address(), authorization.UsagePrivilege, 0, false, true)
	check(actor.ID(), ns.Address(), 0, authorization.UsagePrivilege, true, true)
	ns.ACL.Entries = []authorization.ACLEntry{{Grantor: actor, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.UsagePrivilege}}
	check(4294967294, ns.Address(), authorization.CreatePrivilege, 0, false, true)
	check(4294967294, ns.Address(), authorization.CreatePrivilege|authorization.UsagePrivilege, 0, true, true)
	check(4294967294, ns.Address(), 0, authorization.CreatePrivilege|authorization.UsagePrivilege, false, true)
	check(4294967294, ns.Address(), authorization.UsagePrivilege, authorization.CreatePrivilege, true, true)
	ns.ACL.Entries[0].GrantOptions = authorization.UsagePrivilege
	check(4294967294, ns.Address(), 0, authorization.UsagePrivilege, true, true)

	// TEMP belongs to the physical session, but CREATE uses the queried actor's
	// database TEMP right, not the expression invoker or namespace owner.
	database := *f.read.MustDatabase(f.owner.DatabaseAddress())
	database.ACL = authorization.ACLValue{Explicit: true}
	f.read.State().SetDescriptor(database.Address(), &database)
	check(actor.ID(), database.Address(), authorization.ConnectPrivilege, 0, false, true)
	check(actor.ID(), f.temporary.Address(), authorization.UsagePrivilege, 0, true, true)
	check(actor.ID(), f.temporary.Address(), authorization.CreatePrivilege, 0, false, true)
	check(actor.ID(), f.temporary.Address(), 0, authorization.UsagePrivilege, false, true)
	database.ACL.Entries = []authorization.ACLEntry{{Grantor: f.bootstrap, Grantee: authorization.ACLGrantee{Role: actor}, Privileges: authorization.TemporaryPrivilege}}
	check(actor.ID(), f.temporary.Address(), authorization.CreatePrivilege, 0, true, true)
	check(4294967294, f.temporary.Address(), authorization.CreatePrivilege, 0, false, true)
	env.temporary = ids.ObjectAddress{}
	check(actor.ID(), f.temporary.Address(), authorization.UsagePrivilege, 0, false, true)

	// Seeded monitor inherits pg_read_all_stats; use that edge for ownership.
	ns.Owner = f.view(t, "pg_read_all_stats").State.CurrentUser
	ns.ACL = authorization.ACLValue{Explicit: true}
	check(actor.ID(), ns.Address(), authorization.UsagePrivilege, 0, false, true)
	check(actor.ID(), ns.Address(), 0, authorization.UsagePrivilege, true, true)
	reader := f.view(t, "pg_read_all_data").State.CurrentUser
	check(reader.ID(), ns.Address(), authorization.UsagePrivilege, 0, true, true)
	check(reader.ID(), ns.Address(), authorization.CreatePrivilege, 0, false, true)
	check(reader.ID(), ns.Address(), 0, authorization.UsagePrivilege, false, true)
}

func TestAccessNamespacesTypeDefaults(t *testing.T) {
	f := newAccessFixture(t)
	var base, array *catalog.Type
	for _, typ := range sqltype.Builtins() {
		if typ.ID() != sqltype.Int4 && typ.ID() != 1007 {
			continue
		}
		d := &catalog.Type{SQLType: typ, ObjectNamespace: f.public.Address(), ObjectRevision: 1, Owner: f.bootstrap, Array: typ.AssociatedArray()}
		f.read.State().SetDescriptor(d.Address(), d)
		if typ.ID() == sqltype.Int4 {
			base = d
		} else {
			array = d
		}
	}
	if base == nil || array == nil {
		t.Fatal("missing builtin integer family")
	}
	env := countingEnvironment{calls: new(int)}
	for _, explicit := range []bool{false, true} {
		base.ACL = authorization.ACLValue{Explicit: explicit}
		for _, typ := range []*catalog.Type{base, array} {
			v := f.view(t, "postgres")
			got, exists, err := v.ObjectPrivilege(context.Background(), env, 4294967294, typ.Address(), authorization.UsagePrivilege, 0)
			if err != nil || !exists || got == explicit {
				t.Fatal("absent PUBLIC default versus explicit empty, delegated to base", explicit, typ.Address(), got, exists, err)
			}
		}
	}
	if *env.calls != 0 {
		t.Fatal("type lookup read session environment")
	}
}
