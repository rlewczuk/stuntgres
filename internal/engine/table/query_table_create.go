package table

import (
	"stuntgres/internal/sqltype"
)

// Concrete catalog carriers are not necessarily physical SQL storage values.
// Resolve existing domain/array wrappers without adding a public type framework.
func ValidateQueryTableOutputType(typ *sqltype.Descriptor) error {
	for {
		physical := typ.BaseType()
		if !physical.IsArray() {
			if physical.ID() == sqltype.PgNodeTree {
				return sqlError("0A000", "query table output type pg_node_tree is unsupported")
			}
			return nil
		}
		// Reject direct enum elements, not the supported domain-over-enum
		// element arrays. Outer physical wrappers still reach this boundary.
		if physical.Element().IsEnum() {
			return sqlError("0A000", "unsupported array element type")
		}
		typ = physical.Element()
	}
}
