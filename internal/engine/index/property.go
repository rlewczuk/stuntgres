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
	if index.Method == catalog.GistMethod || index.Method == catalog.SpgistMethod {
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
				if index.Method == catalog.SpgistMethod && property == "returnable" {
					return true, true
				}
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
				if index.Method == catalog.SpgistMethod {
					_, compress := view.FamilySupport(family, input, input, 6)
					return !compress, true
				}
				_, fetch := view.FamilySupport(family, input, input, 9)
				_, compress := view.FamilySupport(family, input, input, 3)
				return fetch || !compress, true
			}
			_, distance := view.FamilySupport(family, input, input, 8)
			if distance || index.Method == catalog.SpgistMethod {
				for _, descriptor := range view.Descriptors() {
					member, ok := descriptor.(*catalog.OperatorFamilyOperator)
					if !ok {
						continue
					}
					inputMember := member.Left == input || index.Method == catalog.SpgistMethod && member.Right == input
					if member.Family == family && inputMember && member.Method == ids.OID(class.MethodOID()) && member.Purpose == 'o' && member.SortFamily != 0 {
						operator, exists := view.Operator(ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: member.Operator})
						if !exists {
							continue
						}
						if _, sortable := view.FamilySupport(member.SortFamily, operator.Result, operator.Result, 1); !sortable {
							continue
						}
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
	case catalog.BtreeMethod, catalog.HashMethod, catalog.GistMethod, catalog.SpgistMethod:
		switch property {
		case "clusterable":
			return def.Method != catalog.HashMethod && def.Method != catalog.SpgistMethod, true
		case "backward_scan":
			return def.Method != catalog.GistMethod && def.Method != catalog.SpgistMethod, true
		case "index_scan", "bitmap_scan":
			return true, true
		}
	}
	return false, false
}
