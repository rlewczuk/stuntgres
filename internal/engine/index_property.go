package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/sqltype"
)

func (h *statementHost) IndexProperty(kind functions.IndexPropertyKind, oid uint32, ordinalValue sqltype.Value, property string) (value, known bool, err error) {
	scope := h.scope()
	if scope.inquiry.commentCatalog.State() == nil {
		return false, false, fmt.Errorf("stuntgres: missing index property catalog snapshot")
	}
	relation, ok := scope.inquiry.commentCatalog.Relation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(oid)})
	if !ok || relation.SQLIndex == nil {
		return false, false, nil
	}
	if kind == functions.WholeIndexProperty {
		switch relation.SQLIndex.Method {
		case catalog.BtreeMethod, catalog.HashMethod, catalog.GistMethod:
			switch property {
			case "clusterable":
				value, known = relation.SQLIndex.Method != catalog.HashMethod, true
			case "backward_scan":
				value, known = relation.SQLIndex.Method != catalog.GistMethod, true
			case "index_scan", "bitmap_scan":
				value, known = true, true
			}
		}
	} else {
		ordinal, err := ordinalValue.Integer()
		if err != nil {
			return false, false, err
		}
		value, known = index.ColumnCapability(relation.SQLIndex, ordinal, property)
	}
	return value, known, nil
}
