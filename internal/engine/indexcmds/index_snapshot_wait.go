package indexcmds

import (
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
)

func waitOlderIndexSnapshots(s *core.StatementScope, xmin uint64, idx ids.ObjectAddress) error {
	e := s.Engine()
	e.Mu.Lock()
	if s.TxRef.Epoch() != e.Catalog.State.Epoch() {
		e.Mu.Unlock()
		return ids.ErrCatalogReplaced
	}
	// Drop every advertised builder phase before collecting the real wait cohort.
	e.Visibility.ResetPhaseLocked(s.TxRef)
	refs := e.Visibility.OlderSnapshotsLocked(s.TxRef, xmin)
	s.IndexProgressWait(len(refs), 0, 0)
	// Publish only after dropping validation ownership and freezing the cohort,
	// under the same critical section used by the sole coordinator observation.
	if err := admitIndexBuildWaitLocked(e, idx, s.TxRef, index.WaitOlderSnapshots); err != nil {
		e.Mu.Unlock()
		return err
	}
	e.Mu.Unlock()
	defer clearIndexBuildWait(e, idx, s.TxRef, index.WaitOlderSnapshots)
	for i := range refs {
		if refs[i] == (ids.TxRef{}) {
			continue // Invalidated by an earlier remaining-cohort recheck.
		}
		if i > 0 {
			// Preserve the original ordinals, but forget every remaining
			// member that has ceased to advertise an endangered snapshot.
			// An entry invalidated in this iteration still settles i+1;
			// later iterations skip entries already invalidated here.
			e.Mu.Lock()
			for j := i; j < len(refs); j++ {
				if refs[j] != (ids.TxRef{}) && !e.Visibility.SnapshotOlderLocked(refs[j], xmin) {
					refs[j] = ids.TxRef{}
				}
			}
			e.Mu.Unlock()
		}
		if refs[i] != (ids.TxRef{}) {
			if owner := e.Locks.TransactionTicket(refs[i]); owner != nil {
				pid, err := e.Locks.ProgressPID(owner)
				if err != nil {
					return err
				}
				s.IndexProgressWaitPID(pid)
				if err := s.WriteHost().WaitTransaction(owner, lock.CompletionVirtual); err != nil {
					return err
				}
			}
		}
		s.IndexProgressWaitDone(i + 1)
	}
	return nil
}
