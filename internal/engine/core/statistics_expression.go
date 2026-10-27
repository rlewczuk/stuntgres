package core

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// BindStatisticsExpression uses the ordinary single-relation binder and executor.
// Unlike index recipes, statistics admit stable and volatile providers; CREATE
// checks coercions but must not evaluate the expression's runtime value.
func (h DDLHost) BindStatisticsExpression(a ddl.Authority, parent *catalog.Relation, raw parser.Expr, input sqltype.InputEnvironment) (catalog.StatisticsExpression, error) {
	auth := DDLAuthorityOf(a)
	binder := h.E.IndexExpressionBinder(h.Op, input, auth, parent)
	binder.Consumer.IndexConsumer = false
	binder.Consumer.StatisticsConsumer = true
	definition, err := binder.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	if err != nil {
		return catalog.StatisticsExpression{}, err
	}
	if exec.SourceReturnsSet(definition) {
		return catalog.StatisticsExpression{}, expr.ErrorAt("0A000", "set-returning functions are not allowed in statistics expressions", raw.SourcePosition())
	}
	var forbidden error
	plan.WalkExpression(definition, func(node expr.Expr) {
		if column, ok := node.(plan.ColumnExpr); ok && column.Virtual != nil {
			forbidden = SQLError("0A000", "statistics creation on virtual generated columns is not supported")
		}
	})
	if forbidden != nil {
		return catalog.StatisticsExpression{}, forbidden
	}
	inputs := make([]ids.ObjectRef, 0, len(binder.Query.Sources[0].Uses))
	for _, slot := range binder.Query.Sources[0].Uses {
		attr := parent.Attributes[slot]
		if attr <= 0 {
			return catalog.StatisticsExpression{}, SQLError("0A000", "statistics creation on system columns is not supported")
		}
		if parent.ColumnMeta.Generations[attr].Kind == catalog.GenerationVirtual {
			return catalog.StatisticsExpression{}, SQLError("0A000", "statistics creation on virtual generated columns is not supported")
		}
		address := parent.Address()
		address.SubID = attr
		inputs = append(inputs, auth.Owner.Domain.Ref(auth.Catalog.Epoch(), address))
	}
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: h.Op.Context(), Input: binder.Input, Catalog: auth.Catalog}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	evaluation.RoutineAuthority, evaluation.RoutineChecks = &auth, binder.Dependencies
	evaluation.RoutineRecipe = true
	execution, _, err := exec.PreflightCoercionsWithPolicy(&evaluation, definition, true)
	if err != nil {
		return catalog.StatisticsExpression{}, err
	}
	execution, _, err = exec.SimplifyConditional(&evaluation, execution, true)
	if err != nil {
		return catalog.StatisticsExpression{}, err
	}
	var dependencies []ids.ObjectRef
	if _, err = (stored.ScalarRenderer{ACLOutput: regobj.ACLItemOutput{Owner: auth.Owner, Roles: auth.Roles}, Owner: parent, Dependencies: &dependencies}).SQL(h.Op.Context(), auth.Catalog, definition); err != nil {
		return catalog.StatisticsExpression{}, err
	}
	return catalog.NewStatisticsExpression(definition, execution, parent.Attributes, inputs, dependencies, definition.ResultUsage()), nil
}
func (h DDLHost) SameStatisticsExpression(a, b catalog.StatisticsExpression) bool {
	binder := analyze.Binder{}
	binder.Consumer.CheckDefinitionEquality = true
	return binder.SameGroupExpression(a.Definition(), b.Definition())
}
