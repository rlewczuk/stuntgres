package partitioncmds

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/tablecmds"
)

// lockPartitionAttachChild returns the partition.AttachFrontier callback of
// the incumbent DEFAULT partition: it waits for the child's AccessExclusive
// lock with nothing held, then rereads the child after the grant.
func lockPartitionAttachChild(s *core.StatementScope) func(catalog.Read, ids.ObjectAddress) (catalog.Read, *catalog.Relation, error) {
	return func(view catalog.Read, address ids.ObjectAddress) (catalog.Read, *catalog.Relation, error) {
		ref := s.Engine().Catalog.Domain.Ref(view.Epoch(), address)
		if err := s.AcquirePartitionLock(ref, lock.AccessExclusive); err != nil {
			return catalog.Read{}, nil, err
		}
		return partitionAttachRelation(s.Engine(), s, ref)
	}
}

// Fences cover the complete candidate stores even when proof elides a scan.
// Scanned DEFAULT stores have their own predecessors. Normal query snapshots
// are untouched; the dedicated view uses the same owner/command and own rows.
func partitionAttachCapture(e *core.Engine, scope *core.StatementScope, stage *core.CatalogStage, view catalog.Read, candidate *catalog.Relation, jobs []partition.AttachScan) error {
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if scope.TxRef.Epoch() != e.Catalog.State.Epoch() {
		return ids.ErrCatalogReplaced
	}
	capture := func(d *catalog.Relation) error {
		if current := e.PrivateCatalogLocked(scope.PendingCatalog()).Get(d.Address()); current != d {
			return ids.ErrCatalogReplaced
		}
		stage.DomainBases = append(stage.DomainBases, d)
		storage := e.PrivateStorageLocked(scope.PendingCatalog(), d.Address())
		if storage == nil {
			return ids.ErrCatalogReplaced
		}
		stage.StorageReads = append(stage.StorageReads, session.NewStorageReadFence(d.Address(), storage))
		return nil
	}
	if err := partition.PhysicalDescendants(view, candidate, capture); err != nil {
		return err
	}
	refs := make([]ids.ObjectRef, 0, len(jobs))
	for _, job := range jobs {
		if job.Exclude {
			if err := capture(job.Leaf); err != nil {
				return err
			}
		}
		refs = append(refs, e.Catalog.Domain.Ref(view.Epoch(), job.Leaf.Address()))
	}
	if len(refs) == 0 {
		return nil
	}
	_, err := tablecmds.AcquirePartitionValidationViewsLocked(scope, e, refs)
	return err
}
