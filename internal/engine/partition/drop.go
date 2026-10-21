package partition

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func DependsOn(relation *catalog.Relation, target ids.ObjectAddress) bool {
	if relation.Partition == nil {
		return false
	}
	for _, key := range relation.Partition.Keys {
		if target.ClassID == ids.TypeClassID && ids.OID(key.Usage.Type.ID()) == target.ObjectID {
			return true
		}
		if target.ClassID == ids.CollationClassID && target.SubID == 0 && target.ObjectID != 0 &&
			(ids.OID(key.Usage.Collation.ID) == target.ObjectID || ids.OID(key.Collation) == target.ObjectID) {
			return true
		}
		if key.Attribute != 0 && target.ClassID == ids.RelationClassID && target.ObjectID == relation.Address().ObjectID && target.SubID == key.Attribute {
			return true
		}
		if key.Derived != nil {
			for _, ref := range key.Derived.Dependencies {
				address := ref.Address
				if address.ClassID == target.ClassID && address.ObjectID == target.ObjectID && (target.SubID == 0 || address.SubID == target.SubID) {
					return true
				}
			}
		}
	}
	return false
}
