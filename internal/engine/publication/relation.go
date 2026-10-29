package publication

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/table"
)

func checkRelation(v catalog.Read, r *catalog.Relation) error {
	if IsPublishable(v, r) {
		return nil
	}
	kind := "system tables"
	switch r.RelationKind {
	case catalog.ViewRelation:
		kind = "views"
	case catalog.MaterializedViewRelation:
		kind = "materialized views"
	case catalog.ForeignRelation:
		kind = "foreign tables"
	case catalog.SequenceRelation:
		kind = "sequences"
	default:
		switch table.EffectivePersistence(v, r) {
		case "temporary":
			kind = "temporary tables"
		case "unlogged":
			kind = "unlogged tables"
		}
	}
	e := failure("22023", fmt.Sprintf(`cannot add relation "%s" to publication`, r.Name()))
	e.Detail = "This operation is not supported for " + kind + "."
	return e
}
