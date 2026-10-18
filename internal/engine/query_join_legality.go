package engine

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Planning-only restrictions run on execution-owned parameters, never Prepare.
// Inherited marks cross view boundaries; a stored local mark does not mark its
// enclosing join or siblings.
func validateQueryJoinExecution(c *EvalContext, node LogicalNode, catalog CatalogRead, inherited lock.Mode) error {
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
func validateMutationJoinExecution(c *EvalContext, b *binding, expressions [][]expr.Expr, catalog CatalogRead) error {
	owner := *c
	owner.Scope, owner.Frame = 1, 1
	validateTargets := func(targets []*arrayAssignmentTarget) error {
		for _, target := range targets {
			for path := target; path != nil; path = path.next {
				var storage [13]expr.Expr
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

func validateExpressionJoinExecution(c *EvalContext, expression expr.Expr, catalog CatalogRead) error {
	if expression == nil {
		return nil
	}
	recipe, err := expressionRecipeDescriptor(expression)
	if err != nil {
		return err
	}
	if recipe != nil && (!c.routineGraft || c.routineConsumer != recipe) || recipe == nil && c.routineGraft {
		local := *c
		if err := local.selectExpressionRecipe(recipe); err != nil {
			return err
		}
		c = &local
	}
	if err := c.Context.Err(); err != nil {
		return err
	}
	if err := admitRoutineCall(c, expression, true, false); err != nil {
		return err
	}
	if column, ok := expression.(columnExpression); ok && column.virtual != nil {
		// A virtual column is a recipe boundary, not a lexical child in the
		// enclosing query's frame or routine dependency manifest.
		return column.admitVirtualGeneration(c, catalog)
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
	boundExpressionChildren(expression, func(child expr.Expr) {
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
		validate := func(expression expr.Expr) {
			if failure == nil {
				failure = validateExpressionJoinExecution(c, expression, catalog)
			}
		}
		if source, ok := node.(*functionFromNode); ok {
			// Preflight preserves the analyzed call and schema. A proven dead
			// FROM path, like a scalar-NULL invocation, has no runtime call or
			// argument demand. Enclosing target expressions remain planned.
			if !emptyInput {
				for i, call := range source.calls {
					if source.scalarNull == nil || !source.scalarNull[i] {
						validate(call)
					}
				}
			}
		} else {
			queryExpressions(node, validate)
		}
		if failure != nil {
			return failure
		}
	}
	switch n := node.(type) {
	case *aggregateNode:
		if !n.presenceOnly {
			if err := admitQueryGroupings(c, n.grouping, n.keys); err != nil {
				return err
			}
			for _, call := range n.calls {
				if err := admitRoutineReference(c, call.routine, "aggregate"); err != nil {
					return err
				}
				if len(call.distinct) > 1 {
					keys := [2]expr.Expr{call.argument, call.separator}
					if err := admitQueryGroupings(c, call.distinct, keys[:len(call.distinct)]); err != nil {
						return err
					}
				}
			}
		}
	case *windowNode:
		for _, call := range n.calls {
			if call.analysisOnly {
				continue
			}
			if err := admitRoutineReference(c, call.routine, "function"); err != nil {
				return err
			}
		}
		for i, spec := range n.specs {
			if !n.specActive(i) {
				continue
			}
			if err := admitQueryGroupings(c, spec.grouping, spec.partition); err != nil {
				return err
			}
			for _, key := range spec.order {
				if privateTreeGrouping(key.expression.ResultUsage().Type) {
					continue
				}
				if err := admitRoutineReference(c, key.equalityRoutine, "function"); err != nil {
					return err
				}
			}
		}
	case *functionFromNode:
		if !emptyInput {
			for i, call := range n.calls {
				if n.scalarNull != nil && n.scalarNull[i] {
					continue
				}
				if err := admitRoutineCall(c, call, false, false); err != nil {
					return err
				}
			}
		}
	case *setProjectNode:
		// Target calls initialize even when the child's FROM input is dead.
		for _, call := range n.calls {
			if err := admitRoutineCall(c, call, false, false); err != nil {
				return err
			}
		}
	case *setNode, *recursiveUnionNode:
		// Inline inherited sets were rejected at analysis. A stored set
		// source is opaque to an outer row mark, but all arms still plan.
		set := querySetNode(node)
		if len(set.grouping) != 0 {
			groups := set.grouping
			var cycleMark expr.Expr
			if recursive, ok := node.(*recursiveUnionNode); ok && recursive.cycle != nil {
				// Seed expressions retain only user columns. CYCLE appends
				// its mark grouping separately; ancestry uses its own executor.
				cycleMark = recursive.cycle.mark
				groups = groups[:len(groups)-1]
			}
			if err := admitQueryGroupings(c, groups, set.leftExpressions); err != nil {
				return err
			}
			if cycleMark != nil {
				keys := [1]expr.Expr{cycleMark}
				if err := admitQueryGroupings(c, set.grouping[len(groups):], keys[:]); err != nil {
					return err
				}
			}
		}
		if err := validateMarkedQueryJoin(c, set.left, catalog, nil, queryRowMark{}, emptyInput, nil, reads); err != nil {
			return err
		}
		return validateMarkedQueryJoin(c, set.right, catalog, nil, queryRowMark{}, emptyInput, nil, reads)
	case *worktableScanNode:
		return nil
	case *sourceNode:
		mark := queryOccurrenceMark(marks, n.occurrence, inherited)
		if d, ok := catalog.Relation(n.ref.Address); ok && d.relationKind == viewRelation {
			if err := viewRowLockLegality(viewOf(d), queryRowDiagnostic(mark)); err != nil {
				return err
			}
			outputUses, err := querySourceReadUses(reads[n.occurrence], d.attributes)
			if err != nil {
				return err
			}
			child := *c
			child.quoteOrigin = quoteOrigin{owner: n.ref, definition: viewOf(d).rule, revision: viewOf(d).quoteRevision}
			child.routineChecks, child.routineReferences, child.routineConsumer = viewOf(d).dependencies, nil, d
			child.routineRecipe, child.routineGraft = true, false
			root, storedReads, err := prepareStoredExecutionRoot(&child, viewOf(d), outputUses)
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
	case *distinctNode:
		if inherited.mode != 0 {
			return sqlError("0A000", queryRowClause(queryRowDiagnostic(inherited))+" is not allowed with DISTINCT clause")
		}
		if !n.presenceOnly {
			if err := admitQueryGroupings(c, n.grouping, n.expressions); err != nil {
				return err
			}
		}
	case *distinctOnNode:
		if inherited.mode != 0 {
			return sqlError("0A000", queryRowClause(queryRowDiagnostic(inherited))+" is not allowed with DISTINCT clause")
		}
		if !n.presenceOnly {
			if err := admitQueryGroupings(c, n.grouping, n.expressions); err != nil {
				return err
			}
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
	left, right expr.Expr
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
func fullJoinCondition(c *EvalContext, join *joinNode, bound expr.Expr, negate bool) (fullJoinFacts, error) {
	if bound == nil {
		return fullJoinFacts{constant: true, truth: True}, nil
	}
	p := bound.Properties()
	// Pruning preserves analyzed dependencies even on a physical literal.
	// Those properties still govern metadata/ACLs, not its planning truth.
	_, literal := bound.(literalExpression)
	if literal || p.Volatility == expr.Immutable && !p.RowDependent && !p.StageDependent && !p.RuntimeDomainConversion {
		value, err := bound.Eval(c)
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
	if e, ok := bound.(logicalExpression); ok {
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
	if e, ok := bound.(caseExpression); ok {
		var test sqltype.Value
		if e.test != nil {
			p := e.test.Properties()
			if p.Volatility != expr.Immutable || p.RowDependent || p.StageDependent || p.RuntimeDomainConversion {
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
			if p.Volatility != expr.Immutable || p.RowDependent || p.StageDependent || p.RuntimeDomainConversion {
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
	if e, ok := bound.(conditionalExpression); ok && e.kind == parser.Coalesce {
		for _, arg := range e.args {
			p := arg.Properties()
			if p.Volatility != expr.Immutable || p.RowDependent || p.StageDependent || p.RuntimeDomainConversion {
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
	if e, ok := bound.(comparisonExpression); ok && fullJoinComparisonOperator(e.operator, negate) == "=" {
		left, err := joinExpressionSides(e.left, join.leftWidth, join.rightWidth)
		if err != nil {
			return fullJoinFacts{}, err
		}
		right, err := joinExpressionSides(e.right, join.leftWidth, join.rightWidth)
		if err != nil {
			return fullJoinFacts{}, err
		}
		if e.left.Properties().Volatility != expr.Volatile && e.right.Properties().Volatility != expr.Volatile && (left == 1 && right == 2 || left == 2 && right == 1) {
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
func joinExpressionSides(node expr.Expr, leftWidth, rightWidth int) (uint8, error) {
	if node == nil {
		return 0, nil
	}
	if e, ok := node.(columnExpression); ok {
		if e.slot < 0 || e.slot >= leftWidth+rightWidth {
			return 0, fmt.Errorf("stuntgres: invalid join expression slot")
		}
		if e.slot < leftWidth {
			return 1, nil
		}
		return 2, nil
	}
	var result uint8
	child := func(e expr.Expr) error {
		side, err := joinExpressionSides(e, leftWidth, rightWidth)
		result |= side
		return err
	}
	var children []expr.Expr
	switch e := node.(type) {
	case literalExpression, parameterExpression, identityExpression:
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
			for _, child := range [2]expr.Expr{s.lower, s.upper} {
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
	case selectedOperatorExpression:
		return joinExpressionSides(e.provider, leftWidth, rightWidth)
	case arithmeticExpression:
		children = []expr.Expr{e.left, e.right}
	case bitwiseExpression:
		children = []expr.Expr{e.left, e.right}
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
		children = []expr.Expr{e.left, e.right}
	case logicalExpression:
		children = []expr.Expr{e.left, e.right}
	case distinctExpression:
		children = []expr.Expr{e.left, e.right}
	case likeExpression:
		children = []expr.Expr{e.left, e.right}
	case seriesExpression:
		children = e.args
	case subscriptsExpression:
		children = e.args
	case unnestExpression:
		children = e.args
	case nullTestExpression:
		children = []expr.Expr{e.child}
	case truthTestExpression:
		children = []expr.Expr{e.child}
	case conditionalExpression:
		children = e.args
	case catalogUserNameExpression:
		return joinExpressionSides(e.arg, leftWidth, rightWidth)
	case catalogForeignKeysExpression:
		return 0, nil
	case transactionIdentityExpression:
		if e.arg == nil {
			return 0, nil
		}
		children = []expr.Expr{e.arg}
	case commitTimestampExpression:
		if e.arg == nil {
			return 0, nil
		}
		return joinExpressionSides(e.arg, leftWidth, rightWidth)
	case advisoryExpression:
		children = e.args
	case sequenceCallExpression:
		children = e.args
	case indexPropertyExpression:
		children = e.children()
	case deparseExpression:
		children = e.args
	case commentInquiryExpression:
		children = e.args
	case installedSoundexFunctionExpression:
		children = e.Args
	case installedMetaphoneFunctionExpression:
		children = e.Args
	case installedLevenshteinFunctionExpression:
		children = e.Args
	case installedUUIDOsspFunctionExpression:
		// No argument children.
	case installedCryptoFunctionExpression:
		children = e.Args
	case installedFunctionExpression:
		children = []expr.Expr{e.Arg}
	case currentSchemasExpression:
		children = []expr.Expr{e.arg}
	case objectAddressExpression:
		children = e.args[:]
	case aclValueExpression:
		children = e.arguments()
	case objectACLExpression:
		children = e.args[:]
	case accessInquiryExpression:
		children = e.arguments()
	case namespaceInquiryExpression:
		children = e.arguments()
	case blockingInquiryExpression:
		children = e.arguments()
	case visibilityInquiryExpression:
		children = []expr.Expr{e.arg}
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
	case expr.Composite:
		// Last, so the explicit cases above keep precedence over embedded nodes.
		children = e.Children()
	default:
		return 0, fmt.Errorf("stuntgres: unsupported join dependency expression %T", node)
	}
	for _, e := range children {
		if err := child(e); err != nil {
			return 0, err
		}
	}
	return result, nil
}
