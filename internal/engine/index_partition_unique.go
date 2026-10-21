package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
)

// stagePartitionIndexChildren serves the partitioned UNIQUE replay executor.
// The caller holds Engine.mu.
func (e *Engine) stagePartitionIndexChildren(op *session.Operation, stage *CatalogStage, table, idx *catalog.Relation, allowIncompleteVirtual bool) error {
	return index.StagePartitionIndexChildren(ddlHost{e: e, op: op, stage: stage}, table, idx, allowIncompleteVirtual)
}
