package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/sqlstate"
)

// checkDatabaseConnectLocked is used only at physical session publication.
// Identity switches and already admitted operations do not call it.
func (e *Engine) checkDatabaseConnectLocked(ctx context.Context, authenticated authorization.PrincipalRef) error {
	view := e.catalog.Current()
	rights, err := access.DatabaseRights(ctx, view, e.roles.read(&e.catalog.Owner, view, e.roles.published), authenticated)
	if err != nil || rights&authorization.ConnectPrivilege != 0 {
		return err
	}
	db := view.MustDatabase(view.Owner().DatabaseAddress())
	diagnostic := sqlError("42501", fmt.Sprintf(`permission denied for database "%s"`, db.Name())).(*sqlstate.Error)
	diagnostic.Detail = "User does not have CONNECT privilege."
	return diagnostic
}
