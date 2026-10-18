package engine

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

type preparedAddedChecks struct {
	old, next *relationDescriptor
	raw       []*parser.CheckConstraint
	scans     []checkConstraint
	ordinal   int
}

// Definition cooking is independent of stage ownership. Admission retains this
// exact result; the sole physical pipeline installs it and scans rows once.
func (e *Engine) prepareAddedChecks(scope *StatementScope, op *Operation, auth AuthorizationView, parent *relationDescriptor, raw []*parser.CheckConstraint) (preparedAddedChecks, error) {
	prepared := preparedAddedChecks{old: parent, next: parent, raw: raw}
	if len(raw) == 0 {
		return prepared, nil
	}
	next := *parent
	next.checkConstraints = slices.Clone(parent.checkConstraints)
	// The ordinary binder writes locality into this graph. A candidate must not
	// mutate captured descriptors, including when a later descendant vetoes it.
	next.inheritance = cloneInheritance(parent.inheritance)
	ordinal := 0
	for _, c := range parent.checkConstraints {
		if c.ordinal >= ordinal {
			ordinal = c.ordinal + 1
		}
	}
	var inherited map[string]checkConstraint
	if scope.inheritanceAlter != nil {
		for _, a := range raw {
			for _, c := range parent.checkConstraints {
				if c.Name != a.Name.Text {
					continue
				}
				merge := scope.inheritanceAlter.descendant
				if parent.inheritance != nil {
					provenance := parent.inheritance.checks[c.Name]
					merge = merge || !provenance.local && provenance.count() != 0
				}
				if !merge {
					continue
				}
				if c.NoInherit {
					return prepared, sqlError("42P17", fmt.Sprintf(`constraint "%s" conflicts with non-inherited constraint on relation "%s"`, c.Name, parent.name))
				}
				if inherited == nil {
					inherited = make(map[string]checkConstraint)
				}
				inherited[c.Name] = c
			}
		}
	}
	if err := e.bindColumnChecks(op, auth, raw, &next, scope.input, true, inherited); err != nil {
		return prepared, err
	}
	if err := scope.inheritanceOnlyCreatedConstraint(parent, &next); err != nil {
		return prepared, err
	}
	if scope.inheritanceAlter != nil && scope.inheritanceAlter.descendant && next.inheritance != nil {
		for _, check := range next.checkConstraints {
			if !slices.ContainsFunc(raw, func(declaration *parser.CheckConstraint) bool { return declaration.Name.Text == check.Name }) {
				continue
			}
			provenance := next.inheritance.checks[check.Name]
			before := parent.inheritance.checks[check.Name]
			retained := slices.ContainsFunc(parent.checkConstraints, func(old checkConstraint) bool { return old.Name == check.Name })
			provenance.local = retained && before.local
			next.inheritance.checks[check.Name] = provenance
		}
	}
	if scope.inheritanceAlter != nil && !scope.inheritanceAlter.descendant && next.inheritance != nil && len(inherited) != 0 {
		graph := cloneInheritance(next.inheritance)
		for name := range inherited {
			provenance := graph.checks[name]
			provenance.local = true
			graph.checks[name] = provenance
		}
		next.inheritance = graph
	}
	for _, c := range orderedChecks(&next) {
		if c.ordinal >= ordinal && !c.NotValid {
			prepared.scans = append(prepared.scans, c)
		}
	}
	prepared.next = &next
	prepared.ordinal = ordinal
	return prepared, nil
}

func (e *Engine) stageAddedChecks(scope *StatementScope, op *Operation, auth AuthorizationView, stage *CatalogStage, parent *relationDescriptor, raw []*parser.CheckConstraint, scans *[]checkConstraint) (*relationDescriptor, error) {
	var prepared preparedAddedChecks
	var err error
	if definition := scope.inheritancePreparedNode(); definition != nil && !definition.phaseZeroOnly {
		prepared = definition.checks
		if prepared.old != parent {
			return nil, ids.ErrCatalogReplaced
		}
		err = definition.checksError
	} else {
		prepared, err = e.prepareAddedChecks(scope, op, auth, parent, raw)
	}
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return parent, nil
	}
	// The cached definition stays immutable even when OIDs/revision are assigned
	// by the later normal pipeline.
	next := *prepared.next
	if err := stage.Replace(&next); err != nil {
		return nil, err
	}
	*scans = append(*scans, prepared.scans...)
	scope.recordInheritanceChecks(parent, &next, raw, prepared.ordinal)
	return &next, nil
}

// Fresh PK preparation observes statement-start NN validity, before DROP/SET
// candidates can remove or validate it. New keys remain with the later binder.
func verifyAlterPrimaryPrerequisites(parent *relationDescriptor, actions []parser.AlterTableAction) error {
	for _, action := range actions {
		if action.Action != parser.AddUnique || !action.Unique.Primary {
			continue
		}
		for _, column := range action.Unique.Columns {
			if slot, exists := parent.index[column.Text]; exists {
				if err := verifyNotNullPKCompatible(parent, parent.attributes[slot:slot+1]); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func verifyNotNullPKCompatible(parent *relationDescriptor, attributes []int32) error {
	for _, n := range parent.notNullConstraints {
		if (!n.NotValid && !n.NoInherit) || !slices.Contains(attributes, n.Attribute) {
			continue
		}
		column := parent.columns[slices.Index(parent.attributes, n.Attribute)].Name
		d := sqlError("55000", fmt.Sprintf(`cannot create primary key on column "%s"`, column)).(*sqlstate.Error)
		modifier := "NOT VALID"
		d.Hint = "You might need to validate it using ALTER TABLE ... VALIDATE CONSTRAINT."
		if n.NoInherit {
			modifier = "NO INHERIT"
			d.Hint = "You might need to make the existing constraint inheritable using ALTER TABLE ... ALTER CONSTRAINT ... INHERIT."
		}
		d.Detail = fmt.Sprintf(`The constraint "%s" on column "%s" of table "%s", marked %s, is incompatible with a primary key.`, n.Name, column, parent.name, modifier)
		return d
	}
	return nil
}

func (e *Engine) stageAddedUnique(op *Operation, auth AuthorizationView, stage *CatalogStage, parent *relationDescriptor, raw *parser.UniqueConstraint) (*relationDescriptor, error) {
	if raw.Primary {
		for _, c := range parent.uniqueConstraints {
			if c.Primary {
				return nil, sqlError("42P16", fmt.Sprintf(`multiple primary keys for table "%s" are not allowed`, parent.name))
			}
		}
	}
	definition, err := bindUniqueAttributes(raw, nil, parent.index, parent.attributes)
	if err != nil {
		return nil, alterCookingError(err)
	}
	if raw.Primary {
		if err := verifyNotNullPKCompatible(parent, definition.Attributes); err != nil {
			return nil, err
		}
		for _, attribute := range definition.Attributes {
			// Raw PRIMARY reuses an existing NN tuple without direct-ADD locality
			// changes. USING INDEX's separate implied ADD NN remains unconditional.
			if slices.ContainsFunc(parent.notNullConstraints, func(constraint notNullConstraint) bool { return constraint.Attribute == attribute }) {
				continue
			}
			column := parent.columns[slices.Index(parent.attributes, attribute)].Name
			parent, err = notNullCandidate(op, e.stageRead(stage), parent, parser.AlterTableAction{Action: parser.AddNotNull, Column: parser.Identifier{Text: column}}, notNullDirect)
			if err != nil {
				return nil, err
			}
		}
	}
	next := *parent
	e.mu.Lock()
	err = e.stageUniqueDefinitions(op, stage, &next, []createUniqueConstraint{{uniqueConstraint: definition}}, true)
	e.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return &next, nil
}

func (e *Engine) stageAddedForeignKey(auth AuthorizationView, stage *CatalogStage, parent *relationDescriptor, raw *parser.ForeignKeyConstraint, target, supporting ids.ObjectAddress) (*relationDescriptor, error) {
	next := *parent
	next.foreignKeys = slices.Clone(parent.foreignKeys)
	if err := stage.Replace(&next); err != nil {
		return nil, err
	}
	e.mu.Lock()
	err := e.stageForeignKey(auth, stage, &next, *raw, target, supporting)
	e.mu.Unlock()
	if err != nil {
		return nil, alterCookingError(err)
	}
	return &next, nil
}
