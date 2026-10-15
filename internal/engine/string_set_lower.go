package engine

import (
	"fmt"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/sqltype"
)

type querySetSlot struct {
	original  BoundExpr
	column    BoundExpr
	scheduled bool // The owner reads its reserved slot only for an active scalar event.
}

func querySetDepth(expr BoundExpr) int {
	depth := 0
	boundExpressionChildren(expr, func(child BoundExpr) {
		if d := querySetDepth(child); d > depth {
			depth = d
		}
	})
	if isQuerySet(expr) {
		depth++
	}
	return depth
}
func sameQuerySetOccurrence(a, b BoundExpr) bool {
	if !isQuerySet(a) || !isQuerySet(b) || a.SourcePosition() != b.SourcePosition() {
		return false
	}
	switch first := a.(type) {
	case seriesExpression:
		second, ok := b.(seriesExpression)
		return ok && first.ResultUsage() == second.ResultUsage() && len(first.args) == len(second.args)
	case subscriptsExpression:
		second, ok := b.(subscriptsExpression)
		return ok && first.ResultUsage() == second.ResultUsage() && len(first.args) == len(second.args)
	case multirangeUnnestExpression:
		second, ok := b.(multirangeUnnestExpression)
		return ok && first.ResultUsage() == second.ResultUsage()
	case functions.JSONKeys[BoundExpr, *EvalContext, ExprProperties]:
		second, ok := b.(functions.JSONKeys[BoundExpr, *EvalContext, ExprProperties])
		return ok && first.Plan.Name() == second.Plan.Name()
	case functions.JSONBElements[BoundExpr, *EvalContext, ExprProperties]:
		second, ok := b.(functions.JSONBElements[BoundExpr, *EvalContext, ExprProperties])
		return ok && first.Plan.Name() == second.Plan.Name() && first.ResultUsage() == second.ResultUsage()
	case functions.JSONEach[BoundExpr, *EvalContext, ExprProperties]:
		second, ok := b.(functions.JSONEach[BoundExpr, *EvalContext, ExprProperties])
		return ok && first.Plan.Name() == second.Plan.Name() && sameRecordUsage(first.ResultUsage(), second.ResultUsage())
	case functions.JSONElements[BoundExpr, *EvalContext, ExprProperties]:
		second, ok := b.(functions.JSONElements[BoundExpr, *EvalContext, ExprProperties])
		return ok && first.Plan.Name() == second.Plan.Name() && first.ResultUsage() == second.ResultUsage()
	case functions.RegexpSet[BoundExpr, *EvalContext, ExprProperties]:
		_, ok := b.(functions.RegexpSet[BoundExpr, *EvalContext, ExprProperties])
		return ok
	default:
		return isStringSet(a) && isStringSet(b)
	}
}

// Requests identify this clause's required occurrences. Companions share only
// already resolved occurrences; their unrelated SRFs remain for later stages.
func (b *expressionBinder) lowerQuerySets(root LogicalNode, requests []BoundExpr, companions [][]BoundExpr, attempts bool, templateRoots ...[]BoundExpr) (LogicalNode, error) {
	maxDepth, scalarUpper := querySetProjectionDepth(requests)
	if maxDepth == 0 {
		return root, nil
	}
	frame := b.frameID()
	template := &querySetTemplate{group: &frame, expressions: append([]BoundExpr(nil), requests...), attempts: attempts}
	if len(templateRoots) != 0 {
		template.expressions = append([]BoundExpr(nil), templateRoots[0]...)
	}
	for level := range maxDepth {
		for _, expr := range requests {
			walkBoundExpression(expr, func(e BoundExpr) {
				if !isQuerySet(e) || querySetDepth(e) != level+1 {
					return
				}
				for _, previous := range template.origins {
					if sameQuerySetOccurrence(e, previous) {
						return
					}
				}
				template.origins = append(template.origins, e)
			})
		}
	}
	if attempts {
		for i, expr := range requests {
			if !isQuerySet(expr) && expr.ResultUsage().Type != nil {
				template.scalars = append(template.scalars, expr)
				template.scalarRequests = append(template.scalarRequests, i)
			}
		}
	}
	for request, expression := range requests {
		for i, scalarRequest := range template.scalarRequests {
			if request == scalarRequest {
				template.steps = append(template.steps, querySetStep{kind: querySetScalarStep, index: i})
			}
		}
		walkBoundExpression(expression, func(marker BoundExpr) {
			for i, original := range template.origins {
				if !sameQuerySetOccurrence(marker, original) {
					continue
				}
				step := querySetStep{kind: querySetCallStep, index: i}
				for _, previous := range template.steps {
					if previous == step {
						return
					}
				}
				template.steps = append(template.steps, step)
				return
			}
		})
	}
	base := len(root.Schema())
	schema := append(querySchema(nil), root.Schema()...)
	for _, expressions := range [][]BoundExpr{template.origins, template.scalars} {
		for _, expression := range expressions {
			schema = append(schema, ColumnDescriptor{Usage: expression.ResultUsage()})
		}
	}
	column := func(expr BoundExpr, slot int) BoundExpr {
		info := boundExpressionInfo(expr)
		info.properties.StageDependent, info.properties.RowDependent = true, true
		return columnExpression{expressionInfo: info, scope: 1, frame: b.frameID(), slot: base + slot}
	}
	rewrite := func(expr BoundExpr, sql99 bool, beforeDepth int) (BoundExpr, error) {
		return b.mapQuerySetStage(expr, func(e BoundExpr) (BoundExpr, bool, error) {
			for i, original := range template.origins {
				if (sameQuerySetOccurrence(e, original) || sql99 && isQuerySet(e) && b.sameGroupExpression(e, original)) && querySetDepth(e) < beforeDepth {
					return column(original, i), true, nil
				}
			}
			return e, false, nil
		})
	}
	for level := range maxDepth {
		node := &setProjectNode{child: root, querySchema: schema, frame: b.frameID(), template: template,
			baseWidth: base, markerWidth: len(template.origins)}
		for i, original := range template.origins {
			if querySetDepth(original) != level+1 {
				continue
			}
			call := original
			args := append([]BoundExpr(nil), functionCallArguments(call)...)
			for j, arg := range args {
				var err error
				args[j], err = rewrite(arg, false, level+1)
				if err != nil {
					return nil, err
				}
			}
			call, err := withFunctionCallArguments(call, args)
			if err != nil {
				return nil, err
			}
			node.calls = append(node.calls, call)
			node.callSlots = append(node.callSlots, i)
		}
		if level+1 == maxDepth && !scalarUpper {
			for i, original := range template.scalars {
				effective, err := rewrite(original, false, maxDepth)
				if err != nil {
					return nil, err
				}
				if querySetDepth(effective) == 0 {
					node.appendScalar(effective, i, len(template.scalars))
				}
			}
		}
		node.steps = querySetStageSteps(template.steps, node.callSlots, node.scalarSlots, len(node.scalars))
		root = node
	}
	owner := root.(*setProjectNode)
	for i, expr := range requests {
		cached := false
		for local := range owner.scalars {
			scalar := querySetScalarSlot(owner.scalarSlots, local)
			if template.scalarRequests[scalar] == i {
				requests[i] = column(template.scalars[scalar], len(template.origins)+scalar)
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
func stringSortExpressions(keys []sortKey) []BoundExpr {
	out := make([]BoundExpr, len(keys))
	for i, key := range keys {
		out[i] = key.expression
	}
	return out
}
func replaceStringSortExpressions(keys []sortKey, expressions []BoundExpr) {
	for i, expr := range expressions {
		keys[i].expression = expr
	}
}

func preflightQuerySet(c *EvalContext, n *setProjectNode) (LogicalNode, error) {
	if n.template == nil {
		return n, nil
	}
	template := n.template
	if len(template.scalars) != len(template.scalarRequests) {
		return nil, fmt.Errorf("stuntgres: invalid string scalar ownership")
	}
	roots := make([]BoundExpr, len(template.expressions))
	for i, expression := range template.expressions {
		folded, _, err := preflightCoercions(c, expression)
		if err != nil {
			return nil, err
		}
		roots[i] = folded
	}
	live := make([]BoundExpr, len(template.origins))
	for _, root := range roots {
		walkBoundExpression(root, func(marker BoundExpr) {
			for i, original := range template.origins {
				if sameQuerySetOccurrence(marker, original) {
					live[i] = marker
				}
			}
		})
	}
	maxDepth, scalarUpper := querySetProjectionDepth(roots)
	if !template.attempts {
		maxDepth = 0
		for _, marker := range live {
			maxDepth = max(maxDepth, querySetDepth(marker))
		}
	}
	base := n.child
	for {
		child, ok := base.(*setProjectNode)
		if !ok || child.template == nil || child.template.group != template.group || child.frame != n.frame ||
			child.baseWidth != n.baseWidth || len(child.Schema()) != len(n.Schema()) {
			break
		}
		base = child.child
	}
	base, err := preflightQuery(c, base)
	if err != nil {
		return nil, err
	}
	column := func(original BoundExpr, slot int) BoundExpr {
		info := boundExpressionInfo(original)
		info.properties.StageDependent, info.properties.RowDependent = true, true
		return columnExpression{expressionInfo: info, scope: 1, frame: n.frame, slot: n.baseWidth + slot}
	}
	rewrite := func(expr BoundExpr, beforeDepth int) (BoundExpr, error) {
		next, _, err := mapBoundExpression(c.catalog, expr, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d },
			func(root LogicalNode) (LogicalNode, error) { return root, nil }, func(e BoundExpr) (BoundExpr, bool, error) {
				for i, original := range template.origins {
					if sameQuerySetOccurrence(e, original) && querySetDepth(e) < beforeDepth {
						return column(original, i), true, nil
					}
				}
				return e, false, nil
			})
		return next, err
	}
	bindings := make([]querySetSlot, len(template.scalars))
	for i, original := range template.scalars {
		request := template.scalarRequests[i]
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
		bindings[i] = querySetSlot{original: effective, column: column(original, len(template.origins)+i), scheduled: scheduled}
	}
	root := base
	for level := range max(1, maxDepth) {
		node := &setProjectNode{child: root, querySchema: n.querySchema, frame: n.frame,
			baseWidth: n.baseWidth, markerWidth: n.markerWidth}
		for i, marker := range live {
			if marker == nil || querySetDepth(marker) != level+1 {
				continue
			}
			call := marker
			args := append([]BoundExpr(nil), functionCallArguments(call)...)
			for j, arg := range args {
				args[j], err = rewrite(arg, level+1)
				if err != nil {
					return nil, err
				}
			}
			call, err := withFunctionCallArguments(call, args)
			if err != nil {
				return nil, err
			}
			node.calls = append(node.calls, call)
			node.callSlots = append(node.callSlots, i)
		}
		if level+1 == max(1, maxDepth) {
			node.scalarBindings, node.scalarUpper = bindings, scalarUpper
			for i, binding := range bindings {
				if binding.scheduled {
					node.appendScalar(binding.original, i, len(bindings))
				}
			}
		}
		node.steps = querySetStageSteps(template.steps, node.callSlots, node.scalarSlots, len(node.scalars))
		root = node
	}
	return root, nil
}

func (n *setProjectNode) appendScalar(expression BoundExpr, candidate, candidates int) {
	if n.scalars == nil {
		n.scalars = make([]BoundExpr, 0, candidates)
	}
	if n.scalarSlots != nil {
		n.scalarSlots = append(n.scalarSlots, candidate)
	} else if candidate != len(n.scalars) {
		n.scalarSlots = make([]int, len(n.scalars), candidates)
		for i := range n.scalarSlots {
			n.scalarSlots[i] = i
		}
		n.scalarSlots = append(n.scalarSlots, candidate)
	}
	n.scalars = append(n.scalars, expression)
}

func querySetScalarSlot(slots []int, index int) int {
	if slots == nil {
		return index
	}
	return slots[index]
}

func stringSetLocalStep(step querySetStep, callSlots, scalarSlots []int, scalars int) (querySetStep, bool) {
	slots := callSlots
	switch step.kind {
	case querySetCallStep:
	case querySetScalarStep:
		if scalarSlots == nil {
			return step, step.index >= 0 && step.index < scalars
		}
		slots = scalarSlots
	default:
		return querySetStep{}, false
	}
	for i, slot := range slots {
		if slot == step.index {
			step.index = i
			return step, true
		}
	}
	return querySetStep{}, false
}

func querySetStageSteps(original []querySetStep, callSlots, scalarSlots []int, scalars int) []querySetStep {
	steps := make([]querySetStep, 0, len(callSlots)+scalars)
	for _, step := range original {
		if local, present := stringSetLocalStep(step, callSlots, scalarSlots, scalars); present {
			steps = append(steps, local)
		}
	}
	return steps
}

func validateQuerySetSteps(steps []querySetStep, calls, scalars int) error {
	if len(steps) != calls+scalars {
		return fmt.Errorf("stuntgres: incomplete string set schedule")
	}
	for i, step := range steps {
		width := calls
		switch step.kind {
		case querySetCallStep:
		case querySetScalarStep:
			width = scalars
		default:
			return fmt.Errorf("stuntgres: invalid string set schedule kind")
		}
		if step.index < 0 || step.index >= width {
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

func validateQuerySetStageOrder(original, executable []querySetStep, callSlots, scalarSlots []int, scalars int) error {
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

func querySetProjectionDepth(expressions []BoundExpr) (int, bool) {
	depth, upper := 0, false
	for _, expression := range expressions {
		current := querySetDepth(expression)
		if current > depth {
			depth, upper = current, !isQuerySet(expression)
		} else if current > 0 && current == depth && !isQuerySet(expression) {
			upper = true
		}
	}
	return depth, upper
}

// Only the current owner's direct scalar roots or reserved scalar references
// change ownership. Child query frames and unrelated scalar subexpressions do not.
func queryScalarOwner(child LogicalNode, expression BoundExpr) (BoundExpr, bool) {
	for child != nil {
		if node, ok := child.(*setProjectNode); ok {
			for _, binding := range node.scalarBindings {
				column := binding.column.(columnExpression)
				existing, isColumn := expression.(columnExpression)
				matches := isColumn && existing.frame == column.frame && existing.scope == column.scope && existing.slot == column.slot &&
					existing.SourcePosition() == column.SourcePosition()
				if isColumn && !matches {
					continue
				}
				if _, cached := expression.(cachedOrderExpression); cached {
					continue
				}
				if !matches && expression.SourcePosition() != column.SourcePosition() && expression.SourcePosition() != binding.original.SourcePosition() {
					continue
				}
				if node.scalarUpper || !binding.scheduled {
					return binding.original, true
				}
				return binding.column, true
			}
		}
		switch child.(type) {
		case *projectNode, *derivedNode, *sourceNode, *joinNode, *distinctNode, *distinctOnNode, *aggregateNode, *setNode:
			return expression, false
		}
		next, right := queryInputs(child)
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
func querySetPeerClosure(requests []BoundExpr, companions [][]BoundExpr) ([]BoundExpr, []BoundExpr) {
	depth := 0
	for _, expression := range requests {
		depth = max(depth, querySetDepth(expression))
	}
	if depth == 0 {
		return requests, nil
	}
	templates := append([]BoundExpr(nil), requests...)
	for _, expressions := range companions {
		templates = append(templates, expressions...)
		for _, expression := range expressions {
			walkBoundExpression(expression, func(marker BoundExpr) {
				if isQuerySet(marker) && querySetDepth(marker) <= depth {
					requests = append(requests, marker)
				}
			})
		}
	}
	return requests, templates
}
