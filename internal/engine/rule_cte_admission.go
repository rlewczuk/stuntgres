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
		inspectTargets(body.ArrayTargets)
		if body.Query != nil {
			inspectQuery(body.Query.Root)
		}
		for _, row := range body.Expressions {
			for _, expr := range row {
				inspectExpression(expr)
			}
		}
		for _, expr := range body.Insert.Defaults {
			inspectExpression(expr)
		}
		inspectExpression(body.Predicate)
		inspectQuery(body.Delete.Using)
		if body.Update != nil {
			inspectQuery(body.Update.From)
			inspectExpression(body.Update.Predicate)
			inspectTargets(body.Update.ArrayTargets)
			for _, expr := range body.Update.Assignments {
				inspectExpression(expr)
			}
		}
		if body.Returning != nil {
			for _, expr := range body.Returning.Expressions {
				inspectExpression(expr)
			}
		}
		if body.Insert.Conflict != nil {
			for _, key := range body.Insert.Conflict.Keys {
				inspectExpression(key.Expression)
			}
			inspectExpression(body.Insert.Conflict.Predicate)
			if body.Insert.Conflict.Update != nil {
				inspectTargets(body.Insert.Conflict.Update.ArrayTargets)
				inspectExpression(body.Insert.Conflict.Update.Predicate)
				for _, expr := range body.Insert.Conflict.Update.Assignments {
					inspectExpression(expr)
				}
			}
			if body.Insert.Conflict.Returning != nil && body.Insert.Conflict.Returning != body.Returning {
				for _, expr := range body.Insert.Conflict.Returning.Expressions {
					inspectExpression(expr)
				}
			}
		}
	}
	for _, definition := range b.CTEs {
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
	if b.Query != nil {
		plan.VisitCTEQuery(b.Query.Root, func(plan.Node) {}, func(bound expr.Expr) {
			plan.WalkExpression(bound, func(node expr.Expr) {
				if query, ok := node.(plan.SubqueryExpr); ok && query.CTEEdge && owned(query.Pos) {
					for _, definition := range b.CTEs {
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
