package engine

import (
	"fmt"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Planning-only restrictions run on execution-owned parameters, never Prepare.
// Inherited marks cross view boundaries; a stored local mark does not mark its
// enclosing join or siblings.
func validateQueryJoinExecution(c *EvalContext, node LogicalNode, catalog CatalogRead, inherited relationLockMode) error {
	reads := queryExecutionSourceReads(node, nil)
	if source, ok := node.(*sourceNode); ok {
		reads[source.occurrence] = nil
	}
	return validateMarkedQueryJoin(c, node, catalog, nil, queryRowMark{mode: inherited}, false, nil, reads)
}

func firstNullableQueryMark(a, b queryRowMark) queryRowMark {
	if a.mode == 0 {
		return b
	}
	if b.mode != 0 && b.order < a.order {
		return b
	}
	return a
}

func nullableQueryRowMark(node LogicalNode, marks []queryRowMark, inherited queryRowMark) queryRowMark {
	switch n := node.(type) {
	case *sourceNode:
		return queryOccurrenceMark(marks, n.occurrence, inherited)
	case *derivedNode:
		if n.cte != nil {
			return queryRowMark{}
		}
		return nullableQueryRowMark(n.query.Root, marks, inherited)
	case *joinNode:
		return firstNullableQueryMark(nullableQueryRowMark(n.left, marks, inherited), nullableQueryRowMark(n.right, marks, inherited))
	case *rowLockNode:
		return nullableQueryRowMark(n.child, marks, inherited)
	case *limitNode:
		return nullableQueryRowMark(n.child, marks, inherited)
	case *evaluationNode:
		return nullableQueryRowMark(n.child, marks, inherited)
	case *filterNode:
		return nullableQueryRowMark(n.child, marks, inherited)
	case *projectNode:
		return nullableQueryRowMark(n.child, marks, inherited)
	case *sortNode:
		return nullableQueryRowMark(n.child, marks, inherited)
	case *aggregateNode:
		return nullableQueryRowMark(n.child, marks, inherited)
	case *setProjectNode:
		return nullableQueryRowMark(n.child, marks, inherited)
	}
	return queryRowMark{}
}

// Mutation targets and RETURNING are planned even when their input selects no
// rows. Walk only the execution-owned expressions that the binding consumes.
func validateMutationJoinExecution(c *EvalContext, b *binding, expressions [][]BoundExpr, catalog CatalogRead) error {
	owner := *c
	owner.Scope, owner.Frame = 1, 1
	validateTargets := func(targets []*arrayAssignmentTarget) error {
		for _, target := range targets {
			for path := target; path != nil; path = path.next {
				var storage [13]BoundExpr
				for _, child := range subscriptChildren(path.node, &storage) {
					if err := validateExpressionJoinExecution(&owner, child, catalog); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	if err := validateTargets(b.arrayTargets); err != nil {
		return err
	}
	action := b.update
	if b.conflict != nil && b.conflict.update != nil {
		action = b.conflict.update
	}
	if action != nil {
		if err := validateTargets(action.arrayTargets); err != nil {
			return err
		}
		for _, expression := range action.assignments {
			if err := validateExpressionJoinExecution(&owner, expression, catalog); err != nil {
				return err
			}
		}
		if err := validateExpressionJoinExecution(&owner, action.predicate, catalog); err != nil {
			return err
		}
	} else if err := validateExpressionJoinExecution(&owner, b.predicate, catalog); err != nil {
		return err
	}
	for _, row := range expressions {
		for j, expression := range row {
			if len(expressions) == 1 && len(b.identityDiscard) != 0 && b.identityDiscard[b.targets[j]] {
				continue
			}
			if err := validateExpressionJoinExecution(&owner, expression, catalog); err != nil {
				return err
			}
		}
	}
	if b.returning != nil {
		for _, expression := range b.returning.expressions {
			if err := validateExpressionJoinExecution(&owner, expression, catalog); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateExpressionJoinExecution(c *EvalContext, expression BoundExpr, catalog CatalogRead) error {
	if expression == nil {
		return nil
	}
	if err := c.Context.Err(); err != nil {
		return err
	}
	if subquery, ok := expression.(subqueryExpression); ok {
		project := subquery.queryPlan.Root.(*projectNode)
		child := *c
		child.Scope, child.Frame = 1, project.frame
		child.Row, child.orderValues, child.Outer = nil, nil, c
		var ignored *projectNode
		if subquery.presenceOnly {
			ignored = project
		}
		if err := validateMarkedQueryJoin(&child, project, catalog, nil, queryRowMark{}, false, ignored, queryExecutionSourceReads(project, ignored)); err != nil {
			return err
		}
	}
	if logical, ok := expression.(logicalExpression); ok {
		if err := validateExpressionJoinExecution(c, logical.left, catalog); err != nil {
			return err
		}
		if logical.queryRight {
			_, decisive, err := decisiveLogicalValue(&logical)
			if err != nil || decisive {
				return err
			}
		}
		return validateExpressionJoinExecution(c, logical.right, catalog)
	}
	var failure error
	boundExpressionChildren(expression, func(child BoundExpr) {
		if failure == nil {
			failure = validateExpressionJoinExecution(c, child, catalog)
		}
	})
	return failure
}
func validateMarkedQueryJoin(c *EvalContext, node LogicalNode, catalog CatalogRead, marks []queryRowMark, inherited queryRowMark, emptyInput bool, ignoredProject *projectNode, reads map[int][]int32) error {

	if node == nil {
		return nil
	}
	if err := c.Context.Err(); err != nil {
		return err
	}
	// Target subplans are planned even when their enclosing input is empty.
	// Eligible EXISTS retains their graph, but does not plan ignored output.
	if node != ignoredProject && !queryIgnoresOwnExpressions(node) {
		var failure error
		queryExpressions(node, func(expression BoundExpr) {
			if failure == nil {
				failure = validateExpressionJoinExecution(c, expression, catalog)
			}
		})
		if failure != nil {
			return failure
		}
	}
	switch n := node.(type) {
	case *setNode, *recursiveUnionNode:
		// Inline inherited sets were rejected at analysis. A stored set
		// source is opaque to an outer row mark, but all arms still plan.
		set := querySetNode(node)
		if err := validateMarkedQueryJoin(c, set.left, catalog, nil, queryRowMark{}, emptyInput, nil, reads); err != nil {
			return err
		}
		return validateMarkedQueryJoin(c, set.right, catalog, nil, queryRowMark{}, emptyInput, nil, reads)
	case *worktableScanNode:
		return nil
	case *sourceNode:
		mark := queryOccurrenceMark(marks, n.occurrence, inherited)
		if d, ok := catalog.state.descriptors[n.ref.Address].(*relationDescriptor); ok && d.relationKind == viewRelation {
			if err := viewRowLockLegality(d.view, queryRowDiagnostic(mark)); err != nil {
				return err
			}
			outputUses, err := querySourceReadUses(reads[n.occurrence], d.attributes)
			if err != nil {
				return err
			}
			child := *c
			child.quoteOrigin = quoteOrigin{owner: n.ref, definition: d.view.rule, revision: d.view.quoteRevision}
			root, storedReads, err := prepareStoredExecutionRoot(&child, d.view, outputUses)
			if err != nil {
				return err
			}
			return validateMarkedQueryJoin(&child, root, catalog, nil, mark, emptyInput, nil, storedReads)
		}
		return nil
	case *derivedNode:
		if n.cte != nil && n.cte.mutation != nil {
			return nil
		}
		if n.cte != nil {
			marks = nil
			inherited = queryRowMark{}
		}
		project, ok := n.query.Root.(*projectNode)
		if !ok {
			return fmt.Errorf("stuntgres: missing derived query frame")
		}
		child := *c
		child.Scope, child.Frame = 1, project.frame
		child.Row, child.orderValues, child.Outer = nil, nil, c.Outer
		root, err := preflightQuery(&child, project)
		if err != nil {
			return err
		}
		return validateMarkedQueryJoin(&child, root, catalog, marks, inherited, emptyInput, nil, reads)
	case *joinNode:
		mark := queryRowMark{}
		if n.kind == parser.JoinLeft || n.kind == parser.JoinFull {
			mark = nullableQueryRowMark(n.right, marks, inherited)
		}
		if n.kind == parser.JoinRight || n.kind == parser.JoinFull {
			mark = firstNullableQueryMark(mark, nullableQueryRowMark(n.left, marks, inherited))
		}
		if mark.mode != 0 {
			return sqlError("0A000", queryRowClause(mark.mode)+" cannot be applied to the nullable side of an outer join")
		}
		if n.kind == parser.JoinFull && !emptyInput {
			facts, err := fullJoinCondition(c, n, n.predicate, false)
			if err != nil {
				return err
			}
			if !facts.constant && len(facts.keys) == 0 {
				return sqlError("0A000", "FULL JOIN is only supported with merge-joinable or hash-joinable join conditions")
			}
		}
	case *rowLockNode:
		if len(marks) == 0 {
			marks = n.marks
		} else {
			width := len(n.marks)
			if len(marks) > width {
				width = len(marks)
			}
			local := make([]queryRowMark, width)
			copy(local, marks)
			for i, mark := range n.marks {
				local[i] = mergeQueryRowMark(local[i], mark)
			}
			marks = local
		}
	case *filterNode:
		// Prune only this input's proven dead FROM path, using this execution's
		// parameter frame. Nullable rowmark legality still applies.
		if !emptyInput {
			rejects, err := queryPredicateRejectsAll(c, n.predicate)
			if err != nil {
				return err
			}
			emptyInput = rejects
		}
	case *distinctNode, *distinctOnNode:
		if inherited.mode != 0 {
			return sqlError("0A000", queryRowClause(queryRowDiagnostic(inherited))+" is not allowed with DISTINCT clause")
		}
	}
	left, right := queryInputs(node)
	if err := validateMarkedQueryJoin(c, left, catalog, marks, inherited, emptyInput, ignoredProject, reads); err != nil {
		return err
	}
	return validateMarkedQueryJoin(c, right, catalog, marks, inherited, emptyInput, ignoredProject, reads)
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
	if literal || p.Volatility == Immutable && !p.RowDependent && !p.StageDependent && !p.RuntimeDomainConversion {
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
			if p.Volatility != Immutable || p.RowDependent || p.StageDependent || p.RuntimeDomainConversion {
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
			if p.Volatility != Immutable || p.RowDependent || p.StageDependent || p.RuntimeDomainConversion {
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
				if arm.testPlan.ChecksDomain() {
					return fullJoinFacts{}, nil
				}
				view, err := arm.testPlan.Apply(c.Context, test, c.Input, c.Output)
				if err != nil {
					return fullJoinFacts{}, err
				}
				if !view.IsNull() && !condition.IsNull() {
					equal, err := arm.equality.EqualValues(c.Context, view, condition, c.Input)
					if err != nil {
						return fullJoinFacts{}, err
					}
					match = equal
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
			if p.Volatility != Immutable || p.RowDependent || p.StageDependent || p.RuntimeDomainConversion {
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
	case literalExpression, parameterExpression, identityExpression, functions.CurrentDateTime[*EvalContext, ExprProperties], functions.TransactionTime[*EvalContext, ExprProperties]:
		return 0, nil
	case rowExpression:
		children = e.fields
	case fieldExpression:
		return joinExpressionSides(e.base, leftWidth, rightWidth)
	case arrayExpression:
		children = e.elements
	case subscriptExpression:
		sides, err := joinExpressionSides(e.base, leftWidth, rightWidth)
		if err != nil {
			return 0, err
		}
		for _, s := range e.subscripts {
			for _, child := range [2]BoundExpr{s.lower, s.upper} {
				if child == nil {
					continue
				}
				side, err := joinExpressionSides(child, leftWidth, rightWidth)
				if err != nil {
					return 0, err
				}
				sides |= side
			}
		}
		return sides, nil
	case coercionExpression:
		return joinExpressionSides(e.child, leftWidth, rightWidth)
	case collationExpression:
		return joinExpressionSides(e.child, leftWidth, rightWidth)
	case arithmeticExpression:
		children = []BoundExpr{e.left, e.right}
	case bitwiseExpression:
		children = []BoundExpr{e.left, e.right}
	case rowComparisonExpression:
		var sides uint8
		for _, pair := range e.comparisons {
			left, err := joinExpressionSides(pair.left, leftWidth, rightWidth)
			if err != nil {
				return 0, err
			}
			right, err := joinExpressionSides(pair.right, leftWidth, rightWidth)
			if err != nil {
				return 0, err
			}
			sides |= left | right
		}
		return sides, nil
	case scalarArrayComparisonExpression:
		sides, err := joinExpressionSides(e.left, leftWidth, rightWidth)
		if err != nil {
			return 0, err
		}
		if e.array != nil {
			side, err := joinExpressionSides(e.array, leftWidth, rightWidth)
			if err != nil {
				return 0, err
			}
			sides |= side
		}
		for _, item := range e.items {
			side, err := joinExpressionSides(item, leftWidth, rightWidth)
			if err != nil {
				return 0, err
			}
			sides |= side
		}
		return sides, nil
	case comparisonExpression:
		children = []BoundExpr{e.left, e.right}
	case logicalExpression:
		children = []BoundExpr{e.left, e.right}
	case distinctExpression:
		children = []BoundExpr{e.left, e.right}
	case likeExpression:
		children = []BoundExpr{e.left, e.right}
	case seriesExpression:
		children = e.args
	case subscriptsExpression:
		children = e.args
	case multirangeUnnestExpression:
		children = e.args
	case functions.LikeEscape[BoundExpr, *EvalContext, ExprProperties]:
		children = []BoundExpr{e.Pattern, e.Escape}
	case functions.Posix[BoundExpr, *EvalContext, ExprProperties]:
		children = []BoundExpr{e.Left, e.Right}
	case functions.Similar[BoundExpr, *EvalContext, ExprProperties]:
		children = []BoundExpr{e.Left, e.Right}
	case functions.SimilarEscape[BoundExpr, *EvalContext, ExprProperties]:
		children = []BoundExpr{e.Pattern, e.Escape}
	case functions.PosixSubstring[BoundExpr, *EvalContext, ExprProperties]:
		children = e.Args[:]
	case functions.SimilarSubstring[BoundExpr, *EvalContext, ExprProperties]:
		children = e.Args[:]
	case functions.Regexp[BoundExpr, *EvalContext, ExprProperties]:
		children = e.Args
	case functions.RegexpSet[BoundExpr, *EvalContext, ExprProperties]:
		children = e.Args
	case functions.JSONKeys[BoundExpr, *EvalContext, ExprProperties]:
		children = e.Args
	case functions.JSONBElements[BoundExpr, *EvalContext, ExprProperties]:
		children = e.Args
	case functions.JSONEach[BoundExpr, *EvalContext, ExprProperties]:
		children = e.Args
	case functions.JSONElements[BoundExpr, *EvalContext, ExprProperties]:
		children = e.Args
	case nullTestExpression:
		children = []BoundExpr{e.child}
	case truthTestExpression:
		children = []BoundExpr{e.child}
	case functions.Timezone[BoundExpr, *EvalContext, ExprProperties]:
		children = []BoundExpr{e.Value, e.Zone}
	case conditionalExpression:
		children = e.args
	case functions.Binary[BoundExpr, *EvalContext, ExprProperties]:
		children = e.Args
	case functions.DatetimeJustify[BoundExpr, *EvalContext, ExprProperties]:
		children = e.Arguments()
	case functions.DatetimeExtract[BoundExpr, *EvalContext, ExprProperties]:
		children = e.Arguments()
	case functions.DatetimeOverlaps[BoundExpr, *EvalContext, ExprProperties]:
		children = e.Arguments()
	case functions.DatetimeBin[BoundExpr, *EvalContext, ExprProperties]:
		children = e.Arguments()
	case functions.DatetimeTrunc[BoundExpr, *EvalContext, ExprProperties]:
		children = e.Arguments()
	case functions.DatetimeZoneCalendar[BoundExpr, *EvalContext, ExprProperties]:
		children = e.Arguments()
	case functions.DatetimeAgeFinite[BoundExpr, *EvalContext, ExprProperties]:
		children = e.Arguments()
	case functions.JSONStripNulls[BoundExpr, *EvalContext, ExprProperties]:
		children = e.Arguments()
	case functions.JSONExtract[BoundExpr, *EvalContext, ExprProperties]:
		children = e.Arguments()
	case functions.JSONBSet[BoundExpr, *EvalContext, ExprProperties]:
		children = e.Arguments()
	case functions.DatetimeMake[BoundExpr, *EvalContext, ExprProperties]:
		children = e.Arguments()
	case functions.DatetimeFormatting[BoundExpr, *EvalContext, ExprProperties]:
		children = e.Args[:]
	case functions.NumericFormatting[BoundExpr, *EvalContext, ExprProperties]:
		children = e.Args[:]
	case functions.String[BoundExpr, *EvalContext, ExprProperties]:
		children = e.Args
	case functions.ArrayInspect[BoundExpr, *EvalContext, ExprProperties]:
		children = e.Args
	case functions.ArraySearch[BoundExpr, *EvalContext, ExprProperties]:
		children = e.Args
	case functions.UUIDExtract[BoundExpr, *EvalContext, ExprProperties]:
		return joinExpressionSides(e.Arg, leftWidth, rightWidth)
	case functions.RangeInspect[BoundExpr, *EvalContext, ExprProperties]:
		children = e.Args
	case functions.Enum[BoundExpr, *EvalContext, ExprProperties]:
		children = e.Args
	case functions.Math[BoundExpr, *EvalContext, ExprProperties]:
		children = e.Args
	case functions.Random[BoundExpr, *EvalContext, ExprProperties]:
		children = e.Args
	case functions.DatetimeSleep[BoundExpr, *EvalContext, ExprProperties]:
		children = []BoundExpr{e.Arg}
	case advisoryExpression:
		children = e.args
	case functions.UUIDGeneration[BoundExpr, *EvalContext, ExprProperties]:
		children = e.Args
	case sequenceCallExpression:
		children = e.args
	case indexPropertyExpression:
		children = e.children()
	case deparseExpression:
		children = e.args
	case commentInquiryExpression:
		children = e.args
	case functions.NullCount[BoundExpr, *EvalContext, ExprProperties]:
		children = e.Args
	case installedLevenshteinFunctionExpression:
		children = e.Args
	case installedCryptoFunctionExpression:
		children = e.Args
	case installedFunctionExpression:
		children = []BoundExpr{e.arg}
	case sqlJSONExpression:
		children = e.args
	case jsonPredicateExpression:
		children = []BoundExpr{e.child}
	case functions.JSONCreate[BoundExpr, *EvalContext, ExprProperties]:
		children = e.Args
	case functions.StringVariadic[BoundExpr, *EvalContext, ExprProperties, quoteOrigin]:
		children = e.Args
	case functions.Format[BoundExpr, *EvalContext, ExprProperties]:
		children = e.Args
	case currentSchemasExpression:
		children = []BoundExpr{e.arg}
	case accessInquiryExpression:
		children = e.arguments()
	case namespaceInquiryExpression:
		children = e.arguments()
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
