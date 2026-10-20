package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func querySetDepth(bound expr.Expr) int {
	depth := 0
	plan.ExpressionChildren(bound, func(child expr.Expr) {
		if d := querySetDepth(child); d > depth {
			depth = d
		}
	})
	if sourceReturnsSet(bound) {
		depth++
	}
	return depth
}
func sameQuerySetOccurrence(a, b expr.Expr) bool {
	if !sourceReturnsSet(a) || !sourceReturnsSet(b) || a.SourcePosition() != b.SourcePosition() {
		return false
	}
	if occurrence, ok := a.(expr.SourceOccurrence); ok {
		return occurrence.SameSourceOccurrence(b)
	}
	return false
}

// Requests identify this clause's required occurrences. Companions share only
// already resolved occurrences; their unrelated SRFs remain for later stages.
func (b *expressionBinder) lowerQuerySets(root plan.Node, requests []expr.Expr, companions [][]expr.Expr, attempts bool, templateRoots ...[]expr.Expr) (plan.Node, error) {
	maxDepth, scalarUpper := querySetProjectionDepth(requests)
	if maxDepth == 0 {
		return root, nil
	}
	frame := b.frameID()
	template := &plan.SetTemplate{Group: &frame, Expressions: append([]expr.Expr(nil), requests...), Attempts: attempts}
	if len(templateRoots) != 0 {
		template.Expressions = append([]expr.Expr(nil), templateRoots[0]...)
	}
	for level := range maxDepth {
		for _, bound := range requests {
			plan.WalkExpression(bound, func(e expr.Expr) {
				if !sourceReturnsSet(e) || querySetDepth(e) != level+1 {
					return
				}
				for _, previous := range template.Origins {
					if sameQuerySetOccurrence(e, previous) {
						return
					}
				}
				template.Origins = append(template.Origins, e)
			})
		}
	}
	if attempts {
		for i, expr := range requests {
			if !sourceReturnsSet(expr) && expr.ResultUsage().Type != nil {
				template.Scalars = append(template.Scalars, expr)
				template.ScalarRequests = append(template.ScalarRequests, i)
			}
		}
	}
	for request, expression := range requests {
		for i, scalarRequest := range template.ScalarRequests {
			if request == scalarRequest {
				template.Steps = append(template.Steps, plan.SetStep{Kind: querySetScalarStep, Index: i})
			}
		}
		plan.WalkExpression(expression, func(marker expr.Expr) {
			for i, original := range template.Origins {
				if !sameQuerySetOccurrence(marker, original) {
					continue
				}
				step := plan.SetStep{Kind: querySetCallStep, Index: i}
				for _, previous := range template.Steps {
					if previous == step {
						return
					}
				}
				template.Steps = append(template.Steps, step)
				return
			}
		})
	}
	base := len(root.Schema())
	schema := append(plan.QuerySchema(nil), root.Schema()...)
	for _, expressions := range [][]expr.Expr{template.Origins, template.Scalars} {
		for _, expression := range expressions {
			schema = append(schema, catalog.Column{Usage: expression.ResultUsage()})
		}
	}
	column := func(bound expr.Expr, slot int) expr.Expr {
		info := expr.InfoOf(bound)
		info.Property.StageDependent, info.Property.RowDependent = true, true
		return plan.ColumnExpr{Info: info, Scope: 1, Frame: b.frameID(), Slot: base + slot}
	}
	rewrite := func(bound expr.Expr, sql99 bool, beforeDepth int) (expr.Expr, error) {
		return b.mapQuerySetStage(bound, func(e expr.Expr) (expr.Expr, bool, error) {
			for i, original := range template.Origins {
				if (sameQuerySetOccurrence(e, original) || sql99 && sourceReturnsSet(e) && b.sameGroupExpression(e, original)) && querySetDepth(e) < beforeDepth {
					return column(original, i), true, nil
				}
			}
			return e, false, nil
		})
	}
	for level := range maxDepth {
		node := &plan.SetProjectNode{Child: root, QuerySchema: schema, Frame: b.frameID(), Template: template,
			BaseWidth: base, MarkerWidth: len(template.Origins)}
		for i, original := range template.Origins {
			if querySetDepth(original) != level+1 {
				continue
			}
			call := original
			args := append([]expr.Expr(nil), call.(expr.Composite).Children()...)
			for j, arg := range args {
				var err error
				args[j], err = rewrite(arg, false, level+1)
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
		if level+1 == maxDepth && !scalarUpper {
			for i, original := range template.Scalars {
				effective, err := rewrite(original, false, maxDepth)
				if err != nil {
					return nil, err
				}
				if querySetDepth(effective) == 0 {
					node.AppendScalar(effective, i, len(template.Scalars))
				}
			}
		}
		node.Steps = querySetStageSteps(template.Steps, node.CallSlots, node.ScalarSlots, len(node.Scalars))
		root = node
	}
	owner := root.(*plan.SetProjectNode)
	for i, expr := range requests {
		cached := false
		for local := range owner.Scalars {
			scalar := querySetScalarSlot(owner.ScalarSlots, local)
			if template.ScalarRequests[scalar] == i {
				requests[i] = column(template.Scalars[scalar], len(template.Origins)+scalar)
				cached = true
				break
			}
		}
		if cached {
			continue
		}
		next, err := rewrite(expr, false, maxDepth+1)
		if err != nil {
			return nil, err
		}
		requests[i] = next
	}
	for _, expressions := range companions {
		for i, expr := range expressions {
			next, err := rewrite(expr, true, maxDepth+1)
			if err != nil {
				return nil, err
			}
			expressions[i] = next
		}
	}
	return root, nil
}
func stringSortExpressions(keys []plan.SortKey) []expr.Expr {
	out := make([]expr.Expr, len(keys))
	for i, key := range keys {
		out[i] = key.Expression
	}
	return out
}
func replaceStringSortExpressions(keys []plan.SortKey, expressions []expr.Expr) {
	for i, expr := range expressions {
		keys[i].Expression = expr
	}
}

func preflightQuerySet(c *EvalContext, n *plan.SetProjectNode) (plan.Node, error) {
	if n.Template == nil {
		return n, nil
	}
	template := n.Template
	if len(template.Scalars) != len(template.ScalarRequests) {
		return nil, fmt.Errorf("stuntgres: invalid string scalar ownership")
	}
	roots := make([]expr.Expr, len(template.Expressions))
	for i, expression := range template.Expressions {
		folded, _, err := preflightCoercions(c, expression)
		if err != nil {
			return nil, err
		}
		roots[i] = folded
	}
	live := make([]expr.Expr, len(template.Origins))
	for _, root := range roots {
		plan.WalkExpression(root, func(marker expr.Expr) {
			for i, original := range template.Origins {
				if sameQuerySetOccurrence(marker, original) {
					live[i] = marker
				}
			}
		})
	}
	maxDepth, scalarUpper := querySetProjectionDepth(roots)
	if !template.Attempts {
		maxDepth = 0
		for _, marker := range live {
			maxDepth = max(maxDepth, querySetDepth(marker))
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
		next, _, err := mapBoundExpressionReader(c.catalog, nil, bound, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d },
			func(root plan.Node) (plan.Node, error) { return root, nil }, nil, true, func(e expr.Expr) (expr.Expr, bool, error) {
				for i, original := range template.Origins {
					if sameQuerySetOccurrence(e, original) && querySetDepth(e) < beforeDepth {
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
		scheduled := !scalarUpper && querySetDepth(effective) == 0
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
			if marker == nil || querySetDepth(marker) != level+1 {
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
		node.Steps = querySetStageSteps(template.Steps, node.CallSlots, node.ScalarSlots, len(node.Scalars))
		root = node
	}
	return root, nil
}

func querySetScalarSlot(slots []int, index int) int {
	if slots == nil {
		return index
	}
	return slots[index]
}

func stringSetLocalStep(step plan.SetStep, callSlots, scalarSlots []int, scalars int) (plan.SetStep, bool) {
	slots := callSlots
	switch step.Kind {
	case querySetCallStep:
	case querySetScalarStep:
		if scalarSlots == nil {
			return step, step.Index >= 0 && step.Index < scalars
		}
		slots = scalarSlots
	default:
		return plan.SetStep{}, false
	}
	for i, slot := range slots {
		if slot == step.Index {
			step.Index = i
			return step, true
		}
	}
	return plan.SetStep{}, false
}

func querySetStageSteps(original []plan.SetStep, callSlots, scalarSlots []int, scalars int) []plan.SetStep {
	steps := make([]plan.SetStep, 0, len(callSlots)+scalars)
	for _, step := range original {
		if local, present := stringSetLocalStep(step, callSlots, scalarSlots, scalars); present {
			steps = append(steps, local)
		}
	}
	return steps
}

func validateQuerySetSteps(steps []plan.SetStep, calls, scalars int) error {
	if len(steps) != calls+scalars {
		return fmt.Errorf("stuntgres: incomplete string set schedule")
	}
	for i, step := range steps {
		width := calls
		switch step.Kind {
		case querySetCallStep:
		case querySetScalarStep:
			width = scalars
		default:
			return fmt.Errorf("stuntgres: invalid string set schedule kind")
		}
		if step.Index < 0 || step.Index >= width {
			return fmt.Errorf("stuntgres: invalid string set schedule index")
		}
		for _, previous := range steps[:i] {
			if previous == step {
				return fmt.Errorf("stuntgres: duplicate string set schedule event")
			}
		}
	}
	return nil
}

func validateQuerySetStageOrder(original, executable []plan.SetStep, callSlots, scalarSlots []int, scalars int) error {
	position := 0
	for _, original := range original {
		step, present := stringSetLocalStep(original, callSlots, scalarSlots, scalars)
		if !present {
			continue
		}
		if position >= len(executable) || executable[position] != step {
			return fmt.Errorf("stuntgres: invalid string set schedule order")
		}
		position++
	}
	if position != len(executable) {
		return fmt.Errorf("stuntgres: invalid string set schedule owner")
	}
	return nil
}

func querySetProjectionDepth(expressions []expr.Expr) (int, bool) {
	depth, upper := 0, false
	for _, expression := range expressions {
		current := querySetDepth(expression)
		if current > depth {
			depth, upper = current, !sourceReturnsSet(expression)
		} else if current > 0 && current == depth && !sourceReturnsSet(expression) {
			upper = true
		}
	}
	return depth, upper
}

// Only the current owner's direct scalar roots or reserved scalar references
// change ownership. Child query frames and unrelated scalar subexpressions do not.
func queryScalarOwner(child plan.Node, expression expr.Expr) (expr.Expr, bool) {
	for child != nil {
		if node, ok := child.(*plan.SetProjectNode); ok {
			for _, binding := range node.ScalarBindings {
				column := binding.Column.(plan.ColumnExpr)
				existing, isColumn := expression.(plan.ColumnExpr)
				matches := isColumn && existing.Frame == column.Frame && existing.Scope == column.Scope && existing.Slot == column.Slot &&
					existing.SourcePosition() == column.SourcePosition()
				if isColumn && !matches {
					continue
				}
				if _, cached := expression.(plan.CachedOrderExpr); cached {
					continue
				}
				if !matches && expression.SourcePosition() != column.SourcePosition() && expression.SourcePosition() != binding.Original.SourcePosition() {
					continue
				}
				if node.ScalarUpper || !binding.Scheduled {
					return binding.Original, true
				}
				return binding.Column, true
			}
		}
		switch child.(type) {
		case *plan.ProjectNode, *plan.DerivedNode, *plan.SourceNode, *plan.JoinNode, *plan.DistinctNode, *plan.DistinctOnNode, *plan.AggregateNode, *plan.SetNode:
			return expression, false
		}
		next, right := plan.QueryInputs(child)
		if right != nil {
			return expression, false
		}
		child = next
	}
	return expression, false
}

// A demanded level brings its peers and their lower dependencies, not unrelated
// higher target levels. Templates retain complete scalar roots so simplification
// can erase a peer's occurrence rather than making its extracted marker live.
func querySetPeerClosure(requests []expr.Expr, companions [][]expr.Expr) ([]expr.Expr, []expr.Expr) {
	depth := 0
	for _, expression := range requests {
		depth = max(depth, querySetDepth(expression))
	}
	if depth == 0 {
		return requests, nil
	}
	templates := append([]expr.Expr(nil), requests...)
	for _, expressions := range companions {
		templates = append(templates, expressions...)
		for _, expression := range expressions {
			plan.WalkExpression(expression, func(marker expr.Expr) {
				if sourceReturnsSet(marker) && querySetDepth(marker) <= depth {
					requests = append(requests, marker)
				}
			})
		}
	}
	return requests, templates
}
