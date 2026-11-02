package explain

import (
	"fmt"
	"maps"
	"slices"

	"stuntgres/internal/engine/catalog"
)

// TEXT consumes the same detached facts as structured formats. Identifier
// values are quoted individually; labels are fixed structure, not SQL data.
func rangeLines(value any, indent, label string, lines *[]string) {
	prefix := indent
	if label != "" {
		prefix += label + ":"
	}
	switch value := value.(type) {
	case map[string]any:
		if label != "" {
			*lines = append(*lines, prefix)
			indent += "  "
		}
		for _, key := range slices.Sorted(maps.Keys(value)) {
			rangeLines(value[key], indent, key, lines)
		}
	case []any:
		*lines = append(*lines, prefix)
		for _, item := range value {
			rangeLines(item, indent+"  ", "Item", lines)
		}
	case []string:
		*lines = append(*lines, prefix)
		for _, item := range value {
			*lines = append(*lines, indent+"  "+catalog.Identifier(item))
		}
	case string:
		switch label {
		case "Range Name", "Relation Name", "Schema", "Principal":
			value = catalog.Identifier(value)
		}
		*lines = append(*lines, prefix+" "+value)
	default:
		*lines = append(*lines, fmt.Sprintf("%s %v", prefix, value))
	}
}
