package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/sqltype"
)

func validatePolicyExpression(view CatalogRead, owner *relationDescriptor, expression *policyExpression) error {
	if expression.syntax == nil || expression.expression == nil || expression.expression.ResultUsage().Type == nil || expression.expression.ResultUsage().Type.ID() != sqltype.Bool || expression.presentation == nil {
		return fmt.Errorf("stuntgres: invalid policy condition")
	}
	var invalid bool
	walkBoundExpression(expression.expression, func(node BoundExpr) {
		switch node.(type) {
		case parameterExpression, aggregateExpression, *windowExpression:
			invalid = true
		}
	})
	if invalid {
		return fmt.Errorf("stuntgres: invalid policy-level expression")
	}
	walkQueryArgumentExpression(expression.expression, func(node BoundExpr) {
		if _, parameter := node.(parameterExpression); parameter {
			invalid = true
		}
	})
	if invalid {
		return fmt.Errorf("stuntgres: parameter in policy definition")
	}
	frozen := expression.presentation
	if frozen.query == nil || frozen.query.Root == nil || len(frozen.sources) == 0 || frozen.sources[0].ref.Address != owner.address {
		return fmt.Errorf("stuntgres: invalid policy owner frame")
	}
	if err := validateViewLeaves(view, frozen); err != nil {
		return err
	}
	if err := frozen.validateLiterals(view); err != nil {
		return err
	}
	if err := frozen.validateNamespaces(); err != nil {
		return err
	}
	for _, usage := range frozen.castUsages {
		if !validExpressionUsage(view, usage) {
			return fmt.Errorf("stuntgres: invalid policy cast authority")
		}
		if err := validateUsageCollation(view, usage); err != nil {
			return err
		}
	}
	for _, leaf := range frozen.sources {
		source, ok := view.state.descriptors[leaf.ref.Address].(*relationDescriptor)
		if !ok || leaf.ref.Domain() != view.owner.catalog.domain || leaf.ref.Address.SubID != 0 {
			return fmt.Errorf("stuntgres: invalid policy source identity")
		}
		for _, attribute := range leaf.reads {
			if !source.hasAttribute(attribute) {
				return fmt.Errorf("stuntgres: missing policy dependency attribute")
			}
		}
	}
	for _, dep := range frozen.dependencies {
		a := dep.ref.Address
		whole := a
		whole.SubID = 0
		provider, ok := view.Descriptor(whole)
		if !ok || dep.ref.Domain() != view.owner.catalog.domain {
			return fmt.Errorf("stuntgres: invalid policy provider identity")
		}
		if a.SubID != 0 {
			relation, ok := provider.(*relationDescriptor)
			if !ok || !relation.hasAttribute(a.SubID) {
				return fmt.Errorf("stuntgres: missing policy provider attribute")
			}
		}
	}
	references, err := pgDependExpressionRefs(context.Background(), view, expression.expression)
	if err != nil {
		return err
	}
	if len(references) != len(frozen.dependencies) {
		return fmt.Errorf("stuntgres: invalid policy dependency manifest")
	}
	for _, address := range references {
		found := false
		for _, dependency := range frozen.dependencies {
			if dependency.ref.Address == address {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("stuntgres: missing policy expression dependency")
		}
	}
	_, err = policyExpressionSQL(context.Background(), view, expression)
	return err
}
