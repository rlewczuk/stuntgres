package engine

import (
	"fmt"
	"slices"
)

func (stage *CatalogStage) removePartitionHierarchyLocked(relation *relationDescriptor) error {
	return stageDropProvider(nil, stage, nil, nil, nil).removePartitionHierarchyLocked(relation)
}

func (provider *dropProvider) removePartitionHierarchyLocked(relation *relationDescriptor) error {
	if relation.partition != nil {
		for _, address := range relation.partition.children {
			ref := ObjectRef{owner: provider.owner.domain, Epoch: provider.base.Epoch(), Address: address}
			if err := provider.removeObjectLocked(ref, false); err != nil {
				return err
			}
		}
	}
	if relation.membership == nil {
		return nil
	}
	address := relation.membership.parent
	if provider.removed(address) {
		return nil
	}
	parent, ok := provider.inputDescriptor(address).(*relationDescriptor)
	if !ok || parent.partition == nil {
		return fmt.Errorf("stuntgres: missing partition parent")
	}
	if staged, ok := provider.replacement(address).(*relationDescriptor); ok {
		parent = staged
	}
	if parent.revision == DefinitionRevision(^uint64(0)) {
		return fmt.Errorf("stuntgres: definition revision exhausted")
	}
	next := *parent
	definition := *parent.partition
	definition.children = slices.DeleteFunc(slices.Clone(definition.children), func(child ObjectAddress) bool { return child == relation.address })
	if definition.defaultChild == relation.address {
		definition.defaultChild = ObjectAddress{}
	}
	next.partition = &definition
	next.revision++
	if err := provider.replace(&next); err != nil {
		return err
	}
	return provider.partitionIndexValidity(ObjectAddress{})
}

func (s *StatementScope) acquirePartitionDropLocks(ref ObjectRef) error {
	e := s.op.session.Engine()
	e.mu.Lock()
	catalog := e.privateCatalogLocked(s.owner.catalog)
	relation, ok := catalog.state.descriptors[ref.Address].(*relationDescriptor)
	if !ok || ref.Epoch != catalog.Epoch() {
		e.mu.Unlock()
		return ErrCatalogReplaced
	}
	if !hierarchyRelation(relation) {
		e.mu.Unlock()
		return nil
	}
	var ancestors []ObjectRef
	for relation.membership != nil {
		relation, ok = catalog.state.descriptors[relation.membership.parent].(*relationDescriptor)
		if !ok {
			e.mu.Unlock()
			return ErrCatalogReplaced
		}
		ancestors = append(ancestors, ObjectRef{owner: ref.owner, Epoch: ref.Epoch, Address: relation.address})
	}
	e.mu.Unlock()
	slices.Reverse(ancestors)
	for i, ancestor := range ancestors {
		mode := accessShare
		if i == len(ancestors)-1 {
			mode = accessExclusive
		}
		if err := s.acquirePartitionLock(ancestor, mode); err != nil {
			return err
		}
	}
	if err := s.acquirePartitionLock(ref, accessExclusive); err != nil {
		return err
	}
	return s.acquirePartitionDescendants(ref, accessExclusive)
}

func partitionDependsOn(relation *relationDescriptor, target ObjectAddress) bool {
	if relation.partition == nil {
		return false
	}
	for _, key := range relation.partition.keys {
		if target.ClassID == TypeClassID && OID(key.usage.Type.ID()) == target.ObjectID {
			return true
		}
		if target.ClassID == CollationClassID && target.SubID == 0 && target.ObjectID != 0 &&
			(OID(key.usage.Collation.ID) == target.ObjectID || OID(key.collation) == target.ObjectID) {
			return true
		}
		if key.attribute != 0 && target.ClassID == RelationClassID && target.ObjectID == relation.address.ObjectID && target.SubID == key.attribute {
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
