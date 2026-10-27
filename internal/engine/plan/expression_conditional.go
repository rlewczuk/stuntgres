package plan

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type CaseBranch struct {
	Condition, Result expr.Expr
	// Simple CASE compares a coerced view of its one saved test value.
	Test        expr.Expr
	Equality    sqltype.Comparison
	TestUsage   sqltype.Usage
	TestContext string             // Original declared context; CASE itself always uses implicit coercion.
	Routine     *catalog.Procedure // Selected equality declaration, never captured permission.
	RoutineRef  ids.ObjectRef      // Exact installed declaration reference, mapped before retagging.
}

type CaseExpr struct {
	expr.Info
	Test      expr.Expr
	Arms      []CaseBranch
	Otherwise expr.Expr
}

func (e CaseExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := BeginEvaluation(ctx); err != nil {
		return sqltype.Value{}, err
	}
	if e.Usage.Type == nil || !e.Usage.Type.Concrete() || e.Otherwise == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid CASE expression")
	}
	var test sqltype.Value
	var err error
	if e.Test != nil {
		test, err = e.Test.Eval(ctx)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	for _, arm := range e.Arms {
		var view sqltype.Value
		if e.Test != nil {
			view, err = EvalCaseTest(ctx, arm.Test, test)
			if err != nil {
				return sqltype.Value{}, err
			}
		}
		condition, err := arm.Condition.Eval(ctx)
		if err != nil {
			return sqltype.Value{}, err
		}
		match := false
		if e.Test != nil {
			if !view.IsNull() && !condition.IsNull() {
				equal, err := arm.Equality.EqualValues(ctx.FunctionContext(), view, condition, ctx.FunctionInput())
				if err != nil {
					return sqltype.Value{}, err
				}
				match = equal
			}
		} else if !condition.IsNull() {
			match, err = condition.Boolean()
			if err != nil {
				return sqltype.Value{}, err
			}
		}
		if match {
			return arm.Result.Eval(ctx)
		}
	}
	return e.Otherwise.Eval(ctx)
}

type ConditionalExpr struct {
	expr.Info
	RoutineRef      ids.ObjectRef
	Kind            parser.ConditionalKind
	MissingOrdering bool
	Args            []expr.Expr
	Comparison      sqltype.Comparison
}

func (e ConditionalExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := BeginEvaluation(ctx); err != nil {
		return sqltype.Value{}, err
	}
	catalogNull := e.Kind == parser.Coalesce && e.Usage.CatalogNullUsage()
	if e.Usage.Type == nil || !e.Usage.Type.Concrete() && !catalogNull || len(e.Args) == 0 || e.Kind > parser.Least || e.Kind == parser.NullIf && len(e.Args) != 2 {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid conditional expression")
	}
	if err := e.ExtremaError(); err != nil {
		return sqltype.Value{}, err
	}
	if e.Kind == parser.NullIf {
		left, err := e.Args[0].Eval(ctx)
		if err != nil {
			return sqltype.Value{}, err
		}
		right, err := e.Args[1].Eval(ctx)
		if err != nil {
			return sqltype.Value{}, err
		}
		if left.Type() != e.Usage.Type || right.Type() != e.Args[1].ResultUsage().Type {
			return sqltype.Value{}, fmt.Errorf("stuntgres: invalid NULLIF operand value")
		}
		if !left.IsNull() && !right.IsNull() {
			equal, err := e.Comparison.EqualValues(ctx.FunctionContext(), left, right, ctx.FunctionInput())
			if err != nil {
				return sqltype.Value{}, err
			}
			if equal {
				return sqltype.Null(e.Usage.Type), nil
			}
		}
		return left, nil
	}
	best := sqltype.ResultNull(e.Usage)
	for _, arg := range e.Args {
		value, err := arg.Eval(ctx)
		if err != nil {
			return sqltype.Value{}, err
		}
		if value.Type() != e.Usage.Type || catalogNull && !value.ValidResult(e.Usage) {
			return sqltype.Value{}, fmt.Errorf("stuntgres: invalid conditional argument value")
		}
		if value.IsNull() {
			continue
		}
		if e.Kind == parser.Coalesce {
			return value, nil
		}
		if best.IsNull() {
			best = value
			continue
		}
		sign, err := e.Comparison.Compare(ctx.FunctionContext(), value, best, ctx.FunctionInput())
		if err != nil {
			return sqltype.Value{}, err
		}
		if e.Kind == parser.Greatest && sign > 0 || e.Kind == parser.Least && sign < 0 {
			best = value
		}
	}
	return best, nil
}

// Missing ordering is an executable initialization failure, not an equality
// provider or an analysis error: prepared result descriptions remain available.
func (e ConditionalExpr) ExtremaError() error {
	if !e.MissingOrdering {
		return nil
	}
	if e.Kind != parser.Greatest && e.Kind != parser.Least {
		return fmt.Errorf("stuntgres: invalid extrema capability")
	}
	return sqlError("42883", "could not identify a comparison function for type "+e.Usage.Type.DiagnosticName())
}

// A physical non-NULL BOOL left operand decides AND/OR without its right
// operand. Coercion preflight and planning demand share this literal policy.
func DecisiveLogicalValue(e *LogicalExpr) (sqltype.Value, bool, error) {
	literal, ok := e.Left.(LiteralExpr)
	if !ok || literal.Value.Type() == nil || !literal.Value.Type().Concrete() ||
		literal.Value.Type().ID() != sqltype.Bool || literal.Value.IsNull() {
		return sqltype.Value{}, false, nil
	}
	truth, err := TruthOf(literal.Value)
	if err != nil {
		return sqltype.Value{}, false, err
	}
	if (e.Op == parser.LogicalOr && truth == True) || (e.Op == parser.LogicalAnd && truth == False) {
		return literal.Value, true, nil
	}
	return sqltype.Value{}, false, nil
}

type LogicalExpr struct {
	expr.Info
	Op          parser.LogicalOperator
	Left, Right expr.Expr
	// A newly allocated child frame identifies a query-containing right operand.
	// This does not enable ordinary native Boolean/cast branch optimization.
	QueryRight bool
}

func (e LogicalExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := BeginEvaluation(ctx); err != nil {
		return sqltype.Value{}, err
	}
	if e.Usage.Type == nil || e.Usage.Type.ID() != sqltype.Bool || e.Left == nil ||
		(e.Op == parser.LogicalNot) != (e.Right == nil) || e.Op > parser.LogicalNot {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid logical expression")
	}
	if e.QueryRight {
		// Match planning demand for a query-owned decisive left literal.
		// Both children retain analysis, capture and ordinary coercion preflight.
		_, decisive, err := DecisiveLogicalValue(&e)
		if err != nil {
			return sqltype.Value{}, err
		}
		if decisive {
			return sqltype.Boolean(e.Usage.Type, e.Op == parser.LogicalOr), nil
		}
	}
	v, err := e.Left.Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	a, err := TruthOf(v)
	if err != nil {
		return sqltype.Value{}, err
	}
	if e.Op == parser.LogicalNot {
		if a == Unknown {
			return sqltype.Null(e.Usage.Type), nil
		}
		return sqltype.Boolean(e.Usage.Type, a == False), nil
	}
	// Evaluation order for nondecisive operands remains private.
	v, err = e.Right.Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	b, err := TruthOf(v)
	if err != nil {
		return sqltype.Value{}, err
	}
	if e.Op == parser.LogicalAnd {
		if a == False || b == False {
			return sqltype.Boolean(e.Usage.Type, false), nil
		}
	} else if a == True || b == True {
		return sqltype.Boolean(e.Usage.Type, true), nil
	}
	if a == Unknown || b == Unknown {
		return sqltype.Null(e.Usage.Type), nil
	}
	return sqltype.Boolean(e.Usage.Type, e.Op == parser.LogicalAnd), nil
}
