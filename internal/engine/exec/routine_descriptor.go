package exec

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// The cross-package interface is an identity carrier, not catalog authority.
// Dynamic extraction rejects arbitrary implementations and typed nil pointers.
func ExpressionRoutineDescriptor(node any) (*catalog.Procedure, error) {
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
	d, ok := reference.(*catalog.Procedure)
	if !ok || d == nil || d.Address().ClassID != ids.ProcedureClassID || d.Address().ObjectID == 0 || d.Address().SubID != 0 || d.Revision() == 0 {
		return nil, fmt.Errorf("stuntgres: invalid routine reference carrier")
	}
	return d, nil
}

// Ownership's domain tag proves carrier provenance only. The captured owner and
// ACL are never used as a permission decision, and a COW ACL/owner replacement
// does not require captured-descriptor pointer equality with the current root.
func ValidateRoutineDescriptor(view catalog.Read, roles authorization.Read, captured *catalog.Procedure) (*catalog.Procedure, error) {
	if err := roles.ValidatePrincipal(captured.Owner); err != nil {
		return nil, err
	}
	return currentRoutineDeclaration(view, captured)
}

// ValidateStoredRoutineDescriptor is ValidateRoutineDescriptor for stored SQL
// validation, which holds no role snapshot: the owner's provenance is checked
// against the principal domain of view's own engine, which is what the
// engine's committed role root would check.
func ValidateStoredRoutineDescriptor(view catalog.Read, captured *catalog.Procedure) (*catalog.Procedure, error) {
	if _, ok := view.Database(catalog.CurrentDatabaseAddress); !ok || view.Owner() == nil || captured.Owner.ID() == 0 || captured.Owner != view.Owner().Principals.Principal(captured.Owner.ID()) {
		return nil, fmt.Errorf("stuntgres: foreign or zero principal")
	}
	return currentRoutineDeclaration(view, captured)
}

func currentRoutineDeclaration(view catalog.Read, captured *catalog.Procedure) (*catalog.Procedure, error) {
	current, ok := view.Procedure(captured.Address())
	if !ok || !SameRoutineDeclaration(current, captured) {
		return nil, fmt.Errorf("stuntgres: stale routine declaration carrier")
	}
	return current, nil
}

func SameRoutineDeclaration(current, captured *catalog.Procedure) bool {
	return current != nil && captured != nil &&
		current.Name() == captured.Name() && current.Signature == captured.Signature &&
		current.RoutineKind == captured.RoutineKind && current.Result == captured.Result &&
		current.Strict == captured.Strict && current.Volatility == captured.Volatility &&
		current.Builtin == captured.Builtin && current.Implementation == captured.Implementation &&
		current.Conversion == captured.Conversion && current.SQL == captured.SQL
}

func ExpressionRecipeDescriptor(node any) (*catalog.Relation, error) {
	carrier, ok := node.(interface {
		CallRoutineRecipeReference() expr.RoutineRecipeReference
	})
	if !ok || carrier.CallRoutineRecipeReference() == nil {
		return nil, nil
	}
	recipe, ok := carrier.CallRoutineRecipeReference().(*catalog.Relation)
	if !ok || recipe == nil || recipe.View == nil || recipe.RelationKind != catalog.ViewRelation || recipe.Address().ClassID != ids.RelationClassID || recipe.Address().ObjectID == 0 {
		return nil, fmt.Errorf("stuntgres: invalid grafted routine recipe")
	}
	if consumer, temporary := recipe.View.TemporaryConsumer(); consumer != (ids.ObjectAddress{}) && consumer != recipe.Address() ||
		temporary != (ids.SessionToken{}) && consumer != recipe.Address() {
		return nil, fmt.Errorf("stuntgres: invalid grafted routine recipe")
	}
	return recipe, nil
}

func PolymorphicBodyArgument(node expr.Expr, id ids.OID) expr.Expr {
	switch n := node.(type) {
	case functions.StringVariadic:
		if (id == 1285 || id == 1290) && len(n.Args) == 1 {
			return n.Args[0]
		}
	case functions.String:
		if id == 2003 && len(n.Args) == 2 {
			return n.Args[1]
		}
		if id == 2004 && len(n.Args) == 2 {
			return n.Args[0]
		}
	}
	return nil
}

func BodyCastMatches(edge catalog.RoutineBodyEdge, argument expr.Expr) bool {
	if argument == nil {
		return false
	}
	id := argument.ResultUsage().Type.BaseType().ID()
	return edge.Demand == catalog.BodyBooleanText && id == sqltype.Bool || edge.Demand == catalog.BodyNetworkText && (id == sqltype.Inet || id == sqltype.Cidr)
}

// Lifetime capture is conservative even where strict NULL later removes actual
// invocation. Only instance-selected polymorphic cast identities participate.
func WalkRoutineBody(outer *catalog.Procedure, node expr.Expr, visit func(*catalog.Procedure) error) error {
	argument := PolymorphicBodyArgument(node, outer.Address().ObjectID)
	for _, edge := range outer.Body {
		if (edge.Demand == catalog.BodyBooleanText || edge.Demand == catalog.BodyNetworkText) && !BodyCastMatches(edge, argument) {
			continue
		}
		if err := visit(edge.Routine); err != nil {
			return err
		}
		if err := WalkRoutineBody(edge.Routine, nil, visit); err != nil {
			return err
		}
	}
	return nil
}
