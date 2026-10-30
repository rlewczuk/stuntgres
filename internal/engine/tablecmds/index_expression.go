package tablecmds

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
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

// transformIndexExpression performs only method-independent parse analysis.
// Caller holds Engine.Mu and retains the result within this CREATE operation.
func transformIndexExpression(e *core.Engine, op *session.Operation, input sqltype.InputEnvironment, auth core.AuthorizationView, parent *catalog.Relation, raw parser.Expr, literals []plan.LiteralExpr) (index.ExpressionTransform, error) {
	binder := e.IndexExpressionBinder(op, input, auth, parent)
	binder.Stored.CheckLiterals = literals
	definition, err := binder.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	if err != nil {
		return index.ExpressionTransform{}, err
	}
	return index.ExpressionTransform{Definition: definition, Input: binder.Input, Dependencies: binder.Dependencies, Uses: binder.Query.Sources[0].Uses}, nil
}

// completeIndexExpression admits the physical expression after destination and
// method selection. Ordering belongs to the caller's selected class/collation.
func completeIndexExpression(op *session.Operation, auth core.AuthorizationView, parent *catalog.Relation, raw parser.Expr, transformed index.ExpressionTransform) (*catalog.DerivedIndexKey, string, error) {
	definition := transformed.Definition
	if err := index.RejectVirtualUses(parent, transformed.Uses); err != nil {
		return nil, "", err
	}
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: op.Context(), Input: transformed.Input, Catalog: auth.Catalog}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	evaluation.RoutineAuthority, evaluation.RoutineChecks = &auth, transformed.Dependencies
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
		return nil, "", core.SQLError("42P17", "functions in index expression must be marked IMMUTABLE")
	}
	key := &catalog.DerivedIndexKey{Definition: definition, Execution: execution, Attributes: parent.Attributes}
	if _, err = (stored.ScalarRenderer{ACLOutput: regobj.ACLItemOutput{Owner: auth.Owner, Roles: auth.Roles}, Owner: parent, Dependencies: &key.Dependencies}).SQL(op.Context(), auth.Catalog, definition); err != nil {
		return nil, "", err
	}
	label := "expr"
	switch r := raw.(type) {
	case parser.XMLDocumentPredicateExpr:
		label = "xml_is_document"
	case parser.XMLParseExpr:
		label = "xmlparse"
	case parser.XMLSerializeExpr:
		label = "xmlserialize"
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
