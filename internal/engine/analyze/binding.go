package analyze

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

type ParameterInfo struct {
	Count int
	Types []sqltype.Usage
}

// Binding is the bound form of one statement. The header holds what every
// statement kind may use; kind payloads and capture groups are held by value
// (see binding_groups.go), so copying a binding copies them too.
type Binding struct {
	// PLCallOutputs belongs only to a reached lexical CALL's private binding.
	// It is not part of the executable input expression graph.
	PLCallOutputs      []PLCallOutput
	Relation           *catalog.Relation
	Storage            *storage.Relation
	Ref                ids.ObjectRef
	ViewWrite          ViewWriteSlot
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
	Returning          *Returning
	Dependencies       []catalog.ExpressionDependency
	Parameters         ParameterInfo
	Expressions        [][]expr.Expr
	// Statement kind payloads.
	Update *UpdateBinding
	Merge  *MergeBinding
	Insert InsertBinding
	Delete DeleteBinding
	Create CreateTableBinding
	// Capture groups shared with the expression binder.
	Capture   ViewCapture
	Prepared  PreparedCaptures
	Resolvers ResolverInputs
	Rule      RuleBinding
}

func (*Binding) CTEMutationPayload() {}

type PLCallOutput struct {
	Target proc.PLTarget // Slot -1 means the selected coerced actual is not a datum.
	Name   string
	Formal int
}
