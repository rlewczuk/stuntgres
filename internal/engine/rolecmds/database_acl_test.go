package rolecmds_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

func TestDatabaseACLBaselineTransitions(t *testing.T) {
	e, owner, _, _, reader := ownershipUnitRoles()
	base := e.CatalogRead()
	db := base.MustDatabase(catalog.CurrentDatabaseAddress)
	if db.ACL.Explicit || len(db.ACL.Entries) != 0 {
		t.Fatal("initial descriptor must retain NULL ACL", db.ACL)
	}
	baseline := authorization.MaterializeDatabaseACL(db.ACL, db.Owner)
	want := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
		{Grantor: owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.DatabasePublicPrivileges},
		{Grantor: owner, Grantee: authorization.ACLGrantee{Role: owner}, Privileges: authorization.DatabasePrivileges},
	}}
	if !reflect.DeepEqual(baseline, want) {
		t.Fatal("ordinary CTc/Tc without stored grant options", baseline)
	}
	for _, tc := range []struct {
		acl                       authorization.ACLValue
		ownerRights, readerRights authorization.Privileges
	}{
		{authorization.ACLValue{}, authorization.DatabasePrivileges, authorization.DatabasePublicPrivileges},
		{authorization.ACLValue{Explicit: true}, 0, 0},
		{authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: owner}, Privileges: authorization.CreatePrivilege, GrantOptions: authorization.CreatePrivilege}}}, authorization.CreatePrivilege, 0},
		{baseline, authorization.DatabasePrivileges, authorization.DatabasePublicPrivileges},
	} {
		candidate := catalog.CloneState(base.State())
		next := *db
		next.ACL = tc.acl
		candidate.SetDescriptor(catalog.CurrentDatabaseAddress, &next)
		catalog := e.Catalog.Read(candidate)
		roles := cloneRoleFixture(e.RoleStore.Published)
		bootstrap := fixtureRole(roles, owner)
		bootstrap.Superuser = false
		putFixtureRole(roles, bootstrap)
		for _, item := range []struct {
			ref  authorization.PrincipalRef
			want authorization.Privileges
		}{{owner, tc.ownerRights}, {reader, tc.readerRights}} {
			rights, err := access.DatabaseRights(context.Background(), catalog, e.RoleStore.Read(&e.Catalog.Owner, catalog, roles), item.ref)
			if err != nil || rights != item.want {
				t.Fatal("NULL/empty/legacy policy", item.ref, rights, item.want, err)
			}
		}
		if tc.acl.Explicit && !authorization.EqualACL(authorization.MaterializeDatabaseACL(next.ACL, next.Owner), tc.acl) {
			t.Fatal("explicit policy gained baseline")
		}
	}
	baseline.Entries[0].Privileges = 0
	if db.ACL.Explicit || authorization.MaterializeDatabaseACL(db.ACL, db.Owner).Entries[0].Privileges != authorization.DatabasePublicPrivileges {
		t.Fatal("materialized baseline mutated immutable descriptor")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if rights, err := access.DatabaseRights(ctx, base, e.RoleStore.PublishedRead(&e.Catalog.Owner, base), owner); rights != 0 || !errors.Is(err, context.Canceled) {
		t.Fatal("superuser must not bypass cancellation", rights, err)
	}
}

func TestDatabaseACLDelegationCancellation(t *testing.T) {
	e, owner, source, target, _ := ownershipUnitRoles()
	db := e.Catalog.State.MustDatabase(catalog.CurrentDatabaseAddress)
	baseline := authorization.MaterializeDatabaseACL(db.ACL, db.Owner)
	roles := e.RoleStore.PublishedRead(&e.Catalog.Owner, e.CatalogRead())
	delegated, _, err := authorization.ChangeObjectACL(context.Background(), roles, baseline, owner, owner, []authorization.ACLGrantee{{Role: source}}, authorization.DatabasePrivileges, true, false, false, false)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _, err := authorization.ChangeObjectACL(context.Background(), roles, delegated, owner, source, []authorization.ACLGrantee{{Role: target}}, authorization.DatabasePrivileges, false, false, false, false)
	if err != nil {
		t.Fatal(err)
	}
	before := authorization.ACLValue{Explicit: leaf.Explicit, Entries: append([]authorization.ACLEntry(nil), leaf.Entries...)}
	if _, _, err := authorization.ChangeObjectACL(context.Background(), roles, leaf, owner, owner, []authorization.ACLGrantee{{Role: source}}, authorization.DatabasePrivileges, false, true, true, false); err == nil {
		t.Fatal("RESTRICT accepted dependent database grants")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := authorization.ChangeObjectACL(ctx, roles, leaf, owner, owner, []authorization.ACLGrantee{{Role: source}}, authorization.DatabasePrivileges, false, true, true, true); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled cascade", err)
	}
	if !authorization.EqualACL(leaf, before) {
		t.Fatal("failed mutation touched source ACL")
	}
	next, changed, err := authorization.ChangeObjectACL(context.Background(), roles, leaf, owner, owner, []authorization.ACLGrantee{{Role: source}}, authorization.DatabasePrivileges, false, true, true, true)
	if err != nil || !changed {
		t.Fatal("cascade", changed, err)
	}
	if authorization.ACLRights(next, owner, []authorization.PrincipalRef{target}, authorization.DatabasePrivileges, false) != authorization.DatabasePublicPrivileges || authorization.ACLRights(next, owner, []authorization.PrincipalRef{source}, authorization.DatabasePrivileges, true) != 0 {
		t.Fatal("cascade removed independent PUBLIC rights or retained delegated leaf", next)
	}
	if !authorization.EqualACL(leaf, before) {
		t.Fatal("successful mutation touched source ACL")
	}
}

func TestDatabaseTemporaryCapturedRights(t *testing.T) {
	e, owner, _, _, reader := ownershipUnitRoles()
	base := e.CatalogRead()
	auth := enginetest.CatalogAuthorization(e, base)
	auth.State.CurrentUser = reader
	if rights, err := auth.View.OwnTemporaryPrivileges(auth); err != nil || rights != authorization.UsagePrivilege|authorization.CreatePrivilege {
		t.Fatal("PUBLIC TEMP baseline", rights, err)
	}
	candidate := catalog.CloneState(base.State())
	next := *candidate.MustDatabase(catalog.CurrentDatabaseAddress)
	next.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: owner}, Privileges: authorization.DatabasePrivileges}}}
	candidate.SetDescriptor(catalog.CurrentDatabaseAddress, &next)
	fresh := enginetest.CatalogAuthorization(e, e.Catalog.Read(candidate))
	fresh.State.CurrentUser = reader
	if rights, err := fresh.View.OwnTemporaryPrivileges(fresh); err != nil || rights != authorization.UsagePrivilege {
		t.Fatal("revoked own namespace keeps USAGE only", rights, err)
	}
	if rights, err := auth.View.OwnTemporaryPrivileges(auth); err != nil || rights != authorization.UsagePrivilege|authorization.CreatePrivilege {
		t.Fatal("retained pair mutated", rights, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := access.DatabaseRights(ctx, fresh.Catalog, fresh.Roles, reader); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	foreign := engine.New().RoleStore.Domain.Principal(reader.ID())
	if _, err := access.DatabaseRights(context.Background(), fresh.Catalog, fresh.Roles, foreign); err == nil {
		t.Fatal("foreign principal accepted")
	}
}

func TestDatabaseTemporaryDeniedAllocation(t *testing.T) {
	e, _, _, _, reader := ownershipUnitRoles()
	s := enginetest.StartSession(t, e, nil)
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	e.Mu.Lock()
	databaseAdmissionPolicyLocked(e, authorization.ACLValue{Explicit: true})
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	auth.State.CurrentUser = reader
	base, oid, revision := e.Catalog.State, e.Catalog.NextOID(), e.Catalog.NextRevision()
	changes := s.Transactions.Catalog.Clone()
	ref, err := e.RelationCreationNamespaceLocked(auth, parser.ObjectName{Parts: []parser.Identifier{{Text: "database_denied"}}}, "temporary", changes)
	e.Mu.Unlock()
	if err == nil || ref != (ids.ObjectRef{}) {
		t.Fatal("lazy TEMP denial allocated namespace", ref, err)
	}
	if e.Catalog.State != base || e.Catalog.NextOID() != oid || e.Catalog.NextRevision() != revision || len(changes.Descriptors) != 0 || len(changes.Names) != 0 || s.TemporaryNamespace != (ids.ObjectAddress{}) {
		t.Fatal("TEMP denial consumed catalog identity or staged objects")
	}
}

func cloneRoleFixture(r *access.RoleRecord) *access.RoleRecord {
	return &access.RoleRecord{Root: r.Root, Revision: r.Revision + 1}
}

func fixtureRole(r *access.RoleRecord, ref authorization.PrincipalRef) authorization.RoleDescriptor {
	d, _ := r.Root.Read(authorization.PrincipalRef{}).Find(ref)
	return d
}

func putFixtureRole(r *access.RoleRecord, d authorization.RoleDescriptor) {
	root, err := r.Root.PutDescriptor(d)
	if err != nil {
		panic(err)
	}
	r.Root = root
}

func databaseAdmissionPolicyLocked(e *engine.Engine, acl authorization.ACLValue) {
	candidate := catalog.CloneState(e.Catalog.State)
	db := *candidate.MustDatabase(catalog.CurrentDatabaseAddress)
	db.ObjectRevision++
	db.ACL = acl
	candidate.SetDescriptor(catalog.CurrentDatabaseAddress, &db)
	e.Catalog.PublishLocked(candidate)
}
