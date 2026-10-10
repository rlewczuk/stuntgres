package engine

import (
	"fmt"
	"maps"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (s *StatementScope) checkExclusionEvents(all bool, selected map[OID]bool) error {
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
		parent, live := view.state.descriptors[event.parent.Address].(*relationDescriptor)
		index, exists := view.state.descriptors[event.index.Address].(*relationDescriptor)
		e.mu.Unlock()
		if event.parent.Epoch != view.Epoch() {
			return ErrCatalogReplaced
		}
		if !live || !exists || index.sqlIndex == nil || !index.sqlIndex.isExclusion() || index.sqlIndex.constraint.OID != event.constraint {
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
			if binding.keys[i].index.address == event.index.Address {
				bound = &binding.keys[i]
				break
			}
		}
		if bound == nil {
			continue
		}
		conflict, _, err := s.probeExclusion(event.parent, binding, bound, rowVersion{row: event.row, version: event.version}, true)
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
func (e *Engine) checkTransactionExclusions(op *Operation, t *transactionOwner) error {
	input, output := sessionPresentation(op.session)
	s := &StatementScope{owner: t, op: op, ticket: t.ticket, txRef: t.ref, input: input, output: output, exclusionStarted: true, exclusionModes: t.exclusionModes}
	if err := s.checkExclusionEvents(true, nil); err != nil {
		return err
	}
	t.exclusionModes = s.exclusionModes
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
	if s.implicit {
		s.op.emitNotice(levelWarning, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "25P01", Message: "SET CONSTRAINTS can only be used in transaction blocks"})
	}
	e := s.op.session.Engine()
	auth, err := s.op.Authorization()
	if err != nil {
		return Result{}, err
	}
	e.mu.Lock()
	auth.catalog = e.privateCatalogLocked(s.owner.catalog)
	s.beginExclusionProjectionLocked(e)
	e.mu.Unlock()
	candidate := s.exclusionModes
	candidate.byConstraint = maps.Clone(candidate.byConstraint)
	selected := make(map[OID]bool)
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
				for _, descriptor := range auth.catalog.state.descriptors {
					parent, ok := descriptor.(*relationDescriptor)
					if !ok || parent.namespace != namespace || parent.relationKind != tableRelation {
						continue
					}
					for _, c := range parent.exclusionConstraints {
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
					for _, c := range parent.uniqueConstraints {
						if c.Name == label {
							nondeferrable = true
						}
					}
					for _, c := range parent.checkConstraints {
						if c.Name == label {
							nondeferrable = true
						}
					}
					for _, c := range parent.notNullConstraints {
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
		if candidate.byConstraint == nil {
			candidate.byConstraint = make(map[OID]bool, len(selected))
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
	if !s.implicit {
		s.exclusionModes = candidate
	}
	return Result{}, nil
}
func (e *Engine) removeExclusionOwner(ticket *relationTicket) {
	e.mu.Lock()
	if _, exists := e.exclusionPending[ticket]; exists {
		delete(e.exclusionPending, ticket)
		e.exclusionGeneration++
	}
	e.mu.Unlock()
}

func (s *StatementScope) reservedExclusionRow(ref ObjectRef, values []sqltype.Value) (rowVersion, bool) {
	for _, row := range s.exclusionCandidates[ref.Address] {
		if len(values) > 0 && &row.values[0] == &values[0] {
			return row, true
		}
	}
	return rowVersion{}, false
}
