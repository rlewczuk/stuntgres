package engine

import "errors"

// Descendant replacement authority is a real command target, captured generation
// and row grant. Neither a source grant nor an RI authorization can manufacture it.
func (s *StatementScope) validateInheritedReplacements(ref ObjectRef, old []rowVersion) error {
	if ref == s.lockedRef {
		return nil
	}
	store := s.mutationStorage(ref)
	if store == nil {
		return errors.New("stuntgres: replacement is outside its mutation targets")
	}
	if s.view == nil || s.view.read().generationAt(ref.Address) != store {
		return errors.New("stuntgres: replacement requires its captured target view")
	}
	e := s.op.session.Engine()
	e.locks.mu.Lock()
	defer e.locks.mu.Unlock()
	entry := e.locks.entries[lockResource{ObjectRef: ref}]
	if s.ticket == nil || s.ticket.ref != s.txRef || !e.locks.valid(s.ticket) || entry == nil || entry.holders[s.ticket].mode&rowExclusive == 0 {
		return errors.New("stuntgres: replacement requires its target grant")
	}
	data := s.view.read()
	for _, row := range old {
		held := e.locks.entries[lockResource{ObjectRef: ref, row: row.row}]
		if held == nil || held.holders[s.ticket].mode&(noKeyUpdate|rowUpdate) == 0 {
			return errors.New("stuntgres: replacement requires its row grant")
		}
		captured := data.committed[ref.Address]
		if row.born == 0 {
			captured = data.own[ref.Address]
		}
		if _, _, found := rowByID(captured, row.row, -1); !found {
			return errors.New("stuntgres: replacement is outside its candidate view")
		}
	}
	return nil
}
