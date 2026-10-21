package catalog

import (
	"strconv"
	"unicode/utf8"

	"stuntgres/internal/engine/ids"
)

// Names are namespace-wide only when generated. Explicit names are relation-local.
func ConstraintNames(read Read, namespace ids.ObjectAddress, candidate *Relation, reader ...DefinitionReader) map[string]bool {
	names := make(map[string]bool)
	descriptors := func(yield func(ids.ObjectAddress, Descriptor) bool) {
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
		if domain, ok := descriptor.(*Type); ok && domain.ObjectNamespace == namespace {
			for _, constraint := range domain.DomainChecks {
				names[constraint.Name] = true
			}
		}
		if relation, ok := descriptor.(*Relation); ok && relation.Namespace() == namespace {
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
func RelationConstraintNameExists(d *Relation, name string) bool {
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

func GeneratedConstraintName(table, column, base string, used map[string]bool) string {
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
