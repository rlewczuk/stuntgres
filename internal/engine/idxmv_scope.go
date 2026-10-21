package engine

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

// indexScope is the statement's index slot: the index maintenance grant,
// index outputs retained for probes and the exclusion-constraint projection.
type indexScope struct {
	Maintenance      *indexMaintenanceGrant
	ExistingOutputs  map[ids.ObjectAddress]map[indexRowIdentity]storage.IndexOutput
	exclusionModes   exclusionModes
	ExclusionStarted bool
}

// endStatement drops the index outputs retained for probes and the index
// maintenance grant. Exclusion modes stay as the statement left them.
func (x *indexScope) EndStatement() {
	if x == nil {
		return
	}
	x.ExistingOutputs, x.Maintenance = nil, nil
}
