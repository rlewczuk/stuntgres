package engine

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

func (s *StatementScope) checkExclusionEvents(all bool, selected map[ids.OID]bool) error {
	return s.checkExclusionCommandEvents(all, selected, false)
}

func (s *StatementScope) checkExclusionCommandEvents(all bool, selected map[ids.OID]bool, bounded bool) error {
	if !s.IndexesOrZero().ExclusionStarted {
		s.indexes().ExclusionModes = ownerExtensionOrZero(s.Owner).exclusionModes
	}
	if len(s.IndexesOrZero().ExclusionModes.Events) == 0 {
		return nil
	}
	remaining := make([]index.ExclusionEvent, 0, len(s.IndexesOrZero().ExclusionModes.Events))
	e := s.Engine()
	for _, event := range s.IndexesOrZero().ExclusionModes.Events {
		if bounded {
			owned := false
			for _, effect := range s.foreignActionsOrZero().Transitions {
				if effect.Command == s.effectCommand && effect.Relation == event.Parent.Address && effect.Next.Row == event.Row && effect.Next.Version == event.Version {
					owned = true
					break
				}
			}
			if !owned {
				remaining = append(remaining, event)
				continue
			}
		}
		e.Mu.Lock()
		view := e.PrivateCatalogLocked(s.Owner.Catalog)
		parent, live := view.Relation(event.Parent.Address)
		idx, exists := view.Relation(event.Index.Address)
		e.Mu.Unlock()
		if event.Parent.Epoch != view.Epoch() {
			return ids.ErrCatalogReplaced
		}
		if !live || !exists || idx.SQLIndex == nil || !idx.SQLIndex.IsExclusion() || idx.SQLIndex.Constraint.OID != event.Constraint {
			continue
		}
		c := index.ExclusionDefinition(parent, idx)
		if c == nil {
			continue
		}
		due := all || selected != nil && selected[c.OID] || selected == nil && !s.IndexesOrZero().ExclusionModes.Deferred(c)
		if !due {
			remaining = append(remaining, event)
			continue
		}
		binding, err := dml.BindUniqueWrite(s.WriteHost(), event.Parent)
		if err != nil {
			return err
		}
		var bound *index.UniqueBoundKey
		for i := range binding.Keys {
			if binding.Keys[i].Index.Address() == event.Index.Address {
				bound = &binding.Keys[i]
				break
			}
		}
		if bound == nil {
			continue
		}
		conflict, _, err := dml.ProbeExclusion(s.WriteHost(), event.Parent, binding, bound, storage.RowVersion{Row: event.Row, Version: event.Version}, true, index.ProbeRuntime)
		if err != nil {
			return err
		}
		if conflict.Index != nil {
			return conflict.Diagnostic(s.WriteHost(), event.Parent)
		}
	}
	s.indexes().ExclusionModes.Events = remaining
	return nil
}

func (s *StatementScope) executeSetConstraints(raw *parser.SetConstraints, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	if s.Expected != nil {
		if err := checkStatementSignature(*s.Expected, StatementDescription{}); err != nil {
			return Result{}, err
		}
	}
	if !s.Owner.InBlock() {
		s.Op.EmitNotice(session.LevelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "25P01", Message: "SET CONSTRAINTS can only be used in transaction blocks"})
	}
	e := s.Engine()
	auth, err := OperationAuthorization(s.Op)
	if err != nil {
		return Result{}, err
	}
	e.Mu.Lock()
	auth.Catalog = e.PrivateCatalogLocked(s.Owner.Catalog)
	auth.Recapture(auth.Catalog)
	s.BeginExclusionProjectionLocked(e)
	e.Mu.Unlock()
	candidate := s.IndexesOrZero().ExclusionModes
	candidate.ByConstraint = maps.Clone(candidate.ByConstraint)
	selected := make(map[ids.OID]bool)
	if raw.All {
		candidate.AllSet, candidate.AllDeferred = true, raw.Deferred
		candidate.ByConstraint = nil
	} else {
		for _, name := range raw.Names {
			path, err := e.lookupNamespaces(auth, name, analyze.OperatorLookup)
			if err != nil {
				return Result{}, index.SemanticError(err)
			}
			label := name.Parts[len(name.Parts)-1].Text
			if err := index.SelectConstraintName(auth.Catalog, path, label, raw.Deferred, selected); err != nil {
				return Result{}, err
			}
		}
		index.SelectInheritedExclusions(auth.Catalog, selected)
		if candidate.ByConstraint == nil {
			candidate.ByConstraint = make(map[ids.OID]bool, len(selected))
		}
		for oid := range selected {
			candidate.ByConstraint[oid] = raw.Deferred
		}
	}
	if !raw.Deferred {
		if err := s.checkExclusionEvents(raw.All, selected); err != nil {
			return Result{}, err
		}
		candidate.Events = s.IndexesOrZero().ExclusionModes.Events
	}
	if s.Owner.InBlock() {
		s.indexes().ExclusionModes = candidate
	}
	return Result{}, nil
}
