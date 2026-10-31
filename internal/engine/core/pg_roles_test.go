package core_test

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
)

func pgRolesUnitRole(e *core.Engine, state *access.RoleRecord, id uint64, name string) authorization.PrincipalRef {
	e.RoleStore.InstallPrincipalFrontierLocked(id)
	ref, err := e.RoleStore.PrincipalCandidateLocked()
	if err != nil {
		panic(err)
	}
	e.RoleStore.InstallPrincipalFrontierLocked(ref.ID() + 1)
	enginetest.PutFixtureRole(state, authorization.RoleDescriptor{Ref: ref, Name: name, Login: true, Inherit: true, ConnectionLimit: -1, Password: "unit-private-verifier"})

	return ref
}
