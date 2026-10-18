package engine

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
)

func (stage *CatalogStage) removePartitionHierarchyLocked(relation *relationDescriptor) error {
	return stageDropProvider(nil, stage, nil, nil, nil).removePartitionHierarchyLocked(relation)
}

func (provider *dropProvider) removePartitionHierarchyLocked(relation *relationDescriptor) error {
	if relation.Partition != nil {
		for _, address := range relation.Partition.children {
			ref := provider.owner.catalog.domain.Ref(provider.base.Epoch(), address)
			if err := provider.removeObjectLocked(ref, false); err != nil {
				return err
			}
		}
	}
	if relation.Membership == nil {
		return nil
	}
	address := relation.Membership.parent
	if provider.removed(address) {
		return nil
	}
	parent, ok := provider.inputDescriptor(address).(*relationDescriptor)
	if !ok || parent.Partition == nil {
		return fmt.Errorf("stuntgres: missing partition parent")
	}
	if staged, ok := provider.replacement(address).(*relationDescriptor); ok {
		parent = staged
	}
	if parent.revision == ids.DefinitionRevision(^uint64(0)) {
		return fmt.Errorf("stuntgres: definition revision exhausted")
	}
	next := *parent
	definition := *parent.Partition
	definition.children = slices.DeleteFunc(slices.Clone(definition.children), func(child ids.ObjectAddress) bool { return child == relation.address })
	if definition.defaultChild == relation.address {
		definition.defaultChild = ids.ObjectAddress{}
	}
	next.Partition = &definition
	next.revision++
	if err := provider.replace(&next); err != nil {
		return err
	}
	return provider.partitionIndexValidity(ids.ObjectAddress{})
}

func (s *StatementScope) acquirePartitionDropLocks(ref ids.ObjectRef) error {
	e := s.op.session.Engine()
	e.mu.Lock()
	catalog := e.privateCatalogLocked(s.owner.catalog)
	relation, ok := catalog.Relation(ref.Address)
	if !ok || ref.Epoch != catalog.Epoch() {
		e.mu.Unlock()
		return ids.ErrCatalogReplaced
	}
	if !hierarchyRelation(relation) {
		e.mu.Unlock()
		return nil
	}
	var ancestors []ids.ObjectRef
	for relation.Membership != nil {
		relation, ok = catalog.Relation(relation.Membership.parent)
		if !ok {
			e.mu.Unlock()
			return ids.ErrCatalogReplaced
		}
		ancestors = append(ancestors, ref.Domain().Ref(ref.Epoch, relation.address))
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

func partitionDependsOn(relation *relationDescriptor, target ids.ObjectAddress) bool {
	if relation.Partition == nil {
		return false
	}
	for _, key := range relation.Partition.keys {
		if target.ClassID == ids.TypeClassID && ids.OID(key.usage.Type.ID()) == target.ObjectID {
			return true
		}
		if target.ClassID == ids.CollationClassID && target.SubID == 0 && target.ObjectID != 0 &&
			(ids.OID(key.usage.Collation.ID) == target.ObjectID || ids.OID(key.collation) == target.ObjectID) {
			return true
		}
		if key.attribute != 0 && target.ClassID == ids.RelationClassID && target.ObjectID == relation.address.ObjectID && target.SubID == key.attribute {
			return true
		}
		if key.derived != nil {
			for _, ref := range key.derived.dependencies {
				address := ref.Address
				if address.ClassID == target.ClassID && address.ObjectID == target.ObjectID && (target.SubID == 0 || address.SubID == target.SubID) {
					return true
				}
			}
		}
	}
	return false
}
