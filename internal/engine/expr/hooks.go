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
