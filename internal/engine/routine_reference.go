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
	if !ok || d == nil || d.Address().ClassID != ids.ProcedureClassID || d.Address().ObjectID == 0 || d.Address().SubID != 0 || d.Revision() == 0 {
		return nil, fmt.Errorf("stuntgres: invalid routine reference carrier")
	}
	return d, nil
}

// Ownership's domain tag proves carrier provenance only. The captured owner and
// ACL are never used as a permission decision, and a COW ACL/owner replacement
// does not require captured-descriptor pointer equality with the current root.
func validateRoutineDescriptor(view CatalogRead, roles authorization.Read, captured *procedureDescriptor) (*procedureDescriptor, error) {
	if err := roles.ValidatePrincipal(captured.Owner); err != nil {
		return nil, err
	}
	return currentRoutineDeclaration(view, captured)
}

// validateStoredRoutineDescriptor is validateRoutineDescriptor for stored SQL
// validation, which holds no role snapshot: the owner's provenance is checked
// against the principal domain of view's own engine, which is what the
// engine's committed role root would check.
func validateStoredRoutineDescriptor(view CatalogRead, captured *procedureDescriptor) (*procedureDescriptor, error) {
	if _, ok := view.Database(currentDatabaseAddress); !ok || view.Owner() == nil || captured.Owner.ID() == 0 || captured.Owner != view.Owner().Principals.Principal(captured.Owner.ID()) {
		return nil, fmt.Errorf("stuntgres: foreign or zero principal")
	}
	return currentRoutineDeclaration(view, captured)
}

func currentRoutineDeclaration(view CatalogRead, captured *procedureDescriptor) (*procedureDescriptor, error) {
	current, ok := view.Procedure(captured.Address())
	if !ok || !sameRoutineDeclaration(current, captured) {
		return nil, fmt.Errorf("stuntgres: stale routine declaration carrier")
	}
	return current, nil
}

func sameRoutineDeclaration(current, captured *procedureDescriptor) bool {
	return current != nil && captured != nil &&
		current.Name() == captured.Name() && current.Signature == captured.Signature &&
		current.RoutineKind == captured.RoutineKind && current.Result == captured.Result &&
		current.Strict == captured.Strict && current.Volatility == captured.Volatility &&
		current.Builtin == captured.Builtin && current.Implementation == captured.Implementation &&
		current.Conversion == captured.Conversion
}

func (d *relationDescriptor) RoutineRecipeOID() uint32 { return uint32(d.Address().ObjectID) }

func expressionRecipeDescriptor(node expr.Expr) (*relationDescriptor, error) {
	carrier, ok := node.(interface {
		CallRoutineRecipeReference() expr.RoutineRecipeReference
	})
	if !ok || carrier.CallRoutineRecipeReference() == nil {
		return nil, nil
	}
	recipe, ok := carrier.CallRoutineRecipeReference().(*relationDescriptor)
	if !ok || recipe == nil || recipe.View == nil || recipe.RelationKind != viewRelation || recipe.Address().ClassID != ids.RelationClassID || recipe.Address().ObjectID == 0 {
		return nil, fmt.Errorf("stuntgres: invalid grafted routine recipe")
	}
	if consumer, temporary := recipe.View.TemporaryConsumer(); consumer != (ids.ObjectAddress{}) && consumer != recipe.Address() ||
		temporary != (ids.SessionToken{}) && consumer != recipe.Address() {
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
		current, ok := auth.catalog.Relation(recipe.Address())
		if !ok || current.View != recipe.View || recipe.Owner.ID() == 0 || recipe.Owner != auth.engine.authorizationDomain.Principal(recipe.Owner.ID()) {
			return ids.ErrCatalogReplaced
		}
	}
	c.routineChecks = viewOf(recipe).dependencies
	return nil
}
