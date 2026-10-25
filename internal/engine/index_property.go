package engine

import (
	"fmt"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/sqltype"
)

func (h *statementHost) IndexProperty(kind functions.IndexPropertyKind, oid uint32, ordinalValue sqltype.Value, property string) (value, known bool, err error) {
	scope := h.scope()
	if scope.Inquiry.CommentCatalog.State() == nil {
		return false, false, fmt.Errorf("stuntgres: missing index property catalog snapshot")
	}
	if kind == functions.AccessMethodProperty {
		method, ok := scope.Inquiry.CommentCatalog.AccessMethod(ids.OID(oid))
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
	relation, ok := scope.Inquiry.CommentCatalog.Relation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(oid)})
	if !ok || relation.SQLIndex == nil {
		return false, false, nil
	}
	if kind == functions.WholeIndexProperty {
		value, known = index.Property(relation.SQLIndex, true, 0, property)
	} else {
		ordinal, err := ordinalValue.Integer()
		if err != nil {
			return false, false, err
		}
		value, known = index.Property(relation.SQLIndex, false, ordinal, property)
	}
	return value, known, nil
}
