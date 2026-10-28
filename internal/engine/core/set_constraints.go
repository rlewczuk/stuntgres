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
	if err := s.checkConstraintEventPhase(all, selected, bounded, false); err != nil {
		return err
	}
	return s.checkConstraintEventPhase(all, selected, bounded, true)
}

// Index events precede RI actions; FK events follow them. Both lanes belong to
// the same transaction state and savepoint mark, with no retained command graph.
func (s *StatementScope) checkConstraintEventPhase(all bool, selected map[ids.OID]bool, bounded, foreign bool) error {
	modes := s.IndexesOrZero().ConstraintModes
	if !s.IndexesOrZero().ConstraintsStarted {
		modes = ownerExtensionOrZero(s.Owner).constraintModes
	}
	events := modes.Events
	if len(events) == 0 {
		return nil
	}
	var remaining []index.ConstraintEvent
	removed := false
	e := s.Engine()
	for at, event := range events {
		keep := (event.ForeignKey != nil) != foreign
		if !keep && bounded {
			keep = true
			for _, effect := range s.foreignActionsOrZero().Transitions {
				if effect.Command == s.effectCommand && effect.Relation == event.Parent.Address &&
					(effect.Next.Row == event.Row && effect.Next.Version == event.Version || effect.Old.Row == event.Row && effect.Old.Version == event.Version) {
					keep = false
					break
				}
			}
		}
		if keep {
			if removed {
				remaining = append(remaining, event)
			}
			continue
		}
		if event.Parent.Epoch != s.TxRef.Epoch() {
			return ids.ErrCatalogReplaced
		}
		deferrable, initiallyDeferred := false, false
		var binding *index.UniqueWriteBinding
		var bound *index.UniqueBoundKey
		if foreign {
			deferrable, initiallyDeferred = event.ForeignKey.Definition.Deferrable, event.ForeignKey.Definition.InitiallyDeferred
		} else {
			e.Mu.Lock()
			view := e.PrivateCatalogLocked(s.PendingCatalog())
			parent, live := view.Relation(event.Parent.Address)
			idx, exists := view.Relation(event.Index.Address)
			e.Mu.Unlock()
			if event.Parent.Epoch != view.Epoch() {
				return ids.ErrCatalogReplaced
			}
			if live && exists && idx.SQLIndex != nil && idx.SQLIndex.Constraint != nil && idx.SQLIndex.Constraint.OID == event.Constraint {
				_, deferrable, initiallyDeferred, exists = index.IndexConstraintTiming(parent, idx)
			} else {
				exists = false
			}
			if exists {
				due := all || selected != nil && selected[event.Constraint] || selected == nil && !modes.Deferred(event.Constraint, deferrable, initiallyDeferred)
				if !due {
					if removed {
						remaining = append(remaining, event)
					}
					continue
				}
				var err error
				binding, err = dml.BindUniqueWrite(s.WriteHost(), event.Parent)
				if err != nil {
					return err
				}
				for i := range binding.Keys {
					if binding.Keys[i].Index.Address() == event.Index.Address {
						bound = &binding.Keys[i]
						break
					}
				}
			}
		}
		due := all || selected != nil && selected[event.Constraint] || selected == nil && !modes.Deferred(event.Constraint, deferrable, initiallyDeferred)
		if !due {
			if removed {
				remaining = append(remaining, event)
			}
			continue
		}
		if foreign {
			if err := dml.CheckDeferredForeignKey(s.WriteHost(), event); err != nil {
				return err
			}
		} else if bound != nil {
			conflict, _, err := dml.ProbeExclusion(s.WriteHost(), event.Parent, binding, bound, storage.RowVersion{Row: event.Row, Version: event.Version}, true, index.ProbeRuntime)
			if err != nil {
				return err
			}
			if conflict.Index != nil {
				return conflict.Diagnostic(s.WriteHost(), event.Parent)
			}
		}
		if !removed {
			remaining = make([]index.ConstraintEvent, at, len(events))
			copy(remaining, events[:at])
			removed = true
		}
	}
	if removed {
		scope := s.indexes()
		scope.ConstraintModes = modes
		scope.ConstraintModes.Events = remaining
		scope.ConstraintsStarted = true
	}
	return nil
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
	candidate.ByConstraint = maps.Clone(candidate.ByConstraint)
	selected := make(map[ids.OID]bool)
	if raw.All {
		candidate.AllSet, candidate.AllDeferred = true, raw.Deferred
		candidate.ByConstraint = nil
	} else {
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
		if candidate.ByConstraint == nil {
			candidate.ByConstraint = make(map[ids.OID]bool, len(selected))
		}
		for oid := range selected {
			candidate.ByConstraint[oid] = raw.Deferred
		}
	}
	if !raw.Deferred {
		if err := s.checkConstraintEvents(raw.All, selected); err != nil {
			return Result{}, err
		}
		candidate.Events = s.IndexesOrZero().ConstraintModes.Events
	}
	if retain {
		s.indexes().ConstraintModes = candidate
	}
	return Result{}, nil
}
