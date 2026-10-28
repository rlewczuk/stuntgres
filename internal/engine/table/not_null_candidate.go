package table

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

type NotNullRecursion uint8

const (
	NotNullDirect NotNullRecursion = iota
	NotNullInherited
	NotNullValidation
)

// selectNotNullConstraint is the pure fact lookup shared by candidate cooking
// and name projection. It selects by stable attribute, or by the explicit name
// for DROP CONSTRAINT. Absence is not a synthesized fact or a removal.
// Legality checks, diagnostics and notices belong to NotNullCandidate.
func selectNotNullConstraint(relation *catalog.Relation, action parser.AlterTableAction) (slot, fact int) {
	if action.Action == parser.DropConstraint {
		for i, constraint := range relation.Constraints.NotNull {
			if constraint.Name == action.Constraint.Text {
				return relation.IndexForAttribute(constraint.Attribute), i
			}
		}
		return -1, -1
	}
	slot, ok := relation.Index[action.Column.Text]
	if !ok {
		return -1, -1
	}
	for i, constraint := range relation.Constraints.NotNull {
		if constraint.Attribute == relation.Attributes[slot] {
			return slot, i
		}
	}
	return slot, -1
}

func NotNullCandidate(op *session.Operation, view catalog.Read, old *catalog.Relation, a parser.AlterTableAction, recursion NotNullRecursion) (*catalog.Relation, error) {
	slot, idx := selectNotNullConstraint(old, a)
	if a.Action == parser.DropConstraint {
		for _, c := range old.Constraints.Check {
			if c.Name == a.Constraint.Text {
				next := *old
				next.Constraints.Check = make([]catalog.CheckConstraint, 0, len(old.Constraints.Check)-1)
				for _, constraint := range old.Constraints.Check {
					if constraint.Name != a.Constraint.Text {
						next.Constraints.Check = append(next.Constraints.Check, constraint)
					}
				}
				return &next, nil
			}
		}
		for i, c := range old.Constraints.Unique {
			if c.Name == a.Constraint.Text {
				if c.Parent != 0 {
					return nil, sqlError("42P16", fmt.Sprintf(`cannot drop inherited constraint "%s" of relation "%s"`, c.Name, old.Name()))
				}
				idx, exists := view.Relation(c.Index)
				if !exists {
					return nil, ids.ErrCatalogReplaced
				}
				if err := index.PartitionIndexDropDependency(view, idx); err != nil {
					return nil, err
				}
				next := *old
				next.Constraints.Unique = make([]catalog.UniqueConstraint, 0, len(old.Constraints.Unique)-1)
				next.Constraints.Unique = append(next.Constraints.Unique, old.Constraints.Unique[:i]...)
				next.Constraints.Unique = append(next.Constraints.Unique, old.Constraints.Unique[i+1:]...)
				TransitionPhysicalNotNull(old, &next)
				return &next, nil
			}
		}
		for i, c := range old.Constraints.Exclusion {
			if c.Name == a.Constraint.Text {
				next := *old
				next.Constraints.Exclusion = make([]catalog.ExclusionConstraint, 0, len(old.Constraints.Exclusion)-1)
				next.Constraints.Exclusion = append(next.Constraints.Exclusion, old.Constraints.Exclusion[:i]...)
				next.Constraints.Exclusion = append(next.Constraints.Exclusion, old.Constraints.Exclusion[i+1:]...)
				return &next, nil
			}
		}
		for i, c := range old.Constraints.ForeignKeys {
			if c.Name == a.Constraint.Text {
				next := *old
				next.Constraints.ForeignKeys = make([]catalog.ForeignKeyConstraint, 0, len(old.Constraints.ForeignKeys)-1)
				next.Constraints.ForeignKeys = append(next.Constraints.ForeignKeys, old.Constraints.ForeignKeys[:i]...)
				next.Constraints.ForeignKeys = append(next.Constraints.ForeignKeys, old.Constraints.ForeignKeys[i+1:]...)
				return &next, nil
			}
		}
		if slot < 0 {
			if !a.IfExists {
				return nil, sqlError("42704", fmt.Sprintf(`constraint "%s" of relation "%s" does not exist`, a.Constraint.Text, old.Name()))
			}
			op.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`constraint "%s" of relation "%s" does not exist, skipping`, a.Constraint.Text, old.Name())})
			return old, nil
		}
	} else {
		if catalog.IsSystemColumn(a.Column.Text) {
			message := "system-column nullability is unsupported"
			if a.Action == parser.AddNotNull {
				message = fmt.Sprintf(`cannot add not-null constraint on system column "%s"`, a.Column.Text)
			} else if a.Action == parser.DropNotNull {
				message = fmt.Sprintf(`cannot alter system column "%s"`, a.Column.Text)
			}
			return nil, sqlError("0A000", message)
		}
		if slot < 0 {
			return nil, sqlError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, a.Column.Text, old.Name()))
		}
	}
	adding := a.Action == parser.SetNotNull || a.Action == parser.AddNotNull
	if a.Action == parser.DropNotNull && idx < 0 {
		if _, physical := old.Constraints.PhysicalNotNull[old.Attributes[slot]]; physical {
			return nil, sqlError("XX000", fmt.Sprintf(`cache lookup failed for not-null constraint on column "%s" of relation "%s"`, old.Columns[slot].Name, old.Name()))
		}
	}
	if !adding {
		_, identity := old.ColumnMeta.Identities[old.Attributes[slot]]
		// ALTER COLUMN checks identity before entering constraint removal.
		// Named DROP reaches the primary-key protection first instead.
		if identity && a.Action != parser.DropConstraint {
			return nil, sqlError("42601", fmt.Sprintf(`column "%s" of relation "%s" is an identity column`, old.Columns[slot].Name, old.Name()))
		}
		for _, constraint := range old.Constraints.Unique {
			if constraint.Primary && slices.Contains(constraint.Attributes, old.Attributes[slot]) {
				return nil, sqlError("42P16", fmt.Sprintf(`column "%s" is in a primary key`, old.Columns[slot].Name))
			}
		}
		if identity {
			return nil, sqlError("42601", fmt.Sprintf(`column "%s" of relation "%s" is an identity column`, old.Columns[slot].Name, old.Name()))
		}
	}
	if adding && idx >= 0 {
		if a.Action == parser.AddNotNull {
			constraint := old.Constraints.NotNull[idx]
			if constraint.NoInherit != a.NoInherit {
				diagnostic := sqlError("55000", fmt.Sprintf(`cannot change NO INHERIT status of NOT NULL constraint "%s" on relation "%s"`, constraint.Name, old.Name())).(*sqlstate.Error)
				diagnostic.Hint = "You might need to make the existing constraint inheritable using ALTER TABLE ... ALTER CONSTRAINT ... INHERIT."
				return nil, diagnostic
			}
			if constraint.NotValid && !a.NotValid {
				return nil, index.IncompatibleNotValid(constraint.Name, old.Name())
			}
			if recursion == NotNullDirect && a.Constraint.Text != "" && a.Constraint.Text != constraint.Name {
				diagnostic := sqlError("55000", fmt.Sprintf(`cannot create not-null constraint "%s" on column "%s" of table "%s"`, a.Constraint.Text, old.Columns[slot].Name, old.Name())).(*sqlstate.Error)
				diagnostic.Detail = fmt.Sprintf(`A not-null constraint named "%s" already exists for this column.`, constraint.Name)
				return nil, diagnostic
			}
			if recursion == NotNullDirect && constraint.PartitionOrigin != nil && !constraint.PartitionOrigin.Local {
				next := *old
				next.Constraints.NotNull = slices.Clone(old.Constraints.NotNull)
				next.Constraints.NotNull[idx].PartitionOrigin = catalog.CloneConstraintOrigin(constraint.PartitionOrigin)
				next.Constraints.NotNull[idx].PartitionOrigin.Local = true
				return &next, nil
			}
			if recursion == NotNullDirect && old.Inheritance != nil && !old.Inheritance.NotNull[constraint.Attribute].Local {
				next := *old
				next.Inheritance = catalog.CloneInheritance(old.Inheritance)
				provenance := next.Inheritance.NotNull[constraint.Attribute]
				provenance.Local = true
				next.Inheritance.NotNull[constraint.Attribute] = provenance
				return &next, nil
			}
		}
		if a.Action == parser.SetNotNull {
			constraint := old.Constraints.NotNull[idx]
			if constraint.NoInherit && !a.NoInherit {
				return nil, sqlError("0A000", fmt.Sprintf(`cannot change NO INHERIT status of NOT NULL constraint "%s" on relation "%s"`, constraint.Name, old.Name()))
			}
			// The first direct SET localizes an inherited fact; it does not
			// validate an inherited NOT VALID fact or visit its descendants.
			if recursion == NotNullInherited {
				return old, nil
			}
			if recursion == NotNullDirect && constraint.PartitionOrigin != nil && !constraint.PartitionOrigin.Local {
				next := *old
				next.Constraints.NotNull = slices.Clone(old.Constraints.NotNull)
				next.Constraints.NotNull[idx].PartitionOrigin = catalog.CloneConstraintOrigin(constraint.PartitionOrigin)
				next.Constraints.NotNull[idx].PartitionOrigin.Local = true
				return &next, nil
			}
			if recursion == NotNullDirect && old.Inheritance != nil && !old.Inheritance.NotNull[constraint.Attribute].Local {
				next := *old
				next.Inheritance = catalog.CloneInheritance(old.Inheritance)
				provenance := next.Inheritance.NotNull[constraint.Attribute]
				provenance.Local = true
				next.Inheritance.NotNull[constraint.Attribute] = provenance
				return &next, nil
			}
			if constraint.NotValid && a.NoInherit && !constraint.NoInherit && len(catalog.InheritanceChildren(view, old.Address())) != 0 {
				return nil, sqlError("42P16", "constraint must be validated on child tables too")
			}
		}
		if !old.Constraints.NotNull[idx].NotValid || a.NotValid {
			return old, nil
		}
		if a.Action == parser.AddNotNull {
			return nil, index.IncompatibleNotValid(old.Constraints.NotNull[idx].Name, old.Name())
		}
		next := *old
		next.Constraints.NotNull = slices.Clone(old.Constraints.NotNull)
		next.Constraints.NotNull[idx].NotValid = false
		return &next, nil
	}
	if !adding && idx < 0 {
		return old, nil
	}
	name := a.Constraint.Text
	if adding {
		if name == "" {
			name = catalog.GeneratedConstraintName(old.Name(), old.Columns[slot].Name, "not_null", catalog.ConstraintNames(view, old.Namespace(), old, op.Definition))
		}
		if catalog.RelationConstraintNameExists(old, name) {
			return nil, sqlError("42710", fmt.Sprintf(`constraint "%s" for relation "%s" already exists`, name, old.Name()))
		}
	}
	next := *old
	next.Columns = append([]catalog.Column(nil), old.Columns...)
	next.Columns[slot].Nullable = !adding
	if adding {
		next.Constraints.NotNull = make([]catalog.NotNullConstraint, len(old.Constraints.NotNull)+1)
		copy(next.Constraints.NotNull, old.Constraints.NotNull)
		next.Constraints.NotNull[len(old.Constraints.NotNull)] = catalog.NotNullConstraint{Name: name, Attribute: old.Attributes[slot], NotValid: a.NotValid, NoInherit: a.NoInherit}
	} else {
		next.Constraints.NotNull = make([]catalog.NotNullConstraint, 0, len(old.Constraints.NotNull)-1)
		next.Constraints.NotNull = append(next.Constraints.NotNull, old.Constraints.NotNull[:idx]...)
		next.Constraints.NotNull = append(next.Constraints.NotNull, old.Constraints.NotNull[idx+1:]...)
	}
	TransitionPhysicalNotNull(old, &next)
	return &next, nil
}

// This is the incumbent USING INDEX NN constructor, shared by admission and
// ordinary staging. Only actual eligible KEY attributes participate; INCLUDE
// attributes and the physical index/constraint are never propagated.
func UsingIndexNotNullCandidate(op *session.Operation, view catalog.Read, reader catalog.DefinitionReader, parent *catalog.Relation, captured index.UsingIndexAttachment) (*catalog.Relation, error) {
	if !captured.Syntax.Primary {
		return parent, nil
	}
	previous := op.Definition
	op.Definition = reader
	defer func() { op.Definition = previous }()
	next := parent
	for _, attribute := range captured.Attributes {
		slot := next.IndexForAttribute(attribute)
		if slot < 0 {
			return nil, ids.ErrCatalogReplaced
		}
		action := parser.AlterTableAction{Action: parser.AddNotNull, Column: parser.Identifier{Text: next.Columns[slot].Name}}
		var err error
		next, err = NotNullCandidate(op, view, next, action, NotNullDirect)
		if err != nil {
			return nil, err
		}
	}
	return next, nil
}
