package engine

import (
	"fmt"
	"strconv"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"unicode/utf8"
)

// Names are namespace-wide only when generated. Explicit names are relation-local.
func constraintNames(read CatalogRead, namespace ObjectAddress) map[string]bool {
	names := make(map[string]bool)
	for _, descriptor := range read.state.descriptors {
		if relation, ok := descriptor.(*relationDescriptor); ok && relation.namespace == namespace {
			for _, constraint := range relation.notNullConstraints {
				names[constraint.Name] = true
			}
		}
	}
	return names
}

func generatedNotNullName(table, column string, used map[string]bool) string {
	for suffix := 0; ; suffix++ {
		label := "not_null"
		if suffix != 0 {
			label += strconv.Itoa(suffix)
		}
		left, right := len(table), len(column)
		for left+right > 63-len(label)-2 {
			if left > right {
				left--
			} else {
				right--
			}
		}
		for left > 0 && left < len(table) && !utf8.RuneStart(table[left]) {
			left--
		}
		for right > 0 && right < len(column) && !utf8.RuneStart(column[right]) {
			right--
		}
		name := table[:left] + "_" + column[:right] + "_" + label
		if !used[name] {
			return name
		}
	}
}

// LIKE contributes copied identities at its original table-element position.
type createLikeConstraint struct {
	Element int
	notNullConstraint
}

func bindCreateNotNull(s *parser.CreateTable, columns []ColumnDescriptor, inherited []createLikeConstraint) ([]notNullConstraint, error) {
	table := s.Name.Parts[len(s.Name.Parts)-1].Text
	type declaration struct {
		columnNotNull bool
		columnName    string
		notNull       bool
		name          string
	}
	declarations := make([]declaration, len(columns))
	index := make(map[string]int, len(columns))
	for i, c := range columns {
		if _, exists := index[c.Name]; exists {
			return nil, sqlError("42701", fmt.Sprintf(`column "%s" specified more than once`, c.Name))
		}
		if isSystemColumn(c.Name) {
			return nil, sqlError("42701", fmt.Sprintf("column name %q conflicts with a system column name", c.Name))
		}
		index[c.Name] = i
	}
	// PostgreSQL validates each column's own list before merging table/LIKE
	// constraints. Explicit column NULL is not a table-level prohibition.
	for _, element := range s.Elements {
		if c := element.Column; c != nil {
			local := &declarations[index[c.Name]]
			seen := false
			for _, constraint := range c.Constraints {
				if constraint.Default != nil {
					continue
				}
				if seen && local.columnNotNull != constraint.NotNull {
					return nil, insertError("42601", fmt.Sprintf(`conflicting NULL/NOT NULL declarations for column "%s" of table "%s"`, c.Name, table), constraint.Pos)
				}
				name := constraint.Name.Text
				if constraint.NotNull && local.columnName != "" && name != "" && local.columnName != name {
					return nil, sqlError("XX000", fmt.Sprintf(`conflicting not-null constraint names "%s" and "%s"`, local.columnName, name))
				}
				seen, local.columnNotNull = true, constraint.NotNull
				if constraint.NotNull && name != "" {
					local.columnName = name
				}
			}
		}
	}
	// Retain first occurrence order while merging later declarations' names.
	var constraints []notNullConstraint
	merge := func(slot int, name string) error {
		old := &declarations[slot]
		if old.name != "" && name != "" && old.name != name {
			return sqlError("42601", fmt.Sprintf(`conflicting not-null constraint names "%s" and "%s"`, old.name, name))
		}
		if !old.notNull {
			constraints = append(constraints, notNullConstraint{Attribute: int32(slot + 1)})
		}
		old.notNull = true
		if name != "" {
			old.name = name
		}
		return nil
	}
	inheritedIndex := 0
	for elementIndex, element := range s.Elements {
		if c := element.Column; c != nil {
			slot := index[c.Name]
			if declarations[slot].columnNotNull {
				if err := merge(slot, declarations[slot].columnName); err != nil {
					return nil, err
				}
			}
		} else if constraint := element.NotNull; constraint != nil {
			if isSystemColumn(constraint.Column.Text) {
				return nil, sqlError("0A000", fmt.Sprintf(`cannot add not-null constraint on system column "%s"`, constraint.Column.Text))
			}
			slot, exists := index[constraint.Column.Text]
			if !exists {
				return nil, sqlError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, constraint.Column.Text, table))
			}
			if err := merge(slot, constraint.Name.Text); err != nil {
				return nil, err
			}
		} else {
			for inheritedIndex < len(inherited) && inherited[inheritedIndex].Element == elementIndex {
				constraint := inherited[inheritedIndex]
				if err := merge(int(constraint.Attribute-1), constraint.Name); err != nil {
					return nil, err
				}
				inheritedIndex++
			}
		}
	}
	for i, declaration := range declarations {
		columns[i].Nullable = !declaration.notNull
	}
	for i := range constraints {
		constraints[i].Name = declarations[constraints[i].Attribute-1].name
	}
	return constraints, nil
}

func nameCreateNotNull(d *relationDescriptor, read CatalogRead) error {
	if len(d.notNullConstraints) == 0 {
		return nil
	}
	used := constraintNames(read, d.namespace)
	// A local false entry was generated; true entries were explicitly named.
	local := make(map[string]bool, len(d.notNullConstraints))
	for i := range d.notNullConstraints {
		constraint := &d.notNullConstraints[i]
		if constraint.Name == "" {
			constraint.Name = generatedNotNullName(d.name, d.columns[constraint.Attribute-1].Name, used)
			local[constraint.Name] = false
		} else {
			if explicit, exists := local[constraint.Name]; exists {
				if explicit {
					return sqlError("42710", fmt.Sprintf(`constraint "%s" for relation "%s" already exists`, constraint.Name, d.name))
				}
				const index = "pg_constraint_conrelid_contypid_conname_index"
				diagnostic := sqlError("23505", `duplicate key value violates unique constraint "`+index+`"`).(*sqlstate.Error)
				diagnostic.Detail = fmt.Sprintf("Key (conrelid, contypid, conname)=(%d, 0, %s) already exists.", d.address.ObjectID, constraint.Name)
				diagnostic.Schema, diagnostic.Table, diagnostic.Constraint = "pg_catalog", "pg_constraint", index
				return diagnostic
			}
			local[constraint.Name] = true
		}
		used[constraint.Name] = true
	}
	return nil
}
