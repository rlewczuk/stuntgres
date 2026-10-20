package engine

import (
	"stuntgres/internal/engine/expr"
)

type querySetSlot struct {
	Original  expr.Expr
	Column    expr.Expr
	Scheduled bool // The owner reads its reserved slot only for an active scalar event.
}

func (n *setProjectNode) AppendScalar(expression expr.Expr, candidate, candidates int) {
	if n.Scalars == nil {
		n.Scalars = make([]expr.Expr, 0, candidates)
	}
	if n.ScalarSlots != nil {
		n.ScalarSlots = append(n.ScalarSlots, candidate)
	} else if candidate != len(n.Scalars) {
		n.ScalarSlots = make([]int, len(n.Scalars), candidates)
		for i := range n.ScalarSlots {
			n.ScalarSlots[i] = i
		}
		n.ScalarSlots = append(n.ScalarSlots, candidate)
	}
	n.Scalars = append(n.Scalars, expression)
}

// These are closed query stages, not a general table-function provider surface.
type functionFromNode struct {
	querySchema
	Calls                  []expr.Expr
	ScalarNull             []bool // Execution-only strict scalar plans; analyzed calls retain the stage layout.
	Ordinality, Correlated bool
	Frame                  uint32
	ViewOrdinal            int
	RegexpNamespace        bool
	Captures               []functionCorrelation
	PresenceSlot           int
}

func (*functionFromNode) logicalNode() {}

type querySetStepKind uint8

type querySetStep struct {
	Kind  querySetStepKind
	Index int
}

// Retained typed operands precede lowering. Canonical execution preflight owns
// simplification and re-lowers surviving occurrences without rebinding syntax.
type querySetTemplate struct {
	Group          *uint32 // Immutable local-group identity and analyzed frame, shared across copies.
	Expressions    []expr.Expr
	Origins        []expr.Expr
	Scalars        []expr.Expr // Typed non-call owner candidates, before lower-marker substitution.
	ScalarRequests []int       // Original owner request indices, not source-text identity.
	Attempts       bool
	Steps          []querySetStep // Original fixed marker/scalar identities in executable root order.
}

type setProjectNode struct {
	querySchema
	Child                  LogicalNode
	Calls                  []expr.Expr
	Scalars                []expr.Expr
	Frame                  uint32
	Template               *querySetTemplate
	CallSlots              []int // Marker slots relative to the group's fixed base, retained and executable.
	ScalarSlots            []int // Sparse candidate slots; nil encodes the ordinary identity layout.
	BaseWidth, MarkerWidth int
	ScalarBindings         []querySetSlot // Execution-private owner roots and fixed scalar slots.
	ScalarUpper            bool
	Steps                  []querySetStep // This level's local call/scalar indices, not storage-slot order.
}

func (*setProjectNode) logicalNode() {}
