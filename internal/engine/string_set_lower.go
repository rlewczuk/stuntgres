package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
)

func sameQuerySetOccurrence(a, b expr.Expr) bool {
	if !exec.SourceReturnsSet(a) || !exec.SourceReturnsSet(b) || a.SourcePosition() != b.SourcePosition() {
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
				if !exec.SourceReturnsSet(e) || exec.QuerySetDepth(e) != level+1 {
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
			if !exec.SourceReturnsSet(expr) && expr.ResultUsage().Type != nil {
				template.Scalars = append(template.Scalars, expr)
				template.ScalarRequests = append(template.ScalarRequests, i)
			}
		}
	}
	for request, expression := range requests {
		for i, scalarRequest := range template.ScalarRequests {
			if request == scalarRequest {
				template.Steps = append(template.Steps, plan.SetStep{Kind: exec.QuerySetScalarStep, Index: i})
			}
		}
		plan.WalkExpression(expression, func(marker expr.Expr) {
			for i, original := range template.Origins {
				if !sameQuerySetOccurrence(marker, original) {
					continue
				}
				step := plan.SetStep{Kind: exec.QuerySetCallStep, Index: i}
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
				if (sameQuerySetOccurrence(e, original) || sql99 && exec.SourceReturnsSet(e) && b.sameGroupExpression(e, original)) && exec.QuerySetDepth(e) < beforeDepth {
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
			if exec.QuerySetDepth(original) != level+1 {
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
				if exec.QuerySetDepth(effective) == 0 {
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
			scalar := exec.QuerySetScalarSlot(owner.ScalarSlots, local)
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

func querySetStageSteps(original []plan.SetStep, callSlots, scalarSlots []int, scalars int) []plan.SetStep {
	steps := make([]plan.SetStep, 0, len(callSlots)+scalars)
	for _, step := range original {
		if local, present := exec.StringSetLocalStep(step, callSlots, scalarSlots, scalars); present {
			steps = append(steps, local)
		}
	}
	return steps
}

func querySetProjectionDepth(expressions []expr.Expr) (int, bool) {
	depth, upper := 0, false
	for _, expression := range expressions {
		current := exec.QuerySetDepth(expression)
		if current > depth {
			depth, upper = current, !exec.SourceReturnsSet(expression)
		} else if current > 0 && current == depth && !exec.SourceReturnsSet(expression) {
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
		depth = max(depth, exec.QuerySetDepth(expression))
	}
	if depth == 0 {
		return requests, nil
	}
	templates := append([]expr.Expr(nil), requests...)
	for _, expressions := range companions {
		templates = append(templates, expressions...)
		for _, expression := range expressions {
			plan.WalkExpression(expression, func(marker expr.Expr) {
				if exec.SourceReturnsSet(marker) && exec.QuerySetDepth(marker) <= depth {
					requests = append(requests, marker)
				}
			})
		}
	}
	return requests, templates
}
