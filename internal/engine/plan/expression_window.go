package plan

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Window occurrences reference immutable appended stage slots, never aggregate state.
type WindowExpr struct {
	expr.Info
	Slot     int
	Frame    uint32
	Call     int
	Nullable bool
}

func (e *WindowExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	runtime, err := nodeRuntimeOf(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	return runtime.EvalWindow(e)
}

type WindowSpec struct {
	Partition  []expr.Expr
	Grouping   []Grouping
	Order      []SortKey
	OrderMap   []int // Raw-to-effective mapping, for literal capture only.
	Frame      WindowFrame
	Explicit   bool
	Definition int // Query-local named ordinal+1; zero retains inline spec identity.
}

type WindowOperation uint8

const (
	WindowCount WindowOperation = iota
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

func (operation WindowOperation) FrameDependent() bool {
	return operation != WindowNumber && operation != WindowNtile && operation != WindowLead && operation != WindowLag && !operation.Ranking()
}

func (operation WindowOperation) EvaluatesArgument() bool {
	return operation == WindowFirst || operation == WindowLast || operation == WindowNth || operation == WindowNtile || operation == WindowLead || operation == WindowLag
}

func (operation WindowOperation) Ranking() bool {
	return operation >= WindowRank && operation <= WindowCumeDist
}

type WindowCall struct {
	Expression   *WindowExpr
	Spec         WindowSpec
	Operation    WindowOperation
	Routine      ids.ObjectRef      // Selected callable, independently of peer/partition equality.
	Procedure    *catalog.Procedure // Original checked declaration, not a refreshed revision.
	Argument     expr.Expr
	Ordinal      expr.Expr
	DefaultValue expr.Expr
	Provider     functions.AggregateProvider
	Raw          *parser.WindowSpec
	AnalysisOnly bool // Retained binding/slot ownership without executable demand.
}

type WindowBound struct {
	Kind    parser.FrameBoundKind
	Offset  expr.Expr
	Support sqltype.RangeSupport
}

type WindowFrame struct {
	Unit       parser.FrameUnit
	Exclusion  parser.FrameExclusion
	Start, End WindowBound
}
