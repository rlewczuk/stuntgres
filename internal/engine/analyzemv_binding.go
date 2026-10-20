package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

type ParameterInfo struct {
	Count int
	Types []sqltype.Usage
}

// binding is the bound form of one statement. The header holds what every
// statement kind may use; kind payloads and capture groups are held by value
// (see binding_groups.go), so copying a binding copies them too.
type binding struct {
	Relation           *catalog.Relation
	Storage            *storage.Relation
	Ref                ids.ObjectRef
	ViewWrite          viewWriteSlot
	Targets            []int
	ArrayTargets       []*plan.ArrayAssignmentTarget
	UsedColumns        []int
	Query              *plan.Query
	CTEs               []*plan.CTEDefinition
	CTESelectBodies    bool
	QuerySources       []exec.Leaf
	VisibilitySnapshot bool
	SubqueryDemand     bool
	CommandQuery       bool
	CurrentOf          string // UPDATE and DELETE WHERE CURRENT OF.
	Predicate          expr.Expr
	Returning          *boundReturning
	Dependencies       []catalog.ExpressionDependency
	Parameters         ParameterInfo
	Expressions        [][]expr.Expr
	// Statement kind payloads.
	Update *boundUpdate
	Merge  *boundMerge
	Insert insertBinding
	Delete deleteBinding
	Create createTableBinding
	// Capture groups shared with the expression binder.
	Capture   viewCapture
	Prepared  preparedCaptures
	Resolvers resolverInputs
	Rule      ruleBinding
}

func (*binding) CTEMutationPayload() {}
