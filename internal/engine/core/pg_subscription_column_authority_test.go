package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func TestPGSubscriptionColumnAuthority(t *testing.T) {
	e := engine.New()
	enginetest.AclUnitRoles(e, "subscription_unit_reader")
	v := e.CatalogRead()
	roles := e.Roles()
	a := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 6100}
	d := v.MustRelation(a)
	auth := e.AuthorityView(access.View{Owner: &e.Catalog.Owner, PairedCatalog: v, Catalog: v, Roles: roles, RoleRecord: e.RoleStore.Published, State: authorization.RoleState{CurrentUser: roles.LookupRef("subscription_unit_reader")}})
	ref := auth.Ref(a)
	whole, err := auth.Effective(ref, false)
	if err != nil || whole&authorization.SelectPrivilege != 0 {
		t.Fatal("unexpected whole-table SELECT", whole, err)
	}
	var public, all []int
	for i := range d.Columns {
		all = append(all, i)
		if i != 13 {
			public = append(public, i)
		}
		column := ref
		column.Address.SubID = int32(i + 1)
		rights, err := auth.Effective(column, false)
		if err != nil || (rights&authorization.SelectPrivilege != 0) != (i != 13) {
			t.Fatal("effective column", i, rights, err)
		}
	}
	for _, indices := range [][]int{nil, public, {0}, {17}} {
		if err := auth.CheckColumns(ref, d, authorization.SelectPrivilege, indices); err != nil {
			t.Fatal(err)
		}
	}
	for _, indices := range [][]int{{13}, {0, 13}, all} {
		err := auth.CheckColumns(ref, d, authorization.SelectPrivilege, indices)
		enginetest.State(t, err, "42501")
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Message != "permission denied for table pg_subscription" {
			t.Fatal(err)
		}
	}
	super := catalogAuthorization(e, v)
	if err := super.CheckColumns(ref, d, authorization.SelectPrivilege, all); err != nil {
		t.Fatal(err)
	}
	// Publish real membership candidates, then resolve the inherited predefined
	// privilege through the ordinary authority view.
	for _, revoke := range []bool{false, true} {
		e.Mu.Lock()
		current := e.RoleStore.Published
		read := e.RoleStore.PublishedRead(&e.Catalog.Owner, e.Catalog.Current())
		root, changed, _, err := authorization.MembershipCandidate(context.Background(), read, authorization.RoleState{CurrentUser: d.Owner}, &parser.RoleMembership{Targets: []parser.RoleSpec{{Name: "pg_read_all_data"}}, Members: []parser.RoleSpec{{Name: "subscription_unit_reader"}}, Revoke: revoke}, nil, true, true)
		if err == nil && changed {
			next := current.Advance()
			next.Root = root
			e.RoleStore.PublishLocked(next)
		}
		e.Mu.Unlock()
		if err != nil || !changed {
			t.Fatal("membership publication", changed, err)
		}
		fresh := e.CatalogRead()
		paired := e.RoleStore.PublishedRead(&e.Catalog.Owner, fresh)
		next := e.AuthorityView(access.View{Owner: &e.Catalog.Owner, PairedCatalog: fresh, Catalog: fresh, Roles: paired, RoleRecord: e.RoleStore.Published, State: auth.State})
		err = next.CheckColumns(next.Ref(a), fresh.MustRelation(a), authorization.SelectPrivilege, all)
		if revoke {
			enginetest.State(t, err, "42501")
		} else if err != nil {
			t.Fatal(err)
		}
	}
	for _, subID := range []int32{14, 0} {
		grant := ref
		grant.Address.SubID = subID
		acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: d.Owner, Grantee: authorization.ACLGrantee{Role: roles.LookupRef("subscription_unit_reader")}, Privileges: authorization.SelectPrivilege}}}
		if err := enginetest.SetObjectACL(e, context.Background(), grant, acl); err != nil {
			t.Fatal(err)
		}
		fresh := e.CatalogRead()
		next := e.AuthorityView(access.View{Owner: &e.Catalog.Owner, PairedCatalog: fresh, Catalog: fresh, Roles: e.RoleStore.PublishedRead(&e.Catalog.Owner, fresh), RoleRecord: e.RoleStore.Published, State: auth.State})
		if err := next.CheckColumns(next.Ref(a), fresh.MustRelation(a), authorization.SelectPrivilege, all); err != nil {
			t.Fatal(err)
		}
		if err := enginetest.SetObjectACL(e, context.Background(), grant, authorization.ACLValue{}); err != nil {
			t.Fatal(err)
		}
	}
}
