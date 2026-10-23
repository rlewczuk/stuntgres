package plan

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// PLAssignmentPlanningError preserves the phase that produced an assignment
// diagnostic. Runtime conversion and containing-domain errors are not planning.
type PLAssignmentPlanningError struct{ Err error }

func (e *PLAssignmentPlanningError) Error() string { return e.Err.Error() }
func (e *PLAssignmentPlanningError) Unwrap() error { return e.Err }

// PLAssignmentExpr marks only the reached assignment's projection, so ordinary
// query planning can preserve its diagnostic origin without altering SQL casts.
// Neither this wrapper nor its invocation-bound children are stored providers.
type PLAssignmentExpr struct {
	expr.Info
	Child expr.Expr
}

func (n PLAssignmentExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	runtime, err := nodeRuntimeOf(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	return runtime.EvalPLAssignment(n)
}
func (n PLAssignmentExpr) Children() []expr.Expr { return []expr.Expr{n.Child} }
func (n PLAssignmentExpr) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != 1 || children[0] == nil {
		return nil, fmt.Errorf("invalid PL assignment operands")
	}
	n.Info, n.Child = info, children[0]
	return n, nil
}
func (PLAssignmentExpr) PlanningFold() expr.Fold { return expr.FoldNever }
func (PLAssignmentExpr) ValidateStoredProvider() error {
	return fmt.Errorf("invocation-only PL assignment expression")
}

// PLIOCoercionExpr is selected only after assignment conversion has no path.
// It reuses codec, sizing and volatility ownership from DatumCastExpr, while
// allowing ordinary constant planning for PL's implicit textual conversion.
type PLIOCoercionExpr struct{ DatumCastExpr }

func (PLIOCoercionExpr) PlanningFold() expr.Fold { return expr.FoldConstant }
func (n PLIOCoercionExpr) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	child, err := n.DatumCastExpr.Rebuild(info, children)
	if err != nil {
		return nil, err
	}
	return PLIOCoercionExpr{DatumCastExpr: child.(DatumCastExpr)}, nil
}
func (PLIOCoercionExpr) ValidateStoredProvider() error {
	return fmt.Errorf("invocation-only PL IO conversion")
}
