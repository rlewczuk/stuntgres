package engine

import (
	"fmt"
	"slices"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func renameLocalConstraint(parent *catalog.Relation, old, destination string, kind byte) (*catalog.Relation, error) {
	collision := func(name string) error {
		if name == destination {
			return sqlError("42710", fmt.Sprintf(`constraint "%s" for relation "%s" already exists`, destination, parent.Name()))
		}
		return nil
	}
	for _, c := range parent.Constraints.NotNull {
		if err := collision(c.Name); err != nil {
			return nil, err
		}
	}
	for _, c := range parent.Constraints.Check {
		if err := collision(c.Name); err != nil {
			return nil, err
		}
	}
	for _, c := range parent.Constraints.Unique {
		if err := collision(c.Name); err != nil {
			return nil, err
		}
	}
	for _, c := range parent.Constraints.Exclusion {
		if err := collision(c.Name); err != nil {
			return nil, err
		}
	}
	for _, c := range parent.Constraints.ForeignKeys {
		if err := collision(c.Name); err != nil {
			return nil, err
		}
	}
	if parent.Revision() == ids.DefinitionRevision(^uint64(0)) {
		return nil, fmt.Errorf("stuntgres: definition revision exhausted")
	}
	next := *parent
	next.ObjectRevision++
	if kind == 'n' {
		next.Constraints.NotNull = slices.Clone(parent.Constraints.NotNull)
		for i := range next.Constraints.NotNull {
			if next.Constraints.NotNull[i].Name == old {
				next.Constraints.NotNull[i].Name = destination
				return &next, nil
			}
		}
	} else if kind == 'f' {
		next.Constraints.ForeignKeys = slices.Clone(parent.Constraints.ForeignKeys)
		for i := range next.Constraints.ForeignKeys {
			if next.Constraints.ForeignKeys[i].Name == old {
				next.Constraints.ForeignKeys[i].Name = destination
				return &next, nil
			}
		}
	} else {
		next.Constraints.Check = slices.Clone(parent.Constraints.Check)
		for i := range next.Constraints.Check {
			if next.Constraints.Check[i].Name == old {
				next.Constraints.Check[i].Name = destination
				slices.SortFunc(next.Constraints.Check, func(a, b catalog.CheckConstraint) int { return strings.Compare(a.Name, b.Name) })
				return &next, nil
			}
		}
	}
	return nil, ids.ErrCatalogReplaced
}
