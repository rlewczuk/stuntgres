package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
)

type aggregateNode struct {
	querySchema
	child          LogicalNode
	keys           []expr.Expr
	grouping       []queryGrouping
	calls          []aggregateCall
	sets           [][]int // Canonical key IDs; each occurrence has isolated state.
	groupingActive bool
	presenceOnly   bool // Retain analyzed grouping while discarding eligible EXISTS demand.
}

func (n *aggregateNode) discardableGrouping() bool {
	if len(n.calls) != 0 || n.groupingActive {
		return false
	}
	if n.sets == nil {
		return len(n.keys) > 0
	}
	return len(n.sets) == 1 && len(n.sets[0]) > 0
}

// A derived source adapts projected child rows to the ordinary FROM frame.
// Its output is execution-private; it never owns captured relation pins.
type derivedNode struct {
	querySchema
	query        *BoundQuery
	cte          *cteDefinition
	table        *parser.TableReference
	ordinal      int
	scanFrame    uint32
	demand       []bool
	presenceSlot int
}

// distinctNode owns source-typed projected tuples. Its parent projection reads
// slots, so ordering and INSERT assignment never reevaluate key expressions.
type distinctNode struct {
	querySchema
	child        LogicalNode
	expressions  []expr.Expr
	grouping     []queryGrouping
	presenceOnly bool // Retain analyzed targets while discarding eligible EXISTS demand.
}

// Unlike DISTINCT, ON retains input occurrences and never demands payload
// equality. The existing cache and source own retained rows and key datums.
type distinctOnNode struct {
	querySchema
	child        LogicalNode
	expressions  []expr.Expr
	grouping     []queryGrouping
	presenceOnly bool
}

type joinNode struct {
	viewOrdinal  int
	presenceSlot int
	querySchema
	kind                  parser.JoinKind
	left, right           LogicalNode
	predicate             expr.Expr
	leftWidth, rightWidth int
	merges                []expr.Expr
	correlatedRight       bool
	frame                 uint32
	correlationPrefix     int
	captures              []functionCorrelation
}

// Captures retain their binding namespace and written origin while an enclosing
// join consumes locally satisfied references. Slots are relative to that join's
// inherited correlation frame, not to its output row.
type functionCorrelation struct {
	frame uint32
	slot  int
	name  string
	pos   parser.Position
}

type limitNode struct {
	querySchema
	child         LogicalNode
	offset, count expr.Expr
	withTies      bool
	peerKeys      []sortKey
	presenceOnly  bool // Eligible EXISTS retains analysis, not bounds/peer production.
}

// Evaluation keeps the input frame/provenance intact and shares the existing
// ORDER cache authority. Final projection reads these slots, never reevaluates.
type evaluationNode struct {
	querySchema
	child       LogicalNode
	expressions []expr.Expr
	base        int
}

// The self edge is an opaque identity, never a child pointing at its own query.
type worktableScanNode struct{ derivedNode }

type recursiveUnionNode struct {
	setNode
	definition *cteDefinition
	search     *searchRecipe
	cycle      *cycleRecipe
}

// Ordinary groupings cover original columns and then the optional CYCLE mark;
// generated record/path slots retain their recipe-owned equality authority.
func (n *recursiveUnionNode) groupingSlot(i int) int {
	if n.cycle != nil && i == n.definition.userWidth {
		return n.cycle.markSlot
	}
	return i
}

type rowLockNode struct {
	querySchema
	child     LogicalNode
	marks     []queryRowMark
	firstMode lock.Mode
}

// setNode describes common-typed projected arms. No execution state or physical
// lineage is retained here. UNION ALL intentionally has no equivalence provider.
type setNode struct {
	querySchema
	left, right                       *projectNode
	operation                         string
	all                               bool
	leftExpressions, rightExpressions []expr.Expr
	grouping                          []queryGrouping
}

func (n *limitNode) finishTies(root LogicalNode) {
	if !n.withTies {
		return
	}
	for {
		switch node := root.(type) {
		case *sortNode:
			for i := range node.keys {
				node.keys[i].peerDemand = true
			}
			n.peerKeys = node.keys
			return
		case *evaluationNode:
			root = node.child
		case *distinctOnNode:
			root = node.child
		case *rowLockNode:
			root = node.child
		default:
			panic("stuntgres: WITH TIES without finished ORDER")
		}
	}
}

func (n *windowNode) specActive(index int) bool {
	for i, call := range n.calls {
		if !call.analysisOnly && n.specIndices[i] == index {
			return true
		}
	}
	return false
}

// Every execution owns its dense occurrences and result overlay; the plan has no caches.
type windowNode struct {
	querySchema
	child                    LogicalNode
	specs                    []boundWindowSpec
	calls                    []windowCall
	specIndices, sortIndices []int
}
