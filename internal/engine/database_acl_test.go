package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/parser"
)

func TestDatabaseACLBaselineTransitions(t *testing.T) {
	e, owner, _, _, reader := ownershipUnitRoles()
	base := e.CatalogRead()
	db := base.state.descriptors[currentDatabaseAddress].(*databaseDescriptor)
	if db.acl.Explicit || len(db.acl.Entries) != 0 {
		t.Fatal("initial descriptor must retain NULL ACL", db.acl)
	}
	baseline := databaseACL(db)
	want := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
		{Grantor: owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: databasePublicPrivileges},
		{Grantor: owner, Grantee: authorization.ACLGrantee{Role: owner}, Privileges: databasePrivileges},
	}}
	if !reflect.DeepEqual(baseline, want) {
		t.Fatal("ordinary CTc/Tc without stored grant options", baseline)
	}
	for _, tc := range []struct {
		acl                       authorization.ACLValue
		ownerRights, readerRights authorization.Privileges
	}{
		{authorization.ACLValue{}, databasePrivileges, databasePublicPrivileges},
		{authorization.ACLValue{Explicit: true}, 0, 0},
		{authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: owner}, Privileges: authorization.CreatePrivilege, GrantOptions: authorization.CreatePrivilege}}}, authorization.CreatePrivilege, 0},
		{baseline, databasePrivileges, databasePublicPrivileges},
	} {
		candidate := cloneCatalog(base.state)
		next := *db
		next.acl = tc.acl
		candidate.descriptors[currentDatabaseAddress] = &next
		catalog := CatalogRead{e, candidate}
		roles := cloneRoleFixture(e.roles)
		bootstrap := fixtureRole(roles, owner)
		bootstrap.Superuser = false
		putFixtureRole(roles, bootstrap)
		for _, item := range []struct {
			ref  authorization.PrincipalRef
			want authorization.Privileges
		}{{owner, tc.ownerRights}, {reader, tc.readerRights}} {
			rights, err := databaseRights(context.Background(), catalog, e.roleRead(catalog, roles), item.ref)
			if err != nil || rights != item.want {
				t.Fatal("NULL/empty/legacy policy", item.ref, rights, item.want, err)
			}
		}
		if tc.acl.Explicit && !authorization.EqualACL(databaseACL(&next), tc.acl) {
			t.Fatal("explicit policy gained baseline")
		}
	}
	baseline.Entries[0].Privileges = 0
	if db.acl.Explicit || databaseACL(db).Entries[0].Privileges != databasePublicPrivileges {
		t.Fatal("materialized baseline mutated immutable descriptor")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if rights, err := databaseRights(ctx, base, e.roleRead(base, e.roles), owner); rights != 0 || !errors.Is(err, context.Canceled) {
		t.Fatal("superuser must not bypass cancellation", rights, err)
	}
}

func TestDatabaseACLDelegationCancellation(t *testing.T) {
	e, owner, source, target, _ := ownershipUnitRoles()
	db := e.state.descriptors[currentDatabaseAddress].(*databaseDescriptor)
	baseline := databaseACL(db)
	roles := e.roleRead(e.CatalogRead(), e.roles)
	delegated, _, err := authorization.ChangeObjectACL(context.Background(), roles, baseline, owner, owner, []authorization.ACLGrantee{{Role: source}}, databasePrivileges, true, false, false, false)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _, err := authorization.ChangeObjectACL(context.Background(), roles, delegated, owner, source, []authorization.ACLGrantee{{Role: target}}, databasePrivileges, false, false, false, false)
	if err != nil {
		t.Fatal(err)
	}
	before := authorization.ACLValue{Explicit: leaf.Explicit, Entries: append([]authorization.ACLEntry(nil), leaf.Entries...)}
	if _, _, err := authorization.ChangeObjectACL(context.Background(), roles, leaf, owner, owner, []authorization.ACLGrantee{{Role: source}}, databasePrivileges, false, true, true, false); err == nil {
		t.Fatal("RESTRICT accepted dependent database grants")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := authorization.ChangeObjectACL(ctx, roles, leaf, owner, owner, []authorization.ACLGrantee{{Role: source}}, databasePrivileges, false, true, true, true); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled cascade", err)
	}
	if !authorization.EqualACL(leaf, before) {
		t.Fatal("failed mutation touched source ACL")
	}
	next, changed, err := authorization.ChangeObjectACL(context.Background(), roles, leaf, owner, owner, []authorization.ACLGrantee{{Role: source}}, databasePrivileges, false, true, true, true)
	if err != nil || !changed {
		t.Fatal("cascade", changed, err)
	}
	if authorization.ACLRights(next, owner, []authorization.PrincipalRef{target}, databasePrivileges, false) != databasePublicPrivileges || authorization.ACLRights(next, owner, []authorization.PrincipalRef{source}, databasePrivileges, true) != 0 {
		t.Fatal("cascade removed independent PUBLIC rights or retained delegated leaf", next)
	}
	if !authorization.EqualACL(leaf, before) {
		t.Fatal("successful mutation touched source ACL")
	}
}

func TestDatabaseTemporaryCapturedRights(t *testing.T) {
	e, owner, _, _, reader := ownershipUnitRoles()
	base := e.CatalogRead()
	auth := catalogAuthorization(e, base)
	auth.state.CurrentUser = reader
	if rights, err := auth.ownTemporaryPrivileges(); err != nil || rights != authorization.UsagePrivilege|authorization.CreatePrivilege {
		t.Fatal("PUBLIC TEMP baseline", rights, err)
	}
	candidate := cloneCatalog(base.state)
	next := *candidate.descriptors[currentDatabaseAddress].(*databaseDescriptor)
	next.acl = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: owner}, Privileges: databasePrivileges}}}
	candidate.descriptors[currentDatabaseAddress] = &next
	fresh := catalogAuthorization(e, CatalogRead{e, candidate})
	fresh.state.CurrentUser = reader
	if rights, err := fresh.ownTemporaryPrivileges(); err != nil || rights != authorization.UsagePrivilege {
		t.Fatal("revoked own namespace keeps USAGE only", rights, err)
	}
	if rights, err := auth.ownTemporaryPrivileges(); err != nil || rights != authorization.UsagePrivilege|authorization.CreatePrivilege {
		t.Fatal("retained pair mutated", rights, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := databaseRights(ctx, fresh.catalog, fresh.roles, reader); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	foreign := New().authorizationDomain.Principal(reader.ID())
	if _, err := databaseRights(context.Background(), fresh.catalog, fresh.roles, foreign); err == nil {
		t.Fatal("foreign principal accepted")
	}
}

func TestDatabaseTemporaryDeniedAllocation(t *testing.T) {
	e, _, _, _, reader := ownershipUnitRoles()
	s := sessionStart(t, e, nil)
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	e.mu.Lock()
	databaseAdmissionPolicyLocked(e, authorization.ACLValue{Explicit: true})
	auth, err := e.authorizationLocked(op)
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	auth.state.CurrentUser = reader
	base, oid, revision := e.state, e.nextOID, e.nextRevision
	changes := cloneCatalogChanges(s.transactions.catalog)
	ref, err := e.relationCreationNamespaceLocked(auth, parser.ObjectName{Parts: []parser.Identifier{{Text: "database_denied"}}}, "temporary", changes)
	e.mu.Unlock()
	if err == nil || ref != (ObjectRef{}) {
		t.Fatal("lazy TEMP denial allocated namespace", ref, err)
	}
	if e.state != base || e.nextOID != oid || e.nextRevision != revision || len(changes.descriptors) != 0 || len(changes.names) != 0 || s.temporaryNamespace != (ObjectAddress{}) {
		t.Fatal("TEMP denial consumed catalog identity or staged objects")
	}
}
