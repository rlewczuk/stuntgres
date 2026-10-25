package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Caller holds engine.mu and supplies final parent/catalog authority.
func bindIndexPredicate(e *Engine, op *session.Operation, input sqltype.InputEnvironment, auth AuthorizationView, parent *catalog.Relation, raw parser.Expr) (*catalog.IndexPredicate, error) {
	if raw == nil {
		return nil, nil
	}
	binder := e.IndexExpressionBinder(op, input, auth, parent)
	binder.Consumer.IndexPredicateConsumer = true
	definition, err := binder.BindBoolean(raw, analyze.FilterContext, "WHERE")
	if err != nil {
		return nil, err
	}
	if err := index.RejectVirtualUses(parent, binder.Query.Sources[0].Uses); err != nil {
		return nil, err
	}
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: op.Context(), Input: binder.Input, Catalog: auth.Catalog}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	evaluation.RoutineAuthority, evaluation.RoutineChecks = &auth, binder.Dependencies
	evaluation.RoutineRecipe = true
	execution, _, err := exec.PreflightCoercionsWithPolicy(&evaluation, definition, true)
	if err != nil {
		return nil, err
	}
	if exec.PlannedVolatility(execution) != expr.Immutable {
		return nil, SQLError("42P17", "functions in index predicate must be marked IMMUTABLE")
	}
	var dependencies []ids.ObjectRef
	if _, err = (stored.ScalarRenderer{ACLOutput: regobj.ACLItemOutput{Owner: auth.Owner, Roles: auth.Roles}, Owner: parent, Dependencies: &dependencies}).SQL(op.Context(), auth.Catalog, definition); err != nil {
		return nil, err
	}
	// Match the analyzed catalog shape, not the execution tree after folding.
	// PostgreSQL removes only a standalone nonnull Boolean TRUE predicate.
	if literal, ok := definition.(plan.LiteralExpr); ok && literal.Usage.Type.ID() == sqltype.Bool && !literal.Value.IsNull() {
		truth, err := plan.TruthOf(literal.Value)
		if err != nil {
			return nil, err
		}
		if truth == plan.True {
			return nil, nil
		}
	}
	return &catalog.IndexPredicate{Definition: definition, Execution: execution, Attributes: parent.Attributes, Dependencies: dependencies}, nil
}
