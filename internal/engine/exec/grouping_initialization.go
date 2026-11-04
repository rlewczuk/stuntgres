package exec

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
)

// InitializeQueryEqualityRoutine is the retained equality-expression
// initialization, not an Equal invocation or a hash-support grant.
func InitializeQueryEqualityRoutine(c *Context, ref ids.ObjectRef, original *catalog.Procedure, recipe expr.RoutineRecipeReference, source expr.Info, phase RoutineInitializationPhase) error {
	if err := BeginRoutineInitialization(c); err != nil {
		return err
	}
	local := *c
	local.ClearNativeBodySource()
	if recipe != nil && (!local.RoutineGraft || local.RoutineGraftSource != recipe) || recipe == nil && local.RoutineGraft {
		if err := SelectExpressionRecipe(&local, recipe); err != nil {
			return err
		}
	}
	owner := local.Catalog.Owner()
	if owner == nil || owner.Sepgsql == nil {
		return AdmitRoutineReference(&local, ref, original, "function")
	}
	if original == nil || original.Address() != ref.Address {
		return fmt.Errorf("stuntgres: missing original grouping equality declaration")
	}
	local.InitializationPhase = phase
	if local.ClosedEvaluation != nil {
		if err := validateRoutineExpressionInitialization(&local, original, source); err != nil {
			return err
		}
	} else {
		auth, dependencies, err := RoutineAdmissionAuthority(&local)
		if err != nil {
			return err
		}
		if err = initializeRoutineExpression(&local, auth, dependencies, original, source, 0); err != nil {
			return err
		}
	}
	return BeginDirectRoutineInitialization(&local, DirectRoutineInitializationSite{Routine: original, Reference: ref, Purpose: DirectGroupingEqual, Occurrence: source.Occurrence, ExpressionSource: source.Source})
}
