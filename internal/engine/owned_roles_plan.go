package engine

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

func ownedSources(view access.View, specs []parser.RoleSpec, reassign bool) (map[authorization.PrincipalRef]bool, authorization.PrincipalRef, error) {
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

// ownedPlan is what a REASSIGN OWNED or DROP OWNED statement acts on.
type ownedPlan struct {
	reassign bool
	sources  map[authorization.PrincipalRef]bool
	// target is REASSIGN OWNED's new owner.
	target authorization.PrincipalRef
	mode   parser.DropMode
	// roots are the objects to reassign or drop; DROP OWNED's include the
	// extension members removed with their extension.
	roots []ids.ObjectAddress
	// primaryRoot is DROP OWNED's only root, named in dependency diagnostics.
	primaryRoot ids.ObjectAddress
	// noop is REASSIGN OWNED to its only source, which changes nothing.
	noop bool
}

// planOwned resolves and authorizes the roles of a REASSIGN OWNED or DROP
// OWNED statement and selects its roots. env supplies the reference path of
// the extension-member diagnostic.
func planOwned(view access.View, env access.Environment, stmt parser.Statement) (ownedPlan, error) {
	var specs []parser.RoleSpec
	plan := ownedPlan{mode: parser.DropRestrict}
	switch s := stmt.(type) {
	case *parser.ReassignOwned:
		plan.reassign = true
		specs = s.Sources
	case *parser.DropOwned:
		specs = s.Sources
		plan.mode = s.Mode
	}
	reassign := plan.reassign
	sources, protected, err := ownedSources(view, specs, reassign)
	if err != nil {
		return ownedPlan{}, err
	}
	if reassign {
		s := stmt.(*parser.ReassignOwned)
		plan.target, err = authorization.MembershipRole(view.Roles, view.State, s.Target)
		if err != nil {
			return ownedPlan{}, err
		}
		allowed, err := view.Roles.InheritsRole(view.State.CurrentUser, plan.target)
		if err != nil {
			return ownedPlan{}, err
		}
		if !allowed {
			role, _ := view.Roles.Descriptor(plan.target)
			return ownedPlan{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied to reassign objects", Detail: fmt.Sprintf(`Only roles with privileges of role "%s" may reassign objects to it.`, role.Name)}
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
		return ownedPlan{}, sqlError("2BP01", fmt.Sprintf(`cannot %s role %s because they are required by the database system`, noun, role.Name))
	}
	plan.sources = sources
	if reassign && len(sources) == 1 && sources[plan.target] {
		plan.noop = true
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
			if a.ClassID != ids.DatabaseClassID {
				local = append(local, a)
			}
		}
		roots = local
		if len(local) == 1 {
			plan.primaryRoot = local[0]
		}
		path, _ := env.ReferencePath()
		pending := make(map[ids.ObjectAddress]bool, len(roots))
		for _, a := range roots {
			pending[a] = true
		}
		for _, a := range roots {
			var extension ids.ObjectAddress
			switch d := view.Catalog.Get(a).(type) {
			case *catalog.Procedure:
				extension = d.Extension
			case *catalog.TSDictionary:
				extension = d.Extension
			}
			if extension != (ids.ObjectAddress{}) && !pending[extension] {
				d := view.Catalog.MustExtension(extension)
				return ownedPlan{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: "cannot drop " + dependencyObject(view.Catalog, a, path) + " because extension " + catalog.DependencyIdentifier(d.Name()) + " requires it", Hint: "You can drop extension " + catalog.DependencyIdentifier(d.Name()) + " instead."}
			}
		}
		roots = extension.RemovalRoots(view.Catalog, roots)
	}
	plan.roots = roots
	return plan, nil
}
