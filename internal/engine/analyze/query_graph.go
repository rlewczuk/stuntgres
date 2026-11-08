package analyze

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// A stored view freezes every query's original input frame as well as the
// executable output tree. The maps are analyzed metadata, never row caches.
func CollectQueryFrames(root plan.Node) (map[uint32]*plan.ProjectNode, map[*parser.Select]*plan.ProjectNode) {
	frames := make(map[uint32]*plan.ProjectNode)
	queries := make(map[*parser.Select]*plan.ProjectNode)
	var walk func(plan.Node)
	walk = func(node plan.Node) {
		if node == nil {
			return
		}
		if n, ok := node.(*plan.ProjectNode); ok && (n.Syntax != nil || n.FrozenValues) {
			if frames[n.Frame] != nil {
				if n.Syntax != nil {
					return
				}
			} else {
				frames[n.Frame] = n
				if n.Syntax != nil {
					queries[n.Syntax] = n
				}
			}
		}
		left, right := plan.QueryInputs(node)
		walk(left)
		walk(right)
		plan.QueryExpressions(node, func(bound expr.Expr) {
			plan.WalkExpression(bound, func(expr expr.Expr) {
				if e, ok := expr.(plan.SubqueryExpr); ok {
					walk(e.QueryPlan.Root)
				}
			})
		})
	}
	walk(root)
	return frames, queries
}

func QueryDescriptorsChanged(node plan.Node, typ func(*sqltype.Descriptor) *sqltype.Descriptor) bool {
	if node == nil {
		return false
	}
	for _, column := range node.Schema() {
		if typ(column.Usage.Type) != column.Usage.Type {
			return true
		}
	}
	changed := false
	plan.QueryExpressions(node, func(bound expr.Expr) {
		plan.WalkExpression(bound, func(expr expr.Expr) {
			if typ(expr.ResultUsage().Type) != expr.ResultUsage().Type {
				changed = true
			}
			if literal, ok := expr.(plan.LiteralExpr); ok && literal.UnknownInput != nil && literal.UnknownInput.CastType != nil && typ(literal.UnknownInput.CastType) != literal.UnknownInput.CastType {
				changed = true
			}
			if coercion, ok := expr.(plan.CoercionExpr); ok && coercion.UnknownInputCast != nil && typ(coercion.UnknownInputCast.Target) != coercion.UnknownInputCast.Target {
				changed = true
			}
			if literal, ok := expr.(plan.LiteralExpr); ok && literal.UnknownInput != nil {
				for _, step := range literal.UnknownInput.Chain {
					changed = changed || typ(step.Target) != step.Target
				}
			}
			if coercion, ok := expr.(plan.CoercionExpr); ok && coercion.UnknownInputCast != nil {
				for _, step := range coercion.UnknownInputCast.Transparent {
					changed = changed || typ(step.Target) != step.Target
				}
			}
			if earth, ok := expr.(plan.InstalledEarthExpr); ok && earth.Plan.Earth() != nil && typ(earth.Plan.Earth()) != earth.Plan.Earth() {
				changed = true
			}
			if child, ok := expr.(plan.SubqueryExpr); ok && QueryDescriptorsChanged(child.QueryPlan.Root, typ) {
				changed = true
			}
		})
	})
	if changed {
		return true
	}
	left, right := plan.QueryInputs(node)
	return QueryDescriptorsChanged(left, typ) || QueryDescriptorsChanged(right, typ)
}

// A type-only fast path must not retain an old routine identity carrier after
// catalog reownership. Pointer comparison here requests metadata reconstruction;
// it is never an ACL or lifetime admission decision.
func QueryRoutineCarriersChanged(view catalog.Read, node plan.Node) bool {
	return queryRoutineCarriersChanged(view, node, false)
}

// Ordinary type evolution must leave unchanged retained captures alone, even
// when COW/bootstrap produced a fresh descriptor of the same declaration.
func QueryRoutineDeclarationsChanged(view catalog.Read, node plan.Node) bool {
	return queryRoutineCarriersChanged(view, node, true)
}

func queryRoutineCarriersChanged(view catalog.Read, node plan.Node, declarationsOnly bool) bool {
	if node == nil || view.State() == nil {
		return false
	}
	changed := false
	if source, ok := node.(*plan.SourceNode); ok && source.Sample != nil {
		captured := source.Sample.Procedure
		current, _ := view.Procedure(source.Sample.Handler.Address)
		if declarationsOnly {
			changed = !exec.SameRoutineDeclaration(current, captured)
		} else {
			changed = current != captured
		}
	}
	plan.QueryExpressions(node, func(expression expr.Expr) {
		plan.WalkExpression(expression, func(expression expr.Expr) {
			captured, err := exec.ExpressionRoutineDescriptor(expression)
			if err != nil {
				changed = true
			} else if captured != nil {
				if declarationsOnly {
					current, _ := view.Procedure(captured.Address())
					changed = changed || !exec.SameRoutineDeclaration(current, captured)
				} else {
					changed = changed || view.Get(captured.Address()) != captured
				}
			}
			if subquery, ok := expression.(plan.SubqueryExpr); ok && queryRoutineCarriersChanged(view, subquery.QueryPlan.Root, declarationsOnly) {
				changed = true
			}
		})
	})
	if changed {
		return true
	}
	left, right := plan.QueryInputs(node)
	return queryRoutineCarriersChanged(view, left, declarationsOnly) || queryRoutineCarriersChanged(view, right, declarationsOnly)
}

// Runtime reads belong to an occurrence's physical input span in its lexical
// frame, not to a relation OID. Invocation owners translate normalized slots
// back to that span; analysis-only CTE edges remain frozen metadata.
func QueryExecutionSourceReads(root plan.Node, ignoredProject *plan.ProjectNode) map[int][]int32 {
	type span struct {
		source *plan.SourceNode
		offset int
	}
	// Each owner maps a runtime row to physical frame coordinates. A join's
	// invocation consists of an inherited prefix followed by its local left
	// row; only the prefix uses the previous owner of the same lexical frame.
	type owner struct {
		frame                 uint32
		offset, prefix, outer int
	}
	var owners []owner
	physicalSlot := func(frame uint32, slot, at int) int {
		for at >= 0 {
			current := owners[at]
			at = current.outer
			if current.frame != frame {
				continue
			}
			if slot >= current.prefix {
				return current.offset + slot - current.prefix
			}
		}
		return slot
	}
	var frames map[uint32][]span
	var reads map[int][]int32
	var walk func(plan.Node, uint32, int, int, *plan.ProjectNode, bool)
	walk = func(node plan.Node, frame uint32, offset, enclosing int, ignored *plan.ProjectNode, collect bool) {
		if node == nil {
			return
		}
		base := len(owners)
		if project, ok := node.(*plan.ProjectNode); ok {
			frame, offset = project.Frame, 0
			if frame == 0 {
				frame = 1
			}
			if collect {
				owners = append(owners, owner{frame: frame, outer: enclosing})
				enclosing = len(owners) - 1
			}
		}
		if source, ok := node.(*plan.SourceNode); ok && !collect {
			if reads == nil {
				frames = make(map[uint32][]span)
				reads = make(map[int][]int32)
			}
			frames[frame] = append(frames[frame], span{source, offset})
			reads[source.Occurrence] = []int32{}
		}
		left, right := plan.QueryInputs(node)
		walk(left, frame, offset, enclosing, ignored, collect)
		rightOffset, rightOwner := offset, enclosing
		if join, ok := node.(*plan.JoinNode); ok {
			rightOffset += join.LeftWidth
			if collect && join.CorrelatedRight {
				owners = append(owners, owner{frame: join.Frame, offset: offset, prefix: join.CorrelationPrefix, outer: enclosing})
				rightOwner = len(owners) - 1
			}
		}
		walk(right, frame, rightOffset, rightOwner, ignored, collect)
		if rightOwner != enclosing {
			owners = owners[:rightOwner]
		}
		if node == ignored || QueryIgnoresOwnExpressions(node) {
			owners = owners[:base]
			return
		}
		// FROM arguments read the invocation, not their own output row.
		// Other producers (notably JOIN predicates and scalar sublinks in
		// them) read their local row at this physical subtree offset.
		if collect {
			switch node.(type) {
			case *plan.SourceNode, *plan.FunctionFromNode, *plan.ProjectNode:
			default:
				owners = append(owners, owner{frame: frame, offset: offset, outer: enclosing})
				enclosing = len(owners) - 1
			}
		}
		var visit func(expr.Expr)
		visit = func(expression expr.Expr) {
			switch e := expression.(type) {
			case plan.SubqueryExpr:
				if !e.CTEEdge {
					var ignoredChild *plan.ProjectNode
					if e.PresenceOnly {
						ignoredChild = e.QueryPlan.Root.(*plan.ProjectNode)
					}
					walk(e.QueryPlan.Root, frame, 0, enclosing, ignoredChild, collect)
				}
			case plan.ColumnExpr:
				if !collect || e.Scope != 1 || e.Origin.Address.SubID <= 0 {
					return
				}
				slot := physicalSlot(e.Frame, e.Slot, enclosing)
				for _, input := range frames[e.Frame] {
					if slot < input.offset || slot >= input.offset+len(input.source.Schema()) || e.Origin.Address.ClassID != input.source.Ref.Address.ClassID || e.Origin.Address.ObjectID != input.source.Ref.Address.ObjectID {
						continue
					}
					items := reads[input.source.Occurrence]
					for _, attribute := range items {
						if attribute == e.Origin.Address.SubID {
							return
						}
					}
					reads[input.source.Occurrence] = append(items, e.Origin.Address.SubID)
					return
				}
				// A virtual column's recipe has its own owner, even if it
				// privately reuses this numeric frame.
				return
			}
			plan.ExpressionChildren(expression, visit)
		}
		if project, ok := node.(*plan.ProjectNode); ok {
			for _, expression := range project.Expressions {
				visit(expression)
			}
		} else {
			plan.QueryExpressions(node, visit)
		}
		owners = owners[:base]
	}
	// Register all spans first, including outer frames referenced by correlated
	// expressions inside other inputs. Then collect only executable producers.
	walk(root, 1, 0, -1, ignoredProject, false)
	if reads != nil {
		walk(root, 1, 0, -1, ignoredProject, true)
	}
	return reads
}

// Bridge captured attribute identities to the current source layout. nil
// denotes full demand; a nonnil empty slice denotes presence-only demand.
func QuerySourceReadUses(reads []int32, attributes []int32) ([]int, error) {
	if reads == nil {
		return nil, nil
	}
	uses := make([]int, 0, len(reads))
	for slot, attribute := range attributes {
		for _, read := range reads {
			if attribute == read {
				uses = append(uses, slot)
				break
			}
		}
	}
	if len(uses) != len(reads) {
		return nil, ids.ErrCatalogReplaced
	}
	return uses, nil
}
