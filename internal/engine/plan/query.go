package plan

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Node is a closed, immutable bound tree. Execution never resolves names.
type Node interface {
	logicalNode()
	Schema() []catalog.Column
}

type Query struct {
	Root           Node
	RegclassOutput bool
	// Local expression demand only; inherited view demand uses the current catalog.
	VisibilitySnapshot bool
}

type QuerySchema []catalog.Column

func (QuerySchema) logicalNode() {}

func (s QuerySchema) Schema() []catalog.Column { return s }

type SourceNode struct {
	QuerySchema
	Ref           ids.ObjectRef
	Occurrence    int
	PresenceSlot  int
	RangeName     string
	ColumnAliases []string
}

type SingletonNode struct{ QuerySchema }

type ValuesNode struct {
	QuerySchema
	ViewOrdinal         int
	PresenceSlot        int
	Rows                [][]expr.Expr
	IdentityOrderDemand []bool // Only singleton USER identity VALUES need pruning.
}

type FilterNode struct {
	QuerySchema
	Child     Node
	Predicate expr.Expr
}

type SortKey struct {
	Expression       expr.Expr
	Comparison       sqltype.Comparison
	EqualityRoutine  ids.ObjectRef // Caller-checked window peers only; ordinary ordering uses support functions.
	Position         int           // Original ORDER reference character position, not reused output syntax.
	OutputOrdinal    int           // One-based resolved output occurrence; zero is a SQL expression.
	TargetIdentity   int           // Resolved ON/ORDER target, independent of physical cache sharing.
	Slot, Cache      int
	Desc, NullsFirst bool
	Output           bool // This cache slot supplies a visible projection.
	PeerDemand       bool // WITH TIES needs this cache identity after locked replacement.
}

type SortNode struct {
	QuerySchema
	Child        Node
	Keys         []SortKey
	CacheWidth   int
	Outputs      []expr.Expr // Nonvolatile targets required by the sort input.
	PresenceOnly bool        // Ignored EXISTS order graphs remain available to capture.
}

type ProjectNode struct {
	QuerySchema
	Child               Node
	Expressions         []expr.Expr
	AnalysisExpressions []expr.Expr // Unused named definitions remain analyzed, not executed.
	FrozenValues        bool        // Synthetic FROM VALUES slots are already produced.
	Frame               uint32
	Syntax              *parser.Select
	CTEOwner            bool // An invocation boundary, not statement-global producer state.
	HasTargetSet        bool // Analyzed query flag, independent of constant folding.
	RuleInvocation      bool // A transplanted input reads the existing lateral invocation.
}

// Only a physical literal or this execution's imported parameter is row-free.
// Never evaluate arbitrary expressions, rows, stages or subqueries as a proof.
func QueryPredicateProofValue(ctx expr.Context, expr expr.Expr) (sqltype.Value, bool, error) {
	switch expression := expr.(type) {
	case LiteralExpr:
		return expression.Value, true, nil
	case ParameterExpr:
		value, err := expression.Eval(ctx)
		return value, true, err
	default:
		return sqltype.Value{}, false, nil
	}
}
