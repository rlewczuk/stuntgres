package engine

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// Only the private importer records exact empty DEFAULT identities before an
// emitted detach. A name prefix alone cannot grant arbitrary DROP admission.
func capturePartitionReplayTeardown(e *Engine, op *session.Operation, s *parser.AlterTableDetachPartition) (partition.ReplayTeardown, error) {
	if !strings.HasPrefix(s.Partition.Parts[len(s.Partition.Parts)-1].Text, partition.ReplayStem) {
		return partition.ReplayTeardown{}, nil
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		return partition.ReplayTeardown{}, err
	}
	if e.ImportTargets == nil {
		return partition.ReplayTeardown{}, fmt.Errorf("stuntgres: invalid private partition replay")
	}
	parent, err := e.ResolveRelationReference(auth, s.Name, true)
	if err != nil {
		return partition.ReplayTeardown{}, err
	}
	child, err := e.ResolveRelationReference(auth, s.Partition, true)
	if err != nil {
		return partition.ReplayTeardown{}, err
	}
	p := auth.Catalog.MustRelation(parent.Address)
	c := auth.Catalog.MustRelation(child.Address)
	if p.Partition == nil || len(p.Partition.Children) != 1 || c.Partition != nil || c.Membership == nil || !c.Membership.DefaultBound || c.Membership.Parent != p.Address() {
		return partition.ReplayTeardown{}, nil
	}
	store := e.Storage.Relations[child.Address]
	if store == nil || len(store.Versions) != 0 {
		return partition.ReplayTeardown{}, nil
	}
	return partition.ReplayTeardown{Child: child, Parent: parent}, nil
}

func validatePartitionReplayTeardown(e *Engine, op *session.Operation, name parser.ObjectName, records map[ids.ObjectAddress]partition.ReplayTeardown) error {
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		return err
	}
	ref, err := e.ResolveRelationReference(auth, name, true)
	if err != nil {
		return err
	}
	record, ok := records[ref.Address]
	if !ok || record.Child != ref || e.ImportTargets == nil {
		return SQLError("0A000", "restore accepts only generated empty partition replay teardown")
	}
	child, ok := auth.Catalog.Relation(ref.Address)
	parent, live := auth.Catalog.Relation(record.Parent.Address)
	if !ok || !live || record.Parent.Epoch != auth.Catalog.Epoch() || child.Membership != nil || child.Partition != nil || parent.Partition == nil || len(parent.Partition.Children) != 0 {
		return SQLError("0A000", "restore accepts only generated empty partition replay teardown")
	}
	store := e.Storage.Relations[ref.Address]
	if store == nil || len(store.Versions) != 0 {
		return SQLError("0A000", "restore accepts only generated empty partition replay teardown")
	}
	for _, descriptor := range auth.Catalog.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if ok && index.SQLIndex != nil && index.SQLIndex.Parent == parent.Address() && !index.SQLIndex.Valid && index.SQLIndex.Ready && index.SQLIndex.Live {
			delete(records, ref.Address)
			return nil
		}
	}
	return SQLError("0A000", "restore accepts only generated empty partition replay teardown")
}
