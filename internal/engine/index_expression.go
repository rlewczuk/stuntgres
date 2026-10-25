package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Caller holds engine.mu and supplies the same final catalog/parent authority
// used by the enclosing CREATE or TYPE owner.
// Ordering admission belongs to the caller's final selected class/collation,
// not an expression-only default comparator.
func bindIndexExpression(e *Engine, op *session.Operation, input sqltype.InputEnvironment, auth AuthorizationView, parent *catalog.Relation, raw parser.Expr, literals []plan.LiteralExpr) (*catalog.DerivedIndexKey, string, error) {
	binder := e.indexExpressionBinder(op, input, auth, parent)
	binder.Stored.CheckLiterals = literals
	definition, err := binder.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	if err != nil {
		return nil, "", err
	}
	if err := index.RejectVirtualUses(parent, binder.Query.Sources[0].Uses); err != nil {
		return nil, "", err
	}
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: op.Context(), Input: binder.Input, Catalog: auth.Catalog}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	evaluation.RoutineAuthority, evaluation.RoutineChecks = &auth, binder.Dependencies
	evaluation.RoutineRecipe = true
	execution, _, err := exec.PreflightCoercionsWithPolicy(&evaluation, definition, true)
	if err != nil {
		return nil, "", err
	}
	// Preflight the complete analyzed definition before narrowing runtime
	// demand, then admit volatility from the surviving physical providers.
	execution, _, err = exec.SimplifyConditional(&evaluation, execution, true)
	if err != nil {
		return nil, "", err
	}
	if exec.PlannedVolatility(execution) != expr.Immutable {
		return nil, "", SQLError("42P17", "functions in index expression must be marked IMMUTABLE")
	}
	key := &catalog.DerivedIndexKey{Definition: definition, Execution: execution, Attributes: parent.Attributes}
	if _, err = (stored.ScalarRenderer{ACLOutput: regobj.ACLItemOutput{Owner: auth.Owner, Roles: auth.Roles}, Owner: parent, Dependencies: &key.Dependencies}).SQL(op.Context(), auth.Catalog, definition); err != nil {
		return nil, "", err
	}
	label := "expr"
	switch r := raw.(type) {
	case parser.ColumnExpr:
		label = r.Name.Parts[len(r.Name.Parts)-1].Text
	case parser.SQLJSONQueryExpr:
		label = r.Kind
	case parser.SQLJSONExpr:
		label = r.Kind
	case parser.FunctionExpr:
		label = r.Name.Parts[len(r.Name.Parts)-1].Text
	case parser.CallExpr:
		label = r.Name.Parts[len(r.Name.Parts)-1].Text
	}
	return key, label, nil
}
