package engine

import (
	"fmt"
	"maps"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqlstate"
)

func renameMissing(err error) bool {
	d, ok := err.(*sqlstate.Error)
	return ok && (d.Code == "42P01" || d.Code == "3F000")
}

func renameColumnCandidate(old *catalog.Relation, name, destination string) (*catalog.Relation, error) {
	if old.RelationKind != catalog.CompositeRelation && catalog.IsSystemColumn(name) {
		return nil, sqlError("0A000", fmt.Sprintf(`cannot rename system column "%s"`, name))
	}
	slot, exists := old.Index[name]
	if !exists {
		return nil, sqlError("42703", fmt.Sprintf(`column "%s" does not exist`, name))
	}
	if old.RelationKind != catalog.CompositeRelation && catalog.IsSystemColumn(destination) {
		return nil, sqlError("42701", fmt.Sprintf(`column name "%s" conflicts with a system column name`, destination))
	}
	if _, exists := old.Index[destination]; exists {
		return nil, sqlError("42701", fmt.Sprintf(`column "%s" of relation "%s" already exists`, destination, old.Name()))
	}
	next := *old
	next.Columns = slices.Clone(old.Columns)
	next.Columns[slot].Name = destination
	next.Index = maps.Clone(old.Index)
	delete(next.Index, name)
	next.Index[destination] = slot
	return &next, nil
}
