package engine

import "stuntgres/internal/parser"

// PostgreSQL checks the action's own WITH and INSERT source SELECT's WITH,
// not WITHs in nested subqueries or the enclosing statement. Position selects
// the already resolved definition recipe; SQL names are never rebound here.
func validateRuleActionCTEs(action parser.Statement, b binding) error {
	direct := parser.StatementWith(action)
	var source []parser.CommonTableExpression
	if insert, ok := action.(*parser.Insert); ok && insert.Source != nil && insert.Source.Select != nil {
		source = insert.Source.Select.With
	}
	if len(direct) == 0 && len(source) == 0 {
		return nil
	}
	owned := func(position parser.Position) bool {
		for _, definition := range direct {
			if definition.Name.Pos == position {
				return true
			}
		}
		for _, definition := range source {
			if definition.Name.Pos == position {
				return true
			}
		}
		return false
	}
	old, new := false, false
	var inspectQuery func(LogicalNode)
	sides := func(expr BoundExpr) {
		if expr == nil {
			return
		}
		walkBoundExpression(expr, func(node BoundExpr) {
			switch node := node.(type) {
			case columnExpression:
				if node.scope == rulePseudoScope {
					old = old || node.frame == 1
					new = new || node.frame == 2
				}
			case rowExpression:
				old = old || node.ruleSide == 1
				new = new || node.ruleSide == 2
			}
		})
	}
	inspectQuery = func(root LogicalNode) { visitCTEQuery(root, func(LogicalNode) {}, sides, true) }
	inspectExpression := func(expr BoundExpr) {
		if expr == nil {
			return
		}
		sides(expr)
		walkBoundExpression(expr, func(node BoundExpr) {
			if query, ok := node.(subqueryExpression); ok {
				inspectQuery(query.queryPlan.Root)
			}
		})
	}
	inspectTargets := func(targets []*arrayAssignmentTarget) {
		for _, target := range targets {
			for current := target; current != nil; current = current.next {
				inspectExpression(current.node)
			}
		}
	}
	inspectBinding := func(body *binding) {
		inspectTargets(body.arrayTargets)
		if body.query != nil {
			inspectQuery(body.query.Root)
		}
		for _, row := range body.expressions {
			for _, expr := range row {
				inspectExpression(expr)
			}
		}
		for _, expr := range body.defaults {
			inspectExpression(expr)
		}
		inspectExpression(body.predicate)
		inspectQuery(body.deleteUsing)
		if body.update != nil {
			inspectQuery(body.update.from)
			inspectExpression(body.update.predicate)
			inspectTargets(body.update.arrayTargets)
			for _, expr := range body.update.assignments {
				inspectExpression(expr)
			}
		}
		if body.returning != nil {
			for _, expr := range body.returning.expressions {
				inspectExpression(expr)
			}
		}
		if body.conflict != nil {
			for _, key := range body.conflict.keys {
				inspectExpression(key.expression)
			}
			inspectExpression(body.conflict.predicate)
			if body.conflict.update != nil {
				inspectTargets(body.conflict.update.arrayTargets)
				inspectExpression(body.conflict.update.predicate)
				for _, expr := range body.conflict.update.assignments {
					inspectExpression(expr)
				}
			}
			if body.conflict.returning != nil && body.conflict.returning != body.returning {
				for _, expr := range body.conflict.returning.expressions {
					inspectExpression(expr)
				}
			}
		}
	}
	for _, definition := range b.ctes {
		if !owned(definition.position) {
			continue
		}
		if definition.query != nil {
			inspectQuery(definition.query.Root)
		}
		if definition.mutation != nil {
			inspectBinding(definition.mutation)
		}
	}
	if b.query != nil {
		visitCTEQuery(b.query.Root, func(LogicalNode) {}, func(expr BoundExpr) {
			walkBoundExpression(expr, func(node BoundExpr) {
				if query, ok := node.(subqueryExpression); ok && query.cteDefinition && owned(query.Pos) {
					for _, definition := range b.ctes {
						if definition.query == query.queryPlan && owned(definition.position) {
							return
						}
					}
					inspectQuery(query.queryPlan.Root)
				}
			})
		}, true)
	}
	if old {
		return sqlError("0A000", "cannot refer to OLD within WITH query")
	}
	if new {
		return sqlError("0A000", "cannot refer to NEW within WITH query")
	}
	return nil
}
