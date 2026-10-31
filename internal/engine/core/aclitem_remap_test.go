package core_test

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/enginetest"
)

func retireFixtureRole(r *access.RoleRecord, ref authorization.PrincipalRef) {
	d := enginetest.FixtureRole(r, ref)
	r.Root = r.Root.Retire(map[authorization.PrincipalRef]authorization.RoleDescriptor{ref: d})
}
