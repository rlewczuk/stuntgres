package tablecmds

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
)

// stagePartitionIndexChildren serves the partitioned UNIQUE replay executor.
// The caller holds Engine.mu.
func stagePartitionIndexChildren(e *core.Engine, op *session.Operation, stage *core.CatalogStage, auth core.AuthorizationView, table, idx *catalog.Relation, allowIncompleteVirtual bool) error {
	return index.StagePartitionIndexChildren(TableHost{core.DDLHost{E: e, Op: op, CatalogStage: stage}}, auth, table, idx, allowIncompleteVirtual)
}
