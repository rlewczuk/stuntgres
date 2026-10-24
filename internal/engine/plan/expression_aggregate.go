package plan

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type AggregateCall struct {
	Provider      functions.AggregateProvider
	Routine       ids.ObjectRef               // Selected owning declaration; fresh caller ACL at admission.
	RoutineRecipe expr.RoutineRecipeReference // Original stored occurrence of this selected declaration.
	Arguments      []expr.Expr
	Direct         []expr.Expr
	Order          []SortKey
	Filter         expr.Expr
	Distinct       []Grouping // Multi-key tuple equality is caller checked; single-key support is not.
	StageWidth     int        // Ordinary arguments, then unique local ordering targets.
	Operation      functions.AggregateOperation
	IncludeNulls   bool // Non-strict providers must see SQL NULL, including ordered replay.
	Procedure      *catalog.Procedure
	Ref            ids.ObjectRef
}


func (call AggregateCall) CallRoutineRecipeReference() expr.RoutineRecipeReference {
	return call.RoutineRecipe
}

// Every analyzed aggregate expression participates in ownership, dependence,
// preflight and immutable query rewrites, not just the first value argument.
func (call AggregateCall) Expressions(visit func(expr.Expr)) {
	for _, argument := range call.Arguments {
		visit(argument)
	}
	for _, direct := range call.Direct {
		visit(direct)
	}
	for _, key := range call.Order {
		visit(key.Expression)
	}
	visit(call.Filter)
}

type AggregateExpr struct {
	expr.Info
	Slot  int
	Frame uint32
}

func (e AggregateExpr) Eval(expr.Context) (sqltype.Value, error) {
	return sqltype.Value{}, fmt.Errorf("stuntgres: aggregate occurrence not rewritten")
}

// Group slots have no table address. Proven output origin is retained separately
// by bindSelect, never inferred from this private row layout.
type GroupExpr struct {
	expr.Info
	Slot  int
	Frame uint32
}

func (e GroupExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	runtime, err := nodeRuntimeOf(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	return runtime.EvalGroup(e)
}

// Arguments remain analyzed dependencies but are never evaluated by GROUPING.
// The immutable masks address the execution-local occurrence suffix in its owner.
type GroupingExpr struct {
	expr.Info
	Args      []expr.Expr
	Positions []parser.Position
	Frame     uint32
	Slot      int
	Masks     []int32
}

func (e GroupingExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	runtime, err := nodeRuntimeOf(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	return runtime.EvalGrouping(e)
}
