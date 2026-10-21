package index

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

// Scope is the statement's index slot: the index maintenance grant,
// index outputs retained for probes and the exclusion-constraint projection.
type Scope struct {
	Maintenance      *MaintenanceGrant
	ExistingOutputs  map[ids.ObjectAddress]map[RowIdentity]storage.IndexOutput
	ExclusionModes   ExclusionModes
	ExclusionStarted bool
}

// EndStatement drops the index outputs retained for probes and the index
// maintenance grant. Exclusion modes stay as the statement left them.
func (x *Scope) EndStatement() {
	if x == nil {
		return
	}
	x.ExistingOutputs, x.Maintenance = nil, nil
}
