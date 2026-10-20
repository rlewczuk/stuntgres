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
	Kind            parser.SubqueryKind
	QueryPlan       *BoundQuery
	Comparisons     []comparisonExpression
	Ordering        []sqltype.Comparison
	Membership      []sqltype.Grouping // Nonempty only for uncorrelated hashable =ANY/IN.
	Operator        string
	Negate          bool
	PresenceOnly    bool
	cteDefinition   bool // Analysis-only graph edge, never expression demand.
	JSONArray       *sqlJSONArrayQuery
	LeftExpansions  map[int]compositeExpansionBinding // Analysis-only source associations.
	LeftConstructor expr.Expr                         // Analysis-only normalized constructor; never evaluated or a dependency child.
	ErasedLeftType  sqltype.ID                        // Original outer identity, only for pruning frozen presentation dependencies.
	UpdateWidth     int                               // Nonzero only for the first recipe of an UPDATE tuple source.
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
	Procedure *catalog.Procedure
	Ref       ids.ObjectRef
	Args      []expr.Expr
}

func (n sqlRoutineExpression) Children() []expr.Expr { return n.Args }

func (n sqlRoutineExpression) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Args: n.Args, Count: len(n.Args), SelectedImplicit: true}, true, nil
}

func (n sqlRoutineExpression) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if current, ok := info.RoutineReference.(*catalog.Procedure); ok {
		n.Procedure = current
	}
	if n.Procedure == nil || n.Procedure.SQL == nil || len(children) != len(n.Procedure.SQL.Inputs) {
		return nil, fmt.Errorf("invalid SQL routine provider")
	}
	for i, child := range children {
		if child == nil || child.ResultUsage().Type == nil || child.ResultUsage().Type.ID() != n.Procedure.SQL.Inputs[i].Type.ID() {
			return nil, fmt.Errorf("invalid SQL routine argument")
		}
	}
	n.Info, n.Args = info, children
	return n, nil
}

func (n sqlRoutineExpression) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(sqlRoutineExpression)
	if !ok || n.Ref != o.Ref || n.Procedure.SQL != o.Procedure.SQL || len(n.Args) != len(o.Args) {
		return false
	}
	for i, arg := range n.Args {
		if !same(arg, o.Args[i]) {
			return false
		}
	}
	return true
}

func (n sqlRoutineExpression) ValidateStoredProvider() error {
	if n.Procedure == nil || n.Procedure.SQL == nil || n.Info.RoutineReference != n.Procedure || n.Ref == (ids.ObjectRef{}) || n.Ref.Address != n.Procedure.Address() || n.Usage.Type == nil || !n.Usage.Valid() || n.Usage.Type.ID() != sqltype.ID(n.Procedure.Result) {
		return fmt.Errorf("invalid stored SQL routine provider")
	}
	if _, err := n.Rebuild(n.Info, n.Args); err != nil {
		return err
	}
	want := expr.Properties{Volatility: expr.Volatile}
	for _, arg := range n.Args {
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
