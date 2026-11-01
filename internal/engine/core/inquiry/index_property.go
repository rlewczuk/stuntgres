package inquiry

import (
	"fmt"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/sqltype"
)

func IndexProperty(h CatalogHost, kind functions.IndexPropertyKind, oid uint32, ordinalValue sqltype.Value, property string) (value, known bool, err error) {
	read, _ := h.CommentSnapshot()
	if read.State() == nil {
		return false, false, fmt.Errorf("stuntgres: missing index property catalog snapshot")
	}
	if kind == functions.AccessMethodProperty {
		method, ok := read.AccessMethod(ids.OID(oid))
		if !ok {
			return false, false, nil
		}
		physical, indexKind := method.IndexImplementation()
		if !indexKind {
			return false, false, nil
		}
		value, known := index.MethodProperty(physical, property)
		return value, known, nil
	}
	relation, ok := read.Relation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(oid)})
	if !ok || relation.SQLIndex == nil {
		return false, false, nil
	}
	if kind == functions.WholeIndexProperty {
		value, known = index.Property(read, relation.SQLIndex, true, 0, property)
	} else {
		ordinal, err := ordinalValue.Integer()
		if err != nil {
			return false, false, err
		}
		value, known = index.Property(read, relation.SQLIndex, false, ordinal, property)
	}
	return value, known, nil
}
