package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqlstate"
)

// DatabaseRights reads one captured catalog/role pair; callers own its coherence
// and the engine mutex when observing live roots. It invokes no host callback.
func DatabaseRights(ctx context.Context, view catalog.Read, roles authorization.Read, principal authorization.PrincipalRef) (authorization.Privileges, error) {
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
	db := view.MustDatabase(catalog.CurrentDatabaseAddress)
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
	view := e.catalog.read(e.catalog.state)
	rights, err := DatabaseRights(ctx, view, e.roles.read(e.catalog.owner(), view, e.roles.published), authenticated)
	if err != nil || rights&authorization.ConnectPrivilege != 0 {
		return err
	}
	db := view.MustDatabase(catalog.CurrentDatabaseAddress)
	diagnostic := sqlError("42501", fmt.Sprintf(`permission denied for database "%s"`, db.Name())).(*sqlstate.Error)
	diagnostic.Detail = "User does not have CONNECT privilege."
	return diagnostic
}
