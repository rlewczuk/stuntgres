package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Window occurrences reference immutable appended stage slots, never aggregate state.
type windowExpression struct {
	expr.Info
	slot     int
	frame    uint32
	call     int
	nullable bool
}

func (e *windowExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	runtime, err := nodeRuntimeOf(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	return runtime.EvalWindow(e)
}

type boundWindowSpec struct {
	partition  []expr.Expr
	grouping   []queryGrouping
	order      []sortKey
	orderMap   []int // Raw-to-effective mapping, for literal capture only.
	frame      boundWindowFrame
	explicit   bool
	definition int // Query-local named ordinal+1; zero retains inline spec identity.
}

type windowOperation uint8

const (
	windowCount windowOperation = iota
	windowNumber
	windowSum
	windowFirst
	windowLast
	windowNth
	windowNtile
	windowLead
	windowLag
	windowRank
	windowDenseRank
	windowPercentRank
	windowCumeDist
)

func (operation windowOperation) frameDependent() bool {
	return operation != windowNumber && operation != windowNtile && operation != windowLead && operation != windowLag && !operation.ranking()
}

func (operation windowOperation) evaluatesArgument() bool {
	return operation == windowFirst || operation == windowLast || operation == windowNth || operation == windowNtile || operation == windowLead || operation == windowLag
}

func (operation windowOperation) ranking() bool {
	return operation >= windowRank && operation <= windowCumeDist
}

type windowCall struct {
	expression   *windowExpression
	spec         boundWindowSpec
	operation    windowOperation
	routine      ids.ObjectRef // Selected callable, independently of peer/partition equality.
	argument     expr.Expr
	ordinal      expr.Expr
	defaultValue expr.Expr
	provider     functions.AggregateProvider
	raw          *parser.WindowSpec
	analysisOnly bool // Retained binding/slot ownership without executable demand.
}

type boundWindowBound struct {
	kind    parser.FrameBoundKind
	offset  expr.Expr
	support sqltype.RangeSupport
}

type boundWindowFrame struct {
	unit       parser.FrameUnit
	exclusion  parser.FrameExclusion
	start, end boundWindowBound
}
