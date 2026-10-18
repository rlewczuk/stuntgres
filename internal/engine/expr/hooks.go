package expr

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
