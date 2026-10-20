package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Planning-only restrictions run on execution-owned parameters, never Prepare.
// Inherited marks cross view boundaries; a stored local mark does not mark its
// enclosing join or siblings.
func validateQueryJoinExecution(c *EvalContext, node plan.Node, catalog catalog.Read, inherited lock.Mode) error {
	reads := queryExecutionSourceReads(node, nil)
	if source, ok := node.(*plan.SourceNode); ok {
		reads[source.Occurrence] = nil
	}
	return validateMarkedQueryJoin(c, node, catalog, nil, plan.RowMark{Mode: inherited}, false, nil, reads)
}

func firstNullableQueryMark(a, b plan.RowMark) plan.RowMark {
	if a.Mode == 0 {
		return b
	}
	if b.Mode != 0 && b.Order < a.Order {
		return b
	}
	return a
}

func nullableQueryRowMark(node plan.Node, marks []plan.RowMark, inherited plan.RowMark) plan.RowMark {
	switch n := node.(type) {
	case *plan.SourceNode:
		return queryOccurrenceMark(marks, n.Occurrence, inherited)
	case *plan.DerivedNode:
		if n.CTE != nil {
			return plan.RowMark{}
		}
		return nullableQueryRowMark(n.Query.Root, marks, inherited)
	case *plan.JoinNode:
		return firstNullableQueryMark(nullableQueryRowMark(n.Left, marks, inherited), nullableQueryRowMark(n.Right, marks, inherited))
	case *plan.RowLockNode:
		return nullableQueryRowMark(n.Child, marks, inherited)
	case *plan.LimitNode:
		return nullableQueryRowMark(n.Child, marks, inherited)
	case *plan.EvaluationNode:
		return nullableQueryRowMark(n.Child, marks, inherited)
	case *plan.FilterNode:
		return nullableQueryRowMark(n.Child, marks, inherited)
	case *plan.ProjectNode:
		return nullableQueryRowMark(n.Child, marks, inherited)
	case *plan.SortNode:
		return nullableQueryRowMark(n.Child, marks, inherited)
	case *plan.AggregateNode:
		return nullableQueryRowMark(n.Child, marks, inherited)
	case *plan.SetProjectNode:
		return nullableQueryRowMark(n.Child, marks, inherited)
	}
	return plan.RowMark{}
}

// Mutation targets and RETURNING are planned even when their input selects no
// rows. Walk only the execution-owned expressions that the binding consumes.
func validateMutationJoinExecution(c *EvalContext, b *binding, expressions [][]expr.Expr, catalog catalog.Read) error {
	owner := *c
	owner.Scope, owner.Frame = 1, 1
	validateTargets := func(targets []*plan.ArrayAssignmentTarget) error {
		for _, target := range targets {
			for path := target; path != nil; path = path.Next {
				var storage [13]expr.Expr
				for _, child := range subscriptChildren(path.Node, &storage) {
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
	if b.insert.conflict != nil && b.insert.conflict.update != nil {
		action = b.insert.conflict.update
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
			if len(expressions) == 1 && len(b.insert.identityDiscard) != 0 && b.insert.identityDiscard[b.targets[j]] {
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

func validateExpressionJoinExecution(c *EvalContext, expression expr.Expr, catalog catalog.Read) error {
	if expression == nil {
		return nil
	}
	recipe, err := routine.ExpressionRecipeDescriptor(expression)
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
	if column, ok := expression.(plan.ColumnExpr); ok && column.Virtual != nil {
		// A virtual column is a recipe boundary, not a lexical child in the
		// enclosing query's frame or routine dependency manifest.
		return c.admitColumnVirtualGeneration(column, catalog)
	}
	if subquery, ok := expression.(plan.SubqueryExpr); ok {
		project := subquery.QueryPlan.Root.(*plan.ProjectNode)
		child := *c
		child.Scope, child.Frame = 1, project.Frame
		child.Row, child.orderValues, child.Outer = nil, nil, c
		var ignored *plan.ProjectNode
		if subquery.PresenceOnly {
			ignored = project
		}
		if err := validateMarkedQueryJoin(&child, project, catalog, nil, plan.RowMark{}, false, ignored, queryExecutionSourceReads(project, ignored)); err != nil {
			return err
		}
	}
	if logical, ok := expression.(plan.LogicalExpr); ok {
		if err := validateExpressionJoinExecution(c, logical.Left, catalog); err != nil {
			return err
		}
		if logical.QueryRight {
			_, decisive, err := plan.DecisiveLogicalValue(&logical)
			if err != nil || decisive {
				return err
			}
		}
		return validateExpressionJoinExecution(c, logical.Right, catalog)
	}
	var failure error
	boundExpressionChildren(expression, func(child expr.Expr) {
		if failure == nil {
			failure = validateExpressionJoinExecution(c, child, catalog)
		}
	})
	return failure
}
func validateMarkedQueryJoin(c *EvalContext, node plan.Node, view catalog.Read, marks []plan.RowMark, inherited plan.RowMark, emptyInput bool, ignoredProject *plan.ProjectNode, reads map[int][]int32) error {

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
				failure = validateExpressionJoinExecution(c, expression, view)
			}
		}
		if source, ok := node.(*plan.FunctionFromNode); ok {
			// Preflight preserves the analyzed call and schema. A proven dead
			// FROM path, like a scalar-NULL invocation, has no runtime call or
			// argument demand. Enclosing target expressions remain planned.
			if !emptyInput {
				for i, call := range source.Calls {
					if source.ScalarNull == nil || !source.ScalarNull[i] {
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
	case *plan.AggregateNode:
		if !n.PresenceOnly {
			if err := admitQueryGroupings(c, n.Grouping, n.Keys); err != nil {
				return err
			}
			for _, call := range n.Calls {
				if err := admitRoutineReference(c, call.Routine, "aggregate"); err != nil {
					return err
				}
				if len(call.Distinct) > 1 {
					keys := [2]expr.Expr{call.Argument, call.Separator}
					if err := admitQueryGroupings(c, call.Distinct, keys[:len(call.Distinct)]); err != nil {
						return err
					}
				}
			}
		}
	case *plan.WindowNode:
		for _, call := range n.Calls {
			if call.AnalysisOnly {
				continue
			}
			if err := admitRoutineReference(c, call.Routine, "function"); err != nil {
				return err
			}
		}
		for i, spec := range n.Specs {
			if !n.SpecActive(i) {
				continue
			}
			if err := admitQueryGroupings(c, spec.Grouping, spec.Partition); err != nil {
				return err
			}
			for _, key := range spec.Order {
				if privateTreeGrouping(key.Expression.ResultUsage().Type) {
					continue
				}
				if err := admitRoutineReference(c, key.EqualityRoutine, "function"); err != nil {
					return err
				}
			}
		}
	case *plan.FunctionFromNode:
		if !emptyInput {
			for i, call := range n.Calls {
				if n.ScalarNull != nil && n.ScalarNull[i] {
					continue
				}
				if err := admitRoutineCall(c, call, false, false); err != nil {
					return err
				}
			}
		}
	case *plan.SetProjectNode:
		// Target calls initialize even when the child's FROM input is dead.
		for _, call := range n.Calls {
			if err := admitRoutineCall(c, call, false, false); err != nil {
				return err
			}
		}
	case *plan.SetNode, *plan.RecursiveUnionNode:
		// Inline inherited sets were rejected at analysis. A stored set
		// source is opaque to an outer row mark, but all arms still plan.
		set := querySetNode(node)
		if len(set.Grouping) != 0 {
			groups := set.Grouping
			var cycleMark expr.Expr
			if recursive, ok := node.(*plan.RecursiveUnionNode); ok && recursive.Cycle != nil {
				// Seed expressions retain only user columns. CYCLE appends
				// its mark grouping separately; ancestry uses its own executor.
				cycleMark = recursive.Cycle.Mark
				groups = groups[:len(groups)-1]
			}
			if err := admitQueryGroupings(c, groups, set.LeftExpressions); err != nil {
				return err
			}
			if cycleMark != nil {
				keys := [1]expr.Expr{cycleMark}
				if err := admitQueryGroupings(c, set.Grouping[len(groups):], keys[:]); err != nil {
					return err
				}
			}
		}
		if err := validateMarkedQueryJoin(c, set.Left, view, nil, plan.RowMark{}, emptyInput, nil, reads); err != nil {
			return err
		}
		return validateMarkedQueryJoin(c, set.Right, view, nil, plan.RowMark{}, emptyInput, nil, reads)
	case *plan.WorktableScanNode:
		return nil
	case *plan.SourceNode:
		mark := queryOccurrenceMark(marks, n.Occurrence, inherited)
		if d, ok := view.Relation(n.Ref.Address); ok && d.RelationKind == catalog.ViewRelation {
			if err := viewRowLockLegality(viewOf(d), queryRowDiagnostic(mark)); err != nil {
				return err
			}
			outputUses, err := querySourceReadUses(reads[n.Occurrence], d.Attributes)
			if err != nil {
				return err
			}
			child := *c
			child.quoteOrigin = quoteOrigin{owner: n.Ref, definition: viewOf(d).rule, revision: viewOf(d).quoteRevision}
			child.routineChecks, child.routineReferences, child.routineConsumer = viewOf(d).dependencies, nil, d
			child.routineRecipe, child.routineGraft = true, false
			root, storedReads, err := prepareStoredExecutionRoot(&child, viewOf(d), outputUses)
			if err != nil {
				return err
			}
			return validateMarkedQueryJoin(&child, root, view, nil, mark, emptyInput, nil, storedReads)
		}
		return nil
	case *plan.DerivedNode:
		if n.CTE != nil && n.CTE.Mutation != nil {
			return nil
		}
		if n.CTE != nil {
			marks = nil
			inherited = plan.RowMark{}
		}
		project, ok := n.Query.Root.(*plan.ProjectNode)
		if !ok {
			return fmt.Errorf("stuntgres: missing derived query frame")
		}
		child := *c
		child.Scope, child.Frame = 1, project.Frame
		child.Row, child.orderValues, child.Outer = nil, nil, c.Outer
		root, err := preflightQuery(&child, project)
		if err != nil {
			return err
		}
		return validateMarkedQueryJoin(&child, root, view, marks, inherited, emptyInput, nil, reads)
	case *plan.JoinNode:
		mark := plan.RowMark{}
		if n.Kind == parser.JoinLeft || n.Kind == parser.JoinFull {
			mark = nullableQueryRowMark(n.Right, marks, inherited)
		}
		if n.Kind == parser.JoinRight || n.Kind == parser.JoinFull {
			mark = firstNullableQueryMark(mark, nullableQueryRowMark(n.Left, marks, inherited))
		}
		if mark.Mode != 0 {
			return sqlError("0A000", queryRowClause(mark.Mode)+" cannot be applied to the nullable side of an outer join")
		}
		if n.Kind == parser.JoinFull && !emptyInput {
			facts, err := fullJoinCondition(c, n, n.Predicate, false)
			if err != nil {
				return err
			}
			if !facts.constant && len(facts.keys) == 0 {
				return sqlError("0A000", "FULL JOIN is only supported with merge-joinable or hash-joinable join conditions")
			}
		}
	case *plan.RowLockNode:
		if len(marks) == 0 {
			marks = n.Marks
		} else {
			width := len(n.Marks)
			if len(marks) > width {
				width = len(marks)
			}
			local := make([]plan.RowMark, width)
			copy(local, marks)
			for i, mark := range n.Marks {
				local[i] = mergeQueryRowMark(local[i], mark)
			}
			marks = local
		}
	case *plan.FilterNode:
		// Prune only this input's proven dead FROM path, using this execution's
		// parameter frame. Nullable rowmark legality still applies.
		if !emptyInput {
			rejects, err := queryPredicateRejectsAll(c, n.Predicate)
			if err != nil {
				return err
			}
			emptyInput = rejects
		}
	case *plan.DistinctNode:
		if inherited.Mode != 0 {
			return sqlError("0A000", queryRowClause(queryRowDiagnostic(inherited))+" is not allowed with DISTINCT clause")
		}
		if !n.PresenceOnly {
			if err := admitQueryGroupings(c, n.Grouping, n.Expressions); err != nil {
				return err
			}
		}
	case *plan.DistinctOnNode:
		if inherited.Mode != 0 {
			return sqlError("0A000", queryRowClause(queryRowDiagnostic(inherited))+" is not allowed with DISTINCT clause")
		}
		if !n.PresenceOnly {
			if err := admitQueryGroupings(c, n.Grouping, n.Expressions); err != nil {
				return err
			}
		}
	}
	left, right := queryInputs(node)
	if err := validateMarkedQueryJoin(c, left, view, marks, inherited, emptyInput, ignoredProject, reads); err != nil {
		return err
	}
	return validateMarkedQueryJoin(c, right, view, marks, inherited, emptyInput, ignoredProject, reads)
}

// Every key is an effective equality. Keep only its ordered, bound operands:
// the original predicate/provider remains untouched, including under NOT.
type fullJoinKey struct {
	left, right expr.Expr
}

type fullJoinFacts struct {
	constant bool
	truth    plan.Truth
	keys     []fullJoinKey
}

// Keys are actual conjuncts, not merely equality syntax somewhere in the tree.
// OR retains only structurally common keys in the same operand order. NOT is
// pushed through Boolean nodes with SQL three-valued truth. At the resulting
// match boundary, FALSE and UNKNOWN both reject a pair.
func fullJoinCondition(c *EvalContext, join *plan.JoinNode, bound expr.Expr, negate bool) (fullJoinFacts, error) {
	if bound == nil {
		return fullJoinFacts{constant: true, truth: plan.True}, nil
	}
	p := bound.Properties()
	// Pruning preserves analyzed dependencies even on a physical literal.
	// Those properties still govern metadata/ACLs, not its planning truth.
	_, literal := bound.(plan.LiteralExpr)
	if literal || p.Volatility == expr.Immutable && !p.RowDependent && !p.StageDependent && !p.RuntimeDomainConversion {
		value, err := bound.Eval(c)
		if err != nil {
			return fullJoinFacts{}, err
		}
		truth, err := plan.TruthOf(value)
		if negate && truth != plan.Unknown {
			if truth == plan.True {
				truth = plan.False
			} else {
				truth = plan.True
			}
		}
		return fullJoinFacts{constant: true, truth: truth}, err
	}
	if e, ok := bound.(plan.LogicalExpr); ok {
		if e.Op == parser.LogicalNot {
			return fullJoinCondition(c, join, e.Left, !negate)
		}
		a, err := fullJoinCondition(c, join, e.Left, negate)
		if err != nil {
			return fullJoinFacts{}, err
		}
		b, err := fullJoinCondition(c, join, e.Right, negate)
		if err != nil {
			return fullJoinFacts{}, err
		}
		and := e.Op == parser.LogicalAnd
		if negate {
			and = !and
		}
		if a.constant && b.constant {
			truth := plan.Unknown
			if and {
				if a.truth == plan.False || b.truth == plan.False {
					truth = plan.False
				} else if a.truth == plan.True && b.truth == plan.True {
					truth = plan.True
				}
			} else {
				if a.truth == plan.True || b.truth == plan.True {
					truth = plan.True
				} else if a.truth == plan.False && b.truth == plan.False {
					truth = plan.False
				}
			}
			return fullJoinFacts{constant: true, truth: truth}, nil
		}
		if and {
			if a.constant && a.truth == plan.False || b.constant && b.truth == plan.False {
				return fullJoinFacts{constant: true, truth: plan.False}, nil
			}
			if a.constant && a.truth == plan.True {
				return b, nil
			}
			if b.constant && b.truth == plan.True {
				return a, nil
			}
			return fullJoinFacts{keys: append(a.keys, b.keys...)}, nil
		}
		if a.constant && a.truth == plan.True || b.constant && b.truth == plan.True {
			return fullJoinFacts{constant: true, truth: plan.True}, nil
		}
		if a.constant && a.truth != plan.True {
			return b, nil
		}
		if b.constant && b.truth != plan.True {
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
	if e, ok := bound.(plan.CaseExpr); ok {
		var test sqltype.Value
		if e.Test != nil {
			p := e.Test.Properties()
			if p.Volatility != expr.Immutable || p.RowDependent || p.StageDependent || p.RuntimeDomainConversion {
				return fullJoinFacts{}, nil
			}
			var err error
			test, err = e.Test.Eval(c)
			if err != nil {
				return fullJoinFacts{}, err
			}
		}
		for _, arm := range e.Arms {
			p := arm.Condition.Properties()
			if p.Volatility != expr.Immutable || p.RowDependent || p.StageDependent || p.RuntimeDomainConversion {
				return fullJoinFacts{}, nil
			}
			condition, err := arm.Condition.Eval(c)
			if err != nil {
				return fullJoinFacts{}, err
			}
			match := false
			if e.Test == nil {
				truth, err := plan.TruthOf(condition)
				if err != nil {
					return fullJoinFacts{}, err
				}
				match = truth == plan.True
			} else {
				if arm.TestPlan.ChecksDomain() {
					return fullJoinFacts{}, nil
				}
				view, err := arm.TestPlan.Apply(c.Context, test, c.Input, c.Output)
				if err != nil {
					return fullJoinFacts{}, err
				}
				if !view.IsNull() && !condition.IsNull() {
					equal, err := arm.Equality.EqualValues(c.Context, view, condition, c.Input)
					if err != nil {
						return fullJoinFacts{}, err
					}
					match = equal
				}
			}
			if match {
				return fullJoinCondition(c, join, arm.Result, negate)
			}
		}
		return fullJoinCondition(c, join, e.Otherwise, negate)
	}
	if e, ok := bound.(plan.ConditionalExpr); ok && e.Kind == parser.Coalesce {
		for _, arg := range e.Args {
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
	if e, ok := bound.(plan.ComparisonExpr); ok && fullJoinComparisonOperator(e.Operator, negate) == "=" {
		left, err := joinExpressionSides(e.Left, join.LeftWidth, join.RightWidth)
		if err != nil {
			return fullJoinFacts{}, err
		}
		right, err := joinExpressionSides(e.Right, join.LeftWidth, join.RightWidth)
		if err != nil {
			return fullJoinFacts{}, err
		}
		if e.Left.Properties().Volatility != expr.Volatile && e.Right.Properties().Volatility != expr.Volatile && (left == 1 && right == 2 || left == 2 && right == 1) {
			return fullJoinFacts{keys: []fullJoinKey{{left: e.Left, right: e.Right}}}, nil
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
	if e, ok := node.(plan.ColumnExpr); ok {
		if e.Slot < 0 || e.Slot >= leftWidth+rightWidth {
			return 0, fmt.Errorf("stuntgres: invalid join expression slot")
		}
		if e.Slot < leftWidth {
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
	case plan.LiteralExpr, plan.ParameterExpr:
		return 0, nil
	case plan.RowExpr:
		children = e.Fields
	case plan.FieldExpr:
		return joinExpressionSides(e.Base, leftWidth, rightWidth)
	case plan.ArrayExpr:
		children = e.Elements
	case plan.SubscriptExpr:
		sides, err := joinExpressionSides(e.Base, leftWidth, rightWidth)
		if err != nil {
			return 0, err
		}
		for _, s := range e.Subscripts {
			for _, child := range [2]expr.Expr{s.Lower, s.Upper} {
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
	case plan.CoercionExpr:
		return joinExpressionSides(e.Child, leftWidth, rightWidth)
	case plan.CollationExpr:
		return joinExpressionSides(e.Child, leftWidth, rightWidth)
	case plan.SelectedOperatorExpr:
		return joinExpressionSides(e.Provider, leftWidth, rightWidth)
	case plan.ArithmeticExpr:
		children = []expr.Expr{e.Left, e.Right}
	case plan.BitwiseExpr:
		children = []expr.Expr{e.Left, e.Right}
	case plan.RowComparisonExpr:
		var sides uint8
		for _, pair := range e.Comparisons {
			left, err := joinExpressionSides(pair.Left, leftWidth, rightWidth)
			if err != nil {
				return 0, err
			}
			right, err := joinExpressionSides(pair.Right, leftWidth, rightWidth)
			if err != nil {
				return 0, err
			}
			sides |= left | right
		}
		return sides, nil
	case plan.ScalarArrayComparisonExpr:
		sides, err := joinExpressionSides(e.Left, leftWidth, rightWidth)
		if err != nil {
			return 0, err
		}
		if e.Array != nil {
			side, err := joinExpressionSides(e.Array, leftWidth, rightWidth)
			if err != nil {
				return 0, err
			}
			sides |= side
		}
		for _, item := range e.Items {
			side, err := joinExpressionSides(item, leftWidth, rightWidth)
			if err != nil {
				return 0, err
			}
			sides |= side
		}
		return sides, nil
	case plan.ComparisonExpr:
		children = []expr.Expr{e.Left, e.Right}
	case plan.LogicalExpr:
		children = []expr.Expr{e.Left, e.Right}
	case plan.DistinctExpr:
		children = []expr.Expr{e.Left, e.Right}
	case plan.LikeExpr:
		children = []expr.Expr{e.Left, e.Right}
	case plan.NullTestExpr:
		children = []expr.Expr{e.Child}
	case plan.TruthTestExpr:
		children = []expr.Expr{e.Child}
	case plan.ConditionalExpr:
		children = e.Args
	case extensionPathsExpression:
		return joinExpressionSides(e.Arg, leftWidth, rightWidth)
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
	case plan.CaseExpr:
		if err := child(e.Test); err != nil {
			return 0, err
		}
		if err := child(e.Otherwise); err != nil {
			return 0, err
		}
		for _, arm := range e.Arms {
			if err := child(arm.Condition); err != nil {
				return 0, err
			}
			if err := child(arm.Result); err != nil {
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
