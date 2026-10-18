package engine

import (
	"fmt"
	"maps"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func (s *StatementScope) checkExclusionEvents(all bool, selected map[ids.OID]bool) error {
	if !s.exclusionStarted {
		s.exclusionModes = s.owner.exclusionModes
	}
	if len(s.exclusionModes.events) == 0 {
		return nil
	}
	remaining := make([]exclusionEvent, 0, len(s.exclusionModes.events))
	e := s.op.session.Engine()
	for _, event := range s.exclusionModes.events {
		e.mu.Lock()
		view := e.privateCatalogLocked(s.owner.catalog)
		parent, live := view.Relation(event.parent.Address)
		index, exists := view.Relation(event.index.Address)
		e.mu.Unlock()
		if event.parent.Epoch != view.Epoch() {
			return ids.ErrCatalogReplaced
		}
		if !live || !exists || index.SQLIndex == nil || !index.SQLIndex.IsExclusion() || index.SQLIndex.Constraint.OID != event.constraint {
			continue
		}
		c := exclusionDefinition(parent, index)
		if c == nil {
			continue
		}
		due := all || selected != nil && selected[c.OID] || selected == nil && !s.exclusionModes.deferred(c)
		if !due {
			remaining = append(remaining, event)
			continue
		}
		binding, err := s.bindUniqueWrite(event.parent)
		if err != nil {
			return err
		}
		var bound *uniqueBoundKey
		for i := range binding.keys {
			if binding.keys[i].index.Address() == event.index.Address {
				bound = &binding.keys[i]
				break
			}
		}
		if bound == nil {
			continue
		}
		conflict, _, err := s.probeExclusion(event.parent, binding, bound, rowVersion{row: event.row, version: event.version}, true, indexProbeRuntime)
		if err != nil {
			return err
		}
		if conflict.index != nil {
			return conflict.diagnostic(s, event.parent)
		}
	}
	s.exclusionModes.events = remaining
	return nil
}
func (e *Engine) checkTransactionExclusions(s *StatementScope) error {
	if err := s.checkExclusionEvents(true, nil); err != nil {
		return err
	}
	s.owner.exclusionModes = s.exclusionModes
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
	if !s.owner.inBlock() {
		s.op.emitNotice(levelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "25P01", Message: "SET CONSTRAINTS can only be used in transaction blocks"})
	}
	e := s.op.session.Engine()
	auth, err := s.op.Authorization()
	if err != nil {
		return Result{}, err
	}
	e.mu.Lock()
	auth.catalog = e.privateCatalogLocked(s.owner.catalog)
	auth.recaptureCatalog(auth.catalog)
	s.beginExclusionProjectionLocked(e)
	e.mu.Unlock()
	candidate := s.exclusionModes
	candidate.byConstraint = maps.Clone(candidate.byConstraint)
	selected := make(map[ids.OID]bool)
	if raw.All {
		candidate.allSet, candidate.allDeferred = true, raw.Deferred
		candidate.byConstraint = nil
	} else {
		for _, name := range raw.Names {
			path, err := e.lookupNamespaces(auth, name, operatorLookup)
			if err != nil {
				return Result{}, indexSemanticError(err)
			}
			label := name.Parts[len(name.Parts)-1].Text
			found := false
			for i := range path.count() {
				namespace := path.at(i)
				matched := false
				for _, descriptor := range auth.catalog.Descriptors() {
					parent, ok := descriptor.(*relationDescriptor)
					if !ok || parent.Namespace() != namespace || parent.RelationKind != tableRelation && parent.RelationKind != partitionedRelation {
						continue
					}
					for _, c := range parent.ExclusionConstraints {
						if c.Name != label {
							continue
						}
						matched = true
						if raw.Deferred && !c.Deferrable {
							return Result{}, sqlError("42809", fmt.Sprintf(`constraint "%s" is not deferrable`, label))
						}
						if c.Deferrable {
							selected[c.OID] = true
						}
					}
					nondeferrable := false
					for _, c := range parent.UniqueConstraints {
						if c.Name == label {
							nondeferrable = true
						}
					}
					for _, c := range parent.CheckConstraints {
						if c.Name == label {
							nondeferrable = true
						}
					}
					for _, c := range parent.NotNullConstraints {
						if c.Name == label {
							nondeferrable = true
						}
					}
					if nondeferrable {
						matched = true
						if raw.Deferred {
							return Result{}, sqlError("42809", fmt.Sprintf(`constraint "%s" is not deferrable`, label))
						}
					}
				}
				if matched {
					found = true
					break
				}
			}
			if !found {
				return Result{}, sqlError("42704", fmt.Sprintf(`constraint "%s" does not exist`, label))
			}
		}
		// Names select their inherited provider subtree, not similarly named
		// independent local declarations. Catalog iteration has no tree order.
		for changed := true; changed; {
			changed = false
			for _, descriptor := range auth.catalog.Descriptors() {
				parent, ok := descriptor.(*relationDescriptor)
				if !ok {
					continue
				}
				for _, c := range parent.ExclusionConstraints {
					if c.Parent != 0 && selected[c.Parent] && !selected[c.OID] {
						selected[c.OID] = true
						changed = true
					}
				}
			}
		}
		if candidate.byConstraint == nil {
			candidate.byConstraint = make(map[ids.OID]bool, len(selected))
		}
		for oid := range selected {
			candidate.byConstraint[oid] = raw.Deferred
		}
	}
	if !raw.Deferred {
		if err := s.checkExclusionEvents(raw.All, selected); err != nil {
			return Result{}, err
		}
		candidate.events = s.exclusionModes.events
	}
	if s.owner.inBlock() {
		s.exclusionModes = candidate
	}
	return Result{}, nil
}

// endLockOwner retires dirty exclusion visibility, then releases the ticket's
// lock ownership, including its completion resource. Neither Engine.mu nor
// the manager mutex is held by the caller.
func (e *Engine) endLockOwner(ticket *lock.Ticket) {
	if ticket != nil && ticket.Ref().Domain() != nil {
		e.removeExclusionOwner(ticket)
	}
	e.locks.End(ticket)
}

func (e *Engine) removeExclusionOwner(ticket *lock.Ticket) {
	e.mu.Lock()
	if _, exists := e.exclusionPending[ticket]; exists {
		delete(e.exclusionPending, ticket)
		e.exclusionGeneration++
	}
	e.mu.Unlock()
}
