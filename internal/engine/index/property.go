package index

import (
	"stuntgres/internal/engine/catalog"
)

func ColumnCapability(index *catalog.IndexDefinition, ordinal int64, property string) (bool, bool) {
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

// Property answers pg_index_has_property (whole) or
// pg_index_column_has_property for ordinal; known is false for a property
// the method does not report.
func Property(def *catalog.IndexDefinition, whole bool, ordinal int64, property string) (value, known bool) {
	if !whole {
		return ColumnCapability(def, ordinal, property)
	}
	switch def.Method {
	case catalog.BtreeMethod, catalog.HashMethod, catalog.GistMethod:
		switch property {
		case "clusterable":
			return def.Method != catalog.HashMethod, true
		case "backward_scan":
			return def.Method != catalog.GistMethod, true
		case "index_scan", "bitmap_scan":
			return true, true
		}
	}
	return false, false
}
