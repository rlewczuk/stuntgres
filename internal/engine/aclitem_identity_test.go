package engine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
)

func aclUnitRoles(e *core.Engine, names ...string) {
	e.Mu.Lock()
	defer e.Mu.Unlock()
	next := cloneRoleFixture(e.RoleStore.Published)
	for i, name := range names {
		putFixtureRole(next, authorization.RoleDescriptor{Ref: e.RoleStore.Domain.Principal(uint64(20000 + i)), Name: name, Inherit: true, ConnectionLimit: -1})
	}
	e.RoleStore.PublishLocked(next)
	if nextID := uint64(20000 + len(names)); e.RoleStore.NextPrincipal() < nextID {
		e.RoleStore.InstallPrincipalFrontierLocked(nextID)
	}
}
