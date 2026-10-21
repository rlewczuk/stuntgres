package engine

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// CaptureRestoredNotNulls records, in origins, the explicitly named, valid,
// purely local NOT NULL constraints raw declared on the relation Restore just
// created as created; a later SET NOT NULL repair pairs with them.
func CaptureRestoredNotNulls(view catalog.Read, created ids.ObjectRef, raw *parser.CreateTable, origins map[ids.OID]table.RestoredNotNull) {
	relation, ok := view.Relation(created.Address)
	if !ok {
		return
	}
	capture := func(column, name string) {
		if name == "" {
			return
		}
		slot, ok := relation.Index[column]
		if !ok {
			return
		}
		attribute := relation.Attributes[slot]
		for _, constraint := range relation.Constraints.NotNull {
			if constraint.Name == name && constraint.Attribute == attribute && !constraint.NotValid {
				if graph := relation.Inheritance; graph != nil {
					fact := graph.NotNull[attribute]
					if !fact.Local || fact.Count() != 0 {
						return
					}
				}
				origins[constraint.OID] = table.RestoredNotNull{Ref: created, Attribute: attribute, Constraint: constraint.OID}
				return
			}
		}
	}
	for _, element := range raw.Elements {
		if element.Column != nil {
			for _, constraint := range element.Column.Constraints {
				if constraint.NotNull {
					capture(element.Column.Name, constraint.Name.Text)
				}
			}
		}
		if element.NotNull != nil {
			capture(element.NotNull.Column.Text, element.NotNull.Name.Text)
		}
	}
}

// RestoredNotNullShape reports whether raw has the one shape a paired NOT
// NULL takes: ALTER TABLE ONLY schema.table ALTER COLUMN ... SET NOT NULL.
func RestoredNotNullShape(raw *parser.AlterTable) bool {
	return raw.Only && !raw.IfExists && len(raw.Name.Parts) == 2 && len(raw.Actions) == 1 && raw.Actions[0].Action == parser.SetNotNull
}

// PairRestoredNotNull returns the captured origin of the valid NOT NULL the
// SET NOT NULL of raw names on ref.
func PairRestoredNotNull(view catalog.Read, ref ids.ObjectRef, raw *parser.AlterTable, origins map[ids.OID]table.RestoredNotNull) (table.RestoredNotNull, error) {
	relation, ok := view.Relation(ref.Address)
	if !ok {
		return table.RestoredNotNull{}, sqlError("0A000", table.RestoreNoInheritBoundary)
	}
	slot, ok := relation.Index[raw.Actions[0].Column.Text]
	if !ok {
		return table.RestoredNotNull{}, sqlError("0A000", table.RestoreNoInheritBoundary)
	}
	for _, constraint := range relation.Constraints.NotNull {
		if constraint.Attribute != relation.Attributes[slot] || constraint.NotValid {
			continue
		}
		origin, exists := origins[constraint.OID]
		if exists && origin.Ref == ref && origin.Attribute == constraint.Attribute {
			return origin, nil
		}
	}
	return table.RestoredNotNull{}, sqlError("0A000", table.RestoreNoInheritBoundary)
}

// NoInheritNotNullCandidate returns ref's relation with the paired NOT NULL
// origin of column, named label, marked NO INHERIT; nil when it already is.
// The constraint must still be the captured, valid, purely local one, and no
// primary key may cover its column.
func NoInheritNotNullCandidate(view catalog.Read, ref ids.ObjectRef, origin table.RestoredNotNull, column, label string) (*catalog.Relation, error) {
	reject := func() (*catalog.Relation, error) { return nil, sqlError("0A000", table.RestoreNoInheritBoundary) }
	old, ok := view.Relation(ref.Address)
	if !ok || old.RelationKind != catalog.TableRelation || origin.Ref != ref {
		return reject()
	}
	slot, ok := old.Index[column]
	if !ok || old.Attributes[slot] != origin.Attribute || old.Columns[slot].Nullable {
		return reject()
	}
	index := slices.IndexFunc(old.Constraints.NotNull, func(c catalog.NotNullConstraint) bool {
		return c.OID == origin.Constraint && c.Name == label && c.Attribute == origin.Attribute && !c.NotValid
	})
	if index < 0 {
		return reject()
	}
	for _, constraint := range old.Constraints.Unique {
		if constraint.Primary && slices.Contains(constraint.Attributes, origin.Attribute) {
			return reject()
		}
	}
	if graph := old.Inheritance; graph != nil {
		fact := graph.NotNull[origin.Attribute]
		if !fact.Local || fact.Count() != 0 {
			return reject()
		}
	}
	if old.Constraints.NotNull[index].NoInherit {
		return nil, nil
	}
	if old.Revision() == ids.DefinitionRevision(^uint64(0)) {
		return nil, fmt.Errorf("stuntgres: definition revision exhausted")
	}
	next := *old
	next.ObjectRevision++
	next.Constraints.NotNull = slices.Clone(old.Constraints.NotNull)
	next.Constraints.NotNull[index].NoInherit = true
	return &next, nil
}
