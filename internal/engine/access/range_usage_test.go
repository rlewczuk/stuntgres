package access

import (
	"context"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestRangeTypeEffectiveUsageDelegation(t *testing.T) {
	f := newAccessFixture(t)
	state := catalog.CloneState(f.read.State())
	ns := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, ObjectRevision: 1, ObjectName: "pg_catalog", Owner: f.bootstrap}
	state.SetDescriptor(ns.Address(), ns)
	state.BindName(catalog.DescriptorNameKey(ns), ns.Address())
	var r, m *sqltype.Descriptor
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == sqltype.Int4Range {
			r = typ
		}
		if typ.ID() == sqltype.Int4Multirange {
			m = typ
		}
	}
	if r == nil || m == nil {
		t.Fatal("missing actual builtin range family")
	}
	selected := map[sqltype.ID]bool{r.ID(): true, m.ID(): true, r.AssociatedArray(): true, m.AssociatedArray(): true}
	for _, typ := range sqltype.Builtins() {
		if selected[typ.ID()] {
			d := &catalog.Type{SQLType: typ, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, ObjectRevision: 1, Owner: f.bootstrap, Array: typ.AssociatedArray(), ACL: authorization.DefaultBaseline(f.bootstrap, authorization.DefaultTypes, false), ACLSyntheticDefault: true}
			state.SetDescriptor(d.Address(), d)
		}
	}
	f.read = catalog.NewRead(f.owner, state)
	actor := f.view(t, "pg_monitor").State.CurrentUser
	inherited := f.view(t, "pg_read_all_stats").State.CurrentUser
	for _, grantee := range []authorization.PrincipalRef{actor, inherited} {
		for _, granted := range []bool{false, true, false} {
			owner := *f.read.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(r.ID())})
			owner.ACL = authorization.ACLValue{Explicit: true}
			owner.ACLSyntheticDefault = false
			if granted {
				owner.ACL.Entries = []authorization.ACLEntry{{Grantor: f.bootstrap, Grantee: authorization.ACLGrantee{Role: grantee}, Privileges: authorization.UsagePrivilege, GrantOptions: authorization.UsagePrivilege}}
			}
			state.SetDescriptor(owner.Address(), &owner)
			v := f.view(t, "pg_monitor")
			calls := 0
			env := countingEnvironment{calls: &calls}
			want := authorization.Privileges(0)
			if granted {
				want = authorization.UsagePrivilege
			}
			for id := range selected {
				for _, options := range []bool{false, true} {
					got, err := v.Effective(env, v.Ref(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(id)}), options)
					if err != nil || got != want {
						t.Fatal("range authority must decide range/companion/both arrays, including inherited grant options", id, options, got, want, err)
					}
					ordinaryMask, optionMask := authorization.UsagePrivilege, authorization.Privileges(0)
					if options {
						ordinaryMask, optionMask = 0, authorization.UsagePrivilege
					}
					inquiry, exists, err := v.ObjectPrivilege(context.Background(), env, actor.ID(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(id)}, ordinaryMask, optionMask)
					if err != nil || !exists || inquiry != granted {
						t.Fatal("raw inquiry lost current inherited range/array authority", id, options, inquiry, exists, err)
					}
				}
			}
			if calls != 0 {
				t.Fatal("type ACL delegation consulted session TEMP state", calls)
			}
			for id := range selected {
				err := v.Check(env, Requirement{Object: v.Ref(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(id)}), Privileges: authorization.UsagePrivilege})
				if granted && err != nil || !granted && (err == nil || !strings.Contains(err.Error(), "permission denied for type")) {
					t.Fatal("Check disagrees with actual delegated usage", id, granted, err)
				}
			}
		}
	}
}
