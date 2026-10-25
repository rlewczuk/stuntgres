package engine

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
)

func waitOlderIndexSnapshots(s *StatementScope, xmin uint64, idx ids.ObjectAddress) error {
	e := s.Engine()
	e.Mu.Lock()
	if s.TxRef.Epoch() != e.Catalog.State.Epoch() {
		e.Mu.Unlock()
		return ids.ErrCatalogReplaced
	}
	// Drop every advertised builder phase before collecting the real wait cohort.
	e.Visibility.ResetPhaseLocked(s.TxRef)
	refs := e.Visibility.OlderSnapshotsLocked(s.TxRef, xmin)
	// Publish only after dropping validation ownership and freezing the cohort,
	// under the same critical section used by the sole coordinator observation.
	if err := admitIndexBuildWaitLocked(e, idx, s.TxRef, index.WaitOlderSnapshots); err != nil {
		e.Mu.Unlock()
		return err
	}
	e.Mu.Unlock()
	defer clearIndexBuildWait(e, idx, s.TxRef, index.WaitOlderSnapshots)
	for _, ref := range refs {
		e.Mu.Lock()
		eligible := e.Visibility.SnapshotOlderLocked(ref, xmin)
		e.Mu.Unlock()
		if !eligible {
			continue
		}
		if owner := e.Locks.TransactionTicket(ref); owner != nil {
			if err := s.WriteHost().WaitTransaction(owner, lock.CompletionVirtual); err != nil {
				return err
			}
		}
	}
	return nil
}
