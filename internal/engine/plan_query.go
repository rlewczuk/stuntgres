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
	regclassOutput bool
	// Local expression demand only; inherited view demand uses the current catalog.
	visibilitySnapshot bool
}

type querySchema []catalog.Column

func (querySchema) logicalNode() {}

func (s querySchema) Schema() []catalog.Column { return s }

type sourceNode struct {
	querySchema
	ref           ids.ObjectRef
	occurrence    int
	presenceSlot  int
	rangeName     string
	columnAliases []string
}

type singletonNode struct{ querySchema }

type valuesNode struct {
	querySchema
	viewOrdinal         int
	presenceSlot        int
	rows                [][]expr.Expr
	identityOrderDemand []bool // Only singleton USER identity VALUES need pruning.
}

type filterNode struct {
	querySchema
	child     LogicalNode
	predicate expr.Expr
}

type sortKey struct {
	expression       expr.Expr
	comparison       sqltype.Comparison
	equalityRoutine  ids.ObjectRef // Caller-checked window peers only; ordinary ordering uses support functions.
	position         int           // Original ORDER reference character position, not reused output syntax.
	outputOrdinal    int           // One-based resolved output occurrence; zero is a SQL expression.
	targetIdentity   int           // Resolved ON/ORDER target, independent of physical cache sharing.
	slot, cache      int
	desc, nullsFirst bool
	output           bool // This cache slot supplies a visible projection.
	peerDemand       bool // WITH TIES needs this cache identity after locked replacement.
}

type sortNode struct {
	querySchema
	child        LogicalNode
	keys         []sortKey
	cacheWidth   int
	outputs      []expr.Expr // Nonvolatile targets required by the sort input.
	presenceOnly bool        // Ignored EXISTS order graphs remain available to capture.
}

type projectNode struct {
	querySchema
	child               LogicalNode
	expressions         []expr.Expr
	analysisExpressions []expr.Expr // Unused named definitions remain analyzed, not executed.
	frozenValues        bool        // Synthetic FROM VALUES slots are already produced.
	frame               uint32
	syntax              *parser.Select
	cteOwner            bool // An invocation boundary, not statement-global producer state.
	hasTargetSet        bool // Analyzed query flag, independent of constant folding.
	ruleInvocation      bool // A transplanted input reads the existing lateral invocation.
}

// Only a physical literal or this execution's imported parameter is row-free.
// Never evaluate arbitrary expressions, rows, stages or subqueries as a proof.
func queryPredicateProofValue(ctx expr.Context, expr expr.Expr) (sqltype.Value, bool, error) {
	switch expression := expr.(type) {
	case literalExpression:
		return expression.value, true, nil
	case parameterExpression:
		value, err := expression.Eval(ctx)
		return value, true, err
	default:
		return sqltype.Value{}, false, nil
	}
}
