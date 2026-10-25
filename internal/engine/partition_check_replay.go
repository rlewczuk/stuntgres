package engine

import (
	"fmt"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// This capability is an exact private-import target, not a public hierarchy
// ALTER policy. The ordinary ALTER binder, providers, stage and Finish still own
// every definition and publication check.
func beginPartitionCheckReplay(e *Engine, op *session.Operation, raw *parser.AlterTable) error {
	if op == nil {
		return fmt.Errorf("stuntgres: invalid trusted restore partition CHECK operation")
	}
	OperationReplay(op).PartitionCheck = nil
	if !partition.CheckReplayActions(raw) {
		return nil
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.StatementAuthorizationLocked(op, raw)
	if err != nil {
		return err
	}
	if !PartitionConstraintReplayImport(e, op, auth) {
		return fmt.Errorf("stuntgres: invalid trusted restore partition CHECK authority")
	}
	if err := table.RestoreAlterConstraints(raw); err != nil {
		return err
	}
	ref, err := e.ResolveRelationReference(auth, raw.Name, true)
	if err != nil {
		return err
	}
	d, ok := auth.Catalog.Relation(ref.Address)
	if !ok {
		return ids.ErrCatalogReplaced
	}
	if partition.CheckReplayTarget(auth.Catalog, d) {
		OperationReplay(op).PartitionCheck = d
	}
	return nil
}
