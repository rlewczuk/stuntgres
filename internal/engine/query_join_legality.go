package engine

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

// Planning-only restrictions run on execution-owned parameters, never Prepare.
// Inherited marks cross view boundaries; a stored local mark does not mark its
// enclosing join or siblings.
func validateQueryJoinExecution(c *exec.Context, node plan.Node, catalog catalog.Read, inherited lock.Mode) error {
	reads := analyze.QueryExecutionSourceReads(node, nil)
	if source, ok := node.(*plan.SourceNode); ok {
		reads[source.Occurrence] = nil
	}
	return validateMarkedQueryJoin(c, node, catalog, nil, plan.RowMark{Mode: inherited}, false, nil, reads)
}

// Mutation targets and RETURNING are planned even when their input selects no
// rows. Walk only the execution-owned expressions that the binding consumes.
func validateMutationJoinExecution(c *exec.Context, b *analyze.Binding, expressions [][]expr.Expr, catalog catalog.Read) error {
	owner := *c
	owner.Scope, owner.Frame = 1, 1
	validateTargets := func(targets []*plan.ArrayAssignmentTarget) error {
		for _, target := range targets {
			for path := target; path != nil; path = path.Next {
				var storage [13]expr.Expr
				for _, child := range plan.SubscriptChildren(path.Node, &storage) {
					if err := validateExpressionJoinExecution(&owner, child, catalog); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	if err := validateTargets(b.ArrayTargets); err != nil {
		return err
	}
	action := b.Update
	if b.Insert.Conflict != nil && b.Insert.Conflict.Update != nil {
		action = b.Insert.Conflict.Update
	}
	if action != nil {
		if err := validateTargets(action.ArrayTargets); err != nil {
			return err
		}
		for _, expression := range action.Assignments {
			if err := validateExpressionJoinExecution(&owner, expression, catalog); err != nil {
				return err
			}
		}
		if err := validateExpressionJoinExecution(&owner, action.Predicate, catalog); err != nil {
			return err
		}
	} else if err := validateExpressionJoinExecution(&owner, b.Predicate, catalog); err != nil {
		return err
	}
	for _, row := range expressions {
		for j, expression := range row {
			if len(expressions) == 1 && len(b.Insert.IdentityDiscard) != 0 && b.Insert.IdentityDiscard[b.Targets[j]] {
				continue
			}
			if err := validateExpressionJoinExecution(&owner, expression, catalog); err != nil {
				return err
			}
		}
	}
	if b.Returning != nil {
		for _, expression := range b.Returning.Expressions {
			if err := validateExpressionJoinExecution(&owner, expression, catalog); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateExpressionJoinExecution(c *exec.Context, expression expr.Expr, catalog catalog.Read) error {
	if expression == nil {
		return nil
	}
	recipe, err := exec.ExpressionRecipeDescriptor(expression)
	if err != nil {
		return err
	}
	if recipe != nil && (!c.RoutineGraft || c.RoutineConsumer != recipe) || recipe == nil && c.RoutineGraft {
		local := *c
		if err := exec.SelectExpressionRecipe(&local, recipe); err != nil {
			return err
		}
		c = &local
	}
	if err := c.Context.Err(); err != nil {
		return err
	}
	if err := exec.AdmitRoutineCall(c, expression, true, false); err != nil {
		return err
	}
	if column, ok := expression.(plan.ColumnExpr); ok && column.Virtual != nil {
		// A virtual column is a recipe boundary, not a lexical child in the
		// enclosing query's frame or routine dependency manifest.
		return admitColumnVirtualGeneration(c, column, catalog)
	}
	if subquery, ok := expression.(plan.SubqueryExpr); ok {
		project := subquery.QueryPlan.Root.(*plan.ProjectNode)
		child := *c
		child.Scope, child.Frame = 1, project.Frame
		child.Row, child.OrderValues, child.Outer = nil, nil, c
		var ignored *plan.ProjectNode
		if subquery.PresenceOnly {
			ignored = project
		}
		if err := validateMarkedQueryJoin(&child, project, catalog, nil, plan.RowMark{}, false, ignored, analyze.QueryExecutionSourceReads(project, ignored)); err != nil {
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
	plan.ExpressionChildren(expression, func(child expr.Expr) {
		if failure == nil {
			failure = validateExpressionJoinExecution(c, child, catalog)
		}
	})
	return failure
}
func validateMarkedQueryJoin(c *exec.Context, node plan.Node, view catalog.Read, marks []plan.RowMark, inherited plan.RowMark, emptyInput bool, ignoredProject *plan.ProjectNode, reads map[int][]int32) error {

	if node == nil {
		return nil
	}
	if err := c.Context.Err(); err != nil {
		return err
	}
	// Target subplans are planned even when their enclosing input is empty.
	// Eligible EXISTS retains their graph, but does not plan ignored output.
	if node != ignoredProject && !analyze.QueryIgnoresOwnExpressions(node) {
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
			plan.QueryExpressions(node, validate)
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
				if err := exec.AdmitRoutineReference(c, call.Routine, "aggregate"); err != nil {
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
			if err := exec.AdmitRoutineReference(c, call.Routine, "function"); err != nil {
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
				if analyze.PrivateTreeGrouping(key.Expression.ResultUsage().Type) {
					continue
				}
				if err := exec.AdmitRoutineReference(c, key.EqualityRoutine, "function"); err != nil {
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
				if err := exec.AdmitRoutineCall(c, call, false, false); err != nil {
					return err
				}
			}
		}
	case *plan.SetProjectNode:
		// Target calls initialize even when the child's FROM input is dead.
		for _, call := range n.Calls {
			if err := exec.AdmitRoutineCall(c, call, false, false); err != nil {
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
		mark := analyze.QueryOccurrenceMark(marks, n.Occurrence, inherited)
		if d, ok := view.Relation(n.Ref.Address); ok && d.RelationKind == catalog.ViewRelation {
			if err := viewRowLockLegality(viewOf(d), analyze.RowLockDiagnostic(mark)); err != nil {
				return err
			}
			outputUses, err := analyze.QuerySourceReadUses(reads[n.Occurrence], d.Attributes)
			if err != nil {
				return err
			}
			child := *c
			child.QuoteOrigin = exec.QuoteOrigin{Owner: n.Ref, Definition: viewOf(d).RuleOID(), Revision: viewOf(d).QuoteRevision()}
			child.RoutineChecks, child.RoutineReferences, child.RoutineConsumer = viewOf(d).Dependencies(), nil, d
			child.RoutineRecipe, child.RoutineGraft = true, false
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
		child.Row, child.OrderValues, child.Outer = nil, nil, c.Outer
		root, err := preflightQuery(&child, project)
		if err != nil {
			return err
		}
		return validateMarkedQueryJoin(&child, root, view, marks, inherited, emptyInput, nil, reads)
	case *plan.JoinNode:
		mark := plan.RowMark{}
		if n.Kind == parser.JoinLeft || n.Kind == parser.JoinFull {
			mark = analyze.NullableQueryRowMark(n.Right, marks, inherited)
		}
		if n.Kind == parser.JoinRight || n.Kind == parser.JoinFull {
			mark = analyze.FirstNullableQueryMark(mark, analyze.NullableQueryRowMark(n.Left, marks, inherited))
		}
		if mark.Mode != 0 {
			return sqlError("0A000", analyze.RowLockClause(mark.Mode)+" cannot be applied to the nullable side of an outer join")
		}
		if n.Kind == parser.JoinFull && !emptyInput {
			facts, err := analyze.FullJoinCondition(c, n, n.Predicate, false)
			if err != nil {
				return err
			}
			if !facts.Constant && len(facts.Keys) == 0 {
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
				local[i] = exec.MergeQueryRowMark(local[i], mark)
			}
			marks = local
		}
	case *plan.FilterNode:
		// Prune only this input's proven dead FROM path, using this execution's
		// parameter frame. Nullable rowmark legality still applies.
		if !emptyInput {
			rejects, err := exec.QueryPredicateRejectsAll(c, n.Predicate)
			if err != nil {
				return err
			}
			emptyInput = rejects
		}
	case *plan.DistinctNode:
		if inherited.Mode != 0 {
			return sqlError("0A000", analyze.RowLockClause(analyze.RowLockDiagnostic(inherited))+" is not allowed with DISTINCT clause")
		}
		if !n.PresenceOnly {
			if err := admitQueryGroupings(c, n.Grouping, n.Expressions); err != nil {
				return err
			}
		}
	case *plan.DistinctOnNode:
		if inherited.Mode != 0 {
			return sqlError("0A000", analyze.RowLockClause(analyze.RowLockDiagnostic(inherited))+" is not allowed with DISTINCT clause")
		}
		if !n.PresenceOnly {
			if err := admitQueryGroupings(c, n.Grouping, n.Expressions); err != nil {
				return err
			}
		}
	}
	left, right := plan.QueryInputs(node)
	if err := validateMarkedQueryJoin(c, left, view, marks, inherited, emptyInput, ignoredProject, reads); err != nil {
		return err
	}
	return validateMarkedQueryJoin(c, right, view, marks, inherited, emptyInput, ignoredProject, reads)
}

// Query equality is caller-checked; index/hash support and native semantic-tree
// grouping are deliberately not callable PostgreSQL routines.
func admitQueryGroupings(c *exec.Context, groups []plan.Grouping, keys []expr.Expr) error {
	if len(groups) != len(keys) {
		return fmt.Errorf("stuntgres: missing query grouping identity")
	}
	for i, group := range groups {
		private := analyze.PrivateTreeGrouping(keys[i].ResultUsage().Type)
		if private != group.PrivateTree {
			return fmt.Errorf("stuntgres: changed query grouping capability")
		}
		if private {
			if group.Routine.Address.ObjectID != 0 {
				return fmt.Errorf("stuntgres: native tree grouping has a callable identity")
			}
			continue
		}
		if err := exec.AdmitRoutineReference(c, group.Routine, "function"); err != nil {
			return err
		}
	}
	return nil
}
