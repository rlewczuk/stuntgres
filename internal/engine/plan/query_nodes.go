package plan

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
)

type AggregateNode struct {
	QuerySchema
	Child          Node
	Keys           []expr.Expr
	Grouping       []Grouping
	Calls          []AggregateCall
	Dependent      []expr.Expr // Representative input values, never grouping keys.
	Dependencies   []GroupDependency
	Sets           [][]int // Canonical key IDs; each occurrence has isolated state.
	GroupingActive bool
	PresenceOnly   bool // Retain analyzed grouping while discarding eligible EXISTS demand.
}

// GroupDependency authenticates one base occurrence's complete grouped primary
// key. Attached constraints have addresses, not standalone descriptors.
type GroupDependency struct {
	Parent     ids.ObjectRef
	Constraint ids.ObjectAddress
	Frame      uint32
	Occurrence int
	Attributes []int32
}

func (n *AggregateNode) DependentStart() int { return len(n.Keys) + len(n.Calls) + 1 }

func (n *AggregateNode) DiscardableGrouping() bool {
	if len(n.Calls) != 0 || n.GroupingActive {
		return false
	}
	if n.Sets == nil {
		return len(n.Keys) > 0
	}
	return len(n.Sets) == 1 && len(n.Sets[0]) > 0
}

// A derived source adapts projected child rows to the ordinary FROM frame.
// Its output is execution-private; it never owns captured relation pins.
type DerivedNode struct {
	QuerySchema
	Query         *Query
	CTE           *CTEDefinition
	Table         *parser.TableReference
	Ordinal       int
	ScanFrame     uint32
	Lateral       bool
	Captures      []FunctionCorrelation // Free reads in the caller's immutable frame.
	Demand        []bool
	PresenceSlot  int
	RangeName     string
	ColumnAliases []string
}

// DistinctNode owns source-typed projected tuples. Its parent projection reads
// slots, so ordering and INSERT assignment never reevaluate key expressions.
type DistinctNode struct {
	QuerySchema
	Child        Node
	Expressions  []expr.Expr
	Grouping     []Grouping
	PresenceOnly bool // Retain analyzed targets while discarding eligible EXISTS demand.
}

// Unlike DISTINCT, ON retains input occurrences and never demands payload
// equality. The existing cache and source own retained rows and key datums.
type DistinctOnNode struct {
	QuerySchema
	Child        Node
	Expressions  []expr.Expr
	Grouping     []Grouping
	PresenceOnly bool
}

type JoinNode struct {
	ViewOrdinal  int
	PresenceSlot int
	QuerySchema
	RangeName     string
	ColumnAliases []string // Immutable final accepted namespace labels.
	Kind          parser.JoinKind
	// Comma preserves the analyzed FROM-list versus explicit JoinExpr layout
	// for selected statement identity; it is not join execution policy.
	Comma                     bool
	Left, Right               Node
	Predicate                 expr.Expr
	LeftWidth, RightWidth     int
	Merges                    []expr.Expr
	CorrelatedRight           bool
	Frame                     uint32
	CorrelationPrefix         int
	CorrelationIdentityPrefix bool // Inherited occurrence/presence lineage, even with a zero-width row prefix.
	Captures                  []FunctionCorrelation
	// ScanOwner is the canonical join of an execution-private copy, so foreign
	// scans below the copy keep the canonical owner identity. Nil otherwise.
	ScanOwner *JoinNode
}

// Captures retain their binding namespace and written origin while an enclosing
// join consumes locally satisfied references. Slots are relative to that join's
// inherited correlation frame, not to its output row.
type FunctionCorrelation struct {
	Frame           uint32
	Slot            int
	QueryOccurrence int // Positive one-based system occurrence; zero absent.
	PresenceSlot    int // Positive one-based whole-row presence; zero absent.
	Name            string
	Pos             parser.Position
}

type LimitNode struct {
	QuerySchema
	Child         Node
	Offset, Count expr.Expr
	WithTies      bool
	PeerKeys      []SortKey
	PresenceOnly  bool // Eligible EXISTS retains analysis, not bounds/peer production.
}

// Evaluation keeps the input frame/provenance intact and shares the existing
// ORDER cache authority. Final projection reads these slots, never reevaluates.
type EvaluationNode struct {
	QuerySchema
	Child       Node
	Expressions []expr.Expr
	Base        int
}

// The self edge is an opaque identity, never a child pointing at its own query.
type WorktableScanNode struct{ DerivedNode }

type RecursiveUnionNode struct {
	SetNode
	Definition *CTEDefinition
	Search     *SearchRecipe
	Cycle      *CycleRecipe
}

// Ordinary groupings cover original columns and then the optional CYCLE mark;
// generated record/path slots retain their recipe-owned equality authority.
func (n *RecursiveUnionNode) GroupingSlot(i int) int {
	if n.Cycle != nil && i == n.Definition.UserWidth {
		return n.Cycle.MarkSlot
	}
	return i
}

type RowLockNode struct {
	QuerySchema
	Child     Node
	Marks     []RowMark
	FirstMode lock.Mode
}

// SetNode describes common-typed projected arms. No execution state or physical
// lineage is retained here. UNION ALL intentionally has no equivalence provider.
type SetNode struct {
	QuerySchema
	Left, Right                       *ProjectNode
	Operation                         string
	All                               bool
	LeftExpressions, RightExpressions []expr.Expr
	Grouping                          []Grouping
}

func (n *LimitNode) FinishTies(root Node) {
	if !n.WithTies {
		return
	}
	for {
		switch node := root.(type) {
		case *SortNode:
			for i := range node.Keys {
				node.Keys[i].PeerDemand = true
			}
			n.PeerKeys = node.Keys
			return
		case *EvaluationNode:
			root = node.Child
		case *DistinctOnNode:
			root = node.Child
		case *RowLockNode:
			root = node.Child
		default:
			panic("stuntgres: WITH TIES without finished ORDER")
		}
	}
}

func (n *WindowNode) SpecActive(index int) bool {
	for i, call := range n.Calls {
		if !call.AnalysisOnly && n.SpecIndices[i] == index {
			return true
		}
	}
	return false
}

// Every execution owns its dense occurrences and result overlay; the plan has no caches.
type WindowNode struct {
	QuerySchema
	Child                    Node
	Specs                    []WindowSpec
	Calls                    []WindowCall
	SpecIndices, SortIndices []int
}
