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
	Slot     int
	Frame    uint32
	Call     int
	Nullable bool
}

func (e *windowExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	runtime, err := nodeRuntimeOf(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	return runtime.EvalWindow(e)
}

type boundWindowSpec struct {
	Partition  []expr.Expr
	Grouping   []queryGrouping
	Order      []sortKey
	OrderMap   []int // Raw-to-effective mapping, for literal capture only.
	Frame      boundWindowFrame
	Explicit   bool
	Definition int // Query-local named ordinal+1; zero retains inline spec identity.
}

type windowOperation uint8

const (
	WindowCount windowOperation = iota
	WindowNumber
	WindowSum
	WindowFirst
	WindowLast
	WindowNth
	WindowNtile
	WindowLead
	WindowLag
	WindowRank
	WindowDenseRank
	WindowPercentRank
	WindowCumeDist
)

func (operation windowOperation) FrameDependent() bool {
	return operation != WindowNumber && operation != WindowNtile && operation != WindowLead && operation != WindowLag && !operation.Ranking()
}

func (operation windowOperation) EvaluatesArgument() bool {
	return operation == WindowFirst || operation == WindowLast || operation == WindowNth || operation == WindowNtile || operation == WindowLead || operation == WindowLag
}

func (operation windowOperation) Ranking() bool {
	return operation >= WindowRank && operation <= WindowCumeDist
}

type windowCall struct {
	Expression   *windowExpression
	Spec         boundWindowSpec
	Operation    windowOperation
	Routine      ids.ObjectRef // Selected callable, independently of peer/partition equality.
	Argument     expr.Expr
	Ordinal      expr.Expr
	DefaultValue expr.Expr
	Provider     functions.AggregateProvider
	Raw          *parser.WindowSpec
	AnalysisOnly bool // Retained binding/slot ownership without executable demand.
}

type boundWindowBound struct {
	Kind    parser.FrameBoundKind
	Offset  expr.Expr
	Support sqltype.RangeSupport
}

type boundWindowFrame struct {
	Unit       parser.FrameUnit
	Exclusion  parser.FrameExclusion
	Start, End boundWindowBound
}
