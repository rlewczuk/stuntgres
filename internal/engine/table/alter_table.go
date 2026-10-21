package table

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqlstate"
)

// Named ALTER lookup rejects protected relations before action preparation.
func AlterTableLookupKind(d *catalog.Relation) error {
	switch d.RelationKind {
	case catalog.CatalogRelation:
		return sqlError("42501", fmt.Sprintf(`permission denied: "%s" is a system catalog`, d.Name()))
	case catalog.CompositeRelation:
		diagnostic := sqlError("42809", fmt.Sprintf(`"%s" is a composite type`, d.Name())).(*sqlstate.Error)
		diagnostic.Hint = "Use ALTER TYPE instead."
		return diagnostic
	default:
		return nil
	}
}
