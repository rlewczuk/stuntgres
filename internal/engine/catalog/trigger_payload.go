package catalog

import "stuntgres/internal/engine/ids"

// TriggersPayload is relation-owned, immutable and sorted by table-local name.
// Executable recipes remain in stored above the catalog layer.
type TriggersPayload interface {
	Validate(Read, *Relation) error
	Len() int
	// OID returns the OID of trigger i in stored order.
	OID(i int) ids.OID
	Visit(func(TriggerFact) error) error
}

type TriggerFact struct {
	OID            ids.OID
	Name           string
	Procedure      ids.ObjectAddress
	Parent         ids.ObjectAddress
	References     []ids.ObjectAddress
	AutoExtensions []ids.ObjectAddress
}

func (d *Relation) HasTriggers() bool { return d.Triggers != nil && d.Triggers.Len() != 0 }
func (d *Relation) VisitTriggers(visit func(TriggerFact) error) error {
	if d.Triggers == nil {
		return nil
	}
	return d.Triggers.Visit(visit)
}
