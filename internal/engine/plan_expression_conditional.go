package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type caseBranch struct {
	condition, result expr.Expr
	// Simple CASE compares a coerced view of its one saved test value.
	testPlan  sqltype.CoercionPlan
	equality  sqltype.Comparison
	testUsage sqltype.Usage
	routine   *catalog.Procedure // Selected equality declaration, never captured permission.
}

type caseExpression struct {
	expr.Info
	test      expr.Expr
	arms      []caseBranch
	otherwise expr.Expr
}

func (e caseExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := beginEvaluation(ctx); err != nil {
		return sqltype.Value{}, err
	}
	if e.Usage.Type == nil || !e.Usage.Type.Concrete() || e.otherwise == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid CASE expression")
	}
	var test sqltype.Value
	var err error
	if e.test != nil {
		test, err = e.test.Eval(ctx)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	for _, arm := range e.arms {
		condition, err := arm.condition.Eval(ctx)
		if err != nil {
			return sqltype.Value{}, err
		}
		match := false
		if e.test != nil {
			view, err := arm.testPlan.Apply(ctx.FunctionContext(), test, ctx.FunctionInput(), ctx.FunctionOutput())
			if err != nil {
				return sqltype.Value{}, err
			}
			if !view.IsNull() && !condition.IsNull() {
				equal, err := arm.equality.EqualValues(ctx.FunctionContext(), view, condition, ctx.FunctionInput())
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
			return arm.result.Eval(ctx)
		}
	}
	return e.otherwise.Eval(ctx)
}

type conditionalExpression struct {
	expr.Info
	kind            parser.ConditionalKind
	missingOrdering bool
	args            []expr.Expr
	comparison      sqltype.Comparison
}

func (e conditionalExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := beginEvaluation(ctx); err != nil {
		return sqltype.Value{}, err
	}
	if e.Usage.Type == nil || !e.Usage.Type.Concrete() || len(e.args) == 0 || e.kind > parser.Least || e.kind == parser.NullIf && len(e.args) != 2 {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid conditional expression")
	}
	if err := e.extremaError(); err != nil {
		return sqltype.Value{}, err
	}
	if e.kind == parser.NullIf {
		left, err := e.args[0].Eval(ctx)
		if err != nil {
			return sqltype.Value{}, err
		}
		right, err := e.args[1].Eval(ctx)
		if err != nil {
			return sqltype.Value{}, err
		}
		if left.Type() != e.Usage.Type || right.Type() != e.args[1].ResultUsage().Type {
			return sqltype.Value{}, fmt.Errorf("stuntgres: invalid NULLIF operand value")
		}
		if !left.IsNull() && !right.IsNull() {
			equal, err := e.comparison.EqualValues(ctx.FunctionContext(), left, right, ctx.FunctionInput())
			if err != nil {
				return sqltype.Value{}, err
			}
			if equal {
				return sqltype.Null(e.Usage.Type), nil
			}
		}
		return left, nil
	}
	best := sqltype.Null(e.Usage.Type)
	for _, arg := range e.args {
		value, err := arg.Eval(ctx)
		if err != nil {
			return sqltype.Value{}, err
		}
		if value.Type() != e.Usage.Type {
			return sqltype.Value{}, fmt.Errorf("stuntgres: invalid conditional argument value")
		}
		if value.IsNull() {
			continue
		}
		if e.kind == parser.Coalesce {
			return value, nil
		}
		if best.IsNull() {
			best = value
			continue
		}
		sign, err := e.comparison.Compare(ctx.FunctionContext(), value, best, ctx.FunctionInput())
		if err != nil {
			return sqltype.Value{}, err
		}
		if e.kind == parser.Greatest && sign > 0 || e.kind == parser.Least && sign < 0 {
			best = value
		}
	}
	return best, nil
}

// Missing ordering is an executable initialization failure, not an equality
// provider or an analysis error: prepared result descriptions remain available.
func (e conditionalExpression) extremaError() error {
	if !e.missingOrdering {
		return nil
	}
	if e.kind != parser.Greatest && e.kind != parser.Least {
		return fmt.Errorf("stuntgres: invalid extrema capability")
	}
	return sqlError("42883", "could not identify a comparison function for type "+e.Usage.Type.DiagnosticName())
}

// A physical non-NULL BOOL left operand decides AND/OR without its right
// operand. Coercion preflight and planning demand share this literal policy.
func decisiveLogicalValue(e *logicalExpression) (sqltype.Value, bool, error) {
	literal, ok := e.left.(literalExpression)
	if !ok || literal.value.Type() == nil || !literal.value.Type().Concrete() ||
		literal.value.Type().ID() != sqltype.Bool || literal.value.IsNull() {
		return sqltype.Value{}, false, nil
	}
	truth, err := TruthOf(literal.value)
	if err != nil {
		return sqltype.Value{}, false, err
	}
	if (e.op == parser.LogicalOr && truth == True) || (e.op == parser.LogicalAnd && truth == False) {
		return literal.value, true, nil
	}
	return sqltype.Value{}, false, nil
}

type logicalExpression struct {
	expr.Info
	op          parser.LogicalOperator
	left, right expr.Expr
	// A newly allocated child frame identifies a query-containing right operand.
	// This does not enable ordinary native Boolean/cast branch optimization.
	queryRight bool
}

func (e logicalExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := beginEvaluation(ctx); err != nil {
		return sqltype.Value{}, err
	}
	if e.Usage.Type == nil || e.Usage.Type.ID() != sqltype.Bool || e.left == nil ||
		(e.op == parser.LogicalNot) != (e.right == nil) || e.op > parser.LogicalNot {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid logical expression")
	}
	if e.queryRight {
		// Match planning demand for a query-owned decisive left literal.
		// Both children retain analysis, capture and ordinary coercion preflight.
		_, decisive, err := decisiveLogicalValue(&e)
		if err != nil {
			return sqltype.Value{}, err
		}
		if decisive {
			return sqltype.Boolean(e.Usage.Type, e.op == parser.LogicalOr), nil
		}
	}
	v, err := e.left.Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	a, err := TruthOf(v)
	if err != nil {
		return sqltype.Value{}, err
	}
	if e.op == parser.LogicalNot {
		if a == Unknown {
			return sqltype.Null(e.Usage.Type), nil
		}
		return sqltype.Boolean(e.Usage.Type, a == False), nil
	}
	// Evaluation order for nondecisive operands remains private.
	v, err = e.right.Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	b, err := TruthOf(v)
	if err != nil {
		return sqltype.Value{}, err
	}
	if e.op == parser.LogicalAnd {
		if a == False || b == False {
			return sqltype.Boolean(e.Usage.Type, false), nil
		}
	} else if a == True || b == True {
		return sqltype.Boolean(e.Usage.Type, true), nil
	}
	if a == Unknown || b == Unknown {
		return sqltype.Null(e.Usage.Type), nil
	}
	return sqltype.Boolean(e.Usage.Type, e.op == parser.LogicalAnd), nil
}
