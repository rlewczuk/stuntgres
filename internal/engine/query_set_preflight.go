package engine

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func preflightQuerySet(c *exec.Context, n *plan.SetProjectNode) (plan.Node, error) {
	if n.Template == nil {
		return n, nil
	}
	template := n.Template
	if len(template.Scalars) != len(template.ScalarRequests) {
		return nil, fmt.Errorf("stuntgres: invalid string scalar ownership")
	}
	roots := make([]expr.Expr, len(template.Expressions))
	for i, expression := range template.Expressions {
		folded, _, err := exec.PreflightCoercions(c, expression)
		if err != nil {
			return nil, err
		}
		roots[i] = folded
	}
	live := make([]expr.Expr, len(template.Origins))
	for _, root := range roots {
		plan.WalkExpression(root, func(marker expr.Expr) {
			for i, original := range template.Origins {
				if analyze.SameQuerySetOccurrence(marker, original) {
					live[i] = marker
				}
			}
		})
	}
	maxDepth, scalarUpper := analyze.QuerySetProjectionDepth(roots)
	if !template.Attempts {
		maxDepth = 0
		for _, marker := range live {
			maxDepth = max(maxDepth, exec.QuerySetDepth(marker))
		}
	}
	base := n.Child
	for {
		child, ok := base.(*plan.SetProjectNode)
		if !ok || child.Template == nil || child.Template.Group != template.Group || child.Frame != n.Frame ||
			child.BaseWidth != n.BaseWidth || len(child.Schema()) != len(n.Schema()) {
			break
		}
		base = child.Child
	}
	base, err := preflightQuery(c, base)
	if err != nil {
		return nil, err
	}
	column := func(original expr.Expr, slot int) expr.Expr {
		info := expr.InfoOf(original)
		info.Property.StageDependent, info.Property.RowDependent = true, true
		return plan.ColumnExpr{Info: info, Scope: 1, Frame: n.Frame, Slot: n.BaseWidth + slot}
	}
	rewrite := func(bound expr.Expr, beforeDepth int) (expr.Expr, error) {
		next, _, err := MapExpressionReader(c.Catalog, nil, bound, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d },
			func(root plan.Node) (plan.Node, error) { return root, nil }, nil, true, func(e expr.Expr) (expr.Expr, bool, error) {
				for i, original := range template.Origins {
					if analyze.SameQuerySetOccurrence(e, original) && exec.QuerySetDepth(e) < beforeDepth {
						return column(original, i), true, nil
					}
				}
				return e, false, nil
			})
		return next, err
	}
	bindings := make([]plan.SetSlot, len(template.Scalars))
	for i, original := range template.Scalars {
		request := template.ScalarRequests[i]
		if request < 0 || request >= len(roots) {
			return nil, fmt.Errorf("stuntgres: invalid string scalar request")
		}
		beforeDepth := maxDepth + 1
		if !scalarUpper {
			beforeDepth = max(1, maxDepth)
		}
		effective, err := rewrite(roots[request], beforeDepth)
		if err != nil {
			return nil, err
		}
		scheduled := !scalarUpper && exec.QuerySetDepth(effective) == 0
		if !scalarUpper && !scheduled {
			effective, err = rewrite(roots[request], maxDepth+1)
			if err != nil {
				return nil, err
			}
		}
		bindings[i] = plan.SetSlot{Original: effective, Column: column(original, len(template.Origins)+i), Scheduled: scheduled}
	}
	root := base
	for level := range max(1, maxDepth) {
		node := &plan.SetProjectNode{Child: root, QuerySchema: n.QuerySchema, Frame: n.Frame,
			BaseWidth: n.BaseWidth, MarkerWidth: n.MarkerWidth}
		for i, marker := range live {
			if marker == nil || exec.QuerySetDepth(marker) != level+1 {
				continue
			}
			call := marker
			args := append([]expr.Expr(nil), call.(expr.Composite).Children()...)
			for j, arg := range args {
				args[j], err = rewrite(arg, level+1)
				if err != nil {
					return nil, err
				}
			}
			call, err := expr.Substitute(call.(expr.Composite), expr.InfoOf(call), args)
			if err != nil {
				return nil, err
			}
			node.Calls = append(node.Calls, call)
			node.CallSlots = append(node.CallSlots, i)
		}
		if level+1 == max(1, maxDepth) {
			node.ScalarBindings, node.ScalarUpper = bindings, scalarUpper
			for i, binding := range bindings {
				if binding.Scheduled {
					node.AppendScalar(binding.Original, i, len(bindings))
				}
			}
		}
		node.Steps = analyze.QuerySetStageSteps(template.Steps, node.CallSlots, node.ScalarSlots, len(node.Scalars))
		root = node
	}
	return root, nil
}
