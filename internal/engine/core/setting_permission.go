package core

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

// Eligible privileged builtins accept parameter SET grants. Read-only and
// deny-only custom definitions retain their separate admission contract.
// The operation supplies one coherent catalog/role root, not a session ACL cache.
func parameterSetAllowed(op *session.Operation, name string) (bool, error) {
	var view AuthorizationView
	var err error
	if scope := ActiveScopeOfLease(op.Lease); scope != nil {
		e := scope.Engine()
		e.Mu.Lock()
		view, err = scope.RoutineAuthorizationLocked()
		e.Mu.Unlock()
	} else {
		view, err = OperationAuthorization(op)
	}
	if err != nil {
		return false, err
	}
	return parameterSetAuthority(view, name)
}

func parameterSetAuthority(view AuthorizationView, name string) (bool, error) {
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
