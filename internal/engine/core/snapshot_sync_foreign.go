package core

import (
	"errors"
	"slices"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
)

var errExportRootDemand = errors.New("stuntgres: retained export-root demand")

func expressionExportRootDemand(read catalog.Read, node expr.Expr) bool {
	if demand, handled := exec.AcceptedDefaultExpressionExportRootDemand(read, node); handled {
		return demand
	}
	if demand, ok := node.(expr.ExportRootDemander); ok && demand.ExportRootDemand() {
		return true
	}
	if call, ok := node.(interface{ CallRoutineReference() expr.RoutineReference }); ok {
		if reference := call.CallRoutineReference(); reference != nil {
			if routine, ok := read.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(reference.RoutineOID())}); ok {
				return exec.RoutineExportRootDemand(read, routine)
			}
		}
	}
	return false
}

func retainedWriteExportRootDemand(read catalog.Read, defaults []expr.Expr, relation *catalog.Relation, event stored.TriggerEvent) bool {
	if event&stored.TriggerDelete != 0 && event&(stored.TriggerInsert|stored.TriggerUpdate) == 0 {
		return false
	}
	visit := func(node expr.Expr) error {
		if expressionExportRootDemand(read, node) { return errExportRootDemand }
		return nil
	}
	return analyze.VisitRetainedWriteExpressions(defaults, relation.ColumnMeta.Generations, relation.Constraints.Check, visit) != nil
}

func triggerExportRootDemand(read catalog.Read, relation *catalog.Relation, event stored.TriggerEvent, attributes []int32) bool {
	for _, trigger := range stored.TriggersOf(relation) {
		parts := trigger.Parts()
		if trigger.Mode() == catalog.TriggerDisabled { continue }
		// An opaque UPDATE/MERGE recipe has no column set yet. It may
		// reach UPDATE OF triggers; an explicitly empty set still cannot.
		if attributes == nil && event&stored.TriggerUpdate != 0 {
			if parts.Events&event == 0 { continue }
		} else if !trigger.Fires(event, attributes) { continue }
		if parts.Binding != nil && parts.Binding.ExportRoots { return true }
		if parts.Binding != nil {
			for _, dependency := range parts.Binding.Dependencies {
				if routine, ok := read.Procedure(dependency.Ref.Address); ok && exec.RoutineExportRootDemand(read, routine) { return true }
			}
		}
		if routine, ok := read.Procedure(parts.Procedure.Address); ok && exec.RoutineExportRootDemand(read, routine) { return true }
	}
	return false
}

func bindingMutationEvent(b *analyze.Binding, statement parser.Statement) stored.TriggerEvent {
	switch command := statement.(type) {
	case *parser.Insert: return stored.TriggerInsert
	case *parser.Update: return stored.TriggerUpdate
	case *parser.Delete: return stored.TriggerDelete
	case *parser.Merge: return stored.TriggerInsert | stored.TriggerUpdate | stored.TriggerDelete
	case *parser.Copy:
		if command.From { return stored.TriggerInsert }
	}
	if b.Merge != nil { return stored.TriggerInsert | stored.TriggerUpdate | stored.TriggerDelete }
	if b.Update != nil { return stored.TriggerUpdate }
	return 0
}

type foreignExportVisit struct {
	child ids.ObjectAddress
	constraint ids.OID
	event stored.TriggerEvent
}

// foreignActionExportRootDemand follows only the current derived incoming
// action index. Ordinary no-FK admission neither scans relations nor allocates
// candidate/visited collections. Classification never checks child privileges
// or executes a recipe, so lazy RI diagnostics and atomicity remain unchanged.
func foreignActionExportRootDemand(read catalog.Read, parent *catalog.Relation, event stored.TriggerEvent, attributes []int32, slots []int) bool {
	if !read.HasIncomingForeignActionOwners(parent.Address()) {
		return false
	}
	var visited []foreignExportVisit
	var walk func(*catalog.Relation, stored.TriggerEvent, []int32, []int) bool
	walk = func(parent *catalog.Relation, event stored.TriggerEvent, attributes []int32, slots []int) bool {
		for _, child := range read.IncomingForeignActionOwners(parent.Address()) {
			for _, f := range child.Constraints.ForeignKeys {
				if f.NotEnforced || f.Target != parent.Address() { continue }
				for _, kind := range [...]stored.TriggerEvent{stored.TriggerDelete, stored.TriggerUpdate} {
					if event&kind == 0 { continue }
					triggerSlot := 0
					if kind == stored.TriggerUpdate { triggerSlot = 1 }
					if f.TriggerMode(triggerSlot) == catalog.TriggerDisabled { continue }
					action := f.DeleteAction
					if kind == stored.TriggerUpdate {
						action = f.UpdateAction
						if attributes != nil || slots != nil {
							changed := false
							for _, referenced := range f.Referenced {
								changed = changed || slices.Contains(attributes, referenced)
								for _, slot := range slots { changed = changed || slot >= 0 && slot < len(parent.Attributes) && parent.Attributes[slot] == referenced }
							}
							if !changed { continue }
						}
					}
					if !catalog.MutatingForeignAction(action) { continue }
					identity := foreignExportVisit{child.Address(), f.OID, kind}
					if slices.Contains(visited, identity) { continue }
					visited = append(visited, identity)
					childEvent, targets := stored.TriggerUpdate, f.Attributes
					if action == parser.ForeignKeyCascade && kind == stored.TriggerDelete { childEvent, targets = stored.TriggerDelete, nil }
					if kind == stored.TriggerDelete && len(f.DeleteAttributes) != 0 { targets = f.DeleteAttributes }
					if action == parser.ForeignKeySetDefault {
						for _, attribute := range targets {
							slot := slices.Index(child.Attributes, attribute)
							if slot < 0 { continue }
							node, _ := analyze.ColumnDefault(read, child, slot)
							var defaults [1]expr.Expr
							defaults[0] = node
							if analyze.VisitRetainedWriteExpressions(defaults[:], nil, nil, func(n expr.Expr) error { if expressionExportRootDemand(read, n) { return errExportRootDemand }; return nil }) != nil { return true }
						}
					}
					if retainedWriteExportRootDemand(read, nil, child, childEvent) || triggerExportRootDemand(read, child, childEvent, targets) || walk(child, childEvent, targets, nil) { return true }
				}
			}
		}
		return false
	}
	return walk(parent, event, attributes, slots)
}

// COPY resolves its supplied target slots before selecting the active pair.
// Inspect only omitted defaults here; ordinary input/authorization and lazy
// default preparation remain in initCopyInputLocked.
func (s *StatementScope) installCopyExportDemand(read catalog.Read, b *analyze.Binding) {
	s.installExportDemand(b)
	if s.exportRootDemand { return }
	for slot := range b.Relation.Columns {
		if slices.Contains(b.Targets, slot) { continue }
		node, _ := analyze.ColumnDefault(read, b.Relation, slot)
		var defaults [1]expr.Expr
		defaults[0] = node
		if analyze.VisitRetainedWriteExpressions(defaults[:], nil, nil, func(n expr.Expr) error {
			if expressionExportRootDemand(read, n) { return errExportRootDemand }
			return nil
		}) != nil {
			s.exportRootDemand = true
			return
		}
	}
}
