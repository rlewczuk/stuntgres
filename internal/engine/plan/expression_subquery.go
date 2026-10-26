package plan

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
type SubqueryExpr struct {
	expr.Info
	Kind            parser.SubqueryKind
	QueryPlan       *Query
	Comparisons     []ComparisonExpr
	Ordering        []sqltype.Comparison
	Membership      []sqltype.Grouping // Nonempty only for uncorrelated hashable =ANY/IN.
	Operator        string
	Negate          bool
	PresenceOnly    bool
	CTEEdge         bool // Analysis-only graph edge, never expression demand.
	JSONArray       *SQLJSONArrayQuery
	LeftExpansions  map[int]CompositeExpansionBinding // Analysis-only source associations.
	LeftConstructor expr.Expr                         // Analysis-only normalized constructor; never evaluated or a dependency child.
	ErasedLeftType  sqltype.ID                        // Original outer identity, only for pruning frozen presentation dependencies.
	UpdateWidth     int                               // Nonzero only for the first recipe of an UPDATE tuple source.
}

func (e SubqueryExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	runtime, err := nodeRuntimeOf(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	return runtime.EvalSubquery(e)
}

type SQLRoutineExpr struct {
	expr.Info
	Procedure      *catalog.Procedure
	Ref            ids.ObjectRef
	Args           []expr.Expr
	WrittenCount   int
	ArgNames       []parser.Identifier
	Variadic       bool
	InputMap       []RoutineInput
	ChildSlots     []int // Validated inverse of InputMap, built once during binding.
	InputCollation sqltype.CollationRef
}

func (n SQLRoutineExpr) Children() []expr.Expr { return n.Args }

func (n SQLRoutineExpr) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	if n.WrittenCount < 0 || n.WrittenCount > len(n.Args) {
		return expr.CaptureOperands{}, false, fmt.Errorf("invalid SQL routine written projection")
	}
	return expr.CaptureOperands{Args: n.Args[:n.WrittenCount], Count: n.WrittenCount, SelectedImplicit: true}, true, nil
}

func (n SQLRoutineExpr) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if current, ok := info.RoutineReference.(*catalog.Procedure); ok {
		n.Procedure = current
	}
	if err := n.ValidateArguments(children); err != nil {
		return nil, err
	}
	n.Info, n.Args = info, children
	return n, nil
}

func (n SQLRoutineExpr) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(SQLRoutineExpr)
	if !ok || n.Ref != o.Ref || n.Procedure.SQL != o.Procedure.SQL || n.InputCollation != o.InputCollation || len(n.Args) != len(o.Args) || !n.SameArguments(o) {
		return false
	}
	for i, arg := range n.Args {
		if !same(arg, o.Args[i]) {
			return false
		}
	}
	return true
}

func (n SQLRoutineExpr) ValidateStoredProvider() error {
	if n.Procedure == nil || n.Procedure.SQL == nil || n.Info.RoutineReference != n.Procedure || n.Ref == (ids.ObjectRef{}) || n.Ref.Address != n.Procedure.Address() || n.Usage.Type == nil || !n.Usage.Valid() || n.Usage.Type.ID() != sqltype.ID(n.Procedure.Result) {
		return fmt.Errorf("invalid stored SQL routine provider")
	}
	if n.Usage.Type != n.Procedure.SQL.Result.Type {
		return fmt.Errorf("SQL routine call has a foreign result generation")
	}
	if n.InputCollation.ID != 0 && !n.InputCollation.Valid() {
		return fmt.Errorf("invalid SQL routine input collation")
	}
	if _, err := n.Rebuild(n.Info, n.Args); err != nil {
		return err
	}
	want := n.GroupProperties(n.Args)
	if n.Property != want {
		return fmt.Errorf("invalid stored SQL routine properties")
	}
	return nil
}

func (n SQLRoutineExpr) IntrinsicVolatility() expr.Volatility { return n.Procedure.Volatility }

// Group rewriting changes actual children, not a call's intrinsic volatility.
// A sibling aggregate does not make an independent scalar call stage-dependent.
func (n SQLRoutineExpr) GroupProperties(children []expr.Expr) expr.Properties {
	properties := expr.Properties{Volatility: n.Procedure.Volatility}
	for _, child := range children {
		properties = expr.MergeProperties(properties, child.Properties())
	}
	return properties
}

func (SQLRoutineExpr) PlanningFold() expr.Fold { return expr.FoldNever }

func (n SQLRoutineExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	runtime, err := nodeRuntimeOf(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	return runtime.EvalSQLRoutine(n)
}
