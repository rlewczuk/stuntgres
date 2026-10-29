package core

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/publication"
	"stuntgres/internal/parser"
)

// PublicationActions includes every potential modifying arm, even when the
// source is empty, an arm is unreachable, or insertion finds no conflict.
func PublicationActions(stmt parser.Statement) publication.Actions {
	var actions publication.Actions
	switch s := stmt.(type) {
	case *parser.Update:
		actions.Update = true
	case *parser.Delete:
		actions.Delete = true
	case *parser.Insert:
		actions.Update = s.Conflict != nil && s.Conflict.Update
	case *parser.Merge:
		for _, arm := range s.Arms {
			actions.Update = actions.Update || arm.Action == parser.MergeUpdateAction
			actions.Delete = actions.Delete || arm.Action == parser.MergeDeleteAction
		}
	}
	return actions
}

// CheckPublicationAdmission is Execute-only admission. The supplied read must
// retain the statement's admitted partition topology and current definitions;
// physical identity is never borrowed from a partition root. It runs before
// row production, not inside a matching-row loop.
func CheckPublicationAdmission(v catalog.Read, target *catalog.Relation, stmt parser.Statement) error {
	actions := PublicationActions(stmt)
	if target == nil || !actions.Update && !actions.Delete {
		return nil
	}
	return partition.PhysicalDescendants(v, target, func(leaf *catalog.Relation) error {
		return publication.CheckReplicaIdentity(v, leaf, actions)
	})
}
