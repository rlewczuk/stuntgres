package expr

import (
	"context"

	"stuntgres/internal/sqltype"
)

// Optional hooks answer individual engine questions. A hook does not require
// Composite membership; nodes without it keep the consumer's generic behavior.
// Each hook is named for the single question it answers.

// ResultSite identifies the descriptor consumer asking about a result expression.
type ResultSite uint8

const (
	ResultSelect    ResultSite = iota // SELECT projection
	ResultReturning                   // DML RETURNING projection
)

// ResultNullability reports a pure, optional claim about the actual top-level
// expression at a descriptor site. It uses only immutable kind/recipe data:
// no evaluation, authority/catalog access, child traversal, allocation or mutation.
// A false known means leave both descriptor fields untouched, not nonnullable.
// Consumers retain their explicit engine-owned precedence and do not unwrap nodes.
type ResultNullability interface {
	ResultNullability(ResultSite) (nullable, known bool)
}

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

// GroupLeaf marks a childless call that grouping leaves unchanged when it
// rewrites an expression above its keys: the SQL value functions, the
// transaction clocks and version information. Grouping rebuilds every other
// call over its rewritten children and marks it stage dependent.
type GroupLeaf interface {
	GroupLeaf()
}

// Preflight is how statement preparation folds a call once the engine has
// preflighted its children (materialized their immutable conversions).
type Preflight uint8

const (
	// PreflightSimplify runs the engine's conditional simplification on the
	// call over its preflighted children.
	PreflightSimplify Preflight = iota
	// PreflightKeep leaves a childless leaf call as it is.
	PreflightKeep
	// PreflightChildren only preflights the children.
	PreflightChildren
	// PreflightEvaluate evaluates the call when it is immutable and depends on
	// no row, stage or domain.
	PreflightEvaluate
	// PreflightNullOrEvaluate replaces the call with a typed NULL when a
	// preflighted child is a literal NULL, and otherwise acts as
	// PreflightEvaluate.
	PreflightNullOrEvaluate
	// PreflightRegexNullOrEvaluate is PreflightNullOrEvaluate for calls that
	// own regular-expression execution state, which the NULL replacement
	// resets.
	PreflightRegexNullOrEvaluate
	// PreflightNullFirst replaces the call with a typed NULL when a child is a
	// literal NULL before preflight, and otherwise acts as PreflightSimplify.
	PreflightNullFirst
	// PreflightNullBare replaces the call with a NULL that keeps only its
	// usage and position when a preflighted child is a literal NULL.
	PreflightNullBare
)

// Preflighting selects a node's Preflight; nodes without it use
// PreflightSimplify.
type Preflighting interface {
	PreflightFold() Preflight
}

// ConstantTemplate nodes specialize their provider for a literal template
// argument during preflight, before conditional simplification. literal reports
// the value of a literal child; SpecializeTemplate returns whether the node
// changed.
type ConstantTemplate interface {
	SpecializeTemplate(ctx context.Context, literal func(Expr) (sqltype.Value, bool)) (Expr, bool, error)
}

// EvaluationOrdered nodes list their children in written order although they
// evaluate them in another (named arguments bound to formal slots).
// EvaluationOrder returns the child indices in evaluation order;
// MapSubstituted follows it.
type EvaluationOrdered interface {
	EvaluationOrder() []int
}

// FunctionSyntax answers PostgreSQL's looks_like_function for index
// definitions: pg_get_indexdef prints a key that looks like a function call
// bare and parenthesizes every other expression. Nodes without the hook are
// parenthesized.
type FunctionSyntax interface {
	LooksLikeFunction() bool
}

// Substitutable nodes replace their metadata and children while keeping their
// provider and every input derived at binding. Passes that substitute
// equivalent children (statement preflight, grouping rewrite) use it instead of
// Rebuild, which rebinds the provider against the new children.
type Substitutable interface {
	Substitute(info Info, children []Expr) (Expr, error)
}

// ConditionalClosure supplies a node-specific closed-DEFAULT decision.
// The adopting caller owns dependency/leaf policy and the child predicate.
// Implementations may refuse; otherwise every relevant child must pass.
// Inspection never evaluates or mutates the node or its children.
type ConditionalClosure interface {
	ClosedDefault(closed func(Expr) bool) bool
}

// ClosureTransparent marks a call that the closed-DEFAULT checks look
// through: the DEFAULT is closed when every child is. Those checks decide
// whether a DEFAULT built around a volatile generator (gen_random_uuid(), the
// transaction clocks) needs no row, parameter or stage. Other calls are closed
// only when no more than stable.
type ClosureTransparent interface {
	ClosureTransparent()
}
