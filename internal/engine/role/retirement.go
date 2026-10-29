package role

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

// RetirementPreflightLocked reports ids.ErrCatalogReplaced when a role that
// journal c retires is still referenced at final publication: by a default
// ACL or a descriptor of state, or by a membership edge of c's role record.
// owner and principals are the engine's catalog owner and principal domain.
//
// The engine calls it only at final publication under Engine.mu, with the
// prepared combined catalog validated against the current committed root.
// Early statement checks must not forbid a later supported removal of an
// owner's own dependencies.
func RetirementPreflightLocked(c *session.Journal, owner *catalog.Owner, principals *authorization.Domain, state *catalog.State) error {
	for ref := range c.RetiredRoles {
		if access.DefaultRoleDependencies(catalog.NewRead(owner, state), access.RoleRead(owner, principals, catalog.NewRead(owner, state), c.Roles), ref, nil) {
			return ids.ErrCatalogReplaced
		}
		if access.InitialRoleDependencies(catalog.NewRead(owner, state), ref, nil) {
			return ids.ErrCatalogReplaced
		}
		for _, descriptor := range state.Descriptors() {
			if access.DescriptorRoleDependencies(descriptor, ref, nil) {
				return ids.ErrCatalogReplaced
			}
		}
		for edge := range c.Roles.Root.Read(authorization.PrincipalRef{}).Edges() {
			if edge.Role == ref || edge.Member == ref || edge.Grantor == ref {
				return ids.ErrCatalogReplaced
			}
		}
	}
	return nil
}
