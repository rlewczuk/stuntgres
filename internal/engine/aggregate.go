package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Providers are immutable; states belong exclusively to one execution's group.
// Later aggregate builtins can implement this contract without scalar shortcuts.
type aggregateProvider interface {
	ResultUsage() sqltype.Usage
	Init(context.Context) (any, error)
	Advance(context.Context, any, sqltype.Value, sqltype.Value) error
	Finalize(context.Context, any) (sqltype.Value, error)
}

// Only providers with environment-sensitive output opt into execution-local
// initialization. Ordinary providers retain their existing Init contract.
type contextualAggregateProvider interface {
	InitWithEnvironment(context.Context, sqltype.OutputEnvironment) (any, error)
}

type aggregateOperation uint8

const (
	aggregateCount aggregateOperation = iota
	aggregateAnd
	aggregateOr
	aggregateIntervalAvg
	aggregateNumericAvg
	aggregateFloatAvg
	aggregateAnyValue
	aggregateString
	aggregateMin
	aggregateMax
	aggregateIntegralSum
	aggregateNumericSum
	aggregateFloatSum
	aggregateMoneySum
	aggregateIntervalSum
	aggregateArrayScalar
	aggregateArrayArray
	aggregateRangeUnion
	aggregateRangeIntersection
	aggregateBitAnd
	aggregateBitOr
	aggregateBitXor
	aggregateJSON
	aggregateJSONStrict
	aggregateJSONArray
	aggregateJSONArrayAbsent
	aggregateJSONObject
	aggregateJSONObjectStrict
	aggregatePercentileCont
	aggregatePercentileDisc
	aggregateMode
	aggregateVarPop
	aggregateVarSamp
	aggregateStddevPop
	aggregateStddevSamp
	aggregateRegrSlope
	aggregateRegrSxx
	aggregateRegrSxy
	aggregateRegrSyy
	aggregateCorr
	aggregateCovarPop
	aggregateCovarSamp
	aggregateRegrAvgx
	aggregateRegrAvgy
	aggregateRegrIntercept
	aggregateRegrR2
	aggregateRegrCount
	aggregateHypotheticalRank
	aggregateHypotheticalDenseRank
	aggregateHypotheticalPercentRank
	aggregateHypotheticalCumeDist
)

type aggregateCall struct {
	provider     aggregateProvider
	routine      ids.ObjectRef // Selected owning declaration; fresh caller ACL at admission.
	argument     BoundExpr
	separator    BoundExpr
	direct       []BoundExpr
	order        []sortKey
	filter       BoundExpr
	distinct     []queryGrouping // Multi-key tuple equality is caller checked; single-key support is not.
	stageWidth   int             // Value, separator, then unique local targets; keys address cache slots.
	operation    aggregateOperation
	includeNulls bool // Non-strict providers must see SQL NULL, including ordered replay.
	procedure    *procedureDescriptor
	ref          ids.ObjectRef
}

// Every analyzed aggregate expression participates in ownership, dependence,
// preflight and immutable query rewrites, not just the first value argument.
func (call aggregateCall) expressions(visit func(BoundExpr)) {
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
	expressionInfo
	slot  int
	frame uint32
}

func (e aggregateExpression) Eval(ExprContext) (sqltype.Value, error) {
	return sqltype.Value{}, fmt.Errorf("stuntgres: aggregate occurrence not rewritten")
}

// Group slots have no table address. Proven output origin is retained separately
// by bindSelect, never inferred from this private row layout.
type groupExpression struct {
	expressionInfo
	slot  int
	frame uint32
}

func (e groupExpression) Eval(ctx ExprContext) (sqltype.Value, error) {
	c := evalContext(ctx)
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	frame := e.frame
	if frame == 0 {
		frame = 1
	}
	for c != nil && (c.Scope != 1 || c.Frame != frame) {
		c = c.Outer
	}
	if c == nil || e.slot < 0 || e.slot >= len(c.Row) || c.Row[e.slot].Type() != e.usage.Type {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid group row frame")
	}
	return c.Row[e.slot], nil
}
