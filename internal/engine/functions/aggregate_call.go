package functions

import (
	"context"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// AggregateProvider is an ordinary aggregate's state algorithm. Providers are
// immutable; states belong exclusively to one execution's group.
type AggregateProvider interface {
	ResultUsage() sqltype.Usage
	Init(context.Context) (any, error)
	Advance(context.Context, any, sqltype.Value, sqltype.Value) error
	Finalize(context.Context, any) (sqltype.Value, error)
}

// ContextualAggregateProvider is implemented by providers whose output is
// environment-sensitive; they opt into execution-local initialization.
// Ordinary providers retain the AggregateProvider Init contract.
type ContextualAggregateProvider interface {
	InitWithEnvironment(context.Context, sqltype.OutputEnvironment) (any, error)
}

// AggregateOperation identifies the selected aggregate algorithm. The engine
// compares it, with the provider's result usage, when it merges equal calls.
type AggregateOperation uint8

const (
	AggregateCount AggregateOperation = iota
	AggregateAnd
	AggregateOr
	AggregateIntervalAvg
	AggregateNumericAvg
	AggregateFloatAvg
	AggregateAnyValue
	AggregateString
	AggregateMin
	AggregateMax
	AggregateIntegralSum
	AggregateNumericSum
	AggregateFloatSum
	AggregateMoneySum
	AggregateIntervalSum
	AggregateArrayScalar
	AggregateArrayArray
	AggregateRangeUnion
	AggregateRangeIntersection
	AggregateBitAnd
	AggregateBitOr
	AggregateBitXor
	AggregateJSON
	AggregateJSONStrict
	AggregateJSONArray
	AggregateJSONArrayAbsent
	AggregateJSONObject
	AggregateJSONObjectStrict
	AggregatePercentileCont
	AggregatePercentileDisc
	AggregateMode
	AggregateVarPop
	AggregateVarSamp
	AggregateStddevPop
	AggregateStddevSamp
	AggregateRegrSlope
	AggregateRegrSxx
	AggregateRegrSxy
	AggregateRegrSyy
	AggregateCorr
	AggregateCovarPop
	AggregateCovarSamp
	AggregateRegrAvgx
	AggregateRegrAvgy
	AggregateRegrIntercept
	AggregateRegrR2
	AggregateRegrCount
	AggregateHypotheticalRank
	AggregateHypotheticalDenseRank
	AggregateHypotheticalPercentRank
	AggregateHypotheticalCumeDist
)

// AggregateCall is the function-owned part of an analyzed aggregate call:
// the selected provider and routine and the coerced inputs. The engine adds
// FILTER, ORDER BY, DISTINCT and grouping state in Binder.FinishAggregate.
type AggregateCall struct {
	Provider  AggregateProvider
	Operation AggregateOperation
	// Routine is the selected owning declaration; the caller's ACL is checked
	// fresh at admission.
	Routine   ids.ObjectRef
	Argument  expr.Expr
	Separator expr.Expr
	Direct    []expr.Expr
	// StageWidth counts the value, separator and unique local targets; zero
	// lets FinishAggregate size it for ORDER BY.
	StageWidth int
	// IncludeNulls makes the provider see SQL NULL inputs (non-strict
	// providers), including in ordered replay.
	IncludeNulls bool
	// RetainUntypedArgument keeps the finished argument pending when it is a
	// parameter whose type is still unresolved (an ANY formal), after ORDER BY
	// matching had its chance to resolve it.
	RetainUntypedArgument bool
}
