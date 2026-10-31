package core_test

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
)

func retireFixtureRole(r *access.RoleRecord, ref authorization.PrincipalRef) {
	d := fixtureRole(r, ref)
	r.Root = r.Root.Retire(map[authorization.PrincipalRef]authorization.RoleDescriptor{ref: d})
}

func fixtureRole(r *access.RoleRecord, ref authorization.PrincipalRef) authorization.RoleDescriptor {
	d, _ := r.Root.Read(authorization.PrincipalRef{}).Find(ref)
	return d
}
