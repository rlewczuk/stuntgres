package tablecmds

import (
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
)

func vacuumDatabasePreflightLocked(e *core.Engine, s *core.StatementScope) error {
	s.Op.Session.Mu.Lock()
	defer s.Op.Session.Mu.Unlock()
	if err := core.PublicationOperationLocked(e, s.Op); err != nil {
		return err
	}
	if s.TxRef.Epoch() != e.Catalog.State.Epoch() || s.LockTicket == nil || s.LockTicket.OwnershipEnded() {
		return ids.ErrCatalogReplaced
	}
	return s.Op.Context().Err()
}

// Completed blocks are high-water estimates over the first capture's extent.
func vacuumProgressBlocks(completed, extent int) int64 {
	if completed >= extent {
		blocks := int64(extent / 128)
		if extent%128 != 0 {
			blocks++
		}
		return blocks
	}
	return int64(completed / 128)
}

func vacuumProgressLocked(e *core.Engine, s *core.StatementScope, progress table.VacuumProgress, extent int, cycles int64) {
	p, ok := e.VacuumProgress[s.TxRef]
	if !ok {
		return
	}
	p.Fact.Phase = progress.Phase
	p.Fact.HeapBlocksTotal = vacuumProgressBlocks(extent, extent)
	p.Fact.HeapBlocksScanned = max(p.Fact.HeapBlocksScanned, vacuumProgressBlocks(progress.Scanned, extent))
	p.Fact.HeapBlocksVacuumed = max(p.Fact.HeapBlocksVacuumed, vacuumProgressBlocks(progress.Vacuumed, extent))
	p.Fact.IndexVacuumCount = cycles
	p.Fact.MaxDeadTupleBytes = progress.Capacity
	p.Fact.NumDeadItemIDs = progress.DeadItems
	p.Fact.DeadTupleBytes = progress.DeadItems * 8
	p.Fact.IndexesTotal = progress.IndexesTotal
	p.Fact.IndexesProcessed = progress.IndexesProcessed
	e.VacuumProgress[s.TxRef] = p
}

func endVacuumProgress(e *core.Engine, ref ids.TxRef) {
	e.Mu.Lock()
	delete(e.VacuumProgress, ref)
	e.Mu.Unlock()
}
