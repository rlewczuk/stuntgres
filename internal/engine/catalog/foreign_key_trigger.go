package catalog

import (
	"iter"
	"strconv"
	"stuntgres/internal/engine/ids"
)

// ForeignKeyTriggerFact is an immutable logical trigger identity owned by one
// constraint. Slots 0/1 execute on the referenced relation; 2/3 on its owner.
type ForeignKeyTriggerFact struct {
	OID             ids.OID
	Name            string
	Relation, Owner ids.ObjectAddress
	Constraint      ForeignKeyConstraint
	Slot            int
	Mode            TriggerMode
}

func ForeignKeyTriggerFacts(view Read) iter.Seq[ForeignKeyTriggerFact] {
	return func(yield func(ForeignKeyTriggerFact) bool) {
		for _, owner := range view.Relations() {
			for _, constraint := range owner.Constraints.ForeignKeys {
				for slot, oid := range constraint.TriggerOIDs {
					if oid == 0 {
						continue
					}
					relation, kind := owner.Address(), "c"
					if slot < 2 {
						relation, kind = constraint.Target, "a"
					}
					fact := ForeignKeyTriggerFact{OID: oid, Name: "RI_ConstraintTrigger_" + kind + "_" + strconv.FormatUint(uint64(oid), 10), Relation: relation, Owner: owner.Address(), Constraint: constraint, Slot: slot, Mode: constraint.TriggerMode(slot)}
					if !yield(fact) {
						return
					}
				}
			}
		}
	}
}
