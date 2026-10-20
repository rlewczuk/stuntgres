package engine

import (
	"stuntgres/internal/engine/expr"
)

type querySetSlot struct {
	original  expr.Expr
	column    expr.Expr
	scheduled bool // The owner reads its reserved slot only for an active scalar event.
}

func (n *setProjectNode) appendScalar(expression expr.Expr, candidate, candidates int) {
	if n.scalars == nil {
		n.scalars = make([]expr.Expr, 0, candidates)
	}
	if n.scalarSlots != nil {
		n.scalarSlots = append(n.scalarSlots, candidate)
	} else if candidate != len(n.scalars) {
		n.scalarSlots = make([]int, len(n.scalars), candidates)
		for i := range n.scalarSlots {
			n.scalarSlots[i] = i
		}
		n.scalarSlots = append(n.scalarSlots, candidate)
	}
	n.scalars = append(n.scalars, expression)
}

// These are closed query stages, not a general table-function provider surface.
type functionFromNode struct {
	querySchema
	calls                  []expr.Expr
	scalarNull             []bool // Execution-only strict scalar plans; analyzed calls retain the stage layout.
	ordinality, correlated bool
	frame                  uint32
	viewOrdinal            int
	regexpNamespace        bool
	captures               []functionCorrelation
	presenceSlot           int
}

func (*functionFromNode) logicalNode() {}

type querySetStepKind uint8

type querySetStep struct {
	kind  querySetStepKind
	index int
}

// Retained typed operands precede lowering. Canonical execution preflight owns
// simplification and re-lowers surviving occurrences without rebinding syntax.
type querySetTemplate struct {
	group          *uint32 // Immutable local-group identity and analyzed frame, shared across copies.
	expressions    []expr.Expr
	origins        []expr.Expr
	scalars        []expr.Expr // Typed non-call owner candidates, before lower-marker substitution.
	scalarRequests []int       // Original owner request indices, not source-text identity.
	attempts       bool
	steps          []querySetStep // Original fixed marker/scalar identities in executable root order.
}

type setProjectNode struct {
	querySchema
	child                  LogicalNode
	calls                  []expr.Expr
	scalars                []expr.Expr
	frame                  uint32
	template               *querySetTemplate
	callSlots              []int // Marker slots relative to the group's fixed base, retained and executable.
	scalarSlots            []int // Sparse candidate slots; nil encodes the ordinary identity layout.
	baseWidth, markerWidth int
	scalarBindings         []querySetSlot // Execution-private owner roots and fixed scalar slots.
	scalarUpper            bool
	steps                  []querySetStep // This level's local call/scalar indices, not storage-slot order.
}

func (*setProjectNode) logicalNode() {}
