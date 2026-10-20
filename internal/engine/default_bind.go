package engine

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindDefault(raw parser.Expr, column catalog.Column) (expr.Expr, error) {
	// Recursive binding carries this consumer policy through Boolean and
	// comparison children, preserving their ordinary diagnostic order.
	previous := b.consumer.defaultConsumer
	b.consumer.defaultConsumer = true
	defer func() { b.consumer.defaultConsumer = previous }()
	inputUsage := column.Usage.Type.Usage()
	if inputUsage.Type.ID() == sqltype.Interval {
		inputUsage = column.Usage
	}
	node, err := b.bind(raw, expressionPolicy{context: projectionContext, expected: inputUsage})
	if err != nil {
		return nil, err
	}
	if node.Properties().Volatility > expr.Stable && !closedSequenceDefault(node) && !closedUUIDDefault(node) && !closedTransactionTimeDefault(node) && !closedPgcryptoDefault(node) && !closedSQLRoutineDefault(node) {
		return nil, expr.ErrorAt("0A000", "unsupported volatile DEFAULT expression", parser.ExpressionStart(raw))
	}
	if node.ResultUsage() != column.Usage {
		input, annotations := analyze.DomainCoercionInput(node, column.Usage)
		info, err := analyze.StorageCoercionInfo(b.bindingCatalog(), b.info(column.Usage, parser.Position{}, input.Properties()), input)
		if err != nil {
			return nil, err
		}
		coercionPlan, err := sqltype.PlanCoercion(input.ResultUsage(), info.Usage, sqltype.Assignment)
		if err != nil {
			if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "42804" {
				d := sqlError("42804", fmt.Sprintf(`column "%s" is of type %s but default expression is of type %s`, column.Name, column.Usage.Type.DiagnosticName(), node.ResultUsage().Type.DiagnosticName())).(*sqlstate.Error)
				d.Hint = "You will need to rewrite or cast the expression."
				return nil, d
			}
			return nil, err
		}
		info.Property = analyze.CoercionProperties(input, coercionPlan)
		node = analyze.RestoreDomainCastCollations(annotations, plan.CoercionExpr{Info: info, Child: input, Plan: coercionPlan, Mode: sqltype.Assignment})
	}
	return analyze.StorageDestination(node, column.Usage, parser.Position{})
}
