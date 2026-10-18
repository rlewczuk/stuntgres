package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
)

// Force flags are COPY-field ordinals. Relation targets retain physical indexes
// for input/default assembly; query targets are its returned schema and aliases.
func bindCopyForce(force copyForce, name string, relation *catalog.Relation, targets []int, schema []ColumnDescriptor) ([]bool, error) {
	if !force.specified {
		return nil, nil
	}
	count := len(schema)
	if relation != nil {
		count = len(targets)
	}
	flags := make([]bool, count)
	if force.all {
		for i := range flags {
			flags[i] = true
		}
		return flags, nil
	}
	if relation != nil {
		columns, err := copyTargets(relation, force.columns)
		if err != nil {
			return nil, err
		}
		for n, target := range columns {
			found := false
			for i, selected := range targets {
				if selected == target {
					flags[i] = true
					found = true
					break
				}
			}
			if !found {
				return nil, sqlError("42P10", fmt.Sprintf("%s column \"%s\" not referenced by COPY", name, force.columns[n].Text))
			}
		}
	} else {
		for n, column := range force.columns {
			for _, prior := range force.columns[:n] {
				if prior.Text == column.Text {
					return nil, sqlError("42701", fmt.Sprintf("column \"%s\" specified more than once", column.Text))
				}
			}
			found := false
			for i, selected := range schema {
				if selected.Name == column.Text {
					flags[i] = true
					found = true
					break
				}
			}
			if !found {
				return nil, sqlError("42703", fmt.Sprintf("column \"%s\" does not exist", column.Text))
			}
		}
	}
	return flags, nil
}
func (o *copyOptions) bindForces(relation *catalog.Relation, targets []int, schema []ColumnDescriptor) (err error) {
	if o.quoteFields, err = bindCopyForce(o.forceQuote, "FORCE_QUOTE", relation, targets, schema); err != nil {
		return err
	}
	if o.notNullFields, err = bindCopyForce(o.forceNotNull, "FORCE_NOT_NULL", relation, targets, schema); err != nil {
		return err
	}
	o.nullFields, err = bindCopyForce(o.forceNull, "FORCE_NULL", relation, targets, schema)
	return err
}
func copyForced(flags []bool, ordinal int) bool { return len(flags) != 0 && flags[ordinal] }
