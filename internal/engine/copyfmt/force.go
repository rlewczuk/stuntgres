package copyfmt

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// ResolveColumns maps a COPY column list to physical column indexes of the
// relation. An empty list selects every non-generated column.
func ResolveColumns(relation *catalog.Relation, columns []parser.Identifier) ([]int, error) {
	targets := make([]int, 0, len(relation.Columns))
	if len(columns) == 0 {
		for i, attribute := range relation.Attributes {
			if _, generated := relation.ColumnMeta.Generations[attribute]; !generated {
				targets = append(targets, i)
			}
		}
		return targets, nil
	}
	for _, name := range columns {
		target := -1
		for i, c := range relation.Columns {
			if c.Name == name.Text {
				target = i
				break
			}
		}
		if target < 0 {
			return nil, sqlError("42703", fmt.Sprintf("column \"%s\" of relation \"%s\" does not exist", name.Text, relation.Name()))
		}
		for _, prior := range targets {
			if prior == target {
				return nil, sqlError("42701", fmt.Sprintf("column \"%s\" specified more than once", name.Text))
			}
		}
		if _, generated := relation.ColumnMeta.Generations[relation.Attributes[target]]; generated {
			d := sqlError("42P10", fmt.Sprintf("column \"%s\" is a generated column", name.Text)).(*sqlstate.Error)
			d.Detail = "Generated columns cannot be used in COPY."
			return nil, d
		}
		targets = append(targets, target)
	}
	return targets, nil
}

// Force flags are COPY-field ordinals. Relation targets retain physical indexes
// for input/default assembly; query targets are its returned schema and aliases.
func bindForce(force Force, name string, relation *catalog.Relation, targets []int, schema []catalog.Column) ([]bool, error) {
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
		columns, err := ResolveColumns(relation, force.columns)
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

// BindForces resolves the FORCE options to per-field flags. Relation COPY
// passes the relation and its target indexes; query COPY passes the query's
// result schema.
func (o *Options) BindForces(relation *catalog.Relation, targets []int, schema []catalog.Column) (err error) {
	if o.QuoteFields, err = bindForce(o.ForceQuote, "FORCE_QUOTE", relation, targets, schema); err != nil {
		return err
	}
	if o.NotNullFields, err = bindForce(o.ForceNotNull, "FORCE_NOT_NULL", relation, targets, schema); err != nil {
		return err
	}
	o.NullFields, err = bindForce(o.ForceNull, "FORCE_NULL", relation, targets, schema)
	return err
}

// Forced reports whether a FORCE flag set selects field ordinal.
func Forced(flags []bool, ordinal int) bool { return len(flags) != 0 && flags[ordinal] }
