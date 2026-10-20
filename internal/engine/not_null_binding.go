package engine

import (
	"fmt"
	"strconv"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"unicode/utf8"
)

// Names are namespace-wide only when generated. Explicit names are relation-local.
func constraintNames(read catalog.Read, namespace ids.ObjectAddress, candidate *catalog.Relation, reader ...catalog.DefinitionReader) map[string]bool {
	names := make(map[string]bool)
	descriptors := func(yield func(ids.ObjectAddress, catalog.Descriptor) bool) {
		for address, descriptor := range read.Descriptors() {
			if !yield(address, descriptor) {
				return
			}
		}
	}
	if len(reader) != 0 && reader[0] != nil {
		descriptors = reader[0].Descriptors()
	}
	for _, descriptor := range descriptors {
		if candidate != nil && descriptor.Address() == candidate.Address() {
			descriptor = candidate
		}
		if domain, ok := descriptor.(*catalog.Type); ok && domain.ObjectNamespace == namespace {
			for _, constraint := range domain.DomainChecks {
				names[constraint.Name] = true
			}
		}
		if relation, ok := descriptor.(*catalog.Relation); ok && relation.Namespace() == namespace {
			for _, constraint := range relation.Constraints.NotNull {
				names[constraint.Name] = true
			}
			for _, constraint := range relation.Constraints.Check {
				names[constraint.Name] = true
			}
			for _, constraint := range relation.Constraints.Unique {
				names[constraint.Name] = true
			}
			for _, constraint := range relation.Constraints.Exclusion {
				names[constraint.Name] = true
			}
			for _, constraint := range relation.Constraints.ForeignKeys {
				names[constraint.Name] = true
			}
		}
	}
	return names
}

// Explicit names are relation-local and span every admitted constraint kind.
// Keep this allocation-free lookup shared by NN additions and FK attachment.
func relationConstraintNameExists(d *catalog.Relation, name string) bool {
	for _, c := range d.Constraints.NotNull {
		if c.Name == name {
			return true
		}
	}
	for _, c := range d.Constraints.Check {
		if c.Name == name {
			return true
		}
	}
	for _, c := range d.Constraints.Unique {
		if c.Name == name {
			return true
		}
	}
	for _, c := range d.Constraints.Exclusion {
		if c.Name == name {
			return true
		}
	}
	for _, c := range d.Constraints.ForeignKeys {
		if c.Name == name {
			return true
		}
	}
	return false
}

func generatedConstraintName(table, column, base string, used map[string]bool) string {
	for suffix := 0; ; suffix++ {
		label := base
		if suffix != 0 {
			label += strconv.Itoa(suffix)
		}
		left, right := len(table), len(column)
		separators := 1
		if column != "" {
			separators++
		}
		for left+right > 63-len(label)-separators {
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
		name := table[:left] + "_"
		if column != "" {
			name += column[:right] + "_"
		}
		name += label
		if !used[name] {
			return name
		}
	}
}

func nameCreateNotNull(d *catalog.Relation, read catalog.Read) error {
	if len(d.Constraints.NotNull) == 0 {
		return nil
	}
	used := constraintNames(read, d.Namespace(), nil)
	// A local false entry was generated; true entries were explicitly named.
	local := make(map[string]bool, len(d.Constraints.NotNull))
	for i := range d.Constraints.NotNull {
		constraint := &d.Constraints.NotNull[i]
		if constraint.Name == "" {
			constraint.Name = generatedConstraintName(d.Name(), d.Columns[constraint.Attribute-1].Name, "not_null", used)
			local[constraint.Name] = false
		} else {
			if explicit, exists := local[constraint.Name]; exists {
				if explicit {
					return sqlError("42710", fmt.Sprintf(`constraint "%s" for relation "%s" already exists`, constraint.Name, d.Name()))
				}
				const index = "pg_constraint_conrelid_contypid_conname_index"
				diagnostic := sqlError("23505", `duplicate key value violates unique constraint "`+index+`"`).(*sqlstate.Error)
				diagnostic.Detail = fmt.Sprintf("Key (conrelid, contypid, conname)=(%d, 0, %s) already exists.", d.Address().ObjectID, constraint.Name)
				diagnostic.Schema, diagnostic.Table, diagnostic.Constraint = "pg_catalog", "pg_constraint", index
				return diagnostic
			}
			local[constraint.Name] = true
		}
		used[constraint.Name] = true
	}
	return nil
}
