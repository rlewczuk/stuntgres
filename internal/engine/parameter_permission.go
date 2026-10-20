package engine

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
)

// settingAccess checks whether op may set d at all: startup-only and
// read-only settings are refused, and privileged ones need a parameter grant.
func settingAccess(d settings.Definition, op *session.Operation) error {
	switch d.Access {
	case settings.StartupOnly:
		return sqlError("55P02", fmt.Sprintf(`parameter "%s" cannot be changed without restarting the server`, d.Name))
	case settings.ReadOnly:
		return sqlError("55P02", fmt.Sprintf(`parameter "%s" cannot be changed`, d.Name))
	case settings.Privileged:
		if d.Name == "lc_messages" {
			allowed, err := parameterSetAllowed(op, d.Name)
			if err != nil {
				return err
			}
			if allowed {
				return nil
			}
		}
		return sqlError("42501", fmt.Sprintf(`permission denied to set parameter "%s"`, d.Name))
	}
	return nil
}

// Only lc_messages has a real native privileged setter that accepts parameter
// SET grants. Read-only settings and deny-only custom settings retain their
// access contract in settingAccess. The operation supplies one coherent current
// catalog/role root; no session ACL cache or second setting authority exists.
func parameterSetAllowed(op *session.Operation, name string) (bool, error) {
	view, err := operationAuthorization(op)
	if err != nil {
		return false, err
	}
	role, exists := view.Roles.Find(view.State.CurrentUser)
	if exists && role.Superuser {
		return true, nil
	}
	address := view.Catalog.NameAddress(ids.NameKey{ClassID: ids.ParameterClassID, Name: settings.FoldName(name)})
	if address == (ids.ObjectAddress{}) {
		return false, nil
	}
	ref := view.Ref(address)
	rights, err := view.Effective(ref, false)
	return rights&authorization.SetPrivilege != 0, err
}
