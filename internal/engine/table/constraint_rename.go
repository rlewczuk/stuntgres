package table

import (
	"fmt"
	"slices"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func RenameLocalConstraint(parent *catalog.Relation, old, destination string, kind byte) (*catalog.Relation, error) {
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

// RenameConstraintKind is the kind of parent's constraint name ('n', 'c', 'u',
// 'x' or 'f') and, for UNIQUE and EXCLUDE, its backing index. PostgreSQL
// accepts view and index targets here, then performs constraint lookup.
func RenameConstraintKind(parent *catalog.Relation, name string) (byte, ids.ObjectAddress, ids.ObjectAddress, error) {
	if parent.RelationKind == catalog.SequenceRelation {
		return 0, ids.ObjectAddress{}, ids.ObjectAddress{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`cannot rename columns of relation "%s"`, parent.Name()), Detail: "This operation is not supported for sequences."}
	}
	var backing ids.ObjectAddress
	kind := byte(0)
	constraint := ids.ObjectAddress{ClassID: 2606}
	for _, c := range parent.Constraints.NotNull {
		if c.Name == name {
			kind = 'n'
			constraint.ObjectID = c.OID
			break
		}
	}
	for _, c := range parent.Constraints.Check {
		if c.Name == name {
			kind = 'c'
			constraint.ObjectID = c.OID
			break
		}
	}
	for _, c := range parent.Constraints.Unique {
		if c.Name == name {
			kind = 'u'
			constraint.ObjectID = c.OID
			backing = c.Index
			break
		}
	}
	for _, c := range parent.Constraints.Exclusion {
		if c.Name == name {
			kind = 'x'
			constraint.ObjectID = c.OID
			backing = c.Index
			break
		}
	}
	for _, c := range parent.Constraints.ForeignKeys {
		if c.Name == name {
			kind = 'f'
			constraint.ObjectID = c.OID
			break
		}
	}
	if kind == 0 {
		return 0, ids.ObjectAddress{}, ids.ObjectAddress{}, sqlError("42704", fmt.Sprintf(`constraint "%s" for table "%s" does not exist`, name, parent.Name()))
	}
	return kind, backing, constraint, nil
}

// StageRenameConstraint stages RENAME CONSTRAINT of a local constraint of
// parent, or of the index backing a UNIQUE or EXCLUDE constraint.
func StageRenameConstraint(stage index.DescriptorStage, view catalog.Read, parent *catalog.Relation, raw *parser.AlterTableRenameConstraint, kind byte, backing ids.ObjectAddress) error {
	if backing != (ids.ObjectAddress{}) {
		idx, ok := view.Relation(backing)
		if !ok {
			return ids.ErrCatalogReplaced
		}
		if _, exists := view.LookupName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: parent.Namespace(), Name: raw.NewName.Text}); exists {
			return sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, raw.NewName.Text))
		}
		if idx.Revision() == ids.DefinitionRevision(^uint64(0)) {
			return fmt.Errorf("stuntgres: definition revision exhausted")
		}
		next := *idx
		next.ObjectName = raw.NewName.Text
		next.ObjectRevision++
		if err := index.StageBackingIndexRename(stage, &next); err != nil {
			return err
		}
		return stage.Replace(&next)
	}
	next, err := RenameLocalConstraint(parent, raw.Constraint.Text, raw.NewName.Text, kind)
	if err != nil {
		return err
	}
	return stage.Replace(next)
}
