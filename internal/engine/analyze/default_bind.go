package analyze

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (b *Binder) BindDefault(raw parser.Expr, column catalog.Column) (expr.Expr, error) {
	// Recursive binding carries this consumer policy through Boolean and
	// comparison children, preserving their ordinary diagnostic order.
	previous := b.Consumer.DefaultConsumer
	b.Consumer.DefaultConsumer = true
	defer func() { b.Consumer.DefaultConsumer = previous }()
	inputUsage := column.Usage.Type.Usage()
	if inputUsage.Type.ID() == sqltype.Interval {
		inputUsage = column.Usage
	}
	node, err := b.Bind(raw, ExpressionPolicy{Context: ProjectionContext, Expected: inputUsage})
	if err != nil {
		return nil, err
	}
	if node.Properties().Volatility > expr.Stable && !ClosedSequenceDefault(node) && !ClosedUUIDDefault(node) && !ClosedTransactionTimeDefault(node) && !ClosedPgcryptoDefault(node) && !ClosedSQLRoutineDefault(node) {
		return nil, expr.ErrorAt("0A000", "unsupported volatile DEFAULT expression", parser.ExpressionStart(raw))
	}
	if node.ResultUsage() != column.Usage {
		input, annotations := DomainCoercionInput(node, column.Usage)
		info, err := StorageCoercionInfo(b.BindingCatalog(), b.Info(column.Usage, parser.Position{}, input.Properties()), input)
		if err != nil {
			return nil, err
		}
		if selected, handled, selectionErr := b.declaredCoercion(input, info.Usage, sqltype.Assignment, raw.SourcePosition(), raw); handled {
			if selectionErr != nil {
				return nil, selectionErr
			}
			return StorageDestination(RestoreDomainCastCollations(annotations, selected), column.Usage, parser.Position{})
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
		info.Property = CoercionProperties(input, coercionPlan)
		node = RestoreDomainCastCollations(annotations, plan.CoercionExpr{Info: info, Child: input, Plan: coercionPlan, Mode: sqltype.Assignment})
	}
	return StorageDestination(node, column.Usage, parser.Position{})
}
