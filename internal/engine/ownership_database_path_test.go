package engine

import (
	"slices"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"testing"
)

func TestRoleOwnershipDatabasePreparedPathIdentity(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	roles := cloneRoleFixture(auth.RoleRecord)
	bootstrap := fixtureRole(roles, auth.State.CurrentUser)
	bootstrap.Superuser = false
	putFixtureRole(roles, bootstrap)
	successor := e.RoleStore.Domain.Principal(100001)
	putFixtureRole(roles, authorization.RoleDescriptor{Ref: successor, Name: "successor", Inherit: true})
	before := catalog.CloneState(auth.Catalog.State())
	address := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	namespace := *before.MustNamespace(address)
	namespace.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: namespace.Owner, Grantee: authorization.ACLGrantee{Role: namespace.Owner}, Privileges: authorization.UsagePrivilege | authorization.CreatePrivilege}}}
	before.SetDescriptor(address, &namespace)
	auth.RoleRecord = roles
	auth.Recapture(e.Catalog.Read(before))
	warm, err := e.sequenceEnvironmentLocked(auth)
	if err != nil || !slices.Contains(warm.Namespaces, address) {
		t.Fatal("owner did not warm public namespace", warm, err)
	}
	key, _ := PayloadOf(op.Session).Sequences.CachedPathKey()
	after := catalog.CloneState(before)
	database := *after.MustDatabase(catalog.CurrentDatabaseAddress)
	database.Owner = successor
	after.SetDescriptor(catalog.CurrentDatabaseAddress, &database)
	fresh := auth
	fresh.Catalog = e.Catalog.Read(after)
	fresh.Recapture(fresh.Catalog)
	revoked, err := e.sequenceEnvironmentLocked(fresh)
	if err != nil || slices.Contains(revoked.Namespaces, address) {
		t.Fatal("warm path retained former database-owner authority", revoked, err)
	}
	replacement, _ := PayloadOf(op.Session).Sequences.CachedPathKey()
	if replacement.Database == key.Database || replacement.Roles != key.Roles || replacement.Epoch != key.Epoch || replacement.Namespace != key.Namespace || replacement.Authorization != key.Authorization {
		t.Fatal("not a database-only path invalidation", key, replacement)
	}
	retained, err := e.sequenceEnvironmentLocked(auth)
	if err != nil || !slices.Contains(retained.Namespaces, address) {
		t.Fatal("retained pair followed live database", retained, err)
	}
	fresh.State.CurrentUser = successor
	owned, err := e.sequenceEnvironmentLocked(fresh)
	if err != nil || !slices.Contains(owned.Namespaces, address) {
		t.Fatal("successor did not acquire actual owner path", owned, err)
	}
}
