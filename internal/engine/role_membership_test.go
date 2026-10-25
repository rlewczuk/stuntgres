package engine

import (
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/parser"
)

// Fixtures use the real private command planner, with checked identities and
// explicit options; there is no second append-only membership publication path.
func grantMembership(t *testing.T, e *core.Engine, m authorization.Membership) error {
	t.Helper()
	r := e.Roles()
	names := make([]string, 3)
	for i, ref := range []authorization.PrincipalRef{m.Role, m.Member, m.Grantor} {
		d, err := r.Descriptor(ref)
		if err != nil {
			return err
		}
		names[i] = d.Name
	}
	op, done := roleOperation(t, e, "postgres")
	defer done()
	spec := func(name string) parser.RoleSpec { return parser.RoleSpec{Name: name} }
	g := spec(names[2])
	stmt := &parser.RoleMembership{Targets: []parser.RoleSpec{spec(names[0])}, Members: []parser.RoleSpec{spec(names[1])}, Grantor: &g, Options: []parser.MembershipOption{{Name: "admin", Value: m.Admin}, {Name: "inherit", Value: m.Inherit}, {Name: "set", Value: m.Set}}}
	_, err := e.Execute(op, stmt, 0, nil)
	return err
}
