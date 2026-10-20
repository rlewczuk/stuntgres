package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
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
		value, known = indexColumnCapability(relation.SQLIndex, ordinal, property)
	}
	return value, known, nil
}

func indexColumnCapability(index *catalog.IndexDefinition, ordinal int64, property string) (bool, bool) {
	if ordinal <= 0 || ordinal > int64(len(index.Keys)+len(index.Include)) {
		return false, false
	}
	if index.Method == catalog.GistMethod {
		key := ordinal <= int64(len(index.Keys))
		switch property {
		case "orderable":
			return false, true
		case "asc", "desc", "nulls_first", "nulls_last", "search_array":
			return false, key
		case "search_nulls":
			return key, key
		case "distance_orderable", "returnable":
			if !key {
				// GiST's property callback finds no opclass for INCLUDE,
				// before the generic nonkey-column fallback can run.
				return false, false
			}
			switch index.Keys[ordinal-1].Class.OID() {
			case 10017, 10078: // inet_ops has fetch; range_ops has no compress.
				// Neither admitted class supplies distance ordering.
				return property == "returnable", true
			}
		}
		return false, false
	}
	if index.Method == catalog.HashMethod {
		switch property {
		case "asc", "desc", "nulls_first", "nulls_last", "orderable", "distance_orderable", "returnable", "search_array", "search_nulls":
			return false, true
		default:
			return false, false
		}
	}
	key := ordinal <= int64(len(index.Keys))
	switch property {
	case "orderable":
		return key, true
	case "distance_orderable":
		return false, true
	case "returnable":
		return true, true
	case "search_array", "search_nulls":
		return key, key
	}
	if !key {
		return false, false
	}
	definition := index.Keys[ordinal-1]
	switch property {
	case "asc":
		return !definition.Desc, true
	case "desc":
		return definition.Desc, true
	case "nulls_first":
		return definition.NullsFirst, true
	case "nulls_last":
		return !definition.NullsFirst, true
	}
	return false, false
}
