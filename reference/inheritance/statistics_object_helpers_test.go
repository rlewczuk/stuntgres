package reference_test

import "strings"

// lib/pq does not name these released OIDs in its static type-name table.
// Native descriptors still expose the exact database type name; catalog type
// identities are asserted independently, not inferred from adapter labels.
func statisticsDatabaseTypeName(postgres bool, name string) string {
	if postgres {
		return ""
	}
	return strings.ToUpper(name)
}
