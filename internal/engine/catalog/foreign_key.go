package catalog

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// All links are stable catalog addresses/attributes, never descriptor pointers.
type ForeignKeyConstraint struct {
	OID ids.OID
	// TriggerOIDs are DELETE/UPDATE action triggers on Target, followed by
	// INSERT/UPDATE check triggers on the owning relation.
	TriggerOIDs      [4]ids.OID
	TriggerModes     [4]TriggerMode
	Name             string
	Attributes       []int32
	Target           ids.ObjectAddress
	Referenced       []int32
	Index            ids.ObjectAddress
	Full             bool
	UpdateAction     parser.ForeignKeyAction
	DeleteAction     parser.ForeignKeyAction
	DeleteAttributes []int32
	NotValid         bool
}

func (f ForeignKeyConstraint) TriggerMode(slot int) TriggerMode {
	return f.TriggerModes[slot].Normalized()
}
