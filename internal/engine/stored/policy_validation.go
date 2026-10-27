package stored

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func ValidatePolicy(view catalog.Read, owner *catalog.Relation, expression *Policy) error {
	if expression.syntax == nil || expression.expression == nil || expression.expression.ResultUsage().Type == nil || expression.expression.ResultUsage().Type.ID() != sqltype.Bool || expression.presentation == nil {
		return fmt.Errorf("stuntgres: invalid policy condition")
	}
	var invalid bool
	plan.WalkExpression(expression.expression, func(node expr.Expr) {
		switch node.(type) {
		case plan.ParameterExpr, plan.AggregateExpr, *plan.WindowExpr:
			invalid = true
		}
	})
	if invalid {
		return fmt.Errorf("stuntgres: invalid policy-level expression")
	}
	analyze.WalkQueryArgumentExpression(expression.expression, func(node expr.Expr) {
		if _, parameter := node.(plan.ParameterExpr); parameter {
			invalid = true
		}
	})
	if invalid {
		return fmt.Errorf("stuntgres: parameter in policy definition")
	}
	frozen := expression.presentation
	if frozen.query == nil || frozen.query.Root == nil || len(frozen.sources) == 0 || frozen.sources[0].Ref.Address != owner.Address() {
		return fmt.Errorf("stuntgres: invalid policy owner frame")
	}
	if err := ValidateViewLeaves(view, frozen); err != nil {
		return err
	}
	if err := frozen.ValidateLiterals(view); err != nil {
		return err
	}
	if err := frozen.validateNamespaces(); err != nil {
		return err
	}
	for path, usage := range frozen.castUsages {
		if !frozen.validCapturedCastUsage(view, path, usage) {
			return fmt.Errorf("stuntgres: invalid policy cast authority")
		}
		if err := ValidateUsageCollation(view, usage); err != nil {
			return err
		}
	}
	for _, leaf := range frozen.sources {
		source, ok := view.Relation(leaf.Ref.Address)
		if !ok || leaf.Ref.Domain() != view.Owner().Domain || leaf.Ref.Address.SubID != 0 {
			return fmt.Errorf("stuntgres: invalid policy source identity")
		}
		for _, attribute := range leaf.Reads {
			if !source.HasAttribute(attribute) {
				return fmt.Errorf("stuntgres: missing policy dependency attribute")
			}
		}
	}
	for _, dep := range frozen.dependencies {
		a := dep.Ref.Address
		whole := a
		whole.SubID = 0
		provider, ok := view.Descriptor(whole)
		if !ok || dep.Ref.Domain() != view.Owner().Domain {
			return fmt.Errorf("stuntgres: invalid policy provider identity")
		}
		if a.SubID != 0 {
			relation, ok := provider.(*catalog.Relation)
			if !ok || !relation.HasAttribute(a.SubID) {
				return fmt.Errorf("stuntgres: missing policy provider attribute")
			}
		}
	}
	references, err := ExpressionDependRefs(context.Background(), view, expression.expression)
	if err != nil {
		return err
	}
	// Catalog dependency recording excludes pinned providers; the execution
	// manifest must retain them. Every provider above is still validated, and
	// every semantic expression edge below must remain represented.
	for _, address := range references {
		found := false
		for _, dependency := range frozen.dependencies {
			if dependency.Ref.Address == address {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("stuntgres: missing policy expression dependency")
		}
	}
	_, err = PolicySQL(context.Background(), view, expression)
	return err
}
