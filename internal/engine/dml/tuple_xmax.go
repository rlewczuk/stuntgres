package dml

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
)

// Mutation expressions consume the frozen candidate header until an actual
// row-lock recheck; RETURNING separately captures the accepted action header.
func captureMutationXmax(h Host, b analyze.Binding, target, inputs *exec.Source) (bool, error) {
	demanded := false
	var visit func(expr.Expr)
	visit = func(value expr.Expr) {
		plan.WalkExpression(value, func(value expr.Expr) {
			if n, ok := value.(plan.SystemAttributeExpr); ok && n.SubID == -4 {
				demanded = true
			}
			if n, ok := value.(plan.SubqueryExpr); ok && !n.CTEEdge && n.QueryPlan != nil {
				plan.VisitExecutableQuery(n.QueryPlan.Root, func(plan.Node) {}, visit)
			}
		})
	}
	var binding func(analyze.Binding)
	binding = func(b analyze.Binding) {
		visit(b.Predicate)
		for _, row := range b.Expressions {
			for _, value := range row {
				visit(value)
			}
		}
		if b.Update != nil {
			visit(b.Update.Predicate)
			for _, value := range b.Update.Assignments {
				visit(value)
			}
			for _, target := range b.Update.ArrayTargets {
				for ; target != nil; target = target.Next {
					if target.Attribute == 0 {
						visit(target.Node)
					}
				}
			}
		}
		if b.Merge != nil {
			visit(b.Merge.On)
			for _, arm := range b.Merge.Arms {
				visit(arm.Condition)
				binding(arm.Input)
			}
		}
		if b.Returning != nil {
			for _, value := range b.Returning.Expressions {
				plan.WalkExpression(value, func(value expr.Expr) {
					if n, ok := value.(plan.SystemAttributeExpr); ok && n.SubID == -4 && n.QueryOccurrence != 0 {
						demanded = true
					}
				})
			}
		}
	}
	binding(b)
	if !demanded {
		return false, nil
	}
	if target != nil {
		target.Host = h
		if err := exec.CaptureSourceXmax(target); err != nil {
			return false, err
		}
	}
	if inputs != nil && inputs != target {
		inputs.Host = h
		if err := exec.CaptureSourceXmax(inputs); err != nil {
			return false, err
		}
	}
	return true, nil
}
