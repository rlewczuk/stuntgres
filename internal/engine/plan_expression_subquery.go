package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Subquery expressions borrow captured sources and the existing execution owner.
// Correlated scans reopen; uncorrelated hashed occurrences retain a built RHS.
type subqueryExpression struct {
	expr.Info
	kind            parser.SubqueryKind
	queryPlan       *BoundQuery
	comparisons     []comparisonExpression
	ordering        []sqltype.Comparison
	membership      []sqltype.Grouping // Nonempty only for uncorrelated hashable =ANY/IN.
	operator        string
	negate          bool
	presenceOnly    bool
	cteDefinition   bool // Analysis-only graph edge, never expression demand.
	jsonArray       *sqlJSONArrayQuery
	leftExpansions  map[int]compositeExpansionBinding // Analysis-only source associations.
	leftConstructor expr.Expr                         // Analysis-only normalized constructor; never evaluated or a dependency child.
	erasedLeftType  sqltype.ID                        // Original outer identity, only for pruning frozen presentation dependencies.
	updateWidth     int                               // Nonzero only for the first recipe of an UPDATE tuple source.
}

func (e subqueryExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	runtime, err := nodeRuntimeOf(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	return runtime.EvalSubquery(e)
}

type sqlRoutineExpression struct {
	expr.Info
	procedure *catalog.Procedure
	ref       ids.ObjectRef
	args      []expr.Expr
}

func (n sqlRoutineExpression) Children() []expr.Expr { return n.args }

func (n sqlRoutineExpression) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Args: n.args, Count: len(n.args), SelectedImplicit: true}, true, nil
}

func (n sqlRoutineExpression) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if current, ok := info.RoutineReference.(*catalog.Procedure); ok {
		n.procedure = current
	}
	if n.procedure == nil || n.procedure.SQL == nil || len(children) != len(n.procedure.SQL.Inputs) {
		return nil, fmt.Errorf("invalid SQL routine provider")
	}
	for i, child := range children {
		if child == nil || child.ResultUsage().Type == nil || child.ResultUsage().Type.ID() != n.procedure.SQL.Inputs[i].Type.ID() {
			return nil, fmt.Errorf("invalid SQL routine argument")
		}
	}
	n.Info, n.args = info, children
	return n, nil
}

func (n sqlRoutineExpression) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(sqlRoutineExpression)
	if !ok || n.ref != o.ref || n.procedure.SQL != o.procedure.SQL || len(n.args) != len(o.args) {
		return false
	}
	for i, arg := range n.args {
		if !same(arg, o.args[i]) {
			return false
		}
	}
	return true
}

func (n sqlRoutineExpression) ValidateStoredProvider() error {
	if n.procedure == nil || n.procedure.SQL == nil || n.Info.RoutineReference != n.procedure || n.ref == (ids.ObjectRef{}) || n.ref.Address != n.procedure.Address() || n.Usage.Type == nil || !n.Usage.Valid() || n.Usage.Type.ID() != sqltype.ID(n.procedure.Result) {
		return fmt.Errorf("invalid stored SQL routine provider")
	}
	if _, err := n.Rebuild(n.Info, n.args); err != nil {
		return err
	}
	want := expr.Properties{Volatility: expr.Volatile}
	for _, arg := range n.args {
		want = expr.MergeProperties(want, arg.Properties())
	}
	if n.Property != want {
		return fmt.Errorf("invalid stored SQL routine properties")
	}
	return nil
}

func (sqlRoutineExpression) IntrinsicVolatility() expr.Volatility { return expr.Volatile }

func (sqlRoutineExpression) PlanningFold() expr.Fold { return expr.FoldNever }

func (n sqlRoutineExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	runtime, err := nodeRuntimeOf(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	return runtime.EvalSQLRoutine(n)
}
