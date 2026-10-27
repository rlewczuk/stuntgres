package catalog

import "stuntgres/internal/engine/ids"

// RelationPayloadAddresses enumerates catalog identities owned by this exact
// immutable relation generation. Attributes are not whole-relation payloads.
func RelationPayloadAddresses(d *Relation) []ids.ObjectAddress {
	var out []ids.ObjectAddress
	add := func(class ids.OID, oid ids.OID) {
		if oid != 0 {
			out = append(out, ids.ObjectAddress{ClassID: class, ObjectID: oid})
		}
	}
	for _, x := range d.ColumnMeta.Defaults {
		add(2604, x.OID)
	}
	for _, x := range d.ColumnMeta.Generations {
		add(2604, x.OID)
	}
	for _, x := range d.Constraints.NotNull {
		add(2606, x.OID)
	}
	for _, x := range d.Constraints.Check {
		add(2606, x.OID)
	}
	for _, x := range d.Constraints.Unique {
		add(2606, x.OID)
	}
	for _, x := range d.Constraints.Exclusion {
		add(2606, x.OID)
	}
	if d.View != nil {
		add(2618, d.View.RuleOID())
	}
	for _, x := range d.Constraints.ForeignKeys {
		add(2606, x.OID)
		for _, trigger := range x.TriggerOIDs {
			add(2620, trigger)
		}
	}
	_ = d.VisitRules(func(x RuleFact) error { add(2618, x.OID); return nil })
	_ = d.VisitTriggers(func(x TriggerFact) error { add(2620, x.OID); return nil })
	for _, x := range d.Security.Policies {
		add(3256, x.OID)
	}
	return out
}
