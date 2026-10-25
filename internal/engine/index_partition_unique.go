package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
)

// stagePartitionIndexChildren serves the partitioned UNIQUE replay executor.
// The caller holds Engine.mu.
func (e *Engine) stagePartitionIndexChildren(op *session.Operation, stage *CatalogStage, table, idx *catalog.Relation, allowIncompleteVirtual bool) error {
	return index.StagePartitionIndexChildren(DDLHost{E: e, Op: op, CatalogStage: stage}, table, idx, allowIncompleteVirtual)
}
