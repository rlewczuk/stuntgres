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
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		t.Fatal(err)
	}
	roles := cloneRoleFixture(auth.RoleRecord)
	bootstrap := fixtureRole(roles, auth.State.CurrentUser)
	bootstrap.Superuser = false
	putFixtureRole(roles, bootstrap)
	successor := e.roles.domain.Principal(100001)
	putFixtureRole(roles, authorization.RoleDescriptor{Ref: successor, Name: "successor", Inherit: true})
	before := catalog.CloneState(auth.Catalog.State())
	address := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	namespace := *before.MustNamespace(address)
	namespace.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: namespace.Owner, Grantee: authorization.ACLGrantee{Role: namespace.Owner}, Privileges: authorization.UsagePrivilege | authorization.CreatePrivilege}}}
	before.SetDescriptor(address, &namespace)
	auth.RoleRecord = roles
	auth.Recapture(e.catalog.read(before))
	warm, err := e.sequenceEnvironmentLocked(auth)
	if err != nil || !slices.Contains(warm.namespaces, address) {
		t.Fatal("owner did not warm public namespace", warm, err)
	}
	key := op.session.sequencePath.key
	after := catalog.CloneState(before)
	database := *after.MustDatabase(catalog.CurrentDatabaseAddress)
	database.Owner = successor
	after.SetDescriptor(catalog.CurrentDatabaseAddress, &database)
	fresh := auth
	fresh.Catalog = e.catalog.read(after)
	fresh.Recapture(fresh.Catalog)
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
	fresh.State.CurrentUser = successor
	owned, err := e.sequenceEnvironmentLocked(fresh)
	if err != nil || !slices.Contains(owned.namespaces, address) {
		t.Fatal("successor did not acquire actual owner path", owned, err)
	}
}
