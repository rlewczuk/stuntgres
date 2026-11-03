package index

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
)

// Scope is the statement's index slot: the index maintenance grant,
// index outputs retained for probes and the exclusion-constraint projection.
type Scope struct {
	Maintenance        *MaintenanceGrant
	ExistingOutputs    map[ids.ObjectAddress]map[RowIdentity]storage.IndexOutput
	ConstraintModes    ConstraintModes
	ConstraintsStarted bool
	GinSourceCaptured  bool
	GinSourceRevision  uint64
	eventsOwned        bool
}

// EndStatement drops the index outputs retained for probes and the index
// maintenance grant. Exclusion modes stay as the statement left them.
func (x *Scope) EndStatement() {
	if x == nil {
		return
	}
	x.ExistingOutputs, x.Maintenance = nil, nil
	x.GinSourceCaptured = false
}

// AppendConstraintEvent detaches the transaction/savepoint event slice once
// before appending a command's owned suffix.
func (x *Scope) AppendConstraintEvent(event ConstraintEvent) {
	if !x.eventsOwned {
		old := x.ConstraintModes.Events
		next := make([]ConstraintEvent, len(old)+1, max(len(old)+1, 2*len(old)))
		copy(next, old)
		next[len(old)] = event
		x.ConstraintModes.Events = next
		x.eventsOwned = true
		return
	}
	x.ConstraintModes.Events = append(x.ConstraintModes.Events, event)
}

// ClaimConstraintEvent removes membership before any executable callback.
// Never edit a predecessor's backing: a savepoint may restore consumed events.
func (x *Scope) ClaimConstraintEvent(at int) ConstraintEvent {
	old := x.ConstraintModes.Events
	event := old[at]
	switch {
	case len(old) == 1:
		x.ConstraintModes.Events = nil
	case at == 0:
		x.ConstraintModes.Events = old[1:len(old):len(old)]
	case at == len(old)-1:
		x.ConstraintModes.Events = old[:at:at]
	default:
		next := make([]ConstraintEvent, len(old)-1)
		copy(next, old[:at])
		copy(next[at:], old[at+1:])
		x.ConstraintModes.Events = next
	}
	x.eventsOwned = true
	return event
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
