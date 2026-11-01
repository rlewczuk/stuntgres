package core

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
)

// installExportDemand consumes the same bound recipes and consumed routine
// graph as ordinary command admission, before any active data snapshot.
func (s *StatementScope) installExportDemand(b *analyze.Binding) {
	s.installExportDemandFor(b, s.StatementMutation.Statement)
}

func (s *StatementScope) installExportDemandFor(b *analyze.Binding, statement parser.Statement) {
	s.exportRootDemand = s.exportRootDemand || b.ExportRoots
	if b.Query != nil { s.exportRootDemand = s.exportRootDemand || b.Query.ExportRoots }
	if b.Returning != nil { s.exportRootDemand = s.exportRootDemand || b.Returning.ExportRoots }
	if s.exportRootDemand { return }
	for _, definition := range b.CTEs {
		if mutation, ok := definition.Mutation.(*analyze.Binding); ok {
			s.installExportDemandFor(mutation, definition.Statement)
		} else if definition.Query != nil {
			s.exportRootDemand = s.exportRootDemand || definition.Query.ExportRoots
		}
		if s.exportRootDemand { return }
	}
	read := s.Engine().PrivateCatalogLocked(s.PendingCatalog())
	// Execute has selected this exact portal before default preflight chooses
	// its wrappers. Retain the selected executable's roots at the first pair,
	// not later at invocation. Invalid ownership remains only possible demand;
	// selectAcceptedDefault retains the incumbent diagnostic boundary.
	if portal := s.protocolOrZero().execute; portal != nil && portal.defaults != nil {
		if portal.Retired || portal.Transaction != s.TxRef || portal.Folds == nil ||
			!portal.defaults.MatchesGeneration(portal.Folds.Generation) || portal.defaults.ExportRootDemand(read) {
			s.exportRootDemand = true
			return
		}
	}
	for _, dependency := range b.Dependencies {
		if routine, ok := read.Procedure(dependency.Ref.Address); ok && exec.RoutineExportRootDemand(read, routine) { s.exportRootDemand = true; return }
	}
	for _,source:=range b.QuerySources{
		if source.AnalysisOnly{continue}
		if relation,ok:=read.Relation(source.Ref.Address);ok&&stored.RelationReadExportRootDemand(read,relation){s.exportRootDemand=true;return}
	}
	if b.Relation != nil {
		event := bindingMutationEvent(b, statement)
		if event != 0 {
			s.exportRootDemand = mutationRelationExportRootDemand(read, b, b.Relation, event, b.Insert.Defaults)
			if s.exportRootDemand { return }
			// Use the already admitted occurrence, not a fresh catalog hierarchy
			// walk (and not the active sibling CTE's mutation manifest).
			mutation := &s.StatementMutation
			if statement != mutation.Statement {
				if index := s.mutationIndex(statement); index >= 0 {
					if index != s.ActiveMutation { mutation = &s.mutations[index] }
				} else if len(s.mutations)!=0 || mutation.LockedRef!=b.Ref {
					mutation = nil
				}
			}
			if mutation != nil {
				for _, target := range mutation.InheritedTargets {
					if mutationRelationExportRootDemand(read, b, target.Relation, event, nil) {
						s.exportRootDemand = true
						return
					}
				}
			}
		}
	}
	for _, program := range s.routinePrograms {
		if program.admission.ExportRootDemand(read) { s.exportRootDemand = true; return }
	}
}

// Descendants can have different physical column orders and their own retained
// recipes, triggers and incoming actions. Translate known targets by name;
// nil attributes deliberately denotes an unknown UPDATE target set.
func mutationRelationExportRootDemand(read catalog.Read, b *analyze.Binding, relation *catalog.Relation, event stored.TriggerEvent, defaults []expr.Expr) bool {
	if retainedWriteExportRootDemand(read, defaults, relation, event) { return true }
	if relation == b.Relation && !relation.HasTriggers() {
		return foreignActionExportRootDemand(read, relation, event, nil, b.Targets)
	}
	var buffer [32]int32
	var attributes []int32
	if b.Targets != nil {
		attributes = buffer[:0]
		if len(b.Targets) > len(buffer) { attributes = make([]int32, 0, len(b.Targets)) }
		for _, slot := range b.Targets {
			if slot < 0 || slot >= len(b.Relation.Columns) { continue }
			name := b.Relation.Columns[slot].Name
			for childSlot, column := range relation.Columns {
				if column.Name == name {
					attributes = append(attributes, relation.Attributes[childSlot])
					break
				}
			}
		}
	}
	return triggerExportRootDemand(read, relation, event, attributes) ||
		foreignActionExportRootDemand(read, relation, event, attributes, nil)
}
