package engine

import (
	"fmt"
	"slices"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

type notNullRecursion uint8

const (
	notNullDirect notNullRecursion = iota
	notNullInherited
	notNullValidation
)

func notNullCandidate(op *Operation, catalog CatalogRead, old *relationDescriptor, a parser.AlterTableAction, recursion notNullRecursion) (*relationDescriptor, error) {
	slot, index := -1, -1
	if a.Action == parser.DropConstraint {
		for i, c := range old.notNullConstraints {
			if c.Name == a.Constraint.Text {
				index = i
				for j, attr := range old.attributes {
					if attr == c.Attribute {
						slot = j
						break
					}
				}
				break
			}
		}
		for _, c := range old.checkConstraints {
			if c.Name == a.Constraint.Text {
				next := *old
				next.checkConstraints = make([]checkConstraint, 0, len(old.checkConstraints)-1)
				for _, constraint := range old.checkConstraints {
					if constraint.Name != a.Constraint.Text {
						next.checkConstraints = append(next.checkConstraints, constraint)
					}
				}
				return &next, nil
			}
		}
		for i, c := range old.uniqueConstraints {
			if c.Name == a.Constraint.Text {
				if c.Parent != 0 {
					return nil, sqlError("42P16", fmt.Sprintf(`cannot drop inherited constraint "%s" of relation "%s"`, c.Name, old.name))
				}
				index, exists := catalog.Relation(c.Index)
				if !exists {
					return nil, ErrCatalogReplaced
				}
				if err := partitionIndexDropDependency(catalog, index); err != nil {
					return nil, err
				}
				next := *old
				next.uniqueConstraints = make([]uniqueConstraint, 0, len(old.uniqueConstraints)-1)
				next.uniqueConstraints = append(next.uniqueConstraints, old.uniqueConstraints[:i]...)
				next.uniqueConstraints = append(next.uniqueConstraints, old.uniqueConstraints[i+1:]...)
				transitionPhysicalNotNull(old, &next)
				return &next, nil
			}
		}
		for i, c := range old.exclusionConstraints {
			if c.Name == a.Constraint.Text {
				next := *old
				next.exclusionConstraints = make([]exclusionConstraint, 0, len(old.exclusionConstraints)-1)
				next.exclusionConstraints = append(next.exclusionConstraints, old.exclusionConstraints[:i]...)
				next.exclusionConstraints = append(next.exclusionConstraints, old.exclusionConstraints[i+1:]...)
				return &next, nil
			}
		}
		for i, c := range old.foreignKeys {
			if c.Name == a.Constraint.Text {
				next := *old
				next.foreignKeys = make([]foreignKeyConstraint, 0, len(old.foreignKeys)-1)
				next.foreignKeys = append(next.foreignKeys, old.foreignKeys[:i]...)
				next.foreignKeys = append(next.foreignKeys, old.foreignKeys[i+1:]...)
				return &next, nil
			}
		}
		if slot < 0 {
			if !a.IfExists {
				return nil, sqlError("42704", fmt.Sprintf(`constraint "%s" of relation "%s" does not exist`, a.Constraint.Text, old.name))
			}
			op.emitNotice(levelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`constraint "%s" of relation "%s" does not exist, skipping`, a.Constraint.Text, old.name)})
			return old, nil
		}
	} else {
		if isSystemColumn(a.Column.Text) {
			message := "system-column nullability is unsupported"
			if a.Action == parser.AddNotNull {
				message = fmt.Sprintf(`cannot add not-null constraint on system column "%s"`, a.Column.Text)
			} else if a.Action == parser.DropNotNull {
				message = fmt.Sprintf(`cannot alter system column "%s"`, a.Column.Text)
			}
			return nil, sqlError("0A000", message)
		}
		var ok bool
		slot, ok = old.index[a.Column.Text]
		if !ok {
			return nil, sqlError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, a.Column.Text, old.name))
		}
		for i, c := range old.notNullConstraints {
			if c.Attribute == old.attributes[slot] {
				index = i
				break
			}
		}
	}
	adding := a.Action == parser.SetNotNull || a.Action == parser.AddNotNull
	if a.Action == parser.DropNotNull && index < 0 {
		if _, physical := old.physicalNotNull[old.attributes[slot]]; physical {
			return nil, sqlError("XX000", fmt.Sprintf(`cache lookup failed for not-null constraint on column "%s" of relation "%s"`, old.columns[slot].Name, old.name))
		}
	}
	if !adding {
		_, identity := old.identities[old.attributes[slot]]
		// ALTER COLUMN checks identity before entering constraint removal.
		// Named DROP reaches the primary-key protection first instead.
		if identity && a.Action != parser.DropConstraint {
			return nil, sqlError("42601", fmt.Sprintf(`column "%s" of relation "%s" is an identity column`, old.columns[slot].Name, old.name))
		}
		for _, constraint := range old.uniqueConstraints {
			if constraint.Primary && slices.Contains(constraint.Attributes, old.attributes[slot]) {
				return nil, sqlError("42P16", fmt.Sprintf(`column "%s" is in a primary key`, old.columns[slot].Name))
			}
		}
		if identity {
			return nil, sqlError("42601", fmt.Sprintf(`column "%s" of relation "%s" is an identity column`, old.columns[slot].Name, old.name))
		}
	}
	if adding && index >= 0 {
		if a.Action == parser.AddNotNull {
			constraint := old.notNullConstraints[index]
			if constraint.NoInherit != a.NoInherit {
				diagnostic := sqlError("55000", fmt.Sprintf(`cannot change NO INHERIT status of NOT NULL constraint "%s" on relation "%s"`, constraint.Name, old.name)).(*sqlstate.Error)
				diagnostic.Hint = "You might need to make the existing constraint inheritable using ALTER TABLE ... ALTER CONSTRAINT ... INHERIT."
				return nil, diagnostic
			}
			if constraint.NotValid && !a.NotValid {
				return nil, incompatibleNotValid(constraint.Name, old.name)
			}
			if recursion == notNullDirect && old.inheritance != nil && !old.inheritance.notNull[constraint.Attribute].local {
				next := *old
				next.inheritance = cloneInheritance(old.inheritance)
				provenance := next.inheritance.notNull[constraint.Attribute]
				provenance.local = true
				next.inheritance.notNull[constraint.Attribute] = provenance
				return &next, nil
			}
		}
		if a.Action == parser.SetNotNull {
			constraint := old.notNullConstraints[index]
			if constraint.NoInherit && !a.NoInherit {
				return nil, sqlError("0A000", fmt.Sprintf(`cannot change NO INHERIT status of NOT NULL constraint "%s" on relation "%s"`, constraint.Name, old.name))
			}
			// The first direct SET localizes an inherited fact; it does not
			// validate an inherited NOT VALID fact or visit its descendants.
			if recursion == notNullInherited {
				return old, nil
			}
			if recursion == notNullDirect && old.inheritance != nil && !old.inheritance.notNull[constraint.Attribute].local {
				next := *old
				next.inheritance = cloneInheritance(old.inheritance)
				provenance := next.inheritance.notNull[constraint.Attribute]
				provenance.local = true
				next.inheritance.notNull[constraint.Attribute] = provenance
				return &next, nil
			}
			if constraint.NotValid && a.NoInherit && !constraint.NoInherit && len(inheritanceChildren(catalog, old.address)) != 0 {
				return nil, sqlError("42P16", "constraint must be validated on child tables too")
			}
		}
		if !old.notNullConstraints[index].NotValid || a.NotValid {
			return old, nil
		}
		if a.Action == parser.AddNotNull {
			return nil, incompatibleNotValid(old.notNullConstraints[index].Name, old.name)
		}
		next := *old
		next.notNullConstraints = slices.Clone(old.notNullConstraints)
		next.notNullConstraints[index].NotValid = false
		return &next, nil
	}
	if !adding && index < 0 {
		return old, nil
	}
	name := a.Constraint.Text
	if adding {
		if name == "" {
			name = generatedConstraintName(old.name, old.columns[slot].Name, "not_null", constraintNames(catalog, old.namespace, old, op.definition))
		}
		if relationConstraintNameExists(old, name) {
			return nil, sqlError("42710", fmt.Sprintf(`constraint "%s" for relation "%s" already exists`, name, old.name))
		}
	}
	next := *old
	next.columns = append([]ColumnDescriptor(nil), old.columns...)
	next.columns[slot].Nullable = !adding
	if adding {
		next.notNullConstraints = make([]notNullConstraint, len(old.notNullConstraints)+1)
		copy(next.notNullConstraints, old.notNullConstraints)
		next.notNullConstraints[len(old.notNullConstraints)] = notNullConstraint{Name: name, Attribute: old.attributes[slot], NotValid: a.NotValid, NoInherit: a.NoInherit}
	} else {
		next.notNullConstraints = make([]notNullConstraint, 0, len(old.notNullConstraints)-1)
		next.notNullConstraints = append(next.notNullConstraints, old.notNullConstraints[:index]...)
		next.notNullConstraints = append(next.notNullConstraints, old.notNullConstraints[index+1:]...)
	}
	transitionPhysicalNotNull(old, &next)
	return &next, nil
}

func incompatibleNotValid(name, table string) error {
	diagnostic := sqlError("55000", fmt.Sprintf(`incompatible NOT VALID constraint "%s" on relation "%s"`, name, table)).(*sqlstate.Error)
	diagnostic.Hint = "You might need to validate it using ALTER TABLE ... VALIDATE CONSTRAINT."
	return diagnostic
}
