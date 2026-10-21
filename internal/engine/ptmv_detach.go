package engine

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqlstate"
)

func PartitionDetachMembership(parent, child *catalog.Relation) error {
	if child.Membership == nil || child.Membership.Parent != parent.Address() || !slices.Contains(parent.Partition.Children, child.Address()) {
		return sqlError("42P01", fmt.Sprintf(`relation "%s" is not a partition of relation "%s"`, child.Name(), parent.Name()))
	}
	return nil
}

func PartitionDetachKindError(view catalog.Read, d *catalog.Relation, opening bool) error {
	kind := "relations"
	switch d.RelationKind {
	case catalog.TableRelation:
		kind = "tables"
	case catalog.ViewRelation:
		kind = "views"
	case catalog.MaterializedViewRelation:
		kind = "materialized views"
	case catalog.SequenceRelation:
		kind = "sequences"
	case catalog.CompositeRelation:
		kind = "composite types"
	case catalog.IndexRelation:
		kind = "indexes"
		if table, ok := view.Relation(d.SQLIndex.Parent); ok && table.Partition != nil {
			kind = "partitioned indexes"
		}
	}
	message := fmt.Sprintf(`ALTER action DETACH PARTITION cannot be performed on relation "%s"`, d.Name())
	if opening {
		message = fmt.Sprintf(`cannot open relation "%s"`, d.Name())
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: message, Detail: "This operation is not supported for " + kind + "."}
}
