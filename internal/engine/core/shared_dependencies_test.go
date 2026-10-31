package core

import (
	"context"
	"errors"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"testing"
)

func TestPGShdependCapturedRoots(t *testing.T) {
	domain := &authorization.Domain{}
	root := authorization.Seed(domain)
	owner, reader := domain.Principal(100), domain.Principal(101)
	var err error
	root, err = root.PutDescriptor(authorization.RoleDescriptor{Ref: owner, Name: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	root, err = root.PutDescriptor(authorization.RoleDescriptor{Ref: reader, Name: "reader"})
	if err != nil {
		t.Fatal(err)
	}
	roles := root.Read(owner)
	authority := &catalog.Owner{Principals: domain}
	address := ids.ObjectAddress{ClassID: 1259, ObjectID: 40000}
	acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: reader}, Privileges: authorization.SelectPrivilege}, {Grantor: reader, Grantee: authorization.ACLGrantee{Role: reader}, Privileges: authorization.UpdatePrivilege}}}
	state := &catalog.State{}
	state.SetDescriptor(address, &catalog.Relation{ObjectAddress: address, Owner: owner, Security: catalog.RelationSecurity{ACL: acl, ColumnACL: map[int32]authorization.ACLValue{2: acl}}})
	dbAddress := ids.ObjectAddress{ClassID: 1262, ObjectID: 555}
	state.SetDescriptor(ids.ObjectAddress{ClassID: 1259, ObjectID: 1262}, &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: 1259, ObjectID: 1262}, RelationKind: catalog.CatalogRelation})
	state.SetDescriptor(dbAddress, &catalog.Database{ObjectAddress: dbAddress, Owner: owner})
	for _, class := range []ids.OID{ids.SubscriptionClassID, ids.ParameterClassID} {
		classAddress := ids.ObjectAddress{ClassID: 1259, ObjectID: class}
		state.SetDescriptor(classAddress, &catalog.Relation{ObjectAddress: classAddress, RelationKind: catalog.CatalogRelation})
	}
	subscriptionAddress := ids.ObjectAddress{ClassID: ids.SubscriptionClassID, ObjectID: 40003}
	state.SetDescriptor(subscriptionAddress, &catalog.Subscription{ObjectAddress: subscriptionAddress, Owner: owner})
	parameterAddress := ids.ObjectAddress{ClassID: ids.ParameterClassID, ObjectID: 40004}
	state.SetDescriptor(parameterAddress, &catalog.Parameter{ObjectAddress: parameterAddress, Owner: owner})
	state.SetInitialPrivileges(map[ids.ObjectAddress]*catalog.InitialPrivilegeRecord{address: {ACL: acl}})
	view := catalog.NewRead(authority, state)
	peer := catalog.NewRead(&catalog.Owner{Principals: domain}, catalog.CloneState(state))
	databases := []sharedDependencyDatabase{{Database: 555, Catalog: view, Current: true}, {Database: 556, Catalog: peer}}
	facts, err := classifySharedDependencies(t.Context(), view, roles, databases)
	if err != nil {
		t.Fatal(err)
	}
	// Colliding local addresses retain owner, object ACL, column ACL and historical
	// owner/reader. Replicated database/subscription/parameter owners occur once.
	if len(facts) != 13 {
		t.Fatalf("facts %d: %v", len(facts), facts)
	}
	counts := map[ids.OID]int{}
	for _, fact := range facts {
		counts[fact.Database]++
	}
	if counts[0] != 3 || counts[555] != 5 || counts[556] != 5 {
		t.Fatal(counts)
	}
	next := catalog.CloneState(state)
	changed := *state.MustRelation(address)
	changed.Owner = reader
	next.SetDescriptor(address, &changed)
	if next == state || next.MustRelation(address) == state.MustRelation(address) || state.MustRelation(address).Owner != owner || peer.State().MustRelation(address) != state.MustRelation(address) {
		t.Fatal("catalog root/backing ownership changed")
	}
	fresh := catalog.NewRead(authority, next)
	databases[0].Catalog = fresh
	newer, err := classifySharedDependencies(t.Context(), fresh, roles, databases)
	if err != nil {
		t.Fatal(err)
	}
	if len(newer) != 13 {
		t.Fatal(newer)
	}
	for _, fact := range facts {
		if fact.Database == 555 && fact.Kind == 'o' && fact.Reference != 100 {
			t.Fatal("old owner capture changed", fact)
		}
	}
	for _, fact := range newer {
		if fact.Database == 555 && fact.Kind == 'o' && fact.Reference != 101 {
			t.Fatal("new owner not captured", fact)
		}
	}
	// Retained immutable roots still derive the original bag after publication.
	databases[0].Catalog = view
	retained, err := classifySharedDependencies(t.Context(), view, roles, databases)
	if err != nil || len(retained) != len(facts) {
		t.Fatal(retained, err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if result, err := classifySharedDependencies(canceled, view, roles, databases); result != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(result, err)
	}
	if result, err := classifySharedDependencies(t.Context(), view, roles, databases[1:]); result != nil || err == nil {
		t.Fatal("missing current", result, err)
	}
}

func TestPGShdependPinnedRoleAuthority(t *testing.T) {
	domain := &authorization.Domain{}
	root := authorization.Seed(domain)
	bootstrap := domain.Principal(authorization.BootstrapPrincipalID)
	owner := &catalog.Owner{Principals: domain}
	state := &catalog.State{}
	address := ids.ObjectAddress{ClassID: 1259, ObjectID: 40001}
	state.SetDescriptor(address, &catalog.Relation{ObjectAddress: address, Owner: bootstrap})
	predefinedAddress := ids.ObjectAddress{ClassID: 1259, ObjectID: 40002}
	state.SetDescriptor(predefinedAddress, &catalog.Relation{ObjectAddress: predefinedAddress, Owner: domain.Principal(authorization.DatabaseOwnerRoleID)})
	view := catalog.NewRead(owner, state)
	databases := []sharedDependencyDatabase{{Database: 555, Catalog: view, Current: true}}
	facts, err := classifySharedDependencies(t.Context(), view, root.Read(bootstrap), databases)
	if err != nil || len(facts) != 0 {
		t.Fatal(facts, err)
	}
	original, err := root.Read(bootstrap).Descriptor(bootstrap)
	if err != nil {
		t.Fatal(err)
	}
	// Free the spelling without retiring a grantor still referenced by seeded
	// memberships. Name reuse must not transfer the authentic bootstrap pin.
	original.Name = "renamed_bootstrap"
	root, err = root.PutDescriptor(original)
	if err != nil {
		t.Fatal(err)
	}
	reused := domain.Principal(100) // Below predefined high-water mark, not pinned.
	root, err = root.PutDescriptor(authorization.RoleDescriptor{Ref: reused, Name: "postgres"})
	if err != nil {
		t.Fatal(err)
	}
	next := catalog.CloneState(state)
	next.SetDescriptor(address, &catalog.Relation{ObjectAddress: address, Owner: reused})
	view = catalog.NewRead(owner, next)
	databases[0].Catalog = view
	facts, err = classifySharedDependencies(t.Context(), view, root.Read(reused), databases)
	if err != nil || len(facts) != 1 || facts[0].Reference != 100 {
		t.Fatal(facts, err)
	}
}
