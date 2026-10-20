package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

type boundUpdate struct {
	Assignments    []expr.Expr
	DefaultOrigins []defaultOrigin
	ArrayTargets   []*plan.ArrayAssignmentTarget
	// Group query graphs are owned by their first assignment recipe.
	Groups      []updateAssignmentGroup
	Origins     []parser.Position // Flat origins, including DEFAULT after group expansion.
	OutputWidth int
	Predicate   expr.Expr
	From        plan.Node
	SourceWidth int
}

// The first recipe owns the source query through plan.SubqueryExpr. Remaining
// outputs are lexical leaves, so all existing query graph transformations visit
// a group source exactly once, without adding a parallel graph ownership path.
type updateAssignmentGroup struct {
	Start, Width int
}

type boundMerge struct {
	Source        plan.Node
	On            expr.Expr
	Arms          []boundMergeArm
	HasInsert     bool
	HasDelete     bool
	HasNothing    bool
	HasBySource   bool
	HasByTarget   bool
	TargetWidth   int
	SourceWidth   int
	InsertTargets []int
	UpdateTargets []int
}

type boundMergeArm struct {
	Match     parser.MergeMatch
	Action    parser.MergeAction
	Condition expr.Expr
	Input     binding
	Syntax    []parser.Expr
}

type boundReturning struct {
	Expressions        []expr.Expr
	Schema             []catalog.Column
	RegclassOutput     bool
	Reads              []int
	ReadRequired       bool // Whole-row references retain SELECT demand with zero members.
	VisibilitySnapshot bool
}
