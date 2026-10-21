package index

import (
	"stuntgres/internal/engine/catalog"
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

// RetainExistingOutput keeps a probe's output of row on idx. The mixed lane
// may need ordinary prior-own keys again after catalog drift; freeze actual
// probe data by immutable row/version, not CHECK truth or values.
func (x *Scope) RetainExistingOutput(idx *catalog.Relation, row storage.RowVersion, output storage.IndexOutput) {
	if x.ExistingOutputs == nil {
		x.ExistingOutputs = make(map[ids.ObjectAddress]map[RowIdentity]storage.IndexOutput)
	}
	outputs := x.ExistingOutputs[idx.Address()]
	if outputs == nil {
		outputs = make(map[RowIdentity]storage.IndexOutput)
		x.ExistingOutputs[idx.Address()] = outputs
	}
	outputs[RowIdentity{Row: row.Row, Version: row.Version}] = output
}
