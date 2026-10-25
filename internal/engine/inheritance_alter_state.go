package engine

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
)

// One statement-owned stage carries every candidate; the ordinary provider
// pipeline still owns cooking, row/index rewrites and physical validation.
// Nothing is installed in the transaction owner or live catalog between nodes.
type InheritanceAlterState struct {
	table.InheritanceAlterPlan
	Stage   *CatalogStage
	Changes *session.Journal
	// Prepared holds the root's prepared nodes, keyed by relation.
	Prepared map[ids.ObjectAddress]*table.InheritancePreparedNode
}
