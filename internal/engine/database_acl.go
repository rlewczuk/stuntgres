package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/sqlstate"
)

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
		return authorization.DatabasePrivileges, nil
	}
	principals, err := roles.RoleClosure(ctx, principal, true)
	if err != nil {
		return 0, err
	}
	db := catalog.MustDatabase(currentDatabaseAddress)
	// Avoid materializing a default ACL merely to inspect its ordinary rights.
	rights := authorization.ACLRights(db.ACL, db.Owner, principals, authorization.DatabasePrivileges, false)
	if !db.ACL.Explicit {
		rights |= authorization.DatabasePublicPrivileges
	}
	return rights & authorization.DatabasePrivileges, nil
}

// checkDatabaseConnectLocked is used only at physical session publication.
// Identity switches and already admitted operations do not call it.
func (e *Engine) checkDatabaseConnectLocked(ctx context.Context, authenticated authorization.PrincipalRef) error {
	catalog := e.catalog.read(e.catalog.state)
	rights, err := databaseRights(ctx, catalog, e.roleRead(catalog, e.roles), authenticated)
	if err != nil || rights&authorization.ConnectPrivilege != 0 {
		return err
	}
	db := catalog.MustDatabase(currentDatabaseAddress)
	diagnostic := sqlError("42501", fmt.Sprintf(`permission denied for database "%s"`, db.name)).(*sqlstate.Error)
	diagnostic.Detail = "User does not have CONNECT privilege."
	return diagnostic
}

// Own TEMP namespaces remain usable after revocation, but CREATE is contingent
// on the current effective role's database TEMP privilege, not authentication.
func (v AuthorizationView) ownTemporaryPrivileges() (authorization.Privileges, error) {
	rights, err := v.Effective(v.engine.catalog.Domain.Ref(v.catalog.Epoch(), currentDatabaseAddress), false)
	if err != nil {
		return 0, err
	}
	if rights&authorization.TemporaryPrivilege != 0 {
		return authorization.UsagePrivilege | authorization.CreatePrivilege, nil
	}
	return authorization.UsagePrivilege, nil
}
