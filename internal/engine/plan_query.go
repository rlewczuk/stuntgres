package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// LogicalNode is a closed, immutable bound tree. Execution never resolves names.
type LogicalNode interface {
	logicalNode()
	Schema() []catalog.Column
}

type BoundQuery struct {
	Root           LogicalNode
	RegclassOutput bool
	// Local expression demand only; inherited view demand uses the current catalog.
	VisibilitySnapshot bool
}

type querySchema []catalog.Column

func (querySchema) logicalNode() {}

func (s querySchema) Schema() []catalog.Column { return s }

type sourceNode struct {
	querySchema
	Ref           ids.ObjectRef
	Occurrence    int
	PresenceSlot  int
	RangeName     string
	ColumnAliases []string
}

type singletonNode struct{ querySchema }

type valuesNode struct {
	querySchema
	ViewOrdinal         int
	PresenceSlot        int
	Rows                [][]expr.Expr
	IdentityOrderDemand []bool // Only singleton USER identity VALUES need pruning.
}

type filterNode struct {
	querySchema
	Child     LogicalNode
	Predicate expr.Expr
}

type sortKey struct {
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

type sortNode struct {
	querySchema
	Child        LogicalNode
	Keys         []sortKey
	CacheWidth   int
	Outputs      []expr.Expr // Nonvolatile targets required by the sort input.
	PresenceOnly bool        // Ignored EXISTS order graphs remain available to capture.
}

type projectNode struct {
	querySchema
	Child               LogicalNode
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
	case literalExpression:
		return expression.Value, true, nil
	case parameterExpression:
		value, err := expression.Eval(ctx)
		return value, true, err
	default:
		return sqltype.Value{}, false, nil
	}
}
