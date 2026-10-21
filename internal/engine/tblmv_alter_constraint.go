package engine

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

type preparedAddedChecks struct {
	old, next *catalog.Relation
	raw       []*parser.CheckConstraint
	scans     []catalog.CheckConstraint
	ordinal   int
}

// Fresh PK preparation observes statement-start NN validity, before DROP/SET
// candidates can remove or validate it. New keys remain with the later binder.
func verifyAlterPrimaryPrerequisites(parent *catalog.Relation, actions []parser.AlterTableAction) error {
	for _, action := range actions {
		if action.Action != parser.AddUnique || !action.Unique.Primary {
			continue
		}
		for _, column := range action.Unique.Columns {
			if slot, exists := parent.Index[column.Text]; exists {
				if err := verifyNotNullPKCompatible(parent, parent.Attributes[slot:slot+1]); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func verifyNotNullPKCompatible(parent *catalog.Relation, attributes []int32) error {
	for _, n := range parent.Constraints.NotNull {
		if (!n.NotValid && !n.NoInherit) || !slices.Contains(attributes, n.Attribute) {
			continue
		}
		column := parent.Columns[slices.Index(parent.Attributes, n.Attribute)].Name
		d := sqlError("55000", fmt.Sprintf(`cannot create primary key on column "%s"`, column)).(*sqlstate.Error)
		modifier := "NOT VALID"
		d.Hint = "You might need to validate it using ALTER TABLE ... VALIDATE CONSTRAINT."
		if n.NoInherit {
			modifier = "NO INHERIT"
			d.Hint = "You might need to make the existing constraint inheritable using ALTER TABLE ... ALTER CONSTRAINT ... INHERIT."
		}
		d.Detail = fmt.Sprintf(`The constraint "%s" on column "%s" of table "%s", marked %s, is incompatible with a primary key.`, n.Name, column, parent.Name(), modifier)
		return d
	}
	return nil
}
