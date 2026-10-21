package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/index"
)

// Property answers pg_index_has_property (whole) or
// pg_index_column_has_property for ordinal; known is false for a property
// the method does not report.
func Property(def *catalog.IndexDefinition, whole bool, ordinal int64, property string) (value, known bool) {
	if !whole {
		return index.ColumnCapability(def, ordinal, property)
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
