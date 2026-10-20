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
	provider     functions.AggregateProvider
	routine      ids.ObjectRef // Selected owning declaration; fresh caller ACL at admission.
	argument     expr.Expr
	separator    expr.Expr
	direct       []expr.Expr
	order        []sortKey
	filter       expr.Expr
	distinct     []queryGrouping // Multi-key tuple equality is caller checked; single-key support is not.
	stageWidth   int             // Value, separator, then unique local targets; keys address cache slots.
	operation    functions.AggregateOperation
	includeNulls bool // Non-strict providers must see SQL NULL, including ordered replay.
	procedure    *catalog.Procedure
	ref          ids.ObjectRef
}

// Every analyzed aggregate expression participates in ownership, dependence,
// preflight and immutable query rewrites, not just the first value argument.
func (call aggregateCall) expressions(visit func(expr.Expr)) {
	visit(call.argument)
	visit(call.separator)
	for _, direct := range call.direct {
		visit(direct)
	}
	for _, key := range call.order {
		visit(key.expression)
	}
	visit(call.filter)
}

type aggregateExpression struct {
	expr.Info
	slot  int
	frame uint32
}

func (e aggregateExpression) Eval(expr.Context) (sqltype.Value, error) {
	return sqltype.Value{}, fmt.Errorf("stuntgres: aggregate occurrence not rewritten")
}

// Group slots have no table address. Proven output origin is retained separately
// by bindSelect, never inferred from this private row layout.
type groupExpression struct {
	expr.Info
	slot  int
	frame uint32
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
	args      []expr.Expr
	positions []parser.Position
	frame     uint32
	slot      int
	masks     []int32
}

func (e groupingExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	runtime, err := nodeRuntimeOf(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	return runtime.EvalGrouping(e)
}
