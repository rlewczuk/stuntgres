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
	operationReplay(op).partitionUsingIndex = CaptureUsingIndexReplay(auth.Catalog, raw, table)
	return nil
}

func partitionUsingIndexReplayAllowed(op *session.Operation, auth AuthorizationView, raw *parser.AlterTable, table *catalog.Relation) bool {
	if op == nil || raw == nil || operationReplayOrZero(op).partitionUsingIndex == nil {
		return false
	}
	return usingIndexReplayMatches(operationReplayOrZero(op).partitionUsingIndex, auth.Catalog, raw, table) && partitionConstraintReplayImport(auth.engine, op, auth)
}
