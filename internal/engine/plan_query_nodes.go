package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
)

type aggregateNode struct {
	querySchema
	Child          LogicalNode
	Keys           []expr.Expr
	Grouping       []queryGrouping
	Calls          []aggregateCall
	Sets           [][]int // Canonical key IDs; each occurrence has isolated state.
	GroupingActive bool
	PresenceOnly   bool // Retain analyzed grouping while discarding eligible EXISTS demand.
}

func (n *aggregateNode) DiscardableGrouping() bool {
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
type derivedNode struct {
	querySchema
	Query        *BoundQuery
	CTE          *cteDefinition
	Table        *parser.TableReference
	Ordinal      int
	ScanFrame    uint32
	Demand       []bool
	PresenceSlot int
}

// distinctNode owns source-typed projected tuples. Its parent projection reads
// slots, so ordering and INSERT assignment never reevaluate key expressions.
type distinctNode struct {
	querySchema
	Child        LogicalNode
	Expressions  []expr.Expr
	Grouping     []queryGrouping
	PresenceOnly bool // Retain analyzed targets while discarding eligible EXISTS demand.
}

// Unlike DISTINCT, ON retains input occurrences and never demands payload
// equality. The existing cache and source own retained rows and key datums.
type distinctOnNode struct {
	querySchema
	Child        LogicalNode
	Expressions  []expr.Expr
	Grouping     []queryGrouping
	PresenceOnly bool
}

type joinNode struct {
	ViewOrdinal  int
	PresenceSlot int
	querySchema
	Kind                  parser.JoinKind
	Left, Right           LogicalNode
	Predicate             expr.Expr
	LeftWidth, RightWidth int
	Merges                []expr.Expr
	CorrelatedRight       bool
	Frame                 uint32
	CorrelationPrefix     int
	Captures              []functionCorrelation
}

// Captures retain their binding namespace and written origin while an enclosing
// join consumes locally satisfied references. Slots are relative to that join's
// inherited correlation frame, not to its output row.
type functionCorrelation struct {
	Frame uint32
	Slot  int
	Name  string
	Pos   parser.Position
}

type limitNode struct {
	querySchema
	Child         LogicalNode
	Offset, Count expr.Expr
	WithTies      bool
	PeerKeys      []sortKey
	PresenceOnly  bool // Eligible EXISTS retains analysis, not bounds/peer production.
}

// Evaluation keeps the input frame/provenance intact and shares the existing
// ORDER cache authority. Final projection reads these slots, never reevaluates.
type evaluationNode struct {
	querySchema
	Child       LogicalNode
	Expressions []expr.Expr
	Base        int
}

// The self edge is an opaque identity, never a child pointing at its own query.
type worktableScanNode struct{ derivedNode }

type recursiveUnionNode struct {
	setNode
	Definition *cteDefinition
	Search     *searchRecipe
	Cycle      *cycleRecipe
}

// Ordinary groupings cover original columns and then the optional CYCLE mark;
// generated record/path slots retain their recipe-owned equality authority.
func (n *recursiveUnionNode) GroupingSlot(i int) int {
	if n.Cycle != nil && i == n.Definition.UserWidth {
		return n.Cycle.MarkSlot
	}
	return i
}

type rowLockNode struct {
	querySchema
	Child     LogicalNode
	Marks     []queryRowMark
	FirstMode lock.Mode
}

// setNode describes common-typed projected arms. No execution state or physical
// lineage is retained here. UNION ALL intentionally has no equivalence provider.
type setNode struct {
	querySchema
	Left, Right                       *projectNode
	Operation                         string
	All                               bool
	LeftExpressions, RightExpressions []expr.Expr
	Grouping                          []queryGrouping
}

func (n *limitNode) FinishTies(root LogicalNode) {
	if !n.WithTies {
		return
	}
	for {
		switch node := root.(type) {
		case *sortNode:
			for i := range node.Keys {
				node.Keys[i].PeerDemand = true
			}
			n.PeerKeys = node.Keys
			return
		case *evaluationNode:
			root = node.Child
		case *distinctOnNode:
			root = node.Child
		case *rowLockNode:
			root = node.Child
		default:
			panic("stuntgres: WITH TIES without finished ORDER")
		}
	}
}

func (n *windowNode) SpecActive(index int) bool {
	for i, call := range n.Calls {
		if !call.AnalysisOnly && n.SpecIndices[i] == index {
			return true
		}
	}
	return false
}

// Every execution owns its dense occurrences and result overlay; the plan has no caches.
type windowNode struct {
	querySchema
	Child                    LogicalNode
	Specs                    []boundWindowSpec
	Calls                    []windowCall
	SpecIndices, SortIndices []int
}
