package engine

import (
	"slices"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"testing"
)

func TestRoleOwnershipDatabasePreparedPathIdentity(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	roles := cloneRoleFixture(auth.roleRecord)
	bootstrap := fixtureRole(roles, auth.state.CurrentUser)
	bootstrap.Superuser = false
	putFixtureRole(roles, bootstrap)
	successor := e.authorizationDomain.Principal(100001)
	putFixtureRole(roles, authorization.RoleDescriptor{Ref: successor, Name: "successor", Inherit: true})
	before := cloneCatalog(auth.catalog.state)
	address := ids.ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}
	namespace := *before.descriptors[address].(*namespaceDescriptor)
	namespace.acl = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: namespace.owner, Grantee: authorization.ACLGrantee{Role: namespace.owner}, Privileges: authorization.UsagePrivilege | authorization.CreatePrivilege}}}
	before.descriptors[address] = &namespace
	auth.roleRecord = roles
	auth.recaptureCatalog(CatalogRead{e, before})
	warm, err := e.sequenceEnvironmentLocked(auth)
	if err != nil || !slices.Contains(warm.namespaces, address) {
		t.Fatal("owner did not warm public namespace", warm, err)
	}
	key := op.session.sequencePath.key
	after := cloneCatalog(before)
	database := *after.descriptors[currentDatabaseAddress].(*databaseDescriptor)
	database.owner = successor
	after.descriptors[currentDatabaseAddress] = &database
	fresh := auth
	fresh.catalog = CatalogRead{e, after}
	fresh.recaptureCatalog(fresh.catalog)
	revoked, err := e.sequenceEnvironmentLocked(fresh)
	if err != nil || slices.Contains(revoked.namespaces, address) {
		t.Fatal("warm path retained former database-owner authority", revoked, err)
	}
	replacement := op.session.sequencePath.key
	if replacement.database == key.database || replacement.roles != key.roles || replacement.epoch != key.epoch || replacement.namespace != key.namespace || replacement.authorization != key.authorization {
		t.Fatal("not a database-only path invalidation", key, replacement)
	}
	retained, err := e.sequenceEnvironmentLocked(auth)
	if err != nil || !slices.Contains(retained.namespaces, address) {
		t.Fatal("retained pair followed live database", retained, err)
	}
	fresh.state.CurrentUser = successor
	owned, err := e.sequenceEnvironmentLocked(fresh)
	if err != nil || !slices.Contains(owned.namespaces, address) {
		t.Fatal("successor did not acquire actual owner path", owned, err)
	}
}
