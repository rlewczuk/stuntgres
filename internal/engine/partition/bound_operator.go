package partition

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// BoundOperator selects the saved opclass's actual family member, not a default
// operator of the key's SQL type or a same-spelling routine from another family.
func BoundOperator(view catalog.Read, key catalog.PartitionKeyEntry, strategy int16) (*catalog.Operator, error) {
	class, ok := view.OperatorClass(ids.OID(key.Class.OID()))
	if !ok {
		return nil, fmt.Errorf("stuntgres: unavailable partition operator class")
	}
	member, ok := view.FamilyOperator(class.Family, class.InputType, class.InputType, strategy, 's')
	if !ok {
		return nil, fmt.Errorf("stuntgres: unavailable partition family operator")
	}
	op, ok := view.Operator(ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: member.Operator})
	if !ok {
		return nil, fmt.Errorf("stuntgres: unavailable partition comparison operator")
	}
	return op, nil
}
