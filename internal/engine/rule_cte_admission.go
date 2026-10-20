package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

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
	var inspectQuery func(plan.Node)
	sides := func(bound expr.Expr) {
		if bound == nil {
			return
		}
		plan.WalkExpression(bound, func(node expr.Expr) {
			switch node := node.(type) {
			case plan.ColumnExpr:
				if node.Scope == rulePseudoScope {
					old = old || node.Frame == 1
					new = new || node.Frame == 2
				}
			case plan.RowExpr:
				old = old || node.RuleSide == 1
				new = new || node.RuleSide == 2
			}
		})
	}
	inspectQuery = func(root plan.Node) { plan.VisitCTEQuery(root, func(plan.Node) {}, sides, true) }
	inspectExpression := func(bound expr.Expr) {
		if bound == nil {
			return
		}
		sides(bound)
		plan.WalkExpression(bound, func(node expr.Expr) {
			if query, ok := node.(plan.SubqueryExpr); ok {
				inspectQuery(query.QueryPlan.Root)
			}
		})
	}
	inspectTargets := func(targets []*plan.ArrayAssignmentTarget) {
		for _, target := range targets {
			for current := target; current != nil; current = current.Next {
				inspectExpression(current.Node)
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
		for _, expr := range body.insert.defaults {
			inspectExpression(expr)
		}
		inspectExpression(body.predicate)
		inspectQuery(body.delete.using)
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
		if body.insert.conflict != nil {
			for _, key := range body.insert.conflict.keys {
				inspectExpression(key.expression)
			}
			inspectExpression(body.insert.conflict.predicate)
			if body.insert.conflict.update != nil {
				inspectTargets(body.insert.conflict.update.arrayTargets)
				inspectExpression(body.insert.conflict.update.predicate)
				for _, expr := range body.insert.conflict.update.assignments {
					inspectExpression(expr)
				}
			}
			if body.insert.conflict.returning != nil && body.insert.conflict.returning != body.returning {
				for _, expr := range body.insert.conflict.returning.expressions {
					inspectExpression(expr)
				}
			}
		}
	}
	for _, definition := range b.ctes {
		if !owned(definition.Position) {
			continue
		}
		if definition.Query != nil {
			inspectQuery(definition.Query.Root)
		}
		if mutation, ok := mutationBinding(definition); ok {
			inspectBinding(mutation)
		}
	}
	if b.query != nil {
		plan.VisitCTEQuery(b.query.Root, func(plan.Node) {}, func(bound expr.Expr) {
			plan.WalkExpression(bound, func(node expr.Expr) {
				if query, ok := node.(plan.SubqueryExpr); ok && query.CTEEdge && owned(query.Pos) {
					for _, definition := range b.ctes {
						if definition.Query == query.QueryPlan && owned(definition.Position) {
							return
						}
					}
					inspectQuery(query.QueryPlan.Root)
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
