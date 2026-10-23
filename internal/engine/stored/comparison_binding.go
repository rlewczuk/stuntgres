package stored

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// ComparisonBindingNamespace derives the binding requirement of implicit cube
// comparisons from their selected captures, never from replay SQL spelling.
func ComparisonBindingNamespace(view catalog.Read, node expr.Expr) (ids.ObjectAddress, error) {
	var namespace ids.ObjectAddress
	var visit func(expr.Expr) error
	add := func(captured *catalog.Procedure, capture cubeComparisonCapture) error {
		current, err := remapCubeComparisonRoutine(view, nil, captured, capture, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
		if err != nil {
			return err
		}
		ns, ok := view.Namespace(current.Namespace())
		if !ok || ns.Temporary != (ids.SessionToken{}) || ns.Name() == "$user" || ns.Name() == "pg_temp" {
			return fmt.Errorf("stuntgres: invalid comparison binding namespace")
		}
		if namespace != (ids.ObjectAddress{}) && namespace != ns.Address() {
			return fmt.Errorf("stuntgres: conflicting comparison binding namespaces")
		}
		namespace = ns.Address()
		return nil
	}
	visit = func(node expr.Expr) error {
		if node == nil {
			return nil
		}
		capture, cube := cubeComparisonCaptureOf(node)
		implicit := false
		switch n := node.(type) {
		case plan.SQLRoutineExpr:
			// Omitted defaults bind in the routine declaration, not this
			// call's SQL. Keep full executable children for other walkers.
			written, _, err := n.Capture(expr.CaptureFunction)
			if err != nil {
				return err
			}
			for _, argument := range written.Args {
				if err := visit(argument); err != nil {
					return err
				}
			}
			return nil
		case plan.ScalarArrayComparisonExpr:
			implicit = n.Array == nil && (n.Operator == "=" && !n.All || n.Operator == "<>" && n.All)
		case plan.ConditionalExpr, plan.DistinctExpr:
			implicit = true
		case plan.ComparisonExpr:
			implicit = n.Namespace == ""
		case plan.CaseExpr:
			for _, arm := range n.Arms {
				if arm.TestUsage.Type.IsCube() {
					if err := add(arm.Routine, cubeComparisonCapture{ref: arm.RoutineRef, left: arm.TestUsage.Type, right: arm.Condition.ResultUsage().Type, name: "cube_eq", result: sqltype.Bool}); err != nil {
						return err
					}
				}
			}
		case plan.RowComparisonExpr:
			for _, pair := range n.Comparisons {
				if n.Distinct() {
					pair.Namespace = ""
				}
				if err := visit(pair); err != nil {
					return err
				}
			}
		case plan.SubqueryExpr:
			for _, pair := range n.Comparisons {
				if err := visit(pair); err != nil {
					return err
				}
			}
			var failure error
			plan.VisitCTEQuery(n.QueryPlan.Root, func(plan.Node) {}, func(child expr.Expr) {
				if failure == nil {
					failure = visit(child)
				}
			}, true)
			if failure != nil {
				return failure
			}
		}
		if implicit && cube {
			captured, ok := expr.InfoOf(node).RoutineReference.(*catalog.Procedure)
			if !ok {
				return fmt.Errorf("stuntgres: missing comparison binding capture")
			}
			if err := add(captured, capture); err != nil {
				return err
			}
		}
		var failure error
		plan.ExpressionChildren(node, func(child expr.Expr) {
			if failure == nil {
				failure = visit(child)
			}
		})
		return failure
	}
	if err := visit(node); err != nil {
		return ids.ObjectAddress{}, err
	}
	return namespace, nil
}
