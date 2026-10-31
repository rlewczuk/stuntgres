package core

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
)

// BindExpression binds a statistics expression with the ordinary
// single-relation binder (the index expression binder over parent) and
// executor. Unlike index recipes, statistics admit stable and volatile
// providers; CREATE checks coercions but must not evaluate the expression's
// runtime value.
func BindExpression(op *session.Operation, binder *analyze.Binder, auth RecipeAuthority, parent *catalog.Relation, raw parser.Expr) (catalog.StatisticsExpression, error) {
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
			forbidden = sqlError("0A000", "statistics creation on virtual generated columns is not supported")
		}
	})
	if forbidden != nil {
		return catalog.StatisticsExpression{}, forbidden
	}
	view := auth.Access()
	inputs := make([]ids.ObjectRef, 0, len(binder.Query.Sources[0].Uses))
	for _, slot := range binder.Query.Sources[0].Uses {
		attr := parent.Attributes[slot]
		if attr <= 0 {
			return catalog.StatisticsExpression{}, sqlError("0A000", "statistics creation on system columns is not supported")
		}
		if parent.ColumnMeta.Generations[attr].Kind == catalog.GenerationVirtual {
			return catalog.StatisticsExpression{}, sqlError("0A000", "statistics creation on virtual generated columns is not supported")
		}
		address := parent.Address()
		address.SubID = attr
		inputs = append(inputs, view.Owner.Domain.Ref(view.Catalog.Epoch(), address))
	}
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: op.Context(), Input: binder.Input, Catalog: view.Catalog}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	evaluation.RoutineAuthority, evaluation.RoutineChecks = auth, binder.Dependencies
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
	view = auth.Access()
	if _, err = (stored.ScalarRenderer{ACLOutput: regobj.ACLItemOutput{Owner: view.Owner, Roles: view.Roles}, Owner: parent, Dependencies: &dependencies}).SQL(op.Context(), view.Catalog, definition); err != nil {
		return catalog.StatisticsExpression{}, err
	}
	return catalog.NewStatisticsExpression(definition, execution, parent.Attributes, inputs, dependencies, definition.ResultUsage()), nil
}

// SameExpression reports whether two statistics expressions are the same
// definition.
func SameExpression(a, b catalog.StatisticsExpression) bool {
	binder := analyze.Binder{}
	binder.Consumer.CheckDefinitionEquality = true
	return binder.SameGroupExpression(a.Definition(), b.Definition())
}
