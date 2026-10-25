package core

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/partition"
)

func (stage *CatalogStage) removePartitionHierarchyLocked(relation *catalog.Relation) error {
	return StageDropProvider(nil, stage, nil, nil, nil).removePartitionHierarchyLocked(relation)
}

func (provider *DropProvider) removePartitionHierarchyLocked(relation *catalog.Relation) error {
	if relation.Partition != nil {
		for _, address := range relation.Partition.Children {
			ref := provider.Owner.Catalog.Domain.Ref(provider.Base.Epoch(), address)
			if err := provider.removeObjectLocked(ref, false); err != nil {
				return err
			}
		}
	}
	if relation.Membership == nil {
		return nil
	}
	address := relation.Membership.Parent
	if provider.Removed(address) {
		return nil
	}
	parent, ok := provider.inputDescriptor(address).(*catalog.Relation)
	if !ok || parent.Partition == nil {
		return fmt.Errorf("stuntgres: missing partition parent")
	}
	if staged, ok := provider.Replacement(address).(*catalog.Relation); ok {
		parent = staged
	}
	next, err := partition.WithoutChild(parent, relation.Address())
	if err != nil {
		return err
	}
	if err := provider.Replace(next); err != nil {
		return err
	}
	return index.PartitionIndexValidity(provider, ids.ObjectAddress{})
}

func (s *StatementScope) AcquirePartitionDropLocks(ref ids.ObjectRef) error {
	e := s.Engine()
	e.Mu.Lock()
	view := e.PrivateCatalogLocked(s.Owner.Catalog)
	ancestors, err := partition.Ancestors(view, ref)
	hierarchy := err == nil && catalog.HierarchyRelation(view.MustRelation(ref.Address))
	e.Mu.Unlock()
	if err != nil || !hierarchy {
		return err
	}
	for i, ancestor := range ancestors {
		mode := lock.AccessShare
		if i == len(ancestors)-1 {
			mode = lock.AccessExclusive
		}
		if err := s.AcquirePartitionLock(ancestor, mode); err != nil {
			return err
		}
	}
	if err := s.AcquirePartitionLock(ref, lock.AccessExclusive); err != nil {
		return err
	}
	return s.AcquirePartitionDescendants(ref, lock.AccessExclusive)
}
