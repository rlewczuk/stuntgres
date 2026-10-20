package analyze

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

type UpdateBinding struct {
	Assignments    []expr.Expr
	DefaultOrigins []DefaultOrigin
	ArrayTargets   []*plan.ArrayAssignmentTarget
	// Group query graphs are owned by their first assignment recipe.
	Groups      []AssignmentGroup
	Origins     []parser.Position // Flat origins, including DEFAULT after group expansion.
	OutputWidth int
	Predicate   expr.Expr
	From        plan.Node
	SourceWidth int
}

// The first recipe owns the source query through plan.SubqueryExpr. Remaining
// outputs are lexical leaves, so all existing query graph transformations visit
// a group source exactly once, without adding a parallel graph ownership path.
type AssignmentGroup struct {
	Start, Width int
}

type MergeBinding struct {
	Source        plan.Node
	On            expr.Expr
	Arms          []MergeArm
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

type MergeArm struct {
	Match     parser.MergeMatch
	Action    parser.MergeAction
	Condition expr.Expr
	Input     Binding
	Syntax    []parser.Expr
}

type Returning struct {
	Expressions        []expr.Expr
	Schema             []catalog.Column
	RegclassOutput     bool
	Reads              []int
	ReadRequired       bool // Whole-row references retain SELECT demand with zero members.
	VisibilitySnapshot bool
}
