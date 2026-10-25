package engine

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// BeginPartitionUniqueReplay records on a private Restore operation the
// partition key UNIQUE origin its ALTER TABLE replays, or clears the record.
//
// This origin belongs to one private-import operation and one captured definition,
// target and subtree, after its intended plain tree is already attached.
func BeginPartitionUniqueReplay(e *core.Engine, op *session.Operation, raw *parser.AlterTable) error {
	if op == nil {
		return fmt.Errorf("stuntgres: invalid trusted restore partition unique operation")
	}
	core.OperationReplay(op).PartitionUnique = nil
	if raw == nil || raw.Only || raw.IfExists || len(raw.Actions) != 1 || raw.Actions[0].Action != parser.AddUnique || raw.Actions[0].Unique == nil {
		return nil
	}
	definition := raw.Actions[0].Unique
	if !strings.HasPrefix(definition.Name.Text, "__stuntgres_partition_key_") {
		return nil
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.StatementAuthorizationLocked(op, raw)
	if err != nil {
		return err
	}
	if !core.PartitionConstraintReplayImport(e, op, auth) {
		return fmt.Errorf("stuntgres: invalid trusted restore partition unique authority")
	}
	ref, err := e.ResolveRelationReference(auth, raw.Name, true)
	if err != nil {
		return err
	}
	table, ok := auth.Catalog.Relation(ref.Address)
	if !ok {
		return ids.ErrCatalogReplaced
	}
	captured, err := partition.CaptureUniqueReplay(auth.Catalog, raw, table)
	if err != nil {
		return err
	}
	core.OperationReplay(op).PartitionUnique = captured
	return nil
}
