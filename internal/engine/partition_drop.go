package engine

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
)

func (stage *CatalogStage) removePartitionHierarchyLocked(relation *catalog.Relation) error {
	return stageDropProvider(nil, stage, nil, nil, nil).removePartitionHierarchyLocked(relation)
}

func (provider *dropProvider) removePartitionHierarchyLocked(relation *catalog.Relation) error {
	if relation.Partition != nil {
		for _, address := range relation.Partition.Children {
			ref := provider.owner.catalog.Domain.Ref(provider.base.Epoch(), address)
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
	if parent.Revision() == ids.DefinitionRevision(^uint64(0)) {
		return fmt.Errorf("stuntgres: definition revision exhausted")
	}
	next := *parent
	definition := *parent.Partition
	definition.Children = slices.DeleteFunc(slices.Clone(definition.Children), func(child ids.ObjectAddress) bool { return child == relation.Address() })
	if definition.DefaultChild == relation.Address() {
		definition.DefaultChild = ids.ObjectAddress{}
	}
	next.Partition = &definition
	next.ObjectRevision++
	if err := provider.Replace(&next); err != nil {
		return err
	}
	return index.PartitionIndexValidity(provider, ids.ObjectAddress{})
}

func (s *StatementScope) acquirePartitionDropLocks(ref ids.ObjectRef) error {
	e := s.engine()
	e.mu.Lock()
	view := e.privateCatalogLocked(s.owner.Catalog)
	relation, ok := view.Relation(ref.Address)
	if !ok || ref.Epoch != view.Epoch() {
		e.mu.Unlock()
		return ids.ErrCatalogReplaced
	}
	if !catalog.HierarchyRelation(relation) {
		e.mu.Unlock()
		return nil
	}
	var ancestors []ids.ObjectRef
	for relation.Membership != nil {
		relation, ok = view.Relation(relation.Membership.Parent)
		if !ok {
			e.mu.Unlock()
			return ids.ErrCatalogReplaced
		}
		ancestors = append(ancestors, ref.Domain().Ref(ref.Epoch, relation.Address()))
	}
	e.mu.Unlock()
	slices.Reverse(ancestors)
	for i, ancestor := range ancestors {
		mode := lock.AccessShare
		if i == len(ancestors)-1 {
			mode = lock.AccessExclusive
		}
		if err := s.acquirePartitionLock(ancestor, mode); err != nil {
			return err
		}
	}
	if err := s.acquirePartitionLock(ref, lock.AccessExclusive); err != nil {
		return err
	}
	return s.acquirePartitionDescendants(ref, lock.AccessExclusive)
}

func partitionDependsOn(relation *catalog.Relation, target ids.ObjectAddress) bool {
	if relation.Partition == nil {
		return false
	}
	for _, key := range relation.Partition.Keys {
		if target.ClassID == ids.TypeClassID && ids.OID(key.Usage.Type.ID()) == target.ObjectID {
			return true
		}
		if target.ClassID == ids.CollationClassID && target.SubID == 0 && target.ObjectID != 0 &&
			(ids.OID(key.Usage.Collation.ID) == target.ObjectID || ids.OID(key.Collation) == target.ObjectID) {
			return true
		}
		if key.Attribute != 0 && target.ClassID == ids.RelationClassID && target.ObjectID == relation.Address().ObjectID && target.SubID == key.Attribute {
			return true
		}
		if key.Derived != nil {
			for _, ref := range key.Derived.Dependencies {
				address := ref.Address
				if address.ClassID == target.ClassID && address.ObjectID == target.ObjectID && (target.SubID == 0 || address.SubID == target.SubID) {
					return true
				}
			}
		}
	}
	return false
}
