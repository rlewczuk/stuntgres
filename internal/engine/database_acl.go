package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/sqlstate"
)

const databasePrivileges = authorization.CreatePrivilege | authorization.TemporaryPrivilege | authorization.ConnectPrivilege
const databasePublicPrivileges = authorization.TemporaryPrivilege | authorization.ConnectPrivilege

// databaseACL materializes only the absent ACL. Explicit empty and legacy
// explicit policies must never acquire PUBLIC privileges from this baseline.
func databaseACL(db *databaseDescriptor) authorization.ACLValue {
	if db.acl.Explicit {
		return db.acl
	}
	return authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
		{Grantor: db.owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: databasePublicPrivileges},
		{Grantor: db.owner, Grantee: authorization.ACLGrantee{Role: db.owner}, Privileges: databasePrivileges},
	}}
}

// databaseRights reads one captured catalog/role pair; callers own its coherence
// and the engine mutex when observing live roots. It invokes no host callback.
func databaseRights(ctx context.Context, catalog CatalogRead, roles authorization.Read, principal authorization.PrincipalRef) (authorization.Privileges, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	actor, err := roles.Descriptor(principal)
	if err != nil {
		return 0, err
	}
	if actor.Superuser {
		return databasePrivileges, nil
	}
	principals, err := roles.RoleClosure(ctx, principal, true)
	if err != nil {
		return 0, err
	}
	db := catalog.state.descriptors[currentDatabaseAddress].(*databaseDescriptor)
	// Avoid materializing a default ACL merely to inspect its ordinary rights.
	rights := authorization.ACLRights(db.acl, db.owner, principals, databasePrivileges, false)
	if !db.acl.Explicit {
		rights |= databasePublicPrivileges
	}
	return rights & databasePrivileges, nil
}

// checkDatabaseConnectLocked is used only at physical session publication.
// Identity switches and already admitted operations do not call it.
func (e *Engine) checkDatabaseConnectLocked(ctx context.Context, authenticated authorization.PrincipalRef) error {
	catalog := CatalogRead{e, e.state}
	rights, err := databaseRights(ctx, catalog, e.roleRead(catalog, e.roles), authenticated)
	if err != nil || rights&authorization.ConnectPrivilege != 0 {
		return err
	}
	db := catalog.state.descriptors[currentDatabaseAddress].(*databaseDescriptor)
	diagnostic := sqlError("42501", fmt.Sprintf(`permission denied for database "%s"`, db.name)).(*sqlstate.Error)
	diagnostic.Detail = "User does not have CONNECT privilege."
	return diagnostic
}

// Own TEMP namespaces remain usable after revocation, but CREATE is contingent
// on the current effective role's database TEMP privilege, not authentication.
func (v AuthorizationView) ownTemporaryPrivileges() (authorization.Privileges, error) {
	rights, err := v.Effective(ObjectRef{owner: v.engine.owner, Epoch: v.catalog.Epoch(), Address: currentDatabaseAddress}, false)
	if err != nil {
		return 0, err
	}
	if rights&authorization.TemporaryPrivilege != 0 {
		return authorization.UsagePrivilege | authorization.CreatePrivilege, nil
	}
	return authorization.UsagePrivilege, nil
}
