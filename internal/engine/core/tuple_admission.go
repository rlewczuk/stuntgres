package core

import (
	"errors"

	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
)

type tupleIdentity struct {
	ref     ids.ObjectRef
	version ids.VersionID
}

// Ordinary grants need no side allocation. Only a repaired physical header
// needs a remembered key, so later events cannot attribute an old grant to a
// newly repaired header. This is statement admission evidence, not lock state.
func (s *StatementScope) admittedTuple(ref ids.ObjectRef, row storage.RowVersion) lock.Resource {
	if resource, ok := s.tupleAdmissions[tupleIdentity{ref, row.Version}]; ok {
		return resource
	}
	return lock.Resource{ObjectRef: ref, Row: row.Row}
}

func (s *StatementScope) rememberTuple(ref ids.ObjectRef, row storage.RowVersion, resource lock.Resource) {
	if resource == (lock.Resource{ObjectRef: ref, Row: row.Row}) {
		return
	}
	if s.tupleAdmissions == nil {
		s.tupleAdmissions = make(map[tupleIdentity]lock.Resource)
	}
	s.tupleAdmissions[tupleIdentity{ref, row.Version}] = resource
}

// Caller holds Engine.Mu. Exact private history and canonical physical slots
// share the current immutable header root; no same-RowID retarget is allowed.
func (s *StatementScope) tupleResourceLocked(ref ids.ObjectRef, row storage.RowVersion, expected *storage.Relation) (lock.Resource, error) {
	e := s.Engine()
	if ref.Domain() != e.Catalog.Domain || ref.Epoch != e.Catalog.State.Epoch() || ref.Epoch != s.TxRef.Epoch() {
		return lock.Resource{}, ids.ErrCatalogReplaced
	}
	store := e.PrivateStorageLocked(s.PendingCatalog(), ref.Address)
	if store == nil || expected != nil && store != expected {
		return lock.Resource{}, ids.ErrCatalogReplaced
	}
	if row.Row == 0 || row.Version == 0 {
		return lock.Resource{}, errors.New("stuntgres: invalid exact tuple admission")
	}
	return exec.TupleResource(ref, row, store.Headers)
}

// Capture -> release -> manager -> readmission. A repair never cancels or
// releases the old genuine request: if the key changed, acquire its replacement
// outside all metadata mutexes and keep both actual grants on their own ticket.
func (s *StatementScope) acquireTuple(ref ids.ObjectRef, row storage.RowVersion, expected *storage.Relation, mode lock.Mode, policy lock.Policy) error {
	e := s.Engine()
	for {
		if err := s.Active(); err != nil {
			return err
		}
		e.Mu.Lock()
		resource, err := s.tupleResourceLocked(ref, row, expected)
		e.Mu.Unlock()
		if err != nil {
			return err
		}
		request, err := e.Locks.StartAcquire(s.Op.Context(), s.LockTicket, resource, mode, policy)
		if err != nil {
			return err
		}
		if request != nil {
			if s.ReplacementRead {
				e.ReplacementMu.RUnlock()
				s.ReplacementRead = false
			}
			if err = request.Wait(); err != nil {
				return err
			}
			if s.implicit {
				e.ReplacementMu.RLock()
				s.ReplacementRead = true
			}
		}
		e.Mu.Lock()
		current, err := s.tupleResourceLocked(ref, row, expected)
		if err == nil && current == resource {
			s.rememberTuple(ref, row, resource)
		}
		e.Mu.Unlock()
		if err != nil {
			return err
		}
		if current == resource {
			return nil
		}
	}
}

func tupleGrantMode(mode lock.MultiMode) lock.Mode {
	switch mode {
	case lock.MultiKeyShare:
		return lock.ForKeyShare
	case lock.MultiShare:
		return lock.ForShare
	case lock.MultiNoKeyLock, lock.MultiNoKeyAction:
		return lock.ForNoKeyUpdate
	default:
		return lock.ForUpdate
	}
}

// Returns with Owner.Mu then Engine.Mu held. The final exact key comparison
// and the caller's publication are one acceptance critical section. A changed
// key is readmitted outside both mutexes, without discarding any old grant.
func (s *StatementScope) lockTupleStage(ref ids.ObjectRef, rows []storage.RowVersion, modes []lock.MultiMode, action lock.MultiMode) error {
	e := s.Engine()
	for {
		for i, row := range rows {
			mode := action
			if mode == 0 && i < len(modes) {
				mode = modes[i]
			}
			grant := tupleGrantMode(mode)
			if grant == lock.ForNoKeyUpdate {
				grant |= lock.ForUpdate
			}
			if !e.Locks.Holds(s.LockTicket, s.admittedTuple(ref, row), grant) {
				return errors.New("stuntgres: tuple stage requires its admitted row grant")
			}
		}
		s.Owner.Mu.Lock()
		e.Mu.Lock()
		changed := -1
		for i, row := range rows {
			resource, err := s.tupleResourceLocked(ref, row, nil)
			if err != nil {
				e.Mu.Unlock()
				s.Owner.Mu.Unlock()
				return err
			}
			if resource != s.admittedTuple(ref, row) {
				changed = i
				break
			}
		}
		if changed < 0 {
			return nil
		}
		e.Mu.Unlock()
		s.Owner.Mu.Unlock()
		mode := action
		if mode == 0 && changed < len(modes) {
			mode = modes[changed]
		}
		if err := s.acquireUpdateRowMode(ref, rows[changed], tupleGrantMode(mode)); err != nil {
			return err
		}
	}
}

// Caller holds Owner.Mu and Engine.Mu after exact-key admission. A proposal
// computed for another physical tuple is never accepted against its successor.
// Stable crosschecks and movement failures retain their canonical SQL errors.
func (s *StatementScope) checkTupleProposalLocked(ref ids.ObjectRef, expected storage.RowVersion, hint int) error {
	latest, err := dml.LatestUpdateRow(s.latestRowSourceLocked(s.Engine(), ref.Address), dml.RowRequest{Ref: ref, Row: expected.Row, Physical: expected.Version, Own: expected.Born == 0 && !expected.ForcedFrozen, Hint: hint})
	if err != nil {
		return err
	}
	if !latest.Live || latest.Version.Version != expected.Version {
		return dml.ErrTupleRecheck
	}
	return nil
}

// Original candidate authority and the producer-checked physical tuple are
// distinct. A late successor requires EPQ, not unchecked canonical retargeting.
func (s *StatementScope) lockDeletionStage(candidate visibility.DeleteCandidate, expected storage.RowVersion) (storage.RowVersion, int, error) {
	e := s.Engine()
	if candidate.Storage == nil {
		return storage.RowVersion{}, -1, ids.ErrCatalogReplaced
	}
	if expected.Row != candidate.Row || expected.Version == 0 {
		return storage.RowVersion{}, -1, errors.New("stuntgres: invalid checked deletion identity")
	}
	if err := s.lockTupleStage(candidate.Ref, []storage.RowVersion{expected}, nil, lock.MultiUpdateAction); err != nil {
		return storage.RowVersion{}, -1, err
	}
	unlock := func() { e.Mu.Unlock(); s.Owner.Mu.Unlock() }
	if root := s.Stage.Deletes[candidate.Ref.Address]; root != nil {
		if prior, ok := root.Lookup(expected.Row, expected.Version); ok && prior.RepairStamp == candidate.Storage.Headers.Header(expected.Version).RepairStamp {
			unlock()
			return storage.RowVersion{}, -1, errors.New("stuntgres: duplicate staged deletion")
		}
	}
	latest, err := dml.LatestDeleteRow(s.latestRowSourceLocked(e, candidate.Ref.Address), dml.DeleteRowRequest(candidate))
	if err == nil && (!latest.Live || latest.Version.Version != expected.Version) {
		err = dml.ErrTupleRecheck
	}
	if err != nil {
		unlock()
		return storage.RowVersion{}, -1, err
	}
	return latest.Version, latest.Hint, nil
}
