package exec

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// FoldPlanningExpression is the demanded computation boundary shared by the
// conditional and preflight planners. Admission is deliberately before lookup:
// a saved datum is not a saved permission decision or an executable recipe.
func FoldPlanningExpression(c *Context, node expr.Expr) (sqltype.Value, error) {
	if err := EvaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	if err := admitPlanningExpression(c, node); err != nil {
		return sqltype.Value{}, err
	}
	p := node.Properties()
	group := c.FoldCaptures
	persistent := group != nil && p.Volatility == expr.Immutable && !p.RowDependent && !p.ParameterDependent && !p.StageDependent && !p.DomainDependent && !p.RuntimeDomainConversion && ConditionalConstantInputs(node)
	info := expr.InfoOf(node)
	key := FoldConstant{Offset: info.Pos.Offset, Occurrence: info.Occurrence, Source: info.Source, Origin: c.FoldOrigin, Frame: info.Source.Frame, Callable: info.RoutineReference, Result: info.Usage}
	switch n := node.(type) {
	case functions.StringVariadic:
		if origin := FoldOriginOf(n); origin.Owner != (ids.ObjectRef{}) {
			key.Origin = origin
		}
		key.Frame = n.Frame
		key.Provider = n.Name // Selected builtin provider, not written call spelling.
	case plan.CoercionExpr:
		key.Coercion = n.Plan // Includes the selected source-output/target-input route.
	case plan.SelectedOperatorExpr:
		key.Operator = n.Ref
	case plan.ComparisonExpr:
		key.Operator = n.RoutineRef
	}
	if c.Catalog.Owner() != nil {
		switch recipe := info.RoutineRecipeReference.(type) {
		case *catalog.Relation:
			if recipe.View != nil {
				key.Origin = FoldOrigin{Owner: c.Catalog.Owner().Domain.Ref(c.Catalog.Epoch(), recipe.Address()), Definition: recipe.View.RuleOID(), Revision: recipe.View.DefinitionRevision()}
			}
		case *catalog.RuleRoutineRecipe:
			key.Origin = FoldOrigin{Owner: c.Catalog.Owner().Domain.Ref(c.Catalog.Epoch(), recipe.Owner.Address()), Attribute: int32(-recipe.Action - 2), Definition: recipe.Rule, Revision: recipe.Presentation.DefinitionRevision()}
		}
	}
	if selected, err := ExpressionRoutineDescriptor(node); err != nil {
		return sqltype.Value{}, err
	} else if selected != nil {
		key.Callable = selected
	}
	index := -1
	if persistent {
		for i, saved := range group.Entries {
			if saved.Offset != key.Offset || saved.Occurrence != key.Occurrence || saved.Source != key.Source || saved.Origin != key.Origin || saved.Frame != key.Frame || saved.Provider != key.Provider {
				continue
			}
			index = i
			if sameFoldCallable(saved.Callable, key.Callable) && saved.Operator == key.Operator && saved.Result == key.Result && saved.Coercion.Equal(key.Coercion) && sameFoldInputs(saved.Inputs, node) {
				return saved.Value, nil
			}
			break
		}
	}
	if persistent && group.Fixed && !group.Sealed {
		// A bound portal already accepted this planning generation. Fresh
		// binding must agree on exact occurrence, inputs and providers; a miss
		// is incompatible provenance, not permission to plan under new settings.
		return sqltype.Value{}, ids.ErrCatalogReplaced
	}
	value, err := evaluateAdmittedPlanningExpression(c, node)
	if err != nil {
		return sqltype.Value{}, err
	}
	if err = EvaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	if persistent && !group.Sealed {
		key.Value = value // Value owns immutable type-owned payload; no argument buffer aliases.
		recordFoldUsage(group, c, key.Result)
		if selected, ok := key.Callable.(*catalog.Procedure); ok {
			recordFoldDependency(group, c, selected)
		}
		foldExpressionInputs(node, func(child expr.Expr) {
			key.Inputs = append(key.Inputs, foldInput(child))
			if child != nil {
				recordFoldUsage(group, c, child.ResultUsage())
			}
		})
		if !group.Private {
			group.Entries = append([]FoldConstant(nil), group.Entries...)
			group.Private = true
		}
		if index >= 0 {
			group.Entries[index] = key
		} else {
			group.Entries = append(group.Entries, key)
		}
	}
	return value, nil
}

// Selected operators wrap the actual constant-input provider. Include its
// inputs as well as its usage so the wrapper cannot hide changed literal data.
func foldExpressionInputs(node expr.Expr, visit func(expr.Expr)) {
	plan.ExpressionChildren(node, func(child expr.Expr) {
		visit(child)
		if _, selected := node.(plan.SelectedOperatorExpr); selected && child != nil {
			foldExpressionInputs(child, visit)
		}
	})
}

func foldInput(child expr.Expr) FoldInput {
	// Keep omitted optional arguments in their ordered slot, distinct from a
	// typed NULL. They have no usage or dependency and must not be evaluated.
	if child == nil {
		return FoldInput{}
	}
	input := FoldInput{Usage: child.ResultUsage()}
	// Values are immutable; retaining one neither copies nor evaluates a child.
	// Lookup uses type-owned physical identity, never SQL or Go float equality.
	// Invalid literals must miss, not inherit an earlier accepted value.
	if literal, ok := child.(plan.LiteralExpr); ok {
		input.Value = literal.Value
		input.UnknownNull = literal.UnknownNull
	} else {
		input.provider = true
	}
	return input
}

func sameFoldInputs(saved []FoldInput, node expr.Expr) bool {
	index, same := 0, true
	foldExpressionInputs(node, func(child expr.Expr) {
		input := foldInput(child)
		// A literal with no descriptor is invalid, even if its missing usage
		// would otherwise look like the zero representation of an omitted slot.
		literal, isLiteral := child.(plan.LiteralExpr)
		if index >= len(saved) || isLiteral && literal.Value.Type() == nil || !sameFoldInput(saved[index], input) {
			same = false
		}
		index++
	})
	return same && index == len(saved)
}

func sameFoldInput(a, b FoldInput) bool {
	if a.Usage != b.Usage || a.UnknownNull != b.UnknownNull || a.provider != b.provider {
		return false
	}
	if a.provider {
		return true // Provider identity is in the enclosing key; its inputs follow.
	}
	if a.Usage == (sqltype.Usage{}) && a.Value.Type() == nil && b.Value.Type() == nil && !a.UnknownNull {
		return true // Explicit omitted-child representation, not a typed datum.
	}
	return sqltype.SameDatum(a.Value, b.Value)
}

func sameFoldCallable(a, b expr.RoutineReference) bool {
	if a == b {
		return true
	}
	left, lok := a.(*catalog.Procedure)
	right, rok := b.(*catalog.Procedure)
	return lok && rok && SameRoutineDeclaration(left, right)
}

// Only selected executable providers/usages enter this planning manifest. Raw
// regobject operands remain in the binder's separate analysis-purpose manifest.
func recordFoldUsage(group *FoldCaptureCandidate, c *Context, usage sqltype.Usage) {
	if c.Catalog.Owner() == nil || usage.Type == nil {
		return
	}
	d := c.Catalog.Get(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(usage.Type.ID())})
	if d != nil {
		recordFoldDependency(group, c, d)
	}
}

func recordFoldDependency(group *FoldCaptureCandidate, c *Context, descriptor catalog.Descriptor) {
	if c.Catalog.Owner() == nil {
		return
	}
	ref := c.Catalog.Owner().Domain.Ref(c.Catalog.Epoch(), descriptor.Address())
	for _, saved := range group.Dependencies {
		if saved.Ref == ref {
			return
		}
	}
	if !group.DependenciesPrivate {
		group.Dependencies = append([]FoldDependency(nil), group.Dependencies...)
		group.DependenciesPrivate = true
	}
	stamp := descriptor.Revision()
	if typ, ok := descriptor.(*catalog.Type); ok && typ.UsesDomainPlanStamp() {
		stamp = typ.EffectiveDomainPlanStamp()
	}
	group.Dependencies = append(group.Dependencies, FoldDependency{Ref: ref, Descriptor: descriptor, Stamp: stamp})
}

// PruneFoldDependencies projects the dependencies of the surviving immutable
// entries. It runs only on changed candidates, never on stable retained hits.
func PruneFoldDependencies(group *FoldCaptureCandidate) {
	if !group.Private {
		return
	}
	needed := func(dependency FoldDependency) bool {
		for _, entry := range group.Entries {
			switch dependency.Ref.Address.ClassID {
			case ids.ProcedureClassID:
				if entry.Callable != nil && ids.OID(entry.Callable.RoutineOID()) == dependency.Ref.Address.ObjectID {
					return true
				}
			case ids.TypeClassID:
				if ids.OID(entry.Result.Type.ID()) == dependency.Ref.Address.ObjectID {
					return true
				}
				for _, input := range entry.Inputs {
					if input.Usage.Type != nil && ids.OID(input.Usage.Type.ID()) == dependency.Ref.Address.ObjectID {
						return true
					}
				}
			default:
				return true // Provider-owned planning dependencies have independent demand.
			}
		}
		return false
	}
	for i, dependency := range group.Dependencies {
		if needed(dependency) {
			continue
		}
		next := make([]FoldDependency, 0, len(group.Dependencies)-1)
		next = append(next, group.Dependencies[:i]...)
		for _, remaining := range group.Dependencies[i+1:] {
			if needed(remaining) {
				next = append(next, remaining)
			}
		}
		group.Dependencies, group.DependenciesPrivate = next, true
		return
	}
}
