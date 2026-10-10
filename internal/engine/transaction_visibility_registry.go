package engine

import "errors"

// The registry contains only detached ownership values under Engine.mu. XIDs
// order visibility incarnations globally; CommitSeq continues to own data views.
type transactionVisibility struct {
	xid, xmin             uint64
	snapshot, safeBuilder bool
}

func (e *Engine) visibilityWriteLocked(ref TxRef) error {
	if ref.owner != e || ref.epoch != e.state.epoch {
		return ErrCatalogReplaced
	}
	v := e.visibility[ref]
	if v.xid != 0 {
		return nil
	}
	if e.visibilityXID == ^uint64(0) {
		return errors.New("stuntgres: visibility identity exhausted")
	}
	e.visibilityXID++
	v.xid = e.visibilityXID
	if e.visibility == nil {
		e.visibility = make(map[TxRef]transactionVisibility)
	}
	e.visibility[ref] = v
	return nil
}

func (e *Engine) visibilitySnapshotLocked(ref TxRef, safeBuilder bool) (uint64, error) {
	if ref.owner != e || ref.epoch != e.state.epoch {
		return 0, ErrCatalogReplaced
	}
	v := e.visibility[ref]
	if v.snapshot {
		return v.xmin, nil
	}
	if e.visibilityXID == ^uint64(0) {
		return 0, errors.New("stuntgres: visibility identity exhausted")
	}
	xmin := e.visibilityXID + 1
	for other, state := range e.visibility {
		if other.epoch == ref.epoch && state.xid != 0 && state.xid < xmin {
			xmin = state.xid
		}
	}
	v.xmin, v.snapshot, v.safeBuilder = xmin, true, safeBuilder
	if e.visibility == nil {
		e.visibility = make(map[TxRef]transactionVisibility)
	}
	e.visibility[ref] = v
	return xmin, nil
}

func (e *Engine) endStatementVisibility(ref TxRef, stable bool) {
	if stable {
		return
	}
	e.mu.Lock()
	e.dropVisibilitySnapshotLocked(ref)
	e.mu.Unlock()
}

// Snapshot advertisement ends independently of writer/completion ownership.
// Engine.mu is held; PREPARE retains the detached incarnation's assigned XID.
func (e *Engine) dropVisibilitySnapshotLocked(ref TxRef) {
	if v, ok := e.visibility[ref]; ok {
		v.xmin, v.snapshot, v.safeBuilder = 0, false, false
		if v.xid == 0 {
			delete(e.visibility, ref)
		} else {
			e.visibility[ref] = v
		}
	}
}

func (s *StatementScope) waitOlderIndexSnapshots(xmin uint64, index ObjectAddress) error {
	e := s.op.session.Engine()
	e.mu.Lock()
	if s.txRef.epoch != e.state.epoch {
		e.mu.Unlock()
		return ErrCatalogReplaced
	}
	// Drop every advertised builder phase before collecting the real wait cohort.
	delete(e.visibility, s.txRef)
	var refs []TxRef
	for ref, v := range e.visibility {
		if ref != s.txRef && ref.epoch == s.txRef.epoch && v.snapshot && !v.safeBuilder && v.xmin <= xmin {
			refs = append(refs, ref)
		}
	}
	// Publish only after dropping validation ownership and freezing the cohort,
	// under the same critical section used by the sole coordinator observation.
	if err := e.admitIndexBuildWaitLocked(index, s.txRef, indexWaitOlderSnapshots); err != nil {
		e.mu.Unlock()
		return err
	}
	e.mu.Unlock()
	defer e.clearIndexBuildWait(index, s.txRef, indexWaitOlderSnapshots)
	for _, ref := range refs {
		e.mu.Lock()
		v, ok := e.visibility[ref]
		eligible := ok && v.snapshot && !v.safeBuilder && v.xmin <= xmin
		e.mu.Unlock()
		if !eligible {
			continue
		}
		e.locks.mu.Lock()
		owner := e.locks.owners[ref]
		e.locks.mu.Unlock()
		if owner != nil {
			if err := s.waitCompletion(owner); err != nil {
				return err
			}
		}
	}
	return nil
}
