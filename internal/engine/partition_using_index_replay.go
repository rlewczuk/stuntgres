package engine

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// Capture the existing physical schema/index origin on the private import lease.
// The ordinary USING INDEX binder owns eligibility, allocation and publication.
func (e *Engine) beginPartitionUsingIndexReplay(op *session.Operation, raw *parser.AlterTable) error {
	if op == nil {
		return fmt.Errorf("stuntgres: invalid trusted restore partition index operation")
	}
	operationReplay(op).partitionUsingIndex = nil
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
	table, ok := auth.Catalog.Relation(ref.Address)
	if !ok {
		return ids.ErrCatalogReplaced
	}
	if table.RelationKind != catalog.TableRelation || table.Partition != nil || table.Membership == nil || table.Inheritance != nil {
		return nil
	}
	address := auth.Catalog.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: table.Namespace(), Name: raw.Actions[0].UsingIndex.Index.Text})
	index, ok := auth.Catalog.Relation(address)
	if ok && index.SQLIndex != nil && index.SQLIndex.Parent == table.Address() && index.SQLIndex.PartitionParent == (ids.ObjectAddress{}) && index.SQLIndex.Constraint == nil {
		operationReplay(op).partitionUsingIndex = &partitionUsingIndexReplay{Target: table, Index: index, Syntax: raw}
	}
	return nil
}

func partitionUsingIndexReplayAllowed(op *session.Operation, auth AuthorizationView, raw *parser.AlterTable, table *catalog.Relation) bool {
	if op == nil || raw == nil || operationReplayOrZero(op).partitionUsingIndex == nil {
		return false
	}
	origin := operationReplayOrZero(op).partitionUsingIndex
	if origin.Index == nil {
		return false
	}
	return origin.Syntax == raw && origin.Target == table && table != nil && table.RelationKind == catalog.TableRelation && table.Partition == nil && table.Membership != nil && table.Inheritance == nil && auth.Catalog.Get(table.Address()) == table && auth.Catalog.Get(origin.Index.Address()) == origin.Index && len(raw.Actions) == 1 && raw.Actions[0].Action == parser.AddUsingIndex && raw.Actions[0].UsingIndex != nil && raw.Actions[0].UsingIndex.Index.Text == origin.Index.Name() && partitionConstraintReplayImport(auth.engine, op, auth)
}
