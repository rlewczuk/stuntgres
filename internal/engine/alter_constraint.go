package engine

import (
	"fmt"
	"slices"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func (e *Engine) stageAddedChecks(scope *StatementScope, op *Operation, auth AuthorizationView, stage *CatalogStage, parent *relationDescriptor, raw []*parser.CheckConstraint, scans *[]checkConstraint) (*relationDescriptor, error) {
	if len(raw) == 0 {
		return parent, nil
	}
	next := *parent
	next.checkConstraints = slices.Clone(parent.checkConstraints)
	ordinal := 0
	for _, c := range parent.checkConstraints {
		if c.ordinal >= ordinal {
			ordinal = c.ordinal + 1
		}
	}
	if err := e.bindColumnChecks(op, auth, raw, &next, scope.input, true, nil); err != nil {
		return nil, err
	}
	for _, c := range orderedChecks(&next) {
		if c.ordinal >= ordinal && !c.NotValid {
			*scans = append(*scans, c)
		}
	}
	if err := stage.Replace(&next); err != nil {
		return nil, err
	}
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
		if !n.NotValid || !slices.Contains(attributes, n.Attribute) {
			continue
		}
		column := parent.columns[slices.Index(parent.attributes, n.Attribute)].Name
		d := sqlError("55000", fmt.Sprintf(`cannot create primary key on column "%s"`, column)).(*sqlstate.Error)
		d.Detail = fmt.Sprintf(`The constraint "%s" on column "%s" of table "%s", marked NOT VALID, is incompatible with a primary key.`, n.Name, column, parent.name)
		d.Hint = "You might need to validate it using ALTER TABLE ... VALIDATE CONSTRAINT."
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
			column := parent.columns[slices.Index(parent.attributes, attribute)].Name
			parent, err = notNullCandidate(op, e.stageRead(stage), parent, parser.AlterTableAction{Action: parser.AddNotNull, Column: parser.Identifier{Text: column}})
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

func (e *Engine) stageAddedForeignKey(auth AuthorizationView, stage *CatalogStage, parent *relationDescriptor, raw *parser.ForeignKeyConstraint, target, supporting ObjectAddress) (*relationDescriptor, error) {
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
