package engine

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

type partitionReplayTeardown struct{ child, parent ids.ObjectRef }

// Only the private importer records exact empty DEFAULT identities before an
// emitted detach. A name prefix alone cannot grant arbitrary DROP admission.
func (e *Engine) capturePartitionReplayTeardown(op *Operation, s *parser.AlterTableDetachPartition) (partitionReplayTeardown, error) {
	if !strings.HasPrefix(s.Partition.Parts[len(s.Partition.Parts)-1].Text, partitionReplayStem) {
		return partitionReplayTeardown{}, nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		return partitionReplayTeardown{}, err
	}
	if e.importTargets == nil {
		return partitionReplayTeardown{}, fmt.Errorf("stuntgres: invalid private partition replay")
	}
	parent, err := e.resolveRelationReference(auth, s.Name, true)
	if err != nil {
		return partitionReplayTeardown{}, err
	}
	child, err := e.resolveRelationReference(auth, s.Partition, true)
	if err != nil {
		return partitionReplayTeardown{}, err
	}
	p := auth.catalog.state.descriptors[parent.Address].(*relationDescriptor)
	c := auth.catalog.state.descriptors[child.Address].(*relationDescriptor)
	if p.partition == nil || len(p.partition.children) != 1 || c.partition != nil || c.membership == nil || !c.membership.defaultBound || c.membership.parent != p.address {
		return partitionReplayTeardown{}, nil
	}
	store := e.storage[child.Address]
	if store == nil || len(store.versions) != 0 {
		return partitionReplayTeardown{}, nil
	}
	return partitionReplayTeardown{child: child, parent: parent}, nil
}

func (e *Engine) validatePartitionReplayTeardown(op *Operation, name parser.ObjectName, records map[ids.ObjectAddress]partitionReplayTeardown) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		return err
	}
	ref, err := e.resolveRelationReference(auth, name, true)
	if err != nil {
		return err
	}
	record, ok := records[ref.Address]
	if !ok || record.child != ref || e.importTargets == nil {
		return sqlError("0A000", "restore accepts only generated empty partition replay teardown")
	}
	child, ok := auth.catalog.state.descriptors[ref.Address].(*relationDescriptor)
	parent, live := auth.catalog.state.descriptors[record.parent.Address].(*relationDescriptor)
	if !ok || !live || record.parent.Epoch != auth.catalog.Epoch() || child.membership != nil || child.partition != nil || parent.partition == nil || len(parent.partition.children) != 0 {
		return sqlError("0A000", "restore accepts only generated empty partition replay teardown")
	}
	store := e.storage[ref.Address]
	if store == nil || len(store.versions) != 0 {
		return sqlError("0A000", "restore accepts only generated empty partition replay teardown")
	}
	for _, descriptor := range auth.catalog.state.descriptors {
		index, ok := descriptor.(*relationDescriptor)
		if ok && index.sqlIndex != nil && index.sqlIndex.parent == parent.address && !index.sqlIndex.valid && index.sqlIndex.ready && index.sqlIndex.live {
			delete(records, ref.Address)
			return nil
		}
	}
	return sqlError("0A000", "restore accepts only generated empty partition replay teardown")
}
