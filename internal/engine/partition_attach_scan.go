package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/session"
)

// lockPartitionAttachChild is the partition.AttachFrontier callback of the
// incumbent DEFAULT partition: it waits for the child's AccessExclusive lock
// with nothing held, then rereads the child after the grant.
func (s *StatementScope) lockPartitionAttachChild(view catalog.Read, address ids.ObjectAddress) (catalog.Read, *catalog.Relation, error) {
	ref := s.engine().catalog.Domain.Ref(view.Epoch(), address)
	if err := s.acquirePartitionLock(ref, lock.AccessExclusive); err != nil {
		return catalog.Read{}, nil, err
	}
	return s.engine().partitionAttachRelation(s, ref)
}

// Fences cover the complete candidate stores even when proof elides a scan.
// Scanned DEFAULT stores have their own predecessors. Normal query snapshots
// are untouched; the dedicated view uses the same owner/command and own rows.
func (e *Engine) partitionAttachCapture(scope *StatementScope, stage *CatalogStage, view catalog.Read, candidate *catalog.Relation, jobs []partition.AttachScan) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if scope.txRef.Epoch() != e.catalog.state.Epoch() {
		return ids.ErrCatalogReplaced
	}
	capture := func(d *catalog.Relation) error {
		if current := e.privateCatalogLocked(scope.owner.Catalog).Get(d.Address()); current != d {
			return ids.ErrCatalogReplaced
		}
		stage.domainBases = append(stage.domainBases, d)
		storage := e.privateStorageLocked(scope.owner.Catalog, d.Address())
		if storage == nil {
			return ids.ErrCatalogReplaced
		}
		stage.storageReads = append(stage.storageReads, session.NewStorageReadFence(d.Address(), storage))
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
		refs = append(refs, e.catalog.Domain.Ref(view.Epoch(), job.Leaf.Address()))
	}
	if len(refs) == 0 {
		return nil
	}
	_, err := scope.acquirePartitionValidationViewsLocked(e, refs)
	return err
}
