package exec

import (
	"fmt"
	"slices"

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
	if view.Owner() == nil {
		return nil, fmt.Errorf("stuntgres: foreign or zero principal")
	}
	if _, ok := view.Database(view.Owner().DatabaseAddress()); !ok || captured.Owner.ID() == 0 || captured.Owner != view.Owner().Principals.Principal(captured.Owner.ID()) {
		return nil, fmt.Errorf("stuntgres: foreign or zero principal")
	}
	return currentRoutineDeclaration(view, captured)
}

func currentRoutineDeclaration(view catalog.Read, captured *catalog.Procedure) (*catalog.Procedure, error) {
	current, ok := view.Procedure(captured.Address())
	if !ok || !SameRoutineDeclaration(current, captured) {
		return nil, fmt.Errorf("stuntgres: stale routine declaration carrier")
	}
	if current.SQL != nil {
		language, exists := view.Get(current.Language).(*catalog.Language)
		if !exists || (catalog.LanguageProvider{}).Validate(view, language) != nil {
			return nil, fmt.Errorf("stuntgres: invalid routine language authority")
		}
		if current.SQL.PL != nil {
			handler, exists := view.Get(language.Call).(*catalog.Procedure)
			if !exists || handler.PLHandler != catalog.PLCallHandler || catalog.ValidatePLHandler(view, handler) != nil {
				return nil, fmt.Errorf("stuntgres: invalid PL call handler authority")
			}
		}
	}
	return current, nil
}

func SameRoutineDeclaration(current, captured *catalog.Procedure) bool {
	return current != nil && captured != nil &&
		current.Address() == captured.Address() && current.Signature == captured.Signature &&
		current.RoutineKind == captured.RoutineKind && current.Result == captured.Result &&
		current.Strict == captured.Strict && current.Volatility == captured.Volatility &&
		current.SecurityDefiner == captured.SecurityDefiner && current.Parallel == captured.Parallel &&
		current.Cost == captured.Cost && current.Rows == captured.Rows && current.Configuration == captured.Configuration &&
		current.Builtin == captured.Builtin && current.Implementation == captured.Implementation &&
		current.Installed == captured.Installed &&
		current.Display == captured.Display && current.Language == captured.Language && current.PLHandler == captured.PLHandler &&
		current.DeclarationBody == captured.DeclarationBody &&
		slices.Equal(current.DeclarationDependencies, captured.DeclarationDependencies) &&
		current.Conversion == captured.Conversion && current.SQL == captured.SQL && current.SQLBody == captured.SQLBody &&
		current.SQLDefaults == captured.SQLDefaults && current.RangeConstructor == captured.RangeConstructor && current.Extension == captured.Extension &&
		current.LiteralDefaults == captured.LiteralDefaults && current.InputNames == captured.InputNames &&
		current.AccessMethod == captured.AccessMethod && current.Dictionary == captured.Dictionary &&
		current.Foreign == captured.Foreign && sameRoutineBody(current.Body, captured.Body) && catalog.SameAggregateDefinition(current.Aggregate, captured.Aggregate)
}

func sameRoutineBody(a, b []catalog.RoutineBodyEdge) bool {
	if len(a) != len(b) {
		return false
	}
	for i, left := range a {
		right := b[i]
		if left.Demand != right.Demand {
			return false
		}
		if left.Routine == nil || right.Routine == nil {
			if left.Routine != right.Routine {
				return false
			}
			continue
		}
		if left.Routine.Address() != right.Routine.Address() || left.Routine.Builtin != right.Routine.Builtin || left.Routine.Signature != right.Routine.Signature || left.Routine.RoutineKind != right.Routine.RoutineKind || left.Routine.Result != right.Routine.Result || left.Routine.Implementation != right.Routine.Implementation || left.Routine.SQL != right.Routine.SQL {
			return false
		}
	}
	return true
}

func ExpressionRecipeDescriptor(node any) (expr.RoutineRecipeReference, error) {
	carrier, ok := node.(interface {
		CallRoutineRecipeReference() expr.RoutineRecipeReference
	})
	if !ok || carrier.CallRoutineRecipeReference() == nil {
		return nil, nil
	}
	reference := carrier.CallRoutineRecipeReference()
	_, _, err := routineRecipeParts(reference)
	return reference, err
}

func routineRecipeParts(reference expr.RoutineRecipeReference) (*catalog.Relation, catalog.ViewPayload, error) {
	var owner *catalog.Relation
	var presentation catalog.ViewPayload
	switch recipe := reference.(type) {
	case *catalog.Relation:
		if recipe == nil || recipe.RelationKind != catalog.ViewRelation {
			return nil, nil, fmt.Errorf("stuntgres: invalid grafted routine recipe")
		}
		owner, presentation = recipe, recipe.View
	case *catalog.RuleRoutineRecipe:
		if recipe == nil || recipe.Rule == 0 || recipe.Action < -1 {
			return nil, nil, fmt.Errorf("stuntgres: invalid grafted rule routine recipe")
		}
		owner, presentation = recipe.Owner, recipe.Presentation
	default:
		return nil, nil, fmt.Errorf("stuntgres: invalid grafted routine recipe")
	}
	if owner == nil || presentation == nil || owner.Address().ClassID != ids.RelationClassID || owner.Address().ObjectID == 0 {
		return nil, nil, fmt.Errorf("stuntgres: invalid grafted routine recipe")
	}
	if consumer, temporary := presentation.TemporaryConsumer(); consumer != (ids.ObjectAddress{}) && consumer != owner.Address() ||
		temporary != (ids.SessionToken{}) && consumer != owner.Address() {
		return nil, nil, fmt.Errorf("stuntgres: invalid grafted routine recipe")
	}
	return owner, presentation, nil
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
