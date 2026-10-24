package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqlstate"
)

// checkDatabaseConnect reports whether authenticated may connect to view's
// current database. It is used only at physical session publication; identity
// switches and already admitted operations do not call it.
func checkDatabaseConnect(ctx context.Context, view catalog.Read, roles authorization.Read, authenticated authorization.PrincipalRef) error {
	rights, err := access.DatabaseRights(ctx, view, roles, authenticated)
	if err != nil || rights&authorization.ConnectPrivilege != 0 {
		return err
	}
	db := view.MustDatabase(view.Owner().DatabaseAddress())
	diagnostic := sqlError("42501", fmt.Sprintf(`permission denied for database "%s"`, db.Name())).(*sqlstate.Error)
	diagnostic.Detail = "User does not have CONNECT privilege."
	return diagnostic
}
