package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/partition"
)

// Candidate AX admission is unconditional and already complete. DEFAULT opens
// descendants only when the deciding relation cannot prove its exclusion.
func (s *StatementScope) partitionAttachFrontier(catalogView catalog.Read, parent, target *catalog.Relation, bound *catalog.PartitionMembership, exclude, lockChildren bool) ([]partition.AttachScan, error) {
	var scans []partition.AttachScan
	var walk func(catalog.Read, *catalog.Relation) error
	walk = func(view catalog.Read, d *catalog.Relation) error {
		proven, err := partition.ConstraintProven(s.op.Context(), s.input, view, parent, d, bound, exclude)
		if err != nil {
			return err
		}
		if proven && !exclude {
			for child := parent; child.Membership != nil; {
				ancestor := view.MustRelation(child.Membership.Parent)
				proven, err = partition.ConstraintProven(s.op.Context(), s.input, view, ancestor, d, child.Membership, false)
				if err != nil || !proven {
					break
				}
				child = ancestor
			}
		}
		if err != nil {
			return err
		}
		if proven {
			return nil
		}
		if d.Partition == nil {
			scans = append(scans, partition.AttachScan{Leaf: d, Exclude: exclude})
			return nil
		}
		for _, address := range d.Partition.Children {
			child, ok := view.Relation(address)
			if !ok {
				return ids.ErrCatalogReplaced
			}
			childView := view
			if lockChildren {
				ref := s.engine().catalog.Domain.Ref(view.Epoch(), address)
				if err := s.acquirePartitionLock(ref, lock.AccessExclusive); err != nil {
					return err
				}
				var err error
				childView, child, err = s.engine().partitionAttachRelation(s, ref)
				if err != nil {
					return err
				}
			}
			if err := walk(childView, child); err != nil {
				return err
			}
		}
		return nil
	}
	err := walk(catalogView, target)
	return scans, err
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
		stage.storageReads = append(stage.storageReads, storageReadFence{address: d.Address(), storage: storage, length: len(storage.Versions), ends: storage.Ends, generation: storage.Generation})
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
