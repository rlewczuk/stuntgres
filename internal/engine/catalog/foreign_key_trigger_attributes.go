package catalog

import (
	"fmt"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// ForeignKeyTriggerAttributes is the shared logical RI trigger description used
// by catalog projection and definition presentation. It grants no execution.
type ForeignKeyTriggerAttributes struct {
	Function                      ids.OID
	Flags                         int64
	Opposite                      ids.ObjectAddress
	Deferrable, InitiallyDeferred bool
}

// Attributes validates the identity and backing index against the captured root.
// simpleAttribute is supplied by the expression owner, not a second catalog binder.
func (fact ForeignKeyTriggerFact) Attributes(view Read, simpleAttribute func(ids.ObjectAddress, IndexKey) (int32, bool)) (ForeignKeyTriggerAttributes, error) {
	var result ForeignKeyTriggerAttributes
	constraint := fact.Constraint
	for _, oid := range constraint.TriggerOIDs {
		if oid == 0 {
			return result, fmt.Errorf("stuntgres: incomplete foreign-key trigger identity")
		}
	}
	owner, ownerOK := view.Relation(fact.Owner)
	parent, parentOK := view.Relation(constraint.Target)
	index, indexOK := view.Relation(constraint.Index)
	if !ownerOK || !parentOK || !indexOK || owner.RelationKind != TableRelation || parent.RelationKind != TableRelation || constraint.OID == 0 || len(constraint.Attributes) == 0 || len(constraint.Attributes) != len(constraint.Referenced) || index.SQLIndex == nil || index.SQLIndex.Parent != parent.Address() || !index.SQLIndex.Unique || !index.SQLIndex.Valid || !index.SQLIndex.Ready || !index.SQLIndex.Live || index.SQLIndex.Predicate != nil || len(index.SQLIndex.Keys) != len(constraint.Referenced) {
		return result, fmt.Errorf("stuntgres: invalid foreign-key trigger provenance")
	}
	for i, attribute := range constraint.Attributes {
		matched := false
		for _, key := range index.SQLIndex.Keys {
			number, simple := simpleAttribute(parent.Address(), key)
			if simple && number == constraint.Referenced[i] {
				matched = true
				break
			}
		}
		if !matched {
			return result, fmt.Errorf("stuntgres: foreign-key trigger index authority mismatch")
		}
		if !owner.HasAttribute(attribute) || !parent.HasAttribute(constraint.Referenced[i]) {
			return result, fmt.Errorf("stuntgres: missing foreign-key trigger attribute")
		}
	}
	if fact.Slot < 0 || fact.Slot >= len(constraint.TriggerOIDs) || fact.OID != constraint.TriggerOIDs[fact.Slot] {
		return result, fmt.Errorf("stuntgres: invalid foreign-key trigger identity")
	}
	result.Flags, result.Opposite, result.Deferrable = 17, constraint.Target, constraint.Deferrable
	expected := fact.Owner
	switch fact.Slot {
	case 0, 1:
		expected, result.Opposite = constraint.Target, fact.Owner
		action := constraint.UpdateAction
		if fact.Slot == 0 {
			result.Flags, action = 9, constraint.DeleteAction
		}
		result.Deferrable = result.Deferrable && action == parser.ForeignKeyNoAction
		switch action {
		case parser.ForeignKeyNoAction:
			result.Function = 1654
		case parser.ForeignKeyRestrict:
			result.Function = 1648
		case parser.ForeignKeyCascade:
			result.Function = 1646
		case parser.ForeignKeySetNull:
			result.Function = 1650
		case parser.ForeignKeySetDefault:
			result.Function = 1652
		default:
			return result, fmt.Errorf("stuntgres: invalid foreign-key trigger action")
		}
		if fact.Slot == 1 {
			result.Function++
		}
	case 2:
		result.Flags, result.Function = 5, 1644
	case 3:
		result.Function = 1645
	default:
		return result, fmt.Errorf("stuntgres: invalid foreign-key trigger slot")
	}
	if fact.Relation != expected {
		return result, fmt.Errorf("stuntgres: invalid foreign-key trigger relation")
	}
	result.InitiallyDeferred = result.Deferrable && constraint.InitiallyDeferred
	return result, nil
}
