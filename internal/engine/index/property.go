package index

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func ColumnCapability(view catalog.Read, index *catalog.IndexDefinition, ordinal int64, property string) (bool, bool) {
	if ordinal <= 0 || ordinal > int64(len(index.Keys)+len(index.Include)) {
		return false, false
	}
	if index.Method == catalog.BrinMethod {
		switch property {
		case "asc", "desc", "nulls_first", "nulls_last", "orderable", "distance_orderable", "returnable", "search_array":
			return false, true
		case "search_nulls":
			return true, true
		}
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
			class := index.Keys[ordinal-1].Class
			current, ok := view.IndexClass(ids.OID(class.OID()))
			if !ok || !class.SameIdentity(current) {
				return false, false
			}
			family, input := ids.OID(class.FamilyOID()), ids.OID(class.InputOID())
			if property == "returnable" {
				_, fetch := view.FamilySupport(family, input, input, 9)
				_, compress := view.FamilySupport(family, input, input, 3)
				return fetch || !compress, true
			}
			if _, distance := view.FamilySupport(family, input, input, 8); distance {
				for _, descriptor := range view.Descriptors() {
					if member, ok := descriptor.(*catalog.OperatorFamilyOperator); ok && member.Family == family && member.Left == input && member.Method == ids.OID(class.MethodOID()) && member.Purpose == 'o' && member.SortFamily != 0 {
						return true, true
					}
				}
			}
			return false, true
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
func Property(view catalog.Read, def *catalog.IndexDefinition, whole bool, ordinal int64, property string) (value, known bool) {
	if !whole {
		return ColumnCapability(view, def, ordinal, property)
	}
	if def.Method == catalog.BrinMethod {
		switch property {
		case "clusterable", "index_scan", "backward_scan":
			return false, true
		case "bitmap_scan":
			return true, true
		}
		return false, false
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
