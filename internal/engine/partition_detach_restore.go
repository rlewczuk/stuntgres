package engine

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/catalog"
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
	p := auth.catalog.MustRelation(parent.Address)
	c := auth.catalog.MustRelation(child.Address)
	if p.Partition == nil || len(p.Partition.Children) != 1 || c.Partition != nil || c.Membership == nil || !c.Membership.DefaultBound || c.Membership.Parent != p.Address() {
		return partitionReplayTeardown{}, nil
	}
	store := e.storage.relations[child.Address]
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
	child, ok := auth.catalog.Relation(ref.Address)
	parent, live := auth.catalog.Relation(record.parent.Address)
	if !ok || !live || record.parent.Epoch != auth.catalog.Epoch() || child.Membership != nil || child.Partition != nil || parent.Partition == nil || len(parent.Partition.Children) != 0 {
		return sqlError("0A000", "restore accepts only generated empty partition replay teardown")
	}
	store := e.storage.relations[ref.Address]
	if store == nil || len(store.versions) != 0 {
		return sqlError("0A000", "restore accepts only generated empty partition replay teardown")
	}
	for _, descriptor := range auth.catalog.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if ok && index.SQLIndex != nil && index.SQLIndex.Parent == parent.Address() && !index.SQLIndex.Valid && index.SQLIndex.Ready && index.SQLIndex.Live {
			delete(records, ref.Address)
			return nil
		}
	}
	return sqlError("0A000", "restore accepts only generated empty partition replay teardown")
}
