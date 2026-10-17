package engine

import "fmt"

// These sources admit semantic planning before checking execution-time authority.
// Other virtual catalogs retain their existing mutation boundaries.
func guardedCatalogRelation(d *relationDescriptor) bool {
	if d == nil || d.relationKind != catalogRelation || d.address.ClassID != RelationClassID {
		return false
	}
	return d.address.ObjectID == pgAggregateOID || d.address.ObjectID == pgAMOID
}

// Binding and frame import have completed before mutation admission. Recheck
// the coherent effective actor at execution, including held SET ROLE commands.
// System-catalog TRUNCATE has its own released English diagnostic.
func guardedCatalogMutationError(auth AuthorizationView, d *relationDescriptor, truncate bool) error {
	actor, err := auth.roles.Descriptor(auth.state.CurrentUser)
	if err != nil {
		return err
	}
	if actor.Superuser {
		return sqlError("0A000", "catalog source mutation is unsupported")
	}
	if truncate {
		return sqlError("42501", fmt.Sprintf("permission denied: %q is a system catalog", d.name))
	}
	return sqlError("42501", "permission denied for table "+d.name)
}
