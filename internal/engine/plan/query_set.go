package plan

import (
	"stuntgres/internal/engine/expr"
)

type SetSlot struct {
	Original  expr.Expr
	Column    expr.Expr
	Scheduled bool // The owner reads its reserved slot only for an active scalar event.
}

func (n *SetProjectNode) AppendScalar(expression expr.Expr, candidate, candidates int) {
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
type FunctionFromNode struct {
	QuerySchema
	Calls                  []expr.Expr
	ScalarNull             []bool // Execution-only strict scalar plans; analyzed calls retain the stage layout.
	Ordinality, Correlated bool
	Frame                  uint32
	ViewOrdinal            int
	RegexpNamespace        bool
	Captures               []FunctionCorrelation
	PresenceSlot           int
}

func (*FunctionFromNode) logicalNode() {}

type SetStepKind uint8

type SetStep struct {
	Kind  SetStepKind
	Index int
}

// Retained typed operands precede lowering. Canonical execution preflight owns
// simplification and re-lowers surviving occurrences without rebinding syntax.
type SetTemplate struct {
	Group          *uint32 // Immutable local-group identity and analyzed frame, shared across copies.
	Expressions    []expr.Expr
	Origins        []expr.Expr
	Scalars        []expr.Expr // Typed non-call owner candidates, before lower-marker substitution.
	ScalarRequests []int       // Original owner request indices, not source-text identity.
	Attempts       bool
	Steps          []SetStep // Original fixed marker/scalar identities in executable root order.
}

type SetProjectNode struct {
	QuerySchema
	Child                  Node
	Calls                  []expr.Expr
	Scalars                []expr.Expr
	Frame                  uint32
	Template               *SetTemplate
	CallSlots              []int // Marker slots relative to the group's fixed base, retained and executable.
	ScalarSlots            []int // Sparse candidate slots; nil encodes the ordinary identity layout.
	BaseWidth, MarkerWidth int
	ScalarBindings         []SetSlot // Execution-private owner roots and fixed scalar slots.
	ScalarUpper            bool
	Steps                  []SetStep // This level's local call/scalar indices, not storage-slot order.
}

func (*SetProjectNode) logicalNode() {}
