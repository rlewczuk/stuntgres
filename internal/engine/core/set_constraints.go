package core

import (
	"fmt"
	"maps"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func (s *StatementScope) checkConstraintEvents(all bool, selected map[ids.OID]bool) error {
	return s.checkConstraintCommandEvents(all, selected, false)
}

func (s *StatementScope) checkConstraintCommandEvents(all bool, selected map[ids.OID]bool, bounded bool) error {
	for {
		if err := s.checkConstraintEventPhase(all, selected, bounded, false); err != nil {
			return err
		}
		if err := s.checkConstraintEventPhase(all, selected, bounded, true); err != nil {
			return err
		}
		fired, err := s.drainConstraintTriggers(all, selected)
		if err != nil || !fired {
			return err
		}
	}
}

// Index events precede RI actions; FK events follow them. Both lanes belong to
// the same transaction state and savepoint mark, with no retained command graph.
func (s *StatementScope) checkConstraintEventPhase(all bool, selected map[ids.OID]bool, bounded, foreign bool) error {
	e := s.Engine()
	for {
		modes := s.constraintModes()
		at := -1
		var event index.ConstraintEvent
		indexLive := false
		for i, candidate := range modes.Events {
			if candidate.UserTrigger != nil || (candidate.ForeignKey != nil) != foreign {
				continue
			}
			if bounded {
				accepted := false
				for _, effect := range s.foreignActionsOrZero().Transitions {
					if effect.Command == s.effectCommand && effect.Relation == candidate.Parent.Address &&
						(effect.Next.Row == candidate.Row && effect.Next.Version == candidate.Version || effect.Old.Row == candidate.Row && effect.Old.Version == candidate.Version) {
						accepted = true
						break
					}
				}
				if !accepted {
					continue
				}
			}
			if candidate.Parent.Epoch != s.TxRef.Epoch() {
				return ids.ErrCatalogReplaced
			}
			deferrable, initiallyDeferred := false, false
			if foreign {
				deferrable, initiallyDeferred = candidate.ForeignKey.Definition.Deferrable, candidate.ForeignKey.Definition.InitiallyDeferred
			} else {
				e.Mu.Lock()
				view := e.PrivateCatalogLocked(s.PendingCatalog())
				parent, live := view.Relation(candidate.Parent.Address)
				idx, exists := view.Relation(candidate.Index.Address)
				e.Mu.Unlock()
				if candidate.Parent.Epoch != view.Epoch() {
					return ids.ErrCatalogReplaced
				}
				indexLive = live && exists && idx.SQLIndex != nil && idx.SQLIndex.Constraint != nil && idx.SQLIndex.Constraint.OID == candidate.Constraint
				if indexLive {
					_, deferrable, initiallyDeferred, indexLive = index.IndexConstraintTiming(parent, idx)
				}
			}
			if all || selected != nil && selected[candidate.Constraint] || selected == nil && !modes.Deferred(candidate.Constraint, deferrable, initiallyDeferred) {
				at, event = i, candidate
				break
			}
		}
		if at < 0 {
			return nil
		}
		if foreign {
			if err := dml.CheckDeferredForeignKey(s.WriteHost(), event); err != nil {
				return err
			}
		} else if indexLive {
			binding, err := dml.BindUniqueWrite(s.WriteHost(), event.Parent)
			if err != nil {
				return err
			}
			for i := range binding.Keys {
				bound := &binding.Keys[i]
				if bound.Index.Address() != event.Index.Address {
					continue
				}
				conflict, _, err := dml.ProbeExclusion(s.ActivateRuntime(s.executionContext()), s.WriteHost(), event.Parent, binding, bound, storage.RowVersion{Row: event.Row, Version: event.Version}, true, index.ProbeRuntime)
				if err != nil {
					return err
				}
				if conflict.Index != nil {
					return conflict.Diagnostic(s.WriteHost(), event.Parent)
				}
				break
			}
		}
		// Providers may have changed modes, consumed work or appended tails.
		// Remove only this checked membership from the current live vector.
		for i, current := range s.constraintModes().Events {
			if current == event {
				s.beginConstraintEvents().ClaimConstraintEvent(i)
				break
			}
		}
	}
}

// HasPendingConstraintEvents reads the effective common event vector, including
// repaired or superseded obligations until consumed. Parent is the transition's
// physical relation, not every endpoint of an FK. The admitted caller owns
// statement serialization and may hold Engine.Mu; this never locks or flushes.
func (s *StatementScope) HasPendingConstraintEvents(address ids.ObjectAddress) bool {
	modes := ownerExtensionOrZero(s.Owner).constraintModes
	if s.IndexesOrZero().ConstraintsStarted {
		modes = s.IndexesOrZero().ConstraintModes
	}
	for _, event := range modes.Events {
		if event.Parent.Address == address {
			return true
		}
	}
	for _, relation := range s.activeConstraintTriggers {
		if relation == address {
			return true
		}
	}
	if state := s.afterTriggers; state != nil {
		for _, frame := range state.frames {
			for _, event := range frame.rows[frame.row:] {
				if !event.skip && event.relation.Address() == address && event.definition.Parts().Constraint.OID != 0 {
					return true
				}
			}
		}
	}
	return false
}

func (s *StatementScope) executeSetConstraints(raw *parser.SetConstraints, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	if s.Expected != nil {
		if err := CheckStatementSignature(*s.Expected, Description{}); err != nil {
			return Result{}, err
		}
	}
	retain := s.Owner.InBlock() || s.activeSQLRoutineDepth != 0
	if !retain {
		s.Op.EmitNotice(session.LevelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "25P01", Message: "SET CONSTRAINTS can only be used in transaction blocks"})
	}
	e := s.Engine()
	auth, err := OperationAuthorization(s.Op)
	if err != nil {
		return Result{}, err
	}
	e.Mu.Lock()
	auth.Catalog = e.PrivateCatalogLocked(s.PendingCatalog())
	auth.Recapture(auth.Catalog)
	s.BeginExclusionProjectionLocked(e)
	e.Mu.Unlock()
	candidate := s.IndexesOrZero().ConstraintModes
	if raw.All {
		candidate.AllSet, candidate.AllDeferred = true, raw.Deferred
		candidate.ByConstraint = nil
	} else {
		selected := make(map[ids.OID]bool)
		for _, name := range raw.Names {
			path, err := e.LookupNamespaces(auth, name, analyze.OperatorLookup)
			if err != nil {
				return Result{}, index.SemanticError(err)
			}
			label := name.Parts[len(name.Parts)-1].Text
			if err := index.SelectConstraintName(auth.Catalog, path, label, raw.Deferred, selected); err != nil {
				return Result{}, err
			}
		}
		index.SelectInheritedConstraints(auth.Catalog, selected)
		if len(selected) != 0 {
			candidate.ByConstraint = maps.Clone(candidate.ByConstraint)
			if candidate.ByConstraint == nil {
				candidate.ByConstraint = make(map[ids.OID]bool, len(selected))
			}
		}
		for oid := range selected {
			candidate.ByConstraint[oid] = raw.Deferred
		}
	}
	// Install before callbacks. Generated work and nested SET use this state;
	// statement/exception rollback restores the complete previous COW frontier.
	s.indexes().ConstraintModes = candidate
	if !raw.Deferred {
		if err := s.checkConstraintEvents(false, nil); err != nil {
			return Result{}, err
		}
	}
	if !retain {
		scope := s.indexes()
		scope.ConstraintModes.AllSet, scope.ConstraintModes.AllDeferred = false, false
		scope.ConstraintModes.ByConstraint = nil
	}
	return Result{}, nil
}
