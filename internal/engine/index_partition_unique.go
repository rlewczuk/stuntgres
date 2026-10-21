package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
)

// stagePartitionIndexChild serves table ATTACH and PARTITION OF until the
// partition decisions move (25d). The caller holds Engine.mu.
func (e *Engine) stagePartitionIndexChild(op *session.Operation, stage *CatalogStage, table, parentIndex, child *catalog.Relation, allowIncompleteVirtual bool) (*catalog.Relation, error) {
	return index.StagePartitionIndexChild(ddlHost{e: e, op: op, stage: stage}, table, parentIndex, child, allowIncompleteVirtual)
}

// stagePartitionIndexChildren serves partition replay until 25d. The caller
// holds Engine.mu.
func (e *Engine) stagePartitionIndexChildren(op *session.Operation, stage *CatalogStage, table, idx *catalog.Relation, allowIncompleteVirtual bool) error {
	return index.StagePartitionIndexChildren(ddlHost{e: e, op: op, stage: stage}, table, idx, allowIncompleteVirtual)
}
