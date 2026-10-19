package expr

import "stuntgres/internal/sqltype"

// Optional hooks a Composite node implements when an engine switch needs more
// than its children. Each hook is named for the single question it answers;
// switches fall back to their previous generic behavior when a node lacks it.

// Intrinsic reports a node's own planned volatility, excluding its children.
// Planning merges it with the children's planned volatility, so dependence that
// analysis retained from eliminated CASE or COALESCE arms does not leak into
// the surviving tree.
type Intrinsic interface {
	IntrinsicVolatility() Volatility
}

// Fold is how conditional planning treats a call once its children are
// simplified.
type Fold uint8

const (
	// FoldConstant evaluates an immutable call whose children are all literals.
	FoldConstant Fold = iota
	// FoldNever leaves the call to execution: set- and record-returning calls
	// whose result belongs to their query stage, and stable, volatile or leaf
	// calls planning never evaluates. Such calls never count as constant inputs.
	FoldNever
	// FoldOverlapsRoute runs the engine's OVERLAPS inline route before the
	// constant fold.
	FoldOverlapsRoute
	// FoldQuoteCapture replaces the constant fold with the engine's quote-capture
	// fold.
	FoldQuoteCapture
)

// Folding selects a node's Fold; nodes without it use FoldConstant.
type Folding interface {
	PlanningFold() Fold
}

// Strict is when conditional planning replaces a call that has a literal NULL
// child with a typed NULL.
type Strict uint8

const (
	// StrictNone never replaces the call.
	StrictNone Strict = iota
	// StrictBefore checks the children before simplifying them.
	StrictBefore
	// StrictAfter checks the simplified children; the NULL keeps the call's
	// metadata without its routine reference.
	StrictAfter
	// StrictAfterBare checks the simplified children; the NULL keeps only the
	// call's usage and position.
	StrictAfterBare
)

// StrictFolding selects a node's Strict; nodes without it use StrictNone.
type StrictFolding interface {
	StrictNull() Strict
}

// StoredProvider marks a node that a stored generation plan may contain.
// Catalog validation calls ValidateStoredProvider before validating the
// children, and rejects protocol nodes without the hook.
type StoredProvider interface {
	ValidateStoredProvider() error
}

// Retag is the engine's environment for rebinding a stored tree to retained
// catalog generations.
type Retag struct {
	// Type maps a descriptor to its retained generation.
	Type func(*sqltype.Descriptor) *sqltype.Descriptor
	// Collation remaps a stored collation; it is the identity when the retag
	// remaps no collations.
	Collation func(sqltype.CollationRef) (sqltype.CollationRef, error)
	// Collated derives collation metadata for info from the given inputs.
	Collated func(Info, ...Expr) (Info, error)
}

// Retaggable nodes hold inputs derived from the catalog beyond their metadata
// and children. Retag receives the retagged metadata, the retagged children and
// whether either changed; it returns the rebuilt node and whether anything
// changed, including its derived inputs. The engine keeps the original node
// when nothing changed.
type Retaggable interface {
	Retag(info Info, children []Expr, changed bool, env Retag) (Expr, bool, error)
}

// Annotated nodes carry an opaque engine annotation that rebuilds keep. Only the
// engine reads or replaces it; the node never interprets it.
type Annotated interface {
	Annotation() any
	WithAnnotation(any) Expr
}

// GroupIdentity nodes decide whether another expression is the same grouping
// expression: GROUP BY matching, DISTINCT ON and CHECK definition equality.
// SameGroup compares the node's own identity and uses same for children, so
// the engine keeps its frame and query comparisons. definition selects CHECK
// definition equality, which ignores presentation-only differences. Nodes
// without the hook never match.
type GroupIdentity interface {
	SameGroup(other Expr, same func(a, b Expr) bool, definition bool) bool
}
