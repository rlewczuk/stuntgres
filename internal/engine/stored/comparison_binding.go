package stored

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// ComparisonBindingNamespace derives implicit installed comparison lookup from
// the authentic current operator, not its independently movable backing routine.
// visitProcedure receives each authenticated current backing declaration.
func ComparisonBindingNamespace(view catalog.Read, node expr.Expr, visitProcedure func(*catalog.Procedure) error) (ids.ObjectAddress, error) {
	var namespace ids.ObjectAddress
	var visit func(expr.Expr) error
	addCurrent := func(captured *catalog.Procedure, name string, left, right *sqltype.Descriptor) error {
		current, ok := view.Procedure(captured.Address())
		if !ok {
			return fmt.Errorf("stuntgres: missing comparison binding procedure")
		}
		installation, ok := catalog.OriginInstallation(current)
		if !ok {
			return fmt.Errorf("stuntgres: missing comparison binding installation")
		}
		var operator *catalog.Operator
		for _, address := range installation.Members {
			if address.ClassID != ids.OperatorClassID {
				continue
			}
			candidate, exists := view.Operator(address)
			if !exists || candidate.Function != current.Address().ObjectID {
				continue
			}
			if operator != nil {
				return fmt.Errorf("stuntgres: ambiguous comparison binding operator")
			}
			if err := (extension.CatalogProvider{Class: ids.OperatorClassID}).Validate(view, candidate); err != nil {
				return err
			}
			if !catalog.SameInstallation(candidate, installation) || candidate.Result != current.Result ||
				candidate.Left != ids.OID(left.ID()) || candidate.Right != ids.OID(right.ID()) {
				return fmt.Errorf("stuntgres: invalid comparison binding operator")
			}
			operator = candidate
		}
		if operator == nil {
			return fmt.Errorf("stuntgres: missing comparison binding operator")
		}
		var expected string
		switch name {
		case "cube_eq", "hstore_eq", "ltree_eq":
			expected = "="
		case "cube_ne", "hstore_ne", "ltree_ne":
			expected = "<>"
		case "cube_lt", "ltree_lt":
			expected = "<"
		case "cube_le", "ltree_le":
			expected = "<="
		case "cube_gt", "ltree_gt":
			expected = ">"
		case "cube_ge", "ltree_ge":
			expected = ">="
		case "hstore_lt":
			expected = "#<#"
		case "hstore_le":
			expected = "#<=#"
		case "hstore_gt":
			expected = "#>#"
		case "hstore_ge":
			expected = "#>=#"
		}
		if expected == "" || operator.Name() != expected {
			return fmt.Errorf("stuntgres: invalid comparison binding operator name")
		}
		ns, ok := view.Namespace(operator.Namespace())
		if !ok || ns.Temporary != (ids.SessionToken{}) || ns.Name() == "$user" || ns.Name() == "pg_temp" {
			return fmt.Errorf("stuntgres: invalid comparison binding namespace")
		}
		if namespace != (ids.ObjectAddress{}) && namespace != ns.Address() {
			return fmt.Errorf("stuntgres: conflicting comparison binding namespaces")
		}
		namespace = ns.Address()
		if visitProcedure != nil {
			return visitProcedure(current)
		}
		return nil
	}
	add := func(captured *catalog.Procedure, capture cubeComparisonCapture) error {
		if err := remapCubeComparisonRoutine(view, nil, captured, captured, capture, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }); err != nil {
			return err
		}
		return addCurrent(captured, capture.name, capture.left, capture.right)
	}
	addHstore := func(captured *catalog.Procedure, capture hstoreComparisonCapture) error {
		if err := remapHstoreComparisonRoutine(view, nil, captured, captured, capture, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }); err != nil {
			return err
		}
		return addCurrent(captured, capture.name, capture.left, capture.right)
	}
	addLtree := func(captured *catalog.Procedure, capture ltreeComparisonCapture) error {
		if err := remapLtreeComparisonRoutine(view, nil, captured, captured, capture, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }); err != nil {
			return err
		}
		return addCurrent(captured, capture.name, capture.left, capture.right)
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
				if arm.TestUsage.Type.IsLtree() {
					if err := addLtree(arm.Routine, ltreeComparisonCapture{ref: arm.RoutineRef, left: arm.TestUsage.Type, right: arm.Condition.ResultUsage().Type, name: "ltree_eq", result: sqltype.Bool}); err != nil {
						return err
					}
				}
				if arm.TestUsage.Type.IsHstore() {
					if err := addHstore(arm.Routine, hstoreComparisonCapture{ref: arm.RoutineRef, left: arm.TestUsage.Type, right: arm.Condition.ResultUsage().Type, name: "hstore_eq", result: sqltype.Bool}); err != nil {
						return err
					}
				}
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
		if capture, ltree := ltreeComparisonCaptureOf(node); implicit && ltree {
			captured, ok := expr.InfoOf(node).RoutineReference.(*catalog.Procedure)
			if !ok {
				return fmt.Errorf("stuntgres: missing comparison binding capture")
			}
			if err := addLtree(captured, capture); err != nil {
				return err
			}
		}
		if capture, hstore := hstoreComparisonCaptureOf(node); implicit && hstore {
			captured, ok := expr.InfoOf(node).RoutineReference.(*catalog.Procedure)
			if !ok {
				return fmt.Errorf("stuntgres: missing comparison binding capture")
			}
			if err := addHstore(captured, capture); err != nil {
				return err
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
