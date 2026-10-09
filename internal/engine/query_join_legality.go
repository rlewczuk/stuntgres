package engine

import (
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Planning-only restrictions run on execution-owned parameters, never Prepare.
// Inherited marks cross view boundaries; a stored local mark does not mark its
// enclosing join or siblings.
func validateQueryJoinExecution(c *EvalContext, node LogicalNode, catalog CatalogRead, inherited relationLockMode) error {
	switch n := node.(type) {
	case *sourceNode:
		if d, ok := catalog.state.descriptors[n.ref.Address].(*relationDescriptor); ok && d.view != nil {
			if err := viewRowLockLegality(d.view, viewRowMode(d.view, inherited)); err != nil {
				return err
			}
			root, err := preflightQuery(c, d.view.query.Root)
			if err != nil {
				return err
			}
			return validateQueryJoinExecution(c, root, catalog, inherited)
		}
	case *joinNode:
		if inherited != 0 && n.kind != parser.JoinInner {
			return sqlError("0A000", queryRowClause(inherited)+" cannot be applied to the nullable side of an outer join")
		}
		if n.kind == parser.JoinFull {
			facts, err := fullJoinCondition(c, n, n.predicate, false)
			if err != nil {
				return err
			}
			if !facts.constant && len(facts.keys) == 0 {
				return sqlError("0A000", "FULL JOIN is only supported with merge-joinable or hash-joinable join conditions")
			}
		}
		if err := validateQueryJoinExecution(c, n.left, catalog, inherited); err != nil {
			return err
		}
		return validateQueryJoinExecution(c, n.right, catalog, inherited)
	case *rowLockNode:
		return validateQueryJoinExecution(c, n.child, catalog, strongerQueryRowMode(inherited, n.mode))
	case *limitNode:
		return validateQueryJoinExecution(c, n.child, catalog, inherited)
	case *evaluationNode:
		return validateQueryJoinExecution(c, n.child, catalog, inherited)
	case *filterNode:
		return validateQueryJoinExecution(c, n.child, catalog, inherited)
	case *projectNode:
		return validateQueryJoinExecution(c, n.child, catalog, inherited)
	case *sortNode:
		return validateQueryJoinExecution(c, n.child, catalog, inherited)
	case *aggregateNode:
		return validateQueryJoinExecution(c, n.child, catalog, inherited)
	case *distinctNode:
		if inherited != 0 {
			return sqlError("0A000", queryRowClause(inherited)+" is not allowed with DISTINCT clause")
		}
		return validateQueryJoinExecution(c, n.child, catalog, inherited)
	}
	return nil
}

// Every key is an effective equality. Keep only its ordered, bound operands:
// the original predicate/provider remains untouched, including under NOT.
type fullJoinKey struct {
	left, right BoundExpr
}

type fullJoinFacts struct {
	constant bool
	truth    Truth
	keys     []fullJoinKey
}

// Keys are actual conjuncts, not merely equality syntax somewhere in the tree.
// OR retains only structurally common keys in the same operand order. NOT is
// pushed through Boolean nodes with SQL three-valued truth. At the resulting
// match boundary, FALSE and UNKNOWN both reject a pair.
func fullJoinCondition(c *EvalContext, join *joinNode, expr BoundExpr, negate bool) (fullJoinFacts, error) {
	if expr == nil {
		return fullJoinFacts{constant: true, truth: True}, nil
	}
	p := expr.Properties()
	// Pruning preserves analyzed dependencies even on a physical literal.
	// Those properties still govern metadata/ACLs, not its planning truth.
	_, literal := expr.(literalExpression)
	if literal || p.Volatility == Immutable && !p.RowDependent && !p.StageDependent {
		value, err := expr.Eval(c)
		if err != nil {
			return fullJoinFacts{}, err
		}
		truth, err := TruthOf(value)
		if negate && truth != Unknown {
			if truth == True {
				truth = False
			} else {
				truth = True
			}
		}
		return fullJoinFacts{constant: true, truth: truth}, err
	}
	if e, ok := expr.(logicalExpression); ok {
		if e.op == parser.LogicalNot {
			return fullJoinCondition(c, join, e.left, !negate)
		}
		a, err := fullJoinCondition(c, join, e.left, negate)
		if err != nil {
			return fullJoinFacts{}, err
		}
		b, err := fullJoinCondition(c, join, e.right, negate)
		if err != nil {
			return fullJoinFacts{}, err
		}
		and := e.op == parser.LogicalAnd
		if negate {
			and = !and
		}
		if a.constant && b.constant {
			truth := Unknown
			if and {
				if a.truth == False || b.truth == False {
					truth = False
				} else if a.truth == True && b.truth == True {
					truth = True
				}
			} else {
				if a.truth == True || b.truth == True {
					truth = True
				} else if a.truth == False && b.truth == False {
					truth = False
				}
			}
			return fullJoinFacts{constant: true, truth: truth}, nil
		}
		if and {
			if a.constant && a.truth == False || b.constant && b.truth == False {
				return fullJoinFacts{constant: true, truth: False}, nil
			}
			if a.constant && a.truth == True {
				return b, nil
			}
			if b.constant && b.truth == True {
				return a, nil
			}
			return fullJoinFacts{keys: append(a.keys, b.keys...)}, nil
		}
		if a.constant && a.truth == True || b.constant && b.truth == True {
			return fullJoinFacts{constant: true, truth: True}, nil
		}
		if a.constant && a.truth != True {
			return b, nil
		}
		if b.constant && b.truth != True {
			return a, nil
		}
		var common []fullJoinKey
		binder := expressionBinder{}
		for _, left := range a.keys {
			for _, right := range b.keys {
				if binder.sameGroupExpression(left.left, right.left) && binder.sameGroupExpression(left.right, right.right) {
					common = append(common, left)
					break
				}
			}
		}
		return fullJoinFacts{keys: common}, nil
	}
	if e, ok := expr.(caseExpression); ok {
		var test sqltype.Value
		if e.test != nil {
			p := e.test.Properties()
			if p.Volatility != Immutable || p.RowDependent || p.StageDependent {
				return fullJoinFacts{}, nil
			}
			var err error
			test, err = e.test.Eval(c)
			if err != nil {
				return fullJoinFacts{}, err
			}
		}
		for _, arm := range e.arms {
			p := arm.condition.Properties()
			if p.Volatility != Immutable || p.RowDependent || p.StageDependent {
				return fullJoinFacts{}, nil
			}
			condition, err := arm.condition.Eval(c)
			if err != nil {
				return fullJoinFacts{}, err
			}
			match := false
			if e.test == nil {
				truth, err := TruthOf(condition)
				if err != nil {
					return fullJoinFacts{}, err
				}
				match = truth == True
			} else {
				view, err := arm.testPlan.Apply(c.Context, test, c.Input, c.Output)
				if err != nil {
					return fullJoinFacts{}, err
				}
				if !view.IsNull() && !condition.IsNull() {
					sign, err := arm.equality.Compare(c.Context, view, condition, c.Input)
					if err != nil {
						return fullJoinFacts{}, err
					}
					match = sign == 0
				}
			}
			if match {
				return fullJoinCondition(c, join, arm.result, negate)
			}
		}
		return fullJoinCondition(c, join, e.otherwise, negate)
	}
	if e, ok := expr.(conditionalExpression); ok && e.kind == parser.Coalesce {
		for _, arg := range e.args {
			p := arg.Properties()
			if p.Volatility != Immutable || p.RowDependent || p.StageDependent {
				// Dropping preceding constant NULLs still leaves COALESCE,
				// not a bare equality key, even with one remaining argument.
				break
			}
			value, err := arg.Eval(c)
			if err != nil {
				return fullJoinFacts{}, err
			}
			if !value.IsNull() {
				return fullJoinCondition(c, join, arg, negate)
			}
		}
	}
	if e, ok := expr.(comparisonExpression); ok && fullJoinComparisonOperator(e.operator, negate) == "=" {
		left, err := joinExpressionSides(e.left, join.leftWidth, join.rightWidth)
		if err != nil {
			return fullJoinFacts{}, err
		}
		right, err := joinExpressionSides(e.right, join.leftWidth, join.rightWidth)
		if err != nil {
			return fullJoinFacts{}, err
		}
		if e.left.Properties().Volatility != Volatile && e.right.Properties().Volatility != Volatile && (left == 1 && right == 2 || left == 2 && right == 1) {
			return fullJoinFacts{keys: []fullJoinKey{{left: e.left, right: e.right}}}, nil
		}
	}
	return fullJoinFacts{}, nil
}

// Bound ordinary comparisons have these SQL negators. This is a planning
// summary only: never replace the runtime comparison or its analyzed metadata.
func fullJoinComparisonOperator(operator string, negate bool) string {
	if !negate {
		return operator
	}
	switch operator {
	case "=":
		return "<>"
	case "<>":
		return "="
	case "<":
		return ">="
	case "<=":
		return ">"
	case ">":
		return "<="
	case ">=":
		return "<"
	}
	return ""
}

// Slots, including descendant synthetic merged slots, belong to child frames.
// Physical origins are deliberately irrelevant to FULL path capability.
func joinExpressionSides(expr BoundExpr, leftWidth, rightWidth int) (uint8, error) {
	if expr == nil {
		return 0, nil
	}
	if e, ok := expr.(columnExpression); ok {
		if e.slot < 0 || e.slot >= leftWidth+rightWidth {
			return 0, fmt.Errorf("stuntgres: invalid join expression slot")
		}
		if e.slot < leftWidth {
			return 1, nil
		}
		return 2, nil
	}
	var result uint8
	child := func(e BoundExpr) error {
		side, err := joinExpressionSides(e, leftWidth, rightWidth)
		result |= side
		return err
	}
	var children []BoundExpr
	switch e := expr.(type) {
	case literalExpression, parameterExpression, identityExpression, currentDateTimeExpression:
		return 0, nil
	case coercionExpression:
		return joinExpressionSides(e.child, leftWidth, rightWidth)
	case collationExpression:
		return joinExpressionSides(e.child, leftWidth, rightWidth)
	case arithmeticExpression:
		children = []BoundExpr{e.left, e.right}
	case bitwiseExpression:
		children = []BoundExpr{e.left, e.right}
	case comparisonExpression:
		children = []BoundExpr{e.left, e.right}
	case logicalExpression:
		children = []BoundExpr{e.left, e.right}
	case distinctExpression:
		children = []BoundExpr{e.left, e.right}
	case likeExpression:
		children = []BoundExpr{e.left, e.right}
	case likeEscapeExpression:
		children = []BoundExpr{e.pattern, e.escape}
	case nullTestExpression:
		children = []BoundExpr{e.child}
	case truthTestExpression:
		children = []BoundExpr{e.child}
	case timeZoneExpression:
		children = []BoundExpr{e.value, e.zone}
	case conditionalExpression:
		children = e.args
	case binaryExpression:
		children = e.args
	case stringExpression:
		children = e.args
	case mathExpression:
		children = e.args
	case randomExpression:
		children = e.args
	case sequenceCallExpression:
		children = e.args
	case indexPropertyExpression:
		children = e.children()
	case deparseExpression:
		children = e.args
	case commentInquiryExpression:
		children = e.args
	case nullCountExpression:
		children = e.args
	case formatExpression:
		children = e.args
	case currentSchemasExpression:
		children = []BoundExpr{e.arg}
	case visibilityInquiryExpression:
		children = []BoundExpr{e.arg}
	case caseExpression:
		if err := child(e.test); err != nil {
			return 0, err
		}
		if err := child(e.otherwise); err != nil {
			return 0, err
		}
		for _, arm := range e.arms {
			if err := child(arm.condition); err != nil {
				return 0, err
			}
			if err := child(arm.result); err != nil {
				return 0, err
			}
		}
		return result, nil
	default:
		return 0, fmt.Errorf("stuntgres: unsupported join dependency expression %T", expr)
	}
	for _, e := range children {
		if err := child(e); err != nil {
			return 0, err
		}
	}
	return result, nil
}
