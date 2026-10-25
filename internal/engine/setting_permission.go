package engine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
)

// settingAccess binds the parameter-grant check to op's authorization.
func settingAccess(d settings.Definition, op *session.Operation) error {
	return session.SettingAccess(d, func(name string) (bool, error) { return parameterSetAllowed(op, name) })
}

// Only lc_messages has a real native privileged setter that accepts parameter
// SET grants. Read-only settings and deny-only custom settings retain their
// access contract in settingAccess. The operation supplies one coherent current
// catalog/role root; no session ACL cache or second setting authority exists.
func parameterSetAllowed(op *session.Operation, name string) (bool, error) {
	view, err := OperationAuthorization(op)
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
