package engine

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
)

// The cross-package interface is an identity carrier, not catalog authority.
// Dynamic extraction rejects arbitrary implementations and typed nil pointers.
func expressionRoutineDescriptor(node expr.Expr) (*procedureDescriptor, error) {
	call, ok := node.(interface {
		CallRoutineReference() expr.RoutineReference
	})
	if !ok {
		return nil, nil
	}
	reference := call.CallRoutineReference()
	if reference == nil {
		return nil, nil
	}
	d, ok := reference.(*procedureDescriptor)
	if !ok || d == nil || d.address.ClassID != ids.ProcedureClassID || d.address.ObjectID == 0 || d.address.SubID != 0 || d.revision == 0 {
		return nil, fmt.Errorf("stuntgres: invalid routine reference carrier")
	}
	return d, nil
}

// Ownership's domain tag proves carrier provenance only. The captured owner and
// ACL are never used as a permission decision, and a COW ACL/owner replacement
// does not require captured-descriptor pointer equality with the current root.
func validateRoutineDescriptor(view CatalogRead, roles authorization.Read, captured *procedureDescriptor) (*procedureDescriptor, error) {
	if err := roles.ValidatePrincipal(captured.owner); err != nil {
		return nil, err
	}
	return currentRoutineDeclaration(view, captured)
}

// validateStoredRoutineDescriptor is validateRoutineDescriptor for stored SQL
// validation, which holds no role snapshot: the owner's provenance is checked
// against the principal domain of view's own engine, which is what the
// engine's committed role root would check.
func validateStoredRoutineDescriptor(view CatalogRead, captured *procedureDescriptor) (*procedureDescriptor, error) {
	if _, ok := view.Database(currentDatabaseAddress); !ok || view.owner == nil || captured.owner.ID() == 0 || captured.owner != view.owner.principals.Principal(captured.owner.ID()) {
		return nil, fmt.Errorf("stuntgres: foreign or zero principal")
	}
	return currentRoutineDeclaration(view, captured)
}

func currentRoutineDeclaration(view CatalogRead, captured *procedureDescriptor) (*procedureDescriptor, error) {
	current, ok := view.Procedure(captured.address)
	if !ok || !sameRoutineDeclaration(current, captured) {
		return nil, fmt.Errorf("stuntgres: stale routine declaration carrier")
	}
	return current, nil
}

func sameRoutineDeclaration(current, captured *procedureDescriptor) bool {
	return current != nil && captured != nil &&
		current.name == captured.name && current.signature == captured.signature &&
		current.routineKind == captured.routineKind && current.result == captured.result &&
		current.strict == captured.strict && current.volatility == captured.volatility &&
		current.builtin == captured.builtin && current.provider == captured.provider &&
		current.queryCodec == captured.queryCodec && current.crypto == captured.crypto &&
		current.fuzzy == captured.fuzzy && current.intagg == captured.intagg &&
		current.uuidOssp == captured.uuidOssp &&
		current.conversion == captured.conversion
}

func (d *relationDescriptor) RoutineRecipeOID() uint32 { return uint32(d.address.ObjectID) }

func expressionRecipeDescriptor(node expr.Expr) (*relationDescriptor, error) {
	carrier, ok := node.(interface {
		CallRoutineRecipeReference() expr.RoutineRecipeReference
	})
	if !ok || carrier.CallRoutineRecipeReference() == nil {
		return nil, nil
	}
	recipe, ok := carrier.CallRoutineRecipeReference().(*relationDescriptor)
	if !ok || recipe == nil || recipe.view == nil || recipe.relationKind != viewRelation || recipe.address.ClassID != ids.RelationClassID || recipe.address.ObjectID == 0 ||
		recipe.view.consumer != (ids.ObjectAddress{}) && recipe.view.consumer != recipe.address ||
		recipe.view.temporary != (ids.SessionToken{}) && recipe.view.consumer != recipe.address {
		return nil, fmt.Errorf("stuntgres: invalid grafted routine recipe")
	}
	return recipe, nil
}

// Only evaluator-local stack copies select graft ancestry. A user replacement
// cannot inherit the enclosing stored node's manifest or relation owner.
func (c *EvalContext) selectExpressionRecipe(recipe *relationDescriptor) error {
	c.routineGraft = recipe != nil
	c.routineRecipe = recipe != nil
	c.routineReferences = nil
	c.routineChecks = nil
	c.routineConsumer = recipe
	if recipe == nil {
		return nil
	}
	auth, _, err := c.routineAdmissionAuthority()
	if err != nil {
		return err
	}
	if auth.engine != nil {
		current, ok := auth.catalog.Relation(recipe.address)
		if !ok || current.view != recipe.view || recipe.owner.ID() == 0 || recipe.owner != auth.engine.authorizationDomain.Principal(recipe.owner.ID()) {
			return ids.ErrCatalogReplaced
		}
	}
	c.routineChecks = recipe.view.dependencies
	return nil
}
