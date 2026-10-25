package engine

import (
	"fmt"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// Capture the existing physical schema/index origin on the private import lease.
// The ordinary USING INDEX binder owns eligibility, allocation and publication.
func beginPartitionUsingIndexReplay(e *Engine, op *session.Operation, raw *parser.AlterTable) error {
	if op == nil {
		return fmt.Errorf("stuntgres: invalid trusted restore partition index operation")
	}
	OperationReplay(op).PartitionUsingIndex = nil
	if raw == nil || len(raw.Actions) != 1 || raw.Actions[0].Action != parser.AddUsingIndex || raw.Actions[0].UsingIndex == nil {
		return nil
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.StatementAuthorizationLocked(op, raw)
	if err != nil {
		return err
	}
	if !PartitionConstraintReplayImport(e, op, auth) {
		return fmt.Errorf("stuntgres: invalid trusted restore partition index authority")
	}
	ref, err := e.ResolveRelationReference(auth, raw.Name, true)
	if err != nil {
		return err
	}
	table, ok := auth.Catalog.Relation(ref.Address)
	if !ok {
		return ids.ErrCatalogReplaced
	}
	OperationReplay(op).PartitionUsingIndex = partition.CaptureUsingIndexReplay(auth.Catalog, raw, table)
	return nil
}
