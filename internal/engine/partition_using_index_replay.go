package engine

import (
	"fmt"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

type partitionUsingIndexReplay struct {
	target, index *relationDescriptor
	syntax        *parser.AlterTable
}

// Capture the existing physical schema/index origin on the private import lease.
// The ordinary USING INDEX binder owns eligibility, allocation and publication.
func (e *Engine) beginPartitionUsingIndexReplay(op *Operation, raw *parser.AlterTable) error {
	if op == nil {
		return fmt.Errorf("stuntgres: invalid trusted restore partition index operation")
	}
	op.restorePartitionUsingIndex = nil
	if raw == nil || len(raw.Actions) != 1 || raw.Actions[0].Action != parser.AddUsingIndex || raw.Actions[0].UsingIndex == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := e.statementAuthorizationLocked(op, raw)
	if err != nil {
		return err
	}
	if !partitionConstraintReplayImport(e, op, auth) {
		return fmt.Errorf("stuntgres: invalid trusted restore partition index authority")
	}
	ref, err := e.resolveRelationReference(auth, raw.Name, true)
	if err != nil {
		return err
	}
	table, ok := auth.catalog.Relation(ref.Address)
	if !ok {
		return ids.ErrCatalogReplaced
	}
	if table.RelationKind != tableRelation || table.Partition != nil || table.Membership == nil || table.Inheritance != nil {
		return nil
	}
	address := auth.catalog.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: table.namespace, Name: raw.Actions[0].UsingIndex.Index.Text})
	index, ok := auth.catalog.Relation(address)
	if ok && index.SQLIndex != nil && index.SQLIndex.parent == table.address && index.SQLIndex.partitionParent == (ids.ObjectAddress{}) && index.SQLIndex.constraint == nil {
		op.restorePartitionUsingIndex = &partitionUsingIndexReplay{target: table, index: index, syntax: raw}
	}
	return nil
}

func partitionUsingIndexReplayAllowed(op *Operation, auth AuthorizationView, raw *parser.AlterTable, table *relationDescriptor) bool {
	if op == nil || raw == nil || op.restorePartitionUsingIndex == nil {
		return false
	}
	origin := op.restorePartitionUsingIndex
	if origin.index == nil {
		return false
	}
	return origin.syntax == raw && origin.target == table && table != nil && table.RelationKind == tableRelation && table.Partition == nil && table.Membership != nil && table.Inheritance == nil && auth.catalog.Get(table.address) == table && auth.catalog.Get(origin.index.address) == origin.index && len(raw.Actions) == 1 && raw.Actions[0].Action == parser.AddUsingIndex && raw.Actions[0].UsingIndex != nil && raw.Actions[0].UsingIndex.Index.Text == origin.index.name && partitionConstraintReplayImport(auth.engine, op, auth)
}
