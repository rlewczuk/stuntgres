package access

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqlstate"
)

// CheckDatabaseConnect reports whether authenticated may connect to view's
// current database. It is used only at physical session publication; identity
// switches and already admitted operations do not call it.
func CheckDatabaseConnect(ctx context.Context, view catalog.Read, roles authorization.Read, authenticated authorization.PrincipalRef) error {
	rights, err := DatabaseRights(ctx, view, roles, authenticated)
	if err != nil || rights&authorization.ConnectPrivilege != 0 {
		return err
	}
	db := view.MustDatabase(view.Owner().DatabaseAddress())
	diagnostic := sqlError("42501", fmt.Sprintf(`permission denied for database "%s"`, db.Name())).(*sqlstate.Error)
	diagnostic.Detail = "User does not have CONNECT privilege."
	return diagnostic
}
