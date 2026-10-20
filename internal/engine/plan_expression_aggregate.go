package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type aggregateCall struct {
	Provider     functions.AggregateProvider
	Routine      ids.ObjectRef // Selected owning declaration; fresh caller ACL at admission.
	Argument     expr.Expr
	Separator    expr.Expr
	Direct       []expr.Expr
	Order        []sortKey
	Filter       expr.Expr
	Distinct     []queryGrouping // Multi-key tuple equality is caller checked; single-key support is not.
	StageWidth   int             // Value, separator, then unique local targets; keys address cache slots.
	Operation    functions.AggregateOperation
	IncludeNulls bool // Non-strict providers must see SQL NULL, including ordered replay.
	Procedure    *catalog.Procedure
	Ref          ids.ObjectRef
}

// Every analyzed aggregate expression participates in ownership, dependence,
// preflight and immutable query rewrites, not just the first value argument.
func (call aggregateCall) Expressions(visit func(expr.Expr)) {
	visit(call.Argument)
	visit(call.Separator)
	for _, direct := range call.Direct {
		visit(direct)
	}
	for _, key := range call.Order {
		visit(key.Expression)
	}
	visit(call.Filter)
}

type aggregateExpression struct {
	expr.Info
	Slot  int
	Frame uint32
}

func (e aggregateExpression) Eval(expr.Context) (sqltype.Value, error) {
	return sqltype.Value{}, fmt.Errorf("stuntgres: aggregate occurrence not rewritten")
}

// Group slots have no table address. Proven output origin is retained separately
// by bindSelect, never inferred from this private row layout.
type groupExpression struct {
	expr.Info
	Slot  int
	Frame uint32
}

func (e groupExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	runtime, err := nodeRuntimeOf(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	return runtime.EvalGroup(e)
}

// Arguments remain analyzed dependencies but are never evaluated by GROUPING.
// The immutable masks address the execution-local occurrence suffix in its owner.
type groupingExpression struct {
	expr.Info
	Args      []expr.Expr
	Positions []parser.Position
	Frame     uint32
	Slot      int
	Masks     []int32
}

func (e groupingExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	runtime, err := nodeRuntimeOf(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	return runtime.EvalGrouping(e)
}
