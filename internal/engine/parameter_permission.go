package engine

import "stuntgres/internal/engine/authorization"

// Only lc_messages has a real native privileged setter that accepts parameter
// SET grants. Read-only settings and deny-only custom settings retain their
// access contract in settingAccess. The operation supplies one coherent current
// catalog/role root; no session ACL cache or second setting authority exists.
func parameterSetAllowed(op *Operation, name string) (bool, error) {
	view, err := op.Authorization()
	if err != nil {
		return false, err
	}
	role, exists := view.roles.Find(view.state.CurrentUser)
	if exists && role.Superuser {
		return true, nil
	}
	address := view.catalog.state.names[NameKey{ClassID: ParameterClassID, Name: settingFold(name)}]
	if address == (ObjectAddress{}) {
		return false, nil
	}
	ref := ObjectRef{owner: view.engine.domain, Epoch: view.catalog.Epoch(), Address: address}
	rights, err := view.Effective(ref, false)
	return rights&authorization.SetPrivilege != 0, err
}
