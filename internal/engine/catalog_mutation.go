package engine

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
)

// Binding and frame import have completed before mutation admission. Recheck
// the coherent effective actor at execution, including held SET ROLE commands.
// System-catalog TRUNCATE has its own released English diagnostic.
func guardedCatalogMutationError(a access.View, d *catalog.Relation, truncate bool) error {
	actor, err := a.Roles.Descriptor(a.State.CurrentUser)
	if err != nil {
		return err
	}
	if actor.Superuser {
		return sqlError("0A000", "catalog source mutation is unsupported")
	}
	if truncate {
		return sqlError("42501", fmt.Sprintf("permission denied: %q is a system catalog", d.Name()))
	}
	return sqlError("42501", "permission denied for table "+d.Name())
}
