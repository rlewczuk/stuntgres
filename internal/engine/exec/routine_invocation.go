package exec

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// RoutineInvocationAuthority refreshes only the actual invocation lane. Original
// manifests/recipe/consumer evidence remain the caller's; preparation and closed
// evaluation never acquire a live host through this capability.
func RoutineInvocationAuthority(c *Context) (RoutineAccess, []catalog.ExpressionDependency, error) {
	if c == nil || c.ClosedEvaluation != nil {
		return RoutineAccess{}, nil, fmt.Errorf("stuntgres: closed evaluation has no native invocation authority")
	}
	original, dependencies, err := RoutineAdmissionAuthority(c)
	if err != nil {
		return original, nil, err
	}
	host := c.LiveHost()
	if !original.Live() || host == nil {
		if owner := c.Catalog.Owner(); owner != nil && owner.Sepgsql != nil {
			return RoutineAccess{}, nil, fmt.Errorf("stuntgres: missing active native invocation owner")
		}
		return original, dependencies, nil
	}
	current, err := host.RoutineInvocationAuthority()
	if err != nil {
		return RoutineAccess{}, nil, err
	}
	if original.Owner == nil || current.Owner != original.Owner || original.Catalog.Owner() != original.Owner || current.Catalog.Owner() != current.Owner || original.Catalog.Domain() != current.Catalog.Domain() || original.Catalog.Epoch() != current.Catalog.Epoch() || original.Sepgsql.Policy != current.Sepgsql.Policy {
		return RoutineAccess{}, nil, ids.ErrCatalogReplaced
	}
	if c.RoutineAuthority != nil {
		if err = original.Roles.ValidatePrincipal(original.State.CurrentUser); err != nil {
			return RoutineAccess{}, nil, err
		}
		if err = current.Roles.ValidatePrincipal(original.State.CurrentUser); err != nil {
			return RoutineAccess{}, nil, err
		}
		current.State.CurrentUser = original.State.CurrentUser
	}
	// Environment carries only the already-owned physical TEMP/path capabilities;
	// routine EXECUTE never reads it, and it is not identity or MAC authority.
	return RoutineAccess{View: current, Env: original.Env}, dependencies, nil
}
