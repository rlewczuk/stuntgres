package role

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// OwnedSources resolves the source roles of REASSIGN OWNED or DROP OWNED and
// checks that the current user has their privileges. It returns the sources
// and the first one the database system requires, if any.
func OwnedSources(view access.View, specs []parser.RoleSpec, reassign bool) (map[authorization.PrincipalRef]bool, authorization.PrincipalRef, error) {
	refs := make(map[authorization.PrincipalRef]bool, len(specs))
	protected := authorization.PrincipalRef{}
	for _, spec := range specs {
		ref, err := authorization.MembershipRole(view.Roles, view.State, spec)
		if err != nil {
			return nil, authorization.PrincipalRef{}, err
		}
		role, err := view.Roles.Descriptor(ref)
		if err != nil {
			return nil, authorization.PrincipalRef{}, err
		}
		allowed, err := view.Roles.InheritsRole(view.State.CurrentUser, ref)
		if err != nil {
			return nil, authorization.PrincipalRef{}, err
		}
		if !allowed {
			message := "permission denied to drop objects"
			detail := "Only roles with privileges of role \"" + role.Name + "\" may drop objects owned by it."
			if reassign {
				message = "permission denied to reassign objects"
				detail = "Only roles with privileges of role \"" + role.Name + "\" may reassign objects owned by it."
			}
			return nil, authorization.PrincipalRef{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: message, Detail: detail}
		}
		if (role.Predefined() || ref == view.Owner.Principals.Principal(authorization.BootstrapPrincipalID)) && protected == (authorization.PrincipalRef{}) {
			protected = ref
		}
		refs[ref] = true
	}
	return refs, protected, nil
}

// OwnedPlan is what a REASSIGN OWNED or DROP OWNED statement acts on.
type OwnedPlan struct {
	Reassign bool
	Sources  map[authorization.PrincipalRef]bool
	// target is REASSIGN OWNED's new owner.
	Target authorization.PrincipalRef
	Mode   parser.DropMode
	// roots are the objects to reassign or drop; DROP OWNED's include the
	// extension members removed with their extension.
	Roots []ids.ObjectAddress
	// primaryRoot is DROP OWNED's only root, named in dependency diagnostics.
	PrimaryRoot ids.ObjectAddress
	// noop is REASSIGN OWNED to its only source, which changes nothing.
	Noop bool
}

// PlanOwned resolves and authorizes the roles of a REASSIGN OWNED or DROP
// OWNED statement and selects its roots. describe names an object in the
// extension-member diagnostic, as dependency diagnostics do.
func PlanOwned(view access.View, stmt parser.Statement, describe func(ids.ObjectAddress) string) (OwnedPlan, error) {
	var specs []parser.RoleSpec
	plan := OwnedPlan{Mode: parser.DropRestrict}
	switch s := stmt.(type) {
	case *parser.ReassignOwned:
		plan.Reassign = true
		specs = s.Sources
	case *parser.DropOwned:
		specs = s.Sources
		plan.Mode = s.Mode
	}
	reassign := plan.Reassign
	sources, protected, err := OwnedSources(view, specs, reassign)
	if err != nil {
		return OwnedPlan{}, err
	}
	if reassign {
		s := stmt.(*parser.ReassignOwned)
		plan.Target, err = authorization.MembershipRole(view.Roles, view.State, s.Target)
		if err != nil {
			return OwnedPlan{}, err
		}
		allowed, err := view.Roles.InheritsRole(view.State.CurrentUser, plan.Target)
		if err != nil {
			return OwnedPlan{}, err
		}
		if !allowed {
			role, _ := view.Roles.Descriptor(plan.Target)
			return OwnedPlan{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied to reassign objects", Detail: fmt.Sprintf(`Only roles with privileges of role "%s" may reassign objects to it.`, role.Name)}
		}
	}
	// PostgreSQL resolves and authorizes utility operands before entering the
	// shared-dependency handler that refuses protected sources.
	if protected != (authorization.PrincipalRef{}) {
		role, _ := view.Roles.Descriptor(protected)
		noun := "drop objects owned by"
		if reassign {
			noun = "reassign ownership of objects owned by"
		}
		return OwnedPlan{}, sqlError("2BP01", fmt.Sprintf(`cannot %s role %s because they are required by the database system`, noun, role.Name))
	}
	plan.Sources = sources
	if reassign && len(sources) == 1 && sources[plan.Target] {
		plan.Noop = true
		return plan, nil
	}
	roots := access.OwnedRoots(view.Catalog, sources, routine.OperatorSeeded)
	if reassign {
		local := roots[:0]
		for _, address := range roots {
			if address.ClassID != ids.ForeignMappingClassID {
				local = append(local, address)
			}
		}
		roots = local
	}
	if !reassign {
		local := roots[:0]
		for _, a := range roots {
			if a.ClassID != ids.DatabaseClassID && a.ClassID != ids.SubscriptionClassID {
				local = append(local, a)
			}
		}
		roots = local
		if len(local) == 1 {
			plan.PrimaryRoot = local[0]
		}
		pending := make(map[ids.ObjectAddress]bool, len(roots))
		for _, a := range roots {
			pending[a] = true
		}
		for _, a := range roots {
			extension := catalog.ExtensionMembership(view.Catalog.Get(a))
			if extension != (ids.ObjectAddress{}) && !pending[extension] {
				d := view.Catalog.MustExtension(extension)
				return OwnedPlan{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: "cannot drop " + describe(a) + " because extension " + catalog.DependencyIdentifier(d.Name()) + " requires it", Hint: "You can drop extension " + catalog.DependencyIdentifier(d.Name()) + " instead."}
			}
		}
		roots = extension.RemovalRoots(view.Catalog, roots)
	}
	plan.Roots = roots
	return plan, nil
}
