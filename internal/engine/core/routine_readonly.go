package core

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
)

// SPI classifies the analyzed executable occurrences, not outer SELECT syntax
// or analysis-only dependencies. The caller still owns the canonical binding.
// Stored sources retain the same occurrence metadata and expand by identity.
// Keep this stricter all-statement contract separate from ts_rewrite's gate.
func routineReadOnlyViolation(statement parser.Statement, read catalog.Read, sources []exec.Leaf, query *plan.Query) string {
	if parser.ModifyingStatement(statement) {
		return RoutineCommandTag(statement)
	}
	var active map[ids.ObjectAddress]bool
	var mode lock.Mode
	present := false
	if query != nil {
		// A parent query's explicit markers precede child-query markers.
		// Order is meaningful only among occurrences owned by this level.
		plan.VisitExecutableQuery(query.Root, func(node plan.Node) {
			project, ok := node.(*plan.ProjectNode)
			if !ok || project.Syntax == nil || present {
				return
			}
			firstOrder := 0
			for pass := range 2 {
				for _, source := range sources {
					if source.AnalysisOnly || source.Query != project.Syntax || (source.Mark.Mode != 0) != (pass == 0) {
						continue
					}
					if present && !connectbyEarlierMark(source.Mark.Order, firstOrder) {
						continue
					}
					candidate, marked := source.Mark.Mode, source.Mark.Mode != 0
					if source.Relation.RelationKind == catalog.ViewRelation {
						if active == nil {
							active = make(map[ids.ObjectAddress]bool)
						}
						candidate, marked = connectbyStoredRowMark(read, source.Relation, source.Mark, active)
					}
					if marked {
						mode, present, firstOrder = candidate, true, source.Mark.Order
					}
				}
				if present {
					return
				}
			}
		}, func(expr.Expr) {})
	}
	if present {
		tag := "SELECT"
		if mode != 0 {
			tag += " " + analyze.RowLockClause(mode)
		}
		return tag
	}
	if parser.HasModifyingCTE(statement) {
		return "SELECT"
	}
	return ""
}

// Transaction-capable loops require the canonical ONE_SELECT strategy, not
// SPI's stricter volatility test: row-marked SELECT is allowed, writable WITH
// and DML RETURNING are not.
func plLoopReadOnly(statement parser.Statement, root plan.Node) bool {
	if _, ok := statement.(*parser.Select); !ok || root == nil {
		return false
	}
	readOnly := true
	plan.VisitExecutableQuery(root, func(node plan.Node) {
		if project, ok := node.(*plan.ProjectNode); ok && project.Syntax != nil && parser.HasModifyingCTE(project.Syntax) {
			readOnly = false
		}
	}, func(expr.Expr) {})
	return readOnly
}

func connectbyEarlierMark(candidate, current int) bool {
	return candidate > 0 && (current == 0 || candidate < current)
}

// Presence is separate from strength: SPI rejects a planned reference/COPY
// marker too, while its LCS_NONE strength renders the plain SELECT command tag.
// Inherited and stored tuple strengths still merge along the occurrence path.
func connectbyStoredRowMark(read catalog.Read, relation *catalog.Relation, inherited plan.RowMark, active map[ids.ObjectAddress]bool) (lock.Mode, bool) {
	if relation.RelationKind != catalog.ViewRelation {
		return inherited.Mode, inherited.Mode != 0
	}
	if active[relation.Address()] {
		return 0, false
	}
	active[relation.Address()] = true
	defer delete(active, relation.Address())
	definition := stored.ViewOf(relation)
	syntax := definition.Syntax()
	// These represented stored-query boundaries are retained by PostgreSQL's
	// pullup rules. An enclosing mark therefore produces a parent COPY marker
	// before the descendant tuple marks. Merely inherited locking on a plain
	// view does not create this boundary: it can still propagate to its base.
	if inherited.Mode != 0 && (len(syntax.Locking) != 0 || syntax.Order != nil || syntax.Bounds != nil || len(syntax.With) != 0 || definition.Barrier()) {
		return 0, true
	}
	var mode lock.Mode
	present := false
	plan.VisitExecutableQuery(definition.Query().Root, func(node plan.Node) {
		project, ok := node.(*plan.ProjectNode)
		if !ok || project.Syntax == nil || present {
			return
		}
		from := analyze.ViewFromRoot(project)
		firstOrder := 0
		for pass := range 2 {
			for occurrence, leaf := range definition.Sources() {
				if leaf.AnalysisOnly || !connectbyLocalSource(from, occurrence) {
					continue
				}
				mark := stored.ViewLeafRowMark(definition, occurrence, inherited)
				if (mark.Mode != 0) != (pass == 0) || present && !connectbyEarlierMark(mark.Order, firstOrder) {
					continue
				}
				if child, ok := read.Relation(leaf.Ref.Address); ok {
					if candidate, marked := connectbyStoredRowMark(read, child, mark, active); marked {
						mode, present, firstOrder = candidate, true, mark.Order
					}
				}
			}
			if present {
				return
			}
		}
	}, func(expr.Expr) {})
	return mode, present
}

// Frozen leaves have global occurrence indexes, but first-mark orders restart
// at each SELECT. Derive local ownership from the retained bound FROM graph.
func connectbyLocalSource(node plan.Node, occurrence int) bool {
	switch node := node.(type) {
	case *plan.SourceNode:
		return node.Occurrence == occurrence
	case *plan.DerivedNode, *plan.ProjectNode:
		return false
	}
	if node == nil {
		return false
	}
	left, right := plan.QueryInputs(node)
	return connectbyLocalSource(left, occurrence) || connectbyLocalSource(right, occurrence)
}
